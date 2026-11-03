// Package evaluator is the evaluator paired with the example configuration format: together they make the
// module an engine module, a configuration format and its evaluator in one component module (FR-048). It
// imports only the core packages an evaluator may.
//
// Every value of a keyvalue document resolves to itself, with the reason STATIC. Two key prefixes show how the
// attributes of a request shape what an evaluator returns: a key starting with "server." resolves to no value
// for a request whose "audience" attribute is "client" (the caller's default stands), and a key starting with
// "client." resolves to no value for anyone else. Between flag sets of one source, a single key resolves from
// the first flag set in name order.
package evaluator

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
)

// FactoryType names the evaluator.
const FactoryType = "keyvalue"

// configurationFormat is the format whose documents the evaluator resolves. It is the type of the keyvalue format of
// the module root; the evaluator does not import it, so that the root can offer both factories of the engine.
const configurationFormat = "keyvalue"

// Factory creates the evaluator; a distribution registers it next to the configuration format's factory.
type Factory struct{}

var _ evaluator.Factory = Factory{}

// Type implements evaluator.Factory.
func (Factory) Type() string { return FactoryType }

// ConfigurationFormat implements evaluator.Factory.
func (Factory) ConfigurationFormat() string { return configurationFormat }

// Create implements evaluator.Factory from the store a distribution injects.
func (Factory) Create(s evaluator.Settings) (evaluator.Evaluator, error) {
	if s.Store == nil {
		return nil, errors.New("the keyvalue evaluator needs a store")
	}
	return &Evaluator{store: s.Store}, nil
}

// Evaluator resolves the keys of the keyvalue documents in a store.
type Evaluator struct {
	store *store.Store
}

var _ evaluator.Evaluator = (*Evaluator)(nil)

type hit struct {
	flagSet  string
	key      string
	priority int
	raw      string
	// hidden marks an entry the request's attributes keep from resolving to a value.
	hidden bool
}

// Evaluate implements evaluator.Evaluator. Every read goes through one view of the store.
func (e *Evaluator) Evaluate(_ context.Context, req evaluator.Request, dst []evaluator.Result) ([]evaluator.Result, model.Metadata, error) {
	meta := req.Selector.ToMetadata()
	hits := e.collect(e.store.View(), req)

	if req.Bulk {
		type pair struct{ flagSet, key string }
		best := map[pair]hit{}
		for _, h := range hits {
			if cur, ok := best[pair{h.flagSet, h.key}]; !ok || h.priority > cur.priority {
				best[pair{h.flagSet, h.key}] = h
			}
		}
		ordered := make([]hit, 0, len(best))
		for _, h := range best {
			ordered = append(ordered, h)
		}
		sort.Slice(ordered, func(i, j int) bool {
			if ordered[i].flagSet != ordered[j].flagSet {
				return ordered[i].flagSet < ordered[j].flagSet
			}
			return ordered[i].key < ordered[j].key
		})
		for _, h := range ordered {
			dst = append(dst, resolve(h))
		}
		return dst, meta, nil
	}

	keys := req.FlagKeys
	if keys == nil {
		keys = []string{req.FlagKey}
	}
	for _, key := range keys {
		var found *hit
		for i := range hits {
			if hits[i].key == key && (found == nil || hits[i].priority > found.priority) {
				found = &hits[i]
			}
		}
		if found == nil {
			dst = append(dst, evaluator.Result{
				FlagKey: key, Reason: model.ErrorReason, ErrorCode: model.FlagNotFoundErrorCode,
				ErrorMessage: model.GetErrorMessage(model.FlagNotFoundErrorCode), Metadata: meta,
			})
			continue
		}
		dst = append(dst, resolve(*found))
	}
	return dst, meta, nil
}

// collect lists the entries of the keyvalue documents the request's selector names, for the keys it asks
// for and its attributes allow, in source order and, within a source, flag set order.
func (e *Evaluator) collect(view store.View, req evaluator.Request) []hit {
	flagSet, named := req.Selector.DocumentFlagSet()
	onlySource := req.Selector.Source()
	var wanted map[string]struct{}
	if !req.Bulk || req.FlagKeys != nil {
		wanted = map[string]struct{}{}
		if req.FlagKeys == nil {
			wanted[req.FlagKey] = struct{}{}
		}
		for _, key := range req.FlagKeys {
			wanted[key] = struct{}{}
		}
	}

	var hits []hit
	for priority, source := range e.store.Sources() {
		if onlySource != "" && source != onlySource {
			continue
		}
		for _, doc := range view.Documents(configurationFormat, source) {
			if named && doc.FlagSet != flagSet {
				continue
			}
			values, _ := doc.Value.(map[string]string)
			for _, key := range doc.FlagKeys {
				if _, ok := wanted[key]; wanted != nil && !ok {
					continue
				}
				hits = append(hits, hit{
					flagSet: doc.FlagSet, key: key, priority: priority, raw: values[key], hidden: !visible(key, req.Attributes),
				})
			}
		}
	}
	return hits
}

func visible(key string, attributes map[string]any) bool {
	client := attributes["audience"] == "client"
	switch {
	case strings.HasPrefix(key, "server."):
		return !client
	case strings.HasPrefix(key, "client."):
		return client
	}
	return true
}

func resolve(h hit) evaluator.Result {
	result := evaluator.Result{FlagKey: h.key, FlagSet: h.flagSet, Variant: "value", Reason: model.StaticReason}
	if h.flagSet != "" {
		result.Metadata = model.Metadata{"flagSetId": h.flagSet}
	}
	if h.hidden {
		result.Variant, result.Reason = "", model.DefaultReason
		return result
	}
	var value any
	if err := json.Unmarshal([]byte(h.raw), &value); err != nil {
		result.Variant, result.Reason = "", model.ErrorReason
		result.ErrorCode = model.ParseErrorCode
		result.ErrorMessage = model.GetErrorMessage(model.ParseErrorCode)
		return result
	}
	result.Value, result.HasValue = value, true
	return result
}
