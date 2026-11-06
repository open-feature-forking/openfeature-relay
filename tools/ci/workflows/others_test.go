package workflows

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// The ports of ReleaseConfiguration, WorkflowPermissions, SpecLintWorkflow, DocsCheckWorkflow, NoEmbeddedMarkdown and
// BuildDockerfiles in test_workflows.py (K2 and K3).

// sha256Of is the hash of a repository file with line endings normalised to LF. A change to a pinned file is
// deliberate: record the new hash in the same commit.
func sha256Of(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(strings.ReplaceAll(string(data), "\r\n", "\n")))
	return hex.EncodeToString(sum[:])
}

// releasePinned: K2, flagd and flagd-proxy release exactly as today.
var releasePinned = map[string]string{
	".github/workflows/release-please.yaml": "7bd0c065fbc00d294c5988aa83b31389f8fa5b3baf1f673f1b8be02bd08b4f0d",
	"release-please-config.json":            "f7f5af2d67434c7a7565fa88cd1cd09b5ad29e7f6bf93dbb3fd57e54afc24c97",
	".release-please-manifest.json":         "4617b2ca55b05124c5c270d1e66184912125b461d6c24ee0d90ec5da142ec6ee",
}

func TestReleaseFilesAreUnchanged(t *testing.T) { // ReleaseConfiguration.test_release_files_are_unchanged
	for path, want := range releasePinned {
		t.Run(path, func(t *testing.T) {
			if got := sha256Of(t, path); got != want {
				t.Errorf("sha256 = %s, want %s", got, want)
			}
		})
	}
}

// K3: the Dockerfiles of flagd and flagd-proxy. A new hash for flagd-proxy means re-recording its image configuration.
var dockerfilesPinned = map[string]string{
	"flagd/build.Dockerfile":       "b95c00f2da32a69b9132c167051b8b435748029c5e732725b14a5b561604f65e",
	"flagd-proxy/build.Dockerfile": "19cbf8fb4450db31b5cbad5dc15fbc29fe3f9cfcab21f5d269e8bfb214207b2a",
}

func TestDockerfilesAreUnchanged(t *testing.T) { // BuildDockerfiles.test_dockerfiles_are_unchanged
	for path, want := range dockerfilesPinned {
		t.Run(path, func(t *testing.T) {
			if got := sha256Of(t, path); got != want {
				t.Errorf("sha256 = %s, want %s", got, want)
			}
		})
	}
}

// ---------------------------------------------------------------------------------------------------------------------
// WorkflowPermissions: the repository's default token may write, so every workflow narrows it: a top-level block that
// reads, and job-level grants only where a job calls an API that needs them. Nothing grants packages: write.

// workflowDocs parses every workflow of the repository, by file name.
func workflowDocs(t *testing.T) (files []string, docs map[string]map[string]any) {
	t.Helper()
	docs = map[string]map[string]any{}
	for _, f := range workflowFiles(t) {
		var doc map[string]any
		if err := yaml.Unmarshal([]byte(readAt(t, f)), &doc); err != nil {
			t.Fatalf("%s: %v", filepath.Base(f), err)
		}
		files = append(files, f)
		docs[filepath.Base(f)] = doc
	}
	return files, docs
}

func TestEveryWorkflowParsesAndHasATopLevelPermissionsBlock(t *testing.T) {
	// WorkflowPermissions.test_every_workflow_parses_and_has_a_top_level_permissions_block
	_, docs := workflowDocs(t)
	if _, ok := docs["build.yaml"]; !ok {
		t.Error("build.yaml is not among the workflows")
	}
	for name, doc := range docs {
		t.Run(name, func(t *testing.T) {
			if _, ok := doc["permissions"].(map[string]any); !ok {
				t.Error("a top-level permissions block narrows the default token")
			}
			if _, ok := doc["jobs"]; !ok {
				t.Error("no jobs")
			}
		})
	}
}

