# Use Case: Develop Sync Provider Extension

## Overview

**Use Case ID:** UC-003  
**Use Case Name:** Develop Sync Provider Extension  
**Primary Actor:** Extension Author  
**Goal:** Publish a sync provider in the Extension Author's own component module, verified against the sync provider contract, so that a distribution can read flags from a kind of source no official sync provider supports.  
**Trigger:** Extension Author decides to make flagd read flags from a kind of source that no official sync provider supports  
**Status:** Draft  

**Requirements:** [FR-010, FR-045, FR-015, FR-003, FR-008, FR-025, FR-029, FR-049, FR-052, C-001](../requirements.md)

## Preconditions

- A release of the core module that contains the sync provider factory interface, the core's poller and the sync provider contract tests is available.
- The Extension Author has a Go module of their own in which the extension will live.

## Main Success Scenario

1. Extension Author adds the core module as a dependency of their component module.
2. Extension Author names the sync provider's type and the schemes of the sources it handles.
3. Extension Author implements the sync provider: it prepares its connection to the source, delivers the source's complete flag configuration as soon as it can, delivers every later change the source announces, delivers the complete flag configuration again on request, and reports whether it is ready to watch for changes. It delivers each flag configuration as read, with its encoding when known, and leaves decoding and validating it to flagd.
4. Extension Author implements the factory that creates the sync provider for one source from the dependencies a distribution injects.
5. Extension Author runs the sync provider contract tests (include UC-008 Run Contract Tests).
6. System reports that the sync provider meets every case of the sync provider contract.
7. Extension Author tags and publishes the component module under its Go module path and version.
8. The sync provider is available as an extension that a Distribution Builder selects in a manifest by module path and version, exactly like an official sync provider.

## Alternative Flows

### A1: Source Only Answers Requests

**Trigger:** The source announces no changes and can only be asked for its current flag configuration, as most vendor flag services can (step 3)  
**Flow:**

1. Extension Author implements the sync provider as a fetching sync provider: given the document version of its previous fetch, it returns the source's flag configuration with its new document version, or reports that nothing changed.
2. Extension Author leaves scheduling to the core's poller, which fetches at the configured interval with jitter, passes the previous document version for a conditional request, backs off after failed fetches, reports readiness and records sync metrics.
3. Extension Author connects the poller's refetch trigger to a change notice the source sends, if it sends one, such as a vendor's realtime update ping.
4. Use case continues at step 4.

### A2: Scheme Not Accepted

**Trigger:** The contract tests reject a declared scheme because it is empty (step 5)  
**Flow:**

1. System reports the rejected scheme and the scheme rule it breaks.
2. Extension Author corrects the declared schemes.
3. Use case continues at step 5.

### A3: Contract Case Fails

**Trigger:** At least one case of the sync provider contract fails (step 6)  
**Flow:**

1. System reports each failed case with its expected and its observed behaviour.
2. Extension Author corrects the sync provider or its factory.
3. Use case continues at step 5.

### A4: Try Extension in a Distribution Before Publishing

**Trigger:** Extension Author wants to see the extension running in flagd before releasing it (step 7)  
**Flow:**

1. Extension Author builds a custom distribution whose manifest replaces the component module with the local checkout (UC-001 Build Custom Distribution).
2. Extension Author starts the distribution with a source of the new scheme and confirms that flags from that source are served.
3. Use case continues at step 7.

### A5: Extension Author Abandons the Extension

**Trigger:** Extension Author stops before the component module is published (step 7)  
**Flow:**

1. Extension Author leaves the component module unpublished.
2. Use case ends.

## Postconditions

### Success Postconditions

- A tagged version of the component module exists under the Extension Author's Go module path.
- The component module provides exactly one sync provider through a factory of the sync provider component type.
- That version of the sync provider passes every case of the sync provider contract of the core module version it depends on, including the fetching cases when it is a fetching sync provider.

### Failure Postconditions

- No version of the component module is published that fails a case of the sync provider contract.
- No official component module, standard distribution or the core module is changed.

## Business Rules

### BR-001: Core Module Is the Only flagd Dependency

A sync provider component module depends on the core module and on no other flagd module: not on the runtime, a service endpoint, another sync provider or a standard distribution. Its own third-party dependencies are compiled only into distributions that select it.

### BR-002: Schemes Select the Sync Provider

Every declared scheme is a non-empty prefix of a source, such as `consul://` or `core.openfeature.dev/`; it need not be a URI scheme. In a distribution, a source is handed to the sync provider whose scheme it starts with, and one scheme belongs to at most one sync provider of that distribution; a clash between two selected sync providers is reported when the distribution is built (UC-001), not by this use case.

### BR-003: First Delivery as Soon as Possible

The sync provider delivers the source's complete flag configuration as soon as it has read it, because a distribution reaches readiness only after every configured source has delivered a flag configuration that its configuration format accepted, and loses it again when a source's last accepted delivery exceeds the configured staleness limit.

### BR-004: Every Delivery Names Its Source

Each delivery carries the source it came from, so that the runtime attributes flag configurations, readiness and configuration change events to the right source.

### BR-005: Complete Configuration Unless Incremental Updates Are Enabled

Each delivery replaces all flags of its source, unless the Operator has enabled incremental updates for that source; then each delivery is an incremental update and replaces only the flag sets it contains.

### BR-006: Recoverable Source Errors Do Not Stop flagd

A sync provider retries or waits on errors it can recover from, such as an unreachable source, and keeps reporting itself not ready meanwhile; a fetching sync provider reports the failed fetch and the core's poller backs off. A sync provider ends with an error only when it can never deliver again, because that error stops the whole distribution.

### BR-007: Stop on Shutdown

The sync provider stops reading and delivering when the distribution shuts down, and returns without an error; a fetching sync provider abandons a running fetch.

### BR-008: Encoding- and Format-Neutral Delivery

A sync provider does not decode, parse, validate or transform the flag configuration it reads. The core decodes its encoding (JSON, YAML or TOML), and the configuration format the source names validates it, flagd's being the default (FR-029). A sync provider therefore works with every encoding and configuration format and in a configuration proxy, which has no evaluator, and an invalid flag configuration is rejected the same way whichever sync provider delivered it.

### BR-009: Fetching Sync Providers Answer Conditionally

A fetching sync provider reports "not changed" when the source's document version equals the one it was given, and then delivers nothing; the previous flag configuration stays in force. A fetch that fails delivers nothing either. At most one fetch of a source runs at a time; a refetch trigger that fires during a fetch leads to exactly one further fetch after it.

### BR-010: Same Selection as Official Components

An extension is referenced in a manifest by Go module path and version in the same way as an official sync provider; no registration with the Maintainers is needed.
