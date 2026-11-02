# Use Case: Run Configuration Proxy

## Overview

**Use Case ID:** UC-017  
**Use Case Name:** Run Configuration Proxy  
**Primary Actor:** Operator  
**Secondary Actors:** In-Process Provider  
**Goal:** Give in-process providers one place to receive validated flag configurations from, by running a configuration proxy that reads the sources and serves the flag sync endpoint without evaluating any flag.  
**Trigger:** Operator decides that in-process providers receive their flag configurations from a configuration proxy instead of reading the sources themselves  
**Status:** Draft  

**Requirements:** [FR-025, FR-026, FR-022, FR-029, FR-044, FR-048, NFR-011, C-005](../requirements.md)

## Preconditions

- A configuration proxy is available as a binary or image: the flagd-sync standard distribution (UC-012), or a custom distribution without evaluation service endpoints (UC-001), such as a vendor configuration proxy built from a vendor's engine module.
- Operator has the sources that hold the deployment's flag configurations and the settings, as flagd-full's CLI flags, configuration file keys or environment variables, for the components a configuration proxy contains: sources, the flag sync endpoint and the management port.
- The in-process providers of the deployment support the flag sync endpoint as their source.

## Main Success Scenario

1. Operator starts the configuration proxy with the deployment's sources, each naming its configuration format or leaving flagd's as the default, and the settings of its components.
2. System checks every setting against the components compiled into the configuration proxy, the scheme of every source against its sync providers and the configuration format of every source against its configuration formats.
3. System starts a sync provider for each source and the flag sync endpoint.
4. Each sync provider delivers the flag configuration of its source.
5. System parses and validates each flag configuration with the configuration format of its source and keeps the accepted flag configuration in the store as that configuration format's documents.
6. System reports readiness on the management port once every configured source has delivered a flag configuration its configuration format accepted.
7. Operator configures the in-process providers of the deployment with the address of the configuration proxy's flag sync endpoint, each with the selector of the flags it needs.
8. In-Process Provider subscribes to the flag sync endpoint with its selector, naming the configuration format it reads or none for flagd's.
9. System sends the In-Process Provider the documents of the requested configuration format in the store that match its selector, unchanged.
10. A source changes and its sync provider delivers the new flag configuration.
11. System validates the new flag configuration, updates the store and sends the changed documents to every subscribed In-Process Provider whose configuration format and selector they match.
12. In-Process Provider evaluates flags locally with the flag configuration it received; the deployment's in-process providers get their flag configurations from one place while the configuration proxy evaluates nothing.

## Alternative Flows

### A1: Scheme Not in Distribution

**Trigger:** A source uses a scheme for which the configuration proxy contains no sync provider (step 2)  
**Flow:**

1. System refuses to start and names the source, its scheme and the schemes the configuration proxy supports.
2. Use case ends.

### A2: Setting for an Absent Component

**Trigger:** A setting of the start command, configuration file or environment variables belongs to a component the configuration proxy does not contain, such as an OFREP port or an OpenTelemetry collector address (step 2)  
**Flow:**

1. System refuses to start and names each such setting and the component it belongs to.
2. Operator removes the settings, or runs flagd-full where the deployment needs those components.
3. Use case continues at step 1.

### A3: Initial Flag Configuration Rejected

**Trigger:** The configuration format rejects the first flag configuration of a source (step 5)  
**Flow:**

1. System logs an error naming the source and the faulty entry, keeps nothing of that flag configuration in the store and does not reach readiness.
2. Operator corrects the flag configuration in the source.
3. Use case continues at step 4.

### A4: Source Unavailable

**Trigger:** A sync provider cannot read its source (step 4)  
**Flow:**

1. System logs an error naming the source and keeps retrying it; the configuration proxy does not reach readiness until the source has delivered a flag configuration its configuration format accepted.
2. Use case continues at step 4.

### A5: Changed Flag Configuration Rejected

**Trigger:** The configuration format rejects the new flag configuration of a source (step 11)  
**Flow:**

1. System logs an error naming the source and the faulty entry, keeps the last flag configuration it accepted from that source in the store and sends nothing to the in-process providers.
2. Operator corrects the flag configuration in the source.
3. Use case continues at step 10.

### A6: In-Process Provider Reconnects

**Trigger:** An In-Process Provider loses its subscription, for example because the configuration proxy restarted (step 11)  
**Flow:**

1. In-Process Provider keeps evaluating with the last flag configuration it received and subscribes again.
2. Use case continues at step 9.

### A7: In-Process Providers of a Vendor

**Trigger:** The in-process providers read the vendor's flag configurations through the vendor's own sync protocol, such as Flagsmith's environment document endpoint or GO Feature Flag's flag configuration endpoint, instead of the flag sync endpoint (step 7)  
**Flow:**

