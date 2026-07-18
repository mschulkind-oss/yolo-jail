# YOLO Jail: Agent Developer Guide

This project provides a secure, isolated container environment for AI agents (Gemini CLI, Copilot, Claude Code) to execute commands on local repositories without compromising host security or identity. Supports both Docker and Podman runtimes.

## Architectural Specs

### 1. Configuration (`yolo-jail.jsonc`)
- **Format**: JSON with comments (JSONC).
- **Location**: Project root.
- **User Defaults**: Optional global config at `~/.config/yolo-jail/config.jsonc` (create with `yolo init-user-config`).
- **Merge Rules**: Workspace config is merged over user config. Lists are merged+deduped; scalar/object values in workspace override user defaults.
- **Dynamic Shims**: Blocked tools are generated dynamically based on this config. All blocked tools are unconditionally blocked unless `YOLO_BYPASS_SHIMS=1` is set.
- **Custom Packages**: The `packages` array specifies additional nix packages to bake into the jail image. Names must match nixpkgs attribute names. The image only rebuilds when this list changes. Uses `--impure` nix build with `builtins.getEnv`.
- **Extra Mounts**: The `mounts` array brings additional host paths into the jail read-only at `/ctx/<basename>` (or a custom container path via `"host:container"` syntax).
- **Runtime Selection**: The `"runtime"` key selects the container runtime (`"podman"` or `"docker"`). Can also be set via `YOLO_RUNTIME` env var. Priority: env var > workspace config > user config > auto-detect (prefers podman).
- **Network Mode**: The `network.mode` key selects network isolation (`"bridge"` default or `"host"`). Bridge mode isolates the container network; host mode shares the host network stack. When using bridge mode, `network.ports` (e.g., `["8000:8000"]`) publishes container ports to the host.
- **Mise Tools Injection**: The `"mise_tools"` object injects tools into the jail's global mise config (`~/.config/mise/config.toml`), not the workspace `mise.toml`. Default: `{"neovim": "stable"}`. Override in workspace or user-level `yolo-jail.jsonc`. Deep-merged across config levels. Does not trigger an image rebuild.
- **LSP Servers**: The `"lsp_servers"` object adds language servers for Copilot and Gemini (Claude uses its own built-in tools). Default servers: python (pyright), typescript, go (gopls). Workspace servers are merged with defaults — add new ones or override existing. Each server specifies `command`, `args`, and `fileExtensions`. The entrypoint auto-translates: Copilot gets native LSP config; Gemini gets servers wrapped via `mcp-language-server`. See `yolo config-ref` for format details.
- **MCP Presets**: The `"mcp_presets"` array enables built-in MCP server presets by name. No presets are enabled by default. Available presets: `chrome-devtools`, `sequential-thinking`. Example: `"mcp_presets": ["chrome-devtools"]`. Custom MCP servers are added via `"mcp_servers"` (same as before). Set a server to `null` in `mcp_servers` to disable a preset or inherited server. MCP servers are configured for all three agents (Copilot, Gemini, Claude).
- **Device Passthrough**: The `"devices"` array passes host devices into the jail. Supports USB by vendor:product ID (`{"usb": "0bda:2838"}`), raw paths (`"/dev/bus/usb/001/004"`), or cgroup rules (`{"cgroup_rule": "c 189:* rwm"}`). USB IDs are resolved to paths at startup via `lsusb`. Missing devices produce a warning, not an error. Subject to config change safety.
- **GPU Passthrough**: The `"gpu"` object enables NVIDIA GPU access inside the jail. Config: `{"gpu": {"enabled": true, "devices": "all", "capabilities": "compute,utility"}}`. Docker uses `--gpus`; Podman uses CDI (`nvidia.com/gpu=...`). Requires NVIDIA Container Toolkit on the host. `yolo check` validates GPU readiness (driver, toolkit, CDI spec). See `docs/research/nvidia-gpu-passthrough.md` for full setup guide including AWS EC2 instructions.
- **Resource Limits**: The `"resources"` object sets hard cgroup constraints on the jail container. Supports `memory` (e.g. `"8g"`), `cpus` (e.g. `4` or `"0.5"`), and `pids_limit` (e.g. `4096`). These map directly to `--memory`, `--cpus`, and `--pids-limit` Docker/Podman flags and are enforced by the kernel. Exceeding the memory limit triggers OOM kill; exceeding pids_limit prevents new process creation. Subject to config change safety.
- **Config Change Safety**: When the config changes between jail startups, the CLI shows a normalized diff and asks the human for y/N confirmation. This prevents agents from silently adding packages or mounts. See `docs/config-safety.md` for the full user/agent workflow.
- **Config Validation Rule**: After **every** edit to `yolo-jail.jsonc` or `~/.config/yolo-jail/config.jsonc`, run `yolo check` before asking for a restart or starting a new jail. Use `yolo check --no-build` inside a running jail for a quick preflight.
- **In-Jail CLI**: The `yolo` command is available inside all jails (mounted from `/opt/yolo-jail`). Agents can run `yolo --help`, `yolo check`, or `yolo config-ref` for full documentation. The CLI can also be used for nested jailing.

