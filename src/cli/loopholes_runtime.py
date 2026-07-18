"""Loophole / host-service runtime — the piece of the CLI that actually
launches the host-side daemons listed by ``yolo loopholes``.

What lives here:
  * LoopholeDaemon dataclass — the handle returned by start_loopholes
    and consumed by stop_loopholes / run()'s container command assembly.
  * _host_service_env_var, _host_service_default_jail_socket,
    _host_service_sockets_dir, _resolve_journal_mode,
    _substitute_socket_in_cmd, _wake_and_close_listener — small helpers
    used by all daemons.
  * Claude OAuth broker singleton — BROKER_SINGLETON_*, _broker_*
    family.  One host-wide daemon, not per-jail; see the long comment
    block near BROKER_SINGLETON_SOCKET for the history.
  * _should_mount_host_nix, _gpu_host_available,
    _rocm_host_available — host-state probes used by run()'s
    mount-decision logic.  _gpu_host_available checks the NVIDIA
    drivers + CDI; _rocm_host_available is the AMD/ROCm twin
    (amdgpu module + /dev/kfd + render nodes).  Kept here so the
    runtime plumbing all lives together.
  * cgroup delegate — _cgroup_delegate_handler,
    _cgd_ensure_agent_cgroup, _cgd_create_and_join, _cgd_destroy,
    _start_host_service_builtin_cgroup, _validate_cgroup_name,
    _parse_memory_value.  Implements the JSON socket protocol that
    backs ``yolo-cglimit``.
  * Journal bridge — JOURNAL_FRAME_*, _journal_send_frame,
    _journal_handle_client, _start_host_service_builtin_journal.
    Implements the framed binary-safe protocol behind
    ``yolo-journalctl``.
  * External daemons — _start_host_service_external.
  * Per-jail broker relay supervision — _relay_ensure / _relay_stop /
    _relay_reap_orphans, which spawn and reap src/broker_relay.py; see
    the comment block above _relay_ensure for why it's a standalone
    process.
  * start_loopholes / stop_loopholes — the lifecycle entry points
    called by run().
"""

import dataclasses
import hashlib
import json
import os
import re
import shutil
import signal
import socket
import struct
import subprocess
import sys
import threading
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Callable, Dict, List, Optional

from src import loopholes as _loopholes

from .console import console
from .paths import (
    BUILTIN_CGROUP_LOOPHOLE_NAME,
    BUILTIN_JOURNAL_LOOPHOLE_NAME,
    CGD_SOCKET_NAME,
    GLOBAL_STORAGE,
    IS_LINUX,
    IS_MACOS,
    JAIL_HOST_SERVICES_DIR,
    JOURNAL_SOCKET_NAME,
)
from .runtime import _resolve_container_cgroup, find_running_container


@dataclass
class LoopholeDaemon:
    """A host-side service exposing a Unix socket inside the jail.

    Created by `start_loopholes` and torn down by `stop_loopholes`.
    Holds everything `run()` needs to wire the service into the container run command:
    bind mount, env var, optional client shim path.

    The `_stop` callable encapsulates the service's shutdown logic (SIGTERM for
    external, shutdown event for builtin) so both kinds look the same to the
    lifecycle manager.
    """

    name: str
    # Absolute path to the Unix socket on the host (inside the per-jail sockets dir).
    host_socket_path: Path
    # Absolute path where the socket appears inside the jail.
    jail_socket_path: str
    # Env var injected into the container so the agent can discover the socket.
    # Always set to YOLO_SERVICE_<sanitized-name>_SOCKET.
    env_var_name: str
    # Stop callable.  Called with no args at container exit.  Must not raise.
    _stop: "Callable[[], None]" = dataclasses.field(
        default_factory=lambda: lambda: None
    )
    # Optional path of a generated client-shim script, relative to the jail
    # filesystem.  If set, the shim is written into the per-workspace overlay
    # and appears on PATH inside the jail.
    client_shim_jail_path: Optional[str] = None


def _host_service_env_var(service_name: str) -> str:
    """Return the canonical env var name for a service's socket path."""
    sanitized = re.sub(r"[^A-Za-z0-9]+", "_", service_name).strip("_").upper()
    return f"YOLO_SERVICE_{sanitized}_SOCKET"


def _host_service_default_jail_socket(name: str) -> str:
    """Default path where a service's socket appears inside the jail."""
    return f"{JAIL_HOST_SERVICES_DIR}/{name}.sock"


def _resolve_journal_mode(config: Dict[str, Any]) -> str:
    """Return the journal bridge mode from config.

    Accepts the canonical strings ("off", "user", "full").  `true` is
    treated as "user" (safer default for unprivileged agents), `false`
    and missing as "off".  Anything else is "off" — validation catches
    the invalid value separately and reports it to the user.
    """
    val = config.get("journal")
    if val is True:
        return "user"
    if val is False or val is None:
        return "off"
    if isinstance(val, str) and val in ("off", "user", "full"):
        return val
    return "off"


def _host_service_sockets_dir(cname: str) -> Path:
    """Per-jail directory holding all host-service sockets on the host side.

    Bind-mounted into the jail at JAIL_HOST_SERVICES_DIR.

    Lives under /tmp (not ws_state!) because Linux's AF_UNIX path limit is
    108 bytes and macOS's is 104 — workspace paths on CI runners or in
    nested directories can easily blow that when we append
    "<service-name>.sock" on top.  /tmp is always 4 bytes, leaving plenty
    of room.

    The directory name uses an 8-char hash of the container name to avoid
    collisions while keeping the path short:

        /tmp/yolo-host-services-<8hex>/cgroup-delegate.sock   (~53 bytes)

    macOS resolves /tmp → /private/tmp; we use the resolved form so paths
    we hand to Python's socket module match what the kernel sees.
    """
    short_hash = hashlib.sha1(cname.encode()).hexdigest()[:8]
    base = Path("/tmp").resolve() if IS_MACOS else Path("/tmp")
    return base / f"yolo-host-services-{short_hash}"


def _substitute_socket_in_cmd(args: List[str], socket_path: str) -> List[str]:
    """Replace '{socket}' in each arg with the actual socket path."""
    return [a.replace("{socket}", socket_path) for a in args]


def _wake_and_close_listener(srv: socket.socket, sock_path: Path) -> None:
    """Wake a serve loop blocked in ``srv.accept()`` and close ``srv``.

    Used by the builtin daemons' ``_stop`` closures after setting their
    shutdown event, so stop latency is milliseconds instead of an accept
    tick.  The wake is a dummy connect: portable for AF_UNIX on both
    Linux and macOS, unlike the alternatives —

    * ``close()`` alone does not reliably wake a thread blocked in
      ``accept()``/``poll()`` on Linux (the waiter keeps sleeping on the
      old fd until its timeout);
    * ``shutdown()`` on a listening socket wakes the accept on Linux but
      fails with ENOTCONN on macOS/BSD.

    The serve loops check their shutdown event right after ``accept()``
    and drop the dummy connection without doing handler work.  Closing
    ``srv`` afterwards makes any subsequent ``accept()`` raise OSError,
    which the loops treat as exit."""
    try:
        wake = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        try:
            wake.settimeout(1.0)
            wake.connect(str(sock_path))
        finally:
            wake.close()
    except OSError:
        # Socket already gone/unconnectable — the serve loop will exit
        # via its fallback accept tick or the close below.
        pass
    try:
        srv.close()
    except OSError:
        pass


# ---------------------------------------------------------------------------
# Claude OAuth broker singleton
# ---------------------------------------------------------------------------
#
# The broker used to be a per-jail host daemon: each ``yolo run`` forked its
# own ``yolo-claude-oauth-broker-host`` process bound to a socket under the
# jail's unique sockets dir.  That had three problems:
#
# 1. N brokers contending for a single flock on a single shared creds file,
#    doing exactly the coordination the flock is meant to enable — but
#    serially rather than concurrently, at higher overhead.
# 2. Daemon processes outlive the container that spawned them — they're
#    host processes, not container processes.  A ``yolo run --new`` rebuilds
#    the container but leaves the original broker running with the OLD
#    Python code still loaded in memory, even after ``just deploy``
#    upgrades the wheel.  (2026-04-24 incident.)
# 3. Per-jail ==> N sockets, N log files, N lines in ``pgrep``, and the
#    operator can't tell at a glance whether "the broker" is healthy.
#
# The singleton model: one host daemon, one well-known socket, one PID file.
# Jails never touch the singleton socket directly — each reaches it through
# its per-jail relay (see ``_relay_ensure``) at the existing
# ``/run/yolo-services/claude-oauth-broker.sock`` path.  The socket is
# under /tmp so AF_UNIX path-length limits aren't a concern (108 bytes on
# Linux, 104 on macOS) and so a host reboot leaves a clean slate.  No
# systemd unit needed — the first ``yolo run`` lazily spawns; ``just
# deploy`` / ``yolo broker restart`` explicitly cycle it.
BROKER_SINGLETON_SOCKET = Path("/tmp/yolo-claude-oauth-broker.sock")
BROKER_SINGLETON_PID_FILE = Path("/tmp/yolo-claude-oauth-broker.pid")
# Separate lock file so flock during spawn doesn't contend with the PID
# file itself (which must be rewritten atomically by the spawner).
BROKER_SINGLETON_LOCK = Path("/tmp/yolo-claude-oauth-broker.lock")
# Name used by ``loopholes`` for this service; keep in sync with the
# bundled manifest's ``name`` field.
BROKER_LOOPHOLE_NAME = "claude-oauth-broker"


