# Use Case: Adopt Standard Distribution

## Overview

**Use Case ID:** UC-010  
**Use Case Name:** Adopt Standard Distribution  
**Primary Actor:** Operator  
**Goal:** Run the smallest standard distribution that contains every component the deployment uses, without running the builder, so that the deployment carries fewer dependencies and less code.  
**Trigger:** Operator decides to replace flagd-full with a smaller standard distribution for a deployment  
**Status:** Draft  

**Requirements:** [FR-021, FR-022, FR-024, NFR-003, NFR-004, NFR-011, C-009](../requirements.md)

## Preconditions

- The standard distributions of the target release are published with their manifests, binaries and images (UC-012).
- Operator has a working start command, configuration file and environment variables for the deployment.

## Main Success Scenario

1. Operator determines the schemes of the deployment's sources, the service endpoints its clients use, whether flagd evaluates flags or only forwards flag configurations to in-process providers, and whether it exports telemetry.
2. Operator compares these needs with the published manifests of flagd-minimal, flagd-cloud, flagd-kubernetes, flagd-proxy and flagd-full.
3. Operator selects the smallest standard distribution that covers every need, as a binary or as an image.
4. Operator starts the selected distribution with the unchanged start command, configuration file and environment variables.
5. System checks the scheme of every source against the sync providers compiled into the distribution.
6. System starts a sync provider for each source and the service endpoints the distribution contains.
7. System reports readiness on the management port.
8. Operator sends evaluation requests and receives the same results as from flagd-full.
9. Operator keeps the selected distribution for the deployment; it runs with only the components the deployment uses.

## Alternative Flows

### A1: FIPS Build Required

**Trigger:** The deployment must use FIPS 140-3 validated cryptography (step 3)  
**Flow:**

1. Operator selects the FIPS build of the chosen standard distribution.
2. Use case continues at step 4.

### A2: Scheme Not in Distribution

**Trigger:** A source uses a scheme for which the distribution contains no sync provider (step 5)  
**Flow:**

1. System refuses to start and names the source, its scheme and the schemes the distribution supports.
2. Operator selects a standard distribution that contains a sync provider for the scheme.
3. Use case continues at step 4.

### A3: No Standard Distribution Fits

**Trigger:** No standard distribution smaller than flagd-full covers every need (step 3)  
**Flow:**

1. Operator either keeps flagd-full or asks a Distribution Builder for a custom distribution (UC-001).
2. Use case ends.

### A4: Settings for an Absent Component

**Trigger:** The configuration sets a service endpoint or telemetry setting for a component the distribution does not contain, such as an OpenTelemetry collector URI in a distribution with the noop telemetry provider (step 6)  
**Flow:**

1. System starts without that component and logs a warning that names each ignored setting and the missing component.
2. Use case continues at step 7.

### A5: Configuration Proxy Selected

**Trigger:** The selected distribution is flagd-proxy, which contains no evaluator (step 8)  
**Flow:**

1. Operator connects an in-process provider to the flag sync endpoint and confirms that it receives the same flag configurations as from flagd-full; running and operating the configuration proxy is UC-017.
2. Use case continues at step 9.

### A6: Different Behaviour Observed

**Trigger:** An evaluation result or a forwarded flag configuration differs from flagd-full for the same request (step 8)  
**Flow:**

1. Operator returns the deployment to flagd-full with the unchanged configuration.
2. Operator reports the difference to the Maintainers as a defect of the standard distribution.
3. Use case ends.

## Postconditions

### Success Postconditions

- The deployment runs a standard distribution that contains a sync provider for every scheme of its sources.
- Every evaluation returns the same result as flagd-full returns for the same request; a configuration proxy forwards the same flag configurations as flagd-full's flag sync endpoint.
- The deployment's start command, configuration file and environment variables are unchanged.

### Failure Postconditions

- A distribution never serves evaluations while one of its configured sources is left without a sync provider.
- The configuration remains usable with flagd-full, so returning to it needs no change.

## Business Rules

### BR-001: Fail on Unavailable Scheme

A distribution started with a source whose scheme it does not support stops before it serves any request and lists the schemes it supports (FR-022). It never skips such a source silently.

### BR-002: Same Configuration Surface

Every standard distribution accepts the same CLI flags, configuration file keys and environment variables as flagd-full; only the components behind them differ.

### BR-003: Telemetry Is a Component

A distribution with the noop telemetry provider contains no observability code and records no traces or metrics (FR-024).

### BR-004: Minimal Distribution Bounds

flagd-minimal is at most 50% of the size of flagd-full for the same OS and architecture (NFR-003), and its module graph contains no module under `k8s.io`, `cloud.google.com`, `github.com/aws`, `github.com/Azure` or `go.opentelemetry.io/otel/sdk` (NFR-004).

### BR-005: FIPS Build per Standard Distribution

Every standard distribution is also published as a FIPS 140-3 build with the same crypto boundary as today's FIPS build of flagd (C-009).

### BR-006: Same Evaluation Results

A standard distribution evaluates with the same evaluator as flagd-full; for the components it contains it returns the same results as flagd-full.

### BR-007: Proxy Distribution Bounds

flagd-proxy is a configuration proxy: it contains sync providers, a configuration format and the flag sync endpoint but no evaluator, and its module graph contains no evaluator module and no `github.com/diegoholiveira/jsonlogic` module (NFR-011). It rejects invalid flag configurations the same way flagd-full does (FR-026).
