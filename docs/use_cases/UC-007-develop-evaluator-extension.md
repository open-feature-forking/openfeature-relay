# Use Case: Develop Evaluator Extension

## Overview

**Use Case ID:** UC-007  
**Use Case Name:** Develop Evaluator Extension  
**Primary Actor:** Extension Author  
**Goal:** Publish an evaluator in the Extension Author's own component module, verified against the evaluator contract, so that a distribution evaluates flags with another vendor's evaluation logic, such as Flagsmith's or GO Feature Flag's, from the documents of its paired configuration format.  
**Trigger:** Extension Author needs flags evaluated in a way the JsonLogic evaluator does not support  
**Status:** Implemented  

**Requirements:** [FR-014, FR-015, FR-018, FR-027, FR-033, FR-035, FR-040, FR-047, FR-048, FR-003, FR-008, NFR-005](../requirements.md)

## Preconditions

- A release of the core module that contains the evaluator factory interface, the shared typed adapter and the evaluator contract tests is available.
- The Extension Author has a Go module of their own in which the extension will live.

## Main Success Scenario

1. Extension Author adds the core module and the component module of the configuration format the evaluator pairs with, flagd's own or one published in UC-018 Develop Configuration Format Extension, as dependencies of their component module.
2. Extension Author names the evaluator's type and the configuration format it pairs with.
3. Extension Author implements the evaluator: for a selector and an evaluation context, it resolves one, several or all flags of the selector from the documents its configuration format keeps in the store, and returns for each flag an untyped result with value, variant, reason, error code, error message and metadata.
4. Extension Author implements the factory that creates the evaluator from the dependencies a distribution injects: the store, configuration and logger.
5. Extension Author runs the evaluator contract tests (include UC-008 Run Contract Tests).
6. System reports that the evaluator meets every case of the evaluator contract.
7. Extension Author tags and publishes the component module under its Go module path and version.
8. The evaluator is available as an extension that a Distribution Builder selects in a manifest by module path and version, together with its configuration format, exactly like an official evaluator.

## Alternative Flows

### A1: Engine Module

**Trigger:** The vendor ships its configuration format and its evaluator in one Go module, as Flagsmith and GO Feature Flag do (step 1)  
**Flow:**

1. Extension Author adds only the core module as a dependency.
2. Extension Author implements the configuration format in the same component module, as UC-018 Develop Configuration Format Extension describes, so that the component module becomes an engine module.
3. Use case continues at step 2.

### A2: Paired With flagd's Configuration Format

**Trigger:** The evaluator evaluates flagd's flag model in a new way instead of a vendor's format (step 2)  
**Flow:**

1. Extension Author pairs the evaluator with flagd's configuration format.
2. Extension Author adds the flagd test harness evaluation suite to the evaluator's contract run.
3. Use case continues at step 3.

### A3: Results Depend on the Credential

**Trigger:** The vendor shows a client fewer flags or other values depending on the kind of credential it uses, as Flagsmith does for client keys (step 3)  
**Flow:**

1. Extension Author implements the evaluator so that it reads the request attributes middleware attached to the request and applies them to its results.
2. Use case continues at step 4.

### A4: Contract Case Fails

**Trigger:** At least one case of the evaluator contract fails, for example an error code outside OpenFeature's set, a reason with parameters, a value for a disabled flag instead of an absent one, or a resolution that sees a half-updated store (step 6)  
**Flow:**

1. System reports each failed case with its expected and its observed behaviour.
2. Extension Author corrects the evaluator or its factory.
3. Use case continues at step 5.

### A5: Try Extension in a Distribution Before Publishing

**Trigger:** Extension Author wants to see the extension running in flagd before releasing it (step 7)  
**Flow:**

1. Extension Author builds a custom distribution whose manifest names the evaluator with its configuration format and replaces the evaluator's component module with the local checkout (UC-001 Build Custom Distribution).
2. Extension Author starts the distribution with a source in the paired configuration format and confirms that a client receives the expected evaluation results through a service endpoint.
3. Use case continues at step 7.

### A6: Extension Author Abandons the Extension

**Trigger:** Extension Author stops before the component module is published (step 7)  
**Flow:**

1. Extension Author leaves the component module unpublished.
2. Use case ends.

## Postconditions

### Success Postconditions

- A tagged version of the component module exists under the Extension Author's Go module path.
- The component module provides exactly one evaluator through a factory of the evaluator component type and names the configuration format it pairs with; an engine module also provides that configuration format.
- That version of the evaluator passes every case of the evaluator contract of the core module version it depends on.

### Failure Postconditions

- No version of the component module is published that fails a case of the evaluator contract.
- No official component module, standard distribution or the core module is changed.

## Business Rules

### BR-001: One Evaluator per Configuration Format

