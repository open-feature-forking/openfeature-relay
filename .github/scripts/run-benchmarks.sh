#!/usr/bin/env bash
# Runs the library benchmarks of the checkout in <dir> and writes the output to <out>.
#
#   run-benchmarks.sh <dir> <out>
#
# The benchmarks run on the library modules the Makefile of <dir> names (GO_LIB_MODULE_DIRS) and on the packages of
# the flagd and flagd-proxy modules, whose service endpoints have benchmarks of their own (NFR-001, NFR-013). Each runs
# a fixed number of iterations, several times, so that the run stays short and the spread of a benchmark shows;
# compare-allocs.py reads allocs/op, which does not depend on the machine, and ignores ns/op. The tests themselves
# are skipped.
set -euo pipefail

dir=${1:?usage: run-benchmarks.sh <dir> <out>}
out=$(realpath -m "${2:?usage: run-benchmarks.sh <dir> <out>}")
benchtime=${BENCHTIME:-100x}
count=${BENCHCOUNT:-3}

cd "$dir"
# a worktree would otherwise find the go.work of the main checkout
export GOWORK=$PWD/go.work
make workspace-init >/dev/null

# GO_LIB_MODULE_DIRS comes from the checkout's own Makefile, so a base commit is measured over its own modules.
libs=$(printf 'include Makefile\nprint-libs:\n\t@echo $(GO_LIB_MODULE_DIRS)\n' | make -s -f - print-libs)
patterns=()
for lib in $libs ./flagd ./flagd-proxy; do
  if [ -d "$lib" ]; then patterns+=("$lib/..."); fi
done

go test -run '^$' -bench=Bench -short -benchtime="$benchtime" -count="$count" -benchmem "${patterns[@]}" | tee "$out"
