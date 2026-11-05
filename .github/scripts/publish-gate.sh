#!/usr/bin/env bash
# Decides whether a run builds and publishes the images (step 17, Q6 (b)) and prints `build=<bool>` and `publish=<bool>`.
#
#   build:   the image-build parts, the testbed layout and the layout artifacts run: a workflow_dispatch, FULL_CI, or a publish.
#   publish: the layouts are pushed. Only on the main branch of open-feature-forking/openfeature-relay: a workflow_dispatch at
#            once, or a push that changed image inputs while :main is missing or older than MAX_AGE seconds (default 3 h).
#
# Inputs, all environment variables: EVENT, REF, REPOSITORY, FULL_CI, INPUTS_CHANGED (true or false), MAIN_CREATED (the
# org.opencontainers.image.created of :main, empty when there is none), NOW (epoch seconds, default: now), MAX_AGE.
set -euo pipefail

repository=open-feature-forking/openfeature-relay
now="${NOW:-$(date +%s)}"
max_age="${MAX_AGE:-10800}"

build=false
publish=false
on_main=false
[ "${REF:-}" = refs/heads/main ] && [ "${REPOSITORY:-}" = "${repository}" ] && on_main=true

if [ "${EVENT:-}" = workflow_dispatch ]; then
  build=true
  [ "${on_main}" = true ] && publish=true
elif [ "${EVENT:-}" = push ] && [ "${on_main}" = true ] && [ "${INPUTS_CHANGED:-false}" = true ]; then
  created=""
  [ -z "${MAIN_CREATED:-}" ] || created="$(date -d "${MAIN_CREATED}" +%s 2>/dev/null || true)"
  if [ -z "${created}" ] || [ $((now - created)) -gt "${max_age}" ]; then
    publish=true
  else
    echo "::notice:::main is $(((now - created) / 60)) min old (less than $((max_age / 60))): not publishing." >&2
  fi
fi
[ "${FULL_CI:-}" != true ] || build=true
[ "${publish}" != true ] || build=true

echo "build=${build}"
echo "publish=${publish}"
