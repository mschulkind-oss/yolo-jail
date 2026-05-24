#!/usr/bin/env python3
"""YOLO Jail Container Entrypoint.

Sets up the container environment (shims, configs, prompt) then exec's bash.
Uses only stdlib — runs before any pip packages are installed.
"""

import json
import os
import re
import shutil
import subprocess
import sys
import time
from pathlib import Path


# ---------------------------------------------------------------------------
# Performance logging
# ---------------------------------------------------------------------------

_PERF_LOG = []
_PERF_START = time.monotonic()


def _perf(label: str):
    """Record a performance checkpoint with elapsed time."""
    elapsed = time.monotonic() - _PERF_START
    _PERF_LOG.append((elapsed, label))


def _perf_dump():
    """Write performance log to ~/.yolo-perf.log for debugging."""
    log_path = HOME / ".yolo-perf.log"
    try:
        prev = None
        lines = [
            f"=== YOLO Jail Entrypoint Perf ({time.strftime('%Y-%m-%d %H:%M:%S')}) ===\n"
        ]
        for elapsed, label in _PERF_LOG:
            delta = f"+{elapsed - prev:.3f}s" if prev is not None else "       "
            lines.append(f"  {elapsed:7.3f}s  {delta:>9s}  {label}\n")
            prev = elapsed
        lines.append(f"  Total: {_PERF_LOG[-1][0]:.3f}s\n\n")
        # Append to log (keep last runs visible)
        with open(log_path, "a") as f:
            f.writelines(lines)
        # Trim to last 50 runs
        content = log_path.read_text()
        runs = content.split("=== YOLO")
        if len(runs) > 51:
            log_path.write_text("=== YOLO" + "=== YOLO".join(runs[-50:]))
    except Exception:
        pass


# ---------------------------------------------------------------------------
# Paths (from container env vars set by cli.py)
# ---------------------------------------------------------------------------

HOME = Path(os.environ.get("JAIL_HOME", os.environ.get("HOME", "/home/agent")))
SHIM_DIR = HOME / ".yolo-shims"
NPM_PREFIX = Path(os.environ.get("NPM_CONFIG_PREFIX", HOME / ".npm-global"))
GOPATH = Path(os.environ.get("GOPATH", HOME / "go"))
NPM_BIN = NPM_PREFIX / "bin"
GO_BIN = GOPATH / "bin"
MISE_SHIMS = Path(os.environ["MISE_DATA_DIR"]) / "shims"
MCP_WRAPPERS_BIN = HOME / ".local" / "bin" / "mcp-wrappers"
BASHRC_PATH = HOME / ".bashrc"
COPILOT_DIR = HOME / ".copilot"
GEMINI_DIR = HOME / ".gemini"
GEMINI_MANAGED_MCP_PATH = GEMINI_DIR / "yolo-managed-mcp-servers.json"
CLAUDE_DIR = HOME / ".claude"
CLAUDE_MANAGED_MCP_PATH = CLAUDE_DIR / "yolo-managed-mcp-servers.json"
CLAUDE_SHARED_CREDENTIALS_DIR = HOME / ".claude-shared-credentials"
MISE_CONFIG_DIR = HOME / ".config" / "mise"

# Writable tmpfs that backs the ``/etc/localtime`` + ``/etc/timezone``
# image symlinks (root fs is mounted --read-only).  A module constant so
# tests can redirect it to a tmp dir without monkey-patching pathlib.
TZ_RUN_DIR = Path("/run")

# Default LSP servers always available in the jail.
# command: absolute path (for Copilot); basename extracted for Gemini's mcp-language-server.
# args: passed to the LSP binary directly.
# fileExtensions: extension → language ID map (required for Copilot).
DEFAULT_LSP_SERVERS = {
    "python": {
        "command": str(NPM_BIN / "pyright-langserver"),
        "args": ["--stdio"],
        "fileExtensions": {".py": "python", ".pyi": "python"},
    },
    "typescript": {
        "command": str(NPM_BIN / "typescript-language-server"),
        "args": ["--stdio"],
        "fileExtensions": {
            ".ts": "typescript",
            ".tsx": "typescriptreact",
            ".js": "javascript",
            ".jsx": "javascriptreact",
        },
    },
    "go": {
        "command": str(GO_BIN / "gopls"),
        "args": [],
        "fileExtensions": {".go": "go"},
    },
}


