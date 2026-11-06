# Main Dockerfile for flagd builds
# Build the manager binary
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:4c9fe60190a2a3350ddc51de80d0224b8a6698d12bdfc999fee45ea9d6c46dbc AS builder
# The toolchain determines which FIPS snapshots exist, so pin it.
ENV GOTOOLCHAIN=local

WORKDIR /src

ARG TARGETOS
ARG TARGETARCH
ARG VERSION
ARG COMMIT
ARG DATE
# Set to "on" for the FIPS variant, built against the CMVP-certified Go
# Cryptographic Module v1.0.0 (certificate #5247).
ARG FIPS=off

# Download dependencies as a separate step to take advantage of Docker's caching.
# Leverage a cache mount to /go/pkg/mod/ to speed up subsequent builds.
# Leverage bind mounts to go.sum and go.mod to avoid having to copy them into
# the container.
RUN --mount=type=cache,target=/go/pkg/mod/ \
    --mount=type=bind,source=./core/go.mod,target=./core/go.mod \
    --mount=type=bind,source=./core/go.sum,target=./core/go.sum \
    --mount=type=bind,source=./sync/file/go.mod,target=./sync/file/go.mod \
    --mount=type=bind,source=./sync/file/go.sum,target=./sync/file/go.sum \
    --mount=type=bind,source=./sync/flagd-grpc/go.mod,target=./sync/flagd-grpc/go.mod \
    --mount=type=bind,source=./sync/flagd-grpc/go.sum,target=./sync/flagd-grpc/go.sum \
    --mount=type=bind,source=./sync/http/go.mod,target=./sync/http/go.mod \
    --mount=type=bind,source=./sync/http/go.sum,target=./sync/http/go.sum \
    --mount=type=bind,source=./sync/openfeature-operator/go.mod,target=./sync/openfeature-operator/go.mod \
    --mount=type=bind,source=./sync/openfeature-operator/go.sum,target=./sync/openfeature-operator/go.sum \
    --mount=type=bind,source=./sync/gcs/go.mod,target=./sync/gcs/go.mod \
    --mount=type=bind,source=./sync/gcs/go.sum,target=./sync/gcs/go.sum \
    --mount=type=bind,source=./sync/azblob/go.mod,target=./sync/azblob/go.mod \
    --mount=type=bind,source=./sync/azblob/go.sum,target=./sync/azblob/go.sum \
    --mount=type=bind,source=./sync/s3/go.mod,target=./sync/s3/go.mod \
    --mount=type=bind,source=./sync/s3/go.sum,target=./sync/s3/go.sum \
    --mount=type=bind,source=./sync/builder/go.mod,target=./sync/builder/go.mod \
    --mount=type=bind,source=./sync/builder/go.sum,target=./sync/builder/go.sum \
    --mount=type=bind,source=./telemetry/otel/go.mod,target=./telemetry/otel/go.mod \
    --mount=type=bind,source=./telemetry/otel/go.sum,target=./telemetry/otel/go.sum \
    --mount=type=bind,source=./eventsink/webhook/go.mod,target=./eventsink/webhook/go.mod \
    --mount=type=bind,source=./eventsink/webhook/go.sum,target=./eventsink/webhook/go.sum \
    --mount=type=bind,source=./evaluator/flagd/go.mod,target=./evaluator/flagd/go.mod \
    --mount=type=bind,source=./evaluator/flagd/go.sum,target=./evaluator/flagd/go.sum \
    --mount=type=bind,source=./configformat/flagd/go.mod,target=./configformat/flagd/go.mod \
    --mount=type=bind,source=./configformat/flagd/go.sum,target=./configformat/flagd/go.sum \
    --mount=type=bind,source=./flagd/go.mod,target=./flagd/go.mod \
    --mount=type=bind,source=./flagd/go.sum,target=./flagd/go.sum \
    --mount=type=bind,source=./runtime/go.mod,target=./runtime/go.mod \
    --mount=type=bind,source=./runtime/go.sum,target=./runtime/go.sum \
    --mount=type=bind,source=./service/transport/go.mod,target=./service/transport/go.mod \
    --mount=type=bind,source=./service/transport/go.sum,target=./service/transport/go.sum \
    --mount=type=bind,source=./service/flag-evaluation/go.mod,target=./service/flag-evaluation/go.mod \
    --mount=type=bind,source=./service/flag-evaluation/go.sum,target=./service/flag-evaluation/go.sum \
    --mount=type=bind,source=./service/flag-evaluation-legacy/go.mod,target=./service/flag-evaluation-legacy/go.mod \
    --mount=type=bind,source=./service/flag-evaluation-legacy/go.sum,target=./service/flag-evaluation-legacy/go.sum \
    --mount=type=bind,source=./service/ofrep/go.mod,target=./service/ofrep/go.mod \
    --mount=type=bind,source=./service/ofrep/go.sum,target=./service/ofrep/go.sum \
    --mount=type=bind,source=./service/flag-sync/go.mod,target=./service/flag-sync/go.mod \
    --mount=type=bind,source=./service/flag-sync/go.sum,target=./service/flag-sync/go.sum \
    --mount=type=bind,source=./service/event-collector/go.mod,target=./service/event-collector/go.mod \
    --mount=type=bind,source=./service/event-collector/go.sum,target=./service/event-collector/go.sum \
    go work init ./core ./sync/file ./sync/flagd-grpc ./sync/http ./sync/openfeature-operator ./sync/gcs ./sync/azblob ./sync/s3 ./sync/builder ./telemetry/otel ./eventsink/webhook ./evaluator/flagd ./configformat/flagd ./runtime ./service/transport ./service/flag-evaluation ./service/flag-evaluation-legacy ./service/ofrep ./service/flag-sync ./service/event-collector ./flagd && go mod download

# Build the application.
# Leverage a cache mount to /go/pkg/mod/ to speed up subsequent builds.
# Leverage a bind mount to the current directory to avoid having to copy the
# source code into the container.
RUN --mount=type=cache,target=/go/pkg/mod/ \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=bind,source=./core,target=./core \
    --mount=type=bind,source=./sync,target=./sync \
    --mount=type=bind,source=./telemetry,target=./telemetry \
    --mount=type=bind,source=./eventsink,target=./eventsink \
    --mount=type=bind,source=./evaluator,target=./evaluator \
    --mount=type=bind,source=./configformat,target=./configformat \
    --mount=type=bind,source=./runtime,target=./runtime \
    --mount=type=bind,source=./service,target=./service \
    --mount=type=bind,source=./flagd,target=./flagd \
    if [ "${FIPS}" = on ]; then export GOFIPS140=v1.0.0 TAGS=fips140; else TAGS=; fi; \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -a -tags "${TAGS}" -ldflags "-X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" -o /bin/flagd-build flagd/main.go

# Both tags together prove the variant built as intended: the toolchain adds
# fips140v1.0 only when GOFIPS140 is set, and fips140 is flagd's own.
RUN [ "${FIPS}" != on ] || go version -m /bin/flagd-build | grep -q -- '-tags=fips140,fips140v1.0'

# # Use distroless as minimal base image to package the manager binary
# # Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
WORKDIR /
COPY --from=builder /bin/flagd-build .
USER 65532:65532

ENTRYPOINT ["/flagd-build"]
