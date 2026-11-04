package flagsmith

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"sort"
	"strings"
	"time"

	"github.com/Flagsmith/flagsmith-go-client/v5/flagengine/engine_eval"
	"github.com/Flagsmith/flagsmith-go-client/v5/flagengine/environments"
	"github.com/Flagsmith/flagsmith-go-client/v5/flagengine/features"
	"github.com/Flagsmith/flagsmith-go-client/v5/flagengine/identities"
	"github.com/Flagsmith/flagsmith-go-client/v5/flagengine/projects"
	"github.com/Flagsmith/flagsmith-go-client/v5/flagengine/segments"
	"github.com/itlightning/dateparse"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
)

// Type is the name by which a source selects this configuration format, and the name of the paired evaluator.
const Type = "flagsmith"

// Factory creates the configuration format.
type Factory struct{}

var _ configformat.Factory = Factory{}

// Type implements configformat.Factory.
func (Factory) Type() string { return Type }

// Create implements configformat.Factory from the store a distribution injects.
func (Factory) Create(s configformat.Settings) (configformat.ConfigurationFormat, error) {
	if s.Store == nil {
		return nil, fmt.Errorf("the %s configuration format needs a store", Type)
	}
	return &Format{store: s.Store}, nil
}

// document is what the store keeps for one environment: the engine's evaluation context, mapped once. It is read-only
// once stored.
type document struct {
	Context      engine_eval.EngineEvaluationContext
	HideDisabled bool
	UpdatedAt    time.Time
	// fingerprints identifies, per feature, everything that can change how it evaluates, so that the next delivery
	// can report what changed.
	fingerprints map[string]string
}

// Format is the Flagsmith configuration format.
type Format struct {
	store *store.Store
}

var _ configformat.ConfigurationFormat = (*Format)(nil)

// Type implements configformat.ConfigurationFormat.
func (*Format) Type() string { return Type }

// Accept implements configformat.ConfigurationFormat.
func (f *Format) Accept(_ context.Context, in configformat.Input) (res configformat.Result, err error) {
	defer func() {
		if v := recover(); v != nil {
			res, err = configformat.Result{}, reject(in.Source, "document", fmt.Sprintf("the environment document is malformed: %v", v))
		}
	}()

	doc, err := decode(in.Source, in.Data)
	if err != nil {
		return configformat.Result{}, err
	}

	keys := make([]string, 0, len(doc.Context.Features))
	for name := range doc.Context.Features {
		keys = append(keys, name)
	}
	sort.Strings(keys)

	var previous *document
	for _, d := range f.store.Documents(Type, in.Source) {
		if d.FlagSet == doc.Context.Environment.Key {
			previous, _ = d.Value.(*document)
		}
	}

	sum := sha256.Sum256([]byte(in.Data))
	report := configformat.DocumentReport{
		FlagSet: doc.Context.Environment.Key, FlagKeys: keys, Version: hex.EncodeToString(sum[:]),
		Removed: []string{},
	}
	for _, key := range keys {
		switch old, ok := previous.fingerprint(key); {
		case !ok:
			report.Added = append(report.Added, key)
		case old != doc.fingerprints[key]:
			report.Updated = append(report.Updated, key)
		}
	}
	if previous != nil {
		for key := range previous.fingerprints {
			if _, ok := doc.fingerprints[key]; !ok {
				report.Removed = append(report.Removed, key)
			}
		}
		sort.Strings(report.Removed)
	}

	err = f.store.Apply(store.ApplyRequest{
		Format: Type, Source: in.Source, Incremental: in.IncrementalUpdate,
		Documents: []store.Document{{
			Format: Type, Source: in.Source, FlagSet: report.FlagSet, Version: report.Version, FlagKeys: keys, Value: doc,
		}},
	})
	if err != nil {
		return configformat.Result{}, err
	}
	return configformat.Result{Documents: []configformat.DocumentReport{report}}, nil
}

func (d *document) fingerprint(key string) (string, bool) {
	if d == nil {
		return "", false
	}
	fp, ok := d.fingerprints[key]
	return fp, ok
}

func reject(source, entry, reason string) error {
	return configformat.Rejection{Source: source, Entry: entry, Reason: reason}
}

type rawProject struct {
	HideDisabledFlags bool              `json:"hide_disabled_flags"`
	Segments          []json.RawMessage `json:"segments"`
}

