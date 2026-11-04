package flagsmith_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"

	flagsmith "github.com/open-feature-forking/openfeature-relay/contrib/flagsmith/engine"
)

// TestEnginePairMatchesTypes pins the two factories an `engines` entry of a manifest is built from.
func TestEnginePairMatchesTypes(t *testing.T) {
	format, ev := flagsmith.NewConfigurationFormatFactory(), flagsmith.NewEvaluatorFactory()

	require.NoError(t, configformat.ValidateDeclarations(format))
	require.NoError(t, evaluator.ValidateDeclarations(ev))
	require.Equal(t, "flagsmith", format.Type())
	require.Equal(t, "flagsmith", ev.Type())
	require.Equal(t, format.Type(), ev.ConfigurationFormat(), "the evaluator resolves the format of the pair")
}

// TestEnginePairServesADocument builds both halves from the pair's factories, as a generated main does, delivers a
// document through the format and resolves a flag of it.
func TestEnginePairServesADocument(t *testing.T) {
	st, err := store.NewStore(logger.NewLogger(nil, false), []string{"src"})
	require.NoError(t, err)
	format, err := flagsmith.NewConfigurationFormatFactory().Create(configformat.Settings{Store: st})
	require.NoError(t, err)
	ev, err := flagsmith.NewEvaluatorFactory().Create(evaluator.Settings{Store: st})
	require.NoError(t, err)

	_, err = format.Accept(context.Background(), configformat.Input{Source: "src", Data: document(t, "basic.json")})
	require.NoError(t, err)

	r := one(t, ev, "welcome", evaluator.TypeString, nil, nil)
	require.Equal(t, "hello", r.Value)
	require.Equal(t, model.StaticReason, r.Reason)
}