### 2. Isolation & Identity
- **Strict Isolation**: The jail MUST NOT access host `~/.ssh/`, `~/.gitconfig`, or any cloud credentials.
- **Login inside the Jail**: Users must perform a one-time `gh auth login`, `gemini login`, and `/login` (inside Claude) within the jail.
- **Persistent Global State**: All jail-specific state (auth tokens, bash history, global tool cache) is stored in `~/.local/share/yolo-jail/`.
    - Host `~/.local/share/yolo-jail/home` → Container `/home/agent` (auth, tools, and shared defaults)
    - Host `~/.local/share/mise` → Container at same path + `/mise` symlink (shared mise data dir — venv paths resolve on both sides)
    - Per-workspace overlays: `<workspace>/.yolo/home/copilot-sessions` → `/home/agent/.copilot/session-state`, `<workspace>/.yolo/home/copilot-command-history` → `/home/agent/.copilot/command-history-state.json`, `<workspace>/.yolo/home/bash_history` → `/home/agent/.bash_history`, `<workspace>/.yolo/home/gemini-history` → `/home/agent/.gemini/history`, `<workspace>/.yolo/home/ssh` → `/home/agent/.ssh`, `<workspace>/.yolo/home/copilot-mcp-config.json` → `/home/agent/.copilot/mcp-config.json`, `<workspace>/.yolo/home/copilot-lsp-config.json` → `/home/agent/.copilot/lsp-config.json`, `<workspace>/.yolo/home/gemini-settings.json` → `/home/agent/.gemini/settings.json`, `<workspace>/.yolo/home/gemini-managed-mcp.json` → `/home/agent/.gemini/yolo-managed-mcp-servers.json`, `<workspace>/.yolo/home/claude-settings.json` → `/home/agent/.claude/settings.json`, `<workspace>/.yolo/home/claude-managed-mcp.json` → `/home/agent/.claude/yolo-managed-mcp-servers.json`, `<workspace>/.yolo/home/claude-projects` → `/home/agent/.claude/projects`

### 3. Execution Engine (`src/cli.py` & `src/entrypoint.py`)

All logic is **pure Python** — no bash scripts with embedded heredocs. The only bash is generated *content* (shim scripts, .bashrc) written by Python.

