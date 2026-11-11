# The flagd testbed image (UC-022): the layout of the flagd-testbed v3.11.0 image, with the testbed distribution in place
# of the launchpad and flagd. /flagd is the testbed, which serves the control API on port 8080 and runs flagd behind
# its listener gate. The data (flags, configurations, certificates) is the pinned test-harness submodule, unmodified.
#
# The build context is the repository root, with the test-harness submodule checked out:
#
#   docker build -f distributions/flagd-testbed/Dockerfile --build-arg VERSION=v1.2.3 -t flagd-testbed:local .
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:4c9fe60190a2a3350ddc51de80d0224b8a6698d12bdfc999fee45ea9d6c46dbc AS builder
ENV GOTOOLCHAIN=local
# The distribution source is a module of its own; a go.work of the context must not capture it.
ENV GOWORK=off

WORKDIR /src

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=HEAD
ARG DATE=unknown

# The Builder generates the main package and the components of the testbed from its manifest, then the result compiles.
# The bind mount is writable so that the generated source can sit next to the manifest, where its replaces resolve; the
# write is discarded with the step.
RUN --mount=type=cache,target=/go/pkg/mod/ \
    --mount=type=cache,target=/root/.cache/go-build,id=testbed-${TARGETOS}-${TARGETARCH} \
    --mount=type=bind,target=/src,rw \
    (cd builder && go build -o /bin/relay-builder ./cmd/relay-builder) && \
    /bin/relay-builder --manifest distributions/flagd-testbed/manifest.yaml --skip-compile && \
    cd distributions/flagd-testbed/build && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -a -ldflags "-X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" -o /bin/flagd .

# The server certificate is signed at build time with the test-harness CA, as upstream's image does.
FROM --platform=$BUILDPLATFORM alpine/openssl@sha256:96e53bf9b9041fdc8a33ea61356688506bbddc368c70d6e8a9acdd28475e8537 AS certs

COPY test-harness/ssl/* ./

RUN openssl genpkey -algorithm RSA -out server-key.pem -pkeyopt rsa_keygen_bits:2048
RUN openssl req -new -key server-key.pem -out server.csr -subj "/CN=localhost" \
      -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" \
   && openssl x509 -req -in server.csr -CA custom-root-cert.crt -CAkey custom-ca.key -CAcreateserial -out server-cert.pem -days 365 -sha256 \
      -copy_extensions copy

FROM busybox:1.38@sha256:a46257e06c82ad85242d0d38077d0a52d9896389ca60a1a59d3463db586e5fa4 AS testbed

WORKDIR /

COPY --from=builder /bin/flagd /flagd
COPY test-harness/flags/* ./rawflags/
COPY test-harness/launchpad/configs /configs

COPY --from=certs server-cert.pem /ssl/
COPY --from=certs server-key.pem /ssl/
COPY --from=certs custom-root-cert.crt /ssl/

# the volume the compose file mounts, where allFlags.json is rendered; WORKDIR creates it without a RUN, which would
# need an emulated arm64 shell
WORKDIR /flags
WORKDIR /

# 8013 flag evaluation, 8014 management, 8015 flag sync, 8080 the control API. Nothing listens on 8016: the testbed has
# no OFREP endpoint, and no pinned suite maps that port.
EXPOSE 8013 8014 8015 8016 8080

ENTRYPOINT ["/flagd"]
