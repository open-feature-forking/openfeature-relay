"""publish-gate.sh decides whether a run builds and publishes the images (step 17, Q6 (b))."""
import os
import pathlib
import shutil
import subprocess
import unittest

SCRIPT = pathlib.Path(__file__).resolve().parent / "publish-gate.sh"
REPO = "open-feature-forking/openfeature-relay"
NOW = 1_800_000_000


def gate(**env):
    base = {"EVENT": "push", "REF": "refs/heads/main", "REPOSITORY": REPO, "FULL_CI": "", "INPUTS_CHANGED": "true",
            "MAIN_CREATED": "", "NOW": str(NOW)}
    out = subprocess.run(["bash", str(SCRIPT)], env={**os.environ, **base, **env}, capture_output=True, text=True, check=True)
    return dict(line.split("=") for line in out.stdout.split())


def created(minutes_ago):
    return subprocess.run(["date", "-u", "-d", f"@{NOW - minutes_ago * 60}", "+%Y-%m-%dT%H:%M:%SZ"],
                          capture_output=True, text=True, check=True).stdout.strip()


@unittest.skipUnless(shutil.which("bash") and shutil.which("date"), "needs bash and GNU date")
class PublishGate(unittest.TestCase):
    def test_a_push_that_changed_nothing_does_not_publish(self):
        self.assertEqual({"build": "false", "publish": "false"}, gate(INPUTS_CHANGED="false"))

    def test_a_fresh_main_does_not_publish(self):
        self.assertEqual({"build": "false", "publish": "false"}, gate(MAIN_CREATED=created(179)))

    def test_an_old_main_publishes(self):
        self.assertEqual({"build": "true", "publish": "true"}, gate(MAIN_CREATED=created(181)))

    def test_a_missing_main_publishes(self):
        self.assertEqual({"build": "true", "publish": "true"}, gate(MAIN_CREATED=""))
        self.assertEqual({"build": "true", "publish": "true"}, gate(MAIN_CREATED="not a date"))

    def test_a_dispatch_on_main_publishes_at_once(self):
        self.assertEqual({"build": "true", "publish": "true"},
                         gate(EVENT="workflow_dispatch", INPUTS_CHANGED="false", MAIN_CREATED=created(1)))

    def test_a_dispatch_on_a_branch_builds_and_does_not_publish(self):
        self.assertEqual({"build": "true", "publish": "false"}, gate(EVENT="workflow_dispatch", REF="refs/heads/step-17/pr-b"))

    def test_a_pull_request_a_branch_push_and_a_fork_never_publish(self):
        for env in ({"EVENT": "pull_request", "REF": "refs/pull/1/merge"}, {"REF": "refs/heads/feature/workflows"},
                    {"REPOSITORY": "someone/openfeature-relay"}, {"EVENT": "workflow_dispatch", "REPOSITORY": "someone/openfeature-relay"}):
            with self.subTest(env=env):
                self.assertEqual("false", gate(**env)["publish"])

    def test_full_ci_builds_and_never_publishes_by_itself(self):
        self.assertEqual({"build": "true", "publish": "false"},
                         gate(EVENT="pull_request", REF="refs/pull/1/merge", FULL_CI="true"))


if __name__ == "__main__":
    unittest.main()
