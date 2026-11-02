# Use Case: Develop Middleware Extension

## Overview

**Use Case ID:** UC-005  
**Use Case Name:** Develop Middleware Extension  
**Primary Actor:** Extension Author  
**Goal:** Add cross-cutting behaviour, such as authentication or rate limits, to flagd's service endpoints by writing a middleware extension in an own component module that a custom distribution can build in.  
**Trigger:** Extension Author decides that flagd needs a cross-cutting behaviour none of the official middleware provides  
**Status:** Draft  

**Requirements:** [FR-012, FR-015](../requirements.md)

## Preconditions

- A released core module provides the middleware factory interface.
- The Extension Author has a Go module of their own that depends on the core module.

## Main Success Scenario

1. Extension Author implements the middleware factory interface in their component module, giving the middleware a component name and the configuration it accepts.
2. Extension Author implements the wrapping behaviour: the middleware receives a service endpoint's HTTP handler and returns a handler that adds the cross-cutting behaviour and passes the request on.
3. Extension Author runs the contract tests for the middleware component type (include UC-008 Run Contract Tests).
4. System reports that the middleware satisfies the middleware contract.
5. Extension Author lists the extension in the manifest of a custom distribution, with a local replacement of its component module.
6. System validates the manifest, generates the distribution source and compiles the custom distribution with the middleware.
7. Extension Author starts the custom distribution with the middleware's configuration.
8. System creates the middleware through its factory and wraps the HTTP handler of every service endpoint that serves over HTTP, in manifest order.
9. Extension Author sends a request to a service endpoint.
10. System applies the middleware's behaviour to the request and the service endpoint answers it; the middleware extension works in a custom distribution.

## Alternative Flows

### A1: Contract Violated

**Trigger:** The contract tests report a violation, for example the middleware does not pass the request on, alters the response of a request it lets through, or fails under concurrent requests (step 4)  
**Flow:**

1. System names each violated contract rule and the failing case.
2. Extension Author corrects the middleware.
3. Use case continues at step 3.

### A2: Middleware Rejects the Request

**Trigger:** The request does not satisfy the middleware's condition, for example missing credentials or an exceeded rate limit (step 10)  
**Flow:**

1. System answers the request on behalf of the middleware with the middleware's rejection, without passing it to the service endpoint.
2. Extension Author confirms that the rejection is the intended behaviour.
3. Use case ends.

### A3: Manifest Invalid

**Trigger:** The manifest entry for the extension cannot be resolved, for example a wrong module path, version or replacement (step 6)  
**Flow:**

1. System rejects the manifest with an error naming the faulty entry and builds nothing.
2. Extension Author corrects the manifest entry.
3. Use case continues at step 6.

### A4: Middleware Configuration Invalid

**Trigger:** The middleware's factory rejects the configuration it is given (step 8)  
**Flow:**

1. System stops the startup with an error naming the middleware and the rejected configuration value; no service endpoint starts.
2. Extension Author corrects the configuration.
3. Use case continues at step 7.

## Postconditions

### Success Postconditions

- The middleware extension lives in the Extension Author's own component module and passes the middleware contract tests.
- A custom distribution that lists the extension in its manifest wraps every HTTP-serving service endpoint with the middleware.
- No flagd source outside the Extension Author's component module was changed.

### Failure Postconditions

- No distribution starts a service endpoint with a middleware whose configuration was rejected.
- A distribution whose manifest does not list the extension is unaffected by it.

## Business Rules

### BR-001: Factory Is the Only Entry Point

A distribution creates a middleware only through its factory; the runtime knows the middleware by its component name, never by its type.

### BR-002: HTTP Service Endpoints Only

A middleware wraps the HTTP handler of a service endpoint. A service endpoint that does not serve over an HTTP handler is not wrapped, and the middleware is not told about it.

### BR-003: Manifest Order

Middleware wrap service endpoints in the order the manifest lists them; the first listed middleware sees the request first.

### BR-004: Pass-Through Contract

A middleware either passes the request on to the wrapped handler unchanged in meaning, or answers it itself; it never answers a request and also passes it on.

### BR-005: Telemetry Through the Telemetry Provider

A middleware that records metrics or traces obtains them from the distribution's telemetry provider through its factory, and depends on no telemetry backend directly. With the noop telemetry provider it records nothing and still works.

### BR-006: Core Module Boundary

The middleware factory interface in the core module uses only Go standard library HTTP types, so a middleware extension pulls no transport or telemetry dependency through the core module.
