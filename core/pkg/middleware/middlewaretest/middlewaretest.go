// Package middlewaretest holds the contract tests of the middleware component type (UC-008). An Extension Author
// hands the runner the middleware's factory and the test inputs:
//
//	func TestContract(t *testing.T) {
//		middlewaretest.Run(t, myFactory{}, middlewaretest.Inputs{Passed: ..., Answered: ..., Binds: ...})
//	}
//
// The runner creates the middleware only through its factory, wraps a recording handler with it and sends real
// HTTP requests through a test server. Each contract case is a subtest. A run that cannot exercise the contract (a
// missing test input, an invalid declaration, a factory that fails on the valid configuration) fails as a whole
// and names why; no case can be skipped.
package middlewaretest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
)

// DefaultTimeout is the time limit of a case that has none of its own.
const DefaultTimeout = 5 * time.Second

// Goroutines is the number of goroutines of the concurrency case.
const Goroutines = 64

// Names of the contract cases of a middleware (UC-008 BR-002).
const (
	CasePassesUnchanged = "passes a request on as it was sent and returns the wrapped handler's response unchanged"
	CaseFlushes         = "keeps http.Flusher and http.ResponseController working for the wrapped handler"
	CaseAnswered        = "answers an answered request without the wrapped handler"
	CaseNeverBoth       = "passes a request on or answers it, never both"
	CaseBindings        = "makes its selector and request attributes visible to the wrapped handler"
	CaseTelemetry       = "ends every span it starts through the injected telemetry provider"
	CaseConcurrent      = "works under concurrent requests"
	CaseInvalidConfig   = "rejects an invalid configuration naming the faulty value"
)

// Binding is what the middleware binds to a request it passes on.
type Binding struct {
	// Selector is the bound selector expression, in the syntax of the Flagd-Selector header; empty means none.
	Selector string
	// Attributes are the request attributes; nil means none.
	Attributes map[string]any
}

// Inputs are the test inputs a middleware's contract needs.
type Inputs struct {
	// Config is a valid configuration.
	Config map[string]any
	// Passed returns a request the middleware passes on to the wrapped handler.
	Passed func() *http.Request
	// Answered returns a request the middleware answers itself, such as one without a credential.
	Answered func() *http.Request
	// NeverAnswers declares that the middleware passes every request on; Answered is then not needed.
	NeverAnswers bool
	// Binds is what the wrapped handler sees for the Passed request; nil means the middleware binds nothing.
	Binds *Binding
	// InvalidConfig is a configuration the factory must reject with an error that contains FaultyValue.
	InvalidConfig map[string]any
	FaultyValue   string
	// NoConfig declares that the middleware takes no configuration; InvalidConfig and FaultyValue are then not
	// needed.
	NoConfig bool
	// Timeout is the time limit of a case; zero means DefaultTimeout.
	Timeout time.Duration
}

// Case is the outcome of one contract case.
type Case struct {
	Name     string
	Passed   bool
	Expected string
	Observed string
}

// Report is the outcome of a run.
type Report struct {
	// Aborted says why no contract case was exercised.
	Aborted string
	Cases   []Case
}