- **Architecture**: `cli.py` runs on the host (typer CLI). `entrypoint.py` runs inside the container at startup (stdlib-only Python, no pip deps).
- **Self-Bootstrapping**: The jail is developed from inside itself. Changes to source files are immediately visible (bind-mounted workspace). Changes to `flake.nix` or `entrypoint.py` require a nix rebuild on the next `yolo` invocation from the host.
- **Config Edits**: Any edit to `yolo-jail.jsonc` must be followed by `yolo check` before asking a human to restart into a new jail session.
- **Direct Execution**: Commands are run via `yolo -- <command>`.
- **Auto-YOLO**: The CLI automatically injects `--yolo` for `gemini` and `copilot` commands. Claude Code uses `settings.json` (comprehensive `permissions.allow` rules) instead of a CLI flag or `bypassPermissions` mode, because both `--dangerously-skip-permissions` and `bypassPermissions` refuse to run as UID 0 (the norm in Podman rootless containers).
- **Container Reuse**: By default, running `yolo` in the same workspace reuses the existing container via `exec` instead of creating a new one. Containers are named deterministically (`yolo-<hash>`) based on the workspace path. Use `yolo --new -- <command>` to force a new container. Use `yolo ps` to list active jails with their workspace mappings. Tracking files are stored in `~/.local/share/yolo-jail/containers/`.
- **Quoting**: Use `shlex.join` in Python to pass quoted arguments correctly to the container's `bash -c`.
- **Self-Updating Build**: The CLI runs `nix build --impure` on every start but only executes `<runtime> load` if the resulting image hash differs from `.last-load`. The `--impure` flag allows reading the `YOLO_EXTRA_PACKAGES` env var for per-project package customization.
- **Runtime Differences**: Docker uses `-u UID:GID` and `--net=bridge` explicitly. Podman rootless omits both (rootless UID mapping handles ownership, pasta networking avoids nftables).
- **Nested Containers (Podman-in-Podman)**: The jail image includes `podman`, `nix`, `fuse-overlayfs`, `slirp4netns`, and `shadow`. When running with podman, the CLI automatically adds UID/GID mappings (`--uidmap`/`--gidmap`), `/dev/fuse`, and `SYS_ADMIN`+`MKNOD` capabilities for rootless nested container support — no `--privileged` needed. When already inside a container, the CLI detects this (`/run/.containerenv` or `/.dockerenv`) and uses `--userns=host` instead of UID/GID mapping to share the parent's user namespace — doubly-nested user namespaces fail on `/proc` mount. Inner containers must use `--net=host` and `--cgroups=disabled` (configured as defaults in the image's `/etc/containers/containers.conf`). The CLI also forces `--net=host` when inside a container since netavark can't create network namespaces without `NET_ADMIN`.
- **Nix Builds Inside Jail**: When the host has a nix daemon (`/nix/var/nix/daemon-socket`), the CLI automatically mounts it plus `/nix/store:ro` and sets `NIX_REMOTE=daemon`. This forces nix inside the jail to delegate builds to the host daemon (which has nixbld users and permissions), avoiding the "build users group has no members" error. The read-only store mount provides cache hits; new derivations built by the host daemon are visible through the bind mount.
- **Reaching the Host from Inside Jail**: 
    - **Podman**: Use `host.containers.internal` (resolves to `169.254.1.2`). Automatically added to `/etc/hosts`.
    - **Docker**: Use `172.17.0.1` (default bridge gateway IP). The CLI adds this to `/etc/hosts` as `host.internal` for convenience. Useful for agents that need to access host services (e.g., pull from host git servers, reach a development API running on the host).
- **Host Port Forwarding**: The `network.forward_host_ports` config forwards host `127.0.0.1` services into the jail via Unix socket tunneling (analogous to SSH `-L`). Architecture:
    1. **Host side** (`cli.py`): Creates `/tmp/yolo-fwd-{cname}/` with socat processes that UNIX-LISTEN on socket files and TCP-connect to host `127.0.0.1:{port}`.
    2. **Container side** (`entrypoint.py`): Starts socat processes that TCP-LISTEN on `127.0.0.1:{port}` and UNIX-CONNECT to the bind-mounted socket files at `/tmp/yolo-fwd/`.
    3. **Why Unix sockets**: Container networking (pasta, slirp4netns, bridge) cannot reach host `127.0.0.1` directly when ports are already bound. Unix sockets bypass all networking via a bind-mounted directory. No network exposure.
    4. **Ordering**: Host socat starts first (creates sockets) → container mounts dir → entrypoint starts container socat. No race condition.
    5. **Cleanup**: When container exits, `cli.py` terminates host socat and removes socket dir.
    6. **Container reuse**: `_port_in_use()` guard in entrypoint prevents duplicate listeners on `exec` into existing container.
    7. **Requires**: `socat` on the host (already in the jail image).
