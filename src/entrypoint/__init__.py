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
# MISE_DATA_DIR is set to /mise by the CLI in every jail, but is absent on
# the host.  Importing this module must never crash there — the host-side
# CLI imports it at load time (e.g. for the agent registry), and MISE_SHIMS
# is only ever consumed inside the jail, so a host fallback is inert.  Match
# the `.get`-with-default form the sibling path constants above already use.
MISE_SHIMS = (
    Path(os.environ.get("MISE_DATA_DIR") or (HOME / ".local" / "share" / "mise"))
    / "shims"
)
MCP_WRAPPERS_BIN = HOME / ".local" / "bin" / "mcp-wrappers"
BASHRC_PATH = HOME / ".bashrc"
COPILOT_DIR = HOME / ".copilot"
GEMINI_DIR = HOME / ".gemini"
GEMINI_MANAGED_MCP_PATH = GEMINI_DIR / "yolo-managed-mcp-servers.json"
CLAUDE_DIR = HOME / ".claude"
CLAUDE_MANAGED_MCP_PATH = CLAUDE_DIR / "yolo-managed-mcp-servers.json"
# opencode: global config + rules under ~/.config/opencode/; auth under
# ~/.local/share/opencode/ (both already sit in writable overlays).
OPENCODE_DIR = HOME / ".config" / "opencode"
# pi (pi.dev coding agent): config/state under ~/.pi/agent/.
PI_DIR = HOME / ".pi" / "agent"
# codex (OpenAI Codex CLI): config.toml + auth.json under ~/.codex/
# (overridable via CODEX_HOME, which we leave at its default).
CODEX_DIR = HOME / ".codex"
# Snapshot of host ~/.claude/settings.json as of the last sync — the
# baseline for the three-way host→jail settings merge (see
# agent_configs._sync_host_settings).
CLAUDE_HOST_SETTINGS_SNAPSHOT_PATH = CLAUDE_DIR / "yolo-host-synced-settings.json"
CLAUDE_SHARED_CREDENTIALS_DIR = HOME / ".claude-shared-credentials"
MISE_CONFIG_DIR = HOME / ".config" / "mise"
# Workspace mount point — fixed across all jails.  A module constant so
# tests can redirect it to a tmp dir.
WORKSPACE = Path("/workspace")

# Writable tmpfs that backs the ``/etc/localtime`` + ``/etc/timezone``
# image symlinks (root fs is mounted --read-only).  A module constant so
# tests can redirect it to a tmp dir without monkey-patching pathlib.
TZ_RUN_DIR = Path("/run")

# LSP servers are opt-in.  Workspaces that want pyright / TypeScript /
# gopls (etc.) wired into Copilot/Gemini/Claude must list them in
# ``lsp_servers`` in yolo-jail.jsonc; the bootstrap script then installs
# the matching binaries and the agent configurators reference them.
#
# Earlier versions of yolo-jail shipped pyright/ts-server/gopls as
# always-on defaults.  That cost ~30 seconds of npm + go installs on
# every fresh jail and added MCP/LSP integrations into agent configs
# regardless of whether the user wanted them.  Now: empty default,
# nothing happens unless asked, and ``configure_*`` actively unwires
# previously-configured servers when they're removed from the config.
#
# Recipes for the common languages (drop into yolo-jail.jsonc under
# ``lsp_servers``):
#
#   "python":     {"command": "$NPM_BIN/pyright-langserver",
#                  "args": ["--stdio"],
#                  "fileExtensions": {".py": "python", ".pyi": "python"}}
#   "typescript": {"command": "$NPM_BIN/typescript-language-server",
#                  "args": ["--stdio"],
#                  "fileExtensions": {".ts": "typescript", ".tsx": "typescriptreact",
#                                     ".js": "javascript", ".jsx": "javascriptreact"}}
#   "go":         {"command": "$GO_BIN/gopls", "args": [],
#                  "fileExtensions": {".go": "go"}}
DEFAULT_LSP_SERVERS: dict = {}


# Stand-alone scripts dropped into the jail's PATH live in
# entrypoint/scripts.py; git/jj identity in entrypoint/identity.py;
# timezone + CA bundle setup in entrypoint/system.py; agent (Copilot /
# Gemini / Claude) MCP+LSP wiring in entrypoint/agent_configs.py.
# Re-import so callers (and tests) keep using the bare names on the
# package.
from .agent_configs import (  # noqa: E402, F401
    CONFIG_WRITERS,
    configure_claude,
    configure_codex,
    configure_copilot,
    configure_gemini,
    configure_opencode,
    configure_pi,
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
from .system import (  # noqa: E402
    configure_timezone,
    generate_ca_bundle,
    generate_ld_cache,
)


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


def _load_agents():
    """Return the selected agent names from ``YOLO_AGENTS`` (a JSON list).

    Falls back to :data:`agent_registry.DEFAULT_AGENTS` (claude only) when
    the var is unset or unparseable — mirroring how ``_load_lsp_servers``
    tolerates a missing/garbage ``YOLO_LSP_SERVERS``.  Unknown names are
    dropped so a newer host CLI naming an agent this jail image doesn't know
    degrades gracefully instead of crashing boot.
    """
    from .agent_registry import AGENTS, DEFAULT_AGENTS

    raw = os.environ.get("YOLO_AGENTS", "")
    names = None
    if raw:
        try:
            parsed = json.loads(raw)
            if isinstance(parsed, list):
                names = [n for n in parsed if isinstance(n, str)]
        except (json.JSONDecodeError, TypeError):
            names = None
    if names is None:
        names = list(DEFAULT_AGENTS)
    return [n for n in names if n in AGENTS]


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


def trust_workspace_configs():
    """Trust the workspace's mise configs (mise.toml, .mise.toml, mise.jail.toml).

    mise trust is dir-scoped and ``--all`` covers cwd+parents only;
    MISE_TRUSTED_CONFIG_PATHS=/workspace (set by cli.py) is the blanket
    mechanism — this hook is belt-and-suspenders for configs written
    after launch.  Output discarded: --quiet still prints on some paths.
    """
    if WORKSPACE.is_dir():
        subprocess.run(
            ["mise", "trust", "--all", "--quiet"],
            cwd=WORKSPACE,
            capture_output=True,
        )


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

    # Populate /run/ld.so.cache (target of the image's /etc/ld.so.cache
    # symlink) from the /lib farm — build-time generation can't run the
    # Linux ldconfig when the image is assembled on a macOS host.
    generate_ld_cache()
    _perf("generate_ld_cache")

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
    # Configure only the selected agents (YOLO_AGENTS).  Each writer is
    # gated so an unselected agent's config dir / MCP wiring is never
    # touched — the performance + isolation win of the library model.  The
    # writer is resolved by the registry's ``config_writer`` name through
    # this module's globals (the configure_* re-exports), so it stays a
    # patchable seam for tests.
    from .agent_registry import AGENTS

    for _agent in _load_agents():
        spec = AGENTS.get(_agent)
        writer = globals().get(spec.config_writer) if spec is not None else None
        if writer is not None:
            writer()
        _perf(f"configure_{_agent}")
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

    trust_workspace_configs()
    _perf("trust_workspace_configs")

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
