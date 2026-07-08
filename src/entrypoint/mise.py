"""Mise global config generation.

cli.py forwards workspace ``mise_tools`` (from yolo-jail.jsonc) via
``YOLO_MISE_TOOLS``; we merge those onto a small set of base tools
(node/python/go) into ``$HOME/.config/mise/config.toml``.

A few subtleties survived from years of working around mise quirks:

  * **Self-heal duplicate keys.**  An earlier bug could write a base
    tool twice when a workspace also injected it; mise refuses to parse
    the resulting file, so we de-duplicate keys (keeping the first) on
    every run before doing anything else.
  * **Retired-tool sweep.**  ``copilot``/``gemini``/``claude`` used to
    be mise-managed but are now installed by the bootstrap script
    (``npm install -g`` for the npm pair, native installer for
    claude).  We strip them from both the global config and any
    ``/workspace/mise.toml`` so stale mise-cached versions don't
    shadow the always-fresh installs, then ``mise uninstall --all``
    them best-effort.
  * **``"system"`` → real version.**  mise deprecated the ``"system"``
    placeholder; we rewrite any base tool pinned to ``"system"`` to
    its real version string.
"""

import json
import os
import re
import subprocess
from pathlib import Path


def _toml_key(key: str) -> str:
    """Quote a TOML key if it contains characters that aren't valid in bare keys."""
    if re.fullmatch(r"[A-Za-z0-9_-]+", key):
        return key
    return f'"{key}"'


def generate_mise_config():
    """Write global mise config, injecting tools from YOLO_MISE_TOOLS."""
    from . import MISE_CONFIG_DIR

    config_path = MISE_CONFIG_DIR / "config.toml"

    # Parse injected tools from env (set by cli.py from yolo-jail.jsonc)
    try:
        injected_tools = json.loads(os.environ.get("YOLO_MISE_TOOLS", "{}"))
    except (ValueError, TypeError):
        injected_tools = {}

    # Base tools always present in the jail.
    # NOTE: copilot, gemini, and claude are NOT managed by mise — the bootstrap
    # script handles their installation (npm install -g for copilot/gemini,
    # native installer for claude) to avoid mise's version cache preventing
    # updates and the npm deprecation warning for claude.
    base_tools = {
        "node": "22",
        "python": "3.13",
        "go": "latest",
    }

    # Tools that used to be in base_tools but are now launcher-managed
    # (npm install -g / native installer via ~/.yolo-shims launchers).
    # Remove from existing configs to avoid stale mise-cached versions
    # shadowing the always-fresh installs.  Driven by the agent registry:
    # EVERY known agent stays retired from mise regardless of which are
    # selected — a deselected agent must also never be mise-managed.
    from .agent_registry import ALL_MISE_RETIRE

    retired_tools = list(ALL_MISE_RETIRE)

    if not config_path.exists():
        MISE_CONFIG_DIR.mkdir(parents=True, exist_ok=True)
        merged = {**base_tools, **injected_tools}
        lines = ["[tools]"]
        for tool, version in merged.items():
            lines.append(f'{_toml_key(tool)} = "{version}"')
        config_path.write_text("\n".join(lines) + "\n")
        return

    # Update existing config:
    # - base_tools: add if missing (don't overwrite user customizations)
    # - injected_tools: always add or update (explicit overrides from config)
    # - retired_tools: remove if present (moved to bootstrap npm install)
    content = config_path.read_text()
    changed = False

    # Self-heal: drop duplicate tool-key lines (keep the first). A prior bug
    # could write a base tool twice when a workspace also injected it, and mise
    # refuses to parse the resulting file.
    seen_keys: set[str] = set()
    deduped_lines: list[str] = []
    key_re = re.compile(r'^\s*"?([^"\s=]+)"?\s*=')
    for line in content.splitlines(keepends=True):
        m = key_re.match(line)
        if m:
            key = m.group(1)
            if key in seen_keys:
                changed = True
                continue
            seen_keys.add(key)
        deduped_lines.append(line)
    if changed:
        content = "".join(deduped_lines)

    # Remove retired tools (now managed by bootstrap npm install, not mise)
    for tool in retired_tools:
        pattern = rf'^{re.escape(tool)}\s*=\s*"[^"]*"\n?'
        new_content = re.sub(pattern, "", content, flags=re.MULTILINE)
        if new_content != content:
            content = new_content
            changed = True

    # Ensure all base tools are present and not using deprecated "system" value.
    # mise deprecated @system — replace with the base version.
    for tool, version in base_tools.items():
        tk = _toml_key(tool)
        pattern = rf'^"?{re.escape(tool)}"?\s*=\s*"[^"]*"'
        match = re.search(pattern, content, re.MULTILINE)
        if not match:
            content = content.rstrip("\n") + f'\n{tk} = "{version}"\n'
            changed = True
        elif '"system"' in match.group():
            content = (
                content[: match.start()]
                + f'{tk} = "{version}"'
                + content[match.end() :]
            )
            changed = True

    # Injected tools always override
    for tool, version in injected_tools.items():
        tk = _toml_key(tool)
        pattern = rf'^"?{re.escape(tool)}"?\s*=\s*"[^"]*"'
        if re.search(pattern, content, re.MULTILINE):
            new_content = re.sub(
                pattern, f'{tk} = "{version}"', content, flags=re.MULTILINE
            )
            if new_content != content:
                content = new_content
                changed = True
        else:
            content = content.rstrip("\n") + f'\n{tk} = "{version}"\n'
            changed = True

    if changed:
        config_path.write_text(content)

    # Also retire from workspace mise.toml if present (mounted from host).
    ws_mise = Path("/workspace/mise.toml")
    if ws_mise.exists():
        ws_content = ws_mise.read_text()
        ws_changed = False
        for tool in retired_tools:
            pattern = rf'^{re.escape(tool)}\s*=\s*"[^"]*"\n?'
            new_ws = re.sub(pattern, "", ws_content, flags=re.MULTILINE)
            if new_ws != ws_content:
                ws_content = new_ws
                ws_changed = True
        if ws_changed:
            ws_mise.write_text(ws_content)

    # Uninstall retired mise tools so stale binaries don't shadow bootstrap ones.
    # mise uninstall is idempotent — safe to call even if already removed.
    for tool in retired_tools:
        tool_name = tool.strip('"')
        try:
            subprocess.run(
                ["mise", "uninstall", "--all", tool_name],
                capture_output=True,
                timeout=30,
            )
        except Exception:
            pass
