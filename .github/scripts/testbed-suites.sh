#!/usr/bin/env bash
# Runs the Gherkin suites of test/integration, TestRPC and TestInProcess, against the upstream launchpad image and the
# relay testbed, and fails when the two pass different scenarios (UC-022 Q14, Q16).
#
# usage: testbed-suites.sh [outages]
#
# With "outages", only TestRPC runs, with the @reconnect, @events and @grace scenarios included.
#
# The images are built by make testbed-launchpad-image and make testbed-relay-image. Needs Docker and the Go toolchain,
# and runs from the root of the repository. The go test output of each image is kept in $TESTBED_OUT (default: a
# temporary directory).
set -euo pipefail

mode="${1:-standard}"
out="${TESTBED_OUT:-$(mktemp -d)}"
mkdir -p "${out}"

# Scenarios switched on for both images, on top of the tags test/integration excludes (see TESTBED_ENABLE_TAGS there).
# Each is a flagd behaviour the relay testbed serves; one tag is added per change, after a run of both suites with it.
enabled_tags=()
enabled="$(IFS=,; echo "${enabled_tags[*]-}")"

run='TestRPC|TestInProcess'
env_extra=()
if [ "${mode}" = outages ]; then
  run='TestRPC'
  env_extra=(TESTBED_RPC_OUTAGES=true)
fi

for side in launchpad relay; do
  case "${side}" in
    launchpad) image=testbed-launchpad:local ;;
    relay) image=flagd-testbed:local ;;
  esac
  # a failing scenario is compared, not fatal here: the suites may fail the same scenarios on both images
  (cd test/integration && env GOWORK=off FLAGD_TESTBED_IMAGE="${image}" TESTBED_ENABLE_TAGS="${enabled}" ${env_extra[@]+"${env_extra[@]}"} go test -count=1 -json -timeout 60m -run "^(${run})\$" .) \
    >"${out}/${side}.json" || echo "::notice::the ${side} run reported failures; the pass sets decide"
done

python3 .github/scripts/compare-pass-sets.py "${out}/launchpad.json" "${out}/relay.json"
