package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/distribution"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/runtime"
	syncbuilder "github.com/open-feature-forking/openfeature-relay/sync/builder"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// SourceRewrite changes a source of a configuration that LoadConfig reads, and returns the source it is given when it
// does not apply to it. A program that embeds the runtime uses it to serve a source its own way.
type SourceRewrite func(sync.SourceConfig) sync.SourceConfig

// LoadConfig reads a configuration file as flagd start does, for a program that builds runtimes itself: the
// runtime.Config and the components to hand to runtime.FromConfig. The settings are those of flagd start, with its
// defaults, its FLAGD_ environment variables and its checks, so a configuration file means what it means to flagd. It
// uses a viper and a flag set of its own and leaves the state of the command alone, so it can be called again and again.
// Each rewrite is applied to every source, in order.
func LoadConfig(path string, components distribution.Components, rewrites []SourceRewrite) (runtime.Config, distribution.Components, error) {
	flags := pflag.NewFlagSet("start", pflag.ContinueOnError)
	defineStartFlags(flags)
	v := viper.New()
	bindFlagsTo(v, flags)
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.SetEnvPrefix("FLAGD")
	v.AutomaticEnv()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return runtime.Config{}, components, fmt.Errorf("reading the configuration file %s: %w", path, err)
	}

	if err := absentSettingsError(components, explicitSettings(flags, os.LookupEnv, fileKeys(path))); err != nil {
		return runtime.Config{}, components, err
	}

	uris := v.GetStringSlice(uriFlagName)
	sources, err := syncbuilder.ParseSyncProviderURIs(uris, components.Sync...)
	if err != nil {
		return runtime.Config{}, components, unsupportedSourceError(err, uris, components)
	}
	var fromFile []sync.SourceConfig
	if err := v.UnmarshalKey(sourcesFlagName, &fromFile); err != nil {
		return runtime.Config{}, components, fmt.Errorf("reading the sources of %s: %w", path, err)
	}
	if err := decodeGRPCSettings(v.Get(sourcesFlagName), fromFile); err != nil {
		return runtime.Config{}, components, fmt.Errorf("reading the sources of %s: %w", path, err)
	}
	sources = append(sources, fromFile...)
	for i := range sources {
		for _, rewrite := range rewrites {
			sources[i] = rewrite(sources[i])
		}
	}

	extensions, err := loadExtensionSettings(components, path)
	if err != nil {
		return runtime.Config{}, components, err
	}
	cfg, err := runtimeConfig(v, sources, extensions)
	if err != nil {
		return runtime.Config{}, components, err
	}
	return cfg, withTelemetryConfigFrom(v, extensions.components), nil
}

// fileKeys returns the keys the configuration file at path holds, read on their own: the merged view of viper cannot
// tell a key of the file from a default.
func fileKeys(path string) []string {
	file := viper.New()
	file.SetConfigFile(path)
	if err := file.ReadInConfig(); err != nil {
		return nil
	}
	return file.AllKeys()
}
