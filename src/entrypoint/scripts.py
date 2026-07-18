"""Generators for stand-alone helper scripts dropped into the jail's PATH.

Each function writes a self-contained python or bash script under
``~/.local/bin/`` (or ``~/.yolo-shims/`` for ``yolo``) at jail boot.
These scripts run *out of process* — none of them import anything
from this package — so the body is just a static string passed to
``write_text``.  Lifting them out of __init__.py reduces noise in the
main entrypoint flow without changing behavior.

The Typer-style API is unchanged: cli/__init__.py imports them and
re-exposes the names so existing call sites and tests don't move.
"""

import os
import stat


def _bootstrap_paths():
    """Late-import HOME and SHIM_DIR from the parent package.

    The parent module computes them from $HOME / $JAIL_HOME at import
    time, but tests rebind ``entrypoint.HOME = tmp_path`` after import.
    Reading them lazily lets the patched values flow through.
    """
    from . import HOME, SHIM_DIR

    return HOME, SHIM_DIR


def generate_cglimit_script():
    """Generate yolo-cglimit helper that delegates to the host-side cgroup daemon.

    Usage: yolo-cglimit [--cpu PCT] [--memory LIMIT] [--pids LIMIT] [--name NAME] -- COMMAND...
    Sends a request to the host-side daemon via Unix socket, which creates
    a child cgroup, sets limits, and moves the caller's process into it.
    """
    home, _ = _bootstrap_paths()
    script_dir = home / ".local" / "bin"
    script_dir.mkdir(parents=True, exist_ok=True)
    script_path = script_dir / "yolo-cglimit"

    # Python script that talks to the host daemon via Unix socket.
    # Uses only stdlib (socket, json, os, sys) — no pip deps.
    script_path.write_text(r'''#!/usr/bin/env python3
"""yolo-cglimit — Run a command under cgroup v2 resource limits.

Usage: yolo-cglimit [OPTIONS] -- COMMAND [ARGS...]

Options:
  --cpu PCT       CPU limit as percentage of ALL CPUs (e.g. 75 = 75% of total)
  --memory LIMIT  Memory limit (e.g. 512m, 2g, 1073741824)
  --pids LIMIT    Max number of processes
  --name NAME     Cgroup name (default: auto-generated from PID)

Examples:
  yolo-cglimit --cpu 75 -- python train.py           # 75% of all CPUs
  yolo-cglimit --cpu 50 --memory 2g -- make -j8      # 50% CPU + 2GB RAM
  yolo-cglimit --pids 100 -- ./fork-heavy-script.sh  # Max 100 processes

Resource limits are enforced by the kernel via cgroup v2 and cannot be exceeded.
The host-side daemon handles all privileged cgroup operations securely.
"""
import json
import os
import socket
import sys

CGD_SOCKET = "/run/yolo-services/cgroup-delegate.sock"


def send_request(request: dict) -> dict:
    """Send a JSON request to the host-side cgroup delegate daemon."""
    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    try:
        sock.connect(CGD_SOCKET)
        sock.sendall((json.dumps(request) + "\n").encode())
        data = b""
        while b"\n" not in data and len(data) < 8192:
            chunk = sock.recv(4096)
            if not chunk:
                break
            data += chunk
        return json.loads(data.decode())
    finally:
        sock.close()


def main():
    cpu_pct = None
    memory = None
    pids = None
    name = None
    command = []

    args = sys.argv[1:]
    i = 0
    while i < len(args):
        if args[i] == "--cpu" and i + 1 < len(args):
            cpu_pct = int(args[i + 1])
            i += 2
        elif args[i] == "--memory" and i + 1 < len(args):
            memory = args[i + 1]
            i += 2
        elif args[i] == "--pids" and i + 1 < len(args):
            pids = int(args[i + 1])
            i += 2
        elif args[i] == "--name" and i + 1 < len(args):
            name = args[i + 1]
            i += 2
        elif args[i] == "--":
            command = args[i + 1:]
            break
        elif args[i] in ("-h", "--help"):
            print(__doc__)
            sys.exit(0)
        else:
            print(f"Unknown option: {args[i]}", file=sys.stderr)
            sys.exit(1)

    if not command:
        print("Error: no command specified. Usage: yolo-cglimit [OPTIONS] -- COMMAND",
              file=sys.stderr)
        sys.exit(1)

    if not os.path.exists(CGD_SOCKET):
        print("Error: cgroup delegation not available — host daemon socket not found.",
              file=sys.stderr)
        print("This requires the jail to be started with the yolo CLI (which runs the",
              file=sys.stderr)
        print("host-side cgroup delegate daemon automatically).", file=sys.stderr)
        sys.exit(1)

    # Build the request
    request = {"op": "create_and_join", "name": name or f"job-{os.getpid()}"}
    if cpu_pct is not None:
        request["cpu_pct"] = cpu_pct
    if memory is not None:
        request["memory"] = memory
    if pids is not None:
        request["pids"] = pids

    try:
        resp = send_request(request)
    except Exception as e:
        print(f"Error: failed to contact cgroup daemon: {e}", file=sys.stderr)
        sys.exit(1)

    if not resp.get("ok"):
        print(f"Error: {resp.get('error', 'unknown error')}", file=sys.stderr)
        sys.exit(1)

    if resp.get("warnings"):
        for w in resp["warnings"]:
            print(f"Warning: {w}", file=sys.stderr)

    # exec the command — we're already in the cgroup (daemon moved us via SO_PEERCRED)
    os.execvp(command[0], command)


if __name__ == "__main__":
    main()
''')
    script_path.chmod(script_path.stat().st_mode | stat.S_IEXEC)


