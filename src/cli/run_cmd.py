"""``yolo`` / ``yolo run`` / ``yolo ps`` / ``yolo doctor`` — the
container-startup command and its closest siblings.

This is the heaviest module in the cli package: ``run`` builds the full
podman / Apple-Container argv (mounts, env, network, devices, GPU, kvm,
loopholes), prestarts the host-side service plumbing, and either execs
into an existing container or launches a fresh one.

Co-resident helpers:
  * _resolve_repo_root — locate the yolo-jail repo root for nix builds.
  * _workspace_is_yolo_source_tree — detect a jailed yolo-jail checkout
    (its live tree then backs /opt/yolo-jail instead of repo_root).
  * _workspace_readonly_mount_args — read-only overlays on /workspace.
  * _entrypoint_preflight — generate jail-managed config in a temp home
    so check() catches config/render errors before any container starts.
  * _inject_agent_yolo_flags — auto-inject --yolo / --dangerously-skip-permissions
    on the leading binary (gemini / copilot / claude).
  * ps — list yolo-* containers.
  * doctor — alias for check.

The Typer commands are registered in cli/__init__.py.
"""

import fcntl
import json
import os
import re
import resource
import shlex
import shutil
import subprocess
import sys
import tempfile
import tomllib
from pathlib import Path
from typing import Any, Dict, List, Optional

import pyjson5
import typer
from rich.console import Console

from src import loopholes as _loopholes

# Agent registry (baked into the jail image, stdlib-only).  Dual-try import
# so it resolves under both the ``cli`` and ``src.cli`` test identities and
# inside a jail (top-level ``entrypoint``).
try:
    from src.entrypoint.agent_registry import AGENTS, YOLO_FLAG_ALIASES
except ImportError:  # pragma: no cover - exercised under the ``entrypoint`` identity
    from entrypoint.agent_registry import AGENTS, YOLO_FLAG_ALIASES

from .agents_md import (
    _prepare_skills,
    _workspace_is_yolo_source_tree,
    generate_agents_md,
)
from .config import (
    ConfigError,
    DEFAULT_HOST_CLAUDE_FILES,
    _check_config_changes,
    _check_preset_null_conflicts,
    _effective_packages,
    _load_jsonc_file,
    _merge_mise_disabled_tools,
    _merge_mise_tools,
    _normalize_blocked_tools,
    _resolve_env_sources,
    _validate_config,
    load_config,
    selected_agents,
)
from .console import console
from .image import _jail_image, auto_load_image
from .loopholes_runtime import (
    BROKER_LOOPHOLE_NAME,
    BROKER_SINGLETON_SOCKET,
    _broker_ensure,
    _gpu_host_available,
    _host_service_env_var,
    _host_service_sockets_dir,
    _relay_ensure,
    _relay_reap_orphans,
    _rocm_host_available,
    _should_mount_host_nix,
    start_loopholes,
    stop_loopholes,
)
from .network import (
    cleanup_port_forwarding,
    start_host_port_forwarding,
)

# Several of these path constants aren't *referenced* by run() directly,
# but tests `monkeypatch.setattr("cli.run_cmd.GLOBAL_MISE", ...)` to
# redirect filesystem layout — the patch fails ATTRIBUTE-not-found if
# the name isn't already bound on the module.  Keep them all imported.
from .paths import (  # noqa: F401
    AGENTS_DIR,
    BUILD_DIR,
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
    SUPPORTED_RUNTIMES,
    USER_CONFIG_PATH,
)
from .runtime import (
    PODMAN_MACHINE_MEMORY_FLOOR_MB,
    _check_container_stuck,
    _get_container_workspace,
    _podman_machine_memory,
    _podman_machine_resize_hint,
    _remove_stale_container,
    _runtime,
    cleanup_container_tracking,
    container_name_for_workspace,
    find_existing_container,
    find_running_container,
    write_container_tracking,
)
from .storage import (
    _ac_materialize_under_ws_state,
    _detect_host_timezone,
    _jail_mise_store_dir,
    _linux_multilib,
    _migrate_old_overlay,
    _seed_agent_dir,
    _sync_claude_json_seed,
    ensure_global_storage,
)
from .terminal import _print_startup_banner, _tmux_rename_window
from .tty_proxy import run_with_proxy
from .version import (
    _container_baked_yolo_version,
    _get_yolo_version,
    _git_describe_version,
)

# Named volume backing the jail-land mise store on macOS (podman and
# Apple Container), mounted at /mise.  Versioned name: the pre-split
# volume (yolo-mise-data) was mounted at the host's ~/.local/share/mise
# path string, so path-embedding installs in it (pipx-backend venv
# shebangs, pyvenv.cfg homes) would break if the same content were
# remounted at /mise — v2 starts a fresh store cold instead (the
# designed migration; see docs/design/jail-state-separation-design.md).
MISE_STORE_VOLUME = "yolo-mise-data-v2"

# _release_lock_when_started polls `find_running_container` until the
# freshly launched container is visible, then releases the workspace
# lock so concurrent `yolo` invocations exec into it instead of racing
# a second launch.  Worst case ATTEMPTS x INTERVAL = 5s before giving
# up and releasing anyway.  Hoisted to module level so tests can shrink
# them explicitly; production defaults must stay 20 x 0.25s.
LOCK_RELEASE_POLL_ATTEMPTS = 20
LOCK_RELEASE_POLL_INTERVAL_SECONDS = 0.25

# How long `podman stop` may run during host-side teardown (window close
# / SIGTERM) before it SIGKILLs the container.  Bounded so a wedged jail
# can't hang the terminal on the way out.
TEARDOWN_STOP_TIMEOUT_SECONDS = 5

# Per-jail owner-PID files: the PID of the `yolo run` that STARTED the
# jail.  Window close and `kill` tear the jail down synchronously (the
# proxy's SIGHUP/SIGTERM handler), but SIGKILL is uncatchable and a host
# crash runs no handler — so a jail can outlive its owner.  A running jail
# whose recorded owner PID is dead was orphaned that way; the next `yolo`
# reaps it.  See _reap_orphaned_jails.
OWNER_PID_DIR = GLOBAL_STORAGE / "owners"


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
    # Resolve relative to the cli package's __init__.py rather than this
    # module — tests monkeypatch ``cli.__file__`` to redirect resolution
    # at known fake-package layouts, and the path math (parent.parent.parent)
    # is calibrated for src/cli/__init__.py.
    import sys as _sys

    _cli_pkg = _sys.modules[__name__.rpartition(".")[0]]
    _cli_init_file = _cli_pkg.__file__

    # 1. Env var (used inside jails, CI, etc.)
    # Validate the path actually contains source — in nested jails the bind
    # mount at /opt/yolo-jail may be empty (doesn't propagate from parent).
    env_val = os.environ.get("YOLO_REPO_ROOT")
    if env_val:
        p = Path(env_val)
        if (p / "flake.nix").exists() or (
            p / "src" / "entrypoint" / "__init__.py"
        ).exists():
            return p.resolve()

    # 2. Running from source checkout (dev mode)
    # __file__ is src/cli/__init__.py — repo root is two parents up.
    source_root = Path(_cli_init_file).parent.parent.parent
    if (source_root / "flake.nix").exists():
        return source_root.resolve()

    # 3. Installed package — flake.nix bundled as package data in src/
    # (so its parent dir, not the cli package, is what we check).
    pkg_dir = Path(_cli_init_file).parent.parent
    if (pkg_dir / "flake.nix").exists():
        build_root = GLOBAL_STORAGE / "nix-build-root"
        # Ensure the *parent* exists for mkdtemp + the rename swap below,
        # but do NOT pre-create build_root itself: an empty placeholder
        # would be renamed aside on the first populate (leaking an empty
        # nix-build-root.old.<uuid>) instead of taking the no-op
        # first-populate path.
        GLOBAL_STORAGE.mkdir(parents=True, exist_ok=True)

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

        # Repopulate.  Build the new tree in a temp dir, then swap it in
        # with two inode-preserving renames.
        #
        # CRITICAL: do NOT rmtree the old build_root here.  podman resolves
        # a -v bind to the source *inode* at `podman run` time, and the
        # repo is bound read-only into the jail at /opt/yolo-jail
        # (f"{repo_root}:/opt/yolo-jail:ro" below).  A jail that launched
        # from the previous copy may still hold that inode mounted; deleting
        # it out from under the live mount leaves the kernel serving a
        # //deleted inode — /opt/yolo-jail reads empty and in-jail `yolo`
        # dies with `No module named 'src'` until the jail restarts.
        #
        # So instead of `rename(build_root -> .old); rmtree(.old)`, rename
        # the old tree ASIDE to a UNIQUE `nix-build-root.old.<uuid>` name
        # (uuid so two concurrent repopulates never collide on one fixed
        # `.old` target — a rename onto a non-empty `.old` would raise
        # ENOTEMPTY and abort one of the launches) and leave it on disk.
        # The liveness-gated `_prune_orphan_build_roots` sweeper reclaims
        # these once no running jail still binds them (`yolo prune`).
        import shutil
        import tempfile
        import time
        import uuid

        tmp_root = Path(tempfile.mkdtemp(dir=GLOBAL_STORAGE, prefix="nix-build-tmp-"))
        try:
            for fname in ("flake.nix", "flake.lock"):
                shutil.copy2(pkg_dir / fname, tmp_root / fname)
            shutil.copytree(pkg_dir, tmp_root / "src")
            aside = build_root.with_name(build_root.name + ".old." + uuid.uuid4().hex)
            try:
                build_root.rename(aside)
            except FileNotFoundError:
                aside = None  # nothing to move aside (first populate)
            tmp_root.rename(build_root)
            # Stamp the aside dir's mtime to *now*: rename doesn't touch a
            # directory's mtime, but the prune sweeper uses mtime as the
            # age grace floor that protects a jail still mid-startup (path
            # resolved, podman bind not yet established).  Best-effort.
            if aside is not None:
                try:
                    now = time.time()
                    os.utime(aside, (now, now))
                except OSError:
                    pass
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
    workspace: Path, config: Dict[str, Any], runtime: str = "podman"
) -> List[str]:
    """Build the ``-v …:ro`` arguments for ``config.workspace_readonly``.

    Each configured sub-path is overlaid onto the writable ``/workspace``
    mount so agents can't modify host-executed source.  When any entry is
    active we also lock ``yolo-jail.jsonc`` itself — otherwise an agent
    could rewrite the config and escape on the next run.

    Entries that escape the workspace or don't exist are skipped with a
    warning rather than failing the run.

    On Apple Container the ``:ro`` suffix is silently ignored
    (apple/container#889), so this protection does NOT hold — unlike the
    ``mounts`` case we can't skip it (the paths are inside the writable
    ``/workspace`` and would still be present), so we WARN loudly that the
    read-only guarantee isn't enforced and point at podman.
    """
    readonly_entries = config.get("workspace_readonly", []) or []
    if not readonly_entries:
        return []

    if runtime == "container":
        console.print(
            "[bold yellow]Warning: workspace_readonly is NOT enforced on Apple "
            "Container[/bold yellow] — it ignores read-only bind mounts "
            "(apple/container#889), so these paths stay writable inside the "
            "jail. Use `YOLO_RUNTIME=podman` to actually protect host-executed "
            f"source: {', '.join(readonly_entries)}"
        )

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


