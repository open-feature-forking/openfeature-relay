# Use Case: Develop Sync Provider Extension

## Overview

**Use Case ID:** UC-003  
**Use Case Name:** Develop Sync Provider Extension  
**Primary Actor:** Extension Author  
**Goal:** Publish a sync provider in the Extension Author's own component module, verified against the sync provider contract, so that a distribution can read flags from a kind of source no official sync provider supports.  
**Trigger:** Extension Author decides to make flagd read flags from a kind of source that no official sync provider supports  
**Status:** Draft  

**Requirements:** [FR-010, FR-015, FR-003, FR-008, FR-025, C-001](../requirements.md)

## Preconditions

- A release of the core module that contains the sync provider factory interface and the sync provider contract tests is available.
- The Extension Author has a Go module of their own in which the extension will live.

## Main Success Scenario

1. Extension Author adds the core module as a dependency of their component module.
2. Extension Author names the sync provider's type and the schemes of the sources it handles.
3. Extension Author implements the sync provider: it prepares its connection to the source, delivers the source's complete flag configuration as soon as it can, delivers every later change of the source, delivers the complete flag configuration again on request, and reports whether it is ready to watch for changes. It delivers each flag configuration as read and leaves parsing and validating it to the configuration format.
4. Extension Author implements the factory that creates the sync provider for one source from the dependencies a distribution injects.
5. Extension Author runs the sync provider contract tests (include UC-008 Run Contract Tests).
6. System reports that the sync provider meets every case of the sync provider contract.
7. Extension Author tags and publishes the component module under its Go module path and version.
8. The sync provider is available as an extension that a Distribution Builder selects in a manifest by module path and version, exactly like an official sync provider.

## Alternative Flows

### A1: Scheme Not Accepted

**Trigger:** The contract tests reject a declared scheme because it is empty or not a URI prefix (step 5)  
**Flow:**

1. System reports the rejected scheme and the scheme rule it breaks.
2. Extension Author corrects the declared schemes.
3. Use case continues at step 5.

### A2: Contract Case Fails

**Trigger:** At least one case of the sync provider contract fails (step 6)  
**Flow:**

1. System reports each failed case with its expected and its observed behaviour.
2. Extension Author corrects the sync provider or its factory.
3. Use case continues at step 5.

### A3: Try Extension in a Distribution Before Publishing

**Trigger:** Extension Author wants to see the extension running in flagd before releasing it (step 7)  
**Flow:**

1. Extension Author builds a custom distribution whose manifest replaces the component module with the local checkout (UC-001 Build Custom Distribution).
2. Extension Author starts the distribution with a source of the new scheme and confirms that flags from that source are served.
3. Use case continues at step 7.

### A4: Extension Author Abandons the Extension

**Trigger:** Extension Author stops before the component module is published (step 7)  
**Flow:**

1. Extension Author leaves the component module unpublished.
2. Use case ends.

## Postconditions

### Success Postconditions

- A tagged version of the component module exists under the Extension Author's Go module path.
- The component module provides exactly one sync provider through a factory of the sync provider component type.
- That version of the sync provider passes every case of the sync provider contract of the core module version it depends on.

### Failure Postconditions

- No version of the component module is published that fails a case of the sync provider contract.
- No official component module, standard distribution or the core module is changed.

## Business Rules

### BR-001: Core Module Is the Only flagd Dependency

A sync provider component module depends on the core module and on no other flagd module: not on the runtime, a service endpoint, another sync provider or a standard distribution. Its own third-party dependencies are compiled only into distributions that select it.

### BR-002: Schemes Select the Sync Provider

Every declared scheme is a URI prefix such as `consul://`. In a distribution, a source is handed to the sync provider whose scheme its URI starts with, and one scheme belongs to at most one sync provider of that distribution; a clash between two selected sync providers is reported when the distribution is built (UC-001), not by this use case.

### BR-003: First Delivery as Soon as Possible

The sync provider delivers the source's complete flag configuration as soon as it has read it, because a distribution becomes ready only after every configured source has delivered once.

### BR-004: Every Delivery Names Its Source

Each delivery carries the source it came from, so that the runtime attributes flag configurations, readiness and flag-change events to the right source.

### BR-005: Complete Configuration Unless Incremental Updates Are Enabled

Each delivery replaces all flags of its source, unless the Operator has enabled incremental updates for that source; then it replaces only the flag sets it contains.

### BR-006: Recoverable Source Errors Do Not Stop flagd

A sync provider retries or waits on errors it can recover from, such as an unreachable source, and keeps reporting itself not ready meanwhile. It ends with an error only when it can never deliver again, because that error stops the whole distribution.

### BR-007: Stop on Shutdown

The sync provider stops reading and delivering when the distribution shuts down, and returns without an error.

### BR-008: Format-Neutral Delivery

A sync provider does not parse, validate or transform the flag configuration it reads; the configuration format the distribution selects does. A sync provider therefore works with any configuration format and in a configuration proxy, which has no evaluator, and an invalid flag configuration is rejected the same way whichever sync provider delivered it.

### BR-009: Same Selection as Official Components

An extension is referenced in a manifest by Go module path and version in the same way as an official sync provider; no registration with the Maintainers is needed.
