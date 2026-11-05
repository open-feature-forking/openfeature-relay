#!/usr/bin/env python3
"""Which Go modules does a change affect, and which test failures did that set miss (step 15, slices P1 and P2).

Standard library only. The module set is the Makefile's ALL_GO_MOD_DIRS; ci-modules.json says how each module is
tested. `changes` prints the matrix of the modules a pull request or push can break; the `test-module` job runs it.
"""
import argparse
import fnmatch
import glob
import json
import os
import pathlib
import re
import subprocess
import sys

RELAY = "github.com/open-feature-forking/openfeature-relay/"
LEGACY = "github.com/open-feature/flagd/"
ZERO = "0" * 40

# Readers of a module that do not import it: a change to the key also affects the values.
EXTRA_EDGES = {
    "builder": ["test/distributions", "flagd"],
    "test/inprocess-example/filegrpc": ["test/inprocess-example/file"],
    "test/distributions/example/cmd": ["test/distributions"],
    "test/distributions/sync-vendor/cmd": ["test/distributions"],
}
MANIFEST_READER = "test/distributions"
MANIFEST_GLOBS = ["distributions/*/manifest.yaml", "test/distributions/**/manifest.yaml"]
SUBMODULE = "test-harness"


class TableError(Exception):
    """A module directory without an entry in ci-modules.json."""


def parse_prune(makefile_text):
    line = next(l for l in makefile_text.splitlines() if l.startswith("ALL_GO_MOD_DIRS"))
    return [p.strip('"') for p in re.findall(r'-path\s+("[^"]+"|\S+)', line)]


def module_dirs(files, prune):
    """The directories of `files` (repository-relative paths) that hold a go.mod and are not pruned."""
    dirs = set()
    for f in files:
        if f == "go.mod" or not f.endswith("/go.mod"):
            continue
        d = f[: -len("/go.mod")]
        parts = d.split("/")
        ancestors = ["./" + "/".join(parts[: i + 1]) for i in range(len(parts))]
        if not any(fnmatch.fnmatch(a, p) for a in ancestors for p in prune):
            dirs.add(d)
    return dirs


def list_files(root, rev=None):
    if rev:
        out = subprocess.run(["git", "ls-tree", "-r", "--name-only", "-z", rev], cwd=root, capture_output=True, check=True).stdout
        return [p.decode() for p in out.split(b"\0") if p]
    found = []
    for top, dirs, files in os.walk(root):
        dirs[:] = [d for d in dirs if d != ".git"]
        base = pathlib.Path(top).relative_to(root).as_posix()
        found += [f if base == "." else base + "/" + f for f in files]
    return found


def parse_gomod(text):
    """The module path and every path of the require directives (indirect ones included)."""
    path, requires, block = None, set(), None
    for raw in text.splitlines():
        line = raw.split("//", 1)[0].strip()
        if not line:
            continue
        if block:
            if line == ")":
                block = None
            elif block == "require":
                requires.add(line.split()[0])
            continue
        m = re.match(r"(module|require|replace|exclude|retract|tool|godebug|ignore)\s*(\(|.*)$", line)
        if not m:
            continue
        kind, rest = m.groups()
        if rest == "(":
            block = kind
        elif kind == "module":
            path = rest.strip().strip('"')
        elif kind == "require":
            requires.add(rest.split()[0])
    return path, requires


class Graph:
    """Module directories, and who requires whom. Edges are keyed on module paths, never on a prefix."""

    def __init__(self, paths, requires, extra=None, manifest_modules=()):
        """paths: dir -> module path; requires: dir -> set of module paths; manifest_modules: module paths."""
        self.paths = dict(paths)
        self.dir_of = {}
        for d, p in self.paths.items():
            self.dir_of[p] = d
        for d, p in self.paths.items():
            for a, b in ((RELAY, LEGACY), (LEGACY, RELAY)):
                if p.startswith(a):
                    self.dir_of.setdefault(b + p[len(a):], d)
        self.rdeps = {d: set() for d in self.paths}
        self.requires = {d: set() for d in self.paths}
        for d, reqs in requires.items():
            for r in reqs:
                t = self.dir_of.get(r)
                if t and t != d:
                    self.requires[d].add(t)
                    self.rdeps[t].add(d)
        for src, targets in (EXTRA_EDGES if extra is None else extra).items():
            for t in targets:
                if src in self.paths and t in self.paths:
                    self.rdeps[src].add(t)
        if MANIFEST_READER in self.paths:
            for m in manifest_modules:
                t = self.dir_of.get(m)
                if t and t != MANIFEST_READER:
                    self.rdeps[t].add(MANIFEST_READER)

    def affected(self, seeds):
        """Seeds and everything that depends on them: dir -> why."""
        why = {s: reason for s, reason in seeds.items()}
        queue = list(why)
        while queue:
            cur = queue.pop(0)
            for d in sorted(self.rdeps.get(cur, ())):
                if d not in why:
                    why[d] = f"depends on {cur}"
                    queue.append(d)
        return why

    def forward(self, d):
        """The relay modules d requires, transitively."""
        seen, queue = set(), [d]
        while queue:
            for t in self.requires.get(queue.pop(), ()):
                if t not in seen:
                    seen.add(t)
                    queue.append(t)
        return seen

    def module_of_package(self, package):
        best = None
        for p, d in self.dir_of.items():
            if (package == p or package.startswith(p + "/")) and (best is None or len(p) > len(best[0])):
                best = (p, d)
        return best[1] if best else None


