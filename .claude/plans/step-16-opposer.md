# Step 16 plan: opposer findings (revision 1)

Plan: `.claude/plans/step-16.md` (revision 1, `main` @ 080f890b), issue #183.

Checked against the sources, not the plan's summary of them:
- on this repository's `main`: `core/pkg/store/store.go`, `core/pkg/model/flag.go`, `evaluator/flagd/evaluate.go`, `runtime/runtime.go`, `runtime/cmd/start.go`, `service/flag-sync/handler.go`, `service/flag-evaluation/{connect_service,eventing,flag_evaluator_v1}.go`, `service/ofrep/handler.go`, `core/pkg/service/ofrep/models.go`, `sync/file/{trigger,fetcher}.go`, `builder/internal/gotool/gotool.go`, the `Makefile`, `.github/workflows/build.yaml`, `.github/scripts/test_workflows.py`, `.claude/plans/step-15.md`;
- k6 on `master`: `internal/js/modules/k6/grpc/{stream,params,client}.go`, and the k6 docs for `Stream`, `Params` and the gRPC metrics;
- an inotify experiment in WSL Ubuntu (kernel 6.6.87.2): a rename over a watched file, with and without a reader holding the old file open;
- WSL's Docker: Docker Engine - Community on the WSL kernel, not Docker Desktop.

Counts: **0 blockers, 4 major, 8 minor.**

## What holds (verified)

- **The window can't pass a wrong answer through k6's own lag.**
  - There is one store (`runtime/from_config.go:142`). The evaluator reads it through one `View` per evaluation, bulk included (`evaluate.go:20-60`). A file change is one `memdb` commit (`Update`).
  - Sync and event streams are fed by `store.Watch`, which reads after the commit. A generation the conductor's stream received by `w_g + D` was therefore committed by then, so the measured `D` bounds the evaluation path too.
  - `s` taken before the call and `e` after it only widen the window. k6 lag can't make it reject a right answer.
- **Bulk answers come from one generation by construction:** `evaluateBulk` reads one `View`, and `ResolveAll` and OFREP bulk both go through `ResolveAllValues`.
- **Q2 (a) reaches the file source.**
  - A rename over the watched path gives `ATTRIB`, then `DELETE_SELF` and `IGNORED`. If a reader holds the old file open, `DELETE_SELF` waits until it closes.
  - `trigger.go` ignores the `Chmod` (the path exists) and handles `Remove`: the re-add succeeds at once, and then it signals. The 200 ms backoff applies only between failed attempts, which the plan's "Read" line overstates.
  - The fetcher skips unchanged content by SHA (`fetcher.go:61`), so no duplicate payload follows.
- **The race build needs no Builder change.** `env` keeps `GOFLAGS` and a set `CGO_ENABLED` (`gotool.go:71-93`). gcc is on WSL Ubuntu and on `ubuntu-latest`.
- **k6 core holds a stream for the whole run.**
  - `newCallParams` sets no default timeout (`params.go`), and `beginStream` adds a deadline only for a non-zero `timeout`. The 2-minute default belongs to `Invoke` alone (`buildInvokeRequest`).
  - flagd sets no stream deadline either: `--stream-deadline` defaults to 0 (`start.go:131`).
  - Both gRPC ports serve h2c, and flagd serves no reflection, so a protoset is the right input.
- **Fits step 15.**
  - Both jobs are whole-repo jobs, skipped only by QD's `paths-ignore`.
  - `test: null` matches what step 15 already gives the load tests.
  - K2's `test_jobs` list on `main` is still the eight-id list, so W records against whatever C3 leaves.

## Major

### 1. A correct run fails `http_req_failed`: OFREP answers `FLAG_NOT_FOUND` with 404 (thresholds, `ofrep-single`)

- **Evidence:** `EvaluationErrorResponseFrom` maps `FLAG_NOT_FOUND` to 404 (`models.go:141-143`). k6 counts any status outside 200–399 as a failed request by default. Every even-generation request for `odd-only` therefore pushes `http_req_failed` above 0, and `rate==0` fails the run.
- **Fix:**
  - Give the single-flag request `responseCallback: http.expectedStatuses(200, 404)`. A 404 is then checked by the window like any other answer.
  - Or drop the `http_req_failed` threshold and count transport errors in `stream_error` or a new `http_error` counter.
  - Add a self-test case: a 404 inside the window passes, and a 404 for a flag that exists fails.