# go-port seam #2 (daemon resolution). During the soak, a host daemon is
# swapped to its Go port by listing its name in YOLO_GO_DAEMONS and pointing
# YOLO_GO_BIN_DIR at the dist-go dir. The Go binary carries the SAME name as
# the Python console script, so resolution is by explicit DIR (never PATH —
# PATH shadowing would defeat per-daemon gating). A listed-but-missing binary
# falls back to the console script (availability beats the A/B preference;
# dist-go/ is gitignored + wiped by ``just clean``). Introduced here for the
# OAuth broker (Stage 6); Stage 5 reuses it at _start_host_service_external.
def _go_daemon_enabled(name: str) -> bool:
    """True iff ``name`` is listed (comma-separated) in YOLO_GO_DAEMONS."""
    listed = os.environ.get("YOLO_GO_DAEMONS", "")
    return name in {n.strip() for n in listed.split(",") if n.strip()}


def _daemon_launcher(console_name: str) -> "List[str]":
    """The argv[0:1] launcher for a host daemon: the Go binary at
    $YOLO_GO_BIN_DIR/<console_name> when the daemon is gated on via
    YOLO_GO_DAEMONS and that binary exists+executable, else the Python
    console-script name resolved on PATH (the default)."""
    if _go_daemon_enabled(console_name):
        bin_dir = os.environ.get("YOLO_GO_BIN_DIR")
        if bin_dir:
            candidate = Path(bin_dir) / console_name
            if os.access(candidate, os.X_OK):
                return [str(candidate)]
            console.print(
                f"[yellow]{console_name}: YOLO_GO_DAEMONS lists it but "
                f"{candidate} is missing/not executable — using the Python "
                f"daemon. Run `just build-go`.[/yellow]"
            )
    return [console_name]


# --- Timing knobs ----------------------------------------------------------
# Condition-polling pattern: TIGHT poll interval, GENEROUS deadline — waits
# return as soon as the condition holds and only burn the full deadline on
# genuine failure.  Tests may shrink these; production defaults must stay
# behavior-identical to the historical hardcoded values.
#
# Deadline for a just-spawned broker/relay to bind its socket.
BROKER_SPAWN_TIMEOUT = 5.0
# Poll interval for socket-appearance and PID-exit waits.  Must be much
# smaller than any deadline that uses it (currently 5.0s spawn / 3.0s kill /
# 1.0s SIGKILL-reap graces).
SOCKET_POLL_INTERVAL = 0.05
# After SIGKILL, how long to keep reaping before giving up on the zombie.
RELAY_SIGKILL_REAP_GRACE = 1.0
# External host-service stop: SIGTERM grace before SIGKILL, then reap grace.
EXTERNAL_SERVICE_TERM_GRACE = 5.0
EXTERNAL_SERVICE_KILL_GRACE = 2.0


def _broker_ping(socket_path: Path, *, timeout: float = 2.0) -> bool:
    """Open ``socket_path``, send ``{action: "ping"}``, expect a
    ``pong: true`` frame within ``timeout`` seconds.  Returns False on
    any error so callers can use this as a boolean liveness probe.

    Implements the small bit of the loophole frame protocol we need
    inline (4-byte length-prefixed JSON request; 1-byte stream id +
    4-byte length frames coming back; the broker emits a single JSON
    line on stream 0 and an exit frame on stream 2).  This keeps the
    probe free of a dependency on the heavier
    ``src.oauth_broker_jail.ask_host_broker`` path.
    """
    import socket as _socket
    import struct as _struct

    try:
        s = _socket.socket(_socket.AF_UNIX, _socket.SOCK_STREAM)
        s.settimeout(timeout)
        s.connect(str(socket_path))
    except OSError:
        return False
    try:
        body = b'{"action":"ping"}'
        s.sendall(_struct.pack(">I", len(body)) + body)
        # Expect at least one data frame (pong) before the exit frame.
        while True:
            hdr = s.recv(5)
            if len(hdr) < 5:
                return False
            sid, ln = _struct.unpack(">BI", hdr)
            payload = b""
            while len(payload) < ln:
                chunk = s.recv(ln - len(payload))
                if not chunk:
                    return False
                payload += chunk
            if sid == 0:  # STREAM_STDOUT
                try:
                    obj = json.loads(payload.decode())
                except (UnicodeDecodeError, json.JSONDecodeError):
                    return False
                return bool(obj.get("pong"))
            if sid == 2:  # STREAM_EXIT without a pong on stream 0 → not alive
                return False
    except OSError:
        return False
    finally:
        try:
            s.close()
        except OSError:
            pass


def _broker_read_pid() -> "Optional[int]":
    """Return the integer PID from the singleton PID file, or None if
    the file is absent / unreadable / malformed."""
    try:
        raw = BROKER_SINGLETON_PID_FILE.read_text().strip()
    except OSError:
        return None
    try:
        return int(raw)
    except ValueError:
        return None


def _broker_pid_is_live(pid: int) -> bool:
    """``os.kill(pid, 0)`` — the standard Unix liveness check.  Returns
    True if the process exists and we can signal it, False on
    ``ProcessLookupError`` or ``OSError`` other than permission.
    EPERM (we can't signal someone else's process) still counts as
    alive for our purposes."""
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    except OSError:
        return False
    return True


def _broker_is_alive() -> bool:
    """Singleton liveness: PID file exists, PID is live, socket is
    present, and a ping round-trips.  All four must hold — otherwise
    we treat the slot as vacant so the next ``_broker_ensure`` respawns."""
    pid = _broker_read_pid()
    if pid is None or not _broker_pid_is_live(pid):
        return False
    if not BROKER_SINGLETON_SOCKET.exists():
        return False
    return _broker_ping(BROKER_SINGLETON_SOCKET)


def _broker_wait_for_socket(
    sock: Path,
    *,
    timeout: float = BROKER_SPAWN_TIMEOUT,
    proc: "Optional[subprocess.Popen]" = None,
) -> bool:
    """Poll until ``sock`` appears or ``timeout`` elapses.

    When ``proc`` (the just-spawned daemon) is given, a dead child is a
    genuine failure detected in milliseconds — return False immediately
    instead of burning the whole deadline waiting for a socket that a
    corpse will never bind."""
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if sock.exists():
            return True
        if proc is not None and proc.poll() is not None:
            return sock.exists()
        time.sleep(SOCKET_POLL_INTERVAL)
    return sock.exists()


def _broker_spawn() -> Path:
    """Fork the singleton broker if it's not already running.  Returns
    the socket path regardless of whether we had to spawn.

    ``fcntl.flock`` on the lock file serializes concurrent spawners so
    two parallel ``yolo run`` invocations don't race into forking two
    brokers.  Inside the lock we re-check liveness; the loser of the
    race finds a live broker on second look and returns without
    spawning."""
    import fcntl as _fcntl

    BROKER_SINGLETON_LOCK.parent.mkdir(parents=True, exist_ok=True)
    with open(BROKER_SINGLETON_LOCK, "w") as lock_f:
        _fcntl.flock(lock_f, _fcntl.LOCK_EX)
        if _broker_is_alive():
            return BROKER_SINGLETON_SOCKET

        # Clean any stale socket left behind by a crashed prior broker;
        # a second bind(2) on a stale path fails with EADDRINUSE.
        try:
            BROKER_SINGLETON_SOCKET.unlink()
        except FileNotFoundError:
            pass

        log_dir = GLOBAL_STORAGE / "logs"
        log_dir.mkdir(parents=True, exist_ok=True)
        log_path = log_dir / "host-service-claude-oauth-broker.log"
        log_file = open(log_path, "ab")
        # go-port seam #2: launcher is the Python console script by default, or
        # the Go binary from $YOLO_GO_BIN_DIR when gated on via YOLO_GO_DAEMONS.
        # Same binary name either way, so _broker_pgrep_strays matches both.
        launcher = _daemon_launcher("yolo-claude-oauth-broker-host")
        proc = subprocess.Popen(
            [*launcher, "--socket", str(BROKER_SINGLETON_SOCKET)],
            stdout=log_file,
            stderr=log_file,
            start_new_session=True,
            close_fds=True,
        )
        BROKER_SINGLETON_PID_FILE.write_text(f"{proc.pid}\n")
        if not _broker_wait_for_socket(
            BROKER_SINGLETON_SOCKET, timeout=BROKER_SPAWN_TIMEOUT, proc=proc
        ):
            # Bind failed — process likely crashed.  Leave the PID
            # file for ``yolo broker status`` to report.
            return BROKER_SINGLETON_SOCKET
        return BROKER_SINGLETON_SOCKET


def _broker_ensure() -> Path:
    """If the singleton is alive, return its socket.  Otherwise spawn
    and return.  One-shot entrypoint for code paths that need a live
    broker (``yolo run`` via ``start_loopholes``, ``yolo broker``
    subcommands)."""
    if _broker_is_alive():
        return BROKER_SINGLETON_SOCKET
    return _broker_spawn()


def _should_mount_host_nix(
    runtime: str,
    *,
    nix_socket_exists: bool,
    nix_store_exists: bool,
    is_macos: bool,
    opt_in_env: Optional[str],
) -> bool:
    """Decide whether ``run()`` should bind-mount the host's Nix daemon + store.

    Linux: mount whenever both paths exist and runtime supports it.
    macOS: skip by default — the container runtime VM (Podman Machine,
    Apple Container) does not share /nix, and bind-mounting statfs-errors
    at startup.  A setup that *does* share /nix into its runtime VM can opt
    back in by setting ``YOLO_NIX_HOST_DAEMON`` to a truthy value
    (``1``/``true``/``yes``).
    Apple Container can't share Unix sockets via -v bind mounts regardless,
    so the runtime gate handles that case.
    """
    if not (nix_socket_exists and nix_store_exists):
        return False
    if runtime == "container":
        return False
    if not is_macos:
        return True
    opt_in = (opt_in_env or "").lower() in ("1", "true", "yes")
    return opt_in


