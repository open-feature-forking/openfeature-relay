package synctest_test

import (
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/synctest"
)

// The extension's factories: the Extension Author's own, in their module.
var (
	extensionFetcherFactory   sync.FetcherFactory
	extensionTriggerFactory   sync.TriggerFactory
	extensionStreamingFactory sync.StreamingFactory
)

func ExampleRunFetcher() {
	_ = func(t *testing.T) {
		synctest.RunFetcher(t, extensionFetcherFactory, synctest.FetcherInputs{
			Source: sync.SourceConfig{URI: "example://flags"},
			Change: func(testing.TB) { /* change the flag configuration of the source */ },
			Break:  func(testing.TB) { /* make every later fetch of the source fail */ },
		})
	}
}

func ExampleRunTrigger() {
	_ = func(t *testing.T) {
		synctest.RunTrigger(t, extensionTriggerFactory, synctest.TriggerInputs{
			Signal:    func(testing.TB) { /* make the change signal arrive once */ },
			Interrupt: func(testing.TB) { /* make the trigger lose its connection to the change signal */ },
		})
	}
}

func ExampleRunStreaming() {
	_ = func(t *testing.T) {
		synctest.RunStreaming(t, extensionStreamingFactory, synctest.StreamingInputs{
			Source:      sync.SourceConfig{URI: "example://flags"},
			Expect:      `{"flags":{}}`,
			Change:      func(testing.TB) { /* push a change */ },
			Break:       func(testing.TB) { /* lose the source */ },
			Restore:     func(testing.TB) { /* bring the source back, with a change */ },
			Remove:      func(testing.TB) { /* remove the flag configuration of the source */ },
			OnRemove:    synctest.Keep,
			Unavailable: func(testing.TB) { /* make the source unavailable before the provider starts */ },
			Available:   func(testing.TB) { /* make it available */ },
		})
	}
}
