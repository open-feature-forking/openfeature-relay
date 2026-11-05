"""Characterization of the release and build configuration that step 12 must leave as it is (C-003).

The hashes are of the files with line endings normalised to LF. A change to a pinned file is deliberate: record the
new hash in the same commit.
"""
import hashlib
import os
import pathlib
import re
import shlex
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
BUILD = ROOT / ".github" / "workflows" / "build.yaml"
RELEASE_CHECK = ROOT / ".github" / "workflows" / "release-check.yaml"


def sha256(relative):
    data = (ROOT / relative).read_bytes().replace(b"\r\n", b"\n")
    return hashlib.sha256(data).hexdigest()


class ReleaseConfiguration(unittest.TestCase):
    """K2: flagd and flagd-proxy release exactly as today."""

    PINNED = {
        ".github/workflows/release-please.yaml": "e0de2bfa567c335e74db5bb1a8cee66fd5911fd80350c20ba1ae61f3bd257c45",
        "release-please-config.json": "f7f5af2d67434c7a7565fa88cd1cd09b5ad29e7f6bf93dbb3fd57e54afc24c97",
        ".release-please-manifest.json": "4617b2ca55b05124c5c270d1e66184912125b461d6c24ee0d90ec5da142ec6ee",
    }

    def test_release_files_are_unchanged(self):
        for path, want in self.PINNED.items():
            with self.subTest(path=path):
                self.assertEqual(want, sha256(path))