# Stand-alone scripts dropped into the jail's PATH live in
# entrypoint/scripts.py; git/jj identity in entrypoint/identity.py;
# timezone + CA bundle setup in entrypoint/system.py; agent (Copilot /
# Gemini / Claude) MCP+LSP wiring in entrypoint/agent_configs.py.
# Re-import so callers (and tests) keep using the bare names on the
# package.
from .agent_configs import (  # noqa: E402
    configure_claude,
    configure_copilot,
    configure_gemini,
)
from .identity import configure_git, configure_jj  # noqa: E402
from .mcp_wrappers import generate_mcp_wrappers  # noqa: E402
from .mise import generate_mise_config  # noqa: E402
from .scripts import (  # noqa: E402
    generate_cglimit_script,
    generate_journalctl_script,
    generate_yolo_ps_script,
    generate_yolo_wrapper,
)
from .shell import (  # noqa: E402
    generate_bashrc,
    generate_bootstrap_script,
    generate_venv_precreate_script,
)
from .shims import (  # noqa: E402
    generate_agent_launchers,
    generate_package_manager_launchers,
    generate_shims,
)
from .runtime import (  # noqa: E402, F401
    FORWARD_SOCKET_DIR,
    SUPERVISOR_PID_FILE,
    _port_in_use,
    _supervisor_is_alive,
    setup_published_port_localnet,
    start_container_port_forwarding,
    start_jail_daemon_supervisor,
)
from .system import configure_timezone, generate_ca_bundle  # noqa: E402


def _load_lsp_servers():
    """Load LSP server config: defaults merged with workspace overrides from YOLO_LSP_SERVERS."""
    servers = dict(DEFAULT_LSP_SERVERS)
    extra_json = os.environ.get("YOLO_LSP_SERVERS", "")
    if extra_json:
        try:
            extra = json.loads(extra_json)
            if isinstance(extra, dict):
                servers.update(extra)
        except (json.JSONDecodeError, TypeError):
            pass
    return servers


# ---------------------------------------------------------------------------
# 11. Cgroup delegation via host-side daemon (socket client)
# ---------------------------------------------------------------------------
# The host runs a cgroup delegate daemon that listens on a Unix socket at
# /run/yolo-services/cgroup-delegate.sock.  The container-side yolo-cglimit
# sends JSON requests to create child cgroups, set limits, and move
# processes.  This avoids needing CAP_SYS_ADMIN or rw cgroup mounts inside
# the container.  All privileged cgroup operations happen on the host, with
# strict validation.
#
# The cgroup delegate is one of several host-side services that yolo-jail
# can run alongside the container.  See cli.py § "Host services" for the
# generic mechanism — user-defined services in `host_services` config also
# appear under /run/yolo-services/.

CGD_SOCKET = Path("/run/yolo-services/cgroup-delegate.sock")


def setup_cgroup_delegation():
    """Check if cgroup delegation is available via the host-side daemon.

    The host-side cgroup delegate daemon (started by cli.py) listens on a
    Unix socket mounted at /run/yolo-services/cgroup-delegate.sock.  This
    function just verifies the socket exists — all actual cgroup work is
    done by the host daemon when yolo-cglimit sends requests.

    Silent on absence: falls back to nice/timeout/ulimit in non-delegated jails.
    """
    if CGD_SOCKET.exists():
        print("  cgroup delegate: available (host-side daemon)", file=sys.stderr)
    else:
        print(
            "  cgroup delegate: not available (no host daemon socket)", file=sys.stderr
        )


# ---------------------------------------------------------------------------
# 11. User-env hydration (mirror yolo-user-env.sh into os.environ)
# ---------------------------------------------------------------------------
# The file is normally sourced by bash on shell startup (and again by
# exec_bash at the end of this entrypoint), but agent-config writers run
# earlier in main() and need its values for MCP env ${VAR} interpolation
# (see _interpolate_env in agent_configs.py).  This step parses the file
# into os.environ so the early writers see the same values bash will.
#
# Format we parse matches what cli/run_cmd.py writes — primarily the
# ``export KEY=${KEY:-'value'}`` form (existing env wins, default
# otherwise), plus the bare and quoted forms as a safety net if the
# writer format ever loosens.  The hydrator must stay in sync with the
# writer's precedence rule.

