"""yolo-jail.jsonc parsing, merging, and validation.

Owns:
  * file/dict loading: _load_jsonc_file, _merge_lists, merge_config, load_config
  * schema constants: KNOWN_*_KEYS, *_RE patterns, JOURNAL_MODES,
    DEFAULT_HOST_CLAUDE_FILES, VALID_MCP_PRESETS, DEFAULT_MISE_TOOLS,
    DEFAULT_MISE_DISABLED_TOOLS
  * validation entry point: _validate_config and its small helpers
    (_report_unknown_keys, _validate_string_list, _validate_port_number,
    _validate_publish_port, _validate_forward_host_port,
    _check_preset_null_conflicts)
  * env_sources resolution: _parse_dotenv, _resolve_env_source_path,
    _resolve_env_sources
  * config-snapshot diffing: _config_snapshot_path, _check_config_changes
  * misc derived helpers: _normalize_blocked_tools, _merge_mise_tools,
    _merge_mise_disabled_tools, _effective_mcp_server_names

ConfigError lives here too — it's the only error type users ever see when
their yolo-jail.jsonc is malformed.
"""

import difflib
import json
import os
import re
import sys
from pathlib import Path
from typing import Any, Dict, List, Optional

import pyjson5
import typer

from .console import console
from .paths import (
    BUILTIN_CGROUP_LOOPHOLE_NAME,
    JAIL_HOST_SERVICES_DIR,
    SUPPORTED_RUNTIMES,
    USER_CONFIG_PATH,
)


class ConfigError(ValueError):
    """Raised when a yolo-jail config file or merged config is invalid."""


# ---------------------------------------------------------------------------
# Schema constants
# ---------------------------------------------------------------------------

DEFAULT_HOST_CLAUDE_FILES = ["settings.json"]

KNOWN_TOP_LEVEL_CONFIG_KEYS = {
    "runtime",
    "repo_path",
    "packages",
    "mounts",
    "workspace_readonly",
    "network",
    "security",
    "mise_tools",
    "lsp_servers",
    "mcp_servers",
    "mcp_presets",
    "devices",
    "gpu",
    "resources",
    "env_sources",
    "host_claude_files",
    "loopholes",
    "host_processes",
    "journal",
    "kvm",
    "prune",
    "ephemeral_storage",
    "include_if_found",
    "agents_md_extra",
}
JOURNAL_MODES = ("off", "user", "full")
EPHEMERAL_STORAGE_MODES = ("volume", "tmpfs")
KNOWN_NETWORK_KEYS = {"mode", "ports", "forward_host_ports"}
KNOWN_SECURITY_KEYS = {"blocked_tools"}
KNOWN_BLOCKED_TOOL_KEYS = {"name", "message", "suggestion", "block_flags"}
KNOWN_HOST_PROCESSES_KEYS = {"visible", "fields"}
KNOWN_PACKAGE_KEYS = {"name", "nixpkgs", "version", "url", "hash", "outputs"}
# Plain attribute names like "gtk4"; an optional single dotted suffix selects a
# non-default output like "gtk4.dev".  Multi-dot strings are rejected because
# nested attribute lookup (e.g. "python3Packages.numpy") is not supported.
PACKAGE_NAME_RE = re.compile(r"^[a-zA-Z0-9_-]+(\.[a-zA-Z0-9_-]+)?$")
# Output names follow the nixpkgs convention: short alphanumeric tokens.
PACKAGE_OUTPUT_RE = re.compile(r"^[a-zA-Z][a-zA-Z0-9_]*$")
KNOWN_LSP_SERVER_KEYS = {"command", "args", "fileExtensions"}
KNOWN_MCP_SERVER_KEYS = {"command", "args", "env", "requires_env"}
KNOWN_DEVICE_KEYS = {"usb", "description", "cgroup_rule"}
KNOWN_GPU_KEYS = {
    "enabled",
    "devices",
    "capabilities",
    "vendor",
    "mode",
    "hsa_override_gfx_version",
    "seccomp_unconfined",
}
KNOWN_RESOURCES_KEYS = {"memory", "cpus", "pids_limit"}
KNOWN_HOST_SERVICE_KEYS = {"command", "env", "jail_socket"}
HOST_SERVICE_NAME_RE = re.compile(r"^[a-zA-Z][a-zA-Z0-9_-]{0,63}$")
USB_ID_RE = re.compile(r"^[0-9a-fA-F]{4}:[0-9a-fA-F]{4}$")
MEMORY_RE = re.compile(r"^\d+[bkmgBKMG]?$")

VALID_MCP_PRESETS = {"chrome-devtools", "sequential-thinking"}
DEFAULT_MISE_TOOLS = {"neovim": "stable"}
DEFAULT_MISE_DISABLED_TOOLS = ("pnpm",)


# ---------------------------------------------------------------------------
# Loading + merging
# ---------------------------------------------------------------------------


def _load_jsonc_file(path: Path, label: str, *, strict: bool = False) -> Dict[str, Any]:
    if not path.exists():
        return {}
    try:
        parsed = pyjson5.loads(path.read_text())
        if isinstance(parsed, dict):
            return parsed
        msg = f"{label} must contain a top-level JSON object"
        if strict:
            raise ConfigError(msg)
        typer.echo(f"Warning: {msg}", err=True)
        return {}
    except Exception as e:
        if strict:
            raise ConfigError(f"Failed to parse {label}: {e}") from e
        typer.echo(f"Warning: Failed to parse {label}: {e}", err=True)
        return {}


