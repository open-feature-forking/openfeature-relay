# Step 13 plan: the flagd testbed as a relay-builder distribution (UC-022 Run Provider Test Suites Against the Testbed)

**Status: revision 3, final. Every question (Q1–Q25) is decided by the project lead.**
- It applies the project lead's decisions to revision 2, which resolved `step-13-opposer.md` (1 blocker, 4 major, 8 minor; section 8):
  - first set: Q2, Q3, Q4 and Q12;
  - second set: Q10, Q11, Q14, Q15, Q16, Q18 and Q20;
  - the clarification of Q3 and `/change`;
  - last: Q22–Q25.

  Every other recommendation of revision 2 is accepted as written.
- **Step 13 is a pure drop-in:** no fault injection, no public fetch call.
- `/change` alters the in-memory state, and `allFlags.json` is an output rendered from it (Q9, Q21, Q25).
- Open: none.

**Decisions (project lead).**
- **#110 (2026-11-04), in the PoC:** `distributions/flagd-testbed`, with a control endpoint and a listener gate in `service/transport` for stop and start. The provider test suites run against it as the testing harness. Restructuring upstream flagd-testbed is out of scope, and upstream gets nothing: no issues, PRs or comments outside this repository.
- **Q2:** the control API is its own configurable module, which the provider TCK and other SDKs' testbeds reuse.
- **Q3:** a restart keeps the runtime. A start gives a cleaned store, loaded fresh in memory from the sources, and the testbed never changes the files the runtime reads.
- **Q3 and `/change`, clarified:**
  - `/change` is an endpoint that alters the in-memory state, the store.
  - `allFlags.json` is written from the in-memory state, never by rewriting source files. It is an output for providers that read the file directly (file, offline, and in-process file mode), and never a source the runtime reads back.
  - The source flag files from `test-harness` never change.
  - The testbed re-renders `allFlags.json` after every `/change` and after every start that cleans and reloads the store. This settles Q21.
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
- the same launchpad endpoints, paths and payloads, without the four quirks of Q12;
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

Every question is decided. Each keeps its number; the decided option is stated first.

