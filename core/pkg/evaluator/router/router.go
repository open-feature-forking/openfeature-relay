// Package router implements evaluator.Evaluator over several evaluators, one per configuration format. It
// finds the evaluator that owns a flag from the documents the formats stored and hands it the request.
package router

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/evaluator"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/model"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
)

// Route sends the flags of one configuration format to an evaluator.
type Route struct {
	Format    string
	Evaluator evaluator.Evaluator
}

// Router is an evaluator.Evaluator over the routes it was built with.
type Router struct {
	store  *store.Store
	routes []Route
}

var _ evaluator.Evaluator = (*Router)(nil)

// New returns a router over routes. A router with one route hands every request to it and needs no store;
// otherwise the store holds the documents that decide where a flag goes. Each format has one route.
// Close closes the evaluator of every route, so an evaluator in two routes is closed twice.
func New(st *store.Store, routes ...Route) (*Router, error) {
	if len(routes) == 0 {
		return nil, errors.New("a router needs at least one evaluator")
	}
	seen := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		if route.Evaluator == nil || route.Format == "" {
			return nil, errors.New("a route needs a configuration format and an evaluator")
		}
		if _, dup := seen[route.Format]; dup {
			return nil, fmt.Errorf("two evaluators for the configuration format %q", route.Format)
		}
		seen[route.Format] = struct{}{}
	}
	if len(routes) > 1 && st == nil {
		return nil, errors.New("a router over several evaluators needs a store")
	}
	return &Router{store: st, routes: routes}, nil
}

// entry is a flag a document holds.
type entry struct {
	flagSet  string
	key      string
	priority int
	route    int
}

// Evaluate implements evaluator.Evaluator.
//
// With one evaluator the request goes to it unchanged. Otherwise the router reads the documents the selector
// names through one view and, for each (flag set, key), picks the evaluator of the document from the
// highest-priority source. A bulk request gets one result for each pick; a single key goes to the evaluator of
// the highest-priority source, which decides between the flag sets of that source as it does without a router.
// A key no document holds is FLAG_NOT_FOUND. The metadata returned is the selector's, extended by what the
// evaluators return.
//
// The router routes from its own view and each evaluator then reads its own, so a request across several
// formats is not one snapshot: a delivery between the two reads can move a key. Consistency (BR-011) holds
// for each evaluator.
func (r *Router) Evaluate(ctx context.Context, req evaluator.Request, dst []evaluator.Result) ([]evaluator.Result, model.Metadata, error) {
	if len(r.routes) == 1 {
		return r.evaluate(ctx, r.routes[0], req, dst)
	}

	view := r.store.View()
	wanted := req.WantedKeys()
	entries := r.collect(view, req.Selector, wanted)
	meta := req.Selector.ToMetadata()

	if !req.Bulk && req.FlagKeys == nil {
		return r.single(ctx, req, entries, dst, meta)
	}

	type pair struct{ flagSet, key string }
	winners := map[pair]entry{}
	for _, e := range entries {
		p := pair{e.flagSet, e.key}
		if best, ok := winners[p]; !ok || e.priority > best.priority {
			winners[p] = e
		}
	}

	keysOf := make([][]string, len(r.routes))
	for p, e := range winners {
		if !slices.Contains(keysOf[e.route], p.key) {
			keysOf[e.route] = append(keysOf[e.route], p.key)
		}
	}

	var results []evaluator.Result
	merged := model.Metadata{}
	for k, v := range meta {
		merged[k] = v
	}
	for i, route := range r.routes {
		if len(keysOf[i]) == 0 {
			continue
		}
		sort.Strings(keysOf[i])
		sub := req
		sub.FlagKeys, sub.FlagKey = keysOf[i], ""
		out, routeMeta, err := route.Evaluator.Evaluate(ctx, sub, nil)
		if err != nil {
			for _, key := range keysOf[i] {
				results = append(results, evaluator.Result{
					FlagKey: key, Reason: model.ErrorReason, ErrorCode: model.GeneralErrorCode, Format: route.Format,
					ErrorMessage: model.GetErrorMessage(model.GeneralErrorCode), Metadata: meta,
				})
			}
			continue
		}
		for k, v := range routeMeta {
			if _, ok := merged[k]; !ok {
				merged[k] = v
			}
		}
		for _, res := range out {
			// an evaluator answers for every flag set holding a key, including those another format won
			if w, ok := winners[pair{res.FlagSet, res.FlagKey}]; ok && w.route != i {
				continue
			}
			res.Format = route.Format
			results = append(results, res)
		}
	}

	if req.FlagKeys != nil && !req.Bulk {
		// an explicit list answers for every key it names
		for _, key := range req.FlagKeys {
			if !hasResult(results, key) {
				results = append(results, notFound(key, meta))
			}
		}
	}

	sort.SliceStable(results, func(a, b int) bool {
		if !req.Bulk {
			return slices.Index(req.FlagKeys, results[a].FlagKey) < slices.Index(req.FlagKeys, results[b].FlagKey)
		}
		if results[a].FlagSet != results[b].FlagSet {
			return results[a].FlagSet < results[b].FlagSet
		}
		return results[a].FlagKey < results[b].FlagKey
	})
	return append(dst, results...), merged, nil
}

