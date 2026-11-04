# Use Case: Run Provider Test Suites Against the Testbed

## Overview

**Use Case ID:** UC-022  
**Use Case Name:** Run Provider Test Suites Against the Testbed  
**Primary Actor:** Provider Author  
**Secondary Actors:** GitHub Actions, flagd Test Harness  
**Goal:** Run a provider's Gherkin suite against the flagd being released, with the outages the suite controls, without changing the suite.  
**Trigger:** A provider suite starts the testbed through the flagd-testbed compose file  
**Status:** Draft  

**Requirements:** [FR-056, FR-057, NFR-014, NFR-015](../requirements.md)

## Preconditions

- The testbed image is built from the manifest `distributions/flagd-testbed`.
- The flagd-testbed data (flags, configurations, certificates, compose file and Gherkin features) is the pinned `test-harness`, unmodified.

## Main Success Scenario

1. GitHub Actions starts the compose stack with the testbed image.
2. The control module combines the raw flags in memory and builds the runtime from the testbed configuration `default`, with `flags/allFlags.json` read from the testbed source. When the runtime is ready, the control module renders `allFlags.json` from the in-memory state and ports 8013 to 8015 open. Port 8080 of the control API opens only after the `default` configuration has started, as in flagd-testbed, and the suite waits for it.
3. The suite calls `/start` for the configuration `default` in every scenario. The testbed closes the gate, resets and reloads the store, opens the gate when the runtime is ready again, and answers.
4. The suite evaluates flags over RPC, in-process sync or the rendered file.
5. The suite calls `/change`. The testbed toggles `changing-flag` in the in-memory state, loads the new version into the store, renders `allFlags.json` atomically once the store holds it, and answers.
6. The suite calls `/restart?seconds=N`. The testbed answers at once, closes the gate and removes the rendered `allFlags.json`; providers see refused connections, broken streams and a missing file. After N seconds it starts as in step 3, which renders the file again.
7. The providers reconnect and fire their ready events; the suite passes the same scenarios as against the flagd-testbed image.

## Alternative Flows

### A1: Configuration Switch

**Trigger:** The suite calls `/start` with a configuration other than the one in use (step 3)  
**Flow:**

1. The testbed closes the gate and cancels the runtime.
2. The testbed builds a new runtime in the same process from that configuration and runs it; a `/start` with the configuration in use rebuilds nothing and is step 3. The control module stays up with its in-memory state, so toggles made by `/change` survive the switch.
3. When the runtime is ready, the testbed renders `allFlags.json`, opens the gate and answers `200`.
4. Use case continues at step 4.

### A3: Invalid Seconds

**Trigger:** `/restart` is called with `seconds` that is not a non-negative integer (step 6)  
**Flow:**

1. The testbed answers `400` with the message `'seconds' must be a non-negative integer` and changes nothing.
2. Use case ends.

### A4: Stop

**Trigger:** The suite calls `/stop` instead of `/restart` (step 6)  
**Flow:**

1. The testbed cancels a pending restart, closes the gate and answers `200`.
2. The gate stays closed until the next `/start`.
3. Use case ends.

### A7: Change Times Out

**Trigger:** The store does not hold the changed version within the change timeout of 5 seconds, the store rejects the load, or `/change` follows a switch that failed (step 5)  
**Flow:**

1. The testbed answers with a plain-text `500`.
2. Use case ends.

### A8: Runtime Not Ready in Time

**Trigger:** The runtime is not ready within the start timeout of 10 seconds (step 3, A1)  
**Flow:**

1. The testbed answers `500` with the message `Failed to start flagd: flagd health check timed out`.
2. The gate stays closed.
3. Use case ends.

### A9: Unknown Configuration

**Trigger:** `/start` names a configuration the testbed does not have (step 3)  
**Flow:**

1. The testbed answers at once, within 100 milliseconds, with `500` and the message `Failed to start flagd: unknown configuration "<name>"`.
2. The gate and the runtime stay as they were, and the last good configuration stays remembered.
3. Use case ends.

## Postconditions

### Success Postconditions

- The suite has passed the same scenarios against the testbed as against the flagd-testbed image.
- The runtime still runs; state outside the store (the event bus, the process start time) survives restarts.

### Failure Postconditions

- No file under the flags and configurations the testbed was built with has changed.
- A failed start leaves the last good configuration remembered.

## Business Rules

### BR-001: Compatible API

The control API keeps the Launchpad API of flagd-testbed v3.11.0: endpoints, paths, methods (any), payload shapes (JSON `{"status","message"}` with a trailing newline), messages and status codes, except four quirks that are fixed:

- a failed start no longer replaces the remembered configuration;
- an unknown configuration is answered at once (A9) and not after the readiness timeout;
- `/change` rewrites no file; it alters the in-memory state;
- an unknown configuration takes no port down and cancels no pending restart: flagd-testbed stops flagd before it starts the new configuration, so a `/start` with an unknown configuration takes 8013 to 8015 down, and the testbed leaves the gate, the runtime and a pending restart as they were (A9).

`/restart` answers at once and a later call cancels a pending restart; `/stop` cancels a pending restart; the start timeout is 10 seconds and the change timeout 5 seconds.

### BR-002: Outages Close Sockets

Closing the gate closes every listener of the runtime and gives every open connection a plain close, with no application-level goodbye, as when a process is killed; providers see refused connections and broken streams. The control port is outside the gate and stays up.

### BR-003: Cleaned Store per Start

Every start empties the store, re-synchronizes every source, and opens the gate only when every source has been accepted again.

### BR-004: The Runtime Reads No File the Testbed Changes

The combined flags reach the runtime through the testbed source; the selector and metadata flag files are read-only. The source files from `test-harness` never change.

### BR-005: Minimal Testbed

The testbed contains only the components a pinned provider suite exercises: the file and testbed sync providers, the flagd configuration format and evaluator, the flag evaluation and flag sync service endpoints, and its own `cmd`. It has no OFREP service endpoint, event collector, other sync provider or OpenTelemetry (noop telemetry).

### BR-006: Test-Only

No standard distribution contains a testbed module (NFR-015).

### BR-007: Data Stays Upstream's

Flags, configurations, certificates, the compose file and the Gherkin features are unmodified; the one rewrite, of the source `flags/allFlags.json` to the testbed source, happens when the configuration is loaded.

### BR-008: Reusable Control

The control module knows no flagd type; a target supplies the behaviour (start, stop, load, readiness), so the provider conformance suite or another vendor's testbed can reuse the control module, the API and the contract test unchanged (FR-056).

### BR-009: Rendered Output

`allFlags.json` is rendered from the in-memory state only after the store holds that state. It is byte-identical to what flagd-testbed v3.11.0 writes for the same state, it is written atomically with a temporary file and a rename in its directory, and the runtime never reads it. It is rendered at start, after every `/change` and after every start, and removed while a `/restart` keeps the gate closed.

## Accepted Test Gaps

- Fault injection and a public fetch call are follow-ups: the testbed has no faults endpoint, no `/fetch` and no evaluation-recording sink.
- The suites' `@customCert` and `@unixsocket` scenarios and the other languages' suites run manually in the proof of concept; the `ssl` configuration itself is tested automatically.
- The scenarios `@fractional-v1` and `@fractional-v2` stay excluded (tracked in #47).
