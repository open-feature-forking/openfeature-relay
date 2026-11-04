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

When the telemetry configuration cannot be built, flagd stops at startup.
From flagd's command line that is an unknown value of `OTEL_TRACES_EXPORTER` or `OTEL_METRICS_EXPORTER`.
Before, flagd logged the error and ran without telemetry.
An unreachable collector does not stop startup, today or before, because the exporters connect lazily.
Correct the exporter variables, or remove them.

#### Invalid blob URL options or Azure variables stop startup

Invalid options in a `gs://`, `azblob://` or `s3://` URL, or invalid Azure variables, stop flagd at startup.
Before, flagd started and never became ready.

#### `awssdk=v1` on an `s3://` source stops startup

The `s3://` source uses AWS SDK v2 only.
Remove `awssdk=v1` from the URL.

#### `event-sinks`, `middleware` and `endpoint-settings` in the configuration file are no longer ignored

The keys `event-sinks`, `middleware` and `endpoint-settings` of flagd's configuration file used to be ignored, like any key flagd does not know.
They now configure the components a distribution contains besides its official ones: event sinks, middleware and the service endpoints of extensions.
An entry names its component with `type`, and the other keys are that component's settings.
An entry whose type the distribution lacks makes it stop at startup, with an error that names the type and lists what the distribution has.
flagd-full has the `webhook` event sink and the `event-collector` service endpoint, and no middleware, so a `middleware` entry always stops it, and so does an `event-sinks` entry of another type.
`endpoint-settings` also stops startup when it holds `flag-evaluation`, `flag-sync` or `ofrep`: the settings of those endpoints are the flags of `flagd start`.
The keys are matched as written, in lower case; `Event-Sinks` is still ignored, as every unknown key is.
A file that holds the keys with no entries, as `event-sinks: []`, starts as before.
If your configuration file has these keys for another purpose, remove them before you upgrade.

### Visible to clients, unlikely to break them

#### Flag sync's `Last-Modified` moves only on a configuration change

The `Last-Modified` of the flag sync service endpoint changes when the flag configuration changes, and not on every reload.

#### flagd-full links two more modules and the `event-collector` key is no longer ignored

flagd-full now contains the webhook event sink, `eventsink/webhook`, and the event collector, `service/event-collector`, so `go version -m` and the SBOM list two more modules.
Neither does anything until you configure it: the sink subscribes to nothing without an `event-sinks` entry, and the collector opens no port.
The `event-collector` key of the configuration file used to be ignored. Now `event-collector: {port: 8017}` opens the collector's port, and an unknown setting under it stops startup with an error that names it.
The collector takes `--cors-origin`, `--max-request-body` and `--max-request-header` as OFREP does; a setting under `event-collector` or `endpoint-settings` wins over a flag.
If your configuration file has an `event-collector` key for another purpose, remove it before you upgrade.

#### OFREP `flagConfigEtag` values change

The values are opaque, so clients that only send them back are not affected.
A change in a vendor flag format now sends a refetch event on the OFREP event stream.

#### Flag sync reads the `Flagd-Configuration-Format` header

A flag sync request can name a configuration format in the `Flagd-Configuration-Format` header, on `SyncFlags`, `FetchAllFlags` and the `/v1/flags` route.
A request without the header, or with `flagd`, is answered as before.
Before, flagd ignored the header.
Now a value that names a configuration format the distribution lacks is answered `InvalidArgument` (HTTP 400), and the message lists the formats the distribution has.
flagd-full has flagd's own format only, so no client of flagd-full gets another format's documents.
If a client sends this header with another value, remove it.

#### Shutdown: event streams get `provider_shutdown`

On `SIGTERM`, event streams receive `provider_shutdown` and end at once, and flag sync streams end cleanly in under 2 seconds.
Shutdown can take up to 10 seconds in total, because telemetry is flushed.
If you set a termination grace period, keep it above 10 seconds.

#### The operator source delivers `spec.flagSpec` as written

A Kubernetes source (`core.openfeature.dev/`) delivers the `spec.flagSpec` of the resource as written, without rewriting it.

#### `flagd components` lists the components instead of failing

`flagd components` is a new command that lists the components compiled into the binary, as text or with `--output json`.
It is hidden from `flagd --help`.
A script that relied on the command failing as an unknown command now sees exit status 0.

### Fixes for failure cases

#### A file read or decode error keeps the current flags

When a `file:` source cannot be read or decoded, flagd keeps serving the flags it has.
Before, it replaced them with an empty configuration.

#### A closed file watch reconnects

When the watch on a `file:` source closes, flagd reconnects.
Before, flagd exited.

#### Unchanged file content is not delivered again

A `file:` source delivers a configuration only when its content changed.
The file metrics count an empty file's `{}` and skip unchanged content.

#### Every fetch has a default deadline

Every fetch of a `file:`, `http://`, `https://` or blob source has a deadline of the larger of the polling interval and 30 seconds.
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

- **flagd-proxy words the error for a missing source file differently.**
    It syncs through this repository's file sync provider, so a request for a file that does not exist fails with the provider's text.
    The status code is the same; only the message differs.

## Rolling back

An upgrade changes your configuration only where an item above asks for it (an `awssdk=v1` option, an exporter variable that flagd could not build), so a rollback needs no change beyond undoing those.
Start the previous flagd release with the same command, configuration file and environment variables.
If the behaviour you saw is not listed above, report it to the maintainers as a compatibility defect.

## For embedders

If you import the `components` package of the `flagd` module rather than run the binary:

- `components.Official()` is now `components.Components()`.
- The hand-written `Inventory` file is gone: `components.go` is generated by the builder and declares `Inventory()`.
