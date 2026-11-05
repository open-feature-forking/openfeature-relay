#!/usr/bin/env bash
# Reports the known CRITICAL and HIGH vulnerabilities of the amd64 image of each layout in a directory (step 17, Q2): it
# only reports, and never fails the build. The Trivy action installs trivy; a layout it already scanned is skipped.
#
# usage: image-scan.sh <layout directory> [<layout file to skip>]
set -uo pipefail

dir="${1:?layout directory}"
skip="${2:-}"
command -v trivy >/dev/null || { echo "::warning::trivy is not on PATH: the other layouts were not scanned"; exit 0; }
for layout in "${dir}"/*.tar; do
  [ "$(basename "${layout}")" != "${skip}" ] || continue
  echo "::group::trivy $(basename "${layout}")"
  TRIVY_DB_REPOSITORY="${TRIVY_DB_REPOSITORY:-public.ecr.aws/aquasecurity/trivy-db:2,ghcr.io/aquasecurity/trivy-db:2}" \
    trivy image --platform linux/amd64 --input "${layout}" --severity CRITICAL,HIGH --exit-code 0 || true
  echo "::endgroup::"
done
exit 0
