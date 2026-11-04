# test/image

Tests of the reference Dockerfile (`builder/reference/Dockerfile`, UC-002). They build images with `docker buildx`, so
they run only with `IMAGE_TEST=1` (`make image-test`); `TestReferenceDockerfilePins` needs no Docker.

The `fixtures/arm64-broken` files carry a `.fixture` suffix so that the Go tooling of the workspace ignores them; the
test copies them to a temporary directory under their real names.
