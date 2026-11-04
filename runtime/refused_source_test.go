package runtime

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/fetching"
	"github.com/stretchr/testify/require"
)

type refusedFetcher struct{}

func (refusedFetcher) Fetch(context.Context, string) (coresync.FetchResult, error) {
	return coresync.FetchResult{}, fmt.Errorf("the source answered 401: %w", coresync.ErrSourceRefused)
}

// A distribution whose source refuses its credentials from the first request does not start serving: the runtime
// returns an error that names the source, and the process exits non-zero.
func TestRuntimeExitsOnRefusedSource(t *testing.T) {
	const source = "https://flags.example/env-1"
	log := logger.NewLogger(nil, false)
	provider := fetching.New(fetching.Options{
		Fetcher:    refusedFetcher{},
		Logger:     log,
		Source:     source,
		SourceType: coresync.SourceHTTP,
		Interval:   5 * time.Millisecond,
	})
	rt := &Runtime{
		Logger:          log,
		DefaultFormat:   nopFormat{},
		Syncs:           []coresync.ISync{provider},
		Endpoints:       []NamedEndpoint{{Type: "blocking", Endpoint: blockingEndpoint{stop: make(chan struct{})}}},
		sourceReadiness: map[string]bool{source: false},
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- rt.start(ctx) }()

	select {
	case err := <-done:
		require.ErrorIs(t, err, coresync.ErrSourceRefused)
		require.ErrorContains(t, err, source)
	case <-time.After(10 * time.Second):
		t.Fatal("the runtime kept running with a source that refuses its credentials")
	}
}