_EXPORT_RE = re.compile(
    r"""
    ^\s*export\s+
    (?P<key>[A-Za-z_][A-Za-z0-9_]*)
    =
    (?:
        \$\{[A-Za-z_][A-Za-z0-9_]*:-'(?P<def>(?:[^']|'\\'')*)'\}
      | '(?P<sq>(?:[^']|'\\'')*)'
      | "(?P<dq>[^"]*)"
      | (?P<bare>\S*)
    )
    \s*$
    """,
    re.VERBOSE,
)


def _hydrate_env_from_user_env_file():
    """Merge ``~/.config/yolo-user-env.sh`` exports into ``os.environ``.

    Mirrors the precedence used by the writer's ``${KEY:-'value'}`` form:
    if the key is already in ``os.environ`` (set on container launch),
    the file's default loses.  Unparseable lines are ignored — bash will
    still source the file at shell time as a backstop.
    """
    f = HOME / ".config" / "yolo-user-env.sh"
    if not f.exists():
        return
    for line in f.read_text().splitlines():
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        m = _EXPORT_RE.match(line)
        if not m:
            continue
        key = m.group("key")
        if key in os.environ:
            continue  # launch-time env beats the file default
        raw = (
            m.group("def")
            if m.group("def") is not None
            else m.group("sq")
            if m.group("sq") is not None
            else m.group("dq")
            if m.group("dq") is not None
            else m.group("bare") or ""
        )
        # Reverse the writer's '\'' escape for single-quoted contexts.
        os.environ[key] = raw.replace("'\\''", "'")


# ---------------------------------------------------------------------------
# 12. Finalize PATH and exec bash
# ---------------------------------------------------------------------------


def exec_bash(command: str):
    """Set up final PATH, activate mise, and exec bash with the given command."""
    local_bin = HOME / ".local" / "bin"
    path = f"{SHIM_DIR}:{NPM_BIN}:{MISE_SHIMS}:{GO_BIN}:{local_bin}:/bin:/usr/bin"
    os.environ["PATH"] = path

    # Show what we're about to run for the exec-into-existing path.
    # For new containers, cli.py already embedded "Provisioning..." and "Executing..."
    # messages in the command string.  For plain interactive shells, skip the noise.
    is_new_container_cmd = "yolo-bootstrap" in command
    if command != "bash" and not is_new_container_cmd:
        sys.stderr.write(f"\033[1;36m⚡ Executing: {command}\033[0m\n")
        sys.stderr.flush()

    # Source user-defined env vars from config (defaults, overridable by .env).
    # Then activate mise so tool paths (copilot, gemini, .venv/bin, etc.) are
    # available.  Mise env runs AFTER user-env so .env can override config vars.
    # Fresh containers get mise activation from cli.py's inline eval, but
    # exec-into-existing skips that code path.
    user_env_file = HOME / ".config" / "yolo-user-env.sh"
    source_user_env = (
        f'. "{user_env_file}" 2>/dev/null; ' if user_env_file.exists() else ""
    )
    activated_command = (
        f'{source_user_env}eval "$(mise env -s bash)" 2>/dev/null; {command}'
    )

    os.execvp(
        "bash",
        [
            "bash",
            "--rcfile",
            str(BASHRC_PATH),
            "-c",
            activated_command,
        ],
    )


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------


