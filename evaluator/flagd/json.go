package flagd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"strings"
	"time"

	jl "github.com/diegoholiveira/jsonlogic/v3"
	flagdformat "github.com/open-feature-forking/openfeature-relay/configformat/flagd"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"go.uber.org/zap"
)

const (
	flagdPropertiesKey = "$flagd"
	// targetingKeyKey is used to extract the targetingKey to bucket on in fractional
	// evaluation if the user did not supply the optional bucketing property.
	targetingKeyKey = "targetingKey"
	Disabled        = "DISABLED"
)

type constraints interface {
	bool | string | map[string]any | float64 | interface{}
}

type JSONEvaluatorOption func(je *JSON)

type flagdProperties struct {
	FlagKey   string `json:"flagKey"`
	Timestamp int64  `json:"timestamp"`
}

type variantEvaluator func(context.Context, string, string, map[string]any) (
	variant string, variants map[string]interface{}, reason string, metadata map[string]interface{}, error error)

// Deprecated - this will be remove in the next release
func WithEvaluator(name string, evalFunc func(interface{}, interface{}) interface{}) JSONEvaluatorOption {
	return func(_ *JSON) {
		jl.AddOperator(name, evalFunc)
	}
}

// WithStrictNumericRange rejects numeric variants outside [-(2^53-1), 2^53-1] with TYPE_MISMATCH.
// Temporary: this will become the default behavior, after which the option will be removed.
// TODO(#2101): remove
func WithStrictNumericRange() JSONEvaluatorOption {
	return func(je *JSON) {
		je.strictNumericRange = true
	}
}

// JSON evaluator
type JSON struct {
	store     store.IStore
	Logger    *logger.Logger
	validator *flagdformat.Validator
	format    *flagdformat.Format // set when the store is a *store.Store
	Resolver
}

func NewJSON(logger *logger.Logger, s store.IStore, opts ...JSONEvaluatorOption) *JSON {
	logger = logger.WithFields(
		zap.String("component", "evaluator"),
		zap.String("evaluator", "json"),
	)

	ev := JSON{
		store:     s,
		Logger:    logger,
		validator: flagdformat.NewValidator(logger),
		Resolver:  NewResolver(s, logger),
	}
	if st, ok := s.(*store.Store); ok {
		ev.format = ev.validator.On(st)
	}

	for _, o := range opts {
		o(&ev)
	}

	return &ev
}

// Deprecated: configuration formats accept flag configurations; SetState remains for callers that
// hold an evaluator and keeps the error texts they see.
func (je *JSON) SetState(payload sync.DataSync) error {
	in := configformat.Input{
		Source: payload.Source, Data: payload.FlagData, Selector: payload.Selector,
		IncrementalUpdate: payload.IncrementalUpdates,
	}

	var err error
	if je.format != nil {
		_, err = je.format.Accept(context.Background(), in)
	} else {
		// an IStore implemented elsewhere may not store documents
		var definition flagdformat.Definition
		if definition, err = je.validator.Validate(in); err == nil {
			je.store.Update(payload.Source, definition.Flags, definition.Metadata, payload.IncrementalUpdates)
		}
	}

	var rejection configformat.Rejection
	if errors.As(err, &rejection) {
		return errors.New(rejection.Reason)
	}
	return err
}

// Resolver implementation for flagd flags. This resolver should be kept reusable, hence must interact with interfaces.
type Resolver struct {
	store              store.IStore
	Logger             *logger.Logger
	strictNumericRange bool // TODO(#2101): remove
}

func NewResolver(store store.IStore, logger *logger.Logger) Resolver {
	// register supported json logic custom operator implementations
	jl.AddOperator(FractionEvaluationName, NewFractional(logger).Evaluate)
	jl.AddOperator(StartsWithEvaluationName, NewStringComparisonEvaluator(logger).StartsWithEvaluation)
	jl.AddOperator(EndsWithEvaluationName, NewStringComparisonEvaluator(logger).EndsWithEvaluation)
	jl.AddOperator(SemVerEvaluationName, NewSemVerComparison(logger).SemVerEvaluation)

	return Resolver{store: store, Logger: logger}
}

