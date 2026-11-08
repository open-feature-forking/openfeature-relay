package workflows

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/tools/ci/internal/modules"
	"go.yaml.in/yaml/v3"
)

// The port of BuildWorkflow in test_workflows.py (K2): the build workflow ignores docs,
// never publishes and keeps its gated jobs. Each Test function carries the name of its Python method.

const (
	nightly     = "if: vars.FULL_CI == 'true' || github.event_name == 'workflow_dispatch' || github.event_name == 'schedule'"
	nightlyOnly = "if: github.event_name == 'schedule' || github.event_name == 'workflow_dispatch'"
	parts       = "part: [flagd-standard, flagd-proxy-standard, images, distributions]"
	onMain      = "github.ref == 'refs/heads/main' && (github.event_name == 'push' || github.event_name == 'schedule')"
)

var gated = map[string]string{
	"docker-local": "needs.changes.outputs.image == 'true'",
	"upgrade-test": "needs.changes.outputs.upgrade == 'true'",
	"image-test":   "needs.changes.outputs.reference == 'true'",
}

func TestParsesAsYAML(t *testing.T) { // test_parses_as_yaml
	// GitHub drops every trigger of a workflow it cannot parse, without failing any check.
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(build(t)), &doc); err != nil {
		t.Fatal(err)
	}
	jobs, ok := doc["jobs"].(map[string]any)
	if !ok {
		t.Fatal("jobs is not a mapping")
	}
	if _, ok := jobs["test"]; !ok {
		t.Error(`"test" is not a job`)
	}
}

