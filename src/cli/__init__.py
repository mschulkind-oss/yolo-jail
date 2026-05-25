# ruff: noqa: F401, E402
# cli/__init__.py is the cli package's public face: it builds the Typer
# ``app``, defines the top-level callback, and re-imports the symbols
# every test and entry point expects on ``cli.X`` after the modules
# were split out.  Those cross-module re-imports look unused to linters,
# so the per-file directive above silences F401 (re-exports) and E402
# (module-level imports below the wired-up Typer app).

import os
import shutil
import subprocess
import sys
import time
from pathlib import Path

import typer

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
from .init_cmd import (  # noqa: E402
    _print_init_briefing,
    init,
    init_user_config,
)
from .config_ref_cmd import config_ref  # noqa: E402
from .prune_cmd import _fmt_bytes, prune_cmd  # noqa: E402
from .check_cmd import (  # noqa: E402
    _check_broker_creds_freshness,
    _check_disk_usage,
    _check_host_service_liveness,
    _check_loopholes,
    _finalize_problem,
    _loophole_exec_checks_skipped_in_jail,
    _split_self_check_problems,
    check,
)
from .run_cmd import (  # noqa: E402
    _entrypoint_preflight,
    _inject_agent_yolo_flags,
    _resolve_lsp_installs,
    _resolve_repo_root,
    _scratch_mount_args,
    _workspace_readonly_mount_args,
    doctor,
    ps,
    run,
)
from .tty_proxy import run_with_proxy  # noqa: E402, F401

# Register the extracted commands on the top-level Typer app.
app.command()(init)
app.command("init-user-config")(init_user_config)
app.command("config-ref")(config_ref)
app.command("prune")(prune_cmd)
app.command()(check)
app.command(
    context_settings={"allow_extra_args": True, "ignore_unknown_options": True}
)(run)
app.command()(ps)
app.command()(doctor)


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
