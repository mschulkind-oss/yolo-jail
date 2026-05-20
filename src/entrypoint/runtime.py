"""Container-side runtime plumbing the entrypoint kicks off after config
generation but before exec'ing bash:

  * setup_published_port_localnet — iptables DNAT rules so traffic
    arriving on published ports reaches services bound to 127.0.0.1
    inside the jail (without this, container runtimes route the traffic
    to eth0 and 127.0.0.1-bound services never see it).
  * _supervisor_is_alive + start_jail_daemon_supervisor — singleton
    fork of src.jail_daemon_supervisor for loophole-declared jail
    daemons.  Re-entrant ``podman exec yolo-entrypoint`` calls don't
    stack additional supervisors thanks to a tmpfs PID file.
  * _port_in_use + start_container_port_forwarding — the container half
    of host-port forwarding (host side lives in cli/network.py).  Spawns
    socat per port to bridge container localhost → host service via
    either a Unix socket (Linux) or a TCP gateway (macOS).

These run from PID 1; kernel reaps them when PID 1 exits.
"""

import json
import os
import shutil
import subprocess
import sys
from pathlib import Path


# /tmp is a per-container tmpfs on podman, so a PID
# file here is naturally scoped to this jail and evaporates on restart.
# Serves as the single-instance lock for the supervisor: entrypoint.main()
# re-runs on every ``podman exec yolo-entrypoint <cmd>``, and without
# this guard each exec would fork another supervisor that tries to
# bind the same port (:443 for the oauth broker) and crashloops on
# EADDRINUSE.  See handover #3 for the full story.
SUPERVISOR_PID_FILE = Path("/tmp/yolo-jail-supervisor.pid")

# The socket directory where host-side socat has already created Unix sockets.
FORWARD_SOCKET_DIR = Path("/tmp/yolo-fwd")


def setup_published_port_localnet():
    """Add iptables DNAT rules so published ports reach services bound to 127.0.0.1.

    Container runtimes forward published-port traffic to the container's network
    interface (eth0), not loopback.  Services that bind to 127.0.0.1 therefore
    never see it.  Combined with route_localnet=1 (set by cli.py via --sysctl),
    PREROUTING DNAT rules redirect arriving traffic to 127.0.0.1 — making
    published ports work regardless of the bind address inside the jail.

    Reads YOLO_PUBLISHED_PORTS (JSON array of "PORT/PROTO" strings).
    Silently skips if iptables is unavailable (e.g. when NET_ADMIN is missing).
    """
    raw = os.environ.get("YOLO_PUBLISHED_PORTS", "")
    if not raw:
        return

    try:
        ports = json.loads(raw)
    except (json.JSONDecodeError, ValueError):
        print(f"Warning: invalid YOLO_PUBLISHED_PORTS: {raw}", file=sys.stderr)
        return

    if not ports:
        return

    iptables_bin = shutil.which("iptables")
    if not iptables_bin:
        return

    for entry in ports:
        parts = str(entry).split("/")
        port = parts[0]
        proto = parts[1] if len(parts) > 1 else "tcp"
        try:
            subprocess.run(
                [
                    iptables_bin,
                    "-t",
                    "nat",
                    "-A",
                    "PREROUTING",
                    "-p",
                    proto,
                    "--dport",
                    port,
                    "-j",
                    "DNAT",
                    "--to-destination",
                    f"127.0.0.1:{port}",
                ],
                capture_output=True,
                timeout=5,
            )
        except Exception as e:
            print(
                f"Warning: iptables DNAT for port {port}/{proto}: {e}",
                file=sys.stderr,
            )


def _supervisor_is_alive(pid_file: Path) -> bool:
    """Read ``pid_file`` and return True iff the PID it names is still a
    live process.  Missing/unreadable/corrupt file → False.  Signal 0
    is the canonical Unix liveness probe — it permission-checks the
    target but doesn't actually deliver anything."""
    try:
        pid = int(pid_file.read_text().strip())
    except (OSError, ValueError):
        return False
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        # PID exists but we can't signal it — still counts as alive
        # for our purposes (don't spawn another).
        return True
    except OSError:
        return False
    return True


