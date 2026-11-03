package flagd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	jl "github.com/diegoholiveira/jsonlogic/v3"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
)

var _ evaluator.Evaluator = (*Resolver)(nil)

// reader is where one evaluation reads flags from. Over a *store.Store it holds one View, so every read of the
// evaluation sees the same state; a foreign IStore is read per call.
type reader struct {
	view    store.View
	foreign store.IStore
	ctx     context.Context
}

func (je *Resolver) reader(ctx context.Context) reader {
	if st, ok := je.store.(*store.Store); ok {
		return reader{view: st.View()}
	}
	return reader{foreign: je.store, ctx: ctx}
}

func (r reader) get(key string, selector *store.Selector) (model.Flag, model.Metadata, error) {
	if r.foreign != nil {
		return r.foreign.Get(r.ctx, key, selector)
	}
	return r.view.Get(key, selector)
}

func (r reader) getAll(selector *store.Selector) ([]model.Flag, model.Metadata, error) {
	if r.foreign != nil {
		return r.foreign.GetAll(r.ctx, selector)
	}
	return r.view.GetAll(selector)
}

// Evaluate resolves the flags of the request. A bulk request resolves every flag the selector names and applies
// the bulk rules: a variant must have the type of the flag's default variant, the value of a flag that resolves
// to none keeps the zero value of that type, and under evaluator.ProtoVersionKey flags whose default variant is
// absent or not a bool, string, number or object are left out.
func (je *Resolver) Evaluate(
	ctx context.Context, req evaluator.Request, dst []evaluator.Result,
) ([]evaluator.Result, model.Metadata, error) {
	rd := je.reader(ctx)

	if req.Bulk {
		return je.evaluateBulk(ctx, rd, req, dst)
	}
	if req.FlagKeys != nil {
		for _, key := range req.FlagKeys {
			dst = je.evaluateSingle(ctx, rd, req, key, dst)
		}
		return dst, nil, nil
	}
	return je.evaluateSingle(ctx, rd, req, req.FlagKey, dst), nil, nil
}

func (je *Resolver) evaluateSingle(
	ctx context.Context, rd reader, req evaluator.Request, key string, dst []evaluator.Result,
) []evaluator.Result {
	je.Logger.Debug(fmt.Sprintf("evaluating flag: %s", key))
	flag, metadata, err := rd.get(key, &req.Selector)
	if err != nil {
		// flag not found
		je.Logger.Debug(fmt.Sprintf("requested flag could not be found: %s", key))
		return append(dst, evaluator.Result{
			FlagKey: key, Reason: model.ErrorReason, ErrorCode: model.FlagNotFoundErrorCode,
			ErrorMessage: model.GetErrorMessage(model.FlagNotFoundErrorCode), Metadata: metadata,
		})
	}
	return append(dst, je.resolveFlag(ctx, flag, flag, metadata, req.Context, false))
}

func (je *Resolver) evaluateBulk(
	ctx context.Context, rd reader, req evaluator.Request, dst []evaluator.Result,
) ([]evaluator.Result, model.Metadata, error) {
	allFlags, flagSetMetadata, err := rd.getAll(&req.Selector)
	if err != nil {
		return dst, flagSetMetadata, fmt.Errorf("error retreiving flags from the store: %w", err)
	}

	var wanted map[string]struct{}
	if req.FlagKeys != nil {
		wanted = make(map[string]struct{}, len(req.FlagKeys))
		for _, key := range req.FlagKeys {
			wanted[key] = struct{}{}
		}
	}

	for _, flag := range allFlags {
		if wanted != nil {
			if _, ok := wanted[flag.Key]; !ok {
				continue
			}
		}
		if _, standard := zeroOfDefault(flag); !standard && ctx.Value(evaluator.ProtoVersionKey) != nil {
			// old proto version behavior
			continue
		}
		// The flag evaluated is the one a single request for the key would read, as it always was: with one key
		// in several flag sets the entries differ in nothing but the flag set the bulk response reports.
		current, metadata, err := rd.get(flag.Key, &req.Selector)
		if err != nil {
			dst = append(dst, evaluator.Result{
				FlagKey: flag.Key, Reason: model.ErrorReason, ErrorCode: model.FlagNotFoundErrorCode,
				ErrorMessage: model.GetErrorMessage(model.FlagNotFoundErrorCode), Metadata: metadata,
			})
			continue
		}
		result := je.resolveFlag(ctx, current, flag, metadata, req.Context, true)
		if result.ErrorCode != "" {
			je.Logger.Error(fmt.Sprintf("bulk evaluation: key: %s returned error: %s", flag.Key, result.ErrorCode))
		}
		dst = append(dst, result)
	}
	return dst, flagSetMetadata, nil
}

// zeroOfDefault returns the zero value of the type of the flag's default variant, and whether that type is a
// bool, string, number or object. Flags of any other type resolve untyped.
func zeroOfDefault(flag model.Flag) (any, bool) {
	switch flag.Variants[flag.DefaultVariant].(type) {
	case bool:
		return false, true
	case string:
		return "", true
	case float64:
		return float64(0), true
	case map[string]any:
		return map[string]any(nil), true
	}
	return nil, false
}

