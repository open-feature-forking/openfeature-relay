// Package middleware is where middleware binds a request to a selector and attaches request attributes,
// and where service endpoints read both.
package middleware

import (
	"context"
	"errors"
	"fmt"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
)

// ErrNoFlags is returned by EffectiveSelector when the bound selector and the client's selector name
// different values for the same key: no flag matches both. A service endpoint answers FLAG_NOT_FOUND for a
// single flag and no flags for a bulk request, a flag sync or an event stream.
var ErrNoFlags = errors.New("the bound selector and the client's selector name different values for the same key")

type boundSelectorKey struct{}

type boundValue struct {
	selector store.Selector
	err      error
}

// BindSelector returns a context whose requests are bound to the selector expression, in the syntax of the
// Flagd-Selector header. An invalid expression is reported by EffectiveSelector. It is the one key for a
// bound selector.
func BindSelector(ctx context.Context, expression string) context.Context {
	selector, err := store.NewSelector(expression)
	return context.WithValue(ctx, boundSelectorKey{}, boundValue{selector: selector, err: err})
}

// BoundSelector returns the selector BindSelector bound to the context. bound reports whether a binding exists,
// valid or not. An invalid expression is an error, never "no binding": a caller that ignored it would serve
// every flag to a request the middleware meant to narrow.
func BoundSelector(ctx context.Context) (selector store.Selector, bound bool, err error) {
	b, ok := ctx.Value(boundSelectorKey{}).(boundValue)
	if !ok {
		return store.Selector{}, false, nil
	}
	if b.err != nil {
		return store.Selector{}, true, fmt.Errorf("invalid bound selector: %w", b.err)
	}
	return b.selector, true, nil
}

// EffectiveSelector is the selector a service endpoint resolves a request for: the intersection of the
// selector bound to the context and the one the client sent, parsed from clientExpression.
//   - A key only the bound selector sets is taken from it.
//   - A key only the client sets narrows the bound selector.
//   - A key both set to different values matches no flag: the error is ErrNoFlags.
//   - An invalid expression, bound or from the client, is an error naming it.
//
// Endpoints write the result into store.SelectorContextKey{}.
func EffectiveSelector(ctx context.Context, clientExpression string) (store.Selector, error) {
	client, err := store.NewSelector(clientExpression)
	if err != nil {
		return store.Selector{}, fmt.Errorf("invalid selector: %w", err)
	}
	b, ok := ctx.Value(boundSelectorKey{}).(boundValue)
	if !ok {
		return client, nil
	}
	if b.err != nil {
		return store.Selector{}, fmt.Errorf("invalid bound selector: %w", b.err)
	}

	var merged store.Selector
	for _, key := range []struct {
		bound, client string
		with          func(store.Selector, string) store.Selector
	}{
		{b.selector.FlagSetId(), client.FlagSetId(), store.Selector.WithFlagSetId},
		{b.selector.Source(), client.Source(), store.Selector.WithSource},
	} {
		switch {
		case key.client == "" || key.client == key.bound:
			if key.bound != "" {
				merged = key.with(merged, key.bound)
			}
		case key.bound == "":
			merged = key.with(merged, key.client)
		default:
			return store.Selector{}, ErrNoFlags
		}
	}
	return merged, nil
}

// WithAttributes returns a context whose requests carry the attributes, for example the caller's audience. It
// is evaluator.WithAttributes: there is one attribute key.
func WithAttributes(ctx context.Context, attributes map[string]any) context.Context {
	return evaluator.WithAttributes(ctx, attributes)
}
