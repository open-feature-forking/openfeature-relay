package builder

import (
	"context"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
)

// signalTrigger is a fetch trigger of an extension: it signals when its channel receives.
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

type signalTriggerFactory struct {
	signals chan struct{}
	// settings records the settings of the last trigger created
	settings *map[string]any
}

func (signalTriggerFactory) Type() string { return "signal" }
func (f signalTriggerFactory) CreateTrigger(s sync.Settings) (sync.Trigger, error) {
	*f.settings = s.Config
	return signalTrigger{f.signals}, nil
}

func TestParseSourcesReadsTriggers(t *testing.T) {
	got, err := ParseSources(`[{"uri":"example://flags","provider":"example","triggers":[
		{"type":"signal","name":"hup","count":2},{"type":"poller"}]}]`)
	require.NoError(t, err)
	require.Equal(t, []sync.TriggerConfig{
		{Type: "signal", Config: map[string]any{"name": "hup", "count": float64(2)}},
		{Type: "poller"},
	}, got[0].Triggers)

	t.Run("a trigger needs a type", func(t *testing.T) {
		_, err := ParseSources(`[{"uri":"example://flags","provider":"example","triggers":[{"name":"hup"}]}]`)
		require.ErrorContains(t, err, `a fetch trigger needs a "type"`)
	})
}

func TestTriggersAreWrittenAsTheyAreRead(t *testing.T) {
	var in sync.TriggerConfig
	require.NoError(t, in.UnmarshalJSON([]byte(`{"type":"signal","name":"hup"}`)))
	out, err := in.MarshalJSON()
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"signal","name":"hup"}`, string(out))
}

func TestUnknownTriggerType(t *testing.T) {
	src := &exampleSource{}
	factories := append(Official(), exampleFactory{src}, signalTriggerFactory{settings: new(map[string]any)})
	source := sync.SourceConfig{
		URI: "example://flags", Provider: "example", Triggers: []sync.TriggerConfig{{Type: "nope"}},
	}

	_, err := NewSyncBuilder(factories...).Resolve(source, loggerForTests())
	require.EqualError(t, err, `unknown fetch trigger type "nope", available: poller, fsnotify, fileinfo, signal`)

	_, err = NewSyncBuilder(factories...).SyncsFromConfig([]sync.SourceConfig{source}, loggerForTests())
	require.ErrorContains(t, err, `unknown fetch trigger type "nope"`)
}

func TestTriggersOfAStreamingSourceAreRejected(t *testing.T) {
	_, err := NewSyncBuilder(Official()...).Resolve(sync.SourceConfig{
		URI: "host:8013", Provider: "grpc", Triggers: []sync.TriggerConfig{{Type: "poller"}},
	}, loggerForTests())
	require.EqualError(t, err, "the grpc sync provider streams its source and takes no fetch triggers")
}

func TestTriggersReplaceTheDefaults(t *testing.T) {
	signals := make(chan struct{}, 1)
	var settings map[string]any
	src := &exampleSource{}
	src.set(flagsA)
	factories := append(Official(), exampleFactory{src}, signalTriggerFactory{signals: signals, settings: &settings})

	res, err := NewSyncBuilder(factories...).Resolve(sync.SourceConfig{
		URI: "example://flags", Provider: "example",
		Triggers: []sync.TriggerConfig{{Type: "signal", Config: map[string]any{"name": "hup"}}},
	}, loggerForTests())
	require.NoError(t, err)
	require.Equal(t, []sync.TriggerConfig{{Type: "signal", Config: map[string]any{"name": "hup"}}}, res.Triggers)

	t.Run("the source is refetched when the trigger signals, and not on an interval", func(t *testing.T) {
		started := startSource(t, sync.SourceConfig{
			URI: "example://flags", Provider: "example", Interval: 1,
			Triggers: []sync.TriggerConfig{{Type: "signal", Config: map[string]any{"name": "hup"}}},
		}, func(sb *SyncBuilder) { sb.factories = factories })
		started.requireDelivery(t, sync.DataSync{FlagData: flagsA, Source: "example://flags"})
		require.Equal(t, map[string]any{"name": "hup"}, settings)

		src.set(flagsB)
		started.requireNoDeliveryWithin(t, 2500*time.Millisecond)
		signals <- struct{}{}
		started.requireDelivery(t, sync.DataSync{FlagData: flagsB, Source: "example://flags"})
	})
}
