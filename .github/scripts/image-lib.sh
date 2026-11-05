#!/usr/bin/env bash
# Shared by release-dry-run-distributions.sh, image-build.sh and image-testbed.sh (step 17): builds the two-platform
# OCI layout of an image, checks the layout, and runs the amd64 image taken out of the layout. Nothing is pushed here;
# publish-images.sh pushes the layouts this builds.
#
# Source it after defining: version (without v), commit, date, out, and the functions note, fail and expect_equal.
# It reads DRY_RUN_LAYOUTS (where the layouts go, default "${out}/layouts") and expects `builder` to name a buildx builder
# with the docker-container driver, which the OCI exporter needs.

registry=ghcr.io/open-feature-forking
short="${commit:0:7}"
layouts="${DRY_RUN_LAYOUTS:-${out}/layouts}"
oci_layout="$(dirname "${BASH_SOURCE[0]}")/oci-layout.py"
mkdir -p "${layouts}"

new_builder() { # sets `builder` to a throwaway container builder that is removed on exit
  builder="dry-run-images-$$"
  docker buildx create --driver docker-container --name "${builder}" >/dev/null
  trap 'docker buildx rm "${builder}" >/dev/null 2>&1 || true' EXIT
}

# build_layout <name> <suffix> <extra buildx arguments...>: the tags are :main<suffix> and :sha-<short><suffix>; the suffix is
# empty, as no FIPS image is published
build_layout() {
  local name="$1" suffix="$2" kv
  local meta=()
  shift 2
  while IFS= read -r kv; do
    meta+=(--label "${kv}" --annotation "index:${kv}" --annotation "manifest:${kv}")
  done < <(python3 "${oci_layout}" marking --name "${name}" --revision "${commit}" --version "main-${short}" --created "${date}")
  docker buildx build --builder "${builder}" -q --platform linux/amd64,linux/arm64 --provenance=mode=max --sbom=true \
    "${meta[@]}" -t "${registry}/${name}:main${suffix}" -t "${registry}/${name}:sha-${short}${suffix}" \
    --output "type=oci,dest=${layouts}/${name}${suffix}.tar" "$@" . >/dev/null
}

# check_layout <name> <suffix>: tags, platforms, attestations, labels and annotations
check_layout() {
  local name="$1" suffix="$2" line checks=0
  while IFS= read -r line; do
    case "${line}" in
      ok:*) checks=$((checks + 1)) ;;
      *) fail "${name}${suffix}: layout: ${line}" ;;
    esac
  done < <(python3 "${oci_layout}" check "${layouts}/${name}${suffix}.tar" --name "${name}" \
    --tags "${registry}/${name}:main${suffix},${registry}/${name}:sha-${short}${suffix}" \
    --revision "${commit}" --version "main-${short}" --created "${date}" 2>&1 || true)
  if [ "${checks}" -gt 0 ]; then
    note "- ok: ${name}${suffix}: the layout (${checks} checks: tags, platforms, attestations, marking)"
  fi
}

# check_runtime <name> <suffix> <distribution>: the amd64 image of the layout runs; the daemon cannot load the layout
# itself, so oci-layout.py writes a docker-archive of that one platform. arm64 is checked as a layout only.
check_runtime() {
  local name="$1" suffix="$2" distribution="$3" ref printed
  ref="${registry}/${name}:main${suffix}"
  python3 "${oci_layout}" load "${layouts}/${name}${suffix}.tar" "${ref}" | docker load >/dev/null
  expect_equal "${ref}: entrypoint and user" "[/flagd-build] 65532:65532" \
    "$(docker image inspect "${ref}" --format '{{.Config.Entrypoint}} {{.Config.User}}')"
  printed="$(docker run --rm "${ref}" version --verbose)"
  expect_equal "${ref}: version" "flagd: v${version} (${commit}), built at: ${date}" "$(grep '^flagd:' <<<"${printed}")"
  expect_equal "${ref}: distribution" "distribution: ${distribution} v${version}" "$(grep '^distribution:' <<<"${printed}")"
  docker rmi --force "${ref}" >/dev/null 2>&1 || true
}
