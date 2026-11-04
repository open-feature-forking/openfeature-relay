# Listing the components of a distribution

`flagd components` lists the components compiled into the binary.
It is hidden from `flagd --help`, so the help of every distribution stays as it was.
The command reads no configuration and opens no port.

```shell
flagd components
flagd components --output json
```

## Text

The listing starts with the lines that `flagd version --verbose` ends with: the distribution and its manifest version, and the runtime version.
One section follows per component type, always in this order: sync providers, configuration formats, evaluators, service endpoints, telemetry providers, middleware and event sinks.
A section without components says `none`.
Each line holds the component's type, its module and the module version, and for a sync provider the schemes it handles.
Fetch triggers are listed with the sync providers and have no schemes.

```text
distribution: flagd-full v0.15.3
runtime: v0.3.1

sync providers:
  file  github.com/open-feature-forking/openfeature-relay/sync/file v0.3.1  schemes: file:
```

## JSON

`--output json` prints the same content as one document.
The keys under `components` are `sync_providers`, `configuration_formats`, `evaluators`, `service_endpoints`, `telemetry`, `middleware` and `event_sinks`, and an empty group is an empty list.
Each entry has `type`, `module` and `version`, and sync providers add `schemes`.

```json
{
  "distribution": { "name": "flagd-full", "version": "v0.15.3" },
  "runtime": { "version": "v0.3.1" },
  "components": { "sync_providers": [{ "type": "file", "module": "github.com/open-feature-forking/openfeature-relay/sync/file", "version": "v0.3.1", "schemes": ["file:"] }] }
}
```

## Binaries from before the builder

A flagd release from before the builder does not know the command and reports an unknown command.
Such a binary contains every component of flagd-full.