func (je *Resolver) ResolveAllValues(ctx context.Context, reqID string, context map[string]any) ([]evaluator.AnyValue,
	model.Metadata, error,
) {

	var selector store.Selector
	s := ctx.Value(store.SelectorContextKey{})
	if s != nil {
		selector = s.(store.Selector)
	}
	allFlags, flagSetMetadata, err := je.store.GetAll(ctx, &selector)
	if err != nil {
		return nil, flagSetMetadata, fmt.Errorf("error retreiving flags from the store: %w", err)
	}

	values := []evaluator.AnyValue{}
	var value interface{}
	var variant string
	var reason string
	var metadata map[string]interface{}

	for _, flag := range allFlags {
		defaultValue := flag.Variants[flag.DefaultVariant]
		switch defaultValue.(type) {
		case bool:
			value, variant, reason, metadata, err = resolve[bool](ctx, reqID, flag.Key, context, je.evaluateVariant)
		case string:
			value, variant, reason, metadata, err = resolve[string](ctx, reqID, flag.Key, context, je.evaluateVariant)
		case float64:
			value, variant, reason, metadata, err = resolve[float64](ctx, reqID, flag.Key, context, je.evaluateVariant)
		case map[string]any:
			value, variant, reason, metadata, err = resolve[map[string]any](ctx, reqID, flag.Key, context, je.evaluateVariant)
		default:
			if ctx.Value(evaluator.ProtoVersionKey) == nil {
				value, variant, reason, metadata, err = resolve[interface{}](ctx, reqID, flag.Key, context, je.evaluateVariant)
			} else {
				// old proto version behavior
				continue
			}
		}
		if err != nil {
			je.Logger.ErrorWithID(reqID, fmt.Sprintf("bulk evaluation: key: %s returned error: %s", flag.Key, err.Error()))
		}
		values = append(values, evaluator.NewAnyValue(value, variant, reason, flag.Key, metadata, err))
	}

	return values, flagSetMetadata, nil
}

func (je *Resolver) ResolveBooleanValue(
	ctx context.Context, reqID string, flagKey string, context map[string]any) (
	value bool,
	variant string,
	reason string,
	metadata map[string]interface{},
	err error,
) {

	je.Logger.DebugWithID(reqID, fmt.Sprintf("evaluating boolean flag: %s", flagKey))
	return resolve[bool](ctx, reqID, flagKey, context, je.evaluateVariant)
}

func (je *Resolver) ResolveStringValue(
	ctx context.Context, reqID string, flagKey string, context map[string]any) (
	value string,
	variant string,
	reason string,
	metadata map[string]interface{},
	err error,
) {

	je.Logger.DebugWithID(reqID, fmt.Sprintf("evaluating string flag: %s", flagKey))
	return resolve[string](ctx, reqID, flagKey, context, je.evaluateVariant)
}

func (je *Resolver) ResolveFloatValue(
	ctx context.Context, reqID string, flagKey string, context map[string]any) (
	value float64,
	variant string,
	reason string,
	metadata map[string]interface{},
	err error,
) {

	je.Logger.DebugWithID(reqID, fmt.Sprintf("evaluating float flag: %s", flagKey))
	value, variant, reason, metadata, err = resolve[float64](ctx, reqID, flagKey, context, je.evaluateVariant)
	if err != nil {
		return
	}

	// TODO(#2101): remove this block
	if !je.strictNumericRange {
		return
	}

	if !isSafeRange(value) {
		return 0, variant, model.ErrorReason, metadata, errors.New(model.TypeMismatchErrorCode)
	}
	return
}

