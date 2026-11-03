package evaluatortest

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/stretchr/testify/require"
)

// fakeTB records what the contract reports instead of failing the test.
type fakeTB struct {
	mu     sync.Mutex
	errors []string
}

func (f *fakeTB) Helper() {}
func (f *fakeTB) Errorf(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errors = append(f.errors, fmt.Sprintf(format, args...))
}
func (f *fakeTB) Fatalf(format string, args ...any) { f.Errorf(format, args...); panic("fatal") }
func (f *fakeTB) Logf(string, ...any)               {}

// flaws are the ways the reference evaluator can be broken.
type flaws struct {
	unknownCode     string // error code of an unknown key, FLAG_NOT_FOUND when empty
	unknownNoError  bool
	fallbackReason  bool
	lowerReason     bool
	disabledValue   bool
	absentZeroValue bool
	bulkDiffers     bool
	ignoreAttrs     bool
	tornPair        bool
	ignoresDst      bool
	noFlagSet       bool
}

// reference is a small evaluator over fixed flags, with a pair of flags updated together.
type reference struct {
	flaws flaws
	round atomic.Int64
}

func (e *reference) Type() string                { return "reference" }
func (e *reference) ConfigurationFormat() string { return "reference" }
func (e *reference) Create(evaluator.Settings) (evaluator.Evaluator, error) {
	return e, nil
}

func (e *reference) result(key string, req evaluator.Request, round int64) evaluator.Result {
	static := evaluator.Result{FlagKey: key, HasValue: true, Variant: "v", Reason: model.StaticReason}
	if e.flaws.lowerReason {
		static.Reason = "static"
	}
	switch key {
	case "bool":
		static.Value = true
		if e.flaws.bulkDiffers && req.Bulk {
			static.Value = false
		}
		return static
	case "int":
		static.Value = 10.0
		return static
	case "disabled":
		r := evaluator.Result{FlagKey: key, Reason: model.DisabledReason}
		if e.flaws.disabledValue {
			r.HasValue, r.Value = true, false
		}
		return r
	case "absent":
		r := evaluator.Result{FlagKey: key, Reason: model.DefaultReason}
		if e.flaws.fallbackReason {
			r.Reason = model.FallbackReason
		}
		if e.flaws.absentZeroValue {
			r.HasValue, r.Value = true, false
		}
		return r
	case "audience":
		if req.Attributes["audience"] == "client" || e.flaws.ignoreAttrs {
			static.Value = "client-visible"
			return static
		}
		return evaluator.Result{FlagKey: key, Reason: model.DefaultReason}
	case "pair-a", "pair-b":
		static.Value = float64(round)
		if key == "pair-a" && e.flaws.tornPair {
			// the pair changes between its two flags
			e.round.Add(1)
		}
		return static
	}
	if e.flaws.unknownNoError {
		return evaluator.Result{FlagKey: key, Reason: model.DefaultReason}
	}
	code := model.FlagNotFoundErrorCode
	if e.flaws.unknownCode != "" {
		code = e.flaws.unknownCode
	}
	return evaluator.Result{FlagKey: key, ErrorCode: code, Reason: model.ErrorReason}
}

func (e *reference) Evaluate(_ context.Context, req evaluator.Request, dst []evaluator.Result) ([]evaluator.Result, model.Metadata, error) {
	// one snapshot per request, unless the evaluator is flawed
	round := e.round.Load()
	if e.flaws.ignoresDst {
		dst = nil
	}
	switch {
	case req.Bulk:
		for _, key := range []string{"audience", "absent", "bool", "disabled", "int", "pair-a", "pair-b"} {
			dst = append(dst, e.result(key, req, e.roundFor(round)))
		}
		for _, set := range []string{"a", "b"} {
			res := evaluator.Result{FlagKey: "shared", FlagSet: set, HasValue: true, Value: set, Variant: "v", Reason: model.StaticReason}
			if e.flaws.noFlagSet {
				res.FlagSet = ""
			}
			dst = append(dst, res)
		}
	case req.FlagKeys != nil:
		for _, key := range req.FlagKeys {
			dst = append(dst, e.result(key, req, e.roundFor(round)))
		}
	default:
		dst = append(dst, e.result(req.FlagKey, req, e.roundFor(round)))
	}
	return dst, nil, nil
}

