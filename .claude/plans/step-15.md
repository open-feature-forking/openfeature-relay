# Step 15 plan: a test job per affected module, one lint job, a lighter build for markdown (issue #176)

**Status: revision 3, final. The project lead has decided QA–QD, and added the spec lint as a CI gate (slice L).**

**Slice R (the release dry run), decided:**
- **QE (c):** the parts in parallel, and a reduced set on PRs. The full set runs on `main`, with `FULL_CI`, and on `workflow_dispatch`. That is ~8–9 min on a PR and ~11 min on `main`, against 43–71 min today.
- **QF (a):** `go build -a` stays.

R is ready to implement, from `main`, in parallel with the others. Open: none.
- Revision 2 resolved `step-15-opposer.md` (1 blocker, 4 major, 6 minor; section 8).
- Revision 3 records the decisions and the markdown rule that QD added.
- Open: none.

**Decisions (section 8):**
- QA (b), the affected-module matrix, with every fix in section 2;
- QB (a), one `lint` job plus `repo-checks`;
- QC (c), no gate;
- QD (a), plus a lighter run for markdown-only changes.

**Hard constraints:**
- the checks named `test` and `lint` stay, as fixed aggregate jobs, so the merge rule ("CI green") and K2's job list in `.github/scripts/test_workflows.py` keep working;
- a change outside every module, other than markdown and `.claude/`, runs every module;
- `make test` and `make lint` keep working locally, as today, over the whole repository.

**Out of scope:** `integration-test`, `builder-e2e`, `upgrade-test` (with `image-test`), `release-dry-run`, `fips`, `docker-local`, `benchmark` and `docs-check`. They stay whole-repo jobs, and their steps don't change.
- **The one rule for them:** they skip only when no Go code changed. QD's `paths-ignore` (markdown and `.claude/` only) gives them that.
- P0 corrects one line in each of the `upgrade-test` and `release-dry-run` gates: the change set (Q7).

**Base:** `main` @ 0066517e.

**Read:** `Makefile` (`workspace-init`, `ALL_GO_MOD_DIRS`, the `*_MODULE_DIRS`, `TEST_TARGETS`, `test-modules`, `test-flagd`, `test-contrib`, `lint`, `check-*`, `generate-docs`), `.github/workflows/build.yaml`, `markdown-checks.yaml` and `docs.yaml`, `.github/scripts/test_workflows.py`, `test/contract-external.sh`, and every module's `go.mod`.

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

- **Inside them:**
  - `sync/openfeature-operator` 87 s, `core` 81 s, `gcs` 45 s, `sync/builder` 40 s;
  - the `flagd` package 170 s, `runtime` 99 s.
- **`lint` takes 390 s:**
  - `check-flagd-full-generated` 127 s;
  - `go install golangci-lint` 52 s;
  - linting the 52 modules 184 s, of which 98 s is `flagd`, cold;
  - the repo-wide checks 13 s.
- **The Go cache never restores** (no root `go.sum`), and the golangci-lint cache isn't cached.
- **The existing gates diff `pull_request.base.sha..github.sha`.** For PR #172 that diff held a later commit on `main`, so the 16-minute differential ran.
- **The queue.** Free plan, public repository: minutes are free, but at most 20 jobs run at once org-wide. Of 38 pushes to `main` in ~11 h, 16 changed only `.claude/` or `CLAUDE.md`. There is no `concurrency` group.

**The module graph.**
- Its edges are the `require`s between relay modules, `// indirect` ones included. The opposer checked that no import lacks a `require`.
- `core` is required by 41 of the 52 modules; the library modules each require only `core`.
- There is one cycle, `runtime` ↔ `flagd`, through `runtime`'s tests.

**Estimates** for the `test` check, against 994 s. O, the per-job overhead, is measured in P0.

| PR touches | Modules tested | `test` wall clock | PR wall clock |
|---|---|---|---|
| a leaf, e.g. `sync/sse` | `sync/sse` and the always set | ~1.5 min | ~8.7 min (`integration-test`) |
| `core`, or a fallback path | ~47–49 | ~6–8 min with `max-parallel: 10`, more under a busy queue | ~8.7 min |
| any `go.mod` or `go.sum` | all | ~6–8 min | ~8.7 min, or ~16.5 min when `upgrade-test` runs |
| markdown only | none | — (`docs-check` and the upgrade-guide test, ~1 min) | ~1 min |

