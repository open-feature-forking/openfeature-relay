#!/usr/bin/env bash
# Builds the release archives of one item, flagd or flagd-proxy: the standard binaries and the FIPS 140-3 variant, each
# in an archive with LICENSE, CHANGELOG.md, README.md and the SBOM. The release workflow and the release dry run both
# call it, so what the dry run checks is what a release builds (UC-009).
#
# usage: release-build.sh <item> <version without v> <commit> <date> [output directory, default .]
#
# Needs cyclonedx-gomod and the Go toolchain on PATH, and runs from the root of the repository.
set -euo pipefail

item="${1:?item: flagd or flagd-proxy}"
version_no_prefix="${2:?version without the v}"
commit="${3:?commit}"
date="${4:?date}"
out="${5:-.}"

version="v${version_no_prefix}"
build_flags=(-a -ldflags "-X main.version=${version} -X main.commit=${commit} -X main.date=${date}")

make workspace-init
mkdir -p "${out}"
out="$(cd "${out}" && pwd)"

# what the archives hold besides the binary
cp "./${item}/CHANGELOG.md" "${out}/CHANGELOG.md"
cp ./LICENSE "${out}/LICENSE"
cp ./README.md "${out}/README.md"
(cd "${item}" && cyclonedx-gomod app >"${out}/sbom.xml")

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
  env CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" go build "${build_flags[@]}" -o "${out}/${bin}" "./${item}/main.go"
  archive "${item}_${version_no_prefix}_${label}" "${format}" "${bin}"
}

build darwin arm64 darwin_arm64 Darwin_arm64 tar
build darwin amd64 darwin_x86_64 Darwin_x86_64 tar
build linux arm64 linux_arm64 Linux_arm64 tar
build linux amd64 linux_x86_64 Linux_x86_64 tar
# Windows artifacts use .zip archives
build windows amd64 windows_x86_64 Windows_x86_64 zip
build windows 386 windows_i386 Windows_i386 zip

# FIPS variant. Fewer targets than the standard build: FIPS mode is unsupported on windows/386, and linux/386 is not a
# validated operating environment for the module. The CMVP-certified Go Cryptographic Module v1.0.0 (certificate #5247).
build_fips() { # <goos> <goarch> <archive label> <format>
  local goos="$1" goarch="$2" label="$3" format="$4"
  local bin="${item}-fips_${goos}_${goarch}"
  env GOFIPS140=v1.0.0 CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" \
    go build "${build_flags[@]}" -tags fips140 -o "${out}/${bin}" "./${item}/main.go"
  # The toolchain adds fips140v1.0 only when GOFIPS140 is set, so both tags together prove the binary built as the FIPS
  # variant.
  build_info=$(go version -m "${out}/${bin}")
  grep -q -- '-tags=fips140,fips140v1.0' <<<"${build_info}" \
    || { echo "::error::${bin} is not a FIPS 140-3 build"; exit 1; }
  archive "${item}-fips_${version_no_prefix}_${label}" "${format}" "${bin}"
}

build_fips darwin arm64 Darwin_arm64 tar
build_fips darwin amd64 Darwin_x86_64 tar
build_fips linux arm64 Linux_arm64 tar
build_fips linux amd64 Linux_x86_64 tar
build_fips windows amd64 Windows_x86_64 zip
