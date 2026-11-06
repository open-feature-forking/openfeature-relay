// Package allocs compares the allocs/op of two `go test -bench -benchmem` outputs and fails if any benchmark allocates
// more. It is the port of .github/scripts/compare-allocs.py.
//
// Only allocs/op decides. ns/op depends on the runner, so a benchmark whose lowest ns/op differs from the base by more
// than 5% while its allocs/op did not rise is reported as a ::notice:: and in the summary, and never fails. A benchmark
// that ran several times has a range of allocs/op. It fails when the lowest allocs/op of the head is above the highest
// of the base and, for a benchmark that is not in noisy, above the lowest of the base at all.
//
// noisy lists the benchmarks that were observed to differ between two runs of identical code, each with the size of the
// difference that is noise and why. Everything else is strict, whatever its size: a hot path, a targeting resolve or
// an endpoint benchmark that gains one allocation fails. A rise that stays inside the tolerance or the base's range is
// listed as noise. A benchmark that exists in only one of the outputs is listed and never fails the comparison. A
// table of the benchmarks whose allocs/op changed, the packages that were benchmarked and the benchmarks within noise
// go to stdout and, when GITHUB_STEP_SUMMARY is set, to the job summary.
package allocs

import (
	"fmt"
	"io"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/open-feature-forking/openfeature-relay/tools/ci/internal/ghactions"
	"github.com/open-feature-forking/openfeature-relay/tools/ci/internal/textio"
)

// usage is printed when the arguments are not <base> <head>. It is the docstring of the Python script with the command
// renamed.
const usage = "Compares the allocs/op of two `go test -bench -benchmem` outputs and fails if any benchmark allocates more.\n" + `
    relay-ci compare-allocs <base> <head>

Only allocs/op decides. ns/op depends on the runner, so a benchmark whose lowest ns/op differs from the base by more
than 5% while its allocs/op did not rise is reported as a ::notice:: and in the summary, and never fails. A benchmark that ran several times has a
range of allocs/op. It fails when the lowest allocs/op of the head is above the highest of the base and, for a
benchmark that is not in NOISY, above the lowest of the base at all.

NOISY lists the benchmarks that were observed to differ between two runs of identical code, each with the size of the
difference that is noise and why. Everything else is strict, whatever its size: a hot path, a targeting resolve or an
endpoint benchmark that gains one allocation fails. A rise that stays inside the tolerance or the base's range is
listed as noise. A benchmark that exists in only one of the outputs
is listed and never fails the comparison. A table of the benchmarks whose allocs/op changed, the packages that were
benchmarked and the benchmarks within noise go to stdout and, when GITHUB_STEP_SUMMARY is set, to the job summary.

`

// line matches BenchmarkName-8     1000     1234 ns/op     456 B/op     7 allocs/op. RE2 reads \s, \d and \S as ASCII.
var line = regexp.MustCompile(`^(Benchmark\S*?)(?:-\d+)?\s+\d+\s+(?:([\d.]+) ns/op\s+)?.*?\s(\d+) allocs/op`)

// nsDeviation is the change in ns/op that is reported.
const nsDeviation = 0.05

// results holds, per "<package> <benchmark>", the value of each run.
type results map[string][]int

// timings holds, per "<package> <benchmark>", the ns/op of each run.
type timings map[string][]float64

// parse returns the allocs/op and the ns/op of every run in the benchmark output text.
func parse(text string) (results, timings) {
	res, times := results{}, timings{}
	pkg := ""
	for _, l := range textio.SplitLines(text) {
		if rest, ok := strings.CutPrefix(l, "pkg: "); ok {
			pkg = strings.TrimSpace(rest)
			continue
		}
		m := line.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		key := pkg + " " + m[1]
		n, err := strconv.Atoi(m[3])
		if err != nil {
			n = math.MaxInt // more digits than an int holds; Python keeps them all
		}
		res[key] = append(res[key], n)
		if m[2] != "" {
			// A value such as "1.2.3" is not a number; Python stops with a traceback, Go ignores the ns/op.
			if ns, err := strconv.ParseFloat(m[2], 64); err == nil {
				times[key] = append(times[key], ns)
			}
		}
	}
	return res, times
}

// tolerated is a noisy entry: the tolerated rise in allocs/op, the extra tolerated rise in thousandths of the base's
// lowest allocs/op, and why.
type tolerated struct {
	floor    int
	perMille int
	why      string
}

