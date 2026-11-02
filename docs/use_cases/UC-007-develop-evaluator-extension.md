# Use Case: Develop Evaluator Extension

## Overview

**Use Case ID:** UC-007  
**Use Case Name:** Develop Evaluator Extension  
**Primary Actor:** Extension Author  
**Goal:** Publish an evaluator in the Extension Author's own component module, verified against the evaluator contract, so that a distribution evaluates flags with another vendor's evaluation logic, such as Flagsmith's, from the documents of its paired configuration format.  
**Trigger:** Extension Author needs flags evaluated in a way the JsonLogic evaluator does not support  
**Status:** Draft  

**Requirements:** [FR-014, FR-015, FR-018, FR-027, FR-033, FR-040, FR-003, FR-008, NFR-005](../requirements.md)

## Preconditions

- A release of the core module that contains the evaluator factory interface and the evaluator contract tests is available.
- A component module of the configuration format the evaluator pairs with is available: flagd's own, or an extension published in UC-018 Develop Configuration Format Extension.
- The Extension Author has a Go module of their own in which the extension will live.

## Main Success Scenario

1. Extension Author adds the core module and the paired configuration format's component module as dependencies of their component module.
2. Extension Author names the evaluator's type and the configuration format it pairs with.
3. Extension Author implements the evaluator: it resolves boolean, string, integer, float and object flags, and all flags at once, for an evaluation context from the documents its configuration format keeps in the store, and returns value, variant, reason, metadata and error.
4. Extension Author implements the factory that creates the evaluator from the dependencies a distribution injects: the store, configuration and logger.
5. Extension Author runs the evaluator contract tests (include UC-008 Run Contract Tests).
6. System reports that the evaluator meets every case of the evaluator contract.
7. Extension Author tags and publishes the component module under its Go module path and version.
8. The evaluator is available as an extension that a Distribution Builder selects in a manifest by module path and version, together with its configuration format, exactly like an official evaluator.

## Alternative Flows

### A1: Paired With flagd's Configuration Format

**Trigger:** The evaluator evaluates flagd's flag model in a new way instead of a vendor's format (step 2)  
**Flow:**

1. Extension Author pairs the evaluator with flagd's configuration format.
2. Extension Author adds the flagd test-harness evaluation suite to the evaluator's contract run.
3. Use case continues at step 3.

### A2: Contract Case Fails

**Trigger:** At least one case of the evaluator contract fails, for example a wrong reason or error code for a missing or disabled flag, an unreported type mismatch, or a resolution that sees a half-updated store (step 6)  
**Flow:**

1. System reports each failed case with its expected and its observed behaviour.
2. Extension Author corrects the evaluator or its factory.
3. Use case continues at step 5.

### A3: Try Extension in a Distribution Before Publishing

**Trigger:** Extension Author wants to see the extension running in flagd before releasing it (step 7)  
**Flow:**

1. Extension Author builds a custom distribution whose manifest names the evaluator with its configuration format and replaces the evaluator's component module with the local checkout (UC-001 Build Custom Distribution).
2. Extension Author starts the distribution with a source in the paired configuration format and confirms that a client receives the expected evaluation results through a service endpoint.
3. Use case continues at step 7.

### A4: Extension Author Abandons the Extension

**Trigger:** Extension Author stops before the component module is published (step 7)  
**Flow:**

1. Extension Author leaves the component module unpublished.
2. Use case ends.

## Postconditions

### Success Postconditions

- A tagged version of the component module exists under the Extension Author's Go module path.
- The component module provides exactly one evaluator through a factory of the evaluator component type, and names the configuration format it pairs with.
- That version of the evaluator passes every case of the evaluator contract of the core module version it depends on.

### Failure Postconditions

- No version of the component module is published that fails a case of the evaluator contract.
- No official component module, standard distribution or the core module is changed.

## Business Rules

### BR-001: At Most One Evaluator per Distribution

A distribution contains at most one evaluator. A distribution without an evaluator, such as a configuration proxy, serves no flag evaluation.

### BR-002: Paired With One Configuration Format

An evaluator names the configuration format it pairs with. The builder rejects a manifest that selects the evaluator without that configuration format (FR-018).

### BR-003: Reads Only Its Format's Documents

Each configuration format keeps its own documents in the store. An evaluator reads only the documents of its paired configuration format and never parses or validates flag configurations itself; flagd's flag model is not changed for it.

### BR-004: Contract Depends on the Format

The evaluator contract cases that do not depend on a format, such as reasons, error codes and concurrent resolution, apply to every evaluator. The flagd test-harness evaluation suite is part of the contract only for evaluators paired with flagd's configuration format.

### BR-005: No Telemetry

The evaluator factory injects no telemetry provider, and an evaluator emits no telemetry. In flagd-full the service endpoints record the evaluation span (FR-033); in-process providers observe evaluations through hooks.

### BR-006: Dependency Boundary

An evaluator module depends on no telemetry, transport or sync provider module, so that an in-process provider can embed it. For the JsonLogic evaluator module this boundary is enforced in CI.

### BR-007: Same Results Contract

An evaluator returns the reasons and error codes of the OpenFeature specification, so that clients cannot tell from the shape of a result which evaluator produced it.

### BR-008: Concurrent Resolution

An evaluator resolves flags concurrently while the store changes; a resolution sees either the documents before or after a change, never a mix.

### BR-009: Same Selection as Official Components

An extension is referenced in a manifest by Go module path and version in the same way as an official evaluator; no registration with the Maintainers is needed.