class BuildWorkflow(unittest.TestCase):
    """K2: the build workflow ignores docs, never publishes and keeps its gated jobs."""

    @classmethod
    def setUpClass(cls):
        cls.text = BUILD.read_text(encoding="utf-8")

    def job(self, name):
        """The text of one top-level job."""
        jobs = self.text.split("\njobs:\n", 1)[1]
        match = re.search(rf"^  {re.escape(name)}:[ \t]*(?:#.*)?\n(.*?)(?=^  [a-z][a-z0-9-]*:[ \t]*(?:#.*)?\n|\Z)", jobs, re.S | re.M)
        self.assertIsNotNone(match, f"job {name} is missing")
        return match.group(1)

    def test_parses_as_yaml(self):
        # GitHub drops every trigger of a workflow it cannot parse, without failing any check.
        try:
            import yaml
        except ImportError:
            if os.environ.get("GITHUB_ACTIONS") == "true":
                self.fail("PyYAML is not installed on the runner, so this test cannot catch a workflow GitHub cannot parse")
            self.skipTest("PyYAML is not installed")
        jobs = yaml.safe_load(self.text)["jobs"]
        self.assertIn("test", jobs)

    def test_paths_ignore_docs_on_push_and_pull_request(self):
        block = '    paths-ignore:\n      - "**/*.md"\n      - "docs/**"\n      - ".claude/**"\n'
        self.assertEqual(2, self.text.count(block))
        head = self.text.split("\njobs:\n", 1)[0]
        self.assertIn("  push:\n    branches:", head)
        self.assertIn("  pull_request:\n    branches:", head)
        self.assertNotIn("labeled", head)

    def test_concurrency_group(self):
        """QD: a superseded pull request run is cancelled; every other run has a group of its own, so none is."""
        head = self.text.split("\njobs:\n", 1)[0]
        self.assertIn(
            "concurrency:\n  group: build-${{ github.event_name == 'pull_request' && github.ref || github.sha }}\n"
            "  cancel-in-progress: ${{ github.event_name == 'pull_request' }}\n", head)

    def test_nothing_is_pushed_or_logged_in(self):
        self.assertNotRegex(self.text, r"push:\s*true")
        self.assertNotIn("docker/login-action", self.text)

    def test_jobs(self):
        names = re.findall(r"^  ([a-z][a-z0-9-]*):[ \t]*(?:#.*)?\n", self.text.split("\njobs:\n", 1)[1], re.M)
        self.assertEqual(
            ["changes", "lint", "benchmark", "docs-check", "test", "miss-report", "builder-e2e", "fips", "docker-local", "integration-test",
             "upgrade-test", "release-dry-run", "testbed", "testbed-rpc-outages", "testbed-grpc-sync"], names)

    def test_changes_is_report_only(self):
        """P1: the affected-module report runs beside today's jobs; nothing but the miss report waits on it."""
        self.assertEqual([], [n for n in ("lint", "test", "builder-e2e", "integration-test", "upgrade-test", "release-dry-run")
                              if re.search(r"needs:.*\bchanges\b", self.job(n))])
        self.assertIn("fetch-depth: 2", self.job("changes"))
        self.assertIn("python3 .github/scripts/ci-modules.py changes", self.job("changes"))
        report = self.job("miss-report")
        self.assertIn("needs: [changes, test]", report)
        self.assertIn("if: always() && needs.changes.result == 'success'", report)
        self.assertIn("continue-on-error: true", report)

    def test_test_still_runs_make_test_and_keeps_its_log(self):
        job = self.job("test")
        self.assertIn("- run: make test 2>&1 | tee test.log\n        shell: bash", job)
        self.assertIn("CONTRACT_EXTERNAL_COMMIT: ${{ github.event.pull_request.head.sha || github.sha }}", job)
        self.assertIn("path: test.log", job)

    def test_lint_runs_the_module_table_tests(self):
        """C1 and C2 are the parity gate for make test and make lint: CI has to run them."""
        self.assertIn("run: python3 .github/scripts/test_ci_modules.py\n", self.job("lint"))

    def test_gated_jobs(self):
        gate = "if: vars.FULL_CI == 'true' || github.event_name == 'workflow_dispatch'"
        for name in ("docker-local", "testbed-rpc-outages", "testbed-grpc-sync"):
            with self.subTest(job=name):
                self.assertIn(gate, self.job(name))
        self.assertIn("if: github.event_name == 'pull_request'", self.job("benchmark"))

    def test_build_never_sees_a_label_event(self):
        """The release-check label starts release-check.yaml, not build.yaml: a label event in build.yaml would cancel the
        pull request's running jobs through the concurrency group and report skipped jobs under the real check names."""
        self.assertNotIn("github.event.action", self.text)
        self.assertNotIn("github.event.label", self.text)
        self.assertNotIn("types:", self.text)

    def test_release_dry_run_runs_when_its_inputs_change(self):
        """build.yaml's dry run is the full set: a push to `main`, FULL_CI or a manual run. A pull request gets the reduced
        set from release-check.yaml, when it carries the label; a change to the release's inputs alone starts nothing."""
        job = self.job("release-dry-run")
        self.assertNotIn("\n    if:", job)
        self.assertIn('[ "$FULL_CI" = true ] || [ "$EVENT" = workflow_dispatch ]', job)
        self.assertIn('[ "$EVENT" = push ] && [ "$REF" = refs/heads/main ]', job)
        for gone in ("git diff", "BASE", "DRY_RUN_SCOPE", "DRY_RUN_DISTS", "pull_request"):
            self.assertNotIn(gone, job.replace("A pull request opts into the reduced set", ""))
        self.assertIn("if: steps.gate.outputs.run == 'true'", job)

    def all_jobs(self):
        """Every job's text by id, including ids with digits that job() cannot reach."""
        jobs = self.text.split("\njobs:\n", 1)[1]
        parts = re.split(r"(?m)^(?=  [a-z][a-z0-9-]*:[^\n]*\n)", jobs)
        return {m.group(1): part for part in parts if (m := re.match(r"  ([a-z0-9-]+):", part))}

    CACHED_JOBS = ("lint", "docs-check", "test", "builder-e2e", "fips", "integration-test", "upgrade-test", "release-dry-run")
    GATED_CACHED_JOBS = ("fips", "upgrade-test", "release-dry-run")
    ON_MAIN = "github.ref == 'refs/heads/main' && github.event_name == 'push'"

    def test_go_caches_are_saved_only_on_main(self):
        """P0: one key per job; every job restores it, and only the push to main saves it. setup-go's own cache has no
        root go.sum to key on, so it is off."""
        self.assertNotRegex(self.text, r"uses: actions/cache@")
        jobs = self.all_jobs()
        for name in self.CACHED_JOBS:
            with self.subTest(job=name):
                job = jobs[name]
                # the dry-run parts run in parallel from one job: each restores and saves a key of its own
                part = "${{ matrix.part }}-" if name == "release-dry-run" else ""
                key = f"key: go-{name}-{part}${{{{ hashFiles('**/go.sum') }}}}"
                self.assertEqual(1, job.count(key))
                self.assertIn(f"restore-keys: go-{name}-{part}\n", job)
                # the save step uses the key the restore step computed: hashFiles of the tree after the build would
                # see go.sum files the build generated (distributions/*/build)
                self.assertEqual(1, job.count("key: ${{ steps.go-cache.outputs.cache-primary-key }}"))
                self.assertEqual(1, job.count("uses: actions/cache/restore@"))
                self.assertEqual(1, job.count("uses: actions/cache/save@"))
                self.assertIn("cache: false", job)
                gate = "steps.gate.outputs.run == 'true' && " if name in self.GATED_CACHED_JOBS else ""
                save = job.split("- name: Save the Go caches\n", 1)[1]
                self.assertIn(f"if: {gate}{self.ON_MAIN} && steps.go-cache.outputs.cache-hit != 'true'", save)
                self.assertIn("~/.cache/go-build", save)
        self.assertEqual(len(self.CACHED_JOBS), self.text.count("uses: actions/cache/save@"))

    def test_lint_caches_the_golangci_lint_binary_and_cache_directory(self):
        job = self.all_jobs()["lint"]
        for path in ("${{ env.GOBIN }}/golangci-lint", "~/.cache/golangci-lint"):
            self.assertEqual(2, job.count(path), path)

    def test_gates_diff_the_first_parent(self):
        """P0: a pull request's change set is its merge commit against the first parent. base.sha is where the pull
        request was opened and holds what the base gained since."""
        jobs = self.all_jobs()
        for name in ("upgrade-test", "testbed", "fips"):
            with self.subTest(job=name):
                job = jobs[name]
                self.assertIn("BASE: ${{ github.event.before }}\n", job)
                self.assertNotIn("base.sha", job)
                self.assertIn('if [ "$EVENT" = pull_request ]; then BASE=$(git rev-parse "$HEAD^1"); fi', job)
                self.assertIn('git diff --name-only "$BASE" "$HEAD"', job)
        check = RELEASE_CHECK.read_text(encoding="utf-8")
        self.assertIn('BASE=$(git rev-parse "$HEAD^1")', check)
        self.assertNotIn("base.sha", check)

    def test_testbed_runs_when_go_code_or_the_testbed_changes(self):
        """UC-022 Q14: the relay testbed has a gate step: FULL_CI, a manual run, or a change outside docs/ and .claude/."""
        job = self.job("testbed")
        self.assertNotIn("\n    if:", job)
        self.assertIn('[ "$FULL_CI" = true ] || [ "$EVENT" = workflow_dispatch ]', job)
        self.assertIn(r"grep -Ev '^(docs|\.claude)/'", job)
        self.assertIn(r"\.go$|(^|/)go\.(mod|sum)$", job)
        self.assertIn("if: steps.gate.outputs.run == 'true'", job)
        self.assertIn("make testbed-contract-both", job)
        self.assertIn("make testbed-suites", job)

    def test_upgrade_differential_runs_on_any_go_change(self):
        """Coverage gap 1: a change under core/, service/, sync/ or runtime/ changes flagd-full, so it runs the differential."""
        job = self.all_jobs()["upgrade-test"]
        pattern = re.search(r"pattern='([^']*)'", job).group(1)
        for path in ("core/pkg/evaluator/x.go", "service/ofrep/x.go", "sync/http/go.mod", "runtime/go.sum", "go.sum",
                     "flagd/build.Dockerfile", "distributions/flagd-full/manifest.yaml", "builder/main.go"):
            with self.subTest(path=path):
                self.assertTrue(re.search(pattern, path), path)
        for path in ("README.md", "docs/vision.md", "Makefile"):
            with self.subTest(path=path):
                self.assertFalse(re.search(pattern, path), path)

    def test_fips_runs_on_go_changes_with_the_submodule(self):
        """Coverage gap 2: runtime/cmd's tests need the test-harness submodule; the job runs on a push to main, FULL_CI,
        a manual run and a pull request that changes Go code, and every step after the checkout waits for the gate."""
        job = self.all_jobs()["fips"]
        self.assertNotRegex(job, r"\n    if:")
        self.assertIn("git submodule update --init test-harness", job)
        self.assertLess(job.index("git submodule update --init test-harness"), job.index("make test-fips"))
        pattern = re.search(r"pattern='([^']*)'", job).group(1)
        self.assertTrue(re.search(pattern, "core/pkg/x.go"))
        self.assertTrue(re.search(pattern, "sync/http/go.sum"))
        self.assertFalse(re.search(pattern, "README.md"))
        self.assertIn('[ "$EVENT" = push ] && [ "$REF" = refs/heads/main ]', job)
        steps = re.split(r"(?m)^      - ", job.split("\n    steps:\n", 1)[1])[1:]
        ungated = [s.splitlines()[0] for s in steps if "steps.gate.outputs.run == 'true'" not in s]
        self.assertEqual(["name: Checkout repository", "name: Fetch the commit before the push",
                          "name: Does the FIPS job apply to this change?"], ungated)

    def test_outage_job_compares_both_images(self):
        """UC-022 Q16: the gated job runs the RPC suite with the outage scenarios against both images."""
        self.assertIn("make testbed-rpc-outages", self.job("testbed-rpc-outages"))

    PARTS = "part: [flagd-standard, flagd-fips, flagd-proxy-standard, flagd-proxy-fips, images, distributions]"

    def test_release_dry_run_parts(self):
        """Slice R: the dry run is a matrix of parts, the full set on `main`, with FULL_CI and manually."""
        job = self.job("release-dry-run")
        self.assertIn(self.PARTS, job)
        self.assertIn("fail-fast: false", job)
        self.assertIn("DRY_RUN_PART: ${{ matrix.part }}", job)

    def test_release_check_workflow(self):
        """A pull request labeled release-check gets the reduced dry run from a workflow of its own: the same parts and
        scripts, its own concurrency group, and nothing else it can cancel or be mistaken for."""
        text = RELEASE_CHECK.read_text(encoding="utf-8")
        self.assertIn("on:\n  pull_request:\n    types: [labeled, synchronize]\n    branches:\n      - main\n", text)
        self.assertIn("group: release-check-${{ github.ref }}-${{ github.event.action == 'labeled' && "
                      "github.event.label.name != 'release-check' && github.run_id || 'run' }}", text)
        self.assertIn("cancel-in-progress: true", text)
        self.assertEqual(1, text.count("\n    if: contains(github.event.pull_request.labels.*.name, 'release-check') && "
                                       "(github.event.action != 'labeled' || github.event.label.name == 'release-check')\n"))
        self.assertIn("name: Release check (${{ matrix.part }})", text)
        self.assertIn(self.PARTS, text)
        self.assertIn("DRY_RUN_PART: ${{ matrix.part }}\n          DRY_RUN_SCOPE: reduced\n", text)
        self.assertIn("run: .github/scripts/release-dry-run.sh", text)
        # it reads the cache build.yaml's dry run saves on main and saves nothing itself
        self.assertIn("key: go-release-dry-run-${{ matrix.part }}-${{ hashFiles('**/go.sum') }}", text)
        self.assertNotIn("cache/save", text)
        self.assertNotRegex(text, r"push:\s*true")
        self.assertNotIn("docker/login-action", text)

    def test_release_dry_run_full_covers_todays_set(self):
        """Slice R: the parts together build the 11 archives of flagd and flagd-proxy, the images and the five distributions."""
        script = (ROOT / ".github" / "scripts" / "release-dry-run.sh").read_text(encoding="utf-8")
        parts = re.search(r"part: \[(.*)\]", self.job("release-dry-run")).group(1)
        for item in ("flagd", "flagd-proxy"):
            for variant in ("standard", "fips"):
                self.assertIn(f"{item}-{variant}", parts)
        # the default part runs everything, and release-build.sh is told only the platforms the scope narrows
        self.assertIn('part="${DRY_RUN_PART:-all}"', script)
        self.assertIn('[ "${scope}" != reduced ] || platforms="linux/amd64"', script)
        for label in ("Darwin_arm64 Darwin_x86_64 Linux_arm64 Linux_x86_64", "Windows_x86_64 Windows_i386"):
            self.assertIn(label, script)
        for tag in (":latest", ":${{ env.VERSION }}", ":latest-fips", ":${{ env.VERSION }}-fips"):
            self.assertIn(tag, script)
        dists = (ROOT / ".github" / "scripts" / "release-dry-run-distributions.sh").read_text(encoding="utf-8")
        self.assertIn('others="flagd-minimal-otel flagd-cloud flagd-kubernetes flagd-sync"', dists)
        self.assertIn('platforms="linux/amd64"', dists)


    def test_grpc_sync_job_runs_the_testbed_test(self):
        """UC-023 Q14: the gated job runs flagd-full with a grpc source against the relay testbed."""
        self.assertIn("make testbed-grpc-sync", self.job("testbed-grpc-sync"))

