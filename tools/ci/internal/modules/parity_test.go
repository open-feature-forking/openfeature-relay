package modules

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Parity with .github/scripts/ci-modules.py until the cutover (plan step 18, slice A): the same events over the same
// repositories through both implementations. What is compared is what a job reads: the exit code, the flag lines of
// GITHUB_OUTPUT byte for byte, and the "Jobs this change calls for" section of the summary. The Python also writes
// the matrix (`test`, `all`, `lint-all`) and an "Affected modules" section, which the port drops (Q8 (b)).

func pythonCmd(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			if out, err := exec.Command(p, "--version").Output(); err == nil && strings.HasPrefix(string(out), "Python 3") {
				return p
			}
		}
	}
	skip(t, "python3 is not available")
	return ""
}

type outcome struct {
	code    int
	flags   string // the flag lines of GITHUB_OUTPUT, in order
	jobs    string // the "Jobs this change calls for" section of the summary
	failure bool   // the output says the computation fell open (::warning::... failed)
}

func (o outcome) String() string {
	return fmt.Sprintf("exit %d, fell open %v\nflags:\n%sjobs:\n%s", o.code, o.failure, o.flags, o.jobs)
}

func flagNames() map[string]bool {
	names := map[string]bool{}
	for _, f := range Flags {
		names[f.Name] = true
	}
	return names
}

func readOutcome(code int, stdout, dir string) outcome {
	// the Python writes text files with the platform's newline: CRLF on Windows
	out, _ := os.ReadFile(filepath.Join(dir, "out"))
	sum, _ := os.ReadFile(filepath.Join(dir, "summary.md"))
	lf := func(b []byte) []byte { return []byte(strings.ReplaceAll(string(b), "\r\n", "\n")) }
	out, sum = lf(out), lf(sum)
	var flags strings.Builder
	names := flagNames()
	for _, l := range strings.SplitAfter(string(out), "\n") {
		if name, _, ok := strings.Cut(l, "="); ok && names[name] {
			flags.WriteString(l)
		}
	}
	jobs := ""
	if i := strings.Index(string(sum), "## Jobs this change calls for"); i >= 0 {
		jobs = string(sum)[i:]
	}
	failure := strings.Contains(stdout, "::warning::")
	if failure { // the message names the tool and the language's own error text
		jobs = ""
	}
	return outcome{code: code, flags: flags.String(), jobs: jobs, failure: failure}
}

func runGo(t *testing.T, root string, fullCI string, args []string) outcome {
	t.Helper()
	t.Setenv("FULL_CI", fullCI)
	dir := t.TempDir()
	var so, se bytes.Buffer
	code := Run(append([]string{"--root", root, "changes", "--github-output", filepath.Join(dir, "out"),
		"--summary", filepath.Join(dir, "summary.md")}, args...), &so, &se)
	return readOutcome(code, so.String(), dir)
}

func runPython(t *testing.T, py, script, root string, fullCI string, args []string) outcome {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command(py, append([]string{"-I", script, "--root", root, "changes", "--github-output", filepath.Join(dir, "out"),
		"--summary", filepath.Join(dir, "summary.md")}, args...)...)
	cmd.Env = append(os.Environ(), "FULL_CI="+fullCI, "PYTHONIOENCODING=utf-8")
	var so bytes.Buffer
	cmd.Stdout = &so
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return readOutcome(code, so.String(), dir)
}

func pythonScript(t *testing.T) string {
	t.Helper()
	script := filepath.Join(repoRoot(t), ".github", "scripts", "ci-modules.py")
	if _, err := os.Stat(script); err != nil {
		skip(t, "ci-modules.py is not here")
	}
	return script
}

// compare runs both on one event and requires the same outcome.
func compare(t *testing.T, py, script, root, fullCI string, args ...string) {
	t.Helper()
	got := runGo(t, root, fullCI, args)
	want := runPython(t, py, script, root, fullCI, args)
	if got.code != want.code || got.flags != want.flags || got.jobs != want.jobs || got.failure != want.failure {
		t.Errorf("%v (FULL_CI=%q)\nGo:\n%v\nPython:\n%v", args, fullCI, got, want)
	}
}

// parityRepo is the fixture repository with a Dockerfile and a deeper tree, so that more flags have a say.
func parityRepo(t *testing.T) string {
	t.Helper()
	root := fixtureRoot(t)
	return root
}

var mergeCount int

