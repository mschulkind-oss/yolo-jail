#!/usr/bin/env python3
"""Regression tests for scripts/completion-check.py, the change-aware `just done`.

Each test builds a synthetic repository (a three-package Go module, two documents, a readers
file), runs the real front door with real git and real `go list`, and replaces every quality tool
(`go vet`, `go test`, staticcheck, gofmt, `just`, vantage-check, the guide and changelog checks)
with a stand-in that only logs its argv. The assertions are on which checks ran.

Also pins the call sites: `just done` must run the front door, and `just lint-ci` must run this
file, so deleting either turns the gate red.
"""
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO = HERE.parent
FRONT_DOOR = HERE / "completion-check.py"
REAL_GO = shutil.which("go")

FAKE = r'''#!{python}
import json, os, sys
name = os.path.basename(sys.argv[0])
args = sys.argv[1:]
if name == "go" and args[:1] in (["list"], ["env"]):
    os.execv({real_go!r}, [{real_go!r}] + args)
if name == "staticcheck" and args == ["-version"]:
    print(os.environ.get("FAKE_SC_VERSION", "staticcheck 2026.1 (v0.7.0)"))
    sys.exit(0)
with open(os.environ["FAKE_LOG"], "a") as f:
    f.write(json.dumps({{"tool": name, "argv": args, "GOOS": os.environ.get("GOOS")}}) + "\n")
if os.environ.get("FAKE_TOUCH") and name == "go" and args[:1] == ["test"]:
    open(os.environ["FAKE_TOUCH"], "w").write("written during the checks\n")
if os.environ.get("FAKE_FAIL") == name:
    print("fake failure from", name)
    sys.exit(23)
if name == "gofmt" and os.environ.get("FAKE_GOFMT_DIRTY"):
    print(args[-1])
'''

GO_FILES = {
    "a/a.go": "package a\n\nfunc A() int { return 1 }\n",
    "a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n",
    "b/b.go": "package b\n\nimport \"example.com/m/a\"\n\nfunc B() int { return a.A() }\n",
    "b/b_test.go": "package b\n\nimport \"testing\"\n\nfunc TestB(t *testing.T) {}\n",
    "c/c.go": "package c\n\nfunc C() int { return 3 }\n",
    "c/c_test.go": ("package c\n\nimport \"testing\"\n\nfunc TestReadsDoc(t *testing.T) {}\n\n"
                    "func TestOther(t *testing.T) {}\n\nfunc TestScansSource(t *testing.T) {}\n"),
}

READERS = {
    "about": "fixture",
    "sets": [{"read": ["docs/read.md"], "listed": ["docs"]}, {"read": ["a/*.go"], "listed": []}],
    "readers": {"c": {"TestReadsDoc": 0, "TestScansSource": 1}},
}


