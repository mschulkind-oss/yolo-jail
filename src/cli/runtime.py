"""Container-runtime selection and per-container plumbing.

Owns:
  * runtime detection — _runtime, _runtime_for_check,
    _detect_runtime, _detect_runtime_for_listing,
    _runtime_is_connectable, _is_apple_container.
  * naming + tracking — container_name_for_workspace,
    write_container_tracking, cleanup_container_tracking.
  * lookup + cleanup — find_running_container,
    find_existing_container, _remove_stale_container,
    _check_container_stuck, _get_container_workspace,
    _resolve_container_cgroup.

Both podman (Linux first-class) and Apple Container (macOS) follow the
same shape: a CLI returns container records, the wrapper functions
hide the `container ls` vs `podman ps -q --filter` divergence so the
rest of the CLI doesn't have to care.
"""

import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path
from typing import Any, Dict, Optional

from .console import console
from .paths import (
    ALL_RUNTIMES,
    CONTAINER_DIR,
    IS_MACOS,
    SUPPORTED_RUNTIMES,
)


def _is_apple_container(path: str) -> bool:
    """Return True if the binary at *path* is Apple's container CLI."""
    try:
        result = subprocess.run(
            [path, "--version"], capture_output=True, text=True, timeout=5
        )
        out = result.stdout + result.stderr
        # Match "Apple" or the distinctive "container CLI" version banner
        return "Apple" in out or "container CLI version" in out
    except Exception:
        return False


def _runtime_is_connectable(rt: str) -> bool:
    """Check if a container runtime daemon is reachable (not just the CLI)."""
    if rt == "container":
        # Apple Container: check system status
        try:
            result = subprocess.run(
                ["container", "system", "status"],
                capture_output=True,
                text=True,
                timeout=5,
            )
            return result.returncode == 0 and "running" in result.stdout.lower()
        except Exception:
            return False
    try:
        result = subprocess.run(
            [rt, "info"],
            capture_output=True,
            text=True,
            timeout=10,
        )
        return result.returncode == 0
    except Exception:
        return False


def _runtime(config: Optional[Dict[str, Any]] = None) -> str:
    """Return the resolved runtime: 'podman' or 'container'.

    Auto-detection priority:
      macOS: container → podman  (native Apple Container preferred)
      Linux: podman             (container CLI is macOS-only)

    Only returns runtimes whose daemon is actually reachable.
    """
    env = os.environ.get("YOLO_RUNTIME")
    if env and env in ALL_RUNTIMES:
        return env
    if config:
        cfg = config.get("runtime")
        if cfg and cfg in ALL_RUNTIMES:
            return cfg
    # Platform-aware auto-detection.
    candidates: tuple[str, ...]
    if IS_MACOS:
        candidates = ("container", "podman")
    else:
        candidates = ("podman",)
    for rt in candidates:
        path = shutil.which(rt)
        if path:
            if rt == "container" and not _is_apple_container(path):
                continue
            if not _runtime_is_connectable(rt):
                continue
            return rt
    console.print(
        "[bold red]No container runtime found. Install podman, or on macOS, Apple's container CLI.[/bold red]"
    )
    sys.exit(1)


def _runtime_for_check(config: Dict[str, Any]) -> tuple[Optional[str], Optional[str]]:
    """Resolve the effective runtime without exiting.

    Same platform-aware priority as _runtime():
      macOS: container → podman
      Linux: podman

    Only returns runtimes whose daemon is actually reachable.
    """
    env = os.environ.get("YOLO_RUNTIME")
    if env and env in ALL_RUNTIMES:
        if shutil.which(env):
            if _runtime_is_connectable(env):
                return env, None
            return (
                None,
                f"Configured runtime '{env}' from YOLO_RUNTIME is not connected",
            )
        return None, f"Configured runtime '{env}' from YOLO_RUNTIME is not on PATH"

    cfg = config.get("runtime")
    if cfg and cfg in ALL_RUNTIMES:
        if shutil.which(cfg):
            if _runtime_is_connectable(cfg):
                return cfg, None
            return (
                None,
                f"Configured runtime '{cfg}' from yolo-jail.jsonc is not connected",
            )
        return None, f"Configured runtime '{cfg}' from yolo-jail.jsonc is not on PATH"

    candidates: tuple[str, ...]
    if IS_MACOS:
        candidates = ("container", "podman")
    else:
        candidates = ("podman",)
    for rt in candidates:
        path = shutil.which(rt)
        if path:
            if rt == "container" and not _is_apple_container(path):
                continue
            if not _runtime_is_connectable(rt):
                continue
            return rt, None
    return None, "No container runtime found on PATH"


