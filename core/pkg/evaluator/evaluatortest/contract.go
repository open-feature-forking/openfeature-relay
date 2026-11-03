// Package evaluatortest is the contract every evaluator keeps, whatever configuration format it reads. The
// author of an evaluator describes flags in the evaluator's own format as Samples, and Run checks what the
// results must look like for all of them.
package evaluatortest

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"sync"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
)

// Source is the name of the source Samples.Setup delivers its flags under.
const Source = "contract"

// UnknownKey is a key no sample defines.
const UnknownKey = "contract-unknown-key"

// T is the part of *testing.T the contract uses; the contract's own tests supply a recording one.
type T interface {
	Helper()
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
	Logf(format string, args ...any)
}

// StaticSample is a flag that always resolves to Value.
type StaticSample struct {
	Key  string
	Hint evaluator.Type
	// Value is what Result.Value holds; numbers are float64.
	Value any
}

// AttributeSample is a flag whose resolution depends on the request's attributes.
type AttributeSample struct {
	Key        string
	Attributes map[string]any
	// With is the value the flag resolves to when the request carries Attributes. Without them it must not.
	With any
}

// PairedSample is two flags that Write always changes together.
type PairedSample struct {
	Keys [2]string
	// Write updates both flags to the round's value in one delivery.
	Write func(st *store.Store, round int) error
	// Value reads the round back from a result.
	Value func(r evaluator.Result) (round int, ok bool)
}

// Samples describes flags in the evaluator's format. Absent parts are not checked.
type Samples struct {
	// Setup delivers the flags below to st as source Source, through the evaluator's configuration format.
	Setup func(st *store.Store) error
	// Config holds the settings handed to the factory.
	Config map[string]any

	Static []StaticSample
	// Disabled names a disabled flag.
	Disabled string
	// AbsentDefault names an enabled flag that has no value to resolve to without targeting.
	AbsentDefault string
	Attribute     *AttributeSample
	Paired        *PairedSample
	// BulkOnly names keys whose bulk result may differ from their single result, because the evaluator
	// documents a bulk-only rule for them.
	BulkOnly []string
}

var (
	errorCodes = map[string]bool{
		model.FlagNotFoundErrorCode: true, model.ParseErrorCode: true, model.TypeMismatchErrorCode: true,
		model.GeneralErrorCode: true, model.InvalidContextCode: true,
		"TARGETING_KEY_MISSING": true, "PROVIDER_NOT_READY": true, "PROVIDER_FATAL": true,
	}
	reasons = map[string]bool{
		"STATIC": true, "DEFAULT": true, "TARGETING_MATCH": true, "SPLIT": true, "CACHED": true,
		"DISABLED": true, "UNKNOWN": true, "STALE": true, "ERROR": true,
	}
	vendorReason = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
)

func (s Samples) keys() []string {
	var keys []string
	for _, st := range s.Static {
		keys = append(keys, st.Key)
	}
	for _, k := range []string{s.Disabled, s.AbsentDefault} {
		if k != "" {
			keys = append(keys, k)
		}
	}
	if s.Attribute != nil {
		keys = append(keys, s.Attribute.Key)
	}
	if s.Paired != nil {
		keys = append(keys, s.Paired.Keys[:]...)
	}
	return keys
}

type run struct {
	t       T
	factory evaluator.Factory
	samples Samples
}

// expect reports a failure naming the case, what was expected and what was observed.
func (r run) expect(name string, ok bool, what string, expected, observed any) {
	r.t.Helper()
	if !ok {
		r.t.Errorf("%s: %s: expected %v, observed %v", name, what, expected, observed)
	}
}

func (r run) create() (evaluator.Evaluator, *store.Store) {
	r.t.Helper()
	log := logger.NewLogger(nil, false)
	st, err := store.NewStore(log, []string{Source})
	if err != nil {
		r.t.Fatalf("creating the store: %v", err)
	}
	if r.samples.Setup != nil {
		if err := r.samples.Setup(st); err != nil {
			r.t.Fatalf("delivering the samples: %v", err)
		}
	}
	ev, err := r.factory.Create(evaluator.Settings{Store: st, Logger: log, Config: r.samples.Config})
	if err != nil {
		r.t.Fatalf("creating the evaluator: %v", err)
	}
	return ev, st
}

func evaluate(ev evaluator.Evaluator, req evaluator.Request) ([]evaluator.Result, error) {
	results, _, err := ev.Evaluate(context.Background(), req, nil)
	return results, err
}

