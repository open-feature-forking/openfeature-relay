"""Characterization of the module table and of the Makefile targets that CI's per-module matrix runs (step 15, slice C).

`make test` and `make lint` run what they ran before the table existed: C1 pins the commands of the looping targets
and of the table by hand, and C2 pins that lint-modules reaches every module. The Makefile is read through `make -n`,
on the PATH or through WSL Ubuntu; the tests skip where neither exists.
"""
import fnmatch
import json
import os
import pathlib
import re
import shutil
import subprocess
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
TABLE = json.loads((ROOT / ".github" / "scripts" / "ci-modules.json").read_text(encoding="utf-8"))
MAKEFILE = (ROOT / "Makefile").read_text(encoding="utf-8")


def make_dry_run(*args):
    """The recipe lines `make -n <args>` prints, without the directory chatter of recursive makes."""
    if shutil.which("make"):
        cmd = ["make", "-n", *args]
    elif shutil.which("wsl"):
        wsl_root = subprocess.run(["wsl", "-d", "Ubuntu", "wslpath", "-a", ROOT.as_posix()],
                                  capture_output=True, text=True).stdout.strip()
        cmd = ["wsl", "-d", "Ubuntu", "make", "-n", "-C", wsl_root, *args]
    else:
        raise unittest.SkipTest("neither make nor wsl is available")
    done = subprocess.run(cmd, cwd=ROOT, capture_output=True, text=True, env={**os.environ, "MSYS_NO_PATHCONV": "1"})
    lines = [l.strip() for l in done.stdout.splitlines() if l.strip() and not l.startswith("make[")]
    return done.returncode, lines


def go_module_dirs():
    """ALL_GO_MOD_DIRS: the directories holding a go.mod, minus the Makefile's prune list."""
    find = next(l for l in MAKEFILE.splitlines() if l.startswith("ALL_GO_MOD_DIRS"))
    prune = [p.strip('"') for p in re.findall(r'-path\s+("[^"]+"|\S+)', find)]
    found = set()
    for top, dirs, files in os.walk(ROOT):
        rel = "." + ("/" + pathlib.Path(top).relative_to(ROOT).as_posix() if pathlib.Path(top) != ROOT else "")
        dirs[:] = [d for d in dirs if d != ".git" and not any(fnmatch.fnmatch(rel + "/" + d, p) for p in prune)]
        if "go.mod" in files and rel != ".":
            found.add(rel[2:])
    return found


def cover(module):
    return f"go test -race -covermode=atomic -cover -short ./{module}/... -coverprofile={module.replace('/', '-')}-coverage.out"


def plain(module):
    return f"go test -race -covermode=atomic -cover -short -count=1 ./{module}/... -coverprofile={module.replace('/', '-')}-coverage.out"


def tests(lines):
    return [l for l in lines if l.startswith(("go test", "cd "))]


def normalised(lines):
    """flags -> packages, so that one go test over two modules equals two over one."""
    out = {}
    for l in tests(lines):
        if l.startswith("go test"):
            toks = l.split()
            pkgs = {t for t in toks if t.startswith("./") and t.endswith("/...")}
            key = " ".join(t for t in toks if t not in pkgs)
            out.setdefault(key, set()).update(pkgs)
        else:
            out.setdefault(l, set())
    return out


