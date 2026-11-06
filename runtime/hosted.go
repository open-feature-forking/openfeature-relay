package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/service"
)

// hostedPlan is how the endpoint factories of a distribution split into hosted endpoints and the rest.
type hostedPlan struct {
	// isHosted tells, by position in the factory list, which factories are hosted endpoints.
	isHosted []bool
	// hosts is the host type of each hosted endpoint, by position.
	hosts map[int]string
}

// planHosted checks the hosted endpoints among the factories and says which they are. A hosted endpoint is refused
// when the endpoint of its host type is missing, is itself hosted, does not declare that it hosts, or shares its type
// with another factory; every message names both types. Without a hosted endpoint it changes nothing.
func planHosted(factories []service.Factory) (hostedPlan, error) {
	plan := hostedPlan{isHosted: make([]bool, len(factories)), hosts: map[int]string{}}
	for i, f := range factories {
		hosted, ok := f.(service.Hosted)
		if !ok {
			continue
		}
		if err := service.ValidateDeclarations(f); err != nil {
			return plan, fmt.Errorf("the service endpoint %q declares: %w", f.Type(), err)
		}
		plan.isHosted[i] = true
		plan.hosts[i] = hosted.Host()
	}
	for i := range factories {
		host, ok := plan.hosts[i]
		if !ok {
			continue
		}
		typ := factories[i].Type()
		var found []service.Factory
		for _, f := range factories {
			if f.Type() == host {
				found = append(found, f)
			}
		}
		switch {
		case len(found) == 0:
			return plan, fmt.Errorf("the service endpoint %q is hosted by %q, which this distribution does not contain", typ, host)
		case len(found) > 1:
			return plan, fmt.Errorf("the service endpoint %q is hosted by %q, but the distribution has %d endpoints of that type",
				typ, host, len(found))
		}
		if _, hosted := found[0].(service.Hosted); hosted {
			return plan, fmt.Errorf("the service endpoint %q is hosted by %q, which is itself a hosted endpoint", typ, host)
		}
		if h, ok := found[0].(service.Hosting); !ok || !h.Hosts() {
			return plan, fmt.Errorf("the service endpoint %q is hosted by %q, which does not host endpoints", typ, host)
		}
	}
	return plan, nil
}

// closeHosted closes the hosted endpoints that hold a resource, once the endpoints that serve them have stopped.
func closeHosted(hosted []service.Endpoint) func(context.Context) error {
	return func(context.Context) error {
		var errs []error
		for _, e := range hosted {
			if closer, ok := e.(io.Closer); ok {
				errs = append(errs, closer.Close())
			}
		}
		return errors.Join(errs...)
	}
}
