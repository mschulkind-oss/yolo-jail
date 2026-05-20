"""Wrapper scripts for MCP servers + the binaries they exec.

Three scripts are written into the jail:

  * ``$HOME/.local/bin/chrome-devtools-mcp-wrapper`` — starts a
    headless chromium on first invocation, then exec's the
    ``chrome-devtools-mcp`` server pointed at the debug URL.  Sets its
    own ``LD_LIBRARY_PATH``/``FONTCONFIG_*`` so it works even when the
    parent agent has scrubbed the env it inherited.
  * ``$HOME/.local/bin/mcp-wrappers/node`` — minimal node wrapper that
    bypasses the mise shim (mise re-evaluates workspace env on every
    invocation, which dominates MCP cold-start latency).
  * ``$HOME/.local/bin/mcp-wrappers/npx`` — same idea for npx.

The chrome wrapper goes through ``mcp-wrappers/node`` rather than
``/bin/node`` so the same env hygiene applies to its child.
"""

import stat
from pathlib import Path


def _write_executable(path: Path, content: str):
    """Write content to path and make executable."""
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content)
    path.chmod(path.stat().st_mode | stat.S_IEXEC)


def generate_mcp_wrappers():
    """Create wrapper scripts for node, npx, and chrome-devtools-mcp."""
    from . import HOME, MCP_WRAPPERS_BIN

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
