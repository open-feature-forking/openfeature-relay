# Glossary

| Term                  | Definition                                                                                                                                  | Avoid                         |
|-----------------------|---------------------------------------------------------------------------------------------------------------------------------------------|-------------------------------|
| OpenFeature Relay | The runtime, component interfaces and builder from which flag relay distributions are built; flagd is its reference implementation. | flagd-builder, proxy builder |
| Builder | The relay builder CLI that turns a manifest into generated source and a compiled distribution. | ocb, generator, flagd-builder |
| Manifest              | The YAML file that names a distribution and lists the component modules, versions and replacements it is built from.                       | config, builder config        |
| Distribution          | A flagd binary, and its image, built from one manifest; it contains exactly the components the manifest selects.                          | flavour                       |
| Standard Distribution | A distribution whose manifest the Maintainers publish with every release: flagd-minimal, flagd-minimal-otel, flagd-cloud, flagd-kubernetes, flagd-sync or flagd-full. |  |
| flagd-full            | The standard distribution with every official component; it behaves exactly like flagd before the builder.                               | monolith                      |
| Custom Distribution   | A distribution built from a manifest that is not a standard distribution.                                                                   |                               |
| Component             | A selectable unit compiled into a distribution, exposed by a component module through a factory.                                          |                               |
| Component Type | The kind of a component: sync provider, configuration format, evaluator, service endpoint, event sink, telemetry provider or middleware. |  |
| Component Module | A Go module with its own go.mod that provides one component, or a configuration format with its paired evaluator (an engine module). |  |
| Core Module           | The Go module holding the factory interfaces, the flag model and the sync data types that all component modules depend on.               | core package                  |
| Factory               | The interface a component module implements so that a distribution can create its component from injected dependencies.                 |                               |
| Extension             | A component written by an Extension Author in their own Go module rather than by the Maintainers.                                        | add-on                        |
| Sync Provider | A component type that obtains flag configurations from a kind of source and emits them: either a fetcher driven by fetch triggers, or a streaming sync provider that detects changes and delivers in one. | sync source, syncer |
| Source | One configured location a sync provider reads from, given as a URI on the command line or in the configuration file; it may name its configuration format, flagd's being the default. |  |
| Scheme | The prefix of a source (`file:`, `https://`, `s3://`, `core.openfeature.dev/`) that selects the sync provider handling it; it need not be a URI scheme. |  |
| Service Endpoint      | A component type that serves flags to clients over a protocol: flag evaluation (gRPC/ConnectRPC), OFREP or flag sync.                     | server, access point          |
| Flag Configuration    | The flags a sync provider delivers from a source, with variants and targeting, written in one configuration format.                                          | flag config, flag definitions |
| Configuration Format | A component type that validates flag configurations of one format (flagd's JSON schema, a vendor's format) and keeps them in the store as its own documents, which only its paired evaluator reads; it reports each document's flag keys, flag set, version and changes. | parser |
| Configuration Proxy | A distribution with sync providers, a configuration format and the flag sync endpoint but no evaluator; it forwards validated flag configurations to in-process providers. flagd-sync is the standard one; today's flagd-proxy is a separate program. |  |
| Evaluator | The component type that resolves one, several or all flags of a selector for an evaluation context from the documents of its paired configuration format, returning untyped results with OpenFeature reasons and error codes; it emits no telemetry. | resolver |
| Telemetry Provider    | A component type that supplies tracing, metrics and HTTP instrumentation to the runtime, sync providers, service endpoints, middleware and event sinks. |                               |
| Noop Telemetry Provider | The telemetry provider a distribution uses when its manifest selects none; it records nothing and carries no observability dependency. | null telemetry |
| Middleware | A component type that wraps a service endpoint's HTTP handler with cross-cutting behaviour; it may bind a request to a selector and attach request attributes. | interceptor, filter |
| Store | The in-memory holder of the documents each configuration format accepted from its sources, with their versions; evaluators read their own format's documents from it, and the flag sync and event stream endpoints watch it. | memdb |
| Selector | An expression a client sends, or middleware derives from the client's credential, to restrict a query, an evaluation or a flag sync subscription to the flags of certain sources or flag sets. |  |
| OFREP                 | The OpenFeature Remote Evaluation Protocol, an HTTP API for evaluating flags; one of flagd's service endpoints.                          |                               |
| Vendor Flag Service | Another vendor's hosted flag management service that a vendor distribution syncs flag configurations from, such as Flagsmith. |  |
| Edge Proxy            | A vendor's own process that caches and evaluates flags close to applications; a vendor distribution can take its place.              |                               |
| Vendor Distribution   | A custom distribution built from another vendor's sync provider, configuration format and evaluator, typically behind OFREP. | |
| Flag Model            | flagd's own representation of a flag: variants, default variant, targeting and metadata, as defined by flagd's flag configuration schema. | |
| Flag Set | A named group of flags a selector can address. The configuration format decides how its documents map to flag sets: flagd by `flagSetId` metadata, Flagsmith by environment, GO Feature Flag by flag set name. |  |
| Incremental Update    | A delivery from a sync provider that replaces only the flag sets it contains instead of all flags of its source. | delta |
| Readiness | The state in which every configured source has delivered a flag configuration that its configuration format accepted, and none is stale; flagd reports it on the management port. |  |
| Management Port       | The port on which flagd serves health, readiness and metrics, separate from the service endpoints. | |
| Contract Test         | A reusable test the Maintainers ship per component type that checks a component behaves as the runtime expects. | |
| FIPS Build            | A distribution compiled in FIPS 140-3 mode, with the crypto boundary described in docs/reference/fips-140-3.md. | |
| Document | The parsed, validated flags of one flag set from one source, owned by the configuration format that accepted them and kept in the store; a source whose flag configuration holds several flag sets yields several documents. |  |
| Contract Case         | One check within a contract test, such as rejecting an invalid flag configuration without changing the store. | |
| Release Tag           | The git tag `<module path>/v<version>` that releases one component module, or the flagd release a standard distribution follows. | |
| flagd Test Harness    | The Gherkin acceptance suites and test bed (open-feature/flagd-testbed) that flagd and its providers run to prove evaluation and sync behaviour. | |
| Event Sink | A component type that subscribes to events on the event bus (configuration changes, evaluations, tracking) and delivers them to an external system, buffered and in batches where needed. |  |
| Notifier | An event sink that tells people or systems about configuration changes (a chat message, a webhook call), as GO Feature Flag's notifiers do; outbound, the counterpart of an inbound fetch trigger. | |
| Evaluation Event | The event a service endpoint publishes for each resolved flag, or relays from an in-process provider: flag key, flag set, variant, value, reason, targeting key and origin. | feature event |
| Configuration Change Event | The event the runtime publishes when a configuration format accepts a changed document: source, configuration format, flag set, added, updated and removed flag keys, and the new version. | diff |
| Tracking Event | An OpenFeature tracking event relayed to event sinks. |  |
| Engine Module | A component module that provides a configuration format and the evaluator paired with it, as vendors ship their engines. |  |
| Fetcher | The part of a sync provider that fetches a source on request, with conditional requests, and knows nothing about when to fetch (file read, HTTP GET, blob read). | retriever |
| Fetch Trigger | A component that tells a fetcher to fetch: an interval with jitter and backoff (the core's poller), a server-sent event stream, a file watch, a webhook, or a vendor's change events. | notifier, watcher |
| Source Version | An opaque value a fetcher returns for its source's flag configuration as read (an ETag, a content hash, a generation) and gets back on the next fetch; not a document version. | |
| Streaming Sync Provider | A sync provider that detects changes and delivers flag configurations in one, such as flagd's gRPC sync protocol and the OpenFeature Operator's FeatureFlag watch. | |
| Fetching Sync Provider | A sync provider built as a fetcher plus the fetch triggers attached to its source. | retriever |
| Encoding | The serialization of a flag configuration (JSON or YAML), decoded by the core before its configuration format validates it; independent of the configuration format. |  |
| Document Version | An opaque value a configuration format reports for each document, such as a hash or the vendor's update time; service endpoints derive ETags and event stream events from it. |  |
| Event Stream | The OFREP server-sent event stream that announces configuration changes for a selector, advertised in OFREP bulk responses. | SSE endpoint |
| Context Enrichment | Values flagd adds to an evaluation context before resolution: static values, header mappings, and per-selector values. |  |
| Request Attribute | A value middleware attaches to a request, such as the kind of credential used, that evaluators may read; not part of the evaluation context. |  |
| Evaluation Router | The part of the runtime that picks, for a selector and flag key, the configuration format holding the flag and its evaluator. |  |
| Staleness | The age of a source's last accepted delivery; beyond a configured limit, readiness fails. |  |
| Event Bus | The publish/subscribe channel through which the runtime and service endpoints announce configuration change, evaluation and tracking events to service endpoints and event sinks. |  |
| Runtime | The part of a distribution that starts sync providers, hands their flag configurations to configuration formats, publishes events on the event bus and starts service endpoints. |  |
| Image Build Environment | The container build tooling (for example Docker Buildx) that turns a compiled distribution and a Dockerfile into an image. | |
| In-Process Provider | An OpenFeature provider that embeds the flagd configuration format and the evaluator and evaluates flags locally instead of calling a service endpoint. | embedded provider |
| Remote Evaluation     | Evaluation performed by a running flagd and returned over a service endpoint, as opposed to in-process evaluation.                      | RPC mode                      |
| Hook | An OpenFeature hook; it runs in the client SDK. flagd has no server-side hooks: middleware, context enrichment and event sinks take their positions. |  |
| Snapshot | A local file holding the last accepted documents of every configuration format, written on every change and read at startup as the lowest-precedence source. |  |
| Operator              | A person who runs a flagd binary or image.                                                                                                 | admin                         |
| Distribution Builder  | A person who composes a custom distribution from a manifest.                                                                               |                               |
| Extension Author      | A person who writes an extension in their own Go module.                                                                                   | contributor                   |
| Provider Author       | A person who maintains an in-process provider that embeds the evaluator.                                                                   |                               |
| Maintainer            | A person who maintains flagd, its component modules and the standard distributions.                                                       |                               |
| Testbed | A distribution of the flagd-testbed role: flagd built from `distributions/flagd-testbed` with a control endpoint and a listener gate, against which provider test suites run. | |
| Launchpad API | The control API of flagd-testbed v3.11.0 on port 8080: `/start`, `/stop`, `/restart` and `/change`, with their texts and codes. | |
| Listener Gate | The layer through which the runtime opens its listeners; closing it closes every listener and connection with a plain close, opening it binds them again. | |
| Control Target | The interface through which the control module drives a runtime: start, stop, load a document and report readiness. | |
