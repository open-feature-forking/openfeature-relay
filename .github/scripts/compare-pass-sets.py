#!/usr/bin/env python3
"""Compares the scenarios that two runs of go test -json passed (UC-022 Q14).

usage: compare-pass-sets.py <launchpad.json> <relay.json>

Fails when the two sets differ, when a run passed nothing, or when either run left a scenario that neither passed or
failed. A scenario is a test or subtest name; the packages are not part of it.
"""
import json
import os
import sys


def passed(path):
    """Returns the names of the tests a go test -json output passed, and the names it failed."""
    ok, failed = set(), set()
    with open(path, encoding="utf-8") as stream:
        for line in stream:
            try:
                event = json.loads(line)
            except ValueError:
                continue
            name = event.get("Test")
            if not name:
                continue
            if event.get("Action") == "pass":
                ok.add(name)
            elif event.get("Action") == "fail":
                failed.add(name)
    return ok, failed


def main(argv):
    if len(argv) != 3:
        print(__doc__)
        return 2
    (launchpad_ok, launchpad_failed), (relay_ok, relay_failed) = passed(argv[1]), passed(argv[2])
    lines = [
        "### Launchpad and relay testbed: scenarios passed",
        "",
        f"- launchpad: {len(launchpad_ok)} passed, {len(launchpad_failed)} failed",
        f"- relay testbed: {len(relay_ok)} passed, {len(relay_failed)} failed",
    ]
    problems = []
    for side, ok in (("launchpad", launchpad_ok), ("relay testbed", relay_ok)):
        if not ok:
            problems.append(f"the {side} run passed no scenario")
    for name in sorted(launchpad_ok - relay_ok):
        problems.append(f"passes on the launchpad and not on the relay testbed: {name}")
    for name in sorted(relay_ok - launchpad_ok):
        problems.append(f"passes on the relay testbed and not on the launchpad: {name}")
    if problems:
        lines += ["", "Differences:"] + [f"- {p}" for p in problems]
    summary = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary:
        with open(summary, "a", encoding="utf-8") as stream:
            stream.write("\n".join(lines) + "\n")
    print("\n".join(lines))
    for problem in problems:
        print(f"::error::{problem}")
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
