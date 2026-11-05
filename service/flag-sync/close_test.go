package sync

import (
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/events"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/stretchr/testify/require"
)

// countingListen opens listeners as net.Listen does and counts those still open.
type countingListen struct{ open int }

type countedListener struct {
	net.Listener
	owner  *countingListen
	closed bool
}

func (c *countedListener) Close() error {
	if !c.closed {
		c.closed = true
		c.owner.open--
	}
	return c.Listener.Close()
}

func (c *countingListen) listen(network, address string) (net.Listener, error) {
	l, err := net.Listen(network, address)
	if err != nil {
		return nil, err
	}
	c.open++
	return &countedListener{Listener: l, owner: c}, nil
}

// The endpoint binds its listener when it is created. One that is never served, because the distribution failed to
// build, is closed instead, and that releases the listener.
func TestCloseReleasesTheListenerOfAnEndpointThatIsNeverServed(t *testing.T) {
	free, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := free.Addr().(*net.TCPAddr).Port
	require.NoError(t, free.Close())

	flagStore, sources := getSimpleFlagStore(t)
	listen := &countingListen{}
	endpoint, err := Factory{}.Create(service.Settings{
		Store: flagStore.(*store.Store), Bus: events.NewBus(), Logger: logger.NewLogger(nil, false), Listen: listen.listen,
		Config: map[string]any{ConfigKey: SvcConfigurations{Port: uint16(port), HTTPEnabled: true, Sources: sources}},
	})
	require.NoError(t, err)
	require.Equal(t, 1, listen.open, "Create binds the listener")

	closer, ok := endpoint.(io.Closer)
	require.True(t, ok, "the endpoint can be closed without being served")
	require.NoError(t, closer.Close())
	require.Zero(t, listen.open, "Close releases the listener")
	require.NoError(t, closer.Close(), "Close is safe to repeat")

	again, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	require.NoError(t, err, "the port is free again")
	require.NoError(t, again.Close())
}
