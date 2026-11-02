# Use Case: Recover From Snapshot

## Overview

**Use Case ID:** UC-016  
**Use Case Name:** Recover From Snapshot  
**Primary Actor:** Operator  
**Goal:** Have a restarted flagd serve the last known flags immediately, even while its sources are unavailable, and switch to live flag configurations as soon as the sources deliver them.  
**Trigger:** Operator restarts a flagd distribution that writes a snapshot  
**Status:** Draft — Deferred (FR-050, FR-051 are planned for a later release)  

**Requirements:** [FR-050, FR-051](../requirements.md)

## Preconditions

- The distribution contains the snapshot components.
- Operator has configured a snapshot location that persists across restarts.

## Main Success Scenario

1. Operator restarts flagd with its configured sources and snapshot location.
2. System reads the snapshot as the lowest-precedence source.
3. System serves evaluations from the flags in the snapshot and reports readiness.
4. System starts a sync provider for every configured source.
5. A source delivers its flag configuration.
6. System merges the live flag configuration over the snapshot, so that live flags override snapshot flags with the same key.
7. System writes the new flag state to the snapshot.
8. System serves evaluations from the live flag configurations; the restart caused no period without flags.

## Alternative Flows

### A1: Sources Remain Unavailable

**Trigger:** No source delivers a flag configuration (step 5)  
**Flow:**

1. System keeps serving evaluations from the snapshot and keeps retrying the sources.
2. A source becomes available and delivers its flag configuration.
3. Use case continues at step 6.

### A2: No Snapshot Yet

**Trigger:** The snapshot location holds no snapshot, as on the first start (step 2)  
**Flow:**

1. System starts without snapshot flags and reports readiness only after a source has delivered its flag configuration, as without a snapshot.
2. Use case continues at step 4.

### A3: Snapshot Unreadable

**Trigger:** The snapshot is corrupt or not a valid flag configuration (step 2)  
**Flow:**

1. System logs a warning that names the snapshot location and starts without snapshot flags.
2. Use case continues at step 4.

### A4: Pre-Seeded Snapshot

**Trigger:** Operator provides a snapshot with the image or the deployment before the first start (step 1)  
**Flow:**

1. System reads the provided snapshot as if a previous run had written it.
2. Use case continues at step 3.

### A5: Snapshot Not Writable

**Trigger:** The snapshot location cannot be written (step 7)  
**Flow:**

1. System logs a warning that names the snapshot location and keeps serving the live flag configurations.
2. Use case continues at step 8.

## Postconditions

### Success Postconditions

- flagd serves the live flag configurations of its sources.
- The snapshot holds the current flag state for the next restart.

### Failure Postconditions

- A snapshot flag never overrides a flag with the same key from a live source.
- An unreadable snapshot is never served as flag state.
- The snapshot is never replaced by a partly written flag state.

## Business Rules

### BR-001: Lowest Precedence

The snapshot is always the lowest-precedence source, whatever the order of the configured sources (FR-051).

### BR-002: Write on Every Change

flagd writes the snapshot after every change of the flag state it serves (FR-050).

### BR-003: Snapshot Is Optional

A distribution without the snapshot components, or a deployment without a snapshot location, behaves exactly as flagd without snapshots; Operators who do not use snapshots pay nothing for them.

### BR-004: Works With Every Sync Provider

The snapshot works with every sync provider without a change to that sync provider.
