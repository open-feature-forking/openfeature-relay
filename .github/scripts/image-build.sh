#!/usr/bin/env bash
# Builds the images of standard distributions as two-platform OCI layouts, standard and FIPS, and checks them (step 17):
# the layout, and the amd64 image run from the layout. flagd-minimal is built by release-dry-run-distributions.sh with the
# rest of its full path. Nothing is pushed; publish-images.sh pushes the layouts.
#
# usage: [DRY_RUN_LAYOUTS=<directory>] image-build.sh <version without v> <commit> <date> <staging directory> <distribution>...
#
# flagd-full is built from the flagd module, which the Builder keeps generated in place; every other distribution is
# generated into its build/ directory first. Needs Docker with buildx and the Go toolchain on PATH, runs from the root of
# the repository, on linux/amd64.
set -euo pipefail

version="${1:?version without the v}"
commit="${2:?commit}"
date="${3:?date}"
out="${4:?staging directory}"
shift 4
[ "$#" -gt 0 ] || { echo "no distribution named" >&2; exit 2; }
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
note "#### Images: $*"
(cd builder && GOWORK=off go build -o "${out}/relay-builder" ./cmd/relay-builder)
new_builder
for d in "$@"; do
  if [ "${d}" = flagd-full ]; then
    source_dir=flagd
  else
    source_dir="distributions/${d}/build"
    GOWORK=off "${out}/relay-builder" --manifest "distributions/${d}/manifest.yaml" --skip-compile >/dev/null
  fi
  for variant in standard fips; do
    if [ "${variant}" = fips ]; then fips=on; suffix="-fips"; else fips=off; suffix=""; fi
    build_layout "${d}" "${suffix}" -f builder/reference/Dockerfile --build-arg "DIST_PATH=${source_dir}" \
      --build-arg "VERSION=v${version}" --build-arg "COMMIT=${commit}" --build-arg "DATE=${date}" --build-arg "FIPS=${fips}"
    check_layout "${d}" "${suffix}"
    check_runtime "${d}" "${suffix}" "${d}"
  done
done

if [ "${failures}" -gt 0 ]; then exit 1; fi
