package testfailures

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const relay = "github.com/open-feature-forking/openfeature-relay"

// fixture turns the literal \t of a log excerpt into a tab and {RELAY} into the module path, as the Python tests'
// f-strings do.
func fixture(s string) string {
	return strings.NewReplacer(`\t`, "\t", "{RELAY}", relay).Replace(s)
}

// without -v: the output follows the FAIL line; Actions timestamps in front of the first block
var plain = fixture(`2026-11-05T10:00:00.0000000Z --- FAIL: TestManagementPort (1.57s)
2026-11-05T10:00:00.0000000Z     --- FAIL: TestManagementPort/inventory (1.24s)
2026-11-05T10:00:00.0000000Z         management_characterization_test.go:151:
2026-11-05T10:00:00.0000000Z             \tError Trace:\t/x/management_characterization_test.go:151
2026-11-05T10:00:00.0000000Z             \tError:      \tNot equal:
2026-11-05T10:00:00.0000000Z             \t            \texpected: "a"
2026-11-05T10:00:00.0000000Z             \tTest:       \tTestManagementPort/inventory
2026-11-05T10:00:00.0000000Z FAIL
2026-11-05T10:00:00.0000000Z FAIL\t{RELAY}/runtime/managementport\t3.1s
--- FAIL: TestResetRepeatedly (1.10s)
    cycle_test.go:178: 200 resets, slowest 24ms
    cycle_test.go:181:
        \tError Trace:\t/x/cycle_test.go:181
        \tError:      \t"15" is not less than or equal to "14"
        \tMessages:   \tfile descriptors leaked
FAIL
FAIL\t{RELAY}/runtime\t98.2s
FAIL\t{RELAY}/sync/flagd-grpc [build failed]
`)

// with -v: the output precedes the FAIL line
var verbose = fixture(`=== RUN   TestSpans
    otlp_test.go:135:
        \tError Trace:\t/x/harness_test.go:65
        \tError:      \tReceived unexpected error:
        \t            \tbuilding flagd-upgrade-baseline: exit status 1
--- FAIL: TestSpans (350.65s)
=== RUN   TestStartupTime
    startup_test.go:56: startup to /readyz: baseline 174ms
--- PASS: TestStartupTime (3.00s)
FAIL
FAIL\tgithub.com/open-feature/flagd/test/upgrade\t350.676s
`)

var timeout = fixture(`panic: test timed out after 10m0s
\trunning tests:
\t\tTestClientReconnectsAfterRestart (9m58s)
FAIL\t{RELAY}/test/distributions\t600.1s
`)

var noError = fixture(`--- FAIL: TestResetRepeatedly (1.12s)
    cycle_test.go:262: 200 resets, slowest 14.757123ms
    cycle_test.go:266: found unexpected goroutines:
        [Goroutine 6546 in state IO wait, with internal/poll.runtime_pollWait on top of the stack:
FAIL
FAIL\t{RELAY}/runtime\t28.465s
`)