class SpecLintWorkflow(unittest.TestCase):
    """The spec lint gates spec changes on errors only, and its sibling scripts sit where load_sibling looks."""

    PATHS = (
        "docs/vision.md", "docs/requirements.md", "docs/glossary.md", "docs/use_cases.puml", "docs/use_cases/**",
        "docs/test_cases/**", "docs/.spec-lint-baseline.json", ".github/scripts/aiup/**", ".github/workflows/spec-lint.yaml")

    @classmethod
    def setUpClass(cls):
        cls.text = (ROOT / ".github" / "workflows" / "spec-lint.yaml").read_text(encoding="utf-8")

    def test_spec_lint_workflow(self):
        paths = "".join(f'      - "{p}"\n' for p in self.PATHS)
        for event in ("push", "pull_request"):
            with self.subTest(event=event):
                self.assertIn(f"  {event}:\n    branches:\n      - main\n    paths:\n{paths}", self.text)
        script = ".github/scripts/aiup/spec-review/scripts/spec_lint.py"
        self.assertIn(f"run: python3 -I {script} --self-test\n", self.text)
        self.assertIn(f"run: python3 -I {script} --docs docs\n", self.text)
        self.assertNotIn("--strict", self.text)
        jobs = self.text.split("\njobs:\n", 1)[1]
        self.assertEqual(["spec-lint"], re.findall(r"^  ([a-z][a-z-]*):\n", jobs, re.M))

    def test_sibling_scripts_are_vendored_where_the_lint_looks(self):
        skills = ROOT / ".github" / "scripts" / "aiup"
        for pattern in ("*use-case-spec/scripts/validate_use_case.py", "*test-case/scripts/bpmn_paths.py"):
            with self.subTest(pattern=pattern):
                self.assertEqual(1, len(list(skills.glob(pattern))))


