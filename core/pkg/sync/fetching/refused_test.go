package fetching

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
)

func refusing() fetcherFunc {
	return func(context.Context, string) (sync.FetchResult, error) {
		return sync.FetchResult{}, fmt.Errorf("the source answered 401: %w", sync.ErrSourceRefused)
	}
}

// A source that refuses the very first request has credentials that will never work: the provider ends, and the
// error names the source.
func TestRefusedBeforeFirstDeliveryEndsProvider(t *testing.T) {
	fetcher := &closingFetcher{fetcherFunc: refusing()}
	trigger := newManualTrigger()
	r := start(t, Options{Fetcher: fetcher, Triggers: []sync.Trigger{trigger}})

	err := r.result(t)
	require.ErrorIs(t, err, sync.ErrSourceRefused)
	require.ErrorContains(t, err, source)
	r.none(t)
	require.False(t, r.provider.IsReady())
	require.Equal(t, int32(1), fetcher.closed.Load(), "the fetcher is closed")
	require.Equal(t, int32(1), trigger.runs.Load(), "the triggers were stopped, not restarted")
}

// A source that has delivered once and then refuses may have a rotated key or a transient firewall: the provider keeps
// serving and tries again.
func TestRefusedAfterDeliveryKeepsServing(t *testing.T) {
	var calls atomic.Int32
	fetcher := fetcherFunc(func(context.Context, string) (sync.FetchResult, error) {
		switch n := calls.Add(1); n {
		case 1:
			return result("v1", flagsV1), nil
		case 2:
			return sync.FetchResult{}, fmt.Errorf("the source answered 403: %w", sync.ErrSourceRefused)
		default:
			return result("v2", flagsV2), nil
		}
	})
	trigger := newManualTrigger()
	r := start(t, Options{Fetcher: fetcher, Triggers: []sync.Trigger{trigger}})
	require.JSONEq(t, flagsV1, r.next(t).FlagData)

	signal := trigger.signal(t)
	signal()
	require.Eventually(t, func() bool { return calls.Load() == 2 }, timeout, time.Millisecond)
	r.none(t)
	select {
	case err := <-r.done:
		t.Fatalf("the provider ended on a refusal after its first delivery: %v", err)
	default:
	}
	require.True(t, r.provider.IsReady())

	signal()
	require.JSONEq(t, flagsV2, r.next(t).FlagData, "the provider fetches again and delivers")
}

// Only ErrSourceRefused ends the provider before its first delivery; any other error keeps it trying.
func TestOtherErrorsBeforeFirstDeliveryAreRetried(t *testing.T) {
	var calls atomic.Int32
	fetcher := fetcherFunc(func(context.Context, string) (sync.FetchResult, error) {
		if calls.Add(1) < 3 {
			return sync.FetchResult{}, fmt.Errorf("the source answered 500")
		}
		return result("v1", flagsV1), nil
	})
	r := start(t, Options{Fetcher: fetcher})
	require.JSONEq(t, flagsV1, r.next(t).FlagData)
}

// A delivery carries the source version it was fetched at, so that the runtime can say which version it has accepted.
func TestDeliveryCarriesTheSourceVersion(t *testing.T) {
	var calls atomic.Int32
	trigger := newManualTrigger()
	r := start(t, Options{Fetcher: versionedFetcher(&calls), Triggers: []sync.Trigger{trigger}})
	require.Equal(t, "v1", r.next(t).Version)

	trigger.signal(t)()
	require.Equal(t, "v2", r.next(t).Version)

	require.NoError(t, r.provider.ReSync(context.Background(), r.deliveries))
	require.Equal(t, "v3", r.next(t).Version, "a forced fetch delivers the version it was fetched at")
}
