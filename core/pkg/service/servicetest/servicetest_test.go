package servicetest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/contracttest"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/stretchr/testify/require"
)

// features are what a stub endpoint does besides holding connections open.
type features struct {
	resolve, documents, validator, listen, collect bool
}

// stub is a small HTTP endpoint with the features of the real ones. broken switches one of its behaviours
// to a violation of the contract.
type stub struct {
	typeName string
	needs    service.Needs
	features features
	broken   string
}

func (s stub) Type() string         { return s.typeName }
func (s stub) Needs() service.Needs { return s.needs }

func (s stub) Create(settings service.Settings) (service.Endpoint, error) {
	return &stubEndpoint{stub: s, settings: settings}, nil
}

type stubEndpoint struct {
	stub
	settings service.Settings
}

func (e *stubEndpoint) Serve(ctx context.Context) error {
	port, _ := e.settings.Config["port"].(int)
	listener, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		if e.broken == "swallows serve error" {
			return nil
		}
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/hold", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	if e.features.resolve {
		mux.HandleFunc("/flags/{key}", e.resolve)
	}
	if e.features.documents {
		mux.HandleFunc("/documents", e.documents)
	}
	if e.features.validator {
		mux.HandleFunc("/validator", e.validator)
	}
	if e.features.listen {
		mux.HandleFunc("/watch", e.watch)
	}
	if e.features.collect {
		mux.HandleFunc("/events", e.collect)
	}

	server := &http.Server{Handler: e.settings.WrapHTTP(mux), ReadHeaderTimeout: time.Second}
	if e.broken != "never stops" {
		go func() {
			<-ctx.Done()
			_ = server.Close()
		}()
	}
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (e *stubEndpoint) resolve(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	key := r.PathValue("key")
	clientSelector := r.URL.Query().Get("selector")

	var selector store.Selector
	var err error
	if e.broken == "ignores the bound selector" {
		selector, err = store.NewSelector(clientSelector)
	} else {
		selector, err = middleware.EffectiveSelector(ctx, clientSelector)
		if err != nil && !errors.Is(err, middleware.ErrNoFlags) && e.broken == "fails open on an invalid bound selector" {
			selector, err = store.NewSelector(clientSelector)
		}
	}
	if err != nil {
		code := "INVALID"
		if err == middleware.ErrNoFlags {
			code = "FLAG_NOT_FOUND"
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"errorCode": code})
		return
	}
	ctx = context.WithValue(ctx, store.SelectorContextKey{}, selector)

	switch {
	case e.broken == "answers by itself":
		_ = json.NewEncoder(w).Encode(map[string]string{})
	case e.needs.Router:
		_, _, _, _, err := e.settings.Resolver.ResolveBooleanValue(ctx, "", key, nil)
		answer := map[string]string{}
		if err != nil {
			answer["errorCode"] = err.Error()
		}
		_ = json.NewEncoder(w).Encode(answer)
	default:
		// a vendor endpoint that uses the evaluator of its format directly
		results, _, _ := e.settings.Evaluator.Evaluate(ctx, evaluatorRequest(key, selector), nil)
		answer := map[string]string{}
		for _, result := range results {
			answer["errorCode"] = result.ErrorCode
			if e.broken != "publishes no evaluation event" {
				events.PublishEvaluation(e.settings.Bus, events.Evaluation{
					FlagKey: result.FlagKey, ErrorCode: result.ErrorCode, Origin: events.OriginService,
				})
			}
		}
		_ = json.NewEncoder(w).Encode(answer)
	}
}

func (e *stubEndpoint) documents(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	docs := e.settings.Store.Documents(format, Source)
	if e.broken == "serves every format" {
		docs = append(docs, e.settings.Store.Documents("flagd", Source)...)
		docs = append(docs, e.settings.Store.Documents(VendorFormat, Source)...)
	}
	_ = json.NewEncoder(w).Encode(docs)
}

