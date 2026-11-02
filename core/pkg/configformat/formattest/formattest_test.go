package formattest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/open-feature/flagd/core/pkg/configformat"
	"github.com/open-feature/flagd/core/pkg/store"
)

// kvFormat is a key-value format: {"environment": "<flag set>", "flags": {"<key>": <value>}}.
// Keys starting with "_" are dropped. The break* fields turn it into a format that violates the contract.
type kvFormat struct {
	st *store.Store

	acceptInvalid  bool
	plainRejection bool
	mutateOnReject bool
	skipAdded      bool
	unstable       *int
}

type kvFactory struct{ kvFormat }

func (kvFactory) Type() string { return "kv" }

func (f kvFactory) Create(s configformat.Settings) (configformat.ConfigurationFormat, error) {
	k := f.kvFormat
	k.st = s.Store
	return &k, nil
}

func (*kvFormat) Type() string { return "kv" }

func (k *kvFormat) Accept(_ context.Context, in configformat.Input) (configformat.Result, error) {
	var raw struct {
		Environment string         `json:"environment"`
		Flags       map[string]any `json:"flags"`
	}
	if err := json.Unmarshal([]byte(in.Data), &raw); err != nil {
		return configformat.Result{}, k.reject(in.Source, "flags", err.Error())
	}

	var res configformat.Result
	values := map[string]string{}
	for key, v := range raw.Flags {
		if strings.HasPrefix(key, "_") {
			res.Dropped = append(res.Dropped, configformat.Rejection{Source: in.Source, Entry: key, Reason: "reserved"})
			continue
		}
		if v == nil && !k.acceptInvalid {
			if k.mutateOnReject {
				_ = k.st.Apply(store.ApplyRequest{Format: "kv", Source: in.Source, Documents: []store.Document{
					{Format: "kv", Source: in.Source, FlagSet: "leak", Version: "x"},
				}})
			}
			return configformat.Result{}, k.reject(in.Source, key, "value is null")
		}
		b, _ := json.Marshal(v)
		values[key] = string(b)
	}

	var old map[string]string
	for _, d := range k.st.Documents("kv", in.Source) {
		if d.FlagSet == raw.Environment {
			old, _ = d.Value.(map[string]string)
		}
	}

	rep := configformat.DocumentReport{FlagSet: raw.Environment}
	h := sha256.New()
	for _, key := range sortedKeys(values) {
		rep.FlagKeys = append(rep.FlagKeys, key)
		fmt.Fprintf(h, "%s=%s;", key, values[key])
		if prev, ok := old[key]; !ok {
			if !k.skipAdded {
				rep.Added = append(rep.Added, key)
			}
		} else if prev != values[key] {
			rep.Updated = append(rep.Updated, key)
		}
	}
	for _, key := range sortedKeys(old) {
		if _, ok := values[key]; !ok {
			rep.Removed = append(rep.Removed, key)
		}
	}
	rep.Version = hex.EncodeToString(h.Sum(nil))
	if k.unstable != nil {
		*k.unstable++
		rep.Version = fmt.Sprint(*k.unstable)
	}
	res.Documents = []configformat.DocumentReport{rep}

	err := k.st.Apply(store.ApplyRequest{
		Format: "kv", Source: in.Source, Incremental: in.IncrementalUpdate,
		Documents: []store.Document{{Format: "kv", Source: in.Source, FlagSet: raw.Environment, Version: rep.Version, FlagKeys: rep.FlagKeys, Value: values}},
	})
	return res, err
}

func (k *kvFormat) reject(source, entry, reason string) error {
	if k.plainRejection {
		return fmt.Errorf("%s: %s", entry, reason)
	}
	return configformat.Rejection{Source: source, Entry: entry, Reason: reason}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var kvSamples = Samples{
	Valid:   Delivery{Data: `{"environment":"prod","flags":{"a":1,"b":2,"c":3}}`, Sets: []Set{{"prod", []string{"a", "b", "c"}}}},
	Changed: Change{Delivery: Delivery{Data: `{"environment":"prod","flags":{"a":1,"b":5,"d":4}}`, Sets: []Set{{"prod", []string{"a", "b", "d"}}}}, FlagSet: "prod", Added: []string{"d"}, Updated: []string{"b"}, Removed: []string{"c"}},
	Invalid: Faulty{Data: `{"environment":"prod","flags":{"a":null}}`, Entries: []string{"a"}},
	Incremental: &[2]Delivery{
		{Data: `{"environment":"prod","flags":{"a":1}}`, Sets: []Set{{"prod", []string{"a"}}}},
		{Data: `{"environment":"dev","flags":{"a":1}}`, Sets: []Set{{"dev", []string{"a"}}}},
	},
	Dropped: &Faulty{Data: `{"environment":"prod","flags":{"a":1,"_x":2}}`, Entries: []string{"_x"}},
}

func TestContractAcceptsConformingFormat(t *testing.T) {
	Run(t, kvFactory{}, kvSamples)
}

type fakeTB struct {
	testing.TB
	messages []string
}

func (f *fakeTB) Helper() {}
func (f *fakeTB) Errorf(format string, args ...any) {
	f.messages = append(f.messages, fmt.Sprintf(format, args...))
}

func TestContractReportsExpectedAndObserved(t *testing.T) {
	counter := 0
	tests := []struct {
		name    string
		format  kvFormat
		wantMsg string
	}{
		{"accepts invalid", kvFormat{acceptInvalid: true}, "expected the invalid delivery to be rejected\nobserved: accepted"},
		{"unnamed rejection", kvFormat{plainRejection: true}, "expected a configformat.Rejection naming source and entry\nobserved: *errors.errorString"},
		{"mutates store on reject", kvFormat{mutateOnReject: true}, "documents after a rejected delivery\nexpected:"},
		{"wrong Added", kvFormat{skipAdded: true}, "keys added by the first delivery of flag set \"prod\"\nexpected: [a b c]\nobserved: []"},
		{"unstable version", kvFormat{unstable: &counter}, "version of flag set \"prod\" after the same input"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ft := &fakeTB{}
			samples := kvSamples
			if tt.format.plainRejection {
				// the plain error comes from a parse failure, so give it an unparsable payload
				samples.Invalid = Faulty{Data: `{`, Entries: []string{"flags"}}
			}
			Run(ft, kvFactory{tt.format}, samples)

			all := strings.Join(ft.messages, "\n---\n")
			if len(ft.messages) == 0 {
				t.Fatalf("the contract accepted a broken format")
			}
			if !strings.Contains(all, tt.wantMsg) {
				t.Fatalf("failure text lacks expected/observed detail\nwant to contain: %q\ngot: %s", tt.wantMsg, all)
			}
		})
	}
}