def generate_yolo_ps_script():
    """Drop a ``yolo-ps`` wrapper into ``~/.local/bin/`` inside the jail.

    The host-processes loophole ships its jail-side CLI as the
    ``yolo-ps`` wheel console script.  Wheels aren't installed inside
    the jail, so we generate a tiny wrapper that invokes
    ``src.yolo_ps:main`` from the bind-mounted repo root instead.

    Same pattern as ``generate_journalctl_script`` / ``generate_yolo_wrapper``:
    no dependency on PYTHONPATH and no cd dance — just a
    ``sys.path.insert`` before the import.
    """
    home, _ = _bootstrap_paths()
    repo_root = os.environ.get("YOLO_REPO_ROOT", "/opt/yolo-jail")
    script_dir = home / ".local" / "bin"
    script_dir.mkdir(parents=True, exist_ok=True)
    script_path = script_dir / "yolo-ps"
    script_path.write_text(
        f"""#!/usr/bin/env python3
\"\"\"yolo-ps — jail-side client for the host-processes loophole.
Thin wrapper that invokes src.yolo_ps:main from the bind-mounted
yolo-jail repo root.
\"\"\"
import sys
sys.path.insert(0, {repo_root!r})
from src.yolo_ps import main
sys.exit(main())
"""
    )
    script_path.chmod(script_path.stat().st_mode | stat.S_IEXEC)


