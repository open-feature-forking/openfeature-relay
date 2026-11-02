# Use Case: Serve Vendor Flags

## Overview

**Use Case ID:** UC-019  
**Use Case Name:** Serve Vendor Flags  
**Primary Actor:** Operator  
**Secondary Actors:** Vendor Flag Service  
**Goal:** Replace a vendor's own edge proxy, such as the Flagsmith Edge Proxy, with a distribution that reads the vendor's flag configurations and evaluates them with the vendor's evaluator behind OFREP, so that clients get the same results from flagd.  
**Trigger:** Operator decides to replace the vendor's edge proxy of a deployment with flagd  
**Status:** Draft  

**Requirements:** [FR-027, FR-014, FR-019, FR-017, FR-018, FR-022, FR-028](../requirements.md)

## Preconditions

- A custom distribution is available whose manifest selects the vendor's sync provider (UC-003), configuration format (UC-018) and evaluator (UC-007) and the OFREP service endpoint, built in UC-001. For Flagsmith these are a sync provider that fetches the environment document, a configuration format for the environment document and an evaluator built on the Flagsmith engine.
- Operator has the address of the Vendor Flag Service and a credential for the environment the edge proxy serves today.
- The clients of the deployment evaluate flags through OFREP.

## Main Success Scenario

1. Operator starts the custom distribution with a source that names the vendor's environment and its credential.
2. System checks the scheme of the source against the sync providers compiled into the distribution.
3. System starts the vendor's sync provider and the OFREP service endpoint.
4. The sync provider fetches the vendor's flag configuration from the Vendor Flag Service.
5. System parses and validates the flag configuration with the vendor's configuration format and writes the accepted flags into the store.
6. System reports readiness on the management port.
7. Operator points the clients of the deployment at the distribution's OFREP service endpoint in place of the vendor's edge proxy.
8. Operator sends evaluation requests for flags with evaluation contexts that the edge proxy also serves.
9. System resolves each flag with the vendor's evaluator from the flags in the store and returns value, variant, reason and metadata through the OFREP service endpoint.
10. The sync provider fetches the vendor's flag configuration again at its refresh interval and System writes an accepted change into the store.
11. System resolves later evaluation requests against the changed flags.
12. Operator retires the vendor's edge proxy; the deployment's clients get the vendor's flags from flagd with the results the vendor's evaluator gives.

## Alternative Flows

### A1: Scheme Not in Distribution

**Trigger:** The source uses a scheme for which the distribution contains no sync provider (step 2)  
**Flow:**

1. System refuses to start and names the source, its scheme and the schemes the distribution supports.
2. Use case ends.

### A2: Credential Refused

**Trigger:** The Vendor Flag Service refuses the credential (step 4)  
**Flow:**

1. System logs an error naming the source, without the credential, and does not report readiness.
2. Operator corrects the credential.
3. Use case continues at step 1.

### A3: Vendor Flag Service Unavailable

**Trigger:** The Vendor Flag Service does not answer (step 4 or step 10)  
**Flow:**

1. System logs an error naming the source and keeps retrying; until a first flag configuration is accepted it does not report readiness, afterwards it keeps serving the last accepted flags.
2. Use case continues at step 4.

### A4: Flag Configuration Rejected

**Trigger:** The vendor's configuration format rejects the flag configuration (step 5 or step 10)  
**Flow:**

1. System logs an error naming the source and the faulty entry, and keeps the last flag configuration it accepted in the store.
2. Operator reports the rejected flag configuration to the Extension Author of the vendor's configuration format.
3. Use case ends.

### A5: Clients Use the Vendor's Own Interface

**Trigger:** Some clients call the edge proxy through the vendor's own interface instead of OFREP (step 7)  
**Flow:**

1. Operator keeps the edge proxy for these clients until they move to an OpenFeature provider for OFREP, or until a service endpoint extension serves the vendor's interface (UC-004).
2. Use case continues at step 8.

### A6: Flag Not Resolvable

**Trigger:** The flag is missing, disabled, or of a different type than requested (step 9)  
**Flow:**

1. System returns the OpenFeature error code and reason through the OFREP service endpoint.
2. Use case continues at step 8.

### A7: Different Result From the Edge Proxy

**Trigger:** An evaluation result differs from the edge proxy's result for the same flag and evaluation context (step 9)  
**Flow:**

1. Operator keeps the clients on the edge proxy.
2. Operator reports the difference as a defect of the vendor's evaluator or configuration format.
3. Use case ends.

## Postconditions

### Success Postconditions

- The deployment's clients evaluate the vendor's flags through the distribution's OFREP service endpoint, and the vendor's edge proxy is retired.
- Every evaluation returns the result the vendor's evaluator gives for the accepted flag configuration and the evaluation context.
- The store follows changes of the vendor's flag configuration within the sync provider's refresh interval.

### Failure Postconditions

- The distribution never serves flags of a flag configuration the vendor's configuration format rejected.
- The credential of the Vendor Flag Service never appears in a log or a response.
- The vendor's edge proxy is not retired while a client still depends on it.

## Business Rules

### BR-001: Vendor Components as One Set

The vendor's evaluator needs the vendor's configuration format, and the builder rejects a manifest that selects one without the other (FR-018). A distribution contains at most one evaluator, so a distribution serving vendor flags serves no flagd flag configurations alongside them.

### BR-002: The Vendor's Own Evaluation Logic

The vendor's evaluator is built on the vendor's own evaluation logic, for Flagsmith the Flagsmith engine, not on a re-implementation of it. Results match the vendor's edge proxy because the same logic produces them.

### BR-003: OpenFeature Results

The OFREP service endpoint returns the vendor's results with the reasons and error codes of the OpenFeature specification (UC-007 BR-005).

### BR-004: Evaluation Context Mapping

The vendor's evaluator maps the evaluation context onto the vendor's own notions: for Flagsmith the targeting key becomes the identity and the remaining attributes become its traits. An evaluation context without a targeting key evaluates at environment level.

### BR-005: Credential Stays in the Sync Provider

Only the vendor's sync provider uses the credential of the Vendor Flag Service. No service endpoint returns it, and no log contains it.

### BR-006: Extensions, Not Official Components

The vendor's sync provider, configuration format and evaluator are extensions in their own component modules. flagd-full and the other standard distributions do not contain them.
