# Use Case: Embed Evaluator in In-Process Provider

## Overview

**Use Case ID:** UC-014  
**Use Case Name:** Embed Evaluator in In-Process Provider  
**Primary Actor:** Provider Author  
**Secondary Actors:** flagd Test Harness  
**Goal:** An in-process provider validates and evaluates flags locally with the same configuration format and evaluator its remote evaluation uses (flagd's, or a vendor's engine module), pulling in only those, the core module and the sync providers it supports, and leaving observability to hooks  
**Trigger:** Provider Author decides to add in-process evaluation to a provider  
**Status:** Draft  

**Requirements:** [FR-040, FR-041, FR-042, FR-044, FR-048, NFR-005, C-010](../requirements.md)

## Preconditions

- The flagd configuration format, the evaluator, the core module and the sync provider component modules, or the vendor's engine module, are released with versions a provider can pin (UC-012).
- The provider is an OpenFeature provider maintained in its own Go module.

## Main Success Scenario

1. Provider Author adds the flagd configuration format and the evaluator component modules to the provider's dependencies.
2. System adds the configuration format, the evaluator and the core module to the provider without any telemetry provider, service endpoint, transport or sync provider code (BR-001).
3. Provider Author adds the sync provider component module for each scheme the provider supports, for example the file sync provider and the gRPC sync provider, which receives flag configurations from flagd-full or from a configuration proxy such as flagd-relay.
4. System adds only the selected sync providers to the provider (BR-004).
5. Provider Author creates an empty store, then the configuration format and its paired evaluator over that same store, and connects the output of the sync provider to the configuration format; a flag sync request of the gRPC sync provider names that configuration format or, for flagd's, leaves it to the default (BR-007).
6. System decodes every flag configuration the sync provider emits, validates it with the configuration format, keeps it in the store as that configuration format's documents and reports which flags changed (BR-006).
7. Provider Author routes the provider's boolean, string, integer, float and object flag resolutions through the core module's typed adapter to the evaluator (BR-008).
8. System resolves each flag for the evaluation context with the evaluator, checks and converts its untyped result to the requested type in the typed adapter, and returns value, variant, reason and flag metadata exactly as remote evaluation does for the same flag configuration (BR-002).
9. Provider Author registers the OpenFeature hooks for traces and metrics in the provider's documentation and examples (BR-003).
10. Provider Author runs the flagd Test Harness in-process suite against the provider.
11. flagd Test Harness reports every scenario as passed, and the provider evaluates flags in-process with the embedded evaluator.

## Alternative Flows

### A1: Evaluator Brings Forbidden Dependencies

**Trigger:** The provider's module graph gains a telemetry, transport or cloud SDK module through the configuration format or the evaluator (step 2)  
**Flow:**

1. Provider Author reports the component module version and the offending module to the Maintainers as a breach of BR-001.
2. Provider Author keeps the provider on its previous configuration format and evaluator versions.
3. Use case ends.

### A2: Vendor Engine Module

**Trigger:** The provider is a vendor's in-process provider whose remote evaluation uses that vendor's engine, such as the Flagsmith engine or GO Feature Flag's evaluation module (step 1)  
**Flow:**

1. Provider Author adds the vendor's engine module, which provides the configuration format and its paired evaluator, instead of the flagd configuration format and evaluator (FR-048).
2. System adds the engine module and the core module to the provider without any telemetry provider, service endpoint, transport or sync provider code (BR-001).
3. Use case continues at step 3, with the vendor's engine module and acceptance suite in place of flagd's (BR-009).

### A3: Custom Sync Provider

**Trigger:** The provider reads flags from a source no official sync provider supports (step 3)  
**Flow:**

1. Provider Author implements a sync provider in the provider's own module against the sync interface of the core module.
2. Use case continues at step 5.

### A4: Invalid Flag Configuration

**Trigger:** The sync provider emits a flag configuration that does not validate against its configuration format (step 6)  
**Flow:**

1. System rejects the flag configuration in the configuration format, keeps the previously loaded flags in the store and reports the validation error to the provider.
2. Provider surfaces the error through its logger and its OpenFeature provider events.
3. Use case continues at step 7.

### A5: Requested Configuration Format Not Served

