# Step 15 plan: opposer findings (revision 1)

Plan: `.claude/plans/step-15.md` (revision 1, `main` @ 63a46242).

Checked against the repository, not the plan's summary of it:
- `Makefile` (`workspace-init`, `ALL_GO_MOD_DIRS`, the `test-*` targets, `lint`, `check-*`), `.github/workflows/build.yaml`, `.github/scripts/test_workflows.py`, `test/contract-external.sh`, `.gitignore`;
- every tracked `go.mod` (requires, replaces, module paths), and every `*.go` file for imports and for reads outside its own module;
- run 41976924232's job times and its `Setup go` logs;
- the last 100 `build` runs (`gh run list -w build -L 100`), for the push-to-`main` rate.

Counts: **1 blocker, 4 major, 6 minor.**

## What holds (verified)

- **The measurements.** Run 41976924232's job times match section 1, and every job logs `Restore cache failed: Dependencies file is not found … go.sum`. There is no root `go.sum`; `go.work` and `go.work.sum` are gitignored and made by `workspace-init`.
- **The `require` graph is complete for imports.** A scan of every `*.go` file's imports of a relay module against its module's `require`s finds no import without a `require` (the six hits are string literals in tests). So Q1 (a) loses nothing that a tidy `go.mod` has, even though workspace mode would let an untidy one build.
- **C3 is right.** `test_jobs`' regex `^  ([a-z][a-z-]*):\n` skips `builder-e2e` (the digit) and `integration-test` (the trailing comment), so K2's list is eight jobs out of ten.
- **Q7's first parent.** On a `pull_request` run `github.sha` is the merge commit, its first parent is the base it was built on, and `fetch-depth: 2` fetches both parents.
- **Q6 (a)** keeps the merge rule: skipped jobs (`fips`, `docker-local`) are already part of a green run today.

## Blocker

### 1. A `go.mod` change in any module changes every module's build (Q1, Q4, R1)

- **Step:** the affected-set rule (section 2), Q1, R1.
- **Evidence:**
  - CI tests in workspace mode (`make workspace-init`, then `go test` from the root), and `go.work` lists all 52 modules.
  - In a workspace, the build list is one MVS over **every** workspace module's requirements. A version bump of a third-party module in one `go.mod` (say `google.golang.org/grpc` in `sync/sse/go.mod`) raises it for `flagd`, `runtime`, `flagd-proxy` and everything else that uses it.
  - Under the plan that file belongs to `sync/sse`, which has no dependents, so only `sync/sse` and the always set run. The PR is green; `main`'s full run (Q3) is the first to compile the rest against the new version.
  - The same holds for a `go` or `toolchain` directive, and `flagd/go.mod` is also every job's `go-version-file`.
- **Fix:** a change to any tracked `go.mod` or `go.sum` sets `all`. That costs little: these PRs are rare and are exactly the ones a dependency update breaks. Add the case to P1's table (`sync/sse/go.mod` → `all`), and say in R1 why.

## Major

### 2. Edges the `require` graph doesn't have (Q4, R1)

- **Step:** Q4 (a)'s `extraEdges`, R1's "residual".
- **Evidence:** R1's grep found two cases. A wider one (`filepath.Join("..", …)`, `"../…"`, `runtime.Caller`) finds more:
  - **`builder` → `test/distributions`.** `test/distributions` runs the Builder from source (`go run ./cmd/relay-builder` in `builder/`, `distributions_test.go:64-66`) and builds every manifest with it. No manifest names `builder`, so a change to the generator runs `builder`'s own tests (always set) but not `test/distributions`. `builder-e2e` and the upgrade differential run then, but they build only flagd-full and flagsmith, not the parity, standard and fixture tests.
  - **`test/inprocess-example/filegrpc` → `test/inprocess-example/file`.** `file`'s `TestBoundaryCatchesForbiddenDependency` reads `../filegrpc`'s module graph (`inprocess_test.go:275`). `file` doesn't require `filegrpc`.
  - **The modules nested in `test/distributions`.** `test/distributions/example/cmd` and `test/distributions/sync-vendor/cmd` are modules of their own (both in `ALL_GO_MOD_DIRS`, both requiring `core` and `runtime`). By the longest-prefix rule their files belong to them, not to `test/distributions`; `go test ./...` in `test/distributions` doesn't descend into them, yet the manifests there build them. "Under `test/distributions/`" has to mean these modules, explicitly.
  - Covered already, for the record: `runtime` reading `../service/flag-sync/test-cert/` (`runtime` requires `service/flag-sync`), and `test/upgrade` reading `docs/guides/upgrade-to-flagd-full.md` (a `docs/**`-only PR runs no build at all today, so that gap predates this step).
- **Fix:** `extraEdges` for `builder` → `test/distributions` (and → `flagd`, whose `main.go`, `go.mod` and `components` the Builder generates, though `repo-checks` covers that) and `filegrpc` → `file`; the nested modules named as edges into `test/distributions`. Replace R1's residual with this list, and give P1 one case per edge.

