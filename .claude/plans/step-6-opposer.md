# Step 6 plan: opposer findings (revision 1)

Plan: `.claude/plans/step-6.md` (draft, revision 1). Checked against `main` @ a26f04c, the open stacks read with `git show` without checking them out (`origin/uc-007/pr-4-router`, `origin/uc-003/pr-4`, `origin/uc-003/pr-5`), the spec (`UC-004`, `UC-005`, `UC-006`, `UC-020`, `docs/requirements.md`, `docs/glossary.md`), and `.claude/plans/uc-004.md` (revision 1, planned in parallel). File references are on `origin/uc-007/pr-4-router` unless marked otherwise.

Counts: **2 blockers, 9 major, 6 minor.**

## What holds (verified)

- **The evaluator stays free of OpenTelemetry.** On `main`, `cd core && go list -deps ./pkg/evaluator/...` lists 259 packages, none matching `opentelemetry|grpc|connectrpc|k8s.io|gocloud`. No step touches `core/pkg/evaluator` or `core/pkg/configformat`. The new core packages import the evaluator (`middleware` → `evaluator.WithAttributes`), never the other way round. `TraceEvaluations` stays in the flagd module (`flag-evaluation/evaluation_span.go:21`).
- **OpenTelemetry cannot leak into the core by accident.** `testdata/module-graph-boundaries.txt` already forbids `opentelemetry`, `google.golang.org/grpc` and `connectrpc.com` in the whole core module graph, and `testdata/module-boundaries.txt` forbids `grpc|connectrpc` for `core:./...`. Q3's optional interfaces, declared in the endpoints and in `sync/flagd-grpc`, are the only design that passes both rows. The `sync/flagd-grpc` rows already forbid `go.opentelemetry.io` and `openfeature-relay/telemetry`, so the type assertion on the injected provider is the only possible route.
- **Q7's description holds for flag evaluation and OFREP.** Flag evaluation adds metrics, then CORS, then h2c with `AddMiddleware` (`connect_service.go:247-261`), so h2c is outermost and metrics innermost. OFREP puts CORS outermost (`ofrep_service.go:106`), records metrics per route (`ofrep/handler.go:95`), puts compression inside the metrics and leaves SSE uncompressed (`ofrep_service.go` comment at the compression setup).
- **Q4 and Q5 describe the globals and the extra consumers correctly.** `otel.SetTracerProvider` and `SetTextMapPropagator` are called together (`telemetry/otel/builder.go:101-102`), and `SetMeterProvider` is called at `metrics.go:242`. The runtime's `flagSync` span uses scope `jsonEvaluator` (`runtime.go:147`). The sync and stream recorders fall back to `otel.GetMeterProvider()` (`sync_metrics.go:64`, `stream_metrics.go:36`).
- **The three core PRs are independent of one another.** M2 and E2 need only the `telemetry.Provider` type from T2, for their `Settings`. Neither needs T3. Three implementers can run T3/T5, M2/M4 and E2/E4 at the same time, subject to findings 4 and 11.

## Blockers

### 1. UC-004 and step 6 both own the event types and event publishing, with incompatible shapes

- **Step:** Q10, Q11, E2a, E3a, E3b, section 6.3. Against `uc-004.md` Q2, Q3, Q4, 2a, 2b, 3b.
- **Evidence:** both plans create `core/pkg/events`, and both publish the same events:

  | Seam | step 6 | UC-004 |
  |---|---|---|
  | `core/pkg/events` | E2a: three event types, plain data, no bus | 2a: event types **and** `Bus`, `Subscribe`, `HasSubscribers`, `Dropped`; "can start now" |
  | Evaluation event fields | glossary fields + time, error code, configuration format, endpoint type; absent value reported as absent | glossary fields + `ErrorCode`; `Value any`; no time, format or endpoint type |
  | Origin values | `service`, `in-process` | `remote`, `in-process` |
  | Configuration change event | glossary fields + time | glossary fields + `Missed int` |
  | Who publishes evaluation events | E3b: the endpoints by hand, in `flag_evaluator*.go` and `ofrep/handler.go` | 3c: the runtime injects `events.Publishing(typed.New(router), bus)`, a decorator that publishes one event per resolved flag |
  | Who publishes configuration change events | E3a: the runtime, after `accept` | 3b: the runtime, after `Accept`, under `r.mu` |

  UC-004 PR 2 can start today; E2 starts as soon as T2 lands. Two implementers would write the same package in parallel. If both E3b and UC-004 3c land, every evaluation is published twice: once by the decorator and once by the endpoint.
