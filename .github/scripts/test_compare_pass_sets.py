"""Tests of compare-pass-sets.py against small go test -json outputs."""
import json
import os
import pathlib
import subprocess
import sys
import tempfile
import unittest

SCRIPT = pathlib.Path(__file__).parent / "compare-pass-sets.py"


def events(passed=(), failed=()):
    out = [{"Action": "pass", "Package": "p"}]  # a package event has no test
    out += [{"Action": "pass", "Package": "p", "Test": name} for name in passed]
    out += [{"Action": "fail", "Package": "p", "Test": name} for name in failed]
    out += [{"Action": "output", "Package": "p", "Test": "TestRPC", "Output": "noise"}]
    return "\n".join(json.dumps(e) for e in out) + "\nnot json\n"


def compare(launchpad, relay):
    with tempfile.TemporaryDirectory() as tmp:
        paths = []
        for name, text in (("launchpad.json", launchpad), ("relay.json", relay)):
            path = pathlib.Path(tmp) / name
            path.write_text(text, encoding="utf-8")
            paths.append(str(path))
        env = dict(os.environ)
        env.pop("GITHUB_STEP_SUMMARY", None)
        done = subprocess.run([sys.executable, str(SCRIPT), *paths], capture_output=True, text=True, check=False, env=env)
    return done.returncode, done.stdout + done.stderr


class ComparePassSets(unittest.TestCase):
    def test_equal_pass_sets_pass(self):
        code, out = compare(events(passed=["TestRPC/a", "TestRPC/b"]), events(passed=["TestRPC/b", "TestRPC/a"]))
        self.assertEqual(0, code, out)

    def test_the_same_failures_on_both_are_not_a_difference(self):
        code, out = compare(events(passed=["TestRPC/a"], failed=["TestRPC/c"]), events(passed=["TestRPC/a"], failed=["TestRPC/c"]))
        self.assertEqual(0, code, out)

    def test_a_scenario_the_relay_testbed_fails_is_a_difference(self):
        code, out = compare(events(passed=["TestRPC/a", "TestRPC/b"]), events(passed=["TestRPC/a"], failed=["TestRPC/b"]))
        self.assertEqual(1, code)
        self.assertIn("passes on the launchpad and not on the relay testbed: TestRPC/b", out)

    def test_a_scenario_only_the_relay_testbed_passes_is_a_difference(self):
        code, out = compare(events(passed=["TestRPC/a"]), events(passed=["TestRPC/a", "TestRPC/b"]))
        self.assertEqual(1, code)
        self.assertIn("passes on the relay testbed and not on the launchpad: TestRPC/b", out)

    def test_a_run_that_passed_nothing_fails(self):
        code, out = compare(events(), events())
        self.assertEqual(1, code)
        self.assertIn("the launchpad run passed no scenario", out)


if __name__ == "__main__":
    unittest.main()
