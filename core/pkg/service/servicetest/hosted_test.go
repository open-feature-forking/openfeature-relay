package servicetest

import (
	"context"
	"fmt"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
	"github.com/stretchr/testify/require"
)

// hostStub is an endpoint stub that hosts: it refuses to be created without the hosted endpoint in its Extensions.
type hostStub struct {
	stub
	hosts bool
}

func (h hostStub) Hosts() bool { return h.hosts }

func (h hostStub) Create(s service.Settings) (service.Endpoint, error) {
	if len(s.Extensions) != 1 {
		return nil, fmt.Errorf("expected one hosted endpoint, got %d", len(s.Extensions))
	}
	return h.stub.Create(s)
}

// hostedStub is a hosted endpoint: it opens nothing, and its Serve names its host.
type hostedStub struct {
	typeName, host string
	serveErr       error
	closed         *int
}

func (h hostedStub) Type() string         { return h.typeName }
func (h hostedStub) Needs() service.Needs { return service.Needs{} }
func (h hostedStub) Host() string         { return h.host }
func (h hostedStub) Create(service.Settings) (service.Endpoint, error) {
	return hostedEndpoint{h}, nil
}

type hostedEndpoint struct{ hostedStub }

func (e hostedEndpoint) Serve(context.Context) error {
	if e.serveErr != nil {
		return e.serveErr
	}
	return fmt.Errorf("the endpoint %q is served by %q, not on its own", e.typeName, e.host)
}

func (e hostedEndpoint) Close() error {
	if e.closed != nil {
		*e.closed++
	}
	return nil
}

func hostedInputs(host service.Factory) Inputs {
	in := inputsFor(resolverFeatures, true)
	in.Host = host
	return in
}

func newHost(hosts bool) hostStub {
	return hostStub{stub: stub{typeName: "host", needs: service.Needs{Router: true}, features: resolverFeatures}, hosts: hosts}
}

// TestHostedEndpointMeetsTheContractComposedWithItsHost: the hosted endpoint is created, given to its host, and the
// cases run against the host.
func TestHostedEndpointMeetsTheContractComposedWithItsHost(t *testing.T) {
	results := Check(t, hostedStub{typeName: "legacy", host: "host"}, hostedInputs(newHost(true)))
	require.Empty(t, failed(results), "%+v", results)
	ran := names(results)
	require.Contains(t, ran, "is served by a host that hosts it")
	require.Contains(t, ran, "is never served on its own")
	require.Contains(t, ran, nameResolves, "the resolution cases run through the host")
}

// TestHostedEndpointWithoutHostFails: a hosted factory run without Inputs.Host says it is served by its host.
func TestHostedEndpointWithoutHostFails(t *testing.T) {
	results := Check(t, hostedStub{typeName: "legacy", host: "host"}, inputsFor(features{}, false))
	require.True(t, results.Failed())
	require.Equal(t, `the endpoint "legacy" is served by "host", not on its own; set Inputs.Host to the factory of that endpoint`, results.Aborted)
}

func TestHostInputsOfAnEndpointThatIsNotHostedAbort(t *testing.T) {
	in := inputsFor(features{}, false)
	in.Host = newHost(true)
	results := Check(t, stub{typeName: "x"}, in)
	require.Contains(t, results.Aborted, "is not hosted")
}

func TestHostedContractFailsWhenTheHostDoesNotHost(t *testing.T) {
	results := Check(t, hostedStub{typeName: "legacy", host: "host"}, hostedInputs(newHost(false)))
	require.Equal(t, []string{"is served by a host that hosts it"}, failed(results))

	wrong := hostedStub{typeName: "legacy", host: "other"}
	results = Check(t, wrong, hostedInputs(newHost(true)))
	require.Equal(t, []string{"is served by a host that hosts it"}, failed(results))
}

func TestHostedContractFailsWhenServeDoesNotNameTheHost(t *testing.T) {
	results := Check(t, hostedStub{typeName: "legacy", host: "host", serveErr: fmt.Errorf("nope")}, hostedInputs(newHost(true)))
	require.Equal(t, []string{"is never served on its own"}, failed(results))
}
