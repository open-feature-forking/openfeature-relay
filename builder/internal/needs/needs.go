// Package needs checks the components of a distribution against each other from what its probe reports, before the
// distribution is compiled (BR-014, BR-015): every need is met, every hosted endpoint has a host that hosts it, no two sync providers claim a scheme, and an
// evaluator that nothing needs is reported.
package needs

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Group names are those of core's distribution package.
const (
	groupSync     = "sync_providers"
	groupFormat   = "configuration_formats"
	groupEval     = "evaluators"
	groupEndpoint = "service_endpoints"
)

// Needs is what a service endpoint declares it resolves flags with.
type Needs struct {
	Router bool   `json:"router,omitempty"`
	Format string `json:"format,omitempty"`
}

// Item is one factory of the distribution, as the probe prints it.
type Item struct {
	Group   string   `json:"group"`
	Type    string   `json:"type"`
	Module  string   `json:"module"`
	Schemes []string `json:"schemes,omitempty"`
	// Format is the configuration format an evaluator resolves.
	Format string `json:"format,omitempty"`
	Needs  *Needs `json:"needs,omitempty"`
	// Host is the type of the endpoint that serves a hosted endpoint; Hosts reports that an endpoint mounts hosted endpoints.
	Host  string `json:"host,omitempty"`
	Hosts bool   `json:"hosts,omitempty"`
}

func (i Item) String() string { return fmt.Sprintf("%s %q (%s)", singular(i.Group), i.Type, i.Module) }

func singular(group string) string {
	switch group {
	case groupSync:
		return "sync provider"
	case groupFormat:
		return "configuration format"
	case groupEval:
		return "evaluator"
	case groupEndpoint:
		return "service endpoint"
	}
	return group
}

// ParseProbe reads what the probe printed.
func ParseProbe(output []byte) ([]Item, error) {
	var items []Item
	if err := json.Unmarshal(output, &items); err != nil {
		return nil, fmt.Errorf("the probe printed unreadable output: %w", err)
	}
	return items, nil
}

// Error reports every fault of the combination of components.
type Error struct {
	Problems []string
}

func (e *Error) Error() string {
	return "the components do not fit together:\n  " + strings.Join(e.Problems, "\n  ")
}

// Check returns an *Error naming every unmet need and every scheme two sync providers claim, and the warnings for
// evaluators that no component needs.
func Check(items []Item) (warnings []string, err error) {
	var problems []string
	formats := map[string]bool{}
	evaluatorsOf := map[string][]Item{}
	var endpoints, evaluators []Item
	claimed := map[string][]Item{}
	for _, it := range items {
		switch it.Group {
		case groupFormat:
			formats[it.Type] = true
		case groupEval:
			evaluators = append(evaluators, it)
			evaluatorsOf[it.Format] = append(evaluatorsOf[it.Format], it)
		case groupEndpoint:
			endpoints = append(endpoints, it)
		case groupSync:
			for _, s := range it.Schemes {
				claimed[s] = append(claimed[s], it)
			}
		}
	}

	// the router is only built from evaluators that exist, so an endpoint that resolves flags needs one
	for _, ep := range endpoints {
		if ep.Needs == nil {
			continue
		}
		if ep.Needs.Router {
			if len(evaluators) == 0 {
				problems = append(problems, fmt.Sprintf("%s resolves flags through the evaluation router, but no evaluator is selected", ep))
			}
		}
		if f := ep.Needs.Format; f != "" {
			if len(evaluatorsOf[f]) == 0 {
				problems = append(problems, fmt.Sprintf("%s needs the evaluator of the configuration format %q, which no selected evaluator resolves", ep, f))
			}
		}
	}
	// a hosted endpoint opens no listener, so it is only served when an endpoint of its host type mounts it
	for _, ep := range endpoints {
		if ep.Host == "" {
			continue
		}
		var typed, hosting int
		for _, h := range endpoints {
			if h.Type == ep.Host && h.Host == "" {
				typed++
				if h.Hosts {
					hosting++
				}
			}
		}
		switch {
		case typed == 0:
			problems = append(problems, fmt.Sprintf("%s is hosted by the service endpoint %q, but no selected service endpoint has that type", ep, ep.Host))
		case hosting == 0:
			problems = append(problems, fmt.Sprintf("%s is hosted by the service endpoint %q, which does not host other endpoints", ep, ep.Host))
		}
	}
	for _, ev := range evaluators {
		if !formats[ev.Format] {
			problems = append(problems, fmt.Sprintf("%s resolves the configuration format %q, which no selected configuration format provides", ev, ev.Format))
		}
	}

	schemes := make([]string, 0, len(claimed))
	for s := range claimed {
		schemes = append(schemes, s)
	}
	sort.Strings(schemes)
	for _, s := range schemes {
		if owners := claimed[s]; len(owners) > 1 {
			names := make([]string, len(owners))
			for i, o := range owners {
				names[i] = o.String()
			}
			problems = append(problems, fmt.Sprintf("the scheme %q is claimed by %s", s, strings.Join(names, " and ")))
		}
	}

	if len(problems) > 0 {
		return nil, &Error{Problems: problems}
	}
	for _, ev := range unused(evaluators, endpoints) {
		warnings = append(warnings, fmt.Sprintf("%s is compiled in, but no selected component uses it", ev))
	}
	return warnings, nil
}

// unused returns the evaluators that no endpoint uses: through the router, which any evaluator serves, or by name of
// its configuration format.
func unused(evaluators, endpoints []Item) []Item {
	router := false
	byFormat := map[string]bool{}
	for _, ep := range endpoints {
		if ep.Needs == nil {
			continue
		}
		router = router || ep.Needs.Router
		if ep.Needs.Format != "" {
			byFormat[ep.Needs.Format] = true
		}
	}
	if router {
		return nil
	}
	var out []Item
	for _, ev := range evaluators {
		if !byFormat[ev.Format] {
			out = append(out, ev)
		}
	}
	return out
}
