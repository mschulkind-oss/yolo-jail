"""Native macOS backend: isolate an agent in a dedicated user + Seatbelt.

``runtime: "macos-user"`` runs the agent as arm64-native macOS binaries in
a dedicated, hidden, unprivileged user account hardened with an Apple
Seatbelt (``sandbox-exec``) profile — no Linux container, no VM, no arch
switch.  It is the yolo-jail port of SandVault's design
(github.com/webcoyote/sandvault); we deliberately match SandVault's
security posture so there is a concrete standard to point at.  See
``docs/design/macos-no-vm-direction.md`` for the honest security delta vs. the
container backend (weaker: shared kernel, deprecated sandbox-exec, no
resource caps) and why it's opt-in only.  ``packages:`` is materialized as
native aarch64-darwin nix — see ``src/cli/darwin_packages.py``.

**Design of this module.**  Everything that produces an *artifact* — the
account-provisioning command lists, the workspace ACL grant, the Seatbelt
profile text, the launch argv, the in-process entrypoint bootstrap — is a
**pure function** returning data, so it is fully unit-testable on Linux
(this repo's CI host) without a Mac.  Only :func:`run_macos_user` and the
handful of ``_run``/``_query`` helpers actually shell out, and those are
guarded to macOS.  This mirrors how ``run_cmd.py`` builds the podman argv
as data before executing it.
"""

import os
import shutil
import subprocess
import sys
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Dict, List, Optional

# Dedicated account.  A hidden service account (``_`` prefix + IsHidden) so
# it never shows on the login window, mirroring SandVault's hidden user.
# The host user is added to SANDBOX_GROUP so the inheriting workspace ACL
# grants both sides rw on the same inodes.
SANDBOX_USER = "_yolojail"
SANDBOX_GROUP = "_yolojail"
SANDBOX_HOME = Path("/Users") / SANDBOX_USER

# Where a shared workspace must live: a NEUTRAL directory outside every user's
# home.  This is the crux of the model's "clear semantics" — the workspace is
# shared host<->sandbox live (same inodes) via one flat inheriting ACL on this
# tree, and NO access-control grant is ever threaded through anyone's home
# directory.  ``/Users/Shared`` exists on every Mac, is a sibling of (never
# nested under) the host home, and its name makes the sharing self-evident —
# nobody drops an ssh key in a dir called "Shared" by accident.  The default
# root is overridable (config ``macos_shared_root``) to any non-home path
# (e.g. an external volume), but it can NEVER be inside a home.
SHARED_ROOT_DEFAULT = Path("/Users/Shared/yolo")
# UID/GID floor for the auto-picked free id (SandVault uses 600; macOS
# hides sub-500 accounts but 500+ service accounts + IsHidden is the safe,
# collision-free range).
SANDBOX_MIN_ID = 600
# Root-owned, 0444 state dir holding the per-session Seatbelt profile, the
# entrypoint bootstrap the sandbox user runs, and a root-owned copy of the
# stdlib-only ``entrypoint`` package.  Root-owned so the sandbox user cannot
# rewrite its own sandbox profile — and world-readable so it can *import*
# the staged entrypoint (the host checkout may sit behind a 0750 home the
# sandbox uid can't traverse).
STATE_DIR = Path("/var/yolo-jail")

# Absolute paths to the system tools the run path invokes under sudo — pinned
# so the argv is deterministic regardless of the caller's PATH.  We do NOT
# install a passwordless-sudo rule: changing the host's sudo policy is the
# user's call, not ours (SandVault prompts every run for the same reason).
# The launch runs under the TTY proxy, which forwards stdin, so sudo's
# password prompt is answerable inline.
MKDIR = "/bin/mkdir"
TEE = "/usr/bin/tee"
CHMOD = "/bin/chmod"
CP = "/bin/cp"

# Candidate python3 interpreters for the sandbox user, best first.  The bare
# ``/usr/bin/python3`` is LAST and only a fallback: with the Command Line
# Tools absent it is the xcode-select *stub*, which triggers a GUI install
# flow (or errors headless) instead of running Python — a service account
# can't authorize that install, so the bootstrap would never run.  A real
# Homebrew/Nix python3 is preferred.
_PYTHON_CANDIDATES = (
    "/opt/homebrew/bin/python3",  # arm64 Homebrew (0755, world-runnable)
    "/usr/local/bin/python3",  # Intel Homebrew / python.org
    "/usr/bin/python3",  # CLT/system — LAST (may be the xcode-select stub)
)


# ---------------------------------------------------------------------------
# Account provisioning — command lists (pure; executed by the orchestrator)
# ---------------------------------------------------------------------------


def create_user_commands(
    uid: int,
    gid: int,
    *,
    host_user: str,
    user: str = SANDBOX_USER,
    group: str = SANDBOX_GROUP,
    home: str = str(SANDBOX_HOME),
) -> List[List[str]]:
    """``dscl``/``dseditgroup`` argv to create the hidden sandbox account.

    Mirrors SandVault: create the group + user via ``dscl``, hide it
    (``IsHidden 1``), strip it from ``staff`` so it isn't a real login
    user, add both it and the host user to SANDBOX_GROUP (so the shared
    workspace ACL works both ways), and provision its home.  The random
    password is set by the orchestrator via ``dscl . -passwd`` with a value
    read from ``openssl rand`` — never a literal argv (it would show in
    ``ps``), so it is intentionally NOT in this list.
    """
    return [
        # Group
        ["dscl", ".", "-create", f"/Groups/{group}"],
        ["dscl", ".", "-create", f"/Groups/{group}", "PrimaryGroupID", str(gid)],
        ["dscl", ".", "-create", f"/Groups/{group}", "RealName", "YOLO Jail"],
        # User
        ["dscl", ".", "-create", f"/Users/{user}"],
        ["dscl", ".", "-create", f"/Users/{user}", "UniqueID", str(uid)],
        ["dscl", ".", "-create", f"/Users/{user}", "PrimaryGroupID", str(gid)],
        ["dscl", ".", "-create", f"/Users/{user}", "RealName", "YOLO Jail"],
        ["dscl", ".", "-create", f"/Users/{user}", "NFSHomeDirectory", home],
        ["dscl", ".", "-create", f"/Users/{user}", "UserShell", "/bin/zsh"],
        # Hidden from the login window
        ["dscl", ".", "-create", f"/Users/{user}", "IsHidden", "1"],
        # Not a real login user: strip from staff
        ["dseditgroup", "-o", "edit", "-d", user, "-t", "user", "staff"],
        # Shared group membership (host user + sandbox user) for the ACL
        ["dseditgroup", "-o", "edit", "-a", user, "-t", "user", group],
        ["dseditgroup", "-o", "edit", "-a", host_user, "-t", "user", group],
        # Provision the home dir with correct ownership + 0750 (SandVault:
        # home is owned+writable by the untrusted sandbox user, 0750 so the
        # group can traverse but the world cannot).
        ["createhomedir", "-c", "-u", user],
        ["chown", "-R", f"{user}:{group}", home],
        ["chmod", "750", home],
    ]


def delete_user_commands(
    *,
    host_user: str,
    user: str = SANDBOX_USER,
    group: str = SANDBOX_GROUP,
    home: str = str(SANDBOX_HOME),
) -> List[List[str]]:
    """``launchctl``/``dscl`` argv to tear the sandbox account down.

    Boots out any running session, removes group memberships, deletes the
    user + group records, and removes the home.  Mirrors SandVault's
    uninstall path.  Home removal is last so a failed earlier step doesn't
    orphan a live session's files.
    """
    return [
        ["dseditgroup", "-o", "edit", "-d", host_user, "-t", "user", group],
        ["dscl", ".", "-delete", f"/Users/{user}"],
        ["dscl", ".", "-delete", f"/Groups/{group}"],
        ["rm", "-rf", home],
    ]


def shared_root_provision_commands(
    root: Path = SHARED_ROOT_DEFAULT,
    *,
    host_user: str,
    group: str = SANDBOX_GROUP,
) -> List[List[str]]:
    """``mkdir``/``chown``/``chmod`` argv to provision the neutral shared root.

    The root (default ``/Users/Shared/yolo``) is owned by the host user, group
    ``_yolojail``, mode ``2770`` — setgid so a project the host user creates
    under it inherits the shared group, ``rwx`` for owner+group, and NO access
    for "other".

    Crucially it also gets the **inheriting** ACL ACEs (dir rights + the
    file-inherit template) applied to the root *itself*.  Because the root is
    provisioned empty and macOS applies inheritable ACEs to everything created
    underneath at create-time, **every project and file the agent or host
    later creates under the root inherits the shared-group grant for free** —
    with NO per-run tree walk.  The only files that miss the ACE are
    pre-existing inodes *moved/preserve-copied in* (rename/`cp -p` don't
    re-trigger inheritance); those are handled on demand by
    ``yolo macos-fix-permissions`` (:func:`fix_permissions_script`), not on the
    hot path.

    Idempotent (``mkdir -p``; re-adding an identical ACE / re-``chmod`` is a
    no-op).  Pure — executed with ``sudo`` by the caller.
    """
    r = str(root)
    aces = workspace_acl_aces(group)
    return [
        ["mkdir", "-p", r],
        ["chown", f"{host_user}:{group}", r],
        # setgid (2) so new subdirs inherit the shared group; 770 = owner+group
        # rwx, other none.  "Shared" in the name makes the sharing self-evident.
        ["chmod", "2770", r],
        # The inheriting ACEs on the root itself — children inherit at create
        # time, so no per-run walk is ever needed.
        ["chmod", "+a", aces["dir"], r],
        ["chmod", "+a", aces["file_inherit"], r],
    ]