## 2. Design

### The changes job (`changes`)

1. Checkout with `fetch-depth: 2`; for a push, fetch `before`.
2. Run `.github/scripts/ci-modules.py`, with Python only and no Go.
3. Output `test` (a JSON list of table entries), `lint-all` (true), `all` (true or false), and the reason for each module, to the job summary.

**The change set (Q7):**
- **a pull request:** `git diff --name-only github.sha^1 github.sha`;
- **a push:** `before..after`;
- **`all`:** a missing or all-zero base, a push to `main`, `workflow_dispatch` and `FULL_CI=true` all set it (QD).

**Ownership:**
- a file belongs to the module with the longest matching directory in the base or the head, so a deleted or moved module still counts;
- the module set is `ALL_GO_MOD_DIRS`, with its prune list (`test/integration`, `test-harness`, `spec`, `schemas`, `playground-app`, `.claude`, the `build` and `.flagd-*` directories);
- `**/*.md` files and `.claude/**` belong to nothing and set nothing: no Go code embeds or reads markdown, apart from the two readers that the markdown workflow covers (section 2, "Markdown");
- a file outside every module **sets `all`**.

**`all` also comes from any change to a tracked `go.mod` or `go.sum`** (opposer finding 1). In a workspace, one MVS over all 52 modules decides every version.

**Edges.** They are keyed on the module paths read from the `go.mod` files, under both prefixes (`github.com/open-feature-forking/openfeature-relay/…` and `github.com/open-feature/flagd/…`), never on a prefix match, so `buf.build/gen/go/open-feature/flagd/…` doesn't match.
- **`replace`s without a `require`** are inert and give no edge (finding 7).
- **The `extraEdges`** in the table (finding 2):

| From (changed) | To (also affected) | Why |
|---|---|---|
| `builder` | `test/distributions`, `flagd` | `test/distributions` runs `go run ./cmd/relay-builder` (`distributions_test.go:64`); the Builder generates `flagd`'s `go.mod`, `main.go` and `components` |
| `test/inprocess-example/filegrpc` | `test/inprocess-example/file` | `TestBoundaryCatchesForbiddenDependency` reads `../filegrpc` (`inprocess_test.go:275`) |
| `test/distributions/example/cmd`, `test/distributions/sync-vendor/cmd` | `test/distributions` | its manifests build them |
| every module that `distributions/*/manifest.yaml` or a manifest under `test/distributions/` names | `test/distributions` | the manifests are read when the job runs |

**The table `.github/scripts/ci-modules.json`:** one entry per `ALL_GO_MOD_DIRS` directory.
- **`test`:** `make test-module MODULE=<dir>`, or `null` with a reason (both load tests, `test/zero-downtime-flagd-proxy`, `test/testbed`).
- **`always`:** `builder`, `test/release`, `test/upgrade`, `test/image`, and the pseudo-entry `contract-external`. They run whenever the set isn't empty.
- **`submodule: test-harness`:** derived by grepping each module for `test-harness`, so `runtime` and `test/evaluator-testkit` get it (finding 6).
- **`coverage`:** for the 14 library modules, the 7 flagd modules and `flagd-proxy`.
- **A directory with no entry fails `changes`.**

**The Makefile.**
- `test-module MODULE=<dir>` holds today's flags per module: `-race` or not, `-timeout 30m` for `test/distributions`, the `-run` filters of `test/upgrade` and `test/image`, the coverage file, and `./flagd-proxy/pkg/...` for `flagd-proxy`.
- `test-modules`, `test-flagd` and `test-contrib` loop over it, so `make test` and the matrix run the same commands (finding 8).
- `test-flagd`'s single coverage profile splits into one per module.

### The jobs

| Job | Runs | Needs | Content |
|---|---|---|---|
| `changes` | always | — | the above |
| `test-module` (matrix from `changes.outputs.test`, `fail-fast: false`, `max-parallel: 10`, name `test (<dir>)`) | `if: needs.changes.outputs.test != '[]'` | `changes` | `make test-module`; a coverage upload with `flags: <module>` |
| `test` (aggregate) | `if: always()` | `changes`, `test-module` | red unless `changes` is `success` and `test-module` is `success`, or `skipped` with an empty set and `all` false (finding 10) |
| `lint` | always | — | `make lint-modules` over all 52 modules; golangci-lint v2.14.0 from a cached `GOBIN`, and its cache directory cached (QB (a)) |
| `repo-checks` | always | — | `check-flagd-full-generated`, `check-package-coverage`, `check-flagd-full-modules`, the Python tests, `check-module-boundary check-evaluator-boundary`, the `x/exp` greps (QB (a)) |

