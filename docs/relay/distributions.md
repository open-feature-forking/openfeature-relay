---
description: Standard, custom and vendor distributions
---

# Distributions

A distribution contains exactly the components its manifest selects.
The builder builds flagd-full and the four smaller standard distributions from their manifests in this repository; none of them is released as an image or an archive yet, so today flagd built from this repository is flagd-full.

## Standard distributions

| Distribution | Contents | Purpose |
|--------------|----------|---------|
| **flagd-full** | every component maintained in this repository | today's flagd: same CLI, configuration, ports and endpoints |
| flagd-minimal | file, http and gRPC sync providers, flagd's configuration format and evaluator, the flag evaluation, OFREP and flag sync endpoints, and the noop telemetry provider; no Kubernetes client, cloud SDK or OpenTelemetry SDK; at most half the size of flagd-full | a small flagd that records no telemetry |
| flagd-cloud | the sync providers of flagd-minimal plus Google Cloud Storage, Azure Blob Storage and S3, all three endpoints, OpenTelemetry | flagd for cloud deployments |
| flagd-kubernetes | the sync providers of flagd-minimal plus the OpenFeature Operator source, all three endpoints, OpenTelemetry | flagd for Kubernetes deployments |
| **flagd-sync** | every sync provider of flagd-full, flagd's configuration format and the flag sync endpoint, OpenTelemetry; no evaluator | a **configuration proxy**: in-process providers get validated flag configurations from one place |

Today's flagd-proxy stays a separate program.
Each standard distribution's manifest is kept in this repository and accepts flagd-full's settings for the components it contains; see the use cases ([UC-010](../use_cases/UC-010-adopt-standard-distribution.md), [UC-012](../use_cases/UC-012-release-distributions-and-component-modules.md)).

## Custom distributions

A Distribution Builder writes a manifest that lists official and third-party component modules, and the builder compiles it ([UC-001](../use_cases/UC-001-build-custom-distribution.md)).
The builder rejects a manifest whose components need a component it does not select, such as OFREP without an evaluator.

## Vendor distributions

A vendor distribution puts another vendor's sync provider, configuration format and evaluator behind OFREP — for example the Flagsmith engine or GO Feature Flag's evaluation module — so it can serve OpenFeature clients in place of that vendor's edge or relay proxy ([UC-019](../use_cases/UC-019-serve-vendor-flags.md)).
Polling, the store, change events, OFREP with its event stream, flag sync, telemetry and event delivery are shared; the flag model, rules, bucketing and identity mapping stay the vendor's.
Vendor components are for Extension Authors to ship, not the Maintainers.
The [cross-vendor evaluation](../research/cross-vendor-evaluation.md) checks the component model against flagd, Flagsmith and GO Feature Flag.
