package modules

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The cases of test_ci_modules_affected.py that this port keeps (plan step 18, Q8 (b)): TestRealRepository's table
// check, TestFlagChangeSet and the table error of TestAffectedModules. TestAffectedModules, TestGraph,
// TestMissReport, TestOracle and the matrix half of TestChangesCommand are dropped with the module matrix and
// `miss-report`.

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{
		"-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false", "-c", "core.autocrlf=false",
	}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, dir, name, text string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commit(t *testing.T, repo, name string) {
	t.Helper()
	writeFile(t, repo, name, name)
	gitIn(t, repo, "add", name)
	gitIn(t, repo, "commit", "-q", "-m", name)
}

func initRepo(t *testing.T, branch string) string {
	t.Helper()
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q", "-b", branch)
	return repo
}

const (
	makefileText = "ALL_GO_MOD_DIRS := $(shell find . \\( -path ./test/integration -o -path \"./distributions/*/build\" \\) -prune -o -name go.mod)\n"
	tableText    = `{"modules": {"core": {"test": "x"}, "tools/ci": {"test": "y"}}, "pseudo": {}}`
)

// fixtureRoot is a repository with the Makefile line, the table and two modules.
func fixtureRoot(t *testing.T) string {
	t.Helper()
	root := initRepo(t, "main")
	writeFile(t, root, "Makefile", makefileText)
	writeFile(t, root, ".github/scripts/ci-modules.json", tableText)
	writeFile(t, root, "core/go.mod", "module example.com/core\n")
	writeFile(t, root, "tools/ci/go.mod", "module example.com/tools/ci\n")
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-q", "-m", "base")
	return root
}

func runChanges(t *testing.T, root string, env map[string]string, args ...string) (code int, stdout, output, summary string) {
	t.Helper()
	t.Setenv("FULL_CI", "")
	for k, v := range env {
		t.Setenv(k, v)
	}
	dir := t.TempDir()
	out, sum := filepath.Join(dir, "out"), filepath.Join(dir, "summary.md")
	var so, se bytes.Buffer
	code = Run(append([]string{"--root", root, "changes", "--github-output", out, "--summary", sum}, args...), &so, &se)
	o, _ := os.ReadFile(out)
	s, _ := os.ReadFile(sum)
	return code, so.String(), string(o), string(s)
}

func allTrue() string { return FlagLines(Values(nil, true)) }

// Flags ------------------------------------------------------------------------------------------------------------

func TestFlagsFollowTheDiff(t *testing.T) { // test_workflows.py: test_flags_follow_the_diff
	on := func(flag, path string) bool {
		for i, f := range Flags {
			if f.Name == flag {
				return Values([]string{path}, false)[i]
			}
		}
		t.Fatalf("no flag %q", flag)
		return false
	}
	tests := []struct {
		flag  string
		paths []string
		want  bool
	}{
		{"upgrade", []string{"flagd/build.Dockerfile", "distributions/flagd-full/manifest.yaml", "builder/main.go",
			"test/upgrade/x_test.go", ".github/workflows/build.yaml"}, true},
		{"upgrade", []string{"core/pkg/evaluator/x.go", "service/ofrep/x.go", "sync/http/go.mod", "runtime/go.sum", "go.sum",
			"README.md", "docs/vision.md", "Makefile"}, false},
		{"reference", []string{"builder/reference/Dockerfile", "test/image/x_test.go", "flagd/build.Dockerfile"}, true},
		{"reference", []string{"core/pkg/x.go", "README.md", "test/upgrade/x_test.go"}, false},
		{"image", []string{"go.sum", "core/go.mod", "flagd/build.Dockerfile", "distributions/flagd-full/Dockerfile",
			".github/workflows/build.yaml"}, true},
		{"image", []string{"core/pkg/x.go", "README.md"}, false},
		{"go", []string{"a.go", "go.mod", "x/go.sum"}, true},
		{"go", []string{"a.go.txt", "mygo.mod", "agfo.sum"}, false},
		{"deps", []string{"go.mod", "a/b/go.sum"}, true},
		{"deps", []string{"a.go"}, false},
		{"dockerfile", []string{"Dockerfile", "a/b.Dockerfile", "a/Dockerfile"}, true},
		{"dockerfile", []string{"Dockerfile.md", "a/Dockerfiles"}, false},
	}
	for _, tt := range tests {
		for _, p := range tt.paths {
			t.Run(tt.flag+"/"+p, func(t *testing.T) {
				if got := on(tt.flag, p); got != tt.want {
					t.Errorf("flag %s for %q = %v, want %v", tt.flag, p, got, tt.want)
				}
			})
		}
	}
}

