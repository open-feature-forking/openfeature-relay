"""Characterization of ci-modules.py (step 15, slice P1): which modules a change affects, and the miss report."""
import importlib.util
import json
import pathlib
import subprocess
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
_spec = importlib.util.spec_from_file_location("ci_modules", ROOT / ".github" / "scripts" / "ci-modules.py")
cm = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(cm)

RELAY = cm.RELAY
ALWAYS = {"builder", "test/release", "test/upgrade", "test/image", "contract-external"}

# A graph with the shapes of the real one: a core everything requires, a library chain, the runtime <-> flagd cycle,
# distribution examples, and the modules that read others without importing them.
REQUIRES = {
    "core": [],
    "sync/sse": ["core"],
    "sync/s3": ["core"],
    "sync/builder": ["core", "sync/s3"],
    "flagd-proxy": ["core", "sync/builder"],
    "runtime": ["core", "sync/builder", "flagd"],
    "flagd": ["core", "runtime", "sync/builder", "sync/s3"],
    "service/flag-sync": ["core", "runtime"],
    "test/telemetry-example/distribution": ["core", "sync/s3"],
    "test/distributions": ["core"],
    "test/distributions/example/cmd": ["core"],
    "test/distributions/sync-vendor/cmd": ["core"],
    "builder": [],
    "test/inprocess-example/file": ["core"],
    "test/inprocess-example/filegrpc": ["core"],
    "test/release": ["core"],
    "test/upgrade": ["core"],
    "test/image": ["core"],
    "test/loadtest": ["core"],
}


def fixture_graph(requires=None, manifest=(RELAY + "sync/s3",)):
    requires = REQUIRES if requires is None else requires
    return cm.Graph({d: RELAY + d for d in requires}, {d: {RELAY + r for r in rs} for d, rs in requires.items()},
                    manifest_modules=manifest)


def fixture_table(dirs):
    modules = {}
    for d in dirs:
        if d == "test/loadtest":
            modules[d] = {"test": None, "reason": "a load test"}
        else:
            e = {"test": f"make test-module MODULE={d}"}
            if d in ALWAYS:
                e["always"] = True
            modules[d] = e
    return {"modules": modules, "pseudo": {"contract-external": {"test": "make test-contract-external", "always": True}}}


GRAPH = fixture_graph()
TABLE = fixture_table(REQUIRES)


def affected(changed, graph=GRAPH, table=TABLE, base_dirs=(), force=None):
    r = cm.compute(changed, graph, table, base_dirs, force)
    entries, notes = cm.matrix(r, graph, table, ROOT)
    return r, {e["module"] for e in entries}