def load_graph(root, dirs):
    root = pathlib.Path(root)
    paths, requires = {}, {}
    for d in sorted(dirs):
        path, reqs = parse_gomod((root / d / "go.mod").read_text(encoding="utf-8"))
        paths[d], requires[d] = path, reqs
    manifests = set()
    for g in MANIFEST_GLOBS:
        for m in root.glob(g):
            manifests |= set(re.findall(r"module:\s*([^\s,}]+)", m.read_text(encoding="utf-8")))
    return Graph(paths, requires, manifest_modules=manifests)


def submodule_of(root, d, dirs):
    """test-harness when a Go file of the module (nested modules apart) mentions it."""
    base = pathlib.Path(root) / d
    for top, subdirs, files in os.walk(base):
        subdirs[:] = [s for s in subdirs if not (pathlib.Path(top) / s / "go.mod").exists()]
        for f in files:
            if f.endswith(".go") and SUBMODULE in (pathlib.Path(top) / f).read_text(encoding="utf-8", errors="replace"):
                return SUBMODULE
    return ""


def owner(path, dirs):
    best = None
    for d in dirs:
        if path.startswith(d + "/") and (best is None or len(d) > len(best)):
            best = d
    return best


class Result:
    def __init__(self, everything, reason, why):
        self.everything = everything
        self.reason = reason
        self.why = why  # dir -> why, before the always set


def compute(changed, graph, table, base_dirs=(), force=None):
    """The modules affected by `changed` (repository-relative paths); `force` is a reason that sets `all`."""
    missing = sorted(set(graph.paths) - set(table["modules"]))
    if missing:
        raise TableError("no entry in ci-modules.json for: " + ", ".join(missing))
    if force:
        return Result(True, force, {})
    seeds = {}
    for f in changed:
        name = f.rsplit("/", 1)[-1]
        if name in ("go.mod", "go.sum"):
            return Result(True, f"{f} changed: one module resolution decides every version", {})
        if f.endswith(".md") or f.startswith(".claude/"):
            continue
        d = owner(f, graph.paths)
        if d:
            seeds.setdefault(d, f"changed: {f}")
        elif owner(f, base_dirs):
            return Result(True, f"{f} belongs to a module the head deleted", {})
        else:
            return Result(True, f"{f} is outside every module", {})
    return Result(False, "", graph.affected(seeds))


def matrix(result, graph, table, root):
    """The test entries to run, and the reason of each (also for modules without a test command)."""
    mods = table["modules"]
    if result.everything:
        why = {d: result.reason for d in graph.paths}
        always = []
    else:
        why = dict(result.why)
        always = [d for d, e in mods.items() if e.get("always") and d in graph.paths] if why else []
        for d in always:
            why.setdefault(d, "always runs when anything is affected")
    entries, notes = [], {}
    for d in sorted(why):
        e = mods[d]
        if e["test"] is None:
            notes[d] = f"{why[d]}; not tested: {e['reason']}"
            continue
        entries.append({"name": f"test ({d})", "module": d, "artifact": d.replace("/", "-"), "run": e["test"],
                        "coverage": bool(e.get("coverage")), "submodule": submodule_of(root, d, graph.paths)})
        notes[d] = why[d]
    if result.everything or why:
        for name, e in table.get("pseudo", {}).items():
            entries.append({"name": f"test ({name})", "module": name, "artifact": name, "run": e["test"], "coverage": False,
                            "submodule": ""})
            notes[name] = "always runs when anything is affected"
    return entries, notes