**Trigger:** flagd-full or flagd-relay holds no documents of the configuration format the flag sync request names (step 6)  
**Flow:**

1. The gRPC sync provider receives no flag configurations, and System reports to the provider that the source does not serve the requested configuration format.
2. Provider Author changes the flag sync request to a configuration format the source serves, or leaves it to the default.
3. Use case continues at step 6.

### A6: Flag Cannot Be Resolved

**Trigger:** The evaluator returns no value for the flag because it is unknown or disabled, or the typed adapter finds that the value does not match the requested type (step 8)  
**Flow:**

1. System returns the same error code and reason remote evaluation returns for that case.
2. Provider returns the caller's default value with that error code and reason.
3. Use case continues at step 9.

### A7: No Observability Needed

**Trigger:** The provider's users do not need traces or metrics (step 9)  
**Flow:**

1. Provider Author registers no hooks.
2. Use case continues at step 10.

### A8: Test Harness Scenario Fails

**Trigger:** flagd Test Harness reports a failing scenario (step 11)  
**Flow:**

1. Provider Author compares the failing scenario's result with remote evaluation against flagd-full.
2. If remote evaluation returns the same result, Provider Author fixes the provider and the use case continues at step 10.
3. If the results differ, Provider Author reports the scenario to the Maintainers as a breach of BR-002.
4. Use case ends.

## Postconditions

### Success Postconditions

- The provider validates flag configurations with the embedded configuration format and evaluates flags in-process with its paired evaluator, through the core module's typed adapter.
- The provider's module graph contains the configuration format and evaluator (or the vendor's engine module), the core module and the chosen sync providers, and no telemetry provider, service endpoint or other sync provider.
- For the same flag configuration and evaluation context, the provider returns the same value, variant, reason and flag metadata as remote evaluation.
- Traces and metrics of in-process evaluation come only from hooks.

### Failure Postconditions

- The provider's released version is unchanged and keeps evaluating as before.
- No version of the provider is released with a configuration format or evaluator that breaches BR-001, BR-002 or BR-006.

## Business Rules

### BR-001: Embedded Dependency Boundary

The flagd configuration format and the evaluator component modules, and a vendor's engine module, depend on no package under `go.opentelemetry.io`, `google.golang.org/grpc`, `connectrpc.com`, `k8s.io` or `gocloud.dev` (NFR-005). UC-013 checks this boundary for every change, so a provider never has to exclude these modules itself.

### BR-002: One Evaluator

The evaluator component module is the code flagd-full evaluates with. There is no separate copy for in-process use, so a fix or a new targeting operator reaches remote evaluation and in-process providers in the same release (FR-041).

### BR-003: Observability Through Hooks

The configuration format and the evaluator emit no traces and no metrics; in flagd-full the service endpoints record the evaluation spans (FR-033). For an in-process provider, observability is added only through hooks, such as the OpenFeature OpenTelemetry hooks.

### BR-004: Sync Providers by Choice

Each sync provider is its own component module; adding one never adds another (FR-042). A provider that supports only the file scheme carries no gRPC code.

### BR-005: Acceptance

An in-process provider counts as evaluating like flagd when it passes the flagd Test Harness in-process suite (C-010).

### BR-006: One Validation

The flagd configuration format is the code flagd-full and flagd-relay validate flag configurations with. A flag configuration the provider rejects is one flagd-full and flagd-relay reject as well (FR-026).

### BR-007: Requested Configuration Format

A flag sync request may name the configuration format whose documents the provider reads; when it names none, flagd's configuration format applies (FR-044). The provider only receives documents of the named configuration format, so a provider written before FR-044 keeps working without a change.

### BR-008: Typed Adapter From Core

The evaluator returns untyped results; type checks and numeric conversion happen in one typed adapter that the core module provides and every service endpoint uses as well (FR-014). A provider never writes its own type checks, so a type mismatch yields the same error code in-process and remotely.

### BR-009: Vendor Engine Takes flagd's Place

For a vendor's in-process provider, BR-002, BR-005 and BR-006 apply to the vendor's engine module, the vendor's remote evaluation and the vendor's acceptance suite in place of flagd's. The provider's flag sync request names the vendor's configuration format (BR-007).
