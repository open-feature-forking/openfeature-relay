package flagsmith

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/Flagsmith/flagsmith-go-client/v5/flagengine"
	"github.com/Flagsmith/flagsmith-go-client/v5/flagengine/engine_eval"
	"github.com/Flagsmith/flagsmith-go-client/v5/trait"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
)

const (
	// CredentialAttribute is the request attribute that carries the kind of the client's credential. The credential
	// middleware sets it (UC-019 BR-004).
	CredentialAttribute = "flagsmith.credential"
	// CredentialClient is the kind of a client-side key. Under it, a project that hides disabled flags hides them.
	CredentialClient = "client"

	// SettingBooleanFromEnabled makes a boolean request resolve to the enabled state of the flag instead of its value.
	SettingBooleanFromEnabled = "booleanFromEnabled"

	targetingKey = "targetingKey"
)

// EvaluatorFactory creates the evaluator paired with the configuration format.
type EvaluatorFactory struct{}

var _ evaluator.Factory = EvaluatorFactory{}

// Type implements evaluator.Factory.
func (EvaluatorFactory) Type() string { return Type }

// ConfigurationFormat implements evaluator.Factory.
func (EvaluatorFactory) ConfigurationFormat() string { return Type }

// Create implements evaluator.Factory from the store a distribution injects. The settings are
// booleanFromEnabled (default false): Flagsmith's OpenFeature providers read a boolean from the value of a flag, and
// optionally from its enabled state.
func (EvaluatorFactory) Create(s evaluator.Settings) (evaluator.Evaluator, error) {
	if s.Store == nil {
		return nil, errors.New("the flagsmith evaluator needs a store")
	}
	e := &Evaluator{store: s.Store}
	if v, ok := s.Config[SettingBooleanFromEnabled]; ok {
		b, isBool := v.(bool)
		if !isBool {
			return nil, fmt.Errorf("the flagsmith evaluator setting %s must be a boolean, not %T", SettingBooleanFromEnabled, v)
		}
		e.booleanFromEnabled = b
	}
	return e, nil
}

// Evaluator resolves the flags of the Flagsmith documents in a store with Flagsmith's engine.
type Evaluator struct {
	store              *store.Store
	booleanFromEnabled bool
}

var _ evaluator.Evaluator = (*Evaluator)(nil)

type hit struct {
	flagSet  string
	key      string
	priority int
	result   evaluator.Result
}

