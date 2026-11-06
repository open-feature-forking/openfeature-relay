package allocs

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const script = "../../../../.github/scripts/compare-allocs.py"

// python3 finds the interpreter; the parity tests skip without one, and fail on a skip in CI.
func python3(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("python3")
	if err != nil {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatal("python3 is missing: the parity tests must not skip in CI")
		}
		t.Skip("python3 is missing")
	}
	return path
}

type outcome struct {
	code           int
	stdout, stderr string
	summary        string
	summaryWritten bool
}

func runPython(t *testing.T, py, base, head string, withSummary bool) outcome {
	t.Helper()
	cmd := exec.Command(py, script, base, head)
	cmd.Env = os.Environ()
	summary := ""
	if withSummary {
		summary = filepath.Join(t.TempDir(), "summary")
		cmd.Env = append(cmd.Env, "GITHUB_STEP_SUMMARY="+summary)
	} else {
		cmd.Env = append(cmd.Env, "GITHUB_STEP_SUMMARY=")
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	out := outcome{code: code, stdout: stdout.String(), stderr: stderr.String()}
	if withSummary {
		if b, err := os.ReadFile(summary); err == nil {
			out.summary, out.summaryWritten = string(b), true
		}
	}
	return out
}

func runGo(t *testing.T, base, head string, withSummary bool) outcome {
	t.Helper()
	summary := ""
	if withSummary {
		summary = filepath.Join(t.TempDir(), "summary")
	}
	t.Setenv("GITHUB_STEP_SUMMARY", summary)
	var stdout, stderr bytes.Buffer
	out := outcome{code: Run([]string{base, head}, &stdout, &stderr)}
	out.stdout, out.stderr = stdout.String(), stderr.String()
	if withSummary {
		if b, err := os.ReadFile(summary); err == nil {
			out.summary, out.summaryWritten = string(b), true
		}
	}
	return out
}

func assertSame(t *testing.T, py, goOut outcome, stderrToo bool) {
	t.Helper()
	if py.code != goOut.code {
		t.Errorf("exit code: python %d, go %d\npython stderr: %s\ngo stderr: %s", py.code, goOut.code, py.stderr, goOut.stderr)
	}
	if py.stdout != goOut.stdout {
		t.Errorf("stdout differs\n--- python\n%s\n--- go\n%s", py.stdout, goOut.stdout)
	}
	if stderrToo && py.stderr != goOut.stderr {
		t.Errorf("stderr differs\n--- python\n%s\n--- go\n%s", py.stderr, goOut.stderr)
	}
	if py.summary != goOut.summary || py.summaryWritten != goOut.summaryWritten {
		t.Errorf("summary differs\n--- python\n%q\n--- go\n%q", py.summary, goOut.summary)
	}
}

func TestParityOnFixtures(t *testing.T) {
	py := python3(t)
	heads := []string{"bench-base.txt", "bench-head-noise.txt", "bench-head-large-rise.txt", "bench-head-hot-path-rise.txt", "bench-head-fall.txt"}
	for _, head := range heads {
		for _, base := range []string{"bench-base.txt", head} {
			t.Run(base+" against "+head, func(t *testing.T) {
				b, h := fixture(base), fixture(head)
				assertSame(t, runPython(t, py, b, h, true), runGo(t, b, h, true), true)
			})
		}
	}
}

func TestParityOnGeneratedOutputs(t *testing.T) {
	py := python3(t)
	multi := func(pkg string, rows ...string) string {
		return "goos: linux\npkg: " + pkg + "\n" + strings.Join(rows, "") + "PASS\n"
	}
	r := func(name string, ns any, allocs ...int) string {
		var sb strings.Builder
		for _, a := range allocs {
			fmt.Fprintf(&sb, "%s-16   \t 1000\t %v ns/op\t 7649 B/op\t %d allocs/op\n", name, ns, a)
		}
		return sb.String()
	}
	tests := []struct {
		name       string
		base, head string
	}{
		{"timing beyond 5 percent", multi("github.com/o/r/a", r("BenchmarkX", 11000, 10, 10, 10), r("BenchmarkY", 1234567.5, 4)),
			multi("github.com/o/r/a", r("BenchmarkX", 12000, 10, 10, 10), r("BenchmarkY", 2000000, 4))},
		{"small and large ns/op", multi("p", r("BenchmarkX", 0.25, 1), r("BenchmarkY", 100000, 1), r("BenchmarkZ", 1e7, 1)),
			multi("p", r("BenchmarkX", 0.5, 1), r("BenchmarkY", 200000, 1), r("BenchmarkZ", 2e7, 1))},
		{"added removed and packages", multi("github.com/o/r/a", r("BenchmarkOld", 100, 3), r("BenchmarkKeep", 100, 3)) + multi("github.com/o/r/gone", r("BenchmarkG", 100, 1)),
			multi("github.com/o/r/a", r("BenchmarkNew", 100, 3), r("BenchmarkKeep", 100, 3)) + multi("github.com/o/r/fresh", r("BenchmarkF", 100, 1))},
		{"same name in two packages", multi("a/x", r("BenchmarkS", 100, 5)) + multi("b/x", r("BenchmarkS", 100, 5)),
			multi("a/x", r("BenchmarkS", 100, 6)) + multi("b/x", r("BenchmarkS", 100, 5))},
		{"range and fall", multi("p", r("BenchmarkR", 100, 30, 36, 31), r("BenchmarkF", 100, 50, 51)),
			multi("p", r("BenchmarkR", 100, 34, 35, 36), r("BenchmarkF", 100, 40, 41))},
		{"noisy and set state", multi("p", r("BenchmarkSetState", 100, 368390), r("BenchmarkOFREPBulk", 100, 213)),
			multi("p", r("BenchmarkSetState", 100, 368700), r("BenchmarkOFREPBulk", 100, 215))},
		{"crlf", strings.ReplaceAll(multi("p", r("BenchmarkX", 100, 5)), "\n", "\r\n"), multi("p", r("BenchmarkX", 100, 6))},
		{"lone cr", strings.ReplaceAll(multi("p", r("BenchmarkX", 100, 5)), "\n", "\r"), multi("p", r("BenchmarkX", 100, 5))},
		{"no ns/op", "BenchmarkX-8 \t 10\t 5 B/op\t 5 allocs/op\n", "BenchmarkX-8 \t 10\t 5 B/op\t 7 allocs/op\n"},
		{"no package", r("BenchmarkX", 100, 5), r("BenchmarkX", 100, 5)},
		{"package with a space", multi("a b/c", r("BenchmarkSetState", 100, 5)), multi("a b/c", r("BenchmarkSetState", 100, 6))},
		{"zero ns/op", multi("p", r("BenchmarkX", 0, 5)), multi("p", r("BenchmarkX", 100, 5))},
		{"empty base", "PASS\n", multi("p", r("BenchmarkX", 100, 5))},
		{"empty head", multi("p", r("BenchmarkX", 100, 5)), ""},
		{"both empty", "", ""},
		{"no trailing newline", strings.TrimSuffix(multi("p", r("BenchmarkX", 100, 5)), "\n"), strings.TrimSuffix(multi("p", r("BenchmarkX", 100, 5)), "\n")},
	}
	for _, tt := range tests {
		for _, summary := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s summary=%v", tt.name, summary), func(t *testing.T) {
				dir := t.TempDir()
				b, h := write(t, dir, "b", tt.base), write(t, dir, "h", tt.head)
				assertSame(t, runPython(t, py, b, h, summary), runGo(t, b, h, summary), true)
			})
		}
	}
}

