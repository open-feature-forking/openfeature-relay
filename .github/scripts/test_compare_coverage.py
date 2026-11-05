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


def run(base, head, summary=None, trees=None):
    """Writes each side's profiles ({file name: text}) to a directory and compares them. trees is
    {"base": [paths], "head": [paths]}, the files of the two checkouts, whose roots are then passed."""
    with tempfile.TemporaryDirectory() as tmp:
        dirs = []
        for side, files in (("base", base), ("head", head)):
            d = pathlib.Path(tmp) / side
            d.mkdir()
            for name, text in files.items():
                (d / name).write_text(text, encoding="utf-8")
            dirs.append(str(d))
        roots = []
        for side, paths in (trees or {}).items():
            for path in paths:
                f = pathlib.Path(tmp) / f"{side}-root" / path
                f.parent.mkdir(parents=True, exist_ok=True)
                f.write_text("package x"+chr(10), encoding="utf-8")
            (pathlib.Path(tmp) / f"{side}-root").mkdir(exist_ok=True)
            roots += [f"--{side}-root", str(pathlib.Path(tmp) / f"{side}-root")]
        env = dict(os.environ)
        env.pop("GITHUB_STEP_SUMMARY", None)
        if summary:
            env["GITHUB_STEP_SUMMARY"] = str(pathlib.Path(tmp) / "summary.md")
        done = subprocess.run([sys.executable, str(SCRIPT), *roots, *dirs], capture_output=True, text=True, env=env)
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

    BASE = {"core.out": profile({"a/a.go": hundred(80), "gone/g.go": hundred(90)})}
    HEAD = {"core.out": profile({"a/a.go": hundred(80), "new/n.go": hundred(0)})}

    def test_a_new_package_never_fails_and_a_deleted_one_is_listed(self):
        code, text, _ = run(self.BASE, self.HEAD, trees={"base": ["core/pkg/gone/g.go"], "head": ["core/pkg/a/a.go"]})
        self.assertEqual(0, code, text)
        self.assertIn("1 new, 1 removed, 0 without a profile", text)
        self.assertIn("New: core/pkg/new 0.0%", text)
        self.assertIn("Removed: core/pkg/gone", text)

    def test_a_package_without_a_profile_in_the_head_fails(self):
        # the module stopped writing its profile: the directory still has Go files
        code, text, _ = run(self.BASE, self.HEAD, trees={"base": ["core/pkg/gone/g.go"], "head": ["core/pkg/gone/g.go"]})
        self.assertEqual(1, code, text)
        self.assertIn("::error::core/pkg/gone has a coverage profile in the base and none in the head", text)
        self.assertIn("No profile in the head: core/pkg/gone", text)

    def test_a_package_whose_directory_is_not_in_the_base_checkout_fails(self):
        code, text, _ = run(self.BASE, self.HEAD, trees={"base": ["unrelated/x.go"], "head": []})
        self.assertEqual(1, code, text)

    def test_without_the_checkouts_every_missing_package_fails(self):
        code, text, _ = run(self.BASE, self.HEAD)
        self.assertEqual(1, code, text)

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
