# Vision: OpenFeature Relay

OpenFeature Relay is a runtime and a builder for flag relays: processes that sync flag configurations, keep them, evaluate them and serve them to OpenFeature clients over OFREP, flag sync and other service endpoints. flagd is its reference implementation: today's flagd becomes the flagd-full distribution, and the code starts from flagd's.

## Problem

flagd ships as one binary that contains every sync provider (file, HTTP, gRPC, Kubernetes, GCS, Azure Blob, S3), every service endpoint (flag evaluation, OFREP, flag sync) and the full OpenTelemetry SDK with all exporters.
The `core` Go module pulls in roughly 700 transitive dependencies, whatever the operator actually uses.
Adding a sync provider or an endpoint means changing flagd itself; there is no way to plug in a component without forking.

`core` is also a library.
In-process providers embed its evaluator, store and sync types.
The evaluator traces through the global OpenTelemetry state, so applications using an in-process provider get spans they cannot turn off, duplicating what the OpenFeature OpenTelemetry hooks already record.

## Vision

flagd becomes a set of components assembled into a distribution, the way the OpenTelemetry Collector Builder assembles collectors:

- **Flexible**: a distribution contains only the components its manifest selects; unused components and their dependencies are not compiled in.
- **Powerful**: sync providers, configuration formats, evaluators, service endpoints, event sinks, telemetry and middleware are all selectable component types behind factory interfaces. Any combination of service endpoints is possible: OFREP alone, flag evaluation alone, both, or extensions.
- **Backward compatible**: the `flagd-full` distribution behaves exactly like today's flagd — same CLI, configuration, ports, endpoints and images.
- **Easy to extend**: anyone can write a component in their own Go module and build it into a distribution without forking flagd.
- **Embeddable**: the evaluator and the flagd configuration format are dependency-free modules and the entry point for in-process providers; their observability comes from OpenFeature hooks, while flagd-full's service endpoints keep recording evaluation spans.

Three distributions show the range:

- **flagd-full**: today's flagd, unchanged.
- **Configuration proxy** (flagd-sync): sync providers, a configuration format and the flag sync endpoint, no evaluator; in-process providers get validated flag configurations from one place. Parsing and validating flag configurations therefore moves out of the evaluator into the configuration format. Today's flagd-proxy stays a separate program.
- **Vendor distribution**: another vendor's sync provider, configuration format and evaluator behind OFREP, for example the Flagsmith engine or GO Feature Flag's evaluation module, so flagd can stand in for that vendor's edge or relay proxy for OpenFeature clients. Each configuration format keeps its own documents in the store, so flagd's flag model stays unchanged; a source names its format, flagd's being the default. Polling, the store, change events, OFREP with its event stream, flag sync, telemetry and event delivery are shared; the flag model, rules, bucketing and identity mapping stay the vendor's.

## Roles

- **Operator**: runs a flagd binary or image, as every flagd adopter does today.
- **Distribution Builder**: composes a custom distribution from a manifest.
- **Extension Author**: writes a component in their own Go module.
- **Provider Author**: maintains an in-process provider that embeds the evaluator.
- **Maintainer**: maintains flagd, its component modules and the standard distributions.

## Scope

In scope for the first release:

- the builder CLI, the manifest format and code generation
- factory interfaces for sync providers, configuration formats, evaluators, service endpoints, telemetry and middleware
- splitting `core` into a lightweight core module and component modules
- an event bus replacing the runtime's typed calls into services
- standard distributions: minimal, cloud, Kubernetes, relay, full
- the evaluator as an embeddable module without telemetry, transport or sync dependencies
- an event sink component type, and an event bus that carries configuration change, evaluation and tracking events
- a shared poller that drives sync providers which only fetch, with conditional requests, jitter, backoff and a refetch trigger

Enabled by the architecture, planned for a later release:

- snapshot sync for high availability (persisting flag state and bootstrapping from it on restart)

Out of scope:

- dynamic loading (Go plugins, shared libraries)
- changes to flag evaluation semantics or flagd's flag configuration schema
- shipping vendor components; the vendor distribution is a goal for Extension Authors, not for the Maintainers
- a hosted registry for community components
- server-side OpenFeature hooks; middleware, context enrichment and event sinks cover the positions before and after evaluation
- stored identities with persisted traits, as some vendor flag services keep them
- per-flag-set pipelines (own sync providers, event sinks and enrichment per flag set)

## Way of working

OpenFeature Relay incubates the work with flagd's full history.
Every piece lands on this repository's `main` through a pull request in this repository; nothing goes to `open-feature/flagd` or any other repository until Simon decides to take it upstream.
The specification in this folder follows the AI Unified Process: vision, requirements, use cases, entity model, test cases.

## Success

- `flagd-full` passes the flagd test-harness suites with the same results as today's flagd.
- A minimal distribution contains no Kubernetes client, cloud SDK or OpenTelemetry SDK.
- An Extension Author builds a distribution with a custom sync provider without changing flagd.
- A configuration proxy serves in-process providers without an evaluator compiled in.
- The Go in-process provider depends on the evaluator module and nothing it does not use.
- A vendor distribution built from a vendor engine module answers OFREP requests with the vendor's values, variants and reasons, and the shared OFREP endpoint serves its ETags and event stream unchanged.
