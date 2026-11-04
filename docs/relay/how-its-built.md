---
description: The specification and the agent loop that implements it
---

# How it's built

## The specification is the contract

The specification follows the AI Unified Process:

- [Vision](../vision.md): the problem, the idea, scope and success criteria.
- [Requirements](../requirements.md): functional requirements as user stories, non-functional requirements and constraints, each with an id.
- [Glossary](../glossary.md): the terms used in code, commits and pull requests, with the words to avoid.
- [Use cases](../use_cases/UC-001-build-custom-distribution.md): one per goal of a role, with main success scenario, alternative flows and business rules.
- [Research](../research/cross-vendor-evaluation.md): studies that informed the design.

A behaviour the specification does not state is a question, not a decision.

## The agent loop

The code is written by AI agents in a fixed loop, one role per agent, with the project lead approving each plan and able to stop any pull request:

| Role | Writes code | Does |
|------|-------------|------|
| planner | no | turns a use case into steps, characterization tests, gates and risks |
| opposer | no | attacks the plan before code is written; a blocker sends it back |
| implementer | yes | implements the approved steps on its own branch, with the real gate output |
| mechanic | yes | does the steps the plan marks as mechanical |
| reviewer | no | re-runs the gates and reviews the pull request |

Every slice lands on `main` as a pull request in this repository, and flagd's gates — unit tests, lint, the evaluator boundary, benchmarks and the flagd test harness — have to stay green.

The agent definitions are in [`.claude/agents/`](https://github.com/open-feature-forking/openfeature-relay/tree/main/.claude/agents), the plans and their reviews in [`.claude/plans/`](https://github.com/open-feature-forking/openfeature-relay/tree/main/.claude/plans), and the working agreement in [`CLAUDE.md`](https://github.com/open-feature-forking/openfeature-relay/blob/main/CLAUDE.md).

## The reference Dockerfile

[`builder/reference/Dockerfile`](https://github.com/open-feature-forking/openfeature-relay/blob/main/builder/reference/Dockerfile) packages a distribution's generated source as an image, the way flagd's own Dockerfile does: the same two pinned digests (Go toolchain and distroless `static:nonroot`), the same build flags, and the FIPS variant against the certified Go Cryptographic Module. It is a static file; the Builder does not emit it.

- **Build arguments:** `DIST_PATH` (the source directory in the context, default `.`), `VERSION`, `COMMIT`, `DATE` and `FIPS=on` for the FIPS variant.
- **Context:** a custom distribution without `replaces` uses its source directory. A distribution of this repository replaces modules with directories of the repository, so its context is the repository root and `DIST_PATH` is the generated directory.
- **Entrypoint:** `/flagd-build`, as in flagd's image, so that a Deployment with `command:` keeps working when it swaps flagd's image for a distribution's. The image runs as `65532:65532` and has no shell and no labels; `version --verbose` reports the distribution, its version, the commit and the date.
- **Platforms:** the builder stage runs on the build platform and cross-compiles, so a multi-platform build needs no emulation.

`flagd/build.Dockerfile` and `flagd-proxy/build.Dockerfile` stay as they are; flagd-full is still built from them.