- **Fix:** UC-004 owns `core/pkg/events`, the bus, the publishing decorator and the configuration change events, because it lands first and its PR 2 is core-only. Step 6 drops E2a and E3a. E3b becomes a check: the decorator already publishes, so the official endpoints publish nothing by hand. The project lead decides the event schema once, before UC-004 PR 2: fields, origin values, and whether the schema carries time, configuration format and endpoint type. Q11 goes to the UC-004 planner as input now. E2 is then based on UC-004 PR 2 instead of only T2, so the dependency becomes explicit.

### 2. The middleware seam sits outside h2c in UC-004, which bypasses middleware for plaintext gRPC

- **Step:** Q7 ("Chain position"), M3b, section 6.1. Against `uc-004.md` 3e ("Each wraps its HTTP handler with `Settings.HTTPMiddleware` outside its own middleware").
- **Evidence:** h2c is the outermost layer on flag evaluation (`connect_service.go:260-261`) and flag sync (`flag-sync/connect_service.go:53-55`). `golang.org/x/net/http2/h2c`'s handler recognizes the prior-knowledge `PRI *` preface (and the `Upgrade: h2c` request), hijacks the connection and serves every following stream to the handler it wraps. A middleware outside h2c sees at most that one preface request per connection, and never sees the gRPC calls. With UC-004's position, step 6's own example (Q14: the API key middleware) would let every plaintext gRPC client through without a key, on flag evaluation and on flag sync. Step 6's position (`h2c → chain → instrumentation → handler`) is the correct one, but the two plans disagree, and `middlewaretest` (M2b) cannot catch this because it wraps a bare handler.
- **Fix:** both plans state one position: inside the endpoint's transport layer (h2c), outside its instrumentation. UC-004 3e changes to match, and its `HTTPMiddleware` goes where M3 puts the chain. M3 adds `TestMiddlewareSeesH2CRequests`: a prior-knowledge h2c gRPC call without a key gets the middleware's answer, on flag evaluation and on flag sync, both with and without TLS. UC-004's `TestBoundSelectorWins` (A6) adds the same h2c case.

## Major

### 3. Three context keys for the selector and two meanings for a bound one

- **Step:** Q8, M2a, M3c, section 6.2. Against `uc-004.md` Q10, 2c, `TestBoundSelectorWins`.
- **Evidence:**
  - The router already reads the selector from `store.SelectorContextKey{}` (`core/pkg/store/store.go:18`, read at `core/pkg/evaluator/typed/typed.go:49`).
  - The endpoints write that key unconditionally from the client's selector (`flag_evaluator.go:81,199,233,…`, `flag_evaluator_v1.go:82,…`, `ofrep/handler.go:154,190`).
  - UC-004 2c adds `service.WithBoundSelector`/`BoundSelector` in `core/pkg/service`, and its A6 test says the bound selector wins.
  - Step 6 adds `middleware.BindSelector`/`EffectiveSelector` in `core/pkg/middleware`, where the effective selector is the intersection.
  - The spec is split the same way. UC-004 A6 step 2 says "before a selector the client sent". UC-005 BR-005 says the client "may narrow it further". So an extension endpoint built to UC-004's contract would ignore a narrowing selector that the official endpoints honour.
- **Fix:** keep one key for the bound selector, in one package. UC-004 2c lands first, so it owns the key, and step 6's `EffectiveSelector` builds on it. The project lead decides the semantics once (BR-005 reads as intersection), and UC-004 A6 is amended to match. Every endpoint writes the effective selector into `store.SelectorContextKey`, so the router still reads one key. UC-004's `TestBoundSelectorWins` becomes Q8's table (bound only, client only, narrowing, conflict).

### 4. The stacks are parallel only up to the wiring, and the wiring is also serial against UC-004 PRs 4 and 5

