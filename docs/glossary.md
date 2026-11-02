# Glossary

| Term                  | Definition                                                                                                                                  | Avoid                         |
|-----------------------|---------------------------------------------------------------------------------------------------------------------------------------------|-------------------------------|
| Builder               | The flagd-builder CLI that turns a manifest into generated source and a compiled distribution.                                             | ocb, generator                |
| Manifest              | The YAML file that names a distribution and lists the component modules, versions and replacements it is built from.                       | config, builder config        |
| Distribution          | A flagd binary, and its image, built from one manifest; it contains exactly the components the manifest selects.                          | flavour, variant              |
| Standard Distribution | A distribution whose manifest the Maintainers publish with every release: flagd-minimal, flagd-cloud, flagd-kubernetes or flagd-full.      |                               |
| flagd-full            | The standard distribution with every official component; it behaves exactly like flagd before the builder.                               | monolith                      |
| Custom Distribution   | A distribution built from a manifest that is not a standard distribution.                                                                   |                               |
| Component             | A selectable unit compiled into a distribution, exposed by a component module through a factory.                                          |                               |
| Component Type        | The kind of a component: sync provider, service endpoint, evaluator, telemetry provider or middleware.                                    |                               |
| Component Module      | A Go module with its own go.mod that provides one component.                                                                                |                               |
| Core Module           | The Go module holding the factory interfaces, the flag model and the sync data types that all component modules depend on.               | core package                  |
| Factory               | The interface a component module implements so that a distribution can create its component from injected dependencies.                 |                               |
| Extension             | A component written by an Extension Author in their own Go module rather than by the Maintainers.                                        | add-on                        |
| Sync Provider         | A component type that reads flag configurations from a kind of source (file, HTTP, gRPC, Kubernetes, blob storage) and emits them.      | sync source, syncer           |
| Source                | One configured location a sync provider reads from, given as a URI on the command line or in the configuration file.                     |                               |
| Scheme                | The URI prefix of a source (`file:`, `https://`, `s3://`) that selects the sync provider handling it.                                     |                               |
| Service Endpoint      | A component type that serves flags to clients over a protocol: flag evaluation (gRPC/ConnectRPC), OFREP or flag sync.                     | server                        |
| Evaluator             | The component type that resolves a flag for an evaluation context; the JsonLogic evaluator is the one in-process providers embed.       | engine, resolver              |
| Telemetry Provider    | A component type that supplies tracing, metrics and HTTP instrumentation to service endpoints and middleware.                            |                               |
| Middleware            | A component type that wraps a service endpoint's HTTP handler with cross-cutting behaviour.                                               | interceptor, filter           |
| Event Bus             | The publish/subscribe channel through which the runtime announces events, such as flag changes, to service endpoints.                  |                               |
| Runtime               | The part of a distribution that starts sync providers, feeds the store and evaluator, and starts service endpoints.                     |                               |
| In-Process Provider   | An OpenFeature provider that embeds the evaluator and evaluates flags locally instead of calling a service endpoint.                    | embedded provider             |
| Remote Evaluation     | Evaluation performed by a running flagd and returned over a service endpoint, as opposed to in-process evaluation.                      | RPC mode                      |
| Hook                  | An OpenFeature hook; for in-process providers, the only place observability is added.                                                    |                               |
| Snapshot              | A local file holding the last known flag state, written on every change and read at startup as the lowest-precedence source.            |                               |
| Operator              | A person who runs a flagd binary or image.                                                                                                 | admin                         |
| Distribution Builder  | A person who composes a custom distribution from a manifest.                                                                               |                               |
| Extension Author      | A person who writes an extension in their own Go module.                                                                                   | contributor                   |
| Provider Author       | A person who maintains an in-process provider that embeds the evaluator.                                                                   |                               |
| Maintainer            | A person who maintains flagd, its component modules and the standard distributions.                                                       |                               |
