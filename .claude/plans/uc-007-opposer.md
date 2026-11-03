# UC-007 opposer findings

Plan: `.claude/plans/uc-007.md`. Code: `main` @ 1e8be3c, which now contains UC-014 PR 6, the base the plan reads from `origin/uc-014/pr-6-evaluator-format-modules`. Commands ran in WSL Ubuntu with `GOTOOLCHAIN=go1.27.3`; scratch probes ran in `/tmp`.

**Result: 0 blockers, 3 major, 5 minor.** The direction holds:
- an untyped bulk interface, a typed adapter, a router with a single-evaluator fast path, and a store read view;
- endpoints keep the typed calls, so spans, `errFormat` and the v1/v2 handling stay put.

The majors are information the untyped `Request`/`Result` loses, a module-boundary break the test layout causes, and an allocation the design adds on the gated path.

## Findings

### 1. major: the untyped `Request`/`Result` can't carry two things today's typed paths decide on

- **Plan step:** Q1 (`Request`, `Result`), Q2 ("the bulk skip of untyped flags" moves into `typed`; "a variant must match the type of the default variant in bulk stays in `evaluator/flagd`"), and step 5.
- **Evidence:** `ResolveAllValues` (evaluator/flagd/json.go:158-180) picks each flag's type from its **default variant's value**, and acts on that type in two ways:
  - **Bulk type rule:** `resolve[bool|string|float64|map]` reports `TYPE_MISMATCH` when the resolved variant's type differs from the default variant's.
  - **Legacy skip:** flags whose default variant is absent or of another type are resolved untyped. Under `ProtoVersionKey`, which `flagd.evaluation.v1` sets (flag_evaluator_v1.go:83 and the five typed methods), they are **skipped** (`continue`, json.go:170-175).

  Neither decision can be made from a `Result`, which carries the resolved value but not the flag's default-variant type:
  - **The skip:** a flag with no default variant whose targeting returns `true` is skipped in v1 `ResolveAll` today. `typed` sees a `bool` result and can't know to skip it.
  - **The bulk type rule:** the rule can't be keyed on `FlagKeys == nil` either. OFREP single (`ResolveAsAnyValue`) never applies it today. In a mixed distribution the router turns a bulk-all request into an explicit key list for flagd (section 3, step 3). So flagd's bulk results there would follow other rules than in flagd-full.
- **Fix:**
  - Keep both behaviours in `evaluator/flagd`. `ProtoVersionKey` already lives in `core/pkg/evaluator`, so the evaluator can read it from `ctx` without importing `typed`.
  - Give `Request` an explicit bulk marker (for example `Bulk bool`, set by `typed.ResolveAllValues`, and preserved by the router when it splits keys). Then the bulk type rule depends on that marker, not on `FlagKeys`.
  - Leave `typed` the requested-type checks, numeric conversion, absent → default, and the single-call "no default variant → `FLAG_NOT_FOUND`" under `ProtoVersionKey`. That one is decidable from `HasValue=false` and reason `DEFAULT` once `FALLBACK` is mapped (Q3).
  - Add to 1b a v1 `ResolveAll` case with a no-default-variant flag whose targeting returns a variant. Add to step 7 a mixed-distribution bulk case with a flagd variant/default type mismatch.

### 2. major: core's adapter tests would pull the JsonLogic module into core's module graph

- **Plan step:** 1a ("the table and runner live in a non-test package `evaluator/flagd/flagdtypedtest` so the adapter tests can reuse them"), and step 3 (`TestAdapterMatchesCharacterization` in `core/pkg/evaluator/typed`, "shared via `flagdtypedtest`").
- **Evidence:**
  - `flagdtypedtest` is a package of module `github.com/open-feature-forking/openfeature-relay/evaluator/flagd`. A test in `core/pkg/evaluator/typed` that imports it makes `go mod tidy` add that module to core's `go.mod`, because test imports count.
  - testdata/module-graph-boundaries.txt line 3 forbids `jsonlogic|…|openfeature-relay/(sync|telemetry|evaluator|configformat)/` in core's module graph. So `make check-module-boundary` goes red in PR 2.
  - It also creates a core ↔ `evaluator/flagd` require cycle.