### 3. The measured saving doesn't need an affected set (section 1, Q2, Q5)

- **Step:** the step as a whole; section 7 dismisses the alternatives without numbers.
- **Evidence:** by the plan's own table, a PR's wall clock goes from `test` (994 s) to `integration-test` (~520 s), or stays at the upgrade differential (~980 s) whenever that runs.
  - **The differential runs often.** Its gate matches `^flagd/`, `^builder/`, `^distributions/`, `^test/upgrade/`, `^test/image/`, any Dockerfile and `build.yaml`, over the `base.sha` diff the plan shows to be too wide. PR #172, the plan's own measurement, would still take 16.5 minutes under step 15, because its differential ran.
  - **A fixed split gets the same wall clock.** The longest unit is `test/distributions`, 268 s. Four shards (`test/distributions`; `test-modules`; `test-flagd` with `test-flagd-proxy` and `test-contrib`; the rest) put every shard under ~6.5 min with O, under `integration-test`'s ~8.7 min. So every PR ends when `integration-test` ends, as with the matrix. There's no graph, no `extraEdges`, no fallback list and no missed dependents, and 4 jobs instead of up to 49.
  - **Go's test cache** (after P0) skips the unchanged packages of the targets without `-count=1`: `test-modules`, `test-flagd`, `test-contrib`, `test-flagd-proxy`, 592 s of the 976 s. It's package-granular and follows the workspace's build list, so it doesn't have finding 1's problem. Two limits to measure in P0: every example target, `test-distributions` and the pins pass `-count=1`; and a test that opens a file misses the cache after a fresh checkout, because the cache key includes the file's mtime.
  - **Lint** isn't on the critical path either: 390 s − 52 s install − the cold cache is below `integration-test`, so Q2, Q5 and P3 buy no wall clock once P0 and a cached golangci-lint land.
- **Fix:** add a question before Q1: (a) shards plus P0, the first-parent gate fix for `upgrade-test` and `release-dry-run` (two `BASE` lines, now deferred as a follow-up), and the cheap queue fixes of finding 4; (b) the affected-set matrix as planned. Recommend (a) for the stated goal (wall clock), with (b) as a later step if runner load, not wall clock, becomes the limit. If the project lead keeps (b), move the gate fix into P0: it's the one change that shortens a PR #172-like PR.

### 4. The queue: every push to `main` becomes ~58 jobs (Q3, R2)

- **Step:** Q3 (a), R2.
- **Evidence:**
  - The last 100 `build` runs took ~11 hours (2026-11-04 19:18 to 2026-11-05 06:23 UTC); 38 were pushes to `main`, and **16 of those pushed only `.claude/` or `CLAUDE.md`**. Plans and reviews go straight to `main`, and `paths-ignore` skips only `README.md` and `docs/**`.
  - Today such a push costs ~10 jobs. Under Q3 (a) it costs ~49 test jobs plus `changes`, `lint`, `repo-checks` and the ~8 others, and runner time for `test` alone goes from 976 s to ~976 s + 49 × O (≈ 2,400–3,900 s).
  - With 20 concurrent jobs org-wide and ~3.5 pushes an hour, a PR's `integration-test` and upgrade differential queue behind `main`'s matrix entries. The table's "6–8 min with queueing" assumes an empty queue. Under load a full run, and the PRs beside it, can take longer than today's 994 s.
  - There's no `concurrency:` group in `build.yaml`, so a force-pushed PR keeps its stale run going.
- **Fix:**
  - Apply Q8's list to pushes too: a push that touches only Q8 paths runs no matrix, and (with P4) no `integration-test` or `builder-e2e`. Or add `.claude/**` and `CLAUDE.md` to `paths-ignore`, which changes K2's `test_paths_ignore_docs_on_push_and_pull_request` deliberately.
  - `concurrency: { group: build-${{ github.ref }}, cancel-in-progress: ${{ github.event_name == 'pull_request' }} }`.
  - `max-parallel` on the matrix (e.g. 10), so one full run can't take every slot.
  - P2's dry run measures a full run with a second PR running beside it, not alone.

### 5. P0 has R3's race, and the cache size is unknown (P0, Q2)

- **Step:** P0, Q2's "restore only", R3.
- **Evidence:**
  - P0 gives "today's jobs" `cache-dependency-path: **/go.sum`. Six jobs (`lint`, `test`, `docs-check`, `builder-e2e`, `integration-test`, `upgrade-test`) would then share one key, and `setup-go` saves the first cache to finish under that key. In run 41976924232 that's `docs-check`, after 36 s, with almost nothing compiled; the others find the key taken and don't save. P0's measurement would show no gain.
  - `GOCACHE` with `-race` for 52 modules (k8s, AWS, Azure and GCP SDKs among them) plus `GOMODCACHE` is likely several GB, against GitHub's 10 GB per repository. 49 jobs each restoring all of it makes the restore part of O, possibly most of it. O = 30–60 s is not measured.
  - golangci-lint's own cache (`~/.cache/golangci-lint`, most of the 98 s for `flagd`) isn't in `setup-go`'s cache at all.