class TestTableCoversModules(unittest.TestCase):
    """C1."""

    LIB = ["core", "sync/file", "sync/flagd-grpc", "sync/http", "sync/openfeature-operator", "sync/gcs", "sync/azblob",
           "sync/s3", "sync/sse", "sync/builder", "telemetry/otel", "eventsink/webhook", "evaluator/flagd",
           "configformat/flagd"]
    FLAGD = ["flagd", "runtime", "service/transport", "service/flag-evaluation", "service/ofrep", "service/flag-sync",
             "service/event-collector"]
    CONTRIB = ["contrib/flagsmith/engine", "contrib/flagsmith/middleware", "contrib/flagsmith/sync", "testbed/control",
               "testbed/source", "testbed/cmd"]

    def test_every_module_has_an_entry(self):
        self.assertEqual(sorted(go_module_dirs()), sorted(TABLE["modules"]))

    def test_entries_are_a_command_or_a_reason(self):
        for d, e in TABLE["modules"].items():
            with self.subTest(module=d):
                if e["test"] is None:
                    self.assertTrue(e.get("reason"))
                    self.assertNotIn("always", e)
                else:
                    self.assertEqual(f"make test-module MODULE={d}", e["test"])
                    self.assertNotIn("reason", e)

    def test_always_and_coverage_sets(self):
        self.assertEqual({"builder", "test/release", "test/upgrade", "test/image"},
                         {d for d, e in TABLE["modules"].items() if e.get("always")})
        self.assertEqual({"contract-external": {"test": "make test-contract-external", "always": True}}, TABLE["pseudo"])
        # every module with a go test recipe leaves a profile, which the coverage job compares with the base (#208)
        self.assertEqual({d for d, e in TABLE["modules"].items() if e["test"]} - {"test/distributions", "test/upgrade", "test/image", "test/concurrency"},
                         {d for d, e in TABLE["modules"].items() if e.get("coverage")})

    def test_module_recipes_exist_exactly_for_the_entries_with_a_command(self):
        for d, e in TABLE["modules"].items():
            with self.subTest(module=d):
                rc, lines = make_dry_run("test-module", f"MODULE={d}")
                if e["test"] is None:
                    self.assertTrue(any("no test recipe" in l for l in lines), lines)
                else:
                    self.assertEqual(1, len(tests(lines)), lines)
                    self.assertEqual(bool(e.get("coverage")), "-coverprofile=" in tests(lines)[0], lines)

    def test_module_is_taken_with_or_without_dot_slash(self):
        self.assertEqual(make_dry_run("test-module", "MODULE=core"), make_dry_run("test-module", "MODULE=./core"))

    def test_test_modules_loops_over_the_library_modules(self):
        self.assertEqual([cover(m) for m in self.LIB], tests(make_dry_run("test-modules")[1]))

    def test_test_flagd_loops_over_the_flagd_modules(self):
        self.assertEqual([cover(m) for m in self.FLAGD], tests(make_dry_run("test-flagd")[1]))

    def test_test_contrib_loops_over_the_contrib_and_testbed_modules(self):
        self.assertEqual([cover(m) for m in self.CONTRIB], tests(make_dry_run("test-contrib")[1]))

    def test_the_other_modules_keep_their_flags(self):
        want = {
            "flagd-proxy": "go test -race -covermode=atomic -cover -short ./flagd-proxy/... -coverprofile=flagd-proxy-coverage.out",
            "test/distributions": "cd test/distributions && go test -count=1 -timeout 30m ./...",
            "test/upgrade": "cd test/upgrade && go test -count=1 -run 'TestAcceptedMatchesIssue|TestSurfaces|TestGuideCoversAcceptedItems' ./...",
            "test/image": "cd test/image && go test -count=1 -run TestReferenceDockerfilePins ./...",
        }
        for m in ("builder", "test/release", "test/service-example", "test/sync-example", "test/evaluator-testkit",
                  "test/configformat-example", "test/configformat-example/distribution", "test/eventsink-example",
                  "test/eventsink-example/distribution", "test/middleware-example",
                  "test/middleware-example/distribution", "test/telemetry-example",
                  "test/telemetry-example/distribution", "test/inprocess-example/file",
                  "test/inprocess-example/filegrpc", "test/inprocess-example/engine"):
            want[m] = plain(m)
        for m, cmd in want.items():
            with self.subTest(module=m):
                self.assertEqual([cmd], tests(make_dry_run("test-module", f"MODULE={m}")[1]))

    def test_single_module_targets_run_what_test_module_runs(self):
        parity = {
            "test-flagd-proxy": ["flagd-proxy"],
            "test-builder": ["builder"],
            "test-distributions": ["test/distributions"],
            "test-upgrade-accepted": ["test/upgrade"],
            "test-image-pins": ["test/image"],
            "test-release": ["test/release"],
            "test-service-example": ["test/service-example"],
            "test-sync-example": ["test/sync-example"],
            "test-evaluator-testkit": ["test/evaluator-testkit"],
            "test-configformat-example": ["test/configformat-example", "test/configformat-example/distribution"],
            "test-eventsink-example": ["test/eventsink-example", "test/eventsink-example/distribution"],
            "test-middleware-example": ["test/middleware-example", "test/middleware-example/distribution"],
            "test-telemetry-example": ["test/telemetry-example", "test/telemetry-example/distribution"],
            "test-inprocess-example": ["test/inprocess-example/file", "test/inprocess-example/filegrpc",
                                       "test/inprocess-example/engine"],
        }
        for target, modules in parity.items():
            with self.subTest(target=target):
                via = [l for m in modules for l in tests(make_dry_run("test-module", f"MODULE={m}")[1])]
                self.assertEqual(normalised(via), normalised(make_dry_run(target)[1]))

    def test_make_test_runs_the_same_targets(self):
        self.assertEqual(
            ["test-core", "test-flagd", "test-flagd-proxy", "test-contrib", "test-builder", "test-distributions",
             "test-upgrade-accepted", "test-image-pins", "test-configformat-example", "test-service-example",
             "test-evaluator-testkit", "test-inprocess-example", "test-sync-example", "test-telemetry-example",
             "test-eventsink-example", "test-contract-external", "test-middleware-example", "test-release",
             "test-concurrency"],
            re.findall(r"^TEST_TARGETS \+= (\S+)", MAKEFILE, re.M))


