package runtime

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	flagdformat "github.com/open-feature-forking/openfeature-relay/configformat/flagd"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	flagdeval "github.com/open-feature-forking/openfeature-relay/evaluator/flagd"
	syncbuilder "github.com/open-feature-forking/openfeature-relay/sync/builder"
	flagdgrpc "github.com/open-feature-forking/openfeature-relay/sync/flagd-grpc"
	otelprovider "github.com/open-feature-forking/openfeature-relay/telemetry/otel"
	flageval "github.com/open-feature/flagd/flagd/pkg/service/flag-evaluation"
	"github.com/open-feature/flagd/flagd/pkg/service/flag-evaluation/ofrep"
	flagsync "github.com/open-feature/flagd/flagd/pkg/service/flag-sync"
	"go.uber.org/zap"
	googlegrpc "google.golang.org/grpc"
)

// from_config is a collection of structures and parsers responsible for deriving flagd runtime

const svcName = "flagd"

// Config is the configuration structure derived from startup arguments.
type Config struct {
	MetricExporter         string
	ManagementPort         uint16
	OfrepServicePort       uint16
	OfrepSSEEnabled        bool
	OfrepSSEInactivityDel  int
	OfrepSSEPublicURL      string
	OtelCollectorURI       string
	OtelCertPath           string
	OtelKeyPath            string
	OtelCAPath             string
	OtelReloadInterval     time.Duration
	ServiceCertPath        string
	ServiceKeyPath         string
	ServicePort            uint16
	ServiceSocketPath      string
	SyncServicePort        uint16
	SyncServiceHTTPEnabled bool
	SyncServiceSocketPath  string
	StreamDeadline         time.Duration
	DisableSyncMetadata    bool
	StrictNumericRange     bool // TODO(#2101): remove

	// Deprecated: no-op, retained so existing configurations keep working. See warnOnNoOpConfig.
	KeepAliveMinTime time.Duration
	// Deprecated: no-op, see KeepAliveMinTime.
	KeepAlivePermitWithoutStream bool

	SyncProviders []sync.SourceConfig
	// Deprecated: pass Components.ConfigurationFormats; both apply.
	// ConfigurationFormats are the formats, besides flagd's own, that sources may name.
	ConfigurationFormats []configformat.Factory
	// Deprecated: pass Components.Evaluators; both apply.
	// Evaluators are the evaluators, besides flagd's own, that resolve the flags of a configuration format.
	// Each one's format must be flagd's own or one of ConfigurationFormats.
	Evaluators []evaluator.Factory
	// Deprecated: pass Components.Sync; both apply.
	// SyncFactories are the sync components, besides the official ones, that sources may select.
	SyncFactories []sync.Factory
	CORS          []string

	ContextValues              map[string]any
	HeaderToContextKeyMappings map[string]string
	MaxRequestBodyBytes        int64
	MaxRequestHeaderBytes      int64
}

// Defaults of the removed keepalive enforcement policy, kept so an unchanged config stays silent.
const (
	KeepAliveMinTimeDefault             = 30 * time.Second
	KeepAlivePermitWithoutStreamDefault = true
)

// warnOnNoOpConfig reports settings that are still accepted but no longer do anything.
func warnOnNoOpConfig(logger *logger.Logger, config Config) {
	//nolint:staticcheck // SA1019 reading the no-op fields is the point
	if config.KeepAliveMinTime != KeepAliveMinTimeDefault ||
		config.KeepAlivePermitWithoutStream != KeepAlivePermitWithoutStreamDefault {
		logger.Warn("keep-alive-min-time and keep-alive-permit-without-stream are no-ops and will be " +
			"removed in a later version. The flag sync server is served by connect over net/http, which " +
			"imposes no minimum keepalive ping interval and always permits pings without an active stream.")
	}
}

