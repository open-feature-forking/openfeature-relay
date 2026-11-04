# Upgrading to flagd-full

flagd-full is the flagd that the relay builder builds from `distributions/flagd-full/manifest.yaml`.
It is a drop-in replacement for the flagd you run today: you keep the start command, the configuration file, the environment variables, the ports and the image name.
This guide lists what stays the same, the few behaviours that changed, and how to roll back.

## What stays the same

- **Start command, flags, configuration keys and environment variables:** the same names, defaults and order of precedence.
- **Ports, endpoints, payloads and headers:** flag evaluation (ConnectRPC v1, v2 and the legacy service), OFREP (single, bulk, `304` and event stream), flag sync and the management paths keep their addresses and shapes.
- **Sources:** `file:`, `http://`, `https://`, `grpc://`, `grpcs://`, the custom gRPC name-resolution scheme, `gs://`, `azblob://`, `s3://` and `core.openfeature.dev/`.
    When several sources define the same flag key, the source given later overrides the earlier one.
- **Metrics and spans:** the same names, attributes and buckets, and one evaluation span per evaluation request.
- **`flagd version`:** the output is byte-identical to today's.
    Use `flagd version --verbose` to see that the running binary is flagd-full and which manifest version it was built from.
- **Image and binaries:** the image stays `ghcr.io/open-feature/flagd`, with the tags `<version>`, `latest`, `<version>-fips` and `latest-fips`.
    The binary archives keep today's names, `flagd_<version>_<OS>_<arch>` and `flagd-fips_<version>_<OS>_<arch>`, and the same contents.
- **Startup time:** flagd-full reaches readiness within 10% of today's flagd with the same sources.
- **Settings that today's flagd accepts only for compatibility**, such as the keep-alive settings of the flag sync service endpoint, are accepted and ignored.

An upgrade-test in this repository runs today's flagd and flagd-full side by side with the same configuration and requests, and fails on any difference that is not listed below.

## What changed

Every item below was a decision of the project lead.
The first group can break a setup that works today.

### Could break a setup that works today

#### A failed first fetch or connect no longer exits flagd

When a source cannot be fetched or connected at startup, flagd now stays up, keeps retrying and reports not ready on `/readyz`.
Before, flagd exited with code 1.
If you relied on the exit to restart a container, use the readiness probe instead.

#### An unbuildable telemetry configuration stops startup

When the telemetry configuration cannot be built, for example an unreachable or malformed OTLP setting, flagd stops at startup.
Before, flagd logged the error and ran without telemetry.
Correct the telemetry settings, or remove them.

#### Invalid blob URL options or Azure variables stop startup

Invalid options in a `gs://`, `azblob://` or `s3://` URL, or invalid Azure variables, stop flagd at startup.
Before, flagd started and never became ready.

#### `awssdk=v1` on an `s3://` source stops startup

The `s3://` source uses AWS SDK v2 only.
Remove `awssdk=v1` from the URL.

### Visible to clients, unlikely to break them

#### Flag sync's `Last-Modified` moves only on a configuration change

The `Last-Modified` of the flag sync service endpoint changes when the flag configuration changes, and not on every reload.

#### OFREP `flagConfigEtag` values change

The values are opaque, so clients that only send them back are not affected.
A change in a vendor flag format now sends a refetch event on the OFREP event stream.

#### Shutdown: event streams get `provider_shutdown`

On `SIGTERM`, event streams receive `provider_shutdown` and end at once, and flag sync streams end cleanly in under 2 seconds.
Shutdown can take up to 10 seconds in total, because telemetry is flushed.
If you set a termination grace period, keep it above 10 seconds.

#### The operator source delivers `spec.flagSpec` as written

A Kubernetes source (`core.openfeature.dev/`) delivers the `spec.flagSpec` of the resource as written, without rewriting it.

### Fixes for failure cases

#### A file read or decode error keeps the current flags

When a `file:` source cannot be read or decoded, flagd keeps serving the flags it has.
Before, it replaced them with an empty configuration.

#### A closed file watch reconnects

When the watch on a `file:` source closes, flagd reconnects.
Before, it stopped.

#### Unchanged file content is not delivered again

A `file:` source delivers a configuration only when its content changed.
The file metrics count an empty file's `{}` and skip unchanged content.

#### Every fetch has a default deadline

Every fetch of an `http://`, `https://` or blob source has a deadline of the larger of the polling interval and 30 seconds.
Polling backs off after two failures.

#### `incrementalUpdates`, which is experimental and opt-in

The experimental `incrementalUpdates` option is honoured for `http`, `file` and blob sources.
It stays off unless you set it.

#### Telemetry is flushed on shutdown

Spans and metrics that are still buffered are exported before flagd exits.

## Known breaks

- **`go install github.com/open-feature/flagd/flagd@<version>` fails.**
    The `flagd/go.mod` carries local `replace` directives for the component modules, which has been so since the evaluator became its own module.
    Use the binary archives or the image, which are the supported install paths.
    Restoring `go install` needs published component modules (UC-012).

## Rolling back

Nothing in your configuration changes in an upgrade, so a rollback needs no configuration change.
Start the previous flagd release with the same command, configuration file and environment variables.
If the behaviour you saw is not listed above, report it to the maintainers as a compatibility defect.

## For embedders

If you import the `components` package of the `flagd` module rather than run the binary:

- `components.Official()` is now `components.Components()`.
- The hand-written `Inventory` file is gone: `components.go` is generated by the builder and declares `Inventory()`.

The module split and the migration of providers to component modules are the subject of UC-015.
