// Package evaluatortestkit runs the testbed's evaluator suite against any evaluator whose configuration format
// is flagd's.
package evaluatortestkit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"
	"github.com/open-feature-forking/openfeature-relay/configformat/flagd"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator/typed"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
)

// The testbed's evaluator suite: its features and the flags they evaluate. The submodule is part of the
// repository's test setup, so a missing one is a failure and never a skip.
const source = "testkit"

// suiteDir is the suite's directory in the testbed submodule, found from this file so that callers in other
// modules reach it too.
var suiteDir = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "test-harness", "evaluator")
}()

var flagsFile = filepath.Join(suiteDir, "flags", "testkit-flags.json")

// excludedTags lists the scenarios of the suite this evaluator is not expected to pass, with the reason.
// flagd's own test-harness run excludes the same two tags.
var excludedTags = []struct{ tag, reason string }{
	{"@fractional-v1", "the suite's legacy float-based bucketing differs from the buckets flagd's fractional operator assigns"},
	{"@fractional-v2", "the suite's integer bucketing ((hash * totalWeight) >> 32) differs from the buckets flagd's fractional operator " +
		"assigns; the scenarios of the suite that carry only this tag (a single entry, hash edge cases) are excluded with it"},
}

func tagExpression() string {
	var parts []string
	for _, e := range excludedTags {
		parts = append(parts, "~"+e.tag)
	}
	return strings.Join(parts, " && ")
}

// Run evaluates the suite with the evaluator the factory creates, resolved through the typed adapter as endpoints
// and in-process providers do.
func Run(t *testing.T, factory evaluator.Factory) {
	t.Helper()
	for _, p := range []string{suiteDir + "/gherkin", flagsFile} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("the testbed's evaluator suite is missing (%v): run `git submodule update --init test-harness`", err)
		}
	}

	suite := godog.TestSuite{
		Name:                "evaluator",
		ScenarioInitializer: func(sc *godog.ScenarioContext) { initializeScenario(sc, factory) },
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{filepath.Join(suiteDir, "gherkin")},
			Tags:     tagExpression(),
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("scenarios of the evaluator suite failed")
	}
}

// world is the state of one scenario: the evaluator under test and the evaluation being described.
type world struct {
	factory   evaluator.Factory
	evaluator evaluator.IResolver

	flagType string
	flagKey  string
	fallback any
	context  map[string]any

	details details
}

// details is what the scenarios assert on, as an OpenFeature client would report it.
type details struct {
	value     any
	reason    string
	errorCode string
	metadata  model.Metadata
}

func initializeScenario(sc *godog.ScenarioContext, factory evaluator.Factory) {
	w := &world{factory: factory}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = world{factory: factory, context: map[string]any{}}
		return ctx, nil
	})

	sc.Step(`^an evaluator$`, w.anEvaluator)
	sc.Step(`^an? ([A-Za-z]+)-flag with key "([^"]*)" and a fallback value "((?:[^"\\]|\\.)*)"$`, w.aFlag)
	sc.Step(`^a context containing a key "([^"]*)", with type "([^"]*)" and with value "((?:[^"\\]|\\.)*)"$`, w.aContextKey)
	sc.Step(`^a context containing a targeting key with value "([^"]*)"$`, w.aTargetingKey)
	sc.Step(`^a context containing a nested property with outer key "([^"]*)" and inner key "([^"]*)", with value "([^"]*)"$`, w.aNestedProperty)
	sc.Step(`^the flag was evaluated with details$`, w.evaluate)
	sc.Step(`^the resolved details value should be "((?:[^"\\]|\\.)*)"$`, w.valueShouldBe)
	sc.Step(`^the reason should be "([^"]*)"$`, w.reasonShouldBe)
	sc.Step(`^the error-code should be "([^"]*)"$`, w.errorCodeShouldBe)
	sc.Step(`^the resolved metadata should contain$`, w.metadataShouldContain)
	sc.Step(`^the resolved metadata is empty$`, w.metadataIsEmpty)
}

// anEvaluator builds the evaluator over a store that flagd's configuration format loaded the
// suite's flags into.
func (w *world) anEvaluator() error {
	data, err := os.ReadFile(flagsFile)
	if err != nil {
		return fmt.Errorf("reading the suite's flags: %w", err)
	}
	log := logger.NewLogger(nil, false)
	st, err := store.NewStore(log, []string{source})
	if err != nil {
		return fmt.Errorf("creating the store: %w", err)
	}
	if _, err := flagd.NewFormat(st, log).Accept(context.Background(), configformat.Input{Source: source, Data: string(data)}); err != nil {
		return fmt.Errorf("loading the suite's flags: %w", err)
	}
	ev, err := w.factory.Create(evaluator.Settings{Store: st, Logger: log})
	if err != nil {
		return fmt.Errorf("creating the evaluator: %w", err)
	}
	w.evaluator = typed.New(ev)
	return nil
}

func (w *world) aFlag(flagType, key, fallback string) error {
	value, err := parseTyped(flagType, fallback)
	if err != nil {
		return fmt.Errorf("fallback value of flag %q: %w", key, err)
	}
	w.flagType, w.flagKey, w.fallback = flagType, key, value
	return nil
}

