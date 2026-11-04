#!/usr/bin/env bash
# Runs the build steps of a release without publishing or signing anything, and checks what a release would produce
# against what upstream flagd's release produces (UC-009 BR-007): the archives of flagd and flagd-proxy by name and
# content, the version line, the build info path and the FIPS tags, and the image of flagd with its tags, entrypoint and
# user. The names are literal, not derived from the repository this runs in, which would render other names here.
#
# usage: [DRY_RUN_ITEMS="flagd"] release-dry-run.sh [version without v] [commit] [date]
#
# Needs Docker, cyclonedx-gomod and the Go toolchain on PATH, and runs from the root of the repository.
set -euo pipefail

version="${1:-9.9.9}"
commit="${2:-$(git rev-parse HEAD)}"
date="${3:-2026-01-01T00:00:00Z}"
out="$(mktemp -d)"
summary="${GITHUB_STEP_SUMMARY:-/dev/null}"

failures=0
note() { echo "$*" | tee -a "${summary}"; }
fail() { echo "::error::$*"; echo "- FAILED: $*" >>"${summary}"; failures=$((failures + 1)); }
expect_equal() { # <what> <expected> <got>
  if [ "$2" = "$3" ]; then note "- ok: $1"; else fail "$1: expected '$2', got '$3'"; fi
}

note "### Release dry run (version v${version}, commit ${commit})"
note ""

for item in ${DRY_RUN_ITEMS:-flagd flagd-proxy}; do
  dir="${out}/${item}"
  .github/scripts/release-build.sh "${item}" "${version}" "${commit}" "${date}" "${dir}"

  expected="$(
    for label in Darwin_arm64 Darwin_x86_64 Linux_arm64 Linux_x86_64; do echo "${item}_${version}_${label}.tar.gz"; done
    for label in Windows_x86_64 Windows_i386; do echo "${item}_${version}_${label}.zip"; done
    for label in Darwin_arm64 Darwin_x86_64 Linux_arm64 Linux_x86_64; do echo "${item}-fips_${version}_${label}.tar.gz"; done
    echo "${item}-fips_${version}_Windows_x86_64.zip"
  )"
  got="$(cd "${dir}" && ls -1 ./*.tar.gz ./*.zip | sed 's#^\./##')"
  expect_equal "${item}: archive names" "$(sort <<<"${expected}")" "$(sort <<<"${got}")"
  [ -s "${dir}/sbom.xml" ] && note "- ok: ${item}: sbom.xml is not empty" || fail "${item}: sbom.xml is empty"

  # every archive holds its binary, LICENSE, CHANGELOG.md, README.md and sbom.xml
  for archive in $(cd "${dir}" && ls -1 ./*.tar.gz ./*.zip | sed 's#^\./##'); do
    if [[ "${archive}" == *.tar.gz ]]; then listing="$(tar -tzf "${dir}/${archive}" | sed 's#^\./##')"; else listing="$(unzip -Z1 "${dir}/${archive}" | sed 's#^\./##')"; fi
    for entry in LICENSE CHANGELOG.md README.md sbom.xml; do
      grep -qx "${entry}" <<<"${listing}" || fail "${item}: ${archive} lacks ${entry}"
    done
    [ "$(wc -l <<<"${listing}")" -eq 5 ] || fail "${item}: ${archive} holds '${listing//$'\n'/ }', not a binary and four files"
  done

  # the release builds ./<item>/main.go, a file, so the build info path is command-line-arguments, as it is today
  tar -xzf "${dir}/${item}_${version}_Linux_x86_64.tar.gz" -C "${dir}" "./${item}_linux_x86_64"
  info="$(go version -m "${dir}/${item}_linux_x86_64")"
  expect_equal "${item}: build info path" "command-line-arguments" "$(awk '$1 == "path" {print $2}' <<<"${info}")"
  tar -xzf "${dir}/${item}-fips_${version}_Linux_x86_64.tar.gz" -C "${dir}" "./${item}-fips_linux_amd64"
  go version -m "${dir}/${item}-fips_linux_amd64" | grep -q -- '-tags=fips140,fips140v1.0' \
    && note "- ok: ${item}: the FIPS build carries the tags fips140 and fips140v1.0" \
    || fail "${item}: the FIPS archive is not a FIPS 140-3 build"
  note "- built ${item}: $(wc -l <<<"${got}") archives"
done

# flagd prints the stamped version line
expect_equal "flagd version" "flagd: v${version} (${commit}), built at: ${date}" \
  "$("${out}/flagd/flagd_linux_x86_64" version | grep '^flagd:')"

# the images: the release pushes these literal names (the workflow derives them from the registry and the owner, which
# the checks below assert are ghcr.io and the owner of upstream flagd)
workflow=.github/workflows/release-please.yaml
grep -q 'REGISTRY: ghcr.io' "${workflow}" || fail "the release registry is not ghcr.io"
prefix='${{ env.REGISTRY }}/${{ env.REPO_OWNER }}/${{ matrix.path }}'
pushed="$(grep -F -- "${prefix}:" "${workflow}" | tr -d '\r' | sed 's/^ *//')"
for suffix in ':latest' ':${{ env.VERSION }}' ':latest-fips' ':${{ env.VERSION }}-fips'; do
  grep -Fxq -- "${prefix}${suffix}" <<<"${pushed}" || fail "the release workflow does not push a tag ending in ${suffix}"
done
grep -q "github.repository == 'open-feature/flagd'" "${workflow}" || fail "the release is not gated to open-feature/flagd"
grep -q 'REPO_OWNER: ${{ github.repository_owner }}' "${workflow}" || fail "the release owner is not the repository owner"

build_image() { # <variant arg> <tag> <tag>
  local fips="$1"
  shift
  docker build -q -f flagd/build.Dockerfile --build-arg "VERSION=v${version}" --build-arg "COMMIT=${commit}" --build-arg "DATE=${date}" \
    --build-arg "FIPS=${fips}" -t "$1" -t "$2" .
}
build_image off "ghcr.io/open-feature/flagd:v${version}" "ghcr.io/open-feature/flagd:latest" >/dev/null
build_image on "ghcr.io/open-feature/flagd:v${version}-fips" "ghcr.io/open-feature/flagd:latest-fips" >/dev/null
for tag in "v${version}" latest "v${version}-fips" latest-fips; do
  image="ghcr.io/open-feature/flagd:${tag}"
  expect_equal "${image}: entrypoint" "[/flagd-build]" "$(docker image inspect "${image}" --format '{{.Config.Entrypoint}}')"
  expect_equal "${image}: user" "65532:65532" "$(docker image inspect "${image}" --format '{{.Config.User}}')"
done
expect_equal "image: flagd version" "flagd: v${version} (${commit}), built at: ${date}" \
  "$(docker run --rm "ghcr.io/open-feature/flagd:v${version}" version | grep '^flagd:')"
note "- image base: $(grep '^FROM gcr.io/distroless' flagd/build.Dockerfile | tail -1)"
note "- platforms in the release workflow: $(grep -m1 'platforms: linux' "${workflow}" | sed 's/^ *//')"

note ""
if [ "${failures}" -gt 0 ]; then note "${failures} check(s) failed."; exit 1; fi
note "Nothing was pushed or signed."
