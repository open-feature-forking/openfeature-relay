# Test Case: Change to Release

## Overview

**ID:** TC-007  
**Goal:** A Maintainer verifies a change against every standard distribution on its pull request and, once it is merged, releases the six standard distributions and the changed component modules.  
**Priority:** Critical  
**Status:** Draft

## Roles

- Maintainer (opens the change, reviews and merges the release)
- GitHub Actions (verifies the change, builds and publishes the release)

## Preconditions

- The six standard distribution manifests exist: `distributions/flagd-minimal`, `flagd-minimal-otel`, `flagd-cloud`, `flagd-kubernetes`, `flagd-sync` and `flagd-full` (`manifest.yaml` each).
- The flag configuration set exists under `test/distributions/testdata/flag-configurations/valid/` and `invalid/`.
- The Test Harness tag selection of today's flagd is recorded in `test/integration/testdata/tag-selection.golden`.

## Flow

| Step | Name | Description | Test Data | Use Case | Covered by |
|------|------|-------------|-----------|----------|------------|
| 1 | Open change | The Maintainer opens a pull request against main that changes a component module | a change under `core/` | [UC-013](../use_cases/UC-013-verify-change-against-distributions.md) | `build.yaml` triggers on `pull_request` to `main`; a change that only touches `docs/**` or `README.md` skips the workflow (A1) |
| 2 | Lint and unit test | GitHub Actions runs the linters with the dependency rules and the unit tests of every module | `make lint`, `make test` | [UC-013](../use_cases/UC-013-verify-change-against-distributions.md) | `lint` job (`make lint`, `check-package-coverage`, `check-module-boundary`, `check-evaluator-boundary`) and `test` job (`make test`), every PR |
| 3 | Check stable dependencies | GitHub Actions checks that no Go file imports and no go.mod directly requires `golang.org/x/exp` | the Go files and go.mod files of every module | [UC-013](../use_cases/UC-013-verify-change-against-distributions.md) | the `golang.org/x/exp` greps in the `lint` job, every PR (NFR-012) |
| 4 | Build standard distributions | GitHub Actions builds every standard distribution with the change's modules in place of released versions | the six manifests | [UC-013](../use_cases/UC-013-verify-change-against-distributions.md) | `TestStandardModuleGraphs` and `TestRepositoryManifestsReplaceEveryRelayModule` (`test/distributions`, `test` job); `builder-e2e` job |
| 5 | Verify dependency boundaries | flagd-minimal links no NFR-004 module, flagd-sync links no evaluator or JsonLogic module, and the evaluator and the flagd configuration format depend on no NFR-005 package | flagd-minimal, flagd-sync | [UC-013](../use_cases/UC-013-verify-change-against-distributions.md) | `TestStandardModuleGraphs`, `TestMinimalShapeLinksNoPlatformSDK` (`test/distributions`); `check-module-boundary` in the `lint` job |
| 6 | Run Test Harness | GitHub Actions builds the flagd-full image of the change and runs the RPC and in-process suites with today's tag selection | `flagd/build.Dockerfile`, `tag-selection.golden` | [UC-013](../use_cases/UC-013-verify-change-against-distributions.md) | `TestRPC`, `TestInProcess`, `TestTagSelectionMatchesBranchPoint` (`test/integration`, `integration-test` job, every PR) |
| 7 | Compare flagd-sync with flagd-full | GitHub Actions gives both each flag configuration of the set and checks they accept and reject the same ones | `flag-configurations/valid/`, `flag-configurations/invalid/` | [UC-013](../use_cases/UC-013-verify-change-against-distributions.md) | `TestProxyRejectsWhatFlagdFullRejects`, `TestInvalidFlagConfigurationRejectedAlike` (`test/distributions`) |
| 8 | Run concurrency load | GitHub Actions builds flagd-minimal with the race detector and runs concurrent OFREP, flag evaluation and flag sync clients against it for 60 seconds while its flags change | `make test-load-smoke`, flag generations every 4.1 s | [UC-013](../use_cases/UC-013-verify-change-against-distributions.md) | `load-smoke` job, every PR: `TestCheckerSelfTest`, `TestConcurrency` (`test/concurrency`, NFR-017); a failure keeps flagd's stderr and k6's summary as an artifact (A11) |
| 9 | Compare benchmarks | GitHub Actions runs the evaluator benchmarks on the change and on the pull request's base and compares allocs/op | `.github/scripts/run-benchmarks.sh` | [UC-013](../use_cases/UC-013-verify-change-against-distributions.md) | `benchmark` job (pull requests only) with `compare-allocs.py`; its logic in `test_compare_allocs.py` (`lint` job) |
| 10 | Verify checks reported | Each check shows its result on the pull request, and all pass | - | - | job status per check; `test_workflows.py` (`test_jobs`, `test_gated_jobs`) pins the job list and its gating |
| 11 | Merge release pull request | The Maintainer reviews the proposed module versions and changelogs and merges the release pull request | `release-please-config.json` | [UC-012](../use_cases/UC-012-release-distributions-and-component-modules.md) | gap |
| 12 | Tag modules | GitHub Actions tags each released module and creates its GitHub release; each tag resolves as a version of its module | `sync/file/v0.1.0` | [UC-012](../use_cases/UC-012-release-distributions-and-component-modules.md) | `TestModulePathsMatchTagDirectories`, `TestModulePathMismatchNamesTheModule` (`test/release`, `test` job) check the tag shape; `TestPublishedComponentModulesBuildADistribution`, `TestUnresolvableModulesAreReported` resolve a simulated release (`test/release`); pushing a real tag is a gap |
| 13 | Build release artifacts | GitHub Actions builds every standard distribution, standard and FIPS, over the platform matrix, checks FIPS refuses to run with FIPS mode off, and adds manifest, archives and SBOM | `GODEBUG=fips140=off` | [UC-012](../use_cases/UC-012-release-distributions-and-component-modules.md) | `release-dry-run` job (`release-dry-run.sh`, `release-dry-run-distributions.sh`; gated by path, `FULL_CI` or manual run); `TestRenderedManifestHasNoReplaces`, `TestRenderedManifestNamesAModuleOutsideTheRepository` (`test/release`); partial, see Gaps |
| 14 | Publish images | GitHub Actions builds the standard and FIPS images for `linux/amd64` and `linux/arm64`, pushes and signs them | `ghcr.io/open-feature/flagd` | [UC-012](../use_cases/UC-012-release-distributions-and-component-modules.md) | dry run builds the flagd and flagd-minimal images; `image-test` in the `upgrade-test` job (gated); push and signing are a gap |
| 15 | Verify release visible | The flagd release carries the manifests, binaries and images of all six standard distributions | - | - | gap |