### 2. Held streams under `constant-vus`: no way to close them, and no event when the run ends (Q1, `sync-held`, `rpc-events`, `sync-churn`)

- **Evidence:**
  - k6's `Stream` exposes only `on`, `write` and `end` (`stream.go`, `defineStream`). `end()` half-closes the send side and doesn't cancel.
  - When the VU context ends, `loop()` calls `closeWithError(nil)`, which fires **neither `end` nor `error`**.
  - `constant-vus` restarts the iteration as soon as it returns, and interrupts the running one after `gracefulStop`.
  - So the plan's end check ("every generation … to the last write", "a `configuration_change` for every generation … to `end − D`") has no event to run in, and a stream whose promise resolves early is reopened by the next iteration.
- **Fix:**
  - Run the held scenarios as `per-vu-iterations` with `iterations: 1` and a `maxDuration` past the run.
  - Run the final check from a `setTimeout` at `end − D` inside the iteration, then resolve.
  - `sync-churn` can only end a stream with `client.close()` and a new `connect`. C's spike has to record which event that fires, and the checker has to keep the planned close out of `stream_error`.
  - Add both cases to `TestStreamSpike`: the run's end, and a churn close.

### 3. Unthrottled VUs and the `ε` write check turn runner load into red runs (Q3, R1, R2)

- **Evidence:**
  - Every scenario is `constant-vus` with no think time. k6 and a `-race` flagd (2–10× slower, R2) share one 4-vCPU `ubuntu-latest` runner.
  - OFREP and RPC VUs loop as fast as they can, so they saturate the CPU that both flagd's apply path and the conductor's timer need.
  - The conductor fails the run for an apply later than `D` = 1 s **and** for a write more than `ε` = 100 ms off schedule. A `time.Sleep` on a saturated runner overshoots 100 ms easily.
  - Neither is a correctness bug, yet both make the job red. That is R1's "false red teaches people to ignore the job".
- **Fix:**
  - Use `constant-arrival-rate` (or a fixed sleep) at a request rate C measures as comfortably below saturation on `ubuntu-latest` with `-race`. The goal is concurrency, not saturation.
  - Measure the apply bound against the **schedule**: generation `g` received by the conductor by `w_g + D`, where `w_g = T0 + g·P`. That absorbs a late write, which only tightens `lo`, and makes the `ε` write check redundant. Drop the check, and keep `ε` only as clock slack in the window.
  - Set `D` from C's measured worst case on a GitHub runner (×3), and `P > D + 2ε` from it. Record the numbers in C's PR.
  - Report a late apply as its own counter (`apply_late`, with generation and lateness), separate from `stale_answer`, so a red run says "slow", not "wrong".

### 4. Two flag sets with shared keys make the oracle ambiguous (section 1, "The flags of generation `g`", `sync-held`)

- **Evidence:**
  - `FlagSetId` is `json:"-"` (`model/flag.go:12`), so the sync payload doesn't carry it.
  - The payload is a map by key (`handler.go`, `convertMap`), so a key present in both sets appears once.
  - Without a selector, `store.get` returns one of the duplicates (`>=` on equal priority, `store.go:189-196`), and `collect` keeps both for bulk.
  - The plan doesn't say which set holds `gen`, `str-<i>` and the rest. With shared keys:
    - "a selected stream holds only set `a`" can't be checked from the payload;
    - an unselected stream silently shows half the flags;
    - a `flagSetId=a` stream has no `gen` unless set `a` has one.
- **Fix:** give each set its own keys (`a-gen`, `a-str-<i>`, `b-gen`, …), each set with its own `gen`. The "only set `a`" check is then a key-prefix check. If overlapping keys are wanted as a case, it is a separate flag with identical values in both sets and an expected answer that says so.

## Minor

### 5. What the window does and doesn't bound (Q3)

- "At most two generations" holds only while `e − s < P − D − 2ε`. A slow answer under load gets a wider, weaker window. Count answers whose window spans more than two generations, and report the count beside the totals.
- k6 and the conductor share the kernel's `CLOCK_REALTIME`, but a time sync can step it during the run. The conductor can compare wall time against Go's monotonic reading each second, and fail with "clock stepped" when they differ by more than `ε`. A step is then named as such, not reported as a stale answer.

### 6. `EventStream` events name changed keys, not a generation (`rpc-events`)

