# Use Case: Sync From an Upstream Flag Sync Endpoint

## Overview

**Use Case ID:** UC-023
**Use Case Name:** Sync From an Upstream Flag Sync Endpoint
**Primary Actor:** Operator
**Secondary Actors:** Upstream Flag Sync Endpoint
**Goal:** Configure a `grpc` source to survive the outages and misconfigurations of the upstream flag sync endpoint as the flagd providers do, with nothing changing for sources that do not opt in.
**Trigger:** Operator wants a `grpc` source to reconnect on the provider's schedule, end flagd on a fatal status code, recycle its stream, keep its connection alive, send its selector as a header or report readiness on its first payload
**Status:** Draft

**Requirements:** [FR-058, FR-059, FR-060, FR-061, FR-062, NFR-016](../requirements.md)

## Preconditions

- The distribution contains the `grpc` sync provider (`sync/flagd-grpc`).
- The upstream is a flag sync endpoint: flagd, a configuration proxy, flagd-proxy or a vendor's.
- The source is configured with `--sources` or the configuration file; the settings have no command line flag.

## Main Success Scenario

1. Operator sets any of the keys of a `grpc` object on a `grpc` source: `retryBackoffMs`, `retryBackoffMaxMs`, `streamDeadlineMs`, `keepAliveTime`, `fatalStatusCodes`, `selectorHeader` or `readyOnFirstPayload`.
2. System validates the object before it starts anything.
3. System syncs the source. After an error it re-opens the stream with the timing of the retry settings (BR-003).
4. System recycles the stream when its deadline elapses, without an error and without a delay (BR-004).
5. System keeps the connection alive with HTTP/2 pings (BR-005).
6. System sends the source's selector as the `flagd-selector` header as well as in the request body (BR-006).
7. System reports the source ready as it does today, or after its first payload with `readyOnFirstPayload` (BR-007).

## Alternative Flows

### A1: Fatal Status Code Before the First Payload

**Trigger:** The upstream answers with a code of `fatalStatusCodes` before the source's first payload (step 3)
**Flow:**

1. System ends flagd, naming the source and the code, as it does for a fetching source that is refused before its first fetch.
2. Operator corrects the credentials or the upstream, and starts flagd again.
3. Use case ends.

### A2: Invalid Setting

**Trigger:** The `grpc` object holds a setting the system rejects (step 2)
**Flow:**

1. System refuses to start and names the source and the setting, for a code name that is not a gRPC status code, a negative duration, `retryBackoffMs` above `retryBackoffMaxMs`, `keepAliveTime` from 1 to 9999, `selectorHeader` together with a `flagd-selector` entry in the source's `headers`, an unknown key or a value of the wrong type inside the `grpc` object, and a `grpc` object on a source whose provider is not `grpc`.
2. Operator corrects the setting.
3. Use case continues at step 1.

### A3: Upstream Outage

**Trigger:** The upstream refuses connections or ends the stream (step 3)
**Flow:**

1. System retries as BR-003 sets, and the source keeps its last accepted flags meanwhile.
2. The upstream returns, and the use case continues at step 3.

### A4: Source Without a `grpc` Object

**Trigger:** A `grpc` source has no `grpc` object (step 1)  
**Flow:**

1. System behaves as before this use case (NFR-016). Keys named as the flagd providers name them, set directly on the source, stay ignored as they are.
2. A configuration that already carried a key named `grpc` on a source has it read and validated now, and flagd stops at startup when its content is malformed.
3. Use case ends.

## Postconditions

### Success Postconditions

- The source syncs with the settings the Operator opted into; every other source, and every `grpc` source without a `grpc` object, behaves as before.
- Each rejected setting has stopped startup before a connection was opened.

### Failure Postconditions

- A fatal status code ends flagd before the source's first payload; after it the code is retried like any error.
- A `grpc` object never changes a source whose provider is not `grpc`: the system refuses to start instead.

## Business Rules

### BR-001: Opt-In Only

Every setting is opt-in, inside the `grpc` object of the source. Absent, `0` or `false` mean not set, key by key, and an empty `grpc` object changes nothing. Where the Go flagd provider behaves differently from flagd today, flagd's behaviour is the default and the provider's is opt-in.

### BR-002: Strict Object

The `grpc` object is decoded strictly: an unknown key or a value of the wrong type stops startup, naming the source and the key. The rest of the source keeps its lenient decoding. The existing settings of a `grpc` source (`tls`, `certPath`, `providerID`, `selector`, `maxMsgSize`, `headers`, `headersFrom`, `incrementalUpdates`, `staleAfter`) stay where they are, and `headersFrom` is resolved as it is for every other source (FR-062).

### BR-003: Provider Retry Timing

With `retryBackoffMs` or `retryBackoffMaxMs` set, the connection retries an RPC up to four times on `UNKNOWN` and `UNAVAILABLE`, from `retryBackoffMs` up to `retryBackoffMaxMs`, doubling; the system waits `retryBackoffMaxMs` before it re-opens a stream after an error, and `SyncFlags` waits for the connection to be ready. If only one key is set, the provider's default fills the other (1000 and 5000 milliseconds). The timings are lower bounds, because the layers add up.

### BR-004: Stream Deadline

With `streamDeadlineMs` set, the stream is re-opened at once when the deadline elapses while the source is running, with no error, no delay and no failure in the stream metrics.

### BR-005: Keepalive

`keepAliveTime` is in milliseconds; absent and `0` send no pings, as today, where the provider's `0` pings every 10 seconds. A value from 1 to 9999 is refused, because gRPC raises any interval below 10 seconds to 10 seconds. From 10000 on, the connection pings at that interval with a timeout of 5 seconds, also without an open stream. An upstream with default gRPC enforcement, such as flagd-proxy, answers faster pings with `GOAWAY` and `too_many_pings`.

### BR-006: Selector Header

With `selectorHeader`, the `flagd-selector` header carries the selector on `SyncFlags` and `FetchAllFlags`, next to the selector field of the request body. A `flagd-selector` entry in `headers` on the same source is refused.

### BR-007: Readiness

By default the sync provider is ready when its stream opens. With `readyOnFirstPayload` it is ready after its first payload, and never when the stream fails before one. The runtime's readiness waits for an accepted flag configuration per source either way.

### BR-008: Fatal Codes

`fatalStatusCodes` lists gRPC status code names. A listed code before the source's first payload ends flagd (A1); the set belongs to the source, so two sources may list different codes. One misconfigured source then stops the sources next to it.

### BR-009: Dial Options Override

When an embedder replaces the dial options, the selector header still applies on top of them, and the retry settings and the keepalive do not, because the override owns the dial. The distribution's instrumentation applies on both paths.
