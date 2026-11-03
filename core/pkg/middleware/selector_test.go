package middleware

import (
	"context"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/stretchr/testify/require"
)

func TestEffectiveSelector(t *testing.T) {
	tests := []struct {
		name   string
		bound  *string
		client string
		want   store.Selector
		err    string
		noFlag bool
	}{
		{name: "neither", want: store.Selector{}},
		{name: "client only", client: "flagSetId=a", want: store.Selector{}.WithFlagSetId("a")},
		{name: "bound only", bound: ptr("flagSetId=a"), want: store.Selector{}.WithFlagSetId("a")},
		{name: "bound only, client empty", bound: ptr("source=s"), client: "", want: store.Selector{}.WithSource("s")},
		{
			name: "the client narrows with another key", bound: ptr("flagSetId=a"), client: "source=s",
			want: store.Selector{}.WithFlagSetId("a").WithSource("s"),
		},
		{
			name: "the client repeats the bound value", bound: ptr("flagSetId=a"), client: "flagSetId=a",
			want: store.Selector{}.WithFlagSetId("a"),
		},
		{name: "the same key with another value matches no flag", bound: ptr("flagSetId=a"), client: "flagSetId=b", noFlag: true},
		{name: "the same source with another value matches no flag", bound: ptr("source=s"), client: "source=t", noFlag: true},
		{name: "a bare client selector is a source", bound: ptr("source=s"), client: "t", noFlag: true},
		{name: "the client's expression is invalid", bound: ptr("flagSetId=a"), client: "colour=red", err: "invalid selector: invalid selector key"},
		{name: "the client's expression is invalid, nothing bound", client: "colour=red", err: "invalid selector: invalid selector key"},
		{name: "the bound expression is invalid", bound: ptr("colour=red"), client: "source=s", err: "invalid bound selector: invalid selector key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.bound != nil {
				ctx = BindSelector(ctx, *tt.bound)
			}

			got, err := EffectiveSelector(ctx, tt.client)

			switch {
			case tt.noFlag:
				require.ErrorIs(t, err, ErrNoFlags)
			case tt.err != "":
				require.ErrorContains(t, err, tt.err)
				require.NotErrorIs(t, err, ErrNoFlags)
			default:
				require.NoError(t, err)
				require.Equal(t, tt.want.FlagSetId(), got.FlagSetId())
				require.Equal(t, tt.want.Source(), got.Source())
			}
		})
	}
}

func TestBoundSelector(t *testing.T) {
	_, ok := BoundSelector(context.Background())
	require.False(t, ok)

	selector, ok := BoundSelector(BindSelector(context.Background(), "source=s"))
	require.True(t, ok)
	require.Equal(t, "s", selector.Source())

	_, ok = BoundSelector(BindSelector(context.Background(), "colour=red"))
	require.False(t, ok, "an invalid expression binds nothing")

	rebound, _ := BoundSelector(BindSelector(BindSelector(context.Background(), "source=a"), "source=b"))
	require.Equal(t, "b", rebound.Source(), "the inner middleware's binding wins")
}

func TestWithAttributesIsTheEvaluatorsKey(t *testing.T) {
	ctx := WithAttributes(context.Background(), map[string]any{"audience": "client"})
	require.Equal(t, map[string]any{"audience": "client"}, evaluator.AttributesFromContext(ctx))
}

func ptr(s string) *string { return &s }