def _merge_lists(base: List[Any], override: List[Any]) -> List[Any]:
    merged = list(base)
    seen = {json.dumps(item, sort_keys=True, default=str) for item in merged}
    for item in override:
        key = json.dumps(item, sort_keys=True, default=str)
        if key not in seen:
            merged.append(item)
            seen.add(key)
    return merged


def merge_config(base: Dict[str, Any], override: Dict[str, Any]) -> Dict[str, Any]:
    result = dict(base)
    for key, value in override.items():
        if key in result and isinstance(result[key], dict) and isinstance(value, dict):
            result[key] = merge_config(result[key], value)
        elif (
            key in result and isinstance(result[key], list) and isinstance(value, list)
        ):
            result[key] = _merge_lists(result[key], value)
        else:
            result[key] = value
    return result


def _load_jsonc_with_includes(
    path: Path,
    label: str,
    *,
    strict: bool = False,
    _seen: Optional[set[Path]] = None,
) -> Dict[str, Any]:
    """Load a JSONC file and merge in any ``include_if_found`` overrides.

    Include entries are relative paths resolved against the including
    file's directory.  Missing files silently skip; overrides win on
    conflict (later wins, like the user→workspace merge above).
    Includes can declare their own includes; cycles are detected and
    aborted.  The ``include_if_found`` key is consumed during loading
    and removed from the returned config — it's a loader directive,
    not a runtime field.
    """
    if _seen is None:
        _seen = set()
    try:
        resolved = path.resolve() if path.exists() else path
    except OSError:
        resolved = path
    if resolved in _seen:
        return {}
    _seen.add(resolved)

    raw = _load_jsonc_file(path, label, strict=strict)
    if not raw:
        return raw

    includes = raw.pop("include_if_found", None)
    if includes is None:
        return raw

    if not isinstance(includes, list):
        msg = f"{label}.include_if_found: expected a list of strings"
        if strict:
            raise ConfigError(msg)
        typer.echo(f"Warning: {msg}", err=True)
        return raw

    base_dir = path.parent
    result = raw
    for idx, entry in enumerate(includes):
        entry_label = f"{label}.include_if_found[{idx}]"
        if not isinstance(entry, str):
            msg = f"{entry_label}: expected a string path"
            if strict:
                raise ConfigError(msg)
            typer.echo(f"Warning: {msg}", err=True)
            continue
        if not entry:
            continue
        if entry.startswith("/") or entry.startswith("~"):
            msg = (
                f"{entry_label}: must be a relative path (got {entry!r}); "
                "absolute paths and '~' are not supported"
            )
            if strict:
                raise ConfigError(msg)
            typer.echo(f"Warning: {msg}", err=True)
            continue
        inc_path = (base_dir / entry).resolve()
        if not inc_path.exists():
            continue
        included = _load_jsonc_with_includes(
            inc_path, str(inc_path), strict=strict, _seen=_seen
        )
        result = merge_config(result, included)

    return result


def load_config(
    workspace: Optional[Path] = None, *, strict: bool = False
) -> Dict[str, Any]:
    workspace = workspace or Path.cwd()
    user_config = _load_jsonc_with_includes(
        USER_CONFIG_PATH, str(USER_CONFIG_PATH), strict=strict
    )
    workspace_config = _load_jsonc_with_includes(
        workspace / "yolo-jail.jsonc", "yolo-jail.jsonc", strict=strict
    )
    return merge_config(user_config, workspace_config)


# ---------------------------------------------------------------------------
# Derived helpers
# ---------------------------------------------------------------------------


def _filter_mcp_servers_by_env(
    mcp_servers: Optional[Dict[str, Any]],
    env_map: Dict[str, str],
) -> Optional[Dict[str, Any]]:
    """Drop MCP servers whose ``requires_env`` gate isn't satisfied.

    A server declaring ``requires_env: ["TAVILY_API_KEY"]`` is removed
    when any listed variable is unset or empty in ``env_map``.  Lets a
    dotfiles-shared user config declare machine-dependent servers that
    only activate where the secrets exist.  Null entries (preset
    removals) and the in-jail gating in
    ``entrypoint.agent_configs._load_mcp_servers`` are unaffected — this
    host-side copy only keeps the AGENTS.md briefing's server list
    honest.
    """
    if not isinstance(mcp_servers, dict):
        return mcp_servers
    filtered: Dict[str, Any] = {}
    for name, cfg in mcp_servers.items():
        if isinstance(cfg, dict):
            required = cfg.get("requires_env")
            if isinstance(required, list) and any(
                isinstance(v, str) and not env_map.get(v) for v in required
            ):
                continue
        filtered[name] = cfg
    return filtered


def _effective_mcp_server_names(
    mcp_servers: Optional[Dict[str, Any]] = None,
    mcp_presets: Optional[List[str]] = None,
) -> List[str]:
    """Return the effective MCP server names after presets + config overrides/removals."""
    # Start with preset names
    names = list(mcp_presets or [])

    if not isinstance(mcp_servers, dict):
        return names

    for name, cfg in mcp_servers.items():
        if cfg is None:
            if name in names:
                names.remove(name)
            continue
        if isinstance(cfg, dict) and name not in names:
            names.append(name)
    return names


def _merge_mise_tools(config: Dict[str, Any]) -> Dict[str, Any]:
    """Merge built-in mise defaults with config overrides."""
    return {**DEFAULT_MISE_TOOLS, **config.get("mise_tools", {})}


def _merge_mise_disabled_tools(user_value: Any = "") -> str:
    """Return MISE_DISABLE_TOOLS with yolo-managed package managers included."""
    tools: list[str] = []
    for tool in DEFAULT_MISE_DISABLED_TOOLS:
        if tool not in tools:
            tools.append(tool)
    if isinstance(user_value, str):
        for tool in user_value.replace(",", " ").split():
            if tool and tool not in tools:
                tools.append(tool)
    return ",".join(tools)


