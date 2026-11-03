package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	evalmock "github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator/mock"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

const fakeFormatType = "fake"

type fakeFormat struct {
	accepted []configformat.Input
	err      error
}

func (*fakeFormat) Type() string { return fakeFormatType }

func (f *fakeFormat) Accept(_ context.Context, in configformat.Input) (configformat.Result, error) {
	f.accepted = append(f.accepted, in)
	if f.err != nil {
		return configformat.Result{}, f.err
	}
	return configformat.Result{Documents: []configformat.DocumentReport{{FlagSet: in.Source, Version: strconv.Itoa(len(f.accepted))}}}, nil
}

type fakeFactory struct{ created []*fakeFormat }

func (*fakeFactory) Type() string { return fakeFormatType }

func (f *fakeFactory) Create(configformat.Settings) (configformat.ConfigurationFormat, error) {
	format := &fakeFormat{}
	f.created = append(f.created, format)
	return format, nil
}

func observedLogger() (*logger.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zap.DebugLevel)
	return logger.NewLogger(zap.New(core), false), logs
}

func fileSource(t *testing.T, name, format string) coresync.SourceConfig {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(`{"flags":{}}`), 0o600))
	return coresync.SourceConfig{URI: path, Provider: "file", Format: format}
}

// FromConfig installs a global trace provider, which the span tests must not lose, so these tests
// call the helpers it is built from.

func TestSourcesNameTheirConfigurationFormat(t *testing.T) {
	log, _ := observedLogger()
	factory := &fakeFactory{}
	factories := []configformat.Factory{factory}
	byDefault := fileSource(t, "default.json", "")
	byName := fileSource(t, "named-default.json", configformat.DefaultType)
	custom := fileSource(t, "custom.json", fakeFormatType)

	providers := sourcesWithKnownFormat(log, []coresync.SourceConfig{byDefault, byName, custom}, factories)
	require.Equal(t, []coresync.SourceConfig{byDefault, byName, custom}, providers)

	st, err := store.NewStore(log, []string{byDefault.URI, byName.URI, custom.URI})
	require.NoError(t, err)
	formats, err := configurationFormats(log, st, providers, factories)
	require.NoError(t, err)

	require.Len(t, formats, 1, "only the source naming a non-default format gets one")
	require.Same(t, factory.created[0], formats[custom.URI])
}

func TestUnknownConfigurationFormatSkipsSource(t *testing.T) {
	log, logs := observedLogger()
	known := fileSource(t, "flags.json", "")
	unknown := fileSource(t, "vendor.json", "nope")
	factories := []configformat.Factory{&fakeFactory{}}

	providers := sourcesWithKnownFormat(log, []coresync.SourceConfig{unknown, known}, factories)
	require.Equal(t, []coresync.SourceConfig{known}, providers, "the skipped source stays out of sync providers, store and readiness")

	var found bool
	for _, entry := range logs.FilterLevelExact(zap.ErrorLevel).All() {
		if strings.Contains(entry.Message, unknown.URI) && strings.Contains(entry.Message, "fake, flagd") {
			found = true
		}
	}
	require.True(t, found, "an error names the skipped source and the available formats: %v", logs.All())

	// readiness is built from the kept sources, as FromConfig does
	readiness := map[string]bool{}
	for _, p := range providers {
		readiness[p.URI] = false
	}
	rt := Runtime{
		Evaluator:       evalmock.NewMockIEvaluator(gomock.NewController(t)),
		Logger:          log,
		sourceReadiness: readiness,
	}
	rt.Evaluator.(*evalmock.MockIEvaluator).EXPECT().SetState(gomock.Any()).Return(nil)
	rt.updateAndEmit(coresync.DataSync{Source: known.URI, FlagData: `{"flags":{}}`})
	require.True(t, rt.initialized.Load(), "readiness is reached on the remaining source alone")
}

func TestSourceWithFormatGoesThroughAccept(t *testing.T) {
	const source = "vendor-source"
	log := logger.NewLogger(nil, false)
	format := &fakeFormat{}
	bus := events.NewBus()
	delivered := deliveredSources(t, bus)
	rt := Runtime{
		// no expectations: a source with a format must not reach the evaluator
		Evaluator:       evalmock.NewMockIEvaluator(gomock.NewController(t)),
		Logger:          log,
		Bus:             bus,
		Telemetry:       spanRecorder,
		Formats:         map[string]configformat.ConfigurationFormat{source: format},
		sourceReadiness: map[string]bool{source: false},
	}
	spanRecorder.Reset()

	rt.updateAndEmit(coresync.DataSync{Source: source, FlagData: "payload", Selector: "sel", IncrementalUpdates: true})

	require.Equal(t, []configformat.Input{{Source: source, Data: "payload", Selector: "sel", IncrementalUpdate: true}}, format.accepted)
	require.Equal(t, []string{source}, delivered())
	require.True(t, rt.initialized.Load())
	require.Equal(t, "jsonEvaluator/flagSync[internal]{feature_flag.source=vendor-source}\n", renderSpans(spanRecorder.Ended()))
}

func TestRejectionLogNamesSourceAndEntry(t *testing.T) {
	const source = "vendor-source"
	log, logs := observedLogger()
	format := &fakeFormat{err: configformat.Rejection{Source: source, Entry: "flag-7", Reason: "no value"}}
	bus := events.NewBus()
	delivered := deliveredSources(t, bus)
	rt := Runtime{
		Evaluator:       evalmock.NewMockIEvaluator(gomock.NewController(t)),
		Logger:          log,
		Bus:             bus,
		Telemetry:       spanRecorder,
		Formats:         map[string]configformat.ConfigurationFormat{source: format},
		sourceReadiness: map[string]bool{source: false},
	}
	spanRecorder.Reset()

	rt.updateAndEmit(coresync.DataSync{Source: source})

	require.Empty(t, delivered(), "a rejection publishes no event")
	require.False(t, rt.initialized.Load())
	errs := logs.FilterLevelExact(zap.ErrorLevel).All()
	require.Len(t, errs, 1)
	require.Contains(t, errs[0].Message, `source "vendor-source"`)
	require.Contains(t, errs[0].Message, `entry "flag-7"`)
	spans := spanRecorder.Ended()
	require.Len(t, spans, 1)
	require.Equal(t, codes.Error, spans[0].Status().Code)
}
