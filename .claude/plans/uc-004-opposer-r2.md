# UC-004 opposer check, revision 2

Plan: `.claude/plans/uc-004.md` revision 2, checked against `.claude/plans/step-6.md` revision 2 (sections 6 and 7) and the code on `main` (a26f04c).

**Result: all 8 findings of `uc-004-opposer.md` are resolved. No new blocker or major; 4 new minors. Seams S1–S8 are complete, and consistent with step 6's section 6, apart from minors N1, N3 and N4.**

## Revision 1 findings

| # | Severity | Status | Check |
|---|---|---|---|
| 1 | blocker | Resolved | S1 is the one definition of the events package. Its schema is the union both plans need: time, format, endpoint, `HasValue`, `Missed`. One publisher per kind. Step 6 confirms: E2a and E3a are dropped, and E3b only checks (step-6.md:244, :321) |
| 2 | blocker | Resolved | S2: `core/pkg/middleware`, at `h2c → CORS → HTTPMiddleware → instrumentation`. That matches today's layering (flag evaluation: h2c added last, so outermost, with CORS, then metrics, inside it; flag sync: h2c → CORS → per-route metrics; OFREP: CORS outermost). `TestHTTPMiddlewareSeesH2CRequests` is in 3e. Q6 leaves the semantics to the project lead |
| 3 | major | Resolved | `Runtime.DefaultFormat` is set only by `FromConfig`. Literals keep `SetState`. The gate "`git diff` empty on `runtime_test.go`, `runtime_span_test.go`" is in section 6, and `TestFlagSyncSpanOnAcceptPath` pins the flagd-full path |
| 4 | major | Resolved | S7: one owner for the benchmark names, and one scraping binary (`flagd/pkg/runtime/managementport`). The OpenTelemetry Prometheus exporter is an *unchecked* collector: `Describe` is empty (exporter.go:161-166, v0.69.0). So a second `FromConfig` in one binary registers fine, and the collision only shows on a gather. 1c–1e start runtimes in `flagd/pkg/runtime` without scraping, and UC-014's gather pin lives in its own `syncmetrics_test` binary. S7's rule is therefore the right constraint |
| 5 | major | Resolved | S3's `Components` is the one registry, and step 6's T4e, M3b and E3a target it (step-6.md:289) |
| 6 | minor | Resolved | S6's stages and hooks, Q11's two 5 s budgets, and `TestShutdownOrder`. Step 6 registers the sink flush, then the telemetry provider's shutdown (E3a, T4e) |
| 7 | minor | Resolved | S5's optional `/metrics` handler, filled by T4c (but see N4) |
| 8 | minor | Resolved | S4: T4 adds `Settings.Telemetry`; adding a field breaks no factory |

## New findings

### N1. minor: S2's "never answers a CORS preflight" stops being a property of the seam after M3

- **Evidence:**
  - S2 states that the chain "never answers a CORS preflight", because CORS sits outside `HTTPMiddleware`.
  - Step 6's M3b moves CORS **into** the chain as its first member ("first in `Components.Middleware`", step-6.md:213). From then on, the guarantee depends on list order. A distribution (UC-001's manifest order, UC-005 BR-003) that lists an authenticating middleware before CORS sends preflights to it.
  - Step-6.md:67 still argues from "CORS sits outside `HTTPMiddleware`".
- **Fix:** state S2 as an ordering rule that holds after M3: CORS, when present, runs first. Either the runtime places it first regardless of list position, or startup fails if it isn't first. Pin this with step 6's `TestPreflightBeforeAuthentication`.

### N2. minor: the observer's signature can't report the typed outcome it promises

- **Evidence:**
  - Q4 and 2b: `typed.WithObserver(func(ctx, evaluator.Request, evaluator.Result))` is "called once per flag with the typed outcome the client sees, `TYPE_MISMATCH` included".
  - `TYPE_MISMATCH`, numeric conversion and the absent value → default are decided by the adapter. They aren't in the evaluator's untyped `Result`.
- **Fix:**
  - Say that the adapter passes the `Result` it finally returns, with its own error code and converted value. Decide whether `Event.Value` is the converted value or the evaluator's.
  - Note that the single-evaluator fast path must stamp `Result.Format` too, which UC-007's `TestRouterSingleEvaluatorFastPath` alloc bound has to keep.
  - 3e's `TestOfficialEndpointsPublishOncePerFlag` and step 6's `TestOneEvaluationEventPerFlag` (E3b) check the same thing. Keep one of them.

### N3. minor: S8's bases leave out T2

- **Evidence:** S8 says "M2 bases on UC-004 PR 2 … and E2 on UC-004 PR 2". Step 6's section 7 bases both on "UC-004 PR 2 + T2": M2's `Settings.Telemetry` and E2's drop counter need T2's interface.
- **Fix:** S8 should name both bases, and say that the branch is cut from `main` once both have merged, because a stacked PR has only one base.

### N4. minor: S5's `Config.MetricsHandler` conflicts with S3's rule, and the two plans disagree about who edits `management.go`

- **Evidence:**
  - S3 says `Config` "keeps only the operator settings derived from flags and files", yet S5 puts an `http.Handler` into `Config.MetricsHandler`.
  - Section 9 (step-6 row 5) says T4 "sets `Config.MetricsHandler` in `runtime/management.go`". S5 and step-6.md:177 and :293 say T4 makes "no edit to `management.go`".
- **Fix:** put the handler in `Components` (or in a `FromConfig` option), not in `Config`, and correct the section 9 row so that T4 sets it in `from_config.go`.
