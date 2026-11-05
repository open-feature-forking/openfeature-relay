#!/usr/bin/env bash
# Runs the build steps of a release without publishing or signing anything, and checks what a release would produce
# against what upstream flagd's release produces (UC-009 BR-007): the archives of flagd and flagd-proxy by name and
# content, the version line and the build info path, and the image of flagd with its tags, entrypoint and
# user, and the standard distributions (release-dry-run-distributions.sh). The names are literal, not derived from the repository this runs in, which would render other names here.
#
# usage: [DRY_RUN_ITEMS="flagd"] [DRY_RUN_PART=all] [DRY_RUN_SCOPE=full] [DRY_RUN_DISTS="flagd-sync ..."] #          release-dry-run.sh [version without v] [commit] [date]
#
# DRY_RUN_PART runs one part of the dry run, so CI can run the parts as parallel jobs: <item>-standard for
# the archives of flagd and flagd-proxy, images, distributions, or all (the default, every part in order). DRY_RUN_SCOPE=reduced
# builds the linux/amd64 archives only and, in the distributions part, only the distributions named in DRY_RUN_DISTS (default:
# every one); the images are built in full either way. full is what a release builds.
#
# Needs Docker, cyclonedx-gomod and the Go toolchain on PATH, and runs from the root of the repository.
set -euo pipefail

version="${1:-9.9.9}"
commit="${2:-$(git rev-parse HEAD)}"
date="${3:-2026-01-01T00:00:00Z}"
out="$(mktemp -d)"
summary="${GITHUB_STEP_SUMMARY:-/dev/null}"
part="${DRY_RUN_PART:-all}"
scope="${DRY_RUN_SCOPE:-full}"
platforms=""
[ "${scope}" != reduced ] || platforms="linux/amd64"
want() { [ "${part}" = all ] || [ "${part}" = "$1" ]; } # <part>

failures=0
note() { echo "$*" | tee -a "${summary}"; }
fail() { echo "::error::$*"; echo "- FAILED: $*" >>"${summary}"; failures=$((failures + 1)); }
expect_equal() { # <what> <expected> <got>
  if [ "$2" = "$3" ]; then note "- ok: $1"; else fail "$1: expected '$2', got '$3'"; fi
}

# the size of each linux_x86_64 binary in MiB, for the step summary
sizes_file="${out}/sizes.md"
add_size() { # <name> <binary>
  printf '| %s | %.1f |\n' "$1" "$(awk -v b="$(stat -c %s "$2")" "BEGIN { print b / 1048576 }")" >>"${sizes_file}"
}

note "### Release dry run: ${part} (version v${version}, commit ${commit})"
[ "${scope}" != reduced ] || note "Reduced: linux/amd64 archives and the distributions this change touches; the full set runs nightly."
note ""

for item in ${DRY_RUN_ITEMS:-flagd flagd-proxy}; do
  want "${item}-standard" || continue
  dir="${out}/${item}"
  RELEASE_PLATFORMS="${platforms}" .github/scripts/release-build.sh "${item}" "${version}" "${commit}" "${date}" "${dir}"

  expected="$(
    if [ -n "${platforms}" ]; then
      echo "${item}_${version}_Linux_x86_64.tar.gz"
    else
      for label in Darwin_arm64 Darwin_x86_64 Linux_arm64 Linux_x86_64; do echo "${item}_${version}_${label}.tar.gz"; done
      for label in Windows_x86_64 Windows_i386; do echo "${item}_${version}_${label}.zip"; done
    fi
  )"
  got="$(cd "${dir}" && find . -maxdepth 1 \( -name '*.tar.gz' -o -name '*.zip' \) -printf '%f\n')"
  expect_equal "${item}: archive names" "$(sort <<<"${expected}")" "$(sort <<<"${got}")"
  [ -s "${dir}/sbom.xml" ] && note "- ok: ${item}: sbom.xml is not empty" || fail "${item}: sbom.xml is empty"

  # every archive holds its binary, LICENSE, CHANGELOG.md, README.md and sbom.xml
  for archive in ${got}; do
    if [[ "${archive}" == *.tar.gz ]]; then listing="$(tar -tzf "${dir}/${archive}" | sed 's#^\./##')"; else listing="$(unzip -Z1 "${dir}/${archive}" | sed 's#^\./##')"; fi
    for entry in LICENSE CHANGELOG.md README.md sbom.xml; do
      grep -qx "${entry}" <<<"${listing}" || fail "${item}: ${archive} lacks ${entry}"
    done
    [ "$(wc -l <<<"${listing}")" -eq 5 ] || fail "${item}: ${archive} holds '${listing//$'
