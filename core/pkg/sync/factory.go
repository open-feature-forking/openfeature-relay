package sync

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
)

// Settings is what a distribution injects into the factory of a sync component for one source.
type Settings struct {
	// Source is the source's configuration. Deliveries always carry Source.URI as their source: the runtime keys
	// readiness and the source's configuration format on it.
	Source SourceConfig
	Logger *logger.Logger
	// Metrics records what the component receives; nil records nothing.
	Metrics MetricsRecorder
	// Telemetry is the telemetry provider of the distribution, for what a component records beyond Metrics, such as
	// the lifecycle of a stream, or instruments, such as the dial options of a client connection. Nil records nothing.
	Telemetry telemetry.Provider
	// Config holds the settings of a fetch trigger, from the source's `triggers` entry.
	Config map[string]any
}

// TriggerConfig names a fetch trigger and its settings in the configuration of a source. In JSON it is an object
// whose "type" is the trigger type and whose other members are the settings.
type TriggerConfig struct {
	Type string
	// Config holds the other members of the trigger's entry; a configuration file read with mapstructure collects
	// them there.
	Config map[string]any `mapstructure:",remain"`
}

// UnmarshalJSON reads {"type": "<trigger type>", ...settings}.
func (c *TriggerConfig) UnmarshalJSON(data []byte) error {
	var members map[string]any
	if err := json.Unmarshal(data, &members); err != nil {
		return fmt.Errorf("a fetch trigger must be an object: %w", err)
	}
	triggerType, ok := members["type"].(string)
	if !ok || triggerType == "" {
		return errors.New(`a fetch trigger needs a "type"`)
	}
	delete(members, "type")
	c.Type, c.Config = triggerType, nil
	if len(members) > 0 {
		c.Config = members
	}
	return nil
}

// MarshalJSON writes the form UnmarshalJSON reads.
func (c TriggerConfig) MarshalJSON() ([]byte, error) {
	members := make(map[string]any, len(c.Config)+1)
	for k, v := range c.Config {
		members[k] = v
	}
	members["type"] = c.Type
	return json.Marshal(members)
}

// Factory is what every sync component's factory declares.
type Factory interface {
	// Type is the component's type name: the provider name of a source, or the type of a fetch trigger.
	Type() string
}

// SchemeFactory is the factory of a component that handles sources, identified by the prefix of their URIs.
type SchemeFactory interface {
	Factory
	// Schemes are the non-empty prefixes of the sources the component handles, such as "consul://". A prefix need
	// not be a URI scheme.
	Schemes() []string
	// ParseURI turns a URI given with --uri into the source it names. It reports false for a URI that does not
	// start with one of the factory's schemes. Official factories reproduce flagd's rewriting, such as stripping
	// "file:".
	ParseURI(uri string) (SourceConfig, bool)
}

// FetcherFactory creates the fetcher of one source.
type FetcherFactory interface {
	SchemeFactory
	CreateFetcher(Settings) (Fetcher, error)
}

// DefaultTriggerer is implemented by a FetcherFactory that attaches fetch triggers other than the core's poller to
// its sources when the source configures none.
type DefaultTriggerer interface {
	DefaultTriggers(SourceConfig) []TriggerConfig
}

// TriggerFactory creates a fetch trigger for one source.
type TriggerFactory interface {
	Factory
	CreateTrigger(Settings) (Trigger, error)
}

// StreamingFactory creates the streaming sync provider of one source.
type StreamingFactory interface {
	SchemeFactory
	CreateStreaming(Settings) (StreamingSyncProvider, error)
}

// ValidateDeclarations checks what a factory declares: a non-empty type name and, for a factory that handles
// sources, non-empty schemes. The error names every broken rule.
func ValidateDeclarations(f Factory) error {
	var problems []string
	if strings.TrimSpace(f.Type()) == "" {
		problems = append(problems, "the type name is empty")
	}
	if sf, ok := f.(SchemeFactory); ok {
		schemes := sf.Schemes()
		if len(schemes) == 0 {
			problems = append(problems, "no scheme is declared")
		}
		for i, s := range schemes {
			if s == "" {
				problems = append(problems, fmt.Sprintf("scheme %d is empty", i+1))
			}
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return errors.New(strings.Join(problems, "; "))
}

// SourceTyper is implemented by a factory whose components record the sync metrics under a source type other than
// the factory's Type(), such as the cloud providers, which all record "blob".
type SourceTyper interface {
	SourceType() string
}

// Alias is a provider name that selects a fetcher factory together with the only fetch trigger attached to the
// sources it selects.
type Alias struct {
	Name    string
	Trigger TriggerConfig
}

// Aliased is implemented by a FetcherFactory that is also selected by other provider names, in the order they are
// listed to the Operator.
type Aliased interface {
	Aliases() []Alias
}
