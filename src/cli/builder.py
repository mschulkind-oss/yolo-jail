"""On-demand macOS Linux builder — lifecycle + reachability.

macOS can't build the jail's ``aarch64-linux`` image locally; Nix offloads
to a small Linux VM (``nixpkgs#darwin.linux-builder``, Apple Virtualization,
~3 GB).  Historically the user had to run and babysit that VM in a terminal.

This module makes it **on-demand**: yolo starts the builder only when a build
is actually needed, polls until it answers, and lets a launchd timer stop it
when idle.  Running jails never touch the builder — it's a build-time-only
dependency — so keeping it resident would waste ~3 GB for a thing used minutes
a day.  See ``docs/handoff-macos-ondemand-builder.md`` for the decision.

Split of concerns:
  * **Pure / verifiable-on-Linux** — reachability (``builder_reachable``),
    setup-state probing (``builder_setup_state``), the ensure-orchestration
    (``ensure_builder``), status, and the nix.conf / ssh_config content
    generators.  All socket/subprocess-mockable; unit-tested.
  * **Privileged / macOS-only apply** — writing ``/etc/nix`` + ``/etc/ssh``
    and (re)starting the launchd service.  Command construction is testable;
    the actual apply must be verified on a Mac (see the handoff).
"""

from __future__ import annotations

import socket
import subprocess
import time
from pathlib import Path
from typing import Optional

from .paths import IS_MACOS
from .storage import _detect_nix_daemon_label, _nix_custom_conf_included

# ── Fixed coordinates of the darwin.linux-builder VM ────────────────────────
# These match nixpkgs' darwin-builder defaults (doc/packages/
# darwin-builder.section.md): the VM forwards guest :22 to host :31022 and the
# daemon reaches it as ssh host alias "linux-builder".  Changing the VM config
# (memory/disk/cores) makes it no longer cache-served, so we keep defaults.
BUILDER_SSH_HOST = "linux-builder"
BUILDER_PORT = 31022
BUILDER_USER = "builder"
BUILDER_KEY_PATH = "/etc/nix/builder_ed25519"
SSH_CONFIG_PATH = Path("/etc/ssh/ssh_config.d/100-linux-builder.conf")

# launchd service label for the yolo-managed builder.  Distinct from the
# nix-daemon label (that's separate, and its restart uses
# _detect_nix_daemon_label()).
BUILDER_LAUNCHD_LABEL = "org.yolo-jail.linux-builder"

# How long to wait for the VM to answer SSH after we start it.  A cache-served
# VM boots in a few seconds; give generous headroom.  Tune once measured on a
# Mac (see handoff open-question #2).
BUILDER_START_TIMEOUT_S = 90
BUILDER_POLL_INTERVAL_S = 1.0


def builder_reachable(host: str = "127.0.0.1", port: int = BUILDER_PORT,
                      timeout: float = 1.0) -> bool:
    """True if something is accepting TCP on the builder's SSH port.

    A cheap liveness signal — the VM forwards guest sshd to
    127.0.0.1:31022, so an accepted connection means the builder is up.
    Never raises; any socket error → False.
    """
    try:
        with socket.create_connection((host, port), timeout=timeout):
            return True
    except OSError:
        return False


def builder_setup_state() -> dict:
    """Probe whether ``yolo builder setup`` has been run, without touching the VM.

    Returns a dict with:
      * ``ssh_config``   — the ``Host linux-builder`` block exists.
      * ``nix_builder``  — the daemon's nix.conf has a ``builders =`` line
        naming ``aarch64-linux`` (checked via the same conf file the setup
        writes to: nix.custom.conf on Determinate, else nix.conf).
      * ``key``          — the ssh identity file exists.
      * ``done``         — all of the above (setup is complete).

    Best-effort: unreadable/absent files count as missing, never raise.
    """
    ssh_ok = SSH_CONFIG_PATH.is_file()
    key_ok = Path(BUILDER_KEY_PATH).is_file()
    nix_ok = _nix_conf_has_builder()
    return {
        "ssh_config": ssh_ok,
        "nix_builder": nix_ok,
        "key": key_ok,
        "done": ssh_ok and key_ok and nix_ok,
    }


def _builder_conf_path() -> Path:
    """The nix.conf file the builder line belongs in.

    Determinate includes ``/etc/nix/nix.custom.conf``; the official installer
    reads ``/etc/nix/nix.conf`` directly.  Mirrors the macOS setup guide's
    trusted-users guidance so both lines land in the same, actually-loaded
    file.
    """
    return (
        Path("/etc/nix/nix.custom.conf")
        if _nix_custom_conf_included()
        else Path("/etc/nix/nix.conf")
    )


