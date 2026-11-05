#!/usr/bin/env bash
# Builds the relay testbed image once, as a two-platform OCI layout with provenance, an SBOM and the proof-of-concept
# marking, checks the layout, and loads its amd64 image under the tag flagd-testbed:layout. The contract and the suites run
# on that image (make testbed-contract-both testbed-suites TESTBED_RELAY_IMAGE=flagd-testbed:layout), so the tested image
# is the one publish-images.sh pushes (step 17). Nothing is pushed here.
#
# usage: [DRY_RUN_LAYOUTS=<directory>] image-testbed.sh <version without v> <commit> <date> <staging directory>
#
# Needs Docker with buildx and runs from the root of the repository with the test-harness submodule checked out.
set -euo pipefail

version="${1:?version without the v}"
commit="${2:?commit}"
date="${3:?date}"
out="${4:?staging directory}"
summary="${GITHUB_STEP_SUMMARY:-/dev/null}"

failures=0
note() { echo "$*" | tee -a "${summary}"; }
fail() { echo "::error::$*"; echo "- FAILED: $*" >>"${summary}"; failures=$((failures + 1)); }
expect_equal() { # <what> <expected> <got>
  if [ "$2" = "$3" ]; then note "- ok: $1"; else fail "$1: expected '$2', got '$3'"; fi
}

mkdir -p "${out}"
# shellcheck source=image-lib.sh
source "$(dirname "$0")/image-lib.sh"

note ""
note "#### Image: flagd-testbed"
new_builder
build_layout flagd-testbed "" -f distributions/flagd-testbed/Dockerfile \
  --build-arg "VERSION=v${version}" --build-arg "COMMIT=${commit}" --build-arg "DATE=${date}"
check_layout flagd-testbed ""
python3 "${oci_layout}" load "${layouts}/flagd-testbed.tar" "${registry}/flagd-testbed:main" | docker load >/dev/null
docker tag "${registry}/flagd-testbed:main" flagd-testbed:layout
docker rmi "${registry}/flagd-testbed:main" >/dev/null 2>&1 || true
note "- loaded the linux/amd64 image of the layout as flagd-testbed:layout"

if [ "${failures}" -gt 0 ]; then exit 1; fi