// FromConfig builds a runtime from startup configurations
// nolint: funlen
func FromConfig(logger *logger.Logger, version string, config Config, components Components) (*Runtime, error) {
	config = withComponents(config, components)
	warnOnNoOpConfig(logger, config)

	telCfg := otelprovider.Config{
		MetricsExporter: config.MetricExporter,
		CollectorConfig: otelprovider.CollectorConfig{
			Target:         config.OtelCollectorURI,
			CertPath:       config.OtelCertPath,
			KeyPath:        config.OtelKeyPath,
			CAPath:         config.OtelCAPath,
			ReloadInterval: config.OtelReloadInterval,
		},
	}

	// register error handling for OpenTelemetry
	otelprovider.RegisterErrorHandling(logger)

	// register trace provider for the runtime
	err := otelprovider.BuildTraceProvider(context.Background(), logger, svcName, version, telCfg)
	if err != nil {
		// log the error but continue
		logger.Error(fmt.Sprintf("error building trace provider: %v", err))
	}

	// build metrics recorder with startup configurations
	_, err = otelprovider.BuildMetricsRecorder(context.Background(), svcName, version, telCfg)
	if err != nil {
		// log the error but continue
		logger.Error(fmt.Sprintf("error building metrics recorder: %v", err))
	}
	// the providers built above are global state; the components record through the telemetry provider over it
	tp := otelprovider.FromGlobals(svcName)

	syncProviders := sourcesWithKnownFormat(logger, config.SyncProviders, config.ConfigurationFormats)
	sources := []string{}

	// URIs are registered verbatim (including any blob query string like
	// s3://bucket/key?use_path_style=true); the sync implementation must
	// emit DataSync.Source matching this exact string. See flagd#1973.
	for _, provider := range syncProviders {
		sources = append(sources, provider.URI)
	}
	sourceReadiness := make(map[string]bool, len(sources))
	for _, source := range sources {
		sourceReadiness[source] = false
	}

	// build flag store, collect flag sources & fill sources details
	store, err := store.NewStore(logger, sources)
	if err != nil {
		return nil, fmt.Errorf("error creating flag store: %w", err)
	}

	formats, err := configurationFormats(logger, store, syncProviders, config.ConfigurationFormats)
	if err != nil {
		return nil, err
	}

	// derive the evaluator options and the service endpoints
	var evalOpts []flagdeval.JSONEvaluatorOption
	// TODO(#2101): remove
	if config.StrictNumericRange {
		evalOpts = append(evalOpts, flagdeval.WithStrictNumericRange())
	}
	formatTypes := make([]string, 0, len(config.ConfigurationFormats))
	for _, f := range config.ConfigurationFormats {
		formatTypes = append(formatTypes, f.Type())
	}

	bus := events.NewBus()
	rt := &Runtime{
		Logger:         logger.WithFields(zap.String("component", "runtime")),
		Bus:            bus,
		Telemetry:      tp,
		MetricsHandler: metricsHandler(tp, components),
		ServiceConfig: service.Configuration{
			Port:                       config.ServicePort,
			ManagementPort:             config.ManagementPort,
			ServiceName:                svcName,
			KeyPath:                    config.ServiceKeyPath,
			CertPath:                   config.ServiceCertPath,
			SocketPath:                 config.ServiceSocketPath,
			CORS:                       config.CORS,
			ContextValues:              config.ContextValues,
			HeaderToContextKeyMappings: config.HeaderToContextKeyMappings,
			StreamDeadline:             config.StreamDeadline,
			MaxRequestBodyBytes:        config.MaxRequestBodyBytes,
			MaxRequestHeaderBytes:      config.MaxRequestHeaderBytes,
		},
		Formats:         formats,
		DefaultFormat:   flagdformat.NewFormat(store, logger.WithFields(zap.String("component", "configformat"))),
		sourceReadiness: sourceReadiness,
	}

	inputs, err := newEndpointInputs(logger, store, bus, components.Endpoints, formatTypes, config.Evaluators, evalOpts)
	if err != nil {
		return nil, err
	}
	if inputs.router != nil {
		rt.Evaluator = inputs.router.IEvaluator()
	}
	endpointConfigs := endpointConfigs{
		config: config, serviceConfig: rt.ServiceConfig, sources: sources,
	}
	for _, factory := range components.Endpoints {
		if err := service.ValidateDeclarations(factory); err != nil {
			return nil, fmt.Errorf("the service endpoint %q declares: %w", factory.Type(), err)
		}
		settings := inputs.settings(factory)
		settings.Initialized = rt.Initialized()
		settings.Telemetry = tp
		settings.Config = endpointConfigs.forEndpoint(factory.Type())
		endpoint, err := factory.Create(settings)
		if err != nil {
			return nil, fmt.Errorf("error creating the service endpoint %q: %w", factory.Type(), err)
		}
		rt.Endpoints = append(rt.Endpoints, NamedEndpoint{Type: factory.Type(), Endpoint: endpoint})
	}

	// build sync providers with a shared source-agnostic sync-metrics recorder so every
	// provider (grpc, http, file, kubernetes, blob) emits the universal client-side sync
	// metrics into the same reader as the rest of flagd's telemetry.
	syncLogger := logger.WithFields(zap.String("component", "sync"))
	iSyncs, err := syncProvidersFromConfig(syncLogger, syncProviders, tp, config.SyncFactories)
	if err != nil {
		return nil, err
	}
	rt.Syncs = iSyncs
	return rt, nil
}

