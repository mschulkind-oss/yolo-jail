#!/usr/bin/env python3
"""YOLO Jail Container Entrypoint.

Sets up the container environment (shims, configs, prompt) then exec's bash.
Uses only stdlib — runs before any pip packages are installed.
"""

import json
import os
import shutil
import stat
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
from .mise import generate_mise_config  # noqa: E402
from .scripts import (  # noqa: E402
    generate_cglimit_script,
    generate_journalctl_script,
    generate_yolo_ps_script,
    generate_yolo_wrapper,
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
# 2. Generate .bashrc
# ---------------------------------------------------------------------------


def generate_bashrc():
    """Write the jail .bashrc with prompt, PATH, aliases, and mise activation."""
    host_dir = os.environ.get("YOLO_HOST_DIR", "unknown")
    mise_shims = str(MISE_SHIMS)

    content = (
        r"""# YOLO Jail Prompt
YELLOW='\[\033[1;33m\]'
RED='\[\033[1;31m\]'
GREEN='\[\033[1;32m\]'
BLUE='\[\033[1;34m\]'
MAGENTA='\[\033[1;35m\]'
CYAN='\[\033[1;36m\]'
NC='\[\033[0m\]'

JAIL_BANNER="${RED}🔒 YOLO-JAIL${NC}"
HOST_INFO="${CYAN}(host: """
        + host_dir
        + r""")${NC}"

export PS1="\n${JAIL_BANNER} ${HOST_INFO}\n${GREEN}jail${NC}:${BLUE}\w${NC}\$ "

# Set terminal/tmux title (only when inside tmux to avoid literal "JAIL" output)
export PROMPT_COMMAND='[ -n "$TMUX" ] && printf "\033]0;JAIL\033\\"'

# Agent-friendly defaults (no pagers, no line numbers)
export PAGER=cat
export BAT_PAGER=""
export BAT_STYLE="plain"
export GIT_PAGER=cat
# EDITOR=cat prevents agents from getting stuck in interactive editors (e.g. git commit).
# VISUAL=nvim is used by interactive tools like Copilot's ctrl-g (edit prompt in editor).
# Standard Unix convention: programs check VISUAL first for full-screen terminals, EDITOR as fallback.
export EDITOR=cat
export VISUAL=nvim

# Combined CA bundle — baseline Nix cacert + every loophole CA.
# Point every standard TLS trust-store env var at one file so Python
# (ssl, requests, httpx), curl, and git all verify the same roots the
# in-jail broker leafs are signed by.  NODE_EXTRA_CA_CERTS is set by
# the container launcher to just the extras (Node adds them to its own
# bundled roots).  See generate_ca_bundle() in entrypoint.py.
if [ -f "$HOME/.yolo-ca-bundle.crt" ]; then
    export SSL_CERT_FILE="$HOME/.yolo-ca-bundle.crt"
    export REQUESTS_CA_BUNDLE="$HOME/.yolo-ca-bundle.crt"
    export CURL_CA_BUNDLE="$HOME/.yolo-ca-bundle.crt"
    export GIT_SSL_CAINFO="$HOME/.yolo-ca-bundle.crt"
fi

# Source user-defined env vars from config (defaults, overridable by .env).
# Loaded early so mise activation can override with .env values.
[ -f "$HOME/.config/yolo-user-env.sh" ] && . "$HOME/.config/yolo-user-env.sh"

# PATH with npm-global and go binaries
export NPM_CONFIG_PREFIX="${NPM_CONFIG_PREFIX:-$HOME/.npm-global}"
export NPM_CONFIG_CACHE="${NPM_CONFIG_CACHE:-$HOME/.cache/npm}"
export GOPATH="${GOPATH:-$HOME/go}"
SHIM_DIR="${HOME}/.yolo-shims"
export PATH="$SHIM_DIR:$HOME/.local/bin:$NPM_CONFIG_PREFIX/bin:"""
        + mise_shims
        + r""":$GOPATH/bin:/bin:/usr/bin"

# Activate mise with shell hooks (interactive shells only).
# Non-interactive shells (bash -lc) skip activation to avoid a deadlock:
# mise hook-env holds a lock then spawns uv via the mise shim (which IS mise),
# re-entering mise locking. The caller's eval "$(mise env ...)" already set up
# the environment before spawning this shell.
if [[ $- == *i* ]]; then
    eval "$(mise activate bash)"
fi
if [ -f /workspace/mise.toml ]; then
    mise trust --quiet /workspace/mise.toml 2>/dev/null || true
fi

# Aliases
alias ls='ls --color=auto'
alias ll='ls -alF'
alias gemini='gemini --yolo'
alias copilot='copilot --yolo --no-auto-update'
# Claude YOLO mode: cli.py injects --dangerously-skip-permissions (with
# IS_SANDBOX=1 to bypass the root check) + settings.json permissions.allow rules.
alias vi='nvim'
alias vim='nvim'
alias bat='bat --style=plain --paging=never'
"""
    )
    BASHRC_PATH.write_text(content)


# ---------------------------------------------------------------------------
# 2a. Combined CA bundle — so every TLS client finds the loophole CAs
# ---------------------------------------------------------------------------

# The combined bundle.  Writable path under $HOME so we can rewrite it at
# every jail boot.


# ---------------------------------------------------------------------------
# 3. Bootstrap script (runs after mise is ready)
# ---------------------------------------------------------------------------


def generate_bootstrap_script():
    """Create the idempotent bootstrap script that installs MCP/LSP tools."""
    script_path = HOME / ".yolo-bootstrap.sh"
    mise_shims = str(MISE_SHIMS)
    script_path.write_text(rf"""#!/bin/bash
export NPM_CONFIG_PREFIX="${{NPM_CONFIG_PREFIX:-$HOME/.npm-global}}"
export NPM_CONFIG_CACHE="${{NPM_CONFIG_CACHE:-$HOME/.cache/npm}}"
export GOPATH="${{GOPATH:-$HOME/go}}"
export GOBIN="$GOPATH/bin"
export PATH="$HOME/.local/bin:$NPM_CONFIG_PREFIX/bin:{mise_shims}:$GOBIN:$PATH"

# Initialize font cache (once, not on every shell session)
fc-cache -f >/dev/null 2>&1

# Agent CLIs (gemini, copilot, claude) are NOT updated here.
# Lazy-update launchers in ~/.yolo-shims/ handle install/update on first use,
# keeping boot fast.  Only MCP/LSP tools that agents depend on are installed here.

# Install binaries if missing.
if ! command -v chrome-devtools-mcp >/dev/null; then
    echo "  Installing MCP tools..." >&2
    # Clean stale npm temp directories that cause ENOTEMPTY on rename.
    # maxdepth 2 catches both top-level and scoped (@org/) packages.
    find "$NPM_CONFIG_PREFIX/lib/node_modules" -maxdepth 2 -name '.*' -type d 2>/dev/null | xargs rm -rf
    YOLO_BYPASS_SHIMS=1 npm install -g chrome-devtools-mcp @modelcontextprotocol/server-sequential-thinking pyright typescript-language-server typescript
fi

if [ ! -f "$GOBIN/mcp-language-server" ] || [ ! -f "$GOBIN/gopls" ]; then
    if command -v go >/dev/null; then
        echo "  Installing Go tools..." >&2
        mkdir -p "$GOBIN"
        [ -f "$GOBIN/mcp-language-server" ] || YOLO_BYPASS_SHIMS=1 go install github.com/isaacphi/mcp-language-server@latest
        [ -f "$GOBIN/gopls" ] || YOLO_BYPASS_SHIMS=1 go install golang.org/x/tools/gopls@latest
    else
        echo "  ⚠ go not found, skipping Go tool installs" >&2
    fi
fi

# Install showboat
if ! command -v showboat >/dev/null; then
    echo "  Installing showboat..." >&2
    YOLO_BYPASS_SHIMS=1 pip install showboat
fi
""")
    script_path.chmod(script_path.stat().st_mode | stat.S_IEXEC)


def generate_venv_precreate_script():
    """Create a script that pre-creates python venvs using real binaries.

    Must run AFTER `mise install` (so tools are available) and BEFORE
    `mise hook-env` / `mise env` (which would deadlock trying to create
    venvs via the mise shim).
    """
    script_path = HOME / ".yolo-venv-precreate.sh"
    script_path.write_text(r"""#!/bin/bash
# Pre-create python venvs to avoid a mise shim deadlock.
# When _.python.venv={create:true} is configured, mise hook-env spawns
# uv via the mise shim (which IS /bin/mise), re-entering mise's flock
# and deadlocking.  Creating the venv beforehand with the real uv binary
# means mise finds it already exists and skips the uv call.

[ -f /workspace/mise.toml ] || exit 0

# Get real binary paths (not shims) — requires mise install to have run
_uv=$(mise which uv 2>/dev/null) || exit 0
_py=$(mise which python 2>/dev/null) || exit 0
[ -n "$_uv" ] && [ -n "$_py" ] || exit 0

# Parse venv path from mise.toml
_vp=$(/bin/python3 -c "
import tomllib, sys
try:
    c = tomllib.load(open('/workspace/mise.toml', 'rb'))
    v = c.get('env', {}).get('_.python.venv', {})
    if isinstance(v, dict):
        if v.get('create', False):
            print(v.get('path', '.venv'))
        else:
            sys.exit(1)
    elif isinstance(v, str):
        print(v)
    else:
        sys.exit(1)
except Exception:
    sys.exit(1)
" 2>/dev/null) || exit 0

[ -d "/workspace/$_vp" ] && exit 0
"$_uv" venv "/workspace/$_vp" --python "$_py" 2>/dev/null || true
""")
    script_path.chmod(script_path.stat().st_mode | stat.S_IEXEC)


# ---------------------------------------------------------------------------
# 5. MCP wrappers (node, npx, chrome)
# ---------------------------------------------------------------------------


def _write_executable(path: Path, content: str):
    """Write content to path and make executable."""
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content)
    path.chmod(path.stat().st_mode | stat.S_IEXEC)