- **The aggregate `lint` is the `lint` job.** `repo-checks` is a check of its own, run beside it, with nothing gated on it (QC (c)).
- **`make lint`** keeps calling `check-flagd-full-generated` and `lint-modules`, for local runs.
- **Coverage:** a new `codecov.yml` sets `carryforward: true` for every flag, so skipped modules keep their last value.
- **The cache:**
  - one key per job (`go-<job>-${{ hashFiles('**/go.sum') }}`), saved only by the push run on `main`;
  - the `test-module` entries only restore (`actions/cache/restore`). A full run on `main` saves the key from one entry, `flagd`, whose closure is the largest (finding 5).
- **Kept as today:** the `GOPATH`/`GOBIN` env, and `CONTRACT_EXTERNAL_COMMIT`.

### Report-only first (P1)

While today's serial `test` still runs in full, `changes` also:
- maps each `FAIL\t<package>` of `make test` to its module, and reports any failing module outside the set as a **miss**;
- on pushes to `main`, compares the `require` closure with `go list -deps -test` over the workspace, and reports the differences.

**P2 switches over only after** P1 has reported on at least ten PR runs with no miss (finding 9).

### Queue and pushes (QD (a))

- **`concurrency`:** `group: build-${{ github.ref }}`, `cancel-in-progress: ${{ github.event_name == 'pull_request' }}`.
- **`build.yaml`'s `paths-ignore`,** on push and pull request, becomes:
  - `"**/*.md"`;
  - `"docs/**"`;
  - `".claude/**"`.

  `README.md` is covered by the first entry. A change touching only these files runs no build. A mixed change runs the build as before.
- **A push to `main`** that touches anything else runs everything.

### Markdown (QD's addition)

**What a markdown-only change can break, checked in the repository:**

| Check | Today on a markdown-only change | After |
|---|---|---|
| markdownlint (`markdown-checks.yaml`, `**.md`, `.claude/` included) | runs | unchanged |
| `mkdocs build --strict` (`docs.yaml`, `docs/**`, `mkdocs.yml`), which fails on a broken internal link | runs | unchanged. `docs_dir` is `docs`, and no `include-markdown` pulls in a file from outside it |
| `docs-check`: the CLI reference that `make generate-docs` generates into `docs/` | **skipped** (`paths-ignore: docs/**`), so a hand edit of a generated page goes unnoticed | runs |
| `make test-upgrade-accepted`: `TestGuideCoversAcceptedItems` reads `docs/guides/upgrade-to-flagd-full.md` | **skipped** | runs |
| `go:embed` of a `.md` file | none in the repository | — |

**How it is selected:** `build.yaml`'s `paths-ignore` drops the markdown-only run, as above. A new light workflow, `.github/workflows/docs-check.yaml`, then runs the two checks that can break:
- **Triggers:** push to `main` and pull request, `paths: ["**/*.md", "docs/**", "!.claude/**", "!CLAUDE.md"]`. Plan pushes therefore stay without a build (QD (a)), and markdownlint still lints them.
- **Jobs:**
  - `docs-check` (Go setup, `make workspace-init`, `make generate-docs`, a `git status` diff), moved from `build.yaml`;
  - `upgrade-guide` (`make test-upgrade-accepted`).
- **`build.yaml`'s `docs-check` job stays,** because the CLI reference also changes with Go code. On a mixed change both run (~36 s twice).

**Not added:**
- **an external link checker:** links to other sites are flaky in CI, and `mkdocs --strict` already checks the internal ones;
- **the spec lint:** it runs from the aiup plugin cache, not from the repository. It stays a local gate, as `CLAUDE.md` says.

### Spec lint (the project lead's later decision)

**The spec lint becomes a CI gate.** Today it is a local gate only (`CLAUDE.md`).

