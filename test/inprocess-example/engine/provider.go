// Package inprocess is the skeleton of an in-process provider that embeds a vendor's engine module instead of
// flagd's: the engine's configuration format, its paired evaluator over the same empty store, and a sync
// provider whose output feeds the configuration format. This example embeds the keyvalue engine module of the
// configuration format example; the provider carries no flagd configuration format or evaluator (UC-014 A2).
package inprocess

import (
	"context"
	"fmt"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/configformat"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator/typed"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	coresync "github.com/open-feature-forking/openfeature-relay/core/pkg/sync"

	keyvalue "github.com/open-feature/flagd/test/configformat-example"
	keyvalueeval "github.com/open-feature/flagd/test/configformat-example/evaluator"
)

// Provider evaluates the flags its sync provider delivers.
type Provider struct {
	// OnChange receives the report of every flag configuration the configuration format accepted.
	OnChange func(configformat.Result)
	// OnError receives the error of every flag configuration the configuration format rejected.
	OnError func(error)

	store  *store.Store
	format configformat.ConfigurationFormat
	eval   evaluator.IResolver
	syncer coresync.ISync
}

// New wires a provider over the sync provider; source is the name under which it delivers its flag
// configurations (DataSync.Source).
func New(log *logger.Logger, source string, syncer coresync.ISync) (*Provider, error) {
	st, err := store.NewStore(log, []string{source})
	if err != nil {
		return nil, fmt.Errorf("creating the store: %w", err)
	}
	format, err := keyvalue.Factory{}.Create(configformat.Settings{Store: st, Logger: log})
	if err != nil {
		return nil, fmt.Errorf("creating the configuration format: %w", err)
	}
	ev, err := keyvalueeval.Factory{}.Create(evaluator.Settings{Store: st, Logger: log})
	if err != nil {
		return nil, fmt.Errorf("creating the evaluator: %w", err)
	}
	return &Provider{store: st, format: format, eval: typed.New(ev), syncer: syncer}, nil
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

// Resolution is what a resolution reports to the OpenFeature SDK: the value, or the default with the error
// code and reason that explain why.
type Resolution[T any] struct {
	Value     T
	Variant   string
	Reason    string
	ErrorCode string
	Metadata  model.Metadata
}

// String resolves a string flag for the evaluation context.
func (p *Provider) String(ctx context.Context, key, def string, evalCtx map[string]any) Resolution[string] {
	value, variant, reason, metadata, err := p.eval.ResolveStringValue(ctx, "inprocess", key, evalCtx)
	return report(def, value, variant, reason, metadata, err)
}

// Boolean resolves a boolean flag for the evaluation context.
func (p *Provider) Boolean(ctx context.Context, key string, def bool, evalCtx map[string]any) Resolution[bool] {
	value, variant, reason, metadata, err := p.eval.ResolveBooleanValue(ctx, "inprocess", key, evalCtx)
	return report(def, value, variant, reason, metadata, err)
}

// report translates the evaluator's result the way remote evaluation reports it: an error resolves to the
// default with its code and reason ERROR, a flag without a value to the default under the evaluator's reason.
func report[T any](def, value T, variant, reason string, metadata model.Metadata, err error) Resolution[T] {
	switch {
	case err != nil:
		return Resolution[T]{Value: def, Reason: model.ErrorReason, ErrorCode: err.Error()}
	case variant == "":
		return Resolution[T]{Value: def, Reason: reason, Metadata: metadata}
	}
	return Resolution[T]{Value: value, Variant: variant, Reason: reason, Metadata: metadata}
}