# ---------------------------------------------------------------------------
# Interpreter resolution — never blindly trust /usr/bin/python3
# ---------------------------------------------------------------------------


def python_candidates() -> List[str]:
    """Ordered python3 candidates for the sandbox user, best first.

    Pure (returns the static preference list); the caller filters to what
    actually exists.  ``/usr/bin/python3`` is intentionally last — it is the
    xcode-select stub when the Command Line Tools are absent, which cannot
    run as a service account.
    """
    return list(_PYTHON_CANDIDATES)


def resolve_python(exists=os.path.exists) -> Optional[str]:
    """First existing candidate interpreter, or ``None`` if none exist.

    ``exists`` is injectable so a Linux test can assert the ordering (a real
    Homebrew/Nix python3 wins over the bare ``/usr/bin/python3`` stub)
    without any of these paths existing on the CI host.
    """
    for cand in python_candidates():
        if exists(cand):
            return cand
    return None


# ---------------------------------------------------------------------------
# Staging the entrypoint package into the root-owned state dir
# ---------------------------------------------------------------------------


def staged_entrypoint_dir(state_dir: Path = STATE_DIR) -> Path:
    """Where the stdlib-only ``entrypoint`` package is staged (importable)."""
    return state_dir / "entrypoint"


def stage_entrypoint_commands(
    repo_src: Path, *, state_dir: Path = STATE_DIR
) -> List[List[str]]:
    """``sudo`` argv copying ``entrypoint`` into the root-owned state dir.

    The bootstrap runs as the sandbox uid, which cannot import ``entrypoint``
    from the host checkout: the credential-hiding step (``chmod 750 ~`` on the
    host home) removes the other-execute bit the non-staff sandbox user would
    need to traverse into the checkout.  So we copy the package (root-owned,
    world-readable) into ``/var/yolo-jail`` — matching the existing root-owned
    STATE_DIR model — and the bootstrap imports it from there instead.  The
    copy is refreshed every run so edits to the entrypoint take effect.
    """
    src = repo_src / "entrypoint"
    dst = staged_entrypoint_dir(state_dir)
    return [
        [MKDIR, "-p", str(state_dir)],
        [CP, "-R", f"{src}/.", str(dst)],
        # World-readable, dirs traversable; root-owned so the sandbox can't
        # rewrite the code it's about to run.
        [CHMOD, "-R", "a+rX", str(dst)],
    ]


# ---------------------------------------------------------------------------
# Workspace location — must be neutral ground, never inside a home
# ---------------------------------------------------------------------------


def home_containing(
    workspace: Path, users_root: Path = Path("/Users")
) -> Optional[Path]:
    """Return the user-home dir that contains ``workspace``, or ``None``.

    A "home" here is a direct child of ``/Users`` (``/Users/<name>``) other
    than ``/Users/Shared`` — i.e. ``/Users/matt`` but not ``/Users/Shared``.
    A workspace AT or UNDER such a dir is rejected by the run path: sharing
    it would require threading an access grant through someone's home, which
    is exactly the layered, error-prone posture this model exists to avoid.

    Pure and path-only (no filesystem access), so it's unit-testable on Linux
    and can't be fooled by symlinks-at-runtime — the caller resolves the path
    first.  Returns the offending home so the error can name it.
    """
    candidates = [workspace, *workspace.parents]
    for p in candidates:
        parent = p.parent
        if parent == users_root and p.name != "Shared":
            return p
    return None


# ---------------------------------------------------------------------------
# Workspace ACL — SandVault's dir/file-split inheriting ACEs
# ---------------------------------------------------------------------------

# Full rights a directory gets (includes search/list so traversal works).
# directory_inherit propagates to child dirs.
_DIR_RIGHTS = (
    "read,write,append,delete,delete_child,readattr,writeattr,readextattr,"
    "writeextattr,readsecurity,writesecurity,chown,search,list,directory_inherit"
)
# The inheritance template a directory also carries so NEW files inherit
# file-appropriate rights (no search/list = no execute-bit surprise),
# only_inherit so it doesn't apply to the dir itself.
_FILE_INHERIT_RIGHTS = (
    "read,write,append,delete,delete_child,readattr,writeattr,readextattr,"
    "writeextattr,readsecurity,writesecurity,chown,"
    "file_inherit,directory_inherit,only_inherit"
)
# Rights an existing plain file gets (no inherit flags, no search/list).
_FILE_RIGHTS = (
    "read,write,append,delete,delete_child,readattr,writeattr,readextattr,"
    "writeextattr,readsecurity,writesecurity,chown"
)


def workspace_acl_aces(group: str = SANDBOX_GROUP) -> Dict[str, str]:
    """The three ``chmod +a`` ACE strings (dir / file-inherit / file).

    Split so directories are searchable/listable and inherit correctly to
    children, while plain files never gain execute/search — SandVault's
    exact scheme, which sidesteps the umask-022 trap of a plain
    setgid-group share.  The dir + file-inherit ACEs are applied to the
    **shared root** at setup (:func:`shared_root_provision_commands`); the
    kernel then inherits them onto everything created underneath, so the hot
    path does no ACL work.  The bare file ACE is only used by the on-demand
    :func:`fix_permissions_script` retrofit for pre-existing moved-in files.
    """
    return {
        "dir": f"group:{group} allow {_DIR_RIGHTS}",
        "file_inherit": f"group:{group} allow {_FILE_INHERIT_RIGHTS}",
        "file": f"group:{group} allow {_FILE_RIGHTS}",
    }


def fix_permissions_script(root: Path, group: str = SANDBOX_GROUP) -> str:
    """A ``find``-based bash script that (re)applies the split ACEs to a tree.

    NOT on the hot path — this is the one-time retrofit behind
    ``yolo macos-fix-permissions``, for the rare case where **pre-existing**
    files were moved or preserve-copied into the shared area (rename / ``cp
    -p`` don't re-trigger inheritance, so those inodes lack the group ACE).
    Files *created* under the shared root inherit the ACE for free from the
    root (see :func:`shared_root_provision_commands`) and never need this.

    Batches with ``find … -exec chmod {} +`` (many paths per ``chmod``, not
    one fork per file) so even a large ``.venv`` retrofit is fast, and prints
    a note first so a multi-second pass on a huge tree doesn't look like a
    hang.  ``chmod -h`` so symlinks aren't followed.  Returned as text so it's
    unit-testable and run via ``bash -c`` on macOS.
    """
    aces = workspace_acl_aces(group)
    r = _sh_quote(str(root))
    return (
        "set -euo pipefail\n"
        f"root={r}\n"
        'echo "Applying shared-group ACLs under $root (this can take a '
        'moment on a large tree)…"\n'
        # Directories: dir rights + inheritance template, batched.
        f'find "$root" -type d -exec chmod -h +a {_sh_quote(aces["dir"])} {{}} +\n'
        f'find "$root" -type d -exec chmod -h +a {_sh_quote(aces["file_inherit"])} {{}} +\n'
        # Everything else (files, symlinks): bare file ACE, batched.
        f'find "$root" ! -type d -exec chmod -h +a {_sh_quote(aces["file"])} {{}} +\n'
        'echo "Done."\n'
    )


def workspace_acl_strip_script(workspace: Path) -> str:
    """A ``find``-based bash script that removes ALL ACLs from the workspace.

    The clean-teardown primitive: ``chmod -h -N`` strips the entire ACL from
    each inode, returning the tree to plain POSIX permissions with no
    lingering ``group:_yolojail`` entries.  Because we only ever ACL neutral
    ground (never a home), this provably leaves nothing of the user's own
    outside the workspace touched.  Run by ``yolo macos-unshare <workspace>``.
    """
    return (
        "set -euo pipefail\n"
        f"ws={_sh_quote(str(workspace))}\n"
        'find "$ws" -exec chmod -h -N {} +\n'
    )


# ---------------------------------------------------------------------------
# Seatbelt (sandbox-exec) profile — SandVault's (allow default) + denies
# ---------------------------------------------------------------------------


