// Package inprocess is the skeleton of an in-process provider that embeds flagd's evaluator: an empty
// store, flagd's configuration format and the evaluator over that same store, and a sync provider whose
// output feeds the configuration format. A provider author copies this wiring into the provider's own
// module and puts the OpenFeature SDK's types around the resolutions.
//
// The module has no telemetry or platform client in its module graph and a single transport, gRPC, through the
// gRPC sync provider; observability comes
// from OpenFeature hooks the provider's users register, never from this code.
package inprocess

import (
	"context"
	"fmt"

	flagdformat "github.com/open-feature-forking/openfeature-relay/configformat/flagd"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	flagdeval "github.com/open-feature-forking/openfeature-relay/evaluator/flagd"
)

// Provider evaluates the flags its sync provider delivers.
type Provider struct {
	// OnChange receives the report of every flag configuration the configuration format accepted: which
	// flags it holds and which of them were added, updated or removed.
	OnChange func(configformat.Result)
	// OnError receives the error of every flag configuration the configuration format rejected. The flags
	// loaded before stay in the store.
	OnError func(error)

	store  *store.Store
	format *flagdformat.Format
	eval   evaluator.IEvaluator
	syncer coresync.ISync
}

// New wires a provider over the sync provider. source is the name under which the sync provider delivers
// its flag configurations (DataSync.Source), which is its URI for the official sync providers.
func New(log *logger.Logger, source string, syncer coresync.ISync) (*Provider, error) {
	st, err := store.NewStore(log, []string{source})
	if err != nil {
		return nil, fmt.Errorf("creating the store: %w", err)
	}
	return &Provider{
		store:  st,
		format: flagdformat.NewFormat(st, log),
		eval:   flagdeval.NewJSON(log, st),
		syncer: syncer,
	}, nil
}

// Store gives access to the documents the configuration format keeps.
func (p *Provider) Store() *store.Store { return p.store }

// Start initializes the sync provider and feeds what it emits to the configuration format until ctx ends.
func (p *Provider) Start(ctx context.Context) error {
	if err := p.syncer.Init(ctx); err != nil {
		return fmt.Errorf("initializing the sync provider: %w", err)
	}
	payloads := make(chan coresync.DataSync, 16)
	go func() { _ = p.syncer.Sync(ctx, payloads) }()
	go func() {
		for {
			select {
			case payload := <-payloads:
				p.apply(ctx, payload)
			case <-ctx.Done():
				return
			}
		}
	}()
	return nil
}

func (p *Provider) apply(ctx context.Context, payload coresync.DataSync) {
	result, err := p.format.Accept(ctx, configformat.Input{
		Source: payload.Source, Data: payload.FlagData, Selector: payload.Selector, IncrementalUpdate: payload.IncrementalUpdates,
	})
	switch {
	case err != nil && p.OnError != nil:
		p.OnError(err)
	case err == nil && p.OnChange != nil:
		p.OnChange(result)
	}
}

// Resolution is what a resolution reports to the OpenFeature SDK: the value, or the default with the
// error code and reason that explain why.
type Resolution[T any] struct {
	Value     T
	Variant   string
	Reason    string
	ErrorCode string
	Metadata  model.Metadata
}

// Boolean resolves a boolean flag for the evaluation context.
func (p *Provider) Boolean(ctx context.Context, key string, def bool, evalCtx map[string]any) Resolution[bool] {
	value, variant, reason, metadata, err := p.eval.ResolveBooleanValue(ctx, "inprocess", key, evalCtx)
	return report(def, value, variant, reason, metadata, err)
}

// String resolves a string flag for the evaluation context.
func (p *Provider) String(ctx context.Context, key string, def string, evalCtx map[string]any) Resolution[string] {
	value, variant, reason, metadata, err := p.eval.ResolveStringValue(ctx, "inprocess", key, evalCtx)
	return report(def, value, variant, reason, metadata, err)
}

// report translates the evaluator's result the way remote evaluation reports it: an error resolves to the
// default with its code and reason ERROR, a disabled flag to the default with reason DISABLED.
func report[T any](def, value T, variant, reason string, metadata model.Metadata, err error) Resolution[T] {
	switch {
	case err != nil && err.Error() == model.FlagDisabledErrorCode:
		return Resolution[T]{Value: def, Reason: model.DisabledReason}
	case err != nil:
		return Resolution[T]{Value: def, Reason: model.ErrorReason, ErrorCode: err.Error()}
	case variant == "":
		// no variant to return: the default stands, under the evaluator's reason (DISABLED, DEFAULT)
		return Resolution[T]{Value: def, Reason: publicReason(reason), Metadata: metadata}
	}
	return Resolution[T]{Value: value, Variant: variant, Reason: publicReason(reason), Metadata: metadata}
}

// publicReason maps the evaluator's internal FALLBACK reason to the DEFAULT that remote evaluation reports.
func publicReason(reason string) string {
	if reason == model.FallbackReason {
		return model.DefaultReason
	}
	return reason
}
