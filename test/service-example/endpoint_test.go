package serviceexample

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator/typed"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service/servicetest"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/stretchr/testify/require"
)

// answering resolves the key "flag", answers FLAG_NOT_FOUND for any other, and records its requests.
type answering struct {
	mu   sync.Mutex
	seen []evaluator.Request
}

func (a *answering) Evaluate(_ context.Context, req evaluator.Request, dst []evaluator.Result) ([]evaluator.Result, model.Metadata, error) {
	a.mu.Lock()
	a.seen = append(a.seen, req)
	a.mu.Unlock()
	if req.Bulk || req.FlagKey == "flag" {
		return append(dst, evaluator.Result{FlagKey: "flag", Value: true, HasValue: true, Variant: "on", Reason: model.StaticReason}), nil, nil
	}
	return append(dst, evaluator.Result{FlagKey: req.FlagKey, ErrorCode: model.FlagNotFoundErrorCode}), nil, nil
}

func (a *answering) requests() []evaluator.Request {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]evaluator.Request(nil), a.seen...)
}

const (
	setA = "set-a"
	setB = "set-b"
	src  = "example-source"
)

// fixture is an endpoint over a real store and bus, serving on a free port.
type fixture struct {
	store *store.Store
	bus   *events.Bus
	eval  *answering
	base  string
}

func (f *fixture) deliver(t *testing.T, flagSet, version string) {
	t.Helper()
	require.NoError(t, f.store.Apply(store.ApplyRequest{
		Format: "flagd", Source: src,
		Documents:   []store.Document{{Format: "flagd", Source: src, FlagSet: flagSet, Version: version, FlagKeys: []string{"flag"}}},
		Incremental: true,
	}))
	events.PublishConfigurationChange(f.bus, events.ConfigurationChange{
		Source: src, Format: "flagd", FlagSet: flagSet, Version: version, Updated: []string{"flag"},
	})
}

// issuedPorts are the ports freePort has handed out in this process. A port is free again the moment it is closed, so
// the next call can get the same one, and one start would listen twice on it.
var issuedPorts sync.Map

func freePort(t *testing.T) int {
	t.Helper()
	// a port the kernel hands out again stays open until a new one comes, so the next listen cannot get it
	var held []net.Listener
	defer func() {
		for _, h := range held {
			_ = h.Close()
		}
	}()
	for range 1000 {
		l, err := net.Listen("tcp", ":0")
		require.NoError(t, err)
		port := l.Addr().(*net.TCPAddr).Port
		if _, taken := issuedPorts.LoadOrStore(port, true); taken {
			held = append(held, l)
			continue
		}
		_ = l.Close()
		return int(port)
	}
	t.Fatal("no free port that was not handed out before")
	return 0
}

// serve starts the endpoint; bound is the selector the distribution's middleware binds, if any.
func serve(t *testing.T, bound string) *fixture {
	t.Helper()
	log := logger.NewLogger(nil, false)
	st, err := store.NewStore(log, []string{src})
	require.NoError(t, err)
	f := &fixture{store: st, bus: events.NewBus(), eval: &answering{}}
	f.deliver(t, setA, "v1")
	f.deliver(t, setB, "v1")

	port := freePort(t)
	settings := service.Settings{
		Store: st, Bus: f.bus, Logger: log, Resolver: typed.New(f.eval, typed.WithObserver(events.EvaluationObserver(f.bus, Type))),
		Config: map[string]any{"port": port, "formats": []string{"flagd"}, "sources": []string{src}},
		HTTPMiddleware: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if bound != "" {
					r = r.WithContext(middleware.BindSelector(r.Context(), bound))
				}
				next.ServeHTTP(w, r)
			})
		},
	}
	endpoint, err := Factory{}.Create(settings)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- endpoint.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(10 * time.Second):
			t.Error("Serve did not return")
		}
	})

	f.base = "http://127.0.0.1:" + strconv.Itoa(port)
	require.Eventually(t, func() bool {
		resp, err := http.Get(f.base + "/v0/changes?wait=0s") //nolint:noctx // local endpoint
		if err != nil {
			return false
		}
		resp.Body.Close()
		return true
	}, 5*time.Second, 20*time.Millisecond)
	return f
}

