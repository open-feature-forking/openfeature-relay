#!/usr/bin/env bash
# Runs the library benchmarks of the checkout in <dir> and writes the output to <out>.
#
#   run-benchmarks.sh <dir> <out>
#
# The benchmarks run on the library modules the Makefile of <dir> names (GO_LIB_MODULE_DIRS) and on the packages of
# the flagd and flagd-proxy modules, whose service endpoints have benchmarks of their own (NFR-001, NFR-013), and on
# the Flagsmith engine, an evaluator of the evaluation path that is not a library module. Each runs
# a fixed number of iterations, several times, so that the spread of a benchmark shows. 1000 iterations amortize the
# one-time allocations of a run to under one per operation (at 100 the benchmarks of 200 to 400 allocs/op moved by up
# to 6). SetState, which allocates 368,000 times per operation and takes 70 s for 1000 iterations, runs 100; a run of
# the whole set takes about 1.5 minutes. See NOISY in compare-allocs.py;
# compare-allocs.py reads allocs/op, which does not depend on the machine, and ignores ns/op. The tests themselves
# are skipped.
set -euo pipefail

dir=${1:?usage: run-benchmarks.sh <dir> <out>}
out=$(realpath -m "${2:?usage: run-benchmarks.sh <dir> <out>}")
benchtime=${BENCHTIME:-1000x}
setstatetime=${SETSTATETIME:-100x}
count=${BENCHCOUNT:-3}

cd "$dir"
# a worktree would otherwise find the go.work of the main checkout
export GOWORK=$PWD/go.work
make workspace-init >/dev/null

# GO_LIB_MODULE_DIRS and FLAGD_MODULE_DIRS come from the checkout's own Makefile, so a base commit is measured over its own modules.
libs=$(printf 'include Makefile\nprint-libs:\n\t@echo $(GO_LIB_MODULE_DIRS) $(FLAGD_MODULE_DIRS)\n' | make -s -f - print-libs)
patterns=()
declare -A seen
# a base without FLAGD_MODULE_DIRS has the flagd module alone, so flagd is named here as well
for lib in $libs ./flagd ./flagd-proxy ./contrib/flagsmith/engine; do
  if [ -d "$lib" ] && [ -z "${seen[$lib]:-}" ]; then seen[$lib]=1; patterns+=("$lib/..."); fi
done

go test -run '^$' -bench=Bench -skip '^BenchmarkSetState$' -short -benchtime="$benchtime" -count="$count" -benchmem "${patterns[@]}" | tee "$out"
go test -run '^$' -bench='^BenchmarkSetState$' -short -benchtime="$setstatetime" -count="$count" -benchmem "${patterns[@]}" | tee -a "$out"
