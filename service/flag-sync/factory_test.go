package sync

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/stretchr/testify/require"
)

// TestLastModifiedFollowsConfigurationChangeEvents pins that the endpoint dates its flags by the configuration
// change events of the runtime: a delivery that changes nothing publishes no event, and Last-Modified stays.
func TestLastModifiedFollowsConfigurationChangeEvents(t *testing.T) {
	const port = 18083

	flagStore, sources := getSimpleFlagStore(t)
	bus := events.NewBus()
	initialized := make(chan struct{})

	endpoint, err := Factory{}.Create(service.Settings{
		Store: flagStore.(*store.Store), Bus: bus, Initialized: initialized, Logger: logger.NewLogger(nil, false),
		Config: map[string]any{ConfigKey: SvcConfigurations{Port: port, HTTPEnabled: true, Sources: sources}},
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- endpoint.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-done)
	})

	close(initialized) // every source delivered: the endpoint serves, and the first delivery dates the flags
	url := fmt.Sprintf("http://localhost:%d%s", port, flagsPath)
	lastModified := func() string {
		resp := getWithRetry(t, flagsClient(t, ""), url)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		return resp.Header.Get("Last-Modified")
	}

	first := lastModified()
	require.NotEmpty(t, first)

	time.Sleep(1100 * time.Millisecond) // Last-Modified has a resolution of one second
	require.Equal(t, first, lastModified(), "no event, no change of the date")

	events.PublishConfigurationChange(bus, events.ConfigurationChange{Source: testSource1, Format: "flagd", Version: "v2", Updated: []string{"flagA"}})
	require.Eventually(t, func() bool { return lastModified() != first }, 3*time.Second, 100*time.Millisecond,
		"a configuration change event dates the flags")
}
