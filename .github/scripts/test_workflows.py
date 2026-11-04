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
        block = '    paths-ignore:\n      - "README.md"\n      - "docs/**"\n'
        self.assertEqual(2, self.text.count(block))
        head = self.text.split("\njobs:\n", 1)[0]
        self.assertIn("  push:\n    branches:", head)
        self.assertIn("  pull_request:\n    branches:", head)

    def test_nothing_is_pushed_or_logged_in(self):
        self.assertNotRegex(self.text, r"push:\s*true")
        self.assertNotIn("docker/login-action", self.text)

    def test_jobs(self):
        names = re.findall(r"^  ([a-z][a-z-]*):\n", self.text.split("\njobs:\n", 1)[1], re.M)
        self.assertEqual(
            ["lint", "benchmark", "docs-check", "test", "fips", "docker-local", "upgrade-test", "release-dry-run"], names)

    def test_gated_jobs(self):
        gate = "if: vars.FULL_CI == 'true' || github.event_name == 'workflow_dispatch'"
        for name in ("fips", "docker-local", "release-dry-run"):
            with self.subTest(job=name):
                self.assertIn(gate, self.job(name))
        self.assertIn("if: github.event_name == 'pull_request'", self.job("benchmark"))


class BuildDockerfiles(unittest.TestCase):
    """K3: the Dockerfiles of flagd and flagd-proxy. A new hash for flagd-proxy means re-recording its image configuration."""

    PINNED = {
        "flagd/build.Dockerfile": "6215615613a2a9e7615b16bdaa731b20a8dc58d67c98baaade550bcf51e4f055",
        "flagd-proxy/build.Dockerfile": "dbdc1298b2474bb454e13e623ae190f4c99d695d688bcdf02d849493f932a837",
    }

    def test_dockerfiles_are_unchanged(self):
        for path, want in self.PINNED.items():
            with self.subTest(path=path):
                self.assertEqual(want, sha256(path))


if __name__ == "__main__":
    unittest.main()
