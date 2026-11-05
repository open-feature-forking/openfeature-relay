# Use Case: Develop Sync Provider Extension

## Overview

**Use Case ID:** UC-003  
**Use Case Name:** Develop Sync Provider Extension  
**Primary Actor:** Extension Author  
**Goal:** Publish a fetcher, a fetch trigger or a streaming sync provider in the Extension Author's own component module, verified against its contract, so that a distribution obtains flags from a kind of source, or reacts to a kind of change signal, that no official component supports.  
**Trigger:** Extension Author decides to make flagd read flags from a kind of source, or refetch on a kind of change signal, that no official component supports  
**Status:** Implemented  

**Requirements:** [FR-010, FR-045, FR-053, FR-054, FR-055, FR-015, FR-003, FR-008, FR-025, FR-029, FR-049, FR-052, C-001](../requirements.md)

## Preconditions

- A release of the core module that contains the sync provider and fetch trigger factory interfaces, the core's poller and their contract tests is available.
- The Extension Author has a Go module of their own in which the extension will live.

## Main Success Scenario

1. Extension Author adds the core module as a dependency of their component module.
2. Extension Author names the sync provider's type and the schemes of the sources it handles.
3. Extension Author implements the sync provider as a fetcher: given the source version of its previous fetch, it fetches the source once and returns the flag configuration as read, with its encoding when known and its new source version, or reports that nothing changed. The fetcher decides nothing about when to fetch.
4. Extension Author implements the factory that creates the fetcher for one source from the dependencies a distribution injects.
5. Extension Author runs the contract tests of the extension (include UC-008 Run Contract Tests).
6. System reports that the extension meets every case of its contract.
7. Extension Author tags and publishes the component module under its Go module path and version.
8. The extension is available to a Distribution Builder, who selects it in a manifest by module path and version exactly like an official component; for a fetcher, the Operator attaches fetch triggers to each of its sources, the core's poller by default.

## Alternative Flows

### A1: Source Pushes Changes Over Its Own Protocol

**Trigger:** The source holds a connection open and pushes each change itself, as flagd's gRPC sync protocol and the OpenFeature Operator's FeatureFlag watch do, so fetching on a signal does not fit (step 3)  
**Flow:**

1. Extension Author implements the sync provider as a streaming sync provider: it connects to the source, delivers the source's complete flag configuration as soon as it can, delivers every change the source pushes, delivers the complete flag configuration again on request, and reports whether it is ready to receive changes.
2. Extension Author leaves decoding and validating each flag configuration to flagd, as a fetcher does.
3. Use case continues at step 4.

### A2: Only the Change Signal Is New

**Trigger:** An official or existing fetcher already reads the source, and only the source's change signal, such as a vendor's change event stream, is not yet understood (step 2)  
**Flow:**

1. Extension Author names the fetch trigger's type instead of a sync provider.
2. Extension Author implements the fetch trigger: it listens for the change signal and tells the fetcher of its source to fetch each time the signal arrives; it never fetches or delivers flag configurations itself.
3. Extension Author implements the factory that creates the fetch trigger for one source from the dependencies a distribution injects.
4. Use case continues at step 5.

### A3: Scheme Not Accepted

**Trigger:** The contract tests reject a declared scheme because it is empty (step 5)  
**Flow:**

1. System reports the rejected scheme and the scheme rule it breaks.
2. Extension Author corrects the declared schemes.
3. Use case continues at step 5.

### A4: Contract Case Fails

**Trigger:** At least one case of the extension's contract fails (step 6)  
**Flow:**

1. System reports each failed case with its expected and its observed behaviour.
2. Extension Author corrects the extension or its factory.
3. Use case continues at step 5.

### A5: Try Extension in a Distribution Before Publishing

**Trigger:** Extension Author wants to see the extension running in flagd before releasing it (step 7)  
**Flow:**

1. Extension Author builds a custom distribution whose manifest replaces the component module with the local checkout (UC-001 Build Custom Distribution).
2. Extension Author starts the distribution with a source the extension handles, changes the source, and confirms that the changed flags are served.
3. Use case continues at step 7.

### A6: Extension Author Abandons the Extension

**Trigger:** Extension Author stops before the component module is published (step 7)  
**Flow:**

1. Extension Author leaves the component module unpublished.
2. Use case ends.

## Postconditions

### Success Postconditions

- A tagged version of the component module exists under the Extension Author's Go module path.
- The component module provides exactly one fetcher, fetch trigger or streaming sync provider through its factory.
- That version of the extension passes every case of its contract in the core module version it depends on.

### Failure Postconditions

- No version of the component module is published that fails a case of its contract.
- No official component module, standard distribution or the core module is changed.

## Business Rules

### BR-001: Core Module Is the Only flagd Dependency

A sync provider or fetch trigger component module depends on the core module and on no other flagd module: not on the runtime, a service endpoint, another sync provider or a standard distribution. Its own third-party dependencies are compiled only into distributions that select it.

### BR-002: Schemes Select the Sync Provider

Every declared scheme is a non-empty prefix of a source, such as `consul://` or `core.openfeature.dev/`; it need not be a URI scheme. In a distribution, a source is handed to the sync provider whose scheme it starts with, and one scheme belongs to at most one sync provider of that distribution; a clash between two selected sync providers is reported when the distribution is built (UC-001), not by this use case. A fetch trigger declares no scheme; the Operator attaches it to a source.

### BR-003: Fetch Triggers Only Signal

