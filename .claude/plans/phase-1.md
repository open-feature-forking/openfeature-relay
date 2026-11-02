# Phase 1 plan: evaluator without telemetry, stdlib instead of x/exp, boundary rules

Base: `spec` @ 628f4b7 (upstream branch point `b23f82e`).

## 1. Scope

Realizes: FR-040 (the evaluator imports no telemetry), FR-033 (for today's two evaluation service endpoints only), NFR-005, NFR-012, C-003, C-006. UC-014 BR-001 and BR-003, as far as the evaluator goes.

Leaves out: the configuration format split (`SetState`, phase 2), telemetry provider factories and "whenever a telemetry provider is present" (phase 3), module split (phase 4), the flag sync endpoint, metrics.

### Today's spans (measured in code, pinned in step 1)

| Path | Spans, parent → child |
|---|---|
| Flag evaluation endpoint, single flag (`flagd.evaluation.v1`, `.v2`, legacy `schema.v1`) | server span `resolve<Type>` (scope `flagd.evaluation.v1` / `flagd.evaluation.v2` / `flagEvaluationService`, kind server, attrs `feature_flag.key`, `feature_flag.result.variant`, `feature_flag.provider.name`, error status on evaluation error) → **evaluator span** `resolve<Type>` (scope `jsonEvaluator`, kind internal, no attrs, never an error status) |
| Flag evaluation endpoint, `ResolveAll` (v1, legacy; v2 has none) | server span `resolveAll` (attr `feature_flag.count`) → **evaluator span** `resolveAll` |
| OFREP single / bulk | otelhttp server span `flagd.ofrep` → **evaluator span** `resolveAnyValue` / `resolveAll`. The handler's `flagd.ofrep.v1` tracer is never used. |
| Sync | **evaluator span** `flagSync`, a root span (`context.Background()`), attr `feature_flag.source`, on a rejected flag configuration status `Error "flagSync error"` plus an exception event. Started in `JSON.SetState`, called from `runtime.updateAndEmit`. |

The evaluator discards the context `Start` returns, so its spans never have children. otelconnect spans around the endpoints are untouched by this phase.

### Spec questions (Simon decides; the plan assumes the first option)

- **Q1, FR-033 and bulk evaluation.** "One evaluation span per flag evaluation" doesn't match the bulk paths: today `ResolveAll` and OFREP bulk record one `resolveAll` span per request, not one per flag. The plan keeps today's shape (C-003). If FR-033 should say "per evaluation request", the requirement needs a reword. The alternative, one span per flag, changes flagd-full's traces.
- **Q2, `flagSync` is a sync span, not an evaluation span.** FR-033 doesn't cover it. The plan moves it unchanged into the runtime (`updateAndEmit`), the only caller of `SetState`. Phase 2/3 may rehome it.
- **Q3, NFR-012 gate.** `grep -rn golang.org/x/exp --include=go.mod .` can't come back empty. `flagd/go.mod` and `test/integration/go.mod` require x/exp `// indirect` through controller-runtime, gateway-api, genproto, grpc-gateway, containerd, buildkit and go-billy (`go mod graph`). Proposed gate: no `.go` file imports `golang.org/x/exp`, and no go.mod requires it without `// indirect`. CLAUDE.md's gate table needs that change (a spec-side edit, not part of the upstream PRs).
- **Q4, NFR-005 "enforced in CI".** depguard only sees direct imports. A transitive breach, such as `core/pkg/store` gaining an OTel import, needs the `go list -deps` gate in CI as well. The plan adds it as a Makefile target (step 6).

## 2. Characterization first

Must not change: every span in the table above (name, instrumentation scope, kind, parent, attributes, status, events), every evaluation result, the CLI, and readiness. No span test exists today: neither `tracetest` nor `SpanRecorder` appears anywhere in the repo.

Test helper: install a global `sdktrace.TracerProvider` with a `tracetest.SpanRecorder`, and restore the previous provider in `t.Cleanup`. No `t.Parallel`, because the provider is global. Render the ended spans as a sorted, normalized tree, for example `flagd.evaluation.v1/resolveBoolean[server]{feature_flag.key=bool-flag,…} > jsonEvaluator/resolveBoolean[internal]`, and compare it with a literal. The tests go in the flagd module (it already requires `go.opentelemetry.io/otel/sdk`) and use the real `evaluator.NewJSON` with a small flag configuration.

- `flagd/pkg/service/flag-evaluation/evaluation_span_test.go` `TestEvaluationSpans`: table over the 3 services × {Boolean, String, Int, Float, Object, ResolveAll where present} × {found, `FLAG_NOT_FOUND`}, plus an invalid selector (server span errors, no evaluator span).
- `flagd/pkg/service/flag-evaluation/ofrep/handler_span_test.go` `TestOfrepEvaluationSpans`: through the `http.Handler` from `NewHandler` (so otelhttp runs), single and bulk, found and not found.
- `flagd/pkg/runtime/runtime_span_test.go` `TestFlagSyncSpan`: `updateAndEmit` with a valid and an invalid payload.

These three tests stay byte-identical through step 4. That is the proof the shape held.

## 3. Steps

| # | Model | Files | Change | Check |
|---|---|---|---|---|
| 1 | implementer | the three `*_span_test.go` above | Characterization tests against today's code | Green on the branch point; then a scratch flip of one span name turns them red (reviewer, not committed) |
| 2 | mechanic | `core/pkg/evaluator/json.go`, `core/go.mod`, `core/go.sum` | `golang.org/x/exp/maps` → stdlib `maps` (nil→nil semantics identical); drop the `//nolint:govet` if lint no longer needs it; `go mod tidy` in core | `make test-core`; `grep -rn '"golang.org/x/exp' --include=*.go .` empty; `grep -n x/exp core/go.mod` empty |
| 3 | implementer | `flagd/pkg/service/flag-evaluation/evaluation_span.go` (new), `connect_service.go`, `ofrep/handler.go`, `flagd/pkg/runtime/runtime.go` | Decorator `TraceEvaluations(evaluator.IEvaluator) evaluator.IEvaluator` in package `service` (exported because `ofrep` already imports that package and needs it). Tracer `otel.Tracer("jsonEvaluator")`; each `Resolve*` method starts the same internal span around the delegate call and discards the returned context, as today. `SetState` delegates without a span. `NewConnectService` and `NewHandler` wrap the evaluator they are given. `updateAndEmit` starts the `flagSync` root span around `SetState`, with the same attr, status text and `RecordError`. **Same commit**: delete every span from `json.go`: the `otel*` imports, `jsonEvaluatorTracerName`, `jsonEvalTracer`, `Resolver.tracer`, `WithTracerProvider`; `NewResolver(store, logger)` loses its tracer parameter | Step 1 tests unchanged and green; `make test-core test-flagd test-flagd-proxy`; Boundary gate empty (today 23; `store`, `sync`, `logger`, `model` already have 0) |
| 4 | implementer | `core/pkg/evaluator/*_test.go`, `mock/` if regenerated | Fix callers of `NewResolver` in tests or mocks, if any (none found outside `json.go`) | `make test-core` |
| 5 | mechanic | `.golangci.yml` | depguard rule `evaluator-boundary`, `files: ["**/core/pkg/evaluator/**"]`, deny `go.opentelemetry.io`, `google.golang.org/grpc`, `connectrpc.com`, `k8s.io`, `gocloud.dev`; rule `stable-dependencies` (all files) deny `golang.org/x/exp` | `make lint` green; scratch import of `go.opentelemetry.io/otel` in `json.go` makes lint fail, and so does `golang.org/x/exp/slices` anywhere (reviewer, not committed) |
| 6 | implementer | `Makefile`, `.github/workflows/build.yaml` | Target `check-evaluator-boundary` running the Boundary gate (fails on any match), run in the lint job | Target exits 0; with the scratch import from step 5 it exits non-zero |

Step 3 is one commit on purpose. Moving the spans in two commits would leave a commit with duplicate or missing evaluator spans and red step-1 tests.

### Upstream PRs (C-006)

| PR | Steps | Title | Depends on |
|---|---|---|---|
| A | 1 | `test: pin evaluation and flag sync spans` | none |
| B | 2 | `chore(core): replace golang.org/x/exp/maps with the standard library` | none (parallel to A) |
| C | 3, 4 | `refactor(core)!: record evaluation spans in the service endpoints instead of the evaluator` | A |
| D | 5, 6 | `ci: enforce the evaluator dependency boundary` | B, C |

## 4. Gates

Run from the repository root, inside WSL with `GOTOOLCHAIN=go1.27.3`, after `make workspace-init`.

| Gate | When | Command |
|---|---|---|
| Unit tests | every PR | `make test-core test-flagd test-flagd-proxy` |
| Lint | every PR | `make lint` |
| Boundary | C, D | `cd core && go list -deps ./pkg/evaluator \| grep -E 'opentelemetry\|grpc\|connectrpc\|k8s.io\|gocloud'` prints nothing (from D on: `make check-evaluator-boundary`) |
| Dependencies | B, D | `grep -rn '"golang.org/x/exp' --include=*.go .` prints nothing, and `grep -rn 'golang.org/x/exp' --include=go.mod . \| grep -v '// indirect'` prints nothing (see Q3) |
| Benchmarks | C | `make flagd-benchmark-test` on `b23f82e` and on the branch; allocs/op must not rise (expected to fall, since the evaluator benchmarks no longer start spans) |
| Acceptance | C | `make flagd-integration-test` |
| Span tests | A, C | `go test ./flagd/pkg/service/flag-evaluation/... ./flagd/pkg/runtime/... -run 'Span'` |

## 5. Risks

- **C-003, trace shape.** The decorator wraps the full evaluator call, where today's span started after the debug log line. Durations shift by nanoseconds; nothing in the tree changes. Instrumentation scope `jsonEvaluator` stays, even though the span now comes from flagd, because dashboards may filter on it.
- **Public API break in `core` (v0).** `WithTracerProvider` (added in #2070, 2026-09-24) and `NewResolver`'s third parameter go away; both take `go.opentelemetry.io/otel/trace` types, so they can't stay. The PR needs a `!` and must say why. #2070's stated goal was to turn these spans off for in-process providers, and this change does that by default, so tag its author. No known caller: go-sdk-contrib doesn't use either.
- **Go in-process provider** (`go-sdk-contrib/providers/flagd`, pins core v0.17.0). It uses only `evaluator.NewJSON(log, store)`, `IEvaluator`, `SetState`, `Resolve*Value` and `AnyValue`, and none of their signatures change. On bumping core, it compiles unchanged and stops emitting `jsonEvaluator/*` spans, which is FR-040 working as intended. None of its tests assert on spans.
- **Release window.** Images build with `go work init ./core ./flagd` (`flagd/build.Dockerfile`), so they get both halves of PR C at once. `go install …/flagd@vX`, which resolves through `flagd/go.mod` (core v0.18.0), shows duplicated evaluator spans until flagd's go.mod requires the core release that carries PR C. Mention it in PR C; release-please / renovate bumps core.
- **NFR-005.** depguard checks direct imports only, so step 6's `go list -deps` gate covers the transitive case (Q4). The `files` glob syntax is unproven in this repo; step 5's negative check proves it.
- **NFR-006, C-005.** Untouched: no go.mod moves, no protobuf changes.
- **Global tracer provider in tests.** The step-1 tests mutate `otel` globals. Keep them out of `t.Parallel`, and in packages whose other tests don't assert on spans (none do today).
- **Upstream content.** `.claude/`, `CLAUDE.md`, the spec docs and this plan never go into PRs A–D.