def _detect_runtime_for_listing() -> Optional[str]:
    """Best-effort runtime discovery for read-only doctor probes."""
    for r in SUPPORTED_RUNTIMES:
        if shutil.which(r):
            return r
    return None


def _detect_runtime() -> str:
    """Return the container runtime for prune / check use.

    Reads ``YOLO_RUNTIME`` if set (same env var the run command uses),
    otherwise falls back to ``podman``.  Kept shallow on purpose —
    cli.py already has richer runtime detection in the ``run`` path,
    but prune doesn't need that full machinery.
    """
    return os.environ.get("YOLO_RUNTIME") or "podman"


def container_name_for_workspace(workspace: Path) -> str:
    """Deterministic container name from workspace path.

    Uses the directory name for readability (e.g. yolo-tillr) with a short
    hash suffix to handle collisions (e.g. two dirs both named 'app').
    """
    name = workspace.resolve().name
    # Sanitize for container naming: lowercase, alphanumeric + hyphens
    safe = re.sub(r"[^a-z0-9-]", "-", name.lower()).strip("-")[:40]
    if not safe:
        safe = "jail"
    h = hashlib.sha256(str(workspace.resolve()).encode()).hexdigest()[:8]
    return f"yolo-{safe}-{h}"


def find_running_container(name: str, runtime: str = "podman") -> Optional[str]:
    """Return container ID if a container with this name is running, else None."""
    try:
        if runtime == "container":
            # Apple Container CLI: 'ls' shows running containers by default.
            # --filter is not supported; scan the table output instead.
            result = subprocess.run(
                ["container", "ls"],
                capture_output=True,
                text=True,
            )
            for line in result.stdout.strip().splitlines()[1:]:  # skip header
                parts = line.split()
                if parts and parts[0] == name:
                    return name
            return None
        else:
            result = subprocess.run(
                [runtime, "ps", "-q", "--filter", f"name=^/{name}$"],
                capture_output=True,
                text=True,
            )
    except FileNotFoundError:
        return None
    cid = result.stdout.strip()
    return cid if cid else None


def list_running_jail_names(
    runtime: str = "podman",
) -> "tuple[list[str], Optional[str]]":
    """Names of running ``yolo-*`` containers, using the runtime-correct list
    command.

    Apple Container has no ``ps`` (that's podman's verb) and no ``--filter``/
    ``--format`` — it lists with ``container ls`` and a fixed table.  Podman
    uses ``ps --filter name=^yolo- --format {{.Names}}``.  Returns
    ``(names, error)``; ``error`` is a human string when listing genuinely
    failed (so the caller can warn rather than false-pass "no jails").
    """
    try:
        if runtime == "container":
            result = subprocess.run(
                ["container", "ls"], capture_output=True, text=True, timeout=5
            )
            if result.returncode != 0:
                return [], (result.stderr or "").strip() or "container ls failed"
            names = []
            for line in result.stdout.strip().splitlines()[1:]:  # skip header
                parts = line.split()
                if parts and parts[0].startswith("yolo-"):
                    names.append(parts[0])
            return names, None
        result = subprocess.run(
            [runtime, "ps", "--filter", "name=^yolo-", "--format", "{{.Names}}"],
            capture_output=True,
            text=True,
            timeout=5,
        )
        if result.returncode != 0:
            return [], (result.stderr or "").strip() or f"{runtime} ps failed"
        return [n.strip() for n in result.stdout.splitlines() if n.strip()], None
    except Exception as e:  # noqa: BLE001 — surface any listing failure as a string
        return [], str(e)