- `configuration_change` carries a diff of keys (`eventing.go:83-104`), and a `keep_alive` arrives every 20 s (`flag_evaluator_v1.go:178`).
- The check can only count: one `configuration_change` per generation written in `(connect + D, end − D)`, with `keep_alive` ignored.
- The source is latest-state (`store.Watch`, buffer 1), so a subscriber that lags more than `P` gets one event for two generations. Say in the NFR that this is what "every change" means for event streams.

### 7. "None skipped" on sync streams is a lag check (`sync-held`)

`store.Watch` re-reads after every commit and holds at most one pending result (`store.go:390-415`, `handler.go:84`), so a generation is skipped only when the stream lags by more than `P`. Name it that way in the failure report ("stream lagged > P" rather than "missed change"). Accept a repeated generation as well as `g+1`, since a coarse `memdb` watch may fire twice for one commit.

### 8. The protoset must be closed over its imports (Q1 (a))

- k6's `LoadProtoset` passes the set to `protodesc.NewFiles` without walking imports (`client.go`). The evaluation protos import `google/protobuf/struct.proto`, so the harness must write every transitive dependency, well-known types included.
- `LoadProtoset` must also run in the init context, and `connect` only in VU code.
- `TestStreamSpike` catches a missing import, but the plan should state the requirement.

### 9. Port 8013 serves three evaluation services; the plan exercises one (coverage)

- `setupHandler` registers `schema.v1`, `flagd.evaluation.v1` and `flagd.evaluation.v2` (`connect_service.go:200-226`). Each has its own handler and its own `EventStream` loop (`flag_evaluator.go`, `_v1.go`, `_v2.go`).
- The plan's `Resolve*` and `EventStream` presumably mean v1. Split `rpc-unary` and `rpc-events` across v1 and v2, or name v2 and `schema.v1` out of scope.
- Otherwise the project lead's three client kinds are covered:
  - OFREP single and bulk;
  - RPC unary, `ResolveAll` and `EventStream`;
  - `SyncFlags` held, with a selector and churned, plus `FetchAllFlags`.
- Leaving SSE out with a follow-up issue is fine.

### 10. Where the flag file lives (Q2)

The rename is atomic and visible to inotify only within one native Linux filesystem. Pin it:
- the temporary file sits in the same directory as the flag file;
- the directory is `t.TempDir()`, never the checkout. A run from the Windows checkout sits on `/mnt/c` (drvfs) in WSL.

### 11. The smoke's cost and what it runs (Q4)

- `make test` doesn't loop over `ALL_GO_MOD_DIRS`; it runs `TEST_TARGETS` (`Makefile:100-106`). The module's Docker-free tests (`TestGeneratorMatchesFixture`) therefore run only in `load-smoke`. Say so, or add a `test-concurrency` entry to `TEST_TARGETS`.
- `make test-load-smoke` runs every test of the package. That means `TestLoadDetectsStaleFlags` and `TestStreamSpike` too: more k6 runs, and possibly more `-race` builds of flagd-minimal.
  - Build once per package (`TestMain`).
  - Decide whether the negative test belongs in the PR smoke.
- The "~4 min" is an estimate. Measure it in L, cold and with step 15's per-job cache, before W puts it on every PR.

### 12. Local runs on Docker Desktop (R6)

- WSL here runs Docker Engine on the WSL kernel, so `--network host` shares the network and clock as R6 assumes.
- On Docker Desktop (macOS, or Windows without WSL's engine), `host` is the Desktop VM: flagd on the host is unreachable from k6, and the clock is the VM's.
- Refusing a non-host network doesn't catch this. Detect Docker Desktop (`docker info` `OperatingSystem`) and skip with that reason.

## Changes to the project lead's open questions

| Question | Change | Finding |
|---|---|---|
| Q1 | (a) stands. Held scenarios run as `per-vu-iterations: 1`, with an end-of-run check on a timer; churn closes with `client.close()`; the spike covers both; the protoset is closed over its imports | 2, 8 |
| Q2 | (a) stands, with the flag file in `t.TempDir()` and the temporary file next to it | 10 |
| Q3 | (a) stands. `D` measured against the schedule, no separate `ε` write check, `apply_late` separate from `stale_answer`, a capped request rate, and keys distinct per flag set | 3, 4, 5, 6, 7 |
| Q4 | (a) stands, with the smoke's cost measured in L and the negative test's place decided | 11 |

Finding 1 (OFREP 404) and finding 9 (v2 coverage) are plan fixes, not questions.
