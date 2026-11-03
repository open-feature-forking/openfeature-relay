// Package evaluation is the former home of the helpers that build flagd's evaluator. They live in
// evaluator/flagd now; this package forwards to them so that code and tests written against it keep working.
package evaluation

import (
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	flagdeval "github.com/open-feature-forking/openfeature-relay/evaluator/flagd"
)

// NewEvaluator builds the evaluator over the given store: flagd's JsonLogic evaluator behind the evaluation
// router.
//
// It is evaluator/flagd's NewEvaluator.
func NewEvaluator(log *logger.Logger, s store.IStore, opts ...flagdeval.JSONEvaluatorOption) evaluator.IEvaluator {
	return flagdeval.NewEvaluator(log, s, opts...)
}

// NewRoutedEvaluator builds the evaluator of a distribution whose configuration formats are formats (besides
// flagd's own) and whose evaluators, besides flagd's, are extra.
//
// It is evaluator/flagd's NewRoutedEvaluator.
func NewRoutedEvaluator(
	log *logger.Logger, st *store.Store, formats []string, extra []evaluator.Factory, opts ...flagdeval.JSONEvaluatorOption,
) (evaluator.IEvaluator, error) {
	return flagdeval.NewRoutedEvaluator(log, st, formats, extra, opts...)
}
