# Step 15 plan: a faster `test` and `lint` (issue #176)

**Status: revision 2. 4 questions are open for the project lead (section 0, A–D).**
- Revision 2 resolves `step-15-opposer.md` (1 blocker, 4 major, 6 minor; section 8) and adds the project lead's input on ordering (QC).

**Decision (#176):** step 15, through the loop. Scope: the unit tests and lint only.

**Goal:** a PR's CI wall clock. Today that is `test`, at 994 s.

**Hard constraints:**
- the checks named `test` and `lint` stay, as fixed aggregate jobs, so the merge rule ("CI green") and K2's job list in `.github/scripts/test_workflows.py` keep working;
- a change outside the part of the repository a job covers runs that job in full;
- `make test` and `make lint` keep working locally, as today, over the whole repository.

**Out of scope:** `integration-test`, `builder-e2e`, `upgrade-test` (with `image-test`), `release-dry-run`, `fips`, `docker-local`, `benchmark` and `docs-check`. They stay whole-repo jobs, with today's steps. Their rule is "skipped only when no Go code changed".
- P0 corrects one line each in the `upgrade-test` and `release-dry-run` gates: the change set (finding 3).
- QD (a) adds the "no Go code changed" skip on pushes.
- Nothing else in them changes.

**Base:** `main` @ c46bbd6c.

**Read:** `Makefile` (`workspace-init`, `ALL_GO_MOD_DIRS`, the `*_MODULE_DIRS`, `TEST_TARGETS`, `test-modules`, `test-flagd`, `test-contrib`, `lint`, `check-*`), `.github/workflows/build.yaml`, `.github/scripts/test_workflows.py`, `test/contract-external.sh`, and every module's `go.mod`.

## 1. Measurements

**Run 41976924232** (PR #172, which changed one file under `test/distributions`). Verified by the opposer.
- **Wall clock per job:** `test` 994 s, `upgrade-test` 980 s, `integration-test` 519 s, `lint` 390 s, `builder-e2e` 273 s, `benchmark` 279 s.
- **`make test` takes 976 s:**

| Target | s | Target | s |
|---|---|---|---|
| `test-modules` (14 library modules) | 378 | `test-evaluator-testkit` | 35 |
| `test-distributions` | 268 | `test-contract-external` | 28 |
| `test-flagd` (7 modules) | 186 | the other 7 examples | 36 |
| `test-flagd-proxy` | 20 | `test-builder` | 10 |
| `test-contrib` (5 modules) | 8 | release, upgrade-accepted, image pins | 5 |

- **`lint` takes 390 s:**
  - `check-flagd-full-generated` 127 s;
  - `go install golangci-lint` 52 s;
  - linting the 52 modules 184 s, of which 98 s is `flagd`, cold;
  - the repo-wide checks 13 s.
- **The Go cache never restores.** There is no root `go.sum`, so `setup-go` finds nothing to restore. The golangci-lint cache isn't cached at all.
- **The change set of the existing gates is too wide.** They diff `pull_request.base.sha..github.sha`. For PR #172 that diff held a later commit on `main` (`test/upgrade/testdata/issue-78.txt`), so the 16-minute differential ran. The first parent of the merge commit, `github.sha^1`, is the base that the merge was built on.
- **The queue.**
  - The org's plan is Free and the repository is public: minutes are free, but at most 20 jobs run at once org-wide.
  - In the last 100 `build` runs (~11 h), 38 were pushes to `main`, and 16 of those changed only `.claude/` or `CLAUDE.md`.
  - `build.yaml` has no `concurrency` group, so a superseded PR run keeps its slots.
- **The PR wall clock floor** is `integration-test`, ~8.7 min, and `upgrade-test`, ~16.5 min, when it runs.

**Estimates** for the `test` check, against 994 s. O, the per-job overhead, is not measured yet; P0 measures it, along with the cache's size and restore time.

| Approach (QA) | Jobs per run | `test` wall clock | PR wall clock |
|---|---|---|---|
| (a) four fixed shards (section 2) | 4 | longest shard, `test-modules`: 378 s + O ≈ 7 min, for every PR | ~8.7 min (`integration-test`) |
| (b) affected-set matrix, one job per module | 1–49 | leaf ~1.5 min; `core` or fallback ~6–8 min, more under a busy queue | ~8.7 min |
| today | 1 | 16.5 min | 16.5 min |

**Both approaches end a PR when `integration-test` ends.**
- (b) saves runner time on narrow PRs, and minutes are free here.
- (b) costs: the graph, `extraEdges`, the fallback list, the risk of missing a dependent, and up to 49 jobs in a 20-slot org.

## 0. Questions for the project lead

Four questions. Each recommendation keeps today's coverage of every module on every PR, unless the question says otherwise.

- **QA, the approach** (opposer finding 3).
  - (a) **Four fixed shards.** These run in parallel, each over its own whole part of the repository, on every Go PR:
    - `test-modules`;
    - `test-distributions`;
    - `test-flagd`, `test-flagd-proxy` and `test-contrib`;
    - the rest of `TEST_TARGETS`.

    Behind them sits an aggregate `test`. With P0's cache fix the longest shard is ~7 min, and the PR ends with `integration-test`.

    **What it doesn't need:** a module graph, edges or a fallback list, and no dependent can be missed.
  - (b) **The affected-set matrix of revision 1, as #176 words it:** one job per affected module, from the `require` graph, with every fix the opposer asked for (section 2 (b)). A narrow PR runs fewer jobs. The wall clock is no better than (a), and it is worse when the queue is full.
  - (c) **(a) now, and (b) as a later step,** if runner load rather than wall clock becomes the limit.
  - **Recommended:** (a), which is (c) in practice. It deviates from #176's "one job per component", because the measured goal, wall clock, is met without the graph's risks.

- **QB, lint.**
  - (a) **One `lint` job over all 52 modules,** with the golangci-lint binary (v2.14.0) and its cache directory cached. Beside it, a fixed `repo-checks` job: `check-flagd-full-generated`, `check-package-coverage`, `check-flagd-full-modules`, the Python tests, the boundaries and the `x/exp` greps. `lint` ≈ 3.5 min, `repo-checks` ≈ 2.5 min, both under `integration-test`. `make lint` is unchanged for local runs.
  - (b) As (a), but linting only the affected modules. This needs QA (b)'s graph, and lint isn't on the critical path.
  - (c) One lint job per module, as #176 words it: up to 52 jobs of 1–5 s of work behind ~45 s of overhead each.
  - **Recommended:** (a).

- **QC, ordering** (the project lead's input). Wall clocks are with QA (a) and QB (a), estimated.
  - (a) **The project lead's sequence: lint, then the tests, then integration.** That is ~3.5 + ~7 + ~8.7 ≈ **19 min**, or ~22 min with today's lint. It only pays off when a run fails early.
  - (b) **A compile gate first** (`go build ./...` and `go vet ./...` over the workspace, ~1.5–2 min, not measured), then lint, the tests and integration in parallel: ≈ **10.5 min**. It catches a break like 8eb82d76's duplicate `retryOptedIn`, and frees the other jobs' queue slots.
  - (c) **No gate, everything in parallel:** ≈ **8.7 min**. A compile error already fails every job within its own compile (~1–2 min), and QD's `concurrency` group cancels a run that a fix-up push supersedes.
  - **Recommended:** (c). Under QA (a) a run is ~10 jobs, so the gate saves few slots, and it costs every green PR ~2 min. Revisit (b) if QA (b) is chosen.

- **QD, pushes to `main` and the queue** (opposer finding 4; revision 1's Q3 and Q8).
  - (a) **Three changes:**
    - a push to `main` runs everything;
    - a push or PR that changes only `.claude/**` or `CLAUDE.md` runs no build at all: both are added to `paths-ignore`, with K2's `test_paths_ignore_docs_on_push_and_pull_request` updated deliberately;
    - a `concurrency` group `build-${{ github.ref }}` that cancels in-progress runs for pull requests only.

    This removes ~16 of every 38 push runs.
  - (b) As (a), without the `paths-ignore` change. Plan pushes keep running the full build, as today.
  - (c) Pushes to `main` run only the affected set. That needs QA (b), and `main` then stops being a full baseline.
  - **Recommended:** (a).

**Decided in revision 2, without a question** (each follows from a finding):
- **The change set** of a pull request is `github.sha^1..github.sha`. P0 applies it to the `upgrade-test` and `release-dry-run` gates, which shortens a PR #172-like PR from ~16.5 to ~8.7 min.
- **Coverage stays as it is today:** the shards write the same profiles and upload them as today.

## 2. Design

### (a) Shards (QA (a))

| Job | Runs | Content |
|---|---|---|
| `test-shard` (matrix: `modules`, `distributions`, `flagd`, `rest`; `fail-fast: false`; name `test (<shard>)`) | every run | `make test-shard SHARD=<shard>`, whose targets are listed in the `Makefile` beside `TEST_TARGETS` |
| `test` (aggregate) | `if: always()`, `needs: test-shard` | green only when `test-shard` is `success` |
| `lint` | every run | QB (a) |
| `repo-checks` | every run | QB (a) |

- **`rest`** holds `test-builder`, `test-upgrade-accepted`, `test-image-pins`, every example target, `test-evaluator-testkit`, `test-contract-external` and `test-release`.
- **Submodules:** `test-harness` is initialised in the `flagd` shard (`runtime/cmd` reads it, finding 6) and in `rest` (the evaluator testkit).
- **Coverage:** each shard uploads the profiles it writes. That is the same set of files as today.
- **The cache:** only the `push` run on `main` saves it, one key per job (`go-<job>-${{ hashFiles('**/go.sum') }}`). Pull requests only restore (finding 5).

### (b) The affected-set matrix (only if QA (b))

Revision 1's design, plus the opposer's fixes:
- **A change to any tracked `go.mod` or `go.sum` sets `all`.** In a workspace, one MVS over all 52 modules decides every version (finding 1).
- **`extraEdges`:**
  - `builder` → `test/distributions` and `flagd`;
  - `test/inprocess-example/filegrpc` → `test/inprocess-example/file`;
  - `test/distributions/example/cmd` and `test/distributions/sync-vendor/cmd` → `test/distributions`;
  - every module a manifest names → `test/distributions` (finding 2).
- **`submodule: test-harness`** is derived by grepping for `test-harness`, so `runtime` gets it (finding 6).
- **The module set:**
  - edges are keyed on the module paths read from the `go.mod` files, not on a prefix;
  - the set of modules comes from `ALL_GO_MOD_DIRS`'s prune list;
  - `replace`s without a `require` are inert and give no edge (finding 7).
- **`test-modules`, `test-flagd` and `test-contrib` loop over `test-module MODULE=…`,** so C1 checks only that the table covers `ALL_GO_MOD_DIRS` (finding 8).
- **Report-only mode, while today's `test` still runs:**
  - each `FAIL` package is mapped to its module, and a failing module outside the set is reported;
  - on pushes to `main`, the closure is compared with `go list -deps -test` (finding 9).
- **The aggregate:** a skipped matrix is green only when the set is empty and `all` is false (finding 10).
- **The matrix:** `max-parallel: 10` (finding 4).

## 3. Tests

**Characterization first (PR C):**

| Pin | Test | Pins |
|---|---|---|
| C1 | `test_workflows.py` `test_shards_cover_make_test` | The union of the `Makefile`'s shard target lists is exactly `TEST_TARGETS`, with no target in two shards. A new target in `TEST_TARGETS` that no shard lists fails |
| C2 | `test_lint_covers_every_module` | The lint job runs `make lint-modules`, which loops over `ALL_GO_MOD_DIRS` as `make lint` does today |
| C3 | `test_jobs`, tightened | The regex reads every job id (`builder-e2e` and `integration-test` were skipped, verified). The new list is recorded deliberately |

**Per PR:**
- **P0:**
  - the cache's size and restore time, and O, in the PR description;
  - `test_change_set_first_parent`: both gates read `github.sha^1` for a pull request.
- **P1:**
  - `test_aggregate_test`: `test` has `if: always()`, needs `test-shard`, and is red on `failure`, `cancelled` and `skipped`;
  - `test_repo_checks_job`: every check of today's `lint` job appears in `lint` or `repo-checks`, by command;
  - `test_concurrency_group` and the `paths-ignore` update (QD (a)).
- **The dry run (P1):**
  - one PR as it stands;
  - a `workflow_dispatch` full run;
  - one PR run beside a push to `main`, to measure the queue (finding 4).

  Their wall clocks are recorded against section 1.

## 4. PR slicing

| PR | Model | Base | Content | Starts |
|---|---|---|---|---|
| P0 | implementer | `main` | The cache, per job, saved only on `main` (finding 5); golangci-lint's binary and cache; the first-parent `BASE` in the `upgrade-test` and `release-dry-run` gates. Measures the cache, O and the test cache's effect (finding 3: `-count=1` targets and files' mtimes miss it) | now |
| C | implementer | `main` | C1–C3, the shard target lists in the `Makefile`, `lint-modules`; `make test` and `make lint` unchanged | now |
| P1 | implementer | C (and P0 merged) | The `test-shard` matrix and aggregate `test`; `lint` and `repo-checks`; the `concurrency` group and `paths-ignore` (QD (a)); K2's job list | after C |
| S | mechanic | `main` | `CLAUDE.md` "Local runs and CI": the shards, `repo-checks`, the plan pushes that skip the build | after P1 |

Under QA (b), P1 becomes revision 1's P1–P3, with section 2 (b) and the tests of finding 9.

## 5. Gates

| Gate | Command | PRs |
|---|---|---|
| Workflow pins | `python3 .github/scripts/test_workflows.py` | P0, C, P1 |
| Parity with `make test` and `make lint` | C1, C2, in CI's `repo-checks` | C, P1 |
| The workflow itself | CI on the PR, and the dry runs of section 3 | P0, P1 |
| Full run | `workflow_dispatch` on the branch, green | P1 |

No Go code changes, so no Go gate runs locally.

## 6. Risks

- **R1, a missed dependency.** None under QA (a): every shard tests its whole part of the repository on every Go PR. Under QA (b), see section 2 (b) and the readers outside their module:
  - `test/distributions`, `test/release`, `test/upgrade` and `test/image` (always set or edges);
  - `runtime/cmd` and `test/evaluator-testkit` (the submodule);
  - `test/inprocess-example/file` (edge) (finding 11).
- **R2, the queue.** QA (a) adds three jobs per run. QD (a) removes ~40% of the push runs and cancels superseded PR runs.
- **R3, the cache.** It is saved only on `main`, one key per job, and its size is measured in P0 against the 10 GB limit per repository.
- **R4, a shard outgrowing `integration-test`.** `test-modules` is the largest shard at 378 s. If it passes ~8 min, it splits in two along `GO_LIB_MODULE_DIRS`.
- **R5, the aggregate goes green on a skipped job.** `test` is red unless `test-shard` is `success`. Pinned by `test_aggregate_test`.
- **R6, K2's job regex.** It already misses two jobs (C3); C fixes it.
- **R7, drop-in (C-003).** Nothing observable by operators or clients changes; only CI does.

## 7. Considered

- **Path-filter actions** (`dorny/paths-filter`): a third-party action for what the gate pattern already does, and QA (a) needs no path filter.
- **Go's test cache alone:** P0 measures it. It could shorten the shards further, but the targets that pass `-count=1` and the tests that open files miss it.
- **Larger runners:** not available on Free.

## 8. Changelog

### Revision 2 (`step-15-opposer.md`; the project lead's ordering input)

| Finding | Severity | Outcome |
|---|---|---|
| 1 A `go.mod` change reaches every module through the workspace's MVS | blocker | Adopted. Under QA (a) it doesn't arise; under QA (b) any `go.mod` or `go.sum` change sets `all` |
| 2 Edges the `require` graph lacks | major | Adopted. Verified `distributions_test.go:64` (`go run ./cmd/relay-builder`) and `inprocess_test.go:275` (`../filegrpc`). Listed in section 2 (b); none needed under QA (a) |
| 3 The saving doesn't need an affected set | major | Adopted as QA, recommending shards. The first-parent gate fix moves into P0 |
| 4 The queue | major | Adopted as QD: `paths-ignore` for plan pushes, a `concurrency` group, `max-parallel` under QA (b), a dry run beside a push |
| 5 P0's cache race, the cache size | major | Adopted. One key per job, saved only on `main`, golangci-lint's cache cached, the size and O measured in P0 |
| 6 `runtime` needs `test-harness` | minor | Adopted. Verified `runtime/cmd/loadconfig_test.go:18-20`. The `flagd` shard initialises it |
| 7 Module paths and the module set | minor | Adopted, in section 2 (b) |
| 8 C1 can't read `make -n test` | minor | Adopted. C1 compares the target lists. Under QA (b), the loops call `test-module` |
| 9 Report-only mode should measure misses | minor | Adopted, in section 2 (b) |
| 10 Green on an empty set with `all` | minor | Adopted, in section 2 (b) |
| 11 R1's list | minor | Adopted (R1) |
| Ordering (project lead) | — | QC, three options with wall clocks; recommends no gate |

Revision 1's Q1, Q2, Q4, Q6 and Q9 apply only under QA (b), and fold into section 2 (b). Q3 and Q8 became QD; Q5 became QB (a); Q7 is decided.
