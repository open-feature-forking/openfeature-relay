package needs

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func sync(typeName string, schemes ...string) Item {
	return Item{Group: groupSync, Type: typeName, Module: "m/" + typeName, Schemes: schemes}
}
func format(typeName string) Item {
	return Item{Group: groupFormat, Type: typeName, Module: "m/" + typeName}
}
func evaluator(typeName, f string) Item {
	return Item{Group: groupEval, Type: typeName, Module: "m/" + typeName, Format: f}
}
func endpoint(typeName string, n Needs) Item {
	return Item{Group: groupEndpoint, Type: typeName, Module: "m/" + typeName, Needs: &n}
}

func TestUnmetNeeds(t *testing.T) {
	items := []Item{
		sync("file", "file:"), format("flagd"),
		endpoint("ofrep", Needs{Router: true}),
		endpoint("vendor-api", Needs{Format: "keyvalue"}),
		evaluator("kv", "keyvalue"),
	}

	_, err := Check(items)

	var nerr *Error
	require.ErrorAs(t, err, &nerr)
	require.Equal(t, []string{
		`evaluator "kv" (m/kv) resolves the configuration format "keyvalue", which no selected configuration format provides`,
	}, nerr.Problems, "an evaluator without its configuration format is the only unmet need here")

	_, err = Check([]Item{sync("file", "file:"), format("flagd"), endpoint("ofrep", Needs{Router: true})})
	require.ErrorAs(t, err, &nerr)
	require.Equal(t, []string{`service endpoint "ofrep" (m/ofrep) resolves flags through the evaluation router, but no evaluator is selected`}, nerr.Problems)

	_, err = Check([]Item{format("flagd"), evaluator("flagd", "flagd"), endpoint("vendor-api", Needs{Format: "keyvalue"})})
	require.ErrorAs(t, err, &nerr)
	require.Equal(t, []string{`service endpoint "vendor-api" (m/vendor-api) needs the evaluator of the configuration format "keyvalue", which no selected evaluator resolves`}, nerr.Problems)
}

func TestEveryUnmetNeedIsReported(t *testing.T) {
	_, err := Check([]Item{
		endpoint("a", Needs{Router: true}), endpoint("b", Needs{Format: "x"}), evaluator("e", "y"),
	})
	var nerr *Error
	require.ErrorAs(t, err, &nerr)
	require.Len(t, nerr.Problems, 2, "b's need and e's format; a is served by e")
}

func TestSchemeClaimedTwice(t *testing.T) {
	_, err := Check([]Item{sync("file", "file:"), sync("other", "file:", "other:"), sync("http", "http://")})

	var nerr *Error
	require.ErrorAs(t, err, &nerr)
	require.Equal(t, []string{`the scheme "file:" is claimed by sync provider "file" (m/file) and sync provider "other" (m/other)`}, nerr.Problems)
}

func TestUnusedEvaluatorWarns(t *testing.T) {
	// a configuration proxy: flag-sync resolves no flag, so an evaluator in the manifest is compiled in for nothing
	warnings, err := Check([]Item{
		sync("file", "file:"), format("flagd"), evaluator("flagd", "flagd"), endpoint("flag-sync", Needs{}),
	})

	require.NoError(t, err)
	require.Equal(t, []string{`evaluator "flagd" (m/flagd) is compiled in, but no selected component uses it`}, warnings)
}

func TestEndpointCombinations(t *testing.T) {
	flagdFormat, flagdEval := format("flagd"), evaluator("flagd", "flagd")
	tests := []struct {
		name         string
		items        []Item
		wantProblems int
		wantWarnings int
	}{
		{"ofrep and flag-evaluation with the evaluator", []Item{flagdFormat, flagdEval, endpoint("ofrep", Needs{Router: true}), endpoint("flag-evaluation", Needs{Router: true})}, 0, 0},
		{"flag-sync alone", []Item{flagdFormat, endpoint("flag-sync", Needs{})}, 0, 0},
		{"flag-sync with an evaluator nothing uses", []Item{flagdFormat, flagdEval, endpoint("flag-sync", Needs{})}, 0, 1},
		{"flag-evaluation without an evaluator", []Item{flagdFormat, endpoint("flag-evaluation", Needs{Router: true})}, 1, 0},
		{"an endpoint of a format uses only that evaluator", []Item{flagdFormat, flagdEval, format("kv"), evaluator("kv", "kv"), endpoint("vendor", Needs{Format: "kv"})}, 0, 1},
		{"an evaluator is used when the router serves any", []Item{flagdFormat, flagdEval, endpoint("ofrep", Needs{Router: true})}, 0, 0},
		{"no endpoint at all leaves an evaluator unused", []Item{flagdFormat, flagdEval}, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings, err := Check(tt.items)
			if tt.wantProblems > 0 {
				var nerr *Error
				require.ErrorAs(t, err, &nerr)
				require.Len(t, nerr.Problems, tt.wantProblems)
				return
			}
			require.NoError(t, err)
			require.Len(t, warnings, tt.wantWarnings)
		})
	}
}

func TestParseProbe(t *testing.T) {
	items, err := ParseProbe([]byte(`[{"group":"service_endpoints","type":"ofrep","module":"m/ofrep","needs":{"router":true}},` +
		`{"group":"sync_providers","type":"file","module":"m/file","schemes":["file:"]}]`))
	require.NoError(t, err)
	require.Equal(t, []Item{
		{Group: groupEndpoint, Type: "ofrep", Module: "m/ofrep", Needs: &Needs{Router: true}},
		{Group: groupSync, Type: "file", Module: "m/file", Schemes: []string{"file:"}},
	}, items)

	_, err = ParseProbe([]byte("not json"))
	require.Error(t, err)
}