func TestFailures(t *testing.T) {
	tests := []struct {
		name string
		log  string
		want []Failure
	}{
		{"plain log", plain, []Failure{
			{"TestManagementPort", relay + "/runtime/managementport", "1.57", `Not equal: expected: "a"`},
			{"TestResetRepeatedly", relay + "/runtime", "1.10", `"15" is not less than or equal to "14": file descriptors leaked`},
			{"-", relay + "/sync/flagd-grpc", "", "[build failed]"},
		}},
		{"verbose log", verbose, []Failure{{"TestSpans", "github.com/open-feature/flagd/test/upgrade", "350.65",
			"Received unexpected error: building flagd-upgrade-baseline: exit status 1"}}},
		{"without an assertion the last located line", noError, []Failure{
			{"TestResetRepeatedly", relay + "/runtime", "1.12", "cycle_test.go:266: found unexpected goroutines:"}}},
		{"timeout", timeout, []Failure{
			{"TestClientReconnectsAfterRestart", relay + "/test/distributions", "", "timed out after 10m0s"}}},
		{"empty log", "", nil},
		{"CRLF line ends", strings.ReplaceAll(plain, "\n", "\r\n"), nil}, // want filled below
		{"lone CR line ends", strings.ReplaceAll(noError, "\n", "\r"), []Failure{
			{"TestResetRepeatedly", relay + "/runtime", "1.12", "cycle_test.go:266: found unexpected goroutines:"}}},
		{"a timeout without a running test", fixture("panic: test timed out after 1m0s\nFAIL\\t{RELAY}/p\\t60s\n"), []Failure{
			{"-", relay + "/p", "", "timed out after 1m0s"}}},
		{"a package failure with a reason", fixture("FAIL\\t{RELAY}/p [setup failed]\n"), []Failure{
			{"-", relay + "/p", "", "[setup failed]"}}},
		{"a bare FAIL package line", fixture("FAIL\\t{RELAY}/p\n"), []Failure{{"-", relay + "/p", "", ""}}},
		{"a subtest's failure belongs to its parent", fixture("    --- FAIL: TestA/b (0.10s)\n--- FAIL: TestA (0.20s)\nFAIL\\t{RELAY}/p\\t1s\n"), []Failure{
			{"TestA", relay + "/p", "0.10", ""}}},
		{"a PASS line drops the output before it", fixture("=== RUN   TestA\n    a_test.go:1: noise\n--- PASS: TestA (0.00s)\n--- FAIL: TestB (0.10s)\nFAIL\\t{RELAY}/p\\t1s\n"), []Failure{
			{"TestB", relay + "/p", "0.10", ""}}},
		{"only the first Error counts", fixture("--- FAIL: TestA (0.10s)\n    \\tError:      \\tfirst\n    \\tError:      \\tsecond\nFAIL\\t{RELAY}/p\\t1s\n"), []Failure{
			{"TestA", relay + "/p", "0.10", "first"}}},
		{"a continuation after a line that does not end in a colon is dropped", fixture("--- FAIL: TestA (0.10s)\n    \\tError:      \\tfirst\n    \\t            \\tsecond\nFAIL\\t{RELAY}/p\\t1s\n"), []Failure{
			{"TestA", relay + "/p", "0.10", "first"}}},
		{"a failure that is never closed by a package line", "--- FAIL: TestA (0.10s)\n", nil},
		{"Unicode separators split lines as Python's splitlines does", fixture("--- FAIL: TestA (0.10s)\u2028FAIL\\t{RELAY}/p\\t1s\n"), []Failure{
			{"TestA", relay + "/p", "0.10", ""}}},
		{"a failure after a package's failure is a new one", fixture(
			"--- FAIL: TestA (0.10s)\nFAIL\\t{RELAY}/p\\t1s\n--- FAIL: TestA (0.20s)\nFAIL\\t{RELAY}/q\\t1s\n"), []Failure{
			{"TestA", relay + "/p", "0.10", ""}, {"TestA", relay + "/q", "0.20", ""}}},
		{"a timeout with two running tests names the first", fixture("panic: test timed out after 1m0s\n\\trunning tests:\n\\t\\tTestA (59s)\n\\t\\tTestB (58s)\nFAIL\\t{RELAY}/p\\t60s\n"), []Failure{
			{"TestA", relay + "/p", "", "timed out after 1m0s"}}},
		{"the timeout is reset at the package line", fixture("panic: test timed out after 1m0s\n\\t\\tTestA (59s)\nFAIL\\t{RELAY}/p\\t60s\n--- FAIL: TestC (0.10s)\nFAIL\\t{RELAY}/q\\t1s\n"), []Failure{
			{"TestA", relay + "/p", "", "timed out after 1m0s"}, {"TestC", relay + "/q", "0.10", ""}}},
		{"leftover output is not carried into the next package", fixture("=== RUN   TestOld\n    a_test.go:1: leftover\n    \\tError:      \\tboom\nFAIL\\t{RELAY}/p\\t1s\n--- FAIL: TestX (0.10s)\nFAIL\\t{RELAY}/q\\t1s\n"), []Failure{
			{"-", relay + "/p", "", "1s"}, {"TestX", relay + "/q", "0.10", ""}}},
		{"an empty location message keeps the earlier line", "--- FAIL: TestA (0.10s)\n    a_test.go:1: earlier\n    a_test.go:2:\nFAIL\tp\t1s\n", []Failure{
			{"TestA", "p", "0.10", "a_test.go:1: earlier"}}},
		{"trailing whitespace on Error and FAIL lines", fixture("--- FAIL: TestA (0.10s)  \n    \\tError:      \\tNot equal:   \nFAIL\\tp\\t1s   \nFAIL\\tq [build failed]  \n"), []Failure{
			{"TestA", "p", "0.10", "Not equal:"}, {"-", "q", "", "[build failed]"}}},
		{"SKIP, PAUSE, CONT and NAME lines", "=== PAUSE TestA\n=== CONT  TestA\n=== NAME  TestB\n--- SKIP: TestA (0.00s)\n--- FAIL: TestB (0.10s)\nFAIL\tp\t1s\n", []Failure{
			{"TestB", "p", "0.10", ""}}},
		{"U+2029, FS, GS and RS break lines", "--- FAIL: TestA (0.10s)\u2029    a_test.go:1: x\x1dFAIL\tp\t1s\x1e--- FAIL: TestB (0.20s)\x1cFAIL\tq\t1s", []Failure{
			{"TestA", "p", "0.10", "a_test.go:1: x"}, {"TestB", "q", "0.20", ""}}},
	}
	tests[5].want = Failures(plain)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Failures(tc.log)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Failures() = %#v\nwant %#v", got, tc.want)
			}
		})
	}
	if len(tests[5].want) != 3 {
		t.Fatalf("the CRLF row compares against the plain log's 3 failures, got %d", len(tests[5].want))
	}
}

