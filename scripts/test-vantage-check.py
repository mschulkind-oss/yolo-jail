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

# A stand-in for uvx that does what vantage-check does to its output: it sets O_NONBLOCK on
# its stdout and stderr, prints, and exits with the status the test asks for.
FAKE_UVX = """#!/usr/bin/env python3
import fcntl, os, sys
for fd in (1, 2):
    fcntl.fcntl(fd, fcntl.F_SETFL, fcntl.fcntl(fd, fcntl.F_GETFL) | os.O_NONBLOCK)
print("fake vantage-check:", " ".join(sys.argv[1:]), flush=True)
sys.exit(int(os.environ.get("FAKE_UVX_STATUS", "0")))
"""


def run_wrapper(status: int):
    """Run the wrapper with a fake uvx, its output on a pipe this test owns.

    Returns the exit status, the output, and whether the write end of that pipe was left
    O_NONBLOCK after the wrapper exited."""
    with tempfile.TemporaryDirectory() as bindir:
        uvx = Path(bindir) / "uvx"
        uvx.write_text(FAKE_UVX)
        uvx.chmod(0o755)
        env = dict(os.environ, PATH=bindir + os.pathsep + os.environ["PATH"], FAKE_UVX_STATUS=str(status))
        r, w = os.pipe()
        try:
            proc = subprocess.Popen(["bash", str(WRAPPER), "CHANGELOG.md"], stdout=w, stderr=w, env=env)
            rc = proc.wait()
            nonblocking = bool(fcntl.fcntl(w, fcntl.F_GETFL) & os.O_NONBLOCK)
        finally:
            os.close(w)
        with os.fdopen(r, "rb") as reader:
            out = reader.read().decode()
    return rc, out, nonblocking


class WrapperTest(unittest.TestCase):
    def test_the_shared_pipe_stays_blocking(self):
        rc, out, nonblocking = run_wrapper(0)
        self.assertEqual(rc, 0, out)
        self.assertIn("fake vantage-check: vantage-check@latest CHANGELOG.md", out)
        self.assertFalse(nonblocking, "the wrapper let vantage-check set O_NONBLOCK on the caller's pipe")

    def test_a_finding_still_fails_the_gate(self):
        rc, out, _ = run_wrapper(3)
        self.assertEqual(rc, 3, "the wrapper must return vantage-check's own status, not cat's")

    def test_the_gate_runs_vantage_check_only_through_the_wrapper(self):
        # The call site: a bare `uvx vantage-check` line in the Justfile would put the
        # shared pipe back in reach.
        justfile = JUSTFILE.read_text()
        self.assertIn("scripts/vantage-check.sh", justfile)
        bare = [line for line in justfile.splitlines()
                if re.search(r"\buvx\s+vantage-check", line) and not line.lstrip().startswith("#")]
        self.assertEqual(bare, [], "run vantage-check through scripts/vantage-check.sh")


if __name__ == "__main__":
    unittest.main()
