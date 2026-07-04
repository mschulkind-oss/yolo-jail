"""Per-workspace AGENTS.md / CLAUDE.md generation and host-skill staging.

* generate_agents_md emits the in-jail briefing files (one per agent —
  Copilot, Gemini, Claude) into AGENTS_DIR/<cname>/.  The files are
  bind-mounted into the jail at boot; their content is the agent's
  primary source of truth for how this jail is set up.
* _prepare_skills + _copy_skill_subdirs stage per-agent skills dirs
  (skills-copilot/, skills-gemini/, skills-claude/) that get bind-mounted
  :ro at /home/agent/.<agent>/skills.  Each one mirrors its host
  counterpart 1:1 (~/.<agent>/skills/) plus the built-in jail-startup
  skill — no cross-agent merging.
"""

import shutil
import tomllib
from pathlib import Path
from typing import Any, Dict, List, Optional

from .paths import AGENTS_DIR


def _workspace_is_yolo_source_tree(workspace: Path) -> bool:
    """True when the workspace being jailed is itself a yolo-jail source
    checkout: ``src/cli/__init__.py`` present AND ``pyproject.toml``
    naming the ``yolo-jail`` project.  An absent, unreadable, or foreign
    pyproject reads as "not a yolo repo"."""
    if not (workspace / "src" / "cli" / "__init__.py").exists():
        return False
    try:
        data = tomllib.loads((workspace / "pyproject.toml").read_text())
    except (OSError, UnicodeDecodeError, tomllib.TOMLDecodeError):
        return False
    project = data.get("project")
    return isinstance(project, dict) and project.get("name") == "yolo-jail"


