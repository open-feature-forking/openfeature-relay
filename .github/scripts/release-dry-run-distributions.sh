#!/usr/bin/env bash
# The release dry run of the standard distributions (UC-012, Q8). Nothing is pushed, signed or tagged.
#
# flagd-minimal takes the full path: the 11 archives of a release, standard and FIPS images for linux/amd64 and
# linux/arm64 as OCI layouts with attestations, the FIPS refusal and `version --verbose`. flagd-minimal-otel, flagd-cloud, flagd-kubernetes
# and flagd-sync are checked on what differs: the archive names of BR-012, one linux/amd64 archive with its
# contents, and `version --verbose`. flagd-full is released under flagd's names by the flagd checks of release-dry-run.sh.
#
# DRY_RUN_DISTS names the distributions to check (default: all five); DRY_RUN_SCOPE=reduced builds the full path's archives for
# linux/amd64 only (one standard, one FIPS) and keeps its images and every check.
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

# shellcheck source=image-lib.sh
source "$(dirname "$0")/image-lib.sh"

note ""
note "#### Standard distributions"

mkdir -p "${out}"
(cd builder && GOWORK=off go build -o "${out}/relay-builder" ./cmd/relay-builder)
for d in ${selected}; do
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

# flagd-minimal: the full path
if chosen "${full_path}"; then
d="${full_path}"
name="$(published_name "${d}")"
dir="${out}/${d}"
RELEASE_PLATFORMS="${platforms}" .github/scripts/release-build.sh "${name}" "${version}" "${commit}" "${date}" "${dir}" "distributions/${d}/build"
if [ -n "${platforms}" ]; then
  expected="$(echo "${name}_${version}_Linux_x86_64.tar.gz"; echo "${name}-fips_${version}_Linux_x86_64.tar.gz")"
else
  expected="$(archive_names "${name}" "standard fips")"
fi
check_archives "${name}" "${dir}" "${expected}"
tar -xzf "${dir}/${name}_${version}_Linux_x86_64.tar.gz" -C "${dir}" "./${name}_linux_x86_64"
add_size "${name}" "${dir}/${name}_linux_x86_64"
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

# the image: both platforms, as an OCI layout with provenance, an SBOM and the proof-of-concept marking
# (Q6, Q11, step 17). The layouts are what publish-images.sh pushes; the amd64 image is run from the layout. The default
# driver cannot export OCI, so a throwaway container builder does.
new_builder
build_layout "${name}" "" -f builder/reference/Dockerfile --build-arg "DIST_PATH=distributions/${d}/build" \
  --build-arg "VERSION=v${version}" --build-arg "COMMIT=${commit}" --build-arg "DATE=${date}"
check_layout "${name}" ""
check_runtime "${name}" "" "${name}"
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