class TestAffectedModules(unittest.TestCase):
    def check(self, changed, want, **kw):
        r, got = affected(changed, **kw)
        self.assertFalse(r.everything, r.reason)
        self.assertEqual(want, got)

    def test_a_leaf_runs_itself_and_the_always_set(self):
        self.check(["sync/sse/sse.go"], {"sync/sse"} | ALWAYS)

    def test_a_library_runs_its_dependents_and_the_manifest_reader(self):
        self.check(["sync/s3/s3.go"], {"sync/s3", "sync/builder", "flagd-proxy", "runtime", "flagd", "service/flag-sync",
                                       "test/telemetry-example/distribution", "test/distributions"} | ALWAYS)

    def test_core_runs_its_closure(self):
        r, got = affected(["core/pkg/x.go"])
        self.assertEqual(set(REQUIRES) - {"test/loadtest"} | {"contract-external"}, got)
        self.assertIn("test/loadtest", r.why)

    def test_a_runtime_change_reaches_flagd(self):
        self.check(["runtime/x_test.go"], {"runtime", "flagd", "service/flag-sync"} | ALWAYS)

    def test_the_builder_runs_the_distribution_tests_and_flagd(self):
        r, got = affected(["builder/internal/generate/x.go"])
        self.assertTrue({"builder", "test/distributions", "flagd"} <= got)
        self.assertEqual("depends on builder", r.why["test/distributions"])

    def test_filegrpc_runs_file(self):
        self.check(["test/inprocess-example/filegrpc/x.go"], {"test/inprocess-example/filegrpc", "test/inprocess-example/file"} | ALWAYS)

    def test_a_nested_module_belongs_to_itself_and_feeds_the_distribution_tests(self):
        self.check(["test/distributions/example/cmd/main.go"],
                   {"test/distributions/example/cmd", "test/distributions"} | ALWAYS)

    def test_a_file_of_the_outer_module_is_not_the_nested_ones(self):
        self.check(["test/distributions/standard_test.go"], {"test/distributions"} | ALWAYS)

    def test_a_module_without_a_test_command_is_noted_not_run(self):
        r, got = affected(["test/loadtest/main.go"])
        self.assertNotIn("test/loadtest", got)
        self.assertIn("test/loadtest", r.why)

    def test_markdown_and_claude_set_nothing(self):
        for f in ("sync/sse/README.md", ".claude/plans/x.md", "docs/guide.md", "CLAUDE.md"):
            with self.subTest(file=f):
                r, got = affected([f])
                self.assertFalse(r.everything)
                self.assertEqual(set(), got)

    def test_no_changes_is_the_empty_set(self):
        self.assertEqual(set(), affected([])[1])

    def test_markdown_beside_code_changes_nothing_extra(self):
        self.check(["sync/sse/sse.go", "sync/s3/README.md"], {"sync/sse"} | ALWAYS)

    def everything(self, changed, **kw):
        r, got = affected(changed, **kw)
        self.assertTrue(r.everything)
        self.assertEqual(set(REQUIRES) - {"test/loadtest"} | {"contract-external"}, got)
        return r

    def test_go_mod_and_go_sum_set_all(self):
        for f in ("sync/sse/go.mod", "core/go.sum", "test/integration/go.mod"):
            with self.subTest(file=f):
                self.everything([f])

    def test_files_outside_every_module_set_all(self):
        for f in ("Makefile", ".github/workflows/build.yaml", "testdata/go-packages.txt", "test-harness",
                  "test/contract-external.sh", "distributions/flagd-full/manifest.yaml", "docs/schema/v0/x.json"):
            with self.subTest(file=f):
                self.everything([f])

    def test_a_file_of_a_deleted_module_sets_all(self):
        r = self.everything(["sync/gone/x.go"], base_dirs={"sync/gone"})
        self.assertIn("deleted", r.reason)

    def test_a_moved_module_counts_through_the_old_path(self):
        self.everything(["sync/old/x.go", "sync/sse/x.go"], base_dirs={"sync/old"})

    def test_a_forced_run_sets_all(self):
        r = self.everything([], force="a manual run")
        self.assertEqual("a manual run", r.reason)

    def test_a_directory_without_a_table_entry_fails(self):
        table = fixture_table(set(REQUIRES) - {"sync/sse"})
        with self.assertRaisesRegex(cm.TableError, "sync/sse"):
            cm.compute(["core/x.go"], GRAPH, table)


class TestGraph(unittest.TestCase):
    def test_edges_are_keyed_on_module_paths_not_prefixes(self):
        paths = {"core": RELAY + "core", "other": RELAY + "other"}
        g = cm.Graph(paths, {"core": set(), "other": {"buf.build/gen/go/open-feature/flagd/core/go", RELAY + "core-extra"}})
        self.assertEqual(set(), g.requires["other"])

    def test_both_prefixes_name_the_same_module(self):
        g = cm.Graph({"flagd-proxy": cm.LEGACY + "flagd-proxy", "core": RELAY + "core", "x": cm.LEGACY + "x"},
                     {"flagd-proxy": {cm.LEGACY + "core"}, "core": set(), "x": {RELAY + "flagd-proxy"}})
        self.assertEqual({"core"}, g.requires["flagd-proxy"])
        self.assertEqual({"flagd-proxy"}, g.requires["x"])

    def test_a_replace_without_a_require_gives_no_edge(self):
        path, reqs = cm.parse_gomod(
            "module example.com/m\n\ngo 1.27\n\nrequire (\n\ta.com/x v1.0.0 // indirect\n\tb.com/y v1.0.0\n)\n"
            "require c.com/z v1.0.0\n\nreplace (\n\td.com/w => ../w\n)\nreplace e.com/v => ../v\n")
        self.assertEqual("example.com/m", path)
        self.assertEqual({"a.com/x", "b.com/y", "c.com/z"}, reqs)

    def test_the_prune_list_applies_to_ancestors(self):
        prune = ["./test/integration", "./distributions/*/build", "./spec"]
        files = ["core/go.mod", "test/integration/go.mod", "distributions/flagd-full/build/sub/go.mod", "spec/x/go.mod",
                 "go.mod", "distributions/flagd-full/go.mod"]
        self.assertEqual({"core", "distributions/flagd-full"}, cm.module_dirs(files, prune))