def generate_agents_md(
    cname: str,
    workspace: Path,
    blocked_tools: List[Dict[str, Any]],
    mount_descriptions: List[str],
    net_mode: str = "bridge",
    runtime: str = "podman",
    forward_host_ports: Optional[List] = None,
    loopholes: Optional[List[tuple]] = None,
    resources: Optional[Dict[str, Any]] = None,
    agents_md_extra: Optional[str] = None,
) -> Path:
    """Generate per-workspace AGENTS.md and CLAUDE.md files and return the directory.

    Produces separate files for Copilot, Gemini, and Claude so that user-level
    ~/.copilot/AGENTS.md, ~/.gemini/AGENTS.md, and ~/.claude/CLAUDE.md content
    can differ between the agents.

    ``agents_md_extra`` is appended verbatim to the generated jail-managed
    content (before host-level user content is prepended) so per-workspace
    or per-user notes — extra MCP server usage hints, project conventions,
    etc. — can ride along with each agent's briefing.
    """
    agents_dir = AGENTS_DIR / cname
    agents_dir.mkdir(parents=True, exist_ok=True)

    if net_mode == "host":
        network_line = "- **Network**: Host networking — the container shares the host network stack. `localhost` / `127.0.0.1` resolves directly to the host. No port mapping needed."
    else:
        network_line = "- **Network**: Bridge mode. Use `host.containers.internal` (resolves to 169.254.1.2) to reach the host."

    # Build forwarded host ports description
    forwarded_ports_lines = []
    if forward_host_ports and net_mode != "host":
        forwarded_ports_lines.append(
            "- **Forwarded Host Ports**: The following host services are available on `localhost` inside this container:"
        )
        for entry in forward_host_ports:
            if isinstance(entry, int):
                forwarded_ports_lines.append(
                    f"  - `localhost:{entry}` → host port {entry}"
                )
            elif isinstance(entry, str) and ":" in entry:
                parts = entry.split(":", 1)
                forwarded_ports_lines.append(
                    f"  - `localhost:{parts[0]}` → host port {parts[1]}"
                )
            elif isinstance(entry, str):
                forwarded_ports_lines.append(
                    f"  - `localhost:{entry}` → host port {entry}"
                )

    resource_line = []
    if resources:
        limits = ", ".join(f"{k}={v}" for k, v in sorted(resources.items()))
        resource_line = [
            f"- **Resource limits** (kernel-enforced): {limits}.  Sub-limit your own processes with `yolo-cglimit` (`--help` for usage)."
        ]

    # Provisioning-failure breadcrumb: only when the last boot actually
    # failed.  The briefing is refreshed on every yolo invocation, so a
    # failure surfaces here on the next attach even though the initial
    # generation ran before provisioning.
    provisioning_failed = []
    try:
        log_text = (workspace / ".yolo" / "startup.log").read_text(errors="replace")
        if "PROVISIONING FAILED" in log_text:
            provisioning_failed = [
                "## ⚠ Provisioning failed",
                "",
                "The last boot's provisioning failed — project tools may be missing.",
                "Read `/workspace/.yolo/startup.log` and self-serve (e.g. run",
                "`mise install` in /workspace, then re-run the step that failed).",
                "",
            ]
    except OSError:
        pass

    lines = [
        "# YOLO Jail Environment",
        "",
        "You are running inside a YOLO Jail — a sandboxed container.",
        "Jail tooling: `yolo --help`; config reference: `yolo config-ref`.",
        "",
        *provisioning_failed,
        "## Environment",
        "",
        f"- **Workspace**: `/workspace` is the host directory `{workspace}`,",
        "  bind-mounted LIVE — the same files, not a copy. Host-side edits are",
        "  instantly visible here and vice versa; there is never a git",
        "  pull/push, fetch, or any sync step between the jail and the host",
        "  for this directory.",
        "- **Home**: `/home/agent` (persistent across sessions)",
        "- **OS**: NixOS-based minimal container (no systemd, no sudo)",
        network_line,
        *forwarded_ports_lines,
        *resource_line,
        "",
        "⚠ rg is recursive by default — never pass grep-style `-r`/`-rn` flags",
        "(in rg, `-r` means `--replace` and silently corrupts match output).",
        "Use `rg -n <pattern> [path]`.",
        "",
    ]

    if loopholes:
        lines.append("## Loopholes — host capabilities wired into this jail")
        lines.append("")
        for name, desc in loopholes:
            first = (desc or "").split(". ")[0].split("\n")[0].strip().rstrip(".")
            lines.append(f"- **{name}**" + (f": {first}" if first else ""))
        lines.append("")
        lines.append("Details: `yolo loopholes list`.")
        lines.append("")

    if blocked_tools:
        lines.append("## Blocked Tools")
        lines.append("")
        lines.append("The following tools are blocked or shimmed in this project:")
        lines.append("")
        for tool in blocked_tools:
            name = tool.get("name", str(tool))
            msg = tool.get("message", "")
            sug = tool.get("suggestion", "")
            entry = f"- `{name}`"
            if msg:
                entry += f": {msg}"
            if sug:
                entry += f" Use `{sug}` instead."
            lines.append(entry)
        lines.append("")

    if mount_descriptions:
        lines.append("## Additional Context Mounts (read-only)")
        lines.append("")
        for m in mount_descriptions:
            host_path, container_path = m.split(":", 1) if ":" in m else (m, m)
            lines.append(f"- `{container_path}` (from host `{host_path}`)")
        lines.append("")

    lines.extend(
        [
            "## Limitations",
            "",
            "- Full internet access, but no host credentials (no ~/.ssh, no ~/.gitconfig): git push/pull and `gh auth login` will not work.",
            "- No sudo/root; context mounts under `/ctx/` are read-only.",
            "",
            "## Packages & Resource Limits",
            "",
            "To request a tool or a container-limit change: edit `/workspace/yolo-jail.jsonc`",
            "(`packages` / `resources`), ALWAYS run `yolo check` after every config edit",
            "(`yolo check --no-build` is fine inside a running jail), then ask the human to",
            "restart the jail. Reference: `yolo config-ref`.",
            "",
            "## Skills",
            "",
            "User-level skills dirs (`~/.<agent>/skills/`) are **read-only** in-jail",
            "(kernel-enforced); workspace-level ones (`/workspace/.<agent>/skills/`) are",
            "writable — develop there, then ask the human to promote to the host.",
            "",
        ]
    )

    if _workspace_is_yolo_source_tree(workspace):
        lines.extend(
            [
                "## Testing Changes to yolo-jail",
                "",
                "The `/workspace` directory is a bind mount of the host's repo, and it also",
                "backs `/opt/yolo-jail` — so nested jails launched from here run your edited",
                "`src/cli` code live.",
                "",
                "When modifying `src/cli/` or `src/entrypoint/`, **always verify with a nested",
                "jail** before telling the human to test on the host. Run `yolo -- bash` from",
                "inside this jail to launch one and confirm your changes work end-to-end.",
                "Container startup errors (mount failures, permission errors, read-only",
                "filesystem conflicts) are only caught by actually running the container —",
                "unit tests alone are not sufficient.",
                "",
                "**Important:** Changes to `src/cli/` take effect on the next `yolo` invocation",
                "on the host (no rebuild needed). Changes to `src/entrypoint/` or `flake.nix`",
                "require `just load && just install` on the host since the entrypoint is baked",
                "into the Nix image.",
                "",
            ]
        )

    jail_content = "\n".join(lines) + "\n"
    if agents_md_extra:
        extra = agents_md_extra.rstrip() + "\n"
        jail_content = jail_content + "\n" + extra

    home = Path.home()
    for agent, dotdir in [("copilot", ".copilot"), ("gemini", ".gemini")]:
        user_agents = home / dotdir / "AGENTS.md"
        if user_agents.exists():
            user_content = user_agents.read_text()
            content = user_content + "\n---\n\n" + jail_content
        else:
            content = jail_content
        _write_briefing(agents_dir / f"AGENTS-{agent}.md", content)

    # Claude reads ~/.claude/CLAUDE.md (not AGENTS.md) at the user-config level.
    user_claude = home / ".claude" / "CLAUDE.md"
    if user_claude.exists():
        claude_content = user_claude.read_text() + "\n---\n\n" + jail_content
    else:
        claude_content = jail_content
    _write_briefing(agents_dir / "CLAUDE.md", claude_content)

    return agents_dir


