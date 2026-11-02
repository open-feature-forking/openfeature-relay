# Use Case: Run Configuration Proxy

## Overview

**Use Case ID:** UC-017  
**Use Case Name:** Run Configuration Proxy  
**Primary Actor:** Operator  
**Secondary Actors:** In-Process Provider  
**Goal:** Give in-process providers one place to receive validated flag configurations from, by running a configuration proxy that reads the sources and serves the flag sync endpoint without evaluating any flag.  
**Trigger:** Operator decides that in-process providers receive their flag configurations from a configuration proxy instead of reading the sources themselves  
**Status:** Draft  

**Requirements:** [FR-025, FR-026, FR-022, NFR-011, C-005](../requirements.md)

## Preconditions

- A configuration proxy is available as a binary or image: the flagd-proxy standard distribution (UC-012) or a custom distribution without an evaluator (UC-001).
- Operator has the sources that hold the deployment's flag configurations and the start command, configuration file and environment variables flagd-full would use for them.
- The in-process providers of the deployment support the flag sync endpoint as their source.

## Main Success Scenario

1. Operator starts the configuration proxy with the deployment's sources.
2. System checks the scheme of every source against the sync providers compiled into the configuration proxy.
3. System starts a sync provider for each source and the flag sync endpoint.
4. Each sync provider delivers the flag configuration of its source.
5. System parses and validates each flag configuration with the configuration format and writes the accepted flags into the store.
6. System reports readiness on the management port once every source has delivered a flag configuration it accepted.
7. Operator configures the in-process providers of the deployment with the address of the configuration proxy's flag sync endpoint, each with the selector of the flags it needs.
8. In-Process Provider subscribes to the flag sync endpoint.
9. System sends the In-Process Provider the current flag configuration that matches its selector.
10. A source changes and its sync provider delivers the new flag configuration.
11. System validates the new flag configuration, updates the store and sends the changed flag configuration to every subscribed In-Process Provider whose selector it matches.
12. In-Process Provider evaluates flags locally with the flag configuration it received; the deployment's in-process providers get their flag configurations from one place while the configuration proxy evaluates nothing.

## Alternative Flows

### A1: Scheme Not in Distribution

**Trigger:** A source uses a scheme for which the configuration proxy contains no sync provider (step 2)  
**Flow:**

1. System refuses to start and names the source, its scheme and the schemes the configuration proxy supports.
2. Use case ends.

### A2: Initial Flag Configuration Rejected

**Trigger:** The configuration format rejects the first flag configuration of a source (step 5)  
**Flow:**

1. System logs an error naming the source and the faulty entry, writes nothing of that flag configuration into the store and does not report readiness.
2. Operator corrects the flag configuration in the source.
3. Use case continues at step 4.

### A3: Source Unavailable

**Trigger:** A sync provider cannot read its source (step 4)  
**Flow:**

1. System logs an error naming the source, keeps retrying it and does not report readiness until it has delivered an accepted flag configuration.
2. Use case continues at step 4.

### A4: Changed Flag Configuration Rejected

**Trigger:** The configuration format rejects the new flag configuration of a source (step 11)  
**Flow:**

1. System logs an error naming the source and the faulty entry, keeps the last flag configuration it accepted from that source in the store and sends nothing to the in-process providers.
2. Operator corrects the flag configuration in the source.
3. Use case continues at step 10.

### A5: In-Process Provider Reconnects

**Trigger:** An In-Process Provider loses its subscription, for example because the configuration proxy restarted (step 11)  
**Flow:**

1. In-Process Provider keeps evaluating with the last flag configuration it received and subscribes again.
2. Use case continues at step 9.

### A6: Evaluation Requested From the Proxy

**Trigger:** A client is configured for remote evaluation against the configuration proxy instead of the flag sync endpoint (step 7)  
**Flow:**

1. System serves no flag evaluation; the client's request finds no service endpoint for it.
2. Operator configures the client as an in-process provider, or points it at a distribution with an evaluator.
3. Use case continues at step 8.

## Postconditions

### Success Postconditions

- The configuration proxy holds in its store the accepted flag configuration of every source.
- Every subscribed In-Process Provider holds the flag configuration that matches its selector, as of the last change the configuration proxy accepted.
- The configuration proxy contains no evaluator and has evaluated no flag.

### Failure Postconditions

- No In-Process Provider ever receives a flag configuration that flagd-full would have rejected.
- A rejected flag configuration of one source never removes or changes the flags of another source.
- An In-Process Provider keeps the last flag configuration it received until the configuration proxy sends an accepted one.

## Business Rules

### BR-001: No Evaluator

A configuration proxy contains sync providers, a configuration format and the flag sync endpoint, and no evaluator. The flagd-proxy module graph contains no evaluator module and no `github.com/diegoholiveira/jsonlogic` module (NFR-011). Without an evaluator it contains neither the flag evaluation nor the OFREP service endpoint (UC-007 BR-001).

### BR-002: Same Validation as flagd-full

A configuration proxy validates flag configurations with the same configuration format, and therefore the same rules, as flagd-full (FR-026). A flag configuration flagd-full accepts the configuration proxy accepts, and a flag configuration flagd-full rejects the configuration proxy rejects.

### BR-003: Only Accepted Flag Configurations Are Forwarded

The flag sync endpoint sends only flags that are in the store, and the store holds only flags of flag configurations the configuration format accepted. A flag configuration is accepted or rejected as a whole; a part of it is never forwarded.

### BR-004: Same Flag Sync Contract as flagd-full

The flag sync endpoint of a configuration proxy is the same component as in flagd-full: same protocol, same selectors and the same merging of several sources by their order of precedence. An In-Process Provider cannot tell from its subscription whether it talks to a configuration proxy or to flagd-full, and needs no change to switch between them (C-005).

### BR-005: Sources Are Fixed at Startup

The sources a configuration proxy reads are given on the command line or in the configuration file when it starts. A selector chooses among the flags of these sources; it never makes the configuration proxy read a new source.
