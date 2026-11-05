package flagdgrpc

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/metadata"
)

func source(settings *sync.GRPCSourceSettings) sync.SourceConfig {
	return sync.SourceConfig{URI: "up:8015", Provider: Provider, GRPC: settings}
}

func TestSettingsValidation(t *testing.T) {
	for name, tc := range map[string]struct {
		source  sync.SourceConfig
		message string
	}{
		"an unknown code name":       {source(&sync.GRPCSourceSettings{FatalStatusCodes: []string{"PERMISSION_DENIED", "NOPE"}}), "fatalStatusCodes"},
		"OK as a fatal code":         {source(&sync.GRPCSourceSettings{FatalStatusCodes: []string{"OK"}}), "fatalStatusCodes"},
		"a lower-case code name":     {source(&sync.GRPCSourceSettings{FatalStatusCodes: []string{"unavailable"}}), "fatalStatusCodes"},
		"a negative retry delay":     {source(&sync.GRPCSourceSettings{RetryBackoffMs: -1}), "retryBackoffMs"},
		"a negative largest delay":   {source(&sync.GRPCSourceSettings{RetryBackoffMaxMs: -1}), "retryBackoffMaxMs"},
		"a negative stream deadline": {source(&sync.GRPCSourceSettings{StreamDeadlineMs: -1}), "streamDeadlineMs"},
		"a negative keepalive":       {source(&sync.GRPCSourceSettings{KeepAliveTime: -1}), "keepAliveTime"},
		"the initial above the largest": {
			source(&sync.GRPCSourceSettings{RetryBackoffMs: 6000, RetryBackoffMaxMs: 5000}), "retryBackoffMs"},
		"the initial above the default largest": {source(&sync.GRPCSourceSettings{RetryBackoffMs: 6000}), "retryBackoffMs"},
		"keepalive below 10 s":                  {source(&sync.GRPCSourceSettings{KeepAliveTime: 9999}), "keepAliveTime"},
		"keepalive of 1 ms":                     {source(&sync.GRPCSourceSettings{KeepAliveTime: 1}), "keepAliveTime"},
		"the selector header and a header": {sync.SourceConfig{
			URI: "up:8015", Provider: Provider, Headers: map[string]string{"Flagd-Selector": "x"},
			GRPC: &sync.GRPCSourceSettings{SelectorHeader: true},
		}, "selectorHeader"},
		"the selector header and a header from": {sync.SourceConfig{
			URI: "up:8015", Provider: Provider, HeadersFrom: map[string]sync.HeaderSource{"flagd-selector": {Env: "SELECTOR"}},
			GRPC: &sync.GRPCSourceSettings{SelectorHeader: true},
		}, "selectorHeader"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := OptionsFrom(tc.source)
			require.Error(t, err)
			require.Contains(t, err.Error(), `source "up:8015"`, "the error names the source")
			require.Contains(t, err.Error(), "grpc."+tc.message, "the error names the setting")

			// the factory stops startup with the same error
			_, err = Factory{}.CreateStreaming(sync.Settings{Source: tc.source, Logger: logger.NewLogger(nil, false)})
			require.Error(t, err)
		})
	}

	t.Run("valid settings are read", func(t *testing.T) {
		opts, err := OptionsFrom(source(&sync.GRPCSourceSettings{
			RetryBackoffMs: 1000, RetryBackoffMaxMs: 5000, StreamDeadlineMs: 600000, KeepAliveTime: 10000,
			FatalStatusCodes: []string{"PERMISSION_DENIED", "UNAUTHENTICATED"}, SelectorHeader: true, ReadyOnFirstPayload: true,
		}))
		require.NoError(t, err)
		require.Equal(t, Options{
			RetryBackoff: time.Second, RetryBackoffMax: 5 * time.Second, StreamDeadline: 10 * time.Minute,
			KeepAliveTime: 10 * time.Second, FatalStatusCodes: []codes.Code{codes.PermissionDenied, codes.Unauthenticated},
			SelectorHeader: true, ReadyOnFirstPayload: true,
		}, opts)
	})

	t.Run("a single delay is checked against the provider's default for the other", func(t *testing.T) {
		_, err := OptionsFrom(source(&sync.GRPCSourceSettings{RetryBackoffMs: 2000}))
		require.NoError(t, err)
		_, err = OptionsFrom(source(&sync.GRPCSourceSettings{RetryBackoffMaxMs: 500}))
		require.ErrorContains(t, err, "retryBackoffMs", "the default initial delay of 1000 ms is above 500 ms")
	})
}

