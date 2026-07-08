"""Generated PATH shims that sit ahead of mise/system binaries.

Three flavors:

  * ``generate_shims`` — block or filter tools per ``YOLO_BLOCK_CONFIG``.
    Shims either unconditionally exit 127 with a configured message, or
    inspect argv against a list of glob patterns and only block when one
    matches (used for ``grep -r`` / ``grep --recursive`` while letting
    plain ``grep`` pass through).
  * ``generate_agent_launchers`` — lazy-update wrappers for ``gemini``,
    ``copilot`` (npm) and ``claude`` (native installer).  First run
    after a stamp expiry triggers a background update; otherwise
    ``exec`` straight to the real binary.  Skips writing if a shim of
    the same name already exists (e.g. ``copilot`` was blocked).
  * ``generate_package_manager_launchers`` — lazy npm launchers for
    package managers we don't pre-install via mise (``pnpm``).  Has a
    failure-throttling stamp so a broken install doesn't hammer npm on
    every invocation.

All three look up ``HOME`` and ``SHIM_DIR`` lazily from the parent
package so test fixtures that rebind ``entrypoint.HOME = tmp_path``
after import still work.
"""

import json
import os
import shlex
import shutil
import stat


def generate_shims():
    """Create shell shims that block or redirect tools per YOLO_BLOCK_CONFIG."""
    from . import SHIM_DIR

    # Use ignore_errors to handle races when multiple jails start concurrently
    # and both try to rmtree the same shared home directory.
    shutil.rmtree(SHIM_DIR, ignore_errors=True)
    SHIM_DIR.mkdir(parents=True, exist_ok=True)

    block_json = os.environ.get("YOLO_BLOCK_CONFIG", "")
    if not block_json:
        return

    try:
        config = json.loads(block_json)
    except (json.JSONDecodeError, TypeError):
        return

    for tool_cfg in config:
        name = tool_cfg.get("name")
        if not name:
            continue

        msg = tool_cfg.get("message", f"Error: tool {name} is blocked in this project.")
        sug = tool_cfg.get("suggestion", "")
        real_bin = f"/bin/{name}" if name in ("grep", "find") else None
        # ``block_flags`` is a list of shell ``case`` glob patterns.
        # When present, the shim only blocks when argv contains one
        # of the patterns; otherwise argv passes through to the real
        # binary.  Absent means "always block" (default for ``find``).
        block_flags = tool_cfg.get("block_flags") or []

        if block_flags and real_bin:
            # Split patterns into explicit long-option exact matches
            # (``--foo``) and everything else.  The shim emits the
            # long matches first, then a wildcard ``--*`` skip so
            # unrelated long options (``--regex`` when the user
            # configured short pattern ``-*[rR]*``) don't get caught,
            # then the short patterns.
            long_exact = [p for p in block_flags if p.startswith("--")]
            short_patterns = [p for p in block_flags if not p.startswith("--")]

            lines = ["#!/bin/sh"]
            lines.append('if [ -z "$YOLO_BYPASS_SHIMS" ]; then')
            lines.append('  for arg in "$@"; do')
            lines.append('    case "$arg" in')
            if long_exact:
                lines.append("      " + "|".join(long_exact) + ")")
                lines.append(f'        echo "{msg}" >&2')
                if sug:
                    lines.append(f'        echo "Suggestion: {sug}" >&2')
                lines.append("        exit 127")
                lines.append("        ;;")
            lines.append("      --*)")
            lines.append("        : ;;")
            if short_patterns:
                lines.append("      " + "|".join(short_patterns) + ")")
                lines.append(f'        echo "{msg}" >&2')
                if sug:
                    lines.append(f'        echo "Suggestion: {sug}" >&2')
                lines.append("        exit 127")
                lines.append("        ;;")
            lines.append("    esac")
            lines.append("  done")
            lines.append("fi")
            lines.append(f'exec {real_bin} "$@"')
            lines.append("")
        else:
            # Unconditional block.
            lines = ["#!/bin/sh"]
            lines.append('if [ -z "$YOLO_BYPASS_SHIMS" ]; then')
            lines.append(f'  echo "{msg}" >&2')
            if sug:
                lines.append(f'  echo "Suggestion: {sug}" >&2')
            lines.append("  exit 127")
            lines.append("fi")
            if real_bin:
                lines.append(f'exec {real_bin} "$@"')
            lines.append("")

        shim_path = SHIM_DIR / name
        shim_path.write_text("\n".join(lines))
        shim_path.chmod(shim_path.stat().st_mode | stat.S_IEXEC)


