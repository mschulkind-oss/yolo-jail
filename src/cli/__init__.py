import dataclasses
import difflib
import fcntl
import os
import platform
import re
import signal
import socket
import struct
import subprocess
import sys
import json
import shlex
import shutil
import hashlib
import time
import tempfile
import threading
from dataclasses import dataclass
from pathlib import Path
from typing import Callable, Optional, List, Dict, Any, Union
import typer
import pyjson5
from rich.console import Console

from src import loopholes as _loopholes
from .paths import (
    AGENTS_DIR,
    BUILD_DIR,
    BUILTIN_CGROUP_LOOPHOLE_NAME,
    BUILTIN_JOURNAL_LOOPHOLE_NAME,
    CGD_SOCKET_NAME,
    CONTAINER_DIR,
    GLOBAL_CACHE,
    GLOBAL_HOME,
    GLOBAL_MISE,
    GLOBAL_STORAGE,
    IS_LINUX,
    IS_MACOS,
    JAIL_HOST_SERVICES_DIR,
    JAIL_IMAGE,
    JAIL_IMAGE_SHORT,
    JOURNAL_SOCKET_NAME,
    SUPPORTED_RUNTIMES,
    USER_CONFIG_PATH,
)
from .version import (
    _container_baked_yolo_version,
    _get_yolo_version,
    _git_describe_version,
    _version_callback,
)
from .terminal import (
    _get_project_name,
    _kitty_setup_jail_tab,
    _print_startup_banner,
    _tmux_rename_window,
    _tmux_setup_jail_pane,
)
from .image import (
    _add_loaded_path,
    _build_image_store_path,
    _convert_via_daemon,
    _convert_via_skopeo,
    _estimate_image_size,
    _format_progress,
    _image_cache_path,
    _image_inspect_cmd,
    _image_load_cmd,
    _jail_image,
    _load_image_for_apple_container,
    _materialize_image,
    _read_loaded_paths,
    _stream_image_command,
    _summarize_nix_line,
    auto_load_image,
)
from .runtime import (
    _check_container_stuck,
    _detect_runtime,
    _detect_runtime_for_listing,
    _get_container_workspace,
    _is_apple_container,
    _remove_stale_container,
    _resolve_container_cgroup,
    _runtime,
    _runtime_for_check,
    _runtime_is_connectable,
    cleanup_container_tracking,
    container_name_for_workspace,
    find_existing_container,
    find_running_container,
    write_container_tracking,
)
from .config import (
    DEFAULT_HOST_CLAUDE_FILES,
    DEFAULT_MISE_DISABLED_TOOLS,
    DEFAULT_MISE_TOOLS,
    HOST_SERVICE_NAME_RE,
    JOURNAL_MODES,
    KNOWN_BLOCKED_TOOL_KEYS,
    KNOWN_DEVICE_KEYS,
    KNOWN_GPU_KEYS,
    KNOWN_HOST_PROCESSES_KEYS,
    KNOWN_HOST_SERVICE_KEYS,
    KNOWN_LSP_SERVER_KEYS,
    KNOWN_MCP_SERVER_KEYS,
    KNOWN_NETWORK_KEYS,
    KNOWN_PACKAGE_KEYS,
    KNOWN_RESOURCES_KEYS,
    KNOWN_SECURITY_KEYS,
    KNOWN_TOP_LEVEL_CONFIG_KEYS,
    MEMORY_RE,
    PACKAGE_NAME_RE,
    PACKAGE_OUTPUT_RE,
    USB_ID_RE,
    VALID_MCP_PRESETS,
    ConfigError,
    _check_config_changes,
    _check_preset_null_conflicts,
    _config_snapshot_path,
    _effective_mcp_server_names,
    _load_jsonc_file,
    _merge_lists,
    _merge_mise_disabled_tools,
    _merge_mise_tools,
    _normalize_blocked_tools,
    _parse_dotenv,
    _report_unknown_keys,
    _resolve_env_source_path,
    _resolve_env_sources,
    _validate_config,
    _validate_forward_host_port,
    _validate_port_number,
    _validate_publish_port,
    _validate_string_list,
    load_config,
    merge_config,
)
from .storage import (
    _ac_materialize_under_ws_state,
    _copy_if_missing,
    _detect_host_timezone,
    _detect_nix_daemon_label,
    _ensure_symlink,
    _host_mise_dir,
    _linux_multilib,
    _migrate_old_overlay,
    _nix_custom_conf_included,
    _seed_agent_dir,
    ensure_global_storage,
)
from .network import (
    _parse_port_forwards,
    cleanup_port_forwarding,
    start_host_port_forwarding,
)
from .agents_md import (
    _BUILTIN_JAIL_STARTUP_SKILL,
    _copy_skill_subdirs,
    _prepare_skills,
    generate_agents_md,
)
from .loopholes_runtime import (
    BROKER_LOOPHOLE_NAME,
    BROKER_SINGLETON_LOCK,
    BROKER_SINGLETON_PID_FILE,
    BROKER_SINGLETON_SOCKET,
    JOURNAL_FRAME_EXIT,
    JOURNAL_FRAME_STDERR,
    JOURNAL_FRAME_STDOUT,
    JOURNAL_MAX_ARG_LEN,
    JOURNAL_MAX_ARGS,
    LoopholeDaemon,
    _broker_ensure,
    _broker_is_alive,
    _broker_kill,
    _broker_pgrep_strays,
    _broker_pid_is_live,
    _broker_ping,
    _broker_read_pid,
    _broker_spawn,
    _broker_status,
    _broker_wait_for_socket,
    _cgd_create_and_join,
    _cgd_destroy,
    _cgd_ensure_agent_cgroup,
    _cgroup_delegate_handler,
    _gpu_host_available,
    _host_service_default_jail_socket,
    _host_service_env_var,
    _host_service_sockets_dir,
    _journal_handle_client,
    _journal_send_frame,
    _parse_memory_value,
    _resolve_journal_mode,
    _should_mount_host_nix,
    _start_broker_relay,
    _start_host_service_builtin_cgroup,
    _start_host_service_builtin_journal,
    _start_host_service_external,
    _substitute_socket_in_cmd,
    _validate_cgroup_name,
    start_loopholes,
    stop_loopholes,
)

app = typer.Typer(
    invoke_without_command=True,
    rich_markup_mode="rich",
    no_args_is_help=False,
)


@app.callback()
def _default(
    ctx: typer.Context,
    network: str = typer.Option("bridge", help="Container network mode (bridge/host)"),
    new: bool = typer.Option(
        False,
        "--new",
        help="Force a new container even if one already exists for this workspace",
    ),
    profile: bool = typer.Option(
        False,
        "--profile",
        help="Show detailed startup performance timing after command exits",
    ),
    version: bool = typer.Option(
        False,
        "--version",
        help="Show version and exit",
        callback=_version_callback,
        is_eager=True,
    ),
):
    """[bold]YOLO Jail[/bold] — Secure container environment for AI agents.

    Runs AI agents (Copilot, Gemini CLI, Claude Code) in isolated Podman/Apple Container containers
    with no access to host credentials (~/.ssh, ~/.gitconfig, cloud tokens).
    Tool state persists across restarts.

    [bold cyan]Quick Start[/bold cyan]

        yolo                      Interactive jail shell
        yolo -- copilot           Run Copilot in jail (--yolo auto-injected)
        yolo -- gemini            Run Gemini in jail (--yolo auto-injected)
        yolo -- claude            Run Claude Code in jail (YOLO mode via settings.json)
        yolo --new -- bash        Force new container (ignore running one)
        yolo --profile -- echo hi Profile startup performance
        yolo check                Validate config and preflight the build
        yolo ps                   List running jails
        yolo init                 Create config + agent briefing
        yolo config-ref           Full configuration reference

    [bold cyan]What Agents Get Inside the Jail[/bold cyan]

        Workspace:  Your project is bind-mounted at /workspace (read-write,
                    same files — edits are visible on the host immediately)
        Internet:   Full network access (bridge mode by default)
        Tools:      Node.js 22, Python 3.13, Go, rg, fd, bat, jq, git, gh,
                    nvim, curl, strace, and anything in packages/mise_tools
        Home:       /home/agent — shared across ALL jails. Auth tokens,
                    tool caches, and configs persist across restarts.
        Identity:   Host git/jj identity is injected automatically.
                    GitHub CLI (gh) is pre-authenticated.
        Resources:  [bold]yolo-cglimit[/bold] enforces CPU/memory/PID limits on
                    sub-processes via cgroup v2. See [bold]yolo config-ref[/bold].

        NOT shared: ~/.ssh, ~/.gitconfig, cloud credentials, host PATH.
        Blocked:    grep → rg, find → fd (configurable). Set YOLO_BYPASS_SHIMS=1
                    in scripts that need the originals.

    [bold cyan]Configuration[/bold cyan]

    Place [bold]yolo-jail.jsonc[/bold] in your project root (JSON with comments):

        {
          "runtime": "podman",              // or "container" (Apple Container)
          "packages": [                     // extra nix packages
            "strace",                       // latest from flake nixpkgs
            "gtk4.dev",                     // non-default output (headers + .pc)
            {"name": "gtk4-layer-shell", "outputs": ["out", "dev"]},
            {"name": "freetype", "nixpkgs": "e6f23dc0..."},  // pinned nixpkgs
            {"name": "freetype", "version": "2.14.1",        // version override
             "url": "mirror://savannah/freetype/freetype-2.14.1.tar.xz",
             "hash": "sha256-..."}
          ],
          "mounts": ["/path/to/repo"],      // read-only at /ctx/<name>
          "network": {"mode": "bridge", "ports": ["8000:8000"]},
          "security": {"blocked_tools": ["curl", "wget"]}
        }

    User defaults: ~/.config/yolo-jail/config.jsonc (merged under workspace).
    Run [bold]yolo check[/bold] to validate config changes before restarting.
    Run [bold]yolo config-ref[/bold] for the complete field reference.

    [bold cyan]Environment Variables[/bold cyan]

        YOLO_RUNTIME          Override runtime (podman/container)
        YOLO_BYPASS_SHIMS     Set to 1 to bypass blocked tool shims

    [bold cyan]Config Safety[/bold cyan]

    When yolo-jail.jsonc changes between runs, the CLI shows a diff and asks
    for human confirmation before starting. This prevents agents from silently
    modifying the config without the operator noticing.

    [bold cyan]Agent Package Workflow[/bold cyan]

    Agents inside the jail can edit yolo-jail.jsonc to add packages, but they
    MUST run [bold]yolo check[/bold] after every config edit before asking the human
    to restart. The human sees the diff and approves at next startup.
    Use [bold]yolo check --no-build[/bold] inside a running jail for a quick preflight.
    See [bold]yolo config-ref[/bold] for details.

    [bold cyan]Project Home[/bold cyan]

    https://github.com/mschulkind-oss/yolo-jail
    """
    if ctx.invoked_subcommand is None:
        # No subcommand → default to `run` (interactive shell)
        ctx.invoke(run, ctx=ctx, network=network, new=new, profile=profile)


from .console import console




def _resolve_repo_root() -> Path:
    """Find the yolo-jail repo root for nix image builds.

    Resolution order:
      1. YOLO_REPO_ROOT env var (set inside jails and CI)
      2. Source checkout detection (Path(__file__) → parent → parent → flake.nix exists)
      3. Installed package detection (flake.nix bundled inside the src/ package)
         Stages a build directory with symlinks so nix sees the expected layout.
      4. User config repo_path field (~/.config/yolo-jail/config.jsonc)
      5. Error with helpful message
    """
    # 1. Env var (used inside jails, CI, etc.)
    # Validate the path actually contains source — in nested jails the bind
    # mount at /opt/yolo-jail may be empty (doesn't propagate from parent).
    env_val = os.environ.get("YOLO_REPO_ROOT")
    if env_val:
        p = Path(env_val)
        if (p / "flake.nix").exists() or (p / "src" / "entrypoint.py").exists():
            return p.resolve()

    # 2. Running from source checkout (dev mode)
    # __file__ is src/cli/__init__.py — repo root is two parents up.
    source_root = Path(__file__).parent.parent.parent
    if (source_root / "flake.nix").exists():
        return source_root.resolve()

    # 3. Installed package — flake.nix bundled as package data in src/
    # (so its parent dir, not the cli package, is what we check).
    pkg_dir = Path(__file__).parent.parent
    if (pkg_dir / "flake.nix").exists():
        build_root = GLOBAL_STORAGE / "nix-build-root"
        build_root.mkdir(parents=True, exist_ok=True)

        # Idempotence: the old approach re-copied every yolo run via
        # an atomic rename dance.  That was expensive and vulnerable
        # to races + partial-copy bugs that left build_root empty
        # (handoff bug 6).  Skip the whole dance when the existing
        # build_root already matches the wheel's flake.nix mtime and
        # has at least cli.py in place.
        try:
            src_cli = build_root / "src" / "cli" / "__init__.py"
            br_flake = build_root / "flake.nix"
            pkg_flake_mtime = (pkg_dir / "flake.nix").stat().st_mtime_ns
            if (
                src_cli.is_file()
                and br_flake.is_file()
                and br_flake.stat().st_mtime_ns >= pkg_flake_mtime
            ):
                return build_root.resolve()
        except OSError:
            pass  # fall through to repopulate

        # Repopulate.  Use a temp dir + atomic rename to avoid races
        # when multiple jails start concurrently — either everybody
        # sees the old (complete) build_root or everybody sees the
        # new one, never a half-written state.
        import shutil
        import tempfile

        tmp_root = Path(tempfile.mkdtemp(dir=GLOBAL_STORAGE, prefix="nix-build-tmp-"))
        try:
            for fname in ("flake.nix", "flake.lock"):
                shutil.copy2(pkg_dir / fname, tmp_root / fname)
            shutil.copytree(pkg_dir, tmp_root / "src")
            target_tmp = build_root.with_name(build_root.name + ".old")
            old_build_root: Optional[Path]
            try:
                build_root.rename(target_tmp)
                old_build_root = target_tmp
            except FileNotFoundError:
                old_build_root = None
            tmp_root.rename(build_root)
            if old_build_root and old_build_root.exists():
                shutil.rmtree(old_build_root, ignore_errors=True)
        except BaseException:
            shutil.rmtree(tmp_root, ignore_errors=True)
            raise
        return build_root.resolve()

    # 4. User config
    if USER_CONFIG_PATH.exists():
        try:
            cfg = pyjson5.loads(USER_CONFIG_PATH.read_text())
            repo_path = cfg.get("repo_path")
            if repo_path:
                p = Path(repo_path).expanduser().resolve()
                if (p / "flake.nix").exists():
                    return p
        except Exception:
            pass

    console.print(
        "[bold red]Cannot find yolo-jail repo root.[/bold red]\n"
        "The yolo CLI needs the repo for nix image builds.\n\n"
        "Fix: add [bold]repo_path[/bold] to ~/.config/yolo-jail/config.jsonc:\n"
        '  { "repo_path": "~/code/yolo-jail" }'
    )
    raise typer.Exit(1)












# ---------------------------------------------------------------------------
# Host services: split the jail boundary with outside-the-jail processes
# ---------------------------------------------------------------------------
# A host service is a process that runs on the host (outside the jail) and
# exposes a Unix socket that gets bind-mounted into the jail at a well-known
# path.  The agent inside the jail can talk to the service without holding
# any of the service's secrets or privileges.  This is how we split the jail
# boundary cleanly:
#
#   • Secrets / privileges live in the host process, not in the jail
#   • Access control lives in the host process, not in the jail
#   • The jail gets a socket — nothing else crosses the boundary
#
# Two kinds of services:
#
#   1. Builtin services.  Implemented in-process in the yolo CLI.  Today the
#      cgroup delegate daemon is the only one — it performs privileged cgroup
#      operations on behalf of the container.  Built-ins are auto-started
#      when applicable (e.g. cgroup delegate on Linux only).
#
#   2. External services.  Configured in yolo-jail.jsonc under `loopholes`.
#      The user provides a command to launch.  yolo substitutes `{socket}` in
#      the command args with the host-side socket path, launches the process,
#      waits for the socket to appear, and tears it down when the jail exits.
#      Example: a token broker that holds API keys and answers scoped requests.
#
# Both kinds share:
#   • Same bind-mounted directory in the jail: /run/yolo-services/
#   • Same socket naming: /run/yolo-services/<service-name>.sock
#   • Same env var convention: YOLO_SERVICE_<NAME>_SOCKET
#   • Same lifecycle: started before the container, stopped after container exits
#
# Security model (both kinds):
#   • The socket dir is bind-mounted from a per-jail host directory — no
#     other jails can see the socket.
#   • Services can use SO_PEERCRED on Linux to attest the caller's host PID.
#   • What the service does with secrets, scopes, and audit logging is the
#     service's problem.  yolo just wires the plumbing.
# ---------------------------------------------------------------------------















def _workspace_readonly_mount_args(
    workspace: Path, config: Dict[str, Any]
) -> List[str]:
    """Build the ``-v …:ro`` arguments for ``config.workspace_readonly``.

    Each configured sub-path is overlaid onto the writable ``/workspace``
    mount so agents can't modify host-executed source.  When any entry is
    active we also lock ``yolo-jail.jsonc`` itself — otherwise an agent
    could rewrite the config and escape on the next run.

    Entries that escape the workspace or don't exist are skipped with a
    warning rather than failing the run.
    """
    readonly_entries = config.get("workspace_readonly", []) or []
    if not readonly_entries:
        return []

    args: List[str] = []
    ws_config_file = workspace / "yolo-jail.jsonc"
    if ws_config_file.exists():
        args.extend(["-v", f"{ws_config_file}:/workspace/yolo-jail.jsonc:ro"])

    workspace_root = workspace.resolve()
    for rel in readonly_entries:
        host_subpath = (workspace / rel).resolve()
        try:
            host_subpath.relative_to(workspace_root)
        except ValueError:
            console.print(
                f"[yellow]Warning: workspace_readonly path escapes workspace, skipping: {rel}[/yellow]"
            )
            continue
        if not host_subpath.exists():
            console.print(
                f"[yellow]Warning: workspace_readonly path does not exist, skipping: {rel}[/yellow]"
            )
            continue
        args.extend(["-v", f"{host_subpath}:/workspace/{rel}:ro"])
    return args



