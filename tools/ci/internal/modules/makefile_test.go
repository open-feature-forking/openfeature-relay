package modules

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/tools/ci/internal/textio"
)

// The port of test_ci_modules.py (step 15, slice C): the module table and the Makefile targets that CI's per-module
// matrix runs. `make -n` reads the Makefile; the tests skip where there is no make, and fail on a skip in CI.

// repoRoot is the repository root: the nearest parent that holds the Makefile and the table.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "Makefile")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, ".github", "scripts", "ci-modules.json")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			skip(t, "the repository root is not above the working directory")
		}
		dir = parent
	}
}

// skip skips a test, except in CI, where a skipped check is a check that silently stopped running.
func skip(t *testing.T, why string) {
	t.Helper()
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		t.Fatalf("would skip in CI: %s", why)
	}
	t.Skip(why)
}

func readTable(t *testing.T, root string) (modules map[string]map[string]any, pseudo map[string]map[string]any) {
	t.Helper()
	text, err := textio.ReadText(filepath.Join(root, ".github", "scripts", "ci-modules.json"))
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		Modules map[string]map[string]any `json:"modules"`
		Pseudo  map[string]map[string]any `json:"pseudo"`
	}
	if err := json.Unmarshal([]byte(text), &table); err != nil {
		t.Fatal(err)
	}
	return table.Modules, table.Pseudo
}

func readMakefile(t *testing.T, root string) string {
	t.Helper()
	text, err := textio.ReadText(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	return text
}

// makeDryRun is the recipe lines `make -n <args>` prints, without the directory chatter of recursive makes. The child
// make does not inherit the flags of the make that runs `make test` (MAKEFLAGS would turn -n into a no-op marker).
func makeDryRun(t *testing.T, root string, args ...string) []string {
	t.Helper()
	if _, err := exec.LookPath("make"); err != nil {
		skip(t, "make is not available")
	}
	cmd := exec.Command("make", append([]string{"-n"}, args...)...)
	cmd.Dir = root
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "MAKEFLAGS=") && !strings.HasPrefix(e, "MFLAGS=") && !strings.HasPrefix(e, "MAKELEVEL=") {
			env = append(env, e)
		}
	}
	cmd.Env = env
	out, _ := cmd.Output()
	var lines []string
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "make[") {
			lines = append(lines, l)
		}
	}
	return lines
}

// goModuleDirs is ALL_GO_MOD_DIRS: the directories holding a go.mod, minus the Makefile's prune list.
func goModuleDirs(t *testing.T, root string) []string {
	t.Helper()
	prune, err := ParsePrune(readMakefile(t, root))
	if err != nil {
		t.Fatal(err)
	}
	files, err := ListFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	return ModuleDirs(files, prune)
}

func coverCmd(module string) string {
	return fmt.Sprintf("go test -race -covermode=atomic -cover -short ./%s/... -coverprofile=%s-coverage.out", module, strings.ReplaceAll(module, "/", "-"))
}

func plainCmd(module string) string {
	return fmt.Sprintf("go test -race -covermode=atomic -cover -short -count=1 ./%s/... -coverprofile=%s-coverage.out", module, strings.ReplaceAll(module, "/", "-"))
}

func testLines(lines []string) []string {
	var out []string
	for _, l := range lines {
		if strings.HasPrefix(l, "go test") || strings.HasPrefix(l, "cd ") {
			out = append(out, l)
		}
	}
	return out
}

