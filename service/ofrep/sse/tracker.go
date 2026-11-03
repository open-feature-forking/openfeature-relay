package sse

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/launchdarkly/eventsource"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"go.uber.org/zap"
)

type version struct {
	configEtag   string
	lastModified int64
}

// subscription is shared by every client on the same channel. It is reachable through Tracker.subs only while
// refs > 0.
type subscription struct {
	selector store.Selector // immutable after construction

	// ver is the version of the documents behind the selector.
	ver atomic.Pointer[version]

	refs int // guarded by Tracker.mu
}

// Tracker keeps the version of the documents behind each channel's selector, and announces a change to the
// clients of the channel when a configuration change event concerns it. The version is derived from the document
// versions of every configuration format, not from the flags, so a change in a vendor's format is announced too.
type Tracker struct {
	logger  *logger.Logger
	store   *store.Store
	formats []string
	es      *eventsource.Server
	changes *events.Subscription

	mu     sync.Mutex
	subs   map[string]*subscription
	closed bool

	wg sync.WaitGroup
}

// NewTracker returns a tracker over the documents of the given configuration formats. It follows the
// configuration change events of bus; without a bus it never announces a change.
func NewTracker(log *logger.Logger, s *store.Store, es *eventsource.Server, bus *events.Bus, formats []string) *Tracker {
	t := &Tracker{
		logger:  log,
		store:   s,
		formats: formats,
		es:      es,
		subs:    map[string]*subscription{},
	}
	if bus != nil {
		t.changes = bus.Subscribe(events.SubscribeOptions{
			Kinds: []events.Kind{events.KindConfigurationChange}, Buffer: 256,
		})
		t.wg.Add(1)
		go t.follow()
	}
	return t
}

// Subscribe registers interest in a channel. The returned release must be called exactly once on disconnect.
func (t *Tracker) Subscribe(channel string, selector store.Selector) (func(), error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil, fmt.Errorf("ofrep sse tracker is shutting down")
	}

	sub, existing := t.subs[channel]
	if !existing {
		sub = &subscription{selector: selector}
		// seeded under the lock, so that a change event is either handled for this subscription or already in the
		// documents the seed reads
		sub.ver.Store(&version{configEtag: t.etag(&sub.selector), lastModified: time.Now().Unix()})
		t.subs[channel] = sub
	}
	sub.refs++

	var once sync.Once
	return func() { once.Do(func() { t.release(channel, sub) }) }, nil
}

// release drops one reference, forgetting the subscription when the last one goes.
func (t *Tracker) release(channel string, sub *subscription) {
	t.mu.Lock()
	defer t.mu.Unlock()
	sub.refs--
	if sub.refs <= 0 && t.subs[channel] == sub {
		delete(t.subs, channel)
	}
}

// follow handles the configuration change events until the bus closes or the tracker does.
func (t *Tracker) follow() {
	defer t.wg.Done()
	for ev := range t.changes.C() {
		t.handle(ev.(events.ConfigurationChange))
	}
}

// handle recomputes the version of each channel the event concerns. An event that reports missed events
// concerns every channel: the tracker cannot tell what it missed, so it reads the documents again.
func (t *Tracker) handle(change events.ConfigurationChange) {
	t.mu.Lock()
	subs := make(map[string]*subscription, len(t.subs))
	for channel, sub := range t.subs {
		subs[channel] = sub
	}
	t.mu.Unlock()

	for channel, sub := range subs {
		if change.Missed == 0 && !concerns(&sub.selector, change) {
			continue
		}
		etag := t.etag(&sub.selector)
		prev := sub.ver.Load()
		if prev != nil && prev.configEtag == etag {
			continue
		}
		// stored before the event is published, so a client that refetches the instant it receives it cannot read the
		// previous ETag and be served a stale 304
		sub.ver.Store(&version{configEtag: etag, lastModified: time.Now().Unix()})
		t.publish(channel, sub)
	}
}

// concerns reports whether a change is to a document behind the selector.
func concerns(selector *store.Selector, change events.ConfigurationChange) bool {
	if source := selector.Source(); source != "" && source != change.Source {
		return false
	}
	if flagSet, named := selector.DocumentFlagSet(); named && flagSet != change.FlagSet {
		return false
	}
	return true
}

// etag is a hash of the (format, source, flag set, version) tuples of the documents behind the selector, scoped
// by the selector, so that two channels never share an ETag unless they come from the same selector.
func (t *Tracker) etag(selector *store.Selector) string {
	flagSet, named := selector.DocumentFlagSet()
	onlySource := selector.Source()
	view := t.store.View()

	var tuples []string
	for _, source := range t.store.Sources() {
		if onlySource != "" && source != onlySource {
			continue
		}
		for _, format := range t.formats {
			for _, d := range view.Documents(format, source) {
				if named && d.FlagSet != flagSet {
					continue
				}
				tuples = append(tuples, strings.Join([]string{d.Format, d.Source, d.FlagSet, d.Version}, "\x00"))
			}
		}
	}
	sort.Strings(tuples)

	h := sha256.New()
	h.Write([]byte(selector.ToLogString()))
	h.Write([]byte{0})
	for _, tuple := range tuples {
		h.Write([]byte(tuple))
		h.Write([]byte{1})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// publish must be called only after the new version is stored.
func (t *Tracker) publish(channel string, sub *subscription) {
	v := sub.ver.Load()
	if v == nil {
		return
	}

	id := strconv.FormatInt(time.Now().UnixMilli(), 10)
	t.es.Publish([]string{channel}, newRefetchEvent(id, v.configEtag, v.lastModified))
	if t.logger != nil {
		t.logger.Debug("published refetch event", zap.String("channel", channel), zap.String("etag", v.configEtag))
	}
}

// Version returns the current config ETag and last-modified time (unix seconds) for the channel
// matching the given selector expression. ok is false when no stream for it is live, so the
// caller skips conditional handling.
func (t *Tracker) Version(channel string) (configEtag string, lastModified int64, ok bool) {
	t.mu.Lock()
	sub, exists := t.subs[channel]
	t.mu.Unlock()

	if !exists {
		return "", 0, false
	}
	v := sub.ver.Load()
	if v == nil {
		return "", 0, false
	}
	return v.configEtag, v.lastModified, true
}

// Channels returns the channels with at least one live subscriber.
func (t *Tracker) Channels() []string {
	t.mu.Lock()
	defer t.mu.Unlock()

	channels := make([]string, 0, len(t.subs))
	for channel := range t.subs {
		channels = append(channels, channel)
	}
	return channels
}

// Close stops accepting subscriptions and waits for the event loop to exit. It must complete before the
// eventsource server is closed, since a publish after that blocks forever.
func (t *Tracker) Close() {
	t.mu.Lock()
	t.closed = true
	t.subs = map[string]*subscription{}
	t.mu.Unlock()

	if t.changes != nil {
		t.changes.Close()
	}
	t.wg.Wait()
}