func (je *Resolver) ResolveIntValue(ctx context.Context, reqID string, flagKey string, context map[string]any) (
	value int64,
	variant string,
	reason string,
	metadata map[string]interface{},
	err error,
) {

	je.Logger.DebugWithID(reqID, fmt.Sprintf("evaluating int flag: %s", flagKey))
	var val float64
	val, variant, reason, metadata, err = resolve[float64](ctx, reqID, flagKey, context, je.evaluateVariant)
	if err != nil {
		return
	}

	// TODO(#2101): remove this block
	if !je.strictNumericRange {
		if !isInt64(val) {
			return 0, variant, model.ErrorReason, metadata, errors.New(model.TypeMismatchErrorCode)
		}
		value = int64(val)
		return
	}

	if !isInteger(val) || !isSafeRange(val) {
		return 0, variant, model.ErrorReason, metadata, errors.New(model.TypeMismatchErrorCode)
	}
	value = int64(val)
	return
}

// maxSafeInteger is 2^53-1, the largest integer exactly representable as a float64
const maxSafeInteger = 1<<53 - 1

// reports whether f has no fractional part
func isInteger(f float64) bool {
	return f == math.Trunc(f)
}

// TODO(#2101): remove
// reports whether f has no fractional part and fits in an int64
func isInt64(f float64) bool {
	// 2^63 is exact as a float64; MaxInt64 is not
	return isInteger(f) && f >= math.MinInt64 && f < 1<<63
}

// reports whether f is within [-(2^53-1), 2^53-1]
func isSafeRange(f float64) bool {
	return math.Abs(f) <= maxSafeInteger
}

func (je *Resolver) ResolveObjectValue(
	ctx context.Context, reqID string, flagKey string, context map[string]any) (
	value map[string]any,
	variant string,
	reason string,
	metadata map[string]interface{},
	err error,
) {

	je.Logger.DebugWithID(reqID, fmt.Sprintf("evaluating object flag: %s", flagKey))
	return resolve[map[string]any](ctx, reqID, flagKey, context, je.evaluateVariant)
}

func (je *Resolver) ResolveAsAnyValue(
	ctx context.Context,
	reqID string,
	flagKey string,
	context map[string]any,
) evaluator.AnyValue {

	je.Logger.DebugWithID(reqID, fmt.Sprintf("evaluating flag `%s` as a generic flag", flagKey))
	value, variant, reason, meta, err := resolve[interface{}](ctx, reqID, flagKey, context, je.evaluateVariant)
	return evaluator.NewAnyValue(value, variant, reason, flagKey, meta, err)
}

// resolve is a helper for generic flag resolving
func resolve[T constraints](ctx context.Context, reqID string, key string, context map[string]any,
	variantEval variantEvaluator) (value T, variant string, reason string, metadata map[string]interface{}, err error,
) {
	variant, variants, reason, metadata, err := variantEval(ctx, reqID, key, context)
	if err != nil {
		return value, variant, reason, metadata, err
	}

	if reason == model.FallbackReason {
		var zero T
		return zero, variant, model.FallbackReason, metadata, nil
	}

	if reason == model.DisabledReason {
		var zero T
		return zero, "", model.DisabledReason, metadata, nil
	}

	var ok bool
	value, ok = variants[variant].(T)
	if !ok {
		return value, variant, model.ErrorReason, metadata, errors.New(model.TypeMismatchErrorCode)
	}

	return value, variant, reason, metadata, nil
}

