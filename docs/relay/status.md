---
description: What works today and what comes next
---

# Status and roadmap

OpenFeature Relay is a proof of concept.
The specification is complete enough to build against, and the code follows it: the component types, the runtime, the builder and the standard distributions exist on `main`.
What runs by default is flagd-full, which is flagd built from this repository by the relay builder.

## Done

Steps 1 to 14 of the proof of concept are done; step 12's release runs as a dry run.
A use case counts as done when its **Status** line reads `Implemented`, or `Tested` once a test case under `docs/test_cases/` covers it.

- **Evaluator without telemetry, with a boundary**: the evaluator no longer imports OpenTelemetry, and `depguard` rules and a CI check keep OpenTelemetry, gRPC, ConnectRPC, Kubernetes and cloud SDKs out of its dependencies (FR-040, NFR-005).
- **Configuration format and evaluator as modules**: the configuration format is split out of the evaluator, with document versions ([UC-018](../use_cases/UC-018-develop-configuration-format-extension.md)), and the evaluator and flagd's format are dependency-free modules that an in-process provider can embed ([UC-014](../use_cases/UC-014-embed-evaluator-in-in-process-provider.md)).
- **Component factories**: sync providers ([UC-003](../use_cases/UC-003-develop-sync-provider-extension.md)),
    evaluators ([UC-007](../use_cases/UC-007-develop-evaluator-extension.md)),
    service endpoints ([UC-004](../use_cases/UC-004-develop-service-endpoint-extension.md)),
    middleware ([UC-005](../use_cases/UC-005-develop-middleware-extension.md)), telemetry providers ([UC-006](../use_cases/UC-006-develop-telemetry-provider-extension.md)) and event sinks ([UC-020](../use_cases/UC-020-develop-event-sink-extension.md)), with the event bus and the runtime registry behind them.
- **Contract tests** for every component type ([UC-008](../use_cases/UC-008-run-contract-tests.md)).
- **The builder**: `relay-builder` turns a manifest into the source of a distribution and compiles it ([UC-001](../use_cases/UC-001-build-custom-distribution.md)).
    The `flagd/` module in this repository is what the builder generates from `distributions/flagd-full/manifest.yaml`, and CI checks that it stays so.
- **flagd-full as the builder's output**, with the differential test that runs today's flagd and flagd-full side by side and the list of accepted differences ([UC-009](../use_cases/UC-009-upgrade-to-flagd-full.md)).
- **Standard distributions**: flagd-minimal, flagd-cloud, flagd-kubernetes and flagd-sync, each with a manifest under `distributions/` ([UC-010](../use_cases/UC-010-adopt-standard-distribution.md), [UC-017](../use_cases/UC-017-run-configuration-proxy.md)).
    `flagd components` lists what is compiled into a binary ([UC-011](../use_cases/UC-011-inspect-distribution-components.md)).
- **Event export**: an event sink receives evaluation data, the webhook sink ships in flagd-full, and the event collector endpoint accepts events from providers.
    Operators switch them on in flagd's configuration file ([UC-021](../use_cases/UC-021-export-evaluation-data.md)).
- **A vendor's flags behind OFREP**: the flagsmith distribution evaluates Flagsmith environments with Flagsmith's engine ([UC-019](../use_cases/UC-019-serve-vendor-flags.md), [guide](../guides/serve-flagsmith-flags.md)).
- **The reference image and the CI checks**: a reference Dockerfile builds any distribution into an image ([UC-002](../use_cases/UC-002-package-distribution-as-image.md)), and CI verifies a change against the distributions ([UC-013](../use_cases/UC-013-verify-change-against-distributions.md)).
- **A release dry run**: the release's build steps run in CI without publishing or signing anything ([UC-012](../use_cases/UC-012-release-distributions-and-component-modules.md), part of it).
- **The flagd testbed as a distribution**: the testbed the provider suites run against is built by the relay builder, with a control module, a listener gate and a runtime reset, and CI runs the RPC and in-process suites against it and against the flagd-testbed image ([UC-022](../use_cases/UC-022-run-provider-test-suites-against-the-testbed.md), [the testbed](testbed.md)).
- **Syncing from an upstream flag sync endpoint**: a `grpc` source opts into the Go flagd provider's gRPC sync features with a `grpc` object: its retry policy, fatal status codes, a stream deadline, keepalive, the selector header and readiness on the first payload; without the object it behaves as before ([UC-023](../use_cases/UC-023-sync-from-an-upstream-flag-sync-endpoint.md), [the settings](grpc-source.md)).