// endpointConfigs derives the settings of the official endpoints from the operator's configuration.
type endpointConfigs struct {
	config        Config
	serviceConfig service.Configuration
	sources       []string
}

// forEndpoint returns Settings.Config for the endpoint type; an extension's endpoint has no settings of the
// operator's yet.
func (c endpointConfigs) forEndpoint(endpointType string) map[string]any {
	switch endpointType {
	case flageval.Type:
		return map[string]any{flageval.ConfigKey: flageval.EndpointConfig{
			Configuration: c.serviceConfig,
		}}
	case ofrep.Type:
		return map[string]any{ofrep.ConfigKey: ofrep.EndpointConfig{
			Service: ofrep.SvcConfiguration{
				Port:                  c.config.OfrepServicePort,
				ServiceName:           svcName,
				MaxRequestBodyBytes:   c.config.MaxRequestBodyBytes,
				MaxRequestHeaderBytes: c.config.MaxRequestHeaderBytes,
				SSEEnabled:            c.config.OfrepSSEEnabled,
				SSEInactivityDelaySec: c.config.OfrepSSEInactivityDel,
				SSEPublicURL:          c.config.OfrepSSEPublicURL,
			},
			CORS:                       c.config.CORS,
			ContextValues:              c.config.ContextValues,
			HeaderToContextKeyMappings: c.config.HeaderToContextKeyMappings,
		}}
	case flagsync.Type:
		return map[string]any{flagsync.ConfigKey: flagsync.SvcConfigurations{
			Port:                  c.config.SyncServicePort,
			Sources:               c.sources,
			ContextValues:         c.config.ContextValues,
			KeyPath:               c.config.ServiceKeyPath,
			CertPath:              c.config.ServiceCertPath,
			SocketPath:            c.config.SyncServiceSocketPath,
			HTTPEnabled:           c.config.SyncServiceHTTPEnabled,
			ServiceName:           svcName,
			CORS:                  c.config.CORS,
			StreamDeadline:        c.config.StreamDeadline,
			DisableSyncMetadata:   c.config.DisableSyncMetadata,
			MaxRequestHeaderBytes: c.config.MaxRequestHeaderBytes,
			MaxRequestBodyBytes:   c.config.MaxRequestBodyBytes,
		}}
	}
	return map[string]any{}
}

// grpcClientInstrumentation is implemented by a telemetry provider that instruments outbound gRPC connections.
type grpcClientInstrumentation interface {
	GRPCDialOptions() []googlegrpc.DialOption
}

