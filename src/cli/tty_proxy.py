"""Host-side TTY proxy that catches Ctrl-Z before it reaches the jail.

Without this, ``yolo -- claude`` wedges on Ctrl-Z: claude's TUI handler does
``process.kill(0, "SIGTSTP")`` to suspend itself, but PID 1 inside the
container is just ``bash -c "...; claude"`` (no job control), so there's no
shell to ``fg`` from.  Even with options like ``stty susp undef`` or
``SIGTSTP=SIG_IGN``, claude self-suspends explicitly — the kernel-level
TTY signal interception isn't what's putting it to sleep.

This module wraps the host-side ``podman run/exec`` with a small TTY
proxy:

  * Allocates a new pty, gives the slave end to ``podman`` as its
    stdio, keeps the master end.
  * Puts the host TTY in raw mode so bytes (including ``0x1A``) flow
    verbatim instead of being translated to SIGTSTP at the host TTY
    level.
  * Reads host stdin one chunk at a time.  When a chunk contains
    ``0x1A``, the proxy forwards everything before it, then
    *self-suspends* (raises SIGTSTP on its own pid after restoring the
    host TTY to cooked mode).
  * The host shell sees the stopped child, prints ``[1]+ Stopped``,
    and gives the user a prompt.  Inside the jail, claude has not
    received any signal — the byte never reached it.
  * On ``fg``, the host shell sends SIGCONT.  The proxy's signal
    handler re-grabs the TTY in raw mode and resumes byte forwarding.
    Any post-``0x1A`` bytes from the original chunk are flushed.
  * Window resize (SIGWINCH) is forwarded to the inside pty via
    ``TIOCSWINSZ`` so curses/TUI apps redraw correctly.

The proxy's exit code is the wrapped process's exit code.  If stdin
is not a TTY, ``run_with_proxy`` falls back to a plain
``subprocess.run`` — there's no ^Z byte to intercept and no host TTY
state to manage.
"""

from __future__ import annotations

import errno
import fcntl
import os
import pty
import select
import signal
import struct
import subprocess
import sys
import termios
import threading
import tty
from typing import Callable, List, Optional

# Ctrl-Z byte.  TTY VSUSP default; what claude reads off stdin and
# converts to ``process.kill(0, SIGTSTP)``.  Intercepting it before
# claude sees it is the whole point of this module.
SUSP_BYTE = 0x1A

# Read chunks small enough that we don't lump too many keystrokes
# together (which would make the "forward up to ^Z" splitting feel
# laggy on paste), but large enough to avoid syscall overhead on
# fast input from agent-driven sessions.
READ_CHUNK = 4096


def _set_winsize(fd: int, rows: int, cols: int) -> None:
    """Push (rows, cols) into the pty referenced by ``fd``."""
    try:
        fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))
    except OSError:
        pass


def _get_winsize(fd: int) -> Optional[tuple]:
    """Read (rows, cols) from a TTY fd, or None if not a TTY."""
    try:
        packed = fcntl.ioctl(fd, termios.TIOCGWINSZ, b"\0" * 8)
        rows, cols, _, _ = struct.unpack("HHHH", packed)
        return rows, cols
    except OSError:
        return None


def run_with_proxy(
    cmd: List[str],
    on_started: Optional[Callable[[subprocess.Popen], None]] = None,
) -> int:
    """Spawn ``cmd`` under a TTY proxy, returning its exit code.

    When stdin isn't a TTY we transparently fall back to a plain
    ``subprocess.run`` — there's no point setting up a pty just to
    relay a pipe, and ^Z isn't a meaningful byte off a non-TTY stdin
    anyway.

    If ``on_started`` is provided, it runs on a daemon thread after
    the child is spawned so the caller can do post-launch work (e.g.
    wait for the container to be visible, then release a lock)
    without blocking the proxy loop.
    """
    try:
        in_fd = sys.stdin.fileno()
        is_tty = os.isatty(in_fd)
    except (OSError, ValueError, AttributeError):
        # CliRunner / subprocess.PIPE / detached stdin: no fileno or not a TTY.
        is_tty = False

    if not is_tty:
        # No host TTY to manage; nothing useful for the proxy to do.
        # Spawn the child directly so test harnesses (and pipes in
        # production) see ``subprocess.Popen`` exactly like before.
        proc = subprocess.Popen(cmd)
        if on_started is not None:
            try:
                on_started(proc)
            except Exception:
                pass
        proc.wait()
        return proc.returncode

    # Capture host TTY attributes so we can restore them on suspend
    # and on exit.  ``tty.setraw`` would mutate them otherwise.
    cooked_attrs = termios.tcgetattr(in_fd)

    master, slave = pty.openpty()

    # Match the inside pty's window to the host TTY's at startup; SIGWINCH
    # forwards subsequent resizes.
    initial_size = _get_winsize(in_fd)
    if initial_size is not None:
        _set_winsize(slave, *initial_size)

    try:
        proc = subprocess.Popen(
            cmd,
            stdin=slave,
            stdout=slave,
            stderr=slave,
            close_fds=True,
        )
    except FileNotFoundError:
        os.close(master)
        os.close(slave)
        raise
    finally:
        # Parent only uses the master end.
        os.close(slave)

    if on_started is not None:
        threading.Thread(
            target=_safe_callback, args=(on_started, proc), daemon=True
        ).start()

    tty.setraw(in_fd)

    # When the host TTY resizes, propagate to the inside pty so
    # curses/TUI apps redraw at the new size.
    def _on_winch(_signum, _frame):
        size = _get_winsize(in_fd)
        if size is not None:
            _set_winsize(master, *size)

    # When the host shell sends SIGCONT (after `fg`), retake raw mode
    # — the host TTY was restored to cooked when we suspended.
    def _on_cont(_signum, _frame):
        try:
            tty.setraw(in_fd)
        except (OSError, termios.error):
            pass

    prev_winch = signal.signal(signal.SIGWINCH, _on_winch)
    prev_cont = signal.signal(signal.SIGCONT, _on_cont)

    try:
        return _proxy_loop(in_fd, master, proc, cooked_attrs)
    finally:
        signal.signal(signal.SIGWINCH, prev_winch)
        signal.signal(signal.SIGCONT, prev_cont)
        # Always restore the host TTY before returning so the user's
        # next prompt is in cooked mode.
        try:
            termios.tcsetattr(in_fd, termios.TCSANOW, cooked_attrs)
        except (OSError, termios.error):
            pass
        try:
            os.close(master)
        except OSError:
            pass


