// Package keyvalue is an example configuration format: a toy vendor format that a source names as
// "keyvalue". It shows what an Extension Author's component module contains, and imports only the
// core packages a configuration format may (no evaluator, sync provider or telemetry).
//
// A flag configuration is {"environment": "<flag set>", "flags": {"<key>": <value>}}. The format
// keeps each one as a document of the environment and rejects a delivery whose flags are not an
// object or whose values are null, naming the faulty key.
//
// A real extension keeps the tests that run it in a distribution in a separate module, so that its
// own module graph stays free of telemetry and transport dependencies; this example keeps them
// next to the format only to stay small.
package keyvalue

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
)

// Type is the name by which a source selects this configuration format.
const Type = "keyvalue"

// Factory creates the configuration format; a distribution registers it next to flagd's own.
type Factory struct{}

var _ configformat.Factory = Factory{}

// Type implements configformat.Factory.
func (Factory) Type() string { return Type }

// Create implements configformat.Factory from the store and logger a distribution injects.
func (Factory) Create(s configformat.Settings) (configformat.ConfigurationFormat, error) {
	if s.Store == nil {
		return nil, fmt.Errorf("the %s configuration format needs a store", Type)
	}
	return &Format{store: s.Store}, nil
}

// Format is the keyvalue configuration format.
type Format struct {
	store *store.Store
}

var _ configformat.ConfigurationFormat = (*Format)(nil)

// Type implements configformat.ConfigurationFormat.
func (*Format) Type() string { return Type }

// Accept implements configformat.ConfigurationFormat.
func (f *Format) Accept(_ context.Context, in configformat.Input) (configformat.Result, error) {
	var raw struct {
		Environment string          `json:"environment"`
		Flags       json.RawMessage `json:"flags"`
	}
	if err := json.Unmarshal([]byte(in.Data), &raw); err != nil {
		return configformat.Result{}, reject(in.Source, "document", err.Error())
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw.Flags, &values); err != nil || values == nil {
		return configformat.Result{}, reject(in.Source, "flags", "flags must be an object")
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	current := make(map[string]string, len(values))
	h := sha256.New()
	for _, key := range keys {
		value := bytes.TrimSpace(values[key])
		if string(value) == "null" {
			return configformat.Result{}, reject(in.Source, key, "value must not be null")
		}
		current[key] = string(value)
		fmt.Fprintf(h, "%q=%s;", key, value)
	}

	var previous map[string]string
	for _, d := range f.store.Documents(Type, in.Source) {
		if d.FlagSet == raw.Environment {
			previous, _ = d.Value.(map[string]string)
		}
	}

	report := configformat.DocumentReport{FlagSet: raw.Environment, FlagKeys: keys, Version: hex.EncodeToString(h.Sum(nil))}
	for _, key := range keys {
		if old, ok := previous[key]; !ok {
			report.Added = append(report.Added, key)
		} else if old != current[key] {
			report.Updated = append(report.Updated, key)
		}
	}
	removed := make([]string, 0)
	for key := range previous {
		if _, ok := current[key]; !ok {
			removed = append(removed, key)
		}
	}
	sort.Strings(removed)
	report.Removed = removed

	err := f.store.Apply(store.ApplyRequest{
		Format: Type, Source: in.Source, Incremental: in.IncrementalUpdate,
		Documents: []store.Document{{
			Format: Type, Source: in.Source, FlagSet: raw.Environment, Version: report.Version,
			FlagKeys: keys, Value: current,
		}},
	})
	if err != nil {
		return configformat.Result{}, err
	}
	return configformat.Result{Documents: []configformat.DocumentReport{report}}, nil
}

func reject(source, entry, reason string) error {
	return configformat.Rejection{Source: source, Entry: entry, Reason: reason}
}