def git(root, *args):
    return subprocess.run(["git", *args], cwd=root, capture_output=True, text=True)


def change_set(root, event, ref, sha, before, full_ci=""):
    """(changed paths or None, reason for `all`, base revision). The first parent of the merge commit is the base (Q7)."""
    if full_ci == "true":
        return None, "FULL_CI is true", None
    if event == "workflow_dispatch":
        return None, "a manual run", None
    if event == "pull_request":
        base = git(root, "rev-parse", "--verify", "-q", f"{sha}^1")
        if base.returncode:
            return None, "the merge commit has no first parent", None
        base = base.stdout.strip()
    elif event == "push":
        if ref == "refs/heads/main":
            return None, "a push to main runs everything", None
        base = (before or "").strip()
        if not base or base == ZERO or git(root, "cat-file", "-e", f"{base}^{{commit}}").returncode:
            return None, "the push has no usable base", None
    else:
        return None, f"the {event} event", None
    out = subprocess.run(["git", "diff", "--name-only", "--no-renames", "-z", base, sha], cwd=root, capture_output=True, check=True).stdout
    return [p.decode() for p in out.split(b"\0") if p], "", base


def failed_packages(log):
    """The packages of the `FAIL<TAB>package` lines of a go test log."""
    return re.findall(r"^FAIL\t(\S+)", log, re.M)


def misses(log, graph, tested):
    """Failing modules outside the tested set (their directories), with the packages that failed."""
    out = {}
    for p in failed_packages(log):
        d = graph.module_of_package(p)
        if d and d not in tested:
            out.setdefault(d, []).append(p)
    return out


def oracle_diff(graph, deps):
    """dir -> relay modules that `go list -deps -test` finds and the require closure lacks (deps: dir -> module paths)."""
    out = {}
    for d, paths in deps.items():
        want = graph.forward(d) | {d}
        found = {graph.dir_of[p] for p in paths if p in graph.dir_of}
        if found - want:
            out[d] = sorted(found - want)
    return out


def go_list_deps(root, dirs):
    deps = {}
    for d in sorted(dirs):
        done = subprocess.run(["go", "list", "-deps", "-test", "-f", "{{with .Module}}{{.Path}}{{end}}", "./..."],
                              cwd=pathlib.Path(root) / d, capture_output=True, text=True)
        if done.returncode == 0:
            deps[d] = set(done.stdout.split())
    return deps


def load(root):
    root = pathlib.Path(root)
    prune = parse_prune((root / "Makefile").read_text(encoding="utf-8"))
    table = json.loads((root / ".github" / "scripts" / "ci-modules.json").read_text(encoding="utf-8"))
    dirs = module_dirs(list_files(root), prune)
    return prune, table, load_graph(root, dirs)


def write(path, text):
    if path:
        with open(path, "a", encoding="utf-8") as f:
            f.write(text)


def predicted(root, graph, table, prune, sha, entries):
    """The entries a pull request with the same change would have run: the push to main against its first parent.
    `entries` is what the run selected, the answer when there is no first parent to compare with."""
    base = git(root, "rev-parse", "--verify", "-q", f"{sha}^1")
    if base.returncode:
        return entries
    base = base.stdout.strip()
    out = subprocess.run(["git", "diff", "--name-only", "--no-renames", "-z", base, sha], cwd=root, capture_output=True, check=True).stdout
    changed = [p.decode() for p in out.split(b"\0") if p]
    result = compute(changed, graph, table, module_dirs(list_files(root, base), prune))
    return matrix(result, graph, table, root)[0]


def emit(a, entries, everything, summary, predicted_entries=None):
    print(summary)
    write(a.summary, summary)
    dump = lambda v: json.dumps(v, separators=(",", ":"))
    write(a.github_output, f"test={dump(entries)}\nall={str(everything).lower()}\nlint-all=true\n"
                           f"predicted={dump(entries if predicted_entries is None else predicted_entries)}\n")


