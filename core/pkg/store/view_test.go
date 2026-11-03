package store

import (
	"context"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/stretchr/testify/require"
)

func TestViewIsSnapshot(t *testing.T) {
	st, err := NewStore(logger.NewLogger(nil, false), []string{"src"})
	require.NoError(t, err)

	flag := func(key, variant string) model.Flag {
		return model.Flag{Key: key, State: "ENABLED", DefaultVariant: variant, Variants: map[string]any{variant: true}, Source: "src"}
	}
	st.Update("src", []model.Flag{flag("a", "one"), flag("b", "one")}, nil, false)

	view := st.View()

	st.Update("src", []model.Flag{flag("a", "two")}, nil, false)

	got, _, err := view.Get("a", &Selector{})
	require.NoError(t, err)
	require.Equal(t, "one", got.DefaultVariant, "the view keeps the state it was taken at")

	all, _, err := view.GetAll(&Selector{})
	require.NoError(t, err)
	require.Len(t, all, 2)

	_, _, err = view.Get("b", &Selector{})
	require.NoError(t, err)

	fresh, _, err := st.Get(context.Background(), "a", &Selector{})
	require.NoError(t, err)
	require.Equal(t, "two", fresh.DefaultVariant)
	_, _, err = st.Get(context.Background(), "b", &Selector{})
	require.Error(t, err)

	later := st.View()
	all, _, err = later.GetAll(&Selector{})
	require.NoError(t, err)
	require.Len(t, all, 1)
}

func TestViewDocumentsAreSnapshot(t *testing.T) {
	st, err := NewStore(logger.NewLogger(nil, false), []string{"src"})
	require.NoError(t, err)

	require.NoError(t, st.Apply(ApplyRequest{
		Format: "fmt", Source: "src",
		Documents: []Document{{Format: "fmt", Source: "src", FlagKeys: []string{"a"}, Value: 1}},
	}))
	view := st.View()
	require.NoError(t, st.Apply(ApplyRequest{
		Format: "fmt", Source: "src",
		Documents: []Document{{Format: "fmt", Source: "src", FlagKeys: []string{"a", "b"}, Value: 2}},
	}))

	require.Equal(t, []string{"a"}, view.Documents("fmt", "src")[0].FlagKeys)
	require.Equal(t, []string{"a", "b"}, st.View().Documents("fmt", "src")[0].FlagKeys)
}

func TestSourcesAreInPriorityOrderAndCopied(t *testing.T) {
	st, err := NewStore(logger.NewLogger(nil, false), []string{"low", "high"})
	require.NoError(t, err)

	sources := st.Sources()
	require.Equal(t, []string{"low", "high"}, sources)
	sources[0] = "changed"
	require.Equal(t, []string{"low", "high"}, st.Sources())
}

func TestSelectorDocumentFlagSet(t *testing.T) {
	for expression, want := range map[string]struct {
		set   string
		named bool
	}{
		"":              {"", false},
		"source=a":      {"", false},
		"flagSetId=x":   {"x", true},
		"flagSetId=":    {"", true},
		"flagSetId=a b": {"a b", true},
	} {
		selector, err := NewSelector(expression)
		require.NoError(t, err)
		set, named := selector.DocumentFlagSet()
		require.Equal(t, want.set, set, expression)
		require.Equal(t, want.named, named, expression)
	}
}
