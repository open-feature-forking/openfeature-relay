# Phase 1 plan: evaluator without telemetry, stdlib instead of x/exp, boundary rules

Base: `spec` @ 2e18b57 (upstream branch point `b23f82e`). Revision 2: folds in Simon's decisions on Q1–Q4 and the seven findings in `phase-1-opposer.md`.

## 1. Scope

Realizes: FR-040 (the evaluator imports no telemetry), FR-033 (one evaluation span per evaluation request, bulk included, for today's two evaluation service endpoints), NFR-005, NFR-012, C-003, C-006. UC-014 BR-001 and BR-003, as far as the evaluator goes.

Leaves out: the configuration format split (`SetState`, phase 2), telemetry provider factories and "whenever a telemetry provider is present" (phase 3), module split (phase 4), the flag sync endpoint, metrics.

### Today's spans (measured in code, pinned by PR A)

| Path | Spans, parent → child |
|---|---|
| Flag evaluation endpoint, single flag (legacy `schema.v1`, `flagd.evaluation.v1`, `.v2`) | server span `resolve<Type>` (scope `flagEvaluationService` / `flagd.evaluation.v1` / `flagd.evaluation.v2`, kind server, attrs `feature_flag.key`, `feature_flag.result.variant`, `feature_flag.provider.name`, error status on evaluation error) → **evaluator span** `resolve<Type>` (scope `jsonEvaluator`, kind internal, no attrs, never an error status) |
| Flag evaluation endpoint, `ResolveAll` (legacy, v1; v2 has none) | server span `resolveAll` (attr `feature_flag.count`) → **evaluator span** `resolveAll` |
| OFREP single / bulk | otelhttp server span `flagd.ofrep` → **evaluator span** `resolveAnyValue` / `resolveAll`. The handler's `flagd.ofrep.v1` tracer is never used. |
| OFREP bulk 304 (`If-None-Match` matches), OFREP invalid `Flagd-Selector` (single and bulk) | only `flagd.ofrep`; no evaluator span |
| Sync | **evaluator span** `flagSync`, a root span, attr `feature_flag.source`, on a rejected flag configuration status `Error "flagSync error"` plus an exception event. Started in `JSON.SetState`, whose only caller is `runtime.updateAndEmit`. |

The evaluator discards the context `Start` returns, so its spans never have children. otelconnect spans are untouched by this phase.

### Decided (Simon, committed in 2e18b57)

- Q1: keep today's shape; FR-033 now reads "one evaluation span per evaluation request, a bulk request included".
- Q2: `flagSync` moves unchanged into `updateAndEmit`; its final home comes later.
- Q3: NFR-012 gate as in CLAUDE.md: no `.go` file imports x/exp, and no go.mod requires it without `// indirect`.
- Q4: the `go list -deps` boundary gate becomes a Makefile target in CI, next to depguard.

## 2. Characterization first: PR A

Self-contained; an implementer can start it alone on a branch cut from `spec`. Test files only, no production change. Must be green on the branch point, and stay byte-identical through C1 and C2. That is the proof the span tree held.

### Test provider rules (otel v1.47.0 global semantics)

- Install the `sdktrace.TracerProvider` with a `tracetest.SpanRecorder` via `otel.SetTracerProvider` **before** building any evaluator, service, handler or runtime. Today every tracer is fetched at construction, and otelhttp reads the global in `NewOfrepHandler`.
- One provider per test package, set in `TestMain`. Each test calls `recorder.Reset()` at its start. No restore: setting the default provider back does nothing, and later SDK providers don't reach already-delegated tracers.
- No `t.Parallel` in these tests.
- Comparison: render the ended spans as a normalized tree, sorted by start order with parent links resolved, as lines like `flagd.evaluation.v1/resolveBoolean[server]{feature_flag.key=bool-flag,…}` with an indented `jsonEvaluator/resolveBoolean[internal]` beneath. Compare with a literal per case. Leave out timestamps and IDs.

### Entry points (the only construction routes allowed)

The tests must go through the production wiring that C1 changes. Building `NewFlagEvaluationService` / `NewOldFlagEvaluationService` directly, or a `handler{}` literal, is not allowed: it would bypass the wrapping and pass on the branch point for the wrong reason.

- **Connect:** `NewConnectService(logger, evaluator.NewJSON(...), store, nil)`, then `s.setupHandler(service.Configuration{...})` (same package), `httptest.NewServer(s.server.Handler)`, and the generated connect clients for legacy `schema.v1`, `evaluation.v1` and `evaluation.v2`.
- **OFREP:** `NewOfrepHandler(...)` with a real `evaluator.NewJSON`, served by `httptest.NewServer`.
- **Flag sync:** a `Runtime{Evaluator: evaluator.NewJSON(...), Logger: ..., SyncService: ...}` built as in `runtime_test.go:155`, but with the real evaluator instead of the mock; call `updateAndEmit`.

### PR A steps

| # | Model | File | Cases | Check |
|---|---|---|---|---|
| A1 | implementer | `flagd/pkg/service/flag-evaluation/evaluation_span_test.go`, `TestMain` + `TestEvaluationSpans` | 3 services × {Boolean, String, Int, Float, Object, ResolveAll where present} × {found, `FLAG_NOT_FOUND`}; invalid selector per service (server span errors, no evaluator span) | `go test ./flagd/pkg/service/flag-evaluation/ -run 'TestEvaluationSpans' -count=1` green |
| A2 | implementer | `flagd/pkg/service/flag-evaluation/ofrep/handler_span_test.go`, `TestMain` + `TestOfrepEvaluationSpans` | single and bulk × {found, not found}; bulk with matching `If-None-Match` (304); invalid `Flagd-Selector` on single and on bulk. The last three expect only `flagd.ofrep` | `go test ./flagd/pkg/service/flag-evaluation/ofrep/ -run 'TestOfrepEvaluationSpans' -count=1` green |
| A3 | implementer | `flagd/pkg/runtime/runtime_span_test.go`, `TestMain` + `TestFlagSyncSpan` | `updateAndEmit` with a valid and an invalid payload | `go test ./flagd/pkg/runtime/ -run 'TestFlagSyncSpan' -count=1` green |

PR A done when: A1–A3 green on `b23f82e`; a scratch flip of one span name in `json.go` turns A1–A3 red (reviewer, not committed); `make test-flagd` and `make lint` green. Title: `test(flagd): pin evaluation and flag sync spans`.

## 3. Steps after PR A

| # | Model | Files | Change | Check |
|---|---|---|---|---|
| B1 | mechanic | `core/pkg/evaluator/json.go`, `core/go.mod`, `core/go.sum` | `golang.org/x/exp/maps` → stdlib `maps` (nil→nil semantics identical); drop the `//nolint:govet` if lint no longer needs it; `go mod tidy` in core | `make test-core`; Dependencies gate |
| C1a | mechanic | `flagd/go.mod`, `flagd/go.sum` | require `github.com/open-feature/flagd/core` ≥ v0.19.0 (first release with `WithTracerProvider`) | `cd flagd && GOWORK=off go build ./...` |
| C1b | implementer | `flagd/pkg/service/flag-evaluation/evaluation_span.go` (new), `connect_service.go`, `ofrep/handler.go`, `flagd/pkg/runtime/runtime.go`, `flagd/pkg/runtime/from_config.go` | Decorator `TraceEvaluations(evaluator.IEvaluator) evaluator.IEvaluator` in package `service` (exported because `ofrep` already imports that package). It fetches `otel.Tracer("jsonEvaluator")` in the constructor, never as a package-level `var`. Each `Resolve*` method starts the same internal span around the delegate call and discards the returned context, as today; `SetState` delegates without a span. `NewConnectService` and `NewOfrepHandler` wrap the evaluator they get. `updateAndEmit` starts the `flagSync` root span around `SetState` (tracer fetched per call), with the same attr, status text and `RecordError`. In `from_config.go:139`, pass `evaluator.WithTracerProvider(noop.NewTracerProvider())` so the evaluator's own spans go nowhere | A1–A3 unchanged and green; `make test-core test-flagd test-flagd-proxy` |
| C2a | implementer | `core/pkg/evaluator/json.go`, `flagd/pkg/runtime/from_config.go` | Pure deletion: `otel*` imports, `jsonEvaluatorTracerName`, `jsonEvalTracer`, `Resolver.tracer`, every span, `WithTracerProvider`; `NewResolver(store, logger)` loses its tracer parameter (no caller outside `json.go`, and the gomock of `IEvaluator` is unaffected; grep again before committing). Drop the `WithTracerProvider` line from `from_config.go` | A1–A3 unchanged and green; Boundary gate empty (today 23); `make test-core test-flagd` |
| D1 | mechanic | `.golangci.yml` | depguard rule `evaluator-boundary`, `files: ["**/core/pkg/evaluator/**"]`, deny `go.opentelemetry.io`, `google.golang.org/grpc`, `connectrpc.com`, `k8s.io`, `gocloud.dev`; rule `stable-dependencies` (all files) deny `golang.org/x/exp` | `make lint` green. The opposer already showed both rules flag today's `json.go:20-25` and nothing else, so `b23f82e` is the negative check |
| D2 | implementer | `Makefile`, `.github/workflows/build.yaml` | Target `check-evaluator-boundary` running the Boundary gate (non-zero exit on any match), run in the lint job | Target exits 0 on the branch, non-zero on `b23f82e` |

### Upstream PRs (C-006)

| PR | Steps | Title | Depends on |
|---|---|---|---|
| A | A1–A3 | `test(flagd): pin evaluation and flag sync spans` | none |
| B | B1 | `chore(core): replace golang.org/x/exp/maps with the standard library` | none |
| C1 | C1a, C1b | `refactor(flagd): record evaluation spans in the service endpoints` | A |
| C2 | C2a | `refactor(core)!: remove tracing from the evaluator` | C1, B (both touch `json.go`) |
| D | D1, D2 | `ci: enforce the evaluator dependency boundary` | C2 |

Order: A, then B ∥ C1, then C2, then D. Every PR keeps A1–A3 green.

Splitting C this way also keeps the core API break out of flagd's changelog: release-please files commits under the component whose paths they touch.

## 4. Gates

Run from the repository root, inside WSL with `GOTOOLCHAIN=go1.27.3`, after `make workspace-init`. `make lint` installs golangci-lint v2.14.0; the preinstalled 2.13.2 refuses to run.

| Gate | PRs | Command |
|---|---|---|
| Unit tests | all | `make test-core test-flagd test-flagd-proxy` |
| Lint | all | `make lint` |
| Span tests | A, C1, C2 | `go test -count=1 -run 'TestEvaluationSpans\|TestOfrepEvaluationSpans\|TestFlagSyncSpan' ./flagd/pkg/service/flag-evaluation/... ./flagd/pkg/runtime/` |
| Boundary | C2, D | `cd core && go list -deps ./pkg/evaluator \| grep -E 'opentelemetry\|grpc\|connectrpc\|k8s.io\|gocloud'` prints nothing (from D on: `make check-evaluator-boundary`) |
| Dependencies | B, D | `grep -rln 'golang.org/x/exp' --include=*.go .` and `grep -rn 'golang.org/x/exp' --include=go.mod . \| grep -v indirect` both print nothing |
| Benchmarks, gating | C2 | `make flagd-benchmark-test` on `b23f82e` and on the branch: allocs/op must not rise (NFR-001; expected to fall) |
| Benchmarks, reported | C1 | `go test -run '^$' -bench=. -benchmem ./flagd/pkg/service/flag-evaluation/` on `b23f82e` and on the branch: put allocs/op and ns/op in the PR text. Not gating, because NFR-001 names evaluator benchmarks only |
| Acceptance | C1, C2 | `make flagd-integration-test` |

## 5. Risks

- **C-003, trace shape.** The decorator wraps the full evaluator call, where today's span started after the debug log line. Durations shift by nanoseconds; nothing in the tree changes. Instrumentation scope `jsonEvaluator` stays, even though the span now comes from flagd, because dashboards may filter on it.
- **Public API break in `core` (v0), PR C2.** It removes `WithTracerProvider`, which shipped in core v0.19.0 on 2026-10-15 (#2070), and `NewResolver`'s third parameter. Both take OTel types, so they can't stay.
  - [open-feature/go-sdk-contrib#1013](https://github.com/open-feature/go-sdk-contrib/pull/1013) is open by #2070's author. It adds a provider option `WithTracerProvider` that feeds a `TracerProvider` into the in-process evaluator, so it breaks against C2.
  - Before opening C2, check go-sdk-contrib main and open PRs again for `WithTracerProvider`.
  - The C2 PR text must say that the option's goal, in-process providers emitting no evaluator spans, is now the default (UC-014 BR-003), and link #1013.
  - Commenting on #1013 needs Simon's explicit yes.
- **Go in-process provider** (`go-sdk-contrib/providers/flagd`, main pins core v0.17.0). It uses only `evaluator.NewJSON(log, store)`, `IEvaluator`, `SetState`, `Resolve*Value` and `AnyValue`, plus `logger`, `model`, `service`, `store`, `sync`, `sync/file`, `sync/grpc` and `sync/grpc/credentials`. None of their signatures change, so it compiles unchanged and stops emitting `jsonEvaluator/*` spans (FR-040 as intended). None of its tests assert on spans.
- **Release window.** Images build with `go work init ./core ./flagd`, and `docs/installation.md` advises against `go install`. With the C1/C2 split, flagd built against any core ≥ v0.19.0 emits the same tree, so there's no duplicated-span window left.
- **NFR-005.** depguard checks direct imports only; D2's `go list -deps` target covers the transitive case.
- **Test globals.** A1–A3 depend on otel's global delegation. Install-before-build and one provider per package (section 2) are what make them deterministic; a later refactor that moves a tracer into a package-level `var` would make them flaky. Section 3 forbids that for new code.
- **NFR-006, C-005.** Untouched: no go.mod moves other than C1a's version bump, no protobuf changes.
- **Upstream content.** `.claude/`, `CLAUDE.md`, the spec docs and this plan never go into the PRs.
