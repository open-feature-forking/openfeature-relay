# UC-004 opposer findings (revision 1)

Plan: `.claude/plans/uc-004.md` revision 1. Code: `main` (pulled; includes #48, #49, #51), plus the open stacks, read without checkout:
- `origin/uc-007/pr-4-router`: endpoints take `evaluator.IResolver`; `TraceEvaluations(IResolver) IResolver`; `evaluation.NewRoutedEvaluator`.
- `origin/uc-003/pr-4`: `sync.Settings{Source, Logger, Metrics, Config}`; `Config.SyncFactories`.

Read together with `.claude/plans/step-6.md` revision 1 (UC-005, UC-006, UC-020), which was written in parallel.

**Result: 2 blockers, 3 major, 3 minor.** Both blockers are seams that this plan and step 6 define differently. Each needs one owner, and a decision by the project lead where the semantics differ. Findings 1, 2, 4, 5, 6 and 8 concern step 6's overlap and should be reconciled together with step 6's own opposer findings.

## Findings

### 1. blocker (cross-plan): event types, the bus, and who publishes are defined twice

- **Plan step:** Q2, Q3, Q4; steps 2a, 2b, 3b. In step 6: Q10, Q11, E2a, E3a, E3b.
- **Evidence:** both plans create `core/pkg/events`, with incompatible contents:

  | Item | This plan | Step 6 |
  |---|---|---|
  | `ConfigurationChange` | `{Source, Format, FlagSet, Version; Added, Updated, Removed; Missed}` | Adds the time; no `Missed` |
  | `Evaluation` | `{FlagKey, FlagSet, Variant, Reason, ErrorCode, TargetingKey; Value; Origin}` | Adds the time, the configuration format and the endpoint type |
  | Origin values | `remote`, `in-process` | `service`, `in-process` |
  | Bus | Ships with the event types (2a) | E2a ships "plain data, no bus" |

  Publishing is doubled:
  - **Evaluation events:** this plan publishes them in a core decorator, `events.Publishing(typed.New(router), bus)` (Q3, 2b). Step 6's E3b publishes them **in the endpoints** (`flag_evaluator*.go`, `ofrep/handler.go`). With both implemented, every evaluation is published twice.
  - **Configuration change events:** the runtime publishes them after `Accept` in both plans (3b here, E3a there), with slightly different "changed" rules.
  - Both plans' PR 2 can start now, so two implementers would create the same package in parallel.
- **Fix:**
  - One owner for `core/pkg/events` (types, kinds, bus, `HasSubscribers`). Since CLAUDE.md gives UC-004 the event bus, this plan's 2a fits, with step 6's E2 consuming it.
  - One schema, the union both need: the time, the configuration format and the endpoint type, plus `Missed`. One origin vocabulary, and the project lead decides `remote` vs `service`.
  - One publisher per kind. Either the decorator (this plan's Q3, which needs the spec amendment Q3 names) or the endpoints (step 6's E3b, as UC-004 step 3 words it), not both.
  - Configuration change events published only once, by the runtime, in this plan's 3b. Step 6's E3a is then dropped.
  - Record the decisions in both plans before either PR 2 starts.

### 2. blocker (cross-plan): the middleware seam differs in package, chain position and selector semantics

- **Plan step:** Q10 (`Settings.HTTPMiddleware func(http.Handler) http.Handler`; `service.WithBoundSelector`/`BoundSelector` in core; "an HTTP endpoint prefers the bound selector over the one the client sent"), and 3e ("Each wraps its HTTP handler with `Settings.HTTPMiddleware` outside its own middleware"). In step 6: Q7, Q8, M2a, M3b, M3c.
- **Evidence:**
  - **Package:** this plan puts the bound selector in `core/pkg/service`. Step 6 puts `BindSelector`/`EffectiveSelector` in `core/pkg/middleware`.
  - **Semantics:** this plan lets the bound selector win. Step 6 recommends the **intersection** (a conflicting client key matches no flags), and lists "bound selector wins" as its rejected alternative. That is a spec decision (UC-005 BR-005, FR-047) both plans make differently.
  - **Position:** "outside its own middleware" puts an extension middleware **outside h2c and CORS**.
    - On main, the plaintext flag evaluation endpoint's outermost layer is h2c, with CORS and HTTP metrics inside it (connect_service.go, "Add middlewares": metrics, then CORS, then h2c added last). Flag sync wraps CORS, then h2c outermost (flag-sync/connect_service.go:52-55). OFREP puts CORS outermost (ofrep_service.go:106).
    - An authenticating middleware outside them answers a browser's CORS preflight and a plaintext gRPC client's `PRI *` h2c preface before CORS or h2c see them, which breaks both.
    - Step 6's `h2c → chain → instrumentation → handler` avoids that. CORS becomes a chain member there, and this plan doesn't account for that.
- **Fix:**
  - Use one seam: step 6's position (inside h2c, outside instrumentation) and its `core/pkg/middleware` package.
  - `Settings` carries the configured chain (or a `func(http.Handler) http.Handler` that step 6's `Chain` builds).
  - Have the project lead decide bound-wins vs intersection.
  - Remove `service.WithBoundSelector`, or make it a thin alias of the middleware package's function.

### 3. major: Q4 "every source through `Accept`" breaks the runtime tests UC-018 pinned

- **Plan step:** Q4 and 3b ("readiness tests green").
- **Evidence:**
  - `flagd/pkg/runtime/runtime_test.go:150-152` and `:198-199` drive readiness through a gomock `IEvaluator` with `EXPECT().SetState(...)`, and build `Runtime{...}` literals (:155, :201) with no format.
  - UC-018 resolved its own blocker by keeping sources without a non-default format on `Evaluator.SetState`, so that these tests and `runtime_span_test.go` stay byte-identical.
  - Routing flagd's sources through `Accept` makes gomock fail on missing `SetState` calls. A literal without flagd's format then has nothing to call.
- **Fix:** decide in the plan how these pins move:
  - **(a)** Keep the `SetState` fallback for a `Runtime` built without formats, and add change events only on the `Accept` path, with flagd-full built through `FromConfig`, which always registers flagd's format. Or:
  - **(b)** Rewrite `runtime_test.go`'s readiness cases in PR 1 to go through `FromConfig` plus `start`, and pin the `flagSync` span on the `Accept` path.

  Name the chosen option in the gates table. Today "readiness tests green" can't hold as written.

### 4. major (cross-plan): duplicate characterization and benchmark names with step 6

- **Plan step:** 1b, 1h. In step 6: T1's inventory pin, and E1 (`BenchmarkConnectResolve{Boolean,All}`, `BenchmarkOfrep{Single,Bulk}`).
- **Evidence:**
  - 1h adds `BenchmarkConnectResolveBoolean` and `BenchmarkConnectResolveAll` to `flag-evaluation/bench_test.go`. Step 6's E1 adds benchmarks of the same names to the same packages. Two declarations of one name in one package don't compile.
  - 1b's `/metrics` pin and step 6's T1 metric inventory both scrape the management port through `FromConfig`. The default Prometheus registerer allows one registration per test binary (UC-014 rev 4), so two such tests in `flagd/pkg/runtime` collide.
- **Fix:** give the endpoint benchmarks and the `/metrics` scrape one owner (this plan's PR 1 lands first), and have step 6 reuse them by name. Put both scrapes in one test, or give the exporter its own registerer.

### 5. major (cross-plan): two registries for the same factories

- **Plan step:** Q9 (`runtime.Components{Sync, ConfigurationFormats, Evaluators, Endpoints}` passed beside `Config`; the `Config` slices deprecated).
- **Evidence:**
  - Step 6's Q9 adds `runtime.Config.Telemetry`, `Config.Middleware` and `Config.EventSinks` as **`Config` fields**, the shape this plan deprecates.
  - The open stacks already use `Config` slices: `ConfigurationFormats` and `Evaluators` (uc-007/pr-4 from_config.go:63-67), and `SyncFactories` (uc-003/pr-4 from_config.go:64-65).
- **Fix:** one rule, decided once. Either `Components` gains `Telemetry`, `Middleware` and `EventSinks` (with their settings), and step 6 targets it, or both plans keep `Config` fields. UC-001's generated `main` fills whichever it is.

### 6. minor: the shutdown order isn't specified in the runtime

- **Plan step:** 3e, 3f (each endpoint ends on `ctx`, inside the runtime's errgroup).
- **Evidence:** step 6 (section 6, item 4) requires endpoints to stop first, then event sinks flush, then the telemetry provider shuts down (UC-020 BR-005). This plan's errgroup gives no place for the later stages.
- **Fix:** say that `start` returns only after the errgroup ends, and that it then runs the post-endpoint shutdown hooks in order. Step 6 fills those hooks.

### 7. minor: `/metrics` arrives through two different mechanisms

- **Plan step:** Q7 (a handler that `FromConfig` passes in, `promhttp.Handler()`) and 3d.
- **Evidence:** step 6's Q3 has the management port type-assert the telemetry provider for `MetricsHandler() http.Handler`, with `/metrics` answering 404 under the noop provider.
- **Fix:** make the management server take an optional `http.Handler` for `/metrics`. Q7's interim source is `promhttp`; from step 6's T4 on, it is the provider's `MetricsHandler()`. That way T4 doesn't rewrite 3d.

### 8. minor: Q10's "no telemetry field" against step 6's ordering

- **Plan step:** Q10.
- **Evidence:** step 6's Q1 asks for its core PRs T2 and M2 to merge before this plan's PR 3, so that `service.Settings` carries `telemetry.Provider` and the chain from the start. Step 6's T4 file list doesn't include `core/pkg/service`, so nothing adds the field later.
- **Fix:** either PR 3 takes the fields once T2 and M2 have merged, or step 6's T4 lists `core/pkg/service/endpoint.go`. State which in both plans.

## Verified, no finding

- **Typed endpoints in uc-007/pr-4.** The endpoints take `evaluator.IResolver` (`NewConnectService(logger, evaluator.IResolver, …)`), and `TraceEvaluations` is `IResolver → IResolver`. So a `Publishing` decorator, if chosen, composes with the span decorator without signature changes.
- **UC-003's sync settings.** `sync.Settings` has `Metrics MetricsRecorder` (uc-003/pr-4 core/pkg/sync/factory.go:13-22), and `Config.SyncFactories` exists. The plan's reading of UC-003 is right.
- **Today's runtime calls.** `Emit(source)` exists (flag-sync/sync_service.go:180), and `Last-Modified` comes from the snapshot time (http_handler.go:53-65). The management port with probes and metrics is served by the flag evaluation endpoint (connect_service.go, "metrics and probes listening at"). Q7's and Q8's descriptions of today match.
- **OpenTelemetry stays out of `core/pkg/events` and `core/pkg/service`.** Neither needs an OTel type, and 2e's rows pin it. The evaluator packages are untouched.