def _entrypoint_preflight(repo_root: Path, workspace: Path, config: Dict[str, Any]):
    """Generate jail-managed config into a temp home to catch config/render errors."""
    src_dir = repo_root / "src"
    host_mise = _host_mise_dir()
    normalized_blocked = _normalize_blocked_tools(config.get("security"))
    env = os.environ.copy()

    with tempfile.TemporaryDirectory(prefix="yolo-check-") as tmp:
        env.update(
            {
                "JAIL_HOME": tmp,
                "HOME": tmp,
                "NPM_CONFIG_PREFIX": f"{tmp}/.npm-global",
                "GOPATH": f"{tmp}/go",
                "MISE_DATA_DIR": str(host_mise),
                "YOLO_HOST_DIR": str(workspace.resolve()),
                "YOLO_BLOCK_CONFIG": json.dumps(normalized_blocked),
                "YOLO_MISE_TOOLS": json.dumps(_merge_mise_tools(config)),
                "YOLO_LSP_SERVERS": json.dumps(config.get("lsp_servers", {})),
                "YOLO_MCP_SERVERS": json.dumps(config.get("mcp_servers", {})),
                "YOLO_MCP_PRESETS": json.dumps(config.get("mcp_presets", [])),
            }
        )
        # Apply user-defined env vars from env_sources
        for env_key, env_val in _resolve_env_sources(workspace, config).items():
            env[env_key] = env_val

        code = f"""
import json
import sys
from pathlib import Path

sys.path.insert(0, {str(src_dir)!r})
import entrypoint

entrypoint.generate_shims()
entrypoint.generate_bashrc()
entrypoint.generate_bootstrap_script()
entrypoint.generate_venv_precreate_script()
entrypoint.generate_mise_config()
entrypoint.generate_mcp_wrappers()
entrypoint.configure_copilot()
entrypoint.configure_gemini()
entrypoint.configure_claude()

json.loads((entrypoint.COPILOT_DIR / "mcp-config.json").read_text())
json.loads((entrypoint.COPILOT_DIR / "lsp-config.json").read_text())
json.loads((entrypoint.GEMINI_DIR / "settings.json").read_text())
json.loads((entrypoint.CLAUDE_DIR / "settings.json").read_text())
print("ok")
"""
        result = subprocess.run(
            [sys.executable, "-c", code],
            cwd=workspace,
            env=env,
            capture_output=True,
            text=True,
        )
        if result.returncode != 0:
            details = "\n".join(
                part for part in (result.stdout.strip(), result.stderr.strip()) if part
            )
            raise ConfigError(details or "entrypoint dry-run failed")


@app.command()
def init(
    mount: List[str] = typer.Option(
        [],
        "--mount",
        "-m",
        help=(
            "Host path to mount read-only at /ctx/<basename> inside the jail. "
            "Repeatable. Use 'HOST:/ctx/NAME' to override the container path. "
            "Example: -m ~/code/shared-lib -m ~/notes:/ctx/notes"
        ),
    ),
):
    """Initialize a yolo-jail.jsonc config and print an agent briefing."""
    config_path = Path.cwd() / "yolo-jail.jsonc"
    if config_path.exists():
        typer.echo("yolo-jail.jsonc already exists.")
        _print_init_briefing(config_path)
        return

    # If the user passed any --mount flags, bake them into a real `mounts`
    # array.  Otherwise emit the same commented-out placeholder as before.
    if mount:
        mounts_block = (
            "  // Extra host paths to mount read-only into the jail at /ctx/.\n"
            '  // Each entry is a host path (mounted at /ctx/<basename>) or "host:container".\n'
            '  "mounts": [\n'
            + "".join(f"    {json.dumps(m)},\n" for m in mount)
            + "  ],\n"
        )
        # Trim the trailing comma on the last list entry — valid JSONC tolerates
        # trailing commas in arrays, but be polite.
        mounts_block = mounts_block.replace(",\n  ],", "\n  ],")
    else:
        mounts_block = (
            "  // Extra host paths to mount read-only into the jail for context.\n"
            '  // Each entry is a host path (mounted at /ctx/<basename>) or "host:container".\n'
            "  // Pass --mount/-m on `yolo init` to populate this automatically, e.g.\n"
            "  //   yolo init -m ~/code/shared-lib -m ~/notes\n"
            '  // "mounts": [\n'
            '  //   "~/code/other-repo",\n'
            '  //   "~/code/shared-lib:/ctx/shared-lib"\n'
            "  // ]\n"
        )

    content = (
        """{
  // ───────────────────────────────────────────────────────────────
  // YOLO Jail workspace config.  First-time agents: run `yolo --help`
  // for an overview of commands, `yolo config-ref` for the full field
  // reference, and `yolo check` after every edit to this file.
  // ───────────────────────────────────────────────────────────────

  // Container runtime: "podman" or "container" (Apple)
  // (also settable via YOLO_RUNTIME env var)
  // "runtime": "podman",

  // Extra nix packages to include in the jail image.
  // Names must match nixpkgs attribute names (search at https://search.nixos.org/packages).
  // The image rebuilds only when this list changes.
  // Supports plain strings (latest), dotted output selection, pinned nixpkgs
  // commits, version overrides, and explicit multi-output objects:
  // "packages": [
  //   "postgresql",
  //   "gtk4.dev",                                       // single non-default output
  //   {"name": "gtk4", "outputs": ["out", "dev"]},      // multiple outputs
  //   {"name": "freetype", "nixpkgs": "<commit-hash>"},
  //   {"name": "freetype", "version": "2.14.1",
  //    "url": "mirror://savannah/freetype/freetype-2.14.1.tar.xz",
  //    "hash": "sha256-..."}
  // ],
  // Common output names: out (default), dev (headers + pkg-config), bin, lib, man, doc.
  // Find nixpkgs commits for specific versions at: https://lazamar.co.uk/nix-versions/

  // security: tool shims injected into the jail's PATH.  Defaults (no
  // config needed): grep is blocked only for recursive usage (``-r``,
  // ``-R``, ``--recursive``, ``-rn`` etc. — pipe filters and
  // single-file greps pass through); find is blocked unconditionally.
  // Override only if you want custom rules — the defaults are sane.
  // "security": {
  //   "blocked_tools": [
  //     {
  //       "name": "grep",
  //       // Only block when argv contains one of these shell-glob
  //       // patterns.  Omit to block unconditionally.
  //       "block_flags": ["--recursive", "-r", "-R", "-*[rR]*"]
  //     },
  //     "find",           // string form → unconditional block
  //     "curl"            // add your own tools here
  //   ]
  // },
  "network": {
    // "bridge" (default) or "host"
    "mode": "bridge",
    // Ports to publish in bridge mode ["Host:Container"]
    // "ports": ["8000:8000"]
    // Forward host ports into the jail (appear on localhost inside container)
    // "forward_host_ports": [5432, "8080:9090"]
  },
"""
        + mounts_block
        + """
  // Environment variables set inside the jail.
  // Ordered list: strings are KEY=VALUE file paths, objects are inline maps.
  // Later entries override earlier ones; workspace config appends to user config.
  // File paths: ~ expanded, relative paths resolve against the workspace root.
  // Missing files warn and skip; keep secrets out of the dotfiles-synced config.
  // "env_sources": [
  //   "~/.config/yolo-jail/defaults.env",
  //   {"DEBUG": "1"},
  //   ".secrets/claude.env"
  // ]

  // Extra tools to install via mise (key: tool name, value: version string).
  // Default: {"neovim": "stable"} — override in user or workspace config.
  // "mise_tools": {"neovim": "nightly", "typst": "latest"}

  // Additional language servers for Copilot and Gemini.
  // Defaults (always present): python (pyright), typescript, go (gopls).
  // Add new servers or override defaults. Binary must be on PATH (e.g., via mise_tools).
  // "lsp_servers": {
  //   "rust": {
  //     "command": "rust-analyzer",
  //     "args": [],
  //     "fileExtensions": {".rs": "rust"}
  //   }
  // }
  //
  // Enable built-in MCP server presets by name.
  // Available presets: chrome-devtools, sequential-thinking
  // "mcp_presets": ["chrome-devtools", "sequential-thinking"]

  // Additional custom MCP servers for Copilot and Gemini.
  // Add custom servers or set a preset/inherited server to null to disable it.
  // Binary must be on PATH or absolute.
  // "mcp_servers": {
  //   "my-custom": {
  //     "command": "/workspace/scripts/my-mcp-server.py",
  //     "args": []
  //   }
  // }

  // NVIDIA GPU passthrough (podman + CDI).  Safe to commit: when
  // enabled on a host without NVIDIA drivers/CDI, yolo warns and
  // starts without passthrough instead of erroring, so the same
  // config works on a GPU box and a GPU-less laptop.
  // Run "yolo check" on the GPU machine to verify readiness.
  // "gpu": {
  //   "enabled": true,
  //   "devices": "all",          // "all", "0", "0,1", or "GPU-<uuid>"
  //   "capabilities": "compute,utility"
  // }

  // Container resource limits.
  // On Apple Container: applied as VM hardware limits (defaults: half host CPUs/RAM).
  // On Podman: applied as --cpus/--memory flags (no defaults — inherits VM limits).
  // On Linux: also feeds cgroup delegation for in-container yolo-cglimit.
  // "resources": {
  //   "memory": "8g",            // Max memory (b/k/m/g suffix). OOM-killed if exceeded.
  //   "cpus": 4,                 // CPU limit (decimal). e.g. 4, 2.5, "0.5"
  //   "pids_limit": 4096         // Max processes (podman only). Prevents fork bombs.
  // }
}
"""
    )

    with open(config_path, "w") as f:
        f.write(content)
    typer.echo("Created yolo-jail.jsonc")

    # Add .yolo/ to .gitignore if not already present
    gitignore = Path.cwd() / ".gitignore"
    if gitignore.exists():
        text = gitignore.read_text()
        if ".yolo/" not in text:
            with open(gitignore, "a") as f:
                f.write("\n# YOLO Jail workspace state\n.yolo/\n")
    else:
        with open(gitignore, "w") as f:
            f.write("# YOLO Jail workspace state\n.yolo/\n")

    _print_init_briefing(config_path)


def _print_init_briefing(config_path: Path):
    """Print the comprehensive agent briefing after init."""
    console.print(f"""
[bold green]✓ Config ready:[/bold green] {config_path}

[bold]═══════════════════════════════════════════════════════════[/bold]
[bold]  YOLO JAIL — AGENT BRIEFING                              [/bold]
[bold]═══════════════════════════════════════════════════════════[/bold]

You are about to be launched inside a YOLO Jail — an isolated container
that protects the host system from unintended changes. Here is what you
need to know.

[bold cyan]WHAT YOU KEEP (shared with the host)[/bold cyan]

  • [bold]Your workspace[/bold] is bind-mounted at [bold]/workspace[/bold] (read-write).
    Every file edit you make is visible on the host immediately.
    This is the SAME directory, not a copy.

  • [bold]Internet access[/bold] is available (bridge networking by default).
    You can curl, pip install, npm install, go get, etc.

  • [bold]Git/jj identity[/bold] is injected from the host (name + email).
    GitHub CLI (gh) is pre-authenticated.

  • [bold]Mise tool data[/bold] is shared with the host. Python, Node, Go, and
    other runtimes installed via mise are available at the same paths.
    Venvs created on the host resolve inside the jail.

[bold cyan]WHAT CHANGES (different from the host)[/bold cyan]

  • [bold]Home directory[/bold] is /home/agent. This is a [bold]shared home[/bold] — the same
    across ALL jail workspaces. Auth tokens, tool caches, shell configs,
    and installed tools all persist here across restarts. It is separate
    from the host home directory.

  • [bold]Per-workspace state[/bold]: Some things are isolated per-workspace
    (not shared across jails): SSH keys, bash history, copilot sessions,
    gemini history. These live in <workspace>/.yolo/.

  • [bold]Workspace path[/bold] is /workspace (not the host's absolute path).
    Venv scripts with absolute host path shebangs may need fixing.

  • [bold]Some tools are blocked[/bold] (e.g., grep → rg, find → fd).
    Set YOLO_BYPASS_SHIMS=1 in scripts that need the originals.

[bold cyan]TOOLS AVAILABLE INSIDE[/bold cyan]

  Runtimes:  Node.js 22, Python 3.13, Go (managed by mise)
  Editors:   nvim (stable by default, configurable via mise_tools)
  CLI tools: rg, fd, bat, jq, git, jj, gh, curl, strace, uv, tmux
  Agents:    copilot, gemini (auto-injected with --yolo flag)
  The 'yolo' command itself is available inside for nested jailing.

  [bold]Mise[/bold] manages all runtimes and supports thousands of tools from
  multiple registries (aqua, asdf, cargo, go, npm, pipx, ubi, and more).
  Run 'mise registry' inside the jail to browse. Add tools to the
  "mise_tools" config or to /workspace/mise.toml for the workspace.
  Examples: rust, zig, terraform, kubectl, typst, pixi, conda.

[bold cyan]WHAT TO DO NOW — TRANSITION QUICKLY[/bold cyan]

  [bold]Your goal is to get inside the jail as fast as possible.[/bold]
  Do only what's needed outside, then hand off. All real work happens
  inside the jail where you have full tool access.

  1. [bold]Review yolo-jail.jsonc[/bold] — edit it [bold]only[/bold] if you need extra packages.
     • "packages": nix packages baked into the image (rebuilds on change).
       Search: https://search.nixos.org/packages
     • "mise_tools": tools installed via mise (no rebuild needed).
       For tools with binary releases — fast, no compilation.
     Most tasks need NO config changes. Skip this step if unsure.

  2. [bold]Run `yolo check`[/bold] after [bold]EVERY[/bold] `yolo-jail.jsonc` edit to validate
     the config and preflight the build. Use `yolo check --no-build` inside a
     running jail if you only need config/entrypoint validation. Do this before
     asking the human to restart you into the jail.

  3. [bold](MANDATORY) Write a handover document[/bold] at:
     [bold yellow].yolo/handover.md[/bold yellow]

     This file is [bold]required[/bold]. Your jail instance will be a completely
     fresh agent session with NO access to this conversation. Without
     this document, the inner agent starts blind. Include:
     • What you were working on and the current state
     • What remains to be done (specific tasks, not vague goals)
     • Key decisions made and why
     • Files to look at first
     • Any gotchas or context the inner agent needs

  4. [bold]Ask the human to restart you inside the jail[/bold]:
     Tell them to run: yolo -- copilot  (or yolo -- gemini, yolo -- claude)

     The inner agent has a built-in [bold]jail-startup[/bold] skill that reads
     your handover doc automatically. The human just needs to say:
     [bold yellow]"invoke the jail-startup skill"[/bold yellow]
     and the inner agent will pick up your handover and continue.

  Do NOT spend time on implementation outside the jail. Write the
  handover doc, request the restart, and stop. The inner agent has
  the same tools and full internet access — it can do everything.

[bold cyan]CONFIGURATION REFERENCE[/bold cyan]

  Run 'yolo config-ref' for the full field reference.
  Run 'yolo --help' for usage examples.
""")


@app.command("init-user-config")
def init_user_config():
    """Initialize a user-level config at ~/.config/yolo-jail/config.jsonc."""
    USER_CONFIG_PATH.parent.mkdir(parents=True, exist_ok=True)
    if USER_CONFIG_PATH.exists():
        typer.echo(f"{USER_CONFIG_PATH} already exists.")
        return
    content = """{
  // ───────────────────────────────────────────────────────────────
  // YOLO Jail user-level defaults.  First-time agents: run `yolo --help`
  // for an overview of commands, `yolo config-ref` for the full field
  // reference, and `yolo check` after every edit to this file.
  // ───────────────────────────────────────────────────────────────
  //
  // User-level defaults merged into every project config.
  // Lists are merged (deduplicated), scalars are overridden by workspace config.
  //
  // Container runtime: "podman" or "container" (Apple)
  // (also settable via YOLO_RUNTIME env var)
  // "runtime": "podman",
  // "packages": ["sqlite", "postgresql"],
  // "mounts": ["~/code/shared-lib:/ctx/shared-lib"],
  // "security": {
  //   "blocked_tools": ["wget"]
  // },

  // Expose `journalctl` from the host inside the jail as `yolo-journalctl`.
  // "off"  (default) — disabled, no shim generated
  // "user" — forces --user on every invocation (safe for unprivileged agents)
  // "full" — passes args through unchanged (needs host journal read access)
  // "journal": "user",

  // Expose /dev/kvm inside the jail for nested hardware-accelerated VMs.
  // Requires your host user to be in the kvm group.  Linux only.
  // "kvm": true
}
"""
    with open(USER_CONFIG_PATH, "w") as f:
        f.write(content)
    typer.echo(f"Created {USER_CONFIG_PATH}")