func TestToolsCISetsNoFlag(t *testing.T) { // test_workflows.py: the tools/ci rows
	set := func(path string) []string {
		var names []string
		for i, v := range Values([]string{path}, false) {
			if v {
				names = append(names, Flags[i].Name)
			}
		}
		return names
	}
	for _, p := range []string{"tools/ci/main.go", "tools/ci/go.mod", "tools/ci/go.sum", "tools/ci/internal/textio/x.go",
		"tools/ci/internal/modules/deeper/x.go", "tools/ci/internal/oci/x.go", "tools/ci/internal/publish/x.go", "tools/ci/Dockerfile"} {
		if got := set(p); len(got) != 0 {
			t.Errorf("%s sets %v, want none", p, got)
		}
	}
	for _, p := range []string{"tools/ci/internal/allocs/x.go", "tools/ci/internal/passsets/x.go"} {
		if got := set(p); len(got) != 0 {
			t.Errorf("%s sets %v, want none", p, got)
		}
	}
	for _, f := range Flags {
		if f.Exclude.String() != `^tools/ci/` {
			t.Errorf("flag %s excludes %q", f.Name, f.Exclude)
		}
	}
	if !slices.Contains(set("core/tools/ci/x.go"), "go") || !slices.Contains(set("go.mod"), "deps") {
		t.Error("the exclusion reaches a path outside tools/ci")
	}
}

func TestFlagOrderIsTheOutputOrder(t *testing.T) {
	var names []string
	for _, f := range Flags {
		names = append(names, f.Name)
	}
	want := []string{"go", "deps", "dockerfile", "image", "reference", "upgrade"}
	if !slices.Equal(names, want) {
		t.Errorf("flags = %v, want %v", names, want)
	}
	if got, want := FlagLines(Values([]string{"core/a.go"}, false)),
		"go=true\ndeps=false\ndockerfile=false\nimage=false\nreference=false\nupgrade=false\n"; got != want {
		t.Errorf("FlagLines = %q, want %q", got, want)
	}
}

func TestEveryFlagIsTrueWithoutADiffAndNoneForNothing(t *testing.T) { // test_workflows.py
	for _, v := range Values(nil, true) {
		if !v {
			t.Error("a flag is false with all set")
		}
	}
	for _, v := range Values(nil, false) {
		if v {
			t.Error("a flag is true for no changed path")
		}
	}
}

func TestPathSemantics(t *testing.T) { // translation hazards: $ before a final newline, ASCII classes, non-ASCII paths
	tests := []struct {
		name string
		path string
		flag string
		want bool
	}{
		{"a final newline still ends the path, as Python's $ does", "a.go\n", "go", true},
		{"only one newline", "a.go\n\n", "go", false},
		{"a non-ASCII directory", "dé/a.go", "go", true},
		{"a non-ASCII digit is not special: nothing in the patterns reads digits", "٣/go.mod", "deps", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for i, f := range Flags {
				if f.Name == tt.flag && Values([]string{tt.path}, false)[i] != tt.want {
					t.Errorf("flag %s for %q = %v, want %v", tt.flag, tt.path, !tt.want, tt.want)
				}
			}
		})
	}
}

// Prune list and module directories ---------------------------------------------------------------------------------

func TestPruneListAppliesToAncestors(t *testing.T) { // test_the_prune_list_applies_to_ancestors
	prune := []string{"./test/integration", "./distributions/*/build", "./spec"}
	files := []string{"core/go.mod", "test/integration/go.mod", "distributions/flagd-full/build/sub/go.mod", "spec/x/go.mod",
		"go.mod", "distributions/flagd-full/go.mod"}
	got := ModuleDirs(files, prune)
	if want := []string{"core", "distributions/flagd-full"}; !slices.Equal(got, want) {
		t.Errorf("ModuleDirs = %v, want %v", got, want)
	}
}

