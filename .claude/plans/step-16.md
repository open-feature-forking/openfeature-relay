# Step 16 plan: flagd-minimal stays correct under concurrent OFREP, RPC and sync clients (issue #183)

**Status: revision 1, for the opposer. Four questions are open for the project lead (section 0).**

**Decided by the project lead (#183):**
- **Goal:** correctness under concurrency, pass/fail. Not a benchmark: no latency or throughput threshold.
- **Load:** many OFREP, RPC evaluation and in-process (flag sync stream) clients at once, while the flags change.
- **Pass:** every answer matches the current flags, no errors, every sync stream sees every change.
- **Tooling:** a k6 script (HTTP and gRPC) in a container.
- **CI:** a gated job (`FULL_CI` or a manual run), like `fips` and `docker-local`, plus a short smoke version.

**Hard constraints:**
- No product code changes in this step. A failure the load test finds becomes an issue with the k6 output and its own fix PR; a check is never loosened to make a run green.
- Images are pulled by digest and never pushed or logged in to (K2's `test_nothing_is_pushed_or_logged_in` holds).
- `make test` and `make lint` keep working locally without Docker: the load test is skipped unless asked for.

**Base:** `main` @ 0b5b354b. Step 15 is planned but not implemented; the jobs fit its design (section 2, "CI"). Step 14 reserved NFR-016, so this step uses **NFR-017**.

**Read:**
- `distributions/flagd-minimal/manifest.yaml`: file, http and gRPC sync providers; `flag-evaluation` (8013, gRPC and Connect, with `EventStream`), `ofrep` (8016, single and bulk evaluation, SSE on by default) and `flag-sync` (8015, `SyncFlags`, `FetchAllFlags`).
- `test/distributions/distributions_test.go`: `build` runs `go run ./cmd/relay-builder --manifest … --output …/build` from `builder` with `GOWORK=off`; tests start the binary with `start --uri file:… --port … --management-port …` on `freePort`s and poll `/readyz`.
- `builder/internal/gotool/gotool.go`: `Build` passes `GOFLAGS` from the environment and adds `CGO_ENABLED=0` only when it isn't set, so `CGO_ENABLED=1 GOFLAGS=-race` gives a race build without a Builder change (to verify in C).
- `.github/workflows/build.yaml` (`fips`, `docker-local`: `if: vars.FULL_CI == 'true' || github.event_name == 'workflow_dispatch'`) and K2, `.github/scripts/test_workflows.py` `test_jobs` and `test_gated_jobs`.
- `testbed/control`: `/change` toggles one flag's default variant between two values and renders the file with an atomic rename (`render.go`). Its target is the testbed distribution, not flagd-minimal.
- `sync/file/trigger.go`: `Create`/`Write` signal; `Remove` re-adds the watch with a 200 ms backoff, then signals (the Kubernetes symlink swap).
- The precedents: `test/loadtest/sample_k6.js` (upstream's k6 sample, RPC only, no checks) and `flagd-proxy/tests/loadtest` (Go watchers plus a file trigger). Both stay as they are.
- k6: `k6/net/grpc` has server streaming (`Stream`, `on('data')`) in core since v0.49.0; stable since v1.0. `Client.loadProtoset` loads a descriptor set. No reflection is needed, and flagd serves none.

## 0. Questions for the project lead

- **Q1, the flag sync stream in k6.**
  - (a) **k6 core `k6/net/grpc` `Stream`.** Every client kind lives in one script. The descriptors come from a protoset that the harness writes from the Go generated packages flagd links (`protodesc`), so they match what flagd serves, with no `schemas` submodule and no reflection. C proves it against flagd-minimal first (a stream held for the run, the payload parsed, a reconnect).
  - (b) An xk6 extension (custom k6 build). It means building our own k6 image locally on every run and keeping the extension current, for nothing core lacks.
  - (c) A Go sidecar holding the sync streams, as `flagd-proxy/tests/loadtest` does, with k6 for OFREP and RPC only. Two load tools, two reports.
  - **Recommended: (a).** If C's spike fails, (c) is the fallback, with the same checks.

- **Q2, changing the flags during the run.**
  - (a) **A conductor in the Go harness rewrites the flag file by atomic rename** (temporary file, then rename over the path), on a fixed schedule: generation `g` at `T0 + g·P`. That is how Kubernetes updates a mounted ConfigMap and how `testbed/control` renders.
  - (b) As (a), alternating rename and in-place truncate-and-write. In-place writes let flagd read a half-written file, which is a real operator pattern but makes "no errors" ambiguous (a parse error is then expected).
  - (c) The testbed control's `/change`. It toggles one flag between two values, runs the testbed distribution instead of flagd-minimal, and can't express generations, additions or removals.
  - (d) As (a), plus an `http` source served by the conductor, so two sources merge under load. A second oracle for precedence; better as a follow-up.
  - **Recommended: (a).** Each generation changes values, targeting, and adds and removes one flag (section 1).

- **Q3, what "matches the current flags" means while a change is in flight.**
  - (a) **A clock window with a measured apply bound.** Generation `g` is written at `w_g = T0 + g·P`. An answer to a request sent at `s` and answered at `e` is correct iff it equals the expected answer of some generation `g` with `lo ≤ g ≤ hi`:
    - `hi` = the last generation written by `e`: `floor((e − T0 + ε)/P)`;
    - `lo` = the last generation written at least `D` before `s`: `floor((s − T0 − D − ε)/P)`;
    - `D` is the apply bound (default 1 s), `ε` the clock slack (100 ms), and `P > D + 2ε` (default 3 s), so the window is at most two generations.
    - `D` is checked, not assumed: the conductor holds its own sync stream and fails the run if any generation arrives later than `w_g + D`, or if a write lands more than `ε` off schedule. k6 and the conductor share the runner's clock (k6 runs with `--network host` on the same kernel).
    - Beside the window: per client, the generation never goes backwards; a bulk answer (OFREP bulk, `ResolveAll`) comes from one generation; a sync stream sees every generation from its first payload to the last write, in order, none skipped.
  - (b) A conductor-acknowledged window: the conductor marks a generation applied when its own probe sees it, and k6 reads that state over HTTP per check. Tighter, but every check calls a second service under load.
  - (c) Check only in quiet windows (between `w_g + D` and `w_{g+1}`). Simpler, but blind to the moment of the swap, which is where concurrency bugs live.
  - **Recommended: (a).** It needs no shared state at run time, and the race to watch (a reader during the store swap) stays inside the checked window.

- **Q4, where the smoke runs, and the race build.**
  - (a) **`load-smoke` on every PR and push that runs `build`** (step 15's `paths-ignore` skips markdown and `.claude/`), 60 s, ~12 VUs. **`load-test` gated** like `fips`, 10 min, ~130 VUs. **Both run a `-race` build** of flagd-minimal with `GORACE=halt_on_error=1`: correctness, not throughput, is the goal, and races are the bug class a load test finds best.
  - (b) As (a), but the smoke gated too. Nothing on a PR catches a regression until a full run.
  - (c) The smoke as an entry of step 15's affected-module matrix, run only when a module flagd-minimal's manifest names changes. Fewer runs, but it ties this step to step 15's P2, and `make test` would then need Docker.
  - (d) As (a), with the full run on a normal build at higher load, and race only in the smoke.
  - **Recommended: (a).** The smoke adds one parallel job of ~4 min (build with `-race`, pull k6, 60 s), below `integration-test`'s ~8.7 min.

## 1. Design

**The module `test/concurrency`** (its own `go.mod`, in `ALL_GO_MOD_DIRS`):
- `concurrency_test.go`: `TestConcurrency`, skipped unless `RELAY_LOAD=smoke` or `full`.
  1. Builds flagd-minimal with the Builder, as `test/distributions` does, with `CGO_ENABLED=1 GOFLAGS=-race`.
  2. Writes generation 0 and the protoset, then starts `flagd start --uri file:<flags> --port --ofrep-port --sync-port --management-port` on free ports, with `GORACE=halt_on_error=1`, and waits for `/readyz`.
  3. Runs `docker run --rm --network host` with `$K6_IMAGE`, the script directory and the protoset mounted read-only, passing ports, `T0`, `P`, `D`, `ε` and the profile as environment variables.
  4. Runs the conductor beside it: the schedule, its own sync stream, `/readyz` polled each second.
  5. Stops flagd with SIGTERM after k6 ends.
- **Pass:** k6 exits 0 (thresholds below); every generation applied within `D` and written within `ε`; `/readyz` stayed 200; flagd exits 0 within its shutdown timeout; no `WARNING: DATA RACE` in its stderr.
- `generations.go`: the flag document of generation `g`, and its expected answers. The JS checker implements the same function; `TestGeneratorMatchesFixture` and the k6 self-test share one fixture of expected answers.
- `k6/load.js`, `k6/check.js` (the window and the expected answers), `k6/selftest.js`.
- `K6_IMAGE` in the Makefile: `grafana/k6:<version>@sha256:<digest>`, a k6 release ≥ v1.0 and at least two weeks old, its digest recorded with `docker buildx imagetools inspect` in the PR description.
- Make targets: `test-load-smoke` and `test-load` (`RELAY_LOAD=… go test -count=1 -timeout … ./test/concurrency/...`).

**The flags of generation `g`** (~100 flags in two flag sets, `a` and `b`):
- `gen`: integer, value `g`;
- `str-<i>`: string, value `g<g>-<i>` (the generation is readable from the answer);
- `bool-<i>`: boolean, default variant `(g + i) mod 2`;
- `tgt-<i>`: targeting with `ends_with` on `targetingKey`: `hit` when the key ends in `g mod 10`, else `miss`; clients send random keys;
- `obj-<i>`: object `{"gen": g, "i": i}`;
- `odd-only`: present only in odd generations. In even ones the expected answer is `FLAG_NOT_FOUND`, so the store's removals are checked.

**The clients** (`constant-vus` scenarios; full / smoke VUs):

| Scenario | Full | Smoke | Calls | Checks |
|---|---|---|---|---|
| `ofrep-single` | 30 | 2 | `POST /ofrep/v1/evaluate/flags/{key}`, random flag and key | window, monotonic `gen`, `FLAG_NOT_FOUND` |
| `ofrep-bulk` | 10 | 2 | `POST /ofrep/v1/evaluate/flags` | window; one generation per answer |
| `rpc-unary` | 30 | 2 | `Resolve{Boolean,String,Int,Object}`, `ResolveAll` over gRPC | window, monotonic; `ResolveAll` one generation |
| `rpc-events` | 10 | 2 | `EventStream`, held | `provider_ready` first; a `configuration_change` for every generation written between `connect + D` and `end − D` |
| `sync-held` | 40 | 2 | `SyncFlags`, held for the run; half with selector `flagSetId=a` | first payload in the window; every generation after it, in order, none skipped; a selected stream holds only set `a` |
| `sync-churn` | 10 | 2 | `SyncFlags` re-opened every ~10 s, plus `FetchAllFlags` | first payload in the window |

**Thresholds** (k6 exits 99 on any): `checks: rate==1`, `http_req_failed: rate==0`, and counters `wrong_answer`, `stale_answer`, `missed_change`, `stream_error` each `count==0`. No duration threshold.

**Profiles:** full 10 min, 200 generations; smoke 60 s, 20 generations. `P` 3 s, `D` 1 s, `ε` 100 ms in both.

**CI** (in step 15's design):
- **Two jobs in `build.yaml`,** whole-repo jobs like `fips`, outside the affected-module matrix:
  - `load-smoke`: always (Q4 (a)), `timeout-minutes: 15`;
  - `load-test`: `if: vars.FULL_CI == 'true' || github.event_name == 'workflow_dispatch'`, `timeout-minutes: 30`.
  - Both: checkout (no submodules), setup-go, `make workspace-init`, `make test-load-smoke` or `make test-load`; flagd's stderr and k6's summary uploaded as an artifact on failure.
- **Step 15's parts they rely on:** the `concurrency` group cancels superseded PR runs; QD's `paths-ignore` skips markdown-only changes; `docs-check.yaml` is unaffected.
- **Step 15's table:** `test/concurrency` gets an entry with `test: null` and the reason "runs in `load-smoke` and `load-test`", whichever of this step's W and step 15's P1 lands second adds it. Without the entry `changes` fails.
- **K2:**
  - `test_jobs` gains both ids, recorded against the list on `main` at the time (PR #168 and step 15's C3 also change it);
  - `test_gated_jobs` adds `load-test`;
  - `test_load_image_pinned`: `K6_IMAGE` carries `@sha256:`, and no workflow names a `grafana/k6` tag without one;
  - `test_nothing_is_pushed_or_logged_in` unchanged.

**Out of scope:**
- OFREP SSE (`/ofrep/v1/sse`): k6 has no core SSE client. A follow-up issue, with the sidecar of Q1 (c) as one way.
- Other distributions; TLS; the `http` and `grpc` sources under load (Q2 (d)).
- Latency, throughput and memory numbers. k6 reports them, and nothing gates on them.

## 2. Spec change (for PR S)

| ID | Requirement |
|---|---|
| NFR-017 | Under concurrent OFREP, flag evaluation and flag sync clients, while its flags change, flagd-minimal answers every request from a generation inside the change window, delivers every change to every flag sync stream and evaluation event stream, and shows no data race. Proven by `make test-load` (gated in CI) and `make test-load-smoke` (every build) |

The window of Q3 goes into the NFR's description in `docs/requirements.md`. No use case changes: this tests flagd's existing endpoints.

## 3. Tests

**Characterization:** no product code changes, so nothing new to pin. The endpoints' answers are already pinned by `test/distributions` and the integration suite; this step reads them under load.

**The test has to fail when it should (C):**

| Test | Proves |
|---|---|
| `k6/selftest.js` run by `TestCheckerSelfTest` | Every check rejects a synthetic answer: one generation too old, one from the future, a bulk with two generations, a backwards `gen`, a stream that skips a generation, `odd-only` found in an even generation, a wrong targeting variant. And accepts both generations at the window's edge |
| `TestGeneratorMatchesFixture` | `generations.go` and `check.js` agree on the expected answers |
| `TestLoadDetectsStaleFlags` | The conductor skips one write: k6 exits 99 with `stale_answer` > 0 and the conductor reports the late generation |
| `TestRaceBuildInEffect` | The race build is in effect: `go version -m` of the binary shows `-race` (`build -race=true`) |
| `TestStreamSpike` | One held `SyncFlags` stream, one reconnect, and one `EventStream` from k6 against flagd-minimal, with the protoset (Q1's spike) |

**The runs:** `make test-load-smoke` green locally in WSL and in CI; `make test-load` green on a manual dispatch, with the generation and check counts in the PR description.

## 4. PR slicing

| PR | Model | Base | Content | Starts |
|---|---|---|---|---|
| S | mechanic | `main` (straight to `main`) | NFR-017 | after the plan is approved |
| C | implementer | `main` | `test/concurrency`: the harness, the race build, the protoset, the conductor, `generations.go`, `check.js`, the self-test and the tests of section 3; one VU per scenario | now |
| L | implementer | C | `load.js` with every scenario and both profiles, the thresholds, `K6_IMAGE`, `test-load-smoke` and `test-load` | after C |
| W | implementer | L | `load-smoke` and `load-test`; the K2 pins; the step 15 table entry if P1 has landed | after L |
| X | mechanic | W | NFR-017 status; `CLAUDE.md`'s CI line (the smoke on every build, `load-test` gated); the SSE follow-up issue; #183 | last |

The critical path is C → L → W. A failure L's full run finds is filed as an issue and fixed in its own PR before W merges; `load-test` merges green.

## 5. Gates

| Gate | Command | PRs |
|---|---|---|
| Unit tests of the module | `go test -race ./test/concurrency/...` (skips the load) | C, L |
| Self-test and spike | `RELAY_LOAD=smoke go test -run 'TestCheckerSelfTest\|TestStreamSpike\|TestLoadDetectsStaleFlags' ./test/concurrency/...` in WSL | C |
| Smoke | `make test-load-smoke` in WSL, and `load-smoke` in CI | L, W |
| Full | `load-test` on a manual dispatch of the branch | W |
| Workflow pins | `python3 .github/scripts/test_workflows.py` | W |
| Lint | CI's `lint` (the new module is linted like any other) | C, L |
| Spec | `spec_lint.py --docs docs` on Windows | S, X |

## 6. Risks

- **R1, a flaky oracle.** A false red teaches people to ignore the job. Against it: `D` is measured per generation and reported, not hidden; `ε` covers scheduler jitter; `P > D + 2ε` bounds the window; the self-test proves each check both ways. A red run names the check, the client, the request times and the generations.
- **R2, the race build's cost.** `-race` slows flagd 2–10× and multiplies its memory. The load is sized for correctness, not saturation; the full run's VUs come down before `D` is raised.
- **R3, real bugs found.** Likely candidates: the store swap against bulk evaluation, the file watch under rename, stream fan-out to slow subscribers. Each is an issue and a fix PR; drop-in rules (C-003) apply to the fixes.
- **R4, k6 drift.** The image is pinned by digest; a k6 upgrade is a PR that re-records it. The protoset comes from the linked Go packages, so a schema change can't drift silently.
- **R5, CI queue.** `load-smoke` is one more job per run; step 15's `concurrency` group and `paths-ignore` keep the count down. `load-test` runs only gated.
- **R6, clock.** k6 and the conductor share the runner's kernel clock; a container on another host would break Q3 (a). The harness refuses a non-host network.
- **R7, Docker for local runs.** Only the load targets need it; `make test` skips the module's load.

## 7. Considered

- **The testbed control (step 13)** for the changes: one toggled flag and the testbed distribution (Q2 (c)). Its atomic rename is the pattern the conductor follows.
- **Extending `test/loadtest/sample_k6.js`:** upstream's sample, RPC only and without checks. It stays as it is; `test/concurrency` is new.
- **A Go load generator only** (no k6): rejected by the project lead's tooling decision.

## 8. Changelog

Revision 1: first version.
