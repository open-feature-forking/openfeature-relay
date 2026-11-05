package flagdgrpc

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	syncv1grpc "buf.build/gen/go/open-feature/flagd/grpc/go/flagd/sync/v1/syncv1grpc"
	v1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/sync/v1"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
)

// TestRetryServiceConfigJSON pins the service config to the one the Go flagd provider v0.8.0 builds for the same
// delays: four attempts on UNKNOWN and UNAVAILABLE, doubling, with the delays in seconds and a missing one filled by the
// provider's default.
func TestRetryServiceConfigJSON(t *testing.T) {
	config := func(initial, largest string) string {
		return `{"methodConfig":[{"name":[{"service":"flagd.sync.v1.FlagSyncService"}],"retryPolicy":` +
			`{"MaxAttempts":4,"InitialBackoff":"` + initial + `","MaxBackoff":"` + largest +
			`","BackoffMultiplier":2,"RetryableStatusCodes":["UNKNOWN","UNAVAILABLE"]}}]}`
	}
	tests := map[string]struct {
		initial, largest time.Duration
		want             string
	}{
		"both set":                   {2 * time.Second, 30 * time.Second, config("2s", "30s")},
		"fractional seconds":         {100 * time.Millisecond, 1500 * time.Millisecond, config("0.1s", "1.5s")},
		"only the largest is set":    {0, 3 * time.Second, config("1s", "3s")},
		"only the initial is set":    {250 * time.Millisecond, 0, config("0.25s", "5s")},
		"minutes stay in seconds":    {time.Second, 2 * time.Minute, config("1s", "120s")},
		"the provider's defaults":    {defaultRetryBackoff, defaultRetryBackoffMax, config("1s", "5s")},
		"nothing set takes defaults": {0, 0, config("1s", "5s")},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			require.JSONEq(t, tt.want, retryServiceConfig(tt.initial, tt.largest))
			require.Equal(t, tt.want, retryServiceConfig(tt.initial, tt.largest), "byte for byte, as the provider writes it")
		})
	}
}

func TestKeepaliveParameters(t *testing.T) {
	t.Run("unset and zero send no parameters", func(t *testing.T) {
		_, ok := Options{}.keepAliveParameters()
		require.False(t, ok)
		_, ok = Options{KeepAliveTime: 0}.keepAliveParameters()
		require.False(t, ok)
	})
	t.Run("ten seconds and more set the provider's parameters", func(t *testing.T) {
		for _, interval := range []time.Duration{10 * time.Second, 30 * time.Second, 5 * time.Minute} {
			params, ok := Options{KeepAliveTime: interval}.keepAliveParameters()
			require.True(t, ok)
			require.Equal(t, keepalive.ClientParameters{Time: interval, Timeout: 5 * time.Second, PermitWithoutStream: true}, params)
		}
	})
	t.Run("the dial carries them", func(t *testing.T) {
		s := defaultSync("host:8013")
		s.Options = Options{KeepAliveTime: 30 * time.Second}
		var got []grpc.DialOption
		s.dialRecorder = func(opts []grpc.DialOption) { got = opts }
		require.NoError(t, s.Init(context.Background()))
		require.Len(t, got, 2, "the transport credentials and the keepalive parameters")
	})
}

func TestRetryPolicyReachesTheDial(t *testing.T) {
	for name, opts := range map[string]Options{
		"retryBackoffMs only":    {RetryBackoff: 100 * time.Millisecond},
		"retryBackoffMaxMs only": {RetryBackoffMax: time.Second},
		"both":                   {RetryBackoff: 100 * time.Millisecond, RetryBackoffMax: time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			s := defaultSync("host:8013")
			s.Options = opts
			var got []grpc.DialOption
			s.dialRecorder = func(o []grpc.DialOption) { got = o }
			require.NoError(t, s.Init(context.Background()))
			require.Len(t, got, 2, "the transport credentials and the service config")
		})
	}
	t.Run("settings that are not the retry policy add none", func(t *testing.T) {
		s := defaultSync("host:8013")
		s.Options = Options{StreamDeadline: time.Minute, FatalStatusCodes: []codes.Code{codes.PermissionDenied}, ReadyOnFirstPayload: true}
		var got []grpc.DialOption
		s.dialRecorder = func(o []grpc.DialOption) { got = o }
		require.NoError(t, s.Init(context.Background()))
		require.Len(t, got, 1, "the transport credentials")
	})
}

// failingServer answers SyncFlags with UNAVAILABLE for its first failures calls, then sends one payload.
type failingServer struct {
	syncv1grpc.UnimplementedFlagSyncServiceServer
	failures int32
	calls    atomic.Int32
}

func (s *failingServer) SyncFlags(_ *v1.SyncFlagsRequest, stream syncv1grpc.FlagSyncService_SyncFlagsServer) error {
	if s.calls.Add(1) <= s.failures {
		return status.Error(codes.Unavailable, "not yet")
	}
	return stream.Send(&v1.SyncFlagsResponse{FlagConfiguration: "{}"})
}

