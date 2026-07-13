"""Native macOS backend: isolate an agent in a dedicated user + Seatbelt.

``runtime: "macos-user"`` runs the agent as arm64-native macOS binaries in
a dedicated, hidden, unprivileged user account hardened with an Apple
Seatbelt (``sandbox-exec``) profile — no Linux container, no VM, no arch
switch.  It is the yolo-jail port of SandVault's design
(github.com/webcoyote/sandvault); we deliberately match SandVault's
security posture so there is a concrete standard to point at.  See
``docs/macos-native-user-sandbox-design.md`` for the honest security delta
vs. the container backend (weaker: shared kernel, deprecated sandbox-exec,
no resource caps) and why it's opt-in only.

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
# Passwordless-sudo policy installed by ``yolo macos-setup`` so the run path
# never prompts (a prompt would hang inside the proxied pty).  NO dot in the
# filename — sudo silently ignores files in sudoers.d whose name contains a
# ``.`` (or ``~``).
SUDOERS_PATH = Path("/etc/sudoers.d/yolo-jail")

# Absolute paths to the system tools the run path invokes under sudo.  Pinned
# so the generated sudoers rule and the actual argv match byte-for-byte
# (sudo's command match is exact-path): a ``mkdir`` PATH lookup that resolved
# to ``/usr/bin/mkdir`` would not match a rule written for ``/bin/mkdir``.
MKDIR = "/bin/mkdir"
TEE = "/usr/bin/tee"
CHMOD = "/bin/chmod"
CP = "/bin/cp"
ENV = "/usr/bin/env"
ZSH = "/bin/zsh"
SANDBOX_EXEC = "/usr/bin/sandbox-exec"

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
# Passwordless-sudo policy — generated as text, installed by macos-setup
# ---------------------------------------------------------------------------


def sudoers_rule(host_user: str, interp: str) -> str:
    """The ``/etc/sudoers.d/yolo-jail`` policy text the run path relies on.

    Scoped to the exact absolute commands the orchestrator runs under sudo:
    root-owned state-file installs (``mkdir``/``tee``/``chmod``/``cp``) and
    the run-as-sandbox-user launch/bootstrap (the resolved interpreter,
    ``sandbox-exec``, ``zsh``, ``env``).  Args are omitted (the profile,
    bootstrap, and workspace paths vary per session) — omitting is the
    simplest match that is still correct, since the command *paths* are
    pinned.  Without this, every ``sudo`` prompts on ``/dev/tty`` and the
    launch (which runs in a fresh proxied pty) hangs unanswerably.
    """
    root_cmds = ", ".join([MKDIR, TEE, CHMOD, CP])
    user_cmds = ", ".join([interp, SANDBOX_EXEC, ZSH, ENV])
    return (
        "# Managed by `yolo macos-setup` — passwordless sudo for the\n"
        "# macos-user backend.  Do not edit by hand; re-run macos-setup.\n"
        f"{host_user} ALL=(root) NOPASSWD: {root_cmds}\n"
        f"{host_user} ALL=({SANDBOX_USER}) NOPASSWD: {user_cmds}\n"
    )


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
# Workspace ancestry — traversal into a workspace nested under the host home
# ---------------------------------------------------------------------------


def workspace_ancestors(workspace: Path, root: Path = Path("/Users")) -> List[Path]:
    """Ancestor dirs strictly between ``root`` and ``workspace``.

    For ``/Users/matt/code/proj`` → ``[/Users/matt/code, /Users/matt]``.
    These are the components a non-staff sandbox user must be able to
    *traverse* (search + stat) to reach a workspace under the host home,
    yet gets no traversal on once the home is ``chmod 750``.  ``/Users``
    itself (root-owned, 0755) and the workspace (granted full rights
    separately) are excluded, as is anything the host user can't ``chmod``.
    """
    return [p for p in workspace.parents if root in p.parents]


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


# Search-only ACE for the workspace's ancestor dirs: grants directory
# traversal (open/stat the dir, read its metadata) WITHOUT read-data, so the
# sandbox user can descend to a workspace nested under the host home without
# being able to list or read sibling files in those ancestors.
_ANCESTOR_RIGHTS = "search,readattr,readsecurity"


def workspace_acl_aces(group: str = SANDBOX_GROUP) -> Dict[str, str]:
    """The four ``chmod +a`` ACE strings (dir / file-inherit / file / ancestor).

    Split so directories are searchable/listable and inherit correctly to
    children, while plain files never gain execute/search — SandVault's
    exact scheme, which sidesteps the umask-022 trap of a plain
    setgid-group share.  The ``ancestor`` ACE is traversal-only (no
    read-data) for the path components leading down to the workspace.
    """
    return {
        "dir": f"group:{group} allow {_DIR_RIGHTS}",
        "file_inherit": f"group:{group} allow {_FILE_INHERIT_RIGHTS}",
        "file": f"group:{group} allow {_FILE_RIGHTS}",
        "ancestor": f"group:{group} allow {_ANCESTOR_RIGHTS}",
    }


def workspace_acl_apply_script(workspace: Path, group: str = SANDBOX_GROUP) -> str:
    """A ``find``-based bash script applying the split ACEs in one pass.

    First grants a traversal-only ACE on each ancestor dir from the host home
    down to the workspace, so a workspace nested under a ``chmod 750`` host
    home is still reachable (without leaking sibling file contents).  Then
    directories get the dir ACE + the file-inherit template and
    non-directories get the file ACE.  ``chmod -h`` so symlinks aren't
    followed (avoid a swap race).  Idempotent (re-adding an identical ACE is
    a no-op).  Returned as text so it can be asserted in tests and run via
    ``bash -c`` on macOS.
    """
    aces = workspace_acl_aces(group)
    ws = str(workspace)
    lines = [
        "set -euo pipefail\n",
        f"ws={_sh_quote(ws)}\n",
    ]
    # Ancestors first: traversal-only so the sandbox user can descend to a
    # workspace under the host home.  Missing/unowned ancestors are skipped
    # (|| true) — the grant is best-effort and the run's own preflight
    # surfaces an unreachable workspace.
    for anc in workspace_ancestors(workspace):
        lines.append(
            f"chmod -h +a {_sh_quote(aces['ancestor'])} {_sh_quote(str(anc))} "
            "2>/dev/null || true\n"
        )
    lines += [
        # Directories: dir rights + inheritance template (two -a ACEs).
        'find "$ws" -type d -print0 | while IFS= read -r -d "" d; do\n',
        f'  chmod -h +a {_sh_quote(aces["dir"])} "$d"\n',
        f'  chmod -h +a {_sh_quote(aces["file_inherit"])} "$d"\n',
        "done\n",
        # Everything else (files, symlinks): file rights, no inherit.
        'find "$ws" ! -type d -print0 | while IFS= read -r -d "" f; do\n',
        f'  chmod -h +a {_sh_quote(aces["file"])} "$f"\n',
        "done\n",
    ]
    return "".join(lines)


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
    """
    ws = _sbpl_str(str(workspace))
    home = _sbpl_str(str(sandbox_home))
    # Path resolution evaluates file-read-metadata on EVERY component, so a
    # workspace nested under the host home needs each ancestor's metadata
    # readable — but NOT its contents.  Emit a per-ancestor
    # file-read-metadata allow (read-data on those dirs stays denied by the
    # /Users blanket deny above, so sibling files remain unreadable).  Only
    # when there ARE ancestors — a bare ``(allow file-read-metadata)`` would
    # match everything.
    ancestors = workspace_ancestors(workspace)
    if ancestors:
        ancestor_block = (
            "\n;; Traversal into a workspace nested under the host home: allow\n"
            + (
                ";; metadata (stat) on each ancestor component so path resolution\n"
                ";; succeeds, without re-allowing read-data (sibling files stay hidden).\n"
                "(allow file-read-metadata"
                + "".join(f"\n    (literal {_sbpl_str(str(a))})" for a in ancestors)
                + ")"
            )
        )
    else:
        ancestor_block = ""
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

