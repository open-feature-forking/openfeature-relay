#!/usr/bin/env bash
# The release dry run of the standard distributions (UC-012, Q8). Nothing is pushed, signed or tagged.
#
# flagd-minimal takes the full path: the archives of a release, an image for linux/amd64 and linux/arm64 as an OCI
# layout with attestations, and `version --verbose`. flagd-minimal-otel, flagd-cloud, flagd-kubernetes
# and flagd-sync are checked on what differs: the archive names of BR-012, one linux/amd64 archive with its
# contents, and `version --verbose`. flagd-full is released under flagd's names by the flagd checks of release-dry-run.sh.
#
# DRY_RUN_DISTS names the distributions to check (default: all five); DRY_RUN_SCOPE=reduced builds the full path's archives for
# linux/amd64 only and keeps its image and every check.
#
# usage: [DRY_RUN_DISTS="flagd-sync ..."] [DRY_RUN_SCOPE=full] release-dry-run-distributions.sh <version without v> <commit> <date> <staging directory>
#
# Needs Docker with buildx, cyclonedx-gomod and the Go toolchain on PATH, and runs from the root of the repository, on
# linux/amd64 (it runs the binaries it builds).
set -euo pipefail

version="${1:?version without the v}"
commit="${2:?commit}"
date="${3:?date}"
out="${4:?staging directory}"
summary="${GITHUB_STEP_SUMMARY:-/dev/null}"
registry=ghcr.io/open-feature

full_path=flagd-minimal
others="flagd-minimal-otel flagd-cloud flagd-kubernetes flagd-sync"
selected="${DRY_RUN_DISTS-${full_path} ${others}}"
platforms=""
[ "${DRY_RUN_SCOPE:-full}" != reduced ] || platforms="linux/amd64"
chosen() { [[ " ${selected} " == *" $1 "* ]]; } # <distribution>

failures=0
note() { echo "$*" | tee -a "${summary}"; }
fail() { echo "::error::$*"; echo "- FAILED: $*" >>"${summary}"; failures=$((failures + 1)); }
expect_equal() { # <what> <expected> <got>
  if [ "$2" = "$3" ]; then note "- ok: $1"; else fail "$1: expected '$2', got '$3'"; fi
}

# BR-012: flagd for flagd-full, flagd-<distribution> for every other one. The directory of a distribution already carries
# the prefix.
published_name() { # <distribution directory>
  if [ "$1" = flagd-full ]; then echo flagd; else echo "$1"; fi
}

# the size of each linux_x86_64 binary in MiB, for the step summary
sizes_file="${out}/sizes.md"
add_size() { # <name> <binary>
  printf '| %s | %.1f |\n' "$1" "$(awk -v b="$(stat -c %s "$2")" "BEGIN { print b / 1048576 }")" >>"${sizes_file}"
}

note ""
note "#### Standard distributions"

mkdir -p "${out}"
(cd builder && GOWORK=off go build -o "${out}/relay-builder" ./cmd/relay-builder)
for d in ${selected}; do
  GOWORK=off "${out}/relay-builder" --manifest "distributions/${d}/manifest.yaml" --skip-compile >/dev/null
done

archive_names() { # <name> <variants: standard> [platform labels]
  local name="$1"
  if [[ " $2 " == *" standard "* ]]; then
    for label in ${3:-Darwin_arm64 Darwin_x86_64 Linux_arm64 Linux_x86_64}; do echo "${name}_${version}_${label}.tar.gz"; done
    [ -n "${3:-}" ] || for label in Windows_x86_64 Windows_i386; do echo "${name}_${version}_${label}.zip"; done
  fi
}

check_archives() { # <name> <directory> <expected names>
  local name="$1" dir="$2" expected="$3" got archive listing
  got="$(cd "${dir}" && find . -maxdepth 1 \( -name '*.tar.gz' -o -name '*.zip' \) -printf '%f\n')"
  expect_equal "${name}: archive names" "$(sort <<<"${expected}")" "$(sort <<<"${got}")"
  [ -s "${dir}/sbom.xml" ] && note "- ok: ${name}: sbom.xml is not empty" || fail "${name}: sbom.xml is empty"
  for archive in ${got}; do
    if [[ "${archive}" == *.tar.gz ]]; then listing="$(tar -tzf "${dir}/${archive}" | sed 's#^\./##')"; else listing="$(unzip -Z1 "${dir}/${archive}" | sed 's#^\./##')"; fi
    for entry in LICENSE CHANGELOG.md README.md sbom.xml; do
      grep -qx "${entry}" <<<"${listing}" || fail "${name}: ${archive} lacks ${entry}"
    done
    [ "$(wc -l <<<"${listing}")" -eq 5 ] || fail "${name}: ${archive} holds '${listing//$'\n'/ }', not a binary and four files"
  done
  note "- built ${name}: $(wc -l <<<"${got}") archives"
}

check_version() { # <name> <binary>: BR-003, the stamped version and the distribution's name
  local name="$1" bin="$2" printed
  printed="$("${bin}" version --verbose)"
  expect_equal "${name}: version" "flagd: v${version} (${commit}), built at: ${date}" "$(grep '^flagd:' <<<"${printed}")"
  expect_equal "${name}: distribution" "distribution: ${name} v${version}" "$(grep '^distribution:' <<<"${printed}")"
}

# flagd-minimal: the full path
if chosen "${full_path}"; then
d="${full_path}"
name="$(published_name "${d}")"
dir="${out}/${d}"
RELEASE_PLATFORMS="${platforms}" RELEASE_VARIANTS=standard .github/scripts/release-build.sh "${name}" "${version}" "${commit}" "${date}" "${dir}" "distributions/${d}/build"
if [ -n "${platforms}" ]; then
  expected="${name}_${version}_Linux_x86_64.tar.gz"