// rounding is why most entries of noisy exist. run-benchmarks.sh runs 1000 iterations (SetState: 100), which spreads
// the allocations that happen once per run (a pool that fills, a map that grows, a timer) to under one per operation;
// what is left is a rounding. Measured on the same code in 6 runs of 3: 203 or 204 on the fractional evaluation, 180
// or 181, 316 or 317, 173 or 174 and 213 to 215 on the endpoints. At 100 iterations the same benchmarks moved by up
// to 6.
const rounding = "one-time allocations amortized over 1000 iterations leave a rounding"

// noisy maps a benchmark name (without package) to its tolerance.
var noisy = map[string]tolerated{
	"BenchmarkFractionalEvaluation/test_a@faas.com": {1, 0, rounding},
	"BenchmarkFractionalEvaluation/test_b@faas.com": {1, 0, rounding},
	"BenchmarkFractionalEvaluation/test_c@faas.com": {1, 0, rounding},
	"BenchmarkFractionalEvaluation/test_d@faas.com": {1, 0, rounding},
	"BenchmarkConnectResolveBoolean":                {1, 0, rounding},
	"BenchmarkConnectResolveAll":                    {1, 0, rounding},
	"BenchmarkOFREPSingle":                          {1, 0, rounding},
	"BenchmarkOFREPBulk":                            {2, 0, rounding},
	// 368,000 allocs/op, run for 100 iterations because 1000 take 70 s: it moved by 4 to 7 (0.002%).
	"BenchmarkSetState": {1, 1, "368,000 allocs/op: a handful of one-time allocations is far below 0.1%"},
}

// tolerance is the rise in allocs/op that is noise for the benchmark key whose base allocates baseLowest at best.
func tolerance(key string, baseLowest int) int {
	_, name, _ := strings.Cut(key, " ")
	t, ok := noisy[name]
	if !ok {
		return 0
	}
	return max(t.floor, baseLowest*t.perMille/1000)
}

// show is the range of values: "7" or "7..9".
func show(v []int) string {
	lo, hi := slices.Min(v), slices.Max(v)
	if lo == hi {
		return strconv.Itoa(lo)
	}
	return fmt.Sprintf("%d..%d", lo, hi)
}

var hostAndRepo = regexp.MustCompile(`^github\.com/[^/]+/[^/]+/`)

// trim drops the host and the repository from a package path.
func trim(pkg string) string { return hostAndRepo.ReplaceAllString(pkg, "") }

func packages(r results) map[string]bool {
	out := map[string]bool{}
	for key := range r {
		out[key[:strings.LastIndex(key, " ")]] = true
	}
	return out
}

// short is "<last element of the package>: <benchmark>".
func short(key string) string {
	i := strings.LastIndex(key, " ")
	pkg, name := key[:i], key[i+1:]
	return pkg[strings.LastIndex(pkg, "/")+1:] + ": " + name
}

func sortedTrimmed(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, trim(p))
	}
	sort.Strings(out)
	return out
}

func difference(a, b map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k := range a {
		if !b[k] {
			out[k] = true
		}
	}
	return out
}

func shorts(keys []string) string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = short(k)
	}
	return strings.Join(out, ", ")
}

func orNone(s []string) string {
	if len(s) == 0 {
		return "none"
	}
	return strings.Join(s, ", ")
}

// g formats a float as Python's {:g} does.
func g(v float64) string { return strconv.FormatFloat(v, 'g', 6, 64) }

type timing struct {
	name   string
	base   float64
	head   float64
	change float64
}

// Result is what a comparison reports.
type Result struct {
	// Text goes to stdout and the summary.
	Text string
	// Notices are the ::notice:: lines that follow the text on stdout.
	Notices []string
	// Timing is the rows of the ns/op table of the summary, empty when nothing deviates.
	Timing []string
	// Rose reports that a benchmark allocates more.
	Rose bool
}