def _mise_config_venv_path(workspace: Path) -> Optional[str]:
    """The ``env._.python.venv`` path from the workspace's mise config.

    mise loads the base pair (``mise.toml``/``.mise.toml``) and — because
    every jail exports ``MISE_ENV=jail`` — the jail pair
    (``mise.jail.toml``/``.mise.jail.toml``) on top.  On a key conflict a
    later file wins (env-specific after base, dotted after plain), so
    parse in that order and keep the last hit.  String form is the path
    itself; dict form carries it in ``path`` (default ``.venv``).  A
    leading ``{{config_root}}/`` tera template resolves to the workspace
    root (== ``/workspace`` in-jail) and is stripped; any other template
    is left verbatim for the caller to reject.  Parse/decode errors read
    as absent — in-jail provisioning surfaces them.
    """
    found: Optional[str] = None
    for fname in ("mise.toml", ".mise.toml", "mise.jail.toml", ".mise.jail.toml"):
        cfg = workspace / fname
        if not cfg.is_file():
            continue
        try:
            data = tomllib.loads(cfg.read_text())
        except (OSError, UnicodeDecodeError, tomllib.TOMLDecodeError):
            continue
        node: Any = data
        for key in ("env", "_", "python", "venv"):
            node = node.get(key) if isinstance(node, dict) else None
        if isinstance(node, str):
            found = node
        elif isinstance(node, dict):
            path = node.get("path", ".venv")
            if isinstance(path, str):
                found = path
    if found:
        found = re.sub(r"^\{\{\s*config_root\s*\}\}/", "", found)
    return found


def _valid_per_side_rel(rel: str) -> bool:
    """True when ``rel`` is a shadowable workspace sub-path: relative,
    no ``..`` traversal, and no unresolved tera template (a literal
    ``{{…}}`` dir must never be materialized in the host workspace)."""
    return (
        bool(rel)
        and rel != "."
        and not rel.startswith("/")
        and ".." not in rel.split("/")
        and "{{" not in rel
        and "{%" not in rel
    )


def _venv_shadow_mount_args(
    workspace: Path, ws_state: Path, config: Dict[str, Any]
) -> List[str]:
    """Build the per-side shadow mounts over ``/workspace``.

    Derived state (venvs and friends) never crosses the host↔jail
    boundary: each side sees its own backing at the same idiomatic path.
    Shadow set: ``.venv`` ∪ the venv path from the workspace mise config
    ∪ config ``per_side_paths``.  Backing dirs live under
    ``ws_state/venv-shadows/`` (``/`` → ``__`` in the name) so they
    persist across restarts and ride the existing ``.yolo`` disk
    accounting.  Entries must be relative, template-free workspace
    sub-paths; offenders are skipped with a warning (config validation
    rejects malformed ``per_side_paths`` separately).  A host path that
    exists as a file or symlink (pipenv's ``.venv`` path file, a venv
    symlinked out of tree, …) is also skipped: a directory mount over a
    non-directory aborts container creation, so the jail sees the host's
    entry through the workspace bind instead — the pre-split behavior.
    Directory mounts only — the safe kind for Apple Container
    (apple/container#1089 is file mounts).
    """
    rels = {".venv"}
    mise_venv = _mise_config_venv_path(workspace)
    if mise_venv:
        rels.add(mise_venv)
    for entry in config.get("per_side_paths") or []:
        if isinstance(entry, str):
            rels.add(entry)

    args: List[str] = []
    for rel in sorted(rels):
        if not _valid_per_side_rel(rel):
            console.print(
                f"[yellow]Warning: invalid per-side path, skipping: {rel!r}[/yellow]"
            )
            continue
        host_path = workspace / rel
        if host_path.is_symlink() or (host_path.exists() and not host_path.is_dir()):
            console.print(
                f"[yellow]Warning: workspace path {rel!r} is a file or symlink "
                "— cannot shadow it per-side; the jail will see the host's "
                "entry[/yellow]"
            )
            continue
        backing = ws_state / "venv-shadows" / rel.replace("/", "__")
        backing.mkdir(parents=True, exist_ok=True)
        args.extend(["-v", f"{backing}:/workspace/{rel}"])
    return args


def _retire_jail_made_venv(workspace: Path) -> None:
    """Delete workspace venvs that a jail materialized under the old
    shared-store model.

    Examines every venv-shaped member of the shadow set — ``.venv`` plus
    the mise-configured venv path.  Detection: ``pyvenv.cfg``'s
    ``home =`` names a jail-flavored interpreter dir (``/workspace/…``,
    ``/mise/…``, or the previously shared ``~/.local/share/mise``) that
    does not exist on the host.  Such a venv is broken derived state on
    the only side that can still see it — the shadow mount hides it from
    every jail, so nothing would ever repair it.  A venv whose ``home``
    resolves is left strictly alone, as is a symlinked venv (host-made
    by definition — jails create real dirs).  Host-side only, and only
    called on the fresh-container path: a running old-model jail may
    still be using the venv through the shared workspace bind.
    """
    if os.environ.get("YOLO_VERSION") is not None:
        return
    rels = {".venv"}
    mise_venv = _mise_config_venv_path(workspace)
    if mise_venv:
        rels.add(mise_venv)
    jail_prefixes = (
        "/workspace/",
        "/mise/",
        str(Path.home() / ".local" / "share" / "mise"),
    )
    for rel in sorted(rels):
        if not _valid_per_side_rel(rel):
            continue
        venv_dir = workspace / rel
        if venv_dir.is_symlink():
            continue
        try:
            text = (venv_dir / "pyvenv.cfg").read_text()
        except (OSError, UnicodeDecodeError):
            continue
        home = ""
        for line in text.splitlines():
            key, sep, value = line.partition("=")
            if sep and key.strip() == "home":
                home = value.strip()
                break
        if not home:
            continue
        if not home.startswith(jail_prefixes) or Path(home).exists():
            continue
        console.print(
            f"[yellow]Removing jail-made {rel} — its interpreter at {home} "
            "does not exist on the host[/yellow]"
        )
        shutil.rmtree(venv_dir, ignore_errors=True)


def _live_yolo_containers(runtime: str) -> "Optional[set[str]]":
    """Names of yolo-* containers currently running/paused/restarting.

    Returns ``None`` (NOT an empty set) when the runtime can't be
    enumerated — "liveness unknown" must never read as "nothing live"
    (same fail-safe polarity as prune._find_referenced_build_roots).
    """
    live: set[str] = set()
    if runtime == "container":
        # Apple Container CLI has no docker-style `ps` or Go-template
        # --format (same special-case as find_existing_container and
        # ps()); `container ls` lists *running* containers only, so
        # every yolo-* row it prints is live.
        try:
            res = subprocess.run(
                ["container", "ls"],
                capture_output=True,
                text=True,
                timeout=10,
            )
        except (FileNotFoundError, OSError, subprocess.TimeoutExpired):
            return None
        if res.returncode != 0:
            return None
        for line in res.stdout.strip().splitlines()[1:]:  # skip header
            parts = line.split()
            if parts and parts[0].startswith("yolo-"):
                live.add(parts[0])
        return live
    try:
        res = subprocess.run(
            [runtime, "ps", "-a", "--format", "{{.Names}} {{.State}}"],
            capture_output=True,
            text=True,
            timeout=10,
        )
    except (FileNotFoundError, OSError, subprocess.TimeoutExpired):
        return None
    if res.returncode != 0:
        return None
    for line in res.stdout.splitlines():
        parts = line.strip().split()
        if len(parts) < 2:
            continue
        name, state = parts[0], parts[1]
        if name.startswith("yolo-") and state.lower() in (
            "running",
            "paused",
            "restarting",
        ):
            live.add(name)
    return live


# Map a configured LSP server name to (npm packages, go packages) the
# bootstrap script should ensure installed.  Anything not in this table
# is the user's responsibility (e.g. they specified a ``command`` that
# already exists in the image).  The ``mcp-language-server`` go binary
# is added once whenever *any* LSP is configured because Gemini wraps
# every LSP through it.
_LSP_INSTALL_RECIPES: Dict[str, Dict[str, List[str]]] = {
    "python": {"npm": ["pyright"], "go": []},
    "typescript": {"npm": ["typescript-language-server", "typescript"], "go": []},
    "go": {"npm": [], "go": ["golang.org/x/tools/gopls@latest"]},
}
_LSP_GEMINI_BRIDGE_GO = "github.com/isaacphi/mcp-language-server@latest"


def _resolve_lsp_installs(lsp_servers: Dict[str, Any]) -> Dict[str, str]:
    """Translate a configured ``lsp_servers`` dict into install lists.

    Returns ``{"npm": "pkg1\\npkg2", "go": "pkg1\\npkg2"}`` (newline-
    separated to keep the bash side parser-free).  Empty strings when
    nothing's configured.

    Server names not in :data:`_LSP_INSTALL_RECIPES` contribute nothing —
    callers wiring a custom LSP point ``command`` at a binary they
    install themselves (image/mise/etc.).
    """
    if not isinstance(lsp_servers, dict) or not lsp_servers:
        return {"npm": "", "go": ""}
    npm: List[str] = []
    go: List[str] = []
    for name in lsp_servers:
        recipe = _LSP_INSTALL_RECIPES.get(name)
        if not recipe:
            continue
        for pkg in recipe["npm"]:
            if pkg not in npm:
                npm.append(pkg)
        for pkg in recipe["go"]:
            if pkg not in go:
                go.append(pkg)
    # mcp-language-server is the bridge Gemini uses to wrap every LSP
    # as an MCP server.  Pull it whenever *any* LSP is configured —
    # including custom ones outside our recipe table — since Gemini
    # will go looking for it regardless of who owns the LSP binary.
    go.append(_LSP_GEMINI_BRIDGE_GO)
    return {"npm": "\n".join(npm), "go": "\n".join(go)}


