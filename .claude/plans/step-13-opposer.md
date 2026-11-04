# Step 13 plan: opposer findings (revision 1)

Plan: `.claude/plans/step-13.md` (revision 1, `main` @ 50ba75bb).

Checked against the upstream sources at the pinned versions:
- flagd-testbed **v3.11.0** (9d8dace5): `launchpad/` (`main.go`, `handlers/http.go`, `pkg/flagd.go`, `pkg/file.go`, `pkg/json.go`), `launchpad/configs/`, `flagd/Dockerfile`, `docker-compose.yaml`, `gherkin/`;
- the Go suite `tests/flagd/v2` **v2.3.0** (`testframework/testcontainer.go`, `testbed_runner.go`, `provider_steps.go`);
- `test/integration` (tag sets, `go.mod`).

On this repository's `main`: `sync/file` (`fetcher.go`, `trigger.go`, `fileinfo_watcher.go`, `watcher.go`), the listeners in `service/*` and `runtime/management.go`, `docs/requirements.md` and `docs/use_cases.puml`.

Counts: **1 blocker, 4 major, 8 minor.**

## What holds (verified)

- **The pinned versions and tag sets.** `test/integration/go.mod` pins `providers/flagd v0.8.0` and `tests/flagd/v2 v2.3.0`. The `test-harness` gitlink is 9d8dace5, tagged v3.11.0. `rpcTags` and `inProcessTags` (`integration_test.go:51-52`) are exactly the plan's lists. So the RPC run has no outage scenario, and the in-process run has `@reconnect`, `@events`, `@grace` and `@targetURI`.
- **The launchpad calls and their texts** (`handlers/http.go`): the JSON shape `{"status","message"}` via `json.NewEncoder` (with a trailing newline), the four success messages, the `400` text for `seconds`, the plain-text `http.Error` `500` of `/change`, and `/restart` answering before the delayed start. They match the plan's table, apart from findings 5 and 8.
- **The launchpad's mechanics** (`pkg/flagd.go`, `pkg/file.go`, `pkg/json.go`):
  - `/restart` takes the lock, cancels a pending restart, **deletes `flags/allFlags.json`**, kills flagd, and after N s calls `StartFlagd(Config)`.
  - `StartFlagd` regenerates the file if it is missing, restarts the watcher, kills and re-execs flagd, and polls `:8014/readyz` every 100 ms for 10 s.
  - `/change` waits up to `DefaultRestartTimeout` (5 s) for the `rawflags/` watcher to regenerate the file.
  - The combiner skips `selector-*` files, deep-merges the rest, and writes atomically by rename.
- **The configs:** `default`, `ssl`, `sync-payload` and `metadata` are as described. No `socket.json` exists.
- **The image** (`flagd/Dockerfile`): busybox; `COPY --from=flagd /flagd-build /flagd`; `ENTRYPOINT ["./launchpad"]`; `rawflags/`, `configs/` and `ssl/` as described; the certificate stage signs with `ssl/custom-ca.key`.
- **The compose file:** 8013–8016 and 8080; the volume `${FLAGS_DIR}:/flags`; the `wget --spider …:8014/healthz` healthcheck; envoy's 9211 routes by authority to `flagd:8013`/`flagd:8015`, with TCP health checks every 1 s, unhealthy threshold 1; 9212 is a `403 PERMISSION_DENIED` direct response; envoy `depends_on` a healthy `flagd`.
- **The Go suite's use of it:**
  - every call is a `GET`;
  - it waits for `8080/tcp` (60 s), then maps 8013, 8014, 8015, 8080, 9211 and 9212 (8016 is not mapped);
  - `unavailable` uses `127.0.0.1:9999` and `forbidden` uses 9212;
  - "the connection is lost for Ns" is `/restart?seconds=N`, and "the flag was modified" is `/change`;
  - `/stop` is never called.
- **Sound recommendations:**
  - Q2: the control endpoint outside the runtime.
  - Q4: noop telemetry, because no pinned scenario asserts a metric or span.
  - Q6: abrupt close, with no `Server.Shutdown` and no GOAWAY (see finding 3 for linger).
  - Q7: the gate covers the management port, so Docker's and envoy's health checks see the outage as today.
  - Q8: an injected `Listen`, not a global.
  - Q15: the lockstep test.
  - Q17: the free IDs. FR-055 and NFR-013 are the last used, and UC-022 is free. The `provider`, `harness` and `ci` actors exist in `use_cases.puml`.
  - Q19: #47 doesn't block, because the tag sets are the same under both images.