func TestParityOnTolerances(t *testing.T) {
	py := python3(t)
	for _, name := range []string{"BenchmarkOFREPBulk", "BenchmarkSetState", "BenchmarkStrict"} {
		for _, base := range []int{100, 1000, 368390} {
			for delta := -1; delta <= 5; delta++ {
				for _, perMille := range []int{0, 1, 2} {
					head := base + delta + base*perMille/1000
					t.Run(fmt.Sprintf("%s %d to %d", name, base, head), func(t *testing.T) {
						dir := t.TempDir()
						b := write(t, dir, "b", bench(map[string][]int{name: {base, base, base}}, 11000))
						h := write(t, dir, "h", bench(map[string][]int{name: {head, head, head}}, 11000))
						assertSame(t, runPython(t, py, b, h, false), runGo(t, b, h, false), true)
					})
				}
			}
		}
	}
}

func TestParityOnBadInput(t *testing.T) {
	py := python3(t)
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")
	// The text of an unreadable file is the operating system's, so only the outcome is compared.
	assertSame(t, runPython(t, py, fixture("bench-base.txt"), missing, false), runGo(t, fixture("bench-base.txt"), missing, false), false)
	assertSame(t, runPython(t, py, missing, fixture("bench-base.txt"), false), runGo(t, missing, fixture("bench-base.txt"), false), false)
}
