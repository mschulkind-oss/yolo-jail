"""``yolo check`` — preflight, validate, and health-probe the jail setup.

The single biggest helper module in cli/ — runs every category of probe
the doctor cares about: container runtime, nix daemon, mac-specific
plumbing, global storage, config validation, entrypoint dry-run, GPU,
KVM, image build, container image presence, running jails (with stuck
detection), loopholes (and broker-creds freshness / per-jail service
liveness), disk usage, and the inline loopholes that live in
yolo-jail.jsonc.

The Typer command is registered in cli/__init__.py via
app.command()(check); this module exports the function body and its
private helpers.
"""

import json
import os
import platform
import re
import shutil
import socket
import subprocess
import time
from pathlib import Path
from typing import Any, Dict, List, Optional

import typer

from src import loopholes as _loopholes

from .config import (
    ConfigError,
    _check_preset_null_conflicts,
    _effective_packages,
    _load_jsonc_with_includes,
    _validate_config,
    load_config,
    merge_config,
)
from .console import console
from .image import _build_image_store_path, _jail_image
from .loopholes_runtime import (
    BROKER_LOOPHOLE_NAME,
    _broker_ping,
    _broker_status,
    _host_service_default_jail_socket,
    _host_service_sockets_dir,
    _parse_memory_value,
)
from .paths import (
    AGENTS_DIR,
    BUILD_DIR,
    BUILTIN_CGROUP_LOOPHOLE_NAME,
    CONTAINER_DIR,
    GLOBAL_HOME,
    GLOBAL_MISE,
    GLOBAL_STORAGE,
    IS_MACOS,
    NATIVE_RUNTIMES,
    SUPPORTED_RUNTIMES,
    USER_CONFIG_PATH,
)
from .prune_cmd import _fmt_bytes
from .runtime import (
    PODMAN_MACHINE_MEMORY_FLOOR_MB,
    _check_container_stuck,
    _detect_runtime,
    _detect_runtime_for_listing,
    _get_container_workspace,
    _podman_machine_memory,
    _podman_machine_resize_hint,
    _runtime_for_check,
    cleanup_container_tracking,
)
from .storage import (
    _detect_nix_daemon_label,
    _nix_custom_conf_included,
    ensure_global_storage,
)
from .version import _git_describe_version


def _loophole_exec_checks_skipped_in_jail() -> bool:
    """True when running inside a jail, where host paths referenced in
    ``loopholes:`` config entries legitimately don't exist.  The
    exec-presence check should short-circuit with an informational
    message instead of false-failing."""
    return os.environ.get("YOLO_VERSION") is not None


def _check_disk_usage(
    ok,
    warn,
    fail,
    *,
    threshold_gb: float = 15.0,
    config: "Optional[Dict[str, Any]]" = None,
) -> None:
    """Surface yolo-jail's total on-disk footprint and nudge toward
    `yolo prune` when it crosses a threshold.

    Threshold defaults to 15 GiB and can be overridden via the
    ``prune.warn_threshold_gb`` config key.  Below threshold: ok.
    Over: warn (never fail — disk use isn't a health bug, just a
    courtesy reminder).
    """
    if os.environ.get("YOLO_VERSION") is not None:
        ok("Inside jail — disk-usage check skipped (runs host-side)")
        return

    # Allow config to override the default threshold without breaking
    # a user who hasn't set one.
    if config:
        prune_cfg = config.get("prune") or {}
        raw = prune_cfg.get("warn_threshold_gb")
        if isinstance(raw, (int, float)) and raw > 0:
            threshold_gb = float(raw)

    from src import prune as _prune

    runtime = _detect_runtime()
    try:
        workspaces = _prune._find_yolo_workspaces(runtime)
    except Exception:  # never block doctor on a prune detection issue
        workspaces = []
    report = _prune._disk_usage_report(
        workspaces=workspaces, global_storage=GLOBAL_STORAGE
    )
    total_gb = report["total"] / (1024**3)
    human = _fmt_bytes(report["total"])
    if total_gb >= threshold_gb:
        warn(
            f"yolo-jail disk usage: {human} (over {threshold_gb:.0f} GiB threshold)",
            "Run `yolo prune` to see reclaim candidates, `yolo prune --apply` to execute",
        )
    else:
        ok(f"yolo-jail disk usage: {human} (threshold {threshold_gb:.0f} GiB)")


def _check_broker_creds_freshness(ok, warn, fail) -> None:
    """Symptom-level health check on the shared Claude credentials.

    The broker exists to keep
    ``~/.local/share/yolo-jail/home/.claude-shared-credentials/.credentials.json``
    valid — its ``expiresAt`` should always be comfortably in the
    future.  When refreshes fail to land (Claude not asking, broker
    crash, server-side revocation, …) the symptom is the same:
    expiresAt approaches now and nothing rewrites the file.

    This is the actually-useful metric the 2026-04-28 handoff called
    for: surface the symptom directly so we don't have to wait for a
    user to hit a 401 to find out refreshes have stopped.

    Caveat: a fresh-looking ``expiresAt`` can still hide a
    server-revoked refresh token (observed 2026-04-28); only a real
    network roundtrip can prove validity.  That's a planned follow-up.
    """
    creds_path = GLOBAL_HOME / ".claude-shared-credentials" / ".credentials.json"
    if not creds_path.exists():
        # First /login hasn't happened yet — nothing to grade.
        return
    try:
        # ``ensure_global_storage`` touches an empty placeholder file so
        # the bind-mount target exists on first boot.  Treat zero-byte
        # as the documented pre-login state (same as "file absent"),
        # not as a corruption warning.
        if creds_path.stat().st_size == 0:
            return
    except OSError:
        pass
    try:
        data = json.loads(creds_path.read_text())
        expires_at_ms = int(data["claudeAiOauth"]["expiresAt"])
    except (json.JSONDecodeError, KeyError, TypeError, ValueError, OSError) as e:
        warn(
            f"shared creds {creds_path}: unreadable",
            f"{type(e).__name__}: {e}",
        )
        return

    now_ms = int(time.time() * 1000)
    remaining_s = (expires_at_ms - now_ms) // 1000
    # File mtime is a proxy for "time since last refresh" — every
    # successful refresh-grant or /login rewrites the file.  Flat
    # mtime + advancing wall-clock = nothing is landing.
    try:
        mtime_age_s = int(time.time() - creds_path.stat().st_mtime)
    except OSError:
        mtime_age_s = -1

    def _fmt(seconds: int) -> str:
        if seconds < 0:
            return "?"
        if seconds < 3600:
            return f"{seconds // 60}m"
        return f"{seconds // 3600}h{(seconds % 3600) // 60}m"

    last_write = f"last write {_fmt(mtime_age_s)} ago" if mtime_age_s >= 0 else ""

    if remaining_s < 0:
        fail(
            f"shared creds expired {_fmt(-remaining_s)} ago"
            + (f" ({last_write})" if last_write else ""),
            "Refreshes are not landing.  Run /login from inside a "
            "jail to recover; check broker log at "
            "~/.local/share/yolo-jail/logs/host-service-claude-oauth-broker.log",
        )
    elif remaining_s < 3600:
        warn(
            f"shared creds expire in {_fmt(remaining_s)}"
            + (f" ({last_write})" if last_write else ""),
            "Approaching expiry without a refresh having landed.  "
            "Healthy cadence keeps this above 1h.",
        )
    else:
        suffix = f", {last_write}" if last_write else ""
        ok(f"shared creds valid for {_fmt(remaining_s)}{suffix}")