## How a change is verified

[UC-013](../use_cases/UC-013-verify-change-against-distributions.md) maps to these checks of the `build` workflow.
The workflow does not run for a change that touches only `README.md` or `docs/**`; the `docs` and `Markdown checks` workflows run for those.

| Check | Job | Covers |
|-------|-----|--------|
| linters, dependency rules, module and evaluator boundaries, package coverage, the modules flagd-full links, no `golang.org/x/exp` | `lint` | NFR-004, NFR-005, NFR-012 |
| unit tests of every module with `-race`, the examples, the evaluator testkit, and the standard distributions' bounds and verdicts against flagd-full | `test` (`make test`) | NFR-004, NFR-011, FR-026 |
| the generated command line reference matches the code | `docs-check` | |
| flagd-full built from its manifest by the builder, its components checked, the CLI pins and the flagsmith distribution built | `builder-e2e` | FR-005, FR-006, FR-027, NFR-008 |
| RPC and in-process Test Harness suites against flagd, with the tag selection pinned to today's flagd | `integration-test` | NFR-007, C-010 |
| allocs/op against the pull request's base commit; time deviations beyond 5% are reported, never failing | `benchmark` (pull requests) | NFR-001 |
| today's flagd and flagd-full side by side, and the reference Dockerfile's images | `upgrade-test`, when a Dockerfile or the build changes | FR-009, NFR-007 |
| the release's build steps without publishing | `release-dry-run`, when a release input changes | FR-030, FR-031 |
| FIPS compile and crypto closure of every standard distribution | `fips`, gated by `FULL_CI` or a manual run | C-009 |
| the local Docker build and its vulnerability scan | `docker-local`, gated by `FULL_CI` or a manual run | NFR-009 |

Branch protection is not set: the project lead decides which of these checks are required for merging.
NFR-001 against the upstream commit the work branched from is not compared yet (`compare-allocs.py` would need a mapping of the old package paths to the new ones), so NFR-001 stays `In Progress`.

## Next

The proof of concept proceeds one use case at a time.
The table shows the order; the sections above say which rows are done.

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
| 13 | [UC-022](../use_cases/UC-022-run-provider-test-suites-against-the-testbed.md) | the flagd testbed as a `relay-builder` distribution |
| 14 | [UC-023](../use_cases/UC-023-sync-from-an-upstream-flag-sync-endpoint.md) | the Go flagd provider's gRPC sync features in `sync/flagd-grpc`, opt-in |
| 15 | [#176](https://github.com/open-feature-forking/openfeature-relay/issues/176) (in progress) | a CI test and lint job per component, only for the modules a PR reaches |
| 16 | [#183](https://github.com/open-feature-forking/openfeature-relay/issues/183) (planned) | a load test that flagd-minimal stays correct under concurrent OFREP, RPC and sync clients |

[UC-016 Recover from Snapshot](../use_cases/UC-016-recover-from-snapshot.md) is deferred to a later release.
Provider migration (formerly UC-015) is a follow-up after the proof of concept, [#156](https://github.com/open-feature-forking/openfeature-relay/issues/156).

Three ideas are written down and not scheduled: [on-demand selectors for sources, with flagd-sync replacing flagd-proxy](https://github.com/open-feature-forking/openfeature-relay/issues/149), [the repository layout after the proof of concept](https://github.com/open-feature-forking/openfeature-relay/issues/111) and [linking evaluations to the sync trace that delivered their configuration](https://github.com/open-feature-forking/openfeature-relay/issues/59).

A use case is done when its main success scenario and every alternative flow have an automated test, its pull request is reviewed and merged, and its **Status** line moves to `Implemented`.