def generate_mcp_wrappers():
    """Create wrapper scripts for node, npx, and chrome-devtools-mcp."""
    # Chrome wrapper
    _write_executable(
        HOME / ".local" / "bin" / "chrome-devtools-mcp-wrapper",
        r"""#!/bin/bash
# Self-contained wrapper: sets its own env since agents sanitize child processes.
export LD_LIBRARY_PATH="/lib:/usr/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
export FONTCONFIG_FILE="${FONTCONFIG_FILE:-/etc/fonts/fonts.conf}"
export FONTCONFIG_PATH="${FONTCONFIG_PATH:-/etc/fonts}"

# Internal Chrome debugging defaults (isolated to container)
CHROME_PORT="${CHROME_DEBUG_PORT:-9222}"
CHROME_ADDR="${CHROME_DEBUG_ADDR:-127.0.0.1}"
CHROME_URL="http://$CHROME_ADDR:$CHROME_PORT"

NPM_BIN="${NPM_CONFIG_PREFIX:-$HOME/.npm-global}/bin"
MCP_WRAPPERS_BIN="$HOME/.local/bin/mcp-wrappers"

# Start Chromium if not already running
if ! curl -s "$CHROME_URL/json/version" >/dev/null 2>&1; then
    /usr/bin/chromium \
        --headless=new \
        --no-sandbox \
        --disable-dev-shm-usage \
        --disable-setuid-sandbox \
        --disable-gpu \
        --disable-software-rasterizer \
        --disable-blink-features=AutomationControlled \
        --disable-breakpad \
        --noerrdialogs \
        --user-agent="Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/144.0.0.0 Safari/537.36" \
        --remote-debugging-address=$CHROME_ADDR \
        --remote-debugging-port=$CHROME_PORT \
        &>/dev/null &

    # Wait for Chrome to be ready
    for i in $(seq 1 30); do
        if curl -s "$CHROME_URL/json/version" >/dev/null 2>&1; then
            break
        fi
        sleep 0.2
    done
fi

exec "$MCP_WRAPPERS_BIN/node" "$NPM_BIN/chrome-devtools-mcp" \
    --browser-url "$CHROME_URL" \
    "$@"
""",
    )

    # Node wrapper — bypass mise shims to avoid workspace env overhead on MCP startup
    _write_executable(
        MCP_WRAPPERS_BIN / "node",
        r"""#!/bin/bash
export LD_LIBRARY_PATH="/lib:/usr/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
export FONTCONFIG_FILE="${FONTCONFIG_FILE:-/etc/fonts/fonts.conf}"
export FONTCONFIG_PATH="${FONTCONFIG_PATH:-/etc/fonts}"
exec /bin/node "$@"
""",
    )

    # npx wrapper — bypass mise shims for same reason
    _write_executable(
        MCP_WRAPPERS_BIN / "npx",
        r"""#!/bin/bash
export LD_LIBRARY_PATH="/lib:/usr/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
export FONTCONFIG_FILE="${FONTCONFIG_FILE:-/etc/fonts/fonts.conf}"
export FONTCONFIG_PATH="${FONTCONFIG_PATH:-/etc/fonts}"
exec /bin/npx "$@"
""",
    )


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
# 11. Finalize PATH and exec bash
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
