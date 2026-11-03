package service

import (
	"context"
	"errors"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
)

// noFlagsSet is a flag set no flag belongs to.
const noFlagsSet = "\x00relay.no-flags"

// RequestSelector is the selector an endpoint resolves a request for. Without a selector bound to the context
// by a middleware, it is the client's: the parsed expression, with its error as it is. With one, it is
// middleware.EffectiveSelector, where a conflict with the client's selector gives a selector that matches no
// flag, so that a single flag is not found and a bulk request, a flag sync and an event stream carry no flags.
func RequestSelector(ctx context.Context, clientExpression string) (store.Selector, error) {
	client, err := store.NewSelector(clientExpression)
	if err != nil {
		return store.Selector{}, err
	}
	if _, bound, err := middleware.BoundSelector(ctx); err != nil {
		return store.Selector{}, err
	} else if !bound {
		return client, nil
	}

	effective, err := middleware.EffectiveSelector(ctx, clientExpression)
	if errors.Is(err, middleware.ErrNoFlags) {
		return store.Selector{}.WithFlagSetId(noFlagsSet), nil
	}
	return effective, err
}

// MatchesNoFlags reports whether the selector is the one RequestSelector gives a request whose bound and client's
// selectors conflict. An endpoint that resolves a single flag answers FLAG_NOT_FOUND for it without evaluating.
func MatchesNoFlags(selector store.Selector) bool { return selector.FlagSetId() == noFlagsSet }