## Blocker

### 1. A gate-only restart leaves `allFlags.json` unwatched, so every later change fails (Q3, Q5, R3)

- **Step:** Q3 ("same configuration: a gate cycle"), Q5, BR-003, R3, PR B.
- **Evidence:**
  - **The plan's premise is wrong.** "`file` is a streaming sync provider (fsnotify) and takes no trigger" isn't true on `main`. Since UC-003, `sync/file` is a **fetcher** with fetch triggers (`fetcher.go`). Outside Kubernetes, which is the compose case (`KUBERNETES_SERVICE_HOST` unset), its default trigger is **`fileinfo`**: a poll of the file's status every 1 s (`DefaultTriggers`, `defaultFileInfoPollIntervalMs = 1000`).
  - **When the file disappears**, `fileInfoWatcher.update` sends `Remove` and **deletes the path from its watches**. `watchTrigger.handle` then tries `reAdd`: `reAddMaxAttempts = 6` with `reAddBackoff = 200 ms`, so about 1 s. Then it logs "error restoring watcher" and signals once. The fetch reads a missing file, which is "no data", so the store gets `{}`.
  - **After that the trigger watches nothing.** Its `Run` loop keeps running on a watcher whose watch list is empty, and no event ever arrives again. Nothing re-adds the path. A file source has no poller, because its default triggers are the file watch only.
  - **The pinned outages are 6 s and 8 s** (`connection.feature:29,85,97`, `events.feature:11,21`, `contextEnrichment.feature:20,33`, `rpc-caching.feature:53`, `sync-payload.feature:19`), and `/restart` deletes the file for that whole time. Re-adding gives up after about 1 s.
  - **So, in the gated runtime:**
    - after the first `/restart`, `allFlags.json`'s source stays at `{}` even though the testbed has regenerated the file;
    - BR-003's wait for acceptance never completes, and the gate never reopens;
    - if it reopened on a timeout, every flag of `allFlags.json` would be missing;
    - every later `/change` would also go unseen. In `events.feature`, "Flag change event" (line 37) runs right after the two 8 s outages, in the same in-process run.
  - **Today this can't happen:** the launchpad kills flagd, and the new process opens the regenerated file with a new watch.
- **Fix:** take Q5's alternative, and make it the only path.
  - **Close:** `/stop`, `/restart`, and `/start` before it rebuilds. The gate closes abruptly (Q6) and the old runtime is cancelled.
  - **Open:** every `/start` (any configuration) and the delayed start of `/restart`. The testbed regenerates the file as the launchpad does, builds a **new** runtime with `FromConfig` from `configs/<name>.json`, runs it, and reopens the gate once `/readyz` is 200.
  - **What this changes in the plan:**
    - Q3's two cases become one, because a same-configuration `/start` is a rebuild too.
    - Q5's list of "known differences" is empty: the store, versions and ETags are fresh, as from a new process.
    - R3 goes away.
    - BR-003 becomes "the gate reopens only on a ready runtime", which `/readyz` already means (every source accepted).
    - The gate is still what makes the close abrupt and the ports refuse during the outage, so G1 stays.
    - L (reentrant `FromConfig`, `Runtime.Run(ctx)`) moves from "only for a config switch" to the main path; see finding 2.
  - **The alternative:** fix the file trigger to keep polling a vanished path. That changes flagd-full's observable behaviour (a file recreated after a deletion would be picked up), so it's a drop-in question for the project lead, not a testbed detail. It also leaves Q5's state differences.

## Major

### 2. The suite calls `/start` in every scenario, so the start path is the hot path (Q3, Q5, R4, R7, L)

- **Step:** "What exists" (the runner skips `/start`), Q3, Q5's alternative ("a full startup per scenario"), R4, R7, L's tests.
- **Evidence:**
  - The skip ("default and `/readyz` is 200") is only in `TestbedRunner`'s setup, once per suite (`testbed_runner.go:127-133`).
  - Every "a <type> flagd provider" step calls `triggerTestbedConfiguration`, which **always** calls `StartFlagdWithConfig`: `default` for every type except `ssl`/`tls` and `socket` (`provider_steps.go:258-283`). So every scenario starts with `/start?config=default`. The launchpad answers that with a SIGKILL and a new process each time.
  - **The budget:** `StartFlagdWithConfig` uses a 10 s HTTP client timeout and then waits 15 s for `/readyz`. The `@grace` scenario "ready → stale → ready" (`events.feature:27-35`, `lost for 1s`, "the error event handler should not have been executed") needs the restart to finish inside the provider's grace period.
  - In CI every scenario uses `default`, because `ssl` is `@customCert` and `socket` is `@unixsocket`, both excluded. Q3's configuration switch (A1) is therefore never reached by the gating suites.
