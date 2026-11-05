# Step 14 plan: opposer findings (revision 1)

Plan: `.claude/plans/step-14.md` (revision 1, `main` @ 1bba76c8).

Checked against the sources, not the plan's summary of them:
- go-sdk-contrib `providers/flagd@v0.8.0` (the version `test/integration` pins): `pkg/service/in_process/grpc_sync.go`, `grpc_config.go`, `grpc_interceptors.go`, `service.go`, `pkg/configuration.go`, `pkg/provider.go`, `internal/flagdmeta`;
- on this repository's `main`: `sync/flagd-grpc` (`grpc_sync.go`, `factory.go`), `core/pkg/sync` (`isync.go`, `fetch.go`, `headers.go`), `runtime/runtime.go`, `service/flag-sync/fetch.go`, `test/upgrade` (`surfaces.yaml`, `upgrade_test.go`);
- the provider spec, `docs/reference/specifications/providers.md` at e6be905 in the flagd checkout: "Stream Reconnection", "gRPC Retry Policy", "Fatal Status Codes", the configuration table and "Selector Precedence";
- grpc-go **v1.84.0** (the version `sync/flagd-grpc/go.mod` requires): `dialoptions.go`, `internal/internal.go` and `internal/transport/http2_client.go`, for keepalive.

Counts: **0 blockers, 2 major, 9 minor.**

## What holds (verified)

- **Most of the comparison table is right.**
  - **The provider:**
    - the retry service config: `MaxAttempts 4`, initial `RetryBackOffMs` (default 1000), max `RetryBackOffMaxMs` (default 5000), ×2, `UNKNOWN`/`UNAVAILABLE`, on `flagd.sync.v1.FlagSyncService`, durations formatted as `fmt.Sprint(seconds)+"s"` (`grpc_config.go:28-66`);
    - a fixed `RetryBackOffMaxMs` sleep after every failed cycle and none after a deadline recycle (`grpc_sync.go` `Sync`, `performSyncCycle`);
    - `grpc.WaitForReady(true)` on every `SyncFlags`;
    - ready after the first payload (`initializer.Do` in `handleFlagSync`);
    - the selector interceptors on unary and stream calls whenever `Selector != ""`, in the override path too (`createConnection`), with the key `flagd-selector` (`flagdmeta.Selector`);
    - `Shutdown` closes the `ClientConn`;
    - unknown fatal code names warned about and skipped.
  - **Ours:**
    - the reconnect schedule 4 s, 16 s, 64 s, then 60 s (`backOffBase 4`, `backOffLimit 3`, `constantBackOffDelay 60`), which starts over each time a stream ends;
    - `ready` stored when `handleFlagSync` starts, before the first `Recv`;
    - no `WaitForReady`, no deadline, no keepalive;
    - `Headers` sent through `contextWithHeaders` while `headersFrom` is ignored: the factory passes `config.Headers`, whereas `http` calls `sync.ResolveHeaders`;
    - the `ClientConn` never closed.
  - **The spec:** the retry policy with `InitialBackoff` fixed at `"1s"`; the delay of `retryBackoffMaxMs` after "an error or completion"; fatal codes "during initialization"; the defaults 500, 600000, 1000, 5000 and 0; header before body in selector precedence.
- **Today's defaults are kept everywhere the plan says so.** Each addition is absent by default, and C1–C4 pin the absent behaviour.
- **The selector header (Q6) is safe as opt-in.** Our own flag sync endpoint already prefers `Flagd-Selector` over the body (`service/flag-sync/fetch.go:15-21`), and it gets the same value either way.
- **The IDs:** after step 13 revision 3 dropped FR-058 and FR-059, UC-023, FR-058–FR-063 and NFR-016 are free. `main` ends at UC-021 and FR-055.

## Major

### 1. A source that returns from `Sync` stops flagd, so Q5 (a) needs more than the provider's code, and the repository's precedent is (b) (Q5, P2)

