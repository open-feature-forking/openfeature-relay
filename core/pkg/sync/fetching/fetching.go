// Package fetching is the core's fetching sync provider: a streaming sync provider built from one fetcher and the
// fetch triggers attached to its source.
package fetching

import (
	"context"
	"fmt"
	"io"
	gosync "sync"
	"sync/atomic"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/polling"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/utils"
	"go.uber.org/zap"
)

const (
	// DefaultInterval is the base of the retry backoff of a source that configures no interval, in seconds.
	DefaultInterval = polling.DefaultInterval

	// minDeadline is the shortest default deadline of a fetch.
	minDeadline = 30 * time.Second

	// healthyRun is how long a fetch trigger has to run before a later failure counts as the first again.
	healthyRun = time.Minute
)

// Options configure a Provider.
type Options struct {
	Fetcher  sync.Fetcher
	Triggers []sync.Trigger
	Logger   *logger.Logger
	// Source is the source every delivery carries: the configured URI.
	Source string
	// SourceType is the type recorded in the sync metrics.
	SourceType string
	Metrics    sync.MetricsRecorder
	// Interval is the base of the retry backoff; zero means DefaultInterval seconds.
	Interval time.Duration
	// Timeout is the deadline of a fetch; zero means the greater of Interval and 30 seconds.
	Timeout time.Duration
	// IncrementalUpdates marks deliveries as incremental updates (BR-007).
	IncrementalUpdates bool
}

// Provider is a sync.StreamingSyncProvider that fetches a source on request.
type Provider struct {
	opts    Options
	backoff polling.Backoff
	timeout time.Duration

	fetchMu gosync.Mutex // one fetch at a time
	version string       // the source version of the last accepted fetch; guarded by fetchMu
	pending chan struct{}
	ready   atomic.Bool
}

var _ sync.StreamingSyncProvider = (*Provider)(nil)

// New creates a Provider.
func New(opts Options) *Provider {
	interval := opts.Interval
	if interval <= 0 {
		interval = time.Duration(DefaultInterval) * time.Second
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = max(interval, minDeadline)
	}
	opts.Logger = opts.Logger.WithFields(zap.String("component", "sync"), zap.String("sync", opts.SourceType))
	return &Provider{
		opts:    opts,
		backoff: polling.Backoff{Interval: interval},
		timeout: timeout,
		pending: make(chan struct{}, 1),
	}
}

// Init initializes the fetcher and the triggers, if they need it.
func (p *Provider) Init(ctx context.Context) error {
	if f, ok := p.opts.Fetcher.(sync.InitializingFetcher); ok {
		if err := f.Init(ctx); err != nil {
			return fmt.Errorf("error initializing the fetcher of %s: %w", p.opts.Source, err)
		}
	}
	for _, trigger := range p.opts.Triggers {
		if t, ok := trigger.(sync.InitializingTrigger); ok {
			if err := t.Init(ctx); err != nil {
				return fmt.Errorf("error initializing a fetch trigger of %s: %w", p.opts.Source, err)
			}
		}
	}
	return nil
}

// IsReady reports whether the first fetch has succeeded.
func (p *Provider) IsReady() bool {
	return p.ready.Load()
}

// ReSync fetches the full flag configuration and always delivers it.
func (p *Provider) ReSync(ctx context.Context, dataSync chan<- sync.DataSync) error {
	return p.fetch(ctx, dataSync, true)
}

// Sync fetches the source once, then fetches again each time one of the triggers signals, until ctx is done.
func (p *Provider) Sync(ctx context.Context, dataSync chan<- sync.DataSync) error {
	p.opts.Logger.Info(fmt.Sprintf("starting sync from %s", p.opts.Source))

	// The triggers start before the first fetch, so that no change falls between that fetch and the moment they
	// watch the source. What they signal meanwhile is handled after the first fetch.
	triggerCtx, cancelTriggers := context.WithCancel(ctx)
	defer cancelTriggers()
	var running gosync.WaitGroup
	for _, trigger := range p.opts.Triggers {
		running.Add(1)
		go func() {
			defer running.Done()
			p.runTrigger(triggerCtx, trigger)
		}()
	}
	// the triggers stop with the context, or with the provider when it stops for another reason
	stopTriggers := func() {
		cancelTriggers()
		running.Wait()
	}

	p.firstFetch(ctx, dataSync)
	if ctx.Err() != nil {
		stopTriggers()
		p.closeFetcher()
		return nil
	}
	p.ready.Store(true)

loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-p.pending:
			if err := p.fetch(ctx, dataSync, false); err != nil && ctx.Err() == nil {
				p.opts.Logger.Warn(fmt.Sprintf("fetching %s failed: %v", p.opts.Source, err))
			}
		}
	}

	stopTriggers()
	p.closeFetcher()
	return nil
}

