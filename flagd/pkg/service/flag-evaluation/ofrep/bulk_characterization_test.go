package ofrep

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature/flagd/flagd/pkg/evaluation"
	"github.com/stretchr/testify/require"
)

var updateBulkGolden = flag.Bool("update-bulk", false, "rewrite testdata/bulk_characterization.golden from the current behaviour")

const bulkGoldenFile = "testdata/bulk_characterization.golden"

// bulkMixedFlags: mixed types, a disabled flag, flags without a default variant (with and without targeting that
// picks a variant), a variant of another type than the default, and a default that is neither bool, string,
// number nor object.
const bulkMixedFlags = `{
  "metadata": {"scope": "mixed"},
  "flags": {
    "bool": {"state": "ENABLED", "defaultVariant": "on", "variants": {"on": true, "off": false}},
    "string": {"state": "ENABLED", "defaultVariant": "a", "variants": {"a": "alpha"}},
    "number": {"state": "ENABLED", "defaultVariant": "ten", "variants": {"ten": 10}},
    "object": {"state": "ENABLED", "defaultVariant": "o", "variants": {"o": {"k": 1}}},
    "disabled": {"state": "DISABLED", "defaultVariant": "on", "variants": {"on": true, "off": false}},
    "no-default": {"state": "ENABLED", "variants": {"on": true, "off": false}},
    "no-default-targeting-variant": {
      "state": "ENABLED", "variants": {"on": true, "off": false},
      "targeting": {"if": [{"==": [{"var": "email"}, "a@b.c"]}, "on", null]}
    },
    "type-mismatch": {
      "state": "ENABLED", "defaultVariant": "text", "variants": {"text": "hello", "number": 42},
      "targeting": {"if": [{"==": [{"var": "email"}, "a@b.c"]}, "number", "text"]}
    },
    "array-default": {"state": "ENABLED", "defaultVariant": "list", "variants": {"list": [1, 2]}}
  }
}`

func bulkSetFlags(set, value string) string {
	return fmt.Sprintf(`{
  "metadata": {"flagSetId": %q},
  "flags": {
    "shared": {"state": "ENABLED", "defaultVariant": "v", "variants": {"v": %q}},
    "only-%s": {"state": "ENABLED", "defaultVariant": "v", "variants": {"v": true}}
  }
}`, set, value, set)
}

func TestBulkResponseCharacterization(t *testing.T) {
	setups := []struct {
		name      string
		payloads  []sync.DataSync
		selectors []string
	}{
		{
			name:      "mixed",
			payloads:  []sync.DataSync{{FlagData: bulkMixedFlags, Source: "src"}},
			selectors: []string{""},
		},
		{
			name: "one-key-in-two-flag-sets-of-one-source",
			payloads: []sync.DataSync{
				{FlagData: bulkSetFlags("set-a", "from-a"), Source: "src", IncrementalUpdates: true},
				{FlagData: bulkSetFlags("set-b", "from-b"), Source: "src", IncrementalUpdates: true},
			},
			selectors: []string{"", "flagSetId=set-a", "flagSetId=set-b"},
		},
		{
			name: "one-key-in-two-sources",
			payloads: []sync.DataSync{
				{FlagData: bulkSetFlags("set-a", "from-one"), Source: "one"},
				{FlagData: bulkSetFlags("set-b", "from-two"), Source: "two"},
			},
			selectors: []string{"", "source=one", "source=two"},
		},
	}

	var out strings.Builder
	for _, setup := range setups {
		log := logger.NewLogger(nil, false)
		var sources []string
		for _, p := range setup.payloads {
			sources = append(sources, p.Source)
		}
		st, err := store.NewStore(log, sources)
		require.NoError(t, err)
		ev := evaluation.NewEvaluator(log, st)
		for _, p := range setup.payloads {
			require.NoError(t, ev.SetState(p))
		}

		h := NewOfrepHandler(log, ev, nil, nil, nil, "characterization", SSEConfig{}, newCompression(t))

		for _, selector := range setup.selectors {
			for _, body := range []string{``, `{"context": {"email": "a@b.c"}}`} {
				fmt.Fprintf(&out, "## %s selector=%q body=%s\n", setup.name, selector, body)

				req := httptest.NewRequest(http.MethodPost, "/ofrep/v1/evaluate/flags", bytes.NewReader([]byte(body)))
				req.Header.Set("Content-Type", "application/json")
				if selector != "" {
					req.Header.Set("Flagd-Selector", selector)
				}
				res := httptest.NewRecorder()
				h.ServeHTTP(res, req)

				var decoded any
				if err := json.Unmarshal(res.Body.Bytes(), &decoded); err != nil {
					t.Fatalf("%s: response is not JSON: %v: %s", setup.name, err, res.Body.String())
				}
				pretty, err := json.MarshalIndent(decoded, "", "  ")
				require.NoError(t, err)
				fmt.Fprintf(&out, "status=%d\n%s\n", res.Code, pretty)
			}
		}
	}

	if *updateBulkGolden {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(bulkGoldenFile, []byte(out.String()), 0o600))
		return
	}
	want, err := os.ReadFile(bulkGoldenFile)
	if err != nil {
		t.Fatalf("%v: record the current behaviour with `go test -run TestBulkResponseCharacterization -update-bulk`", err)
	}
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	if got := out.String(); got != string(want) {
		wantLines, gotLines := strings.Split(string(want), "\n"), strings.Split(got, "\n")
		for n := 0; n < len(wantLines) && n < len(gotLines); n++ {
			if wantLines[n] != gotLines[n] {
				t.Fatalf("bulk responses changed at line %d\nwant: %s\n got: %s", n+1, wantLines[n], gotLines[n])
			}
		}
		t.Fatalf("bulk responses changed: %d lines recorded, %d produced", len(wantLines), len(gotLines))
	}
}
