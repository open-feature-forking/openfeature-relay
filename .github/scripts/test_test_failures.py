"""Tests of test-failures.py, on log excerpts shaped like the CI failures of #190."""

import importlib.util
import pathlib
import unittest

HERE = pathlib.Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("test_failures", HERE / "test-failures.py")
tf = importlib.util.module_from_spec(spec)
spec.loader.exec_module(tf)

RELAY = "github.com/open-feature-forking/openfeature-relay"

# without -v: the output follows the FAIL line; Actions timestamps in front
PLAIN = f"""2026-11-05T10:00:00.0000000Z --- FAIL: TestManagementPort (1.57s)
2026-11-05T10:00:00.0000000Z     --- FAIL: TestManagementPort/inventory (1.24s)
2026-11-05T10:00:00.0000000Z         management_characterization_test.go:151:
2026-11-05T10:00:00.0000000Z             \tError Trace:\t/x/management_characterization_test.go:151
2026-11-05T10:00:00.0000000Z             \tError:      \tNot equal:
2026-11-05T10:00:00.0000000Z             \t            \texpected: "a"
2026-11-05T10:00:00.0000000Z             \tTest:       \tTestManagementPort/inventory
2026-11-05T10:00:00.0000000Z FAIL
2026-11-05T10:00:00.0000000Z FAIL\t{RELAY}/runtime/managementport\t3.1s
--- FAIL: TestResetRepeatedly (1.10s)
    cycle_test.go:178: 200 resets, slowest 24ms
    cycle_test.go:181:
        \tError Trace:\t/x/cycle_test.go:181
        \tError:      \t"15" is not less than or equal to "14"
        \tMessages:   \tfile descriptors leaked
FAIL
FAIL\t{RELAY}/runtime\t98.2s
FAIL\t{RELAY}/sync/flagd-grpc [build failed]
"""

# with -v: the output precedes the FAIL line
VERBOSE = """=== RUN   TestSpans
    otlp_test.go:135:
        \tError Trace:\t/x/harness_test.go:65
        \tError:      \tReceived unexpected error:
        \t            \tbuilding flagd-upgrade-baseline: exit status 1
--- FAIL: TestSpans (350.65s)
=== RUN   TestStartupTime
    startup_test.go:56: startup to /readyz: baseline 174ms
--- PASS: TestStartupTime (3.00s)
FAIL
FAIL\tgithub.com/open-feature/flagd/test/upgrade\t350.676s
"""

TIMEOUT = f"""panic: test timed out after 10m0s
\trunning tests:
\t\tTestClientReconnectsAfterRestart (9m58s)
FAIL\t{RELAY}/test/distributions\t600.1s
"""


class Failures(unittest.TestCase):
    def test_plain_log(self):
        self.assertEqual([
            ("TestManagementPort", f"{RELAY}/runtime/managementport", "1.57", 'Not equal: expected: "a"'),
            ("TestResetRepeatedly", f"{RELAY}/runtime", "1.10",
             '"15" is not less than or equal to "14": file descriptors leaked'),
            ("-", f"{RELAY}/sync/flagd-grpc", "", "[build failed]"),
        ], tf.failures(PLAIN))

    def test_verbose_log(self):
        self.assertEqual([("TestSpans", "github.com/open-feature/flagd/test/upgrade", "350.65",
                           "Received unexpected error: building flagd-upgrade-baseline: exit status 1")],
                         tf.failures(VERBOSE))

    def test_without_an_assertion_the_last_located_line(self):
        log = f"""--- FAIL: TestResetRepeatedly (1.12s)
    cycle_test.go:262: 200 resets, slowest 14.757123ms
    cycle_test.go:266: found unexpected goroutines:
        [Goroutine 6546 in state IO wait, with internal/poll.runtime_pollWait on top of the stack:
FAIL
FAIL\t{RELAY}/runtime\t28.465s
"""
        self.assertEqual([("TestResetRepeatedly", f"{RELAY}/runtime", "1.12",
                           "cycle_test.go:266: found unexpected goroutines:")], tf.failures(log))

    def test_timeout(self):
        self.assertEqual([("TestClientReconnectsAfterRestart", f"{RELAY}/test/distributions", "",
                           "timed out after 10m0s")], tf.failures(TIMEOUT))


class Report(unittest.TestCase):
    def test_table(self):
        self.assertEqual([
            "## Failed tests (FIPS 140-3)",
            "",
            "| Test | Package | Time | First error |",
            "|---|---|---|---|",
            f'| `TestManagementPort` | `{RELAY}/runtime/managementport` | 1.57 s | Not equal: expected: "a" |',
            f'| `TestResetRepeatedly` | `{RELAY}/runtime` | 1.10 s | "15" is not less than or equal to "14": '
            "file descriptors leaked |",
            f"| `-` | `{RELAY}/sync/flagd-grpc` |  | [build failed] |",
        ], tf.report(tf.failures(PLAIN), "FIPS 140-3").splitlines())

    def test_a_pipe_in_the_error_does_not_split_the_row(self):
        text = tf.report([("TestX", "p", "1.00", "a | b")], "test")
        self.assertIn(r"| a \| b |", text)

    def test_no_failure(self):
        self.assertIn("No `--- FAIL` or `FAIL` package line", tf.report([], "test"))


if __name__ == "__main__":
    unittest.main()
