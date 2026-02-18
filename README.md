# YOLO Jail

A secure, isolated container environment for AI agents (Copilot, Gemini CLI) to safely modify codebases without compromising host security or identity. Supports both Docker and Podman runtimes.

## Features

- **Isolated:** Runs in a Docker/Podman container with no access to host credentials.
- **Optimized:** Pre-installed with modern, fast tools (`rg`, `fd`, `bat`, `eza`, `jq`, `delta`, `fzf`).
- **Restricted:** Blocked tools return clear errors with suggestions (e.g., `rg` instead of `grep`).
- **Reproducible:** Defined entirely via Nix Flakes.
- **Agent-Ready:** Pre-configured MCP servers (Chrome DevTools, Sequential Thinking) and LSP servers (Pyright, TypeScript).

## Installation

Symlink the entry point to your PATH:
```bash
sudo ln -s $(pwd)/yolo-enter.sh /usr/local/bin/yolo
```

Optionally set user-level defaults:
```bash
yolo init-user-config
# edits: ~/.config/yolo-jail/config.jsonc
```

## Usage

Navigate to any repository and run:
```bash
# Start an interactive shell
yolo

# Run a command directly
yolo -- gemini prompt "Explain this code"
yolo -- copilot

# Force a new container (instead of reusing existing)
yolo --new -- bash
```

The jail mounts your current directory to `/workspace`. Auth and tool state are persisted in `~/.local/share/yolo-jail/` and isolated from host credentials.

## Configuration

Per-project config in `yolo-jail.jsonc`:
```jsonc
{
  "runtime": "podman",              // or "docker"
  "packages": ["strace", "htop"],   // extra nix packages
  "mounts": ["/path/to/ref-repo"],  // extra read-only mounts
  "security": {
    "blocked_tools": ["curl", "wget"]
  }
}
```

Workspace config merges over user defaults (`~/.config/yolo-jail/config.jsonc`). Lists merge+dedupe, scalars override.

## How It Works

1. **Build**: `nix build` produces a layered Docker image with all tools.
2. **Load**: Image is loaded into Docker/Podman (only when hash changes).
3. **Run**: Container starts with workspace bind-mounted, persistent home, and tool provisioning.
4. **Reuse**: Subsequent `yolo` invocations in the same workspace reuse the running container.

## Security

- **Strict Isolation**: No access to host `~/.ssh/`, `~/.gitconfig`, or cloud credentials.
- **Separate Auth**: Run `gh auth login` and `gemini login` inside the jail once.
- **User Mapping**: Files created in the jail are owned by your host user (matching UID/GID).
- **Blocked Tools**: Configurable list of tools that return clear error messages.
