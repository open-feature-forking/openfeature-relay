package passsets

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// events is a go test -json output with the given tests passed and failed, and a line that is not JSON.
func events(passed, failed []string) string {
	out := []map[string]string{{"Action": "pass", "Package": "p"}} // a package event has no test
	for _, name := range passed {
		out = append(out, map[string]string{"Action": "pass", "Package": "p", "Test": name})
	}
	for _, name := range failed {
		out = append(out, map[string]string{"Action": "fail", "Package": "p", "Test": name})
	}
	out = append(out, map[string]string{"Action": "output", "Package": "p", "Test": "TestRPC", "Output": "noise"})
	var sb strings.Builder
	for _, e := range out {
		b, err := json.Marshal(e)
		if err != nil {
			panic(err)
		}
		sb.Write(b)
		sb.WriteByte('\n')
	}
	return sb.String() + "not json\n"
}

func write(t *testing.T, dir, name, text string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func compare(t *testing.T, launchpad, relay string) (int, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GITHUB_STEP_SUMMARY", "")
	var stdout, stderr bytes.Buffer
	code := Run([]string{write(t, dir, "launchpad.json", launchpad), write(t, dir, "relay.json", relay)}, &stdout, &stderr)
	return code, stdout.String() + stderr.String()
}

func TestComparePassSets(t *testing.T) {
	tests := []struct {
		name            string // test_* method of test_compare_pass_sets.py
		launchpad       string
		relay           string
		wantCode        int
		wantContains    string
		wantNotContains string
	}{
		{"test_equal_pass_sets_pass", events([]string{"TestRPC/a", "TestRPC/b"}, nil), events([]string{"TestRPC/b", "TestRPC/a"}, nil), 0, "", "Differences"},
		{"test_the_same_failures_on_both_are_not_a_difference",
			events([]string{"TestRPC/a"}, []string{"TestRPC/c"}), events([]string{"TestRPC/a"}, []string{"TestRPC/c"}), 0, "1 passed, 1 failed", "Differences"},
		{"test_a_scenario_the_relay_testbed_fails_is_a_difference",
			events([]string{"TestRPC/a", "TestRPC/b"}, nil), events([]string{"TestRPC/a"}, []string{"TestRPC/b"}), 1,
			"passes on the launchpad and not on the relay testbed: TestRPC/b", ""},
		{"test_a_scenario_only_the_relay_testbed_passes_is_a_difference",
			events([]string{"TestRPC/a"}, nil), events([]string{"TestRPC/a", "TestRPC/b"}, nil), 1,
			"passes on the relay testbed and not on the launchpad: TestRPC/b", ""},
		{"test_a_run_that_passed_nothing_fails", events(nil, nil), events(nil, nil), 1, "the launchpad run passed no scenario", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, out := compare(t, tt.launchpad, tt.relay)
			if code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d\n%s", code, tt.wantCode, out)
			}
			if !strings.Contains(out, tt.wantContains) {
				t.Errorf("output lacks %q:\n%s", tt.wantContains, out)
			}
			if tt.wantNotContains != "" && strings.Contains(out, tt.wantNotContains) {
				t.Errorf("output has %q:\n%s", tt.wantNotContains, out)
			}
		})
	}
}

func TestOutputFormat(t *testing.T) {
	code, out := compare(t, events([]string{"TestRPC/a", "TestRPC/b"}, nil), events([]string{"TestRPC/a"}, nil))
	want := `### Launchpad and relay testbed: scenarios passed

- launchpad: 2 passed, 0 failed
- relay testbed: 1 passed, 0 failed

Differences:
- passes on the launchpad and not on the relay testbed: TestRPC/b
::error::passes on the launchpad and not on the relay testbed: TestRPC/b
`
	if code != 1 || out != want {
		t.Errorf("exit code = %d, output:\n%s\nwant:\n%s", code, out, want)
	}
}

func TestSummaryIsAppended(t *testing.T) {
	dir := t.TempDir()
	summary := write(t, dir, "summary", "before\n")
	t.Setenv("GITHUB_STEP_SUMMARY", summary)
	var stdout, stderr bytes.Buffer
	args := []string{write(t, dir, "a", events([]string{"T"}, nil)), write(t, dir, "b", events([]string{"T"}, nil))}
	if code := Run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d\n%s", code, stderr.String())
	}
	b, _ := os.ReadFile(summary)
	if got := string(b); !strings.HasPrefix(got, "before\n### Launchpad") || !strings.HasSuffix(got, "- relay testbed: 1 passed, 0 failed\n") {
		t.Errorf("summary = %q", got)
	}
}