1. Operator runs a vendor configuration proxy that also contains a service endpoint extension serving the vendor's sync protocol (UC-004).
2. Operator configures the vendor's in-process providers with the address of that service endpoint.
3. In-Process Provider requests the flag configuration through the vendor's sync protocol.
4. System answers with the vendor's documents in the store that match the request's selector, unchanged, and answers a repeated request with no change when their document versions are unchanged.
5. Use case continues at step 10.

### A8: Evaluation Requested From the Proxy

**Trigger:** A client is configured for remote evaluation against the configuration proxy instead of the flag sync endpoint (step 7)  
**Flow:**

1. System serves no flag evaluation; the client's request finds no service endpoint for it.
2. Operator configures the client as an in-process provider, or points it at a distribution with an evaluator.
3. Use case continues at step 8.

### A9: Configuration Format Not in Distribution

**Trigger:** A source names a configuration format the configuration proxy does not contain (step 2)  
**Flow:**

1. System skips the source, logs an error naming the source and the configuration formats the configuration proxy contains, and leaves the source out of readiness (UC-018 BR-011).
2. Use case continues at step 3 with the other sources.

## Postconditions

### Success Postconditions

- The configuration proxy holds in its store the accepted flag configuration of every source, as documents of that source's configuration format.
- Every subscribed In-Process Provider holds the flag configuration that matches its selector, as of the last change the configuration proxy accepted.
- The configuration proxy serves no flag evaluation and has evaluated no flag.

### Failure Postconditions

- No In-Process Provider ever receives a flag configuration that flagd-full would have rejected.
- A rejected flag configuration of one source never removes or changes the flags of another source.
- An In-Process Provider keeps the last flag configuration it received until the configuration proxy sends an accepted one.

## Business Rules

### BR-001: No Evaluation

A configuration proxy contains sync providers, configuration formats and service endpoints that forward flag configurations, and neither the flag evaluation nor the OFREP service endpoint; it resolves no flag. The flagd-sync module graph contains no evaluator module and no `github.com/diegoholiveira/jsonlogic` module (NFR-011). A vendor configuration proxy carries the vendor's engine module, which holds the vendor's evaluator next to its configuration format (FR-048); that evaluator is compiled in but never resolves a flag.

### BR-002: Same Validation as flagd-full

A configuration proxy validates flag configurations with the same configuration format, and therefore the same rules, as flagd-full (FR-026). A flag configuration flagd-full accepts the configuration proxy accepts, and a flag configuration flagd-full rejects the configuration proxy rejects.

### BR-003: Only Accepted Flag Configurations Are Forwarded

The flag sync endpoint sends only documents that are in the store, and the store holds only flag configurations their configuration format accepted. A configuration format accepts a flag configuration as a whole, rejects it as a whole, or accepts it without entries it drops and reports one by one (UC-018 BR-001); a rejected or dropped entry is never forwarded.

### BR-004: Documents of the Requested Format, Unchanged

A flag sync request may name the configuration format it reads; a request that names none reads flagd's (FR-044). The flag sync endpoint sends only documents of that configuration format, unchanged; it never converts them into another configuration format or into the flag model. Today's in-process providers name no format and receive only flagd documents.

### BR-005: Same Flag Sync Contract as flagd-full

The flag sync endpoint of a configuration proxy is the same component as in flagd-full: same protocol, same selectors, the same default to flagd's configuration format and the same merging of several sources by their order of precedence. For flagd's configuration format an In-Process Provider cannot tell from its subscription whether it talks to a configuration proxy or to flagd-full, and needs no change to switch between them (C-005).

### BR-006: Settings Only for Contained Components

A configuration proxy accepts flagd-full's CLI flags, configuration file keys and environment variables for the components it contains, with the same meaning. It refuses to start on a setting for a component it lacks; it does not accept flagd-full's whole start command.

### BR-007: Sources Are Fixed at Startup

The sources a configuration proxy reads are given on the command line or in the configuration file when it starts. A selector chooses among the flags of these sources; it never makes the configuration proxy read a new source. Starting a sync for a source on a subscriber's request is what today's flagd-proxy does; it stays a separate program and is not a configuration proxy.

### BR-008: Vendor Sync Protocols Are Service Endpoint Extensions

A vendor's own sync protocol for its in-process providers, such as Flagsmith's environment document endpoint or GO Feature Flag's flag configuration endpoint, is a service endpoint extension. It reads the vendor's documents and their document versions from the same store as the flag sync endpoint and forwards them unchanged; the configuration proxy's validation applies to it alike.