func addMerge(t *testing.T, root string, ops ...string) (merge, parent string) {
	t.Helper()
	mergeCount++
	gitIn(t, root, "checkout", "-q", "-b", "br")
	for _, op := range ops {
		if name, ok := strings.CutPrefix(op, "-"); ok {
			gitIn(t, root, "rm", "-q", "-f", name)
		} else {
			content := op + "\n" + strconv.Itoa(mergeCount)
			if strings.HasSuffix(op, "go.mod") { // the Python reads the module path of every go.mod
				content = "module example.com/" + strings.TrimSuffix(op, "/go.mod") + "\n// " + strconv.Itoa(mergeCount) + "\n"
			}
			writeFile(t, root, op, content)
			gitIn(t, root, "add", op)
		}
	}
	gitIn(t, root, "commit", "-q", "-m", strings.Join(ops, ","))
	gitIn(t, root, "checkout", "-q", "main")
	writeFile(t, root, "later.txt", strconv.Itoa(mergeCount))
	gitIn(t, root, "add", "later.txt")
	gitIn(t, root, "commit", "-q", "-m", "main moved")
	gitIn(t, root, "merge", "-q", "--no-ff", "-m", "merge", "br")
	gitIn(t, root, "branch", "-q", "-D", "br")
	merge = gitIn(t, root, "rev-parse", "HEAD")
	return merge, gitIn(t, root, "rev-parse", merge+"^1")
}

func TestParityOnTemporaryRepositories(t *testing.T) {
	py, script := pythonCmd(t), pythonScript(t)
	root := parityRepo(t)
	zero, unknown := Zero, strings.Repeat("1", 40)
	scenarios := [][]string{
		{"core/x.go"}, {"README.md"}, {".claude/plans/x.md"}, {"go.mod"}, {"core/go.mod"}, {"core/go.sum"}, {"-core/go.mod"},
		{"tools/ci/main.go"}, {"tools/ci/go.mod"}, {"tools/ci/go.sum"}, {"tools/ci/internal/allocs/a.go"},
		{"tools/ci/internal/passsets/p.go"}, {"tools/ci/internal/modules/m.go"}, {"core/tools/ci/x.go"},
		{".github/workflows/build.yaml"}, {".github/workflows/other.yaml"}, {"a/Dockerfile"}, {"a/b.Dockerfile"},
		{"builder/reference/x"}, {"test/image/x"}, {"distributions/f/manifest.yaml"}, {"flagd/x"}, {"testbed/cmd/x.go"},
		{"test/upgrade/x"}, {"test/testbed/x"}, {"test/integration/x"}, {".github/scripts/compare-allocs.py"},
		{".github/scripts/run-benchmarks.sh"}, {".github/scripts/testbed-suites.sh"}, {".github/scripts/compare-pass-sets.py"},
		{"-core/x.go"}, {"core/x.go", "README.md"}, {"README.md", "tools/ci/main.go"}, {"dé/ü.go"},
	}
	for _, ops := range scenarios {
		merge, parent := addMerge(t, root, ops...)
		name := strings.Join(ops, ",")
		t.Run("pull request "+name, func(t *testing.T) {
			compare(t, py, script, root, "", "--event", "pull_request", "--ref", "refs/pull/1/merge", "--sha", merge, "--before", "")
		})
		t.Run("push to main "+name, func(t *testing.T) {
			compare(t, py, script, root, "", "--event", "push", "--ref", "refs/heads/main", "--sha", merge, "--before", parent)
		})
		t.Run("push to a branch "+name, func(t *testing.T) {
			compare(t, py, script, root, "", "--event", "push", "--ref", "refs/heads/x", "--sha", merge, "--before", parent)
		})
	}

	head := gitIn(t, root, "rev-parse", "HEAD")
	first := gitIn(t, root, "rev-list", "--max-parents=0", "HEAD")
	events := map[string][]string{
		"push to main, empty before":   {"--event", "push", "--ref", "refs/heads/main", "--sha", head, "--before", ""},
		"push to main, zero before":    {"--event", "push", "--ref", "refs/heads/main", "--sha", head, "--before", zero},
		"push to main, unknown before": {"--event", "push", "--ref", "refs/heads/main", "--sha", head, "--before", unknown},
		"push to a branch, no before":  {"--event", "push", "--ref", "refs/heads/x", "--sha", head},
		"a pull request without ^1":    {"--event", "pull_request", "--sha", first},
		"a pull request, unknown sha":  {"--event", "pull_request", "--sha", unknown},
		"a push, unknown sha":          {"--event", "push", "--sha", unknown, "--before", first},
		"workflow_dispatch":            {"--event", "workflow_dispatch", "--sha", head},
		"schedule":                     {"--event", "schedule", "--sha", head},
		"an event nobody listed":       {"--event", "release", "--sha", head},
	}
	for name, args := range events {
		t.Run(name, func(t *testing.T) { compare(t, py, script, root, "", args...) })
	}
	merge, parent := addMerge(t, root, "README.md")
	for _, ev := range [][]string{
		{"--event", "pull_request", "--sha", merge},
		{"--event", "push", "--ref", "refs/heads/main", "--sha", merge, "--before", parent},
	} {
		t.Run("FULL_CI on "+ev[1], func(t *testing.T) { compare(t, py, script, root, "true", ev...) })
		t.Run("FULL_CI=1 on "+ev[1], func(t *testing.T) { compare(t, py, script, root, "1", ev...) })
	}
}

