// Package typed implements evaluator.IResolver, the typed calls endpoints and in-process providers make, on
// top of any evaluator.Evaluator.
package typed

import (
	"context"
	"errors"
	"math"
	"sync"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
)

// maxSafeInteger is 2^53-1, the largest integer exactly representable as a float64
const maxSafeInteger = 1<<53 - 1

// Option configures the adapter.
type Option func(*adapter)

// WithStrictNumericRange makes integer and float resolutions fail with TYPE_MISMATCH outside
// [-(2^53-1), 2^53-1], and integer resolutions fail for fractional values.
// TODO(#2101): remove with the default behaviour it guards.
func WithStrictNumericRange() Option {
	return func(a *adapter) { a.strictNumericRange = true }
}

// Observer is called once for each flag the adapter resolves, with the result it finally returns to the
// caller: its error code (TYPE_MISMATCH included), the converted value, and HasValue unset where it
// answers with the caller's default. It runs on the caller's goroutine and must not block. In a bulk
// request the request is the bulk one and Result.FlagKey names the flag.
type Observer func(ctx context.Context, req evaluator.Request, res evaluator.Result)

// WithObserver reports every resolution to observer.
func WithObserver(observer Observer) Option {
	return func(a *adapter) { a.observer = observer }
}

type adapter struct {
	ev                 evaluator.Evaluator
	strictNumericRange bool
	observer           Observer
	buffers            sync.Pool
}

