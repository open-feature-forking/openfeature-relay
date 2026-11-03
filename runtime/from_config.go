package runtime

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	syncbuilder "github.com/open-feature-forking/openfeature-relay/sync/builder"
	"go.uber.org/zap"
)

// from_config is a collection of structures and parsers responsible for deriving flagd runtime

const svcName = "flagd"

// Config is the configuration structure derived from startup arguments.
type Config struct {
	ManagementPort         uint16
	OfrepServicePort       uint16
	OfrepSSEEnabled        bool
	OfrepSSEInactivityDel  int
	OfrepSSEPublicURL      string
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
	// SyncFactories are the sync components that sources may select.
	SyncFactories []sync.Factory
	CORS          []string
	// EvaluatorSettings are the settings of the evaluators of a distribution, keyed by the type of the evaluator's
	// factory; an evaluator gets its own as Settings.Config.
	EvaluatorSettings map[string]map[string]any
	// EndpointSettings are the settings of the service endpoints of a distribution, besides the official ones, keyed by
	// the endpoint's type. An endpoint gets its own as Settings.Config.
	EndpointSettings map[string]map[string]any

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
func FromConfig(logger *logger.Logger, version string, config Config, components Components) (rt *Runtime, err error) {
	config = withComponents(config, components)
	warnOnNoOpConfig(logger, config)

	tp, shutdownTelemetry, err := telemetryProvider(logger, version, components)
	if err != nil {
		return nil, err
	}
	// a runtime that is not built releases what the telemetry provider holds
	defer func() {
		if rt == nil && shutdownTelemetry != nil {
			ctx, cancel := context.WithTimeout(context.Background(), hookBudget)
			defer cancel()
			_ = shutdownTelemetry(ctx)
		}
	}()

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

	// derive the default format and the service endpoints
	defaultFormat, err := defaultFormatOf(logger, store, syncProviders, config.ConfigurationFormats)
	if err != nil {
		return nil, err
	}
	formatTypes := make([]string, 0, len(config.ConfigurationFormats))
	for _, f := range config.ConfigurationFormats {
		formatTypes = append(formatTypes, f.Type())
	}

	bus := events.NewBus()
	rt = &Runtime{
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
		DefaultFormat:   defaultFormat,
		sourceReadiness: sourceReadiness,
	}

	inputs, err := newEndpointInputs(logger, store, bus, components.Endpoints, formatTypes, config.Evaluators,
		func(evaluatorType string) map[string]any { return maps.Clone(config.EvaluatorSettings[evaluatorType]) },
		config.StrictNumericRange)
	if err != nil {
		return nil, err
	}
	rt.Evaluator = inputs.evaluator()
	for _, factory := range components.Endpoints {
		if err := service.ValidateDeclarations(factory); err != nil {
			return nil, fmt.Errorf("the service endpoint %q declares: %w", factory.Type(), err)
		}
		settings := inputs.settings(factory)
		settings.Initialized = rt.Initialized()
		settings.Telemetry = tp
		settings.Config = endpointSettings(config, factory.Type())
		settings.Formats = append([]string{configformat.DefaultType}, formatTypes...)
		settings.Sources = sources
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
	staleLimits, err := staleLimitsOf(syncProviders, iSyncs)
	if err != nil {
		return nil, err
	}
	rt.staleLimits = staleLimits
	if shutdownTelemetry != nil {
		rt.onShutdown("telemetry", shutdownTelemetry)
	}
	return rt, nil
}

// telemetryProvider returns the telemetry provider of the runtime, and how to shut it down. It is the provider of the
// factory in the components, or the noop telemetry provider without one.
func telemetryProvider(
	logger *logger.Logger, version string, components Components,
) (telemetry.Provider, func(context.Context) error, error) {
	if components.Telemetry == nil {
		return telemetry.Noop(), nil, nil
	}
	if err := telemetry.ValidateDeclarations(components.Telemetry); err != nil {
		return nil, nil, fmt.Errorf("the telemetry provider declares: %w", err)
	}
	tp, err := components.Telemetry.Create(context.Background(), telemetry.Settings{
		Logger:              logger,
		DistributionName:    svcName,
		DistributionVersion: version,
		Config:              components.TelemetryConfig,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("error creating the telemetry provider %q: %w", components.Telemetry.Type(), err)
	}
	return tp, tp.Shutdown, nil
}

// staleLimitsOf pairs the sources that set staleAfter with their sync providers, which must report their last contact.
func staleLimitsOf(sources []sync.SourceConfig, syncs []sync.ISync) ([]staleLimit, error) {
	var limits []staleLimit
	for i, source := range sources {
		if source.StaleAfter == 0 {
			continue
		}
		reporter, ok := syncs[i].(sync.ContactReporter)
		if !ok {
			return nil, fmt.Errorf("source %q sets staleAfter, which the %s sync provider cannot honour: it does not report its last contact",
				source.URI, source.Provider)
		}
		limits = append(limits, staleLimit{
			source: source.URI, provider: reporter, after: time.Duration(source.StaleAfter) * time.Second,
		})
	}
	return limits, nil
}

// syncProvidersFromConfig is a helper to build ISync implementations from SourceConfig.
// The sync providers record the universal client-side sync metrics, and the stream metrics of the gRPC source,
// through the telemetry provider, whose gRPC client instrumentation the gRPC source dials with.
func syncProvidersFromConfig(
	logger *logger.Logger,
	sources []sync.SourceConfig,
	tp telemetry.Provider,
	factories []sync.Factory,
) ([]sync.ISync, error) {
	builder := syncbuilder.NewSyncBuilder(factories...)
	builder.Telemetry = tp
	builder.SyncMetricsRecorder = sync.NewMetricsRecorder(tp.Meter(sync.MetricsScope))
	syncs, err := builder.SyncsFromConfig(sources, logger)
	if err != nil {
		return nil, fmt.Errorf("could not create sync sources from config: %w", err)
	}

	return syncs, nil
}

// defaultFormatOf creates the format of the sources that name none, through the factory of the type
// configformat.DefaultType. A distribution without that factory fails when a source needs it, naming the formats it has.
func defaultFormatOf(
	logger *logger.Logger, st *store.Store, providers []sync.SourceConfig, factories []configformat.Factory,
) (configformat.ConfigurationFormat, error) {
	for _, f := range factories {
		if f.Type() == configformat.DefaultType {
			format, err := f.Create(configformat.Settings{Store: st, Logger: logger.WithFields(zap.String("component", "configformat"))})
			if err != nil {
				return nil, fmt.Errorf("error creating configuration format %q: %w", f.Type(), err)
			}
			return format, nil
		}
	}
	for _, provider := range providers {
		if usesDefaultFormat(provider) {
			return nil, fmt.Errorf("source %q names no configuration format, and this distribution has no %q format; available formats: %s",
				provider.URI, configformat.DefaultType, strings.Join(formatTypesOf(factories), ", "))
		}
	}
	return nil, nil
}

func formatTypesOf(factories []configformat.Factory) []string {
	types := make([]string, 0, len(factories))
	for _, f := range factories {
		types = append(types, f.Type())
	}
	sort.Strings(types)
	return types
}

func usesDefaultFormat(provider sync.SourceConfig) bool {
	return provider.Format == "" || provider.Format == configformat.DefaultType
}

// sourcesWithKnownFormat drops the sources that name a configuration format this distribution
// doesn't contain. They get no sync provider, no store registration and no readiness entry.
func sourcesWithKnownFormat(
	logger *logger.Logger, providers []sync.SourceConfig, factories []configformat.Factory,
) []sync.SourceConfig {
	// flagd's format is named available, as it always was; a distribution that lacks it fails at startup instead,
	// for a source that needs it (defaultFormatOf)
	available := append([]string{configformat.DefaultType}, formatTypesOf(factories)...)
	sort.Strings(available)
	available = slices.Compact(available)

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
