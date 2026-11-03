package eventsink

import (
	"errors"
	"fmt"
	"strings"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
)

// Factory creates an event sink.
type Factory interface {
	// Type names the event sink.
	Type() string
	// Kinds are the kinds of event the sink subscribes to.
	Kinds() []events.Kind
	// Create builds the sink from settings. An invalid configuration is an error that names the faulty value.
	Create(settings Settings) (Sink, error)
}

// Settings are what a factory gets to build a sink.
type Settings struct {
	Logger    *logger.Logger
	Telemetry telemetry.Provider
	// Config holds the sink's settings, with the core's own keys (see Options) next to the sink's. A sink ignores
	// the keys it does not know.
	Config map[string]any
}

// ValidateDeclaration checks what a factory declares: a non-empty type name and at least one known kind.
func ValidateDeclaration(f Factory) error {
	if f == nil {
		return errors.New("the factory is nil")
	}
	if strings.TrimSpace(f.Type()) == "" {
		return errors.New("the type name is empty")
	}
	kinds := f.Kinds()
	if len(kinds) == 0 {
		return errors.New("the sink subscribes to no event kind")
	}
	for _, k := range kinds {
		switch k {
		case events.KindConfigurationChange, events.KindEvaluation, events.KindTracking:
		default:
			return fmt.Errorf("unknown event kind %q", k)
		}
	}
	return nil
}
