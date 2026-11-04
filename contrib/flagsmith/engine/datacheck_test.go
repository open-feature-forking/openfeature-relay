package flagsmith

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Flagsmith/flagsmith-go-client/v5/flagengine/engine_eval"
	"github.com/stretchr/testify/require"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
)

// testDataEnv names the directory with the test cases of Flagsmith's shared engine test data. The data carries no
// licence, so it is not part of this repository: `make flagsmith-engine-data-check` reads it from a clone outside it.
const testDataEnv = "FLAGSMITH_ENGINE_TEST_DATA"

// TestSharedEngineTestData runs the evaluator over every test case of the shared engine test data that can be asked
// through an evaluation context: the identity key of a case must be the one the module derives from the identifier. The
// cases come with the environment already mapped, so the format is not part of this check.
func TestSharedEngineTestData(t *testing.T) {
	dir := os.Getenv(testDataEnv)
	if dir == "" {
		t.Skipf("set %s to the test_cases directory of Flagsmith's engine-test-data", testDataEnv)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.json*"))
	require.NoError(t, err)
	require.NotEmpty(t, files, "no test cases in %s", dir)

	checked, skipped := 0, 0
	for _, file := range files {
		t.Run(strings.TrimSuffix(filepath.Base(file), filepath.Ext(file)), func(t *testing.T) {
			raw, err := os.ReadFile(file)
			require.NoError(t, err)
			if filepath.Ext(file) == ".jsonc" {
				raw = stripJSONC(raw)
			}
			var tc struct {
				Context engine_eval.EngineEvaluationContext `json:"context"`
				Result  engine_eval.EvaluationResult        `json:"result"`
			}
			require.NoError(t, json.Unmarshal(raw, &tc))

			var request map[string]any
			if id := tc.Context.Identity; id != nil {
				if id.Key != "" && id.Key != tc.Context.Environment.Key+"_"+id.Identifier {
					skipped++
					t.Skipf("the identity key %q is not the one derived from the identifier %q", id.Key, id.Identifier)
				}
				if _, clash := id.Traits[targetingKey]; clash || id.Identifier == "" {
					skipped++
					t.Skip("the case cannot be written as an evaluation context")
				}
				request = map[string]any{targetingKey: id.Identifier}
				for k, v := range id.Traits {
					request[k] = v
				}
			}
			ec := tc.Context
			ec.Identity = nil

			st, err := store.NewStore(logger.NewLogger(nil, false), []string{"data"})
			require.NoError(t, err)
			require.NoError(t, st.Apply(store.ApplyRequest{
				Format: Type, Source: "data",
				Documents: []store.Document{{Format: Type, Source: "data", FlagSet: ec.Environment.Key, Value: &document{Context: ec}}},
			}))
			ev, err := EvaluatorFactory{}.Create(evaluator.Settings{Store: st})
			require.NoError(t, err)
			results, _, err := ev.Evaluate(context.Background(), evaluator.Request{Bulk: true, Context: request}, nil)
			require.NoError(t, err)
			got := map[string]evaluator.Result{}
			for _, r := range results {
				got[r.FlagKey] = r
			}

			require.Len(t, got, len(tc.Result.Flags))
			for name, want := range tc.Result.Flags {
				r, ok := got[name]
				require.True(t, ok, name)
				switch {
				case !want.Enabled:
					require.Equal(t, model.DisabledReason, r.Reason, name)
				case want.Value == nil:
					require.Equal(t, model.DefaultReason, r.Reason, name)
				default:
					require.True(t, r.HasValue, name)
					require.Equal(t, want.Value, r.Value, name)
					require.Equal(t, want.Variant, r.Variant, name)
					require.Equal(t, openFeatureReason(want.Reason, model.Metadata{}), r.Reason, name)
				}
			}
			checked++
		})
	}
	t.Logf("%d test cases checked, %d skipped", checked, skipped)
}

var trailingComma = regexp.MustCompile(`,(\s*[}\]])`)

// stripJSONC removes the comments and the trailing commas of a JSONC document.
func stripJSONC(in []byte) []byte {
	var out bytes.Buffer
	inString := false
	for i := 0; i < len(in); i++ {
		c := in[i]
		switch {
		case inString:
			out.WriteByte(c)
			if c == '\\' && i+1 < len(in) {
				i++
				out.WriteByte(in[i])
			} else if c == '"' {
				inString = false
			}
		case c == '"':
			inString = true
			out.WriteByte(c)
		case c == '/' && i+1 < len(in) && in[i+1] == '/':
			for i < len(in) && in[i] != '\n' {
				i++
			}
			out.WriteByte('\n')
		case c == '/' && i+1 < len(in) && in[i+1] == '*':
			i += 2
			for i+1 < len(in) && !(in[i] == '*' && in[i+1] == '/') {
				i++
			}
			i++
		default:
			out.WriteByte(c)
		}
	}
	return trailingComma.ReplaceAll(out.Bytes(), []byte("$1"))
}

func TestStripJSONC(t *testing.T) {
	in := "{\n// a comment\n\"url\": \"http://x/*y*/\", /* block */ \"list\": [1, 2,],\n}\n"
	var v map[string]any
	require.NoError(t, json.Unmarshal(stripJSONC([]byte(in)), &v))
	require.Equal(t, "http://x/*y*/", v["url"])
	require.Equal(t, []any{1.0, 2.0}, v["list"])
}
