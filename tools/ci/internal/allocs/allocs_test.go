package allocs

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The saved outputs of go test -bench -benchmem stay with the Python tests until the cutover.
const dataDir = "../../../../.github/scripts/testdata"

func fixture(name string) string { return filepath.Join(dataDir, name) }

// bench is a go test -bench output with the allocs/op of each benchmark run, all at ns ns/op.
func bench(rows map[string][]int, ns int) string {
	var sb strings.Builder
	sb.WriteString("pkg: github.com/open-feature-forking/openfeature-relay/evaluator/flagd\n")
	names := make([]string, 0, len(rows))
	for name := range rows {
		names = append(names, name)
	}
	// the order of benchmarks does not matter to the comparison
	for _, name := range names {
		for _, allocs := range rows[name] {
			fmt.Fprintf(&sb, "%s-4   \t 100\t %d ns/op\t 7649 B/op\t %d allocs/op\n", name, ns, allocs)
		}
	}
	return sb.String()
}

func write(t *testing.T, dir, name, text string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// compare runs the subcommand and returns the exit code, stdout and stderr together, and the summary it wrote.
func compare(t *testing.T, base, head string, withSummary bool) (int, string, string) {
	t.Helper()
	summary := ""
	if withSummary {
		summary = filepath.Join(t.TempDir(), "summary")
	}
	t.Setenv("GITHUB_STEP_SUMMARY", summary)
	var stdout, stderr bytes.Buffer
	code := Run([]string{base, head}, &stdout, &stderr)
	text := stdout.String() + stderr.String()
	if summary == "" {
		return code, text, ""
	}
	b, err := os.ReadFile(summary)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return code, text, string(b)
}

func TestFixtures(t *testing.T) {
	tests := []struct {
		name     string // test_* method of test_compare_allocs.py
		head     string
		wantCode int
		contains []string
		excludes []string
	}{
		{"test_noise_of_the_same_code_passes", "bench-head-noise.txt", 0,
			[]string{"Within noise: flagd: BenchmarkFractionalEvaluation/test_a@faas.com"}, nil},
		{"test_the_same_output_passes", "bench-base.txt", 0, nil, []string{"Within noise"}},
		{"test_a_real_rise_of_a_large_benchmark_fails", "bench-head-large-rise.txt", 1,
			[]string{"FAIL: allocs/op rose for flagd: BenchmarkResolveAllValues"}, nil},
		{"test_one_more_allocation_on_a_hot_path_fails", "bench-head-hot-path-rise.txt", 1,
			[]string{"BenchmarkResolveBooleanValue/test_staticBoolFlag"}, nil},
		{"test_a_fall_passes_and_is_listed", "bench-head-fall.txt", 0,
			[]string{"| flagd: BenchmarkResolveAllValues | 383 | 375..376 | -8 |"}, nil},
		{"test_the_existing_fixtures_report_no_time_deviation noise", "bench-head-noise.txt", 0, nil, []string{"::notice::"}},
		{"test_the_existing_fixtures_report_no_time_deviation fall", "bench-head-fall.txt", 0, nil, []string{"::notice::"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, text, _ := compare(t, fixture("bench-base.txt"), fixture(tt.head), false)
			if code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d\n%s", code, tt.wantCode, text)
			}
			for _, s := range tt.contains {
				if !strings.Contains(text, s) {
					t.Errorf("output lacks %q:\n%s", s, text)
				}
			}
			for _, s := range tt.excludes {
				if strings.Contains(text, s) {
					t.Errorf("output has %q:\n%s", s, text)
				}
			}
		})
	}
}

