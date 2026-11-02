---
description: Standard, custom and vendor distributions
---

# Distributions

A distribution contains exactly the components its manifest selects.
None of these is built by the builder yet; today flagd built from this repository is flagd-full.

## Standard distributions

| Distribution | Contents | Purpose |
|--------------|----------|---------|
| **flagd-full** | every official component | today's flagd: same CLI, configuration, ports, endpoints and images |
| flagd-minimal | no Kubernetes client, cloud SDK or OpenTelemetry SDK; at most half the size of flagd-full | a small flagd |
| flagd-cloud | not specified yet | named for cloud deployments |
| flagd-kubernetes | not specified yet | named for Kubernetes deployments |
| **flagd-sync** | sync providers, flagd's configuration format and the flag sync endpoint, no evaluator | a **configuration proxy**: in-process providers get validated flag configurations from one place |

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
