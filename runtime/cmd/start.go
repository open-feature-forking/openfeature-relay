package cmd

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/distribution"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/fips"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/runtime"
	syncbuilder "github.com/open-feature-forking/openfeature-relay/sync/builder"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// The settings of the telemetry provider of flagd-full, as its documentation names them: the table that maps the flags
// onto the component they belong to (UC-001 Q15).
const (
	telemetryMetricsExporter = "metricsExporter"
	telemetryCollectorURI    = "collectorURI"
	telemetryCertPath        = "certPath"
	telemetryKeyPath         = "keyPath"
	telemetryCAPath          = "caPath"
	telemetryReloadInterval  = "reloadInterval"
)

const (
	corsFlagName               = "cors-origin"
	logFormatFlagName          = "log-format"
	managementPortFlagName     = "management-port"
	metricsExporter            = "metrics-exporter"
	ofrepPortFlagName          = "ofrep-port"
	ofrepSSEEnabledFlagName    = "ofrep-sse-enabled"
	ofrepSSEInactivityFlagName = "ofrep-sse-inactivity-delay"
	ofrepSSEPublicURLFlagName  = "ofrep-sse-public-url"
	otelCollectorURI           = "otel-collector-uri"
	otelCertPathFlagName       = "otel-cert-path"
	otelKeyPathFlagName        = "otel-key-path"
	otelCAPathFlagName         = "otel-ca-path"
	otelReloadIntervalFlagName = "otel-reload-interval"
	portFlagName               = "port"
	serverCertPathFlagName     = "server-cert-path"
	serverKeyPathFlagName      = "server-key-path"
	socketPathFlagName         = "socket-path"
	sourcesFlagName            = "sources"
	syncPortFlagName           = "sync-port"
	syncHTTPFlagName           = "sync-http-enabled"
	syncSocketPathFlagName     = "sync-socket-path"
	uriFlagName                = "uri"
	disableSyncMetadata        = "disable-sync-metadata"
	strictNumericRangeFlagName = "strict-numeric-range" // TODO(#2101): remove
	contextValueFlagName       = "context-value"
	headerToContextKeyFlagName = "context-from-header"
	streamDeadlineFlagName     = "stream-deadline"
	maxRequestBodyFlagName     = "max-request-body"
	maxRequestHeaderFlagName   = "max-request-header"

	keepAliveMinTimeFlagName             = "keep-alive-min-time"
	keepAlivePermitWithoutStreamFlagName = "keep-alive-permit-without-stream"
)

// fromConfig builds the runtime `flagd start` runs; tests replace it to see the settings it is given.
var fromConfig = runtime.FromConfig

func init() {
	flags := startCmd.Flags()
	// allows environment variables to use _ instead of -
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_")) // sync-provider-args becomes SYNC_PROVIDER_ARGS
	viper.SetEnvPrefix("FLAGD")                            // port becomes FLAGD_PORT

	defineStartFlags(flags)
	bindFlags(flags)
}

