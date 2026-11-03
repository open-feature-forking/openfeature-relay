# Step 6 plan: Middleware, Telemetry Provider and Event Sink extensions (UC-005, UC-006, UC-020)

**Status: revision 3.** Revision 3 resolves the check of revision 2 (`step-6-opposer-r2.md`: 1 blocker, 2 major, 3 minor) and adds Q16 and Q17, both decided; revision 2's approval stands. Revision 2 resolves `step-6-opposer.md` (2 blockers, 9 major, 6 minor) and the step-6 overlaps in `uc-004-opposer.md` (findings 1, 2, 4, 5, 6, 7 and 8). Every finding's outcome is in section 8. Section 0 holds the open product questions. Section 6 maps every seam to UC-004 revision 2's section 1, "Seams step 6 builds on" (S1–S8). It is reconciled: step 6 defines none of those seams, and its plan matches each of them.

**Approval (2026-11-03): Revision 2 approved; open questions as recommended.** Q1–Q7, Q9, Q11, Q14 and Q15 are decided as recommended. Q8, Q10, Q12 and Q13 are decided in UC-004 (its Q6, Q3, Q11 and Q5).

**Approval (2026-11-03): Q16 and Q17 decided as recommended.** Q16: `flagd/pkg/runtime` is gated on its direct imports, and GCS's OpenTelemetry instrumentation is a named gap. Q17: CORS stays an endpoint layer outside the chain.

**Ownership rule (from the orchestrator).** UC-004 comes first in the PoC order and owns these seams: the event types, the event bus and event publishing; the middleware seam (package, position inside h2c, selector context key); the factory registry (`runtime.Components`); the shared characterization pins and benchmark names; and the shutdown order. Step 6 uses them and defines none of them.

**Base.** `main` @ a26f04c. The open stacks end at `uc-007/pr-5-engine-example` (UC-007; `uc-007/pr-4-router` has the `IResolver` endpoints and `core/pkg/evaluator/attributes.go`) and `uc-003/pr-5` (UC-003; `sync.Settings.Metrics`, `fetching.Config.Metrics`, the native cloud fetchers). Each PR's base is in section 7. Issues: UC-005 #17, UC-006 #18, UC-020 #32.

## 0. Open questions for the project lead

The recommendation comes first. Questions marked *(joint)* change UC-004's plan too; they are decided once, for both.

