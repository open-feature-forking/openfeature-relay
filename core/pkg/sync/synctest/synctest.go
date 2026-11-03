// Package synctest holds the contract tests of the sync component types: fetchers, fetch triggers and streaming sync
// providers (UC-008). An Extension Author hands a runner the component's factory and the test inputs of its type:
//
//	func TestContract(t *testing.T) {
//		synctest.RunFetcher(t, myFactory{}, synctest.FetcherInputs{Source: ..., Change: ..., Break: ...})
//	}
//
// The runners create the component only through its factory and give it only what a distribution injects. Each
// contract case is a subtest. A run that cannot exercise the contract (a missing test input, an invalid declaration,
// a factory that fails) fails as a whole and names why; no case can be skipped.
package synctest

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
)

// DefaultTimeout is the time limit of a contract case that has none of its own.
const DefaultTimeout = 5 * time.Second

// quietPeriod is how long a case waits to conclude that nothing happens.
const quietPeriod = 300 * time.Millisecond

// Case is the outcome of one contract case.
type Case struct {
	Name     string
	Passed   bool
	Expected string
	Observed string
}

// Report is the outcome of a run.
type Report struct {
	// Aborted says why no contract case was exercised: a missing test input, an invalid declaration or a factory
	// that cannot create the component.
	Aborted string
	Cases   []Case
}

// Failed reports whether the run is a failure: it was aborted or a case failed.
func (r Report) Failed() bool {
	if r.Aborted != "" {
		return true
	}
	for _, c := range r.Cases {
		if !c.Passed {
			return true
		}
	}
	return false
}

// FailedCases names the cases that failed.
func (r Report) FailedCases() []string {
	var names []string
	for _, c := range r.Cases {
		if !c.Passed {
			names = append(names, c.Name)
		}
	}
	return names
}

// report shows the outcome in t: one subtest per case, failed with the expected and the observed behaviour.
func (r Report) report(t *testing.T) {
	t.Helper()
	if r.Aborted != "" {
		t.Fatalf("the contract tests were not run: %s", r.Aborted)
	}
	for _, c := range r.Cases {
		t.Run(c.Name, func(t *testing.T) {
			if !c.Passed {
				t.Errorf("expected: %s\nobserved: %s", c.Expected, c.Observed)
			}
		})
	}
}

// RemoveBehaviour is what a streaming sync provider delivers when its source disappears.
type RemoveBehaviour int

const (
	// Keep delivers nothing: the last accepted flags stay in force.
	Keep RemoveBehaviour = iota + 1
	// Empty delivers an empty flag configuration.
	Empty
)

func (b RemoveBehaviour) String() string {
	switch b {
	case Keep:
		return "Keep"
	case Empty:
		return "Empty"
	default:
		return "unset"
	}
}

// Input is a step the Extension Author provides to change the world of the component under test.
type Input func(t testing.TB)

func newSettings(source sync.SourceConfig, typeName string, config map[string]any) sync.Settings {
	if source.Provider == "" {
		source.Provider = typeName
	}
	return sync.Settings{Source: source, Logger: logger.NewLogger(nil, false), Config: config}
}

// checkSchemes checks what a factory that handles sources declares: the schemes, and that ParseURI claims the URIs
// that start with one and declines the others.
func checkSchemes(f sync.SchemeFactory) string {
	if err := sync.ValidateDeclarations(f); err != nil {
		return fmt.Sprintf("invalid declarations: %v", err)
	}
	for _, scheme := range f.Schemes() {
		source, ok := f.ParseURI(scheme + "example")
		if !ok {
			return fmt.Sprintf("ParseURI does not claim a URI that starts with the declared scheme %q", scheme)
		}
		if source.Provider != f.Type() {
			return fmt.Sprintf("ParseURI of a %q URI names the provider %q, not the type %q", scheme, source.Provider, f.Type())
		}
		if source.URI == "" {
			return fmt.Sprintf("ParseURI of a %q URI returns a source without a URI", scheme)
		}
	}
	if _, ok := f.ParseURI("\x00not-a-declared-scheme"); ok {
		return "ParseURI claims a URI that starts with none of the declared schemes"
	}
	return ""
}

// missing builds the message for test inputs that are not set, naming each with the cases that need it.
func missing(inputs ...missingInput) string {
	var parts []string
	for _, in := range inputs {
		if in.set {
			continue
		}
		parts = append(parts, fmt.Sprintf("test input %s is missing, needed by the cases %s", in.name, quoteAll(in.cases)))
	}
	return strings.Join(parts, "; ")
}

type missingInput struct {
	name  string
	set   bool
	cases []string
}

func quoteAll(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = fmt.Sprintf("%q", n)
	}
	return strings.Join(quoted, ", ")
}

// within runs f and reports whether it returned within d.
func within(d time.Duration, f func()) bool {
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// sameJSON reports whether two flag configurations are the same document.
func sameJSON(a, b string) bool {
	var x, y any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return a == b
	}
	return reflect.DeepEqual(x, y)
}

func timeoutOr(d time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return DefaultTimeout
}

func check(name string, ok bool, expected, observed string) Case {
	return Case{Name: name, Passed: ok, Expected: expected, Observed: observed}
}
