package telemetry

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
	"go.uber.org/zap"
)

const (
	metricsExporterOtel = "otel"
)

type CollectorConfig struct {
	Target         string
	CertPath       string
	KeyPath        string
	ReloadInterval time.Duration
	CAPath         string
}

// Config of the telemetry runtime. These are expected to be mapped to start-up arguments
type Config struct {
	MetricsExporter string
	CollectorConfig CollectorConfig
}

func RegisterErrorHandling(log *logger.Logger) {
	otel.SetErrorHandler(otelErrorsHandler{
		logger: log,
	})
}

// buildMetricReader builds a metric reader based on provided configurations
// Uses autoexport to automatically handle OTEL environment variables
func buildMetricReader(ctx context.Context, cfg Config) (metric.Reader, error) {
	// For backwards compatibility: set environment variables from flagd configuration
	// before calling autoexport if they are provided via flags
	if cfg.MetricsExporter == metricsExporterOtel && cfg.CollectorConfig.Target != "" {
		// Set OTEL environment variables from configuration if not already set
		setEnvIfNotSet("OTEL_METRICS_EXPORTER", "otlp")
		setEnvIfNotSet("OTEL_EXPORTER_OTLP_ENDPOINT", cfg.CollectorConfig.Target)
		setEnvIfNotSet("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")
	}

	// Use autoexport with Prometheus as fallback for backwards compatibility
	return autoexport.NewMetricReader(
		ctx,
		autoexport.WithFallbackMetricReader(buildDefaultMetricReader),
	)
}

// buildDefaultMetricReader provides the default metric reader
func buildDefaultMetricReader(_ context.Context) (metric.Reader, error) {
	p, err := prometheus.New()
	if err != nil {
		return nil, fmt.Errorf("unable to create default metric reader: %w", err)
	}
	return p, nil
}

// buildResourceFor builds a resource identifier with set of resources and service key as attributes
func buildResourceFor(ctx context.Context, serviceName string, serviceVersion string) (*resource.Resource, error) {
	r, err := resource.New(
		ctx,
		resource.WithOS(),
		resource.WithHost(),
		resource.WithProcessRuntimeVersion(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String(serviceName),
			semconv.ServiceVersionKey.String(serviceVersion)),
		resource.WithFromEnv(),
	)
	if err != nil {
		return nil, fmt.Errorf("unable to create resource identifier: %w", err)
	}
	return r, nil
}

// setEnvIfNotSet sets an environment variable only if it's not already set
func setEnvIfNotSet(key, value string) {
	if os.Getenv(key) == "" {
		os.Setenv(key, value)
	}
}

// OTelErrorsHandler is a custom error interceptor for OpenTelemetry
type otelErrorsHandler struct {
	logger *logger.Logger
}

func (h otelErrorsHandler) Handle(err error) {
	msg := fmt.Sprintf("OpenTelemetry Error: %s", err.Error())
	h.logger.WithFields(zap.String("component", "otel")).Debug(msg)
}
