# Glossary

| Term                  | Definition                                                                                                                                  | Avoid                         |
|-----------------------|---------------------------------------------------------------------------------------------------------------------------------------------|-------------------------------|
| Builder               | The flagd-builder CLI that turns a manifest into generated source and a compiled distribution.                                             | ocb, generator                |
| Manifest              | The YAML file that names a distribution and lists the component modules, versions and replacements it is built from.                       | config, builder config        |
| Distribution          | A flagd binary, and its image, built from one manifest; it contains exactly the components the manifest selects.                          | flavour                       |
| Standard Distribution | A distribution whose manifest the Maintainers publish with every release: flagd-minimal, flagd-cloud, flagd-kubernetes, flagd-relay or flagd-full. |  |
| flagd-full            | The standard distribution with every official component; it behaves exactly like flagd before the builder.                               | monolith                      |
| Custom Distribution   | A distribution built from a manifest that is not a standard distribution.                                                                   |                               |
| Component             | A selectable unit compiled into a distribution, exposed by a component module through a factory.                                          |                               |
| Component Type        | The kind of a component: sync provider, configuration format, evaluator, service endpoint, telemetry provider or middleware.               |                               |
| Component Module      | A Go module with its own go.mod that provides one component.                                                                                |                               |
| Core Module           | The Go module holding the factory interfaces, the flag model and the sync data types that all component modules depend on.               | core package                  |
| Factory               | The interface a component module implements so that a distribution can create its component from injected dependencies.                 |                               |
| Extension             | A component written by an Extension Author in their own Go module rather than by the Maintainers.                                        | add-on                        |
| Sync Provider         | A component type that reads flag configurations from a kind of source (file, HTTP, gRPC, Kubernetes, blob storage) and emits them.      | sync source, syncer           |
| Source | One configured location a sync provider reads from, given as a URI on the command line or in the configuration file; it may name its configuration format, flagd's being the default. |  |
| Scheme | The prefix of a source (`file:`, `https://`, `s3://`, `core.openfeature.dev/`) that selects the sync provider handling it; it need not be a URI scheme. |  |
| Service Endpoint      | A component type that serves flags to clients over a protocol: flag evaluation (gRPC/ConnectRPC), OFREP or flag sync.                     | server, access point          |
| Flag Configuration    | The flags a sync provider delivers from a source, with variants and targeting, written in one configuration format.                                          | flag config, flag definitions |
| Configuration Format | A component type that parses and validates flag configurations of one format (flagd's JSON schema, a vendor's format) and keeps them in the store as its own documents, which only its paired evaluator reads. | parser |
| Configuration Proxy | A distribution with sync providers, a configuration format and the flag sync endpoint but no evaluator; it forwards validated flag configurations to in-process providers. flagd-relay is the standard one; today's flagd-proxy is a separate program. |  |
| Evaluator | The component type that resolves a flag for an evaluation context from the documents of its paired configuration format; it emits no telemetry. | resolver |
| Telemetry Provider    | A component type that supplies tracing, metrics and HTTP instrumentation to service endpoints and middleware.                            |                               |
| Middleware            | A component type that wraps a service endpoint's HTTP handler with cross-cutting behaviour.                                               | interceptor, filter           |
| Store | The in-memory holder of the documents each configuration format accepted from its sources; evaluators read their own format's documents from it, and the flag sync endpoint watches it. | memdb |
| Selector              | An expression a client sends to restrict a query or a flag sync subscription to the flags of certain sources or flag sets.                |                               |
| OFREP                 | The OpenFeature Remote Evaluation Protocol, an HTTP API for evaluating flags; one of flagd's service endpoints.                          |                               |
| Vendor Flag Service | Another vendor's hosted flag management service that a vendor distribution syncs flag configurations from, such as Flagsmith. |  |
| Edge Proxy            | A vendor's own process that caches and evaluates flags close to applications; a vendor distribution can take its place.              |                               |
| Vendor Distribution   | A custom distribution built from another vendor's sync provider, configuration format and evaluator, typically behind OFREP. | |
| Flag Model            | flagd's own representation of a flag: variants, default variant, targeting and metadata, as defined by flagd's flag configuration schema. | |
| Flag Set              | A named group of flags within a flag configuration that a selector can address. | |
| Incremental Update    | A delivery from a sync provider that replaces only the flag sets it contains instead of all flags of its source. | delta |
| Readiness             | The state in which every configured source has delivered a flag configuration that its configuration format accepted; flagd reports it on the management port. | |
| Management Port       | The port on which flagd serves health, readiness and metrics, separate from the service endpoints. | |
| Contract Test         | A reusable test the Maintainers ship per component type that checks a component behaves as the runtime expects. | |
| FIPS Build            | A distribution compiled in FIPS 140-3 mode, with the crypto boundary described in docs/reference/fips-140-3.md. | |
| Document              | The parsed, validated form of one source's flag configuration, owned by the configuration format that accepted it and kept in the store. | |
| Contract Case         | One check within a contract test, such as rejecting an invalid flag configuration without changing the store. | |
| Release Tag           | The git tag `<module path>/v<version>` that releases one component module, or the flagd release a standard distribution follows. | |
| flagd Test Harness    | The Gherkin acceptance suites and test bed (open-feature/flagd-testbed) that flagd and its providers run to prove evaluation and sync behaviour. | |
| Event Bus             | The publish/subscribe channel through which the runtime announces events, such as flag changes, to service endpoints.                  |                               |
| Runtime | The part of a distribution that starts sync providers, hands their flag configurations to configuration formats, publishes events on the event bus and starts service endpoints. |  |
| Image Build Environment | The container build tooling (for example Docker Buildx) that turns a compiled distribution and a Dockerfile into an image. | |
| In-Process Provider | An OpenFeature provider that embeds the flagd configuration format and the evaluator and evaluates flags locally instead of calling a service endpoint. | embedded provider |
| Remote Evaluation     | Evaluation performed by a running flagd and returned over a service endpoint, as opposed to in-process evaluation.                      | RPC mode                      |
| Hook                  | An OpenFeature hook; for in-process providers, the only place observability is added.                                                    |                               |
| Snapshot | A local file holding the last accepted documents of every configuration format, written on every change and read at startup as the lowest-precedence source. |  |
| Operator              | A person who runs a flagd binary or image.                                                                                                 | admin                         |
| Distribution Builder  | A person who composes a custom distribution from a manifest.                                                                               |                               |
| Extension Author      | A person who writes an extension in their own Go module.                                                                                   | contributor                   |
| Provider Author       | A person who maintains an in-process provider that embeds the evaluator.                                                                   |                               |
| Maintainer            | A person who maintains flagd, its component modules and the standard distributions.                                                       |                               |
