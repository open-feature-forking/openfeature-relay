---
description: Components, component types, distributions and the builder
---

# Concepts

Every term on this page is defined in the [glossary](../glossary.md); the glossary wins where the two differ.

## Components and component types

A **component** is a selectable unit compiled into a distribution.
A **component module** is a Go module that provides one component through a **factory**, the interface a distribution uses to create it from injected dependencies.

| Component type | What it does | In flagd-full |
|----------------|--------------|---------------|
| Sync provider | Reads flag configurations from a kind of source and emits them; a source's scheme (`file:`, `https://`, `s3://`, …) selects the sync provider | file, HTTP, gRPC, Kubernetes, GCS, Azure Blob, S3 |
| Configuration format | Validates flag configurations of one format and keeps them in the store as documents, reporting each document's flag keys, flag set, version and changes | flagd's JSON schema |
| Evaluator | Resolves one, several or all flags of a selector for an evaluation context from the documents of its paired configuration format; emits no telemetry | flagd's targeting rules |
| Service endpoint | Serves flags to clients over a protocol | flag evaluation (gRPC/Connect), OFREP, flag sync, event collector (off until configured) |
| Middleware | Wraps a service endpoint's HTTP handler with cross-cutting behaviour | none; configured in flagd's configuration file |
| Telemetry provider | Supplies tracing, metrics and HTTP instrumentation to service endpoints and middleware | OpenTelemetry |
| Event sink | Delivers configuration change, evaluation and tracking events to an external system | webhook (off until configured) |

A component written outside the Maintainers' modules is an **extension**.
A module that ships a configuration format together with its evaluator, as vendors ship their engines, is an **engine module**.

## Configuration formats and documents

A source names its configuration format; flagd's format is the default.
The format turns a flag configuration into **documents**, one per flag set and source, and keeps them in the **store** with a **document version**.
Only its paired evaluator reads them, so flagd's flag model stays unchanged while a vendor's documents stay in the vendor's model.
Service endpoints derive ETags and event stream events from document versions.

## Distributions, manifest and builder

A **distribution** is a binary, and its image, built from one **manifest**: a YAML file that names the distribution and lists the component modules and versions it is built from.
The **builder** turns a manifest into generated Go source and compiles it.
The Maintainers publish **standard distributions**; anything else is a **custom distribution**. See [Distributions](distributions.md).

## Roles

| Role | Does |
|------|------|
| Operator | runs a distribution's binary or image, as every flagd adopter does today |
| Distribution Builder | composes a custom distribution from a manifest |
| Extension Author | writes a component in their own Go module |
| Provider Author | maintains an in-process provider that embeds the evaluator |
| Maintainer | maintains the component modules and the standard distributions |

What each role does, step by step, is in the [use cases](../use_cases/UC-001-build-custom-distribution.md).
