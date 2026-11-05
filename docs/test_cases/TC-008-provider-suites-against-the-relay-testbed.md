# Test Case: Provider Suites Against the Relay Testbed

## Overview

**ID:** TC-008  
**Goal:** GitHub Actions runs the flagd provider test suites against the relay testbed in place of the flagd-testbed image, drives it through start, change and restart over the control API, and the providers pass the same scenarios as against the flagd-testbed image.  
**Priority:** High  
**Status:** Draft

## Roles

- GitHub Actions (builds both images and runs the suites)
- Provider test suite (calls the control API and evaluates flags)

## Preconditions

- The `test-harness` submodule is checked out; it provides the flagd-testbed image (`test-harness/flagd/Dockerfile`, built as `testbed-launchpad:local`), the raw flags and the Gherkin suites.
- The relay testbed image `flagd-testbed:local` is built from `distributions/flagd-testbed/Dockerfile` by `make testbed-relay-image`.
- Docker is available (`testbed` job).

## Flow

| Step | Name | Description | Test Data | Use Case | Covered by |
|------|------|-------------|-----------|----------|------------|
| 1 | Start testbed | GitHub Actions starts the relay testbed image as the testbed's compose file does | `flagd-testbed:local` | [UC-022](../use_cases/UC-022-run-provider-test-suites-against-the-testbed.md) | `TestTestbedContract` (`test/testbed`, `make testbed-contract-both`); `make testbed-suites` |
| 2 | Build default runtime | The control module combines the raw flags in memory, builds the runtime from the configuration `default`, renders `allFlags.json`, opens ports 8013 to 8015, and only then opens the control port | ports `8013`–`8015`, `8080`, `flags/allFlags.json` | [UC-022](../use_cases/UC-022-run-provider-test-suites-against-the-testbed.md) | `TestServeOpensThePortWhenTheDefaultConfigurationIsReady`, `TestCombineSkipsSelectorFilesAndMergesDeeply`, `TestRenderLifecycle`, `TestRawFlagsAreNeverWritten` (`testbed/control`); `TestNewFactoriesServeDefault` (`testbed/source`); contract `control-port-opens-ready` (`test/testbed`) |
| 3 | Verify control API ready | The control port answers once the `default` configuration has started | `8080` | - | contract `control-port-opens-ready` and `api` (`test/testbed`) |
| 4 | Start configuration | The suite calls `/start` for `default`; the testbed closes the listener gate, resets and reloads the store, opens the gate when ready and answers | `/start`, configuration `default` | [UC-022](../use_cases/UC-022-run-provider-test-suites-against-the-testbed.md) | `TestStartReloadsCleanedStore`, `TestSameNameDoesNotRebuild` (`testbed/cmd`); `TestAPITexts` (`testbed/control`); contract `api` (`test/testbed`) |
| 5 | Evaluate flags | The suite evaluates flags over RPC and over in-process sync | Gherkin `@rpc`, `@in-process` | [UC-022](../use_cases/UC-022-run-provider-test-suites-against-the-testbed.md) | `TestRPC`, `TestInProcess` (`test/integration`, run by `make testbed-suites` against both images) |
| 6 | Change flag | The suite calls `/change`; the testbed toggles `changing-flag`, loads the new version into the store and then renders `allFlags.json` atomically | `/change`, `changing-flag` | [UC-022](../use_cases/UC-022-run-provider-test-suites-against-the-testbed.md) | `TestChangeThroughTrigger`, `TestLoadReturnsAfterAccept` (`testbed/cmd`); `TestRenderIsAtomic`, `TestDocumentIsWhatIsRendered` (`testbed/control`); `TestLoadFetchesNow` (`testbed/source`) |
| 7 | Restart testbed | The suite calls `/restart`; the testbed answers at once, closes the gate, removes the rendered file and starts again after the delay | `/restart?seconds=3` | [UC-022](../use_cases/UC-022-run-provider-test-suites-against-the-testbed.md) | `TestRestartAnswersAtOnceAndStartsAfterTheDelay` (`testbed/control`); `TestRestartClosesEveryRuntimePort`, `TestChangeSurvivesRestart` (`testbed/cmd`); contract `outage`, `files`, `change-after-restart` (`test/testbed`) |
| 8 | Verify providers reconnect | The providers reconnect and fire their ready events after the restart | Gherkin `@reconnect`, `@events`, `@grace` | - | in-process: `TestInProcess` (`make testbed-suites`, whose tags keep these scenarios); RPC: `TestRPC` with `TESTBED_RPC_OUTAGES=true` (`make testbed-rpc-outages`, `testbed-rpc-outages` job), see Gaps |
| 9 | Verify same scenarios pass | The suites pass the same scenarios against the relay testbed as against the flagd-testbed image | `launchpad.json`, `relay.json` | - | `.github/scripts/compare-pass-sets.py` in `make testbed-suites` |

The `testbed/*` modules run in the `test` job (`make test`, through `test-contrib`) on every pull request. `make testbed-contract-both` and `make testbed-suites` run in the `testbed` job, whenever a change touches Go code, a Dockerfile, `distributions/`, `builder/`, `flagd/`, the testbed or the build, and on every `FULL_CI` or manual run.

## Validation

1. **Same verdicts**: The set of passing scenarios is the same against `flagd-testbed:local` as against `testbed-launchpad:local`.
2. **Same contract**: The launchpad contract of `test/testbed` passes against both images, with the four launchpad quirks fixed on the relay testbed only.

## Postconditions

- The local images `testbed-launchpad:local` and `flagd-testbed:local`; the containers the suites start are removed when the tests end.
- The `go test` output of both runs in `$TESTBED_OUT`, a temporary directory by default.

## Gaps

| Step | Gap |
|------|-----|
| 8 | The RPC provider's reconnect, event and grace scenarios run only in the `testbed-rpc-outages` job, with `FULL_CI` or a manual run; the `testbed` job excludes them from `TestRPC`. |
