package formattest_test

import (
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat/formattest"
)

// extensionFormatFactory is the Extension Author's own configuration format factory.
var extensionFormatFactory configformat.Factory

func ExampleRun() {
	_ = func(t *testing.T) {
		formattest.Run(t, extensionFormatFactory, formattest.Samples{
			Valid: formattest.Delivery{
				Data: `{"environment":"prod","flags":{"a":1}}`,
				Sets: []formattest.Set{{FlagSet: "prod", Keys: []string{"a"}}},
			},
			Changed: formattest.Change{
				Delivery: formattest.Delivery{
					Data: `{"environment":"prod","flags":{"a":1,"b":2}}`,
					Sets: []formattest.Set{{FlagSet: "prod", Keys: []string{"a", "b"}}},
				},
				FlagSet: "prod", Added: []string{"b"},
			},
			Invalid: formattest.Faulty{Data: `{"environment":"prod","flags":{"a":null}}`, Entries: []string{"a"}},
			// the format has no incremental update and skips no entries
			Without: []formattest.Feature{formattest.FeatureIncremental, formattest.FeatureDropped},
		})
	}
}
