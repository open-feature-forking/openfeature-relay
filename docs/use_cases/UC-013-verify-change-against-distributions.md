# Use Case: Verify Change Against Distributions

## Overview

**Use Case ID:** UC-013  
**Use Case Name:** Verify Change Against Distributions  
**Primary Actor:** Maintainer  
**Secondary Actors:** GitHub Actions, flagd Test Harness  
**Goal:** Know before a change is merged whether it keeps every standard distribution building, keeps flagd-full compatible with today's flagd, and keeps evaluation performance and dependency boundaries within their limits.  
**Trigger:** A pull request against main is opened or updated  
**Status:** Draft  

**Requirements:** [FR-032, NFR-001, NFR-004, NFR-005, NFR-007, NFR-011, C-009, C-010](../requirements.md)

## Preconditions

- The manifest of each standard distribution is kept in the repository.
- The flagd Test Harness suites and their tag selection for RPC and in-process evaluation are part of the repository.
- The checks of this use case are required for merging into main.

## Main Success Scenario

1. Maintainer opens or updates a pull request with a change to main.
2. GitHub Actions runs the linters, including the dependency rules, and the unit tests of every module.
3. GitHub Actions builds every standard distribution from its manifest, with the modules of the change in place of their released versions, as a standard build and a FIPS build.
4. GitHub Actions checks that the module graph of flagd-minimal contains no module listed in NFR-004, that the module graph of flagd-proxy contains no evaluator module and no JsonLogic module, and that the evaluator module depends on no package listed in NFR-005.
5. GitHub Actions builds the flagd-full image of the change and runs the flagd Test Harness RPC and in-process suites against it with the tag selection of today's flagd.
6. GitHub Actions runs the evaluator benchmarks on the change and on its merge base and compares time and allocations per operation of each benchmark.
7. GitHub Actions reports the result of each check on the pull request.
8. Maintainer sees that all checks passed and knows the change keeps every standard distribution building and flagd-full compatible.

## Alternative Flows

### A1: Documentation-Only Change

**Trigger:** The change touches only documentation (step 1)  
**Flow:**

1. GitHub Actions runs only the documentation checks.
2. Use case ends.

### A2: Lint or Unit Test Fails

**Trigger:** A linter, a dependency rule or a unit test fails (step 2)  
**Flow:**

1. GitHub Actions reports the failing module, rule or test on the pull request.
2. Maintainer updates the change.
3. Use case continues at step 2.

### A3: Standard Distribution Does Not Build

**Trigger:** A standard distribution does not build, its FIPS build does not carry the certified Go Cryptographic Module, or its cryptographic dependency closure differs from the reviewed one (step 3)  
**Flow:**

1. GitHub Actions reports the standard distribution, the variant and the failing component on the pull request.
2. Maintainer updates the change; a deliberate change of the cryptographic dependency closure is reviewed against the FIPS reference and committed with the change.
3. Use case continues at step 2.

### A4: Dependency Boundary Violated

**Trigger:** flagd-minimal contains a forbidden module, flagd-proxy contains an evaluator module or a JsonLogic module, or the evaluator module depends on a forbidden package (step 4)  
**Flow:**

1. GitHub Actions reports the forbidden module or package and the import chain that pulls it in.
2. Maintainer updates the change.
3. Use case continues at step 2.

### A5: Test Harness Scenario Fails

**Trigger:** A scenario of the RPC or in-process suite fails against flagd-full (step 5)  
**Flow:**

1. GitHub Actions reports the suite and the failing scenarios on the pull request.
2. Maintainer updates the change.
3. Use case continues at step 2.

### A6: Test Harness Cannot Start

**Trigger:** The flagd Test Harness or the flagd-full image cannot be started, e.g. an image is not available (step 5)  
**Flow:**

1. GitHub Actions reports a setup failure, distinct from a failing scenario.
2. Maintainer re-runs the check.
3. Use case continues at step 5.

### A7: Tag Selection Changed

**Trigger:** The change alters the tag selection of the RPC or in-process suite (step 5)  
**Flow:**

1. GitHub Actions marks the changed tag selection on the pull request.
2. Maintainer accepts the new selection in the review with a stated reason, or asks for it to be reverted.
3. Use case continues at step 6 after acceptance, or at step 2 after a revert.

### A8: Benchmark Regression

**Trigger:** A benchmark exceeds the merge base by more than 5% in time or allocations per operation (step 6)  
**Flow:**

1. GitHub Actions reports each benchmark that regressed, with both measurements.
2. Maintainer re-runs the comparison when the regression is only in time per operation and may be measurement noise, or updates the change.
3. Use case continues at step 6 after a re-run, or at step 2 after an update.

## Postconditions

### Success Postconditions

- The pull request carries a passed result for every check.
- Every standard distribution builds from the change, as standard and FIPS build.
- flagd-full built from the change passes the RPC and in-process suites with today's tag selection, or with a selection the Maintainer accepted in review.
- No evaluator benchmark is more than 5% slower or allocates more than 5% more than on the merge base.
- flagd-minimal, flagd-proxy and the evaluator module stay inside their dependency boundaries.

### Failure Postconditions

- A change with a failed or missing check is not reported as verified and cannot be merged into main.
- No binary or image built for verification is pushed to the Container Registry or attached to a release.
- No released version, manifest or tag is changed by the verification.

## Business Rules

### BR-001: Every Standard Distribution on Every Code Change

Every pull request that changes more than documentation builds all five standard distributions, each as standard and FIPS build, regardless of which modules it touches.

### BR-002: Verify the Change, Not the Last Release

Distributions are built from the modules of the change itself, so that a change to a component module is verified in every standard distribution that selects it before that module is released.

### BR-003: Today's Tag Selection

The RPC suite runs with `@rpc` and the in-process suite with `@in-process`, each with the exclusions that `test/integration/integration_test.go` holds on the upstream commit the work branched from. A change to either selection needs the Maintainer's explicit acceptance in review.

### BR-004: Harness Runs Against flagd-full

The flagd Test Harness suites run against the flagd-full image built from the change, the same way the integration test runs them against today's flagd image.

### BR-005: Benchmark Baseline

The benchmark baseline is the merge base of the pull request with main. A benchmark fails when its time or allocations per operation exceed the baseline by more than 5%.

### BR-006: Dependency Boundaries

flagd-minimal contains no module under `k8s.io`, `cloud.google.com`, `github.com/aws`, `github.com/Azure` or `go.opentelemetry.io/otel/sdk`. flagd-proxy contains no evaluator module and no `github.com/diegoholiveira/jsonlogic` module. The evaluator module depends on no package under `go.opentelemetry.io`, `google.golang.org/grpc`, `connectrpc.com`, `k8s.io` or `gocloud.dev`. The existing FIPS dependency rules of the linters stay in force.

### BR-007: Verification Publishes Nothing

Binaries and images built for verification stay inside the verification run.
