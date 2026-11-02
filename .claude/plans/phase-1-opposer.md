# Phase 1 opposer findings

Plan: `.claude/plans/phase-1.md` on `spec` @ 628f4b7. Commands ran in WSL Ubuntu with `GOTOOLCHAIN=go1.27.3`.

**Result: 0 blockers, 2 major, 5 minor.** The plan's main claims hold (see "Verified" at the end). The two majors are about how the move is proven and how it is split into PRs, not about whether it works.

## Findings

### 1. major: step 1 doesn't name the entry point that the step-3 wrapping goes through

- **Plan step:** 1 (characterization) together with 3 ("`NewConnectService` and `NewHandler` wrap the evaluator they are given").
- **Evidence:**
  - Every existing service test builds the services directly with `NewFlagEvaluationService` / `NewOldFlagEvaluationService` (flag_evaluator_test.go, flag_evaluator_v1_test.go). It never goes through `ConnectService.setupHandler`, which is where `s.eval` is passed to all three services (connect_service.go:173-219).
  - On the OFREP side, sse_bulk_test.go builds a `handler{}` literal, which bypasses the constructor.
- **What goes wrong:** if `TestEvaluationSpans` follows the existing tests' pattern, it passes on the branch point. After step 3 it loses the `jsonEvaluator` child and goes red. Then either the test changes, which breaks "byte-identical", or the implementer wraps the evaluator inside the test. In that case the test no longer proves the production wiring in `from_config.go` → `NewConnectService` / `NewOfrepService`.
- **Fix:** write the entry points into the plan:
  - Connect: `NewConnectService(...)` + `setupHandler(svcConf)` (same package), then `httptest.NewServer(s.server.Handler)`, driving the legacy schema, v1 and v2 through their connect clients.
  - OFREP: `NewOfrepHandler(...)`, the real name; the plan calls it `NewHandler`, see 5.
  - Flag sync: a `Runtime` whose `Evaluator` is a real `evaluator.NewJSON` (runtime_test.go:155 already builds one by hand).

  State that the tests must not build services by any other route.

### 2. major: PR C can be split; the "must be one commit" reasoning is wrong

- **Plan step:** 3, and the upstream PR table (C-006).
- **Evidence:** the plan says that moving the spans in two commits "would leave a commit with duplicate or missing evaluator spans". But core already ships a switch for exactly this: `WithTracerProvider` (json.go:82). It was released in core v0.19.0 (core/CHANGELOG.md:64), and it routes both the `resolve*` spans and `flagSync` (json.go:84-86).
- **Fix:** split C into two PRs, each with green step-1 tests:
  - **C1 `refactor(flagd)`:** add the decorator and move `flagSync` into `updateAndEmit`. In `from_config.go:139`, pass `evaluator.WithTracerProvider(noop.NewTracerProvider())` to `NewJSON`. The span tree stays identical and core doesn't change. This needs `flagd/go.mod` to require core ≥ v0.19.0 (today v0.18.0, flagd/go.mod:15), or a `GOWORK=off` build of flagd won't compile. The bump goes in C1.
  - **C2 `refactor(core)!`:** pure deletion in `json.go`, plus removing the one `WithTracerProvider` line in flagd.

  The upstream reviewer then sees the behaviour change and the API break apart. Release-please files a flagd-path commit under the flagd component, so a single `refactor(core)!` that touches both `core/` and `flagd/` also shows up as breaking in flagd's changelog. The split avoids that too.

### 3. minor: the test helper's "restore the previous provider" does nothing, and tracers bind early

- **Plan step:** 2 (test helper), and 3 (where the decorator gets its tracer).
- **Evidence:** otel v1.47.0, `internal/global/state.go:77-98`:
  - Setting the default delegating provider back on itself only logs `no delegate configured in tracer provider` and returns.
  - `delegateTraceOnce` forwards the default provider's tracers only to the *first* SDK provider ever set.

  So after the first test, the global stays on that test's recorder. Tracers fetched before a later test's `SetTracerProvider` keep reporting to the old recorder.

  Today every tracer is fetched at construction: json.go:104, flag_evaluator.go:53, flag_evaluator_v1.go:49, flag_evaluator_v2.go:51, ofrep/handler.go:82. otelhttp reads the global when `NewOfrepHandler` runs.
- **Fix:**
  - The tests install the provider *before* building any evaluator, service or handler.
  - Drop the restore, or use one `TestMain` provider per package and reset its recorder per test.
  - In step 3, say the decorator and the runtime fetch their tracer at construction or per call, never as a package-level `var`.

### 4. minor: the benchmark gate doesn't measure the code that moves into flagd

- **Plan step:** gate "Benchmarks", PR C.
- **Evidence:** `make flagd-benchmark-test` runs `go test -bench=Bench ... ./core/...` only (Makefile:84). The decorator sits in flagd, and flagd has evaluation benchmarks in flag_evaluator_test.go and flag_evaluator_v1_test.go that the gate never runs. "allocs/op expected to fall" holds for core, and is true by construction there.
- **Fix:** for PR C, also compare `go test -bench=. -benchmem ./flagd/pkg/service/flag-evaluation/` on `b23f82e` and on the branch. Report it rather than gate on it: NFR-001 names evaluator benchmarks only.

