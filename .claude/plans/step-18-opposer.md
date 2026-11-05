# Step 18 plan: opposer findings (revision 1)

Plan: `.claude/plans/step-18.md` (revision 1, written against `main` @ 3488de6b). Checked on `main` @ 3cfb0113.

Checked against the sources, not the plan's summary of them:
- on `main`: `ci-modules.py`, `ci-modules.json`, `compare-allocs.py`, `compare-pass-sets.py`, `test-failures.py`, every `test_*.py`, `testbed-suites.sh`, the two dry-run scripts, `build.yaml`, the `Makefile`, `.golangci.yml`, `.gitattributes`, `.gitignore`, every `go.mod`;
- step 17's branches `step-17/pr-b-builds` (#250) and `step-17/pr-p-publish` (#251): `build.yaml`, `image-lib.sh`, `image-testbed.sh`, `publish-gate.sh`, `publish-images.sh`, `dependabot.yml`;
- the step timings of run 42126854566 (`changes`, `lint`, `test`, `benchmark`).

Counts: **0 blocker, 7 major, 9 minor.**

## What holds (verified)

- **Dependencies.** `golang.org/x/mod v0.41.0` (14 `go.mod` lines) and `go.yaml.in/yaml/v3 v3.0.5` (45) are already in the workspace's build list. 38 modules declare `go 1.27`. `go.yaml.in/yaml/v3` is the maintained fork, a better pick than `gopkg.in/yaml.v3`. The `on:` gotcha is right: PyYAML reads it as `True`, yaml/v3 as the string key `on` when decoding into `any`.
- **Workspace and boundaries.**
  - `go.work` and `go.work.sum` are gitignored (`.gitignore:13-14`). `workspace-init` loops over `ALL_GO_MOD_DIRS` (`Makefile:39-41`), so `tools/ci` joins with no edit.
  - `check-package-coverage` loops over explicit lists (`:282`), and `check-module-boundary` reads `testdata/module-*boundaries.txt` (`:301-317`). `tools/ci` enters neither.
  - `make lint` reaches it through `ALL_GO_MOD_DIRS` (`:275`). The `fips-140-3` depguard rule allows `crypto/sha256`, which P needs.
- **`make test`.** A module in `PLAIN_TEST_MODULES` runs as `go test -race -covermode=atomic -cover -short -count=1 ./tools/ci/...` from the root (`:135-136`), inside the workspace.
- **Parallel slices.** A, B, C and P write disjoint directories. Only A and K touch `go.mod`/`go.sum`, and K is cut from A. Only K and X touch `build.yaml`.
- **Regexps of A, B and C.** No lookaround and no backreference in `ci-modules.py`, `compare-allocs.py`, `compare-pass-sets.py` or `test-failures.py`. K2 is the exception (major 5).
- **The inventory.** The heredocs are at `release-dry-run.sh:128` and `release-dry-run-distributions.sh:123`. The step 17 additions match #250 and #251.
- **Q4 (a), Q6 (a) and Q7 (a)** hold as recommended.

## Major

### 1. Nothing reads the module matrix: only the flags gate a job (section 2, A, R2)

- **Evidence:**
  - `changes` exports `test`, `all` and `lint-all` (`build.yaml:47-49`). No job in any workflow reads `needs.changes.outputs.test`, `.all` or `.lint-all`, on `main` or on #251. Every gated job reads a flag (`:154`, `:238`, `:332`, `:373`, `:463`). The plan itself says the `test` job ignores the matrix.
  - The flags depend only on the changed paths and `FLAG_PATTERNS` (`ci-modules.py:308-310`), with the base from `flag_change_set`. None of the graph feeds them: `parse_gomod`, `EXTRA_EDGES`, manifests, `affected`, `submodule_of` and the `test=` JSON.
  - The one gating effect of the graph is the `TableError` exit. C1's `test_every_module_has_an_entry` (`test_ci_modules.py:86`) already pins it in `lint`.
- **So:**
  - R2 ("a different module set skips a job") does not hold. A different module set changes a summary, not a job. The real R2 surface is the base selection plus `FLAG_PATTERNS`.
  - Most of A is spent on output that only a reader of the step summary sees: `x/mod`, the BFS goldens, the `json.dumps` hazard row, the 30-range matrix parity, `submodule_of`. That code is also the only reason `changes` downloads a module (major 3).
