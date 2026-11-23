#!/usr/bin/env python3
"""Regression tests for Git-aware Go package enumeration and its production call sites."""
import importlib.util
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
sys.path.insert(0, str(HERE))
from go_packages import SHORT_TEST_TIMEOUT  # noqa: E402
REPO = HERE.parent
REAL_GO = shutil.which("go")
JUST = shutil.which("just")

FAKE_GO = r'''#!{python}
import json, os, sys
if sys.argv[1:2] == ["list"]:
    os.execv({real_go!r}, [{real_go!r}] + sys.argv[1:])
with open(os.environ["GO_CALL_LOG"], "a") as f:
    f.write(json.dumps({{"tool": "go", "argv": sys.argv[1:], "GOOS": os.environ.get("GOOS")}}) + "\n")
sys.exit(0)
'''

ARCHIVE_BOOTSTRAP = r'''import os
import runpy
from pathlib import Path

fixture_root = Path(__file__).resolve().parent.parent
original_lexists = os.path.lexists

def lexists_without_ambient_git(path):
    candidate = Path(os.path.abspath(os.fsdecode(path)))
    if candidate.name == ".git":
        try:
            candidate.relative_to(fixture_root)
        except ValueError:
            return False
    return original_lexists(path)

# Model a source archive below the checkout without hiding .git inside the fixture root.
os.path.lexists = lexists_without_ambient_git
runpy.run_path(str(Path(__file__).with_name("go_packages_production.py")), run_name="__main__")
'''


