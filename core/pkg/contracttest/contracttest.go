// Package contracttest is what the contract runners of the component types (UC-008) have in common: the shape of a
// result, and the rules for a run that cannot exercise the contract. An Extension Author hands a runner the factory and
// the test inputs, and gets a Report; Run turns it into subtests, one per case.
//
// A run fails when a case fails, and also when it is aborted, because a test input is missing or the declarations are
// invalid. A case the component does not have is declared not applicable by the author, never left out by accident.
package contracttest

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// Case is the outcome of one contract case.
type Case struct {
	Name   string
	Passed bool
	// Expected and Observed say what the contract asks and what the component did. A case that runs in a mode the
	// author declared (such as a component without configuration) states the mode in Expected.
	Expected string
	Observed string
	// NotApplicable is why the case was not exercised: the author named its feature in Without. Such a case is
	// neither passed nor failed.
	NotApplicable string
}

// Pass is a case that passed.
func Pass(name, expected string) Case {
	return Case{Name: name, Passed: true, Expected: expected}
}

// Check makes a case from what it observed: it passed when observed is empty.
func Check(name, expected, observed string) Case {
	return Case{Name: name, Passed: observed == "", Expected: expected, Observed: observed}
}

// NotApplicable is a case the author declared not applicable, with the reason.
func NotApplicable(name, reason string) Case {
	return Case{Name: name, NotApplicable: reason}
}

// Report is the outcome of a run.
type Report struct {
	// Aborted says why no contract case was exercised: a missing test input, an invalid declaration or a factory that
	// cannot create the component.
	Aborted string
	Cases   []Case
}

// Abort is a report of a run that could not exercise the contract.
func Abort(format string, args ...any) Report {
	return Report{Aborted: fmt.Sprintf(format, args...)}
}

// Failed reports whether the run is a failure: it was aborted or a case failed. A case that is not applicable does
// not fail it.
func (r Report) Failed() bool {
	return r.Aborted != "" || len(r.FailedCases()) > 0
}

// FailedCases names the cases that failed.
func (r Report) FailedCases() []string {
	var names []string
	for _, c := range r.Cases {
		if c.NotApplicable == "" && !c.Passed {
			names = append(names, c.Name)
		}
	}
	return names
}

// Run reports the cases as subtests of t, one per case. An aborted run fails t once, naming why. A case that is not
// applicable is logged with its reason and does not fail.
func (r Report) Run(t *testing.T) {
	t.Helper()
	if r.Aborted != "" {
		t.Fatalf("the contract tests were not run: %s", r.Aborted)
	}
	for _, c := range r.Cases {
		t.Run(c.Name, func(t *testing.T) {
			switch {
			case c.NotApplicable != "":
				t.Logf("not applicable (%s)", c.NotApplicable)
			case !c.Passed:
				t.Errorf("expected: %s\nobserved: %s", c.Expected, c.Observed)
			}
		})
	}
}

// Need says a test input is missing when it is not present, naming the cases that need it. It returns "" for a present
// input. Join the messages of all inputs with Missing.
func Need(input string, present bool, cases ...string) string {
	if present {
		return ""
	}
	quoted := make([]string, len(cases))
	for i, c := range cases {
		quoted[i] = fmt.Sprintf("%q", c)
	}
	return fmt.Sprintf("test input %s is missing, needed by the cases %s", input, strings.Join(quoted, ", "))
}

// Missing joins the messages of the missing inputs, dropping the empty ones of the inputs that are present.
func Missing(needs ...string) string {
	var parts []string
	for _, n := range needs {
		if n != "" {
			parts = append(parts, n)
		}
	}
	return strings.Join(parts, "; ")
}

// ValidateWithout checks the features an author declared not applicable. Each must be a feature of the contract, and
// none may be supplied: a feature either has its test input or is named in Without, never both. supplied maps every
// feature of the contract to whether its input is set.
func ValidateWithout[F ~string](without []F, supplied map[F]bool) error {
	var known []F
	for f := range supplied {
		known = append(known, f)
	}
	slices.Sort(known)

	var errs []error
	for _, f := range without {
		set, ok := supplied[f]
		switch {
		case !ok:
			errs = append(errs, fmt.Errorf("expected Without to name features of the contract: %v\nobserved: %q", known, f))
		case set:
			errs = append(errs, fmt.Errorf("expected Without not to name a feature whose input is set\nobserved: %q is both set and named", f))
		}
	}
	return errors.Join(errs...)
}
