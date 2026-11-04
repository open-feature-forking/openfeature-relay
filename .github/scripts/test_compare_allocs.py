"""Tests of compare-allocs.py against saved outputs of go test -bench -benchmem (testdata/)."""
import os
import pathlib
import subprocess
import sys
import tempfile
import unittest

HERE = pathlib.Path(__file__).parent
SCRIPT = HERE / "compare-allocs.py"
DATA = HERE / "testdata"


def compare(base, head, summary=None):
    env = dict(os.environ)
    env.pop("GITHUB_STEP_SUMMARY", None)
    if summary:
        env["GITHUB_STEP_SUMMARY"] = str(summary)
    done = subprocess.run([sys.executable, str(SCRIPT), str(base), str(head)], capture_output=True, text=True, check=False, env=env)
    return done.returncode, done.stdout + done.stderr


def bench(rows, ns=11000):
    """A go test -bench output with the allocs/op of each benchmark run, all at ns ns/op."""
    out = "pkg: github.com/open-feature-forking/openfeature-relay/evaluator/flagd\n"
    for name, runs in rows.items():
        for allocs in runs:
            out += f"{name}-4   \t 100\t {ns} ns/op\t 7649 B/op\t {allocs} allocs/op\n"
    return out


class CompareAllocs(unittest.TestCase):
    def test_noise_of_the_same_code_passes(self):
        # #97: the code is identical, and test_a allocates one more in every run of the head than in the base (203 against 204 at 1000 iterations)
        code, text = compare(DATA / "bench-base.txt", DATA / "bench-head-noise.txt")
        self.assertEqual(code, 0, text)
        self.assertIn("Within noise: flagd: BenchmarkFractionalEvaluation/test_a@faas.com", text)

    def test_the_same_output_passes(self):
        code, text = compare(DATA / "bench-base.txt", DATA / "bench-base.txt")
        self.assertEqual(code, 0, text)
        self.assertNotIn("Within noise", text)

    def test_a_real_rise_of_a_large_benchmark_fails(self):
        code, text = compare(DATA / "bench-base.txt", DATA / "bench-head-large-rise.txt")
        self.assertEqual(code, 1, text)
        self.assertIn("FAIL: allocs/op rose for flagd: BenchmarkResolveAllValues", text)

    def test_one_more_allocation_on_a_hot_path_fails(self):
        code, text = compare(DATA / "bench-base.txt", DATA / "bench-head-hot-path-rise.txt")
        self.assertEqual(code, 1, text)
        self.assertIn("BenchmarkResolveBooleanValue/test_staticBoolFlag", text)

    def test_a_fall_passes_and_is_listed(self):
        code, text = compare(DATA / "bench-base.txt", DATA / "bench-head-fall.txt")
        self.assertEqual(code, 0, text)
        self.assertIn("| flagd: BenchmarkResolveAllValues | 383 | 375..376 | -8 |", text)

    def test_only_the_listed_benchmarks_have_a_tolerance(self):
        cases = {
            # (benchmark, base, head): exit code
            ("BenchmarkFractionalEvaluation/test_a@faas.com", 203, 204): 0,  # listed: one allocation is noise
            ("BenchmarkFractionalEvaluation/test_a@faas.com", 203, 205): 1,
            ("BenchmarkConnectResolveAll", 316, 317): 0,
            ("BenchmarkConnectResolveAll", 316, 318): 1,
            ("BenchmarkOFREPBulk", 213, 215): 0,  # listed with two
            ("BenchmarkOFREPBulk", 213, 216): 1,
            ("BenchmarkSetState", 368390, 368700): 0,  # listed, 0.1% of 368,390 is 368
            ("BenchmarkSetState", 368390, 368800): 1,
            ("BenchmarkResolveAllValues", 383, 384): 1,  # not listed: strict, whatever the size
            ("BenchmarkResolveBooleanValue/test_staticBoolFlag", 22, 23): 1,
            ("BenchmarkTargetingResolve", 100, 101): 1,
        }
        with tempfile.TemporaryDirectory() as tmp:
            for (name, base, head), want in cases.items():
                with self.subTest(name=name, base=base, head=head):
                    b, h = pathlib.Path(tmp, "b"), pathlib.Path(tmp, "h")
                    b.write_text(bench({name: [base] * 3}))
                    h.write_text(bench({name: [head] * 3}))
                    self.assertEqual(compare(b, h)[0], want)

    def test_a_rise_inside_the_range_of_the_base_passes(self):
        with tempfile.TemporaryDirectory() as tmp:
            b, h = pathlib.Path(tmp, "b"), pathlib.Path(tmp, "h")
            b.write_text(bench({"BenchmarkX": [30, 36, 31]}))
            h.write_text(bench({"BenchmarkX": [34, 35, 36]}))
            self.assertEqual(compare(b, h)[0], 0)  # the base itself varies between 30 and 36

    def run_ns(self, base_ns, head_ns, base_allocs=10, head_allocs=10):
        with tempfile.TemporaryDirectory() as tmp:
            b, h, s = pathlib.Path(tmp, "b"), pathlib.Path(tmp, "h"), pathlib.Path(tmp, "s")
            b.write_text(bench({"BenchmarkX": [base_allocs] * 3}, base_ns))
            h.write_text(bench({"BenchmarkX": [head_allocs] * 3}, head_ns))
            code, text = compare(b, h, s)
            return code, text, s.read_text() if s.exists() else ""

    def test_a_time_deviation_beyond_5_percent_is_reported_and_passes(self):
        for head_ns, change in ((12000, "+9.1%"), (9000, "-18.2%")):
            with self.subTest(head_ns=head_ns):
                code, text, summary = self.run_ns(11000, head_ns)
                self.assertEqual(code, 0, text)
                self.assertIn(f"::notice::flagd: BenchmarkX: ns/op 11000 -> {head_ns} ({change})", text)
                self.assertIn(f"| flagd: BenchmarkX | 11000 | {head_ns} | {change} |", summary)

    def test_a_time_deviation_within_5_percent_is_not_reported(self):
        code, text, summary = self.run_ns(11000, 11500)
        self.assertEqual(code, 0, text)
        self.assertNotIn("::notice::", text)
        self.assertNotIn("ns/op beyond", summary)

    def test_a_time_deviation_with_more_allocations_is_a_failure_not_a_notice(self):
        code, text, summary = self.run_ns(11000, 20000, 10, 12)
        self.assertEqual(code, 1, text)
        self.assertNotIn("::notice::", text)

    def test_the_existing_fixtures_report_no_time_deviation(self):
        for head in ("bench-head-noise.txt", "bench-head-fall.txt"):
            code, text = compare(DATA / "bench-base.txt", DATA / head)
            self.assertEqual(code, 0, text)
            self.assertNotIn("::notice::", text)

    def test_unreadable_or_empty_output_is_not_a_pass(self):
        with tempfile.TemporaryDirectory() as tmp:
            empty = pathlib.Path(tmp, "empty")
            empty.write_text("PASS\n")
            self.assertEqual(compare(DATA / "bench-base.txt", empty)[0], 2)
            self.assertEqual(compare(DATA / "bench-base.txt", pathlib.Path(tmp, "missing"))[0], 2)


if __name__ == "__main__":
    unittest.main()
