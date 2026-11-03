#!/usr/bin/env python3
"""Compares the allocs/op of two `go test -bench -benchmem` outputs and fails if any benchmark allocates more.

    compare-allocs.py <base> <head>

Only allocs/op is compared. ns/op depends on the runner and is ignored. A benchmark that ran several times has a
range of allocs/op. It fails when the lowest allocs/op of the head is above the highest of the base and, for a
benchmark that allocates 100 or more times per operation, above the lowest of the base by more than max(2, 1%).

Why a tolerance on the large benchmarks only: a benchmark runs a fixed number of iterations, so the allocations that
happen once per run (a pool that fills, a map that grows, a timer) are spread over those iterations and add up to a
fraction of an allocation per operation that rounds either way. That is a handful of allocations in absolute terms:
about 1 on a benchmark of 380 allocs/op on one machine, 2 across the machines of two jobs of the same pull request.
A hot path of under 100 allocs/op shows one extra allocation as a change of its own, so it gets no tolerance. A rise
that stays inside the tolerance or the base's range is listed as noise. A benchmark that exists in only one of the outputs
is listed and never fails the comparison. A table of the benchmarks whose allocs/op changed, the packages that were
benchmarked and the benchmarks within noise go to stdout and, when GITHUB_STEP_SUMMARY is set, to the job summary.
"""
import os
import re
import sys

# BenchmarkName-8     1000     1234 ns/op     456 B/op     7 allocs/op
LINE = re.compile(r"^(Benchmark\S*?)(?:-\d+)?\s+\d+\s+.*?\s(\d+) allocs/op")


def parse(path):
    """Returns {"<package> <benchmark>": [allocs/op of each run]}."""
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
                results.setdefault(f"{package} {m.group(1)}", []).append(int(m.group(2)))
    return results


def tolerance(base_lowest):
    """The rise in allocs/op that is noise for a benchmark whose base allocates base_lowest at best."""
    return 0 if base_lowest < 100 else max(2, base_lowest // 100)


def show(values):
    low, high = min(values), max(values)
    return str(low) if low == high else f"{low}..{high}"


def trim(package):
    """Drops the host and the repository from a package path."""
    return re.sub(r"^github\.com/[^/]+/[^/]+/", "", package)


def packages(results):
    return {key.rsplit(" ", 1)[0] for key in results}


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
    rose = [k for k in common if min(head[k]) > max(base[k]) and min(head[k]) - min(base[k]) > tolerance(min(base[k]))]
    fell = [k for k in common if max(head[k]) < min(base[k])]
    noise = [k for k in common if k not in rose and min(head[k]) > min(base[k])]
    added = sorted(set(head) - set(base))
    removed = sorted(set(base) - set(head))

    out = []
    if rose or fell:
        out += ["| benchmark | base allocs/op | head allocs/op | change |", "|---|---:|---:|---:|"]
        for k in rose + fell:
            out.append(f"| {short(k)} | {show(base[k])} | {show(head[k])} | {min(head[k]) - min(base[k]):+d} |")
        out.append("")
    out.append(
        f"{len(common)} benchmarks compared: {len(rose)} allocate more, {len(fell)} allocate less, "
        f"{len(added)} new, {len(removed)} removed."
    )
    head_packages, base_packages = packages(head), packages(base)
    out.append("Packages benchmarked: " + ", ".join(sorted(trim(p) for p in head_packages)))
    if head_packages != base_packages:
        out.append(
            "Only in the base: " + (", ".join(sorted(trim(p) for p in base_packages - head_packages)) or "none")
            + "; only in the head: " + (", ".join(sorted(trim(p) for p in head_packages - base_packages)) or "none")
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