// Failed reports whether the run is a failure: it was aborted or a case failed.
func (r Report) Failed() bool {
	return r.Aborted != "" || len(r.FailedCases()) > 0
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

// Run runs the contract tests of a middleware: one subtest per contract case.
func Run(t *testing.T, f middleware.Factory, in Inputs) {
	t.Helper()
	Check(f, in).report(t)
}

// Check runs the contract tests of a middleware and returns their outcome.
func Check(f middleware.Factory, in Inputs) Report {
	if msg := missing(in); msg != "" {
		return Report{Aborted: msg}
	}
	if err := middleware.ValidateDeclaration(f); err != nil {
		return Report{Aborted: fmt.Sprintf("invalid declaration: %v", err)}
	}
	r := runner{f: f, in: in, limit: in.Timeout}
	if r.limit <= 0 {
		r.limit = DefaultTimeout
	}
	if mw, err := r.create(in.Config, telemetry.Noop()); err != nil || mw == nil {
		return Report{Aborted: fmt.Sprintf("the factory cannot create the middleware from the valid configuration (middleware %v, error %v)", mw, err)}
	}

	cases := []struct {
		name string
		run  func() (expected, observed string)
	}{
		{CasePassesUnchanged, r.passesUnchanged},
		{CaseFlushes, r.flushes},
		{CaseAnswered, r.answered},
		{CaseNeverBoth, r.neverBoth},
		{CaseBindings, r.bindings},
		{CaseTelemetry, r.telemetry},
		{CaseConcurrent, r.concurrent},
		{CaseInvalidConfig, r.invalidConfig},
	}
	report := Report{}
	for _, c := range cases {
		expected, observed := guarded(c.run)
		report.Cases = append(report.Cases, Case{Name: c.name, Passed: observed == "", Expected: expected, Observed: observed})
	}
	return report
}

func missing(in Inputs) string {
	var parts []string
	need := func(name string, set bool, cases ...string) {
		if !set {
			quoted := make([]string, len(cases))
			for i, c := range cases {
				quoted[i] = fmt.Sprintf("%q", c)
			}
			parts = append(parts, fmt.Sprintf("test input %s is missing, needed by the cases %s", name, strings.Join(quoted, ", ")))
		}
	}
	need("Inputs.Passed", in.Passed != nil, CasePassesUnchanged, CaseFlushes, CaseNeverBoth, CaseBindings, CaseTelemetry, CaseConcurrent)
	need("Inputs.Answered", in.NeverAnswers || in.Answered != nil, CaseAnswered, CaseNeverBoth)
	need("Inputs.InvalidConfig", in.NoConfig || in.InvalidConfig != nil, CaseInvalidConfig)
	need("Inputs.FaultyValue", in.NoConfig || in.FaultyValue != "", CaseInvalidConfig)
	return strings.Join(parts, "; ")
}

// guarded runs a case and turns a panic into its observed behaviour. A case reports "" as observed when it passed.
func guarded(run func() (string, string)) (expected, observed string) {
	defer func() {
		if v := recover(); v != nil {
			observed = fmt.Sprintf("panic: %v", v)
		}
	}()
	return run()
}

type runner struct {
	f     middleware.Factory
	in    Inputs
	limit time.Duration
}

func (r runner) create(config map[string]any, p telemetry.Provider) (middleware.Middleware, error) {
	return r.f.Create(middleware.Settings{Logger: logger.NewLogger(nil, false), Telemetry: p, Config: config})
}

// What the wrapped handler answers. The body is written in two parts with a flush in between, and the trailer is
// set after the body.
const (
	innerStatus  = http.StatusMultiStatus
	innerHeader  = "X-Middlewaretest"
	innerTrailer = "X-Middlewaretest-Trailer"
	innerBody1   = "first part;"
	innerBody2   = "second part"
)

// seen is what the wrapped handler observed of one request.
type seen struct {
	// request is what the wrapped handler received of the request, its body read in full
	request    sentRequest
	bound      bool
	selector   store.Selector
	boundErr   error
	attributes map[string]any
	flusher    bool
	controller error
}

// recordingHandler is the handler the middleware wraps.
type recordingHandler struct {
	calls atomic.Int64
	mu    sync.Mutex
	last  seen
}

func (h *recordingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.calls.Add(1)
	s := seen{attributes: evaluator.AttributesFromContext(r.Context())}
	body, _ := io.ReadAll(r.Body)
	s.request = sentRequest{method: r.Method, uri: r.RequestURI, contentType: r.Header.Get("Content-Type"), body: string(body)}
	s.selector, s.bound, s.boundErr = middleware.BoundSelector(r.Context())

	w.Header().Set("Trailer", innerTrailer)
	w.Header().Set(innerHeader, "inner")
	w.WriteHeader(innerStatus)
	_, _ = io.WriteString(w, innerBody1)
	if f, ok := w.(http.Flusher); ok {
		s.flusher = true
		f.Flush()
	}
	s.controller = http.NewResponseController(w).Flush()
	_, _ = io.WriteString(w, innerBody2)
	w.Header().Set(innerTrailer, "end")

	h.mu.Lock()
	h.last = s
	h.mu.Unlock()
}

func (h *recordingHandler) lastSeen() seen {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.last
}

// response is what a client received.
type response struct {
	status  int
	header  http.Header
	trailer http.Header
	body    string
}

// server serves the middleware around a recording handler.
type server struct {
	srv   *httptest.Server
	inner *recordingHandler
}

func (r runner) serve(p telemetry.Provider) (*server, string) {
	mw, err := r.create(r.in.Config, p)
	if err != nil || mw == nil {
		return nil, fmt.Sprintf("the factory failed on the valid configuration (middleware %v, error %v)", mw, err)
	}
	inner := &recordingHandler{}
	return &server{srv: httptest.NewServer(middleware.Chain([]middleware.Middleware{mw}, inner)), inner: inner}, ""
}

func (s *server) close() { s.srv.Close() }

func (s *server) send(limit time.Duration, req *http.Request) (response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	out := req.Clone(ctx)
	out.RequestURI = ""
	out.URL.Scheme = "http"
	out.URL.Host = strings.TrimPrefix(s.srv.URL, "http://")
	out.Host = ""
	resp, err := s.srv.Client().Do(out)
	if err != nil {
		return response{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return response{}, err
	}
	return response{status: resp.StatusCode, header: resp.Header, trailer: resp.Trailer, body: string(body)}, nil
}

// unchanged describes how resp differs from the wrapped handler's response, "" if it does not.
func unchanged(resp response) string {
	switch {
	case resp.status != innerStatus:
		return fmt.Sprintf("the status is %d, the wrapped handler answered %d", resp.status, innerStatus)
	case resp.header.Get(innerHeader) != "inner":
		return fmt.Sprintf("the header %s is %q, the wrapped handler set %q", innerHeader, resp.header.Get(innerHeader), "inner")
	case resp.body != innerBody1+innerBody2:
		return fmt.Sprintf("the body is %q, the wrapped handler wrote %q", resp.body, innerBody1+innerBody2)
	case resp.trailer.Get(innerTrailer) != "end":
		return fmt.Sprintf("the trailer %s is %q, the wrapped handler set %q", innerTrailer, resp.trailer.Get(innerTrailer), "end")
	}
	return ""
}

func (r runner) passesUnchanged() (string, string) {
	const expected = "the wrapped handler is called once with the request as it was sent, and its status, headers, body and trailers reach the client unchanged"
	s, failure := r.serve(telemetry.Noop())
	if failure != "" {
		return expected, failure
	}
	defer s.close()
	req, sent := prepare(r.in.Passed())
	resp, err := s.send(r.limit, req)
	if err != nil {
		return expected, fmt.Sprintf("the request failed: %v", err)
	}
	if n := s.inner.calls.Load(); n != 1 {
		return expected, fmt.Sprintf("the wrapped handler was called %d times", n)
	}
	if diff := sent.differsFrom(s.inner.lastSeen().request); diff != "" {
		return expected, "the request was changed on its way: " + diff
	}
	return expected, unchanged(resp)
}

func (r runner) flushes() (string, string) {
	const expected = "the wrapped handler's response writer is an http.Flusher and http.ResponseController can flush it"
	s, failure := r.serve(telemetry.Noop())
	if failure != "" {
		return expected, failure
	}
	defer s.close()
	if _, err := s.send(r.limit, r.in.Passed()); err != nil {
		return expected, fmt.Sprintf("the request failed: %v", err)
	}
	got := s.inner.lastSeen()
	switch {
	case s.inner.calls.Load() == 0:
		return expected, "the wrapped handler was not called"
	case !got.flusher:
		return expected, "the response writer is not an http.Flusher"
	case got.controller != nil:
		return expected, fmt.Sprintf("http.ResponseController could not flush: %v", got.controller)
	}
	return expected, ""
}

func (r runner) answered() (string, string) {
	if r.in.NeverAnswers {
		const expected = "a middleware that passes every request on calls the wrapped handler for the passed request"
		s, failure := r.serve(telemetry.Noop())
		if failure != "" {
			return expected, failure
		}
		defer s.close()
		if _, err := s.send(r.limit, r.in.Passed()); err != nil {
			return expected, fmt.Sprintf("the request failed: %v", err)
		}
		if s.inner.calls.Load() != 1 {
			return expected, fmt.Sprintf("the wrapped handler was called %d times", s.inner.calls.Load())
		}
		return expected, ""
	}
	const expected = "the client receives the middleware's answer and the wrapped handler is never called"
	s, failure := r.serve(telemetry.Noop())
	if failure != "" {
		return expected, failure
	}
	defer s.close()
	resp, err := s.send(r.limit, r.in.Answered())
	if err != nil {
		return expected, fmt.Sprintf("the request failed: %v", err)
	}
	if n := s.inner.calls.Load(); n != 0 {
		return expected, fmt.Sprintf("the wrapped handler was called %d times (answered %d)", n, resp.status)
	}
	return expected, ""
}

func (r runner) neverBoth() (string, string) {
	const expected = "each response comes from exactly one of them: the wrapped handler's, untouched, or the middleware's alone"
	s, failure := r.serve(telemetry.Noop())
	if failure != "" {
		return expected, failure
	}
	defer s.close()
	requests := []*http.Request{r.in.Passed()}
	if !r.in.NeverAnswers {
		requests = append(requests, r.in.Answered())
	}
	for i, req := range requests {
		before := s.inner.calls.Load()
		resp, err := s.send(r.limit, req)
		if err != nil {
			return expected, fmt.Sprintf("request %d failed: %v", i+1, err)
		}
		called := s.inner.calls.Load() - before
		switch {
		case called > 1:
			return expected, fmt.Sprintf("request %d reached the wrapped handler %d times", i+1, called)
		case called == 1:
			if diff := unchanged(resp); diff != "" {
				return expected, fmt.Sprintf("request %d was passed on and answered: %s", i+1, diff)
			}
		case resp.header.Get(innerHeader) != "":
			return expected, fmt.Sprintf("request %d was answered with the wrapped handler's header without calling it", i+1)
		}
	}
	return expected, ""
}

func (r runner) bindings() (string, string) {
	want := Binding{}
	if r.in.Binds != nil {
		want = *r.in.Binds
	}
	expected := fmt.Sprintf("the wrapped handler sees the bound selector %q and the request attributes %v", want.Selector, want.Attributes)
	if want.Selector == "" && want.Attributes == nil {
		expected = "the wrapped handler sees no bound selector and no request attributes"
	}
	s, failure := r.serve(telemetry.Noop())
	if failure != "" {
		return expected, failure
	}
	defer s.close()
	if _, err := s.send(r.limit, r.in.Passed()); err != nil {
		return expected, fmt.Sprintf("the request failed: %v", err)
	}
	if s.inner.calls.Load() == 0 {
		return expected, "the wrapped handler was not called"
	}
	got := s.inner.lastSeen()
	if got.boundErr != nil {
		return expected, fmt.Sprintf("the bound selector is invalid: %v", got.boundErr)
	}
	switch {
	case want.Selector == "" && got.bound:
		return expected, fmt.Sprintf("a selector is bound: flag set %q, source %q", got.selector.FlagSetId(), got.selector.Source())
	case want.Selector != "":
		w, err := store.NewSelector(want.Selector)
		if err != nil {
			return expected, fmt.Sprintf("the test input Binds.Selector is invalid: %v", err)
		}
		if !got.bound || got.selector.FlagSetId() != w.FlagSetId() || got.selector.Source() != w.Source() {
			return expected, fmt.Sprintf("bound %v: flag set %q, source %q", got.bound, got.selector.FlagSetId(), got.selector.Source())
		}
	}
	if !equalAttributes(got.attributes, want.Attributes) {
		return expected, fmt.Sprintf("the request attributes are %v", got.attributes)
	}
	return expected, ""
}

// equalAttributes compares attributes by type and value: evaluators read the typed value.
func equalAttributes(got, want map[string]any) bool {
	if len(got) == 0 && len(want) == 0 {
		return true
	}
	return reflect.DeepEqual(got, want)
}

func (r runner) telemetry() (string, string) {
	const expected = "the middleware is handed the distribution's telemetry provider, may record through it, and ends every span it starts"
	rec := &recordingProvider{}
	s, failure := r.serve(rec)
	if failure != "" {
		return expected, failure
	}
	requests := []*http.Request{r.in.Passed()}
	if !r.in.NeverAnswers {
		requests = append(requests, r.in.Answered())
	}
	for i, req := range requests {
		if _, err := s.send(r.limit, req); err != nil {
			s.close()
			return expected, fmt.Sprintf("request %d failed: %v", i+1, err)
		}
	}
	s.close()
	if started, ended := rec.started.Load(), rec.ended.Load(); started != ended {
		return expected, fmt.Sprintf("%d spans started, %d ended", started, ended)
	}
	return expected, ""
}

func (r runner) concurrent() (string, string) {
	expected := fmt.Sprintf("%d goroutines send passed and answered requests at once; every passed request gets the wrapped handler's response", Goroutines)
	s, failure := r.serve(telemetry.Noop())
	if failure != "" {
		return expected, failure
	}
	defer s.close()
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		problems []string
	)
	for g := range Goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 10 {
				passed := r.in.NeverAnswers || (g+i)%2 == 0
				req := r.in.Passed
				if !passed {
					req = r.in.Answered
				}
				resp, err := s.send(r.limit, req())
				var problem string
				switch {
				case err != nil:
					problem = fmt.Sprintf("a request failed: %v", err)
				case passed:
					problem = unchanged(resp)
				case resp.header.Get(innerHeader) != "":
					problem = "an answered request reached the wrapped handler"
				}
				if problem != "" {
					mu.Lock()
					problems = append(problems, problem)
					mu.Unlock()
					return
				}
			}
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(r.limit):
		return expected, fmt.Sprintf("the requests did not finish within %v", r.limit)
	}
	if len(problems) > 0 {
		return expected, problems[0]
	}
	return expected, ""
}