func TestTheTopLevelBlockOnlyReadsUnlessADeployWorkflowSaysWhy(t *testing.T) {
	// WorkflowPermissions.test_the_top_level_block_only_reads_unless_a_deploy_workflow_says_why
	// the Pages deploy workflows are upstream flagd's, gated to open-feature/flagd
	deploy := map[string]map[string]any{"docs.yaml": {"contents": "read"}}
	special := map[string]map[string]any{"lint-pr.yaml": {"pull-requests": "write"}}
	_, docs := workflowDocs(t)
	for name, doc := range docs {
		t.Run(name, func(t *testing.T) {
			want := map[string]any{"contents": "read"}
			if w, ok := special[name]; ok {
				want = w
			} else if w, ok := deploy[name]; ok {
				want = w
			}
			if got := doc["permissions"]; !reflect.DeepEqual(any(want), got) {
				t.Errorf("permissions = %v, want %v", got, want)
			}
		})
	}
}

var packagesWriteRE = regexp.MustCompile(`packages:\s*write`)

func TestPackagesWriteIsGrantedInTwoJobsOfBuildYamlOnly(t *testing.T) {
	// WorkflowPermissions.test_packages_write_is_granted_in_two_jobs_of_build_yaml_only
	files, _ := workflowDocs(t)
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			want := 0
			if filepath.Base(f) == "build.yaml" {
				want = 2
			}
			if got := len(packagesWriteRE.FindAllString(readAt(t, f), -1)); got != want {
				t.Errorf("packages: write occurs %d times, want %d", got, want)
			}
		})
	}
}

func TestTheRegistryLoginsArePublishImagesAndThePinnedReleaseWorkflow(t *testing.T) {
	// WorkflowPermissions.test_the_registry_logins_are_publish_images_and_the_pinned_release_workflow
	// docker/login-action appears in build.yaml's publish-images and in release-please.yaml (gated to
	// open-feature/flagd, never enabled here); the hash in ReleaseConfiguration pins that file, so a second login or a
	// change to this one fails a test.
	files, _ := workflowDocs(t)
	var withLogin []string
	for _, f := range files {
		if strings.Contains(readAt(t, f), "docker/login-action") {
			withLogin = append(withLogin, filepath.Base(f))
		}
	}
	assertEqualStrings(t, []string{"build.yaml", "release-please.yaml"}, withLogin)
	if _, ok := releasePinned[".github/workflows/release-please.yaml"]; !ok {
		t.Error("release-please.yaml is not pinned")
	}
}

func TestLintPRRunsOnPullRequestTargetWithoutThePullRequestsCode(t *testing.T) {
	// WorkflowPermissions.test_lint_pr_runs_on_pull_request_target_without_the_pull_requests_code
	text := read(t, ".github", "workflows", "lint-pr.yaml")
	assertIn(t, text, "pull_request_target:")
	for _, gone := range []string{"actions/checkout", "github.event.pull_request.head", "run:"} {
		assertNotIn(t, text, gone)
	}
}

// ---------------------------------------------------------------------------------------------------------------------
// SpecLintWorkflow: the spec lint gates spec changes on errors only, and its sibling scripts sit where load_sibling looks.

var specLintPaths = []string{
	"docs/vision.md", "docs/requirements.md", "docs/glossary.md", "docs/use_cases.puml", "docs/use_cases/**",
	"docs/test_cases/**", "docs/.spec-lint-baseline.json", ".github/scripts/aiup/**", ".github/workflows/spec-lint.yaml"}

func TestSpecLintWorkflow(t *testing.T) { // SpecLintWorkflow.test_spec_lint_workflow
	text := read(t, ".github", "workflows", "spec-lint.yaml")
	var paths strings.Builder
	for _, p := range specLintPaths {
		paths.WriteString("      - \"" + p + "\"\n")
	}
	for _, event := range []string{"push", "pull_request"} {
		t.Run(event, func(t *testing.T) {
			assertIn(t, text, "  "+event+":\n    branches:\n      - main\n    paths:\n"+paths.String())
		})
	}
	script := ".github/scripts/aiup/spec-review/scripts/spec_lint.py"
	assertIn(t, text, "run: python3 -I "+script+" --self-test\n")
	assertIn(t, text, "run: python3 -I "+script+" --docs docs\n")
	assertNotIn(t, text, "--strict")
	var names []string
	for _, m := range regexp.MustCompile(`(?m)^  ([a-z][a-z-]*):\n`).FindAllStringSubmatch(after(t, text), -1) {
		names = append(names, m[1])
	}
	assertEqualStrings(t, []string{"spec-lint"}, names)
}

