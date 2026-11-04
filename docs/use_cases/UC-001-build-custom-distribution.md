# Use Case: Build Custom Distribution

## Overview

**Use Case ID:** UC-001  
**Use Case Name:** Build Custom Distribution  
**Primary Actor:** Distribution Builder  
**Secondary Actors:** Go Toolchain  
**Goal:** Turn a manifest into a runnable flagd binary that contains exactly the components the manifest selects, with its generated source available for inspection or commit.  
**Trigger:** Distribution Builder needs a flagd with a set of components that no standard distribution offers  
**Status:** Implemented  

**Requirements:** [FR-001, FR-002, FR-003, FR-004, FR-005, FR-006, FR-007, FR-008, FR-017, FR-018, FR-034, NFR-008, NFR-010, C-001](../requirements.md)

## Preconditions

- The Builder is installed for the Distribution Builder.
- The Go Toolchain is installed in the Go version the core module declares.
- Every component module the Distribution Builder intends to select is released with a version, or available as a local checkout.

## Main Success Scenario

1. Distribution Builder writes a manifest that names the distribution, its version and the output location.
2. Distribution Builder lists in the manifest the components to compile in, grouped by component type (sync providers, configuration formats, evaluators, engines, service endpoints, and optionally a telemetry provider, middleware and event sinks), each as a component module path with a version. The manifest can also name the module that provides the distribution's command line, with a version; without it the distribution has the command line of the runtime.
3. Distribution Builder runs the Builder on the manifest.
4. Builder validates the manifest.
5. Builder generates the distribution source into the output location: an entry point that registers the factory of every listed component, and a module definition that requires every listed component module at its declared version.
6. Builder asks the Go Toolchain to resolve the component modules and all their dependencies.
7. Builder checks that every component module resolved at the version the manifest declares.
8. Builder checks that every component a selected component needs is selected as well, and that every scheme is claimed by one sync provider only.
9. Builder asks the Go Toolchain to compile the distribution source into a single flagd binary, stamped with the distribution name, the distribution version, the source revision and the build date.
10. Builder reports the location of the distribution binary and of the distribution source; the custom distribution is ready to run.

## Alternative Flows

### A1: Invalid Manifest

**Trigger:** The manifest is malformed, misses a mandatory entry, or breaks a rule of this use case (step 4)  
**Flow:**

1. Builder reports every faulty entry with its position in the manifest and the reason.
2. Builder generates nothing and leaves the output location unchanged.
3. Distribution Builder corrects the manifest.
4. Use case continues at step 3.

### A2: Build Against a Local Checkout

**Trigger:** Distribution Builder wants to build against a local checkout of a component module instead of its released version (step 2)  
**Flow:**

1. Distribution Builder declares in the manifest a module replacement that maps the component module path to the local directory of the checkout.
2. Builder carries the replacement into the generated module definition in step 5, and the Go Toolchain uses the local checkout in step 6.
3. Builder skips the version check of step 7 for the replaced module.
4. Use case continues at step 3.

### A3: Generation Skipped

**Trigger:** Distribution Builder requested to skip the generate step (step 5)  
**Flow:**

1. Builder leaves the distribution source in the output location as it is.
2. Use case continues at step 6.

### A4: No Distribution Source to Reuse

**Trigger:** Distribution Builder requested to skip the generate step, and the output location holds no distribution source (step 5)  
**Flow:**

1. Builder reports that the output location holds no distribution source to build.
2. Builder produces no binary.
3. Use case ends.

### A5: Module Resolution Skipped

**Trigger:** Distribution Builder requested to skip the module resolution step (step 6)  
**Flow:**

1. Builder uses the module dependencies already recorded with the distribution source and does not ask the Go Toolchain to resolve them again.
2. Builder skips the version check of step 7.
3. Use case continues at step 8.

### A6: Compilation Skipped

**Trigger:** Distribution Builder requested to skip the compile step (step 9)  
**Flow:**

1. Builder produces no binary.
2. Builder reports the location of the distribution source, ready for the Distribution Builder's own build pipeline.
3. Use case ends.

### A7: Component Module Cannot Be Resolved

**Trigger:** The Go Toolchain cannot find a component module or its declared version, or cannot reach the module source (step 6)  
**Flow:**

1. Builder reports the component module, the declared version and the reason the Go Toolchain gave.
2. Builder produces no binary.
3. Use case ends.

### A8: Resolved Version Differs From the Manifest

**Trigger:** A component module resolved at a version other than the one the manifest declares, because another selected component module requires a newer version of it (step 7)  
**Flow:**

1. Builder reports the component module, the declared version, the resolved version and the component modules that require the resolved version.
2. Builder produces no binary.
3. Distribution Builder aligns the declared version in the manifest.
4. Use case continues at step 3.

### A9: Needed Component Not Selected

**Trigger:** A selected component needs a component the manifest does not select, such as OFREP or flag evaluation without an evaluator, or an evaluator without its configuration format (step 8)  
**Flow:**

1. Builder reports every unmet need, naming the component that needs it and the component type or component that is missing.
2. Builder produces no binary.
3. Distribution Builder adds the missing component to the manifest or removes the component that needs it.
4. Use case continues at step 3.

### A10: Two Sync Providers Claim the Same Scheme

**Trigger:** Two selected sync providers claim the same scheme (step 8)  
**Flow:**

