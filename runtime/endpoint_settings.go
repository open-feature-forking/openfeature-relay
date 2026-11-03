package runtime

import "maps"

// The types of the endpoints of flagd-full, which Config's flags are mapped to. The runtime holds their names, not
// their packages.
const (
	flagEvaluationType = "flag-evaluation"
	ofrepType          = "ofrep"
	flagSyncType       = "flag-sync"
)

// officialEndpointSettings maps the flags of flagd-full that Config carries to the plain-data settings of the
// endpoint each belongs to. The keys are the ones the endpoint's factory documents; a factory ignores nothing it
// does not know, so a distribution without an endpoint just never reads its entry.
func officialEndpointSettings(c Config) map[string]map[string]any {
	return map[string]map[string]any{
		flagEvaluationType: {
			"port":                           c.ServicePort,
			"management-port":                c.ManagementPort,
			"service-name":                   svcName,
			"key-path":                       c.ServiceKeyPath,
			"cert-path":                      c.ServiceCertPath,
			"socket-path":                    c.ServiceSocketPath,
			"cors":                           c.CORS,
			"context-values":                 c.ContextValues,
			"header-to-context-key-mappings": c.HeaderToContextKeyMappings,
			"stream-deadline":                c.StreamDeadline,
			"max-request-body-bytes":         c.MaxRequestBodyBytes,
			"max-request-header-bytes":       c.MaxRequestHeaderBytes,
		},
		ofrepType: {
			"port":                           c.OfrepServicePort,
			"service-name":                   svcName,
			"max-request-body-bytes":         c.MaxRequestBodyBytes,
			"max-request-header-bytes":       c.MaxRequestHeaderBytes,
			"sse-enabled":                    c.OfrepSSEEnabled,
			"sse-inactivity-delay":           c.OfrepSSEInactivityDel,
			"sse-public-url":                 c.OfrepSSEPublicURL,
			"cors":                           c.CORS,
			"context-values":                 c.ContextValues,
			"header-to-context-key-mappings": c.HeaderToContextKeyMappings,
		},
		flagSyncType: {
			"port":                     c.SyncServicePort,
			"context-values":           c.ContextValues,
			"key-path":                 c.ServiceKeyPath,
			"cert-path":                c.ServiceCertPath,
			"socket-path":              c.SyncServiceSocketPath,
			"http-enabled":             c.SyncServiceHTTPEnabled,
			"service-name":             svcName,
			"cors":                     c.CORS,
			"stream-deadline":          c.StreamDeadline,
			"disable-sync-metadata":    c.DisableSyncMetadata,
			"max-request-header-bytes": c.MaxRequestHeaderBytes,
			"max-request-body-bytes":   c.MaxRequestBodyBytes,
		},
	}
}

// endpointSettings returns Settings.Config of the endpoint type: the official endpoints' settings from Config's
// flags, then the entries of Config.EndpointSettings, which win.
func endpointSettings(c Config, endpointType string) map[string]any {
	settings := maps.Clone(officialEndpointSettings(c)[endpointType])
	if settings == nil {
		settings = map[string]any{}
	}
	maps.Copy(settings, c.EndpointSettings[endpointType])
	return settings
}
