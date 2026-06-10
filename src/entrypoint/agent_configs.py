"""Agent (Copilot / Gemini / Claude Code) MCP + LSP configuration.

Each ``configure_*`` builds the mcp/lsp config the corresponding agent
expects and writes it under ``$HOME``.  Shared MCP-server resolution
(presets + ``YOLO_MCP_*`` env merges) lives in :func:`_load_mcp_servers`
so all three agents see the same servers.

Claude Code gets a few extras the others don't need:

  * ``_install_claude_plugins`` runs ``claude plugins install`` for the
    LSP plugins that match the jail's configured servers (best-effort).
  * ``_sync_host_claude_files`` copies host ``~/.claude/`` files (minus
    settings.json, which is deep-merged) into the jail.
  * ``_isolate_claude_history`` symlinks ``history.jsonl`` to a
    per-host-workspace file so up-arrow history doesn't bleed between
    jails sharing the same $HOME.
  * ``_ensure_credentials_symlink`` migrates the legacy single-file
    bind mount of ``.credentials.json`` to a symlink into the rw
    directory bind mount, fixing IWH atomic-write EBUSY.

Path constants and ``_load_lsp_servers`` are imported lazily from the
parent package so test fixtures that rebind ``entrypoint.HOME`` (etc.)
after import keep working.
"""

import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path

# ``${VAR}`` references in MCP env values get expanded against the jail's
# startup env at _load_mcp_servers time.  Only the braced form is
# recognised — keeps the rule predictable and dodges the ``$foo$bar``
# ambiguities that os.path.expandvars inherits from sh.
_ENV_VAR_PATTERN = re.compile(r"\$\{([A-Za-z_][A-Za-z0-9_]*)\}")


def _interpolate_env(env) -> dict:
    """Expand ``${VAR}`` in MCP env values against ``os.environ``.

    Undefined variables are left as the literal ``${VAR}`` (same default
    as ``set +u`` shell expansion) and a warning is written to stderr so
    the user notices the missing reference without aborting startup.
    Non-string values are passed through untouched — the host-side
    validator already rejects them, this guards against bypass.
    """
    if not isinstance(env, dict):
        return env
    resolved: dict = {}
    unresolved: list[str] = []
    for k, v in env.items():
        if not isinstance(v, str):
            resolved[k] = v
            continue

        def _replace(match, _missing=unresolved):
            name = match.group(1)
            if name in os.environ:
                return os.environ[name]
            _missing.append(name)
            return match.group(0)

        resolved[k] = _ENV_VAR_PATTERN.sub(_replace, v)
    if unresolved:
        names = ", ".join(sorted(set(unresolved)))
        print(
            f"warning: MCP env references undefined variable(s): {names}",
            file=sys.stderr,
        )
    return resolved


def _chrome_devtools_args() -> list:
    """Common chrome-devtools-mcp args."""
    from . import NPM_BIN

    return [
        str(NPM_BIN / "chrome-devtools-mcp"),
        "--headless",
        "--isolated",
        "--executablePath",
        "/usr/bin/chromium",
        "--chrome-arg=--no-sandbox",
        "--chrome-arg=--disable-dev-shm-usage",
        "--chrome-arg=--disable-setuid-sandbox",
        "--chrome-arg=--disable-gpu",
        "--chrome-arg=--disable-software-rasterizer",
    ]


