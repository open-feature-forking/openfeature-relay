# Use Case: Develop Configuration Format Extension

## Overview

**Use Case ID:** UC-018  
**Use Case Name:** Develop Configuration Format Extension  
**Primary Actor:** Extension Author  
**Goal:** Publish a configuration format in the Extension Author's own component module, verified against the configuration format contract, so that a distribution reads flag configurations written in another vendor's format, such as Flagsmith's, and keeps them as documents for its paired evaluator and the flag sync endpoint.  
**Trigger:** Extension Author needs flagd to accept flag configurations in a format flagd's own configuration format does not support  
**Status:** Implemented  

**Requirements:** [FR-019, FR-015, FR-018, FR-029, FR-044, FR-048, FR-049, FR-003, FR-008](../requirements.md)

## Preconditions

- A release of the core module that contains the configuration format factory interface, the store and the configuration format contract tests is available.
- The Extension Author has a Go module of their own in which the extension will live.
- The Extension Author has valid and invalid sample flag configurations in the vendor's format.

## Main Success Scenario

1. Extension Author adds the core module as a dependency of their component module.
2. Extension Author names the configuration format's type, by which a source names it.
3. Extension Author implements the configuration format: it validates a flag configuration in the vendor's format, which the core has already decoded from its encoding, against the vendor's rules and keeps it in the store as its own documents, or rejects it with an error naming the faulty entry.
4. Extension Author makes the configuration format report, for each document it keeps, its flag keys, its flag set, its document version and its changes against the previous document of the same flag set from the same source.
5. Extension Author implements the factory that creates the configuration format from the dependencies a distribution injects: the store, configuration and logger.
6. Extension Author runs the configuration format contract tests with the sample flag configurations (include UC-008 Run Contract Tests).
7. System reports that the configuration format meets every case of the configuration format contract.
8. Extension Author tags and publishes the component module under its Go module path and version.
9. The configuration format is available as an extension that a Distribution Builder selects in a manifest by module path and version, alone in a configuration proxy or together with its paired evaluator, exactly like an official configuration format.

## Alternative Flows

### A1: Contract Case Fails

**Trigger:** At least one case of the configuration format contract fails, for example a valid sample that is rejected, an invalid sample that is accepted, a rejection or dropped entry that is not named, a rejection that changes the store, or flag keys, flag set, document version or changes that are reported wrongly (step 7)  
**Flow:**

1. System reports each failed case with its expected and its observed behaviour.
2. Extension Author corrects the configuration format or its factory.
3. Use case continues at step 6.

### A2: Try Extension in a Distribution Before Publishing

**Trigger:** Extension Author wants to see the extension running in flagd before releasing it (step 8)  
**Flow:**

1. Extension Author builds a custom distribution whose manifest names the configuration format, with its paired evaluator or with the flag sync endpoint alone, and replaces the configuration format's component module with the local checkout (UC-001 Build Custom Distribution).
2. Extension Author starts the distribution with a source that holds a flag configuration in the vendor's format and names the configuration format.
3. Extension Author confirms that a client receives the flags as the vendor's flag configuration defines them, as evaluation results of the paired evaluator or as documents through a flag sync request that names the configuration format.
4. Use case continues at step 8.

### A3: Source Does Not Name the Configuration Format

**Trigger:** While trying the extension, the source names no configuration format, so flagd's configuration format reads the vendor's flag configuration (step 8)  
**Flow:**

1. System rejects the flag configuration with flagd's error text and the source, or accepts it with schema warnings and without the vendor's flags.
2. Extension Author names the configuration format on the source and restarts the same distribution; no rebuild is needed.
3. Use case continues at step 8.

### A4: Extension Author Abandons the Extension

**Trigger:** Extension Author stops before the component module is published (step 8)  
**Flow:**

1. Extension Author leaves the component module unpublished.
2. Use case ends.

### A5: Vendor Engine Holds Format and Evaluator

**Trigger:** The vendor's engine contains the configuration format and the evaluator in one Go module, such as the Flagsmith engine or GO Feature Flag's evaluation module (step 1)  
**Flow:**

1. Extension Author makes the component module an engine module that provides the configuration format and its paired evaluator, each through its own factory (FR-048).
2. Extension Author develops the evaluator in the same component module (UC-007 Develop Evaluator Extension).
3. Use case continues at step 2.

### A6: Source Names an Absent Configuration Format

**Trigger:** While trying the extension, the source names a configuration format the distribution does not contain, for example because the manifest does not select the extension (step 8)  
**Flow:**

1. System skips the source, logs an error naming the source and the configuration formats the distribution contains, and leaves the source out of readiness, so that the distribution still becomes ready with its other sources.
2. Extension Author adds the extension to the manifest and rebuilds the distribution, or corrects the configuration format the source names.
3. Use case continues at step 8.

## Postconditions

### Success Postconditions

- A tagged version of the component module exists under the Extension Author's Go module path.
- The component module provides exactly one configuration format through a factory of the configuration format component type and, as an engine module, also the evaluator paired with it.
- That version of the configuration format passes every case of the configuration format contract of the core module version it depends on.