- **AGENTS Injection**: Per-workspace AGENTS.md and CLAUDE.md are generated host-side by `cli.py` and stored at `~/.local/share/yolo-jail/agents/<container-name>/`. AGENTS.md is mounted read-only over `~/.copilot/AGENTS.md` and `~/.gemini/AGENTS.md`; CLAUDE.md is mounted over `~/.claude/CLAUDE.md` (Claude only reads CLAUDE.md at the user-config level). This ensures each workspace jail gets its own context without stomping the shared home directory, and outside-jail agents never see jail-specific instructions.
- **Skills Auto-Mount**: Host user-level skills from `~/.gemini/skills/` (which `~/.copilot/skills` typically symlinks to) are automatically mounted and synced into the jail at `/home/agent/.copilot/skills/`, `/home/agent/.gemini/skills/`, and `/home/agent/.claude/skills/`. If a workspace has `.copilot/skills/`, `.gemini/skills/`, or `.claude/skills/`, those skills are also synced and take precedence. Symlinks in skill directories are followed automatically.
- **Built-in Skills**: The `jail-startup` skill is auto-injected into every jail by `entrypoint.py`. It reads `.yolo/handover.md` and orients the inner agent. Priority order: built-in (lowest) → host user-level → workspace (highest). Built-in skills can be overridden by placing a skill with the same directory name in host or workspace skills.

## Developer Runbook

### Self-Bootstrapping Development
This project is developed **from inside the jail itself**. The source code is bind-mounted at `/workspace`, so edits are immediately visible on the host.
- **Source changes** (`src/cli.py`, `src/entrypoint.py`): Visible immediately, take effect on next jail start.
- **Image changes** (`flake.nix`, `src/entrypoint.py`): Require `nix build` + image reload on next `yolo` from the host. The CLI auto-rebuilds when it detects changes.
- **Test changes**: Run `uv run --group dev python -m pytest tests/` from inside the jail or on the host.
- **Always commit and push** after changes — the nix image builds from the working tree.

### Testing
- **Host**: `uv run --group dev python -m pytest tests/` — all tests run (unit + integration).
- **Inside Jail**: All tests should work. The CLI detects it's inside a container and uses `--userns=host` for nested containers instead of creating new user namespaces.
- **Entrypoint unit tests**: `uv run --group dev python -m pytest tests/test_entrypoint.py` — tests config generation (shims, MCP, LSP, bashrc) without containers.

### First Run vs Subsequent Runs
- **First Run**: When you run `yolo -- <command>`, the jail entrypoint automatically provisions all tools:
  1. Builds the Docker image via `nix build --impure` (if config changed)
  2. Loads the image into Docker (if hash differs from `.last-load`)
  3. Runs the bootstrap script to install MCP servers, language servers, and utilities
  4. Executes your command
  - This takes longer (npm/go installs + potential image rebuild)
- **Subsequent Runs**: Tools are cached in persistent storage (`~/.local/share/yolo-jail/home`), so:
  1. Bootstrap script runs but skips installation (tools already exist)
  2. Your command executes immediately
  - Much faster than first run

### Debugging MCP & LSP
- **Logs**: 
  - **Copilot**: Inside jail: `~/.copilot/logs/`. On host: `~/.local/share/yolo-jail/home/.copilot/logs/`.
  - **Gemini**: Inside jail: `~/.cache/gemini-cli/logs/`. On host: `~/.local/share/yolo-jail/home/.cache/gemini-cli/logs/`.
  - **Claude**: Inside jail: `~/.claude/projects/`. On host: `~/.local/share/yolo-jail/home/.claude/projects/` (per-project conversation logs).
