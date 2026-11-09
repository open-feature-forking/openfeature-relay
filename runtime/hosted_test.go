package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/stretchr/testify/require"
)

// hostLog records what the runtime does with a hosted endpoint and its host.
type hostLog struct {
	mu      sync.Mutex
	log     []string
	served  []string
	closed  map[string]int
	gotHost map[string][]service.Endpoint
}

func newHostLog() *hostLog {
	return &hostLog{closed: map[string]int{}, gotHost: map[string][]service.Endpoint{}}
}

func (p *hostLog) record(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.log = append(p.log, s)
}

func (p *hostLog) saw(s string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, l := range p.log {
		if l == s {
			return true
		}
	}
	return false
}

type hostedProbe struct {
	p            *hostLog
	typeName     string
	host         string
	failCreation bool
}

func (f hostedProbe) Type() string         { return f.typeName }
func (f hostedProbe) Needs() service.Needs { return service.Needs{} }
func (f hostedProbe) Host() string         { return f.host }
func (f hostedProbe) Create(service.Settings) (service.Endpoint, error) {
	f.p.record("create " + f.typeName)
	if f.failCreation {
		return nil, errors.New("cannot create")
	}
	return hostedProbeEndpoint{f}, nil
}

type hostedProbeEndpoint struct{ f hostedProbe }

func (e hostedProbeEndpoint) Serve(context.Context) error {
	e.f.p.mu.Lock()
	e.f.p.served = append(e.f.p.served, e.f.typeName)
	e.f.p.mu.Unlock()
	return errors.New("served on its own")
}

func (e hostedProbeEndpoint) Close() error {
	e.f.p.mu.Lock()
	defer e.f.p.mu.Unlock()
	e.f.p.closed[e.f.typeName]++
	return nil
}

// hostProbe declares Hosts, with the answer it is given.
type hostProbe struct {
	p        *hostLog
	typeName string
	hosts    bool
	fail     bool
}

func (f hostProbe) Type() string         { return f.typeName }
func (f hostProbe) Needs() service.Needs { return service.Needs{} }
func (f hostProbe) Hosts() bool          { return f.hosts }
func (f hostProbe) Create(s service.Settings) (service.Endpoint, error) {
	f.p.record("create " + f.typeName)
	f.p.mu.Lock()
	f.p.gotHost[f.typeName] = s.Extensions
	f.p.mu.Unlock()
	if f.fail {
		return nil, errors.New("host cannot be created")
	}
	return servingProbe{f.p, f.typeName}, nil
}

// plainProbe is an endpoint that declares no Hosts method.
type plainProbe struct {
	p        *hostLog
	typeName string
}

func (f plainProbe) Type() string         { return f.typeName }
func (f plainProbe) Needs() service.Needs { return service.Needs{} }
func (f plainProbe) Create(s service.Settings) (service.Endpoint, error) {
	f.p.record("create " + f.typeName)
	f.p.mu.Lock()
	f.p.gotHost[f.typeName] = s.Extensions
	f.p.mu.Unlock()
	return servingProbe{f.p, f.typeName}, nil
}

type servingProbe struct {
	p        *hostLog
	typeName string
}

func (e servingProbe) Serve(ctx context.Context) error {
	e.p.record("serve " + e.typeName)
	<-ctx.Done()
	return nil
}

func buildHosted(t *testing.T, factories ...service.Factory) (*Runtime, error) {
	t.Helper()
	return FromConfig(logger.NewLogger(nil, false), "test", Config{SyncProviders: oneFileSource(t)},
		withFlagd(Components{Endpoints: factories}))
}

// TestHostedEndpointsAreCreatedFirstGivenToTheirHostAndNeverServed is T1: the runtime creates the hosted endpoints
// before the others, hands each to the endpoint of its host type only, leaves it out of the endpoints it serves, and
// closes it once the endpoints have stopped.
func TestHostedEndpointsAreCreatedFirstGivenToTheirHostAndNeverServed(t *testing.T) {
	p := newHostLog()
	rt, err := buildHosted(t,
		plainProbe{p, "bystander"},
		hostProbe{p: p, typeName: "host", hosts: true},
		hostedProbe{p: p, typeName: "legacy-a", host: "host"},
		hostedProbe{p: p, typeName: "legacy-b", host: "host"},
	)
	require.NoError(t, err)

	require.Equal(t, []string{"create legacy-a", "create legacy-b", "create bystander", "create host"}, p.log,
		"hosted endpoints first, then the others in list order")
	require.Len(t, p.gotHost["host"], 2)
	require.Empty(t, p.gotHost["bystander"], "only the host gets them")
	require.Len(t, rt.Endpoints, 2)
	require.Equal(t, "bystander", rt.Endpoints[0].Type)
	require.Equal(t, "host", rt.Endpoints[1].Type, "a hosted endpoint is not one of the endpoints the runtime serves")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rt.start(ctx) }()
	require.Eventually(t, func() bool { return p.saw("serve host") }, 10*time.Second, 20*time.Millisecond)
	p.mu.Lock()
	require.Zero(t, p.closed["legacy-a"], "not closed while its host serves")
	p.mu.Unlock()
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err, "a hosted endpoint's Serve is never called, so its error cannot stop the runtime")
	case <-time.After(20 * time.Second):
		t.Fatal("the runtime did not stop")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	require.Empty(t, p.served)
	require.Equal(t, map[string]int{"legacy-a": 1, "legacy-b": 1}, p.closed, "closed once at shutdown")
}

