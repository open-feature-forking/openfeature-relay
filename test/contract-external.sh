#!/usr/bin/env bash
# Runs the contract tests of the example modules the way an Extension Author outside this repository does (UC-008, C-001):
# from a copy of the module in a temporary directory, without the workspace, the test-harness submodule or any
# `replace`, with the core module (and so the contract runners) fetched by version. A runner that reaches for a file of
# the checkout, or an example that needs a module this repository does not publish, fails here and nowhere else.
#
# The modules are every test/*-example module whose module graph holds no module of this repository but core. The list
# in testdata/contract-external.txt must name exactly those: the run fails when a listed module stops qualifying and
# when a qualifying one is missing from the file.
#
# The core module is fetched at the commit under test, by pseudo-version, when the commit is on the remote. For a commit
# that is not (a local one), core comes from a `git archive` copy of the checkout in a second temporary directory, which
# holds no submodules; the output says so. CONTRACT_EXTERNAL_CORE names such a copy of core/ directly (for a checkout whose
# git cannot run where the tests do).
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

relay=github.com/open-feature-forking/openfeature-relay
marker=testdata/contract-external.txt

echo "== modules that qualify"
qualifying=()
for gomod in test/*-example/go.mod; do
	dir=$(dirname "$gomod")
	graph=$(cd "$dir" && GOWORK=off GOPROXY=off go list -m all) || { echo "cannot list the module graph of $dir" >&2; exit 1; }
	others=$(echo "$graph" | awk '{print $1}' | grep -E "^$relay/" | grep -vxE "$relay/core" || true)
	if [ -z "$others" ]; then
		qualifying+=("$dir")
	fi
done
derived=$(printf '%s\n' "${qualifying[@]}" | sort)
listed=$(grep -vE '^\s*(#|$)' "$marker" | tr -d '\r' | sort)
if [ "$derived" != "$listed" ]; then
	echo "the modules that qualify differ from $marker:" >&2
	diff <(echo "$listed") <(echo "$derived") >&2 || true
	echo "(< listed, > qualifies: a listed module that no longer qualifies has gained a dependency on a module of this repository other than core)" >&2
	exit 1
fi
echo "$derived"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

core_replace=""
if [ -n "${CONTRACT_EXTERNAL_CORE:-}" ]; then
	echo "== core from $CONTRACT_EXTERNAL_CORE (CONTRACT_EXTERNAL_CORE)"
	core_replace=$CONTRACT_EXTERNAL_CORE
elif sha=$(git rev-parse HEAD) && [ -n "$(git branch -r --contains "$sha" 2>/dev/null)" ]; then
	echo "== core at commit ${sha:0:12}, fetched by pseudo-version"
else
	echo "== commit ${sha:0:12} is not on the remote: core comes from a git archive copy of the checkout"
	mkdir -p "$work/archive"
	git archive "$sha" core | tar -x -C "$work/archive"
	core_replace="$work/archive/core"
fi

status=0
for dir in $derived; do
	echo "== $dir"
	copy="$work/$(basename "$dir")"
	cp -r "$dir" "$copy"
	(
		cd "$copy"
		export GOWORK=off GOFLAGS=-mod=mod
		# drop every replace directive: single lines and blocks
		awk '
			/^replace[ \t]*\(/ { skip = 1; next }
			skip && /^\)/ { skip = 0; next }
			skip { next }
			/^replace[ \t]/ { next }
			{ print }
		' go.mod >go.mod.new && mv go.mod.new go.mod
		if [ -n "$core_replace" ]; then
			go mod edit -replace "$relay/core=$core_replace"
			go mod tidy
		else
			GOPROXY=direct GONOSUMDB="github.com/open-feature-forking/*" go get "$relay/core@$sha"
		fi
		go test -race -count=2 ./...
	) || status=1
done
exit $status