// New returns the typed calls over ev.
func New(ev evaluator.Evaluator, opts ...Option) evaluator.IResolver {
	a := &adapter{ev: ev}
	a.buffers.New = func() any {
		buffer := make([]evaluator.Result, 0, 1)
		return &buffer
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

func selectorFrom(ctx context.Context) store.Selector {
	selector, _ := ctx.Value(store.SelectorContextKey{}).(store.Selector)
	return selector
}

// resolution is a single result after the adapter's rules, before the requested type is applied.
type resolution struct {
	value    any
	hasValue bool
	variant  string
	reason   string
	metadata model.Metadata
	err      error
	// the request and the configuration format are kept for the observer
	req    evaluator.Request
	format string
}

// observe reports a single resolution as the adapter returns it.
func (a *adapter) observe(ctx context.Context, r resolution, value any, variant, reason string, metadata model.Metadata, err error) {
	res := evaluator.Result{FlagKey: r.req.FlagKey, Variant: variant, Reason: reason, Metadata: metadata, Format: r.format}
	if err != nil {
		res.ErrorCode = errorCode(err)
	} else if r.hasValue {
		res.Value, res.HasValue = value, true
	}
	a.observer(ctx, r.req, res)
}

// observeBulk reports one flag of a bulk request, as the adapter returns it in value.
func (a *adapter) observeBulk(ctx context.Context, req evaluator.Request, r evaluator.Result, value evaluator.AnyValue) {
	res := evaluator.Result{
		FlagKey: r.FlagKey, Variant: value.Variant, Reason: value.Reason, ErrorCode: r.ErrorCode, Metadata: r.Metadata, Format: r.Format,
	}
	if r.ErrorCode == "" && r.HasValue {
		res.Value, res.HasValue = r.Value, true
	}
	a.observer(ctx, req, res)
}

// errorCode is the error's text when it is an OpenFeature error code, and GENERAL otherwise.
func errorCode(err error) string {
	switch code := err.Error(); code {
	case model.FlagNotFoundErrorCode, model.ParseErrorCode, model.TypeMismatchErrorCode,
		model.GeneralErrorCode, model.FlagDisabledErrorCode:
		return code
	}
	return model.GeneralErrorCode
}

func (a *adapter) single(ctx context.Context, key string, evalCtx map[string]any, hint evaluator.Type) resolution {
	bufferp := a.buffers.Get().(*[]evaluator.Result)
	req := evaluator.Request{
		Selector:   selectorFrom(ctx),
		FlagKey:    key,
		Context:    evalCtx,
		TypeHint:   hint,
		Attributes: evaluator.AttributesFromContext(ctx),
	}
	results, _, err := a.ev.Evaluate(ctx, req, (*bufferp)[:0])

	var result evaluator.Result
	if err == nil && len(results) > 0 {
		result = results[0]
	}
	clear(results)
	*bufferp = results[:0]
	a.buffers.Put(bufferp)

	r := classify(ctx, result, len(results), err)
	r.req, r.format = req, result.Format
	return r
}

// classify applies the adapter's rules to the evaluator's answer, n results long, to a single request.
func classify(ctx context.Context, result evaluator.Result, n int, err error) resolution {
	switch {
	case err != nil:
		return resolution{reason: model.ErrorReason, err: err}
	case n == 0:
		return resolution{reason: model.ErrorReason, err: errors.New(model.FlagNotFoundErrorCode)}
	case result.ErrorCode != "":
		return resolution{variant: result.Variant, reason: model.ErrorReason, metadata: result.Metadata, err: errors.New(result.ErrorCode)}
	case !result.HasValue && result.Reason == model.DisabledReason:
		return resolution{reason: model.DisabledReason, metadata: result.Metadata}
	case !result.HasValue:
		if ctx.Value(evaluator.ProtoVersionKey) != nil {
			// old proto version behavior
			return resolution{reason: model.ErrorReason, metadata: result.Metadata, err: errors.New(model.FlagNotFoundErrorCode)}
		}
		return resolution{reason: model.FallbackReason, metadata: result.Metadata}
	}
	return resolution{value: result.Value, hasValue: true, variant: result.Variant, reason: result.Reason, metadata: result.Metadata}
}

func mismatch[T any](r resolution) (value T, variant, reason string, metadata model.Metadata, err error) {
	return value, r.variant, model.ErrorReason, r.metadata, errors.New(model.TypeMismatchErrorCode)
}

// finish reports a single resolution to the observer, when there is one, and returns it.
func finish[T any](ctx context.Context, a *adapter, r resolution, value T, variant, reason string, metadata model.Metadata, err error) (T, string, string, model.Metadata, error) {
	if a.observer != nil {
		a.observe(ctx, r, value, variant, reason, metadata, err)
	}
	return value, variant, reason, metadata, err
}

func resolve[T any](r resolution) (value T, variant, reason string, metadata model.Metadata, err error) {
	if r.err != nil || !r.hasValue {
		return value, r.variant, r.reason, r.metadata, r.err
	}
	value, ok := r.value.(T)
	if !ok {
		return mismatch[T](r)
	}
	return value, r.variant, r.reason, r.metadata, nil
}

func (a *adapter) ResolveBooleanValue(ctx context.Context, _ string, flagKey string, evalCtx map[string]any) (bool, string, string, model.Metadata, error) {
	r := a.single(ctx, flagKey, evalCtx, evaluator.TypeBool)
	value, variant, reason, metadata, err := resolve[bool](r)
	return finish(ctx, a, r, value, variant, reason, metadata, err)
}

func (a *adapter) ResolveStringValue(ctx context.Context, _ string, flagKey string, evalCtx map[string]any) (string, string, string, model.Metadata, error) {
	r := a.single(ctx, flagKey, evalCtx, evaluator.TypeString)
	value, variant, reason, metadata, err := resolve[string](r)
	return finish(ctx, a, r, value, variant, reason, metadata, err)
}

func (a *adapter) ResolveObjectValue(ctx context.Context, _ string, flagKey string, evalCtx map[string]any) (map[string]any, string, string, model.Metadata, error) {
	r := a.single(ctx, flagKey, evalCtx, evaluator.TypeObject)
	value, variant, reason, metadata, err := resolve[map[string]any](r)
	return finish(ctx, a, r, value, variant, reason, metadata, err)
}

// asFloat reads the numeric values engines produce; JSON numbers arrive as float64.
func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

func isInteger(f float64) bool { return f == math.Trunc(f) }

// TODO(#2101): remove
// isInt64 reports whether f has no fractional part and fits in an int64; 2^63 is exact as a float64, MaxInt64 is not.
func isInt64(f float64) bool { return isInteger(f) && f >= math.MinInt64 && f < 1<<63 }

func isSafeRange(f float64) bool { return math.Abs(f) <= maxSafeInteger }

func (a *adapter) ResolveFloatValue(ctx context.Context, _ string, flagKey string, evalCtx map[string]any) (float64, string, string, model.Metadata, error) {
	r := a.single(ctx, flagKey, evalCtx, evaluator.TypeFloat)
	value, variant, reason, metadata, err := a.floatValue(r)
	return finish(ctx, a, r, value, variant, reason, metadata, err)
}

func (a *adapter) floatValue(r resolution) (float64, string, string, model.Metadata, error) {
	if r.err != nil || !r.hasValue {
		return 0, r.variant, r.reason, r.metadata, r.err
	}
	value, ok := asFloat(r.value)
	if !ok || (a.strictNumericRange && !isSafeRange(value)) {
		return mismatch[float64](r)
	}
	return value, r.variant, r.reason, r.metadata, nil
}

func (a *adapter) ResolveIntValue(ctx context.Context, _ string, flagKey string, evalCtx map[string]any) (int64, string, string, model.Metadata, error) {
	r := a.single(ctx, flagKey, evalCtx, evaluator.TypeInt)
	value, variant, reason, metadata, err := a.intValue(r)
	return finish(ctx, a, r, value, variant, reason, metadata, err)
}

func (a *adapter) intValue(r resolution) (int64, string, string, model.Metadata, error) {
	if r.err != nil || !r.hasValue {
		return 0, r.variant, r.reason, r.metadata, r.err
	}
	switch n := r.value.(type) {
	case int:
		return int64(n), r.variant, r.reason, r.metadata, nil
	case int32:
		return int64(n), r.variant, r.reason, r.metadata, nil
	case int64:
		return n, r.variant, r.reason, r.metadata, nil
	}
	value, ok := asFloat(r.value)
	if !ok {
		return mismatch[int64](r)
	}
	// TODO(#2101): remove the non-strict branch
	if a.strictNumericRange {
		if !isInteger(value) || !isSafeRange(value) {
			return mismatch[int64](r)
		}
	} else if !isInt64(value) {
		return mismatch[int64](r)
	}
	return int64(value), r.variant, r.reason, r.metadata, nil
}

func (a *adapter) ResolveAsAnyValue(ctx context.Context, _ string, flagKey string, evalCtx map[string]any) evaluator.AnyValue {
	r := a.single(ctx, flagKey, evalCtx, evaluator.TypeAny)
	if a.observer != nil {
		a.observe(ctx, r, r.value, r.variant, r.reason, r.metadata, r.err)
	}
	return evaluator.NewAnyValue(r.value, r.variant, r.reason, flagKey, r.metadata, r.err)
}

func (a *adapter) ResolveAllValues(ctx context.Context, _ string, evalCtx map[string]any) ([]evaluator.AnyValue, model.Metadata, error) {
	bufferp := a.buffers.Get().(*[]evaluator.Result)
	req := evaluator.Request{
		Selector:   selectorFrom(ctx),
		Bulk:       true,
		Context:    evalCtx,
		Attributes: evaluator.AttributesFromContext(ctx),
	}
	results, setMetadata, err := a.ev.Evaluate(ctx, req, (*bufferp)[:0])
	defer func() {
		clear(results)
		*bufferp = results[:0]
		a.buffers.Put(bufferp)
	}()
	if err != nil {
		return nil, setMetadata, err
	}

	values := make([]evaluator.AnyValue, 0, len(results))
	for _, r := range results {
		value := evaluator.AnyValue{
			Value: r.Value, Variant: r.Variant, Reason: r.Reason, FlagKey: r.FlagKey, Metadata: r.Metadata,
		}
		switch {
		case r.ErrorCode != "":
			value.Reason = model.ErrorReason
			value.Error = errors.New(r.ErrorCode)
		case !r.HasValue && r.Reason == model.DisabledReason:
			value.Variant = ""
		case !r.HasValue:
			value.Variant = ""
			value.Reason = model.FallbackReason
		}
		if a.observer != nil {
			a.observeBulk(ctx, req, r, value)
		}
		values = append(values, value)
	}
	return values, setMetadata, nil
}