def _write_briefing(path: Path, content: str) -> None:
    """Truncate-in-place, but never through a hardlink.

    In-place truncation preserves the inode a running jail's file bind
    mount captured, so refreshes propagate (see _refresh_jail_briefings).
    But a hardlink-dedup pass (`yolo prune`) can fuse identical briefing
    files onto ONE inode — truncating through that link would clobber
    every sibling briefing with whichever agent's content is written
    last (observed 2026-07-04 after a host-side `yolo prune --apply`).
    A multi-linked file has already lost its live-refresh identity, so
    breaking the link with a fresh inode is strictly an improvement.
    """
    try:
        if path.lstat().st_nlink > 1:
            path.unlink()
    except OSError:
        pass
    path.write_text(content)


# ---------------------------------------------------------------------------
# Skills merging (host-side, for :ro bind mounts)
# ---------------------------------------------------------------------------

_BUILTIN_JAIL_STARTUP_SKILL = """\
---
name: jail-startup
description: First-run skill for agents entering a YOLO Jail. Reads the handover document left by the outer agent and orients you to the jail environment. Invoke this skill immediately when starting a new session inside a jail.
---

# Jail Startup

You are running inside a **YOLO Jail** — an isolated container environment.
This skill helps you pick up where the previous (outer) agent left off.

## Step 1: Read the Handover Document

The outer agent was REQUIRED to write a handover document before you were
launched. Read it now:

**Primary location:** `.yolo/handover.md` (i.e., `/workspace/.yolo/handover.md`)

If it exists, read it carefully — it contains:
- What the outer agent was working on
- What remains to be done
- Key decisions and rationale
- Files to look at first
- Gotchas and context you need

If the file does NOT exist, tell the human:
> "No handover document found at `.yolo/handover.md`. The outer agent should
> have created one. Can you tell me what I should be working on?"

## Step 2: Orient Yourself

Key facts about your environment:
- **Workspace** is at `/workspace` — this is the SAME directory as on the host (bind-mounted read-write). Changes you make are immediately visible on the host.
- **Internet** is available. You can curl, pip install, npm install, etc.
- **Home** is `/home/agent` — shared across ALL jail workspaces. Auth tokens, tool caches, and configs persist here.
- **Tools**: git, rg, fd, bat, jq, nvim, curl, gh, uv, mise, tmux, and more.
- **Runtimes**: Node.js, Python, Go (managed by mise).
- **Blocked tools**: Some tools may be shimmed (e.g., grep → rg). Check AGENTS.md or run `ls ~/.yolo-shims/` if you hit unexpected blocks. Set `YOLO_BYPASS_SHIMS=1` for scripts that need originals.
- **No pagers**: `PAGER=cat`. Never pipe to `less` or `more`.
- Run `yolo config-ref` for full configuration and environment reference.

## Step 3: Execute

After reading the handover document, proceed with the tasks described in it.
You have full capability — treat this as your primary working environment.
"""