// TestEmptyGRPCObjectChangesNothing: with `grpc: {}` the source is built and behaves as without the object.
func TestEmptyGRPCObjectChangesNothing(t *testing.T) {
	build := func(object *sync.GRPCSourceSettings, uri string) *Sync {
		provider, err := Factory{}.CreateStreaming(sync.Settings{
			Source: sync.SourceConfig{
				URI: uri, Provider: Provider, Selector: "source=orders", ProviderID: "provider-1",
				Headers: map[string]string{"x-tenant-id": "tenant1"}, GRPC: object,
			},
			Logger: logger.NewLogger(nil, false),
		})
		require.NoError(t, err)
		return provider.(*Sync)
	}

	srv := &recordingServer{}
	uri := loopback(t, srv)
	without, with := build(nil, uri), build(&sync.GRPCSourceSettings{}, uri)
	require.Equal(t, without.Options, with.Options)
	require.Equal(t, Options{}, with.Options)

	var dials [2]int
	for i, s := range []*Sync{without, with} {
		s.dialRecorder = func(opts []grpc.DialOption) { dials[i] = len(opts) }
		require.NoError(t, s.Init(context.Background()))
	}
	require.Equal(t, dials[0], dials[1], "the same dial options")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	data := make(chan sync.DataSync, 4)
	go func() { _ = with.Sync(ctx, data) }()
	select {
	case <-data:
	case <-time.After(10 * time.Second):
		t.Fatal("no payload from SyncFlags")
	}
	require.NoError(t, with.ReSync(ctx, data))
	srv.mu.Lock()
	defer srv.mu.Unlock()
	for name, md := range map[string]metadata.MD{"SyncFlags": srv.syncMetadata, "FetchAllFlags": srv.fetchMetadata} {
		require.NotContains(t, md, "flagd-selector", name)
		require.Equal(t, []string{"tenant1"}, md.Get("x-tenant-id"), name)
	}
	require.False(t, srv.syncDeadline)
	require.False(t, srv.fetchDeadline)
}

func TestHeadersFromResolved(t *testing.T) {
	t.Setenv("TEST_UPSTREAM_TOKEN", "secret-value")
	srv := &recordingServer{}
	provider, err := Factory{}.CreateStreaming(sync.Settings{
		Source: sync.SourceConfig{
			URI: loopback(t, srv), Provider: Provider,
			Headers:     map[string]string{"x-tenant-id": "tenant1", "x-token": "overridden"},
			HeadersFrom: map[string]sync.HeaderSource{"x-token": {Env: "TEST_UPSTREAM_TOKEN"}},
		},
		Logger: logger.NewLogger(nil, false),
	})
	require.NoError(t, err)
	s := provider.(*Sync)
	require.NoError(t, s.Init(context.Background()))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	data := make(chan sync.DataSync, 4)
	go func() { _ = s.Sync(ctx, data) }()
	select {
	case <-data:
	case <-time.After(10 * time.Second):
		t.Fatal("no payload from SyncFlags")
	}
	require.NoError(t, s.ReSync(ctx, data))
	srv.mu.Lock()
	defer srv.mu.Unlock()
	for name, md := range map[string]metadata.MD{"SyncFlags": srv.syncMetadata, "FetchAllFlags": srv.fetchMetadata} {
		require.Equal(t, []string{"secret-value"}, md.Get("x-token"), name)
		require.Equal(t, []string{"tenant1"}, md.Get("x-tenant-id"), name)
	}

	t.Run("an unreadable source stops startup without the value", func(t *testing.T) {
		_, err := Factory{}.CreateStreaming(sync.Settings{
			Source: sync.SourceConfig{
				URI: "up:8015", Provider: Provider,
				HeadersFrom: map[string]sync.HeaderSource{"x-token": {Env: "TEST_UPSTREAM_TOKEN_NOT_SET"}},
			},
			Logger: logger.NewLogger(nil, false),
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "up:8015")
		require.Contains(t, err.Error(), "x-token")
	})
}

// openFDs counts the open file descriptors of the process; it is 0 where /proc is not available.
func openFDs(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return 0
	}
	return len(entries)
}

// TestConnectionClosedWhenSyncReturns: the client connection does not outlive the sync provider, so a process that
// builds several runtimes does not leak one connection per source.
func TestConnectionClosedWhenSyncReturns(t *testing.T) {
	uri := loopback(t, &recordingServer{})

	cycle := func() connectivity.State {
		s := defaultSync(uri)
		require.NoError(t, s.Init(context.Background()))
		conn := s.conn
		ctx, cancel := context.WithCancel(context.Background())
		data := make(chan sync.DataSync, 4)
		done := make(chan error, 1)
		go func() { done <- s.Sync(ctx, data) }()
		select {
		case <-data:
		case <-time.After(10 * time.Second):
			t.Fatal("no payload from SyncFlags")
		}
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("Sync did not return")
		}
		return conn.GetState()
	}

	require.Equal(t, connectivity.Shutdown, cycle())

	before := openFDs(t)
	for range 20 {
		require.Equal(t, connectivity.Shutdown, cycle())
	}
	if before > 0 {
		require.Eventually(t, func() bool { return openFDs(t) <= before+3 }, 5*time.Second, 50*time.Millisecond,
			"the file descriptors do not grow by one connection per cycle")
	}
}
