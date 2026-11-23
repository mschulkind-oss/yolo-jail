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

A child whose environment a test spelled from scratch never sees that TMPDIR: it writes to the
machine's /tmp, which no TMPDIR check reaches (the 2026-11-13 census runs left 293 directories
there that way). So the command runs in a session of its own, and testtmp.Isolate names each
directory it makes after its session (internal/testtmp, SessionDirPrefix). One such directory
of this run's session still in /tmp afterward fails the run too, named, and is removed.

There is no switch to turn it off: a leak is a test bug, and the fix is in the test.
"""
import os
import re
import shutil
import signal
import stat
import subprocess
import sys
import tempfile
import time

PR_SET_CHILD_SUBREAPER = 36

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


def become_subreaper():
    """Make every orphaned descendant of the command reparent to this process (Linux), so the
    gate can see and reap what outlives the command. Returns whether it took effect."""
    if not sys.platform.startswith("linux"):
        return False
    try:
        import ctypes
        libc = ctypes.CDLL(None, use_errno=True)
        return libc.prctl(PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0) == 0
    except (OSError, AttributeError):
        return False


def adopted_children():
    """Pids of this process's live children, read from /proc (Linux)."""
    me = os.getpid()
    found = []
    try:
        names = os.listdir("/proc")
    except OSError:
        return found
    for name in names:
        if not name.isdigit():
            continue
        try:
            with open(f"/proc/{name}/stat") as f:
                stat_line = f.read()
        except OSError:
            continue
        # "pid (comm) state ppid ...": comm may hold spaces and parens, so split after the last ")".
        fields = stat_line.rsplit(")", 1)[1].split()
        if len(fields) > 1 and int(fields[1]) == me and fields[0] != "Z":
            found.append(int(name))
    return found


def reap_survivors(command_pid):
    """Kill and reap every process the command left running after it exited. A test that leaves a
    grandchild behind keeps writing into the private TMPDIR while the gate scans and removes it,
    so the gate waits on each one's death (waitpid, not a sleep) before it looks at the directory.
    Returns the pids it had to kill."""
    killed = adopted_children()  # read BEFORE any kill: a killed child turns into a zombie
    try:
        os.killpg(command_pid, signal.SIGKILL)  # the command's own group, orphans included
    except OSError:
        pass
    for pid in killed:
        try:
            os.kill(pid, signal.SIGKILL)
        except OSError:
            pass
    while True:  # blocks on each death; ECHILD means none is left
        try:
            os.waitpid(-1, 0)
        except ChildProcessError:
            break
        except InterruptedError:
            continue
    return killed


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


# A directory testtmp.Isolate made (internal/testtmp, SessionDirPrefix): "tt", the maker's session
# id in five base-36 digits, four random base-36 digits.
SESSION_DIR = re.compile(r"^tt([0-9a-z]{5})[0-9a-z]{4}$")

SESSION_HOW_TO_FIX = """\
Each is the private TMPDIR a child of a Go test binary made for itself (testtmp.Isolate) because
a test started it with an environment spelled from scratch: without the parent's TMPDIR and its
TESTTMP_ROOT marker, the child isolated again in the machine's /tmp and exited from inside its
test before removing that directory. The entries inside name the package (TestMain's own
directories). Find the exec.Cmd whose Env is a fresh list and that reaches the test binary again
(directly, through a stub `yolo` script, or through a program that runs one):
    rg -n 'Env = \\[\\]string\\{|Env = append\\(\\[\\]string\\{' <package>
and wrap it: cmd.Env = testtmp.ChildEnv([]string{...})."""


def session_leftovers(sid, since, dirs=None):
    """The directories testtmp.Isolate made in session `sid` that are still in the machine's
    temporary directories: each one a child of a test binary that lost its parent's private
    TMPDIR. `since` (a time.time()) excludes a directory last changed before the run began, so a
    reused session id never names an older run's leftover."""
    if dirs is None:
        dirs = {"/tmp", os.environ.get("TMPDIR") or "/tmp"}
    found = []
    for base in sorted(set(dirs)):
        try:
            names = os.listdir(base)
        except OSError:
            continue
        for name in names:
            m = SESSION_DIR.match(name)
            if not m or int(m.group(1), 36) != sid:
                continue
            path = os.path.join(base, name)
            try:
                st = os.lstat(path)
            except OSError:
                continue
            if stat.S_ISDIR(st.st_mode) and st.st_mtime >= since - 1:
                found.append(path)
    return found


def report_session_leftovers(found, label):
    print(f"\nTMPDIR LEAK: {label} left {len(found)} director{'y' if len(found) == 1 else 'ies'} "
          "outside its TMPDIR, removed now:", file=sys.stderr)
    for path in found:
        entry = describe(os.path.dirname(path), os.path.basename(path)).strip()
        print(f"  {os.path.dirname(path)}/{entry}", file=sys.stderr)
        remove_tree(path)
    print(SESSION_HOW_TO_FIX, file=sys.stderr)


def main(argv):
    if not argv:
        print("usage: tmp-leak-gate.py <command> [args...]", file=sys.stderr)
        return 2
    private = make_private_dir()
    env = dict(os.environ, TMPDIR=private, TMP_LEAK_GATE_DIR=private)
    proc = None

    # The command runs in a session of its own, so a directory a descendant makes in the machine's
    # /tmp names this run (session_leftovers), and a signal reaches its whole group.
    def forward(signum, _frame):
        if proc is not None and proc.poll() is None:
            try:
                os.killpg(proc.pid, signum)
            except OSError:
                proc.send_signal(signum)

    for sig in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
        signal.signal(sig, forward)
    try:
        start = time.time()
        reaper = become_subreaper()
        proc = subprocess.Popen(argv, env=env, start_new_session=True)
        rc = proc.wait()
        if rc < 0:  # killed by a signal: the shell's spelling of it
            rc = 128 - rc
        leaked = False
        survivors = reap_survivors(proc.pid) if reaper else []
        if survivors:
            print(f"\nTMPDIR LEAK: the command exited but left {len(survivors)} process(es) of its own "
                  f"running (pids {', '.join(map(str, survivors))}), killed now. A process that outlives "
                  "go test (a self-exec'd daemon, a helper re-exec, or a test binary orphaned when go "
                  "test was killed) keeps writing into TMPDIR, so what is left there is not a verdict "
                  "on the tests. Find the test that starts it and make it wait for the child's exit "
                  "before it returns.", file=sys.stderr)
            leaked = True
        found = session_leftovers(proc.pid, start)
        if found:
            report_session_leftovers(found, "the run")
            leaked = True
        leftovers = sorted(os.listdir(private))
        if leftovers:
            print(f"\nTMPDIR LEAK: the run left {len(leftovers)} entr{'y' if len(leftovers) == 1 else 'ies'} "
                  f"in its private TMPDIR ({private}):", file=sys.stderr)
            for name in leftovers:
                print(describe(private, name), file=sys.stderr)
            print(HOW_TO_FIX, file=sys.stderr)
            leaked = True
        return (rc or 1) if leaked else rc
    finally:
        removal_error = None
        try:
            remove_tree(private)
        except OSError as err:
            removal_error = err
        if removal_error is not None:
            # Never a traceback that hides the report above: say what stopped it, and fail.
            print(f"TMPDIR LEAK: the gate could not remove its private TMPDIR {private}: {removal_error}",
                  file=sys.stderr)
            sys.exit(1)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