func TestParityOnTheErrorSources(t *testing.T) {
	py, script := pythonCmd(t), pythonScript(t)
	args := []string{"--event", "workflow_dispatch", "--sha", "HEAD"}
	tests := []struct {
		name  string
		setup func(t *testing.T, root string)
	}{
		{"a table entry is missing", func(t *testing.T, r string) { writeFile(t, r, "extra/go.mod", "module example.com/extra\n") }},
		{"two table entries are missing", func(t *testing.T, r string) {
			writeFile(t, r, "extra/go.mod", "module example.com/extra\n")
			writeFile(t, r, "a/b/go.mod", "module example.com/b\n")
		}},
		{"a pruned module needs no entry", func(t *testing.T, r string) { writeFile(t, r, "test/integration/go.mod", "module example.com/i\n") }},
		{"no ALL_GO_MOD_DIRS line", func(t *testing.T, r string) { writeFile(t, r, "Makefile", "all:\n") }},
		{"broken ci-modules.json", func(t *testing.T, r string) { writeFile(t, r, ".github/scripts/ci-modules.json", "{") }},
		{"ci-modules.json without modules", func(t *testing.T, r string) { writeFile(t, r, ".github/scripts/ci-modules.json", "{}") }},
		{"CRLF Makefile and table", func(t *testing.T, r string) {
			writeFile(t, r, "Makefile", strings.ReplaceAll(makefileText, "\n", "\r\n"))
			writeFile(t, r, ".github/scripts/ci-modules.json", strings.ReplaceAll("{\n \"modules\": {\"core\": {\"test\": null, \"reason\": \"r\"}, \"tools/ci\": {\"test\": null, \"reason\": \"r\"}}\n}\n", "\n", "\r\n"))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := fixtureRoot(t)
			tt.setup(t, root)
			compare(t, py, script, root, "", args...)
			// a pull request: the diff of the (first-parent-less) head
			compare(t, py, script, root, "", "--event", "pull_request", "--sha", "HEAD")
		})
	}
}

// TestParityOnRealRanges is the local gate of plan M6: both implementations over the last N first-parent merges of the
// branch, fed as pull requests (sha = the merge, base = its first parent), in the working tree of this checkout. It
// needs full history, so CI does not run it: RELAY_CI_REAL_RANGES=30 RELAY_CI_RANGES_BRANCH=origin/main.
func TestParityOnRealRanges(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("RELAY_CI_REAL_RANGES"))
	if n <= 0 {
		t.Skip("set RELAY_CI_REAL_RANGES=<n> to compare both implementations over the last n merges")
	}
	py, script, root := pythonCmd(t), pythonScript(t), repoRoot(t)
	branch := os.Getenv("RELAY_CI_RANGES_BRANCH")
	if branch == "" {
		branch = "origin/main"
	}
	merges := strings.Fields(gitIn(t, root, "rev-list", "--first-parent", "--merges", "-n", strconv.Itoa(n), branch))
	if len(merges) == 0 {
		t.Fatalf("no merges on %s", branch)
	}
	differing := 0
	for _, m := range merges {
		args := []string{"--event", "pull_request", "--sha", m}
		got := runGo(t, root, "", args)
		want := runPython(t, py, script, root, "", args)
		same := got.code == want.code && got.flags == want.flags && got.jobs == want.jobs && got.failure == want.failure
		if !same {
			differing++
			t.Errorf("%s differs\nGo:\n%v\nPython:\n%v", m, got, want)
		}
		t.Logf("%s %s: %s", m[:10], map[bool]string{true: "same", false: "DIFFERENT"}[same], strings.ReplaceAll(strings.TrimSpace(got.flags), "\n", " "))
	}
	t.Logf("%d ranges, %d differences", len(merges), differing)
}
