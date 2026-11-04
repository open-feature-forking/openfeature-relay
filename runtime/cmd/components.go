package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"runtime/debug"
	"strings"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/distribution"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/spf13/cobra"
)

// groupOrder is the order of the sections of the listing, and the title of each.
var groupOrder = []struct {
	group distribution.Group
	title string
}{
	{distribution.GroupSync, "sync providers"},
	{distribution.GroupConfigurationFormat, "configuration formats"},
	{distribution.GroupEvaluator, "evaluators"},
	{distribution.GroupServiceEndpoint, "service endpoints"},
	{distribution.GroupTelemetry, "telemetry providers"},
	{distribution.GroupMiddleware, "middleware"},
	{distribution.GroupEventSink, "event sinks"},
}

// identity is what `flagd version --verbose` and `flagd components` print first.
type identity struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// distributionIdentity is the distribution's name and manifest version.
func distributionIdentity() identity { return identity{Name: Distribution, Version: Version} }

// identityLines are the two lines that name the distribution and the runtime.
func identityLines(id identity, info *debug.BuildInfo) string {
	return fmt.Sprintf("distribution: %s %s\nruntime: %s\n", id.Name, id.Version, runtimeVersion(info))
}

// listedComponent is one component of the listing.
type listedComponent struct {
	Type    string   `json:"type"`
	Module  string   `json:"module"`
	Version string   `json:"version"`
	Schemes []string `json:"schemes,omitempty"`
}

// listing is the content of `flagd components`, in the layout of its JSON form.
type listing struct {
	Distribution identity `json:"distribution"`
	Runtime      struct {
		Version string `json:"version"`
	} `json:"runtime"`
	Components struct {
		SyncProviders        []listedComponent `json:"sync_providers"`
		ConfigurationFormats []listedComponent `json:"configuration_formats"`
		Evaluators           []listedComponent `json:"evaluators"`
		ServiceEndpoints     []listedComponent `json:"service_endpoints"`
		Telemetry            []listedComponent `json:"telemetry"`
		Middleware           []listedComponent `json:"middleware"`
		EventSinks           []listedComponent `json:"event_sinks"`
	} `json:"components"`
}

// section returns the components of one group.
func (l *listing) section(g distribution.Group) *[]listedComponent {
	switch g {
	case distribution.GroupSync:
		return &l.Components.SyncProviders
	case distribution.GroupConfigurationFormat:
		return &l.Components.ConfigurationFormats
	case distribution.GroupEvaluator:
		return &l.Components.Evaluators
	case distribution.GroupServiceEndpoint:
		return &l.Components.ServiceEndpoints
	case distribution.GroupTelemetry:
		return &l.Components.Telemetry
	case distribution.GroupMiddleware:
		return &l.Components.Middleware
	default:
		return &l.Components.EventSinks
	}
}

// buildListing reads only what the distribution carries: the factories for the schemes, and the resolved inventory.
func buildListing(id identity, runtimeVer string, components distribution.Components, inventory distribution.Inventory) *listing {
	schemes := map[string][]string{}
	for _, f := range components.Sync {
		if sf, ok := f.(sync.SchemeFactory); ok {
			schemes[f.Type()] = sf.Schemes()
		}
	}
	l := &listing{Distribution: id}
	l.Runtime.Version = runtimeVer
	for _, g := range groupOrder {
		*l.section(g.group) = []listedComponent{}
	}
	for _, c := range inventory.Components {
		entry := listedComponent{Type: c.Type, Module: c.Module, Version: c.Version}
		if c.Group == distribution.GroupSync {
			entry.Schemes = schemes[c.Type]
		}
		s := l.section(c.Group)
		*s = append(*s, entry)
	}
	return l
}

// text writes the listing as sections, one line per component.
func (l *listing) text(w io.Writer) {
	fmt.Fprintf(w, "distribution: %s %s\nruntime: %s\n", l.Distribution.Name, l.Distribution.Version, l.Runtime.Version)
	for _, g := range groupOrder {
		fmt.Fprintf(w, "\n%s:\n", g.title)
		entries := *l.section(g.group)
		if len(entries) == 0 {
			fmt.Fprintln(w, "  none")
		}
		for _, e := range entries {
			line := "  " + e.Type + "  " + e.Module
			if e.Version != "" {
				line += " " + e.Version
			}
			if len(e.Schemes) > 0 {
				line += "  schemes: " + strings.Join(e.Schemes, ", ")
			}
			fmt.Fprintln(w, line)
		}
	}
}

// invokesComponents reports whether the arguments run the components command, which reads no configuration file and
// needs no home directory.
func invokesComponents(args []string) bool {
	c, _, err := rootCmd.Find(args)
	return err == nil && c == componentsCmd
}

// resolveInventory fills in the versions of the running binary; tests replace it.
var resolveInventory = func(i distribution.Inventory) distribution.Inventory { return i.Resolved() }

// componentsCmd lists the components compiled into the binary. It is hidden so that the help of every distribution
// stays as it was; the startup errors and the docs name it.
var componentsCmd = &cobra.Command{
	Use:    "components",
	Short:  "List the components compiled into this distribution",
	Args:   cobra.NoArgs,
	Hidden: true,
	// The root command's banner goes to stdout, where the listing must stand alone for tooling to read.
	PersistentPreRun: func(*cobra.Command, []string) {},
	RunE: func(cmd *cobra.Command, args []string) error {
		format, _ := cmd.Flags().GetString("output")
		if format != "text" && format != "json" {
			return fmt.Errorf("unknown output format %q, must be text or json", format)
		}
		details, _ := debug.ReadBuildInfo()
		l := buildListing(distributionIdentity(), runtimeVersion(details), Components, resolveInventory(Inventory))
		if format == "json" {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(l)
		}
		l.text(cmd.OutOrStdout())
		return nil
	},
}

func init() {
	componentsCmd.Flags().String("output", "text", "output format: text or json")
}
