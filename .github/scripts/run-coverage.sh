#!/usr/bin/env bash
# Runs the unit tests of the checkout in <dir> with the same flags as `make test-module` and keeps one coverage profile
# per module in <outdir>.
#
#   run-coverage.sh <dir> <outdir>
#
# The modules come from COVERAGE_MODULES in the Makefile of <dir>; a checkout from before that variable existed is
# measured over the modules that had profiles then. compare-coverage.py reads packages from the profiles, so the file
# names do not matter. A module whose tests fail still leaves a profile: the run only warns, because the comparison
# is about coverage, and the test job decides whether the tests pass.
set -uo pipefail

dir=${1:?usage: run-coverage.sh <dir> <outdir>}
out=$(realpath -m "${2:?usage: run-coverage.sh <dir> <outdir>}")
mkdir -p "$out"

cd "$dir"
# a worktree would otherwise find the go.work of the main checkout
export GOWORK=$PWD/go.work
make workspace-init >/dev/null

modules=$(printf 'include Makefile\nprint-modules:\n\t@echo $(or $(COVERAGE_MODULES),$(patsubst ./%%,%%,$(GO_LIB_MODULE_DIRS) $(FLAGD_MODULE_DIRS) $(CONTRIB_MODULE_DIRS) $(TESTBED_MODULE_DIRS)) flagd-proxy)\n' | make -s -f - print-modules)

for m in $modules; do
  echo "== $m"
  go test -race -covermode=atomic -short -count=1 "./$m/..." -coverprofile="$out/${m//\//-}.out" || echo "::warning::the tests of $m failed in $dir"
done
