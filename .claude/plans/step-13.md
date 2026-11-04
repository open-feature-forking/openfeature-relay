# Step 13 plan: the flagd testbed as a relay-builder distribution (UC-022 Run Provider Test Suites Against the Testbed)

**Status: revision 3, with the project lead's second set of decisions.**
- It applies the decisions on Q2, Q3, Q4, Q10, Q11, Q12, Q14, Q15, Q16, Q18 and Q20 to revision 2, which resolved `step-13-opposer.md` (1 blocker, 4 major, 8 minor; section 8). Every other recommendation of revision 2 is accepted as written.
- **Step 13 is a pure drop-in:** no fault injection, no public fetch call.
- Open: Q21–Q24 (section 0).

**Decisions (project lead).**
- **#110 (2026-11-04), in the PoC:** `distributions/flagd-testbed`, with a control endpoint and a listener gate in `service/transport` for stop and start. The provider test suites run against it as the testing harness. Restructuring upstream flagd-testbed is out of scope, and upstream gets nothing: no issues, PRs or comments outside this repository.
- **Q2:** the control API is its own configurable module, which the provider TCK and other SDKs' testbeds reuse.
- **Q3:** a restart keeps the runtime. A start gives a cleaned store, loaded fresh in memory from the sources, and the testbed never changes the files the runtime reads.
- **Q4:** noop telemetry.
- **Q10, Q11, Q18:** fault injection, a public `/fetch` call and an evaluation-recording sink are follow-ups, not part of step 13.
- **Q12:** compatible, with the launchpad's quirks fixed.
- **Q14:** both images, with the pass sets compared.
- **Q15:** no lockstep with flagd-full. The manifest holds only the components the provider suites exercise ("the smaller the better").
- **Q16:** the RPC outage scenarios get a gated CI job, run with `FULL_CI` or a manual dispatch.
- **Q20:** no issue in this repository; the upstream fix is open-feature/flagd#2117.
- **Accepted as written:**
  - the abrupt close with a plain close (FIN), Q6;
  - the gate covering every runtime listener, Q7;
  - the injected `Listen` seam, Q8;
  - the image layout, Q13;
  - the UC-022 spec, Q17;
  - #47's exclusions staying, Q19.

**Base.** `main` @ a684ba1e. Step 10 is merged: operator settings for extension components, `dist.components_hint`, the standard manifests and the `cmd:` entry. Issue: #110. "R" is `github.com/open-feature-forking/openfeature-relay`.

**Drop-in rule for this step.** Today's provider suites run **unchanged** against our testbed:
- the same launchpad endpoints, paths and payloads, without the three quirks of Q12;
- the same ports, image paths, working directory, volume, healthcheck and compose file;
- outages that close listeners, so connections are refused and streams break;
- a store that holds exactly the sources' flags after every start.

## What exists (read at the pinned versions)

**Pinned versions.**
- `test/integration` pins `providers/flagd v0.8.0` and `tests/flagd/v2 v2.3.0`.
- It builds `flagd/build.Dockerfile`, then the testbed image from `test-harness/flagd/Dockerfile`.
- `test-harness` is flagd-testbed **v3.11.0** (9d8dace5).
- The Go suite starts `test-harness/docker-compose.yaml` through testcontainers' compose module, with `IMAGE`, `VERSION` and `FLAGS_DIR`.

**The image:**
- busybox, working directory `/`; `/flagd` is flagd and `/launchpad` the entrypoint;
- `/rawflags/` holds the 11 flag files; `/configs/` the four configs; `/flags/` is mounted from `FLAGS_DIR`;
- `/ssl/` holds the build-time server certificate signed with `ssl/custom-ca.key`, and the root CA.

**The compose file:**
- **Service `flagd`:** ports 8013–8016 and 8080; `${FLAGS_DIR}:/flags`; healthcheck `wget --spider …:8014/healthz` every 10 s, 3 retries, 30 s start period.
- **Service `envoy`:** ports 9211, 9212 and 9901.
  - 9211 routes `rpc.service` to `flagd:8013` and `sync.service` to `flagd:8015`, with 1 s TCP health checks; these are what sees an outage.
  - 9212 answers `403 PERMISSION_DENIED`.

**The launchpad (port 8080, any HTTP method; JSON `{"status","message"}` from `json.NewEncoder`, with a trailing newline):**

| Call | Behaviour |
|---|---|
| `/start?config=<name>` | Remembers `<name>` **before** trying (quirk 1). Kills flagd, regenerates `/flags/allFlags.json` if missing, execs `./flagd start --config ./configs/<name>.json`, and polls `:8014/readyz` for up to 10 s. `200 … "flagd started successfully"`. Otherwise, and for an unknown name too, `500 … "Failed to start flagd: flagd health check timed out"` after 10 s (quirk 2) |
| `/stop` | Cancels a pending restart, kills flagd, `200 … "flagd stopped successfully"` |
| `/restart?seconds=N` | Default 5; anything but a non-negative integer gets `400 … "'seconds' must be a non-negative integer"`. Cancels a pending restart, **deletes `/flags/allFlags.json`**, kills flagd, answers `200 … "flagd will restart in N seconds"` at once, and starts the remembered configuration after N s |
| `/change` | Rewrites `rawflags/changing-flag.json` through a narrow struct with two-space indentation (quirk 3), toggling `changing-flag`'s `defaultVariant` between `foo` and `bar`. Waits up to 5 s for the regenerated `allFlags.json`, then answers `200 … "Default variant successfully changed to '<v>'\n"`. A failure is a plain-text `500` |
| at startup | Deep-merges `rawflags/*.json`, except `selector-*`, into `flags/allFlags.json` and starts `default` |