Steps 1–10 are covered on every pull request that changes more than documentation.

## Validation

1. **Change verified**: Every check of steps 2–9 shows its result on the pull request.
2. **Distributions consistent**: flagd-sync and flagd-full give the same verdict for every flag configuration of the set.
3. **Correct under load**: flagd-minimal answers every client from a flag generation inside the change window, and every stream sees every flag generation.
4. **Release complete**: The flagd release lists the six standard distributions with manifest, binary archives, SBOM and images, and each released module resolves at its new tag.

## Postconditions

- The change is merged into main.
- One tag and one GitHub release per released module, and one flagd release with the artifacts of the six standard distributions.
- Signed images of the six standard distributions in the Container Registry.

## Gaps

| Step | Gap |
|------|-----|
| 11 | Component modules are not registered with release-please and its workflow runs only in `open-feature/flagd`; no release pull request exists in this repository. |
| 12 | Nothing is tagged; `TestPublishedComponentModulesBuildADistribution` (`test/release`) resolves the modules from a simulated release with a `file://` proxy, so a real tag resolving remains the gap. |
| 13 | The dry run builds every archive of flagd, flagd-proxy and flagd-minimal, but only one `linux/amd64` archive of the other four distributions and none of their FIPS archives, from the tree rather than released versions; nothing is attached to a release. |
| 14 | No image is pushed or signed; the dry run builds no image of the four other distributions and no FIPS image of flagd-full. |
| 15 | No release workflow produces the flagd release. |
