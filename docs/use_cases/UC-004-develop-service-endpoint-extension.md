# Use Case: Develop Service Endpoint Extension

## Overview

**Use Case ID:** UC-004  
**Use Case Name:** Develop Service Endpoint Extension  
**Primary Actor:** Extension Author  
**Goal:** Publish a service endpoint in the Extension Author's own component module, verified against the service endpoint contract, so that a distribution serves flag evaluations or flag configurations to clients over a protocol no official service endpoint supports.  
**Trigger:** Extension Author decides to make flagd serve flags over a protocol that no official service endpoint supports  
**Status:** Implemented  

**Requirements:** [FR-011, FR-015, FR-016, FR-017, FR-018, FR-025, FR-029, FR-035, FR-037, FR-038, FR-039, FR-044, FR-046, FR-047, FR-003, FR-008, NFR-013, C-001](../requirements.md)

## Preconditions

- A release of the core module that contains the service endpoint factory interface, the evaluation router, the event bus and the service endpoint contract tests is available.
- The Extension Author has a Go module of their own in which the extension will live.

## Main Success Scenario

1. Extension Author adds the core module as a dependency of their component module.
2. Extension Author names the service endpoint's type and declares that it resolves flags through the evaluation router.
3. Extension Author implements the service endpoint: it starts serving clients when the distribution starts, resolves the flags of each request for the request's selector through the injected evaluation router, which publishes an evaluation event for each resolved flag on the event bus, answers in its protocol, and stops serving when the distribution shuts down.
4. Extension Author subscribes the service endpoint, through the event bus, to the configuration change events it reacts to, such as a change of the flags its clients have asked for.
5. Extension Author implements the factory that creates the service endpoint from the dependencies a distribution injects: store with document versions, event bus, configuration, logger, telemetry provider and, because it declared that it resolves flags, the evaluation router.
6. Extension Author runs the service endpoint contract tests (include UC-008 Run Contract Tests).
7. System reports that the service endpoint meets every case of the service endpoint contract.
8. Extension Author tags and publishes the component module under its Go module path and version.
9. The service endpoint is available as an extension that a Distribution Builder selects in a manifest by module path and version, exactly like an official service endpoint.

## Alternative Flows

### A1: Endpoint Serves Flag Configurations Without an Evaluator

**Trigger:** The service endpoint forwards flag configurations to clients, such as in-process providers or a vendor's local-evaluation SDKs, instead of evaluating flags (step 2)  
**Flow:**

1. Extension Author declares that the service endpoint resolves no flags.
2. Extension Author implements the service endpoint so that it sends each client, unchanged, the documents of the configuration format the client names in its request, flagd's being the default, with their document versions, and sends them again after every configuration change event for those documents.
3. Use case continues at step 5.

### A2: Endpoint Serves a Vendor's Client API

**Trigger:** The service endpoint answers clients in the shape of one vendor's client API and needs that vendor evaluator's native results, such as a segment or an enabled state (step 2)  
**Flow:**

1. Extension Author declares that the service endpoint needs the evaluator of that vendor's configuration format.
2. Extension Author implements the service endpoint so that it asks the evaluation router for that configuration format's evaluator and builds its answers from that evaluator's own results, and, because it does not resolve through the evaluation router, publishes an evaluation event for each resolved flag itself.
3. Use case continues at step 4.

### A3: Endpoint Collects Events From In-Process Providers

**Trigger:** The service endpoint accepts evaluation and tracking events that in-process providers send instead of resolving flags itself (step 3)  
**Flow:**

1. Extension Author declares that the service endpoint resolves no flags.
2. Extension Author implements the service endpoint so that it accepts batches of evaluation and tracking events from in-process providers and publishes each of them on the event bus, marked as relayed from an in-process provider.
3. Use case continues at step 5.

### A4: Endpoint Announces Changes to Its Clients

**Trigger:** The service endpoint lets clients cache answers or be told about changes, as OFREP does with ETags and its event stream (step 4)  
**Flow:**

1. Extension Author derives the service endpoint's cache validators and change announcements from the document versions of the documents behind the client's selector.
2. Extension Author implements the service endpoint so that it announces a change to a client only when a configuration change event concerns that client's selector.
3. Use case continues at step 5.

### A5: Endpoint Needs No Runtime Events

**Trigger:** The service endpoint answers every request from the evaluation router or the store alone and has nothing to react to (step 4)  
**Flow:**

1. Extension Author subscribes to no event.
2. Use case continues at step 5.

### A6: Endpoint Serves HTTP Clients

**Trigger:** The service endpoint serves its clients over HTTP (step 5)  
**Flow:**

1. Extension Author lets the factory wrap the service endpoint's HTTP handler with the middleware the distribution selects.
2. Extension Author implements the service endpoint so that it takes the selector and request attributes that middleware bound to a request and intersects that selector with the one the client sent: a key only one of them sets is taken from it, and a key both set to different values matches no flag. It hands the result and the attributes to the evaluation router.
3. Use case continues at step 6.

### A7: Contract Case Fails

**Trigger:** At least one case of the service endpoint contract fails (step 7)  
**Flow:**

1. System reports each failed case with its expected and its observed behaviour.
2. Extension Author corrects the service endpoint or its factory.
3. Use case continues at step 6.

### A8: Try Extension in a Distribution Before Publishing

