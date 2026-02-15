<!-- YOLO-JAIL-START (auto-generated, do not edit above YOLO-JAIL-END) -->
<!-- This section is dynamically generated inside the jail at runtime. -->
<!-- See src/entrypoint.sh for the generation logic. -->
<!-- YOLO-JAIL-END -->

# YOLO Jail: Agent Developer Guide

This project provides a secure, isolated Docker environment for AI agents (Gemini CLI, Copilot) to execute commands on local repositories without compromising host security or identity.

## Architectural Specs

### 1. Configuration (`yolo-jail.jsonc`)
- **Format**: JSON with comments (JSONC). **TOML is deprecated**.
- **Location**: Project root.
- **User Defaults**: Optional global config at `~/.config/yolo-jail/config.jsonc` (create with `yolo init-user-config`).
- **Merge Rules**: Workspace config is merged over user config. Lists are merged+deduped; scalar/object values in workspace override user defaults.
- **Dynamic Shims**: Blocked tools are generated dynamically based on this config. All blocked tools are unconditionally blocked unless `YOLO_BYPASS_SHIMS=1` is set.
- **Custom Packages**: The `packages` array specifies additional nix packages to bake into the jail image. Names must match nixpkgs attribute names. The image only rebuilds when this list changes. Uses `--impure` nix build with `builtins.getEnv`.

### 2. Isolation & Identity
- **Strict Isolation**: The jail MUST NOT access host `~/.ssh/`, `~/.gitconfig`, or any cloud credentials.
- **Login inside the Jail**: Users must perform a one-time `gh auth login` and `gemini login` within the jail.
- **Persistent Global State**: All jail-specific state (auth tokens, bash history, global tool cache) is stored in `~/.local/share/yolo-jail/`.
    - Host `~/.local/share/yolo-jail/home` -> Container `/home/agent`
    - Host `~/.local/share/yolo-jail/mise` -> Container `/mise`

### 3. Execution Engine (`src/cli.py` & `src/entrypoint.sh`)
- **Direct Execution**: Commands are run via `yolo -- <command>`. 
- **Auto-YOLO**: The CLI automatically injects `--yolo` for `gemini` and `copilot` commands.
- **Quoting**: Use `shlex.join` in Python to pass quoted arguments correctly to the container's `bash -c`.
- **Self-Updating Build**: The CLI runs `nix build --impure` on every start but only executes `docker load` if the resulting image hash differs from `.last-load`. The `--impure` flag allows reading the `YOLO_EXTRA_PACKAGES` env var for per-project package customization.
- **AGENTS Injection**: Runtime AGENTS context is written to `~/.copilot/AGENTS.md` and `~/.gemini/AGENTS.md` inside the jail. `/workspace/AGENTS.md` is not modified.

## Developer Runbook

### Debugging MCP & LSP
- **Logs**: Copilot logs are in `~/.copilot/logs/`.
- **Config**: Copilot config lives at `~/.copilot/` (not XDG). Contains `config.json`, `mcp-config.json`, `lsp-config.json`.
- **Chromium Stability**: Headless Chromium in Docker is brittle. 
    - **Connect Mode**: Chrome DevTools MCP uses a wrapper script (`~/.local/bin/chrome-devtools-mcp-wrapper`) that pre-launches Chromium with `--remote-debugging-port` and connects via `--browser-url`. This avoids pipe-mode fd conflicts when MCP servers are spawned by agents.
    - **Required Chrome Flags**: `--no-sandbox`, `--disable-setuid-sandbox`, `--disable-dev-shm-usage`, `--disable-gpu`, `--disable-software-rasterizer`.
    - **Docker**: Use `--shm-size=2g` in the Docker run command for adequate shared memory.
    - **Binary Discovery**: Always use absolute paths (e.g., `/usr/bin/chromium`) in MCP configs.
- **LSP Schemas**:
    - **Copilot**: Requires `mcpServers` (plural) key in `~/.copilot/mcp-config.json` and a separate `lsp-config.json` with `fileExtensions`.
    - **Gemini**: Uses `mcpServers` key in `settings.json`.

### Tool Management
- **Mise**: All runtimes (Node@22, Python@3.13, Go) are managed by `mise`. 
- **Bootstrapping**: MCP servers are installed via `npm install -g` or `go install` into the persistent `/home/agent` partition during the container's startup (`~/.yolo-bootstrap.sh`).
- **Binary Locations**:
    - NPM Globals: `/home/agent/.npm-global/bin/`
    - Go Binaries: `/home/agent/go/bin/`
- **PATH Order**: `${SHIM_DIR}:/home/agent/.npm-global/bin:/home/agent/go/bin:/mise/shims:/bin:/usr/bin`.
- **Node Wrappers**: `~/.local/bin/mcp-wrappers/node` and `npx` are wrapper scripts that set `LD_LIBRARY_PATH` before calling the mise-installed binary. MCP configs use absolute paths to these wrappers. This is required because agents (Copilot) may sanitize the environment when spawning MCP child processes, stripping `LD_LIBRARY_PATH` which mise-installed node needs to find `libstdc++.so.6`.

### Environment Hygiene
- **No Pagers**: Agents cannot handle interactive pagers.
    - `PAGER=cat`, `GIT_PAGER=cat`, `BAT_PAGER=""`.
    - `alias bat='bat --style=plain --paging=never'`.
- **Terminal**: `TERM=xterm-256color` should be passed to maintain color support for agent parsing.
- **Permissions**: Map host UID/GID to the container user to ensure file ownership on the host is preserved.

## Workflow for Modification
1. **Change Image**: Edit `flake.nix` (e.g., add `pkgs.strace`).
2. **Change Logic**: Edit `src/entrypoint.sh` or `src/cli.py`.
3. **Automatic Test**: Run `uv run pytest tests/test_jail.py`.
4. **Manual Test**: Run `yolo -- bash -c "my-new-tool --version"`.
5. **Enforce YOLO**: Always ensure `YOLO_BYPASS_SHIMS=1` is set when running installers inside the jail.
6. **Commit & Push**: Always commit and push after every change. The Nix image is built from the working tree, and other users need the latest code.

## Testing Guidelines
- **Model**: When testing Copilot interactively or in scripts, always use the `gpt-4.1` model (e.g., `copilot --model gpt-4.1`). Do not use expensive models for testing.
- **No Agent Tests**: Automated tests (`uv run pytest`) must NOT run `copilot` or `gemini` interactively. Tests may check that they are installed (`--version`) but must never start interactive sessions or make API calls.
- **Workspace MCP Shadowing**: The CLI shadows any workspace `.vscode/mcp.json` with `/dev/null` inside the jail so agents only use the jail's MCP config. Host MCP configs designed for VS Code won't interfere.