- **Fix:** keep the table in `evaluator/flagd`, and run the adapter-against-characterization test there: `evaluator/flagd` importing `core/pkg/evaluator/typed` is the allowed direction. Core's own `typed` tests use a fake with hand-written cases only.

### 3. major: the single-flag path gains an allocation the buffer pool doesn't remove, and the NFR-001 gate measures exactly that path

- **Plan step:** Q1 ("Appending into a caller buffer lets the typed adapter reuse a pooled slice, so the single-flag path doesn't add an allocation"), step 3 (`TestAdapterAllocs`: 0 extra allocs), and the Benchmarks gate (PRs 3, 4).
- **Evidence:**
  - A scratch module with the plan's shape (an adapter calling `Evaluator.Evaluate` through the interface, with `Request{FlagKeys: []string{key}, …}` and a preallocated `dst`) reports `[]string{...} escapes to heap` (`-gcflags=-m`) and `testing.AllocsPerRun` **1** per call. The cause is the single-key slice escaping through the interface call, not `dst`.
  - `make flagd-benchmark-test` gates exactly the single-flag typed methods: `BenchmarkResolve{Boolean,String,Float,Int,Object}Value` (evaluator/flagd/json_test.go:540-946), plus fractional.
  - There's no bulk benchmark, so the read view's savings (one transaction instead of 1 + n) aren't measured at all.
  - `View(ctx)` returning an interface adds another escaping value per call, unless it's a concrete struct.
- **Fix:**
  - Add `FlagKey string` for single-key requests, used when `FlagKeys` is nil, or pool the key slice together with `dst`.
  - Make `View` return a concrete value type.
  - Add `BenchmarkResolveAllValues` to PR 1's baselines (1c), so the bulk path is measured too.
  - `TestAdapterAllocs` should measure through `evaluator/flagd`'s `Evaluate` over a real store, not only over a fake.

### 4. minor: the endpoints and the span decorator take `IEvaluator`, not `IResolver`

- **Plan step:** Q2 ("endpoints keep calling `IResolver` unchanged"), and step 7 ("endpoints get `TraceEvaluations(typed.New(router))`").
- **Evidence:**
  - `TraceEvaluations(e evaluator.IEvaluator) evaluator.IEvaluator` (evaluation_span.go:21).
  - `NewConnectService(…, evaluator evaluator.IEvaluator, …)` (connect_service.go:116-117).
  - The three services' `eval evaluator.IEvaluator` fields, `NewOfrepHandler`/`NewOfrepService(evaluator evaluator.IEvaluator, …)`, and `Runtime.Evaluator evaluator.IEvaluator`.
  - `typed.New` returns an `IResolver`, which has no `SetState`, so step 7 doesn't compile as written.
- **Fix:** state the signature change: the decorator and the endpoint constructors take `IResolver`. That changes no pinned test, because `IEvaluator` values still satisfy `IResolver` at the call sites (the span tests pass `evaluation.NewEvaluator(...)`). Keep `Runtime.Evaluator` for `SetState`.

### 5. minor: #39's package-coverage pin goes red in every PR that adds a package

- **Plan step:** the gates table; PRs 1–5 add `evaluator/flagd/flagdtypedtest`, `core/pkg/evaluator/{typed,router,evaluatortest}` and the example's `evaluator`.
- **Evidence:** `check-package-coverage` (Makefile:128-134) diffs the packages of `GO_LIB_MODULE_DIRS` against `testdata/go-packages.txt` exactly, and fails on an extra package.
- **Fix:** add "update `testdata/go-packages.txt`" to each step that adds a package, and add `check-package-coverage` to the gates table. If `test/configformat-example` gains packages, add rows to `testdata/module-boundaries.txt` for the engine module (BR-009).

### 6. minor: Q3 is right for the endpoints, but in-process callers can still reach `ERROR`