// decode reads an environment document entry by entry, so that a faulty one is named.
func decode(source, data string) (*document, error) {
	var raw struct {
		ID                int               `json:"id"`
		Name              string            `json:"name"`
		APIKey            string            `json:"api_key"`
		UpdatedAt         json.RawMessage   `json:"updated_at"`
		Project           *rawProject       `json:"project"`
		FeatureStates     []json.RawMessage `json:"feature_states"`
		IdentityOverrides []json.RawMessage `json:"identity_overrides"`
	}
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return nil, reject(source, "document", err.Error())
	}
	if strings.TrimSpace(raw.APIKey) == "" {
		return nil, reject(source, "api_key", "the environment document has no api_key, which names its flag set")
	}
	updatedAt, err := parseUpdatedAt(raw.UpdatedAt)
	if err != nil {
		return nil, reject(source, "updated_at", err.Error())
	}

	env := &environments.EnvironmentModel{ID: raw.ID, Name: raw.Name, APIKey: raw.APIKey, UpdatedAt: updatedAt}
	env.Project = &projects.ProjectModel{}
	if raw.Project != nil {
		env.Project.HideDisabledFlags = raw.Project.HideDisabledFlags
		for i, s := range raw.Project.Segments {
			var seg segments.SegmentModel
			if err := json.Unmarshal(s, &seg); err != nil {
				return nil, reject(source, entryName(s, "name", "segments", i), err.Error())
			}
			if err := checkFeatureStates(source, fmt.Sprintf("segment %q", seg.Name), seg.FeatureStates); err != nil {
				return nil, err
			}
			env.Project.Segments = append(env.Project.Segments, &seg)
		}
	}
	for i, s := range raw.FeatureStates {
		var fs features.FeatureStateModel
		if err := json.Unmarshal(s, &fs); err != nil {
			return nil, reject(source, entryName(s, "feature.name", "feature_states", i), err.Error())
		}
		if err := checkFeatureState(source, fmt.Sprintf("feature_states[%d]", i), &fs); err != nil {
			return nil, err
		}
		env.FeatureStates = append(env.FeatureStates, &fs)
	}
	for i, s := range raw.IdentityOverrides {
		var id identities.IdentityModel
		if err := json.Unmarshal(s, &id); err != nil {
			return nil, reject(source, entryName(s, "identifier", "identity_overrides", i), err.Error())
		}
		if err := checkFeatureStates(source, fmt.Sprintf("identity %q", id.Identifier), id.IdentityFeatures); err != nil {
			return nil, err
		}
		env.IdentityOverrides = append(env.IdentityOverrides, &id)
	}

	ec := engine_eval.MapEnvironmentDocumentToEvaluationContext(env)
	return &document{
		Context: ec, HideDisabled: env.Project.HideDisabledFlags, UpdatedAt: updatedAt, fingerprints: fingerprints(ec),
	}, nil
}

// parseUpdatedAt reads the timestamp the way Flagsmith's Python engine does: a zone is optional, and a missing one is
// UTC. An absent or null timestamp is the zero time.
func parseUpdatedAt(raw json.RawMessage) (time.Time, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return time.Time{}, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return time.Time{}, errors.New("updated_at must be a string")
	}
	if s == "" {
		return time.Time{}, nil
	}
	t, err := dateparse.ParseIn(s, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("updated_at %q is not a timestamp", s)
	}
	return t, nil
}

// entryName names a faulty entry by the string at path, or by its position in its list.
func entryName(raw json.RawMessage, path, list string, index int) string {
	var v any
	if json.Unmarshal(raw, &v) == nil {
		for _, part := range strings.Split(path, ".") {
			obj, ok := v.(map[string]any)
			if !ok {
				v = nil
				break
			}
			v = obj[part]
		}
		if name, ok := v.(string); ok && name != "" {
			return name
		}
	}
	return fmt.Sprintf("%s[%d]", list, index)
}

// checkFeatureStates refuses the feature states the engine's mapping cannot read: one without a feature, or a
// multivariate value without its option.
func checkFeatureStates(source, where string, states []*features.FeatureStateModel) error {
	for i, fs := range states {
		if err := checkFeatureState(source, fmt.Sprintf("%s[%d]", where, i), fs); err != nil {
			return err
		}
	}
	return nil
}

// checkFeatureState names a faulty feature state by its feature, or by position when it has none.
func checkFeatureState(source, position string, fs *features.FeatureStateModel) error {
	if fs.Feature == nil || fs.Feature.Name == "" {
		return reject(source, position, "the feature state names no feature")
	}
	for _, mv := range fs.MultivariateFeatureStateValues {
		if mv == nil || mv.MultivariateFeatureOption == nil {
			return reject(source, fs.Feature.Name, "a multivariate value has no multivariate_feature_option")
		}
	}
	return nil
}

// fingerprints hashes, for each feature, its own definition and every segment override of it together with the rules of
// the segment that carries the override.
func fingerprints(ec engine_eval.EngineEvaluationContext) map[string]string {
	type override struct {
		segment string
		feature engine_eval.FeatureContext
	}
	byFeature := map[string][]override{}
	segmentKeys := make([]string, 0, len(ec.Segments))
	for key := range ec.Segments {
		segmentKeys = append(segmentKeys, key)
	}
	sort.Strings(segmentKeys)
	rules := make(map[string][]byte, len(segmentKeys))
	for _, key := range segmentKeys {
		seg := ec.Segments[key]
		rules[key], _ = json.Marshal(seg.Rules)
		for _, o := range seg.Overrides {
			byFeature[o.Name] = append(byFeature[o.Name], override{segment: key, feature: o})
		}
	}

	out := make(map[string]string, len(ec.Features))
	for name, fc := range ec.Features {
		h := sha256.New()
		writeFeature(h, fc)
		for _, o := range byFeature[name] {
			fmt.Fprintf(h, "segment %q %s;", o.segment, rules[o.segment])
			writeFeature(h, o.feature)
		}
		out[name] = hex.EncodeToString(h.Sum(nil))
	}
	return out
}

func writeFeature(h hash.Hash, fc engine_eval.FeatureContext) {
	value, _ := json.Marshal(fc.Value)
	fmt.Fprintf(h, "%q %t %q %s;", fc.Name, fc.Enabled, fc.Key, value)
	if fc.Priority != nil {
		fmt.Fprintf(h, "priority %g;", *fc.Priority)
	}
	for _, v := range fc.Variants {
		vv, _ := json.Marshal(v.Value)
		fmt.Fprintf(h, "variant %q %g %s %s;", v.Key, v.Weight, v.Priority.String(), vv)
	}
}