def seatbelt_profile(workspace: Path, sandbox_home: Path = SANDBOX_HOME) -> str:
    """Generate the SBPL sandbox profile, matching SandVault's structure.

    Base is ``(allow default)`` with targeted denies — SandVault's model,
    replicated so we match its guarantees:

    * deny all writes, then re-allow the workspace, the sandbox home, and
      the scratch dirs an agent needs (/tmp, /var/folders, /dev);
    * deny reads under /Volumes except the boot volume;
    * deny raw disk + packet-capture devices (/dev/rdisk*, /dev/bpf);
    * deny reads under /Users (other users' homes), then re-allow the
      directory-entry lookups needed for traversal plus the workspace and
      sandbox home;
    * deny reads of /Library/Keychains — load-bearing, because
      System.keychain is world-readable (0644) on stock macOS.

    Seatbelt is last-match-wins, so re-allows follow their denies.  Network
    is NOT restricted (``allow default`` covers it) — matching SandVault,
    which does not filter egress; egress control is a documented follow-up,
    not part of the SandVault-parity baseline.

    The workspace lives on neutral ground outside every home
    (:func:`home_containing` enforces this), so there are NO per-ancestor
    grants: the ``/Users`` read-deny re-allows only the ``/Users`` and
    ``/Users/Shared`` directory-entry lookups needed to traverse to a
    ``/Users/Shared/...`` workspace, plus the workspace and sandbox-home
    subtrees.  A workspace under a non-``/Users`` root (e.g. an external
    volume) is reachable for free — only ``/Users`` is read-denied.
    """
    ws = _sbpl_str(str(workspace))
    home = _sbpl_str(str(sandbox_home))
    return f"""\
(version 1)
;; yolo-jail macOS-user sandbox profile — SandVault-parity.
;; Base allow with targeted denies; last match wins.
(allow default)

;; --- Writes: deny everywhere, then re-allow the agent's writable set ---
(deny file-write* (subpath "/"))
(allow file-write*
    (subpath {ws})
    (subpath {home})
    (subpath "/tmp")
    (subpath "/private/tmp")
    (subpath "/var/folders")
    (subpath "/private/var/folders")
    (subpath "/dev"))

;; --- Volumes: deny reads except the boot volume ---
(deny file-read* (subpath "/Volumes"))
(allow file-read* (subpath "/Volumes/Macintosh HD"))

;; --- Raw disk + packet capture: never ---
(deny file-read* file-write*
    (regex #"^/dev/r?disk")
    (regex #"^/private/dev/r?disk")
    (regex #"^/dev/bpf"))

;; --- Other users' homes: deny reads under /Users, re-allow the traversal
;;     entries + the (neutral, non-home) workspace + this sandbox user's own
;;     home.  The workspace is NOT under any /Users/<name> home, so no
;;     ancestor grant is needed. ---
(deny file-read* (subpath "/Users"))
(allow file-read*
    (literal "/Users")
    (literal "/Users/Shared")
    (subpath {ws})
    (subpath {home}))

;; --- Keychains: System.keychain is world-readable (0644) on stock
;;     macOS, so this deny is load-bearing ---
(deny file-read* (subpath "/Library/Keychains"))

;; --- Process introspection the agent's tooling needs ---
(allow process-info*)
(allow sysctl-read)
"""


# ---------------------------------------------------------------------------
# Launch — sudo -u + env -i + sandbox-exec, SandVault-style
# ---------------------------------------------------------------------------


def sandbox_path(home: Path = SANDBOX_HOME, prefix: Optional[List[str]] = None) -> str:
    """PATH for the sandboxed agent — its own bin dirs first, then system.

    Mirrors the jail's PATH ordering (shims → .local/bin → npm-global →
    mise shims → system) so the entrypoint-generated agent launchers
    (``~/.yolo-shims/claude`` etc.) and mise-managed tools are found, then
    system binaries.  Without this the scrubbed ``env -i`` PATH wouldn't
    include the agent binaries at all.

    ``prefix`` (the native darwin ``packages:`` store bin dirs) is inserted
    AFTER the agent/tool dirs but BEFORE the system dirs — so declared
    packages shadow ``/usr/bin`` while the agent shim launchers still win.
    """
    h = str(home)
    return ":".join(
        [
            f"{h}/.yolo-shims",
            f"{h}/.local/bin",
            f"{h}/.npm-global/bin",
            f"{h}/.local/share/mise/shims",
            f"{h}/go/bin",
            *(prefix or []),
            "/usr/bin",
            "/bin",
            "/usr/sbin",
            "/sbin",
        ]
    )


def launch_argv(
    agent_argv: List[str],
    *,
    profile_path: Path,
    sandbox_env: Dict[str, str],
    workspace: Path,
    user: str = SANDBOX_USER,
    home: Path = SANDBOX_HOME,
    path_prefix: Optional[List[str]] = None,
) -> List[str]:
    """Build the ``sudo -u … env -i … sandbox-exec -f … -- <agent>`` argv.

    Workspace-centric, matching the container backend's semantics: the
    agent starts **cd'd into the workspace** (``sudo --login`` would
    otherwise drop it in the sandbox home), and PATH leads with the sandbox
    user's own bin dirs so the entrypoint-generated agent launchers are
    found.

    ``env -i`` is load-bearing: without a scrubbed env, HOME still resolves
    to the *admin* home and the agent reads the host user's
    ``~/.gitconfig``/``~/.ssh`` — the #1 documented footgun.  ``sudo -u``
    (not ``launchctl asuser``) preserves the controlling TTY so the agent
    REPL works interactively.  ``sandbox_env`` is layered after the fixed
    identity vars; the HOME/USER/SHELL/PATH quartet is not caller-overridable
    (a caller could otherwise drop HOME or shadow the agent PATH).
    """
    protected = ("HOME", "USER", "SHELL", "PATH")
    env_pairs: List[str] = [
        f"HOME={home}",
        f"USER={user}",
        "SHELL=/bin/zsh",
        f"PATH={sandbox_path(home, path_prefix)}",
    ]
    for k, v in sandbox_env.items():
        if k in protected:
            continue  # never let a caller override the identity/PATH quartet
        env_pairs.append(f"{k}={v}")
    # Run the agent from the workspace.  A login zsh `cd`s in, then execs the
    # agent so it inherits the TTY and PID (no wrapper process lingering).
    inner = f"cd {_sh_quote(str(workspace))} && exec " + " ".join(
        _sh_quote(a) for a in agent_argv
    )
    return [
        "sudo",
        "--login",
        "--set-home",
        f"--user={user}",
        "/usr/bin/env",
        "-i",
        *env_pairs,
        "/usr/bin/sandbox-exec",
        "-f",
        str(profile_path),
        "--",
        "/bin/zsh",
        "-c",
        inner,
    ]


# ---------------------------------------------------------------------------
# In-process entrypoint bootstrap — reuse the stdlib-only generators
# ---------------------------------------------------------------------------