- **Step:** Q5, A1, R3, P2's `TestFatalCodeStopsBeforeFirstPayload`.
- **Evidence:**
  - The provider ends `Sync` with `ProviderInitError{ProviderFatalCode}` on a fatal code.
  - In our runtime every `Sync` runs in the runtime's `errgroup`, and an error from it (`"sync provider returned error"`, `runtime.go:113-120`) cancels the group. **flagd exits.** Ported as the provider does it, Q5 (a) "the source stops, flagd keeps running" becomes (b).
  - **The repository already decided this case for another source.** A fetching source whose credentials are refused before its first successful fetch ends the sync provider (`core/pkg/sync/fetch.go:8-15`, `ErrSourceRefused`; UC-019 Q13). The runtime then exits naming the source (`TestRuntimeExitsOnRefusedSource`). A `PERMISSION_DENIED` from a gRPC upstream is the same situation: a key or route that no retry can fix.
  - **The plan's "before the source's first payload" can't reuse our readiness.** `ready` is set when the stream opens (Q8 keeps that), so a `PERMISSION_DENIED` on the first `Recv` arrives with `IsReady()` already true. The provider's `!g.IsReady()` check would never fire here.
- **Fix:**
  - Recommend **(b)**: on a listed code before the first payload, `Sync` returns an error naming the source and the code, and flagd exits as for a refused fetching source. One rule for both kinds of source, and an orchestrator restarts or alerts as it does for any failed start.
  - Track "first payload received" in its own flag, separate from `ready`.
  - Tests: `TestFatalCodeOnFirstRecvStopsSync` (the stream opens, then `PERMISSION_DENIED`) and a runtime-level `TestRuntimeExitsOnFatalGRPCCode` next to `TestRuntimeExitsOnRefusedSource`.
  - If the project lead still wants (a), `Sync` has to stop retrying and **block until ctx is done, returning nil**, with a test that the other sources keep serving. That is not the provider's code, so the plan should say so.

### 2. The provider's `keepAliveTime: 0` pings every 10 s, so the same name means something else here (Q13, Q3, R2)

- **Step:** Q13, Q3 ("absent or zero keeps today's behaviour"), R2.
- **Evidence:** the plan hedges ("may ping every 10 s; the implementer verifies"). In grpc-go v1.84.0 it does:
  - `WithKeepaliveParams` raises any `Time` below `internal.KeepaliveMinPingTime` (10 s) to 10 s **when the dial option is built** (`dialoptions.go:575-583`).
  - The transport's `kp.Time == 0` → infinity branch (`http2_client.go:266`) is therefore never reached through that option.
  - The provider always calls `WithKeepaliveParams` with `Time` 0 by default and `PermitWithoutStream: true`, so **every Go provider pings every 10 s, even without a stream.**
  - A grpc-go server with default enforcement (5-minute `MinTime`, no `PermitWithoutStream`) answers that with `GOAWAY ENHANCE_YOUR_CALM "too_many_pings"` and closes the connection. flagd-proxy is such a server.
  - Q3 keeps the provider's name and makes 0 mean "off". That is the safe default, and the right one, but it's the opposite of what 0 does in the provider.
- **Fix:**
  - Q13: absent and 0 mean no keepalive parameters, as today. That is a deliberate difference from the provider, stated in the settings table.
  - A value from 1 to 9999 is rejected at startup naming the setting and grpc-go's 10 s floor (A2), not silently raised.
  - Replace R2's hedge with the fact above, and say in the table that `PermitWithoutStream` against a grpc-go upstream with default enforcement means GOAWAY.
  - `TestKeepaliveParameters` asserts the `ClientParameters` passed to the dial (through a recording seam) and the rejection below 10 s. Waiting for real pings isn't needed.

## Minor

### 3. Q12 has no caller in flagd

`FetchAllFlags` is only reached through `Sync.ReSync`. On `main` the only `ReSync` callers are flagd-proxy's subscription manager and `synctest`. Step 13's `Reset` calls it only in the testbed, and the testbed's minimal manifest (step 13 Q15) has no `grpc` source. So `deadlineMs` would be a setting with nothing behind it in flagd. The Go provider applies its `deadlineMs` to local in-process evaluations (`applyDeadlineToContext` in `service.go:489-496`), which has no relay analog either. Recommend Q12 (b): leave it out until a caller exists. P3 then holds only the stream deadline, and can fold into P2 (finding 11).

### 4. C3 can't be done with a fake clock

grpc-go's keepalive pings and GOAWAY run on the transport's real timers. The plan's `sleep` seam reaches only `connectWithRetry`. "An idle stream stays open past 2 minutes of fake time with no ping" is therefore either a 2-minute real-time test or an assertion that proves nothing. Pin the absence at the dial instead: a recording seam over the dial options asserts that neither `WithKeepaliveParams`, nor `WithDefaultServiceConfig`, nor any interceptor is passed with today's settings. Keep C3's "no deadline in `FetchAllFlags`'s context" part.

### 5. Today flagd ignores these keys rather than rejecting them

