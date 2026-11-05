# Use Case: Verify Change Against Distributions

## Overview

**Use Case ID:** UC-013  
**Use Case Name:** Verify Change Against Distributions  
**Primary Actor:** Maintainer  
**Secondary Actors:** GitHub Actions, flagd Test Harness  
**Goal:** Know before a change is merged whether it keeps every standard distribution building, keeps flagd-full compatible with today's flagd, keeps flagd-sync rejecting what flagd-full rejects, keeps flagd-minimal correct under concurrent clients, and keeps evaluation performance and dependencies within their limits.  
**Trigger:** A pull request against main is opened or updated  
**Status:** Tested  

**Requirements:** [FR-026, FR-032, NFR-001, NFR-004, NFR-005, NFR-007, NFR-011, NFR-012, NFR-017, C-010](../requirements.md)

## Preconditions

- The manifest of each standard distribution is kept in the repository.
- The flagd Test Harness suites and their tag selection for RPC and in-process evaluation are part of the repository.
- A set of valid and invalid flag configurations for the flagd configuration format is kept in the repository.
- The checks of this use case are required for merging into main.

## Main Success Scenario

1. Maintainer opens or updates a pull request with a change to main.
2. GitHub Actions runs the linters, including the dependency rules and the check that no Go file imports and no go.mod directly requires `golang.org/x/exp` (NFR-012), and the unit tests of every module.
3. GitHub Actions builds every standard distribution from its manifest, with the modules of the change in place of their released versions.
4. GitHub Actions checks that the binary of flagd-minimal links no module listed in NFR-004 and its packages import none, that the binary of flagd-sync links no evaluator module and no JsonLogic module and its packages import none, and that neither the evaluator module nor the flagd configuration format module depends on a package listed in NFR-005.
5. GitHub Actions builds the flagd-full image of the change and runs the flagd Test Harness RPC and in-process suites against it with the tag selection of today's flagd.
6. GitHub Actions gives flagd-sync and flagd-full each flag configuration of the set and checks that flagd-sync rejects every flag configuration flagd-full rejects and accepts every one flagd-full accepts.
7. GitHub Actions builds flagd-minimal from the change with the race detector, runs concurrent OFREP, flag evaluation and flag sync clients against it for 60 seconds while its flags change, and checks every answer and every stream against the flag generations written (NFR-017).
8. GitHub Actions runs the evaluator benchmarks on the change and on the pull request's base commit and compares allocations and time per operation of each benchmark.
9. GitHub Actions reports the result of each check on the pull request.
10. Maintainer sees that all checks passed and knows the change keeps every standard distribution building, flagd-full compatible, flagd-sync consistent with flagd-full and flagd-minimal correct under concurrent clients.

## Alternative Flows

### A1: Documentation-Only Change

**Trigger:** The change touches only documentation (step 1)  
**Flow:**

1. GitHub Actions runs only the documentation checks.
2. Use case ends.

### A2: Lint or Unit Test Fails

**Trigger:** A linter, a dependency rule, the `golang.org/x/exp` check or a unit test fails (step 2)  
**Flow:**

1. GitHub Actions reports the failing module, rule or test on the pull request.
2. Maintainer updates the change.
3. Use case continues at step 2.

### A3: Standard Distribution Does Not Build

**Trigger:** A standard distribution does not build (step 3)  
**Flow:**

1. GitHub Actions reports the standard distribution and the failing component on the pull request.
2. Maintainer updates the change.
3. Use case continues at step 2.

### A4: Dependency Boundary Violated

**Trigger:** flagd-minimal contains a forbidden module, flagd-sync contains an evaluator module or a JsonLogic module, or the evaluator module or the flagd configuration format module depends on a forbidden package (step 4)  
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

### A8: flagd-sync and flagd-full Disagree

**Trigger:** flagd-sync accepts a flag configuration flagd-full rejects, or rejects one flagd-full accepts (step 6)  
**Flow:**

1. GitHub Actions reports each flag configuration with the result of flagd-sync and of flagd-full.
2. Maintainer updates the change.
3. Use case continues at step 2.

### A9: Allocation Regression

**Trigger:** A benchmark allocates more per operation than on the pull request's base commit (step 8)  
**Flow:**

1. GitHub Actions reports each benchmark that regressed, with both measurements.
2. Maintainer updates the change.
3. Use case continues at step 2.

### A10: Time Deviation Beyond 5%

**Trigger:** A benchmark's time per operation differs from the pull request's base commit by more than 5% while its allocations do not increase (step 8)  
**Flow:**

1. GitHub Actions reports the benchmark with both measurements as a finding that does not fail the check.
2. Maintainer decides in the review whether the change needs work.
3. Use case continues at step 9.

### A11: Incorrect Under Concurrent Load