// TestRetryServiceConfigRetriesUnavailable shows the service config at work: with the retry policy the connection
// retries a call the upstream refuses with UNAVAILABLE, and without it the first refusal reaches the caller.
func TestRetryServiceConfigRetriesUnavailable(t *testing.T) {
	open := func(t *testing.T, opts Options, failures int32) (*failingServer, error) {
		t.Helper()
		srv := &failingServer{failures: failures}
		s := defaultSync(loopback(t, srv))
		s.Options = opts
		require.NoError(t, s.Init(context.Background()))
		t.Cleanup(s.closeConn)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		t.Cleanup(cancel)
		stream, err := s.client.SyncFlags(ctx, &v1.SyncFlagsRequest{})
		if err != nil {
			return srv, err
		}
		_, err = stream.Recv()
		return srv, err
	}

	t.Run("opted in, the third attempt succeeds", func(t *testing.T) {
		srv, err := open(t, Options{RetryBackoff: 10 * time.Millisecond, RetryBackoffMax: 20 * time.Millisecond}, 2)
		require.NoError(t, err)
		require.Equal(t, int32(3), srv.calls.Load())
	})
	t.Run("opted in, four attempts are the limit", func(t *testing.T) {
		srv, err := open(t, Options{RetryBackoff: 10 * time.Millisecond, RetryBackoffMax: 20 * time.Millisecond}, 10)
		require.Equal(t, codes.Unavailable, status.Code(err))
		require.Equal(t, int32(4), srv.calls.Load())
	})
	t.Run("not opted in, the first refusal reaches the caller", func(t *testing.T) {
		srv, err := open(t, Options{}, 2)
		require.Equal(t, codes.Unavailable, status.Code(err))
		require.Equal(t, int32(1), srv.calls.Load())
	})
}

// TestSelectorHeaderOnBothCalls pins Q6: with selectorHeader the selector travels as the flagd-selector header on
// SyncFlags and FetchAllFlags as well as in the request body, and without it, or without a selector, no header is sent.
func TestSelectorHeaderOnBothCalls(t *testing.T) {
	run := func(t *testing.T, opts Options, selector string) *recordingServer {
		t.Helper()
		srv := &recordingServer{}
		s := defaultSync(loopback(t, srv))
		s.Options, s.Selector = opts, selector
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
		return srv
	}

	t.Run("sent, in addition to the body", func(t *testing.T) {
		srv := run(t, Options{SelectorHeader: true}, "flagSetId=app")
		srv.mu.Lock()
		defer srv.mu.Unlock()
		require.Equal(t, []string{"flagSetId=app"}, srv.syncMetadata.Get("flagd-selector"))
		require.Equal(t, []string{"flagSetId=app"}, srv.fetchMetadata.Get("flagd-selector"))
		require.Equal(t, "flagSetId=app", srv.syncRequest.GetSelector())  //nolint:staticcheck // the body field stays
		require.Equal(t, "flagSetId=app", srv.fetchRequest.GetSelector()) //nolint:staticcheck // the body field stays
	})
	t.Run("not sent without the setting", func(t *testing.T) {
		srv := run(t, Options{}, "flagSetId=app")
		srv.mu.Lock()
		defer srv.mu.Unlock()
		require.Empty(t, srv.syncMetadata.Get("flagd-selector"))
		require.Empty(t, srv.fetchMetadata.Get("flagd-selector"))
	})
	t.Run("not sent without a selector", func(t *testing.T) {
		srv := run(t, Options{SelectorHeader: true}, "")
		srv.mu.Lock()
		defer srv.mu.Unlock()
		require.Empty(t, srv.syncMetadata.Get("flagd-selector"))
		require.Empty(t, srv.fetchMetadata.Get("flagd-selector"))
	})
}

// TestOverrideKeepsInterceptorsOnly pins Q15: under GrpcDialOptionsOverride the selector header still applies, and the
// retry policy and the keepalive do not, because the override owns the dial. The injected options apply on both paths.
func TestOverrideKeepsInterceptorsOnly(t *testing.T) {
	all := Options{
		RetryBackoff: 10 * time.Millisecond, RetryBackoffMax: 20 * time.Millisecond,
		KeepAliveTime: 30 * time.Second, SelectorHeader: true,
	}
	record := func(s *Sync) []grpc.DialOption {
		var got []grpc.DialOption
		s.dialRecorder = func(o []grpc.DialOption) { got = o }
		require.NoError(t, s.Init(context.Background()))
		return got
	}

	t.Run("the default path has every option", func(t *testing.T) {
		s := defaultSync("host:8013")
		s.Options, s.Selector = all, "flagSetId=app"
		s.DialOptions = []grpc.DialOption{grpc.WithUserAgent("distribution")}
		// credentials, service config, keepalive, the injected option and the two interceptors
		require.Len(t, record(s), 6)
	})
	t.Run("the override path has the interceptors and the injected options only", func(t *testing.T) {
		s := defaultSync("host:8013")
		s.Options, s.Selector = all, "flagSetId=app"
		s.GrpcDialOptionsOverride = []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
		s.DialOptions = []grpc.DialOption{grpc.WithUserAgent("distribution")}
		// the override, the injected option and the two interceptors; no service config and no keepalive
		require.Len(t, record(s), 4)
	})
	t.Run("the selector header still reaches the upstream under an override", func(t *testing.T) {
		srv := &recordingServer{}
		s := defaultSync(loopback(t, srv))
		s.Options, s.Selector = Options{SelectorHeader: true}, "flagSetId=app"
		s.GrpcDialOptionsOverride = []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
		require.NoError(t, s.Init(context.Background()))
		t.Cleanup(s.closeConn)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		t.Cleanup(cancel)
		require.NoError(t, s.ReSync(ctx, make(chan sync.DataSync, 1)))
		srv.mu.Lock()
		defer srv.mu.Unlock()
		require.Equal(t, []string{"flagSetId=app"}, srv.fetchMetadata.Get("flagd-selector"))
	})
}
