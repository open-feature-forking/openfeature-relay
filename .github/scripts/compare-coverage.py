#!/usr/bin/env python3
"""Compares the statement coverage per package of two sets of Go coverage profiles and fails if a package lost coverage.

    compare-coverage.py <base-dir> <head-dir>

Each directory holds the profiles (`go test -coverprofile`) that a run wrote, one or more `*.out` files. A package's
total is covered statements over statements, the figure `go test -cover` prints; a block that two profiles list is
counted once, and covered if either covers it.

A package fails when its total in the head is lower than in the base by more than TOLERANCE percentage points. Smaller
drops are listed as noise, and a package that exists in only one of the sets is listed and never fails. A table of
the packages whose total changed and the counts go to stdout and, when GITHUB_STEP_SUMMARY is set, to the job summary.
"""
import glob
import os
import re
import sys

# Percentage points a package may lose. A drop of this size in a package of 100 statements is one statement without a
# test; in a package of 20 statements it is 5 points, and fails.
TOLERANCE = 1.0

BLOCK = re.compile(r"^(.+):(\d+\.\d+,\d+\.\d+) (\d+) (\d+)$")


def parse(directory):
    """Returns {package: (covered statements, statements)} over every profile in directory."""
    blocks = {}
    for path in sorted(glob.glob(os.path.join(directory, "**", "*.out"), recursive=True)):
        with open(path, encoding="utf-8") as f:
            for line in f:
                m = BLOCK.match(line.strip())
                if not m:
                    continue
                key = (m.group(1), m.group(2))
                statements, count = int(m.group(3)), int(m.group(4))
                seen = blocks.get(key)
                blocks[key] = (statements, max(count, seen[1]) if seen else count)
    totals = {}
    for (name, _), (statements, count) in blocks.items():
        package = name.rsplit("/", 1)[0] if "/" in name else "."
        covered, total = totals.get(package, (0, 0))
        totals[package] = (covered + (statements if count > 0 else 0), total + statements)
    return {p: v for p, v in totals.items() if v[1] > 0}


def percent(value):
    return 100.0 * value[0] / value[1]


def trim(package):
    """Drops the host and the repository from a package path."""
    return re.sub(r"^github\.com/[^/]+/[^/]+/", "", package)


def main(argv):
    if len(argv) != 3:
        print(__doc__, file=sys.stderr)
        return 2
    try:
        base, head = parse(argv[1]), parse(argv[2])
    except OSError as err:
        print(f"cannot read the coverage profiles: {err}", file=sys.stderr)
        return 2
    if not base or not head:
        print(f"no coverage profiles in {'the base' if not base else 'the head'} directory", file=sys.stderr)
        return 2

    common = sorted(set(base) & set(head))
    delta = {p: percent(head[p]) - percent(base[p]) for p in common}
    dropped = [p for p in common if delta[p] < -TOLERANCE]
    noise = [p for p in common if -TOLERANCE <= delta[p] < 0]
    rose = [p for p in common if delta[p] > 0]
    added, removed = sorted(set(head) - set(base)), sorted(set(base) - set(head))

    out = []
    changed = [p for p in common if delta[p] != 0]
    if changed:
        out += ["| package | base | head | change |", "|---|---:|---:|---:|"]
        for p in sorted(changed, key=lambda p: delta[p]):
            out.append(f"| {trim(p)} | {percent(base[p]):.1f}% | {percent(head[p]):.1f}% | {delta[p]:+.1f} |")
        out.append("")
    out.append(
        f"{len(common)} packages compared: {len(dropped)} lost more than {TOLERANCE:g} point, {len(noise)} lost less, "
        f"{len(rose)} gained, {len(added)} new, {len(removed)} removed."
    )
    if added:
        out.append("New: " + ", ".join(f"{trim(p)} {percent(head[p]):.1f}%" for p in added))
    if removed:
        out.append("Removed: " + ", ".join(trim(p) for p in removed))
    base_all = (sum(v[0] for v in base.values()), sum(v[1] for v in base.values()))
    head_all = (sum(v[0] for v in head.values()), sum(v[1] for v in head.values()))
    out.append(f"All statements: {percent(base_all):.1f}% ({base_all[1]}) -> {percent(head_all):.1f}% ({head_all[1]}).")
    for p in dropped:
        print(f"::error::coverage of {trim(p)} fell from {percent(base[p]):.1f}% to {percent(head[p]):.1f}% "
              f"({delta[p]:+.1f} points, more than {TOLERANCE:g} allowed)")
    text = "\n".join(out)
    print(text)
    summary = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary:
        with open(summary, "a", encoding="utf-8") as f:
            f.write("### Coverage against the base\n\n" + text + "\n")
    return 1 if dropped else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