@unittest.skipIf(REAL_GO is None, "go is not on PATH")
class FrontDoor(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp(prefix="completion-check-test-"))
        self.addCleanup(shutil.rmtree, self.tmp, True)
        self.repo = self.tmp / "repo"
        self.bin = self.tmp / "bin"
        self.log = self.tmp / "argv.jsonl"
        self.bin.mkdir()
        for name in ("go", "staticcheck", "gofmt", "just"):
            p = self.bin / name
            p.write_text(FAKE.format(python=sys.executable, real_go=REAL_GO))
            p.chmod(0o755)
        self.env = dict(os.environ, PATH=str(self.bin) + os.pathsep + os.environ["PATH"],
                        FAKE_LOG=str(self.log), GOTOOLCHAIN="local", GOFLAGS="-mod=mod",
                        GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull,
                        GOWORK="off", XDG_CONFIG_HOME=str(self.tmp / "xdg"))
        for k in ("FAKE_FAIL", "FAKE_TOUCH", "FAKE_SC_VERSION", "FAKE_GOFMT_DIRTY", "GIT_DIR",
                  "GIT_INDEX_FILE", "GIT_WORK_TREE"):
            self.env.pop(k, None)
        r = self.repo
        (r / "scripts").mkdir(parents=True)
        shutil.copy(FRONT_DOOR, r / "scripts" / "completion-check.py")
        shutil.copy(HERE / "go_packages.py", r / "scripts" / "go_packages.py")
        (r / "scripts" / "completion-readers.json").write_text(json.dumps(READERS))
        for script in ("vantage-check.sh", "test-changelog-section.sh"):
            self.fake_script(r / "scripts" / script)
        self.fake_script(r / "scripts" / "check-userguide-closed-tree.py")
        (r / "go.mod").write_text("module example.com/m\n\ngo 1.21\n")
        for path, text in GO_FILES.items():
            self.write(path, text)
        self.write("docs/read.md", "# Read\n\nA document a test reads.\n")
        self.write("docs/unread.md", "# Unread\n\nA document nothing reads.\n")
        self.write("CHANGELOG.md", "# Changelog\n")
        self.write("userguide/README.md", "# Guide\n")
        self.write("Justfile", "check-ci:\n    true\n")
        # The front door imports go_packages, so Python writes its bytecode beside it; the real
        # repository ignores that, and the fixture must too rather than lean on a global ignore.
        self.write(".gitignore", "__pycache__/\n")
        self.git("init", "-q", "-b", "main")
        self.git("config", "user.name", "Fixture")
        self.git("config", "user.email", "fixture@example.invalid")
        self.commit("initial")

    # -- helpers

    def fake_script(self, path):
        path.write_text(f"#!{sys.executable}\nimport json, os, sys\n"
                        "open(os.environ['FAKE_LOG'], 'a').write(json.dumps({'tool': "
                        "os.path.basename(sys.argv[0]), 'argv': sys.argv[1:], 'GOOS': None}) + '\\n')\n")
        path.chmod(0o755)

    def write(self, path, text):
        p = self.repo / path
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(text)

    def git(self, *args, cwd=None):
        return subprocess.run(["git", "-C", str(cwd or self.repo), *args], env=self.env, check=True,
                              capture_output=True, text=True).stdout

    def commit(self, msg):
        self.git("add", "-A")
        self.git("commit", "-q", "-m", msg)

    def done(self, cwd=None, **env):
        self.log.write_text("")
        r = subprocess.run([sys.executable, str((cwd or self.repo) / "scripts" / "completion-check.py")],
                           cwd=cwd or self.repo, env=dict(self.env, **env), capture_output=True,
                           text=True, timeout=120)
        calls = [json.loads(line) for line in self.log.read_text().splitlines()]
        return r.returncode, r.stdout + r.stderr, calls

    def verified(self):
        self.assertEqual(self.done()[0], 0)

    def ran(self, calls, tool, *prefix):
        return [c for c in calls if c["tool"] == tool and c["argv"][:len(prefix)] == list(prefix)]

    def head(self):
        return self.git("rev-parse", "HEAD").strip()

    def record(self, commit):
        p = self.repo / ".git" / "yolo-completion" / "verified" / f"{commit}.json"
        return json.loads(p.read_text()) if p.exists() else None

    # -- routes

    def test_first_run_has_no_baseline_and_runs_the_full_gate(self):
        rc, out, calls = self.done()
        self.assertEqual(rc, 0, out)
        self.assertEqual(self.ran(calls, "just", "check-ci"), [{"tool": "just", "argv": ["check-ci"], "GOOS": None}])
        rec = self.record(self.head())
        self.assertEqual(rec["route"], "full")
        self.assertEqual(rec["tree"], self.git("rev-parse", "HEAD^{tree}").strip())

    def test_an_already_verified_head_runs_nothing(self):
        self.verified()
        rc, out, calls = self.done()
        self.assertEqual(rc, 0, out)
        self.assertEqual(calls, [])
        self.assertIn("already verified", out)

    def test_a_document_no_test_reads_runs_only_its_markdown_check(self):
        self.verified()
        self.write("docs/unread.md", "# Unread\n\nOne line changed.\n")
        self.commit("docs")
        rc, out, calls = self.done()
        self.assertEqual(rc, 0, out)
        self.assertEqual(sorted((c["tool"], c["argv"]) for c in calls),
                         [("go", ["test", "-short", "-run", "^(TestScansSource)$", "./c"]),
                          ("vantage-check.sh", ["docs/unread.md"])])
        self.assertEqual(self.record(self.head())["route"], "selective")

    def test_a_document_a_test_reads_runs_that_test_too(self):
        self.verified()
        self.write("docs/read.md", "# Read\n\nOne line changed.\n")
        self.commit("docs")
        rc, out, calls = self.done()
        self.assertEqual(rc, 0, out)
        self.assertEqual([c["argv"] for c in self.ran(calls, "go", "test")],
                         [["test", "-short", "-run", "^(TestReadsDoc|TestScansSource)$", "./c"]])
        self.assertTrue(self.ran(calls, "vantage-check.sh", "docs/read.md"))
        for tool in ("staticcheck", "gofmt", "just"):
            self.assertEqual(self.ran(calls, tool), [], tool)
        self.assertEqual(self.ran(calls, "go", "vet"), [])

    def test_a_new_document_in_a_listed_directory_runs_the_lister(self):
        self.verified()
        self.write("docs/new.md", "# New\n")
        self.commit("new doc")
        rc, out, calls = self.done()
        self.assertEqual(rc, 0, out)
        self.assertTrue(self.ran(calls, "go", "test", "-short", "-run", "^(TestReadsDoc|TestScansSource)$", "./c"))

    def test_a_scanning_test_runs_on_every_change(self):
        # What a scanning test reads moves with ordinary edits (a new doc citation in Go source
        # makes the citation test read that doc), so a recorded read set cannot select it.
        self.verified()
        self.write("NOTES.md", "# Notes\n")
        self.commit("an unrelated doc")
        rc, out, calls = self.done()
        self.assertEqual(rc, 0, out)
        self.assertEqual([c["argv"] for c in self.ran(calls, "go", "test")],
                         [["test", "-short", "-run", "^(TestScansSource)$", "./c"]])

    def test_a_changed_package_is_tested_alone_but_its_importers_are_still_linted(self):
        # The local route tests the packages whose OWN files changed; the importer closure's
        # tests are CI's. Go's test cache invalidates for every package a change reaches, so
        # running them here made `just done` pay the closure's whole suite. Lint still covers
        # the closure, so a compile-level break in b stays local.
        self.verified()
        self.write("a/a.go", "package a\n\nfunc A() int { return 2 }\n")
        self.commit("go")
        rc, out, calls = self.done()
        self.assertEqual(rc, 0, out)
        closure = ["example.com/m/a", "example.com/m/b"]
        self.assertEqual(sorted(c["argv"] for c in self.ran(calls, "go", "test")),
                         sorted([["test", "-short", "example.com/m/a"],
                                 ["test", "-short", "-run", "^(TestScansSource)$", "./c"]]))
        self.assertNotIn("example.com/m/b",
                         [a for c in self.ran(calls, "go", "test") for a in c["argv"]])
        self.assertNotIn("example.com/m/c", [a for c in calls for a in c["argv"]])
        for goos in ("linux", "darwin"):
            vet = [c for c in self.ran(calls, "go", "vet") if c["GOOS"] == goos]
            sc = [c for c in self.ran(calls, "staticcheck") if c["GOOS"] == goos]
            self.assertEqual([c["argv"][-2:] for c in vet], [closure], goos)
            self.assertEqual([c["argv"][-2:] for c in sc], [closure], goos)
        self.assertIn(["-checks=inherit,-SA4023"] + closure,
                      [c["argv"] for c in self.ran(calls, "staticcheck") if c["GOOS"] == "darwin"])
        self.assertEqual([c["argv"] for c in self.ran(calls, "gofmt")], [["-l", "a/a.go"]])
        # The retired pin tool (docs/reference/pack-binaries.md OQ-BP8) is not run.
        self.assertFalse([c for c in self.ran(calls, "go", "run") if "./tools/pack-binaries" in c["argv"]])
        self.assertEqual(self.ran(calls, "just"), [])

    def test_ignored_external_package_does_not_poison_the_go_graph(self):
        self.verified()
        (self.repo / ".git" / "info" / "exclude").write_text("/scratch/\\n")
        self.write("scratch/yolo-packs/matt/skills/headful-browser/references/client.go",
                   'package references\\nimport _ "github.com/gorilla/websocket"\\n')
        self.write("a/a.go", "package a\\n\\nfunc A() int { return 2 }\\n")
        self.commit("go with ignored external package")
        rc, out, calls = self.done()
        self.assertEqual(rc, 0, out)
        self.assertNotIn("go list", out)
        self.assertTrue(self.ran(calls, "go", "test", "-short", "example.com/m/a"))
        self.assertFalse(self.ran(calls, "just", "check-ci"))

    def test_unformatted_go_fails_without_rewriting_it(self):
        self.verified()
        self.write("c/c.go", "package c\n\nfunc C() int { return 4 }\n")
        self.commit("go")
        rc, out, _ = self.done(FAKE_GOFMT_DIRTY="1")
        self.assertEqual(rc, 1, out)
        self.assertIn("just format", out)
        self.assertEqual(self.git("status", "--porcelain"), "")

    def test_every_commit_since_the_baseline_counts(self):
        self.verified()
        self.write("a/a.go", "package a\n\nfunc A() int { return 2 }\n")
        self.commit("go")
        self.write("docs/unread.md", "# Unread\n\nLater doc edit.\n")
        self.commit("docs")
        rc, out, calls = self.done()
        self.assertEqual(rc, 0, out)
        self.assertTrue(self.ran(calls, "go", "test", "-short", "example.com/m/a"))
        self.assertTrue(self.ran(calls, "vantage-check.sh", "docs/unread.md"))

    def test_a_gate_input_runs_the_full_gate(self):
        self.verified()
        self.write("Justfile", "check-ci:\n    echo changed\n")
        self.commit("justfile")
        rc, out, calls = self.done()
        self.assertEqual(rc, 0, out)
        self.assertTrue(self.ran(calls, "just", "check-ci"))
        self.assertIn("Justfile", out)

    def test_a_readers_file_change_routes_selective(self):
        # scripts/ is a global prefix, but the census is this run's OWN selection input, read
        # fresh from the working tree; refreshing it must not force the full gate.
        self.verified()
        (self.repo / "scripts" / "completion-readers.json").write_text(
            json.dumps(dict(READERS, about="fixture, refreshed")))
        self.commit("refresh the census")
        rc, out, calls = self.done()
        self.assertEqual(rc, 0, out)
        self.assertEqual(self.ran(calls, "just", "check-ci"), [])
        self.assertEqual(self.record(self.head())["route"], "selective")
        # The scanning test still runs: a census refresh can move what any test reads.
        self.assertTrue(self.ran(calls, "go", "test", "-short", "-run", "^(TestScansSource)$", "./c"))

    def test_another_script_change_still_runs_the_full_gate(self):
        # Only the census is exempt; every other scripts/ path remains a gate input.
        self.verified()
        self.write("scripts/completion-census.py", "# regenerates the census\n")
        self.commit("a new script")
        rc, out, calls = self.done()
        self.assertEqual(rc, 0, out)
        self.assertTrue(self.ran(calls, "just", "check-ci"))
        self.assertIn("scripts/completion-census.py", out)

    def test_an_unknown_path_runs_the_full_gate(self):
        self.verified()
        self.write("NOTES.txt", "something nothing classifies\n")
        self.commit("unknown")
        rc, out, calls = self.done()
        self.assertEqual(rc, 0, out)
        self.assertTrue(self.ran(calls, "just", "check-ci"))

    def test_a_changed_toolchain_discards_the_baseline(self):
        self.verified()
        self.write("docs/unread.md", "# Unread\n\nedit\n")
        self.commit("docs")
        rc, out, calls = self.done(FAKE_SC_VERSION="staticcheck 2099.1")
        self.assertEqual(rc, 0, out)
        self.assertTrue(self.ran(calls, "just", "check-ci"))

    def test_a_missing_readers_file_runs_the_full_gate(self):
        self.verified()
        # A readers-file change is itself a scripts/ change; delete it in the same commit as a doc.
        (self.repo / "scripts" / "completion-readers.json").unlink()
        self.write("docs/unread.md", "# Unread\n\nedit\n")
        self.commit("drop readers")
        rc, out, calls = self.done()
        self.assertTrue(self.ran(calls, "just", "check-ci"))

    def test_a_rename_classifies_both_names(self):
        self.verified()
        self.git("mv", "docs/read.md", "docs/moved.md")
        self.commit("rename")
        rc, out, calls = self.done()
        self.assertEqual(rc, 0, out)
        self.assertTrue(self.ran(calls, "go", "test", "-short", "-run", "^(TestReadsDoc|TestScansSource)$"))
        self.assertTrue(self.ran(calls, "vantage-check.sh", "docs/moved.md"))

    # -- safety

    def test_a_dirty_tree_is_refused_before_any_check(self):
        self.verified()
        self.write("docs/unread.md", "uncommitted\n")
        rc, out, calls = self.done()
        self.assertEqual(rc, 1)
        self.assertEqual(calls, [])
        self.assertIn("commit", out)

    def test_an_untracked_file_is_refused(self):
        self.write("scratch.txt", "x\n")
        rc, out, calls = self.done()
        self.assertEqual(rc, 1)
        self.assertEqual(calls, [])

    def test_a_failure_records_nothing_and_the_next_run_still_checks_it(self):
        self.verified()
        base = self.head()
        self.write("a/a.go", "package a\n\nfunc A() int { return 5 }\n")
        self.commit("go")
        rc, out, _ = self.done(FAKE_FAIL="go")
        self.assertEqual(rc, 1, out)
        self.assertIsNone(self.record(self.head()))
        self.assertIn("Nothing was recorded", out)
        rc, out, calls = self.done()
        self.assertEqual(rc, 0, out)
        self.assertTrue(self.ran(calls, "go", "test", "-short", "example.com/m/a"))
        self.assertEqual(self.record(self.head())["baseline"], base)

    def test_a_write_during_the_checks_records_nothing(self):
        self.verified()
        self.write("a/a.go", "package a\n\nfunc A() int { return 6 }\n")
        self.commit("go")
        rc, out, _ = self.done(FAKE_TOUCH=str(self.repo / "a" / "late.txt"))
        self.assertEqual(rc, 1, out)
        self.assertIn("changed while the checks ran", out)
        self.assertIsNone(self.record(self.head()))

    def test_a_failing_full_gate_records_nothing(self):
        rc, out, _ = self.done(FAKE_FAIL="just")
        self.assertEqual(rc, 1, out)
        self.assertIsNone(self.record(self.head()))

    def test_another_worktree_reuses_the_verification(self):
        self.verified()
        other = self.tmp / "other"
        self.git("worktree", "add", "-q", "--detach", str(other), "HEAD")
        rc, out, calls = self.done(cwd=other)
        self.assertEqual(rc, 0, out)
        self.assertEqual(calls, [])
        self.assertIn("already verified", out)

    def test_a_record_for_another_tree_is_ignored(self):
        self.verified()
        p = self.repo / ".git" / "yolo-completion" / "verified" / f"{self.head()}.json"
        rec = json.loads(p.read_text())
        rec["tree"] = "0" * 40
        p.write_text(json.dumps(rec))
        rc, out, calls = self.done()
        self.assertTrue(self.ran(calls, "just", "check-ci"))
        p.write_text("[]")
        rc, out, calls = self.done()
        self.assertTrue(self.ran(calls, "just", "check-ci"))


