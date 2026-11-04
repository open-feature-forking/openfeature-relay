# Step 13 plan: the flagd testbed as a relay-builder distribution (UC-022 Run Provider Test Suites Against the Testbed)

**Status: revision 2.** It resolves `step-13-opposer.md` (1 blocker, 4 major, 8 minor). Every finding is adopted; section 8 says how. Section 0 holds the questions for the project lead, each with a recommendation. Q3 and Q5 are merged, and Q20 is new.

**Decision (2026-11-04, issue #110): in the PoC.**
- **In scope:** `distributions/flagd-testbed` with a control endpoint, a listener gate in `service/transport` for stop and start, a fault-injection middleware and a fetch trigger. The provider test suites run against it as the testing harness.
- **Out of scope:** restructuring the upstream flagd-testbed repository into data and Gherkin files only. Upstream gets nothing: no issues, PRs or comments outside this repository.

**Base.** `main` @ f0cf5170. Step 10 is merged: operator settings for middleware and event sinks (`Components.MiddlewareFactories`, `EventSinkFactories`, `runtime/cmd/extensions.go`), `dist.components_hint`, the standard manifests, and the `cmd:` entry used by `test/distributions/example` and `sync-vendor`. Issue: #110. "R" is `github.com/open-feature-forking/openfeature-relay`.

**Drop-in rule for this step.** Today's provider suites run **unchanged** against our testbed. That means:
- the same launchpad API, the same ports, the same image contract (paths, working directory, volume, healthcheck) and the same compose file;
- outages that close listeners, so connections are refused and streams break;
- a fresh runtime on every start, as a new process gives today.

Where an exact match is impossible, section 0 names it as a question.

## What exists (read at the pinned versions)

**Pinned versions.**
- `test/integration` pins `github.com/open-feature/go-sdk-contrib/providers/flagd v0.8.0` and `tests/flagd/v2 v2.3.0`.
- It builds `flagd/build.Dockerfile`, then the testbed image from `test-harness/flagd/Dockerfile` with `FLAGD_BASE_IMAGE` set to that flagd image.
- `test-harness` is the flagd-testbed submodule at **v3.11.0** (9d8dace5).
- The Go suite starts `test-harness/docker-compose.yaml` through testcontainers' compose module, with `IMAGE`, `VERSION` and `FLAGS_DIR` set.

**The image (`test-harness/flagd/Dockerfile`):**
- busybox, working directory `/` (busybox's default);
- `/flagd` is the flagd binary; `/launchpad` is the entrypoint;
- `/rawflags/` holds the 11 flag files of `flags/`; `/configs/` holds `launchpad/configs/*.json`; `/flags/` is created empty and mounted from `FLAGS_DIR`;
- `/ssl/server-cert.pem` and `server-key.pem` are generated at build time and signed with `ssl/custom-ca.key`; `/ssl/custom-root-cert.crt` is copied in.

**The compose file:**
- **Service `flagd`:**
  - ports 8013, 8014, 8015, 8016 and 8080; the volume `${FLAGS_DIR}:/flags`;
  - healthcheck `wget --spider http://localhost:8014/healthz` every 10 s, timeout 5 s, 3 retries, 30 s start period. An 8 s outage does not make the container unhealthy.
- **Service `envoy`:**
  - ports 9211, 9212 and 9901 (admin);
  - 9211 routes by authority (`rpc.service` to `flagd:8013`, `sync.service` to `flagd:8015`) with TCP health checks every 1 s, unhealthy threshold 1. These checks are what sees an outage;
  - 9212 answers every request with `403 PERMISSION_DENIED`;
  - it starts once `flagd` is healthy.

**The launchpad (port 8080, `net/http` mux, any HTTP method):**

| Call | Behaviour |
|---|---|
| `/start?config=<name>` | Sets the remembered configuration to `<name>` **before** trying (empty means the last one, initially `default`). Cancels a pending restart and kills the running flagd (SIGKILL; waits up to 5 s). Regenerates `/flags/allFlags.json` if it is missing and restarts the `rawflags/` watcher. Runs `./flagd start --config ./configs/<name>.json` from `/`, then polls `:8014/readyz` every 100 ms for up to 10 s. Answers `200 {"status":"success","message":"flagd started successfully"}`. Otherwise it stops flagd and answers `500 {"status":"error","message":"Failed to start flagd: flagd health check timed out"}`, after 10 s; an unknown configuration takes this path too |
| `/stop` | Cancels a pending restart, kills flagd, `200 {"status":"success","message":"flagd stopped successfully"}` |
| `/restart?seconds=N` | Default 5. A value that is not a non-negative integer gets `400 {"status":"error","message":"'seconds' must be a non-negative integer"}`. Otherwise it cancels a previous pending restart, **deletes `/flags/allFlags.json`**, kills flagd, answers `200 "flagd will restart in N seconds"` at once, and calls `/start`'s path with the remembered configuration after N s |
| `/change` | Reads `rawflags/changing-flag.json` into a narrow struct (`state`, `variants` as `map[string]string`, `defaultVariant`), toggles `changing-flag`'s `defaultVariant` between `foo` and `bar`, and writes it back with two-space indentation by atomic rename. Waits up to 5 s until the `rawflags/` watcher has regenerated `allFlags.json`, then answers `200 "Default variant successfully changed to '<v>'\n"`. On failure it answers a plain-text `500` (`http.Error`) |
| at startup | Combines `rawflags/*.json`, except `selector-*`, by deep merge into `flags/allFlags.json`; watches `rawflags/` and recombines on every change; starts flagd with `default` |

JSON bodies come from `json.NewEncoder`, with a trailing newline.

**Configs** (`launchpad/configs`; flagd configuration files, with paths relative to `/`):
- `default`: sources `flags/allFlags.json` and `rawflags/selector-flags.json`, `context-value`, `strict-numeric-range`;
- `ssl`: as `default`, plus `server-cert-path` and `server-key-path`;
- `sync-payload`: as `default`, plus `disable-sync-metadata`;
- `metadata`: source `rawflags/selector-flag-combined-metadata.json`.

**How the Go suite (tests/flagd v2.3.0) drives it:**
- **Setup:** every launchpad call is an HTTP **GET**. `NewFlagdContainer` waits for port 8080 (60 s), then maps 8013, 8014, 8015, 8080 and envoy's 9211 and 9212; 8016 is not mapped. The runner calls `/start?config=default` once at suite setup, unless `/readyz` is already 200 (`testbed_runner.go:127-133`).
- **`/start` runs once per scenario.** Every "a <type> flagd provider" step calls `triggerTestbedConfiguration`, which always calls `StartFlagdWithConfig`: `ssl`/`tls` for those types, `socket` for that one, `default` for everything else (`provider_steps.go:258-283`). It uses a 10 s HTTP timeout, then waits 15 s for `/readyz`. In CI every scenario uses `default`, because `@customCert` and `@unixsocket` are excluded.
- **Provider types:** `unavailable` uses port 9999; `forbidden` uses envoy's 9212.
- **Steps:** "the connection is lost for Ns" calls `/restart?seconds=N`, with N of 6 or 8 in the pinned scenarios and 1 in one `@grace` case. "The flag was modified" calls `/change`. `/stop` is never called.
- **Outage-dependent scenarios:**
  - `connection.feature` `@reconnect`, including through envoy (`@targetURI`);
  - `events.feature`: ready, then error, then ready; with `@grace`, stale in between, and "lost for 1s" with no error;
  - `contextEnrichment.feature`, `sync-payload.feature` and `rpc-caching.feature` with `@grace`.

  `events.feature`'s "Flag change event" runs `/change` right after two 8 s outages.

**Tag sets CI runs today** (`test/integration/integration_test.go`):
- **RPC:** no outage scenario; `@reconnect`, `@events`, `@grace`, `@caching` and others are excluded.
- **In-process:** `@reconnect`, `@events`, `@grace` and `@targetURI` (envoy) run, and they all go through `/restart`.
- **Fractional:** both sets exclude `@fractional-v1` and `@fractional-v2` (#47, Q19).

**The relay side:**
- **Listeners:**
  - flag evaluation and flag sync call `net.Listen` (tcp or unix) themselves (`connect_service.go:162`, `sync_service.go:130`);
  - OFREP, the management port (`runtime/management.go`) and the event collector use `ListenAndServe`;
  - `ConnectService.Serve`, the embedder path the runtime does not use, starts its own management server with `ListenAndServe` (`connect_service.go:146-148, 317-330`);
  - `service/transport` has no listener helper.
- **The runtime:**
  - `Runtime.Start()` blocks on SIGINT and SIGTERM; `start(ctx)` is unexported.
  - `FromConfig` with the OpenTelemetry telemetry provider registers a Prometheus exporter with the default registerer and sets OpenTelemetry globals, so a second call in one process fails (step 6 finding N3).
- **`runtime/cmd`** reads flagd's flags and configuration through the global viper, and calls `fromConfig` and `rt.Start()` inside cobra's `Run`. Nothing exposes "configuration file to `runtime.Config` and `Components`".
- **`sync/file` is a fetcher with fetch triggers** (UC-003). Outside Kubernetes its default trigger is `fileinfo`, which polls the file's status every 1 s.
  - When the file is removed, the trigger signals once and tries to re-add the watch about 1 s long (6 attempts, 200 ms apart), then logs "error restoring watcher" and **watches nothing from then on**.
  - The fetch of a missing file delivers `{}`.
  - So a runtime that outlives a deletion longer than about 1 s never sees the file again. This is today's flagd-full behaviour too (Q20).
- **Middleware chain** (step 6): `h2c → CORS → middleware → instrumentation → handler`. CORS preflights and the management port bypass it.

## 0. Open questions for the project lead

The recommendation comes first.

- **Q1, slicing and parallelism. (changed in revision 2)**
  - **Recommendation:** eleven PRs (section 7).
    - **Parallel now, all on `main`:** S (the spec), C (the launchpad contract characterization against today's image), G1 (the listener gate), G2 (the listen seam), F (the fault middleware) and T (the fetch trigger).
    - **L** (the runtime lifecycle seam) stacks on G2, because both edit `runtime`.
    - **B1** (the testbed `cmd`: control API, gate wiring, the rebuild on every start, Dockerfile, manifest without fault and trigger) stacks on G1 and L.
    - **I** (integration in CI) stacks on B1 and C. **B2** (`/faults` and `/fetch` wiring, their manifest entries) stacks on B1, F and T and runs beside I.
    - **X** (close) comes last.
    - The critical path is G2 → L → B1 → I → X; F, T and B2 stay off it.
  - *Alternative:* one B on all five branches. Fewer PRs, but the drop-in proof would wait for faults and the trigger, and every parent change would force a re-merge.

- **Q2, where the control endpoint lives.**
  - **Recommendation:** in the testbed's `cmd:` module (`R/testbed/cmd`), as a plain `net/http` server on 8080 **outside the runtime**, not as a service endpoint component.
    - It has to stay up while flagd is "down" and to replace the runtime on every start (Q3). A service endpoint is created by the runtime and stops with it.
    - The `cmd:` entry (UC-001) is the place a distribution owns its process.
  - *Alternative:* a service endpoint the gate skips. It can gate, but it cannot replace the runtime that owns it.

- **Q3, what a start does. (changed in revision 2; it absorbs Q5)**
  - **The facts:** the launchpad gives providers a new flagd process on every `/start` and after every `/restart` delay, and the suite calls `/start` in every scenario. A runtime kept across a `/restart` would also lose `allFlags.json` for good (the `fileinfo` trigger stops watching about 1 s after the deletion; finding 1).
  - **Recommendation:** every start builds a fresh runtime. There is no gate-only cycle.
    - **Close** (`/stop`, `/restart`, and every `/start` before it builds): the gate closes abruptly (Q6), then the old runtime's context is cancelled. Its shutdown finds no connection left, so no client receives a goodbye.
    - **Open** (every `/start`, and the delayed start of `/restart`):
      1. regenerate `allFlags.json` if missing and restart the `rawflags/` watcher, as the launchpad does;
      2. build a **new** runtime with `FromConfig` from `configs/<name>.json` and run it with `Runtime.Run(ctx)`;
      3. once the runtime reports ready in-process, which means every source has been accepted, open the gate, so `:8014/readyz` answers 200.

      The 10 s limit and the `500` text are the launchpad's.
    - **What this gives:** a fresh store, document versions and ETags, as from a new process. A configuration switch is the same path with another file (A1). The gate still makes the close abrupt and keeps the ports refusing during the outage.
    - **What it needs:** `Runtime.Run(ctx)`, a configuration loader without the global viper, and a `FromConfig` that can run hundreds of times in one process with the noop telemetry provider (Q4). L proves that (finding 2).
  - *Alternative:* the testbed `cmd` re-executes itself as a child process per start, as the launchpad does with flagd. It is faithful by construction, but stop is then a process kill, not the listener gate the decision names.

- **Q4, the testbed's telemetry provider.**
  - **Recommendation:** the noop telemetry provider; the manifest is otherwise in lockstep with flagd-full (Q15).
    - The provider suites at the pinned tags assert no metric or span.
    - The OpenTelemetry provider's Prometheus registration and globals make `FromConfig` non-reentrant, and Q3 rebuilds once per scenario.
    - Tracing tests (#59) can bring the OpenTelemetry provider back with Q3's alternative.
  - *Alternative:* OpenTelemetry, with Q3's child-process alternative, or made reentrant: a private registry per provider, which changes flagd-full's `/metrics` composition and needs its own C-003 question.

- **Q5, fresh-process semantics.** *Merged into Q3 in revision 2:* every start is a fresh runtime, so no state survives a restart and nothing is left to decide here.

- **Q6, how the gate closes. (changed in revision 2)**
  - **Recommendation:** abrupt, with a plain close.
    - The gate closes the OS listener, so new connections are refused.
    - It closes every tracked `net.Conn` with a plain `Close()` under the HTTP servers. No `Server.Shutdown`, so no shutdown hooks, `provider_shutdown` events or GOAWAY.
    - **No `SetLinger(0)`:** a killed process's sockets close like any `close(2)`, with a FIN, and a RST only when unread data is waiting. A plain `Close()` does the same.
    - `Accept` on the gate blocks while it is closed, so an endpoint's `Serve` does not return until its runtime is cancelled.
    - Opening binds the addresses again; Go listeners on Linux set `SO_REUSEADDR`.
    - C2 records what an idle `EventStream` and `SyncFlags` stream see against the launchpad image (EOF or reset), and I compares ours with it.
  - *Alternative:* graceful, which matches SIGTERM. Providers would then take the `provider_shutdown` path, which is not today's.

- **Q7, what the gate covers.**
  - **Recommendation:** every listener the runtime opens: flag evaluation, OFREP, flag sync, the management port, the event collector, and the unix sockets of a `socket` configuration. Not the control port 8080.
    - Envoy's 1 s TCP checks see the outage on 8013 and 8015 as today.
    - Docker's healthcheck (every 10 s, 3 retries) does not notice an 8 s outage, today or with the gate.
    - An extension endpoint that opens its own listener without the seam (Q8) is not gated; the UC says so.
  - *Alternative:* service endpoints only. `/readyz` would then answer during an outage, unlike today.

- **Q8, the listen seam (G2). (scope named in revision 2)**
  - **Recommendation:** an injected function, not a package global.
    - `service.Settings.Listen func(network, address string) (net.Listener, error)` in the core, filled by the runtime from `distribution.Components.Listen`, defaulting to `net.Listen`.
    - flag evaluation, flag sync, OFREP, the event collector and the runtime's management port open their listeners through it. OFREP, management and the collector move from `ListenAndServe` to `Listen` plus `Serve`.
    - `ConnectService.Serve`'s own management server (the embedder path) stays outside the seam. The runtime never calls it, and `TestListenSeamUsedByEveryEndpoint` names it as the one exception.
    - For flagd-full nothing changes: same addresses, unix socket handling, TLS and bind error texts. The port, TLS, socket and serve-error pins plus CI's upgrade differential cover it.
  - *Alternative:* a `transport.SetListen` package hook. Smaller, but process-global.

- **Q9, `/change` completion. (changed in revision 2)**
  - **Recommendation:** answer once both hold, within the same 5 s, with the same texts and codes:
    - the launchpad's condition: `allFlags.json` regenerated;
    - the runtime's store holds `changing-flag` with the new `defaultVariant`. The `fileinfo` trigger sees the rename within its 1 s poll; the store is what a provider reads next.

    `/change` goes through the file and the runtime's own file sync, the path flagd-full takes.
  - *Alternative:* answer exactly when the launchpad does, on regeneration only.

- **Q10, the fetch trigger. (premise corrected in revision 2)** File sources are fetchers too, so an extra trigger can attach to them as well as to `http` and blob sources. No launchpad call needs one (Q9).
  - **Recommendation:** build it small and opt-in: trigger type `testbed` (module `R/testbed/trigger`), fired by a new control call `POST /fetch?source=<uri>`, for any fetcher source in new scenarios. Wired in B2.
  - *Alternative:* drop it from step 13 until a scenario needs it.

- **Q11, the fault middleware and its control API.**
  - **Recommendation:** module `R/testbed/fault`, listed in the manifest and configured by the testbed `cmd` in every runtime it builds. It passes everything until a fault is set. New control paths, additive to the launchpad's:
    - `POST /faults` with a JSON body: `endpoint` (an endpoint type, or `*`), optional `path` prefix, `kind`, and `probability` (default 1).
    - Kinds: `latency` (`ms`); `status` (`code`: an HTTP status, or a ConnectRPC/gRPC code name for ConnectRPC routes); `abort` (the stream is reset with `http.ErrAbortHandler`); `refuse-all` (every request answers `unavailable` with the port open).
    - `GET /faults` lists the faults; `DELETE /faults` clears them, and so does every `/start`.
    - CORS preflights and the management port are outside the chain (step 6), so they are never faulted.
  - *Alternative:* faults only through the configuration file; a test could not change them mid-scenario.

- **Q12, launchpad API fidelity. (sharpened in revision 2)**
  - **Recommendation:** byte-compatible with v3.11.0 for the four calls:
    - same paths, any method, same JSON shapes with the trailing newline, same messages and status codes;
    - the plain-text `500` of `/change`;
    - `/start` sets the remembered configuration before it tries, so a failed `config=nope` sticks for later `/restart` and bare `/start` calls until a good name is given;
    - an unknown configuration answers like any start that is not ready in 10 s: `500 … flagd health check timed out`, after 10 s;
    - `/restart` answers at once, cancels a pending restart and deletes `allFlags.json`;
    - `/stop` cancels a pending restart;
    - `/change` keeps the launchpad's narrow struct and two-space indentation, so `changing-flag.json` and `allFlags.json` are byte-identical after every toggle.

    PR C pins all of it against today's image, and PR I runs the same contract test against ours.
  - *Alternative:* the Go suite's subset only (GET, status codes). Other languages' suites would be unverified.

- **Q13, the image contract. (working directory added in revision 2)**
  - **Recommendation:** `distributions/flagd-testbed/Dockerfile`, with the repository root as build context.
    - It builds the testbed binary with `relay-builder`. The certificate stage is upstream's; the final image is busybox (`wget` for the healthcheck) with an explicit `WORKDIR /`.
    - Paths: `/flagd` (the testbed binary, entrypoint), `/rawflags/` and `/configs/` from the **`test-harness` submodule** (data stays upstream's), `/flags/` (the volume) and `/ssl/`.
    - Exposed ports: 8013, 8014, 8015, 8016 and 8080.
    - The image stays local to CI; nothing is published.
  - *Alternative:* keep `test-harness/flagd/Dockerfile` and only swap the binary. The launchpad would still own the process, which defeats the gate.

- **Q14, which suites run in CI, and how. (filter widened in revision 2)**
  - **Recommendation:** the Go provider suites `TestRPC` and `TestInProcess`, **unchanged** (same module, versions, compose file and tag sets), against two images: today's launchpad image with our flagd, and the relay testbed.
    - CI fails if the pass sets differ, or if the relay testbed fails a scenario the launchpad image passes.
    - They run in the `integration-test` job. The relay testbed image is built when a change touches what the upgrade differential's filter covers (Dockerfiles, the build) **or** any Go source outside `docs/` and `.claude/`: `sync/file`, the fetching sync provider, the store and the format decide whether a restart works. With `FULL_CI=true` or a manual run, always.
    - Java, Python and JavaScript suites stay out of CI in the PoC, with a documented manual run.
  - *Alternative:* the relay testbed only. Faster, but it loses the differential that proves the drop-in.

- **Q15, the manifest's lockstep with flagd-full.**
  - **Recommendation:** `distributions/flagd-testbed/manifest.yaml` lists:
    - flagd-full's sync providers, configuration format, evaluator and service endpoints;
    - the noop telemetry provider (Q4) and `cmd: R/testbed/cmd`; B2 adds the fault middleware and the testbed trigger;
    - `components_hint: false`, so error texts are flagd's.

    `test/distributions` `TestTestbedLockstepWithFlagdFull` fails when flagd-full gains or loses a component of those four kinds that the testbed does not mirror.
  - *Alternative:* no check; drift shows only as a suite failure.

- **Q16, RPC outage scenarios. (changed in revision 2)** Today's RPC tag set excludes `@reconnect`, `@events` and `@grace`.
  - **Recommendation:** keep the gating tag sets identical. Add a `make testbed-rpc-outages` target that runs RPC with those three tags against both images, documented as a manual run, like the other languages' suites. No CI job.
  - *Alternative:* none.

- **Q17, the use case and its requirements** (next free IDs: UC-022, FR-056, NFR-014).
  - **Recommendation:** section 1's UC-022 "Run Provider Test Suites Against the Testbed".
    - Primary actor: Provider Author. Secondary actor: CI.
    - New requirements:
      - FR-056: a testbed with a launchpad-compatible control API;
      - FR-057: outages close listeners, and every start gives a fresh runtime;
      - FR-058: fault injection;
      - FR-059: the testbed fetch trigger;
      - NFR-014: drop-in: unchanged provider suites pass the same scenarios;
      - NFR-015: testbed components never appear in a standard distribution.
    - Drawn in `docs/use_cases.puml`, linked to `harness` and `ci`.
  - *Alternative:* fold the testbed into UC-013 as an alternative flow; UC-013's actor and goal differ.

- **Q18, an evaluation-recording event sink** (in #110's mapping, not in the decision).
  - **Recommendation:** not in step 13; a follow-up once a scenario needs it.
  - *Alternative:* add it now with a `GET /evaluations` control call.

- **Q19, the fractional scenarios of #47 ("Evaluator: pass the testbed's fractional scenarios", Backlog).**
  - **The facts:**
    - The suites run with feature path `./` under `test-harness`, so godog reads every `.feature` file: `gherkin/targeting.feature`'s fractional outlines and `evaluator/gherkin/fractional.feature`.
    - Both tag sets exclude `@fractional-v1` and `@fractional-v2`, the 21 scenarios #47 is about. The other fractional scenarios (`@fractional-v3` examples, `@fractional-nested`, the shared-seed outline) run today and pass.
    - Step 13 keeps the tag sets unchanged, and both images run with the same tags, so #47's failures cannot show up as a difference between them.
  - **Recommendation:** #47 does not have to land first.
    - The `@fractional-v1` and `@fractional-v2` exclusions stay in `test/integration`'s tag sets, owned by #47.
    - #47's definition of done gains one line: remove the two exclusions from `TestRPC` and `TestInProcess` too, not only from `test/evaluator-testkit`. The orchestrating session edits the issue.
    - Step 13 adds no new fractional exclusion.
  - *Alternative:* land #47 first. That couples step 13 to an evaluator change that alters bucketing, a C-003 question of its own.

- **Q20, flagd-full's file source after a deletion. (new in revision 2)**
  - **The facts:** the opposer's blocker shows that flagd-full's `file` source stops watching a file that has been gone for more than about 1 s. Recreating the file is then never picked up, and only a restart recovers. The testbed sidesteps this with a fresh runtime per start (Q3), but an operator who replaces a flag file by delete-then-write with a gap meets it too.
  - **Recommendation:** keep flagd-full's behaviour in step 13 (drop-in). Open an issue in this repository asking whether the `fileinfo` and `fsnotify` triggers should keep polling a vanished path. That would be new behaviour behind opt-in configuration, per the drop-in rule.
  - *Alternative:* change the triggers now. That changes observable flagd-full behaviour, and the testbed doesn't need it.

## 1. Scope

### UC-022: Run Provider Test Suites Against the Testbed (draft for PR S)

- **Primary actor:** Provider Author. **Secondary actor:** CI.
- **Goal:** run a provider's Gherkin suite against the flagd being released, with outages and faults the suite controls, without changing the suite.
- **Trigger:** a provider suite starts the testbed through the flagd-testbed compose file.
- **Preconditions:** the testbed image is built from `distributions/flagd-testbed`, and the flagd-testbed data (flags, configs, certificates, compose file, Gherkin) is the pinned `test-harness`.

**Main success scenario:**
1. CI starts the compose stack with the testbed image.
2. The testbed combines the raw flags into `/flags/allFlags.json` and builds a runtime from `configs/default.json`. Once it is ready, it opens 8013–8016; the control API on 8080 is always up.
3. The suite calls `/start?config=default` in every scenario. The testbed closes the gate, replaces the runtime with a fresh one, opens the gate once that runtime is ready, and answers.
4. The suite evaluates flags over RPC, in-process sync, OFREP or the file.
5. The suite calls `/change`. The testbed toggles `changing-flag`, regenerates the file, waits until the runtime's store holds the new variant, and answers.
6. The suite calls `/restart?seconds=N`. The testbed answers at once, deletes `allFlags.json`, closes the gate and cancels the runtime; providers see refused connections and broken streams. After N s it starts as in step 3.
7. The suite's providers reconnect and fire their ready events; the suite passes the same scenarios as against the launchpad image.

**Alternative flows:**
- **A1 Configuration switch:** `/start?config=<other>` builds the fresh runtime from that file (the same path as step 3).
- **A3 Invalid seconds:** `/restart?seconds=-1` answers `400`.
- **A4 Stop:** `/stop` closes the gate and cancels the runtime until the next `/start`.
- **A5 Fault injected:** `POST /faults`; requests on the chosen endpoint get latency, an error, an aborted stream or refusal with the port open. `DELETE /faults` or `/start` clears it.
- **A6 Fetch triggered:** `POST /fetch?source=` makes a fetcher source fetch now.
- **A7 Change times out:** `/change` answers a plain-text `500` as the launchpad does.
- **A8 Not ready in time, unknown configuration included:** `/start` answers `500 … flagd health check timed out` after 10 s and leaves the gate closed. The configuration it named is remembered.

**Business rules:**
- **BR-001 Same API:** the four launchpad calls behave as flagd-testbed v3.11.0's, for any HTTP method, byte for byte.
- **BR-002 Outages close sockets:** stop and restart close listeners and connections with a plain close and no application-level goodbye. The control port stays up.
- **BR-003 Fresh runtime per start:** every start builds a new runtime, and the gate opens only on a ready one.
- **BR-004 Faults are opt-in and additive:** no fault is active unless set, and outages never use the middleware.
- **BR-005 Test-only:** testbed components appear in no standard distribution (NFR-015).
- **BR-006 Lockstep:** the testbed contains flagd-full's sync providers, configuration format, evaluator and service endpoints.
- **BR-007 Data stays upstream's:** flags, configs, certificates, the compose file and Gherkin come unmodified from the pinned `test-harness`.

**Realizes:**

| Requirement | Delivered by |
|---|---|
| FR-056 | B1: control API; C and I: contract |
| FR-057 | G1, G2, L; B1 wires them |
| FR-058 | F, B2 |
| FR-059 | T, B2 |
| NFR-014 | I: both images, same pass set |
| NFR-015 | B1: boundary rows and `TestStandardManifestsHaveNoTestbedModule` |
| UC-001's gap "no repository manifest compiles middleware with `cmd:`" | B2 (middleware and `cmd:`; event sinks stay with Q18) |

**Use case coverage:**

| Flow | Test |
|---|---|
| main 1–7, BR-001, NFR-014 | I: `TestRPC` and `TestInProcess` against both images, plus `TestTestbedContract` (C) against ours |
| main 3, 6, BR-003 | B1's `TestEveryStartIsAFreshRuntime` (a `/change`, then `/restart?seconds=8`, then `/change` again reaches the store: the blocker's scenario), `TestRestartClosesEveryRuntimePort` (8013–8016 refused, 8080 up) |
| main 6, BR-002 | G1's `TestGateRefusesWhileClosed`, `TestGateBreaksOpenStreams`, `TestGateReopensSamePort`, `TestGateCloseIsPlain` (no `provider_shutdown`, no GOAWAY, FIN on an idle connection) |
| main 5 | B1's `TestChangeWaitsForStore`; C3 byte comparison |
| A1 | B1's `TestConfigSwitch` (`ssl`: TLS on 8013) |
| A3, A8 | C's contract cases, replayed by I; B1's `TestStartNotReadyIn10s` (a runtime that never gets ready: `500` after 10 s, gate closed) |
| A4 | C, and B1's `TestStopClosesUntilStart` |
| A5, BR-004 | F's `middlewaretest.Run` and `TestFaultKinds`; B2's `TestFaultsClearedOnStart`, `TestNoFaultPassesThrough` |
| A6 | T's trigger contract run; B2's `TestFetchCallFiresTrigger` |
| A7 | B1's `TestChangeTimesOut` (a combiner that never completes: the exact plain-text `500`) |
| BR-005, BR-006 | `TestStandardManifestsHaveNoTestbedModule`, `TestTestbedLockstepWithFlagdFull` |
| BR-007 | B1's Dockerfile copies only from `test-harness`; `TestImageDataMatchesSubmodule` also starts the built image with `/start?config=default` |

**Leaves out:**
- restructuring upstream flagd-testbed;
- Java, Python and JavaScript suites in CI (Q14);
- an evaluation-recording sink (Q18);
- tracing scenarios (#59, Q4);
- the `@fractional-v1` and `@fractional-v2` scenarios, excluded as today and owned by #47 (Q19);
- changing flagd-full's file source after a deletion (Q20);
- publishing the image.

## 2. Characterization (PR C, base `main`, parallel now)

The oracle is today's image: `test-harness/flagd/Dockerfile` over `flagd/build.Dockerfile`, as `test/integration` builds it.

| Pin | Test | Pins |
|---|---|---|
| C1 | `test/testbed` `TestTestbedContract/api` | For `/start`, `/stop`, `/restart` and `/change`, with GET and POST: status code, `Content-Type`, the exact body including the trailing newline. `/restart?seconds=x` and `-1`. `/change` three times (foo, bar, foo). `/start?config=nope` takes at least 10 s and answers the "health check timed out" `500`. Then `/restart?seconds=0` and a bare `/start` fail the same way (the remembered configuration), and `/start?config=default` recovers. This case runs last |
| C2 | `TestTestbedContract/outage` | During `/stop` and the delay of `/restart?seconds=3`: 8013, 8014, 8015 and 8016 refuse connections, and 8080 answers. An idle `EventStream` on 8013 and an idle `SyncFlags` stream on 8015: the error each client sees (EOF or reset) is recorded, and no `provider_shutdown` arrives first. After the delay `/readyz` is 200 and both streams can be reopened |
| C3 | `TestTestbedContract/files` | `/flags/allFlags.json` exists at start, is gone during a restart, and is back afterwards byte-identical. After each `/change`, `rawflags/changing-flag.json` and `allFlags.json` are byte-identical to a recorded golden |
| C4 | `TestTestbedContract/configs` | `/start?config=ssl` serves TLS on 8013 with the custom root CA; `sync-payload` sends sync without metadata; `metadata` serves only the metadata flag set |
| C5 | `TestTestbedContract/change-after-restart` | `/change`, `/restart?seconds=8`, `/change`: an RPC evaluation of `changing-flag` follows both toggles (the blocker's scenario, green on today's image) |

The contract test is a Go module of its own (`test/testbed`), parameterized by image. C runs it against the launchpad image; I adds the relay testbed.

## 3. Steps

Every commit builds and passes `make test`. A change to flagd-full's observable behaviour is a defect here: G2 and L must be invisible.

| PR | Model | Base | Files | Change | Check |
|---|---|---|---|---|---|
| S | mechanic | `main` | `docs/use_cases/UC-022-*.md`, `docs/requirements.md`, `docs/use_cases.puml`, `docs/glossary.md` (Testbed, Launchpad API, Listener Gate) | Section 1's UC-022 and requirements | spec lint |
| C | implementer | `main` | `test/testbed/` (module, `contract_test.go`), `Makefile` target `testbed-contract` | Section 2 against the launchpad image | green locally in WSL and in `integration-test` |
| G1 | implementer | `main` | `service/transport/gate/` | `Gate.Listen(network, address)` returns a virtual listener and registers the address; `Open()` binds every registered address; `Close()` closes the OS listeners and every tracked connection with a plain `Close()`. `Accept` blocks while closed; closing a virtual listener releases its address | `TestGateRefusesWhileClosed`, `TestGateBreaksOpenStreams` (h2c ConnectRPC stream), `TestGateReopensSamePort`, `TestGateCloseIsPlain`, `TestGateUnixSocket`, `TestGateReleasesAddressesOfAClosedServer`; `-race` |
| G2 | implementer | `main` | `core/pkg/service` (`Settings.Listen`), `core/pkg/distribution` (`Components.Listen`), `runtime` (passes it; management port), `service/flag-evaluation`, `service/flag-sync`, `service/ofrep`, `service/event-collector` | Q8 | existing port, TLS, socket and serve-error pins unchanged; `TestListenSeamUsedByEveryEndpoint` (a recording `Listen`; names `ConnectService.Serve`'s management server as the exception); `make check-flagd-full`; acceptance; upgrade differential; allocs/op |
| L | implementer | G2 | `runtime` (`Runtime.Run(ctx)`; `Start` keeps its signal handling; `Runtime.Ready() <-chan struct{}` if `Initialized` is not enough), `runtime/cmd` (`LoadConfig(path, components)` over a private viper, sharing the flag-to-setting table with `start`) | Q3's seams | `TestRunStopsOnContext`; `TestFromConfigRepeatedly` (200 sequential build, run and cancel cycles with noop telemetry on the same ports: no goroutine growth (`goleak`), no file-descriptor growth (`/proc/self/fd`), no registration error); `TestRebuildBudget` (build to ready under 1 s for `default` on CI's runner); `TestLoadConfigMatchesStart` for each `test-harness` config; `start_config.golden` unchanged |
| F | implementer | `main` | `testbed/fault/` (module) | Q11's middleware; a `Controller` owned by the testbed `cmd`, looked up by name from the middleware's settings | `middlewaretest.Run`, `TestFaultKinds` on ConnectRPC, gRPC and OFREP routes, `TestNoFaultPassesThrough` |
| T | implementer | `main` | `testbed/trigger/` (module) | Q10's trigger type `testbed`, fired by a `Controller` | the trigger contract run; `TestFireFetchesNow` on a file and an `http` source |
| B1 | implementer | G1 + L | `testbed/cmd/` (module: control server, the launchpad's combiner and narrow-struct toggle ported as they are, gate wiring, fresh runtime per start, remembered configuration), `distributions/flagd-testbed/manifest.yaml` (without fault and trigger), `Dockerfile` (`WORKDIR /`), `test/distributions` (lockstep, boundary and image tests), `testdata/module-boundaries.txt` | Q2–Q4, Q6, Q7, Q9, Q12, Q13, Q15 | B1's tests in section 1; `relay-builder` builds the manifest in `make test-distributions`; `docker build` in WSL; C's contract test against the built image |
| B2 | implementer | B1 + F + T | `testbed/cmd` (`/faults`, `/fetch`), the manifest entries for `testbed/fault` and `testbed/trigger` | Q10, Q11 | B2's tests in section 1; C's contract unchanged |
| I | implementer | B1 + C | `test/integration/integration_test.go` (image chosen by an environment variable; the default stays the launchpad image), `test/testbed` (second image), `.github/workflows/build.yaml` (the relay testbed run with Q14's filter), `Makefile` (`testbed-rpc-outages`, Q16) | Q14, Q16 | both images pass `TestRPC` and `TestInProcess` with today's tags; the pass-set comparison; `TestTestbedContract` against both, C2's recorded stream errors equal |
| X | mechanic | I + B2 | `docs/use_cases/UC-022`, `docs/relay/testbed.md`, `docs/use_cases/UC-001` (gap closed), requirement statuses; #110; the issue of Q20 | Status `Implemented` | spec lint |

`test/integration` stays the orphaned module it is. The suites are not edited; only the image under them changes.

## 4. Gates

In WSL from the repository root (`export GOTOOLCHAIN=go1.27.3`; in a worktree `export GOWORK=$PWD/go.work`; for lint `export GOPATH=$(go env GOPATH)`). CI is the full run; implementers run their packages' tests with `-race`.

| Gate | Command | PRs |
|---|---|---|
| Unit tests | `make workspace-init && make test` | all |
| Lint, boundaries | `make lint check-module-boundary check-package-coverage`; a new row forbids `R/testbed/...` in every module except `testbed/*`, `test/*` and `distributions/flagd-testbed` | all |
| flagd-full unchanged | `make check-flagd-full`, CI's upgrade differential, `make flagd-integration-test` | G2, L |
| Benchmarks | CI's allocs/op comparison | G2, F |
| Testbed contract | `make testbed-contract IMAGE=<image>` | C, B1, I |
| Provider suites | `make flagd-integration-test` with `TESTBED_IMAGE=relay` and with the default | I |
| Spec | `spec_lint.py --docs docs` on Windows | S, X |

## 5. Risks

- **R1, gate fidelity against SIGKILL.** Providers' reconnect paths distinguish `UNAVAILABLE`, `CANCELLED`, EOF and reset. The gate's plain close matches a killed process's FIN, and RST with unread data (Q6). C2 records what idle streams see on today's image, and I compares ours with it. The in-process `@reconnect`, `@events` and `@grace` scenarios are the acceptance.
- **R2, a reentrant `FromConfig` at scale (the biggest).** Q3 builds one runtime per scenario, a few hundred per suite run. Hidden process-global state (viper, the logger, `fips`, OpenTelemetry globals, Prometheus), leaked goroutines or file descriptors would break a later runtime or exhaust the container.
  - **Tests:** L's `TestFromConfigRepeatedly` (200 cycles, leak checks) and `TestRebuildBudget`.
  - **Fallback:** if they cannot be made green, Q3's child-process alternative, which changes only B1.
- **R3, timing.** A start costs N s plus an in-process build, against the launchpad's N s plus a process start, so the margins are close to today's. The `@grace` "lost for 1s, no error" scenario needs the rebuild well inside the provider's grace period; `TestRebuildBudget` keeps it under 1 s. I's pass-set comparison catches a scenario that turns timing-sensitive.
- **R4, rebinding.** Opening right after closing relies on `SO_REUSEADDR`, which Go sets on Linux. G1's `TestGateReopensSamePort` runs 100 cycles.
- **R5, G2 and L touch flagd-full's listeners and runtime.** Bind error texts, unix socket permissions, TLS loading and `Start`'s signal handling must stay byte-identical. The serve-error, startup and shutdown pins plus the upgrade differential cover them.
- **R6, CI time.** The relay testbed image and two more suite runs add about 8 minutes to `integration-test` whenever Q14's filter matches, which is most Go changes. The per-scenario rebuild adds well under a second per scenario.
- **R7, upstream drift.** Renovate moves `test-harness` and the Go suite. A new launchpad call upstream fails the differential until the testbed implements it, which is the intended alarm.
- **R8, containment.** Nothing goes upstream. The ported combiner and toggle are Apache-2.0 code from the same organization; the files say where they come from.

## 6. Mapping: launchpad to relay pieces

| Launchpad / testbed piece | Relay piece | PR |
|---|---|---|
| flagd binary at `/flagd` | the testbed distribution binary (flagd-full's components, noop telemetry) | B1 |
| launchpad process, port 8080 | `R/testbed/cmd`: control server outside the runtime | B1 |
| kill and re-exec on every `/start` | gate close, cancel the runtime, build a fresh one (`FromConfig`, `Runtime.Run`), gate open when ready | G1, L, B1 |
| `/stop` | gate close, cancel the runtime | G1, B1 |
| `/restart?seconds=N` | gate close, delete `allFlags.json`, cancel; after N s, a fresh start | G1, B1 |
| SIGKILL socket behaviour | plain close of listeners and tracked connections, no `Shutdown` | G1 |
| every flagd port (8013–8016, unix socket) | `Components.Listen` through the gate | G2 |
| `configs/*.json` passed to `flagd start --config` | `runtime/cmd.LoadConfig` (flagd's own configuration keys) | L, B1 |
| `/change`, its watcher and combiner | ported combiner and narrow-struct toggle; the runtime's file sync; wait on the store | B1 |
| `/ssl` certificates | the same Dockerfile certificate stage | B1 |
| compose file, envoy, `FLAGS_DIR` volume, healthcheck | unchanged, from `test-harness` | — |
| (new) latency, errors, aborts, refusal with the port open | fault middleware, `/faults` | F, B2 |
| (new) immediate fetch of a fetcher source | testbed fetch trigger, `/fetch` | T, B2 |

## 7. PR split

| PR | Branch | Base | Runs |
|---|---|---|---|
| S | straight to `main` (spec) | `main` | now |
| C | `uc-022/pr-1-testbed-contract` | `main` | now |
| G1 | `uc-022/pr-2-listener-gate` | `main` | now |
| G2 | `uc-022/pr-3-listen-seam` | `main` | now |
| L | `uc-022/pr-4-runtime-lifecycle` | G2 (both edit `runtime`) | once G2's branch exists |
| F | `uc-022/pr-5-fault-middleware` | `main` | now |
| T | `uc-022/pr-6-fetch-trigger` | `main` | now |
| B1 | `uc-022/pr-7-testbed-distribution` | G1 + L | once both branches exist |
| B2 | `uc-022/pr-8-faults-and-fetch` | B1 + F + T | after B1, beside I |
| I | `uc-022/pr-9-suites-in-ci` | B1 (+ C merged) | after B1 |
| X | `uc-022/pr-10-close` | I + B2 | last |

Every PR description starts with `Part of #110`; S and X also name the UC-022 issue once it exists.

## 8. Changelog (revision 2, `step-13-opposer.md`)

| Finding | Severity | Outcome |
|---|---|---|
| 1 A gate-only restart leaves `allFlags.json` unwatched | blocker | Adopted. Verified on `main`: `sync/file`'s `fileinfo` trigger gives up re-adding about 1 s after a removal and watches nothing afterwards. Q3 now builds a fresh runtime on every start, Q5 is merged into it, and BR-003 is "fresh runtime per start". C5 and B1's `TestEveryStartIsAFreshRuntime` pin the scenario. The trigger change is kept out of step 13 as Q20 |
| 2 `/start` runs once per scenario | major | Adopted. "What exists" says so; L tests 200 cycles with leak checks and a 1 s budget; R2 and R3 are restated |
| 3 `SetLinger(0)` makes the gate less like SIGKILL | major | Adopted. Plain `Close()`; C2 records idle-stream errors on today's image, and I compares them |
| 4 A7 and A8 have no failing test; A2 is A8 | major | Adopted. A2 is merged into A8 with the "health check timed out" text after 10 s; B1 adds `TestChangeTimesOut` and `TestStartNotReadyIn10s` with injected seams |
| 5 B waits on F and T; G2 and L collide | major | Adopted. B is split into B1 (on G1 and L) and B2 (on B1, F and T); I depends on B1 and C; L stacks on G2 |
| 6 `/start` remembers a failed configuration | minor | Adopted. Q12 and C1 (runs last, recovers with `default`); B1 keeps the order |
| 7 File sources take triggers; `/change` wait | minor | Adopted. Q10's premise corrected; Q9 waits on the store holding the new variant |
| 8 `/change`'s narrow struct | minor | Adopted. Q12; C3 compares both files byte for byte; B1 ports the struct as it is |
| 9 Working directory `/` | minor | Adopted. Q13 (`WORKDIR /`); the image test starts `default` |
| 10 Non-gating job goes unread | minor | Adopted. Q16 is a manual `make` target |
| 11 Change filter misses the restart path | minor | Adopted. Q14 builds the relay testbed for any Go change outside `docs/` and `.claude/`, plus the upgrade filter |
| 12 Second listener in flag evaluation | minor | Adopted. Q8 names `ConnectService.Serve`'s management server as outside the seam, and the seam test names the exception |
| 13 Envoy 9901; healthcheck cadence | minor | Adopted. "What exists" and Q7 corrected |