// PyYAML reads the key `on` as True; yaml/v3 reads it as the string "on" when decoding into any.
func TestOnIsAStringKey(t *testing.T) { // translation row for the PyYAML `on:` gotcha
	var doc map[string]any
	if err := yaml.Unmarshal([]byte("on:\n  push:\n    branches: [main]\njobs: {}\n"), &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["on"]; !ok {
		t.Errorf("keys = %v, want the string key on", doc)
	}
	var real map[string]any
	if err := yaml.Unmarshal([]byte(build(t)), &real); err != nil {
		t.Fatal(err)
	}
	if _, ok := real["on"]; !ok {
		t.Error("build.yaml has no on key")
	}
}

var (
	usesRE   = regexp.MustCompile(`(?m)^\s*(?:- )?uses: (\S+)`)
	pinnedRE = regexp.MustCompile(`^[\w./-]+@[0-9a-f]{40}$`)
)

func TestEveryActionIsPinnedToACommit(t *testing.T) { // test_every_action_is_pinned_to_a_commit
	// A mangled `uses:` (a stray character in place of the @) is still valid YAML, and GitHub rejects the workflow.
	for _, text := range []string{build(t), releaseCheck(t)} {
		for _, m := range usesRE.FindAllStringSubmatch(text, -1) {
			if !pinnedRE.MatchString(m[1]) {
				t.Errorf("%q is not pinned to a commit", m[1])
			}
		}
	}
}

func TestPathsIgnoreDocsOnPushAndPullRequest(t *testing.T) { // test_paths_ignore_docs_on_push_and_pull_request
	text := build(t)
	block := "    paths-ignore:\n      - \"**/*.md\"\n      - \"docs/**\"\n      - \".claude/**\"\n"
	assertCount(t, 2, text, block)
	head := before(t, text)
	assertIn(t, head, "  push:\n    branches:")
	assertIn(t, head, "  pull_request:\n    branches:")
	assertNotIn(t, head, "labeled")
}

func TestConcurrencyGroup(t *testing.T) { // test_concurrency_group
	// QD: a superseded pull request run is cancelled; every other run has a group of its own, so none is.
	assertIn(t, before(t, build(t)),
		"concurrency:\n  group: build-${{ github.event_name == 'pull_request' && github.ref || github.sha }}\n"+
			"  cancel-in-progress: ${{ github.event_name == 'pull_request' }}\n")
}

// Every way a workflow can push an image, log in to a registry or ask for the right to. They may appear in the
// publish-images and move-main jobs of build.yaml and nowhere else; release-please.yaml (gated to open-feature/flagd,
// never enabled here) is allowlisted by the hash that ReleaseConfiguration pins.
var pushSurface = []string{"docker/login-action", "--push", "push: true", "push:true", "push=true", "type=registry", "docker push",
	"docker login", "crane", "skopeo", "oras", "push-to-registry", "packages: write", "publish-images.sh",
	"publish-images.py", "REGISTRY_TOKEN", "attest-build-provenance", "imagetools create"}

func TestOnlyPublishImagesPushesOrLogsIn(t *testing.T) { // test_only_publish_images_pushes_or_logs_in
	// Step 17: the push surface is two jobs of build.yaml, on the main branch of this repository, from the nightly run or a manual run.
	text := build(t)
	publishing := []string{job(t, text, "publish-images"), job(t, text, "move-main")}
	for _, f := range workflowFiles(t) {
		name := filepath.Base(f)
		if name == "release-please.yaml" {
			continue
		}
		outside := readAt(t, f)
		if name == "build.yaml" {
			for _, j := range publishing {
				outside = strings.ReplaceAll(outside, j, "")
			}
		}
		for _, needle := range pushSurface {
			t.Run(name+"/"+needle, func(t *testing.T) { assertNotIn(t, outside, needle) })
		}
	}
	for _, needle := range []string{"docker/login-action", "push-to-registry", "attest-build-provenance"} {
		assertIn(t, job(t, text, "publish-images"), needle)
	}
	// a pull request, a fork, FULL_CI and a branch never publish
	gate := beforeSep(job(t, text, "publish-images"), "steps:")
	for _, condition := range []string{"needs.publish-gate.outputs.publish == 'true'", "github.ref == 'refs/heads/main'",
		"github.repository == 'open-feature-forking/openfeature-relay'",
		"github.event_name == 'schedule' || github.event_name == 'workflow_dispatch'"} {
		assertIn(t, gate, condition)
	}
}

func TestPublishJobs(t *testing.T) { // test_publish_jobs
	// Step 17: publish-images needs the builds and builds nothing; move-main moves :main by compare-and-set.
	text := build(t)
	j := job(t, text, "publish-images")
	assertIn(t, j, "needs: [publish-gate, release-dry-run, image-build, testbed]")
	for _, b := range []string{"docker build", "buildx build", "go build", "make "} {
		assertNotIn(t, j, b)
	}
	assertCount(t, 7, j, "uses: actions/attest-build-provenance@")
	assertCount(t, 1, j, "uses: docker/login-action@")
	assertMatch(t, j, `permissions:\n      contents: read\n      packages: write\n      id-token: write\n      attestations: write\n`)
	move := job(t, text, "move-main")
	assertIn(t, move, "needs: publish-images")
	assertIn(t, move, "concurrency:\n      group: move-main\n      cancel-in-progress: false\n")
	assertIn(t, move, "fetch-depth: 0")
	assertMatch(t, move, `permissions:\n      contents: read\n      packages: write\n`)
	// packages: write is granted at job level in these two jobs and nowhere else
	assertCount(t, 2, text, "packages: write")
	assertNoMatch(t, before(t, text), `packages:`)
}

func TestMoveMainAnywayInput(t *testing.T) { // test_move_main_anyway_input
	// Step 17: the dispatch input that lets move-main pass --force is a boolean off by default, read only in the
	// move-main job through the environment, and --force is passed only when it is true.
	text := build(t)
	head := before(t, text)
	assertMatch(t, head, `workflow_dispatch:\n    inputs:\n      move-main-anyway:\n(?:        description: .*\n)?`+
		`        type: boolean\n        default: false\n`)
	assertCount(t, 1, head, "move-main-anyway")
	jobs := after(t, text)
	assertCount(t, 1, jobs, "move-main-anyway")
	move := job(t, text, "move-main")
	assertMatch(t, move, `env:\n(?:          .*\n)*?          ANYWAY: \$\{\{ inputs\.move-main-anyway \}\}\n`)
	run := afterSep(t, move, "run: |\n")
	assertNotIn(t, run, "inputs.")
	assertCount(t, 1, text, "--force")
	assertIn(t, run, `if [ "$ANYWAY" = true ]; then force=(--force); fi`)
	assertIn(t, run, `"${force[@]}"`)
}

func TestJobs(t *testing.T) { // test_jobs
	assertEqualStrings(t, []string{"changes", "lint", "benchmark", "test", "docker-local", "integration-test", "upgrade-test",
		"image-test", "publish-gate", "release-dry-run", "image-build", "testbed", "publish-images", "move-main",
		"testbed-rpc-outages", "testbed-grpc-sync", "load-smoke", "load-test"}, jobNames(t, build(t)))
}

func flagNames() []string {
	var names []string
	for _, f := range modules.Flags {
		names = append(names, f.Name)
	}
	return names
}

func TestChangesIsTheSingleGate(t *testing.T) { // test_changes_is_the_single_gate
	// The jobs that cost a runner for a change that cannot break them wait for a flag of `changes` and take no runner
	// without it; there is no gate step in a job, and no miss report.
	text := build(t)
	assertNotIn(t, text, "miss-report")
	all := allJobs(t, text)
	for name, condition := range gated {
		t.Run(name, func(t *testing.T) {
			j, ok := all[name]
			if !ok {
				t.Fatalf("job %s is missing", name)
			}
			assertIn(t, j, "    needs: changes\n")
			assertIn(t, j, "    if: "+condition+"\n")
			for _, gone := range []string{"steps.gate", "git diff --name-only", "vars.FULL_CI", "github.event.before"} {
				assertNotIn(t, j, gone)
			}
		})
	}
	needsChanges := regexp.MustCompile(`needs:.*\bchanges\b`)
	for _, n := range []string{"lint", "test", "integration-test", "release-dry-run", "load-smoke", "load-test"} {
		j, ok := all[n]
		if !ok {
			t.Fatalf("job %s is missing", n)
		}
		if needsChanges.MatchString(j) {
			t.Errorf("job %s needs changes", n)
		}
	}
	changes := job(t, text, "changes")
	assertIn(t, changes, "fetch-depth: 2")
	assertIn(t, changes, "python3 .github/scripts/ci-modules.py changes")
	for _, flag := range flagNames() {
		assertIn(t, changes, fmt.Sprintf("      %s: ${{ steps.affected.outputs.%s }}\n", flag, flag))
	}
}

func TestTestRunsMakeTest(t *testing.T) { // test_test_runs_make_test
	j := job(t, build(t), "test")
	assertIn(t, j, "- run: make test 2>&1 | tee test.log\n        shell: bash")
	assertIn(t, j, "CONTRACT_EXTERNAL_COMMIT: ${{ github.event.pull_request.head.sha || github.sha }}")
	// the log stays for a failure summary; there is no artifact of it and no miss report to read one
	assertNotIn(t, j, "path: test.log")
}

var namesRE = regexp.MustCompile(`(?m)^  ([a-z][a-z0-9-]*):`)

func TestDocsCheckAndBuilderE2EAreFoldedIntoOtherJobs(t *testing.T) { // test_docs_check_and_builder_e2e_are_folded_into_other_jobs
	// docs-check runs first in lint, while the tree is clean; builder-e2e is integration-test's flagd-full build.
	text := build(t)
	var names []string
	for _, m := range namesRE.FindAllStringSubmatch(after(t, text), -1) {
		names = append(names, m[1])
	}
	if slices.Contains(names, "docs-check") {
		t.Error("docs-check is a job")
	}
	if slices.Contains(names, "builder-e2e") {
		t.Error("builder-e2e is a job")
	}
	lint := job(t, text, "lint")
	assertBefore(t, lint, "make workspace-init", "- run: make generate-docs")
	assertBefore(t, lint, "- run: make generate-docs", "git status --porcelain")
	assertBefore(t, lint, "git status --porcelain", "- run: make lint")
	integration := allJobs(t, text)["integration-test"]
	assertIn(t, integration, "run: make build-flagd-full")
	assertNotIn(t, integration, "check-flagd-full-generated")
	assertBefore(t, integration, "make workspace-init", "make build-flagd-full")
	assertIn(t, integration, "run: make build-flagsmith")
	assertBefore(t, integration, "make build-flagd-full", "make build-flagsmith")
	assertBefore(t, integration, "make build-flagsmith", "make workspace-clean")
}

// onFor is cm.flags([path])[flag].
func onFor(t *testing.T, flag, path string) bool {
	t.Helper()
	for i, f := range modules.Flags {
		if f.Name == flag {
			return modules.Values([]string{path}, false)[i]
		}
	}
	t.Fatalf("no flag %q", flag)
	return false
}

func TestFlagsFollowTheDiff(t *testing.T) { // test_flags_follow_the_diff
	// The flag patterns are the conditions the gate steps of the jobs used to hold.
	check := func(flag string, want bool, paths ...string) {
		t.Helper()
		for _, p := range paths {
			if got := onFor(t, flag, p); got != want {
				t.Errorf("flag %s for %q = %v, want %v", flag, p, got, want)
			}
		}
	}
	// the differential runs on a pull request that touches what it is built from, and Go code elsewhere reaches it
	// through the nightly run
	check("upgrade", true, "flagd/build.Dockerfile", "distributions/flagd-full/manifest.yaml", "builder/main.go",
		"flagd/cmd/main.go", "test/upgrade/x_test.go", ".github/workflows/build.yaml", "x/Dockerfile")
	check("upgrade", false, "README.md", "docs/vision.md", "Makefile", "core/pkg/evaluator/x.go", "service/ofrep/x.go",
		"sync/http/go.mod", "runtime/go.sum", "go.sum", "testbed/cmd/main.go")
	check("reference", true, "builder/reference/Dockerfile", "test/image/x_test.go", "flagd/build.Dockerfile")
	check("reference", false, "core/pkg/x.go", "README.md", "test/upgrade/x_test.go")
	check("image", true, "go.sum", "core/go.mod", "flagd/build.Dockerfile", "distributions/flagd-full/Dockerfile")
	check("image", false, "core/pkg/x.go", "README.md")
	// the testbed and the benchmarks are not on pull requests, so no flag of the diff gates them
	for _, name := range []string{"testbed", "benchmark"} {
		if slices.Contains(flagNames(), name) {
			t.Errorf("%s is a flag", name)
		}
	}
}

func TestToolsCISetsNoFlag(t *testing.T) { // test_tools_ci_sets_no_flag
	// tools/ci is the repository's CI tooling: its Go files and module files call for no gated job.
	set := func(path string) []string {
		var names []string
		for i, v := range modules.Values([]string{path}, false) {
			if v {
				names = append(names, modules.Flags[i].Name)
			}
		}
		return names
	}
	for _, p := range []string{"tools/ci/main.go", "tools/ci/go.mod", "tools/ci/go.sum", "tools/ci/internal/textio/x.go",
		"tools/ci/internal/modules/deeper/x.go", "tools/ci/internal/oci/x.go",
		"tools/ci/internal/publish/x.go", "tools/ci/internal/allocs/x.go",
		"tools/ci/internal/passsets/x.go", "tools/ci/Dockerfile"} {
		t.Run(p, func(t *testing.T) {
			if got := set(p); len(got) != 0 {
				t.Errorf("%s sets %v, want none", p, got)
			}
		})
	}
	for _, f := range modules.Flags {
		t.Run(f.Name, func(t *testing.T) {
			if f.Exclude == nil {
				t.Fatal("no exclusion")
			}
			if got := f.Exclude.String(); got != `^tools/ci/` {
				t.Errorf("exclusion = %q, want ^tools/ci/", got)
			}
		})
	}
	// the exclusion does not reach a path outside tools/ci
	if !slices.Contains(set("core/tools/ci/x.go"), "go") {
		t.Error("core/tools/ci/x.go does not set go")
	}
	if !slices.Contains(set("go.mod"), "deps") {
		t.Error("go.mod does not set deps")
	}
}

func TestEveryFlagIsTrueWithoutADiffAndNoneIsTrueForAChangeThatTouchesNothing(t *testing.T) {
	// test_every_flag_is_true_without_a_diff_and_none_is_true_for_a_change_that_touches_nothing
	all := modules.Values(nil, true)
	if len(all) != len(modules.Flags) {
		t.Fatalf("%d values for %d flags", len(all), len(modules.Flags))
	}
	for i, v := range all {
		if !v {
			t.Errorf("flag %s is false without a diff", modules.Flags[i].Name)
		}
	}
	for i, v := range modules.Values([]string{}, false) {
		if v {
			t.Errorf("flag %s is true for a change that touches nothing", modules.Flags[i].Name)
		}
	}
}

func TestFipsIsNotRunInCI(t *testing.T) { // test_fips_is_not_run_in_ci
	// FIPS is a documented build with relay-builder, not a job, a flag or a leg of the dry run; the test target and the
	// Dockerfile's build arg stay for the guide.
	text := build(t)
	if slices.Contains(flagNames(), "fips") {
		t.Error("fips is a flag")
	}
	assertNotIn(t, strings.ToLower(text), "fips")
	assertNotIn(t, strings.ToLower(releaseCheck(t)), "fips")
	// the release builds the standard variant only: no FIPS step, archives or image tags, and no variant to choose
	assertNotIn(t, strings.ToLower(read(t, ".github", "workflows", "release-please.yaml")), "fips")
	buildSh := read(t, ".github", "scripts", "release-build.sh")
	assertNotIn(t, strings.ToLower(buildSh), "fips")
	assertNotIn(t, buildSh, "RELEASE_VARIANTS")
	for _, script := range []string{"release-dry-run.sh", "release-dry-run-distributions.sh"} {
		t.Run(script, func(t *testing.T) {
			var lines []string
			for _, l := range pyLines(read(t, ".github", "scripts", script)) {
				if strings.Contains(strings.ToLower(l), "fips") && !strings.Contains(l, "${fips}") && !strings.Contains(l, "FIPS=") &&
					!strings.Contains(l, `local fips="$1"`) && !strings.Contains(l, "fips=off") {
					lines = append(lines, l)
				}
			}
			assertEqualStrings(t, nil, lines)
		})
	}
	assertMatch(t, read(t, "Makefile"), `(?m)^test-fips:`)
	assertMatch(t, read(t, "flagd", "build.Dockerfile"), `(?m)^ARG FIPS`)
}

func TestTheTestJobSummarizesItsFailedTests(t *testing.T) { // test_the_test_job_summarizes_its_failed_tests
	// #190: a failed test job lists each failed test with its package, time and first error; report-only.
	text := build(t)
	assertIn(t, job(t, text, "test"), "if: failure()")
	assertIn(t, job(t, text, "test"), "test-failures.py --log test.log")
	assertIn(t, job(t, text, "lint"), "run: python3 .github/scripts/test_test_failures.py\n")
}

func TestLintRunsTheModuleTableTests(t *testing.T) { // test_lint_runs_the_module_table_tests
	// C1 and C2 are the parity gate for make test and make lint: CI has to run them.
	lint := job(t, build(t), "lint")
	assertIn(t, lint, "run: python3 .github/scripts/test_ci_modules.py\n")
	assertIn(t, lint, "run: python3 .github/scripts/test_ci_modules_affected.py\n")
}

func TestGatedJobs(t *testing.T) { // test_gated_jobs
	text := build(t)
	gate := "if: vars.FULL_CI == 'true' || github.event_name == 'workflow_dispatch' || github.event_name == 'schedule'"
	for _, name := range []string{"testbed-rpc-outages", "testbed-grpc-sync"} {
		t.Run(name, func(t *testing.T) { assertIn(t, job(t, text, name), gate) })
	}
	assertIn(t, job(t, text, "benchmark"), nightlyOnly)
	assertIn(t, job(t, text, "testbed"), nightly)
	for _, name := range []string{"benchmark", "testbed"} {
		t.Run(name, func(t *testing.T) {
			head := beforeSep(job(t, text, name), "steps:")
			assertNotIn(t, strings.ReplaceAll(head, "not on pull requests", ""), "pull_request")
		})
	}
}

func TestLoadSmokeRunsOnEveryBuildAndLoadTestRunsNightly(t *testing.T) {
	// test_load_smoke_runs_on_every_build_and_load_test_runs_nightly
	// W: the 60 s race-build smoke is on every build, the 10 min run is nightly, and both keep their logs on failure.
	text := build(t)
	assertNotIn(t, beforeSep(job(t, text, "load-smoke"), "steps:"), "if:")
	assertIn(t, job(t, text, "load-smoke"), "run: make test-load-smoke")
	assertIn(t, job(t, text, "load-test"), nightly)
	assertMatch(t, job(t, text, "load-test"), `run: make test-load\s`)
	for _, name := range []string{"load-smoke", "load-test"} {
		t.Run(name, func(t *testing.T) {
			assertIn(t, job(t, text, name), "RELAY_LOAD_LOGS:")
			assertIn(t, job(t, text, name), "if: failure()")
		})
	}
}

func TestMarkdownChecksPushOnlyOnMainAndTheInertPagesWorkflowIsGone(t *testing.T) {
	// test_markdown_checks_push_only_on_main_and_the_inert_pages_workflow_is_gone
	assertIn(t, read(t, ".github", "workflows", "markdown-checks.yaml"), "  push:\n    branches:\n      - main\n    paths:\n")
	if _, err := os.Stat(filepath.Join(root(t), ".github", "workflows", "dev-benches-page.yml")); err == nil {
		t.Error("dev-benches-page.yml exists")
	}
}

var k6RE = regexp.MustCompile(`grafana/k6:[^\s'"]+`)

func TestLoadImagePinned(t *testing.T) { // test_load_image_pinned
	// W: the k6 image is pulled by digest; no workflow or script names a grafana/k6 tag without one.
	makefile := read(t, "Makefile")
	assertMatch(t, makefile, `(?m)^K6_IMAGE \?= grafana/k6:\d+\.\d+\.\d+@sha256:[0-9a-f]{64}$`)
	r := root(t)
	var paths []string
	err := filepath.WalkDir(filepath.Join(r, ".github"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			if ok, _ := filepath.Match("*.y*ml", d.Name()); ok || strings.HasSuffix(d.Name(), ".py") {
				paths = append(paths, p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	paths = append(paths, filepath.Join(r, "Makefile"))
	for _, p := range paths {
		for _, line := range pyLines(readAt(t, p)) {
			for _, ref := range k6RE.FindAllString(line, -1) {
				t.Run(filepath.Base(p)+"/"+ref, func(t *testing.T) { assertIn(t, ref, "@sha256:") })
			}
		}
	}
}

func TestBuildNeverSeesALabelEvent(t *testing.T) { // test_build_never_sees_a_label_event
	// The release-check label starts release-check.yaml, not build.yaml: a label event in build.yaml would cancel the
	// pull request's running jobs through the concurrency group and report skipped jobs under the real check names.
	text := build(t)
	assertNotIn(t, text, "github.event.action")
	assertNotIn(t, text, "github.event.label")
	assertNotIn(t, text, "types:")
}

func TestReleaseDryRunRunsNightly(t *testing.T) { // test_release_dry_run_runs_nightly
	// build.yaml's dry run is the full set: nightly, FULL_CI or a manual run, and not on a push to main. A pull request
	// gets the reduced set from release-check.yaml, when it carries the label.
	j := job(t, build(t), "release-dry-run")
	assertIn(t, j, "    "+nightly+"\n")
	for _, gone := range []string{"steps.gate", "git diff", "BASE", "DRY_RUN_SCOPE", "DRY_RUN_DISTS"} {
		assertNotIn(t, j, gone)
	}
}

func TestTheBenchmarkComparesWithTheCommitADayOlderAndSkipsWithoutOne(t *testing.T) {
	// test_the_benchmark_compares_with_the_commit_a_day_older_and_skips_without_one
	j := job(t, build(t), "benchmark")
	assertIn(t, j, "fetch-depth: 0")
	assertIn(t, j, `git rev-list -1 --first-parent --before='24 hours ago' "$GITHUB_SHA"`)
	assertIn(t, j, `[ "$base" = "$GITHUB_SHA" ]`)
	assertIn(t, j, "compare-allocs.py")
	assertCount(t, 4, j, "if: steps.base.outputs.run == 'true'")
}

func TestEveryJobThatMovedToTheNightlyRunAcceptsTheScheduleEvent(t *testing.T) {
	// test_every_job_that_moved_to_the_nightly_run_accepts_the_schedule_event
	// A job whose condition lacks `schedule` silently skips there. The jobs behind a flag run because every flag is
	// true without a diff, and the cache of the nightly run is saved.
	text := build(t)
	for _, name := range []string{"release-dry-run", "load-test", "testbed-rpc-outages", "testbed-grpc-sync", "testbed"} {
		t.Run(name, func(t *testing.T) { assertIn(t, job(t, text, name), nightly) })
	}
	assertIn(t, job(t, text, "benchmark"), nightlyOnly)
	all := allJobs(t, text)
	for name := range gated {
		t.Run(name, func(t *testing.T) {
			head := beforeSep(all[name], "steps:")
			assertNotIn(t, strings.ReplaceAll(head, "on the schedule", ""), "schedule")
		})
	}
	// a scheduled run has no change set: every flag is true
	_, everything, err := modules.ChangeSet(root(t), "schedule", "HEAD", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !everything {
		t.Error("a scheduled run has a change set; it must run every flag")
	}
	assertIn(t, all["release-dry-run"], "(github.event_name == 'push' || github.event_name == 'schedule')")
}

func TestImageTestsAreTheirOwnJobAndDockerLocalHasNoQemu(t *testing.T) {
	// test_image_tests_are_their_own_job_and_docker_local_has_no_qemu
	all := allJobs(t, build(t))
	assertIn(t, all["upgrade-test"], "make upgrade-test")
	assertNotIn(t, all["upgrade-test"], "make image-test")
	assertIn(t, all["image-test"], "run: make image-test")
	assertIn(t, all["image-test"], "timeout-minutes: 75")
	assertNotIn(t, strings.ToLower(all["docker-local"]), "qemu")
}

var cachedJobs = []string{"lint", "test", "integration-test", "upgrade-test", "image-test", "release-dry-run", "load-smoke", "load-test"}

func TestGoCachesAreSavedOnlyOnMain(t *testing.T) { // test_go_caches_are_saved_only_on_main
	// P0: one key per job; every job restores it, and only the push to main saves it. setup-go's own cache has no
	// root go.sum to key on, so it is off.
	text := build(t)
	assertNoMatch(t, text, `uses: actions/cache@`)
	jobs := allJobs(t, text)
	for _, name := range cachedJobs {
		t.Run(name, func(t *testing.T) {
			j, ok := jobs[name]
			if !ok {
				t.Fatalf("job %s is missing", name)
			}
			// the dry-run parts run in parallel from one job: each restores and saves a key of its own
			part := ""
			if name == "release-dry-run" {
				part = "${{ matrix.part }}-"
			}
			key := fmt.Sprintf("key: go-%s-%s${{ hashFiles('**/go.sum') }}", name, part)
			assertCount(t, 1, j, key)
			assertIn(t, j, fmt.Sprintf("restore-keys: go-%s-%s\n", name, part))
			// the save step uses the key the restore step computed: hashFiles of the tree after the build would
			// see go.sum files the build generated (distributions/*/build)
			assertCount(t, 1, j, "key: ${{ steps.go-cache.outputs.cache-primary-key }}")
			assertCount(t, 1, j, "uses: actions/cache/restore@")
			assertCount(t, 1, j, "uses: actions/cache/save@")
			assertIn(t, j, "cache: false")
			save := afterSep(t, j, "- name: Save the Go caches\n")
			assertIn(t, save, "if: "+onMain+" && steps.go-cache.outputs.cache-hit != 'true'")
			assertIn(t, save, "~/.cache/go-build")
		})
	}
	assertCount(t, len(cachedJobs), text, "uses: actions/cache/save@")
}

func TestLintCachesTheGolangciLintBinaryAndCacheDirectory(t *testing.T) {
	// test_lint_caches_the_golangci_lint_binary_and_cache_directory
	j := allJobs(t, build(t))["lint"]
	for _, path := range []string{"${{ env.GOBIN }}/golangci-lint", "~/.cache/golangci-lint"} {
		assertCount(t, 2, j, path)
	}
}

func TestGatesDiffTheFirstParent(t *testing.T) { // test_gates_diff_the_first_parent
	// P0: a pull request's change set is its merge commit against the first parent. base.sha is where the pull
	// request was opened and holds what the base gained since. The gated jobs get theirs from `changes`.
	all := allJobs(t, build(t))
	for name := range gated {
		t.Run(name, func(t *testing.T) {
			assertNotIn(t, all[name], "git diff --name-only")
			assertNotIn(t, all[name], "base.sha")
		})
	}
	check := releaseCheck(t)
	assertIn(t, check, `BASE=$(git rev-parse "$HEAD^1")`)
	assertNotIn(t, check, "base.sha")
}

func TestTheJobsAfterTheGateKeepTheirSteps(t *testing.T) { // test_the_jobs_after_the_gate_keep_their_steps
	all := allJobs(t, build(t))
	assertIn(t, all["testbed"], "make testbed-contract-both")
	assertIn(t, all["testbed"], "make testbed-suites")
	assertIn(t, all["docker-local"], "security-events: write")
	assertIn(t, all["docker-local"], "aquasecurity/trivy-action")
}

func TestOutageAndGRPCSourceSuitesRunNightlyOnMain(t *testing.T) { // test_outage_and_grpc_source_suites_run_nightly_on_main
	// Coverage gap 7: a cron schedule (which GitHub runs on the default branch only) starts both suites.
	text := build(t)
	assertMatch(t, before(t, text), `  schedule:\n    - cron: "[0-9*/, ]+ [0-9*/, ]+ \* \* \*"\n`)
	for _, name := range []string{"testbed-rpc-outages", "testbed-grpc-sync"} {
		t.Run(name, func(t *testing.T) { assertIn(t, job(t, text, name), "github.event_name == 'schedule'") })
	}
	// the benchmark comparison runs nightly as well, against the newest commit that is a day older
	assertIn(t, job(t, text, "benchmark"), nightlyOnly)
}

func TestCoverageProfilesAreKept(t *testing.T) { // test_coverage_profiles_are_kept
	// #208: the profiles are an artifact for a coverage tracker; the comparison with the base was dropped by the
	// project lead, and the Codecov step that failed on every run for want of a token is gone.
	text := build(t)
	assertNotIn(t, text, "coverage job")
	assertNotIn(t, strings.ToLower(text), "codecov")
	assertIn(t, job(t, text, "test"), "name: coverage-profiles\n          path: \"*-coverage.out\"")
	assertNotIn(t, text, "compare-coverage")
}

func TestOutageJobComparesBothImages(t *testing.T) { // test_outage_job_compares_both_images
	// UC-022 Q16: the gated job runs the RPC suite with the outage scenarios against both images.
	assertIn(t, job(t, build(t), "testbed-rpc-outages"), "make testbed-rpc-outages")
}

func TestReleaseDryRunParts(t *testing.T) { // test_release_dry_run_parts
	// Slice R: the dry run is a matrix of parts, the full set nightly, with FULL_CI and manually.
	j := job(t, build(t), "release-dry-run")
	assertIn(t, j, parts)
	assertIn(t, j, "fail-fast: false")
	assertIn(t, j, "DRY_RUN_PART: ${{ matrix.part }}")
}

func TestReleaseCheckWorkflow(t *testing.T) { // test_release_check_workflow
	// A pull request labeled release-check gets the reduced dry run from a workflow of its own: the same parts and
	// scripts, its own concurrency group, and nothing else it can cancel or be mistaken for.
	text := releaseCheck(t)
	assertIn(t, text, "on:\n  pull_request:\n    types: [labeled, synchronize]\n    branches:\n      - main\n")
	assertIn(t, text, "group: release-check-${{ github.ref }}-${{ github.event.action == 'labeled' && "+
		"github.event.label.name != 'release-check' && github.run_id || 'run' }}")
	assertIn(t, text, "cancel-in-progress: true")
	assertCount(t, 1, text, "\n    if: contains(github.event.pull_request.labels.*.name, 'release-check') && "+
		"(github.event.action != 'labeled' || github.event.label.name == 'release-check')\n")
	assertIn(t, text, "name: Release check (${{ matrix.part }})")
	assertIn(t, text, parts)
	assertIn(t, text, "DRY_RUN_PART: ${{ matrix.part }}\n          DRY_RUN_SCOPE: reduced\n")
	assertIn(t, text, "run: .github/scripts/release-dry-run.sh")
	// it reads the cache build.yaml's dry run saves from the nightly run and saves nothing itself
	assertIn(t, text, "key: go-release-dry-run-${{ matrix.part }}-${{ hashFiles('**/go.sum') }}")
	assertNotIn(t, text, "cache/save")
	assertNoMatch(t, text, `push:\s*true`)
	assertNotIn(t, text, "docker/login-action")
}

func TestPublishGateJob(t *testing.T) { // test_publish_gate_job
	// Step 17: one small job decides whether a run builds and publishes the images; the image jobs wait for it.
	text := build(t)
	gate := job(t, text, "publish-gate")
	assertNotIn(t, gate, "permissions:")
	assertIn(t, gate, ".github/scripts/publish-gate.sh")
	// tools/ci is CI tooling: its files do not count as image inputs, except the packages that publish (step 18, plan 4)
	assertIn(t, gate, "grep -Ev '^tools/ci/'")
	assertBefore(t, gate, "grep -Ev '^tools/ci/'", `grep -Eq "$pattern"`)
	assertIn(t, gate, "|^tools/ci/internal/(oci|publish)/'")
	// the group must exit 0 when nothing matches, or a diff of core/x.go alone reads as "not changed" under pipefail
	assertIn(t, gate, "grep -E '^tools/ci/internal/(oci|publish)/' || true; } | grep -Eq")
	assertIn(t, gate, "build: ${{ steps.gate.outputs.build }}")
	assertIn(t, gate, "publish: ${{ steps.gate.outputs.publish }}")
	for _, name := range []string{"release-dry-run", "image-build"} {
		t.Run(name, func(t *testing.T) { assertIn(t, job(t, text, name), "needs: publish-gate\n") })
	}
	// the testbed runs nightly, with FULL_CI and manually, and tests the layout of a run that builds the images
	testbed := job(t, text, "testbed")
	assertIn(t, testbed, "    needs: publish-gate\n")
	assertIn(t, testbed, nightly)
	assertIn(t, job(t, text, "image-build"), "if: needs.publish-gate.outputs.build == 'true'")
}

var distributionsRE = regexp.MustCompile(`distributions: "([^"]+)"`)

func TestImageBuildPartsCoverTheOtherFiveDistributions(t *testing.T) {
	// test_image_build_parts_cover_the_other_five_distributions
	// Step 17: flagd-minimal is built by the distributions part and the testbed by its job; the rest by image-build,
	// as layouts checked as layouts and run from the layout. FIPS is not published.
	j := job(t, build(t), "image-build")
	var listed []string
	for _, m := range distributionsRE.FindAllStringSubmatch(j, -1) {
		listed = append(listed, strings.Fields(m[1])...)
	}
	slices.Sort(listed)
	assertEqualStrings(t, []string{"flagd-cloud", "flagd-full", "flagd-kubernetes", "flagd-minimal-otel", "flagd-sync"}, listed)
	assertIn(t, j, ".github/scripts/image-build.sh")
	assertIn(t, j, `"0.0.0-main.${GITHUB_SHA::7}"`)
	script := read(t, ".github", "scripts", "image-build.sh")
	// no FIPS image is published: the build has one variant and no FIPS build argument
	assertNotIn(t, strings.ReplaceAll(strings.ToLower(script), "no fips images", ""), "fips")
	assertIn(t, script, "check_layout")
	assertIn(t, script, "check_runtime")
	assertIn(t, script, "source_dir=flagd\n")
}

func TestTheTestbedRunsOnTheLayoutThatIsPublished(t *testing.T) { // test_the_testbed_runs_on_the_layout_that_is_published
	j := job(t, build(t), "testbed")
	assertIn(t, j, ".github/scripts/image-testbed.sh")
	assertIn(t, j, "make testbed-contract-both TESTBED_RELAY_IMAGE=flagd-testbed:layout")
	assertIn(t, j, "make testbed-suites TESTBED_RELAY_IMAGE=flagd-testbed:layout")
	assertIn(t, j, "name: layouts-flagd-testbed")
}

func TestTheTestbedJobsKeepTheirFailureDetails(t *testing.T) { // test_the_testbed_jobs_keep_their_failure_details
	// #190: the suites' go test output is written to testbed-out/, a failed run prints its failed tests, and the
	// directory is uploaded with a short retention; the triggers are not part of this.
	text := build(t)
	script := read(t, ".github", "scripts", "testbed-suites.sh")
	assertIn(t, script, `out="${TESTBED_OUT:-testbed-out}"`)
	assertIn(t, script, "testbed-failures.py")
	assertIn(t, script, "test-failures.py --log")
	assertIn(t, read(t, ".gitignore"), "testbed-out/")
	for _, name := range []string{"testbed", "testbed-rpc-outages", "testbed-grpc-sync"} {
		t.Run(name, func(t *testing.T) {
			assertMatch(t, job(t, text, name), `(?s)if: always\(\)\n\s+uses: actions/upload-artifact@\S+ # v4[^\n]*\n\s+with:\n`+
				`\s+name: testbed-[a-z-]+\n\s+path: testbed-out/\n\s+retention-days: 7\n`)
		})
	}
	grpc := job(t, text, "testbed-grpc-sync")
	assertIn(t, grpc, "make testbed-grpc-sync 2>&1 | tee testbed-out/grpc-sync.log")
	assertIn(t, grpc, "test-failures.py --log testbed-out/grpc-sync.log")
	assertIn(t, grpc, "if: failure()")
}

func TestImagesAreMarkedAsAProofOfConcept(t *testing.T) { // test_images_are_marked_as_a_proof_of_concept
	// Q4: labels and annotations come from the build scripts, never from flagd's or flagd-proxy's Dockerfile (K3).
	layout := read(t, ".github", "scripts", "oci-layout.py")
	assertIn(t, layout, "Proof of concept from the OpenFeature Relay incubator, built from main. ")
	assertIn(t, layout, "Not an official OpenFeature image; not for production.")
	assertNotIn(t, layout, "vendor")
	lib := read(t, ".github", "scripts", "image-lib.sh")
	assertIn(t, lib, `--label "${kv}" --annotation "index:${kv}" --annotation "manifest:${kv}"`)
	for _, dockerfile := range []string{"flagd/build.Dockerfile", "flagd-proxy/build.Dockerfile"} {
		assertNotIn(t, read(t, dockerfile), "org.opencontainers.image")
	}
}

func TestReleaseDryRunFullCoversTodaysSet(t *testing.T) { // test_release_dry_run_full_covers_todays_set
	// Slice R: the parts together build the standard archives of flagd and flagd-proxy, the image and the five distributions.
	script := read(t, ".github", "scripts", "release-dry-run.sh")
	m := regexp.MustCompile(`part: \[(.*)\]`).FindStringSubmatch(job(t, build(t), "release-dry-run"))
	if m == nil {
		t.Fatal("release-dry-run has no part list")
	}
	for _, item := range []string{"flagd", "flagd-proxy"} {
		assertIn(t, m[1], item+"-standard")
	}
	// the default part runs everything, and release-build.sh is told only the platforms the scope narrows
	assertIn(t, script, `part="${DRY_RUN_PART:-all}"`)
	assertIn(t, script, `[ "${scope}" != reduced ] || platforms="linux/amd64"`)
	for _, label := range []string{"Darwin_arm64 Darwin_x86_64 Linux_arm64 Linux_x86_64", "Windows_x86_64 Windows_i386"} {
		assertIn(t, script, label)
	}
	for _, tag := range []string{":latest", ":${{ env.VERSION }}"} {
		assertIn(t, script, tag)
	}
	dists := read(t, ".github", "scripts", "release-dry-run-distributions.sh")
	assertIn(t, dists, `others="flagd-minimal-otel flagd-cloud flagd-kubernetes flagd-sync"`)
	assertIn(t, dists, `platforms="linux/amd64"`)
}

func TestGRPCSyncJobRunsTheTestbedTest(t *testing.T) { // test_grpc_sync_job_runs_the_testbed_test
	// UC-023 Q14: the gated job runs flagd-full with a grpc source against the relay testbed.
	assertIn(t, job(t, build(t), "testbed-grpc-sync"), "make testbed-grpc-sync")
}

// Not in the Python: these tests run in the test job through the Makefile's PLAIN_TEST_MODULES, which replaces the lint
// steps that ran test_workflows.py and test_publish_gate.py.
func TestToolsCIRunsInMakeTest(t *testing.T) {
	assertMatch(t, read(t, "Makefile"), `(?m)^PLAIN_TEST_MODULES :=(?:.*\\\n)*.*\btools/ci\b`)
	assertIn(t, job(t, build(t), "test"), "- run: make test 2>&1 | tee test.log\n        shell: bash")
}