A distribution contains at most one evaluator per configuration format, so flagd's and a vendor's evaluator can run side by side. The flag evaluation and OFREP service endpoints resolve each flag of a request's selector with the evaluator of the configuration format holding it, following today's source precedence when two formats hold the same key within that selector (FR-035). A distribution without an evaluator, such as a configuration proxy, serves no flag evaluation.

### BR-002: Paired With One Configuration Format

An evaluator names the configuration format it pairs with, either in a separate component module or in the same engine module. The builder rejects a manifest that selects the evaluator without that configuration format (FR-018).

### BR-003: Reads Only Its Format's Documents

Each configuration format keeps its own documents in the store. An evaluator reads only the documents of its paired configuration format and never validates flag configurations itself; flagd's flag model is not changed for it.

### BR-004: Untyped Results, Typed Adapter in the Core

An evaluator returns untyped results and receives the type the caller expects only as a hint, so that it can, for example, answer a boolean request with a vendor's enabled state. Type checks and numeric conversion are done by one shared typed adapter in the core module that every service endpoint and in-process provider uses; an evaluator does not repeat them.

### BR-005: Absent Value Means the Caller's Default

When a flag has no value to serve, such as a disabled flag or a flag without a default variant, the evaluator returns no value and the caller's default applies. It never invents a value of the requested type.

### BR-006: One Resolution, Many Flags

Resolving several or all flags of a selector is the evaluator's primary operation; resolving a single flag is the same operation with one flag key. An evaluator prepares shared work, such as segment membership, once per resolution.

### BR-007: Contract Depends on the Format

The evaluator contract cases that do not depend on a format, such as reasons, error codes, absent values and concurrent resolution, apply to every evaluator. The flagd test harness evaluation suite is part of the contract only for evaluators paired with flagd's configuration format; the vendor's own evaluation test data belongs in that evaluator's contract run.

### BR-008: No Telemetry

The evaluator factory injects no telemetry provider, and an evaluator emits no telemetry. The flag evaluation and OFREP service endpoints record the evaluation span whenever the distribution has an OpenTelemetry telemetry provider (FR-033); in-process providers observe evaluations through hooks.

### BR-009: Dependency Boundary

An evaluator module, or an engine module as a whole, depends on no telemetry, transport or sync provider module, so that an in-process provider can embed it. For the JsonLogic evaluator module this boundary is enforced in CI; a configuration proxy with flagd's configuration format stays free of any evaluator, while a vendor configuration proxy carries the vendor's engine module.

### BR-010: Reasons and Error Codes

A result's reason is an OpenFeature reason or a vendor reason without parameters, such as `TARGETING_MATCH_SPLIT`. A result's error code is one of OpenFeature's error codes only; a vendor error without an OpenFeature equivalent becomes `GENERAL` with the vendor's message. Vendor detail, such as the matched segment or rule, goes into the result's metadata.

### BR-011: Concurrent Resolution

An evaluator resolves flags concurrently while the store changes; a resolution sees either the documents before or after a change, never a mix.

### BR-012: Same Selection as Official Components

An extension is referenced in a manifest by Go module path and version in the same way as an official evaluator; no registration with the Maintainers is needed.

## Accepted Test Gaps

- A6: has no system behaviour to test.
- A3: the evaluator half is tested (the request attributes shape the results); the middleware half that attaches the attributes is tested with UC-005.

## Delivered by Later Use Cases

This use case is `Implemented` for the evaluator side: the untyped evaluator interface and factory, the shared typed adapter, the evaluator contract, the evaluation router in flagd's evaluator constructor, the JsonLogic evaluator on the new interface and an example engine module in the configuration format example. These parts are delivered by the use cases named here, and are accepted gaps of this one:

- Main scenario steps 7–8, BR-012, FR-003, FR-008, FR-018's builder check and A5's manifest and `replace` part (tag and publish the module, select it in a manifest by module path and version, build a distribution that names the evaluator and replaces its module locally): UC-001 and UC-012. A5 is tested without a builder: `FromConfig` given the engine's factories, a file source in the paired format, and the flag evaluation and OFREP endpoints on local ports.
- FR-047 and A3's middleware half (middleware attaches the request attributes the evaluator reads): UC-005.
- FR-027 (a distribution that serves another vendor's flags behind OFREP, with the vendor's sync provider, configuration format and evaluator): UC-019.
- Contract packaging across component types is delivered by UC-008, except that the exported flagd suite, `testkit.Run`, runs only inside this repository, because the testbed carries no licence to redistribute it: UC-012, or an upstream change of the testbed that the project lead decides. The evaluator contract itself, `evaluatortest.Run`, is delivered here.
- Whether `NewJSON` over a foreign `IStore` retires, which keeps its per-call reads without the consistency guarantee of BR-011, and the note for embedders that the error text of an unmarshalable context changed from `ERROR` to `GENERAL` (BR-010): #156 (provider migration, after the PoC).