// defineStartFlags defines the flags of flagd start. LoadConfig defines them on a flag set of its own, so that the
// defaults of a configuration file read there are those of flagd start.
func defineStartFlags(flags *pflag.FlagSet) {
	flags.Int32P(managementPortFlagName, "m", 8014, "Port for management operations")
	flags.Int32P(portFlagName, "p", 8013, "Port to listen on")
	flags.Int32P(syncPortFlagName, "g", 8015, "Flag sync service port, serving gRPC, gRPC-Web and Connect")
	flags.Int32P(ofrepPortFlagName, "r", 8016, "ofrep service port")
	flags.Bool(syncHTTPFlagName, true, "Serve the flag configuration document over HTTP at /v1/flags on the sync port. Defaults to true.")

	flags.Bool(ofrepSSEEnabledFlagName, true, "Enable the OFREP SSE change-notification endpoint (ADR-0008) at /ofrep/v1/sse/{channel} on the ofrep port, where the channel is a selector expression. Defaults to true.")
	flags.Int(ofrepSSEInactivityFlagName, 120, "Inactivity delay (seconds) advertised to OFREP SSE clients in the eventStreams block. Clients close idle connections after this. Defaults to 120.")
	flags.String(ofrepSSEPublicURLFlagName, "", "Origin (scheme://host) advertised as the OFREP SSE eventStreams endpoint.origin. Omitted when empty, so clients resolve the requestUri against the OFREP base URL. Set when flagd is behind a proxy.")
	flags.StringP(socketPathFlagName, "d", "", "Flagd unix socket path. "+
		"With grpc the evaluations service will become available on this address. "+
		"With http(s) the grpc-gateway proxy will use this address internally.")
	flags.StringP(syncSocketPathFlagName, "e", "", "Flagd sync service socket path. "+
		"With grpc the sync service will be available on this address.")
	flags.StringP(serverCertPathFlagName, "c", "", "Server side tls certificate path")
	flags.StringP(serverKeyPathFlagName, "k", "", "Server side tls key path")
	flags.StringSliceP(
		uriFlagName, "f", []string{}, "Set a sync provider uri to read data from, this can be a filepath,"+
			" URL (HTTP and gRPC), FeatureFlag custom resource, or GCS, Azure Blob or S3. "+
			"When flag keys are duplicated across multiple providers the "+
			"merge priority follows the index of the flag arguments, as such flags from the uri at index 0 take the "+
			"lowest precedence, with duplicated keys being overwritten by those from the uri at index 1. "+
			"Please note that if you are using filepath, flagd only supports files with `.yaml/.yml/.json` extension.",
	)
	flags.StringSliceP(corsFlagName, "C", []string{}, "CORS allowed origins, * will allow all origins")
	flags.StringP(
		sourcesFlagName, "s", "", "JSON representation of an array of SourceConfig objects. "+
			"Required fields: uri (string) and provider (string). "+
			"Optional source-specific fields are also available, "+
			"see https://flagd.dev/reference/sync-configuration/#source-configuration",
	)
	flags.StringP(logFormatFlagName, "z", "console", "Set the logging format, e.g. console or json")
	flags.StringP(metricsExporter, "t", "", "Set the metrics exporter. Default(if unset) is Prometheus."+
		" Can be override to otel - OpenTelemetry metric exporter. Overriding to otel require otelCollectorURI to"+
		" be present")
	flags.StringP(otelCollectorURI, "o", "", "Set the grpc URI of the OpenTelemetry collector "+
		"for flagd runtime. If unset, the collector setup will be ignored and traces will not be exported.")
	flags.StringP(otelCertPathFlagName, "D", "", "tls certificate path to use with OpenTelemetry collector")
	flags.StringP(otelKeyPathFlagName, "K", "", "tls key path to use with OpenTelemetry collector")
	flags.StringP(otelCAPathFlagName, "A", "", "tls certificate authority path to use with OpenTelemetry collector")
	flags.DurationP(otelReloadIntervalFlagName, "I", time.Hour, "how long between reloading the otel tls certificate "+
		"from disk")
	flags.StringToStringP(contextValueFlagName, "X", map[string]string{}, "add arbitrary key value pairs "+
		"to the flag evaluation context")
	flags.StringToStringP(headerToContextKeyFlagName, "H", map[string]string{}, "add key-value pairs to map "+
		"header values to context values, where key is Header name, value is context key")
	flags.Duration(streamDeadlineFlagName, 0, "Set a server-side deadline for flagd sync and event streams (default 0, means no deadline).")
	flags.Bool(disableSyncMetadata, false, "Disables the getMetadata endpoint of the sync service. Defaults to false, but will default to true in later versions.")
	flags.Bool(strictNumericRangeFlagName, false, "Rejects numeric flag values outside [-(2^53-1), 2^53-1] with TYPE_MISMATCH. Defaults to false, but will default to true in later versions.")
	flags.Int64P(maxRequestBodyFlagName, "B", 1_000_000, "Maximum allowed request body size in bytes. Requests exceeding this are rejected with HTTP 413 (OFREP) or 429 (connect). Set to 0 to disable. WARNING: disabling this limit may allow memory exhaustion from oversized requests.")
	flags.Int64P(maxRequestHeaderFlagName, "R", 1_000_000, "Maximum allowed request header size in bytes. Requests exceeding this are rejected with HTTP 431. Set to 0 to use Go's built-in default (1 MiB). WARNING: setting a very large or zero value may allow memory exhaustion from oversized headers.")
	flags.Duration(keepAliveMinTimeFlagName, runtime.KeepAliveMinTimeDefault, "No-op, accepted for compatibility. The flag sync server is served by connect over net/http, which has no keepalive enforcement policy to configure and imposes no minimum ping interval.")
	flags.Bool(keepAlivePermitWithoutStreamFlagName, runtime.KeepAlivePermitWithoutStreamDefault, "No-op, accepted for compatibility. The flag sync server is served by connect over net/http, which always permits keepalive pings without an active stream.")
}

