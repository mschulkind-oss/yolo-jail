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
from pathlib import Path
from typing import Any, Dict, List, Optional

from .config import _effective_mcp_server_names
from .paths import AGENTS_DIR


def generate_agents_md(
    cname: str,
    workspace: Path,
    blocked_tools: List[Dict[str, Any]],
    mount_descriptions: List[str],
    net_mode: str = "bridge",
    runtime: str = "podman",
    forward_host_ports: Optional[List] = None,
    mcp_servers: Optional[Dict[str, Any]] = None,
    mcp_presets: Optional[List[str]] = None,
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

    mcp_server_names = _effective_mcp_server_names(mcp_servers, mcp_presets)

    lines = [
        "# YOLO Jail Environment",
        "",
        "You are running inside a YOLO Jail — a sandboxed container.",
        "",
        "## Environment",
        "",
        f"- **Workspace**: `/workspace` (mounted from host `{workspace}`)",
        "- **Home Directory**: `/home/agent` (persistent across sessions)",
        "- **OS**: NixOS-based minimal container (no systemd, no sudo)",
        network_line,
        *forwarded_ports_lines,
        "",
        "## Available Tools",
        "",
        "Standard CLI tools: git, rg (ripgrep), fd, bat, jq, nvim, curl, wget, strace, gh",
        "Runtimes: Node.js 22, Python 3.13, Go (managed by mise)",
        f"MCP Servers: {', '.join(mcp_server_names)}",
        "",
        "## Loopholes — controlled host access",
        "",
        "The jail may expose **loopholes**: sanctioned narrow passages through the jail wall for specific host-side capabilities (OAuth brokers, process views, log tailers, etc.). What's active in this jail depends on workspace/user config; list them with:",
        "",
        "```sh",
        "yolo loopholes list     # every loophole + its transport",
        "yolo loopholes status   # doctor self-check per loophole",
        "```",
        "",
        "If the command you need isn't in the standard toolset, a loophole may already expose it (e.g. `yolo-ps` for host processes). Don't enumerate them from memory — run `yolo loopholes list` to see what's actually wired up.",
        "",
    ]

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
            "- **No internet restrictions** but no host credentials (no ~/.ssh, no ~/.gitconfig).",
            "- **No pagers**: PAGER=cat, GIT_PAGER=cat. Do not pipe to less/more.",
            "- **Read-only mounts**: Context mounts under `/ctx/` are read-only.",
            "- **No sudo/root**: You run as a mapped host user with no privilege escalation.",
            "- **No git push/pull**: No GitHub credentials are available. Do not attempt `gh auth login` or SSH-based git operations.",
            "",
            "## Adding Packages",
            "",
            "If you need a tool that is not installed, you can request it:",
            "",
            "1. Edit `/workspace/yolo-jail.jsonc` and add the package to the `packages` array",
            "2. ALWAYS run `yolo check` after every config edit (`yolo check --no-build` is fine inside a running jail)",
            '3. If the check passes, tell the human user: "Please restart the jail so the new package becomes available"',
            "4. The human will see a config diff and confirm the change at next startup",
            "5. After restart, the package will be available",
            "",
            "Example — to add PostgreSQL tools (latest version):",
            "```json",
            '  "packages": ["postgresql"]',
            "```",
            "",
            "To pin a specific version, use an object with a nixpkgs commit hash:",
            "```json",
            '  "packages": [{"name": "freetype", "nixpkgs": "e6f23dc0..."}]',
            "```",
            "Find nixpkgs commits for specific versions at: https://lazamar.co.uk/nix-versions/",
            "",
            "To pull in a non-default nix output (e.g. C headers from `.dev`), use a "
            "dotted shorthand for one output, or the `outputs` field for several:",
            "```json",
            '  "packages": ["gtk4", "gtk4.dev"]',
            '  "packages": [{"name": "gtk4", "outputs": ["out", "dev"]}]',
            "```",
            "Common output names: out (default), dev (headers + .pc), bin, lib, man, doc.",
            "",
            "To override a version with an upstream source (when nixpkgs hasn't caught up):",
            "```json",
            '  "packages": [{"name": "freetype", "version": "2.14.1",',
            '    "url": "mirror://savannah/freetype/freetype-2.14.1.tar.xz",',
            '    "hash": "sha256-MkJ+jEcawJWFMhKjeu+BbGC0IFLU2eSCMLqzvfKTbMw="}]',
            "```",
            "Get the hash: run nix-prefetch-url <url>, or set hash to empty and nix reports it.",
            "",
            "Package names must match nixpkgs attributes (https://search.nixos.org/packages).",
            "Do NOT install packages via apt, nix-env, or other package managers.",
            "Run `yolo config-ref` for the full configuration reference.",
            "",
            "## Resource Management",
            "",
            "The jail may have hard resource limits set by the human operator (memory, CPU, PIDs).",
            "These are kernel-enforced — exceeding memory triggers OOM kill, exceeding PIDs prevents",
            "new processes. You cannot change container-level limits, but you can enforce hard limits",
            "on your own sub-processes using `yolo-cglimit`:",
            "",
            "### yolo-cglimit (recommended for hard limits)",
            "",
            "Located at `~/.local/bin/yolo-cglimit` (on PATH). Run `yolo-cglimit --help` for usage.",
            "",
            "```bash",
            "# Limit a training job to 75% of all CPUs",
            "yolo-cglimit --cpu 75 -- python train.py",
            "",
            "# 50% CPU + 2GB RAM",
            "yolo-cglimit --cpu 50 --memory 2g -- make -j8",
            "",
            "# Max 100 processes (prevent fork bombs)",
            "yolo-cglimit --pids 100 -- ./build.sh",
            "",
            "# Named cgroup for monitoring",
            "yolo-cglimit --cpu 75 --name training -- python train.py",
            "```",
            "",
            "These limits are enforced by the kernel via cgroup v2 — they cannot be exceeded.",
            "The tool communicates with a host-side daemon over a Unix socket; no elevated",
            "privileges are needed inside the jail. If the daemon is unavailable, `yolo-cglimit`",
            "will print an error with guidance.",
            "",
            "**How it works**: The yolo CLI runs a cgroup delegate daemon on the host alongside",
            "the container. When you call `yolo-cglimit`, it sends a JSON request to the daemon",
            "via `/run/yolo-services/cgroup-delegate.sock`. The daemon creates a child cgroup in the container's",
            "cgroup tree, sets limits, and moves your process into it using SO_PEERCRED for secure",
            "PID identity. All operations are logged for auditability.",
            "",
            "**Podman is the primary supported runtime** for cgroup delegation.",
            "",
            "### Soft limits (always available)",
            "",
            "| Tool | Purpose | Example |",
            "|------|---------|---------|",
            "| `nice` | Lower CPU priority | `nice -n 19 python train.py` |",
            "| `ionice` | Lower I/O priority | `ionice -c 3 python train.py` |",
            "| `timeout` | Wall-clock limit | `timeout 3600 python train.py` |",
            "| `ulimit` | Per-process limits | `ulimit -v 4000000` (4GB virtual mem) |",
            "",
            "For long-running jobs (training, builds), combine limits:",
            "```bash",
            "yolo-cglimit --cpu 75 --memory 4g -- nice -n 10 timeout 7200 python train.py",
            "```",
            "",
            "To request container-level resource limit changes, edit `/workspace/yolo-jail.jsonc`:",
            "```json",
            '  "resources": {"memory": "8g", "cpus": 4, "pids_limit": 4096}',
            "```",
            "Then run `yolo check --no-build` and ask the human to restart the jail.",
            "",
            "## Skills",
            "",
            "Skills directories (`~/.copilot/skills/`, `~/.gemini/skills/`, `~/.claude/skills/`)",
            "are **read-only** (kernel-enforced). You cannot create or modify skills inside the jail.",
            "If you attempt to write, you will get a 'Read-only file system' error — this is expected.",
            "",
            "To develop a new skill: create it in `/workspace/.copilot/skills/` (or `.gemini/`, `.claude/`),",
            "test it manually, then ask the human to promote it to their host-level skills directory",
            "outside the jail. The skill will be available in all jails after the next restart.",
            "",
            "## Testing Changes to yolo-jail",
            "",
            "The `/workspace` directory is a bind mount of the host's repo. Your edits to",
            "`src/cli.py` are **immediately visible to the host** — no commit or push needed.",
            "The host's `yolo` command reads from this shared working tree.",
            "",
            "When modifying `src/cli.py` or `src/entrypoint.py`, **always verify with a nested",
            "jail** before telling the human to test on the host. Run `yolo -- bash` from inside",
            "this jail to launch a nested jail and confirm your changes work end-to-end.",
            "Container startup errors (mount failures, permission errors, read-only filesystem",
            "conflicts) are only caught by actually running the container — unit tests alone are",
            "not sufficient.",
            "",
            "**Important:** Changes to `src/cli.py` take effect on the next `yolo` invocation",
            "on the host (no rebuild needed). Changes to `src/entrypoint.py` or `flake.nix`",
            "require `just load && just install` on the host since the entrypoint is baked",
            "into the Nix image.",
            "",
            "## First Session — Handover",
            "",
            "If this is your first session in this jail, invoke the **jail-startup** skill.",
            "It reads the handover document at `.yolo/handover.md` left by the outer agent",
            "and orients you to the jail environment. The human may ask you to invoke it —",
            'just say "invoke the jail-startup skill" or use your skill invocation tool.',
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
        (agents_dir / f"AGENTS-{agent}.md").write_text(content)

    # Claude reads ~/.claude/CLAUDE.md (not AGENTS.md) at the user-config level.
    user_claude = home / ".claude" / "CLAUDE.md"
    if user_claude.exists():
        claude_content = user_claude.read_text() + "\n---\n\n" + jail_content
    else:
        claude_content = jail_content
    (agents_dir / "CLAUDE.md").write_text(claude_content)

    return agents_dir


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