def _load_mcp_servers():
    """Load MCP servers from presets plus YOLO_MCP_SERVERS overrides.

    Presets are expanded from YOLO_MCP_PRESETS (JSON array of preset names).
    Custom servers from YOLO_MCP_SERVERS are merged on top.
    A null value removes a preset or inherited server.
    """
    from . import MCP_WRAPPERS_BIN, NPM_BIN

    presets = {
        "chrome-devtools": {
            "command": str(MCP_WRAPPERS_BIN / "node"),
            "args": _chrome_devtools_args(),
        },
        "sequential-thinking": {
            "command": str(MCP_WRAPPERS_BIN / "node"),
            "args": [str(NPM_BIN / "mcp-server-sequential-thinking")],
        },
    }

    # Start empty — presets are opt-in
    servers = {}

    # Expand requested presets
    presets_json = os.environ.get("YOLO_MCP_PRESETS", "")
    if presets_json:
        try:
            preset_names = json.loads(presets_json)
            if isinstance(preset_names, list):
                for name in preset_names:
                    if isinstance(name, str) and name in presets:
                        servers[name] = presets[name]
        except (json.JSONDecodeError, TypeError):
            pass

    # Merge custom servers (overrides, additions, and null-removals)
    extra_json = os.environ.get("YOLO_MCP_SERVERS", "")
    if extra_json:
        try:
            extra = json.loads(extra_json)
            if isinstance(extra, dict):
                for name, cfg in extra.items():
                    if cfg is None:
                        servers.pop(name, None)
                    elif isinstance(cfg, dict):
                        servers[name] = cfg
        except (json.JSONDecodeError, TypeError):
            pass

    # Conditional loading: a server declaring ``requires_env`` is only
    # configured when every listed variable is set (and non-empty) in
    # the jail's startup env.  Lets a dotfiles-shared user config carry
    # machine-dependent servers (e.g. tavily) that activate only where
    # env_sources provides the keys.  The key is stripped before the
    # config reaches any agent — it's a yolo-jail directive, not part
    # of the MCP server schema.
    for name in list(servers):
        cfg = servers[name]
        if not isinstance(cfg, dict):
            continue
        required = cfg.get("requires_env")
        if not isinstance(required, list):
            continue
        missing = [v for v in required if isinstance(v, str) and not os.environ.get(v)]
        if missing:
            print(
                f"notice: MCP server '{name}' skipped — required env not set: "
                f"{', '.join(missing)}",
                file=sys.stderr,
            )
            del servers[name]
        else:
            servers[name] = {k: v for k, v in cfg.items() if k != "requires_env"}

    # Expand ${VAR} in env values against the jail's startup env (which
    # already has env_sources merged in).  Lets users keep a secret in
    # one unsynced file and scope it to a single MCP server's env
    # without hoisting it jail-wide.
    for name, cfg in servers.items():
        if isinstance(cfg, dict) and isinstance(cfg.get("env"), dict):
            servers[name] = {**cfg, "env": _interpolate_env(cfg["env"])}
    return servers


def configure_copilot():
    """Set up Copilot directory, MCP config, and LSP config."""
    from . import COPILOT_DIR, _load_lsp_servers

    COPILOT_DIR.mkdir(parents=True, exist_ok=True)

    config_json = COPILOT_DIR / "config.json"
    if not config_json.exists():
        config_json.write_text('{"yolo": true}\n')

    # MCP config
    mcp_config = {"mcpServers": _load_mcp_servers()}
    (COPILOT_DIR / "mcp-config.json").write_text(
        json.dumps(mcp_config, indent=2) + "\n"
    )

    # LSP config (defaults + workspace overrides from YOLO_LSP_SERVERS)
    servers = _load_lsp_servers()
    lsp_config = {"lspServers": {}}
    for name, cfg in servers.items():
        lsp_config["lspServers"][name] = {
            "command": cfg["command"],
            "args": cfg.get("args", []),
            "fileExtensions": cfg.get("fileExtensions", {}),
        }
    (COPILOT_DIR / "lsp-config.json").write_text(
        json.dumps(lsp_config, indent=2) + "\n"
    )


