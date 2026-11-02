# Phase 1, PR A review: span characterization tests

Reviewer run on 2026-11-02. Commits `95ff313..caee1ea` on `claude/p1-pr-a-span-tests-886205`:

| Commit | Step | File |
|---|---|---|
| f1ad342 `test(flagd): pin evaluation spans of the flag evaluation endpoint` | A1 | `flagd/pkg/service/flag-evaluation/evaluation_span_test.go` |
| 70aeda6 `test(flagd): pin evaluation spans of the OFREP endpoint` | A2 | `flagd/pkg/service/flag-evaluation/ofrep/handler_span_test.go` |
| caee1ea `test(flagd): pin the flagSync span of a flag configuration update` | A3 | `flagd/pkg/runtime/runtime_span_test.go` |

`git diff spec..HEAD` also shows `CLAUDE.md` losing the Containment section. That isn't in these commits: `spec` moved on to de73cc7 after the branch was cut from 95ff313. The PR content is the three test files only.

## Gates (reviewer's own run, WSL Ubuntu, `GOTOOLCHAIN=go1.27.3`)

| Gate | Result |
|---|---|
| `make workspace-init` | green, once `GOWORK=$PWD/go.work` is exported (see Environment) |
| Span tests: `go test -count=1 -v -run 'TestEvaluationSpans\|TestOfrepEvaluationSpans\|TestFlagSyncSpan' ./flagd/pkg/service/flag-evaluation/... ./flagd/pkg/runtime/` | `ok` in all three packages: `TestEvaluationSpans`, `TestOfrepEvaluationSpans` and `TestFlagSyncSpan` PASS |
| Same three tests, `-race -count=10` | `ok` in all three packages, so no flakiness or races |
| `make test-flagd` | exit 0, every package `ok` (results served from cache after the run above, which is valid: inputs unchanged) |
| `make lint` (golangci-lint v2.14.0) | exit 0, `0 issues.` in all six modules (needs `GOPATH` exported, see Environment) |

### Mutation checks (scratch edits to `core/pkg/evaluator/json.go`, each reverted with `git checkout`, tree clean afterwards)

1. Span name `resolveAll` renamed to `resolveAllMutated` (json.go:180), and attribute `feature_flag.source` renamed to `feature_flag.sourceMutated` (json.go:142):
   - A1 red: `legacy/All`, `v1/All`
   - A2 red: `bulk found`, `bulk selecting no flags`, `bulk not modified`
   - A3 red: both cases
2. `resolveBoolean` (json.go:235) and `resolveAnyValue` (json.go:359) started as root spans (`trace.ContextWithSpanContext(ctx, trace.SpanContext{})`):
   - A1 red: all six Boolean found/not-found cases
   - A2 red: `single found`, `single not found`

   So the tests pin the parent link as well as the names.

### Upstream equivalence

`git diff --stat b23f82e 95ff313 -- . ':!docs' ':!.claude' ':!CLAUDE.md'` shows only `mkdocs.yml`. No Go source, `go.mod` or `go.sum` differs from the branch point, so the green run here is a green run on `b23f82e`.

## Acceptance criteria from the plan

| Criterion | Status |
|---|---|
| Connect: `NewConnectService(log, evaluator.NewJSON(...), store, nil)` + `setupHandler` + `httptest.NewServer(svc.server.Handler)` + generated clients for `schema.v1`, `evaluation.v1`, `evaluation.v2` | met (evaluation_span_test.go:115-134) |
| OFREP: `NewOfrepHandler` with real `evaluator.NewJSON` behind `httptest.NewServer` | met (handler_span_test.go:110-114) |
| Sync: `Runtime{Evaluator: evaluator.NewJSON(...), ...}` + `updateAndEmit` | met (runtime_span_test.go:99-108) |
| No `NewFlagEvaluationService` / `NewOldFlagEvaluationService` / `handler{}` literal | met |
| Provider installed in `TestMain` before anything is built, no restore, `recorder.Reset()` per test, no `t.Parallel` | met in all three packages |
| Service × type matrix, found and `FLAG_NOT_FOUND`, `ResolveAll` where present (none for v2) | met: 34 cases |
| Invalid selector per service: server span has error status, no evaluator span | met (legacy and v1 cover Boolean and All; v2 covers Boolean) |
| OFREP single and bulk × found/not found; 304; invalid selector on single and on bulk | met, plus an extra "bulk selecting no flags" case |
| Normalized tree comparison, no timestamps or IDs, one literal per case | met |
| Tests only, no production change | met |
| Commits: conventional, DCO sign-off, no AI trailer, nothing from `.claude/` or spec docs | met |

## Findings

