package telemetry

import (
	"context"
	"fmt"
	"os"
	"time"

	coretel "github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel/propagation"
	msdk "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// Type is the type name of the OpenTelemetry telemetry provider.
const Type = "otel"

// Settings of the OpenTelemetry telemetry provider, as keys of telemetry.Settings.Config. They map one to one onto
// flagd's --metrics-exporter, --otel-collector-uri, --otel-cert-path, --otel-key-path, --otel-ca-path and
// --otel-reload-interval.
const (
	SettingMetricsExporter = "metricsExporter"
	SettingCollectorURI    = "collectorURI"
	SettingCertPath        = "certPath"
	SettingKeyPath         = "keyPath"
	SettingCAPath          = "caPath"
	SettingReloadInterval  = "reloadInterval"
)

// Factory creates the OpenTelemetry telemetry provider.
type Factory struct {
	opts options
}

type options struct {
	withoutGlobals bool
	spanExporter   trace.SpanExporter
	metricReader   msdk.Reader
}

// Option configures the factory.
type Option func(*options)

// WithoutGlobals builds the provider without installing the global tracer provider, meter provider, propagator and
// error handler, so that tests share no process-wide state.
func WithoutGlobals() Option {
	return func(o *options) { o.withoutGlobals = true }
}

// WithSpanExporter replaces the span exporter the OTEL_* environment variables would select.
func WithSpanExporter(e trace.SpanExporter) Option {
	return func(o *options) { o.spanExporter = e }
}

// WithMetricReader replaces the metric reader the OTEL_* environment variables would select, Prometheus by default.
// A reader serves one provider only.
func WithMetricReader(r msdk.Reader) Option {
	return func(o *options) { o.metricReader = r }
}

// NewFactory returns the factory of the OpenTelemetry telemetry provider.
func NewFactory(opts ...Option) Factory {
	f := Factory{}
	for _, o := range opts {
		o(&f.opts)
	}
	return f
}

// Type returns "otel".
func (Factory) Type() string { return Type }

// Create builds the provider. It installs the global OpenTelemetry state, as flagd always has, unless the factory
// was built WithoutGlobals. An invalid setting or a span exporter or metric reader that cannot be built is an error.
func (f Factory) Create(ctx context.Context, s coretel.Settings) (coretel.Provider, error) {
	cfg, err := parseConfig(s.Config)
	if err != nil {
		return nil, fmt.Errorf("telemetry provider %q: %w", Type, err)
	}
	if s.Logger != nil && !f.opts.withoutGlobals {
		RegisterErrorHandling(s.Logger)
	}

	rsc, err := buildResourceFor(ctx, s.DistributionName, s.DistributionVersion)
	if err != nil {
		return nil, fmt.Errorf("telemetry provider %q: %w", Type, err)
	}

	p := &Provider{
		serviceName:    s.DistributionName,
		tracerProvider: tracenoop.NewTracerProvider(),
		propagator:     propagation.NewCompositeTextMapPropagator(),
	}

	exporter := f.opts.spanExporter
	if exporter == nil {
		if exporter, err = buildSpanExporter(ctx, cfg); err != nil {
			return nil, fmt.Errorf("telemetry provider %q: %w", Type, err)
		}
	}
	// without a span exporter, spans and trace propagation stay noop, as before
	if exporter != nil {
		p.sdkTracerProvider = trace.NewTracerProvider(
			trace.WithSampler(trace.AlwaysSample()),
			trace.WithSpanProcessor(trace.NewBatchSpanProcessor(exporter)),
			trace.WithResource(rsc))
		p.tracerProvider = p.sdkTracerProvider
		p.propagator = propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
	}

	reader := f.opts.metricReader
	if reader == nil {
		if reader, err = buildMetricReader(ctx, cfg); err != nil {
			if p.sdkTracerProvider != nil {
				_ = p.sdkTracerProvider.Shutdown(ctx)
			}
			return nil, fmt.Errorf("telemetry provider %q: failed to setup metric reader: %w", Type, err)
		}
	}
	p.meterProvider = newMeterProvider(reader, rsc, s.DistributionName)
	p.recorder = newRecorder(p.meterProvider.Meter(s.DistributionName))

	if !f.opts.withoutGlobals {
		p.installGlobals()
	}
	return p, nil
}

// buildSpanExporter returns the span exporter the configuration and the OTEL_* environment variables select, nil
// when they select none.
func buildSpanExporter(ctx context.Context, cfg Config) (trace.SpanExporter, error) {
	// For backwards compatibility: set environment variables from flagd configuration
	// before calling autoexport if they are provided via flags
	if cfg.CollectorConfig.Target != "" {
		setEnvIfNotSet("OTEL_TRACES_EXPORTER", "otlp")
		setEnvIfNotSet("OTEL_EXPORTER_OTLP_ENDPOINT", cfg.CollectorConfig.Target)
		setEnvIfNotSet("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")
	}
	exporter, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create span exporter (OTEL_TRACES_EXPORTER=%q): %w", os.Getenv("OTEL_TRACES_EXPORTER"), err)
	}
	if autoexport.IsNoneSpanExporter(exporter) {
		return nil, nil
	}
	return exporter, nil
}

// parseConfig reads the settings into today's Config. An unknown key or a value of the wrong type is an error that
// names the key and the value.
func parseConfig(raw map[string]any) (Config, error) {
	var cfg Config
	for key, value := range raw {
		var err error
		switch key {
		case SettingMetricsExporter:
			cfg.MetricsExporter, err = stringSetting(key, value)
		case SettingCollectorURI:
			cfg.CollectorConfig.Target, err = stringSetting(key, value)
		case SettingCertPath:
			cfg.CollectorConfig.CertPath, err = stringSetting(key, value)
		case SettingKeyPath:
			cfg.CollectorConfig.KeyPath, err = stringSetting(key, value)
		case SettingCAPath:
			cfg.CollectorConfig.CAPath, err = stringSetting(key, value)
		case SettingReloadInterval:
			cfg.CollectorConfig.ReloadInterval, err = durationSetting(key, value)
		default:
			err = fmt.Errorf("unknown setting %q", key)
		}
		if err != nil {
			return Config{}, err
		}
	}
	return cfg, nil
}

func stringSetting(key string, value any) (string, error) {
	s, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("setting %q: %v is not a string", key, value)
	}
	return s, nil
}

// durationSetting accepts a time.Duration or a duration string such as "1h".
func durationSetting(key string, value any) (time.Duration, error) {
	switch v := value.(type) {
	case time.Duration:
		return v, nil
	case string:
		d, err := time.ParseDuration(v)
		if err != nil {
			return 0, fmt.Errorf("setting %q: %q is not a duration", key, v)
		}
		return d, nil
	default:
		return 0, fmt.Errorf("setting %q: %v is not a duration", key, value)
	}
}