- **Viewing Logs from Inside Jail**:
  ```bash
  # List recent logs
  yolo -- bash -lc 'ls -lt ~/.copilot/logs/ | head -5'
  
  # View latest log
  yolo -- bash -lc 'tail -100 ~/.copilot/logs/$(ls -1t ~/.copilot/logs | head -1)'
  
  # Watch logs in real-time (open in one tmux pane, run copilot in another)
  yolo -- bash -lc 'tail -f ~/.copilot/logs/$(ls -1t ~/.copilot/logs | head -1)'
  
  # Search for MCP errors
  yolo -- bash -lc 'grep -i "MCP\|Failed\|Error" ~/.copilot/logs/$(ls -1t ~/.copilot/logs | head -1)'
  ```
- **Common MCP Errors**:
  - `libstdc++.so.6: cannot open shared object file`: Node wrapper not used or `LD_LIBRARY_PATH` stripped. Check MCP config uses `/home/agent/.local/bin/mcp-wrappers/node`. The chrome-devtools wrapper sets its own `LD_LIBRARY_PATH` to be self-contained.
  - `Cannot find module '/bin/chrome-devtools-mcp'`: The chrome wrapper failed to resolve NPM_CONFIG_PREFIX. This means `$HOME` or `$NPM_CONFIG_PREFIX` wasn't set in the spawned environment.
  - `Protocol error (Target.setDiscoverTargets): Target closed`: Chrome DevTools MCP often hits this when reusing the persistent Chrome profile. Use `--isolated` in MCP args so each session gets a fresh temp profile.
  - `Runtime.callFunctionOn timed out` on complex pages: typically caused by missing fontconfig defaults in Nix images. Ensure `/etc/fonts` is present and `FONTCONFIG_FILE/FONTCONFIG_PATH` are set.
  - `Connection closed`: MCP server crashed or failed to start. Check server binary is installed (`npm list -g` inside jail).
  - `argument list too long`: Shim conflict or PATH issue. Check `.local/bin/` is not in PATH (should only be used by absolute MCP paths).
- **Config Locations**:
  - **Copilot**: `~/.copilot/config.json` (main), `~/.copilot/mcp-config.json` (MCP servers), `~/.copilot/lsp-config.json` (LSP servers).
  - **Gemini**: `~/.gemini/settings.json` (all config including MCP/LSP).
  - **Claude**: `~/.claude/settings.json` (MCP servers in `mcpServers` key, permissions in `permissions.defaultMode`).
- **Workspace MCP Shadowing**: The CLI shadows any workspace `.vscode/mcp.json` with `/dev/null` inside the jail so agents only use the jail's MCP config. Host VS Code MCP configs won't interfere.
- **Chromium Stability**: Headless Chromium in Docker is brittle. 
    - **Launch Mode**: Chrome DevTools MCP launches Chromium directly via Puppeteer's `pipe: true` mode with `--headless --isolated --executablePath /usr/bin/chromium`. Docker-required flags (`--no-sandbox`, `--disable-dev-shm-usage`, etc.) are passed via `--chrome-arg=...`.
    - **Required Chrome Flags**: `--no-sandbox`, `--disable-setuid-sandbox`, `--disable-dev-shm-usage`, `--disable-gpu`, `--disable-software-rasterizer`.
    - **Docker**: Use `--shm-size=2g` in the Docker run command for adequate shared memory.
    - **Binary Discovery**: Always use absolute paths (e.g., `/usr/bin/chromium`) in MCP configs.
- **LSP Config Format**:
    - **Copilot**: Uses `~/.copilot/lsp-config.json` with `lspServers` (plural) key.
    - **Gemini**: LSP servers are wrapped as MCP servers via `mcp-language-server` in `~/.gemini/settings.json`.
    - **Default Servers**: Python (pyright), TypeScript, Go (gopls) — always present.
    - **Workspace Customization**: Add `"lsp_servers"` to `yolo-jail.jsonc` to add or override servers. See `yolo config-ref`.
    - **Format**: `fileExtensions` must be an object mapping extensions to language IDs, e.g., `{".py": "python", ".pyi": "python"}`, not an array.
    - **Lazy Loading**: LSP servers are spawned on-demand when Copilot/Gemini analyze code files, not as persistent background services.
    - **Testing**: To verify LSP works, ask the agent to analyze a file with type errors: `@file.py check for type errors`.
    - **TypeScript Requirements**: TypeScript LSP requires a `tsconfig.json` or `jsconfig.json` in the workspace root. Without it, typescript-language-server throws `ThrowNoProject` errors. Python LSP (pyright) works without configuration.