**Trigger:** Extension Author wants to see the extension running in flagd before releasing it (step 8)  
**Flow:**

1. Extension Author builds a custom distribution whose manifest replaces the component module with the local checkout (UC-001 Build Custom Distribution).
2. Extension Author starts the distribution, changes a flag in its source and confirms that a client of the new protocol receives the evaluation result and the change.
3. Use case continues at step 8.

### A9: Extension Author Abandons the Extension

**Trigger:** Extension Author stops before the component module is published (step 8)  
**Flow:**

1. Extension Author leaves the component module unpublished.
2. Use case ends.

## Postconditions

### Success Postconditions

- A tagged version of the component module exists under the Extension Author's Go module path.
- The component module provides exactly one service endpoint through a factory of the service endpoint component type.
- That version of the service endpoint passes every case of the service endpoint contract of the core module version it depends on.
- The runtime needs no knowledge of the service endpoint's type to start it, stop it or notify it of configuration changes.

### Failure Postconditions

- No version of the component module is published that fails a case of the service endpoint contract.
- No official component module, standard distribution or the core module is changed.

## Business Rules

### BR-001: Core Module Is the Only flagd Dependency

A service endpoint component module depends on the core module and on no other flagd module: not on the runtime, another service endpoint, a sync provider or a standard distribution. A vendor client API endpoint may in addition depend on that vendor's engine module. Its own third-party dependencies are compiled only into distributions that select it.

### BR-002: Events Travel Only Over the Event Bus

The event bus carries three kinds of events: configuration change events, which the runtime publishes after a configuration format accepts a changed document; evaluation events, which service endpoints publish for each resolved flag or relay from in-process providers; and tracking events, which service endpoints relay. A service endpoint receives events only by subscribing and sends them only by publishing; the runtime never calls a service endpoint by its type, as it calls the flag sync service today. Configuration change events do not depend on an evaluator, so they also reach the service endpoints of a configuration proxy.

### BR-003: Resolve Through the Evaluation Router

A service endpoint resolves flags only through the evaluation router, which picks for a selector and flag key the configuration format holding the flag and its evaluator; when two sources within the selector hold the same flag key, today's source precedence decides (FR-035). Typed values and type mismatch errors come from the core's typed adapter, so that every service endpoint reports a type mismatch alike. A service endpoint does not create an evaluator, decode, parse or convert flag configurations or read sources itself, so that every service endpoint of a distribution serves the same flags.

### BR-004: Declared Needs

A service endpoint declares what it needs: the evaluation router, the evaluator of one specific configuration format, or neither. The builder rejects a manifest that selects a service endpoint without the evaluators it needs (FR-018); a service endpoint that needs neither runs in a configuration proxy and never receives an evaluator.

### BR-005: Flag Configurations of the Requested Format Only

A service endpoint that serves flag configurations sends the unchanged documents of the one configuration format the client names, flagd's by default, so that today's in-process providers receive only flagd documents (FR-044).

### BR-006: Current State Before Events

A service endpoint that serves flag configurations or change announcements takes the current state and document versions from the store when a client connects, and uses configuration change events only to learn that the state changed. It does not rely on having seen every earlier event.

### BR-007: Versions Come From the Configuration Format

Cache validators, such as ETags, and change announcements, such as event stream events, are derived from the document versions the configuration formats report, never from re-reading or fingerprinting flags, so that they work for every configuration format (FR-046). A validator that also depends on the evaluation context, such as the ETag of a bulk evaluation, may hash the response it validates.

### BR-008: Publishing Never Slows Evaluation

Publishing an evaluation event never blocks or delays the answer to the client. With no event sink selected, it adds no allocation; with sinks selected, a full sink buffer drops the event and counts the drop (NFR-013).

### BR-009: Observability Through the Telemetry Provider

A service endpoint records traces and metrics only through the injected telemetry provider, so that a distribution with the noop telemetry provider carries no observability code for it.

### BR-010: Stop on Shutdown

The service endpoint stops accepting clients when the distribution shuts down, ends its open client connections and returns without an error. An error that prevents it from serving, such as a port already in use, stops the whole distribution.

### BR-011: Same Selection as Official Components

An extension is referenced in a manifest by Go module path and version in the same way as an official service endpoint; no registration with the Maintainers is needed.

## Delivered by Later Use Cases

This use case is `Implemented` for the service endpoint side: the factory, settings and declared needs, the event bus, the runtime registry that serves any set of endpoints, the contract tests, the OFREP event stream and validators from document versions, and `test/service-example` with a test endpoint for a vendor client API. These parts are delivered by the use cases named here, and are accepted gaps of this one:

| Gap | Delivered by |
|-----|--------------|
| Main scenario steps 8–9, A8, BR-011, FR-003, FR-008, the manifest check of FR-018, C-001 | UC-001 |
| BR-009, `Settings.Telemetry`, `/metrics` from the telemetry provider | UC-006 |
| Middleware factories, the chain, the official endpoints honouring a bound selector, FR-047 | UC-005 |
| Event sinks and their flush | UC-020 |
| An official collector endpoint, FR-039 | UC-021 |
| Vendor flags on the flag evaluation event stream | UC-019 |
| Flag sync of vendor documents | UC-017 |
| A webhook fetch trigger | UC-019 |
