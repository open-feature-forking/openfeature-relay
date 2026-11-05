// Package serviceexample is an example service endpoint: a toy REST API over plain HTTP and JSON. It shows
// what an Extension Author's component module contains, and imports the core module only.
//
//   - GET /v0/flags/{key} resolves one flag through the injected resolver.
//   - GET /v0/flags resolves every flag the selector names.
//   - GET /v0/changes long-polls: it answers once a configuration change event concerns the client's selector
//     and the documents behind it have a new version, with that version's validator; it answers 304 when the
//     wait ends first.
//
// The client's selector comes in the Flagd-Selector header. The endpoint resolves for the effective selector:
// the intersection of that and the selector the distribution's middleware bound to the request.
//
// The settings of the endpoint, in Settings.Config:
//
//	port     the TCP port to listen on
//	formats  the configuration formats whose documents the validator covers
//	sources  the sources whose documents the validator covers
//
// The store lists no formats and sources, so the validator is taken over those the distribution names.
package serviceexample

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
)

// Type is the name by which a distribution selects this service endpoint.
const Type = "rest-example"

// SelectorHeader carries the client's selector expression.
const SelectorHeader = "Flagd-Selector"

const (
	defaultWait = 30 * time.Second
	closeBudget = 4 * time.Second
)

// Factory creates the service endpoint; a distribution registers it next to the official ones.
type Factory struct{}

var _ service.Factory = Factory{}

// Type implements service.Factory.
func (Factory) Type() string { return Type }

// Needs implements service.Factory: the endpoint resolves flags through the evaluation router.
func (Factory) Needs() service.Needs { return service.Needs{Router: true} }

// Create implements service.Factory from what a distribution injects.
func (Factory) Create(s service.Settings) (service.Endpoint, error) {
	if s.Resolver == nil || s.Store == nil || s.Bus == nil {
		return nil, fmt.Errorf("the %s endpoint needs a resolver, a store and an event bus", Type)
	}
	port, err := intSetting(s.Config["port"])
	if err != nil {
		return nil, fmt.Errorf("the %s endpoint's port: %w", Type, err)
	}
	return &endpoint{settings: s, port: port, formats: stringsSetting(s.Config["formats"]), sources: stringsSetting(s.Config["sources"])}, nil
}

func intSetting(v any) (int, error) {
	switch n := v.(type) {
	case int:
		return n, nil
	case float64:
		return int(n), nil
	case string:
		return strconv.Atoi(n)
	}
	return 0, fmt.Errorf("expected a number, got %v", v)
}

