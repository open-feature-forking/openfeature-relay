package flagd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	flagdEvaluator "github.com/open-feature-forking/openfeature-relay/evaluator/flagd"
)

var updateTypedGolden = flag.Bool("update-typed", false, "rewrite testdata/typed_characterization.golden from the current behaviour")

const typedGoldenFile = "testdata/typed_characterization.golden"

// typedFlags holds one flag per resolution rule the typed methods have: static values of each type, targeting
// that matches, returns null (with and without a default variant), a disabled flag, a flag without a default
// variant, a variant of another type than the default, numbers outside int64 and outside 2^53, and a
// fractional split.
const typedFlags = `{
  "flags": {
    "static-bool": {
      "state": "ENABLED", "defaultVariant": "on", "variants": {"on": true, "off": false},
      "metadata": {"owner": "team", "weight": 2}
    },
    "static-string": {"state": "ENABLED", "defaultVariant": "a", "variants": {"a": "alpha", "b": "beta"}},
    "static-int": {"state": "ENABLED", "defaultVariant": "ten", "variants": {"one": 1, "ten": 10}},
    "static-float": {"state": "ENABLED", "defaultVariant": "half", "variants": {"half": 0.5, "quarter": 0.25}},
    "static-object": {
      "state": "ENABLED", "defaultVariant": "o", "variants": {"o": {"k": 1, "n": {"x": true}}, "p": {}}
    },
    "targeting-match": {
      "state": "ENABLED", "defaultVariant": "off", "variants": {"on": true, "off": false},
      "targeting": {"if": [{"==": [{"var": "email"}, "a@b.c"]}, "on", "off"]}
    },
    "targeting-null-with-default": {
      "state": "ENABLED", "defaultVariant": "off", "variants": {"on": true, "off": false},
      "targeting": {"if": [{"==": [{"var": "email"}, "never"]}, "on", null]}
    },
    "targeting-null-no-default": {
      "state": "ENABLED", "variants": {"on": true, "off": false},
      "targeting": {"if": [{"==": [{"var": "email"}, "never"]}, "on", null]}
    },
    "targeting-variant-no-default": {
      "state": "ENABLED", "variants": {"on": true, "off": false},
      "targeting": {"if": [{"==": [{"var": "email"}, "a@b.c"]}, "on", null]}
    },
    "disabled": {"state": "DISABLED", "defaultVariant": "on", "variants": {"on": true, "off": false}},
    "no-default": {"state": "ENABLED", "variants": {"on": true, "off": false}},
    "type-mismatch": {
      "state": "ENABLED", "defaultVariant": "text", "variants": {"text": "hello", "number": 42},
      "targeting": {"if": [{"==": [{"var": "email"}, "a@b.c"]}, "number", "text"]}
    },
    "outside-int64": {"state": "ENABLED", "defaultVariant": "huge", "variants": {"huge": 10000000000000000000}},
    "outside-2-53": {"state": "ENABLED", "defaultVariant": "big", "variants": {"big": 9007199254740993}},
    "fractional": {
      "state": "ENABLED", "defaultVariant": "red", "variants": {"red": "red", "green": "green", "blue": "blue"},
      "targeting": {"fractional": [["red", 50], ["green", 25], ["blue", 25]]}
    }
  }
}`

type typedContext struct {
	name    string
	context map[string]any
}

var typedContexts = []typedContext{
	{"none", nil},
	{"match", map[string]any{"email": "a@b.c", "targetingKey": "user-1"}},
	{"other", map[string]any{"email": "x@y.z", "targetingKey": "user-2"}},
}

// typedResolution renders everything a typed call reports.
func typedResolution(value any, variant, reason string, metadata map[string]any, err error) string {
	return fmt.Sprintf("value=%#v variant=%q reason=%s err=%v metadata=%v", value, variant, reason, err, metadata)
}