// normalised maps flags to packages, so that one go test over two modules equals two over one.
func normalised(lines []string) map[string][]string {
	out := map[string][]string{}
	for _, l := range testLines(lines) {
		if !strings.HasPrefix(l, "go test") {
			out[l] = out[l]
			continue
		}
		var flags []string
		var pkgs []string
		for _, tok := range strings.Fields(l) {
			if strings.HasPrefix(tok, "./") && strings.HasSuffix(tok, "/...") {
				pkgs = append(pkgs, tok)
			} else {
				flags = append(flags, tok)
			}
		}
		key := strings.Join(flags, " ")
		out[key] = append(out[key], pkgs...)
	}
	for k := range out {
		slices.Sort(out[k])
		out[k] = slices.Compact(out[k])
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }

func TestTableCoversModules(t *testing.T) { // C1
	root := repoRoot(t)
	modules, pseudo := readTable(t, root)

	t.Run("every module has an entry", func(t *testing.T) { // test_every_module_has_an_entry
		if got, want := sortedKeys(modules), goModuleDirs(t, root); !slices.Equal(got, want) {
			t.Errorf("table = %v\nmodules = %v", got, want)
		}
	})

	t.Run("entries are a command or a reason", func(t *testing.T) { // test_entries_are_a_command_or_a_reason
		for d, e := range modules {
			_, hasReason := e["reason"]
			_, hasAlways := e["always"]
			if e["test"] == nil {
				if r, _ := e["reason"].(string); r == "" || hasAlways {
					t.Errorf("%s: no command needs a reason and no always: %v", d, e)
				}
			} else if e["test"] != "make test-module MODULE="+d || hasReason {
				t.Errorf("%s: %v", d, e)
			}
		}
	})

	t.Run("always and coverage sets", func(t *testing.T) { // test_always_and_coverage_sets
		always := map[string]bool{}
		covered := map[string]bool{}
		withTest := map[string]bool{}
		for d, e := range modules {
			if e["always"] == true {
				always[d] = true
			}
			if e["coverage"] == true {
				covered[d] = true
			}
			if e["test"] != nil {
				withTest[d] = true
			}
		}
		if got, want := sortedKeys(always), []string{"builder", "test/image", "test/release", "test/upgrade"}; !slices.Equal(got, want) {
			t.Errorf("always = %v, want %v", got, want)
		}
		if len(pseudo) != 1 || pseudo["contract-external"]["test"] != "make test-contract-external" || pseudo["contract-external"]["always"] != true ||
			len(pseudo["contract-external"]) != 2 {
			t.Errorf("pseudo = %v", pseudo)
		}
		// every module with a go test recipe leaves a profile, which the coverage job compares with the base (#208)
		for _, d := range []string{"test/distributions", "test/upgrade", "test/image", "test/concurrency"} {
			delete(withTest, d)
		}
		if got, want := sortedKeys(covered), sortedKeys(withTest); !slices.Equal(got, want) {
			t.Errorf("coverage = %v, want %v", got, want)
		}
	})

	t.Run("module recipes exist exactly for the entries with a command", func(t *testing.T) {
		for _, d := range sortedKeys(modules) {
			lines := makeDryRun(t, root, "test-module", "MODULE="+d)
			if modules[d]["test"] == nil {
				if !slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, "no test recipe") }) {
					t.Errorf("%s: %v", d, lines)
				}
				continue
			}
			got := testLines(lines)
			if len(got) != 1 {
				t.Errorf("%s: %v", d, lines)
				continue
			}
			if want := modules[d]["coverage"] == true; want != strings.Contains(got[0], "-coverprofile=") {
				t.Errorf("%s: coverage %v, recipe %v", d, want, got[0])
			}
		}
	})
}

func TestModuleIsTakenWithOrWithoutDotSlash(t *testing.T) {
	root := repoRoot(t)
	if a, b := makeDryRun(t, root, "test-module", "MODULE=core"), makeDryRun(t, root, "test-module", "MODULE=./core"); !slices.Equal(a, b) {
		t.Errorf("%v != %v", a, b)
	}
}

func TestLoopingTargets(t *testing.T) {
	root := repoRoot(t)
	lib := []string{"core", "sync/file", "sync/flagd-grpc", "sync/http", "sync/openfeature-operator", "sync/gcs", "sync/azblob",
		"sync/s3", "sync/sse", "sync/builder", "telemetry/otel", "eventsink/webhook", "evaluator/flagd", "configformat/flagd"}
	flagd := []string{"flagd", "runtime", "service/transport", "service/flag-evaluation", "service/flag-evaluation-legacy", "service/ofrep", "service/flag-sync",
		"service/event-collector"}
	contrib := []string{"contrib/flagsmith/engine", "contrib/flagsmith/middleware", "contrib/flagsmith/sync", "testbed/control",
		"testbed/source", "testbed/cmd"}
	for target, modules := range map[string][]string{"test-modules": lib, "test-flagd": flagd, "test-contrib": contrib} {
		t.Run(target, func(t *testing.T) {
			var want []string
			for _, m := range modules {
				want = append(want, coverCmd(m))
			}
			if got := testLines(makeDryRun(t, root, target)); !slices.Equal(got, want) {
				t.Errorf("got  %v\nwant %v", got, want)
			}
		})
	}
}

