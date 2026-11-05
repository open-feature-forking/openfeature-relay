//go:build linux

package runtime

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/runtime/internal/porttest"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// registry opens listeners as the testbed's gate does: an address stays registered until its listener is closed, and
// listening on a registered address is an error.
type registry struct {
	mu   sync.Mutex
	open map[string]bool
}

func (r *registry) listen(network, address string) (net.Listener, error) {
	key := network + " " + address
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.open[key] {
		return nil, fmt.Errorf("listen %s: already registered", key)
	}
	l, err := porttest.Listen(network, address)
	if err != nil {
		return nil, err
	}
	if r.open == nil {
		r.open = map[string]bool{}
	}
	r.open[key] = true
	return &registered{Listener: l, release: func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		delete(r.open, key)
	}}, nil
}

func (r *registry) addresses() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.open))
	for k := range r.open {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

type registered struct {
	net.Listener
	once    sync.Once
	release func()
}

func (l *registered) Close() error {
	err := l.Listener.Close()
	l.once.Do(l.release)
	return err
}

// failingEndpoint is a service endpoint whose factory cannot create it; atCreate sees what was registered by then.
type failingEndpoint struct{ atCreate func() }

func (failingEndpoint) Type() string         { return "failing" }
func (failingEndpoint) Needs() service.Needs { return service.Needs{} }
func (f failingEndpoint) Create(service.Settings) (service.Endpoint, error) {
	f.atCreate()
	return nil, errors.New("the failing endpoint cannot be created")
}

// A FromConfig that fails releases what it created: flag sync binds its listener when its endpoint is created, and a
// build that fails must not leave it behind, or the next build on the same addresses is refused.
func TestFailedFromConfigReleasesItsListeners(t *testing.T) {
	failures := map[string]func(t *testing.T, cfg *Config, components *Components, reg *registry, sawSync *[]string){
		"a sync provider that cannot be created": func(_ *testing.T, cfg *Config, _ *Components, _ *registry, _ *[]string) {
			cfg.SyncProviders = append(cfg.SyncProviders, coresync.SourceConfig{URI: "nope://flags", Provider: "nope"})
		},
		"an endpoint created after flag sync that cannot be created": func(_ *testing.T, _ *Config, components *Components, reg *registry, saw *[]string) {
			components.Endpoints = append(components.Endpoints, failingEndpoint{atCreate: func() { *saw = reg.addresses() }})
		},
	}

	for name, fail := range failures {
		t.Run(name, func(t *testing.T) {
			p := newPorts(t)
			reg := &registry{}
			components := officialComponents()
			components.Listen = reg.listen
			syncAddress := fmt.Sprintf("tcp :%d", p.sync)

			cfg := characterizedConfig(t, p)
			var registeredAtFailure []string
			fail(t, &cfg, &components, reg, &registeredAtFailure)
			rt, err := FromConfig(logger.NewLogger(nil, false), "test", cfg, components)
			require.Error(t, err)
			require.Nil(t, rt)
			if registeredAtFailure != nil {
				require.Contains(t, registeredAtFailure, syncAddress, "flag sync was created before the failure")
			}
			require.Empty(t, reg.addresses(), "a build that fails holds no listener")

			// the same addresses can be listened on again, through the gate and in the process
			good := characterizedConfig(t, p)
			good.OfrepServicePort, good.ServicePort = cfg.OfrepServicePort, cfg.ServicePort
			good.ManagementPort, good.SyncServicePort = cfg.ManagementPort, cfg.SyncServicePort
			valid, stop := buildWith(t, good, func() Components { c := officialComponents(); c.Listen = reg.listen; return c }())
			newProbe(p.management).waitReady(t, time.Now())
			require.NotNil(t, valid)
			require.Contains(t, reg.addresses(), syncAddress)
			stop()
			require.Empty(t, reg.addresses(), "a runtime that stops holds no listener either")

			held, err := porttest.Listen("tcp", fmt.Sprintf(":%d", p.sync))
			require.NoError(t, err, "the port is free in the process")
			require.NoError(t, held.Close())
		})
	}
}

// Builds that fail and builds that work, one after the other on one gate, leave nothing behind and each one listens:
// the testbed switches configurations this way, and an invalid one is among them.
func TestFailedAndValidBuildsAlternate(t *testing.T) {
	p := newPorts(t)
	reg := &registry{}
	components := officialComponents()
	components.Listen = reg.listen
	check := newProbe(p.management)

	failing := characterizedConfig(t, p)
	failing.SyncProviders = append(failing.SyncProviders, coresync.SourceConfig{URI: "nope://flags", Provider: "nope"})
	valid := characterizedConfig(t, p)

	cycle := func() {
		_, err := FromConfig(logger.NewLogger(nil, false), "test", failing, components)
		require.Error(t, err)
		_, stop := buildWith(t, valid, components)
		check.waitReady(t, time.Now())
		stop()
		require.Empty(t, reg.addresses())
	}

	cycle()
	check.client.CloseIdleConnections()
	ignore := goleak.IgnoreCurrent()
	fds := settledFDs(t)

	for range 100 {
		cycle()
	}

	check.client.CloseIdleConnections()
	goleak.VerifyNone(t, ignore)
	requireNoFDLeak(t, fds)
}