func TestFnmatchStarCrossesSlashes(t *testing.T) { // fnmatch `*` matches `/`; path.Match does not
	tests := []struct {
		name, pattern string
		want          bool
	}{
		{"./distributions/flagd-full/build", "./distributions/*/build", true},
		{"./distributions/a/b/build", "./distributions/*/build", true},
		{"./distributions/build", "./distributions/*/build", false},
		{"./spec", "./spec", true},
		{"./specs", "./spec", false},
		{"./a1", "./a[0-9]", true},
		{"./ab", "./a[!0-9]", true},
		{"./a1", "./a[!0-9]", false},
		{"./a?", "./a[", false},
		{"./ab", "./a?", true},
		{"./a.b", "./a.b", true},
		{"./axb", "./a.b", false},
	}
	for _, tt := range tests {
		if got := Fnmatch(tt.name, tt.pattern); got != tt.want {
			t.Errorf("Fnmatch(%q, %q) = %v, want %v", tt.name, tt.pattern, got, tt.want)
		}
	}
}

func TestParsePruneTerms(t *testing.T) {
	got, err := ParsePrune("X := 1\nALL_GO_MOD_DIRS := $(shell find . \\( -path ./a -o -path \"./b/*/c\" -o -path  ./d \\) -prune)\n")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"./a", "./b/*/c", "./d"}; !slices.Equal(got, want) {
		t.Errorf("ParsePrune = %v, want %v", got, want)
	}
	if _, err := ParsePrune("X := 1\n"); err == nil {
		t.Error("a Makefile without the line must fail")
	}
}

// The table ----------------------------------------------------------------------------------------------------------

func TestCheckTable(t *testing.T) { // test_a_directory_without_a_table_entry_fails
	dirs := []string{"core", "sync/sse", "builder"}
	err := CheckTable(dirs, `{"modules": {"core": {}, "builder": {}}}`)
	var te *TableError
	if !errors.As(err, &te) || !slices.Equal(te.Missing, []string{"sync/sse"}) || !strings.Contains(err.Error(), "sync/sse") {
		t.Errorf("error = %v, want a table error naming sync/sse", err)
	}
	err = CheckTable([]string{"b", "a"}, `{"modules": {}}`)
	if !errors.As(err, &te) || !slices.Equal(te.Missing, []string{"a", "b"}) {
		t.Errorf("missing = %v, want the sorted names", err)
	}
	if err := CheckTable(dirs, `{"modules": {"core": {}, "builder": {}, "sync/sse": {}, "extra": {}}}`); err != nil {
		t.Errorf("a complete table fails: %v", err)
	}
	for _, bad := range []string{`{`, ``, `{"pseudo": {}}`, `[]`} {
		if err := CheckTable(dirs, bad); err == nil || errors.As(err, &te) {
			t.Errorf("CheckTable(%q) = %v, want a plain error that falls open", bad, err)
		}
	}
}

func TestEveryModuleIsInTheTable(t *testing.T) { // TestRealRepository.test_every_module_is_in_the_table
	root := repoRoot(t)
	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	prune, err := ParsePrune(string(makefile))
	if err != nil {
		t.Fatal(err)
	}
	table, err := os.ReadFile(filepath.Join(root, ".github", "scripts", "ci-modules.json"))
	if err != nil {
		t.Fatal(err)
	}
	files, err := ListFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckTable(ModuleDirs(files, prune), string(table)); err != nil {
		t.Error(err)
	}
}

// Base selection ------------------------------------------------------------------------------------------------------

func mergeFixture(t *testing.T) (repo, base, merge string) {
	t.Helper()
	repo = initRepo(t, "main")
	commit(t, repo, "a.txt")
	base = gitIn(t, repo, "rev-parse", "HEAD")
	gitIn(t, repo, "checkout", "-q", "-b", "pr")
	commit(t, repo, "from-pr.go")
	gitIn(t, repo, "checkout", "-q", "main")
	commit(t, repo, "later-on-main.txt")
	gitIn(t, repo, "merge", "-q", "--no-ff", "-m", "merge", "pr")
	return repo, base, gitIn(t, repo, "rev-parse", "HEAD")
}