func (p *Provider) closeFetcher() {
	if closer, ok := p.opts.Fetcher.(io.Closer); ok {
		if err := closer.Close(); err != nil {
			p.opts.Logger.Warn(fmt.Sprintf("closing the fetcher of %s failed: %v", p.opts.Source, err))
		}
	}
}

// firstFetch fetches until it succeeds, waiting with a backoff in between, unless ctx is done.
func (p *Provider) firstFetch(ctx context.Context, dataSync chan<- sync.DataSync) {
	p.opts.Logger.Debug(fmt.Sprintf("initial fetch from %s", p.opts.Source))
	for failures := 1; ; failures++ {
		err := p.fetch(ctx, dataSync, false)
		if err == nil || ctx.Err() != nil {
			return
		}
		wait := p.backoff.Retry(failures)
		p.opts.Logger.Warn(fmt.Sprintf("the initial fetch from %s failed, retrying in %s: %v", p.opts.Source, wait, err))
		if !sleep(ctx, wait) {
			return
		}
	}
}

// runTrigger runs a trigger until ctx is done, running it again after a backoff when it fails.
func (p *Provider) runTrigger(ctx context.Context, trigger sync.Trigger) {
	signal := func() {
		select {
		case p.pending <- struct{}{}:
		default:
		}
	}
	failures := 0
	for {
		started := time.Now()
		err := trigger.Run(ctx, signal)
		if ctx.Err() != nil || err == nil {
			return
		}
		if time.Since(started) >= healthyRun {
			failures = 0
		}
		failures++
		wait := p.backoff.Retry(failures)
		p.opts.Logger.Warn(fmt.Sprintf("a fetch trigger of %s failed, running it again in %s: %v", p.opts.Source, wait, err))
		if !sleep(ctx, wait) {
			return
		}
	}
}

// fetch fetches the source once, under the lock that allows one fetch at a time, and delivers the result unless it
// is unchanged. With force, it asks for the full flag configuration and always delivers it.
func (p *Provider) fetch(ctx context.Context, dataSync chan<- sync.DataSync, force bool) error {
	p.fetchMu.Lock()
	defer p.fetchMu.Unlock()

	err := p.fetchLocked(ctx, dataSync, force)
	if ctx.Err() == nil {
		for _, trigger := range p.opts.Triggers {
			if observer, ok := trigger.(sync.OutcomeObserver); ok {
				observer.Observe(err)
			}
		}
	}
	return err
}

func (p *Provider) fetchLocked(ctx context.Context, dataSync chan<- sync.DataSync, force bool) error {
	previous := p.version
	if force {
		previous = ""
	}

	fetchCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	result, err := p.opts.Fetcher.Fetch(fetchCtx, previous)
	if err != nil {
		return fmt.Errorf("error fetching %s: %w", p.opts.Source, err)
	}

	if result.Unchanged {
		if result.Version != "" && !force {
			p.version = result.Version
		}
		p.opts.Logger.Debug(fmt.Sprintf("%s has not changed", p.opts.Source))
		return nil
	}
	if !force && previous != "" && result.Version == previous {
		p.opts.Logger.Debug(fmt.Sprintf("%s has the version it had", p.opts.Source))
		return nil
	}

	flags := "{}"
	if len(result.Data) > 0 {
		flags, err = utils.ConvertToJSON(result.Data, result.Extension, result.ContentType)
		if err != nil {
			return fmt.Errorf("error decoding %s: %w", p.opts.Source, err)
		}
	}

	if err := p.deliver(ctx, dataSync, flags); err != nil {
		return err
	}
	p.version = result.Version
	return nil
}

// deliver sends a flag configuration, unless ctx is done first.
func (p *Provider) deliver(ctx context.Context, dataSync chan<- sync.DataSync, flags string) error {
	update := sync.DataSync{
		FlagData:           flags,
		Source:             p.opts.Source,
		IncrementalUpdates: p.opts.IncrementalUpdates,
	}
	select {
	case dataSync <- update:
	case <-ctx.Done():
		return ctx.Err()
	}
	sync.RecordFlagConfigReceived(ctx, p.opts.Metrics, p.opts.SourceType, p.opts.Source, "")
	return nil
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