### Failure Postconditions

- No version of the component module is published that fails a case of the configuration format contract.
- No official component module, standard distribution, the core module or flagd's flag model is changed.

## Business Rules

### BR-001: Accept, Reject, or Drop Reported Entries

A configuration format accepts a flag configuration of a source as a whole, rejects it as a whole, or accepts it without the entries it drops, as GO Feature Flag drops invalid flags one by one. It drops an entry only when it reports each dropped entry with the source and the reason. On rejection the store keeps the last flag configuration it accepted from that source.

### BR-002: Own Documents, Read Only by the Paired Evaluator

A configuration format keeps the flag configurations it accepted in the store as its own documents. Only the evaluator paired with it reads them; flagd's flag model and its JsonLogic evaluator never see them. The flag sync endpoint sends them unchanged, and only to a flag sync request that names this configuration format (FR-044, UC-017 BR-004).

### BR-003: Validation Belongs to the Configuration Format

Only the configuration format validates flag configurations and turns them into its documents; sync providers deliver them unchanged (UC-003 BR-008), and evaluators and service endpoints read only what the configuration format kept in the store (UC-007 BR-003). The same configuration format therefore serves a distribution with an evaluator and a configuration proxy alike.

### BR-004: A Source Names Its Configuration Format

Each source names the configuration format that reads it; a source that names none is read by flagd's configuration format (FR-029). One distribution can therefore read flagd and vendor flag configurations side by side, and pointing a source at a configuration format the distribution already contains needs no rebuild.

### BR-005: Dependency Boundary

A configuration format module depends on no telemetry provider, service endpoint or sync provider module, so that an in-process provider or a configuration proxy can contain it without them. It depends on no evaluator module either, unless it is an engine module that holds its own paired evaluator (FR-048); flagd's configuration format stays free of any evaluator (NFR-011).

### BR-006: Rejections Name the Source

A rejection names the source and, where the configuration format can tell, the faulty entry of the flag configuration, so that the Operator can correct the source without reading the configuration format's code. flagd's configuration format keeps today's error texts and adds the source (BR-010).

### BR-007: Same Selection as Official Components

A Distribution Builder selects the extension in a manifest by Go module path and version exactly like an official configuration format, and may replace it with a local checkout (FR-003, FR-008).

### BR-008: Reported Document Facts

A document is the flags of one flag set from one source. A source whose flag configuration holds several flag sets, as flagd's array configuration does, yields several documents. For each document it keeps, a configuration format reports its flag keys, its flag set, its document version and its changes against the previous document of the same flag set from the same source (FR-019). The evaluation router routes by selector and flag key with the flag keys and flag set, the OFREP service endpoint derives ETags and event stream events from the document version, and the runtime publishes configuration change events from the changes. How documents map to flag sets is the configuration format's decision: flagd by `flagSetId` metadata, Flagsmith by environment, GO Feature Flag by flag set name.

### BR-009: Decoding Belongs to the Core

The core decodes JSON and YAML flag configurations before the configuration format sees them (FR-049). A configuration format handles only its schema, and the same configuration format reads a flag configuration in any encoding the core decodes.

### BR-010: flagd's Configuration Format Rejects What flagd Rejects Today

flagd's configuration format rejects exactly the flag configurations today's flagd rejects: unparsable JSON, broken `$evaluators`, unparsable `flags`, and a flag whose default variant is missing from its variants. It rejects them with today's error texts plus the source. A flag configuration that violates flagd's schema in any other way is accepted with a warning, as today; a strict mode that rejects schema violations is not part of this use case.

### BR-011: Absent Configuration Format Skips the Source

A source that names a configuration format the distribution does not contain is skipped: System logs an error naming the source and the configuration formats the distribution contains, reads nothing from the source and leaves it out of readiness, so that the distribution becomes ready with its other sources.

## Accepted Test Gaps

- A2: the client half for documents through the flag sync endpoint is tested with UC-017; a client receiving evaluation results of the paired evaluator is tested with UC-007.
- A4: has no system behaviour to test.

## Delivered by Later Use Cases

This use case is `Implemented` for the configuration format side: the interface, the store's documents, flagd's own format, sources naming a format, the contract and an example format. These parts are delivered by the use cases named here, and are accepted gaps of this one:

- Main scenario steps 8–9 and BR-007 (tag and publish the module, select it in a manifest by module path and version): UC-001 Build Custom Distribution and UC-012.
- BR-002, flag sync half (the flag sync endpoint sends documents only to a request that names the format, FR-044): UC-017.
- BR-008, consumers of the reported facts: the OFREP endpoint derives ETags and event stream events from document versions and the runtime publishes configuration change events (UC-004).

- BR-003 for the Kubernetes sync provider, which shapes the flag configuration from the FeatureFlag CRD instead of delivering it unchanged: UC-003.
- The module split: the `configformat` packages stay in the core module for now: UC-014 and phase 4.