`--sources` is decoded without `DisallowUnknownFields`, so a configuration that already carries provider-named keys today is accepted and the keys are ignored. Provider documentation is where operators copy from: `keepAliveTime`, `deadlineMs`, `retryBackoffMaxMs`. After step 14 such a configuration changes behaviour without any edit. The risk is small, but it is the one way "today's settings" can change meaning. Name it in UC-023 A4 and in the migration notes (#78).

### 6. Where the keys live, and on which providers they're accepted (Q3)

`sync.SourceConfig` is shared by every sync provider in `core`. Flat keys like `deadlineMs` or `keepAliveTime` on an `http` or `file` source would be silently ignored. Either reject a gRPC-only key on another provider at startup, naming source and key, or nest them under one `grpc` object. Q3 should pick one. The flat names match the provider docs, so rejection is the lighter fix. Adding the fields with `omitempty` keeps `TestSourceConfigJSONRoundTripWithoutHeadersFrom` and `start_config.golden` unchanged; say so in P1.

### 7. The selector header and a `flagd-selector` entry in `headers` (Q6)

`metadata.AppendToOutgoingContext` appends, so `selectorHeader: true` together with a `headers` entry for `flagd-selector` sends two values. Our flag sync endpoint's `header.Get` takes the first. Reject the combination at startup (A2), or let one win explicitly.

### 8. A2 tests only one of its cases

A2 says "unknown code name, negative duration", but only `TestUnknownFatalCodeStopsStartup` is listed. Add table cases for:
- a negative `retryBackoffMs`, `retryBackoffMaxMs` or `streamDeadlineMs`;
- `retryBackoffMs` above `retryBackoffMaxMs`;
- `keepAliveTime` below 10 s (finding 2);
- the header conflict (finding 7).

Also state Q4's rule when only one of the two backoff keys is set: the provider's default fills the other.

### 9. What T and the differential can and can't prove (Q14, NFR-016)

- **The upgrade differential's `source-grpc` scenario covers the happy path only:** start, become ready, one resolve (`upgrade_test.go:279-289`). NFR-016's failure paths (schedule, fail-fast, retries) are proven by C1–C4, not by the differential. Say so in NFR-016 and section 5.
- **T can't check the selector header or keepalive enforcement.** The testbed's flag sync endpoint runs on `net/http`, which enforces no ping policy and echoes no headers, so keepalive's GOAWAY risk (finding 2) and the header stay unit-tested. T's real value is reconnects against closed listeners and `PERMISSION_DENIED` from envoy's 9212. Q14 should list that, and no more.

### 10. The dial-option override path

With `GrpcDialOptionsOverride` the provider applies the selector interceptors but neither the retry service config nor keepalive (`createConnection`). Ours applies `DialOptions` (the instrumentation) on both paths. P4 should state which of the new options apply under the override, and `TestOverrideKeepsInterceptorsOnly` should pin it. Also, don't port the provider's package-global `nonRetryableCodes` (`grpc_config.go:68-87`). It is shared across `Sync` instances, so two sources with different `fatalStatusCodes` would overwrite each other. Keep the set per `Sync`.

### 11. The slicing (Q1)

The slicing holds:
- P2 (the `Sync` loop) and P4 (`Init`) edit different functions of `grpc_sync.go`, so a parallel merge is mechanical;
- T rightly waits for step 13's I.

With finding 3, P3 is just the stream deadline, which lives in P2's loop. Fold it into P2: the critical path becomes C → P1 → P2 → T → X, and one PR goes. Q1 (a) otherwise stands.

## Changes to the project lead's open questions

| Question | Change | Finding |
|---|---|---|
| Q1 | P3 folded into P2; six PRs | 3, 11 |
| Q3 | gRPC-only keys rejected on other providers (or nested); 0 is "absent" for every key | 2, 6 |
| Q5 | Recommend (b), like a refused fetching source; (a) only with `Sync` parking until ctx is done | 1 |
| Q12 | Recommend (b), no caller exists | 3 |
| Q13 | 0 means off, unlike the provider; 1–9999 rejected; R2 stated as fact | 2 |
| Q14 | T covers reconnects and fatal codes only; header and keepalive stay unit-tested | 9 |

Q2, Q4 (with finding 8's single-key rule), Q6 (with finding 7), Q7, Q8, Q9, Q10 and Q11 stand as recommended. All 14 are real except Q12, whose premise (step 13's reset calls `ReSync` on a `grpc` source) doesn't hold.
