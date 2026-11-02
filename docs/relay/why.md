---
description: The problem OpenFeature Relay solves
---

# Why OpenFeature Relay

## The problem

flagd ships as one binary that contains every sync provider (file, HTTP, gRPC, Kubernetes, GCS, Azure Blob, S3), every service endpoint (flag evaluation, OFREP, flag sync) and the full OpenTelemetry SDK with all exporters.

- The `core` Go module pulls in roughly 700 transitive dependencies, whatever an operator actually uses.
- Adding a sync provider or an endpoint means changing flagd itself; there is no way to plug in a component without forking.
- `core` is also a library: in-process providers embed its evaluator, store and sync types, and get its dependencies with them.
- A vendor that wants OFREP, flag sync, an event stream and telemetry for its own flag engine has to build all of it again.

## The idea

flagd becomes a set of components assembled into a distribution:

- **Flexible**: a distribution contains only the components its manifest selects; unused components and their dependencies are not compiled in.
- **Powerful**: sync providers, configuration formats, evaluators, service endpoints, event sinks, telemetry and middleware are all selectable component types behind factory interfaces.
- **Backward compatible**: the flagd-full distribution behaves exactly like today's flagd.
- **Easy to extend**: anyone can write a component in their own Go module and build it into a distribution.
- **Embeddable**: the evaluator and the flagd configuration format are dependency-free modules for in-process providers.

What every relay needs stays shared: polling, the store, change events, OFREP with its event stream, flag sync, telemetry and event delivery.
What makes a vendor a vendor stays the vendor's: the flag model, rules, bucketing and identity mapping.

The full statement, with scope and success criteria, is the [vision](../vision.md).