- **Node Wrappers**: `~/.local/bin/mcp-wrappers/node` and `npx` are wrapper scripts that set `LD_LIBRARY_PATH` and fontconfig defaults before calling the mise-installed binary. MCP configs use absolute paths to these wrappers.
- **Self-Contained Wrappers**: MCP wrapper scripts (node, npx) set their own runtime env (`LD_LIBRARY_PATH`, `FONTCONFIG_*`) and use `$HOME`-relative paths instead of calling `npm config` at runtime. This ensures they work even when agents sanitize the environment. Never use `subprocess` or `npm config get` in wrapper scripts.

### Tool Management
- **Mise**: All runtimes (Node, Python, Go) are managed by `mise`. 
- **Auto-Provisioning**: On every jail start, the CLI runs `~/.yolo-bootstrap.sh` with `YOLO_BYPASS_SHIMS=1` (to avoid shim interference) before executing the user's command. The bootstrap script and all config files (MCP, LSP, bashrc, shims) are generated by `src/entrypoint.py` (pure Python, stdlib only). Tools are installed only if missing (idempotent).
  - **NPM Globals**: `chrome-devtools-mcp`, `@modelcontextprotocol/server-sequential-thinking`, `pyright`, `typescript-language-server`, `typescript`, `@anthropic-ai/claude-code` (Copilot and Gemini agents)
  - **Go Binaries**: `mcp-language-server` (used by Gemini LSP), `gopls` (Go language server)
  - **Claude Code**: Installed via native installer (`curl claude.ai/install.sh | bash`), binary at `~/.local/bin/claude`
  - **Python**: `showboat` (if pip is available)
- **Persistent Storage**: All installed binaries live in `~/.local/share/yolo-jail/home/` on the host, so they survive jail restarts and are reused without reinstalling.
- **Binary Locations**:
    - NPM Globals: `/home/agent/.npm-global/bin/`
    - Go Binaries: `/home/agent/go/bin/`
    - Claude Code: `/home/agent/.local/bin/claude`
    - MCP Node Wrappers: `/home/agent/.local/bin/mcp-wrappers/`
- **PATH Order**: `${SHIM_DIR}:$HOME/.local/bin:/home/agent/.npm-global/bin:/home/agent/go/bin:/mise/shims:/bin:/usr/bin`.

### Agent Package Management

Agents inside the jail can install and manage additional tools via **`mise`**, which persists across jail restarts and isolates tools per workspace.

**Key Concept**: Add tools to your workspace's `mise.toml` file. On next jail start, `mise install` automatically fetches and makes them available.

#### How It Works
1. **Workspace Declaration**: Tools declared in `/workspace/mise.toml` are workspace-specific.
2. **Installation**: At jail startup, `cli.py` runs `mise install` from the workspace, downloading all declared tools into the shared mise data dir.
3. **Persistence**: Tools are stored in `~/.local/share/mise/` on the host (shared between host and jail), surviving jail restarts.
4. **PATH Resolution**: `mise hook-env` resolves tool directories into PATH at startup. Interactive shells also use `mise activate` with PROMPT_COMMAND hooks to keep tools available.
5. **No Jail Config Shortcut**: If you edit `yolo-jail.jsonc` while doing package/tool setup, run `yolo check` before restart. Do not rely on the next startup prompt to discover mistakes.

#### Installing a Tool (Example: Typst)

**Step 1**: Add to your workspace's `mise.toml`:
```toml
[tools]
typst = "latest"
```

**Step 2**: On next jail startup (or manually inside jail):
```bash
mise install typst
```