def configure_gemini():
    """Set up Gemini settings with MCP servers, merging with existing config."""
    from . import GEMINI_DIR, GEMINI_MANAGED_MCP_PATH, GO_BIN, _load_lsp_servers

    GEMINI_DIR.mkdir(parents=True, exist_ok=True)
    config_path = GEMINI_DIR / "settings.json"

    configured_servers = _load_mcp_servers()

    # Add LSP servers wrapped as MCP via mcp-language-server
    lsp_servers = _load_lsp_servers()
    for name, cfg in lsp_servers.items():
        cmd = cfg["command"]
        bare_cmd = Path(cmd).name
        lsp_args = cfg.get("args", [])
        mcp_args = ["-lsp", bare_cmd, "-workspace", "/workspace"]
        if lsp_args:
            mcp_args.extend(["--"] + lsp_args)
        configured_servers[f"{name}-lsp"] = {
            "command": str(GO_BIN / "mcp-language-server"),
            "args": mcp_args,
        }

    try:
        if config_path.exists():
            try:
                current = json.loads(config_path.read_text())
            except json.JSONDecodeError:
                current = {}
        else:
            current = {}

        current_mcp_servers = current.setdefault("mcpServers", {})
        try:
            previous_managed = set(json.loads(GEMINI_MANAGED_MCP_PATH.read_text()))
        except (FileNotFoundError, json.JSONDecodeError, TypeError, ValueError):
            # Migration path for older jails: clean up the default yolo-managed
            # servers plus any stale workspace-bound servers from previous runs.
            previous_managed = {"chrome-devtools", "sequential-thinking"}
            for name, cfg in current_mcp_servers.items():
                if not isinstance(cfg, dict):
                    continue
                command = str(cfg.get("command", ""))
                if name.endswith("-lsp") and command == str(
                    GO_BIN / "mcp-language-server"
                ):
                    previous_managed.add(name)
                if command.startswith("/workspace/"):
                    previous_managed.add(name)

        for name in previous_managed:
            current_mcp_servers.pop(name, None)
        current_mcp_servers.update(configured_servers)

        current.setdefault("security", {})
        current["security"].setdefault("approvalMode", "yolo")
        current["security"].setdefault("enablePermanentToolApproval", True)
        current.setdefault("general", {})
        current["general"]["enableAutoUpdate"] = False
        current["general"]["enableAutoUpdateNotification"] = False

        config_path.write_text(json.dumps(current, indent=2) + "\n")
        GEMINI_MANAGED_MCP_PATH.write_text(
            json.dumps(sorted(configured_servers.keys()), indent=2) + "\n"
        )
    except Exception as e:
        print(f"Error configuring Gemini MCP: {e}", file=sys.stderr)


# Map jail LSP server names to Claude Code official plugin IDs.
CLAUDE_LSP_PLUGIN_MAP = {
    "python": "pyright-lsp@claude-plugins-official",
    "typescript": "typescript-lsp@claude-plugins-official",
    "go": "gopls-lsp@claude-plugins-official",
}


def _install_claude_plugins(plugin_map: dict, lsp_servers: dict):
    """Install/uninstall Claude Code LSP plugins to match ``lsp_servers``.

    For each entry in ``plugin_map``:
      * if the matching LSP is configured and the plugin isn't installed
        yet → ``claude plugins install``;
      * if the LSP is *not* configured but the plugin *is* installed
        (left over from a previous boot when it was) →
        ``claude plugins uninstall``.

    Reads installed_plugins.json to know what's currently installed.
    All claude invocations are best-effort; failures don't abort jail
    startup (worst case the plugin sticks around an extra boot).
    """
    from . import CLAUDE_DIR, HOME

    plugins_meta = CLAUDE_DIR / "plugins" / "installed_plugins.json"
    try:
        installed = set(json.loads(plugins_meta.read_text()).get("plugins", {}).keys())
    except (FileNotFoundError, json.JSONDecodeError, TypeError):
        installed = set()

    claude_bin = HOME / ".local" / "bin" / "claude"
    if not claude_bin.exists():
        claude_bin = Path("claude")

    def _claude(*args: str) -> None:
        try:
            subprocess.run(
                [str(claude_bin), *args],
                capture_output=True,
                timeout=30,
                env={**os.environ, "YOLO_BYPASS_SHIMS": "1"},
            )
        except Exception:
            pass  # non-fatal — retry next boot

    for lsp_name, plugin_id in plugin_map.items():
        wanted = lsp_name in lsp_servers
        present = plugin_id in installed
        if wanted and not present:
            _claude("plugins", "install", plugin_id)
        elif present and not wanted:
            _claude("plugins", "uninstall", plugin_id)


def _sync_host_claude_files():
    """Copy host ~/.claude/ files into the jail, except settings.json (merged separately)."""
    from . import CLAUDE_DIR

    host_claude_files = json.loads(os.environ.get("YOLO_HOST_CLAUDE_FILES", "[]"))
    host_claude_dir = Path("/ctx/host-claude")

    for fname in host_claude_files:
        if fname == "settings.json":
            continue  # handled by configure_claude() via deep-merge
        src = host_claude_dir / fname
        dst = CLAUDE_DIR / fname
        if not src.exists():
            continue
        try:
            shutil.copy2(str(src), str(dst))
        except shutil.SameFileError:
            pass  # nested jail — src and dst are the same inode
        except OSError as e:
            print(
                f"Warning: could not copy host claude file {fname}: {e}",
                file=sys.stderr,
            )