func TestLongLines(t *testing.T) {
	long := strings.Repeat("x", 1<<20)
	log := "--- FAIL: TestLong (1.00s)\n    a_test.go:1: " + long + "\nFAIL\tp\t1s\n"
	got := Failures(log)
	want := []Failure{{"TestLong", "p", "1.00", "a_test.go:1: " + long}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("a 1 MiB line is not kept whole: got %d failures", len(got))
	}
	if r := Report(got, "test"); !strings.Contains(r, "a_test.go:1: "+strings.Repeat("x", 160-len("a_test.go:1: "))+" |") {
		t.Error("the error is not cut at 160 characters")
	}
}

func TestReport(t *testing.T) {
	want := []string{
		"## Failed tests (test)",
		"",
		"| Test | Package | Time | First error |",
		"|---|---|---|---|",
		"| `TestManagementPort` | `" + relay + "/runtime/managementport` | 1.57 s | Not equal: expected: \"a\" |",
		"| `TestResetRepeatedly` | `" + relay + "/runtime` | 1.10 s | \"15\" is not less than or equal to \"14\": " +
			"file descriptors leaked |",
		"| `-` | `" + relay + "/sync/flagd-grpc` |  | [build failed] |",
	}
	got := strings.Split(strings.TrimSuffix(Report(Failures(plain), "test"), "\n"), "\n")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("table = %q\nwant %q", got, want)
	}
	if !strings.HasSuffix(Report(Failures(plain), "test"), "|\n") {
		t.Error("the report does not end in one newline")
	}
}

func TestReportPipeDoesNotSplitTheRow(t *testing.T) {
	if got := Report([]Failure{{"TestX", "p", "1.00", "a | b"}}, "test"); !strings.Contains(got, `| a \| b |`) {
		t.Errorf("pipe not escaped: %q", got)
	}
}

func TestReportNoFailure(t *testing.T) {
	got := Report(nil, "lint")
	want := "## Failed tests (lint)\n\nNo `--- FAIL` or `FAIL` package line in the log.\n"
	if got != want {
		t.Errorf("Report(nil) = %q, want %q", got, want)
	}
}

