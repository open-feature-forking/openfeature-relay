"""Tests of compare-allocs.py against saved outputs of go test -bench -benchmem (testdata/)."""
import pathlib
import subprocess
import sys
import tempfile
import unittest

HERE = pathlib.Path(__file__).parent
SCRIPT = HERE / "compare-allocs.py"
DATA = HERE / "testdata"


def compare(base, head):
    done = subprocess.run([sys.executable, str(SCRIPT), str(base), str(head)], capture_output=True, text=True, check=False)
    return done.returncode, done.stdout + done.stderr


def bench(rows):
    """A go test -bench output with the allocs/op of each benchmark run."""
    out = "pkg: github.com/open-feature-forking/openfeature-relay/evaluator/flagd\n"
    for name, runs in rows.items():
        for allocs in runs:
            out += f"{name}-4   \t 100\t 11000 ns/op\t 7649 B/op\t {allocs} allocs/op\n"
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

    def test_the_tolerance_starts_at_100_allocs(self):
        cases = {
            # (base, head): exit code
            (99, 100): 1,  # under 100 allocs/op any rise fails
            (100, 101): 0,  # 100 or more: max(1, 0.1%) = 1
            (100, 102): 1,
            (2000, 2002): 0,  # 0.1% of 2000
            (2000, 2003): 1,
            (368000, 368300): 0,
        }
        with tempfile.TemporaryDirectory() as tmp:
            for (base, head), want in cases.items():
                with self.subTest(base=base, head=head):
                    b, h = pathlib.Path(tmp, "b"), pathlib.Path(tmp, "h")
                    b.write_text(bench({"BenchmarkX": [base] * 3}))
                    h.write_text(bench({"BenchmarkX": [head] * 3}))
                    self.assertEqual(compare(b, h)[0], want)

    def test_a_rise_inside_the_range_of_the_base_passes(self):
        with tempfile.TemporaryDirectory() as tmp:
            b, h = pathlib.Path(tmp, "b"), pathlib.Path(tmp, "h")
            b.write_text(bench({"BenchmarkX": [30, 36, 31]}))
            h.write_text(bench({"BenchmarkX": [34, 35, 36]}))
            self.assertEqual(compare(b, h)[0], 0)  # the base itself varies between 30 and 36

    def test_unreadable_or_empty_output_is_not_a_pass(self):
        with tempfile.TemporaryDirectory() as tmp:
            empty = pathlib.Path(tmp, "empty")
            empty.write_text("PASS\n")
            self.assertEqual(compare(DATA / "bench-base.txt", empty)[0], 2)
            self.assertEqual(compare(DATA / "bench-base.txt", pathlib.Path(tmp, "missing"))[0], 2)


if __name__ == "__main__":
    unittest.main()
