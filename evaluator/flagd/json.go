package flagd

import (
	"context"
	"encoding/json"
	"errors"
	"maps"

	jl "github.com/diegoholiveira/jsonlogic/v3"
	flagdformat "github.com/open-feature-forking/openfeature-relay/configformat/flagd"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator/typed"
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

type JSONEvaluatorOption func(je *JSON)

type flagdProperties struct {
	FlagKey   string `json:"flagKey"`
	Timestamp int64  `json:"timestamp"`
}

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
	ev.typed = ev.newAdapter()

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
	typed              evaluator.IResolver
}

func NewResolver(store store.IStore, logger *logger.Logger) Resolver {
	// register supported json logic custom operator implementations
	jl.AddOperator(FractionEvaluationName, NewFractional(logger).Evaluate)
	jl.AddOperator(StartsWithEvaluationName, NewStringComparisonEvaluator(logger).StartsWithEvaluation)
	jl.AddOperator(EndsWithEvaluationName, NewStringComparisonEvaluator(logger).EndsWithEvaluation)
	jl.AddOperator(SemVerEvaluationName, NewSemVerComparison(logger).SemVerEvaluation)

	return Resolver{store: store, Logger: logger}
}

// adapter returns the typed calls over Evaluate.
func (je *Resolver) adapter() evaluator.IResolver {
	if je.typed != nil {
		return je.typed
	}
	return je.newAdapter()
}

func (je *Resolver) newAdapter() evaluator.IResolver {
	var opts []typed.Option
	if je.strictNumericRange {
		opts = append(opts, typed.WithStrictNumericRange())
	}
	return typed.New(je, opts...)
}

func (je *Resolver) ResolveAllValues(ctx context.Context, reqID string, context map[string]any) ([]evaluator.AnyValue,
	model.Metadata, error,
) {
	return je.adapter().ResolveAllValues(ctx, reqID, context)
}

func (je *Resolver) ResolveBooleanValue(
	ctx context.Context, reqID string, flagKey string, context map[string]any) (
	bool, string, string, map[string]interface{}, error,
) {
	return je.adapter().ResolveBooleanValue(ctx, reqID, flagKey, context)
}

func (je *Resolver) ResolveStringValue(
	ctx context.Context, reqID string, flagKey string, context map[string]any) (
	string, string, string, map[string]interface{}, error,
) {
	return je.adapter().ResolveStringValue(ctx, reqID, flagKey, context)
}

func (je *Resolver) ResolveFloatValue(
	ctx context.Context, reqID string, flagKey string, context map[string]any) (
	float64, string, string, map[string]interface{}, error,
) {
	return je.adapter().ResolveFloatValue(ctx, reqID, flagKey, context)
}

func (je *Resolver) ResolveIntValue(ctx context.Context, reqID string, flagKey string, context map[string]any) (
	int64, string, string, map[string]interface{}, error,
) {
	return je.adapter().ResolveIntValue(ctx, reqID, flagKey, context)
}

func (je *Resolver) ResolveObjectValue(
	ctx context.Context, reqID string, flagKey string, context map[string]any) (
	map[string]any, string, string, map[string]interface{}, error,
) {
	return je.adapter().ResolveObjectValue(ctx, reqID, flagKey, context)
}

func (je *Resolver) ResolveAsAnyValue(
	ctx context.Context,
	reqID string,
	flagKey string,
	context map[string]any,
) evaluator.AnyValue {
	return je.adapter().ResolveAsAnyValue(ctx, reqID, flagKey, context)
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