func TestArgumentsAndUnreadableFiles(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"one"}, &stdout, &stderr); code != 2 || !strings.Contains(stdout.String(), "usage: relay-ci compare-pass-sets") {
		t.Errorf("exit code = %d, stdout %q", code, stdout.String())
	}
	dir := t.TempDir()
	ok := write(t, dir, "ok", events([]string{"T"}, nil))
	if code := Run([]string{ok, filepath.Join(dir, "missing")}, &stdout, &stderr); code != 1 {
		t.Errorf("missing file: exit code = %d, want 1", code)
	}
}

// The translation hazards of the plan: each row documents what the Go port does.
func TestTranslationHazards(t *testing.T) {
	t.Run("crlf is read as lf", func(t *testing.T) {
		text := strings.ReplaceAll(events([]string{"TestRPC/a"}, nil), "\n", "\r\n")
		if code, out := compare(t, text, text); code != 0 {
			t.Errorf("exit code = %d\n%s", code, out)
		}
	})
	t.Run("a one megabyte line is kept", func(t *testing.T) {
		big := `{"Action":"output","Test":"TestRPC","Output":"` + strings.Repeat("x", 1<<20) + `"}` + "\n"
		text := big + events([]string{"TestRPC/a"}, nil) + big
		if code, out := compare(t, text, text); code != 0 {
			t.Errorf("exit code = %d\n%s", code, out)
		}
	})
	t.Run("field names match exactly", func(t *testing.T) {
		// encoding/json would match "test" and "action" to the fields Test and Action; Python does not.
		text := `{"action":"pass","test":"TestRPC/a"}` + "\n"
		if ok, _ := Passed(text); len(ok) != 0 {
			t.Errorf("passed = %v, want none", ok)
		}
	})
	t.Run("lines that are not objects are skipped", func(t *testing.T) {
		text := "[1]\n42\n\"s\"\nnull\n\n" + `{"Action":"pass","Test":"T"}` + "\n"
		if ok, _ := Passed(text); len(ok) != 1 || !ok["T"] {
			t.Errorf("passed = %v, want T", ok)
		}
	})
	t.Run("a line without a final newline is read", func(t *testing.T) {
		if ok, _ := Passed(`{"Action":"pass","Test":"T"}`); !ok["T"] {
			t.Errorf("passed = %v, want T", ok)
		}
	})
	t.Run("a test with an empty name is not a scenario", func(t *testing.T) {
		if ok, _ := Passed(`{"Action":"pass","Test":""}`); len(ok) != 0 {
			t.Errorf("passed = %v, want none", ok)
		}
	})
	t.Run("differences are sorted", func(t *testing.T) {
		_, problems := Compare(events([]string{"b", "a", "c"}, nil), events([]string{"c"}, nil))
		want := []string{
			"passes on the launchpad and not on the relay testbed: a",
			"passes on the launchpad and not on the relay testbed: b",
		}
		if strings.Join(problems, "\n") != strings.Join(want, "\n") {
			t.Errorf("problems = %q", problems)
		}
	})
}

