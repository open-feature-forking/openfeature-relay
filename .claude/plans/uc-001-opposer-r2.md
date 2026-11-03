# UC-001 opposer check, revision 2

Plan: `.claude/plans/uc-001.md` revision 2. Code: `origin/main`; `origin/uc-004/pr-3-runtime-endpoints` (#71, head 664e6ef) and the UC-004 stack up to `pr-6-contract-and-status`, read without checkout; this checkout's `.claude/worktrees/`.

**Result: all 10 findings of `uc-001-opposer.md` are resolved on paper. No new blocker; 3 new majors and 5 new minors.**
- N1 and N2 would make PR 5 or PR 7 fail as written.
- D1–D3 come from the drop-in rule (`CLAUDE.md` Conventions, origin/main:106).

## Revision 1 findings

| # | Severity | Status | Note |
|---|---|---|---|
| 1 | blocker | Resolved | Q15: plain `Config` maps, decoded by each factory; the runtime builds from `Components`; flagd's helpers move into `evaluator/flagd`; `Runtime.Evaluator` stays a core interface; the package row forbids the endpoint packages and both flagd modules; an OFREP-only manifest is tested. One precondition doesn't hold (N1) |
| 2 | major | Resolved | Q14's `local_root`, with paths rewritten relative to `output_path`. Its scan needs pruning (N2) |
| 3 | major | Resolved | The probe runs for the host, with the final build's other flags; `TestCrossBuildRunsProbeOnHost` |
| 4 | major | Resolved | One parent per PR, merge-ins named, nothing waits for a merge |
| 5 | minor | Resolved | Runtime release on the first line, manifest version on the new line |
| 6 | minor | Resolved | Versions from `debug.ReadBuildInfo()`; `TestInventoryVersionsFromBuildInfo` |
| 7 | minor | Resolved | Allowlist: `R/core`, `R/runtime`, `R/service/transport`, `R/sync/builder` |
| 8 | minor | Resolved | 5d and 6c are package rows |
| 9 | minor | Resolved | T4d is a precondition; 5c runs the sync metrics pin through `components.go` (see N4 on where that pin can import it from) |
| 10 | minor | Resolved | The gates follow `CLAUDE.md`'s "Local runs and CI" |

## New findings

### N1. major: PR 5's precondition "UC-004 3f is in the parents" doesn't hold

- **Evidence:** the typed runtime fields are still there at the top of UC-004's stack.
  - `runtime.go` keeps `SyncService flagsync.ISyncService`, `OfrepService ofrep.IOfrepService` and `EvaluationService service.IFlagEvaluationService` at #71's head (664e6ef, runtime.go:32-34, used at :77 and :118).
  - The same file on `uc-004/pr-4-ofrep-document-versions` and on `uc-004/pr-6-contract-and-status` still has 14 references to them.
- **Effect:** none of PR 5's parents removes these fields, so the runtime keeps importing `ofrep` and `flag-sync`, and 5d's package row fails for a reason that isn't PR 5's parent being "before".
- **Fix:** either UC-004 delivers 3f in a named branch that PR 5 merges in, or PR 5 removes the fields itself (a 5a step, with UC-004's endpoint pins as its check). Record which.

### N2. major: `local_root: ../..` finds the worktrees' copies of every relay module

- **Evidence:**
  - Q14 scans for "every `go.mod` under it whose module path starts with `R/`".
  - This checkout's `.claude/worktrees/` holds full repository copies, each with `core/go.mod` and every other relay module. Five are present right now (`docs-relay-rewrite-…`, `p1-pr-a-…`, `p1-pr-b-…`, `p1-pr-c1-…`, `rv-pr67b`).
  - The scan therefore emits two or more `replace` lines per module path, which `go mod` rejects. `TestBuildIgnoresWorkspace` with the flagd-full manifest, and 7a locally, fail whenever a worktree exists. In CI there are no worktrees, so CI stays green while local runs fail.
  - `builder/testdata/proxy` and other `testdata` directories can hold fake `R/` modules too.
- **Fix:** prune hidden directories (`.claude`, `.git`), `testdata` and `vendor` from the scan. Fail with both paths named when one module path is found twice. Add a test that has a duplicate module under `local_root`.

### N3. minor: settings at their defaults would hit absent components

- **Evidence:**
  - Q15's table maps every flagd-full flag to a component. Flags such as `--port`, `--sync-port` and `--management-port` always carry a default value.
  - An OFREP-only distribution started with no flags would still receive `flag-evaluation/port` and `flag-sync/port` settings.
  - Until UC-010 (gap: "settings of absent components stop startup"), the runtime has to drop these silently. UC-010 then has to stop only on *explicitly set* settings.
- **Fix:** let the table record whether a value was set explicitly (viper's `IsSet`). The runtime ignores settings of absent components until UC-010, and UC-010 stops on explicit ones only. Cover it in `TestEndpointCombinations` at runtime level: OFREP-only starts with flagd-full's defaults.

### N4. minor: flagd's `components.go` sits in package `main`, which tests elsewhere can't import

- **Evidence:**
  - `flagd/main.go` is `package main` at the module root (origin/main), and 5c adds `flagd/components.go` next to it.
  - 1d ("green against `components.go`") and 5c's sync metrics pin (in `flagd/pkg/runtime/syncmetrics_test`) would need to import it, and Go forbids importing a `main` package.
  - 7a then compares the generated file with it byte for byte, so generated and hand-written code have to sit in the same kind of package.
- **Fix:** put `components.go` in an importable package in both the hand-written and the generated module (for example `<module>/components`, imported by `main.go`). Or move those tests into the `flagd` module's root package, and say so.

### N5. minor: the version stamping targets move, and replaced builds lose today's `dev`

- **Evidence:**
  - `make build-flagd` stamps `-X main.version=dev -X main.commit=… -X main.date=…` (Makefile:51-52), and the three Dockerfiles stamp the same `main.*` symbols. Q10 stamps `runtime/cmd` variables, but 6c only "registers modules" in the Dockerfiles.
  - With `local_root` replaces, "the resolved `R/runtime` version" is `v0.0.0`, while 1a pins today's `dev` output.
- **Fix:**
  - 6a/6c move every `-X` target, in the Makefile and in all three Dockerfiles, to the new symbols.
  - A replaced runtime stamps `dev` (or the `-X` value given), so 1a stays unchanged for developer builds.
  - `TestVersionNamesDistribution` covers the replaced case.

## Drop-in check (CLAUDE.md Conventions: what operators and clients observe does not change)

### D1. major: `flagd version` changes its first line and gains a line

- **Plan step:** Q10, 6b, and the update to pin 1a ("one line added").
- **Evidence:** today `flagd version` prints `flagd: <Version> (<Commit>), built at: <Date>`, then the two FIPS lines (flagd/cmd/version.go:31-33). Released builds stamp:
  - `VERSION=$(git describe --tags --abbrev=0)`, flagd's own release tag;
  - `COMMIT=$(git rev-parse --short HEAD)`.

  (Makefile:46-56 and the Dockerfiles' `ARG`s.) Q10 changes three observable things:
  - `<Version>` becomes "the resolved `R/runtime` version", a different version series;
  - the revision becomes the full `git rev-parse HEAD`;
  - a `distribution: <name> <manifest version>` line is added before the FIPS lines.

  Scripts that parse the output, or compare the version with the image tag, see all three.
- **Fix:** per the rule, make this a question for the project lead, with "keep today's output" as the recommended default:
  - the first line keeps flagd-full's release version and the short commit;
  - nothing is added to the default output;
  - the distribution identity appears in `flagd components` (UC-011) and behind opt-in output (a `--verbose` flag or `-o json`).

  UC-009 step 8 and FR-034 ("`flagd version` names the distribution") conflict with the rule, so the spec needs an amendment or an accepted exception tracked in #78.

### D2. minor: the binary's name and the image's entrypoint must not follow `dist.name`

- **Plan step:** Q2 (`output_path`, `name: flagd-custom`), 4d, 7a; UC-009 releases from the generated source.
- **Evidence:** the plan never says what the built binary is called. If it follows `dist.name` (`flagd-full`), then:
  - flagd-full's release binary, its archive names, and the image's `/flagd-build` entrypoint (flagd/build.Dockerfile) change;
  - and so do the start commands that call `flagd start`.
- **Fix:** the binary's name is a manifest field with default `flagd`. The flagd-full manifest pins it, and 7a checks it.

### D3. minor: startup failure behaviour isn't pinned, although the start path is rebuilt

- **Plan step:** pins 1a–1e; PR 5 (Q15 settings path), PR 6 (`runtime/cmd.Execute`).
- **Evidence:**
  - The rule names "startup and exit behaviour".
  - 1b pins the flags, and 1e the effective endpoint settings. Nothing pins the process exit code and the stderr/log line for a failing start: an invalid `--sources` JSON, an unknown scheme, a port in use, a bad config file.
  - Yet Q15 reroutes every setting, and `Execute` replaces the command wiring.
- **Fix:** add a 1f table: for each startup failure, the exit code and the first error line. PRs 5 and 6 keep it unchanged.

### Verified against the rule

- **Flags, configuration and endpoints:** flags, config keys, environment variables and ports (1b), the effective endpoint settings (1e) and the module set (1c) are pinned.
- **Opt-in for custom distributions only:** Q6's "a source without a format fails startup" applies only to distributions without flagd's format, which flagd-full always selects.
- **No observable change:** removing the Go import paths under `flagd/pkg/...` (C-004, UC-015) changes nothing operators or clients see.
