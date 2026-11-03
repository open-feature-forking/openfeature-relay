# OpenFeature Relay (incubator)

Incubator for OpenFeature Relay: an OpenTelemetry Collector Builder-style runtime and builder for flag relays, with flagd as the reference implementation. This repository carries flagd's full history but is not a GitHub fork. `main` is upstream flagd plus this work: every slice lands on `main` through a pull request in this repository. Upstream changes come in by merging `upstream/main` into `main`.

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
- The reviewer reviews the pull request and posts its verdict as a PR review comment. `ready` needs CI green plus every gate CI does not run green in its own run (see "Local runs and CI" under Gates).
- Stack ahead instead of waiting for merges. When a slice builds on another, the implementer cuts it from the parent's branch as soon as that branch exists, and the orchestrating session opens it as a PR against the parent. Review runs on every PR of a stack at once, each against its parent's diff. Fixes land as new commits on the branch they belong to and are merged upward through the stack; the implementer of the top branch merges new parent heads in with a merge commit.
- On `ready` with green CI, the orchestrating session merges with a merge commit (keeping the signed-off commits) and deletes the branch. In a stack, retarget the next PR to `main` before merging the one below it: deleting a PR's base branch closes the PR instead of retargeting it. Confirm the new base with `gh pr view` before merging (the edit can fail silently), and close and reopen the retargeted PR so its build CI runs against `main`. `ready after fixes` goes back to the implementer as new commits on the same branch, never an amend.
- The project lead approves each plan before implementation, and can stop or veto any PR; they are not a required approver for merges inside this repository.
- The release-please and benchmark-pages workflows are disabled in this repository; do not enable them.
- During the proof of concept, CI runs `test`, `lint` (with the boundary and coverage checks), `integration-test`, `docs-check` and markdown linting. FIPS 140-3 and `docker-local` are gated, and so is the Pages deploy: set the repository variable `FULL_CI=true` (or run `build` manually) for a full run, and `PAGES=true` once GitHub Pages is enabled. Images are built only when a PR changes a Dockerfile or the build, and then in CI with `FULL_CI=true`.

## Phases

1. Remove tracing from the evaluator; replace `golang.org/x/exp` with the standard library; add `depguard` rules for the evaluator boundary (FR-040, NFR-005, NFR-012). Pilot for the loop.
2. Split the configuration format out of the evaluator's `SetState` (FR-019, FR-026, FR-029).
3. Factory interfaces, event bus, runtime registry (FR-010..FR-016, FR-020, FR-033).
4. Module split into component modules (NFR-004, NFR-006, C-004).
5. Builder CLI, manifest, code generation (FR-001..FR-009, FR-017, FR-018).
6. Standard distributions, images, CI (FR-021, FR-030..FR-032, NFR-003, NFR-009, NFR-011).
7. Contract tests, example extension, migration guide (FR-015, FR-043, NFR-010).

## Issues

Every use case has an issue titled `UC-0XX: <name>` (label `use-case`); the pinned roadmap issue lists them in PoC order. Every PR starts its description with `Part of #<issue>`. The orchestrating session keeps the use case issue's PR list and definition-of-done checkboxes current, adds the `in progress` label when planning starts, and closes the issue when the use case is done. Work outside the use cases (docs, public readiness) has its own issue.