- **Step:** Q1, section 7, PR bases of T4, M3 and E3.
- **Evidence:**
  - T4 is based on "UC-004's service factory PR", which is UC-004 PR 3.
  - UC-004 PR 4 then rewrites `ofrep/sse/tracker.go` and `ofrep/handler.go` (4a, 4b). M3c and T4b/T4c edit the same files: `ofrep/handler.go`, `ofrep/sse`.
  - UC-004 PR 5 runs `servicetest.Run` on the three official factories (5b), and T4 changes their `Settings`.
  - UC-004 3b rewrites the runtime's delivery path, which holds the `flagSync` span T4b moves (`runtime.go:147`).
  - The example PRs (T5, M4, E4) end in a status commit and a runtime test that need the wiring (T5c, M4c, E4c), so they cannot merge when their parallel part is done.
- **Fix:**
  - Base T4 on UC-004's last PR that touches the endpoints (PR 5), or state the interleaving explicitly: UC-004 3 → 4 → 5 → T4 → M3 → E3.
  - Split each status commit, with its runtime test, into a closing PR after the wiring, so that T5, M4 and E4 can merge as soon as they are reviewed.

### 5. Step 6 asks UC-004 for an ordering that UC-004 does not need, and T4 edits a management server that UC-004 moves

- **Step:** Q1 ("T2, M2 and E2 should land before UC-004's service factory PR"), section 6.1, section 6.5, T4c.
- **Evidence:**
  - UC-004 Q10 explicitly adds no telemetry field ("UC-006 adds `Settings.Telemetry`; adding a field breaks no factory"), and it carries middleware as `HTTPMiddleware func(http.Handler) http.Handler`.
  - T2 and UC-004 PR 3 both wait for the same two stacks, so "lands before" is a race, not a dependency.
  - UC-004 3d moves the management server, with `/metrics`, into `runtime/management.go`, and takes the handler from `FromConfig`. T4c still serves `/metrics` from `flag-evaluation/connect_service.go:334`.
- **Fix:**
  - Drop the ordering demand. M3 fills UC-004's `HTTPMiddleware` with `middleware.Chain(...)`, and T4 adds `Settings.Telemetry` to UC-004's `service.Settings`.
  - Move T4c's `/metrics` change to `runtime/management.go`, using Q3's `MetricsHandler()`.
  - Section 6 then shrinks to items 2–4 of this file.

### 6. Two buffers and two drop counters between the bus and a sink

- **Step:** Q10, Q12, E2b, E3c. Against `uc-004.md` Q2, 2a.
- **Evidence:**
  - UC-004's bus gives each subscription its own bounded buffer, drops when it is full and counts the drops in `Subscription.Dropped()`.
  - Step 6 puts a second buffer of 10 000 events in the sink pipeline, with its own drops counted in `feature_flag.flagd.event_sink.dropped`.
  - A drop at the bus is not reported through the telemetry provider, which UC-020 BR-003 requires.
  - UC-004 marks dropped configuration change events with `Missed` on the next event, and expects the subscriber to reread the store. An event sink has no store, and UC-020 BR-008 says it receives every event of its kinds.
- **Fix:** keep one buffer per sink. Either the sink's subscription is the pipeline (the bus calls `Offer` directly), or the subscription's buffer is sized by the sink's `bufferSize` and the pipeline reads from it with no second queue. Count every drop once, through telemetry, with `reason=buffer_full`. State what a sink receives after `Missed > 0`, for example a counted drop with no reread, and get the project lead's decision if that contradicts BR-008.

### 7. Q5 and T4d ignore UC-003's existing sync telemetry seam

- **Step:** Q5, T4d.
- **Evidence:** on `origin/uc-003/pr-4`:
  - `sync.Settings` already has `Metrics MetricsRecorder` (`core/pkg/sync/factory.go:19`).
  - `core/pkg/sync/metrics.go` defines the `MetricsRecorder` interface, with a nil-safe helper.
  - `fetching.Config.Metrics` uses it (`core/pkg/sync/fetching/fetching.go:40`).
  - `from_config.go:260-269` wires `StreamMetrics` (`flagdgrpc.StreamMetricsRecorder`) and `GRPCDialOptions` into the builder.
  - Q5 adds `Settings.Telemetry` and `sync.NewMetricsRecorder(Meter)` without saying whether `Settings.Metrics` stays, which would give two injection paths for the same metric.
  - The UC-003 stack also continues to `uc-003/pr-5` (native `sync/gcs`, `sync/azblob` and `sync/s3` on the fetching provider, and `bloburi` moved into the core), so T4d's file list is incomplete.
