# Use Case: Serve Vendor Flags

## Overview

**Use Case ID:** UC-019  
**Use Case Name:** Serve Vendor Flags  
**Primary Actor:** Operator  
**Secondary Actors:** Vendor Flag Service  
**Goal:** Replace a vendor's edge proxy, such as the Flagsmith Edge Proxy or the GO Feature Flag relay proxy, for the deployment's OpenFeature clients with a vendor distribution that reads the vendor's flag configurations for one or several environments and evaluates them with the vendor's evaluator behind OFREP, so that these clients get the same results from flagd.  
**Trigger:** Operator decides to replace the vendor's edge proxy of a deployment with flagd  
**Status:** Implemented  

**Requirements:** [FR-027, FR-014, FR-019, FR-017, FR-018, FR-022, FR-028, FR-029, FR-035, FR-046, FR-047, FR-048, FR-052](../requirements.md)

## Preconditions

- A vendor distribution is available, built in UC-001, whose manifest selects a sync provider for the vendor's sources (UC-003), the vendor's engine module with its configuration format and evaluator (UC-018, UC-007), middleware that derives a selector and request attributes from a client's credential (UC-005) and the OFREP service endpoint. For Flagsmith these are a sync provider that fetches the environment document, and the Flagsmith engine; for GO Feature Flag, flagd's own sync providers for the files the relay proxy reads, and GO Feature Flag's evaluation module.
- Operator has the address of the Vendor Flag Service and a credential for each environment the edge proxy serves today, and the client credentials of each environment.
- The deployment's clients reach the edge proxy as OpenFeature clients through OFREP, or through the vendor's own client interface.

## Main Success Scenario

1. Operator supplies the credential of each environment through an environment variable or a file and starts the vendor distribution with one source per environment, each naming the vendor's configuration format.
2. System checks the scheme of every source against the sync providers, and its configuration format against the configuration formats, compiled into the vendor distribution.
3. System starts a sync provider for each source, the middleware and the OFREP service endpoint.
4. Each sync provider fetches the vendor's flag configuration of its environment from the Vendor Flag Service with that environment's credential.
5. System validates each flag configuration with the vendor's configuration format and keeps it in the store as that configuration format's documents, one flag set per environment.
6. System reports readiness on the management port.
7. Operator configures the middleware to map each client credential to the selector of its environment, and points the deployment's OpenFeature clients at the vendor distribution's OFREP service endpoint in place of the edge proxy.
8. Operator sends evaluation requests with client credentials of each environment for flags and evaluation contexts that the edge proxy also serves.
9. The middleware binds each request to the selector of its credential's environment and to request attributes, such as the kind of credential.
10. System resolves each flag of the request's selector with the vendor's evaluator, which honours the request attributes, and returns value, variant, reason and metadata through the OFREP service endpoint.
11. A sync provider fetches a changed flag configuration of its environment and System keeps the accepted change in the store; the OFREP service endpoint announces the change on the event stream of that environment's selector.
12. System resolves later evaluation requests against the changed flag configuration.
13. Operator retires the edge proxy; the deployment's OpenFeature clients get the vendor's flags of their environment from flagd with the results the vendor's evaluator gives.

## Alternative Flows

### A1: Scheme Not in Distribution

**Trigger:** A source uses a scheme for which the vendor distribution contains no sync provider (step 2)  
**Flow:**

1. System refuses to start and names the source, its scheme and the schemes the vendor distribution supports.
2. Use case ends.

### A2: Credential Refused

**Trigger:** The Vendor Flag Service refuses the credential of an environment (step 4)  
**Flow:**

1. When no flag configuration of the source has been accepted yet, the sync provider ends with an error naming the source, without the credential, because it can never deliver with this credential (UC-003 BR-008). After a first flag configuration was accepted, a refusal is logged without the credential, and the sync provider keeps serving the last accepted flag configuration and retries as in A3, so that a rotated key or a refusal by a gateway in front of the Vendor Flag Service does not stop every environment; the staleness limit takes the vendor distribution out of readiness, and steps 2 to 4 do not apply.
2. System stops the vendor distribution.
3. Operator corrects the credential in the environment variable or file.
4. Use case continues at step 1.