- **Fix:** a question for the lead before A starts.
  - **Q8:** (a) port the matrix as it is; (b) port only the flags and the table check, and drop `test`/`all`/`lint-all` with a line in X's PR, like Q3; (c) keep the matrix as a summary, computed outside the critical path.
  - (b) makes `modules changes` stdlib-only and A much smaller. #253's "`x/mod` answers it directly" argues for (a) or (c), so the lead decides. Either way, R2 should name the flags as what can skip a job.

### 2. The fail-open fallback cannot work as written (section 2, "The fail-open rule survives the build")

- **Evidence:**
  - "A setup-go failure runs a shell fallback in the same step" is not possible. `setup-go` is a `uses:` step of its own. When it fails, the next `run` step is skipped (the default is `if: success()`), `changes` fails and every gated job is skipped.
  - "A panic is recovered" covers too little. Python catches **every** `Exception` (`ci-modules.py:400`). That includes:
    - `StopIteration` from `parse_prune` when the Makefile line moves (`:57`);
    - `json.JSONDecodeError` from a broken `ci-modules.json` (`:356`);
    - `CalledProcessError` from `git diff`/`ls-tree` with `check=True` (`:77`, `:287`, `:304`);
    - a `go.mod` that cannot be read.

    In Go these are returned errors, not panics. A port that does `return err` exits 1, which the plan reserves for the table error, so `changes` goes red instead of falling open.
  - The fallback output is exact today: `all=true` plus the 8 flags, **no** `test=` and no `lint-all=` (`:403`), and a summary line that names the script (`:402`). The plan does not say whether Go reproduces it.
- **Fix:**
  - Every error except `TableError` goes to the fail-open path inside `relay-ci`, which exits 0. Panics are recovered as well.
  - The exit codes are a contract with a row each: 0 for decided or fallen open, 1 for the table error, anything else for a build or startup failure.
  - In the workflow, `setup-go` (or the cache step of major 3) gets `continue-on-error: true` and an `id`. The run step builds `relay-ci` and runs it. When the toolchain or the build is missing, it writes the fallback lines and exits 0 on anything except exit 1 from `relay-ci`. K2 pins this shape.

### 3. The `changes` cost: the measured numbers make +30 s a ceiling, and the plan leaves caching as a fallback (section 2, R1, Q2)

- **Evidence** (run 42126854566):
  - `changes` ran in **2 s**, from 23:05:25 to 23:05:27, not 5 s.
  - `setup-go` with `cache: false` took 8 s (`lint`) and 11 s (`test`).
  - `benchmark`'s `setup-go` with its default cache took 10 s, plus a **7 s `Post Setup go`** that saves the cache at the end of the job. In `changes` that post step sits on the critical path.
  - Restoring the shared Go caches took 42–44 s (`lint`, `test`). Reusing them in `changes` would blow the budget alone.
  - Go ships no compiled standard library, so a cold `GOCACHE` compiles `os/exec`, `encoding/json`, `regexp` and the rest. With `x/mod` there is also a download from the module proxy, a network dependency the gate does not have today.
  - Estimate: 2 s → about 25–35 s. That is at or past the budget, on every run.
  - A measures on a scratch branch: one cold run, which says nothing about the hit path.
