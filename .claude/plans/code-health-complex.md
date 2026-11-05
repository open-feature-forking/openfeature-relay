# Code health: the complex functions (#177, findings 1, 2 and 4)

Plan for the findings of `code-health.md` that sit on observable behaviour: `FromConfig` (1), the evaluation paths
(2) and four settings parsers (4). Measured on `main` at `348c0233`; revision 2 at `c2011c40` folds in the opposer's
findings (`code-health-complex-opposer.md`, 0 blockers, 4 major, 6 minor) and the open PRs as of today.

## Revision 2: what changed

| Finding | Resolution |
|---------|------------|
| M1 open PRs touch these files | New section "Open PRs and order". A2 lands before #239; B2/B3 use `req.WantedKeys()`; A1 waits for #230; B4 after #237 and #238 |
| M2 the tests-only gate can be fooled | The gate names the pinning files and runs A's head versions of them on B's head; `git rebase -x` per commit for B1, B4 |
| M3 new benchmarks without a noise measurement | A2 measures both benchmarks 6×3 on unchanged code, adds `NOISY` entries with reasons where they spread, and its CI runs twice |
| M4 "one of" does not pin Q2 (a) | A 1,000-call test per two-fault row requires both texts to appear |
| m5 call order, missing texts | `TestFromConfigCallOrder` records `Close`/`Shutdown` too and has a row without a router; two error texts added |
| m6 router rows | Bulk with `FlagKeys`, error together with results, no error from the multi-route path |
| m7 Flagsmith sub-benchmarks exist | Keep the four names; add `keys/identity` and `keys/environment` beside them |
| m8 parser rows | Trailing data and duplicate keys in `parseEvent`; repeated and every-client-option rows in s3 |
| m9 equal allocs/op is a review rule | Said so in the Gates table |
| m10 escape diff is noisy | Criterion is no new heap allocation site on the request path, judged by the reviewer |

Q1 (a) and Q2 (a) are decided (see the end).

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
`needs.Check`), and findings 5–7, which are open PRs of their own (below).