func TestParity(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatal("python3 is missing: the parity tests must not skip in CI")
		}
		t.Skip("python3 is missing")
	}
	big := `{"Action":"output","Test":"TestRPC","Output":"` + strings.Repeat("x", 1<<20) + `"}` + "\n"
	cases := []struct {
		name             string
		launchpad, relay string
	}{
		{"equal", events([]string{"TestRPC/a", "TestRPC/b"}, nil), events([]string{"TestRPC/b", "TestRPC/a"}, nil)},
		{"same failures", events([]string{"a"}, []string{"c"}), events([]string{"a"}, []string{"c"})},
		{"relay fails one", events([]string{"a", "b"}, nil), events([]string{"a"}, []string{"b"})},
		{"relay passes one more", events([]string{"a"}, nil), events([]string{"a", "b"}, nil)},
		{"nothing passed", events(nil, nil), events(nil, nil)},
		{"relay passed nothing", events([]string{"a"}, nil), events(nil, []string{"a"})},
		{"many differences", events([]string{"z", "y", "x", "m"}, nil), events([]string{"a", "b", "m"}, nil)},
		{"crlf", strings.ReplaceAll(events([]string{"a"}, nil), "\n", "\r\n"), events([]string{"a"}, nil)},
		{"lone cr", strings.ReplaceAll(events([]string{"a"}, nil), "\n", "\r"), events([]string{"a"}, nil)},
		{"long line", big + events([]string{"a"}, nil), events([]string{"a"}, nil) + big},
		{"lower case fields", `{"action":"pass","test":"a"}` + "\n", events([]string{"a"}, nil)},
		{"no final newline", strings.TrimSuffix(events([]string{"a"}, nil), "\n"), events([]string{"a"}, nil)},
		{"unicode names", events([]string{"TestÄ/ü", "Test/日本"}, nil), events([]string{"Test/日本"}, nil)},
		{"percent in a name", events([]string{"a%0Ab"}, nil), events(nil, nil)},
	}
	for _, c := range cases {
		for _, withSummary := range []bool{true, false} {
			t.Run(c.name, func(t *testing.T) {
				dir := t.TempDir()
				a, b := write(t, dir, "a.json", c.launchpad), write(t, dir, "b.json", c.relay)
				pySummary, goSummary := filepath.Join(dir, "py-summary"), filepath.Join(dir, "go-summary")
				if !withSummary {
					pySummary, goSummary = "", ""
				}
				cmd := exec.Command(py, "../../../../.github/scripts/compare-pass-sets.py", a, b)
				cmd.Env = append(os.Environ(), "GITHUB_STEP_SUMMARY="+pySummary)
				var pyOut, pyErr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &pyOut, &pyErr
				pyCode := 0
				if err := cmd.Run(); err != nil {
					ee, ok := err.(*exec.ExitError)
					if !ok {
						t.Fatal(err)
					}
					pyCode = ee.ExitCode()
				}
				t.Setenv("GITHUB_STEP_SUMMARY", goSummary)
				var goOut, goErr bytes.Buffer
				goCode := Run([]string{a, b}, &goOut, &goErr)
				if pyCode != goCode || pyOut.String() != goOut.String() || pyErr.String() != goErr.String() {
					t.Errorf("python: %d\n%s%s\ngo: %d\n%s%s", pyCode, pyOut.String(), pyErr.String(), goCode, goOut.String(), goErr.String())
				}
				if withSummary {
					p, _ := os.ReadFile(pySummary)
					g, _ := os.ReadFile(goSummary)
					if string(p) != string(g) {
						t.Errorf("summary differs\n--- python\n%q\n--- go\n%q", p, g)
					}
				}
			})
		}
	}
	t.Run("wrong arguments", func(t *testing.T) {
		out, err := exec.Command(py, "../../../../.github/scripts/compare-pass-sets.py", "one").Output()
		ee, ok := err.(*exec.ExitError)
		if !ok || ee.ExitCode() != 2 {
			t.Fatalf("python: %v", err)
		}
		var goOut, goErr bytes.Buffer
		if code := Run([]string{"one"}, &goOut, &goErr); code != 2 {
			t.Errorf("exit code = %d", code)
		}
		// The usage names the command, which is the only line that differs.
		fix := func(s string) string {
			return strings.NewReplacer("compare-pass-sets.py", "X", "relay-ci compare-pass-sets", "X").Replace(s)
		}
		if fix(string(out)) != fix(goOut.String()) {
			t.Errorf("usage differs\n--- python\n%s\n--- go\n%s", out, goOut.String())
		}
	})
}

// A skipped test is not a pass: the other side passing it is a difference.
func TestASkipIsNotAPass(t *testing.T) {
	skip := `{"Action":"skip","Package":"p","Test":"TestRPC/a"}` + "\n"
	ok, failed := Passed(skip)
	if len(ok) != 0 || len(failed) != 0 {
		t.Errorf("passed %v, failed %v, want neither", ok, failed)
	}
	code, out := compare(t, events([]string{"TestRPC/a"}, nil), skip+events([]string{"TestRPC/b"}, nil))
	if code != 1 || !strings.Contains(out, "passes on the launchpad and not on the relay testbed: TestRPC/a") {
		t.Errorf("exit code = %d\n%s", code, out)
	}
}