1. Builder reports the scheme and both sync providers that claim it.
2. Builder produces no binary.
3. Distribution Builder removes one of the two sync providers from the manifest.
4. Use case continues at step 3.

### A11: Evaluator Nothing Needs

**Trigger:** A selected evaluator is needed by no selected component, for example in a configuration proxy that lists one by mistake (step 8)  
**Flow:**

1. Builder warns that the evaluator is compiled in without a component that uses it.
2. Use case continues at step 9.

### A12: Compilation Fails

**Trigger:** The Go Toolchain cannot compile the distribution source, for example because a component module does not provide a factory of the component type it is listed under, or two component modules depend on incompatible versions of the core module (step 9)  
**Flow:**

1. Builder reports the Go Toolchain's failure together with the components it concerns.
2. Builder produces no binary.
3. Use case ends.

## Postconditions

### Success Postconditions

- The output location holds the distribution source generated from the manifest.
- Unless the compile step was skipped, the output location holds a flagd binary that contains exactly the components the manifest lists, each at its declared version or from its declared local checkout.
- The distribution binary reports the distribution name, distribution version, source revision and build date.
- The manifest is unchanged.

### Failure Postconditions

- No binary is reported as built from the manifest.
- A binary left in the output location by an earlier build is never presented as the result of this build.
- The manifest is unchanged.
- No file outside the output location is changed by the Builder.

## Business Rules

### BR-001: Mandatory Manifest Entries

A manifest names the distribution, its version and its output location, and lists at least one component.

### BR-002: Component Cardinality

A distribution contains at least one sync provider, at least one configuration format and at least one service endpoint. An engine module, which provides a configuration format and its evaluator together, is listed once under engines and counts for both. It contains one evaluator for each configuration format whose flags it evaluates, and none when no selected component needs one, as in a configuration proxy; an evaluator nothing needs is compiled in with a warning (A11). A telemetry provider is optional, at most one is selected, and the noop telemetry provider is compiled in when the manifest names none. Middleware is optional.

### BR-003: Same Reference for Every Component

Every component, official or extension, is referenced the same way: by its component module path and an explicit version. A version range or a moving reference such as "latest" is invalid.

### BR-004: Each Component Module Once

A component module appears at most once in a manifest.

### BR-005: Only Selected Components

The distribution source registers exactly the components the manifest lists; no other component and no dependency that only an unlisted component needs is compiled in.

### BR-006: Declared Versions Are Binding

A distribution is built only when every component module resolves at the version the manifest declares, so that the manifest stays an exact record of the distribution's contents. Replaced modules (A2) and skipped module resolution (A5) are exempt. The Distribution Builder can ask the Builder to report a version that differs from the declared one as a warning instead (`--skip-strict-versioning`).

### BR-007: Local Replacement Target

A module replacement maps a component module path to a local directory that contains that module; a replacement that points to a missing directory or to another module is invalid.

### BR-008: Skippable Steps

Each of the generate, module resolution and compile steps can be skipped on its own; the Builder runs the remaining steps in their usual order.

### BR-009: Generated Files Only

The Builder writes only the files it generates into the output location and overwrites them on every run; other files there are left untouched.

### BR-010: Reproducible Source

The same manifest generates the same distribution source on every run.

### BR-011: Standard Go Build

A distribution is one statically linked binary built with Go modules and the standard Go compiler; components are never loaded at run time.

### BR-012: Build Duration

With a warm module cache, generating and compiling flagd-full takes at most 3 minutes on a GitHub-hosted `ubuntu-latest` runner.

### BR-013: Any Combination of Service Endpoints

Any non-empty combination of service endpoints is valid, official or extension: OFREP alone, flag evaluation alone, flag sync alone, several of them, or all of them plus extensions, as long as BR-014 holds.

### BR-014: Component Needs Are Met

Each component states which component types or components it needs; a distribution is built only when every need of every selected component is met by another selected component. OFREP and flag evaluation need an evaluator; an evaluator needs the configuration format it evaluates; flag sync needs a configuration format but no evaluator.

### BR-015: One Sync Provider per Scheme

Every scheme is claimed by exactly one selected sync provider. A scheme is the prefix of a source, which need not be a URI scheme (for example `core.openfeature.dev/`); two sync providers claiming the same prefix make the manifest invalid.

## Delivered by Later Use Cases

This use case is `Implemented`: the manifest and its validation, the generated source, module resolution with the version checks, the checks of needs and schemes, the compile, the skippable steps, the distribution's own command line, the manifests of flagd-full and of the test distributions that the Builder builds in CI, and (UC-021) the operator settings for the middleware, event sinks and service endpoints a generated source lists: the generated source lists their factories, and the Operator's configuration file activates them. These parts are delivered by the use cases named here, and are accepted gaps of this one:

| Gap | Delivered by |
|-----|--------------|
| Tagged releases and the Builder's release | UC-012 |
| The platform matrix of the standard distributions (NFR-009) | UC-012 |
| flagd-full released from the generated source | UC-009 |
| `flagd components` (the inventory and the identity exist) | UC-011 |
| Reference Dockerfile (FR-009) | UC-002 |
| Compiling a distribution with middleware: no repository manifest lists one yet; the Flagsmith distribution will list `contrib/flagsmith/middleware` | UC-019 (#94) |