**The script, vendored from aiup-core 2.28.0** (`~/.claude/plugins/cache/ai-unified-process-marketplace/aiup-core/2.28.0/skills/`):
- `spec_lint.py` needs only the standard library.
- It loads two sibling scripts from `<skills>/*use-case-spec/scripts/validate_use_case.py` and `<skills>/*test-case/scripts/bpmn_paths.py`, and silently drops them when they're missing.
- Copied flat into `.github/scripts/`, it reports 7 info findings on `main` instead of 6, and skips the use-case validator's checks.
- So the three files are vendored **in the plugin's own layout**, unmodified apart from a header comment that names the source path, the version 2.28.0 and the upstream file's sha256:
  - `.github/scripts/aiup/spec-review/scripts/spec_lint.py`;
  - `.github/scripts/aiup/use-case-spec/scripts/validate_use_case.py`;
  - `.github/scripts/aiup/test-case/scripts/bpmn_paths.py`.
- On `main` @ 0066517e, the vendored tree gives `0 error(s), 5 warning(s), 6 info(s)`, the same as the plugin's copy. `--self-test` passes.

**The job:** a new workflow, `.github/workflows/spec-lint.yaml`, with one job `spec-lint`.
- **Steps:**
  - `python3 -I .github/scripts/aiup/spec-review/scripts/spec_lint.py --self-test`;
  - `python3 -I .github/scripts/aiup/spec-review/scripts/spec_lint.py --docs docs`, without `--strict`. Exit 1 on an ERROR fails the job; warnings don't.
- **Triggers:** push to `main` and pull request, with `paths:`
  - `docs/vision.md`, `docs/requirements.md`, `docs/glossary.md`, `docs/use_cases.puml`, `docs/use_cases/**`, `docs/test_cases/**`;
  - `docs/.spec-lint-baseline.json`, for when a baseline is added;
  - `.github/scripts/aiup/**`;
  - `.github/workflows/spec-lint.yaml`.
- **Why a workflow of its own:** its paths are exact, without a gate step. `build.yaml` ignores all of them (`docs/**`, `**/*.md`), and `docs-check.yaml` triggers on them too, so a spec-only PR runs the markdown light set and the spec lint, and nothing else.
- **Updating the script:** a new aiup-core version means re-vendoring all three files and their header comments in one commit.

**K2:**
- `test_paths_ignore_docs_on_push_and_pull_request` pins the new three-line block, twice;
- `test_spec_lint_workflow` pins `spec-lint.yaml`'s paths, the command without `--strict`, and that the two sibling scripts exist where `load_sibling` looks;
- `test_docs_check_workflow` pins `docs-check.yaml`'s paths and its two jobs;
- `test_jobs` is unchanged by this part.

### Release dry run (slice R, the project lead's later request)

