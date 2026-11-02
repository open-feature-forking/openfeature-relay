# Use Case: Export Evaluation Data

## Overview

**Use Case ID:** UC-021  
**Use Case Name:** Export Evaluation Data  
**Primary Actor:** Operator  
**Goal:** Deliver every evaluation flagd serves or collects, and every configuration change it accepts, to the analytics, experimentation and audit systems the Operator chooses, without slowing or failing evaluation.  
**Trigger:** Operator decides that a deployment's evaluation and configuration change events must reach external systems  
**Status:** Draft  

**Requirements:** [FR-037, FR-038, FR-039, FR-028, NFR-013](../requirements.md)

## Preconditions

- Operator runs a distribution with the flag evaluation or OFREP service endpoint.
- The external systems that receive the events are reachable from the deployment and have issued the credentials flagd needs.

## Main Success Scenario

1. Operator checks with `flagd components` that the distribution contains an event sink for each external system (UC-011).
2. Operator configures each event sink with its destination and the event kinds it subscribes to: evaluation events, configuration change events or tracking events.
3. Operator supplies each event sink's credentials through environment variables or files.
4. Operator starts flagd with the event sink settings.
5. System checks every event sink setting against the event sinks compiled into the distribution.
6. System starts each event sink and subscribes it to the event bus for its event kinds.
7. A client evaluates flags through the flag evaluation or OFREP service endpoint.
8. System publishes one evaluation event per resolved flag, with flag key, flag set, variant, value, reason, targeting key and remote origin.
9. A source delivers a changed flag configuration and its configuration format accepts it.
10. System publishes a configuration change event naming the source, configuration format, flag set, added, updated and removed flag keys and the new document version.
11. Each event sink buffers the events of its kinds and delivers them to its external system, in batches where the system needs them.
12. Operator confirms that each external system receives the evaluation and configuration change events; flagd now exports its evaluation data continuously.

## Alternative Flows

### A1: Distribution Has No Suitable Event Sink

**Trigger:** No event sink in the distribution delivers to the required external system (step 1)  
**Flow:**

1. Operator asks a Distribution Builder for a custom distribution with a matching event sink (UC-001), or an Extension Author for a new one (UC-020).
2. Use case ends.

### A2: Invalid Event Sink Setting

**Trigger:** A setting names an event sink the distribution does not contain, or an event sink's setting or credential is missing or invalid (step 5)  
**Flow:**

1. System refuses to start and names each faulty setting and the event sink it belongs to.
2. Operator corrects the setting or the credential.
3. Use case continues at step 4.

### A3: Events From In-Process Providers

**Trigger:** Applications evaluate flags with in-process providers, whose evaluations never reach the flag evaluation or OFREP service endpoint (step 7)  
**Flow:**

1. Operator enables the service endpoint that collects provider events and points the in-process providers at it.
2. An in-process provider sends evaluation and tracking events to the service endpoint.
3. System publishes each received event on the event bus with in-process origin.
4. Use case continues at step 9.

### A4: Invalid Provider Event

**Trigger:** The service endpoint that collects provider events receives an event from an in-process provider that lacks a flag key or is malformed (step 7, via A3)  
**Flow:**

1. System rejects the event, tells the sender which event it rejected and publishes nothing for it.
2. Use case continues at step 9.

### A5: Event Sink Buffer Full

**Trigger:** An event sink's buffer is full when a new event of its kinds is published (step 11)  
**Flow:**

1. System drops the event for that event sink, counts the drop and reports the count on the management port.
2. System answers the evaluation without waiting for the event sink.
3. Use case continues at step 11.

### A6: External System Unavailable

**Trigger:** An external system refuses or does not answer a delivery (step 11)  
**Flow:**

1. The event sink keeps the undelivered events in its buffer and retries the delivery.
2. System keeps answering evaluations and keeps reporting readiness unchanged.
3. Use case continues at step 11; once the buffer is full, A5 applies.

## Postconditions

### Success Postconditions

- Each external system receives the evaluation events and configuration change events of the kinds its event sink subscribes to.
- Evaluation events from flagd's service endpoints and from in-process providers reach the same event sinks, marked by origin.

### Failure Postconditions

- No evaluation is delayed, failed or answered differently because of an event sink.
- Every published event is either delivered or counted as dropped; no event is lost without being counted.
- No event sink credential appears in a manifest or on the command line.

## Business Rules

### BR-001: Evaluation Never Waits for an Event Sink

Publishing an evaluation event never blocks evaluation: a full event sink buffer drops the event and counts the drop (NFR-013).

### BR-002: No Event Sink, No Cost

With no event sink selected, publishing evaluation events adds no allocation to the evaluator benchmarks (NFR-013).

### BR-003: One Event per Resolved Flag

The flag evaluation and OFREP service endpoints publish one evaluation event for every flag they resolve, including each flag of a bulk evaluation; the evaluator itself publishes nothing (FR-037).

### BR-004: Change Events Only for Accepted Changes

A configuration change event is published only when a configuration format accepts a changed document; a rejected or unchanged delivery publishes none (FR-038).

### BR-005: Event Sinks Do Not Affect Readiness

The state of an event sink or of its external system never changes readiness.

### BR-006: Credentials Outside the Manifest

Event sink credentials come from environment variables or files, never from the manifest or the command line, as for sources (FR-028).

### BR-007: Event Sinks Cover All Flag Sets

An event sink receives the events of its kinds for every flag set; event sinks per flag set are out of scope for the first release.
