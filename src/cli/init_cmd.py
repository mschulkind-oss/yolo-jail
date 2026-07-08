"""``yolo init`` and ``yolo init-user-config`` — one-time scaffolding commands.

Both write a starter jsonc with comments explaining each field, and
``yolo init`` follows up with a long agent-briefing block (printed via
_print_init_briefing) so the human/agent operator knows what they're
about to step into.

The Typer commands are registered in cli/__init__.py.  This module
just exports the function bodies + briefing helper.
"""

import json
from pathlib import Path
from typing import List

import typer

from .console import console
from .paths import USER_CONFIG_PATH


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

  // Coding agents to install in the jail (only these are installed).
  // Available: claude, copilot, gemini, opencode, pi.  Default: ["claude"].
  // A workspace list REPLACES the user-level default (it does not union),
  // so you can narrow to just the agents this project uses.
  // "agents": ["claude", "opencode"],

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
  Agents:    the ones in your `agents` config (default: claude). Available:
             claude, copilot, gemini, opencode, pi (auto-approve wired up).
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
     Tell them to run: yolo -- claude  (or copilot / gemini / opencode / pi,
     whichever is in the `agents` config)

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