def entrypoint_bootstrap_script(
    repo_src: Path,
    *,
    workspace: Path,
    sandbox_home: Path,
    agents: List[str],
    macos_log: str = "off",
    git_identity: Optional[Dict[str, str]] = None,
    bootstrap_env: Optional[Dict[str, str]] = None,
    path_prefix: Optional[List[str]] = None,
    staged_dir: Path = STATE_DIR,
) -> str:
    """A Python script the sandbox user runs to generate its jail config.

    ``src/entrypoint`` is stdlib-only and env-driven, and already runs
    outside a container in a temp HOME via ``_entrypoint_preflight`` — so
    it runs natively too.  This script (executed as the sandbox user via
    ``sudo -u … python3 <script>``) rebinds the entrypoint's path constants
    to the sandbox user's real macOS locations, then runs the same
    config-generation the container entrypoint runs — the shims, agent
    launchers, bashrc, mise config, MCP wrappers, git/jj identity, and the
    per-agent ``CONFIG_WRITERS`` loop (already gated on YOLO_AGENTS) — while
    skipping the Linux-only boot steps (cgroups, iptables/socat, ld.so
    cache, /run timezone).  The result: ``~/.yolo-shims`` + real
    ``~/.claude``/``~/.codex``/… configs in the sandbox home, natively.

    ``entrypoint`` is imported from the root-owned staged copy under
    ``staged_dir`` (see :func:`stage_entrypoint_commands`), NOT from
    ``repo_src`` — the sandbox uid can't traverse a ``chmod 750`` host home
    to reach the checkout.  ``git_identity`` (``{"YOLO_GIT_NAME": …,
    "YOLO_GIT_EMAIL": …}``) is baked into the script's env so
    ``configure_git``/``configure_jj`` write the right identity (the
    bootstrap runs under a scrubbed env, so the launch-time env doesn't reach
    it).

    Returned as text (not executed) so it is unit-testable and can be
    written to the root-owned STATE_DIR before the sandbox user runs it.
    ``macos_log`` gates the in-sandbox ``yolo-log`` helper (off/user/full).
    """
    import json

    log_helper = macos_log_wrapper_script(macos_log)
    import_path = staged_entrypoint_dir(staged_dir).parent
    # The SAME PATH baked into launch_argv (shims → darwin store dirs → system).
    # A login/interactive shell the agent spawns (the default REPL is `zsh -l`,
    # and the runbook used `bash -lc`) re-runs macOS path_helper via
    # /etc/zprofile /etc/profile, which prepends /usr/local/bin (Homebrew) AHEAD
    # of our baked PATH — shadowing the nix-store packages.  So we also write
    # login rc files that RE-prepend this PATH *after* path_helper runs.
    login_path = sandbox_path(sandbox_home, path_prefix)
    # Bake git identity AND the config-derived env the entrypoint generators
    # read (YOLO_BLOCK_CONFIG for security.blocked_tools → generate_shims;
    # YOLO_MISE_TOOLS → generate_mise_config; YOLO_MCP_* / YOLO_LSP_* → the
    # mcp/lsp generators) into the bootstrap's scrubbed env, so the native
    # backend enforces the SAME per-workspace config surface as the container.
    baked = dict(git_identity or {})
    baked.update(bootstrap_env or {})
    identity_lines = "".join(
        f"os.environ[{k!r}] = {v!r}\n" for k, v in sorted(baked.items())
    )
    return f"""\
#!/usr/bin/env python3
# yolo-jail macOS-user entrypoint bootstrap (generated).  Runs AS the
# sandbox user to populate its home with shims + agent configs natively.
import os
import stat
import sys
from pathlib import Path

# Point the entrypoint's HOME-derived path constants at the sandbox user's
# home BEFORE importing it — SHIM_DIR/NPM_BIN/CLAUDE_DIR/MISE_SHIMS/… are
# computed at import time, so rebinding after import would be too late.
# JAIL_HOME drives HOME; leaving NPM_CONFIG_PREFIX/GOPATH/MISE_DATA_DIR unset
# makes them derive from HOME (.npm-global / go / .local/share/mise), which
# is exactly the PATH the launch env expects.  No /mise, no /workspace mount
# on a native host.
home = Path({str(sandbox_home)!r})
os.environ["JAIL_HOME"] = str(home)
os.environ["HOME"] = str(home)
os.environ["YOLO_AGENTS"] = {json.dumps(json.dumps(agents))}
os.environ.setdefault("YOLO_HOST_DIR", {str(workspace)!r})
{identity_lines}
# Import the stdlib-only entrypoint from the root-owned staged copy — the
# host checkout ({str(repo_src)!r}) may be unreadable to this uid.
sys.path.insert(0, {str(import_path)!r})
import entrypoint

# The workspace path is a hardcoded /workspace mount in the container; point
# it at the real workspace so any workspace-relative entrypoint logic lines up.
entrypoint.WORKSPACE = Path({str(workspace)!r})

# Generate the same config the container entrypoint does.  The Linux-only
# boot steps the container entrypoint also runs are intentionally NOT
# called here — they are no-ops (or nonsensical) on a native macOS user.
entrypoint.generate_shims()
entrypoint.generate_agent_launchers()
entrypoint.generate_bashrc()
entrypoint.generate_mise_config()
entrypoint.generate_mcp_wrappers()
entrypoint.configure_git()
entrypoint.configure_jj()
from entrypoint.agent_configs import CONFIG_WRITERS
from entrypoint.agent_registry import AGENTS

for _name in entrypoint._load_agents():
    _spec = AGENTS.get(_name)
    _writer = CONFIG_WRITERS.get(_name) if _spec is not None else None
    if _writer is not None:
        _writer()

# Install the macOS unified-logging helper (yolo-log) — the native analog
# of the Linux jail's yolo-journalctl bridge, gated by `macos_log`.
_bin = home / ".local" / "bin"
_bin.mkdir(parents=True, exist_ok=True)
_ylog = _bin / "yolo-log"
_ylog.write_text({json.dumps(log_helper)})
_ylog.chmod(_ylog.stat().st_mode | stat.S_IEXEC)

# Re-prepend the sandbox PATH in the login rc files.  macOS path_helper
# (/etc/zprofile for zsh -l, /etc/profile for bash -lc) reorders PATH to put
# /usr/local/bin (Homebrew) first; these rc files run AFTER it, so the
# nix-store packages + agent shims win again.  Covers login zsh (the default
# REPL), interactive zsh, and login bash.  Bare binaries / plain `-c` shells
# don't read these and keep the correct baked env -i PATH.
_login_path = {json.dumps(login_path)}
_rc = f'# yolo-jail: re-prepend the sandbox PATH AFTER macOS path_helper\\nexport PATH="{{_login_path}}:$PATH"\\n'
for _f in (".zprofile", ".zshrc", ".bash_profile"):
    (home / _f).write_text(_rc)

print("yolo-jail macos-user bootstrap ok")
"""


# ---------------------------------------------------------------------------
# Loopholes on the native backend
# ---------------------------------------------------------------------------
# The container backend bridges a few host capabilities INTO the jail over
# Unix sockets (loopholes).  On the native backend the sandbox is just
# another local user on the same box, so most of that plumbing collapses:
#
#   * claude-oauth-broker: no socket relay / bind mount.  The broker's
#     singleton Unix socket is already local; we just have to let uid 449
#     connect.  ``broker_socket_grant_commands`` builds the chmod/ACL for
#     that (SO_PEERCRED/getpeereid then attests a REAL macOS uid — a
#     stronger identity signal than a mapped container uid).
#   * host-processes / cgroup-delegate / journal bridge: dropped — a native
#     macOS user can already see host processes it's entitled to and has no
#     cgroup analog.  The Linux "journal" loophole's ANALOG is the unified
#     logging system (`log show`/`log stream`), replicated below as an
#     opt-in ``yolo-log`` helper with the same config-gated ergonomics.

# Config values for the macOS log helper, mirroring `journal`'s modes:
#   "off"       — no helper (default)
#   "user"      — helper scoped to the current process subsystem/predicate
#   "full"      — helper passes args straight through to `log`
MACOS_LOG_MODES = ("off", "user", "full")


def broker_socket_grant_commands(
    socket_path: Path, *, group: str = SANDBOX_GROUP
) -> List[List[str]]:
    """chmod/chgrp argv letting the sandbox group reach the broker socket.

    The claude-oauth-broker's singleton socket lives on the host fs; the
    sandbox user connects to it directly (no relay).  Grant the shared
    group rw on the socket + its parent dir so uid 449 can connect while
    other local users can't (the dir stays group-scoped, not world).
    """
    parent = socket_path.parent
    return [
        ["chgrp", group, str(parent)],
        ["chmod", "0750", str(parent)],
        ["chgrp", group, str(socket_path)],
        ["chmod", "0660", str(socket_path)],
    ]


def macos_log_wrapper_script(mode: str) -> str:
    """A ``yolo-log`` helper wrapping Apple's unified logging (`log`).

    The macOS analog of the Linux jail's ``yolo-journalctl`` journal
    bridge — but no socket bridge is needed (the sandbox user is local), so
    this just wraps ``/usr/bin/log`` with the same opt-in, config-gated
    ergonomics:

      * ``mode == "off"``  — a stub that explains how to enable it and exits 1.
      * ``mode == "user"`` — defaults to ``log show`` for recent messages;
        the user can still pass ``stream``/``show`` + predicates.
      * ``mode == "full"`` — passes all args straight through to ``log``.

    Returned as text so it's unit-testable and installed into the sandbox
    user's ``~/.local/bin/yolo-log`` by the entrypoint bootstrap.
    """
    if mode not in MACOS_LOG_MODES:
        mode = "off"
    if mode == "off":
        body = (
            'echo "yolo-log: macOS log access is disabled." >&2\n'
            'echo "  Enable it by setting \\"macos_log\\": \\"user\\" (or '
            '\\"full\\") in yolo-jail.jsonc, then restart." >&2\n'
            "exit 1\n"
        )
    elif mode == "full":
        # Unrestricted passthrough to `log`.
        body = 'exec /usr/bin/log "$@"\n'
    else:  # "user"
        # Default to a recent `show` when no subcommand is given; otherwise
        # pass through (so `yolo-log stream --predicate …` works).
        body = (
            'if [ "$#" -eq 0 ]; then\n'
            "  exec /usr/bin/log show --last 5m --style compact\n"
            "fi\n"
            'case "$1" in\n'
            "  show|stream|collect|config|help)\n"
            '    exec /usr/bin/log "$@" ;;\n'
            "  *)\n"
            '    exec /usr/bin/log show "$@" ;;\n'
            "esac\n"
        )
    return "#!/bin/bash\nset -euo pipefail\n" + body


# ---------------------------------------------------------------------------
# Helpers (small; pure)
# ---------------------------------------------------------------------------


def session_profile_path(cname: str, state_dir: Path = STATE_DIR) -> Path:
    """Root-owned per-session Seatbelt profile path (0444, sandbox can't edit)."""
    return state_dir / f"profile-{cname}.sb"