@app.command("config-ref")
def config_ref():
    """Show the full YOLO Jail configuration reference."""
    console.print("""[bold]YOLO Jail Configuration Reference[/bold]

[bold cyan]CONFIG FILE: yolo-jail.jsonc[/bold cyan]

  Location: Project root (per-workspace)
  Format:   JSON with comments (JSONC)
  User defaults: ~/.config/yolo-jail/config.jsonc

  Workspace config merges over user defaults.
  Lists are merged and deduplicated. Scalars override.

  [bold yellow]Rule:[/bold yellow] After [bold]EVERY[/bold] edit to `yolo-jail.jsonc` or
  `~/.config/yolo-jail/config.jsonc`, run `yolo check` before restarting or
  asking a human to restart the jail. Use `yolo check --no-build` inside a
  running jail for a faster preflight.

[bold cyan]FIELDS[/bold cyan]

  [bold]runtime[/bold] (string): Container runtime.
    Values: "podman" or "container"
    Override: YOLO_RUNTIME env var takes priority.
    Auto-detect: macOS prefers "container" then "podman"; Linux uses "podman".

  [bold]packages[/bold] (array): Extra nix packages baked into the image.
    Supports three formats:
    • String: package name from nixpkgs (latest from flake's pin)
      Example: "postgresql"
    • Object with nixpkgs: pinned to a specific nixpkgs commit
      Example: {"name": "freetype", "nixpkgs": "<commit-hash>"}
    • Object with version override: build from upstream source
      Uses the existing nix build recipe but swaps version+source.
      Example: {"name": "freetype", "version": "2.14.1",
                "url": "mirror://savannah/freetype/freetype-2.14.1.tar.xz",
                "hash": "sha256-MkJ+jEcawJWFMhKjeu+BbGC0IFLU2eSCMLqzvfKTbMw="}
      Get the hash: nix-prefetch-url <url>  (then convert with nix hash)
      Or set hash to "" and nix will tell you the correct one on build failure.
    Find nixpkgs commits per version: https://lazamar.co.uk/nix-versions/
    Search package names: https://search.nixos.org/packages
    Image rebuilds only when this list changes.
    Nix caches builds — identical configs across jails share cached results.

  [bold]host_claude_files[/bold] (array of strings): Host ~/.claude/ files to sync into the jail.
    Each entry is a filename (not a path) relative to ~/.claude/.
    Files are mounted read-only at /ctx/host-claude/ and copied into the jail's
    ~/.claude/ on startup. For settings.json, host settings are deep-merged with
    YOLO-required overrides (YOLO wins on conflicts).
    The fileSuggestion script referenced in host settings.json is auto-discovered
    and synced (if it lives under ~/.claude/) — no need to list it explicitly.
    Default: ["settings.json"]
    Set to [] to disable host claude file syncing.
    Example: ["settings.json", "keybindings.json"]

  [bold]host_services[/bold] (object): Host-side services exposed inside the jail via Unix sockets.
    Each key is a service name (must match ^[a-zA-Z][a-zA-Z0-9_-]{0,63}$).
    The name "cgroup-delegate" is reserved for the built-in cgroup daemon.

    Each value is an object with:
      "command" (array of strings, required): the command to launch on the host
        when the jail starts.  "{socket}" in any arg is substituted with the
        actual host-side socket path the service should bind.
      "env" (object, optional): extra env vars for the host daemon (NOT the jail).
      "jail_socket" (string, optional): override the jail-side socket path.
        Must start with /run/yolo-services/ and end in .sock.
        Default: /run/yolo-services/<name>.sock

    Each service gets:
      • Its socket bind-mounted into the jail at /run/yolo-services/<name>.sock
      • An env var YOLO_SERVICE_<NAME>_SOCKET injected into the container so
        agents can locate the socket without hard-coding paths.
      • A managed lifecycle: started before container start, SIGTERM + 5s grace +
        SIGKILL after the container exits.
      • stdout/stderr captured to ~/.local/share/yolo-jail/logs/host-service-<name>.log

    Use this to split the jail boundary cleanly: a host-side process can hold
    secrets, credentials, and access-control logic that the agent inside the
    jail can call but never sees.  See docs/USER_GUIDE.md § Host Services for
    a complete example.

    Example:
      "loopholes": {
        "auth-broker": {
          "command": ["~/code/auth-broker/serve.py", "--socket", "{socket}"],
          "env": {"KEYS_FILE": "~/secrets/keys.json"}
        }
      }

    Apple Container is unsupported (no Unix-socket bind-mount through virtiofs).

  [bold]journal[/bold] (string, default "off"): Enable the built-in journal bridge.
    Exposes [bold]yolo-journalctl[/bold] inside the jail, which forwards its args to
    [cyan]journalctl[/cyan] running on the host and streams stdout/stderr back to the
    terminal.  Useful when an agent needs to inspect systemd logs (e.g.
    the Claude token refresher's own output) without mounting the host
    journal rw into the jail.
    Values:
      • "off"  (default) — no daemon, no shim
      • "user" — daemon forces [cyan]--user[/cyan] on every invocation (recommended)
      • "full" — args pass through unchanged (requires host journal read access)
    Socket: /run/yolo-services/journal.sock
    Env var: YOLO_SERVICE_JOURNAL_SOCKET
    "journal" is reserved as a host_services name — you cannot shadow it.

  [bold]env_sources[/bold] (array): Environment variables set inside the jail.
    Ordered list; each entry is either:
      • a string — path to a KEY=VALUE dotenv file (# comments allowed,
        quoted values OK, `export` prefix tolerated)
      • an object — inline {"KEY": "VALUE"} map
    Later entries override earlier ones.  User-config list loads first,
    then workspace-config list.  File paths support ~ expansion,
    absolute paths, and workspace-relative paths.  Missing files warn
    and skip rather than failing the run — keep secrets in an unsynced
    file outside your dotfiles tree.
    Written to ~/.config/yolo-user-env.sh (sourced by .bashrc and entrypoint);
    can be overridden by mise .env or by editing that file inside the jail.
    Example: [
      "~/.config/yolo-jail/defaults.env",
      {"DEBUG": "1"},
      ".secrets/claude.env"
    ]

  [bold]mounts[/bold] (array of strings): Extra host paths mounted read-only.
    Simple path → mounted at /ctx/<basename>
    "host:container" → custom container path
    Example: ["/path/to/repo", "~/lib:/ctx/lib"]

  [bold]workspace_readonly[/bold] (array of strings): Workspace sub-paths to overlay as read-only.
    Each entry is a relative path inside the workspace (no leading /, no ..).
    Mounted on top of the writable /workspace volume so agents cannot modify
    those paths. Use this to protect host-executed code that lives in the
    workspace repo (e.g. the yolo-jail src/ directory itself).
    Example: ["src", "flake.nix", "Justfile"]

  [bold]network.mode[/bold] (string): Network isolation mode.
    "bridge" (default): Isolated. Use network.ports for access.
    "host": Share host network stack (localhost works directly).

  [bold]network.ports[/bold] (array of strings): Port mappings in bridge mode.
    Format: "host_port:container_port"
    Example: ["8000:8000", "3000:3000"]
    Makes container services reachable from the host.

  [bold]network.forward_host_ports[/bold] (array): Forward host ports into the jail.
    Makes host services appear on localhost inside the container, even if the
    host service only listens on 127.0.0.1 (like SSH -L port forwarding).
    Integer: same port on both sides (e.g., 5432)
    String "local:host": remap ports (e.g., "5432:3306")
    Example: [5432, 6379, "8080:9090"]
    Uses socat via Unix sockets; only active in bridge mode.
    Requires socat installed on the host.

  [bold]security.blocked_tools[/bold] (array): Tools to block inside the jail.
    Simple: ["curl", "wget"]
    Detailed: [{"name": "grep", "message": "Use rg", "suggestion": "rg <pattern>"}]
    Default: grep and find are blocked (rg/fd suggested instead).
      • grep is conditionally blocked — only recursive invocations
        (``-r``, ``-R``, ``--recursive``, or short-flag bundles like
        ``-rn``).  Pipe filters and single-file greps pass through.
      • find is unconditionally blocked.
    Conditional: add ``block_flags`` (array of shell case-glob patterns)
    to block only when argv contains a matching flag.  Absence means
    "always block" (find's default behavior).  Long options in
    block_flags match exactly; short patterns (starting with ``-``)
    match after any non-matching ``--*`` arg is skipped, so patterns
    like ``-*[rR]*`` catch ``-rn`` / ``-Rn`` without false-positive-ing
    ``--regex``.
    Example:
      "security": {
        "blocked_tools": [
          {
            "name": "grep",
            "message": "grep -r blocked; use rg",
            "suggestion": "rg <pattern>",
            "block_flags": ["-r", "-R", "--recursive", "-*[rR]*"]
          }
        ]
      }
    Bypass: Set YOLO_BYPASS_SHIMS=1 in scripts that need blocked tools.

  [bold]mise_tools[/bold] (object): Extra tools installed via mise in the jail.
    Keys are mise tool names, values are version strings.
    Default: {"neovim": "stable"}
    These are injected into the jail's global mise config (not workspace mise.toml).
    Deep-merged: user config adds tools, workspace config overrides versions.
    Example: {"neovim": "nightly", "typst": "latest"}

  [bold]lsp_servers[/bold] (object): Additional language servers for Copilot and Gemini (Claude uses its own tools).
    Default servers (always present): python (pyright), typescript, go (gopls).
    Workspace servers are merged with defaults — add new ones or override existing.
    Each key is a server name; value is an object with:
      • command (string, required): Binary name (on PATH) or absolute path.
      • args (array of strings): Args passed to the LSP binary. Default: [].
      • fileExtensions (object): Extension → language ID map (required for Copilot).
    The entrypoint translates these for each agent:
      • Copilot: written to ~/.copilot/lsp-config.json as native LSP servers.
      • Gemini: wrapped via mcp-language-server as MCP servers in settings.json.
    Example: {"rust": {"command": "rust-analyzer", "args": [],
              "fileExtensions": {".rs": "rust"}}}

  [bold]mcp_presets[/bold] (array of strings): Enable built-in MCP server presets by name.
    No presets are enabled by default. Available presets:
      • chrome-devtools: Headless Chromium automation via Chrome DevTools Protocol.
      • sequential-thinking: Chain-of-thought reasoning via MCP.
    Invalid: enabling a preset here and null-removing it in the same config file.
    Example: ["chrome-devtools", "sequential-thinking"]

  [bold]mcp_servers[/bold] (object): Custom MCP servers for Copilot, Gemini, and Claude.
    Add custom servers, or set a preset/inherited server to [bold]null[/bold] to disable it.
    Each key is a server name; value is an object with:
      • command (string, required): Binary name (on PATH) or absolute path.
      • args (array of strings): Args passed to the MCP server. Default: [].
    The entrypoint translates these for each agent:
      • Copilot: written to a per-workspace overlay mounted at ~/.copilot/mcp-config.json.
      • Gemini: written to a per-workspace overlay mounted at ~/.gemini/settings.json.
      • Claude: written to a per-workspace overlay mounted at ~/.claude/settings.json.
    Example: {"my-custom": {"command": "/workspace/scripts/my-mcp.py", "args": []}}

  [bold]devices[/bold] (array): Host devices to pass through to the jail.
    Three formats supported:
    • USB by vendor:product ID (preferred — stable across reboots):
      {"usb": "0bda:2838", "description": "RTL-SDR Blog V4"}
      Resolved to /dev/bus/usb/... at startup via lsusb.
    • Raw device path (fragile — changes on replug):
      "/dev/bus/usb/001/004"
    • Cgroup rule (broad access):
      {"cgroup_rule": "c 189:* rwm"}
      Grants access to all devices matching the major number.
    Missing devices produce a warning, not an error — the jail still starts.
    Subject to config change safety (human approval required).

  [bold]gpu[/bold] (object): NVIDIA GPU passthrough configuration.
    Requires NVIDIA Container Toolkit on the host (podman + CDI).
    • [bold]enabled[/bold] (bool): Enable GPU passthrough. Default: false.
      If true but the host lacks drivers/CDI (e.g. laptop without an
      NVIDIA GPU), yolo prints a one-line warning and starts without
      GPU passthrough — so the same config can be committed and used
      on both a GPU box and a GPU-less machine.
    • [bold]devices[/bold] (string): Which GPUs to expose. Default: "all".
      Values: "all", "0", "0,1", or "GPU-<uuid>".  Mapped to CDI
      device entries (nvidia.com/gpu=...).
    • [bold]capabilities[/bold] (string): NVIDIA driver capabilities. Default: "compute,utility".
      Valid: compute, utility, graphics, video, display, compat32.
      "compute,utility" is sufficient for PyTorch/CUDA training.

    Host prerequisites (on the GPU machine):
      1. NVIDIA driver installed (nvidia-smi works)
      2. nvidia-container-toolkit installed
      3. sudo nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml
    Run [bold]yolo check[/bold] to verify GPU readiness on a given host.
    Subject to config change safety (human approval required).

  [bold]kvm[/bold] (boolean, default false): Expose /dev/kvm inside the jail.
    When true, yolo adds [cyan]--device /dev/kvm[/cyan] to the container run
    command plus [cyan]--group-add keep-groups[/cyan] so the in-jail user
    inherits the host's kvm-group membership (podman).
    Enables nested hardware-accelerated VMs inside the jail (QEMU,
    firecracker, Android emulator, kernel dev workflows).  Runs full-speed
    virtualization via KVM instead of falling back to software emulation.
    Host prerequisites (verified by [bold]yolo check[/bold] when enabled):
      1. CPU virtualization extensions enabled in firmware (VT-x / AMD-V)
      2. kvm kernel module loaded ([cyan]modprobe kvm_intel[/cyan] or [cyan]kvm_amd[/cyan])
      3. Your host user is a member of the kvm group
    Not supported on macOS (Apple hosts use the VZ framework) or on the
    Apple Container runtime (no device passthrough).  Skipped with a warn
    when /dev/kvm is absent on a Linux host.
    [yellow]Security note:[/yellow] /dev/kvm is a kernel hypervisor interface.
    The attack surface is narrow — historical CVEs have mostly been
    guest-to-host escape bugs requiring attacker code in a KVM guest —
    but it is strictly larger than no-kvm.  Leave this off unless you
    actually need nested virtualization.

  [bold]resources[/bold] (object): Container resource limits.
    Sets hard cgroup constraints on the jail container via podman flags.
    These limits are enforced by the kernel — the jail cannot exceed them.
    • [bold]memory[/bold] (string): Maximum memory. Format: number + suffix (b/k/m/g).
      Examples: "8g" (8 GB), "512m" (512 MB), "2g".
      Maps to --memory flag. OOM-killed if exceeded.
    • [bold]cpus[/bold] (number|string): CPU limit as a decimal. Default: no limit.
      Examples: 4 (four cores), 2.5 (two and a half cores), "0.5" (half a core).
      Maps to --cpus flag (CFS quota).
    • [bold]pids_limit[/bold] (integer): Maximum number of processes. Default: 32768 (Podman's built-in default of 2048 is too low for agent workloads).
      Prevents fork bombs and runaway process creation.
      Maps to --pids-limit flag.

    [bold]In-jail sub-process limits (cgroup v2 delegation)[/bold]:
    A host-side cgroup delegate daemon runs alongside the container and
    performs all privileged cgroup operations on behalf of agents inside the
    jail.  No CAP_SYS_ADMIN or writable cgroup mount is needed inside the
    container — the daemon validates every request and operates securely on
    the host cgroup filesystem via a Unix socket.
    Use the [bold]yolo-cglimit[/bold] helper inside the jail:
      yolo-cglimit --cpu 75 -- python train.py           # 75% of all CPUs
      yolo-cglimit --cpu 50 --memory 2g -- make -j8      # 50% CPU + 2GB RAM
      yolo-cglimit --pids 100 -- ./script.sh             # Max 100 processes
    The daemon is started automatically by the yolo CLI.  Podman is the
    primary supported runtime.
    Falls back to nice/timeout/ulimit if delegation is unavailable.
    Subject to config change safety (human approval required).

[bold cyan]EXAMPLE CONFIG[/bold cyan]

  {
    "runtime": "podman",
    "mise_tools": {"neovim": "nightly"},
    "mcp_presets": ["chrome-devtools"],
    "lsp_servers": {
      "rust": {"command": "rust-analyzer", "args": [],
               "fileExtensions": {".rs": "rust"}}
    },
    "packages": [
      "strace",
      "gtk4", "gtk4.dev",
      {"name": "gtk4-layer-shell", "outputs": ["out", "dev"]},
      {"name": "freetype", "nixpkgs": "e6f23dc0..."},
      {"name": "freetype", "version": "2.14.1",
       "url": "mirror://savannah/freetype/freetype-2.14.1.tar.xz",
       "hash": "sha256-MkJ+jEcawJWFMhKjeu+BbGC0IFLU2eSCMLqzvfKTbMw="}
    ],
    "env_sources": [{"DEBUG": "1"}, "~/.config/yolo-jail/secrets.env"],
    "mounts": ["/path/to/ref-repo"],
    "devices": [
      {"usb": "0bda:2838", "description": "RTL-SDR Blog V4"}
    ],
    "gpu": {
      "enabled": true,
      "devices": "all",
      "capabilities": "compute,utility"
    },
    "resources": {
      "memory": "8g",
      "cpus": 4,
      "pids_limit": 4096
    },
    "network": {
      "mode": "bridge",
      "ports": ["8000:8000"],
      "forward_host_ports": [5432]
    },
    "security": {
      "blocked_tools": [
        {"name": "grep", "message": "Use rg", "suggestion": "rg <pattern>"},
        "wget"
      ]
    }
  }

[bold cyan]ENVIRONMENT VARIABLES[/bold cyan]

  YOLO_RUNTIME          Override container runtime (podman/container)
  YOLO_BYPASS_SHIMS     Set to 1 to bypass blocked tool shims
  YOLO_EXTRA_PACKAGES   JSON array of extra nix packages (internal)

[bold cyan]CONFIG CHANGE SAFETY[/bold cyan]

  When yolo-jail.jsonc changes between jail startups, the CLI shows a
  diff of the normalized config and asks for y/N confirmation. This
  prevents agents from silently adding packages or mounts without the
  human operator noticing. Agents should still run `yolo check` after
  every config edit before asking for that restart.

  - First run: config is accepted and a snapshot saved.
  - Subsequent runs: changes require explicit y/N approval.
  - Non-interactive (piped input): accepted with a warning.

  Snapshot location: <workspace>/.yolo/config-snapshot.json

[bold cyan]AGENT PACKAGE WORKFLOW[/bold cyan]

  Agents inside the jail can request new packages:

  1. Agent edits /workspace/yolo-jail.jsonc, adds to "packages" array
  2. Agent ALWAYS runs `yolo check` after the edit (`--no-build` is okay inside a running jail)
  3. If the check passes, agent tells the human: "Please restart the jail for new packages"
  4. On next startup, human sees the config diff and approves (y/N)
  5. Image rebuilds with the new package
  6. Agent can use the package after restart

  This keeps the human in the loop for all environment changes.
  Do NOT install packages via apt, nix-env, or other package managers.

  [bold cyan]COMMANDS[/bold cyan]

  yolo                      Start interactive jail shell
  yolo -- <command>         Run a command inside the jail
  yolo --new -- <command>   Force a new container
  yolo check                Validate config and preflight the build
  yolo ps                   List running jail containers
  yolo init                 Create yolo-jail.jsonc in current directory
  yolo init-user-config     Create user-level defaults config
  yolo config-ref           Show this reference

[bold cyan]INSIDE THE JAIL[/bold cyan]

  [bold]Workspace[/bold]
    Your project is bind-mounted read-write at /workspace.
    Edits are visible on the host immediately — this is the SAME directory.
    The workspace path changes from the host path to /workspace.

  [bold]Networking[/bold]
    Full internet access is available. Bridge mode (default) isolates the
    container network but allows outbound connections. Use network.ports
    to publish container ports to the host. Host mode shares the host
    network stack directly.

  [bold]Home Directory (/home/agent)[/bold]
    A shared persistent home that is the SAME across ALL jail workspaces.
    Contains: auth tokens (gh, gemini, claude), tool caches, npm/go globals,
    nvim config, shell configs, mise tool data. All of this survives
    jail restarts and is shared between every project's jail.

  [bold]Per-Workspace State[/bold]
    Some state is isolated per-workspace (in <workspace>/.yolo/):
    SSH keys, bash history, copilot sessions, gemini history, claude projects.
    These are NOT shared across different project jails.

  [bold]Identity & Auth[/bold]
    Git/jj identity (name + email) is injected from the host automatically.
    GitHub CLI (gh) is pre-authenticated via the shared home.
    SSH keys are per-workspace — configure in <workspace>/.yolo/home/ssh/.

  [bold]Tools & Runtimes[/bold]
    Runtimes: Node.js 22, Python 3.13, Go (managed by mise)
    Editors:  nvim (version configurable via mise_tools config)
    CLI:      rg, fd, bat, jq, git, jj, gh, curl, strace, uv, tmux
    Agents:   copilot, gemini (--yolo auto-injected), claude (YOLO mode via settings.json)
    The 'yolo' command is available inside for nested jailing and help.

  [bold]Mise Tool Management[/bold]
    Mise manages all runtimes and supports thousands of tools from
    multiple registries:
    • aqua — pre-built binaries (kubectl, terraform, gh, etc.)
    • asdf — version-managed runtimes (python, node, ruby, etc.)
    • cargo — Rust crates (ripgrep, fd-find, bat, etc.)
    • go — Go modules (built from source)
    • npm — Node packages (installed globally)
    • pipx — Python CLI tools (isolated envs)
    • ubi — universal binary installer (GitHub releases)
    Run 'mise registry' to browse all available tools. Add tools via:
    • "mise_tools" in yolo-jail.jsonc (injected into jail global config)
    • /workspace/mise.toml (workspace-specific, checked into git)
    The host's mise data directory is shared with the jail, so tool
    installs are available in both environments.

  [bold]Blocked Tools[/bold]
    By default, grep is replaced by rg and find by fd. These are shims —
    set YOLO_BYPASS_SHIMS=1 in scripts that need the real commands.
    Configure via security.blocked_tools in yolo-jail.jsonc.

  [bold]Venvs & Python[/bold]
    The host's mise data directory is shared with the jail, so venvs
    created on the host resolve inside the jail (python binary paths
    match). The workspace path changes to /workspace though, so
    venv scripts with absolute shebangs may need fixing.

  [bold]Persistence Summary[/bold]
    Shared home:   /home/agent (same across all jails — auth, tools, caches)
    Workspace:     /workspace edits visible on host immediately
    Per-workspace: SSH keys, bash history, copilot/gemini sessions
    Ephemeral:     /tmp, container processes

[bold cyan]SPAWNING A NEW PROJECT[/bold cyan]

  When setting up a new project for jail use:

  1. Run 'yolo init' in the project root to create yolo-jail.jsonc
  2. Edit the config — add any nix packages or mise_tools needed
  3. Run 'yolo check' after EVERY config edit to validate the config before restarting
  4. Run 'yolo -- bash' to enter the jail interactively
  5. Start your agent: 'yolo -- copilot', 'yolo -- gemini', or 'yolo -- claude'

  [bold]For agents preparing to enter a jail:[/bold]
  Before asking the human to restart you inside the jail, ALWAYS run 'yolo check'
  and write a
  handoff document (e.g., scratch/jail-notes.md) with:
  • Current task state and what remains to be done
  • Decisions made and their rationale
  • Key files to examine first
  Your inner-jail self will be a fresh session without your context.
""")


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
    cnames = [c.strip() for c in result.stdout.splitlines() if c.strip()]
    if not cnames:
        ok("no jails running — nothing to probe")
        return
    for cname in cnames:
        sockets_dir = _host_service_sockets_dir(cname)
        for lp in externals:
            # Singleton broker: its per-jail entry is a bind-mount
            # placeholder (zero-byte regular file on the host;
            # connect() against it raises ENOTSOCK).  Liveness for
            # the singleton is checked separately in
            # ``_check_loopholes`` via ``_broker_status`` against the
            # well-known singleton path.  Probing here was producing
            # ``socket dead`` false positives that sent investigators
            # down the wrong trail (handoff 2026-04-28).
            if lp.name == BROKER_LOOPHOLE_NAME:
                continue
            sock_path = sockets_dir / f"{lp.name}.sock"
            label = f"loophole {lp.name} @ {cname}"
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