func (r *Router) single(
	ctx context.Context, req evaluator.Request, entries []entry, dst []evaluator.Result, meta model.Metadata,
) ([]evaluator.Result, model.Metadata, error) {
	best := -1
	for i, e := range entries {
		if best == -1 || e.priority > entries[best].priority {
			best = i
		}
	}
	if best == -1 {
		return append(dst, notFound(req.FlagKey, meta)), meta, nil
	}
	return r.evaluate(ctx, r.routes[entries[best].route], req, dst)
}

// evaluate hands the request to the route and stamps the results it appended with the route's format.
func (r *Router) evaluate(ctx context.Context, route Route, req evaluator.Request, dst []evaluator.Result) ([]evaluator.Result, model.Metadata, error) {
	from := len(dst)
	out, meta, err := route.Evaluator.Evaluate(ctx, req, dst)
	for i := from; i < len(out); i++ {
		out[i].Format = route.Format
	}
	return out, meta, err
}

// Evaluator returns the evaluator of a configuration format, for a caller that needs its native results.
func (r *Router) Evaluator(format string) (evaluator.Evaluator, bool) {
	for _, route := range r.routes {
		if route.Format == format {
			return route.Evaluator, true
		}
	}
	return nil, false
}

// collect lists the flags the documents of all routes hold, restricted by the selector and the wanted keys.
func (r *Router) collect(view store.View, selector store.Selector, wanted map[string]struct{}) []entry {
	flagSet, flagSetNamed := selector.DocumentFlagSet()
	onlySource := selector.Source()

	var entries []entry
	for priority, source := range r.store.Sources() {
		if onlySource != "" && source != onlySource {
			continue
		}
		for i, route := range r.routes {
			for _, doc := range view.Documents(route.Format, source) {
				if flagSetNamed && doc.FlagSet != flagSet {
					continue
				}
				for _, key := range doc.FlagKeys {
					if wanted != nil {
						if _, ok := wanted[key]; !ok {
							continue
						}
					}
					entries = append(entries, entry{flagSet: doc.FlagSet, key: key, priority: priority, route: i})
				}
			}
		}
	}
	return entries
}

func notFound(key string, meta model.Metadata) evaluator.Result {
	return evaluator.Result{
		FlagKey: key, Reason: model.ErrorReason, ErrorCode: model.FlagNotFoundErrorCode,
		ErrorMessage: model.GetErrorMessage(model.FlagNotFoundErrorCode), Metadata: meta,
	}
}

func hasResult(results []evaluator.Result, key string) bool {
	for _, res := range results {
		if res.FlagKey == key {
			return true
		}
	}
	return false
}

// Close closes the evaluators of the routes that are an io.Closer, and returns their errors joined.
func (r *Router) Close() error {
	return closeEvaluators(r.routes)
}

// closeEvaluators closes the evaluators of routes that are an io.Closer, and returns their errors joined.
func closeEvaluators(routes []Route) error {
	var errs []error
	for _, route := range routes {
		if closer, ok := route.Evaluator.(io.Closer); ok {
			errs = append(errs, closer.Close())
		}
	}
	return errors.Join(errs...)
}
