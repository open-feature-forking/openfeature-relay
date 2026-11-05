# Test Case: Evaluation Data Export

## Overview

**ID:** TC-004  
**Goal:** An Extension Author delivers an event sink that meets its contract, and an Operator configures an event sink in flagd-full so that an external system receives the evaluation and configuration change events of flagd continuously.  
**Priority:** High  
**Status:** Draft

## Roles

- Extension Author (writes the event sink and runs its contract tests)
- Operator (configures and runs the event sink in a distribution)
- Client (evaluates flags)

## Preconditions

- The example event sink module `test/eventsink-example` (`webhook`) and the official event sink `eventsink/webhook` exist.
- The flagd-full manifest `distributions/flagd-full/manifest.yaml` lists `eventsink/webhook` under `event_sinks`.
- The external system is a collector stub inside the test, listening on `/events/v1`.

## Flow

| Step | Name | Description | Test Data | Use Case | Covered by |
|------|------|-------------|-----------|----------|------------|
| 1 | Declare event sink | The Extension Author names the event sink's type, the event kinds it subscribes to and its settings; the factory names a faulty setting | `webhook`, kinds `configuration_change`, `evaluation`, `tracking` | [UC-020](../use_cases/UC-020-develop-event-sink-extension.md) | `TestNewFactoryMatchesType`, `TestDefaultKinds`, `TestInvalidSettingsNameTheFaultyValue` (`test/eventsink-example`) |
| 2 | Implement delivery | The event sink delivers a batch to the external system and reports whether the delivery succeeded | - | [UC-020](../use_cases/UC-020-develop-event-sink-extension.md) | `TestPostsEveryKindAsJSON`, `TestStatusMapping`, `TestUnreachableWebhookIsRetried`, `TestTimeoutBoundsADelivery` (`test/eventsink-example`) |
| 3 | Run contract tests | The Extension Author runs the event sink contract tests | `eventsinktest.Run` | [UC-008](../use_cases/UC-008-run-contract-tests.md) | `TestContract`, `TestContractAsNotifier` (`test/eventsink-example`); `TestDrops`, `TestContractInventory` (`core/pkg/eventsink/eventsinktest`) |
| 4 | Verify contract met | The event sink receives events in batches, a full buffer drops and counts events without blocking, and a shutdown delivers the events still buffered | - | - | as step 3 |
| 5 | Publish module | The Extension Author tags and publishes the event sink's component module | `github.com/open-feature/flagd/test/eventsink-example` | [UC-020](../use_cases/UC-020-develop-event-sink-extension.md) | `TestPublishedComponentModulesBuildADistribution`, `TestEveryPublishedModuleResolvesAlone`, `TestUnresolvableModulesAreReported` (`test/release`) serve the modules of the working tree from a `file://` module proxy at `<directory>/v0.0.1-test`, build a manifest that pins them with no replaces and check `components`; no real tag is pushed |
| 6 | Run in a distribution | The event sink runs in a distribution next to the official components | - | [UC-020](../use_cases/UC-020-develop-event-sink-extension.md) | `TestExtensionEventSink` (`test/eventsink-example/distribution`); selected by module path and version in a manifest with no replaces: `TestPublishedComponentModulesBuildADistribution` (`test/release`, the official event sink module) |
| 7 | Check event sinks compiled in | The Operator runs `flagd components` and finds an event sink for the external system | `flagd-full components --output json` | [UC-011](../use_cases/UC-011-inspect-distribution-components.md) | `TestComponentsMatchesManifest` (`test/distributions`), `TestComponentsListsInventory` (`runtime/cmd`) |
| 8 | Configure event sink | The Operator configures the event sink with its destination, its event kinds and a credential from an environment variable, and starts flagd-full | `event-sinks: [{type: webhook}]`, `Authorization: {env: WEBHOOK_TOKEN}`, `flushInterval: 100ms` | [UC-021](../use_cases/UC-021-export-evaluation-data.md) | `TestExportToWebhook`, `TestKindsNarrowWhatTheWebhookReceives` (`test/distributions`); a credential from a file: `TestResolveFromFileTrimsSurroundingWhitespace` (`core/pkg/secret`) |
| 9 | Verify settings checked | flagd-full checks every event sink setting against the event sinks compiled in, starts each sink and subscribes it to its event kinds | - | - | `TestInvalidEventSinkSettingStops` (`test/distributions`, `runtime/cmd`); `TestEventSinkEntriesBecomeConfiguredSinks` (`runtime/cmd`); `TestNoEventSinkNoSubscriber` (`runtime`) |
| 10 | Evaluate flags | A client evaluates flags, and flagd-full publishes one evaluation event per resolved flag with flag key, flag set, variant, value, reason, targeting key and remote origin | flag sets `set-a`, `set-b`, flags `a`, `b` | [UC-021](../use_cases/UC-021-export-evaluation-data.md) | `TestExportToWebhook` (`test/distributions`: flag key, flag set, variant, origin); `TestObserverOncePerFlag` (`core/pkg/events`: value, reason, targeting key); `TestSinkReceivesOneEntryPerFlag` (`runtime`) |
| 11 | Change flag configuration | A source delivers a changed flag configuration, and flagd-full publishes a configuration change event with source, configuration format, flag set, added, updated and removed flag keys and the new document version | updated flag `a` in `set-a` | [UC-021](../use_cases/UC-021-export-evaluation-data.md) | `TestExportToWebhook` (`test/distributions`: source, flag set, updated); `TestChangeEventPerChangedDocument`, `TestChangeEventForVendorFormat` (`runtime`: format, added, version) |
| 12 | Verify events delivered | The external system receives the evaluation and configuration change events in batches; a full buffer drops and counts events on the metrics | `/events/v1` | - | `TestExportToWebhook`, `TestFullBufferDropsAndReportsOnMetrics` (`test/distributions`); `TestBatchBySize`, `TestBatchByAge` (`core/pkg/eventsink`) |

All tests above run in the `test` job (`make test`) on every pull request.

## Validation

1. **Evaluation events exported**: The collector receives one evaluation event for each flag a client resolved.
2. **Configuration change exported**: The collector receives a configuration change event naming the changed flag set and its updated flag key.
3. **Publisher never blocked**: Evaluations keep answering when the event sink's buffer is full, and the dropped events are counted.

## Postconditions

- The events the collector stub received, held in memory and discarded when the test ends.
- The flag configuration files in the test's temporary directory; removing the directory removes them.

## Gaps

| Step | Gap |
|------|-----|
| 5 | No real tag is pushed; `test/release` simulates the release with a `file://` proxy (see TC-002 gap 8). |
