# Test Case: Vendor Flags Through flagd

## Overview

**ID:** TC-003  
**Goal:** An Extension Author publishes a vendor's configuration format and its paired evaluator, and an Operator serves that vendor's flags per environment through a vendor distribution's OFREP service endpoint with the results the vendor's evaluator gives.  
**Priority:** High  
**Status:** Draft

## Roles

- Extension Author (writes the configuration format and evaluator and runs their contract tests)
- Operator (runs the vendor distribution in place of the vendor's edge proxy)

## Preconditions

- The Flagsmith engine module `contrib/flagsmith/engine`, its sync provider `contrib/flagsmith/sync` and its middleware `contrib/flagsmith/middleware` exist.
- The vendor distribution manifest `distributions/flagsmith/manifest.yaml` exists.
- The environment document `test/distributions/testdata/flagsmith-environment.json` (environment key `ENVKEY1`) is served by a stub of the Vendor Flag Service inside the test.
- The second engine module `test/configformat-example` (`keyvalue`) and the manifest `test/distributions/sync-vendor/manifest.yaml` exist.

## Flow

| Step | Name | Description | Test Data | Use Case | Covered by |
|------|------|-------------|-----------|----------|------------|
| 1 | Implement configuration format | The Extension Author names the configuration format, validates a vendor flag configuration, rejects a faulty one naming the entry, and reports flag keys, flag set, document version and changes | `flagsmith`, `keyvalue` | [UC-018](../use_cases/UC-018-develop-configuration-format-extension.md) | `TestFormatContract`, `TestAcceptRejectsNamingEntry`, `TestVersionIsDigestOfBody`, `TestFactoryNeedsStore` (`contrib/flagsmith/engine`); `TestEngineModuleExportsFormatFactory`, `TestDocumentTextIsTheDelivery` (`test/configformat-example`); `TestChangeEventPerChangedDocument` (`runtime`) |
| 2 | Implement evaluator | The Extension Author pairs an evaluator with the configuration format; it resolves one, several or all flags of a selector into untyped results | flags `banner`, `dark-mode`, `limit` | [UC-007](../use_cases/UC-007-develop-evaluator-extension.md) | `TestEnginePairMatchesTypes`, `TestFactory`, `TestEvaluateMatchesFlagengine`, `TestFlagSetsAreEnvironments` (`contrib/flagsmith/engine`); `TestFactory`, `TestAttributesShapeResults` (`test/configformat-example/evaluator`) |
| 3 | Run contract tests | The Extension Author runs the configuration format and evaluator contract tests | `formattest.Run`, `evaluatortest.Run` | [UC-008](../use_cases/UC-008-run-contract-tests.md) | `TestFormatContract`, `TestEvaluatorContract` (`contrib/flagsmith/engine`); `TestContract` (`test/configformat-example`), `TestEvaluatorContract` (`test/configformat-example/evaluator`); outside the workspace: `make test-contract-external` |
| 4 | Verify contracts met | Every contract case of both components passes | - | - | as step 3 |
| 5 | Publish modules | The Extension Author tags and publishes the engine module | `contrib/flagsmith/engine` | [UC-018](../use_cases/UC-018-develop-configuration-format-extension.md), [UC-007](../use_cases/UC-007-develop-evaluator-extension.md) | gap |
| 6 | Select in manifest | A Distribution Builder lists the engine module under `engines:` and builds the distribution | `distributions/flagsmith/manifest.yaml`, `test/distributions/sync-vendor/manifest.yaml` | [UC-001](../use_cases/UC-001-build-custom-distribution.md) | `TestFlagsmithDistribution`, `TestVendorConfigurationProxy` (`test/distributions`); `build-flagsmith` (`builder-e2e` job) |
| 7 | Start vendor distribution | The Operator starts the vendor distribution with one source per environment, its credential from an environment variable, and the vendor's configuration format | `FLAGSMITH_TEST_KEY=ser.test-key`, format `flagsmith` | [UC-019](../use_cases/UC-019-serve-vendor-flags.md) | `TestFlagsmithServesAnEnvironmentDocument` (`test/distributions`, one environment); `TestSchemesAndURIs` (`contrib/flagsmith/sync`); `TestHeadersFromEnvAndFile` (`core/pkg/sync`) |
| 8 | Verify ready | Each source's environment document is fetched and accepted, and the management port reports readiness | `/readyz` | - | `TestFlagsmithServesAnEnvironmentDocument` (`test/distributions`) |
| 9 | Bind credential to environment | The Operator configures the middleware to map each client credential to its environment's selector and to request attributes | `X-Environment-Key: ENVKEY1`, selector `flagSetId=ENVKEY1` | [UC-019](../use_cases/UC-019-serve-vendor-flags.md) | `TestBindsFlagSetAndKind`, `TestClientCannotOverrideSelector` (`contrib/flagsmith/middleware`); `TestBoundSelectorNarrowing` (`runtime`); not in the distribution, see Gaps |
| 10 | Evaluate vendor flags | The Operator sends OFREP evaluation requests for an environment and an identity and gets the vendor evaluator's value, variant, reason and metadata | flag `banner` | [UC-019](../use_cases/UC-019-serve-vendor-flags.md) | `TestFlagsmithServesAnEnvironmentDocument` (`test/distributions`, compared against `flagengine`) |
| 11 | Announce vendor change | A changed environment document is fetched and accepted, and the OFREP event stream of its selector announces the change | - | [UC-019](../use_cases/UC-019-serve-vendor-flags.md) | `TestAnEventMakesTheProviderFetch` (`sync/sse`), `TestOFREPEventStreamAnnouncesAVendorChange` (`runtime`), `TestEventStreamVendorFormat` (`service/ofrep/sse`); component level only, see Gaps |
| 12 | Verify changed result | Later evaluation requests resolve against the changed environment document, as the edge proxy would | - | - | gap |

All tests above run in the `test` job (`make test`) on every pull request.

## Validation

1. **Vendor results**: The OFREP service endpoint returns, for each flag and evaluation context, the value and reason the vendor's own evaluator gives.
2. **One flag set per environment**: Each environment's flags are resolved only for the selector its credential is bound to.

## Postconditions

- The `flagsmith` binary and its generated source in the manifest's output location.
- No data outside the test's temporary directory; the stub Vendor Flag Service stops with the test.

## Gaps

| Step | Gap |
|------|-----|
| 5 | No test tags or publishes a component module. |
| 7 | The distribution test runs one environment with its key from an environment variable; several environments and a key from a file are tested at component level only (#94). |
| 9 | `distributions/flagsmith/manifest.yaml` lists no middleware, so the credential-to-environment binding is not exercised in the vendor distribution (#94). |
| 11 | No test changes a Flagsmith environment document on a running vendor distribution; the event stream announcement is tested with a stub configuration format (#94). |
| 12 | No end-to-end test resolves against a changed vendor document or compares the results with the edge proxy (#94). |
