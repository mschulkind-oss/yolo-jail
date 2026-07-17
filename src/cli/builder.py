"""On-demand macOS Linux builder — lifecycle + reachability.

macOS can't build the jail's ``aarch64-linux`` image locally; Nix offloads
to a small Linux VM (``nixpkgs#darwin.linux-builder``, Apple Virtualization,
~3 GB).  Historically the user had to run and babysit that VM in a terminal.

This module makes it **on-demand**: yolo starts the builder only when a build
is actually needed, polls until it answers, and lets a launchd timer stop it
when idle.  Running jails never touch the builder — it's a build-time-only
dependency — so keeping it resident would waste ~3 GB for a thing used minutes
a day.  See ``docs/handoff-macos-ondemand-builder.md`` for the decision.

``yolo builder setup`` does the wiring FOR the user: it explains what will
happen, then runs every privileged mutation in ONE ``sudo`` invocation (a
single password prompt) via a generated root script.  This is an interactive
per-run prompt, not a sudo-policy change — it never installs NOPASSWD rules
and never silently mutates files (it prints the exact script first).

Split of concerns:
  * **Pure / verifiable-on-Linux** — reachability (``builder_reachable``),
    setup-state probing (``builder_setup_state``), the ensure-orchestration
    (``ensure_builder``), status, trusted-users merge, and the nix.conf /
    ssh_config / launchd-plist / root-script generators.  All
    string/socket/subprocess-mockable; unit-tested.
  * **Privileged / macOS-only apply** — actually running the root script and
    the VM-credential install.  The script content is testable; running it
    (and the VM bring-up it enables) is verified on a Mac (see the handoff).
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

# The command the launchd service runs to boot the VM.  darwin.linux-builder
# builds a `create-builder` program; we resolve its store path at setup time
# (`nix build nixpkgs#darwin.linux-builder`) and substitute it into the plist.
# This placeholder is what the plist generator emits until resolved.
BUILDER_INSTALLER_BIN = "/run/current-system/sw/bin/create-builder"

# How long to wait for the VM to answer SSH after we start it.  A cache-served
# VM boots in a few seconds; give generous headroom.  Tune once measured on a
# Mac (see handoff open-question #2).
BUILDER_START_TIMEOUT_S = 90
BUILDER_POLL_INTERVAL_S = 1.0


def builder_reachable(
    host: str = "127.0.0.1", port: int = BUILDER_PORT, timeout: float = 1.0
) -> bool:
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


def _current_trusted_users() -> list[str]:
    """The daemon's effective ``trusted-users`` (via ``nix config show``).

    Reading the *effective* value (not just grepping one conf file) is what
    lets us MERGE rather than clobber — Nix is last-line-wins across all its
    conf files, so a naive append could silently drop ``@admin`` or another
    user someone set elsewhere.  Best-effort: empty list if nix is
    unavailable.
    """
    try:
        r = subprocess.run(
            ["nix", "config", "show"], capture_output=True, text=True, timeout=10
        )
    except (OSError, subprocess.SubprocessError):
        return []
    if r.returncode != 0:
        return []
    for line in r.stdout.splitlines():
        if line.startswith("trusted-users") and "=" in line:
            return line.split("=", 1)[1].split()
    return []


def trusted_users_line(current: list[str], me: str) -> Optional[str]:
    """A merged ``trusted-users`` line adding *me*, or None if already trusted.

    Preserves ``root`` and every existing entry (incl. ``@admin`` groups);
    returns None when *me* (or a group that covers it, ``@admin``/``@wheel``)
    is already present, so setup skips a redundant write.
    """
    have = set(current)
    if me in have or "@admin" in have or "@wheel" in have:
        return None
    merged = list(dict.fromkeys(["root", *current, me]))  # dedup, keep order
    return "trusted-users = " + " ".join(merged)


def launchd_plist(idle_timeout_min: int = 30, resident: bool = False) -> str:
    """The launchd plist for the on-demand builder service.

    ``resident=False`` (default): the service is demand-started (yolo
    kickstarts it) and NOT kept alive, so it exits when the VM is shut down
    by the idle watchdog.  ``resident=True``: ``KeepAlive`` for users who
    build so constantly that even a warm-session boot annoys them.

    The idle-stop watchdog itself is a separate concern (a timer that stops
    the VM after no builds for ``idle_timeout_min``); this plist just defines
    the service launchd manages.  The ``create-builder`` command comes from
    the darwin.linux-builder installer at ``BUILDER_INSTALLER_BIN``.
    """
    keepalive = "<true/>" if resident else "<false/>"
    return (
        '<?xml version="1.0" encoding="UTF-8"?>\n'
        '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" '
        '"http://www.apple.com/DTDs/PropertyList-1.0.dtd">\n'
        '<plist version="1.0">\n'
        "<dict>\n"
        f"  <key>Label</key><string>{BUILDER_LAUNCHD_LABEL}</string>\n"
        "  <key>ProgramArguments</key>\n"
        f"  <array><string>{BUILDER_INSTALLER_BIN}</string></array>\n"
        f"  <key>KeepAlive</key>{keepalive}\n"
        "  <key>RunAtLoad</key><false/>\n"
        "  <key>StandardOutPath</key><string>/var/log/yolo-linux-builder.log</string>\n"
        "  <key>StandardErrorPath</key><string>/var/log/yolo-linux-builder.log</string>\n"
        f"  <!-- idle timeout: {idle_timeout_min} min (watchdog stops the VM) -->\n"
        "</dict>\n"
        "</plist>\n"
    )


def setup_root_script(
    max_jobs: int, me: str, current_trusted: list[str], conf_path: Path
) -> str:
    """The ONE root script ``yolo builder setup`` runs under a single sudo.

    Batches every privileged mutation so the user sees one password prompt:
      1. append the ``builders`` line to the daemon conf (idempotent guard);
      2. merge-in trusted-users if *me* isn't already trusted;
      3. write the ssh_config block;
      4. install the launchd service plist;
      5. restart the nix-daemon to apply.

    Pure string builder — no side effects — so it's fully unit-testable; the
    caller runs it via ``sudo bash``.  Uses heredocs/guards so re-running is
    safe (won't duplicate lines).
    """
    label = _detect_nix_daemon_label() or "org.nixos.nix-daemon"
    tu_line = trusted_users_line(current_trusted, me)
    plist_dir = "/Library/LaunchDaemons"
    plist_path = f"{plist_dir}/{BUILDER_LAUNCHD_LABEL}.plist"
    lines = [
        "set -euo pipefail",
        "",
        "# 1. Offload aarch64-linux builds to the builder VM (guard: skip if present).",
        f"if ! grep -qs 'ssh-ng://{BUILDER_USER}@{BUILDER_SSH_HOST}' {conf_path}; then",
        f"  cat >> {conf_path} <<'YOLO_EOF'",
        nix_builders_line(max_jobs).rstrip("\n"),
        "YOLO_EOF",
        "fi",
    ]
    if tu_line is not None:
        lines += [
            "",
            "# 2. Trust this user to hand the daemon a builder (merged, not clobbered).",
            f"cat >> {conf_path} <<'YOLO_EOF'",
            tu_line,
            "YOLO_EOF",
        ]
    lines += [
        "",
        "# 3. ssh host alias so the daemon can reach the VM.",
        f"mkdir -p {SSH_CONFIG_PATH.parent}",
        f"cat > {SSH_CONFIG_PATH} <<'YOLO_EOF'",
        ssh_config_block().rstrip("\n"),
        "YOLO_EOF",
        "",
        "# 4. launchd service for the on-demand builder.",
        f"cat > {plist_path} <<'YOLO_EOF'",
        launchd_plist().rstrip("\n"),
        "YOLO_EOF",
        "",
        "# 5. Apply: restart the nix-daemon.",
        f"launchctl kickstart -k system/{label}",
    ]
    return "\n".join(lines) + "\n"


def run_setup(
    max_jobs: int,
    me: str,
    on_output=None,
    _run=None,
) -> tuple[bool, Optional[str]]:
    """Do the whole privileged wiring in ONE ``sudo`` (single password prompt).

    Pipes ``setup_root_script`` to ``sudo bash -s`` with the terminal
    inherited so the sudo password prompt is visible and the user types it
    once.  ``sudo`` may already be warm (recent prompt) → zero prompts.  This
    is an interactive per-run prompt, NOT a sudo-policy change.

    ``_run`` is injectable for tests (defaults to ``subprocess.run``);
    ``on_output`` is unused for the piped form but kept for symmetry.
    Returns ``(ok, error)``.
    """
    run = _run or subprocess.run
    script = setup_root_script(
        max_jobs, me, _current_trusted_users(), _builder_conf_path()
    )
    try:
        # `sudo bash -s` reads the script on stdin; the tty stays attached for
        # the password prompt.  A single sudo authenticates the whole script.
        r = run(
            ["sudo", "bash", "-s"],
            input=script,
            text=True,
            timeout=120,
        )
    except (OSError, subprocess.SubprocessError) as e:
        return False, str(e)
    if getattr(r, "returncode", 1) != 0:
        return False, f"privileged setup exited {r.returncode}"
    return True, None


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
        r = subprocess.run(_start_cmd(), capture_output=True, text=True, timeout=15)
    except (OSError, subprocess.SubprocessError) as e:
        return False, str(e)
    if r.returncode != 0:
        return False, (r.stderr or "").strip() or f"launchctl exited {r.returncode}"
    return True, None


def stop_builder() -> tuple[bool, Optional[str]]:
    """Ask launchd to stop the builder VM. Returns (stopped_ok, error)."""
    try:
        r = subprocess.run(_stop_cmd(), capture_output=True, text=True, timeout=15)
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
