#!/usr/bin/env python3
"""Compares the allocs/op of two `go test -bench -benchmem` outputs and fails if any benchmark allocates more.

    compare-allocs.py <base> <head>

Only allocs/op is compared. ns/op depends on the runner and is ignored. A benchmark that ran several times counts with
its lowest allocs/op, which drops the noise of amortized setup. Benchmarks that allocate a hundred or more
times per operation vary by one allocation between runs of the same code, so a rise of one, or of up to 0.1% of
the base, is noise there and does not fail. Below a hundred allocs/op any rise fails. A benchmark that exists in only one of the
outputs is listed and never fails the comparison. A table of the benchmarks whose allocs/op changed goes to stdout and, when GITHUB_STEP_SUMMARY is set, to the job summary.
"""
import os
import re
import sys

# BenchmarkName-8     1000     1234 ns/op     456 B/op     7 allocs/op
LINE = re.compile(r"^(Benchmark\S*?)(?:-\d+)?\s+\d+\s+.*?\s(\d+) allocs/op")


def parse(path):
    """Returns {"<package> <benchmark>": allocs/op}."""
    results = {}
    package = ""
    with open(path, encoding="utf-8") as f:
        for raw in f:
            line = raw.rstrip("\n")
            if line.startswith("pkg: "):
                package = line[len("pkg: "):].strip()
                continue
            m = LINE.match(line)
            if m:
                key, allocs = f"{package} {m.group(1)}", int(m.group(2))
                results[key] = min(allocs, results.get(key, allocs))
    return results


def tolerance(base_allocs):
    """The rise in allocs/op that is noise for a benchmark with base_allocs."""
    return max(base_allocs // 1000, 1 if base_allocs >= 100 else 0)


def short(key):
    package, name = key.rsplit(" ", 1)
    return f"{package.rsplit('/', 1)[-1]}: {name}"


def main(argv):
    if len(argv) != 3:
        print(__doc__, file=sys.stderr)
        return 2
    try:
        base, head = parse(argv[1]), parse(argv[2])
    except OSError as err:
        print(f"cannot read the benchmark output: {err}", file=sys.stderr)
        return 2
    if not base or not head:
        print(f"no benchmark results with allocs/op in {'the base' if not base else 'the head'} output", file=sys.stderr)
        return 2

    common = sorted(set(base) & set(head))
    rose = [k for k in common if head[k] - base[k] > tolerance(base[k])]
    fell = [k for k in common if head[k] < base[k]]
    noise = [k for k in common if 0 < head[k] - base[k] <= tolerance(base[k])]
    added = sorted(set(head) - set(base))
    removed = sorted(set(base) - set(head))

    out = []
    if rose or fell:
        out += ["| benchmark | base allocs/op | head allocs/op | change |", "|---|---:|---:|---:|"]
        for k in rose + fell:
            out.append(f"| {short(k)} | {base[k]} | {head[k]} | {head[k] - base[k]:+d} |")
        out.append("")
    out.append(
        f"{len(common)} benchmarks compared: {len(rose)} allocate more, {len(fell)} allocate less, "
        f"{len(added)} new, {len(removed)} removed."
    )
    if noise:
        out.append("Within noise: " + ", ".join(short(k) for k in noise))
    if added:
        out.append("New: " + ", ".join(short(k) for k in added))
    if removed:
        out.append("Removed: " + ", ".join(short(k) for k in removed))
    if rose:
        out.append("")
        out.append("FAIL: allocs/op rose for " + ", ".join(short(k) for k in rose) + " (NFR-001).")

    text = "\n".join(out)
    print(text)
    summary = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary:
        with open(summary, "a", encoding="utf-8") as f:
            f.write("### Benchmarks: allocs/op against the base\n\n" + text + "\n")
    return 1 if rose else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