- **Plan step:** Q3 ("can't be reached from a protobuf or JSON context, so no observable change").
- **Evidence:**
  - Verified for the endpoints: a NaN `NumberValue` survives binary protobuf, but `structpb.Struct.AsMap()` turns NaN and Inf into strings, so `json.Marshal` succeeds (scratch probe: `err: <nil>`). OFREP's JSON can't carry NaN.
  - In-process callers pass Go maps directly (`typed` over the evaluator, UC-014 steps 7–8). A `math.NaN()`, a `func` or a `chan` in the evaluation context reaches json.go:389, and the error text then changes from `ERROR` to `GENERAL`.
- **Fix:** add that case to 1a, through the typed `NewJSON` methods with a NaN in the context. Record the change for embedders in the PR text and in UC-015's guide.

### 7. minor: the router's precedence is coarser than the flags table's, and ties are undefined

- **Plan step:** section 3, router step 2 ("Map each key to the document whose source has the highest priority. That is the same precedence the flags table uses").
- **Evidence:**
  - The flags table deduplicates per (flagSetId, key) (`collect`, store.go:428-442). A bulk request without a selector therefore returns one entry **per flag set** holding the key.
  - `Get` without a selector picks the highest-priority row across flag sets, with `>=` (store.go `Get`). Within one source, equal priority then falls to memdb's index order. Flags without a flag set use a per-process random UUID (`nilFlagSetId`), so that order can change from run to run.
  - A per-key router mapping collapses the per-flag-set entries in mixed distributions, and leaves same-source ties undefined. The flagd-only fast path keeps today's shape.
- **Fix:**
  - Map per (flag set, key), mirroring `collect`. Define the tie: same source, two flag sets.
  - Pin today's duplicate-across-flag-sets bulk shape in 1b, using named flag sets only (the unnamed one is order-unstable).
  - Add the mixed-distribution variant to `TestRouterSelectorScopesDocuments`.

### 8. minor: the read view needs a path for a foreign `IStore`

- **Plan step:** Q4 and step 5 (`Evaluate` over a read view).
- **Evidence:**
  - `NewJSON(logger, s store.IStore, …)` keeps an explicit branch for an `IStore` implemented elsewhere (json.go:103-111; setstate_foreign_store_test.go).
  - `IStore` has no `View`, and `Factory.Settings.Store` is `*store.Store`.
- **Fix:** state how `NewJSON` over a foreign `IStore` resolves: per-call `Get`/`GetAll` as today, without the BR-011 guarantee. Keep `setstate_foreign_store_test.go` green, or retire that path explicitly (UC-015).

## Verified, no finding

- **Phase-1 spans are safe.** The endpoints reach the evaluator only through the typed methods: `resolve[T]` with `s.eval.Resolve<Type>Value` in the three connect services, and `ResolveAsAnyValue`/`ResolveAllValues` in OFREP. Keeping the typed surface above the router therefore keeps `TraceEvaluations`' spans, `errFormat` (flag_evaluator.go:412-425) and the v2 reason handling unchanged.
- **flagd's documents exist for the router.** In flagd-full, `JSON.SetState` goes through `format.Accept` whenever the store is a `*store.Store` (json.go:97-120), and `Accept` writes documents with `FlagKeys` per (format, source, flag set) (configformat/flagd/flagd.go:248-306; store/documents.go).
- **`FALLBACK` is distinguishable after Q3.** It arises only where a flag has no default variant (json.go:410, 433). `HasValue=false` plus reason `DEFAULT` identifies it, so the adapter can restore `FALLBACK` and v1's `FLAG_NOT_FOUND` for single calls.
- **The evaluator testkit already runs in CI.** The `test` job initializes `test-harness`, and `test-evaluator-testkit` is part of `make test` (Makefile:59, 70-71). The suite fails when the harness is missing (evaluator_suite_test.go:48-51), and it calls the typed methods today (lines 198-206), so switching it to `typed` over `Evaluate` is a contained change.
- **The core boundary rows allow the new interface package.** `core:./pkg/evaluator/...` forbids only telemetry, transport, `jsonlogic` and `jsonschema`. `Request.Selector store.Selector` is an in-module import.