The board [OpenFeature Relay PoC](https://github.com/orgs/open-feature-forking/projects/1) shows every use case issue, other work issue and open PR in a `Stage` column: Backlog, Planning, Needs your decision, Implementing, In review, Done. The orchestrating session moves the cards whenever the state changes, adds each new PR to the board, and puts anything waiting for the project lead under **Needs your decision**.

## PoC: one use case after the other

After phase 1 (C1, C2, D), work proceeds one use case at a time, in this order. Each runs the same loop and lands as one or more PRs against `main`; phases 2–7 above are covered by the use cases that need them.

| # | Use case | Brings in |
|---|----------|-----------|
| 1 | UC-018 Develop Configuration Format Extension | configuration format split out of `SetState`, document versions (phase 2) |
| 2 | UC-014 Embed Evaluator in In-Process Provider | evaluator and flagd configuration format as dependency-free modules |
| 3 | UC-007 Develop Evaluator Extension | untyped bulk evaluator, typed adapter, evaluation router, engine modules |
| 4 | UC-003 Develop Sync Provider Extension | sync factory, fetching sync providers and the shared poller |
| 5 | UC-004 Develop Service Endpoint Extension | service factory, event bus, runtime registry (phase 3) |
| 6 | UC-005, UC-006, UC-020 | middleware, telemetry provider and event sink factories |
| 7 | UC-008 Run Contract Tests | contract tests for every component type, grown with 3–6 |
| 8 | UC-001 Build Custom Distribution | module split (phase 4), builder CLI and manifest (phase 5) |
| 9 | UC-009 Upgrade to flagd-full | flagd-full built by the builder, test-harness suites green |
| 10 | UC-011, UC-010, UC-017, UC-021 | components command, standard distributions, flagd-sync, event export |
| 11 | UC-019 Serve Vendor Flags | a Flagsmith engine module as the cross-vendor proof |
| 12 | UC-002, UC-012, UC-013, UC-015 | image, release, CI verification, migration guide |

UC-016 stays Deferred.

A use case is done when its main success scenario and every alternative flow have an automated test, the reviewer's verdict is `ready`, the PR is merged, and its `**Status:**` line moves to `Implemented` (to `Tested` once a test case under `docs/test_cases/` covers it). This is a proof of concept: upstream reviewability (C-006) is a preference, not a gate. When a later use case delivers part of it, the use case still moves to `Implemented` once its own scope is done, provided every such step and rule is listed as an accepted gap naming the use case that delivers it, and the requirements it shares move to `In Progress` so the spec lint shows no drift.

## Gates

On Windows, Go and Docker work only inside WSL Ubuntu. The system Go there is 1.22 and cannot resolve `go 1.27` on its own, so pin the toolchain. Run Go gates from the repository root; `wsl` starts in the translated Windows working directory:

```bash
wsl -d Ubuntu -- bash -lc 'export GOTOOLCHAIN=go1.27.3; make workspace-init && make test-core test-flagd'
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

### Local runs and CI

The machine is shared by several sessions, so gate runs collide on ports and on the golangci-lint lock. CI runs on every pushed PR and is the gate for what it covers: `make test` (every module with `-race`, the examples and the evaluator testkit), `make lint` with `depguard`, package coverage, module boundaries (the evaluator boundary included), the `golang.org/x/exp` greps, the integration suite, the docs check and, on pull requests, a comparison of the allocs/op of the library and the flagd endpoint benchmarks against the PR's base (it fails if the head's lowest value is above the base's highest over three runs and the benchmark is not on the `NOISY` list of `compare-allocs.py` (a few benchmarks observed to differ by one allocation on identical code, each with its reason); each run is 1000 iterations, `SetState` 100; ns/op is not compared). Do not repeat those locally.

- **Implementer:** run the tests of the packages you touched (`go test -race`), plus spec lint for spec changes, the one gate CI does not run (the build workflow ignores `docs/**`). Commit and report. The orchestrating session pushes, and CI is the full run.
- **Reviewer:** read the PR's CI result instead of re-running those gates. Run locally only what judgement needs: targeted tests, mutations, spec lint, and benchmarks for ns/op, which CI does not compare. A verdict may be `ready, pending CI`; the merge waits for green CI either way.
- **A full local run** is only for diagnosing a CI failure that does not reproduce in a targeted run.

## Containment

All work stays in this repository until the project lead says otherwise. No pushes, branches, pull requests, issues or comments anywhere except `open-feature-forking/openfeature-relay`: not to `open-feature/flagd`, not to `open-feature-forking/flagd`, not to go-sdk-contrib or any other repository. Other checkouts (go-sdk-contrib, flagd) are read-only references. "Upstream PR" in a plan means a branch here, shaped so it could go upstream later. The `upstream` remote has pushing disabled.

## Conventions

- flagd built from this repository is a drop-in replacement for today's flagd. Internals change freely; what operators and clients observe does not: CLI, configuration, ports, endpoints, payloads, headers, readiness, startup and exit behaviour, metrics, spans, shutdown. A plan that changes any of it raises a question for the project lead with "keep today's behaviour" as the recommended default, and new behaviour goes behind opt-in configuration. The changes accepted before this rule are tracked in #78 for the migration guide.
- Conventional commits, `git commit -s` signed off with the committer's own identity (DCO); no AI co-author trailers.
- Stage files by name; parallel sessions share checkouts.
- Code comments keep only facts a reader cannot derive; measurements and history go in the PR description.
- Upstream PRs never include `.claude/`, `CLAUDE.md` or the spec files under `docs/` (`vision.md`, `requirements.md`, `glossary.md`, `use_cases*`).
- The generated protobuf package layout stays as it is (C-005).
