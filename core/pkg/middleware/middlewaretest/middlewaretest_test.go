package middlewaretest_test

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware/middlewaretest"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
)

type bug int

const (
	bugNone bug = iota
	bugHidesFlusher
	bugChangesStatus
	bugAnswersAndPasses
	bugNoBinding
	bugLeaksSpan
	bugNotConcurrencySafe
	bugAcceptsInvalid
	bugVagueError
	bugNilMiddleware
	bugDrainsBody
	bugDropsTrailer
	bugBindsUnaskedSelector
	bugBindsWrongType
)

const keyHeader, goodKey = "X-Api-Key", "good"

type stubFactory struct {
	typ string
	bug bug
	// attributesOnly makes the stub bind the attributes and no selector
	attributesOnly bool
}

func (f stubFactory) Type() string { return f.typ }

func (f stubFactory) Create(s middleware.Settings) (middleware.Middleware, error) {
	if f.bug == bugNilMiddleware {
		return nil, nil
	}
	key, ok := s.Config["key"].(string)
	if !ok && f.bug != bugAcceptsInvalid {
		if f.bug == bugVagueError {
			return nil, errors.New("invalid configuration")
		}
		return nil, fmt.Errorf("setting \"key\": %v is not a string", s.Config["key"])
	}
	return &stub{bug: f.bug, key: key, attributesOnly: f.attributesOnly, tracer: s.Telemetry.Tracer("stub")}, nil
}

// stub binds the selector flagSetId=a and the attribute credential=api-key to a request with the right key, and
// answers 401 to any other.
type stub struct {
	bug            bug
	key            string
	attributesOnly bool
	tracer         telemetry.Tracer
	busy           atomic.Bool
}

// writer passes everything on and, with bugChangesStatus, replaces the status.
type writer struct {
	http.ResponseWriter
	force        int
	dropTrailers bool
}

func (w writer) WriteHeader(code int) {
	if w.force != 0 {
		code = w.force
	}
	if w.dropTrailers {
		w.Header().Del("Trailer")
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w writer) Flush() { http.NewResponseController(w.ResponseWriter).Flush() }

func (w writer) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// opaque hides every optional interface of the response writer.
type opaque struct{ http.ResponseWriter }

func (s *stub) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, span := s.tracer.Start(r.Context(), "stub")
		if s.bug != bugLeaksSpan {
			defer span.End()
		}
		if s.bug == bugNotConcurrencySafe {
			if !s.busy.CompareAndSwap(false, true) {
				panic("concurrent use")
			}
			time.Sleep(time.Millisecond)
			defer s.busy.Store(false)
		}
		if r.Header.Get(keyHeader) != s.key {
			w.WriteHeader(http.StatusUnauthorized)
			if s.bug != bugAnswersAndPasses {
				return
			}
		}
		ctx := r.Context()
		if s.bug == bugDrainsBody {
			_, _ = io.Copy(io.Discard, r.Body)
		}
		if s.bug != bugNoBinding {
			if !s.attributesOnly || s.bug == bugBindsUnaskedSelector {
				ctx = middleware.BindSelector(ctx, "flagSetId=a")
			}
			tier := any("1")
			if s.bug == bugBindsWrongType {
				tier = 1
			}
			ctx = middleware.WithAttributes(ctx, map[string]any{"credential": "api-key", "tier": tier})
		}
		switch s.bug {
		case bugHidesFlusher:
			next.ServeHTTP(opaque{w}, r.WithContext(ctx))
		case bugChangesStatus:
			next.ServeHTTP(writer{ResponseWriter: w, force: http.StatusOK}, r.WithContext(ctx))
		case bugDropsTrailer:
			next.ServeHTTP(writer{ResponseWriter: w, dropTrailers: true}, r.WithContext(ctx))
		default:
			next.ServeHTTP(w, r.WithContext(ctx))
		}
	})
}

func request(key string) func() *http.Request {
	return func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/ofrep/v1/evaluate/flags", strings.NewReader("{}"))
		if key != "" {
			r.Header.Set(keyHeader, key)
		}
		return r
	}
}

func inputs() middlewaretest.Inputs {
	return middlewaretest.Inputs{
		Config:        map[string]any{"key": goodKey},
		Passed:        request(goodKey),
		Answered:      request(""),
		Binds:         &middlewaretest.Binding{Selector: "flagSetId=a", Attributes: map[string]any{"credential": "api-key", "tier": "1"}},
		InvalidConfig: map[string]any{"key": 42},
		FaultyValue:   "42",
		Timeout:       2 * time.Second,
	}
}

func TestContractOnPassingStub(t *testing.T) {
	middlewaretest.Run(t, stubFactory{typ: "stub"}, inputs())
}

// passThrough passes every request on and binds nothing.
type passThrough struct{}

func (passThrough) Type() string { return "pass-through" }
func (passThrough) Create(middleware.Settings) (middleware.Middleware, error) {
	return passThrough{}, nil
}
func (passThrough) Handler(next http.Handler) http.Handler { return next }

