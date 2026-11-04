//go:build unix

package keyvalue_test

import (
	"testing"

	keyvalue "github.com/open-feature/flagd/test/configformat-example"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat/formattest"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/stretchr/testify/require"
)

// TestContract covers the main scenario's steps 1 to 7: the example is a component module of its own
// that depends on the core module and meets every case of the configuration format contract.
func TestContract(t *testing.T) {
	formattest.Run(t, keyvalue.Factory{}, formattest.Samples{
		Valid: formattest.Delivery{
			Data: `{"environment":"prod","flags":{"a":1,"b":"two","c":true}}`,
			Sets: []formattest.Set{{FlagSet: "prod", Keys: []string{"a", "b", "c"}}},
		},
		Changed: formattest.Change{
			Delivery: formattest.Delivery{
				Data: `{"environment":"prod","flags":{"a":1,"b":"changed","d":4}}`,
				Sets: []formattest.Set{{FlagSet: "prod", Keys: []string{"a", "b", "d"}}},
			},
			FlagSet: "prod", Added: []string{"d"}, Updated: []string{"b"}, Removed: []string{"c"},
		},
		Invalid: formattest.Faulty{
			Data:    `{"environment":"prod","flags":{"a":1,"b":null}}`,
			Entries: []string{"b"},
		},
		Incremental: &[2]formattest.Delivery{
			{Data: `{"environment":"prod","flags":{"a":1}}`, Sets: []formattest.Set{{FlagSet: "prod", Keys: []string{"a"}}}},
			{Data: `{"environment":"dev","flags":{"a":1}}`, Sets: []formattest.Set{{FlagSet: "dev", Keys: []string{"a"}}}},
		},
		// the format has no entries it skips and accepts the rest of a delivery without
		Without: []formattest.Feature{formattest.FeatureDropped},
	})
}

// TestEngineModuleExportsFormatFactory covers A5's configuration format half: the module exports the
// factory under the type name that sources name. The paired evaluator is tested with UC-007.
func TestEngineModuleExportsFormatFactory(t *testing.T) {
	var factory configformat.Factory = keyvalue.Factory{}
	require.Equal(t, "keyvalue", factory.Type())

	st, err := store.NewStore(logger.NewLogger(nil, false), []string{"src"})
	require.NoError(t, err)
	format, err := factory.Create(configformat.Settings{Store: st})
	require.NoError(t, err)
	require.Equal(t, factory.Type(), format.Type())
}

// TestDocumentTextIsTheDelivery pins that the flag sync endpoint can send a keyvalue document: its value offers the
// text the source delivered, unchanged.
func TestDocumentTextIsTheDelivery(t *testing.T) {
	st, err := store.NewStore(logger.NewLogger(nil, false), []string{"src"})
	require.NoError(t, err)
	format, err := keyvalue.Factory{}.Create(configformat.Settings{Store: st})
	require.NoError(t, err)
	data := `{ "environment": "prod",  "flags": {"a":1,   "b":"two"} }`

	_, err = format.Accept(t.Context(), configformat.Input{Source: "src", Data: data})
	require.NoError(t, err)

	docs := st.Documents(keyvalue.Type, "src")
	require.Len(t, docs, 1)
	text, ok := store.DocumentText(docs[0])
	require.True(t, ok)
	require.Equal(t, data, text)
}