// nolint: funlen
func (je *Resolver) evaluateVariant(ctx context.Context, reqID string, flagKey string, evalCtx map[string]any) (
	variant string, variants map[string]interface{}, reason string, metadata map[string]interface{}, err error,
) {
	var selector store.Selector
	s := ctx.Value(store.SelectorContextKey{})
	if s != nil {
		selector = s.(store.Selector)
	}
	flag, metadata, err := je.store.Get(ctx, flagKey, &selector)
	if err != nil {
		// flag not found
		je.Logger.DebugWithID(reqID, fmt.Sprintf("requested flag could not be found: %s", flagKey))
		return "", map[string]interface{}{}, model.ErrorReason, metadata, errors.New(model.FlagNotFoundErrorCode)
	}

	for key, value := range flag.Metadata {
		// If value is not nil or empty, copy to metadata
		if value != nil {
			metadata[key] = value
		}
	}

	if flag.State == Disabled {
		je.Logger.DebugWithID(reqID, fmt.Sprintf("requested flag is disabled: %s", flagKey))
		return "", nil, model.DisabledReason, metadata, nil
	}

	// get the targeting logic, if any
	targeting := flag.Targeting

	if targeting != nil && string(targeting) != "{}" {
		targetingBytes, err := targeting.MarshalJSON()
		if err != nil {
			je.Logger.ErrorWithID(reqID, fmt.Sprintf("Error parsing rules for flag: %s, %s", flagKey, err))
			return "", flag.Variants, model.ErrorReason, metadata, errors.New(model.ParseErrorCode)
		}

		evalCtx = setFlagdProperties(je.Logger, evalCtx, flagdProperties{
			FlagKey:   flagKey,
			Timestamp: time.Now().Unix(),
		})

		b, err := json.Marshal(evalCtx)
		if err != nil {
			je.Logger.ErrorWithID(reqID, fmt.Sprintf("error parsing context for flag: %s, %s, %v", flagKey, err, evalCtx))

			return "", flag.Variants, model.ErrorReason, metadata, errors.New(model.ErrorReason)
		}

		var result bytes.Buffer
		// evaluate JsonLogic rules to determine the variant
		err = jl.Apply(bytes.NewReader(targetingBytes), bytes.NewReader(b), &result)
		if err != nil {
			je.Logger.ErrorWithID(reqID, fmt.Sprintf("error applying targeting rules: %s", err))
			return "", flag.Variants, model.ErrorReason, metadata, errors.New(model.ParseErrorCode)
		}

		// check if string is "null" before we strip quotes, so we can differentiate between JSON null and "null"
		trimmed := strings.TrimSpace(result.String())

		if trimmed == "null" {
			if flag.DefaultVariant == "" {
				if ctx.Value(evaluator.ProtoVersionKey) != nil {
					// old proto version behavior
					return "", flag.Variants, model.ErrorReason, metadata, errors.New(model.FlagNotFoundErrorCode)
				}

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
		je.Logger.ErrorWithID(reqID,
			fmt.Sprintf("invalid or missing variant: %s for flagKey: %s, variant is not valid", variant, flagKey))
		return "", flag.Variants, model.ErrorReason, metadata, errors.New(model.GeneralErrorCode)
	}

	if flag.DefaultVariant == "" {
		if ctx.Value(evaluator.ProtoVersionKey) != nil {
			// old proto version behavior
			return "", flag.Variants, model.ErrorReason, metadata, errors.New(model.FlagNotFoundErrorCode)
		}
		return "", flag.Variants, model.FallbackReason, metadata, nil
	}

	return flag.DefaultVariant, flag.Variants, model.StaticReason, metadata, nil
}

func setFlagdProperties(
	log *logger.Logger,
	context map[string]any,
	properties flagdProperties,
) map[string]any {
	if context == nil {
		context = map[string]any{}
	}

	newContext := maps.Clone(context)

	if _, ok := newContext[flagdPropertiesKey]; ok {
		log.Debug("overwriting $flagd properties in the context")
	}

	newContext[flagdPropertiesKey] = properties

	return newContext
}

func getFlagdProperties(context map[string]any) (flagdProperties, bool) {
	properties, ok := context[flagdPropertiesKey]
	if !ok {
		return flagdProperties{}, false
	}

	b, err := json.Marshal(properties)
	if err != nil {
		return flagdProperties{}, false
	}

	var p flagdProperties
	if err := json.Unmarshal(b, &p); err != nil {
		return flagdProperties{}, false
	}

	return p, true
}
