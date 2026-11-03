package eventsinktest_test

import (
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/eventsink"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/eventsink/eventsinktest"
)

// extensionSinkFactory is the Extension Author's own event sink factory.
var extensionSinkFactory eventsink.Factory

func ExampleRun() {
	_ = func(t *testing.T) {
		eventsinktest.Run(t, extensionSinkFactory, eventsinktest.Inputs{
			Config:        map[string]any{"endpoint": "http://collector.example"},
			InvalidConfig: map[string]any{"endpoint": 42},
			FaultyValue:   "endpoint",
			// the batches the external system received, in order
			Received: func(testing.TB) [][]events.Event { return nil },
			// make the external system stop answering until release is called
			Stall: func(testing.TB) (release func()) { return func() {} },
			// make the external system refuse every batch: for now, or for good when permanent
			Refuse: func(_ testing.TB, permanent bool) (restore func()) { return func() {} },
		})
	}
}