def _gpu_host_available(runtime: str) -> tuple[bool, Optional[str]]:
    """Probe whether NVIDIA GPU passthrough will actually work on this host.

    Returns ``(True, None)`` if the host has the drivers + toolkit
    podman needs, or ``(False, reason)`` explaining what's missing.
    ``reason`` is a single short phrase suitable for a one-line
    warning (e.g. ``"nvidia-smi not found on host"``).

    Used by :func:`run` so a workspace config with ``gpu.enabled: true``
    stays portable across a GPU box and a GPU-less laptop — the
    GPU-less machine sees a warning and starts without the CDI device
    flags instead of a hard podman error.

    GPU passthrough requires podman + CDI.  Other runtimes (macOS,
    Apple Container) return a skip reason; callers already warn/skip
    for those earlier.
    """
    if IS_MACOS or runtime == "container":
        return False, "runtime does not support NVIDIA passthrough"
    if runtime != "podman":
        return False, f"unsupported runtime: {runtime}"

    nvidia_smi = shutil.which("nvidia-smi")
    if not nvidia_smi:
        return False, "nvidia-smi not found on host"
    try:
        probe = subprocess.run(
            [nvidia_smi, "-L"],
            capture_output=True,
            timeout=5,
        )
    except (OSError, subprocess.SubprocessError) as e:
        return False, f"nvidia-smi failed to run ({e})"
    if probe.returncode != 0:
        return False, "nvidia-smi reported no GPUs"

    cdi_paths = (Path("/etc/cdi/nvidia.yaml"), Path("/var/run/cdi/nvidia.yaml"))
    if not any(p.exists() for p in cdi_paths):
        return False, "no CDI spec at /etc/cdi/nvidia.yaml"
    return True, None


def _rocm_host_available(runtime: str) -> tuple[bool, Optional[str]]:
    """Probe whether AMD ROCm GPU passthrough will actually work on this host.

    Returns ``(True, None)`` when the host exposes the AMD kernel
    device nodes ROCm needs, or ``(False, reason)`` with a one-line
    warning phrase.  ROCm passthrough is podman + Linux only; other
    runtimes return a skip reason (callers warn/skip earlier).

    Default (device-node) mode needs no host toolkit — just the
    amdgpu kernel driver and the /dev/kfd + /dev/dri render nodes.
    """
    if IS_MACOS or runtime == "container":
        return False, "runtime does not support ROCm/AMD passthrough"
    if runtime != "podman":
        return False, f"unsupported runtime: {runtime}"

    # amdgpu kernel module loaded? (cheap, no subprocess)
    if not Path("/sys/module/amdgpu").exists():
        return False, "amdgpu kernel module not loaded"

    # Mandatory compute interface, shared by all GPUs.
    if not Path("/dev/kfd").exists():
        return False, "no /dev/kfd on host"

    # At least one DRI render node.
    if not any(Path("/dev/dri").glob("renderD*")):
        return False, "no /dev/dri render node on host"

    # Functional enumeration via rocminfo, when present, catches the
    # blacklisted/unsupported-GPU false-negative.  rocminfo's banner
    # and agent list are the AMD analog of `nvidia-smi -L`.  rocminfo
    # ignores argv, so no flags.  Absence of rocminfo is NOT fatal:
    # the device nodes above are the real precondition and ROCm
    # userspace lives in the image, not on the host.
    rocminfo = shutil.which("rocminfo")
    if rocminfo:
        try:
            probe = subprocess.run([rocminfo], capture_output=True, timeout=5)
        except (OSError, subprocess.SubprocessError) as e:
            return False, f"rocminfo failed to run ({e})"
        if probe.returncode != 0:
            return False, "rocminfo reported no GPUs"

    return True, None


# ---------------------------------------------------------------------------
# Per-jail broker relay
# ---------------------------------------------------------------------------
#
# Jails never touch BROKER_SINGLETON_SOCKET directly.  A socket-FILE bind
# mount pins the inode: ``yolo broker restart`` unlinks and re-binds the
# host path, and every already-running jail keeps piping into the dead
# socket — Connection refused / 502 until relaunch (2026-07-03 incident).
# macOS Podman Machine can't bind-mount a socket file at all (EOPNOTSUPP).
# Instead each jail gets a relay process (src/broker_relay.py) listening
# inside its host-services sockets dir — visible in-jail through the
# existing directory mount, no extra -v flag — that dials the singleton
# per connection and stamps jail_id on each request for attribution.
#
# The relay is a supervised standalone process, NOT a thread inside the
# ``yolo run`` host process: conmon keeps a container alive independently
# of any yolo invocation (terminal close, SIGKILL, exec/attach from other
# processes), so an in-process thread dies out from under a live jail.
# Supervision mirrors the broker singleton machinery above, keyed per
# jail.  The PID and lock files live OUTSIDE the sockets dir (which
# ``stop_loopholes`` rmtree's and attach recreates) and are keyed by the
# same 8-char hash as the dir name, so ``stop_loopholes`` can find them
# without being handed cname.


def _relay_short_hash(cname: str) -> str:
    """The 8-char hash that keys the jail's sockets dir — keep in sync
    with ``_host_service_sockets_dir``."""
    return hashlib.sha1(cname.encode()).hexdigest()[:8]


def _relay_pid_file(short_hash: str) -> Path:
    return Path(f"/tmp/yolo-broker-relay-{short_hash}.pid")


def _relay_lock_file(short_hash: str) -> Path:
    return Path(f"/tmp/yolo-broker-relay-{short_hash}.lock")


def _relay_read_pid(pid_file: Path) -> "Optional[int]":
    try:
        raw = pid_file.read_text().strip()
    except OSError:
        return None
    try:
        return int(raw)
    except ValueError:
        return None


def _relay_socket_connectable(socket_path: Path, *, timeout: float = 2.0) -> bool:
    """connect() probe only — no protocol ping.  The relay is a byte
    proxy: it is alive even while the broker behind it is down (that
    layer is graded separately via ``_broker_status`` / doctor)."""
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    try:
        s.settimeout(timeout)
        s.connect(str(socket_path))
        return True
    except OSError:
        return False
    finally:
        try:
            s.close()
        except OSError:
            pass


def _relay_is_alive(pid_file: Path, socket_path: Path) -> bool:
    """Relay liveness: PID live, socket connectable.
    ``_broker_is_alive`` minus the ping — see ``_relay_socket_connectable``.

    A MISSING pidfile does not condemn the relay: write-once /tmp files
    can be aged away (e.g. systemd-tmpfiles) under a long-lived jail
    while the relay itself is healthy.  Declaring it dead here would
    unlink its LIVE socket, spawn a duplicate on the same path, and
    leak the original.  A connectable socket is proof enough of life;
    ``_relay_kill``'s pgrep fallback keeps such a relay reapable."""
    pid = _relay_read_pid(pid_file)
    if pid is None:
        return _relay_socket_connectable(socket_path)
    if not _broker_pid_is_live(pid):
        return False
    if not socket_path.exists():
        return False
    return _relay_socket_connectable(socket_path)


def _reap_if_child(pid: int) -> bool:
    """Reap ``pid`` iff it is a zombie child of THIS process, returning
    True when it was (or has just been) waited on.

    The relay is spawned by ``_relay_ensure`` as a direct, never-waited
    child of the ``yolo run`` process.  Once SIGTERM'd it exits within
    milliseconds but sits in the process table as a zombie — and
    ``os.kill(pid, 0)`` counts a zombie as alive, so without reaping,
    ``_relay_kill``'s liveness poll would spin its full timeout and then
    SIGKILL the corpse on every graceful jail exit.  A relay that is NOT
    our child (spawned by an earlier, now-dead yolo process) raises
    ChildProcessError here and is reaped by init instead.
    """
    try:
        waited, _status = os.waitpid(pid, os.WNOHANG)
    except ChildProcessError:
        return False  # not our child — its parent (or init) reaps it
    except OSError:
        return False
    return waited == pid


def _relay_pid_cmdline_matches(pid: int) -> bool:
    """True iff the process at ``pid`` really is a broker relay — its
    cmdline names ``broker_relay.py`` (the same argv matching idea as
    ``_broker_pgrep_strays``).

    This is ``_relay_kill``'s identity guard: unlike ``_broker_kill``
    (explicit ``yolo broker stop`` only), ``_relay_kill`` fires
    automatically from ``_relay_ensure`` on every run/attach, so a stale
    PID file from a crashed/orphaned relay must never translate into a
    SIGTERM at whatever same-user process the kernel recycled that PID
    onto.  Unknown identity reads as False — never signal a process we
    can't positively identify.  Linux: /proc cmdline; macOS (no /proc):
    ``ps`` fallback.
    """
    # go-port seam #2: match BOTH the Python relay (broker_relay.py) and the
    # Go relay (yolo-broker-relay) so a mixed-era yolo can reap either.
    try:
        cmdline = Path(f"/proc/{pid}/cmdline").read_bytes()
    except OSError:
        try:
            probe = subprocess.run(
                ["ps", "-p", str(pid), "-o", "args="],
                capture_output=True,
                text=True,
                timeout=2,
            )
        except (OSError, subprocess.SubprocessError):
            return False
        return probe.returncode == 0 and (
            "broker_relay.py" in probe.stdout or "yolo-broker-relay" in probe.stdout
        )
    return b"broker_relay.py" in cmdline or b"yolo-broker-relay" in cmdline


