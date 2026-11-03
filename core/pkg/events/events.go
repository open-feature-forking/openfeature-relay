// Package events is the event bus through which the runtime and service endpoints announce configuration
// change, evaluation and tracking events to service endpoints and event sinks.
package events

import "time"

// Kind names a kind of event.
type Kind string

const (
	KindConfigurationChange Kind = "configuration_change"
	KindEvaluation          Kind = "evaluation"
	KindTracking            Kind = "tracking"
)

// Origin says where an event came from.
type Origin string

const (
	// OriginService marks an event for a flag a service endpoint of this distribution resolved.
	OriginService Origin = "service"
	// OriginInProcess marks an event a service endpoint relays from an in-process provider.
	OriginInProcess Origin = "in-process"
)

// Event is one of ConfigurationChange, Evaluation and Tracking.
type Event interface {
	EventKind() Kind
}

// ConfigurationChange announces that a configuration format accepted a changed document, or a new version of one.
type ConfigurationChange struct {
	Time time.Time
	// Source, Format, FlagSet and Version identify the document.
	Source, Format, FlagSet, Version string
	// Added, Updated and Removed name the flag keys the document gained, changed and lost.
	Added, Updated, Removed []string
	// Missed is the number of configuration change events dropped for the subscription since the one it
	// received last, plus the number the publisher reports as lost before it published. A subscriber that sees
	// it above zero rereads the store.
	Missed int
}

func (ConfigurationChange) EventKind() Kind { return KindConfigurationChange }

// Evaluation reports one resolved flag.
type Evaluation struct {
	Time                                     time.Time
	FlagKey, FlagSet, Format                 string
	Endpoint                                 string // the type of the service endpoint's factory
	Variant, Reason, ErrorCode, TargetingKey string
	Value                                    any
	HasValue                                 bool // false where the caller's default was answered
	Origin                                   Origin
}

func (Evaluation) EventKind() Kind { return KindEvaluation }

// Tracking reports a tracking event of an in-process provider.
type Tracking struct {
	Time                    time.Time
	EventName, TargetingKey string
	Value                   float64
	Details                 map[string]any
	Origin                  Origin
}

func (Tracking) EventKind() Kind { return KindTracking }