def _normalize_blocked_tools(
    security_section: Optional[Dict[str, Any]],
) -> List[Dict[str, Any]]:
    """Normalize blocked tool config into the format consumed by the entrypoint."""
    if security_section is None:
        security_section = {}

    raw_blocked = security_section.get("blocked_tools", ["grep", "find"])
    if raw_blocked is None:
        raw_blocked = ["grep", "find"]

    default_messages: Dict[str, Dict[str, Any]] = {
        "grep": {
            "message": "grep's recursive mode is blocked. Use ripgrep (rg) for recursive searches; pipe filters and single-file greps pass through.",
            "suggestion": "Try: rg <pattern> [path]",
            # Only block when argv contains a recursive flag.  Patterns
            # are shell ``case`` globs.  The entrypoint's shim generator
            # splits ``--*`` patterns into exact long-match arm (first),
            # then skips any other ``--*`` argv (so ``--regex`` /
            # ``--regexp`` aren't false-positive'd by the short-bundle
            # pattern below), then matches the remaining short patterns.
            "block_flags": ["--recursive", "-r", "-R", "-*[rR]*"],
        },
        "find": {
            "message": "find is blocked to prevent unintended recursive searches. Use fd for a faster, more intuitive alternative.",
            "suggestion": "Try: fd <pattern>",
        },
    }

    normalized_blocked: List[Dict[str, Any]] = []
    for tool in raw_blocked:
        if isinstance(tool, str):
            merged = {"name": tool}
            if tool in default_messages:
                merged.update(default_messages[tool])
            normalized_blocked.append(merged)
        elif isinstance(tool, dict) and "name" in tool:
            # Merge defaults with user fields — user wins on conflict
            # but unspecified fields inherit.  Without this,
            # ``{"name": "grep"}`` in a workspace config would
            # silently lose the default ``block_flags`` and revert
            # to unconditional blocking.  Explicit override: user
            # can pass ``"block_flags": []`` to turn it off.
            name = tool["name"]
            merged = dict(default_messages.get(name, {}))
            merged.update(tool)
            normalized_blocked.append(merged)
    return normalized_blocked


# ---------------------------------------------------------------------------
# Validation
# ---------------------------------------------------------------------------


def _report_unknown_keys(
    mapping: Dict[str, Any], allowed: set[str], path: str, errors: List[str]
):
    for key in sorted(mapping):
        if key not in allowed:
            errors.append(f"{path}.{key}: unknown key")


def _validate_string_list(values: Any, path: str, errors: List[str]):
    if not isinstance(values, list):
        errors.append(f"{path}: expected a list")
        return
    for idx, value in enumerate(values):
        if not isinstance(value, str):
            errors.append(f"{path}[{idx}]: expected a string")


def _validate_port_number(value: Any, path: str, errors: List[str]):
    try:
        port = int(value)
    except (TypeError, ValueError):
        errors.append(f"{path}: expected an integer port number")
        return
    if port < 1 or port > 65535:
        errors.append(f"{path}: port must be between 1 and 65535")


def _validate_publish_port(value: Any, path: str, errors: List[str]):
    if not isinstance(value, str):
        errors.append(f"{path}: expected a string like '8000:8000'")
        return
    base = value
    if "/" in base:
        base, protocol = base.rsplit("/", 1)
        if protocol not in ("tcp", "udp"):
            errors.append(f"{path}: protocol must be tcp or udp")
    parts = base.split(":")
    if len(parts) == 2:
        host_port, container_port = parts
    elif len(parts) == 3:
        _, host_port, container_port = parts
    else:
        errors.append(f"{path}: expected 'host:container' or 'ip:host:container'")
        return
    _validate_port_number(host_port, f"{path}.host", errors)
    _validate_port_number(container_port, f"{path}.container", errors)


def _validate_forward_host_port(value: Any, path: str, errors: List[str]):
    if isinstance(value, int):
        _validate_port_number(value, path, errors)
        return
    if not isinstance(value, str):
        errors.append(f"{path}: expected an int or string like '8080:9090'")
        return
    parts = value.split(":")
    if len(parts) == 1:
        _validate_port_number(parts[0], path, errors)
        return
    if len(parts) == 2:
        _validate_port_number(parts[0], f"{path}.local", errors)
        _validate_port_number(parts[1], f"{path}.host", errors)
        return
    errors.append(f"{path}: expected '<port>' or '<local>:<host>'")


def _check_preset_null_conflicts(config: Dict[str, Any], label: str) -> List[str]:
    """Report same-file preset/null contradictions.

    Cross-hierarchy conflicts (user-level preset + workspace-level null) are
    valid and intentional, so this only checks within a single config file.
    """
    errors: List[str] = []
    presets = config.get("mcp_presets")
    servers = config.get("mcp_servers")
    if not isinstance(presets, list) or not isinstance(servers, dict):
        return errors
    for name in presets:
        if isinstance(name, str) and name in servers and servers[name] is None:
            errors.append(
                f"{label}: preset '{name}' is enabled in mcp_presets but "
                f"null-removed in mcp_servers within the same config file"
            )
    return errors