func TestHostedEndpointIsClosedWhenTheBuildFails(t *testing.T) {
	p := newHostLog()
	rt, err := buildHosted(t,
		hostedProbe{p: p, typeName: "legacy", host: "host"},
		hostProbe{p: p, typeName: "host", hosts: true, fail: true},
	)
	require.EqualError(t, err, `error creating the service endpoint "host": host cannot be created`)
	require.Nil(t, rt)
	require.Equal(t, map[string]int{"legacy": 1}, p.closed)

	p = newHostLog()
	_, err = buildHosted(t,
		hostedProbe{p: p, typeName: "legacy", host: "host", failCreation: true},
		hostProbe{p: p, typeName: "host", hosts: true},
	)
	require.EqualError(t, err, `error creating the service endpoint "legacy": cannot create`)
	require.Empty(t, p.closed)
}

func TestHostedEndpointRefusals(t *testing.T) {
	p := newHostLog()
	tests := []struct {
		name      string
		factories []service.Factory
		wantErr   string
	}{
		{
			name:      "no host in the distribution",
			factories: []service.Factory{hostedProbe{p: p, typeName: "legacy", host: "host"}, plainProbe{p, "other"}},
			wantErr:   `the service endpoint "legacy" is hosted by "host", which this distribution does not contain`,
		},
		{
			name:      "a host that does not declare Hosts",
			factories: []service.Factory{hostedProbe{p: p, typeName: "legacy", host: "host"}, plainProbe{p, "host"}},
			wantErr:   `the service endpoint "legacy" is hosted by "host", which does not host endpoints`,
		},
		{
			name:      "a host whose Hosts is false",
			factories: []service.Factory{hostedProbe{p: p, typeName: "legacy", host: "host"}, hostProbe{p: p, typeName: "host"}},
			wantErr:   `the service endpoint "legacy" is hosted by "host", which does not host endpoints`,
		},
		{
			name: "two factories of the host's type",
			factories: []service.Factory{
				hostedProbe{p: p, typeName: "legacy", host: "host"},
				hostProbe{p: p, typeName: "host", hosts: true}, hostProbe{p: p, typeName: "host", hosts: true},
			},
			wantErr: `the service endpoint "legacy" is hosted by "host", but the distribution has 2 endpoints of that type`,
		},
		{
			name:      "a host that is hosted itself",
			factories: []service.Factory{hostedProbe{p: p, typeName: "legacy", host: "middle"}, hostedProbe{p: p, typeName: "middle", host: "legacy"}},
			wantErr:   `the service endpoint "legacy" is hosted by "middle", which is itself a hosted endpoint`,
		},
		{
			name:      "an invalid host declaration",
			factories: []service.Factory{hostedProbe{p: p, typeName: "legacy", host: ""}},
			wantErr:   `the service endpoint "legacy" declares: the host is empty`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p.log = nil
			rt, err := buildHosted(t, tt.factories...)
			require.Nil(t, rt)
			require.EqualError(t, err, tt.wantErr)
			require.Empty(t, p.log, "nothing is created when the declarations are refused")
		})
	}
}

// TestBadDeclarationOfAnotherEndpointCreatesNothing: with a hosted endpoint present, the declaration of any other
// factory is checked before the hosted endpoint is created.
func TestBadDeclarationOfAnotherEndpointCreatesNothing(t *testing.T) {
	p := newHostLog()
	rt, err := buildHosted(t,
		hostedProbe{p: p, typeName: "legacy", host: "host"}, hostProbe{p: p, typeName: "host", hosts: true},
		plainProbe{p, ""})
	require.Nil(t, rt)
	require.EqualError(t, err, `the service endpoint "" declares: the type name is empty`)
	require.Empty(t, p.log, "nothing is created")
	require.Empty(t, p.closed, "so nothing is closed")
}

// TestNoHostedEndpointChangesNothing: without a hosted endpoint every endpoint is created in list order, with no
// extensions, and served.
func TestNoHostedEndpointChangesNothing(t *testing.T) {
	p := newHostLog()
	rt, err := buildHosted(t, plainProbe{p, "a"}, hostProbe{p: p, typeName: "b", hosts: true})
	require.NoError(t, err)
	require.Equal(t, []string{"create a", "create b"}, p.log)
	require.Empty(t, p.gotHost["a"])
	require.Empty(t, p.gotHost["b"])
	require.Len(t, rt.Endpoints, 2)
}
