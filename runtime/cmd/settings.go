package cmd

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/distribution"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// The component types a setting of flagd-full can belong to: the service endpoints, by their type, and the telemetry
// provider. A setting that belongs to no component is the runtime's own.
const (
	componentFlagEvaluation = "flag-evaluation"
	componentOFREP          = "ofrep"
	componentFlagSync       = "flag-sync"
	componentTelemetry      = "telemetry"
)

var allEndpoints = []string{componentFlagEvaluation, componentFlagSync, componentOFREP}

// settingComponents maps each flag of flagd start, which is also the key of the configuration file and, upper-cased
// with FLAGD_ in front, the name of the environment variable, to the components it configures (UC-010 BR-001). A
// setting whose components are all missing from the distribution stops startup when it is set explicitly. The runtime's
// own settings, which have an empty set, never do. A new flag has no entry here and fails
// TestEveryFlagdFullSettingHasAComponent.
var settingComponents = map[string][]string{
	"port":                             {componentFlagEvaluation},
	"socket-path":                      {componentFlagEvaluation},
	"sync-port":                        {componentFlagSync},
	"sync-socket-path":                 {componentFlagSync},
	"sync-http-enabled":                {componentFlagSync},
	"disable-sync-metadata":            {componentFlagSync},
	"ofrep-port":                       {componentOFREP},
	"ofrep-sse-enabled":                {componentOFREP},
	"ofrep-sse-inactivity-delay":       {componentOFREP},
	"ofrep-sse-public-url":             {componentOFREP},
	"server-cert-path":                 {componentFlagEvaluation, componentFlagSync},
	"server-key-path":                  {componentFlagEvaluation, componentFlagSync},
	"stream-deadline":                  {componentFlagEvaluation, componentFlagSync},
	"context-from-header":              {componentFlagEvaluation, componentOFREP},
	"cors-origin":                      allEndpoints,
	"context-value":                    allEndpoints,
	"max-request-body":                 allEndpoints,
	"max-request-header":               allEndpoints,
	"metrics-exporter":                 {componentTelemetry},
	"otel-collector-uri":               {componentTelemetry},
	"otel-cert-path":                   {componentTelemetry},
	"otel-key-path":                    {componentTelemetry},
	"otel-ca-path":                     {componentTelemetry},
	"otel-reload-interval":             {componentTelemetry},
	"management-port":                  nil,
	"strict-numeric-range":             nil,
	"keep-alive-min-time":              nil,
	"keep-alive-permit-without-stream": nil,
	"uri":                              nil,
	"sources":                          nil,
	"log-format":                       nil,
	"debug":                            nil,
	"config":                           nil,
}

// presentComponents returns the component types of the distribution that settings can belong to.
func presentComponents(c distribution.Components) map[string]bool {
	present := map[string]bool{}
	for _, endpoint := range c.Endpoints {
		present[endpoint.Type()] = true
	}
	present[componentTelemetry] = c.Telemetry != nil && c.Telemetry.Type() != telemetry.NoopType
	return present
}

// explicitSetting is a setting that was set on purpose, and where.
type explicitSetting struct {
	name, source string
}

// explicitSettings returns the settings that are set explicitly. A setting is explicit by the source it was set in: a
// flag that was given on the command line, an environment variable that exists, a key of the configuration file
// itself. The defaults, which the merged configuration also holds, are not, and neither is a value another source
// overrides. A key at its default value still counts.
func explicitSettings(flags *pflag.FlagSet, lookupEnv func(string) (string, bool), fileKeys []string) []explicitSetting {
	var set []explicitSetting
	for name := range settingComponents {
		if flags.Changed(name) {
			set = append(set, explicitSetting{name, "--" + name})
		}
		if env := settingEnvName(name); lookupEnvPresent(lookupEnv, env) {
			set = append(set, explicitSetting{name, env})
		}
	}
	for _, key := range fileKeys {
		name, _, _ := strings.Cut(key, ".")
		if _, known := settingComponents[name]; known {
			set = append(set, explicitSetting{name, name + " in the configuration file"})
		}
	}
	return set
}

func lookupEnvPresent(lookupEnv func(string) (string, bool), name string) bool {
	_, ok := lookupEnv(name)
	return ok
}

func settingEnvName(setting string) string {
	return "FLAGD_" + strings.ToUpper(strings.ReplaceAll(setting, "-", "_"))
}

// configFileKeys returns the keys the configuration file holds, read once on their own: viper's merged view cannot tell
// a key of the file from a default. A file that cannot be read has none; start ignores it as it always has.
func configFileKeys() []string {
	file := viper.New()
	if cfgFile != "" {
		file.SetConfigFile(cfgFile)
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		file.AddConfigPath(home)
		file.SetConfigType("yaml")
		file.SetConfigName(".agent")
	}
	if err := file.ReadInConfig(); err != nil {
		return nil
	}
	return file.AllKeys()
}

// componentsHint is the pointer the startup errors that name a missing component end with, when the distribution's
// manifest asks for it.
const componentsHint = `; run "flagd components" to list the components of this distribution`

// withComponentsHint ends the error with the pointer to the components command, if the distribution has it on.
func withComponentsHint(err error) error {
	if !ComponentsHint {
		return err
	}
	return fmt.Errorf("%w%s", err, componentsHint)
}

// absentSettingsError returns the error that names every explicit setting none of whose components the distribution
// contains, with the components it belongs to; nil when there is none.
func absentSettingsError(c distribution.Components, set []explicitSetting) error {
	present := presentComponents(c)
	var lines []string
	for _, s := range set {
		components := settingComponents[s.name]
		if len(components) == 0 {
			continue
		}
		contained := false
		for _, component := range components {
			contained = contained || present[component]
		}
		if !contained {
			sorted := append([]string(nil), components...)
			sort.Strings(sorted)
			lines = append(lines, fmt.Sprintf("%s (%s)", s.source, strings.Join(sorted, ", ")))
		}
	}
	if len(lines) == 0 {
		return nil
	}
	sort.Strings(lines)
	return withComponentsHint(errors.New("the distribution " + Distribution + " does not contain the components of these settings: " + strings.Join(lines, ", ")))
}