def _validate_config(
    config: Dict[str, Any], workspace: Optional[Path] = None
) -> tuple[List[str], List[str]]:
    errors: List[str] = []
    warnings: List[str] = []
    workspace = workspace or Path.cwd()

    _report_unknown_keys(config, KNOWN_TOP_LEVEL_CONFIG_KEYS, "config", errors)

    runtime = config.get("runtime")
    if runtime == "docker":
        errors.append(
            "config.runtime: 'docker' is no longer supported — "
            "use 'podman' (Linux) or 'container' (macOS Apple Container)"
        )
    elif runtime is not None and runtime not in SUPPORTED_RUNTIMES:
        errors.append("config.runtime: expected 'podman' or 'container'")

    repo_path = config.get("repo_path")
    if repo_path is not None and not isinstance(repo_path, str):
        errors.append("config.repo_path: expected a string path")

    packages = config.get("packages")
    if packages is not None:
        if not isinstance(packages, list):
            errors.append("config.packages: expected a list")
        else:
            for idx, pkg in enumerate(packages):
                path = f"config.packages[{idx}]"
                if isinstance(pkg, str):
                    if not PACKAGE_NAME_RE.match(pkg):
                        errors.append(
                            f"{path}: invalid package name {pkg!r}; "
                            "expected '<name>' or '<name>.<output>' "
                            "(letters, digits, '_' and '-' only; at most one dot)"
                        )
                    continue
                if not isinstance(pkg, dict):
                    errors.append(f"{path}: expected a string or object")
                    continue
                _report_unknown_keys(pkg, KNOWN_PACKAGE_KEYS, path, errors)
                name = pkg.get("name")
                if not isinstance(name, str):
                    errors.append(f"{path}.name: expected a string")
                elif "." in name:
                    errors.append(
                        f"{path}.name: dotted output shorthand ('gtk4.dev') is "
                        "string-only; use the 'outputs' field on the object form"
                    )
                outputs = pkg.get("outputs")
                if "outputs" in pkg:
                    if not isinstance(outputs, list) or not all(
                        isinstance(o, str) for o in outputs
                    ):
                        errors.append(
                            f"{path}.outputs: expected a list of strings "
                            '(e.g. ["out", "dev"])'
                        )
                    else:
                        for o_idx, out in enumerate(outputs):
                            if not PACKAGE_OUTPUT_RE.match(out):
                                errors.append(
                                    f"{path}.outputs[{o_idx}]: invalid output name "
                                    f"{out!r} (common values: out, dev, bin, lib, "
                                    "man, doc)"
                                )
                has_nixpkgs = "nixpkgs" in pkg
                has_version_override = any(
                    key in pkg for key in ("version", "url", "hash")
                )
                has_outputs = "outputs" in pkg
                if has_nixpkgs:
                    if not isinstance(pkg.get("nixpkgs"), str):
                        errors.append(f"{path}.nixpkgs: expected a string")
                    if has_version_override:
                        errors.append(
                            f"{path}: use either nixpkgs pinning or version/url/hash overrides, not both"
                        )
                elif has_version_override:
                    for key in ("version", "url", "hash"):
                        if not isinstance(pkg.get(key), str):
                            errors.append(f"{path}.{key}: expected a string")
                elif not has_outputs:
                    errors.append(
                        f"{path}: object packages must use 'nixpkgs', "
                        "'version'+'url'+'hash', or 'outputs'"
                    )

    mounts = config.get("mounts")
    if mounts is not None:
        if not isinstance(mounts, list):
            errors.append("config.mounts: expected a list")
        else:
            for idx, mount in enumerate(mounts):
                path = f"config.mounts[{idx}]"
                if not isinstance(mount, str):
                    errors.append(f"{path}: expected a string")
                    continue
                colon_idx = mount.rfind(":")
                host_path = mount
                if colon_idx > 0 and mount[colon_idx + 1 : colon_idx + 2] == "/":
                    host_path = mount[:colon_idx]
                    container_path = mount[colon_idx + 1 :]
                    if not container_path.startswith("/"):
                        errors.append(f"{path}: container mount path must be absolute")
                if not host_path:
                    errors.append(f"{path}: host mount path cannot be empty")
                    continue
                resolved_host = Path(host_path).expanduser().resolve()
                if not resolved_host.exists():
                    warnings.append(
                        f"{path}: host path does not exist and will be skipped: {resolved_host}"
                    )

    workspace_readonly = config.get("workspace_readonly")
    if workspace_readonly is not None:
        if not isinstance(workspace_readonly, list):
            errors.append("config.workspace_readonly: expected a list of strings")
        else:
            for idx, entry in enumerate(workspace_readonly):
                path = f"config.workspace_readonly[{idx}]"
                if not isinstance(entry, str):
                    errors.append(f"{path}: expected a string")
                elif entry.startswith("/"):
                    errors.append(f"{path}: must be a relative path, not absolute")
                elif ".." in entry.split("/"):
                    errors.append(f"{path}: must not contain '..' components")

    host_claude_files = config.get("host_claude_files")
    if host_claude_files is not None:
        if not isinstance(host_claude_files, list):
            errors.append("config.host_claude_files: expected a list of strings")
        else:
            for idx, entry in enumerate(host_claude_files):
                if not isinstance(entry, str):
                    errors.append(f"config.host_claude_files[{idx}]: expected a string")
                elif "/" in entry or "\\" in entry:
                    errors.append(
                        f"config.host_claude_files[{idx}]: must be a filename, not a path"
                    )

    host_services = config.get("loopholes")
    if host_services is not None:
        if not isinstance(host_services, dict):
            errors.append("config.loopholes: expected an object")
        else:
            for name, spec in host_services.items():
                path = f"config.loopholes.{name}"
                if not isinstance(name, str) or not HOST_SERVICE_NAME_RE.match(name):
                    errors.append(
                        f"config.loopholes: service name {name!r} must match "
                        f"^[a-zA-Z][a-zA-Z0-9_-]{{0,63}}$"
                    )
                    continue
                if name == BUILTIN_CGROUP_LOOPHOLE_NAME:
                    errors.append(
                        f"{path}: '{BUILTIN_CGROUP_LOOPHOLE_NAME}' is reserved "
                        "for the built-in cgroup delegate service"
                    )
                    continue
                if not isinstance(spec, dict):
                    errors.append(f"{path}: expected an object")
                    continue
                _report_unknown_keys(spec, KNOWN_HOST_SERVICE_KEYS, path, errors)
                cmd = spec.get("command")
                if cmd is None:
                    errors.append(f"{path}.command: required")
                elif not isinstance(cmd, list) or not cmd:
                    errors.append(
                        f"{path}.command: expected a non-empty list of strings"
                    )
                else:
                    for ci, ca in enumerate(cmd):
                        if not isinstance(ca, str):
                            errors.append(
                                f"{path}.command[{ci}]: expected a string, got {type(ca).__name__}"
                            )
                env = spec.get("env")
                if env is not None:
                    if not isinstance(env, dict):
                        errors.append(f"{path}.env: expected an object")
                    else:
                        for k, v in env.items():
                            if not isinstance(k, str) or not isinstance(v, str):
                                errors.append(
                                    f"{path}.env: keys and values must be strings"
                                )
                                break
                jail_socket = spec.get("jail_socket")
                if jail_socket is not None:
                    if not isinstance(jail_socket, str):
                        errors.append(f"{path}.jail_socket: expected a string")
                    elif not jail_socket.startswith(JAIL_HOST_SERVICES_DIR + "/"):
                        errors.append(
                            f"{path}.jail_socket: must start with "
                            f"{JAIL_HOST_SERVICES_DIR}/ "
                            f"(got {jail_socket!r})"
                        )

    journal = config.get("journal")
    if journal is not None and not isinstance(journal, bool):
        if not isinstance(journal, str) or journal not in JOURNAL_MODES:
            errors.append(
                f"config.journal: expected one of {list(JOURNAL_MODES)} "
                f"or a boolean (got {journal!r})"
            )

    kvm = config.get("kvm")
    if kvm is not None and not isinstance(kvm, bool):
        errors.append(f"config.kvm: expected a boolean (got {kvm!r})")

    ephemeral_storage = config.get("ephemeral_storage")
    if ephemeral_storage is not None and (
        not isinstance(ephemeral_storage, str)
        or ephemeral_storage not in EPHEMERAL_STORAGE_MODES
    ):
        errors.append(
            f"config.ephemeral_storage: expected one of "
            f"{list(EPHEMERAL_STORAGE_MODES)} (got {ephemeral_storage!r})"
        )

    network = config.get("network")
    if network is not None:
        if not isinstance(network, dict):
            errors.append("config.network: expected an object")
        else:
            _report_unknown_keys(network, KNOWN_NETWORK_KEYS, "config.network", errors)
            mode = network.get("mode")
            if mode is not None and mode not in ("bridge", "host"):
                errors.append("config.network.mode: expected 'bridge' or 'host'")
            ports = network.get("ports")
            if ports is not None:
                if not isinstance(ports, list):
                    errors.append("config.network.ports: expected a list")
                else:
                    for idx, port in enumerate(ports):
                        _validate_publish_port(
                            port, f"config.network.ports[{idx}]", errors
                        )
            forward_host_ports = network.get("forward_host_ports")
            if forward_host_ports is not None:
                if not isinstance(forward_host_ports, list):
                    errors.append("config.network.forward_host_ports: expected a list")
                else:
                    for idx, port in enumerate(forward_host_ports):
                        _validate_forward_host_port(
                            port,
                            f"config.network.forward_host_ports[{idx}]",
                            errors,
                        )
            if mode == "host":
                if network.get("ports"):
                    warnings.append(
                        "config.network.ports: ignored when network.mode is 'host'"
                    )
                if network.get("forward_host_ports"):
                    warnings.append(
                        "config.network.forward_host_ports: ignored when network.mode is 'host'"
                    )

    security = config.get("security")
    if security is not None:
        if not isinstance(security, dict):
            errors.append("config.security: expected an object")
        else:
            _report_unknown_keys(
                security, KNOWN_SECURITY_KEYS, "config.security", errors
            )
            blocked_tools = security.get("blocked_tools")
            if blocked_tools is not None:
                if not isinstance(blocked_tools, list):
                    errors.append("config.security.blocked_tools: expected a list")
                else:
                    for idx, tool in enumerate(blocked_tools):
                        path = f"config.security.blocked_tools[{idx}]"
                        if isinstance(tool, str):
                            continue
                        if not isinstance(tool, dict):
                            errors.append(f"{path}: expected a string or object")
                            continue
                        _report_unknown_keys(
                            tool, KNOWN_BLOCKED_TOOL_KEYS, path, errors
                        )
                        if not isinstance(tool.get("name"), str):
                            errors.append(f"{path}.name: expected a string")
                        for key in ("message", "suggestion"):
                            if key in tool and not isinstance(tool.get(key), str):
                                errors.append(f"{path}.{key}: expected a string")
                        if "block_flags" in tool:
                            bf = tool.get("block_flags")
                            if not isinstance(bf, list) or not all(
                                isinstance(x, str) for x in bf
                            ):
                                errors.append(
                                    f"{path}.block_flags: expected a list of strings"
                                )

    host_processes = config.get("host_processes")
    if host_processes is not None:
        if not isinstance(host_processes, dict):
            errors.append("config.host_processes: expected an object")
        else:
            _report_unknown_keys(
                host_processes,
                KNOWN_HOST_PROCESSES_KEYS,
                "config.host_processes",
                errors,
            )
            for list_key in ("visible", "fields"):
                if list_key in host_processes:
                    val = host_processes.get(list_key)
                    if not isinstance(val, list) or not all(
                        isinstance(x, str) for x in val
                    ):
                        errors.append(
                            f"config.host_processes.{list_key}: "
                            "expected a list of strings"
                        )

    mise_tools = config.get("mise_tools")
    if mise_tools is not None:
        if not isinstance(mise_tools, dict):
            errors.append("config.mise_tools: expected an object")
        else:
            for key, value in mise_tools.items():
                if not isinstance(key, str):
                    errors.append("config.mise_tools: tool names must be strings")
                if not isinstance(value, str):
                    errors.append(f"config.mise_tools.{key}: expected a version string")

    lsp_servers = config.get("lsp_servers")
    if lsp_servers is not None:
        if not isinstance(lsp_servers, dict):
            errors.append("config.lsp_servers: expected an object")
        else:
            for name, cfg in lsp_servers.items():
                path = f"config.lsp_servers.{name}"
                if not isinstance(cfg, dict):
                    errors.append(f"{path}: expected an object")
                    continue
                _report_unknown_keys(cfg, KNOWN_LSP_SERVER_KEYS, path, errors)
                if not isinstance(cfg.get("command"), str):
                    errors.append(f"{path}.command: expected a string")
                if "args" in cfg:
                    _validate_string_list(cfg["args"], f"{path}.args", errors)
                file_extensions = cfg.get("fileExtensions")
                if not isinstance(file_extensions, dict):
                    errors.append(f"{path}.fileExtensions: expected an object")
                else:
                    for ext, lang in file_extensions.items():
                        if not isinstance(ext, str) or not isinstance(lang, str):
                            errors.append(
                                f"{path}.fileExtensions: keys and values must be strings"
                            )

    mcp_presets = config.get("mcp_presets")
    if mcp_presets is not None:
        if not isinstance(mcp_presets, list):
            errors.append("config.mcp_presets: expected an array of preset names")
        else:
            for idx, name in enumerate(mcp_presets):
                if not isinstance(name, str):
                    errors.append(f"config.mcp_presets[{idx}]: expected a string")
                elif name not in VALID_MCP_PRESETS:
                    errors.append(
                        f"config.mcp_presets[{idx}]: unknown preset '{name}'. "
                        f"Valid presets: {', '.join(sorted(VALID_MCP_PRESETS))}"
                    )

    mcp_servers = config.get("mcp_servers")
    if mcp_servers is not None:
        if not isinstance(mcp_servers, dict):
            errors.append("config.mcp_servers: expected an object")
        else:
            for name, cfg in mcp_servers.items():
                path = f"config.mcp_servers.{name}"
                if cfg is None:
                    continue
                if not isinstance(cfg, dict):
                    errors.append(f"{path}: expected an object or null")
                    continue
                _report_unknown_keys(cfg, KNOWN_MCP_SERVER_KEYS, path, errors)
                if not isinstance(cfg.get("command"), str):
                    errors.append(f"{path}.command: expected a string")
                if "args" in cfg:
                    _validate_string_list(cfg["args"], f"{path}.args", errors)
                if "env" in cfg:
                    env = cfg["env"]
                    if not isinstance(env, dict):
                        errors.append(f"{path}.env: expected an object")
                    else:
                        for k, v in env.items():
                            if not isinstance(k, str) or not isinstance(v, str):
                                errors.append(
                                    f"{path}.env.{k}: expected string keys and values"
                                )
                                break
                if "requires_env" in cfg:
                    req = cfg["requires_env"]
                    if not isinstance(req, list):
                        errors.append(
                            f"{path}.requires_env: expected a list of env var names"
                        )
                    else:
                        for r_idx, var in enumerate(req):
                            if not isinstance(var, str) or not re.match(
                                r"^[A-Za-z_][A-Za-z0-9_]*$", var
                            ):
                                errors.append(
                                    f"{path}.requires_env[{r_idx}]: invalid env var "
                                    f"name {var!r} (must match [A-Za-z_][A-Za-z0-9_]*)"
                                )

    devices = config.get("devices")
    if devices is not None:
        if not isinstance(devices, list):
            errors.append("config.devices: expected a list")
        else:
            for idx, device in enumerate(devices):
                path = f"config.devices[{idx}]"
                if isinstance(device, str):
                    if not Path(device).exists():
                        warnings.append(
                            f"{path}: device path does not exist and may be skipped: {device}"
                        )
                    continue
                if not isinstance(device, dict):
                    errors.append(f"{path}: expected a string or object")
                    continue
                _report_unknown_keys(device, KNOWN_DEVICE_KEYS, path, errors)
                has_usb = "usb" in device
                has_cgroup = "cgroup_rule" in device
                if has_usb == has_cgroup:
                    errors.append(
                        f"{path}: expected exactly one of 'usb' or 'cgroup_rule'"
                    )
                    continue
                if has_usb:
                    if not isinstance(device.get("usb"), str):
                        errors.append(f"{path}.usb: expected a string")
                    elif not USB_ID_RE.match(device["usb"]):
                        errors.append(
                            f"{path}.usb: expected vendor:product hex format like '0bda:2838'"
                        )
                    if "description" in device and not isinstance(
                        device.get("description"), str
                    ):
                        errors.append(f"{path}.description: expected a string")
                if has_cgroup and not isinstance(device.get("cgroup_rule"), str):
                    errors.append(f"{path}.cgroup_rule: expected a string")

    # GPU config validation
    gpu = config.get("gpu")
    if gpu is not None:
        if not isinstance(gpu, dict):
            errors.append("config.gpu: expected an object")
        else:
            _report_unknown_keys(gpu, KNOWN_GPU_KEYS, "config.gpu", errors)
            enabled = gpu.get("enabled")
            if enabled is not None and not isinstance(enabled, bool):
                errors.append("config.gpu.enabled: expected a boolean")

            vendor = gpu.get("vendor")
            if vendor is not None and vendor not in ("nvidia", "amd"):
                errors.append("config.gpu.vendor: expected 'nvidia' or 'amd'")
            is_amd = vendor == "amd"

            devices_val = gpu.get("devices")
            if devices_val is not None and not isinstance(devices_val, str):
                errors.append(
                    "config.gpu.devices: expected a string ('all', '0', or '0,1')"
                )

            mode = gpu.get("mode")
            if mode is not None:
                if not is_amd:
                    errors.append("config.gpu.mode: only valid when vendor='amd'")
                elif mode not in ("devices", "cdi"):
                    errors.append("config.gpu.mode: expected 'devices' or 'cdi'")

            capabilities = gpu.get("capabilities")
            if capabilities is not None:
                if is_amd:
                    errors.append(
                        "config.gpu.capabilities: not supported for vendor='amd' "
                        "(ROCm has no driver-capabilities concept)"
                    )
                elif not isinstance(capabilities, str):
                    errors.append(
                        "config.gpu.capabilities: expected a string (e.g. 'compute,utility')"
                    )
                else:
                    valid_caps = {
                        "compute",
                        "utility",
                        "graphics",
                        "video",
                        "display",
                        "compat32",
                    }
                    for cap in capabilities.split(","):
                        cap = cap.strip()
                        if cap and cap not in valid_caps:
                            errors.append(
                                f"config.gpu.capabilities: unknown capability '{cap}'. "
                                f"Valid: {', '.join(sorted(valid_caps))}"
                            )

            gfx = gpu.get("hsa_override_gfx_version")
            if gfx is not None:
                if not is_amd:
                    errors.append(
                        "config.gpu.hsa_override_gfx_version: only valid when vendor='amd'"
                    )
                elif not isinstance(gfx, str):
                    errors.append(
                        "config.gpu.hsa_override_gfx_version: expected a string (e.g. '11.0.0')"
                    )

            seccomp = gpu.get("seccomp_unconfined")
            if seccomp is not None and not isinstance(seccomp, bool):
                errors.append("config.gpu.seccomp_unconfined: expected a boolean")

    # Resources config validation
    resources = config.get("resources")
    if resources is not None:
        if not isinstance(resources, dict):
            errors.append("config.resources: expected an object")
        else:
            _report_unknown_keys(
                resources, KNOWN_RESOURCES_KEYS, "config.resources", errors
            )
            memory = resources.get("memory")
            if memory is not None:
                if not isinstance(memory, str):
                    errors.append(
                        "config.resources.memory: expected a string (e.g. '8g', '512m')"
                    )
                elif not MEMORY_RE.match(memory):
                    errors.append(
                        "config.resources.memory: invalid format. "
                        "Use a number with optional suffix: b, k, m, g (e.g. '8g', '512m')"
                    )
            cpus = resources.get("cpus")
            if cpus is not None:
                if isinstance(cpus, (int, float)):
                    if cpus <= 0:
                        errors.append(
                            "config.resources.cpus: must be a positive number"
                        )
                elif isinstance(cpus, str):
                    try:
                        val = float(cpus)
                        if val <= 0:
                            errors.append(
                                "config.resources.cpus: must be a positive number"
                            )
                    except ValueError:
                        errors.append(
                            "config.resources.cpus: expected a number (e.g. 4, 2.5, '0.5')"
                        )
                else:
                    errors.append(
                        "config.resources.cpus: expected a number (e.g. 4, 2.5, '0.5')"
                    )
            pids_limit = resources.get("pids_limit")
            if pids_limit is not None:
                if not isinstance(pids_limit, int) or pids_limit <= 0:
                    errors.append(
                        "config.resources.pids_limit: expected a positive integer"
                    )

    include_if_found = config.get("include_if_found")
    if include_if_found is not None:
        if not isinstance(include_if_found, list):
            errors.append(
                "config.include_if_found: expected a list of relative path strings"
            )
        else:
            for idx, entry in enumerate(include_if_found):
                path = f"config.include_if_found[{idx}]"
                if not isinstance(entry, str):
                    errors.append(f"{path}: expected a string")
                elif not entry:
                    errors.append(f"{path}: empty string is not a valid path")
                elif entry.startswith("/") or entry.startswith("~"):
                    errors.append(
                        f"{path}: must be a relative path (got {entry!r}); "
                        "absolute paths and '~' are not supported"
                    )

    agents_md_extra = config.get("agents_md_extra")
    if agents_md_extra is not None and not isinstance(agents_md_extra, str):
        errors.append("config.agents_md_extra: expected a string of markdown")

    # env_sources validation — ordered list of str (file path) or dict (inline map)
    if "env" in config:
        errors.append(
            "config.env: removed — rename to 'env_sources' (an ordered list where "
            'strings are KEY=VALUE files and objects are inline {"KEY": "VALUE"} sets). '
            "See `yolo config-ref`."
        )
    env_sources = config.get("env_sources")
    if env_sources is not None:
        if not isinstance(env_sources, list):
            errors.append(
                "config.env_sources: expected a list of strings (file paths) "
                "or objects (inline env maps)"
            )
        else:
            for idx, entry in enumerate(env_sources):
                path = f"config.env_sources[{idx}]"
                if isinstance(entry, str):
                    if not entry:
                        errors.append(f"{path}: empty string is not a valid path")
                elif isinstance(entry, dict):
                    for key, value in entry.items():
                        if not isinstance(key, str) or not key:
                            errors.append(
                                f"{path}: inline map keys must be non-empty strings"
                            )
                        elif not re.match(r"^[A-Za-z_][A-Za-z0-9_]*$", key):
                            errors.append(
                                f"{path}.{key}: invalid variable name "
                                "(must match [A-Za-z_][A-Za-z0-9_]*)"
                            )
                        if not isinstance(value, str):
                            errors.append(f"{path}.{key}: expected a string value")
                else:
                    errors.append(
                        f"{path}: expected a string (file path) or object (inline map), "
                        f"got {type(entry).__name__}"
                    )

    return errors, warnings