func TestContractOnMiddlewareThatNeverAnswers(t *testing.T) {
	middlewaretest.Run(t, passThrough{}, middlewaretest.Inputs{
		Passed:       request(""),
		NeverAnswers: true,
		NoConfig:     true,
	})
}

func TestBrokenStubsFailExactlyTheirCases(t *testing.T) {
	tests := []struct {
		bug  bug
		want []string
	}{
		{bugHidesFlusher, []string{middlewaretest.CaseFlushes}},
		{bugChangesStatus, []string{middlewaretest.CasePassesUnchanged, middlewaretest.CaseNeverBoth, middlewaretest.CaseConcurrent}},
		{bugAnswersAndPasses, []string{middlewaretest.CaseAnswered, middlewaretest.CaseNeverBoth}},
		{bugNoBinding, []string{middlewaretest.CaseBindings}},
		{bugLeaksSpan, []string{middlewaretest.CaseTelemetry}},
		{bugNotConcurrencySafe, []string{middlewaretest.CaseConcurrent}},
		{bugAcceptsInvalid, []string{middlewaretest.CaseInvalidConfig}},
		{bugVagueError, []string{middlewaretest.CaseInvalidConfig}},
		{bugDrainsBody, []string{middlewaretest.CasePassesUnchanged}},
		{bugDropsTrailer, []string{middlewaretest.CasePassesUnchanged, middlewaretest.CaseNeverBoth, middlewaretest.CaseConcurrent}},
		{bugBindsWrongType, []string{middlewaretest.CaseBindings}},
	}
	for _, tt := range tests {
		report := middlewaretest.Check(stubFactory{typ: "stub", bug: tt.bug}, inputs())
		if report.Aborted != "" {
			t.Errorf("bug %d: aborted: %s", tt.bug, report.Aborted)
			continue
		}
		if got := report.FailedCases(); !slices.Equal(got, tt.want) {
			t.Errorf("bug %d: failed cases %q, want %q", tt.bug, got, tt.want)
		}
		for _, c := range report.Cases {
			if !c.Passed && (c.Expected == "" || c.Observed == "") {
				t.Errorf("bug %d: case %q fails without expected and observed behaviour", tt.bug, c.Name)
			}
		}
	}
}

func TestAbortedRuns(t *testing.T) {
	noInputs := middlewaretest.Check(stubFactory{typ: "stub"}, middlewaretest.Inputs{Config: map[string]any{"key": goodKey}})
	for _, name := range []string{"Inputs.Passed", "Inputs.Answered", "Inputs.InvalidConfig", "Inputs.FaultyValue",
		middlewaretest.CasePassesUnchanged, middlewaretest.CaseAnswered, middlewaretest.CaseInvalidConfig} {
		if !strings.Contains(noInputs.Aborted, name) {
			t.Errorf("missing inputs: %q does not name %q", noInputs.Aborted, name)
		}
	}
	if len(noInputs.Cases) != 0 || !noInputs.Failed() {
		t.Error("a run with missing inputs exercised cases or passed")
	}
	if r := middlewaretest.Check(stubFactory{typ: " "}, inputs()); !strings.Contains(r.Aborted, "type name") {
		t.Errorf("empty type: aborted %q", r.Aborted)
	}
	if r := middlewaretest.Check(stubFactory{typ: "stub", bug: bugNilMiddleware}, inputs()); !strings.Contains(r.Aborted, "cannot create") {
		t.Errorf("nil middleware: aborted %q", r.Aborted)
	}
	in := inputs()
	in.Config = map[string]any{"key": 7}
	if r := middlewaretest.Check(stubFactory{typ: "stub"}, in); !strings.Contains(r.Aborted, "7 is not a string") {
		t.Errorf("factory failure: aborted %q", r.Aborted)
	}
}

// attributesInputs are the inputs of a middleware that binds attributes and no selector.
func attributesInputs() middlewaretest.Inputs {
	in := inputs()
	in.Binds = &middlewaretest.Binding{Attributes: map[string]any{"credential": "api-key", "tier": "1"}}
	return in
}

func TestContractOnMiddlewareThatBindsNoSelector(t *testing.T) {
	middlewaretest.Run(t, stubFactory{typ: "stub", attributesOnly: true}, attributesInputs())
}

func TestBindingASelectorThatWasNotExpectedFailsOnlyTheBindings(t *testing.T) {
	report := middlewaretest.Check(stubFactory{typ: "stub", attributesOnly: true, bug: bugBindsUnaskedSelector}, attributesInputs())

	if report.Aborted != "" {
		t.Fatalf("aborted: %s", report.Aborted)
	}
	if got, want := report.FailedCases(), []string{middlewaretest.CaseBindings}; !slices.Equal(got, want) {
		t.Errorf("failed cases %q, want %q", got, want)
	}
}