- **Q1, slicing and parallelism. (decided: accepted as written; F, T and B2 removed)**
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
        // Start cleans the store and reloads every source, the combined document included; it returns when ready,
        // with the target still closed.
        Start(ctx context.Context, config string) error
        // Open makes the ready target reachable: it opens the listener gate.
        Open(ctx context.Context) error
        Stop(ctx context.Context) error
        // Load hands the target the combined document and returns once the store has accepted that version.
        Load(ctx context.Context, document []byte, version string) error
        Ready() bool
    }
    ```

  - **The order of a start:** `Start` leaves the target ready but closed. The server then renders `allFlags.json` (Q25) and calls `Open`. If `Open` fails, the server stops the target and the start fails. #157 implements it this way.
  - **What the server does, the same for every target:**
    - the four launchpad calls, their texts and status codes;
    - `/restart`'s delay and the cancellation of a pending restart by a later call;
    - the timeouts;
    - the in-memory state: the combined document and its toggles (Q9);
    - rendering `allFlags.json` from that state once the target confirms the store holds it (Q25).

    The testbed `cmd` supplies the flagd target. The provider TCK, or a Flagsmith or GO Feature Flag testbed, supplies its own and reuses the server, the API and the contract test unchanged.
  - **Outside the gate:** the server opens its own listener with `net.Listen` on its configured address, and is never passed `Components.Listen`.
  - **Configuration:** `control.Config`, plain data a `cmd` fills from flags, `TESTBED_*` environment variables or a file. Defaults reproduce v3.11.0:

    | Key | Default |
    |---|---|
    | `address` | `:8080` |
    | `configs` | `/configs` |
    | `rawFlags` | `/rawflags` |
    | `render` | `/flags/allFlags.json` (Q21, Q25; empty means none) |
    | `changingFlag` | `changing-flag`, variants `foo` and `bar` |
    | `startTimeout` | `10s` |
    | `changeTimeout` | `5s` |
    | `defaultRestartSeconds` | `5` |

- **Q3, restart and start. (decided: keep the runtime, cleaned store)**
  - **Close** (`/stop`, `/restart`): the gate closes abruptly (Q6). The runtime keeps running behind it.
  - **Start** (`/start` with the configuration in use, and `/restart`'s delayed start):
    1. `Runtime.Reset(ctx)` (L) removes every source's flags and documents in one store transaction and clears per-source readiness.
    2. It calls `ReSync` on every sync provider: a forced full fetch that bypasses the unchanged-version check and always delivers. This is internal plumbing, not a public call.
    3. Once every source has been accepted again, the runtime is ready.
    4. The control module re-renders `allFlags.json` from the in-memory state (Q25). Then the gate opens, and `/start` answers `200`.

    `/restart` also removes the rendered `allFlags.json` while the gate is closed, as the launchpad deletes it today. File-mode providers see the outage as a missing file (`@file @reconnect`, `events.feature`). Removing the output changes no source file, and the next start renders it again.
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

- **Q9, `/change`. (decided: it alters the in-memory state, the store)**
  - **At startup** the control module reads `rawflags/*.json` (except `selector-*`) once and deep-merges them into a map, as the launchpad does: decoded into `interface{}`, so numbers are `float64`. The source files are never written.
  - **`/change`:**
    1. toggles `changing-flag`'s `defaultVariant` between `foo` and `bar` in that map;
    2. encodes the map with the launchpad's encoder (Q25), which gives the document bytes and a version (their digest);
    3. calls `Target.Load(bytes, version)`. The flagd target hands the bytes to the testbed source and fires its internal trigger, and returns once the store holds that version;
    4. re-renders `allFlags.json` from the same bytes (Q25);
    5. answers `200 … "Default variant successfully changed to '<v>'\n"`, all within 5 s. Otherwise it answers the plain-text `500`.
  - **The runtime reads exactly the bytes the launchpad would have written.** Today flagd reads the launchpad's `allFlags.json`, so RPC and in-process providers see the same flag values, including the numbers re-encoded as the launchpad encodes them (`10.0` becomes `10`).
  - **Order:** the file is written only after the store has accepted the version, so a file-mode provider never sees a state the RPC and in-process paths don't serve yet. The launchpad wrote the file first and let flagd catch up.
  - **Restarts:** the toggle survives them, as the rewritten source file does today, because a start reloads the same in-memory state.

- **Q10, `/fetch`. (decided: a follow-up)**
  - No public endpoint.
  - The testbed source's trigger stays, as internal plumbing that `/change` fires. Its type name is not selectable in a configuration file.
  - The fetch trigger named in #110 is therefore internal to step 13; a public trigger is the follow-up.

- **Q11, fault injection. (decided: a follow-up)** No fault middleware, no `/faults` and no new fault types in step 13. UC-001's gap "no repository manifest compiles middleware with `cmd:`" stays open for that follow-up.

- **Q12, fidelity. (decided: compatible, quirks fixed)** Same endpoints, paths, methods, payload shapes (with the trailing newline), messages and status codes for every case that works today. The four quirks go:

  | # | Quirk at v3.11.0 | Revision 3 | A pinned suite depends on it? |
  |---|---|---|---|
  | 1 | A failed `/start?config=nope` is remembered, so a later `/restart` or bare `/start` fails too | A failed start leaves the last good configuration remembered | No. The only names at v2.3.0 are `default`, `ssl` and `socket`, and `socket` is excluded by `@unixsocket` in CI |
  | 2 | An unknown configuration answers after the 10 s readiness timeout, as "health check timed out" | It answers at once (Q24 for the code) | No. Every caller checks `200` only |
  | 3 | `/change` rewrites `changing-flag.json` through a narrow struct | No file is rewritten; the in-memory toggle keeps every field | No for RPC and in-process. File mode sees the rendered file (Q21), whose `changing-flag` keeps `state`, `variants` and `defaultVariant` |
  | 4 | `/start?config=nope` cancels a pending restart and kills flagd before it fails | An unknown configuration changes nothing: no pending restart is cancelled, and the gate, the runtime and the remembered configuration stay as they were (Q24) | No. No pinned scenario names an unknown configuration |

  - C pins the four quirks on today's image as **launchpad-only** cases.
  - **Kept, because suites depend on them:**
    - any HTTP method;
    - `/restart`'s immediate answer and its cancellation of a pending restart;
    - `/stop`'s cancellation;
    - **every `/start` cancels a pending `/restart`**, a failed start of a known configuration included. The only exception is an unknown configuration (quirk 4), which changes nothing;
    - the 10 s limit for a start that doesn't get ready;
    - the 5 s limit for `/change`.

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

- **Q21, file-mode providers. (decided by the clarification)**
  - The testbed renders `/flags/allFlags.json` from the in-memory state:
    - at startup;
    - after every `/change`;
    - after every start that cleans and reloads the store.
  - It removes the file while a `/restart` keeps the gate closed.
  - The runtime never reads it back. Every pinned `@file` scenario sees what it sees today: the file at start, gone during the outage, rewritten on `/change`.
  - `render` set to empty turns it off, for a target with no file-mode providers.

- **Q22, `/start` with another configuration. (decided: (a), a full in-process rebuild on a switch; required, because other languages' `ssl` suites need it)**
  - **When it rebuilds:** only when the configuration name changes. A `/start` with the name in use resets and reloads the store (Q3).
  - **On a switch:**
    1. close the gate and cancel the runtime;
    2. build a new runtime from `configs/<name>.json` with `FromConfig` (noop telemetry, Q4) and run it with `Runtime.Run(ctx)`;
    3. once it is ready, render `allFlags.json` (Q25), open the gate and answer `200`.

    The control module stays up throughout, with its in-memory state, so `/change` toggles survive a switch.
  - **What a switch covers:** every setting the runtime fixes when it is built:
    - inbound TLS (`ssl`'s `server-cert-path` and `server-key-path`);
    - endpoint settings (`sync-payload`'s `disable-sync-metadata`);
    - the source list (`metadata`);
    - **outbound TLS** in the same way: a source's own certificate settings, such as a `flagd-grpc` or `http` source's `certPath` or `tls`. A switch to a configuration that names such a source builds the runtime with them. That source's sync provider must then be in the manifest. None of the four pinned configs names one, so Q15's minimal manifest has none; adding one is a manifest line plus `TestTestbedManifestIsMinimal`.
  - **A failed switch** (not ready in 10 s) answers `500 … health check timed out` and leaves the gate closed. The last good configuration stays remembered (quirk 1 fixed), and the next `/start` builds it again.
  - **Tests:** L's `TestFromConfigRepeatedly` (20 cycles, `goleak`, fd count); B1's `TestConfigSwitch` (`default`, `ssl` with TLS on 8013 under the custom root CA, `sync-payload`, `metadata`, back to `default`, with the toggle state kept) and `TestSameNameDoesNotRebuild`.
  - *Rejected:* a child process on a switch; unsupported.

- **Q23, how the testbed source replaces `flags/allFlags.json`. (decided: (a))** The configs stay upstream's (BR-007).
  - `runtime/cmd.LoadConfig`, given a rewrite rule by the testbed `cmd`, swaps the sync provider behind the source `flags/allFlags.json` for the testbed source, and **keeps the URI `flags/allFlags.json`**. Every other source stays as written.
  - **Why the URI stays:** selectors address sources by URI (`source=flags/allFlags.json`), and flags carry their source in the store. With the URI renamed to `testbed:allFlags`, the both-images comparison failed "Flag disabled in one flag set, enabled in another".
  - L's `TestLoadConfigRewriteTouchesOneSource` checks each of the four configs.
  - *Rejected:* edited copies of the configs; the testbed source claiming the `flags/` prefix.

- **Q24, the code for an unknown configuration. (decided: (a))**
  - `/start?config=nope` answers **at once** with `500 {"status":"error","message":"Failed to start flagd: unknown configuration \"nope\""}`, which names the configuration.
  - The gate and the runtime stay as they were, and the last good configuration stays remembered.
  - K's `TestUnknownConfiguration` pins the code, the body and an answer within 100 ms.
  - *Rejected:* `404`; `400`.

- **Q25, where `allFlags.json` is rendered from, and by whom. (decided: (a), the control module after the store confirms)**
  - **The facts:**
    - The store keeps each source's document as the format's parsed value (`store.Document.Value` is opaque), not as bytes.
    - The launchpad writes the deep-merged map with `json.MarshalIndent(map, "", "  ")`: keys sorted, two-space indent, no trailing newline, numbers re-encoded from `float64`.
    - It writes through `atomicWriteFile`: `os.CreateTemp` in the same directory (`.tmp-*`), then write, `chmod 0644`, close and `os.Rename` over `allFlags.json`.
    - A file-mode provider's watcher therefore sees a create of the temp file and a rename, never a partly written `allFlags.json`.
  - **Options:**
    - **(a) The control module renders, triggered by the store's confirmation.** It renders after `Target.Load` or `Target.Start` has returned, which is when the store holds that version. It writes the bytes it encoded with the launchpad's encoder, because they are the bytes the store accepted. One encoder makes both the runtime's input and the file, and nothing runs asynchronously, so `/change` and `/start` answer only once the file is in place, as today.
    - **(b) A small event sink** (UC-020) subscribed to configuration change events for the testbed source (URI `flags/allFlags.json`). It fits the component model, but the pipeline is asynchronous and batched (default flush 1 s). Events carry flag keys and versions, not bodies, so the sink would have to ask for the document anyway, and `/change` would have no completion signal to wait on.
    - **(c) Re-encoding from the store's parsed flags.** It reads the "real" store, but it is not byte-compatible: it loses fields the flag model drops, and it formats numbers and ordering its own way. File-mode providers would parse a different file.
  - **Decided:** (a).
    - **Port the launchpad's code as it is:** the deep merge, `MarshalIndent` with two spaces, and an atomic write that matches `atomicWriteFile`: temp file `.tmp-*` in the target directory, mode `0644`, rename.
    - **Pin the bytes:** C3 records today's image's `allFlags.json`, byte for byte, at start and after one and two `/change` calls; K's `TestRenderMatchesLaunchpad` compares against that golden.
    - **Pin the atomicity:** K's `TestRenderIsAtomic` runs a reader loop during 1000 renders; every read parses, and every read is either the old or the new version.

## 1. Scope

### UC-022: Run Provider Test Suites Against the Testbed (draft for PR S)

- **Primary actor:** Provider Author. **Secondary actor:** CI.
- **Goal:** run a provider's Gherkin suite against the flagd being released, with the outages the suite controls, without changing the suite.
- **Trigger:** a provider suite starts the testbed through the flagd-testbed compose file.
- **Preconditions:** the testbed image is built from `distributions/flagd-testbed`; the flagd-testbed data is the pinned `test-harness`.

**Main success scenario:**
1. CI starts the compose stack with the testbed image.
2. The control module combines the raw flags in memory and builds the runtime from `configs/default.json`, with `flags/allFlags.json` read from the testbed source. When the runtime is ready, the control module renders `allFlags.json` from the in-memory state, and 8013–8015 open. The control API on 8080 is up from the start.
3. The suite calls `/start?config=default` in every scenario. The testbed closes the gate, resets and reloads the store, opens the gate when the runtime is ready again, and answers.
4. The suite evaluates flags over RPC, in-process sync or the rendered file.
5. The suite calls `/change`. The testbed toggles `changing-flag` in the in-memory state, loads the new version into the store, re-renders `allFlags.json` atomically once the store holds it, and answers.
6. The suite calls `/restart?seconds=N`. The testbed answers at once, closes the gate and removes the rendered `allFlags.json`; providers see refused connections, broken streams and a missing file. After N s it starts as in step 3, which renders the file again.
7. The providers reconnect and fire their ready events; the suite passes the same scenarios as against the launchpad image.

**Alternative flows:**
- **A1 Configuration switch:** Q22.
- **A3 Invalid seconds:** `400`.
- **A4 Stop:** the gate stays closed until the next `/start`.
- **A7 Change times out:** plain-text `500`.
- **A8 Not ready in 10 s:** `500 … health check timed out`, gate closed.
- **A9 Unknown configuration:** answered at once (Q24); the last good configuration stays remembered.

**Business rules:**
- **BR-001 Compatible API:** v3.11.0's endpoints, paths, methods, payload shapes, messages and codes, except Q12's four quirks.
- **BR-002 Outages close sockets:** a plain close, no application-level goodbye; the control port stays up.
- **BR-003 Cleaned store per start:** every start empties the store, re-syncs every source, and opens the gate only when every source has been accepted again.
- **BR-004:** the runtime reads no file the testbed changes.
- **BR-005 Minimal:** the testbed contains only the components listed in Q15, each needed by a pinned suite.
- **BR-006 Test-only:** NFR-015.
- **BR-007 Data stays upstream's:** flags, configs, certificates, the compose file and Gherkin are unmodified; Q23's rewrite happens at load time.
- **BR-008 Reusable control:** the control module knows no flagd type; a target supplies the behaviour.
- **BR-009 Rendered output:**
  - `allFlags.json` is rendered from the in-memory state only after the store holds it.
  - It is byte-identical to what the launchpad writes for the same state.
  - It is written atomically with a temp file and a rename.
  - The runtime never reads it.

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
| Q21, Q25, BR-009 | K's `TestRenderLifecycle` (rendered at start, after `/change` and after every start; removed during `/restart`; never before `Target.Load` returns), `TestRenderMatchesLaunchpad` (C3's golden), `TestRenderIsAtomic`; B1's `TestRuntimeNeverReadsRenderedFile` (the rendered path is in no source of the loaded configuration) |
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
| C3 | `TestTestbedContract/files` | `/flags/allFlags.json` exists at start, is gone during a restart, and is back afterwards. Its bytes at start, after one `/change` and after two are recorded as goldens, and compared on both images. (`changing-flag.json` at v3.11.0 has only the three fields the narrow struct keeps, so quirk 3 leaves `allFlags.json` byte-identical.) **Launchpad-only:** the byte layout of `rawflags/changing-flag.json` (quirk 3) |
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
| K | implementer | `main` | `testbed/control/` (module) | Q2's server, `Config` and `Target`; the in-memory state with the launchpad's deep merge and encoder ported as they are; the render with the launchpad's atomic write (Q25); the launchpad texts; Q12's fixes and Q24 | K's tests against a fake target, among them the three render tests; C's api and files cases against a K server with a fake target |
| M | implementer | `main` | `testbed/source/` (module) | The testbed sync provider: a fetcher with scheme `testbed:` that serves the document bytes its owner last loaded, versioned by the given digest. It has an internal trigger its owner fires; the trigger is not registered as a selectable trigger type | `synctest` contract run; `TestLoadFetchesNow`; `TestUnchangedDocumentNotRedelivered`; `TestTriggerNotSelectable` |
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
- **R12, render fidelity.** File-mode providers parse the rendered file unchanged only if its bytes match the launchpad's.
  - **Covered by:**
    - porting the deep merge and `MarshalIndent(…, "", "  ")` as they are, decode into `interface{}` included, so `10.0` becomes `10` as today;
    - C3's golden from today's image and K's `TestRenderMatchesLaunchpad`.
  - **Atomicity:** the temp file is created in the target directory, so the rename never crosses a filesystem, which matters on a bind mount. Providers' watchers see the same create-and-rename pattern as today.
- **R11, containment.** Nothing goes upstream. The ported deep merge is Apache-2.0 code from the same organization, and the file says where it comes from.

## 6. Mapping: launchpad to relay pieces

| Launchpad / testbed piece | Relay piece | PR |
|---|---|---|
| launchpad process, port 8080, its four calls | `R/testbed/control`: a reusable library server driving a `Target`, on its own listener | K |
| flagd binary at `/flagd` | the minimal testbed distribution (Q15) | B1 |
| kill and re-exec on `/start` | gate close, `Runtime.Reset` (store emptied, every source re-synced), gate open when ready | G1, L, B1 |
| `/stop` | gate close | G1, B1 |
| `/restart?seconds=N` | gate close and removal of the rendered file; a start after N s | G1, K, B1 |
| SIGKILL socket behaviour | plain close, no `Shutdown` | G1 |
| every flagd port | `Components.Listen` through the gate | G2 |
| `configs/*.json` | `runtime/cmd.LoadConfig`, with the source `flags/allFlags.json` served by the testbed source under the same URI | L, B1 |
| `rawflags/` combined into `allFlags.json` | combined once in memory with the launchpad's merge and encoder; the bytes are served by the testbed source | K, M |
| `/change` rewriting `changing-flag.json` | in-memory toggle, `Target.Load` (internal trigger, wait on the store), then render | K, M, B1 |
| `allFlags.json` written by the launchpad's `atomicWriteFile` | rendered by the control module from the in-memory state after the store holds it, with the same encoder and the same temp-and-rename write; never read by the runtime | K |
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

### Revision 3, final decisions (Q22–Q25)

| Decision | Change |
|---|---|
| Q22 (a) | A full in-process rebuild only when the configuration name changes; required for other languages' `ssl` suites. Outbound TLS of a source is covered the same way. `TestConfigSwitch` covers all four configs; `TestSameNameDoesNotRebuild` added |
| Q23 (a) | The loader rewrites the `flags/allFlags.json` source; nothing else |
| Q24 (a) | `500` at once, with a message naming the unknown configuration; answered within 100 ms |
| Q25 (a) | Render in the control module after the store confirms, with the launchpad's encoder and atomic write |
| Header | Every question decided; no open list |

### Revision 3, clarification of Q3 and `/change`

| Point | Change |
|---|---|
| `/change` alters the in-memory state | Q9: toggle in the control module's map, `Target.Load` into the store, then render; `Target.Change` replaced by `Target.Load` |
| `allFlags.json` is an output rendered from the in-memory state | Q21 decided; Q25 (new) recommends the control module as renderer, after the store confirms, with the launchpad's encoder and atomic write; BR-009; `export` renamed `render` |
| Re-rendered after every `/change` and start | Q3 step 4; removed during `/restart` for file-mode outages |
| Source files never change | Unchanged: K reads `rawflags/` once |

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
| Q12 quirks fixed | Quirks 1–4 dropped and pinned as launchpad-only; no pinned suite depends on them. Open: Q24 |

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