func stringsSetting(v any) []string {
	switch list := v.(type) {
	case []string:
		return list
	case []any:
		out := make([]string, 0, len(list))
		for _, item := range list {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

type endpoint struct {
	settings service.Settings
	port     int
	formats  []string
	sources  []string
	stopping chan struct{}
}

// Serve implements service.Endpoint: it serves until ctx ends, then ends the open long polls and returns.
func (e *endpoint) Serve(ctx context.Context) error {
	// the distribution decides how the endpoint listens (Settings.Listen); nil means net.Listen
	listener, err := e.settings.Listen.Or()("tcp", ":"+strconv.Itoa(e.port))
	if err != nil {
		return fmt.Errorf("the %s endpoint cannot listen: %w", Type, err)
	}
	e.stopping = make(chan struct{})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v0/flags/{key}", e.flag)
	mux.HandleFunc("GET /v0/flags", e.flags)
	mux.HandleFunc("GET /v0/changes", e.changes)
	server := &http.Server{Handler: e.settings.WrapHTTP(mux), ReadHeaderTimeout: 3 * time.Second}

	go func() {
		<-ctx.Done()
		close(e.stopping)
		shutdown, cancel := context.WithTimeout(context.Background(), closeBudget)
		defer cancel()
		if server.Shutdown(shutdown) != nil {
			_ = server.Close()
		}
	}()
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-e.stopping
	return nil
}

// selector is the selector the request is resolved for. It answers the request itself when there is none:
// 400 for an invalid expression, and the second result true when no flag matches.
func (e *endpoint) selector(w http.ResponseWriter, r *http.Request) (sel store.Selector, noFlags, ok bool) {
	sel, err := middleware.EffectiveSelector(r.Context(), r.Header.Get(SelectorHeader))
	switch {
	case errors.Is(err, middleware.ErrNoFlags):
		return sel, true, true
	case err != nil:
		http.Error(w, err.Error(), http.StatusBadRequest)
		return sel, false, false
	}
	return sel, false, true
}

func evaluationContext(r *http.Request) map[string]any {
	if key := r.URL.Query().Get("targetingKey"); key != "" {
		return map[string]any{"targetingKey": key}
	}
	return nil
}

type flagAnswer struct {
	Key       string `json:"key"`
	Value     any    `json:"value,omitempty"`
	Variant   string `json:"variant,omitempty"`
	Reason    string `json:"reason,omitempty"`
	ErrorCode string `json:"errorCode,omitempty"`
}

func answerOf(key string, value any, variant, reason string, err error) flagAnswer {
	answer := flagAnswer{Key: key, Value: value, Variant: variant, Reason: reason}
	if err != nil {
		answer.ErrorCode = err.Error()
	}
	return answer
}

func (e *endpoint) flag(w http.ResponseWriter, r *http.Request) {
	selector, noFlags, ok := e.selector(w, r)
	if !ok {
		return
	}
	key := r.PathValue("key")
	if noFlags {
		writeJSON(w, http.StatusNotFound, flagAnswer{Key: key, ErrorCode: "FLAG_NOT_FOUND"})
		return
	}
	ctx := context.WithValue(r.Context(), store.SelectorContextKey{}, selector)
	// the resolver publishes the evaluation event
	value := e.settings.Resolver.ResolveAsAnyValue(ctx, "", key, evaluationContext(r))
	writeJSON(w, http.StatusOK, answerOf(key, value.Value, value.Variant, value.Reason, value.Error))
}

func (e *endpoint) flags(w http.ResponseWriter, r *http.Request) {
	selector, noFlags, ok := e.selector(w, r)
	if !ok {
		return
	}
	answers := []flagAnswer{}
	if !noFlags {
		ctx := context.WithValue(r.Context(), store.SelectorContextKey{}, selector)
		values, _, err := e.settings.Resolver.ResolveAllValues(ctx, "", evaluationContext(r))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, v := range values {
			answers = append(answers, answerOf(v.FlagKey, v.Value, v.Variant, v.Reason, v.Error))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"flags": answers})
}

// validator is derived from the versions of the documents behind the selector, not from the flags.
func (e *endpoint) validator(selector store.Selector) string {
	var tuples []string
	view := e.settings.Store.View()
	for _, format := range e.formats {
		for _, source := range e.sources {
			if want := selector.Source(); want != "" && want != source {
				continue
			}
			for _, d := range view.Documents(format, source) {
				if want := selector.FlagSetId(); want != "" && want != d.FlagSet {
					continue
				}
				tuples = append(tuples, strings.Join([]string{d.Format, d.Source, d.FlagSet, d.Version}, "\x00"))
			}
		}
	}
	sort.Strings(tuples)
	sum := sha256.Sum256([]byte(strings.Join(tuples, "\x01")))
	return hex.EncodeToString(sum[:])
}

// concerns reports whether a configuration change event is about documents behind the selector.
func concerns(selector store.Selector, change events.ConfigurationChange) bool {
	if want := selector.FlagSetId(); want != "" && want != change.FlagSet {
		return false
	}
	if want := selector.Source(); want != "" && want != change.Source {
		return false
	}
	return true
}

func (e *endpoint) changes(w http.ResponseWriter, r *http.Request) {
	selector, noFlags, ok := e.selector(w, r)
	if !ok {
		return
	}
	wait := defaultWait
	if v := r.URL.Query().Get("wait"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			http.Error(w, "invalid wait: "+err.Error(), http.StatusBadRequest)
			return
		}
		wait = d
	}
	known := strings.Trim(r.Header.Get("If-None-Match"), `"`)

	// subscribing before the first look leaves no change unseen between the two
	sub := e.settings.Bus.Subscribe(events.SubscribeOptions{Kinds: []events.Kind{events.KindConfigurationChange}, Buffer: 16})
	defer sub.Close()

	current := func() string {
		if noFlags {
			return ""
		}
		return e.validator(selector)
	}
	answer := func(v string) {
		w.Header().Set("ETag", `"`+v+`"`)
		writeJSON(w, http.StatusOK, map[string]string{"validator": v})
	}
	if v := current(); v != known {
		answer(v)
		return
	}

	timeout := time.NewTimer(wait)
	defer timeout.Stop()
	for {
		select {
		case ev, open := <-sub.C():
			if !open {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			change := ev.(events.ConfigurationChange)
			// a subscription that missed events reads the store again, whatever the event says
			if change.Missed == 0 && !concerns(selector, change) {
				continue
			}
			if v := current(); v != known {
				answer(v)
				return
			}
		case <-timeout.C:
			w.WriteHeader(http.StatusNotModified)
			return
		case <-e.stopping:
			w.WriteHeader(http.StatusNotModified)
			return
		case <-r.Context().Done():
			return
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// NewFactory returns the factory of the example endpoint, as a generated main of a distribution calls it.
func NewFactory() service.Factory { return Factory{} }