def _relay_pgrep(socket_path: Path) -> List[int]:
    """PIDs of relay processes serving ``socket_path``, found by argv —
    the ``_broker_pgrep_strays`` twin for relays whose pidfile vanished
    (e.g. systemd-tmpfiles aged it out of /tmp).  ``_relay_is_alive``
    deliberately keeps such a relay ALIVE on the strength of its
    connectable socket; this keeps it REAPABLE at jail exit."""
    # go-port seam #2: pgrep pattern matches BOTH relay impls. Both argvs share
    # the identical ``--socket <path>`` tail (see _relay_spawn_argv), so an
    # alternation on the launcher token finds either. pgrep -f uses an extended
    # regex; the socket path is interpolated raw exactly as before (paths here
    # are controlled — no new escaping, which would be a behavior change).
    try:
        result = subprocess.run(
            [
                "pgrep",
                "-f",
                f"(broker_relay.py|yolo-broker-relay) --socket {socket_path}",
            ],
            capture_output=True,
            text=True,
            timeout=2,
        )
    except Exception:
        return []
    if result.returncode != 0:
        return []
    pids: List[int] = []
    for line in result.stdout.splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            pid = int(line)
        except ValueError:
            continue
        if pid == os.getpid():
            continue
        pids.append(pid)
    return pids


def _relay_kill(
    pid_file: Path,
    *,
    socket_path: "Optional[Path]" = None,
    timeout: float = 3.0,
) -> None:
    """SIGTERM the relay named by ``pid_file`` (SIGKILL stragglers) and
    remove the PID file.  No-op when the file is absent, the PID is
    already dead, or the PID's cmdline no longer names the relay script
    (stale PID file + recycled PID — see ``_relay_pid_cmdline_matches``).

    When the pidfile is missing but ``socket_path`` is given, falls
    back to argv-based discovery (``_relay_pgrep``) so a relay whose
    pidfile was aged out of /tmp is still reaped rather than leaked.
    """
    pids: List[int] = []
    pid = _relay_read_pid(pid_file)
    if pid is not None:
        pids.append(pid)
    elif socket_path is not None:
        pids.extend(_relay_pgrep(socket_path))
    for pid in pids:
        if (
            _reap_if_child(pid)  # zombie child == already dead
            or not _broker_pid_is_live(pid)
            or not _relay_pid_cmdline_matches(pid)
        ):
            continue
        try:
            os.kill(pid, signal.SIGTERM)
        except OSError:
            pass
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if _reap_if_child(pid) or not _broker_pid_is_live(pid):
                break
            time.sleep(SOCKET_POLL_INTERVAL)
        if _broker_pid_is_live(pid):
            try:
                os.kill(pid, signal.SIGKILL)
            except OSError:
                pass
            # Don't leave the SIGKILL'd child as a zombie either.
            kill_deadline = time.monotonic() + RELAY_SIGKILL_REAP_GRACE
            while time.monotonic() < kill_deadline:
                if _reap_if_child(pid) or not _broker_pid_is_live(pid):
                    break
                time.sleep(SOCKET_POLL_INTERVAL)
    try:
        pid_file.unlink()
    except FileNotFoundError:
        pass


# go-port seam #2: the relay binary is Python by default, or the Go port when
# YOLO_BROKER_RELAY_BIN points at it (dist-go/<goos>-<goarch>/yolo-broker-relay
# during the soak). Both take the identical --socket/--broker/--jail argv, so
# _relay_pgrep's pattern and the identity guard below match either — but the
# guards MUST recognize both cmdlines or a mixed-era invocation can't reap the
# other impl's relay (the orphan-relay incident class). Kept in the SAME commit
# as this env branch, per the plan's non-negotiable rider.
def _relay_binary_marker() -> str:
    """The substring that identifies a broker-relay process in its cmdline,
    covering BOTH implementations. Python launches ``… broker_relay.py …``;
    the Go binary's argv[0] basename is ``yolo-broker-relay``."""
    # Not used directly (the matchers check both tokens); kept as documentation
    # of the identity contract.
    return "broker_relay.py|yolo-broker-relay"


def _relay_spawn_argv(
    socket_path: Path, broker_socket: Path, cname: str
) -> "List[str]":
    """Build the relay spawn argv, selecting the implementation.

    Default: the Python relay, launched by ABSOLUTE FILE PATH (not
    ``-m src.broker_relay``): ``python -m`` prepends the child's cwd to
    sys.path, and yolo runs from arbitrary workspaces — one with its own
    ``src`` package would shadow ours and the relay would never bind.
    broker_relay.py is stdlib-only precisely so a by-path launch needs no
    importable package context.

    When ``YOLO_BROKER_RELAY_BIN`` is set (go-port Stage 3 soak), spawn that
    binary instead. The trailing --socket/--broker/--jail argv is IDENTICAL so
    _relay_pgrep's ``--socket <path>`` match still finds either impl.
    """
    tail = [
        "--socket",
        str(socket_path),
        "--broker",
        str(broker_socket),
        "--jail",
        cname,
    ]
    go_bin = os.environ.get("YOLO_BROKER_RELAY_BIN")
    if go_bin:
        # Seam-hardening (go-port): the soak flag points into dist-go/, which is
        # gitignored and wiped by ``just clean`` — so it can vanish out from
        # under a still-exported flag (fresh clone, cleaned tree, un-rebuilt
        # binary). A missing/non-executable path would spawn a broken relay and
        # 502 every jail. Warn once and FALL BACK to the Python relay instead,
        # which always exists in the source tree — availability beats the
        # A/B preference. Rebuild with ``just build-go`` to re-engage the Go relay.
        if os.access(go_bin, os.X_OK):
            return [go_bin, *tail]
        console.print(
            f"[yellow]YOLO_BROKER_RELAY_BIN={go_bin} is missing or not "
            f"executable — falling back to the Python relay. Run "
            f"`just build-go` to rebuild dist-go/.[/yellow]"
        )
    relay_script = Path(__file__).resolve().parent.parent / "broker_relay.py"
    return [sys.executable, str(relay_script), *tail]


def _relay_ensure(
    cname: str,
    sockets_dir: Path,
    broker_socket: "Optional[Path]" = None,
) -> None:
    """Idempotent per-jail relay supervision — the relay twin of
    ``_broker_ensure``.  Alive → return; dead/absent → spawn under a
    flock (two concurrent ``yolo`` invocations must not fork two
    relays).  Runs on every code path that targets the jail — fresh run
    AND exec/attach — because the container outlives any single yolo
    process; whoever touches the jail next heals a relay whose spawning
    process died."""
    import fcntl as _fcntl

    if broker_socket is None:
        broker_socket = BROKER_SINGLETON_SOCKET
    short_hash = _relay_short_hash(cname)
    pid_file = _relay_pid_file(short_hash)
    socket_path = sockets_dir / f"{BROKER_LOOPHOLE_NAME}.sock"
    if _relay_is_alive(pid_file, socket_path):
        return
    with open(_relay_lock_file(short_hash), "w") as lock_f:
        _fcntl.flock(lock_f, _fcntl.LOCK_EX)
        if _relay_is_alive(pid_file, socket_path):
            return

        # A live PID whose socket died (e.g. the sockets dir was removed
        # under it) would be orphaned forever if we just overwrote the
        # PID file — reap it before spawning.  socket_path covers the
        # inverse wreckage: a pidfile-less relay wedged on a dead socket.
        _relay_kill(pid_file, socket_path=socket_path)

        # stop_loopholes rmtree's the dir at jail exit; attach paths
        # arrive here with it missing.
        sockets_dir.mkdir(parents=True, exist_ok=True)
        # Stale socket from a crashed relay → EADDRINUSE on bind.
        socket_path.unlink(missing_ok=True)

        log_dir = GLOBAL_STORAGE / "logs"
        log_dir.mkdir(parents=True, exist_ok=True)
        log_path = log_dir / f"broker-relay-{short_hash}.log"
        argv = _relay_spawn_argv(socket_path, broker_socket, cname)
        with open(log_path, "ab") as log_file:
            proc = subprocess.Popen(
                argv,
                stdout=log_file,
                stderr=log_file,
                start_new_session=True,
                close_fds=True,
            )
        pid_file.write_text(f"{proc.pid}\n")
        if not _broker_wait_for_socket(
            socket_path, timeout=BROKER_SPAWN_TIMEOUT, proc=proc
        ):
            # Leave the PID file for diagnosis; doctor's per-jail probe
            # reports the dead relay.
            console.print(
                f"[yellow]claude-oauth-broker: relay for {cname} did not "
                f"bind {socket_path} within {BROKER_SPAWN_TIMEOUT:.0f}s — "
                f"see {log_path}[/yellow]"
            )


def _relay_stop(cname: str) -> None:
    """Stop the jail's relay and remove its PID file; no-op if absent.
    ``stop_loopholes`` reaches the same relay by deriving the hash from
    the sockets-dir name instead (it isn't handed cname)."""
    _relay_kill(_relay_pid_file(_relay_short_hash(cname)))