func (r runner) invalidConfig() (string, string) {
	if r.in.NoConfig {
		const expected = "a middleware that takes no configuration is created from any configuration"
		mw, err := r.create(map[string]any{"middlewaretest": "unknown"}, telemetry.Noop())
		if err != nil || mw == nil {
			return expected, fmt.Sprintf("Create returned middleware %v and error %v", mw, err)
		}
		return expected, ""
	}
	expected := fmt.Sprintf("Create rejects the invalid configuration with an error that names %q", r.in.FaultyValue)
	_, err := r.create(r.in.InvalidConfig, telemetry.Noop())
	if err == nil {
		return expected, "Create accepted it"
	}
	if !strings.Contains(err.Error(), r.in.FaultyValue) {
		return expected, fmt.Sprintf("the error %q does not name it", err)
	}
	return expected, ""
}

// recordingProvider counts the spans a middleware starts and ends; everything else is the noop provider.
type recordingProvider struct {
	started, ended atomic.Int64
}

func (p *recordingProvider) Tracer(string) telemetry.Tracer { return recordingTracer{p} }

func (p *recordingProvider) Meter(scope string) telemetry.Meter { return telemetry.Noop().Meter(scope) }

func (p *recordingProvider) HTTPServerSpans(h http.Handler, _ string) http.Handler { return h }

