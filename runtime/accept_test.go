//go:build unix

package runtime

import (
	"context"
	flagdcomponents "github.com/open-feature/flagd/flagd/components"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
)

const changeFlags = `{"flags":{"a":{"state":"ENABLED","defaultVariant":"on","variants":{"on":true,"off":false}}}}`

// acceptRuntime builds a runtime through FromConfig, with a file source and the fake vendor format's source,
// without starting it, and subscribes to its configuration change events.
func acceptRuntime(t *testing.T) (rt *Runtime, changes *events.Subscription, flagdSource, vendorSource string) {
	t.Helper()
	dir := t.TempDir()
	flagdSource = filepath.Join(dir, "flags.json")
	require.NoError(t, os.WriteFile(flagdSource, []byte(changeFlags), 0o600))
	vendorSource = filepath.Join(dir, "vendor.json")
	require.NoError(t, os.WriteFile(vendorSource, []byte(`{}`), 0o600))

	cfg := characterizedConfig(t, newPorts(t))
	cfg.SyncProviders = []coresync.SourceConfig{
		{URI: flagdSource, Provider: "file"},
		{URI: vendorSource, Provider: "file", Format: "versioned"},
	}
	var err error
	rt, err = FromConfig(logger.NewLogger(nil, false), "test", cfg, Components{
		Sync:                 flagdcomponents.Components().Sync,
		ConfigurationFormats: append(flagdcomponents.Components().ConfigurationFormats, versionedFactory{}),
		Evaluators:           flagdcomponents.Components().Evaluators,
	})
	require.NoError(t, err)
	changes = rt.Bus.Subscribe(events.SubscribeOptions{Kinds: []events.Kind{events.KindConfigurationChange}, Buffer: 16})
	t.Cleanup(changes.Close)
	return rt, changes, flagdSource, vendorSource
}

// versionedFactory is a vendor format whose document version is the delivery's text.
type versionedFactory struct{}

func (versionedFactory) Type() string { return "versioned" }

func (versionedFactory) Create(s configformat.Settings) (configformat.ConfigurationFormat, error) {
	return versionedFormat{store: s.Store}, nil
}

type versionedFormat struct{ store *store.Store }

func (versionedFormat) Type() string { return "versioned" }

func (f versionedFormat) Accept(_ context.Context, in configformat.Input) (configformat.Result, error) {
	err := f.store.Apply(store.ApplyRequest{
		Format: "versioned", Source: in.Source,
		Documents: []store.Document{{Format: "versioned", Source: in.Source, FlagSet: "v", Version: in.Data, FlagKeys: []string{"k"}}},
	})
	return configformat.Result{Documents: []configformat.DocumentReport{{FlagSet: "v", Version: in.Data, FlagKeys: []string{"k"}}}}, err
}

func nextChange(t *testing.T, sub *events.Subscription) (events.ConfigurationChange, bool) {
	t.Helper()
	select {
	case ev := <-sub.C():
		return ev.(events.ConfigurationChange), true
	case <-time.After(300 * time.Millisecond):
		return events.ConfigurationChange{}, false
	}
}

func TestChangeEventPerChangedDocument(t *testing.T) {
	rt, changes, source, _ := acceptRuntime(t)

	rt.updateAndEmit(coresync.DataSync{Source: source, FlagData: changeFlags})
	ev, ok := nextChange(t, changes)
	require.True(t, ok, "the first delivery is a new document")
	require.Equal(t, "flagd", ev.Format)
	require.Equal(t, source, ev.Source)
	require.Equal(t, []string{"a"}, ev.Added)
	require.NotEmpty(t, ev.Version)
	require.False(t, ev.Time.IsZero())

	changed := `{"flags":{"a":{"state":"ENABLED","defaultVariant":"off","variants":{"on":true,"off":false}},"b":{"state":"ENABLED","defaultVariant":"on","variants":{"on":true}}}}`
	rt.updateAndEmit(coresync.DataSync{Source: source, FlagData: changed})
	ev, ok = nextChange(t, changes)
	require.True(t, ok)
	require.Equal(t, []string{"b"}, ev.Added)
	require.Equal(t, []string{"a"}, ev.Updated)
	_, more := nextChange(t, changes)
	require.False(t, more, "one event per document")
}

func TestNoEventForUnchangedDelivery(t *testing.T) {
	rt, changes, source, _ := acceptRuntime(t)

	rt.updateAndEmit(coresync.DataSync{Source: source, FlagData: changeFlags})
	_, ok := nextChange(t, changes)
	require.True(t, ok)

	rt.updateAndEmit(coresync.DataSync{Source: source, FlagData: changeFlags})
	_, ok = nextChange(t, changes)
	require.False(t, ok, "the same flags again change nothing")
}

func TestChangeEventForVendorFormat(t *testing.T) {
	rt, changes, _, vendor := acceptRuntime(t)

	rt.updateAndEmit(coresync.DataSync{Source: vendor, FlagData: "one"})
	ev, ok := nextChange(t, changes)
	require.True(t, ok)
	require.Equal(t, "versioned", ev.Format)
	require.Equal(t, "one", ev.Version)

	rt.updateAndEmit(coresync.DataSync{Source: vendor, FlagData: "one"})
	_, ok = nextChange(t, changes)
	require.False(t, ok, "the same version again")

	rt.updateAndEmit(coresync.DataSync{Source: vendor, FlagData: "two"})
	ev, ok = nextChange(t, changes)
	require.True(t, ok, "a new version of the document, though no flag key changed")
	require.Equal(t, "two", ev.Version)
	require.Empty(t, ev.Added)
}

// TestFlagSyncSpanOnAcceptPath pins that the flagSync span is there when the delivery goes through a format.
func TestFlagSyncSpanOnAcceptPath(t *testing.T) {
	rt, _, source, _ := acceptRuntime(t)
	rt.Telemetry = spanRecorder
	spanRecorder.Reset()

	rt.updateAndEmit(coresync.DataSync{Source: source, FlagData: changeFlags})

	require.Equal(t, "jsonEvaluator/flagSync[internal]{feature_flag.source="+source+"}\n", renderSpans(spanRecorder.Ended()))
}

func TestInitializedClosesWhenEverySourceIsAccepted(t *testing.T) {
	rt, _, source, vendor := acceptRuntime(t)
	select {
	case <-rt.Initialized():
		t.Fatal("initialized before any source delivered")
	default:
	}

	rt.updateAndEmit(coresync.DataSync{Source: source, FlagData: changeFlags})
	select {
	case <-rt.Initialized():
		t.Fatal("initialized with one of two sources")
	default:
	}

	rt.updateAndEmit(coresync.DataSync{Source: vendor, FlagData: "one"})
	select {
	case <-rt.Initialized():
	default:
		t.Fatal("not initialized after every source delivered")
	}
}