def generate_journalctl_script():
    """Generate yolo-journalctl helper that bridges to a host-side daemon.

    Usage: yolo-journalctl [journalctl args...]

    The helper reads YOLO_SERVICE_JOURNAL_SOCKET and connects to the host
    daemon over Unix socket, sends its argv as a single JSON line, and
    decodes framed [stdout/stderr/exit] responses until the daemon closes
    the connection.  Exits with the exit code the daemon reports (the code
    journalctl returned on the host).

    The daemon only runs if the user enabled it in config via
    `journal: "user"` or `"full"`.  When the socket is absent the helper
    prints a clear hint and exits 1 — that's the signal the user hasn't
    opted in.
    """
    home, _ = _bootstrap_paths()
    script_dir = home / ".local" / "bin"
    script_dir.mkdir(parents=True, exist_ok=True)
    script_path = script_dir / "yolo-journalctl"

    script_path.write_text(r'''#!/usr/bin/env python3
"""yolo-journalctl — Run journalctl on the host via the yolo-jail journal bridge.

Usage: yolo-journalctl [journalctl args...]

Forwards all arguments to `journalctl` running on the host, streams stdout
and stderr back to the local terminal, and exits with the host process's
exit code.  Enabled only when the jail's config sets `journal: "user"`
(forces --user) or `journal: "full"` (unrestricted).

Examples:
  yolo-journalctl -u nginx -n 50
  yolo-journalctl --user -f
  yolo-journalctl -p err --since "1 hour ago"
"""
import json
import os
import socket
import struct
import sys

DEFAULT_SOCKET = "/run/yolo-services/journal.sock"
SOCKET_PATH = os.environ.get("YOLO_SERVICE_JOURNAL_SOCKET", DEFAULT_SOCKET)

FRAME_STDOUT = 1
FRAME_STDERR = 2
FRAME_EXIT = 3


def _read_exact(sock, n):
    buf = bytearray()
    while len(buf) < n:
        chunk = sock.recv(n - len(buf))
        if not chunk:
            return bytes(buf)
        buf.extend(chunk)
    return bytes(buf)


def main():
    args = sys.argv[1:]
    if args and args[0] in ("-h", "--help") and not os.environ.get("YOLO_JOURNALCTL_PASSTHROUGH_HELP"):
        # -h/--help without env override prints our own doc, not journalctl's.
        # Set YOLO_JOURNALCTL_PASSTHROUGH_HELP=1 to forward it through.
        print(__doc__)
        print(f"Socket: {SOCKET_PATH}")
        return 0

    if not os.path.exists(SOCKET_PATH):
        sys.stderr.write(
            "yolo-journalctl: host journal bridge is not available.\n"
        )
        sys.stderr.write(
            f"  expected socket: {SOCKET_PATH}\n"
        )
        sys.stderr.write(
            "  enable it by setting `journal: \"user\"` (or \"full\") in yolo-jail.jsonc\n"
        )
        sys.stderr.write(
            "  or in ~/.config/yolo-jail/config.jsonc, then restart the jail.\n"
        )
        return 1

    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    try:
        sock.connect(SOCKET_PATH)
    except OSError as e:
        sys.stderr.write(f"yolo-journalctl: connect failed: {e}\n")
        return 1

    try:
        sock.sendall((json.dumps({"args": args}) + "\n").encode())
        exit_code = 1
        while True:
            header = _read_exact(sock, 5)
            if len(header) < 5:
                break
            stream, length = struct.unpack(">BI", header)
            payload = _read_exact(sock, length)
            if len(payload) < length:
                break
            if stream == FRAME_STDOUT:
                sys.stdout.buffer.write(payload)
                sys.stdout.flush()
            elif stream == FRAME_STDERR:
                sys.stderr.buffer.write(payload)
                sys.stderr.flush()
            elif stream == FRAME_EXIT:
                if len(payload) == 4:
                    (exit_code,) = struct.unpack(">i", payload)
                break
            else:
                # Unknown frame type — ignore, forward-compat.
                continue
        return exit_code
    except KeyboardInterrupt:
        return 130
    finally:
        try:
            sock.close()
        except Exception:
            pass


if __name__ == "__main__":
    sys.exit(main())
''')
    script_path.chmod(script_path.stat().st_mode | stat.S_IEXEC)


