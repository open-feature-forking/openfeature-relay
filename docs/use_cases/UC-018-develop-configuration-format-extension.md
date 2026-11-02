# Use Case: Develop Configuration Format Extension

## Overview

**Use Case ID:** UC-018  
**Use Case Name:** Develop Configuration Format Extension  
**Primary Actor:** Extension Author  
**Goal:** Let a distribution read flag configurations written in another vendor's format, such as Flagsmith's, by writing a configuration format extension in an own component module that parses and validates them and keeps them in the store for its paired evaluator and the flag sync endpoint.  
**Trigger:** Extension Author needs flagd to accept flag configurations in a format flagd's own configuration format does not support  
**Status:** Draft  

**Requirements:** [FR-019, FR-015, FR-018, FR-029, NFR-010](../requirements.md)

## Preconditions

- A released core module provides the configuration format factory interface and the store a configuration format keeps its documents in.
- The Extension Author has a Go module of their own that depends on the core module.
- The Extension Author has valid and invalid sample flag configurations in the vendor's format.

## Main Success Scenario

1. Extension Author implements the configuration format factory interface in their component module, giving the configuration format a component name and the configuration it accepts.
2. Extension Author implements the configuration format: it parses a flag configuration in the vendor's format, validates it against the vendor's rules and keeps it in the store as its own documents, or rejects it with an error naming the faulty entry.
3. Extension Author runs the contract tests for the configuration format component type with the sample flag configurations (include UC-008 Run Contract Tests).
4. System reports that the configuration format satisfies the configuration format contract.
5. Extension Author lists the extension as a configuration format in the manifest of a custom distribution, with a local replacement of its component module, together with the evaluator paired with it or with the flag sync endpoint alone.
6. System validates the manifest, checks that every selected evaluator's configuration format is selected, generates the distribution source and compiles the custom distribution.
7. Extension Author starts the custom distribution with a source that holds a flag configuration in the vendor's format and names the extension as its configuration format.
8. System hands the flag configuration of the source to the extension, which parses and validates it and keeps the accepted flag configuration in the store as its documents.
9. Extension Author reads the flags through a service endpoint of the distribution, as an evaluation result of the paired evaluator or as documents through the flag sync endpoint.
10. System returns the flags as the vendor's flag configuration defines them; the configuration format extension works in a custom distribution.

## Alternative Flows

### A1: Contract Violated

**Trigger:** The contract tests report a violation, for example a valid sample that is rejected, an invalid sample that is accepted, a rejection that does not name the faulty entry, or a rejection that changes the store (step 4)  
**Flow:**

1. System names each violated contract rule and the failing sample.
2. Extension Author corrects the configuration format.
3. Use case continues at step 3.

### A2: Manifest Invalid

**Trigger:** The manifest selects an evaluator without the configuration format it is paired with, or the entry for the extension cannot be resolved (step 6)  
**Flow:**

1. System rejects the manifest with an error naming the faulty entry and builds nothing.
2. Extension Author corrects the manifest.
3. Use case continues at step 6.

### A3: Configuration Format Not in Distribution

**Trigger:** The source names a configuration format the distribution does not contain, or names none, so that flagd's configuration format reads the vendor's flag configuration (step 7)  
**Flow:**

1. System refuses the source and names it, the configuration format it asked for and the configuration formats the distribution contains; for an unnamed format flagd's configuration format rejects the flag configuration.
2. Extension Author names the extension as the source's configuration format, or adds the extension to the manifest.
3. Use case continues at step 6.

### A4: Flag Configuration Rejected

**Trigger:** The extension rejects the flag configuration of the source (step 8)  
**Flow:**

1. System logs an error naming the source and the faulty entry, and keeps the last flag configuration it accepted from that source in the store.
2. Extension Author corrects the flag configuration in the source, or corrects the extension when the flag configuration is valid in the vendor's format.
3. Use case continues at step 7.

## Postconditions

### Success Postconditions

- The configuration format extension lives in the Extension Author's own component module and passes the configuration format contract tests.
- A custom distribution that names the extension in its manifest keeps a valid flag configuration in the vendor's format in the store as the extension's documents, for a source that names the extension.
- No flagd source outside the Extension Author's component module was changed, and flagd's flag model is unchanged.

### Failure Postconditions

- The store never holds a flag configuration the extension rejected.
- A distribution whose manifest does not name the extension is unaffected by it.

## Business Rules

### BR-001: Accept or Reject as a Whole

A configuration format accepts a flag configuration of a source as a whole or rejects it as a whole. On rejection the store keeps the last flag configuration it accepted from that source.

### BR-002: Own Documents, Read Only by the Paired Evaluator

A configuration format keeps the flag configurations it accepted in the store as its own documents. Only the evaluator paired with it reads them; flagd's flag model and its JsonLogic evaluator never see them. The flag sync endpoint forwards them unchanged, tagged with the configuration format (UC-017 BR-004).

### BR-003: Parsing and Validation Belong to the Configuration Format

Only the configuration format parses and validates flag configurations. The evaluator and the service endpoints read only what the configuration format kept in the store, so the same configuration format serves a distribution with an evaluator and a configuration proxy alike (UC-007 BR-002, UC-017 BR-002).

### BR-004: A Source Names Its Configuration Format

Each source names the configuration format that reads it; a source that names none is read by flagd's configuration format (FR-029). One distribution can therefore read flagd and vendor flag configurations side by side.

### BR-005: Dependency Boundary

A configuration format module depends on no telemetry provider, service endpoint, sync provider or evaluator module, so that an in-process provider or a configuration proxy can contain it without them.

### BR-006: Rejections Name the Faulty Entry

A rejection names the source and the faulty entry of the flag configuration, so that the Operator can correct the source without reading the configuration format's code.