def find_existing_container(name: str, runtime: str = "podman") -> Optional[str]:
    """Return container ID if a container with this name exists (running OR stopped)."""
    try:
        if runtime == "container":
            # Apple Container CLI: 'ls' only shows running by default;
            # use --all to include stopped containers.
            # --filter is not supported; scan the table output instead.
            result = subprocess.run(
                ["container", "ls", "--all"],
                capture_output=True,
                text=True,
            )
            for line in result.stdout.strip().splitlines()[1:]:
                parts = line.split()
                if parts and parts[0] == name:
                    return name
            return None
        result = subprocess.run(
            [runtime, "ps", "-a", "-q", "--filter", f"name=^/{name}$"],
            capture_output=True,
            text=True,
        )
    except FileNotFoundError:
        return None
    cid = result.stdout.strip()
    return cid if cid else None


def _remove_stale_container(name: str, runtime: str = "podman") -> bool:
    """Remove a stopped container. Returns True if removal succeeded."""
    try:
        if runtime == "container":
            # Apple Container CLI: use 'delete' (aliased as 'rm') with --force
            result = subprocess.run(
                ["container", "rm", "--force", name],
                capture_output=True,
                text=True,
            )
        else:
            result = subprocess.run(
                [runtime, "rm", name],
                capture_output=True,
                text=True,
            )
        if result.returncode == 0:
            cleanup_container_tracking(name)
            return True
        return False
    except FileNotFoundError:
        return False


def write_container_tracking(name: str, workspace: Path):
    """Write a tracking file so users can inspect active containers."""
    tracking_file = CONTAINER_DIR / name
    tracking_file.write_text(str(workspace.resolve()) + "\n")


def cleanup_container_tracking(name: str):
    """Remove tracking file for a container."""
    tracking_file = CONTAINER_DIR / name
    tracking_file.unlink(missing_ok=True)


def _resolve_container_cgroup(cname: str, runtime: str) -> Optional[Path]:
    """Discover the host-side cgroup path for a running container.

    Returns the absolute Path to the container's cgroup directory on the host
    cgroup v2 filesystem, or None if it cannot be determined.

    Always returns None on macOS — cgroups are a Linux kernel feature.
    """
    if IS_MACOS:
        return None
    try:
        if runtime == "podman":
            # podman inspect returns the cgroup path (relative to cgroup root)
            result = subprocess.run(
                ["podman", "inspect", "--format", "{{.State.CgroupPath}}", cname],
                capture_output=True,
                text=True,
                timeout=5,
            )
            if result.returncode == 0 and result.stdout.strip():
                cg_path = result.stdout.strip()
                # Podman with systemd cgroup manager returns paths like
                # "user.slice/user-1000.slice/..." — these are already absolute
                # within /sys/fs/cgroup.
                candidate = Path("/sys/fs/cgroup") / cg_path
                if candidate.exists():
                    return candidate
                # Some podman versions return the scope name only
                # Try to find it via the container's init PID
        # Fallback: use init PID's /proc cgroup
        result = subprocess.run(
            [runtime, "inspect", "--format", "{{.State.Pid}}", cname],
            capture_output=True,
            text=True,
            timeout=5,
        )
        if result.returncode != 0 or not result.stdout.strip():
            return None
        pid = int(result.stdout.strip())
        if pid <= 0:
            return None
        # Read /proc/<pid>/cgroup — format: "0::/path/to/cgroup"
        proc_cgroup = Path(f"/proc/{pid}/cgroup")
        if not proc_cgroup.exists():
            return None
        for line in proc_cgroup.read_text().splitlines():
            parts = line.split(":", 2)
            if len(parts) == 3 and parts[0] == "0":
                cg_rel = parts[2].lstrip("/")
                candidate = Path("/sys/fs/cgroup") / cg_rel
                if candidate.exists():
                    return candidate
    except Exception:
        pass
    return None


def _check_container_stuck(name: str, runtime: str) -> "str | None":
    """Check if a container is stuck in provisioning by inspecting its process tree.

    Returns a reason string if stuck, None if healthy.
    """
    if runtime == "container":
        # Apple Container CLI doesn't support 'top'
        return None
    try:
        result = subprocess.run(
            [runtime, "top", name, "-eo", "comm"],
            capture_output=True,
            text=True,
            timeout=5,
        )
        if result.returncode != 0:
            return None
        procs = [p.strip() for p in result.stdout.strip().splitlines()[1:] if p.strip()]
        if not procs:
            return "no processes"
        # A healthy container has user commands running (claude, copilot, bash shell, etc.)
        # A stuck container's leaf processes are provisioning tools
        provisioning_commands = {"uv", "mise", "pip", "npm"}
        # Check if ALL non-init processes are provisioning-related
        user_procs = [
            p
            for p in procs
            if p not in provisioning_commands
            and p not in ("bash", "sh", "podman-init", "yolo-entrypo", "sleep", "sed")
        ]
        if not user_procs:
            return "stuck in provisioning"
    except Exception:
        pass
    return None


