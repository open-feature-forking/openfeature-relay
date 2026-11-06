# Step 19 plan: deprecated flag evaluation services as their own component (issue #266)

**Status: revision 2, waiting for the project lead (Q1–Q8, section 0).** It answers `step-19-opposer.md`: 2 blockers, 6 majors,
9 minors. Base: `main` @ 6e222eb5.

**Decided by the project lead (#266):**
- the deprecated evaluation services become their own component;
- flagd-full includes it and stays a drop-in replacement;
- flagd-minimal, flagd-minimal-otel, flagd-cloud, flagd-kubernetes and flagd-sync leave it out;
- custom manifests opt in.

## Revision 2: what changed

| Finding | Resolution |
|---|---|
| B1 v1 is deprecated upstream | Section 1 is rewritten against `4824facda674`, where the whole v1 file is deprecated. The project lead adds that **the flagd-web provider is deprecated too**, so its use of v1 `ResolveAll` no longer argues for keeping v1. Q1 now recommends **(b): schema.v1 and flagd.evaluation.v1 both move into the legacy component; flagd-full keeps both; the slim distributions serve only v2.** The other options stay listed. Sections 2–6 follow (b): two mounted routes, the v1 test split, and the v1 consumers C must move. The note on bumping the pin (SA1019 hits) is in section 1. |
| B2 C is red alone | C now carries everything that keeps the observable state unchanged: the flagd-full and flagd-testbed manifests with their regeneration, `officialset`, `load.js` / `protoset.go`, and the runtime composition test (M6). C stacks on A, which brings the runtime's two-pass creation. E keeps only the slim-distribution tests and `/spec-review`. Rule: C never merges without A below it, and the stack merges bottom-up. |
| M3 marshal options | `EvaluationHost.HandlerOptions` is defined as every option the host applies to its own handlers, marshal options included. The host builds the slice once and uses it for v1, v2 and the mounted routes. T10 asserts `"value":false` on a schema.v1 `ResolveBoolean` over Connect JSON. |
| M4 silent drop | Hosting is opt-in: core adds `Hosting interface{ Hosts() bool }`. The runtime (T1) and the builder's needs check (T7) refuse a hosted endpoint whose host type is absent **or does not declare `Hosts()`**. |
| M5 host coverage | Section 4, "Test split", names each test: what stays on the host (rewritten on v2) and what moves to the legacy module. |
| M6 shutdown, change events, gRPC | New T11 in C. On the `officialset` plus legacy composition, a schema.v1 `EventStream` gets `configuration_change` after a store update and `provider_shutdown` on stop, and the runtime returns within the budget. T5 gains one grpc-go schema.v1 call. |
| M7 transport surface | `service/transport` exports only the contract: `EvaluationHost`, `EvaluationExtension`, `Route` and a two-method `Events` interface. The legacy module **copies** the frozen helpers and its own response wrappers. `flag_evaluator_types.go` only drops its `schemaV1Resp` fields. |
| M8 orphan files | Section 6 assigns each file to a slice: the boundary and module testdata, `go-packages.txt`, `FLAGD_MODULE_DIRS`, `.golangci.yml`, `officialset` and `contract-external.txt`. The release dry run derives its modules from the manifests; nothing lists flag-evaluation (`grep` of `.github/scripts/release*.sh` and the workflows), so it needs no change. |
| m1 R1 | Solved: every generated handler's `default` is `http.NotFound`. One T4 assertion pins status, body `404 page not found\n` and headers. |
| m2 route validation | The host refuses a mounted prefix that does not start and end with `/`, or that overlaps `/flagd.evaluation.v2.Service/` or another route. T4 pins `/flagdX` and the service paths without a slash (today's 404). |
| m3 when Mount runs | The host mounts in `Create` with `cfg.Configuration`, so a mount error fails `Create`. |
| m4 logger fields | `Mount` uses `EvaluationHost.Logger`, the host's `component=service` logger, and adds `component=flagservice`. T10 asserts both keys. |
| m5 Serve of a hosted endpoint | It returns `the endpoint "<type>" is served by "<host>", not on its own`. `servicetest.Run` on a hosted factory without `Inputs.Host` fails with that message. |
| m6 metric routes per instance | `evaluationMetricRoutes` becomes per instance, and `RouteFromRequest` is a closure over the instance's set (C). |
| m7 UC-011 JSON | `host` is `omitempty`. The golden has a row with it and one without. |
| m8 R9 | Nothing enforces unique endpoint types; only modules are unique (UC-001 BR-004). The runtime refuses two factories of one type when a hosted endpoint names that type. T1 has the case. |
| m9 alternatives | Section 7, "Considered": a config switch inside flag-evaluation, a build tag, a new manifest group. |

## 1. What is deprecated: evidence

The flag evaluation endpoint (`service/flag-evaluation`, `setupHandler` in `connect_service.go`) serves three services on one
port. Markers are from the generated `buf.build/gen/go/open-feature/flagd/protocolbuffers/go` modules: the pin `1388a552fc3c`
(2026-02-17, every relay module) and `4824facda674` (2026-08-24, flagd-schemas' current state, already resolved indirectly by
`test/integration/go.mod`).

| Service | `1388a552fc3c` (pin) | `4824facda674` (current) | Who calls it | Has a replacement |
|---|---|---|---|---|
| `schema.v1.Service` (`OldFlagEvaluationService`) | file deprecated | file deprecated | no current provider | yes (v1, v2) |
| `flagd.evaluation.v1.Service` | no marker | **file deprecated** (`Deprecated: The entire proto file flagd/evaluation/v1/evaluation.proto is marked as deprecated.`) | the **flagd-web provider, itself deprecated** (project lead; `flagd-web-provider.ts:21`: `ResolveAll`, `EventStream`), older releases of every flagd provider, any RPC bulk client | singles and stream: v2. Bulk: none, since v2 has no `ResolveAll` at either version; OFREP bulk is the live alternative |
| `flagd.evaluation.v2.Service` | none | none | current Go, Java, JS, Python providers | — |

The sync side is out of scope (Q8):
- the deprecated `sync.v1` is served only by flagd-proxy, which is not a relay distribution;
- `flagd.sync.v1/GetMetadata` is one deprecated method of the current flag sync service, and `disable-sync-metadata` turns it
  off.

**Both schema.v1 and v1 are deprecated upstream, and so is v1's last current consumer, flagd-web.** The rest of this plan follows
the recommended Q1 (b): both move into the legacy component. Section 0 says what changes under the other options.

**The pin.** Bumping to `4824facda674` turns every v1 symbol into an SA1019 hit:
- `flag_evaluator_v1.go`;
- the host's contract test and benchmarks;
- `test/distributions/parity_test.go`;
- about a dozen `runtime/*_test.go`.

That cost belongs to whichever step bumps the pin, not this one. This plan keeps the pin. Under (b), most of those hits land
in the legacy module, where a single `//nolint:staticcheck` covers frozen code.

## 2. The seam: a hosted endpoint, mounted by an opted-in host

flagd-full must answer schema.v1 on the flag evaluation port (8013), with the same TLS, socket path, CORS, h2c, limits,
middleware, metric route attribute, spans, readiness and shutdown. A second listener cannot do that. So the component opens no
listener: **the flag evaluation endpoint, its host, mounts its handlers.**

### Core (generic, no ConnectRPC: NFR-006)

`core/pkg/service`, four names:
- `Hosted interface{ Host() string }`, on a factory: the endpoint type whose server serves this endpoint.
- `Hosting interface{ Hosts() bool }`, on a factory: this endpoint's type mounts hosted endpoints (M4).
- `Settings.Extensions []Endpoint`: the hosted endpoints of this factory's type.
- `ValidateDeclarations` checks `Host()`: it is non-empty, has no surrounding whitespace, and is not the factory's own type.

A hosted endpoint implements `Endpoint`, and its `Serve` returns an error naming its host (m5). The runtime never calls it. It
is closed through `io.Closer` when a build fails.

### Runtime (`runtime/from_config.go`)

The runtime creates every hosted factory first, then the others, handing each `Hosting` factory its `Settings.Extensions`. Hosted
endpoints are not served. The start fails, naming both types, when:
- the host type is absent;
- the host does not declare `Hosts()` (M4);
- two factories share the host's type (m8).

`flagd components` (`core/pkg/distribution/inventory.go`) shows the host in text and as an `omitempty` JSON `host` field (m7).

### The flag evaluation contract (`service/transport`, contract only: M7)

- `EvaluationHost{Logger, Resolver, Events, Telemetry, Configuration service.Configuration, HandlerOptions
  []connect.HandlerOption}`:
  - `Logger` is the host's `component=service` logger (m4);
  - `Resolver` is the host's traced resolver, so evaluation events keep naming `flag-evaluation` (R4);
  - `Events` is the host's subscriptions, so `provider_shutdown` and `configuration_change` reach schema.v1 streams;
  - `HandlerOptions` is **every option the host applies to its own handlers**: the telemetry's ConnectRPC instrumentation **and**
    the JSON marshal options (`EmitUnpopulated`, `DiscardUnknown`). The host builds it once and uses it for v1, v2 and every
    route (M3).
- `Events interface{ Subscribe(ctx, id, *store.Selector, chan service.Notification); Unsubscribe(id) }`. Today's `IEvents`
  satisfies it.
- `EvaluationExtension interface{ Mount(EvaluationHost) ([]Route, error) }` and `Route{Prefix string; Handler http.Handler;
  Procedures []string}`.

Nothing else moves to transport. The legacy module copies what it needs of `resolve`, `mergeContexts`, `formatContextKeys`,
`errFormat`, `selectorFromHeader`, `recordSpanError` and the schema.v1 response wrappers (about 150 lines). A one-line comment
marks them as frozen with schema.v1.

### The host (`service/flag-evaluation`)

- `Factory` implements `Hosting`.
- `Create` type-asserts each `Settings.Extensions` entry to `EvaluationExtension`, failing with both types otherwise, and calls
  `Mount` there (m3).
- The host validates each route (m2).
- The dispatch replaces `bufSwitchHandler`, in this order: `/flagd.evaluation.v2.Service/` → v2, then the mounted routes, then
  `http.NotFound`. That is the bytes today's generated `default` writes (m1). Today's `/flagd` prefix rule goes, because v1 is
  now a mounted route. `/flagdX`, `/flagd.evaluation.v2` and `/flagd.evaluation.v1.Service` without the slash still reach a 404
  with the same bytes; T4 pins them.
- `evaluationMetricRoutes` is built per instance from v2 and the mounted `Procedures`; `RouteFromRequest` closes over it (m6).
- `flag_evaluator_types.go` keeps only the v2 wrappers; `flag_evaluator.go`, `flag_evaluator_v1.go` and their tests leave the
  host.

### The component (`service/flag-evaluation-legacy`, type `flag-evaluation-legacy`, Q2)

- It is a separate module, because UC-001 BR-004 rejects a module listed twice. It depends on core and `service/transport`
  only.
- It holds `OldFlagEvaluationService` (schema.v1: logger field `component=flagservice`, tracer `flagEvaluationService`) and
  `FlagEvaluationService` (v1: logger field `component=flagd.evaluation.v1`), with their span names and metrics unchanged.
- `Host()` is `flag-evaluation`, and `Needs()` is `{}`.
- `Create` takes no keys (Q5) and allocates no subscriptions.
- `Mount` returns two routes, `/schema.v1.Service/` and `/flagd.evaluation.v1.Service/`, with their procedures. v1 uses the
  host's `context-values`, `header-to-context-key-mappings` and `stream-deadline` from `EvaluationHost.Configuration`.

## 3. What each distribution's users observe

| Distribution | Lists the component | Observable change | #78? |
|---|---|---|---|
| flagd-full (and `flagd`) | yes | none (T5, T11) | no |
| flagd-testbed | Q3, recommended yes | none | no |
| flagd-minimal, -minimal-otel, -cloud, -kubernetes | no | `schema.v1.Service/*` and `flagd.evaluation.v1.Service/*` answer `Unimplemented` (gRPC, ConnectRPC) or 404 (HTTP); v2 is unchanged. **A user of the deprecated flagd-web provider** gets `Unimplemented` on `ResolveAll` and `EventStream`, so the provider does not become ready. The remedy is flagd-full, a custom distribution that lists the component, or OFREP with the ofrep-web provider. Old releases of the RPC providers that still call v1 see the same thing. | no, these are new distributions. Each one's section in `docs/relay/distributions.md` and UC-010 A7 carry it (Q7). |
| flagd-sync | no | none: it has no evaluation endpoint (`flagdsync_test.go:310` already pins `Unimplemented`) | no |
| Flagsmith | no | none | no |

Users of the slim `:main` images (step 17) get a line in the PR text and in `docs/relay/status.md`.

## 4. Tests

| # | Test | Where | Slice |
|---|---|---|---|
| T1 | Hosted endpoints are created first, never served, given only to their host, and closed on a failed build. The start fails for a missing host, a host without `Hosts()`, or a duplicate host type. | `runtime/hosted_test.go` | A |
| T2 | `ValidateDeclarations` of `Host()` | `core/pkg/service/factory_test.go` | S0 |
| T3 | `servicetest.Inputs.Host service.Factory`: the hosted factory is run composed with its host. The new inventory kind "hosted" goes in the golden and in `testdata/contract-external.txt`. A hosted factory without `Inputs.Host` fails with m5's message. The legacy contract test is `servicetest.Run(legacy.Factory{}, Inputs{Host: flagevaluation.Factory{}, …schema.v1 clients})`. | `core/pkg/service/servicetest`, `service/flag-evaluation-legacy/contract_test.go` | A (harness), C (legacy test) |
| T4 | Host alone: schema.v1 and v1 get `CodeUnimplemented` (connect-go), `codes.Unimplemented` (grpc-go) and 404 (HTTP). An unknown path, `/flagdX`, `/schema.v1.Service` and `/flagd.evaluation.v1.Service` get status `404`, body `404 page not found\n` and today's headers. Route validation refuses a bad prefix. | `service/flag-evaluation/dispatch_test.go` | C |
| T5 | The upgrade differential stays green with `accepted.yaml` unchanged, and gains one grpc-go call each on schema.v1 and v1 `ResolveBoolean`. | `test/upgrade` | C |
| T6 | Every slim distribution answers `Unimplemented` on schema.v1 and v1 `ResolveBoolean`, v1 `ResolveAll` and both `EventStream`s, and serves v2. | `test/distributions/standard_test.go` | E |
| T7 | The builder refuses `flag-evaluation-legacy` without `flag-evaluation`, and a fixture whose host has no `Hosts()`. The probe reports `host` and `hosts`. | `builder/internal/needs`, the probe template, a fixture | B |
| T8 | `flagd components`: flagd-full lists the host. flagd-minimal does not list the component. The JSON golden has rows with and without `host`. | `core/pkg/distribution` (A), `test/distributions` (E) | A, E |
| T9 | `load.js` `SERVICES` keeps only v2, and its `ResolveAll` rotation goes. `protoset.go` drops schema.v1 and v1. `load-smoke` is green. | `test/concurrency` | C |
| T10 | The schema.v1 and v1 tests moved into the legacy module, composed with the host: a false flag returns `"value":false` over Connect JSON on both (M3); the log lines carry `component=service` plus `component=flagservice` or `flagd.evaluation.v1` (m4); events name `flag-evaluation` (R4); v1 honours the host's header mappings and stream deadline. | `service/flag-evaluation-legacy` | C |
| T11 | Runtime composition test (`officialset` plus legacy): a schema.v1 and a v1 `EventStream` each get `configuration_change` after a store update and `provider_shutdown` on stop, and the runtime returns within the endpoint budget (M6). | `runtime/legacy_composition_test.go` | C |

### Test split (M5)

| Today in `service/flag-evaluation` | Host keeps | Legacy module gets |
|---|---|---|
| `connect_service_test.go` `TestConnectServiceUnixConnection` (schema.v1 grpc client) | yes, rewritten on a v2 grpc client | a schema.v1 grpc copy over a socket |
| `connect_service_test.go` `TestConnectServiceHTTPMetricLabels` | yes, on v2 routes, plus `/schema.v1.Service/ResolveAll` and `/flagd.evaluation.v1.Service/ResolveAll` labelled `/` when unmounted | a copy asserting their procedure labels when mounted |
| `contract_test.go` `TestConnectContract` (v1 client) | yes, rewritten on a v2 client | the composed contract (T3) on schema.v1 and v1 |
| `bench_test.go` `BenchmarkConnect*` (v1) | rewritten on v2 as `BenchmarkConnectV2*` (no v2 `ResolveAll`) | the v1 benchmarks under **unchanged names**, so `compare-allocs.py` keeps comparing them (R5) |
| `evaluation_span_interceptor_test.go`, `evaluation_span_test.go` | v2 cases | the `legacy` and v1 cases |
| `bulk_characterization_test.go` | v2 rows | the schema.v1 and v1 rows (bulk is legacy-only) |
| `eventstream_handlers_test.go`, `evaluation_metrics_test.go`, `connect_service_test.go` (the rest) | v2 cases | schema.v1 and v1 cases |
| `flag_evaluator_test.go`, `flag_evaluator_v1_test.go` | — | all of it |

**v1 consumers outside the endpoint (moved in C, so that C is green):**
- runtime tests that build on `officialset` keep working, because `officialset` gains the legacy factory;
- `bound_selector_test.go`, `middleware_characterization_test.go`, `endpoint_settings_characterization_test.go` and
  `managementport/inventory_test.go` build their own components: each one moves to v2, or adds the legacy factory when v1 is
  what it pins;
- `test/distributions/parity_test.go:61-64` moves to v2, and its bulk row moves to OFREP bulk;
- `test/configformat-example` (a custom distribution) moves to v2.

The testbed's tests (`test/testbed`, `testbed/cmd/target_test.go`) stay on v1 under Q3 (a).

`make flagd-integration-test` runs unchanged against flagd-full. The released Go provider v0.8.0 already calls v2.

## 5. Spec changes (slice D)

- **requirements.md:** FR-063 "Select Deprecated Flag Evaluation Services": "As a Distribution Author, I want the deprecated
  `schema.v1` and `flagd.evaluation.v1` flag evaluation services as their own component that the flag evaluation endpoint serves on its port, so that a
  distribution serves it only when its manifest lists it." Priority Medium, status Planned. It is linked from UC-001, UC-004 and
  UC-010.
- **glossary.md:**
  - **Hosted Endpoint**: "A service endpoint that opens no listener; an endpoint of its host type that hosts endpoints serves
    its services on its own server." Avoid: service extension.
  - **Deprecated Evaluation Service**.
  - **Service Endpoint** gets "…or a hosted endpoint".
- **UC-004:**
  - A10 "Endpoint Served by Another Endpoint";
  - BR-013 "A Hosted Endpoint Serves on Its Host's Listener": no listener, the host opts in, the host's settings and attribution,
    the start fails without the host;
  - BR-001 names `service/transport` as the shared module official endpoints already use (R7).
- **UC-001:** A13 "Hosted Endpoint Without a Host That Hosts It"; BR-014 adds the need.
- **UC-010:**
  - A7 "Client Uses a Deprecated Evaluation Service";
  - BR-008 "Deprecated Evaluation Services Only in flagd-full" (and flagd-testbed under Q3);
  - step 1 adds "the evaluation services its clients call".
- **UC-011:** the listing shows a hosted endpoint's host.
- **Docs:** `docs/relay/distributions.md`, `load-test.md:16`, `status.md`.

## 6. Slices

| Slice | Role | Files (exclusive) | Base |
|---|---|---|---|
| **S0** core seam | mechanic | `core/pkg/service/endpoint.go`, `factory.go`, `factory_test.go` (T2) | main |
| **A** runtime and contract harness | implementer | `runtime/from_config.go`, `runtime/hosted_test.go`, `core/pkg/service/servicetest/**`, `testdata/contract-external.txt`, `core/pkg/distribution/inventory.go` and its test (T1, T3 harness, T8 unit) | S0 |
| **B** builder | implementer | `builder/internal/needs/**`, the probe template in `builder/internal/generate/`, builder fixtures (T7) | S0 |
| **D** spec and docs | implementer | section 5 files; spec lint | main |
| **C** legacy component, wired in | implementer, with a mechanic for regeneration and testdata | see the list below (T3 legacy, T4, T5, T9, T10, T11) | A (stacked) |
| **E** slim distributions and closing | implementer | `test/distributions/**` (T6, T8 end-to-end), `/spec-review` fixes | C |

Slice C's files:
- `service/flag-evaluation/**` (including `Makefile:328`'s mockgen source if the mock moves);
- the v1 consumers listed under the test split: the four runtime tests, `test/distributions/parity_test.go` and
  `test/configformat-example/**`;
- the contract files in `service/transport/`;
- `service/flag-evaluation-legacy/**`;
- `runtime/internal/officialset/officialset.go`;
- `runtime/legacy_composition_test.go`;
- `distributions/flagd-full/manifest.yaml` and `distributions/flagd-testbed/manifest.yaml` (entry plus `replaces`);
- the regenerated `flagd/` (`components/`, `go.mod`, `go.sum`) and the testbed regeneration;
- `test/concurrency/k6/load.js` and `protoset.go`;
- `test/upgrade/upgrade_test.go`;
- `testdata/module-boundaries.txt`: a new line for the module, and the module added to the forbidden sets of `runtime:. ./cmd`,
  `service/transport` and the sibling endpoints;
- `testdata/module-graph-boundaries.txt`, likewise;
- `testdata/flagd-full-modules.txt` and `testdata/go-packages.txt`;
- `Makefile` (`FLAGD_MODULE_DIRS`, `workspace-init`);
- `.github/scripts/ci-modules.json` and `test_ci_modules.py`;
- `.golangci.yml` (the new module beside the entries at `:89` and `:140`).

**Order and parallelism.** S0 lands first. A, B and D then run in parallel. C's implementer starts at the same time on S0, with
the module, the host refactor and the copies, and merges A's head before the wiring commits. The PR stacks S0 → A → C → E.

**Every PR is green on its own (B2):**
- S0, A and B change no served behaviour;
- C adds the component and selects it in flagd-full and flagd-testbed in the same PR, so schema.v1 never leaves them;
- C drops schema.v1 and v1 from the flagd-minimal load, and moves the parity test to v2, in the same PR that removes them
  there.

That makes E own `test/distributions/**` except `parity_test.go`, which C owns.

C must not merge without A below it. The stack merges bottom-up, as CLAUDE.md describes. Branches are
`phase-19/pr-<x>-<topic>`; each PR starts with `Part of #266`.

## 7. Considered (m9)

- **A switch inside flag-evaluation** (on for flagd-full through `officialEndpointSettings`, off in slim manifests). About 20
  lines, but the code stays linked, it is not "its own component" (#266), and custom manifests could not opt in through the
  manifest.
- **A build tag.** It fights the builder model of one manifest, one binary, and the components command could not show it.
- **A new manifest group `service_extensions`.** It changes the builder, the component types, the components groups and the
  contract runner for one component.

The seam costs four core names (NFR-006 holds) and a contract-only transport surface.

## 8. Gates

| Gate | Pass condition |
|---|---|
| Unit `-race`, touched packages | green per slice |
| `make lint` with depguard; the module boundaries | green; core gains no import; the legacy module imports only core and transport |
| Upgrade differential | green on C with `accepted.yaml` unchanged |
| `make flagd-integration-test` | unchanged |
| allocs/op | no regression. The v1 `BenchmarkConnect*` keep their names in the legacy module; `run-benchmarks.sh` must include that package (C), or the comparison drops them silently. |
| `load-smoke` | green on C |
| Builder regeneration check | green on C |
| Spec lint, `/spec-review` | D, E: no finding left unfixed or unfiled |
| The legacy module's CI job ran | reviewer, on C |

## 9. Risks

- **R2, metric route attribute:** per-instance routes (m6); covered by the test split and T5.
- **R3, schema.v1 streams on the host's `Events`:** gated by T11, no longer by T5 alone.
- **R4, attribution:** `Needs{}` plus the host's resolver; asserted in T10.
- **R6, public surface:** contract only (M7).
- **R7, UC-004 BR-001:** already stricter than the official endpoints behave; D amends it.
- **R10, copied helpers drift from the host's:** intended. schema.v1 is frozen, and T5 pins its bytes.
- **R5, benchmark names:** the v1 benchmarks move module, so CI's comparison must still find them (gate above).
- **R11, C's size:** the largest slice. Under Q1 (b) it moves two services and their tests, but no public API. A mechanic
  regenerates and edits the testdata. It cannot be split without a red PR, because the host stops serving v1 the moment it moves.

## 0. Questions for the project lead

- **Q1, which services move.** schema.v1, flagd.evaluation.v1 and the flagd-web provider are all deprecated (section 1). v2 has
  no `ResolveAll`.

  | Option | Slim distributions serve | A (deprecated) flagd-web user on a slim distribution |
  |---|---|---|
  | (a) schema.v1 only now; v1 later | v1, v2 | works |
  | **(b) schema.v1 and v1 in the legacy component; flagd-full keeps both; slim serves only v2** | v2 | `Unimplemented` on `ResolveAll` and `EventStream`, so the provider never becomes ready. Remedy: flagd-full, a custom manifest that lists the component, or OFREP (ofrep-web). |
  | (c) both in one component, slim keeps it until v2 has `ResolveAll` | schema.v1, v1, v2 | works, but slim keeps schema.v1, against #266 |
  | (d) two components (schema.v1; v1), slim lists v1 until v2 has `ResolveAll` | v1, v2 | works; dropping v1 later is a manifest change |
  | (e) split v1: single RPCs move, `ResolveAll` and `EventStream` stay | v1 bulk and stream, v2 | works, but one service spans two components; rejected |

  **Recommended: (b)** (project lead's input). Every deprecated service leaves the slim distributions in one step, and flagd-full
  stays a drop-in replacement. The cost is a larger slice C (R11).
- **Q2, name.** (a) `flag-evaluation-legacy`, module `service/flag-evaluation-legacy`; (b) `flag-evaluation-schema-v1`; (c)
  `flag-evaluation-deprecated`. **Recommended: (a).**
- **Q3, flagd-testbed.** (a) include it, since it stands in for flagd in provider suites; (b) leave it out. **Recommended:
  (a).**
- **Q4, the component without a host that hosts it.** (a) the builder refuses the manifest and the runtime refuses to start, both
  naming the types; (b) it is ignored with a warning. **Recommended: (a).**
- **Q5, configuration keys.** (a) none, it rides the host's settings; (b) a `services` list. **Recommended: (a).**
- **Q6, the shape of the seam.** (a) optional `Hosted` / `Hosting` interfaces under `service_endpoints`, with a contract-only
  transport surface; (b) a new manifest group; (c) a legacy module that wraps the flag evaluation factory. **Recommended: (a)**
  (section 7).
- **Q7, documenting the slim change.** (a) in `docs/relay/distributions.md` and UC-010 A7, not #78; (b) in #78 as well.
  **Recommended: (a).**
- **Q8, `flagd.sync.v1` `GetMetadata`.** (a) out of scope, since `disable-sync-metadata` already turns it off; (b) a hosted
  endpoint of flag sync. **Recommended: (a).**
