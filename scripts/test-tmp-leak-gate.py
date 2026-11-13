#!/usr/bin/env python3
"""Regression tests for scripts/tmp-leak-gate.py and its call site in the Justfile."""
import os
import re
import subprocess
import random
import shutil
import sys
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
GATE = HERE / "tmp-leak-gate.py"
JUSTFILE = HERE.parent / "Justfile"


def run_gate(script, env=None):
    """Run the gate over `sh -c script`; the script learns the private dir from $TMPDIR and
    prints it, so the test can check it is gone afterward."""
    return subprocess.run([sys.executable, str(GATE), "sh", "-c", 'echo "dir=$TMPDIR"; ' + script],
                          capture_output=True, text=True, timeout=60, env=dict(os.environ, **(env or {})))


def private_dir(result):
    m = re.search(r"^dir=(.*)$", result.stdout, re.M)
    assert m, result.stdout
    return m.group(1)


class TmpLeakGateTest(unittest.TestCase):
    def test_a_clean_run_passes_and_leaves_no_private_dir(self):
        r = run_gate('mkdir "$TMPDIR/x" && rm -r "$TMPDIR/x"')
        self.assertEqual(r.returncode, 0, r.stderr)
        d = private_dir(r)
        self.assertTrue(os.path.basename(d).startswith("yt."), d)
        self.assertLessEqual(len(d), 16, "the private TMPDIR must stay short for darwin sockets")
        self.assertFalse(os.path.exists(d))

    def test_a_leftover_fails_a_passing_run_and_is_named_with_the_fix(self):
        r = run_gate('mkdir -p "$TMPDIR/TestSomethingLeaky123/001" "$TMPDIR/yolo-leaky-42"; '
                     'chmod 0555 "$TMPDIR/TestSomethingLeaky123"')
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("TMPDIR LEAK", r.stderr)
        self.assertIn("TestSomethingLeaky123", r.stderr)
        self.assertIn("yolo-leaky-42", r.stderr)
        self.assertIn("rg -n", r.stderr, "the failure must say how to find the maker")
        self.assertIn("testtmp.Isolate", r.stderr)
        self.assertFalse(os.path.exists(private_dir(r)), "a read-only leftover must still be removed")

    def test_a_descendants_isolate_dir_in_tmp_fails_the_run_and_is_removed(self):
        # The shape of a test binary's child started with an environment spelled from scratch:
        # no TMPDIR, so testtmp.Isolate made its directory in /tmp, named after its session.
        make = ("import os, random, sys\n"
                "def b36(n, w):\n"
                "    s = ''\n"
                "    while n: n, r = divmod(n, 36); s = '0123456789abcdefghijklmnopqrstuvwxyz'[r] + s\n"
                "    return s.rjust(w, '0')[-w:]\n"
                "d = '/tmp/tt' + b36(os.getsid(0), 5) + b36(random.randrange(36**4), 4)\n"
                "os.mkdir(d); os.mkdir(d + '/yolo-test-image-bins-1'); print('made=' + d)\n")
        r = run_gate(f"env -u TMPDIR {sys.executable} -c \"$MAKE\"", env={"MAKE": make})
        made = re.search(r"^made=(.*)$", r.stdout, re.M).group(1)
        self.addCleanup(shutil.rmtree, made, True)
        self.assertNotEqual(r.returncode, 0, r.stderr)
        self.assertIn("TMPDIR LEAK", r.stderr)
        self.assertIn(made, r.stderr)
        self.assertIn("yolo-test-image-bins-1", r.stderr, "the entries inside name the package")
        self.assertIn("testtmp.ChildEnv", r.stderr, "the failure must say how to fix it")
        self.assertFalse(os.path.exists(made), "the run's own leftover must be removed")

    def test_another_sessions_isolate_dir_is_left_alone(self):
        # No pid reaches zzzzz in base 36, so no run of the gate is this session.
        other = "/tmp/ttzzzzz" + "".join(random.choice("abcdefghij") for _ in range(4))
        os.mkdir(other)
        self.addCleanup(os.rmdir, other)
        r = run_gate("true")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertTrue(os.path.isdir(other), "the gate removed a directory another session made")

    def test_the_command_runs_in_a_session_of_its_own(self):
        r = run_gate(f"{sys.executable} -c 'import os; print(\"sid=%d %d %d\" % (os.getsid(0), os.getpid(), os.getppid()))'")
        self.assertEqual(r.returncode, 0, r.stderr)
        sid, pid, ppid = map(int, re.search(r"sid=(\d+) (\d+) (\d+)", r.stdout).groups())
        # The gate's child is `sh`, or the python sh exec'd in its place: either leads the session.
        self.assertIn(sid, (pid, ppid), "the command must lead its own session, or a leftover names no run")
        self.assertNotEqual(sid, os.getsid(0))

    def test_the_commands_failure_is_kept(self):
        r = run_gate("exit 7")
        self.assertEqual(r.returncode, 7)
        self.assertFalse(os.path.exists(private_dir(r)))

    def test_test_fast_runs_under_the_gate(self):
        text = JUSTFILE.read_text()
        m = re.search(r"^test-fast:\n((?:    .*\n)+)", text, re.M)
        self.assertIsNotNone(m, "no test-fast recipe")
        body = m.group(1)
        self.assertRegex(body, r"python3 scripts/tmp-leak-gate\.py python3 scripts/go_packages\.py run go test -short",
                         "test-fast must run the short suite under scripts/tmp-leak-gate.py")

    def test_lint_ci_runs_this_test(self):
        m = re.search(r"^lint-ci:.*\n((?:    .*\n|\s*#.*\n)+)", JUSTFILE.read_text(), re.M)
        self.assertIsNotNone(m)
        self.assertIn("python3 scripts/test-tmp-leak-gate.py", m.group(1))


if __name__ == "__main__":
    unittest.main()