// Compare compares the text of a base output with the text of a head output. Neither may be without results.
func Compare(baseText, headText string) (Result, error) {
	base, baseNs := parse(baseText)
	head, headNs := parse(headText)
	if len(base) == 0 || len(head) == 0 {
		side := "the head"
		if len(base) == 0 {
			side = "the base"
		}
		return Result{}, fmt.Errorf("no benchmark results with allocs/op in %s output", side)
	}

	var common []string
	for k := range base {
		if _, ok := head[k]; ok {
			common = append(common, k)
		}
	}
	sort.Strings(common)

	var rose, fell, noise []string
	for _, k := range common {
		bLow, hLow := slices.Min(base[k]), slices.Min(head[k])
		if hLow > slices.Max(base[k]) && hLow-bLow > tolerance(k, bLow) {
			rose = append(rose, k)
		}
		if slices.Max(head[k]) < bLow {
			fell = append(fell, k)
		}
	}
	for _, k := range common {
		if !slices.Contains(rose, k) && slices.Min(head[k]) > slices.Min(base[k]) {
			noise = append(noise, k)
		}
	}
	var deviations []timing
	for _, k := range common {
		if slices.Contains(rose, k) || len(baseNs[k]) == 0 || len(headNs[k]) == 0 {
			continue
		}
		b, h := slices.Min(baseNs[k]), slices.Min(headNs[k])
		if b > 0 && math.Abs(h-b)/b > nsDeviation {
			deviations = append(deviations, timing{short(k), b, h, (h - b) / b * 100})
		}
	}
	var added, removed []string
	for k := range head {
		if _, ok := base[k]; !ok {
			added = append(added, k)
		}
	}
	for k := range base {
		if _, ok := head[k]; !ok {
			removed = append(removed, k)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)

	var out []string
	if len(rose) > 0 || len(fell) > 0 {
		out = append(out, "| benchmark | base allocs/op | head allocs/op | change |", "|---|---:|---:|---:|")
		for _, k := range slices.Concat(rose, fell) {
			out = append(out, fmt.Sprintf("| %s | %s | %s | %+d |", short(k), show(base[k]), show(head[k]),
				slices.Min(head[k])-slices.Min(base[k])))
		}
		out = append(out, "")
	}
	out = append(out, fmt.Sprintf("%d benchmarks compared: %d allocate more, %d allocate less, %d new, %d removed.",
		len(common), len(rose), len(fell), len(added), len(removed)))
	headPackages, basePackages := packages(head), packages(base)
	out = append(out, "Packages benchmarked: "+strings.Join(sortedTrimmed(headPackages), ", "))
	onlyBase, onlyHead := difference(basePackages, headPackages), difference(headPackages, basePackages)
	if len(onlyBase) > 0 || len(onlyHead) > 0 {
		out = append(out, "Only in the base: "+orNone(sortedTrimmed(onlyBase))+"; only in the head: "+orNone(sortedTrimmed(onlyHead)))
	}
	if len(noise) > 0 {
		out = append(out, "Within noise: "+shorts(noise))
	}
	if len(added) > 0 {
		out = append(out, "New: "+shorts(added))
	}
	if len(removed) > 0 {
		out = append(out, "Removed: "+shorts(removed))
	}
	if len(rose) > 0 {
		out = append(out, "", "FAIL: allocs/op rose for "+shorts(rose)+" (NFR-001).")
	}

	res := Result{Text: strings.Join(out, "\n"), Rose: len(rose) > 0}
	for _, t := range deviations {
		res.Notices = append(res.Notices,
			fmt.Sprintf("::notice::%s: ns/op %s -> %s (%+.1f%%), allocs/op did not rise", t.name, g(t.base), g(t.head), t.change))
		res.Timing = append(res.Timing, fmt.Sprintf("| %s | %s | %s | %+.1f%% |", t.name, g(t.base), g(t.head), t.change))
	}
	return res, nil
}

// Run is the relay-ci compare-allocs subcommand: args are <base> <head>. It returns the exit code: 0, or 1 when a
// benchmark allocates more, or 2 for bad arguments and unreadable or empty outputs.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 {
		_, _ = io.WriteString(stderr, usage)
		return 2
	}
	baseText, err := textio.ReadText(args[0])
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "cannot read the benchmark output: %v\n", err)
		return 2
	}
	headText, err := textio.ReadText(args[1])
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "cannot read the benchmark output: %v\n", err)
		return 2
	}
	res, err := Compare(baseText, headText)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	_, _ = fmt.Fprintln(stdout, res.Text)
	for _, n := range res.Notices {
		_, _ = fmt.Fprintln(stdout, n)
	}
	summary := "### Benchmarks: allocs/op against the base\n\n" + res.Text + "\n"
	if len(res.Timing) > 0 {
		summary += "\n### Benchmarks: ns/op beyond 5% of the base (does not fail)\n\n" +
			"| benchmark | base ns/op | head ns/op | change |\n|---|---:|---:|---:|\n" + strings.Join(res.Timing, "\n") + "\n"
	}
	if err := ghactions.AppendSummary(summary); err != nil {
		_, _ = fmt.Fprintf(stderr, "cannot write the step summary: %v\n", err)
		return 1
	}
	if res.Rose {
		return 1
	}
	return 0
}