// Evaluate implements evaluator.Evaluator. Every read goes through one view of the store.
//
// A flag's value is the engine's: the environment default, or the override of the first segment that matches, or of an
// identity override, or the variant a multivariate flag's percentage split picks for the identity. A request without a
// targeting key evaluates at environment level: no identity, so no split and no identity override; the other
// attributes of the evaluation context are the identity's traits.
func (e *Evaluator) Evaluate(_ context.Context, req evaluator.Request, dst []evaluator.Result) ([]evaluator.Result, model.Metadata, error) {
	meta := req.Selector.ToMetadata()
	flagSet, named := req.Selector.DocumentFlagSet()
	onlySource := req.Selector.Source()
	wanted := req.WantedKeys()
	id := identityOf(req.Context)
	client := req.Attributes[CredentialAttribute] == CredentialClient

	type pair struct{ flagSet, key string }
	best := map[pair]hit{}
	view := e.store.View()
	for priority, source := range e.store.Sources() {
		if onlySource != "" && source != onlySource {
			continue
		}
		for _, doc := range view.Documents(Type, source) {
			d, _ := doc.Value.(*document)
			if d == nil || named && doc.FlagSet != flagSet {
				continue
			}
			for key, fr := range evaluate(d, wanted, id) {
				if client && d.HideDisabled && !fr.Enabled {
					continue
				}
				p := pair{doc.FlagSet, key}
				if cur, ok := best[p]; !ok || priority > cur.priority {
					best[p] = hit{flagSet: doc.FlagSet, key: key, priority: priority, result: e.resolve(doc.FlagSet, key, fr, req)}
				}
			}
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

	if req.Bulk && req.FlagKeys == nil {
		for _, h := range ordered {
			dst = append(dst, h.result)
		}
		return dst, meta, nil
	}

	keys := req.FlagKeys
	if keys == nil {
		keys = []string{req.FlagKey}
	}
	for _, key := range keys {
		var found *hit
		for i := range ordered {
			if ordered[i].key == key && (found == nil || ordered[i].priority > found.priority) {
				found = &ordered[i]
			}
		}
		if found == nil {
			dst = append(dst, evaluator.Result{
				FlagKey: key, Reason: model.ErrorReason, ErrorCode: model.FlagNotFoundErrorCode,
				ErrorMessage: model.GetErrorMessage(model.FlagNotFoundErrorCode), Metadata: meta,
			})
			continue
		}
		dst = append(dst, found.result)
	}
	return dst, meta, nil
}

type identity struct {
	identifier string
	traits     []*trait.Trait
}

// identityOf maps the evaluation context onto Flagsmith's notions: the targeting key is the identifier and the other
// attributes are its traits (UC-019 BR-007). Without a targeting key there is no identity.
func identityOf(ctx map[string]any) *identity {
	key, _ := ctx[targetingKey].(string)
	if key == "" {
		return nil
	}
	id := &identity{identifier: key, traits: make([]*trait.Trait, 0, len(ctx))}
	for k, v := range ctx {
		if k != targetingKey {
			id.traits = append(id.traits, &trait.Trait{TraitKey: k, TraitValue: v})
		}
	}
	return id
}

// evaluate runs the engine over one document, for the wanted features only: a feature is evaluated on its own, so
// narrowing the context changes no result.
func evaluate(d *document, wanted map[string]struct{}, id *identity) map[string]*engine_eval.FlagResult {
	ec := d.Context
	if wanted != nil {
		subset := make(map[string]engine_eval.FeatureContext, len(wanted))
		for key := range wanted {
			if fc, ok := ec.Features[key]; ok {
				subset[key] = fc
			}
		}
		if len(subset) == 0 {
			return nil
		}
		ec.Features = subset
	}
	if id != nil {
		ec = engine_eval.MapContextAndIdentityDataToContext(ec, id.identifier, id.traits)
	}
	return flagengine.GetEvaluationResult(&ec).Flags
}

// resolve maps an engine result onto the evaluator's: the reason as OpenFeature's (UC-019 Q4), and the value by the
// mode of the booleans and the type the caller asks for (Q5).
func (e *Evaluator) resolve(flagSet, key string, fr *engine_eval.FlagResult, req evaluator.Request) evaluator.Result {
	res := evaluator.Result{FlagKey: key, FlagSet: flagSet, Metadata: model.Metadata{}}
	if flagSet != "" {
		res.Metadata["flagSetId"] = flagSet
	}
	if fr.Metadata.FeatureID != 0 {
		res.Metadata["flagsmithFeatureId"] = fr.Metadata.FeatureID
	}
	reason := openFeatureReason(fr.Reason, res.Metadata)
	boolean := req.TypeHint == evaluator.TypeBool || req.Bulk && isBooleanValue(fr.Value)

	if !fr.Enabled {
		res.Reason = model.DisabledReason
		if e.booleanFromEnabled && boolean {
			res.Value, res.HasValue = false, true
		}
		return res
	}
	if e.booleanFromEnabled && boolean {
		res.Value, res.HasValue, res.Variant, res.Reason = true, true, fr.Variant, reason
		return res
	}
	if fr.Value == nil || req.TypeHint == evaluator.TypeBool && fr.Value == "" {
		res.Reason = model.DefaultReason
		return res
	}
	value, ok := coerce(fr.Value, req.TypeHint)
	if !ok {
		res.Reason, res.ErrorCode = model.ErrorReason, model.TypeMismatchErrorCode
		res.ErrorMessage = model.GetErrorMessage(model.TypeMismatchErrorCode)
		return res
	}
	res.Value, res.HasValue, res.Variant, res.Reason = value, true, fr.Variant, reason
	return res
}

// openFeatureReason maps the engine's reason, DEFAULT, "TARGETING_MATCH; segment=<name>" or "SPLIT; weight=<w>", and
// records the segment and the weight in the metadata.
func openFeatureReason(engineReason string, meta model.Metadata) string {
	name, detail, _ := strings.Cut(engineReason, ";")
	detail = strings.TrimSpace(detail)
	switch name {
	case "DEFAULT":
		return model.StaticReason
	case model.TargetingMatchReason:
		if segment, ok := strings.CutPrefix(detail, "segment="); ok {
			meta["flagsmithSegment"] = segment
		}
		return model.TargetingMatchReason
	case model.SplitReason:
		if w, ok := strings.CutPrefix(detail, "weight="); ok {
			if weight, err := strconv.ParseFloat(w, 64); err == nil {
				meta["flagsmithWeight"] = weight
			}
		}
		return model.SplitReason
	}
	return model.UnknownReason
}

// isBooleanValue reports whether a flag's value is that of a boolean flag: none, which an identity override writes as
// empty text, or a boolean.
func isBooleanValue(v any) bool {
	if v == nil || v == "" {
		return true
	}
	_, ok := v.(bool)
	return ok
}

// coerce reads a flag's value as the type a caller asks for. A value that Flagsmith holds as text reads as a number,
// a boolean or an object when it parses as one: an identity override carries its value as text, whatever the type of
// the flag. Numbers are float64.
func coerce(v any, hint evaluator.Type) (any, bool) {
	switch hint {
	case evaluator.TypeBool:
		switch t := v.(type) {
		case bool:
			return t, true
		case string:
			if t == "true" || t == "false" {
				return t == "true", true
			}
		}
	case evaluator.TypeString:
		s, ok := v.(string)
		return s, ok
	case evaluator.TypeInt:
		switch t := v.(type) {
		case float64:
			return t, t == math.Trunc(t)
		case string:
			if n, err := strconv.ParseInt(t, 10, 64); err == nil {
				return float64(n), true
			}
		}
	case evaluator.TypeFloat:
		switch t := v.(type) {
		case float64:
			return t, true
		case string:
			if f, err := strconv.ParseFloat(t, 64); err == nil {
				return f, true
			}
		}
	case evaluator.TypeObject:
		switch t := v.(type) {
		case map[string]any:
			return t, true
		case string:
			var obj map[string]any
			if err := json.Unmarshal([]byte(t), &obj); err == nil && obj != nil {
				return obj, true
			}
		}
	default:
		return v, true
	}
	return nil, false
}
