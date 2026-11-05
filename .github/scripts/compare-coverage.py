#!/usr/bin/env python3
"""Compares the statement coverage per package of two sets of Go coverage profiles and fails if a package lost coverage.

    compare-coverage.py [--base-root <checkout> --head-root <checkout>] <base-dir> <head-dir>

Each directory holds the profiles (`go test -coverprofile`) that a run wrote, one or more `*.out` files. A package's
total is covered statements over statements, the figure `go test -cover` prints; a block that two profiles list is
counted once, and covered if either covers it.

A package fails when its total in the head is lower than in the base by more than TOLERANCE percentage points. Smaller
drops are listed as noise, and a package that is new in the head is listed and never fails.

A package that has a profile in the base and none in the head fails, because a module that stops writing its profile
would otherwise leave the check green. The exception is a package the change deleted: with the roots of the two
checkouts given, a package whose directory exists in the base and holds no Go file in the head is listed as removed.
A package whose directory cannot be found in the base checkout fails as well, since nothing then shows it is gone;
without the roots every missing package fails. A table of
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


def deleted(package, base_root, head_root):
    """True if the package's directory exists in the base checkout and holds no Go file in the head checkout."""
    if not base_root or not head_root:
        return False
    rel = trim(package)
    if not os.path.isdir(os.path.join(base_root, rel)):
        return False
    return not glob.glob(os.path.join(head_root, rel, "*.go"))


def main(argv):
    roots = {"--base-root": None, "--head-root": None}
    args = list(argv[1:])
    while args and args[0] in roots:
        if len(args) < 2:
            print(__doc__, file=sys.stderr)
            return 2
        roots[args[0]] = args[1]
        args = args[2:]
    if len(args) != 2:
        print(__doc__, file=sys.stderr)
        return 2
    base_root, head_root = roots["--base-root"], roots["--head-root"]
    try:
        base, head = parse(args[0]), parse(args[1])
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
    added, gone = sorted(set(head) - set(base)), sorted(set(base) - set(head))
    removed = [p for p in gone if deleted(p, base_root, head_root)]
    missing = [p for p in gone if p not in removed]

    out = []
    changed = [p for p in common if delta[p] != 0]
    if changed:
        out += ["| package | base | head | change |", "|---|---:|---:|---:|"]
        for p in sorted(changed, key=lambda p: delta[p]):
            out.append(f"| {trim(p)} | {percent(base[p]):.1f}% | {percent(head[p]):.1f}% | {delta[p]:+.1f} |")
        out.append("")
    out.append(
        f"{len(common)} packages compared: {len(dropped)} lost more than {TOLERANCE:g} point, {len(noise)} lost less, "
        f"{len(rose)} gained, {len(added)} new, {len(removed)} removed, {len(missing)} without a profile."
    )
    if added:
        out.append("New: " + ", ".join(f"{trim(p)} {percent(head[p]):.1f}%" for p in added))
    if removed:
        out.append("Removed: " + ", ".join(trim(p) for p in removed))
    if missing:
        out.append("No profile in the head: " + ", ".join(trim(p) for p in missing))
    base_all = (sum(v[0] for v in base.values()), sum(v[1] for v in base.values()))
    head_all = (sum(v[0] for v in head.values()), sum(v[1] for v in head.values()))
    out.append(f"All statements: {percent(base_all):.1f}% ({base_all[1]}) -> {percent(head_all):.1f}% ({head_all[1]}).")
    for p in dropped:
        print(f"::error::coverage of {trim(p)} fell from {percent(base[p]):.1f}% to {percent(head[p]):.1f}% "
              f"({delta[p]:+.1f} points, more than {TOLERANCE:g} allowed)")
    for p in missing:
        print(f"::error::{trim(p)} has a coverage profile in the base and none in the head, and its directory still has "
              f"Go files (or could not be found in the base checkout)")
    text = "\n".join(out)
    print(text)
    summary = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary:
        with open(summary, "a", encoding="utf-8") as f:
            f.write("### Coverage against the base\n\n" + text + "\n")
    return 1 if dropped or missing else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
