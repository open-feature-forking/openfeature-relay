# Use Case: Inspect Distribution Components

## Overview

**Use Case ID:** UC-011  
**Use Case Name:** Inspect Distribution Components  
**Primary Actor:** Operator  
**Goal:** Learn which components a flagd binary or image contains, so that the Operator knows whether it supports the deployment's sources, service endpoints and telemetry before or after starting it.  
**Trigger:** Operator asks a flagd binary or image which components it contains  
**Status:** Implemented  

**Requirements:** [FR-023, FR-034](../requirements.md)

## Preconditions

- Operator has access to the flagd binary or image to inspect.

## Main Success Scenario

1. Operator runs `flagd components` with the binary or image to inspect.
2. System identifies the distribution by its name and manifest version.
3. System lists the components compiled into the binary, grouped by component type: sync providers, configuration formats, evaluators, service endpoints, telemetry providers and middleware.
4. System shows, for every sync provider, the schemes it handles.
5. System shows, for every component, the component module and version it comes from.
6. Operator compares the list with the schemes of the deployment's sources, the configuration format of its flag configurations, the service endpoints its clients use and its telemetry needs, and knows whether the distribution supports the deployment.

## Alternative Flows

### A1: Binary Without Components Command

**Trigger:** The binary is a flagd release from before the builder and does not know the command (step 1)  
**Flow:**

1. System reports an unknown command.
2. Operator treats the binary as containing every component of flagd-full.
3. Use case ends.

### A2: Inspection After a Startup Failure

**Trigger:** Operator inspects the binary because it refused to start with a source whose scheme it does not support or a setting of a component it does not contain (step 1)  
**Flow:**

1. Operator runs the command named in the startup error.
2. Use case continues at step 2.

### A3: Listing for Tooling

**Trigger:** Operator asks for the listing as JSON, for a script or a deployment check to read (step 1)  
**Flow:**

1. System returns the distribution name, manifest version, components, schemes and component module versions as one JSON document with the same content as the text listing.
2. Operator's tooling checks the document against the deployment's needs.
3. Use case continues at step 6.

### A4: Required Component Missing

**Trigger:** A scheme, configuration format, evaluator, service endpoint or telemetry provider the deployment needs is not in the list (step 6)  
**Flow:**

1. Operator selects a standard distribution that contains it (UC-010) or asks a Distribution Builder for a custom distribution (UC-001).
2. Use case ends.

## Postconditions

### Success Postconditions

- Operator knows every component the binary contains, the schemes its sync providers handle and the component module versions.

### Failure Postconditions

- The inspection starts no sync provider, service endpoint or telemetry provider and opens no port.

## Business Rules

### BR-001: List Matches Binary

The list shows exactly the components compiled into the binary, no more and no fewer; it is derived from the binary itself, not from a separate file.

### BR-002: No Runtime Required

The command needs no source, configuration file or network access and returns without starting the runtime.

### BR-003: Same Output for Every Distribution

Standard and custom distributions list their components in the same text and JSON layout, so that an Operator and their tooling read any distribution the same way (FR-023).

### BR-004: Same Identity as Version

The distribution name and version in the listing are the ones `flagd version` reports with `--verbose` (FR-034); the plain output stays as it is.

## Delivered by Later Use Cases

This use case is `Implemented`: the hidden `flagd components` command lists the components of any distribution as text and as JSON with the distribution's identity, the schemes of its sync providers and the module versions, reads no configuration and opens no port. This part is delivered by the use case named here, and is an accepted gap of this one:

| Gap | Delivered by |
|-----|--------------|
| A2: the startup errors for an unsupported scheme or a setting of an absent component name `flagd components` (flagd-full keeps today's error text) | UC-010 |
