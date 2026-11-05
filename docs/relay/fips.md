---
description: Build any distribution as a FIPS 140-3 binary or image with the relay builder, and verify it
---

# Build a FIPS distribution

OpenFeature Relay's releases and images ship no FIPS 140-3 build, unlike today's flagd.
Any distribution, standard or custom, can be built as a FIPS 140-3 binary or image with the relay builder instead (C-009).
The FIPS build is compiled against the Go Cryptographic Module v1.0.0 (CMVP certificate #5247) and runs it in approved mode, with the same crypto boundary as flagd's own FIPS build.
What the FIPS build restricts, TLS above all, is in the [FIPS 140-3 reference](../reference/fips-140-3.md).

## Two settings

A FIPS build needs both:

- `GOFIPS140=v1.0.0` selects the frozen, certified module snapshot and makes the binary default to `GODEBUG=fips140=on`. Use the literal version: the `certified` and `inprocess` aliases resolve differently per Go toolchain.
- The build tag `fips140` makes the binary require that module: with the runtime's command line, which every standard distribution uses, a binary built with the tag but without `GOFIPS140` refuses to start.

Build with `CGO_ENABLED=0`, as every release build does.

## A binary

Generate the distribution's source with the builder, then compile it with the two settings:

```shell
GOWORK=off relay-builder --manifest manifest.yaml --skip-compile
cd build   # the manifest's dist.output_path
GOFIPS140=v1.0.0 CGO_ENABLED=0 go build -tags fips140 -o flagd-fips .
```

The builder stamps the version, commit and date as linker flags when it compiles; a build of your own sets them with `-ldflags "-X main.version=… -X main.commit=… -X main.date=…"` if you want them.
To build for another platform, set `GOOS` and `GOARCH` as well. FIPS mode is not available on `wasm`, `windows/386`, `openbsd` and `aix`.

## An image

The [reference Dockerfile](how-its-built.md) builds the FIPS variant with the build argument `FIPS=on`, and fails the build if the binary does not carry both tags:

```shell
docker buildx build -f builder/reference/Dockerfile \
  --build-arg DIST_PATH=distributions/flagd-minimal/build --build-arg FIPS=on \
  -t flagd-minimal:local-fips .
```

Tag a FIPS image with the `-fips` suffix, beside the regular image and never in its place.

## Verify it

The build settings are recorded in the binary:

```shell
go version -m ./flagd-fips | grep -E 'GOFIPS140|DefaultGODEBUG|tags'
```

```console
build   -tags=fips140,fips140v1.0
build   DefaultGODEBUG=fips140=on
build   GOFIPS140=v1.0.0-c2097c7c
```

`fips140` is flagd's tag; the toolchain adds `fips140v1.0` only when `GOFIPS140` is set, so both together prove a FIPS build.

Then run it with strict enforcement, where a non-approved algorithm errors or panics instead of running:

```shell
GODEBUG=fips140=only ./flagd-fips start --uri file:flags.json
```

Exercise the endpoints and sources your deployment uses: a TLS peer that offers only algorithms outside the approved set fails here.
Started with `GODEBUG=fips140=off`, a FIPS build with the runtime's command line refuses to run.

## Test the modules

In a checkout of this repository, `make test-fips` runs the tests of the component modules, flagd and flagd-proxy as a FIPS build under `GODEBUG=fips140=only`.
`make fips-closure` regenerates the crypto dependency closure of flagd and flagd-proxy recorded in `docs/reference/fips-crypto-closure.txt`, for review against the [FIPS 140-3 reference](../reference/fips-140-3.md).

## Not tested in CI

CI does not build or test a FIPS build of any distribution (C-009).
The image tests only check that the reference Dockerfile's `FIPS=on` produces a binary with both tags, for a test distribution, when the Dockerfile changes and nightly.
A FIPS build of any distribution, flagd-full included, is yours to build and verify as above.