@unittest.skipIf(REAL_GO is None, "go is not on PATH")
class GoPackageSelection(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp(prefix="go-package-selection-"))
        self.addCleanup(shutil.rmtree, self.tmp, True)
        self.root = self.tmp / "repo"
        self.root.mkdir()
        self.env = dict(os.environ, GOPROXY="off", GOTOOLCHAIN="local", GOWORK="off")
        self.write("go.mod", "module example.test/project\n\ngo 1.21\n")
        self.write(".gitignore", "/scratch/\n/pkg/retained.go\n/ignoredpkg/\n")
        self.write("pkg/pkg.go", "package pkg\nfunc Value() int { return 1 }\n")
        self.write("pkg/pkg_test.go", "package pkg\nimport \"testing\"\nfunc TestValue(t *testing.T) {}\n")
        self.git("init", "-q", "-b", "main")
        self.git("config", "user.name", "Fixture")
        self.git("config", "user.email", "fixture@example.invalid")
        self.commit("fixture")
        (self.root / "scripts").mkdir()
        shutil.copy(HERE / "go_packages.py", self.root / "scripts" / "go_packages.py")
        # `just test-fast` runs the suite through the TMPDIR leak gate.
        shutil.copy(HERE / "tmp-leak-gate.py", self.root / "scripts" / "tmp-leak-gate.py")

    def write(self, path, contents):
        file = self.root / path
        file.parent.mkdir(parents=True, exist_ok=True)
        file.write_text(contents)

    def git(self, *args):
        return subprocess.run(["git", "-C", str(self.root), *args], env=self.env, check=True,
                              stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True).stdout

    def commit(self, message):
        self.git("add", "-A")
        self.git("commit", "-qm", message)

    def selected(self, env=None):
        result = subprocess.run([sys.executable, str(self.root / "scripts" / "go_packages.py")],
                                cwd=self.root, env=env or self.env, text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        return result.stdout.splitlines()

    def run_go(self, *args, env=None):
        result = subprocess.run([sys.executable, str(self.root / "scripts" / "go_packages.py"),
                                 "run", "go", *args], cwd=self.root,
                                env=env or self.env, text=True, capture_output=True)
        return result.returncode, result.stdout + result.stderr

    def test_non_git_source_archive_uses_go_wildcard_and_preserves_failures(self):
        self.assertTrue(os.path.lexists(self.root / ".git"), "fixture starts as a real Git checkout")
        ambient_git_metadata = os.path.lexists(REPO / ".git")
        shutil.rmtree(self.root / ".git")
        self.assertFalse(os.path.lexists(self.root / ".git"), "fixture's own Git metadata was removed")
        self.assertEqual(os.path.lexists(REPO / ".git"), ambient_git_metadata,
                         "archive fixture must preserve the ambient checkout metadata state")
        production = self.root / "scripts" / "go_packages.py"
        archive_production = production.with_name("go_packages_production.py")
        production.rename(archive_production)
        self.assertEqual(archive_production.read_bytes(), (HERE / "go_packages.py").read_bytes())
        production.write_text(ARCHIVE_BOOTSTRAP)
        self.assertEqual(self.selected(), ["./pkg"])
        rc, output = self.run_go("test")
        self.assertEqual(rc, 0, output)

        if JUST is not None:
            self.write("integration/integration_test.go", "package integration\n")
            shutil.copy(REPO / "Justfile", self.root / "Justfile")
            bindir = self.tmp / "archive-bin"
            bindir.mkdir()
            fake_go = bindir / "go"
            fake_go.write_text(FAKE_GO.format(python=sys.executable, real_go=REAL_GO))
            fake_go.chmod(0o755)
            log = self.tmp / "archive-calls.jsonl"
            env = dict(self.env, PATH=str(bindir) + os.pathsep + os.environ["PATH"],
                       GO_CALL_LOG=str(log))
            for recipe in ("test-fast", "test"):
                subprocess.run([JUST, "--justfile", str(self.root / "Justfile"), recipe],
                               cwd=self.root, env=env, check=True, capture_output=True, text=True,
                               timeout=120)
            calls = [json.loads(line) for line in log.read_text().splitlines()]
            go_calls = [call["argv"] for call in calls]
            self.assertIn(["test", "-short", f"-timeout={SHORT_TEST_TIMEOUT}", "./..."], go_calls)
            self.assertIn(["test", "-short", "./..."], go_calls)
            self.assertIn(["test", "-count=1", "-timeout", "0", "./integration"], go_calls)

        self.write("broken/broken.go", "//go:build linux &&\n\npackage broken\n")
        rc, output = self.run_go("test")
        self.assertNotEqual(rc, 0, output)
        self.assertIn("broken", output.lower())

        self.write("missing/missing.go", 'package missing\nimport _ "github.com/gorilla/websocket"\n')
        rc, output = self.run_go("test")
        self.assertNotEqual(rc, 0, output)
        self.assertIn("github.com/gorilla/websocket", output)

    def test_git_enumeration_failure_in_a_checkout_does_not_fall_back(self):
        bindir = self.tmp / "broken-git-bin"
        bindir.mkdir()
        git = bindir / "git"
        git.write_text("#!/bin/sh\necho injected Git failure >&2\nexit 23\n")
        git.chmod(0o755)
        result = subprocess.run([sys.executable, str(self.root / "scripts" / "go_packages.py")],
                                cwd=self.root, env=dict(self.env, PATH=str(bindir) + os.pathsep + os.environ["PATH"]),
                                text=True, capture_output=True)
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn("injected Git failure", result.stderr)

    def test_ignored_external_package_is_the_red_case_but_is_not_selected(self):
        self.write("scratch/yolo-packs/matt/skills/headful-browser/references/client.go",
                   'package references\nimport _ "github.com/gorilla/websocket"\n')
        ignored = subprocess.run(["git", "-C", str(self.root), "check-ignore", "-q",
                                  "scratch/yolo-packs/matt/skills/headful-browser/references/client.go"],
                                 env=self.env, check=False)
        self.assertEqual(ignored.returncode, 0)
        old = subprocess.run(["go", "list", "./..."], cwd=self.root, env=self.env,
                             text=True, capture_output=True)
        self.assertNotEqual(old.returncode, 0, old.stdout)
        self.assertIn("github.com/gorilla/websocket", old.stderr)
        self.assertEqual(self.selected(), ["./pkg"])
        rc, output = self.run_go("test", "./pkg")
        self.assertEqual(rc, 0, output)

    def test_untracked_nonignored_package_is_selected_and_run(self):
        self.write("newpkg/extra source.go", "package newpackage\nfunc Extra() int { return 2 }\n")
        self.assertEqual(self.selected(), ["./newpkg", "./pkg"])
        rc, output = self.run_go("test")
        self.assertEqual(rc, 0, output)

    def test_tracked_files_matched_by_ignore_rules_remain_visible_inside_package(self):
        self.write("pkg/retained.go", 'package pkg\nimport _ "github.com/gorilla/websocket"\n')
        self.git("add", "-f", "pkg/retained.go")
        self.commit("tracked ignored source")
        self.assertEqual(self.selected(), ["./pkg"])
        rc, output = self.run_go("test", "./pkg")
        self.assertNotEqual(rc, 0, output)
        self.assertIn("github.com/gorilla/websocket", output)

    def test_tracked_ignored_directory_package_is_selected(self):
        self.write("ignoredpkg/pkg.go", "package ignoredpkg\nfunc Value() int { return 1 }\n")
        self.git("add", "-f", "ignoredpkg/pkg.go")
        self.commit("tracked package under ignored directory")
        self.assertEqual(self.selected(), ["./ignoredpkg", "./pkg"])

    def test_ignored_extra_go_file_inside_selected_package_fails_checks(self):
        self.write("pkg/retained.go", 'package pkg\nimport _ "github.com/gorilla/websocket"\n')
        ignored = subprocess.run(["git", "-C", str(self.root), "check-ignore", "-q",
                                  "pkg/retained.go"], env=self.env, check=False)
        self.assertEqual(ignored.returncode, 0)
        self.assertEqual(self.selected(), ["./pkg"])
        rc, output = self.run_go("test", "./pkg")
        self.assertNotEqual(rc, 0, output)
        self.assertIn("github.com/gorilla/websocket", output)

    def test_tracked_syntax_error_fails_the_selected_package(self):
        self.write("pkg/pkg.go", "package pkg\nfunc Value( {\n")
        rc, output = self.run_go("test", "./pkg")
        self.assertNotEqual(rc, 0, output)
        self.assertIn("syntax", output.lower())

    def test_malformed_build_constraint_fails_checks_graph_and_census(self):
        self.write("broken/broken.go", "//go:build linux &&\n\npackage broken\n")
        self.git("add", "broken/broken.go")
        self.commit("malformed build constraint")

        result = subprocess.run([sys.executable, str(self.root / "scripts" / "go_packages.py")],
                                cwd=self.root, env=self.env, text=True, capture_output=True)
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn("broken", result.stderr.lower())

        for command in ("test", "vet"):
            rc, output = self.run_go(command)
            self.assertNotEqual(rc, 0, output)
            self.assertIn("broken", output.lower())

        check = load_module("completion_check_broken", HERE / "completion-check.py")
        self.assertIsNone(check.go_graph(self.root, "linux"))
        shutil.copy(HERE / "completion-census.py", self.root / "scripts" / "completion-census.py")
        census = load_module("completion_census_broken", self.root / "scripts" / "completion-census.py")
        with self.assertRaises(RuntimeError):
            census.go_packages()

        self.git("rm", "broken/broken.go")
        self.commit("remove tracked malformed build constraint")
        self.write("untrackedbroken/broken.go", "//go:build linux &&\n\npackage untrackedbroken\n")
        result = subprocess.run([sys.executable, str(self.root / "scripts" / "go_packages.py")],
                                cwd=self.root, env=self.env, text=True, capture_output=True)
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn("untrackedbroken", result.stderr.lower())

    def test_nonignored_untracked_missing_dependency_fails(self):
        self.write("newpkg/bad.go", 'package newpkg\nimport _ "github.com/gorilla/websocket"\n')
        self.assertIn("./newpkg", self.selected())
        rc, output = self.run_go("test")
        self.assertNotEqual(rc, 0, output)
        self.assertIn("github.com/gorilla/websocket", output)

    def test_go_build_constraints_hidden_dirs_vendor_and_nested_modules(self):
        self.write("darwinonly/only_darwin.go", "//go:build darwin\n\npackage darwinonly\n")
        self.write(".hidden/hidden.go", "package hidden\n")
        self.write("_private/private.go", "package private\n")
        self.write("vendor/dep/dep.go", "package dep\n")
        self.write("vendor/top.go", "package vendor\n")
        self.write("pkg/vendor/local.go", "package vendor\n")
        self.write("testdata/fixture.go", "package fixture\n")
        self.write("nested/go.mod", "module example.test/nested\n\ngo 1.21\n")
        self.write("nested/nested.go", "package nested\n")
        for path in ("darwinonly/only_darwin.go", ".hidden/hidden.go", "_private/private.go",
                     "vendor/dep/dep.go", "vendor/top.go", "pkg/vendor/local.go",
                     "testdata/fixture.go", "nested/go.mod", "nested/nested.go"):
            self.git("add", "-f", path)
        self.commit("exclusions")
        self.assertEqual(self.selected(dict(self.env, GOOS="linux")), ["./pkg"])
        self.assertEqual(self.selected(dict(self.env, GOOS="darwin")), ["./darwinonly", "./pkg"])

    def test_completion_graph_and_census_use_the_shared_selector(self):
        self.write("scratch/bad.go", 'package scratch\nimport _ "github.com/gorilla/websocket"\n')
        check = load_module("completion_check", HERE / "completion-check.py")
        graph = check.go_graph(self.root, "linux")
        self.assertIsNotNone(graph)
        self.assertEqual({p["rel"] for p in graph.values()}, {"pkg"})

        shutil.copy(HERE / "completion-census.py", self.root / "scripts" / "completion-census.py")
        census = load_module("completion_census", self.root / "scripts" / "completion-census.py")
        pkgs, dirs = census.go_packages()
        self.assertEqual({ip for ip, _ in pkgs}, {"example.test/project/pkg"})
        self.assertEqual(dirs, {"pkg"})

    def test_completion_graph_names_package_dirs_through_a_symlinked_root(self):
        # macOS's TMPDIR is under /var -> /private/var, and go list reports each Dir resolved;
        # a checkout reached through any symlink must still yield rel paths inside it.
        link = self.tmp / "link"
        link.symlink_to(self.root)
        check = load_module("completion_check_symlink", HERE / "completion-check.py")
        graph = check.go_graph(link, "linux")
        self.assertIsNotNone(graph)
        self.assertEqual({p["rel"] for p in graph.values()}, {"pkg"})

    @unittest.skipIf(JUST is None, "just is not on PATH")
    def test_production_just_lint_and_test_recipes_use_selected_packages(self):
        shutil.copy(REPO / "Justfile", self.root / "Justfile")
        self.write("integration/integration_test.go", "package integration\n")
        self.commit("integration package")
        bindir = self.tmp / "bin"
        bindir.mkdir()
        go = bindir / "go"
        go.write_text(FAKE_GO.format(python=sys.executable, real_go=REAL_GO))
        go.chmod(0o755)
        staticcheck = bindir / "staticcheck"
        staticcheck.write_text("#!" + sys.executable + "\nimport json, os, sys\n"
                               "open(os.environ['GO_CALL_LOG'], 'a').write(json.dumps({'tool':'staticcheck',"
                               "'argv':sys.argv[1:],'GOOS':os.environ.get('GOOS')})+'\\n')\n")
        staticcheck.chmod(0o755)
        log = self.tmp / "calls.jsonl"
        env = dict(self.env, PATH=str(bindir) + os.pathsep + os.environ["PATH"],
                   GO_CALL_LOG=str(log))
        for recipe in ("lint", "test-fast", "test"):
            subprocess.run([JUST, "--justfile", str(self.root / "Justfile"), recipe],
                           cwd=self.root, env=env, check=True, capture_output=True, text=True,
                           timeout=120)
        calls = [json.loads(line) for line in log.read_text().splitlines()]
        go_calls = [call for call in calls if call["tool"] == "go"]
        vet = [c for c in go_calls if c["argv"][:1] == ["vet"]]
        self.assertEqual({c["GOOS"] for c in vet}, {"linux", "darwin"})
        self.assertTrue(all(c["argv"][-1:] == ["./pkg"] for c in vet))
        static = [c for c in calls if c["tool"] == "staticcheck"]
        self.assertEqual({c["GOOS"] for c in static}, {"linux", "darwin"})
        self.assertTrue(all(c["argv"][-1:] == ["./pkg"] for c in static))
        tests = [c["argv"] for c in go_calls if c["argv"][:1] == ["test"]]
        self.assertIn(["test", "-short", f"-timeout={SHORT_TEST_TIMEOUT}", "./integration", "./pkg"], tests)
        self.assertIn(["test", "-short", "./integration", "./pkg"], tests)
        self.assertIn(["test", "-count=1", "-timeout", "0", "./integration"], tests)
        self.assertEqual((REPO / "Justfile").read_text().count("python3 scripts/go_packages.py run"), 7)

    def test_lint_ci_calls_this_test(self):
        text = (REPO / "Justfile").read_text()
        self.assertIn("python3 scripts/test-go-packages.py", text)


CI_WORKFLOW = REPO / ".github" / "workflows" / "ci.yml"


def ci_check_go_block(text):
    lines = text.splitlines()
    jobs = [i for i, line in enumerate(lines) if re.fullmatch(r"  check-go:\s*", line)]
    if len(jobs) != 1:
        raise AssertionError("expected exactly one jobs.check-go block")
    start = jobs[0]
    end = next((i for i in range(start + 1, len(lines))
                if re.fullmatch(r"  [A-Za-z0-9_-]+:\s*", lines[i])), len(lines))
    return lines[start:end]


def replace_ci_check_go_fragment(text, fragment):
    """Remove one exact callsite only inside jobs.check-go for a mutation fixture."""
    job = "\n".join(ci_check_go_block(text))
    needle = fragment.rstrip("\n")
    if job.count(needle) != 1:
        raise AssertionError("expected one check-go mutation fragment")
    return text.replace(job, job.replace(needle, "", 1), 1)


def ci_check_go_steps(text):
    """Extract the known check-go step shape without treating comments as YAML fields."""
    job = ci_check_go_block(text)
    steps_headers = [i for i, line in enumerate(job) if re.fullmatch(r"    steps:\s*", line)]
    if len(steps_headers) != 1:
        raise AssertionError("expected exactly one check-go steps block")
    header = steps_headers[0]
    starts = [i for i in range(header + 1, len(job)) if re.match(r"^      -(?:\s|$)", job[i])]
    if not starts:
        raise AssertionError("check-go has no steps")
    if any(re.fullmatch(r"    continue-on-error:\s*.*", line) for line in job):
        raise AssertionError("check-go job must not mask errors")
    steps = []
    for number, offset in enumerate(starts):
        stop = starts[number + 1] if number + 1 < len(starts) else len(job)
        step = job[offset:stop]
        names = []
        first = re.fullmatch(r"      - name:\s*(.*?)\s*", step[0])
        if first:
            names.append(first.group(1))
        names.extend(m.group(1) for line in step[1:]
                     if (m := re.fullmatch(r"        name:\s*(.*?)\s*", line)))
        if len(names) > 1:
            raise AssertionError("check-go step must not duplicate its name")
        steps.append((names[0] if names else None, step))
    return steps


def ci_step_fields(step, key):
    """Read one step-level scalar or block field, excluding comments and nested `with` keys."""
    matches = [(i, re.fullmatch(r"        " + re.escape(key) + r":(?:\s+(.*?))?\s*", line))
               for i, line in enumerate(step)]
    matches = [(i, match) for i, match in matches if match]
    if len(matches) != 1:
        raise AssertionError("expected exactly one step field: " + key)
    index, match = matches[0]
    value = (match.group(1) or "").strip()
    if value == "|":
        body = []
        for line in step[index + 1:]:
            if line.strip() and not line.startswith("          "):
                break
            body.append(line[10:] if line.startswith("          ") else "")
        return "\n".join(body).rstrip()
    return value


def ci_with_fields(step):
    headers = [i for i, line in enumerate(step) if re.fullmatch(r"        with:\s*", line)]
    if len(headers) != 1:
        raise AssertionError("expected exactly one upload `with` block")
    values = {}
    for line in step[headers[0] + 1:]:
        if not line.strip():
            continue
        match = re.fullmatch(r"          ([A-Za-z0-9_-]+):(?:\s+(.*?))?\s*", line)
        if match:
            key, value = match.group(1), (match.group(2) or "").strip()
            if key in values:
                raise AssertionError("duplicate upload field: " + key)
            if value == "|":
                values[key] = "\n".join(
                    item[12:] for item in step[step.index(line) + 1:]
                    if item.startswith("            ")).rstrip()
            else:
                values[key] = value
    return values


class CIWorkflowDiagnostic(unittest.TestCase):
    def assert_workflow_contract(self, text):
        steps = ci_check_go_steps(text)
        by_name = {}
        for name, block in steps:
            if name is None:
                continue
            if name in by_name:
                raise AssertionError("duplicate check-go step: " + name)
            by_name[name] = block
        gate_name = "Quality gate (vet + staticcheck + gofmt + short tests)"
        diagnostic_name = "Capture CLI/run test events after a quality-gate failure"
        upload_name = "Preserve failed quality-gate output"
        for expected in (gate_name, diagnostic_name, upload_name):
            if expected not in by_name:
                raise AssertionError("missing check-go step: " + expected)
        gate = by_name[gate_name]
        diagnostic = by_name[diagnostic_name]
        upload = by_name[upload_name]
        names = [name for name, _ in steps]
        self.assertLess(names.index(gate_name), names.index(diagnostic_name))
        self.assertLess(names.index(diagnostic_name), names.index(upload_name))

        self.assertEqual(ci_step_fields(gate, "id"), "quality_gate")
        self.assertEqual(ci_step_fields(gate, "run"),
                         'set -o pipefail\njust check-ci 2>&1 | tee "$RUNNER_TEMP/check-go-output.log"')
        expected_condition = "${{ failure() && steps.quality_gate.outcome == 'failure' }}"
        self.assertEqual(ci_step_fields(diagnostic, "if"), expected_condition)
        self.assertEqual(ci_step_fields(diagnostic, "run"),
                         "go test -short -count=1 -timeout=" + SHORT_TEST_TIMEOUT +
                         " -json ./internal/cli/run > \"$RUNNER_TEMP/check-go-cli-run.jsonl\" 2>&1",
                         "diagnostic timeout/command must match SHORT_TEST_TIMEOUT")
        self.assertEqual(ci_step_fields(upload, "if"), expected_condition)
        self.assertEqual(ci_step_fields(upload, "uses"), "actions/upload-artifact@v7")
        upload_fields = ci_with_fields(upload)
        self.assertEqual(upload_fields.get("name"), "check-go-output-attempt-${{ github.run_attempt }}")
        self.assertEqual(upload_fields.get("path"),
                         "${{ runner.temp }}/check-go-output.log\n"
                         "${{ runner.temp }}/check-go-cli-run.jsonl")
        self.assertEqual(upload_fields.get("retention-days"), "7")
        self.assertEqual(upload_fields.get("if-no-files-found"), "error")
        for _, block in steps:
            self.assertEqual(sum(bool(re.fullmatch(r"        continue-on-error:\s*.*", line))
                                 for line in block), 0)
        for name, block in steps:
            if name in (gate_name, diagnostic_name):
                self.assertNotIn("|| true", ci_step_fields(block, "run"))
                self.assertNotIn("exit 0", ci_step_fields(block, "run"))

    def test_ci_failure_diagnostic_uses_the_short_suite_timeout(self):
        self.assert_workflow_contract(CI_WORKFLOW.read_text())

    def test_ci_quality_gate_stays_fatal_and_owns_diagnostic(self):
        self.assert_workflow_contract(CI_WORKFLOW.read_text())

    def test_ci_failure_artifact_retains_both_logs(self):
        self.assert_workflow_contract(CI_WORKFLOW.read_text())

    def test_removed_workflow_callsite_fields_fail_closed(self):
        source = CI_WORKFLOW.read_text()
        # Only the mutation fixture gets the intended bound. Production stays untouched;
        # prove this SAME helper accepts the baseline before testing any deletion.
        job = "\n".join(ci_check_go_block(source))
        timeout = re.compile(r"(?<=go test -short -count=1 -timeout=)[^ ]+(?= -json)")
        self.assertEqual(len(timeout.findall(job)), 1, "one diagnostic timeout fixture")
        valid_job = timeout.sub(SHORT_TEST_TIMEOUT, job)
        source = source.replace(job, valid_job, 1)
        self.assert_workflow_contract(source)
        condition = "${{ failure() && steps.quality_gate.outcome == 'failure' }}"
        diagnostic_run = next(line for line in source.splitlines()
                              if re.fullmatch(r'        run: go test -short -count=1 -timeout=[^ ]+ '
                                              r'-json \.\/internal/cli/run > '
                                              r'"\$RUNNER_TEMP/check-go-cli-run\.jsonl" 2>&1', line))
        diagnostic = ("      - name: Capture CLI/run test events after a quality-gate failure\n"
                      "        if: " + condition + "\n" + diagnostic_run + "\n")
        gate_run = ('        run: |\n          set -o pipefail\n'
                    '          just check-ci 2>&1 | tee "$RUNNER_TEMP/check-go-output.log"\n')
        upload_condition = "        if: " + condition + "\n        uses: actions/upload-artifact@v7\n"
        removals = (
            (diagnostic_run + "\n", "diagnostic executable run", "step field: run"),
            (diagnostic, "diagnostic step and condition", "missing check-go step: Capture CLI/run"),
            ("        if: " + condition + "\n" + diagnostic_run + "\n", "diagnostic condition", "step field: if"),
            ("        id: quality_gate\n", "quality gate id", "step field: id"),
            (gate_run, "quality gate pipefail and tee command", "step field: run"),
            ("          set -o pipefail\n", "quality gate pipefail", "set -o pipefail"),
            ("      - name: Preserve failed quality-gate output\n", "upload step", "missing check-go step: Preserve"),
            ("        uses: actions/upload-artifact@v7\n", "upload action", "step field: uses"),
            ("          name: check-go-output-attempt-${{ github.run_attempt }}\n", "attempt-qualified artifact name", "check-go-output-attempt"),
            ("            ${{ runner.temp }}/check-go-cli-run.jsonl\n", "diagnostic artifact path", "check-go-cli-run"),
            ("          retention-days: 7\n", "artifact retention", "None != '7'"),
            ("          if-no-files-found: error\n", "missing-files error policy", "None != 'error'"),
            (upload_condition, "upload failure condition", "step field: if"),
        )
        for fragment, label, error in removals:
            with self.subTest(removed=label):
                self.assertEqual("\n".join(ci_check_go_block(source)).count(fragment.rstrip("\n")),
                                 1, label + " fixture is unambiguous")
                with self.assertRaisesRegex(AssertionError, error, msg=label):
                    self.assert_workflow_contract(replace_ci_check_go_fragment(source, fragment))


def load_module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


if __name__ == "__main__":
    unittest.main()
