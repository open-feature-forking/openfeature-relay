// Package eventcollector is the event collector service endpoint: it accepts the evaluation and tracking events that
// in-process providers record and puts them on the event bus, so that they reach the same event sinks as the events of
// the remote evaluation endpoints (FR-039, UC-021 A3 and A4).
//
// The endpoint is inert unless it is given a port. With the setting
//
//	{"port": 8017}
//
// it serves POST /events/v1 on that port. Without one, Serve opens no port and blocks until its context ends.
//
// In flagd-full the operator sets the port with the event-collector key of the configuration file, or the same
// settings under endpoint-settings, which wins where both set a key. flagd's own flags reach the collector as they
// reach OFREP: --cors-origin gives it its CORS origins, --max-request-body and --max-request-header its size limits,
// and the service name is flagd's. A setting of the operator wins over a flag.
//
// The protocol is provisional and specific to this project: no OpenFeature specification defines how an in-process
// provider reports its events. The body is a JSON object with an "events" array, whose items are objects with a "kind":
//
//	{"events": [
//	  {"kind": "evaluation", "time": "2026-01-02T03:04:05Z", "flagKey": "checkout", "flagSet": "web", "format": "flagd",
//	   "variant": "on", "reason": "TARGETING_MATCH", "errorCode": "", "targetingKey": "user-1", "value": true, "hasValue": true},
//	  {"kind": "tracking", "time": "2026-01-02T03:04:06Z", "name": "purchase", "targetingKey": "user-1", "value": 9.5,
//	   "details": {"currency": "EUR"}}
//	]}
//
// An evaluation event needs a flagKey; a tracking event needs a name. Every other field is optional: time defaults to the
// moment the batch arrives, hasValue to whether a value other than null was sent, and value of a tracking event to 0.
// The origin is always in-process; one that a client sends is ignored.
//
// Events are accepted one by one. Every valid event of a batch is published. A batch whose events are all valid is
// answered 202 with {"accepted": n}. A batch with invalid events is answered 400 with {"accepted": n, "rejected":
// [{"index": i, "reason": "..."}]}, which lists every invalid event by its position in the batch; nothing is published
// for those, so a sender that resends only them does not publish an event twice. A body that is not such an object is
// answered 400 with {"error": "..."}, one above the size limit 413, another method 405 and another path 404.
//
// A number in "value" of an evaluation event without a fraction or exponent arrives as an integer, as the value of an
// integer flag does on the remote evaluation endpoints; any other number arrives as a float, as does every number
// inside an object value or in "details".
package eventcollector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	corsmw "github.com/open-feature-forking/openfeature-relay/service/transport/middleware/cors"
	"go.uber.org/zap"
)

// Type is the type of the event collector endpoint.
const Type = "event-collector"

// Path is the path the endpoint serves.
const Path = "/events/v1"

// DefaultMaxRequestBodyBytes is the size limit of a request body when the settings do not give one. A limit of 0
// switches it off, as it does for the other endpoints.
const DefaultMaxRequestBodyBytes = 1_000_000

// DefaultServiceName is the service name recorded with the HTTP metrics when the settings do not give one, flagd's.
const DefaultServiceName = "flagd"

// shutdownGrace is how long a request in flight gets when the context ends; then the connections are closed.
const shutdownGrace = time.Second

type config struct {
	port                  uint16
	serviceName           string
	cors                  []string
	maxRequestBodyBytes   int64
	maxRequestHeaderBytes int64
}

// decodeConfig reads Settings.Config. Its keys, the names OFREP uses for the same things, are:
//
//	port                       a number; 0 or absent leaves the endpoint inert
//	service-name               a string, recorded with the HTTP metrics; default "flagd"
//	cors                       a list of allowed origins
//	max-request-body-bytes     a number, default 1000000; 0 means no limit
//	max-request-header-bytes   a number; 0 means Go's default
func decodeConfig(settings map[string]any) (config, error) {
	cfg := config{serviceName: DefaultServiceName, maxRequestBodyBytes: DefaultMaxRequestBodyBytes}
	err := service.ConfigDecoder{
		"port":                     service.Set(&cfg.port, service.Uint16),
		"service-name":             service.Set(&cfg.serviceName, service.String),
		"cors":                     service.Set(&cfg.cors, service.Strings),
		"max-request-body-bytes":   service.Set(&cfg.maxRequestBodyBytes, service.Int64),
		"max-request-header-bytes": service.Set(&cfg.maxRequestHeaderBytes, service.Int64),
	}.Decode(settings)
	if err == nil && (cfg.maxRequestBodyBytes < 0 || cfg.maxRequestHeaderBytes < 0) {
		err = errors.New("the size limits must not be negative")
	}
	return cfg, err
}