**The real PR bottleneck.**
- In the last 100 `build` runs, the release dry run's gate fired on 23. Each of those took **43–71 min** (2559–4255 s), against `test`'s 16.5 min.
- B1 (#163) is waiting on run 41982722997, which was still in progress when this was written.

**Where the time goes,** from run 41977622128 (job 127080054036; main step 55 min):

| Phase | min | What it builds |
|---|---|---|
| flagd archives | 18.0 | 11 cross-compiled `go build -a` (6 standard, 5 FIPS), serial, ~98 s each; SBOM; checks |
| flagd-proxy archives | 17.6 | the same 11, ~96 s each; flagd-proxy pulls in every sync provider through `sync/builder` |
| flagd images | 4.1 | `docker build` standard and FIPS, linux/amd64, then the image configuration checks |
| flagd multi-arch OCI | 3.8 | `buildx` linux/amd64 + linux/arm64 with a fresh `docker-container` builder (cold cache) |
| flagd-minimal, full path | 6.3 | 11 archives (~25 s each, a small closure) and 2 × 2-platform OCI images with provenance and SBOM |
| the 4 other distributions | 4.5 | one linux/amd64 archive each, and the published-manifest checks |

**What the measurement rules out:**
- **arm64 is not emulated.** Both Dockerfiles build in `FROM --platform=$BUILDPLATFORM` with `GOOS=${TARGETOS} GOARCH=${TARGETARCH}`. The arm64 stage is a distroless `COPY`, with no `RUN`, and the job sets up no QEMU. Cross-compiling arm64 in the Dockerfile is already done and would gain nothing.
- **The 11 targets of one item share no build cache with each other** (GOOS, GOARCH and GOFIPS140 differ), so `-a` costs little within an item. Its cost is across items: flagd-proxy recompiles the dependencies that flagd has just built for the same target. That gain is not measured.

**Constraints:**
- `release-please.yaml` (K2 hash), `release-build.sh` (which the release calls), and the two Dockerfiles (K3 hashes, and the image drop-in rule) stay unchanged under every option but QF (b);
- what a release would build and publish stays the same;
- only the dry run's own scripts and job change.

**Design (QE (c), recommended):**
- **The job:** `release-dry-run` keeps its id and its gate step, so K2's `test_release_dry_run_runs_when_its_inputs_change` still holds. It becomes a matrix over `part`:
  - `flagd-standard`, `flagd-fips`, `flagd-proxy-standard`, `flagd-proxy-fips` (`DRY_RUN_ITEMS`, `RELEASE_VARIANTS`);
  - `images` (the flagd images, the image configuration, the OCI build);
  - `distributions` (`release-dry-run-distributions.sh`).
- **The scripts:** `release-dry-run.sh` gains `DRY_RUN_PART`. Each part runs its own checks, and derives its expected archive list from the same lists it builds.
- **The gate step** adds a `scope` output:
  - `full` on a push to `main`, on `FULL_CI=true` and on `workflow_dispatch`;
  - `reduced` on a pull request.
- **A reduced scope builds:**
  - `RELEASE_PLATFORMS="linux/amd64"` for each archive part, so one standard and one FIPS archive per item;
  - the flagd images, and the multi-arch OCI build unchanged, because the platform check is the point;
  - only the distributions whose `distributions/<d>/` the change touches, every distribution when `builder/`, a Dockerfile or the release scripts change, and flagd-minimal's full path in two archives.

  The step summary says "reduced; the full set runs on `main`".
- **The first-parent change set** (P0) applies to this gate.

**Estimates:**

| | PR | push to `main`, `FULL_CI` |
|---|---|---|
| today | 43–71 min | 43–71 min |
| QE (a) parallel parts, full set | ~11 min (the longest part: distributions ~11, flagd-standard ~10) | ~11 min |
| QE (b) reduced set, serial | ~21 min | 43–71 min |
| QE (c) parallel parts, reduced on PRs | **~8–9 min** (images ~8, with the cold `buildx` builder) | ~11 min |

The parts are 6 jobs against the 20-job limit. Each needs ~30 s of setup and the `cyclonedx-gomod` install.

## 3. Tests

**Characterization first (PR C):**

| Pin | Test | Pins |
|---|---|---|
| C1 | `test_ci_modules.py` `TestTableCoversModules` | Every `ALL_GO_MOD_DIRS` directory, with its prune list, has a table entry. `test-modules`, `test-flagd` and `test-contrib` loop over `test-module` with today's packages and flags, pinned against a command list written by hand |
| C2 | `test_lint_covers_every_module` | `lint-modules` loops over `ALL_GO_MOD_DIRS`, as `make lint` does today |
| C3 | `test_workflows.py` `test_jobs`, tightened | The regex reads every job id (`builder-e2e` and `integration-test` were skipped). The list is recorded deliberately in each PR that adds a job |

**P1, `TestAffected*`:** each case is a list of changed paths against a fixture graph and the table.

| Case | Expected |
|---|---|
| `sync/sse/sse.go` | `sync/sse` and the always set |
| `sync/s3/s3.go` | `sync/s3`, `sync/builder`, `flagd-proxy`, `runtime` and its services, `flagd`, the `/distribution` examples, `test/distributions` (manifest edge), and the always set |
| `core/pkg/x.go` | `core`'s closure |
| `runtime/x_test.go` | `runtime`, `flagd` (the cycle) and their dependents |
| `builder/internal/generate/x.go` | `builder`, `test/distributions`, `flagd` and the always set |
| `test/inprocess-example/filegrpc/x.go` | `filegrpc` and `file` |
| `test/distributions/example/cmd/main.go` | that module and `test/distributions` |
| `sync/sse/go.mod`, `core/go.sum` | `all` |
| `Makefile`, `.github/workflows/build.yaml`, `testdata/go-packages.txt`, `test-harness` (the pointer), `test/contract-external.sh` | `all` |
| `sync/sse/README.md`, `.claude/plans/x.md` | empty |
| a file of a module that the head deleted | `all` |
| a new module directory that has no table entry | `changes` fails |
| no base, or an all-zero base | `all` |

- **The run against the real repository** asserts invariants only: every module is in the table, and `core`'s closure holds at least 40 modules (finding 9).
- **`TestChangeSetIsFirstParent`:** a fixture repository with a merge commit whose base moved.
- **`TestMissReport`:** a `make test` log with a `FAIL` outside the set is reported as a miss.

**P2:**
- `test_aggregate_test`:
  - `test` has `if: always()` and needs `changes` and `test-module`;
  - it is red on `failure` and `cancelled`, and on `skipped` when `all` is true;
  - it is green on `skipped` only for an empty set;
- `test_matrix_guarded`: the empty-set `if` and `max-parallel`.
- **Dry runs, with their job lists and wall clocks recorded against section 1:**
  - a PR that touches one leaf;
  - one that touches `core`;
  - one that touches the workflow;
  - one that bumps a `go.mod`;
  - one run beside a push to `main`.

**P3:** `test_repo_checks_job`: every check of today's `lint` job appears in `lint` or `repo-checks`, by command.

**P4:**
- `test_concurrency_group`;
- the new `paths-ignore` pin;
- `test_docs_check_workflow`;
- `TestNoEmbeddedMarkdown` (R7);
- two dry runs: a markdown-only PR (only markdownlint, `docs`, `docs-check` and `upgrade-guide` run), and a `.claude/`-only push (only markdownlint).

## 4. PR slicing

| PR | Model | Base | Content | Starts |
|---|---|---|---|---|
| P0 | implementer | `main` | The cache: one key per job, saved only on `main`; golangci-lint's binary and cache directory; the first-parent `BASE` in the `upgrade-test` and `release-dry-run` gates. Reports the cache's size and restore time, and O | now |
| C | implementer | `main` | C1–C3; `ci-modules.json`; `test-module` and `lint-modules`, with `test-modules`, `test-flagd` and `test-contrib` looping over `test-module`. `make test` and `make lint` run what they run today | now |
| P4 | implementer | `main` | QD: the `concurrency` group, the new `paths-ignore`, `docs-check.yaml` with `docs-check` and `upgrade-guide`, and the K2 pins | now |
| L | implementer | `main` | The spec lint: the three vendored aiup-core 2.28.0 scripts in their layout, `spec-lint.yaml`, `test_spec_lint_workflow` | now |
| R | implementer | `main` (if P0 merges first, its first-parent `BASE` line is merged in mechanically) | QE (c): `release-dry-run` as a matrix over `part`, with `DRY_RUN_PART` in `release-dry-run.sh`, the `scope` output, and the reduced set on PRs. `release-please.yaml`, `release-build.sh` and the Dockerfiles stay unchanged. Dry runs: a PR touching `builder/` (reduced, every distribution), one touching `distributions/flagd-sync/` (reduced, that one), and a `workflow_dispatch` (full, every check of today green). `test_release_dry_run_parts` pins the parts and that `full` covers today's archive and image lists | now |
| R2 | implementer | `main` | When the dry run runs: only on a push to `main`, `FULL_CI=true`, `workflow_dispatch`, or a pull request labelled `release-check` (with `labeled` added to the `pull_request` types). Never by default on a PR, and the input-path gate no longer selects PRs. On `main` it keeps R's full set; a labelled PR gets R's reduced scope. K2's `test_release_dry_run_runs_when_its_inputs_change` is updated deliberately | now |
| P1 | implementer | C | `ci-modules.py` (ownership, change set, edges, `extraEdges`, the `go.mod` rule) and the `TestAffected*` tests; the `changes` job in **report-only mode** with the miss report, beside today's `test` and `lint` | after C |
| P2 | implementer | P1, and P0 merged | the `test-module` matrix and the aggregate `test`; per-module coverage and `codecov.yml`; the restore-only cache for the matrix; K2's job list | after P1 has reported ten PR runs with no miss |
| P3 | implementer | P2 | `lint` over `lint-modules` and `repo-checks` (QB (a)); K2's job list | after P2 |
| S | mechanic | `main` (straight to `main`) | `CLAUDE.md` "Local runs and CI": the affected-module matrix, `repo-checks`, everything on `main`, the markdown-only run, and the spec lint now in CI (the Spec gate row and the implementer's "the one gate CI does not run" line) | after P3 |

**The critical path is C → P1 → (the ten reports) → P2 → P3.**
- P0, C, P4, L, R and R2 start now, in parallel (R2 edits R's gate step, so it merges after R or onto it). L touches no existing workflow; R touches only the `release-dry-run` job and its scripts.
- They touch different parts of `build.yaml`, so their merges into the stack are mechanical; `test_workflows.py` takes one new test per PR.
- P2 needs P0's cache on `main` before its matrix is measured.

## 5. Gates

| Gate | Command | PRs |
|---|---|---|
| Workflow pins | `python3 .github/scripts/test_workflows.py` and `test_ci_modules.py` (Windows or WSL) | all |
| Parity with `make test` and `make lint` | C1 and C2, in CI | C, P2, P3 |
| The workflow itself | CI on the PR, and the dry runs of section 3 | P0, P2, P3, P4 |
| Full run | `workflow_dispatch` on the branch, every module green | P2, P3 |
| No miss | P1's reports on ten PR runs | before P2 merges |
| Spec lint parity | the vendored script and the plugin's copy give the same summary line on `main`, recorded in the PR description | L |
| Spec lint in CI | a dry-run PR with a broken use case link (ERROR, red) and one with a weak word (WARN, green) | L |

No Go code changes, so no Go gate runs locally.

## 6. Risks

- **R1, a missed dependency makes a red module look green.**
  - **The readers outside their own module, and the rule that covers each:**
    - `test/distributions` (manifest edges, the `builder` edge, the nested `cmd` modules);
    - `test/release`, `test/upgrade`, `test/image` (always set);
    - `runtime/cmd`, `test/evaluator-testkit` (`test-harness`: the submodule flag, and a pointer change sets `all`);
    - `test/inprocess-example/file` (edge to `filegrpc`);
    - `runtime` reading `service/flag-sync/test-cert` (a `require` already) (finding 11).
  - **Backstops:**
    - any `go.mod` or `go.sum` change sets `all`;
    - a file outside every module sets `all`;
    - push to `main` runs everything;
    - P1's miss report runs before anything is skipped.
- **R2, the queue.** A full run is ~49 test entries. Against it: `max-parallel: 10`, the `concurrency` group, and QD's `paths-ignore`, which removes ~16 of every 38 push runs. P2 measures a run beside a push to `main`.
- **R3, the cache.** One key per job, saved only on `main`; the size is measured in P0 against the 10 GB limit per repository.
- **R4, the change set.** The first parent of the merge commit (Q7). A force-push whose `before` is gone gets a missing base, so `all`.
- **R5, the aggregate goes green on a skipped job.** It is green only for an empty set with `all` false. Pinned by `test_aggregate_test`.
- **R6, coverage noise.** Splitting `flagd-coverage.out` and carrying flags forward changes how Codecov groups coverage, once. The upload doesn't fail CI, as today.
- **R7, markdown that Go reads.** Only two readers exist today: the generated CLI reference and the upgrade guide, and `docs-check.yaml` covers both. A new `go:embed` of a `.md` file, or a new test reading one, would be a third. `TestNoEmbeddedMarkdown` in P4 fails on a `go:embed` pattern that matches `.md`; a test that reads markdown has to be added to `docs-check.yaml`, which R7 says.
- **R11 (with R2: a release-build break is found on `main`, after the merge, and fixed forward; release-please is disabled here). A reduced dry run on a labelled PR misses a broken non-Linux or arm64 archive** until the full set runs on `main`. A darwin or Windows build failure surfaces after the merge. Releases are disabled here, so nothing ships from it; `FULL_CI` or a manual run gives a PR the full set.
- **R10, the vendored spec lint drifts from the plugin.** The header records the version and the sha256. A local run with a newer plugin can disagree with CI until L's files are re-vendored; the CI copy is the gate. The sibling scripts are pinned to exist, so the lint can't silently lose the use-case validator.
- **R8, K2's job regex.** It already misses two jobs (C3); C fixes it.
- **R9, drop-in (C-003).** Nothing observable by operators or clients changes; only CI does.

## 7. Considered

- **Four fixed shards (revision 2's QA (a)):** the same wall clock, with no graph. Declined by the project lead in favour of the affected-module matrix of #176.
- **A compile gate before lint, the tests and integration (QC (b), ~10.5 min), and the project lead's own sequence (QC (a), ~19–22 min):** both declined for no gate (~8.7 min), with QA (b) in view. A compile error already fails every job within its own compile, and the `concurrency` group cancels superseded runs.
- **Markdown through the `changes` job instead of `paths-ignore`:** that would put an `if` on every out-of-scope job. `paths-ignore` plus `docs-check.yaml` leaves them untouched.
- **Path-filter actions** (`dorny/paths-filter`): a third-party action for what `ci-modules.py` and the gate pattern already do.
- **Package-level graph (`go list -deps -test`):** P1 uses it as an oracle on `main`, not as the rule.
- **Larger runners:** not available on Free.

## 8. Changelog

### Final decisions (project lead)

| Question | Decision | Change |
|---|---|---|
| QA | (b), not the recommendation | The affected-module matrix, with every fix of the opposer: the `go.mod`/`go.sum` rule, `extraEdges`, the derived submodule flag, path-keyed edges, report-only mode with the miss report, and green on an empty set only with `all` false. Section 2 rewritten; P1–P3 restored; per-module coverage with carryforward, as #176 asks |
| QB | (a), as recommended | One `lint` job over all modules, and `repo-checks` beside it |
| QC | (c), as recommended | No gate. The compile gate (b) was considered with QA (b) in view and declined (section 7) |
| QE (release dry run) | (c), as recommended | Slice R: `release-dry-run` as a matrix over six parts; a reduced scope on PRs; full on `main`, with `FULL_CI` and on `workflow_dispatch` |
| QF (release dry run) | (a), as recommended | `go build -a` stays; `release-build.sh`, `release-please.yaml` and both Dockerfiles stay unchanged |
| R2 (release dry run, later decision) | runs only on `main`, `FULL_CI`, a manual run or a `release-check` label | Slice R2. A PR no longer runs the dry run by default; a break is fixed forward on `main` |
| Spec lint in CI (later decision) | vendored, gating on errors | Slice L: aiup-core 2.28.0's `spec_lint.py` and its two sibling scripts, vendored in the plugin's layout; `spec-lint.yaml` on the spec files; fails on ERROR, not on WARN; K2 pin |
| QD | (a), plus markdown | The `concurrency` group and everything on `main`. `paths-ignore` grows to `**/*.md`, `docs/**` and `.claude/**`. A new `docs-check.yaml` runs `docs-check` and the upgrade-guide test on markdown and `docs/` changes, with `.claude/` excluded. K2 pins the new block and the workflow. All in P4 |

### Revision 2 (`step-15-opposer.md`; the project lead's ordering input)

| Finding | Severity | Outcome |
|---|---|---|
| 1 A `go.mod` change reaches every module through the workspace's MVS | blocker | Adopted: any `go.mod` or `go.sum` change sets `all` |
| 2 Edges the `require` graph lacks | major | Adopted. Verified `distributions_test.go:64` and `inprocess_test.go:275`; `extraEdges` in section 2 |
| 3 The saving doesn't need an affected set | major | Put to the project lead as QA; they chose (b). The first-parent gate fix is in P0 |
| 4 The queue | major | Adopted: the `concurrency` group, `max-parallel: 10`, QD's `paths-ignore`, a dry run beside a push |
| 5 P0's cache race, the cache size | major | Adopted: one key per job, saved only on `main`; golangci-lint's cache; the size and O measured in P0 |
| 6 `runtime` needs `test-harness` | minor | Adopted. Verified `runtime/cmd/loadconfig_test.go:18-20`; the submodule flag is derived by grep |
| 7 Module paths and the module set | minor | Adopted: edges keyed on paths, the prune list, inert `replace`s |
| 8 C1 can't read `make -n test` | minor | Adopted: the targets loop over `test-module`; C1 pins a list written by hand |
| 9 Report-only mode should measure misses | minor | Adopted: the miss report, the `go list` oracle, ten clean PR runs before P2 |
| 10 Green on an empty set with `all` | minor | Adopted (R5) |
| 11 R1's list | minor | Adopted (R1) |
| Ordering (project lead) | — | Became QC; decided (c) |