func (p *recordingProvider) HTTPServerMetrics(h http.Handler, _ telemetry.HTTPMetricsOptions) http.Handler {
	return h
}

func (p *recordingProvider) Shutdown(context.Context) error { return nil }

type recordingTracer struct{ p *recordingProvider }

func (t recordingTracer) Start(ctx context.Context, _ string, _ ...telemetry.SpanOption) (context.Context, telemetry.Span) {
	t.p.started.Add(1)
	return ctx, &recordingSpan{p: t.p}
}

type recordingSpan struct {
	p     *recordingProvider
	ended atomic.Bool
}

func (*recordingSpan) SetAttributes(...telemetry.Attribute) {}
func (*recordingSpan) RecordError(error)                    {}
func (*recordingSpan) SetError(string)                      {}

func (s *recordingSpan) End() {
	if s.ended.CompareAndSwap(false, true) {
		s.p.ended.Add(1)
	}
}

// sentRequest is what a request carries that a middleware that passes it on must not change: its method, target, media
// type and body. A middleware may add to the request and may remove its credential, so other headers are not compared.
type sentRequest struct {
	method, uri, contentType, body string
}

// prepare reads the body of a request, to know what was sent, and gives the request a body to send.
func prepare(req *http.Request) (*http.Request, sentRequest) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
	}
	uri := req.RequestURI
	if uri == "" {
		uri = req.URL.RequestURI()
	}
	return req, sentRequest{method: req.Method, uri: uri, contentType: req.Header.Get("Content-Type"), body: string(body)}
}

// differsFrom says how the request the wrapped handler received differs from the one that was sent, "" if it does not.
func (want sentRequest) differsFrom(got sentRequest) string {
	switch {
	case got.method != want.method:
		return fmt.Sprintf("the method is %q, the request was sent with %q", got.method, want.method)
	case got.uri != want.uri:
		return fmt.Sprintf("the target is %q, the request was sent to %q", got.uri, want.uri)
	case got.contentType != want.contentType:
		return fmt.Sprintf("the Content-Type is %q, the request was sent with %q", got.contentType, want.contentType)
	case got.body != want.body:
		return fmt.Sprintf("the body is %q, the request was sent with %q", got.body, want.body)
	}
	return ""
}