def _check_loopholes(ok, warn, fail) -> None:
    """Surface loophole discovery + each loophole's own self-check.

    Bad manifests warn (one broken third-party loophole shouldn't fail
    the whole check); individual self-checks that return non-zero fail,
    since the loophole's author declared this is the health signal.
    """
    if os.environ.get("YOLO_VERSION") is not None:
        ok("Inside jail — loophole checks skipped (managed by host)")
        return
    entries = _loopholes.validate_loopholes()
    if not entries:
        ok(f"No loopholes installed ({_loopholes.loopholes_dir()})")
        return
    for path, loophole, err in entries:
        if err:
            warn(f"loophole {path.name}: invalid manifest", err)
            continue
        assert loophole is not None
        if not loophole.enabled:
            ok(f"loophole {loophole.name}: disabled")
            continue
        if not loophole.requirements_met:
            # Present-but-inactive: running doctor_cmd would invoke a
            # binary the loophole explicitly declared a precondition
            # for, and we know that precondition isn't met.  Just
            # report and skip.
            ok(f"loophole {loophole.name}: inactive ({loophole.inactive_reason})")
            continue
        if not loophole.doctor_cmd:
            ok(f"loophole {loophole.name}: no self-check declared")
            continue
        results = _loopholes.run_doctor_checks([loophole], timeout=10.0)
        r = results[0]
        if r.returncode == 0:
            ok(f"loophole {loophole.name}: self-check ok")
            # Broker gets an additional runtime probe: self_check
            # validates static state (CA files, creds parseable) but
            # can't tell whether the daemon is actually answering.
            # This is the check that would have caught the 2026-04-24
            # stale-wheel incident in doctor instead of at
            # /login-prompt time.
            if loophole.name == BROKER_LOOPHOLE_NAME:
                # Symptom-level: are the shared creds about to expire?
                # Liveness above only tells us the daemon is up; this
                # tells us whether refreshes are actually landing.
                _check_broker_creds_freshness(ok, warn, fail)
                status = _broker_status()
                if status["pid_live"] and status["ping_ok"]:
                    ok(
                        "loophole claude-oauth-broker: daemon live "
                        f"(pid={status['pid']}, ping ok)"
                    )
                elif status["pid"] is None:
                    warn(
                        "loophole claude-oauth-broker: daemon not running",
                        "First `yolo run` will spawn it; "
                        "`yolo broker status` reports state, "
                        "`yolo broker restart` cycles.",
                    )
                elif not status["pid_live"]:
                    fail(
                        "loophole claude-oauth-broker: stale PID file, "
                        f"pid {status['pid']} not running",
                        "Run `yolo broker restart` to clean up and respawn.",
                    )
                else:
                    fail(
                        "loophole claude-oauth-broker: daemon unresponsive "
                        f"(pid={status['pid']}, socket "
                        f"{'present' if status['socket_exists'] else 'missing'}, "
                        "ping failed)",
                        "Run `yolo broker restart` — typical after a "
                        "wheel upgrade; old code still loaded in memory.",
                    )
        elif r.returncode is None:
            warn(
                f"loophole {loophole.name}: self-check could not run",
                r.output or "command missing",
            )
        else:
            # Each "FAIL: …" chunk is a distinct problem that should
            # render on its own (with its own ❌ and arrow-indented
            # note). Without this split, multi-problem self-checks pack
            # several issues into one run-on blob.
            problems = _split_self_check_problems(r.output)
            if not problems:
                fail(
                    f"loophole {loophole.name}: self-check failed (rc={r.returncode})",
                    "no output",
                )
            else:
                for title, detail in problems:
                    fail(f"loophole {loophole.name}: {title}", detail)


def _split_self_check_problems(output: str) -> List["tuple[str, str]"]:
    """Split module self-check output into (title, detail) pairs.

    Self-checks print one or more ``FAIL: …`` entries, each optionally
    followed by continuation lines that provide remediation.  This splits
    on ``FAIL:`` boundaries, takes the first line of each chunk as the
    title and the rest as the detail.  Non-FAIL preamble is dropped.
    """
    problems: List["tuple[str, str]"] = []
    current: Optional[List[str]] = None
    for raw in output.splitlines():
        line = raw.rstrip()
        if line.startswith("FAIL:"):
            if current is not None:
                problems.append(_finalize_problem(current))
            current = [line[len("FAIL:") :].strip()]
        elif current is not None:
            current.append(line)
    if current is not None:
        problems.append(_finalize_problem(current))
    return problems


def _finalize_problem(lines: List[str]) -> "tuple[str, str]":
    title = lines[0]
    detail_lines = [line for line in lines[1:] if line.strip()]
    return title, "\n".join(detail_lines)


def _relay_socket_visible_in_jail(
    runtime: Optional[str], cname: Optional[str]
) -> Optional[bool]:
    """Does the RUNNING container actually see the relay socket?

    A host-side probe of the sockets dir can pass while the jail 502s:
    if the dir was removed and recreated AFTER the container mounted it
    (host /tmp aging, a teardown/startup race), the bind mount pins the
    old, deleted inode — the healed relay binds into a new inode the
    jail never sees.  That is exactly the "doctor says healthy while
    one jail 502s" blind spot this probe closes: check the path where
    the jail looks, from inside the jail.

    Returns True (visible), False (absent in-jail), or None (could not
    determine — exec unavailable/failed; never grade on a guess).
    """
    if runtime is None or cname is None:
        return None
    jail_sock = _host_service_default_jail_socket(BROKER_LOOPHOLE_NAME)
    try:
        result = subprocess.run(
            [runtime, "exec", cname, "sh", "-c", f"test -S {jail_sock}"],
            capture_output=True,
            timeout=10,
        )
    except Exception:
        return None
    if result.returncode == 0:
        return True
    if result.returncode == 1:
        return False
    return None  # 125/126/127…: exec-level failure, not a probe answer


def _check_broker_relay(
    ok,
    fail,
    label: str,
    sock_path: Path,
    *,
    runtime: Optional[str] = None,
    cname: Optional[str] = None,
) -> None:
    """Probe one jail's broker relay socket end-to-end.

    The failing LAYER must be named: the per-jail relay is a supervised
    standalone process that any ``yolo`` invocation against the jail
    respawns, while the singleton broker behind it is cycled with
    ``yolo broker restart``.  Four outcomes:

    - socket file absent            → relay layer (never started / dir wiped)
    - connect() refused             → relay layer (process exited, stale socket)
    - connected but no pong proxied → broker layer (relay dials the
      singleton per connection and closes the client when it can't)
    - host-side pong but the socket is invisible IN-JAIL → mount layer
      (sockets dir recreated after the container mounted it; only a
      relaunch remounts it — see ``_relay_socket_visible_in_jail``)
    """
    if not sock_path.exists():
        fail(
            f"{label}: relay socket missing",
            f"Expected {sock_path}.  The per-jail relay never started or "
            f"its sockets dir was removed.  Any `yolo` invocation against "
            f"this jail respawns it.",
        )
        return
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    try:
        s.settimeout(2.0)
        s.connect(str(sock_path))
    except (OSError, socket.timeout) as e:
        fail(
            f"{label}: relay socket dead",
            f"connect({sock_path}) failed: {e}.  The relay process exited; "
            f"any `yolo` invocation against this jail respawns it.",
        )
        return
    finally:
        try:
            s.close()
        except OSError:
            pass
    if _broker_ping(sock_path):
        if _relay_socket_visible_in_jail(runtime, cname) is False:
            fail(
                f"{label}: relay ok on host, socket invisible in-jail",
                "The sockets dir was recreated after the container mounted "
                "it (host /tmp cleanup or a teardown/startup race): the "
                "jail's bind mount still points at the old, deleted "
                "directory, so in-jail auth requests 502 even though the "
                "host-side relay answers.  Relaunch the jail to remount "
                "the directory.",
            )
        else:
            ok(f"{label}: relay ok, broker answers through it")
    else:
        fail(
            f"{label}: relay up, broker unreachable",
            "The relay accepted but the singleton broker did not answer "
            "the proxied ping.  Check `yolo broker status` / "
            "`yolo broker restart`.",
        )


def _check_host_service_liveness(ok, warn, fail) -> None:
    """For each running jail, verify each external host_daemon's socket is alive.

    A loophole's static ``self-check`` (run earlier) only validates the
    loophole code itself — it doesn't tell us whether the per-jail
    daemon actually spawned, stayed up, and is currently accepting
    connections.  Without this probe, a daemon that crash-loops on
    startup (e.g. broker can't find openssl) shows ``self-check ok``
    while every jail's broker is dead.
    """
    if os.environ.get("YOLO_VERSION") is not None:
        return  # inside jail — host sockets aren't reachable
    try:
        entries = _loopholes.validate_loopholes()
    except Exception:
        return
    externals = [
        lp
        for _, lp, err in entries
        if lp is not None
        and not err
        and lp.enabled
        and lp.requirements_met
        and lp.host_daemon is not None
    ]
    if not externals:
        ok("no host-side daemons to probe")
        return
    detected_runtime = _detect_runtime_for_listing()
    if detected_runtime is None:
        warn("no container runtime found — skipping liveness probe")
        return
    try:
        result = subprocess.run(
            [
                detected_runtime,
                "ps",
                "--filter",
                "name=^yolo-",
                "--format",
                "{{.Names}}",
            ],
            capture_output=True,
            text=True,
            timeout=5,
        )
    except Exception as e:
        warn(f"could not list containers: {e}")
        return
    if result.returncode != 0:
        stderr = (result.stderr or "").strip()
        # Old Apple Container CLIs don't ship the `ps` plugin the newer
        # ones do (`container-ps` not found, exit 64).  Don't dump the raw
        # plugin-help wall of text — say plainly it's a too-old CLI and how
        # to fix it.  A failed `ps` must not read as "no jails running"
        # (that would false-pass every per-jail probe below).
        if detected_runtime == "container" and (
            "container-ps" in stderr or "plugin" in stderr.lower()
        ):
            warn(
                "Apple Container CLI too old for the per-jail liveness probe",
                "Your `container` CLI lacks the `ps` plugin this probe uses. "
                "Upgrade it: `brew upgrade container` (then `container system "
                "start`).  This only affects the liveness probe, not running jails.",
            )
        else:
            warn(
                f"could not list containers: `{detected_runtime} ps` exited "
                f"{result.returncode}",
                stderr.splitlines()[0] if stderr else "",
            )
        return
    cnames = [c.strip() for c in result.stdout.splitlines() if c.strip()]
    if not cnames:
        ok("no jails running — nothing to probe")
        return
    for cname in cnames:
        sockets_dir = _host_service_sockets_dir(cname)
        for lp in externals:
            sock_path = sockets_dir / f"{lp.name}.sock"
            label = f"loophole {lp.name} @ {cname}"
            # Singleton broker: since relay unification its per-jail
            # entry is a real listening socket owned by the per-jail
            # relay process (it used to be a bind-mount placeholder
            # that had to be skipped here — handoff 2026-04-28).  A
            # dead relay reproduces the "one jail 502s while doctor
            # says broker healthy" symptom, so probe it end-to-end
            # and name the failing layer.  Singleton liveness itself
            # is still graded in ``_check_loopholes``.
            if lp.name == BROKER_LOOPHOLE_NAME:
                _check_broker_relay(
                    ok,
                    fail,
                    label,
                    sock_path,
                    runtime=detected_runtime,
                    cname=cname,
                )
                continue
            if not sock_path.exists():
                fail(
                    f"{label}: no socket",
                    f"Expected {sock_path}.  Daemon never started or "
                    f"crashed at spawn.  Tail "
                    f"~/.local/share/yolo-jail/logs/host-service-{lp.name}.log "
                    f"for the reason; restart the jail to respawn.",
                )
                continue
            s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            try:
                s.settimeout(2.0)
                s.connect(str(sock_path))
                ok(f"{label}: socket accepting")
            except (OSError, socket.timeout) as e:
                fail(
                    f"{label}: socket dead",
                    f"connect({sock_path}) failed: {e}.  "
                    f"Daemon process likely exited; restart the jail.",
                )
            finally:
                try:
                    s.close()
                except Exception:
                    pass


