# Step 6 plan: opposer check of revision 2

Plan: `.claude/plans/step-6.md` revision 2, checked against `.claude/plans/uc-004.md` revision 2 (section 1, S1–S8), `step-6-opposer.md`, and the code on `main` and `origin/uc-003/pr-5`.

Counts: **17 of 17 findings resolved.** The revision introduces **6 new findings: 1 blocker, 2 major, 3 minor.** The reconciliation with UC-004 holds, except for the CORS position (N2).

## Resolved

| # | Sev. | Check |
|---|---|---|
| 1 | blocker | Holds. E2 has no `core/pkg/events` commit and no E3a. Publishing is UC-004's: the typed adapter's observer (S1, UC-004 Q4) and the runtime's change events (3b). The schema and origin values (`service`/`in-process`) match UC-004 Q2. E2 is based on UC-004 PR 2 |
| 2 | blocker | Holds for h2c. S2 puts the chain inside h2c, and both plans pin it: UC-004 3e `TestHTTPMiddlewareSeesH2CRequests`, step 6 M3d. See N2 for CORS |
| 3 | major | Holds. There is one key, in `core/pkg/middleware` (UC-004 2c). The semantics are UC-004 Q6 (intersection). Endpoints write the effective selector into `store.SelectorContextKey`. See N5 for who switches the official endpoints |
| 4 | major | Holds. The wiring order is UC-004 PR 3 → 4 → 6 → T4 → M3 → E3, the same in both plans (S8). The closing PRs T6, M5 and E5 are split from the examples |
| 5 | major | Holds. There is no ordering demand. T4e adds `Settings.Telemetry` (S4). T4c sets `Config.MetricsHandler` (S5). M3b fills `HTTPMiddleware` |
| 6 | major | Holds. The subscription is the only buffer (`SubscribeOptions{Buffer; OnDrop}`). Each drop is counted once, by reason. A sink receives `Missed` events as they are. The BR-008 amendment is in E5b |
| 7 | major | Holds. `sync.Settings.Metrics`, `fetching.Config.Metrics` and the builder's `StreamMetrics`/`GRPCDialOptions` stay, and `sync.Settings` gains no field. The base is `uc-003/pr-5` |
| 8 | major | Holds. The test moves to `flagd/pkg/runtime` with a loopback server, but see N3 |
| 9 | major | Holds for (a)–(c): depguard `!$test`, forbidden-regexp rows, `WithoutGlobals()`. But the new `flagd:./pkg/runtime` row cannot pass: N1 |
| 10 | major | Holds. E1 is dropped. The benchmark names are S7's. T1's inventory is a subtest in `managementport`. T4g re-baselines |
| 11 | major | Holds. There is one mechanic step per PR, PRs merge in merge order with a rebase, and the examples run inside `make test` |
| 12–17 | minor | Accepted as written: Q7 corrected, T1 covers `SyncFlags`/`FetchAllFlags`; Q9; UC-004 Q11; concrete `Value` plus the allocation test; the `otelprovider` alias; the real stack ends and T2 cut from `main` |

## New

### N1 (blocker). The new OpenTelemetry boundary row for `flagd/pkg/runtime` cannot be green

- **Step:** T4h, section 4 ("flagd OpenTelemetry boundary").
- **Evidence:**
  - On `uc-003/pr-5`, `sync/builder/syncbuilder.go:16` imports `sync/gcs`, which reads objects with `cloud.google.com/go/storage`.
  - On `main`, `GOWORK=off go list -deps ./...` in `sync/gcs` lists `go.opentelemetry.io/otel/attribute/internal`, `go.opentelemetry.io/auto/sdk/internal/telemetry` and others. The storage client and Google's HTTP transport are instrumented with OpenTelemetry.
  - `flagd/pkg/runtime` imports `sync/builder`, so `go list -deps ./pkg/runtime` contains `go.opentelemetry.io`, and the row `flagd:./pkg/runtime:…go.opentelemetry.io…` fails in T4.
  - The `sync/gcs` row does not forbid OpenTelemetry for this reason (`testdata/module-boundaries.txt:21` on `uc-003/pr-5`).
- **Fix:**
  - Scope the row to what the runtime itself must not use: `go.opentelemetry.io/otel/sdk|go.opentelemetry.io/contrib/instrumentation/net/http|otelconnect|prometheus/client_golang|openfeature-relay/telemetry/`. Check that `sync/gcs` brings none of these.
  - Or gate on the runtime's direct imports (`go list -f '{{join .Imports "\n"}}'`) in addition to the `flagd:./pkg/service/...` row, which has no sync dependency.
  - Name the consequence in the accepted gaps: a distribution with the GCS source carries the OpenTelemetry API, even with the noop telemetry provider (UC-006 BR-002, FR-024). UC-010's flagd-minimal must leave out `sync/gcs`.
  - Prove the chosen regexp with `go list -deps` on the T4 base before T4h is written.

