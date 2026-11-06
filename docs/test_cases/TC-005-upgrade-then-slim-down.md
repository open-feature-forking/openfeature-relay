# Test Case: Upgrade, Then Slim Down

## Overview

**ID:** TC-005  
**Goal:** An Operator replaces today's flagd with flagd-full under the unchanged start command and gets the same results, then inspects the standard distributions and moves the deployment to the smallest one that covers it.  
**Priority:** Critical  
**Status:** Draft

## Roles

- Operator (upgrades the deployment, compares distributions and selects one)
- Client (sends evaluation requests)

## Preconditions

- The baseline flagd is today's flagd at commit `90255568`, built as the image `flagd-upgrade-baseline` by `test/upgrade`.
- flagd-full built from this repository (`flagd-upgrade-candidate`, from `flagd/build.Dockerfile`) stands in for the flagd-full release, whose publication UC-012 delivers.
- The flag configuration `/data/flags.json` with the flags `bool-flag`, `string-flag`, `int-flag`, `float-flag`, `object-flag` and `disabled-flag` is written by the test (`test/upgrade`, `test/distributions/parity_test.go`).
- The standard distribution manifests exist under `distributions/` and are built by the Builder in `test/distributions`.
- Accepted differences between the baseline and flagd-full are listed in `test/upgrade/accepted.yaml`.

## Flow

| Step | Name | Description | Test Data | Use Case | Covered by |
|------|------|-------------|-----------|----------|------------|
| 1 | Start flagd-full | The Operator starts flagd-full with the start command of the previous deployment | `start --uri file:/data/flags.json` | [UC-009](../use_cases/UC-009-upgrade-to-flagd-full.md) | `TestUpgrade` (`test/upgrade`, same arguments on both images); `TestCLICharacterization` (`flagd`, `builder-e2e` job) |
| 2 | Read settings as before | flagd-full reads the command line, the configuration file and the environment variables with today's names and precedence | `FLAGD_PORT`, `--config` | [UC-009](../use_cases/UC-009-upgrade-to-flagd-full.md) | `TestStartConfigCharacterization`, `TestStartFlagsAreReadFromEnvironmentAndConfigFile` (`runtime/cmd`) |
| 3 | Start sources and endpoints | flagd-full recognizes the scheme of every source, merges later sources over earlier ones, and opens today's ports | ports `8013`, `8014`, `8015`, `8016` | [UC-009](../use_cases/UC-009-upgrade-to-flagd-full.md) | `TestUpgrade` scenarios `source-http`, `source-grpc`, `source-fsnotify`; `TestSourceMappingCharacterization` (`sync/builder`); `TestRouterSourcePrecedence` (`core/pkg/evaluator/router`), `TestSourcesAreInPriorityOrderAndCopied` (`core/pkg/store`); `TestFlagdFullOpensTodaysPorts` (`test/distributions`) |
| 4 | Verify ready | The management port reports readiness once every source delivered an accepted flag configuration, with no staleness limit | `/readyz` | - | `TestUpgrade` scenarios `management`, `no-staleness-default` |
| 5 | Verify identity | `flagd version --verbose` names flagd-full and its manifest version | `flagd version --verbose` | [UC-009](../use_cases/UC-009-upgrade-to-flagd-full.md) | `TestVersionVerboseNamesEachDistribution` (`test/distributions`), `TestVersionVerboseNamesDistribution` (`runtime/cmd`) |
| 6 | Verify same results | Requests the previous deployment answered get the same results | `string-flag` with `{"tier":"gold"}` → `beta` | [UC-009](../use_cases/UC-009-upgrade-to-flagd-full.md) | `TestUpgrade` scenarios `resolve`, `streams`; `TestRPC`, `TestInProcess` (`test/integration`, `integration-test` job) |
| 7 | Inspect distributions | The Operator lists the components of each standard distribution | `flagd-minimal components --output json` | [UC-011](../use_cases/UC-011-inspect-distribution-components.md) | `TestComponentsMatchesManifest` (`test/distributions`, every manifest); `TestComponentsListsInventory` (`runtime/cmd`) |
| 8 | Select standard distribution | The Operator compares the deployment's needs with the listings and selects the smallest standard distribution that covers them | `flagd-minimal` | [UC-010](../use_cases/UC-010-adopt-standard-distribution.md) | `TestStandardModuleGraphs`, `TestMinimalOtelLinksTheSDK` (`test/distributions`) pin what each manifest contains |
| 9 | Start standard distribution | The Operator starts it with the unchanged start command; it checks every setting and scheme against its components and starts its sources and endpoints | `flagd-minimal start --uri s3://bucket/flags.json` rejected with `run "flagd components"` | [UC-010](../use_cases/UC-010-adopt-standard-distribution.md) | `TestStartsWithFlagdFullDefaults`, `TestAbsentComponentSettingStopsStartup`, `TestUnknownSchemeListsSchemes`, `TestNoEvaluationEndpoint` (`test/distributions`) |
| 10 | Verify ready and identified | The standard distribution reports readiness, not while a source is unavailable or stale, and `flagd version --verbose` names it | `distribution: flagd-minimal 0.0.0` | - | `TestUnavailableSourceNotReady`, `TestVersionVerboseNamesEachDistribution` (`test/distributions`); `TestReadinessLostWhenStale`, `TestReadinessWithoutStaleAfterIgnoresContact` (`runtime`) |
| 11 | Verify same results as flagd-full | Evaluation requests get the same results from the standard distribution as from flagd-full | flags of step 6 | [UC-010](../use_cases/UC-010-adopt-standard-distribution.md) | `TestStandardDistributionParity` (`test/distributions`) |
| 12 | Meet a deprecated evaluation service | A client of the deprecated flagd-web provider or an old RPC provider calls `schema.v1` or `flagd.evaluation.v1`: the slim distributions answer HTTP 404 (`Unimplemented` to gRPC and ConnectRPC clients) while flagd-full serves it, so the Operator keeps flagd-full for that client or moves it to v2 or OFREP | `schema.v1.Service/ResolveBoolean`, `flagd.evaluation.v1.Service/ResolveAll` | [UC-010](../use_cases/UC-010-adopt-standard-distribution.md) | `TestSlimDistributionsDoNotServeDeprecatedServices`, `TestFlagdFullServesDeprecatedServices`, `TestComponentsListsTheHostOfTheLegacyEndpoint` (`test/distributions`) |

`test/upgrade` runs in the `upgrade-test` job (`make upgrade-test`) when a change touches a Dockerfile, `distributions/`, `builder/`, `flagd/` or the build, and on every `FULL_CI` or manual run; the other tests run in the `test` job on every pull request.

## Validation

1. **Drop-in upgrade**: Every difference between the baseline flagd and flagd-full is listed in `test/upgrade/accepted.yaml`.
2. **Smaller distribution, same answers**: The selected standard distribution resolves every flag of the configuration as flagd-full does, and contains only the components its manifest lists, and answers the deprecated evaluation services as the manifest says (FR-063).

## Postconditions

- The images `flagd-upgrade-baseline` and `flagd-upgrade-candidate`, and the built standard distribution binaries under the test's output locations.
- The flag configuration in the test's temporary directory; the configuration and start command are unchanged by the upgrade.

## Gaps

None in the flow. The release of flagd-full and of the standard distributions as binaries and images is UC-012's gap (see [TC-007](TC-007-change-to-release.md)); the staleness limit is tested on the runtime, not on a built distribution.
