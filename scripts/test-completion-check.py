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
                    "func TestOther(t *testing.T) {}\n"),
}

READERS = {
    "about": "fixture",
    "sets": [{"read": ["docs/read.md"], "listed": ["docs"]}],
    "readers": {"c": {"TestReadsDoc": 0}},
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
                        GOWORK="off")
        for k in ("FAKE_FAIL", "FAKE_TOUCH", "FAKE_SC_VERSION", "FAKE_GOFMT_DIRTY", "GIT_DIR",
                  "GIT_INDEX_FILE", "GIT_WORK_TREE"):
            self.env.pop(k, None)
        r = self.repo
        (r / "scripts").mkdir(parents=True)
        shutil.copy(FRONT_DOOR, r / "scripts" / "completion-check.py")
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
        self.assertEqual([(c["tool"], c["argv"]) for c in calls],
                         [("vantage-check.sh", ["docs/unread.md"])])
        self.assertEqual(self.record(self.head())["route"], "selective")

    def test_a_document_a_test_reads_runs_that_test_too(self):
        self.verified()
        self.write("docs/read.md", "# Read\n\nOne line changed.\n")
        self.commit("docs")
        rc, out, calls = self.done()
        self.assertEqual(rc, 0, out)
        self.assertEqual(self.ran(calls, "go", "test"),
                         [{"tool": "go", "argv": ["test", "-short", "-run", "^(TestReadsDoc)$", "./c"], "GOOS": None}])
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
        self.assertTrue(self.ran(calls, "go", "test", "-short", "-run", "^(TestReadsDoc)$", "./c"))

    def test_a_leaf_package_change_checks_it_and_its_importers_only(self):
        self.verified()
        self.write("a/a.go", "package a\n\nfunc A() int { return 2 }\n")
        self.commit("go")
        rc, out, calls = self.done()
        self.assertEqual(rc, 0, out)
        want = ["example.com/m/a", "example.com/m/b"]
        self.assertEqual([c["argv"] for c in self.ran(calls, "go", "test")], [["test", "-short"] + want])
        for goos in ("linux", "darwin"):
            vet = [c for c in self.ran(calls, "go", "vet") if c["GOOS"] == goos]
            sc = [c for c in self.ran(calls, "staticcheck") if c["GOOS"] == goos]
            self.assertEqual([c["argv"][-2:] for c in vet], [want], goos)
            self.assertEqual([c["argv"][-2:] for c in sc], [want], goos)
        self.assertIn(["-checks=inherit,-SA4023"] + want,
                      [c["argv"] for c in self.ran(calls, "staticcheck") if c["GOOS"] == "darwin"])
        self.assertEqual([c["argv"] for c in self.ran(calls, "gofmt")], [["-l", "a/a.go"]])
        self.assertTrue(self.ran(calls, "go", "run", "./tools/pack-binaries", "check"))
        self.assertEqual(self.ran(calls, "just"), [])

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
        self.assertTrue(self.ran(calls, "go", "test", "-short", "-run", "^(TestReadsDoc)$"))
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


class CallSites(unittest.TestCase):
    def test_just_done_runs_the_front_door_and_nothing_that_writes(self):
        deps, body = just_recipe("done")
        self.assertIn("python3 scripts/completion-check.py", [line.lstrip("@") for line in body])
        self.assertNotIn("check", deps, "`done` must not depend on `check`, whose `format` rewrites files")
        self.assertNotIn("format", deps)

    def test_lint_ci_runs_these_tests(self):
        _, body = just_recipe("lint-ci")
        self.assertIn("python3 scripts/test-completion-check.py", body)

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
