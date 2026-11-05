# Use Case: Recover From Snapshot

## Overview

**Use Case ID:** UC-016  
**Use Case Name:** Recover From Snapshot  
**Primary Actor:** Operator  
**Goal:** Have a restarted flagd serve the last known flags immediately, even while its sources are unavailable, and switch to live flag configurations as soon as the sources deliver them.  
**Trigger:** Operator restarts a flagd distribution that writes a snapshot  
**Status:** Draft — Deferred (FR-050, FR-051 are planned for a later release)  

**Requirements:** [FR-050, FR-051, FR-052](../requirements.md)

## Preconditions

- The distribution contains a sync provider for snapshots and writes the snapshot on every change of its flag state.
- Operator has configured a snapshot location that persists across restarts.

## Main Success Scenario

1. Operator restarts flagd with its configured sources and snapshot location.
2. System reads the snapshot as the lowest-precedence source: for every document it restores the source, configuration format, flag set and document version.
3. System serves flags from the snapshot: an evaluating distribution answers evaluation requests and flagd-sync forwards the snapshot's flag configurations to in-process providers.
4. System starts a sync provider for every configured source.
5. Every configured source delivers a flag configuration that its configuration format accepts.
6. System merges the live flag configurations over the snapshot, so that live flags override snapshot flags with the same key.
7. System reports readiness on the management port.
8. System writes the new flag state with its document versions to the snapshot.
9. System serves flags from the live flag configurations; the restart caused no period without flags.

## Alternative Flows

### A1: Sources Remain Unavailable

**Trigger:** A configured source does not deliver a flag configuration (step 5)  
**Flow:**

1. System keeps serving flags from the snapshot, keeps retrying the source and does not report readiness, because readiness requires every configured source to have delivered.
2. The source becomes available and delivers a flag configuration that its configuration format accepts.
3. Use case continues at step 6.

### A2: Live Delivery Matches the Snapshot

**Trigger:** A source delivers a document whose document version equals the version restored from the snapshot (step 6)  
**Flow:**

1. System keeps the restored document and publishes no configuration change event for it, so that clients holding that version see no change.
2. Use case continues at step 7.

### A3: No Snapshot Yet

**Trigger:** The snapshot location holds no snapshot, as on the first start (step 2)  
**Flow:**

1. System starts without snapshot flags and serves no flags until a source has delivered, as without a snapshot.
2. Use case continues at step 4.

### A4: Snapshot Unreadable

**Trigger:** The snapshot is corrupt, or the distribution lacks a configuration format the snapshot's documents name, or that format does not accept them (step 2)  
**Flow:**

1. System logs a warning that names the snapshot location and starts without snapshot flags.
2. Use case continues at step 4.

### A5: Pre-Seeded Snapshot

**Trigger:** Operator provides a snapshot with the image or the deployment before the first start (step 1)  
**Flow:**

1. System reads the provided snapshot as if a previous run had written it.
2. Use case continues at step 2.

### A6: Snapshot Not Writable

**Trigger:** The snapshot location cannot be written (step 8)  
**Flow:**

1. System logs a warning that names the snapshot location and keeps serving the live flag configurations.
2. Use case continues at step 9.

### A7: Source Becomes Stale

**Trigger:** The last live contact with a source is older than its configured staleness limit (step 9)  
**Flow:**

1. System reports readiness as failed on the management port, keeps serving the last accepted flag configurations and keeps the snapshot unchanged.
2. The source delivers a flag configuration that its configuration format accepts.
3. Use case continues at step 6.

## Postconditions

### Success Postconditions

- flagd serves the live flag configurations of its sources and reports readiness.
- The snapshot holds the current flag state with its document versions for the next restart.

### Failure Postconditions

- A snapshot flag never overrides a flag with the same key from a live source.
- A snapshot its configuration format does not accept is never served.
- The snapshot is never replaced by a partly written flag state.
- Readiness is never reported before every configured source has delivered an accepted flag configuration, nor while a source is stale.

## Business Rules

### BR-001: Lowest Precedence

The snapshot is always the lowest-precedence source, whatever the order of the configured sources (FR-051).

### BR-002: Write on Every Change

flagd writes the snapshot after every change of the flag state it serves (FR-050).

### BR-003: Snapshot Is Optional

A distribution without snapshot support, or a deployment without a snapshot location, behaves exactly as flagd without snapshots; Operators who do not use snapshots pay nothing for them.

### BR-004: Works With Every Sync Provider and Configuration Format

The snapshot works with every sync provider and every configuration format without a change to them: it stores each document as its configuration format accepted it, with source, flag set and document version.

### BR-005: Snapshot Does Not Make flagd Ready

The snapshot is not a configured source for readiness: flagd serves flags from it before readiness, and reports readiness only when every configured source has delivered a flag configuration that its configuration format accepted.

### BR-006: Snapshot Does Not Reset Staleness

Staleness counts from a source's last live contact, never from the time its documents were restored from the snapshot (FR-052).

### BR-007: Restored Versions Are Kept

Document versions restored from the snapshot are the ones service endpoints use for ETags and event streams until a source delivers a different version.

## Prior Art

GO Feature Flag's relay proxy has a persistent flag configuration file: it writes the retrieved flag configurations to a local file and serves from it when no source is available at startup. UC-016 follows the same idea, with the snapshot as the lowest-precedence source and readiness kept separate.
