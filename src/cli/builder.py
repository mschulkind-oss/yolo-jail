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

The VM is started DETACHED (``nix run nixpkgs#darwin.linux-builder`` in its own
session with a PID file, the broker_relay pattern), so it survives ``yolo``
exiting and can be stopped later by an idle watchdog.

Split of concerns:
  * **Pure / verifiable-on-Linux** — reachability (``builder_reachable``),
    setup-state probing (``builder_setup_state``), the ensure-orchestration
    (``ensure_builder``), status, trusted-users merge, and the nix.conf /
    ssh_config / root-script generators.  All string/socket/subprocess/
    Popen-mockable; unit-tested.
  * **Privileged / macOS-only apply** — actually running the root script under
    sudo and booting the VM.  The script content + spawn args are testable;
    the real VM bring-up is verified on a Mac (see the handoff).
"""

from __future__ import annotations

import os
import signal
import socket
import subprocess
import time
from pathlib import Path
from typing import Optional

from .paths import GLOBAL_STORAGE, IS_MACOS
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
      * ``key``          — the ssh identity file exists (installed by the VM on
        its FIRST boot — so it is NOT part of ``done``, or setup could never
        register complete before a build).
      * ``done``         — the daemon is WIRED to offload (nix.conf +
        ssh_config).  This is the gate for "may we start the VM on demand?".

    Best-effort: unreadable/absent files count as missing, never raise.
    """
    ssh_ok = SSH_CONFIG_PATH.is_file()
    key_ok = Path(BUILDER_KEY_PATH).is_file()
    nix_ok = _nix_conf_has_builder()
    return {
        "ssh_config": ssh_ok,
        "nix_builder": nix_ok,
        "key": key_ok,
        "done": ssh_ok and nix_ok,
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


def setup_root_script(
    max_jobs: int, me: str, current_trusted: list[str], conf_path: Path
) -> str:
    """The ONE root script ``yolo builder setup`` runs under a single sudo.

    Batches every privileged mutation so the user sees one password prompt:
      1. append the ``builders`` line to the daemon conf (idempotent guard);
      2. merge-in trusted-users if *me* isn't already trusted;
      3. write the ssh_config host alias;
      4. restart the nix-daemon to apply.

    The VM itself is NOT started here — yolo boots it on demand (``nix run
    nixpkgs#darwin.linux-builder``, which also installs the ssh key on first
    run).  Pure string builder — no side effects — so it's fully
    unit-testable; the caller runs it via ``sudo bash``.  Uses heredocs/guards
    so re-running is safe (won't duplicate lines).
    """
    label = _detect_nix_daemon_label() or "org.nixos.nix-daemon"
    tu_line = trusted_users_line(current_trusted, me)
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
        "# 4. Apply: restart the nix-daemon.",
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


def _builder_start_cmd() -> list[str]:
    """The command that boots the darwin.linux-builder VM.

    This is nixpkgs' own flake app — the same thing the manual tells users to
    run, and the same thing that installs the VM ssh key on first run.  We use
    it (rather than a nix-darwin launchd service) so setup needs no nix-darwin
    and no pre-resolved store path: it works on any Mac with flakes enabled.
    """
    return ["nix", "run", "nixpkgs#darwin.linux-builder"]


BUILDER_PID_FILE = GLOBAL_STORAGE / "linux-builder.pid"


def _read_builder_pid() -> Optional[int]:
    try:
        return int(BUILDER_PID_FILE.read_text().strip())
    except (OSError, ValueError):
        return None


def _pid_is_live(pid: int) -> bool:
    """``os.kill(pid, 0)`` liveness check (same idiom as the broker)."""
    try:
        os.kill(pid, 0)
        return True
    except OSError:
        return False


def start_builder(_popen=None) -> tuple[bool, Optional[str]]:
    """Boot the builder VM detached (survives this process), like the broker.

    Spawns ``nix run nixpkgs#darwin.linux-builder`` in its own session with
    output to a logfile and records a PID file, so the VM keeps running after
    ``yolo`` exits and the idle-watchdog (Mac TODO) can later stop it.  Does
    NOT block on readiness — the caller polls ``builder_reachable``.
    Returns ``(started_ok, error)``.
    """
    popen = _popen or subprocess.Popen
    # Already running? (live PID or something already answering the port)
    pid = _read_builder_pid()
    if (pid and _pid_is_live(pid)) or builder_reachable():
        return True, None
    try:
        log_dir = GLOBAL_STORAGE / "logs"
        log_dir.mkdir(parents=True, exist_ok=True)
        log_file = open(log_dir / "linux-builder.log", "ab")
        proc = popen(
            _builder_start_cmd(),
            stdout=log_file,
            stderr=log_file,
            stdin=subprocess.DEVNULL,
            start_new_session=True,
            close_fds=True,
        )
    except (OSError, subprocess.SubprocessError) as e:
        return False, str(e)
    try:
        BUILDER_PID_FILE.write_text(f"{proc.pid}\n")
    except OSError:
        pass
    # A corpse within the first moment means the launch itself failed.
    if proc.poll() is not None and proc.returncode not in (0, None):
        return (
            False,
            f"builder process exited {proc.returncode} (see linux-builder.log)",
        )
    return True, None


def stop_builder() -> tuple[bool, Optional[str]]:
    """Stop the detached builder VM by terminating its process group."""
    pid = _read_builder_pid()
    if not pid or not _pid_is_live(pid):
        BUILDER_PID_FILE.unlink(missing_ok=True)
        return True, None
    try:
        os.killpg(os.getpgid(pid), signal.SIGTERM)
    except OSError as e:
        # Fall back to a plain kill; report only if that also fails.
        try:
            os.kill(pid, signal.SIGTERM)
        except OSError:
            return False, str(e)
    BUILDER_PID_FILE.unlink(missing_ok=True)
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
