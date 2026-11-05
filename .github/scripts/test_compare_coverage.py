"""Tests of compare-coverage.py against generated Go coverage profiles."""
import os
import pathlib
import subprocess
import sys
import tempfile
import unittest

HERE = pathlib.Path(__file__).parent
SCRIPT = HERE / "compare-coverage.py"
PKG = "github.com/open-feature-forking/openfeature-relay/core/pkg"


def profile(blocks):
    """A profile from {file: [(statements, count), ...]}; every block gets a range of its own."""
    out = "mode: atomic\n"
    for name, rows in blocks.items():
        for i, (statements, count) in enumerate(rows):
            out += f"{PKG}/{name}:{i + 1}.1,{i + 1}.20 {statements} {count}\n"
    return out


def run(base, head, summary=None):
    """Writes each side's profiles ({file name: text}) to a directory and compares them."""
    with tempfile.TemporaryDirectory() as tmp:
        dirs = []
        for side, files in (("base", base), ("head", head)):
            d = pathlib.Path(tmp) / side
            d.mkdir()
            for name, text in files.items():
                (d / name).write_text(text, encoding="utf-8")
            dirs.append(str(d))
        env = dict(os.environ)
        env.pop("GITHUB_STEP_SUMMARY", None)
        if summary:
            env["GITHUB_STEP_SUMMARY"] = str(pathlib.Path(tmp) / "summary.md")
        done = subprocess.run([sys.executable, str(SCRIPT), *dirs], capture_output=True, text=True, env=env)
        written = pathlib.Path(env["GITHUB_STEP_SUMMARY"]).read_text(encoding="utf-8") if summary else ""
        return done.returncode, done.stdout + done.stderr, written


def hundred(covered):
    """One package of 100 statements, `covered` of them covered."""
    return [(1, 1)] * covered + [(1, 0)] * (100 - covered)


class CompareCoverage(unittest.TestCase):
    def test_the_same_profiles_pass(self):
        p = {"core.out": profile({"a/a.go": hundred(80)})}
        code, text, _ = run(p, p)
        self.assertEqual(0, code, text)
        self.assertIn("1 packages compared: 0 lost more than 1 point", text)

    def test_a_drop_beyond_the_tolerance_fails(self):
        code, text, _ = run({"core.out": profile({"a/a.go": hundred(80)})}, {"core.out": profile({"a/a.go": hundred(78)})})
        self.assertEqual(1, code, text)
        self.assertIn("::error::coverage of core/pkg/a fell from 80.0% to 78.0%", text)

    def test_a_drop_within_the_tolerance_is_noise(self):
        code, text, _ = run({"core.out": profile({"a/a.go": hundred(80)})}, {"core.out": profile({"a/a.go": hundred(79)})})
        self.assertEqual(0, code, text)
        self.assertIn("1 lost less", text)

    def test_a_gain_passes_and_is_listed(self):
        code, text, _ = run({"core.out": profile({"a/a.go": hundred(80)})}, {"core.out": profile({"a/a.go": hundred(90)})})
        self.assertEqual(0, code, text)
        self.assertIn("| core/pkg/a | 80.0% | 90.0% | +10.0 |", text)

    def test_new_and_removed_packages_never_fail(self):
        code, text, _ = run({"core.out": profile({"a/a.go": hundred(80), "gone/g.go": hundred(90)})},
                            {"core.out": profile({"a/a.go": hundred(80), "new/n.go": hundred(0)})})
        self.assertEqual(0, code, text)
        self.assertIn("1 new, 1 removed", text)
        self.assertIn("New: core/pkg/new 0.0%", text)
        self.assertIn("Removed: core/pkg/gone", text)

    def test_packages_are_totalled_over_files_and_profiles(self):
        # a.go and b.go are one package; the second profile covers a block the first did not
        base = {"one.out": profile({"a/a.go": [(10, 1)], "a/b.go": [(10, 0)]})}
        head = {"one.out": profile({"a/a.go": [(10, 1)], "a/b.go": [(10, 0)]}),
                "two.out": profile({"a/b.go": [(10, 1)]})}
        code, text, _ = run(base, head)
        self.assertEqual(0, code, text)
        self.assertIn("| core/pkg/a | 50.0% | 100.0% | +50.0 |", text)

    def test_a_small_package_loses_more_per_statement(self):
        code, text, _ = run({"core.out": profile({"a/a.go": [(1, 1)] * 20})},
                            {"core.out": profile({"a/a.go": [(1, 1)] * 19 + [(1, 0)]})})
        self.assertEqual(1, code, text)

    def test_the_summary_is_written(self):
        p = {"core.out": profile({"a/a.go": hundred(80)})}
        code, _, summary = run(p, p, summary=True)
        self.assertEqual(0, code)
        self.assertIn("### Coverage against the base", summary)

    def test_an_empty_side_is_an_error(self):
        code, text, _ = run({}, {"core.out": profile({"a/a.go": hundred(80)})})
        self.assertEqual(2, code, text)
        self.assertIn("no coverage profiles in the base", text)


if __name__ == "__main__":
    unittest.main()
