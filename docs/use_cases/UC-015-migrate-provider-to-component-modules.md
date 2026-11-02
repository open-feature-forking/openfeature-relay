# Use Case: Migrate Provider to Component Modules

## Overview

**Use Case ID:** UC-015  
**Use Case Name:** Migrate Provider to Component Modules  
**Primary Actor:** Provider Author  
**Secondary Actors:** Maintainer, flagd Test Harness  
**Goal:** An in-process provider moves from the current core import paths to the new component modules in one change and behaves exactly as before  
**Trigger:** The Maintainers publish a core release that moves Go import paths under `core/`  
**Status:** Draft  

**Requirements:** [FR-043, FR-044, C-004, C-005](../requirements.md)

## Preconditions

- The core release that moves the import paths and the component modules it points to are released (UC-012).
- The migration guide for that release is published.
- The provider passes its own tests and the flagd Test Harness suites on the core version it uses today.

## Main Success Scenario

1. Provider Author opens the migration guide for the target core release.
2. System lists every moved import path under `core/` with its new component module and import path, and every renamed or removed identifier with its replacement.
3. Provider Author lists the core import paths the provider uses today: evaluator, store, model, sync, logger, service, the file sync provider, the gRPC sync provider and its credentials, the evaluator's combined loading of flag configurations, and its typed resolution per flag type.
4. Provider Author finds each listed path in the migration guide (BR-001).
5. Provider Author replaces each old import path and identifier with the one the guide names, hands the loading of flag configurations to the flagd configuration format (BR-005), routes typed resolutions through the core module's typed adapter (BR-006), and pins the new core module and component module versions.
6. System builds the provider against the new modules, with no reference to the old core version left in the provider's module graph.
7. Provider Author runs the provider's tests and the flagd Test Harness in-process and remote evaluation suites.
8. flagd Test Harness reports the same results as on the old core version.
9. Provider Author releases the provider with the migration as one change.

## Alternative Flows

### A1: Import Path Missing From Guide

**Trigger:** A core import path the provider uses has no entry in the migration guide (step 4)  
**Flow:**

1. Provider Author reports the missing path to the Maintainers as a gap in the migration guide (BR-001).
2. Maintainer adds the path and its replacement to the migration guide.
3. Use case continues at step 4.

### A2: Path Removed Without Replacement

**Trigger:** The migration guide marks a path the provider uses as removed and names no replacement module (step 4)  
**Flow:**

1. Provider Author asks the Maintainers to restore the path as a component module or to name a replacement.
2. Provider Author keeps the provider on the old core version.
3. Use case ends.

### A3: Build Fails on Undocumented Change

**Trigger:** The provider does not build because an identifier changed in a way the guide does not describe (step 6)  
**Flow:**

1. Provider Author reports the change to the Maintainers as a gap in the migration guide (BR-001).
2. Maintainer adds the change to the migration guide.
3. Use case continues at step 5.

### A4: Another Dependency Needs the Old Core Version

**Trigger:** Another module the provider depends on still imports the old core paths, so both cannot be satisfied in one module graph (step 6)  
**Flow:**

1. Provider Author waits for that module to release a version on the new component modules.
2. Provider Author keeps the provider on the old core version.
3. Use case ends.

### A5: Behaviour Changed

**Trigger:** A test or a flagd Test Harness scenario that passed on the old core version fails (step 8)  
**Flow:**

1. Provider Author checks whether the failure comes from the provider's own changes.
2. If it does, Provider Author fixes the provider and the use case continues at step 7.
3. If it does not, Provider Author reports the scenario to the Maintainers as a breach of BR-002 and keeps the provider on the old core version.
4. Use case ends.

## Postconditions

### Success Postconditions

- The provider imports only the new core module and component module paths; no old core import path remains.
- The provider validates flag configurations with the flagd configuration format and evaluates them with the evaluator, both over the same store.
- The provider passes its tests and the flagd Test Harness suites with the same results as before the migration.
- The released provider version pins the new core module and component module versions.

### Failure Postconditions

- The provider's released version stays on the old core version and keeps working.
- No provider version is released that mixes old and new core import paths.

## Business Rules

### BR-001: Complete Migration Guide

The migration guide covers every exported import path under `core/` that moves or disappears in the release, so a provider migrates in one change without reading the core source (FR-043). It covers at least every path the Go flagd provider imports today: `core/pkg/evaluator`, `core/pkg/store`, `core/pkg/model`, `core/pkg/sync`, `core/pkg/logger`, `core/pkg/service`, `core/pkg/sync/file`, `core/pkg/sync/grpc` and `core/pkg/sync/grpc/credentials`. A gap found by a Provider Author is a defect of the guide.

### BR-002: Paths Move, Behaviour Does Not

A core release that moves import paths changes no evaluation result, flag configuration format or sync behaviour. The migration changes the provider's import paths, identifiers and dependency versions only. The provider's flag sync request needs no change: a request that names no configuration format still receives flagd's (FR-044).

### BR-003: Moves Ship in a Major or Minor Release

Moved import paths ship only in a new major or minor core release (C-004). Patch releases of the old core version never move a path, so a provider that has not migrated keeps building.

### BR-004: Protobuf Packages Stay

The generated protobuf packages for flag evaluation and flag sync keep their layout (C-005). Migrating a provider never changes its imports of these packages.

### BR-005: Loading and Evaluation Are Split

Today `evaluator.SetState` parses, validates and loads a flag configuration into the store in one call. After the migration the flagd configuration format parses and validates the flag configuration and keeps it in the store as its own documents, and its paired evaluator only reads those documents. The migration guide maps `SetState` to this split, including where validation errors are now reported, and the provider adds the configuration format component module to its dependencies.

### BR-006: Typed Resolution Moves to Core

Today the provider calls the evaluator's typed methods (`ResolveBooleanValue`, `ResolveStringValue`, `ResolveIntValue`, `ResolveFloatValue`, `ResolveObjectValue`) through `evaluator.IEvaluator`. After the migration the evaluator returns untyped results, and the provider calls the core module's typed adapter, which checks and converts the value as every service endpoint does (FR-014). The migration guide maps each typed method and its return values to the adapter, so the provider returns the same value, variant, reason, error code and flag metadata as before.
