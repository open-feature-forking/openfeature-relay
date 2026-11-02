# Use Case: Develop Event Sink Extension

## Overview

**Use Case ID:** UC-020  
**Use Case Name:** Develop Event Sink Extension  
**Primary Actor:** Extension Author  
**Goal:** Publish an event sink in the Extension Author's own component module, verified against the event sink contract, so that a distribution delivers configuration change, evaluation or tracking events to an external system flagd does not support, such as a vendor's analytics API, GO Feature Flag's exporters or a change notification channel.  
**Trigger:** Extension Author needs flagd's events delivered to a system no official event sink supports  
**Status:** Draft  

**Requirements:** [FR-036, FR-015, FR-037, FR-038, FR-039, FR-003, FR-008, NFR-013](../requirements.md)

## Preconditions

- A release of the core module that contains the event sink factory interface, the event types and the event sink contract tests is available.
- The Extension Author has a Go module of their own in which the extension will live.

## Main Success Scenario

1. Extension Author adds the core module as a dependency of their component module.
2. Extension Author names the event sink's type, the event kinds it subscribes to (configuration change events, evaluation events, tracking events) and the configuration it accepts.
3. Extension Author implements the delivery: the event sink receives a batch of events and delivers it to the external system, reporting whether the delivery succeeded.
4. Extension Author implements the factory that creates the event sink from the dependencies a distribution injects: configuration, logger and telemetry provider. The factory rejects an invalid configuration with an error naming the faulty value.
5. Extension Author runs the event sink contract tests (include UC-008 Run Contract Tests).
6. System reports that the event sink meets every case of the event sink contract: it receives events in batches, a full buffer drops and counts events instead of blocking the publisher, and a shutdown delivers the events still buffered.
7. Extension Author tags and publishes the component module under its Go module path and version.
8. The event sink is available as an extension that a Distribution Builder selects in a manifest by module path and version, exactly like an official event sink.

## Alternative Flows

### A1: Event Sink Notifies of Configuration Changes Only

**Trigger:** The event sink tells people or systems about flag changes, as GO Feature Flag's change notifications do, rather than exporting evaluation data (step 2)  
**Flow:**

1. Extension Author subscribes the event sink to configuration change events only.
2. Extension Author implements the delivery so that each configuration change event names the source, flag set, and added, updated and removed flag keys in the external system's terms.
3. Use case continues at step 4.

### A2: External System Refuses or Does Not Answer

**Trigger:** The event sink reports a failed delivery because the external system is unavailable or refuses the batch (step 3)  
**Flow:**

1. Extension Author distinguishes in the reported failure whether the batch may be delivered again.
2. Extension Author leaves the repetition to the core module's retry with backoff, which drops and counts the batch once its retries are exhausted.
3. Use case continues at step 4.

### A3: Contract Case Fails

**Trigger:** At least one case of the event sink contract fails, for example the event sink blocks the publisher while the external system is slow, loses buffered events on shutdown, or does not return from shutdown in time (step 6)  
**Flow:**

1. System reports each failed case with its expected and its observed behaviour.
2. Extension Author corrects the event sink or its factory.
3. Use case continues at step 5.

### A4: Try Extension in a Distribution Before Publishing

**Trigger:** Extension Author wants to see the extension running in flagd before releasing it (step 7)  
**Flow:**

1. Extension Author builds a custom distribution whose manifest lists the event sink and replaces its component module with the local checkout (UC-001 Build Custom Distribution).
2. Extension Author starts the distribution, evaluates flags and changes a flag in its source, and confirms that the external system receives the evaluation and configuration change events.
3. Use case continues at step 7.

### A5: Extension Author Abandons the Extension

**Trigger:** Extension Author stops before the component module is published (step 7)  
**Flow:**

1. Extension Author leaves the component module unpublished.
2. Use case ends.

## Postconditions

### Success Postconditions

- A tagged version of the component module exists under the Extension Author's Go module path.
- The component module provides exactly one event sink through a factory of the event sink component type.
- That version of the event sink passes every case of the event sink contract of the core module version it depends on.

### Failure Postconditions

- No version of the component module is published that fails a case of the event sink contract.
- No official component module, standard distribution or the core module is changed.

## Business Rules

### BR-001: Core Module Is the Only flagd Dependency

An event sink component module depends on the core module and on no other flagd module. Its own third-party dependencies, such as a vendor's API client, are compiled only into distributions that select it.

### BR-002: The Core Buffers, the Event Sink Delivers

The core module gives each event sink a bounded buffer, batches its events, flushes a batch by size or age, and retries failed deliveries with backoff. The event sink implements only the delivery of a batch to its external system.

### BR-003: Never Block Evaluation

Publishing an event never waits for an event sink. When an event sink's buffer is full, new events for it are dropped and counted, and the count is reported through the telemetry provider; evaluation goes on unchanged.

### BR-004: No Cost Without Event Sinks

A distribution without an event sink publishes no evaluation events and adds no allocation to flag evaluation.

### BR-005: Flush on Shutdown

When the distribution shuts down, the core module flushes every event sink's buffer once and the event sink delivers it within the shutdown time limit; events still undelivered when the limit expires are counted as dropped.

### BR-006: Events Come From the Runtime and Service Endpoints

Configuration change events come from the runtime after a configuration format accepts a changed document. Evaluation events come from the flag evaluation and OFREP service endpoints, and, with tracking events, from a service endpoint that collects them from in-process providers. The evaluator publishes no events.

### BR-007: Product Data, Not Telemetry

An event sink carries product data that must be delivered, such as experiment exposure, analytics or audit, and is a component type of its own. It never replaces the telemetry provider, and the telemetry provider never delivers events.

### BR-008: All Events of the Subscribed Kinds

An event sink receives every event of the kinds it subscribes to, across all flag sets. An event sink that serves only some flag sets selects them itself from the flag set each event names.

### BR-009: Invalid Configuration Stops the Distribution

When the factory rejects the event sink's configuration, the distribution does not start and names the event sink and the faulty value.

### BR-010: Same Selection as Official Components

An extension is referenced in a manifest by Go module path and version in the same way as an official event sink; no registration with the Maintainers is needed.
