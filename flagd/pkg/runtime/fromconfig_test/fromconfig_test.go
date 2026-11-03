//go:build unix

// Package fromconfig_test builds runtimes through runtime.FromConfig. FromConfig installs a global
// trace provider, which the span tests in package runtime must not lose, so these tests live in a
// test binary of their own.
//
// The tests start the runtime and stop it with SIGTERM, which Start handles itself.
package fromconfig_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature/flagd/flagd/pkg/runtime"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

const (
	formatType = "fake"
	flagdFlags = `{"flags":{"f1":{"state":"ENABLED","defaultVariant":"on","variants":{"on":true,"off":false}}}}`
)

// fakeFactory creates a format that keeps one document per delivery and records the injected store.
type fakeFactory struct{ store *store.Store }

func (*fakeFactory) Type() string { return formatType }

func (f *fakeFactory) Create(s configformat.Settings) (configformat.ConfigurationFormat, error) {
	f.store = s.Store
	return fakeFormat{store: s.Store}, nil
}

type fakeFormat struct{ store *store.Store }

func (fakeFormat) Type() string { return formatType }

func (f fakeFormat) Accept(_ context.Context, in configformat.Input) (configformat.Result, error) {
	err := f.store.Apply(store.ApplyRequest{
		Format: formatType, Source: in.Source,
		Documents: []store.Document{{Format: formatType, Source: in.Source, FlagSet: "set", Version: "v1", FlagKeys: []string{"k"}}},
	})
	return configformat.Result{Documents: []configformat.DocumentReport{{FlagSet: "set", Version: "v1", FlagKeys: []string{"k"}}}}, err
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func freePort(t *testing.T) uint16 {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	return uint16(l.Addr().(*net.TCPAddr).Port)
}

// start builds a runtime from config, runs it, and returns once it reports ready. The returned
// function stops it.
func start(
	t *testing.T, providers []coresync.SourceConfig, factory *fakeFactory, extra ...coresync.Factory,
) (*runtime.Runtime, func()) {
	t.Helper()
	return startConfig(t, baseConfig(t, providers, factory, extra...))
}

func baseConfig(t *testing.T, providers []coresync.SourceConfig, factory *fakeFactory, extra ...coresync.Factory) runtime.Config {
	t.Helper()
	return runtime.Config{
		ManagementPort:               freePort(t),
		ServicePort:                  freePort(t),
		OfrepServicePort:             freePort(t),
		SyncServicePort:              freePort(t),
		StreamDeadline:               5 * time.Second,
		KeepAliveMinTime:             runtime.KeepAliveMinTimeDefault,
		KeepAlivePermitWithoutStream: runtime.KeepAlivePermitWithoutStreamDefault,
		SyncProviders:                providers,
		ConfigurationFormats:         []configformat.Factory{factory},
		SyncFactories:                extra,
	}
}

// startConfig builds a runtime from config, runs it, and returns once it reports ready.
func startConfig(t *testing.T, config runtime.Config) (*runtime.Runtime, func()) {
	t.Helper()
	managementPort := config.ManagementPort
	rt, err := runtime.FromConfig(logger.NewLogger(nil, false), "test", config, runtime.Official())
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- rt.Start() }()

	ready := fmt.Sprintf("http://127.0.0.1:%d/readyz", managementPort)
	require.Eventually(t, func() bool {
		resp, err := http.Get(ready) //nolint:gosec,noctx // local test endpoint
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 20*time.Second, 100*time.Millisecond, "the runtime becomes ready")

	return rt, func() {
		require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGTERM))
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatal("the runtime did not stop")
		}
	}
}

func TestSourcesNameTheirConfigurationFormat(t *testing.T) {
	byDefault := writeFile(t, "default.json", flagdFlags)
	byName := writeFile(t, "named.json", flagdFlags)
	custom := writeFile(t, "custom.json", `{"anything":"goes"}`)
	factory := &fakeFactory{}

	rt, stop := start(t, []coresync.SourceConfig{
		{URI: byDefault, Provider: "file"},
		{URI: byName, Provider: "file", Format: configformat.DefaultType},
		{URI: custom, Provider: "file", Format: formatType},
	}, factory)
	defer stop()

	require.Len(t, rt.Syncs, 3)
	require.Len(t, rt.Formats, 1, "only the source naming a non-default format has one")
	require.Contains(t, rt.Formats, custom)

	require.Len(t, factory.store.Documents(formatType, custom), 1, "the named format got the delivery")
	flags, _, err := factory.store.GetAll(context.Background(), &store.Selector{})
	require.NoError(t, err)
	require.Len(t, flags, 1, "flagd's format read the other two sources; both carry the key f1")
	require.Equal(t, "f1", flags[0].Key)
}

