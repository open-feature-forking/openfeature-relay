package eventcollector

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/stretchr/testify/require"
)

var parseNow = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// TestParseEvent pins the event each item of a batch becomes, or the exact reason it is refused.
func TestParseEvent(t *testing.T) {
	at := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	evaluation := func(e events.Evaluation) events.Evaluation {
		e.Endpoint, e.Origin = Type, events.OriginInProcess
		if e.Time.IsZero() {
			e.Time = parseNow
		}
		return e
	}
	tracking := func(e events.Tracking) events.Tracking {
		e.Origin = events.OriginInProcess
		if e.Time.IsZero() {
			e.Time = parseNow
		}
		return e
	}

	for name, tt := range map[string]struct {
		raw    string
		want   events.Event
		reason string
	}{
		"not an object": {raw: `[1]`, reason: "the event must be a JSON object"},
		"null":          {raw: `null`, reason: "the event must be a JSON object"},
		"no kind":       {raw: `{}`, reason: `the "kind" must be "evaluation" or "tracking"`},
		"unknown kind":  {raw: `{"kind":"other"}`, reason: `the "kind" must be "evaluation" or "tracking"`},

		"evaluation, every field": {
			raw: `{"kind":"evaluation","time":"2026-02-03T04:05:06Z","flagKey":"k","flagSet":"s","format":"f",` +
				`"variant":"on","reason":"STATIC","errorCode":"","targetingKey":"u","value":true}`,
			want: evaluation(events.Evaluation{
				Time: at, FlagKey: "k", FlagSet: "s", Format: "f", Variant: "on", Reason: "STATIC", TargetingKey: "u",
				Value: true, HasValue: true,
			}),
		},
		"evaluation, integer value": {
			raw:  `{"kind":"evaluation","flagKey":"k","value":3}`,
			want: evaluation(events.Evaluation{FlagKey: "k", Value: int64(3), HasValue: true}),
		},
		"evaluation, float value": {
			raw:  `{"kind":"evaluation","flagKey":"k","value":1.5}`,
			want: evaluation(events.Evaluation{FlagKey: "k", Value: 1.5, HasValue: true}),
		},
		"evaluation, nested value": {
			raw: `{"kind":"evaluation","flagKey":"k","value":{"a":1,"b":[2]}}`,
			want: evaluation(events.Evaluation{
				FlagKey: "k", Value: map[string]any{"a": 1.0, "b": []any{2.0}}, HasValue: true,
			}),
		},
		"evaluation, null value": {
			raw:  `{"kind":"evaluation","flagKey":"k","value":null}`,
			want: evaluation(events.Evaluation{FlagKey: "k"}),
		},
		"evaluation, hasValue overrides a value": {
			raw:  `{"kind":"evaluation","flagKey":"k","value":1,"hasValue":false}`,
			want: evaluation(events.Evaluation{FlagKey: "k", Value: int64(1)}),
		},
		"evaluation, hasValue without a value": {
			raw:  `{"kind":"evaluation","flagKey":"k","hasValue":true}`,
			want: evaluation(events.Evaluation{FlagKey: "k", HasValue: true}),
		},
		"evaluation, hasValue no boolean": {
			raw: `{"kind":"evaluation","flagKey":"k","hasValue":"yes"}`, reason: `"hasValue" must be a boolean`,
		},
		"evaluation, no flagKey":    {raw: `{"kind":"evaluation"}`, reason: `an evaluation event needs a "flagKey"`},
		"evaluation, empty flagKey": {raw: `{"kind":"evaluation","flagKey":""}`, reason: `an evaluation event needs a "flagKey"`},
		"evaluation, flagKey no string": {
			raw: `{"kind":"evaluation","flagKey":1}`, reason: `"flagKey" must be a string`,
		},
		"evaluation, flagSet no string": {
			raw: `{"kind":"evaluation","flagKey":"k","flagSet":1}`, reason: `"flagSet" must be a string`,
		},
		"evaluation, format no string": {
			raw: `{"kind":"evaluation","flagKey":"k","format":true}`, reason: `"format" must be a string`,
		},
		"evaluation, variant no string": {
			raw: `{"kind":"evaluation","flagKey":"k","variant":[]}`, reason: `"variant" must be a string`,
		},
		"evaluation, reason no string": {
			raw: `{"kind":"evaluation","flagKey":"k","reason":{}}`, reason: `"reason" must be a string`,
		},
		"evaluation, errorCode no string": {
			raw: `{"kind":"evaluation","flagKey":"k","errorCode":2}`, reason: `"errorCode" must be a string`,
		},
		"evaluation, targetingKey no string": {
			raw: `{"kind":"evaluation","flagKey":"k","targetingKey":2}`, reason: `"targetingKey" must be a string`,
		},
		"evaluation, a null field is absent": {
			raw:  `{"kind":"evaluation","flagKey":"k","variant":null}`,
			want: evaluation(events.Evaluation{FlagKey: "k"}),
		},

		"time absent":  {raw: `{"kind":"tracking","name":"n"}`, want: tracking(events.Tracking{EventName: "n"})},
		"time empty":   {raw: `{"kind":"tracking","name":"n","time":""}`, want: tracking(events.Tracking{EventName: "n"})},
		"time invalid": {raw: `{"kind":"tracking","name":"n","time":"yesterday"}`, reason: `"time" must be an RFC 3339 time`},
		"time no string": {
			raw: `{"kind":"tracking","name":"n","time":5}`, reason: `"time" must be a string`,
		},

		"tracking, every field": {
			raw: `{"kind":"tracking","time":"2026-02-03T04:05:06Z","name":"n","targetingKey":"u","value":2,` +
				`"details":{"a":1,"b":"x"}}`,
			want: tracking(events.Tracking{
				Time: at, EventName: "n", TargetingKey: "u", Value: 2, Details: map[string]any{"a": 1.0, "b": "x"},
			}),
		},
		"tracking, null value and details": {
			raw:  `{"kind":"tracking","name":"n","value":null,"details":null}`,
			want: tracking(events.Tracking{EventName: "n"}),
		},
		"tracking, no name":        {raw: `{"kind":"tracking"}`, reason: `a tracking event needs a "name"`},
		"tracking, name no string": {raw: `{"kind":"tracking","name":1}`, reason: `"name" must be a string`},
		"tracking, targetingKey no string": {
			raw: `{"kind":"tracking","name":"n","targetingKey":1}`, reason: `"targetingKey" must be a string`,
		},
		"tracking, value no number": {
			raw: `{"kind":"tracking","name":"n","value":"2"}`, reason: `"value" of a tracking event must be a number`,
		},
		"tracking, details no object": {
			raw: `{"kind":"tracking","name":"n","details":[1]}`, reason: `"details" must be an object`,
		},

		"data after the object is ignored": {
			raw: `{"kind":"tracking","name":"n"} trailing`, want: tracking(events.Tracking{EventName: "n"}),
		},
		"a duplicate key: the last wins": {
			raw: `{"kind":"tracking","name":"first","name":"last"}`, want: tracking(events.Tracking{EventName: "last"}),
		},

		"bad time before an unknown kind": {raw: `{"kind":"other","time":"x"}`, reason: `"time" must be an RFC 3339 time`},
		"bad time before a missing flagKey": {
			raw: `{"kind":"evaluation","time":"x"}`, reason: `"time" must be an RFC 3339 time`,
		},
		"a wrong-typed field before a missing flagKey": {
			raw: `{"kind":"evaluation","variant":1}`, reason: `"variant" must be a string`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, reason := parseEvent(json.RawMessage(tt.raw), parseNow)
			require.Equal(t, tt.reason, reason)
			require.Equal(t, tt.want, got)
		})
	}
}

// TestRandomChoiceBetweenTwoFaultsIsKept pins a decided quirk (#177, Q2 (a)): with two wrong-typed fields the
// reported one depends on the map iteration, so over many calls both are reported. Making the choice
// deterministic is a change of its own.
func TestRandomChoiceBetweenTwoFaultsIsKept(t *testing.T) {
	raw := json.RawMessage(`{"kind":"evaluation","flagKey":"k","variant":1,"reason":2}`)
	seen := map[string]bool{}
	for range 1000 {
		_, reason := parseEvent(raw, parseNow)
		seen[reason] = true
	}
	require.Equal(t, map[string]bool{`"variant" must be a string`: true, `"reason" must be a string`: true}, seen)
}