def _relay_reap_orphans(
    live_cnames: "Optional[set[str]]",
    *,
    apply: bool = True,
    older_than_seconds: float = 3600.0,
    base: "Optional[Path]" = None,
) -> List[Path]:
    """Reap relay processes whose jail is no longer running.

    ``stop_loopholes`` only reaps a relay in the graceful tail of the
    original ``yolo run`` process.  When that process dies first
    (terminal close → SIGHUP; conmon keeps the container alive; the
    user keeps working via attach sessions and eventually exits from
    one), nothing kills the relay: it survives — with its PID file,
    lock file, and sockets dir — until host reboot.  A stale PID file
    left this way is also the precondition for the recycled-PID hazard
    ``_relay_kill`` guards against.  This sweep is the backstop: kill
    every relay whose PID-file hash matches no live yolo container.
    It runs from ``yolo run`` (piggybacking on the store-prune gate's
    container enumeration) and from ``yolo prune``.

    ``live_cnames`` is the set of live container names; ``None`` means
    liveness could not be enumerated and the sweep declines (unknown
    must never read as "nothing live" — same polarity as the
    store-prune gate).  ``older_than_seconds`` is a grace floor keyed
    off the PID file's mtime so a relay spawned for a jail mid-startup
    (ensured before its container is visible) is never reaped.
    Returns the PID files reaped (or, with ``apply=False``, the ones
    that would be).
    """
    if live_cnames is None:
        return []
    if base is None:
        base = Path("/tmp")
    live_hashes = {_relay_short_hash(c) for c in live_cnames}
    reaped: List[Path] = []
    now = time.time()
    for pid_file in sorted(base.glob("yolo-broker-relay-*.pid")):
        short_hash = pid_file.name.removeprefix("yolo-broker-relay-").removesuffix(
            ".pid"
        )
        if short_hash in live_hashes:
            continue
        try:
            if now - pid_file.stat().st_mtime < older_than_seconds:
                continue
        except OSError:
            continue  # unlinked under us — someone else reaped it
        reaped.append(pid_file)
        if not apply:
            continue
        _relay_kill(pid_file)
        (base / f"yolo-broker-relay-{short_hash}.lock").unlink(missing_ok=True)
        # The orphaned jail's sockets dir was never rmtree'd by
        # stop_loopholes either — sweep it with the relay.
        shutil.rmtree(base / f"yolo-host-services-{short_hash}", ignore_errors=True)
    return reaped


def _broker_pgrep_strays() -> List[int]:
    """Return PIDs of any running ``yolo-claude-oauth-broker-host``
    processes the OS knows about, regardless of our PID file state.

    Why this exists: when an older yolo-jail wheel ran the broker
    under a different PID-file path (or before we had a PID file at
    all), upgrades left the old daemon running.  ``_broker_kill``
    saw an empty PID file and silently no-op'd, so ``yolo broker
    restart`` couldn't actually cycle the daemon — old code kept
    serving until the next host reboot.  pgrep is the belt-and-
    suspenders cleanup: any process whose argv contains the broker
    binary name gets caught.
    """
    try:
        result = subprocess.run(
            ["pgrep", "-f", "yolo-claude-oauth-broker-host"],
            capture_output=True,
            text=True,
            timeout=2,
        )
    except (FileNotFoundError, subprocess.TimeoutExpired, OSError):
        return []
    if result.returncode != 0:
        return []
    pids: List[int] = []
    for line in result.stdout.splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            pid = int(line)
        except ValueError:
            continue
        # Don't kill ourselves if pgrep happens to match the
        # currently-running ``yolo`` invocation.  (It shouldn't —
        # different argv — but be defensive.)
        if pid == os.getpid():
            continue
        pids.append(pid)
    return pids


def _broker_kill(*, sig: int = signal.SIGTERM, timeout: float = 3.0) -> bool:
    """Send ``sig`` to the singleton broker.  Returns True if a
    broker was running and has been signaled (whether or not it's
    already exited); False if nothing was running.  Always cleans up
    the PID file and socket on success.

    When the PID file is missing or stale, falls back to ``pgrep``-
    based discovery so wheel-upgrade orphans (whose original PID file
    layout differed) still get reaped.  See ``_broker_pgrep_strays``."""
    pids: List[int] = []
    primary = _broker_read_pid()
    if primary is not None:
        pids.append(primary)
    else:
        pids.extend(_broker_pgrep_strays())

    if not pids:
        # Nothing to kill — still remove a stale socket if present so
        # the next spawn gets a clean slate.
        try:
            BROKER_SINGLETON_SOCKET.unlink()
        except FileNotFoundError:
            pass
        return False

    for pid in pids:
        try:
            os.kill(pid, sig)
        except ProcessLookupError:
            # Already dead — treat as a clean stop for this PID.
            continue
        except OSError:
            # Permission error or similar — skip this PID, keep going
            # so a partial cleanup beats none.
            continue

    # Wait for every signaled PID to actually exit before declaring
    # success.  Escalate to SIGKILL on stragglers.
    deadline = time.monotonic() + timeout
    survivors = list(pids)
    while survivors and time.monotonic() < deadline:
        survivors = [p for p in survivors if _broker_pid_is_live(p)]
        if survivors:
            time.sleep(SOCKET_POLL_INTERVAL)
    for pid in survivors:
        try:
            os.kill(pid, signal.SIGKILL)
        except ProcessLookupError:
            pass

    # Cleanup.
    for p in (BROKER_SINGLETON_PID_FILE, BROKER_SINGLETON_SOCKET):
        try:
            p.unlink()
        except FileNotFoundError:
            pass
    return True


def _broker_status() -> Dict[str, Any]:
    """Snapshot for ``yolo broker status``: pid (or None), alive bool,
    socket exists bool, ping ok bool, socket path (for display)."""
    pid = _broker_read_pid()
    pid_live = pid is not None and _broker_pid_is_live(pid)
    sock_exists = BROKER_SINGLETON_SOCKET.exists()
    ping_ok = sock_exists and _broker_ping(BROKER_SINGLETON_SOCKET)
    return {
        "pid": pid,
        "pid_live": pid_live,
        "socket_exists": sock_exists,
        "ping_ok": ping_ok,
        "socket": str(BROKER_SINGLETON_SOCKET),
        "pid_file": str(BROKER_SINGLETON_PID_FILE),
    }


def _validate_cgroup_name(name: str) -> bool:
    """Validate that a cgroup name is safe (no path traversal)."""
    return (
        bool(re.match(r"^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$", name)) and ".." not in name
    )


def _parse_memory_value(val: str) -> Optional[int]:
    """Parse a human-readable memory value to bytes.  Returns None on invalid input."""
    val = val.strip().lower()
    try:
        if val.endswith("g"):
            return int(float(val[:-1]) * 1073741824)
        if val.endswith("m"):
            return int(float(val[:-1]) * 1048576)
        if val.endswith("k"):
            return int(float(val[:-1]) * 1024)
        return int(val)
    except (ValueError, OverflowError):
        return None


def _cgroup_delegate_handler(
    conn: socket.socket,
    container_cgroup: Path,
    log_file,
):
    """Handle a single cgroup delegate request from the container.

    Protocol: single-line JSON request, single-line JSON response.
    """
    try:
        data = b""
        while b"\n" not in data and len(data) < 4096:
            chunk = conn.recv(4096)
            if not chunk:
                break
            data += chunk
        if not data:
            return

        request = json.loads(data.decode("utf-8", errors="replace"))
        op = request.get("op", "")

        # Get the host-PID of the caller — only the PID is used; UID/GID
        # returned alongside it are ignored.
        # Linux: SO_PEERCRED (returns pid/uid/gid as three ints)
        # macOS: LOCAL_PEERPID (returns just the pid)
        try:
            if IS_LINUX:
                cred = conn.getsockopt(
                    socket.SOL_SOCKET,
                    getattr(socket, "SO_PEERCRED"),
                    struct.calcsize("3i"),
                )
                peer_pid = struct.unpack("3i", cred)[0]
            elif IS_MACOS:
                # macOS: LOCAL_PEERPID (0x002) returns the peer PID
                LOCAL_PEERPID = 0x002
                cred = conn.getsockopt(0, LOCAL_PEERPID, struct.calcsize("i"))
                peer_pid = struct.unpack("i", cred)[0]
            else:
                peer_pid = 0
        except (OSError, struct.error, AttributeError):
            peer_pid = 0

        # Log every request for auditability
        log_line = f"op={op} peer_pid={peer_pid} request={json.dumps(request)}"
        print(log_line, file=log_file, flush=True)

        if op == "status":
            # Check if delegation is available
            agent_cg = container_cgroup / "agent"
            controllers = ""
            if agent_cg.exists():
                try:
                    controllers = (agent_cg / "cgroup.controllers").read_text().strip()
                except OSError:
                    pass
            response = {
                "ok": True,
                "delegated": agent_cg.exists(),
                "controllers": controllers,
                "cgroup": str(container_cgroup),
            }

        elif op == "create_and_join":
            name = request.get("name", "")
            if not _validate_cgroup_name(name):
                response = {"ok": False, "error": f"Invalid cgroup name: {name!r}"}
            elif peer_pid <= 0:
                response = {"ok": False, "error": "Could not determine caller PID"}
            else:
                response = _cgd_create_and_join(
                    container_cgroup, name, request, peer_pid, log_file
                )

        elif op == "destroy":
            name = request.get("name", "")
            if not _validate_cgroup_name(name):
                response = {"ok": False, "error": f"Invalid cgroup name: {name!r}"}
            else:
                response = _cgd_destroy(container_cgroup, name, log_file)

        else:
            response = {"ok": False, "error": f"Unknown operation: {op!r}"}

        conn.sendall((json.dumps(response) + "\n").encode())
        print(f"  response={json.dumps(response)}", file=log_file, flush=True)

    except Exception as exc:
        try:
            conn.sendall((json.dumps({"ok": False, "error": str(exc)}) + "\n").encode())
        except Exception:
            pass
    finally:
        conn.close()


