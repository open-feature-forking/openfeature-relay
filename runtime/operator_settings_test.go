//go:build unix

package runtime

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/eventsink"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// TestEventSinksNeedMetricsTelemetry pins Q21 (UC-021 A5): event sinks that the operator's event-sinks setting names
// are refused when the telemetry provider serves no /metrics, because the events a sink does not get are reported
// there. A main that lists its own sinks is not affected.
func TestEventSinksNeedMetricsTelemetry(t *testing.T) {
	sinks := []eventsink.Configured{{Factory: recordingSinkFactory{sink: &recordingSink{}}}}
	start := func(t *testing.T, needs bool, c Components) (*Runtime, error) {
		t.Helper()
		return FromConfig(logger.NewLogger(nil, false), "test",
			Config{SyncProviders: oneFileSource(t), EventSinksNeedMetrics: needs}, withFlagd(c))
	}

	t.Run("refused without a telemetry provider", func(t *testing.T) {
		rt, err := start(t, true, Components{EventSinks: sinks})
		require.Nil(t, rt)
		require.ErrorContains(t, err, "event-sinks")
		require.ErrorContains(t, err, "serves /metrics")
		require.ErrorContains(t, err, "(noop)")
	})
	t.Run("refused with a provider that serves no metrics", func(t *testing.T) {
		rt, err := start(t, true, Components{EventSinks: sinks, Telemetry: &dropCountingTelemetry{}})
		require.Nil(t, rt)
		require.ErrorContains(t, err, "(drop-counting)")
	})
	t.Run("allowed when the distribution serves metrics", func(t *testing.T) {
		rt, err := start(t, true, Components{EventSinks: sinks, MetricsHandler: http.NotFoundHandler()})
		require.NoError(t, err)
		require.NotNil(t, rt)
	})
	t.Run("not asked for without the operator's setting", func(t *testing.T) {
		rt, err := start(t, false, Components{EventSinks: sinks})
		require.NoError(t, err)
		require.NotNil(t, rt)
	})
	t.Run("no sinks, no check", func(t *testing.T) {
		rt, err := start(t, true, Components{})
		require.NoError(t, err)
		require.NotNil(t, rt)
	})
}

// TestMiddlewareCredentialReference pins Q14 for middleware: a setting that names an environment variable or a file is
// replaced by its value before the factory creates the middleware, and a variable that is not set stops startup with
// the middleware and the setting named, never a value.
func TestMiddlewareCredentialReference(t *testing.T) {
	t.Setenv("RELAY_TEST_MW_KEY", "the-api-key")
	var settings middleware.Settings
	config := map[string]any{"tag": "x", "keys": map[string]any{"env": "RELAY_TEST_MW_KEY"}}
	_, err := FromConfig(logger.NewLogger(nil, false), "test", Config{SyncProviders: oneFileSource(t)}, withFlagd(Components{
		Middleware: []middleware.Configured{{Factory: taggingFactory{name: "api-key", settings: &settings}, Config: config}},
	}))
	require.NoError(t, err)
	require.Equal(t, "the-api-key", settings.Config["keys"])
	require.Equal(t, map[string]any{"env": "RELAY_TEST_MW_KEY"}, config["keys"], "the components' settings are left as they were")

	rt, err := FromConfig(logger.NewLogger(nil, false), "test", Config{SyncProviders: oneFileSource(t)}, withFlagd(Components{
		Middleware: []middleware.Configured{{
			Factory: taggingFactory{name: "api-key"},
			Config:  map[string]any{"tag": "x", "keys": map[string]any{"env": "RELAY_TEST_MW_UNSET"}},
		}},
	}))
	require.Nil(t, rt)
	require.ErrorContains(t, err, `middleware "api-key"`)
	require.ErrorContains(t, err, `setting "keys"`)
	require.ErrorContains(t, err, `"RELAY_TEST_MW_UNSET"`)
}

// TestNoEventSinksConfiguredNothingSubscribes pins NFR-013's no-cost path: a distribution that lists no event sink,
// as flagd-full does, has no subscriber on the event bus for any kind.
func TestNoEventSinksConfiguredNothingSubscribes(t *testing.T) {
	rt, err := FromConfig(logger.NewLogger(nil, false), "test", Config{SyncProviders: oneFileSource(t)}, withFlagd(Components{
		EventSinkFactories: []eventsink.Factory{recordingSinkFactory{sink: &recordingSink{}}},
	}))
	require.NoError(t, err)
	for _, kind := range []events.Kind{events.KindConfigurationChange, events.KindEvaluation, events.KindTracking} {
		require.False(t, rt.Bus.HasSubscribers(kind), "kind %s", kind)
	}
}

// TestCredentialNeverLogged pins UC-021 BR-006: the value of a credential reference reaches the factory and nothing
// the runtime logs, at any level, while the event sinks and the middleware are created.
func TestCredentialNeverLogged(t *testing.T) {
	const secret = "s3cr3t-credential-value"
	t.Setenv("RELAY_TEST_CREDENTIAL", secret)
	core, logs := observer.New(zapcore.DebugLevel)
	log := logger.NewLogger(zap.New(core), false)

	var sinkSettings eventsink.Settings
	var mwSettings middleware.Settings
	reference := map[string]any{"env": "RELAY_TEST_CREDENTIAL"}
	_, err := FromConfig(log, "test", Config{SyncProviders: oneFileSource(t), EventSinksNeedMetrics: true}, withFlagd(Components{
		MetricsHandler: http.NotFoundHandler(),
		EventSinks: []eventsink.Configured{{
			Factory: recordingSinkFactory{sink: &recordingSink{}, settings: &sinkSettings},
			Config:  map[string]any{"headers": map[string]any{"Authorization": reference}},
		}},
		Middleware: []middleware.Configured{{
			Factory: taggingFactory{name: "api-key", settings: &mwSettings},
			Config:  map[string]any{"tag": "x", "key": reference},
		}},
	}))
	require.NoError(t, err)

	require.Equal(t, map[string]any{"Authorization": secret}, sinkSettings.Config["headers"], "the sink gets the value")
	require.Equal(t, secret, mwSettings.Config["key"], "the middleware gets the value")
	require.NotZero(t, logs.Len(), "the runtime logged nothing, so the test shows nothing")
	for _, entry := range logs.All() {
		require.NotContains(t, entry.Message, secret)
		for _, field := range entry.Context {
			require.NotContains(t, field.String, secret)
			require.NotContains(t, fmt.Sprint(field.Interface), secret)
		}
	}
}