else
  expected="$(archive_names "${name}" standard)"
fi
check_archives "${name}" "${dir}" "${expected}"
tar -xzf "${dir}/${name}_${version}_Linux_x86_64.tar.gz" -C "${dir}" "./${name}_linux_x86_64"
add_size "${name}" "${dir}/${name}_linux_x86_64"
expect_equal "${name}: build info path" "github.com/open-feature-forking/openfeature-relay/distributions/${d}" \
  "$(go version -m "${dir}/${name}_linux_x86_64" | awk '$1 == "path" {print $2}')"
check_version "${name}" "${dir}/${name}_linux_x86_64"
# the image: both platforms, as an OCI layout with provenance and an SBOM (Q6, Q11). The default driver
# cannot export OCI, so a throwaway container builder does.
builder="dry-run-distributions-$$"
docker buildx create --driver docker-container --name "${builder}" >/dev/null
trap 'docker buildx rm "${builder}" >/dev/null 2>&1 || true' EXIT
image_build() { # <variant arg> <extra buildx arguments...>
  local fips="$1"
  shift
  docker buildx build --builder "${builder}" -q -f builder/reference/Dockerfile --build-arg "DIST_PATH=distributions/${d}/build" \
    --build-arg "VERSION=v${version}" --build-arg "COMMIT=${commit}" --build-arg "DATE=${date}" --build-arg "FIPS=${fips}" "$@" . >/dev/null
}
for variant in standard; do
  fips=off; suffix=""
  oci="${dir}/${name}${suffix}-oci.tar"
  image_build "${fips}" --platform linux/amd64,linux/arm64 --provenance=mode=max --sbom=true \
    -t "${registry}/${name}:v${version}${suffix}" -t "${registry}/${name}:latest${suffix}" --output "type=oci,dest=${oci}"
  found="$(python3 - "${oci}" <<'PY'
import json, sys, tarfile
tar = tarfile.open(sys.argv[1])
def blob(digest):
    return json.load(tar.extractfile("blobs/sha256/" + digest.split(":", 1)[1]))
top = json.load(tar.extractfile("index.json"))["manifests"]
names = sorted(m.get("annotations", {}).get("io.containerd.image.name", "") for m in top)
manifests = blob(top[0]["digest"])["manifests"]
platforms = sorted(m["platform"]["os"] + "/" + m["platform"]["architecture"] for m in manifests if m.get("platform", {}).get("os", "unknown") != "unknown")
attestations = [m for m in manifests if m.get("annotations", {}).get("vnd.docker.reference.type") == "attestation-manifest"]
kinds = set()
for m in attestations:
    for layer in blob(m["digest"]).get("layers", []):
        kinds.add(layer.get("annotations", {}).get("in-toto.io/predicate-type", ""))
print(",".join(names), " ".join(platforms), len(attestations), "provenance" if any("slsa.dev/provenance" in k for k in kinds) else "-", "sbom" if any("spdx" in k for k in kinds) else "-")
PY
)"
  expect_equal "${name}${suffix}: tags, platforms, attestation manifests, provenance and SBOM of the OCI image" \
    "${registry}/${name}:latest${suffix},${registry}/${name}:v${version}${suffix} linux/amd64 linux/arm64 2 provenance sbom" "${found}"
done
# the local platform runs: the identity, the user and the entrypoint of the image (BR-003)
local_tag="${registry}/${name}:v${version}"
image_build off --load -t "${local_tag}"
expect_equal "${local_tag}: entrypoint and user" "[/flagd-build] 65532:65532" \
  "$(docker image inspect "${local_tag}" --format '{{.Config.Entrypoint}} {{.Config.User}}')"
printed="$(docker run --rm "${local_tag}" version --verbose)"
expect_equal "${local_tag}: version" "flagd: v${version} (${commit}), built at: ${date}" "$(grep '^flagd:' <<<"${printed}")"
expect_equal "${local_tag}: distribution" "distribution: ${name} v${version}" "$(grep '^distribution:' <<<"${printed}")"
docker rmi --force "${local_tag}" >/dev/null 2>&1 || true
fi

# the other four: only what differs from flagd-minimal
for d in ${others}; do
  chosen "${d}" || continue
  name="$(published_name "${d}")"
  dir="${out}/${d}"
  RELEASE_PLATFORMS=linux/amd64 RELEASE_VARIANTS=standard \
    .github/scripts/release-build.sh "${name}" "${version}" "${commit}" "${date}" "${dir}" "distributions/${d}/build"
  check_archives "${name}" "${dir}" "${name}_${version}_Linux_x86_64.tar.gz"
  tar -xzf "${dir}/${name}_${version}_Linux_x86_64.tar.gz" -C "${dir}" "./${name}_linux_x86_64"
  add_size "${name}" "${dir}/${name}_linux_x86_64"
  check_version "${name}" "${dir}/${name}_linux_x86_64"
done

# the published manifests (Q10): the placeholder version and no replaces, every module a component module of this repository
mkdir -p "${out}/manifests"
for d in ${selected}; do
  rendered="${out}/manifests/${d}.yaml"
  if .github/scripts/render-published-manifest.sh "distributions/${d}/manifest.yaml" "${rendered}"; then
    if ! grep -q '^replaces:' "${rendered}" && ! grep -q 'version: v0\.0\.0}' "${rendered}"; then
      note "- ok: ${d}: the published manifest pins v0.0.0-dryrun and has no replaces"
    else
      fail "${d}: the published manifest keeps a replace or a version"
    fi
  else
    fail "${d}: the published manifest names a module that is not a component module of this repository"
  fi
done

if [ "${failures}" -gt 0 ]; then exit 1; fi