**The rule:** pure refactors under the drop-in rule. Unchanged: results, error texts (including `%v` against `%w`
wrapping), which error wins when two inputs are wrong (including today's random choice, Q2), log lines and their
fields, the order of side effects and of cleanup, what a failed build does *not* release, and allocs/op on the
evaluation path. Equal allocs/op is the target: a refactor that allocates *less* changed shape and goes back too;
optimisations get their own PR. Quirks are pinned, not fixed (for example `rate_limiter_capacity` reporting
`invalid value for capacity`).

## Open PRs and order

Checked with `gh pr view --json files` for #230, #232–#241.

| PR | Touches | Effect on this plan |
|----|---------|---------------------|
| #239 `health/request-keys` (finding 5) | `Router.Evaluate`, Flagsmith `Evaluate`; removes both `wantedKeys`, adds `evaluator.Request.WantedKeys()` (the router's presized version) | On the evaluation path. **Merges after A2** (it merges `main` in and its CI runs again), so the allocs comparison covers the router's multi-route path and the engine. B2 and B3 start from `main` after #239 and call `req.WantedKeys()` |
| #230 `fix/ports-bind` | rewrites the runtime test helpers (`newPorts`, `characterizedConfig` move to `runtime/internal/porttest`), `failed_build_test.go`, `serve_error_characterization_test.go`, `collector_test.go` | A1 builds on those helpers: **A1 is cut from `fix/ports-bind`** (stacked) or from `main` after it merges. A3 adds its own test file, so only a textual merge-up |
| #237 `health/common-settings` (finding 6) | `collector.go` `decodeConfig` and its config struct, not `parseEvent` | B4's `parseEvent` commit starts after #237 merges |
| #238 `health/headers` (finding 7) | `trigger.go` `parseHeaders` through a new `core/pkg/headers`, not `parseHeadersFrom` | B4's `parseHeadersFrom` commit starts after #238 merges |
| #232–#236, #240, #241 | none of the six files, nor `run-benchmarks.sh` or `compare-allocs.py` | none |

## Characterization first

Each area gets a tests-only PR on unchanged code. **The pinning files** of an area are the test files its A PR adds
or changes plus the existing files listed under "Pinned today"; the gate (below) runs A's head versions of them
against B's head.

### 1. `FromConfig`

**Pinned today:** telemetry `Create` error text (`TestUnbuildableTelemetryProviderStopsFromConfig`); telemetry shutdown
on failure (`TestTelemetryProviderIsShutDownWhenFromConfigFails`); listeners of created endpoints released on a
failed build, and alternating failed/valid builds leak no goroutine or fd (`failed_build_test.go`, linux); the
endpoint `Create` error text (`serve_error_characterization_test.go`); the `staleAfter` error (`runtime_test.go:300`);
`names no configuration format` (substring, `endpoints_test.go`); the event-sinks-need-metrics refusal (behaviour,
`operator_settings_test.go`); the skipped-source log (substring, at `sourcesWithKnownFormat`, not through
`FromConfig`); sink close on a later sink's failure; shutdown order (`shutdown_order_test.go`). Paths as after #230.

**Missing, add in `runtime/from_config_characterization_test.go`:**

- `TestFromConfigErrorTexts`: one row per `return nil, err`, exact `EqualError`: `the telemetry provider declares: …`,
  the metrics refusal (`... serves none`), `error creating flag store: ...` (if `store.NewStore` can fail from a
  config; otherwise note it unreachable), `error creating configuration format %q for source %q`, the default-format
  `error creating configuration format %q` and `source %q names no configuration format ...; available formats: ...`,
  `the service endpoint %q needs the evaluator of the configuration format %q, which the distribution lacks`
  (`endpoints.go:59`), the other `newEndpointInputs` errors, the middleware `at position %d declares` and `error
  creating the middleware`, `the service endpoint %q declares`, `could not create sync sources from config: ...`, the
  event sink errors.
- `TestFromConfigCallOrder`: recording fakes for telemetry, configuration format, evaluator, middleware, two endpoints,
  sync and event sink append `Create`, `Close` and `Shutdown` to one log.
  - A valid build pins telemetry → formats per source → default format → evaluators → middleware → endpoint 1
    validate/create → endpoint 2 → syncs → sinks, and the `onShutdown` registration order (event sinks, then
    telemetry).
  - A row where no endpoint needs the router: no evaluator is created (`endpoints.go:41-49`).
  - Failure rows pin what is released *and what is not*: sync providers created before a `staleAfter` refusal stay
    unclosed (`from_config.go:238-246`); evaluators stay unclosed when middleware fails (`:209-213`); endpoints close,
    then telemetry shuts down.
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
  second flag set; **`Bulk` with `FlagKeys`**: keys restricted, sorted by (flag set, key), no `FLAG_NOT_FOUND` filled
  in (`router.go:140` fills only for `FlagKeys != nil && !Bulk`).
- `TestRouterAppendsToDst`: a non-empty `dst` keeps its prefix on every path (fast path, single, bulk, error).
- `TestRouterRouteErrorDropsItsResults`: an evaluator that returns an error *together with* results and metadata:
  both are dropped, its keys get `GENERAL` results carrying the selector's `meta` (not `merged`), and the multi-route
  path itself returns a nil error.
- `TestRouterMetadataAliasing`: the error and not-found results carry the selector's metadata map, the returned
  metadata is a copy extended by first-route-wins keys (pin by identity where it is shared today).
- `TestRouterSubRequests`: a recording evaluator sees `FlagKey == ""`, the sorted keys of its route and a `nil` dst.
- `BenchmarkRouterEvaluate` in `router_test.go`: `one-route`, `two-routes/single`, `two-routes/bulk`,
  `two-routes/keys`, `two-routes/selector`, with `b.ReportAllocs()` and stub evaluators that allocate nothing, so the
  router's own allocations show.

### 2b. Flagsmith `Evaluate`

**Pinned today** (`evaluator_test.go`, `vectors_test.go`, `segments_test.go`): the evaluator contract kit, values by
type, type mismatch, unknown key, reasons, both boolean modes, hide-disabled for client keys, flag sets as
environments, later source wins, explicit keys of a bulk request, equality with `flagengine`. `BenchmarkEvaluate`
has `single/identity`, `single/environment`, `bulk/identity`, `bulk/environment`; CI does not compare it yet.

**Missing:**

- `TestEvaluateTieBetweenFlagSets`: one key in two flag sets at the same priority, single key: the first flag set in
  sorted order wins.
- `TestEvaluateAppendsToDst` and `TestEvaluateOrder`, as for the router.
- `TestEvaluateSkipsForeignDocuments`: a document whose `Value` is not `*document` is skipped, not an error.
- `keys/identity` and `keys/environment` beside the four existing sub-benchmarks, which keep their names (a renamed
  benchmark is in only one output and goes uncompared for a PR).

### A2's benchmark work (Q1 (a))

- `./contrib/flagsmith/engine/...` joins the fixed list in `run-benchmarks.sh`, so base and head measure it alike.
- **Noise first:** before A2 is opened, `BenchmarkRouterEvaluate/*` and `BenchmarkEvaluate/*` run 6 × `-count=3` at
  `-benchtime=1000x` on unchanged code, as recorded in the `NOISY` comment of `compare-allocs.py`. `BenchmarkEvaluate`'s
  identity context is a 4-entry map that `identityOf` ranges over, so the trait order is random per call. A
  benchmark whose lowest and highest allocs/op differ gets a `NOISY` entry with its observed spread and reason. The
  six runs go in A2's description.
- A2's CI runs twice (re-run the `benchmark` job) before it merges; both green.

### 4. Settings parsers

Three of the four iterate a map, so with two faulty entries the reported one is random today (`parseEvent`'s
`fields`, `parseHeadersFrom`'s outer loops and `refFromStrings`, `configOptions` over the query). Q2 (a) keeps that.

- **Single-fault rows** pin the text exactly.
- **Two-fault rows** go in `TestRandomChoiceBetweenTwoFaultsIsKept` (one per package): the parser runs 1,000 times on
  the same input and **both** texts must appear (odds of a false failure about 2⁻¹⁰⁰⁰). The name says it pins a
  decided quirk; a later Q2 (b) changes it on purpose. Rows whose winner is fixed today stay single-answer rows, for
  example `parseHeadersFrom` with `{"env": 1, "foo": "x"}` always reports `env`.

Per parser:

- **`parseEvent`** — pinned by substring through HTTP (`TestCollectorRejectsInvalidEvents`). Add `TestParseEvent` in
  `collector_internal_test.go`: exact reason and the full returned event (`require.Equal`) per row, each field's wrong
  type, `time` absent/empty/invalid, `value` integer/float/nested, `hasValue` overriding a present `value`, tracking
  `value: null` and `details: null`, data after the object (accepted: the decoder reads the first value only), a
  duplicate key (the last wins); precedence rows: bad `time` with unknown `kind` reports `time`; bad `time` with
  missing `flagKey` reports `time`; a wrong-typed field with missing `flagKey` reports the field. Two-fault row: two
  wrong-typed string fields.
- **`parseHeadersFrom`** — pinned by substring (`TestSettings`, `TestHeadersFrom*`). Add `TestParseHeadersFrom`:
  exact errors and returned map for every accepted Go type (`map[string]secret.Ref`, `map[string]map[string]string`,
  `map[string]any` with `secret.Ref`, `map[string]string` and `map[string]any` entries), empty input maps (`nil`
  result, not an empty map), a non-text value under a key other than `env`/`file` (today `""`, then the unknown-key
  error), an unknown key. Two-fault rows: two headers with unknown keys; one header with two unknown keys.
- **`open` / `configOptions`** — `TestQueryOptions` pins most texts exactly. Add rows for invalid
  `hostname_immutable`, `dualstack`, `fips`, `anonymous`, `rate_limiter_capacity`, `disable_https`, an unparsable URL,
  a repeated parameter (the first value wins); precedence rows (`ssetype=nope&foo=bar` reports `ssetype`;
  `accelerate=maybe&use_path_style=maybe` reports `accelerate`; `use_path_style=true&s3ForcePathStyle=false` gives path
  style off); one row that sets **every** client option to a valid value at once (`open` deletes the keys it handles
  before `configOptions` rejects unknown ones, so a helper that re-reads `u.Query()` fails it); and a test that reads
  the built client's options (`UsePathStyle`, `BaseEndpoint`, `UseAccelerate`, `DisableHTTPS`, retryer attempts) for
  the `endpoint`/`hostname_immutable` combinations. Two-fault row: `foo=1&bar=2`.

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
- No helper gains a cleanup of its own: what today's failed build leaves unclosed (syncs, evaluators) stays so.
- `startSinks` registers `onShutdown` for event sinks before telemetry, as today.
- `metricsHandler` stays called twice (check and runtime), so a provider sees the same calls.
- `// nolint: funlen` goes. Target: `FromConfig` ≤ 60 lines, cyclomatic ≤ 15, cognitive ≤ 20, no helper above them.

### 2a. Router (after #239)

- `Evaluate`: fast path; view, `req.WantedKeys()`, `collect`, `meta`; `single` (unchanged) or `bulk`.
- `bulk(ctx, req, entries, dst, meta)`: `winnersOf(entries)`, `keysPerRoute(winners)`, `evaluateRoutes` (the per-route
  loop with the error branch as `routeFailed(route, keys, meta)` and the winner filter), `fillNotFound`,
  `sortResults(req, results)`, then `append(dst, results...)` as today.
- `collect`: the per-document loop moves to `appendEntries(entries, doc, priority, route, wanted)`.
- `pair` moves to package level. No change to the `results`/`merged` construction, the sort closure, or the
  two-step append into `dst`.

### 2b. Flagsmith `Evaluate` (after #239)

- `Evaluate`: request facts (`meta`, selector, `req.WantedKeys()`, identity, client), `e.bestHits(...)`,
  `sortedHits(best)`, then `appendAll` for an unrestricted bulk request or `appendByKey(dst, ordered, keys, meta)`.
- `bestHits` keeps the source × document × engine-result loops and calls `resolve` for every candidate, as today;
  resolving only the winners would save allocations and is not this pass.
- The `keys := []string{req.FlagKey}` fallback stays in `Evaluate` unless moving it adds no heap allocation site
  (see gates).

### 4. Settings parsers

- `parseEvent`: `parseEvaluation(item, when)` and `parseTracking(item, when)`; the `fields` map and its loop move
  into `parseEvaluation` as a map, so the iteration stays a map's (Q2). `optTime` stays before the `kind` switch.
- `parseHeadersFrom`: one helper per accepted type (`refsFromRefs`, `refsFromStringMaps`, `refsFromAny`) and
  `refFromAny(name, v)` for the inner switch, all ranging over the same maps; the lazily created `out` map stays (nil
  for empty input).
- `open`: `checkSSEType(q)`, `clientOptionsOf(q) (opts, pathStyle, err)` on the same `q` (deleting what it handles),
  with today's order accelerate → disable_https → use_path_style → s3ForcePathStyle; then `configOptions`,
  `LoadDefaultConfig`, the retryer. Both `open bucket %v: %v` wraps stay `%v`.
- `configOptions`: return a `sharedOptions` struct instead of five results; one small parser per key keeping each
  key's own text (they differ: `invalid value for hostname_immutable` vs `invalid value for capacity`); still one
  `range q`.

## Gates

Inside WSL from the repository root, `GOTOOLCHAIN=go1.27.3`.

| Gate | Command | Applies to | Who |
|------|---------|------------|-----|
| Tests, race | `go test -race -count=3 ./<package>/...` for the PR's packages (`runtime`, `core/pkg/evaluator/router`, `contrib/flagsmith/engine`, `service/event-collector`, `sync/sse`, `sync/s3`) | every PR | implementer, CI |
| Characterization holds | on B's head: `git checkout <A head> -- <pinning files>` then `go test -race -count=3 ./<package>/...`; a clean run is the gate. `git diff <A> -- <pinning files>` is informative only (merge-ups change it) | every B PR | reviewer |
| Per commit | `git rebase -x 'go test ./<package>/...' <A head>` passes on every commit | B1, B4 | reviewer |
| Complexity | `gocyclo -over 15` and `gocognit -over 20` print none of the scope's functions or their new helpers; `funlen` passes without `nolint` | every B PR | reviewer |
| allocs/op | CI `Benchmarks (allocs/op)`: fails on a rise outside `NOISY`. **Equal** is a review rule: CI lists a fall and never fails on it, so the reviewer reads the "changed" table and sends a fall back | 2a, 2b, A2 | CI, reviewer |
| Heap sites | `go build -gcflags=-m` on parent and head for the router and engine packages; the criterion is no new heap allocation site (`escapes to heap`, `moved to heap`) on the request path. New `leaking param`, `inlining call` and renamed-variable lines are expected noise. allocs/op decides | 2a, 2b | reviewer |
| ns/op | `-count=10` parent and head, `benchstat`; no regression above 5% with p < 0.05 | 2a, 2b | reviewer |
| Benchmark noise | 6 × `-count=3` on unchanged code, `NOISY` entries where needed; A2's CI green twice | A2 | implementer |
| Acceptance | CI integration suite (flagd-full goes through the router's fast path) | 2a | CI |
| Load smoke | CI `load-smoke` | 1, 2a | CI |
| Spec | none: no spec file changes | — | — |

## Slicing

| PR | Branch | Base | Waits for | Content | Role |
|----|--------|------|-----------|---------|------|
| A1 | `health/pin-from-config` | `fix/ports-bind` (#230), then `main` | — (stacked on #230) | the `FromConfig` characterization tests | implementer |
| A2 | `health/pin-evaluation` | `main` | — | router and Flagsmith characterization tests, `BenchmarkRouterEvaluate`, `keys/*` sub-benchmarks, the engine in `run-benchmarks.sh`, `NOISY` entries | implementer |
| A3 | `health/pin-settings-parsers` | `main` | — | `TestParseEvent`, `TestParseHeadersFrom`, the s3 rows and client-options test, the three random-choice tests | implementer |
| — | #239 | `main` | A2 merged | merges `main` in; CI compares its allocs/op | its own session |
| B1 | `health/from-config-extract` | A1 | — | the `FromConfig` shape, one commit per helper | implementer |
| B2 | `health/router-extract` | `main` | A2 and #239 merged | the router shape | implementer |
| B3 | `health/flagsmith-evaluate-extract` | `main` | A2 and #239 merged | the Flagsmith shape | implementer |
| B4 | `health/settings-parsers-extract` | A3 | #237, #238 merged (merged up into A3 or B4) | one commit per function: `parseEvent`, `parseHeadersFrom`, `open`, `configOptions` | implementer |

A1–A3 run in parallel now. B2 and B3 are separate PRs so an allocation change points at one evaluator. `gofmt` and
merge-ups go to a mechanic. Each PR starts `Part of #177`; #177's PR list gets the seven.

## Risks

- **Deferred cleanup (1):** moving a `defer` into a helper, capturing `created` by value, returning a non-nil `rt`
  with an error, or adding a cleanup a helper did not have changes what a failed build releases.
  `TestFromConfigCallOrder`, `TestFailedFromConfigCleanupOrder` and `failed_build_test.go` (linux only: the reviewer
  runs it in WSL, CI runs it) catch each.
- **Side-effect order (1):** reordering steps changes which error an operator sees for a doubly broken config and
  what gets created before a failure. `TestFromConfigCallOrder` and `TestFromConfigFirstFailureWins`.
- **Allocations (2):** a map, the `keys` slice or the sort closure escaping through a new helper adds an allocation
  per request (NFR-001). The heap-site review shows the cause; the allocs/op comparison shows the cost. Without A2
  merged first, neither #239 nor B2 is measured on the multi-route path or the engine.
- **Benchmark noise (A2):** a ±1 on a newly compared benchmark would make unrelated PRs red; the noise measurement
  and `NOISY` entries prevent it.
- **Result order and ties (2):** map iteration in `winners`/`best` is hidden by the sorts and the priority
  comparisons; a helper that sorts or breaks ties differently changes what clients see. The tie and order tests.
- **Random error choice (4):** turning a map loop into a slice loop makes the choice deterministic, against Q2 (a);
  the 1,000-call tests fail it.
- **Error wrapping (4):** `%v` → `%w` changes no text but changes `errors.Is`; keep the verbs.
- **Merge conflicts:** #230, #237, #238 and #239 touch these files or their tests; the order above avoids
  overlapping edits, and the rest is textual merge-up.
- C-005 and the evaluator boundary (NFR-005) are not touched: no imports move, no protobuf changes.

## Decisions of the project lead

**Q1. Should CI compare the Flagsmith engine's allocs/op?** `contrib/flagsmith/engine` is not in the benchmark set.
**Decided (a):** A2 adds `./contrib/flagsmith/engine` to the fixed list in `run-benchmarks.sh`, so B3 and every later
PR are compared in CI (~10 s more per run).

**Q2. The random error choice in the parsers.** With two faulty entries, `parseEvent`, `parseHeadersFrom` and
`configOptions` report a random one today. **Decided (a):** keep the map iteration, so the refactor changes nothing;
pin single-fault texts exactly, and pin that both texts of a two-fault input still occur. A deterministic order can
follow as its own PR.

## Open question

**Q3. Hold #239 until A2 has merged?** #239 changes both evaluation paths, which CI does not measure today (the
router's multi-route path has no benchmark, the engine is not in the set). (a) #239 waits for A2, merges `main` in
and merges on a green allocs comparison; (b) #239 merges now with a local before/after of the engine benchmark in its
description, and the router's multi-route path stays unmeasured for that change. **Default: (a)**; A2 is tests and
benchmarks only, so the wait is short.