func (r run) single(ev evaluator.Evaluator, key string, hint evaluator.Type, attributes map[string]any) (evaluator.Result, bool) {
	r.t.Helper()
	results, err := evaluate(ev, evaluator.Request{FlagKey: key, TypeHint: hint, Attributes: attributes})
	if err != nil {
		r.t.Errorf("evaluating %s: expected no error, observed %v", key, err)
		return evaluator.Result{}, false
	}
	if len(results) != 1 {
		r.t.Errorf("evaluating %s: expected 1 result, observed %d", key, len(results))
		return evaluator.Result{}, false
	}
	return results[0], true
}

// Run checks the evaluator the factory creates against the samples.
func Run(t T, factory evaluator.Factory, samples Samples) {
	t.Helper()
	r := run{t: t, factory: factory, samples: samples}

	r.expect("factory", factory.Type() != "", "type", "a name", `""`)
	r.expect("factory", factory.ConfigurationFormat() != "", "configuration format", "a name", `""`)

	r.staticValues()
	r.errorCodesAndReasons()
	r.disabled()
	r.absentDefault()
	r.unknownKey()
	r.bulkEqualsSingle()
	r.attributes()
	r.consistentResolution()
}

func (r run) staticValues() {
	r.t.Helper()
	ev, _ := r.create()
	for _, s := range r.samples.Static {
		res, ok := r.single(ev, s.Key, s.Hint, nil)
		if !ok {
			continue
		}
		r.expect("static values", res.FlagKey == s.Key, s.Key+" flag key", s.Key, res.FlagKey)
		r.expect("static values", res.HasValue && res.ErrorCode == "", s.Key+" has a value",
			"a value", fmt.Sprintf("HasValue=%v ErrorCode=%q", res.HasValue, res.ErrorCode))
		r.expect("static values", reflect.DeepEqual(res.Value, s.Value), s.Key+" value", fmt.Sprintf("%#v", s.Value), fmt.Sprintf("%#v", res.Value))
	}
}

func (r run) checkResult(name, where string, res evaluator.Result) {
	r.t.Helper()
	r.expect(name, res.ErrorCode == "" || errorCodes[res.ErrorCode], where+" error code",
		"empty or an OpenFeature error code", fmt.Sprintf("%q", res.ErrorCode))
	r.expect(name, reasons[res.Reason] || vendorReason.MatchString(res.Reason),
		where+" reason", "an OpenFeature reason or an uppercase vendor reason without parameters", fmt.Sprintf("%q", res.Reason))
	r.expect(name, res.Reason != model.FallbackReason, where+" reason", "DEFAULT with HasValue=false instead of FALLBACK", res.Reason)
}

func (r run) errorCodesAndReasons() {
	r.t.Helper()
	ev, _ := r.create()
	keys := append(r.samples.keys(), UnknownKey)
	for _, key := range keys {
		if res, ok := r.single(ev, key, evaluator.TypeAny, nil); ok {
			r.checkResult("error codes and reasons", key, res)
		}
	}
	if bulk, err := evaluate(ev, evaluator.Request{Bulk: true}); err == nil {
		for _, res := range bulk {
			r.checkResult("error codes and reasons", "bulk "+res.FlagKey, res)
		}
	}
}

func (r run) disabled() {
	r.t.Helper()
	if r.samples.Disabled == "" {
		return
	}
	ev, _ := r.create()
	if res, ok := r.single(ev, r.samples.Disabled, evaluator.TypeBool, nil); ok {
		r.expect("disabled", !res.HasValue && res.ErrorCode == "", "disabled flag",
			"HasValue=false and no error", fmt.Sprintf("HasValue=%v ErrorCode=%q Value=%#v", res.HasValue, res.ErrorCode, res.Value))
	}
}

func (r run) absentDefault() {
	r.t.Helper()
	if r.samples.AbsentDefault == "" {
		return
	}
	ev, _ := r.create()
	for _, hint := range []evaluator.Type{evaluator.TypeAny, evaluator.TypeBool, evaluator.TypeString, evaluator.TypeInt, evaluator.TypeFloat, evaluator.TypeObject} {
		if res, ok := r.single(ev, r.samples.AbsentDefault, hint, nil); ok {
			r.expect("absent default", !res.HasValue && res.Value == nil && res.ErrorCode == "",
				fmt.Sprintf("flag without default, hint %d", hint),
				"HasValue=false, no zero value for the hint, no error",
				fmt.Sprintf("HasValue=%v Value=%#v ErrorCode=%q", res.HasValue, res.Value, res.ErrorCode))
		}
	}
}