func TestSiblingScriptsAreVendoredWhereTheLintLooks(t *testing.T) {
	// SpecLintWorkflow.test_sibling_scripts_are_vendored_where_the_lint_looks
	skills := filepath.Join(root(t), ".github", "scripts", "aiup")
	for _, pattern := range []string{"*use-case-spec/scripts/validate_use_case.py", "*test-case/scripts/bpmn_paths.py"} {
		t.Run(pattern, func(t *testing.T) {
			if got := len(glob(t, filepath.Join(skills, filepath.FromSlash(pattern)))); got != 1 {
				t.Errorf("%d files match, want 1", got)
			}
		})
	}
}

// ---------------------------------------------------------------------------------------------------------------------
// DocsCheckWorkflow: QD, markdown-only changes skip the build and run docs-check and the upgrade-guide test instead.

func TestDocsCheckPaths(t *testing.T) { // DocsCheckWorkflow.test_paths
	text := read(t, ".github", "workflows", "docs-check.yaml")
	paths := "    paths:\n      - \"**/*.md\"\n      - \"docs/**\"\n      - \"!.claude/**\"\n      - \"!CLAUDE.md\"\n"
	assertCount(t, 2, text, paths)
	head := before(t, text)
	assertIn(t, head, "  push:\n    branches:\n      - main\n"+paths)
	assertIn(t, head, "  pull_request:\n    branches:\n      - main\n"+paths)
}

func TestDocsCheckJobs(t *testing.T) { // DocsCheckWorkflow.test_jobs
	text := read(t, ".github", "workflows", "docs-check.yaml")
	var names []string
	for _, m := range regexp.MustCompile(`(?m)^  ([a-z][a-z-]*):\n`).FindAllStringSubmatch(after(t, text), -1) {
		names = append(names, m[1])
	}
	assertEqualStrings(t, []string{"docs-check"}, names)
}

func TestTheCheckIsNotOneOfTheBuildWorkflows(t *testing.T) { // DocsCheckWorkflow.test_the_check_is_not_one_of_the_build_workflows
	// build.yaml folded its docs-check job into lint, so the name is the light workflow's alone.
	assertNoMatch(t, build(t), `(?m)^  docs-check:`)
	assertNotIn(t, after(t, read(t, ".github", "workflows", "docs-check.yaml")), "name: docs-check\n")
}

func TestDocsCheckCommands(t *testing.T) { // DocsCheckWorkflow.test_commands
	text := read(t, ".github", "workflows", "docs-check.yaml")
	assertIn(t, text, "      - run: make generate-docs\n")
	assertIn(t, text, "      - run: make test-upgrade-accepted\n        if: ${{ !cancelled() }}\n")
}

// ---------------------------------------------------------------------------------------------------------------------
// NoEmbeddedMarkdown: R7, a go:embed of a markdown file would be read by a build that markdown-only changes skip.

var embedRE = regexp.MustCompile(`^\s*//go:embed\b`)

// shlexSplit splits an embed directive's arguments like shlex.split in POSIX mode (words, single and double quotes,
// backslash escapes). A backquoted string, which go:embed also accepts, is a quote here too: it can only find more.
func shlexSplit(s string) []string {
	var words []string
	var cur strings.Builder
	inWord := false
	var quote rune
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case quote != 0:
			switch {
			case c == quote:
				quote = 0
			case c == '\\' && quote == '"' && i+1 < len(rs) && (rs[i+1] == '"' || rs[i+1] == '\\'):
				i++
				cur.WriteRune(rs[i])
			default:
				cur.WriteRune(c)
			}
		case c == '\'' || c == '"' || c == '`':
			quote, inWord = c, true
		case c == '\\' && i+1 < len(rs):
			i++
			cur.WriteRune(rs[i])
			inWord = true
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(c)
			inWord = true
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words
}

