---
description: Build and run flagd-full from this repository
---

# Try it

There are no releases or images of OpenFeature Relay; build from source.
What you build today is flagd-full, which behaves like released flagd.

You need Go (the version in `flagd/go.mod`), `make` and `curl`.
On Windows, use WSL for Go and Docker.

```shell
git clone https://github.com/open-feature-forking/openfeature-relay.git
cd openfeature-relay
git submodule update --init
make workspace-init
make build-flagd
./bin/flagd start --uri file:./config/samples/example_flags.flagd.json
```

Evaluate a flag over flagd's evaluation API (port 8013):

```shell
curl -X POST "http://localhost:8013/flagd.evaluation.v1.Service/ResolveBoolean" \
  -H "Content-Type: application/json" -d '{"flagKey":"myBoolFlag","context":{}}'
```

…or over OFREP (port 8016):

```shell
curl -X POST "http://localhost:8016/ofrep/v1/evaluate/flags/myBoolFlag" \
  -H "Content-Type: application/json" -d '{"context":{}}'
```

From here, flagd's own [quick start](../quick-start.md) and [reference](../reference/flagd-cli/flagd.md) apply unchanged.

## See what is in a binary

`flagd components` lists the components compiled into a distribution, with the module each comes from and the sources it reads:

```shell
./bin/flagd components
```

```text
distribution: flagd-full dev
runtime: dev

sync providers:
  file  github.com/open-feature-forking/openfeature-relay/sync/file (devel)  schemes: file:
  …
service endpoints:
  flag-evaluation  github.com/open-feature-forking/openfeature-relay/service/flag-evaluation (devel)
  ofrep  github.com/open-feature-forking/openfeature-relay/service/ofrep (devel)
  flag-sync  github.com/open-feature-forking/openfeature-relay/service/flag-sync (devel)
  event-collector  github.com/open-feature-forking/openfeature-relay/service/event-collector (devel)

telemetry providers:
  otel  github.com/open-feature-forking/openfeature-relay/telemetry/otel (devel)

middleware:
  none

event sinks:
  webhook  github.com/open-feature-forking/openfeature-relay/eventsink/webhook (devel)
```

`flagd components --output json` prints the same as JSON.
The command is hidden from `flagd --help`.

## Build a standard distribution

Each distribution is a manifest under `distributions/<name>/manifest.yaml`.
The standard ones are `flagd-full`, `flagd-minimal`, `flagd-cloud`, `flagd-kubernetes` and `flagd-sync`, and `flagsmith` is a vendor distribution; [Distributions](distributions.md) says what each contains.
`relay-builder` reads a manifest, generates the distribution's source and compiles it:

```shell
(cd builder && go build -o ../bin/relay-builder ./cmd/relay-builder)
GOWORK=off ./bin/relay-builder --manifest distributions/flagd-minimal/manifest.yaml
```

```text
binary: …/distributions/flagd-minimal/build/flagd
source: …/distributions/flagd-minimal/build
```

`GOWORK=off` keeps the build from using the workspace of this repository, so that it resolves the modules the way a distribution outside it would.
The build takes about 15 seconds with a warm module cache.
The binary has flagd's command line, so it starts the same way:

```shell
./distributions/flagd-minimal/build/flagd start --uri file:./config/samples/example_flags.flagd.json
./distributions/flagd-minimal/build/flagd components
```

The second command shows what flagd-minimal is made of: the file, HTTP and gRPC sync providers, one configuration format and evaluator, three service endpoints, and no telemetry provider, middleware or event sink.
`relay-builder --help` lists the flags, among them `--validate-only` to check a manifest and `--skip-compile` to leave the source for your own build.
`make build-flagd-full` builds flagd-full this way and prints how long it took.

## Serve another vendor's flags

The `flagsmith` distribution evaluates Flagsmith environments with Flagsmith's own engine and serves them over OFREP.
[Serve Flagsmith flags over OFREP](../guides/serve-flagsmith-flags.md) builds it with `make build-flagsmith`, starts it and lists what it does not do.

## Run the gates

Every change has to keep flagd's gates green: unit tests (`make test`), lint (`make lint`), the evaluator boundary (`make check-evaluator-boundary`), benchmarks, the flagd test harness (`make flagd-integration-test`) and the [load test](load-test.md) (`make test-load-smoke`).
The [contributing guide](https://github.com/open-feature-forking/openfeature-relay/blob/main/CONTRIBUTING.md) has the details.