def _sync_host_settings(jail: dict, host: dict, prev: dict) -> dict:
    """Three-way host→jail settings merge.  Mutates and returns ``jail``.

    ``prev`` is the host settings as of the last sync (the snapshot at
    ``CLAUDE_HOST_SETTINGS_SNAPSHOT_PATH``).  It lets us distinguish
    "key the jail set locally" from "key we copied from the host" —
    without it, host-originated keys could never be updated or removed
    (the pre-snapshot merge was add-only, so a host setting that later
    reverted stayed baked into every jail forever).

    Per key:
      * in host, not in jail               → add (host fills gaps)
      * in host and jail, jail == prev     → update to host value (the jail
                                             never touched it, so the host
                                             change propagates)
      * in host and jail, jail != prev     → keep jail (jail-local runtime
                                             state wins — Claude writes this
                                             file live)
      * in prev, not in host, jail == prev → remove (host dropped it and the
                                             jail never modified it: roll back)
    Dict values get the same rules one level deep — matching the depth of
    the original merge; deeper structures are compared atomically.
    First boot (``prev == {}``) degrades to the old add-only behavior.
    """
    _sync_settings_level(jail, host, prev, deep=True)
    return jail


def _sync_settings_level(jail: dict, host: dict, prev: dict, *, deep: bool):
    # Roll back keys we synced before that the host no longer has —
    # but only when the jail still holds exactly what we wrote.
    for key, prev_val in prev.items():
        if key in host or key not in jail:
            continue
        if jail[key] == prev_val:
            del jail[key]
        elif deep and isinstance(prev_val, dict) and isinstance(jail[key], dict):
            for k, v in prev_val.items():
                if k in jail[key] and jail[key][k] == v:
                    del jail[key][k]
    # Adds + updates from the current host file.
    for key, host_val in host.items():
        if key not in jail:
            jail[key] = host_val
        elif deep and isinstance(host_val, dict) and isinstance(jail[key], dict):
            prev_sub = prev.get(key)
            if not isinstance(prev_sub, dict):
                prev_sub = {}
            _sync_settings_level(jail[key], host_val, prev_sub, deep=False)
        elif key in prev and jail[key] == prev[key] and jail[key] != host_val:
            jail[key] = host_val


def _load_host_claude_settings() -> dict:
    """Load host settings.json from /ctx/host-claude/ if available."""
    host_claude_files = json.loads(os.environ.get("YOLO_HOST_CLAUDE_FILES", "[]"))
    if "settings.json" not in host_claude_files:
        return {}
    host_settings_path = Path("/ctx/host-claude/settings.json")
    if not host_settings_path.exists():
        return {}
    try:
        return json.loads(host_settings_path.read_text())
    except (ValueError, OSError):
        return {}


def _isolate_claude_history():
    """Give each jail its own Claude Code prompt history (up-arrow isolation).

    Claude stores readline history in ~/.claude/history.jsonl — a single global
    file.  Since all jails share $HOME and all have cwd /workspace, the default
    history is shared across jails.

    Fix: replace history.jsonl with a symlink to a per-project file inside
    ~/.claude/jail-history/<hash>.jsonl, keyed on YOLO_HOST_DIR (the unique
    host workspace path).
    """
    from . import CLAUDE_DIR

    host_dir = os.environ.get("YOLO_HOST_DIR", "")
    if not host_dir:
        return

    history_dir = CLAUDE_DIR / "jail-history"
    history_dir.mkdir(parents=True, exist_ok=True)

    h = hashlib.sha256(host_dir.encode()).hexdigest()[:12]
    per_jail = history_dir / f"{h}.jsonl"
    per_jail.touch(exist_ok=True)

    history_file = CLAUDE_DIR / "history.jsonl"
    # If it's already the right symlink, nothing to do
    if history_file.is_symlink():
        try:
            if history_file.resolve() == per_jail.resolve():
                return
        except OSError:
            pass
    # Remove existing (regular file or stale symlink)
    try:
        history_file.unlink(missing_ok=True)
    except OSError:
        pass
    history_file.symlink_to(per_jail)