- **Fix:**
  - Keep `Settings.Metrics` as the seam. `sync.NewMetricsRecorder(telemetry.Meter) MetricsRecorder` lives in the core, and the runtime builds it.
  - `Settings.Telemetry` is used only for `flagd-grpc`'s stream metrics and Q3's `GRPCDialOptions`.
  - If `Metrics` is removed instead, list it for UC-015.
  - Base the plan on `uc-003/pr-5`, and add the cloud modules to T4d.

### 8. `TestClientInstrumentationCharacterization` cannot live in `sync/flagd-grpc`

- **Step:** section 2, row T `sync/flagd-grpc TestClientInstrumentationCharacterization` ("built through `FromConfig`").
- **Evidence:** a `sync/flagd-grpc` test that calls `runtime.FromConfig` has to require the flagd module. That module requires `telemetry/otel`, so `GOWORK=off go list -m all` in `sync/flagd-grpc` would list `openfeature-relay/telemetry/otel`, which `testdata/module-graph-boundaries.txt` forbids (`sync/flagd-grpc:openfeature-relay/(telemetry|…)`). It also creates a module cycle: flagd → `sync/builder` → `sync/flagd-grpc` → flagd.
- **Fix:** put the test in `flagd/pkg/runtime`, with a bufconn or loopback flagd-grpc server.

### 9. The gates for the OpenTelemetry boundary in the flagd module have gaps

- **Step:** Q4, T2c, M2c, E2d, T4b, T4g.
- **Evidence:**
  - **(a) depguard and tests.** `.golangci.yml` scopes depguard rules with `files`. T4b's span tests inject "the OpenTelemetry provider built on the test recorder", so test files in `flagd/pkg/service/...` import the OpenTelemetry SDK. A T4g depguard rule without a `!$test` exclusion fails lint. With the exclusion, the boundary row (`go list -deps`, no tests) is the real gate, and the plan should say so.
  - **(b) the row format.** `testdata/module-boundaries.txt` holds forbidden-import regexps (`Makefile:139-152`), not allowlists. "Forbidding everything outside the standard library and `R/core`" (T2c, M2c, E2d) cannot be written as a row. `core/pkg/middleware` also pulls `hashicorp/go-memdb` through `store.Selector` (`core/pkg/store/store.go:10`), so an allowlist would fail anyway.
  - **(c) global state in tests.** Q4's provider installs the process-wide tracer provider, meter provider and propagator in `Create`. T4's span and metric tests then share global state across packages and `t.Parallel()`.
- **Fix:**
  - Write the T4g depguard rule with `files: ["!$test", "**/flagd/pkg/service/**", "**/flagd/pkg/runtime/**"]`, and name the boundary rows as the gate.
  - Write concrete forbidden regexps: the `core:./...` list plus `prometheus|otelconnect|go.opentelemetry.io`.
  - Give the OpenTelemetry factory a test option that does not install the globals, and have T3a's `telemetrytest.Run` show that the provider works without them.

### 10. Characterization and benchmarks duplicate UC-004's PR 1

- **Step:** section 2 (E1, the T rows "through `FromConfig`"), Q13, section 4 benchmarks.
- **Evidence:**
  - E1 adds `BenchmarkConnectResolve{Boolean,All}` and `BenchmarkOfrep{Single,Bulk}`. UC-004 1h adds `BenchmarkConnectResolveBoolean`, `BenchmarkConnectResolveAll`, `BenchmarkOFREPSingle` and `BenchmarkOFREPBulk`, in the same `bench_test.go` files, and both are startable today.
  - T1's inventory pin goes through `FromConfig`, whose signature UC-004 3a changes (it adds `components`). UC-004 1a adds the `start(ctx)` seam that its own pins use, and 1b already pins `/metrics` on the management port.
  - The NFR-013 gate compares allocs/op against PR 1's baseline, but T4 changes the telemetry provider those benchmarks run with.