def generate_agent_launchers():
    """Create lazy-update wrappers for the SELECTED agent CLIs.

    Instead of updating all agents at boot (slow), these wrappers install +
    update the specific agent on first use.  They sit in SHIM_DIR (highest
    PATH priority) and ``exec`` the real binary once it's current.

    Which agents get a launcher is driven by ``YOLO_AGENTS`` (the library
    model) via :func:`entrypoint._load_agents`, and HOW each installs — npm
    package vs native ``curl | bash`` installer — comes from that agent's
    :class:`~entrypoint.agent_registry.InstallSpec`.  Only the selected
    subset is emitted, so a claude-only jail never installs copilot/gemini.
    """
    from . import HOME, SHIM_DIR, _load_agents
    from .agent_registry import AGENTS

    SHIM_DIR.mkdir(parents=True, exist_ok=True)
    stamp_dir = HOME / ".cache" / "yolo-agent-stamps"

    for name in _load_agents():
        spec = AGENTS.get(name)
        if spec is None:
            continue
        bin_name = spec.install.bin
        # Don't overwrite a blocked-tool shim (YOLO_BLOCK_CONFIG ran first).
        shim_path = SHIM_DIR / bin_name
        if shim_path.exists():
            continue

        if spec.install.kind == "npm":
            launcher = _npm_agent_launcher(spec, stamp_dir)
        elif spec.install.kind == "native":
            launcher = _native_agent_launcher(spec, stamp_dir)
        else:
            continue

        shim_path.write_text(launcher)
        shim_path.chmod(shim_path.stat().st_mode | stat.S_IEXEC)


def _npm_agent_launcher(spec, stamp_dir) -> str:
    """Lazy install+update launcher body for an npm-packaged agent."""
    bin_name = spec.install.bin
    pkg_name = spec.install.package
    # Extra one-time install flags (e.g. pi's --ignore-scripts).  Joined into
    # the npm invocation; empty for most agents.
    extra_flags = " ".join(spec.install.install_flags)
    extra_flags = (extra_flags + " ") if extra_flags else ""
    return f"""#!/bin/bash
# Lazy-update launcher for {bin_name} — installs/updates on first use, not at boot.
set -euo pipefail
export NPM_CONFIG_PREFIX="${{NPM_CONFIG_PREFIX:-$HOME/.npm-global}}"
export NPM_CONFIG_CACHE="${{NPM_CONFIG_CACHE:-$HOME/.cache/npm}}"
STAMP_DIR="{stamp_dir}"
STAMP="$STAMP_DIR/{bin_name}.stamp"
REAL_BIN="$NPM_CONFIG_PREFIX/bin/{bin_name}"
PKG="{pkg_name}"
UPDATE_INTERVAL=3600  # seconds between update checks

mkdir -p "$STAMP_DIR"

_do_install() {{
    echo "  Installing $PKG..." >&2
    # Clean stale npm temp dirs that cause ENOTEMPTY
    rm -rf "$NPM_CONFIG_PREFIX"/lib/node_modules/${{PKG%%/*}}/.${{PKG##*/}}-* 2>/dev/null
    YOLO_BYPASS_SHIMS=1 npm install -g {extra_flags}--prefer-online "$PKG@latest" 2>&1 || true
    touch "$STAMP"
}}

if [ ! -x "$REAL_BIN" ]; then
    _do_install
elif [ ! -f "$STAMP" ]; then
    # First run since jail boot — check if update needed
    INSTALLED=$(jq -r '.version' "$NPM_CONFIG_PREFIX/lib/node_modules/$PKG/package.json" 2>/dev/null || echo "0")
    LATEST=$(YOLO_BYPASS_SHIMS=1 npm view "$PKG" version 2>/dev/null || echo "$INSTALLED")
    if [ "$INSTALLED" != "$LATEST" ]; then
        echo "  Updating {bin_name} $INSTALLED → $LATEST..." >&2
        _do_install
    else
        touch "$STAMP"
    fi
else
    # Check if stamp is stale (older than UPDATE_INTERVAL)
    STAMP_AGE=$(( $(date +%s) - $(stat -c %Y "$STAMP" 2>/dev/null || echo 0) ))
    if [ "$STAMP_AGE" -gt "$UPDATE_INTERVAL" ]; then
        INSTALLED=$(jq -r '.version' "$NPM_CONFIG_PREFIX/lib/node_modules/$PKG/package.json" 2>/dev/null || echo "0")
        LATEST=$(YOLO_BYPASS_SHIMS=1 npm view "$PKG" version 2>/dev/null || echo "$INSTALLED")
        if [ "$INSTALLED" != "$LATEST" ]; then
            echo "  Updating {bin_name} $INSTALLED → $LATEST..." >&2
            _do_install
        else
            touch "$STAMP"
        fi
    fi
fi

if [ -x "$REAL_BIN" ]; then
    exec "$REAL_BIN" "$@"
else
    echo "  ⚠ {bin_name} not available" >&2
    exit 1
fi
"""


