#!/usr/bin/env python3
"""Regression checks for scripts/vantage-check.sh, which keeps vantage-check off the gate's shared pipe.

The defect: vantage-check exits with its stdout pipe left O_NONBLOCK, and `just check-ci` runs
it in parallel with `go test` on one pipe, so go test's output for a failing package was cut
short with EAGAIN and CI showed a bare FAIL with no failing test named.
"""
import fcntl
import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest

HERE = Path(__file__).resolve().parent
WRAPPER = HERE / "vantage-check.sh"
JUSTFILE = HERE.parent / "Justfile"

# A stand-in for uvx that does what vantage-check does to its descriptors: it sets O_NONBLOCK
# on its stdin, stdout and stderr, prints, and exits with the status the test asks for. Stdin
# is included so a wrapper that leaves the caller's stdin attached would be caught too.
FAKE_UVX = """#!/usr/bin/env python3
import fcntl, os, sys
for fd in (0, 1, 2):
    try:
        fcntl.fcntl(fd, fcntl.F_SETFL, fcntl.fcntl(fd, fcntl.F_GETFL) | os.O_NONBLOCK)
    except OSError:
        pass
print("fake vantage-check:", " ".join(sys.argv[1:]), flush=True)
sys.exit(int(os.environ.get("FAKE_UVX_STATUS", "0")))
"""


def run_wrapper(status: int):
    """Run the wrapper with a fake uvx, its output on a pipe this test owns, its stdin on a
    second pipe this test owns.

    Returns the exit status, the output, whether the write end of the output pipe was left
    O_NONBLOCK after the wrapper exited, and whether the read end of the stdin pipe (the
    wrapper's stdin) was left O_NONBLOCK."""
    with tempfile.TemporaryDirectory() as bindir:
        uvx = Path(bindir) / "uvx"
        uvx.write_text(FAKE_UVX)
        uvx.chmod(0o755)
        env = dict(os.environ, PATH=bindir + os.pathsep + os.environ["PATH"], FAKE_UVX_STATUS=str(status))
        r, w = os.pipe()
        in_r, in_w = os.pipe()
        try:
            # The write end of the stdin pipe is never used; close it so only the wrapper holds
            # the read end.
            os.close(in_w)
            proc = subprocess.Popen(["bash", str(WRAPPER), "CHANGELOG.md"],
                                    stdin=in_r, stdout=w, stderr=w, env=env)
            rc = proc.wait()
            nonblocking = bool(fcntl.fcntl(w, fcntl.F_GETFL) & os.O_NONBLOCK)
            stdin_nonblocking = bool(fcntl.fcntl(in_r, fcntl.F_GETFL) & os.O_NONBLOCK)
        finally:
            os.close(w)
            os.close(in_r)
        with os.fdopen(r, "rb") as reader:
            out = reader.read().decode()
    return rc, out, nonblocking, stdin_nonblocking


class WrapperTest(unittest.TestCase):
    def test_the_shared_pipe_stays_blocking(self):
        rc, out, nonblocking, stdin_nonblocking = run_wrapper(0)
        self.assertEqual(rc, 0, out)
        self.assertIn("fake vantage-check: vantage-check@latest CHANGELOG.md", out)
        self.assertFalse(nonblocking, "the wrapper let vantage-check set O_NONBLOCK on the caller's pipe")
        self.assertFalse(stdin_nonblocking, "the wrapper let vantage-check set O_NONBLOCK on the caller's stdin")

    def test_a_finding_still_fails_the_gate(self):
        rc, out, _, _ = run_wrapper(3)
        self.assertEqual(rc, 3, "the wrapper must return vantage-check's own status, not cat's")

    def test_the_gate_runs_vantage_check_only_through_the_wrapper(self):
        # The call site: a bare `uvx ... vantage-check` line in the Justfile (with or without
        # flags before the tool) would put the shared pipe back in reach.
        justfile = JUSTFILE.read_text()
        self.assertIn("scripts/vantage-check.sh", justfile)
        bare = [line for line in justfile.splitlines()
                if re.search(r"\buvx\b.*\bvantage-check\b", line) and not line.lstrip().startswith("#")]
        self.assertEqual(bare, [], "run vantage-check through scripts/vantage-check.sh")


if __name__ == "__main__":
    unittest.main()
