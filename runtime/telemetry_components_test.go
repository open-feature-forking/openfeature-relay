package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	"github.com/stretchr/testify/require"
)

// recordingTelemetry is a telemetry factory that keeps what it was asked to build, and its provider.
type recordingTelemetry struct {
	err       error
	settings  telemetry.Settings
	provider  *recordingProvider
	createdOn int
}

func (*recordingTelemetry) Type() string { return "recording" }

func (f *recordingTelemetry) Create(_ context.Context, s telemetry.Settings) (telemetry.Provider, error) {
	f.createdOn++
	f.settings = s
	if f.err != nil {
		return nil, f.err
	}
	f.provider = &recordingProvider{Provider: telemetry.Noop()}
	return f.provider, nil
}

type recordingProvider struct {
	telemetry.Provider
	shutdowns int
}

func (p *recordingProvider) Shutdown(ctx context.Context) error {
	p.shutdowns++
	return p.Provider.Shutdown(ctx)
}

// capturingEndpoint is a service endpoint factory that keeps the settings it got.
type capturingEndpoint struct {
	settings *service.Settings
	err      error
}

func (capturingEndpoint) Type() string         { return "capturing" }
func (capturingEndpoint) Needs() service.Needs { return service.Needs{} }
func (f capturingEndpoint) Create(s service.Settings) (service.Endpoint, error) {
	*f.settings = s
	return nil, f.err
}

func oneFileSource(t *testing.T) []coresync.SourceConfig {
	t.Helper()
	path := filepath.Join(t.TempDir(), "flags.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"flags":{}}`), 0o600))
	return []coresync.SourceConfig{{URI: path, Provider: "file"}}
}

// TestTelemetryProviderIsHandedToEveryComponent pins that the provider of the components' factory is created once,
// with the distribution's name and version and the components' configuration, and is the one the runtime and the
// service endpoints get. Its shutdown is a shutdown hook.
func TestTelemetryProviderIsHandedToEveryComponent(t *testing.T) {
	factory := &recordingTelemetry{}
	var endpointSettings service.Settings

	rt, err := FromConfig(logger.NewLogger(nil, false), "1.2.3", Config{SyncProviders: oneFileSource(t)}, withFlagd(Components{
		Telemetry:       factory,
		TelemetryConfig: map[string]any{"key": "value"},
		Endpoints:       []service.Factory{capturingEndpoint{settings: &endpointSettings}},
	}))
	require.NoError(t, err)

	require.Equal(t, 1, factory.createdOn)
	require.Equal(t, "flagd", factory.settings.DistributionName)
	require.Equal(t, "1.2.3", factory.settings.DistributionVersion)
	require.Equal(t, map[string]any{"key": "value"}, factory.settings.Config)
	require.NotNil(t, factory.settings.Logger)
	require.Same(t, factory.provider, endpointSettings.Telemetry)
	require.Same(t, factory.provider, rt.Telemetry)

	require.Zero(t, factory.provider.shutdowns, "the provider runs until the runtime stops")
	rt.runShutdownHooks()
	require.Equal(t, 1, factory.provider.shutdowns)
}

// TestUnbuildableTelemetryProviderStopsFromConfig: an error of the telemetry factory is an error of FromConfig that
// names the telemetry provider.
func TestUnbuildableTelemetryProviderStopsFromConfig(t *testing.T) {
	factory := &recordingTelemetry{err: errors.New(`setting "metricsExporter": 3 is not a string`)}

	rt, err := FromConfig(logger.NewLogger(nil, false), "test", Config{SyncProviders: oneFileSource(t)},
		Components{Telemetry: factory})

	require.Nil(t, rt)
	require.EqualError(t, err,
		`error creating the telemetry provider "recording": setting "metricsExporter": 3 is not a string`)
}

// TestTelemetryProviderIsShutDownWhenFromConfigFails: a runtime that is not built does not leave the provider
// running.
func TestTelemetryProviderIsShutDownWhenFromConfigFails(t *testing.T) {
	factory := &recordingTelemetry{}
	var endpointSettings service.Settings

	rt, err := FromConfig(logger.NewLogger(nil, false), "test", Config{SyncProviders: oneFileSource(t)}, withFlagd(Components{
		Telemetry: factory,
		Endpoints: []service.Factory{capturingEndpoint{settings: &endpointSettings, err: errors.New("no port")}},
	}))

	require.Nil(t, rt)
	require.ErrorContains(t, err, "no port")
	require.Equal(t, 1, factory.provider.shutdowns)
}
