---
description: The deprecated schema.v1 and flagd.evaluation.v1 services as their own component
---

# The deprecated flag evaluation services

The flag evaluation endpoint serves `flagd.evaluation.v2` on its port.
`schema.v1` and `flagd.evaluation.v1` are deprecated upstream, and so is the flagd-web provider, the last current client of v1.
The relay serves them from their own component, `flag-evaluation-legacy` (module `service/flag-evaluation-legacy`), so that a distribution carries them only when its manifest lists it ([FR-063](../requirements.md), [UC-010](../use_cases/UC-010-adopt-standard-distribution.md)).

## How it works

`flag-evaluation-legacy` is a **hosted endpoint**: it opens no listener.
The flag evaluation endpoint, which hosts it, mounts its two routes on its own server.
They share the endpoint's port, TLS, socket path, CORS, middleware, limits, metrics and readiness, and the endpoint's settings (`context-values`, `header-to-context-key-mappings`, `stream-deadline`) apply to them.
The component has no settings of its own.

The builder refuses a manifest that lists `flag-evaluation-legacy` without `flag-evaluation`, and the runtime refuses to start without it, both naming the two types ([UC-001](../use_cases/UC-001-build-custom-distribution.md)).
`flagd components` shows the host next to the component ([UC-011](../use_cases/UC-011-inspect-distribution-components.md)).

## Which distribution includes it

| Distribution | Includes it |
|--------------|:-----------:|
| flagd-full (and `flagd`) | yes |
| flagd-testbed | yes |
| flagd-minimal, flagd-minimal-otel, flagd-cloud, flagd-kubernetes | no |
| flagd-sync | no (it has no flag evaluation endpoint) |

A custom distribution lists it in the `service_endpoints` group of its manifest, beside `flag-evaluation`.

## Without the component

On a distribution that does not include it, a call to `schema.v1.Service` or `flagd.evaluation.v1.Service` fails:

- `Unimplemented` over gRPC and ConnectRPC;
- 404 over HTTP.

`flagd.evaluation.v2` and OFREP are unchanged.
The deprecated flagd-web provider calls v1 `ResolveAll` and `EventStream`, so it does not become ready on such a distribution, and neither do old releases of the RPC providers that still call v1.

To keep a client working:

- run flagd-full;
- build a custom distribution that lists the component;
- or move the client to `flagd.evaluation.v2`, or to OFREP; the flagd-web provider's replacement is the ofrep-web provider.
