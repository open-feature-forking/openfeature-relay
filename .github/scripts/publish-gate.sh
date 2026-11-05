#!/usr/bin/env bash
# Decides whether a run builds and publishes the images (step 17, Q6 as the project lead changed it: from the nightly run) and
# prints `build=<bool>` and `publish=<bool>`.
#
#   build:   the image-build parts, the testbed layout and the layout artifacts run: a workflow_dispatch, FULL_CI, or a publish.
#   publish: the layouts are pushed. Only on the main branch of open-feature-forking/openfeature-relay: a workflow_dispatch
#            at once, or the nightly schedule when image inputs changed since the commit :main was built from.
#
# Inputs, all environment variables: EVENT, REF, REPOSITORY, FULL_CI, INPUTS_CHANGED (true or false; for the schedule, against
# the commit :main was built from, true when there is none).
set -euo pipefail

repository=open-feature-forking/openfeature-relay
build=false
publish=false
on_main=false
[ "${REF:-}" = refs/heads/main ] && [ "${REPOSITORY:-}" = "${repository}" ] && on_main=true

if [ "${EVENT:-}" = workflow_dispatch ]; then
  build=true
  [ "${on_main}" = true ] && publish=true
elif [ "${EVENT:-}" = schedule ] && [ "${on_main}" = true ] && [ "${INPUTS_CHANGED:-false}" = true ]; then
  publish=true
fi
[ "${FULL_CI:-}" != true ] || build=true
[ "${publish}" != true ] || build=true

echo "build=${build}"
echo "publish=${publish}"