// matchesDefaultType reports whether value has the type of the flag's default variant, the rule for bulk
// resolutions. Flags of any other type accept every non-null value.
func matchesDefaultType(flag model.Flag, value any) bool {
	switch flag.Variants[flag.DefaultVariant].(type) {
	case bool:
		_, ok := value.(bool)
		return ok
	case string:
		_, ok := value.(string)
		return ok
	case float64:
		_, ok := value.(float64)
		return ok
	case map[string]any:
		_, ok := value.(map[string]any)
		return ok
	}
	return value != nil
}

// resolveFlag evaluates one flag. In bulk mode the type of typed's default variant decides the type of the
// result, and results that carry no value keep its zero value.
func (je *Resolver) resolveFlag(
	ctx context.Context, flag, typed model.Flag, metadata model.Metadata, evalCtx map[string]any, bulk bool,
) evaluator.Result {
	variant, variants, reason, metadata, err := je.evaluateFlag(ctx, flag, metadata, evalCtx)
	result := evaluator.Result{
		FlagKey: flag.Key, FlagSet: store.FlagSetID(typed, nil), Variant: variant, Reason: reason, Metadata: metadata,
	}

	var zero any
	if bulk {
		zero, _ = zeroOfDefault(typed)
	}
	result.Value = zero

	switch {
	case err != nil:
		result.ErrorCode = err.Error()
		result.ErrorMessage = model.GetErrorMessage(result.ErrorCode)
		result.Reason = model.ErrorReason
	case reason == model.FallbackReason:
		result.Variant = ""
		result.Reason = model.DefaultReason
	case reason == model.DisabledReason:
		result.Variant = ""
	default:
		value := variants[variant]
		if value == nil || (bulk && !matchesDefaultType(typed, value)) {
			result.ErrorCode = model.TypeMismatchErrorCode
			result.ErrorMessage = model.GetErrorMessage(model.TypeMismatchErrorCode)
			result.Reason = model.ErrorReason
			return result
		}
		result.Value, result.HasValue = value, true
	}
	return result
}

// evaluateFlag picks the flag's variant. A flag that resolves to no variant has the reason FALLBACK, a disabled
// flag DISABLED.
func (je *Resolver) evaluateFlag(ctx context.Context, flag model.Flag, metadata model.Metadata, evalCtx map[string]any) (
	variant string, variants map[string]interface{}, reason string, _ map[string]interface{}, err error,
) {
	flagKey := flag.Key
	if metadata == nil {
		metadata = model.Metadata{}
	}
	for key, value := range flag.Metadata {
		// If value is not nil or empty, copy to metadata
		if value != nil {
			metadata[key] = value
		}
	}

	if flag.State == Disabled {
		je.Logger.Debug(fmt.Sprintf("requested flag is disabled: %s", flagKey))
		return "", nil, model.DisabledReason, metadata, nil
	}

	// get the targeting logic, if any
	targeting := flag.Targeting

	if targeting != nil && string(targeting) != "{}" {
		targetingBytes, err := targeting.MarshalJSON()
		if err != nil {
			je.Logger.Error(fmt.Sprintf("Error parsing rules for flag: %s, %s", flagKey, err))
			return "", flag.Variants, model.ErrorReason, metadata, errors.New(model.ParseErrorCode)
		}

		evalCtx = setFlagdProperties(je.Logger, evalCtx, flagdProperties{
			FlagKey:   flagKey,
			Timestamp: time.Now().Unix(),
		})

		b, err := json.Marshal(evalCtx)
		if err != nil {
			je.Logger.Error(fmt.Sprintf("error parsing context for flag: %s, %s, %v", flagKey, err, evalCtx))

			return "", flag.Variants, model.ErrorReason, metadata, errors.New(model.GeneralErrorCode)
		}

		var result bytes.Buffer
		// evaluate JsonLogic rules to determine the variant
		err = jl.Apply(bytes.NewReader(targetingBytes), bytes.NewReader(b), &result)
		if err != nil {
			je.Logger.Error(fmt.Sprintf("error applying targeting rules: %s", err))
			return "", flag.Variants, model.ErrorReason, metadata, errors.New(model.ParseErrorCode)
		}

		// check if string is "null" before we strip quotes, so we can differentiate between JSON null and "null"
		trimmed := strings.TrimSpace(result.String())

		if trimmed == "null" {
			if flag.DefaultVariant == "" {
				return "", flag.Variants, model.FallbackReason, metadata, nil
			}

			return flag.DefaultVariant, flag.Variants, model.DefaultReason, metadata, nil
		}

		// strip whitespace and quotes from the variant
		variant = strings.ReplaceAll(trimmed, "\"", "")

		// if this is a valid variant, return it
		if _, ok := flag.Variants[variant]; ok {
			return variant, flag.Variants, model.TargetingMatchReason, metadata, nil
		}
		je.Logger.Error(fmt.Sprintf("invalid or missing variant: %s for flagKey: %s, variant is not valid", variant, flagKey))
		return "", flag.Variants, model.ErrorReason, metadata, errors.New(model.GeneralErrorCode)
	}

	if flag.DefaultVariant == "" {
		return "", flag.Variants, model.FallbackReason, metadata, nil
	}

	return flag.DefaultVariant, flag.Variants, model.StaticReason, metadata, nil
}
