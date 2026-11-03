# Use Case: Run Contract Tests

## Overview

**Use Case ID:** UC-008  
**Use Case Name:** Run Contract Tests  
**Primary Actor:** Extension Author  
**Goal:** Learn, before the component is built into any distribution, whether a component behaves as flagd expects of its component type.  
**Trigger:** Extension Author asks to verify a component against the contract of its component type  
**Status:** Implemented  

**Requirements:** [FR-015, FR-014, FR-036, FR-045, FR-054, FR-055, NFR-013, C-001](../requirements.md)

## Preconditions

- The component module provides one component through a factory of one component type.
- The component module depends on a release of the core module that contains the contract tests for that component type.

## Main Success Scenario

1. Extension Author adds the contract tests of the component's type to the component module's own tests and hands them the component's factory and the test inputs that component type requires.
2. Extension Author runs the component module's tests.
3. System checks the factory's declarations: a non-empty type name and, where the component type requires them, non-empty schemes (fetchers and streaming sync providers) and the components it needs, each named by component type or as one specific component, such as an evaluator's paired configuration format.
4. System creates the component through its factory, injecting test stand-ins for every dependency a distribution would inject.
5. System exercises the component in every case of its component type's contract.
6. System reports, for each contract case, its name and whether it passed.
7. Every case passed, and the Extension Author knows the component meets the contract of the core module version it depends on.

## Alternative Flows

### A1: Test Inputs Missing

**Trigger:** The Extension Author did not supply a test input the component type requires, such as a way to change a sync provider's source (step 1)  
**Flow:**

1. System names the missing test input and the contract cases that need it. An optional feature of the component type counts as a missing test input unless the Extension Author named it as not applicable (BR-006).
2. Use case ends.

### A2: Declarations Invalid

**Trigger:** The factory declares an empty type name, an empty scheme, or a need that names neither an existing component type nor a specific component (step 3)  
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

- A run with a failed or missing contract case, or a case that was not exercised and that the Extension Author did not declare not applicable, is never reported as passed.
- The component module, the core module and any distribution are left unchanged by the run.

## Business Rules

### BR-001: One Contract per Component Type

The core module provides one set of contract tests per component type, and a component is tested only against the set of its own type. The sets are versioned with the core module, so a component is verified against the contract of the core module version it depends on.

### BR-002: Contract Cases per Component Type

Each set covers what the runtime relies on for that component type; the extension use cases add the details:

| Component Type     | The contract cases verify that the component …                                                                                                                                          |
|--------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Fetcher            | fetches once per request and returns the flag configuration unchanged with its source version, reports "not changed" when given the current source version, returns a new source version after the source changed, reports a failed fetch without returning a flag configuration, abandons a running fetch on shutdown (UC-003) |
| Fetch Trigger      | signals its source's fetcher each time the change signal arrives and never fetches or delivers itself, reconnects after losing the change signal, stops listening on shutdown (UC-003 A2); that a failing fetch trigger leaves the other fetch triggers of its source running is the fetching sync provider's, and is tested with it (UC-003 BR-003) |
| Streaming Sync Provider | delivers the source's complete flag configuration first, delivers each change the source pushes, delivers the complete flag configuration again on request, names its source, delivers the flag configuration unchanged, reports readiness, reconnects after losing the source, stops on shutdown (UC-003 A1) |
| Configuration Format | accepts a valid flag configuration into the store as its own documents, reports each document's flag keys, flag set and document version and the added, updated and removed flag keys against the previous document, rejects an invalid one naming the faulty entry, leaves the store unchanged after a rejection, replaces only the delivering source's flags, or only the flag sets of an incremental update (UC-018) |
| Service Endpoint   | starts and stops with the distribution, resolves flags only through the evaluation router for the request's selector, publishes one evaluation event per resolved flag, serves only the documents of the configuration format a client requests, derives cache validators and change announcements from document versions, runs without evaluators when it declares no need for them, reacts to a configuration change event, stops on shutdown (UC-004) |
| Middleware         | passes requests to the wrapped handler and returns its responses unchanged unless the middleware's own behaviour applies, and does so under concurrent requests (UC-005) |
| Telemetry Provider | supplies tracing, metrics and HTTP instrumentation that service endpoints and middleware can use without further setup, never blocks a request, works under concurrent use and returns from shutdown (UC-006) |
| Evaluator          | resolves one, several or all flags of a selector in one request and returns an untyped result per flag, with no value where the caller's default applies; the core's typed adapter turns these results into correct typed resolutions; every reason is an OpenFeature reason or a vendor reason without parameters, and every error code is one of OpenFeature's; it resolves concurrently while the store changes without seeing a half-updated store, and, when paired with flagd's configuration format, resolves as the flagd test-harness evaluation suite expects (UC-007) |
| Event Sink         | receives the events it subscribed to, delivers them to its external system in batches, drops and counts events when its buffer is full instead of blocking the publisher, flushes buffered events on shutdown and returns from shutdown (UC-020) |

### BR-003: Only Injected Dependencies

The contract tests create the component only through its factory and give it only the dependencies a distribution injects. A component that needs anything else to pass is not compatible with flagd.

### BR-004: No Running Distribution Needed

The contract tests run inside the component module with the standard Go tooling. They need neither the builder nor a distribution; services the component itself talks to, such as a sync provider's source, are supplied by the Extension Author as test inputs.

### BR-005: Contract Is the Same for Official Components and Extensions

The example component of each factory interface in the flagd repository passes the contract tests of its component type, so the contract is proven on a working component before an Extension Author relies on it.

### BR-006: Optional Features Are Declared

A component type's optional feature, such as a service endpoint's documents or an evaluator's disabled flag, is either supplied through its test input or named by the Extension Author as not applicable. The case behind a named feature is reported as not applicable, with the feature's name, never left out unseen. A feature that is neither supplied nor named fails its case, naming the test input. A feature that is both supplied and named, or one the contract does not have, is a mistake of the test inputs and no case is run. A component that differs in what a case asserts, such as one without configuration, declares it as a mode, and the case states the mode in its expected behaviour.

## Delivered by Later Use Cases

This use case is `Implemented`: every component type has a contract runner in the core module with the same entry point and report, the example modules run them, and `make test-contract-external` runs them from the module cache outside the repository. These parts are delivered by the use cases named here, and are accepted gaps of this one:

- The evaluator contract for flagd's configuration format, the testbed's evaluator suite, from a module outside this repository: the suite is a pinned submodule of the testbed, which carries no licence to redistribute it, so `test/evaluator-testkit` runs only inside this repository. UC-012, together with releasing the modules, or an upstream change of the testbed that the project lead decides.
- Release tags of the core module, behind BR-001's "a release of the core module that contains the contract tests": UC-012. Until then the external run fetches the core module at a commit.
- Building the example components into a distribution and starting it (NFR-010): UC-001.
- That a fetch trigger fetches nothing of its source on its own beyond signalling is checked as far as the signal goes; a trigger that reads the source without signalling cannot be seen by a contract run.
- The names this use case changed go into the migration guide of UC-015: the `Case` and `Report` types of the runner packages, `servicetest.Result`, `ValidateDeclaration` of the telemetry, event sink and middleware factories, `Run` of the format and evaluator runners taking `*testing.T`, and the samples a format or evaluator leaves out becoming `Without` entries.
