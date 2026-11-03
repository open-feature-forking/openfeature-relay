package syncexample

import (
	"os"
	gosync "sync"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/synctest"
	"github.com/stretchr/testify/require"
)

const (
	flagsA = `{"flags":{"a":{}}}`
	flagsB = `{"flags":{"b":{}}}`
)

func TestFetcherContract(t *testing.T) {
	source := NewSource(flagsA)
	synctest.RunFetcher(t, Factory{Source: source}, synctest.FetcherInputs{
		Source: sync.SourceConfig{URI: "example://flags", Provider: Type},
		Change: func(testing.TB) { source.Set(flagsB) },
		Break:  func(testing.TB) { source.Fail(true) },
	})
}

// hangups stands in for the process's SIGHUP, so that a subscription can be lost.
type hangups struct {
	mu   gosync.Mutex
	subs []chan os.Signal
}

func (h *hangups) subscribe() (<-chan os.Signal, func()) {
	c := make(chan os.Signal, 8)
	h.mu.Lock()
	h.subs = append(h.subs, c)
	h.mu.Unlock()
	return c, func() { h.drop(c, false) }
}

func (h *hangups) drop(c chan os.Signal, closeIt bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, s := range h.subs {
		if s == c {
			h.subs = append(h.subs[:i], h.subs[i+1:]...)
			if closeIt {
				close(c)
			}
			return
		}
	}
}

func (h *hangups) hangup(testing.TB) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.subs {
		select {
		case c <- os.Interrupt:
		default:
		}
	}
}

func (h *hangups) lose(testing.TB) {
	h.mu.Lock()
	var latest chan os.Signal
	if n := len(h.subs); n > 0 {
		latest = h.subs[n-1]
	}
	h.mu.Unlock()
	if latest != nil {
		h.drop(latest, true)
	}
}

func TestTriggerContract(t *testing.T) {
	h := &hangups{}
	synctest.RunTrigger(t, TriggerFactory{Subscribe: h.subscribe}, synctest.TriggerInputs{
		Source:    sync.SourceConfig{URI: "example://flags", Provider: Type},
		Signal:    h.hangup,
		Interrupt: h.lose,
	})
}

func TestFactoryNeedsASource(t *testing.T) {
	_, err := Factory{}.CreateFetcher(sync.Settings{})
	require.EqualError(t, err, "the example fetcher needs a source")
}