// test_only_the_listed_benchmarks_have_a_tolerance
func TestOnlyTheListedBenchmarksHaveATolerance(t *testing.T) {
	tests := []struct {
		name       string
		base, head int
		want       int
	}{
		{"BenchmarkFractionalEvaluation/test_a@faas.com", 203, 204, 0}, // listed: one allocation is noise
		{"BenchmarkFractionalEvaluation/test_a@faas.com", 203, 205, 1},
		{"BenchmarkConnectResolveAll", 316, 317, 0},
		{"BenchmarkConnectResolveAll", 316, 318, 1},
		{"BenchmarkOFREPBulk", 213, 215, 0}, // listed with two
		{"BenchmarkOFREPBulk", 213, 216, 1},
		{"BenchmarkSetState", 368390, 368700, 0}, // listed, 0.1% of 368,390 is 368
		{"BenchmarkSetState", 368390, 368800, 1},
		{"BenchmarkResolveAllValues", 383, 384, 1}, // not listed: strict, whatever the size
		{"BenchmarkResolveBooleanValue/test_staticBoolFlag", 22, 23, 1},
		{"BenchmarkTargetingResolve", 100, 101, 1},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s %d to %d", tt.name, tt.base, tt.head), func(t *testing.T) {
			dir := t.TempDir()
			b := write(t, dir, "b", bench(map[string][]int{tt.name: {tt.base, tt.base, tt.base}}, 11000))
			h := write(t, dir, "h", bench(map[string][]int{tt.name: {tt.head, tt.head, tt.head}}, 11000))
			if code, text, _ := compare(t, b, h, false); code != tt.want {
				t.Errorf("exit code = %d, want %d\n%s", code, tt.want, text)
			}
		})
	}
}

// Every entry of the noisy list is a tolerance of its own, with a reason.
func TestNoisyEntriesAreComplete(t *testing.T) {
	for name, entry := range noisy {
		if entry.floor < 1 || entry.why == "" {
			t.Errorf("%s: floor %d, reason %q", name, entry.floor, entry.why)
		}
		if !strings.HasPrefix(name, "Benchmark") {
			t.Errorf("%q is not a benchmark name", name)
		}
	}
	if len(noisy) != 9 {
		t.Errorf("noisy has %d entries, want the 9 of the Python script", len(noisy))
	}
}

// test_a_rise_inside_the_range_of_the_base_passes
func TestARiseInsideTheRangeOfTheBasePasses(t *testing.T) {
	dir := t.TempDir()
	b := write(t, dir, "b", bench(map[string][]int{"BenchmarkX": {30, 36, 31}}, 11000))
	h := write(t, dir, "h", bench(map[string][]int{"BenchmarkX": {34, 35, 36}}, 11000))
	// the base itself varies between 30 and 36
	if code, text, _ := compare(t, b, h, false); code != 0 {
		t.Errorf("exit code = %d, want 0\n%s", code, text)
	}
}

func runNs(t *testing.T, baseNs, headNs, baseAllocs, headAllocs int) (int, string, string) {
	t.Helper()
	dir := t.TempDir()
	b := write(t, dir, "b", bench(map[string][]int{"BenchmarkX": {baseAllocs, baseAllocs, baseAllocs}}, baseNs))
	h := write(t, dir, "h", bench(map[string][]int{"BenchmarkX": {headAllocs, headAllocs, headAllocs}}, headNs))
	return compare(t, b, h, true)
}

// test_a_time_deviation_beyond_5_percent_is_reported_and_passes
func TestATimeDeviationBeyond5PercentIsReportedAndPasses(t *testing.T) {
	for _, tt := range []struct {
		headNs int
		change string
	}{{12000, "+9.1%"}, {9000, "-18.2%"}} {
		t.Run(tt.change, func(t *testing.T) {
			code, text, summary := runNs(t, 11000, tt.headNs, 10, 10)
			if code != 0 {
				t.Fatalf("exit code = %d, want 0\n%s", code, text)
			}
			if want := fmt.Sprintf("::notice::flagd: BenchmarkX: ns/op 11000 -> %d (%s)", tt.headNs, tt.change); !strings.Contains(text, want) {
				t.Errorf("output lacks %q:\n%s", want, text)
			}
			if want := fmt.Sprintf("| flagd: BenchmarkX | 11000 | %d | %s |", tt.headNs, tt.change); !strings.Contains(summary, want) {
				t.Errorf("summary lacks %q:\n%s", want, summary)
			}
		})
	}
}

// test_a_time_deviation_within_5_percent_is_not_reported
func TestATimeDeviationWithin5PercentIsNotReported(t *testing.T) {
	code, text, summary := runNs(t, 11000, 11500, 10, 10)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, text)
	}
	if strings.Contains(text, "::notice::") || strings.Contains(summary, "ns/op beyond") {
		t.Errorf("a deviation was reported:\n%s\n%s", text, summary)
	}
}

// test_a_time_deviation_with_more_allocations_is_a_failure_not_a_notice
func TestATimeDeviationWithMoreAllocationsIsAFailureNotANotice(t *testing.T) {
	code, text, _ := runNs(t, 11000, 20000, 10, 12)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, text)
	}
	if strings.Contains(text, "::notice::") {
		t.Errorf("a notice was printed:\n%s", text)
	}
}

