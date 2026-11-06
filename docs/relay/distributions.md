---
description: Standard, custom and vendor distributions
---

# Distributions

A distribution contains exactly the components its manifest selects.
The builder builds flagd-full and the five smaller standard distributions from their manifests in this repository; none of them is released as an image or an archive yet, so today flagd built from this repository is flagd-full.

## Standard distributions

| Distribution | Contents | Purpose |
|--------------|----------|---------|
| **flagd-full** | every component maintained in this repository, the deprecated evaluation services included | today's flagd: same CLI, configuration, ports and endpoints |
| flagd-minimal | file, http and gRPC sync providers, flagd's configuration format and evaluator, the flag evaluation (v2 only), OFREP and flag sync endpoints, and the noop telemetry provider; no Kubernetes client, cloud SDK or OpenTelemetry SDK; at most half the size of flagd-full | a small flagd that records no telemetry |
| flagd-minimal-otel | flagd-minimal plus the OpenTelemetry telemetry provider; still no Kubernetes client or cloud SDK | a small flagd that exports metrics and traces |
| flagd-cloud | the sync providers of flagd-minimal plus Google Cloud Storage, Azure Blob Storage and S3, all three endpoints (flag evaluation v2 only), OpenTelemetry | flagd for cloud deployments |
| flagd-kubernetes | the sync providers of flagd-minimal plus the OpenFeature Operator source, all three endpoints (flag evaluation v2 only), OpenTelemetry | flagd for Kubernetes deployments |
| **flagd-sync** | every sync provider of flagd-full, flagd's configuration format and the flag sync endpoint, OpenTelemetry; no evaluator | a **configuration proxy**: in-process providers get validated flag configurations from one place |

Today's flagd-proxy stays a separate program.
Each standard distribution's manifest is kept in this repository and accepts flagd-full's settings for the components it contains; see the use cases ([UC-010](../use_cases/UC-010-adopt-standard-distribution.md), [UC-012](../use_cases/UC-012-release-distributions-and-component-modules.md)).

## Deprecated evaluation services

Only flagd-full (and flagd-testbed) includes [`flag-evaluation-legacy`](flag-evaluation-legacy.md), which serves `schema.v1` and `flagd.evaluation.v1`.
flagd-minimal, flagd-minimal-otel, flagd-cloud and flagd-kubernetes serve only `flagd.evaluation.v2` on their flag evaluation port (OFREP has its own, `--ofrep-port`, 8016): the server answers HTTP 404 for the paths of `schema.v1` and `flagd.evaluation.v1`, which gRPC and ConnectRPC clients report as `Unimplemented`.
flagd-sync has no flag evaluation endpoint.
A client that still calls the deprecated services, such as the deprecated flagd-web provider, needs flagd-full or a custom distribution that lists the component, or must move to `flagd.evaluation.v2` or OFREP.

## Sizes

Binary sizes of upstream flagd at b23f82ec and of the distributions built from this repository, in MiB.
Method: Go 1.27.3, `linux/amd64`, `CGO_ENABLED=0`, the release ldflags, not stripped. The sizes come from two runs, at relay `326a8e2c` and `c1e0c3bc`.

| Build | MiB | vs upstream | Go modules |
|-------|----:|------------:|-----------:|
| upstream flagd | 115.3 | baseline | 190 |
| flagd-full | 110.1 | −4.5% | 197 |
| flagd-sync | 107.7 | −6.6% | 184 |
| flagd-cloud | 75.3 | −34.7% | 154 |
| flagd-kubernetes | 72.8 | −36.8% | 142 |
| flagd-minimal-otel | 37.2 | −67.7% | 101 |
| flagd-minimal | 28.5 | −75.3% | 61 |

What each addition costs flagd-minimal:

| Added to flagd-minimal | Added MiB | Go modules |
|------------------------|----------:|-----------:|
| OpenTelemetry | +8.6 | 101 |
| S3 | +10.5 | 80 |
| Google Cloud Storage | +27.3 | 100 |
| Azure Blob Storage | +3.6 | 71 |
| OpenFeature Operator | +35.8 | 104 |

OpenTelemetry with S3 is 47.6 MiB, with Google Cloud Storage 62.0 MiB and with Azure Blob Storage 40.8 MiB.
OpenTelemetry with all three clouds is 75.3 MiB, the size of flagd-cloud; with the operator it is 72.8 MiB, the size of flagd-kubernetes.

- Google Cloud Storage and the operator are the heavy dependencies.
- OpenTelemetry costs 8.6 MiB.
- A distribution for one cloud would save 13 to 35 MiB against flagd-cloud.

Percentages come from the unrounded sizes; flagd-kubernetes reads −36.9% when computed from the rounded values.
flagd-full measures smaller than upstream while linking 7 more modules.
This is not attributed to a change and is not a goal: a toolchain or dependency bump can move it by this much.

The release dry run writes the size of each `linux_x86_64` binary it builds to the job summary.

## Custom distributions

A Distribution Builder writes a manifest that lists official and third-party component modules, and the builder compiles it ([UC-001](../use_cases/UC-001-build-custom-distribution.md)).
The builder rejects a manifest whose components need a component it does not select, such as OFREP without an evaluator, or `flag-evaluation-legacy` without `flag-evaluation`.

Any distribution, standard or custom, can be built as a FIPS 140-3 binary or image: [Build a FIPS distribution](fips.md).

## Vendor distributions

A vendor distribution puts another vendor's sync provider, configuration format and evaluator behind OFREP — for example the Flagsmith engine or GO Feature Flag's evaluation module — so it can serve OpenFeature clients in place of that vendor's edge or relay proxy ([UC-019](../use_cases/UC-019-serve-vendor-flags.md)).
Polling, the store, change events, OFREP with its event stream, flag sync, telemetry and event delivery are shared; the flag model, rules, bucketing and identity mapping stay the vendor's.
Vendor components are for Extension Authors to ship, not the Maintainers.
The [cross-vendor evaluation](../research/cross-vendor-evaluation.md) checks the component model against flagd, Flagsmith and GO Feature Flag.