def start_jail_daemon_supervisor():
    """Fork ``src.jail_daemon_supervisor`` as a detached child, once.

    The supervisor reads ``YOLO_JAIL_DAEMONS`` from the env and spawns
    each loophole-declared jail daemon with restart-on-failure
    semantics.  Absent or empty env means nothing to do.

    Guarded by a tmpfs PID file so repeated ``podman exec yolo-entrypoint``
    calls (the way every ``yolo -- <cmd>`` after the first lands) don't
    stack additional supervisors inside the same container.  Extras
    would each try to bind the same loophole port and crashloop.

    We launch via ``python -m`` rather than a direct import + fork to
    keep the supervisor out of the entrypoint's GC roots and let it
    evolve independently.  The child inherits PID 1's env, including
    the daemon list.
    """
    if not os.environ.get("YOLO_JAIL_DAEMONS", "").strip():
        return
    if _supervisor_is_alive(SUPERVISOR_PID_FILE):
        return
    repo_root = os.environ.get("YOLO_REPO_ROOT", "/opt/yolo-jail")
    proc = subprocess.Popen(
        [sys.executable, "-m", "src.jail_daemon_supervisor"],
        env={**os.environ, "PYTHONPATH": repo_root},
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        close_fds=True,
        start_new_session=False,  # stay in the same process group as PID 1
    )
    try:
        SUPERVISOR_PID_FILE.write_text(f"{proc.pid}\n")
    except OSError:
        # Best-effort: losing the PID file just means a re-entrant
        # exec may spawn a redundant supervisor.  Don't abort boot.
        pass


def _port_in_use(port: int) -> bool:
    """Check if a TCP port is already bound on localhost."""
    import socket

    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        try:
            s.bind(("127.0.0.1", port))
            return False
        except OSError:
            return True


def start_container_port_forwarding():
    """Start container-side socat: TCP-LISTEN on localhost → host service.

    Reads YOLO_FORWARD_HOST_PORTS (JSON array). For each port, starts a socat
    that listens on container's 127.0.0.1:PORT.

    Two modes depending on environment:
    - Unix socket mode (Linux): connects to /tmp/yolo-fwd/port-PORT.sock
      (bind-mounted from host where host-side socat bridges to host localhost).
    - TCP gateway mode (macOS): connects to YOLO_FWD_HOST_GATEWAY:PORT
      directly via TCP (host.containers.internal resolves to the host).

    Skips ports already bound (idempotent for container reuse via exec).
    """
    from . import HOME

    raw = os.environ.get("YOLO_FORWARD_HOST_PORTS", "")
    if not raw:
        return

    try:
        ports = json.loads(raw)
    except (json.JSONDecodeError, ValueError):
        print(f"Warning: invalid YOLO_FORWARD_HOST_PORTS: {raw}", file=sys.stderr)
        return

    if not ports:
        return

    # Determine forwarding mode
    host_gateway = os.environ.get("YOLO_FWD_HOST_GATEWAY", "")

    log_path = HOME / ".yolo-socat.log"
    log_file = open(log_path, "a")

    for entry in ports:
        if isinstance(entry, int):
            local_port = entry
        elif isinstance(entry, str) and ":" in entry:
            local_port = int(entry.split(":", 1)[0])
        elif isinstance(entry, str):
            local_port = int(entry)
        else:
            print(f"Warning: invalid port forward entry: {entry}", file=sys.stderr)
            continue

        if _port_in_use(local_port):
            continue

        if host_gateway:
            # TCP gateway mode: connect directly to host via host gateway
            target = f"TCP:{host_gateway}:{local_port}"
        else:
            # Unix socket mode: connect to bind-mounted socket from host
            sock_path = FORWARD_SOCKET_DIR / f"port-{local_port}.sock"
            if not sock_path.exists():
                print(
                    f"Warning: socket {sock_path} not found for port {local_port}",
                    file=sys.stderr,
                )
                continue
            target = f"UNIX-CONNECT:{sock_path}"

        try:
            subprocess.Popen(
                [
                    "socat",
                    f"TCP-LISTEN:{local_port},bind=127.0.0.1,fork,reuseaddr",
                    target,
                ],
                stdout=subprocess.DEVNULL,
                stderr=log_file,
            )
        except FileNotFoundError:
            print(
                "Warning: socat not found, cannot forward host ports", file=sys.stderr
            )
            log_file.close()
            return
        except Exception as e:
            print(f"Warning: failed to forward port {local_port}: {e}", file=sys.stderr)