// test_unreadable_or_empty_output_is_not_a_pass
func TestUnreadableOrEmptyOutputIsNotAPass(t *testing.T) {
	dir := t.TempDir()
	empty := write(t, dir, "empty", "PASS\n")
	if code, text, _ := compare(t, fixture("bench-base.txt"), empty, false); code != 2 || !strings.Contains(text, "in the head output") {
		t.Errorf("empty head: exit code = %d, output %q", code, text)
	}
	if code, text, _ := compare(t, empty, fixture("bench-base.txt"), false); code != 2 || !strings.Contains(text, "in the base output") {
		t.Errorf("empty base: exit code = %d, output %q", code, text)
	}
	if code, _, _ := compare(t, fixture("bench-base.txt"), filepath.Join(dir, "missing"), false); code != 2 {
		t.Errorf("missing head: exit code = %d, want 2", code)
	}
}

func TestArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"one"}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "relay-ci compare-allocs <base> <head>") {
		t.Errorf("exit code = %d, stderr %q", code, stderr.String())
	}
}

// The translation hazards of the plan: each row documents what the Go port does.
func TestTranslationHazards(t *testing.T) {
	row := func(pkg string, allocs int) string {
		return fmt.Sprintf("pkg: %s\nBenchmarkA-8 \t 10\t 500 ns/op\t 1 B/op\t %d allocs/op\n", pkg, allocs)
	}
	t.Run("crlf is read as lf", func(t *testing.T) {
		dir := t.TempDir()
		b := write(t, dir, "b", strings.ReplaceAll(row("example.com/x/y/z", 5), "\n", "\r\n"))
		h := write(t, dir, "h", strings.ReplaceAll(row("example.com/x/y/z", 5), "\n", "\r\n"))
		code, text, _ := compare(t, b, h, false)
		if code != 0 || !strings.Contains(text, "1 benchmarks compared") || !strings.Contains(text, "Packages benchmarked: example.com/x/y/z\n") {
			t.Errorf("exit code = %d\n%s", code, text)
		}
	})
	t.Run("a one megabyte line is kept", func(t *testing.T) {
		dir := t.TempDir()
		long := "log: " + strings.Repeat("x", 1<<20) + "\n"
		b := write(t, dir, "b", long+row("p", 5))
		h := write(t, dir, "h", row("p", 5)+long)
		if code, text, _ := compare(t, b, h, false); code != 0 {
			t.Errorf("exit code = %d\n%s", code, text)
		}
	})
	t.Run("a non-ascii digit is not a number", func(t *testing.T) {
		// Python's \d matches it, RE2's does not: the line is not a benchmark result, so there is none.
		dir := t.TempDir()
		b := write(t, dir, "b", "BenchmarkA-8 \t 10\t 500 ns/op\t 1 B/op\t ٣ allocs/op\n")
		if code, _, _ := compare(t, b, b, false); code != 2 {
			t.Errorf("exit code = %d, want 2", code)
		}
	})
	t.Run("invalid utf-8 is replaced", func(t *testing.T) {
		dir := t.TempDir()
		b := write(t, dir, "b", "pkg: a\xffb/c\n"+row("a\xffb/c", 5)[len("pkg: a\xffb/c\n"):])
		code, text, _ := compare(t, b, b, false)
		if code != 0 || !strings.Contains(text, "Packages benchmarked: a\ufffdb/c") {
			t.Errorf("exit code = %d\n%s", code, text)
		}
	})
	t.Run("packages only on one side are listed", func(t *testing.T) {
		dir := t.TempDir()
		b := write(t, dir, "b", row("github.com/o/r/one", 5)+row("github.com/o/r/two", 5))
		h := write(t, dir, "h", row("github.com/o/r/one", 5)+row("github.com/o/r/three", 5))
		code, text, _ := compare(t, b, h, false)
		want := "Only in the base: two; only in the head: three"
		if code != 0 || !strings.Contains(text, want) {
			t.Errorf("exit code = %d, output lacks %q:\n%s", code, want, text)
		}
	})
	t.Run("the ns/op format is Python's g", func(t *testing.T) {
		for v, want := range map[float64]string{11000: "11000", 1234567: "1.23457e+06", 0.5: "0.5", 100000: "100000", 1e6: "1e+06", 12.3456789: "12.3457"} {
			if got := g(v); got != want {
				t.Errorf("g(%v) = %q, want %q", v, got, want)
			}
		}
	})
}