def _sh_quote(s: str) -> str:
    """Single-quote a string for safe bash embedding."""
    return "'" + s.replace("'", "'\\''") + "'"


def _sbpl_str(s: str) -> str:
    r"""Quote a path as an SBPL double-quoted string literal."""
    return '"' + s.replace("\\", "\\\\").replace('"', '\\"') + '"'


# ---------------------------------------------------------------------------
# Run plan — the ordered, pre-computed artifacts + commands for a session
# ---------------------------------------------------------------------------
# Everything the orchestrator does is first assembled into this plan as data
# (Linux-pure), then either PRINTED (``--dry-run``) or EXECUTED.  Building the
# plan and validating its invariants needs no Mac, so a Linux unit test
# statically catches the interpreter-stub, host-import, and workspace-in-home
# problems before any privileged command runs.


@dataclass
class RunPlan:
    """The fully-resolved, ordered artifacts + commands for one session."""

    workspace: Path
    cname: str
    profile_path: Path
    seatbelt: str
    interp: Optional[str]
    interp_candidates: List[str]
    staged_dir: Path
    stage_commands: List[List[str]]
    bootstrap: str
    bootstrap_path: Path
    bootstrap_argv: List[str]
    launch_argv: List[str]
    git_identity: Dict[str, str]
    # The home dir the workspace lives inside, or None when it's on neutral
    # ground.  Non-None is a hard error (the run refuses).
    offending_home: Optional[Path] = None
    # Native aarch64-darwin ``packages:`` materialization results (populated by
    # the caller after nix realizes the devShell; empty on the dry-run/no-nix
    # path).  darwin_path_prefix rides into launch_argv's PATH; darwin_env is
    # merged into sandbox_env; darwin_skipped names had no darwin build.
    darwin_path_prefix: List[str] = field(default_factory=list)
    darwin_env: Dict[str, str] = field(default_factory=dict)
    darwin_skipped: List[str] = field(default_factory=list)
    darwin_materialized: bool = False


def _bootstrap_argv(
    interp: str, boot_path: Path, user: str = SANDBOX_USER
) -> List[str]:
    """``sudo -u <sandbox> <interp> <boot>`` — run the bootstrap as the sandbox.

    No ``--login``/``--set-home``: the bootstrap imports the staged entrypoint
    from the root-owned STATE_DIR and is launched with an explicit safe cwd,
    so it never needs to traverse the host home or source ``_yolojail``'s
    login zsh + ``/etc/zprofile`` (a documented fragility on managed Macs).
    """
    return ["sudo", f"--user={user}", interp, str(boot_path)]


def build_run_plan(
    workspace: Path,
    config: Dict[str, Any],
    agents: List[str],
    agent_argv: List[str],
    *,
    repo_src: Path,
    sandbox_env: Dict[str, str],
    interp: Optional[str],
    darwin: Optional[Any] = None,
) -> RunPlan:
    """Assemble the full :class:`RunPlan` (pure — no shelling out).

    ``sandbox_env`` is the fully-resolved launch env (git identity + TERM +
    any provider keys); ``interp`` is the resolved python3 (may be ``None`` if
    none was found — the invariant check flags that).  The git identity is
    lifted out of ``sandbox_env`` and baked into the bootstrap script too, so
    ``configure_git``/``configure_jj`` write the right identity under the
    bootstrap's scrubbed env.

    ``darwin`` (a ``darwin_packages.DarwinPackages``, or None) carries the
    already-materialized native ``packages:`` result — this function stays
    PURE (the nix build happened in the caller).  When present its store bin
    dirs are threaded into the launch PATH and its whitelisted env is merged
    into the sandbox env.
    """
    darwin_prefix = list(darwin.path_prefix) if darwin else []
    darwin_env = dict(darwin.env) if darwin else {}
    darwin_skipped = list(darwin.skipped) if darwin else []
    if darwin_env:
        # Merge non-PATH darwin build vars (e.g. PKG_CONFIG_PATH) into the
        # launch env; the store PATH rides the separate path_prefix channel
        # (launch_argv's protected quartet drops any PATH key in sandbox_env).
        sandbox_env = {**sandbox_env, **darwin_env}
    cname = _cname(workspace)
    profile_path = session_profile_path(cname)
    bootstrap_path = STATE_DIR / f"bootstrap-{cname}.py"
    git_identity = {
        k: v
        for k, v in sandbox_env.items()
        if k.startswith("YOLO_GIT") or k.startswith("YOLO_JJ")
    }
    # A concrete interpreter string for the argv even when unresolved, so the
    # plan is still printable/inspectable; the invariant check fails the plan
    # when interp is None.
    interp_str = interp or _PYTHON_CANDIDATES[-1]
    # Config-derived env the entrypoint generators read, mirroring the
    # container path's _entrypoint_preflight block — so the native backend
    # enforces the same per-workspace surface: blocked_tools (incl. the
    # default grep/find recursive blocks), mise tools, mcp servers/presets,
    # and lsp servers.  Reuses the container-side resolvers (no duplication).
    from .config import _merge_mise_tools, _normalize_blocked_tools

    import json as _json

    bootstrap_env = {
        "YOLO_HOST_DIR": str(workspace.resolve()),
        "YOLO_BLOCK_CONFIG": _json.dumps(
            _normalize_blocked_tools(config.get("security"))
        ),
        "YOLO_MISE_TOOLS": _json.dumps(_merge_mise_tools(config)),
        "YOLO_LSP_SERVERS": _json.dumps(config.get("lsp_servers", {})),
        "YOLO_MCP_SERVERS": _json.dumps(config.get("mcp_servers", {})),
        "YOLO_MCP_PRESETS": _json.dumps(config.get("mcp_presets", [])),
    }
    boot = entrypoint_bootstrap_script(
        repo_src,
        workspace=workspace,
        sandbox_home=SANDBOX_HOME,
        agents=agents,
        macos_log=str(config.get("macos_log", "off")),
        git_identity=git_identity,
        bootstrap_env=bootstrap_env,
        path_prefix=darwin_prefix,
    )
    return RunPlan(
        workspace=workspace,
        cname=cname,
        profile_path=profile_path,
        seatbelt=seatbelt_profile(workspace, SANDBOX_HOME),
        interp=interp,
        interp_candidates=python_candidates(),
        staged_dir=STATE_DIR,
        stage_commands=stage_entrypoint_commands(repo_src),
        bootstrap=boot,
        bootstrap_path=bootstrap_path,
        bootstrap_argv=_bootstrap_argv(interp_str, bootstrap_path),
        launch_argv=launch_argv(
            agent_argv,
            profile_path=profile_path,
            sandbox_env=sandbox_env,
            workspace=workspace,
            path_prefix=darwin_prefix,
        ),
        git_identity=git_identity,
        offending_home=home_containing(workspace),
        darwin_path_prefix=darwin_prefix,
        darwin_env=darwin_env,
        darwin_skipped=darwin_skipped,
        darwin_materialized=darwin is not None,
    )


def plan_invariants(plan: RunPlan) -> List[str]:
    """Static checks over a :class:`RunPlan`; returns violation messages.

    All Linux-checkable — this is what makes ``--dry-run`` a real gate rather
    than a pretty-printer.  Each failure corresponds to a confirmed
    real-macOS blocker the run path would otherwise hit.
    """
    problems: List[str] = []

    # B2: a real interpreter must resolve, and never the bare stub as first
    # choice while better candidates exist.
    if plan.interp is None:
        problems.append(
            "no python3 interpreter resolved for the sandbox user "
            "(install Command Line Tools or a Homebrew/Nix python3)"
        )
    if plan.interp_candidates and plan.interp_candidates[0] == "/usr/bin/python3":
        problems.append(
            "/usr/bin/python3 (the xcode-select stub risk) must not be the "
            "first interpreter candidate"
        )

    # B3: the bootstrap must import from the root-owned staged dir, never the
    # host checkout.
    if str(staged_entrypoint_dir(plan.staged_dir).parent) not in plan.bootstrap:
        problems.append(
            "bootstrap does not import entrypoint from the staged state dir "
            f"({plan.staged_dir}); it would fail to import from a 0750 home"
        )

    # The workspace must be neutral ground — never inside a user's home.
    # Sharing a workspace under ~ would require threading an access grant
    # through the home dir, the layered footgun this model exists to avoid.
    if plan.offending_home is not None:
        problems.append(
            f"workspace {plan.workspace} is inside the home directory "
            f"{plan.offending_home}; the macos-user backend shares only "
            f"neutral ground. Move it under {SHARED_ROOT_DEFAULT} (or set "
            "config `macos_shared_root` to another non-home path)."
        )

    # Git identity must reach the BOOTSTRAP env (not only the launch env),
    # else configure_git/jj no-op and commits get the wrong identity.
    for k in plan.git_identity:
        if k not in plan.bootstrap:
            problems.append(f"git identity {k} not baked into the bootstrap env")

    # Acceptance-bar guard: catch the WIRING bug where darwin materialization
    # produced store bin dirs but they never reached the launch PATH — a green
    # run with the declared tools silently absent is the exact failure that got
    # the first macos-user attempt excised.  Checks the actual launch_argv, so
    # it can't false-fire on a legitimately-empty prefix (all packages skipped,
    # or library-only packages with no bin output).
    launch_str = " ".join(plan.launch_argv)
    for store_bin in plan.darwin_path_prefix:
        if store_bin not in launch_str:
            problems.append(
                f"darwin package bin dir {store_bin} did not reach the launch "
                "PATH — declared tools would be silently missing"
            )

    return problems