def _ensure_credentials_symlink():
    """Ensure .claude/.credentials.json is a symlink into the shared credentials dir.

    The shared credentials directory is a rw directory bind mount, so Claude
    Code's IWH atomic writer (readlinkSync → tmp → rename) works correctly.
    The old approach — a single-file bind mount — caused EBUSY on rename,
    forcing the fallback truncate+write path which can lose data in races.
    """
    from . import CLAUDE_DIR, CLAUDE_SHARED_CREDENTIALS_DIR

    link = CLAUDE_DIR / ".credentials.json"
    target = Path("..") / ".claude-shared-credentials" / ".credentials.json"

    if link.is_symlink():
        try:
            if Path(os.readlink(str(link))) == target:
                return  # already correct
        except OSError:
            pass
        link.unlink()
    elif link.exists():
        # Migration: existing regular file (from old single-file bind mount era).
        # Copy its data to the shared dir if the shared dir's copy is missing
        # or empty, then replace with symlink.
        shared = CLAUDE_SHARED_CREDENTIALS_DIR / ".credentials.json"
        if not shared.exists() or shared.stat().st_size == 0:
            try:
                shutil.copy2(str(link), str(shared))
            except OSError:
                pass
        try:
            link.unlink()
        except OSError:
            return  # can't remove — leave as-is (still works via fallback write)

    link.symlink_to(target)