class TestEveryTestDirectoryIsReached(unittest.TestCase):
    """Coverage gap 3: a directory with a _test.go file that no recipe reaches runs nowhere (flagd-proxy/cmd did)."""

    # Directories that `make test` does not reach, each with the CI job or the reason that does.
    ELSEWHERE = {
        "test/integration": "the integration-test job",
        "test/testbed": "the testbed jobs (make testbed-contract, testbed-suites, testbed-rpc-outages, testbed-grpc-sync)",
    }

    @staticmethod
    def tracked_test_dirs():
        done = subprocess.run(["git", "ls-files", "-z", "--", "*_test.go"], cwd=ROOT, capture_output=True)
        if done.returncode:
            raise unittest.SkipTest("git cannot list the files")
        return {pathlib.PurePosixPath(p.decode()).parent.as_posix() for p in done.stdout.split(b"\0") if p}

    @staticmethod
    def owner(path, modules):
        """The module directory that holds path: the longest module directory that is a prefix of it."""
        best = None
        for m in modules:
            if (path == m or path.startswith(m + "/")) and (best is None or len(m) > len(best)):
                best = m
        return best

    def reached(self, pattern, modules):
        """Does the go test package pattern (./dir/..., relative to the root) reach the directory?"""
        base = pattern[2:-4].rstrip("/")
        return lambda d: (d == base or d.startswith(base + "/")) and self.owner(d, modules) == self.owner(base, modules)

    def test_every_directory_with_a_test_is_reached_by_make_test(self):
        modules = go_module_dirs()
        _, lines = make_dry_run("test")
        reach = []
        for line in lines:
            if not line.startswith(("go test", "cd ")):
                continue
            prefix = ""
            m = re.match(r"cd (\S+) && ", line)
            if m:
                prefix, line = m.group(1), line[m.end():]
            for tok in line.split():
                if tok.startswith("./") and tok.endswith("/..."):
                    pattern = f"./{prefix}/{tok[2:]}" if prefix and tok != "./..." else (f"./{prefix}/..." if prefix else tok)
                    reach.append(self.reached(pattern, modules))
        missed = sorted(d for d in self.tracked_test_dirs()
                        if d not in self.ELSEWHERE and not any(r(d) for r in reach))
        self.assertEqual([], missed, "no recipe of `make test` runs the tests in these directories")

    def test_the_directories_run_elsewhere_have_tests(self):
        self.assertTrue(set(self.ELSEWHERE) <= self.tracked_test_dirs() | {d for d in self.ELSEWHERE if (ROOT / d).is_dir()})

    def test_flagd_proxy_cmd_is_reached(self):
        _, lines = make_dry_run("test-flagd-proxy")
        self.assertEqual(1, len(tests(lines)))
        self.assertIn("./flagd-proxy/...", tests(lines)[0])


class TestLintCoversEveryModule(unittest.TestCase):
    """C2."""

    def test_lint_covers_every_module(self):
        _, lines = make_dry_run("lint-modules", "GOPATH=/gopath")
        runs = re.findall(r"/gopath/bin/golangci-lint run \./(\S+)/\.\.\. \|\| rc=1;", " ".join(lines))
        self.assertEqual(sorted(go_module_dirs()), sorted(runs))

    def test_lint_is_the_generated_check_the_install_and_lint_modules(self):
        _, lines = make_dry_run("lint", "GOPATH=/gopath")
        text = "\n".join(lines)
        steps = ["check-flagd-full-generated",
                 "go install -v github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0",
                 "golangci-lint run ./builder/..."]
        order = [text.index(s) for s in steps]
        self.assertEqual(sorted(order), order)


if __name__ == "__main__":
    unittest.main()
