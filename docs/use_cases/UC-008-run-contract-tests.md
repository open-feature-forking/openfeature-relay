# Use Case: Run Contract Tests

## Overview

**Use Case ID:** UC-008  
**Use Case Name:** Run Contract Tests  
**Primary Actor:** Extension Author  
**Goal:** Learn, before the component is built into any distribution, whether a component behaves as flagd expects of its component type.  
**Trigger:** Extension Author asks to verify a component against the contract of its component type  
**Status:** Draft  

**Requirements:** [FR-015, NFR-010, C-001](../requirements.md)

## Preconditions

- The component module provides one component through a factory of one component type.
- The component module depends on a release of the core module that contains the contract tests for that component type.

## Main Success Scenario

1. Extension Author adds the contract tests of the component's type to the component module's own tests and hands them the component's factory and the test inputs that component type requires.
2. Extension Author runs the component module's tests.
3. System checks the factory's declarations: a non-empty type name and, where the component type requires them, well-formed schemes and the component types it needs.
4. System creates the component through its factory, injecting test stand-ins for every dependency a distribution would inject.
5. System exercises the component in every case of its component type's contract.
6. System reports, for each contract case, its name and whether it passed.
7. Every case passed, and the Extension Author knows the component meets the contract of the core module version it depends on.

## Alternative Flows

### A1: Test Inputs Missing

**Trigger:** The Extension Author did not supply a test input the component type requires, such as a way to change a sync provider's source (step 1)  
**Flow:**

1. System names the missing test input and the contract cases that need it.
2. Use case ends.

### A2: Declarations Invalid

**Trigger:** The factory declares an empty type name, a scheme that is not a URI prefix or a needed component type that does not exist (step 3)  
**Flow:**

1. System reports the invalid declaration and the rule it breaks.
2. Use case ends.

### A3: Factory Cannot Create the Component

**Trigger:** The factory returns no component or an error for valid dependencies (step 4)  
**Flow:**

1. System reports the factory failure; no contract case is exercised.
2. Use case ends.

### A4: Contract Case Fails

**Trigger:** The component does not show the expected behaviour in a contract case, or does not show it within the case's time limit (step 5)  
**Flow:**

1. System records the case as failed, with the expected and the observed behaviour.
2. System continues with the remaining cases.
3. Use case continues at step 6.

### A5: Report Contains a Failed Case

**Trigger:** At least one contract case failed (step 6)  
**Flow:**

1. System marks the whole run as failed.
2. Use case ends.

## Postconditions

### Success Postconditions

- Every contract case of the component's type passed against this version of the component.
- The run is repeatable: the same component and core module versions give the same result.

### Failure Postconditions

- A run with a failed, missing or not exercised contract case is never reported as passed.
- The component module, the core module and any distribution are left unchanged by the run.

## Business Rules

### BR-001: One Contract per Component Type

The core module provides one set of contract tests per component type, and a component is tested only against the set of its own type. The sets are versioned with the core module, so a component is verified against the contract of the core module version it depends on.

### BR-002: Contract Cases per Component Type

Each set covers what the runtime relies on for that component type; the extension use cases add the details:

| Component Type     | The contract cases verify that the component …                                                                                                                                          |
|--------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Sync Provider      | delivers the source's complete flag configuration first, delivers a change of the source, delivers the complete flag configuration again on request, names its source, delivers the flag configuration unchanged, reports readiness, stops on shutdown (UC-003) |
| Configuration Format | accepts a valid flag configuration into the store, rejects an invalid one naming the fault, leaves the store unchanged after a rejection, replaces only the delivering source's flags (UC-018) |
| Service Endpoint   | starts and stops with the distribution, answers from the injected evaluator or store, runs without an evaluator when it declares no need for one, reacts to a flag change announced on the event bus, stops on shutdown (UC-004) |
| Middleware         | passes requests to the wrapped handler and returns its responses unless the middleware's own behaviour applies (UC-005)                                                                 |
| Telemetry Provider | supplies tracing, metrics and HTTP instrumentation that service endpoints and middleware can use without further setup (UC-006)                                                         |
| Evaluator          | resolves flags as the flagd test-harness evaluation suite expects (UC-007)                                                                                                              |

### BR-003: Only Injected Dependencies

The contract tests create the component only through its factory and give it only the dependencies a distribution injects. A component that needs anything else to pass is not compatible with flagd.

### BR-004: No Running Distribution Needed

The contract tests run inside the component module with the standard Go tooling. They need neither the builder nor a distribution; services the component itself talks to, such as a sync provider's source, are supplied by the Extension Author as test inputs.

### BR-005: Contract Is the Same for Official Components and Extensions

The example component of each factory interface in the flagd repository passes the contract tests of its component type, so the contract is proven on a working component before an Extension Author relies on it.