'/ }', not a binary and four files"
  done

  # the release builds ./<item>/main.go, a file, so the build info path is command-line-arguments, as it is today
  tar -xzf "${dir}/${item}_${version}_Linux_x86_64.tar.gz" -C "${dir}" "./${item}_linux_x86_64"
  add_size "${item}" "${dir}/${item}_linux_x86_64"
  info="$(go version -m "${dir}/${item}_linux_x86_64")"
  expect_equal "${item}: build info path" "command-line-arguments" "$(awk '$1 == "path" {print $2}' <<<"${info}")"
  # flagd prints the stamped version line
  if [ "${item}" = flagd ]; then
    expect_equal "flagd version" "flagd: v${version} (${commit}), built at: ${date}"         "$("${dir}/flagd_linux_x86_64" version | grep '^flagd:')"
  fi
  note "- built ${item}: $(wc -l <<<"${got}") archives"
done

if want images; then
# the images: the release pushes these literal names (the workflow derives them from the registry and the owner, which
# the checks below assert are ghcr.io and the owner of upstream flagd)
workflow=.github/workflows/release-please.yaml
grep -q 'REGISTRY: ghcr.io' "${workflow}" || fail "the release registry is not ghcr.io"
prefix='${{ env.REGISTRY }}/${{ env.REPO_OWNER }}/${{ matrix.path }}'
pushed="$(grep -F -- "${prefix}:" "${workflow}" | tr -d '\r' | sed 's/^ *//')"
for suffix in ':latest' ':${{ env.VERSION }}'; do
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
for tag in "v${version}" latest; do
  image="ghcr.io/open-feature/flagd:${tag}"
  expect_equal "${image}: entrypoint" "[/flagd-build]" "$(docker image inspect "${image}" --format '{{.Config.Entrypoint}}')"
  expect_equal "${image}: user" "65532:65532" "$(docker image inspect "${image}" --format '{{.Config.User}}')"
done
# the image configuration, reduced to the fields an operator observes, equals the recorded one (K1); the image has no shell
golden="$(cat .github/scripts/testdata/image-config.golden)"
image_config() { # <image>
  for field in Entrypoint Cmd User WorkingDir ExposedPorts Labels Env; do
    echo "${field}: $(docker image inspect "$1" --format "{{json .Config.${field}}}")"
  done
}
for tag in "v${version}"; do
  image="ghcr.io/open-feature/flagd:${tag}"
  expect_equal "${image}: image configuration" "${golden}" "$(image_config "${image}")"
  if docker run --rm --entrypoint /bin/sh "${image}" -c true >/dev/null 2>&1; then fail "${image} holds a shell"; else note "- ok: ${image}: no /bin/sh"; fi
done
# both platforms build into one OCI image
# (the default driver cannot export OCI, so a throwaway container builder does)
oci="${out}/flagd-oci.tar"
builder="dry-run-$$"
docker buildx create --driver docker-container --name "${builder}" >/dev/null
trap 'docker buildx rm "${builder}" >/dev/null 2>&1 || true' EXIT
docker buildx build --builder "${builder}" -q -f flagd/build.Dockerfile --platform linux/amd64,linux/arm64 --build-arg "VERSION=v${version}"   --build-arg "COMMIT=${commit}" --build-arg "DATE=${date}" --output "type=oci,dest=${oci}" . >/dev/null
platforms="$(python3 - "${oci}" <<'PY'
import json, sys, tarfile
tar = tarfile.open(sys.argv[1])
def blob(digest):
    return json.load(tar.extractfile("blobs/sha256/" + digest.split(":", 1)[1]))
manifests = json.load(tar.extractfile("index.json"))["manifests"]
if len(manifests) == 1 and "manifests" in blob(manifests[0]["digest"]):
    manifests = blob(manifests[0]["digest"])["manifests"]
print(" ".join(sorted(m["platform"]["os"] + "/" + m["platform"]["architecture"] for m in manifests if m.get("platform", {}).get("os", "unknown") != "unknown")))
PY
)"
expect_equal "image: platforms of the OCI build" "linux/amd64 linux/arm64" "${platforms}"
expect_equal "image: flagd version" "flagd: v${version} (${commit}), built at: ${date}" \
  "$(docker run --rm "ghcr.io/open-feature/flagd:v${version}" version | grep '^flagd:')"
note "- image base: $(grep '^FROM gcr.io/distroless' flagd/build.Dockerfile | tail -1)"
note "- platforms in the release workflow: $(grep -m1 'platforms: linux' "${workflow}" | sed 's/^ *//')"
fi

# the standard distributions (UC-012); DRY_RUN_DISTRIBUTIONS=off skips them
if want distributions && [ "${DRY_RUN_DISTRIBUTIONS:-on}" != off ]; then
  DRY_RUN_SCOPE="${scope}" .github/scripts/release-dry-run-distributions.sh "${version}" "${commit}" "${date}" "${out}" || fail "the standard distributions"
fi

if [ -s "${sizes_file}" ]; then
  note ""
  note "#### Binary sizes (linux_x86_64)"
  note ""
  note "| Binary | MiB |"
  note "|---|---|"
  tee -a "${summary}" <"${sizes_file}"
fi
if [ "${failures}" -gt 0 ]; then note "${failures} check(s) failed."; exit 1; fi
note "Nothing was pushed or signed."