### Major

**M1. The plan is wrong about OFREP, and the tests (correctly) contradict it.** The tests record today's behaviour, but the plan's table in §1 and step A2 describe something different in two places:

- The otelhttp server span is named `POST`, not `flagd.ofrep`. With otelhttp v0.72.0, the operation name passed to `otelhttp.NewHandler` (handler.go:120) is no longer the span name.
- The bulk 304 does produce `jsonEvaluator/resolveAll`. `conditionalETag` (etag.go:15-37) runs the bulk handler first and only then compares the ETag, so evaluation always happens. The plan says "only `flagd.ofrep`; no evaluator span", and A2 says "the last three expect only `flagd.ofrep`".

The tests are right to pin reality, so this needs no code change. Two things do need to happen:

- The planner corrects §1 and A2 before C1 starts, because C1's "A1–A3 unchanged" depends on the same facts.
- The PR description states both facts. Neither commit message mentions the deviation, so the project lead should acknowledge it explicitly.

### Minor

**m1. Comments that must stay byte-identical describe internals that C1/C2 change.** The plan requires the test files to stay byte-identical through C1 and C2, but some comments name the evaluator's own tracer:

- runtime_span_test.go:23-25 says "The evaluator fetches its tracer when it is built and never again". After C1, `flagSync` is started in `updateAndEmit` with a tracer fetched per call. After C2, the evaluator has no tracer at all.
- evaluation_span_test.go:47-48 and handler_span_test.go:36-38 tie tracer fetching to construction of the evaluator and handler. The handler part stays true; the evaluator part goes stale.

Failure scenario: after C2 the comment is false, and fixing it would break the byte-identical proof. Fix now with implementation-neutral wording, for example: "Tracers may be fetched when components are built, so the provider is installed before any test builds one. A global provider cannot be restored; tests reset the recorder instead."

**m2. Unused constant.** evaluation_span_test.go:44 declares `invalidSelector`, but the table repeats the literal `"invalid=selector"` at lines 241, 242, 254, 255 and 266. Lint doesn't flag it. Use the constant or drop it.

**m3. The OFREP tests pin otelhttp's semconv attributes through a denylist** (handler_span_test.go:85-89). `http.request.method`, `http.response.status_code`, `url.path` and `url.scheme` are pinned, and anything not on the denylist would be too. Failure scenario: a Renovate bump of otelhttp adds or renames an attribute, and `TestOfrepEvaluationSpans` goes red on a PR unrelated to tracing. Optional fix: keep only `http.response.status_code` (an allowlist), or pin only scope, name, kind and children for the otelhttp span. This phase is about the evaluator span beneath it. I'm not requiring this fix: the brittleness is real but cheap to repair when it happens.

### Nits

- Subtest names are built from `"legacy/All"` plus empty key and selector, giving `TestEvaluationSpans/legacy/All__`. The `/` nests subtests and the trailing `__` makes `-run` patterns awkward. A name like `legacy_All` with omitted empty parts would read better.
- `renderSpanTree` and `renderSpan` are duplicated across three packages (about 50 lines each), and runtime's `renderSpans` handles only one level of nesting. That's acceptable for test-only code without a shared test-util package. An upstream reviewer may ask about it; say so in the PR text.

## Correctness, boundaries, upstream fit

- No races (`-race -count=10` clean). Servers are closed via `t.Cleanup` and response bodies are drained and closed. The global provider is set once per package before any construction.
- Boundary: no production code touched, so NFR-005/NFR-006 are unchanged by this PR. The Boundary gate isn't in scope for A.
- Each commit is self-contained and green on its own: each adds one test file in a separate package.
- Upstream content: three `_test.go` files only.

## Environment (not the change)

- In a worktree nested under the main checkout, `make workspace-init` fails with `go: .../flagd-builder/go.work already exists`. `go work init` finds the parent checkout's `go.work`. Export `GOWORK=$PWD/go.work` first.
- `make lint` uses `${GOPATH}/bin/golangci-lint`, and `GOPATH` isn't set in the WSL login environment, which gives `/bin/golangci-lint: not found`. Export `GOPATH=$(go env GOPATH)`.

Both would be worth a line in CLAUDE.md's Gates section.

## Verdict

**ready after fixes**

1. m1: reword the three `TestMain` comments so they don't describe the evaluator's tracer (needed now because of the byte-identical rule).
2. m2: use `invalidSelector` in the table or remove the constant.
3. M1: the planner corrects §1 and A2 of `phase-1.md` (span name `POST`; 304 includes `jsonEvaluator/resolveAll`), and the PR A description states both facts for the project lead's approval. No test change.

m3 and the nits are optional.
