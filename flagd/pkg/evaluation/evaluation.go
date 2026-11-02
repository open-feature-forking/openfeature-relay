// Package evaluation builds the evaluator flagd serves flags with.
package evaluation

import (
	"github.com/open-feature/flagd/core/pkg/evaluator"
	"github.com/open-feature/flagd/core/pkg/logger"
	"github.com/open-feature/flagd/core/pkg/store"
	"go.opentelemetry.io/otel/trace/noop"
)

// NewEvaluator builds the evaluator over the given store. The evaluation and flagSync spans are
// recorded by flagd, so the evaluator's own spans go nowhere.
func NewEvaluator(log *logger.Logger, s store.IStore, opts ...evaluator.JSONEvaluatorOption) evaluator.IEvaluator {
	opts = append([]evaluator.JSONEvaluatorOption{evaluator.WithTracerProvider(noop.NewTracerProvider())}, opts...)
	return evaluator.NewJSON(log, s, opts...)
}
