# Step 14 plan: adopt the Go flagd provider's gRPC sync features in `sync/flagd-grpc` (issue #158)

**Status: revision 2.** It resolves `step-14-opposer.md` (0 blockers, 2 major, 9 minor); every finding is adopted, and section 8 says how.
- The recommendations of Q1, Q3, Q5, Q12, Q13 and Q14 changed.
- Q15 (the dial-option override) and Q16 (keys today's flagd ignores) are new.
- Section 0 holds the questions for the project lead, numbered, each with two or three options and a recommendation.

**Decision (2026-11-04, #158): in the PoC, roadmap step 14.** Bring the Go flagd provider's gRPC sync features into `sync/flagd-grpc`, so that one gRPC sync module can serve both the Relay and, later, in-process providers (#156).

**Hard constraint (drop-in):** a `grpc` source keeps today's behaviour with today's settings.
- Every addition is an **opt-in source setting**.
- Where the provider behaves differently from flagd today, today's flagd is the default and the provider's behaviour is opt-in.
- The upgrade differential's `source-grpc` scenario stays green. It covers the happy path (start, ready, one resolve); C1–C4 pin the failure paths (NFR-016).

**Base.** `main` @ 5aa3ca2e. Step 13 reserves UC-022, FR-056, FR-057, NFR-014 and NFR-015, so this step uses **UC-023, FR-058–FR-062 and NFR-016**. "R" is `github.com/open-feature-forking/openfeature-relay`.

**Read (read-only):**
- go-sdk-contrib `providers/flagd/v0.8.0`, the version `test/integration` pins: `pkg/service/in_process/` (`grpc_sync.go`, `grpc_config.go`, `grpc_interceptors.go`, `service.go`, tests) and `internal/flagdmeta`.
- `sync/flagd-grpc`, `core/pkg/sync` (`isync.go`, `fetch.go`, `headers.go`) and `runtime/runtime.go` on `main`.
- The provider spec, `docs/reference/specifications/providers.md` at e6be905 in the flagd checkout.
- grpc-go **v1.84.0**, the version `sync/flagd-grpc` requires, for keepalive (`dialoptions.go:575-583`, `internal/transport/http2_client.go:266`).

## 1. Comparison, field by field

Legend: **have**, **missing**, **differs**, **ours only**, and **n/a** (the provider's SDK-event layer, not a sync provider).

**How flagd configures a `grpc` source today:** the `--sources` JSON, decoded into `sync.SourceConfig` without `DisallowUnknownFields` (so unknown keys are ignored), or `--uri grpc://…`, `grpcs://…`, `envoy://…`. The keys that reach `flagdgrpc.Sync` are `uri`, `provider`, `tls`, `certPath`, `providerID`, `selector`, `maxMsgSize`, `headers` (not `headersFrom`), `incrementalUpdates` and `staleAfter`.

| # | Feature | Provider v0.8.0 | Provider spec | Ours on `main` | Status |
|---|---|---|---|---|---|
| 1 | Target | `URI`, envoy resolver | `host`/`port`, `targetUri`, `socketPath` | `grpc://`, `grpcs://`, `envoy`, `dns`, `uds`, `xds`; envoy resolver | **have** |
| 2 | TLS, `certPath` | `CredentialBuilder.Build` | `tls`, `certPath` | same builder | **have** |
| 3 | `providerID` in the request | yes | `providerId` | yes | **have** |
| 4 | Selector in the request body (deprecated field) | yes | `selector` | yes | **have** |
| 5 | `flagd-selector` metadata on unary and stream calls | interceptors whenever a selector is set, in the override path too | header before body (Selector Precedence) | not sent | **missing** (Q6) |
| 6 | Max receive message size | `MaxMsgSize` | — | `maxMsgSize` | **have** |
| 7 | Custom headers | — | — | `headers` on both calls; **`headersFrom` ignored** (the factory passes `config.Headers`, where `http` and Flagsmith call `sync.ResolveHeaders`) | **ours only**, with a gap (Q10) |
| 8 | gRPC retry service config | `MaxAttempts 4`, initial `RetryBackOffMs` (default 1000), max `RetryBackOffMaxMs` (default 5000), ×2, `UNKNOWN`/`UNAVAILABLE`, on `flagd.sync.v1.FlagSyncService`; not applied under the override | the same, but `InitialBackoff` fixed at `1s` | none | **missing**; the provider and the spec differ (Q4) |
| 9 | Delay before re-opening a stream | a fixed `RetryBackOffMaxMs` after every failed cycle, none after a deadline recycle | `retryBackoffMaxMs` after an error **or completion** | 4 s, 16 s, 64 s, then 60 s; restarts each time a stream ends | **differs** (Q4, Q7) |
| 10 | `WaitForReady` on `SyncFlags` | always | — | no (fail fast) | **differs** (Q9) |
| 11 | Stream deadline | `StreamDeadlineMs`; recycle without error or delay | `streamDeadlineMs`, default 600000 | none | **missing** (Q7) |
| 12 | Unary deadline | `deadlineMs` bounds local evaluations (`applyDeadlineToContext`), not the sync | `deadlineMs`, default 500 | none; `FetchAllFlags` is reached only through `ReSync`, which only flagd-proxy and `synctest` call | **n/a for flagd today** (Q12) |
| 13 | Keepalive | `WithKeepaliveParams` always applied: `Time` = `KeepAliveTime` ms, `Timeout` 5 s, `PermitWithoutStream: true`. grpc-go raises any `Time` below 10 s to 10 s **when the dial option is built**, so the default `0` **pings every 10 s, without a stream too**; not applied under the override | `keepAliveTime`, default 0 | none: no keepalive parameters | **missing**, and the provider's `0` means "every 10 s" (Q13) |
| 14 | Fatal status codes | on a failed first cycle with `!IsReady()`: `Sync` returns `ProviderInitError{ProviderFatalCode}`; unknown names warned about and skipped; the set is a **package global** | `fatalStatusCodes`, `[]`; "during initialization" | none: every error retried; a failed first connect doesn't exit (#78) | **missing** (Q5) |
| 15 | Sync provider readiness | after the first payload | — | when the stream opens, pinned by `TestReadinessCharacterization` | **differs**; invisible at the runtime's readiness (Q8) |
| 16 | Context cancellation | `Sync` returns `ctx.Err()` | — | `Sync` returns `nil`; the runtime relies on it (an error from `Sync` exits flagd, `runtime.go:113-120`) | **have**, return value kept |
| 17 | Connection state events, `retryGracePeriod` | SDK events, stale to error timer | — | stream metrics, `LastContact`, `staleAfter` | **n/a** |
| 18 | Closing the connection | `Shutdown` closes the `ClientConn` | — | never closed | **missing**; an internal leak that matters once a process builds several runtimes (step 13 Q22). Fixed without a setting (P1) |
| 19 | `GrpcDialOptionsOverride` | yes; interceptors applied on top, service config and keepalive not | — | yes; `DialOptions` (instrumentation) applied on top | **have**; the new options under the override are Q15 |
| 20 | Instrumentation, sync and stream metrics, `LastContact`, `incrementalUpdates` | — | — | yes | **ours only**, kept |

**The runtime's rule for a `Sync` that returns an error:** every sync provider's `Sync` runs in the runtime's errgroup. An error from it ends the group, and **flagd exits** (`runtime.go:113-120`). The repository has already used that on purpose: a fetching source refused before its first successful fetch (`sync.ErrSourceRefused`, UC-019 Q13) ends the provider, and `runtime/refused_source_test.go` `TestRuntimeExitsOnRefusedSource` pins that flagd exits naming the source.

## 0. Open questions for the project lead

The recommendation comes first; each keeps today's flagd as the default.

- **Q1, slicing.**
  - (a) **Six PRs** (section 4): S (spec) and C (pins) now; P1 (settings surface, validation, `headersFrom`, connection close) on C; then P2 (the `Sync` loop: reconnect delay, `WaitForReady`, fatal codes, stream deadline) and P4 (`Init`: retry service config, keepalive, selector interceptors) in parallel on P1; T (testbed job) on P2, P4 and step 13's I; X last.
  - (b) One PR per setting. Smaller, but they all edit `grpc_sync.go`.
  - (c) A single PR.
  - **Recommendation:** (a). The critical path is C → P1 → P2 → T → X.

- **Q2, which use case owns this.**
  - (a) A new **UC-023 "Sync From an Upstream Flag Sync Endpoint"**, actor Operator, with FR-058–FR-062 and NFR-016.
  - (b) Business rules in UC-003, whose actor is the Extension Author.
  - (c) An alternative flow of UC-017 (the proxy chain), which is narrower than the settings.
  - **Recommendation:** (a).

- **Q3, the settings surface.**
  - (a) **Flat keys in `sync.SourceConfig`,** named as the provider spec names them: `retryBackoffMs`, `retryBackoffMaxMs`, `streamDeadlineMs`, `keepAliveTime` (ms) and `fatalStatusCodes`, plus `selectorHeader` (bool).
    - Absent or `0` means "not set", for every key.
    - A gRPC-only key on a source of another provider stops startup, naming the source and the key.
    - The fields are `omitempty`, so `TestSourceConfigJSONRoundTripWithoutHeadersFrom` and `start_config.golden` stay unchanged.
  - (b) The same keys nested under one `grpc` object on the source. Nothing can leak onto other providers, but the names no longer match the provider documentation.
  - (c) One `profile: "provider"` switch that turns on the provider's defaults together.
  - **Recommendation:** (a). The names match the docs operators copy from, and the rejection keeps them from being silently ignored elsewhere.

- **Q4, reconnect timing once opted in** (`retryBackoffMs` or `retryBackoffMaxMs` set).
  - (a) **The provider's:**
    - the retry service config with initial `retryBackoffMs`, max `retryBackoffMaxMs`, ×2, 4 attempts, `UNKNOWN`/`UNAVAILABLE`;
    - a fixed `retryBackoffMaxMs` delay before re-opening after an error, in place of 4/16/64/60 s.

    If only one key is set, the provider's default fills the other (1000 or 5000 ms). `retryBackoffMs` above `retryBackoffMaxMs` stops startup.
  - (b) The spec's literal policy: as (a), with `InitialBackoff` fixed at 1 s.
  - (c) Today's 4^n schedule, made configurable, with no service config.
  - **Recommendation:** (a). It is what the released Go providers run.

- **Q5, what a fatal status code does** (`fatalStatusCodes` set, and a listed code arrives before the source's first payload). *(Recommendation changed in revision 2.)*
  - (a) **flagd exits,** naming the source and the code, as for a fetching source refused before its first fetch (`ErrSourceRefused`): `Sync` returns the error and the runtime ends. One rule for both kinds of source, and an orchestrator restarts or alerts as for any failed start.
  - (b) **The source stops and flagd keeps serving its other sources,** never becoming ready. That is not the provider's code: `Sync` would have to stop retrying and block until `ctx` is done, returning `nil`, because an error from `Sync` exits flagd.
  - (c) The source stops on a listed code at any time, before or after the first payload. This goes beyond the spec.
  - **Recommendation:** (a).
    - "Before the first payload" is tracked in its own flag, not in `ready`, which is set when the stream opens (Q8): a `PERMISSION_DENIED` on the first `Recv` comes with `ready` already true.
    - After the first payload, a listed code is retried like any error.
    - The set of codes is held per `Sync`, not in a package global as in the provider.
    - An unknown code name stops startup.

- **Q6, the `flagd-selector` header.**
  - (a) **Opt-in `selectorHeader: true`:** the header is sent in addition to the body field, on `SyncFlags` and `FetchAllFlags`. A `headers` entry named `flagd-selector` on the same source stops startup, because `AppendToOutgoingContext` would send two values and the first wins.
  - (b) Always, whenever a selector is set, as the provider does. That changes what every upstream sees, so it breaks the drop-in rule.
  - (c) Opt-in, header only, with no body field. An old upstream would then get no selector.
  - **Recommendation:** (a). It is the subscriber's side of #149. Our flag sync endpoint already prefers the header and gets the same value; flagd-proxy reads only the body.

- **Q7, stream deadline semantics** (`streamDeadlineMs` set).
  - (a) **The provider's:** when the deadline elapses with the parent context alive, re-open at once, with no error, no delay and no failure in the stream metrics.
  - (b) The spec's: a completed stream waits `retryBackoffMaxMs` before re-opening.
  - (c) (a), but counted as a reconnect.
  - **Recommendation:** (a), with a debug log line.

- **Q8, sync provider readiness.**
  - (a) **Keep today's** (ready when the stream opens), with no setting. The runtime's readiness already waits for an accepted configuration from each source.
  - (b) An opt-in `readyOnFirstPayload`.
  - (c) The provider's by default. That breaks a pin for no visible gain.
  - **Recommendation:** (a).

- **Q9, `WaitForReady` on `SyncFlags`.**
  - (a) **On exactly when Q4's retry policy is opted in.**
  - (b) A key of its own.
  - (c) Never.
  - **Recommendation:** (a). Without the service config it would block where flagd now fails fast and backs off.

- **Q10, `headersFrom` on `grpc` sources.**
  - (a) **Fix it in P1:** the factory calls `sync.ResolveHeaders`, as `http` does. Upstream flagd never had the key, so nothing a configuration means today changes.
  - (b) A separate issue.
  - (c) Document `headersFrom` as http-only.
  - **Recommendation:** (a).

- **Q11, provider consumption later (#156).**
  - (a) **One exported `flagdgrpc.Options` struct** that the factory fills, and that a later provider constructor can fill too. No provider-facing API in this step.
  - (b) A provider constructor and an event callback now.
  - (c) Nothing.
  - **Recommendation:** (a).

- **Q12, a unary deadline for `FetchAllFlags`.** *(Recommendation changed in revision 2.)*
  - (a) **Leave it out** until something in flagd calls `FetchAllFlags`. Today only flagd-proxy and `synctest` call `ReSync`. Step 13's testbed has no `grpc` source, and the provider's `deadlineMs` bounds local evaluations, which have no relay counterpart.
  - (b) An opt-in `deadlineMs` now, with nothing behind it in flagd.
  - (c) The spec's 500 ms default. That changes today's behaviour.
  - **Recommendation:** (a).

- **Q13, keepalive** (`keepAliveTime` in ms). *(Recommendation changed in revision 2.)*
  - (a) **Absent and `0` mean no keepalive parameters, as today.** That deliberately differs from the provider, where `0` means a 10 s ping.
    - A value from 1 to 9999 stops startup, naming the setting and grpc-go's 10 s floor, instead of being raised silently.
    - Any value of 10000 or more sets `Time`, with `Timeout` 5 s and `PermitWithoutStream: true`, as the provider does.
    - The settings table says that a grpc-go upstream with default enforcement (5-minute `MinTime`, no pings without streams; flagd-proxy is one) answers faster pings with `GOAWAY ENHANCE_YOUR_CALM "too_many_pings"`.
  - (b) As (a), and `0` means the provider's 10 s ping. That matches the name's meaning in the provider, but turns "not set" into a behaviour.
  - (c) Leave keepalive out.
  - **Recommendation:** (a).

- **Q14, end-to-end tests.** *(Scope narrowed in revision 2.)*
  - (a) **Unit tests always, plus a gated testbed job** (`FULL_CI` or a manual dispatch).
    - The job runs flagd-full with a `grpc` source against step 13's testbed. Its `/restart` closes the listeners for real reconnects, and envoy's 9212 answers `PERMISSION_DENIED` for fatal codes.
    - The selector header and keepalive stay unit-tested only: the testbed's endpoint runs on `net/http`, which neither enforces a ping policy nor echoes headers.
  - (b) Unit tests only.
  - (c) The testbed job on every PR.
  - **Recommendation:** (a).

- **Q15, the new options under `GrpcDialOptionsOverride`.** *(New in revision 2.)*
  - (a) **The provider's rule:** the selector interceptors apply on top of an override, and the retry service config and keepalive don't, because the override owns the dial. `DialOptions` (instrumentation) keep applying on both paths, as today.
  - (b) Every new option applies on top of the override.
  - (c) None applies under an override.
  - **Recommendation:** (a), pinned by `TestOverrideKeepsInterceptorsOnly`.

- **Q16, configurations that already carry these keys.** *(New in revision 2.)* Today flagd ignores unknown keys in `--sources`. Operators copy from the provider documentation, so a configuration may already carry `keepAliveTime` or `retryBackoffMaxMs`, which would start to act after step 14.
  - (a) **Accept it.** List it in UC-023 A4 and as a #78 item, with its `accepted.yaml` entry (`covered_by` the new tests).
  - (b) Put all new keys behind one switch, `providerSettings: true`, so a key that is already present keeps being ignored until the operator opts in. This also brings back a profile-like switch on top of Q3 (a).
  - (c) Log a warning for each such key on today's builds first, and switch it on in a later release.
  - **Recommendation:** (a). The keys are spelled as the provider spells them, so an operator who wrote them meant them.

## 2. Spec change (Q2 (a); for PR S)

**UC-023: Sync From an Upstream Flag Sync Endpoint.**
- **Primary actor:** Operator. **Secondary actor:** the upstream flag sync endpoint (flagd, flagd-sync, a vendor relay).
- **Goal:** configure a `grpc` source to survive the upstream's outages and misconfigurations as the flagd providers do, with nothing changing for sources that don't opt in.

**Main success scenario:**
1. The Operator sets any of Q3's keys on a `grpc` source.
2. flagd validates them.
3. flagd syncs, re-opening streams with Q4's timing.
4. flagd recycles streams at the deadline (Q7).
5. flagd keeps the connection alive (Q13).
6. flagd sends the selector header (Q6).
7. Readiness reflects the source as today (Q8).

**Alternative flows:**
- **A1 Fatal code before the first payload:** flagd exits, naming the source and the code (Q5).
- **A2 Invalid setting:** startup stops and names the setting and the source, for:
  - an unknown code name;
  - a negative duration;
  - `retryBackoffMs` above `retryBackoffMaxMs`;
  - `keepAliveTime` from 1 to 9999;
  - `selectorHeader` together with a `flagd-selector` entry in `headers`;
  - a gRPC-only key on another provider's source.
- **A3 Upstream outage:** Q4's timing.
- **A4 No opt-in setting:** today's behaviour. The one exception is a configuration that already carried a key spelled as the provider spells it, which now acts (Q16).

**Requirements:**

| ID | Requirement |
|---|---|
| FR-058 | A `grpc` source can opt into the provider's retry policy and reconnect delay (`retryBackoffMs`, `retryBackoffMaxMs`), with `WaitForReady` |
| FR-059 | A `grpc` source can opt into fatal status codes that end flagd before the source's first payload (`fatalStatusCodes`) |
| FR-060 | A `grpc` source can opt into a stream deadline (`streamDeadlineMs`) and HTTP/2 keepalive (`keepAliveTime`, at least 10 s) |
| FR-061 | A `grpc` source can opt into sending its selector as the `flagd-selector` header (`selectorHeader`) |
| FR-062 | A `grpc` source honours `headersFrom` like every other source |
| NFR-016 | Without any of these settings, a `grpc` source behaves as today. The happy path is proven by the upgrade differential's `source-grpc` scenario; the failure paths (schedule, fail-fast, retries, no keepalive, no service config, no header) by C1–C4 |

UC-003 gets one line under "Delivered by later use cases", and `docs/relay/` gets the source settings table with the differences from the provider: `keepAliveTime` 0, the override rule, and fatal codes ending flagd.

## 3. Tests

**Characterization first (PR C, before any change):**

| Pin | Test | Pins |
|---|---|---|
| C1 | `sync/flagd-grpc` `TestDefaultRequestMetadataCharacterization` | Against a loopback flag sync server, with today's settings: the metadata of `SyncFlags` and `FetchAllFlags` has no `flagd-selector` key; `headers` arrive as set |
| C2 | `TestDefaultReconnectScheduleCharacterization` | With a fake clock through a new, behaviour-neutral `sleep` seam: the delays after repeated failures are 4 s, 16 s, 64 s, 60 s, 60 s; the first failure of `Sync` goes straight to the schedule; the schedule restarts after a stream ends |
| C3 | `TestDefaultDialOptionsCharacterization` | Through a behaviour-neutral recording seam over the dial options, with today's settings: no `WithKeepaliveParams`, no `WithDefaultServiceConfig`, no interceptors. `FetchAllFlags`'s context has no deadline. Keepalive is pinned at the dial, not by waiting for pings, because grpc-go's ping timers are real-time |
| C4 | `TestDefaultFailFastCharacterization` | Without `WaitForReady`, `SyncFlags` against a closed port fails at once and enters the schedule; `PERMISSION_DENIED` on the first call or the first `Recv` is retried like any error, and flagd keeps running |
| — | already there | `TestReadinessCharacterization`, `Test_ConnectWithRetry`, `Test_SyncRetry`, the stream metric tests, the upgrade differential's `source-grpc` |

**Per feature:**
- **P1:**
  - `TestSettingsValidation`: a table over every A2 case, each error naming the source and the setting;
  - `TestGRPCKeysRejectedOnOtherProviders`;
  - `TestHeadersFromResolved`;
  - `TestConnectionClosedWhenSyncReturns`, with `goleak` and an fd count;
  - `TestSourceConfigJSONRoundTripWithoutHeadersFrom` and `start_config.golden` unchanged.
- **P2:**
  - `TestRetryDelayFixedOnceOptedIn` and `TestSingleBackoffKeyFillsTheOther`;
  - `TestWaitForReadyOnlyWithRetryPolicy`;
  - `TestFatalCodeOnFirstRecvStopsSync` (the stream opens, then `PERMISSION_DENIED`) and `TestFatalCodeAfterFirstPayloadIsRetried`;
  - `TestFatalCodesPerSync` (two sources, different sets);
  - `runtime` `TestRuntimeExitsOnFatalGRPCCode`, beside `TestRuntimeExitsOnRefusedSource`;
  - `TestStreamDeadlineRecyclesWithoutDelay`.
- **P4:**
  - `TestRetryServiceConfigJSON`: equal to the provider's `buildRetryPolicy` output for the same values, fractional seconds included;
  - `TestKeepaliveParameters`: the recorded `ClientParameters` for 10000 ms and more;
  - `TestSelectorHeaderOnBothCalls`;
  - `TestOverrideKeepsInterceptorsOnly` (Q15).

**End to end (T, gated, Q14):** flagd-full with a `grpc` source against step 13's testbed.
- **Through 8015:** `/restart` gives real reconnects, timed against Q4.
- **Through envoy's 9212:** `PERMISSION_DENIED` gives the fatal exit (Q5).
- **A stream deadline:** the stream recycles.
- **Not covered here:** the selector header and keepalive enforcement (Q14).

## 4. PR slicing

| PR | Model | Base | Content | Starts |
|---|---|---|---|---|
| S | mechanic | `main` (spec, straight to `main`) | UC-023, FR-058–FR-062, NFR-016, the UC-003 line, the `docs/relay/` settings table | now |
| C | implementer | `main` | C1–C4. The only code changes are the `sleep` and dial-option recording seams, behaviour-neutral | now |
| P1 | implementer | C | Q3's keys (`omitempty`) and A2's validation; gRPC-only keys rejected elsewhere; `flagdgrpc.Options` (Q11); `headersFrom` (Q10); the `ClientConn` closed when `Sync` returns | after C |
| P2 | implementer | P1 | The `Sync` loop: Q4's delay, Q9's `WaitForReady`, Q5's fatal codes with a first-payload flag and a per-`Sync` set, Q7's stream deadline | after P1 |
| P4 | implementer | P1 | `Init`: Q4's service config, Q13's keepalive, Q6's interceptors, Q15's override rule | after P1, beside P2 |
| T | implementer | P2 + P4, and step 13's I merged | The gated testbed job (Q14) | after both |
| X | mechanic | T | UC-023 status, requirement statuses, the #78 item of Q16 with its `accepted.yaml` entry, #158 | last |

The critical path is C → P1 → P2 → T → X. P2 and P4 edit different functions of `grpc_sync.go`, so their merge is mechanical.

## 5. Gates

In WSL from the repository root (`export GOTOOLCHAIN=go1.27.3`; in a worktree `export GOWORK=$PWD/go.work`):

| Gate | Command | PRs |
|---|---|---|
| Unit tests | `make workspace-init && make test` | all |
| Lint, boundaries | `make lint check-module-boundary check-package-coverage` | all |
| Drop-in, happy path | the upgrade differential (`source-grpc`), `make flagd-integration-test` | P1, P2, P4 |
| Drop-in, failure paths | C1–C4 unchanged | P1, P2, P4 |
| Benchmarks | CI's allocs/op comparison | P1, P2, P4 |
| #78 | `TestAcceptedMatchesIssue` | X |
| Testbed | the gated job on a manual dispatch | T |
| Spec | `spec_lint.py --docs docs` on Windows | S, X |

## 6. Risks

- **R1, retry layers multiply.** The service config retries an RPC up to 4 times inside one attempt, and the application-level delay then waits `retryBackoffMaxMs`. That is the provider's design (Q4). The settings table states timings as "at least", and `TestRetryServiceConfigJSON` keeps the policy identical to the provider's.
- **R2, keepalive against a strict upstream.**
  - grpc-go v1.84.0 raises a `Time` below 10 s to 10 s when the dial option is built. The provider's default therefore pings every 10 s, with `PermitWithoutStream`.
  - A grpc-go server with default enforcement, such as flagd-proxy, answers faster pings with `GOAWAY ENHANCE_YOUR_CALM "too_many_pings"`.
  - Q13 (a) keeps "off" as the default, rejects 1–9999, and the settings table states the GOAWAY.
- **R3, fatal codes end flagd (Q5).** That is the same rule as a refused fetching source, and it is opt-in. One misconfigured source then stops the others too, which the settings table says.
- **R4, the provider spec and the Go provider differ** on the initial backoff (Q4) and the delay after a deadline (Q7). We follow the provider code. Nothing goes upstream; the docs name the differences.
- **R5, drift from the provider (#156).** We port behaviour, not the package. The service-config builder is matched by a test, and the provider's package-global fatal set is not ported.
- **R6, configurations that already carry the keys (Q16).** Recorded in #78 and A4.
- **R7, step 13 dependency.** Only T waits for step 13's testbed.
- **R8, `headersFrom` reads secrets.** It uses the same resolver as `http`: values never logged, unreadable sources stop startup.

## 7. Considered

- **Step 13's testbed** is the end-to-end harness for reconnects and fatal codes (Q14).
- **#149:** the selector header is the subscriber's side (Q6).
- **#156:** `flagdgrpc.Options` is where a provider would later configure the module (Q11).

## 8. Changelog (revision 2, `step-14-opposer.md`)

| Finding | Severity | Outcome |
|---|---|---|
| 1 A returning `Sync` exits flagd; the precedent is exit | major | Adopted. Verified `runtime.go:113-120` and `TestRuntimeExitsOnRefusedSource`. Q5 now recommends exit; the first payload is tracked separately; runtime-level test added; the alternative names what it would take |
| 2 The provider's `keepAliveTime: 0` pings every 10 s | major | Adopted as fact (R2). Q13: 0 means off, 1–9999 rejected; `TestKeepaliveParameters` uses the recording seam |
| 3 Q12 has no caller in flagd | minor | Adopted. Verified the `ReSync` callers (flagd-proxy, `synctest`). Q12 recommends leaving it out; FR-060 drops `deadlineMs`; P3 is gone |
| 4 C3 can't use a fake clock | minor | Adopted. C3 pins the absence at the dial through a recording seam |
| 5 Today flagd ignores these keys | minor | Adopted as Q16, with A4 and a #78 item in X |
| 6 Where the keys live | minor | Adopted. Q3 (a) rejects gRPC-only keys on other providers; `omitempty` keeps the round-trip pin and `start_config.golden` |
| 7 Header conflict | minor | Adopted. Q6 (a) rejects `selectorHeader` with a `flagd-selector` entry in `headers` (A2) |
| 8 A2 tests one case | minor | Adopted. `TestSettingsValidation` covers every case; Q4 states the single-key rule |
| 9 What T and the differential prove | minor | Adopted. NFR-016, section 5 and Q14 say it |
| 10 The override path; the global fatal set | minor | Adopted. Q15; the fatal set per `Sync` (Q5, `TestFatalCodesPerSync`) |
| 11 Fold P3 into P2 | minor | Adopted. Six PRs |
