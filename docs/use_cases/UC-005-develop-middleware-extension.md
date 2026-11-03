# Use Case: Develop Middleware Extension

## Overview

**Use Case ID:** UC-005  
**Use Case Name:** Develop Middleware Extension  
**Primary Actor:** Extension Author  
**Goal:** Publish a middleware in the Extension Author's own component module, verified against the middleware contract, so that a distribution adds cross-cutting behaviour such as authentication, rate limits or binding a credential to a selector to its HTTP service endpoints.  
**Trigger:** Extension Author decides that flagd needs a cross-cutting behaviour none of the official middleware provides  
**Status:** Implemented  

**Requirements:** [FR-012, FR-015, FR-047, FR-003, FR-008, FR-018](../requirements.md)

## Preconditions

- A release of the core module that contains the middleware factory interface and the middleware contract tests is available.
- The Extension Author has a Go module of their own in which the extension will live.

## Main Success Scenario

1. Extension Author adds the core module as a dependency of their component module.
2. Extension Author names the middleware's type and the configuration it accepts.
3. Extension Author implements the middleware: it receives a service endpoint's HTTP handler and returns a handler that adds the cross-cutting behaviour and either passes each request on or answers it itself.
4. Extension Author implements the factory that creates the middleware from the dependencies a distribution injects: configuration, logger and telemetry provider. The factory rejects an invalid configuration with an error naming the faulty value.
5. Extension Author runs the middleware contract tests (include UC-008 Run Contract Tests).
6. System reports that the middleware meets every case of the middleware contract.
7. Extension Author tags and publishes the component module under its Go module path and version.
8. The middleware is available as an extension that a Distribution Builder selects in a manifest by module path and version, exactly like an official middleware.

## Alternative Flows

### A1: Middleware Answers Requests Itself

**Trigger:** The cross-cutting behaviour refuses some requests, such as requests without valid credentials or above a rate limit (step 3)  
**Flow:**

1. Extension Author implements the middleware so that it answers a refused request itself and does not pass it on to the service endpoint.
2. Use case continues at step 4.

### A2: Middleware Binds the Credential to a Selector

**Trigger:** The credential a client sends decides which flags it may see, as a Flagsmith environment key or a GO Feature Flag API key does (step 3)  
**Flow:**

1. Extension Author implements the middleware so that it maps the request's credential to a selector and binds the request to it.
2. Extension Author lets the middleware attach request attributes derived from the credential, such as the kind of credential used, for evaluators that need them.
3. Use case continues at step 4.

### A3: Contract Case Fails

**Trigger:** At least one case of the middleware contract fails (step 6)  
**Flow:**

1. System reports each failed case with its expected and its observed behaviour.
2. Extension Author corrects the middleware or its factory.
3. Use case continues at step 5.

### A4: Try Extension in a Distribution Before Publishing

**Trigger:** Extension Author wants to see the extension running in flagd before releasing it (step 7)  
**Flow:**

1. Extension Author builds a custom distribution whose manifest lists the middleware and replaces its component module with the local checkout (UC-001 Build Custom Distribution).
2. Extension Author starts the distribution and confirms that requests to its HTTP service endpoints pass through the middleware.
3. Use case continues at step 7.

### A5: Extension Author Abandons the Extension

**Trigger:** Extension Author stops before the component module is published (step 7)  
**Flow:**

1. Extension Author leaves the component module unpublished.
2. Use case ends.

## Postconditions

### Success Postconditions

- A tagged version of the component module exists under the Extension Author's Go module path.
- The component module provides exactly one middleware through a factory of the middleware component type.
- That version of the middleware passes every case of the middleware contract of the core module version it depends on.

### Failure Postconditions

- No version of the component module is published that fails a case of the middleware contract.
- No official component module, standard distribution or the core module is changed.

## Business Rules

### BR-001: Core Module Is the Only flagd Dependency

A middleware component module depends on the core module and on no other flagd module. Its own third-party dependencies are compiled only into distributions that select it.

### BR-002: HTTP Service Endpoints Only

A middleware wraps the HTTP handler of every service endpoint that serves over Go's `net/http`: flag evaluation, OFREP and flag sync. It does not cover a service endpoint without an HTTP handler.

### BR-003: Whole Distribution, Manifest Order

Middleware is chosen for the whole distribution, not per service endpoint. Middleware wraps the service endpoints in the order the manifest lists it; the first listed middleware sees a request first.

### BR-004: No Middleware in flagd-full

flagd-full lists no middleware. h2c, CORS and HTTP metrics are layers of the service endpoints, outside the middleware: h2c serves gRPC over plaintext, CORS answers a preflight before any middleware sees it, and the HTTP metrics come from the telemetry provider's HTTP instrumentation at today's positions.

### BR-005: Bound Selector Cannot Be Widened

A selector the middleware binds to a request restricts every evaluation, bulk request and event stream of that request. A selector the client sends itself may narrow it further but never reach flags outside it.

### BR-006: Request Attributes Are Not Evaluation Context

Request attributes travel with the request under a key the core module documents and are read only by evaluators that need them. They are not part of the evaluation context, so a client cannot set or override them.

### BR-007: Pass On or Answer, Never Both

A middleware either passes a request on to the wrapped handler or answers it itself; it never does both for the same request.

### BR-008: Invalid Configuration Stops the Distribution

When the factory rejects the middleware's configuration, the distribution does not start and names the middleware and the faulty value; it never starts its service endpoints without the middleware.

### BR-009: Observability Through the Telemetry Provider

A middleware records traces and metrics only through the injected telemetry provider, so that a distribution with the noop telemetry provider carries no observability code for it.

### BR-010: Standard Library Types Only

The middleware factory interface uses only Go standard library HTTP types, so that a middleware pulls no transport or telemetry dependency through the core module.

### BR-011: Same Selection as Official Components

An extension is referenced in a manifest by Go module path and version in the same way as an official middleware; no registration with the Maintainers is needed.

## Delivered by Later Use Cases

This use case is `Implemented` for the middleware side: the middleware factory interface and the chain in the core module, the middleware contract tests, the official service endpoints wrapped by the configured middleware and honouring the selector it binds, the runtime that stops on an invalid middleware configuration, and `test/middleware-example`, an API key middleware that binds each key to a selector and answers unknown keys with 401 in front of a flagd-full distribution. These parts are delivered by the use cases named here, and are accepted gaps of this one:

| Gap | Delivered by |
|-----|--------------|
| Main scenario steps 7–8, A4, the manifest order of BR-003, BR-011, FR-003, FR-008, the manifest check of FR-018 | UC-001 |
| An evaluator that reads request attributes (BR-006) | UC-019 |
