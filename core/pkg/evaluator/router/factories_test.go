package router

import (
	"context"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/stretchr/testify/require"
)

type fakeFactory struct {
	name, format string
	got          *map[string]any
}

func (f fakeFactory) Type() string                { return f.name }
func (f fakeFactory) ConfigurationFormat() string { return f.format }
func (f fakeFactory) Create(s evaluator.Settings) (evaluator.Evaluator, error) {
	if f.got != nil {
		*f.got = s.Config
	}
	return &fake{name: f.name, flags: map[string][]string{"k": {""}}}, nil
}

func TestFromFactories(t *testing.T) {
	st := newStore(t, []string{"s"}, delivery{"a", "s", map[string][]string{"": {"k"}}})

	t.Run("a route for each evaluator, with its settings", func(t *testing.T) {
		var got map[string]any
		r, err := FromFactories(logger.NewLogger(nil, false), st, []string{"a", "b"},
			[]evaluator.Factory{fakeFactory{name: "ea", format: "a", got: &got}},
			func(evaluatorType string) map[string]any { return map[string]any{"for": evaluatorType} })
		require.NoError(t, err)

		out, _, err := r.Evaluate(context.Background(), evaluator.Request{FlagKey: "k"}, nil)
		require.NoError(t, err)
		require.Equal(t, "a", out[0].Format)
		require.Equal(t, map[string]any{"for": "ea"}, got)
	})

	t.Run("an evaluator of a format the distribution lacks", func(t *testing.T) {
		_, err := FromFactories(logger.NewLogger(nil, false), st, []string{"b", "a", "a"},
			[]evaluator.Factory{fakeFactory{name: "ez", format: "z"}}, nil)
		require.EqualError(t, err, `the evaluator "ez" resolves the configuration format "z", which the distribution lacks; it has: a, b`)
	})

	t.Run("no evaluator at all", func(t *testing.T) {
		_, err := FromFactories(logger.NewLogger(nil, false), st, []string{"a"}, nil, nil)
		require.ErrorContains(t, err, "the distribution has no evaluator")
		require.ErrorContains(t, err, "a")
	})
}
