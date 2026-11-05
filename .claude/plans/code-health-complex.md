# Code health: the complex functions (#177, findings 1, 2 and 4)

Plan for the findings of `code-health.md` that sit on observable behaviour: `FromConfig` (1), the evaluation paths
(2) and four settings parsers (4). Measured on `main` at `348c0233`. No open PR touches the six files; #163 and
`uc-022/pr-6-runtime-reset` (#162), which finding 1 waited for, are merged.

## Scope

| # | Function | File |
|---|----------|------|
| 1 | `FromConfig` | `runtime/from_config.go:104` |
| 2a | `Router.Evaluate`, `Router.collect` | `core/pkg/evaluator/router/router.go:73`, `:197` |
| 2b | `Evaluator.Evaluate` (Flagsmith) | `contrib/flagsmith/engine/evaluator.go:85` |
| 4a | `parseEvent` | `service/event-collector/collector.go:268` |
| 4b | `parseHeadersFrom` | `sync/sse/trigger.go:182` |
| 4c | `open`, `configOptions` | `sync/s3/s3.go:85`, `:162` |

Out of scope: the other parsers of finding 4 (`OptionsFrom`, `loadExtensionSettings`, `manifest.Parse`,
`needs.Check`), the shared `wantedKeys` (finding 5, an API decision), the shared settings keys (6) and the two header
parsers (7). Findings 5–7 touch `router.go`, `evaluator.go`, `collector.go` and `trigger.go` in other functions: the
refactors here leave `wantedKeys`, `decodeConfig` and `parseHeaders` where they are, so either side can land first
and the other merges up.

**The rule:** pure refactors under the drop-in rule. Unchanged: results, error texts (including `%v` against `%w`
wrapping), which error wins when two inputs are wrong, log lines and their fields, the order of side effects and of
cleanup, and allocs/op on the evaluation path. Equal allocs/op is the gate: a refactor that allocates *less* changed
shape and goes back too; optimisations get their own PR. Quirks are pinned, not fixed (for example `rate_limiter_capacity`
reporting `invalid value for capacity`).

## Characterization first

Each area gets a tests-only PR on unchanged code. The refactor PR above it adds tests at most; `git diff <parent> --
'*_test.go'` shows no changed or deleted line in the pinning tests.

### 1. `FromConfig`

**Pinned today:** telemetry `Create` error text (`TestUnbuildableTelemetryProviderStopsFromConfig`); telemetry shutdown
on failure (`TestTelemetryProviderIsShutDownWhenFromConfigFails`); listeners of created endpoints released on a
failed build, and alternating failed/valid builds leak no goroutine or fd (`failed_build_test.go`, linux); the
endpoint `Create` error text (`serve_error_characterization_test.go`); the `staleAfter` error (`runtime_test.go:300`);
`names no configuration format` (substring, `endpoints_test.go`); the event-sinks-need-metrics refusal (behaviour,
`operator_settings_test.go`); the skipped-source log (substring, at `sourcesWithKnownFormat`, not through
`FromConfig`); sink close on a later sink's failure; shutdown order (`shutdown_order_test.go`).

**Missing, add in `runtime/from_config_characterization_test.go`:**

- `TestFromConfigErrorTexts`: one row per `return nil, err`, exact `EqualError`: metrics refusal (`... serves none`),
  `error creating flag store: ...` (if `store.NewStore` can fail from a config; otherwise note it unreachable),
  `error creating configuration format %q for source %q`, the default-format `error creating configuration format %q`
  and `source %q names no configuration format ...; available formats: ...`, `newEndpointInputs` errors, the middleware
  `at position %d declares` and `error creating the middleware`, `the service endpoint %q declares`,
  `could not create sync sources from config: ...`, the event sink errors.
- `TestFromConfigCallOrder`: recording fakes for telemetry, configuration format, evaluator, middleware, two endpoints,
  sync and event sink append to one log; a valid build pins the sequence telemetry → formats per source → default
  format → evaluators → middleware → endpoint 1 validate/create → endpoint 2 → syncs → sinks, and the `onShutdown`
  registration order (event sinks, then telemetry).
- `TestFromConfigFirstFailureWins`: two faults at once (bad middleware and a failing endpoint; unknown default format
  and a bad sync), the earlier step's error is the one returned.
- `TestFailedFromConfigCleanupOrder`: a failure after an endpoint was created closes the endpoint (`io.Closer`) and
  *then* shuts telemetry down (defers are LIFO); a failure before any endpoint shuts telemetry down and closes
  nothing; a failure inside `telemetryProvider` neither.
- `TestFromConfigLogLines`: observed logger; `warnOnNoOpConfig` text, the skipped-source error through `FromConfig`,
  and the `component` fields (`runtime`, `sync`, `configformat`, `middleware`).
- `TestEndpointSettingsAreNotShared`: two endpoints get distinct `Settings.Formats` slices (today a fresh `append` per
  endpoint) and the `Sources`, `Telemetry`, `HTTPMiddleware`, `Listen`, `Initialized` values of the build.

### 2a. Router

**Pinned today** (`router_test.go`): source precedence, selector scoping, the same-source flag-set tie, unknown keys,
an evaluator error as `GENERAL` for its keys, the single-route fast path, format stamping, metadata merge. **No
benchmark exists**, so CI compares nothing for the router.

**Missing:**

- `TestRouterResultOrder`: bulk sorted by (flag set, key); explicit `FlagKeys` in request order, with `FLAG_NOT_FOUND`
  filled in for missing keys, duplicates in `FlagKeys`, a key one route wins in one flag set and another route in a
  second flag set.
- `TestRouterAppendsToDst`: a non-empty `dst` keeps its prefix on every path (fast path, single, bulk, error).
- `TestRouterMetadataAliasing`: the error and not-found results carry the selector's metadata map, the returned
  metadata is a copy extended by first-route-wins keys (pin by identity where it is shared today).
- `TestRouterSubRequests`: a recording evaluator sees `FlagKey == ""`, the sorted keys of its route and a `nil` dst.
- `BenchmarkRouterEvaluate` in `router_test.go` (core is in `GO_LIB_MODULE_DIRS`, so CI compares it once it is on the
  base): `one-route`, `two-routes/single`, `two-routes/bulk`, `two-routes/keys`, `two-routes/selector`, with
  `b.ReportAllocs()` and stub evaluators that allocate nothing, so the router's own allocations show.

### 2b. Flagsmith `Evaluate`

**Pinned today** (`evaluator_test.go`, `vectors_test.go`, `segments_test.go`): the evaluator contract kit, values by
type, type mismatch, unknown key, reasons, both boolean modes, hide-disabled for client keys, flag sets as
environments, later source wins, explicit keys of a bulk request, equality with `flagengine`. `BenchmarkEvaluate`
exists, but `contrib/flagsmith/engine` is not in `run-benchmarks.sh`, so CI does not compare it yet; A2 adds it (Q1).

**Missing:**

- `TestEvaluateTieBetweenFlagSets`: one key in two flag sets at the same priority, single key: the first flag set in
  sorted order wins.
- `TestEvaluateAppendsToDst` and `TestEvaluateOrder`, as for the router.
- `TestEvaluateSkipsForeignDocuments`: a document whose `Value` is not `*document` is skipped, not an error.
- Sub-benchmarks of `BenchmarkEvaluate` for single key, explicit keys and bulk, if it does not have them.

### 4. Settings parsers

Three of the four iterate a map: with two faulty entries the reported one is random today (`parseEvent`'s `fields`,
every branch of `parseHeadersFrom`, `configOptions` over the query). Tests pin single-fault inputs exactly and, for
multi-fault inputs, that the error is one of the possible texts (Q2).

- **`parseEvent`** — pinned by substring through HTTP (`TestCollectorRejectsInvalidEvents`). Add
  `TestParseEvent` in `collector_internal_test.go`: exact reason and the full returned event (`require.Equal`) per
  row, each field's wrong type, `time` absent/empty/invalid, `value` integer/float/nested, `hasValue` overriding a
  present `value`, tracking `value: null` and `details: null`, and the precedence rows: bad `time` with unknown `kind`
  reports `time`; bad `time` with missing `flagKey` reports `time`; wrong-typed field with missing `flagKey` reports
  the field.
- **`parseHeadersFrom`** — pinned by substring (`TestSettings`, `TestHeadersFrom*`). Add `TestParseHeadersFrom`:
  exact errors and returned map for every accepted Go type (`map[string]secret.Ref`, `map[string]map[string]string`,
  `map[string]any` with `secret.Ref`, `map[string]string` and `map[string]any` entries), empty input maps (`nil`
  result, not an empty map), a non-text value under a key other than `env`/`file` (today `""`, then the unknown-key
  error), an unknown key.
- **`open` / `configOptions`** — `TestQueryOptions` pins most texts exactly. Add rows for invalid `hostname_immutable`,
  `dualstack`, `fips`, `anonymous`, `rate_limiter_capacity`, `disable_https`, and an unparsable URL; precedence rows
  (`ssetype=nope&foo=bar` reports `ssetype`; `accelerate=maybe&use_path_style=maybe` reports `accelerate`;
  `use_path_style=true&s3ForcePathStyle=false` gives path style off); and a test that reads the built client's
  options (`UsePathStyle`, `BaseEndpoint`, `UseAccelerate`, `DisableHTTPS`, retryer attempts) for the
  `endpoint`/`hostname_immutable` combinations.

## Target shape

### 1. `FromConfig`

```go
func FromConfig(logger, version, config, components) (rt *Runtime, err error) {
	config = withComponents(config, components)
	warnOnNoOpConfig(logger, config)
	tp, shutdownTelemetry, err := telemetryProvider(logger, version, components)
	...
	defer func() { if rt == nil && shutdownTelemetry != nil { ... } }()  // stays here, unchanged
	if err := checkSinkMetrics(config, tp, components); err != nil { return nil, err }
	syncProviders, sources, readiness := sourcesOf(logger, config)
	store, formats, defaultFormat, formatTypes, err := formatsOf(logger, syncProviders, sources, config)
	...
	rt = newRuntime(logger, tp, config, components, store, formats, defaultFormat, readiness)
	var created []service.Endpoint
	defer func() { if rt == nil { closeEndpoints(created) } }()     // stays here, unchanged
	if err := rt.createEndpoints(logger, config, components, tp, formatTypes, sources, &created); err != nil {
		return nil, err
	}
	if err := rt.startSyncs(logger, config, syncProviders, tp); err != nil { return nil, err }
	if err := rt.startSinks(logger, components, tp, shutdownTelemetry); err != nil { return nil, err }
	return rt, nil
}
```

- Both `defer`s stay in `FromConfig`: a `defer` in a helper runs when the helper returns. Both read the named result
  `rt`, so every failure path stays `return nil, err`, never `return rt, err`.
- `createEndpoints` appends to the caller's `created` through a pointer, endpoint by endpoint, so a failure at
  endpoint *n* still closes endpoints 1..*n*-1. It covers `newEndpointInputs`, `rt.Evaluator`, `httpMiddleware` and
  the loop, in today's order; `Settings.Formats` stays a fresh slice per endpoint.
- `startSinks` registers `onShutdown` for event sinks before telemetry, as today.
- `metricsHandler` stays called twice (check and runtime), so a provider sees the same calls.
- `// nolint: funlen` goes. Target: `FromConfig` ≤ 60 lines, cyclomatic ≤ 15, cognitive ≤ 20, no helper above them.

### 2a. Router

- `Evaluate`: fast path; view, `wantedKeys`, `collect`, `meta`; `single` (unchanged) or `bulk`.
- `bulk(ctx, req, entries, dst, meta)`: `winnersOf(entries)`, `keysPerRoute(winners)`, `evaluateRoutes` (the per-route
  loop with the error branch as `routeFailed(route, keys, meta)` and the winner filter), `fillNotFound`,
  `sortResults(req, results)`, then `append(dst, results...)` as today.
- `collect`: the per-document loop moves to `appendEntries(entries, doc, priority, route, wanted)`.
- `pair` moves to package level. No change to the `results`/`merged` construction, the sort closure, or the
  two-step append into `dst`.

### 2b. Flagsmith `Evaluate`

- `Evaluate`: request facts (`meta`, selector, `wanted`, identity, client), `e.bestHits(...)`, `sortedHits(best)`,
  then `appendAll` for an unrestricted bulk request or `appendByKey(dst, ordered, keys, meta)`.
- `bestHits` keeps the source × document × engine-result loops and calls `resolve` for every candidate, as today;
  resolving only the winners would save allocations and is not this pass.
- The `keys := []string{req.FlagKey}` fallback stays in `Evaluate` or moves with `appendByKey` only if escape
  analysis is unchanged (see gates).

### 4. Settings parsers

- `parseEvent`: `parseEvaluation(item, when)` and `parseTracking(item, when)`; the `fields` map and its loop move
  into `parseEvaluation` as a map, so the iteration stays a map's (Q2). `optTime` stays before the `kind` switch.
- `parseHeadersFrom`: one helper per accepted type (`refsFromRefs`, `refsFromStringMaps`, `refsFromAny`) and
  `refFromAny(name, v)` for the inner switch; the lazily created `out` map stays (nil for empty input).
- `open`: `checkSSEType(q)`, `clientOptionsOf(q) (opts, pathStyle, err)` with today's order accelerate →
  disable_https → use_path_style → s3ForcePathStyle; then `configOptions`, `LoadDefaultConfig`, the retryer. Both
  `open bucket %v: %v` wraps stay `%v`.
- `configOptions`: return a `sharedOptions` struct instead of five results; one small parser per key
  (`boolOption(name, value)` keeping each key's own text, which differ: `invalid value for hostname_immutable` vs
  `invalid value for capacity`); still one `range q`.

## Gates

Inside WSL from the repository root, `GOTOOLCHAIN=go1.27.3`.

| Gate | Command | Applies to |
|------|---------|------------|
| Tests, race | `go test -race -count=3 ./runtime/... ./core/pkg/evaluator/... ./contrib/flagsmith/engine/... ./service/event-collector/... ./sync/sse/... ./sync/s3/...` (the package of the PR) | every PR |
| Characterization holds | the tests-only PR is green on unchanged code; the refactor PR's `git diff <parent> -- '*_test.go'` adds lines only | every refactor PR |
| Complexity | `gocyclo -over 15` and `gocognit -over 20` print none of the scope's functions or their new helpers; `funlen` passes without `nolint` | every refactor PR |
| allocs/op | CI `Benchmarks (allocs/op)`, equal to base for `BenchmarkRouterEvaluate/*`, `BenchmarkConnectResolve*`, `BenchmarkOFREP*` and the Flagsmith `BenchmarkEvaluate/*` (in the CI set from A2 on, Q1) | 2a, 2b |
| Escape analysis | `go build -gcflags=-m ./core/pkg/evaluator/router` (and the engine package) on parent and head, line numbers stripped, sorted, diffed: no new `escapes to heap` / `moved to heap` | 2a, 2b |
| ns/op | reviewer: `-count=10` parent and head, `benchstat`; no regression above 5% with p < 0.05 | 2a, 2b |
| Acceptance | CI integration suite (flagd-full goes through the router's fast path) | 2a |
| Load smoke | CI `load-smoke` | 1, 2a |
| Spec | none: no spec file changes | — |

## Slicing

| PR | Branch | Base | Content | Role |
|----|--------|------|---------|------|
| A1 | `health/pin-from-config` | `main` | the `FromConfig` characterization tests | implementer |
| A2 | `health/pin-evaluation` | `main` | router and Flagsmith characterization tests, `BenchmarkRouterEvaluate`, Flagsmith sub-benchmarks, and `./contrib/flagsmith/engine` in `run-benchmarks.sh` | implementer |
| A3 | `health/pin-settings-parsers` | `main` | `TestParseEvent`, `TestParseHeadersFrom`, the s3 rows and client-options test | implementer |
| B1 | `health/from-config-extract` | A1 | the `FromConfig` shape, one commit per helper | implementer |
| B2 | `health/router-extract` | A2 | the router shape | implementer |
| B3 | `health/flagsmith-evaluate-extract` | A2 | the Flagsmith shape | implementer |
| B4 | `health/settings-parsers-extract` | A3 | one commit per function: `parseEvent`, `parseHeadersFrom`, `open`, `configOptions` | implementer |

A1–A3 run in parallel; each B stacks on its A, so the allocs comparison of B2/B3 sees the new benchmarks on its base.
B2 and B3 are separate PRs so an allocation change points at one evaluator. `gofmt` and merge-ups go to a mechanic.
Each PR starts `Part of #177`; #177's PR list gets the seven.

## Risks

- **Deferred cleanup (1):** moving a `defer` into a helper, capturing `created` by value, or returning a non-nil `rt`
  with an error silently drops a cleanup. `TestFailedFromConfigCleanupOrder` and `failed_build_test.go` (linux only:
  the reviewer runs it in WSL, CI runs it) catch each.
- **Side-effect order (1):** reordering steps changes which error an operator sees for a doubly broken config and
  what gets created before a failure. `TestFromConfigCallOrder` and `TestFromConfigFirstFailureWins`.
- **Allocations (2):** a map, the `keys` slice or the sort closure escaping through a new helper adds an allocation
  per request (NFR-001). The escape-analysis diff shows the cause; the allocs/op comparison shows the cost. The router's
  multi-route path has no benchmark today, so A2 must merge (or be B2's base) before B2's numbers mean anything.
- **Result order and ties (2):** map iteration in `winners`/`best` is hidden by the sorts and the priority
  comparisons; a helper that sorts or breaks ties differently changes what clients see. The tie and order tests.
- **Random error choice (4):** a refactor that turns a map loop into a slice loop makes the choice deterministic: no
  failing test, but a behaviour change. Ruled out (Q2).
- **Error wrapping (4):** `%v` → `%w` changes no text but changes `errors.Is`; keep the verbs.
- **Merge conflicts:** findings 5–7 of the parallel health session touch the same files (other functions); merge up.
- C-005 and the evaluator boundary (NFR-005) are not touched: no imports move, no protobuf changes.

## Decisions of the project lead

**Q1. Should CI compare the Flagsmith engine's allocs/op?** `contrib/flagsmith/engine` is not in the benchmark set.
**Decided (a):** A2 adds `./contrib/flagsmith/engine` to the fixed list in `run-benchmarks.sh`, so B3 and every later
PR are compared in CI (~10 s more per run). The local Flagsmith comparison in the allocs/op gate falls away.

**Q2. The random error choice in the parsers.** With two faulty entries, `parseEvent`, `parseHeadersFrom` and
`configOptions` report a random one today. **Decided (a):** keep the map iteration, so the refactor changes nothing;
pin single-fault texts exactly and "one of" for multi-fault inputs. A deterministic order can follow as its own PR.
