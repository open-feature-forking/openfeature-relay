#!/usr/bin/env bash
# Renders the published manifest of a standard distribution (UC-012 BR-004, Q10): every component module at the one
# placeholder version v0.0.0-dryrun and no replaces. A release pins the versions it has just tagged; the dry run tags
# nothing, so it checks the shape. Every module the manifest names must be a component module of this repository: its
# path is the repository path followed by a directory that holds a go.mod declaring it. A module that is not fails, named
# (A2).
#
# usage: render-published-manifest.sh <manifest> <output file>
set -euo pipefail

manifest="${1:?manifest}"
output="${2:?output file}"
repository=github.com/open-feature-forking/openfeature-relay
placeholder=v0.0.0-dryrun

# what a release never tags: tests, the distributions' own modules and the binaries that keep the upstream module paths
not_components='^(test|test-harness|spec|schemas|playground-app|distributions|flagd|flagd-proxy|docs|samples|images)(/|$)'

# the modules of the build list: the components, the runtime, the telemetry provider and the cmd entry
modules="$(awk '/^replaces:/ {skip = 1; next} /^[a-z_]+:/ {skip = 0} !skip' "${manifest}" \
  | grep -oE '\{module: [^,}]+' | sed 's/^{module: //')"

failed=0
for module in ${modules}; do
  dir="${module#"${repository}"/}"
  if [ "${dir}" = "${module}" ] || [[ "${dir}" =~ ${not_components} ]] \
    || ! awk -v want="module ${module}" '{sub(/$/, "")} $0 == want {found = 1} END {exit !found}' "${dir}/go.mod" 2>/dev/null; then
    echo "::error::${manifest} names ${module}, which is not a component module of this repository"
    failed=1
  fi
done
[ "${failed}" -eq 0 ] || exit 1

# drop the replaces block and pin the placeholder; the distribution's own version line is not in braces and stays
awk '/^replaces:/ {skip = 1; next} /^[a-z_]+:/ {skip = 0} !skip' "${manifest}" \
  | sed -E "s/(\{module: [^,}]+, version: )[^,}]+/\1${placeholder}/" >"${output}"