func bindFlags(flags *pflag.FlagSet) { bindFlagsTo(viper.GetViper(), flags) }

// bindFlagsTo binds the flags that flagd start reads through viper to the keys of v.
func bindFlagsTo(v *viper.Viper, flags *pflag.FlagSet) {
	_ = v.BindPFlag(corsFlagName, flags.Lookup(corsFlagName))
	_ = v.BindPFlag(logFormatFlagName, flags.Lookup(logFormatFlagName))
	_ = v.BindPFlag(metricsExporter, flags.Lookup(metricsExporter))
	_ = v.BindPFlag(managementPortFlagName, flags.Lookup(managementPortFlagName))
	_ = v.BindPFlag(otelCollectorURI, flags.Lookup(otelCollectorURI))
	_ = v.BindPFlag(otelCertPathFlagName, flags.Lookup(otelCertPathFlagName))
	_ = v.BindPFlag(otelKeyPathFlagName, flags.Lookup(otelKeyPathFlagName))
	_ = v.BindPFlag(otelCAPathFlagName, flags.Lookup(otelCAPathFlagName))
	_ = v.BindPFlag(portFlagName, flags.Lookup(portFlagName))
	_ = v.BindPFlag(serverCertPathFlagName, flags.Lookup(serverCertPathFlagName))
	_ = v.BindPFlag(serverKeyPathFlagName, flags.Lookup(serverKeyPathFlagName))
	_ = v.BindPFlag(socketPathFlagName, flags.Lookup(socketPathFlagName))
	_ = v.BindPFlag(sourcesFlagName, flags.Lookup(sourcesFlagName))
	_ = v.BindPFlag(uriFlagName, flags.Lookup(uriFlagName))
	_ = v.BindPFlag(syncPortFlagName, flags.Lookup(syncPortFlagName))
	_ = v.BindPFlag(syncHTTPFlagName, flags.Lookup(syncHTTPFlagName))
	_ = v.BindPFlag(syncSocketPathFlagName, flags.Lookup(syncSocketPathFlagName))
	_ = v.BindPFlag(ofrepPortFlagName, flags.Lookup(ofrepPortFlagName))
	_ = v.BindPFlag(ofrepSSEEnabledFlagName, flags.Lookup(ofrepSSEEnabledFlagName))
	_ = v.BindPFlag(ofrepSSEInactivityFlagName, flags.Lookup(ofrepSSEInactivityFlagName))
	_ = v.BindPFlag(ofrepSSEPublicURLFlagName, flags.Lookup(ofrepSSEPublicURLFlagName))
	_ = v.BindPFlag(contextValueFlagName, flags.Lookup(contextValueFlagName))
	_ = v.BindPFlag(headerToContextKeyFlagName, flags.Lookup(headerToContextKeyFlagName))
	_ = v.BindPFlag(streamDeadlineFlagName, flags.Lookup(streamDeadlineFlagName))
	_ = v.BindPFlag(disableSyncMetadata, flags.Lookup(disableSyncMetadata))
	_ = v.BindPFlag(strictNumericRangeFlagName, flags.Lookup(strictNumericRangeFlagName))
	_ = v.BindPFlag(maxRequestBodyFlagName, flags.Lookup(maxRequestBodyFlagName))
	_ = v.BindPFlag(maxRequestHeaderFlagName, flags.Lookup(maxRequestHeaderFlagName))
	_ = v.BindPFlag(keepAliveMinTimeFlagName, flags.Lookup(keepAliveMinTimeFlagName))
	_ = v.BindPFlag(keepAlivePermitWithoutStreamFlagName, flags.Lookup(keepAlivePermitWithoutStreamFlagName))
}