func samplesFor(e *reference) Samples {
	return Samples{
		Static: []StaticSample{
			{Key: "bool", Hint: evaluator.TypeBool, Value: true},
			{Key: "int", Hint: evaluator.TypeInt, Value: 10.0},
		},
		Shared:        &SharedSample{Key: "shared", FlagSets: []string{"a", "b"}},
		Disabled:      "disabled",
		AbsentDefault: "absent",
		Attribute:     &AttributeSample{Key: "audience", Attributes: map[string]any{"audience": "client"}, With: "client-visible"},
		BulkOnly:      nil,
		Paired: &PairedSample{
			Keys: [2]string{"pair-a", "pair-b"},
			Write: func(_ *store.Store, round int) error {
				e.round.Store(int64(round))
				return nil
			},
			Value: func(r evaluator.Result) (int, bool) {
				v, ok := r.Value.(float64)
				return int(v), ok
			},
		},
	}
}

func TestContractAcceptsAConformingEvaluator(t *testing.T) {
	e := &reference{}
	Run(t, e, samplesFor(e))
}

func TestContractReportsExpectedAndObserved(t *testing.T) {
	tests := map[string]struct {
		flaws flaws
		// the case the failure is reported under
		reported string
	}{
		"error code outside OpenFeature's": {flaws{unknownCode: "NOT_FOUND"}, "error codes and reasons"},
		"FALLBACK leaves the evaluator":    {flaws{fallbackReason: true}, "error codes and reasons"},
		"reason in lower case":             {flaws{lowerReason: true}, "error codes and reasons"},
		"disabled flag has a value":        {flaws{disabledValue: true}, "disabled"},
		"zero value for an absent default": {flaws{absentZeroValue: true}, "absent default"},
		"unknown key without an error":     {flaws{unknownNoError: true}, "unknown key"},
		"bulk differs from single":         {flaws{bulkDiffers: true}, "bulk equals single"},
		"attributes ignored":               {flaws{ignoreAttrs: true}, "attributes"},
		"torn read of a pair":              {flaws{tornPair: true}, "consistent resolution"},
		"bulk entries without a flag set":  {flaws{noFlagSet: true}, "bulk flag sets"},
		"results replace dst":              {flaws{ignoresDst: true}, "append to dst"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			e := &reference{flaws: tt.flaws}
			tb := &fakeTB{}
			Run(tb, e, samplesFor(e))

			require.NotEmpty(t, tb.errors, "the contract accepted a broken evaluator")
			var matching []string
			for _, msg := range tb.errors {
				if strings.HasPrefix(msg, tt.reported+":") {
					matching = append(matching, msg)
				}
			}
			require.NotEmpty(t, matching, "no failure under %q in %v", tt.reported, tb.errors)
			for _, msg := range matching {
				require.Contains(t, msg, "expected")
				require.Contains(t, msg, "observed")
			}
		})
	}
}

func TestContractReportsFactoryProblems(t *testing.T) {
	tb := &fakeTB{}
	Run(tb, nameless{}, Samples{})
	require.Len(t, tb.errors, 2)
	for _, msg := range tb.errors {
		require.Contains(t, msg, "expected")
		require.Contains(t, msg, "observed")
	}
}

type nameless struct{}

func (nameless) Type() string                { return "" }
func (nameless) ConfigurationFormat() string { return "" }
func (nameless) Create(evaluator.Settings) (evaluator.Evaluator, error) {
	return &reference{}, nil
}

// roundFor is the round a flag is read at: the snapshot, or the current value when the pair can tear.
func (e *reference) roundFor(snapshot int64) int64 {
	if e.flaws.tornPair {
		return e.round.Load()
	}
	return snapshot
}

// A bulk result without a value may carry a typed zero value; the single result of the same flag carries none.
func TestSameIgnoresTheValueOfResultsWithoutOne(t *testing.T) {
	single := evaluator.Result{FlagKey: "k", Reason: model.DisabledReason}
	bulk := single
	bulk.Value = false
	require.True(t, same(single, bulk))

	single.HasValue, bulk.HasValue = true, true
	single.Value = true
	require.False(t, same(single, bulk))
}
