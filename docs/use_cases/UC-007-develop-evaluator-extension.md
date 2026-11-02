# Use Case: Develop Evaluator Extension

## Overview

**Use Case ID:** UC-007  
**Use Case Name:** Develop Evaluator Extension  
**Primary Actor:** Extension Author  
**Goal:** Let flagd evaluate flags with another vendor's evaluation logic, such as Flagsmith's, by writing an evaluator extension in an own component module that a custom distribution can build in together with its configuration format.  
**Trigger:** Extension Author needs flags evaluated in a way the JsonLogic evaluator does not support  
**Status:** Draft  

**Requirements:** [FR-014, FR-015, FR-018, FR-040, NFR-005](../requirements.md)

## Preconditions

- A released core module provides the evaluator factory interface, the flag model and the store the evaluator reads from.
- A configuration format exists for the flag configurations the evaluator works with: flagd's own, or an extension written in UC-018 Develop Configuration Format Extension.
- The Extension Author has a Go module of their own that depends on the core module.

## Main Success Scenario

1. Extension Author implements the evaluator factory interface in their component module, giving the evaluator a component name, the configuration format it needs and the configuration it accepts.
2. Extension Author implements the evaluator: it resolves boolean, string, integer, float and object flags, and all flags at once, for an evaluation context from the flags in the store, returning value, variant, reason, metadata and error.
3. Extension Author runs the contract tests for the evaluator component type (include UC-008 Run Contract Tests).
4. System reports that the evaluator satisfies the evaluator contract.
5. Extension Author lists the extension as the evaluator in the manifest of a custom distribution, together with its configuration format, with a local replacement of its component module.
6. System validates the manifest, checks that the evaluator's configuration format is selected, generates the distribution source and compiles the custom distribution with the evaluator.
7. Extension Author starts the custom distribution with a source holding flag configurations in the evaluator's configuration format.
8. System parses and validates the flag configurations with the configuration format into the store, and creates the evaluator through its factory.
9. Extension Author evaluates a flag through a service endpoint.
10. System resolves the flag with the evaluator and returns the result through the service endpoint; the evaluator extension works in a custom distribution.

## Alternative Flows

### A1: Contract Violated

**Trigger:** The contract tests report a violation, for example a wrong reason or error for a missing or disabled flag, a type mismatch that is not reported, or a resolution that sees a half-updated store (step 4)  
**Flow:**

1. System names each violated contract rule and the failing case.
2. Extension Author corrects the evaluator.
3. Use case continues at step 3.

### A2: Manifest Invalid

**Trigger:** The manifest names more than one evaluator, does not select the evaluator's configuration format, or the entry for the extension cannot be resolved (step 6)  
**Flow:**

1. System rejects the manifest with an error naming the faulty entry, and for a missing configuration format the format the evaluator needs, and builds nothing.
2. Extension Author corrects the manifest.
3. Use case continues at step 6.

### A3: Flag Configuration Rejected

**Trigger:** The configuration format rejects the flag configuration from a source (step 8)  
**Flow:**

1. System logs an error naming the source and keeps the last flag configuration it accepted in the store.
2. Extension Author corrects the flag configuration in the source.
3. Use case continues at step 8.

### A4: Flag Not Resolvable

**Trigger:** The flag is missing, disabled, or of a different type than requested (step 10)  
**Flow:**

1. System returns the error code and reason the evaluator gives, in the same form the JsonLogic evaluator gives them.
2. Use case ends.

## Postconditions

### Success Postconditions

- The evaluator extension lives in the Extension Author's own component module and passes the evaluator contract tests.
- A custom distribution that names the extension and its configuration format in its manifest resolves every flag evaluation with it.
- No flagd source outside the Extension Author's component module was changed.

### Failure Postconditions

- No distribution is built with an evaluator whose configuration format it does not select.
- A distribution never resolves flags against a flag configuration its configuration format rejected.
- A distribution whose manifest does not name the extension is unaffected by it.

## Business Rules

### BR-001: At Most One Evaluator per Distribution

A distribution contains at most one evaluator. A distribution without an evaluator, such as a configuration proxy, serves no flag evaluation.

### BR-002: Paired With a Configuration Format

An evaluator names the configuration format it needs, and a distribution with that evaluator always contains that configuration format. The evaluator never parses or validates flag configurations itself; it reads only what the configuration format put into the store.

### BR-003: No Telemetry Dependency

The evaluator factory hands an evaluator no telemetry provider, and an evaluator records no traces or metrics of its own. Service endpoints observe evaluations through the telemetry provider; in-process providers observe them through hooks.

### BR-004: Dependency Boundary

An evaluator module depends on no telemetry, transport or sync provider module, so that an in-process provider can embed it. For the JsonLogic evaluator module this boundary is enforced in CI.

### BR-005: Same Results Contract

An evaluator returns the reasons and error codes of the OpenFeature specification, so that clients cannot tell from the shape of a result which evaluator produced it.

### BR-006: Concurrent Resolution

An evaluator resolves flags concurrently while the store changes; a resolution sees either the flags before or after a change, never a mix.
