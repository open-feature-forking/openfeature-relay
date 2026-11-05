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
        match = re.search(rf"^  {re.escape(name)}:\n(.*?)(?=^  [a-z][a-z-]*:\n|\Z)", jobs, re.S | re.M)
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
        names = re.findall(r"^  ([a-z][a-z-]*):\n", self.text.split("\njobs:\n", 1)[1], re.M)
        self.assertEqual(
            ["lint", "benchmark", "docs-check", "test", "fips", "docker-local", "upgrade-test", "release-dry-run"], names)

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
