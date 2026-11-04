---
description: What works today and what comes next
---

# Status and roadmap

OpenFeature Relay is a proof of concept.
The specification is complete enough to build against; the code is flagd's, part way through the change.
What runs today is flagd-full, which is flagd built from this repository.

## Done

- **Evaluator without telemetry**: the evaluator no longer imports OpenTelemetry; the service endpoints record evaluation spans instead (FR-040, FR-033).
- **Evaluator boundary**: `depguard` rules and a CI check keep OpenTelemetry, gRPC, ConnectRPC, Kubernetes and cloud SDKs out of the evaluator's dependencies (NFR-005).
- **Standard library instead of `golang.org/x/exp`** (NFR-012).

## In progress

- [UC-018 Develop Configuration Format Extension](../use_cases/UC-018-develop-configuration-format-extension.md): the configuration format is split out of the evaluator, with document versions.

## How a change is verified

[UC-013](../use_cases/UC-013-verify-change-against-distributions.md) maps to these checks of the `build` workflow:

| Check | Job | Covers |
|-------|-----|--------|
| linters, dependency rules, module and evaluator boundaries | `lint` | NFR-004, NFR-005 |
| unit tests of every module, and the standard distributions' bounds and verdicts against flagd-full | `test` (`make test`, with `test/distributions`) | NFR-004, NFR-011, FR-026 |
| RPC and in-process Test Harness suites against flagd, with the tag selection pinned to today's flagd | `integration-test` (`TestTagSelectionMatchesBranchPoint`) | NFR-007, C-010 |
| allocs/op against the pull request's base commit; time deviations beyond 5% are reported, never failing | `benchmark` | NFR-001 |
| FIPS compile and crypto closure of every standard distribution | `fips`, gated by `FULL_CI` or a manual run | C-009 |

Branch protection is not set: the project lead decides which of these checks are required for merging. NFR-001 against the upstream commit the work branched from is not compared yet (`compare-allocs.py` would need a mapping of the old package paths to the new ones), so NFR-001 stays `In Progress`.

## Next

The proof of concept proceeds one use case at a time:

| # | Use case | Brings in |
|---|----------|-----------|
| 1 | [UC-018](../use_cases/UC-018-develop-configuration-format-extension.md) | configuration format split out of the evaluator, document versions |
| 2 | [UC-014](../use_cases/UC-014-embed-evaluator-in-in-process-provider.md) | evaluator and flagd configuration format as dependency-free modules |
| 3 | [UC-007](../use_cases/UC-007-develop-evaluator-extension.md) | untyped bulk evaluator, typed adapter, evaluation router, engine modules |
| 4 | [UC-003](../use_cases/UC-003-develop-sync-provider-extension.md) | sync factory, fetching sync providers and the shared poller |
| 5 | [UC-004](../use_cases/UC-004-develop-service-endpoint-extension.md) | service factory, event bus, runtime registry |
| 6 | [UC-005](../use_cases/UC-005-develop-middleware-extension.md), [UC-006](../use_cases/UC-006-develop-telemetry-provider-extension.md), [UC-020](../use_cases/UC-020-develop-event-sink-extension.md) | middleware, telemetry provider and event sink factories |
| 7 | [UC-008](../use_cases/UC-008-run-contract-tests.md) | contract tests for every component type |
| 8 | [UC-001](../use_cases/UC-001-build-custom-distribution.md) | module split, builder CLI and manifest |
| 9 | [UC-009](../use_cases/UC-009-upgrade-to-flagd-full.md) | flagd-full built by the builder, flagd test harness green |
| 10 | [UC-011](../use_cases/UC-011-inspect-distribution-components.md), [UC-010](../use_cases/UC-010-adopt-standard-distribution.md), [UC-017](../use_cases/UC-017-run-configuration-proxy.md), [UC-021](../use_cases/UC-021-export-evaluation-data.md) | components command, standard distributions, flagd-sync, event export |
| 11 | [UC-019](../use_cases/UC-019-serve-vendor-flags.md) | a Flagsmith engine module as the cross-vendor proof |
| 12 | [UC-002](../use_cases/UC-002-package-distribution-as-image.md), [UC-012](../use_cases/UC-012-release-distributions-and-component-modules.md), [UC-013](../use_cases/UC-013-verify-change-against-distributions.md) | image, release, CI verification |

[UC-016 Recover from Snapshot](../use_cases/UC-016-recover-from-snapshot.md) is deferred to a later release.

A use case is done when its main success scenario and every alternative flow have an automated test, its pull request is reviewed and merged, and its **Status** line moves to `Implemented`.
