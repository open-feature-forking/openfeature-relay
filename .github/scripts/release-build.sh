#!/usr/bin/env bash
# Builds the release archives of one item, flagd or flagd-proxy, each in an archive with LICENSE, CHANGELOG.md, README.md and the SBOM. The release workflow and the release dry run both
# call it, so what the dry run checks is what a release builds (UC-009).
#
# usage: [RELEASE_PLATFORMS="linux/amd64 ..."] \
#          release-build.sh <item> <version without v> <commit> <date> [output directory, default .] [source directory]
#
# The source directory is the generated source of a standard distribution (UC-012): it is built as ./ of that directory
# with GOWORK=off, and the changelog stays flagd's. Without it, ./<item>/main.go is built as always. RELEASE_PLATFORMS
# narrows the targets to the listed <goos>/<goarch> pairs; it defaults to everything.
#
# Needs cyclonedx-gomod and the Go toolchain on PATH, and runs from the root of the repository.
set -euo pipefail

item="${1:?item: flagd or flagd-proxy}"
version_no_prefix="${2:?version without the v}"
commit="${3:?commit}"
date="${4:?date}"
out="${5:-.}"
source="${6:-}"
platforms="${RELEASE_PLATFORMS:-}"

version="v${version_no_prefix}"
build_flags=(-a -ldflags "-X main.version=${version} -X main.commit=${commit} -X main.date=${date}")

make workspace-init
mkdir -p "${out}"
out="$(cd "${out}" && pwd)"

# what the archives hold besides the binary
if [ -n "${source}" ]; then
  source="$(cd "${source}" && pwd)"
  cp ./flagd/CHANGELOG.md "${out}/CHANGELOG.md"
else
  cp "./${item}/CHANGELOG.md" "${out}/CHANGELOG.md"
fi
cp ./LICENSE "${out}/LICENSE"
cp ./README.md "${out}/README.md"
if [ -n "${source}" ]; then
  (cd "${source}" && GOWORK=off cyclonedx-gomod app >"${out}/sbom.xml")
else
  (cd "${item}" && cyclonedx-gomod app >"${out}/sbom.xml")
fi

# gobuild <binary path> <go build arguments...>: a distribution's source is a module of its own, built outside the workspace
gobuild() {
  local bin="$1"
  shift
  if [ -n "${source}" ]; then
    (cd "${source}" && GOWORK=off go build "$@" -o "${bin}" .)
  else
    go build "$@" -o "${bin}" "./${item}/main.go"
  fi
}
selected() { # <goos> <goarch>: whether RELEASE_PLATFORMS names the target
  [ -z "${platforms}" ] || [[ " ${platforms} " == *" $1/$2 "* ]]
}

archive() { # <archive name without extension> <format> <binary>
  local name="$1" format="$2" bin="$3"
  (
    cd "${out}"
    if [ "${format}" = tar ]; then
      tar -cvzf "${name}.tar.gz" "./${bin}" ./LICENSE ./CHANGELOG.md ./README.md ./sbom.xml
    else
      zip -r "${name}.zip" "./${bin}" ./LICENSE ./CHANGELOG.md ./README.md ./sbom.xml
    fi
  )
}

build() { # <goos> <goarch> <binary suffix> <archive label> <format>
  local goos="$1" goarch="$2" suffix="$3" label="$4" format="$5"
  local bin="${item}_${suffix}"
  selected "${goos}" "${goarch}" || return 0
  CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" gobuild "${out}/${bin}" "${build_flags[@]}"
  archive "${item}_${version_no_prefix}_${label}" "${format}" "${bin}"
}

build darwin arm64 darwin_arm64 Darwin_arm64 tar
build darwin amd64 darwin_x86_64 Darwin_x86_64 tar
build linux arm64 linux_arm64 Linux_arm64 tar
build linux amd64 linux_x86_64 Linux_x86_64 tar
# Windows artifacts use .zip archives
build windows amd64 windows_x86_64 Windows_x86_64 zip
build windows 386 windows_i386 Windows_i386 zip
