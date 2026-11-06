package flagevallegacy

import (
	"context"
	"net"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	flageval "github.com/open-feature-forking/openfeature-relay/service/flag-evaluation"
	"github.com/stretchr/testify/require"
)

// noEvents is the subscriptions of a service that no stream is opened on.
type noEvents struct{}

func (noEvents) Subscribe(context.Context, any, *store.Selector, chan service.Notification) {}

func (noEvents) Unsubscribe(any) {}

// withoutConnectOptions hides the ConnectRPC instrumentation of a telemetry provider, which a flag evaluation endpoint
// applies when its provider has it.
type withoutConnectOptions struct{ telemetry.Provider }

// serveComposed serves the flag evaluation endpoint with the legacy endpoint mounted on it, created as the runtime
// creates them, on a free port until the test ends. It returns the base URL.
func serveComposed(
	t testing.TB, resolver evaluator.IResolver, flagStore *store.Store, tp telemetry.Provider, cfg service.Configuration,
) string {
	t.Helper()
	return serveComposedLogged(t, logger.NewLogger(nil, false), resolver, flagStore, tp, cfg)
}

func serveComposedLogged(
	t testing.TB, log *logger.Logger, resolver evaluator.IResolver, flagStore *store.Store, tp telemetry.Provider,
	cfg service.Configuration,
) string {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	hosted, err := Factory{}.Create(service.Settings{Config: map[string]any{}})
	require.NoError(t, err)
	host, err := flageval.Factory{}.Create(service.Settings{
		Resolver: resolver, Store: flagStore, Telemetry: tp, Logger: log,
		Config:     map[string]any{flageval.ConfigKey: flageval.EndpointConfig{Configuration: cfg}},
		Extensions: []service.Endpoint{hosted},
		Listen:     func(string, string) (net.Listener, error) { return lis, nil },
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- host.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-done)
	})
	return "http://" + lis.Addr().String()
}

func mustStore(t testing.TB) *store.Store {
	t.Helper()
	s, err := store.NewStore(logger.NewLogger(nil, false), []string{"source"})
	require.NoError(t, err)
	return s
}
