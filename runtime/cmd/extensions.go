package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/distribution"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/eventsink"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware"
	"github.com/spf13/viper"
	"go.yaml.in/yaml/v3"
)

// The keys of the configuration file with which an operator configures the components a distribution contains besides
// flagd-full's official ones (UC-021 steps 2 to 5). They are file keys only, with no flag: flagd's help is unchanged.
const (
	eventSinksKey       = "event-sinks"
	middlewareKey       = "middleware"
	endpointSettingsKey = "endpoint-settings"

	// entryTypeKey names the component type in a list entry; the other keys of the entry are the component's settings.
	entryTypeKey = "type"
)

var extensionKeys = []string{eventSinksKey, middlewareKey, endpointSettingsKey}

// officialEndpoints are the endpoints whose settings are flagd's flags.
var officialEndpoints = map[string]bool{componentFlagEvaluation: true, componentFlagSync: true, componentOFREP: true}

// extensionSettings are what the operator's configuration file says about the components of the distribution.
type extensionSettings struct {
	components distribution.Components
	// endpointSettings are the settings of the service endpoints, keyed by the endpoint's type.
	endpointSettings map[string]map[string]any
	// eventSinks is true when the file configures event sinks.
	eventSinks bool
}

// loadExtensionSettings reads the event-sinks, middleware and endpoint-settings keys of the configuration file and
// returns the components with the configured event sinks and middleware added, and the settings of the endpoints. The
// file is read as it was written, because viper lower-cases every key and the settings of a component are camelCase
// (bufferSize) or header names. path is the configuration file in use, or empty. With no such key it returns the
// components unchanged.
func loadExtensionSettings(c distribution.Components, path string) (extensionSettings, error) {
	result := extensionSettings{components: c}
	values, err := readExtensionValues(path)
	if err != nil {
		return result, err
	}

	if raw, ok := values[eventSinksKey]; ok {
		entries, err := parseEntries(eventSinksKey, raw)
		if err != nil {
			return result, err
		}
		factories := map[string]eventsink.Factory{}
		for _, f := range c.EventSinkFactories {
			factories[f.Type()] = f
		}
		result.components.EventSinks = append([]eventsink.Configured(nil), c.EventSinks...)
		for i, e := range entries {
			f, ok := factories[e.typ]
			if !ok {
				return result, unknownType(eventSinksKey, i, e.typ, keysOf(factories))
			}
			result.components.EventSinks = append(result.components.EventSinks, eventsink.Configured{Factory: f, Config: e.config})
		}
		result.eventSinks = len(entries) > 0
	}

	if raw, ok := values[middlewareKey]; ok {
		entries, err := parseEntries(middlewareKey, raw)
		if err != nil {
			return result, err
		}
		factories := map[string]middleware.Factory{}
		for _, f := range c.MiddlewareFactories {
			factories[f.Type()] = f
		}
		result.components.Middleware = append([]middleware.Configured(nil), c.Middleware...)
		for i, e := range entries {
			f, ok := factories[e.typ]
			if !ok {
				return result, unknownType(middlewareKey, i, e.typ, keysOf(factories))
			}
			result.components.Middleware = append(result.components.Middleware, middleware.Configured{Factory: f, Config: e.config})
		}
	}

	if raw, ok := values[endpointSettingsKey]; ok {
		settings, err := parseEndpointSettings(raw, c)
		if err != nil {
			return result, err
		}
		result.endpointSettings = settings
	}
	return result, nil
}

// entry is one element of the event-sinks or middleware list.
type entry struct {
	typ    string
	config map[string]any
}

func parseEntries(key string, raw any) ([]entry, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: expected a list of entries, each with a type, observed %T", key, raw)
	}
	entries := make([]entry, 0, len(list))
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s[%d]: expected an entry with a %q, observed %T", key, i, entryTypeKey, item)
		}
		typ, ok := m[entryTypeKey].(string)
		if !ok || strings.TrimSpace(typ) == "" {
			return nil, fmt.Errorf("%s[%d]: the entry needs a %q naming the component", key, i, entryTypeKey)
		}
		config := make(map[string]any, len(m))
		for k, v := range m {
			if k != entryTypeKey {
				config[k] = v
			}
		}
		entries = append(entries, entry{typ: typ, config: config})
	}
	return entries, nil
}

func parseEndpointSettings(raw any, c distribution.Components) (map[string]map[string]any, error) {
	if raw == nil {
		return nil, nil
	}
	byType, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: expected the settings of each endpoint, keyed by its type, observed %T", endpointSettingsKey, raw)
	}
	present := map[string]bool{}
	for _, e := range c.Endpoints {
		present[e.Type()] = true
	}
	settings := make(map[string]map[string]any, len(byType))
	for typ, v := range byType {
		if officialEndpoints[typ] {
			return nil, fmt.Errorf("%s.%s: the settings of the %s endpoint are the flags of flagd start", endpointSettingsKey, typ, typ)
		}
		if !present[typ] {
			return nil, withComponentsHint(fmt.Errorf("%s.%s: the distribution %s has no service endpoint of this type; it has: %s",
				endpointSettingsKey, typ, Distribution, listOrNone(keysOf(present))))
		}
		m, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s.%s: expected the endpoint's settings, observed %T", endpointSettingsKey, typ, v)
		}
		settings[typ] = m
	}
	return settings, nil
}

func unknownType(key string, i int, typ string, available []string) error {
	return withComponentsHint(fmt.Errorf("%s[%d]: the distribution %s has no component of type %q; it has: %s",
		key, i, Distribution, typ, listOrNone(available)))
}

func keysOf[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func listOrNone(list []string) string {
	if len(list) == 0 {
		return "none"
	}
	return strings.Join(list, ", ")
}

// readExtensionValues returns the values of the extension keys as the configuration file holds them. No file, or
// none of the keys, gives none. A file that is neither YAML nor JSON but holds one of the keys is an error: its keys
// would reach the components in lower case.
func readExtensionValues(path string) (map[string]any, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil // start has used what viper could read of it
	}
	var file map[string]any
	if err := yaml.Unmarshal(data, &file); err != nil {
		file := viper.New()
		file.SetConfigFile(path)
		if file.ReadInConfig() != nil {
			return nil, nil
		}
		for _, key := range file.AllKeys() {
			name, _, _ := strings.Cut(key, ".")
			for _, extension := range extensionKeys {
				if name == extension {
					return nil, fmt.Errorf("the configuration file %s holds %s but is neither YAML nor JSON, so the case of its keys cannot be kept", path, extension)
				}
			}
		}
		return nil, nil
	}
	values := map[string]any{}
	for _, key := range extensionKeys {
		if v, ok := file[key]; ok {
			values[key] = v
		}
	}
	return values, nil
}