- **Fix:**
  - State in "What exists" that `/start?config=default` runs once per scenario.
  - With finding 1, every scenario rebuilds the runtime, so L must prove that this is repeatable at scale, not just twice:
    - `TestFromConfigRepeatedly`: about 200 sequential build, run and cancel cycles on the same ports, with no goroutine or file-descriptor growth (`goleak`, or a count of `/proc/self/fd`) and no global registration error;
    - a budget assertion: build to `/readyz` 200 under 1 s for `default`.
  - R4 then reads: N s plus an in-process start, which is closer to the launchpad's N s plus a process start than the plan's "exactly N s".
  - R7 should count about one rebuild per scenario.

### 3. `SetLinger(0)` makes the gate less like SIGKILL, not more (Q6, R1, G1)

- **Step:** R1's first check, G1.
- **Evidence:**
  - When a killed process exits, the kernel closes its descriptors like any `close(2)`: a FIN, and a RST only when unread data is still in the receive buffer.
  - Go's `net.Conn.Close` does exactly that.
  - `SetLinger(0)` turns **every** close into a RST, so a provider would see `ECONNRESET` where today it sees EOF on an idle stream. That is the very difference R1 worries about ("providers' reconnect paths distinguish `UNAVAILABLE` from `CANCELLED` and EOF"), introduced on purpose.
- **Fix:**
  - G1 closes the tracked connections with a plain `Close()`, with no linger change. Closing the raw connection under the server already sends no GOAWAY, which is the point of Q6.
  - C2 records what an idle `EventStream` and `SyncFlags` stream see against the launchpad image (EOF or reset), and I's differential compares it.
  - Drop the `SetLinger` line from R1.

### 4. A7 and A8 have no test that can fail, and A2 is A8 (section 1 coverage, C)

- **Step:** the use case coverage row "A2, A3, A7, A8: C's contract cases, replayed by I".
- **Evidence:**
  - **A7 ("/change times out"):** black-box against today's image, nothing makes the launchpad's watcher miss the 5 s window. C can't provoke it, so it pins nothing.
  - **A8 ("not ready in 10 s"):** the only black-box way to provoke it is a configuration whose flagd never gets ready. `/start?config=nope` is that case: flagd exits on the missing file, and the launchpad times out after 10 s with `500 {"status":"error","message":"Failed to start flagd: flagd health check timed out"}` (`flagd.go:116-121`, `http.go:24`). So A2 and A8 are one launchpad path with one text, and A2 doesn't "answer 500" at once.
- **Fix:**
  - Merge A2 into A8 in UC-022, or say that A2 answers as A8 does, after 10 s.
  - C pins that response, its timing (at least 10 s), and the follow-up in finding 5.
  - B gets two unit tests with injected seams: `TestChangeTimesOut` (a combiner that never completes: plain-text `500`, the exact `http.Error` text) and `TestStartNotReadyIn10s` (a runtime whose readiness never turns: `500` after 10 s, gate closed).

### 5. B waits on F and T, which the drop-in proof doesn't need (Q1, section 7)

- **Step:** Q1, section 3's B row, section 7.
- **Evidence:**
  - B stacks on five branches: G1, G2, L, F and T. I, the drop-in proof, then waits for the fault middleware and the fetch trigger, though no pinned scenario uses either (Q10, Q11).
  - A branch with five parents also has to be re-merged whenever any of them changes. The stacked-PR drill in this repository (merge breaks the PRs behind it) makes that the slowest part of the step.
  - G2 and L both edit `runtime` in parallel: `management.go` and the endpoint setup for G2, `runtime.go` for L. That is a guaranteed rebase.
- **Fix:**
  - Split B into **B1** (the control API, the gate wiring, the rebuild, the Dockerfile and the manifest without `testbed/fault` and `testbed/trigger`, on G1, G2 and L) and **B2** (the `/faults` and `/fetch` wiring and the manifest entries, on B1, F and T).
  - I depends on B1 and C only.
  - The critical path stays G2 → B1 → I → X, and F, T and B2 run beside it.
  - Order G2 before L in `runtime`, or have L rebase on G2, and say so in section 7.

## Minor