def just_recipe(name):
    text = (REPO / "Justfile").read_text()
    m = re.search(rf"^(?:\[[^\]]*\]\n)*{re.escape(name)}(?: [^:\n]*)?:([^\n]*)\n((?:[ \t]+[^\n]*\n|\n)*)",
                  text, re.M)
    if not m:
        raise AssertionError(f"no `{name}` recipe in the Justfile")
    return m.group(1).split(), [line.strip() for line in m.group(2).splitlines() if line.strip()]


def load_census():
    import importlib.util
    spec = importlib.util.spec_from_file_location("census", HERE / "completion-census.py")
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


class Census(unittest.TestCase):
    def git_fixture(self, with_git=True, ignore=".yolo/\n"):
        temp = Path(tempfile.mkdtemp(prefix="completion-census-source-test-")).resolve()
        self.addCleanup(shutil.rmtree, temp, True)
        root = temp / "repo"
        root.mkdir()
        (root / "pkg").mkdir()
        (root / "pkg" / "pkg_test.go").write_text("package pkg\n")
        real = root / "internal" / "cli" / "run"
        real.mkdir(parents=True)
        (real / "tracked.json").write_text("{}\n")
        docs = root / "docs"
        docs.mkdir()
        (docs / "config.json").write_text("{}\n")
        if with_git:
            (root / ".gitignore").write_text(ignore)
            subprocess.run(["git", "-C", str(root), "init", "-q"], check=True)
            subprocess.run(["git", "-C", str(root), "config", "user.name", "Fixture"], check=True)
            subprocess.run(["git", "-C", str(root), "config", "user.email", "fixture@example.invalid"], check=True)
            subprocess.run(["git", "-C", str(root), "add", "-A"], check=True)
            subprocess.run(["git", "-C", str(root), "commit", "-q", "-m", "fixture"], check=True)
        census = load_census()
        previous_root = census.ROOT
        census.ROOT = root
        self.addCleanup(setattr, census, "ROOT", previous_root)
        return census, root

    def test_ignored_generated_directory_entries_do_not_become_reader_dependencies(self):
        census, root = self.git_fixture()
        transient = root / "internal" / "cli" / "run" / ".yolo"
        transient.mkdir()
        (transient / ".gitignore").write_text("*\n")
        (transient / "housekeeping.log").write_text("generated during another test\n")
        log = "\n".join([
            f"open {transient / '.gitignore'}",
            f"open {transient}",  # A testlog open of a directory records ReadDir membership.
            f"open {transient / 'housekeeping.log'}",
            f"stat {transient}",
            f"stat {transient.parent}",
            f"open {transient.parent / 'tracked.json'}",
            f"stat {root / 'docs'}",
            f"open {root / 'docs' / 'config.json'}",
        ])
        opened, listed, stats = census.parse_log(log, root / "pkg", {"pkg"})
        self.assertEqual(opened, {"internal/cli/run/tracked.json", "docs/config.json"})
        self.assertEqual(listed, {"internal/cli/run", "docs"})
        self.assertEqual(stats, set())

    def test_child_only_ignore_rules_preserve_real_directory_and_gitignore_dependencies(self):
        census, root = self.git_fixture(ignore="")
        transient = root / "internal" / "cli" / "run" / ".yolo"
        transient.mkdir()
        ignore_file = transient / ".gitignore"
        ignore_file.write_text("*\n")
        parent = transient.parent
        prefix = "\n".join([
            f"open {ignore_file}",
            f"open {transient}",  # A testlog open of a directory records ReadDir membership.
            f"stat {transient}",
            f"stat {parent}",
            f"open {parent / 'tracked.json'}",
        ])
        before = census.parse_log(prefix, root / "pkg", {"pkg"})
        housekeeping = transient / "housekeeping.log"
        housekeeping.write_text("ignored by the nested .gitignore\n")
        after = census.parse_log(prefix + f"\nopen {housekeeping}", root / "pkg", {"pkg"})
        self.assertEqual(after, before)
        opened, listed, stats = after
        self.assertEqual(opened, {"internal/cli/run/tracked.json"})
        self.assertEqual(listed, {"internal/cli/run"})
        self.assertEqual(stats, set())

    def test_force_tracked_file_under_ignored_name_remains_a_reader_dependency(self):
        census, root = self.git_fixture()
        tracked = root / "internal" / "cli" / "run" / ".yolo"
        tracked.mkdir()
        (tracked / "keep.json").write_text("tracked despite ignore\n")
        subprocess.run(["git", "-C", str(root), "add", "-f", "internal/cli/run/.yolo/keep.json"], check=True)
        subprocess.run(["git", "-C", str(root), "commit", "-q", "-m", "force tracked ignored file"], check=True)
        (tracked / "keep.json").unlink()  # Cached tracking preserves an observed absence too.
        log = f"open {tracked / 'keep.json'}\nstat {tracked}"
        opened, listed, _ = census.parse_log(log, root / "pkg", {"pkg"})
        self.assertEqual(opened, {"internal/cli/run/.yolo/keep.json"})
        self.assertEqual(listed, {"internal/cli/run/.yolo"})

    def test_missing_nonignored_optional_path_remains_a_dependency(self):
        census, root = self.git_fixture()
        missing = root / "internal" / "cli" / "optional" / "config.json"
        opened, listed, stats = census.parse_log(
            f"stat {missing}", root / "pkg", {"pkg"})
        self.assertEqual(opened, set())
        self.assertEqual(listed, set())
        self.assertEqual(stats, {"internal/cli/optional/config.json"})

    def test_nonignored_untracked_dependency_is_preserved(self):
        census, root = self.git_fixture()
        visibility = census.GitVisibility(root)  # The actual collector snapshots before tests run.
        untracked = root / "assets"
        untracked.mkdir()
        (untracked / "local.json").write_text("visible untracked dependency\n")
        opened, listed, _ = census.parse_log(
            f"open {untracked / 'local.json'}\nstat {untracked}", root / "pkg", {"pkg"}, visibility)
        self.assertEqual(opened, {"assets/local.json"})
        self.assertEqual(listed, {"assets"})

    def test_non_git_archive_keeps_conservative_directory_coverage(self):
        census, root = self.git_fixture(with_git=False)
        ignored_like = root / "pkg" / ".yolo"
        ignored_like.mkdir()
        (ignored_like / "housekeeping.log").write_text("archive has no ignore authority\n")
        opened, listed, _ = census.parse_log(
            f"open {ignored_like / 'housekeeping.log'}\nstat {ignored_like}", root / "other", set())
        self.assertEqual(opened, {"pkg/.yolo/housekeeping.log"})
        self.assertEqual(listed, {"pkg/.yolo"})

    def test_git_inventory_errors_fail_closed(self):
        from unittest import mock
        census, root = self.git_fixture()
        failed = subprocess.CompletedProcess([], 128, stdout=b"", stderr=b"bad Git index")
        with mock.patch.object(census.subprocess, "run", return_value=failed):
            with self.assertRaisesRegex(census.CensusError, "Git source inventory failed"):
                census.GitVisibility(root)

    def test_go_package_and_import_loading_errors_are_actionable(self):
        from unittest import mock
        census = load_census()
        with mock.patch.object(census, "package_patterns", side_effect=RuntimeError("bad module graph")):
            with self.assertRaisesRegex(census.CensusError, "repair the Go package graph.*rerun scripts/completion-census.py"):
                census.go_packages()
        failure = subprocess.CalledProcessError(1, ["go", "list"], stderr=b"missing import")
        with mock.patch.object(census, "package_patterns", return_value=["./pkg"]), \
             mock.patch.object(census.subprocess, "check_output", side_effect=failure):
            with self.assertRaisesRegex(census.CensusError, "go list failed.*missing import") as caught:
                census.go_packages()
            self.assertIn("rerun scripts/completion-census.py", str(caught.exception))

    def test_test_listing_failures_and_timeouts_are_fatal(self):
        from unittest import mock
        census = load_census()
        failed = subprocess.CompletedProcess([], 23, stdout="", stderr="list failed")
        with mock.patch.object(census.subprocess, "run", return_value=failed):
            with self.assertRaisesRegex(census.CensusError, "Go test listing failed"):
                census.list_tests(Path("fixture.test"), Path.cwd())
        with mock.patch.object(census.subprocess, "run", side_effect=subprocess.TimeoutExpired("list", 2)):
            with self.assertRaisesRegex(census.CensusError, "timed out"):
                census.list_tests(Path("fixture.test"), Path.cwd())

    def test_checked_run_reports_mixed_stream_failure_and_stream_controls(self):
        from unittest import mock
        census = load_census()
        mixed = [sys.executable, "-c",
                 "import sys; print('  assertion from stdout  '); print('  listener from stderr  ', file=sys.stderr); sys.exit(23)"]
        with self.assertRaises(census.CensusError) as caught:
            census.checked_run(mixed, HERE, 10, "mixed-stream fixture")
        detail = str(caught.exception)
        self.assertIn("stdout (verbatim):\n  assertion from stdout  \n", detail)
        self.assertIn("stderr (verbatim):\n  listener from stderr  \n", detail)
        self.assertIn(f"command argv: {mixed!r}", detail)
        self.assertIn("direct return code: 23", detail)
        self.assertIn("fix the failing test/collector and rerun scripts/completion-census.py", detail)

        controls = (
            ([sys.executable, "-c", "print('  stdout only  '); raise SystemExit(4)"],
             "  stdout only  \n", "", 4),
            ([sys.executable, "-c", "import sys; print('  stderr only  ', file=sys.stderr); raise SystemExit(5)"],
             "", "  stderr only  \n", 5),
            ([sys.executable, "-c", "raise SystemExit(6)"], "", "", 6),
        )
        for command, stdout, stderr, returncode in controls:
            with self.subTest(returncode=returncode):
                with self.assertRaises(census.CensusError) as caught:
                    census.checked_run(command, HERE, 10, "stream control")
                detail = str(caught.exception)
                self.assertIn(f"command argv: {command!r}", detail)
                self.assertIn(f"direct return code: {returncode}", detail)
                self.assertIn("stdout (verbatim):\n" + (stdout if stdout else "<empty>"), detail)
                self.assertIn("stderr (verbatim):\n" + (stderr if stderr else "<empty>"), detail)

        success = [sys.executable, "-c",
                   "import sys; print('  success stdout  '); print('  success stderr  ', file=sys.stderr)"]
        result = census.checked_run(success, HERE, 10, "successful stream control")
        self.assertEqual(result.args, success)
        self.assertEqual(result.returncode, 0)
        self.assertEqual(result.stdout, "  success stdout  \n")
        self.assertEqual(result.stderr, "  success stderr  \n")

        negative = subprocess.CompletedProcess(["fixture"], -9, stdout="out\n", stderr="err\n")
        with mock.patch.object(census.subprocess, "run", return_value=negative):
            with self.assertRaises(census.CensusError) as caught:
                census.checked_run(["fixture"], HERE, 10, "negative return control")
        self.assertIn("command argv: ['fixture']", str(caught.exception))
        self.assertIn("direct return code: -9", str(caught.exception))
        self.assertNotIn("SIGKILL", str(caught.exception))

    def test_test_failures_and_timeouts_are_fatal(self):
        from unittest import mock
        census = load_census()
        failed = subprocess.CompletedProcess([], 23, stdout="", stderr="assertion failed")
        with mock.patch.object(census.subprocess, "run", return_value=failed):
            with self.assertRaisesRegex(census.CensusError, "Go test fixture TestFixture failed"):
                census.run_test(Path("fixture.test"), Path("fixture"), "TestFixture", Path("missing.log"))
        with mock.patch.object(census.subprocess, "run", side_effect=subprocess.TimeoutExpired("test", 2)):
            with self.assertRaisesRegex(census.CensusError, "timed out"):
                census.run_test(Path("fixture.test"), Path("fixture"), "TestFixture", Path("missing.log"))

    def test_main_propagates_real_collector_phase_failures_without_writing_partial_output(self):
        from unittest import mock
        phases = ("build-fail", "build-timeout", "list-fail", "list-timeout",
                  "test-fail", "test-timeout", "git-error", "success")
        for phase in phases:
            with self.subTest(phase=phase):
                census, root = self.git_fixture()
                (root / "pkg" / "pkg_test.go").write_text("package pkg\n")
                out = root / "generated.json"
                out.write_text("preserve prior generated artifact\n")
                real_run = subprocess.run

                def fake_run(command, **kwargs):
                    if command[0] == "git":
                        if phase == "git-error" and "ls-files" in command:
                            return subprocess.CompletedProcess(command, 128, stdout=b"", stderr=b"bad index")
                        return real_run(command, **kwargs)
                    args = command
                    stage = ("build" if args[0] == "go" else
                             "list" if "-test.list" in args else
                             "test" if "-test.run" in args else "unknown")
                    if phase == f"{stage}-timeout":
                        raise subprocess.TimeoutExpired(command, 2)
                    if phase == f"{stage}-fail":
                        return subprocess.CompletedProcess(command, 23, stdout="", stderr="fixture failure")
                    if stage == "list":
                        return subprocess.CompletedProcess(command, 0, stdout="TestFixture\n", stderr="")
                    if stage == "test":
                        logfile = next(arg.split("=", 1)[1] for arg in args if arg.startswith("-test.testlogfile="))
                        Path(logfile).write_text(f"open {root / 'docs' / 'config.json'}\n")
                    return subprocess.CompletedProcess(command, 0, stdout="", stderr="")

                with mock.patch.object(census, "ROOT", root), mock.patch.object(census, "OUT", out), \
                     mock.patch.object(census, "go_packages",
                                       return_value=([("fixture/pkg", root / "pkg")], {"pkg"})), \
                     mock.patch.dict(os.environ, {"CENSUS_JOBS": "1"}), \
                     mock.patch.object(census.subprocess, "run", side_effect=fake_run):
                    if phase == "success":
                        census.main()
                        self.assertEqual(json.loads(out.read_text())["readers"], {"pkg": {"TestFixture": 0}})
                    else:
                        with self.assertRaises(census.CensusError):
                            census.main()
                        self.assertEqual(out.read_text(), "preserve prior generated artifact\n")

    def test_a_read_in_a_subpackage_of_the_tests_own_directory_is_kept(self):
        census = load_census()
        root = census.ROOT
        log = "\n".join([
            f"open {root}/internal/cli/run/packtree.go",   # another package, under internal/cli
            f"open {root}/internal/cli/configdiff.go",     # the test's own package
            f"open {root}/internal/cli/testdata",          # its own testdata, a non-package dir
            f"open {root}/docs/reference/jail-home.md",
        ])
        pkg_dirs = {"internal/cli", "internal/cli/run"}
        opened, listed, stats = census.parse_log(log, root / "internal" / "cli", pkg_dirs)
        self.assertEqual(sorted(opened | listed),
                         ["docs/reference/jail-home.md", "internal/cli/run/packtree.go"])


