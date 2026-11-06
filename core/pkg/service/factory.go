package service

import (
	"errors"
	"strings"
)

// Factory creates a service endpoint from what a distribution injects.
type Factory interface {
	// Type names the service endpoint, for example in a distribution's configuration.
	Type() string
	// Needs declares what the endpoint resolves flags with. An endpoint that resolves no flag declares none.
	Needs() Needs
	Create(Settings) (Endpoint, error)
}

// Hosted is implemented by the factory of a hosted endpoint: an endpoint that opens no listener and is served by the server
// of another endpoint, its host. The runtime creates it, never serves it, and hands it to the host in Settings.Extensions.
// It implements Endpoint, but its Serve is never called and returns an error naming its host. It is closed through
// io.Closer when a build fails.
type Hosted interface {
	// Host is the type of the endpoint whose server serves this endpoint.
	Host() string
}

// Hosting is implemented by the factory of an endpoint that mounts hosted endpoints. A host that does not declare Hosts
// would ignore Settings.Extensions, so the runtime refuses a hosted endpoint whose host is not Hosting or whose Hosts
// returns false: only a host that returns true receives hosted endpoints.
type Hosting interface {
	// Hosts reports whether the endpoint mounts hosted endpoints.
	Hosts() bool
}

// ValidateDeclarations checks what a factory declares: a non-empty type name, no blank format need and, for a Hosted
// factory, a host that is non-empty, has no surrounding whitespace and is not the factory's own type. The
// error names every broken rule.
func ValidateDeclarations(f Factory) error {
	var problems []string
	if strings.TrimSpace(f.Type()) == "" {
		problems = append(problems, "the type name is empty")
	}
	if needs := f.Needs(); needs.Format != strings.TrimSpace(needs.Format) {
		problems = append(problems, "the needed configuration format has surrounding whitespace")
	}
	if h, ok := f.(Hosted); ok {
		switch host := h.Host(); {
		case host == "":
			problems = append(problems, "the host is empty")
		case host != strings.TrimSpace(host):
			problems = append(problems, "the host has surrounding whitespace")
		case host == f.Type():
			problems = append(problems, "the host is the endpoint's own type")
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return errors.New(strings.Join(problems, "; "))
}