# ---------------------------------------------------------------------------
# env_sources resolution
# ---------------------------------------------------------------------------


def _parse_dotenv(text: str) -> Dict[str, str]:
    """Parse KEY=VALUE dotenv content into a dict.

    Lines starting with ``#`` and blank lines are ignored.  ``export``
    prefix is allowed and stripped.  Values may be wrapped in matching
    single or double quotes; inner whitespace is preserved.  Malformed
    lines (no ``=``, invalid variable name) are silently skipped — the
    caller has already validated the file path; dropping one junk line
    is better than refusing to start the jail over a typo in a secrets
    file.
    """
    out: Dict[str, str] = {}
    for raw in text.splitlines():
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        if line.startswith("export "):
            line = line[len("export ") :].lstrip()
        if "=" not in line:
            continue
        key, value = line.split("=", 1)
        key = key.strip()
        if not re.match(r"^[A-Za-z_][A-Za-z0-9_]*$", key):
            continue
        value = value.strip()
        if len(value) >= 2 and value[0] == value[-1] and value[0] in ("'", '"'):
            value = value[1:-1]
        out[key] = value
    return out


def _resolve_env_source_path(entry: str, workspace: Path) -> Path:
    """Resolve an env_sources string entry to an absolute filesystem path.

    Supports ``~`` expansion, absolute paths, and workspace-relative
    paths.  Relative paths resolve against the workspace root so
    workspace-level configs can reference files inside the repo, and
    user-level configs typically use ``~/...`` or absolute paths.
    """
    expanded = os.path.expanduser(entry)
    p = Path(expanded)
    if not p.is_absolute():
        p = (workspace / p).resolve()
    return p