### A3: Vendor Flag Service Unavailable or Source Stale

**Trigger:** The Vendor Flag Service does not answer (step 4 or step 11)  
**Flow:**

1. System logs an error naming the source and the sync provider keeps retrying.
2. Until a first flag configuration of the source is accepted, the vendor distribution does not reach readiness.
3. Afterwards System keeps serving the last accepted flag configuration; once the source's staleness exceeds the configured limit, readiness fails until a new delivery is accepted, so that a load balancer stops routing to the vendor distribution (FR-052).
4. Use case continues at step 4.

### A4: Flag Configuration Rejected

**Trigger:** The vendor's configuration format rejects the flag configuration (step 5 or step 11)  
**Flow:**

1. System logs an error naming the source and the faulty entry, and keeps the last flag configuration it accepted in the store.
2. Operator reports the rejected flag configuration to the vendor as the owner of the configuration format.
3. Use case ends.

### A5: Clients Use the Vendor's Own Client Interface

**Trigger:** Some clients call the edge proxy through the vendor's own client interface instead of OFREP (step 7)  
**Flow:**

1. Operator has the vendor distribution rebuilt with a service endpoint extension that serves the vendor's client interface (UC-004, UC-001), or keeps the edge proxy for these clients until they move to OpenFeature.
2. Operator points the clients served by the service endpoint extension at the vendor distribution.
3. Use case continues at step 8.

### A6: Unknown Client Credential

**Trigger:** A request carries no credential, or one the middleware maps to no environment (step 9)  
**Flow:**

1. The middleware refuses the request as unauthorized; no flag is resolved.
2. Use case continues at step 8.

### A7: Flag Not Resolvable

**Trigger:** The flag is missing from the request's selector, disabled, hidden from the request's kind of credential, or of a different type than requested (step 10)  
**Flow:**

1. System returns the OpenFeature error code and reason through the OFREP service endpoint.
2. Use case continues at step 8.

### A8: Different Result From the Edge Proxy

**Trigger:** An evaluation result differs from the edge proxy's result for the same flag, credential and evaluation context (step 10)  
**Flow:**

1. Operator keeps the clients of that environment on the edge proxy.
2. Operator reports the difference to the vendor as a defect of the vendor's evaluator or configuration format.
3. Use case ends.

### A9: Configuration Format Not in Distribution

**Trigger:** A source names a configuration format the vendor distribution does not contain (step 2)  
**Flow:**

1. System skips the source, logs an error naming the source and the configuration formats the vendor distribution contains, and leaves the source out of readiness (UC-018 BR-011).
2. Use case continues at step 3 with the other sources.

## Postconditions

### Success Postconditions

- The deployment's OpenFeature clients evaluate the vendor's flags of their own environment through the vendor distribution's OFREP service endpoint, and the edge proxy is retired.
- Every evaluation returns the result the vendor's evaluator gives for the accepted flag configuration of the request's environment, the evaluation context and the request attributes.
- The store follows changes of each environment's flag configuration within its sync provider's refresh interval.

### Failure Postconditions

- The vendor distribution never serves flags of a flag configuration the vendor's configuration format rejected.
- A request is never answered with flags of an environment other than the one its credential maps to.
- No credential of the Vendor Flag Service appears in a log, a response, the manifest or the command line.
- The edge proxy is not retired while a client still depends on it.

## Business Rules

### BR-001: Vendor Components as One Set

The vendor's evaluator needs the vendor's configuration format, and the builder rejects a manifest that selects one without the other (FR-018); an engine module provides both (FR-048). A distribution has one evaluator per configuration format, so a vendor distribution may also contain flagd's configuration format and evaluator and serve flagd flags alongside the vendor's.

### BR-002: Routing by Selector and Flag Key

The OFREP service endpoint resolves each flag of a request's selector with the evaluator of the configuration format whose documents hold it within that selector. A flag key held by documents of two sources within the selector is resolved from the source with the higher precedence, as today (FR-035). Two environments of one vendor project hold the same flag keys, so a key is never resolved without the selector that picks its environment.

### BR-003: Selector From the Client's Credential

The middleware binds every request to the selector of the environment its client credential maps to (FR-047). A client cannot reach another environment's flags by sending a selector of its own.