func TestTheOtherModulesKeepTheirFlags(t *testing.T) {
	root := repoRoot(t)
	want := map[string]string{
		"flagd-proxy":        "go test -race -covermode=atomic -cover -short ./flagd-proxy/... -coverprofile=flagd-proxy-coverage.out",
		"test/distributions": "cd test/distributions && go test -count=1 -timeout 30m ./...",
		"test/upgrade":       "cd test/upgrade && go test -count=1 -run 'TestAcceptedMatchesIssue|TestSurfaces|TestGuideCoversAcceptedItems' ./...",
		"test/image":         "cd test/image && go test -count=1 -run 'TestReferenceDockerfilePins|TestTestbedDockerfilePins' ./...",
	}
	for _, m := range []string{"builder", "test/release", "test/service-example", "test/sync-example", "test/evaluator-testkit",
		"test/configformat-example", "test/configformat-example/distribution", "test/eventsink-example",
		"test/eventsink-example/distribution", "test/middleware-example", "test/middleware-example/distribution",
		"test/telemetry-example", "test/telemetry-example/distribution", "test/inprocess-example/file",
		"test/inprocess-example/filegrpc", "test/inprocess-example/engine", "test/porttest", "tools/ci"} {
		want[m] = plainCmd(m)
	}
	for _, m := range sortedKeys(want) {
		t.Run(m, func(t *testing.T) {
			if got := testLines(makeDryRun(t, root, "test-module", "MODULE="+m)); !slices.Equal(got, []string{want[m]}) {
				t.Errorf("got %v, want %v", got, want[m])
			}
		})
	}
}

func TestSingleModuleTargetsRunWhatTestModuleRuns(t *testing.T) {
	root := repoRoot(t)
	parity := map[string][]string{
		"test-flagd-proxy":          {"flagd-proxy"},
		"test-builder":              {"builder"},
		"test-distributions":        {"test/distributions"},
		"test-upgrade-accepted":     {"test/upgrade"},
		"test-image-pins":           {"test/image"},
		"test-release":              {"test/release"},
		"test-porttest":             {"test/porttest"},
		"test-tools-ci":             {"tools/ci"},
		"test-service-example":      {"test/service-example"},
		"test-sync-example":         {"test/sync-example"},
		"test-evaluator-testkit":    {"test/evaluator-testkit"},
		"test-configformat-example": {"test/configformat-example", "test/configformat-example/distribution"},
		"test-eventsink-example":    {"test/eventsink-example", "test/eventsink-example/distribution"},
		"test-middleware-example":   {"test/middleware-example", "test/middleware-example/distribution"},
		"test-telemetry-example":    {"test/telemetry-example", "test/telemetry-example/distribution"},
		"test-inprocess-example":    {"test/inprocess-example/file", "test/inprocess-example/filegrpc", "test/inprocess-example/engine"},
	}
	for _, target := range sortedKeys(parity) {
		t.Run(target, func(t *testing.T) {
			var via []string
			for _, m := range parity[target] {
				via = append(via, testLines(makeDryRun(t, root, "test-module", "MODULE="+m))...)
			}
			a, b := normalised(via), normalised(makeDryRun(t, root, target))
			if !maps.EqualFunc(a, b, slices.Equal[[]string]) {
				t.Errorf("test-module gives %v, %s gives %v", a, target, b)
			}
		})
	}
}

func TestMakeTestRunsTheSameTargets(t *testing.T) {
	root := repoRoot(t)
	var got []string
	for _, m := range regexp.MustCompile(`(?m)^TEST_TARGETS \+= (\S+)`).FindAllStringSubmatch(readMakefile(t, root), -1) {
		got = append(got, m[1])
	}
	want := []string{"test-core", "test-flagd", "test-flagd-proxy", "test-contrib", "test-builder", "test-distributions",
		"test-upgrade-accepted", "test-image-pins", "test-configformat-example", "test-service-example",
		"test-evaluator-testkit", "test-inprocess-example", "test-sync-example", "test-telemetry-example",
		"test-eventsink-example", "test-contract-external", "test-middleware-example", "test-release",
		"test-concurrency", "test-porttest", "test-tools-ci"}
	if !slices.Equal(got, want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}
}

// Coverage gap 3: a directory with a _test.go file that no recipe reaches runs nowhere (flagd-proxy/cmd did).