### 5. minor: wrong constructor name, and step 4 is empty

- **Plan step:** 3 and 4.
- **Evidence:**
  - The OFREP constructor is `NewOfrepHandler` (ofrep/handler.go:66); no `NewHandler` exists.
  - `NewResolver` has no caller outside json.go anywhere in the repository (grep across all modules).
  - `core/pkg/evaluator/mock/ievaluator.go` is a gomock of the `IEvaluator` interface, whose signatures don't change.
- **Fix:** rename in the plan. Drop step 4, or fold it into step 3 as "no other callers, verified by grep".

### 6. minor: the OFREP characterization misses two paths that create no evaluator span

- **Plan step:** 1, `TestOfrepEvaluationSpans`.
- **Evidence:** both paths return before the evaluator is called, and neither is pinned:
  - The bulk endpoint answers 304 through `conditionalETag` (ofrep/handler.go:110-111).
  - An invalid selector answers 400 (ofrep/handler.go:145-153, 186-191).

  A decorator placed at the wrong layer, such as around the router, would add spans there.
- **Fix:** add cases for bulk with a matching `If-None-Match` (304) and for an invalid `Flagd-Selector` on both the single and bulk endpoints. Each expects only the otelhttp `flagd.ofrep` span.

### 7. minor: #2070 is released, not just merged

- **Plan step:** risks, "Public API break in core".
- **Evidence:**
  - `WithTracerProvider` shipped in core v0.19.0 on 2026-10-15 (core/CHANGELOG.md:25, 64). Core is now at v0.19.2.
  - The #2070 commit message (d090fa7) says its goal was to wire the option into go-sdk-contrib's `InProcessService`.
  - The local go-sdk-contrib checkout pins core v0.17.0, so "no known caller" is only verified against an old pin.
- **Fix:** before opening C (or C2), check go-sdk-contrib main and its open PRs for `WithTracerProvider`. Have the PR text say the option's goal (in-process providers emitting no evaluator spans) is now the default (UC-014 BR-003).

## Verified, no finding

- **Span table:** names, scopes, kinds, attributes and the discarded child context all match the code.
  - Server spans: flag_evaluator.go:53/71/192, flag_evaluator_v1.go:49/70/218, flag_evaluator_v2.go:51/301.
  - Semconv attributes: core/pkg/telemetry/utils.go:23-28.
  - Evaluator spans: json.go:139-142 and 180-359.
  - v2 has no `ResolveAll`.
- **Callers that lose spans:** the evaluator's only callers in flagd are the three connect services and the OFREP handler. The flag sync service uses the store, never the evaluator. `SetState`'s only caller is `runtime.updateAndEmit` (runtime.go:139). flagd-proxy doesn't import the evaluator. Nothing type-asserts the evaluator, so a decorator is transparent.
- **Boundary:** `go list -deps ./pkg/evaluator | grep -E ...` prints 23 today. The same grep over the evaluator's non-OTel imports (jsonlogic, jsonschema, zap, flagd-schemas and core `logger` / `model` / `store` / `sync`) prints 0, so deleting the OTel imports from json.go brings the gate to 0.
- **depguard can express the boundary:**
  - The plan's two rules, with golangci-lint v2.14.0 (built with go1.27.3) on the untouched branch point, report exactly json.go:20-23 (`evaluator-boundary`) and json.go:25 (`stable-dependencies`).
  - They don't report `core/pkg/telemetry`, which imports OTel. So the `**/core/pkg/evaluator/**` glob works.
  - Today's json.go already serves as the negative check, so the reviewer needs no scratch edit.
  - depguard sees direct imports only. The `go list -deps` target in step 6 covers the transitive case, as Q4 says.
- **Baselines:**
  - `make lint` config: 0 issues on `core/...` and `flagd/...`. The installed `~/go/bin/golangci-lint` 2.13.2 is built with go1.26 and refuses to run; `make lint` installs v2.14.0, which works.
  - Tests are green: `core/pkg/evaluator`, `flagd/pkg/service/flag-evaluation/...` and `flagd/pkg/runtime`.
- **NFR-012:** the only `.go` import is `golang.org/x/exp/maps` (json.go:25, `maps.Clone` at 501, nil→nil identical in stdlib). flagd/go.mod:168 and test/integration/go.mod:177 require x/exp `// indirect`, so Q3 is right that the CLAUDE.md gate can't come back empty.
- **Release window:** the risk is smaller than the plan states. docs/installation.md:41 already advises against `go install`, and images build with `go work init ./core ./flagd` (flagd/build.Dockerfile:27).
- **Go in-process provider:** it imports `evaluator`, `logger`, `model`, `service`, `store`, `sync`, `sync/file`, `sync/grpc` and `sync/grpc/credentials` from core, and calls no `NewResolver` or `WithTracerProvider`. It compiles unchanged.
- **PR dependencies:** with C split as in finding 2, the order is A, B ∥ C1, then C2, then D.