func (w *world) aContextKey(key, valueType, value string) error {
	v, err := parseTyped(valueType, value)
	if err != nil {
		return fmt.Errorf("context key %q: %w", key, err)
	}
	w.context[key] = v
	return nil
}

func (w *world) aTargetingKey(value string) error {
	w.context["targetingKey"] = value
	return nil
}

func (w *world) aNestedProperty(outer, inner, value string) error {
	w.context[outer] = map[string]any{inner: value}
	return nil
}

// parseTyped reads a value of one of the suite's types from its text in a feature file.
func parseTyped(valueType, text string) (any, error) {
	switch valueType {
	case "Boolean":
		return strconv.ParseBool(text)
	case "String":
		return text, nil
	case "Integer":
		return strconv.ParseInt(text, 10, 64)
	case "Float":
		return strconv.ParseFloat(text, 64)
	case "Object":
		// a feature file cannot contain a bare double quote in a table cell
		var v map[string]any
		if err := json.Unmarshal([]byte(strings.ReplaceAll(text, `\"`, `"`)), &v); err != nil {
			return nil, fmt.Errorf("object %q: %w", text, err)
		}
		return v, nil
	default:
		return nil, fmt.Errorf("unknown type %q", valueType)
	}
}

// evaluate resolves the flag the way an in-process provider reports it: an error resolves to the
// fallback with reason ERROR, a disabled flag to the fallback with reason DISABLED, and a flag with no
// variant to the fallback with the evaluator's reason.
func (w *world) evaluate() error {
	if w.evaluator == nil {
		return fmt.Errorf("no evaluator")
	}
	ctx := context.Background()
	var (
		value    any
		variant  string
		reason   string
		metadata model.Metadata
		err      error
	)
	switch w.flagType {
	case "Boolean":
		value, variant, reason, metadata, err = resolve(w.evaluator.ResolveBooleanValue, ctx, w)
	case "String":
		value, variant, reason, metadata, err = resolve(w.evaluator.ResolveStringValue, ctx, w)
	case "Integer":
		value, variant, reason, metadata, err = resolve(w.evaluator.ResolveIntValue, ctx, w)
	case "Float":
		value, variant, reason, metadata, err = resolve(w.evaluator.ResolveFloatValue, ctx, w)
	case "Object":
		value, variant, reason, metadata, err = resolve(w.evaluator.ResolveObjectValue, ctx, w)
	default:
		return fmt.Errorf("unknown flag type %q", w.flagType)
	}

	w.details = details{value: value, reason: reason, metadata: metadata}
	switch {
	case err != nil && err.Error() == model.FlagDisabledErrorCode:
		w.details.value, w.details.reason = w.fallback, model.DisabledReason
	case err != nil:
		w.details.value, w.details.reason, w.details.errorCode = w.fallback, model.ErrorReason, err.Error()
	case variant == "":
		w.details.value = w.fallback
	}
	if w.details.reason == model.FallbackReason {
		// an internal reason that the API reports as the default
		w.details.reason = model.DefaultReason
	}
	return nil
}

func resolve[T any](
	f func(context.Context, string, string, map[string]any) (T, string, string, model.Metadata, error),
	ctx context.Context, w *world,
) (any, string, string, model.Metadata, error) {
	value, variant, reason, metadata, err := f(ctx, "testkit", w.flagKey, w.context)
	return value, variant, reason, metadata, err
}

func (w *world) valueShouldBe(expected string) error {
	want, err := parseTyped(w.flagType, expected)
	if err != nil {
		return fmt.Errorf("expected value: %w", err)
	}
	if !equal(want, w.details.value) {
		return fmt.Errorf("resolved value: want %#v, got %#v", want, w.details.value)
	}
	return nil
}

func (w *world) reasonShouldBe(expected string) error {
	if w.details.reason != expected {
		return fmt.Errorf("reason: want %q, got %q", expected, w.details.reason)
	}
	return nil
}

func (w *world) errorCodeShouldBe(expected string) error {
	if w.details.errorCode != expected {
		return fmt.Errorf("error code: want %q, got %q", expected, w.details.errorCode)
	}
	return nil
}

func (w *world) metadataShouldContain(table *godog.Table) error {
	for _, row := range table.Rows[1:] {
		key, valueType, text := row.Cells[0].Value, row.Cells[1].Value, row.Cells[2].Value
		want, err := parseTyped(valueType, text)
		if err != nil {
			return fmt.Errorf("metadata %q: %w", key, err)
		}
		got, ok := w.details.metadata[key]
		if !ok {
			return fmt.Errorf("metadata %q is missing from %v", key, w.details.metadata)
		}
		if !equal(want, got) {
			return fmt.Errorf("metadata %q: want %#v, got %#v", key, want, got)
		}
	}
	return nil
}

func (w *world) metadataIsEmpty() error {
	if len(w.details.metadata) != 0 {
		return fmt.Errorf("want no metadata, got %v", w.details.metadata)
	}
	return nil
}

// equal compares values the way JSON would carry them, so that an int64 and a float64 holding the same
// number are equal.
func equal(want, got any) bool {
	if reflect.DeepEqual(want, got) {
		return true
	}
	a, errA := json.Marshal(want)
	b, errB := json.Marshal(got)
	return errA == nil && errB == nil && string(a) == string(b)
}
