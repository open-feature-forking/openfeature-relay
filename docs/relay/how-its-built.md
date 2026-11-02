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