def _proxy_loop(
    in_fd: int,
    master: int,
    proc: subprocess.Popen,
    cooked_attrs: list,
) -> int:
    """Pump bytes between host TTY and the master pty until proc exits.

    Self-suspends on ``0x1A`` so the byte never reaches the wrapped
    process.  Bytes that arrived in the same read AFTER the ^Z are
    queued and flushed once we resume.
    """
    out_fd = sys.stdout.fileno()
    pending: bytes = b""

    while True:
        if proc.poll() is not None and not pending:
            # Drain any final bytes the child wrote before exiting.
            try:
                tail = os.read(master, READ_CHUNK)
            except OSError:
                tail = b""
            if tail:
                os.write(out_fd, tail)
                continue
            break

        try:
            r, _, _ = select.select([in_fd, master], [], [], 0.1)
        except (InterruptedError, OSError) as e:
            if isinstance(e, OSError) and e.errno != errno.EINTR:
                raise
            continue

        if master in r:
            try:
                data = os.read(master, READ_CHUNK)
            except OSError as e:
                if e.errno in (errno.EIO,):
                    # Master closed (child exited).
                    break
                raise
            if not data:
                break
            os.write(out_fd, data)

        if in_fd in r:
            try:
                data = os.read(in_fd, READ_CHUNK)
            except OSError as e:
                if e.errno == errno.EIO:
                    break
                raise
            if not data:
                # EOF on host stdin — close the master so the child
                # sees EOF too.
                try:
                    os.close(master)
                except OSError:
                    pass
                # Don't ``break`` yet; let the child's drain on the
                # next iteration finish.
                master = -1  # sentinel, select() will skip it
                continue

            if pending:
                data = pending + data
                pending = b""

            idx = data.find(SUSP_BYTE)
            if idx == -1:
                os.write(master, data)
                continue

            # Bytes before the ^Z get sent now.
            if idx > 0:
                os.write(master, data[:idx])
            # Bytes after the ^Z are queued for after resume.
            pending = data[idx + 1 :]
            _self_suspend(in_fd, cooked_attrs)
            # If anything was queued, flush it on the next loop iteration.
            if pending:
                # Send pending into master immediately rather than
                # round-tripping through select again.
                os.write(master, pending)
                pending = b""

    proc.wait()
    return proc.returncode


def _safe_callback(
    cb: Callable[[subprocess.Popen], None], proc: subprocess.Popen
) -> None:
    """Run ``cb(proc)`` on a daemon thread, swallowing exceptions.

    The callback is post-launch best-effort housekeeping (release a
    lock, write a tracking file, etc.); a bug in it must not crash
    the proxy loop.
    """
    try:
        cb(proc)
    except Exception:
        # Daemon thread; nothing to report into.  The user-visible
        # symptom of a callback bug is the housekeeping step not
        # happening, which the caller will notice on their own.
        pass


def _self_suspend(in_fd: int, cooked_attrs: list) -> None:
    """Restore host TTY to cooked mode, raise SIGTSTP on self.

    The default SIGTSTP disposition stops the process; the kernel
    notifies the parent shell, which prints ``[1]+ Stopped`` and
    redraws its prompt.  ``fg`` later sends SIGCONT, which our
    handler responds to by retaking raw mode (so the proxy_loop
    resumes byte-faithful forwarding when ``signal.signal`` returns).
    """
    try:
        termios.tcsetattr(in_fd, termios.TCSANOW, cooked_attrs)
    except (OSError, termios.error):
        pass
    # Use SIGTSTP rather than os.kill so the default handler
    # (suspend the process) runs.  We installed a SIGCONT handler
    # earlier; that re-raws the TTY when the shell resumes us.
    os.kill(os.getpid(), signal.SIGTSTP)
    # Control returns here after SIGCONT.  The SIGCONT handler has
    # already put the TTY back in raw mode.