// embeddedMarkdown lists the markdown files that the go:embed directives of goFile pull in: files, globs and directories.
func embeddedMarkdown(t *testing.T, goFile string) []string {
	t.Helper()
	var found []string
	dir := filepath.Dir(goFile)
	for _, line := range pyLines(readAt(t, goFile)) {
		if !embedRE.MatchString(line) {
			continue
		}
		_, args, _ := strings.Cut(line, "embed")
		for _, pattern := range shlexSplit(args) {
			withHidden := strings.HasPrefix(pattern, "all:")
			pattern = strings.TrimPrefix(pattern, "all:")
			matches, err := filepath.Glob(filepath.Join(dir, pattern))
			if err != nil {
				t.Fatal(err)
			}
			for _, match := range matches {
				st, err := os.Stat(match)
				if err != nil {
					continue
				}
				var files []string
				base := filepath.Dir(match)
				if st.IsDir() {
					base = match
					_ = filepath.WalkDir(match, func(p string, d os.DirEntry, err error) error {
						if err == nil && !d.IsDir() {
							files = append(files, p)
						}
						return nil
					})
				} else {
					files = []string{match}
				}
				for _, f := range files {
					rel, _ := filepath.Rel(base, f)
					hidden := false
					for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
						if strings.HasPrefix(part, ".") || strings.HasPrefix(part, "_") {
							hidden = true
						}
					}
					if filepath.Ext(f) == ".md" && (withHidden || !hidden) {
						found = append(found, goFile+": "+f)
					}
				}
			}
		}
	}
	return found
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEmbeddedMarkdownIsFoundInFilesGlobsAndDirectories(t *testing.T) {
	// NoEmbeddedMarkdown.test_embedded_markdown_is_found_in_files_globs_and_directories
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "docs", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(tmp, "docs", "sub", "a.md"), "a")
	write(t, filepath.Join(tmp, "x.md"), "x")
	write(t, filepath.Join(tmp, "y.txt"), "y")
	src := filepath.Join(tmp, "e.go")
	cases := []struct {
		pattern string
		want    int
	}{{"x.md", 1}, {"*.md", 1}, {"docs", 1}, {"all:docs", 1}, {"y.txt", 0}, {"*.txt", 0}, {`"x.md" y.txt`, 1}}
	for _, c := range cases {
		t.Run(c.pattern, func(t *testing.T) {
			write(t, src, "package e\n\n//go:embed "+c.pattern+"\nvar f string\n")
			if got := len(embeddedMarkdown(t, src)); got != c.want {
				t.Errorf("found %d markdown files, want %d", got, c.want)
			}
		})
	}
	if err := os.Rename(filepath.Join(tmp, "docs", "sub", "a.md"), filepath.Join(tmp, "docs", "sub", "_a.md")); err != nil {
		t.Fatal(err)
	}
	write(t, src, "package e\n\n//go:embed docs\nvar f string\n")
	assertEqualStrings(t, nil, embeddedMarkdown(t, src))
}

func TestNoGoEmbedMatchesMarkdown(t *testing.T) { // NoEmbeddedMarkdown.test_no_go_embed_matches_markdown
	r := root(t)
	var found []string
	err := filepath.WalkDir(r, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if slices.Contains([]string{".git", "node_modules", "worktrees"}, d.Name()) && p != r {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".go") {
			found = append(found, embeddedMarkdown(t, p)...)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertEqualStrings(t, nil, found)
}

// The rows of the translation: Python's splitlines breaks, universal newlines for every read.
func TestPyLinesBreaksLikeSplitlines(t *testing.T) {
	assertEqualStrings(t, []string{"a", "b", "c", "d", "e", "f"}, pyLines("a\nb\vc\fd\u2028e\u0085f\n"))
	assertEqualStrings(t, nil, pyLines(""))
	assertEqualStrings(t, []string{"a", "", "b"}, pyLines("a\n\nb"))
}

func TestReadNormalizesCRLF(t *testing.T) {
	p := filepath.Join(t.TempDir(), "w.yaml")
	write(t, p, "a:\r\n  b: 1\r\n")
	assertEqualStrings(t, []string{"a:", "  b: 1"}, strings.Split(strings.TrimSuffix(readAt(t, p), "\n"), "\n"))
}