// startCmd represents the start command
var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start flagd",
	Long:  ``,
	Run: func(cmd *cobra.Command, args []string) {
		// Configure loggers -------------------------------------------------------
		var level zapcore.Level
		var err error
		if Debug {
			level = zapcore.DebugLevel
		} else {
			level = zapcore.InfoLevel
		}
		l, err := logger.NewZapLogger(level, viper.GetString(logFormatFlagName))
		if err != nil {
			log.Fatalf("can't initialize zap logger: %v", err)
		}
		logger := logger.NewLogger(l, Debug)
		rtLogger := logger.WithFields(zap.String("component", "start"))

		rtLogger.Info("flagd version",
			zap.String("version", Version),
			zap.String("commit", Commit),
			zap.String("date", Date),
			zap.String("variant", fips.Variant),
			zap.Stringer("FIPS 140-3 mode", fips.Current()),
		)
		if err := fips.Check(); err != nil {
			rtLogger.Fatal("refusing to start", zap.Error(err))
		}

		if err := absentSettingsError(Components, explicitSettings(cmd.Flags(), os.LookupEnv, configFileKeys())); err != nil {
			rtLogger.Fatal(err.Error())
		}

		syncProviders, err := syncbuilder.ParseSyncProviderURIs(viper.GetStringSlice(uriFlagName), Components.Sync...)
		if err != nil {
			log.Fatal(unsupportedSourceError(err, viper.GetStringSlice(uriFlagName), Components))
		}

		syncProvidersFromConfig := []sync.SourceConfig{}
		if cfgFile == "" && viper.GetString(sourcesFlagName) != "" {
			syncProvidersFromConfig, err = syncbuilder.ParseSources(viper.GetString(sourcesFlagName))
			if err != nil {
				log.Fatal(err)
			}
		} else {
			err = viper.UnmarshalKey(sourcesFlagName, &syncProvidersFromConfig)
			if err != nil {
				log.Fatal(err)
			}
		}
		syncProviders = append(syncProviders, syncProvidersFromConfig...)

		// Request size limits
		maxRequestBodyBytes := viper.GetInt64(maxRequestBodyFlagName)
		if maxRequestBodyBytes > 0 {
			rtLogger.Info(fmt.Sprintf("request body limit set to %d bytes", maxRequestBodyBytes))
		}
		maxRequestHeaderBytes := viper.GetInt64(maxRequestHeaderFlagName)
		if maxRequestHeaderBytes > 0 {
			rtLogger.Info(fmt.Sprintf("request header limit set to %d bytes", maxRequestHeaderBytes))
		}

		// Build Runtime -----------------------------------------------------------
		extensions, err := loadExtensionSettings(Components, viper.ConfigFileUsed())
		if err != nil {
			rtLogger.Fatal(err.Error())
		}
		components := withTelemetryConfig(extensions.components)
		rt, err := fromConfig(logger, Version, runtimeConfig(viper.GetViper(), syncProviders, extensions), components)
		if err != nil {
			rtLogger.Fatal(err.Error())
		}

		if err := rt.Start(); err != nil {
			rtLogger.Fatal(err.Error())
		}
	},
}