- **Fix:** P0 sets `cache: false` on every job but `test`, or gives each job its own key with `actions/cache` (`go-<job>-${{ hashFiles('**/go.sum') }}`), and saves only on `main`. P0 reports the cache size and the restore time; O in section 1 comes from that. The lint job caches golangci-lint's cache directory.

## Minor

### 6. The table's facts: `runtime` needs the submodule (Q4, section 2)

`runtime/cmd/loadconfig_test.go:18-20` globs `../../test-harness/launchpad/configs/*.json` and fails with `NotEmpty` when the submodule is missing. The plan gives `submodule: test-harness` only to `test/evaluator-testkit`, so `test (runtime)` would be red on every run. `test/evaluator-testkit` finds the harness through `runtime.Caller` (`testkit.go:37`). C1 should derive `submodule` by grepping for `test-harness` rather than by hand. A change to `test-harness` (the pointer) running `all` stays right.

### 7. What counts as a module and a module path (Q1, section 2)

- Module paths have two prefixes (`github.com/open-feature-forking/openfeature-relay/…` and `github.com/open-feature/flagd/…`, for `flagd`, `flagd-proxy`, `test/upgrade` and the examples), and three modules have no path at all (`integration_test`, `tests.loadtest`, `zero-downtime-test`). `buf.build/gen/go/open-feature/flagd/…` must not match. The script should key edges on the set of module paths it reads from the `go.mod` files, not on a prefix.
- "A module with no entry fails `changes`" must use `ALL_GO_MOD_DIRS`'s prune list (`test/integration`, `test-harness`, `spec`, `schemas`, `playground-app`, `.claude`, the `build` and `.flagd-*` directories). Otherwise `test/integration/go.mod` fails every PR that touches it, and a local run picks up `.claude/worktrees/*/go.mod`.
- 26 `go.mod` files carry `replace`s with no matching `require` (e.g. `configformat/flagd` replaces eight sync modules it doesn't require). They're inert, so Q1 (a) is right to ignore them. Say so, so the implementer doesn't add edges from them.

### 8. C1 can't be read from `make -n test` (section 3)

`test-modules` is a shell loop, so `make -n` prints the loop, not one command per module, and `$(ALL_GO_MOD_DIRS)` needs `find`. Parsing that pins the parser, not the parity. Either make `test-modules`, `test-flagd` and `test-contrib` loop over `test-module MODULE=…` (one source, and C1 checks only that the table covers `ALL_GO_MOD_DIRS`), or pin an expected command list written by hand.

### 9. Report-only mode can measure misses, and should (P1, testability)

As planned, P1 shows the sets but can't show a miss, since everything still runs. While today's `test` still runs in full, P1's `changes` job can map each `FAIL\t<package>` of `make test` to its module and report a failing module that isn't in the set. On pushes to `main` it can also compare Q1 (a)'s closure with `go list -deps -test` (Q1 (c)) and report the differences. That gives an oracle for findings 1 and 2 from real PRs before P2 skips anything. The single "run against the real repository" in P1 should assert only invariants (every module in the table, `core`'s closure is ≥ 40 modules), not exact sets that a new module breaks.

### 10. The aggregate's green on an empty set (Q6, R5)

"`skipped` is green when the set is empty" should also check that `all` is false. With `all == 'true'`, a skipped `test-module` means the script emitted an empty list, a bug, and must be red. Add the case to `test_aggregate_jobs`.

### 11. R1's grep and the always set

R1 names `test/upgrade` and `test/image` as the only cross-directory readers. Finding 2's grep adds `test/distributions`, `test/release` (`runtime.Caller` to the root), `runtime/cmd`, `test/evaluator-testkit` and `test/inprocess-example/file`. R1 should list them all and say which rule covers each.

## Changes to the project lead's open questions

| Question | Change | Finding |
|---|---|---|
| new, before Q1 | shards + test cache + gate fix (recommended) vs the affected-set matrix | 3 |
| Q1 | any `go.mod`/`go.sum` change sets `all`; key on module paths, not prefixes | 1, 7 |
| Q2 | the job count matters more than the plan says; `max-parallel` on the matrix | 4 |
| Q3 | (a), but Q8's list applies to pushes too; a `concurrency` group | 4 |
| Q4 | `extraEdges` for `builder`, `filegrpc` and the nested `cmd` modules; `runtime` gets the submodule | 2, 6 |
| Q6 | red when `all` is true and the matrix is skipped | 10 |
| Q7 | move the `upgrade-test` and `release-dry-run` gates into P0, not a follow-up | 3 |

Q5, Q8 and Q9 stand as recommended.