func TestChangeSet(t *testing.T) { // TestFlagChangeSet and the base rows of plan M6
	repo, _, merge := mergeFixture(t)
	parent := gitIn(t, repo, "rev-parse", merge+"^1")
	feature := initRepo(t, "feature")
	commit(t, feature, "one")
	commit(t, feature, "two")
	before, after := gitIn(t, feature, "rev-parse", "HEAD~1"), gitIn(t, feature, "rev-parse", "HEAD")

	tests := []struct {
		name                    string
		root, event, sha, befor string
		fullCI                  string
		want                    []string
		all                     bool
	}{
		{name: "a pull request is its merge commit against the first parent", root: repo, event: "pull_request", sha: merge, want: []string{"from-pr.go"}},
		{name: "a pull request ignores before", root: repo, event: "pull_request", sha: merge, befor: "1111111111111111111111111111111111111111", want: []string{"from-pr.go"}},
		{name: "a push to main with a usable before is the diff, not everything", root: repo, event: "push", sha: merge, befor: parent,
			want: []string{"from-pr.go"}},
		{name: "a push is before to after", root: feature, event: "push", sha: after, befor: before, want: []string{"two"}},
		{name: "a push with whitespace around before", root: feature, event: "push", sha: after, befor: " " + before + "\n", want: []string{"two"}},
		{name: "a push with an empty before", root: feature, event: "push", sha: after, all: true},
		{name: "a push with the zero before", root: feature, event: "push", sha: after, befor: Zero, all: true},
		{name: "a push with an unknown before", root: feature, event: "push", sha: after, befor: strings.Repeat("1", 40), all: true},
		{name: "a pull request whose commit has no first parent", root: feature, event: "pull_request",
			sha: gitIn(t, feature, "rev-list", "--max-parents=0", "HEAD"), all: true},
		{name: "FULL_CI on a push", root: feature, event: "push", sha: after, befor: before, fullCI: "true", all: true},
		{name: "FULL_CI on a pull request", root: repo, event: "pull_request", sha: merge, fullCI: "true", all: true},
		{name: "FULL_CI other than true is not set", root: feature, event: "push", sha: after, befor: before, fullCI: "1", want: []string{"two"}},
		{name: "a manual run", root: feature, event: "workflow_dispatch", sha: after, befor: before, all: true},
		{name: "the schedule", root: feature, event: "schedule", sha: after, befor: before, all: true},
		{name: "an event nobody listed", root: feature, event: "release", sha: after, all: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, all, err := ChangeSet(tt.root, tt.event, tt.sha, tt.befor, tt.fullCI)
			if err != nil {
				t.Fatal(err)
			}
			if all != tt.all || !slices.Equal(got, tt.want) {
				t.Errorf("ChangeSet = %v, all=%v; want %v, all=%v", got, all, tt.want, tt.all)
			}
		})
	}
}

func TestTheTwoDotDiffWouldHoldTheLaterCommitOnMain(t *testing.T) { // the point of the first-parent base
	repo, base, merge := mergeFixture(t)
	twoDot := gitIn(t, repo, "diff", "--name-only", base, merge)
	if !strings.Contains(twoDot, "later-on-main.txt") {
		t.Fatalf("fixture: the two-dot diff = %q", twoDot)
	}
	got, _, err := ChangeSet(repo, "pull_request", merge, "", "")
	if err != nil || slices.Contains(got, "later-on-main.txt") {
		t.Errorf("ChangeSet = %v, %v", got, err)
	}
}

func TestChangeSetFailsOnAnUnknownHead(t *testing.T) {
	feature := initRepo(t, "feature")
	commit(t, feature, "one")
	before := gitIn(t, feature, "rev-parse", "HEAD")
	if _, _, err := ChangeSet(feature, "push", strings.Repeat("2", 40), before, ""); err == nil {
		t.Error("git diff against an unknown sha must be an error, which falls open")
	}
}

// The command --------------------------------------------------------------------------------------------------------

func TestChangesWritesTheFlagsAndTheSummary(t *testing.T) { // test_the_changes_command_writes_the_flags_...
	root := fixtureRoot(t)
	code, stdout, output, summary := runChanges(t, root, nil, "--event", "workflow_dispatch", "--sha", "HEAD")
	if code != 0 || output != allTrue() {
		t.Errorf("code=%d output=%q", code, output)
	}
	if !strings.HasPrefix(summary, "## Jobs this change calls for\n\n- `go`: yes\n- `deps`: yes\n") || !strings.HasSuffix(summary, "- `upgrade`: yes\n") {
		t.Errorf("summary = %q", summary)
	}
	if stdout != summary+"\n" {
		t.Errorf("stdout = %q, want the summary and a newline", stdout)
	}
}

func TestChangesOnAPullRequest(t *testing.T) {
	root := fixtureRoot(t)
	gitIn(t, root, "checkout", "-q", "-b", "pr")
	commit(t, root, "core/x.go")
	gitIn(t, root, "checkout", "-q", "main")
	gitIn(t, root, "merge", "-q", "--no-ff", "-m", "merge", "pr")
	merge := gitIn(t, root, "rev-parse", "HEAD")
	code, _, output, summary := runChanges(t, root, nil, "--event", "pull_request", "--sha", merge, "--ref", "refs/pull/1/merge")
	want := "go=true\ndeps=false\ndockerfile=false\nimage=false\nreference=false\nupgrade=false\n"
	if code != 0 || output != want {
		t.Errorf("code=%d output=%q, want %q", code, output, want)
	}
	if !strings.Contains(summary, "- `deps`: no\n") {
		t.Errorf("summary = %q", summary)
	}
}