def changes(a):
    root = pathlib.Path(a.root)
    prune, table, graph = load(root)
    changed, force, base = change_set(root, a.event, a.ref, a.sha, a.before, os.environ.get("FULL_CI", ""))
    base_dirs = module_dirs(list_files(root, base), prune) if base else set()
    try:
        result = compute(changed or [], graph, table, base_dirs, force or None)
    except TableError as e:
        print(f"::error::{e}")
        return 1
    entries, notes = matrix(result, graph, table, root)
    lines = [f"## Affected modules ({'all' if result.everything else len(entries)})", ""]
    if result.everything:
        lines.append(f"Everything runs: {result.reason}.")
    lines += [f"- `{d}`: {w}" for d, w in sorted(notes.items())]
    guess = None
    if a.event == "push" and a.ref == "refs/heads/main":
        guess = predicted(root, graph, table, prune, a.sha, entries)
        lines += ["", f"A pull request with this change would have run {len(guess)} entries; the miss report compares the failures with them."]
    emit(a, entries, result.everything, "\n".join(lines) + "\n", guess)
    return 0


def everything_from_the_table(root, why):
    """Every entry with a test command, from the table alone, for when the affected set cannot be computed."""
    table = json.loads((pathlib.Path(root) / ".github" / "scripts" / "ci-modules.json").read_text(encoding="utf-8"))
    entries = [{"name": f"test ({d})", "module": d, "artifact": d.replace("/", "-"), "run": e["test"],
                "coverage": bool(e.get("coverage")), "submodule": SUBMODULE}
               for d, e in sorted(table["modules"].items()) if e["test"]]
    entries += [{"name": f"test ({n})", "module": n, "artifact": n, "run": e["test"], "coverage": False, "submodule": SUBMODULE}
                for n, e in sorted(table.get("pseudo", {}).items())]
    return entries


def cmd_changes(a):
    """A bug in the computation must not turn a pull request red: it runs everything instead, and says so."""
    try:
        return changes(a)
    except Exception as e:  # noqa: BLE001 (any failure of the computation, not only the ones anticipated)
        print(f"::warning::ci-modules.py failed ({type(e).__name__}: {e}); every module runs")
        entries = everything_from_the_table(a.root, str(e))
        emit(a, entries, True, f"## Affected modules (all)\n\nEverything runs: ci-modules.py failed with `{type(e).__name__}: {e}`.\n")
        return 0


def cmd_miss_report(a):
    _, _, graph = load(a.root)
    log = "".join(pathlib.Path(f).read_text(encoding="utf-8", errors="replace") for f in sorted(glob.glob(a.log))) if a.log else ""
    tested = {e["module"] for e in json.loads(a.tested or "[]")}
    found = {} if a.all == "true" else misses(log, graph, tested)
    lines = ["## Miss report", ""]
    if not log:
        lines.append("No test log: nothing to compare.")
    elif found:
        lines.append("**MISS**: these modules failed but are outside the set a pull request with this change would have run.")
        lines += [f"- `{d}`: {', '.join(p)}" for d, p in sorted(found.items())]
        for d in sorted(found):
            print(f"::warning::miss: {d} failed but is outside the affected set")
    else:
        lines.append("No miss: every failing module is in the affected set" + (" (everything was selected)." if a.all == "true" else "."))
    if a.oracle:
        _, table, graph = load(a.root)
        diff = oracle_diff(graph, go_list_deps(a.root, graph.paths))
        lines += ["", "### `go list -deps -test` against the require closure", ""]
        lines += [f"- `{d}` uses {', '.join(f'`{x}`' for x in m)}, which its requires do not reach" for d, m in sorted(diff.items())] or ["No difference."]
        for d in sorted(diff):
            print(f"::warning::oracle: {d} uses {', '.join(diff[d])} outside its require closure")
    text = "\n".join(lines) + "\n"
    print(text)
    write(a.summary, text)
    return 0


def main(argv=None):
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--root", default=".")
    sub = p.add_subparsers(dest="command", required=True)
    c = sub.add_parser("changes")
    c.add_argument("--event", required=True)
    c.add_argument("--ref", default="")
    c.add_argument("--sha", required=True)
    c.add_argument("--before", default="")
    c.add_argument("--github-output")
    c.add_argument("--summary")
    c.set_defaults(fn=cmd_changes)
    m = sub.add_parser("miss-report")
    m.add_argument("--log", help="a path or glob of go test logs")
    m.add_argument("--tested", help="the `predicted` output of the changes job")
    m.add_argument("--all", default="false")
    m.add_argument("--oracle", action="store_true", help="also compare the require closure with go list -deps -test")
    m.add_argument("--summary")
    m.set_defaults(fn=cmd_miss_report)
    a = p.parse_args(argv)
    return a.fn(a)


if __name__ == "__main__":
    sys.exit(main())