// withTelemetryConfig returns the components with the settings of the telemetry provider that the telemetry flags
// name. The distribution's main chooses the provider.
func withTelemetryConfig(components distribution.Components) distribution.Components {
	return withTelemetryConfigFrom(viper.GetViper(), components)
}

func withTelemetryConfigFrom(v *viper.Viper, components distribution.Components) distribution.Components {
	components.TelemetryConfig = map[string]any{
		telemetryMetricsExporter: v.GetString(metricsExporter),
		telemetryCollectorURI:    v.GetString(otelCollectorURI),
		telemetryCertPath:        v.GetString(otelCertPathFlagName),
		telemetryKeyPath:         v.GetString(otelKeyPathFlagName),
		telemetryCAPath:          v.GetString(otelCAPathFlagName),
		telemetryReloadInterval:  v.GetDuration(otelReloadIntervalFlagName),
	}
	return components
}

// runtimeConfig is the runtime.Config that flagd start builds from the settings v holds, with the sources and the
// extension settings it has read. flagd start reads the global viper through it, LoadConfig a viper of its own.
func runtimeConfig(v *viper.Viper, syncProviders []sync.SourceConfig, extensions extensionSettings) runtime.Config {
	contextValuesToMap := make(map[string]any)
	for k, value := range v.GetStringMapString(contextValueFlagName) {
		contextValuesToMap[k] = value
	}

	headerToContextKeyMappings := make(map[string]string)
	for k, value := range v.GetStringMapString(headerToContextKeyFlagName) {
		headerToContextKeyMappings[k] = value
	}

	return runtime.Config{
		EndpointSettings:       extensions.endpointSettings,
		EventSinksNeedMetrics:  extensions.eventSinks,
		CORS:                   v.GetStringSlice(corsFlagName),
		ManagementPort:         v.GetUint16(managementPortFlagName),
		OfrepServicePort:       v.GetUint16(ofrepPortFlagName),
		OfrepSSEEnabled:        v.GetBool(ofrepSSEEnabledFlagName),
		OfrepSSEInactivityDel:  v.GetInt(ofrepSSEInactivityFlagName),
		OfrepSSEPublicURL:      v.GetString(ofrepSSEPublicURLFlagName),
		ServiceCertPath:        v.GetString(serverCertPathFlagName),
		ServiceKeyPath:         v.GetString(serverKeyPathFlagName),
		ServicePort:            v.GetUint16(portFlagName),
		ServiceSocketPath:      v.GetString(socketPathFlagName),
		SyncServicePort:        v.GetUint16(syncPortFlagName),
		SyncServiceHTTPEnabled: v.GetBool(syncHTTPFlagName),
		SyncServiceSocketPath:  v.GetString(syncSocketPathFlagName),
		StreamDeadline:         v.GetDuration(streamDeadlineFlagName),
		DisableSyncMetadata:    v.GetBool(disableSyncMetadata),
		StrictNumericRange:     v.GetBool(strictNumericRangeFlagName),
		//nolint:staticcheck // SA1019 no-ops, read so FromConfig can warn when they are set
		KeepAliveMinTime: v.GetDuration(keepAliveMinTimeFlagName),
		//nolint:staticcheck // SA1019 see above
		KeepAlivePermitWithoutStream: v.GetBool(keepAlivePermitWithoutStreamFlagName),
		SyncProviders:                syncProviders,
		ContextValues:                contextValuesToMap,
		HeaderToContextKeyMappings:   headerToContextKeyMappings,
		MaxRequestBodyBytes:          v.GetInt64(maxRequestBodyFlagName),
		MaxRequestHeaderBytes:        v.GetInt64(maxRequestHeaderFlagName),
	}
}
