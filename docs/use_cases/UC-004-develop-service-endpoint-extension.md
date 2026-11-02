# Use Case: Develop Service Endpoint Extension

## Overview

**Use Case ID:** UC-004  
**Use Case Name:** Develop Service Endpoint Extension  
**Primary Actor:** Extension Author  
**Goal:** Publish a service endpoint in the Extension Author's own component module, verified against the service endpoint contract, so that a distribution serves flag evaluations or flag configurations to clients over a protocol no official service endpoint supports.  
**Trigger:** Extension Author decides to make flagd serve flags over a protocol that no official service endpoint supports  
**Status:** Draft  

**Requirements:** [FR-011, FR-015, FR-016, FR-017, FR-018, FR-025, FR-003, FR-008, C-001](../requirements.md)

## Preconditions

- A release of the core module that contains the service endpoint factory interface, the event bus and the service endpoint contract tests is available.
- The Extension Author has a Go module of their own in which the extension will live.

## Main Success Scenario

1. Extension Author adds the core module as a dependency of their component module.
2. Extension Author names the service endpoint's type and declares that it needs an evaluator.
3. Extension Author implements the service endpoint: it starts serving clients when the distribution starts, answers each evaluation request by asking the injected evaluator, and stops serving when the distribution shuts down.
4. Extension Author subscribes the service endpoint, through the event bus, to the runtime events it reacts to, such as flag changes.
5. Extension Author implements the factory that creates the service endpoint from the dependencies a distribution injects: store, evaluator, event bus, configuration, logger and telemetry provider.
6. Extension Author runs the service endpoint contract tests (include UC-008 Run Contract Tests).
7. System reports that the service endpoint meets every case of the service endpoint contract.
8. Extension Author tags and publishes the component module under its Go module path and version.
9. The service endpoint is available as an extension that a Distribution Builder selects in a manifest by module path and version, exactly like an official service endpoint.

## Alternative Flows

### A1: Endpoint Serves Flag Configurations Without an Evaluator

**Trigger:** The service endpoint forwards flag configurations to clients, such as in-process providers, instead of evaluating flags (step 2)
**Flow:**

1. Extension Author declares that the service endpoint needs no evaluator.
2. Extension Author implements the service endpoint so that it sends each client the validated flag configurations from the store, and sends them again after every flag change announced on the event bus.
3. Use case continues at step 5.

### A2: Endpoint Needs No Runtime Events

**Trigger:** The service endpoint answers every request from the evaluator or the store alone and has nothing to react to (step 4)  
**Flow:**

1. Extension Author subscribes to no runtime event.
2. Use case continues at step 5.

### A3: Endpoint Serves HTTP Clients

**Trigger:** The service endpoint serves its clients over HTTP (step 5)  
**Flow:**

1. Extension Author lets the factory wrap the service endpoint's HTTP handler with the middleware the distribution selects.
2. Use case continues at step 6.

### A4: Contract Case Fails

**Trigger:** At least one case of the service endpoint contract fails (step 7)  
**Flow:**

1. System reports each failed case with its expected and its observed behaviour.
2. Extension Author corrects the service endpoint or its factory.
3. Use case continues at step 6.

### A5: Try Extension in a Distribution Before Publishing

**Trigger:** Extension Author wants to see the extension running in flagd before releasing it (step 8)  
**Flow:**

1. Extension Author builds a custom distribution whose manifest replaces the component module with the local checkout (UC-001 Build Custom Distribution).
2. Extension Author starts the distribution, changes a flag in its source and confirms that a client of the new protocol receives the evaluation result and the change.
3. Use case continues at step 8.

### A6: Extension Author Abandons the Extension

**Trigger:** Extension Author stops before the component module is published (step 8)  
**Flow:**

1. Extension Author leaves the component module unpublished.
2. Use case ends.

## Postconditions

### Success Postconditions

- A tagged version of the component module exists under the Extension Author's Go module path.
- The component module provides exactly one service endpoint through a factory of the service endpoint component type.
- That version of the service endpoint passes every case of the service endpoint contract of the core module version it depends on.
- The runtime needs no knowledge of the service endpoint's type to start it, stop it or notify it of flag changes.

### Failure Postconditions

- No version of the component module is published that fails a case of the service endpoint contract.
- No official component module, standard distribution or the core module is changed.

## Business Rules

### BR-001: Core Module Is the Only flagd Dependency

A service endpoint component module depends on the core module and on no other flagd module: not on the runtime, another service endpoint, a sync provider or a standard distribution. Its own third-party dependencies are compiled only into distributions that select it.

### BR-002: Runtime Events Arrive Only Through the Event Bus

The runtime announces flag changes, naming the source that changed, on the event bus after the configuration format has validated the new flag configuration into the store. The event does not depend on an evaluator being selected, so it also reaches the service endpoints of a configuration proxy. A service endpoint learns of them only by subscribing; the runtime never calls a service endpoint by its type, as it calls the flag sync service today.

### BR-003: Serve Only What the Distribution Injects

A service endpoint answers evaluation requests from the injected evaluator and serves flag configurations from the injected store. It does not create an evaluator, parse flag configurations or read sources itself, so that every service endpoint of a distribution serves the same flags.

### BR-004: Declared Need for an Evaluator

A service endpoint declares whether it needs an evaluator. The builder rejects a manifest that selects a service endpoint needing an evaluator without one (FR-018); a service endpoint that declares no need for one runs in a configuration proxy and never receives an evaluator.

### BR-005: Current State Before Events

A service endpoint that serves the complete flag configuration takes the current state from the store when a client connects, and uses events only to learn that the state changed. It does not rely on having seen every earlier event.

### BR-006: Observability Through the Telemetry Provider

A service endpoint records traces and metrics only through the injected telemetry provider, so that a distribution with the noop telemetry provider carries no observability code for it.

### BR-007: Stop on Shutdown

The service endpoint stops accepting clients when the distribution shuts down, ends its open client connections and returns without an error. An error that prevents it from serving, such as a port already in use, stops the whole distribution.

### BR-008: Same Selection as Official Components

An extension is referenced in a manifest by Go module path and version in the same way as an official service endpoint; no registration with the Maintainers is needed.
