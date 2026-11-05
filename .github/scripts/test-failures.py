"""List the failed tests of a go test log, and whether the change edits each one's module (#190).

A failure in a module the change does not edit is a candidate flake: compare it with #190 and
.claude/plans/flake-report.md before looking for the cause in the change. Report-only; the exit code is always 0.
"""

import argparse
import os
import pathlib
import re
import subprocess
import sys

FAIL = re.compile(r"^\s*--- FAIL: (\S+) \(([\d.]+)s\)")
PKG_FAIL = re.compile(r"^FAIL\t(\S+)(?:\s+(.*\S))?")
OTHER = re.compile(r"^\s*(=== (RUN|CONT|PAUSE|NAME)|--- (PASS|SKIP)):? ")
TIMEOUT = re.compile(r"^panic: test timed out after (\S+)")
RUNNING = re.compile(r"^\s+(Test\S+) \(")
ERROR = re.compile(r"^\s+(Error|Messages):\s+(.*\S)")
CONTINUATION = re.compile(r"^\s+\t\s+(\S.*)$")
LOCATION = re.compile(r"^\s+(\S+_test\.go:\d+): ?(.*)$")
SKIP_DIRS = {".git", "node_modules", "worktrees", "test-harness", "vendor"}


def _record(name, secs, error=""):
    return {"name": name, "secs": secs, "Error": error, "Messages": "", "log": ""}


def failures(log):
    """(test, package, seconds, first error) per failed top-level test, in log order; a package that failed without a
    failed test (a build failure, a panic) has the test '-'. Without -v a test's output follows its FAIL line; with -v
    it precedes it, so the output since the last RUN or PASS line is kept for the FAIL line that may follow."""
    out, pending = [], []
    current, before, field, timeout = None, _record("", ""), None, None
    for line in log.splitlines():
        line = re.sub(r"^\S+Z ", "", line)  # the timestamps of a downloaded Actions log
        if m := FAIL.match(line):
            name = m.group(1).split("/")[0]
            current = next((p for p in pending if p["name"] == name), None)
            if current is None:
                current = dict(before, name=name, secs=m.group(2))
                pending.append(current)
            before, field = _record("", ""), None
            continue
        if OTHER.match(line):
            current, before, field = None, _record("", ""), None
            continue
        if m := TIMEOUT.match(line):
            timeout = _record(None, "", f"timed out after {m.group(1)}")
            continue
        if timeout and timeout["name"] is None and (m := RUNNING.match(line)):
            timeout["name"] = m.group(1)
            continue
        target = current if current is not None else before
        if m := ERROR.match(line):
            field = m.group(1) if not target[m.group(1)] else None
            if field:
                target[field] = m.group(2)
            continue
        if field and (m := CONTINUATION.match(line)):
            if target[field].endswith(":"):
                target[field] += " " + m.group(1)
            field = None
            continue
        field = None
        if not target["log"] and (m := LOCATION.match(line)) and m.group(2):
            target["log"] = f"{m.group(1)}: {m.group(2)}"
        if m := PKG_FAIL.match(line):
            if timeout:
                timeout["name"] = timeout["name"] or "-"
                pending.append(timeout)
            for f in pending or [_record("-", "", m.group(2) or "")]:
                err = f["Error"] or f["log"]
                if f["Messages"]:
                    err += ": " + f["Messages"]
                out.append((f["name"], m.group(1), f["secs"], err))
            pending, current, before, field, timeout = [], None, _record("", ""), None, None
    return out


def modules(root):
    """module path -> directory, from every go.mod under root."""
    found = {}
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in SKIP_DIRS and not d.startswith(".")]
        if "go.mod" in filenames:
            text = pathlib.Path(dirpath, "go.mod").read_text(encoding="utf-8", errors="replace")
            if m := re.search(r"^module\s+(\S+)", text, re.M):
                rel = os.path.relpath(dirpath, root).replace(os.sep, "/")
                found[m.group(1)] = "" if rel == "." else rel
    return found


def module_dir(pkg, mods):
    """The directory of the module that holds the package, by the longest module path that prefixes it."""
    best = None
    for path, d in mods.items():
        if (pkg == path or pkg.startswith(path + "/")) and (best is None or len(path) > len(best[0])):
            best = (path, d)
    return None if best is None else best[1]


def edits(dir_, changed):
    if dir_ is None:
        return None
    if dir_ == "":
        return any("/" not in f and f.endswith((".go", "go.mod", "go.sum")) for f in changed)
    return any(f.startswith(dir_ + "/") for f in changed)


def changed_files(root, base, head):
    if not base:
        return None
    done = subprocess.run(["git", "diff", "--name-only", "--no-renames", base, head], cwd=root, capture_output=True,
                          text=True)
    return done.stdout.split() if done.returncode == 0 else None


def report(rows, mods, changed, job):
    lines = [f"## Failed tests ({job})", ""]
    if not rows:
        return "\n".join(lines + ["No `--- FAIL` or `FAIL` package line in the log.", ""])
    lines += ["| Test | Package | Time | First error | This change edits the module |", "|---|---|---|---|---|"]
    outside = 0
    for name, pkg, secs, err in rows:
        d = module_dir(pkg, mods)
        hit = None if changed is None else edits(d, changed)
        if "[build failed]" in err or "[setup failed]" in err:
            verdict = "build failure, not a flake"
        else:
            outside += hit is False
            verdict = {True: "yes", False: "**no: candidate flake**", None: "unknown"}[hit]
        err = err.replace("|", "\\|")[:160]
        lines.append(f"| `{name}` | `{pkg}` | {secs + ' s' if secs else ''} | {err} | {verdict} |")
    lines.append("")
    if outside:
        lines.append(f"{outside} failure(s) in modules this change does not edit: compare them with #190 and "
                     "`.claude/plans/flake-report.md`, and re-run the job, before looking for the cause in the change.")
    elif changed is None:
        lines.append("No base commit to compare with: the change set is unknown.")
    return "\n".join(lines) + "\n"


def main(argv=None):
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--root", default=".")
    p.add_argument("--log", required=True)
    p.add_argument("--job", default="test")
    p.add_argument("--base", default="", help="the commit to compare with; empty when there is none")
    p.add_argument("--head", default="HEAD")
    p.add_argument("--summary")
    a = p.parse_args(argv)
    log = pathlib.Path(a.log).read_text(encoding="utf-8", errors="replace") if os.path.exists(a.log) else ""
    rows = failures(log)
    text = report(rows, modules(a.root), changed_files(a.root, a.base, a.head), a.job)
    print(text)
    for name, pkg, _, err in rows:
        print(f"::error title=FAIL {name}::{pkg}: {err}")
    if a.summary:
        with open(a.summary, "a", encoding="utf-8") as f:
            f.write(text)
    return 0


if __name__ == "__main__":
    sys.exit(main())
