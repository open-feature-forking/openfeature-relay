package flagsmith_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat/formattest"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"

	flagsmith "github.com/open-feature-forking/openfeature-relay/contrib/flagsmith/engine"
)

func document(t testing.TB, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "documents", name))
	require.NoError(t, err)
	return string(raw)
}

func newFormat(t testing.TB, sources ...string) (configformat.ConfigurationFormat, *store.Store) {
	t.Helper()
	st, err := store.NewStore(logger.NewLogger(nil, false), sources)
	require.NoError(t, err)
	format, err := flagsmith.Factory{}.Create(configformat.Settings{Store: st})
	require.NoError(t, err)
	return format, st
}

var basicKeys = []string{"banner", "config", "dark-mode", "experiment", "kill-switch", "limit", "ratio", "welcome"}

// TestFormatContract covers the main scenario's step 5 and A4: the format meets every case of the configuration format
// contract with Flagsmith's environment documents as samples.
func TestFormatContract(t *testing.T) {
	formattest.Run(t, flagsmith.Factory{}, formattest.Samples{
		Valid: formattest.Delivery{
			Data: document(t, "basic.json"),
			Sets: []formattest.Set{{FlagSet: "ENVKEY1", Keys: basicKeys}},
		},
		Changed: formattest.Change{
			Delivery: formattest.Delivery{
				Data: document(t, "changed.json"),
				Sets: []formattest.Set{{FlagSet: "ENVKEY1", Keys: []string{"added", "banner", "dark-mode", "kill-switch", "ratio", "welcome"}}},
			},
			FlagSet: "ENVKEY1",
			Added:   []string{"added"},
			// banner lost its segment override, dark-mode its identity override, welcome its value
			Updated: []string{"banner", "dark-mode", "welcome"},
			Removed: []string{"config", "experiment", "limit"},
		},
		Invalid: formattest.Faulty{
			Data:    `{"api_key":"ENVKEY1","feature_states":[{"feature":{"id":1,"name":"banner"},"enabled":true,"multivariate_feature_state_values":[{"id":1,"percentage_allocation":50}]}]}`,
			Entries: []string{"banner"},
		},
		Incremental: &[2]formattest.Delivery{
			{Data: document(t, "basic.json"), Sets: []formattest.Set{{FlagSet: "ENVKEY1", Keys: basicKeys}}},
			{Data: document(t, "second.json"), Sets: []formattest.Set{{FlagSet: "ENVKEY2", Keys: []string{"dark-mode", "welcome"}}}},
		},
		// every feature of a document is used; the format skips none and accepts the rest of a delivery without
		Without: []formattest.Feature{formattest.FeatureDropped},
	})
}

func accept(t testing.TB, format configformat.ConfigurationFormat, source, data string) (configformat.Result, error) {
	t.Helper()
	return format.Accept(context.Background(), configformat.Input{Source: source, Data: data})
}

func rejection(t testing.TB, err error) configformat.Rejection {
	t.Helper()
	var r configformat.Rejection
	require.ErrorAs(t, err, &r)
	return r
}

func TestAcceptRejectsNamingEntry(t *testing.T) {
	format, st := newFormat(t, "src")
	_, err := accept(t, format, "src", document(t, "basic.json"))
	require.NoError(t, err)

	for name, tc := range map[string]struct{ data, entry string }{
		"not json":             {`{"api_key":`, "document"},
		"no api key":           {`{"feature_states":[]}`, "api_key"},
		"feature without name": {`{"api_key":"K","feature_states":[{"enabled":true},{"feature":{"name":"b"}}]}`, "feature_states[0]"},
		"second feature faulty": {
			`{"api_key":"K","feature_states":[{"feature":{"name":"a"}},{"enabled":true}]}`, "feature_states[1]",
		},
		"undecodable feature": {`{"api_key":"K","feature_states":[{"feature":{"id":"x","name":"typed"}}]}`, "typed"},
		"segment override without feature": {
			`{"api_key":"K","project":{"segments":[{"id":1,"name":"s","feature_states":[{"enabled":true}]}]}}`, `segment "s"[0]`,
		},
		"identity override without feature": {
			`{"api_key":"K","identity_overrides":[{"identifier":"vip","identity_features":[{"enabled":true}]}]}`, `identity "vip"[0]`,
		},
		"undecodable segment": {`{"api_key":"K","project":{"segments":[{"id":"x","name":"broken"}]}}`, "broken"},
		"updated_at":          {`{"api_key":"K","updated_at":"yesterday-ish"}`, "updated_at"},
		"updated_at type":     {`{"api_key":"K","updated_at":5}`, "updated_at"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := accept(t, format, "src", tc.data)
			r := rejection(t, err)
			require.Equal(t, tc.entry, r.Entry)
			require.Equal(t, "src", r.Source)
			require.NotEmpty(t, r.Reason)
		})
	}

	// A4: every rejection leaves the document accepted before it in the store.
	docs := st.Documents(flagsmith.Type, "src")
	require.Len(t, docs, 1)
	require.Equal(t, "ENVKEY1", docs[0].FlagSet)
	require.Equal(t, basicKeys, docs[0].FlagKeys)
}