def generate_yolo_wrapper():
    """Generate a yolo CLI wrapper in ~/.yolo-shims/.

    The host's mise-installed `yolo` console_script does `from src.cli import main`
    which fails inside the jail because the package isn't pip-installed there.
    mise activation can prepend installs/python/.../bin/ to PATH, so the wrapper
    must be in ~/.yolo-shims/ (first on PATH) to take priority.
    """
    home, shim_dir = _bootstrap_paths()
    repo_root = os.environ.get("YOLO_REPO_ROOT", "/opt/yolo-jail")
    shim_dir.mkdir(parents=True, exist_ok=True)
    script_path = shim_dir / "yolo"
    # Use --no-project with explicit --with deps so uv doesn't need to find
    # or build the project (which fails on read-only /opt/yolo-jail mount and
    # when CWD is outside the project tree).
    # Two broken approaches this design avoids:
    #
    # 1. ``export PYTHONPATH={repo_root}`` — ``uv run`` doesn't
    #    reliably honor PYTHONPATH (it manages its own environment
    #    for the ephemeral venv), so ``from src.cli import main``
    #    fails with ModuleNotFoundError intermittently.
    # 2. ``cd {repo_root}`` — the repo root is a read-only bind
    #    mount, and uv's getcwd() fails on bind-mounted CWDs with
    #    "Current directory does not exist" before the Python child
    #    even starts.
    #
    # Instead: a tiny bootstrap Python file in the writable shim dir
    # does the sys.path insert before importing.  The shim runs
    # ``uv run -- python {bootstrap}`` from whatever CWD the user is
    # in (normal writable directory), so neither of the above bites.
    bootstrap_py = shim_dir / "_yolo_bootstrap.py"
    bootstrap_py.write_text(f'''#!/usr/bin/env python3
"""Make ``src`` importable without PYTHONPATH or cd gymnastics."""
import sys
sys.path.insert(0, {repo_root!r})
# Rewrite argv[0] so typer's help/usage strings read "yolo", not
# this bootstrap path.
sys.argv[0] = "yolo"
from src.cli import main

main()
''')
    bootstrap_py.chmod(0o755)
    uv_cli = 'uv run --no-project --with typer --with rich --with "pyjson5>=2.0.0" -- python'
    script_path.write_text(f"""#!/bin/bash
exec {uv_cli} "{bootstrap_py}" "$@"
""")
    script_path.chmod(0o755)

    # A single-executable python shim for the Go binary's delegate-to-Python
    # path (which execs YOLO_PYTHON as ONE argv[0], so it can't be a multi-word
    # `uv run …` string). This wraps the same uv incantation as `yolo`, so
    # delegated subcommands get the CLI deps a bare python3 may lack on a fresh
    # jail. It forwards to `python "$@"` (Go calls it as `<shim> -m src.cli …`).
    py_shim = shim_dir / "_yolo_python"
    py_shim.write_text(f"""#!/bin/bash
exec {uv_cli} "$@"
""")
    py_shim.chmod(0o755)

    # Sibling `yolo-go` shim: same bootstrap, but with the Go gate baked in so
    # `yolo-go` inside the jail runs the Go front door (Python main() re-execs
    # $YOLO_GO_BIN_DIR/yolo). `yolo` stays transparent (Go iff the jail was
    # launched via host `yolo-go`, which forwards YOLO_IMPL=go — seam #11);
    # `yolo-go` lets an in-jail agent pick Go explicitly regardless. The Go
    # binaries ride the live-mounted dist-go dir under /opt/yolo-jail; delegated
    # subcommands fall back to Python via the _yolo_python shim above.
    machine = os.uname().machine
    go_arch = {"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(
        machine, machine
    )
    go_bin_dir = f"/opt/yolo-jail/dist-go/linux-{go_arch}"
    go_script_path = shim_dir / "yolo-go"
    go_script_path.write_text(f"""#!/bin/bash
export YOLO_IMPL=go
export YOLO_GO_BIN_DIR="{go_bin_dir}"
export YOLO_PYTHON="{py_shim}"
exec {uv_cli} "{bootstrap_py}" "$@"
""")
    go_script_path.chmod(0o755)

    # Remove stale yolo wrapper from .local/bin if present — it was generated
    # by older entrypoint versions and lacks the --no-project fix.
    stale = home / ".local" / "bin" / "yolo"
    if stale.exists() and stale.is_file():
        stale.unlink()
