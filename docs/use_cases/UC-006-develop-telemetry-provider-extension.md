# Use Case: Develop Telemetry Provider Extension

## Overview

**Use Case ID:** UC-006  
**Use Case Name:** Develop Telemetry Provider Extension  
**Primary Actor:** Extension Author  
**Goal:** Let flagd report tracing and metrics to an observability backend other than OpenTelemetry by writing a telemetry provider extension in an own component module that a custom distribution can build in.  
**Trigger:** Extension Author needs flagd to report to a backend no official telemetry provider supports  
**Status:** Draft  

**Requirements:** [FR-013, FR-015, FR-024, NFR-006](../requirements.md)

## Preconditions

- A released core module provides the telemetry factory interface.
- The Extension Author has a Go module of their own that depends on the core module.

## Main Success Scenario

1. Extension Author implements the telemetry factory interface in their component module, giving the telemetry provider a component name and the configuration it accepts.
2. Extension Author implements what the telemetry provider supplies: tracing, metrics and HTTP instrumentation for service endpoints and middleware, reported to the chosen backend.
3. Extension Author runs the contract tests for the telemetry provider component type (include UC-008 Run Contract Tests).
4. System reports that the telemetry provider satisfies the telemetry provider contract.
5. Extension Author lists the extension as the telemetry provider in the manifest of a custom distribution, with a local replacement of its component module.
6. System validates the manifest, generates the distribution source and compiles the custom distribution with the telemetry provider.
7. Extension Author starts the custom distribution with the telemetry provider's configuration.
8. System creates the telemetry provider through its factory and hands it to every service endpoint and middleware of the distribution.
9. Extension Author evaluates flags through a service endpoint.
10. System records the requests and evaluations through the telemetry provider, and the backend receives the traces and metrics; the telemetry provider extension works in a custom distribution.
11. Extension Author stops the custom distribution.
12. System shuts the telemetry provider down, which delivers what it still holds to the backend before the distribution exits.

## Alternative Flows

### A1: Contract Violated

**Trigger:** The contract tests report a violation, for example the telemetry provider blocks a request, fails under concurrent use, or does not return from shutdown (step 4)  
**Flow:**

1. System names each violated contract rule and the failing case.
2. Extension Author corrects the telemetry provider.
3. Use case continues at step 3.

### A2: Manifest Invalid

**Trigger:** The manifest names more than one telemetry provider, or the entry for the extension cannot be resolved (step 6)  
**Flow:**

1. System rejects the manifest with an error naming the faulty entry and builds nothing.
2. Extension Author corrects the manifest.
3. Use case continues at step 6.

### A3: Telemetry Provider Configuration Invalid

**Trigger:** The telemetry provider's factory rejects the configuration it is given (step 8)  
**Flow:**

1. System logs an error naming the telemetry provider and the rejected configuration value.
2. System continues with the noop telemetry provider.
3. Use case continues at step 9.

### A4: Backend Unreachable

**Trigger:** The backend does not accept the traces or metrics (step 10)  
**Flow:**

1. System logs the reporting error and keeps serving evaluations without delay.
2. Extension Author restores the backend or corrects its address.
3. Use case continues at step 9.

## Postconditions

### Success Postconditions

- The telemetry provider extension lives in the Extension Author's own component module and passes the telemetry provider contract tests.
- A custom distribution that names the extension in its manifest reports the tracing and metrics of its service endpoints and middleware to the chosen backend.
- No flagd source outside the Extension Author's component module was changed.

### Failure Postconditions

- A failing telemetry provider never stops a distribution from serving flag evaluations.
- A distribution whose manifest does not name the extension is unaffected by it.

## Business Rules

### BR-001: One Telemetry Provider per Distribution

A distribution contains exactly one telemetry provider. When its manifest names none, the distribution uses the noop telemetry provider, which records nothing and carries no observability dependency.

### BR-002: Consumers Are Service Endpoints and Middleware

A telemetry provider serves service endpoints and middleware only. The evaluator receives no telemetry provider and takes no telemetry dependency; observability of evaluation in an in-process provider comes from hooks.

### BR-003: Transport Instrumentation Stays Outside the Core Module

Instrumentation of a specific transport, such as gRPC or ConnectRPC, belongs to the service endpoint or telemetry provider module that uses it. The telemetry factory interface in the core module offers only transport-neutral tracing, metrics and HTTP instrumentation.

### BR-004: Core Module Boundary

The telemetry factory interface uses only types of the Go standard library and of the core module; it exposes no type of OpenTelemetry or any other telemetry library.

### BR-005: Telemetry Never Blocks Serving

A telemetry provider never makes a service endpoint wait for the backend and never fails a request because reporting failed.

### BR-006: Same Signals as Today

The OpenTelemetry telemetry provider in flagd-full reports the same metric names, attributes and spans as flagd before the builder; an extension may choose its own names for its backend.
