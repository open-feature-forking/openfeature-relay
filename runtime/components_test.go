package runtime

import (
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
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
