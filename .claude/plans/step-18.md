# Step 18 plan: move the CI helpers from Python to Go (issue #253)

**Status: revision 2.** It answers `step-18-opposer.md` (0 blockers, 7 major, 9 minor). Base: `main` @ 444f4a20.
Step 17 (#250 builds, #251 publish, stacked) is still open and adds Python of its own (section 1). Q1–Q8 wait for the
project lead (section 0).

## Revision 2: what changed

| Finding | Resolution |
|---------|------------|
| M1 nothing reads the matrix | New **Q8**. The recommendation is (b): port the flags and the table check, and drop `test`/`all`/`lint-all`. Under (b), slice A has no `x/mod`, no graph, no BFS goldens and no `json.dumps` row, and `modules changes` is stdlib-only. R2 now names the base selection and `FLAG_PATTERNS` as what can skip a job |
| M2 the fallback cannot work as written | Section 2, "Exit codes and fail-open": an exit-code contract (0 decided or fell open, 1 table error, anything else a build or startup failure). Every error except the table error falls open inside `relay-ci`, panics included, with today's exact fallback lines. The workflow gives the toolchain steps `continue-on-error: true`, and the run step writes the fallback when it has no binary. K2 pins the shape |
| M3 `changes` costs 2 s, not 5 | The binary cache is now the default (section 2, "Getting the binary"). A hit skips `setup-go` and the build. A miss runs `setup-go` with `cache: false`. New budget: hit ≤ +5 s, miss ≤ +30 s. A miss happens only when `tools/ci/**` changes. A measures 3 hits and 3 misses; X1 measures live |
| M4 `tools/ci` trips the `.go` and `go.mod` terms | S0 adds an exclusion list per flag (`^tools/ci/`) to `ci-modules.py`. It is needed because RE2 has no lookahead, and the Python moves first so the slice PRs do not pay for the full set. Per-package terms come back. The same exclusion goes into step 17's `publish-gate` pattern, carried by whichever of S0 and #251 lands second. A K2 row per flag |
| M5 missing translation hazards | Rows added: K2's splitter rewritten without lookahead or `\Z`; one `textio.ReadText` doing universal-newline translation for every read; flags kept as an ordered slice; `bufio.Reader` instead of `Scanner`, with a 1 MiB line row; ASCII `\s\d\S` as a stated decision; `$` before a final `\n`; `splitlines` breaks |
| M6 parity against a depth-1 checkout | The real-range parity is a **local** gate (WSL, full clone), and its output is attached to A's PR. CI runs parity only on fixtures and temp repositories. Ranges are fed as `pull_request`. New rows: a push to `main` with a usable `before`, with an empty `before` and with an unknown `before`; a PR with no `^1`. The two base functions stay separate under Q8 (a) and (c) |
| M7 no live comparison; "Same decision" is not a gate | X splits in two. **X1** runs Go as a shadow while the Python still decides: `changes`, `allocs` and `pass-sets`, diffed in the same step, with a `::warning` on any difference. The **shadow-clean gate** (20 PRs or 7 days without a difference) holds **X2**, which switches the jobs to Go and deletes the Python. A's parity compares the flag lines byte for byte |
| m1 `make -n` under `make test` | The Go test clears `MAKEFLAGS`, `MFLAGS` and `MAKELEVEL` for the child make |
| m2 coverage profile | Stated: `tools-ci-coverage.out` joins the `coverage-profiles` artifact through `PLAIN_TEST_MODULES`. Accepted |
| m3 Q3 against "every case" | The six dropped cases of `test_ci_modules_affected.py` are listed under A |
| m4 `always: true` | Dropped. The entry is a plain test command |
| m5 `modfile` is stricter | Moot under Q8 (b). Under (a) or (c): `go mod edit -json` instead of `x/mod`, with a malformed-`require` row |
| m6 local entry points | `RELAY_CI` in the scripts, defaulting to `make -s ci-tool` into the gitignored `.relay-ci/` |
| m7 the summary depends on what failed | Accepted: when `tools/ci` itself does not compile, `make test` already prints the compile error, and the failed-test summary is skipped |
| m8 over-engineered | Adopted. Parity only for A and P; B and C rely on their ported rows. `main.go` is a `switch`, with no `init()` registry |
| m9 inventory gaps | X2's Touches column names every Python lint step, `test_publish_images.py`, and the merge with #250's `testbed-suites.sh`/`test_ci_modules.py` edits |

**Goal.** The Python CI helpers under `.github/scripts/` become one small Go module, `tools/ci`, tested with `go test`.
One language and one test runner for the repository's own tooling. Each port preserves behaviour: same inputs, same
exit codes, same `GITHUB_OUTPUT` lines, same step summary and annotations, byte for byte where CI or a reader consumes
them. Q3 and Q8 may drop outputs that nothing reads; each such drop is named in a PR.

**Out of scope:**
- the vendored spec lint, `.github/scripts/aiup/` (and `spec-lint.yaml`), stays Python;
- the docs site build (`docs.yaml`, `requirements.txt`, mkdocs) stays Python;
- the shell scripts stay shell (`release-*.sh`, `run-benchmarks.sh`, `testbed-suites.sh`, `render-published-manifest.sh`,
  and step 17's `image-*.sh`, `publish-gate.sh`, `publish-images.sh`). Only the Python they call or embed moves;
- no change to which jobs a PR runs or what a job reports, apart from M4's exclusion of `tools/ci` itself.

## 1. Inventory

| File | Lines | Called by | Tests (lines) | Notes |
|---|---|---|---|---|
| `ci-modules.py` | 459 | `build.yaml` `changes` (every run); `test_workflows.py` imports `FLAG_PATTERNS` | `test_ci_modules_affected.py` (415, temp git repos), `test_ci_modules.py` (258, `make -n` against the Makefile) | Only the **flags** gate a job (`build.yaml:154`, `:238`, `:332`, `:373`, `:463`). No job reads `test`, `all` or `lint-all`. The graph decides nothing except the table error. `miss-report` and `--oracle` are called nowhere |
| `ci-modules.json` | 230 | `ci-modules.py`, `test_ci_modules.py`, a Makefile comment | – | data |
| `compare-allocs.py` | 168 | `benchmark` (from `head/`) | `test_compare_allocs.py` (131) + `testdata/bench-*.txt` | `NOISY` with reasons; named in CLAUDE.md |
| `compare-pass-sets.py` | 65 | `testbed-suites.sh` (testbed jobs, `make testbed-suites`) | `test_compare_pass_sets.py` (60) | `go test -json` |
| `test-failures.py` | 119 | `test`, on failure | `test_test_failures.py` (120) | report-only, exit 0 |
| `test_workflows.py` (K2) | 588 | `lint`, after `pip install pyyaml` | itself | regexps over workflow text, including a lookahead job splitter (`:55`, `:285`); PyYAML in 2 places; also K3 and `NoEmbeddedMarkdown`; SHA-256 pins with LF normalisation |
| lint steps | – | `build.yaml:110-124` | – | run the 6 Python test files; #251 adds a 7th (`test_publish_images.py`) |
| inline heredocs | ~20 | `release-dry-run.sh:128`, `release-dry-run-distributions.sh:123` | – | #250 replaces the second with `oci-layout.py` |
| **#250** `oci-layout.py` | 163 | `image-lib.sh`, `image-testbed.sh` (found by `dirname "${BASH_SOURCE[0]}"`) | via K2 and the dry run | `check`, `marking`, `load` |
| **#250** `test_publish_gate.py` | 47 | `lint` | – | a Python test of a shell script |
| **#250** `testbed-suites.sh`, `test_ci_modules.py` | – | – | – | small edits S0 and X2 merge with |
| **#251** `publish-images.py` | 286 | `publish-images.sh` → `publish-images`, `move-main` | `test_publish_images.py` (322) | stdlib registry client; `registry:2` rehearsal |
| **#251** `publish-gate` pattern | – | `build.yaml:435` on that branch | – | `\.go$` and `^\.github/scripts/(image-\|oci-layout\|publish-\|release-)` |

Every helper is stdlib-only except K2 (PyYAML). `go.work` is generated from `ALL_GO_MOD_DIRS` and gitignored, so a new
module under `tools/` joins it with no edit.

**Measured (run 42126854566):** `changes` 2 s with no Go. `setup-go` with `cache: false` 8 s (`lint`) and 11 s (`test`).
`setup-go` with its cache 10 s, plus a 7 s post step. Restoring the shared Go caches 42–44 s.

## 2. The tools module

- **Location:** `tools/ci`, module `github.com/open-feature-forking/openfeature-relay/tools/ci`, `go 1.27`.
- **One binary, `relay-ci`** (Q1). `main.go` dispatches with a `switch` on the first argument:

  | Subcommand | Replaces |
  |---|---|
  | `relay-ci modules changes …` | `ci-modules.py changes` (same flags) |
  | `relay-ci allocs <base> <head>` | `compare-allocs.py` |
  | `relay-ci pass-sets <a.json> <b.json>` | `compare-pass-sets.py` |
  | `relay-ci test-failures --log … --job … --summary …` | `test-failures.py` |
  | `relay-ci oci check\|marking\|load\|platforms …` | `oci-layout.py` and both heredocs |
  | `relay-ci publish push\|move-main …` | `publish-images.py` |

- **Layout:**
  - `internal/<name>/` holds the logic and its table tests, one package per helper;
  - `internal/textio` (`ReadText`, line reading) and `internal/ghactions` (output and summary appends, `::error` escaping)
    are written once in S0;
  - K2 is the test-only package `tools/ci/workflows/`.

  Each slice adds one `case` to `main.go`; the conflict is a line.
- **Dependencies:**
  - under Q8 (b), **only `go.yaml.in/yaml/v3` v3.0.5**, added by K. `modules changes` is stdlib-only;
  - under Q8 (a) or (c), `go mod edit -json` instead of `golang.org/x/mod` (m5), so still no second dependency.

  The yaml version is the one already in the workspace's build list. S0 adds a test that `tools/ci` requires no module
  of this repository.
- **Registration:**
  - `ci-modules.json` gets `"tools/ci": {"test": "make test-module MODULE=tools/ci", "coverage": true}`, with no
    `always` (m4);
  - in the Makefile, `tools/ci` joins `PLAIN_TEST_MODULES`. `make test` runs it, and its profile joins the
    `coverage-profiles` artifact (m2). `make lint` reaches it through `ALL_GO_MOD_DIRS`;
  - a `ci-tool` target builds `.relay-ci/relay-ci` (gitignored);
  - not in `check-package-coverage`'s lists, `GO_LIB_MODULE_DIRS`, the boundary tables or any distribution.

### Getting the binary (Q2; M3)

One composite-free pattern, the same in every job that runs `relay-ci`, pinned by K2:

1. `actions/cache/restore` of `$RUNNER_TEMP/relay-ci`, key `relay-ci-${{ runner.os }}-${{ runner.arch }}-${{ hashFiles('tools/ci/**') }}`, `id: relay-ci-cache`, `continue-on-error: true`.
2. On a miss, `setup-go` with `go-version-file: tools/ci/go.mod`, `cache: false` (no post step), `continue-on-error: true`.
   Jobs that already run `setup-go` skip this step.
3. On a miss, `CGO_ENABLED=0 GOWORK=off go build -C tools/ci -trimpath -o "$RUNNER_TEMP/relay-ci" .`, then
   `actions/cache/save` with the same key. Saving happens on every ref, `main` and PR branches alike: the binary is
   ~5 MB, so the convention that the shared Go caches are saved only on `main` does not apply. K2 states the exception.

`GOTOOLCHAIN: local` (`build.yaml:38`) stays. `go-version-file: tools/ci/go.mod` makes the installed version satisfy
the `go` line.

**Budget:** a hit costs ≤ +5 s over today's 2 s (one cache restore). A miss costs ≤ +30 s (setup-go ~10 s plus a cold
stdlib compile). A miss happens only on the first run after a `tools/ci/**` change, per ref scope. A measures 3 hits
and 3 misses on a scratch branch; X1 reports the live median over its shadow window. If a hit goes over +5 s, the
lead hears about it before X2.

`benchmark` builds from `head/tools/ci`, with the key on `head/tools/ci/**`.

### Exit codes and fail-open (M2)

| Exit | Meaning | Workflow |
|---|---|---|
| 0 | decided, or fell open (`::warning`) | outputs used |
| 1 | a module directory without a `ci-modules.json` entry | `changes` fails, as today |
| other or none | no binary, a crash before main, a killed process | the run step writes the fallback and exits 0 |

- **Inside `relay-ci modules changes`:** every error except the table error goes to the fail-open path. That covers a
  missing `ALL_GO_MOD_DIRS` line, broken JSON, a failed `git`, an unreadable file and a recovered panic. The path writes
  exactly today's fallback: `all=true` plus the flag lines, no `test=` and no `lint-all=`, and the summary line naming
  the tool. Under Q8 (b) it writes the flag lines only. One row per error source.
- **In the workflow:** the toolchain steps are `continue-on-error: true`. The run step:

  ```bash
  rc=0; "$RUNNER_TEMP/relay-ci" modules changes … || rc=$?
  [ "$rc" -eq 1 ] && exit 1
  [ "$rc" -ne 0 ] && { echo "::warning::relay-ci unavailable (exit $rc); every gated job runs"; printf '%s=true\n' go deps … >>"$GITHUB_OUTPUT"; }
  exit 0
  ```

  A missing binary gives 127 and falls open. K2 pins the flag list in the fallback against `modules.Flags`.

## 3. The port, per helper

**Characterization first.** Each slice's first commit adds Go table tests carrying every case of the Python test file
(one row per `test_*` method or `subTest`), red against a stub. Cases dropped by Q3 or Q8 are listed by name in the
slice.

**Parity (Q5; M6, m8).** Parity is kept only where a port decides something hard to re-derive: A and P.
- **In CI** (the `test` job, depth 1): parity on fixtures and temp git repositories only. The test runs the Python and
  the Go and compares stdout, the files written and the exit code. It skips without `python3`, and fails on a skip under
  `GITHUB_ACTIONS=true`.
- **Local gate for A** (WSL, full clone): 30 first-parent ranges of `main` from the last two weeks, fed as
  `pull_request` (sha = merge, base = `^1`), including `go.mod`-only, markdown-only, workflow-only and deleted-module
  changes. Both implementations run on the same working tree, so nothing is a recorded golden that drifts. The flag
  lines are compared **byte for byte**. The output is attached to A's PR.
- **P:** parity on step 17's layouts from a dry-run artifact, plus the `registry:2` rehearsal with the Go binary.
- B and C: no parity tests. Their ported rows come from complete Python test files.

**Translation hazards** (M5), each with a row or a stated decision:

| Hazard | Where | Rule |
|---|---|---|
| Lookahead and `\Z` in K2's job splitter (`test_workflows.py:55`, `:285`) | K | a line scanner: a job starts at `^  [a-z][a-z0-9-]*:` and ends at the next one or EOF. Rows: the last job, a job with a trailing comment, a job name that is a prefix of another |
| Universal newlines: Python turns `\r\n` and lone `\r` into `\n`; `os.ReadFile` does not. Files are CRLF on a Windows checkout (`core.autocrlf`, `.gitattributes` covers few paths) | every read | `textio.ReadText` translates `\r\n` and then `\r` to `\n`, and every read goes through it. A CRLF row per helper, beside the SHA pins' own normalisation |
| Dict order is output order | A's flags (`GITHUB_OUTPUT`, summary) | an ordered slice `Flags []Flag{Name, Include, Exclude}`; a golden of the line order |
| `bufio.Scanner` stops at 64 KiB | B `pass-sets` (`-json` `Output`), C | `textio` lines through `bufio.Reader`; a 1 MiB line row |
| `\s`, `\d`, `\S` are Unicode in Python, ASCII in RE2 | A, B, C | decision: ASCII. The inputs are `go test`/`git` output and repository paths. One row with a non-ASCII digit documents the difference |
| `$` without `re.M` also matches before a final `\n` | patterns anchored with `$` | inputs are split into lines first; a row with a trailing newline |
| `splitlines()` also breaks on `\x0b`, `\x0c`, `\x1c-\x1e`, `\x85`, U+2028/9 | C | decision: break on `\n` after `ReadText`. A row with `\x0c` in test output documents it |
| `fnmatch` `*` matches `/`; `path.Match` does not | A, the prune list | a small `fnmatch` equivalent; a row with a deeper path |
| Float formatting | B's summary | goldens from the Python output |
| Python catches every `Exception` | A | section 2's exit codes |
| `errors="replace"` decoding | A, C | `strings.ToValidUTF8`; an invalid-UTF-8 row |

**Per helper:**

- **A, `modules`.**
  - **Under Q8 (b):**
    - the prune list from `ALL_GO_MOD_DIRS`, the module directories and the table check (exit 1);
    - `flag_change_set`'s base selection: `pull_request` = merge against `^1`; `push` = `before..after`, with an empty,
      zero or unknown `before` setting every flag; anything else, or `FULL_CI`, sets every flag;
    - the flags with S0's exclusions;
    - the summary's "Jobs this change calls for" section.

    No graph, no `x/mod`.
  - **Under Q8 (a) or (c):** also `change_set`, which stays a separate function from `flag_change_set`. On a push to
    `main`, `change_set` sets everything while the flags still come from the diff. Plus the graph, `EXTRA_EDGES`, the
    manifests, the BFS in sorted order and the `test=` JSON (key order, `SetEscapeHTML(false)`).
  - **Base rows:**
    - a push to `main` with a usable `before`: flags from the diff (and `all=true` under (a));
    - a push with an empty `before` and one with an unknown `before`: every flag;
    - a PR whose merge has no `^1`;
    - `workflow_dispatch`, `schedule`, `FULL_CI=true`.
  - **`test_ci_modules.py`** becomes `internal/modules/makefile_test.go`. Its child make runs with `MAKEFLAGS`, `MFLAGS`
    and `MAKELEVEL` cleared (m1).
  - **Dropped by Q3** (`test_ci_modules_affected.py:288-331`):
    - `test_failing_packages_are_read_from_the_fail_lines`;
    - `test_a_failure_outside_the_set_is_a_miss`;
    - `test_a_failure_inside_the_set_is_no_miss`;
    - `test_a_package_of_no_module_is_ignored`;
    - `test_the_cli_reports_and_does_not_fail`;
    - `test_a_dependency_the_requires_lack_is_reported`.

    Under Q8 (b), the `TestAffectedModules`, `TestGraph`, `TestChangeSetIsFirstParent` and `TestChangesCommand` cases
    are listed as dropped too, except `test_a_directory_without_a_table_entry_fails` and
    `test_every_module_is_in_the_table`.
- **B, `allocs` and `pass-sets`.** `NOISY` keeps every entry and its reason. It is used only for lookups; where Python
  prints in an order, Go sorts the same way. `bench-*.txt` move to `tools/ci/internal/allocs/testdata/` in X2.
- **C, `test-failures`.** The state machine ports line by line. Escaping goes through `internal/ghactions`. Exit 0
  always.
- **K, K2, K3 and `NoEmbeddedMarkdown`** → `tools/ci/workflows`, run by `make test`.
  - The text checks stay text checks, over `textio.ReadText`.
  - The YAML checks use `go.yaml.in/yaml/v3`: `on` is a string key there and `True` in PyYAML (a row pins it).
  - The SHA-256 pins carry over unchanged.
  - Flag rows read `modules.Flags`, one per flag's include and exclude (M4).
  - `test_publish_gate.py` becomes a Go test that runs `publish-gate.sh` with `bash`.
  - The `pip install pyyaml` step goes.
- **P, `oci` and `publish`.** A stdlib port (`archive/tar`, `net/http`, `crypto/sha256`, allowed by depguard's
  `fips-140-3` rule), with no registry library (Q6). The same allowlist of 7 destinations is checked before the first
  byte, plus the same Basic login, the localhost-only `REGISTRY_HOST`, push by digest, and `move-main`'s ancestor
  check and `--force`. `oci platforms` replaces the `release-dry-run.sh` heredoc.
- **Local entry points (m6).** `testbed-suites.sh`, `image-lib.sh`, `image-testbed.sh` and `publish-images.sh` call
  `"${RELAY_CI:-$(make -s ci-tool-path)}"`. The `ci-tool-path` target builds `.relay-ci/relay-ci` if needed and prints
  its path. CI sets `RELAY_CI=$RUNNER_TEMP/relay-ci`.

## 4. The flag patterns (M4)

Applied in S0, to the **Python** `ci-modules.py`, so the slice PRs already run what their change calls for:
- each flag gets an exclusion: a path the include matches does not count if it matches `^tools/ci/`. Python and Go
  implement it the same way (include and not exclude), because RE2 has no lookahead;
- terms that come back, per package:
  - `benchmark`: `^tools/ci/internal/allocs/` and `^\.github/scripts/run-benchmarks`;
  - `testbed`: `^tools/ci/internal/passsets/` and `^\.github/scripts/testbed`;
  - the old `.github/scripts/compare-*` terms stay until X2 deletes those files;
- `tools/ci/go.mod`/`go.sum` no longer set `deps`, `image`, `upgrade` or `testbed`;
- **step 17's `publish-gate` pattern** gets the same: `grep -Ev '^tools/ci/'` before its `grep -E`, plus
  `^tools/ci/internal/(oci|publish)/` as a term. Whichever of S0 and #251 lands second carries it; X2 drops the
  `^\.github/scripts/(oci-layout|publish-)` part once the Python is gone.

K2 gains a row per flag: a `tools/ci/internal/<other>/x.go` change sets nothing except the flag that names its package.

## 5. Slices

| Slice | Model | Base | Content | Touches | Starts |
|---|---|---|---|---|---|
| **S0** foundation | implementer | `main` | `tools/ci` skeleton: `go.mod` with no requirement, the `switch`, `textio`, `ghactions` and their rows, the no-relay-module test; `ci-modules.json` entry; Makefile (`PLAIN_TEST_MODULES`, `ci-tool`, `ci-tool-path`), `.gitignore` (`.relay-ci/`); **section 4's exclusions in `ci-modules.py`** with Python rows; `test_ci_modules.py` and `test_workflows.py` updated for both | `tools/ci/**`, `ci-modules.json`, `ci-modules.py`, `Makefile`, `.gitignore`, two Python tests | now |
| **A** modules | implementer | S0 (stacked) | `internal/modules`, its `case`, ported rows, CI parity on temp repos, the local 30-range parity (attached), 3+3 cache timings on a scratch branch (not merged). Scope per Q8 | `tools/ci/internal/modules/**`, `tools/ci/main.go` (one case) | after Q8, once S0 exists |
| **B** comparisons | implementer | S0 | `internal/allocs`, `internal/passsets`, cases, rows | own dirs, one case each | once S0 exists |
| **C** test failures | implementer | S0 | `internal/testfailures`, case, rows | own dir, one case | once S0 exists |
| **K** K2 in Go | implementer | A, **step 17 merged** | `tools/ci/workflows/**` with every case of `test_workflows.py` and `test_publish_gate.py` at that point, the yaml dependency; deletes both Python files and their lint steps (`pip install pyyaml`, `test_workflows.py`) in the same PR | `tools/ci/workflows/**`, `go.mod`/`go.sum`, `build.yaml` (2 lint steps), 2 Python files | after step 17 merges (or stacked on #251 once its review is quiet) |
| **P** publish | implementer | S0, **step 17 merged** | `internal/oci`, `internal/publish`, cases, ported `test_publish_images.py` rows, parity, the rehearsal | own dirs, two cases | after step 17 merges |
| **X1** shadow | implementer | A, B, C, K, P merged | the binary pattern of section 2 in `changes`, `benchmark` and the testbed jobs. **Python still decides.** `relay-ci` runs beside `ci-modules.py`, `compare-allocs.py` and `compare-pass-sets.py` on the same inputs; the step diffs the flag blocks (or the exit code and summary) and raises `::warning title=shadow::…` on any difference; the cache timings go into the summary | `build.yaml`, `testbed-suites.sh` | after K and P |
| **X2** cutover | implementer | X1 + the shadow-clean gate | `relay-ci` decides, with the exit-code fallback; every call site switched (`changes`, `benchmark`, `test`, `testbed-suites.sh`, `image-lib.sh`, `image-testbed.sh`, `publish-images.sh`, both heredocs, the `publish-gate` pattern's Python terms); deletes `ci-modules.py`, `compare-allocs.py`, `compare-pass-sets.py`, `test-failures.py`, `oci-layout.py`, `publish-images.py`, the 6 remaining Python tests (`test_ci_modules*.py`, `test_compare_*.py`, `test_test_failures.py`, `test_publish_images.py`), their lint steps (`build.yaml:113-124` and #251's), the parity tests; moves `bench-*.txt`; K2 for the new steps; CLAUDE.md (`FLAG_PATTERNS` in `ci-modules.py`, `NOISY` of `compare-allocs.py`) | `build.yaml`, scripts, CLAUDE.md, `tools/ci/**` | last |

- **Parallelism:** S0 is short and serial. Then **A, B, C** run in parallel (A after Q8), and **K, P** once step 17
  merges: never more than 4 at once. Only S0, K, X1 and X2 touch `build.yaml` or `ci-modules.py`, never at the same
  time.
- **`go.mod`/`go.sum`:** only K adds a requirement.
- **Waiting for step 17:** K, P, X1, X2. S0 merges with #250's small edits to `test_ci_modules.py`,
  `testbed-suites.sh` and the Makefile, whichever lands second (m9).
- **The shadow-clean gate:** X1 on `main` for **20 PRs or 7 days, whichever is later**, with no shadow warning on any
  run (PRs, pushes to `main`, the nightly). A warning is a bug fixed in Go on a new PR, and the window restarts.
- Each slice is one PR, `Part of #253`.

## 6. Gates

| Gate | Command (WSL, repo root, `GOTOOLCHAIN=go1.27.3`) | Slices |
|---|---|---|
| Unit and characterization | `GOWORK=off go test -race ./...` in `tools/ci`, and `make test-module MODULE=tools/ci` in the workspace | all |
| CRLF | the same tests on a Windows checkout with `core.autocrlf=true` (in WSL on `/mnt/c`) | S0, A, K |
| Parity in CI | fixtures and temp repositories; fails on a skip under `GITHUB_ACTIONS` | A, P |
| Parity on real ranges | local, full clone, 30 ranges as `pull_request`, flag lines byte for byte; output on the PR | A |
| Lint | `make lint` (CI) | all |
| Workspace unchanged | `go list -m all` from the workspace before and after: only `tools/ci` added | S0, K |
| Flag exclusions | K2 rows; a `tools/ci`-only PR (S0's own follow-up commit) sets only the flags it names | S0 |
| Binary cost | 3 hits ≤ +5 s, 3 misses ≤ +30 s (A, scratch branch); live median in X1's summaries | A, X1 |
| Fail-open | X2's PR: one commit with a broken `relay-ci` build and one with a `relay-ci` that exits 2. Every gated job runs and a `::warning` shows; a missing table entry still fails. Reverted before merge | X2 |
| **Shadow-clean** | section 5 | holds X2 |
| Testbed | `make testbed-suites` through `RELAY_CI` | X1, X2 |
| Publish rehearsal | `publish-images.sh` with the Go binary against `registry:2` | P, X2 |
| Release dry run | `release-check` label | X2 |
| Spec | `spec_lint.py --docs docs` (no spec change expected) | X2 |

## 7. Risks

- **R1, the gate gets slower.** `changes` goes from 2 s to ≤ 7 s on a cache hit, ≤ 32 s on a miss, and the miss is
  only after a `tools/ci` change. The cache restore itself is a network step; when it fails, the miss path runs. When
  setup-go fails too, the gate falls open.
- **R2, a silent change of what runs.** Only the flags gate a job. What can move them is the base selection and
  `FLAG_PATTERNS` (with S0's exclusions). Against it: the base rows, the byte-for-byte flag parity, the shadow window
  and the fail-open contract. Under Q8 (a) or (c), a different module set changes a summary, not a job.
- **R3, semantics drift.** Section 3's hazards, each with a row.
- **R4, step 17 churn.** K and P port files still in review. They start after the merge, or stack on #251 and re-port
  any later commit.
- **R5, two copies.** From A/B/C/P until X2, a fix to a Python helper must land in Go too. The CI parity (A, P) and the
  shadow (X1) catch a one-sided fix to a deciding helper. A one-sided fix to `test-failures` is caught only by review.
  K deletes the Python K2 in its own PR.
- **R6, the workspace.** `tools/ci`'s single requirement is already at that version in the build list. The workspace
  gate checks it.
- **R7, dependency surface.** One pinned module, in no distribution. No Dependabot `gomod` entry (Q7).
- **R8, `tools/ci` changes look like code changes to step 17's gate.** Section 4's exclusion. Until it lands on both
  sides, a `tools/ci` change on `main` republishes the images on the next nightly: harmless, and wasteful.

## 0. Questions for the project lead

- **Q1, the shape of the binary.**
  - (a) One `relay-ci` with subcommands.
  - (b) One `main` per helper.
  - **Recommended:** (a): one compile and one cache key.
- **Q2, how jobs get the binary.**
  - (a) A cached binary keyed on `tools/ci/**`, built on a miss in each job that needs it (section 2).
  - (b) `go run` at each call.
  - (c) Built in `changes` and passed on as an artifact.
  - **Recommended:** (a). A hit costs one cache restore, with no toolchain. (b) puts setup-go and a compile on the gate
    every run. (c) costs an artifact round trip and ties every job to `changes`.
- **Q3, `miss-report` and `--oracle`.** No workflow calls them.
  - (a) Drop them in X2, with the six cases listed in A.
  - (b) Port them.
  - **Recommended:** (a).
- **Q4, where `ci-modules.json` lives.**
  - (a) Stays in `.github/scripts/`.
  - (b) Moves to `tools/ci/`.
  - **Recommended:** (a). Every reader names the path.
- **Q5, comparing the two implementations.**
  - (a) Parity tests for A and P (fixtures in CI, real ranges locally for A), then X1's live shadow on `main` before X2.
  - (b) Parity tests only.
  - **Recommended:** (a). The shadow is the only check against live PRs and the nightly, and it holds X2 rather than
    following it.
- **Q6, the registry client.**
  - (a) A stdlib port.
  - (b) `go-containerregistry`.
  - **Recommended:** (a). It is a line-for-line port of reviewed code, with no large tree in the job that holds
    `packages: write`.
- **Q7, Dependabot for `tools/ci`.**
  - (a) None during the PoC.
  - (b) A weekly `gomod` entry.
  - **Recommended:** (a): one module, no runtime exposure.
- **Q8, the module matrix (`test`, `all`, `lint-all`).** No job reads it; only the flags gate jobs.
  - (a) Port it as it is.
  - (b) Port only the flags and the table check, and drop the matrix and the "Affected modules" summary, named in X2's
    PR.
  - (c) Keep the matrix as a summary only, computed after the flags are written, so it is off the gate's critical path.
  - **Recommended:** (b). It makes the gate stdlib-only, A much smaller and R2 narrower. #253's "`go list`/`x/mod`
    answer it directly" argued for a cheaper graph, but nothing consumes the graph. If #176's per-module jobs come
    back, the matrix is rebuilt then from `go list -m -json`, against a consumer that exists.

**Planner defaults (the lead can overrule):** the names `tools/ci` and `relay-ci`; `GOWORK=off` builds; `textio` and
`ghactions` as the only places for reads and workflow commands; flag names and every kept output line unchanged; the
shadow window of 20 PRs or 7 days.