def _bind_mount_targets() -> "set[str]":
    """Paths that are themselves bind mountpoints in the current mount ns.

    Read from ``/proc/self/mountinfo`` (field 5 is the mount point).  Used
    to detect the nested-jail case where a host source *file* we want to
    bind-mount ``:ro`` is itself a bind mountpoint — rootless nested
    podman/crun cannot use such a file as a bind *source* and fails the
    whole ``run`` with ``mount <src>: No such file or directory``.  Empty
    set on any read error (non-Linux, restricted proc) — callers then take
    the normal direct-mount path, which is correct off-nested-jail.
    """
    targets: "set[str]" = set()
    try:
        with open("/proc/self/mountinfo") as f:
            for line in f:
                parts = line.split()
                if len(parts) >= 5:
                    targets.add(parts[4])
    except OSError:
        pass
    return targets


def _is_bind_mountpoint(path: Path, mount_targets: "set[str]") -> bool:
    """True if ``path`` (or its realpath) is itself a bind mountpoint.

    ``os.path.ismount`` only detects directory mountpoints, not single-file
    bind mounts, so we match against ``/proc/self/mountinfo`` targets.
    """
    try:
        rp = os.path.realpath(path)
    except OSError:
        rp = str(path)
    return str(path) in mount_targets or rp in mount_targets


def _ro_file_mount_arg(
    host_file: Path,
    container_path: str,
    ws_state: Path,
    rel: str,
    mount_targets: "set[str]",
) -> List[str]:
    """``-v host_file:container_path:ro`` args, dereferencing nested binds.

    When ``host_file`` is itself a bind mountpoint (nested jail — the outer
    jail already bind-mounted this exact config file in), rootless podman
    can't use it as a bind source.  Copy it to a plain file under
    ``ws_state`` (``rel``) and mount that stable inode instead.  On a real
    host the file is plain, so we mount it directly with no copy.
    """
    src = host_file
    if _is_bind_mountpoint(host_file, mount_targets):
        deref = ws_state / rel
        deref.parent.mkdir(parents=True, exist_ok=True)
        try:
            shutil.copy2(host_file, deref)
            src = deref
        except OSError:
            src = host_file  # best-effort: fall back to the direct mount
    return ["-v", f"{src}:{container_path}:ro"]


def _scratch_mount_args(mode: object) -> List[str]:
    """Build the mount args for the read-only-rootfs scratch dirs.

    ``--read-only`` (set unconditionally on the rootfs) means anything
    that writes to /tmp, /var/tmp, /var/lib/containers,
    /var/cache/containers, /run, or /dev/shm needs an explicit writable
    mount.

    Nested podman (podman-in-jail) needs BOTH container scratch dirs
    writable: /var/lib/containers is the graph/run store, and
    /var/cache/containers is podman's own cache (blob-info + additional
    image store), which containers-common computes as
    ``<cachedir>/containers`` = /var/cache/containers for root.  Its parent
    /var/cache is on the read-only rootfs overlay, so ``podman run`` dies
    with ``mkdir /var/cache/containers: read-only file system`` unless we
    mount it writable — same treatment as /var/lib/containers.

    ``ephemeral_storage`` chooses the backing for /tmp, /var/tmp,
    /var/lib/containers, and /var/cache/containers:

      * ``"volume"`` (default) — anonymous podman volumes.  Disk-backed,
        wiped by ``podman run --rm`` on container exit, doesn't compete
        with the jail's memory budget.
      * ``"tmpfs"`` — RAM-backed.  Faster but counts against the host's
        free memory and can OOM the jail under pressure.

    /run and /dev/shm always stay on tmpfs regardless: /run holds the
    /etc/localtime and /etc/timezone symlinks the entrypoint
    rewrites (and is small), and /dev/shm is shared memory by
    definition — disk-backing it would defeat the point and slow
    Chromium.

    Apple Container's ``-v`` syntax for anonymous volumes isn't
    interchangeable with podman's, and its ``--tmpfs`` only takes a
    bare path, so AC stays on tmpfs.
    """
    if not isinstance(mode, str) or mode not in ("volume", "tmpfs"):
        mode = "volume"
    if mode == "volume":
        # Anonymous volumes: ``-v <container_path>`` with no host source.
        # Podman creates an anonymous volume and removes it with ``--rm``.
        # Image bakes /tmp etc. with default mkdir perms (0755); that's
        # fine since the container runs as root inside the user namespace.
        return [
            "-v",
            "/tmp",
            "-v",
            "/var/tmp",
            "-v",
            "/var/lib/containers",
            "-v",
            "/var/cache/containers",
            "--tmpfs",
            "/run",
            "--tmpfs",
            "/dev/shm:size=2g",
        ]
    return [
        "--tmpfs",
        # mode=1777 ensures non-root UIDs can write to tmpfs (some
        # backends default to 755).
        "/tmp:exec,mode=1777",
        "--tmpfs",
        "/var/tmp:exec,mode=1777",
        "--tmpfs",
        "/var/lib/containers",
        "--tmpfs",
        "/var/cache/containers",
        "--tmpfs",
        "/run",
        "--tmpfs",
        "/dev/shm:size=2g",
    ]


def _refresh_jail_briefings(
    cname: str,
    workspace: Path,
    config: Dict[str, Any],
    runtime: str,
    network_default: str,
) -> Path:
    """Rebuild the per-jail skills staging + AGENTS.md/CLAUDE.md files.

    Called on every ``yolo`` invocation — including the attach-to-running
    branch — so host-side edits and deletions to
    ``~/.{copilot,gemini,claude}/skills/`` and the host-level AGENTS.md /
    CLAUDE.md propagate to a live jail.

    The propagation only works because the underlying writes preserve the
    inodes the container's bind mounts captured at start time:
      * `_prepare_skills` clears the contents *inside* each
        ``skills-<agent>/`` dir rather than rmtree-and-mkdir'ing the dir
        itself — so the container's `-v skills-claude:/home/agent/.claude/skills`
        mount keeps seeing the same parent inode and the updated listing.
      * `generate_agents_md` uses ``Path.write_text`` (O_WRONLY|O_CREAT|O_TRUNC),
        which truncates the existing AGENTS-*.md / CLAUDE.md file in place
        and preserves the inode the file→file bind mount captured.

    If either side starts unlinking and recreating the mount source, the
    container will stay pinned to the orphaned inode and refreshes
    silently no-op for any running jail.

    Computes the per-call args from ``config`` alone so it can run before
    the heavyweight ``run_cmd`` is assembled.  Idempotent; safe to call
    repeatedly.
    """
    network_section = config.get("network") or {}
    net_mode = network_section.get("mode") or network_default
    forward_host_ports = (
        network_section.get("forward_host_ports") or [] if net_mode == "bridge" else []
    )
    normalized_blocked = _normalize_blocked_tools(config.get("security"))

    mount_descriptions: List[str] = []
    for mount in config.get("mounts", []) or []:
        colon_idx = mount.rfind(":")
        if colon_idx > 0 and mount[colon_idx + 1 : colon_idx + 2] == "/":
            host_path = mount[:colon_idx]
            container_path = mount[colon_idx + 1 :]
        else:
            host_path = mount
            container_path = f"/ctx/{Path(host_path).expanduser().resolve().name}"
        resolved = Path(host_path).expanduser().resolve()
        if resolved.exists():
            mount_descriptions.append(f"{resolved}:{container_path}")

    # Enabled loopholes, listed by name in the briefing (the agent gets
    # the actual set, not an instruction to go enumerate it).
    try:
        enabled_loopholes = [
            (lo.name, lo.description)
            for lo in _loopholes.discover_loopholes(
                loopholes_config=config.get("loopholes")
            )
        ]
    except Exception:
        enabled_loopholes = []

    agents = selected_agents(config)
    _prepare_skills(cname, agents)
    return generate_agents_md(
        cname,
        workspace,
        normalized_blocked,
        mount_descriptions,
        net_mode=net_mode,
        runtime=runtime,
        forward_host_ports=forward_host_ports or None,
        loopholes=enabled_loopholes or None,
        resources=config.get("resources") or None,
        agents_md_extra=config.get("agents_md_extra"),
        agents=agents,
    )