def _resolve_env_sources(workspace: Path, config: Dict[str, Any]) -> Dict[str, str]:
    """Resolve ``env_sources`` into the final env map injected into the jail.

    Iterates entries in order: inline dicts apply directly; string
    entries are read as KEY=VALUE dotenv files.  Later entries override
    earlier ones.  Missing files emit a warning and are skipped — start
    the jail with whatever env is available rather than failing on an
    absent secrets file.
    """
    merged: Dict[str, str] = {}
    for entry in config.get("env_sources", []) or []:
        if isinstance(entry, dict):
            for k, v in entry.items():
                if isinstance(k, str) and isinstance(v, str):
                    merged[k] = v
        elif isinstance(entry, str):
            path = _resolve_env_source_path(entry, workspace)
            if not path.exists():
                console.print(
                    f"[yellow]Warning: env_sources file not found, skipping: "
                    f"{entry} (resolved to {path})[/yellow]"
                )
                continue
            try:
                text = path.read_text()
            except OSError as e:
                console.print(
                    f"[yellow]Warning: env_sources file unreadable, skipping: "
                    f"{entry}: {e}[/yellow]"
                )
                continue
            merged.update(_parse_dotenv(text))
    return merged


# ---------------------------------------------------------------------------
# Config-snapshot diff
# ---------------------------------------------------------------------------


