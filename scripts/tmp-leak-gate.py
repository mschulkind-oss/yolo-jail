#!/usr/bin/env python3
"""Run a command under a private TMPDIR and fail if it leaves anything there.

    python3 scripts/tmp-leak-gate.py <command> [args...]

THE RULE: a test run leaves nothing in TMPDIR. Before this gate, `go test` runs left about
1,000 directories in /tmp in 15 minutes of an agent fan-out (2026-11-13): TestMain directories
that a re-exec'd helper child made and never removed, t.TempDirs of children that were killed
or exited from inside their test, and fixtures that made a directory and never removed it.
Nothing failed, so nothing was fixed.

`just test-fast` (and so `just check-ci`, CI's check-go and check-macos) runs the short suite
through this script. It makes one short private directory, /tmp/yt.XXXXXX (short because
fixtures bind AF_UNIX sockets under TMPDIR, and darwin's sun_path is 104 bytes; see
internal/testtmp.ShortDir), runs the command with TMPDIR pointing at it, and lists whatever is
left there afterward. Any leftover fails the run, even when the command passed. The directory
is removed on every exit path, read-only trees included, so the gate itself leaves nothing.

There is no switch to turn it off: a leak is a test bug, and the fix is in the test.
"""
import os
import shutil
import signal
import stat
import subprocess
import sys
import tempfile

# How a leftover's name points at its maker. A t.TempDir is named after its test
# (<TestName><digits>), and an os.MkdirTemp after the prefix its caller passed.
HOW_TO_FIX = """\
Each name above is either a test's name (a t.TempDir whose cleanup never ran) or the prefix an
os.MkdirTemp/os.CreateTemp call passed. Find the maker with:
    rg -n '<name without its random suffix>' cmd internal integration tools packs
and fix it at the source:
  - a test that makes a temp dir: use t.TempDir(), or register t.Cleanup(os.RemoveAll) at once;
  - production code: remove the directory on every exit path, with a test that fails if the
    removal is deleted;
  - a child process of the test binary (a helper re-exec, a self-exec'd daemon, a killed
    child): call testtmp.Isolate() in the package's TestMain (internal/testtmp), so children
    inherit a private TMPDIR the parent removes.
Rerun the one package under the gate to confirm:
    python3 scripts/tmp-leak-gate.py go test -short -count=1 ./<package>"""


def make_private_dir():
    base = "/tmp" if os.path.isdir("/tmp") and os.access("/tmp", os.W_OK) else None
    return tempfile.mkdtemp(prefix="yt.", dir=base)


def remove_tree(path):
    """Remove path, making read-only directories writable first when that is what stops it."""
    def on_error(func, failed, _exc):
        parent = os.path.dirname(failed)
        for p in (parent, failed):
            try:
                os.chmod(p, os.stat(p).st_mode | stat.S_IWUSR | stat.S_IRUSR | stat.S_IXUSR)
            except OSError:
                pass
        func(failed)
    if sys.version_info >= (3, 12):
        shutil.rmtree(path, onexc=on_error)
    else:
        shutil.rmtree(path, onerror=lambda f, p, e: on_error(f, p, e))


def describe(private, name):
    path = os.path.join(private, name)
    try:
        if os.path.isdir(path) and not os.path.islink(path):
            inside = sorted(os.listdir(path))
            shown = ", ".join(inside[:4]) + (", ..." if len(inside) > 4 else "")
            return f"  {name}/  ({len(inside)} entries{': ' + shown if inside else ''})"
    except OSError:
        pass
    return f"  {name}"


def main(argv):
    if not argv:
        print("usage: tmp-leak-gate.py <command> [args...]", file=sys.stderr)
        return 2
    private = make_private_dir()
    env = dict(os.environ, TMPDIR=private)
    proc = None

    def forward(signum, _frame):
        if proc is not None and proc.poll() is None:
            proc.send_signal(signum)

    for sig in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
        signal.signal(sig, forward)
    try:
        proc = subprocess.Popen(argv, env=env)
        rc = proc.wait()
        if rc < 0:  # killed by a signal: the shell's spelling of it
            rc = 128 - rc
        leftovers = sorted(os.listdir(private))
        if leftovers:
            print(f"\nTMPDIR LEAK: the run left {len(leftovers)} entr{'y' if len(leftovers) == 1 else 'ies'} "
                  f"in its private TMPDIR ({private}):", file=sys.stderr)
            for name in leftovers:
                print(describe(private, name), file=sys.stderr)
            print(HOW_TO_FIX, file=sys.stderr)
            return rc or 1
        return rc
    finally:
        remove_tree(private)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