def configure_claude():
    """Set up Claude Code: settings.json (permissions, plugins) + ~/.claude.json (MCP)."""
    from . import (
        CLAUDE_DIR,
        CLAUDE_HOST_SETTINGS_SNAPSHOT_PATH,
        CLAUDE_MANAGED_MCP_PATH,
        HOME,
        _load_lsp_servers,
    )

    CLAUDE_DIR.mkdir(parents=True, exist_ok=True)
    settings_path = CLAUDE_DIR / "settings.json"
    # Claude reads user-scoped MCP servers from ~/.claude.json, not settings.json.
    claude_json_path = HOME / ".claude.json"

    configured_servers = _load_mcp_servers()

    # Ensure .credentials.json is a symlink into the shared credentials dir.
    # Claude Code's IWH atomic writer resolves symlinks before writing, so
    # tmp+rename happens in the directory mount (where rename works) instead
    # of on the old single-file bind mount (where rename returned EBUSY).
    _ensure_credentials_symlink()

    # Sync non-settings host claude files first
    _sync_host_claude_files()

    # Isolate prompt history per jail
    _isolate_claude_history()

    try:
        # --- settings.json: permissions, preferences, plugins ---
        # Start from host settings (three-way merge base), then layer
        # YOLO overrides
        host_settings = _load_host_claude_settings()

        if settings_path.exists():
            try:
                settings = json.loads(settings_path.read_text())
            except json.JSONDecodeError:
                settings = {}
        else:
            settings = {}

        # Three-way merge against the last-synced snapshot so host
        # changes propagate AND roll back, while jail-local edits
        # (Claude writes this file at runtime) are preserved.  See
        # _sync_host_settings for the per-key rules.
        try:
            prev_synced = json.loads(CLAUDE_HOST_SETTINGS_SNAPSHOT_PATH.read_text())
            if not isinstance(prev_synced, dict):
                prev_synced = {}
        except (FileNotFoundError, json.JSONDecodeError, OSError):
            prev_synced = {}
        _sync_host_settings(settings, host_settings, prev_synced)
        CLAUDE_HOST_SETTINGS_SNAPSHOT_PATH.write_text(
            json.dumps(host_settings, indent=2) + "\n"
        )

        # Remove any stale mcpServers from settings.json (moved to ~/.claude.json)
        settings.pop("mcpServers", None)

        # YOLO mode permissions — acceptEdits auto-approves tool use.
        # skipDangerousModePermissionPrompt suppresses the one-time confirmation
        # Claude shows when defaultMode is first set in a workspace.
        #
        permissions = settings.setdefault("permissions", {})
        # YOLO is ``--dangerously-skip-permissions`` on the CLI (cli.py
        # injects it into claude invocations); the per-tool allow-list
        # that used to live here was fragile and half-broken for weeks.
        # We set ``acceptEdits`` + ``additionalDirectories=["/"]`` as a
        # defence-in-depth fallback so dropping the flag degrades to
        # "prompt for non-edit bash/web" rather than "prompt for
        # everything", but the real policy is the flag.
        permissions["allow"] = []
        permissions["deny"] = []
        permissions["defaultMode"] = "acceptEdits"
        # Pre-authorize reads everywhere. The jail container is the security
        # boundary; whatever is reachable from inside is already scoped. A
        # per-directory allowlist was whack-a-mole (forgot /ctx, etc.); "/"
        # matches every path so we stop playing.
        permissions["additionalDirectories"] = ["/"]
        settings["skipDangerousModePermissionPrompt"] = True

        settings.setdefault("preferences", {})["autoUpdaterStatus"] = "disabled"

        # Enable/disable LSP plugins to match the jail's configured LSP
        # servers.  Plugins for LSPs that *were* configured on a prior
        # boot but aren't now are removed from enabledPlugins so claude
        # doesn't try to talk to a binary the bootstrap script just
        # uninstalled.
        lsp_servers = _load_lsp_servers()
        enabled_plugins = settings.setdefault("enabledPlugins", {})
        for lsp_name, plugin_id in CLAUDE_LSP_PLUGIN_MAP.items():
            if lsp_name in lsp_servers:
                enabled_plugins[plugin_id] = True
            else:
                enabled_plugins.pop(plugin_id, None)

        # ENABLE_LSP_TOOL turns on Claude's language-server tool.  Only set
        # it when at least one LSP is actually configured — leaving it on
        # in an LSP-less jail makes claude advertise a tool with nothing
        # behind it.  When LSPs are removed between boots, pop the key
        # (and the env dict if it ends up empty) so the setting doesn't
        # linger.
        env_block = settings.get("env")
        if lsp_servers:
            settings.setdefault("env", {})["ENABLE_LSP_TOOL"] = "1"
        elif isinstance(env_block, dict):
            env_block.pop("ENABLE_LSP_TOOL", None)
            if not env_block:
                settings.pop("env", None)

        settings_path.write_text(json.dumps(settings, indent=2) + "\n")

        # --- ~/.claude.json: user-scoped MCP servers ---
        if claude_json_path.exists():
            try:
                claude_json = json.loads(claude_json_path.read_text())
            except json.JSONDecodeError:
                claude_json = {}
        else:
            claude_json = {}

        mcp_servers = claude_json.setdefault("mcpServers", {})
        try:
            previous_managed = set(json.loads(CLAUDE_MANAGED_MCP_PATH.read_text()))
        except (FileNotFoundError, json.JSONDecodeError, TypeError, ValueError):
            previous_managed = set()

        for name in previous_managed:
            mcp_servers.pop(name, None)
        mcp_servers.update(configured_servers)

        # Belt-and-suspenders: mark the /workspace project as auto-approving
        # all its MCP servers.  This suppresses any secondary trust dialog
        # Claude may fire on first use of a server (`Dx$` in the 2.1.x binary
        # checks `enableAllProjectMcpServers` before returning "pending").
        # The permission-rule fix above handles the per-tool prompts; this
        # handles the per-server trust dialog, if one applies.
        workspace_project = claude_json.setdefault("projects", {}).setdefault(
            "/workspace", {}
        )
        workspace_project["enableAllProjectMcpServers"] = True
        workspace_project.setdefault("hasTrustDialogAccepted", True)

        claude_json_path.write_text(json.dumps(claude_json, indent=2) + "\n")
        CLAUDE_MANAGED_MCP_PATH.write_text(
            json.dumps(sorted(configured_servers.keys()), indent=2) + "\n"
        )
    except Exception as e:
        print(f"Error configuring Claude: {e}", file=sys.stderr)

    # Install LSP plugins if not already present (idempotent, persists across restarts).
    _install_claude_plugins(CLAUDE_LSP_PLUGIN_MAP, _load_lsp_servers())