def _print_plan(plan: RunPlan, problems: List[str]) -> None:
    """Render a :class:`RunPlan` for ``--dry-run`` (human-readable)."""
    from .console import console

    console.print("[bold]macos-user run plan[/bold] (dry-run — nothing executed)\n")
    console.print(f"workspace:   {plan.workspace}")
    console.print(f"session:     {plan.cname}")
    console.print(f"interpreter: {plan.interp or '[red]<unresolved>[/red]'}")
    console.print(f"  candidates: {', '.join(plan.interp_candidates)}")
    console.print(f"profile:     {plan.profile_path}")
    console.print(f"staged src:  {staged_entrypoint_dir(plan.staged_dir)}")
    console.print(
        f"git identity: {plan.git_identity or '(none — commits use no identity)'}"
    )
    if plan.darwin_materialized:
        console.print(
            f"darwin pkgs: {len(plan.darwin_path_prefix)} store bin dir(s) on PATH"
        )
        if plan.darwin_skipped:
            console.print(
                f"  [yellow]skipped (no darwin build):[/yellow] "
                f"{', '.join(plan.darwin_skipped)}"
            )
    else:
        console.print(
            "darwin pkgs: [dim]not materialized (dry-run — nix build skipped)[/dim]"
        )
    console.print()

    def _section(title: str, body: str) -> None:
        console.print(f"[bold]── {title} ──[/bold]")
        console.print(body.rstrip("\n"))
        console.print("")

    console.print(
        "[bold]── privileged commands (run via sudo) ──[/bold]\n"
        "[dim]sudo may prompt for your password; it's forwarded through the "
        "TTY proxy so you can answer inline.[/dim]"
    )
    for cmd in plan.stage_commands:
        console.print("  sudo " + " ".join(cmd))
    console.print("  sudo " + " ".join(plan.bootstrap_argv[1:]))
    console.print("")
    _section("Seatbelt profile", plan.seatbelt)
    _section("bootstrap script", plan.bootstrap)
    console.print("[bold]── launch argv ──[/bold]")
    console.print("  " + " ".join(plan.launch_argv))
    console.print("")
    if problems:
        console.print("[bold red]plan invariant violations:[/bold red]")
        for p in problems:
            console.print(f"  ✗ {p}")
    else:
        console.print("[green]✓ all plan invariants hold[/green]")


# ---------------------------------------------------------------------------
# Orchestrator (macOS-only; shells out) — thin, wired from run()
# ---------------------------------------------------------------------------


def _is_macos() -> bool:
    return sys.platform == "darwin"


def macos_sandbox_env(config: Dict[str, Any]) -> Dict[str, str]:
    """Extra env layered into the sandbox launch (git identity, TERM).

    Host credentials never cross: only the git/jj identity (safe, and what
    the container backend also injects) and TERM/COLORTERM for a working
    REPL.  Provider API keys, if the user wants them, come from the config's
    ``env_sources`` — resolved by the caller and merged in — not from the
    host environment wholesale.
    """
    env: Dict[str, str] = {}
    term = os.environ.get("TERM")
    if term:
        env["TERM"] = term
    colorterm = os.environ.get("COLORTERM")
    if colorterm:
        env["COLORTERM"] = colorterm
    for var, key in (("YOLO_GIT_NAME", "user.name"), ("YOLO_GIT_EMAIL", "user.email")):
        val = _git_config(key)
        if val:
            env[var] = val
    return env


def run_macos_user(
    workspace: Path,
    config: Dict[str, Any],
    agents: List[str],
    agent_argv: List[str],
    *,
    repo_src: Path,
    sandbox_env: Optional[Dict[str, str]] = None,
    dry_run: bool = False,
) -> int:
    """Launch ``agent_argv`` in the dedicated-user + Seatbelt sandbox.

    Steps (all macOS-only; the builders they call are Linux-tested):

      0. Build the full :class:`RunPlan` (pure) and check its invariants.
         In ``dry_run`` mode, print the plan + invariant results and return
         (0 if the plan is clean, 1 otherwise) WITHOUT executing anything —
         this is the Linux/CI-testable gate.
      1. Preconditions: macOS, ``sandbox-exec`` present, sandbox account
         provisioned, and — the fail-closed additions — passwordless sudo
         configured and a real interpreter runnable as the sandbox user, so
         we never prompt into an unanswerable proxied pty.
      2. Install the root-owned Seatbelt profile + stage the entrypoint pkg.
         (No ACL step: files under the shared root already inherit the group
         grant from the root ACE applied at `macos-setup`.)
      3. Run the entrypoint bootstrap AS the sandbox user; ABORT on failure
         (a dead bootstrap means no shims/configs — launching is pointless).
      4. Launch the agent under ``run_with_proxy``.

    Returns the agent's exit code (or 1 on a precondition/setup failure).
    """
    from .console import console
    from .tty_proxy import run_with_proxy

    def _plan(darwin=None) -> "RunPlan":
        env = macos_sandbox_env(config)
        # Provider API keys etc. the agent needs to authenticate — resolved
        # from the config's `env_sources` (files/host vars the user opted in
        # to), exactly as the container path does.  Without this the native
        # agent silently has no keys.  launch_argv protects the HOME/USER/
        # SHELL/PATH quartet, so user keys can't shadow the identity/PATH.
        from .config import _resolve_env_sources

        try:
            env.update(_resolve_env_sources(workspace, config))
        except Exception:
            pass  # a bad env_sources entry must not crash the plan
        if sandbox_env:
            env.update(sandbox_env)
        return build_run_plan(
            workspace,
            config,
            agents,
            agent_argv,
            repo_src=repo_src,
            sandbox_env=env,
            interp=resolve_python(),
            darwin=darwin,
        )

    # 0. Dry-run: build the plan, print it + its invariants, execute nothing.
    # Pure (darwin=None → no nix build), so CI and a Mac agent can both inspect
    # it pre-launch on any OS.  The Packages section shows the intent.
    if dry_run:
        plan = _plan()
        problems = plan_invariants(plan)
        _print_plan(plan, problems)
        return 1 if problems else 0

    # Fail closed BEFORE any subprocess when we can't run here — the plan
    # builder reads host git config, so build it only past this gate.
    if not _is_macos():
        console.print(
            "[bold red]runtime 'macos-user' requires macOS.[/bold red] "
            "Use 'podman' or 'container' on this host.\n"
            "[dim]Tip: `yolo run --dry-run` prints the full plan on any OS.[/dim]"
        )
        return 1
    # Must NOT be run under sudo — the launch self-escalates (sudo -u sandbox),
    # and running as root makes _host_user() → 'root', misassigning the git
    # identity + ACL grant.  (dry-run above already returned; this only gates
    # the real launch.)
    if os.geteuid() == 0:
        console.print(
            "[bold red]Don't run `yolo` under sudo for the macos-user "
            "backend.[/bold red]  It escalates each step itself; running as "
            "root breaks the per-user identity/ACL."
        )
        return 1

    # Cheap preconditions FIRST — before the (potentially slow) nix build, so a
    # host missing Seatbelt or the sandbox user is rejected in milliseconds
    # rather than after a multi-minute darwin package build.
    if shutil.which("sandbox-exec") is None:
        console.print(
            "[bold red]sandbox-exec not found[/bold red] — the macos-user "
            "backend needs Apple Seatbelt (built into macOS)."
        )
        return 1
    if not _sandbox_user_exists():
        console.print(
            f"[bold red]Sandbox user '{SANDBOX_USER}' does not exist.[/bold red]\n"
            "Run the one-time setup to create it (`yolo macos-setup`; see "
            "`docs/design/macos-no-vm-direction.md`)."
        )
        return 1

    # Materialize `packages:` as native aarch64-darwin nix (the acceptance
    # bar).  Runs nix on the HOST user before any sandbox; on failure abort
    # with an actionable message pointing at the Apple Container fallback
    # rather than launch a sandbox missing the declared tools.
    from .config import _effective_packages
    from . import darwin_packages

    darwin = None
    pkgs = _effective_packages(config)
    if pkgs:
        try:
            darwin = darwin_packages.materialize(repo_src.parent, pkgs)
        except darwin_packages.DarwinPackagesError as e:
            console.print(
                f"[bold red]Could not materialize packages natively:[/bold red] {e}\n"
                "[dim]Fix the package, or use the Apple Container runtime "
                '(runtime: "container") which builds them in a Linux VM.[/dim]'
            )
            return 1
        if darwin.skipped:
            console.print(
                "[yellow]Skipped packages with no aarch64-darwin build:[/yellow] "
                f"{', '.join(darwin.skipped)}\n"
                "[dim](use the container runtime for these — or, if a name is "
                "unexpected, check for a typo: an unknown attr is skipped, not "
                "errored, because a hard error would abort the whole eval.)[/dim]"
            )

    plan = _plan(darwin)
    problems = plan_invariants(plan)
    if problems:
        console.print("[bold red]macos-user run plan is not viable:[/bold red]")
        for p in problems:
            console.print(f"  ✗ {p}")
        console.print("\n[dim]Run `yolo run --dry-run` to inspect the full plan.[/dim]")
        return 1

    # (Seatbelt + sandbox-user preconditions already checked above, before the
    # nix build, so a misconfigured host fails fast.)

    # The setup steps below run under sudo.  We deliberately do NOT install a
    # passwordless-sudo rule (changing the host's sudo policy is the user's
    # call, not ours), so sudo may prompt for a password.  The launch itself
    # runs under the TTY proxy, which forwards stdin, so the prompt is
    # answerable inline.  Give a heads-up so the prompt isn't a surprise.
    # NOTE: no per-run ACL walk — the workspace is shared via the inheriting
    # ACL on the shared root (applied once at `macos-setup`), so files under
    # it already carry the group grant.  Pre-existing files moved in are
    # retrofitted on demand with `yolo macos-fix-permissions`, off the hot
    # path.  These sudo steps are consecutive + fast, so one password covers
    # the whole run.
    console.print(
        "[dim]Setting up the sandbox (Seatbelt profile + bootstrap) — sudo may "
        "prompt for your password once.[/dim]"
    )

    # 2. Install the root-owned Seatbelt profile (0444) + stage entrypoint.
    if not _install_root_file(plan.profile_path, plan.seatbelt):
        console.print(f"[bold red]Could not write Seatbelt profile {plan.profile_path}")
        return 1
    for cmd in plan.stage_commands:
        if subprocess.run(["sudo", *cmd]).returncode != 0:
            console.print(
                f"[bold red]Could not stage entrypoint ({' '.join(cmd)}).[/bold red]"
            )
            return 1

    # 3. Bootstrap the sandbox user's home (shims + agent configs), natively.
    #    ABORT on failure: without shims/configs the agent launch is doomed
    #    and would fail confusingly downstream.
    if not _install_root_file(plan.bootstrap_path, plan.bootstrap):
        console.print(f"[bold red]Could not write bootstrap {plan.bootstrap_path}")
        return 1
    if subprocess.run(plan.bootstrap_argv).returncode != 0:
        console.print(
            "[bold red]entrypoint bootstrap failed[/bold red] — the sandbox "
            "user's shims/agent configs were not generated, so the agent "
            "would not run correctly. Aborting."
        )
        return 1

    # 4. Launch under the TTY proxy.
    return run_with_proxy(plan.launch_argv)