def _config_snapshot_path(workspace: Path) -> Path:
    """Path to the normalized config snapshot for change detection."""
    return workspace / ".yolo" / "config-snapshot.json"


def _check_config_changes(workspace: Path, config: Dict[str, Any]) -> bool:
    """Compare config with last-seen snapshot. Returns True to proceed, False to abort."""
    snapshot_path = _config_snapshot_path(workspace)
    current_json = json.dumps(config, indent=2, sort_keys=True)

    # First run or no snapshot — accept and save
    if not snapshot_path.exists():
        snapshot_path.parent.mkdir(parents=True, exist_ok=True)
        snapshot_path.write_text(current_json + "\n")
        return True

    old_json = snapshot_path.read_text().rstrip()
    if old_json == current_json:
        return True

    # Show diff
    diff_lines = list(
        difflib.unified_diff(
            old_json.splitlines(),
            current_json.splitlines(),
            fromfile="previous config",
            tofile="current config",
            lineterm="",
        )
    )

    console.print(
        "\n[bold yellow]⚠  Jail config changed since last run:[/bold yellow]\n"
    )
    for line in diff_lines:
        if line.startswith("+++") or line.startswith("---"):
            console.print(f"[dim]{line}[/dim]")
        elif line.startswith("+"):
            console.print(f"[green]{line}[/green]")
        elif line.startswith("-"):
            console.print(f"[red]{line}[/red]")
        elif line.startswith("@@"):
            console.print(f"[cyan]{line}[/cyan]")
        else:
            console.print(line)

    if not sys.stdin.isatty():
        console.print(
            "\n[yellow]Non-interactive mode: accepting config changes automatically.[/yellow]"
        )
        snapshot_path.write_text(current_json + "\n")
        return True

    console.print()
    try:
        response = input("Accept these config changes? [y/N] ").strip().lower()
    except (EOFError, KeyboardInterrupt):
        console.print("\n[red]Aborted.[/red]")
        return False

    if response in ("y", "yes"):
        snapshot_path.write_text(current_json + "\n")
        return True

    console.print("[red]Config changes rejected. Exiting.[/red]")
    return False