class CallSites(unittest.TestCase):
    def test_just_done_runs_the_front_door_and_nothing_that_writes(self):
        deps, body = just_recipe("done")
        self.assertIn("python3 scripts/completion-check.py", [line.lstrip("@") for line in body])
        self.assertNotIn("check", deps, "`done` must not depend on `check`, whose `format` rewrites files")
        self.assertNotIn("format", deps)

    def test_lint_ci_runs_these_tests(self):
        _, body = just_recipe("lint-ci")
        self.assertIn("python3 scripts/test-go-packages.py", body)
        self.assertIn("python3 scripts/test-completion-check.py", body)

    def test_the_markdown_checker_is_pinned(self):
        # An upstream release must not turn an unchanged document red; changing the pin is a
        # scripts/ change, which runs the full gate.
        wrapper = (REPO / "scripts" / "vantage-check.sh").read_text()
        self.assertRegex(wrapper, r"uvx vantage-check@\d+\.\d+\.\d+ ")
        self.assertNotIn("vantage-check@latest", wrapper)

    def test_the_readers_file_names_real_packages_and_tests(self):
        doc = json.loads((REPO / "scripts" / "completion-readers.json").read_text())
        for pkg, tests in doc["readers"].items():
            d = REPO / pkg
            self.assertTrue(d.is_dir(), f"{pkg}: no such package directory; rerun scripts/completion-census.py")
            src = "".join(p.read_text() for p in d.glob("*_test.go"))
            for name in tests:
                self.assertRegex(src, rf"func {re.escape(name)}\(",
                                 f"{pkg}: {name} is gone; rerun scripts/completion-census.py")


if __name__ == "__main__":
    unittest.main()
