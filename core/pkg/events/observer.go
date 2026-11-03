package events

import (
	"context"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
)

// targetingKey is the evaluation context key that holds the targeting key.
const targetingKey = "targetingKey"

// EvaluationObserver returns the observer the typed adapter of a service endpoint of the given type
// reports its resolutions to: it publishes an evaluation event for each resolved flag, origin service.
// It returns at once while nothing subscribes to evaluation events.
func EvaluationObserver(bus *Bus, endpoint string) func(context.Context, evaluator.Request, evaluator.Result) {
	return func(_ context.Context, req evaluator.Request, res evaluator.Result) {
		if !bus.HasSubscribers(KindEvaluation) {
			return
		}
		ev := Evaluation{
			Time:      time.Now(),
			FlagKey:   res.FlagKey,
			FlagSet:   flagSet(req, res),
			Format:    res.Format,
			Endpoint:  endpoint,
			Variant:   res.Variant,
			Reason:    res.Reason,
			ErrorCode: res.ErrorCode,
			Value:     res.Value,
			HasValue:  res.HasValue,
			Origin:    OriginService,
		}
		ev.TargetingKey, _ = req.Context[targetingKey].(string)
		bus.Publish(ev)
	}
}

// flagSet is the flag set the flag belongs to, as its metadata says, or else the one the selector names.
func flagSet(req evaluator.Request, res evaluator.Result) string {
	if id, ok := res.Metadata["flagSetId"].(string); ok {
		return id
	}
	return req.Selector.FlagSetId()
}