func TestTypedResolutionCharacterization(t *testing.T) {
	var out strings.Builder
	for _, strict := range []bool{false, true} {
		var opts []flagdEvaluator.JSONEvaluatorOption
		if strict {
			opts = append(opts, flagdEvaluator.WithStrictNumericRange())
		}
		log := logger.NewLogger(nil, false)
		st, err := store.NewStore(log, []string{"characterization"})
		if err != nil {
			t.Fatal(err)
		}
		ev := flagdEvaluator.NewJSON(log, st, opts...)
		if err := ev.SetState(sync.DataSync{FlagData: typedFlags, Source: "characterization"}); err != nil {
			t.Fatal(err)
		}

		for _, protoVersion := range []bool{false, true} {
			ctx := context.Background()
			if protoVersion {
				ctx = context.WithValue(ctx, evaluator.ProtoVersionKey, true)
			}
			fmt.Fprintf(&out, "## strictNumericRange=%v protoVersion=%v\n", strict, protoVersion)

			var config struct {
				Flags map[string]json.RawMessage `json:"flags"`
			}
			if err := json.Unmarshal([]byte(typedFlags), &config); err != nil {
				t.Fatal(err)
			}
			keys := make([]string, 0, len(config.Flags))
			for key := range config.Flags {
				keys = append(keys, key)
			}
			sort.Strings(keys)

			for _, key := range keys {
				for _, c := range typedContexts {
					b, bVar, bReason, bMeta, bErr := ev.ResolveBooleanValue(ctx, "char", key, c.context)
					fmt.Fprintf(&out, "%s ctx=%s bool    %s\n", key, c.name, typedResolution(b, bVar, bReason, bMeta, bErr))

					s, sVar, sReason, sMeta, sErr := ev.ResolveStringValue(ctx, "char", key, c.context)
					fmt.Fprintf(&out, "%s ctx=%s string  %s\n", key, c.name, typedResolution(s, sVar, sReason, sMeta, sErr))

					i, iVar, iReason, iMeta, iErr := ev.ResolveIntValue(ctx, "char", key, c.context)
					fmt.Fprintf(&out, "%s ctx=%s int     %s\n", key, c.name, typedResolution(i, iVar, iReason, iMeta, iErr))

					f, fVar, fReason, fMeta, fErr := ev.ResolveFloatValue(ctx, "char", key, c.context)
					fmt.Fprintf(&out, "%s ctx=%s float   %s\n", key, c.name, typedResolution(f, fVar, fReason, fMeta, fErr))

					o, oVar, oReason, oMeta, oErr := ev.ResolveObjectValue(ctx, "char", key, c.context)
					fmt.Fprintf(&out, "%s ctx=%s object  %s\n", key, c.name, typedResolution(o, oVar, oReason, oMeta, oErr))

					a := ev.ResolveAsAnyValue(ctx, "char", key, c.context)
					fmt.Fprintf(&out, "%s ctx=%s any     %s flagKey=%s\n", key, c.name, typedResolution(a.Value, a.Variant, a.Reason, a.Metadata, a.Error), a.FlagKey)
				}
			}

			for _, c := range typedContexts {
				values, metadata, err := ev.ResolveAllValues(ctx, "char", c.context)
				fmt.Fprintf(&out, "bulk ctx=%s err=%v setMetadata=%v count=%d\n", c.name, err, metadata, len(values))
				for _, v := range values {
					fmt.Fprintf(&out, "  %s %s flagKey=%s\n", v.FlagKey, typedResolution(v.Value, v.Variant, v.Reason, v.Metadata, v.Error), v.FlagKey)
				}
			}
		}
	}

	// A context the evaluator cannot marshal, which only in-process callers can build: its error text is part
	// of the contract today.
	log := logger.NewLogger(nil, false)
	st, err := store.NewStore(log, []string{"characterization"})
	if err != nil {
		t.Fatal(err)
	}
	ev := flagdEvaluator.NewJSON(log, st)
	if err := ev.SetState(sync.DataSync{FlagData: typedFlags, Source: "characterization"}); err != nil {
		t.Fatal(err)
	}
	nan := map[string]any{"email": math.NaN(), "targetingKey": "user-1"}
	out.WriteString("## context holding NaN\n")
	b, bVar, bReason, bMeta, bErr := ev.ResolveBooleanValue(context.Background(), "char", "targeting-match", nan)
	fmt.Fprintf(&out, "targeting-match bool    %s\n", typedResolution(b, bVar, bReason, bMeta, bErr))
	s, sVar, sReason, sMeta, sErr := ev.ResolveStringValue(context.Background(), "char", "fractional", nan)
	fmt.Fprintf(&out, "fractional string  %s\n", typedResolution(s, sVar, sReason, sMeta, sErr))
	i, iVar, iReason, iMeta, iErr := ev.ResolveIntValue(context.Background(), "char", "targeting-match", nan)
	fmt.Fprintf(&out, "targeting-match int     %s\n", typedResolution(i, iVar, iReason, iMeta, iErr))
	f, fVar, fReason, fMeta, fErr := ev.ResolveFloatValue(context.Background(), "char", "targeting-match", nan)
	fmt.Fprintf(&out, "targeting-match float   %s\n", typedResolution(f, fVar, fReason, fMeta, fErr))
	o, oVar, oReason, oMeta, oErr := ev.ResolveObjectValue(context.Background(), "char", "targeting-match", nan)
	fmt.Fprintf(&out, "targeting-match object  %s\n", typedResolution(o, oVar, oReason, oMeta, oErr))

	if *updateTypedGolden {
		if err := os.WriteFile(typedGoldenFile, []byte(out.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(typedGoldenFile)
	if err != nil {
		t.Fatalf("%v: record the current behaviour with `go test -run TestTypedResolutionCharacterization -update-typed`", err)
	}
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	if got := out.String(); got != string(want) {
		wantLines, gotLines := strings.Split(string(want), "\n"), strings.Split(got, "\n")
		for n := 0; n < len(wantLines) && n < len(gotLines); n++ {
			if wantLines[n] != gotLines[n] {
				t.Fatalf("typed resolutions changed at line %d\nwant: %s\n got: %s", n+1, wantLines[n], gotLines[n])
			}
		}
		t.Fatalf("typed resolutions changed: %d lines recorded, %d produced", len(wantLines), len(gotLines))
	}
}
