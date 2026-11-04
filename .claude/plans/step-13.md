# Step 13 plan: the flagd testbed as a relay-builder distribution (UC-022 Run Provider Test Suites Against the Testbed)

**Status: revision 3.** It applies the project lead's decisions on Q2, Q3, Q4 and Q12 to revision 2. Revision 2 resolved `step-13-opposer.md` (1 blocker, 4 major, 8 minor; section 8). Section 0 holds the questions; Q21–Q24 are new, and they are the ones still open.

**Decisions (project lead).**
- **#110 (2026-11-04), in the PoC:** `distributions/flagd-testbed` with a control endpoint, a listener gate in `service/transport` for stop and start, a fault-injection middleware and a fetch trigger. The provider test suites run against it as the testing harness.
  - Out of scope: restructuring the upstream flagd-testbed repository. Upstream gets nothing: no issues, PRs or comments outside this repository.
- **Q2, the control API is its own configurable module.** The provider TCK and other SDKs' testbeds reuse it.
- **Q3, a restart keeps the runtime.** A start gives a cleaned store, loaded fresh in memory from the sources. The testbed never changes the files the runtime reads.
- **Q4:** the noop telemetry provider.
- **Q12, compatible with the quirks fixed:** the same endpoints, paths and payloads, without the launchpad's quirks.

**Base.** `main` @ c231ac78. Step 10 is merged: operator settings for middleware and event sinks, `dist.components_hint`, the standard manifests, and the `cmd:` entry. Issue: #110. "R" is `github.com/open-feature-forking/openfeature-relay`.

**Drop-in rule for this step.** Today's provider suites run **unchanged** against our testbed:
- the same launchpad endpoints, paths and payloads;
- the same ports, image paths, working directory, volume, healthcheck and compose file;
- outages that close listeners, so connections are refused and streams break;
- a store that holds exactly the sources' flags after every start.

Where an exact match is impossible, section 0 names it.

## What exists (read at the pinned versions)

**Pinned versions.**
- `test/integration` pins `github.com/open-feature/go-sdk-contrib/providers/flagd v0.8.0` and `tests/flagd/v2 v2.3.0`.
- It builds `flagd/build.Dockerfile`, then the testbed image from `test-harness/flagd/Dockerfile` with `FLAGD_BASE_IMAGE`.
- `test-harness` is flagd-testbed **v3.11.0** (9d8dace5).
- The Go suite starts `test-harness/docker-compose.yaml` through testcontainers' compose module, with `IMAGE`, `VERSION` and `FLAGS_DIR`.

**The image (`test-harness/flagd/Dockerfile`):**
- busybox, working directory `/`; `/flagd` is flagd and `/launchpad` the entrypoint;
- `/rawflags/` holds the 11 flag files; `/configs/` the four configs; `/flags/` is mounted from `FLAGS_DIR`;
- `/ssl/` holds the server certificate (signed at build time with `ssl/custom-ca.key`) and the root CA.

**The compose file:**
- **Service `flagd`:** ports 8013–8016 and 8080; `${FLAGS_DIR}:/flags`; healthcheck `wget --spider …:8014/healthz` every 10 s, 3 retries, 30 s start period.
- **Service `envoy`:** ports 9211, 9212 and 9901.
  - 9211 routes `rpc.service` to `flagd:8013` and `sync.service` to `flagd:8015`, with 1 s TCP health checks. These checks are what sees an outage.
  - 9212 answers `403 PERMISSION_DENIED`.
  - It starts once `flagd` is healthy.

**The launchpad (port 8080, any HTTP method; JSON bodies `{"status","message"}` from `json.NewEncoder`, with a trailing newline):**

| Call | Behaviour |
|---|---|
| `/start?config=<name>` | Remembers `<name>` **before** trying (quirk 1). Kills flagd and regenerates `/flags/allFlags.json` if it is missing. Execs `./flagd start --config ./configs/<name>.json` and polls `:8014/readyz` for up to 10 s. `200 … "flagd started successfully"`; otherwise, and for an unknown name too, `500 … "Failed to start flagd: flagd health check timed out"` after 10 s (quirk 2) |
| `/stop` | Cancels a pending restart, kills flagd, `200 … "flagd stopped successfully"` |
| `/restart?seconds=N` | Default 5; anything but a non-negative integer gets `400 … "'seconds' must be a non-negative integer"`. Cancels a pending restart, **deletes `/flags/allFlags.json`**, kills flagd, answers `200 … "flagd will restart in N seconds"` at once, and starts the remembered configuration after N s |
| `/change` | Rewrites `rawflags/changing-flag.json` through a narrow struct with two-space indentation (quirk 3), toggling `changing-flag`'s `defaultVariant` between `foo` and `bar`. Waits up to 5 s for the regenerated `allFlags.json`, then answers `200 … "Default variant successfully changed to '<v>'\n"`; a failure is a plain-text `500` |
| at startup | Combines `rawflags/*.json`, except `selector-*`, by deep merge into `flags/allFlags.json`, and starts `default` |

**Configs:**
- `default`: sources `flags/allFlags.json` (file) and `rawflags/selector-flags.json` (file), plus `context-value` and `strict-numeric-range`;
- `ssl`: `default` plus server certificate and key;
- `sync-payload`: `default` plus `disable-sync-metadata`;
- `metadata`: source `rawflags/selector-flag-combined-metadata.json`.