def _cgd_ensure_agent_cgroup(container_cgroup: Path, log_file) -> Optional[Path]:
    """Ensure the agent cgroup subtree exists with controllers enabled.

    Returns the path to the agent cgroup, or None on failure.
    """
    agent_cg = container_cgroup / "agent"
    init_cg = container_cgroup / "init"

    if agent_cg.exists():
        return agent_cg

    try:
        init_cg.mkdir(exist_ok=True)
        agent_cg.mkdir(exist_ok=True)
    except OSError as e:
        print(f"  ERROR creating cgroup dirs: {e}", file=log_file, flush=True)
        return None

    # Move all existing processes to 'init' (cgroup v2 no-internal-process constraint)
    try:
        procs = (container_cgroup / "cgroup.procs").read_text().strip().split()
        for pid in procs:
            try:
                (init_cg / "cgroup.procs").write_text(pid)
            except OSError:
                pass  # Process may have exited or be a kthread
    except OSError:
        pass

    # Enable controllers on container root → agent subtree
    for cg in [container_cgroup, agent_cg]:
        try:
            available = (cg / "cgroup.controllers").read_text().strip().split()
            wanted = [c for c in ["cpu", "memory", "pids"] if c in available]
            if wanted:
                ctrl = " ".join(f"+{c}" for c in wanted)
                (cg / "cgroup.subtree_control").write_text(ctrl)
        except OSError:
            pass

    return agent_cg


def _cgd_create_and_join(
    container_cgroup: Path,
    name: str,
    request: dict,
    peer_pid: int,
    log_file,
) -> dict:
    """Create a child cgroup under agent/, set limits, and move the caller into it."""
    agent_cg = _cgd_ensure_agent_cgroup(container_cgroup, log_file)
    if agent_cg is None:
        return {"ok": False, "error": "Failed to set up agent cgroup hierarchy"}

    job_cg = agent_cg / name
    try:
        job_cg.mkdir(exist_ok=True)
    except OSError as e:
        return {"ok": False, "error": f"Cannot create cgroup {name}: {e}"}

    errors = []

    # CPU limit: percentage of all CPUs → cpu.max (quota period)
    cpu_pct = request.get("cpu_pct")
    if cpu_pct is not None:
        try:
            pct = int(cpu_pct)
            nproc = os.cpu_count() or 1
            if pct < 1 or pct > 100 * nproc:
                errors.append(f"cpu_pct out of range: {pct}")
            else:
                quota = pct * 1000 * nproc
                (job_cg / "cpu.max").write_text(f"{quota} 100000")
        except (ValueError, OSError) as e:
            errors.append(f"cpu.max: {e}")

    # Memory limit
    memory = request.get("memory")
    if memory is not None:
        mem_bytes = _parse_memory_value(str(memory))
        if mem_bytes is None or mem_bytes < 1048576:  # min 1MB
            errors.append(f"Invalid memory value: {memory}")
        else:
            try:
                (job_cg / "memory.max").write_text(str(mem_bytes))
            except OSError as e:
                errors.append(f"memory.max: {e}")

    # PID limit
    pids = request.get("pids")
    if pids is not None:
        try:
            pids_val = int(pids)
            if pids_val < 1 or pids_val > 1000000:
                errors.append(f"pids out of range: {pids_val}")
            else:
                (job_cg / "pids.max").write_text(str(pids_val))
        except (ValueError, OSError) as e:
            errors.append(f"pids.max: {e}")

    # Move the caller into the new cgroup (peer_pid is already host-namespace)
    try:
        (job_cg / "cgroup.procs").write_text(str(peer_pid))
    except OSError as e:
        return {
            "ok": False,
            "error": f"Cannot move PID {peer_pid} into cgroup: {e}",
            "limit_errors": errors,
        }

    cg_root = Path("/sys/fs/cgroup")
    try:
        cg_path = str(job_cg.relative_to(cg_root))
    except ValueError:
        cg_path = str(job_cg)
    result = {"ok": True, "cgroup": cg_path}
    if errors:
        result["warnings"] = errors
    return result


def _cgd_destroy(container_cgroup: Path, name: str, _log_file) -> dict:
    """Remove a child cgroup (must be empty of processes).

    `_log_file` is accepted to match the signature of sibling handlers
    (`_cgd_create`, `_cgd_status`) that all share a dispatch table; this
    handler doesn't need to log anything extra beyond the top-level request
    log line.
    """
    agent_cg = container_cgroup / "agent"
    job_cg = agent_cg / name
    if not job_cg.exists():
        return {"ok": True}  # Already gone — idempotent
    try:
        # Check for remaining processes
        procs = (job_cg / "cgroup.procs").read_text().strip()
        if procs:
            return {
                "ok": False,
                "error": f"Cgroup {name} still has processes: {procs}",
            }
        job_cg.rmdir()
        return {"ok": True}
    except OSError as e:
        return {"ok": False, "error": f"Cannot remove cgroup {name}: {e}"}


def _start_host_service_builtin_cgroup(
    cname: str, runtime: str, sockets_dir: Path
) -> Optional[LoopholeDaemon]:
    """Start the built-in cgroup delegate daemon as a host service.

    Listens on <sockets_dir>/cgroup-delegate.sock (the name the jail's
    entrypoint probes for).  Returns a LoopholeDaemon handle, or
    None if cgroup v2 is not available (macOS or Linux without cgroup v2).

    This is functionally identical to the pre-refactor `start_cgroup_delegate`
    — same thread, same handler, same JSON protocol.  The only difference is
    that the socket now lives in the unified per-jail host-services directory
    instead of /tmp/yolo-cgd-<cname>.
    """
    if IS_MACOS:
        # macOS has no cgroup v2 — skip the delegation daemon entirely.
        return None

    # Quick sanity: is cgroup v2 available on the host?
    if not Path("/sys/fs/cgroup/cgroup.controllers").exists():
        return None

    sockets_dir.mkdir(parents=True, exist_ok=True)
    sock_path = sockets_dir / CGD_SOCKET_NAME
    sock_path.unlink(missing_ok=True)

    log_dir = GLOBAL_STORAGE / "logs"
    log_dir.mkdir(parents=True, exist_ok=True)
    log_file = open(log_dir / f"{cname}-cgd.log", "a")

    srv = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    srv.bind(str(sock_path))
    sock_path.chmod(0o777)  # Container runs as mapped UID — must be accessible
    srv.listen(8)
    # Fallback tick only: normal shutdown is woken instantly by
    # _wake_and_close_listener; this bounds the exit latency if that
    # wake ever fails (e.g. socket file removed under us).
    srv.settimeout(1.0)

    container_cgroup: Optional[Path] = None
    container_cgroup_lock = threading.Lock()
    shutdown = threading.Event()

    def serve():
        nonlocal container_cgroup
        while not shutdown.is_set():
            try:
                conn, _ = srv.accept()
            except socket.timeout:
                continue
            except OSError:
                break
            if shutdown.is_set():
                # Woken by _stop's dummy connection — not a real client.
                try:
                    conn.close()
                except OSError:
                    pass
                break

            # Lazy-resolve container cgroup on first request
            with container_cgroup_lock:
                if container_cgroup is None:
                    container_cgroup = _resolve_container_cgroup(cname, runtime)
                    if container_cgroup:
                        print(
                            f"Resolved container cgroup: {container_cgroup}",
                            file=log_file,
                            flush=True,
                        )
                    else:
                        print(
                            "WARNING: Could not resolve container cgroup",
                            file=log_file,
                            flush=True,
                        )
            if container_cgroup is None:
                try:
                    conn.sendall(
                        (
                            json.dumps(
                                {
                                    "ok": False,
                                    "error": "Container cgroup not yet available",
                                }
                            )
                            + "\n"
                        ).encode()
                    )
                    conn.close()
                except Exception:
                    pass
                continue

            _cgroup_delegate_handler(conn, container_cgroup, log_file)
        # Double-close guards: _stop also closes srv (both closes are
        # idempotent in CPython, the try/except is belt-and-braces).
        try:
            srv.close()
        except OSError:
            pass
        try:
            log_file.close()
        except OSError:
            pass

    t = threading.Thread(
        target=serve, daemon=True, name=f"host-service-{BUILTIN_CGROUP_LOOPHOLE_NAME}"
    )
    # bind()+listen() completed synchronously above, so the socket is
    # already connectable — no settling sleep needed.
    t.start()

    def _stop():
        shutdown.set()
        # Wake the accept() immediately (dummy connect + close) so stop
        # doesn't wait out the fallback accept tick; join is a generous
        # deadline that only bites on genuine failure.
        _wake_and_close_listener(srv, sock_path)
        t.join(timeout=3)

    return LoopholeDaemon(
        name=BUILTIN_CGROUP_LOOPHOLE_NAME,
        host_socket_path=sock_path,
        jail_socket_path=_host_service_default_jail_socket(
            BUILTIN_CGROUP_LOOPHOLE_NAME
        ),
        env_var_name=_host_service_env_var(BUILTIN_CGROUP_LOOPHOLE_NAME),
        _stop=_stop,
    )


# --- Journal bridge -------------------------------------------------------
#
# Wire protocol (framed, binary-safe — `journalctl -o export` is not
# line-delimited and `-f` follows indefinitely, so a plain newline-delimited
# stream wouldn't work):
#
#   Client → server:  single JSON line  {"args": ["-u", "foo", "-n", "50"]}\n
#   Server → client:  zero or more frames, each:
#                       [stream:1 byte][length:4 bytes BE][payload:length bytes]
#                     where stream ∈ {1=stdout, 2=stderr, 3=exit}.
#                     An "exit" frame has length=4 and payload=int32 BE.
#                     After the exit frame, the server closes the socket.
#
# The client script (~/.local/bin/yolo-journalctl, generated by
# entrypoint.py) decodes these frames back onto its own stdout/stderr and
# exits with the received code.
JOURNAL_FRAME_STDOUT = 1
JOURNAL_FRAME_STDERR = 2
JOURNAL_FRAME_EXIT = 3
JOURNAL_MAX_ARGS = 64
JOURNAL_MAX_ARG_LEN = 1024