### BR-004: Credential-Dependent Results Through Request Attributes

When the vendor's results depend on the kind of credential, the middleware attaches it as a request attribute and the vendor's evaluator honours it: a Flagsmith client credential does not see server-only features and, where the environment says so, disabled flags. Request attributes are not part of the evaluation context.

### BR-005: The Vendor's Own Evaluation Logic

The vendor's evaluator is built on the vendor's own evaluation logic, for Flagsmith the Flagsmith engine and for GO Feature Flag its evaluation module, not on a re-implementation of it. Where the edge proxy evaluates with another implementation of that logic, results match only as far as both agree: the Flagsmith Edge Proxy evaluates with Flagsmith's Python engine, the vendor distribution with its Go engine, so the vendor's shared engine test data belongs in that evaluator's contract cases.

### BR-006: OpenFeature Results

The OFREP service endpoint returns the vendor's results with the reasons and error codes of the OpenFeature specification (UC-007 BR-007), and serves ETags and the event stream for the vendor's documents as for flagd's (FR-046).

### BR-007: Evaluation Context Mapping

The vendor's evaluator maps the evaluation context onto the vendor's own notions: for Flagsmith the targeting key becomes the identity and the remaining attributes become its traits. An evaluation context without a targeting key evaluates at environment level.

### BR-008: Credentials Through Environment Variables or Files

The credential of each environment at the Vendor Flag Service reaches its sync provider through an environment variable or a file, never through the manifest or the command line (FR-028). Only the vendor's sync provider uses it; no service endpoint returns it and no log contains it.

### BR-009: Owned by the Vendor

The vendor's engine module and any vendor-specific sync provider or service endpoint are extensions in component modules the vendor owns. flagd-full and the other standard distributions do not contain them.

### BR-010: Scope Is OpenFeature Clients

A vendor distribution replaces the edge proxy for OpenFeature clients that evaluate through OFREP. Clients of the vendor's own client interface need a service endpoint extension that serves it.

## Inherited Gaps

- A webhook fetch trigger for a source, alongside server-sent events and vendor change events: it is not a service endpoint, because the event bus has no "fetch now" event and a trigger runs per source (moved here from UC-003 and UC-004).

## Delivered by Later Use Cases

This use case is `Implemented` as a small validation that the relay serves a vendor's flags: the Flagsmith distribution (`distributions/flagsmith`) fetches the environment document of an environment with its key from an environment variable, keeps it as the Flagsmith configuration format's document, resolves it with Flagsmith's own Go engine and serves it through OFREP, as an end-to-end test shows for an environment and for an identity against Flagsmith's engine. It is not a feature-complete replacement of the Flagsmith Edge Proxy. These parts are accepted gaps of this use case:

- The credential middleware in the vendor distribution (main steps 7 and 9, BR-003, BR-004 for client keys, A6, and the part of A7 that hides a flag from the kind of credential): the middleware module exists (`contrib/flagsmith/middleware`), but the command line of flagd has no setting for middleware, so the distribution lists none and a request picks its environment with the `Flagd-Selector` header. Until the operator settings for middleware land, run the distribution only behind something that authenticates its clients: UC-005 and step 10's E1 (#127).
- The comparison with the Flagsmith Edge Proxy (main step 13, A8, BR-005's Edge Proxy half): a recorded oracle and a live differential against the Edge Proxy image: #94.
- End-to-end scenarios for paginated environment documents, an unavailable Vendor Flag Service and a stream that is down (A3, and the SSE half of main step 11): the fetcher and the SSE trigger have their own tests, the scenarios in the built distribution are #94.
- The test case under `docs/test_cases/` that chains UC-001, UC-003, UC-005 and this use case: #94.
- Flagsmith's own service endpoint for SDKs in local evaluation, and the Edge Proxy's configuration and health endpoints (A5, BR-010): #94.
- GO Feature Flag (the second vendor named in the goal and in the preconditions): a later slice. FR-027 stays `In Progress` until then.
- Hiding the features that only a server-side key may see (BR-004): the environment document has no marker for them.
- A FIPS 140-3 variant of the Flagsmith distribution: the engine places an identity in a percentage split with MD5.