**How the Go suite (tests/flagd v2.3.0) drives it:**
- every call is a GET; it maps 8013, 8014, 8015, 8080, 9211 and 9212;
- **`/start` once per scenario:** every "a <type> flagd provider" step calls it, with `ssl`/`tls` for those types, `socket` for that one (no `socket.json` exists) and `default` for every other type;
- "the connection is lost for Ns" calls `/restart?seconds=N` (N is 6 or 8, and 1 in one `@grace` case); "the flag was modified" calls `/change`; `/stop` is never called.

**Who reads the flag file directly.** A provider has three modes:
- RPC, which evaluates through 8013;
- in-process, which syncs from 8015;
- file (offline), which reads `FLAGS_DIR/allFlags.json` itself.

Only file mode reads the file. These pinned scenarios are tagged `@file`:
- `evaluation.feature` and `connection.feature` "Connection" read the file once;
- `connection.feature` "Provider reconnection" (`@file @reconnect`) needs the file to vanish and come back across `/restart`;
- `events.feature` (feature-level `@file`) needs that for the ready/error/stale chains, and needs `/change` to rewrite the file for "Flag change event".

The Go suite here runs no `@file` scenario: the RPC and in-process tag sets select `@rpc` or `@in-process`, and there is no file run. Other languages' suites run file mode against the same image with `FLAGS_DIR` mounted.