**Trigger:** flagd-minimal answers from a flag generation outside the change window, a stream misses or lags a flag generation, a request fails, flagd-minimal loses readiness or does not exit cleanly, or the race detector reports a data race (step 7)  
**Flow:**

1. GitHub Actions reports the failed check with the client, the request times and the flag generations, and keeps flagd-minimal's output and the load summary with the run.
2. Maintainer updates the change.
3. Use case continues at step 2.

## Postconditions

### Success Postconditions

- The pull request carries a passed result for every check.
- Every standard distribution builds from the change.
- flagd-full built from the change passes the RPC and in-process suites with today's tag selection, or with a selection the Maintainer accepted in review.
- flagd-sync rejects and accepts exactly the flag configurations of the set that flagd-full rejects and accepts.
- No evaluator benchmark allocates more per operation than on the pull request's base commit, and every time deviation beyond 5% is reported on the pull request.
- flagd-minimal, flagd-sync, the evaluator module and the flagd configuration format module stay inside their dependency boundaries, and no module depends directly on `golang.org/x/exp`.
- flagd-minimal built from the change answers every client from a flag generation inside the change window, every stream sees every flag generation, and the race detector reports nothing.

### Failure Postconditions

- A change with a failed or missing check is not reported as verified and cannot be merged into main.
- No binary or image built for verification is pushed to the Container Registry or attached to a release.
- No released version, manifest or tag is changed by the verification.

## Business Rules

### BR-001: Every Standard Distribution on Every Code Change

Every pull request that changes more than documentation builds all six standard distributions, regardless of which modules it touches. A FIPS build is not part of this verification: it is documented for the builder, not verified in CI (C-009).

### BR-002: Verify the Change, Not the Last Release

Distributions are built from the modules of the change itself, so that a change to a component module is verified in every standard distribution that selects it before that module is released.

### BR-003: Today's Tag Selection

The RPC suite runs with `@rpc` and the in-process suite with `@in-process`, each with the exclusions that `test/integration/integration_test.go` holds on the upstream commit the work branched from. A change to either selection needs the Maintainer's explicit acceptance in review.

### BR-004: Harness Runs Against flagd-full

The flagd Test Harness suites run against the flagd-full image built from the change, the same way the integration test runs them against today's flagd image.

### BR-005: Benchmark Baseline and Gate

The benchmark baseline is the pull request's base commit. A benchmark fails the check when its allocations per operation exceed the baseline. A time per operation more than 5% away from the baseline is reported and never fails the check, because shared CI runners do not measure time reliably.

### BR-006: Dependency Boundaries

flagd-minimal contains no module under `k8s.io`, `cloud.google.com`, `github.com/aws`, `github.com/Azure` or `go.opentelemetry.io/otel/sdk`. flagd-sync contains no evaluator module and no `github.com/diegoholiveira/jsonlogic` module. Neither the evaluator module nor the flagd configuration format module depends on a package under `go.opentelemetry.io`, `google.golang.org/grpc`, `connectrpc.com`, `k8s.io` or `gocloud.dev`. The existing FIPS dependency rules of the linters stay in force.

### BR-007: Verification Publishes Nothing

Binaries and images built for verification stay inside the verification run.

### BR-008: Same Verdict as flagd-full

flagd-sync and flagd-full are checked against the same set of flag configurations, and flagd-sync gives the same verdict, accept or reject, as flagd-full for each one. A flag configuration added to the set is valid or invalid by flagd-full's verdict, never by flagd-sync's.

### BR-009: Short Load on Every Change

Every pull request that changes more than documentation runs the 60-second concurrency load against flagd-minimal. The 10-minute run with more clients is not part of this verification; it runs on demand or in a full build.

## Delivered by Later Use Cases

This use case is `Tested` (TC-007): the build of every standard distribution from its manifest; the dependency boundaries of NFR-004 and NFR-005; the Test Harness suites against the flagd-full image with a pinned tag selection; the verdict of flagd-sync against flagd-full for the set of flag configurations; and the allocs/op comparison with the pull request's base commit, with time deviations beyond 5% reported. The checks are listed in [Status and roadmap](../relay/status.md). These parts are accepted gaps of this use case:

| Gap | Delivered by |
|-----|--------------|
| Precondition, "required for merging": branch protection is a repository setting the project lead makes; no workflow enforces it | the project lead, after the proof of concept |
| BR-005, NFR-001: the allocs/op of the evaluator benchmarks against the upstream commit the work branched from. Every benchmark's package path moved, so `compare-allocs.py` finds no common benchmark without a mapping of old paths to new ones | after the incubator |
| A6: a setup failure of the Test Harness or the flagd-full image fails the integration test with `failed to build flagd image` before any scenario; no test pins it | the project lead, after the proof of concept |