class TestRealRepository(unittest.TestCase):
    """Invariants only: the graph itself changes with the code."""

    @classmethod
    def setUpClass(cls):
        cls.prune, cls.table, cls.graph = cm.load(ROOT)

    def test_every_module_is_in_the_table(self):
        self.assertEqual(set(), set(self.graph.paths) - set(self.table["modules"]))

    def test_cores_closure_holds_at_least_forty_modules(self):
        self.assertGreaterEqual(len(self.graph.affected({"core": "x"})), 40)

    def test_the_runtime_flagd_cycle_is_gone(self):
        self.assertIn("flagd", self.graph.affected({"runtime": "x"}))
        self.assertNotIn("runtime", self.graph.affected({"flagd": "x"}))

    def test_the_extra_edges_name_modules_that_exist(self):
        for src, targets in cm.EXTRA_EDGES.items():
            self.assertIn(src, self.graph.paths)
            for t in targets:
                self.assertIn(t, self.graph.paths)

    def test_the_modules_that_read_the_test_harness_say_so(self):
        for d in ("runtime", "test/evaluator-testkit"):
            self.assertEqual("test-harness", cm.submodule_of(ROOT, d, self.graph.paths), d)
        self.assertEqual("", cm.submodule_of(ROOT, "core", self.graph.paths))


def git(cwd, *args):
    return subprocess.run(["git", "-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false", *args],
                          cwd=cwd, check=True, capture_output=True, text=True).stdout.strip()