func TestReportCutsByCharacters(t *testing.T) {
	err := strings.Repeat("é", 200)
	got := Report([]Failure{{"T", "p", "", err}}, "test")
	if !strings.Contains(got, "| "+strings.Repeat("é", 160)+" |") {
		t.Errorf("not cut at 160 characters: %q", got)
	}
}

func TestAnnotation(t *testing.T) {
	tests := []struct{ name, test, pkg, err, want string }{
		{
			"escapes the workflow command",
			"TestX/a:b,c%", "example.test/p", "100% off\r\nnext line: a, b",
			"::error title=FAIL TestX/a%3Ab%2Cc%25::example.test/p: 100%25 off%0D%0Anext line: a, b",
		},
		{"plain failure", "TestX", "example.test/p", "Not equal:", "::error title=FAIL TestX::example.test/p: Not equal:"},
		{"newline in the title", "T\nX", "p", "e", "::error title=FAIL T%0AX::p: e"},
		{"percent only", "T", "p", "50%", "::error title=FAIL T::p: 50%25"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Annotation(tc.test, tc.pkg, tc.err); got != tc.want {
				t.Errorf("Annotation() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")
	if err := os.WriteFile(logPath, []byte(plain), 0o600); err != nil {
		t.Fatal(err)
	}
	summary := filepath.Join(dir, "summary.md")
	var out, errOut bytes.Buffer
	if code := Run([]string{"--log", logPath, "--job", "test", "--summary", summary}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	rows := Failures(plain)
	want := Report(rows, "test") + "\n"
	for _, r := range rows {
		want += Annotation(r.Test, r.Package, r.Err) + "\n"
	}
	if out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
	if got, _ := os.ReadFile(summary); string(got) != Report(rows, "test") {
		t.Errorf("summary = %q", got)
	}
}

func TestRunMissingLogIsAnEmptyLog(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"--log", filepath.Join(t.TempDir(), "none.log"), "--job", "lint"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if want := Report(nil, "lint") + "\n"; out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
}

func TestRunWithoutLogIsAUsageError(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run(nil, &out, &errOut); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
}

// Parity: the Python script and the Go port give the same stdout, summary file and exit code on the same logs.

func python(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("python3")
	if err != nil {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatal("python3 is required for the parity test under GITHUB_ACTIONS")
		}
		t.Skip("no python3")
	}
	return p
}

func TestParityWithPython(t *testing.T) {
	py := python(t)
	script, err := filepath.Abs(filepath.Join("..", "..", "..", "..", ".github", "scripts", "test-failures.py"))
	if err != nil {
		t.Fatal(err)
	}
	logs := map[string]string{
		"plain":               plain,
		"verbose":             verbose,
		"timeout":             timeout,
		"no located error":    noError,
		"crlf":                strings.ReplaceAll(plain, "\n", "\r\n"),
		"lone cr":             strings.ReplaceAll(noError, "\n", "\r"),
		"empty":               "",
		"no failure":          "ok  \tp\t0.1s\n",
		"escapes":             fixture("--- FAIL: TestX/a (1.00s)\n    x_test.go:1: 100% | off, a: b\nFAIL\\tp\\t1s\n"),
		"long error":          fixture("--- FAIL: TestX (1.00s)\n    \\tError:      \\t" + strings.Repeat("ü|", 150) + "\nFAIL\\tp\\t1s\n"),
		"long line":           "--- FAIL: TestLong (1.00s)\n    a_test.go:1: " + strings.Repeat("x", 1<<20) + "\nFAIL\tp\t1s\n",
		"unicode separators":  fixture("--- FAIL: TestA (0.10s)\u2028    \u00a0\\tError:\u00a0\\tNot equal:\v    \\t   \\tnext\u0085FAIL\\tp\\t1s\n"),
		"unicode digits":      fixture("--- FAIL: TestA (\u0661.\u0662s)\nFAIL\\tp\\t1s\n"),
		"two packages":        fixture("--- FAIL: TestA (0.10s)\nFAIL\\t{RELAY}/p\\t1s\n--- FAIL: TestB (0.20s)\nFAIL\\t{RELAY}/q\\t1s\n"),
		"setup failed":        fixture("FAIL\\t{RELAY}/p [setup failed]\n"),
		"messages first":      fixture("--- FAIL: TestA (0.10s)\n    \\tMessages:   \\tm\n    \\tError:      \\te:\n    \\t   \\tmore\nFAIL\\tp\\t1s\n"),
		"timeout no running":  "panic: test timed out after 1m0s\nFAIL\tp\t60s\n",
		"timestamps":          "2026-11-05T10:00:00.0000000Z --- FAIL: TestA (0.10s)\n2026-11-05T10:00:00.0000000Z FAIL\tp\t1s\n",
		"unterminated":        "--- FAIL: TestA (0.10s)\n",
		"two running tests":   fixture("panic: test timed out after 1m0s\n\\trunning tests:\n\\t\\tTestA (59s)\n\\t\\tTestB (58s)\nFAIL\\tp\\t60s\n"),
		"timeout reset":       fixture("panic: test timed out after 1m0s\n\\t\\tTestA (59s)\nFAIL\\tp\\t60s\n--- FAIL: TestC (0.10s)\nFAIL\\tq\\t1s\n"),
		"leftover output":     fixture("=== RUN   TestOld\n    a_test.go:1: leftover\n    \\tError:      \\tboom\nFAIL\\tp\\t1s\n--- FAIL: TestX (0.10s)\nFAIL\\tq\\t1s\n"),
		"empty location":      "--- FAIL: TestA (0.10s)\n    a_test.go:1: earlier\n    a_test.go:2:\nFAIL\tp\t1s\n",
		"trailing whitespace": fixture("--- FAIL: TestA (0.10s)  \n    \\tError:      \\tNot equal:   \nFAIL\\tp\\t1s   \nFAIL\\tq [build failed]  \n"),
		"skip and pause":      "=== PAUSE TestA\n=== CONT  TestA\n=== NAME  TestB\n--- SKIP: TestA (0.00s)\n--- FAIL: TestB (0.10s)\nFAIL\tp\t1s\n",
		"other line breaks":   "--- FAIL: TestA (0.10s)\u2029    a_test.go:1: x\x1dFAIL\tp\t1s\x1e--- FAIL: TestB (0.20s)\x1cFAIL\tq\t1s",
		"no trailing newline": "--- FAIL: TestA (0.10s)\nFAIL\tp\t1s",
	}
	for name, log := range logs {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			logPath := filepath.Join(dir, "test.log")
			if err := os.WriteFile(logPath, []byte(log), 0o600); err != nil {
				t.Fatal(err)
			}
			pySummary, goSummary := filepath.Join(dir, "py.md"), filepath.Join(dir, "go.md")

			var pyOut bytes.Buffer
			cmd := exec.Command(py, "-I", script, "--log", logPath, "--job", "test", "--summary", pySummary)
			cmd.Stdout = &pyOut
			pyCode := 0
			if err := cmd.Run(); err != nil {
				var ee *exec.ExitError
				if !errors.As(err, &ee) {
					t.Fatal(err)
				}
				pyCode = ee.ExitCode()
			}

			var goOut, goErr bytes.Buffer
			goCode := Run([]string{"--log", logPath, "--job", "test", "--summary", goSummary}, &goOut, &goErr)

			if goCode != pyCode {
				t.Errorf("exit code: go %d, python %d", goCode, pyCode)
			}
			if goOut.String() != pyOut.String() {
				t.Errorf("stdout differs:\ngo:\n%s\npython:\n%s", clip(goOut.String()), clip(pyOut.String()))
			}
			a, _ := os.ReadFile(goSummary)
			b, _ := os.ReadFile(pySummary)
			if !bytes.Equal(a, b) {
				t.Errorf("summary differs:\ngo:\n%s\npython:\n%s", clip(string(a)), clip(string(b)))
			}
		})
	}
}

func clip(s string) string {
	if len(s) > 2000 {
		return s[:2000] + "..."
	}
	return s
}