def _prepare_skills(cname: str) -> Path:
    """Prepare per-agent skills directories on the host for :ro bind mounting.

    Each agent's staging dir mirrors its host counterpart 1:1:
      * skills-copilot/ ← ~/.copilot/skills/
      * skills-gemini/  ← ~/.gemini/skills/
      * skills-claude/  ← ~/.claude/skills/

    Plus the built-in ``jail-startup`` skill in every staging dir (it's
    not a host skill — it's our orientation doc).

    Workspace skills (``<workspace>/.{copilot,gemini,claude}/skills/``)
    are NOT collected here — agents already discover them natively from
    the workspace tree, so duplicating them into the user-level mount
    would surface the same skill twice.

    Returns the staging directory containing skills-copilot/,
    skills-gemini/, skills-claude/.
    """
    staging = AGENTS_DIR / cname
    staging.mkdir(parents=True, exist_ok=True)

    home = Path.home()

    for agent_suffix in ("copilot", "gemini", "claude"):
        skills_dir = staging / f"skills-{agent_suffix}"
        skills_dir.mkdir(exist_ok=True)
        # Clear contents *inside* skills_dir — never unlink skills_dir
        # itself.  `run_cmd` bind-mounts this exact path into the running
        # container at /home/agent/.<agent>/skills.  Linux bind mounts
        # capture the source's *inode* at mount time, not its path; if
        # we rmtree(skills_dir)+mkdir(skills_dir) here, mkdir allocates
        # a new inode and the container's mount stays pinned to the old
        # one — so attach-time refreshes (the whole point of the
        # `_refresh_jail_briefings` plumbing) silently no-op for any
        # running jail.  Removing entries *inside* the dir is fine: the
        # parent mount is unaffected and the container sees the updated
        # listing immediately.
        for child in skills_dir.iterdir():
            if child.is_dir():
                shutil.rmtree(child)
            else:
                child.unlink()

        # 1. Built-in skills (every agent gets jail-startup).
        builtin = skills_dir / "jail-startup"
        builtin.mkdir()
        (builtin / "SKILL.md").write_text(_BUILTIN_JAIL_STARTUP_SKILL)

        # 2. Host user-level skills — strictly per-agent.  Whatever's in
        # ~/.<agent>/skills/ is what the matching jail agent sees; no
        # cross-agent merging.  Deleting from one host dir cleanly
        # removes from the matching jail dir.
        _copy_skill_subdirs(home / f".{agent_suffix}" / "skills", skills_dir)

    return staging


def _copy_skill_subdirs(src: Path, dst: Path):
    """Copy skill subdirectories from src into dst, following symlinks."""
    if not src.is_dir():
        return
    for item in src.iterdir():
        if item.is_dir():
            target = dst / item.name
            if target.exists():
                shutil.rmtree(target)
            shutil.copytree(item, target, symlinks=False)
