"""Host-side socat tunnels for forward_host_ports.

Provides Unix-socket-backed port forwarding from container localhost to
host localhost services.  Used by run() to spin up tunnels before the
container starts and tear them down after it exits.

  * _parse_port_forwards turns config entries (int, "1234",
    "1234:5678") into (local_port, host_port) tuples.
  * start_host_port_forwarding spawns one ``socat UNIX-LISTEN -> TCP``
    per port and returns the live ``Popen`` handles.
  * cleanup_port_forwarding terminates them and removes the socket dir.

The container side of the tunnel lives in src/entrypoint.py; this
module is the host half.
"""

import shutil
import subprocess
import sys
import time
from pathlib import Path
from typing import List, Optional


def _parse_port_forwards(forward_host_ports: List) -> List[tuple]:
    """Parse forward_host_ports config into (local_port, host_port) tuples."""
    result = []
    for entry in forward_host_ports:
        if isinstance(entry, int):
            result.append((entry, entry))
        elif isinstance(entry, str) and ":" in entry:
            parts = entry.split(":", 1)
            result.append((int(parts[0]), int(parts[1])))
        elif isinstance(entry, str):
            port = int(entry)
            result.append((port, port))
        else:
            print(f"Warning: invalid port forward entry: {entry}", file=sys.stderr)
    return result


def start_host_port_forwarding(
    forward_host_ports: List, cname: str, socket_dir: Path
) -> List[subprocess.Popen]:
    """Start host-side socat to bridge Unix sockets to host localhost services.

    Uses Unix sockets (shared via bind mount) to tunnel host localhost ports
    into the jail — analogous to SSH -L port forwarding. This avoids exposing
    services to the network and works regardless of container networking mode
    (pasta, slirp4netns, bridge, etc.).

    Architecture:
      container app → container socat (TCP→Unix) → socket file → host socat (Unix→TCP) → host 127.0.0.1

    Host side (this function): socat UNIX-LISTEN:sock → TCP:127.0.0.1:PORT
    Container side (entrypoint.py): socat TCP-LISTEN:PORT → UNIX-CONNECT:sock

    Must be called BEFORE the container starts so socket files exist when
    entrypoint.py runs.
    """
    if not forward_host_ports:
        return []

    parsed = _parse_port_forwards(forward_host_ports)
    if not parsed:
        return []

    socket_dir.mkdir(parents=True, exist_ok=True)
    log_dir = Path.home() / ".local" / "share" / "yolo-jail" / "logs"
    log_dir.mkdir(parents=True, exist_ok=True)
    log_file = open(log_dir / f"{cname}-socat.log", "a")

    processes = []
    for local_port, host_port in parsed:
        sock_path = socket_dir / f"port-{local_port}.sock"
        # Remove stale socket from previous run
        sock_path.unlink(missing_ok=True)

        try:
            proc = subprocess.Popen(
                [
                    "socat",
                    f"UNIX-LISTEN:{sock_path},fork,mode=777",
                    f"TCP:127.0.0.1:{host_port}",
                ],
                stdout=subprocess.DEVNULL,
                stderr=log_file,
            )
            processes.append(proc)
        except FileNotFoundError:
            print(
                "Warning: socat not found on host, cannot forward ports. "
                "Install socat (e.g., nix-shell -p socat, apt install socat).",
                file=sys.stderr,
            )
            break
        except Exception as e:
            print(
                f"Warning: failed to start port forward {local_port}: {e}",
                file=sys.stderr,
            )

    # Give socat a moment to create the socket files before the container starts
    if processes:
        time.sleep(0.1)

    return processes


def cleanup_port_forwarding(
    socat_procs: List[subprocess.Popen], socket_dir: Optional[Path]
):
    """Terminate host-side socat processes and remove socket directory."""
    for sp in socat_procs:
        try:
            sp.terminate()
            sp.wait(timeout=2)
        except Exception:
            try:
                sp.kill()
            except Exception:
                pass
    if socket_dir and socket_dir.exists():
        shutil.rmtree(socket_dir, ignore_errors=True)
