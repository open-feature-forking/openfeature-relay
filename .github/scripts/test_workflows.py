"""Characterization of the release and build configuration that step 12 must leave as it is (C-003).

The hashes are of the files with line endings normalised to LF. A change to a pinned file is deliberate: record the
new hash in the same commit.
"""
import hashlib
import importlib.util
import os
import pathlib
import re
import shlex
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
BUILD = ROOT / ".github" / "workflows" / "build.yaml"
RELEASE_CHECK = ROOT / ".github" / "workflows" / "release-check.yaml"

_spec = importlib.util.spec_from_file_location("ci_modules", ROOT / ".github" / "scripts" / "ci-modules.py")
cm = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(cm)
FLAGS = cm.FLAG_PATTERNS


def sha256(relative):
    data = (ROOT / relative).read_bytes().replace(b"\r\n", b"\n")
    return hashlib.sha256(data).hexdigest()


class ReleaseConfiguration(unittest.TestCase):
    """K2: flagd and flagd-proxy release exactly as today."""

    PINNED = {
        ".github/workflows/release-please.yaml": "7bd0c065fbc00d294c5988aa83b31389f8fa5b3baf1f673f1b8be02bd08b4f0d",
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

    def test_every_action_is_pinned_to_a_commit(self):
        """A mangled `uses:` (a stray character in place of the @) is still valid YAML, and GitHub rejects the workflow."""
        for text in (self.text, RELEASE_CHECK.read_text(encoding="utf-8")):
            for ref in re.findall(r"^\s*(?:- )?uses: (\S+)", text, re.M):
                self.assertRegex(ref, r"^[\w./-]+@[0-9a-f]{40}$")

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
            ["changes", "lint", "benchmark", "test", "docker-local", "integration-test", "upgrade-test", "image-test",
             "publish-gate", "release-dry-run", "image-build", "testbed", "testbed-rpc-outages", "testbed-grpc-sync",
             "load-smoke", "load-test"], names)

    GATED = {
        "docker-local": "needs.changes.outputs.image == 'true'",
        "upgrade-test": "needs.changes.outputs.upgrade == 'true'",
        "image-test": "needs.changes.outputs.reference == 'true'",
    }

    def test_changes_is_the_single_gate(self):
        """The jobs that cost a runner for a change that cannot break them wait for a flag of `changes` and take no runner
        without it; there is no gate step in a job, and no miss report."""
        self.assertNotIn("miss-report", self.text)
        for name, condition in self.GATED.items():
            with self.subTest(job=name):
                job = self.all_jobs()[name]
                self.assertIn("    needs: changes\n", job)
                self.assertIn(f"    if: {condition}\n", job)
                for gone in ("steps.gate", "git diff --name-only", "vars.FULL_CI", "github.event.before"):
                    self.assertNotIn(gone, job)
        self.assertEqual([], [n for n in ("lint", "test", "integration-test", "release-dry-run", "load-smoke", "load-test")
                              if re.search(r"needs:.*\bchanges\b", self.all_jobs()[n])])
        changes = self.job("changes")
        self.assertIn("fetch-depth: 2", changes)
        self.assertIn("python3 .github/scripts/ci-modules.py changes", changes)
        for flag in FLAGS:
            self.assertIn(f"      {flag}: ${{{{ steps.affected.outputs.{flag} }}}}\n", changes)

    def test_test_runs_make_test(self):
        job = self.job("test")
        self.assertIn("- run: make test 2>&1 | tee test.log\n        shell: bash", job)
        self.assertIn("CONTRACT_EXTERNAL_COMMIT: ${{ github.event.pull_request.head.sha || github.sha }}", job)
        # the log stays for a failure summary; there is no artifact of it and no miss report to read one
        self.assertNotIn("path: test.log", job)

    def test_docs_check_and_builder_e2e_are_folded_into_other_jobs(self):
        """docs-check runs first in lint, while the tree is clean; builder-e2e is integration-test's flagd-full build."""
        names = re.findall(r"^  ([a-z][a-z0-9-]*):", self.text.split("\njobs:\n", 1)[1], re.M)
        self.assertNotIn("docs-check", names)
        self.assertNotIn("builder-e2e", names)
        lint = self.job("lint")
        self.assertLess(lint.index("make workspace-init"), lint.index("- run: make generate-docs"))
        self.assertLess(lint.index("- run: make generate-docs"), lint.index("git status --porcelain"))
        self.assertLess(lint.index("git status --porcelain"), lint.index("- run: make lint"))
        integration = self.all_jobs()["integration-test"]
        self.assertIn("run: make build-flagd-full", integration)
        self.assertNotIn("check-flagd-full-generated", integration)
        self.assertLess(integration.index("make workspace-init"), integration.index("make build-flagd-full"))
        self.assertIn("run: make build-flagsmith", integration)
        self.assertLess(integration.index("make build-flagd-full"), integration.index("make build-flagsmith"))
        self.assertLess(integration.index("make build-flagsmith"), integration.index("make workspace-clean"))

    def test_flags_follow_the_diff(self):
        """The flag patterns are the conditions the gate steps of the jobs used to hold."""
        def on(flag, path):
            return bool(cm.flags([path])[flag])

        # the differential runs on a pull request that touches what it is built from, and Go code elsewhere reaches it
        # through the nightly run
        for path in ("flagd/build.Dockerfile", "distributions/flagd-full/manifest.yaml", "builder/main.go",
                     "flagd/cmd/main.go", "test/upgrade/x_test.go", ".github/workflows/build.yaml", "x/Dockerfile"):
            self.assertTrue(on("upgrade", path), path)
        for path in ("README.md", "docs/vision.md", "Makefile", "core/pkg/evaluator/x.go", "service/ofrep/x.go",
                     "sync/http/go.mod", "runtime/go.sum", "go.sum", "testbed/cmd/main.go"):
            self.assertFalse(on("upgrade", path), path)
        for path in ("builder/reference/Dockerfile", "test/image/x_test.go", "flagd/build.Dockerfile"):
            self.assertTrue(on("reference", path), path)
        for path in ("core/pkg/x.go", "README.md", "test/upgrade/x_test.go"):
            self.assertFalse(on("reference", path), path)
        for path in ("go.sum", "core/go.mod", "flagd/build.Dockerfile", "distributions/flagd-full/Dockerfile"):
            self.assertTrue(on("image", path), path)
        for path in ("core/pkg/x.go", "README.md"):
            self.assertFalse(on("image", path), path)
        # the testbed and the benchmarks are not on pull requests, so no flag of the diff gates them
        self.assertNotIn("testbed", FLAGS)
        self.assertNotIn("benchmark", FLAGS)

    def test_tools_ci_sets_no_flag(self):
        """tools/ci is the repository's CI tooling: its Go files and module files call for no gated job."""
        for path in ("tools/ci/main.go", "tools/ci/go.mod", "tools/ci/go.sum", "tools/ci/internal/textio/x.go",
                     "tools/ci/internal/modules/deeper/x.go", "tools/ci/internal/oci/x.go",
                     "tools/ci/internal/publish/x.go", "tools/ci/internal/allocs/x.go",
                     "tools/ci/internal/passsets/x.go", "tools/ci/Dockerfile"):
            with self.subTest(path=path):
                self.assertEqual([], [n for n, v in cm.flags([path]).items() if v])
        for flag in FLAGS:
            with self.subTest(flag=flag):
                self.assertIn(flag, cm.FLAG_EXCLUDES)
                self.assertEqual(r"^tools/ci/", cm.FLAG_EXCLUDES[flag])
        # the exclusion does not reach a path outside tools/ci
        self.assertTrue(cm.flags(["core/tools/ci/x.go"])["go"])
        self.assertTrue(cm.flags(["go.mod"])["deps"])

    def test_every_flag_is_true_without_a_diff_and_none_is_true_for_a_change_that_touches_nothing(self):
        self.assertEqual({n: True for n in FLAGS}, cm.flags(None))
        self.assertFalse(any(cm.flags([]).values()))

    def test_fips_is_not_run_in_ci(self):
        """FIPS is a documented build with relay-builder, not a job, a flag or a leg of the dry run; the test target and the
        Dockerfile's build arg stay for the guide."""
        self.assertNotIn("fips", FLAGS)
        self.assertNotIn("fips", self.text.lower())
        self.assertNotIn("fips", RELEASE_CHECK.read_text(encoding="utf-8").lower())
        # the release builds the standard variant only: no FIPS step, archives or image tags, and no variant to choose
        self.assertNotIn("fips", (ROOT / ".github" / "workflows" / "release-please.yaml").read_text(encoding="utf-8").lower())
        build = (ROOT / ".github" / "scripts" / "release-build.sh").read_text(encoding="utf-8")
        self.assertNotIn("fips", build.lower())
        self.assertNotIn("RELEASE_VARIANTS", build)
        for script in ("release-dry-run.sh", "release-dry-run-distributions.sh"):
            with self.subTest(script=script):
                lines = [l for l in (ROOT / ".github" / "scripts" / script).read_text(encoding="utf-8").splitlines()
                         if "fips" in l.lower() and "${fips}" not in l and "FIPS=" not in l and 'local fips="$1"' not in l and "fips=off" not in l]
                self.assertEqual([], lines)
        self.assertRegex((ROOT / "Makefile").read_text(encoding="utf-8"), r"(?m)^test-fips:")
        self.assertRegex((ROOT / "flagd" / "build.Dockerfile").read_text(encoding="utf-8"), r"(?m)^ARG FIPS")

    def test_the_test_job_summarizes_its_failed_tests(self):
        """#190: a failed test job lists each failed test with its package, time and first error; report-only."""
        self.assertIn("if: failure()", self.job("test"))
        self.assertIn("test-failures.py --log test.log", self.job("test"))
        self.assertIn("run: python3 .github/scripts/test_test_failures.py\n", self.job("lint"))

    def test_lint_runs_the_module_table_tests(self):
        """C1 and C2 are the parity gate for make test and make lint: CI has to run them."""
        self.assertIn("run: python3 .github/scripts/test_ci_modules.py\n", self.job("lint"))
        self.assertIn("run: python3 .github/scripts/test_ci_modules_affected.py\n", self.job("lint"))

    NIGHTLY_ONLY = "if: github.event_name == 'schedule' || github.event_name == 'workflow_dispatch'"

    def test_gated_jobs(self):
        gate = "if: vars.FULL_CI == 'true' || github.event_name == 'workflow_dispatch' || github.event_name == 'schedule'"
        for name in ("testbed-rpc-outages", "testbed-grpc-sync"):
            with self.subTest(job=name):
                self.assertIn(gate, self.job(name))
        self.assertIn(self.NIGHTLY_ONLY, self.job("benchmark"))
        self.assertIn(self.NIGHTLY, self.job("testbed"))
        for name in ("benchmark", "testbed"):
            with self.subTest(job=name):
                self.assertNotIn("pull_request", self.job(name).split("steps:", 1)[0].replace("not on pull requests", ""))

    def test_load_smoke_runs_on_every_build_and_load_test_runs_nightly(self):
        """W: the 60 s race-build smoke is on every build, the 10 min run is nightly, and both keep their logs on failure."""
        self.assertNotIn("if:", self.job("load-smoke").split("steps:", 1)[0])
        self.assertIn("run: make test-load-smoke", self.job("load-smoke"))
        self.assertIn(self.NIGHTLY, self.job("load-test"))
        self.assertRegex(self.job("load-test"), r"run: make test-load\s")
        for name in ("load-smoke", "load-test"):
            with self.subTest(job=name):
                self.assertIn("RELAY_LOAD_LOGS:", self.job(name))
                self.assertIn("if: failure()", self.job(name))

    def test_markdown_checks_push_only_on_main_and_the_inert_pages_workflow_is_gone(self):
        text = (ROOT / ".github" / "workflows" / "markdown-checks.yaml").read_text(encoding="utf-8").replace("\r\n", "\n")
        self.assertIn("  push:\n    branches:\n      - main\n    paths:\n", text)
        self.assertFalse((ROOT / ".github" / "workflows" / "dev-benches-page.yml").exists())

    def test_load_image_pinned(self):
        """W: the k6 image is pulled by digest; no workflow or script names a grafana/k6 tag without one."""
        makefile = (ROOT / "Makefile").read_text(encoding="utf-8")
        self.assertRegex(makefile, r"(?m)^K6_IMAGE \?= grafana/k6:\d+\.\d+\.\d+@sha256:[0-9a-f]{64}$")
        for path in [*(ROOT / ".github").rglob("*.y*ml"), *(ROOT / ".github").rglob("*.py"), ROOT / "Makefile"]:
            for line in path.read_text(encoding="utf-8", errors="replace").splitlines():
                if path.name != "test_workflows.py":
                    for ref in re.findall(r"grafana/k6:[^\s'\"]+", line):
                        with self.subTest(path=path.name, ref=ref):
                            self.assertIn("@sha256:", ref)

    def test_build_never_sees_a_label_event(self):
        """The release-check label starts release-check.yaml, not build.yaml: a label event in build.yaml would cancel the
        pull request's running jobs through the concurrency group and report skipped jobs under the real check names."""
        self.assertNotIn("github.event.action", self.text)
        self.assertNotIn("github.event.label", self.text)
        self.assertNotIn("types:", self.text)

    NIGHTLY = "if: vars.FULL_CI == 'true' || github.event_name == 'workflow_dispatch' || github.event_name == 'schedule'"

    def test_release_dry_run_runs_nightly(self):
        """build.yaml's dry run is the full set: nightly, FULL_CI or a manual run, and not on a push to main. A pull request
        gets the reduced set from release-check.yaml, when it carries the label."""
        job = self.job("release-dry-run")
        self.assertIn("    " + self.NIGHTLY + "\n", job)
        for gone in ("steps.gate", "git diff", "BASE", "DRY_RUN_SCOPE", "DRY_RUN_DISTS"):
            self.assertNotIn(gone, job)

    def test_the_benchmark_compares_with_the_commit_a_day_older_and_skips_without_one(self):
        job = self.job("benchmark")
        self.assertIn("fetch-depth: 0", job)
        self.assertIn("git rev-list -1 --before='24 hours ago' \"$GITHUB_SHA\"", job)
        self.assertIn('[ "$base" = "$GITHUB_SHA" ]', job)
        self.assertIn("compare-allocs.py", job)
        self.assertEqual(4, job.count("if: steps.base.outputs.run == 'true'"))

    def test_every_job_that_moved_to_the_nightly_run_accepts_the_schedule_event(self):
        """A job whose condition lacks `schedule` silently skips there. The jobs behind a flag run because every flag is
        true without a diff, and the cache of the nightly run is saved."""
        for name in ("release-dry-run", "load-test", "testbed-rpc-outages", "testbed-grpc-sync", "testbed"):
            with self.subTest(job=name):
                self.assertIn(self.NIGHTLY, self.job(name))
        self.assertIn(self.NIGHTLY_ONLY, self.job("benchmark"))
        for name in self.GATED:
            with self.subTest(job=name):
                self.assertNotIn("schedule", self.all_jobs()[name].split("steps:", 1)[0].replace("on the schedule", ""))
        self.assertIsNone(cm.flag_change_set(ROOT, "schedule", "HEAD", ""))
        self.assertIn("(github.event_name == 'push' || github.event_name == 'schedule')", self.all_jobs()["release-dry-run"])

    def test_image_tests_are_their_own_job_and_docker_local_has_no_qemu(self):
        upgrade = self.all_jobs()["upgrade-test"]
        self.assertIn("make upgrade-test", upgrade)
        self.assertNotIn("make image-test", upgrade)
        image = self.all_jobs()["image-test"]
        self.assertIn("run: make image-test", image)
        self.assertIn("timeout-minutes: 75", image)
        self.assertNotIn("qemu", self.all_jobs()["docker-local"].lower())

    def all_jobs(self):
        """Every job's text by id, including ids with digits that job() cannot reach."""
        jobs = self.text.split("\njobs:\n", 1)[1]
        parts = re.split(r"(?m)^(?=  [a-z][a-z0-9-]*:[^\n]*\n)", jobs)
        return {m.group(1): part for part in parts if (m := re.match(r"  ([a-z0-9-]+):", part))}

    CACHED_JOBS = ("lint", "test", "integration-test", "upgrade-test", "image-test", "release-dry-run", "load-smoke", "load-test")
    GATED_CACHED_JOBS = ()
    # the nightly run saves the caches of the jobs that run there
    ON_MAIN = "github.ref == 'refs/heads/main' && (github.event_name == 'push' || github.event_name == 'schedule')"

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
        request was opened and holds what the base gained since. The gated jobs get theirs from `changes`."""
        for name in self.GATED:
            with self.subTest(job=name):
                job = self.all_jobs()[name]
                self.assertNotIn("git diff --name-only", job)
                self.assertNotIn("base.sha", job)
        check = RELEASE_CHECK.read_text(encoding="utf-8")
        self.assertIn('BASE=$(git rev-parse "$HEAD^1")', check)
        self.assertNotIn("base.sha", check)

    def test_the_jobs_after_the_gate_keep_their_steps(self):
        testbed = self.all_jobs()["testbed"]
        self.assertIn("make testbed-contract-both", testbed)
        self.assertIn("make testbed-suites", testbed)
        docker = self.all_jobs()["docker-local"]
        self.assertIn("security-events: write", docker)
        self.assertIn("aquasecurity/trivy-action", docker)

    def test_outage_and_grpc_source_suites_run_nightly_on_main(self):
        """Coverage gap 7: a cron schedule (which GitHub runs on the default branch only) starts both suites."""
        head = self.text.split("\njobs:\n", 1)[0]
        self.assertRegex(head, r'  schedule:\n    - cron: "[0-9*/, ]+ [0-9*/, ]+ \* \* \*"\n')
        for name in ("testbed-rpc-outages", "testbed-grpc-sync"):
            with self.subTest(job=name):
                self.assertIn("github.event_name == 'schedule'", self.job(name))
        # the benchmark comparison runs nightly as well, against the newest commit that is a day older
        self.assertIn(self.NIGHTLY_ONLY, self.job("benchmark"))

    def test_coverage_profiles_are_kept(self):
        """#208: the profiles are an artifact for a coverage tracker; the comparison with the base was dropped by the
        project lead, and the Codecov step that failed on every run for want of a token is gone."""
        self.assertNotIn("coverage job", self.text)
        self.assertNotIn("codecov", self.text.lower())
        test = self.job("test")
        self.assertIn('name: coverage-profiles' + chr(10) + '          path: "*-coverage.out"', test)
        self.assertNotIn("compare-coverage", self.text)

    def test_outage_job_compares_both_images(self):
        """UC-022 Q16: the gated job runs the RPC suite with the outage scenarios against both images."""
        self.assertIn("make testbed-rpc-outages", self.job("testbed-rpc-outages"))

    PARTS = "part: [flagd-standard, flagd-proxy-standard, images, distributions]"

    def test_release_dry_run_parts(self):
        """Slice R: the dry run is a matrix of parts, the full set nightly, with FULL_CI and manually."""
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
        # it reads the cache build.yaml's dry run saves from the nightly run and saves nothing itself
        self.assertIn("key: go-release-dry-run-${{ matrix.part }}-${{ hashFiles('**/go.sum') }}", text)
        self.assertNotIn("cache/save", text)
        self.assertNotRegex(text, r"push:\s*true")
        self.assertNotIn("docker/login-action", text)

    def test_publish_gate_job(self):
        """Step 17: one small job decides whether a run builds and publishes the images; the image jobs wait for it."""
        gate = self.job("publish-gate")
        self.assertNotIn("permissions:", gate)
        self.assertIn(".github/scripts/publish-gate.sh", gate)
        # tools/ci is CI tooling: its files do not count as image inputs, except the packages that publish (step 18, plan 4)
        self.assertIn("grep -Ev '^tools/ci/'", gate)
        self.assertLess(gate.index("grep -Ev '^tools/ci/'"), gate.index('grep -Eq "$pattern"'))
        self.assertIn("|^tools/ci/internal/(oci|publish)/'", gate)
        # the group must exit 0 when nothing matches, or a diff of core/x.go alone reads as "not changed" under pipefail
        self.assertIn("grep -E '^tools/ci/internal/(oci|publish)/' || true; } | grep -Eq", gate)
        self.assertIn("build: ${{ steps.gate.outputs.build }}", gate)
        self.assertIn("publish: ${{ steps.gate.outputs.publish }}", gate)
        for name in ("release-dry-run", "image-build"):
            with self.subTest(job=name):
                self.assertIn("needs: publish-gate\n", self.job(name))
        # the testbed runs nightly, with FULL_CI and manually, and tests the layout of a run that builds the images
        testbed = self.job("testbed")
        self.assertIn("    needs: publish-gate\n", testbed)
        self.assertIn(self.NIGHTLY, testbed)
        self.assertIn("if: needs.publish-gate.outputs.build == 'true'", self.job("image-build"))

    def test_image_build_parts_cover_the_other_five_distributions(self):
        """Step 17: flagd-minimal is built by the distributions part and the testbed by its job; the rest by image-build,
        as layouts checked as layouts and run from the layout. FIPS is not published."""
        job = self.job("image-build")
        listed = " ".join(re.findall(r'distributions: "([^"]+)"', job)).split()
        self.assertEqual(["flagd-cloud", "flagd-full", "flagd-kubernetes", "flagd-minimal-otel", "flagd-sync"], sorted(listed))
        self.assertIn(".github/scripts/image-build.sh", job)
        self.assertIn('"0.0.0-main.${GITHUB_SHA::7}"', job)
        script = (ROOT / ".github" / "scripts" / "image-build.sh").read_text(encoding="utf-8")
        # no FIPS image is published: the build has one variant and no FIPS build argument
        self.assertNotIn("fips", script.lower().replace("no fips images", ""))
        self.assertIn("check_layout", script)
        self.assertIn("check_runtime", script)
        self.assertIn('source_dir=flagd\n', script)

    def test_the_testbed_runs_on_the_layout_that_is_published(self):
        job = self.job("testbed")
        self.assertIn(".github/scripts/image-testbed.sh", job)
        self.assertIn("make testbed-contract-both TESTBED_RELAY_IMAGE=flagd-testbed:layout", job)
        self.assertIn("make testbed-suites TESTBED_RELAY_IMAGE=flagd-testbed:layout", job)
        self.assertIn("name: layouts-flagd-testbed", job)

    def test_images_are_marked_as_a_proof_of_concept(self):
        """Q4: labels and annotations come from the build scripts, never from flagd's or flagd-proxy's Dockerfile (K3)."""
        layout = (ROOT / ".github" / "scripts" / "oci-layout.py").read_text(encoding="utf-8")
        self.assertIn("Proof of concept from the OpenFeature Relay incubator, built from main. ", layout)
        self.assertIn("Not an official OpenFeature image; not for production.", layout)
        self.assertNotIn("vendor", layout)
        lib = (ROOT / ".github" / "scripts" / "image-lib.sh").read_text(encoding="utf-8")
        self.assertIn('--label "${kv}" --annotation "index:${kv}" --annotation "manifest:${kv}"', lib)
        for dockerfile in ("flagd/build.Dockerfile", "flagd-proxy/build.Dockerfile"):
            self.assertNotIn("org.opencontainers.image", (ROOT / dockerfile).read_text(encoding="utf-8"))

    def test_release_dry_run_full_covers_todays_set(self):
        """Slice R: the parts together build the standard archives of flagd and flagd-proxy, the image and the five distributions."""
        script = (ROOT / ".github" / "scripts" / "release-dry-run.sh").read_text(encoding="utf-8")
        parts = re.search(r"part: \[(.*)\]", self.job("release-dry-run")).group(1)
        for item in ("flagd", "flagd-proxy"):
            self.assertIn(f"{item}-standard", parts)
        # the default part runs everything, and release-build.sh is told only the platforms the scope narrows
        self.assertIn('part="${DRY_RUN_PART:-all}"', script)
        self.assertIn('[ "${scope}" != reduced ] || platforms="linux/amd64"', script)
        for label in ("Darwin_arm64 Darwin_x86_64 Linux_arm64 Linux_x86_64", "Windows_x86_64 Windows_i386"):
            self.assertIn(label, script)
        for tag in (":latest", ":${{ env.VERSION }}"):
            self.assertIn(tag, script)
        dists = (ROOT / ".github" / "scripts" / "release-dry-run-distributions.sh").read_text(encoding="utf-8")
        self.assertIn('others="flagd-minimal-otel flagd-cloud flagd-kubernetes flagd-sync"', dists)
        self.assertIn('platforms="linux/amd64"', dists)


    def test_grpc_sync_job_runs_the_testbed_test(self):
        """UC-023 Q14: the gated job runs flagd-full with a grpc source against the relay testbed."""
        self.assertIn("make testbed-grpc-sync", self.job("testbed-grpc-sync"))

class WorkflowPermissions(unittest.TestCase):
    """The repository's default token may write, so every workflow narrows it: a top-level block that reads, and job-level
    grants only where a job calls an API that needs them. Nothing grants packages: write."""

    @classmethod
    def setUpClass(cls):
        try:
            import yaml
        except ImportError:
            if os.environ.get("GITHUB_ACTIONS") == "true":
                raise
            raise unittest.SkipTest("PyYAML is not installed")
        cls.files = sorted((ROOT / ".github" / "workflows").glob("*.y*ml"))
        cls.docs = {f.name: yaml.safe_load(f.read_text(encoding="utf-8")) for f in cls.files}

    def test_every_workflow_parses_and_has_a_top_level_permissions_block(self):
        self.assertIn("build.yaml", self.docs)
        for name, doc in self.docs.items():
            with self.subTest(workflow=name):
                self.assertIsInstance(doc.get("permissions"), dict, "a top-level permissions block narrows the default token")
                self.assertIn("jobs", doc)

    def test_the_top_level_block_only_reads_unless_a_deploy_workflow_says_why(self):
        # the Pages deploy workflows are upstream flagd's, gated to open-feature/flagd
        deploy = {"docs.yaml": {"contents": "read"}}
        for name, doc in self.docs.items():
            with self.subTest(workflow=name):
                want = {"lint-pr.yaml": {"pull-requests": "write"}}.get(name) or deploy.get(name) or {"contents": "read"}
                self.assertEqual(want, doc["permissions"])

    def test_nothing_grants_packages_write(self):
        for f in self.files:
            with self.subTest(workflow=f.name):
                self.assertNotRegex(f.read_text(encoding="utf-8"), r"packages:\s*write")

    def test_the_only_registry_login_is_the_pinned_release_workflow(self):
        """docker/login-action appears in release-please.yaml only (gated to open-feature/flagd, never enabled here); the
        hash in ReleaseConfiguration pins that file, so a second login or a change to this one fails a test."""
        with_login = [f.name for f in self.files if "docker/login-action" in f.read_text(encoding="utf-8")]
        self.assertEqual(["release-please.yaml"], with_login)
        self.assertIn(".github/workflows/release-please.yaml", ReleaseConfiguration.PINNED)

    def test_lint_pr_runs_on_pull_request_target_without_the_pull_requests_code(self):
        text = (ROOT / ".github" / "workflows" / "lint-pr.yaml").read_text(encoding="utf-8")
        self.assertIn("pull_request_target:", text)
        for gone in ("actions/checkout", "github.event.pull_request.head", "run:"):
            self.assertNotIn(gone, text)


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
        self.assertEqual(["docs-check"], names)

    def test_the_check_is_not_one_of_the_build_workflows(self):
        """build.yaml folded its docs-check job into lint, so the name is the light workflow's alone."""
        build = BUILD.read_text(encoding="utf-8")
        self.assertNotRegex(build, r"(?m)^  docs-check:")
        self.assertNotIn("name: docs-check\n", self.text.split("\njobs:\n", 1)[1])

    def test_commands(self):
        self.assertIn("      - run: make generate-docs\n", self.text)
        self.assertIn("      - run: make test-upgrade-accepted\n        if: ${{ !cancelled() }}\n", self.text)


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
