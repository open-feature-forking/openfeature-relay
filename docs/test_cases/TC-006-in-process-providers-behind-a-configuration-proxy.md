# Test Case: In-Process Providers Behind a Configuration Proxy

## Overview

**ID:** TC-006  
**Goal:** An Operator runs flagd-sync as the configuration proxy of a deployment, and an in-process provider that embeds the evaluator receives its selector's flag configurations from it, follows a change and evaluates flags locally with the results remote evaluation gives.  
**Priority:** High  
**Status:** Draft

## Roles

- Operator (runs the configuration proxy)
- Provider Author (embeds the evaluator in an in-process provider)
- In-Process Provider (subscribes to the flag sync endpoint and evaluates locally)

## Preconditions

- The flagd-sync manifest `distributions/flagd-sync/manifest.yaml` exists and is built by the Builder in `test/distributions`.
- The test writes two sources: `a.json` with flag set `set-a` (`flag-a`, `shared`) and `b.json` with flag set `set-b` (`flag-b`).
- The in-process provider examples `test/inprocess-example/file`, `test/inprocess-example/filegrpc` and `test/inprocess-example/engine` exist.
- The flagd Test Harness evaluator suite is available as the `test-harness` submodule (`test/evaluator-testkit`).

## Flow

| Step | Name | Description | Test Data | Use Case | Covered by |
|------|------|-------------|-----------|----------|------------|
| 1 | Start configuration proxy | The Operator starts flagd-sync with the deployment's sources; it checks every setting, scheme and configuration format against its components | sources `a.json`, `b.json` | [UC-017](../use_cases/UC-017-run-configuration-proxy.md) | `TestAbsentComponentSettingStopsStartup`, `TestUnknownSchemeListsSchemesOnFlagdSync`, `TestAbsentFormatSkipsSource` (`test/distributions`) |
| 2 | Verify proxy ready | Each source's flag configuration is validated and kept, and the management port reports readiness only once every source delivered | `/readyz` | - | `TestFlagSyncProxyServesInProcessProvider`, `TestUnavailableSourceNotReady`, `TestProxyRejectsWhatFlagdFullRejects` (`test/distributions`) |
| 3 | Embed evaluator | The Provider Author adds the flagd configuration format, the evaluator and a sync provider to the provider, without telemetry, service endpoint or transport code | `test/inprocess-example/file`, `filegrpc` | [UC-014](../use_cases/UC-014-embed-evaluator-in-in-process-provider.md) | `TestModuleGraph`, `TestBoundaryCatchesForbiddenDependency` (`test/inprocess-example/file`); `TestModuleGraph` (`test/inprocess-example/filegrpc`, `test/inprocess-example/engine`); `check-module-boundary` (`lint` job) |
| 4 | Subscribe with selector | The in-process provider subscribes to the flag sync endpoint with its selector and receives the matching documents unchanged | selector `flagSetId=set-a` | [UC-017](../use_cases/UC-017-run-configuration-proxy.md) | `TestFlagSyncProxyServesInProcessProvider` (`test/distributions`, a subscriber of the flag sync protocol); `TestFlagSyncPayloadCharacterization` (`test/distributions`); a vendor document: `TestVendorConfigurationProxy` (`test/distributions`), `TestFormatHeaderServesVendorDocument` (`service/flag-sync`) |
| 5 | Feed the configuration format | The provider's gRPC sync provider delivers each flag configuration to the configuration format, which validates it and keeps it in the store | - | [UC-014](../use_cases/UC-014-embed-evaluator-in-in-process-provider.md) | `TestGrpcSyncFeedsFormat` (`test/inprocess-example/filegrpc`, against a flag sync stub); `TestFileSyncFeedsFormat`, `TestInvalidConfigurationKeepsFlags` (`test/inprocess-example/file`) |
| 6 | Push a change | A source changes, and flagd-sync sends the changed documents only to subscribers whose selector they match | `set-b` changed | [UC-017](../use_cases/UC-017-run-configuration-proxy.md) | `TestFlagSyncProxyServesInProcessProvider` (`test/distributions`: a change of `set-b` reaches no subscriber of `set-a`); `TestVendorStreamFollowsConfigurationChangeEvents` (`service/flag-sync`) |
| 7 | Evaluate locally | The provider resolves boolean, string, integer, float and object flags through the typed adapter with the embedded evaluator | `flag-a` | [UC-014](../use_cases/UC-014-embed-evaluator-in-in-process-provider.md) | `TestResolveThroughAdapter` (`test/inprocess-example/file`, `test/inprocess-example/engine`), `TestUnresolvableFlags` (`test/inprocess-example/file`) |
| 8 | Verify same as remote | The embedded evaluator gives the results remote evaluation gives for the same flag configuration | flagd Test Harness evaluator scenarios | - | `TestJsonLogicEvaluatorPassesFlagdSuite` (`test/evaluator-testkit`, fractional scenarios excluded, #47) |
| 9 | Verify proxy evaluates nothing | flagd-sync offers no evaluation endpoint | - | - | `TestNoEvaluationEndpoint` (`test/distributions`) |
| 10 | Run provider test suite | The Provider Author runs the flagd Test Harness in-process suite against the provider, and every scenario passes | - | [UC-014](../use_cases/UC-014-embed-evaluator-in-in-process-provider.md) | gap |

All tests above run in the `test` job (`make test`) on every pull request; `check-module-boundary` runs in the `lint` job.

## Validation

1. **One place for flag configurations**: The in-process provider receives exactly the flags of its selector from flagd-sync, and a change of another flag set does not reach it.
2. **Local results match remote ones**: The embedded evaluator returns value, variant, reason and metadata as remote evaluation does.
3. **Dependency-free embedding**: The provider's module graph contains no telemetry, service endpoint or transport module.

## Postconditions

- The source files `a.json` and `b.json` in the test's temporary directory; removing the directory removes them.
- The `flagd-sync` binary under the test's output location.

## Gaps

| Step | Gap |
|------|-----|
| 4–6 | No single test connects an in-process provider that embeds the evaluator to a running flagd-sync: the proxy is tested with a subscriber of the flag sync protocol, and the provider with a flag sync stub (UC-017 gap table, #156). |
| 5 | No test sends a flag sync request that names a vendor configuration format from the provider; `sync/flagd-grpc` has no such setting. |
| 10 | No test runs the flagd Test Harness in-process suite against a provider that embeds this repository's evaluator; `TestInProcess` (`test/integration`) runs the released Go flagd provider against flagd-full (#156). |
