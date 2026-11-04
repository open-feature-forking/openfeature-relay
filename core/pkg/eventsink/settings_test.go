package eventsink_test

import (
	"context"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/eventsink"
	"github.com/stretchr/testify/require"
)

var allKinds = []events.Kind{events.KindConfigurationChange, events.KindEvaluation, events.KindTracking}

// capturingFactory declares every kind and keeps the settings it was created with.
type capturingFactory struct {
	sink *fakeSink
	got  *eventsink.Settings
}

func (capturingFactory) Type() string          { return "capture" }
func (capturingFactory) Kinds() []events.Kind { return allKinds }
func (f capturingFactory) Create(s eventsink.Settings) (eventsink.Sink, error) {
	*f.got = s
	return f.sink, nil
}

func TestKindsNarrowFactoryKinds(t *testing.T) {
	sink := &fakeSink{}
	bus := events.NewBus()
	p, err := eventsink.NewPipeline(bus, capturingFactory{sink: sink, got: &eventsink.Settings{}},
		eventsink.Settings{Config: map[string]any{eventsink.KeyKinds: []any{"evaluation"}, eventsink.KeyFlushInterval: time.Hour}})
	require.NoError(t, err)

	bus.Publish(events.Evaluation{FlagKey: "f"})
	bus.Publish(events.ConfigurationChange{Source: "s"})
	bus.Publish(events.Tracking{EventName: "t"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, p.Close(ctx))

	got := sink.received()
	require.Equal(t, 1, count(got), "only the evaluation event reaches the sink")
	require.IsType(t, events.Evaluation{}, got[0][0])
}

func TestNoKindsSettingKeepsTheFactorysKinds(t *testing.T) {
	kinds, err := eventsink.NarrowKinds(allKinds, map[string]any{"url": "x"})
	require.NoError(t, err)
	require.Equal(t, allKinds, kinds)
}

func TestNarrowKindsAcceptsEveryListForm(t *testing.T) {
	for name, raw := range map[string]any{
		"any":    []any{"evaluation", "tracking"},
		"string": []string{"evaluation", "tracking"},
		"kind":   []events.Kind{events.KindEvaluation, events.KindTracking},
	} {
		kinds, err := eventsink.NarrowKinds(allKinds, map[string]any{eventsink.KeyKinds: raw})
		require.NoError(t, err, name)
		require.Equal(t, []events.Kind{events.KindEvaluation, events.KindTracking}, kinds, name)
	}
	kinds, err := eventsink.NarrowKinds(allKinds, map[string]any{eventsink.KeyKinds: []any{"tracking", "tracking"}})
	require.NoError(t, err)
	require.Equal(t, []events.Kind{events.KindTracking}, kinds, "a repeated kind subscribes once")
}

func TestNarrowKindsRefusesWhatTheSinkDoesNotTake(t *testing.T) {
	declared := []events.Kind{events.KindConfigurationChange}
	for name, tt := range map[string]struct {
		raw  any
		want string
	}{
		"undeclared kind": {[]any{"evaluation"}, `"evaluation"`},
		"unknown kind":    {[]any{"bogus"}, `"bogus"`},
		"empty list":      {[]any{}, "names no event kind"},
		"not a list":      {"evaluation", "expected a list"},
		"not a string":    {[]any{5}, "5 is not an event kind"},
	} {
		_, err := eventsink.NarrowKinds(declared, map[string]any{eventsink.KeyKinds: tt.raw})
		require.Error(t, err, name)
		require.ErrorContains(t, err, `"kinds"`, name)
		require.ErrorContains(t, err, tt.want, name)
		require.ErrorContains(t, err, "configuration_change", name+": names what the sink takes")
	}
}

func TestPipelineRefusesAnUndeclaredKindNamingTheSink(t *testing.T) {
	_, err := eventsink.NewPipeline(events.NewBus(),
		fakeFactory{sink: &fakeSink{}, kinds: []events.Kind{events.KindEvaluation}},
		eventsink.Settings{Config: map[string]any{eventsink.KeyKinds: []any{"tracking"}}})
	require.ErrorContains(t, err, `event sink "fake"`)
	require.ErrorContains(t, err, `"tracking"`)
}

func TestCredentialReference(t *testing.T) {
	t.Setenv("RELAY_TEST_SINK_TOKEN", "Bearer from-the-environment")
	var got eventsink.Settings
	config := map[string]any{
		"url":     "https://hooks.example.com",
		"headers": map[string]any{"Authorization": map[string]any{"env": "RELAY_TEST_SINK_TOKEN"}},
	}
	p, err := eventsink.NewPipeline(events.NewBus(), capturingFactory{sink: &fakeSink{}, got: &got}, eventsink.Settings{Config: config})
	require.NoError(t, err)
	t.Cleanup(func() { _ = closeWithin(t, p, time.Second) })

	require.Equal(t, map[string]any{"Authorization": "Bearer from-the-environment"}, got.Config["headers"],
		"the sink gets the value, not the reference")
	require.Equal(t, map[string]any{"env": "RELAY_TEST_SINK_TOKEN"}, config["headers"].(map[string]any)["Authorization"],
		"the caller's settings are left as they were")
}

func TestCredentialNeverInAnError(t *testing.T) {
	t.Setenv("RELAY_TEST_SINK_SET", "must-not-appear")
	_, err := eventsink.NewPipeline(events.NewBus(), capturingFactory{sink: &fakeSink{}, got: &eventsink.Settings{}},
		eventsink.Settings{Config: map[string]any{"headers": map[string]any{
			"Authorization": map[string]any{"env": "RELAY_TEST_SINK_SET"},
			"X-Missing":     map[string]any{"env": "RELAY_TEST_SINK_UNSET"},
		}}})
	require.ErrorContains(t, err, `event sink "capture"`)
	require.ErrorContains(t, err, `setting "headers.X-Missing"`)
	require.ErrorContains(t, err, `"RELAY_TEST_SINK_UNSET"`)
	require.NotContains(t, err.Error(), "must-not-appear")
}
