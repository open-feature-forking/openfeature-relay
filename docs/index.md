---
description: OpenFeature Relay, a builder and runtime for flag relays, with flagd as the reference implementation
---

# OpenFeature Relay

**Build the flag relay you need, from components — with flagd as the reference implementation.**

!!! warning "Proof of concept, not an official OpenFeature project"

    OpenFeature Relay is an independent incubator by a flagd maintainer.
    It is **not** an official OpenFeature or CNCF project, and nothing here is endorsed by the OpenFeature Technical Committee.
    There are no releases and no images yet; build from source.
    For production, use [released flagd](https://flagd.dev/installation/).

A **flag relay** is a process that syncs flag configurations, keeps them, evaluates them and serves them to OpenFeature clients over OFREP, flag evaluation, flag sync or other service endpoints.
flagd is one; so are a vendor's edge proxy and relay proxy.

OpenFeature Relay is a **runtime and a builder** for flag relays, the way the [OpenTelemetry Collector Builder](https://opentelemetry.io/docs/collector/custom-collector/) is for collectors:
you list the components you want in a **manifest**, and the **builder** compiles a **distribution** that contains exactly those components.
Anyone can write a component in their own Go module and build it in, without forking.

flagd is the reference implementation, and the code starts from flagd's.
Today's flagd becomes the **flagd-full** distribution: same CLI, configuration, ports, endpoints and behaviour.
Its documentation is preserved under [flagd](flagd/index.md).

## Where to go next

| If you want to… | Read |
|-----------------|------|
| know what problem this solves | [Why OpenFeature Relay](relay/why.md) |
| learn the moving parts | [Concepts](relay/concepts.md) and [Architecture](relay/architecture.md) |
| see what can be built | [Distributions](relay/distributions.md) |
| know what works today | [Status and roadmap](relay/status.md) |
| run it | [Try it](relay/try-it.md) |
| read the contract | [Vision](vision.md), [Requirements](requirements.md), [Glossary](glossary.md), [Use cases](use_cases/UC-001-build-custom-distribution.md) |
| see how it is built | [How it's built](relay/how-its-built.md) |
