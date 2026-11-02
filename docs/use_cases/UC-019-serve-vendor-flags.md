# Use Case: Serve Vendor Flags

## Overview

**Use Case ID:** UC-019  
**Use Case Name:** Serve Vendor Flags  
**Primary Actor:** Operator  
**Secondary Actors:** Vendor Flag Service  
**Goal:** Replace a vendor's edge proxy, such as the Flagsmith Edge Proxy, for the deployment's OpenFeature clients with a vendor distribution that reads the vendor's flag configurations and evaluates them with the vendor's evaluator behind OFREP, so that these clients get the same results from flagd.  
**Trigger:** Operator decides to replace the vendor's edge proxy of a deployment with flagd  
**Status:** Draft  

**Requirements:** [FR-027, FR-014, FR-019, FR-017, FR-018, FR-022, FR-028, FR-029, FR-035](../requirements.md)

## Preconditions

- A vendor distribution is available whose manifest selects the vendor's sync provider (UC-003), configuration format (UC-018) and evaluator (UC-007) and the OFREP service endpoint, built in UC-001. For Flagsmith these are a sync provider that fetches the environment document, a configuration format for the environment document and an evaluator built on the Flagsmith engine.
- Operator has the address of the Vendor Flag Service and a credential for the environment the edge proxy serves today.
- The deployment's clients reach the edge proxy as OpenFeature clients through OFREP, or through the vendor's own client interface.

## Main Success Scenario

1. Operator supplies the credential through an environment variable or a file and starts the vendor distribution with a source that names the vendor's environment and the vendor's configuration format.
2. System checks the scheme of the source against the sync providers, and its configuration format against the configuration formats, compiled into the vendor distribution.
3. System starts the vendor's sync provider and the OFREP service endpoint.
4. The sync provider fetches the vendor's flag configuration from the Vendor Flag Service with the credential.
5. System parses and validates the flag configuration with the vendor's configuration format and keeps the accepted flag configuration in the store as that configuration format's documents.
6. System reports readiness on the management port.
7. Operator points the deployment's OpenFeature clients at the vendor distribution's OFREP service endpoint in place of the edge proxy.
8. Operator sends evaluation requests for flags with evaluation contexts that the edge proxy also serves.
9. System resolves each flag with the vendor's evaluator from the vendor's documents in the store and returns value, variant, reason and metadata through the OFREP service endpoint.
10. The sync provider fetches the vendor's flag configuration again at its refresh interval and System keeps an accepted change in the store.
11. System resolves later evaluation requests against the changed flag configuration.
12. Operator retires the edge proxy; the deployment's OpenFeature clients get the vendor's flags from flagd with the results the vendor's evaluator gives.

## Alternative Flows

### A1: Scheme or Configuration Format Not in Distribution

**Trigger:** The source uses a scheme for which the vendor distribution contains no sync provider, or names a configuration format it does not contain (step 2)  
**Flow:**

1. System refuses to start and names the source, its scheme or configuration format, and the schemes and configuration formats the vendor distribution supports.
2. Use case ends.

### A2: Credential Refused

**Trigger:** The Vendor Flag Service refuses the credential (step 4)  
**Flow:**

1. The sync provider ends with an error naming the source, without the credential, because it can never deliver with this credential (UC-003 BR-006).
2. System stops the vendor distribution.
3. Operator corrects the credential in the environment variable or file.
4. Use case continues at step 1.

### A3: Vendor Flag Service Unavailable

**Trigger:** The Vendor Flag Service does not answer (step 4 or step 10)  
**Flow:**

1. System logs an error naming the source and keeps retrying; until a first flag configuration is accepted the vendor distribution does not reach readiness, afterwards it keeps serving the last accepted flag configuration.
2. Use case continues at step 4.

### A4: Flag Configuration Rejected

**Trigger:** The vendor's configuration format rejects the flag configuration (step 5 or step 10)  
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

### A6: Flag Not Resolvable

**Trigger:** The flag is missing, disabled, or of a different type than requested (step 9)  
**Flow:**

1. System returns the OpenFeature error code and reason through the OFREP service endpoint.
2. Use case continues at step 8.

### A7: Different Result From the Edge Proxy

**Trigger:** An evaluation result differs from the edge proxy's result for the same flag and evaluation context (step 9)  
**Flow:**

1. Operator keeps the clients on the edge proxy.
2. Operator reports the difference to the vendor as a defect of the vendor's evaluator or configuration format.
3. Use case ends.

## Postconditions

### Success Postconditions

- The deployment's OpenFeature clients evaluate the vendor's flags through the vendor distribution's OFREP service endpoint, and the edge proxy is retired.
- Every evaluation returns the result the vendor's evaluator gives for the accepted flag configuration and the evaluation context.
- The store follows changes of the vendor's flag configuration within the sync provider's refresh interval.

### Failure Postconditions

- The vendor distribution never serves flags of a flag configuration the vendor's configuration format rejected.
- The credential of the Vendor Flag Service never appears in a log, a response, the manifest or the command line.
- The edge proxy is not retired while a client still depends on it.

## Business Rules

### BR-001: Vendor Components as One Set

The vendor's evaluator needs the vendor's configuration format, and the builder rejects a manifest that selects one without the other (FR-018). A distribution has one evaluator per configuration format, so a vendor distribution may also contain flagd's configuration format and evaluator and serve flagd flags alongside the vendor's.

### BR-002: Each Flag Resolved by Its Format's Evaluator

The OFREP service endpoint resolves each flag with the evaluator of the configuration format whose documents hold it. A flag key held by documents of two sources is resolved from the source with the higher precedence, as today (FR-035).

### BR-003: The Vendor's Own Evaluation Logic

The vendor's evaluator is built on the vendor's own evaluation logic, for Flagsmith the Flagsmith engine, not on a re-implementation of it. Results match the edge proxy because the same logic produces them.

### BR-004: OpenFeature Results

The OFREP service endpoint returns the vendor's results with the reasons and error codes of the OpenFeature specification (UC-007 BR-007).

### BR-005: Evaluation Context Mapping

The vendor's evaluator maps the evaluation context onto the vendor's own notions: for Flagsmith the targeting key becomes the identity and the remaining attributes become its traits. An evaluation context without a targeting key evaluates at environment level.

### BR-006: Credentials Through Environment Variables or Files

The credential of the Vendor Flag Service reaches the sync provider through an environment variable or a file, never through the manifest or the command line (FR-028). Only the vendor's sync provider uses it; no service endpoint returns it and no log contains it.

### BR-007: Owned by the Vendor

The vendor's sync provider, configuration format and evaluator are extensions in component modules the vendor owns. flagd-full and the other standard distributions do not contain them.

### BR-008: Scope Is OpenFeature Clients

A vendor distribution replaces the edge proxy for OpenFeature clients that evaluate through OFREP. Clients of the vendor's own client interface need a service endpoint extension that serves it.