def _diagnose_nix_build_failure(stderr_tail: List[str]) -> "tuple[str, str]":
    """Turn opaque nix build stderr into a (title, remediation) pair.

    The common macOS failure is nix trying to BUILD a Linux
    (``aarch64-linux``) derivation from source — e.g. the flake's own
    generated ``yolo-jail-conf.json``, which is never in the binary cache —
    with no Linux builder available.  nix reports only ``1 dependency
    failed`` at the top level, which tells the user nothing.  Detect the
    "needs a Linux builder" signatures and lead the user to the fix (a
    prebuilt image from the published cache, or the nix-darwin
    linux-builder); otherwise fall back to the stderr tail.
    """
    text = "\n".join(stderr_tail)
    low = text.lower()

    # nix's EXPLICIT cross-build refusal — unambiguous "needs a Linux builder".
    explicit_cross = ("required to build" in low and "aarch64-linux" in low) or (
        "cannot build" in low and "aarch64-linux" in low
    )
    # A bare "dependency failed" on macOS is the AMBIGUOUS case: either the
    # image needs a Linux builder for a from-source drv, OR a workspace
    # `packages` entry forces a source build (a version/url/hash override is
    # never cacheable by construction; a bad nixpkgs pin misses the cache).
    ambiguous_mac = IS_MACOS and "dependency failed" in low and not explicit_cross

    if explicit_cross:
        return (
            "Image build needs a Linux builder (something must be built from source)",
            "Part of the image isn't in the binary cache and must be BUILT, "
            "but building a Linux image on macOS needs a Linux builder.\n"
            + _LINUX_BUILDER_REMEDY,
        )
    if ambiguous_mac:
        return (
            "Image build failed — likely needs a Linux builder or a cached package",
            "A Linux derivation had to be built from source and couldn't be.\n"
            + _LINUX_BUILDER_REMEDY,
        )
    return "nix build failed", "\n".join(stderr_tail[-10:]) if stderr_tail else ""


_WILL_BUILD_RE = re.compile(
    r"^(this derivation|these \d+ derivations) will be built:", re.MULTILINE
)


def _nix_dry_run_will_build(
    repo_root: Path, extra_packages: Optional[List[object]]
) -> "tuple[Optional[bool], list[str]]":
    """Return (will_build, offending_drvs) from a `nix build --dry-run`.

    ``will_build`` is True when nix's plan lists derivations that will be
    BUILT (a binary-cache miss → needs a Linux builder on macOS), False
    when everything is substitutable, or **None** when inconclusive (offline
    / substituter unreachable / dry-run errored) — callers must treat None
    as "unknown", never as a miss (offline makes everything look built).

    Same invocation as the real build (image.py) minus the actual build:
    no ``--system`` (the flake already maps darwin→linux), and we parse
    STDERR (``--json`` has no build-vs-fetch distinction).
    """
    env = os.environ.copy()
    if extra_packages:
        env["YOLO_EXTRA_PACKAGES"] = json.dumps(extra_packages)
    try:
        p = subprocess.run(
            [
                "nix",
                "--extra-experimental-features",
                "nix-command flakes",
                "build",
                ".#ociImage",
                "--impure",
                "--dry-run",
            ],
            cwd=repo_root,
            env=env,
            capture_output=True,
            text=True,
            timeout=120,
        )
    except (OSError, subprocess.SubprocessError):
        return None, []
    err = p.stderr
    if not isinstance(err, str):
        return None, []  # defensive: unexpected shape -> inconclusive
    # A non-zero exit with a network/substituter error (not a plan) is
    # inconclusive, not a miss.
    if p.returncode != 0 and not _WILL_BUILD_RE.search(err):
        return None, []
    if not _WILL_BUILD_RE.search(err):
        return False, []
    # Collect the .drv names under the "will be built:" header so we can
    # name the offending package(s).
    offending: list[str] = []
    in_build = False
    for line in err.splitlines():
        if _WILL_BUILD_RE.match(line):
            in_build = True
            continue
        if in_build:
            s = line.strip()
            if s.endswith(".drv"):
                offending.append(s.rsplit("/", 1)[-1])
            elif not s or "will be fetched" in line:
                in_build = False
    return True, offending


def _has_linux_builder() -> bool:
    """True if a usable aarch64-linux Nix builder is reachable on this host.

    Checks the ``builders`` config (inline entries and/or the
    ``@/etc/nix/machines`` file) for an entry whose systems list contains
    EXACT ``aarch64-linux`` with a non-zero job slot, plus Determinate
    Nix's native-linux-builder.  Best-effort: any probe error → False.
    """
    # Determinate Nix native-linux-builder (no /etc/nix/machines entry).
    try:
        r = subprocess.run(
            ["nix", "config", "show"], capture_output=True, text=True, timeout=10
        )
        cfg = r.stdout if r.returncode == 0 else ""
    except (OSError, subprocess.SubprocessError):
        cfg = ""
    builder_lines: list[str] = []
    for line in cfg.splitlines():
        if line.startswith("builders ="):
            spec = line.split("=", 1)[1].strip()
            for part in spec.split(";"):
                part = part.strip()
                if part.startswith("@"):
                    fp = Path(part[1:])
                    if fp.exists():
                        try:
                            builder_lines.extend(fp.read_text().splitlines())
                        except OSError:
                            pass
                elif part:
                    builder_lines.append(part)
    for entry in builder_lines:
        entry = entry.strip()
        if not entry or entry.startswith("#"):
            continue
        fields = entry.split()
        systems = fields[1].split(",") if len(fields) > 1 else []
        max_jobs = fields[3] if len(fields) > 3 else "1"
        if "aarch64-linux" in systems and max_jobs != "0":
            return True
    return False


_LINUX_BUILDER_REMEDY = (
    "Building a Linux image on macOS needs a Linux builder.  Set up the "
    "nix-darwin linux-builder (a persistent, launchd-managed Linux VM — the "
    "standard Nix tool for this):\n"
    "  nix-darwin:  nix.linux-builder.enable = true;  (then darwin-rebuild switch)\n"
    "  standalone:  nix run nixpkgs#darwin.linux-builder  (leave it running)\n"
    "See docs/macos.md > 'Linux builder' for the trusted-users step.\n"
    "If this is a custom `packages` entry: a {version,url,hash} override is "
    "never cached (a rebuild is unavoidable); a {nixpkgs:<commit>} pin may "
    "just need a released revision that IS cached."
)


