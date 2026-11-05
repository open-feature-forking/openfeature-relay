# Step 19 plan: deprecated flag evaluation services as their own component (issue #266)

**Status: revision 1, waiting for the project lead (Q1–Q8, section 0).** Base: `main` @ c6e0f77f.

**Decided by the project lead (#266):**
- the deprecated evaluation services become their own component;
- flagd-full includes it and stays a drop-in replacement;
- flagd-minimal, flagd-minimal-otel, flagd-cloud, flagd-kubernetes and flagd-sync leave it out;
- custom manifests opt in.

## 1. What is deprecated: evidence

The flag evaluation endpoint (`service/flag-evaluation`, `setupHandler` in `connect_service.go`) serves three services on one
port, split by `bufSwitchHandler` on the path prefix:

| Service | Proto marker (pinned `buf.build/gen/go/open-feature/flagd` @ 1388a552fc3c, and flagd-schemas `1daf5ff`) | Who calls it today | Verdict |
|---|---|---|---|
| `schema.v1.Service` (`OldFlagEvaluationService`, `flag_evaluator.go`) | `option deprecated = true;` on the file, header `DEPRECATED; use flagd.evaluation.v1`. The generated Go marks every type `Deprecated: The entire proto file schema/v1/schema.proto is marked as deprecated.` The code says "can be removed as a part of open-feature/flagd#1088". | No current provider (the Go, Java, JS and Python flagd providers use `flagd.evaluation.v2`). | **Deprecated** |
| `flagd.evaluation.v1.Service` (`flag_evaluator_v1.go`) | **None.** No file, service, method or message option; the header has no DEPRECATED line. Upstream flagd docs mark nothing either. | The **flagd-web provider** (`js-sdk-contrib/libs/providers/flagd-web`, its `flagd-web-provider.ts` imports `flagd/evaluation/v1/evaluation_connect`) for `ResolveAll` and `EventStream`. Older releases of every flagd provider. | **Not deprecated.** v2 has **no `ResolveAll`**: v1 is still the only RPC bulk evaluation. |
| `flagd.evaluation.v2.Service` (`flag_evaluator_v2.go`) | none | current providers | current |

The sync side:
- `sync.v1` (`sync/v1/sync_service.proto`) is deprecated as a whole file, but only `flagd-proxy` serves it. flagd-proxy is not a
  relay distribution (UC-010 BR-007), so it is out of scope.
- `flagd.sync.v1.FlagSyncService/GetMetadata` is a deprecated **method** (`option deprecated = true`), plus the deprecated
  `selector` field. The method lives inside the current flag sync service (`service/flag-sync/handler.go:183`), and the
  `disable-sync-metadata` setting already switches it off. A component cannot carry one method of another component's service
  without a second dispatch layer. Q8 keeps it where it is.

**So the evidence supports schema.v1 only.** The issue text ("and the v1 services where a v2 exists") would also move
`flagd.evaluation.v1`. That breaks the flagd-web provider and every `ResolveAll` client on the slim distributions, because v2
has no replacement. This is Q1.

The rest of this plan assumes Q1 (a), schema.v1 only. Section 6 lists what changes under (b).

## 2. The seam: a hosted endpoint that the host mounts on its own server

flagd-full must answer schema.v1 on the flag evaluation port (8013), with the same TLS, socket path, CORS, h2c, body and header
limits, middleware, metrics route attribute, spans, readiness and shutdown. A second listener cannot do that. So the component
opens no listener: **the flag evaluation endpoint, its host, mounts its handlers.**

### Core (generic, no ConnectRPC: NFR-006)

`core/pkg/service`:
- `type Hosted interface { Host() string }`: an optional interface of a `service.Factory`. `Host` names the endpoint type whose
  server serves this endpoint's services.
- `Settings.Extensions []Endpoint`: the runtime fills it for a factory with the endpoints of every hosted factory whose
  `Host()` equals that factory's `Type()`. The host type-asserts each one to its own contract. An endpoint the host does not
  understand fails the host's `Create`, naming both types.
- `ValidateDeclarations`: for a hosted factory, `Host()` is non-empty, has no surrounding whitespace, and is not its own type.

A hosted endpoint still implements `Endpoint`, but the runtime never calls its `Serve`. It is created like every other endpoint
(same `Settings`, `Config` from `endpointSettings`) and closed through `io.Closer` when a build fails (`from_config.go`).

### Runtime

`runtime/from_config.go`, in two passes over `components.Endpoints`:
1. Create every hosted factory first.
2. Create the others, each with `Settings.Extensions` set to the hosted endpoints of its type.

Hosted endpoints are not added to `rt.Endpoints`, so they are not served. A hosted factory whose host is not in the distribution
fails the start: `the service endpoint "flag-evaluation-legacy" is served by the endpoint "flag-evaluation", which the
distribution lacks` (Q4). The listing of `flagd components` (`core/pkg/distribution/inventory.go`) names the host of a hosted
endpoint, in text and JSON (UC-011).

### The flag evaluation host contract (ConnectRPC-specific, in `service/transport`)

UC-004 BR-001 forbids an endpoint module to depend on another endpoint module, so the legacy module cannot import
`service/flag-evaluation`. The contract and the helpers both sides share move to `service/transport`. That module is already a
dependency of flag-evaluation, OFREP and flag sync, and it adds no new dependency.

- `transport.EvaluationHost{Logger, Resolver, Events, Telemetry, Configuration service.Configuration, HandlerOptions
  []connect.HandlerOption}`:
  - `Resolver` is the host's traced resolver, so evaluation events keep naming `flag-evaluation` (`events.EvaluationObserver(bus,
    f.Type())`);
  - `Events` is the host's subscriptions, so a schema.v1 `EventStream` gets the host's `provider_shutdown` and ends with the host;
  - `HandlerOptions` already holds the JSON marshal options and the telemetry's ConnectRPC instrumentation.
- `transport.EvaluationExtension interface { Mount(EvaluationHost) ([]Route, error) }`, with `Route{Prefix string; Handler
  http.Handler; Procedures []string}`.
- The move: `IEvents` and `eventingConfiguration` (`eventing.go`), plus from `flag_evaluator.go` and `context_utils.go` the
  generic `resolve`, `mergeContexts`, `formatContextKeys`, `errFormat`, `selectorFromHeader` and `recordSpanError`. They are
  exported with the smallest names that work. `flag_evaluator_types.go` is split: each response wrapper carries one schema's
  response, so the host no longer imports `schema/v1`.

The host's dispatch replaces `bufSwitchHandler`. The prefix `/flagd.evaluation.v2` goes to v2, then each mounted route's prefix,
then `/flagd` to v1, then the fallback. **The fallback must answer exactly what today's fallback answers.** Today an unknown
path falls through to the schema.v1 ConnectRPC handler, which answers 404. The new fallback is the v1 handler's 404, or a plain
`http.NotFound` proven byte-identical (R1).

`evaluationMetricRoutes` is built from v1, v2 and every mounted route's `Procedures`. On a slim distribution, a schema.v1 path is
then labelled `/` like any unregistered path.

A ConnectRPC client and a grpc-go client both map HTTP 404 to `Unimplemented`. Test T4 proves it with real clients rather than
assuming it.

### The component

- New module `service/flag-evaluation-legacy`, type `flag-evaluation-legacy` (Q2). It is a separate module because UC-001
  BR-004 rejects a module listed twice, so a sub-package of `service/flag-evaluation` cannot be selected beside it.
- It holds `OldFlagEvaluationService` (moved unchanged: logger field `component=flagservice`, tracer `flagEvaluationService`,
  span names, metrics) and its tests.
- `Factory`: `Type()` is `flag-evaluation-legacy`, `Host()` is `flag-evaluation`, `Needs()` is `{}` because it resolves
  through the host's resolver.
- `Create` decodes `Settings.Config` with **no keys** (Q5): any key is `unknown setting`. The endpoint implements
  `transport.EvaluationExtension` and mounts one route, `/schema.v1.Service/`.
- Its module depends on core and service/transport only. A depguard rule (or the boundary check of the per-component CI) pins
  that.

## 3. What each distribution's users observe

| Distribution | Lists the component | Observable change | #78? |
|---|---|---|---|
| flagd-full (and `flagd`) | yes | none; the upgrade differential proves it (T5) | no |
| flagd-testbed | Q3, recommended yes | none | no |
| flagd-minimal, flagd-minimal-otel, flagd-cloud, flagd-kubernetes | no | `schema.v1.Service/*` answers `Unimplemented` (gRPC, ConnectRPC) or 404 (plain HTTP). `flagd.evaluation.v1` and v2 are unchanged. | **no**: these distributions are new in this repository, not today's flagd. Each distribution's section in `docs/relay/distributions.md` and the new UC-010 alternative flow carry it (Q7). |
| flagd-sync | no | **none**: it has no flag evaluation endpoint, and already answers `Unimplemented` on every evaluation path (`test/distributions/flagdsync_test.go:310`) | no |
| Flagsmith | no | none (OFREP only) | no |

A user of a published `:main` image of a slim distribution (step 17) sees the change. It gets a line in the step's PR text and
in `docs/relay/status.md`.

## 4. Tests

| # | Test | Where | Proves |
|---|---|---|---|
| T1 | Hosted factories are created first, never served, handed only to their host, closed on a failed build. A missing host fails the start with the message above. | `runtime/hosted_test.go` (new) | runtime seam, Q4 |
| T2 | `ValidateDeclarations` for `Hosted`: empty, whitespace, self-host | `core/pkg/service/factory_test.go` | core |
| T3 | Contract: `servicetest.Inputs.Extensions []service.Factory`. The harness creates them and passes `Settings.Extensions`. New case "hosted endpoint: served by its host" in the contract inventory golden. The legacy module runs `servicetest.Run(flagevaluation.Factory{}, Inputs{Extensions: legacy, Resolve and Connect over schema.v1})`. The host's own `TestConnectContract` stays on v1, unchanged. | `core/pkg/service/servicetest`, `service/flag-evaluation-legacy/contract_test.go` | UC-004 / UC-008 |
| T4 | **Unimplemented without the component:** the host alone answers `connect.CodeUnimplemented` to a connect-go schema.v1 client, `codes.Unimplemented` to a grpc-go client, and 404 to a plain POST. An unknown path answers today's 404 bytes in both setups. | `service/flag-evaluation/unmounted_test.go` (new) | Q-3 behaviour, R1 |
| T5 | **Upgrade differential unchanged:** `test/upgrade` keeps `connect-legacy/*`, `evaluation-schema-v1`, v1 and v2 green against flagd-full, with **no new `accepted.yaml` entry**. Add one unknown-path probe and one schema.v1 span probe (`otlp_test.go`). | `test/upgrade` | drop-in |
| T6 | Every slim distribution answers `Unimplemented` on `schema.v1.Service/ResolveBoolean` and `EventStream`, and still serves v1 `ResolveAll` and v2. flagd-full serves all three. | `test/distributions/standard_test.go` | per-distribution change |
| T7 | The builder refuses a manifest that lists `flag-evaluation-legacy` without `flag-evaluation`, naming both (probe `host` field, `needs` check) | `builder/internal/needs/needs_test.go`, builder fixture | UC-001 new alt flow |
| T8 | `flagd components` lists `flag-evaluation-legacy` with its host in text and JSON for flagd-full, and not for flagd-minimal | `core/pkg/distribution/inventory_test.go`, `test/distributions` | UC-011 |
| T9 | Load: `test/concurrency/k6/load.js` `SERVICES` and `protoset.go` drop schema.v1 for flagd-minimal. `make test-load-smoke` stays green. | `test/concurrency` | NFR-017 |
| T10 | The moved schema.v1 tests (`flag_evaluator_test.go`, the schema.v1 cases of `evaluation_span_test.go`, `evaluation_metrics_test.go`, `eventstream_handlers_test.go`, `bulk_characterization_test.go`, `connect_service_test.go`) pass in the new module, composed with the host. | `service/flag-evaluation-legacy` | no behaviour lost in the move |

`make flagd-integration-test` (the test-harness suites, the released Go provider in RPC and in-process mode) runs unchanged
against flagd-full.

## 5. Spec changes (slice D)

- **requirements.md:** FR-063 "Select Deprecated Flag Evaluation Services": "As a Distribution Author, I want the deprecated
  `schema.v1` flag evaluation service as its own component that the flag evaluation endpoint serves on its port, so that a
  distribution serves it only when its manifest lists it." Priority Medium, status Planned. Add it to UC-004, UC-010 and UC-001.
- **glossary.md:**
  - **Hosted Endpoint**: "A service endpoint that opens no listener; the endpoint of its host type serves its services on its own
    server." Avoid: service extension. "Extension" already means a third-party component.
  - **Deprecated Evaluation Service**: "`schema.v1.Service`, served only by distributions that select `flag-evaluation-legacy`."
  - The entry **Service Endpoint** gets "…or a hosted endpoint".
- **UC-004:**
  - A10 "Endpoint Served by Another Endpoint" (declare `Host`, receive `EvaluationHost`, mount routes);
  - BR-013 "A Hosted Endpoint Serves on Its Host's Listener" (no listener of its own, the host's settings, events attributed to
    the host, the start fails without the host).
- **UC-001:** A13 "Hosted Endpoint Without Its Host" (the builder refuses, naming both). BR-014 adds "a hosted endpoint needs
  its host".
- **UC-010:**
  - A7 "Client Uses a Deprecated Evaluation Service" (`Unimplemented`; select flagd-full or a custom distribution);
  - BR-008 "Deprecated Evaluation Services Only in flagd-full" (and flagd-testbed under Q3);
  - step 1 adds "the evaluation services its clients call".
- **UC-011:** BR-001 and the listing show a hosted endpoint's host.
- **UC-009:** nothing changes; the differential covers it. `docs/guides/upgrade-to-flagd-full.md:10` stays true.
- **Docs, not spec:**
  - `docs/relay/distributions.md`: the component table and a line per slim distribution;
  - `docs/relay/load-test.md:16`;
  - `docs/relay/status.md`;
  - `docs/relay/how-its-built.md` if it lists the endpoints.
- `/spec-review` runs in the closing PR (E). A known risk is UC-004 BR-001 against the official endpoints' existing
  `service/transport` dependency (R7).

## 6. Slices

S0 lands first, because every other slice compiles against its two names. After S0, **A, B, C and D run in parallel, with no
shared file.** E closes the step.

| Slice | Role | Files (exclusive) | Content | Base |
|---|---|---|---|---|
| **S0** core seam | mechanic | `core/pkg/service/endpoint.go`, `factory.go`, `factory_test.go` | `Hosted`, `Settings.Extensions`, `ValidateDeclarations`, T2 | main |
| **A** runtime and contract | implementer | `runtime/from_config.go`, `runtime/hosted_test.go`, `core/pkg/service/servicetest/**`, `core/pkg/distribution/inventory.go` and its test | two-pass creation, missing-host error, `Inputs.Extensions`, contract case and golden, listing (T1, T3 harness, T8 unit) | S0 |
| **B** builder | implementer | `builder/internal/needs/**`, the probe template in `builder/internal/generate/`, builder fixtures | probe reports `host`, needs check refuses a missing host (T7) | S0 |
| **C** host refactor and legacy module | implementer | `service/flag-evaluation/**`, new files in `service/transport/`, `service/flag-evaluation-legacy/**` (new), `go.work` setup in `Makefile`, `.github/scripts/ci-modules.json`, `test_ci_modules.py`, `.golangci` depguard entry | contract in transport, move, new dispatch and fallback, metric routes, legacy factory (T4, T10). Its last commit adds the T3 contract test after merging A's head. | S0 (then A) |
| **D** spec and docs | implementer | `docs/requirements.md`, `docs/glossary.md`, `docs/use_cases/UC-001,004,010,011*`, `docs/relay/*.md` | section 5; spec lint | main (spec goes straight to main) |
| **E** wiring | implementer, with a mechanic for regeneration | `distributions/flagd-full/manifest.yaml`, `distributions/flagd-testbed/manifest.yaml`, regenerated `flagd/` (`components/`, `go.mod`, `go.sum`), testbed regen, `test/distributions/**`, `test/concurrency/**`, `test/upgrade/**` | select the component, regenerate, T5, T6, T8 end-to-end, T9; `/spec-review` | A + C (stack: S0 → A → C → E) |

The PRs are `phase-19/pr-<x>-<topic>`; every description starts with `Part of #266`. Every slice lands as its own PR against
`main`, or stacked as in the table.

**Under Q1 (b)**, C also moves `flag_evaluator_v1.go` and the module carries both services. E also changes:
- the host's contract test and benchmarks (they call v1; the allocs/op names change, R5);
- the parity test (`test/distributions/parity_test.go:61`);
- about a dozen runtime tests (`runtime/*_test.go` call v1);
- `test/configformat-example`, the testbed tests, `testbed/cmd/target_test.go`;
- `test/concurrency` `ResolveAll`.

## 7. Gates

| Gate | Who | Pass condition |
|---|---|---|
| Unit, `-race`, touched packages | each implementer | green |
| `make lint` with depguard; the boundary check | CI | green; core gains no import |
| Upgrade differential (T5) | CI (`FLAG_PATTERNS` covers `service/**`, `distributions/**`) | green with `accepted.yaml` unchanged (the key gate of the step) |
| `make flagd-integration-test` | CI | green, unchanged |
| allocs/op, library and flagd endpoint benchmarks | CI | no regression; the `BenchmarkConnect*` names stay in `service/flag-evaluation` (v1) |
| `make test-load-smoke` on flagd-minimal | CI | green without schema.v1 |
| Builder regeneration check (generated `flagd/` equals committed) | CI | green |
| Spec lint and `/spec-review` | D, E | no finding left unfixed or unfiled |
| Binary size of flagd-minimal | reviewer | reported. The schema.v1 code leaves the binary, and its descriptors leave with it once nothing imports `schema/v1`, so it should shrink slightly. NFR-003 is not at risk. |

## 8. Risks

- **R1, fallback bytes.** Today an unknown path on 8013 is answered by the schema.v1 handler. A different 404 (headers,
  body) is an observable change in flagd-full. T4 and the T5 probe pin it.
- **R2, metric route attribute.** flagd-full must keep the schema.v1 procedures as route values. The route set is built from the
  mounted routes; the T5 metrics comparison covers it.
- **R3, shutdown of schema.v1 streams.** The legacy `EventStream` must sit on the host's `Events`, or it misses
  `provider_shutdown` and keeps the shutdown budget waiting. T10 keeps `eventstream_handlers_test.go`'s shutdown case.
- **R4, evaluation event attribution.** If the legacy endpoint resolved through its own `Settings.Resolver`, events would name
  `flag-evaluation-legacy`. `Needs{}` plus the host's resolver prevents that. Pin it with one assertion in T10.
- **R5, benchmark names.** Moving code renames benchmarks that `compare-allocs.py` compares by name. Under Q1 (a), none of
  the existing ones move.
- **R6, public surface.** `transport.EvaluationHost` and the exported helpers become API of `service/transport`. Keep the set
  minimal and document it as "for endpoints hosted by flag evaluation".
- **R7, UC-004 BR-001.** It already reads stricter than the official endpoints behave (they depend on `service/transport`). The
  spec review may flag it. The fix is to name `service/transport` in BR-001; that is a spec change in D, not a design change.
- **R8, CI plumbing.** The new module needs its entries in `ci-modules.json` (#176 per-component jobs), the workspace init,
  coverage and the release dry run's module list, or CI quietly skips it. The reviewer checks that the module's job ran.
- **R9, two hosts of one type.** The runtime assumes endpoint types are unique in a distribution. If that is not enforced, T1
  adds the case.

## 0. Questions for the project lead

- **Q1, which services move.** The evidence is in section 1.
  - (a) `schema.v1` only. It is the only service the protos mark deprecated, v2 has no `ResolveAll`, and the flagd-web provider
    calls v1.
  - (b) `schema.v1` and `flagd.evaluation.v1`, as the issue reads. The slim distributions then break flagd-web and RPC bulk
    evaluation.
  - (c) Two components, one per service.

  **Recommended: (a).** v1 can follow once flagd-schemas marks it deprecated, and the seam takes it without design change.
- **Q2, name.**
  - (a) `flag-evaluation-legacy`, module `service/flag-evaluation-legacy`. It matches "the legacy service" in the upgrade guide
    and the code, and stays right if v1 joins later.
  - (b) `flag-evaluation-schema-v1`.
  - (c) `flag-evaluation-deprecated`.

  **Recommended: (a).**
- **Q3, flagd-testbed.**
  - (a) Include it. The testbed stands in for flagd in provider suites (UC-022), and it costs nothing more to link.
  - (b) Leave it out. No current provider suite calls schema.v1.

  **Recommended: (a).**
- **Q4, the component without the flag evaluation endpoint.**
  - (a) The builder refuses the manifest (T7), and the runtime refuses to start (embedders), both naming the host.
  - (b) It is ignored with a warning.

  **Recommended: (a).** It matches BR-014 and the missing-evaluator stop.
- **Q5, configuration keys.**
  - (a) None. It rides the host's port, TLS, CORS, limits and `context-values`, and any key under `endpoint-settings:
    flag-evaluation-legacy` is an unknown setting.
  - (b) A `services` list.

  **Recommended: (a).**
- **Q6, the shape of the seam.**
  - (a) An optional `Hosted` interface on `service.Factory`, listed under `service_endpoints`, with the ConnectRPC contract in
    `service/transport`. There is no new manifest group, component type or codegen.
  - (b) A new component type and manifest group `service_extensions`. That changes the builder, the glossary's component types,
    the components command groups and the contract runner.
  - (c) No seam: the legacy module wraps the flag evaluation factory. Two modules would then produce the type
    `flag-evaluation`, and a custom extension could not reuse it.

  **Recommended: (a).**
- **Q7, documenting the slim distributions' change.**
  - (a) In `docs/relay/distributions.md` and UC-010 A7, not #78, because #78 covers today's flagd / flagd-full, which do not
    change.
  - (b) In #78 as well.

  **Recommended: (a).**
- **Q8, `flagd.sync.v1` `GetMetadata`.**
  - (a) Out of scope. It is one deprecated method of the current flag sync service, and `disable-sync-metadata` already turns it
    off.
  - (b) Move it into a hosted endpoint of flag sync.

  **Recommended: (a).**
