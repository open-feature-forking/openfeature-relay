# Step 19 plan: opposer findings (revision 1)

Plan: `.claude/plans/step-19.md` (revision 1, written against `main` @ c6e0f77f). Checked on `main` @ 5eabf1aa.

Checked against the sources, not the plan's summary of them:
- `service/flag-evaluation`: `connect_service.go`, `factory.go`, `eventing.go`, `flag_evaluator*.go`, `context_utils.go`, every
  `*_test.go` that names `schema.v1`;
- `runtime/from_config.go`, `endpoints.go`, `endpoint_settings.go`, `runtime.go`, `internal/officialset`;
- `core/pkg/service/endpoint.go`, the builder probe template and `builder/internal/needs`, `builder/internal/manifest/validate.go`;
- `test/upgrade/upgrade_test.go`, `test/concurrency/k6/load.js`, `test/distributions/parity_test.go`,
  `flagd/components/generated_test.go`, `distributions/flagd-full/manifest.yaml`;
- `testdata/module-boundaries.txt`, `module-graph-boundaries.txt`, `flagd-full-modules.txt`, `go-packages.txt`, the `Makefile`,
  `.github/scripts/ci-modules.{py,json}`, `.golangci.yml`;
- the generated `buf.build/gen/go/open-feature/flagd` modules in the module cache: `1388a552fc3c` (the pin) and
  `4824facda674` (2026-08-24);
- `js-sdk-contrib/libs/providers/flagd-web/src/lib/flagd-web-provider.ts`;
- UC-001 BR-004/BR-013/BR-014, UC-004 BR-001..BR-012, UC-010 BR-002/BR-006.

Counts: **2 blocker, 6 major, 9 minor.**

## What holds (verified)

- **schema.v1 is deprecated.** At the pin, `schema/v1/schema.pb.go` carries 224 `Deprecated` markers (file option). No current
  provider calls it; nothing outside `service/flag-evaluation` and `test/{upgrade,concurrency}` imports it.
- **flagd-web uses v1.** `flagd-web-provider.ts:21` imports `flagd/evaluation/v1/evaluation_connect`. v2 has no `ResolveAll`, at
  the pin and at `4824facda674`.
- **The fallback is `http.NotFound`.** The generated `schemav1connect` handler (`return "/schema.v1.Service/", ...`) switches on the
  exact path and calls `http.NotFound` in `default`. The v1 and v2 handlers do the same. A plain `http.NotFound` fallback is the
  same bytes and headers. R1 is real but cheap.
- **Shared eventing.** The legacy service only needs the host's `IEvents`. `serveEndpoint` emits `Shutdown` through
  `s.Notify`, so every subscription on that `IEvents` gets `provider_shutdown`. A legacy handler on the same `http.Server` also
  ends with the `BaseContext` cancel. Passing `Events` creates no second subscription, as long as the legacy `Create` allocates
  none.
- **Attribution.** `inputs.settings` builds the resolver with `events.EvaluationObserver(bus, f.Type())`, so `Needs{}` plus the
  host's resolver keeps the events naming `flag-evaluation` (R4).
- **Middleware order.** The dispatch replaces `bufSwitchHandler` inside `MaxBytesHandler`. Metrics, the distribution's
  middleware, CORS and h2c wrap it unchanged.
