# Use Case: Upgrade to flagd-full

## Overview

**Use Case ID:** UC-009  
**Use Case Name:** Upgrade to flagd-full  
**Primary Actor:** Operator  
**Goal:** Replace a running flagd from before the builder with flagd-full without changing the start command, configuration file, environment variables, ports or image name, and keep the same evaluation results.  
**Trigger:** Operator decides to move a flagd deployment to the first release built as flagd-full  
**Status:** Draft  

**Requirements:** [FR-020, FR-033, FR-034, FR-052, NFR-002, NFR-007, C-003](../requirements.md)

## Preconditions

- Operator runs a flagd from before the builder with a working start command, configuration file and environment variables.
- The flagd-full binary and image of the target release are published (UC-012).

## Main Success Scenario

1. Operator selects the flagd-full release of the same kind as the current deployment: the binary, or the image `ghcr.io/open-feature/flagd` with the new version tag.
2. Operator starts flagd-full with the unchanged start command, configuration file and environment variables.
3. System reads the settings from the command line, the configuration file and the environment variables with the same names and the same order of precedence as before.
4. System recognizes the scheme of every source and starts a sync provider for each one.
5. System merges the flag configurations of all sources so that a later source overrides flag keys of an earlier one.
6. System starts the flag evaluation, OFREP and flag sync service endpoints and the management port on the configured ports.
7. System reports readiness on the management port once every source has delivered a flag configuration that its configuration format accepted, as before; no staleness limit applies unless the Operator configures one.
8. Operator checks with `flagd version` that the running binary names flagd-full, its manifest version and the target release.
9. Operator sends evaluation requests that the previous deployment answered and receives the same results.
10. Operator retires the previous flagd; the deployment now runs flagd-full with no change to its configuration.

## Alternative Flows

### A1: FIPS Build in Use

**Trigger:** The current deployment runs the FIPS build of flagd (step 1)  
**Flow:**

1. Operator selects the FIPS build of flagd-full under the names of BR-007: the binary archive `flagd-fips_<version>_<OS>_<arch>` or the image `ghcr.io/open-feature/flagd` with the `-fips` tag suffix.
2. Use case continues at step 2.

### A2: Source With Unrecognized Scheme

**Trigger:** A source starts with a scheme that no sync provider of flagd-full handles (step 4)  
**Flow:**

1. System refuses to start and names the source and the schemes it accepts.
2. Operator corrects the source, which was equally invalid before the upgrade.
3. Use case continues at step 2.

### A3: Deprecated Setting in Configuration

**Trigger:** The configuration contains a setting that today's flagd accepts only for compatibility, such as a keep-alive setting of the flag sync service endpoint (step 3)  
**Flow:**

1. System accepts the setting and ignores it, exactly as the previous flagd did.
2. Use case continues at step 4.

### A4: Source Not Reachable

**Trigger:** A source does not deliver flag configurations at startup (step 4)  
**Flow:**

1. System keeps retrying the source and reports it as not ready, with the same retry and readiness behaviour as the previous flagd.
2. Operator restores access to the source.
3. Use case continues at step 5.

### A5: Different Behaviour Observed

**Trigger:** An evaluation result, an endpoint, the readiness behaviour or the shape of the traces differs from the previous deployment (step 9)  
**Flow:**

1. Operator rolls back to the previous flagd release with the unchanged configuration.
2. Operator reports the difference to the Maintainers as a compatibility defect.
3. Use case ends.

## Postconditions

### Success Postconditions

- flagd-full serves every flag of the configured sources on the same ports and endpoints as the previous deployment.
- Every evaluation returns the same result as the previous deployment returned for the same request.
- The start command, configuration file, environment variables and image name are unchanged.

### Failure Postconditions

- The start command, configuration file and environment variables remain usable with the previous flagd release, so a rollback needs no configuration change.
- No source or flag configuration is modified by the attempt.

## Business Rules

### BR-001: Unchanged Operator Surface

flagd-full accepts every CLI flag, short flag, configuration file key and environment variable of the previous release with the same default values, and keeps its ports, endpoints and image name `ghcr.io/open-feature/flagd` (C-003). An Operator never needs to edit a configuration to upgrade.

### BR-002: Same Source Precedence

When several sources define the same flag key, the source given later on the command line or in the sources list overrides the one given earlier, as before the builder.

### BR-003: Same Scheme Coverage

A scheme is the prefix of a source that selects its sync provider and need not be a URI scheme. flagd-full contains a sync provider for every scheme the previous flagd accepted: `file:`, `http://`, `https://`, `grpc://`, `grpcs://`, the custom gRPC name-resolution scheme, `gs://`, `azblob://`, `s3://` and `core.openfeature.dev/`.

### BR-004: Verified Compatibility

A flagd-full release is published only when it passes the flagd test-harness RPC and in-process suites with the same tag selection and results as the previous flagd (NFR-007).

### BR-005: Startup Time

flagd-full reaches readiness within 10% of the time the previous flagd needs with the same sources, measured over 10 runs (NFR-002).

### BR-006: Same Trace Shape

The flag evaluation and OFREP service endpoints record one evaluation span per flag evaluation in every distribution with an OpenTelemetry telemetry provider, flagd-full included, so that an Operator's traces keep the shape the previous flagd's evaluator gave them (FR-033).

### BR-007: Release Names

Binary archives follow today's release pattern with the distribution name in the product position, and FIPS builds add `-fips` to it: flagd-full keeps `flagd_<version>_<OS>_<arch>` and `flagd-fips_<version>_<OS>_<arch>`; every other standard distribution is `flagd-<distribution>_<version>_<OS>_<arch>` and `flagd-<distribution>-fips_<version>_<OS>_<arch>`, e.g. `flagd-relay-fips_<version>_Linux_x86_64`. flagd-full's image stays `ghcr.io/open-feature/flagd`, the others are `ghcr.io/open-feature/flagd-<distribution>`, and FIPS images add `-fips` to their tags (UC-012).

### BR-008: No Staleness Limit by Default

flagd-full applies no staleness limit unless the Operator configures one, so readiness behaves as before the upgrade (C-003). With a configured limit, readiness fails while a source has not delivered within it (FR-052).