func TestChangesAppendsToExistingFiles(t *testing.T) {
	root := fixtureRoot(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	if err := os.WriteFile(out, []byte("earlier=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var so, se bytes.Buffer
	t.Setenv("FULL_CI", "")
	if code := Run([]string{"--root", root, "changes", "--event", "schedule", "--sha", "HEAD", "--github-output", out}, &so, &se); code != 0 {
		t.Fatal(se.String())
	}
	got, _ := os.ReadFile(out)
	if string(got) != "earlier=1\n"+allTrue() {
		t.Errorf("output = %q", got)
	}
}

func TestFullCIComesFromTheEnvironment(t *testing.T) {
	root := fixtureRoot(t)
	commit(t, root, "README.md")
	head := gitIn(t, root, "rev-parse", "HEAD")
	before := gitIn(t, root, "rev-parse", "HEAD~1")
	_, _, output, _ := runChanges(t, root, nil, "--event", "push", "--sha", head, "--before", before)
	if strings.Contains(output, "true") {
		t.Errorf("a markdown change sets %q", output)
	}
	_, _, output, _ = runChanges(t, root, map[string]string{"FULL_CI": "true"}, "--event", "push", "--sha", head, "--before", before)
	if output != allTrue() {
		t.Errorf("FULL_CI=true gives %q", output)
	}
}

// Exit codes and fail-open (plan M2): one row per error source.
func TestEveryErrorButTheTableErrorFallsOpen(t *testing.T) {
	tests := []struct {
		name   string
		break_ func(t *testing.T, root string)
		args   []string
	}{
		{"the Makefile has no ALL_GO_MOD_DIRS line", func(t *testing.T, r string) { writeFile(t, r, "Makefile", "all:\n") },
			[]string{"--event", "workflow_dispatch", "--sha", "HEAD"}},
		{"the Makefile is missing", func(t *testing.T, r string) { _ = os.Remove(filepath.Join(r, "Makefile")) },
			[]string{"--event", "workflow_dispatch", "--sha", "HEAD"}},
		{"ci-modules.json is broken", func(t *testing.T, r string) { writeFile(t, r, ".github/scripts/ci-modules.json", "{") },
			[]string{"--event", "workflow_dispatch", "--sha", "HEAD"}},
		{"ci-modules.json has no modules", func(t *testing.T, r string) { writeFile(t, r, ".github/scripts/ci-modules.json", "{}") },
			[]string{"--event", "workflow_dispatch", "--sha", "HEAD"}},
		{"ci-modules.json is missing", func(t *testing.T, r string) { _ = os.Remove(filepath.Join(r, ".github/scripts/ci-modules.json")) },
			[]string{"--event", "workflow_dispatch", "--sha", "HEAD"}},
		{"git cannot diff an unknown head of a push", func(t *testing.T, r string) {},
			[]string{"--event", "push", "--sha", strings.Repeat("3", 40), "--before", "HEAD"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := fixtureRoot(t)
			tt.break_(t, root)
			code, stdout, output, summary := runChanges(t, root, nil, tt.args...)
			if code != 0 {
				t.Fatalf("exit code = %d, want 0", code)
			}
			if output != allTrue() {
				t.Errorf("output = %q, want every flag true and no other line", output)
			}
			if !strings.HasPrefix(stdout, "::warning::relay-ci failed (") || !strings.Contains(stdout, "every gated job runs") {
				t.Errorf("stdout = %q", stdout)
			}
			if !strings.HasPrefix(summary, "## Jobs this change calls for\n\nAll: relay-ci failed with `") {
				t.Errorf("summary = %q", summary)
			}
		})
	}
}

func TestAPanicFallsOpen(t *testing.T) {
	root := fixtureRoot(t)
	gitIn(t, root, "checkout", "-q", "-b", "pr")
	commit(t, root, "core/x.go")
	gitIn(t, root, "checkout", "-q", "main")
	gitIn(t, root, "merge", "-q", "--no-ff", "-m", "merge", "pr")
	merge := gitIn(t, root, "rev-parse", "HEAD")
	saved := Flags
	t.Cleanup(func() { Flags = saved })
	Flags = []Flag{{Name: "go"}} // a flag without expressions panics when it is evaluated
	code, stdout, output, _ := runChanges(t, root, nil, "--event", "pull_request", "--sha", merge)
	if code != 0 || output != "go=true\n" || !strings.Contains(stdout, "relay-ci failed (panic:") {
		t.Errorf("code=%d output=%q stdout=%q", code, output, stdout)
	}
}

func TestATableErrorFails(t *testing.T) { // test_a_table_error_still_fails
	root := fixtureRoot(t)
	writeFile(t, root, "extra/go.mod", "module example.com/extra\n")
	writeFile(t, root, "another/go.mod", "module example.com/another\n")
	code, stdout, output, summary := runChanges(t, root, nil, "--event", "workflow_dispatch", "--sha", "HEAD")
	if code != 1 || stdout != "::error::no entry in ci-modules.json for: another, extra\n" || output != "" || summary != "" {
		t.Errorf("code=%d stdout=%q output=%q summary=%q", code, stdout, output, summary)
	}
}

func TestPrunedModulesNeedNoEntry(t *testing.T) {
	root := fixtureRoot(t)
	writeFile(t, root, "test/integration/go.mod", "module example.com/i\n")
	writeFile(t, root, "distributions/x/build/go.mod", "module example.com/b\n")
	code, _, output, _ := runChanges(t, root, nil, "--event", "schedule", "--sha", "HEAD")
	if code != 0 || output != allTrue() {
		t.Errorf("code=%d output=%q", code, output)
	}
}

func TestCRLFFilesAreRead(t *testing.T) { // a Windows checkout holds CRLF in the Makefile and the table
	root := fixtureRoot(t)
	writeFile(t, root, "Makefile", strings.ReplaceAll(makefileText, "\n", "\r\n"))
	writeFile(t, root, ".github/scripts/ci-modules.json", strings.ReplaceAll(`{
  "modules": {"core": {"test": null, "reason": "r"}, "tools/ci": {"test": null, "reason": "r"}}
}`, "\n", "\r\n"))
	code, stdout, output, _ := runChanges(t, root, nil, "--event", "schedule", "--sha", "HEAD")
	if code != 0 || output != allTrue() || strings.Contains(stdout, "::warning") {
		t.Errorf("code=%d output=%q stdout=%q", code, output, stdout)
	}
}

func TestWithoutOutputFilesNothingIsWritten(t *testing.T) {
	root := fixtureRoot(t)
	var so, se bytes.Buffer
	t.Setenv("FULL_CI", "")
	if code := Run([]string{"--root", root, "changes", "--event", "schedule", "--sha", "HEAD"}, &so, &se); code != 0 {
		t.Fatal(se.String())
	}
	if !strings.HasPrefix(so.String(), "## Jobs this change calls for") {
		t.Errorf("stdout = %q", so.String())
	}
}

func TestAnUnwritableOutputIsAnErrorTheWorkflowCoversForAndTheTableErrorStillFails(t *testing.T) {
	root := fixtureRoot(t)
	var so, se bytes.Buffer
	t.Setenv("FULL_CI", "")
	code := Run([]string{"--root", root, "changes", "--event", "schedule", "--sha", "HEAD",
		"--github-output", filepath.Join(root, "no", "such", "dir", "out")}, &so, &se)
	if code != 2 {
		t.Errorf("code = %d, want 2 (neither decided output nor fallback could be written)", code)
	}
}

func TestUsage(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"no arguments", nil},
		{"another subcommand", []string{"matrix"}},
		{"no event", []string{"changes", "--sha", "x"}},
		{"no sha", []string{"changes", "--event", "push"}},
		{"an unknown flag", []string{"changes", "--event", "push", "--sha", "x", "--nope"}},
		{"a stray argument", []string{"changes", "--event", "push", "--sha", "x", "extra"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var so, se bytes.Buffer
			if code := Run(tt.args, &so, &se); code != 2 {
				t.Errorf("code = %d, want 2", code)
			}
			if se.Len() == 0 {
				t.Error("no message on stderr")
			}
		})
	}
}

func TestListFilesSkipsGit(t *testing.T) {
	root := fixtureRoot(t)
	files, err := ListFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasPrefix(f, ".git/") {
			t.Errorf("ListFiles lists %s", f)
		}
	}
	if !slices.Contains(files, "core/go.mod") {
		t.Errorf("files = %v", files)
	}
}