// TestUpdatedAtWithoutZone pins UC-019 Q6: the document of an environment whose updated_at carries no zone is
// accepted, as Flagsmith's Python engine accepts it. Flagsmith's Go client refuses it.
func TestUpdatedAtWithoutZone(t *testing.T) {
	format, st := newFormat(t, "src")
	for name, stamp := range map[string]string{
		"no zone":         `"2026-01-02T03:04:05"`,
		"fractions":       `"2026-01-02T03:04:05.123456"`,
		"zone":            `"2026-01-02T03:04:05Z"`,
		"offset":          `"2026-01-02T03:04:05+02:00"`,
		"space separated": `"2026-01-02 03:04:05"`,
		"null":            `null`,
		"empty":           `""`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := accept(t, format, "src", `{"api_key":"K","updated_at":`+stamp+`,"feature_states":[{"feature":{"id":1,"name":"f"},"enabled":true}]}`)
			require.NoError(t, err)
			require.Len(t, st.Documents(flagsmith.Type, "src"), 1)
		})
	}
	_, err := accept(t, format, "src", `{"api_key":"K","feature_states":[]}`)
	require.NoError(t, err, "an absent updated_at is accepted")
}

// TestVersionIsDigestOfBody keeps the version of a delivery stable across restarts and equal for equal bodies.
func TestVersionIsDigestOfBody(t *testing.T) {
	format, _ := newFormat(t, "src")
	first, err := accept(t, format, "src", document(t, "basic.json"))
	require.NoError(t, err)
	again, err := accept(t, format, "src", document(t, "basic.json"))
	require.NoError(t, err)
	other, err := accept(t, format, "src", document(t, "changed.json"))
	require.NoError(t, err)

	require.Len(t, first.Documents, 1)
	require.Equal(t, first.Documents[0].Version, again.Documents[0].Version)
	require.NotEqual(t, first.Documents[0].Version, other.Documents[0].Version)
	require.Len(t, first.Documents[0].Version, 64)
}

// TestUnchangedDeliveryReportsNoChange pins the report of a delivery of a document the store already holds.
func TestUnchangedDeliveryReportsNoChange(t *testing.T) {
	format, _ := newFormat(t, "src")
	_, err := accept(t, format, "src", document(t, "basic.json"))
	require.NoError(t, err)
	res, err := accept(t, format, "src", document(t, "basic.json"))
	require.NoError(t, err)
	require.Len(t, res.Documents, 1)
	require.Empty(t, res.Documents[0].Added)
	require.Empty(t, res.Documents[0].Updated)
	require.Empty(t, res.Documents[0].Removed)
}

// TestSegmentRuleChangeUpdatesItsFeature: a segment's rules decide who gets an override, so a change to them is a change
// to the features the segment overrides, and to no others.
func TestSegmentRuleChangeUpdatesItsFeature(t *testing.T) {
	format, _ := newFormat(t, "src")
	_, err := accept(t, format, "src", document(t, "basic.json"))
	require.NoError(t, err)
	changed := replaceOnce(t, document(t, "basic.json"), `"value": "beta"`, `"value": "alpha"`)
	res, err := accept(t, format, "src", changed)
	require.NoError(t, err)
	require.Equal(t, []string{"banner"}, res.Documents[0].Updated)
}

func TestFactoryNeedsStore(t *testing.T) {
	_, err := flagsmith.Factory{}.Create(configformat.Settings{})
	require.Error(t, err)
	require.Equal(t, "flagsmith", flagsmith.Factory{}.Type())
}

func replaceOnce(t testing.TB, s, old, replacement string) string {
	t.Helper()
	require.Equal(t, 1, strings.Count(s, old), "%q must occur once", old)
	return strings.Replace(s, old, replacement, 1)
}