**Step 3**: Use it:
```bash
typst compile myfile.typ output.pdf
```

The tool is now available to the agent and all its subprocesses, and will persist across jail restarts.

#### Available Tools via Mise

Mise supports thousands of tools from registries like **aqua**, **asdf**, and **cargo**. Examples:
- **Build tools**: `typst`, `just`, `protoc`, `cmake`
- **Languages**: `rust`, `zig`, `nim`, `kotlin`
- **CLI tools**: `fd`, `ripgrep`, `bat`, `jq`, `yq`
- **Database**: `postgresql`, `redis`, `sqlite`
- **DevOps**: `terraform`, `ansible`, `kubectl`, `helm`

Search available tools:
```bash
mise registry  # List all available tools
```

#### Workspace vs Global Tools

| Scope | Location | Syntax | Persistence | Visibility |
|-------|----------|--------|-------------|------------|
| **Workspace** | `/workspace/mise.toml` | `[tools] typst = "latest"` | ✅ Survives restarts | ✅ Workspace-specific |
| **Global** | `~/.config/mise/config.toml` | `[tools] typst = "latest"` | ✅ Shared across workspaces | ⚠️ Cross-workspace |

**Recommendation**: Use workspace-level tools in `mise.toml` for project-specific dependencies. This keeps each workspace isolated and reproducible.

#### Troubleshooting

- **Tool not found after installation**: Restart jail or run `eval "$(mise hook-env -s bash)"` in current shell to refresh PATH.
- **Version conflicts**: Each workspace has its own `mise.toml` — edit it to change versions. Multiple versions of same tool can coexist (mise manages them separately).
- **Check installed tools**: `mise ls` (shows all), `mise ls typst` (shows typst versions), `mise which typst` (shows path).
- **Remove a tool**: Delete from `mise.toml` and run `mise uninstall typst@VERSION`, or just leave it — unused tools take no space in PATH.

### Agent Resource Management

Agents can manage compute resources at two levels: container-level hard limits (set by the human via config) and in-jail per-process controls via cgroup v2 delegation.

#### Container-Level Limits (Config)

Set hard cgroup constraints in `yolo-jail.jsonc`:
```jsonc
{
  "resources": {
    "memory": "8g",      // OOM-killed if exceeded
    "cpus": 4,           // CFS CPU quota
    "pids_limit": 4096   // Prevents fork bombs
  }
}
```
These are enforced by the kernel — the jail cannot exceed them. Run `yolo check` after editing config.

#### In-Jail Cgroup v2 Delegation (Hard Limits on Sub-Processes)

The yolo CLI runs a **host-side cgroup delegate daemon** alongside the container. This daemon performs all privileged cgroup operations on behalf of agents inside the jail — no `CAP_SYS_ADMIN` or writable cgroup mount is needed inside the container.

Agents use the `yolo-cglimit` helper (at `~/.local/bin/yolo-cglimit`, on PATH) to enforce kernel-level resource limits on sub-processes:

```bash
# Limit a training job to 75% of all CPUs
yolo-cglimit --cpu 75 -- python train.py

# 50% CPU + 2GB RAM
yolo-cglimit --cpu 50 --memory 2g -- make -j8

# Max 100 processes (prevent fork bombs)
yolo-cglimit --pids 100 -- ./build.sh

# Named cgroup for monitoring
yolo-cglimit --cpu 75 --name training -- python train.py
```

These limits are kernel-enforced via cgroup v2 and cannot be exceeded.

**How it works**: `yolo-cglimit` sends a JSON request to the host-side daemon via a Unix socket at `/tmp/yolo-cgd/cgroup.sock`. The daemon creates a child cgroup in the container's cgroup tree on the host filesystem, sets resource limits, and moves the caller's process into it (using `SO_PEERCRED` for secure, kernel-attested PID identity). All operations are validated and logged for auditability.