- **Benchmarks.** No `BenchmarkConnect*` calls schema.v1 (R5).
- **Q4 placement.** The missing-evaluator check is in `needs.Check` (probe-based), not `validate.go`, and the runtime has the
  matching `newEndpointInputs` stop. A host check in both places is consistent. UC-001 BR-013 allows it ("as long as BR-014
  holds").
- **Q5, Q7, Q8 (a)** hold as recommended. UC-010 BR-002 already makes `endpoint-settings: flag-evaluation-legacy` on a slim
  distribution stop the start.

## Blocker

### 1. flagd.evaluation.v1 *is* deprecated in flagd-schemas now (section 1, Q1)

- **Evidence:**
  - The generated module `buf.build/gen/go/open-feature/flagd/protocolbuffers/go@v1.36.12-20260824202443-4824facda674.2` marks
    the whole file: `Deprecated: The entire proto file flagd/evaluation/v1/evaluation.proto is marked as deprecated.` (236
    markers in `evaluation.pb.go`). The pin `1388a552fc3c` has none.
  - This repository already resolves that version: `test/integration/go.mod` lists it (indirect).
  - v2 at `4824facda674` still has **no `ResolveAll`** (its requests are the five `Resolve*` and `EventStream`).
- **So:**
  - The table's "**None.** No file, service, method or message option" and Q1 (a)'s "the only service the protos mark
    deprecated" are true only for the pin. They are false for flagd-schemas' current state.
  - Q1 (a)'s escape ("v1 can follow once flagd-schemas marks it deprecated") is already triggered.
  - The lead decides Q1 on this evidence. It must be correct.
- **Fix:**
  - Rewrite section 1 against `4824facda674`, citing the flagd-schemas commit.
  - Reframe Q1: "v1 is deprecated upstream, but has no bulk replacement and flagd-web still depends on it." Moving it would break
    flagd-web and every RPC `ResolveAll` client on the slim distributions. (a) stays defensible on *consumers*, not on markers.
  - Add a note: bumping the pin turns every v1 symbol into an SA1019 hit in `flag_evaluator_v1.go`, `parity_test.go` and the
    runtime tests. That cost belongs to whichever step bumps it, not this one.
  - Consider (d): move v1's single RPCs, keep `ResolveAll` + `EventStream`. Probably worse (one service split across two
    components), but the lead should see it was weighed.

### 2. Slice C cannot be green on its own: it removes schema.v1 from flagd-full and flagd-minimal before E selects it (section 6)

- **Evidence:**
  - After C, `service/flag-evaluation` serves schema.v1 only when a hosted endpoint mounts it. flagd-full's committed generated
    code (`flagd/components`) and its manifest do not list `flag-evaluation-legacy` until E.
  - On C's PR, CI runs:
    - the upgrade differential: `FLAG_PATTERNS["upgrade"]` matches every `.go` file (`ci-modules.py:44`). `scenarioResolve` posts
      `connect-legacy/*` (`upgrade_test.go:132`), and `scenarioStreams` opens `/schema.v1.Service/EventStream` (`:222`). Both turn
      into 404 against flagd-full;
    - `load-smoke` on every PR: `load.js:191` has `SERVICES = [..., 'schema.v1.Service']` against flagd-minimal.
  - The plan puts the manifests, the regeneration, `test/concurrency` and `test/upgrade` in E. So C's PR is red by construction,
    and the loop requires green CI for `ready`.
- **Fix:** move the manifest change and regeneration of flagd-full and flagd-testbed (plus the `replaces` entry), and the `load.js`
  / `protoset.go` change, into C. That makes C the PR where the observable state stays unchanged. E keeps the slim-distribution
  tests (T6, T8 end-to-end) and `/spec-review`. Alternatively, merge C and E as one PR. Either way, state that the stack must not
  merge C alone.

## Major

### 3. `HandlerOptions` does not "already hold the JSON marshal options" (section 2, `EvaluationHost`)

- **Evidence:** `s.handlerOptions` is only the telemetry's `ConnectHandlerOptions()` (`factory.go:79-82`). The marshal options
  (`EmitUnpopulated: true`, `DiscardUnknown: true`) are built inside `setupHandler` (`connect_service.go:194-198`) and appended
  per handler.
- **So:** a legacy module that trusts the plan builds its handler without `EmitUnpopulated`. schema.v1 JSON responses then drop
  falsy fields (`"value":false`, `"reason"` on zero). `connect-legacy/bool-flag` would differ in the upgrade differential, which
  only catches it once E runs (see blocker 2).
- **Fix:** `EvaluationHost.HandlerOptions` is defined as "every option the host applies to its own handlers, marshal options
  included". The host builds it once with `append(clone(handlerOptions), marshalOpts)` and uses the same slice for v1, v2 and the
  mounted routes. T10 asserts a schema.v1 `ResolveBoolean` of a false flag returns `"value":false` over Connect JSON.

### 4. A host that does not understand extensions drops them silently (section 2, core)