// Factory creates the event collector endpoint. It resolves no flag and so declares no need.
type Factory struct{}

var _ service.Factory = Factory{}

// Type implements service.Factory.
func (Factory) Type() string { return Type }

// Needs implements service.Factory.
func (Factory) Needs() service.Needs { return service.Needs{} }

// Create implements service.Factory.
func (Factory) Create(s service.Settings) (service.Endpoint, error) {
	if s.Bus == nil {
		return nil, fmt.Errorf("the %s endpoint needs the event bus", Type)
	}
	cfg, err := decodeConfig(s.Config)
	if err != nil {
		return nil, fmt.Errorf("the %s endpoint: %w", Type, err)
	}
	e := &endpoint{port: cfg.port, listen: s.Listen}
	if cfg.port == 0 {
		return e, nil
	}

	collector := &collector{bus: s.Bus}
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+Path, collector.collect)
	var h http.Handler = mux
	if cfg.maxRequestBodyBytes > 0 {
		h = http.MaxBytesHandler(h, cfg.maxRequestBodyBytes)
	}
	// CORS, then the distribution's middleware, then the telemetry provider's instrumentation, as the other HTTP
	// endpoints order them.
	tp := s.TelemetryProvider()
	h = tp.HTTPServerMetrics(h, telemetry.HTTPMetricsOptions{Service: cfg.serviceName, Route: Path})
	h = tp.HTTPServerSpans(h, "flagd.event-collector")
	h = s.WrapHTTP(h)
	h = corsmw.New(cfg.cors).Handler(h)

	e.server = &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.port),
		Handler:           h,
		ReadHeaderTimeout: 3 * time.Second,
		// slowloris protection; a batch is small
		ReadTimeout:    5 * time.Second,
		MaxHeaderBytes: int(cfg.maxRequestHeaderBytes),
	}
	log := s.Logger
	if log == nil {
		log = logger.NewLogger(nil, false)
	}
	e.log = log.WithFields(zap.String("component", "EventCollector"))
	return e, nil
}

type endpoint struct {
	port   uint16
	server *http.Server
	log    *logger.Logger
	listen service.ListenFunc
}

// Serve implements service.Endpoint. Without a port it opens nothing and blocks until ctx ends. With one it listens
// until ctx ends, then gives requests in flight a second and closes the connections.
func (e *endpoint) Serve(ctx context.Context) error {
	if e.server == nil {
		<-ctx.Done()
		return nil
	}
	errc := make(chan error, 1)
	go func() {
		listener, err := e.listen.Or()("tcp", e.server.Addr)
		if err != nil {
			errc <- err
			return
		}
		errc <- e.server.Serve(listener)
	}()
	e.log.Info(fmt.Sprintf("event collector listening at %d", e.port))

	select {
	case err := <-errc:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("the %s endpoint: %w", Type, err)
		}
		return nil
	case <-ctx.Done():
		grace, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := e.server.Shutdown(grace); err != nil {
			_ = e.server.Close()
		}
		<-errc
		return nil
	}
}

// NewFactory returns the factory of the event collector endpoint, as a generated main of a distribution calls it.
func NewFactory() service.Factory { return Factory{} }

type collector struct{ bus *events.Bus }

type rejection struct {
	Index  int    `json:"index"`
	Reason string `json:"reason"`
}