A fetch trigger tells its source's fetcher to fetch and does nothing else. Several fetch triggers may be attached to one source, such as a server-sent event stream with the core's poller as fallback (FR-053); every signal from any of them leads to a fetch. A fetch trigger that loses its connection to the change signal reconnects by itself and never stops the other fetch triggers of its source.

### BR-004: Fetchers Answer Conditionally

A fetcher reports "not changed" when the source version of the source equals the one it was given, and then delivers nothing; the previous flag configuration stays in force. A fetch that fails delivers nothing either. At most one fetch of a source runs at a time; a signal that arrives during a fetch leads to exactly one further fetch after it.

### BR-005: First Delivery as Soon as Possible

The source's complete flag configuration is delivered as soon as it can be read: a fetcher is asked to fetch once at startup before any fetch trigger signals, and a streaming sync provider delivers as soon as it has connected. A distribution reaches readiness only after every configured source has delivered a flag configuration that its configuration format accepted, and, for a source with a staleAfter setting, loses it again while the last contact with that source is older than staleAfter. The last contact is a successful fetch, whether or not it changed anything, or the time a stream was last open; a source that delivers only when it changes is therefore not stale while it is reachable. A source without the setting never goes stale.

### BR-006: Every Delivery Names Its Source

Each delivery carries the source it came from, so that the runtime attributes flag configurations, readiness and configuration change events to the right source.

### BR-007: Complete Configuration Unless Incremental Updates Are Enabled

Each delivery replaces all flags of its source, unless the Operator has enabled incremental updates for that source; then each delivery is an incremental update and replaces only the flag sets it contains.

### BR-008: Recoverable Source Errors Do Not Stop flagd

Errors the extension can recover from, such as an unreachable source, never stop the distribution, not even a failed first fetch or first connection: a fetcher reports the failed fetch and is fetched again after a backoff; a streaming sync provider reconnects. The sync provider stays up and not ready until its first delivery. A sync provider that loses its source after a first delivery reconnects and stays ready with its last accepted flag configuration; readiness is lost only while the last contact with a source that sets staleAfter is older than that (BR-005). A streaming sync provider ends with an error only when it can never deliver again, because that error stops the whole distribution. A source that cannot be configured, such as a file path that does not exist at startup, is a configuration error and still stops flagd at startup.

### BR-009: Stop on Shutdown

When the distribution shuts down, a fetcher abandons a running fetch, a fetch trigger stops listening, and a streaming sync provider stops receiving and delivering; each returns without an error.

### BR-010: Encoding- and Format-Neutral Delivery

A sync provider does not decode, parse, validate or transform the flag configuration it obtains. It may take the flag configuration out of its source's protocol envelope (the `flagConfiguration` field of a flagd sync message, the `spec.flagSpec` of a FeatureFlag resource) but never changes it. The core decodes its encoding (JSON or YAML), and the configuration format the source names validates it, flagd's being the default (FR-029). A sync provider therefore works with every encoding and configuration format and in a configuration proxy, which has no evaluator, and an invalid flag configuration is rejected the same way whichever sync provider delivered it.

### BR-011: Same Selection as Official Components

An extension is referenced in a manifest by Go module path and version in the same way as an official fetcher, fetch trigger or streaming sync provider, such as `sync/http` or `sync/flagd-grpc`; no registration with the Maintainers is needed.

### BR-012: Sources That Disappear

A deleted file is an empty flag configuration: the sync provider delivers `{}`, which removes the flags of its source. A deleted FeatureFlag resource delivers nothing, and the flags last accepted from it stay in force. A file that exists but cannot be read, and content that cannot be decoded, deliver nothing and keep the last accepted flags.

## Delivered by Later Use Cases

This use case is `Implemented` for the sync provider side: the factory interfaces, the fetching sync provider, the fetch triggers and the contract tests are in the core module, the official sync providers are created through their factories, and `test/sync-example` shows an Extension Author's module. These parts are delivered by the use cases named here, and are accepted gaps of this one:

- Steps 7–8 (publishing, and selecting the extension in a manifest), A5, BR-011 and the scheme clash of BR-002 at build time: UC-001.
- Telemetry injected generically: the gRPC dial options and the stream recorder stay fields of the `flagd-grpc` factory until UC-006 supplies them through the telemetry provider.
- Fetch triggers that need an inbound endpoint or a vendor: a webhook, server-sent events and vendor change events (UC-019). Notifying a system of a change is an event sink (UC-020).
- The official `http` fetcher answers conditionally with the ETag and the body digest only; sources that answer `If-Modified-Since`, such as the Flagsmith environment document, need their own fetcher (UC-019).
- The opt-in reconnect, fatal-code, keepalive, stream deadline, selector header and readiness settings of the `grpc` sync provider, which the Go flagd provider has: UC-023.
- flagd-proxy keeps `SyncFromURI`. Not delivered: flagd-proxy stays a separate program (UC-017 BR-007, UC-012 BR-013).
- The exported names this use case removed (`NewHTTP`, `NewFileSync`, the fields of `file.Sync`, `polling.Poller`, `CronPoller`, `NewCronPoller`, `NewSourcePoller`, `MockPoller`, `gcs.New`, `azblob.New`, `s3.New`, and the module `sync/blob` with `blob.NewSync`, `blob.Factory`, `blob.Fetcher` and the fields of `blob.Sync`; each cloud module now exports `NewFactory`) go into the migration guide of #156 (provider migration, after the PoC).
