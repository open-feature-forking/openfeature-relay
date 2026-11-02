# flagd-builder

Incubator for an OpenTelemetry Collector Builder-style modular flagd. This repository carries flagd's full history but is not a GitHub fork. `main` is upstream flagd plus this work: every slice lands on `main` through a pull request in this repository. Upstream changes come in by merging `upstream/main` into `main`.

The specification is the contract: `docs/vision.md`, `docs/requirements.md`, `docs/glossary.md`, `docs/use_cases.puml`, `docs/use_cases/`. Use glossary terms in code, commits and PR text. A behaviour the spec does not state is a question, not a decision.

## The loop

Each phase runs plan → oppose → implement → review, one role per agent in `.claude/agents/`:

| Role        | Model  | Writes code | Output                                         |
|-------------|--------|-------------|------------------------------------------------|
| planner     | opus   | no          | steps, characterization tests, gates, risks    |
| opposer     | opus   | no          | findings against the plan (blocker/major/minor) |
| implementer | sonnet | yes         | commits per step, real gate output             |
| mechanic    | haiku  | yes         | mechanical steps the plan marks `mechanic`     |
| reviewer    | opus   | no          | re-run gates, findings, verdict                |

- A plan with an open `blocker` from the opposer goes back to the planner.
- The implementer works on its own worktree, cut from `main` (or from the slice it builds on); it never pushes.
- The orchestrating session pushes the slice as `phase-<n>/pr-<x>-<topic>` and opens a pull request in this repository against `main` (or against the slice below it, stacked). Plans, reviews and spec changes go straight to `main`.
- The reviewer reviews the pull request: it re-runs the gates and posts its verdict as a PR review comment. `ready` needs every named gate green in its own run and CI green.
- On `ready` with green CI, the orchestrating session merges with a merge commit (keeping the signed-off commits) and deletes the branch. `ready after fixes` goes back to the implementer as new commits on the same branch, never an amend.
- Simon approves each plan before implementation, and can stop or veto any PR; he is not a required approver for merges inside this repository.
- The release-please and benchmark-pages workflows are disabled in this repository; do not enable them.

## Phases

1. Remove tracing from the evaluator; replace `golang.org/x/exp` with the standard library; add `depguard` rules for the evaluator boundary (FR-040, NFR-005, NFR-012). Pilot for the loop.
2. Split the configuration format out of the evaluator's `SetState` (FR-019, FR-026, FR-029).
3. Factory interfaces, event bus, runtime registry (FR-010..FR-016, FR-020, FR-033).
4. Module split into component modules (NFR-004, NFR-006, C-004).
5. Builder CLI, manifest, code generation (FR-001..FR-009, FR-017, FR-018).
6. Standard distributions, images, CI (FR-021, FR-030..FR-032, NFR-003, NFR-009, NFR-011).
7. Contract tests, example extension, migration guide (FR-015, FR-043, NFR-010).

## Gates

Go and Docker work only inside WSL Ubuntu. The system Go there is 1.22 and cannot resolve `go 1.27` on its own, so pin the toolchain. Run Go gates from the repository root:

```bash
wsl -d Ubuntu -- bash -lc 'export GOTOOLCHAIN=go1.27.3; cd /mnt/c/Users/simon/openfeature/flagd-builder && make workspace-init && make test-core test-flagd'
```

From Git Bash, prefix `wsl` calls with `MSYS_NO_PATHCONV=1`. The spec gate is Python and runs on Windows directly.

In a worktree under `.claude/worktrees/`, export `GOWORK=$PWD/go.work` before `make workspace-init`, or `go work init` finds the main checkout's `go.work` and fails. `make lint` needs `export GOPATH=$(go env GOPATH)`, which the WSL login shell does not set.

| Gate            | Command (inside WSL, repo root)                                                         | Requirement     |
|-----------------|-----------------------------------------------------------------------------------------|-----------------|
| Unit tests      | `make test-core test-flagd test-flagd-proxy`                                            | all             |
| Lint            | `make lint` (includes `depguard`)                                                       | NFR-005, NFR-006 |
| Boundary        | `cd core && go list -deps ./pkg/evaluator \| grep -E 'opentelemetry\|grpc\|connectrpc\|k8s.io\|gocloud'` must print nothing | NFR-005 |
| Benchmarks      | `make flagd-benchmark-test`, compare allocs/op with the branch point                    | NFR-001         |
| Acceptance      | `make flagd-integration-test` (builds flagd from source, runs the test-harness Gherkin suites in Docker; slow) | NFR-007, C-010 |
| Dependencies    | `grep -rln 'golang.org/x/exp' --include=*.go .` and `grep -rn 'golang.org/x/exp' --include=go.mod . \| grep -v indirect` must both print nothing | NFR-012 |
| Spec (Windows)  | `python ~/.claude/plugins/cache/ai-unified-process-marketplace/aiup-core/*/skills/spec-review/scripts/spec_lint.py --docs docs` | spec changes |

A red gate is reported with its output. If a gate fails on untouched code too, say so; it is the environment, not the change.

## Containment

All work stays in this repository until Simon says otherwise. No pushes, branches, pull requests, issues or comments anywhere except `open-feature-forking/flagd-builder`: not to `open-feature/flagd`, not to `open-feature-forking/flagd`, not to go-sdk-contrib or any other repository. Other checkouts (go-sdk-contrib, flagd) are read-only references. "Upstream PR" in a plan means a branch here, shaped so it could go upstream later. The `upstream` remote has pushing disabled.

## Conventions

- Conventional commits, `git commit -s` signed off as `simon.schrottner@flagsmith.com`; no AI co-author trailers.
- Stage files by name; parallel sessions share checkouts.
- Code comments keep only facts a reader cannot derive; measurements and history go in the PR description.
- Upstream PRs never include `.claude/`, `CLAUDE.md` or the spec files under `docs/` (`vision.md`, `requirements.md`, `glossary.md`, `use_cases*`).
- The generated protobuf package layout stays as it is (C-005).