// elsewhere are the directories that `make test` does not reach, each with the CI job or the reason that does.
var elsewhere = map[string]string{
	"test/integration": "the integration-test job",
	"test/testbed":     "the testbed jobs (make testbed-contract, testbed-suites, testbed-rpc-outages, testbed-grpc-sync)",
}

func trackedTestDirs(t *testing.T, root string) map[string]bool {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "-z", "--", "*_test.go")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		skip(t, "git cannot list the files")
	}
	dirs := map[string]bool{}
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			dirs[filepath.ToSlash(filepath.Dir(p))] = true
		}
	}
	return dirs
}

// ownerOf is the module directory that holds path: the longest module directory that is a prefix of it.
func ownerOf(path string, modules []string) string {
	best := ""
	for _, m := range modules {
		if (path == m || strings.HasPrefix(path, m+"/")) && len(m) > len(best) {
			best = m
		}
	}
	return best
}

func TestEveryDirectoryWithATestIsReachedByMakeTest(t *testing.T) {
	root := repoRoot(t)
	modules := goModuleDirs(t, root)
	type reach struct{ base string }
	var reaches []reach
	cd := regexp.MustCompile(`^cd (\S+) && `)
	for _, line := range makeDryRun(t, root, "test") {
		if !strings.HasPrefix(line, "go test") && !strings.HasPrefix(line, "cd ") {
			continue
		}
		prefix := ""
		if m := cd.FindStringSubmatch(line); m != nil {
			prefix, line = m[1], line[len(m[0]):]
		}
		for _, tok := range strings.Fields(line) {
			if !strings.HasPrefix(tok, "./") || !strings.HasSuffix(tok, "/...") {
				continue
			}
			pattern := tok
			if prefix != "" {
				pattern = "./" + prefix + "/..."
				if tok != "./..." {
					pattern = "./" + prefix + "/" + tok[2:]
				}
			}
			reaches = append(reaches, reach{strings.TrimRight(pattern[2:len(pattern)-4], "/")})
		}
	}
	reached := func(d string) bool {
		return slices.ContainsFunc(reaches, func(r reach) bool {
			return (d == r.base || strings.HasPrefix(d, r.base+"/")) && ownerOf(d, modules) == ownerOf(r.base, modules)
		})
	}
	var missed []string
	for d := range trackedTestDirs(t, root) {
		if _, ok := elsewhere[d]; !ok && !reached(d) {
			missed = append(missed, d)
		}
	}
	slices.Sort(missed)
	if len(missed) > 0 {
		t.Errorf("no recipe of `make test` runs the tests in these directories: %v", missed)
	}
}

func TestTheDirectoriesRunElsewhereHaveTests(t *testing.T) {
	root := repoRoot(t)
	tracked := trackedTestDirs(t, root)
	for d := range elsewhere {
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(d))); !tracked[d] && (err != nil || !info.IsDir()) {
			t.Errorf("%s has no tests and is no directory", d)
		}
	}
}

func TestFlagdProxyCmdIsReached(t *testing.T) {
	root := repoRoot(t)
	lines := testLines(makeDryRun(t, root, "test-flagd-proxy"))
	if len(lines) != 1 || !strings.Contains(lines[0], "./flagd-proxy/...") {
		t.Errorf("lines = %v", lines)
	}
}

func TestLintCoversEveryModule(t *testing.T) { // C2
	root := repoRoot(t)
	lines := makeDryRun(t, root, "lint-modules", "GOPATH=/gopath")
	var got []string
	for _, m := range regexp.MustCompile(`/gopath/bin/golangci-lint run \./(\S+)/\.\.\. \|\| rc=1;`).FindAllStringSubmatch(strings.Join(lines, " "), -1) {
		got = append(got, m[1])
	}
	slices.Sort(got)
	if want := goModuleDirs(t, root); !slices.Equal(got, want) {
		t.Errorf("lint covers %v, modules %v", got, want)
	}
}

func TestLintIsTheGeneratedCheckTheInstallAndLintModules(t *testing.T) {
	root := repoRoot(t)
	text := strings.Join(makeDryRun(t, root, "lint", "GOPATH=/gopath"), "\n")
	prev := -1
	for _, s := range []string{"check-flagd-full-generated",
		"go install -v github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0", "golangci-lint run ./builder/..."} {
		i := strings.Index(text, s)
		if i < 0 || i < prev {
			t.Fatalf("%q at %d after %d in\n%s", s, i, prev, text)
		}
		prev = i
	}
}
