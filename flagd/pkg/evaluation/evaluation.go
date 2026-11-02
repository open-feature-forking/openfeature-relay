// Package evaluation builds the evaluator flagd serves flags with.
package evaluation

import (
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator/jsonlogic"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
)

// NewEvaluator builds the evaluator over the given store.
func NewEvaluator(log *logger.Logger, s store.IStore, opts ...jsonlogic.JSONEvaluatorOption) evaluator.IEvaluator {
	return jsonlogic.NewJSON(log, s, opts...)
}