**Tag sets CI runs today:**
- **RPC:** no outage scenario.
- **In-process:** `@reconnect`, `@events`, `@grace` and `@targetURI` run, all through `/restart`.
- **Both:** exclude `@fractional-v1` and `@fractional-v2` (#47, Q19).

**The relay side:**
- **Listeners:** flag evaluation and flag sync call `net.Listen`; OFREP, the management port and the event collector use `ListenAndServe`; `ConnectService.Serve` (the embedder path) has its own management server.
- **The runtime:**
  - `Runtime.Start()` blocks on signals.
  - `FromConfig` with OpenTelemetry cannot run twice in one process.
  - Nothing clears the store or makes every source fetch again.
- **Sync providers:**
  - `sync/file` is a fetcher. Its default `fileinfo` trigger watches nothing after the file has been gone for about 1 s (revision 2's blocker; Q20).
  - The fetching provider skips a fetch whose version equals the last one (`fetching.go:267`). Its `ReSync` forces a full fetch that always delivers.
- **Middleware chain** (step 6): `h2c → CORS → middleware → instrumentation → handler`. CORS preflights and the management port bypass it.

## 0. Open questions for the project lead

The recommendation comes first. Q21–Q24 are open; the rest are decided, or follow from a decision.

- **Q1, slicing and parallelism. (updated in revision 3)**
  - **Recommendation:** twelve PRs (section 7).
    - **Parallel now, all on `main`:** S (the spec), C (the contract against today's image), G1 (the listener gate), G2 (the listen seam), K (the control module), M (the testbed source and trigger) and F (the fault middleware).
    - **L** (runtime seams: `Run`, `Reset`, `LoadConfig`) stacks on G2.
    - **B1** (the testbed `cmd`, the manifest and the Dockerfile) stacks on G1, L, K and M.
    - **I** (CI) on B1 and C. **B2** (faults) on B1 and F, beside I.
    - **X** (close) comes last.
    - The critical path is G2 → L → B1 → I → X.
  - *Alternative:* fewer, larger PRs; the drop-in proof would wait for faults.

- **Q2, the control API's module. (decided: its own configurable module; shape below)**
  - **Recommendation:** `R/testbed/control`, a **library module, not a relay component type**.
    - **Why no component type:** the component types are what a runtime is built from. The control API sits above the runtime and must outlive it: it closes and opens the runtime's gate, resets its store, and replaces it on a configuration switch (Q22). As a service endpoint it would be created by the very runtime it controls.
    - **The API:** `control.New(cfg control.Config, target control.Target) *control.Server` and `Server.Serve(ctx) error`. `Target` is the interface the server drives:

      ```go
      type Target interface {
          Start(ctx context.Context, config string) error // ready, or an error (500)
          Stop(ctx context.Context) error
          Change(ctx context.Context) (variant string, err error)
          Ready() bool
      }
      ```

      Optional interfaces add the extension calls when the target implements them: `Faulter` (`/faults`) and `Fetcher` (`/fetch`).
    - **The server does what is the same for every target:**
      - the four launchpad calls, their texts and status codes;
      - `/restart`'s delay, and the cancellation of a pending restart by a later call;
      - the timeouts;
      - the file export of Q21.
    - **The testbed `cmd` supplies the flagd target.** The provider TCK, or a Flagsmith or GO Feature Flag testbed, supplies its own target and reuses the server, the API and the contract test unchanged.
    - **Outside the gate:** the server opens its own listener with `net.Listen`, on the address in its `Config`. It is never passed `Components.Listen`, so the gate does not know it. B1's `TestControlPortOutsideGate` checks that 8080 answers while 8013–8016 refuse.
    - **Configuration:** `control.Config` is plain data that a `cmd` fills from flags, environment variables (`TESTBED_*`) or a file. Defaults reproduce v3.11.0:

      | Key | Default |
      |---|---|
      | `address` | `:8080` |
      | `configs` | `/configs` |
      | `rawFlags` | `/rawflags` |
      | `export` | `/flags/allFlags.json` (Q21) |
      | `changingFlag` | `changing-flag`, variants `foo` and `bar` |
      | `startTimeout` | `10s` |
      | `changeTimeout` | `5s` |
      | `defaultRestartSeconds` | `5` |
      | `extensions` | `[faults, fetch]` |

  - *Alternative:* a service endpoint with an "ungated" declaration. It is reusable as a component, but it dies with the runtime on a configuration switch and needs a new kind of declaration in the core.

- **Q3, what restart and start do. (decided: keep the runtime, cleaned store)**
  - **Recommendation:**
    - **Close** (`/stop`, `/restart`): the gate closes abruptly (Q6). The runtime keeps running behind it; nothing is cancelled.
    - **Start** (`/start` with the configuration in use, and `/restart`'s delayed start):
      1. **Reset the store** through the new hook `Runtime.Reset(ctx)` (L). It removes every flag and document of every source and marks every source not yet delivered, so the runtime is not ready.
      2. **Reload:** `Reset` calls `ReSync` on every sync provider: a forced full fetch that bypasses the unchanged-version check and always delivers.
      3. Once every source has been accepted again, the runtime is ready, the gate opens, and `/start` answers `200`.
    - **No file is written, deleted or renamed that the runtime reads, so revision 2's blocker cannot occur:**
      - the testbed's runtime reads the combined flags from the testbed source (Q23), not from `flags/allFlags.json`;
      - the selector and metadata files are read-only.
    - **State kept across a restart.** Everything outside the store survives: the bus, connection-independent counters and the process start time. No pinned scenario observes any of them; the UC lists them. Everything a provider reads comes from the store.
    - **Another configuration** goes to Q22.
  - *Alternative (revision 2):* a fresh runtime per start.

- **Q4, telemetry. (decided: noop telemetry provider)** The manifest is otherwise in lockstep with flagd-full (Q15).

- **Q5:** *superseded by Q3 (revision 3).*

- **Q6, how the gate closes.**
  - **Recommendation:** abrupt, with a plain close.
    - The gate closes the OS listeners; it closes every tracked connection with a plain `Close()`, not `SetLinger(0)`.
    - It never calls `Server.Shutdown`, so no `provider_shutdown` event or GOAWAY is sent.
    - `Accept` blocks while the gate is closed; opening binds the addresses again with `SO_REUSEADDR`.
    - C2 records what idle streams see on today's image, and I compares ours.
  - *Alternative:* graceful, as with SIGTERM; providers would take a path they don't take today.

- **Q7, what the gate covers.**
  - **Recommendation:** every listener the runtime opens: flag evaluation, OFREP, flag sync, the management port, the event collector and unix sockets. Not the control port.
    - Envoy's 1 s checks see the outage as today. Docker's 10 s healthcheck doesn't notice 8 s, as today.
    - An extension endpoint outside the seam is not gated.
  - *Alternative:* service endpoints only; `/readyz` would answer during an outage.

- **Q8, the listen seam.**
  - **Recommendation:** `service.Settings.Listen`, filled from `distribution.Components.Listen`, defaulting to `net.Listen`.
    - flag evaluation, flag sync, OFREP, the event collector and the management port use it.
    - `ConnectService.Serve`'s embedder-path management server is the named exception.
    - flagd-full is unchanged.
  - *Alternative:* a package-level hook.

- **Q9, `/change`. (rewritten in revision 3)**
  - **Recommendation:** an in-memory switch, through the testbed source and the fetch trigger.
    - At startup the control module combines `rawflags/*.json` (except `selector-*`) in memory, by the launchpad's deep merge. The files are read once and never written.
    - `/change` toggles `changing-flag`'s `defaultVariant` between `foo` and `bar` in that in-memory document, keeping every other field. The document version changes.
    - It fires the `testbed` fetch trigger on the testbed source. The source's fetch returns the new document, the store accepts it, and the change events go out as for any source.
    - It answers `200 … "Default variant successfully changed to '<v>'\n"` once the store holds the new variant, within 5 s. Otherwise it answers the plain-text `500`.
    - The toggle survives `/restart` and `/start`, as the rewritten file does today: `Reset` reloads from the same in-memory document.
    - File-mode providers see the change only through Q21.
  - *Alternative:* point the source at one of two pre-built files, one per variant. That needs a source whose path can change at run time, and it is still a file switch.

- **Q10, the fetch trigger.** It is now on the main path: Q9 fires it.
  - **Recommendation:** the trigger type `testbed` lives in module `R/testbed/source`, beside the testbed source (M). It is the testbed source's default trigger, and any fetcher source may name it. `POST /fetch?source=<uri>` fires it for new scenarios.
  - *Alternative:* the source notifies itself without a trigger. Smaller, but the decision names a fetch trigger, and other sources could not be refreshed on demand.

- **Q11, the fault middleware.**
  - **Recommendation:** module `R/testbed/fault`, in the manifest and configured by the testbed `cmd`. It passes everything until a fault is set.
    - `POST /faults` (`endpoint`, `path`, `kind`, `probability`), with the kinds `latency`, `status`, `abort` and `refuse-all`.
    - `GET /faults` lists them; `DELETE /faults` clears them, and so does every `/start`.
    - CORS preflights and the management port are outside the chain.
    - The control module serves `/faults` when the target implements `Faulter`.
  - *Alternative:* faults only through the configuration file.

- **Q12, fidelity. (decided: compatible, quirks fixed)**
  - **Recommendation:** same endpoints, paths, methods, payload shapes (with the trailing newline), messages and status codes for every case that works today. The three quirks go:

    | # | Quirk at v3.11.0 | Revision 3 | A pinned suite depends on it? |
    |---|---|---|---|
    | 1 | `/start?config=nope` is remembered before it fails, so a later `/restart` or bare `/start` fails too | A failed start leaves the last good configuration remembered | No. No Go scenario at v2.3.0 names an unknown configuration; the only names are `default`, `ssl` and `socket`, and `socket` is excluded by `@unixsocket` in CI |
    | 2 | An unknown configuration answers after the 10 s readiness timeout, as "health check timed out" | It answers at once with `500 {"status":"error","message":"Failed to start flagd: unknown configuration \"nope\""}`. Same shape and code, a truthful message (Q24) | No. Every caller checks `200` only. A suite that named `socket` would get its `500` at once instead of after 10 s; that suite is excluded today |
    | 3 | `/change` rewrites `changing-flag.json` through a narrow struct, dropping other fields and re-indenting | No file is rewritten. The in-memory toggle keeps every field | No for RPC and in-process: they see `changing-flag` through flagd. File mode sees the exported file (Q21), whose `changing-flag` has the same `state`, `variants` and `defaultVariant` |

    - C pins quirks 1–3 on today's image as **launchpad-only** cases. I runs every other contract case against both images, and these three against ours with the fixed behaviour.
    - **Kept, because suites depend on them:**
      - any HTTP method (GET is what the suite uses; the upstream README says POST);
      - `/restart`'s immediate answer and its cancellation of a pending restart;
      - `/stop` cancelling a pending restart;
      - the 10 s limit for a start that doesn't get ready;
      - the 5 s limit for `/change`.
  - *Alternative:* not offered; this is decided.

- **Q13, the image contract.**
  - **Recommendation:** `distributions/flagd-testbed/Dockerfile` (repository root as context):
    - `relay-builder` builds the binary;
    - upstream's certificate stage; busybox with `WORKDIR /`;
    - `/flagd` as the entrypoint; `/rawflags/` and `/configs/` from `test-harness`; `/flags/` as the volume; `/ssl/`;
    - ports 8013–8016 and 8080; not published.
  - *Alternative:* swap only the binary in upstream's Dockerfile.

- **Q14, CI.**
  - **Recommendation:** `TestRPC` and `TestInProcess`, unchanged, against today's launchpad image and the relay testbed. CI fails if the pass sets differ, or if ours fails a scenario the launchpad image passes.
    - The relay testbed builds for any Go change outside `docs/` and `.claude/`, for the upgrade differential's paths, and with `FULL_CI` or a manual run.
    - Other languages' suites, including their file mode, run manually in the PoC.
  - *Alternative:* the relay testbed only.

- **Q15, lockstep.**
  - **Recommendation:** the manifest lists:
    - flagd-full's sync providers, configuration format, evaluator and service endpoints;
    - the noop telemetry provider; the testbed source and trigger (M); `cmd: R/testbed/cmd`; B2 adds the fault middleware;
    - `components_hint: false`.

    `TestTestbedLockstepWithFlagdFull` checks the four kinds.
  - *Alternative:* no check.

- **Q16, RPC outage scenarios.**
  - **Recommendation:** gating tag sets unchanged; a manual `make testbed-rpc-outages` target against both images.
  - *Alternative:* none.

- **Q17, the use case and its requirements.**
  - **Recommendation:** section 1's UC-022.
    - Primary actor: Provider Author. Secondary actor: CI.
    - New requirements:
      - FR-056: launchpad-compatible control API as a reusable module;
      - FR-057: outages close listeners; every start reloads a cleaned store;
      - FR-058: fault injection;
      - FR-059: testbed source and fetch trigger;
      - NFR-014: drop-in: unchanged suites pass the same scenarios;
      - NFR-015: testbed components are never in a standard distribution.
  - *Alternative:* fold into UC-013.

- **Q18, an evaluation-recording sink.**
  - **Recommendation:** a follow-up, not in step 13.
  - *Alternative:* add it now with `GET /evaluations`.

- **Q19, #47's fractional scenarios.**
  - **Recommendation:** #47 needs not land first. The `@fractional-v1` and `@fractional-v2` exclusions stay in `test/integration`, owned by #47, whose definition of done gains removing them there too. Step 13 adds no exclusion.
  - *Alternative:* land #47 first.

- **Q20, flagd-full's file source after a deletion.** The `fileinfo` and `fsnotify` triggers stop watching a file gone for more than about 1 s.
  - **Recommendation:** keep it in step 13. Open an issue here about an opt-in that keeps polling. Revision 3 no longer depends on it, because the runtime reads no file the testbed changes.
  - *Alternative:* change the triggers now.

- **Q21, file-mode providers. (new in revision 3; open)**
  - **The constraint:** file mode reads `FLAGS_DIR/allFlags.json` directly. Its `@file` scenarios need three things:
    - the file at start (evaluation, connection);
    - the file gone during `/restart` and back afterwards (`@reconnect`, the event chains);
    - the file rewritten on `/change` ("Flag change event").

    The decision says the testbed never changes files. The Go suite runs no `@file` scenario, but other languages do, against this image.
  - **Options:**
    - **(a) Export file, not read by the runtime.** The control module writes `/flags/allFlags.json` as the launchpad does: written at startup and on `/change`, deleted on `/restart`, recreated on start. The runtime never reads it (Q23), so revision 2's blocker cannot return. "Never changes files" becomes "never changes a file the runtime reads".
    - **(b) No file mode.** The testbed writes nothing. `@file` suites keep using the launchpad image, and the UC states that.
    - **(c) A static export.** Written once at startup. The `@file` evaluation scenarios pass; `@file @reconnect`, the event chains and "Flag change event" fail and are excluded for file runs.
  - **Recommendation:** (a). It keeps every pinned `@file` scenario working, and it is off when `export` is empty.

- **Q22, `/start` with another configuration. (new in revision 3; open)** `ssl`, `sync-payload` and `metadata` change TLS, sources and endpoint settings, which a running runtime cannot change. The pinned Go suites in CI use `default` only; other languages use `ssl` (`@customCert`).
  - **Options:**
    - **(a) Rebuild on a switch only.** Close the gate, cancel the runtime, build a new one from the other file, open the gate when ready. It needs `FromConfig` reentrant with noop telemetry for a handful of switches per run (L: 20 cycles, leak checks).
    - **(b) A child process on a switch.** The testbed `cmd` re-executes itself with the new configuration; the control module stays in the parent.
    - **(c) Unsupported.** `/start` with another name answers `500 "configuration switch not supported"`; `ssl` suites keep the launchpad image.
  - **Recommendation:** (a). It keeps one process and one gate, and the reentrancy it needs is small.

- **Q23, how the testbed source replaces `flags/allFlags.json` in the configs. (new in revision 3; open)** The configs, which stay upstream's (BR-007), name `flags/allFlags.json` as a file source. Under Q3 the runtime must read the in-memory document instead.
  - **Options:**
    - **(a) The loader rewrites one source.** `runtime/cmd.LoadConfig`, called by the testbed `cmd` with a rewrite rule, turns the file source `flags/allFlags.json` into `testbed:allFlags`. Every other source stays as written.
    - **(b) The testbed ships its own configs:** copies of upstream's with that one line changed. This breaks BR-007, and the copies drift from upstream.
    - **(c) The testbed source claims the `flags/` prefix.** That clashes with the file sync provider, which claims bare paths (UC-001's scheme clash rule).
  - **Recommendation:** (a). A test pins that the rewrite touches exactly that source in each of the four configs.

- **Q24, the unknown-configuration status code. (new in revision 3; open)** Quirk 2 goes, so `/start?config=nope` answers at once. With what?
  - **Options:**
    - **(a) `500`, the launchpad's code for every failed start, with a truthful message.**
    - **(b) `404`, with the same JSON shape.**
    - **(c) `400`, as for bad `seconds`.**
  - **Recommendation:** (a). The decision keeps payloads compatible, and a client that branches on the code sees no new value.

## 1. Scope

### UC-022: Run Provider Test Suites Against the Testbed (draft for PR S)

- **Primary actor:** Provider Author. **Secondary actor:** CI.
- **Goal:** run a provider's Gherkin suite against the flagd being released, with outages and faults the suite controls, without changing the suite.
- **Trigger:** a provider suite starts the testbed through the flagd-testbed compose file.
- **Preconditions:** the testbed image is built from `distributions/flagd-testbed`, and the flagd-testbed data is the pinned `test-harness`.

**Main success scenario:**
1. CI starts the compose stack with the testbed image.
2. The control module combines the raw flags in memory, writes the export file (Q21) and builds the runtime from `configs/default.json`, with `flags/allFlags.json` read from the testbed source. Once the runtime is ready, it opens 8013–8016. The control API on 8080 is up from the start.
3. The suite calls `/start?config=default` in every scenario. The testbed closes the gate, resets and reloads the store, opens the gate when the runtime is ready again, and answers.
4. The suite evaluates flags over RPC, in-process sync, OFREP or the exported file.
5. The suite calls `/change`. The testbed toggles `changing-flag` in memory, fires the trigger, rewrites the export, waits until the store holds the new variant, and answers.
6. The suite calls `/restart?seconds=N`. The testbed answers at once, closes the gate and removes the export; providers see refused connections and broken streams. After N s it starts as in step 3.
7. The providers reconnect and fire their ready events; the suite passes the same scenarios as against the launchpad image.

**Alternative flows:**
- **A1 Configuration switch:** Q22.
- **A3 Invalid seconds:** `400`.
- **A4 Stop:** the gate stays closed until the next `/start`.
- **A5 Fault injected:** `/faults`.
- **A6 Fetch triggered:** `/fetch`.
- **A7 Change times out:** plain-text `500`.
- **A8 Not ready in 10 s:** `500 … health check timed out`, gate closed.
- **A9 Unknown configuration:** `500` at once (Q24); the last good configuration stays remembered.

**Business rules:**
- **BR-001 Compatible API:** the four calls keep v3.11.0's endpoints, paths, methods, payload shapes, messages and codes, except the three quirks of Q12.
- **BR-002 Outages close sockets:** a plain close, no application-level goodbye; the control port stays up.
- **BR-003 Cleaned store per start:** every start empties the store, reloads every source with a forced fetch, and opens the gate only when every source has been accepted again.
- **BR-004 The runtime reads no file the testbed changes.**
- **BR-005 Faults are opt-in and additive.**
- **BR-006 Test-only:** NFR-015.
- **BR-007 Lockstep** with flagd-full's sync providers, configuration format, evaluator and service endpoints.
- **BR-008 Data stays upstream's:** flags, configs, certificates, the compose file and Gherkin are unmodified; the one source rewrite of Q23 happens at load time.
- **BR-009 Reusable control:** the control module knows no flagd type; a target supplies the behaviour.

**Realizes:**

| Requirement | Delivered by |
|---|---|
| FR-056 | K (module), B1 (flagd target); C and I (contract) |
| FR-057 | G1, G2, L (`Reset`); B1 |
| FR-058 | F, B2 |
| FR-059 | M, B1 |
| NFR-014 | I |
| NFR-015 | B1 |
| UC-001's gap "no repository manifest compiles middleware with `cmd:`" | B2 |

**Use case coverage:**

| Flow | Test |
|---|---|
| main 1–7, BR-001, NFR-014 | I: `TestRPC` and `TestInProcess` against both images; `TestTestbedContract` (C) against ours |
| main 3, BR-003 | L's `TestResetEmptiesAndReloads` (every source re-accepted, versions unchanged still delivered via `ReSync`, readiness false until then); B1's `TestStartReloadsCleanedStore` (a flag removed from the in-memory document by a test seam disappears after `/start`) |
| main 5, BR-004 | B1's `TestChangeThroughTrigger` (store holds the new variant; no file under `/rawflags` or `/configs` changes: mtime and content checked), `TestChangeSurvivesRestart`; the blocker's sequence `TestChangeRestartChange` |
| main 6, BR-002 | G1's gate tests; B1's `TestRestartClosesEveryRuntimePort`, `TestControlPortOutsideGate` |
| A1 | B1's `TestConfigSwitch` (Q22) |
| A3, A7, A8, A9 | C (launchpad-only for quirk 2) and I; K's `TestChangeTimesOut`, `TestStartNotReadyIn10s`, `TestUnknownConfiguration`, `TestFailedStartKeepsLastGoodConfiguration` against a fake target |
| A4 | K's `TestStopUntilStart`; C |
| A5, BR-005 | F's tests; B2's `TestFaultsClearedOnStart`, `TestNoFaultPassesThrough` |
| A6 | M's trigger tests; B1's `TestFetchCallFiresTrigger` |
| Q21 export | K's `TestExportLifecycle` (written at start and on `/change`, removed on `/restart`, recreated on start); C3's byte comparison of the combined document, apart from quirk 3 |
| BR-006, BR-007 | `TestStandardManifestsHaveNoTestbedModule`, `TestTestbedLockstepWithFlagdFull` |
| BR-008 | `TestImageDataMatchesSubmodule`; L's `TestLoadConfigRewriteTouchesOneSource` |
| BR-009 | K's tests run against a fake target only; boundary row: `testbed/control` imports no flagd or relay module except the core's logger |

**Leaves out:**
- restructuring upstream flagd-testbed;
- other languages' suites in CI;
- an evaluation-recording sink (Q18);
- tracing (#59);
- `@fractional-v1` and `@fractional-v2` (#47);
- flagd-full's file trigger (Q20);
- publishing the image.

## 2. Characterization (PR C, base `main`, parallel now)

The oracle is today's image, built as `test/integration` builds it. The contract test is a Go module of its own (`test/testbed`), parameterized by image; cases marked **launchpad-only** pin a quirk Q12 drops.

| Pin | Test | Pins |
|---|---|---|
| C1 | `TestTestbedContract/api` | For the four calls, with GET and POST: code, `Content-Type`, exact body with the trailing newline. `/restart?seconds=x` and `-1`. `/change` three times. **Launchpad-only:** `/start?config=nope` takes at least 10 s with "health check timed out" (quirk 2), and the remembered `nope` makes the next `/restart?seconds=0` and bare `/start` fail too (quirk 1). These run last and recover with `default` |
| C2 | `TestTestbedContract/outage` | During `/stop` and `/restart?seconds=3`: 8013–8016 refuse and 8080 answers. The error an idle `EventStream` (8013) and `SyncFlags` stream (8015) see is recorded, with no `provider_shutdown` before it. After the delay `/readyz` is 200 and both streams reopen |
| C3 | `TestTestbedContract/files` | `/flags/allFlags.json` exists at start, is gone during a restart, and is back afterwards. After each `/change` its `changing-flag` has the toggled `defaultVariant`. **Launchpad-only:** the byte layout of `rawflags/changing-flag.json` (quirk 3) |
| C4 | `TestTestbedContract/configs` | `ssl` serves TLS with the custom root CA; `sync-payload` syncs without metadata; `metadata` serves only the metadata flag set |
| C5 | `TestTestbedContract/change-after-restart` | `/change`, `/restart?seconds=8`, `/change`: an RPC evaluation of `changing-flag` follows both toggles |

## 3. Steps

Every commit builds and passes `make test`. G2 and L must be invisible to flagd-full.

| PR | Model | Base | Files | Change | Check |
|---|---|---|---|---|---|
| S | mechanic | `main` | `docs/use_cases/UC-022-*.md`, `docs/requirements.md`, `docs/use_cases.puml`, `docs/glossary.md` (Testbed, Launchpad API, Listener Gate, Control Target) | Section 1 | spec lint |
| C | implementer | `main` | `test/testbed/`, `Makefile` `testbed-contract` | Section 2 | green against the launchpad image |
| G1 | implementer | `main` | `service/transport/gate/` | Virtual listeners, `Open`, `Close` with a plain close, address release | the gate tests; `-race`; 100 reopen cycles |
| G2 | implementer | `main` | `core/pkg/service`, `core/pkg/distribution`, `runtime`, the four endpoint modules | Q8 | existing pins unchanged; `TestListenSeamUsedByEveryEndpoint`; `make check-flagd-full`; acceptance; upgrade differential; allocs/op |
| K | implementer | `main` | `testbed/control/` (module) | Q2's server, `Config`, `Target` with `Faulter`/`Fetcher`; the in-memory combiner (ported deep merge); the export of Q21; the launchpad texts; quirks fixed (Q12, Q24) | K's tests in section 1 against a fake target; C's contract test run against a K server with a fake target, except the outage and configs cases |
| M | implementer | `main` | `testbed/source/` (module) | The testbed sync provider (fetcher, scheme `testbed:`; fetches a document from a `Store` it is given, versioned by digest) and the trigger type `testbed` (its default trigger) | `synctest` contract run; `TestTriggerFetchesNow`; `TestUnchangedDocumentNotRedelivered` |
| F | implementer | `main` | `testbed/fault/` (module) | Q11 | `middlewaretest.Run`, `TestFaultKinds`, `TestNoFaultPassesThrough` |
| L | implementer | G2 | `runtime` (`Runtime.Run(ctx)`; `Runtime.Reset(ctx)`: removes every source's documents and flags in one store transaction, clears per-source readiness, calls `ReSync` on every sync provider; `Start` unchanged), `runtime/cmd` (`LoadConfig(path, components, rewrites)` over a private viper) | Q3, Q22 (a), Q23 (a) | `TestRunStopsOnContext`; `TestResetEmptiesAndReloads`; `TestResetUnderLoad` (resets while evaluations run: every answer is a flag or `FLAG_NOT_FOUND`, never a panic); `TestFromConfigRepeatedly` (20 cycles, `goleak`, fd count); `TestLoadConfigMatchesStart` and `TestLoadConfigRewriteTouchesOneSource` for each `test-harness` config; `start_config.golden` unchanged |
| B1 | implementer | G1 + L + K + M | `testbed/cmd/` (module: the flagd `Target`, wiring the gate, `Reset`, the testbed source over K's in-memory document, the trigger, `LoadConfig` with Q23's rewrite, Q22's switch), `distributions/flagd-testbed/manifest.yaml`, `Dockerfile` (`WORKDIR /`), `test/distributions` tests, boundary rows | Q3, Q6, Q7, Q9, Q10, Q13, Q15, Q21–Q23 | B1's tests in section 1; the manifest builds; `docker build` in WSL; C's contract against the built image |
| B2 | implementer | B1 + F | `testbed/cmd` (`Faulter`), the manifest entry for `testbed/fault` | Q11 | B2's tests |
| I | implementer | B1 + C | `test/integration` (image by environment variable; default unchanged), `test/testbed` (second image), `.github/workflows/build.yaml`, `Makefile` (`testbed-rpc-outages`) | Q14, Q16 | both images pass `TestRPC` and `TestInProcess` with today's tags; pass sets equal; contract against both, with launchpad-only cases on the launchpad image only |
| X | mechanic | I + B2 | UC-022 status, `docs/relay/testbed.md` (including how the TCK and other testbeds plug in a `Target`), UC-001 gap, requirement statuses, #110, the issue of Q20 | Close | spec lint |

`test/integration`'s suites are not edited; only the image under them changes.

## 4. Gates

In WSL from the repository root (`export GOTOOLCHAIN=go1.27.3`; in a worktree `export GOWORK=$PWD/go.work`; for lint `export GOPATH=$(go env GOPATH)`).

| Gate | Command | PRs |
|---|---|---|
| Unit tests | `make workspace-init && make test` | all |
| Lint, boundaries | `make lint check-module-boundary check-package-coverage`. New rows: `R/testbed/...` is imported only by `testbed/*`, `test/*` and `distributions/flagd-testbed`; `testbed/control` imports nothing of flagd's | all |
| flagd-full unchanged | `make check-flagd-full`, the upgrade differential, `make flagd-integration-test` | G2, L |
| Benchmarks | CI's allocs/op comparison (`Reset` is off the request path) | G2, L, F |
| Testbed contract | `make testbed-contract IMAGE=<image>` | C, K, B1, I |
| Provider suites | `make flagd-integration-test` with `TESTBED_IMAGE=relay` and with the default | I |
| Spec | `spec_lint.py --docs docs` on Windows | S, X |

## 5. Risks

- **R1, store reset correctness (the biggest).** `Reset` must leave no flag of the old state in place, never expose a half-loaded store to a client, and re-accept every source even when its content is unchanged.
  - **Covered by:**
    - the gate, which is closed for the whole reset;
    - `ReSync`'s forced fetch, which bypasses the unchanged-version check (`fetching.go:267`);
    - per-source readiness cleared before the reload;
    - L's `TestResetEmptiesAndReloads` and `TestResetUnderLoad`.
  - **Streaming sources:** a source whose `ReSync` does not deliver would never become ready again. The testbed's configs use only file sources and the testbed source. `Reset` fails with the source's name after the start timeout rather than hanging.
- **R2, gate fidelity against SIGKILL.** Providers distinguish `UNAVAILABLE`, `CANCELLED`, EOF and reset. The plain close matches a killed process. C2 records idle-stream errors on today's image; I compares ours. The in-process `@reconnect`, `@events` and `@grace` scenarios are the acceptance.
- **R3, the runtime survives a restart.** State outside the store is kept: the bus, the process start time, the in-process caches of the endpoints. No pinned scenario observes it. The pass-set comparison is the alarm, and the UC lists the state. An OFREP ETag after a reset is derived from the reloaded documents. For unchanged content it is the same value as before, as with a fresh process.
- **R4, timing.** A start now costs a reset and a reload, well under a second, against the launchpad's process start. Recovery after `/restart?seconds=N` is a little faster than today, never slower, so `@grace` margins only grow.
- **R5, the testbed source replaces flagd-full's file source for `allFlags.json`.** The runtime no longer exercises `sync/file` for that document. `rawflags/selector-flags.json` and the metadata source still go through `sync/file`. flagd-full's own file path stays covered by `make flagd-integration-test`'s launchpad image (I keeps it) and by UC-003's pins.
- **R6, G2 and L touch flagd-full.** Bind errors, sockets, TLS and `Start`'s signals must stay byte-identical; the existing pins and the upgrade differential cover them.
- **R7, reuse pressure on K.** A `Target` interface designed for flagd only would not fit the TCK or a vendor testbed. K's tests run against a fake target; BR-009's boundary row keeps flagd types out. X documents the interface with a sketch for a second target.
- **R8, CI time.** About 8 minutes more in `integration-test` when Q14's filter matches.
- **R9, upstream drift.** A new launchpad call upstream fails the differential until the control module implements it.
- **R10, containment.** Nothing goes upstream. The ported deep merge is Apache-2.0 code from the same organization, and the file says where it comes from.

## 6. Mapping: launchpad to relay pieces

| Launchpad / testbed piece | Relay piece | PR |
|---|---|---|
| launchpad process, port 8080, its four calls | `R/testbed/control`: a reusable library server driving a `Target`, its own listener | K |
| flagd binary at `/flagd` | the testbed distribution (flagd-full's components, noop telemetry, testbed source and trigger, `cmd`) | B1 |
| kill and re-exec on `/start` | gate close, `Runtime.Reset` (store emptied, every source re-synced), gate open when ready | G1, L, B1 |
| `/stop` | gate close | G1, B1 |
| `/restart?seconds=N` | gate close and export removal; after N s, a start | G1, K, B1 |
| SIGKILL socket behaviour | plain close of listeners and connections, no `Shutdown` | G1 |
| every flagd port | `Components.Listen` through the gate | G2 |
| `configs/*.json` | `runtime/cmd.LoadConfig`, with `flags/allFlags.json` rewritten to `testbed:allFlags` | L, B1 |
| `rawflags/` combined into `allFlags.json` | combined once in memory; served by the testbed source | K, M |
| `/change` rewriting `changing-flag.json` | in-memory toggle, `testbed` fetch trigger, wait on the store | K, M, B1 |
| `/flags/allFlags.json` for file-mode providers | the export (Q21), never read by the runtime | K |
| a configuration switch | Q22 | L, B1 |
| `/ssl` certificates | the same certificate stage | B1 |
| compose file, envoy, volume, healthcheck | unchanged, from `test-harness` | — |
| (new) faults | `testbed/fault`, `/faults` | F, B2 |
| (new) on-demand fetch | `/fetch` | M, B1 |

## 7. PR split

| PR | Branch | Base | Runs |
|---|---|---|---|
| S | straight to `main` (spec) | `main` | now |
| C | `uc-022/pr-1-testbed-contract` | `main` | now |
| G1 | `uc-022/pr-2-listener-gate` | `main` | now |
| G2 | `uc-022/pr-3-listen-seam` | `main` | now |
| K | `uc-022/pr-4-control-module` | `main` | now |
| M | `uc-022/pr-5-testbed-source` | `main` | now |
| F | `uc-022/pr-6-fault-middleware` | `main` | now |
| L | `uc-022/pr-7-runtime-reset` | G2 | once G2's branch exists |
| B1 | `uc-022/pr-8-testbed-distribution` | G1 + L + K + M | once those branches exist |
| B2 | `uc-022/pr-9-faults` | B1 + F | after B1, beside I |
| I | `uc-022/pr-10-suites-in-ci` | B1 (+ C merged) | after B1 |
| X | `uc-022/pr-11-close` | I + B2 | last |

Every PR description starts with `Part of #110`.

## 8. Changelog

### Revision 3 (the project lead's decisions)

| Decision | Change |
|---|---|
| Q2: own configurable module | `R/testbed/control`: a library with `Config`, `Target` and the optional `Faulter` and `Fetcher`. Not a component type; outside the gate by its own listener. New PR K; BR-009 |
| Q3: keep the runtime, cleaned store | `Runtime.Reset` (store emptied, readiness cleared, `ReSync` on every source); no runtime reads a file the testbed changes; `/change` toggles an in-memory document and fires the `testbed` trigger on the testbed source (M). Revision 2's per-start rebuild and its 200-cycle test are gone; reentrancy is needed only for Q22. New open questions Q21 (file mode), Q22 (configuration switch), Q23 (source rewrite) |
| Q4: noop telemetry | Unchanged recommendation, now decided |
| Q12: compatible, quirks fixed | Quirks 1–3 dropped; C pins them as launchpad-only; no pinned suite depends on them. New open question Q24 (the code for an unknown configuration) |

### Revision 2 (`step-13-opposer.md`)

| Finding | Severity | Outcome in revision 3 |
|---|---|---|
| 1 A gate-only restart leaves `allFlags.json` unwatched | blocker | Removed at the root: the runtime reads no file the testbed changes (Q3, Q23); C5 and `TestChangeRestartChange` pin the scenario |
| 2 `/start` once per scenario | major | Kept in "What exists"; each start is a reset, well under a second (R4) |
| 3 No `SetLinger(0)` | major | Kept (Q6) |
| 4 A7 and A8 testable; A2 merged | major | Kept. A9 is new for the unknown configuration (Q12, Q24); K tests A7–A9 against a fake target |
| 5 Split B; L on G2 | major | Kept: B1/B2, L on G2 |
| 6 Remembered failed configuration | minor | Dropped by decision (quirk 1); pinned as launchpad-only |
| 7 File sources take triggers; `/change` wait | minor | Kept; `/change` now waits on the store after the trigger |
| 8 `/change`'s narrow struct | minor | Dropped by decision (quirk 3); pinned as launchpad-only |
| 9 `WORKDIR /` | minor | Kept (Q13) |
| 10 Manual RPC outage target | minor | Kept (Q16) |
| 11 Change filter | minor | Kept (Q14) |
| 12 Second listener in flag evaluation | minor | Kept (Q8) |
| 13 Envoy 9901; healthcheck cadence | minor | Kept ("What exists", Q7) |