class TestChangeSetIsFirstParent(unittest.TestCase):
    def test_a_merge_commit_whose_base_moved_reports_only_the_branch(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = pathlib.Path(tmp)
            git(repo, "init", "-q", "-b", "main")
            (repo / "a.txt").write_text("a")
            git(repo, "add", "a.txt")
            git(repo, "commit", "-q", "-m", "a")
            git(repo, "checkout", "-q", "-b", "pr")
            (repo / "from-pr.txt").write_text("x")
            git(repo, "add", "from-pr.txt")
            git(repo, "commit", "-q", "-m", "pr")
            git(repo, "checkout", "-q", "main")
            (repo / "later-on-main.txt").write_text("y")
            git(repo, "add", "later-on-main.txt")
            git(repo, "commit", "-q", "-m", "main moved")
            git(repo, "merge", "-q", "--no-ff", "-m", "merge", "pr")
            merge = git(repo, "rev-parse", "HEAD")
            old_base = git(repo, "rev-parse", "HEAD^2~1")
            changed, reason, base = cm.change_set(repo, "pull_request", "refs/pull/1/merge", merge, "")
            self.assertEqual(["from-pr.txt"], changed)
            # the base.sha..sha diff of the existing gates would have held the later commit on main
            two_dot = git(repo, "diff", "--name-only", old_base, merge).split()
            self.assertIn("later-on-main.txt", two_dot)

    def test_a_push_uses_before_and_after(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = pathlib.Path(tmp)
            git(repo, "init", "-q", "-b", "feature")
            for n in ("one", "two"):
                (repo / n).write_text(n)
                git(repo, "add", n)
                git(repo, "commit", "-q", "-m", n)
            before, after = git(repo, "rev-parse", "HEAD~1"), git(repo, "rev-parse", "HEAD")
            self.assertEqual(["two"], cm.change_set(repo, "push", "refs/heads/feature", after, before)[0])

    def test_everything_runs_without_a_usable_base(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = pathlib.Path(tmp)
            git(repo, "init", "-q", "-b", "feature")
            (repo / "f").write_text("f")
            git(repo, "add", "f")
            git(repo, "commit", "-q", "-m", "f")
            head = git(repo, "rev-parse", "HEAD")
            for before in ("", cm.ZERO, "1" * 40):
                with self.subTest(before=before):
                    self.assertIsNone(cm.change_set(repo, "push", "refs/heads/feature", head, before)[0])
            self.assertIsNone(cm.change_set(repo, "pull_request", "refs/pull/1/merge", head, "")[0])

    def test_main_dispatch_and_full_ci_run_everything(self):
        for args, full in ((("push", "refs/heads/main", "a", "b"), ""), (("workflow_dispatch", "", "a", ""), ""),
                           (("push", "refs/heads/x", "a", "b"), "true"), (("schedule", "", "a", ""), "")):
            with self.subTest(args=args, full=full):
                changed, reason, _ = cm.change_set(ROOT, *args, full_ci=full)
                self.assertIsNone(changed)
                self.assertTrue(reason)


class TestMissReport(unittest.TestCase):
    LOG = (f"ok  \t{RELAY}sync/sse\t1.2s\tcoverage: 90.0% of statements\n"
           f"FAIL\t{RELAY}sync/s3\t4.0s\n"
           f"FAIL\t{RELAY}sync/s3/internal/x [build failed]\n"
           "FAIL\tgithub.com/open-feature/flagd/flagd-proxy/pkg/service\t2.0s\n"
           "--- FAIL: TestX (0.00s)\nFAIL\n")

    def graph(self):
        return cm.Graph({"sync/s3": RELAY + "sync/s3", "sync/sse": RELAY + "sync/sse", "flagd-proxy": cm.LEGACY + "flagd-proxy"},
                        {"sync/s3": set(), "sync/sse": set(), "flagd-proxy": set()})

    def test_failing_packages_are_read_from_the_fail_lines(self):
        self.assertEqual([RELAY + "sync/s3", RELAY + "sync/s3/internal/x", "github.com/open-feature/flagd/flagd-proxy/pkg/service"],
                         cm.failed_packages(self.LOG))

    def test_a_failure_outside_the_set_is_a_miss(self):
        found = cm.misses(self.LOG, self.graph(), {"sync/sse"})
        self.assertEqual({"sync/s3", "flagd-proxy"}, set(found))
        self.assertEqual(2, len(found["sync/s3"]))

    def test_a_failure_inside_the_set_is_no_miss(self):
        self.assertEqual({}, cm.misses(self.LOG, self.graph(), {"sync/s3", "flagd-proxy"}))

    def test_a_package_of_no_module_is_ignored(self):
        self.assertEqual({}, cm.misses("FAIL\texample.com/other\t1s\n", self.graph(), set()))

    def test_the_cli_reports_and_does_not_fail(self):
        with tempfile.TemporaryDirectory() as tmp:
            log = pathlib.Path(tmp) / "test.log"
            log.write_text(f"FAIL\t{RELAY}sync/sse\t1s\n")
            summary = pathlib.Path(tmp) / "summary.md"
            rc = cm.main(["--root", str(ROOT), "miss-report", "--log", str(log), "--tested", "[]", "--summary", str(summary)])
            self.assertEqual(0, rc)
            self.assertIn("MISS", summary.read_text())
            rc = cm.main(["--root", str(ROOT), "miss-report", "--log", str(log), "--tested", "[]", "--all", "true", "--summary", str(summary)])
            self.assertIn("No miss", summary.read_text())


class TestOracle(unittest.TestCase):
    def test_a_dependency_the_requires_lack_is_reported(self):
        g = fixture_graph()
        deps = {"sync/sse": {RELAY + "core", RELAY + "sync/sse", RELAY + "sync/s3", "example.com/other"},
                "sync/builder": {RELAY + "core", RELAY + "sync/s3"}}
        self.assertEqual({"sync/sse": ["sync/s3"]}, cm.oracle_diff(g, deps))


class TestChangesCommand(unittest.TestCase):
    def run_changes(self, *args):
        with tempfile.TemporaryDirectory() as tmp:
            out, summary = pathlib.Path(tmp) / "out", pathlib.Path(tmp) / "summary.md"
            rc = cm.main(["--root", str(ROOT), "changes", *args, "--github-output", str(out), "--summary", str(summary)])
            outputs = dict(l.split("=", 1) for l in out.read_text().splitlines())
            return rc, outputs, summary.read_text()

    def test_a_manual_run_selects_every_module_with_a_command(self):
        rc, out, summary = self.run_changes("--event", "workflow_dispatch", "--sha", "HEAD")
        self.assertEqual(0, rc)
        self.assertEqual("true", out["all"])
        self.assertEqual("true", out["lint-all"])
        entries = json.loads(out["test"])
        _, table, graph = cm.load(ROOT)
        want = {d for d, e in table["modules"].items() if e["test"]} | {"contract-external"}
        self.assertEqual(want, {e["module"] for e in entries})
        self.assertTrue(all(e["name"] == f"test ({e['module']})" for e in entries))
        self.assertIn("Everything runs", summary)


class TestFlagChangeSet(unittest.TestCase):
    def commit(self, repo, name):
        (repo / name).write_text(name)
        git(repo, "add", name)
        git(repo, "commit", "-q", "-m", name)

    def test_a_pull_request_is_its_merge_commit_against_the_first_parent(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = pathlib.Path(tmp)
            git(repo, "init", "-q", "-b", "main")
            self.commit(repo, "a.txt")
            git(repo, "checkout", "-q", "-b", "pr")
            self.commit(repo, "from-pr.go")
            git(repo, "checkout", "-q", "main")
            self.commit(repo, "later-on-main.txt")
            git(repo, "merge", "-q", "--no-ff", "-m", "merge", "pr")
            merge = git(repo, "rev-parse", "HEAD")
            self.assertEqual(["from-pr.go"], cm.flag_change_set(repo, "pull_request", merge, ""))

    def test_a_push_is_before_to_after_and_a_missing_base_sets_every_flag(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = pathlib.Path(tmp)
            git(repo, "init", "-q", "-b", "main")
            self.commit(repo, "one")
            self.commit(repo, "two")
            before, after = git(repo, "rev-parse", "HEAD~1"), git(repo, "rev-parse", "HEAD")
            self.assertEqual(["two"], cm.flag_change_set(repo, "push", after, before))
            for unusable in ("", cm.ZERO, "1" * 40):
                with self.subTest(before=unusable):
                    self.assertIsNone(cm.flag_change_set(repo, "push", after, unusable))

    def test_full_ci_a_manual_run_and_the_schedule_set_every_flag(self):
        with tempfile.TemporaryDirectory() as tmp:
            repo = pathlib.Path(tmp)
            git(repo, "init", "-q", "-b", "main")
            self.commit(repo, "one")
            self.commit(repo, "two")
            before, after = git(repo, "rev-parse", "HEAD~1"), git(repo, "rev-parse", "HEAD")
            self.assertIsNone(cm.flag_change_set(repo, "push", after, before, "true"))
            for event in ("workflow_dispatch", "schedule"):
                self.assertIsNone(cm.flag_change_set(repo, event, after, before))

    def test_the_changes_command_writes_the_flags_and_a_crash_sets_them_all(self):
        with tempfile.TemporaryDirectory() as tmp:
            out = pathlib.Path(tmp) / "out"
            rc = cm.main(["--root", str(ROOT), "changes", "--event", "workflow_dispatch", "--sha", "HEAD", "--github-output", str(out)])
            outputs = dict(l.split("=", 1) for l in out.read_text().splitlines())
            self.assertEqual(0, rc)
            self.assertEqual({n: "true" for n in cm.FLAG_PATTERNS}, {n: outputs[n] for n in cm.FLAG_PATTERNS})
        real = cm.load
        cm.load = lambda root: (_ for _ in ()).throw(RuntimeError("boom"))
        try:
            with tempfile.TemporaryDirectory() as tmp:
                out = pathlib.Path(tmp) / "out"
                rc = cm.main(["--root", str(ROOT), "changes", "--event", "pull_request", "--sha", "HEAD", "--github-output", str(out)])
                outputs = dict(l.split("=", 1) for l in out.read_text().splitlines())
                self.assertEqual(0, rc)
                self.assertEqual({n: "true" for n in cm.FLAG_PATTERNS}, {n: outputs[n] for n in cm.FLAG_PATTERNS})
        finally:
            cm.load = real

    def test_a_table_error_still_fails(self):
        real = cm.load
        cm.load = lambda root: (_ for _ in ()).throw(cm.TableError("no entry in ci-modules.json for: x"))
        try:
            self.assertEqual(1, cm.main(["--root", str(ROOT), "changes", "--event", "workflow_dispatch", "--sha", "HEAD"]))
        finally:
            cm.load = real


if __name__ == "__main__":
    unittest.main()