@app.command()
def check(
    build: bool = typer.Option(
        True,
        "--build/--no-build",
        help="Run nix build as part of the preflight (default: on)",
    ),
):
    """Validate environment, config, and build. Run after every config edit."""
    ensure_global_storage()
    workspace = Path.cwd()

    passed = 0
    failed = 0
    warned = 0

    def _print_note(note: str) -> None:
        """Render a note; every line gets the same indent, first line
        marked with an arrow so multi-line messages don't become a wall
        of text."""
        lines = note.splitlines() or [note]
        for i, line in enumerate(lines):
            prefix = "     → " if i == 0 else "       "
            console.print(f"{prefix}{line}")

    def ok(msg: str):
        nonlocal passed
        passed += 1
        console.print(f"  ✅ {msg}")

    def fail(msg: str, note: str = ""):
        nonlocal failed
        failed += 1
        console.print(f"  ❌ {msg}")
        if note:
            _print_note(note)

    def warn(msg: str, note: str = ""):
        nonlocal warned
        warned += 1
        console.print(f"  ⚠️  {msg}")
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
    # live before deciding severity on the rest.
    offline: list[tuple[str, str, str]] = []  # (rt, version, hint)
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
                f"  [dim]· {rt}: {version} (not connected — not selected)[/dim]"
            )
    if detected_runtime is None:
        fail(
            "No container runtime found",
            "Install podman, or Apple Container (macOS)",
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

        # Check for Linux builder (required for cross-building images)
        try:
            machines_file = Path("/etc/nix/machines")
            cfg_result = subprocess.run(
                ["nix", "show-config"],
                capture_output=True,
                text=True,
                timeout=10,
            )
            has_builder = False
            if cfg_result.returncode == 0:
                for line in cfg_result.stdout.split("\n"):
                    if line.startswith("builders =") and "@" in line:
                        if machines_file.exists() and machines_file.read_text().strip():
                            has_builder = True
                    if line.startswith("extra-platforms =") and "linux" in line:
                        warn(
                            "extra-platforms includes linux — builds will fail locally",
                            "Remove 'extra-platforms = aarch64-linux' from "
                            "/etc/nix/nix.custom.conf; use a remote builder instead",
                        )
            if has_builder:
                ok("Linux builder configured in /etc/nix/machines")
            else:
                # The flake fetches aarch64-linux packages from the binary
                # cache rather than building them locally, so a Linux builder
                # is not required in practice. Surface as info only.
                warn(
                    "No Linux builder configured (binary cache substitution used)",
                    "A remote Linux builder speeds up fresh image builds. "
                    "See docs/macos.md for optional setup with Colima or a remote host",
                )
        except Exception:
            pass
    console.print()

    if IS_MACOS:
        console.print("[bold]macOS Platform[/bold]")
        ok(f"Architecture: {platform.machine()}")

        # Container VM backend check
        for vm_backend in ("colima", "podman"):
            vm_path = shutil.which(vm_backend)
            if vm_path:
                try:
                    if vm_backend == "colima":
                        result = subprocess.run(
                            ["colima", "status"],
                            capture_output=True,
                            text=True,
                            timeout=5,
                        )
                        if result.returncode == 0:
                            ok("Colima: running")
                        else:
                            warn(
                                "Colima installed but not running",
                                "Start with: colima start --arch aarch64 --cpu 4 --memory 8",
                            )
                    else:
                        result = subprocess.run(
                            ["podman", "machine", "info"],
                            capture_output=True,
                            text=True,
                            timeout=5,
                        )
                        if result.returncode == 0:
                            ok("Podman Machine: available")
                        else:
                            warn("Podman Machine: not configured")
                except Exception as e:
                    warn(f"{vm_backend}: {e}")

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
                        "Apple Container CLI: installed but not working",
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
        ("Mise", GLOBAL_MISE),
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
        user_config = _load_jsonc_file(
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
        workspace_config = _load_jsonc_file(
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
        if not (repo_root / "src" / "entrypoint.py").exists():
            raise ConfigError(f"entrypoint source not found under {repo_root}")
        _entrypoint_preflight(repo_root, workspace, config)
        ok("Generated Copilot/Gemini/Claude jail config in a temp home")
    except (ConfigError, SystemExit) as e:
        fail("Entrypoint preflight failed", str(e))
    console.print()

    # --- GPU Checks ---

    gpu_config = config.get("gpu", {})
    if gpu_config.get("enabled", False):
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

    console.print("[bold]Image Build[/bold]")
    if build:
        out_link = BUILD_DIR / "check-result"
        if repo_root is None:
            fail("Skipped nix build", "repo root resolution failed")
        else:
            try:
                store_path, build_stderr_tail = _build_image_store_path(
                    repo_root,
                    extra_packages=config.get("packages") or None,
                    out_link=out_link,
                    status_message="[bold blue]Preflighting jail image...",
                )
                if store_path is None:
                    fail(
                        "nix build failed",
                        "\n".join(build_stderr_tail[-10:]) if build_stderr_tail else "",
                    )
                else:
                    ok(f"nix build succeeded: {store_path}")
            finally:
                out_link.unlink(missing_ok=True)
    else:
        warn("Skipped nix build (--no-build)")
    console.print()

    if detected_runtime:
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
                            "Run 'yolo' once to build and load the image",
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
                            "Run 'yolo' once to build and load the image",
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
                    console.print(f"    {cname} → {container_workspace}{marker}")
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
                    fail(f"loopholes.{name}: missing command")
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




def _inject_agent_yolo_flags(full_command: "list[str]") -> None:
    """Mutate ``full_command`` in place to inject agent-specific YOLO
    flags based on the leading binary name.

    - ``gemini``: prepend ``--yolo`` (unless the user passed ``-y`` /
      ``--yolo`` themselves).
    - ``copilot``: prepend ``--yolo`` AND ``--no-auto-update`` on the
      same no-duplicate basis.
    - ``claude``: prepend ``--dangerously-skip-permissions``.  The
      settings.json allow-list that used to serve as YOLO was
      half-broken for weeks (bare ``"Bash"`` is inert, Claude Code's
      matcher needs a pattern).  The flag is the single source of
      truth; ``IS_SANDBOX=1`` in the jail env already suppresses the
      flag's own confirmation prompt.

    All other commands are left alone.
    """
    if not full_command:
        return
    head = full_command[0]
    if head in ("gemini", "copilot"):
        if "--yolo" not in full_command and "-y" not in full_command:
            full_command.insert(1, "--yolo")
    if head == "copilot":
        if "--no-auto-update" not in full_command:
            full_command.insert(1, "--no-auto-update")
    if head == "claude":
        if "--dangerously-skip-permissions" not in full_command:
            full_command.insert(1, "--dangerously-skip-permissions")


@app.command(
    context_settings={"allow_extra_args": True, "ignore_unknown_options": True}
)
def run(
    ctx: typer.Context,
    network: str = typer.Option("bridge", help="Container network mode (bridge/host)"),
    new: bool = typer.Option(
        False,
        "--new",
        help="Force a new container even if one already exists for this workspace",
    ),
    profile: bool = typer.Option(
        False,
        "--profile",
        help="Show detailed startup performance timing after command exits",
    ),
):
    """Run the YOLO jail in the current directory."""
    repo_root = _resolve_repo_root()
    workspace = Path.cwd()

    ensure_global_storage()
    try:
        config = load_config(workspace, strict=True)
    except ConfigError as e:
        console.print(f"[bold red]{e}[/bold red]")
        sys.exit(1)
    config_errors, config_warnings = _validate_config(config, workspace=workspace)
    # Cross-hierarchy overrides are valid, but same-file contradictions are not.
    try:
        user_raw = _load_jsonc_file(
            USER_CONFIG_PATH, str(USER_CONFIG_PATH), strict=False
        )
    except Exception:
        user_raw = {}
    ws_config_path = workspace / "yolo-jail.jsonc"
    try:
        ws_raw = _load_jsonc_file(ws_config_path, "yolo-jail.jsonc", strict=False)
    except Exception:
        ws_raw = {}
    config_errors.extend(_check_preset_null_conflicts(user_raw, str(USER_CONFIG_PATH)))
    config_errors.extend(_check_preset_null_conflicts(ws_raw, "yolo-jail.jsonc"))
    if config_warnings:
        for message in config_warnings:
            console.print(f"  [yellow]⚠ {message}[/yellow]")
    if config_errors:
        console.print("[bold red]Invalid jail config:[/bold red]")
        for message in config_errors:
            console.print(f"  • {message}")
        console.print(
            "\n[dim]Run `yolo check` for a full preflight before restarting.[/dim]"
        )
        sys.exit(1)
    runtime = _runtime(config)

    # Command construction (needed for both exec and run paths)
    full_command = list(ctx.args)

    target_cmd = "bash"
    if full_command:
        _inject_agent_yolo_flags(full_command)
        target_cmd = shlex.join(full_command)

    # Collect identity env vars early — needed for both exec and run paths
    identity_env = []
    try:
        git_name = (
            subprocess.check_output(
                ["git", "config", "--get", "user.name"], stderr=subprocess.DEVNULL
            )
            .decode()
            .strip()
        )
        if git_name:
            identity_env.extend(["-e", f"YOLO_GIT_NAME={git_name}"])
    except Exception:
        pass
    try:
        git_email = (
            subprocess.check_output(
                ["git", "config", "--get", "user.email"], stderr=subprocess.DEVNULL
            )
            .decode()
            .strip()
        )
        if git_email:
            identity_env.extend(["-e", f"YOLO_GIT_EMAIL={git_email}"])
    except Exception:
        pass
    try:
        jj_name = (
            subprocess.check_output(
                ["jj", "config", "get", "user.name"], stderr=subprocess.DEVNULL
            )
            .decode()
            .strip()
            .strip('"')
        )
        if jj_name:
            identity_env.extend(["-e", f"YOLO_JJ_NAME={jj_name}"])
    except Exception:
        pass
    try:
        jj_email = (
            subprocess.check_output(
                ["jj", "config", "get", "user.email"], stderr=subprocess.DEVNULL
            )
            .decode()
            .strip()
            .strip('"')
        )
        if jj_email:
            identity_env.extend(["-e", f"YOLO_JJ_EMAIL={jj_email}"])
    except Exception:
        pass

    # Check for existing container BEFORE touching the image.
    # If one is already running we just exec into it — no rebuild needed.
    cname = container_name_for_workspace(workspace)
    existing_cid = None if new else find_running_container(cname, runtime=runtime)

    if existing_cid:
        # Exec into the existing container.  Surface the jail's baked
        # version so a host CLI upgrade attaching to a pre-upgrade
        # container (stale shims / mounts) is visible at a glance.
        host_version = _get_yolo_version()
        jail_version = _container_baked_yolo_version(runtime, cname)
        _print_startup_banner(host_version, runtime, cname, jail_version=jail_version)
        console.print(
            f"[bold cyan]Attaching to existing jail [dim]({cname})[/dim]...[/bold cyan]"
        )
        _tmux_rename_window("JAIL")
        exec_flags = ["-i"]
        if sys.stdout.isatty():
            exec_flags.append("-t")
        run_cmd = [
            runtime,
            "exec",
            *exec_flags,
            *identity_env,
            cname,
            "yolo-entrypoint",
            target_cmd,
        ]
        # Use subprocess.run (not execvp) so atexit handlers fire for tmux cleanup
        try:
            result = subprocess.run(run_cmd)
        except FileNotFoundError:
            console.print(
                f"[bold red]Configured runtime '{runtime}' not found on PATH.[/bold red]"
            )
            console.print(
                "[dim]Run `yolo check` to validate runtime availability before restarting.[/dim]"
            )
            sys.exit(1)
        sys.exit(result.returncode)

    # No existing container — build/load the image then start a new one.
    # Check for config changes and get human confirmation
    if not _check_config_changes(workspace, config):
        sys.exit(1)

    # Acquire a workspace-specific lock to prevent two concurrent yolo invocations
    # from racing on build + container creation. The loser waits, then execs into
    # the container the winner created.
    lock_path = GLOBAL_STORAGE / "locks"
    lock_path.mkdir(parents=True, exist_ok=True)
    lock_file = open(lock_path / f"{cname}.lock", "w")
    try:
        fcntl.flock(lock_file, fcntl.LOCK_EX)
    except OSError as e:
        console.print(
            f"[dim]Warning: could not acquire workspace lock ({e}); race protection disabled[/dim]"
        )

    # Re-check after acquiring the lock — another process may have started
    # a container while we were waiting.
    if not new:
        raced_cid = find_running_container(cname, runtime=runtime)
        if raced_cid:
            lock_file.close()
            _print_startup_banner(_get_yolo_version(), runtime, cname)
            console.print(
                f"[bold cyan]Attaching to jail started by another process [dim]({cname})[/dim]...[/bold cyan]"
            )
            _tmux_rename_window("JAIL")
            exec_flags = ["-i"]
            if sys.stdout.isatty():
                exec_flags.append("-t")
            run_cmd = [
                runtime,
                "exec",
                *exec_flags,
                *identity_env,
                cname,
                "yolo-entrypoint",
                target_cmd,
            ]
            try:
                result = subprocess.run(run_cmd)
            except FileNotFoundError:
                console.print(
                    f"[bold red]Configured runtime '{runtime}' not found on PATH.[/bold red]"
                )
                console.print(
                    "[dim]Run `yolo check` to validate runtime availability before restarting.[/dim]"
                )
                sys.exit(1)
            sys.exit(result.returncode)

    # Remove any stopped container with the same name left over from an
    # unclean shutdown (e.g. OOM-kill, host reboot).  Without this,
    # `<runtime> run --name <cname>` fails with "container already exists".
    stale_cid = find_existing_container(cname, runtime=runtime)
    if stale_cid:
        print(f"Removing stale container {cname}...", file=sys.stderr)
        _remove_stale_container(cname, runtime=runtime)

    import time as _time

    _profile_times = {}
    if profile:
        _profile_times["start"] = _time.monotonic()

    extra_packages = config.get("packages", [])
    mise_tools = _merge_mise_tools(config)
    lsp_servers = config.get("lsp_servers", {})
    mcp_servers = config.get("mcp_servers", {})
    mcp_presets = config.get("mcp_presets", [])
    host_claude_files = config.get("host_claude_files", DEFAULT_HOST_CLAUDE_FILES)
    user_env = _resolve_env_sources(workspace, config)
    mise_disabled_tools = _merge_mise_disabled_tools(user_env.get("MISE_DISABLE_TOOLS"))
    auto_load_image(repo_root, extra_packages=extra_packages or None, runtime=runtime)

    # Resolve host mise path — share the same data dir so venv paths match.
    # Inside a nested jail, YOLO_OUTER_MISE_PATH carries the original host path.
    host_mise = _host_mise_dir()

    if profile:
        _profile_times["image_loaded"] = _time.monotonic()

    # Determine Network Mode
    net_mode = network
    if config.get("network", {}).get("mode"):
        net_mode = config["network"]["mode"]

    # Determine Ports
    publish_args = []
    if net_mode == "bridge" and config.get("network", {}).get("ports"):
        for p in config["network"]["ports"]:
            publish_args.extend(["-p", p])

    # Host port forwarding (host services → container localhost)
    forward_host_ports = []
    if net_mode == "bridge" and config.get("network", {}).get("forward_host_ports"):
        forward_host_ports = config["network"]["forward_host_ports"]

    normalized_blocked = _normalize_blocked_tools(config.get("security"))
    blocked_config_json = json.dumps(normalized_blocked)

    # Process Extra Mounts
    mount_args = []
    mount_descriptions = []
    for mount in config.get("mounts", []):
        # Support "host:container" syntax — split on the LAST colon that precedes
        # an absolute container path (starts with /).  Plain host-only paths like
        # "/home/user/.copilot" or "~/data" fall through to the else branch.
        colon_idx = mount.rfind(":")
        if colon_idx > 0 and mount[colon_idx + 1 : colon_idx + 2] == "/":
            host_path = mount[:colon_idx]
            container_path = mount[colon_idx + 1 :]
        else:
            host_path = mount
            container_path = f"/ctx/{Path(host_path).expanduser().resolve().name}"
        host_path = str(Path(host_path).expanduser().resolve())
        if not Path(host_path).exists():
            console.print(
                f"[yellow]Warning: mount path does not exist, skipping: {host_path}[/yellow]"
            )
            continue
        mount_args.extend(["-v", f"{host_path}:{container_path}:ro"])
        mount_descriptions.append(f"{host_path}:{container_path}")

    # Construct Container Run Command
    run_flags = [
        "--rm",
        "-i",
        "--init",
        "--read-only",
        "--name",
        cname,
    ]
    # Apple Container doesn't support --cgroupns
    if runtime != "container":
        run_flags.insert(3, "--cgroupns=private")
    if runtime == "podman" and IS_LINUX:
        # On Linux, Podman auto-adds tmpfs mounts for /run, /tmp, /dev/shm when
        # --read-only is set.  This conflicts with our explicit --tmpfs /tmp and
        # can trigger conmon JSON parsing errors with crun.  Disable the
        # auto-tmpfs and let our explicit mounts handle it.
        # On macOS (Podman Machine), do NOT set this: crun inside the VM needs
        # --read-only-tmpfs enabled so it can unlink /dev/console when -t is
        # passed (otherwise: "crun: unlink /dev/console: Read-only file system").
        run_flags.append("--read-only-tmpfs=false")
    if runtime == "podman":
        # Never let podman fall back to a registry pull. The image is built
        # locally by nix and loaded by auto_load_image; if it isn't present
        # at run time, that's a bug we want to see — not a confusing
        # "Trying to pull docker://localhost/yolo-jail:latest" retry loop.
        run_flags.extend(["--pull=never"])
        # Don't capture container stdout/stderr anywhere.  The default log
        # driver on systemd hosts is journald, which means every interactive
        # TUI redraw (Claude Code's status line, vim scrolls, progress bars)
        # lands in the user's journal at kilobytes-per-keystroke.  We never
        # actually read `podman logs <name>` — failure diagnosis lives in
        # the agent-side logs under ~/.local/share/yolo-jail/logs/ and in
        # the nix build output.  Drop it on the floor.
        run_flags.extend(["--log-driver", "none"])
    if sys.stdout.isatty():
        run_flags.append("-t")

    # Per-workspace overlays for workspace-specific state
    ws_state = workspace / ".yolo" / "home"
    ws_state.mkdir(parents=True, exist_ok=True)
    (ws_state / "ssh").mkdir(exist_ok=True, mode=0o700)
    # Per-workspace writable overlays — isolate cross-jail writes.
    # These sit on top of the :ro GLOBAL_HOME base so each jail has its
    # own copy of generated configs, installed tools, and caches.
    for subdir in [
        "npm-global",
        "local",
        "go",
        "yolo-shims",
        "config",
        "copilot",
        "gemini",
        "claude",
    ]:
        (ws_state / subdir).mkdir(exist_ok=True)
    for fname in [
        "bash_history",
        "yolo-bootstrap.sh",
        "yolo-venv-precreate.sh",
        "yolo-perf.log",
        "yolo-socat.log",
        "yolo-entrypoint.lock",
        # CA bundle is per-workspace: the set of active loopholes (and
        # therefore the set of CAs to trust) is workspace-specific.
        "yolo-ca-bundle.crt",
    ]:
        (ws_state / fname).touch()

    # Seed agent config dirs with auth tokens from the :ro GLOBAL_HOME base.
    # On first boot for this workspace the per-workspace dirs are empty — copy
    # auth-related files so agents can authenticate.  Subsequent boots skip
    # files that already exist (the entrypoint regenerates configs each time).
    _seed_agent_dir(GLOBAL_HOME / ".copilot", ws_state / "copilot")
    _seed_agent_dir(GLOBAL_HOME / ".gemini", ws_state / "gemini")
    # Credentials are in the shared dir (.claude-shared-credentials/), not
    # .claude/, so no skip needed — _seed_agent_dir won't encounter them.
    _seed_agent_dir(GLOBAL_HOME / ".claude", ws_state / "claude")

    # Seed claude.json onboarding state into the per-workspace overlay.
    # ~/.claude.json is a symlink → .claude/claude.json, so the actual file
    # lives inside the writable .claude/ overlay.  Merge GLOBAL_HOME's data
    # (hasCompletedOnboarding, numStartups, oauthAccount, etc.) into the
    # per-workspace file, filling missing keys while preserving workspace-specific
    # MCP server config.
    src_claude_json = GLOBAL_HOME / ".claude" / "claude.json"
    dst_claude_json = ws_state / "claude" / "claude.json"
    if src_claude_json.is_file():
        try:
            src_data = json.loads(src_claude_json.read_text())
            try:
                dst_data = json.loads(dst_claude_json.read_text())
            except (json.JSONDecodeError, FileNotFoundError, OSError):
                dst_data = {}
            for key, val in src_data.items():
                if key not in dst_data:
                    dst_data[key] = val
            dst_claude_json.write_text(json.dumps(dst_data, indent=2) + "\n")
        except (json.JSONDecodeError, OSError):
            pass

    # Migrate old per-workspace overlays into new unified agent dirs.
    # Before the read-only refactor, agent state used individual file/dir overlays
    # (e.g. claude-projects/, copilot-sessions/).  Now each agent gets a single
    # dir overlay (claude/, copilot/, gemini/).  Copy old data once if present.
    _migrate_old_overlay(ws_state / "claude-projects", ws_state / "claude" / "projects")
    _migrate_old_overlay(
        ws_state / "copilot-sessions", ws_state / "copilot" / "session-state"
    )
    _migrate_old_overlay(ws_state / "gemini-history", ws_state / "gemini" / "history")

    # Migrate old claude-settings.json file overlay into new claude/settings.json.
    # Preserves user customizations (model, hooks, etc.) from pre-refactor.
    old_claude_settings = ws_state / "claude-settings.json"
    new_claude_settings = ws_state / "claude" / "settings.json"
    if old_claude_settings.is_file() and not new_claude_settings.exists():
        (ws_state / "claude").mkdir(parents=True, exist_ok=True)
        shutil.copy2(old_claude_settings, new_claude_settings)

    # Detect host timezone once — passed to the container via TZ env var below
    # so `date` and glibc time functions report host-local time instead of UTC.
    host_tz = _detect_host_timezone()

    if runtime == "container":
        # Apple Container has a limit of ~22 directory sharing devices
        # (Virtualization.framework constraint).  Instead of the GLOBAL_HOME :ro
        # base + 15 individual per-workspace writable overlays (which would use
        # 16 slots), mount ws_state as a single writable /home/agent.
        # Auth tokens are already seeded into ws_state from GLOBAL_HOME above.
        #
        # Note: Apple Container cannot do the cross-jail shared .credentials.json
        # rw mount (one bind mount per file would push us over the device limit).
        # On AC, each workspace has its own credentials file; cross-jail /login
        # propagation requires podman on macOS, or running the
        # host-side claude-oauth-broker which refreshes against the
        # GLOBAL_HOME source.
        run_cmd = [
            runtime,
            "run",
            *run_flags,
            "-v",
            f"{workspace}:/workspace",
            "-v",
            f"{ws_state}:/home/agent",
            "-v",
            f"{GLOBAL_CACHE}:/home/agent/.cache",
            # Host mise dir mirrored at its native host path so absolute paths
            # baked into host-side venvs (e.g., python symlink targets) resolve
            # inside the jail. On macOS the host tree has Mach-O binaries that
            # can't run in Linux, so we back it with a named volume but mount
            # it at the same host path string — keeping a single canonical
            # mise location across runtimes.
            "-v",
            f"yolo-mise-data:{host_mise}",
            # Apple Container's --tmpfs only takes a plain path (no options)
            "--tmpfs",
            "/tmp",
            "--tmpfs",
            "/var/tmp",
            "--tmpfs",
            "/var/lib/containers",
            "--tmpfs",
            "/run",
            "--tmpfs",
            "/dev/shm",
        ]
    else:
        run_cmd = [
            runtime,
            "run",
            *run_flags,
            "-v",
            f"{workspace}:/workspace",
            # Global home — read-only base with auth tokens and base configs.
            # Per-workspace writable overlays are mounted on top below.
            "-v",
            f"{GLOBAL_HOME}:/home/agent:ro",
            # --- Per-workspace writable overlays (isolate cross-jail writes) ---
            # Directories: installed tools, generated configs, shims
            "-v",
            f"{ws_state / 'npm-global'}:/home/agent/.npm-global",
            "-v",
            f"{ws_state / 'local'}:/home/agent/.local",
            "-v",
            f"{ws_state / 'go'}:/home/agent/go",
            "-v",
            f"{ws_state / 'yolo-shims'}:/home/agent/.yolo-shims",
            "-v",
            f"{ws_state / 'config'}:/home/agent/.config",
            # Shared download cache (CAS — safe across workspaces, avoids re-downloads)
            "-v",
            f"{GLOBAL_CACHE}:/home/agent/.cache",
            # Files: generated scripts, configs, logs
            # (.bashrc and .gitconfig are symlinks into the writable .config/ overlay,
            # so they don't need separate file bind mounts.)
            "-v",
            f"{ws_state / 'yolo-bootstrap.sh'}:/home/agent/.yolo-bootstrap.sh",
            "-v",
            f"{ws_state / 'yolo-venv-precreate.sh'}:/home/agent/.yolo-venv-precreate.sh",
            "-v",
            f"{ws_state / 'yolo-perf.log'}:/home/agent/.yolo-perf.log",
            "-v",
            f"{ws_state / 'yolo-socat.log'}:/home/agent/.yolo-socat.log",
            "-v",
            f"{ws_state / 'yolo-entrypoint.lock'}:/home/agent/.yolo-entrypoint.lock",
            # Writable overlay for the jail-generated CA bundle.  Without
            # this, the entrypoint's write to /home/agent/.yolo-ca-bundle.crt
            # hits the :ro GLOBAL_HOME base and raises EROFS.
            "-v",
            f"{ws_state / 'yolo-ca-bundle.crt'}:/home/agent/.yolo-ca-bundle.crt",
            # Agent config dirs — full per-workspace overlays.
            # Auth tokens are seeded from GLOBAL_HOME on first use (see _seed_agent_dir).
            # The entrypoint regenerates all configs into these writable dirs each boot.
            "-v",
            f"{ws_state / 'copilot'}:/home/agent/.copilot",
            "-v",
            f"{ws_state / 'gemini'}:/home/agent/.gemini",
            "-v",
            f"{ws_state / 'claude'}:/home/agent/.claude",
            # Shared credentials dir — mounted rw so /login in any jail
            # persists for all jails.  Using a directory mount (not a
            # single-file mount) because Claude Code's IWH atomic writer
            # uses tmp+rename which returns EBUSY on single-file bind
            # mounts.  The entrypoint creates a symlink from
            # .claude/.credentials.json → this dir so Claude finds it.
            "-v",
            f"{GLOBAL_HOME / '.claude-shared-credentials'}:/home/agent/.claude-shared-credentials",
            # Other per-workspace overlays
            "-v",
            f"{ws_state / 'bash_history'}:/home/agent/.bash_history",
            "-v",
            f"{ws_state / 'ssh'}:/home/agent/.ssh",
            # --- Shared mounts ---
            "-v",
            # Host mise dir mirrored at its native host path inside the jail.
            # This keeps absolute paths baked into host-side venvs (python
            # symlink targets, shebangs) resolvable from inside the container —
            # no /mise alias, no divergence.
            #
            # On macOS the host tree has Mach-O (darwin) binaries that cannot
            # execute inside the Linux container, so we back the mount with a
            # podman named volume that holds Linux toolchains. The volume is
            # mounted at the same host path string, keeping a single canonical
            # mise location across runtimes (and matching what nested jails see).
            f"yolo-mise-data:{host_mise}" if IS_MACOS else f"{host_mise}:{host_mise}",
            "--tmpfs",
            # Explicit mode=1777 ensures non-root UIDs can write to tmpfs
            # (some container backends default to 755).
            "/tmp:exec,mode=1777",
            "--tmpfs",
            "/var/tmp:exec,mode=1777",
            # Podman needs writable storage, runtime dirs, and shared memory for nested containers.
            # --read-only-tmpfs=false disables automatic tmpfs mounts (including /dev/shm),
            # so we must explicitly mount all tmpfs paths podman needs.
            "--tmpfs",
            "/var/lib/containers",
            "--tmpfs",
            "/run",
            "--tmpfs",
            "/dev/shm:size=2g",
        ]

    # Common env vars and flags for all runtimes
    run_cmd.extend(
        [
            "-e",
            "JAIL_HOME=/home/agent",
            "-e",
            "NPM_CONFIG_PREFIX=/home/agent/.npm-global",
            "-e",
            # Redirect npm cache to the writable shared cache dir (GLOBAL_HOME is :ro,
            # so the default ~/.npm/_cacache would fail with EROFS).
            "NPM_CONFIG_CACHE=/home/agent/.cache/npm",
            "-e",
            "GOPATH=/home/agent/go",
            "-e",
            f"MISE_DATA_DIR={host_mise}",
            "-e",
            # Use a per-container cache dir so mise lockfiles don't contend with
            # the host/outer-jail's locks (shared /home/agent would otherwise share
            # ~/.cache/mise/lockfiles/, causing deadlocks in nested jails).
            "MISE_CACHE_DIR=/tmp/mise-cache",
            "-e",
            # Explicitly request the non-freethreaded prebuilt to avoid
            # "missing lib directory" errors from freethreaded builds.
            "MISE_PYTHON_PRECOMPILED_FLAVOR=install_only_stripped",
            "-e",
            "MISE_TRUST=1",
            "-e",
            "MISE_YES=1",
            "-e",
            "COPILOT_ALLOW_ALL=true",
            # Tell Claude Code this is a sandboxed environment so it skips the
            # root-user check that blocks bypassPermissions / --dangerously-skip-permissions.
            # This is a belt-and-suspenders fix: the entrypoint also configures
            # permissions.allow rules instead of bypassPermissions.
            "-e",
            "IS_SANDBOX=1",
            "-e",
            f"LD_LIBRARY_PATH=/lib:/usr/lib:/usr/lib/{_linux_multilib()}",
            "-e",
            "HOME=/home/agent",
            # EDITOR=cat prevents agents from getting stuck in interactive editors.
            # VISUAL=nvim is used by Copilot ctrl-g (checks COPILOT_EDITOR > VISUAL > EDITOR).
            # These must be container-level env vars, not just in .bashrc, because
            # Copilot runs as a non-interactive process that doesn't source .bashrc.
            "-e",
            "EDITOR=cat",
            "-e",
            "VISUAL=nvim",
            "-e",
            "PAGER=cat",
            "-e",
            "GIT_PAGER=cat",
            "-e",
            f"YOLO_BLOCK_CONFIG={blocked_config_json}",
            # TZ is set from the host's timezone so `date` and time functions
            # inside the jail report the same wall-clock time as the host.
            # Without this, the minimal Linux image defaults to UTC, which is
            # confusing for log timestamps, cron expressions, and file mtimes.
            *(["-e", f"TZ={host_tz}"] if host_tz else []),
            "-e",
            f"YOLO_HOST_DIR={workspace}",
            "-e",
            f"YOLO_VERSION={_git_describe_version() or 'unknown'}",
            "-e",
            "OVERMIND_SOCKET=/tmp/overmind.sock",
            "-e",
            f"YOLO_MISE_TOOLS={json.dumps(mise_tools)}",
            "-e",
            f"YOLO_LSP_SERVERS={json.dumps(lsp_servers)}",
            "-e",
            f"YOLO_MCP_SERVERS={json.dumps(mcp_servers)}",
            "-e",
            f"YOLO_MCP_PRESETS={json.dumps(mcp_presets)}",
            "-e",
            # Inside the container, podman is always the available runtime (it's
            # built into the image).  Using the host's runtime value (e.g.
            # 'container' on macOS) would fail since that CLI isn't in the image.
            "YOLO_RUNTIME=podman",
            "-e",
            "YOLO_REPO_ROOT=/opt/yolo-jail",
        ]
    )

    # User-defined environment variables from config.
    # Written to a sourceable file instead of container -e flags so they can be
    # overridden by editing .env or the file inside the jail without restarting.
    user_env_file = ws_state / "yolo-user-env.sh"
    if user_env:
        lines = ["# Auto-generated from yolo-jail.jsonc env config.\n"]
        lines.append("# Override by editing this file or workspace .env (mise).\n")
        for env_key, env_val in user_env.items():
            # Only set if not already overridden (e.g. by mise .env loading)
            escaped = env_val.replace("'", "'\\''")
            lines.append(
                "export %(k)s=${%(k)s:-'%(v)s'}\n" % {"k": env_key, "v": escaped}
            )
        user_env_file.write_text("".join(lines))
    else:
        # Ensure the file exists (empty) so the mount doesn't fail
        user_env_file.touch()
    if runtime == "container":
        # See _ac_materialize_under_ws_state — AC can't do single-file
        # mounts under the ws_state parent mount without dropping it.
        _ac_materialize_under_ws_state(
            user_env_file, ".config/yolo-user-env.sh", ws_state
        )
    else:
        run_cmd.extend(["-v", f"{user_env_file}:/home/agent/.config/yolo-user-env.sh"])

    run_cmd += [
        "--workdir",
        "/workspace",
        # Mount yolo-jail repo for in-jail CLI (yolo --help, nested jailing).
        # In nested jails, YOLO_REPO_ROOT may point to an empty /opt/yolo-jail
        # (bind mount doesn't propagate). Fall back to /workspace if it's the repo.
        "-v",
        f"{repo_root}:/opt/yolo-jail:ro"
        if (repo_root / "flake.nix").exists()
        else f"{workspace}:/opt/yolo-jail:ro",
    ]

    # Detect if we're already inside a container (macOS host is never in a container)
    in_container = not IS_MACOS and (
        Path("/run/.containerenv").exists() or Path("/.dockerenv").exists()
    )

    # Check if GPU passthrough is both requested and actually available on
    # this host.  Same config works on a GPU box and a GPU-less machine:
    # if drivers/CDI are missing we fall back to starting without GPU flags
    # rather than letting podman error on a nonexistent CDI device.  This
    # also gates the uidmap/runc branch below — that strategy only makes
    # sense when we're really going to inject CDI devices.
    gpu_requested = config.get("gpu", {}).get("enabled", False)
    gpu_unavailable_reason: Optional[str] = None
    if gpu_requested:
        ok_gpu, gpu_unavailable_reason = _gpu_host_available(runtime)
        gpu_enabled = ok_gpu
    else:
        gpu_enabled = False

    # Podman: enable nested container support (rootless podman-in-podman)
    # When running on the host, use UID/GID mapping to create a user namespace.
    # When already inside a container, share the parent's user namespace instead
    # to avoid kernel restrictions on doubly-nested user namespaces.
    if runtime == "podman":
        if in_container:
            # Inside a container: share parent's user namespace
            run_cmd.extend(
                [
                    "--security-opt",
                    "label=disable",
                    "--userns",
                    "host",
                ]
            )
        elif gpu_enabled:
            # GPU passthrough: CDI device injection fails with crun and custom
            # user namespaces (https://github.com/containers/podman/issues/27483).
            # Use runc to avoid the CDI+crun incompatibility, and identity UID/GID
            # mapping (same as non-GPU) instead of keep-id. keep-id forces podman
            # to shift UIDs across every file in every image layer — with a large
            # image (100 layers, multi-GB) and no native shifting support this
            # causes 10+ minute container startup. Identity mapping needs no
            # shifting since container UIDs match the namespace UIDs as stored.
            # SYS_ADMIN is needed for nested containers (podman-in-podman).
            run_cmd.extend(
                [
                    "--security-opt",
                    "label=disable",
                    "--uidmap",
                    "0:0:1",
                    "--uidmap",
                    "1:1:65536",
                    "--gidmap",
                    "0:0:1",
                    "--gidmap",
                    "1:1:65536",
                    "--runtime",
                    "runc",
                    "--cap-add",
                    "SYS_ADMIN",
                ]
            )
        else:
            # On host: create user namespace with UID/GID mapping for nesting
            run_cmd.extend(
                [
                    "--security-opt",
                    "label=disable",
                    "--device",
                    "/dev/fuse",
                    "--uidmap",
                    "0:0:1",
                    "--uidmap",
                    "1:1:65536",
                    "--gidmap",
                    "0:0:1",
                    "--gidmap",
                    "1:1:65536",
                    "--cap-add",
                    "SYS_ADMIN",
                    "--cap-add",
                    "MKNOD",
                ]
            )

    # Mount host nix daemon socket + store so nix builds work inside the jail.
    # NIX_REMOTE=daemon forces nix to use the host daemon (which has nixbld users)
    # instead of trying local store access (which fails on UID mapping/permissions).
    # On macOS, /nix exists on the host but the typical container runtime VM
    # (Podman Machine, Apple container) does not have it shared in — bind-mounting
    # would statfs-error at startup.  Setups that *do* share /nix into the runtime
    # VM can opt in via YOLO_NIX_HOST_DAEMON=1.
    nix_socket = Path("/nix/var/nix/daemon-socket")
    nix_store = Path("/nix/store")
    if _should_mount_host_nix(
        runtime,
        nix_socket_exists=nix_socket.exists(),
        nix_store_exists=nix_store.exists(),
        is_macos=IS_MACOS,
        opt_in_env=os.environ.get("YOLO_NIX_HOST_DAEMON"),
    ):
        # Apple Container VMs can't share Unix sockets via -v bind mounts
        run_cmd.extend(
            [
                "-v",
                f"{nix_socket}:{nix_socket}",
                "-v",
                f"{nix_store}:{nix_store}:ro",
                "-e",
                "NIX_REMOTE=daemon",
            ]
        )

    # Podman rootless uses pasta networking by default (no nftables needed).
    # Only pass --net explicitly for non-default modes like "host".
    # Inside a container, always use host networking (netavark can't create
    # network namespaces without NET_ADMIN).
    # Apple Container: each container gets its own VM with dedicated networking;
    # --net flags are not supported.
    if runtime == "container":
        pass  # Apple Container handles networking internally
    elif runtime == "podman" and in_container:
        run_cmd.append("--net=host")
    elif net_mode != "bridge":
        run_cmd.append(f"--net={net_mode}")

    # Pass identity env vars (git + jj) collected earlier
    run_cmd.extend(identity_env)

    # Propagate host global gitignore into the jail
    # (We don't mount ~/.gitconfig to avoid credential leaks, but gitignore is safe)
    try:
        excludes_file = (
            subprocess.check_output(
                ["git", "config", "--global", "--get", "core.excludesFile"],
                stderr=subprocess.DEVNULL,
            )
            .decode()
            .strip()
        )
        if excludes_file:
            excludes_path = Path(excludes_file).expanduser()
        else:
            excludes_path = Path.home() / ".config" / "git" / "ignore"
    except Exception:
        excludes_path = Path.home() / ".config" / "git" / "ignore"
    if excludes_path.is_file():
        if runtime == "container":
            _ac_materialize_under_ws_state(
                excludes_path, ".config/git/ignore", ws_state
            )
        else:
            run_cmd.extend(["-v", f"{excludes_path}:/home/agent/.config/git/ignore:ro"])
        run_cmd.extend(["-e", "YOLO_GLOBAL_GITIGNORE=/home/agent/.config/git/ignore"])

    run_cmd.extend(publish_args)
    run_cmd.extend(mount_args)

    # Enable iptables DNAT so published ports reach services bound to 127.0.0.1.
    # Container runtimes forward published-port traffic to the container's eth0,
    # not loopback — so services listening on localhost never see it.
    # Podman rootless runs as UID 0 in a user namespace, so iptables works.
    # route_localnet allows the kernel to route DNAT'd packets to 127.0.0.1;
    # the entrypoint adds matching iptables PREROUTING rules.
    if publish_args and runtime == "podman":
        run_cmd.extend(["--sysctl", "net.ipv4.conf.all.route_localnet=1"])
        # Extract container-side ports for the entrypoint's DNAT rules
        published_ports = []
        for p in config.get("network", {}).get("ports", []):
            spec = str(p)
            proto = "tcp"
            if "/" in spec:
                spec, proto = spec.rsplit("/", 1)
            parts = spec.split(":")
            container_port = parts[-1]  # always the last element
            published_ports.append(f"{container_port}/{proto}")
        if published_ports:
            run_cmd.extend(
                ["-e", f"YOLO_PUBLISHED_PORTS={json.dumps(published_ports)}"]
            )

    # Host port forwarding.
    # On Linux: uses Unix sockets bind-mounted between host and container.
    # On macOS+Apple Container: native --publish-socket for socket forwarding.
    # On macOS+Podman: virtiofs doesn't support Unix sockets, so the container-side
    # socat connects directly to host.containers.internal (TCP) instead.
    _host_tmp = Path("/tmp").resolve() if IS_MACOS else Path("/tmp")
    socket_dir = None
    if forward_host_ports:
        run_cmd.extend(
            ["-e", f"YOLO_FORWARD_HOST_PORTS={json.dumps(forward_host_ports)}"]
        )
        if runtime == "container":
            # Apple Container: native socket forwarding (no TCP gateway needed)
            socket_dir = _host_tmp / f"yolo-fwd-{cname}"
            socket_dir.mkdir(parents=True, exist_ok=True)
            for port_spec in forward_host_ports:
                port = str(port_spec).split(":")[0]
                host_sock = socket_dir / f"port-{port}.sock"
                run_cmd.extend(
                    [
                        "--publish-socket",
                        f"{host_sock}:/tmp/yolo-fwd/port-{port}.sock",
                    ]
                )
        elif IS_MACOS:
            # Tell the container entrypoint to use TCP forwarding via the
            # host gateway instead of Unix sockets.
            run_cmd.extend(["-e", "YOLO_FWD_HOST_GATEWAY=host.containers.internal"])
        else:
            socket_dir = _host_tmp / f"yolo-fwd-{cname}"
            run_cmd.extend(["-v", f"{socket_dir}:/tmp/yolo-fwd:rw"])

    # Host services: bind-mount the per-jail sockets directory into the jail
    # at /run/yolo-services/.  Each service (built-in cgroup delegate,
    # user-configured external services from `loopholes` in config) drops
    # its Unix socket here.  Apple Container can't share Unix sockets via
    # virtiofs, so we skip the mount entirely there — start_loopholes()
    # also returns no handles in that case.
    #
    # The sockets dir lives under /tmp (not ws_state) because Linux's
    # AF_UNIX path limit is 108 bytes and a deep workspace path blows it.
    # See _host_service_sockets_dir() docstring.
    host_services_sockets_dir = _host_service_sockets_dir(cname)
    if runtime != "container":
        host_services_sockets_dir.mkdir(parents=True, exist_ok=True)
        # _host_service_sockets_dir already resolves /tmp → /private/tmp on
        # macOS (Podman Machine's VM mounts /private but not the /tmp symlink,
        # so unresolved /tmp/... paths are invisible to the VM).
        run_cmd.extend(
            ["-v", f"{host_services_sockets_dir}:{JAIL_HOST_SERVICES_DIR}:rw"]
        )
        # Claude OAuth broker singleton — eagerly ensure it's alive
        # BEFORE we add the bind-mount flag so the socket source path
        # exists at the moment podman tries to set up the mount.
        # ``start_loopholes`` (called later) also calls _broker_ensure
        # for idempotence, but putting it here too means the mount
        # never fails for want of a source.
        try:
            _broker_ensure()
        except Exception as e:  # noqa: BLE001 — never fail run() on this
            console.print(
                f"[yellow]claude-oauth-broker: singleton not ensured pre-mount: {e}[/yellow]"
            )
        if BROKER_SINGLETON_SOCKET.exists():
            _broker_jail_socket = (
                f"{JAIL_HOST_SERVICES_DIR}/{BROKER_LOOPHOLE_NAME}.sock"
            )
            if IS_MACOS:
                # Podman Machine on macOS cannot bind-mount a Unix socket
                # *file* (EOPNOTSUPP).  Socket files that live *inside* a
                # mounted directory are accessible via the virtiofs dir
                # mount.  Start a relay inside host_services_sockets_dir;
                # no extra -v flag needed — the relay socket is already
                # visible at {JAIL_HOST_SERVICES_DIR}/{BROKER_LOOPHOLE_NAME}.sock
                # through the directory mount above.
                _relay_path = host_services_sockets_dir / f"{BROKER_LOOPHOLE_NAME}.sock"
                _start_broker_relay(_relay_path, BROKER_SINGLETON_SOCKET.resolve())
            else:
                run_cmd.extend(
                    [
                        "-v",
                        f"{BROKER_SINGLETON_SOCKET.resolve()}:{_broker_jail_socket}:rw",
                    ]
                )
            # The jail-side TLS terminator reads
            # YOLO_SERVICE_CLAUDE_OAUTH_BROKER_SOCKET to find the
            # broker.  start_loopholes no longer synthesizes this env
            # (singleton doesn't come back as a LoopholeDaemon handle)
            # so inject it explicitly.
            _broker_env_var = _host_service_env_var(BROKER_LOOPHOLE_NAME)
            run_cmd.extend(["-e", f"{_broker_env_var}={_broker_jail_socket}"])

    # Device passthrough from config
    # On macOS, device passthrough goes through the container runtime's VM.
    # Raw /dev paths and lsusb are Linux concepts — USB passthrough is not
    # supported on macOS.  Device cgroup rules are also Linux-only.
    for dev in config.get("devices", []):
        if isinstance(dev, str):
            # Raw device path: "/dev/bus/usb/001/004"
            if IS_MACOS:
                console.print(
                    f"[yellow]Warning: device passthrough ({dev}) not supported on macOS — skipping[/yellow]"
                )
                continue
            if not Path(dev).exists():
                console.print(
                    f"[yellow]Warning: device {dev} not found — skipping[/yellow]"
                )
                continue
            run_cmd.extend(["--device", dev])
        elif isinstance(dev, dict):
            if "usb" in dev:
                usb_id = dev["usb"]
                desc = dev.get("description", usb_id)
                if IS_MACOS:
                    console.print(
                        f"[yellow]Warning: USB device passthrough ({desc}) not supported on macOS — skipping[/yellow]"
                    )
                    continue
                # Resolve USB vendor:product ID to /dev/bus/usb path
                try:
                    lsusb_result = subprocess.run(
                        ["lsusb", "-d", usb_id],
                        capture_output=True,
                        text=True,
                        timeout=5,
                    )
                    if lsusb_result.returncode != 0 or not lsusb_result.stdout.strip():
                        console.print(
                            f"[yellow]Warning: USB device {desc} ({usb_id}) not found — skipping[/yellow]"
                        )
                        continue
                    # Parse: "Bus 001 Device 004: ID 0bda:2838 ..."
                    line = lsusb_result.stdout.strip().split("\n")[0]
                    parts = line.split()
                    bus = parts[1]  # "001"
                    device = parts[3].rstrip(":")  # "004"
                    dev_path = f"/dev/bus/usb/{bus}/{device}"
                    if not Path(dev_path).exists():
                        console.print(
                            f"[yellow]Warning: USB device {desc} found by lsusb but {dev_path} missing — skipping[/yellow]"
                        )
                        continue
                    run_cmd.extend(["--device", dev_path])
                    console.print(f"[dim]USB device: {desc} → {dev_path}[/dim]")
                except FileNotFoundError:
                    console.print(
                        "[yellow]Warning: lsusb not found — cannot resolve USB device IDs[/yellow]"
                    )
                except Exception as e:
                    console.print(
                        f"[yellow]Warning: USB device resolution failed for {usb_id}: {e}[/yellow]"
                    )
            elif "cgroup_rule" in dev:
                if IS_MACOS:
                    console.print(
                        "[yellow]Warning: device cgroup rules not supported on macOS — skipping[/yellow]"
                    )
                    continue
                run_cmd.extend(["--device-cgroup-rule", dev["cgroup_rule"]])

    # GPU passthrough from config (NVIDIA via podman+CDI).  Availability
    # already probed above — gpu_enabled reflects "requested AND present",
    # gpu_unavailable_reason carries the warning text when requested but
    # not present.
    if gpu_requested and not gpu_enabled:
        console.print(
            f"[yellow]Warning: GPU requested but {gpu_unavailable_reason} — "
            "starting without GPU passthrough[/yellow]"
        )
    if gpu_enabled:
        gpu_config = config.get("gpu", {})
        gpu_devices = gpu_config.get("devices", "all")
        gpu_capabilities = gpu_config.get("capabilities", "compute,utility")

        # Podman: use CDI (Container Device Interface) notation
        if gpu_devices == "all":
            run_cmd.extend(["--device", "nvidia.com/gpu=all"])
        else:
            # CDI supports individual GPU indices: nvidia.com/gpu=0
            for gpu_idx in gpu_devices.split(","):
                gpu_idx = gpu_idx.strip()
                run_cmd.extend(["--device", f"nvidia.com/gpu={gpu_idx}"])

        # Set NVIDIA environment variables for the container runtime to pick up
        run_cmd.extend(
            [
                "-e",
                f"NVIDIA_VISIBLE_DEVICES={gpu_devices}",
                "-e",
                f"NVIDIA_DRIVER_CAPABILITIES={gpu_capabilities}",
            ]
        )
        console.print(
            f"[dim]GPU passthrough: devices={gpu_devices}, capabilities={gpu_capabilities}[/dim]"
        )

    # KVM passthrough from config.  Opt-in via top-level `kvm: true`.
    # Not available on macOS (no /dev/kvm) or Apple Container (uses VZ
    # framework, no device passthrough).  When /dev/kvm is missing on a
    # Linux host we warn and skip — either virtualization extensions are
    # disabled in firmware, or the kvm module isn't loaded.
    if config.get("kvm") is True:
        if IS_MACOS or runtime == "container":
            console.print(
                "[yellow]Warning: kvm passthrough is not supported on this runtime — skipping[/yellow]"
            )
        elif not Path("/dev/kvm").exists():
            console.print(
                "[yellow]Warning: /dev/kvm not present on host — skipping kvm passthrough[/yellow]"
            )
        else:
            run_cmd.extend(["--device", "/dev/kvm"])
            # Rootless podman drops supplementary groups by default, so even
            # after --device the in-container process can't open /dev/kvm
            # unless we explicitly preserve the invoking user's kvm group.
            # `keep-groups` is a podman-specific convenience flag.
            if runtime == "podman":
                run_cmd.extend(["--group-add", "keep-groups"])
            console.print("[dim]KVM passthrough: /dev/kvm[/dim]")

    # Resource limits from config.
    # Apple Container needs explicit defaults (its built-in defaults are 4 CPU / 1GB RAM).
    # Podman inherits VM-level resources; only set limits when explicitly configured.
    resources_config = config.get("resources", {})
    res_parts = []
    memory = resources_config.get("memory")
    cpus = resources_config.get("cpus")

    if runtime == "container":
        # Apple Container: apply sane defaults since its built-ins are too low
        # for agent workloads. Default to half the host resources.
        if cpus is None:
            import multiprocessing

            host_cpus = multiprocessing.cpu_count()
            cpus = max(2, host_cpus // 2)
        if memory is None:
            try:
                if IS_MACOS:
                    sysctl_result = subprocess.run(
                        ["sysctl", "-n", "hw.memsize"],
                        capture_output=True,
                        text=True,
                        timeout=5,
                    )
                    host_mem_bytes = int(sysctl_result.stdout.strip())
                else:
                    host_mem_bytes = os.sysconf("SC_PAGE_SIZE") * os.sysconf(
                        "SC_PHYS_PAGES"
                    )
                # Default to half of host memory, minimum 4GB, formatted for Apple Container
                default_mem = max(4 * 1024**3, host_mem_bytes // 2)
                memory = f"{default_mem // (1024**3)}g"
            except Exception:
                memory = "8g"

    if memory:
        run_cmd.extend(["--memory", memory])
        res_parts.append(f"memory={memory}")
    if cpus is not None:
        run_cmd.extend(["--cpus", str(cpus)])
        res_parts.append(f"cpus={cpus}")
    pids_limit = resources_config.get("pids_limit")
    # Apple Container doesn't support --pids-limit (each container is a VM)
    if runtime != "container":
        # Podman defaults to 2048 pids which is too low for agent workloads.
        # Always set a sane default.
        effective_pids = pids_limit if pids_limit is not None else 32768
        run_cmd.extend(["--pids-limit", str(effective_pids)])
        res_parts.append(f"pids={effective_pids}")
    # Print version at startup for log capture
    _print_startup_banner(_get_yolo_version(), runtime, cname, res_parts or None)

    # Mount host nvim config read-only for entrypoint to copy into the writable
    # .config/ overlay.  We can't bind-mount directly because dotfile managers
    # (stow, etc.) create symlinks that break inside the container.
    host_nvim_config = Path.home() / ".config" / "nvim"
    if host_nvim_config.is_dir():
        run_cmd.extend(["-v", f"{host_nvim_config}:/ctx/host-nvim-config:ro"])

    # Shadow workspace .vscode/mcp.json so agents use only our jail MCP config
    vscode_mcp = workspace / ".vscode" / "mcp.json"
    if vscode_mcp.exists():
        run_cmd.extend(["-v", "/dev/null:/workspace/.vscode/mcp.json:ro"])

    # Shadow workspace .overmind.sock so host overmind doesn't leak into the jail
    overmind_sock = workspace / ".overmind.sock"
    if overmind_sock.exists():
        run_cmd.extend(["-v", "/dev/null:/workspace/.overmind.sock:ro"])

    # Overlay workspace sub-paths as read-only to protect host-executed code.
    # Mounted after the rw workspace volume so they shadow it for those paths.
    run_cmd.extend(_workspace_readonly_mount_args(workspace, config))

    # Mount user-level yolo config so nested jails see the same merged config.
    # Without this, ~/.config/ is an empty per-workspace overlay and the nested
    # yolo resolves to empty config, stomping the host's config snapshot.
    if USER_CONFIG_PATH.is_file():
        container_config = f"/home/agent/.config/yolo-jail/{USER_CONFIG_PATH.name}"
        if runtime == "container":
            _ac_materialize_under_ws_state(
                USER_CONFIG_PATH,
                f".config/yolo-jail/{USER_CONFIG_PATH.name}",
                ws_state,
            )
        else:
            run_cmd.extend(["-v", f"{USER_CONFIG_PATH}:{container_config}:ro"])

    # Pass the mise path through to any nested jail so the same host path
    # keeps resolving one level deeper. The path is identical inside and out.
    run_cmd.extend(["-e", f"YOLO_OUTER_MISE_PATH={host_mise}"])
    run_cmd.extend(["-e", f"MISE_DISABLE_TOOLS={mise_disabled_tools}"])

    # Mount merged skills directories read-only (prepared on host side).
    # Kernel-enforced :ro — agents get "Read-only file system" on write attempts.
    skills_path = _prepare_skills(cname)
    run_cmd.extend(
        ["-v", f"{skills_path / 'skills-copilot'}:/home/agent/.copilot/skills:ro"]
    )
    run_cmd.extend(
        ["-v", f"{skills_path / 'skills-gemini'}:/home/agent/.gemini/skills:ro"]
    )
    run_cmd.extend(
        ["-v", f"{skills_path / 'skills-claude'}:/home/agent/.claude/skills:ro"]
    )

    # Mount host ~/.claude/ files for syncing into the jail.
    # Auto-discover scripts referenced in host settings.json (fileSuggestion,
    # statusLine, hooks) and include them if they live under ~/.claude/.
    host_claude_dir = Path.home() / ".claude"
    effective_claude_files = list(host_claude_files)
    host_settings_file = host_claude_dir / "settings.json"
    if host_settings_file.exists():
        try:
            host_settings = json.loads(host_settings_file.read_text())
            # Collect all command paths referenced in settings
            script_cmds: List[str] = []
            for key in ("fileSuggestion", "statusLine"):
                cmd = (host_settings.get(key) or {}).get("command", "")
                if cmd:
                    script_cmds.append(cmd)
            # Walk hooks: {"EventName": [{"hooks": [{"command": "..."}]}]}
            for matchers in (host_settings.get("hooks") or {}).values():
                if not isinstance(matchers, list):
                    continue
                for matcher in matchers:
                    for hook in matcher.get("hooks") or []:
                        cmd = hook.get("command", "") if isinstance(hook, dict) else ""
                        if cmd:
                            script_cmds.append(cmd)
            # Add scripts that live under ~/.claude/
            for cmd in script_cmds:
                resolved = Path(cmd.replace("~", str(Path.home())))
                try:
                    resolved.relative_to(host_claude_dir)
                    fname = resolved.name
                    if fname not in effective_claude_files:
                        effective_claude_files.append(fname)
                except ValueError:
                    pass  # script lives outside ~/.claude/, must mount manually
        except (json.JSONDecodeError, OSError):
            pass
    mounted_claude_files = []
    for fname in effective_claude_files:
        host_file = host_claude_dir / fname
        if host_file.exists() and host_file.is_file():
            run_cmd.extend(["-v", f"{host_file}:/ctx/host-claude/{fname}:ro"])
            mounted_claude_files.append(fname)
    if mounted_claude_files:
        run_cmd.extend(
            ["-e", f"YOLO_HOST_CLAUDE_FILES={json.dumps(mounted_claude_files)}"]
        )

    # Generate per-workspace AGENTS.md / CLAUDE.md (separate for each agent to
    # respect user-level ~/.copilot/AGENTS.md, ~/.gemini/AGENTS.md, ~/.claude/CLAUDE.md)
    agents_path = generate_agents_md(
        cname,
        workspace,
        normalized_blocked,
        mount_descriptions,
        net_mode=net_mode,
        runtime=runtime,
        forward_host_ports=forward_host_ports or None,
        mcp_servers=mcp_servers or None,
        mcp_presets=mcp_presets or None,
    )
    if runtime == "container":
        _ac_materialize_under_ws_state(
            agents_path / "AGENTS-copilot.md", ".copilot/AGENTS.md", ws_state
        )
        _ac_materialize_under_ws_state(
            agents_path / "AGENTS-gemini.md", ".gemini/AGENTS.md", ws_state
        )
        _ac_materialize_under_ws_state(
            agents_path / "CLAUDE.md", ".claude/CLAUDE.md", ws_state
        )
    else:
        run_cmd.extend(
            [
                "-v",
                f"{agents_path / 'AGENTS-copilot.md'}:/home/agent/.copilot/AGENTS.md:ro",
            ]
        )
        run_cmd.extend(
            [
                "-v",
                f"{agents_path / 'AGENTS-gemini.md'}:/home/agent/.gemini/AGENTS.md:ro",
            ]
        )
        run_cmd.extend(
            ["-v", f"{agents_path / 'CLAUDE.md'}:/home/agent/.claude/CLAUDE.md:ro"]
        )

    if "TERM" in os.environ:
        run_cmd.extend(["-e", f"TERM={os.environ['TERM']}"])

    if profile:
        run_cmd.extend(["-e", "YOLO_PROFILE=1"])

    # Apply host-side loopholes: --add-host entries for DNS interception,
    # CA cert bind mounts, and NODE_EXTRA_CA_CERTS for tls-intercept
    # loopholes. Spawned + unix-socket loopholes (yolo-jail.jsonc
    # host_services entries) ride the existing start_loopholes
    # pipeline below; we only do jail-side integration here. See
    # src/loopholes.py for the full schema.
    run_cmd.extend(
        _loopholes.runtime_args_for(
            _loopholes.discover_loopholes(loopholes_config=config.get("loopholes")),
            runtime=runtime,
        )
    )

    run_cmd.append(_jail_image(runtime))
    run_cmd.append("yolo-entrypoint")

    # If mise.toml exists in workspace, trust it.
    # Then ensure all tools (global + local) are ready.
    # --quiet on mise trust suppresses "No untrusted config files found" warning.
    # mise upgrade stderr is filtered to hide deprecation noise (@system warnings).
    setup_script = (
        "YOLO_BYPASS_SHIMS=1 sh -c '"
        "(if [ -f mise.toml ]; then mise trust --quiet 2>/dev/null; fi) && "
        'echo "  ↳ mise install" >&2 && '
        "mise install --quiet && "
        'echo "  ↳ mise upgrade" >&2 && '
        'mise upgrade --yes 2>&1 | grep -v "^mise WARN" | sed "s/^/    /" >&2 && '
        'echo "  ↳ bootstrap" >&2 && '
        "~/.yolo-bootstrap.sh >&2 && "
        "~/.yolo-venv-precreate.sh >&2'"
    )
    # After setup, activate mise so tool paths (copilot, gemini, claude, etc.) are in PATH.
    # We use `mise env` (one-time activation) rather than `mise hook-env` (continuous
    # shell integration) because hook-env deadlocks when it needs to create a venv:
    # it holds a lock, spawns `uv` via the mise shim (which IS mise), and the shim
    # tries to acquire the same lock → deadlock.
    # Re-prepend yolo-shims after mise env so our wrappers (yolo, blocked tools)
    # take priority over mise-installed console_scripts in installs/python/.../bin/.
    mise_activate = (
        '. "$HOME/.config/yolo-user-env.sh" 2>/dev/null; '
        'eval "$(mise env -s bash)" 2>/dev/null; export PATH="$HOME/.yolo-shims:$PATH"'
    )

    # Human-readable command for status messages
    display_cmd = target_cmd.replace("'", "'\\''")

    # Use && for fail-fast: if provisioning fails, don't proceed with broken env
    if profile:
        # Wrap each phase with timing output for profiling
        final_internal_cmd = (
            "exec 3>&2; "  # save stderr
            "printf '\\033[2m📦 Provisioning tools...\\033[0m\\n' >&2; "
            f"_t0=$(date +%s%N); {setup_script}; "
            "_t1=$(date +%s%N); "
            f"{mise_activate}; "
            "_t2=$(date +%s%N); "
            f"printf '\\033[1;36m⚡ Executing: {display_cmd}\\033[0m\\n' >&2; "
            f"{target_cmd}; _rc=$?; "
            "_t3=$(date +%s%N); "
            # Print profile report to stderr
            "echo '' >&3; echo '=== YOLO Jail Profile ===' >&3; "
            "echo '' >&3; echo '--- Entrypoint (config generation) ---' >&3; "
            # Extract only the LAST run from the perf log (separated by === markers)
            'awk \'/^=== YOLO/{buf=""} {buf=buf $0 "\\n"} END{printf "%s", buf}\' ~/.yolo-perf.log >&3 2>/dev/null; '
            "echo '' >&3; echo '--- Container setup ---' >&3; "
            "printf '  mise install + bootstrap: %s\\n' \"$(( (_t1 - _t0) / 1000000 ))ms\" >&3; "
            "printf '  mise hook-env:            %s\\n' \"$(( (_t2 - _t1) / 1000000 ))ms\" >&3; "
            "printf '  command execution:        %s\\n' \"$(( (_t3 - _t2) / 1000000 ))ms\" >&3; "
            "printf '  total in-container:       %s\\n' \"$(( (_t3 - _t0) / 1000000 ))ms\" >&3; "
            "echo '' >&3; "
            # Also show mise shim vs direct node timing
            "echo '--- Node path comparison ---' >&3; "
            "_n0=$(date +%s%N); /bin/node --version >/dev/null 2>&1; _n1=$(date +%s%N); "
            "printf '  /bin/node:        %sms\\n' \"$(( (_n1 - _n0) / 1000000 ))\" >&3; "
            '_n2=$(date +%s%N); "$MISE_DATA_DIR/shims/node" --version >/dev/null 2>&1; _n3=$(date +%s%N); '
            "printf '  mise shim node:   %sms\\n' \"$(( (_n3 - _n2) / 1000000 ))\" >&3; "
            "echo '' >&3; "
            "exit $_rc"
        )
    else:
        # Provisioning message → bootstrap → activate → ready → command
        final_internal_cmd = (
            "printf '\\033[2m📦 Provisioning tools...\\033[0m\\n' >&2 && "
            f"{setup_script} && "
            f"{mise_activate}; "
            f"printf '\\033[1;36m⚡ Executing: {display_cmd}\\033[0m\\n' >&2; "
            f"{target_cmd}"
        )

    write_container_tracking(cname, workspace)
    _tmux_rename_window("JAIL")

    # Start host-side port forwarding BEFORE the container so socket files
    # exist when entrypoint.py starts the container-side socat.
    socat_procs: List[subprocess.Popen] = []
    if socket_dir:
        socat_procs = start_host_port_forwarding(forward_host_ports, cname, socket_dir)

    # Start all host-side services (built-in cgroup delegate + any user
    # `loopholes` from config) BEFORE the container so their sockets
    # exist when the entrypoint and agent code inside the jail try to
    # connect to them.  Env vars must be added BEFORE the image arg, so
    # do this before appending final_internal_cmd.
    host_services = start_loopholes(cname, runtime, config)
    for svc in host_services:
        # Insert each `-e VAR=val` pair just before the image arg.  We can't
        # extend at the end — the image and command args are already in
        # the run command at this point.
        image_idx = run_cmd.index(_jail_image(runtime))
        run_cmd[image_idx:image_idx] = [
            "-e",
            f"{svc.env_var_name}={svc.jail_socket_path}",
        ]

    run_cmd.append(final_internal_cmd)

    if os.environ.get("YOLO_DEBUG"):
        print(" ".join(shlex.quote(s) for s in run_cmd), file=sys.stderr)

    # Use Popen so we can release the workspace lock once the container is
    # confirmed running.  Any concurrent yolo process waiting on the lock will
    # re-check and find our container, then exec into it.
    try:
        proc = subprocess.Popen(run_cmd)
    except FileNotFoundError:
        console.print(
            f"[bold red]Configured runtime '{runtime}' not found on PATH.[/bold red]"
        )
        console.print(
            "[dim]Run `yolo check` to validate runtime availability before restarting.[/dim]"
        )
        cleanup_port_forwarding(socat_procs, socket_dir)
        stop_loopholes(host_services, host_services_sockets_dir)
        lock_file.close()
        sys.exit(1)
    for _ in range(20):
        if find_running_container(cname, runtime=runtime):
            break
        _time.sleep(0.25)
    lock_file.close()

    proc.wait()
    # Clean up host-side socat processes, host services (incl. cgroup
    # delegate), and their per-jail socket directory.
    cleanup_port_forwarding(socat_procs, socket_dir)
    stop_loopholes(host_services, host_services_sockets_dir)

    if profile and _profile_times:
        _profile_times["container_exited"] = _time.monotonic()
        start = _profile_times["start"]
        err = Console(stderr=True)
        err.print("\n[bold cyan]--- Host-side timing ---[/bold cyan]")
        err.print(
            f"  Image build/load:   {_profile_times.get('image_loaded', start) - start:.3f}s"
        )
        err.print(
            f"  Total (host-side):  {_profile_times['container_exited'] - start:.3f}s\n"
        )

    sys.exit(proc.returncode)




@app.command()
def ps():
    """List running YOLO jail containers."""
    runtime = _runtime()
    if runtime == "container":
        # Apple Container CLI does not support --filter; scan output instead.
        result = subprocess.run(
            ["container", "ls"],
            capture_output=True,
            text=True,
        )
        lines = []
        for line in result.stdout.strip().splitlines()[1:]:  # skip header
            parts = line.split()
            if parts and parts[0].startswith("yolo-"):
                cname = parts[0]
                status = " ".join(parts[1:]) if len(parts) > 1 else ""
                lines.append(f"{cname}\t{status}\t")
    else:
        result = subprocess.run(
            [
                runtime,
                "ps",
                "--filter",
                "name=^yolo-",
                "--format",
                "{{.Names}}\t{{.Status}}\t{{.RunningFor}}",
            ],
            capture_output=True,
            text=True,
        )
        lines = result.stdout.strip().splitlines() if result.stdout.strip() else []

    if not lines:
        typer.echo("No running jails.")
        # Clean up all stale tracking files
        if CONTAINER_DIR.exists():
            for tracking_file in CONTAINER_DIR.iterdir():
                cleanup_container_tracking(tracking_file.name)
        return

    # Parse container info and resolve workspaces
    containers = []
    for line in lines:
        parts = line.split("\t")
        if len(parts) >= 3:
            name, status, running_for = parts[0], parts[1], parts[2]
            workspace = _get_container_workspace(name, runtime)
            containers.append((name, status, running_for, workspace))

    # Clean up stale tracking files
    running_names = {c[0] for c in containers}
    if CONTAINER_DIR.exists():
        for tracking_file in CONTAINER_DIR.iterdir():
            if tracking_file.name not in running_names:
                cleanup_container_tracking(tracking_file.name)

    # Display as a table
    if containers:
        w_name = max(len(c[0]) for c in containers)
        w_status = max(len(c[1]) for c in containers)
        header = f"{'CONTAINER':<{w_name}}  {'STATUS':<{w_status}}  WORKSPACE"
        typer.echo(header)
        for name, status, _, workspace in containers:
            typer.echo(f"{name:<{w_name}}  {status:<{w_status}}  {workspace}")

    # Warn about orphaned/stuck jails
    problems = []
    for name, *_, workspace in containers:
        if workspace != "unknown" and not Path(workspace).is_dir():
            problems.append((name, "workspace gone"))
        else:
            reason = _check_container_stuck(name, runtime)
            if reason:
                problems.append((name, reason))
    if problems:
        typer.echo(f"\n⚠  {len(problems)} problem jail(s):")
        for name, reason in problems:
            typer.echo(f"  {name}  ({reason})")
        typer.echo("\n  Run 'yolo doctor' to clean up")


@app.command()
def doctor(
    build: bool = typer.Option(
        True,
        "--build/--no-build",
        help="Run nix build as part of the preflight (default: on)",
    ),
):
    """Alias for 'check'. Validate environment, config, and build."""
    check(build=build)


@app.command("prune")
def prune_cmd(
    apply: bool = typer.Option(
        False,
        "--apply",
        help="Actually reclaim space.  Without this flag, prune prints what "
        "it WOULD do and exits (safe default).",
    ),
    no_hardlink: bool = typer.Option(
        False,
        "--no-hardlink",
        help="Skip the cross-workspace hardlink dedup pass.",
    ),
    dedup_global: bool = typer.Option(
        False,
        "--dedup-global",
        help="Also hardlink-dedupe inside the shared global cache/mise/home "
        "subtrees.  Opt-in because these can be hundreds of GiB and the "
        "scan takes real time — but that's where the duplicate wheels "
        "live.",
    ),
    no_containers: bool = typer.Option(
        False,
        "--no-containers",
        help="Skip the stopped-container cleanup.",
    ),
    no_images: bool = typer.Option(
        False,
        "--no-images",
        help="Skip the old-image cleanup.",
    ),
    keep_images: int = typer.Option(
        2,
        "--keep-images",
        help="Number of most-recent yolo-jail images to retain (default: 2).",
    ),
    no_image_cache: bool = typer.Option(
        False,
        "--no-image-cache",
        help="Skip the ~/.cache/images/ tarball cleanup.",
    ),
    no_shadowed_home: bool = typer.Option(
        False,
        "--no-shadowed-home",
        help="Skip the shadowed-seed cleanup.  By default, prune deletes "
        "subdirs of the :ro GLOBAL_HOME seed that are fully masked by "
        "overlay mounts at runtime (.cache, .npm, .npm-global, .local, "
        "go).  These can never be read by any live jail but accumulate "
        "tens of GiB from pre-cache-split installs.",
    ),
    image_cache_keep: int = typer.Option(
        3,
        "--image-cache-keep",
        help="Number of most-recent cached image tarballs to retain "
        "under ~/.cache/images/ (default: 3).  Each tar is ~3 GiB, so "
        "this bucket dominates disk use — it's the single biggest win "
        "for most users.  Orphan .tmp files from crashed builds are "
        "always swept regardless of this count.",
    ),
    cache_age: int = typer.Option(
        30,
        "--cache-age",
        help="Purge files under re-downloadable ~/.cache/ subdirs "
        "(uv, pip, npm, go-build, mise, pex, pants, node-gyp, gopls) "
        "older than this many days.  Pass 0 to skip the pass entirely; "
        "pass a smaller number to be more aggressive.  Content is "
        "re-downloadable from PyPI/npm/go/mise on next install.",
    ),
    purge_heavy_caches: bool = typer.Option(
        False,
        "--purge-heavy-caches",
        help="With --cache-age, also purge playwright browsers + huggingface "
        "models older than the cutoff.  Re-download cost is significant "
        "(~400 MiB per browser, multi-GiB per HF model) — opt-in.",
    ),
):
    """Reclaim disk space: hardlink-dedup, drop stale containers + old images.

    Defaults to dry-run — nothing on disk changes unless you pass --apply.
    Only touches yolo-* containers, yolo-jail images, and files under
    ``<workspace>/.yolo/home/{npm-global,local,go}``.  Browser profile
    dirs in the cache (chromium/firefox families) are NEVER touched by
    the age-based purge — those carry live user state.
    """
    from src import prune as _prune

    runtime = _detect_runtime()
    workspaces = _prune._find_yolo_workspaces(runtime)

    mode = "APPLY" if apply else "DRY-RUN"
    console.print(f"[bold]yolo prune ({mode})[/bold]")
    console.print(f"Runtime: {runtime}  Workspaces tracked: {len(workspaces)}")
    for ws in workspaces:
        console.print(f"  • {ws}")
    if not workspaces:
        console.print(
            "[dim]No yolo-* containers found — nothing to dedupe across.[/dim]"
        )

    # --- Pre-report ---
    before = _prune._disk_usage_report(
        workspaces=workspaces, global_storage=GLOBAL_STORAGE
    )
    console.print(
        f"\n[bold]Current usage[/bold]  total={_fmt_bytes(before['total'])}  "
        f"(workspaces={_fmt_bytes(before['workspaces'])}, "
        f"global={_fmt_bytes(before['global_storage'])})"
    )
    breakdown = before.get("breakdown") or {}
    if breakdown:
        console.print("  [dim]global-storage breakdown (largest first):[/dim]")
        for name, size in sorted(breakdown.items(), key=lambda kv: kv[1], reverse=True):
            console.print(f"    {name:<20} {_fmt_bytes(size):>12}")

    # When the cache bucket dominates, break it down too — saves the
    # operator from running `du -sh` manually to find the fat subdir.
    cache_breakdown = before.get("cache_breakdown") or {}
    if cache_breakdown:
        top = sorted(cache_breakdown.items(), key=lambda kv: kv[1], reverse=True)[:5]
        console.print("  [dim]cache/ top 5 (largest first):[/dim]")
        for name, size in top:
            console.print(f"    cache/{name:<14} {_fmt_bytes(size):>12}")

    # Hint: the image tar cache is almost always the biggest offender
    # and an ideal candidate for cold storage (HDD).  Surface it once,
    # proactively, when it exceeds a reasonable SSD budget.
    images_bytes = cache_breakdown.get("images", 0)
    if images_bytes >= 20 * (1024**3):  # 20 GiB
        console.print(
            f"  [yellow]hint:[/yellow] cache/images holds "
            f"{_fmt_bytes(images_bytes)} of jail tarballs.  They're "
            "streamed once at podman load then unused — consider "
            "symlinking this subdir to HDD storage if you have it."
        )

    total_saved = 0
    total_links = 0
    removed_containers: list[str] = []
    removed_images: list[str] = []
    image_cache_bytes = 0
    image_cache_files = 0

    if not no_hardlink and (workspaces or dedup_global):
        console.print("\n[bold]Hardlink dedup[/bold]")
        from rich.progress import (
            BarColumn,
            MofNCompleteColumn,
            Progress,
            SpinnerColumn,
            TextColumn,
            TimeElapsedColumn,
            TimeRemainingColumn,
        )

        entries: list = []
        # Walk phase: unknown total, show indeterminate spinner.
        with Progress(
            SpinnerColumn(),
            TextColumn("[bold]{task.description}[/bold]"),
            TextColumn("[dim]{task.completed:,} files scanned[/dim]"),
            TimeElapsedColumn(),
            console=console,
            transient=True,
        ) as prog:
            task = prog.add_task("scanning", total=None)
            if workspaces:
                for e in _prune._walk_dedupable_files(workspaces):
                    entries.append(e)
                    prog.advance(task)
            if dedup_global:
                for e in _prune._walk_global_dedupable(GLOBAL_STORAGE):
                    entries.append(e)
                    prog.advance(task)
        console.print(f"  candidate files: {len(entries):,}")
        if dedup_global:
            console.print("  [dim]scope: workspaces + global cache/mise/home[/dim]")
        else:
            console.print(
                "  [dim]scope: workspaces only  (pass --dedup-global to include "
                "the shared caches)[/dim]"
            )
        # Dedup phase: we don't know how many links we'll make until
        # we've hashed, so the bar tracks decisions-made as they land.
        # Total is unknown → spinner-like bar, but with a counter.
        with Progress(
            SpinnerColumn(),
            TextColumn("[bold]{task.description}[/bold]"),
            BarColumn(),
            MofNCompleteColumn(),
            TimeElapsedColumn(),
            TimeRemainingColumn(),
            console=console,
            transient=True,
        ) as prog:
            task = prog.add_task("deduping", total=None)

            def cb(advance: int = 1):
                prog.advance(task, advance)

            saved, links = _prune._hardlink_duplicate_files(
                entries, apply=apply, progress_cb=cb
            )
        verb = "would save" if not apply else "saved"
        console.print(f"  {verb}: {_fmt_bytes(saved)} across {links:,} hardlinks")
        total_saved += saved
        total_links += links

    if not no_containers:
        console.print("\n[bold]Stopped yolo-* containers[/bold]")
        removed_containers = _prune._prune_stopped_containers(runtime, apply=apply)
        verb = "would remove" if not apply else "removed"
        if removed_containers:
            console.print(f"  {verb}: {len(removed_containers)}")
            for name in removed_containers:
                console.print(f"    • {name}")
        else:
            console.print("  [dim]none[/dim]")

    if not no_images:
        console.print(f"\n[bold]Old yolo-jail images[/bold]  (keep={keep_images})")
        removed_images = _prune._prune_old_images(
            runtime, keep=keep_images, apply=apply
        )
        verb = "would remove" if not apply else "removed"
        if removed_images:
            console.print(f"  {verb}: {len(removed_images)}")
            for img in removed_images:
                console.print(f"    • {img}")
        else:
            console.print("  [dim]none[/dim]")

    if not no_image_cache:
        console.print(
            f"\n[bold]Cached image tarballs[/bold]  (keep={image_cache_keep})"
        )
        image_cache_bytes, image_cache_files = _prune._prune_image_cache(
            GLOBAL_CACHE / "images",
            keep=image_cache_keep,
            apply=apply,
        )
        verb = "would remove" if not apply else "removed"
        if image_cache_files:
            console.print(
                f"  {verb}: {_fmt_bytes(image_cache_bytes)} across "
                f"{image_cache_files:,} file(s)"
            )
        else:
            console.print("  [dim]none[/dim]")
        total_saved += image_cache_bytes

    shadowed_bytes = 0
    shadowed_items = 0
    if not no_shadowed_home:
        console.print("\n[bold]Shadowed seed subtrees[/bold]")
        console.print(
            f"  [dim]targets: {', '.join(_prune.SHADOWED_HOME_PATHS)} "
            "(each overlay-masked at runtime)[/dim]"
        )
        shadowed_bytes, shadowed_items = _prune._prune_shadowed_home(
            GLOBAL_HOME, apply=apply
        )
        verb = "would remove" if not apply else "removed"
        if shadowed_items:
            console.print(
                f"  {verb}: {_fmt_bytes(shadowed_bytes)} across "
                f"{shadowed_items:,} path(s)"
            )
        else:
            console.print("  [dim]none[/dim]")
        total_saved += shadowed_bytes

    cache_bytes = 0
    cache_files = 0
    if cache_age > 0:
        subdirs: List[str] = list(_prune.CACHE_PURGE_DEFAULT_SUBDIRS)
        if purge_heavy_caches:
            subdirs.extend(_prune.CACHE_PURGE_HEAVY_SUBDIRS)
        console.print(
            f"\n[bold]Cache purge[/bold]  (subdirs={','.join(subdirs)}, "
            f"age > {cache_age}d)"
        )
        # cache lives at GLOBAL_STORAGE/cache
        cache_bytes, cache_files = _prune._purge_cache_by_age(
            GLOBAL_STORAGE / "cache",
            subdirs=subdirs,
            older_than_days=cache_age,
            apply=apply,
        )
        verb = "would remove" if not apply else "removed"
        console.print(
            f"  {verb}: {_fmt_bytes(cache_bytes)} across {cache_files:,} files"
        )
        total_saved += cache_bytes

    console.print()
    if apply:
        console.print(
            f"[bold green]Reclaimed {_fmt_bytes(total_saved)}[/bold green] via "
            f"{total_links:,} hardlinks, {len(removed_containers)} container(s), "
            f"{len(removed_images)} image(s), {image_cache_files:,} image tar(s), "
            f"{shadowed_items:,} shadowed seed path(s), "
            f"{cache_files:,} cache file(s)."
        )
    else:
        console.print(
            f"[bold yellow]DRY-RUN:[/bold yellow] would reclaim "
            f"{_fmt_bytes(total_saved)} via {total_links:,} hardlinks, remove "
            f"{len(removed_containers)} container(s), "
            f"{len(removed_images)} image(s), "
            f"{image_cache_files:,} image tar(s), "
            f"{shadowed_items:,} shadowed seed path(s), "
            f"{cache_files:,} cache file(s).  "
            f"Re-run with [cyan]--apply[/cyan] to execute."
        )


def _fmt_bytes(n: int) -> str:
    """Human-readable byte count: 1536 → '1.5 KiB', 1_500_000_000 → '1.4 GiB'."""
    units = ("B", "KiB", "MiB", "GiB", "TiB")
    size = float(n)
    i = 0
    while size >= 1024 and i < len(units) - 1:
        size /= 1024
        i += 1
    if i == 0:
        return f"{int(size)} {units[i]}"
    return f"{size:.1f} {units[i]}"




# ---------------------------------------------------------------------------
# yolo loopholes — list / status / enable / disable.
# Subcommand definitions live in cli/loopholes_cmd.py.
# ---------------------------------------------------------------------------

from .loopholes_cmd import loopholes_app  # noqa: E402

app.add_typer(loopholes_app, name="loopholes")


# ---------------------------------------------------------------------------
# yolo broker — manage the singleton Claude OAuth broker.
# Subcommand definitions live in cli/broker_cmd.py.
# ---------------------------------------------------------------------------

from .broker_cmd import broker_app  # noqa: E402

app.add_typer(broker_app, name="broker")


def main():
    """Entry point for the `yolo` console script.

    Handles visual jail indicator (kitty tab or tmux pane border) and routes to
    the typer CLI.  Detection priority: kitty-native > tmux > neither.
    YOLO_NO_TMUX=1 skips all tmux interactions (useful in kitty-only setups).
    """
    import atexit

    # The jail-side shim chdirs into the repo root so ``python -m
    # src.cli`` can find the ``src`` package (uv run doesn't honor
    # PYTHONPATH).  Chdir back to the real invocation CWD here so
    # everything downstream — Path.cwd() for workspace resolution,
    # yolo-jail.jsonc lookup, etc. — sees the user's actual directory.
    _invocation_cwd = os.environ.pop("YOLO_INVOCATION_CWD", None)
    if _invocation_cwd:
        try:
            os.chdir(_invocation_cwd)
        except OSError:
            pass

    # Rewrite argv so `yolo -- echo foo` routes to `yolo run -- echo foo`.
    # Typer groups resolve the first positional arg as a subcommand name, so
    # extra args after `--` that aren't subcommands would fail with "No such
    # command".  We detect this and insert `run` before `--`.
    _SUBCOMMANDS = {
        "init",
        "init-user-config",
        "config-ref",
        "check",
        "run",
        "ps",
        "doctor",
        "loopholes",
    }
    args = sys.argv[1:]
    if args and "--" in args:
        pre_dash = args[: args.index("--")]
        # If nothing before `--` looks like a subcommand, insert `run`
        if not any(a in _SUBCOMMANDS for a in pre_dash):
            idx = sys.argv.index("--")
            sys.argv.insert(idx, "run")

    # Kitty-native mode takes priority over tmux
    if os.environ.get("KITTY_PID") and not os.environ.get("TMUX"):
        restore = _kitty_setup_jail_tab()
    else:
        restore = _tmux_setup_jail_pane()
    if restore:
        atexit.register(restore)

    app()


if __name__ == "__main__":
    main()