**Configs:**
- `default`: file sources `flags/allFlags.json` and `rawflags/selector-flags.json`, plus `context-value` and `strict-numeric-range`;
- `ssl`: adds a certificate and key;
- `sync-payload`: adds `disable-sync-metadata`;
- `metadata`: one file source, `rawflags/selector-flag-combined-metadata.json`.

**How the Go suite (tests/flagd v2.3.0) drives it:**
- every call is a GET; it maps 8013, 8014, 8015, 8080, 9211 and 9212, and **not 8016**;
- `/start` runs once per scenario: `ssl`/`tls` and `socket` for those provider types, `default` for every other;
- "the connection is lost for Ns" is `/restart?seconds=N` (N is 6 or 8, and 1 in one `@grace` case); "the flag was modified" is `/change`; `/stop` is never called;
- no Gherkin feature at v3.11.0 and no Go step at v2.3.0 uses OFREP.

**Who reads the flag file directly.** Only file (offline) mode does, from `FLAGS_DIR/allFlags.json`. The pinned `@file` scenarios need:
- the file at start (`evaluation.feature`, `connection.feature` "Connection");
- the file gone during `/restart` and back afterwards (`connection.feature` "Provider reconnection", and `events.feature`'s chains);
- the file rewritten on `/change` (`events.feature` "Flag change event").

The Go suite here runs no `@file` scenario. Other languages run file mode against this image.

**Tag sets CI runs today:**
- **RPC:** no outage scenario.
- **In-process:** `@reconnect`, `@events`, `@grace` and `@targetURI` run, all through `/restart`.
- **Both:** exclude `@fractional-v1` and `@fractional-v2` (#47, Q19).

**The relay side:**
- **Listeners:** flag evaluation and flag sync call `net.Listen`; OFREP, the management port and the event collector use `ListenAndServe`; `ConnectService.Serve` (the embedder path) has its own management server.
- **The runtime:**
  - `Runtime.Start()` blocks on signals.
  - `FromConfig` with OpenTelemetry cannot run twice in a process.
  - Nothing clears the store or re-syncs every source.
- **Sync providers:**
  - `sync/file` is a fetcher whose `fileinfo` trigger watches nothing after the file has been gone for about 1 s. That is revision 2's blocker; upstream's fix is open-feature/flagd#2117.
  - The fetching provider skips a fetch whose version equals the last (`fetching.go:267`). Its `ReSync` forces a full fetch that always delivers.

## 0. Open questions for the project lead

Decided questions keep their number. Q21–Q24 are open.

- **Q1, slicing and parallelism. (updated: F, T and B2 removed)**
  - **Recommendation:** nine PRs (section 7).
    - **Parallel now, all on `main`:** S (the spec), C (the contract against today's image), G1 (the listener gate), G2 (the listen seam), K (the control module) and M (the testbed source and its internal trigger).
    - **L** (runtime seams: `Run`, `Reset`, `LoadConfig`) stacks on G2.
    - **B1** (the testbed `cmd`, the manifest and the Dockerfile) stacks on G1, L, K and M.
    - **I** (CI) stacks on B1 and C. **X** (close) comes last.
    - The critical path is G2 → L → B1 → I → X.
  - *Alternative:* fewer, larger PRs.

- **Q2, the control module. (decided)** `R/testbed/control` is a **library module, not a relay component type**.
  - **Why no component type:** the component types are what a runtime is built from. The control module sits above the runtime and must outlive it: it closes and opens the runtime's gate, resets its store, and replaces it on a configuration switch (Q22).
  - **The API:** `control.New(cfg control.Config, target control.Target) *control.Server` and `Server.Serve(ctx) error`, with:

    ```go
    type Target interface {
        Start(ctx context.Context, config string) error // ready, or an error (500)
        Stop(ctx context.Context) error
        Change(ctx context.Context) (variant string, err error)
        Ready() bool
    }
    ```

  - **What the server does, the same for every target:**
    - the four launchpad calls, their texts and status codes;
    - `/restart`'s delay and the cancellation of a pending restart by a later call;
    - the timeouts;
    - the in-memory combination of the raw flags;
    - the export of Q21.

    The testbed `cmd` supplies the flagd target. The provider TCK, or a Flagsmith or GO Feature Flag testbed, supplies its own and reuses the server, the API and the contract test unchanged.
  - **Outside the gate:** the server opens its own listener with `net.Listen` on its configured address, and is never passed `Components.Listen`.
  - **Configuration:** `control.Config`, plain data a `cmd` fills from flags, `TESTBED_*` environment variables or a file. Defaults reproduce v3.11.0:

    | Key | Default |
    |---|---|
    | `address` | `:8080` |
    | `configs` | `/configs` |
    | `rawFlags` | `/rawflags` |
    | `export` | `/flags/allFlags.json` (Q21; empty means none) |
    | `changingFlag` | `changing-flag`, variants `foo` and `bar` |
    | `startTimeout` | `10s` |
    | `changeTimeout` | `5s` |
    | `defaultRestartSeconds` | `5` |

- **Q3, restart and start. (decided: keep the runtime, cleaned store)**
  - **Close** (`/stop`, `/restart`): the gate closes abruptly (Q6). The runtime keeps running behind it.
  - **Start** (`/start` with the configuration in use, and `/restart`'s delayed start):
    1. `Runtime.Reset(ctx)` (L) removes every source's flags and documents in one store transaction and clears per-source readiness.
    2. It calls `ReSync` on every sync provider: a forced full fetch that bypasses the unchanged-version check and always delivers. This is internal plumbing, not a public call.
    3. Once every source has been accepted again, the runtime is ready, the gate opens, and `/start` answers `200`.
  - **The runtime reads no file the testbed changes:**
    - the combined flags come from the testbed source (Q23);
    - the selector and metadata files are read-only.

    Revision 2's blocker cannot occur.
  - **What survives a restart:** state outside the store (the bus, the process start time). No pinned scenario observes it, and the UC lists it.

- **Q4, telemetry. (decided)** The noop telemetry provider.

- **Q5:** superseded by Q3.

- **Q6, the gate's close. (decided)** Abrupt and plain:
  - the OS listeners close, and every tracked connection gets a plain `Close()` (a FIN; a RST only with unread data);
  - no `Server.Shutdown`, so no `provider_shutdown` and no GOAWAY;
  - `Accept` blocks while closed; opening rebinds with `SO_REUSEADDR`.

- **Q7, the gate's scope. (decided)** Every listener the runtime opens: flag evaluation, flag sync, the management port, and the unix sockets of a `socket` configuration. Not the control port.

- **Q8, the listen seam. (decided)**
  - `service.Settings.Listen`, filled from `distribution.Components.Listen`, defaulting to `net.Listen`.
  - flag evaluation, flag sync, OFREP, the event collector and the management port use it, so the seam is uniform even though the testbed links only some of them (Q15).
  - `ConnectService.Serve`'s embedder-path management server is the named exception.
  - flagd-full is unchanged.

- **Q9, `/change`. (follows from Q3)**
  - At startup the control module deep-merges `rawflags/*.json` (except `selector-*`) in memory; the files are read once and never written.
  - `/change` toggles `changing-flag`'s `defaultVariant` in that document, keeping every other field, and calls the target's `Change`. The flagd target fires the testbed source's internal trigger, and the store accepts the new version.
  - The server answers `200 … "Default variant successfully changed to '<v>'\n"` once the store holds the new variant, within 5 s. Otherwise it answers the plain-text `500`.
  - The toggle survives restarts, as the rewritten file does today, because `Reset` reloads the same document.
  - File-mode providers see the change only through Q21.

- **Q10, `/fetch`. (decided: a follow-up)**
  - No public endpoint.
  - The testbed source's trigger stays, as internal plumbing that `/change` fires. Its type name is not selectable in a configuration file.
  - The fetch trigger named in #110 is therefore internal to step 13; a public trigger is the follow-up.

- **Q11, fault injection. (decided: a follow-up)** No fault middleware, no `/faults` and no new fault types in step 13. UC-001's gap "no repository manifest compiles middleware with `cmd:`" stays open for that follow-up.

- **Q12, fidelity. (decided: compatible, quirks fixed)** Same endpoints, paths, methods, payload shapes (with the trailing newline), messages and status codes for every case that works today. The three quirks go:

  | # | Quirk at v3.11.0 | Revision 3 | A pinned suite depends on it? |
  |---|---|---|---|
  | 1 | A failed `/start?config=nope` is remembered, so a later `/restart` or bare `/start` fails too | A failed start leaves the last good configuration remembered | No. The only names at v2.3.0 are `default`, `ssl` and `socket`, and `socket` is excluded by `@unixsocket` in CI |
  | 2 | An unknown configuration answers after the 10 s readiness timeout, as "health check timed out" | It answers at once (Q24 for the code) | No. Every caller checks `200` only |
  | 3 | `/change` rewrites `changing-flag.json` through a narrow struct | No file is rewritten; the in-memory toggle keeps every field | No for RPC and in-process. File mode sees the export (Q21), whose `changing-flag` keeps `state`, `variants` and `defaultVariant` |

  - C pins the three quirks on today's image as **launchpad-only** cases.
  - **Kept, because suites depend on them:** any HTTP method; `/restart`'s immediate answer and its cancellation of a pending restart; `/stop`'s cancellation; the 10 s limit for a start that doesn't get ready; the 5 s limit for `/change`.

- **Q13, the image. (decided)** `distributions/flagd-testbed/Dockerfile`, with the repository root as context:
  - `relay-builder` builds the binary;
  - upstream's certificate stage; busybox with `WORKDIR /`; `/flagd` as the entrypoint;
  - `/rawflags/` and `/configs/` from `test-harness`; `/flags/` as the volume; `/ssl/`;
  - ports 8013–8016 and 8080, though nothing listens on 8016 (Q15); not published.

- **Q14, CI. (decided)**
  - `TestRPC` and `TestInProcess`, unchanged, against today's launchpad image and the relay testbed. CI fails if the pass sets differ, or if ours fails a scenario the launchpad image passes.
  - The relay testbed builds for any Go change outside `docs/` and `.claude/`, for the upgrade differential's paths, and with `FULL_CI` or a manual run.
  - Other languages' suites, including file mode, run manually in the PoC.

- **Q15, the manifest. (decided: minimal, no lockstep)** `distributions/flagd-testbed/manifest.yaml` holds exactly these:

  | Component | Why the suites need it |
  |---|---|
  | sync provider `R/sync/file` | every config's `rawflags/selector-flags.json` (selector scenarios in RPC and in-process) and `metadata`'s single source |
  | sync provider `R/testbed/source` (M) | the combined flags that `default`, `ssl` and `sync-payload` name as `flags/allFlags.json` (Q23); every RPC and in-process scenario |
  | configuration format `R/configformat/flagd` | every source's flag configuration |
  | evaluator `R/evaluator/flagd` | `TestRPC`: every `@rpc` evaluation through 8013 |
  | service endpoint `R/service/flag-evaluation` | `TestRPC` (8013), and envoy's `rpc.service` route |
  | service endpoint `R/service/flag-sync` | `TestInProcess` (8015), and envoy's `sync.service` route (`@targetURI`) |
  | telemetry: none, so noop (Q4) | — |
  | `cmd: R/testbed/cmd` | the control module and the flagd target |
  | `components_hint: false` | flagd's error texts |

  - **Not included:** OFREP (no pinned feature or step uses it, and 8016 is not mapped), the event collector, the http, flagd-grpc, Kubernetes and blob sync providers (no config names them), and OpenTelemetry.
  - The management port is the runtime's and is always there.
  - B1's `TestTestbedManifestIsMinimal` pins this exact list, so a component added without a suite that needs it is a visible diff.

- **Q16, RPC outage scenarios. (decided: gated CI job)**
  - The gating tag sets stay unchanged.
  - A job `testbed-rpc-outages` in `build.yaml` runs `TestRPC` with `@reconnect`, `@events` and `@grace` included, against both images, and fails on a difference in pass sets.
  - It runs only with `FULL_CI=true` or a manual dispatch, as the FIPS job does.

- **Q17, the use case. (decided)** Section 1's UC-022, with Provider Author as primary actor and CI as secondary. Requirements:
  - FR-056: a launchpad-compatible control API as a reusable module;
  - FR-057: outages close listeners, and every start reloads a cleaned store;
  - NFR-014: drop-in: unchanged suites pass the same scenarios;
  - NFR-015: testbed components are never in a standard distribution.

  Revision 2's FR-058 (faults) and FR-059 (trigger) are dropped with Q10 and Q11.

- **Q18, the evaluation-recording sink. (decided: a follow-up)**

- **Q19, #47's fractional scenarios. (decided)** #47 needs not land first. The `@fractional-v1` and `@fractional-v2` exclusions stay in `test/integration`, owned by #47, whose definition of done gains removing them there too.

- **Q20, flagd-full's file source after a deletion. (decided)** Nothing in step 13; no issue here. The upstream fix is open-feature/flagd#2117, "fix(core): re-watch file sync path after the file is deleted and restored". Step 13 doesn't depend on it.

- **Q21, file-mode providers. (open)**
  - **The constraint:** file mode reads `FLAGS_DIR/allFlags.json` directly. Its `@file` scenarios need the file at start, its removal across `/restart`, and its rewrite on `/change`. The decision says the testbed never changes the files the runtime reads.
  - **Options:**
    - **(a) An export file the runtime never reads.** The control module writes `/flags/allFlags.json` as the launchpad does: at startup and on `/change`, removed on `/restart`, recreated on start. Revision 2's blocker cannot return. Every pinned `@file` scenario keeps working.
    - **(b) No file mode.** Nothing is written; `@file` suites keep the launchpad image.
    - **(c) A static export, written once.** The `@file` evaluation scenarios pass; `@file @reconnect`, the event chains and "Flag change event" are excluded for file runs.
  - **Recommendation:** (a), off when `export` is empty.

- **Q22, `/start` with another configuration. (open)** `ssl`, `sync-payload` and `metadata` change TLS, sources and endpoint settings, which a running runtime cannot change. CI's Go suites use `default` only; other languages use `ssl` (`@customCert`).
  - **Options:**
    - **(a) Rebuild on a switch only.** Close the gate, cancel the runtime, build one from the other file, open the gate when ready. Needs `FromConfig` reentrant with noop telemetry for a few switches per run (L: 20 cycles with leak checks).
    - **(b) A child process on a switch.** The testbed `cmd` re-executes itself with the new configuration; the control module stays in the parent.
    - **(c) Unsupported.** Another name answers `500 "configuration switch not supported"`; `ssl` suites keep the launchpad image.
  - **Recommendation:** (a).

- **Q23, how the testbed source replaces `flags/allFlags.json`. (open)** The configs stay upstream's (BR-008) and name `flags/allFlags.json` as a file source.
  - **Options:**
    - **(a) The loader rewrites that one source.** `runtime/cmd.LoadConfig`, given a rewrite rule by the testbed `cmd`, turns the file source `flags/allFlags.json` into `testbed:allFlags`; every other source stays.
    - **(b) The testbed ships edited copies of the configs.** This breaks BR-008, and the copies drift.
    - **(c) The testbed source claims the `flags/` prefix.** That clashes with the file sync provider's bare paths.
  - **Recommendation:** (a), with a test that the rewrite touches exactly that source in each config.

- **Q24, the code for an unknown configuration. (open)** Quirk 2 goes, so `/start?config=nope` answers at once.
  - **Options:**
    - **(a) `500` with a truthful message:** `{"status":"error","message":"Failed to start flagd: unknown configuration \"nope\""}`. This is the launchpad's code for every failed start.
    - **(b) `404`**, same JSON shape.
    - **(c) `400`**, as for bad `seconds`.
  - **Recommendation:** (a). A client that branches on the code sees no new value.

## 1. Scope

### UC-022: Run Provider Test Suites Against the Testbed (draft for PR S)

- **Primary actor:** Provider Author. **Secondary actor:** CI.
- **Goal:** run a provider's Gherkin suite against the flagd being released, with the outages the suite controls, without changing the suite.
- **Trigger:** a provider suite starts the testbed through the flagd-testbed compose file.
- **Preconditions:** the testbed image is built from `distributions/flagd-testbed`; the flagd-testbed data is the pinned `test-harness`.

**Main success scenario:**
1. CI starts the compose stack with the testbed image.
2. The control module combines the raw flags in memory, writes the export (Q21), and builds the runtime from `configs/default.json`, with `flags/allFlags.json` read from the testbed source. When the runtime is ready, 8013–8015 open. The control API on 8080 is up from the start.
3. The suite calls `/start?config=default` in every scenario. The testbed closes the gate, resets and reloads the store, opens the gate when the runtime is ready again, and answers.
4. The suite evaluates flags over RPC, in-process sync or the exported file.
5. The suite calls `/change`. The testbed toggles `changing-flag` in memory, fires the source's internal trigger, rewrites the export, waits until the store holds the new variant, and answers.
6. The suite calls `/restart?seconds=N`. The testbed answers at once, closes the gate and removes the export; providers see refused connections and broken streams. After N s it starts as in step 3.
7. The providers reconnect and fire their ready events; the suite passes the same scenarios as against the launchpad image.

**Alternative flows:**
- **A1 Configuration switch:** Q22.
- **A3 Invalid seconds:** `400`.
- **A4 Stop:** the gate stays closed until the next `/start`.
- **A7 Change times out:** plain-text `500`.
- **A8 Not ready in 10 s:** `500 … health check timed out`, gate closed.
- **A9 Unknown configuration:** answered at once (Q24); the last good configuration stays remembered.

**Business rules:**
- **BR-001 Compatible API:** v3.11.0's endpoints, paths, methods, payload shapes, messages and codes, except Q12's three quirks.
- **BR-002 Outages close sockets:** a plain close, no application-level goodbye; the control port stays up.
- **BR-003 Cleaned store per start:** every start empties the store, re-syncs every source, and opens the gate only when every source has been accepted again.
- **BR-004:** the runtime reads no file the testbed changes.
- **BR-005 Minimal:** the testbed contains only the components listed in Q15, each needed by a pinned suite.
- **BR-006 Test-only:** NFR-015.
- **BR-007 Data stays upstream's:** flags, configs, certificates, the compose file and Gherkin are unmodified; Q23's rewrite happens at load time.
- **BR-008 Reusable control:** the control module knows no flagd type; a target supplies the behaviour.

**Realizes:**

| Requirement | Delivered by |
|---|---|
| FR-056 | K (module), B1 (flagd target); C and I (contract) |
| FR-057 | G1, G2, L, M; B1 |
| NFR-014 | I |
| NFR-015 | B1 |

**Use case coverage:**

| Flow | Test |
|---|---|
| main 1–7, BR-001, NFR-014 | I: `TestRPC` and `TestInProcess` against both images; `TestTestbedContract` (C) against ours |
| main 3, BR-003 | L's `TestResetEmptiesAndReloads` (every source re-accepted, unchanged content delivered through `ReSync`, readiness false until then); B1's `TestStartReloadsCleanedStore` |
| main 5, BR-004 | B1's `TestChangeThroughTrigger` (the store holds the new variant; no file under `/rawflags` or `/configs` changes, by mtime and content), `TestChangeSurvivesRestart`, `TestChangeRestartChange` (revision 2's blocker sequence) |
| main 6, BR-002 | G1's gate tests; B1's `TestRestartClosesEveryRuntimePort`, `TestControlPortOutsideGate` |
| A1 | B1's `TestConfigSwitch` (Q22) |
| A3, A4, A7, A8, A9 | K's tests against a fake target: `TestStopUntilStart`, `TestChangeTimesOut`, `TestStartNotReadyIn10s`, `TestUnknownConfiguration`, `TestFailedStartKeepsLastGoodConfiguration`; C and I |
| Q21 export | K's `TestExportLifecycle`; C3 |
| BR-005 | `TestTestbedManifestIsMinimal` |
| BR-006 | `TestStandardManifestsHaveNoTestbedModule` |
| BR-007 | `TestImageDataMatchesSubmodule`; L's `TestLoadConfigRewriteTouchesOneSource` |
| BR-008 | K's tests use a fake target only; a boundary row keeps flagd and relay modules out of `testbed/control` |

**Leaves out (follow-ups):**
- fault injection and `/faults` (Q11);
- a public fetch trigger and `/fetch` (Q10);
- an evaluation-recording sink (Q18);
- OFREP in the testbed (Q15);
- other languages' suites in CI (Q14);
- tracing (#59);
- `@fractional-v1` and `@fractional-v2` (#47);
- restructuring upstream flagd-testbed;
- publishing the image.

## 2. Characterization (PR C, base `main`, parallel now)

The oracle is today's image, built as `test/integration` builds it. The contract test is its own Go module (`test/testbed`), parameterized by image. Cases marked **launchpad-only** pin a quirk Q12 drops.

| Pin | Test | Pins |
|---|---|---|
| C1 | `TestTestbedContract/api` | For the four calls, with GET and POST: code, `Content-Type`, the exact body with the trailing newline. `/restart?seconds=x` and `-1`. `/change` three times. **Launchpad-only:** `/start?config=nope` after at least 10 s with "health check timed out" (quirk 2), and the remembered `nope` failing the next `/restart?seconds=0` and bare `/start` (quirk 1). These run last and recover with `default` |
| C2 | `TestTestbedContract/outage` | During `/stop` and `/restart?seconds=3`: 8013, 8014 and 8015 refuse, and 8080 answers. What an idle `EventStream` (8013) and `SyncFlags` stream (8015) see is recorded, with no `provider_shutdown` before it. Afterwards `/readyz` is 200 and both streams reopen |
| C3 | `TestTestbedContract/files` | `/flags/allFlags.json` exists at start, is gone during a restart, and is back afterwards. After each `/change` its `changing-flag` has the toggled `defaultVariant`. **Launchpad-only:** the byte layout of `rawflags/changing-flag.json` (quirk 3) |
| C4 | `TestTestbedContract/configs` | `ssl` serves TLS with the custom root CA; `sync-payload` syncs without metadata; `metadata` serves only the metadata flag set |
| C5 | `TestTestbedContract/change-after-restart` | `/change`, `/restart?seconds=8`, `/change`: an RPC evaluation of `changing-flag` follows both toggles |

## 3. Steps

Every commit builds and passes `make test`. G2 and L must be invisible to flagd-full.

| PR | Model | Base | Files | Change | Check |
|---|---|---|---|---|---|
| S | mechanic | `main` | `docs/use_cases/UC-022-*.md`, `docs/requirements.md`, `docs/use_cases.puml`, `docs/glossary.md` (Testbed, Launchpad API, Listener Gate, Control Target) | Section 1 | spec lint |
| C | implementer | `main` | `test/testbed/`, `Makefile` `testbed-contract` | Section 2 | green against the launchpad image |
| G1 | implementer | `main` | `service/transport/gate/` | Virtual listeners; `Open`; `Close` with a plain close; address release | the gate tests; `-race`; 100 reopen cycles |
| G2 | implementer | `main` | `core/pkg/service`, `core/pkg/distribution`, `runtime`, `service/flag-evaluation`, `service/flag-sync`, `service/ofrep`, `service/event-collector` | Q8 | existing pins unchanged; `TestListenSeamUsedByEveryEndpoint`; `make check-flagd-full`; acceptance; upgrade differential; allocs/op |
| K | implementer | `main` | `testbed/control/` (module) | Q2's server, `Config` and `Target`; the in-memory deep merge (ported); Q21's export; the launchpad texts; Q12's fixes and Q24 | K's tests against a fake target; C's api and files cases against a K server with a fake target |
| M | implementer | `main` | `testbed/source/` (module) | The testbed sync provider (a fetcher, scheme `testbed:`, fetching a document from the store it is given, versioned by digest), with an internal trigger its owner fires. The trigger is not registered as a selectable trigger type | `synctest` contract run; `TestTriggerFetchesNow`; `TestUnchangedDocumentNotRedelivered`; `TestTriggerNotSelectable` |
| L | implementer | G2 | `runtime` (`Runtime.Run(ctx)`; `Runtime.Reset(ctx)`; `Start` unchanged), `runtime/cmd` (`LoadConfig(path, components, rewrites)` over a private viper) | Q3; Q22 (a); Q23 (a) | `TestRunStopsOnContext`, `TestResetEmptiesAndReloads`, `TestResetUnderLoad` (every answer during a reset is a flag or `FLAG_NOT_FOUND`), `TestFromConfigRepeatedly` (20 cycles, `goleak`, fd count), `TestLoadConfigMatchesStart` and `TestLoadConfigRewriteTouchesOneSource` per `test-harness` config; `start_config.golden` unchanged |
| B1 | implementer | G1 + L + K + M | `testbed/cmd/` (module: the flagd `Target` wiring the gate, `Reset`, the testbed source over K's document and its trigger, `LoadConfig` with Q23's rewrite, Q22's switch), `distributions/flagd-testbed/manifest.yaml` (Q15), `Dockerfile` (`WORKDIR /`), `test/distributions` (`TestTestbedManifestIsMinimal`, `TestStandardManifestsHaveNoTestbedModule`, `TestImageDataMatchesSubmodule`), boundary rows | Q3, Q6, Q7, Q9, Q13, Q15, Q21–Q23 | B1's tests; the manifest builds in `make test-distributions`; `docker build` in WSL; C's contract against the built image |
| I | implementer | B1 (+ C merged) | `test/integration` (image chosen by an environment variable; the default unchanged), `test/testbed` (second image), `.github/workflows/build.yaml` (the relay testbed run with Q14's filter; the gated `testbed-rpc-outages` job, Q16) | Q14, Q16 | both images pass `TestRPC` and `TestInProcess` with today's tags; pass sets equal; contract against both, launchpad-only cases on the launchpad image only; the outage job green on a manual dispatch |
| X | mechanic | I | UC-022 status, `docs/relay/testbed.md` (how the TCK and other testbeds plug in a `Target`), requirement statuses, #110, follow-up issues for Q10, Q11 and Q18 in this repository | Close | spec lint |

`test/integration`'s suites are not edited; only the image under them changes.

## 4. Gates

In WSL from the repository root (`export GOTOOLCHAIN=go1.27.3`; in a worktree `export GOWORK=$PWD/go.work`; for lint `export GOPATH=$(go env GOPATH)`).

| Gate | Command | PRs |
|---|---|---|
| Unit tests | `make workspace-init && make test` | all |
| Lint, boundaries | `make lint check-module-boundary check-package-coverage`. New rows: `R/testbed/...` is imported only by `testbed/*`, `test/*` and `distributions/flagd-testbed`; `testbed/control` imports nothing of flagd's | all |
| flagd-full unchanged | `make check-flagd-full`, the upgrade differential, `make flagd-integration-test` | G2, L |
| Benchmarks | CI's allocs/op comparison (`Reset` is off the request path) | G2, L |
| Testbed contract | `make testbed-contract IMAGE=<image>` | C, K, B1, I |
| Provider suites | `make flagd-integration-test` with `TESTBED_IMAGE=relay` and with the default; the `testbed-rpc-outages` job on a manual dispatch | I |
| Spec | `spec_lint.py --docs docs` on Windows | S, X |

## 5. Risks

- **R1, store reset correctness (the biggest).** `Reset` must leave no old flag, never expose a half-loaded store, and re-accept every source even when its content is unchanged.
  - **Covered by:**
    - the gate, closed for the whole reset;
    - `ReSync`'s forced fetch past the unchanged-version check (`fetching.go:267`);
    - per-source readiness cleared before the reload;
    - L's `TestResetEmptiesAndReloads` and `TestResetUnderLoad`.
  - **Streaming sources:** a source whose `ReSync` never delivers would keep the start from completing. The testbed has only file and testbed sources (Q15). `Reset` fails with the source's name at the start timeout instead of hanging.
- **R2, gate fidelity against SIGKILL.** Providers distinguish `UNAVAILABLE`, `CANCELLED`, EOF and reset. The plain close matches a killed process. C2 records idle-stream errors on today's image, and I compares ours. The in-process `@reconnect`, `@events` and `@grace` scenarios, plus the gated RPC outage job, are the acceptance.
- **R3, the runtime survives a restart.** State outside the store is kept. No pinned scenario observes it; the pass-set comparison is the alarm, and the UC lists the state.
- **R4, timing.** A start is a reset and a reload, well under a second, against the launchpad's process start. Recovery is a little faster than today, never slower.
- **R5, the testbed reads `allFlags.json` from the testbed source, not from `sync/file`.** `sync/file` is still exercised by the selector and metadata sources, and flagd-full's own file path by the launchpad image I keeps.
- **R6, a minimal manifest diverges from flagd-full.** A provider suite that later starts using OFREP or another sync provider fails against the relay testbed and passes against the launchpad image. The pass-set comparison names it, and the fix is one manifest line plus `TestTestbedManifestIsMinimal`.
- **R7, G2 and L touch flagd-full.** Bind errors, sockets, TLS and `Start`'s signals must stay byte-identical; the existing pins and the upgrade differential cover them.
- **R8, reuse pressure on K.** K's tests use a fake target, and a boundary row keeps flagd types out. X documents the interface with a sketch for a second target.
- **R9, CI time.** About 8 minutes more in `integration-test` when Q14's filter matches. The outage job runs only with `FULL_CI` or a manual dispatch.
- **R10, upstream drift.** A new launchpad call upstream fails the differential until K implements it.
- **R11, containment.** Nothing goes upstream. The ported deep merge is Apache-2.0 code from the same organization, and the file says where it comes from.

## 6. Mapping: launchpad to relay pieces

| Launchpad / testbed piece | Relay piece | PR |
|---|---|---|
| launchpad process, port 8080, its four calls | `R/testbed/control`: a reusable library server driving a `Target`, on its own listener | K |
| flagd binary at `/flagd` | the minimal testbed distribution (Q15) | B1 |
| kill and re-exec on `/start` | gate close, `Runtime.Reset` (store emptied, every source re-synced), gate open when ready | G1, L, B1 |
| `/stop` | gate close | G1, B1 |
| `/restart?seconds=N` | gate close and export removal; a start after N s | G1, K, B1 |
| SIGKILL socket behaviour | plain close, no `Shutdown` | G1 |
| every flagd port | `Components.Listen` through the gate | G2 |
| `configs/*.json` | `runtime/cmd.LoadConfig`, with `flags/allFlags.json` rewritten to `testbed:allFlags` | L, B1 |
| `rawflags/` combined into `allFlags.json` | combined once in memory, served by the testbed source | K, M |
| `/change` rewriting `changing-flag.json` | in-memory toggle, internal trigger, wait on the store | K, M, B1 |
| `/flags/allFlags.json` for file-mode providers | the export (Q21), never read by the runtime | K |
| a configuration switch | Q22 | L, B1 |
| `/ssl` certificates | the same certificate stage | B1 |
| compose file, envoy, volume, healthcheck | unchanged, from `test-harness` | — |

## 7. PR split

| PR | Branch | Base | Runs |
|---|---|---|---|
| S | straight to `main` (spec) | `main` | now |
| C | `uc-022/pr-1-testbed-contract` | `main` | now |
| G1 | `uc-022/pr-2-listener-gate` | `main` | now |
| G2 | `uc-022/pr-3-listen-seam` | `main` | now |
| K | `uc-022/pr-4-control-module` | `main` | now |
| M | `uc-022/pr-5-testbed-source` | `main` | now |
| L | `uc-022/pr-6-runtime-reset` | G2 | once G2's branch exists |
| B1 | `uc-022/pr-7-testbed-distribution` | G1 + L + K + M | once those branches exist |
| I | `uc-022/pr-8-suites-in-ci` | B1 (+ C merged) | after B1 |
| X | `uc-022/pr-9-close` | I | last |

Every PR description starts with `Part of #110`.

## 8. Changelog

### Revision 3, second set of decisions

| Decision | Change |
|---|---|
| Q10 `/fetch` a follow-up | PR T and `/fetch` removed; the testbed source keeps an internal trigger for `/change`, not selectable in configuration |
| Q11 faults a follow-up | PRs F and B2, `/faults`, `Faulter` and FR-058 removed; UC-001's middleware gap stays open |
| Q14 both images | Decided as recommended |
| Q15 minimal manifest | Lockstep test dropped; the manifest lists six components plus `cmd`, each with the suite that needs it; `TestTestbedManifestIsMinimal`; OFREP, the collector, other sync providers and OpenTelemetry left out; R6 added |
| Q16 gated job | The `testbed-rpc-outages` job runs with `FULL_CI` or a manual dispatch |
| Q18 sink a follow-up | Decided as recommended |
| Q20 no issue | References open-feature/flagd#2117 as plain text; X opens no issue for it |
| Rest accepted | Q6, Q7, Q8, Q13, Q17 and Q19 marked decided |

### Revision 3, first set of decisions

| Decision | Change |
|---|---|
| Q2 own configurable module | `R/testbed/control`: a library with `Config` and `Target`, outside the gate by its own listener; PR K; BR-008 |
| Q3 keep the runtime, cleaned store | `Runtime.Reset` (store emptied, readiness cleared, `ReSync` on every source); the runtime reads no file the testbed changes; `/change` toggles an in-memory document served by the testbed source (M). Open: Q21–Q23 |
| Q4 noop telemetry | Decided |
| Q12 quirks fixed | Quirks 1–3 dropped and pinned as launchpad-only; no pinned suite depends on them. Open: Q24 |

### Revision 2 (`step-13-opposer.md`)

| Finding | Severity | Outcome now |
|---|---|---|
| 1 A gate-only restart leaves `allFlags.json` unwatched | blocker | Removed at the root: the runtime reads no file the testbed changes (Q3, Q23); C5 and `TestChangeRestartChange` pin the scenario |
| 2 `/start` once per scenario | major | Kept; each start is a reset, well under a second |
| 3 No `SetLinger(0)` | major | Kept (Q6) |
| 4 A7 and A8 testable; A2 merged | major | Kept; A9 added (Q12, Q24) |
| 5 Split B; L on G2 | major | L on G2 kept; B is one PR again (B1) now that faults are out |
| 6 Remembered failed configuration | minor | Dropped by decision (quirk 1); pinned as launchpad-only |
| 7 File sources take triggers; `/change` waits | minor | Kept; `/change` waits on the store |
| 8 `/change`'s narrow struct | minor | Dropped by decision (quirk 3); pinned as launchpad-only |
| 9 `WORKDIR /` | minor | Kept (Q13) |
| 10 RPC outages | minor | Now a gated CI job by decision (Q16) |
| 11 Change filter | minor | Kept (Q14) |
| 12 Second listener in flag evaluation | minor | Kept (Q8) |
| 13 Envoy 9901; healthcheck cadence | minor | Kept |