def _get_container_workspace(name: str, runtime: str) -> str:
    """Get the workspace path for a running container via inspect or tracking file."""
    # Try tracking file first (fast)
    tracking_file = CONTAINER_DIR / name
    if tracking_file.exists():
        ws = tracking_file.read_text().strip()
        if ws:
            return ws
    # Fall back to inspecting the container's YOLO_HOST_DIR env var
    try:
        if runtime == "container":
            # Apple Container: inspect outputs JSON without --format support
            result = subprocess.run(
                ["container", "inspect", name],
                capture_output=True,
                text=True,
                timeout=5,
            )
            if result.returncode == 0:
                try:
                    data = json.loads(result.stdout)
                    # Apple Container inspect returns a dict with config.env
                    env_list = data.get("config", {}).get("env", [])
                    for env_entry in env_list:
                        if env_entry.startswith("YOLO_HOST_DIR="):
                            return env_entry.split("=", 1)[1]
                except (ValueError, KeyError, TypeError):
                    pass
        else:
            result = subprocess.run(
                [
                    runtime,
                    "inspect",
                    name,
                    "--format",
                    "{{range .Config.Env}}{{println .}}{{end}}",
                ],
                capture_output=True,
                text=True,
                timeout=5,
            )
            if result.returncode == 0:
                for line in result.stdout.splitlines():
                    if line.startswith("YOLO_HOST_DIR="):
                        return line.split("=", 1)[1]
    except Exception:
        pass
    return "unknown"


# ---------------------------------------------------------------------------
# Podman Machine (macOS VM) introspection — advisory only, never gating
# ---------------------------------------------------------------------------

# Floor below which Podman Machine struggles to host a single jail running
# even one modern agent.  Empirically: claude's first-run native install
# alone has been observed to OOM at 2 GB on macOS.  4 GB leaves enough
# headroom for one agent + provisioning; users running multiple jails or
# heavy in-jail workloads will want more.
PODMAN_MACHINE_MEMORY_FLOOR_MB = 4096


def _podman_machine_memory() -> "Optional[tuple[str, int]]":
    """Return ``(machine_name, memory_mb)`` for the running Podman Machine,
    or None if podman/the machine is unavailable or output isn't parseable.

    Best-effort and side-effect free — every callsite uses this for
    *advisory* output, never as a gate.
    """
    try:
        result = subprocess.run(
            ["podman", "machine", "inspect"],
            capture_output=True,
            text=True,
            timeout=5,
        )
    except Exception:
        return None
    if result.returncode != 0 or not result.stdout.strip():
        return None
    try:
        machines = json.loads(result.stdout)
    except json.JSONDecodeError:
        return None
    if not isinstance(machines, list) or not machines:
        return None

    # Prefer the running machine if there's one; otherwise just take the first.
    machine = next(
        (m for m in machines if isinstance(m, dict) and m.get("State") == "running"),
        machines[0] if isinstance(machines[0], dict) else None,
    )
    if not isinstance(machine, dict):
        return None
    resources = machine.get("Resources") or {}
    mem_mb = resources.get("Memory")
    if not isinstance(mem_mb, int) or mem_mb <= 0:
        return None
    name = machine.get("Name") or "podman-machine-default"
    return name, mem_mb


def _podman_machine_resize_hint() -> str:
    """Single source of truth for the `podman machine set` advice we print.

    Includes the VM-restart caveat — a `machine stop && start` is not
    free, it kills every container running on the VM.
    """
    return (
        f"Increase the VM: `podman machine set --memory "
        f"{PODMAN_MACHINE_MEMORY_FLOOR_MB} && podman machine stop && "
        "podman machine start`.  Note: this restarts the VM and stops "
        "every container running on it."
    )