func (c *collector) collect(w http.ResponseWriter, r *http.Request) {
	if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(strings.ToLower(ct), "application/json") {
		write(w, http.StatusUnsupportedMediaType, map[string]any{"error": fmt.Sprintf("the content type must be application/json, observed %q", ct)})
		return
	}
	var body struct {
		Events *[]json.RawMessage `json:"events"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			write(w, http.StatusRequestEntityTooLarge, map[string]any{"error": fmt.Sprintf("the body is larger than %d bytes", tooLarge.Limit)})
			return
		}
		write(w, http.StatusBadRequest, map[string]any{"error": "the body must be a JSON object with an events array: " + err.Error()})
		return
	}
	if body.Events == nil {
		write(w, http.StatusBadRequest, map[string]any{"error": `the body must be a JSON object with an "events" array`})
		return
	}

	now := time.Now()
	accepted := make([]events.Event, 0, len(*body.Events))
	var rejected []rejection
	for i, raw := range *body.Events {
		ev, reason := parseEvent(raw, now)
		if reason != "" {
			rejected = append(rejected, rejection{Index: i, Reason: reason})
			continue
		}
		accepted = append(accepted, ev)
	}
	for _, ev := range accepted {
		switch v := ev.(type) {
		case events.Evaluation:
			events.PublishEvaluation(c.bus, v)
		case events.Tracking:
			events.PublishTracking(c.bus, v)
		}
	}
	if len(rejected) > 0 {
		write(w, http.StatusBadRequest, map[string]any{"accepted": len(accepted), "rejected": rejected})
		return
	}
	write(w, http.StatusAccepted, map[string]any{"accepted": len(accepted)})
}

func write(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// parseEvent turns one item of the batch into an event marked in-process, or says why it cannot.
func parseEvent(raw json.RawMessage, now time.Time) (events.Event, string) {
	var item map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&item); err != nil || item == nil {
		return nil, "the event must be a JSON object"
	}
	when, reason := optTime(item, "time", now)
	if reason != "" {
		return nil, reason
	}

	switch kind, _ := item["kind"].(string); events.Kind(kind) {
	case events.KindEvaluation:
		ev := events.Evaluation{Time: when, Endpoint: Type, Origin: events.OriginInProcess, Value: topLevelNumber(item["value"])}
		fields := map[string]*string{
			"flagKey": &ev.FlagKey, "flagSet": &ev.FlagSet, "format": &ev.Format, "variant": &ev.Variant,
			"reason": &ev.Reason, "errorCode": &ev.ErrorCode, "targetingKey": &ev.TargetingKey,
		}
		for name, dst := range fields {
			s, reason := optString(item, name)
			if reason != "" {
				return nil, reason
			}
			*dst = s
		}
		if ev.FlagKey == "" {
			return nil, `an evaluation event needs a "flagKey"`
		}
		ev.HasValue = item["value"] != nil
		if v, ok := item["hasValue"]; ok {
			b, isBool := v.(bool)
			if !isBool {
				return nil, `"hasValue" must be a boolean`
			}
			ev.HasValue = b
		}
		return ev, ""

	case events.KindTracking:
		ev := events.Tracking{Time: when, Origin: events.OriginInProcess}
		var reason string
		if ev.EventName, reason = optString(item, "name"); reason != "" {
			return nil, reason
		}
		if ev.EventName == "" {
			return nil, `a tracking event needs a "name"`
		}
		if ev.TargetingKey, reason = optString(item, "targetingKey"); reason != "" {
			return nil, reason
		}
		if v, ok := item["value"]; ok && v != nil {
			number, isNumber := v.(json.Number)
			if !isNumber {
				return nil, `"value" of a tracking event must be a number`
			}
			n, err := number.Float64()
			if err != nil {
				return nil, `"value" of a tracking event must be a number`
			}
			ev.Value = n
		}
		if v, ok := item["details"]; ok && v != nil {
			details, isObject := v.(map[string]any)
			if !isObject {
				return nil, `"details" must be an object`
			}
			ev.Details = floats(details).(map[string]any)
		}
		return ev, ""
	}
	return nil, fmt.Sprintf(`the "kind" must be %q or %q`, events.KindEvaluation, events.KindTracking)
}

// topLevelNumber turns the number of a value into an integer when it is one and into a float otherwise, and every
// number inside an object or array into a float; other values stay as they are.
func topLevelNumber(v any) any {
	if n, ok := v.(json.Number); ok {
		if i, err := n.Int64(); err == nil {
			return i
		}
	}
	return floats(v)
}

// floats replaces every json.Number inside v with a float64, as the JSON of a flag configuration yields them.
func floats(v any) any {
	switch x := v.(type) {
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return x.String()
		}
		return f
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, item := range x {
			out[k] = floats(item)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = floats(item)
		}
		return out
	}
	return v
}

func optString(item map[string]any, key string) (string, string) {
	v, ok := item[key]
	if !ok || v == nil {
		return "", ""
	}
	s, isString := v.(string)
	if !isString {
		return "", fmt.Sprintf("%q must be a string", key)
	}
	return s, ""
}

func optTime(item map[string]any, key string, fallback time.Time) (time.Time, string) {
	s, reason := optString(item, key)
	if reason != "" || s == "" {
		return fallback, reason
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Sprintf("%q must be an RFC 3339 time", key)
	}
	return t, ""
}
