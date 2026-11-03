#!/usr/bin/env python3
"""Compares the allocs/op of two `go test -bench -benchmem` outputs and fails if any benchmark allocates more.

    compare-allocs.py <base> <head>

Only allocs/op is compared. ns/op depends on the runner and is ignored. A benchmark that ran several times has a
range of allocs/op. It fails when the lowest allocs/op of the head is above the highest of the base and, for a
benchmark that is not in NOISY, above the lowest of the base at all.

NOISY lists the benchmarks that were observed to differ between two runs of identical code, each with the size of the
difference that is noise and why. Everything else is strict, whatever its size: a hot path, a targeting resolve or an
endpoint benchmark that gains one allocation fails. A rise that stays inside the tolerance or the base's range is
listed as noise. A benchmark that exists in only one of the outputs
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


# Benchmark name (without package) -> (tolerated rise in allocs/op, extra tolerated rise in thousandths of the base's
# lowest allocs/op, why). run-benchmarks.sh runs 1000 iterations (SetState: 100), which spreads the allocations that
# happen once per run (a pool that fills, a map that grows, a timer) to under one per operation; what is left is a
# rounding. Measured on the same code in 6 runs of 3: 203 or 204 on the fractional evaluation, 180 or 181, 316 or 317,
# 173 or 174 and 213 to 215 on the endpoints. At 100 iterations the same benchmarks moved by up to 6.
_ROUNDING = "one-time allocations amortized over 1000 iterations leave a rounding"
NOISY = {
    "BenchmarkFractionalEvaluation/test_a@faas.com": (1, 0, _ROUNDING),
    "BenchmarkFractionalEvaluation/test_b@faas.com": (1, 0, _ROUNDING),
    "BenchmarkFractionalEvaluation/test_c@faas.com": (1, 0, _ROUNDING),
    "BenchmarkFractionalEvaluation/test_d@faas.com": (1, 0, _ROUNDING),
    "BenchmarkConnectResolveBoolean": (1, 0, _ROUNDING),
    "BenchmarkConnectResolveAll": (1, 0, _ROUNDING),
    "BenchmarkOFREPSingle": (1, 0, _ROUNDING),
    "BenchmarkOFREPBulk": (2, 0, _ROUNDING),
    # 368,000 allocs/op, run for 100 iterations because 1000 take 70 s: it moved by 4 to 7 (0.002%).
    "BenchmarkSetState": (1, 1, "368,000 allocs/op: a handful of one-time allocations is far below 0.1%"),
}


def tolerance(key, base_lowest):
    """The rise in allocs/op that is noise for the benchmark key whose base allocates base_lowest at best."""
    name = key.split(" ", 1)[1]
    if name not in NOISY:
        return 0
    floor, per_mille, _ = NOISY[name]
    return max(floor, base_lowest * per_mille // 1000)


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
    rose = [k for k in common if min(head[k]) > max(base[k]) and min(head[k]) - min(base[k]) > tolerance(k, min(base[k]))]
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
