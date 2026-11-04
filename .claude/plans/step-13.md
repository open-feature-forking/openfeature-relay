# Step 13 plan: the flagd testbed as a relay-builder distribution (UC-022 Run Provider Test Suites Against the Testbed)

**Status: revision 1, not opposed yet.** Section 0 holds the questions for the project lead, each with a recommendation.

**Decision (2026-11-04, issue #110): in the PoC.**
- **In scope:** `distributions/flagd-testbed` with a control endpoint, a listener gate in `service/transport` for stop and start, a fault-injection middleware and a fetch trigger. The provider test suites run against it as the testing harness.
- **Out of scope:** restructuring the upstream flagd-testbed repository into data and Gherkin files only. Upstream gets nothing: no issues, PRs or comments outside this repository.

**Base.** `main` @ 7c1fb7e6. Step 10 is merged: operator settings for middleware and event sinks (`Components.MiddlewareFactories`, `EventSinkFactories`, `runtime/cmd/extensions.go`), `dist.components_hint`, the standard manifests, and the `cmd:` entry used by `test/distributions/example` and `sync-vendor`. Issue: #110. "R" is `github.com/open-feature-forking/openfeature-relay`.

**Drop-in rule for this step.** Today's provider suites run **unchanged** against our testbed. That means:
- the same launchpad API, the same ports, the same image contract (paths, volume, healthcheck) and the same compose file;
- outages that close listeners, so connections are refused and streams break.

Where an exact match is impossible, section 0 names it as a question.

## What exists (read at the pinned versions)

**Pinned versions.**
- `test/integration` pins `github.com/open-feature/go-sdk-contrib/providers/flagd v0.8.0` and `tests/flagd/v2 v2.3.0`.
- It builds `flagd/build.Dockerfile`, then the testbed image from `test-harness/flagd/Dockerfile` with `FLAGD_BASE_IMAGE` set to that flagd image.
- `test-harness` is the flagd-testbed submodule at **v3.11.0** (9d8dace5).
- The Go suite starts `test-harness/docker-compose.yaml` through testcontainers' compose module, with `IMAGE`, `VERSION` and `FLAGS_DIR` set.

**The image (`test-harness/flagd/Dockerfile`):**
- busybox base;
- `/flagd` is the flagd binary; `/launchpad` is the entrypoint;
- `/rawflags/` holds the 11 flag files of `flags/`; `/configs/` holds `launchpad/configs/*.json`; `/flags/` is created empty and mounted from `FLAGS_DIR`;
- `/ssl/server-cert.pem` and `server-key.pem` are generated at build time and signed by `ssl/custom-root-cert.crt`; `/ssl/custom-root-cert.crt` is copied in.

**The compose file:**
- **Service `flagd`:** the image; ports 8013, 8014, 8015, 8016 and 8080; the volume `${FLAGS_DIR}:/flags`; healthcheck `wget --spider http://localhost:8014/healthz`.
- **Service `envoy`:**
  - port 9211 routes by authority (`rpc.service` to `flagd:8013`, `sync.service` to `flagd:8015`) with TCP health checks every 1 s;
  - port 9212 answers every request with gRPC `403 PERMISSION_DENIED`;
  - it starts once `flagd` is healthy.

**The launchpad (port 8080, `net/http` mux, any HTTP method):**

| Call | Behaviour |
|---|---|
| `/start?config=<name>` | Cancels a pending restart, kills the running flagd (SIGKILL, waits up to 5 s), regenerates `/flags/allFlags.json` if it is missing and restarts the file watcher. Then runs `./flagd start --config ./configs/<name>.json` (empty means the last config, initially `default`) and polls `:8014/readyz` every 100 ms for up to 10 s. Answers `200 {"status":"success","message":"flagd started successfully"}`; otherwise `500 {"status":"error",…}` and stops flagd |
| `/stop` | Cancels a pending restart, kills flagd. `200 {"status":"success","message":"flagd stopped successfully"}` |
| `/restart?seconds=N` | Default 5; a value that is not a non-negative integer gets `400 {"status":"error","message":"'seconds' must be a non-negative integer"}`. Cancels a previous pending restart, **deletes `/flags/allFlags.json`** and kills flagd. Answers `200 "flagd will restart in N seconds"` at once, and starts with the last config after N s |
| `/change` | Toggles `changing-flag`'s `defaultVariant` between `foo` and `bar` in `rawflags/changing-flag.json` (atomic rename). Waits up to 5 s until its watcher has regenerated `allFlags.json`, then answers `200 "Default variant successfully changed to '<v>'\n"`. On failure it answers a plain-text `500` (`http.Error`) |
| at startup | Combines `rawflags/*.json`, except `selector-*`, by deep merge into `flags/allFlags.json`; watches `rawflags/` and recombines on every change; starts flagd with `default` |

**Configs** (`launchpad/configs`; flagd configuration files):
- `default`: sources `flags/allFlags.json` and `rawflags/selector-flags.json`, `context-value`, `strict-numeric-range`;
- `ssl`: as `default`, plus `server-cert-path` and `server-key-path`;
- `sync-payload`: as `default`, plus `disable-sync-metadata`;
- `metadata`: source `rawflags/selector-flag-combined-metadata.json`.

**How the Go suite (tests/flagd v2.3.0) drives it:**
- **Calls:** every launchpad call is an HTTP **GET**. `NewFlagdContainer` waits for port 8080, then maps 8013, 8014, 8015, 8080 and envoy's 9211 and 9212.
- **Provider types to configs:**
  - "a stable / ssl / … flagd provider" calls `/start?config=` with `default`, or `ssl`, or `socket` (no `socket.json` exists at v3.11.0). Every other type (`unavailable`, `syncpayload`, `metadata`, …) uses `default`.
  - `unavailable` uses port 9999; `forbidden` uses envoy's 9212.
  - The runner skips `/start` when the config is `default` and `:8014/readyz` is 200.
- **Steps:** "the connection is lost for Ns" calls `/restart?seconds=N`; "the flag was modified" calls `/change`; `/stop` exists but no scenario of the pinned tag sets calls it.
- **Outage-dependent scenarios:**
  - `connection.feature` `@reconnect`, including through envoy (`@targetURI`);
  - `events.feature`: ready, then error, then ready; with `@grace`, stale in between;
  - `contextEnrichment.feature` and `sync-payload.feature` with `@grace`.

**Tag sets CI runs today** (`test/integration/integration_test.go`):
- **RPC:** `@rpc` without unixsocket, targetURI, sync, metadata, grace, events, customCert, reconnect, caching, forbidden, fractional-v1/v2 and deprecated. So the RPC run contains **no** outage scenario.
- **In-process:** `@in-process` without unixsocket, metadata, contextEnrichment, customCert, forbidden, sync-port, sync-payload, fractional-v1/v2 and deprecated. So `@reconnect`, `@events`, `@grace` and `@targetURI` (envoy) do run, and they all go through `/restart`.

**The relay side:**
- **Listeners:**
  - flag evaluation and flag sync call `net.Listen` (tcp or unix) themselves (`connect_service.go:162`, `sync_service.go:130`);
  - OFREP, the management port (`runtime/management.go`) and the event collector use `ListenAndServe`;
  - `service/transport` has no listener helper.
- **Runtime lifecycle:**
  - `Runtime.Start()` blocks on SIGINT and SIGTERM; `start(ctx)` is unexported.
  - `FromConfig` with the OpenTelemetry telemetry provider registers a Prometheus exporter with the default registerer and sets OpenTelemetry globals, so a second call in one process fails (step 6 finding N3).
- **`runtime/cmd`** reads flagd's flags and configuration through the global viper, and calls `fromConfig` and `rt.Start()` inside cobra's `Run`. Nothing exposes "configuration file to `runtime.Config` and `Components`".
- **Middleware chain** (step 6): `h2c → CORS → middleware → instrumentation → handler`. CORS preflights and the management port bypass it.
- **Fetch triggers** (UC-003) apply to fetcher sources only. `file` is a streaming sync provider (fsnotify) and takes no trigger.

## 0. Open questions for the project lead

The recommendation comes first.

- **Q1, slicing and parallelism.**
  - **Recommendation:** nine PRs (section 7).
    - **Parallel now, all on `main`:**
      - C, the launchpad contract characterization against today's image;
      - S, the spec;
      - G1, the listener gate;
      - G2, the listen seam;
      - L, the runtime lifecycle seam;
      - F, the fault middleware;
      - T, the fetch trigger.
    - **Then:** B (the testbed `cmd` module, manifest and Dockerfile) on G1, G2, L, F and T. Then I (integration in CI) on B and C. Then X (close).
    - The critical path is G2 → B → I → X.
  - *Alternative:* one PR per piece, in sequence. Simpler, about twice as long.

- **Q2, where the control endpoint lives.**
  - **Recommendation:** in the testbed's `cmd:` module (`R/testbed/cmd`), as a plain `net/http` server on 8080 **outside the runtime**, not as a service endpoint component.
    - It has to stay up while flagd is "down", and to rebuild the runtime for a configuration switch (Q3). A service endpoint is created by the runtime and stops with it.
    - The `cmd:` entry (UC-001) is the place a distribution owns its process. The issue's mapping allowed either.
  - *Alternative:* a service endpoint whose listener the gate skips. It can gate, but it cannot switch configurations, because it cannot replace the runtime that owns it.

- **Q3, `/start?config=<name>` with another configuration.**
  - **The constraint:** `ssl`, `sync-payload` and `metadata` change TLS, sources and endpoint settings, which a running runtime cannot change. The listener gate alone cannot serve them.
  - **Recommendation:** an in-process rebuild.
    - **Same configuration** (every call our CI makes): a gate cycle, which closes everything and reopens.
    - **Different configuration:** close the gate (Q6), shut the old runtime down, build a new one from `configs/<name>.json` with `FromConfig`, run it, and reopen the gate once `/readyz` is 200.
    - **Needs:** `Runtime.Run(ctx)` exported (L), a configuration loader that does not use the global viper (L), and a `FromConfig` that can be called again in one process. With the noop telemetry provider it can (Q4); L proves it with a test.
  - *Alternative:* the testbed `cmd` re-executes itself as a child process for a configuration switch, as the launchpad does with flagd. It is faithful to a fresh process for every `/start`, but stop then means killing a process, not the listener gate the decision names.

- **Q4, the testbed's telemetry provider.**
  - **Recommendation:** the noop telemetry provider. The manifest is otherwise in lockstep with flagd-full (Q15).
    - The provider suites at the pinned tags assert no metric or span.
    - The OpenTelemetry provider's Prometheus registration and globals make `FromConfig` non-reentrant, and making them reentrant would touch how flagd-full's `/metrics` is built.
    - Tracing tests (#59) can bring the OpenTelemetry provider back with Q3's alternative.
  - *Alternative:* OpenTelemetry, with Q3's child-process alternative, or made reentrant: a private registry per provider, which changes flagd-full's `/metrics` composition, so it needs its own C-003 question.

- **Q5, "fresh process" semantics under the gate.**
  - **What differs:** the launchpad kills flagd, so after `/restart` providers meet a new process. The gate keeps the runtime and its store.
  - **What providers can still observe is unchanged:** a reconnecting RPC provider gets `provider_ready` and an in-process provider gets the full payload, as from a fresh flagd.
  - **Recommendation:**
    - The testbed keeps the launchpad's file handling exactly: `/restart` deletes `/flags/allFlags.json` (file-provider suites of other languages see the file disappear) and regenerates it on start.
    - While gated, the runtime's file sync sees the deletion and delivers `{}` (UC-003 BR-012). On start, the testbed regenerates the file, **waits for the runtime's configuration change event for that source**, and only then reopens the gate. No client ever reaches the empty store.
    - In-memory state that survives the gate is listed in the UC as a known difference: the store's document versions, OFREP ETags (stable) and the process start time in logs. No pinned scenario observes any of them.
  - *Alternative:* rebuild the runtime on every `/start` and `/restart`, as for Q3's configuration switch. That gives fresh state every time, at the cost of a full startup per scenario (the suite calls `/start` per scenario).

- **Q6, how the gate closes.** SIGKILL closes every socket without application-level goodbyes: no `provider_shutdown` event and no HTTP/2 GOAWAY.
  - **Recommendation:** abrupt.
    - The gate closes the OS listener, so new connections are refused.
    - It closes every tracked `net.Conn` directly, under the HTTP servers, so streams end with EOF or reset.
    - It never calls `Server.Shutdown`, so no shutdown hooks or `provider_shutdown` events run.
    - `Accept` on the gate blocks while it is closed, so the endpoints' `Serve` never returns and the runtime never sees an error.
    - Start rebinds the same address; Go listeners on Linux set `SO_REUSEADDR`.
  - *Alternative:* graceful, which matches SIGTERM. Providers would then take the `provider_shutdown` path, which is not today's.

- **Q7, what the gate covers.**
  - **Recommendation:** every listener the runtime opens: flag evaluation, OFREP, flag sync, the management port, the event collector, and the unix sockets of a `socket` configuration. Not the control port 8080.
    - The management port is included because a killed flagd answers no `/readyz` or `/healthz`. Docker's healthcheck and envoy's TCP health checks see the outage as today.
    - An extension endpoint that opens its own listener without the seam (Q8) is not gated; the UC states this.
  - *Alternative:* service endpoints only. The management port would stay reachable during outages, unlike today.

- **Q8, the listen seam (G2).**
  - **Recommendation:** an injected function, not a package global.
    - `service.Settings.Listen func(network, address string) (net.Listener, error)` in the core, filled by the runtime from `distribution.Components.Listen`, defaulting to `net.Listen`.
    - flag evaluation, flag sync, OFREP, the event collector and the management port all open their listeners through it. OFREP, management and the collector move from `ListenAndServe` to `Listen` plus `Serve`.
    - For flagd-full nothing changes: same addresses, unix socket handling, TLS paths and bind error texts. The port, TLS, socket and serve-error pins already cover them, plus CI's upgrade differential.
  - *Alternative:* a `transport.SetListen` package hook. Smaller, but process-global, and it invites use by components that should not have it.

- **Q9, `/change` completion.**
  - **Recommendation:** answer after the launchpad's condition (`allFlags.json` regenerated) **and** the runtime's configuration change event for `flags/allFlags.json`, with the same 5 s limit, texts and status codes.
    - The launchpad answers before flagd has read the file; the Go suite then waits for the provider's change event, so answering later is never observable as a failure.
    - `/change` keeps going through the file and fsnotify, the same path flagd-full takes.
  - *Alternative:* answer exactly when the launchpad does, on regeneration only.

- **Q10, the fetch trigger (named in the decision).** No launchpad call needs one: `/change` edits a file, and `file` sources stream.
  - **Recommendation:** build it small and opt-in. The trigger type `testbed` (module `R/testbed/trigger`) fires on a new control call `POST /fetch?source=<uri>`, for fetcher sources (`http`, blob) in new scenarios. `/change` does not use it (Q9).
  - *Alternative:* drop it from step 13 until a scenario needs it.

- **Q11, the fault middleware and its control API.**
  - **Recommendation:** module `R/testbed/fault`, listed in the manifest and configured by the testbed `cmd` in every runtime it builds. It passes everything until a fault is set. New control paths, additive to the launchpad's:
    - `POST /faults` with a JSON body: `endpoint` (an endpoint type, or `*`), optional `path` prefix, `kind`, and `probability` (default 1).
    - Kinds: `latency` (`ms`); `status` (`code`: an HTTP status, or a ConnectRPC/gRPC code name for ConnectRPC routes); `abort` (the stream is reset with `http.ErrAbortHandler`); `refuse-all` (every request answers `unavailable` with the port open: "reachable but refusing everything").
    - `GET /faults` lists the faults; `DELETE /faults` clears them, and so does every `/start`.
    - CORS preflights and the management port are outside the chain (step 6), so they are never faulted; the UC says so.
  - *Alternative:* faults only through the configuration file. Simpler, but a test could not change faults mid-scenario.

- **Q12, launchpad API fidelity.**
  - **Recommendation:** byte-compatible with v3.11.0 for the four calls:
    - same paths, any method;
    - same JSON shapes, messages and status codes, including the plain-text `500` of `/change`;
    - `/start` returns only after `:8014/readyz` is 200 or 10 s have passed (then `500` and stopped);
    - `/restart` answers at once, cancels a pending restart and deletes `allFlags.json`;
    - `/stop` cancels a pending restart.

    PR C pins all of it against today's image, and PR I runs the same contract test against ours (a differential).
  - *Alternative:* the Go suite's subset only (GET, status codes). Other languages' suites would be unverified.

- **Q13, the image contract.**
  - **Recommendation:** `distributions/flagd-testbed/Dockerfile`, with the repository root as build context.
    - It builds the testbed binary with `relay-builder` from the manifest. The certificate stage is upstream's. The final image is busybox, so `wget` serves the compose healthcheck.
    - Paths: `/flagd` (the testbed binary, entrypoint), `/rawflags/` and `/configs/` from the **`test-harness` submodule** (data stays upstream's, unrestructured), `/flags/` (the volume) and `/ssl/`.
    - Exposed ports: 8013, 8014, 8015, 8016 and 8080.
    - The image stays local to CI; nothing is published (UC-012's dry-run rule).
  - *Alternative:* keep `test-harness/flagd/Dockerfile` and only swap the binary. The launchpad would still own the process, which defeats the gate.

- **Q14, which suites run in CI, and how.**
  - **Recommendation:** the Go provider suites `TestRPC` and `TestInProcess`, **unchanged** (same module, versions, compose file and tag sets).
    - They run against two images: today's launchpad image with our flagd, and the relay testbed. CI fails if the two pass sets differ, or if the relay testbed fails a scenario the launchpad image passes.
    - They run in the `integration-test` job. The image builds only when a PR touches the testbed modules, `service/transport`, the endpoints or the runtime; otherwise the launchpad image alone runs, as today.
    - Java, Python and JavaScript suites stay out of CI in the PoC, with a documented manual run.
  - *Alternative:* the relay testbed only. Faster, but it loses the differential that proves the drop-in.

- **Q15, the manifest's lockstep with flagd-full.**
  - **Recommendation:** `distributions/flagd-testbed/manifest.yaml` lists:
    - flagd-full's sync providers, configuration format, evaluator and service endpoints;
    - the noop telemetry provider (Q4), the fault middleware, the testbed trigger, and `cmd: R/testbed/cmd`;
    - `components_hint: false`, so error texts are flagd's.

    `test/distributions` `TestTestbedLockstepWithFlagdFull` fails when flagd-full gains or loses a sync provider, format, evaluator or endpoint that the testbed does not mirror.
  - *Alternative:* no check; drift shows only as a suite failure.

- **Q16, RPC outage scenarios.** Today's RPC tag set excludes `@reconnect`, `@events` and `@grace`.
  - **Recommendation:** keep the gating tag sets identical (the drop-in proof). Add one **non-gating** job that runs RPC with those three tags against both images and reports the difference. It is information for the project lead, not a gate.
  - *Alternative:* none; the outage behaviour is covered by the in-process run only.

- **Q17, the use case and its requirements** (next free IDs: UC-022, FR-056, NFR-014).
  - **Recommendation:** section 1's UC-022 "Run Provider Test Suites Against the Testbed".
    - Primary actor: Provider Author. Secondary actor: CI.
    - New requirements: FR-056 (testbed with a launchpad-compatible control API), FR-057 (outages close listeners), FR-058 (fault injection), FR-059 (testbed fetch trigger), NFR-014 (drop-in: unchanged provider suites pass the same scenarios), NFR-015 (testbed components never in a standard distribution).
    - It is drawn in `docs/use_cases.puml`, linked to `harness` and `ci`.
  - *Alternative:* fold the testbed into UC-013 (Verify Change Against Distributions) as an alternative flow. Fewer artefacts, but UC-013's actor is the Maintainer and its goal is different.

- **Q18, an evaluation-recording event sink** (in #110's mapping, not in the decision).
  - **Recommendation:** not in step 13. No pinned scenario asserts on recorded evaluations; it becomes a follow-up once a scenario needs it.
  - *Alternative:* add it now with a `GET /evaluations` control call.

- **Q19, the fractional scenarios of #47 ("Evaluator: pass the testbed's fractional scenarios", Backlog).**
  - **The facts:**
    - The suites run with feature path `./` under `test-harness`, so godog reads every `.feature` file: the provider files in `gherkin/` (`targeting.feature`'s fractional outlines) and the evaluator files in `evaluator/gherkin/fractional.feature`.
    - Both tag sets exclude `@fractional-v1` and `@fractional-v2`, the 21 scenarios #47 is about. The remaining fractional scenarios (`@fractional-v3` examples, `@fractional-nested`, the shared-seed outline) run today and pass in today's `integration-test` job.
    - Step 13 keeps the tag sets unchanged (Q14), and the pass-set comparison runs both images with the same tags. Under both images the fractional exclusions are therefore the same, and #47's failures cannot show up as a difference between them.
  - **Recommendation:** #47 does not have to land first.
    - The `@fractional-v1` and `@fractional-v2` exclusions stay in `test/integration`'s tag sets, owned by #47.
    - #47's definition of done gains one line: remove the two exclusions from `TestRPC` and `TestInProcess` too, not only from `test/evaluator-testkit`. The orchestrating session edits the issue; this plan changes nothing in it.
    - Step 13 adds no new fractional exclusion. If the relay testbed failed a fractional scenario the launchpad image passes, I's comparison would fail, which is a step 13 defect, not #47's.
  - *Alternative:* land #47 first and run the full fractional set in step 13. That couples step 13 to an evaluator change that alters bucketing, which is a C-003 question of its own.

## 1. Scope

### UC-022: Run Provider Test Suites Against the Testbed (draft for PR S)

- **Primary actor:** Provider Author. **Secondary actor:** CI.
- **Goal:** run a provider's Gherkin suite against the flagd being released, with outages and faults the suite controls, without changing the suite.
- **Trigger:** a provider suite starts the testbed through the flagd-testbed compose file.
- **Preconditions:** the testbed image is built from `distributions/flagd-testbed`, and the flagd-testbed data (flags, configs, certificates, compose file, Gherkin) is the pinned `test-harness`.

**Main success scenario:**
1. CI starts the compose stack with the testbed image.
2. The testbed combines the raw flags into `/flags/allFlags.json`, builds the runtime from `configs/default.json`, serves 8013–8016 and the control API on 8080, and reports readiness.
3. The suite calls `/start?config=default` per scenario. The testbed cycles the listener gate and answers once `/readyz` is 200.
4. The suite evaluates flags over RPC, in-process sync, OFREP or the file.
5. The suite calls `/change`. The testbed toggles `changing-flag`, regenerates the file, waits for the runtime to accept it, and answers.
6. The suite calls `/restart?seconds=N`. The testbed answers at once, deletes `allFlags.json` and closes every runtime listener and connection; providers see refused connections and broken streams. After N s it regenerates the file, waits for acceptance and reopens.
7. The suite's providers reconnect and fire their ready events; the suite passes the same scenarios as against the launchpad image.

**Alternative flows:**
- **A1 Configuration switch:** `/start?config=<other>` rebuilds the runtime (Q3).
- **A2 Unknown configuration:** `/start?config=nope` answers `500`, and the runtime stays stopped, as with the launchpad.
- **A3 Invalid seconds:** `/restart?seconds=-1` answers `400`.
- **A4 Stop:** `/stop` closes the gate until the next `/start`.
- **A5 Fault injected:** `POST /faults`; requests on the chosen endpoint get latency, an error, an aborted stream or refusal with the port open; `DELETE /faults` or `/start` clears it.
- **A6 Fetch triggered:** `POST /fetch?source=` makes a fetcher source fetch now.
- **A7 Change times out:** `/change` answers `500` as the launchpad does.
- **A8 Not ready in time:** `/start` answers `500` after 10 s and leaves the runtime stopped.

**Business rules:**
- **BR-001 Same API:** the four launchpad calls behave as flagd-testbed v3.11.0's, for any HTTP method.
- **BR-002 Outages close sockets:** stop and restart close listeners and connections without application-level goodbyes; the control port stays up.
- **BR-003 No empty store reachable:** after a restart the gate reopens only when every source has been accepted again.
- **BR-004 Faults are opt-in and additive:** no fault is active unless set; outages never use the middleware.
- **BR-005 Test-only:** testbed components appear in no standard distribution (NFR-015).
- **BR-006 Lockstep:** the testbed contains flagd-full's sync providers, configuration format, evaluator and service endpoints.
- **BR-007 Data stays upstream's:** flags, configs, certificates, the compose file and Gherkin come unmodified from the pinned `test-harness`.

**Realizes:**

| Requirement | Delivered by |
|---|---|
| FR-056 | B: control API; C and I: contract |
| FR-057 | G1, G2; B wires them |
| FR-058 | F, plus B's `/faults` |
| FR-059 | T, plus B's `/fetch` |
| NFR-014 | I: both images, same pass set |
| NFR-015 | B: boundary rows and `TestStandardManifestsHaveNoTestbedModule` |
| UC-001's gap "no repository manifest compiles middleware or event sinks with `cmd:`" | B (middleware and `cmd:`; event sinks stay with Q18) |

**Use case coverage:**

| Flow | Test |
|---|---|
| main 1–7, BR-001, NFR-014 | I: `TestRPC` and `TestInProcess` against both images, plus `TestTestbedContract` (C's test) against ours |
| main 6, BR-002 | G1's `TestGateRefusesWhileClosed`, `TestGateBreaksOpenStreams`, `TestGateReopensSamePort`, `TestGateCloseIsAbrupt` (no `provider_shutdown` on an event stream); B's `TestRestartClosesEveryRuntimePort` (8013–8016 refused, 8080 up) |
| BR-003 | B's `TestRestartReopensOnlyAfterAccept` |
| A1 | B's `TestConfigSwitchRebuildsRuntime` (`ssl`: TLS on 8013) |
| A2, A3, A7, A8 | C's contract cases, replayed by I |
| A4 | C and B `TestStopClosesUntilStart` |
| A5, BR-004 | F's `middlewaretest.Run` and `TestFaultKinds`; B's `TestFaultsClearedOnStart`, `TestNoFaultPassesThrough` |
| A6 | T's `triggertest` run and B's `TestFetchCallFiresTrigger` |
| BR-005, BR-006 | `TestStandardManifestsHaveNoTestbedModule`, `TestTestbedLockstepWithFlagdFull` |
| BR-007 | B's Dockerfile copies only from `test-harness`; `TestImageDataMatchesSubmodule` |

**Leaves out:**
- restructuring upstream flagd-testbed;
- Java, Python and JavaScript suites in CI (Q14);
- an evaluation-recording sink (Q18);
- tracing scenarios (#59, Q4);
- the `@fractional-v1` and `@fractional-v2` scenarios, excluded as today and owned by #47 (Q19);
- publishing the image.

## 2. Characterization (PR C, base `main`, parallel now)

The oracle is today's image: `test-harness/flagd/Dockerfile` over `flagd/build.Dockerfile`, as `test/integration` builds it.

| Pin | Test | Pins |
|---|---|---|
| C1 | `test/testbed` `TestTestbedContract/api` | For `/start`, `/stop`, `/restart` and `/change`, with GET and POST: status code, `Content-Type`, body. Also `/restart?seconds=x` and `-1`, `/start?config=nope`, and `/change` twice (foo, bar, foo) |
| C2 | `TestTestbedContract/outage` | During `/stop` and the delay of `/restart?seconds=3`: 8013, 8014, 8015 and 8016 refuse connections; 8080 answers. An open `EventStream` on 8013 and a `SyncFlags` stream on 8015 end with an error, and no `provider_shutdown` arrives first. After the delay, `/readyz` is 200 and both streams can be reopened |
| C3 | `TestTestbedContract/files` | `/flags/allFlags.json` exists at start, is gone during a restart, and is back afterwards with the same content. After `/change` it holds the new variant |
| C4 | `TestTestbedContract/configs` | `/start?config=ssl` serves TLS on 8013 with the custom root CA; `sync-payload` sends sync without metadata; `metadata` serves only the metadata flag set |

The contract test is a Go module of its own (`test/testbed`), parameterized by image. C runs it against the launchpad image; I adds the relay testbed.

## 3. Steps

Every commit builds and passes `make test`. A change to flagd-full's observable behaviour is a defect here: G2 must be invisible.

| PR | Model | Base | Files | Change | Check |
|---|---|---|---|---|---|
| S | mechanic | `main` | `docs/use_cases/UC-022-*.md`, `docs/requirements.md`, `docs/use_cases.puml`, `docs/glossary.md` (Testbed, Launchpad API, Listener Gate) | Section 1's UC-022 and requirements | spec lint |
| C | implementer | `main` | `test/testbed/` (module, `contract_test.go`), `Makefile` target `testbed-contract` | Section 2 against the launchpad image | green locally in WSL and in the `integration-test` job |
| G1 | implementer | `main` | `service/transport/gate/` | `Gate` with `Listen(network, address) (net.Listener, error)`, `Close()` and `Open() error`. The returned listener's `Accept` blocks while the gate is closed; connections are tracked and closed on `Close`; `Open` rebinds every address | `TestGateRefusesWhileClosed`, `TestGateBreaksOpenStreams` (h2c ConnectRPC stream), `TestGateReopensSamePort`, `TestGateCloseIsAbrupt`, `TestGateUnixSocket`; `-race` |
| G2 | implementer | `main` | `core/pkg/service` (`Settings.Listen`), `core/pkg/distribution` (`Components.Listen`), `runtime` (passes it; management port), `service/flag-evaluation`, `service/flag-sync`, `service/ofrep`, `service/event-collector` | Q8; `ListenAndServe` becomes `Listen` plus `Serve` | existing port, TLS, socket and serve-error pins unchanged; `TestListenSeamUsedByEveryEndpoint` (a recording `Listen`); `make check-flagd-full` (no generated change); acceptance; upgrade differential; allocs/op |
| L | implementer | `main` | `runtime` (`Runtime.Run(ctx)`; `Start` keeps signal handling), `runtime/cmd` (`LoadConfig(path, components) (runtime.Config, distribution.Components, error)` over a private viper, sharing the flag-to-setting table with `start`) | Q3's seams | `TestRunStopsOnContext`, `TestFromConfigTwiceWithNoopTelemetry` (same ports, sequential), `TestLoadConfigMatchesStart` (same `runtime.Config` as `flagd start --config` for each `test-harness` config); `start_config.golden` unchanged |
| F | implementer | `main` | `testbed/fault/` (module) | Q11's middleware; a `Controller` the testbed `cmd` owns, looked up by name from the middleware's settings | `middlewaretest.Run`, `TestFaultKinds` per kind on ConnectRPC, gRPC and OFREP routes, `TestNoFaultPassesThrough` (allocs/op of a passing request ≤ the chain without it) |
| T | implementer | `main` | `testbed/trigger/` (module) | Q10's trigger type `testbed`, fired by a `Controller` | the trigger contract run; `TestFireFetchesNow` |
| B | implementer | G1 + G2 + L + F + T | `testbed/cmd/` (module: control server, combiner ported from the launchpad, gate wiring, rebuild), `distributions/flagd-testbed/manifest.yaml`, `Dockerfile`, `test/distributions` (lockstep and boundary tests), `testdata/module-boundaries.txt` | Q2–Q7, Q9, Q11–Q13, Q15 | B's tests in section 1; `relay-builder` builds the manifest in `make test-distributions`; `docker build` of the image in WSL |
| I | implementer | B + C | `test/integration/integration_test.go` (image choice by environment variable; the default stays the launchpad image), `test/testbed` (second image), `.github/workflows/build.yaml` (the relay testbed run, change-filtered; the non-gating RPC outage job, Q16), `Makefile` | Q14, Q16 | both images pass `TestRPC` and `TestInProcess` with today's tags; the pass-set comparison; `TestTestbedContract` against both |
| X | mechanic | I | `docs/use_cases/UC-022`, `docs/relay/testbed.md`, `docs/use_cases/UC-001` (gap closed), requirement statuses; #110 | Status `Implemented` | spec lint |

`test/integration` stays the orphaned module it is. The suites are not edited; only the image under them changes.

## 4. Gates

In WSL from the repository root (`export GOTOOLCHAIN=go1.27.3`; in a worktree `export GOWORK=$PWD/go.work`; for lint `export GOPATH=$(go env GOPATH)`). CI is the full run; implementers run their packages' tests with `-race`.

| Gate | Command | PRs |
|---|---|---|
| Unit tests | `make workspace-init && make test` | all |
| Lint, boundaries | `make lint check-module-boundary check-package-coverage`; a new row forbids `R/testbed/...` in every module except `testbed/*`, `test/*` and `distributions/flagd-testbed` | all |
| flagd-full unchanged | `make check-flagd-full`, CI's upgrade differential, `make flagd-integration-test` | G2, L |
| Benchmarks | CI's allocs/op comparison (G2 touches every endpoint's setup, not its request path) | G2, F |
| Testbed contract | `make testbed-contract IMAGE=<image>` | C, I |
| Provider suites | `make flagd-integration-test` with `TESTBED_IMAGE=relay` and with the default | I |
| Spec | `spec_lint.py --docs docs` on Windows | S, X |

## 5. Risks

- **R1, gate fidelity against SIGKILL (the biggest).**
  - **The differences:** a killed process makes the kernel reset connections that have unread data. Closing a `net.Conn` from Go sends FIN and, under HTTP/2, may race the server's own writes. Providers' reconnect paths distinguish `UNAVAILABLE` from `CANCELLED` and EOF.
  - **The checks:**
    - G1 sets `SetLinger(0)` on TCP connections before closing, so they reset as a killed process's would.
    - C2's stream assertions run against both images.
    - The in-process `@reconnect`, `@events` and `@grace` scenarios are the acceptance.
- **R2, a reentrant `FromConfig` (Q3, Q4).** Hidden process-global state would break the second runtime: viper, the logger, `fips`, OpenTelemetry globals, Prometheus. L's `TestFromConfigTwiceWithNoopTelemetry` and B's `TestConfigSwitchRebuildsRuntime` are the proof. If they fail, Q3's child-process alternative is the fallback.
- **R3, the transient empty store (Q5).** Deleting `allFlags.json` while gated makes the file sync deliver `{}`. BR-003's wait for acceptance covers it. If the file sync coalesces the delete and the recreate, no change event fires. Then B waits for the store to hold the file's flags instead, so the wait is "accepted or unchanged".
- **R4, timing.** The relay testbed restarts in exactly N s; the launchpad takes N s plus a flagd startup. Grace scenarios ("lost for 1s, no error") get more slack, never less. Scenarios that depend on a slow start would show up in I's pass-set comparison.
- **R5, rebinding.** Rebinding the port right after close relies on `SO_REUSEADDR`, which Go sets on Linux. Envoy's TCP health checks (1 s) and the compose healthcheck see the outage as today.
- **R6, G2 touches flagd-full's listeners.** Bind error texts, unix socket permissions and TLS loading must stay byte-identical. The serve-error and startup pins, plus the upgrade differential, cover them.
- **R7, CI time.** A second image and two more suite runs add about 8 minutes to `integration-test` when the change filter matches.
- **R8, upstream drift.** Renovate moves `test-harness` and the Go suite. A new launchpad call upstream fails the differential until the testbed implements it, which is the intended alarm.
- **R9, containment.** Nothing goes upstream. The ported combiner is Apache-2.0 code from the same organization; the file says where it comes from.

## 6. Mapping: launchpad to relay pieces

| Launchpad / testbed piece | Relay piece | PR |
|---|---|---|
| flagd binary at `/flagd` | the testbed distribution binary (flagd-full's components, noop telemetry) | B |
| launchpad process, port 8080 | `R/testbed/cmd`: control server outside the runtime | B |
| `/start?config=` (same config) | listener gate: close and open | G1, B |
| `/start?config=` (other config) | runtime rebuild: `Runtime.Run(ctx)`, `runtime/cmd.LoadConfig` | L, B |
| `/stop` | listener gate: close | G1, B |
| `/restart?seconds=N` | gate close, delete `allFlags.json`, gate open after N s and acceptance | G1, B |
| SIGKILL socket behaviour | abrupt gate close (`SetLinger(0)`, no `Shutdown`) | G1 |
| every flagd port (8013–8016, unix socket) | `Components.Listen` through the gate | G2 |
| `/change`, fsnotify, combiner | ported combiner and toggle; the runtime's file sync; wait on the configuration change event | B |
| `configs/*.json` | read by `runtime/cmd.LoadConfig` (flagd's own configuration keys) | L, B |
| `/ssl` certificates | the same Dockerfile certificate stage | B |
| compose file, envoy, `FLAGS_DIR` volume, healthcheck | unchanged, from `test-harness` | — |
| (new) latency, errors, aborts, refusal with the port open | fault middleware, `/faults` | F, B |
| (new) immediate fetch of a fetcher source | testbed fetch trigger, `/fetch` | T, B |

## 7. PR split

| PR | Branch | Base | Runs |
|---|---|---|---|
| S | straight to `main` (spec) | `main` | now |
| C | `uc-022/pr-1-testbed-contract` | `main` | now |
| G1 | `uc-022/pr-2-listener-gate` | `main` | now |
| G2 | `uc-022/pr-3-listen-seam` | `main` | now |
| L | `uc-022/pr-4-runtime-lifecycle` | `main` | now |
| F | `uc-022/pr-5-fault-middleware` | `main` | now |
| T | `uc-022/pr-6-fetch-trigger` | `main` | now |
| B | `uc-022/pr-7-testbed-distribution` | G1 + G2 + L + F + T | after its parents' branches exist (stacked; merged up as they land) |
| I | `uc-022/pr-8-suites-in-ci` | B (+ C merged) | after B |
| X | `uc-022/pr-9-close` | I | last |

Every PR description starts with `Part of #110`; S and X also name the UC-022 issue once it exists.