// syncProvidersFromConfig is a helper to build ISync implementations from SourceConfig.
// The sync providers record the universal client-side sync metrics, and the stream metrics of the gRPC source,
// through the telemetry provider, whose gRPC client instrumentation the gRPC source dials with.
func syncProvidersFromConfig(
	logger *logger.Logger,
	sources []sync.SourceConfig,
	tp telemetry.Provider,
	extra []sync.Factory,
) ([]sync.ISync, error) {
	var dialOptions []googlegrpc.DialOption
	if p, ok := tp.(grpcClientInstrumentation); ok {
		dialOptions = p.GRPCDialOptions()
	}
	factories := syncbuilder.OfficialWith(syncbuilder.OfficialOptions{
		StreamMetrics:   flagdgrpc.NewStreamMetrics(tp.Meter(flagdgrpc.StreamMetricsScope)),
		GRPCDialOptions: dialOptions,
	})
	builder := syncbuilder.NewSyncBuilder(append(factories, extra...)...)
	builder.SyncMetricsRecorder = sync.NewMetricsRecorder(tp.Meter(sync.MetricsScope))
	syncs, err := builder.SyncsFromConfig(sources, logger)
	if err != nil {
		return nil, fmt.Errorf("could not create sync sources from config: %w", err)
	}

	return syncs, nil
}

func usesDefaultFormat(provider sync.SourceConfig) bool {
	return provider.Format == "" || provider.Format == configformat.DefaultType
}

// sourcesWithKnownFormat drops the sources that name a configuration format this distribution
// doesn't contain. They get no sync provider, no store registration and no readiness entry.
func sourcesWithKnownFormat(
	logger *logger.Logger, providers []sync.SourceConfig, factories []configformat.Factory,
) []sync.SourceConfig {
	available := []string{configformat.DefaultType}
	for _, f := range factories {
		available = append(available, f.Type())
	}
	sort.Strings(available)

	kept := make([]sync.SourceConfig, 0, len(providers))
	for _, provider := range providers {
		if !usesDefaultFormat(provider) && !slices.Contains(available, provider.Format) {
			logger.Error(fmt.Sprintf(
				"skipping source %q: configuration format %q is not available, available formats: %s",
				provider.URI, provider.Format, strings.Join(available, ", ")))
			continue
		}
		kept = append(kept, provider)
	}
	return kept
}

// configurationFormats creates the format of every source that names one other than flagd's.
func configurationFormats(
	logger *logger.Logger, st *store.Store, providers []sync.SourceConfig, factories []configformat.Factory,
) (map[string]configformat.ConfigurationFormat, error) {
	formats := map[string]configformat.ConfigurationFormat{}
	for _, provider := range providers {
		if usesDefaultFormat(provider) {
			continue
		}
		var factory configformat.Factory
		for _, f := range factories {
			if f.Type() == provider.Format {
				factory = f
			}
		}
		format, err := factory.Create(configformat.Settings{Store: st, Logger: logger})
		if err != nil {
			return nil, fmt.Errorf("error creating configuration format %q for source %q: %w",
				provider.Format, provider.URI, err)
		}
		formats[provider.URI] = format
	}
	return formats, nil
}

// withComponents appends the components to the deprecated slices of the Config, which keep applying.
func withComponents(config Config, components Components) Config {
	config.ConfigurationFormats = append(slices.Clone(config.ConfigurationFormats), components.ConfigurationFormats...) //nolint:staticcheck // deprecated, still applied
	config.Evaluators = append(slices.Clone(config.Evaluators), components.Evaluators...)                               //nolint:staticcheck // deprecated, still applied
	config.SyncFactories = append(slices.Clone(config.SyncFactories), components.Sync...)                               //nolint:staticcheck // deprecated, still applied
	return config
}

// metricsHandler returns what serves /metrics on the management port: the handler of the components, or else the one
// of the telemetry provider if it has one.
func metricsHandler(tp telemetry.Provider, components Components) http.Handler {
	if components.MetricsHandler != nil {
		return components.MetricsHandler
	}
	if p, ok := tp.(interface{ MetricsHandler() http.Handler }); ok {
		return p.MetricsHandler()
	}
	return nil
}