- **Evidence:**
  - "An endpoint the host does not understand fails the host's `Create`" holds only for a host that checks
    `Settings.Extensions`. OFREP, flag sync, the event collector and every third-party endpoint ignore a field they predate.
  - A custom hosted endpoint declaring `Host() = "ofrep"` would be created and never served, with no error from the builder or
    the runtime. That is exactly the silent drop Q4 refuses.
- **Fix:** the host opts in. Add an optional factory interface in core (for example `HostsEndpoints interface{ Hosts() bool }`, or
  fold it into `Hosted`'s counterpart). The runtime and the builder's needs check refuse a hosted endpoint whose host does not
  declare it. T1 and T7 get the case.

### 5. Moving `connect_service_test.go` loses host coverage; several host tests probe the host *through* schema.v1 (T10)

- **Evidence:**
  - `TestConnectServiceUnixConnection` checks the socket path with a grpc-go **schema.v1** client
    (`connect_service_test.go:131-224`, `schemaGrpcV1`).
  - `TestConnectServiceHTTPMetricLabels` (`:42`) uses `schemaConnectV1.ServiceResolveAllProcedure` as a registered route.
  - `evaluation_span_interceptor_test.go` and `evaluation_span_test.go` drive a `legacy` client beside v1/v2.
  - `bulk_characterization_test.go:112` mixes legacy with v1/v2.
- **So:** T10 "moves" them, but they test host behaviour (unix socket, route labels, shutdown) as much as schema.v1. Moved
  wholesale, the host loses its unix-socket test. Left in place, they no longer compile without schema.v1.
- **Fix:** split per test. The host keeps each host-behaviour test, rewritten on v1 (the socket test with a v1 grpc client, the
  metrics test with v1 routes plus one unregistered `/schema.v1.Service/ResolveAll` labelled `/`). The legacy module gets
  schema.v1 copies composed with the host. The plan names the split test by test.

### 6. The upgrade differential does not cover what the seam can break: schema.v1 shutdown, change events, gRPC (T5, R3)

- **Evidence:**
  - `scenarioStreams` reads only the first messages for 3 s (`upgrade_test.go:213-237`). `scenarioSigterm` checks only the exit
    code ("The streams' shutdown differences are #78's", `:201`).
  - schema.v1 is probed over Connect JSON only, never over gRPC or gRPC-Web.
  - `accepted.yaml:48` covers `provider_shutdown` with `runtime shutdown_characterization_test.go`, which streams v1 only
    (`:184-187`).
- **So:** a hosted mount that is handed a fresh `IEvents`, or the wrong `Events`, keeps T5 green. Its streams would then miss
  `configuration_change` and `provider_shutdown` and hold the 5 s shutdown budget. The "key gate of the step" does not gate R3.
- **Fix:** add to E (or C, see blocker 2) a runtime-level test on the flagd-full composition (`officialset` plus legacy):
  - a schema.v1 `EventStream` gets `configuration_change` after a store update;
  - it gets `provider_shutdown` on stop;
  - the runtime returns within the budget.

  Add one grpc-go schema.v1 call to T5 or T6.

### 7. The shared-helper move bloats `service/transport` for frozen code (section 2, "The move"; R6)

- **Evidence:** the move exports `resolve[T constraints]` with its `response[T]`, `constraints`, `resolverSignature`,
  `flagNotFound`, plus `mergeContexts`, `formatContextKeys`, `errFormat`, `selectorFromHeader`, `recordSpanError`, `IEvents` and
  the eventing implementation (`flag_evaluator.go:377-440`, `context_utils.go`, `eventing.go`). It also splits
  `flag_evaluator_types.go`, whose wrappers hold schema.v1 and v1 responses side by side. That is the generic evaluation core of
  flag evaluation, published as API of a transport module that OFREP and flag sync also import.
- **So:** schema.v1 is frozen, deprecated code. Exporting the live v1/v2 internals to serve it couples their future changes to a
  public surface. It also makes C the largest and riskiest slice of the step.
- **Fix:** export only the contract (`EvaluationHost`, `EvaluationExtension`, `Route`) and a minimal `Events` interface
  (`Subscribe`/`Unsubscribe`). The legacy module **copies** the ~150 lines of helpers it needs, plus its own response wrappers.
  Duplication of frozen code is cheaper than a public API (the code-comment rule allows "copied from …; frozen with schema.v1").
  `flag_evaluator_types.go` then only drops the `schemaV1Resp` fields. If the planner keeps the move, justify each exported
  name.

### 8. Files the step must change belong to no slice (section 6, R8)

- **Evidence:** the plan names R8 but assigns none of:
  - `testdata/module-boundaries.txt` and `module-graph-boundaries.txt`. They need a `service/flag-evaluation-legacy` line, and
    the `runtime:. ./cmd` (`:63`) and `service/transport` (`:66`) lines need the new module in their forbidden sets;
  - `testdata/flagd-full-modules.txt` and `testdata/go-packages.txt` (coverage list);
  - `Makefile` `FLAGD_MODULE_DIRS` (`:12`);
  - `runtime/internal/officialset/officialset.go`, which "assembles the components of flagd-full" for the runtime tests;
  - the release dry run's module lists;
  - `testdata/contract-external.txt` if the contract golden grows;
  - `.golangci.yml:89,140` (the existing `flag-evaluation` entries).

  C names the `Makefile` and `ci-modules.json`, but not these.
- **Fix:** list each file under one slice. The boundary and module files go to C. `officialset` goes to C or E (whichever
  carries the test of major 6). The reviewer's check "the module's job ran" stays.

## Minor

1. **R1 is solved, not a risk.** Every fallback today is the generated `default: http.NotFound`. Say so, keep one T4 assertion of
   status, body `404 page not found\n` and headers, and drop "proven byte-identical" as an open question.
2. **Route validation.** The host should refuse a mounted `Prefix` that does not start and end with `/`, that overlaps
   `/flagd.evaluation.v2`, `/flagd`, or another route. Without that, a later v1 move (or an extension) can shadow a live service.
   The dispatch order (v2, routes, `/flagd`) is right; pin it with a test of `/flagdX` (today v1's 404) and
   `/schema.v1.Service` without a trailing slash (today schema.v1's 404).
3. **When `Mount` runs.** `setupHandler` runs at `Serve` (`setupServer`), so a `Mount` error surfaces at serve time, not at
   `Create` as section 2 implies. Either mount in `Create` (with `cfg.Configuration`, which is the same value) or say the error is a
   serve error. Mounting in `Create` matches "fails the host's Create".
4. **The legacy logger.** Today it is the host's logger (already `component=service`) plus `component=flagservice`, so zap writes
   both keys. Built from the legacy's own `Settings.Logger`, the JSON log line loses `component=service`. That is observable. Pin
   it: `Mount` uses `EvaluationHost.Logger`, and T10 asserts the fields.
5. **`Serve` of a hosted endpoint.** UC-004 BR-010 ("Stop on Shutdown") and the `Endpoint` doc describe a `Serve` the runtime never
   calls. Define it: return an error naming the host. Make sure `servicetest.Run` on the hosted factory alone fails with a clear
   message instead of waiting for a listener.
6. **`evaluationMetricRoutes` becomes per instance.** It is a package `var` today (`connect_service.go:65`). With mounted
   procedures, the `RouteFromRequest` must be a closure over the instance's set. Say so in C; the existing test covers the slim
   case once rewritten (major 5).
7. **UC-011 JSON shape.** A `host` field on every listed component changes the JSON for every distribution (BR-003 "Same Output
   for Every Distribution"). Use `omitempty` and add a golden row for a component without it.
8. **R9.** Endpoint types must be unique for `Extensions` keyed by `Type()`. Check where that is enforced (`endpoint-settings`
   is keyed by type, so a duplicate already misbehaves) and cite it, or add the T1 case.
9. **Over-engineering, weighed.** A config switch inside `flag-evaluation` (on in flagd-full via `officialEndpointSettings`, off in
   the slim manifests) is ~20 lines, but it keeps the code linked and contradicts #266's "own component". A build tag fights
   the builder model (one module, one binary per manifest). So the seam is justified for the builder. The cost to cut is the
   transport surface (major 7), not the core interface, which is two names and fits NFR-006. Q6 (a) holds. Say this in the plan,
   so the lead sees the alternatives were weighed.
