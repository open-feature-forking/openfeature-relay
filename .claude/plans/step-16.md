# Step 16 plan: flagd-minimal stays correct under concurrent OFREP, RPC and sync clients (issue #183)

**Status: revision 2. Four questions are open for the project lead (section 0).**
- Revision 2 resolves `step-16-opposer.md` (0 blockers, 4 major, 8 minor; section 8). Every finding was adopted.
- The opposer found that all four recommendations stand; the fixes change how they are carried out, not the choice.

**Decided by the project lead (#183):**
- **Goal:** correctness under concurrency, pass/fail. Not a benchmark: no latency or throughput threshold.
- **Load:** many OFREP, RPC evaluation and in-process (flag sync stream) clients at once, while the flags change.
- **Pass:** every answer matches the current flags, no errors, every sync stream sees every change.
- **Tooling:** a k6 script (HTTP and gRPC) in a container.
- **CI:** a gated job (`FULL_CI` or a manual run), like `fips` and `docker-local`, plus a short smoke version.

**Hard constraints:**
- No product code changes in this step. A failure the load test finds becomes an issue with the k6 output and its own fix PR; a check is never loosened to make a run green.
- Images are pulled by digest and never pushed or logged in to (K2's `test_nothing_is_pushed_or_logged_in` holds).
- `make test` keeps working locally without Docker: the load runs only when asked for.

**Base:** `main` @ b3f7e344. Step 15 is planned and in implementation; the jobs fit its design (section 1, "CI"). Step 14 reserved NFR-016, so this step uses **NFR-017**.

**Read:**
- `distributions/flagd-minimal/manifest.yaml`: file, http and gRPC sync providers; `flag-evaluation` on 8013 (three services: `flagd.evaluation.v1`, `flagd.evaluation.v2`, `schema.v1`, each with its own `EventStream`), `ofrep` on 8016 (single and bulk, SSE on by default) and `flag-sync` on 8015 (`SyncFlags`, `FetchAllFlags`).
- `test/distributions/distributions_test.go`: `build` runs `go run ./cmd/relay-builder` from `builder` with `GOWORK=off`; tests start the binary on `freePort`s and poll `/readyz`.
- `builder/internal/gotool/gotool.go:71-93`: `env` keeps `GOFLAGS` and a set `CGO_ENABLED`, so `CGO_ENABLED=1 GOFLAGS=-race` gives a race build without a Builder change (verified by the opposer).
- `.github/workflows/build.yaml` (`fips`, `docker-local`) and K2, `.github/scripts/test_workflows.py` `test_jobs` and `test_gated_jobs`.
- `testbed/control`: `/change` toggles one flag between two values; `render.go` writes with an atomic rename.
- `sync/file/trigger.go`: a rename over the watched path gives `Chmod` (ignored while the path exists), then `Remove`, which re-adds the watch and signals. The fetcher skips unchanged content by SHA.
- The store (`core/pkg/store/store.go`): one store, one `View` per evaluation (bulk included); sync and event streams are fed by `store.Watch`, latest-state with a buffer of one.
- k6: `k6/net/grpc` `Stream` (core since v0.49.0, stable since v1.0) exposes `on`, `write` and `end`; a stream has no default deadline; `loadProtoset` runs in the init context and doesn't walk imports.
- The precedents `test/loadtest/sample_k6.js` and `flagd-proxy/tests/loadtest` stay as they are.

## 0. Questions for the project lead

- **Q1, the flag sync stream in k6.**
  - (a) **k6 core `k6/net/grpc` `Stream`.** One script for every client kind. The protoset is written from the Go generated packages flagd links, closed over every import (well-known types included). C's spike proves it first.
  - (b) An xk6 extension: our own k6 build on every run, for nothing core lacks.
  - (c) A Go sidecar holding the sync streams, k6 for OFREP and RPC only.
  - **Recommended: (a)**, with (c) as the fallback if the spike fails.

- **Q2, changing the flags during the run.**
  - (a) **The conductor rewrites the flag file by atomic rename on a fixed schedule,** as Kubernetes and `testbed/control` do.
  - (b) As (a), alternating with in-place writes. A half-written file makes "no errors" ambiguous.
  - (c) The testbed control's `/change`: one flag, and the testbed distribution instead of flagd-minimal.
  - (d) As (a), plus an `http` source, so two sources merge under load.
  - **Recommended: (a).** (d) is a follow-up.

- **Q3, what "matches the current flags" means while a change is in flight.**
  - (a) **A clock window.** An answer is correct if it matches a generation written between "`D` before the request" and "when the answer came back". `D` is the apply bound, measured on a GitHub runner in C and set to three times the worst case. The conductor checks every generation against it and reports a late one as "slow" (`apply_late`), not "wrong".
  - (b) The conductor marks each generation applied, and k6 asks it per check.
  - (c) Check only between changes; blind to the moment of the swap.
  - **Recommended: (a).**

- **Q4, where the smoke runs, and the race build.**
  - (a) **`load-smoke` on every build (60 s), `load-test` gated (10 min), both with `-race`.** The smoke's cost is measured in L before W puts it on every PR.
  - (b) The smoke gated too.
  - (c) The smoke in step 15's affected-module matrix, run only when flagd-minimal's modules change.
  - (d) As (a), with the full run on a normal build and `-race` only in the smoke.
  - **Recommended: (a).**

## 1. Design

**The module `test/concurrency`** (its own `go.mod`, in `ALL_GO_MOD_DIRS`):
- `TestMain` builds flagd-minimal once per package, with the Builder, as `test/distributions` does, with `CGO_ENABLED=1 GOFLAGS=-race`. Only when `RELAY_LOAD` is set.
- `TestConcurrency`, skipped unless `RELAY_LOAD=smoke` or `full`:
  1. Writes generation 0 into `t.TempDir()` (a native Linux filesystem, never the checkout: in WSL that would be drvfs), and the protoset.
  2. Starts `flagd start --uri file:<flags> --port --ofrep-port --sync-port --management-port` on free ports, with `GORACE=halt_on_error=1`, and waits for `/readyz`.
  3. Runs `docker run --rm --network host` with `$K6_IMAGE`, the scripts and the protoset mounted read-only, passing ports, `T0`, `P`, `D`, `ε`, the rates and the profile as environment variables.
  4. Runs the conductor beside it (below).
  5. Stops flagd with SIGTERM after k6 ends.
- **Docker Desktop** (macOS, or Windows without WSL's engine) puts `--network host` in its VM: k6 can't reach flagd, and the clock is the VM's. The harness reads `docker info` `OperatingSystem` and skips with that reason; any other network mode is refused.
- **Pass:** k6 exits 0 (thresholds below); `apply_late` is 0; no clock step; `/readyz` stayed 200; flagd exits 0 within its shutdown timeout; no `WARNING: DATA RACE` in its stderr.
- `generations.go`: the flag document of generation `g`, and its expected answers. `k6/check.js` implements the same function; `TestGeneratorMatchesFixture` and the k6 self-test share one fixture.
- `k6/load.js`, `k6/check.js`, `k6/selftest.js`.
- `K6_IMAGE` in the Makefile: `grafana/k6:<version>@sha256:<digest>`, a k6 release ≥ v1.0 and at least two weeks old, its digest recorded in the PR description.
- **Make targets:**
  - `test-concurrency` in `TEST_TARGETS`: the module's Docker-free tests (`TestGeneratorMatchesFixture`, the conductor's unit tests), so `make test` and CI's `test` run them;
  - `test-load-smoke`: `RELAY_LOAD=smoke go test -run 'TestCheckerSelfTest|TestConcurrency'`;
  - `test-load`: `RELAY_LOAD=full`, every test, the negative test and the spike included.

**The conductor:**
- Writes generation `g` at `w_g = T0 + g·P`: a temporary file in the flag file's directory, then a rename over the path.
- Holds its own sync stream and records when each generation arrives. A generation that arrives after `w_g + D` counts in `apply_late`, with its generation and lateness. A late write only arrives later, so this one check covers it; there is no separate write check.
- Compares wall time with Go's monotonic reading each second, and fails with "clock stepped" when they drift apart by more than `ε`.
- Polls `/readyz` each second.

**The window (Q3 (a)):** an answer to a request sent at `s` and answered at `e` is correct iff it equals the expected answer of some generation `g` with `lo ≤ g ≤ hi`:
- `hi = floor((e − T0 + ε)/P)`, the last generation written by `e`;
- `lo = floor((s − T0 − D − ε)/P)`, the last generation that must have been applied by `s`;
- `ε` = 100 ms is clock slack. `D` comes from C's measurement (worst case on `ubuntu-latest` with `-race`, ×3); `P > D + 2ε`, with 3 s as the starting value.
- The window holds at most two generations while `e − s < P − D − 2ε`. A slower answer gets a wider window: it is still checked, and counted in `wide_window`, which the summary reports beside the totals.
- Why it holds (verified by the opposer): one store, one `View` per evaluation, and the conductor's stream reads after the commit, so a generation it received by `w_g + D` was committed by then. Taking `s` before the call and `e` after it only widens the window, so k6's own lag can't reject a right answer.

**The flags of generation `g`,** in two flag sets, `a` and `b`, with distinct keys (the sync payload carries no flag set and is a map by key):
- `<set>-gen`: integer, value `g`;
- `<set>-str-<i>`: string, value `g<g>-<i>`;
- `<set>-bool-<i>`: boolean, default variant `(g + i) mod 2`;
- `<set>-tgt-<i>`: targeting with `ends_with` on `targetingKey`: `hit` when the key ends in `g mod 10`, else `miss`;
- `<set>-obj-<i>`: object `{"gen": g, "i": i}`;
- `a-odd-only`: present only in odd generations; in even ones the expected answer is `FLAG_NOT_FOUND`.
- About 50 flags per set. A selected stream (`flagSetId=a`) is checked by key prefix: only `a-` keys.

**The clients.** Request scenarios run at a fixed rate (`constant-arrival-rate`), set below saturation from C's measurement: the goal is concurrency, not saturation. Stream scenarios hold their streams in one iteration per VU.

| Scenario | Executor | Full | Smoke | Calls | Checks |
|---|---|---|---|---|---|
| `ofrep-single` | arrival rate | 30 VUs | 2 | `POST /ofrep/v1/evaluate/flags/{key}`, random flag and key, `expectedStatuses(200, 404)` | window; monotonic `gen`; a 404 is an answer (`FLAG_NOT_FOUND`) checked by the window |
| `ofrep-bulk` | arrival rate | 10 | 2 | `POST /ofrep/v1/evaluate/flags` | window; one generation per answer |
| `rpc-unary` | arrival rate | 30 | 3 | `Resolve{Boolean,String,Int,Float,Object}` rotated over `flagd.evaluation.v1`, `flagd.evaluation.v2` and `schema.v1`; `ResolveAll` on v1 and `schema.v1` | window, monotonic; `ResolveAll` one generation |
| `rpc-events` | `per-vu-iterations: 1` | 12 | 3 | `EventStream` held, split over the three services | `provider_ready` first; one `configuration_change` per generation written in `(connect + D, end − D)`; `keep_alive` ignored |
| `sync-held` | `per-vu-iterations: 1` | 40 | 2 | `SyncFlags` held; half with `flagSetId=a` | first payload in the window; then each payload is the same generation or the next one, in order; a selected stream holds only `a-` keys |
| `sync-churn` | per VU, looping | 10 | 2 | `SyncFlags` opened, closed after ~10 s with `client.close()` and a new `connect`; plus `FetchAllFlags` | first payload in the window; the planned close is not a `stream_error` |

**Held streams** run as `per-vu-iterations` with `iterations: 1` and a `maxDuration` past the run. Each iteration sets a `setTimeout` at `end − D`, runs its final check there, and resolves. A VU context ending fires neither `end` nor `error` in k6, so the check never waits for one.

**Event and sync streams are latest-state** (`store.Watch`, a buffer of one). A subscriber that lags more than `P` gets one event or payload for two generations. With `P` above the apply bound, that is a lagging stream, and it fails as `stream_lag` (naming the stream and the gap). NFR-017 states this meaning of "every change".

**Thresholds** (k6 exits 99 on any):
- `checks: rate==1`;
- counters at `count==0`: `wrong_answer`, `stale_answer`, `stream_lag`, `stream_error`, `http_error` (a transport error, or a status other than 200 and 404), `grpc_error` (a status other than OK, or `NOT_FOUND` where expected);
- no `http_req_failed` threshold: the expected 404s count there;
- no duration threshold. `wide_window` is reported, not thresholded.

**Profiles:** full 10 min, ~200 generations; smoke 60 s, ~20 generations. Rates and `D` from C's measurement.

**CI** (in step 15's design):
- **Two jobs in `build.yaml`,** whole-repo jobs like `fips`, outside the affected-module matrix:
  - `load-smoke`: always (Q4 (a)), `timeout-minutes: 15`, `make test-load-smoke`;
  - `load-test`: `if: vars.FULL_CI == 'true' || github.event_name == 'workflow_dispatch'`, `timeout-minutes: 30`, `make test-load`.
  - Both: checkout (no submodules), setup-go, `make workspace-init`; flagd's stderr and k6's summary uploaded as an artifact on failure.
- **Step 15's parts they rely on:** the `concurrency` group and QD's `paths-ignore`.
- **Step 15's table:** `test/concurrency` gets `test: null` with "runs in `test-concurrency`, `load-smoke` and `load-test`". Whichever of W and step 15's P1 lands second adds it. Once step 15's matrix replaces `TEST_TARGETS`, `test-concurrency` becomes the entry's `test` command instead.
- **K2:**
  - `test_jobs` gains both ids, against the list on `main` at the time (PR #168 and step 15's C3 also change it);
  - `test_gated_jobs` adds `load-test`;
  - `test_load_image_pinned`: `K6_IMAGE` carries `@sha256:`, and no workflow names a `grafana/k6` tag without one;
  - `test_nothing_is_pushed_or_logged_in` unchanged.

**Out of scope:**
- OFREP SSE (`/ofrep/v1/sse`): k6 has no core SSE client. A follow-up issue, with the sidecar of Q1 (c) as one way.
- Other distributions; TLS; a second source (Q2 (d)); flag changes faster than `P`.
- Latency, throughput and memory numbers. k6 reports them, and nothing gates on them.

## 2. Spec change (for PR S)

| ID | Requirement |
|---|---|
| NFR-017 | Under concurrent OFREP, flag evaluation and flag sync clients, while its flags change, flagd-minimal answers every request from a generation inside the change window, delivers to every flag sync stream and evaluation event stream every generation written while it is connected (a stream that lags more than one change interval fails), and shows no data race. Proven by `make test-load` (gated in CI) and `make test-load-smoke` (every build) |

The window of section 1 goes into the NFR's description in `docs/requirements.md`. No use case changes: this tests flagd's existing endpoints.

## 3. Tests

**Characterization:** no product code changes, so nothing new to pin. The endpoints' answers are already pinned by `test/distributions` and the integration suite; this step reads them under load.

**The test has to fail when it should (C):**

| Test | Proves |
|---|---|
| `k6/selftest.js`, run by `TestCheckerSelfTest` | Every check rejects a synthetic answer: one generation too old, one from the future, a bulk with two generations, a backwards `gen`, a stream that skips a generation, `a-odd-only` found in an even generation, a wrong targeting variant, a `b-` key on a selected stream, a 404 for a flag that exists. It accepts both generations at the window's edge, a 404 for `a-odd-only` in an even generation, and a repeated generation on a stream |
| `TestGeneratorMatchesFixture` | `generations.go` and `check.js` agree on the expected answers |
| `TestLoadDetectsStaleFlags` | The conductor skips one write: k6 exits 99 with `stale_answer` > 0, and the conductor reports `apply_late` for that generation |
| `TestRaceBuildInEffect` | `go version -m` of the binary shows `-race=true` |
| `TestStreamSpike` | From k6 against flagd-minimal with the protoset: one held `SyncFlags` to the end of a short run, with its final check on the timer; one churn close with `client.close()`, recording which event it fires; one `EventStream` per evaluation service |
| `TestProtosetClosed` | The protoset holds every transitive import (`google/protobuf/struct.proto` among them): `protodesc.NewFiles` accepts it |
| conductor unit tests | `apply_late` against the schedule; the clock-step check with a fake clock |

**Measurements (C, recorded in the PR):** the apply latency's worst case over 200 generations on `ubuntu-latest` with `-race`, which sets `D`; the request rate at which flagd's CPU stays below ~60 % under `-race`, which sets the rates.

**The runs:** `make test-load-smoke` green in WSL and in CI, with its cost measured cold and with step 15's cache (L); `make test-load` green on a manual dispatch, with the generation, check and `wide_window` counts in the PR description (W).

## 4. PR slicing

| PR | Model | Base | Content | Starts |
|---|---|---|---|---|
| S | mechanic | `main` (straight to `main`) | NFR-017 | after the plan is approved |
| C | implementer | `main` | `test/concurrency`: `TestMain` and the race build, the protoset, the conductor, `generations.go`, `check.js`, the self-test and the tests of section 3; one VU per scenario; the measurements that set `D` and the rates; `test-concurrency` in `TEST_TARGETS` | now |
| L | implementer | C | `load.js` with every scenario, both profiles and the thresholds; `K6_IMAGE`; `test-load-smoke` and `test-load`; the smoke's cost measured | after C |
| W | implementer | L | `load-smoke` and `load-test`; the K2 pins; the step 15 table entry if P1 has landed | after L |
| X | mechanic | W | NFR-017 status; `CLAUDE.md`'s CI line (the smoke on every build, `load-test` gated); the SSE follow-up issue; #183 | last |

The critical path is C → L → W. A failure L's full run finds is filed as an issue and fixed in its own PR before W merges; `load-test` merges green.

## 5. Gates

| Gate | Command | PRs |
|---|---|---|
| Docker-free tests | `go test -race ./test/concurrency/...` (CI's `test` through `test-concurrency`) | C, L |
| Self-test, spike, negative | `RELAY_LOAD=smoke go test -run 'TestCheckerSelfTest\|TestStreamSpike\|TestLoadDetectsStaleFlags\|TestProtosetClosed' ./test/concurrency/...` in WSL | C |
| Smoke | `make test-load-smoke` in WSL, and `load-smoke` in CI | L, W |
| Full | `load-test` on a manual dispatch of the branch | W |
| Workflow pins | `python3 .github/scripts/test_workflows.py` | W |
| Lint | CI's `lint` (the new module is linted like any other) | C, L |
| Spec | `spec_lint.py --docs docs` on Windows | S, X |

## 6. Risks

- **R1, a flaky oracle.** A false red teaches people to ignore the job. Against it: `D` measured, ×3, and checked against the schedule; a late apply reported as `apply_late` ("slow"), apart from `stale_answer` ("wrong"); a clock step named as such; capped request rates; the self-test proves each check both ways. A red run names the check, the client, the request times and the generations.
- **R2, the race build's cost.** `-race` slows flagd 2–10× and multiplies its memory. The rates come from C's measurement on the runner, not from a guess.
- **R3, real bugs found.** Likely candidates: the file watch under rename, stream fan-out to slow subscribers, the event diff. Each is an issue and a fix PR; drop-in rules (C-003) apply to the fixes.
- **R4, k6 drift.** The image is pinned by digest; a k6 upgrade re-records it, and the spike's stream-end behaviour is re-checked. The protoset comes from the linked Go packages.
- **R5, CI queue.** `load-smoke` is one more job per run; step 15's `concurrency` group and `paths-ignore` keep the count down. `load-test` runs only gated.
- **R6, clock and network.** k6 and the conductor share the runner's kernel clock and network only with Docker Engine on the same kernel (CI, WSL). Docker Desktop is detected and skipped; a clock step fails as "clock stepped".
- **R7, Docker for local runs.** Only the load targets need it; `make test` runs the Docker-free tests only.

## 7. Considered

- **The testbed control (step 13)** for the changes: one toggled flag and the testbed distribution (Q2 (c)). Its atomic rename is the pattern the conductor follows.
- **Extending `test/loadtest/sample_k6.js`:** upstream's sample, RPC only and without checks. It stays; `test/concurrency` is new.
- **A Go load generator only** (no k6): rejected by the project lead's tooling decision.
- **Shared keys across flag sets** as a case: the sync payload can't tell them apart, so the oracle would be ambiguous. Left out.

## 8. Changelog

### Revision 2 (`step-16-opposer.md`)

| Finding | Severity | Outcome |
|---|---|---|
| 1 OFREP answers `FLAG_NOT_FOUND` with 404, failing `http_req_failed` | major | Adopted. Verified `models.go:141-143`. `expectedStatuses(200, 404)`; no `http_req_failed` threshold; `http_error` counter; self-test cases for both 404s |
| 2 Held streams under `constant-vus` have no end event | major | Adopted. `per-vu-iterations: 1` with `maxDuration`; the final check on a timer at `end − D`; churn closes with `client.close()`; the spike covers both |
| 3 Unthrottled VUs and the `ε` write check make runner load red | major | Adopted. Arrival-rate scenarios at a measured rate; the apply bound checked against the schedule; the write check dropped; `apply_late` apart from `stale_answer`; `D` from C's measurement ×3 |
| 4 Two flag sets with shared keys make the oracle ambiguous | major | Adopted. Verified `model/flag.go:12` (`json:"-"`). Keys prefixed per set, each set with its own `gen`; the selector check by prefix |
| 5 What the window bounds; clock steps | minor | Adopted. `wide_window` reported; the conductor's clock-step check |
| 6 `EventStream` events carry keys, not a generation | minor | Adopted. Counting check, `keep_alive` ignored; NFR-017 states latest-state delivery |
| 7 "None skipped" is a lag check | minor | Adopted. `stream_lag` names it; a repeated generation is accepted |
| 8 The protoset must be closed over its imports | minor | Adopted. `TestProtosetClosed`; `loadProtoset` in the init context |
| 9 Three evaluation services on 8013 | minor | Adopted. Verified `connect_service.go`. `rpc-unary` and `rpc-events` rotate over v1, v2 and `schema.v1`; `ResolveAll` on v1 and `schema.v1` (v2 has none) |
| 10 Where the flag file lives | minor | Adopted. `t.TempDir()`, the temporary file beside it |
| 11 The smoke's cost and what it runs | minor | Adopted. Verified `Makefile:100-106`. `test-concurrency` in `TEST_TARGETS`; `TestMain` builds once; the smoke runs the self-test and `TestConcurrency`, the full run everything; the cost measured in L |
| 12 Docker Desktop's host network | minor | Adopted. Detected through `docker info` and skipped with the reason |

### Revision 1

First version.