type reply struct {
	status    int
	validator string
	answer    flagAnswer
	flags     []flagAnswer
}

func (f *fixture) get(t *testing.T, path, selector, ifNoneMatch string) reply {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, f.base+path, nil)
	require.NoError(t, err)
	if selector != "" {
		req.Header.Set(SelectorHeader, selector)
	}
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", `"`+ifNoneMatch+`"`)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	r := reply{status: resp.StatusCode}
	if resp.StatusCode == http.StatusNotModified || resp.StatusCode == http.StatusBadRequest {
		return r
	}
	var body struct {
		Validator string       `json:"validator"`
		Flags     []flagAnswer `json:"flags"`
		flagAnswer
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	r.validator, r.flags, r.answer = body.Validator, body.Flags, body.flagAnswer
	return r
}

// TestEndpointContract is step 6 of the use case: the contract tests of the core module.
func TestEndpointContract(t *testing.T) {
	get := func(ctx context.Context, rawURL, selector, ifNoneMatch string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		if selector != "" {
			req.Header.Set(SelectorHeader, selector)
		}
		if ifNoneMatch != "" {
			req.Header.Set("If-None-Match", `"`+ifNoneMatch+`"`)
		}
		return http.DefaultClient.Do(req)
	}
	validator := func(ctx context.Context, addr, selector string) (string, error) {
		resp, err := get(ctx, "http://"+addr+"/v0/changes?wait=0s", selector, "")
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		var body struct{ Validator string }
		err = json.NewDecoder(resp.Body).Decode(&body)
		return body.Validator, err
	}

	servicetest.Run(t, Factory{}, servicetest.Inputs{
		PortKey: "port",
		Config:  map[string]any{"formats": []string{"flagd", servicetest.VendorFormat}, "sources": []string{servicetest.Source}},
		HTTP:    true,
		// the example serves no flag configurations and collects no events
		Without: []servicetest.Feature{servicetest.FeatureDocuments, servicetest.FeatureSendEvents},
		Connect: func(_ context.Context, addr string) (<-chan struct{}, error) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			known, err := validator(ctx, addr, "")
			if err != nil {
				return nil, err
			}
			// the long poll answers when it ends, so it runs on its own; the endpoint serves, or the validator would have failed
			ended := make(chan struct{})
			go func() {
				defer close(ended)
				resp, err := get(context.Background(), "http://"+addr+"/v0/changes?wait=30s", "", known)
				if err == nil {
					resp.Body.Close()
				}
			}()
			return ended, nil
		},
		Resolve: func(ctx context.Context, addr, key, selector string) (servicetest.Answer, error) {
			resp, err := get(ctx, "http://"+addr+"/v0/flags/"+key, selector, "")
			if err != nil {
				return servicetest.Answer{}, err
			}
			defer resp.Body.Close()
			var body flagAnswer
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				return servicetest.Answer{}, err
			}
			return servicetest.Answer{ErrorCode: body.ErrorCode}, nil
		},
		Validator: validator,
		Listen: func(ctx context.Context, addr, selector string) (<-chan struct{}, error) {
			known, err := validator(ctx, addr, selector)
			if err != nil {
				return nil, err
			}
			announced := make(chan struct{}, 64)
			go func() {
				for ctx.Err() == nil {
					resp, err := get(ctx, "http://"+addr+"/v0/changes?wait=10s", selector, known)
					if err != nil {
						return
					}
					var body struct{ Validator string }
					_ = json.NewDecoder(resp.Body).Decode(&body)
					resp.Body.Close()
					if resp.StatusCode == http.StatusOK && body.Validator != known {
						known = body.Validator
						announced <- struct{}{}
					}
				}
			}()
			return announced, nil
		},
	})
}

