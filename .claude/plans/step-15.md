# Step 15 plan: a test and lint job per module, only for the modules a PR reaches (issue #176)

**Status: revision 1, for the opposer. 9 questions open for the project lead (section 0).**

**Decision (#176):** step 15, through the loop. Scope: the unit tests and lint only.

**Hard constraints:**
- the checks named `test` and `lint` stay, as fixed aggregate jobs, so the merge rule ("CI green") and K2's job list in `.github/scripts/test_workflows.py` keep working;
- a change outside any module runs every module;
- `make test` and `make lint` keep working locally, as today, over the whole repository.

**Out of scope:** `integration-test`, `builder-e2e`, `upgrade-test` (with `image-test`), `release-dry-run`, `fips`, `docker-local`, `benchmark` and `docs-check`. They stay whole-repo jobs with today's triggers. Their rule is "skipped only when no Go code changed". This step doesn't redesign them; P4 only gives `integration-test` and `builder-e2e` that `if` from the changes job's `go` output (section 2).

**Base:** `main` @ dab83f0a.

**Read:** `Makefile` (`workspace-init`, `ALL_GO_MOD_DIRS`, `GO_LIB_MODULE_DIRS`, `FLAGD_MODULE_DIRS`, `CONTRIB_MODULE_DIRS`, `TESTBED_MODULE_DIRS`, `TEST_TARGETS`, `test-modules`, `test-flagd`, `test-contrib`, `lint`), `.github/workflows/build.yaml`, `.github/scripts/test_workflows.py`, `test/contract-external.sh`, and every module's `go.mod`.

## 1. Measurements

**Run 41976924232** (PR #172, which changed one file under `test/distributions`).
- **Wall clock per job:** `test` 994 s, `upgrade-test` 980 s, `integration-test` 519 s, `lint` 390 s, `builder-e2e` 273 s, `benchmark` 279 s.
- **`make test` takes 976 s**, serial:

| Target | s | Target | s |
|---|---|---|---|
| `test-modules` (14 library modules) | 378 | `test-evaluator-testkit` | 35 |
| `test-flagd` (7 modules, one `go test`) | 186 | `test-contract-external` | 28 |
| `test-distributions` | 268 | the other 7 examples | 36 |
| `test-flagd-proxy` | 20 | `test-builder` | 10 |
| `test-contrib` (5 modules) | 8 | release, upgrade-accepted, image pins | 5 |

- **Inside `test-modules`:**
  - `sync/openfeature-operator` 87 s, `core` 81 s, `gcs` 45 s, `sync/builder` 40 s, `flagd-grpc` 32 s and `otel` 25 s;
  - the other eight take 3–17 s each.
  - Inside `test-flagd`, the `flagd` package takes 170 s and `runtime` 99 s.
- **`lint` takes 390 s:**
  - `check-flagd-full-generated` 127 s (it is part of `make lint`);
  - `go install golangci-lint` 52 s;
  - linting the 52 modules 184 s, of which 98 s is the first big module (`flagd`) with a cold cache and 1–5 s for each module after it;
  - the repo-wide checks after it 13 s.
- **The Go cache never restores.** Every job logs `Restore cache failed: Dependencies file is not found … go.sum`: `setup-go` looks for a root `go.sum`, and there is none. Each job therefore downloads and compiles from cold.
- **The change set is wrong today.** The `upgrade-test` gate diffs `pull_request.base.sha..github.sha`. For PR #172 that diff held `test/upgrade/testdata/issue-78.txt`, a commit that landed on `main` after the base SHA, so the 16-minute differential ran for a one-file test change. The first parent of the merge commit (`github.sha^1`) is the base that the merge was actually built on.
- **The org's plan is Free, and the repository is public:** standard runner minutes cost nothing, but at most 20 jobs run at once across the whole org. Parallel sessions push PRs at the same time, so a full run of ~100 jobs queues.

**The module graph.** Its edges are the `require`s between relay modules, `// indirect` ones included.
- **`core` is required by 41 of the 52 modules,** directly or transitively.
- **The library modules are near-leaves:** each requires only `core`, and only `sync/builder` requires the seven fetching sources.
- **There is one cycle,** from `runtime` to `flagd`, through `runtime`'s tests.
- **Eleven modules require no relay module:**
  - `builder`, `test/release`, `test/upgrade`, `test/image` and `test/distributions` read the checkout outside their own directory, so their dependencies aren't in their `go.mod`;
  - `test/testbed`, both load tests, `test/zero-downtime-flagd-proxy` and `testbed/control` don't.

**Estimates** for the `test` check, against 994 s today:
- **O, the per-job overhead,** is 30–60 s: checkout, `setup-go`, restoring a working cache, and `workspace-init`.
- **PR wall clock** stays bounded by `integration-test` (~520 s), and by `upgrade-test` (~980 s) when that one runs.

| PR touches | Modules tested (Q4 defaults) | `test` wall clock | Runner time | PR wall clock |
|---|---|---|---|---|
| a leaf, e.g. `sync/sse` (no dependents, in no manifest) | `sync/sse` and the always-run set (~45 s: release, upgrade-accepted, image pins, `builder`, contract-external) | ~1.5 min (−15 min) | ~6 min | ~16.5 → ~8.7 min (`integration-test`) |
| a leaf named in a manifest, e.g. `sync/s3` | as above, plus `sync/builder`, `flagd-proxy`, `runtime`, the services, `flagd`, the examples and `test/distributions` | ~6 min | ~30 min | ~8.7 min |
| `core` | every module in `core`'s closure (~47 test jobs) | ~6–8 min with queueing (−9 to −11 min); the longest unit is `test/distributions` at 268 s + O | ~45 min (≈ 976 s of work + 47 × O) | ~8.7 min |
| the workflow (fallback) | every module, as for `core` | ~6–8 min | ~45 min | unchanged ~16.5 min: `upgrade-test` runs, because its gate matches `build.yaml` |

Fixing the cache is worth doing even without the matrix (P0). It is measured on its own before the matrix lands.

## 0. Questions for the project lead

- **Q1, how the module graph is computed.**
  - (a) **Parse each `go.mod`'s `require` blocks in the changes job,** in a Python script under `.github/scripts`.
    - This needs no Go and no downloads, takes under 1 s, and gives the graph #176 names.
    - `// indirect` edges count, so the result is a safe superset.
    - It reads the graph at the head, and the module directories of the base and the head together (Q7).
  - (b) **`go mod graph` or `go list -m -json all`** per module, with `GOWORK=off`. Inside the workspace, `go list -m all` lists every module of the workspace, so the answer would be wrong. Outside it, each of the 52 modules resolves its full graph: about a minute of downloads and a Go setup, for the same edges as (a).
  - (c) **Package level:** `go list -deps -test` in the workspace. Finer: `runtime` would no longer be reached through the indirect `sync/s3`. It needs Go and every module downloaded (~1 min), and the decision named `require`s.
  - **Recommended:** (a). (c) is a later refinement if the sets turn out too wide.

- **Q2, the matrix size and the runner cost.**
  - **The facts:**
    - one matrix holds up to 256 entries, so 52 fit;
    - minutes are free here;
    - at most 20 jobs run at once, org-wide;
    - a full run is ~49 test entries (48 tested modules and contract-external), plus 52 lint entries under (a).
  - **The options:**
    - (a) **As #176 says:** a test matrix and a lint matrix, one job per module each, so ~101 jobs on a full run. Lint work per module is 1–5 s against ~45 s of O, and the job count doubles the queue.
    - (b) **A test matrix, one job per module, and one `lint` job over the affected set:** `golangci-lint run` per affected module, as `make lint` does today, with one install and one cache. A leaf PR lints in ~1 min; a full run takes today's ~3 min.
    - (c) **One job per module that runs both test and lint.** That shares the setup, but the aggregates can't tell a lint failure from a test failure without per-entry artifacts.
  - **Recommended:** (b). It deviates from the issue's "lint becomes a matrix", because lint isn't the long pole and the job count is.
  - **With any option:**
    - the cache is fixed (P0);
    - the matrix jobs only restore it (`actions/cache/restore`), and only the push run on `main` saves it, from one job that has built the whole workspace, so ~49 partial saves don't race for one key;
    - golangci-lint v2.14.0 comes from a cached `GOBIN`, not from `go install` per job.

- **Q3, push to `main`.**
  - (a) **Always everything,** as with `workflow_dispatch` and `FULL_CI=true`. `main` is the coverage baseline, and it catches two PRs that were each green on their own affected set but meet on `main`.
  - (b) The affected set of `before..after`, as on a PR.
  - (c) (b), plus a nightly full run.
  - **Recommended:** (a).

- **Q4, the examples, the evaluator testkit, contract-external, and the modules that read the checkout.**
  - **The examples and their `/distribution` modules** (configformat, eventsink, middleware, telemetry, service, sync, in-process): ordinary modules. The `/distribution` module requires its example, so a change to the example reaches both.
  - **`test/evaluator-testkit`:** an ordinary module, with a `submodule: test-harness` flag in its table entry, so only that job initialises the submodule. A change to the submodule pointer is outside every module, so it runs everything.
  - **`test-contract-external`:**
    - (a) **always runs (28 s).** It tests `core` as it is published, against every example listed in `testdata/contract-external.txt`;
    - (b) runs when `core` or a listed example is affected;
    - (c) runs once per example module.
    - **Recommended:** (a).
  - **The modules that read the checkout:**
    - (a) **`builder`, `test/release`, `test/upgrade` (the accepted-items run) and `test/image` (the pins) always run,** ~15 s together. `test/distributions` (268 s) gets edges derived from the manifests: it is affected when any module named in `distributions/*/manifest.yaml` or under `test/distributions/` is affected. Today that is `core`'s whole closure except the modules that no manifest names, such as `sync/sse`;
    - (b) all five, `test/distributions` included, always run, so every Go PR takes ≥ 5 min;
    - (c) they get only the edges in their `go.mod`, which is none, and are missed.
    - **Recommended:** (a).

- **Q5, the checks inside `make lint` and the `lint` job.** These are `check-flagd-full-generated` (127 s), `check-package-coverage`, `check-flagd-full-modules`, the two Python tests, `check-module-boundary check-evaluator-boundary`, and the `x/exp` greps.
  - (a) **A fixed `repo-checks` job that always runs** (~2.5 min) beside the lint work, and that the aggregate `lint` waits for. `make lint` keeps calling `check-flagd-full-generated` for local runs; CI calls a new `lint-modules MODULES=…` target.
  - (b) Run them in the aggregate `lint` job after the matrix. That adds its ~2.5 min to the critical path.
  - (c) Gate each check on its own inputs: `check-flagd-full-generated` on `builder/`, `distributions/flagd-full/` and `flagd/`, and so on.
  - **Recommended:** (a). Each check is cheap except the generate check, and (c) is a pattern list to keep correct for ~2 min.

- **Q6, how a skipped module still reports.** GitHub has no branch protection or ruleset on `main` today: "CI green" is what the orchestrating session reads.
  - (a) **The aggregates are the only checks that matter.**
    - `test` and `lint` are jobs with `needs: [changes, <work jobs>]` and `if: always()`.
    - Each is green only when `changes` succeeded and every work job it needs is `success`, or is `skipped` because the affected set is empty.
    - `failure` and `cancelled` are red.
    - The matrix job is skipped by an `if` on an empty set, because GitHub rejects an empty matrix.
    - A skipped module has no check of its own; the job summary of `changes` lists what ran and why.
  - (b) One required check per module, with skipped ones reporting success. That only matters if a ruleset is added later, and GitHub doesn't report a skipped matrix entry under its name.
  - **Recommended:** (a). It also holds if a ruleset later requires `test` and `lint`.

- **Q7, which changes a PR is measured by** (not asked by #176; the measurement above makes it necessary).
  - (a) **`git diff --name-only github.sha^1 github.sha` on the merge commit** for a pull request, and `before..after` for a push. A file belongs to the module with the longest matching directory in the base or the head, so a deleted or moved module still counts. A missing or all-zero base runs everything.
  - (b) `pull_request.base.sha..github.sha`, as the existing gates do. This is the over-selection seen on PR #172.
  - **Recommended:** (a). Moving the `upgrade-test` and `release-dry-run` gates to it is a follow-up outside this step.

- **Q8, files outside any module that can't affect Go.** Today `paths-ignore` drops only `README.md` and `docs/**`.
  - (a) **A short list that runs nothing in the matrix:**
    - `.claude/**` and `CLAUDE.md`;
    - root `*.md` files;
    - `playground-app/**`, `images/**`, `.devcontainer/**`, `.markdownlint*`, `.pre-commit-config.yaml`, `requirements.txt`, `runtime.txt`, `mkdocs.yml`.

    Anything else outside a module runs everything:
    - the `Makefile`;
    - `.github/**`;
    - `.golangci.yml`;
    - `testdata/`, `config/`, `samples/`, `distributions/`, the submodule pointers;
    - `Dockerfile`, `test/*.sh`.
  - (b) No list: every file outside a module runs everything.
  - **Recommended:** (a). The same list defines the `go` output used in P4.

- **Q9, coverage.**
  - (a) **Per module.** Each test job uploads `<module>-coverage.out` with `flags: <module>`. A new `codecov.yml` sets `carryforward: true` for every flag, so the modules a PR skips keep their last value instead of dropping out of the comparison. The modules that write profiles stay the same as today: the 14 library modules, the 7 flagd modules (now one profile each instead of `flagd-coverage.out`) and `flagd-proxy`.
  - (b) (a), and a profile for every module.
  - (c) One upload from the aggregate, from artifacts.
  - **Recommended:** (a). Splitting `flagd-coverage.out` changes how Codecov groups it once, not the coverage.

## 2. Design

**The changes job (`changes`):**
1. Checkout with `fetch-depth: 2`, which is enough for `github.sha^1`; for a push, fetch `before`.
2. Run `.github/scripts/ci-modules.py`, with Python only and no Go.
3. Output `test` (a JSON list of table entries), `lint` (a list of module directories), `go` (true or false) and `all` (true or false).
4. Write the set, and for each module the reason it is in the set, to the job summary.

**The table `.github/scripts/ci-modules.json`:** one entry per directory in `ALL_GO_MOD_DIRS`.
- Each entry has a `test` command (a Makefile target `test-module MODULE=<dir>` that holds today's flags: `-race` or not, `-timeout 30m` for `test/distributions`, the `-run` filters of `test/upgrade` and `test/image`, the coverage file), or `"test": null` with a reason (both load tests, `test/zero-downtime-flagd-proxy`, `test/testbed`).
- Each entry has optional `submodule`, `always` and `extraEdges` (the manifests rule of Q4) fields.
- **A module with no entry fails `changes`,** so a new module can't go untested unnoticed.
- `flagd-proxy`'s entry tests `./flagd-proxy/pkg/...`, as today.

**The affected set:**
- start with the modules that own a changed file;
- add every module that reaches one of them through the reverse `require` edges and the `extraEdges`;
- add the `always` entries, whenever the change touches Go (`go` is true).

A changed file outside every module and outside Q8's list sets `all`. So do a push to `main`, `workflow_dispatch`, `FULL_CI=true` and a missing base.

**The jobs:**

| Job | Runs | Needs |
|---|---|---|
| `changes` | always | — |
| `test-module` (matrix, `fail-fast: false`, name `test (<dir>)`) | `if: needs.changes.outputs.test != '[]'` | `changes` |
| `test` (aggregate) | `if: always()` | `changes`, `test-module` |
| `lint` (Q2 (b): golangci-lint per affected module in one job, with the aggregate checks of Q6 in its last step) | `if: always()` | `changes`, `repo-checks` |
| `repo-checks` (Q5 (a)) | always | — |

**Kept:**
- the `GOPATH`/`GOBIN` env of today's jobs;
- `CONTRACT_EXTERNAL_COMMIT`;
- `git submodule update --init test-harness`, only where the entry asks for it;
- `make test`, `make lint`, `test-modules` and `test-flagd`, for local runs.

**P4's one change to an out-of-scope job:** `integration-test` and `builder-e2e` get `needs: changes` and `if: needs.changes.outputs.go == 'true'`. Their steps don't change.

## 3. Tests

**Characterization first (PR C):**

| Pin | Test | Pins |
|---|---|---|
| C1 | `test_ci_modules.py` `TestTableMatchesMakeTest` | The `test` commands of the table over `all` are exactly what `make -n test` runs today, module by module. Same flags, same `-run` filters, same coverage files; `test-flagd`'s one invocation and `test-contrib`'s one invocation each split into per-module commands, with the packages unchanged. Every `ALL_GO_MOD_DIRS` entry is in the table |
| C2 | `TestLintCoversEveryModule` | The lint set over `all` equals `ALL_GO_MOD_DIRS`, the list `make lint` runs today |
| C3 | `test_workflows.py` `test_jobs`, as it is | Recorded before P2: its regex `^  ([a-z][a-z-]*):\n` silently skips `builder-e2e` (a digit) and `integration-test` (a trailing comment). P2 tightens it to every job id and records the new list deliberately |

**Per PR:**
- **P1:** the `TestAffected*` cases of `test_ci_modules.py`, each a list of changed paths against the table and a fixture graph, plus one run against the real repository:

| Case | Expected |
|---|---|
| `sync/sse/sse.go` | `sync/sse` and the always set |
| `sync/s3/s3.go` | `sync/s3`, `sync/builder`, `flagd-proxy`, `runtime` and its services, `flagd`, the `/distribution` examples, `test/distributions` (manifest edge), and the always set |
| `core/pkg/x.go` | `core`'s closure |
| `runtime/x_test.go` | `runtime`, `flagd` (the cycle) and their dependents |
| `Makefile`, `.github/workflows/build.yaml`, `testdata/go-packages.txt`, `test-harness` (the pointer), `test/contract-external.sh` | `all` |
| `.claude/plans/x.md`, `CLAUDE.md` | empty, `go` false |
| a file of a module that the head deleted | `all` |
| a new module directory that has no table entry | `changes` fails |
| no base, or an all-zero base | `all` |

- **P1 also adds** `TestChangeSetIsFirstParent`, a fixture repository with a merge commit whose base moved.
- **P2:**
  - `test_workflows.py` gets `test_aggregate_jobs`: `test` and `lint` exist with `if: always()`, need `changes` and their work jobs, and fail on `failure` and `cancelled`;
  - the job list is updated;
  - `test_matrix_guarded`: the matrix job has the empty-set `if`.
  - **A dry run in CI:** a PR that touches one leaf, one that touches `core`, and one that touches the workflow. Their job lists are recorded in the PR description, against the table in section 1.
- **P3:** `test_repo_checks_job`: every check of today's `lint` job appears in `repo-checks` or in `lint`, by step command.

## 4. PR slicing

| PR | Model | Base | Content | Starts |
|---|---|---|---|---|
| S | mechanic | `main` (straight to `main`) | `CLAUDE.md` "Local runs and CI": CI runs the affected modules on a PR and everything on `main` (Q3) | after the decisions |
| P0 | implementer | `main` | `setup-go` caching from `**/go.sum` in today's jobs, and nothing else. Measured on its own run | now |
| C | implementer | `main` | C1–C3, `ci-modules.json`, the `test-module` Makefile target; `make test` unchanged | now |
| P1 | implementer | C | `ci-modules.py` with the graph, the ownership, Q7's change set, Q8's list; a `changes` job in **report-only mode** beside today's `test` and `lint`, which stay as they are | after C |
| P2 | implementer | P1 | the `test-module` matrix and the aggregate `test`; per-module coverage and `codecov.yml` (Q9); the restore-only cache (Q2); K2's job list | after P1 |
| P3 | implementer | P2 | `repo-checks`, `lint-modules`, the new `lint` (Q2, Q5); the cached golangci-lint | after P2 |
| P4 | mechanic | P3 | the `go` `if` on `integration-test` and `builder-e2e`; #176 closed | last |

The critical path is C → P1 → P2 → P3. P0 is independent and lands first, so the matrix's estimates start from a working cache. P1's report-only mode shows the sets on real PRs before anything is skipped.

## 5. Gates

| Gate | Command | PRs |
|---|---|---|
| Workflow pins | `python3 .github/scripts/test_workflows.py` and `test_ci_modules.py` (Windows or WSL) | C, P1–P4 |
| Parity with `make test` | C1 and C2, in CI's `lint`/`repo-checks` | C, P2, P3 |
| The workflow itself | CI on the PR; for P2 and P3, the three dry-run PRs of section 3 | P2, P3 |
| Full run | `workflow_dispatch` on the branch: every module, green | P2, P3 |
| Unit tests, lint | CI (the new jobs) | all |

No Go code changes, so no Go gate runs locally. `actionlint` is worth running locally on `build.yaml` if it's installed; nothing pins it.

## 6. Risks

- **R1, a missed dependency makes a red module look green.**
  - A test that reads another module's files has an edge that isn't in its `go.mod`.
  - The mitigations: the always set and the manifest edges (Q4); every file outside a module runs everything (Q8); push to `main` runs everything (Q3).
  - Residual: a module's test reading a file of another module directory. A grep of `filepath.Join("..", …)` in tests finds today's cases, `test/upgrade` and `test/image`, which are in the always set.
- **R2, the queue.** With 20 concurrent jobs org-wide, a full run of ~49 test jobs, plus the other ~10 jobs, plus other PRs, runs in waves. Q2 (b) halves the job count against (a). If full runs measure slower than today's 994 s, the small modules are batched into shards, as a follow-up.
- **R3, cache races and partial caches.** ~49 jobs saving one cache key leave a cache of whichever finished first. Only the full run on `main` saves (Q2).
- **R4, a change set that is too wide or too narrow.** The `base.sha` diff over-selected on PR #172; Q7 takes the first parent. A force-push whose `before` is gone gets a missing base, which runs everything.
- **R5, the aggregate goes green on a skipped job.** `skipped` counts as green only when the set is empty. `cancelled` is red. `changes` failing makes both aggregates red. Pinned by `test_aggregate_jobs`.
- **R6, coverage noise.** Splitting `flagd-coverage.out` and carrying flags forward changes how Codecov groups coverage, once. The upload doesn't fail CI, as today.
- **R7, K2's job regex.** It already misses two jobs (C3); P2 fixes it, and records the job list deliberately.
- **R8, drop-in (C-003).** Nothing observable by operators or clients changes; only CI does.

## 7. Considered

- **Path-filter actions** (`dorny/paths-filter`) or `go-modules-changed` tools: a third-party action for what 100 lines of Python and the existing gate pattern already do. The graph would still have to come from Q1.
- **Turborepo-style content hashing per module:** it skips by input hash instead of by diff. Too much for the proof of concept.
- **Larger runners:** not available on Free.

## 8. Changelog

### Revision 1

The first revision; no opposer findings yet.