### N2 (major). Where CORS sits: S2 and M3b disagree

- **Step:** Q7, Q9, M3b, section 6 row S2. Against UC-004 S2 and 3e.
- **Evidence:**
  - S2 fixes `h2c → CORS → HTTPMiddleware → instrumentation`, so CORS is outside the chain. UC-004 3e pins that "a CORS preflight never reaches" the test middleware.
  - M3b instead makes CORS "the chain's first member", which puts it inside `HTTPMiddleware`. Q9's argument ("because CORS sits outside `HTTPMiddleware`, a preflight is answered before any authentication middleware sees it") then holds only while `flagd/cmd` lists CORS first.
  - Once UC-001's manifest order decides the order (UC-005 BR-003), an authentication middleware listed before CORS answers preflights with 401.
  - UC-004's 3e pin fails in M3b, because the endpoint no longer has its own CORS layer.
- **Fix:** decide once, in UC-004 S2, and get the project lead's decision if needed:
  - **(a)** CORS stays a fixed endpoint layer outside the chain, configured from `--cors-origin`, and is not a middleware factory. BR-004 is amended to "no official middleware". M3b drops the CORS factory.
  - **(b)** CORS is middleware, and the runtime always places it first, whatever the configured order. BR-003 is amended to say so. UC-004's 3e pin is changed in M3b's commit, and the change is listed there.
  - Q9's text follows the choice.

### N3 (major). The T1 rows in `flagd/pkg/runtime` share one test binary with process-wide telemetry

- **Step:** section 2: `TestTracePropagationCharacterization`, `TestTelemetryStartupCharacterization`, `TestGRPCSyncClientInstrumentationCharacterization`; T4f.
- **Evidence:**
  - Today `FromConfig` installs global providers. Its default metric reader is `prometheus.New()` (`telemetry/otel/builder.go:146`), which registers with the default registerer.
  - The repo's convention is one `FromConfig` per test binary. `flagd/pkg/runtime/syncmetrics_test/sync_metrics_test.go` says so ("a test binary of its own and calls FromConfig exactly once"), and so does UC-004 S7's `managementport`.
  - Three T1 rows that build through `FromConfig` in the same package would conflict on registration and global state. Today the conflict shows up only as a log line.
  - After T4f, a telemetry factory error stops startup, so a second `FromConfig` in that binary fails instead.
- **Fix:** give each `FromConfig`-based T1 row its own test package, as `syncmetrics_test` and `managementport` do, or have those rows use a non-Prometheus reader (`OTEL_METRICS_EXPORTER=none` plus an in-memory reader). Have the reviewer check that no test binary calls `FromConfig` twice with the Prometheus default, before and after T4f.

### N4 (minor). E3b repeats UC-004's tests

- **Step:** E3b.
- **Evidence:** UC-004 3e already has `TestOfficialEndpointsPublishOncePerFlag` (ConnectRPC and OFREP, single and bulk), and 3b has `TestChangeEventPerChangedDocument`. E3b's `TestOneEvaluationEventPerFlag` and `TestOneChangeEventPerDocument` pin the same thing again.
- **Fix:** E3b tests through a sink instead: one recorded batch entry per flag and per document, end to end through the pipeline. Or drop it and name UC-004's tests in the coverage table.

### N5 (minor). Who switches the official endpoints to `EffectiveSelector`

- **Step:** M3c, section 6 row S2. Against UC-004 S2 ("Every endpoint writes the effective selector…"), UC-004 section 3's gap ("official endpoints honouring bound selectors: UC-005"), and UC-004 6a (`servicetest.Run` on the official factories, with an optional `Bind` case).
- **Evidence:** UC-004 S2's wording reads as if UC-004 already switches the official endpoints, while its gap table and step-6 M3c say M3 does. The revision 2 changelog's "M3c reduced if UC-004 already reads the key" leaves it open.
- **Fix:** state that UC-004 does not switch the official endpoints. UC-004 6a passes no `Bind` for them, and M3c adds `Bind` to the three official contract runs.

### N6 (minor). One hook budget is shared by the sinks and telemetry, and the changelog points at the wrong seam

- **Step:** E3a, T4e, section 8 row 14.
- **Evidence:** UC-004 Q11 gives the sinks' flush and the telemetry `Shutdown` one 5 s budget, in that order. A stalled sink uses all of it, and the telemetry provider then gets an expired context and loses its buffered spans. Section 8 row 14 says "UC-004 owns the order (S5)"; the shutdown order is S6.
- **Fix:** cap the sinks' flush at a share of the hook budget, so the telemetry provider keeps a minimum (for example 1 s). Pin this in `TestSinksFlushedOnShutdown` with a stalled sink. Correct the row to S6.