class DocsCheckWorkflow(unittest.TestCase):
    """QD: markdown-only changes skip the build and run docs-check and the upgrade-guide test instead."""

    @classmethod
    def setUpClass(cls):
        text = (ROOT / ".github" / "workflows" / "docs-check.yaml").read_text(encoding="utf-8")
        cls.text = text.replace("\r\n", "\n")

    def test_paths(self):
        paths = '    paths:\n      - "**/*.md"\n      - "docs/**"\n      - "!.claude/**"\n      - "!CLAUDE.md"\n'
        self.assertEqual(2, self.text.count(paths))
        head = self.text.split("\njobs:\n", 1)[0]
        self.assertIn("  push:\n    branches:\n      - main\n" + paths, head)
        self.assertIn("  pull_request:\n    branches:\n      - main\n" + paths, head)

    def test_jobs(self):
        names = re.findall(r"^  ([a-z][a-z-]*):\n", self.text.split("\njobs:\n", 1)[1], re.M)
        self.assertEqual(["docs-check", "upgrade-guide"], names)

    def test_the_job_names_differ_from_the_build_workflows(self):
        build = BUILD.read_text(encoding="utf-8")
        self.assertNotIn("name: docs-check\n", self.text.split("\njobs:\n", 1)[1])
        self.assertIn("    name: docs-check (markdown run)\n", self.text)
        self.assertNotIn("name: docs-check (markdown run)", build)

    def test_commands(self):
        self.assertIn("      - run: make generate-docs\n", self.text)
        self.assertIn("      - run: make test-upgrade-accepted\n", self.text)


