#!/usr/bin/env bash
# The release dry run of the standard distributions (UC-012, Q8). Nothing is pushed, signed or tagged.
#
# flagd-minimal takes the full path: the 11 archives of a release, standard and FIPS images for linux/amd64 and
# linux/arm64 as OCI layouts with attestations, the FIPS refusal and `version --verbose`. flagd-cloud, flagd-kubernetes
# and flagd-sync are checked on what differs: the archive and image names of BR-012, one linux/amd64 archive with its
# contents, and `version --verbose`. flagd-full is released under flagd's names by the flagd checks of release-dry-run.sh.
#
# usage: release-dry-run-distributions.sh <version without v> <commit> <date> <staging directory>
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
others="flagd-cloud flagd-kubernetes flagd-sync"

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

note ""
note "#### Standard distributions"

mkdir -p "${out}"
(cd builder && GOWORK=off go build -o "${out}/relay-builder" ./cmd/relay-builder)
for d in ${full_path} ${others}; do
  GOWORK=off "${out}/relay-builder" --manifest "distributions/${d}/manifest.yaml" --skip-compile >/dev/null
done

archive_names() { # <name> <variants: standard fips> [platform labels]
  local name="$1"
  if [[ " $2 " == *" standard "* ]]; then
    for label in ${3:-Darwin_arm64 Darwin_x86_64 Linux_arm64 Linux_x86_64}; do echo "${name}_${version}_${label}.tar.gz"; done
    [ -n "${3:-}" ] || for label in Windows_x86_64 Windows_i386; do echo "${name}_${version}_${label}.zip"; done
  fi
  if [[ " $2 " == *" fips "* ]]; then
    for label in Darwin_arm64 Darwin_x86_64 Linux_arm64 Linux_x86_64; do echo "${name}-fips_${version}_${label}.tar.gz"; done
    echo "${name}-fips_${version}_Windows_x86_64.zip"
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

# the names and tags a release pushes for a distribution (BR-012); the dry run pushes none of them
image_tags() { # <published name>
  for suffix in latest "v${version}" latest-fips "v${version}-fips"; do echo "${registry}/$1:${suffix}"; done
}

# flagd-minimal: the full path
d="${full_path}"
name="$(published_name "${d}")"
dir="${out}/${d}"
.github/scripts/release-build.sh "${name}" "${version}" "${commit}" "${date}" "${dir}" "distributions/${d}/build"
check_archives "${name}" "${dir}" "$(archive_names "${name}" "standard fips")"
tar -xzf "${dir}/${name}_${version}_Linux_x86_64.tar.gz" -C "${dir}" "./${name}_linux_x86_64"
expect_equal "${name}: build info path" "github.com/open-feature-forking/openfeature-relay/distributions/${d}" \
  "$(go version -m "${dir}/${name}_linux_x86_64" | awk '$1 == "path" {print $2}')"
check_version "${name}" "${dir}/${name}_linux_x86_64"
tar -xzf "${dir}/${name}-fips_${version}_Linux_x86_64.tar.gz" -C "${dir}" "./${name}-fips_linux_amd64"
go version -m "${dir}/${name}-fips_linux_amd64" | grep -q -- '-tags=fips140,fips140v1.0' \
  && note "- ok: ${name}: the FIPS build carries the tags fips140 and fips140v1.0" \
  || fail "${name}: the FIPS archive is not a FIPS 140-3 build"
# BR-007: FIPS mode cannot be switched off in the FIPS binary
if GODEBUG=fips140=off timeout 20 "${dir}/${name}-fips_linux_amd64" start -f file:./config/samples/example_flags.flagd.json >/dev/null 2>&1; then
  fail "${name}: the FIPS binary runs with GODEBUG=fips140=off"
else
  note "- ok: ${name}: the FIPS binary refuses GODEBUG=fips140=off"
fi

# the images: standard and FIPS, both platforms, as OCI layouts with provenance and an SBOM (Q6, Q11). The default driver
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
for variant in standard fips; do
  if [ "${variant}" = fips ]; then fips=on; suffix="-fips"; else fips=off; suffix=""; fi
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

# the other three: only what differs from flagd-minimal
for d in ${others}; do
  name="$(published_name "${d}")"
  dir="${out}/${d}"
  RELEASE_PLATFORMS=linux/amd64 RELEASE_VARIANTS=standard \
    .github/scripts/release-build.sh "${name}" "${version}" "${commit}" "${date}" "${dir}" "distributions/${d}/build"
  check_archives "${name}" "${dir}" "${name}_${version}_Linux_x86_64.tar.gz"
  tar -xzf "${dir}/${name}_${version}_Linux_x86_64.tar.gz" -C "${dir}" "./${name}_linux_x86_64"
  check_version "${name}" "${dir}/${name}_linux_x86_64"
done

# the published manifests (Q10): the placeholder version and no replaces, every module a component module of this repository
mkdir -p "${out}/manifests"
for d in ${full_path} ${others}; do
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

for d in ${full_path} ${others}; do
  name="$(published_name "${d}")"
  expect_equal "${name}: image names and tags" "${registry}/${name}:latest ${registry}/${name}:v${version} ${registry}/${name}:latest-fips ${registry}/${name}:v${version}-fips" \
    "$(image_tags "${name}" | tr '\n' ' ' | sed 's/ $//')"
done

if [ "${failures}" -gt 0 ]; then exit 1; fi
