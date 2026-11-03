package telemetry_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
)

// The instruments are held through the interfaces, as a component holds the injected provider, so the compiler
// cannot see the noop implementation at the call sites below.
type instruments struct {
	tracer    telemetry.Tracer
	counter   telemetry.Int64Counter
	upDown    telemetry.Int64UpDownCounter
	histogram telemetry.Float64Histogram
}

var (
	sinkAttr telemetry.Attribute
	errSpan  = errors.New("failed")
)

func newInstruments(p telemetry.Provider) instruments {
	m := p.Meter("test")
	return instruments{
		tracer:    p.Tracer("test"),
		counter:   m.Int64Counter("counter"),
		upDown:    m.Int64UpDownCounter("up_down"),
		histogram: m.Float64Histogram("histogram", telemetry.WithBuckets(1, 2, 3)),
	}
}

// TestNoopAllocatesNothing pins what the noop telemetry provider costs a caller: nothing for building attributes or
// for a call without attributes or options, and exactly one allocation, the variadic slice escaping through the
// interface, for a call that passes them.
func TestNoopAllocatesNothing(t *testing.T) {
	in := newInstruments(telemetry.Noop())
	ctx := context.Background()

	free := map[string]func(){
		"building attributes": func() {
			sinkAttr = telemetry.String("feature_flag.key", "flag")
			sinkAttr = telemetry.Int64("count", 3)
			sinkAttr = telemetry.Int("count", 3)
			sinkAttr = telemetry.Float64("ratio", 0.5)
			sinkAttr = telemetry.Bool("ok", true)
		},
		"span start and end": func() {
			_, span := in.tracer.Start(ctx, "span")
			span.End()
		},
		"span error": func() {
			_, span := in.tracer.Start(ctx, "span")
			span.RecordError(errSpan)
			span.SetError("failed")
			span.End()
		},
		"counter":          func() { in.counter.Add(ctx, 1) },
		"up-down counter":  func() { in.upDown.Add(ctx, -1) },
		"histogram":        func() { in.histogram.Record(ctx, 0.25) },
		"set no attribute": func() { _, span := in.tracer.Start(ctx, "span"); span.SetAttributes(); span.End() },
	}
	for name, f := range free {
		if n := testing.AllocsPerRun(100, f); n != 0 {
			t.Errorf("%s: %v allocations, want 0", name, n)
		}
	}

	oneEach := map[string]func(){
		"span start with a kind": func() {
			_, span := in.tracer.Start(ctx, "span", telemetry.WithSpanKind(telemetry.SpanKindServer))
			span.End()
		},
		"set attributes": func() {
			_, span := in.tracer.Start(ctx, "span")
			span.SetAttributes(telemetry.String("k", "v"), telemetry.Int("n", 1))
			span.End()
		},
		"counter with attributes": func() { in.counter.Add(ctx, 1, telemetry.String("k", "v")) },
		"up-down counter with attributes": func() {
			in.upDown.Add(ctx, 1, telemetry.String("k", "v"), telemetry.Bool("b", true))
		},
		"histogram with attributes": func() { in.histogram.Record(ctx, 1, telemetry.Float64("f", 1)) },
	}
	for name, f := range oneEach {
		if n := testing.AllocsPerRun(100, f); n != 1 {
			t.Errorf("%s: %v allocations, want 1", name, n)
		}
	}
}

func TestAttributeValues(t *testing.T) {
	tests := []struct {
		attr telemetry.Attribute
		typ  telemetry.ValueType
		want any
	}{
		{telemetry.String("s", "v"), telemetry.StringType, "v"},
		{telemetry.Int64("i", -7), telemetry.Int64Type, int64(-7)},
		{telemetry.Int("i", 42), telemetry.Int64Type, int64(42)},
		{telemetry.Float64("f", -1.5), telemetry.Float64Type, -1.5},
		{telemetry.Bool("t", true), telemetry.BoolType, true},
		{telemetry.Bool("f", false), telemetry.BoolType, false},
	}
	for _, tt := range tests {
		v := tt.attr.Value
		if v.Type() != tt.typ {
			t.Errorf("%s: type %v, want %v", tt.attr.Key, v.Type(), tt.typ)
		}
		var got any
		switch tt.typ {
		case telemetry.StringType:
			got = v.AsString()
		case telemetry.Int64Type:
			got = v.AsInt64()
		case telemetry.Float64Type:
			got = v.AsFloat64()
		case telemetry.BoolType:
			got = v.AsBool()
		}
		if got != tt.want {
			t.Errorf("%s: value %v, want %v", tt.attr.Key, got, tt.want)
		}
	}
	if s := telemetry.String("s", "1"); s.Value.AsInt64() != 0 || s.Value.AsFloat64() != 0 || s.Value.AsBool() {
		t.Error("a string value reads as another type")
	}
	if (telemetry.Value{}).Type() != telemetry.Invalid {
		t.Error("the zero value is not Invalid")
	}
}

func TestSpanAndInstrumentConfig(t *testing.T) {
	sc := telemetry.NewSpanConfig(
		telemetry.WithAttributes(telemetry.String("a", "1")),
		telemetry.WithSpanKind(telemetry.SpanKindServer),
		telemetry.WithAttributes(telemetry.String("b", "2")),
	)
	if sc.Kind != telemetry.SpanKindServer || len(sc.Attributes) != 2 || sc.Attributes[1].Key != "b" {
		t.Errorf("span config %+v", sc)
	}
	if telemetry.NewSpanConfig().Kind != telemetry.SpanKindInternal {
		t.Error("the default span kind is not internal")
	}

	ic := telemetry.NewInstrumentConfig(
		telemetry.WithDescription("first"),
		telemetry.WithUnit("s"),
		telemetry.WithBuckets(1, 2),
		telemetry.WithDescription("second"),
	)
	if ic.Description != "second" || ic.Unit != "s" || len(ic.Buckets) != 2 {
		t.Errorf("instrument config %+v", ic)
	}
	if telemetry.NewInstrumentConfig().Buckets != nil {
		t.Error("buckets set without an option")
	}
}

type namedFactory string

func (n namedFactory) Type() string { return string(n) }
func (namedFactory) Create(context.Context, telemetry.Settings) (telemetry.Provider, error) {
	return telemetry.Noop(), nil
}

func TestValidateDeclaration(t *testing.T) {
	if err := telemetry.ValidateDeclaration(telemetry.NoopFactory()); err != nil {
		t.Errorf("noop factory: %v", err)
	}
	if err := telemetry.ValidateDeclaration(namedFactory(" ")); err == nil {
		t.Error("an empty type name is accepted")
	}
	if err := telemetry.ValidateDeclaration(nil); err == nil {
		t.Error("a nil factory is accepted")
	}
}

func TestNoopLeavesHandlersUnchanged(t *testing.T) {
	p := telemetry.Noop()
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	wrapped := p.HTTPServerMetrics(p.HTTPServerSpans(h, "op"), telemetry.HTTPMetricsOptions{Service: "s"})
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusTeapot {
		t.Errorf("status %d", rec.Code)
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

// TestNoopMeterHasGauges pins that the noop telemetry provider offers gauges, and records nothing in them.
func TestNoopMeterHasGauges(t *testing.T) {
	gauges, ok := telemetry.Noop().Meter("scope").(telemetry.GaugeMeter)
	if !ok {
		t.Fatal("the noop meter has no gauges")
	}
	gauges.Int64Gauge("last", telemetry.WithUnit("s")).Record(context.Background(), 1, telemetry.String("k", "v"))
}