def main():
    cmd = " ".join(sys.argv[1:]) if len(sys.argv) > 1 else "bash"
    _perf("start")

    # Hydrate env_sources values into os.environ before any configure_*
    # so MCP env ${VAR} interpolation sees them.  bash will source the
    # same file again at shell time — this is purely the Python-side
    # mirror needed for the early agent-config writers.
    _hydrate_env_from_user_env_file()
    _perf("hydrate_user_env")

    # Populate /run/localtime + /run/timezone from $TZ before anything else
    # so file mtimes, log timestamps, etc. from subsequent setup steps use the
    # right wall-clock zone.  /etc/localtime and /etc/timezone are symlinks
    # into /run baked by the image (root fs is read-only).
    configure_timezone()
    _perf("configure_timezone")

    # Each jail writes to its own per-workspace overlay dirs (mounted by cli.py),
    # so no flock needed — no cross-jail contention.
    generate_shims()
    _perf("generate_shims")
    generate_agent_launchers()
    _perf("generate_agent_launchers")
    generate_package_manager_launchers()
    _perf("generate_package_manager_launchers")
    # Build the combined CA bundle BEFORE bashrc so bashrc can just
    # reference ``$HOME/.yolo-ca-bundle.crt`` and the env vars we set
    # in ``os.environ`` propagate to every child the entrypoint spawns
    # (jail daemon supervisor, port-forwarders, etc.) ahead of bash.
    generate_ca_bundle()
    _perf("generate_ca_bundle")
    generate_bashrc()
    _perf("generate_bashrc")
    generate_bootstrap_script()
    _perf("generate_bootstrap_script")
    generate_venv_precreate_script()
    _perf("generate_venv_precreate_script")
    generate_mise_config()
    _perf("generate_mise_config")

    # Copy host nvim config into the writable .config/ overlay.
    # In nested jails, src and dst may be the same inode (both point to the
    # shared .config overlay), so catch shutil.Error and skip silently.
    host_nvim = Path("/ctx/host-nvim-config")
    if host_nvim.is_dir():
        jail_nvim = HOME / ".config" / "nvim"
        jail_nvim.parent.mkdir(parents=True, exist_ok=True)
        try:
            shutil.copytree(
                host_nvim,
                jail_nvim,
                symlinks=False,
                ignore_dangling_symlinks=True,
                dirs_exist_ok=True,
            )
        except shutil.Error:
            pass  # already in place (nested jail, same filesystem)
    _perf("nvim_config")

    generate_mcp_wrappers()
    _perf("generate_mcp_wrappers")
    configure_git()
    _perf("configure_git")
    configure_jj()
    _perf("configure_jj")
    # Skills are mounted :ro by cli.py — no entrypoint action needed.
    _perf("skills_skipped")
    configure_copilot()
    _perf("configure_copilot")
    configure_gemini()
    _perf("configure_gemini")
    configure_claude()
    _perf("configure_claude")
    setup_cgroup_delegation()
    _perf("cgroup_delegation")
    generate_cglimit_script()
    _perf("cglimit_script")
    generate_journalctl_script()
    _perf("journalctl_script")
    generate_yolo_ps_script()
    _perf("yolo_ps_script")
    generate_yolo_wrapper()
    _perf("yolo_wrapper")

    # These are per-container (use container-local network state), not shared
    setup_published_port_localnet()
    _perf("published_port_localnet")
    start_container_port_forwarding()
    _perf("port_forwarding")

    # Start the jail-daemon supervisor if any loopholes declared a
    # ``jail_daemon``.  Runs as a child of PID 1; kernel kills it when
    # PID 1 exits so no explicit teardown is needed.
    start_jail_daemon_supervisor()
    _perf("jail_daemon_supervisor")

    # Set PATH including mise shims so tools like copilot/gemini/claude are found
    os.environ["PATH"] = f"{SHIM_DIR}:{NPM_BIN}:{MISE_SHIMS}:{GO_BIN}:/bin:/usr/bin"

    # Trust workspace mise.toml (--quiet suppresses "No untrusted config files" noise)
    if Path("/workspace/mise.toml").exists():
        subprocess.run(
            ["mise", "trust", "--quiet", "/workspace/mise.toml"],
            capture_output=True,
        )

    # NOTE: We intentionally do NOT call `mise hook-env` here.
    # hook-env holds a WRITE flock, then spawns `uv` via the mise shim
    # (which IS /bin/mise), re-entering mise's flock → deadlock.
    # Instead, cli.py's setup_script calls ~/.yolo-venv-precreate.sh (after
    # `mise install`) to create venvs with real binaries, then uses
    # `eval "$(mise env -s bash)"` for stateless env activation.

    _perf_dump()

    exec_bash(cmd)


if __name__ == "__main__":
    main()
