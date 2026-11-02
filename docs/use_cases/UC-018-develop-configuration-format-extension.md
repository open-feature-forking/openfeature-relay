# Use Case: Develop Configuration Format Extension

## Overview

**Use Case ID:** UC-018  
**Use Case Name:** Develop Configuration Format Extension  
**Primary Actor:** Extension Author  
**Goal:** Publish a configuration format in the Extension Author's own component module, verified against the configuration format contract, so that a distribution reads flag configurations written in another vendor's format, such as Flagsmith's, and keeps them as documents for its paired evaluator and the flag sync endpoint.  
**Trigger:** Extension Author needs flagd to accept flag configurations in a format flagd's own configuration format does not support  
**Status:** Draft  

**Requirements:** [FR-019, FR-015, FR-018, FR-029, FR-044, FR-003, FR-008](../requirements.md)

## Preconditions

- A release of the core module that contains the configuration format factory interface, the store and the configuration format contract tests is available.
- The Extension Author has a Go module of their own in which the extension will live.
- The Extension Author has valid and invalid sample flag configurations in the vendor's format.

## Main Success Scenario

1. Extension Author adds the core module as a dependency of their component module.
2. Extension Author names the configuration format's type, by which a source names it.
3. Extension Author implements the configuration format: it parses a flag configuration in the vendor's format, validates it against the vendor's rules and keeps it in the store as its own documents, or rejects it as a whole with an error naming the faulty entry.
4. Extension Author implements the factory that creates the configuration format from the dependencies a distribution injects: the store, configuration and logger.
5. Extension Author runs the configuration format contract tests with the sample flag configurations (include UC-008 Run Contract Tests).
6. System reports that the configuration format meets every case of the configuration format contract.
7. Extension Author tags and publishes the component module under its Go module path and version.
8. The configuration format is available as an extension that a Distribution Builder selects in a manifest by module path and version, alone in a configuration proxy or together with its paired evaluator, exactly like an official configuration format.

## Alternative Flows

### A1: Contract Case Fails

**Trigger:** At least one case of the configuration format contract fails, for example a valid sample that is rejected, an invalid sample that is accepted, a rejection that does not name the faulty entry, or a rejection that changes the store (step 6)  
**Flow:**

1. System reports each failed case with its expected and its observed behaviour.
2. Extension Author corrects the configuration format or its factory.
3. Use case continues at step 5.

### A2: Try Extension in a Distribution Before Publishing

**Trigger:** Extension Author wants to see the extension running in flagd before releasing it (step 7)  
**Flow:**

1. Extension Author builds a custom distribution whose manifest names the configuration format, with its paired evaluator or with the flag sync endpoint alone, and replaces the configuration format's component module with the local checkout (UC-001 Build Custom Distribution).
2. Extension Author starts the distribution with a source that holds a flag configuration in the vendor's format and names the configuration format.
3. Extension Author confirms that a client receives the flags as the vendor's flag configuration defines them, as evaluation results of the paired evaluator or as documents through a flag sync request that names the configuration format.
4. Use case continues at step 7.

### A3: Source Does Not Name the Configuration Format

**Trigger:** While trying the extension, the source names no configuration format, so flagd's configuration format reads the vendor's flag configuration and rejects it (step 7)  
**Flow:**

1. System logs an error naming the source and the faulty entry, and keeps nothing of that flag configuration in the store.
2. Extension Author names the configuration format on the source and restarts the same distribution; no rebuild is needed.
3. Use case continues at step 7.

### A4: Extension Author Abandons the Extension

**Trigger:** Extension Author stops before the component module is published (step 7)  
**Flow:**

1. Extension Author leaves the component module unpublished.
2. Use case ends.

## Postconditions

### Success Postconditions

- A tagged version of the component module exists under the Extension Author's Go module path.
- The component module provides exactly one configuration format through a factory of the configuration format component type.
- That version of the configuration format passes every case of the configuration format contract of the core module version it depends on.

### Failure Postconditions

- No version of the component module is published that fails a case of the configuration format contract.
- No official component module, standard distribution, the core module or flagd's flag model is changed.

## Business Rules

### BR-001: Accept or Reject as a Whole

A configuration format accepts a flag configuration of a source as a whole or rejects it as a whole. On rejection the store keeps the last flag configuration it accepted from that source.

### BR-002: Own Documents, Read Only by the Paired Evaluator

A configuration format keeps the flag configurations it accepted in the store as its own documents. Only the evaluator paired with it reads them; flagd's flag model and its JsonLogic evaluator never see them. The flag sync endpoint sends them unchanged, and only to a flag sync request that names this configuration format (FR-044, UC-017 BR-004).

### BR-003: Parsing and Validation Belong to the Configuration Format

Only the configuration format parses and validates flag configurations; sync providers deliver them unchanged (UC-003 BR-008), and evaluators and service endpoints read only what the configuration format kept in the store (UC-007 BR-003). The same configuration format therefore serves a distribution with an evaluator and a configuration proxy alike.

### BR-004: A Source Names Its Configuration Format

Each source names the configuration format that reads it; a source that names none is read by flagd's configuration format (FR-029). One distribution can therefore read flagd and vendor flag configurations side by side, and pointing a source at a configuration format the distribution already contains needs no rebuild.

### BR-005: Dependency Boundary

A configuration format module depends on no telemetry provider, service endpoint, sync provider or evaluator module, so that an in-process provider or a configuration proxy can contain it without them.

### BR-006: Rejections Name the Faulty Entry

A rejection names the source and the faulty entry of the flag configuration, so that the Operator can correct the source without reading the configuration format's code.

### BR-007: Same Selection as Official Components

A Distribution Builder selects the extension in a manifest by Go module path and version exactly like an official configuration format, and may replace it with a local checkout (FR-003, FR-008).