- **Fix:**
  - Drop E1 and use UC-004 1h's benchmarks for NFR-013 (both plans gate on them).
  - Build T1 on UC-004 PR 1's `start(ctx)`.
  - Say which telemetry provider the endpoint benchmarks inject, and re-baseline in T4, with the numbers in its PR description.

### 11. Six parallel PRs edit the same shared files

- **Step:** T2c, M2c, E2d, T5b, M4b, E4b.
- **Evidence:** each of these edits `testdata/go-packages.txt`, both boundary files, the `Makefile` (`GO_LIB_MODULE_DIRS`, the `test-*` targets) and the workspace module list. The three example modules also need CI to run their tests. UC-004 2e and 5e edit the same files.
- **Fix:** these are mechanical conflicts, not a reason to serialize. Name a merge order (root first, then by merge time) and have the mechanic rebase each step onto the previous one. State whether `build.yaml` runs the new `test-*` targets, or whether `make test` covers them.

## Minor

### 12. Q7 misdescribes flag sync's HTTP metrics

- **Step:** Q7 ("records HTTP metrics only on its flags route"), T4c.
- **Evidence:** `flag-sync/connect_service.go:42`: `mux.Handle(connectPath, httpMetrics(cfg, connectPath).Handler(connectHandler))`. The ConnectRPC path is metered too.
- **Fix:** correct Q7. T4c moves both wrappers, and T1's inventory row covers the `SyncFlags`/`FetchAllFlags` HTTP metric labels.

### 13. HTTP metrics inside the chain hide refused requests

- **Step:** Q7.
- **Evidence:** with `h2c → chain → instrumentation`, a 401 or 429 from an extension middleware is neither counted nor traced. UC-005 BR-004 positions HTTP metrics as middleware, by manifest order. C-003 holds, because today nothing refuses requests.
- **Fix:** a question for the project lead: is middleware that refuses requests visible in HTTP metrics? Also state that `flagd/cmd` puts CORS first in `runtime.Config.Middleware`, so an authentication middleware listed after CORS never answers a preflight with 401. Pin this in M3.

### 14. Shutdown order and time limit are not stated together

- **Step:** E3c, T4e, section 6.4. Against `uc-004.md` 3e/3f (each endpoint stops on `ctx` inside the errgroup).
- **Evidence:** neither plan says who closes the bus, or whether that happens after the errgroup and before the sinks flush. Neither says whether the 5 s limit applies per stage (endpoints, then sinks, then telemetry, which T3a now flushes) or to the whole shutdown (UC-020 BR-005: "within the shutdown time limit").
- **Fix:** one sequence in the runtime: errgroup done → bus closed → pipelines flushed → telemetry `Shutdown`, with one stated budget. Pin it in `TestSinksFlushedOnShutdown`.

### 15. `telemetry.Attribute` without boxing needs a concrete value type

- **Step:** Q2, T2a, section 5 (per-request allocations).
- **Evidence:** `Attribute{Key; Value}` avoids boxing only if `Value` is a concrete struct, as OpenTelemetry's `attribute.Value` is, and not `any`. T2a's allocation test covers span start/end and instrument calls, but not attribute construction or `SetAttributes`.
- **Fix:** define `Value` concretely, extend `TestNoopRecordsNothingAndAllocatesNothing` to attributes, and add a T3 benchmark for the adapter's conversion.

### 16. Package name collision

- **Step:** T3a.
- **Evidence:** `telemetry/otel` is package `telemetry` (`from_config.go:20`, used as `telemetry.Config`), the same name as the new `core/pkg/telemetry`.
- **Fix:** rename the package in `telemetry/otel` to `otel` in T3a (a C-004 entry), or fix one import alias in the plan.

### 17. Stale base and unnecessary waits

- **Step:** header, section 7 (T2 "after UC-007 PR 4 and UC-003 PR 4").
- **Evidence:** the stacks end at `uc-003/pr-5` and `uc-007/pr-5-engine-example`, not at PR 4. T2 adds only a new core package and touches `testdata` files, so nothing in either stack blocks it. M2 needs only `core/pkg/evaluator/attributes.go` (UC-007 PR 4). E2 needs nothing from either stack, but under finding 1 it needs UC-004 PR 2.
- **Fix:** cut T2 from today's `main`. Base M2 on UC-007 PR 4, and E2 on UC-004 PR 2. Name the real ends of the stacks.
