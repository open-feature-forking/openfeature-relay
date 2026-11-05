"""List the failed tests of a go test log: test, package, time and first error, as a step summary and as annotations
(#190). Report-only; the exit code is always 0.
"""

import argparse
import os
import pathlib
import re
import sys

FAIL = re.compile(r"^\s*--- FAIL: (\S+) \(([\d.]+)s\)")
PKG_FAIL = re.compile(r"^FAIL\t(\S+)(?:\s+(.*\S))?")
OTHER = re.compile(r"^\s*(=== (RUN|CONT|PAUSE|NAME)|--- (PASS|SKIP)):? ")
TIMEOUT = re.compile(r"^panic: test timed out after (\S+)")
RUNNING = re.compile(r"^\s+(Test\S+) \(")
ERROR = re.compile(r"^\s+(Error|Messages):\s+(.*\S)")
CONTINUATION = re.compile(r"^\s+\t\s+(\S.*)$")
LOCATION = re.compile(r"^\s+(\S+_test\.go:\d+): ?(.*)$")


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
        # without an assertion's Error, the last located line: a test logs before it fails
        if (m := LOCATION.match(line)) and m.group(2):
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


def report(rows, job):
    lines = [f"## Failed tests ({job})", ""]
    if not rows:
        return "\n".join(lines + ["No `--- FAIL` or `FAIL` package line in the log.", ""])
    lines += ["| Test | Package | Time | First error |", "|---|---|---|---|"]
    for name, pkg, secs, err in rows:
        err = err.replace("|", "\\|")[:160]
        lines.append(f"| `{name}` | `{pkg}` | {secs + ' s' if secs else ''} | {err} |")
    return "\n".join(lines) + "\n"


def main(argv=None):
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--log", required=True)
    p.add_argument("--job", default="test")
    p.add_argument("--summary")
    a = p.parse_args(argv)
    log = pathlib.Path(a.log).read_text(encoding="utf-8", errors="replace") if os.path.exists(a.log) else ""
    rows = failures(log)
    text = report(rows, a.job)
    print(text)
    for name, pkg, _, err in rows:
        print(f"::error title=FAIL {name}::{pkg}: {err}")
    if a.summary:
        with open(a.summary, "a", encoding="utf-8") as f:
            f.write(text)
    return 0


if __name__ == "__main__":
    sys.exit(main())
