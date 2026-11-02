# Use Case: Inspect Distribution Components

## Overview

**Use Case ID:** UC-011  
**Use Case Name:** Inspect Distribution Components  
**Primary Actor:** Operator  
**Goal:** Learn which components a flagd binary or image contains, so that the Operator knows whether it supports the deployment's sources, service endpoints and telemetry before or after starting it.  
**Trigger:** Operator asks a flagd binary or image which components it contains  
**Status:** Draft  

**Requirements:** [FR-023](../requirements.md)

## Preconditions

- Operator has access to the flagd binary or image to inspect.

## Main Success Scenario

1. Operator runs `flagd components` with the binary or image to inspect.
2. System identifies the distribution by its name and release version.
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

**Trigger:** Operator inspects the binary because it refused to start with a source whose scheme it does not support (step 1)  
**Flow:**

1. Operator runs the command named in the startup error.
2. Use case continues at step 2.

### A3: Required Component Missing

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

Standard and custom distributions list their components in the same format, so that an Operator reads any distribution the same way.
