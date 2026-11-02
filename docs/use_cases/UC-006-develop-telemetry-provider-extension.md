# Use Case: Develop Telemetry Provider Extension

## Overview

**Use Case ID:** UC-006  
**Use Case Name:** Develop Telemetry Provider Extension  
**Primary Actor:** Extension Author  
**Goal:** Publish a telemetry provider in the Extension Author's own component module, verified against the telemetry provider contract, so that a distribution reports the traces and metrics of its service endpoints and middleware to a backend other than OpenTelemetry.  
**Trigger:** Extension Author needs flagd to report to a backend no official telemetry provider supports  
**Status:** Draft  

**Requirements:** [FR-013, FR-015, FR-024, FR-033, FR-003, FR-008, NFR-006](../requirements.md)

## Preconditions

- A release of the core module that contains the telemetry factory interface and the telemetry provider contract tests is available.
- The Extension Author has a Go module of their own in which the extension will live.

## Main Success Scenario

1. Extension Author adds the core module as a dependency of their component module.
2. Extension Author names the telemetry provider's type and the configuration it accepts.
3. Extension Author implements the telemetry provider: it supplies tracing, metrics and HTTP instrumentation to service endpoints and middleware, reports them to the chosen backend, and delivers what it still holds when the distribution shuts down.
4. Extension Author implements the factory that creates the telemetry provider from the dependencies a distribution injects: configuration, logger, and the distribution's name and version. The factory rejects an invalid configuration with an error naming the faulty value.
5. Extension Author runs the telemetry provider contract tests (include UC-008 Run Contract Tests).
6. System reports that the telemetry provider meets every case of the telemetry provider contract.
7. Extension Author tags and publishes the component module under its Go module path and version.
8. The telemetry provider is available as an extension that a Distribution Builder selects in a manifest by module path and version, exactly like an official telemetry provider.

## Alternative Flows

### A1: Contract Case Fails

**Trigger:** At least one case of the telemetry provider contract fails, for example the telemetry provider blocks a request while the backend is slow, or does not return from shutdown (step 6)  
**Flow:**

1. System reports each failed case with its expected and its observed behaviour.
2. Extension Author corrects the telemetry provider or its factory.
3. Use case continues at step 5.

### A2: Try Extension in a Distribution Before Publishing

**Trigger:** Extension Author wants to see the extension running in flagd before releasing it (step 7)  
**Flow:**

1. Extension Author builds a custom distribution whose manifest names the telemetry provider and replaces its component module with the local checkout (UC-001 Build Custom Distribution).
2. Extension Author starts the distribution, evaluates flags through a service endpoint and confirms that the backend receives the evaluation spans and the request metrics.
3. Use case continues at step 7.

### A3: Extension Author Abandons the Extension

**Trigger:** Extension Author stops before the component module is published (step 7)  
**Flow:**

1. Extension Author leaves the component module unpublished.
2. Use case ends.

## Postconditions

### Success Postconditions

- A tagged version of the component module exists under the Extension Author's Go module path.
- The component module provides exactly one telemetry provider through a factory of the telemetry provider component type.
- That version of the telemetry provider passes every case of the telemetry provider contract of the core module version it depends on.

### Failure Postconditions

- No version of the component module is published that fails a case of the telemetry provider contract.
- No official component module, standard distribution or the core module is changed.

## Business Rules

### BR-001: Core Module Is the Only flagd Dependency

A telemetry provider component module depends on the core module and on no other flagd module. Its own third-party dependencies, such as a backend's client library, are compiled only into distributions that select it.

### BR-002: Optional, Noop When Omitted

A distribution contains at most one telemetry provider. A manifest may omit it; the distribution then uses the noop telemetry provider, which records nothing and carries no observability dependency.

### BR-003: Consumers Are Service Endpoints and Middleware

A telemetry provider serves service endpoints and middleware only. The evaluator receives no telemetry provider and emits no telemetry; in-process providers observe evaluations through hooks.

### BR-004: Evaluation Spans Come From Service Endpoints

flagd-full's service endpoints record one evaluation span per flag evaluation through the telemetry provider, with the shape today's evaluator gives it (FR-033). A telemetry provider extension receives these spans like any other and needs no knowledge of the evaluator.

### BR-005: Same Signals in flagd-full

The OpenTelemetry telemetry provider in flagd-full reports the same metric names, attributes and evaluation spans as flagd before the builder. A telemetry provider extension may name them as its backend requires.

### BR-006: Transport Instrumentation Stays Outside the Core Module

Instrumentation of a specific transport, such as gRPC or ConnectRPC, belongs to the service endpoint or telemetry provider module that uses it. The telemetry factory interface offers only transport-neutral tracing, metrics and HTTP instrumentation.

### BR-007: No Telemetry Library Types in the Core Module

The telemetry factory interface uses only types of the Go standard library and of the core module; it exposes no type of OpenTelemetry or any other telemetry library.

### BR-008: Invalid Configuration Stops the Distribution

When the factory rejects the telemetry provider's configuration, the distribution does not start and names the telemetry provider and the faulty value; it does not fall back to the noop telemetry provider.

### BR-009: Telemetry Never Blocks Serving

Once started, a telemetry provider never makes a service endpoint wait for the backend and never fails a request because reporting failed; it logs the reporting error instead.

### BR-010: Same Selection as Official Components

An extension is referenced in a manifest by Go module path and version in the same way as an official telemetry provider; no registration with the Maintainers is needed.
