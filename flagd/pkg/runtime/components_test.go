package runtime

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/telemetry"
	"github.com/stretchr/testify/require"
)

type namedFormat struct{ name string }

func (f namedFormat) Type() string { return f.name }
func (namedFormat) Create(configformat.Settings) (configformat.ConfigurationFormat, error) {
	return nil, nil
}

// TestDeprecatedConfigSlicesStillApply pins that the factories of the deprecated Config fields and those of
// Components are both used, the Config's first, and that the caller's slices are not written to.
func TestDeprecatedConfigSlicesStillApply(t *testing.T) {
	inConfig := []configformat.Factory{namedFormat{"from-config"}}
	config := withComponents(Config{ConfigurationFormats: inConfig}, Components{
		ConfigurationFormats: []configformat.Factory{namedFormat{"from-components"}},
	})

	var types []string
	for _, f := range config.ConfigurationFormats {
		types = append(types, f.Type())
	}
	require.Equal(t, []string{"from-config", "from-components"}, types)
	require.Len(t, inConfig, 1)
}

type metricsProvider struct {
	telemetry.Provider
	handler http.Handler
}

func (p metricsProvider) MetricsHandler() http.Handler { return p.handler }

// TestMetricsHandlerSelection pins who serves /metrics: the handler of the components, else the telemetry
// provider's, else nobody.
func TestMetricsHandlerSelection(t *testing.T) {
	fromComponents := http.NotFoundHandler()
	fromProvider := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	withHandler := metricsProvider{Provider: telemetry.Noop(), handler: fromProvider}

	require.Equal(t, reflect.ValueOf(fromComponents).Pointer(),
		reflect.ValueOf(metricsHandler(withHandler, Components{MetricsHandler: fromComponents})).Pointer())
	require.Equal(t, reflect.ValueOf(fromProvider).Pointer(),
		reflect.ValueOf(metricsHandler(withHandler, Components{})).Pointer())
	require.Nil(t, metricsHandler(telemetry.Noop(), Components{}), "the noop telemetry provider serves no metrics")
}