def _preflight_builder_needs(
    repo_root: Path, extra_packages: Optional[List[object]], ok, warn, fail
) -> "Optional[bool]":
    """Decide whether the real image build should even be attempted.

    Returns True when the build is viable (fully cached, a builder is
    present, or we couldn't tell and should try), and **False** when we
    KNOW it will fail (a from-source Linux build is required but no builder
    exists) — the caller then skips the doomed build + its misleading
    "Run 'yolo' once to build the image" hint, so the user sees ONE clear
    message instead of a WARN followed by a duplicate FAIL.

    States: A (nothing builds) quiet dim info → True; B (will build, builder
    present) PASS → True; C (will build, no builder) actionable FAIL → False;
    inconclusive (offline) dim info → True (try; the build surfaces the truth).
    """
    will_build, offending = _nix_dry_run_will_build(repo_root, extra_packages)
    if will_build is None:
        console.print(
            "  [dim]- Could not check binary-cache coverage (nix dry-run "
            "unavailable/offline); attempting the build anyway.[/dim]"
        )
        return True
    if not will_build:
        console.print(
            "  [dim]- No Linux builder needed: every image path is served from "
            "the binary cache (nothing is built from source).[/dim]"
        )
        return True
    named = f" ({', '.join(offending[:3])})" if offending else ""
    if _has_linux_builder():
        ok(
            f"A package will be built from source{named}; a Linux builder will handle it"
        )
        return True
    # Known-doomed: a from-source Linux build with no builder.  Emit ONE
    # actionable FAIL and tell the caller to skip the real build entirely.
    fail(
        f"Image needs a Linux builder — a package must be built from source{named}",
        _LINUX_BUILDER_REMEDY,
    )
    return False


def _check_macos_user_backend(ok, warn, fail) -> None:
    """Probe readiness of the native macos-user backend.

    Reports the OS, Apple Seatbelt (``sandbox-exec``), and whether the
    dedicated sandbox account is provisioned — with actionable fixes.
    Never runs inside a jail (host-side state).  Uses the same builders +
    detectors the backend itself uses so the check and the run agree.
    """
    from .macos_user import SANDBOX_USER, _sandbox_user_exists
    from .paths import IS_MACOS

    console.print("[bold]macOS-user backend[/bold]")
    if os.environ.get("YOLO_VERSION") is not None:
        ok("Inside jail — macos-user checks skipped (host-side backend)")
        return
    if not IS_MACOS:
        fail(
            "runtime 'macos-user' requires macOS",
            "It isolates via a dedicated macOS user account; use 'podman' "
            "or 'container' on this host.",
        )
        return
    if shutil.which("sandbox-exec"):
        ok("Apple Seatbelt (sandbox-exec) available")
    else:
        fail(
            "sandbox-exec not found",
            "Seatbelt ships with macOS; a missing binary means an unusual PATH.",
        )
    if _sandbox_user_exists():
        ok(f"Sandbox user '{SANDBOX_USER}' exists")
    else:
        warn(
            f"Sandbox user '{SANDBOX_USER}' not provisioned",
            "Run the one-time setup to create it (see "
            "docs/macos-native-user-sandbox-design.md).",
        )


def _check_podman_machine_resources(workspace, *, ok, warn) -> None:
    """Surface Podman Machine VM memory in `yolo check` output and warn if
    it's below a sensible floor or below the workspace's
    ``resources.memory`` request.  Best-effort: any error is silently
    skipped — this check is informational, not gating.

    Ported from PR #21 (kurt-hs) against the pre-package-split layout.
    """
    info = _podman_machine_memory()
    if info is None:
        return
    name, mem_mb = info

    # Compare against the workspace's requested resources.memory if set.
    workspace_floor_mb = None
    try:
        ws_config = load_config(workspace, strict=False)
    except Exception:
        ws_config = {}
    requested = (ws_config.get("resources") or {}).get("memory")
    if isinstance(requested, str):
        parsed = _parse_memory_value(requested)
        if parsed is not None:
            workspace_floor_mb = parsed // (1024 * 1024)

    fix = _podman_machine_resize_hint()

    if mem_mb < PODMAN_MACHINE_MEMORY_FLOOR_MB:
        warn(
            f"Podman Machine '{name}' memory: {mem_mb} MB "
            f"(below {PODMAN_MACHINE_MEMORY_FLOOR_MB} MB recommended floor)",
            f"Agent installs (claude, copilot) and `mise install` can OOM at "
            f"this size — claude's first-run native install has been observed "
            f"to take SIGKILL at 2 GB.  {fix}",
        )
    elif workspace_floor_mb is not None and mem_mb < workspace_floor_mb:
        warn(
            f"Podman Machine '{name}' memory: {mem_mb} MB "
            f"(workspace requests resources.memory={requested})",
            f"The jail's memory limit is enforced inside the VM, so the VM "
            f"itself needs at least that much.  {fix}",
        )
    else:
        ok(f"Podman Machine '{name}' memory: {mem_mb} MB")


