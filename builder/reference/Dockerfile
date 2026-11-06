# Reference Dockerfile for a distribution the Builder generated (UC-002). It builds the image the way flagd's own
# Dockerfile does: the same two pinned digests, build flags and runtime image.
#
# The build context is the distribution source. A distribution whose manifest has no `replaces` uses its source
# directory as the context. One that replaces modules with directories of this repository uses the repository root as
# the context and DIST_PATH for the source directory, so that every replaced directory is inside the context:
#
#   docker buildx build -f builder/reference/Dockerfile --build-arg DIST_PATH=distributions/flagd-minimal/build \
#     --build-arg VERSION=v1.2.3 --build-arg COMMIT=$(git rev-parse --short HEAD) --build-arg DATE=$(date -u +%FT%TZ) .
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:4c9fe60190a2a3350ddc51de80d0224b8a6698d12bdfc999fee45ea9d6c46dbc AS builder
# The toolchain determines which FIPS snapshots exist, so pin it.
ENV GOTOOLCHAIN=local
# The distribution source is a module of its own; a go.work of the context must not capture it.
ENV GOWORK=off

WORKDIR /src

ARG TARGETOS
ARG TARGETARCH
# The directory of the distribution source in the context.
ARG DIST_PATH=.
ARG VERSION
ARG COMMIT
ARG DATE
# Set to "on" for the FIPS variant, built against the CMVP-certified Go
# Cryptographic Module v1.0.0 (certificate #5247).
ARG FIPS=off

# Download dependencies as a separate step to take advantage of Docker's caching.
RUN --mount=type=cache,target=/go/pkg/mod/ \
    --mount=type=bind,target=/src \
    cd "${DIST_PATH}" && go mod download

# Build the distribution. The Go build cache is per target platform.
RUN --mount=type=cache,target=/go/pkg/mod/ \
    --mount=type=cache,target=/root/.cache/go-build,id=reference-${TARGETOS}-${TARGETARCH} \
    --mount=type=bind,target=/src \
    cd "${DIST_PATH}" && \
    if [ "${FIPS}" = on ]; then export GOFIPS140=v1.0.0 TAGS=fips140; else TAGS=; fi; \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -a -tags "${TAGS}" -ldflags "-X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" -o /bin/flagd-build .

# Both tags together prove the variant built as intended: the toolchain adds
# fips140v1.0 only when GOFIPS140 is set, and fips140 is flagd's own.
RUN [ "${FIPS}" != on ] || go version -m /bin/flagd-build | grep -q -- '-tags=fips140,fips140v1.0'

# Use distroless as minimal base image to package the distribution binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329
WORKDIR /
COPY --from=builder /bin/flagd-build .
USER 65532:65532

ENTRYPOINT ["/flagd-build"]
