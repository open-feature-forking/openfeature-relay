# Step 14 plan: adopt the Go flagd provider's gRPC sync features in `sync/flagd-grpc` (issue #158)

**Status: revision 1, not opposed yet.** Section 0 holds the questions for the project lead, numbered, each with options and a recommendation.

**Decision (2026-11-04, #158): in the PoC, roadmap step 14.** Bring the Go flagd provider's gRPC sync features into `sync/flagd-grpc`, so that one gRPC sync module can serve both the Relay and, later, in-process providers (#156).

**Hard constraint (drop-in):** a `grpc` source keeps today's behaviour with today's settings.
- Every addition is an **opt-in source setting**.
- Where the provider behaves differently from flagd today (retry timing, error reporting, the selector header), today's flagd is the default and the provider's behaviour is opt-in.
- The upgrade differential's `source-grpc` scenario (`test/upgrade/surfaces.yaml`) stays green.

**Base.** `main` @ dd44d8fc. Step 13 (#110) is planned. Its spec PR reserves UC-022, FR-056, FR-057, NFR-014 and NFR-015, so this step uses **UC-023, FR-058–FR-063 and NFR-016**. "R" is `github.com/open-feature-forking/openfeature-relay`.

**Read (read-only):**
- go-sdk-contrib `providers/flagd/pkg/service/in_process/` at `providers/flagd/v0.8.0`, the version `test/integration` pins: `grpc_sync.go`, `grpc_config.go`, `grpc_interceptors.go`, `service.go` and their tests.
- `sync/flagd-grpc` on `main` (`grpc_sync.go`, `factory.go`, tests), and `core/pkg/sync.SourceConfig`.
- The provider spec, `docs/reference/specifications/providers.md` in the flagd checkout (e6be905): "Stream Reconnection", "gRPC Retry Policy", "Fatal Status Codes", and the configuration table.

## 1. Comparison, field by field

Legend: **have**: ours already does it; **missing**: the provider or spec has it and we don't; **differs**: both have it, with different behaviour; **ours only**; **n/a**: belongs to the provider's SDK-event layer, not to a sync provider.

**How flagd configures a `grpc` source today** (`--sources` JSON, `sync.SourceConfig`; or `--uri grpc://…`, `grpcs://…`, `envoy://…`): `uri`, `provider: "grpc"`, `tls`, `certPath`, `providerID`, `selector`, `maxMsgSize`, `headers`, `headersFrom`, `incrementalUpdates` and `staleAfter`. Nothing else reaches `flagdgrpc.Sync`.

| # | Feature | Provider v0.8.0 | Provider spec | Ours on `main` | Status |
|---|---|---|---|---|---|
| 1 | Target | `URI`, custom resolvers (envoy) | `host`/`port`, `targetUri`, `socketPath` | `grpc://`, `grpcs://`, `envoy`, `dns`, `uds` and `xds` schemes; envoy resolver | **have** |
| 2 | TLS, `certPath` | `CredentialBuilder.Build(Secure, CertPath)` | `tls`, `certPath` | same builder (`tls`, `certPath`) | **have** |
| 3 | `providerID` in the request | yes | `providerId` | yes | **have** |
| 4 | Selector in the request body (deprecated field) | yes | `selector` | yes | **have** |
| 5 | Selector as `flagd-selector` metadata, on unary and stream calls | interceptors, whenever a selector is set | header has precedence over the body | not sent (only `headers` could carry it) | **missing** (Q6) |
| 6 | Max receive message size | `MaxMsgSize` | — | `maxMsgSize` | **have** |
| 7 | Custom headers | — | — | `headers` sent on `SyncFlags` and `FetchAllFlags`; **`headersFrom` ignored** (the factory passes `config.Headers`; `http` and Flagsmith call `sync.ResolveHeaders`) | **ours only**, with a gap (Q10) |
| 8 | gRPC retry policy (service config) | `MaxAttempts 4`, initial `RetryBackOffMs` (default 1 s), max `RetryBackOffMaxMs` (default 5 s), ×2, `UNKNOWN`/`UNAVAILABLE`, on `flagd.sync.v1.FlagSyncService` | the same, but `InitialBackoff` fixed at `1s` | none | **missing**, and provider and spec differ on the initial backoff (Q4) |
| 9 | Application-level delay before re-opening a stream | a fixed `RetryBackOffMaxMs` after every failed cycle | "must apply … `retryBackoffMaxMs`" after an error **or completion** | `connectWithRetry`: 4 s, 16 s, 64 s, then 60 s constant; the first failure of `Sync` goes straight to it; the schedule restarts each time a stream ends | **differs** (Q4) |
| 10 | `WaitForReady` on `SyncFlags` | always | — | no: a call fails at once when the connection is not ready | **differs** (Q9) |
| 11 | Stream deadline | `StreamDeadlineMs`; reaching it recycles the stream with no error and no delay | `streamDeadlineMs`, default 600000; delay after "completion" too | none: a stream lives until it breaks | **missing**, provider and spec differ (Q7) |
| 12 | Unary deadline for `FetchAllFlags` (`ReSync`) | from the caller's context | `deadlineMs`, default 500 | none | **missing** (Q12) |
| 13 | Keepalive | `Time` = `KeepAliveTime` ms (default 0), `Timeout` 5 s, `PermitWithoutStream: true`, always applied | `keepAliveTime`, default 0 | none | **missing** (Q13). See R2: grpc-go raises a `Time` below 10 s to 10 s, so the provider's "0" may ping every 10 s; the implementer verifies this against grpc-go before Q13's defaults are fixed |
| 14 | Fatal status codes | `FatalStatusCodes`; a match on a failed **first** cycle stops the sync with `ProviderFatal`; unknown names are warned about and ignored | `fatalStatusCodes`, default `[]`; "during initialization" only | none: every error is retried forever; a failed first connect no longer exits (#78) | **missing** (Q5) |
| 15 | Readiness of the sync provider | ready after the **first payload** | — | ready when the stream **opens** (`handleFlagSync` stores `ready` before `Recv`; pinned by `TestReadinessCharacterization`) | **differs**, and invisible at the runtime's readiness, which waits for an accepted configuration per source (Q8) |
| 16 | Context cancellation | `Recv` in a goroutine, `select` on ctx; `Sync` returns `ctx.Err()` | — | `Recv` on a stream bound to ctx, and the send to `dataSync` selects on ctx; `Sync` returns `nil` | **have** in effect; the return value differs and the runtime relies on ours. Kept |
| 17 | Connection state monitor (`TransientFailure` raises an SDK error event) | yes | STALE then ERROR after `retryGracePeriod` | stream metrics (`RecordStreamOpened`/`Closed`/`Reconnect`), `LastContact`, `staleAfter` readiness | **n/a**: SDK events. The relay's analog is `staleAfter` (FR-052) and the stream metrics |
| 18 | `retryGracePeriod` | in `service.go` (stale to error timer) | default 10 s | — | **n/a**: provider state machine |
| 19 | Closing the connection | `Shutdown` closes the `ClientConn` | — | **never closed**: `Init` creates it and nothing closes it | **missing**, an internal leak. It matters once a process builds several runtimes (step 13's configuration switch). Fixed without a setting (P1) |
| 20 | `GrpcDialOptionsOverride` | yes | — | yes | **have** |
| 21 | Client instrumentation, sync metrics, stream metrics, `LastContact`, `incrementalUpdates` | — | — | yes | **ours only**, kept |

**Missing or different, by drop-in risk:**
- **Invisible to flagd-full**, done without asking: the `ClientConn` close (19).
- **New outbound behaviour,** so opt-in: the retry policy (8, 9), `WaitForReady` (10), the stream deadline (11), the unary deadline (12), keepalive (13), fatal codes (14) and the selector header (5).
- **Default kept:** readiness (15), and the return value on cancel (16).
- **Ours with a gap:** `headersFrom` (7).

## 0. Open questions for the project lead

The recommendation comes first; each keeps today's flagd as the default.

- **Q1, slicing and parallelism.**
  - (a) Seven PRs (section 4): C (pins), S (spec), P1 (settings surface, `headersFrom`, connection close), then P2 (reconnect: delay, `WaitForReady`, fatal codes) and P4 (dial options: retry service config, keepalive, selector header) in parallel on P1. Then P3 (deadlines) on P2, T (testbed integration) on P2, P3, P4 and step 13's I, and X (close).
  - (b) One PR per setting: smaller reviews, but each one edits `grpc_sync.go`, so they would conflict.
  - (c) A single PR.
  - **Recommendation:** (a).

- **Q2, which use case owns this.**
  - (a) A new **UC-023 "Sync From an Upstream Flag Sync Endpoint"**. The Operator configures a `grpc` source's resilience settings to chain flagd, flagd-sync or a vendor relay. It brings FR-058–FR-063 and NFR-016 (section 2).
  - (b) Business rules in **UC-003** (Develop Sync Provider Extension). UC-003's actor is the Extension Author, and these are operator settings of an official provider.
  - (c) An alternative flow of **UC-017** (Run Configuration Proxy), the proxy chain. Narrower than the settings, which also apply to a plain flagd syncing from another flagd.
  - **Recommendation:** (a).

- **Q3, the settings surface.**
  - (a) **One opt-in key per feature,** named as the provider spec names them: `retryBackoffMs`, `retryBackoffMaxMs`, `streamDeadlineMs`, `deadlineMs`, `keepAliveTime` (ms), `fatalStatusCodes`, plus `selectorHeader` (bool), which the spec has no name for. Absent or zero keeps today's behaviour.
  - (b) **One switch,** `profile: "provider"`, which turns on the provider's defaults together (1 s and 5 s retry, `WaitForReady`, 600000 ms stream deadline, 500 ms unary deadline, keepalive, selector header).
  - (c) Both: the profile sets the defaults, and the individual keys override them.
  - **Recommendation:** (a). An operator turns on exactly what they need, and the names match the provider documentation operators already know. (b) and (c) can be added later without breaking (a).

- **Q4, reconnect timing once opted in** (`retryBackoffMs` or `retryBackoffMaxMs` set).
  - (a) **The provider's:**
    - the gRPC retry service config on `flagd.sync.v1.FlagSyncService`, initial `retryBackoffMs` (default 1 s), max `retryBackoffMaxMs` (default 5 s), ×2, 4 attempts, `UNKNOWN`/`UNAVAILABLE`;
    - a fixed `retryBackoffMaxMs` delay before re-opening a stream after an error, in place of 4/16/64/60 s.
  - (b) **The spec's literal policy:** as (a), but `InitialBackoff` fixed at 1 s and `retryBackoffMs` used only for the application-level delay.
  - (c) **Today's 4^n schedule, made configurable** (base and cap), with no service config.
  - **Recommendation:** (a). It is what every released Go provider runs, so a relay in a provider's place behaves like one. The spec difference goes to the spec maintainers as a note, outside this repository's scope.

- **Q5, what a fatal status code does in the relay** (`fatalStatusCodes` set, and a listed code arrives before the source's first payload).
  - (a) **The source stops.** It logs an error naming the source and the code, and stops retrying. flagd keeps running and serving its other sources, and stays **not ready**, because that source never delivers (FR-052's readiness).
  - (b) **flagd exits** with an error naming the source and the code: a fail-fast startup error like UC-010 BR-001's.
  - (c) **The source stops whenever a listed code arrives,** before or after the first payload. This goes beyond the spec, which limits fatal codes to initialization.
  - **Recommendation:** (a). It matches the spec's "during initialization", keeps the other sources served, and readiness already makes the failure visible to Kubernetes and load balancers. An unknown code name in the setting stops startup with the name (an invalid component configuration), unlike the provider, which warns and ignores it.

- **Q6, the `flagd-selector` header.** Today flagd sends the selector only in the deprecated request field. The provider also sends it as metadata whenever a selector is set.
  - (a) **Opt-in `selectorHeader: true`:** the header is sent in addition to the body field, on `SyncFlags` and `FetchAllFlags`.
  - (b) **Always,** whenever `selector` is set, as the provider does. That changes what every upstream server sees, so it breaks the drop-in rule.
  - (c) **Opt-in, and it replaces the body field:** the header only. An old upstream that ignores headers would then get no selector at all.
  - **Recommendation:** (a).
    - It is the subscriber side of #149: a later on-demand-selector server reads the header first, as the spec's precedence says.
    - flagd-proxy reads only the body, and gets the same value either way.

- **Q7, stream deadline semantics** (`streamDeadlineMs` set).
  - (a) **The provider's:** when the deadline elapses with the parent context alive, the stream is re-opened at once, with no error, no backoff and no stream metric counted as a failure.
  - (b) **The spec's:** a completed stream is treated like an error, with the `retryBackoffMaxMs` delay before re-opening.
  - (c) **(a), counted:** recycle at once, but record it as a reconnect in the stream metrics.
  - **Recommendation:** (a), with a distinct log line at debug level. The deadline is an application-level keepalive, not a failure.

- **Q8, sync provider readiness.** Ours is ready when the stream opens; the provider's after the first payload.
  - (a) **Keep today's,** no setting. The runtime's readiness already waits for an accepted configuration from each source, so the difference is not observable outside the module, and `TestReadinessCharacterization` stays as it is.
  - (b) An opt-in `readyOnFirstPayload`.
  - (c) Switch to the provider's by default. It breaks a pin, for no visible gain.
  - **Recommendation:** (a).

- **Q9, `WaitForReady` on `SyncFlags`.**
  - (a) **On when Q4's retry policy is opted in, off otherwise.** The provider couples them, and `WaitForReady` without the service config would block where flagd now fails fast and backs off.
  - (b) A separate opt-in key.
  - (c) Never.
  - **Recommendation:** (a).

- **Q10, `headersFrom` on `grpc` sources.** It is documented for every source (`sync.SourceConfig`), but `flagd-grpc` ignores it. Upstream flagd never had the key, so honouring it changes nothing for a configuration today's flagd accepts.
  - (a) **Fix in this step (P1):** the factory calls `sync.ResolveHeaders`, as `http` does. An unreadable variable or file stops startup naming the header.
  - (b) A separate issue, outside step 14.
  - (c) Document `headersFrom` as http-only.
  - **Recommendation:** (a).

- **Q11, provider consumption later (#156).**
  - (a) **Keep the runtime-facing `Sync` and factory for now.** Make the settings one exported struct (`flagdgrpc.Options`) that both the factory and a future provider constructor fill. No provider-facing API, events or `retryGracePeriod` in this step.
  - (b) Add a provider-facing constructor and an event callback now, so a provider could switch at once.
  - (c) Nothing; #156 designs its own.
  - **Recommendation:** (a). It keeps one place for the settings without designing #156's API early.

- **Q12, a unary deadline for `FetchAllFlags`** (`deadlineMs`).
  - (a) **Include it,** opt-in. Absent means no deadline, as today. It bounds `ReSync`, which step 13's store reset calls on every source.
  - (b) Leave it out.
  - (c) Include it with the spec's default of 500 ms. That changes today's behaviour.
  - **Recommendation:** (a).

- **Q13, keepalive** (`keepAliveTime` set, in ms).
  - (a) **The provider's parameters:** `Time` = `keepAliveTime`, `Timeout` 5 s, `PermitWithoutStream: true`. The settings document grpc-go's 10 s floor and the upstream server's ping policy (R2).
  - (b) As (a), plus `keepAliveTimeout` and `keepAlivePermitWithoutStream` as their own keys.
  - (c) Leave keepalive out.
  - **Recommendation:** (a). Absent means no keepalive parameters at all, as today. The provider applies them always, even at 0; we don't.

- **Q14, how these features are tested end to end.**
  - (a) **Unit tests always, plus a gated testbed job.**
    - **Unit tests** run on every PR: a `bufconn` or loopback flag sync server with fault injection in the test, and a fake clock for timings.
    - **The gated job** (`FULL_CI` or a manual dispatch) runs flagd-full with a `grpc` source pointed at step 13's testbed. The testbed's `/restart` closes its listeners for real reconnects, and envoy's 9212 answers `PERMISSION_DENIED` for fatal codes. It covers reconnect timing, fatal codes, the selector header and the stream deadline.
  - (b) Unit tests only.
  - (c) The testbed job on every PR.
  - **Recommendation:** (a). T depends on step 13's B1 and I, so it is last.

## 2. Spec change (Q2 (a); for PR S)

**UC-023: Sync From an Upstream Flag Sync Endpoint.**
- **Primary actor:** Operator. **Secondary actor:** the upstream flag sync endpoint (flagd, flagd-sync, or a vendor relay).
- **Goal:** configure a `grpc` source to survive the upstream's outages and misconfigurations as the flagd providers do, without changing anything for sources that don't opt in.

**Main success scenario:**
1. The Operator configures a `grpc` source with any of the opt-in settings.
2. flagd validates them.
3. flagd syncs, re-opening streams with the configured timing.
4. flagd recycles streams at the configured deadline.
5. flagd keeps the connection alive with the configured keepalive.
6. flagd sends the selector header when configured.
7. flagd's readiness reflects the source as today.

**Alternative flows:**
- **A1 Fatal code at initialization:** Q5.
- **A2 Invalid setting** (unknown code name, negative duration): startup stops and names the setting.
- **A3 Upstream outage:** the reconnect timing of Q4.
- **A4 No opt-in setting:** today's behaviour, byte for byte (NFR-016).

**Requirements:**

| ID | Requirement |
|---|---|
| FR-058 | A `grpc` source can opt into the provider's retry policy and reconnect delay (`retryBackoffMs`, `retryBackoffMaxMs`) |
| FR-059 | A `grpc` source can opt into fatal status codes that stop it before its first payload (`fatalStatusCodes`) |
| FR-060 | A `grpc` source can opt into stream and unary deadlines (`streamDeadlineMs`, `deadlineMs`) |
| FR-061 | A `grpc` source can opt into HTTP/2 keepalive (`keepAliveTime`) |
| FR-062 | A `grpc` source can opt into sending its selector as the `flagd-selector` header (`selectorHeader`) |
| FR-063 | A `grpc` source honours `headersFrom` like every other source (Q10) |
| NFR-016 | Without any of these settings, a `grpc` source behaves as today. The upgrade differential's `source-grpc` scenario and the pins of section 3 stay green |

UC-003 gets one line under "Delivered by later use cases", and `docs/relay/` gets the source settings table.

## 3. Tests

**Characterization first (PR C, before any change):**

| Pin | Test | Pins |
|---|---|---|
| C1 | `sync/flagd-grpc` `TestDefaultRequestMetadataCharacterization` | Against a loopback flag sync server: the incoming metadata of `SyncFlags` and `FetchAllFlags` with today's settings has no `flagd-selector` key; `headers` arrive as set |
| C2 | `TestDefaultReconnectScheduleCharacterization` | With a fake clock injected through a new `sleep` seam (same behaviour): the delays after repeated stream failures are 4 s, 16 s, 64 s, 60 s, 60 s, and the first failure of `Sync` goes straight to the schedule |
| C3 | `TestDefaultNoDeadlineNoKeepaliveCharacterization` | An idle stream stays open past 2 minutes of fake time with no `GOAWAY` and no ping from the client (the server counts pings); `FetchAllFlags` has no deadline in its context |
| C4 | `TestDefaultFailFastCharacterization` | Without `WaitForReady`, `SyncFlags` against a closed port fails at once and enters the schedule; `PERMISSION_DENIED` on the first call is retried like any error |
| — | already there | `TestReadinessCharacterization`, `Test_ConnectWithRetry`, `Test_SyncRetry`, the stream metric tests, the upgrade differential's `source-grpc` |

**Per feature (P1–P4):**
- table tests per setting: absent means C1–C4 unchanged; set means the provider's behaviour;
- `TestRetryServiceConfigJSON`: the generated service config equals the provider's `buildRetryPolicy` output for the same values, including fractional seconds;
- `TestFatalCodeStopsBeforeFirstPayload` and `TestFatalCodeAfterReadyIsRetried`;
- `TestStreamDeadlineRecyclesWithoutBackoff`;
- `TestKeepaliveParameters`, which checks the ping interval the server sees, including grpc-go's 10 s floor;
- `TestSelectorHeaderOnBothCalls`;
- `TestHeadersFromResolved`;
- `TestConnectionClosedWhenSyncReturns`, with a goroutine and fd leak check;
- `TestUnknownFatalCodeStopsStartup`.

**End to end (T, gated, Q14):** `test/testbed-grpc`. flagd-full syncs from step 13's testbed, through its flag sync port and through envoy's 9212, and the settings are exercised against real outages.

**Drop-in:**
- the upgrade differential's `source-grpc` scenario;
- `make flagd-integration-test`;
- CI's allocs/op comparison.

## 4. PR slicing

| PR | Model | Base | Content | Starts |
|---|---|---|---|---|
| S | mechanic | `main` (spec, straight to `main`) | UC-023, FR-058–FR-063, NFR-016, the UC-003 line, the `docs/relay/` settings table | now |
| C | implementer | `main` | C1–C4. The only code change is the `sleep` seam, behaviour-neutral | now |
| P1 | implementer | C | The new `SourceConfig` keys with validation (Q3, Q5's unknown-code error); `flagdgrpc.Options` (Q11); `headersFrom` (Q10); the `ClientConn` closed when `Sync` returns (item 19) | after C |
| P2 | implementer | P1 | The reconnect delay, `WaitForReady` (Q9) and fatal codes (Q5), in the `Sync` loop | after P1 |
| P4 | implementer | P1 | Dial options: the retry service config (Q4), keepalive (Q13), selector interceptors (Q6), in `Init` | after P1, beside P2 |
| P3 | implementer | P2 | Stream deadline (Q7) and unary deadline (Q12) | after P2 |
| T | implementer | P2 + P3 + P4, and step 13's I merged | The gated testbed job (Q14) | after both |
| X | mechanic | T | UC-023 status, requirement statuses, #158 | last |

The critical path is C → P1 → P2 → P3 → T → X. T also waits for step 13.

## 5. Gates

In WSL from the repository root (`export GOTOOLCHAIN=go1.27.3`; in a worktree `export GOWORK=$PWD/go.work`):

| Gate | Command | PRs |
|---|---|---|
| Unit tests | `make workspace-init && make test` | all |
| Lint, boundaries | `make lint check-module-boundary check-package-coverage` | all |
| Drop-in | the upgrade differential (`source-grpc`), `make flagd-integration-test` | P1–P4 |
| Benchmarks | CI's allocs/op comparison | P1–P4 |
| Testbed | the gated job on a manual dispatch | T |
| Spec | `spec_lint.py --docs docs` on Windows | S, X |

## 6. Risks

- **R1, retry layers multiply.** The service config retries an RPC up to 4 times inside one stream attempt, and the application-level delay then waits `retryBackoffMaxMs`. That is the provider's behaviour, by design (Q4). Timings in the settings documentation are stated as "at least", and `TestRetryServiceConfigJSON` keeps the policy identical to the provider's.
- **R2, keepalive against the upstream's ping policy.**
  - grpc-go raises any client `Time` below 10 s to 10 s.
  - A grpc-go **server** (flagd-proxy is one) enforces a 5-minute minimum by default, and answers faster pings, especially with `PermitWithoutStream`, with `GOAWAY ENHANCE_YOUR_CALM ("too_many_pings")`. flagd's own flag sync endpoint runs on `net/http`, which does not enforce it.
  - The settings table states both. `TestKeepaliveParameters` checks the floor, and T's job runs keepalive against the testbed.
- **R3, fatal codes and readiness (Q5).** A source stopped by a fatal code keeps flagd not ready for good. The error log names the source and the code, and the stream metrics record no reconnect for it.
- **R4, the provider spec and the Go provider differ.** On the initial backoff (Q4) and on the delay after a deadline (Q7), we follow the provider code. Nothing goes upstream (containment); the differences are noted in the plan and the docs.
- **R5, drift from the provider (#156).** We port behaviour, not the provider's package. The provider's service-config builder is matched by a test, not shared. #156 decides whether providers later import `flagd-grpc`.
- **R6, step 13 dependency.** T needs step 13's testbed. P1–P4 do not.
- **R7, `headersFrom` reads secrets.** It is the same resolver as `http`, already reviewed: values are never logged, and unreadable sources stop startup.

## 7. Considered

- **Step 13's testbed** is the end-to-end harness (Q14): real listener closes for reconnects, envoy's `PERMISSION_DENIED` for fatal codes, and `/change` for payloads after a recycle.
- **#149:** the selector header (Q6) is the subscriber's side. An on-demand-selector server keys on it, and flagd-proxy keeps reading the body field, so both get the selector.
- **#156:** the `Options` struct (Q11) is where a provider would later configure the same module.