def _native_agent_launcher(spec, stamp_dir) -> str:
    """Lazy install+update launcher body for a native-installer agent (claude).

    Installs via the agent's ``curl | bash`` installer, then self-updates
    with ``$REAL_BIN install`` (the pattern claude's installer supports).
    """
    bin_name = spec.install.bin
    installer_url = spec.install.installer_url
    return f"""#!/bin/bash
# Lazy-update launcher for {bin_name} — installs/updates on first use, not at boot.
set -euo pipefail
STAMP_DIR="{stamp_dir}"
STAMP="$STAMP_DIR/{bin_name}.stamp"
REAL_BIN="$HOME/.local/bin/{bin_name}"
UPDATE_INTERVAL=3600

mkdir -p "$STAMP_DIR"

_do_install() {{
    echo "  Installing {bin_name}..." >&2
    YOLO_BYPASS_SHIMS=1 curl -fsSL {installer_url} | bash 2>&1 || true
    touch "$STAMP"
}}

if [ ! -x "$REAL_BIN" ]; then
    _do_install
elif [ ! -f "$STAMP" ]; then
    # First run since boot — try a quick update
    YOLO_BYPASS_SHIMS=1 "$REAL_BIN" install 2>&1 || true
    touch "$STAMP"
else
    STAMP_AGE=$(( $(date +%s) - $(stat -c %Y "$STAMP" 2>/dev/null || echo 0) ))
    if [ "$STAMP_AGE" -gt "$UPDATE_INTERVAL" ]; then
        YOLO_BYPASS_SHIMS=1 "$REAL_BIN" install 2>&1 || true
        touch "$STAMP"
    fi
fi

if [ -x "$REAL_BIN" ]; then
    exec "$REAL_BIN" "$@"
else
    echo "  ⚠ {bin_name} not available" >&2
    exit 1
fi
"""


def generate_package_manager_launchers():
    """Create lazy npm-backed launchers for package managers disabled in mise."""
    from . import HOME, SHIM_DIR

    SHIM_DIR.mkdir(parents=True, exist_ok=True)
    stamp_dir = HOME / ".cache" / "yolo-package-manager-stamps"
    # shlex.quote so a $HOME containing shell metacharacters (spaces, quotes,
    # backslashes) doesn't break the generated launcher.
    stamp_dir_literal = shlex.quote(str(stamp_dir))
    npm_package_managers = {"pnpm": "pnpm"}

    for bin_name, pkg_name in npm_package_managers.items():
        shim_path = SHIM_DIR / bin_name
        if shim_path.exists():
            continue

        launcher = f"""#!/bin/bash
set -euo pipefail
export NPM_CONFIG_PREFIX="${{NPM_CONFIG_PREFIX:-$HOME/.npm-global}}"
export NPM_CONFIG_CACHE="${{NPM_CONFIG_CACHE:-$HOME/.cache/npm}}"
STAMP_DIR={stamp_dir_literal}
STAMP="$STAMP_DIR/{bin_name}.stamp"
REAL_BIN="$NPM_CONFIG_PREFIX/bin/{bin_name}"
PKG="{pkg_name}"
RETRY_INTERVAL=3600  # seconds before retrying a failed install

mkdir -p "$STAMP_DIR"

if [ ! -x "$REAL_BIN" ]; then
    # Throttle repeated install attempts after a failure — without this, every
    # invocation would re-hit npm registry when offline / install is broken.
    SHOULD_INSTALL=1
    if [ -f "$STAMP" ]; then
        STAMP_AGE=$(( $(date +%s) - $(stat -c %Y "$STAMP" 2>/dev/null || echo 0) ))
        if [ "$STAMP_AGE" -lt "$RETRY_INTERVAL" ]; then
            SHOULD_INSTALL=0
        fi
    fi
    if [ "$SHOULD_INSTALL" = "1" ]; then
        echo "  Installing $PKG..." >&2
        YOLO_BYPASS_SHIMS=1 npm install -g --prefer-online "$PKG@latest" 2>&1 || true
        touch "$STAMP"
    fi
fi

if [ -x "$REAL_BIN" ]; then
    exec "$REAL_BIN" "$@"
else
    echo "  ⚠ {bin_name} not available" >&2
    exit 1
fi
"""
        shim_path.write_text(launcher)
        shim_path.chmod(shim_path.stat().st_mode | stat.S_IEXEC)