func (r run) unknownKey() {
	r.t.Helper()
	ev, _ := r.create()
	if res, ok := r.single(ev, UnknownKey, evaluator.TypeAny, nil); ok {
		r.expect("unknown key", res.ErrorCode == model.FlagNotFoundErrorCode && !res.HasValue, "unknown key",
			model.FlagNotFoundErrorCode, fmt.Sprintf("ErrorCode=%q HasValue=%v", res.ErrorCode, res.HasValue))
	}
}

func same(a, b evaluator.Result) bool {
	return a.HasValue == b.HasValue && reflect.DeepEqual(a.Value, b.Value) &&
		a.Variant == b.Variant && a.Reason == b.Reason && a.ErrorCode == b.ErrorCode
}

func (r run) bulkEqualsSingle() {
	r.t.Helper()
	ev, _ := r.create()
	bulk, err := evaluate(ev, evaluator.Request{Bulk: true})
	if err != nil {
		r.t.Errorf("bulk equals single: bulk evaluation: expected no error, observed %v", err)
		return
	}
	exempt := map[string]bool{}
	for _, k := range r.samples.BulkOnly {
		exempt[k] = true
	}
	byKey := map[string]evaluator.Result{}
	for _, res := range bulk {
		byKey[res.FlagKey] = res
	}
	for _, key := range r.samples.keys() {
		if exempt[key] {
			continue
		}
		got, present := byKey[key]
		if !present {
			r.t.Errorf("bulk equals single: %s: expected a bulk result, observed none", key)
			continue
		}
		want, ok := r.single(ev, key, evaluator.TypeAny, nil)
		if ok && !same(want, got) {
			r.t.Errorf("bulk equals single: %s: expected %+v, observed %+v", key, want, got)
		}
	}
}

func (r run) attributes() {
	r.t.Helper()
	a := r.samples.Attribute
	if a == nil {
		return
	}
	ev, _ := r.create()
	with, ok := r.single(ev, a.Key, evaluator.TypeAny, a.Attributes)
	if ok {
		r.expect("attributes", with.HasValue && reflect.DeepEqual(with.Value, a.With), a.Key+" with attributes",
			fmt.Sprintf("%#v", a.With), fmt.Sprintf("HasValue=%v Value=%#v", with.HasValue, with.Value))
	}
	without, ok := r.single(ev, a.Key, evaluator.TypeAny, nil)
	if ok {
		r.expect("attributes", !without.HasValue || !reflect.DeepEqual(without.Value, a.With), a.Key+" without attributes",
			"a different resolution", fmt.Sprintf("HasValue=%v Value=%#v", without.HasValue, without.Value))
	}
}

// consistentResolution updates two flags together while readers resolve both in one request; with -race
// this is also the test for data races between evaluation and delivery.
func (r run) consistentResolution() {
	r.t.Helper()
	p := r.samples.Paired
	if p == nil {
		return
	}
	ev, st := r.create()
	if err := p.Write(st, 0); err != nil {
		r.t.Fatalf("consistent resolution: writing round 0: %v", err)
	}

	const rounds, readers = 200, 4
	var wg sync.WaitGroup
	var mu sync.Mutex
	failed := false
	fail := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		if !failed {
			failed = true
			r.t.Errorf(format, args...)
		}
	}
	done := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(done)
		for round := 1; round <= rounds; round++ {
			if err := p.Write(st, round); err != nil {
				fail("consistent resolution: writing round %d: expected no error, observed %v", round, err)
				return
			}
		}
	}()
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			keys := p.Keys[:]
			// every reader resolves at least once: a writer that finishes before the readers are scheduled
			// must not leave the probe without an observation
			for observed := false; ; observed = true {
				select {
				case <-done:
					if observed {
						return
					}
				default:
				}
				results, _, err := ev.Evaluate(context.Background(), evaluator.Request{FlagKeys: keys}, nil)
				if err != nil || len(results) != 2 {
					fail("consistent resolution: expected 2 results, observed %d results and error %v", len(results), err)
					return
				}
				sort.Slice(results, func(a, b int) bool { return results[a].FlagKey < results[b].FlagKey })
				first, ok1 := p.Value(results[0])
				second, ok2 := p.Value(results[1])
				if !ok1 || !ok2 {
					fail("consistent resolution: expected round values, observed %+v and %+v", results[0], results[1])
					return
				}
				if first != second {
					fail("consistent resolution: expected %s and %s from one round, observed rounds %d and %d",
						results[0].FlagKey, results[1].FlagKey, first, second)
					return
				}
			}
		}()
	}
	wg.Wait()
}
