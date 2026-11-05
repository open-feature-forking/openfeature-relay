# Test Case: Extension Components in a Distribution

## Overview

**ID:** TC-002  
**Goal:** An Extension Author develops a sync provider, a service endpoint, a middleware and a telemetry provider in their own component modules, proves each against its contract, publishes them, and a Distribution Builder compiles them into a distribution like official components.  
**Priority:** High  
**Status:** Draft

## Roles

- Extension Author (writes the component modules and runs their contract tests)
- Distribution Builder (selects the extensions in a manifest)

## Preconditions

- The example component modules exist, each with its own `go.mod` that requires the core module: `test/sync-example`, `test/service-example`, `test/middleware-example` and `test/telemetry-example`.
- The manifest `test/distributions/example/manifest.yaml` lists `github.com/open-feature/flagd/test/service-example`.
- The external contract run lists its modules in `testdata/contract-external.txt`.

## Flow

| Step | Name | Description | Test Data | Use Case | Covered by |
|------|------|-------------|-----------|----------|------------|
| 1 | Declare sync provider | The Extension Author names the sync provider's type and schemes in a module that depends on the core module | `example`, `example://` | [UC-003](../use_cases/UC-003-develop-sync-provider-extension.md) | `TestNewFactoriesMatchTypes`, `TestFactoryNeedsASource` (`test/sync-example`) |
| 2 | Implement fetcher | The fetcher fetches its source once per call and reports a new source version or that nothing changed | `example://flags` | [UC-003](../use_cases/UC-003-develop-sync-provider-extension.md) | `TestFetcherContract` (`test/sync-example`, runs `synctest.RunFetcher`) |
| 3 | Implement service endpoint | The service endpoint declares it resolves through the evaluation router, resolves the flags of a request's selector, publishes one evaluation event per flag and reacts to configuration changes of its selector | `rest-example` | [UC-004](../use_cases/UC-004-develop-service-endpoint-extension.md) | `TestNewFactoryMatchesType`, `TestEffectiveSelector`, `TestChangesOnlyForSelector`, `TestValidatorFollowsDocumentVersion` (`test/service-example`) |
| 4 | Implement middleware | The middleware wraps an endpoint's handler, passes requests on or answers them, and its factory names a faulty setting | `api-key` | [UC-005](../use_cases/UC-005-develop-middleware-extension.md) | `TestNewFactoryMatchesType`, `TestCustomHeader`, `TestKeyWithoutSelector`, `TestRefusalsAnsweredAndCounted`, `TestInvalidSettingsAreNamed` (`test/middleware-example`) |
| 5 | Implement telemetry provider | The telemetry provider reports to its backend, never blocks and delivers what it holds on shutdown; its factory names a faulty setting | `jsonlines` | [UC-006](../use_cases/UC-006-develop-telemetry-provider-extension.md) | `TestNewFactoryMatchesType`, `TestWritesJSONLines`, `TestInvalidSettingsAreNamed` (`test/telemetry-example`) |
| 6 | Run contract tests | The Extension Author runs each component type's contract tests with the component's factory | `synctest.RunFetcher`, `servicetest.Run`, `middlewaretest.Run`, `telemetrytest.Run` | [UC-008](../use_cases/UC-008-run-contract-tests.md) | `TestFetcherContract`, `TestTriggerContract` (`test/sync-example`), `TestEndpointContract` (`test/service-example`), `TestContract` (`test/middleware-example`, `test/telemetry-example`); outside the workspace: `make test-contract-external` |
| 7 | Verify contract report | The run checks the declarations, creates each component with stand-ins, exercises every case and reports each case by name | - | - | `TestValidateDeclarations` (`core/pkg/sync`, `configformat`, `evaluator`, `eventsink`), `TestMissingInputFailsNamingIt` (`servicetest`), `TestContractInventory` (every runner package), `TestBrokenStubsFailExactlyTheirCases`, `TestRunReportsCasesAsSubtests` (`synctest`) |
| 8 | Publish modules | The Extension Author tags and publishes each component module under its module path and version | `github.com/open-feature/flagd/test/service-example v0.0.0` | [UC-004](../use_cases/UC-004-develop-service-endpoint-extension.md) | `TestPublishedComponentModulesBuildADistribution`, `TestEveryPublishedModuleResolvesAlone`, `TestUnresolvableModulesAreReported` (`test/release`) serve the modules of the working tree from a `file://` module proxy at `<directory>/v0.0.1-test`, build a manifest that pins them with no replaces and check `components`; no real tag is pushed; the example modules themselves are not component modules of the repository, the test publishes one of each component type instead |
| 9 | Select in manifest | The Distribution Builder lists the service endpoint in a manifest, builds the distribution, and a request resolves through `rest-example` | `test/distributions/example/manifest.yaml` | [UC-004](../use_cases/UC-004-develop-service-endpoint-extension.md) | `TestBuildExampleDistribution` (`test/distributions`) |
| 10 | Compose other extensions | The sync provider, middleware and telemetry provider run in a distribution next to the official components, with the core's poller as the sync provider's default fetch trigger | `api-key`, `jsonlines`, `example://` | [UC-005](../use_cases/UC-005-develop-middleware-extension.md), [UC-006](../use_cases/UC-006-develop-telemetry-provider-extension.md), [UC-003](../use_cases/UC-003-develop-sync-provider-extension.md) | `TestExtensionMiddlewareBindsSelector` (`test/middleware-example/distribution`), `TestExtensionTelemetryProvider` (`test/telemetry-example/distribution`), `TestDefaultTriggersReplaceThePoller` (`sync/builder`), `TestExtensionTriggerAttached` (`runtime`); selected by module path and version in a manifest with no replaces: `TestPublishedComponentModulesBuildADistribution` (`test/release`, the official middleware, telemetry provider and sync provider modules) |
| 11 | Verify extension serves | The distribution built in step 9 answers an evaluation request through the extension endpoint | - | - | `TestBuildExampleDistribution` (`test/distributions`) |

All tests above run in the `test` job (`make test`) on every pull request.

## Validation

1. **Contracts met**: Every contract case of the four extensions passes, inside the workspace and with the core module fetched from outside it.
2. **Extensions like official components**: A distribution built from a manifest contains the extension and serves through it, with no change to the core module.

## Postconditions

- The distribution binary `example` and its generated source in the manifest's output location; removing the output location removes both.
- The example component modules and the manifest are unchanged.

## Gaps

| Step | Gap |
|------|-----|
| 8 | A real tag is not pushed and no registry serves the modules: `test/release` simulates the release with a `file://` proxy. The proxy rewrites the in-repository requirements to the test version, which the release automation (registered with release-please, UC-012 gap) must do for real. The contract tests still resolve the core module at a pseudo-version. `runtime` does not require the flagd module (`github.com/open-feature/flagd/flagd`), which no tag of this repository resolves; its tests build the components they need from `runtime/internal/officialset` (`TestRuntimeDoesNotRequireTheFlagdModule`). |