def _host_user() -> str:
    """The invoking (admin) user — best-effort, empty string if unknown."""
    import getpass

    try:
        return getpass.getuser()
    except (OSError, KeyError):
        return os.environ.get("USER", "")


def _refuse_if_root() -> None:
    """Fail fast if invoked as root (i.e. under ``sudo``).

    The macos-* commands SELF-ESCALATE: every privileged step shells out via
    ``sudo`` internally, and the design is "run as your normal admin user, get
    prompted per op."  Running the whole command under ``sudo`` is actively
    harmful, not just redundant: ``_host_user()`` (``getpass.getuser()``) would
    return ``root``, so the shared-workspace group grant
    (``dseditgroup -a <host_user> … _yolojail``) would go to ``root`` instead
    of you — silently breaking host↔sandbox rw-on-the-same-inodes sharing for
    your account.  So we reject euid 0 with a clear message instead of doing
    the wrong thing quietly.
    """
    import typer

    from .console import console

    if os.geteuid() == 0:
        console.print(
            "[bold red]Don't run this under sudo.[/bold red]  Run it as your "
            "normal admin user — it escalates each privileged step itself "
            "(prompting for your password).  Running the whole command as root "
            "would grant the shared-workspace ACL to 'root' instead of you and "
            "silently break host↔sandbox file sharing."
        )
        raise typer.Exit(1)


def _install_root_file(path: Path, content: str, mode: str = "0444") -> bool:
    """Write ``content`` to a root-owned file at ``path`` (mode ``0444``).

    Uses ``sudo mkdir -p`` + ``sudo tee`` + ``sudo chmod`` so the file is
    owned by root and unwritable by the sandbox user — the sandbox must not
    be able to edit its own Seatbelt profile or bootstrap script.
    """
    try:
        # Absolute tool paths so the argv is deterministic regardless of the
        # caller's PATH.
        if subprocess.run(["sudo", MKDIR, "-p", str(path.parent)]).returncode != 0:
            return False
        proc = subprocess.run(
            ["sudo", TEE, str(path)],
            input=content.encode(),
            stdout=subprocess.DEVNULL,
        )
        if proc.returncode != 0:
            return False
        return subprocess.run(["sudo", CHMOD, mode, str(path)]).returncode == 0
    except (OSError, subprocess.SubprocessError):
        return False


def _git_config(key: str) -> Optional[str]:
    """Read a host git config value (best-effort; None if unset)."""
    try:
        out = subprocess.run(
            ["git", "config", "--get", key], capture_output=True, timeout=5
        )
        if out.returncode == 0:
            return out.stdout.decode().strip() or None
    except (OSError, subprocess.SubprocessError):
        pass
    return None


def _sandbox_user_exists(user: str = SANDBOX_USER) -> bool:
    """True if the sandbox account exists (macOS ``id`` lookup)."""
    try:
        return (
            subprocess.run(["id", user], capture_output=True, timeout=5).returncode == 0
        )
    except (OSError, subprocess.SubprocessError):
        return False


def _cname(workspace: Path) -> str:
    """Session key — reuse the container-name scheme for consistency."""
    from .runtime import container_name_for_workspace

    return container_name_for_workspace(workspace)


def next_free_id(existing: "set[int]", floor: int = SANDBOX_MIN_ID) -> int:
    """First integer >= ``floor`` not in ``existing`` (SandVault's picker).

    ``existing`` is the union of taken UIDs and GIDs so the account's UID
    and GID can match.  Pure so it's unit-testable without ``dscl``.
    """
    uid = floor
    while uid in existing:
        uid += 1
    return uid


# ---------------------------------------------------------------------------
# Setup / teardown commands (macOS-only; shell out with sudo) — registered
# as `yolo macos-setup` / `yolo macos-teardown`.
# ---------------------------------------------------------------------------