// TestChangesOnlyForSelector is A4: a change is announced to a client only when it concerns its selector.
func TestChangesOnlyForSelector(t *testing.T) {
	f := serve(t, "")
	known := f.get(t, "/v0/changes?wait=0s", "flagSetId="+setA, "").validator
	require.NotEmpty(t, known)

	other := make(chan reply, 1)
	go func() { other <- f.get(t, "/v0/changes?wait=700ms", "flagSetId="+setA, known) }()
	time.Sleep(100 * time.Millisecond)
	f.deliver(t, setB, "v2")
	require.Equal(t, http.StatusNotModified, (<-other).status, "a change of another flag set is not announced")

	concerned := make(chan reply, 1)
	go func() { concerned <- f.get(t, "/v0/changes?wait=5s", "flagSetId="+setA, known) }()
	time.Sleep(100 * time.Millisecond)
	f.deliver(t, setA, "v2")
	got := <-concerned
	require.Equal(t, http.StatusOK, got.status)
	require.NotEqual(t, known, got.validator)

	all := f.get(t, "/v0/changes?wait=0s", "", "")
	require.Equal(t, http.StatusOK, all.status, "a client without a selector hears of every change")
}

// TestValidatorFollowsDocumentVersion pins that the validator comes from the document versions.
func TestValidatorFollowsDocumentVersion(t *testing.T) {
	f := serve(t, "")
	first := f.get(t, "/v0/changes?wait=0s", "flagSetId="+setA, "").validator

	f.deliver(t, setA, "v1") // the same version again
	require.Equal(t, first, f.get(t, "/v0/changes?wait=0s", "flagSetId="+setA, "").validator, "same version, same validator")

	f.deliver(t, setB, "v9")
	require.Equal(t, first, f.get(t, "/v0/changes?wait=0s", "flagSetId="+setA, "").validator, "another flag set's version does not count")

	f.deliver(t, setA, "v2")
	require.NotEqual(t, first, f.get(t, "/v0/changes?wait=0s", "flagSetId="+setA, "").validator, "a new version of its own document")

	require.Equal(t, http.StatusNotModified, f.get(t, "/v0/changes?wait=0s", "flagSetId="+setA,
		f.get(t, "/v0/changes?wait=0s", "flagSetId="+setA, "").validator).status, "a client with the current validator is told nothing changed")
}

// TestEffectiveSelector is A6: the selector bound by middleware and the client's selector, through HTTPMiddleware.
func TestEffectiveSelector(t *testing.T) {
	tests := []struct {
		name, bound, client string
		wantSet, wantSource string
		noFlags             bool
		status              int
	}{
		{name: "bound only", bound: "flagSetId=" + setA, wantSet: setA},
		{name: "client only", client: "flagSetId=" + setB, wantSet: setB},
		{name: "narrowing", bound: "flagSetId=" + setA, client: "source=" + src, wantSet: setA, wantSource: src},
		{name: "conflict", bound: "flagSetId=" + setA, client: "flagSetId=" + setB, noFlags: true},
		{name: "invalid", client: "colour=red", status: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := serve(t, tt.bound)

			one := f.get(t, "/v0/flags/flag", tt.client, "")
			bulk := f.get(t, "/v0/flags", tt.client, "")

			switch {
			case tt.status != 0:
				require.Equal(t, tt.status, one.status)
				require.Equal(t, tt.status, bulk.status)
			case tt.noFlags:
				require.Equal(t, http.StatusNotFound, one.status)
				require.Equal(t, model.FlagNotFoundErrorCode, one.answer.ErrorCode)
				require.Empty(t, bulk.flags, "no flags for a bulk request")
				require.Empty(t, f.eval.requests(), "nothing is evaluated")
			default:
				require.Equal(t, http.StatusOK, one.status)
				require.Len(t, f.eval.requests(), 2)
				for _, req := range f.eval.requests() {
					require.Equal(t, tt.wantSet, req.Selector.FlagSetId(), fmt.Sprint(req))
					require.Equal(t, tt.wantSource, req.Selector.Source())
				}
			}
		})
	}
}