**Security model**:
- Cgroup names are strictly validated (alphanumeric + dash/underscore, no path traversal)
- Limit values are range-checked (CPU 1–100×nproc, memory ≥ 1MB, PIDs 1–1M)
- Operations are confined to the container's cgroup subtree
- Every request is logged to `~/.local/share/yolo-jail/logs/<container>-cgd.log`

**Podman is the primary supported runtime** for cgroup delegation. Docker support is best-effort. If cgroup delegation is unavailable, `yolo-cglimit` will print an error with guidance.

#### Soft Limits (Always Available)

Agents can also constrain individual processes using standard Unix tools:

| Tool | Purpose | Example |
|------|---------|---------|
| `nice` / `renice` | CPU scheduling priority | `nice -n 19 python train.py` (lowest priority) |
| `ionice` | I/O scheduling priority | `ionice -c 3 python train.py` (idle I/O class) |
| `timeout` | Wall-clock time limit | `timeout 3600 python train.py` (kill after 1 hour) |
| `ulimit` | Per-process limits | `ulimit -v 4000000` (4GB virtual memory) |

For long-running jobs (training, builds), prefer combining hard and soft limits:
```bash
yolo-cglimit --cpu 75 --memory 4g -- nice -n 10 timeout 7200 python train.py
```

### Environment Hygiene
- **No Pagers**: Agents cannot handle interactive pagers.
    - `PAGER=cat`, `GIT_PAGER=cat`, `BAT_PAGER=""`.
    - `alias bat='bat --style=plain --paging=never'`.
- **Editor Split**: `EDITOR=cat` prevents agents from getting stuck in interactive editors (e.g., `git commit` without `-m`). `VISUAL=nvim` enables human-interactive editing (e.g., Copilot's ctrl-g to edit prompt in editor). Standard Unix convention: programs check `VISUAL` first for full-screen terminals, `EDITOR` as fallback. Host `~/.config/nvim` is copied (with symlinks resolved) into the persistent jail home on each startup.
- **Terminal**: `TERM=xterm-256color` should be passed to maintain color support for agent parsing.
- **Permissions**: Map host UID/GID to the container user to ensure file ownership on the host is preserved.
- **No LD_LIBRARY_PATH Stripping**: `LD_LIBRARY_PATH=/lib:/usr/lib` is baked into the Docker image Env to survive agent environment sanitization.
- **Tmux Window Title**: `cli.py` runs `tmux rename-window JAIL` on the host before exec'ing into the container. This sets the window name to "JAIL" and implicitly disables `automatic-rename` for that window. `PROMPT_COMMAND` inside the jail also emits title escape sequences as a fallback for interactive sessions.
- **Overmind Isolation**: `OVERMIND_SOCKET=/tmp/overmind.sock` is set inside the jail so overmind processes don't conflict with host-side overmind (which defaults to `.overmind.sock` in the workspace directory).
- **Global Gitignore**: The host's global gitignore (`core.excludesFile` or `~/.config/git/ignore`) is mounted read-only and configured via `git config --global core.excludesFile` inside the jail.

## Workflow for Modification
1. **Change Image**: Edit `flake.nix` (e.g., add `pkgs.strace`).
2. **Change Logic**: Edit `src/entrypoint.py` or `src/cli.py`. All Python, no bash heredocs.
3. **Manual Test**: Run `yolo -- bash -c "my-new-tool --version"`.
4. **Enforce YOLO**: Always ensure `YOLO_BYPASS_SHIMS=1` is set when running installers inside the jail.
5. **Commit**: The pre-commit hook runs quality checks automatically.

## Testing Guidelines
- **Fast tests** (`just test-fast`): Unit tests, no containers. Run by pre-commit hook.
- **Full tests** (`just test`): Includes container integration tests. Run by GitHub CI.
- **No Agent Tests**: Automated tests must NOT run `copilot`, `gemini`, or `claude` interactively. Tests may check they are installed (`--version`) but must never start interactive sessions or make API calls.
- **Manual Agent Testing**: Always test agent functionality manually before committing. Use `yolo -- copilot --yolo`, `yolo -- gemini --yolo`, or `yolo -- claude` from a test project.
