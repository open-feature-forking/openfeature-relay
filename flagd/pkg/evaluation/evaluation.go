// Package evaluation builds the evaluator flagd serves flags with.
package evaluation

import (
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	flagdeval "github.com/open-feature-forking/openfeature-relay/evaluator/flagd"
)

// NewEvaluator builds the evaluator over the given store.
func NewEvaluator(log *logger.Logger, s store.IStore, opts ...flagdeval.JSONEvaluatorOption) evaluator.IEvaluator {
	return flagdeval.NewJSON(log, s, opts...)
}
