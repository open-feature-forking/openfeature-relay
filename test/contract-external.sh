#!/usr/bin/env bash
# Runs the contract tests of the example modules the way an Extension Author outside this repository does (UC-008, C-001):
# from a copy of the module in a temporary directory, without the workspace, the test-harness submodule or any
# `replace`, with the core module (and so the contract runners) fetched by version. A runner that reaches for a file of
# the checkout, or an example that needs a module this repository does not publish, fails here and nowhere else.
#
# The modules are every example module under test/ whose module graph holds core and no other module of this repository, under
# the relay path or the path the flagd modules still carry. A module that needs another one, such as the engine example
# (it imports the configuration format example), cannot run outside the checkout and is not in the list. The list
# in testdata/contract-external.txt must name exactly those: the run fails when a listed module stops qualifying and
# when a qualifying one is missing from the file.
#
# The core module is fetched by pseudo-version at a commit a branch of the remote serves: CONTRACT_EXTERNAL_COMMIT when it
# is set (CI passes a pull request's head there, because the merge commit actions/checkout builds is on no branch), else
# HEAD when a remote branch contains it. For any other commit (a local one), core comes from a `git archive` copy of the
# checkout in a second temporary directory, which holds no submodules. CONTRACT_EXTERNAL_CORE names such a copy of core/
# directly (for a checkout whose git cannot run where the tests do). The output names the core version, and every module
# is checked to resolve exactly that one: a core that cannot be fetched fails the run instead of resolving to another.
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

relay=github.com/open-feature-forking/openfeature-relay
legacy=github.com/open-feature/flagd
marker=testdata/contract-external.txt

echo "== modules that qualify"
qualifying=()
while read -r gomod; do
	dir=$(dirname "$gomod")
	self=$(sed -n 's/^module[[:space:]]\{1,\}//p' "$gomod" | tr -d '\r')
	# The graph is listed outside the workspace, so its modules may not be in the module cache yet (a fresh CI runner filled
	# it in workspace mode): lookup stays allowed, and -mod=readonly keeps the checkout's go.mod and go.sum untouched.
	graph=$(cd "$dir" && GOWORK=off GOFLAGS=-mod=readonly go list -m all) || { echo "cannot list the module graph of $dir" >&2; exit 1; }
	paths=$(echo "$graph" | awk '{print $1}')
	# a module of this repository is one under the relay path or under the path its flagd modules still carry
	others=$(echo "$paths" | grep -E "^($relay|$legacy)/" | grep -vxE "$relay/core|$self" || true)
	if [ -n "$(echo "$paths" | grep -x "$relay/core" || true)" ] && [ -z "$others" ]; then
		qualifying+=("$dir")
	fi
done < <(find test -name go.mod -path '*-example/*' | sort)
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
want=""
if [ -n "${CONTRACT_EXTERNAL_CORE:-}" ]; then
	core_replace=$CONTRACT_EXTERNAL_CORE
	echo "== core from $core_replace (CONTRACT_EXTERNAL_CORE)"
else
	sha=${CONTRACT_EXTERNAL_COMMIT:-$(git rev-parse HEAD)}
	# refs/remotes/pull/* are the refs actions/checkout fetches for a pull request; no branch serves their commits
	on_branch=$(git for-each-ref --contains "$sha" --format='%(refname)' refs/remotes/ 2>/dev/null | grep -v '^refs/remotes/pull/' || true)
	if [ -n "${CONTRACT_EXTERNAL_COMMIT:-}" ] || [ -n "$on_branch" ]; then
		probe="$work/probe"
		mkdir -p "$probe"
		want=$(cd "$probe" && GOWORK=off GOFLAGS=-mod=mod go mod init probe >/dev/null 2>&1 &&
			GOWORK=off GOPROXY=direct GONOSUMDB="github.com/open-feature-forking/*" go list -m -f '{{.Version}}' "$relay/core@$sha") ||
			{ echo "core cannot be fetched at commit $sha" >&2; exit 1; }
		case "$want" in
		*-"${sha:0:12}") ;;
		*)
			echo "core at commit $sha resolved to $want, which is not that commit" >&2
			exit 1
			;;
		esac
		echo "== core $want: commit $sha, fetched by pseudo-version"
	else
		echo "== commit ${sha:0:12} is on no branch of the remote: core comes from a git archive copy of the checkout"
		mkdir -p "$work/archive"
		git archive "$sha" core | tar -x -C "$work/archive"
		core_replace="$work/archive/core"
	fi
fi

# check_core fails unless the module in the current directory resolves core to the one chosen above.
check_core() {
	local got
	if [ -n "$core_replace" ]; then
		got=$(go list -m -f '{{with .Replace}}{{.Path}}{{end}}' "$relay/core") || return 1
		[ "$got" = "$core_replace" ] || { echo "core resolves to ${got:-no replacement}, not $core_replace" >&2; return 1; }
	else
		got=$(go list -m -f '{{.Version}}' "$relay/core") || return 1
		[ "$got" = "$want" ] || { echo "core resolves to $got, not $want" >&2; return 1; }
	fi
	echo "core: $(go list -m "$relay/core")"
}

status=0
for dir in $derived; do
	echo "== $dir"
	copy="$work/$(basename "$dir")"
	cp -r "$dir" "$copy"
	# Each command ends the module's run on failure itself: set -e does not apply inside a subshell tested with ||.
	(
		cd "$copy" || exit 1
		export GOWORK=off GOFLAGS=-mod=mod
		# drop every replace directive: single lines and blocks
		awk '
			/^replace[ \t]*\(/ { skip = 1; next }
			skip && /^\)/ { skip = 0; next }
			skip { next }
			/^replace[ \t]/ { next }
			{ print }
		' go.mod >go.mod.new && mv go.mod.new go.mod || exit 1
		if [ -n "$core_replace" ]; then
			go mod edit -replace "$relay/core=$core_replace" || exit 1
			go mod tidy || exit 1
		else
			# the requirement names a core version no tag publishes; only the replace made it resolvable
			go mod edit -droprequire "$relay/core" || exit 1
			GOPROXY=direct GONOSUMDB="github.com/open-feature-forking/*" go get "$relay/core@$want" || exit 1
		fi
		check_core || exit 1
		go test -race -count=2 ./... || exit 1
		check_core >/dev/null || exit 1
	) || status=1
done
exit $status
