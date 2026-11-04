package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/distribution"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

type schemeStub struct {
	typeName string
	schemes  []string
}

func (s schemeStub) Type() string      { return s.typeName }
func (s schemeStub) Schemes() []string { return s.schemes }
func (s schemeStub) ParseURI(string) (sync.SourceConfig, bool) {
	return sync.SourceConfig{}, false
}

type triggerStub struct{}

func (triggerStub) Type() string { return "poll" }

// withDistribution sets what Execute stamps, and restores it afterwards.
func withDistribution(t *testing.T) {
	t.Helper()
	name, version, comps, inv := Distribution, Version, Components, Inventory
	t.Cleanup(func() { Distribution, Version, Components, Inventory = name, version, comps, inv })
	resolve := resolveInventory
	resolveInventory = func(i distribution.Inventory) distribution.Inventory { return i }
	t.Cleanup(func() { resolveInventory = resolve })
	Distribution, Version = "flagd-test", "v1.2.3"
	Components = distribution.Components{Sync: []sync.Factory{
		schemeStub{"file", []string{"file:"}},
		schemeStub{"http", []string{"http://", "https://"}},
		triggerStub{},
	}}
	Inventory = distribution.Inventory{Components: []distribution.ComponentInfo{
		{Group: distribution.GroupSync, Type: "file", Module: "example.com/sync/file", Version: "v0.1.0"},
		{Group: distribution.GroupSync, Type: "http", Module: "example.com/sync/http", Version: "v0.1.0"},
		{Group: distribution.GroupSync, Type: "poll", Module: "example.com/sync/http", Version: "v0.1.0"},
		{Group: distribution.GroupConfigurationFormat, Type: "flagd", Module: "example.com/format", Version: "dev"},
		{Group: distribution.GroupEvaluator, Type: "flagd", Module: "example.com/evaluator"},
		{Group: distribution.GroupServiceEndpoint, Type: "ofrep", Module: "example.com/ofrep", Version: "v2.0.0"},
		{Group: distribution.GroupTelemetry, Type: "otel", Module: "example.com/otel", Version: "v1.0.0"},
	}}
}

// runComponents runs the command's handler with its output captured, without executing the root command, which would
// add completion and help to it.
func runComponents(t *testing.T, args ...string) string {
	t.Helper()
	out, err := runComponentsErr(t, args...)
	require.NoError(t, err)
	return out
}

func runComponentsErr(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	componentsCmd.SetOut(&out)
	t.Cleanup(func() {
		componentsCmd.SetOut(nil)
		require.NoError(t, componentsCmd.Flags().Set("output", "text"))
	})
	require.NoError(t, componentsCmd.ParseFlags(args))
	err := componentsCmd.RunE(componentsCmd, componentsCmd.Flags().Args())
	return out.String(), err
}

// TestComponentsListsInventory: the listing names the distribution, groups components in a fixed order and shows the
// schemes of sync providers only (UC-011 steps 2-5).
func TestComponentsListsInventory(t *testing.T) {
	withDistribution(t)
	got := runComponents(t)
	require.Equal(t, `distribution: flagd-test v1.2.3
runtime: dev

sync providers:
  file  example.com/sync/file v0.1.0  schemes: file:
  http  example.com/sync/http v0.1.0  schemes: http://, https://
  poll  example.com/sync/http v0.1.0

configuration formats:
  flagd  example.com/format dev

evaluators:
  flagd  example.com/evaluator

service endpoints:
  ofrep  example.com/ofrep v2.0.0

telemetry providers:
  otel  example.com/otel v1.0.0

middleware:
  none

event sinks:
  none
`, got)
}

// TestComponentsJSONSameContentAsText: --output json is the same listing as one document (UC-011 A3).
func TestComponentsJSONSameContentAsText(t *testing.T) {
	withDistribution(t)
	text := runComponents(t)
	raw := runComponents(t, "--output", "json")

	var l listing
	require.NoError(t, json.Unmarshal([]byte(raw), &l))
	var again bytes.Buffer
	l.text(&again)
	require.Equal(t, text, again.String())

	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &doc))
	components := doc["components"].(map[string]any)
	for _, g := range groupOrder {
		require.Contains(t, components, string(g.group))
	}
	require.Equal(t, []any{}, components["middleware"], "an empty group is an empty list")
	require.Contains(t, raw, `"schemes": [`)
}

func TestComponentsRejectsUnknownOutput(t *testing.T) {
	withDistribution(t)
	_, err := runComponentsErr(t, "--output", "yaml")
	require.ErrorContains(t, err, `unknown output format "yaml"`)
}

// TestComponentsIdentityMatchesVersionVerbose: the first two lines are the lines flagd version --verbose ends with.
func TestComponentsIdentityMatchesVersionVerbose(t *testing.T) {
	withDistribution(t)
	t.Cleanup(func() { require.NoError(t, versionCmd.Flags().Set("verbose", "false")) })
	require.NoError(t, versionCmd.Flags().Set("verbose", "true"))
	verbose := captureStdout(t, func() { versionCmd.Run(versionCmd, nil) })
	lines := strings.Split(strings.TrimRight(verbose, "\n"), "\n")
	identity := strings.Join(lines[len(lines)-2:], "\n") + "\n"

	require.True(t, strings.HasPrefix(runComponents(t), identity), "the listing starts with %q", identity)
}

// TestComponentsOpensNoPortAndReadsNoConfig: the command reads only the components and the inventory, so a
// configuration that would stop flagd from starting changes nothing.
func TestComponentsOpensNoPortAndReadsNoConfig(t *testing.T) {
	withDistribution(t)
	want := runComponents(t)

	viper.Set(managementPortFlagName, "not-a-port")
	viper.Set(sourcesFlagName, "unknown://x")
	t.Cleanup(func() {
		viper.Set(managementPortFlagName, nil)
		viper.Set(sourcesFlagName, nil)
	})
	require.Equal(t, want, runComponents(t))
}