def macos_setup() -> None:
    """Create the dedicated sandbox account (one-time, needs admin).

    Idempotent: exits early if the account already exists.  Picks a free
    UID/GID at/above SANDBOX_MIN_ID, runs the create command list, then sets
    a random password via ``dscl . -passwd`` reading the value from stdin
    (never an argv — it would show in ``ps``).  macOS only.

    We intentionally do NOT touch the host's sudo policy: per-run ``sudo``
    prompts are expected (SandVault does the same), and installing a NOPASSWD
    rule is the user's own decision, not something yolo-jail should make for
    them.  If you want non-interactive runs, configure sudo yourself.
    """
    import typer

    from .console import console

    if not _is_macos():
        console.print("[bold red]yolo macos-setup requires macOS.[/bold red]")
        raise typer.Exit(1)
    _refuse_if_root()  # self-escalates; running under sudo misassigns the ACL

    # 1. Account — create if missing, otherwise reuse (idempotent).  BOTH
    #    outcomes are success; report which one so the operator isn't left
    #    guessing whether "already exists" means it bailed.
    if _sandbox_user_exists():
        console.print(f"• Sandbox user [bold]{SANDBOX_USER}[/bold] already exists.")
    else:
        host_user = _host_user()
        uid = next_free_id(_taken_ids())
        console.print(
            f"• Creating sandbox user [bold]{SANDBOX_USER}[/bold] (uid {uid}); "
            "you may be prompted for your admin password by sudo."
        )
        for cmd in create_user_commands(uid, uid, host_user=host_user):
            if subprocess.run(["sudo", *cmd]).returncode != 0:
                console.print(
                    f"[bold red]✗ setup step failed:[/bold red] {' '.join(cmd)}"
                )
                raise typer.Exit(1)
        # Random password, piped via stdin (openssl rand → dscl . -passwd -).
        _set_random_password()
        console.print(f"  [green]created[/green] {SANDBOX_USER}.")

    # 1b. Provision the neutral shared root (idempotent).  This is where
    #     projects live to be shared host<->sandbox; it is NOT inside any home.
    console.print(
        f"• Provisioning shared root [bold]{SHARED_ROOT_DEFAULT}[/bold] "
        "(setgid + inheriting ACL, group _yolojail, no other-access) — "
        "projects created under it are shared automatically, no per-run walk."
    )
    for cmd in shared_root_provision_commands(host_user=_host_user()):
        if subprocess.run(["sudo", *cmd]).returncode != 0:
            console.print(f"[bold red]✗ setup step failed:[/bold red] {' '.join(cmd)}")
            raise typer.Exit(1)

    # 2. Readiness checks — report each so the final verdict is unambiguous.
    #    A real python3 must be reachable for the bootstrap; sandbox-exec must
    #    exist for the Seatbelt profile.  Neither is fatal to *setup* (they can
    #    be fixed later), but both gate a successful *run*, so surface them now.
    warnings: List[str] = []

    interp = resolve_python()
    if interp is None:
        warnings.append(
            "No Homebrew/Nix python3 found — the run path would fall back to "
            "/usr/bin/python3, which is the xcode-select stub unless the "
            "Command Line Tools are installed. Fix: `brew install python` or "
            "`xcode-select --install`."
        )
        console.print("• python3 for the sandbox: [yellow]not found[/yellow]")
    else:
        console.print(f"• python3 for the sandbox: [green]{interp}[/green]")

    if shutil.which("sandbox-exec") is None:
        warnings.append(
            "sandbox-exec not found on PATH — it ships with macOS, so this is "
            "unusual; the run path needs it for the Seatbelt profile."
        )
        console.print("• Apple Seatbelt (sandbox-exec): [yellow]not found[/yellow]")
    else:
        console.print("• Apple Seatbelt (sandbox-exec): [green]available[/green]")

    # nix + flake.lock: the backend materializes `packages:` as native
    # aarch64-darwin nix, so both are load-bearing for any config with packages.
    if shutil.which("nix") is None:
        warnings.append(
            "nix not found on PATH — the backend materializes `packages:` via "
            "native nix; install it (https://nixos.org/download) or configs "
            "with packages get no declared tools."
        )
        console.print("• nix (native darwin packages): [yellow]not found[/yellow]")
    else:
        console.print("• nix (native darwin packages): [green]available[/green]")

    # 3. One clear verdict + next steps.  Green ✓ only when nothing is
    #    outstanding; otherwise a yellow ⚠ that lists exactly what to fix.
    console.print("")
    if warnings:
        console.print(
            "[bold yellow]⚠ Setup done, but the macos-user backend is not "
            "ready to run yet:[/bold yellow]"
        )
        for w in warnings:
            console.print(f"  • {w}")
        console.print(
            "\nResolve the above, then verify with "
            "[bold]yolo run --dry-run[/bold] (prints the full plan; needs no "
            "further setup)."
        )
    else:
        console.print(
            f"[bold green]✓ macos-user backend ready.[/bold green] "
            f"Sandbox user '{SANDBOX_USER}' is provisioned and preconditions "
            "pass."
        )
        console.print(
            f"Next: put your project under [bold]{SHARED_ROOT_DEFAULT}/"
            "<name>[/bold] (the agent can only share neutral ground, never a "
            'path inside your home), set `runtime: "macos-user"` in '
            "yolo-jail.jsonc (or YOLO_RUNTIME=macos-user), then from that "
            "directory run [bold]yolo run --dry-run[/bold] to preview, or "
            "[bold]yolo[/bold] to launch.\n"
            "[dim]sudo will prompt per run — that's expected (we don't change "
            "your sudo policy).[/dim]"
        )


def macos_teardown() -> None:
    """Delete the sandbox account + home (needs admin).  macOS only."""
    import typer

    from .console import console

    if not _is_macos():
        console.print("[bold red]yolo macos-teardown requires macOS.[/bold red]")
        raise typer.Exit(1)
    _refuse_if_root()  # self-escalates; under sudo it'd `dseditgroup -d root`
    if not _sandbox_user_exists():
        console.print(f"Sandbox user '{SANDBOX_USER}' does not exist — nothing to do.")
        return

    for cmd in delete_user_commands(host_user=_host_user()):
        subprocess.run(["sudo", *cmd])
    console.print(f"[green]✓ Removed sandbox user '{SANDBOX_USER}'.[/green]")


def macos_unshare(workspace: str) -> None:
    """Strip the yolo-jail ACLs from a shared workspace (``chmod -h -N``).

    The clean-teardown primitive for a single project: returns every inode
    under ``workspace`` to plain POSIX permissions with no lingering
    ``group:_yolojail`` ACEs.  Safe because we only ever ACL neutral ground —
    this cannot touch anything of yours outside the workspace.  macOS only.
    """
    import typer

    from .console import console

    if not _is_macos():
        console.print("[bold red]yolo macos-unshare requires macOS.[/bold red]")
        raise typer.Exit(1)
    _refuse_if_root()  # self-escalates via sudo per op
    ws = Path(workspace).resolve()
    if not ws.is_dir():
        console.print(f"[bold red]Not a directory:[/bold red] {ws}")
        raise typer.Exit(1)
    rc = subprocess.run(["bash", "-c", workspace_acl_strip_script(ws)]).returncode
    if rc != 0:
        console.print(f"[yellow]ACL strip reported an error on {ws}.[/yellow]")
        raise typer.Exit(1)
    console.print(f"[green]✓ Stripped yolo-jail ACLs from {ws}.[/green]")


def macos_fix_permissions(path: Optional[str] = None) -> None:
    """Retrofit the shared-group ACL onto pre-existing files in the shared area.

    You should rarely need this.  Files *created* under the shared root
    inherit the group ACL automatically (set once at ``macos-setup``).  This
    command exists for the exception: **pre-existing files moved or
    preserve-copied in** (a ``mv ~/old-proj`` or ``cp -p``), which don't
    re-trigger inheritance and so arrive without the group grant — the agent
    could read world-readable ones but not *write* them, and couldn't read
    tightened-perm ones at all.

    ``path`` defaults to the whole shared root; pass a single project to scope
    it.  Batched + progress-announced, so even a large ``.venv`` is quick and
    doesn't look like a hang.  macOS only.
    """
    import typer

    from .console import console

    if not _is_macos():
        console.print("[bold red]yolo macos-fix-permissions requires macOS.[/bold red]")
        raise typer.Exit(1)
    _refuse_if_root()  # self-escalates via sudo per op
    target = Path(path).resolve() if path else SHARED_ROOT_DEFAULT
    if not target.is_dir():
        console.print(f"[bold red]Not a directory:[/bold red] {target}")
        raise typer.Exit(1)
    if home_containing(target) is not None:
        console.print(
            f"[bold red]{target} is inside a user home[/bold red] — the "
            "macos-user backend only manages ACLs on neutral ground "
            f"(under {SHARED_ROOT_DEFAULT} or another non-home root)."
        )
        raise typer.Exit(1)
    rc = subprocess.run(["bash", "-c", fix_permissions_script(target)]).returncode
    if rc != 0:
        console.print(
            f"[yellow]Some ACLs could not be applied under {target} "
            "(e.g. a file whose ACL is locked). The rest were applied.[/yellow]"
        )
        raise typer.Exit(1)
    console.print(f"[green]✓ Applied shared-group ACLs under {target}.[/green]")


def _taken_ids() -> "set[int]":
    """Union of existing UIDs and GIDs (for next_free_id).  macOS dscl."""
    ids: "set[int]" = set()
    for kind in ("Users", "Groups"):
        key = "UniqueID" if kind == "Users" else "PrimaryGroupID"
        try:
            out = subprocess.run(
                ["dscl", ".", "-list", f"/{kind}", key],
                capture_output=True,
                text=True,
                timeout=10,
            )
        except (OSError, subprocess.SubprocessError):
            continue
        for line in out.stdout.splitlines():
            parts = line.split()
            if parts and parts[-1].lstrip("-").isdigit():
                ids.add(int(parts[-1]))
    return ids


def _set_random_password(user: str = SANDBOX_USER) -> bool:
    """Set a random password on the sandbox account (value never in argv)."""
    try:
        rand = subprocess.run(
            ["openssl", "rand", "-base64", "32"], capture_output=True, timeout=5
        )
        if rand.returncode != 0:
            return False
        pw = rand.stdout.decode().strip()
        # `dscl . -passwd /Users/<u> <newpass>` — pass via a shell that reads
        # the value from an env var so it doesn't appear in this process's
        # argv/ps.  sudo preserves the single var we pass.
        proc = subprocess.run(
            [
                "sudo",
                "/bin/sh",
                "-c",
                f'dscl . -passwd /Users/{user} "$YOLO_SBPW"',
            ],
            env={"YOLO_SBPW": pw, "PATH": "/usr/bin:/bin:/usr/sbin:/sbin"},
        )
        return proc.returncode == 0
    except (OSError, subprocess.SubprocessError):
        return False
