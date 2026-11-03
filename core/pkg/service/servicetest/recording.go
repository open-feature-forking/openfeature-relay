package servicetest

import (
	"context"
	"net/http"
	"sync"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/middleware"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
)

// recordingEvaluator resolves the flag the harness stores, answers FLAG_NOT_FOUND for any other, and records
// the requests it received.
type recordingEvaluator struct {
	mu   sync.Mutex
	seen []evaluator.Request
}

func (r *recordingEvaluator) Evaluate(_ context.Context, req evaluator.Request, dst []evaluator.Result) ([]evaluator.Result, model.Metadata, error) {
	r.mu.Lock()
	r.seen = append(r.seen, req)
	r.mu.Unlock()

	if req.Bulk || req.FlagKey == flagKey {
		return append(dst, evaluator.Result{
			FlagKey: flagKey, Value: true, HasValue: true, Variant: "on", Reason: model.StaticReason, Format: "flagd",
			Metadata: model.Metadata{"flagSetId": flagSet},
		}), nil, nil
	}
	return append(dst, evaluator.Result{FlagKey: req.FlagKey, ErrorCode: model.FlagNotFoundErrorCode, Format: "flagd"}), nil, nil
}

func (r *recordingEvaluator) requests() []evaluator.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]evaluator.Request(nil), r.seen...)
}

func (r *recordingEvaluator) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = nil
}

// bindingMiddleware binds the request attributes the contract looks for and no selector.
func bindingMiddleware(next http.Handler) http.Handler { return bindingMiddlewareFor("", next) }

// bindingMiddlewareFor binds the selector expression, when there is one, and the request attributes.
func bindingMiddlewareFor(selector string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := middleware.WithAttributes(r.Context(), map[string]any{"audience": "contract"})
		if selector != "" {
			ctx = middleware.BindSelector(ctx, selector)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
