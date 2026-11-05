package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/open-feature-forking/openfeature-relay/service/transport"
	"github.com/stretchr/testify/require"
)

func TestRequestSelectorWithoutBinding(t *testing.T) {
	got, err := service.RequestSelector(context.Background(), "flagSetId=a")
	require.NoError(t, err)
	want, _ := store.NewSelector("flagSetId=a")
	require.Equal(t, want, got)
	require.False(t, service.MatchesNoFlags(got))

	_, want2 := store.NewSelector("color=red")
	_, err = service.RequestSelector(context.Background(), "color=red")
	require.Equal(t, want2, err, "the client's parse error is returned as it is")
	require.False(t, errors.Is(err, service.ErrInvalidBoundSelector))
}

func TestRequestSelectorWithBinding(t *testing.T) {
	ctx := middleware.BindSelector(context.Background(), "flagSetId=a")

	got, err := service.RequestSelector(ctx, "")
	require.NoError(t, err)
	require.Equal(t, "a", got.FlagSetId())

	got, err = service.RequestSelector(ctx, "flagSetId=a")
	require.NoError(t, err)
	require.Equal(t, "a", got.FlagSetId())
	require.False(t, service.MatchesNoFlags(got))

	got, err = service.RequestSelector(ctx, "flagSetId=b")
	require.NoError(t, err, "a conflict is no error: the request matches no flag")
	require.True(t, service.MatchesNoFlags(got))
}

func TestRequestSelectorWithInvalidBinding(t *testing.T) {
	ctx := middleware.BindSelector(context.Background(), "color=red")

	_, err := service.RequestSelector(ctx, "flagSetId=a")
	require.ErrorIs(t, err, service.ErrInvalidBoundSelector)
	_, _, cause := middleware.BoundSelector(ctx)
	require.Equal(t, cause.Error(), err.Error(), "the message is the cause's")

	_, err = service.RequestSelector(ctx, "color=blue")
	require.NotErrorIs(t, err, service.ErrInvalidBoundSelector, "the client's error comes first")
}