def _entrypoint_preflight(repo_root: Path, workspace: Path, config: Dict[str, Any]):
    """Generate jail-managed config into a temp home to catch config/render errors."""
    src_dir = repo_root / "src"
    normalized_blocked = _normalize_blocked_tools(config.get("security"))
    env = os.environ.copy()
    # Drop inherited PYTHONPATH so the subprocess can only import
    # entrypoint from src_dir (via sys.path.insert below).  Without
    # this, running ``yolo check`` from inside a jail would silently
    # validate the nix-baked entrypoint instead of this repo's, since
    # PYTHONPATH there points at the baked package.
    env.pop("PYTHONPATH", None)

    with tempfile.TemporaryDirectory(prefix="yolo-check-") as tmp:
        env.update(
            {
                "JAIL_HOME": tmp,
                "HOME": tmp,
                "NPM_CONFIG_PREFIX": f"{tmp}/.npm-global",
                "GOPATH": f"{tmp}/go",
                # The fixed in-jail store path — the dry-run then renders
                # the same /mise/shims PATH strings the jail will.
                "MISE_DATA_DIR": "/mise",
                "YOLO_HOST_DIR": str(workspace.resolve()),
                "YOLO_BLOCK_CONFIG": json.dumps(normalized_blocked),
                "YOLO_MISE_TOOLS": json.dumps(_merge_mise_tools(config)),
                "YOLO_LSP_SERVERS": json.dumps(config.get("lsp_servers", {})),
                "YOLO_MCP_SERVERS": json.dumps(config.get("mcp_servers", {})),
                "YOLO_MCP_PRESETS": json.dumps(config.get("mcp_presets", [])),
                "YOLO_AGENTS": json.dumps(selected_agents(config)),
            }
        )
        # Apply user-defined env vars from env_sources
        for env_key, env_val in _resolve_env_sources(workspace, config).items():
            env[env_key] = env_val

        # Only the SELECTED agents' config writers run in the dry-run, and
        # each agent validates only its own output JSON — so `yolo check`
        # never requires configs for agents that won't be installed.
        code = f"""
import json
import sys
import tomllib
from pathlib import Path

sys.path.insert(0, {str(src_dir)!r})
import entrypoint
from entrypoint.agent_configs import CONFIG_WRITERS

entrypoint.generate_shims()
entrypoint.generate_agent_launchers()
entrypoint.generate_bashrc()
entrypoint.generate_bootstrap_script()
entrypoint.generate_venv_precreate_script()
entrypoint.generate_mise_config()
entrypoint.generate_mcp_wrappers()

def _load_json(p):
    json.loads(p.read_text())

def _load_toml(p):
    tomllib.loads(p.read_text())

# Per-agent config outputs to validate after each writer runs — each with
# the parser for its format (JSON for most agents, TOML for codex).
_agent_outputs = {{
    "copilot": [
        (entrypoint.COPILOT_DIR / "mcp-config.json", _load_json),
        (entrypoint.COPILOT_DIR / "lsp-config.json", _load_json),
    ],
    "gemini": [(entrypoint.GEMINI_DIR / "settings.json", _load_json)],
    "claude": [(entrypoint.CLAUDE_DIR / "settings.json", _load_json)],
    "opencode": [(entrypoint.OPENCODE_DIR / "opencode.json", _load_json)],
    "pi": [(entrypoint.PI_DIR / "settings.json", _load_json)],
    "codex": [(entrypoint.CODEX_DIR / "config.toml", _load_toml)],
}}
for _agent in entrypoint._load_agents():
    CONFIG_WRITERS[_agent]()
    for _out, _parse in _agent_outputs.get(_agent, []):
        _parse(_out)
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


def _inject_agent_yolo_flags(full_command: "list[str]") -> None:
    """Mutate ``full_command`` in place to inject agent-specific YOLO
    flags based on the leading binary name.

    The per-agent flag list comes from the agent registry
    (``AgentSpec.yolo_flags``), keyed on the leading binary:

    - ``gemini``: ``--yolo`` (skipped if the user passed ``-y`` / ``--yolo``).
    - ``copilot``: ``--yolo`` and ``--no-auto-update`` (same no-dup basis).
    - ``claude``: ``--dangerously-skip-permissions``.  The settings.json
      allow-list that used to serve as YOLO was half-broken for weeks; the
      flag is the single source of truth, and ``IS_SANDBOX=1`` in the jail
      env suppresses its confirmation prompt.
    - ``opencode`` / ``pi``: no launch flags — their auto-approve lives in
      their config files (opencode ``permission: allow``, pi
      ``defaultProjectTrust: always``).

    All other commands are left alone.
    """
    if not full_command:
        return
    head = full_command[0]
    spec = next((s for s in AGENTS.values() if s.install.bin == head), None)
    if spec is None:
        return
    # Insert flags in reverse so their relative order is preserved (each
    # inserts at index 1).  Skip a flag if it (or a known alias, e.g. -y for
    # --yolo) is already present.
    for flag in reversed(spec.yolo_flags):
        aliases = YOLO_FLAG_ALIASES.get(flag, [])
        if flag in full_command or any(a in full_command for a in aliases):
            continue
        full_command.insert(1, flag)


def _maybe_warn_about_oom_killer(exit_code: int, runtime: str) -> None:
    """Print a hint when the agent's exit looks like an OOM-kill on a tiny
    Podman Machine.  Triggered by exit 137 (128 + SIGKILL) on macOS+podman
    with a VM under the recommended floor.

    137 isn't *only* OOM (manual `kill -9` also produces it), so we phrase
    the hint as "this often means" rather than asserting.  Side effects
    are limited to a single `podman machine inspect` call.

    Ported from PR #21 (kurt-hs).
    """
    if not (IS_MACOS and runtime == "podman" and exit_code == 137):
        return
    info = _podman_machine_memory()
    if info is None:
        return
    name, mem_mb = info
    if mem_mb >= PODMAN_MACHINE_MEMORY_FLOOR_MB:
        return
    console.print(
        f"[dim]Exit 137 is SIGKILL.  On Podman Machine this often means "
        f"the VM's OOM-killer fired — '{name}' has only {mem_mb} MB "
        f"(below the {PODMAN_MACHINE_MEMORY_FLOOR_MB} MB recommended floor "
        f"for running an agent).  {_podman_machine_resize_hint()}[/dim]"
    )


def _ensure_broker_relay(cname: str, runtime: str) -> None:
    """Ensure the per-jail broker relay is running; never fail the caller.

    Called on every path that targets a jail — fresh run AND both
    exec/attach branches — because the relay is a supervised standalone
    process: the container outlives any single ``yolo`` invocation
    (conmon supervises it independently), so a relay whose spawning
    process died must be healed by whichever invocation touches the
    jail next.

    Skipped for Apple Container (no sockets-dir mount there, so the
    broker was never wired through it) and when the singleton socket is
    absent (nothing to relay to; ``_broker_ensure`` owns that layer).
    """
    if runtime == "container" or not BROKER_SINGLETON_SOCKET.exists():
        return
    sockets_dir = _host_service_sockets_dir(cname)
    if not sockets_dir.is_dir():
        # On the fresh-run path the dir was just mkdir'd, so a missing
        # dir means we are HEALING a running jail whose mounted dir was
        # removed after launch (host /tmp aging; a teardown that lost
        # its guards).  The mkdir below creates a NEW inode — the
        # container's bind mount still pins the deleted one, so the
        # healed relay socket will never appear in-jail.  Heal anyway
        # (host state stays consistent, doctor probes the host side)
        # but say plainly that only a relaunch remounts the dir.
        try:
            if find_running_container(cname, runtime=runtime):
                console.print(
                    f"[yellow]claude-oauth-broker: sockets dir {sockets_dir} "
                    f"was removed after the jail started — the healed relay "
                    f"will NOT be visible inside the running jail.  Relaunch "
                    f"the jail to remount it.[/yellow]"
                )
        except Exception:  # noqa: BLE001 — diagnosis must not block the heal
            pass
    try:
        _relay_ensure(cname, sockets_dir)
    except Exception as e:  # noqa: BLE001 — a broken relay must not block the jail
        console.print(f"[yellow]claude-oauth-broker: relay not ensured: {e}[/yellow]")


def _owner_pid_file(cname: str) -> Path:
    return OWNER_PID_DIR / cname


def _write_owner_pid(cname: str) -> None:
    """Record that THIS process started the jail, so a later ``yolo`` can
    tell a jail orphaned by an uncatchable kill from a live one."""
    try:
        OWNER_PID_DIR.mkdir(parents=True, exist_ok=True)
        _owner_pid_file(cname).write_text(f"{os.getpid()}\n")
    except OSError:
        pass


def _clear_owner_pid(cname: str) -> None:
    try:
        _owner_pid_file(cname).unlink(missing_ok=True)
    except OSError:
        pass


def _pid_alive(pid: int) -> bool:
    """True if a process with ``pid`` exists (owned by anyone)."""
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        # Exists but owned by another user — still alive.
        return True
    except OSError:
        # Uncertain — assume alive so we never reap a jail we can't prove
        # is orphaned.
        return True
    return True


def _stop_jail(cname: str, runtime: str) -> None:
    """Best-effort stop of a jail, then drop its owner-PID file.

    The jail was started with ``--rm``, so stopping it also removes it.
    Bounded timeout so teardown can't hang the terminal.
    """
    if runtime == "container":
        # Apple Container has no `-t` grace flag; give its own default.
        cmd = ["container", "stop", cname]
        timeout = 30
    else:
        cmd = [runtime, "stop", "-t", str(TEARDOWN_STOP_TIMEOUT_SECONDS), cname]
        timeout = TEARDOWN_STOP_TIMEOUT_SECONDS + 5
    try:
        subprocess.run(cmd, capture_output=True, timeout=timeout)
    except (FileNotFoundError, OSError, subprocess.TimeoutExpired):
        pass
    _clear_owner_pid(cname)


def _reap_orphaned_jails(runtime: str) -> None:
    """Stop running jails whose owning ``yolo run`` process is gone.

    Window close (SIGHUP) and ``kill`` (SIGTERM) are torn down
    synchronously by the proxy's teardown handler.  But SIGKILL is
    uncatchable and a host crash runs no handler, so a jail can outlive
    its owner with the agent still running.  Every ``yolo run`` sweeps
    these first: a live ``yolo-*`` jail whose recorded owner PID is dead
    was abandoned — stop it (transparent teardown, same end state as a
    clean window close).  Running this before the attach decision also
    stops us execing a SECOND agent into an orphan of this workspace.

    Conservative: a jail with no owner-PID file (started before this
    feature landed, or a runtime we don't track) is left alone — we only
    reap what we can prove is orphaned.
    """
    if runtime == "container":
        # No owner-PID lifecycle wired for Apple Container yet.
        return
    live = _live_yolo_containers(runtime)
    if not live:
        return
    for name in live:
        try:
            raw = _owner_pid_file(name).read_text().strip()
        except OSError:
            continue  # no owner recorded — can't prove orphaned
        try:
            pid = int(raw)
        except ValueError:
            continue
        if not _pid_alive(pid):
            console.print(
                f"[dim]Reaping orphaned jail {name} (owner pid {pid} is gone)...[/dim]"
            )
            _stop_jail(name, runtime)


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
    dry_run: bool = typer.Option(
        False,
        "--dry-run",
        help="macos-user only: print the full native run plan without executing it.",
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

    # Which coding agents this jail installs/configures (library model).
    # Default is claude only; a config's ``agents`` list narrows/expands it.
    agents = selected_agents(config)
    agent_specs = [AGENTS[a] for a in agents]

    # Command construction (needed for both exec and run paths)
    full_command = list(ctx.args)

    target_cmd = "bash"
    if full_command:
        _inject_agent_yolo_flags(full_command)
        target_cmd = shlex.join(full_command)

    # ── Native macOS backend dispatch (macos-user) ────────────────────────
    # No VM, no Linux image: run the agent as a dedicated macOS user under
    # Seatbelt, with `packages:` materialized via native aarch64-darwin nix.
    # This must branch BEFORE any container machinery (stale-jail reap, image
    # load, container argv) so the native path never touches podman/AC.
    if runtime == "macos-user":
        from .macos_user import run_macos_user

        agent_argv = full_command or ["/bin/zsh", "-l"]
        sys.exit(
            run_macos_user(
                workspace,
                config,
                agents,
                agent_argv,
                repo_src=repo_root / "src",
                dry_run=dry_run,
            )
        )
    if dry_run:
        console.print(
            "[bold red]--dry-run is only supported for the macos-user "
            'runtime.[/bold red]  Set runtime: "macos-user" (or '
            "YOLO_RUNTIME=macos-user) to use it."
        )
        sys.exit(1)

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
    # Sweep jails orphaned by an uncatchable kill (SIGKILL / host crash)
    # before we decide whether to attach — clean teardown is handled by
    # the proxy's SIGHUP/SIGTERM handler; this covers what a handler
    # can't catch, and stops us from execing a second agent into an
    # orphan of this workspace.  See _reap_orphaned_jails.
    _reap_orphaned_jails(runtime)
    existing_cid = None if new else find_running_container(cname, runtime=runtime)

    # Refresh the per-jail skills + AGENTS/CLAUDE staging on every invocation.
    # The staging dir is bind-mounted into the jail, so refreshing it here
    # propagates host-side edits and deletions to a running container
    # (inode-sharing).  Without this, deletions only took effect after a
    # full container restart.  See _refresh_jail_briefings.
    agents_path = _refresh_jail_briefings(cname, workspace, config, runtime, network)

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
        # The container may have outlived the `yolo run` that spawned its
        # relay (terminal close, SIGKILL) — heal it before handing the
        # session over.
        _ensure_broker_relay(cname, runtime)
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
        # Wrap with run_with_proxy so a host-side ^Z suspends the proxy
        # (host shell shows it as a stopped job) instead of wedging
        # claude inside the jail.  See cli/tty_proxy.py for the why.
        # Bare subprocess.run when stdin isn't a TTY happens automatically
        # inside run_with_proxy.
        try:
            rc = run_with_proxy(run_cmd)
        except FileNotFoundError:
            console.print(
                f"[bold red]Configured runtime '{runtime}' not found on PATH.[/bold red]"
            )
            console.print(
                "[dim]Run `yolo check` to validate runtime availability before restarting.[/dim]"
            )
            sys.exit(1)
        _maybe_warn_about_oom_killer(rc, runtime)
        sys.exit(rc)

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
            # Same healing as the existing-container branch above.
            _ensure_broker_relay(cname, runtime)
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
                rc = run_with_proxy(run_cmd)
            except FileNotFoundError:
                console.print(
                    f"[bold red]Configured runtime '{runtime}' not found on PATH.[/bold red]"
                )
                console.print(
                    "[dim]Run `yolo check` to validate runtime availability before restarting.[/dim]"
                )
                sys.exit(1)
            _maybe_warn_about_oom_killer(rc, runtime)
            sys.exit(rc)

    # Remove any stopped container with the same name left over from an
    # unclean shutdown (e.g. OOM-kill, host reboot).  Without this,
    # `<runtime> run --name <cname>` fails with "container already exists".
    stale_cid = find_existing_container(cname, runtime=runtime)
    if stale_cid:
        print(f"Removing stale container {cname}...", file=sys.stderr)
        _remove_stale_container(cname, runtime=runtime)

    # Retire jail-made workspace venvs left behind by the old
    # shared-store model — after the exec-into-existing paths above (a
    # still-running old-model jail may be using that venv through the
    # shared workspace bind) and before the venv shadow mounts hide the
    # paths from the new jail.  See _retire_jail_made_venv.
    _retire_jail_made_venv(workspace)

    import time as _time

    _profile_times = {}
    if profile:
        _profile_times["start"] = _time.monotonic()

    extra_packages = _effective_packages(config)
    mise_tools = _merge_mise_tools(config)
    lsp_servers = config.get("lsp_servers", {})
    lsp_installs = _resolve_lsp_installs(lsp_servers)
    mcp_servers = config.get("mcp_servers", {})
    mcp_presets = config.get("mcp_presets", [])
    host_claude_files = config.get("host_claude_files", DEFAULT_HOST_CLAUDE_FILES)
    user_env = _resolve_env_sources(workspace, config)
    mise_disabled_tools = _merge_mise_disabled_tools(user_env.get("MISE_DISABLE_TOOLS"))
    if not auto_load_image(
        repo_root, extra_packages=extra_packages or None, runtime=runtime
    ):
        # No runnable image and we can't build/load one — auto_load_image has
        # already printed the actionable reason (e.g. macOS needs a Linux
        # builder / a published cache).  Stop cleanly instead of falling
        # through to a doomed container launch that ends in a registry-pull
        # 401.
        sys.exit(1)

    # Jail-land mise store — shared by all jails, never by the host, and
    # mounted at the fixed neutral path /mise in every jail (see
    # docs/design/jail-state-separation-design.md).  Nested jails re-mount the
    # /mise they already see.
    mise_store = _jail_mise_store_dir()

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
    #
    # `mounts` are meant to be READ-ONLY context (`/ctx/…`).  Apple Container
    # does NOT honor the `:ro` suffix on `-v` (per-mount read-only is still
    # unimplemented upstream — apple/container#889; only the whole-rootfs
    # `--read-only` shipped), so the mount would silently be read-WRITE — the
    # agent could clobber the user's source.  Rather than hand out unintended
    # write access, we SKIP these mounts on Apple Container and tell the user
    # to switch to podman for read-only context mounts.
    mount_args = []
    mount_descriptions = []
    ctx_mounts_unsafe = runtime == "container"
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
        if ctx_mounts_unsafe:
            console.print(
                f"[yellow]Skipping mount {host_path} → {container_path}: Apple "
                "Container ignores read-only (:ro), so it would be writable. "
                "Use `YOLO_RUNTIME=podman` for read-only context mounts.[/yellow]"
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
        # Unmask /proc/sys (OCI default marks it read-only): the jail's own
        # podman needs to write netns-scoped sysctls when netavark sets up
        # bridge networking (net/ipv4/conf/<bridge>/route_localnet et al.) —
        # with the default mask, `podman run` inside the jail dies with
        # "set sysctl ...: Read-only file system".  Safe for a rootless
        # jail: the kernel still rejects writes to non-namespaced sysctls,
        # so only the jail's private netns/ipc knobs actually open up.
        run_flags.extend(["--security-opt", "unmask=/proc/sys"])
    if sys.stdout.isatty():
        run_flags.append("-t")

    # Per-workspace overlays for workspace-specific state
    ws_state = workspace / ".yolo" / "home"
    ws_state.mkdir(parents=True, exist_ok=True)
    (ws_state / "ssh").mkdir(exist_ok=True, mode=0o700)
    # Snapshot the current mount table once: single-file :ro host mounts
    # (git ignore, user config, host-claude files) dereference their source
    # through this when it's itself a bind mountpoint (nested-jail case).
    mount_targets = _bind_mount_targets()
    # Per-workspace writable overlays — isolate cross-jail writes.
    # These sit on top of the :ro GLOBAL_HOME base so each jail has its
    # own copy of generated configs, installed tools, and caches.  The
    # per-agent overlay dirs (.copilot/.gemini/.claude/.pi) are derived
    # from the selected agents' registry specs — an unselected agent gets
    # no overlay.  Stored without the leading dot (ws_state uses bare
    # names, mounted at /home/agent/.<name> below).
    agent_overlay_subdirs = [
        d.lstrip(".") for spec in agent_specs for d in spec.overlay_dirs
    ]
    for subdir in [
        "npm-global",
        "local",
        "go",
        "yolo-shims",
        "config",
        *agent_overlay_subdirs,
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
        # LSP install sentinel — per-workspace because lsp_servers is a
        # per-workspace config knob.  Writable overlay so the bootstrap
        # script can update it after install/uninstall.
        "yolo-installed-lsps",
    ]:
        (ws_state / fname).touch()

    # Seed the selected agents' config dirs with auth tokens from the :ro
    # GLOBAL_HOME base.  On first boot for this workspace the per-workspace
    # dirs are empty — copy auth-related files so agents can authenticate.
    # Subsequent boots skip files that already exist (the entrypoint
    # regenerates configs each time).  Only selected agents' overlay dirs
    # exist, so seed only those.  (Claude credentials live in the shared
    # dir .claude-shared-credentials/, not .claude/, so _seed_agent_dir
    # won't encounter them.)
    for subdir in agent_overlay_subdirs:
        _seed_agent_dir(GLOBAL_HOME / f".{subdir}", ws_state / subdir)

    if "claude" in agents:
        # Sync claude.json login/onboarding state between the GLOBAL_HOME seed
        # and the per-workspace overlay — both directions, see
        # _sync_claude_json_seed.  ~/.claude.json is a symlink →
        # .claude/claude.json, so the actual file lives inside the writable
        # .claude/ overlay; the seed is likewise written through the real path
        # (GLOBAL_HOME/.claude.json is a symlink too — see storage.py).
        _sync_claude_json_seed(
            GLOBAL_HOME / ".claude" / "claude.json",
            ws_state / "claude" / "claude.json",
        )

    # Migrate old per-workspace overlays into new unified agent dirs.
    # Before the read-only refactor, agent state used individual file/dir overlays
    # (e.g. claude-projects/, copilot-sessions/).  Now each agent gets a single
    # dir overlay (claude/, copilot/, gemini/).  Copy old data once if present.
    # Gated on selection so we don't recreate an overlay dir for an agent that
    # isn't installed in this jail (the mount below only exists for selected).
    if "claude" in agents:
        _migrate_old_overlay(
            ws_state / "claude-projects", ws_state / "claude" / "projects"
        )
    if "copilot" in agents:
        _migrate_old_overlay(
            ws_state / "copilot-sessions", ws_state / "copilot" / "session-state"
        )
    if "gemini" in agents:
        _migrate_old_overlay(
            ws_state / "gemini-history", ws_state / "gemini" / "history"
        )

    # Migrate old claude-settings.json file overlay into new claude/settings.json.
    # Preserves user customizations (model, hooks, etc.) from pre-refactor.
    if "claude" in agents:
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
            # Jail-land mise store at the fixed neutral path /mise —
            # shared across jails only, never with the host, so no host
            # path string leaks into the jail (see
            # docs/design/jail-state-separation-design.md).  Backed by a named
            # volume: the macOS host filesystem couldn't hold the Linux
            # toolchains anyway.  See MISE_STORE_VOLUME for why the name
            # is versioned; reclaim the old volume with
            # `container volume rm yolo-mise-data`.
            "-v",
            f"{MISE_STORE_VOLUME}:/mise",
            # Apple Container's --tmpfs only takes a plain path (no options)
            "--tmpfs",
            "/tmp",
            "--tmpfs",
            "/var/tmp",
            "--tmpfs",
            "/var/lib/containers",
            "--tmpfs",
            "/var/cache/containers",
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
            "-v",
            f"{ws_state / 'yolo-installed-lsps'}:/home/agent/.yolo-installed-lsps",
            # (Per-agent config-dir overlays + claude shared credentials are
            # appended below, gated on the selected agent set.)
            # Other per-workspace overlays
            "-v",
            f"{ws_state / 'bash_history'}:/home/agent/.bash_history",
            "-v",
            f"{ws_state / 'ssh'}:/home/agent/.ssh",
            # --- Shared mounts ---
            "-v",
            # Jail-land mise store at the fixed neutral path /mise —
            # identical in every jail on every machine.  The store is
            # shared across jails only, never with the host (whose
            # ~/.local/share/mise stays fully its own): the only thing
            # crossing the host↔jail boundary is the workspace itself.
            # Host-created venvs no longer resolve in-jail by design —
            # the per-side venv shadow mounts below give each side its
            # own.  See docs/design/jail-state-separation-design.md.
            #
            # macOS podman keeps a named volume: the host tree has
            # Mach-O binaries that cannot execute in the Linux container.
            # See MISE_STORE_VOLUME for why the name is versioned.
            f"{MISE_STORE_VOLUME}:/mise" if IS_MACOS else f"{mise_store}:/mise",
        ]
        # Ephemeral scratch dirs — see _scratch_mount_args() for the
        # tmpfs-vs-volume trade-off and config knob.
        run_cmd.extend(_scratch_mount_args(config.get("ephemeral_storage")))

        # Per-agent config-dir overlays (library model): mount only the
        # SELECTED agents' overlay dirs (.copilot/.gemini/.claude/.pi).  Auth
        # tokens are seeded from GLOBAL_HOME on first use (see _seed_agent_dir);
        # the entrypoint regenerates configs into these writable dirs each boot.
        for subdir in agent_overlay_subdirs:
            run_cmd.extend(["-v", f"{ws_state / subdir}:/home/agent/.{subdir}"])
        # Claude's shared credentials dir — mounted rw so /login in any jail
        # persists for all jails.  A directory mount (not single-file) because
        # Claude Code's IWH atomic writer uses tmp+rename which returns EBUSY
        # on single-file bind mounts.  The entrypoint symlinks
        # .claude/.credentials.json → this dir so Claude finds it.  Only
        # relevant when claude is selected.
        if "claude" in agents:
            run_cmd.extend(
                [
                    "-v",
                    f"{GLOBAL_HOME / '.claude-shared-credentials'}:/home/agent/.claude-shared-credentials",
                ]
            )

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
            "MISE_DATA_DIR=/mise",
            "-e",
            # Use a per-container cache dir so mise lockfiles don't contend with
            # the host/outer-jail's locks (shared /home/agent would otherwise share
            # ~/.cache/mise/lockfiles/, causing deadlocks in nested jails).
            "MISE_CACHE_DIR=/tmp/mise-cache",
            "-e",
            # Explicitly request the non-freethreaded prebuilt to avoid
            # "missing lib directory" errors from freethreaded builds
            # (exact flavor match excludes freethreaded+install_only).
            # install_only, NOT install_only_stripped: stripped assets only
            # exist for python-build-standalone releases from mid-2024 on,
            # so a project pinning an older patch version (e.g. 3.11.7 /
            # 20240107) got no precompiled match and mise fell back to a
            # from-source python-build — which fails in the toolchain-less
            # jail.  install_only exists for every release.
            "MISE_PYTHON_PRECOMPILED_FLAVOR=install_only",
            "-e",
            # Old python-build-standalone releases also predate GitHub
            # artifact attestations, and mise hard-fails when the expected
            # attestation is absent ("No GitHub artifact attestations
            # found") — same old-pin breakage as the flavor above.  Disable
            # the attestation layer; mise still checksums the artifact.
            "MISE_PYTHON_GITHUB_ATTESTATIONS=false",
            "-e",
            # Blanket trust for every mise config under the workspace —
            # recursive-downward and path-component-aware, which no
            # `mise trust` invocation provides (trust is dir-scoped;
            # --all only covers cwd + parents).
            "MISE_TRUSTED_CONFIG_PATHS=/workspace",
            "-e",
            # Lets projects keep host-inert, jail-only overrides in a
            # checked-in mise.jail.toml (overrides mise.toml, including
            # _.python.venv); side-effect-free when no such file exists.
            "MISE_ENV=jail",
            "-e",
            # mise's rust core backend is the one tool that does NOT
            # install into MISE_DATA_DIR: it drives rustup, whose default
            # homes (~/.rustup, ~/.cargo) are read-only in-jail — a bare
            # `rust = "..."` in a project's mise config would fail
            # provisioning with `failed create_dir_all: ~/.rustup`.
            # Point both into the writable jail-land store so toolchains
            # install once for all jails, and so the recorded
            # installs/rust/<ver> -> $CARGO_HOME/bin symlink resolves
            # identically in every jail (closes the jail<->jail rust
            # collision residual from mise-host-jail-path-mismatch.md).
            # A workspace's own mise [env] wins over these on activation
            # (verified against mise 2026.6.13).
            "RUSTUP_HOME=/mise/rustup",
            "-e",
            "CARGO_HOME=/mise/cargo",
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
            # Disable pi (pi.dev) install/usage telemetry inside the jail.
            # Container-level (not just .bashrc) so `yolo -- pi` — a
            # non-interactive process that never sources .bashrc — sees it.
            "-e",
            "PI_TELEMETRY=0",
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
            # The bootstrap script reads these to decide which language
            # server binaries to install/uninstall on this boot.  See
            # _resolve_lsp_installs and entrypoint/shell.py.
            f"YOLO_LSP_NPM_INSTALL={lsp_installs['npm']}",
            "-e",
            f"YOLO_LSP_GO_INSTALL={lsp_installs['go']}",
            "-e",
            f"YOLO_MCP_SERVERS={json.dumps(mcp_servers)}",
            "-e",
            f"YOLO_MCP_PRESETS={json.dumps(mcp_presets)}",
            "-e",
            # Selected coding agents (library model) — the entrypoint reads
            # this to decide which agent launchers + configs to generate.
            f"YOLO_AGENTS={json.dumps(agents)}",
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
            # Only set if not already overridden (e.g. by mise .env loading).
            # The ${KEY:-'value'} precedence (launch env wins) is mirrored by
            # entrypoint._hydrate_env_from_user_env_file — keep both in sync.
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

    # Mount yolo-jail repo for in-jail CLI (yolo --help, nested jailing).
    # Dev loop: when the workspace IS a yolo-jail source tree, it must back
    # /opt/yolo-jail — for a uv-tool-installed yolo, repo_root is a frozen
    # copy from the last `just install`, so nested jails would run stale
    # code instead of the live src/cli edits the workspace bind carries
    # (the loop agents_md's "Testing Changes to yolo-jail" promises).
    # Otherwise keep repo_root; in nested jails YOLO_REPO_ROOT may point to
    # an empty /opt/yolo-jail (bind mount doesn't propagate), so fall back
    # to /workspace if it's the repo.
    if _workspace_is_yolo_source_tree(workspace):
        repo_mount_src = workspace
    elif (repo_root / "flake.nix").exists():
        repo_mount_src = repo_root
    else:
        repo_mount_src = workspace
    run_cmd += [
        "--workdir",
        "/workspace",
        "-v",
        f"{repo_mount_src}:/opt/yolo-jail:ro",
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
    gpu_vendor = config.get("gpu", {}).get("vendor", "nvidia")
    gpu_unavailable_reason: Optional[str] = None
    if gpu_requested:
        if gpu_vendor == "amd":
            ok_gpu, gpu_unavailable_reason = _rocm_host_available(runtime)
        else:
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
            # Inside a container (nested jail): share the parent's user
            # namespace — a doubly-nested *new* userns hits kernel
            # restrictions.  But the nested jail still needs the same
            # device+capability set as a host-launched jail for its OWN
            # nested podman to work (podman-in-podman-in-podman, e.g. an
            # agent running DB containers inside a nested jail):
            #   * /dev/fuse   — fuse-overlayfs storage driver
            #   * SYS_ADMIN   — mount /proc et al. for the grandchild container
            #   * MKNOD       — create device nodes in image layers
            #   * NET_ADMIN + NET_RAW — netavark bridge networking for the
            #     grandchild container.  The jail's podman runs as jail-root
            #     WITHOUT a second userns (see above), so every netns it
            #     creates is owned by the jail's userns and configuring it
            #     (veth/bridge via netlink, even loopback SIOCSIFFLAGS for
            #     --network=none) needs NET_ADMIN there.  Scoped: these caps
            #     only govern the jail's own private netns, not the host's.
            #   * /dev/net/tun (best-effort) — pasta/slirp4netns rootless
            #     networking; only passed through if the parent actually has
            #     it, since a missing --device is a hard podman error.
            run_cmd.extend(
                [
                    "--security-opt",
                    "label=disable",
                    "--userns",
                    "host",
                    "--cap-add",
                    "SYS_ADMIN",
                    "--cap-add",
                    "MKNOD",
                    "--cap-add",
                    "NET_ADMIN",
                    "--cap-add",
                    "NET_RAW",
                ]
            )
            for dev in ("/dev/fuse", "/dev/net/tun"):
                if Path(dev).exists():
                    run_cmd.extend(["--device", dev])
        elif gpu_enabled and gpu_vendor == "nvidia":
            # NVIDIA GPU passthrough: CDI device injection fails with crun and
            # custom user namespaces (https://github.com/containers/podman/issues/27483).
            # AMD/ROCm does NOT take this branch — rootless --group-add keep-groups
            # is crun-only, so AMD falls through to the normal host else-branch
            # (keeping /dev/fuse + MKNOD + the default crun runtime).
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
                    "--cap-add",
                    "NET_ADMIN",
                    "--cap-add",
                    "NET_RAW",
                ]
            )
        else:
            # On host: create user namespace with UID/GID mapping for nesting.
            # NET_ADMIN + NET_RAW make the jail's own podman's bridge
            # networking work: the jail's podman runs as jail-root without a
            # second userns, so netavark's netlink ops (and even loopback
            # SIOCSIFFLAGS for --network=none) are capability-checked against
            # the jail's userns.  Scoped to the jail's private netns — the
            # host netns is owned by the init userns and stays out of reach.
            # /dev/net/tun (best-effort) additionally enables pasta/
            # slirp4netns rootless networking in the jail, and passing it
            # here is what lets nested jails inherit it.
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
                    "--cap-add",
                    "NET_ADMIN",
                    "--cap-add",
                    "NET_RAW",
                ]
            )
            if Path("/dev/net/tun").exists():
                run_cmd.extend(["--device", "/dev/net/tun"])

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
            # A single-file :ro mount whose source may itself be a bind
            # mountpoint in a nested jail (the outer jail bind-mounted this
            # exact file) — dereference it so rootless podman can bind it.
            run_cmd.extend(
                _ro_file_mount_arg(
                    excludes_path,
                    "/home/agent/.config/git/ignore",
                    ws_state,
                    ".config/git/ignore",
                    mount_targets,
                )
            )
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
        # BEFORE the relay spawn so the relay has a live upstream from
        # its first connection.  ``start_loopholes`` (called later) also
        # calls _broker_ensure for idempotence.
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
            # Per-jail relay, both platforms.  It listens inside
            # host_services_sockets_dir — already visible in-jail at
            # {JAIL_HOST_SERVICES_DIR}/{BROKER_LOOPHOLE_NAME}.sock through
            # the directory mount above, no extra -v flag — and dials the
            # singleton per connection.  macOS: Podman Machine cannot
            # bind-mount a Unix socket *file* (EOPNOTSUPP).  Linux: a
            # socket-file bind mount pins the inode, so a broker restart
            # left every running jail holding the dead socket
            # (Connection refused / 502 until relaunch).
            _ensure_broker_relay(cname, runtime)
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

    # GPU passthrough from config (NVIDIA via podman+CDI; AMD/ROCm via raw
    # device nodes or amd.com/gpu CDI).  Availability already probed above —
    # gpu_enabled reflects "requested AND present", gpu_unavailable_reason
    # carries the warning text when requested but not present.
    if gpu_requested and not gpu_enabled:
        console.print(
            f"[yellow]Warning: GPU requested but {gpu_unavailable_reason} — "
            "starting without GPU passthrough[/yellow]"
        )
    if gpu_enabled:
        # Raise the container's locked-memory *soft* limit to the host's hard
        # cap for GPU passthrough.  GPU runtimes pin (mlock) device queue ring
        # buffers, and ROCm/CUDA check the soft RLIMIT_MEMLOCK; lifting soft to
        # the host ceiling gives them the most a rootless container can have.
        #
        # We clamp to the host hard cap rather than blindly requesting
        # `memlock=-1`: a rootless container cannot raise the *hard* limit
        # above the host's, and on some crun/host combinations requesting a
        # finite value above the cap is rejected with EPERM at container start.
        # Requesting exactly the host hard cap always succeeds.  yolo runs on
        # the host, so resource.getrlimit here sees the real ceiling.
        #
        # Note: current ROCm userspace (verified on gfx1151 / ROCm 7.2) runs
        # GPU compute fine at the common 8 MB rootless cap — no host change is
        # required.  (Older ROCm builds pinned a larger queue buffer; if a
        # workload ever hits AMDKFD_IOC_CREATE_QUEUE EINVAL, raising the host
        # cap via limits.conf / systemd LimitMEMLOCK / podman containers.conf
        # default_ulimits is the remedy — but it is not needed by default.)
        _, _host_hard_memlock = resource.getrlimit(resource.RLIMIT_MEMLOCK)
        if _host_hard_memlock == resource.RLIM_INFINITY:
            run_cmd.extend(["--ulimit", "memlock=-1:-1"])
        else:
            run_cmd.extend(
                ["--ulimit", f"memlock={_host_hard_memlock}:{_host_hard_memlock}"]
            )
    if gpu_enabled and gpu_vendor == "nvidia":
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
    elif gpu_enabled and gpu_vendor == "amd":
        gpu_config = config.get("gpu", {})
        gpu_devices = gpu_config.get("devices", "all")
        gpu_mode = gpu_config.get("mode", "devices")

        if gpu_mode == "cdi":
            # AMD CDI: amd.com/gpu=all | amd.com/gpu=N  (spec at /etc/cdi/amd.json)
            if gpu_devices == "all":
                run_cmd.extend(["--device", "amd.com/gpu=all"])
            else:
                for idx in gpu_devices.split(","):
                    run_cmd.extend(["--device", f"amd.com/gpu={idx.strip()}"])
        else:
            # Default: raw device nodes (no host toolkit needed).
            # /dev/kfd is the shared compute interface and is ALWAYS required.
            if Path("/dev/kfd").exists():
                run_cmd.extend(["--device", "/dev/kfd"])
            if gpu_devices == "all":
                run_cmd.extend(["--device", "/dev/dri"])
            else:
                for idx in gpu_devices.split(","):
                    node = Path(f"/dev/dri/renderD{128 + int(idx.strip())}")
                    if node.exists():
                        run_cmd.extend(["--device", str(node)])

        # Rootless podman drops supplementary groups; keep-groups (crun-only)
        # preserves the host render/video GID so /dev/kfd is openable.  This
        # is REQUIRED for both modes and is why AMD stays on crun (not runc).
        if runtime == "podman":
            run_cmd.extend(["--group-add", "keep-groups"])

        # ROCm in-container selectors (NOT a security boundary).  No
        # NVIDIA_DRIVER_CAPABILITIES analog exists — omit it.  Unlike
        # NVIDIA's NVIDIA_VISIBLE_DEVICES, the ROCr/HSA selector does NOT
        # accept the literal "all": setting ROCR_VISIBLE_DEVICES=all matches
        # no device and hides every GPU (verified on gfx1151 — torch.cuda
        # goes False).  When devices=="all" we therefore leave these env
        # vars UNSET, which is ROCm's "all GPUs visible" default; we only
        # set explicit indices/UUIDs for a non-"all" selection.
        if gpu_devices != "all":
            run_cmd.extend(
                [
                    "-e",
                    f"ROCR_VISIBLE_DEVICES={gpu_devices}",
                    "-e",
                    f"HIP_VISIBLE_DEVICES={gpu_devices}",
                ]
            )
        gfx = gpu_config.get("hsa_override_gfx_version")
        if gfx:
            run_cmd.extend(["-e", f"HSA_OVERRIDE_GFX_VERSION={gfx}"])
        if gpu_config.get("seccomp_unconfined") is True:
            run_cmd.extend(["--security-opt", "seccomp=unconfined"])

        # VAAPI (video encode/decode accel).  gpu.vaapi bakes mesa +
        # libva-utils into the image (see _effective_packages); the
        # radeonsi driver lands at /lib/dri via the image contents merge,
        # which is NOT on libva's compiled-in search path
        # (/run/opengl-driver/lib/dri:/usr/lib/dri:...) — point
        # LIBVA_DRIVERS_PATH at it.  libva auto-derives the driver name
        # (amdgpu → radeonsi) from the render node, so no
        # LIBVA_DRIVER_NAME needed.  Device + group access is already
        # covered by the ROCm flags above (/dev/dri + keep-groups).
        if gpu_config.get("vaapi") is True:
            run_cmd.extend(["-e", "LIBVA_DRIVERS_PATH=/lib/dri:/usr/lib/dri"])

        console.print(
            f"[dim]ROCm passthrough (mode={gpu_mode}): devices={gpu_devices}"
            + (", vaapi" if gpu_config.get("vaapi") is True else "")
            + "[/dim]"
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
    run_cmd.extend(_workspace_readonly_mount_args(workspace, config, runtime))

    # Per-side venv shadows — mounted after the rw workspace volume so
    # each jail sees its own backing at /workspace/.venv (and friends)
    # while the host keeps its own.  See _venv_shadow_mount_args.
    run_cmd.extend(_venv_shadow_mount_args(workspace, ws_state, config))

    # Mount user-level yolo config so nested jails see the same merged config.
    # Without this, ~/.config/ is an empty per-workspace overlay and the nested
    # yolo resolves to empty config, stomping the host's config snapshot.
    if USER_CONFIG_PATH.is_file():
        container_config = f"/home/agent/.config/yolo-jail/{USER_CONFIG_PATH.name}"
        rel_config = f".config/yolo-jail/{USER_CONFIG_PATH.name}"
        if runtime == "container":
            _ac_materialize_under_ws_state(USER_CONFIG_PATH, rel_config, ws_state)
        else:
            # Dereference if the source is itself a bind mountpoint (nested
            # jail — the outer jail already bind-mounted this config file).
            run_cmd.extend(
                _ro_file_mount_arg(
                    USER_CONFIG_PATH,
                    container_config,
                    ws_state,
                    rel_config,
                    mount_targets,
                )
            )

    run_cmd.extend(["-e", f"MISE_DISABLE_TOOLS={mise_disabled_tools}"])

    # Store-prune gate: a dangling symlink in the shared jail store can be
    # live for a sibling jail (same path string, per-jail backing), so the
    # in-jail prune is only allowed once the host CLI has proved no other
    # jail is running.  Never granted from inside a jail — an inner CLI
    # can't see its siblings.  TOCTOU: a jail launching between this check
    # and the prune could lose an entry mid-boot; accepted — mise relinks
    # it on that jail's next install.
    if os.environ.get("YOLO_VERSION") is None:
        live_jails = _live_yolo_containers(runtime)
        if live_jails == set():
            run_cmd.extend(["-e", "YOLO_STORE_PRUNE_OK=1"])
        # Backstop reap of orphaned per-jail broker relays (piggybacks on
        # the enumeration above): a relay outlives the yolo process that
        # spawned it by design, and stop_loopholes only runs in that
        # original process's graceful tail — jails ended from attach
        # sessions leave their relay running forever otherwise.  The
        # current jail's relay (just ensured, container not started yet)
        # is excluded by name; None (liveness unknown) declines inside.
        if live_jails is not None:
            try:
                _relay_reap_orphans(live_jails | {cname})
            except Exception:  # noqa: BLE001 — cleanup must never block a run
                pass

    # Mount merged skills directories read-only, one per SELECTED agent that
    # has a user-skills dir (copilot/gemini/claude; opencode/pi have none).
    # The staging dir was already rebuilt at the top of `run` by
    # `_refresh_jail_briefings` (same dir as `agents_path` — both resolve to
    # AGENTS_DIR/cname).  Kernel-enforced :ro — agents get "Read-only file
    # system" on write attempts.
    for spec in agent_specs:
        if spec.skills:
            run_cmd.extend(
                [
                    "-v",
                    f"{agents_path / spec.skills_staging}:/home/agent/{spec.skills}:ro",
                ]
            )

    # Mount host ~/.claude/ files for syncing into the jail (claude only).
    # Auto-discover scripts referenced in host settings.json (fileSuggestion,
    # statusLine, hooks) and include them if they live under ~/.claude/.
    host_claude_dir = Path.home() / ".claude"
    effective_claude_files = list(host_claude_files) if "claude" in agents else []
    host_settings_file = host_claude_dir / "settings.json"
    if "claude" in agents and host_settings_file.exists():
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
            # Dereference if the source is itself a bind mountpoint (nested
            # jail).  Staged under ctx-host-claude/ in ws_state so the copy
            # doesn't collide with the agent's own overlay files.
            run_cmd.extend(
                _ro_file_mount_arg(
                    host_file,
                    f"/ctx/host-claude/{fname}",
                    ws_state,
                    f"ctx-host-claude/{fname}",
                    mount_targets,
                )
            )
            mounted_claude_files.append(fname)
    if mounted_claude_files:
        run_cmd.extend(
            ["-e", f"YOLO_HOST_CLAUDE_FILES={json.dumps(mounted_claude_files)}"]
        )

    # Per-workspace briefing files already generated at the top of `run` by
    # `_refresh_jail_briefings` (so attach-to-running picks up host
    # edits/deletions too).  Mount each SELECTED agent's staged briefing at
    # its in-jail read path (registry ``briefing.staging`` → ``briefing.mount``;
    # e.g. CLAUDE.md → .claude/CLAUDE.md, AGENTS-copilot.md → .copilot/AGENTS.md).
    for spec in agent_specs:
        staged = agents_path / spec.briefing.staging
        if runtime == "container":
            _ac_materialize_under_ws_state(staged, spec.briefing.mount, ws_state)
        else:
            run_cmd.extend(["-v", f"{staged}:/home/agent/{spec.briefing.mount}:ro"])

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

    # Trust the workspace mise configs (mise.toml AND .mise.toml; --all also
    # covers parent dirs — blanket subtree trust comes from
    # MISE_TRUSTED_CONFIG_PATHS in the env block, this hook is
    # belt-and-suspenders), then ensure all tools (global + local) are ready.
    # --quiet on mise trust suppresses "No untrusted config files found".
    # The inner wrapper is POSIX sh — no PIPESTATUS in here.
    setup_script = (
        "YOLO_BYPASS_SHIMS=1 sh -c '"
        "(mise trust --all --quiet 2>/dev/null || true) && "
        # Store hygiene: entries like installs/rust/<v> → /workspace/.cargo
        # dangle in jails whose workspace lacks the target.  Removal is only
        # safe with zero sibling jails — the host CLI grants that via
        # YOLO_STORE_PRUNE_OK (see the run() gate).  Output lands in the
        # startup log via the tee wrapper below.
        'if [ "${YOLO_STORE_PRUNE_OK:-0}" = "1" ]; then '
        'for _p in "$MISE_DATA_DIR"/installs/*/*; do '
        'if [ -L "$_p" ] && [ ! -e "$_p" ]; then '
        'rm -f -- "$_p" && echo "  ↳ pruned dangling store symlink: $_p" >&2; '
        "fi; done; fi && "
        'echo "  ↳ mise install" >&2 && '
        "mise install --quiet && "
        'echo "  ↳ mise upgrade" >&2 && '
        # Capture the real upgrade status — piping straight into grep/sed
        # (to hide "mise WARN" deprecation noise) would report the filter
        # status and swallow upgrade failures.
        "{ mise upgrade --yes >/tmp/yolo-mise-upgrade.out 2>&1; _urc=$?; "
        'grep -v "^mise WARN" /tmp/yolo-mise-upgrade.out | sed "s/^/    /" >&2; '
        '[ "$_urc" -eq 0 ]; } && '
        'echo "  ↳ bootstrap" >&2 && '
        "~/.yolo-bootstrap.sh >&2 && "
        "~/.yolo-venv-precreate.sh >&2'"
    )
    # Provisioning wrapper: tee everything into /workspace/.yolo/startup.log
    # (fresh per container, timestamped header) while still streaming to
    # stderr.  On failure: the PROVISIONING FAILED marker in the log, a red
    # banner naming the log, then a continue/abort prompt (tty only;
    # YOLO_PROVISION_PROMPT=0 bypasses; default answer = continue, n
    # aborts with the provisioning exit code).  The outer shell is bash
    # (the entrypoint execs bash -c), so PIPESTATUS is available here.
    startup_log = "/workspace/.yolo/startup.log"
    provision_script = (
        'printf "=== yolo provisioning %s ===\\n" "$(date "+%Y-%m-%dT%H:%M:%S%z")" '
        f">{startup_log}; "
        f"({setup_script}) 2>&1 | tee -a {startup_log} >&2; "
        '_prc="${PIPESTATUS[0]}"; '
        'if [ "$_prc" -ne 0 ]; then '
        f'printf "PROVISIONING FAILED (exit %s)\\n" "$_prc" >>{startup_log}; '
        'printf "\\033[1;31m✗ Provisioning failed (exit %s) — log: '
        f'{startup_log}\\033[0m\\n" "$_prc" >&2; '
        'if [ -t 0 ] && [ "${YOLO_PROVISION_PROMPT:-1}" != "0" ]; then '
        'printf "Provisioning failed — continue anyway? [Y/n] " >&2; '
        'read -r _ans; case "$_ans" in [nN]*) exit "$_prc";; esac; '
        "fi; fi"
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

    # provision_script owns failure handling (log marker + banner +
    # continue/abort prompt); on continue, target_cmd still runs with the
    # best-effort environment.
    if profile:
        # Wrap each phase with timing output for profiling
        final_internal_cmd = (
            "exec 3>&2; "  # save stderr
            "printf '\\033[2m📦 Provisioning tools...\\033[0m\\n' >&2; "
            f"_t0=$(date +%s%N); {provision_script}; "
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
        # Provisioning message → bootstrap (logged/gated) → activate →
        # ready → command
        final_internal_cmd = (
            "printf '\\033[2m📦 Provisioning tools...\\033[0m\\n' >&2; "
            f"{provision_script}; "
            f"{mise_activate}; "
            f"printf '\\033[1;36m⚡ Executing: {display_cmd}\\033[0m\\n' >&2; "
            f"{target_cmd}"
        )

    write_container_tracking(cname, workspace)
    # We own this jail (started it with --rm).  Record our PID so a later
    # yolo can reap it if we're SIGKILLed / the host crashes before the
    # proxy's teardown handler can stop it.
    _write_owner_pid(cname)
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

    # Wrap the container launch in run_with_proxy so a host-side ^Z
    # suspends the proxy (host shell shows it as a stopped job)
    # instead of wedging claude inside the jail.  See cli/tty_proxy.py
    # for the why.  The on_started callback releases the workspace
    # lock once the container is visible — any concurrent yolo
    # process waiting on the lock then exec's into our container.
    def _release_lock_when_started(_proc):
        for _ in range(LOCK_RELEASE_POLL_ATTEMPTS):
            if find_running_container(cname, runtime=runtime):
                break
            _time.sleep(LOCK_RELEASE_POLL_INTERVAL_SECONDS)
        lock_file.close()

    # Host-side teardown for window close (SIGHUP) / kill (SIGTERM).  The
    # proxy would otherwise die WITHOUT stopping the container (conmon
    # supervises PID 1 independently), orphaning the jail.  As its owner
    # we stop it — --rm then removes it — and tear down the same host-side
    # services the normal-exit path below cleans up.  Best-effort: this
    # runs from a signal handler, on the way out.
    def _on_terminate():
        _stop_jail(cname, runtime)
        cleanup_port_forwarding(socat_procs, socket_dir)
        try:
            lock_file.close()
        except Exception:
            pass
        stop_loopholes(
            host_services, host_services_sockets_dir, cname=cname, runtime=runtime
        )

    try:
        rc = run_with_proxy(
            run_cmd,
            on_started=_release_lock_when_started,
            on_terminate=_on_terminate,
        )
    except FileNotFoundError:
        console.print(
            f"[bold red]Configured runtime '{runtime}' not found on PATH.[/bold red]"
        )
        console.print(
            "[dim]Run `yolo check` to validate runtime availability before restarting.[/dim]"
        )
        cleanup_port_forwarding(socat_procs, socket_dir)
        # Release the workspace lock BEFORE stop_loopholes: its cleanup
        # guard takes the same lock non-blocking, and we still hold it
        # here (on_started never ran — the runtime binary is missing).
        lock_file.close()
        stop_loopholes(
            host_services, host_services_sockets_dir, cname=cname, runtime=runtime
        )
        _clear_owner_pid(cname)
        sys.exit(1)
    # Clean up host-side socat processes, host services (incl. cgroup
    # delegate), and their per-jail socket directory.  cname/runtime arm
    # stop_loopholes' guards: never reap the relay or rmtree the mounted
    # sockets dir under a container that is still running (e.g. a
    # `--new` name-conflict launch failure while the old jail lives on),
    # and never race a concurrent relaunch that is mid-spawn.
    cleanup_port_forwarding(socat_procs, socket_dir)
    stop_loopholes(
        host_services, host_services_sockets_dir, cname=cname, runtime=runtime
    )
    # The container exited on its own (--rm removed it); drop our
    # owner-PID marker so a later yolo doesn't try to reap a dead name.
    _clear_owner_pid(cname)

    _maybe_warn_about_oom_killer(rc, runtime)

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

    sys.exit(rc)


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


def doctor(
    build: bool = typer.Option(
        True,
        "--build/--no-build",
        help="Run nix build as part of the preflight (default: on)",
    ),
):
    """Alias for 'check'. Validate environment, config, and build."""
    # Late import — check_cmd lives in cli.check_cmd, but doctor used to
    # be defined in __init__.py where ``check`` was a module-local name.
    from .check_cmd import check

    check(build=build)
