"""Characterization of the release and build configuration that step 12 must leave as it is (C-003).

The hashes are of the files with line endings normalised to LF. A change to a pinned file is deliberate: record the
new hash in the same commit.
"""
import hashlib
import pathlib
import re
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
BUILD = ROOT / ".github" / "workflows" / "build.yaml"


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

    def test_paths_ignore_docs_on_push_and_pull_request(self):
        block = '    paths-ignore:\n      - "**/*.md"\n      - "docs/**"\n      - ".claude/**"\n'
        self.assertEqual(2, self.text.count(block))
        head = self.text.split("\njobs:\n", 1)[0]
        self.assertIn("  push:\n    branches:", head)
        self.assertIn("  pull_request:\n    branches:", head)

    def test_concurrency_group(self):
        """QD: a superseded pull request run is cancelled; a run on main never is."""
        head = self.text.split("\njobs:\n", 1)[0]
        self.assertIn(
            "concurrency:\n  group: build-${{ github.ref }}\n"
            "  cancel-in-progress: ${{ github.event_name == 'pull_request' }}\n", head)

    def test_nothing_is_pushed_or_logged_in(self):
        self.assertNotRegex(self.text, r"push:\s*true")
        self.assertNotIn("docker/login-action", self.text)

    def test_jobs(self):
        names = re.findall(r"^  ([a-z][a-z0-9-]*):[ \t]*(?:#.*)?\n", self.text.split("\njobs:\n", 1)[1], re.M)
        self.assertEqual(
            ["changes", "lint", "benchmark", "docs-check", "test", "miss-report", "builder-e2e", "fips", "docker-local", "integration-test",
             "upgrade-test", "release-dry-run"], names)

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

    def test_gated_jobs(self):
        gate = "if: vars.FULL_CI == 'true' || github.event_name == 'workflow_dispatch'"
        for name in ("fips", "docker-local"):
            with self.subTest(job=name):
                self.assertIn(gate, self.job(name))
        self.assertIn("if: github.event_name == 'pull_request'", self.job("benchmark"))

    def test_release_dry_run_runs_when_its_inputs_change(self):
        """Q16: the job has a gate step, like upgrade-test: FULL_CI, a manual run, or a change to the release's inputs."""
        job = self.job("release-dry-run")
        self.assertNotIn("\n    if:", job)
        self.assertIn('[ "$FULL_CI" = true ] || [ "$EVENT" = workflow_dispatch ]', job)
        self.assertIn(r"^\.github/scripts/testdata/|^\.github/workflows/release-please\.yaml$|^builder/|^distributions/|Dockerfile$", job)
        self.assertIn("if: steps.gate.outputs.run == 'true'", job)

    def all_jobs(self):
        """Every job's text by id, including ids with digits that job() cannot reach."""
        jobs = self.text.split("\njobs:\n", 1)[1]
        parts = re.split(r"(?m)^(?=  [a-z][a-z0-9-]*:[^\n]*\n)", jobs)
        return {m.group(1): part for part in parts if (m := re.match(r"  ([a-z0-9-]+):", part))}

    CACHED_JOBS = ("lint", "docs-check", "test", "builder-e2e", "fips", "integration-test", "upgrade-test", "release-dry-run")
    GATED_CACHED_JOBS = ("upgrade-test", "release-dry-run")
    ON_MAIN = "github.ref == 'refs/heads/main' && github.event_name == 'push'"

    def test_go_caches_are_saved_only_on_main(self):
        """P0: one key per job; every job restores it, and only the push to main saves it. setup-go's own cache has no
        root go.sum to key on, so it is off."""
        self.assertNotRegex(self.text, r"uses: actions/cache@")
        jobs = self.all_jobs()
        for name in self.CACHED_JOBS:
            with self.subTest(job=name):
                job = jobs[name]
                key = f"key: go-{name}-${{{{ hashFiles('**/go.sum') }}}}"
                self.assertEqual(2, job.count(key))
                self.assertIn(f"restore-keys: go-{name}-\n", job)
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
        for name in ("upgrade-test", "release-dry-run"):
            with self.subTest(job=name):
                job = jobs[name]
                self.assertIn("BASE: ${{ github.event.before }}\n", job)
                self.assertNotIn("base.sha", job)
                self.assertIn('if [ "$EVENT" = pull_request ]; then BASE=$(git rev-parse "$HEAD^1"); fi', job)
                self.assertIn('git diff --name-only "$BASE" "$HEAD"', job)

    def test_release_dry_run_parts(self):
        """Slice R: the dry run is a matrix of parts; a pull request builds the reduced set, `main`, FULL_CI and a manual run the full one."""
        job = self.job("release-dry-run")
        self.assertIn(
            "part: [flagd-standard, flagd-fips, flagd-proxy-standard, flagd-proxy-fips, images, distributions]", job)
        self.assertIn("fail-fast: false", job)
        self.assertIn("DRY_RUN_PART: ${{ matrix.part }}", job)
        self.assertIn("DRY_RUN_SCOPE: ${{ steps.gate.outputs.scope }}", job)
        self.assertIn('[ "$REF" = refs/heads/main ]', job)
        self.assertIn('echo "scope=$scope" >> "$GITHUB_OUTPUT"', job)

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

    def test_commands(self):
        self.assertIn("      - run: make generate-docs\n", self.text)
        self.assertIn("      - run: make test-upgrade-accepted\n", self.text)


class NoEmbeddedMarkdown(unittest.TestCase):
    """R7: a go:embed of a markdown file would be read by a build that markdown-only changes skip."""

    def test_no_go_embed_matches_markdown(self):
        found = []
        for path in ROOT.rglob("*.go"):
            if any(part in (".git", "node_modules", "worktrees") for part in path.relative_to(ROOT).parts):
                continue
            for line in path.read_text(encoding="utf-8", errors="replace").splitlines():
                if re.match(r"\s*//go:embed\b", line) and re.search(r"\.md\b", line):
                    found.append(f"{path.relative_to(ROOT)}: {line.strip()}")
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