;; --- Other users' homes: deny reads under /Users, re-allow traversal
;;     entries + the workspace + this sandbox user's own home ---
(deny file-read* (subpath "/Users"))
(allow file-read*
    (literal "/Users")
    (literal "/Users/Shared")
    (subpath {ws})
    (subpath {home})){ancestor_block}

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


def sandbox_path(home: Path = SANDBOX_HOME) -> str:
    """PATH for the sandboxed agent — its own bin dirs first, then system.

    Mirrors the jail's PATH ordering (shims → .local/bin → npm-global →
    mise shims → system) so the entrypoint-generated agent launchers
    (``~/.yolo-shims/claude`` etc.) and mise-managed tools are found, then
    system binaries.  Without this the scrubbed ``env -i`` PATH wouldn't
    include the agent binaries at all.
    """
    h = str(home)
    return ":".join(
        [
            f"{h}/.yolo-shims",
            f"{h}/.local/bin",
            f"{h}/.npm-global/bin",
            f"{h}/.local/share/mise/shims",
            f"{h}/go/bin",
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
        f"PATH={sandbox_path(home)}",
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
    identity_lines = "".join(
        f"os.environ[{k!r}] = {v!r}\n" for k, v in sorted((git_identity or {}).items())
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
# statically catches the interpreter-stub, host-import, ancestor-traversal,
# and sudoers/argv-mismatch problems before any privileged command runs.


@dataclass
class RunPlan:
    """The fully-resolved, ordered artifacts + commands for one session."""

    workspace: Path
    cname: str
    profile_path: Path
    seatbelt: str
    acl_script: str
    interp: Optional[str]
    interp_candidates: List[str]
    staged_dir: Path
    stage_commands: List[List[str]]
    bootstrap: str
    bootstrap_path: Path
    bootstrap_argv: List[str]
    launch_argv: List[str]
    sudoers_text: str
    sudoers_path: Path
    git_identity: Dict[str, str]
    ancestors: List[Path] = field(default_factory=list)


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
    host_user: str,
) -> RunPlan:
    """Assemble the full :class:`RunPlan` (pure — no shelling out).

    ``sandbox_env`` is the fully-resolved launch env (git identity + TERM +
    any provider keys); ``interp`` is the resolved python3 (may be ``None`` if
    none was found — the invariant check flags that).  The git identity is
    lifted out of ``sandbox_env`` and baked into the bootstrap script too, so
    ``configure_git``/``configure_jj`` write the right identity under the
    bootstrap's scrubbed env.
    """
    cname = _cname(workspace)
    profile_path = session_profile_path(cname)
    bootstrap_path = STATE_DIR / f"bootstrap-{cname}.py"
    git_identity = {
        k: v
        for k, v in sandbox_env.items()
        if k.startswith("YOLO_GIT") or k.startswith("YOLO_JJ")
    }
    # A concrete interpreter string for the argv/sudoers even when unresolved,
    # so the plan is still printable/inspectable; the invariant check fails
    # the plan when interp is None.
    interp_str = interp or _PYTHON_CANDIDATES[-1]
    boot = entrypoint_bootstrap_script(
        repo_src,
        workspace=workspace,
        sandbox_home=SANDBOX_HOME,
        agents=agents,
        macos_log=str(config.get("macos_log", "off")),
        git_identity=git_identity,
    )
    return RunPlan(
        workspace=workspace,
        cname=cname,
        profile_path=profile_path,
        seatbelt=seatbelt_profile(workspace, SANDBOX_HOME),
        acl_script=workspace_acl_apply_script(workspace),
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
        ),
        sudoers_text=sudoers_rule(host_user, interp_str),
        sudoers_path=SUDOERS_PATH,
        git_identity=git_identity,
        ancestors=workspace_ancestors(workspace),
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

    # B4: every workspace ancestor must be granted traversal in BOTH layers.
    for anc in plan.ancestors:
        if _sh_quote(str(anc)) not in plan.acl_script:
            problems.append(f"workspace ancestor {anc} missing a traversal ACE")
        if _sbpl_str(str(anc)) not in plan.seatbelt:
            problems.append(
                f"workspace ancestor {anc} missing a Seatbelt file-read-metadata allow"
            )

    # Git identity must reach the BOOTSTRAP env (not only the launch env),
    # else configure_git/jj no-op and commits get the wrong identity.
    for k in plan.git_identity:
        if k not in plan.bootstrap:
            problems.append(f"git identity {k} not baked into the bootstrap env")

    # Every privileged command path must be covered by the sudoers rule
    # (exact-path match), else it prompts + hangs in the proxied pty.
    def _sudo_command_paths(argv: List[str]) -> List[str]:
        # The command sudo matches is the first token after sudo's own flags
        # (``--user=…`` / ``-n`` / ``--login`` / ``--set-home``) and, for the
        # run-as-sandbox path, after the ``env -i k=v …`` prefix.
        rest = argv[1:] if argv and argv[0] == "sudo" else argv
        i = 0
        while i < len(rest) and (rest[i].startswith("-") or "=" in rest[i]):
            # stop stepping over the env prefix once we hit ``/usr/bin/env``
            if rest[i].startswith("/"):
                break
            i += 1
        paths: List[str] = []
        if i < len(rest):
            paths.append(rest[i])
        return paths

    covered = set()
    for line in plan.sudoers_text.splitlines():
        if "NOPASSWD:" in line:
            for tok in line.split("NOPASSWD:", 1)[1].split(","):
                covered.add(tok.strip())
    # stage_commands are bare argv (no ``sudo`` prefix); they run via sudo.
    for argv in plan.stage_commands:
        if argv and argv[0] not in covered:
            problems.append(
                f"privileged command {argv[0]} is not covered by the sudoers rule"
            )
    for argv in (plan.bootstrap_argv, plan.launch_argv):
        for p in _sudo_command_paths(argv):
            # For the launch, the matched command is ``/usr/bin/env`` (the
            # first absolute token after sudo's flags); for the bootstrap it's
            # the interpreter.  Either way it must be in the rule.
            if p not in covered:
                problems.append(
                    f"privileged command {p} is not covered by the sudoers rule"
                )

    # The sudoers filename must be dot-free (sudo silently ignores dotted
    # names in sudoers.d).
    if "." in plan.sudoers_path.name:
        problems.append(
            f"sudoers filename {plan.sudoers_path.name} contains a dot — sudo "
            "would silently ignore it"
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
        f"git identity: {plan.git_identity or '(none — commits use no identity)'}\n"
    )

    def _section(title: str, body: str) -> None:
        console.print(f"[bold]── {title} ──[/bold]")
        console.print(body.rstrip("\n"))
        console.print("")

    _section(f"sudoers rule → {plan.sudoers_path} (0440 root:wheel)", plan.sudoers_text)
    console.print("[bold]── privileged commands (run via sudo) ──[/bold]")
    for cmd in plan.stage_commands:
        console.print("  sudo " + " ".join(cmd))
    console.print("  sudo " + " ".join(plan.bootstrap_argv[1:]))
    console.print("")
    _section("Seatbelt profile", plan.seatbelt)
    _section("workspace ACL script", plan.acl_script)
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
      3. Apply the inheriting workspace ACL (incl. ancestor traversal).
      4. Run the entrypoint bootstrap AS the sandbox user; ABORT on failure
         (a dead bootstrap means no shims/configs — launching is pointless).
      5. Launch the agent under ``run_with_proxy``.

    Returns the agent's exit code (or 1 on a precondition/setup failure).
    """
    from .console import console
    from .tty_proxy import run_with_proxy

    def _plan() -> "RunPlan":
        env = macos_sandbox_env(config)
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
            host_user=_host_user(),
        )

    # 0. Dry-run: build the plan, print it + its invariants, execute nothing.
    # Pure, so CI and a Mac agent can both inspect it pre-launch on any OS.
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

    plan = _plan()
    problems = plan_invariants(plan)
    if problems:
        console.print("[bold red]macos-user run plan is not viable:[/bold red]")
        for p in problems:
            console.print(f"  ✗ {p}")
        console.print("\n[dim]Run `yolo run --dry-run` to inspect the full plan.[/dim]")
        return 1

    # 1. Preconditions — fail closed with actionable messages.
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
            "`docs/macos-native-user-sandbox-design.md`)."
        )
        return 1
    if not _passwordless_sudo_ok():
        console.print(
            "[bold red]passwordless sudo for the macos-user backend is not "
            "configured.[/bold red]\n"
            "Every run would prompt for your admin password, and the launch "
            "prompt (inside a proxied pty) would hang.\n"
            "Run `yolo macos-setup` to install the sudoers rule."
        )
        return 1
    # The resolved interpreter must actually RUN as the sandbox user (catches
    # the /usr/bin/python3 xcode-select stub — it errors instead of running).
    assert plan.interp is not None  # guaranteed by the invariant check above
    if not _interp_runs_as_sandbox(plan.interp):
        console.print(
            f"[bold red]python3 ({plan.interp}) can't run as '{SANDBOX_USER}'."
            "[/bold red]\n"
            "Install the Command Line Tools (`xcode-select --install`) or a "
            "Homebrew/Nix python3, then re-run."
        )
        return 1

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

    # 3. Share the workspace via the inheriting ACL (incl. ancestor traversal).
    if subprocess.run(["bash", "-c", plan.acl_script]).returncode != 0:
        console.print(
            "[yellow]workspace ACL grant reported an error — the sandbox "
            "user may not have full rw. Try `yolo macos-fix-permissions`.[/yellow]"
        )

    # 4. Bootstrap the sandbox user's home (shims + agent configs), natively.
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

    # 5. Launch under the TTY proxy.
    return run_with_proxy(plan.launch_argv)


def _host_user() -> str:
    """The invoking (admin) user — best-effort, empty string if unknown."""
    import getpass

    try:
        return getpass.getuser()
    except (OSError, KeyError):
        return os.environ.get("USER", "")


def _passwordless_sudo_ok() -> bool:
    """True if the run path's sudo commands won't prompt (``sudo -n`` probe).

    Probes both the root-owned-file path (``sudo -n <mkdir> …``) and the
    run-as-sandbox path (``sudo -n -u <user> true``); either prompting means
    the sudoers rule is missing or ignored.  ``sudo -n`` never prompts — it
    fails immediately when a password would be required — so this can't hang.
    """
    try:
        root_ok = (
            subprocess.run(
                ["sudo", "-n", MKDIR, "-p", str(STATE_DIR)],
                capture_output=True,
                timeout=10,
            ).returncode
            == 0
        )
        user_ok = (
            subprocess.run(
                ["sudo", "-n", f"--user={SANDBOX_USER}", "/usr/bin/true"],
                capture_output=True,
                timeout=10,
            ).returncode
            == 0
        )
        return root_ok and user_ok
    except (OSError, subprocess.SubprocessError):
        return False


def _interp_runs_as_sandbox(interp: str) -> bool:
    """True if ``interp`` actually executes Python as the sandbox user.

    Catches the ``/usr/bin/python3`` xcode-select stub, which (CLT absent)
    errors instead of running.  Uses ``sudo -n`` so it never prompts.
    """
    try:
        return (
            subprocess.run(
                ["sudo", "-n", f"--user={SANDBOX_USER}", interp, "-c", "import sys"],
                capture_output=True,
                timeout=15,
            ).returncode
            == 0
        )
    except (OSError, subprocess.SubprocessError):
        return False


def _install_root_file(path: Path, content: str, mode: str = "0444") -> bool:
    """Write ``content`` to a root-owned file at ``path`` (mode ``0444``).

    Uses ``sudo mkdir -p`` + ``sudo tee`` + ``sudo chmod`` so the file is
    owned by root and unwritable by the sandbox user — the sandbox must not
    be able to edit its own Seatbelt profile or bootstrap script.
    """
    try:
        # Absolute tool paths so these match the NOPASSWD sudoers rule
        # exactly (sudo's command match is by exact path).
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
    """Create the sandbox account + install the passwordless-sudo rule.

    Two idempotent steps (both need admin; this setup path DOES prompt for
    your password — that's expected; it's the per-run launch that must never
    prompt):

      1. Create the hidden sandbox account if absent (free UID/GID ≥
         SANDBOX_MIN_ID, hidden, off staff, random password via stdin so it
         never shows in ``ps``).
      2. Install ``/etc/sudoers.d/yolo-jail`` (validated with ``visudo -cf``,
         0440 root:wheel, dot-free name) so the run path's ``sudo`` calls are
         NOPASSWD — otherwise the launch prompts inside a proxied pty and
         hangs.  Re-written every run so a changed interpreter/host-user is
         picked up.

    macOS only.
    """
    import typer

    from .console import console

    if not _is_macos():
        console.print("[bold red]yolo macos-setup requires macOS.[/bold red]")
        raise typer.Exit(1)

    host_user = _host_user()

    # 1. Account.
    if _sandbox_user_exists():
        console.print(f"[green]Sandbox user '{SANDBOX_USER}' already exists.[/green]")
    else:
        uid = next_free_id(_taken_ids())
        console.print(
            f"Creating sandbox user [bold]{SANDBOX_USER}[/bold] (uid {uid}); "
            "you may be prompted for your admin password by sudo."
        )
        for cmd in create_user_commands(uid, uid, host_user=host_user):
            if subprocess.run(["sudo", *cmd]).returncode != 0:
                console.print(
                    f"[bold red]setup step failed:[/bold red] {' '.join(cmd)}"
                )
                raise typer.Exit(1)
        # Random password, piped via stdin (openssl rand → dscl . -passwd -).
        _set_random_password()

    # 2. Passwordless-sudo rule (needs a resolved interpreter to scope it).
    interp = resolve_python()
    if interp is None:
        console.print(
            "[bold red]No python3 found[/bold red] for the sandbox user "
            "(looked for Homebrew/Nix, then /usr/bin/python3).\n"
            "Install one (`brew install python` or `xcode-select --install`) "
            "and re-run `yolo macos-setup`."
        )
        raise typer.Exit(1)
    console.print(f"Installing passwordless-sudo rule at {SUDOERS_PATH} …")
    if not _install_sudoers(sudoers_rule(host_user, interp)):
        console.print(
            "[bold red]Could not install the sudoers rule.[/bold red] "
            "The run path would prompt for a password and hang."
        )
        raise typer.Exit(1)

    console.print(
        f"[green]✓ Sandbox user '{SANDBOX_USER}' + sudoers rule ready.[/green] "
        'Run agents with `runtime: "macos-user"` (or YOLO_RUNTIME=macos-user).'
    )


def _install_sudoers(rule_text: str, path: Path = SUDOERS_PATH) -> bool:
    """Validate + install ``rule_text`` as a sudoers.d policy (root:wheel 0440).

    Writes to a temp file, validates with ``visudo -cf`` (a bad rule is
    rejected rather than locking sudo), installs it root-owned 0440 with a
    dot-free name, then self-verifies the NOPASSWD lines are in effect via
    ``sudo -n -l`` (catches the silent-ignore-on-bad-name/perms case).
    """
    import tempfile

    try:
        with tempfile.NamedTemporaryFile(
            "w", prefix="yolo-sudoers-", delete=False
        ) as tf:
            tf.write(rule_text)
            tmp = tf.name
        # Validate first — never install an unparseable rule.
        if subprocess.run(["visudo", "-cf", tmp]).returncode != 0:
            os.unlink(tmp)
            return False
        # Install root-owned 0440 (install(1) sets owner+mode atomically).
        ok = (
            subprocess.run(
                [
                    "sudo",
                    "install",
                    "-m",
                    "0440",
                    "-o",
                    "root",
                    "-g",
                    "wheel",
                    tmp,
                    str(path),
                ]
            ).returncode
            == 0
        )
        os.unlink(tmp)
        if not ok:
            return False
        # Self-verify: the NOPASSWD rule must actually be in effect.
        return _passwordless_sudo_ok()
    except (OSError, subprocess.SubprocessError):
        return False


def macos_teardown() -> None:
    """Delete the sandbox account + home (needs admin).  macOS only."""
    import typer

    from .console import console

    if not _is_macos():
        console.print("[bold red]yolo macos-teardown requires macOS.[/bold red]")
        raise typer.Exit(1)
    # Remove the sudoers rule first (safe even if the account is already gone).
    subprocess.run(["sudo", "rm", "-f", str(SUDOERS_PATH)])
    if not _sandbox_user_exists():
        console.print(f"Sandbox user '{SANDBOX_USER}' does not exist — nothing to do.")
        return

    for cmd in delete_user_commands(host_user=_host_user()):
        subprocess.run(["sudo", *cmd])
    console.print(
        f"[green]✓ Removed sandbox user '{SANDBOX_USER}' + sudoers rule.[/green]"
    )


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