func TestUnknownConfigurationFormatSkipsSourceAndReadiness(t *testing.T) {
	known := writeFile(t, "flags.json", flagdFlags)
	unknown := writeFile(t, "vendor.json", `{"anything":"goes"}`)
	custom := writeFile(t, "custom.json", `{"anything":"goes"}`)
	factory := &fakeFactory{}

	// readiness is only reached if the skipped source is left out of it
	rt, stop := start(t, []coresync.SourceConfig{
		{URI: unknown, Provider: "file", Format: "nope"},
		{URI: known, Provider: "file"},
		{URI: custom, Provider: "file", Format: formatType},
	}, factory)
	defer stop()

	require.Len(t, rt.Syncs, 2, "the skipped source gets no sync provider")
	require.Len(t, rt.Formats, 1)
	require.Contains(t, rt.Formats, custom)
	require.Empty(t, factory.store.Documents(formatType, unknown))
	flags, _, err := factory.store.GetAll(context.Background(), &store.Selector{})
	require.NoError(t, err)
	require.Len(t, flags, 1)
	require.Equal(t, known, flags[0].Source)
}

// The config file lists sources the way --sources does; viper maps "format" onto SourceConfig.Format.
func TestConfigFileSourcesNameTheirFormat(t *testing.T) {
	path := writeFile(t, "flagd.yaml", `sources:
  - uri: file:/flags.json
    provider: file
  - uri: file:/vendor.json
    provider: file
    format: fake
`)
	v := viper.New()
	v.SetConfigFile(path)
	require.NoError(t, v.ReadInConfig())

	var sources []coresync.SourceConfig
	require.NoError(t, v.UnmarshalKey("sources", &sources))

	require.Len(t, sources, 2)
	require.Equal(t, "", sources[0].Format)
	require.Equal(t, formatType, sources[1].Format)
}

// signalTriggerFactory is a fetch trigger of an extension that signals when its channel receives.
type signalTriggerFactory struct{ signals chan struct{} }

func (signalTriggerFactory) Type() string { return "signal" }

func (f signalTriggerFactory) CreateTrigger(coresync.Settings) (coresync.Trigger, error) {
	return signalTrigger(f), nil
}

type signalTrigger struct{ signals chan struct{} }

func (t signalTrigger) Run(ctx context.Context, signal func()) error {
	for {
		select {
		case <-t.signals:
			signal()
		case <-ctx.Done():
			return nil
		}
	}
}

// TestExtensionTriggerAttached: the `triggers` of a source replace the file watch, so the source is fetched again
// when the extension's trigger signals and not when the file changes.
func TestExtensionTriggerAttached(t *testing.T) {
	path := writeFile(t, "flags.json", flagdFlags)
	signals := make(chan struct{}, 1)

	rt, stop := start(t, []coresync.SourceConfig{{
		URI: path, Provider: "file", Triggers: []coresync.TriggerConfig{{Type: "signal"}},
	}}, &fakeFactory{}, signalTriggerFactory{signals})
	defer stop()

	resolves := func(key string) bool {
		_, _, _, _, err := rt.Evaluator.ResolveBooleanValue(context.Background(), "req", key, nil)
		return err == nil
	}
	require.True(t, resolves("f1"))

	require.NoError(t, os.WriteFile(path, []byte(`{"flags":{"f2":{"state":"ENABLED","defaultVariant":"on","variants":{"on":true}}}}`), 0o600))
	require.Never(t, func() bool { return resolves("f2") }, 2*time.Second, 100*time.Millisecond,
		"a file change does not fetch the source: its file watch was replaced")

	signals <- struct{}{}
	require.Eventually(t, func() bool { return resolves("f2") }, 10*time.Second, 50*time.Millisecond,
		"the signal fetches the changed file")
	require.False(t, resolves("f1"))
}

// The config file lists the triggers of a source with their settings; viper keeps the settings besides the type.
func TestConfigFileSourcesListTheirTriggers(t *testing.T) {
	path := writeFile(t, "flagd.yaml", `sources:
  - uri: file:/flags.json
    provider: file
    triggers:
      - type: signal
        name: reload
        count: 2
`)
	v := viper.New()
	v.SetConfigFile(path)
	require.NoError(t, v.ReadInConfig())

	var sources []coresync.SourceConfig
	require.NoError(t, v.UnmarshalKey("sources", &sources))

	require.Equal(t, []coresync.TriggerConfig{
		{Type: "signal", Config: map[string]any{"name": "reload", "count": 2}},
	}, sources[0].Triggers)
}

// TestStrictNumericRangeReachesTheRuntime pins that Config.StrictNumericRange, which --strict-numeric-range sets,
// makes the runtime's resolution refuse an integer outside the range a double holds exactly.
func TestStrictNumericRangeReachesTheRuntime(t *testing.T) {
	const flags = `{"flags":{"big":{"state":"ENABLED","defaultVariant":"on","variants":{"on":10000000000000000}}}}`
	for _, strict := range []bool{false, true} {
		t.Run(fmt.Sprintf("strict=%v", strict), func(t *testing.T) {
			config := baseConfig(t, []coresync.SourceConfig{{URI: writeFile(t, "flags.json", flags), Provider: "file"}}, &fakeFactory{})
			config.StrictNumericRange = strict
			rt, stop := startConfig(t, config)
			defer stop()

			value, _, _, _, err := rt.Evaluator.ResolveIntValue(context.Background(), "", "big", nil)
			if strict {
				require.EqualError(t, err, model.TypeMismatchErrorCode)
				return
			}
			require.NoError(t, err)
			require.Equal(t, int64(10000000000000000), value)
		})
	}
}