### 6. `/start` remembers a configuration even when it fails (Q12, C1)

`StartFlagd` sets `Config = config` **before** it tries (`flagd.go:84-88`). After `/start?config=nope`, a later `/restart` and a `/start` without `config` use `nope` again. Q12's "byte-compatible" includes that. C1 must end its `nope` case with `/start?config=default`, or later cases fail for the wrong reason. B keeps the same order, and a C1 case pins it: `/start?config=nope`, then `/restart?seconds=0`, then `/start`, all failing until `default` is named.

### 7. Q10's premise: file sources do take triggers (Q9, Q10)

As in finding 1, `file` is a fetcher, and an extra trigger can be attached to it. Correct the premise in Q10 and in "What exists". The recommendation (a small, opt-in `testbed` trigger) still holds, and it now applies to file sources as well as `http` and blob sources.

Q9 should also say how long the wait takes. The runtime notices `/change`'s rename on its next `fileinfo` poll, within 1 s, and the 5 s limit covers that. Wait on the store holding the new `defaultVariant` rather than on a change event: the store is what the provider reads next.

### 8. `/change` rewrites `changing-flag.json` through a narrow struct (Q12, C3)

`ToggleChangingFlag` unmarshals into a `FlagConfig` that keeps only `state`, `variants` (as `map[string]string`) and `defaultVariant`, and re-marshals with two-space indentation (`file.go:16-22, 64`). After the first `/change`, the file loses any other field, and `allFlags.json` changes with it. B's port must keep this struct as it is, not "improve" it. C3 should compare `allFlags.json` byte for byte after `/change`, not just the variant.

### 9. The testbed must run with `/` as its working directory (Q13)

The configs name `flags/allFlags.json` and `rawflags/selector-flags.json` relative to the working directory. The launchpad runs `./flagd` with `./configs/<name>.json` from `/` (busybox's default `WORKDIR`). B's Dockerfile should set `WORKDIR /` explicitly, and `TestImageDataMatchesSubmodule` should also check that `/start?config=default` succeeds in the built image.

### 10. Q16's non-gating RPC outage job will go unread

A permanent job that "reports the difference" but never fails costs runner time on every matching PR, and nobody watches it. Make it `workflow_dispatch` only, or a `make` target with the documented manual run, like the other languages' suites in Q14.

### 11. Q14's change filter misses the restart path

"The testbed modules, `service/transport`, the endpoints or the runtime" leaves out `sync/file`, the fetching sync provider in `core/pkg/sync`, the store and `configformat/flagd`. Finding 1 shows those decide whether the testbed survives a restart. Use `upgrade-test`'s pattern plus the testbed paths, or run the relay testbed on every PR that touches Go code.

### 12. G2 has a second listener in flag evaluation

`ConnectService.Serve` (the embedder path) starts its own management server with `ListenAndServe` (`connect_service.go:146-148, 317-330`). The runtime path (`serveEndpoint`) doesn't use it, but G2's `TestListenSeamUsedByEveryEndpoint` should either cover it or name it as outside the seam.

### 13. Two facts to correct in "What exists"

- Envoy also publishes **9901** (its admin port).
- The compose healthcheck runs every 10 s with 3 retries and a 30 s start period, so an 8 s outage doesn't make the container unhealthy. Only envoy's 1 s TCP checks see it. That supports Q7, but the plan's text implies Docker's healthcheck "sees the outage as today", and with these settings it doesn't see it at all.

## Changes to the project lead's open questions

| Question | Change | Finding |
|---|---|---|
| Q1 | Split B into B1 (drop-in, on G1, G2 and L) and B2 (faults and fetch, on F and T); I depends on B1 and C | 5 |
| Q3 | One path: every `/start` rebuilds the runtime; there is no gate-only cycle for the same configuration | 1, 2 |
| Q5 | Take the alternative: a fresh runtime on every `/start` and every delayed restart; the list of differences goes away | 1 |
| Q6 | Abrupt close without `SetLinger(0)` | 3 |
| Q10 | The premise changes (file sources take triggers); the recommendation stands | 7 |
| Q12 | A2 answers as A8 does (after 10 s, the same text); the failed configuration is remembered | 4, 6 |
| Q16 | Manual only, not a job on every matching PR | 10 |

Q2, Q4, Q7, Q8, Q9 (with finding 7's wait condition), Q11, Q13 (with finding 9), Q14 (with finding 11's filter), Q15, Q17, Q18 and Q19 stand as recommended.