def _journal_send_frame(conn: socket.socket, stream: int, payload: bytes) -> None:
    header = struct.pack(">BI", stream, len(payload))
    conn.sendall(header + payload)


def _journal_handle_client(conn: socket.socket, mode: str, log_file) -> None:
    """Serve one yolo-journalctl request end-to-end.

    `mode` is "user" (force --user) or "full" (pass args through).
    """
    try:
        # Read a single JSON request line.  Cap the header to avoid a
        # runaway client hanging the daemon thread.
        data = b""
        while b"\n" not in data and len(data) < 16384:
            chunk = conn.recv(4096)
            if not chunk:
                break
            data += chunk
        if b"\n" not in data:
            _journal_send_frame(
                conn, JOURNAL_FRAME_STDERR, b"yolo-journal: malformed request\n"
            )
            _journal_send_frame(conn, JOURNAL_FRAME_EXIT, struct.pack(">i", 2))
            return
        header, _ = data.split(b"\n", 1)
        try:
            request = json.loads(header.decode("utf-8", errors="replace"))
        except json.JSONDecodeError as e:
            msg = f"yolo-journal: invalid JSON: {e}\n".encode()
            _journal_send_frame(conn, JOURNAL_FRAME_STDERR, msg)
            _journal_send_frame(conn, JOURNAL_FRAME_EXIT, struct.pack(">i", 2))
            return

        args = request.get("args") or []
        if not isinstance(args, list) or len(args) > JOURNAL_MAX_ARGS:
            _journal_send_frame(
                conn,
                JOURNAL_FRAME_STDERR,
                f"yolo-journal: args must be a list of ≤{JOURNAL_MAX_ARGS} strings\n".encode(),
            )
            _journal_send_frame(conn, JOURNAL_FRAME_EXIT, struct.pack(">i", 2))
            return
        clean_args: List[str] = []
        for a in args:
            if not isinstance(a, str) or len(a) > JOURNAL_MAX_ARG_LEN:
                _journal_send_frame(
                    conn,
                    JOURNAL_FRAME_STDERR,
                    b"yolo-journal: each arg must be a string under 1024 bytes\n",
                )
                _journal_send_frame(conn, JOURNAL_FRAME_EXIT, struct.pack(">i", 2))
                return
            clean_args.append(a)

        # "user" mode: always force --user.  The user could technically add
        # their own --user already; a duplicate flag is harmless to
        # journalctl.  We do NOT strip conflicting flags (--system, -M) —
        # journalctl itself rejects those combinations and prints a clear
        # error, which we forward to the client.
        if mode == "user":
            clean_args = ["--user", *clean_args]

        print(
            f"[journal] mode={mode} args={json.dumps(clean_args)}",
            file=log_file,
            flush=True,
        )

        # Spawn journalctl.  start_new_session so SIGTERM reaches the
        # process and any children if the client disconnects.
        try:
            proc = subprocess.Popen(
                ["journalctl", *clean_args],
                stdin=subprocess.DEVNULL,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                start_new_session=True,
            )
        except FileNotFoundError:
            _journal_send_frame(
                conn,
                JOURNAL_FRAME_STDERR,
                b"yolo-journal: journalctl not found on host\n",
            )
            _journal_send_frame(conn, JOURNAL_FRAME_EXIT, struct.pack(">i", 127))
            return
        except OSError as e:
            _journal_send_frame(
                conn,
                JOURNAL_FRAME_STDERR,
                f"yolo-journal: spawn failed: {e}\n".encode(),
            )
            _journal_send_frame(conn, JOURNAL_FRAME_EXIT, struct.pack(">i", 1))
            return

        send_lock = threading.Lock()

        def pump(stream_fd, frame_type: int):
            try:
                while True:
                    buf = stream_fd.read(4096)
                    if not buf:
                        return
                    with send_lock:
                        try:
                            _journal_send_frame(conn, frame_type, buf)
                        except OSError:
                            # Client went away — kill journalctl and bail.
                            try:
                                proc.terminate()
                            except Exception:
                                pass
                            return
            except Exception:
                return

        t_out = threading.Thread(
            target=pump, args=(proc.stdout, JOURNAL_FRAME_STDOUT), daemon=True
        )
        t_err = threading.Thread(
            target=pump, args=(proc.stderr, JOURNAL_FRAME_STDERR), daemon=True
        )
        t_out.start()
        t_err.start()

        rc = proc.wait()
        t_out.join(timeout=2)
        t_err.join(timeout=2)
        with send_lock:
            try:
                _journal_send_frame(conn, JOURNAL_FRAME_EXIT, struct.pack(">i", rc))
            except OSError:
                pass
    finally:
        try:
            conn.close()
        except Exception:
            pass


def _start_host_service_builtin_journal(
    cname: str, sockets_dir: Path, mode: str
) -> Optional[LoopholeDaemon]:
    """Start the built-in journal bridge as a host service.

    `mode` is "user" or "full".  Returns None if journalctl isn't on the
    host's PATH (macOS or a minimal Linux without systemd).
    """
    if shutil.which("journalctl") is None:
        console.print(
            "[yellow]journal bridge requested but journalctl not found on host — "
            "skipping[/yellow]"
        )
        return None

    sockets_dir.mkdir(parents=True, exist_ok=True)
    sock_path = sockets_dir / JOURNAL_SOCKET_NAME
    sock_path.unlink(missing_ok=True)

    log_dir = GLOBAL_STORAGE / "logs"
    log_dir.mkdir(parents=True, exist_ok=True)
    log_file = open(log_dir / f"{cname}-journal.log", "a")

    srv = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    srv.bind(str(sock_path))
    sock_path.chmod(0o777)
    srv.listen(8)
    # Fallback tick only — see the cgroup twin for rationale.
    srv.settimeout(1.0)

    shutdown = threading.Event()

    def serve():
        while not shutdown.is_set():
            try:
                conn, _ = srv.accept()
            except socket.timeout:
                continue
            except OSError:
                break
            if shutdown.is_set():
                # Woken by _stop's dummy connection — not a real client.
                try:
                    conn.close()
                except OSError:
                    pass
                break
            handler = threading.Thread(
                target=_journal_handle_client,
                args=(conn, mode, log_file),
                daemon=True,
                name="journal-client",
            )
            handler.start()
        # Double-close guards — _stop also closes srv.
        try:
            srv.close()
        except OSError:
            pass
        try:
            log_file.close()
        except OSError:
            pass

    t = threading.Thread(
        target=serve, daemon=True, name=f"host-service-{BUILTIN_JOURNAL_LOOPHOLE_NAME}"
    )
    # bind()+listen() completed synchronously above — no settling sleep.
    t.start()

    def _stop():
        shutdown.set()
        _wake_and_close_listener(srv, sock_path)
        t.join(timeout=3)

    return LoopholeDaemon(
        name=BUILTIN_JOURNAL_LOOPHOLE_NAME,
        host_socket_path=sock_path,
        jail_socket_path=_host_service_default_jail_socket(
            BUILTIN_JOURNAL_LOOPHOLE_NAME
        ),
        env_var_name=_host_service_env_var(BUILTIN_JOURNAL_LOOPHOLE_NAME),
        _stop=_stop,
    )


def _start_host_service_external(
    name: str,
    spec: Dict[str, Any],
    sockets_dir: Path,
    startup_timeout_secs: float = 5.0,
) -> Optional[LoopholeDaemon]:
    """Launch a user-configured external host service.

    The service's command is expected to bind a Unix socket at the path
    substituted for `{socket}` in its args.  Returns a LoopholeDaemon handle if
    the service bound the socket within `startup_timeout_secs`, or None on
    failure (command not found, socket not bound, process exited early).
    """
    sockets_dir.mkdir(parents=True, exist_ok=True)
    host_socket = sockets_dir / f"{name}.sock"
    host_socket.unlink(missing_ok=True)

    cmd_template = spec.get("command") or []
    if not isinstance(cmd_template, list) or not cmd_template:
        console.print(f"[red]Host service '{name}' has no command; skipping[/red]")
        return None

    cmd = _substitute_socket_in_cmd(
        [
            str(Path(str(a)).expanduser()) if a.startswith("~") else str(a)
            for a in cmd_template
        ],
        str(host_socket),
    )

    # go-port seam #2: if cmd[0] is a console-script daemon gated on via
    # YOLO_GO_DAEMONS, swap it for the Go binary at $YOLO_GO_BIN_DIR (resolved
    # by explicit dir; missing -> falls back to the console script). Only the
    # launcher token is replaced; the substituted --socket/... tail is kept.
    if cmd:
        launcher = _daemon_launcher(cmd[0])
        if launcher != [cmd[0]]:
            cmd = [*launcher, *cmd[1:]]

    env = {**os.environ}
    for k, v in (spec.get("env") or {}).items():
        if not isinstance(k, str) or not isinstance(v, str):
            continue
        env[k] = str(Path(v).expanduser()) if v.startswith("~") else v

    log_dir = GLOBAL_STORAGE / "logs"
    log_dir.mkdir(parents=True, exist_ok=True)
    log_path = log_dir / f"host-service-{name}.log"
    log_file = open(log_path, "ab")

    def _print_log_tail(reason: str, max_lines: int = 5) -> None:
        """Surface the last few log lines so operators don't have to fish.

        The service's own stderr/stdout already captured the actionable
        error (e.g. a Python traceback ending in FileNotFoundError); we
        echo the tail to the console alongside the failure message.
        """
        try:
            log_file.flush()
        except Exception:
            pass
        try:
            with open(log_path, "rb") as f:
                tail = f.read()[-4096:].decode(errors="replace").rstrip()
        except OSError:
            return
        if not tail:
            return
        lines = tail.splitlines()[-max_lines:]
        console.print(
            f"[yellow]Last {len(lines)} line(s) of {log_path} ({reason}):[/yellow]"
        )
        for line in lines:
            console.print(f"  [dim]{line}[/dim]")

    try:
        proc = subprocess.Popen(
            cmd,
            env=env,
            stdout=log_file,
            stderr=log_file,
            start_new_session=True,  # own process group so SIGTERM reaches kids
        )
    except (OSError, FileNotFoundError) as e:
        console.print(f"[red]Failed to launch host service '{name}': {e}[/red]")
        log_file.close()
        return None

    # Wait for the service to bind the socket (or exit early with an error).
    deadline = time.monotonic() + startup_timeout_secs
    while time.monotonic() < deadline:
        if host_socket.exists():
            break
        if proc.poll() is not None:
            console.print(
                f"[red]Host service '{name}' exited early with code "
                f"{proc.returncode} before binding {host_socket}[/red]"
            )
            _print_log_tail(f"exit code {proc.returncode}")
            log_file.close()
            return None
        time.sleep(SOCKET_POLL_INTERVAL)
    else:
        console.print(
            f"[red]Host service '{name}' did not bind {host_socket} within "
            f"{startup_timeout_secs:.1f}s — killing[/red]"
        )
        _print_log_tail("startup timeout")
        try:
            proc.kill()
            proc.wait(timeout=EXTERNAL_SERVICE_KILL_GRACE)
        except Exception:
            pass
        log_file.close()
        return None

    def _stop():
        # SIGTERM, give it EXTERNAL_SERVICE_TERM_GRACE seconds, SIGKILL
        # if it's still around.
        if proc.poll() is None:
            try:
                proc.terminate()
            except Exception:
                pass
            try:
                proc.wait(timeout=EXTERNAL_SERVICE_TERM_GRACE)
            except subprocess.TimeoutExpired:
                try:
                    proc.kill()
                    proc.wait(timeout=EXTERNAL_SERVICE_KILL_GRACE)
                except Exception:
                    pass
        try:
            log_file.close()
        except Exception:
            pass

    jail_socket = spec.get("jail_socket") or _host_service_default_jail_socket(name)
    return LoopholeDaemon(
        name=name,
        host_socket_path=host_socket,
        jail_socket_path=jail_socket,
        env_var_name=_host_service_env_var(name),
        _stop=_stop,
    )


