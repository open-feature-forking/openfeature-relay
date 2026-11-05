package source

import "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"

// Default is the source of the generated components of a testbed distribution: the document its command loads, and the
// one the provider of NewFactories serves.
var Default = New()

// NewFactories returns the factories of the testbed sync component, which serve Default.
func NewFactories() []sync.Factory {
	f := Default.Factory()
	return []sync.Factory{f}
}
