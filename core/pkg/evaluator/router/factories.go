package router

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
)

// FromFactories builds the router of a distribution from the factories of its evaluators. formats are the types
// of the distribution's configuration formats. An evaluator for a configuration format the distribution lacks is
// an error that lists the formats; a format without an evaluator is logged, and its flags resolve to
// FLAG_NOT_FOUND. settings returns the evaluator's own settings by the type of its factory; it may be nil.
func FromFactories(
	log *logger.Logger, st *store.Store, formats []string, factories []evaluator.Factory,
	settings func(evaluatorType string) map[string]any,
) (*Router, error) {
	available := slices.Clone(formats)
	sort.Strings(available)
	available = slices.Compact(available)

	var routes []Route
	covered := map[string]struct{}{}
	for _, factory := range factories {
		format := factory.ConfigurationFormat()
		if !slices.Contains(available, format) {
			return nil, fmt.Errorf("the evaluator %q resolves the configuration format %q, which the distribution lacks; it has: %s",
				factory.Type(), format, strings.Join(available, ", "))
		}
		var config map[string]any
		if settings != nil {
			config = settings(factory.Type())
		}
		ev, err := factory.Create(evaluator.Settings{Store: st, Logger: log, Config: config})
		if err != nil {
			return nil, fmt.Errorf("error creating the evaluator %q: %w", factory.Type(), err)
		}
		routes = append(routes, Route{Format: format, Evaluator: ev})
		covered[format] = struct{}{}
	}
	for _, format := range available {
		if _, ok := covered[format]; !ok {
			log.Warn(fmt.Sprintf("the configuration format %q has no evaluator; its flags resolve to FLAG_NOT_FOUND", format))
		}
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("the distribution has no evaluator, but a service endpoint needs the evaluation router; its configuration formats are: %s",
			strings.Join(available, ", "))
	}
	return New(st, routes...)
}