func (e *stubEndpoint) validator(w http.ResponseWriter, _ *http.Request) {
	if e.broken == "constant validator" {
		_ = json.NewEncoder(w).Encode("same")
		return
	}
	var versions string
	for _, d := range e.settings.Store.Documents("flagd", Source) {
		versions += d.FlagSet + "@" + d.Version + ";"
	}
	_ = json.NewEncoder(w).Encode(versions)
}

func (e *stubEndpoint) watch(w http.ResponseWriter, r *http.Request) {
	selector, err := store.NewSelector(r.URL.Query().Get("selector"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sub := e.settings.Bus.Subscribe(events.SubscribeOptions{Kinds: []events.Kind{events.KindConfigurationChange}, Buffer: 8})
	defer sub.Close()
	w.WriteHeader(http.StatusOK)
	flusher := w.(http.Flusher)
	flusher.Flush()
	for {
		select {
		case ev, ok := <-sub.C():
			if !ok {
				return
			}
			change := ev.(events.ConfigurationChange)
			seesMissed := change.Missed > 0 && e.broken != "ignores missed events"
			if e.broken != "announces every change" && !seesMissed && change.FlagSet != selector.FlagSetId() {
				continue
			}
			_, _ = fmt.Fprintln(w, "change")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (e *stubEndpoint) collect(w http.ResponseWriter, r *http.Request) {
	var batch struct {
		Evaluations []events.Evaluation
		Trackings   []events.Tracking
	}
	if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	origin := events.OriginInProcess
	if e.broken == "keeps the origin" {
		origin = events.OriginService
	}
	for _, ev := range batch.Evaluations {
		ev.Origin = origin
		events.PublishEvaluation(e.settings.Bus, ev)
	}
	for _, ev := range batch.Trackings {
		ev.Origin = origin
		events.PublishTracking(e.settings.Bus, ev)
	}
}

// inputsFor connects the harness to a stub with the given features.
func inputsFor(f features, withHTTP bool) Inputs {
	url := func(addr, path string, query map[string]string) string {
		u := "http://" + addr + path
		sep := "?"
		for k, v := range query {
			u += sep + k + "=" + v
			sep = "&"
		}
		return u
	}
	get := func(ctx context.Context, rawURL string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		return http.DefaultClient.Do(req)
	}

	in := Inputs{
		PortKey: "port",
		HTTP:    withHTTP,
		Connect: func(_ context.Context, addr string) (<-chan struct{}, error) {
			resp, err := http.Get(url(addr, "/hold", nil)) //nolint:gosec,noctx // local stub
			if err != nil {
				return nil, err
			}
			ended := make(chan struct{})
			go func() {
				buf := make([]byte, 16)
				for {
					if _, err := resp.Body.Read(buf); err != nil {
						close(ended)
						return
					}
				}
			}()
			return ended, nil
		},
	}
	if f.resolve {
		in.Resolve = func(ctx context.Context, addr, key, selector string) (Answer, error) {
			resp, err := get(ctx, url(addr, "/flags/"+key, map[string]string{"selector": selector}))
			if err != nil {
				return Answer{}, err
			}
			defer resp.Body.Close()
			var body map[string]string
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				return Answer{}, err
			}
			return Answer{ErrorCode: body["errorCode"]}, nil
		}
	}
	if f.documents {
		in.Documents = func(ctx context.Context, addr, format, _ string) ([]store.Document, error) {
			resp, err := get(ctx, url(addr, "/documents", map[string]string{"format": format}))
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()
			var docs []store.Document
			return docs, json.NewDecoder(resp.Body).Decode(&docs)
		}
	}
	if f.validator {
		in.Validator = func(ctx context.Context, addr, _ string) (string, error) {
			resp, err := get(ctx, url(addr, "/validator", nil))
			if err != nil {
				return "", err
			}
			defer resp.Body.Close()
			var validator string
			return validator, json.NewDecoder(resp.Body).Decode(&validator)
		}
	}
	if f.listen {
		in.Listen = func(ctx context.Context, addr, selector string) (<-chan struct{}, error) {
			resp, err := get(ctx, url(addr, "/watch", map[string]string{"selector": selector}))
			if err != nil {
				return nil, err
			}
			announced := make(chan struct{}, 64)
			go func() {
				defer resp.Body.Close()
				buf := make([]byte, 64)
				for {
					n, err := resp.Body.Read(buf)
					for i := 0; i < n; i++ {
						if buf[i] == '\n' {
							select {
							case announced <- struct{}{}:
							default:
							}
						}
					}
					if err != nil {
						return
					}
				}
			}()
			return announced, nil
		}
	}
	if f.collect {
		in.SendEvents = func(ctx context.Context, addr string, batch Batch) error {
			body, err := json.Marshal(map[string]any{"Evaluations": batch.Evaluations, "Trackings": batch.Trackings})
			if err != nil {
				return err
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, url(addr, "/events", nil), bytes.NewReader(body))
			if err != nil {
				return err
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			return resp.Body.Close()
		}
	}
	for _, feature := range allFeatures {
		if !feature.supplied(in) {
			in.Without = append(in.Without, feature)
		}
	}
	return in
}

func failed(report contracttest.Report) []string {
	return report.FailedCases()
}

// names are the cases the run exercised, in order.
func names(report contracttest.Report) []string {
	out := make([]string, 0, len(report.Cases))
	for _, c := range report.Cases {
		if c.NotApplicable == "" {
			out = append(out, c.Name)
		}
	}
	return out
}

// text is what a failed case says of the expected and the observed behaviour.
func text(c contracttest.Case) string {
	return c.Expected + "\n" + c.Observed
}

const (
	nameServes     = "serves and returns within the budget with its connections ended"
	nameServeError = "a serve error is returned"
	nameResolves   = "resolves through the injected resolver with the effective selector"
	nameEvent      = "publishes one evaluation event per resolved flag"
	nameDocuments  = "serves the documents of the requested format, unchanged, with versions"
	nameValidator  = "the validator follows the document version"
	nameReacts     = "reacts to a configuration change event for its selector only"
	nameRelays     = "relays events marked in-process"
	nameSelector   = "honours the bound selector and the client's selector"
)

var (
	resolverFeatures = features{resolve: true}
	serverFeatures   = features{documents: true, validator: true, listen: true}
	collectFeatures  = features{collect: true}
)

// TestStubsMeetTheContract runs the contract on a stub of each kind of endpoint: one that resolves through
// the router, one that needs a vendor's evaluator, one that serves configurations, one that collects events
// and one that subscribes to nothing.
func TestStubsMeetTheContract(t *testing.T) {
	tests := []struct {
		name    string
		factory stub
		inputs  Inputs
		ran     []string
	}{
		{
			name:    "resolver",
			factory: stub{typeName: "resolver", needs: service.Needs{Router: true}, features: resolverFeatures},
			inputs:  inputsFor(resolverFeatures, true),
			ran:     []string{nameServes, nameServeError, nameResolves, nameEvent, nameSelector},
		},
		{
			name:    "vendor evaluator",
			factory: stub{typeName: "vendor", needs: service.Needs{Format: "flagd"}, features: resolverFeatures},
			inputs:  inputsFor(resolverFeatures, false),
			ran:     []string{nameServes, nameServeError, nameResolves, nameEvent},
		},
		{
			name:    "config server",
			factory: stub{typeName: "config-server", features: serverFeatures},
			inputs:  inputsFor(serverFeatures, false),
			ran:     []string{nameServes, nameServeError, nameDocuments, nameValidator, nameReacts},
		},
		{
			name:    "collector",
			factory: stub{typeName: "collector", features: collectFeatures},
			inputs:  inputsFor(collectFeatures, false),
			ran:     []string{nameServes, nameServeError, nameRelays},
		},
		{
			name:    "silent",
			factory: stub{typeName: "silent"},
			inputs:  inputsFor(features{}, false),
			ran:     []string{nameServes, nameServeError},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := Check(t, tt.factory, tt.inputs)
			require.Empty(t, failed(results), "%+v", results)
			ran := names(results)
			require.Subset(t, ran, tt.ran)
			require.Len(t, ran, len(tt.ran)+2, "the two cases every endpoint gets beside these: declarations and creation")
		})
	}
}

// TestBrokenStubsFailExactlyTheirCases is A7: an endpoint that breaks one rule fails the case of that rule
// and no other, and the failure says what was expected and what was observed.
func TestBrokenStubsFailExactlyTheirCases(t *testing.T) {
	tests := []struct {
		broken   string
		factory  stub
		inputs   Inputs
		wantFail []string
		wantText string
	}{
		{
			broken:   "never stops",
			factory:  stub{typeName: "x", features: features{}, broken: "never stops"},
			inputs:   inputsFor(features{}, false),
			wantFail: []string{nameServes},
			wantText: "expected Serve to return within",
		},
		{
			broken:   "swallows serve error",
			factory:  stub{typeName: "x", broken: "swallows serve error"},
			inputs:   inputsFor(features{}, false),
			wantFail: []string{nameServeError},
			wantText: "expected Serve to return an error when its port is in use\nobserved: it returned nil",
		},
		{
			broken:   "answers by itself",
			factory:  stub{typeName: "x", needs: service.Needs{Router: true}, features: resolverFeatures, broken: "answers by itself"},
			inputs:   inputsFor(resolverFeatures, true),
			wantFail: []string{nameResolves, nameEvent, nameSelector},
			wantText: "through the injected resolver",
		},
		{
			broken:   "ignores the bound selector",
			factory:  stub{typeName: "x", needs: service.Needs{Router: true}, features: resolverFeatures, broken: "ignores the bound selector"},
			inputs:   inputsFor(resolverFeatures, true),
			wantFail: []string{nameSelector},
			wantText: `selector row "bound only"`,
		},
		{
			broken:   "publishes no evaluation event",
			factory:  stub{typeName: "x", needs: service.Needs{Format: "flagd"}, features: resolverFeatures, broken: "publishes no evaluation event"},
			inputs:   inputsFor(resolverFeatures, false),
			wantFail: []string{nameEvent},
			wantText: "expected one evaluation event for each of the two resolved flags",
		},
		{
			broken:   "serves every format",
			factory:  stub{typeName: "x", features: serverFeatures, broken: "serves every format"},
			inputs:   inputsFor(serverFeatures, false),
			wantFail: []string{nameDocuments},
			wantText: "document(s) of format",
		},
		{
			broken:   "constant validator",
			factory:  stub{typeName: "x", features: serverFeatures, broken: "constant validator"},
			inputs:   inputsFor(serverFeatures, false),
			wantFail: []string{nameValidator},
			wantText: "expected the validator to change with the document version",
		},
		{
			broken:   "announces every change",
			factory:  stub{typeName: "x", features: serverFeatures, broken: "announces every change"},
			inputs:   inputsFor(serverFeatures, false),
			wantFail: []string{nameReacts},
			wantText: "expected no announcement for a change that does not concern the client's selector",
		},
		{
			broken:   "ignores missed events",
			factory:  stub{typeName: "x", features: serverFeatures, broken: "ignores missed events"},
			inputs:   inputsFor(serverFeatures, false),
			wantFail: []string{nameReacts},
			wantText: "the endpoint did not reread the store",
		},
		{
			broken:   "fails open on an invalid bound selector",
			factory:  stub{typeName: "x", needs: service.Needs{Router: true}, features: resolverFeatures, broken: "fails open on an invalid bound selector"},
			inputs:   inputsFor(resolverFeatures, true),
			wantFail: []string{nameSelector},
			wantText: `selector row "invalid bound selector"`,
		},
		{
			broken:   "keeps the origin",
			factory:  stub{typeName: "x", features: collectFeatures, broken: "keeps the origin"},
			inputs:   inputsFor(collectFeatures, false),
			wantFail: []string{nameRelays},
			wantText: `expected the relayed evaluation event to carry origin "in-process"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.broken, func(t *testing.T) {
			results := Check(t, tt.factory, tt.inputs)
			require.Equal(t, tt.wantFail, failed(results))
			for _, r := range results.Cases {
				if !r.Passed && r.NotApplicable == "" {
					require.Contains(t, text(r), tt.wantText)
					require.NotEmpty(t, r.Observed, "a failure states the observed behaviour")
					require.NotEmpty(t, r.Expected, "a failure states the expected behaviour")
					break
				}
			}
		})
	}
}

// TestMissingInputFailsNamingIt pins that an applicable case cannot be skipped.
func TestMissingInputFailsNamingIt(t *testing.T) {
	in := inputsFor(resolverFeatures, false)
	in.Resolve = nil
	results := Check(t, stub{typeName: "x", needs: service.Needs{Router: true}, features: resolverFeatures}, in)

	require.Equal(t, []string{nameResolves, nameEvent}, failed(results))
	for _, r := range results.Cases {
		if !r.Passed && r.NotApplicable == "" {
			require.Contains(t, text(r), "the input Resolve is required")
		}
	}

	in = inputsFor(features{}, false)
	in.Connect = nil
	results = Check(t, stub{typeName: "x"}, in)
	require.Equal(t, []string{nameServes}, failed(results))
	require.Contains(t, text(results.Cases[2]), "the input Connect is required")

	in = inputsFor(features{}, false)
	in.PortKey = ""
	results = Check(t, stub{typeName: "x"}, in)
	require.Contains(t, failed(results), nameServeError)
}

func TestInvalidDeclarationsFailTheDeclarationsCase(t *testing.T) {
	results := Check(t, stub{typeName: ""}, inputsFor(features{}, false))
	require.Equal(t, "declarations", results.Cases[0].Name)
	require.Contains(t, text(results.Cases[0]), "the type name is empty")
}

func evaluatorRequest(key string, selector store.Selector) evaluator.Request {
	return evaluator.Request{FlagKey: key, Selector: selector}
}

// TestLeavingAnInputOutFails is the fail-closed rule: a feature that is neither supplied nor named in Without
// fails its case, naming the input; a feature both supplied and named, or a name that is no feature, aborts the run.
func TestLeavingAnInputOutFails(t *testing.T) {
	in := inputsFor(serverFeatures, false)
	in.Documents = nil
	in.Without = removeFeature(in.Without, FeatureDocuments)
	results := Check(t, stub{typeName: "x", features: serverFeatures}, in)

	require.Equal(t, []string{nameDocuments}, failed(results))
	for _, r := range results.Cases {
		if !r.Passed && r.NotApplicable == "" {
			require.Contains(t, text(r), "the input Documents is required")
			require.Contains(t, text(r), "name Documents in Without")
		}
	}

	in = inputsFor(serverFeatures, false)
	in.Without = append(in.Without, FeatureValidator)
	results = Check(t, stub{typeName: "x", features: serverFeatures}, in)
	require.Contains(t, results.Aborted, "Validator")
	require.Empty(t, results.Cases)

	in = inputsFor(features{}, false)
	in.Without = append(in.Without, "Colour")
	results = Check(t, stub{typeName: "x"}, in)
	require.Contains(t, results.Aborted, "Colour")
	require.Empty(t, results.Cases)
}

// TestResolverNeedsDeclaresHTTP pins that an endpoint that resolves flags says whether it serves HTTP.
func TestResolverNeedsDeclaresHTTP(t *testing.T) {
	in := inputsFor(resolverFeatures, false)
	in.Without = removeFeature(in.Without, FeatureHTTP)
	results := Check(t, stub{typeName: "x", needs: service.Needs{Router: true}, features: resolverFeatures}, in)
	require.Equal(t, []string{nameSelector}, failed(results))

	in = inputsFor(resolverFeatures, false) // names HTTP in Without
	require.Empty(t, failed(Check(t, stub{typeName: "x", needs: service.Needs{Router: true}, features: resolverFeatures}, in)))
}

func removeFeature(list []Feature, drop Feature) []Feature {
	var out []Feature
	for _, f := range list {
		if f != drop {
			out = append(out, f)
		}
	}
	return out
}
