"""publish-gate.sh decides whether a run builds and publishes the images: the nightly run and a manual run on main (step 17)."""
import os
import pathlib
import shutil
import subprocess
import unittest

SCRIPT = pathlib.Path(__file__).resolve().parent / "publish-gate.sh"
REPO = "open-feature-forking/openfeature-relay"


def gate(**env):
    base = {"EVENT": "schedule", "REF": "refs/heads/main", "REPOSITORY": REPO, "FULL_CI": "", "INPUTS_CHANGED": "true"}
    out = subprocess.run(["bash", str(SCRIPT)], env={**os.environ, **base, **env}, capture_output=True, text=True, check=True)
    return dict(line.split("=") for line in out.stdout.split())


@unittest.skipUnless(shutil.which("bash"), "needs bash")
class PublishGate(unittest.TestCase):
    def test_the_nightly_run_publishes_when_image_inputs_changed(self):
        self.assertEqual({"build": "true", "publish": "true"}, gate())

    def test_the_nightly_run_does_not_publish_when_nothing_changed(self):
        self.assertEqual({"build": "false", "publish": "false"}, gate(INPUTS_CHANGED="false"))

    def test_a_push_never_publishes_or_builds(self):
        self.assertEqual({"build": "false", "publish": "false"}, gate(EVENT="push"))

    def test_a_dispatch_on_main_publishes_at_once(self):
        self.assertEqual({"build": "true", "publish": "true"}, gate(EVENT="workflow_dispatch", INPUTS_CHANGED="false"))

    def test_a_dispatch_on_a_branch_builds_and_does_not_publish(self):
        self.assertEqual({"build": "true", "publish": "false"}, gate(EVENT="workflow_dispatch", REF="refs/heads/step-17/pr-b"))

    def test_a_pull_request_a_branch_and_a_fork_never_publish(self):
        for env in ({"EVENT": "pull_request", "REF": "refs/pull/1/merge"}, {"REF": "refs/heads/feature/workflows"},
                    {"REPOSITORY": "someone/openfeature-relay"}, {"EVENT": "workflow_dispatch", "REPOSITORY": "someone/openfeature-relay"}):
            with self.subTest(env=env):
                self.assertEqual("false", gate(**env)["publish"])

    def test_full_ci_builds_and_never_publishes_by_itself(self):
        self.assertEqual({"build": "true", "publish": "false"},
                         gate(EVENT="pull_request", REF="refs/pull/1/merge", FULL_CI="true"))


if __name__ == "__main__":
    unittest.main()