def start_loopholes(
    cname: str,
    runtime: str,
    config: Dict[str, Any],
) -> List[LoopholeDaemon]:
    """Start all host services for this jail and return handles.

    Always attempts the built-in cgroup delegate (skipped gracefully on
    macOS and non-cgroup-v2 Linux).  Then launches any services declared in
    ``config["loopholes"]`` as external processes.

    The caller is responsible for passing the returned handles to
    ``stop_loopholes`` at container exit, along with the same socket
    directory (recoverable via ``_host_service_sockets_dir(cname)``).
    """
    sockets_dir = _host_service_sockets_dir(cname)
    sockets_dir.mkdir(parents=True, exist_ok=True)

    handles: List[LoopholeDaemon] = []

    # Apple Container doesn't support Unix socket bind mounts at all (it
    # can't share the sockets dir into the jail), so we skip host services
    # entirely there.  The sockets dir still gets created above so any
    # subsequent per-file bind mounts don't fail.
    if runtime == "container":
        return handles

    # 1. Built-in cgroup delegate (Linux only, cgroup v2 only).
    builtin = _start_host_service_builtin_cgroup(cname, runtime, sockets_dir)
    if builtin is not None:
        handles.append(builtin)

    # 2. Built-in journal bridge (opt in via top-level `journal` key).
    journal_mode = _resolve_journal_mode(config)
    if journal_mode != "off":
        journal = _start_host_service_builtin_journal(cname, sockets_dir, journal_mode)
        if journal is not None:
            handles.append(journal)

    # 3. External services.  Discovery unifies three sources:
    #      a) Bundled loopholes (ship in the wheel).
    #      b) User-installed loopholes (~/.local/share/yolo-jail/loopholes/).
    #      c) Inline ``loopholes:`` entries in yolo-jail.jsonc for daemons
    #         that don't need a file-backed manifest.
    #    Workspace config can also override (a) and (b) via name-matching
    #    entries — see ``_apply_workspace_overrides`` in src/loopholes.py.
    #    Inactive loopholes (disabled, or ``requires`` not met) are skipped.
    loopholes_config = config.get("loopholes")
    discovered = _loopholes.discover_loopholes(loopholes_config=loopholes_config)
    manifest_specs = _loopholes.manifest_host_daemon_specs(discovered)
    external_specs: Dict[str, Any] = dict(manifest_specs)
    # Config-inline loopholes (no matching file-backed entry) still end up
    # as unix-socket daemons — the synthesizer captured them, but
    # _start_host_service_external wants the original config dict shape,
    # so pull those straight from config for their command fields.
    if isinstance(loopholes_config, dict):
        for name, spec in loopholes_config.items():
            if name in external_specs:
                continue  # already covered by a file-backed manifest's host_daemon
            if isinstance(spec, dict) and "command" in spec:
                external_specs[name] = spec
    for name, spec in external_specs.items():
        if name in (BUILTIN_CGROUP_LOOPHOLE_NAME, BUILTIN_JOURNAL_LOOPHOLE_NAME):
            continue  # reserved builtins
        if not isinstance(spec, dict):
            continue
        # Claude OAuth broker is a HOST-WIDE singleton, not per-jail.
        # See the block near ``BROKER_SINGLETON_SOCKET`` for why.  We
        # ensure a live broker here; the per-jail relay that exposes it
        # inside the jail at
        # ``/run/yolo-services/claude-oauth-broker.sock`` is ensured in
        # ``run()``'s container command assembly (``_ensure_broker_relay``
        # → ``_relay_ensure``).  Returning no handle is correct —
        # singleton lifecycle is NOT tied to a single jail; we don't
        # want ``stop_loopholes`` reaping it when this jail exits.  (The
        # relay isn't a handle either: it must survive yolo-process
        # death, so it's supervised via its PID file and reaped by
        # ``stop_loopholes`` through the sockets-dir hash.)
        if name == BROKER_LOOPHOLE_NAME:
            try:
                _broker_ensure()
            except Exception as e:
                console.print(
                    f"[red]claude-oauth-broker: failed to ensure singleton: {e}[/red]"
                )
            continue
        h = _start_host_service_external(name, spec, sockets_dir)
        if h is not None:
            handles.append(h)

    return handles


def stop_loopholes(
    handles: List[LoopholeDaemon],
    sockets_dir: Optional[Path],
    *,
    cname: Optional[str] = None,
    runtime: Optional[str] = None,
) -> None:
    """Stop all host services and clean up the sockets directory.

    The relay reap + sockets-dir rmtree are DESTRUCTIVE to a live jail:
    the container bind-mounts the dir, so removing it under a running
    container orphans the mount on a deleted inode — any later
    heal-on-attach mkdir creates a NEW inode the container cannot see,
    and every in-jail auth request 502s while host-side probes look
    healthy.  When ``cname``/``runtime`` are given (``run()`` passes
    them), two guards protect that state:

    * ownership/liveness — if the container is STILL RUNNING (e.g.
      ``yolo run --new`` lost the name-conflict race against a live
      jail and is exiting without ever having started a container),
      the relay and dir belong to that jail: leave both alone.
    * relaunch race — a concurrent ``yolo run`` holds the per-workspace
      lock from before its relay spawn until its container is visible;
      taking the same lock (non-blocking) here means we can never read
      the pidfile after the new run wrote its fresh relay's PID and
      kill it, or rmtree the dir its container is about to mount.  If
      the lock is busy, a relaunch is mid-flight — skip cleanup and let
      the new invocation own the state.
    """
    for h in handles:
        try:
            h._stop()
        except Exception as e:
            console.print(
                f"[yellow]Error stopping host service '{h.name}': {e}[/yellow]"
            )
    if sockets_dir is None:
        return

    lock_f = None
    if cname is not None:
        import fcntl as _fcntl

        try:
            lock_dir = GLOBAL_STORAGE / "locks"
            lock_dir.mkdir(parents=True, exist_ok=True)
            lock_f = open(lock_dir / f"{cname}.lock", "w")
            _fcntl.flock(lock_f, _fcntl.LOCK_EX | _fcntl.LOCK_NB)
        except OSError:
            if lock_f is not None:
                lock_f.close()
            console.print(
                f"[dim]Another yolo invocation is launching {cname}; "
                f"leaving its relay and sockets dir alone.[/dim]"
            )
            return
    try:
        if cname is not None:
            try:
                still_running = bool(
                    find_running_container(cname, runtime=runtime or "podman")
                )
            except Exception:
                still_running = False
            if still_running:
                console.print(
                    f"[dim]Container {cname} is still running; leaving its "
                    f"relay and sockets dir alone.[/dim]"
                )
                return
        # The per-jail broker relay isn't in ``handles`` — it has to
        # survive yolo-process death, so it's supervised via a PID file
        # keyed by the sockets-dir hash (callers don't pass cname here).
        # Reap it BEFORE the rmtree so its SIGTERM socket cleanup targets
        # a directory that still exists.
        prefix = "yolo-host-services-"
        if sockets_dir.name.startswith(prefix):
            _relay_kill(
                _relay_pid_file(sockets_dir.name.removeprefix(prefix)),
                socket_path=sockets_dir / f"{BROKER_LOOPHOLE_NAME}.sock",
            )
        if sockets_dir.exists():
            shutil.rmtree(sockets_dir, ignore_errors=True)
    finally:
        if lock_f is not None:
            lock_f.close()