class NoEmbeddedMarkdown(unittest.TestCase):
    """R7: a go:embed of a markdown file would be read by a build that markdown-only changes skip."""

    @staticmethod
    def embedded_markdown(go_file):
        """The markdown files that the go:embed directives of go_file pull in: files, globs and directories."""
        found = []
        for line in go_file.read_text(encoding="utf-8", errors="replace").splitlines():
            if not re.match(r"\s*//go:embed\b", line):
                continue
            for pattern in shlex.split(line.split("embed", 1)[1]):
                with_hidden = pattern.startswith("all:")
                for match in go_file.parent.glob(pattern.removeprefix("all:")):
                    files = [f for f in match.rglob("*") if f.is_file()] if match.is_dir() else [match]
                    for f in files:
                        hidden = any(p.startswith((".", "_")) for p in f.relative_to(match if match.is_dir() else f.parent).parts)
                        if f.suffix == ".md" and (with_hidden or not hidden):
                            found.append(f"{go_file}: {f}")
        return found

    def test_embedded_markdown_is_found_in_files_globs_and_directories(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            (root / "docs" / "sub").mkdir(parents=True)
            (root / "docs" / "sub" / "a.md").write_text("a")
            (root / "x.md").write_text("x")
            (root / "y.txt").write_text("y")
            cases = {"x.md": 1, "*.md": 1, "docs": 1, "all:docs": 1, "y.txt": 0, "*.txt": 0, '"x.md" y.txt': 1}
            for pattern, want in cases.items():
                with self.subTest(pattern=pattern):
                    src = root / "e.go"
                    src.write_text(f"package e\n\n//go:embed {pattern}\nvar f string\n")
                    self.assertEqual(want, len(self.embedded_markdown(src)))
            (root / "docs" / "sub" / "a.md").rename(root / "docs" / "sub" / "_a.md")
            src.write_text("package e\n\n//go:embed docs\nvar f string\n")
            self.assertEqual([], self.embedded_markdown(src))

    def test_no_go_embed_matches_markdown(self):
        found = []
        for path in ROOT.rglob("*.go"):
            if any(part in (".git", "node_modules", "worktrees") for part in path.relative_to(ROOT).parts):
                continue
            found += self.embedded_markdown(path)
        self.assertEqual([], found)


class BuildDockerfiles(unittest.TestCase):
    """K3: the Dockerfiles of flagd and flagd-proxy. A new hash for flagd-proxy means re-recording its image configuration."""

    PINNED = {
        "flagd/build.Dockerfile": "b95c00f2da32a69b9132c167051b8b435748029c5e732725b14a5b561604f65e",
        "flagd-proxy/build.Dockerfile": "19cbf8fb4450db31b5cbad5dc15fbc29fe3f9cfcab21f5d269e8bfb214207b2a",
    }

    def test_dockerfiles_are_unchanged(self):
        for path, want in self.PINNED.items():
            with self.subTest(path=path):
                self.assertEqual(want, sha256(path))


if __name__ == "__main__":
    unittest.main()
