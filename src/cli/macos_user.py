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
# Root-owned, 0444 state dir holding the per-session Seatbelt profile and
# the entrypoint bootstrap the sandbox user runs.  Root-owned so the
# sandbox user cannot rewrite its own sandbox profile.
STATE_DIR = Path("/var/yolo-jail")


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
    setgid-group share.
    """
    return {
        "dir": f"group:{group} allow {_DIR_RIGHTS}",
        "file_inherit": f"group:{group} allow {_FILE_INHERIT_RIGHTS}",
        "file": f"group:{group} allow {_FILE_RIGHTS}",
    }


def workspace_acl_apply_script(workspace: Path, group: str = SANDBOX_GROUP) -> str:
    """A ``find``-based bash script applying the split ACEs in one pass.

    Directories get the dir ACE + the file-inherit template; non-directories
    get the file ACE.  ``chmod -h`` so symlinks aren't followed (avoid a
    swap race).  Idiomatic and idempotent (re-adding an identical ACE is a
    no-op).  Returned as text so it can be asserted in tests and run via
    ``bash -c`` on macOS.
    """
    aces = workspace_acl_aces(group)
    ws = str(workspace)
    return (
        "set -euo pipefail\n"
        f"ws={_sh_quote(ws)}\n"
        # Directories: dir rights + inheritance template (two -a ACEs).
        'find "$ws" -type d -print0 | while IFS= read -r -d "" d; do\n'
        f'  chmod -h +a {_sh_quote(aces["dir"])} "$d"\n'
        f'  chmod -h +a {_sh_quote(aces["file_inherit"])} "$d"\n'
        "done\n"
        # Everything else (files, symlinks): file rights, no inherit.
        'find "$ws" ! -type d -print0 | while IFS= read -r -d "" f; do\n'
        f'  chmod -h +a {_sh_quote(aces["file"])} "$f"\n'
        "done\n"
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

;; --- Other users' homes: deny reads under /Users, re-allow traversal
;;     entries + the workspace + this sandbox user's own home ---
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


def launch_argv(
    agent_argv: List[str],
    *,
    profile_path: Path,
    sandbox_env: Dict[str, str],
    user: str = SANDBOX_USER,
    home: Path = SANDBOX_HOME,
) -> List[str]:
    """Build the ``sudo -u … env -i … sandbox-exec -f … -- <agent>`` argv.

    ``env -i`` is load-bearing: without a scrubbed env, HOME still resolves
    to the *admin* home and the agent reads the host user's
    ``~/.gitconfig``/``~/.ssh`` — the #1 documented footgun.  ``sudo -u``
    (not ``launchctl asuser``) preserves the controlling TTY so the agent
    REPL works interactively.  ``sandbox_env`` is layered after the fixed
    HOME/USER/SHELL/PATH so a caller can inject e.g. git identity or a
    provider API key, but can't accidentally drop HOME.
    """
    env_pairs: List[str] = [
        f"HOME={home}",
        f"USER={user}",
        "SHELL=/bin/zsh",
        "PATH=/usr/bin:/bin:/usr/sbin:/sbin",
    ]
    for k, v in sandbox_env.items():
        if k in ("HOME", "USER", "SHELL"):
            continue  # never let a caller override the identity trio
        env_pairs.append(f"{k}={v}")
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
        *agent_argv,
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

    Returned as text (not executed) so it is unit-testable and can be
    written to the root-owned STATE_DIR before the sandbox user runs it.
    """
    import json

    return f"""\
#!/usr/bin/env python3
# yolo-jail macOS-user entrypoint bootstrap (generated).  Runs AS the
# sandbox user to populate its home with shims + agent configs natively.
import os
import sys

sys.path.insert(0, {str(repo_src)!r})
os.environ["YOLO_AGENTS"] = {json.dumps(json.dumps(agents))}
os.environ.setdefault("YOLO_HOST_DIR", {str(workspace)!r})

import entrypoint

# Rebind the entrypoint's path constants to native macOS locations.  On a
# real host $HOME is the sandbox user's home; there is no /mise store and
# no /workspace mount, so point everything at the sandbox home + the real
# workspace path.
home = {str(sandbox_home)!r}
entrypoint.HOME = __import__("pathlib").Path(home)
entrypoint.WORKSPACE = __import__("pathlib").Path({str(workspace)!r})

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
print("yolo-jail macos-user bootstrap ok")
"""


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
) -> int:
    """Launch ``agent_argv`` in the dedicated-user + Seatbelt sandbox.

    Steps (all macOS-only; the builders they call are Linux-tested):

      1. Preconditions: macOS, ``sandbox-exec`` present, sandbox account
         provisioned.  Fail closed with an actionable message otherwise.
      2. Write the per-session Seatbelt profile to the root-owned STATE_DIR
         (``sudo tee`` + ``chmod 0444`` — the sandbox user can't edit its own
         profile).
      3. Apply the inheriting workspace ACL so the sandbox user shares the
         repo rw on the same inodes.
      4. Run the entrypoint bootstrap AS the sandbox user to populate its
         home with shims + per-agent configs natively.
      5. Launch the agent under ``run_with_proxy`` via
         ``sudo -u … env -i … sandbox-exec -f profile -- <agent>``.

    Returns the agent's exit code (or 1 on a precondition/setup failure).
    """
    from .console import console
    from .tty_proxy import run_with_proxy

    if not _is_macos():
        console.print(
            "[bold red]runtime 'macos-user' requires macOS.[/bold red] "
            "Use 'podman' or 'container' on this host."
        )
        return 1
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

    cname = _cname(workspace)
    profile_path = session_profile_path(cname)

    # 2. Install the root-owned Seatbelt profile (0444).
    if not _install_root_file(profile_path, seatbelt_profile(workspace, SANDBOX_HOME)):
        console.print(f"[bold red]Could not write Seatbelt profile {profile_path}")
        return 1

    # 3. Share the workspace via the inheriting ACL.
    acl_script = workspace_acl_apply_script(workspace)
    if subprocess.run(["bash", "-c", acl_script]).returncode != 0:
        console.print(
            "[yellow]workspace ACL grant reported an error — the sandbox "
            "user may not have full rw. Try `yolo macos-fix-permissions`.[/yellow]"
        )

    # 4. Bootstrap the sandbox user's home (shims + agent configs), natively.
    boot = entrypoint_bootstrap_script(
        repo_src, workspace=workspace, sandbox_home=SANDBOX_HOME, agents=agents
    )
    boot_path = STATE_DIR / f"bootstrap-{cname}.py"
    if not _install_root_file(boot_path, boot, mode="0444"):
        console.print(f"[bold red]Could not write bootstrap {boot_path}")
        return 1
    boot_rc = subprocess.run(
        [
            "sudo",
            "--login",
            f"--user={SANDBOX_USER}",
            "/usr/bin/python3",
            str(boot_path),
        ]
    ).returncode
    if boot_rc != 0:
        console.print(
            "[yellow]entrypoint bootstrap returned non-zero — agent configs "
            "may be incomplete; continuing.[/yellow]"
        )

    # 5. Launch under the TTY proxy.
    env = macos_sandbox_env(config)
    if sandbox_env:
        env.update(sandbox_env)
    argv = launch_argv(agent_argv, profile_path=profile_path, sandbox_env=env)
    return run_with_proxy(argv)


def _install_root_file(path: Path, content: str, mode: str = "0444") -> bool:
    """Write ``content`` to a root-owned file at ``path`` (mode ``0444``).

    Uses ``sudo mkdir -p`` + ``sudo tee`` + ``sudo chmod`` so the file is
    owned by root and unwritable by the sandbox user — the sandbox must not
    be able to edit its own Seatbelt profile or bootstrap script.
    """
    try:
        if subprocess.run(["sudo", "mkdir", "-p", str(path.parent)]).returncode != 0:
            return False
        proc = subprocess.run(
            ["sudo", "tee", str(path)],
            input=content.encode(),
            stdout=subprocess.DEVNULL,
        )
        if proc.returncode != 0:
            return False
        return subprocess.run(["sudo", "chmod", mode, str(path)]).returncode == 0
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