def check(
    build: bool = typer.Option(
        True,
        "--build/--no-build",
        help="Run nix build as part of the preflight (default: on)",
    ),
):
    """Validate environment, config, and build. Run after every config edit."""
    # Late imports — _resolve_repo_root and _entrypoint_preflight still
    # live in cli/__init__.py.  Inline the dotted-path lookup so this
    # module doesn't take a circular import at package init time.
    from . import _entrypoint_preflight, _resolve_repo_root

    ensure_global_storage()
    workspace = Path.cwd()

    passed = 0
    failed = 0
    warned = 0

    def _print_note(note: str) -> None:
        """Render a note; every line gets the same indent, first line
        marked with an arrow so multi-line messages don't become a wall
        of text.  ASCII ``->`` (not a Unicode arrow) so it's legible on a
        terminal without special-glyph fonts (e.g. plain ssh)."""
        lines = note.splitlines() or [note]
        for i, line in enumerate(lines):
            prefix = "       -> " if i == 0 else "          "
            console.print(f"{prefix}[dim]{line}[/dim]")

    # Status badges are colored TEXT, not emoji — legible in any font and
    # far louder than a subtle ✅/⚠️/❌ (which render as tofu over plain
    # ssh/tmux without an emoji font).  All three badges are 6 columns wide
    # so the messages stay aligned.
    def ok(msg: str):
        nonlocal passed
        passed += 1
        console.print(rf"  [bold green]\[PASS][/bold green] {msg}")

    def fail(msg: str, note: str = ""):
        nonlocal failed
        failed += 1
        console.print(rf"  [bold white on red]\[FAIL][/bold white on red] {msg}")
        if note:
            _print_note(note)

    def warn(msg: str, note: str = ""):
        nonlocal warned
        warned += 1
        console.print(rf"  [bold black on yellow]\[WARN][/bold black on yellow] {msg}")
        if note:
            _print_note(note)

    console.print("\n[bold]YOLO Jail Check[/bold]\n")

    # Show version for debugging
    ver = _git_describe_version() or "unknown"
    console.print(f"[dim]Version: {ver}[/dim]\n")

    # --- Environment Health ---

    console.print("[bold]Container Runtime[/bold]")
    detected_runtime = None
    # Each entry: (name, version_cmd, liveness_cmd, liveness_hint)
    # Apple Container's daemon check is `container system status`, not
    # `container info` — the latter returns usage text even without a
    # running apiserver.
    runtime_probes = [
        (
            "podman",
            ["podman", "--version"],
            ["podman", "info"],
            "Run 'podman info' to diagnose",
        ),
        (
            "container",
            ["container", "--version"],
            ["container", "system", "status"],
            "Start with: container system start",
        ),
    ]
    # Only warn about an offline runtime if the user explicitly selected
    # it (YOLO_RUNTIME).  The merged-config runtime pick happens later
    # and emits its own error via ``_runtime_for_check``.
    selected_runtime = os.environ.get("YOLO_RUNTIME")
    if selected_runtime not in SUPPORTED_RUNTIMES:
        selected_runtime = None
    # First pass: collect probe results so we know whether anything is
    # live before deciding severity on the rest.  ``offline`` = installed
    # but its daemon/VM isn't running (fixable with a start command);
    # tracked separately from "not installed at all" so the final guidance
    # doesn't tell you to INSTALL something that's already installed.
    offline: list[tuple[str, str, str]] = []  # (rt, version, start_hint)
    for rt, version_cmd, liveness_cmd, liveness_hint in runtime_probes:
        path = shutil.which(rt)
        if not path:
            continue
        try:
            result = subprocess.run(
                version_cmd, capture_output=True, text=True, timeout=5
            )
            version = result.stdout.strip().split("\n")[0]
            # Verify the daemon/apiserver is actually reachable, not just the CLI
            ping = subprocess.run(
                liveness_cmd, capture_output=True, text=True, timeout=10
            )
            ping_ok = ping.returncode == 0
            if rt == "container" and ping_ok:
                # `container system status` succeeds even when the apiserver
                # is stopped — the real signal is "running" in stdout.
                ping_ok = "running" in ping.stdout.lower()
            if ping_ok:
                ok(f"{rt}: {version}")
                if detected_runtime is None:
                    detected_runtime = rt
            else:
                offline.append((rt, version, liveness_hint))
        except Exception as e:
            fail(f"{rt} found but not working: {e}")
    # Grade the offline runtimes after all probes finish.  If the user
    # explicitly selected one and it's offline, that's a real problem.
    # If another runtime is live, dormant siblings are just clutter —
    # print them as dim info so the signal is there without a warning.
    for rt, version, hint in offline:
        if rt == selected_runtime or detected_runtime is None:
            warn(f"{rt}: {version} (not connected)", hint)
        else:
            console.print(
                f"  [dim]- {rt}: {version} (not connected, not selected)[/dim]"
            )
    # Final verdict when nothing is live.  Distinguish "installed but not
    # started" (just start it — the common case that misleadingly read as
    # "install something" before) from "genuinely nothing installed".
    if detected_runtime is None:
        if offline:
            # At least one runtime is installed; it just needs starting.
            names = ", ".join(rt for rt, _, _ in offline)
            starts = "; ".join(f"{rt}: {hint}" for rt, _, hint in offline)
            fail(
                f"Container runtime installed but not started ({names})",
                f"It's installed — you just need to START it.\n{starts}",
            )
        else:
            fail(
                "No container runtime installed",
                "Install one:\n"
                "  Linux:  your package manager, e.g. `sudo apt install podman`\n"
                "  macOS:  `brew install podman` then `podman machine init "
                "&& podman machine start`,\n"
                "          or `brew install container` then `container system start`\n"
                '  (or use the native macOS backend: runtime "macos-user", '
                "no container needed — see docs/macos.md)",
            )
    console.print()

    console.print("[bold]Nix[/bold]")
    nix_path = shutil.which("nix")
    if nix_path:
        try:
            result = subprocess.run(
                ["nix", "--version"],
                capture_output=True,
                text=True,
                timeout=5,
            )
            ok(f"nix: {result.stdout.strip()}")
        except Exception as e:
            fail(f"nix found but not working: {e}")
    else:
        fail("nix not found", "Install Nix: https://nixos.org/download/")

    if IS_MACOS and nix_path:
        # Nix daemon store connectivity (catches determinate-nixd trust bug)
        try:
            result = subprocess.run(
                ["nix", "store", "info"],
                capture_output=True,
                text=True,
                timeout=15,
            )
            # nix store info writes its output to stderr (not stdout)
            output = result.stdout + result.stderr
            if result.returncode == 0 and "Trusted: 1" in output:
                ok("Nix daemon: connected, user is trusted")
            elif result.returncode == 0:
                # On macOS with Determinate Nix, untrusted users can still
                # build images via the binary cache (no local Linux builder
                # needed). Demote to a warning rather than a hard failure.
                included = _nix_custom_conf_included()
                label = _detect_nix_daemon_label() or "<label>"
                restart = f"sudo launchctl kickstart -k system/{label}"
                if included is False:
                    # nix.conf exists but has no include — the typical
                    # official-NixOS-installer layout.  Writing to
                    # nix.custom.conf alone won't do anything.
                    hint = (
                        "/etc/nix/nix.conf does not include nix.custom.conf. "
                        "Either add it to the trusted-users line directly in "
                        "/etc/nix/nix.conf, or add an include line once: "
                        "echo '!include /etc/nix/nix.custom.conf' | "
                        "sudo tee -a /etc/nix/nix.conf. Then add your user "
                        "(trusted-users = root $(whoami)) and restart the "
                        f"daemon: {restart}"
                    )
                else:
                    # Determinate Systems layout (or unknown) — the
                    # existing custom.conf advice is correct.
                    hint = (
                        "Add your user to trusted-users in "
                        "/etc/nix/nix.custom.conf and restart the Nix daemon: "
                        f"{restart}"
                    )
                warn("Nix daemon: connected but user is NOT trusted", hint)
            else:
                fail(
                    "Nix daemon: connection failed",
                    result.stderr.strip().split("\n")[0] if result.stderr else "",
                )
        except subprocess.TimeoutExpired:
            label = _detect_nix_daemon_label()
            kickstart = (
                f"sudo launchctl kickstart -k system/{label}"
                if label
                else "sudo launchctl kickstart -k system/<label>"
                " — check ls /Library/LaunchDaemons/ for your *nix-daemon.plist"
            )
            fail(
                "Nix daemon: store operation timed out (daemon may be hung)",
                "This is a known issue with determinate-nixd. "
                f"Try: {kickstart} or switch to the vanilla nix-daemon",
            )
        except Exception as e:
            warn(f"Could not verify Nix daemon connectivity: {e}")

        # Linux builder: only surface the presence of one (positive) and the
        # extra-platforms footgun here.  The "is a builder actually NEEDED?"
        # verdict is owned by the Image Build preflight below, which knows —
        # via a nix dry-run — whether anything will be built from source, so
        # we don't cry wolf about a missing builder that isn't needed.
        try:
            cfg_result = subprocess.run(
                ["nix", "config", "show"],
                capture_output=True,
                text=True,
                timeout=10,
            )
            if cfg_result.returncode == 0:
                for line in cfg_result.stdout.split("\n"):
                    if line.startswith("extra-platforms =") and "linux" in line:
                        warn(
                            "extra-platforms includes linux — local Linux builds "
                            "will be attempted and fail",
                            "Remove 'aarch64-linux' from extra-platforms in your "
                            "nix config; use the nix-darwin linux-builder instead "
                            "(see docs/macos.md).",
                        )
            if _has_linux_builder():
                ok("Linux builder configured")
        except Exception:
            pass
    console.print()

    if IS_MACOS:
        console.print("[bold]macOS Platform[/bold]")
        ok(f"Architecture: {platform.machine()}")

        # Container VM backend check.  The supported macOS runtimes are
        # Podman Machine and Apple Container (below); Colima is not a
        # supported runtime, so it isn't probed.
        if shutil.which("podman"):
            try:
                result = subprocess.run(
                    ["podman", "machine", "info"],
                    capture_output=True,
                    text=True,
                    timeout=5,
                )
                if result.returncode == 0:
                    ok("Podman Machine: available")
                    _check_podman_machine_resources(workspace, ok=ok, warn=warn)
                else:
                    warn("Podman Machine: not configured")
            except Exception as e:
                warn(f"podman: {e}")

        # Apple Container CLI check (native macOS container runtime)
        container_path = shutil.which("container")
        if container_path:
            try:
                result = subprocess.run(
                    ["container", "system", "status"],
                    capture_output=True,
                    text=True,
                    timeout=5,
                )
                if result.returncode == 0:
                    ok("Apple Container CLI: available")
                    if "running" in result.stdout.lower():
                        ok("Apple Container system: running")
                    else:
                        warn(
                            "Apple Container system not running",
                            "Start with: container system start",
                        )
                else:
                    warn(
                        "Apple Container: installed but not started",
                        "Start with: container system start",
                    )
            except Exception as e:
                warn(f"Apple Container CLI: {e}")

        # OCI conversion tool check (for Apple Container image loading)
        if container_path:
            if shutil.which("skopeo"):
                ok("skopeo: available (OCI image conversion, no daemon needed)")
            elif shutil.which("podman"):
                ok(
                    "OCI conversion: via podman (skopeo recommended: brew install skopeo)"
                )
            else:
                warn(
                    "No OCI conversion tool for Apple Container",
                    "Install skopeo (recommended): brew install skopeo",
                )

        # Nix store volume check
        nix_mount = Path("/nix")
        if nix_mount.exists():
            try:
                result = subprocess.run(
                    ["mount"],
                    capture_output=True,
                    text=True,
                    timeout=5,
                )
                nix_line = [
                    line
                    for line in result.stdout.split("\n")
                    if " /nix " in line or " on /nix" in line
                ]
                if nix_line:
                    if "apfs" in nix_line[0].lower():
                        ok("Nix store: mounted (APFS volume)")
                    else:
                        ok("Nix store: mounted")
                else:
                    warn(
                        "Nix store: /nix exists but mount not detected",
                        "Check /etc/synthetic.conf and Disk Utility",
                    )
            except Exception:
                ok("Nix store: /nix exists")
        else:
            fail(
                "Nix store: /nix not found",
                "Reinstall Nix or check /etc/synthetic.conf",
            )

        console.print()

    console.print("[bold]Global Storage[/bold]")
    for name, storage_path in [
        ("Home", GLOBAL_HOME),
        ("Mise (jail store)", GLOBAL_MISE),
        ("Containers", CONTAINER_DIR),
        ("Agents", AGENTS_DIR),
        ("Build", BUILD_DIR),
    ]:
        if storage_path.exists():
            ok(f"{name}: {storage_path}")
        else:
            warn(
                f"{name} directory missing: {storage_path}",
                "Will be created on first run",
            )
    console.print()

    # --- Config Validation ---

    console.print("[bold]Config Files[/bold]")
    try:
        user_config = _load_jsonc_with_includes(
            USER_CONFIG_PATH, str(USER_CONFIG_PATH), strict=True
        )
        if USER_CONFIG_PATH.exists():
            ok(f"Parsed user config: {USER_CONFIG_PATH}")
        else:
            ok(f"No user config found: {USER_CONFIG_PATH}")
    except ConfigError as e:
        user_config = {}
        fail(str(e))

    workspace_config_path = workspace / "yolo-jail.jsonc"
    try:
        workspace_config = _load_jsonc_with_includes(
            workspace_config_path, "yolo-jail.jsonc", strict=True
        )
        if workspace_config_path.exists():
            ok(f"Parsed workspace config: {workspace_config_path}")
        else:
            ok("No workspace yolo-jail.jsonc found")
    except ConfigError as e:
        workspace_config = {}
        fail(str(e))
    console.print()

    if failed:
        console.print("[bold]Summary[/bold]")
        console.print(f"  [red]{failed} failed[/red]\n")
        raise typer.Exit(1)

    config = merge_config(user_config, workspace_config)
    repo_root: Optional[Path] = None
    try:
        repo_root = _resolve_repo_root()
        flake = repo_root / "flake.nix"
        if flake.exists():
            ok(f"flake.nix found: {flake}")
        else:
            warn(f"flake.nix not found at {flake}")
    except SystemExit:
        fail("Could not resolve the yolo-jail repo root")

    console.print("[bold]Merged Configuration[/bold]")
    errors, warnings = _validate_config(config, workspace=workspace)
    runtime, runtime_error = _runtime_for_check(config)
    if runtime_error:
        errors.append(runtime_error)
    elif runtime:
        ok(f"Runtime available: {runtime}")
    # Native backends (macos-user) build no image and run no container, so
    # the image-build + container-image + per-jail-container-liveness probes
    # below are irrelevant — skip them rather than FAIL on a Linux builder
    # or WARN about a container CLI the user isn't using.
    is_native_runtime = runtime in NATIVE_RUNTIMES

    if workspace_config_path.exists() and "repo_path" in workspace_config:
        warnings.append(
            "config.repo_path: workspace repo_path is ignored; only the user config uses it"
        )

    # Check individual config files for same-file preset+null contradictions.
    # Cross-hierarchy overrides are valid; same-file contradictions are errors.
    for label, cfg in [
        (str(USER_CONFIG_PATH), user_config),
        ("yolo-jail.jsonc", workspace_config),
    ]:
        errors.extend(_check_preset_null_conflicts(cfg, label))

    for message in warnings:
        warn(message)
    if errors:
        for message in errors:
            fail(message)
        console.print()
        console.print("[bold]Summary[/bold]")
        parts = [f"[red]{failed} failed[/red]"]
        if warned:
            parts.append(f"[yellow]{warned} warnings[/yellow]")
        console.print(f"  {', '.join(parts)}\n")
        raise typer.Exit(1)
    ok("Merged config is semantically valid")
    console.print()

    # --- Entrypoint Dry-Run ---

    console.print("[bold]Entrypoint Dry-Run[/bold]")
    try:
        if repo_root is None:
            raise ConfigError("repo root resolution failed")
        if not (repo_root / "src" / "entrypoint" / "__init__.py").exists():
            raise ConfigError(f"entrypoint source not found under {repo_root}")
        _entrypoint_preflight(repo_root, workspace, config)
        ok("Generated Copilot/Gemini/Claude jail config in a temp home")
    except (ConfigError, SystemExit) as e:
        fail("Entrypoint preflight failed", str(e))
    console.print()

    # --- macOS-user backend readiness ---
    #
    # Only when the native backend is the resolved runtime.  It needs macOS,
    # Apple Seatbelt, and a provisioned sandbox account — probe each and give
    # actionable fixes.  Host-side only (never meaningful inside a jail).
    if runtime == "macos-user":
        _check_macos_user_backend(ok, warn, fail)
        console.print()

    # --- GPU Checks ---

    gpu_config = config.get("gpu", {})
    gpu_vendor = gpu_config.get("vendor", "nvidia")
    if gpu_config.get("enabled", False) and gpu_vendor != "amd":
        console.print("[bold]GPU (NVIDIA)[/bold]")
        if IS_MACOS:
            warn(
                "GPU passthrough is not supported on macOS",
                "NVIDIA GPU passthrough requires Linux with NVIDIA drivers",
            )
            console.print()
        else:
            # Check nvidia-smi
            nvidia_smi = shutil.which("nvidia-smi")
            if nvidia_smi:
                try:
                    result = subprocess.run(
                        [
                            "nvidia-smi",
                            "--query-gpu=name,driver_version",
                            "--format=csv,noheader",
                        ],
                        capture_output=True,
                        text=True,
                        timeout=10,
                    )
                    if result.returncode == 0 and result.stdout.strip():
                        for line in result.stdout.strip().split("\n"):
                            ok(f"GPU detected: {line.strip()}")
                    else:
                        fail(
                            "nvidia-smi found but no GPUs detected",
                            "Check NVIDIA driver installation",
                        )
                except Exception as e:
                    fail("nvidia-smi execution failed", str(e))
            else:
                fail(
                    "nvidia-smi not found",
                    "Install NVIDIA drivers: https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/install-nvidia-driver.html",
                )

            # Check nvidia-ctk
            nvidia_ctk = shutil.which("nvidia-ctk")
            if nvidia_ctk:
                ok("nvidia-ctk found (NVIDIA Container Toolkit)")
            else:
                fail(
                    "nvidia-ctk not found",
                    "Install: https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html",
                )

            # Runtime-specific checks
            effective_runtime, _ = _runtime_for_check(config)
            if effective_runtime == "podman":
                # GPU+Podman requires runc (CDI device injection fails with crun,
                # see https://github.com/containers/podman/issues/27483)
                runc_path = shutil.which("runc")
                if runc_path:
                    ok("runc found (required for Podman GPU passthrough)")
                else:
                    fail(
                        "runc not found",
                        "GPU passthrough requires runc (CDI fails with crun). "
                        "Install runc: https://github.com/opencontainers/runc/releases",
                    )

                # Check CDI spec exists
                cdi_paths = [
                    Path("/etc/cdi/nvidia.yaml"),
                    Path("/var/run/cdi/nvidia.yaml"),
                ]
                cdi_found = None
                for p in cdi_paths:
                    if p.exists():
                        cdi_found = p
                        break
                if cdi_found:
                    ok("CDI spec found for Podman GPU support")
                    # Check CDI spec driver version matches installed driver
                    try:
                        cdi_text = cdi_found.read_text()
                        # nvidia-smi driver version from earlier check
                        smi_result = subprocess.run(
                            [
                                "nvidia-smi",
                                "--query-gpu=driver_version",
                                "--format=csv,noheader",
                            ],
                            capture_output=True,
                            text=True,
                            timeout=10,
                        )
                        if smi_result.returncode == 0:
                            smi_driver = (
                                smi_result.stdout.strip().split("\n")[0].strip()
                            )
                            if smi_driver and smi_driver in cdi_text:
                                ok(f"CDI spec matches driver {smi_driver}")
                            elif smi_driver:
                                warn(
                                    f"CDI spec may be stale (driver is {smi_driver})",
                                    "Regenerate: sudo nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml",
                                )
                    except Exception:
                        pass  # Non-critical check
                else:
                    fail(
                        "No CDI spec found for Podman",
                        "Generate with: sudo nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml",
                    )
            console.print()

    # --- GPU Checks (AMD / ROCm) ---
    #
    # Parallel to the NVIDIA block above, gated on vendor == "amd".  ROCm
    # passthrough is the device-node path (no host toolkit): /dev/kfd plus
    # one or more /dev/dri/renderD* render nodes, with podman preserving
    # the owning group via --group-add keep-groups.  We reuse the KVM
    # block's device-node + group-membership idiom (os.access /
    # grp.getgrgid / os.getgroups) and skip host-state probes inside a
    # jail (YOLO_VERSION set).  Verified on real AMD hardware (Radeon
    # 8060S / gfx1151, ROCm 7.2): rocminfo enumeration + amd-ctk CDI.

    if gpu_config.get("enabled", False) and gpu_vendor == "amd":
        console.print("[bold]GPU (AMD/ROCm)[/bold]")
        if IS_MACOS:
            warn(
                "ROCm passthrough is not supported on macOS",
                "AMD ROCm GPU passthrough requires Linux with the amdgpu driver",
            )
            console.print()
        else:
            in_jail = os.environ.get("YOLO_VERSION") is not None

            # Functional enumeration via rocminfo (the AMD analog of
            # `nvidia-smi -L`).  rocminfo ignores argv, so no flags.
            # rocm-smi / amd-smi are secondary signals.
            rocminfo = shutil.which("rocminfo")
            if rocminfo:
                try:
                    result = subprocess.run(
                        ["rocminfo"],
                        capture_output=True,
                        text=True,
                        timeout=10,
                    )
                    if result.returncode == 0 and result.stdout.strip():
                        # rocminfo lists every HSA agent — CPU and (on APUs)
                        # the NPU/DSP too — so only report agents whose
                        # "Device Type:" is GPU.  "Marketing Name:" precedes
                        # "Device Type:" within each agent block.
                        found_gpu = False
                        pending_name = None
                        for line in result.stdout.splitlines():
                            if "Marketing Name:" in line:
                                pending_name = line.split("Marketing Name:", 1)[
                                    1
                                ].strip()
                            elif "Device Type:" in line:
                                dev_type = line.split("Device Type:", 1)[1].strip()
                                if dev_type == "GPU" and pending_name:
                                    ok(f"GPU detected: {pending_name}")
                                    found_gpu = True
                                pending_name = None
                        if not found_gpu:
                            warn(
                                "rocminfo ran but enumerated no GPU agent",
                                "Check the amdgpu driver and that the GPU is "
                                "ROCm-supported",
                            )
                    else:
                        fail(
                            "rocminfo found but reported no GPUs",
                            "Check amdgpu driver installation",
                        )
                except Exception as e:
                    fail("rocminfo execution failed", str(e))
            else:
                rocm_smi = shutil.which("rocm-smi") or shutil.which("amd-smi")
                if rocm_smi:
                    warn(
                        "rocminfo not found (rocm-smi/amd-smi present)",
                        "Install full ROCm for enumeration: "
                        "https://rocm.docs.amd.com/projects/install-on-linux/",
                    )
                else:
                    fail(
                        "rocminfo not found",
                        "Install ROCm: "
                        "https://rocm.docs.amd.com/projects/install-on-linux/",
                    )

            # amdgpu kernel module — host state, skipped inside a jail.
            if in_jail:
                ok("Inside jail — amdgpu module check skipped (managed by host)")
            elif Path("/sys/module/amdgpu").exists():
                ok("amdgpu kernel module loaded")
            else:
                fail(
                    "amdgpu kernel module not loaded",
                    "Install amdgpu-dkms and reboot, or `modprobe amdgpu`",
                )

            # Device nodes + group membership.  /dev/kfd is the shared
            # compute interface (always required); each /dev/dri/renderD*
            # is a per-GPU render node.  Both are typically root:render.
            # Reuses the KVM block's os.access / grp.getgrgid / os.getgroups
            # idiom.  Host device state isn't meaningful inside a jail.
            if in_jail:
                ok("Inside jail — device-node checks skipped (managed by host)")
            else:
                import grp

                def _check_node(node: Path):
                    if not node.exists():
                        fail(f"{node} not present")
                        return
                    ok(f"Device node: {node}")
                    # The actual gate is open(), not the file mode.
                    if os.access(node, os.R_OK | os.W_OK):
                        ok(f"{node} is readable and writable by the current user")
                        return
                    try:
                        node_gid = node.stat().st_gid
                        try:
                            node_group_name = grp.getgrgid(node_gid).gr_name
                        except KeyError:
                            node_group_name = str(node_gid)
                        if node_gid in set(os.getgroups()):
                            # Group membership is correct but access still
                            # fails — usually the session hasn't picked up
                            # the new group yet.
                            warn(
                                f"User is in group '{node_group_name}' but "
                                f"{node} is not accessible from this process",
                                f"Log out and back in (or `newgrp {node_group_name}`) "
                                "so the new group takes effect",
                            )
                        else:
                            fail(
                                f"{node} not accessible; user missing group "
                                f"'{node_group_name}'",
                                f"sudo usermod -aG {node_group_name} $USER && "
                                "log out / log back in",
                            )
                    except OSError as e:
                        fail(f"Could not stat {node}: {e}")

                _check_node(Path("/dev/kfd"))
                render_nodes = sorted(Path("/dev/dri").glob("renderD*"))
                if render_nodes:
                    for node in render_nodes:
                        _check_node(node)
                else:
                    fail(
                        "no /dev/dri render node present",
                        "ROCm needs at least one /dev/dri/renderD* node",
                    )

            # Rootless podman drops supplementary groups; keep-groups
            # (crun-only) preserves the host render/video GID.  We add this
            # flag automatically in run(); here we just confirm the runtime
            # supports it.  AMD stays on crun (not the NVIDIA runc branch).
            effective_runtime_rocm, _ = _runtime_for_check(config)
            if effective_runtime_rocm == "podman":
                ok(
                    "Podman will preserve render/video group via "
                    "--group-add keep-groups"
                )
            elif effective_runtime_rocm == "container":
                warn(
                    "Apple Container does not support device passthrough",
                    "ROCm passthrough will be ignored on the 'container' runtime",
                )

            # mode: "cdi" only — the AMD Container Toolkit CDI spec.  The
            # default device-node mode needs no CDI spec, so only check
            # when the user opted into CDI.  (CDI mode verified on hardware:
            # amd-ctk-generated /etc/cdi/amd.json runs ROCm under crun.)
            if gpu_config.get("mode") == "cdi":
                cdi_paths = [
                    Path("/etc/cdi/amd.json"),
                    Path("/var/run/cdi/amd.json"),
                ]
                cdi_found = None
                for p in cdi_paths:
                    if p.exists():
                        cdi_found = p
                        break
                if cdi_found:
                    ok(f"AMD CDI spec found: {cdi_found}")
                    # No auto-refresh service exists for AMD CDI; regenerate
                    # manually after driver/GPU changes (amd-ctk cdi validate).
                else:
                    fail(
                        "No AMD CDI spec found (mode: cdi)",
                        "Generate with: sudo amd-ctk cdi generate "
                        "--output=/etc/cdi/amd.json",
                    )
            console.print()

    # --- KVM Checks ---
    #
    # Only runs when the user has opted in via `kvm: true`.  Never runs
    # inside a jail (the host's /dev/kvm state isn't meaningfully visible
    # from inside a container that wasn't started with passthrough).

    if config.get("kvm") is True:
        console.print("[bold]KVM Virtualization[/bold]")
        if os.environ.get("YOLO_VERSION") is not None:
            ok("Inside jail — kvm checks skipped (managed by host)")
        elif IS_MACOS:
            warn(
                "kvm passthrough is not supported on macOS",
                "Apple hosts use the VZ framework; drop the `kvm` key on mac",
            )
        else:
            kvm_path = Path("/dev/kvm")
            if not kvm_path.exists():
                fail(
                    "/dev/kvm not present",
                    "Enable virtualization in firmware and `modprobe kvm_intel` "
                    "or `modprobe kvm_amd`",
                )
            else:
                ok(f"Device node: {kvm_path}")
                # Can the current user open /dev/kvm for read+write?
                # This is the actual gate — not the file mode.
                if os.access(kvm_path, os.R_OK | os.W_OK):
                    ok("/dev/kvm is readable and writable by the current user")
                else:
                    try:
                        st = kvm_path.stat()
                        kvm_gid = st.st_gid
                        import grp

                        try:
                            kvm_group_name = grp.getgrgid(kvm_gid).gr_name
                        except KeyError:
                            kvm_group_name = str(kvm_gid)
                        user_groups = set(os.getgroups())
                        if kvm_gid in user_groups:
                            # Group membership is correct, but access still
                            # fails — almost always means the login session
                            # hasn't picked up the new group yet.
                            warn(
                                f"User is in group '{kvm_group_name}' but "
                                "/dev/kvm is not accessible from this process",
                                "Log out and back in (or `newgrp kvm`) so the "
                                "new group takes effect",
                            )
                        else:
                            fail(
                                f"/dev/kvm not accessible; user missing group '{kvm_group_name}'",
                                f"sudo usermod -aG {kvm_group_name} $USER && "
                                "log out / log back in",
                            )
                    except OSError as e:
                        fail(f"Could not stat /dev/kvm: {e}")

                # Podman rootless needs --group-add keep-groups to honor
                # supplementary groups inside the user namespace.  We add
                # this flag automatically in run(); here we just confirm
                # the runtime is one that supports it.
                effective_runtime_kvm, _ = _runtime_for_check(config)
                if effective_runtime_kvm == "podman":
                    ok("Podman will preserve kvm group via --group-add keep-groups")
                elif effective_runtime_kvm == "container":
                    warn(
                        "Apple Container does not support device passthrough",
                        "kvm: true will be ignored on the 'container' runtime",
                    )
        console.print()

    # --- Image & Containers ---
    #
    # Native backends (macos-user) build no image and run no container, so
    # the whole image/container region is skipped with a single info line —
    # no builder FAIL, no image-not-loaded WARN, no container-CLI probes.
    image_build_skipped = False
    _not_loaded_hint = "Run 'yolo' once to build and load the image"
    if is_native_runtime:
        console.print("[bold]Image Build[/bold]")
        console.print(
            f"  [dim]- Not applicable: the '{runtime}' backend runs the agent "
            "natively (no container image to build or load).[/dim]"
        )
        console.print()
    else:
        console.print("[bold]Image Build[/bold]")
        if build:
            out_link = BUILD_DIR / "check-result"
            if repo_root is None:
                fail("Skipped nix build", "repo root resolution failed")
            else:
                # Preflight: will anything be BUILT from source (cache miss), or
                # is the whole image substitutable?  Only a from-source build
                # needs a Linux builder on macOS — stay quiet in the common
                # (fully-cached) case and escalate only on a real miss.  Gated
                # on --build (dry-run costs cache round-trips); INCONCLUSIVE
                # when offline so we never cry wolf.  When it returns False the
                # build is known-doomed (needs a builder we don't have) — skip
                # the real build so the user sees ONE clear message, not a WARN
                # + a duplicate FAIL, and no misleading "Run 'yolo' once" hint.
                image_buildable = _preflight_builder_needs(
                    repo_root, _effective_packages(config) or None, ok, warn, fail
                )
                if image_buildable is False:
                    image_build_skipped = True
                else:
                    try:
                        store_path, build_stderr_tail = _build_image_store_path(
                            repo_root,
                            extra_packages=_effective_packages(config) or None,
                            out_link=out_link,
                            status_message="[bold blue]Preflighting jail image...",
                        )
                        if store_path is None:
                            title, note = _diagnose_nix_build_failure(build_stderr_tail)
                            fail(title, note)
                        else:
                            ok(f"nix build succeeded: {store_path}")
                    finally:
                        out_link.unlink(missing_ok=True)
        else:
            warn("Skipped nix build (--no-build)")
        console.print()

        # When the image can't be built here (no Linux builder), the "Image
        # not loaded → Run 'yolo' once" hint below is misleading — running
        # yolo hits the same wall.  Point at the real fix instead.
        if image_build_skipped:
            _not_loaded_hint = (
                "This host can't build the image (needs a Linux builder — see "
                "above / docs/macos.md), or download a prebuilt image once the "
                "cache is published."
            )

    if not is_native_runtime and detected_runtime:
        console.print("[bold]Container Image[/bold]")
        # Skip image check when running inside a jail — the nested podman
        # won't have the image loaded (it's on the host's runtime).
        in_jail = os.environ.get("YOLO_VERSION") is not None
        if in_jail:
            ok("Inside jail — image check skipped (managed by host)")
        else:
            check_image = _jail_image(detected_runtime)
            try:
                if detected_runtime == "container":
                    result = subprocess.run(
                        ["container", "image", "inspect", check_image],
                        capture_output=True,
                        text=True,
                        timeout=10,
                    )
                    if result.returncode == 0:
                        ok(f"Image loaded: {check_image}")
                    else:
                        warn(
                            f"Image '{check_image}' not loaded",
                            _not_loaded_hint,
                        )
                else:
                    result = subprocess.run(
                        [
                            detected_runtime,
                            "images",
                            check_image,
                            "--format",
                            "{{.Repository}}:{{.Tag}} ({{.Size}})",
                        ],
                        capture_output=True,
                        text=True,
                        timeout=10,
                    )
                    images = result.stdout.strip()
                    if images:
                        ok(f"Image loaded: {images.split(chr(10))[0]}")
                    else:
                        warn(
                            f"Image '{check_image}' not loaded",
                            _not_loaded_hint,
                        )
            except Exception as e:
                warn(f"Could not check image: {e}")
        console.print()

        console.print("[bold]Running Jails[/bold]")
        try:
            if detected_runtime == "container":
                result = subprocess.run(
                    ["container", "ls", "--filter", "name=yolo-"],
                    capture_output=True,
                    text=True,
                    timeout=5,
                )
                # Parse Apple container ls table output
                containers = []
                for line in result.stdout.strip().splitlines()[1:]:  # skip header
                    parts = line.split()
                    if parts:
                        cname = parts[0]
                        if cname.startswith("yolo-"):
                            containers.append(f"{cname}\t")
            else:
                result = subprocess.run(
                    [
                        detected_runtime,
                        "ps",
                        "--filter",
                        "name=^yolo-",
                        "--format",
                        "{{.Names}}\t{{.RunningFor}}",
                    ],
                    capture_output=True,
                    text=True,
                    timeout=5,
                )
                containers = [c for c in result.stdout.strip().split("\n") if c]
            if containers:
                orphaned_jails = []
                ok(f"{len(containers)} jail(s) running")
                for line in containers:
                    parts = line.split("\t")
                    cname = parts[0]
                    running_for = parts[1] if len(parts) > 1 else ""
                    container_workspace = _get_container_workspace(
                        cname, detected_runtime
                    )
                    ws_exists = (
                        Path(container_workspace).is_dir()
                        if container_workspace != "unknown"
                        else True
                    )
                    reason = None
                    if not ws_exists:
                        reason = "workspace gone"
                    else:
                        reason = _check_container_stuck(cname, detected_runtime)
                    if reason:
                        marker = f" [red]({reason})[/red]"
                        orphaned_jails.append(
                            (cname, running_for, container_workspace, reason)
                        )
                    else:
                        marker = ""
                    console.print(f"    {cname} -> {container_workspace}{marker}")
                if orphaned_jails:
                    warn(
                        f"{len(orphaned_jails)} orphaned jail(s)",
                        "These containers are stuck or have lost their workspace",
                    )
                    console.print()
                    answer = console.input(
                        f"  [bold yellow]Stop {len(orphaned_jails)} orphaned jail(s)? [y/N][/bold yellow] "
                    )
                    if answer.strip().lower() in ("y", "yes"):
                        for cname, _, _, _ in orphaned_jails:
                            subprocess.run(
                                [detected_runtime, "rm", "-f", cname],
                                capture_output=True,
                            )
                            cleanup_container_tracking(cname)
                            console.print(f"    [green]Stopped {cname}[/green]")
            else:
                ok("No jails currently running")
        except Exception:
            warn("Could not check running containers")
        console.print()

    # --- Host-side loopholes ---

    console.print("[bold]Loopholes[/bold]")
    _check_loopholes(ok, warn, fail)
    console.print()

    # --- Per-jail host-service liveness ---
    #
    # Loophole self-checks are static (binary present, config parses).
    # They don't catch the case where the per-jail daemon was spawned
    # but immediately crashed.  This probe connects to each running
    # jail's host-service socket and reports any that aren't listening.
    # It enumerates *containers*, so it's skipped for native backends (no
    # containers to probe — this is where the stale "Apple Container CLI
    # too old" WARN came from on a macos-user host).
    if not is_native_runtime:
        console.print("[bold]Per-jail host-service liveness[/bold]")
        _check_host_service_liveness(ok, warn, fail)
        console.print()

    # --- Disk usage (nudges toward `yolo prune` when large) ---

    console.print("[bold]Disk usage[/bold]")
    _check_disk_usage(ok, warn, fail, config=config)
    console.print()

    # --- Loopholes (config-inline daemons) ---

    loopholes_cfg = config.get("loopholes") or {}
    if loopholes_cfg:
        console.print("[bold]Loopholes — inline daemons[/bold]")
        if _loophole_exec_checks_skipped_in_jail():
            ok("Inside jail — exec checks skipped (host paths aren't reachable here)")
        else:
            for name, spec in loopholes_cfg.items():
                if name == BUILTIN_CGROUP_LOOPHOLE_NAME:
                    continue  # builtin is unconditional, not user-configurable
                if not isinstance(spec, dict):
                    continue
                cmd = spec.get("command") or []
                if not isinstance(cmd, list) or not cmd:
                    # No command — an override of a file-backed loophole
                    # (enabled/env/jail_env only), so there is nothing to
                    # exec-check.  A genuinely inline service missing its
                    # command is already reported by the merged-config
                    # validation above.
                    continue
                # Resolve the command's executable.  Allow ~ expansion and PATH lookup.
                exe_arg = str(cmd[0])
                exe_path = Path(exe_arg).expanduser()
                if exe_path.is_absolute():
                    if exe_path.is_file() and os.access(exe_path, os.X_OK):
                        ok(f"loopholes.{name}: {exe_path}")
                    else:
                        fail(
                            f"loopholes.{name}: command not found or not executable: {exe_path}"
                        )
                else:
                    resolved = shutil.which(exe_arg)
                    if resolved:
                        ok(f"loopholes.{name}: {resolved}")
                    else:
                        fail(f"loopholes.{name}: command not found on PATH: {exe_arg}")
        console.print()

    # --- Summary ---

    console.print("[bold]Summary[/bold]")
    parts = [f"[green]{passed} passed[/green]"]
    if failed:
        parts.append(f"[red]{failed} failed[/red]")
    if warned:
        parts.append(f"[yellow]{warned} warnings[/yellow]")
    console.print(f"  {', '.join(parts)}\n")

    if failed:
        raise typer.Exit(1)
