package distribution

import "runtime/debug"

// Group is the component type a component belongs to.
type Group string

const (
	GroupSync                 Group = "sync_providers"
	GroupConfigurationFormat  Group = "configuration_formats"
	GroupEvaluator            Group = "evaluators"
	GroupServiceEndpoint      Group = "service_endpoints"
	GroupTelemetry            Group = "telemetry"
	GroupMiddleware           Group = "middleware"
	GroupEventSink            Group = "event_sinks"
)

// ComponentInfo describes one component a distribution contains.
type ComponentInfo struct {
	Group Group
	// Type is the name the component's factory gives.
	Type string
	// Module is the path of the Go module that holds the component.
	Module string
	// Version is the version of the module that is linked: the replacement's version when the module is replaced,
	// and "dev" when it is replaced by a local path. It is empty until the inventory is resolved, and when the module
	// is not in the build information, as in a build from the module's own source.
	Version string
	// Replacement is the path the module is replaced by, or empty.
	Replacement string
}

// Inventory lists the components of a distribution. A distribution declares group, type and module; the versions
// are filled in at run time from the build information, so that a replaced module shows what was actually linked.
type Inventory struct {
	Components []ComponentInfo
}

// Resolved returns the inventory with the versions of the running binary's build information.
func (i Inventory) Resolved() Inventory {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return i
	}
	return i.ResolvedFrom(info)
}

// ResolvedFrom returns the inventory with the versions the build information names. The inventory itself is not
// changed.
func (i Inventory) ResolvedFrom(info *debug.BuildInfo) Inventory {
	linked := make(map[string]*debug.Module, len(info.Deps))
	for _, dep := range info.Deps {
		linked[dep.Path] = dep
	}
	if info.Main.Path != "" {
		linked[info.Main.Path] = &info.Main
	}

	out := Inventory{Components: make([]ComponentInfo, len(i.Components))}
	for n, c := range i.Components {
		if dep, ok := linked[c.Module]; ok {
			c.Version = dep.Version
			if dep.Replace != nil {
				c.Replacement = dep.Replace.Path
				c.Version = dep.Replace.Version
				if c.Version == "" {
					c.Version = "dev"
				}
			}
		} else {
			c.Version = ""
		}
		out.Components[n] = c
	}
	return out
}