def _nix_conf_has_builder() -> bool:
    """True if the daemon's nix.conf already offloads aarch64-linux builds."""
    conf = _builder_conf_path()
    if not conf.is_file():
        return False
    try:
        text = conf.read_text()
    except OSError:
        return False
    for raw in text.splitlines():
        s = raw.strip()
        if s.startswith("#") or not s.startswith("builders"):
            continue
        if "aarch64-linux" in s and BUILDER_SSH_HOST in s:
            return True
    return False


# ── Content generators (pure strings — unit-tested) ─────────────────────────


def ssh_config_block() -> str:
    """The ``/etc/ssh/ssh_config.d`` block that lets the daemon ssh the VM."""
    return (
        f"Host {BUILDER_SSH_HOST}\n"
        f"  Hostname localhost\n"
        f"  HostKeyAlias {BUILDER_SSH_HOST}\n"
        f"  Port {BUILDER_PORT}\n"
        f"  User {BUILDER_USER}\n"
        f"  IdentityFile {BUILDER_KEY_PATH}\n"
    )


def nix_builders_line(max_jobs: int = 4) -> str:
    """The ``builders`` + ``builders-use-substitutes`` lines for nix.conf.

    Uses the ssh host alias (resolved via ssh_config_block) rather than an
    inline host key, so this stays a pure function of max_jobs.  ``ssh-ng``
    is the modern protocol; ``builders-use-substitutes`` lets the VM pull
    deps from the cache instead of copying them all from the mac.
    """
    return (
        f"builders = ssh-ng://{BUILDER_USER}@{BUILDER_SSH_HOST} "
        f"aarch64-linux {BUILDER_KEY_PATH} {max_jobs} - - - -\n"
        f"builders-use-substitutes = true\n"
    )


# ── Lifecycle (launchctl command construction is testable; apply is Mac) ────


def _start_cmd() -> list[str]:
    """launchctl command to start the managed builder service."""
    return ["launchctl", "kickstart", f"system/{BUILDER_LAUNCHD_LABEL}"]


def _stop_cmd() -> list[str]:
    """launchctl command to stop the managed builder service."""
    return ["launchctl", "kill", "SIGTERM", f"system/{BUILDER_LAUNCHD_LABEL}"]


def start_builder() -> tuple[bool, Optional[str]]:
    """Ask launchd to start the builder VM. Returns (started_ok, error)."""
    try:
        r = subprocess.run(
            _start_cmd(), capture_output=True, text=True, timeout=15
        )
    except (OSError, subprocess.SubprocessError) as e:
        return False, str(e)
    if r.returncode != 0:
        return False, (r.stderr or "").strip() or f"launchctl exited {r.returncode}"
    return True, None


def stop_builder() -> tuple[bool, Optional[str]]:
    """Ask launchd to stop the builder VM. Returns (stopped_ok, error)."""
    try:
        r = subprocess.run(
            _stop_cmd(), capture_output=True, text=True, timeout=15
        )
    except (OSError, subprocess.SubprocessError) as e:
        return False, str(e)
    if r.returncode != 0:
        return False, (r.stderr or "").strip() or f"launchctl exited {r.returncode}"
    return True, None


def _poll_until_reachable(
    timeout_s: float = BUILDER_START_TIMEOUT_S,
    interval_s: float = BUILDER_POLL_INTERVAL_S,
    _sleep=time.sleep,
    _now=time.monotonic,
) -> bool:
    """Block until the builder answers on its SSH port or timeout elapses.

    ``_sleep``/``_now`` are injectable so tests drive it without real waits.
    """
    deadline = _now() + timeout_s
    while _now() < deadline:
        if builder_reachable():
            return True
        _sleep(interval_s)
    return builder_reachable()


def ensure_builder(on_progress=None) -> tuple[bool, Optional[str]]:
    """Make the builder ready to accept a build, starting it if needed.

    The frictionless core: replaces "go run a VM in another terminal".
      * already reachable → (True, None), instant.
      * not reachable but setup done → start it, poll until ready.
      * setup NOT done → (False, "not set up") so the caller can point at
        ``yolo builder setup`` instead of a doomed build.
      * not macOS → (False, "not macOS") — there is no darwin builder to start.

    ``on_progress(msg)`` is an optional callback for user-facing status while
    the VM boots.
    """
    if not IS_MACOS:
        return False, "not macOS"
    if builder_reachable():
        return True, None
    state = builder_setup_state()
    if not state["done"]:
        return False, "not set up"
    if on_progress:
        on_progress("starting Linux builder VM…")
    started, err = start_builder()
    if not started:
        return False, f"could not start builder: {err}"
    if _poll_until_reachable():
        return True, None
    return False, f"builder did not become reachable within {BUILDER_START_TIMEOUT_S}s"


def builder_status() -> dict:
    """Full status snapshot for ``yolo builder status`` (read-only, safe)."""
    state = builder_setup_state()
    return {
        **state,
        "reachable": builder_reachable(),
        "conf_path": str(_builder_conf_path()),
        "daemon_label": _detect_nix_daemon_label(),
    }