- **Q1, slicing and parallelism. (decided: as recommended)**
  - **Recommendation:** three stacks, T (UC-006), M (UC-005) and E (UC-020). Each has a core PR, an example PR and a wiring PR, followed by a short closing PR (a runtime test and the status).
    - **Runs in parallel now:** T2 (core telemetry interface, cut from today's `main`), T3 (OpenTelemetry provider, on T2) and T5 (example provider, on T2).
    - **Runs in parallel once UC-004 PR 2 is merged:** M2 (middleware factory and contract) and E2 (event sink pipeline and contract), each needing T2 for the `telemetry.Provider` type, and then M4 and E4 (examples).
    - **Runs in sequence after UC-004 PR 6,** the last UC-004 PR that touches the endpoints (UC-004 S8: PR 3 → PR 4 → PR 6 → T4 → M3 → E3). Each wiring PR changes the endpoints and `FromConfig`, so each waits for the previous one. T1 (the telemetry pins) runs beside the core PRs, built on UC-004 PR 1.
    - **Closing PRs** (T6, M5, E5) follow their wiring PR, so the example PRs can merge as soon as they are reviewed.
    - With three implementers, the critical path is UC-004 PR 6 → T4 → M3 → E3 → E5.
  - *Alternative:* one wiring PR for all three stacks after UC-004 PR 6. One rebase instead of three, but one large review.

- **Q2, the telemetry interface. (decided: as recommended)**
  - **Recommendation:** `core/pkg/telemetry` mirrors the subset flagd uses today, in standard library and core types:
    - `Tracer(scope).Start(ctx, name, ...SpanOption)` returns a `Span`, with `SetAttributes`, `RecordError`, `SetError(description)` and `End`.
    - `Meter(scope)` offers `Int64Counter`, `Int64UpDownCounter`, and `Float64Histogram` with explicit buckets.
    - HTTP instrumentation: `HTTPServerSpans(h, operation)` and `HTTPServerMetrics(h, HTTPMetricsOptions)`. The provider names and records the HTTP server metrics, so the OpenTelemetry provider keeps today's names and buckets (BR-005).
    - `Attribute{Key string; Value Value}`, where `Value` is a concrete struct (a type tag, an `int64`/`float64`/`bool` slot and a `string` slot), never `any`, so no attribute boxes a value (finding 15).
    - The evaluation, impression and flag sync stream metrics are recorded through `Meter` under today's names, by a flagd-module helper that replaces `IMetricsRecorder`.
  - *Alternative:* a domain-level recorder in the core (today's `IMetricsRecorder` with core attribute types). Less churn, but an extension middleware could not add a metric of its own (UC-005 BR-009).

- **Q3, transport instrumentation (UC-006 BR-006). (decided: as recommended)**
  - **Recommendation:** optional interfaces, declared where they are used and checked on the injected provider:
    - `interface{ ConnectHandlerOptions() []connect.HandlerOption }` in the flag evaluation and flag sync endpoints;
    - `interface{ GRPCDialOptions() []grpc.DialOption }` where the runtime fills `sync/builder`'s existing `GRPCDialOptions` field;
    - `interface{ MetricsHandler() http.Handler }`, whose result T4 passes as UC-004's `Config.MetricsHandler` (S5).
    - The OpenTelemetry provider implements all three. With the noop telemetry provider, `/metrics` answers 404.
  - *Alternative:* the endpoints build a ConnectRPC interceptor over the core `Tracer`. Transport-neutral, but it changes the `rpc.*` spans and metrics `otelconnect` records today (C-003).

- **Q4, global OpenTelemetry state. (decided: as recommended)**
  - **Recommendation:** the OpenTelemetry provider installs the global tracer provider, meter provider, propagator and error handler in `Create`, exactly as today, because `otelconnect`, the gRPC stats handler and third-party code read them. A test option `WithoutGlobals()` builds the provider without them; T3's contract run and T4's tests use it, so tests share no process-wide state (finding 9c).
  - *Alternative:* no globals in production either. Cleaner, but instrumentation that reads the globals silently stops reporting.

- **Q5, who consumes the telemetry provider (UC-006 BR-003). (decided: as recommended)** BR-003 names service endpoints and middleware. Today the runtime also records the `flagSync` span (scope `jsonEvaluator`), and sync providers record the client sync metrics and gRPC client instrumentation; event sinks report drops (UC-020 BR-003).
  - **Recommendation:** amend BR-003 to "the runtime, sync providers, service endpoints, middleware and event sinks; never the evaluator or a configuration format". Sync providers keep UC-003's seams: `sync.Settings.Metrics` and the builder's `StreamMetrics` and `GRPCDialOptions` fields. The runtime fills them from the provider: `sync.NewMetricsRecorder(telemetry.Meter)` in the core, the stream recorder over `Meter` in `sync/flagd-grpc`, and the dial options through Q3. `sync.Settings` gains no field (finding 7).
  - *Alternative:* keep BR-003's wording and treat the runtime and sync providers as parts of the core. The behaviour is the same, but the spec no longer says who consumes what.

- **Q6, an unbuildable telemetry configuration (UC-006 BR-008 against today). (decided: as recommended)** Today `FromConfig` logs a failed trace provider, metric reader or interceptor and starts without it.
  - **Recommendation:** implement BR-008. The OpenTelemetry factory returns the error, and startup stops naming the telemetry provider and the faulty value. Listed as a behaviour change (T4f) and in UC-009's migration notes.
  - *Alternative:* amend BR-008 so flagd-full keeps today's leniency.

- **Q7, which middleware is official (UC-005 BR-002, BR-004). (decided: as recommended)** UC-004 owns the chain's position (S2): `h2c → CORS → HTTPMiddleware → instrumentation → handler`; on OFREP it is `CORS → HTTPMiddleware → router`. With that position fixed, BR-004's list (h2c, CORS, HTTP metrics) cannot hold. Today's endpoints differ:
  - flag evaluation has h2c, then CORS, then HTTP metrics;
  - OFREP has CORS and per-route metrics inside its router, with compression inside them;
  - flag sync has h2c and CORS, and meters both its ConnectRPC path (`connect_service.go:42`) and its flags route (finding 12).
  - **Recommendation:** amend BR-004.
    - ~~CORS is the only official middleware.~~ *Superseded by Q17 (revision 3): CORS stays an endpoint layer outside the chain, so flagd-full lists no middleware.*
    - h2c stays the transport of the endpoints that serve gRPC over plaintext.
    - HTTP metrics become the telemetry provider's HTTP instrumentation, at today's positions.
  - Amend BR-002 so middleware covers every service endpoint served over `net/http`, flag sync included.
  - *Alternative:* keep BR-004 and make HTTP metrics a middleware that reads a route name the endpoint writes into a holder in the request context. That changes OFREP's per-route metrics and the 304 and compressed-size pins (C-003).

- **Q8, a bound selector meets the client's selector: moved to UC-004 Q6, decided there** (S2). UC-004 recommends the intersection, as revision 1 did here. M3 switches the official endpoints to UC-004's `EffectiveSelector`.

- **Q9 (new), is a refused request visible in HTTP metrics? (finding 13). (decided: as recommended)** With the chain outside the instrumentation, a 401 or 429 from a middleware is neither counted nor traced by the endpoint. C-003 holds, because no official middleware refuses requests.
  - **Recommendation:** no. A middleware that refuses requests records its refusals through the injected telemetry provider (UC-005 BR-009), and the contract checks that it may. CORS is an endpoint layer outside `HTTPMiddleware` (S2, Q17), so a preflight is answered before any middleware sees it, whatever the configured order; M3 pins this.
  - *Alternative:* the endpoints add a second, outer HTTP metrics layer for refused requests. Complete counts, but two measurements per request and new label values.

- **Q10, what an event sink gets when the bus drops events: moved to UC-004 Q3, decided there** (S1). A sink subscribes with `Buffer` set to its `bufferSize` and with an `OnDrop` that counts `feature_flag.flagd.event_sink.dropped{sink, kind, reason=buffer_full}` through the telemetry provider. A configuration change event with `Missed > 0` reaches the sink like any other event, and the sink rereads nothing. E5b still amends UC-020 BR-008 to "every event of its kinds that its buffer accepted; the others are counted as dropped", which BR-003 already implies.

- **Q11, buffer, batching, retry and shutdown share (UC-020 BR-002, BR-005, A2). (decided: as recommended)**
  - **Recommendation:** core defaults: buffer 10 000 events per sink, batch of 100 events or 1 s age, 5 delivery attempts with backoff from 1 s doubling to 30 s plus jitter. Each is overridable through core-owned keys in a sink's settings (`bufferSize`, `batchSize`, `flushInterval`, `maxAttempts`). `eventsink.Permanent(err)` is never retried. One delivery runs per sink at a time. Drop reasons: `buffer_full`, `retries_exhausted`, `permanent`, `shutdown`.
  - *Alternative:* fixed defaults only.

- **Q12, the shutdown budget: moved to UC-004 Q11, decided there** (S6). UC-004 recommends 5 s for the endpoint stage, then 5 s shared, in order, by the sinks' flush and the telemetry provider's shutdown. Step 6 registers its two hooks in that order.

- **Q13, NFR-013 measured on the endpoint benchmarks: moved to UC-004 Q5, decided there** (S7). T4 re-baselines S7's benchmarks with the telemetry provider it injects, and E3 gates on them.

- **Q14, the example components (UC-008 BR-005). (decided: as recommended)**
  - **Recommendation:**
    - `test/middleware-example`: an API key middleware. A known key binds a selector and `credential=api-key`, which covers A2. A missing or unknown key gets a 401, which covers A1.
    - `test/telemetry-example`: spans and metrics written as JSON lines on shutdown.
    - `test/eventsink-example`: a webhook sink. It reports a 5xx as retryable and a 4xx as `Permanent`; with `kinds: [configuration_change]` it acts as a notifier, which covers A1 and A2.
    - Each module requires only `R/core`.
  - *Alternative:* in-memory examples only.

- **Q15, operator configuration before UC-001 and UC-021. (decided: as recommended)**
  - **Recommendation:** none in step 6. UC-004's `runtime.Components` (S3) gains `Telemetry telemetry.Factory` with `TelemetryConfig map[string]any`, `Middleware []middleware.Configured` (in list order, which stands in for the manifest order) and `EventSinks []eventsink.Configured`. `flagd/cmd` maps today's flags onto the official factories. No configuration file keys; event sink settings come with UC-021.
  - *Alternative:* configuration file sections now. That would fix a surface UC-021 and UC-001 have not specified.

- **Q16 (new in revision 3; decided: as recommended), OpenTelemetry that a sync provider brings with it (finding N1).** The Google client libraries instrument themselves with OpenTelemetry. On `main`, `go list -deps` in `sync/gcs` lists `go.opentelemetry.io/otel`, `otel/sdk/metric`, `contrib/instrumentation/net/http`, gRPC's OpenTelemetry stats and Google Cloud's metric exporter. `flagd/pkg/runtime` imports `sync/builder`, which imports `sync/gcs` (`uc-003/pr-5`). So no transitive pattern for `flagd/pkg/runtime` can be green, including the narrower one the check proposed. `flagd/pkg/service/...` imports no sync module (checked: `go list -deps` shows none), so the full row holds there.
  - **Recommendation: gate what flagd's own code imports, and name the transitive part as a gap.**
    - `flagd/pkg/service/...` keeps the transitive row: no `go.opentelemetry.io`, `prometheus/client_golang`, `otelconnect` or `openfeature-relay/telemetry/`.
    - `flagd/pkg/runtime` is gated on its direct imports through T4h's depguard rule (depguard checks direct imports only). There is no transitive row.
    - Accepted gap, named in T6b: a distribution that contains the GCS sync provider carries Google's OpenTelemetry instrumentation even with the noop telemetry provider (UC-006 BR-002, FR-024). UC-010's flagd-minimal leaves out `sync/gcs`. The runtime stops importing every sync module once UC-001's generated `main` passes the distribution's components (`runtime.Official()` and `sync/builder`'s official list move there).
    - Reason: moving the official factory lists out of `flagd/pkg/runtime` and `sync/builder` now rewrites files owned by UC-003 and UC-004 (S3), which is the work UC-001 does anyway. And nothing in step 6 needs a distribution without GCS.
  - *Alternative:* move `runtime.Official()` and the official list in `sync/builder` into `flagd/cmd` in T4, so `flagd/pkg/runtime` has no sync module in its graph and gets the transitive row. That proves FR-024 at package level one use case earlier, at the cost of a T4 change to UC-003's and UC-004's files.

- **Q17 (new in revision 3; decided: as recommended), is CORS a middleware (finding N2)?** UC-004 S2 fixes `h2c → CORS → HTTPMiddleware → instrumentation` and pins that a preflight never reaches the chain (UC-004 3e). Revision 2's M3b made CORS the chain's first member instead. That contradicts S2, breaks UC-004's pin, and keeps preflights safe only while CORS is listed first. Once UC-001's manifest order decides (UC-005 BR-003), an authentication middleware listed before CORS would answer preflights with 401.
  - **Recommendation: CORS stays an endpoint layer, outside the chain,** configured from `--cors-origin` as today. It is not a middleware factory. Amend UC-005 BR-004 to "flagd-full lists no middleware; h2c, CORS and HTTP metrics are layers of the service endpoints". This follows the seam's owner (S2), keeps UC-004's pin, and makes preflights safe by construction. M3b drops the CORS factory. The contract is proven on the example middleware (UC-008 BR-005).
  - *Alternative:* CORS is a middleware that the runtime always places first, whatever the configured order. Amend UC-005 BR-003 for that exception, and change UC-004's 3e pin in M3b's commit as a listed change.

**Decided in UC-004, not here:**
- the event schema and origin values (UC-004 Q2): the glossary fields plus the time, error code, configuration format, endpoint type and `HasValue`; origin `service` or `in-process`;
- who publishes evaluation events (UC-004 Q4): the typed adapter's observer, so the official endpoints publish nothing by hand.

## 1. Scope

**Realizes:**

| Requirement | What step 6 delivers |
|---|---|
| FR-013, FR-024 | Telemetry factory interface, noop telemetry provider, OpenTelemetry provider behind the factory; endpoints, runtime and sync wiring use only the injected provider |
| FR-033 | Evaluation spans through the provider, same shape |
| FR-012, FR-047 | Middleware factory, chain helper and contract on UC-004's seam; CORS stays an endpoint layer (Q17); Q8's effective selector in the official endpoints |
| FR-036 | Event sink factory, the core's batching and retry pipeline on UC-004's bus, contract |
| FR-037, FR-038 | Delivered to event sinks; publishing itself is UC-004's (decorator and runtime) |
| FR-015 | Contract runners for Middleware, Telemetry Provider and Event Sink |
| NFR-006, NFR-013 | Boundary rows; Q13 gate on UC-004's benchmarks |

**Use case coverage:**

| Flow | Test |
|---|---|
| UC-005 main 1–6, A1, A2, A3 | `middlewaretest.Run` on `test/middleware-example`; the runner's broken stubs (A3) |
| UC-005 BR-003, BR-005, BR-008 | `TestMiddlewareOrder`, `TestBoundSelectorNarrowing` per endpoint (Q8), `TestMiddlewareSeesH2CRequests`, `TestInvalidMiddlewareConfigStops` |
| UC-006 main 1–6, A1 | `telemetrytest.Run` on `test/telemetry-example` and `telemetry/otel`; broken stubs |
| UC-006 BR-002, BR-004, BR-005, BR-008, BR-009 | `TestNoopWhenOmitted`, span pins and T1 inventory unchanged, `TestInvalidTelemetryConfigStops`, the runner's "never blocks" case |
| UC-020 main 1–6, A1, A2, A3 | `eventsinktest.Run` on `test/eventsink-example` (also as a notifier); broken stubs |
| FR-037, FR-038 (publishing) | UC-004's `TestOfficialEndpointsPublishOncePerFlag` (3e) and `TestChangeEventPerChangedDocument` (3b); end to end through a sink in E3b |
| UC-020 BR-003, BR-004, BR-005 | `TestDropsCountedOnce`, Q13 benchmarks, `TestSinksFlushedOnShutdown` |
| A4 / A2 (try in a distribution), A5 / A3 (abandon) | UC-001 (gap) / no system behaviour, no test |

**Accepted gaps:** steps 7–8, the try-in-a-distribution flows and the selection rules (UC-001); a distribution with no OpenTelemetry compiled in (UC-010 with UC-001's generated `main`; step 6 proves it for `flagd/pkg/service/...` transitively and for `flagd/pkg/runtime` by its direct imports); Google's own OpenTelemetry instrumentation in any distribution with the GCS sync provider, the noop telemetry provider notwithstanding (Q16; UC-010 leaves `sync/gcs` out of flagd-minimal); tracking events and in-process evaluation events (FR-039, UC-004's collector flow); operator settings for event sinks (UC-021); official middleware and the noop provider as their own component modules (UC-001's module split); flagd-proxy's own sync metrics (UC-017).

## 2. Characterization

UC-004 PR 1 owns the shared pins (S7): the `start(ctx)` seam (1a), the management port and `/metrics` scrape (1b, in its own package `flagd/pkg/runtime/managementport`), shutdown (1c), and the endpoint benchmarks (1h). Step 6 adds rows to those tests where they cover the same ground, and duplicates none. No step-6 test scrapes the management port outside that package.

Already pinned: `TestEvaluationSpans`, `TestOfrepEvaluationSpans`, `TestFlagSyncSpan`, `TestConnectServiceHTTPMetricLabels`, `TestOfrepHTTPMetrics`, `TestInstrumentFlagsRouteMetrics`, `TestFlagsRouteMetricsByMethod`, `TestRecordedResponseSizeIsTheCompressedSize`, `TestSyncMetricsCharacterization`, `TestOfrepServiceCORSPreflightAllowsFlagdSelector`, the `telemetry/otel` metric tests, and the test-harness suites.

| PR | Test | Pins |
|---|---|---|
| T1 | `flagd/pkg/runtime/managementport`, second subtest `inventory` against 1b's runtime (golden) | After one call on each endpoint (ConnectRPC single and bulk, OFREP single, bulk and 304, flag sync `SyncFlags`, `FetchAllFlags` and the flags route, a file source): every flagd metric name, type, label keys, bounded label values and buckets; Go and process collectors are excluded by prefix |
| T1 | `flag-evaluation` `TestEvaluationSpansWithInterceptor` | The span tree with `BuildConnectOptions`' interceptor, as `FromConfig` wires it (the existing span tests pass `nil` options) |
| T1 | `flagd/pkg/runtime/propagation_test` (own package) `TestTracePropagationCharacterization` | An incoming `traceparent` parents the server span when a trace exporter is configured, and does not without one; ConnectRPC, OFREP, the flags route |
| T1 | `flag-sync` `TestFlagsRouteSpans` | The `flagd.sync.http` span |
| T1 | `flagd/pkg/runtime/telemetrystartup_test` (own package) `TestTelemetryStartupCharacterization` | `OTEL_TRACES_EXPORTER=foo` and an unbuildable metric reader: startup logs and continues (Q6 flips this) |
| T1 | `flagd/pkg/runtime/grpcclient_test` (own package) `TestGRPCSyncClientInstrumentationCharacterization` (finding 8) | A `flagd-grpc` source against a loopback flagd-grpc server records a client span and the stream metrics |
| M3a | `TestMiddlewareChainCharacterization` (first commit of M3, before any change) | Per endpoint, with and without TLS: h2c prior knowledge served or refused; CORS preflight answered and not counted; an oversized body's 413 and whether HTTP metrics record it; flag sync's CORS and h2c |
| M3a | `TestSelectorCharacterization` | Selector sources per endpoint: the header, flag sync's request field against the header, OFREP's ETag and SSE selector, the invalid-selector error |

Each T1 row that builds through `FromConfig` lives in a test package of its own and calls `FromConfig` exactly once, as `syncmetrics_test` and UC-004's `managementport` do: today `FromConfig` installs global providers and registers the Prometheus reader with the default registerer, and from T4f on a second call would fail (finding N3). The reviewer checks, before and after T4f, that no test binary calls `FromConfig` twice. T1 runs on UC-004 PR 1 beside T2–T5. Its rows must be green before T4, and T4 keeps their `want` lines byte for byte (the reviewer checks this with `git diff`).

## 3. Steps

"R" is `github.com/open-feature-forking/openfeature-relay`. Every commit builds and passes `make test`.

### Stack T (UC-006)

#### T2 `uc-006/pr-2-telemetry-interface` (base `main`)

| Step | Model | Files | Change | Check |
|---|---|---|---|---|
| T2a | implementer | `core/pkg/telemetry` | Q2's interface; `Factory{Type(); Create(ctx, Settings) (Provider, error)}`; `Settings{Logger; DistributionName, DistributionVersion string; Config map[string]any}`; `Noop()`; `ValidateDeclaration` | `TestNoopAllocatesNothing`: `AllocsPerRun` = 0 for building attributes, span start, `SetAttributes`, end, counter, up-down counter, histogram |
| T2b | implementer | `core/pkg/telemetry/telemetrytest` | `Run(t, Factory, Inputs{Config; InvalidConfig; FaultyValue; StallBackend; Observed})`, one subtest per UC-008 case: usable without setup; never blocks a request (round trip through both HTTP wrappers within 100 ms while the backend stalls); 64 goroutines under `-race`; `Shutdown` returns within its context with a stalled backend; held data delivered on shutdown; invalid configuration named; declaration. A missing input names the cases that need it; no skips | Noop passes the backend-free cases; broken stubs fail exactly theirs |
| T2c | mechanic | `testdata/go-packages.txt`, both boundary files | Rows `core:./pkg/telemetry/...` with the `core:./...` regexp plus `prometheus\|otelconnect\|go.opentelemetry.io` (finding 9b) | `make check-package-coverage check-module-boundary` |

#### T3 `uc-006/pr-3-otel-provider` (base T2)

| Step | Model | Files | Change | Check |
|---|---|---|---|---|
| T3a | implementer | `telemetry/otel/{provider.go,factory.go}` | Type `otel`; settings `metricsExporter`, `collectorURI`, `certPath`, `keyPath`, `caPath`, `reloadInterval`, mapped 1:1 from today's `Config`; globals as today, `WithoutGlobals()` test option (Q4); core API adapted to the SDK; `HTTPServerSpans` = `otelhttp` with the noop meter; `HTTPServerMetrics` = today's metrics middleware, moved here; Q3's three optional methods; `Shutdown` flushes (new). The package keeps its name `telemetry`; the plan's importers alias it as `otelprovider` (finding 16) | `telemetrytest.Run` with in-memory exporters and `WithoutGlobals()`; existing tests unchanged |
| T3b | implementer | `telemetry/otel` tests | Through `Meter`, every instrument the endpoints will create has today's name, attributes and buckets; a benchmark of the core-to-SDK attribute conversion | `TestMeterMatchesRecorder`, `BenchmarkAttributeConversion` (numbers in the PR text) |

#### T5 `uc-006/pr-5-example` (base T2)

| Step | Model | Files | Change | Check |
|---|---|---|---|---|
| T5a | implementer | `test/telemetry-example` (requires `R/core` only) | Q14's provider; `telemetrytest.Run` | Its tests |
| T5b | mechanic | `Makefile` (`test-telemetry-example` in `test:`; CI runs `make test`), workspace, both boundary files | Register; rows forbid `openfeature-relay/(sync\|flagd\|telemetry)/`, `go.opentelemetry.io`, `google.golang.org/grpc`, `connectrpc.com`, `k8s.io`, `gocloud.dev` | `make test check-module-boundary` |

#### T4 `uc-006/pr-4-inject-telemetry` (base UC-004 PR 6, with T1 and T3 merged)

| Step | Model | Files | Change | Check |
|---|---|---|---|---|
| T4a | implementer | `flagd/pkg/service/metrics.go`, the three endpoints | `IMetricsRecorder` becomes a helper over `Meter` with today's names; attributes as `telemetry.Attribute` | T1 inventory and every metric test unchanged |
| T4b | implementer | `flag-evaluation/{flag_evaluator*.go,evaluation_span.go}`, `ofrep/handler.go`, `runtime/runtime.go` (UC-004 3b's delivery path) | Spans through the injected `Tracer`, today's scopes; `TraceEvaluations(resolver, tracer)` | Span pins with unchanged `want` lines; tests inject the OpenTelemetry provider with `WithoutGlobals()` and an in-memory recorder |
| T4c | implementer | `ofrep/handler.go`, `flag-sync/connect_service.go` (both wrappers, finding 12), `flag-evaluation/connect_service.go`, `runtime/from_config.go` | `otelhttp` and the metrics middleware become `HTTPServerSpans` and `HTTPServerMetrics` at the same positions; ConnectRPC options through Q3; `Config.MetricsHandler` (S5) gets `MetricsHandler()` instead of `promhttp.Handler()`, with no edit to `management.go`; `flagd/pkg/service/middleware/metrics` removed | The metric, compressed-size, span and propagation pins unchanged |
| T4d | implementer | `core/pkg/sync/metrics.go`, `sync/flagd-grpc`, `runtime/from_config.go`, `telemetry/otel` | Q5: `sync.NewMetricsRecorder(Meter)` fills `Settings.Metrics` and `fetching.Config.Metrics` (the cloud fetchers of `uc-003/pr-5` record through these and need no change; checked); the `flagd-grpc` stream recorder on `Meter`; the builder's `GRPCDialOptions` from Q3; `SyncRecorder` and `StreamRecorder` removed from `telemetry/otel` | `TestSyncMetricsCharacterization`, `TestGRPCSyncClientInstrumentationCharacterization` unchanged; `synctest.RunStreaming` on `flagd-grpc` |
| T4e | implementer | `runtime/components.go`, `from_config.go`, `flagd/cmd/start.go`, `core/pkg/service/endpoint.go` | `Components.Telemetry` and `TelemetryConfig` (S3); `service.Settings.Telemetry` (S4); created once and handed to every component; nil means `Noop()`; `Shutdown` registered with `r.onShutdown` after the sinks' flush (S6); `flagd/cmd` is the only flagd-module importer of `telemetry/otel` | `TestNoopWhenOmitted` (serves; `/metrics` 404); UC-004's `TestShutdownOrder` green |
| T4f | implementer | `from_config.go`, the startup pin | **Behaviour change (Q6):** a factory error stops startup naming provider and value; the `Build*` helpers removed | Pin updated in this commit; `TestInvalidTelemetryConfigStops` |
| T4g | implementer | S7's endpoint benchmarks | Re-baseline with the provider T4 injects (noop by default; UC-004 Q5); numbers in the PR text | Benchmarks run |
| T4h | mechanic | `.golangci.yml`, `testdata/module-boundaries.txt` | depguard rule `files: ["!$test", "**/flagd/pkg/service/**", "**/flagd/pkg/runtime/**"]` denying `go.opentelemetry.io`, `github.com/prometheus/client_golang`, `connectrpc.com/otelconnect` and `openfeature-relay/telemetry/`. This is the gate for `flagd/pkg/runtime`'s direct imports (Q16). The boundary row `flagd:./pkg/service/...` forbids the same paths transitively; there is no transitive row for `flagd/pkg/runtime` (finding N1). Before writing the row, run `go list -deps ./pkg/service/...` on the T4 base and record that it prints none of them | `make lint check-module-boundary`; the recorded `go list` output in the PR text |

#### T6 `uc-006/pr-6-close` (base T4, with T5 merged)

| Step | Model | Files | Change | Check |
|---|---|---|---|---|
| T6a | implementer | `flagd/pkg/runtime` test | `TestExtensionTelemetryProvider`: the example records an OFREP call's evaluation span and HTTP metrics | Green |
| T6b | mechanic | `UC-006-*.md`, `requirements.md`, `glossary.md` | `Implemented` with gaps, including the GCS instrumentation gap (Q16); BR-003 (Q5), BR-008 if Q6's alternative; glossary **Noop Telemetry Provider**; FR-013, FR-033 as the spec lint expects, FR-024 `In Progress` | Spec lint |

### Stack M (UC-005)

#### M2 `uc-005/pr-2-middleware-factory` (base UC-004 PR 2, with T2 merged)

| Step | Model | Files | Change | Check |
|---|---|---|---|---|
| M2a | implementer | `core/pkg/middleware` (created by UC-004 2c with `BindSelector`, `BoundSelector`, `EffectiveSelector` and `WithAttributes`; S2) | Adds `Middleware{Handler(http.Handler) http.Handler}`, `Factory{Type(); Create(Settings) (Middleware, error)}`, `Settings{Logger; Telemetry; Config}`, `Configured{Factory; Config}` and `Chain(mws, h)`, where the first middleware sees the request first. The selector and attribute functions stay UC-004's | `TestChainOrder`, `TestChainIdentityWhenEmpty` |
| M2b | implementer | `…/middlewaretest` | `Run(t, Factory, Inputs{Config; Passed; Answered; Binds; InvalidConfig; FaultyValue})`. Cases: passed requests return the wrapped response unchanged (status, headers, trailers, body); `http.Flusher` and `http.ResponseController` still work through the wrapper; answered requests never reach the wrapped handler; never both (BR-007); bindings visible to the wrapped handler; a refusal may be recorded through the injected provider (Q9); 64 concurrent requests under `-race`; invalid configuration; declaration | Broken stubs per case |
| M2c | mechanic | `testdata/*` | Package list; a row with the `core:./...` regexp (the package may import `core/pkg/store`, so not an allowlist; finding 9b) | `make check-package-coverage check-module-boundary` |

#### M4 `uc-005/pr-4-example` (base M2)

| Step | Model | Files | Change | Check |
|---|---|---|---|---|
| M4a | implementer | `test/middleware-example` | Q14's API key middleware; `middlewaretest.Run` | Its tests |
| M4b | mechanic | `Makefile`, workspace, boundary files | As T5b | `make test check-module-boundary` |

#### M3 `uc-005/pr-3-middleware-in-endpoints` (base T4)

| Step | Model | Files | Change | Check |
|---|---|---|---|---|
| M3a | implementer | tests only | The two characterization rows of section 2 | Green on the base |
| M3b | implementer | `runtime/components.go`, `from_config.go` | The runtime fills UC-004's `Settings.HTTPMiddleware` with `Chain(Components.Middleware...)` (S2). CORS stays the endpoints' own layer outside the chain, configured from `--cors-origin` (Q17); flagd-full passes no middleware. A factory error stops startup naming the middleware and the value | `TestMiddlewareChainCharacterization` and UC-004's 3e pin unchanged; `TestMiddlewareOrder`, `TestPreflightBeforeAnyMiddleware` (an authentication middleware listed first still never sees a preflight; Q9), `TestInvalidMiddlewareConfigStops` |
| M3c | implementer | `flag-evaluation/context_utils.go`, `ofrep/handler.go`, `ofrep/sse`, `flag-sync/{fetch.go,handler.go}` | Every official endpoint writes the result of UC-004's `EffectiveSelector` into `store.SelectorContextKey` (S2, UC-004 Q6). UC-004 does not switch the official endpoints, and its 6a passes no `Bind` for them; this step adds `Bind` to the three official `servicetest.Run` calls (finding N5) | `TestSelectorCharacterization` unchanged without a bound selector; `TestBoundSelectorNarrowing` per endpoint; the three official contract runs with `Bind` |
| M3d | implementer | tests | `TestMiddlewareSeesH2CRequests`: a prior-knowledge h2c gRPC call without a key gets the middleware's answer, on flag evaluation and flag sync, with and without TLS (blocker 2) | Green; `make flagd-integration-test` |

#### M5 `uc-005/pr-5-close` (base M3, with M4 merged)

| Step | Model | Files | Change | Check |
|---|---|---|---|---|
| M5a | implementer | `flagd/pkg/runtime` test | `TestExtensionMiddlewareBindsSelector`: OFREP bulk with a key bound to flag set A returns only A's flags; without a key, 401 | Green |
| M5b | mechanic | `UC-005-*.md`, `requirements.md` | `Implemented` with gaps; BR-002 (Q7), BR-004 (Q17: flagd-full lists no middleware), BR-005 if Q8 changes it; FR-012, FR-047 as the spec lint expects | Spec lint |

### Stack E (UC-020)

#### E2 `uc-020/pr-2-event-sink-pipeline` (base UC-004 PR 2, with T2 merged; no `core/pkg/events` commit)

| Step | Model | Files | Change | Check |
|---|---|---|---|---|
| E2a | implementer | `core/pkg/eventsink/{sink.go,factory.go,pipeline.go}` | `Sink{Deliver(ctx, []events.Event) error}`, optional `io.Closer`; `Factory{Type(); Kinds() []events.Kind; Create(Settings) (Sink, error)}`; `Settings{Logger; Telemetry; Config}`; `Permanent(err)`. The pipeline subscribes with `SubscribeOptions{Kinds; Buffer: bufferSize; OnDrop}` (S1) and reads the subscription's channel, with no second queue. `OnDrop` counts `reason=buffer_full`. The pipeline counts its own `retries_exhausted`, `permanent` and `shutdown` drops where they happen; every drop is counted once, through the telemetry provider. It batches, retries and delivers per Q11, and it ends when the channel closes after `Bus.Close()` (S6) or on `Close(ctx)` | `pipeline_test.go` on UC-004's bus, `-race`: `TestPublisherNeverBlocked`, `TestBatchBySize`, `TestBatchByAge`, `TestRetryWithBackoff`, `TestPermanentNotRetried`, `TestRetriesExhaustedDropped`, `TestDropsCountedOnce` (bus-side and pipeline-side), `TestMissedPassedThrough`, `TestFlushOnClose`, `TestCloseWithinBudget` |
| E2b | implementer | `core/pkg/eventsink/eventsinktest` | `Run(t, Factory, Inputs{Config; Received; Stall; Refuse; InvalidConfig; FaultyValue})`. Cases per UC-008: only subscribed kinds; batches; full buffer drops and counts without blocking the publisher; flush on shutdown; returns in time; retryable delivered later; permanent not retried; invalid configuration; declaration (empty type, no kinds) | Broken stubs per case |
| E2c | mechanic | `testdata/*` | Package list; `core:./pkg/eventsink/...` row with the `core:./...` regexp | `make check-package-coverage check-module-boundary` |

#### E4 `uc-020/pr-4-example` (base E2)

| Step | Model | Files | Change | Check |
|---|---|---|---|---|
| E4a | implementer | `test/eventsink-example` | Q14's webhook sink; `eventsinktest.Run` against `httptest`, also as a notifier | Its tests |
| E4b | mechanic | `Makefile`, workspace, boundary files | As T5b | `make test check-module-boundary` |

#### E3 `uc-020/pr-3-sinks-in-runtime` (base M3)

| Step | Model | Files | Change | Check |
|---|---|---|---|---|
| E3a | implementer | `runtime/components.go`, `from_config.go` | `Components.EventSinks` (Q15): a pipeline per sink subscribed for its kinds; a factory error stops startup naming the sink and the value (BR-009); flush registered with `r.onShutdown` before the telemetry provider's hook, within UC-004 Q11's hook budget (S6). The flush stops 1 s before that budget ends, so the telemetry provider always keeps at least 1 s; whatever is undelivered is counted as `reason=shutdown` (finding N6) | `TestInvalidEventSinkConfigStops`; `TestSinksFlushedOnShutdown` with a stalled sink: the flush ends 1 s before the budget, the drops are counted, and the telemetry provider's `Shutdown` gets a live context with at least 1 s left |
| E3b | implementer | tests only | Publishing is UC-004's (S1), and UC-004 already pins one event per flag and per document (3e, 3b). This step tests end to end through a sink: a recording sink receives exactly one batch entry per resolved flag (single and bulk, ConnectRPC and OFREP) and one per changed document (finding N4) | `TestSinkReceivesOneEntryPerFlag`, `TestSinkReceivesOneEntryPerDocument`; S7's benchmarks with no sink equal T4g's baseline (UC-004 Q5); `make flagd-integration-test` |

#### E5 `uc-020/pr-5-close` (base E3, with E4 merged)

| Step | Model | Files | Change | Check |
|---|---|---|---|---|
| E5a | implementer | `flagd/pkg/runtime` test | `TestExtensionEventSink`: an OFREP evaluation and a file change reach the example's receiver | Green |
| E5b | mechanic | `UC-020-*.md`, `requirements.md` | `Implemented` with gaps; BR-008 (Q10); FR-036 as the spec lint expects (NFR-013's wording is UC-004 Q5); FR-037 and FR-038 follow UC-004's status; FR-039 stays `Open` | Spec lint |

## 4. Gates

In WSL from the repository root (`export GOTOOLCHAIN=go1.27.3`; in a worktree `export GOWORK=$PWD/go.work`; for lint `export GOPATH=$(go env GOPATH)`):

| Gate | Command | PRs |
|---|---|---|
| Unit tests | `make workspace-init && make test` (CI's `make test` includes the new `test-*-example` targets) | all |
| Lint | `make lint` | all |
| Package coverage, boundaries | `make check-package-coverage check-module-boundary`; every graph claim above is a forbidden-regexp row in `testdata/module-boundaries.txt` or `module-graph-boundaries.txt` | all |
| Evaluator boundary | `cd core && go list -deps ./pkg/evaluator \| grep -E 'opentelemetry\|grpc\|connectrpc\|k8s.io\|gocloud'` prints nothing | all |
| flagd OpenTelemetry boundary | `cd flagd && go list -deps ./pkg/service/... \| grep -E 'go.opentelemetry.io\|prometheus/client_golang\|otelconnect\|openfeature-relay/telemetry/'` prints nothing (also a boundary row); `flagd/pkg/runtime`'s direct imports through `make lint` (depguard, Q16) | T4 and later |
| Dependencies | The two `golang.org/x/exp` greps from `CLAUDE.md` | all |
| Acceptance | `make flagd-integration-test` | T4, M3, E3 |
| Benchmarks | `make flagd-benchmark-test`, allocs/op unchanged against the branch point (NFR-001; the reviewer checks with `git diff --stat` that `evaluator/` and `configformat/` are untouched); S7's endpoint benchmarks with no sink against T4g's baseline (NFR-013, UC-004 Q5) | T4, M3, E3 |
| Spec | `spec_lint.py --docs docs` on Windows | T6, M5, E5, and every spec change from section 0 |

## 5. Risks

- **C-003, behaviour changes**, each in its own commit and listed for UC-009:
  - an unbuildable telemetry configuration stops startup (Q6);
  - telemetry is flushed on shutdown (T3a).
  - The happy path keeps metric names, labels, buckets, span names, scopes, kinds, propagation, `/metrics`, CORS and h2c, all pinned by T1 and M3a.
- **Adapter drift.** Attribute types, span status and the `exception` event must survive the core-to-SDK conversion. Covered by T1, T3b and unchanged span `want` lines.
- **Per-request allocations** from the attribute conversion: `BenchmarkAttributeConversion` and T4g's endpoint numbers show them. They do not reach the evaluator benchmarks.
- **NFR-006.** The new core packages carry only core and standard library imports, pinned by forbidden-regexp rows. ConnectRPC, gRPC and Prometheus types appear only in the optional interfaces of the modules that use them.
- **UC-004 drift.** If UC-004's seams change during its review, M2, E2 and the wiring PRs follow. Section 6 is the checklist.
- **Shared-file conflicts** (finding 11). T2c, T5b, M2c, M4b, E2c and E4b, and UC-004 2e and 5e, all edit `testdata/go-packages.txt`, both boundary files, the `Makefile` and the workspace. These are mechanical. Merge order is by merge time; the next one is rebased by the mechanic before review. Within step 6, each of these edits stays in the one mechanic step of its PR.
- **C-004.** Removed names for UC-015: `middleware.IMiddleware`, `metricsmw`, `BuildTraceProvider`, `BuildMetricsRecorder`, `BuildConnectOptions`, `GRPCClientDialOption`, `SyncRecorder`, `NewStreamRecorder`, `IMetricsRecorder`'s attribute types.
- **C-005.** No protobuf change.

## 6. Seams taken from UC-004 (reconciled with `uc-004.md` revision 2)

| UC-004 seam | Step 6 uses it in | Step 6's part |
|---|---|---|
| S1 events and bus (`core/pkg/events`, UC-004 2a; schema Q2, overflow Q3, publisher Q4) | E2, E3 | A subscription with `Buffer` and `OnDrop`. No events commit and no publishing; E3b checks one event per flag and per document |
| S2 middleware seam (`core/pkg/middleware`, UC-004 2c; semantics Q6) | M2, M3 | `Middleware`, `Factory`, `Configured`, `Chain` and `middlewaretest` in the same package; CORS stays outside the chain as S2 fixes (Q17); the official endpoints switch to `EffectiveSelector` in M3c, with `Bind` in their contract runs; `TestMiddlewareSeesH2CRequests` |
| S3 `runtime.Components` (UC-004 3a) | T4e, M3b, E3a | `Telemetry` with `TelemetryConfig`, `Middleware`, `EventSinks` |
| S4 `service.Settings` (UC-004 2c) | T4e | `Telemetry telemetry.Provider` in `endpoint.go` |
| S5 management port, `Config.MetricsHandler` (UC-004 3d) | T4c | `MetricsHandler()` replaces `promhttp.Handler()`; no edit to `management.go` |
| S6 shutdown order and hooks (UC-004 3f; budget Q11) | T4e, E3a | The sinks' flush hook, then the telemetry provider's hook |
| S7 shared pins and benchmarks (UC-004 PR 1) | T1, T4g, E3b | The inventory subtest in `flagd/pkg/runtime/managementport`; re-baseline in T4; E1 dropped |
| S8 order and shared files | Q1, section 7 | Wiring after UC-004 PR 6; shared files merged in merge order and rebased by the mechanic; examples inside `make test` |

The collector endpoint (FR-039) stays UC-004's. UC-020 names it as a gap owned by UC-004.

## 7. PR split

| PR | Branch | Base | Content | Runs |
|---|---|---|---|---|
| T2 | `uc-006/pr-2-telemetry-interface` | `main` | Core telemetry interface, noop, contract | now |
| T3 | `uc-006/pr-3-otel-provider` | T2 | OpenTelemetry provider | after T2 |
| T5 | `uc-006/pr-5-example` | T2 | Example provider | after T2, beside T3 |
| T1 | `uc-006/pr-1-characterization` | UC-004 PR 1 | Telemetry pins | after UC-004 PR 1, beside T2–T5 |
| M2 | `uc-005/pr-2-middleware-factory` | UC-004 PR 2 + T2 | Factory, chain, contract | after UC-004 PR 2 |
| M4 | `uc-005/pr-4-example` | M2 | Example middleware | after M2 |
| E2 | `uc-020/pr-2-event-sink-pipeline` | UC-004 PR 2 + T2 | Pipeline, contract | after UC-004 PR 2, beside M2 |
| E4 | `uc-020/pr-4-example` | E2 | Example sink | after E2 |
| T4 | `uc-006/pr-4-inject-telemetry` | UC-004 PR 6 + T1 + T3 | Injected telemetry everywhere | after UC-004 PR 6 |
| M3 | `uc-005/pr-3-middleware-in-endpoints` | T4 | Chain pins, chain in the endpoints, effective selector | after T4 |
| E3 | `uc-020/pr-3-sinks-in-runtime` | M3 | Sinks in the runtime, single-publisher checks | after M3 |
| T6 / M5 / E5 | `uc-00x/pr-6-close`, `pr-5-close` | their wiring PR + example | Runtime test, status | after their wiring PR |

Every PR description starts with `Part of #18`, `Part of #17` or `Part of #32`.

## 8. Changelog (revision 2)

| Finding | Severity | Outcome |
|---|---|---|
| 1 Event types and publishing owned twice | blocker | Resolved: UC-004 owns `core/pkg/events`, the bus, the decorator and the change events (S1). E2a and E3a are dropped; E3b only checks that one event is published per flag and per document. The schema goes to UC-004 as input; E2 is based on UC-004 PR 2 |
| 2 Middleware outside h2c | blocker | Resolved: one position, inside h2c and outside instrumentation, owned by UC-004 (S2); M3d `TestMiddlewareSeesH2CRequests` |
| 3 Three selector keys, two meanings | major | Resolved: UC-004's key; the semantics become joint Q8; endpoints write the effective selector into `store.SelectorContextKey`; M3c reduced if UC-004 already reads the key |
| 4 Wiring serial against UC-004 PRs 4–5 | major | Resolved: T4 is based on UC-004 PR 6 (its last endpoint PR in revision 2); T4 → M3 → E3; closing PRs T6, M5, E5 split from the examples (Q1, section 7) |
| 5 Unneeded ordering demand; `/metrics` moved | major | Resolved: no ordering demand; M3 fills `HTTPMiddleware`; T4e adds `Settings.Telemetry`; T4c fills `Config.MetricsHandler` (S5, S4) |
| 6 Two buffers, two drop counters | major | Resolved: the subscription is the only buffer; drops are counted once, through telemetry; `Missed` and BR-008 become Q10 |
| 7 UC-003's sync telemetry seam ignored | major | Resolved: `Settings.Metrics` and the builder's fields kept; no `Settings.Telemetry` for sync; based on `uc-003/pr-5`, cloud fetchers checked unchanged (Q5, T4d) |
| 8 gRPC client pin in the wrong module | major | Resolved: moved to `flagd/pkg/runtime` with a loopback server |
| 9 OpenTelemetry boundary gates | major | Resolved: depguard with `!$test`, the boundary rows named as the gate, forbidden regexps instead of allowlists, `WithoutGlobals()` for tests |
| 10 Duplicate pins and benchmarks | major | Resolved: E1 dropped; UC-004 1h's benchmarks used (Q13); T1 on UC-004 PR 1, inventory as a subtest of 1b; re-baseline in T4g |
| 11 Shared-file edits | major | Resolved: one mechanic step per PR, merge order by merge time with a rebase, CI through `make test` (section 5) |
| 12 Flag sync's ConnectRPC path is metered | minor | Accepted: Q7 corrected; T4c moves both wrappers; T1 covers the labels |
| 13 Refused requests invisible to HTTP metrics | minor | Accepted as Q9; CORS listed first, pinned in M3b |
| 14 Shutdown order and budget | minor | Accepted: UC-004 owns the order (S6; revision 2 said S5); the budget is joint Q12; `TestCloseWithinBudget`, `TestSinksFlushedOnShutdown` |
| 15 Concrete attribute value | minor | Accepted: concrete `Value` (Q2), attributes in the allocation test, `BenchmarkAttributeConversion` |
| 16 Package name collision | minor | Accepted: importers alias `telemetry/otel` as `otelprovider`; no rename |
| 17 Stale bases, unneeded waits | minor | Accepted: T2 from today's `main`; M2 and E2 on UC-004 PR 2; stacks named by their real ends |
| uc-004-opposer 4 | major | Resolved with finding 10 |
| uc-004-opposer 5 (Components vs `Config` fields) | major | Resolved: step 6 targets `runtime.Components` (Q15, S3) |
| uc-004-opposer 6 (post-endpoint stages) | minor | Resolved with S5 and Q12 |
| uc-004-opposer 7, 8 | minor | Resolved with S6, S7 |

| Reconciliation with `uc-004.md` revision 2 | — | Moved to UC-004: Q8 → Q6, Q10 → Q3, Q12 → Q11 (its two 5 s budgets replace this plan's single 10 s budget), Q13 → Q5, the event schema → Q2, the publisher → Q4. M2 drops the selector and attribute functions (UC-004 2c has them). E2 uses `OnDrop` instead of `Dropped()`. T4c uses `Config.MetricsHandler`. T1 moves into `managementport`. Wiring comes after UC-004 PR 6 (was PR 5) |

Revision 1's Q9 (configuration) becomes Q15, its Q11 (event schema) becomes input to UC-004, its Q12 becomes Q11, its Q13 a joint question; Q9, Q10 and Q12 are new.

### Changelog (revision 3, `step-6-opposer-r2.md`)

| Finding | Severity | Outcome |
|---|---|---|
| N1 OpenTelemetry boundary row for `flagd/pkg/runtime` cannot pass | blocker | Resolved by Q16 (decided): `flagd/pkg/service/...` keeps the transitive row; `flagd/pkg/runtime` is gated on its direct imports through depguard; GCS's instrumentation is a named gap for UC-010 and UC-001. The narrower pattern the check proposed was tested and fails too: on `main`, `sync/gcs` brings `otel/sdk/metric` and `contrib/instrumentation/net/http`. T4h records the `go list` output on its base |
| N2 CORS position against S2 | major | Resolved by Q17 (decided): CORS stays an endpoint layer outside the chain, per S2; M3b drops the CORS factory; UC-004's 3e pin unchanged; Q7's CORS bullet superseded; Q9's text follows |
| N3 `FromConfig` twice in one test binary | major | Resolved: each `FromConfig`-based T1 row in its own test package; the reviewer checks before and after T4f |
| N4 E3b repeats UC-004's tests | minor | Accepted: E3b tests end to end through a recording sink; UC-004's tests named in the coverage table |
| N5 Who switches the official endpoints | minor | Accepted: M3c does; UC-004 does not; M3c adds `Bind` to the three official contract runs |
| N6 Shared hook budget | minor | Accepted: the sinks' flush stops 1 s before the hook budget ends, so the telemetry provider keeps at least 1 s, pinned with a stalled sink; changelog row 14 corrected to S6 |
