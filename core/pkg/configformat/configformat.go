// Package configformat defines the contract between a flag source and the format of the flag
// configuration it delivers. A configuration format validates a delivery, turns it into documents
// and reports what changed.
package configformat

import (
	"context"
	"fmt"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
)

// DefaultType is the configuration format of sources that name none: flagd's own.
const DefaultType = "flagd"

// ConfigurationFormat accepts the flag configuration of sources.
//
// Accept applies all documents of the delivery to the store, or none: on error the store is
// unchanged. Accept reads the source's previous documents before it applies the new ones, so
// callers serialize Accept per store.
type ConfigurationFormat interface {
	Type() string
	Accept(ctx context.Context, in Input) (Result, error)
}

// Input is one delivery from a source.
type Input struct {
	Source string
	// Data is the flag configuration as JSON text.
	Data              string
	Selector          string
	IncrementalUpdate bool
}

// Result reports what a delivery did.
type Result struct {
	Documents []DocumentReport
	// Dropped lists entries the format skipped without rejecting the delivery.
	Dropped []Rejection
}

// DocumentReport describes the document of one flag set after a delivery. Every key list is
// sorted, and FlagSet is "" for the unnamed flag set.
type DocumentReport struct {
	FlagSet string
	Version string
	// FlagKeys lists the keys the source delivered, including keys another source shadows.
	FlagKeys []string
	Added    []string
	Updated  []string
	Removed  []string
}

// Rejection names the source and the entry that made a format refuse a delivery or drop an entry.
type Rejection struct {
	Source string
	Entry  string
	Reason string
}

func (r Rejection) Error() string {
	return fmt.Sprintf("source %q, entry %q: %s", r.Source, r.Entry, r.Reason)
}

// Factory creates a configuration format.
type Factory interface {
	Type() string
	Create(Settings) (ConfigurationFormat, error)
}

// Settings are what a factory gets to build a format.
type Settings struct {
	Store  *store.Store
	Logger *logger.Logger
	Config map[string]any
}