- **Fix:**
  - Make the binary cache the default design, not R1's fallback. `actions/cache` keyed on `runner.os`, `runner.arch`, the Go version and `hashFiles('tools/ci/**')`, holding a `CGO_ENABLED=0` binary. On a hit, skip `setup-go` and the build: about +2 s.
  - On a miss, run `setup-go` with `cache: false` (no post step) and `go-version-file: tools/ci/go.mod`. The workflow sets `GOTOOLCHAIN: local` (`build.yaml:38`), so the installed version must satisfy the `go` line or the build fails.
  - A measures 3 hits and 3 misses.
  - Q2 (a) stays. The same cache key serves every job that builds `relay-ci`.

### 4. `tools/ci` changes trip the generic flag terms and step 17's publish gate (X, "no change to what a PR runs")

- **Evidence:**
  - Today an edit to a helper triggers at most its own job: `^\.github/scripts/(run-benchmarks|compare-allocs)` for `benchmark`, `(testbed|compare-pass)` for `testbed` (`ci-modules.py:37`, `:48`).
  - After the move every helper is `.go`, so `\.go$` matches it in `go`, `benchmark`, `upgrade` and `testbed`. A and K also change `tools/ci/go.mod`/`go.sum`, which sets `deps`, `image` (docker-local plus Trivy), `upgrade` and `testbed`.
  - So every slice PR, and every later edit of the tool, runs the benchmark, upgrade and testbed jobs. X's plan to move the explicit path terms to `tools/ci/internal/...` changes nothing, because `\.go$` already matches those paths.
  - #251's `publish-gate` job has its own pattern (`build.yaml:435` on that branch), with `\.go$` and `^\.github/scripts/(image-|oci-layout|publish-|release-)`:
    - after X, a `tools/ci`-only change on `main` makes the nightly republish all 7 images;
    - the explicit term goes stale.

    X's list of call sites does not name this pattern.
- **Fix:**
  - Exclude `^tools/ci/` from the generic `.go` and `go.(mod|sum)` terms, in `FLAG_PATTERNS` and in the publish gate. Give back the per-package terms: `tools/ci/internal/allocs` and `run-benchmarks.sh` for `benchmark`; `tools/ci/internal/passsets` for `testbed`; `tools/ci/internal/(oci|publish)` for the publish gate.
  - Add a K2 row for each.
  - Until X, the slices' PRs pay the full set. Say so, or land the pattern change in S0.

### 5. The table of translation hazards is missing entries that bite (section 3)

- **K2's job splitter cannot keep its regexp.** `test_workflows.py:55` uses a lookahead `(?=...)` and `\Z`, and `:285` uses `(?m)^(?=...)`. RE2 rejects both. Every job-scoped assertion of K2 reads the splitter's output, so the rewrite needs its own rows: the last job, a job with a trailing comment, a job name that only shares a prefix.
- **Python normalises line endings without being asked.**
  - `read_text()` and `open()` use universal newlines, so every helper and K2 see LF.
  - This checkout has `core.autocrlf=true`, and `build.yaml` is CRLF on disk. `.gitattributes` forces LF only on root `testdata/*.txt`, `*.golden`, `*.sh`, `flagd/go.mod` and `aiup/**`. `.github/scripts/testdata/bench-*.txt`, the workflows, the Makefile and the other 55 `go.mod` files are CRLF on a Windows checkout.
  - `os.ReadFile` keeps the `\r`, so K2's literal `"\n"` checks (`:77-89`) fail locally.
  - **Fix:** one `readText` in `internal/ghactions` (or a `textio` package) that normalises, used for every read, with a CRLF row per helper rather than only for the SHA pins.
- **Order.** `FLAG_PATTERNS` is a dict, and its insertion order is the order of the `GITHUB_OUTPUT` lines and of the summary. A Go map is random, so keep an ordered slice. The same applies to the `NOISY` table if its order is ever printed.
- **Line length.** `bufio.Scanner` stops at 64 KiB per line with `ErrTooLong`. `go test -json` `Output` events (pass-sets) and `test.log` lines (test-failures) can exceed that; Python has no limit. Use `bufio.Reader` or `Scanner.Buffer` with a big limit, with a row of a 1 MiB line.
- **Smaller semantics.**
  - Python `str` patterns treat `\s`, `\d` and `\S` as Unicode, RE2 as ASCII.
  - `$` without `re.M` also matches before a final `\n`.
  - `splitlines()` also splits on `\r`, `\x0b`, `\x0c`, `\x1c-\x1e`, `\x85`, U+2028 and U+2029. `test-failures.py` reads raw test output, where a lone `\r` (progress output) is plausible.

  Each gets a row or a stated decision.

### 6. A's parity over real ranges cannot run where the plan says it must (section 3, Q5, gates)

- **Evidence:**
  - The parity tests run in `make test`, so in the `test` job, which checks out at the default depth 1 (`build.yaml:186`). The 30 ranges' commits are not there. Under `GITHUB_ACTIONS=true` "CI fails on a skip", so A's PR goes red unless the test fetches history, which is network in a unit test.
  - The output is not a function of the range alone. The graph comes from walking the **working tree** (`list_files(root)`, `ci-modules.py:79-84`), so recorded goldens drift with every module added.
  - Main's merge commits fed as `push` to `refs/heads/main` give `all=true` without a diff (`:280-281`). They must be fed as `pull_request` (sha = merge, base = `^1`).
  - **The two base functions differ, and the plan has no row for it.** On a push to `main`, `change_set` returns "everything" but `flag_change_set` diffs `before..after` (`:291-305`). So `main` runs the gated jobs by flag, not all of them. A Go port that merges the two functions, the natural refactor, sets every flag on every `main` push, or the reverse.
- **Fix:**
  - The 30-range parity is a local gate (WSL, full clone), with its output attached to A's PR. CI runs parity only on fixtures and temp repositories.
  - Add rows for: a push to `main` with a usable `before` (all=true, flags from the diff); a push with an empty `before`; a push with an unknown `before`; a PR whose merge has no `^1`.

### 7. The cutover has no live comparison, and the "Same decision" gate is not one (Q5, X, gates)

- **Evidence:**
  - Q5 rejects shadow steps because they put every slice on `build.yaml`. That argument does not apply to X, which edits `build.yaml` anyway.
  - "Same decision" compares X's first 5 PRs after merge with summaries recorded for **other** ranges, and it runs after the merge. It cannot hold anything back.
  - Between K and X, K2 in Go pins the flag names and rows against `modules.FlagPatterns`, while the Python `FLAG_PATTERNS` is what gates CI. Only A's parity ties them, and only if it compares the flag lines.
- **Fix:**
  - X switches the gate to Go and keeps the Python as a shadow in the same `changes` step (`python3` is on the runner). The step diffs the two `GITHUB_OUTPUT` flag blocks and raises `::warning` on a difference, for a fixed period (a week, or 20 PRs).
  - A follow-up X2 deletes the Python. That costs about a second per run.
  - State that A's parity compares the flag lines byte for byte.
  - Drop "Same decision" as a gate.

## Minor

1. **`make -n` inside `make test`.** `test_ci_modules.py` runs `make -n` from the `lint` job, outside make. As a Go test under `make test-module MODULE=tools/ci`, the child make inherits `MAKEFLAGS`: the command-line `MODULE=tools/ci` override and the jobserver. Clear `MAKEFLAGS`, `MFLAGS` and `MAKELEVEL` in the test's environment.
2. **Coverage profile.** Through `PLAIN_TEST_MODULES`, `tools/ci` lands in `COVERAGE_MODULES` (`Makefile:128`), so `tools-ci-coverage.out` goes into the `coverage-profiles` artifact (`build.yaml:216-222`). Either say so or keep it out.
3. **Q3 against "every case".** With Q3 (a), six cases of `test_ci_modules_affected.py` have no Go counterpart: `:288-331`, the failed packages, the miss and no-miss cases, a package of no module, the CLI report, the oracle. List them as dropped so a reviewer can count the rest.
4. **`"always": true` for `tools/ci`** has no stated reason, and `test_ci_modules.py:99` pins the always set. A `tools/ci`-only change is now inside a module, so it no longer sets `all=true` the way a `.github/scripts/` change does today ("outside every module"). That is harmless while the matrix is unread (major 1), but say what `always` is for or drop it.
5. **`modfile` is stricter than `parse_gomod`.** `ParseLax` still validates versions, where `parse_gomod` takes the first token. A malformed `require` now falls open instead of giving a narrow set. Add a row. `go mod edit -json <file>` would give the same data with no dependency, if the lead picks Q8 (a) or (c) but wants a stdlib-only gate.
6. **Local entry points.** `make testbed-suites` (`Makefile:504`, `:506`) runs `testbed-suites.sh`, and `image-lib.sh` finds its helper next to itself (`oci_layout=$(dirname "${BASH_SOURCE[0]}")/oci-layout.py`). `$RUNNER_TEMP/relay-ci` does not exist locally. The scripts need a `RELAY_CI` variable with a default that builds the binary (`go run -C tools/ci .`, or `make ci-tool` into an ignored path).
7. **The failure summary depends on what failed.** `relay-ci test-failures` is built inside the failed `test` job. When the failure is a compile error in `tools/ci` itself, there is no summary; the Python never depended on it. Accept it in a line, or build the binary before `make test`.
8. **Over-engineered for a PoC:**
   - B's and C's parity tests duplicate rows already ported from complete Python test files, with goldens taken from the Python output. Keep parity for A, and for P, where the registry rehearsal is the real check.
   - The `init()` registry spares each slice one line in `main.go`, a conflict that resolves trivially. A `switch` is simpler. Keep the registry if preferred, but the "never edit the same file" argument is weak.
9. **Inventory gaps.**
   - `lint` also runs `test_compare_allocs.py`, `test_ci_modules.py`, `test_ci_modules_affected.py`, `test_compare_pass_sets.py` and `test_test_failures.py` (`build.yaml:113-124`). #251 adds `test_publish_images.py` (`:131` there). X deletes these steps, but its "Touches" column does not name them.
   - #250 also edits `testbed-suites.sh` and `test_ci_modules.py`, both of which S0 or X edit. That is a merge to expect, not a blocker.
