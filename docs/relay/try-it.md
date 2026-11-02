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

## Run the gates

Every change has to keep flagd's gates green: unit tests (`make test`), lint (`make lint`), the evaluator boundary (`make check-evaluator-boundary`), benchmarks and the flagd test harness (`make flagd-integration-test`).
The [contributing guide](https://github.com/open-feature-forking/openfeature-relay/blob/main/CONTRIBUTING.md) has the details.
