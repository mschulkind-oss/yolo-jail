# Gemini, Miscellaneous Data & Directory Structure Analysis

> Analysis of `/workspace/.yolo/home/` — everything outside copilot-sessions, bash_history, and copilot-command-history, plus a full structural audit of the `.yolo/` tree.

---

## Gemini History

### What's There

The `gemini-history/` directory exists but is **completely empty** — zero files. This means either:
1. Gemini CLI was never used interactively in this workspace jail, or
2. Gemini history is being written somewhere else (the shared home at `~/.local/share/yolo-jail/home` on the host), and the per-workspace overlay mount for `gemini-history/` was created but never populated.

### Gemini Configuration

While no conversations happened, Gemini **is fully configured** in the jail:

**`~/.gemini/settings.json`** contains:
- `"approvalMode": "yolo"` — auto-approve all tool use (matches jail philosophy)
- `"enablePermanentToolApproval": true`
- 4 MCP servers configured:
  - `chrome-devtools` — headless Chromium browser automation
  - `sequential-thinking` — structured reasoning MCP
  - `python-lsp` — Pyright via `mcp-language-server` Go wrapper
  - `typescript-lsp` — typescript-language-server via same Go wrapper
- **`~/.gemini/AGENTS.md`** — exists but is **empty** (0 bytes). This is the mount point for per-workspace agent instructions, but none were injected for Gemini.

### Gemini vs Copilot Usage Comparison

| Dimension | Copilot | Gemini |
|-----------|---------|--------|
| Conversations | 5 sessions (UUIDs in `copilot-sessions/`) | **0 sessions** |
| Command history | 14 commands logged | None |
| Log files | 1 process log (3.7KB) | None |
| Configuration | Full (MCP, LSP, config.json) | Full (MCP in settings.json) |
| AGENTS.md | Empty (0 bytes) | Empty (0 bytes) |

**Verdict**: This workspace was exclusively used with **Copilot CLI**. Gemini was provisioned and configured but never invoked. All 14 commands in `copilot-command-history` are Copilot interactions — including debugging jail issues, adding features (typst support, package management), fixing bugs (`UnboundLocalError`), and investigating tmux window titles.

---

## Other Data (go/ directory and miscellaneous files)

### `go/` Directory

**Empty.** The directory exists as the `$GOPATH` mount point (`/home/agent/go/`). The bootstrap script (`~/.yolo-bootstrap.sh`) would install `mcp-language-server` to `$GOPATH/bin/` if Go is available, but since this is the per-workspace overlay and the actual Go binaries live in the shared home (`~/.local/share/yolo-jail/home/go/` on the host), this overlay directory was never populated.

### `.bashrc` — Jail Shell Configuration

A rich shell config that reveals the jail's personality:
- **Colorful prompt**: `🔒 YOLO-JAIL (host: <dir>)` in red, with jail:path in green/blue
- **Tmux integration**: `PROMPT_COMMAND` sets window title to `JAIL <dirname>`
- **Font cache init**: `fc-cache -f` for Chromium compatibility
- **Anti-pager settings**: `PAGER=cat`, `BAT_PAGER=""`, `GIT_PAGER=cat`
- **Editor**: `nvim`
- **PATH order**: shims → npm-global → go/bin → mise/shims → system
- **Aliases**: `gemini`/`copilot` auto-add `--yolo`, `vi`/`vim` → `nvim`, `bat` → plain no-pager mode

### `.gitconfig` — Git Identity

```ini
[user]
    name = Matthew Schulkind
    email = mschulkind@gmail.com
```

**⚠️ Privacy note**: This is the developer's real name and email, persisted in the workspace overlay. This is expected (needed for git commits) but worth noting as PII.

### `.yolo-bootstrap.sh` — Tool Provisioning Script

Idempotent installer that runs on every jail start:
1. **NPM globals** (if `chrome-devtools-mcp` missing): `chrome-devtools-mcp`, `@modelcontextprotocol/server-sequential-thinking`, `pyright`, `typescript-language-server`, `typescript`
2. **Go binary** (if `mcp-language-server` missing): `github.com/isaacphi/mcp-language-server@latest`
3. **Python** (if `showboat` missing): `pip install showboat`

All installs use `YOLO_BYPASS_SHIMS=1` to avoid shim interference.

### `.yolo-shims/` — Blocked Tool Shims

Two shim scripts that intercept dangerous commands:

| Shim | Blocks | Suggests Instead |
|------|--------|-----------------|
| `find` | Recursive filesystem searches | `fd <pattern>` |
| `grep` | Recursive text searches | `rg <pattern> [file]` |

Both shims exit 127 (command not found) unless `YOLO_BYPASS_SHIMS=1` is set. This prevents AI agents from running expensive recursive searches.

### `.copilot/` — Copilot Runtime & Config

| Item | Size | Purpose |
|------|------|---------|
| `config.json` | 39B | `{"yolo": true, "banner": "never"}` |
| `mcp-config.json` | 716B | Chrome DevTools + Sequential Thinking MCP servers |
| `lsp-config.json` | 547B | Pyright (Python) + typescript-language-server |
| `pkg/linux-x64/0.0.410/` | ~47MB | **Copilot CLI v0.0.410 binary distribution** — the big one |
| `session-state/` | 4KB | 1 session (9e141f80...) with empty checkpoint history |
| `skills/` | ~50KB | 9 skill definitions (agent-standards, debug-devtools, implement-project, new-project, project-setup, qa-autonomous, qa-collaborative, qa-core, researcher) |
| `logs/` | 3.7KB | Single process log from Feb 17 |
| `AGENTS.md` | 0B | Empty — per-workspace agent instructions mount point |

**Copilot process log** (Feb 17, 17:15 - 17:41) shows:
- CLI v0.0.410 on Node.js v24.11.1
- Session 9e141f80 created
- MCP servers started: `sequential-thinking` (289ms), `chrome-devtools` (610ms)
- Login status: "unknown" → "Logged out" (auth may not have been set up in this overlay)
- Session lasted ~26 minutes before MCP transports closed

### `.local/` — Local Binaries & State

- **`bin/chrome-devtools-mcp-wrapper`** (1.5KB): Self-contained wrapper that starts headless Chromium on port 9222 then launches chrome-devtools-mcp against it. Sets `LD_LIBRARY_PATH` and fontconfig for agent env sanitization resilience.
- **`bin/mcp-wrappers/`**: Empty — the node/npx wrappers are in the shared home.
- **`share/chrome-devtools-mcp/telemetry_state.json`**: `{"lastActive": "2026-02-17T17:15:04.505Z"}` — Chrome DevTools MCP telemetry timestamp.
- **`state/mise/`**: Mise runtime state — tracked configs (workspace `mise.toml` + user config) and trusted workspace.

### `.config/mise/config.toml` — Runtime Versions

```toml
[tools]
node = "22"
python = "3.13"
go = "latest"
"npm:@google/gemini-cli" = "latest"
"npm:@github/copilot" = "latest"
```

Both Copilot and Gemini CLI are managed as npm packages via mise.

### `.npm/` — NPM Cache

- **`_cacache/`**: 2 cached packages (~2.8MB total, sha512-indexed)
- **`_logs/`**: 6 debug logs from Feb 17, all from mise checking npm package versions (`@github/copilot` and `@google/gemini-cli` dist-tags, versions, and publish times)
- Node v22.22.0, npm v10.9.4, running on Linux 6.18.6-arch1-1

### `.npm-global/` — Global NPM Packages

**Empty.** Like `go/`, the actual globals live in the shared home.

### `.cache/mise/` — Mise Download Cache (17MB)

Cached metadata for multiple runtimes:
- `android-sdk`, `go`, `just`, `node`, `pnpm` — version metadata
- `npm-github-copilot`, `npm-google-gemini-cli` — npm package metadata
- `python` — **17MB** (likely CPython source/binary cache)
- `uv` — Python package manager metadata
- `lockfiles` — mise lock coordination

---

## Directory Structure Analysis

### Overall Layout

```
.yolo/                          72MB total
└── home/                       72MB — the sole subdirectory
    ├── .bashrc                 1.4KB  — shell config
    ├── .gitconfig              63B    — git identity (PII)
    ├── .yolo-bootstrap.sh      969B   — tool installer
    ├── bash_history            142B   — 3 commands
    ├── copilot-command-history 7.2KB  — 14 user prompts
    ├── .cache/                 17MB   — mise runtime cache
    ├── .config/                12KB   — mise config
    ├── .copilot/               48MB   — Copilot runtime + pkg
    ├── .gemini/                8KB    — Gemini config (unused)
    ├── .local/                 44KB   — wrappers, state, telemetry
    ├── .npm/                   2.9MB  — npm cache + logs
    ├── .npm-global/            4KB    — empty (shared home)
    ├── .yolo-shims/            12KB   — find/grep blockers
    ├── copilot-sessions/       3.8MB  — 5 session directories
    ├── gemini-history/         4KB    — empty directory
    └── go/                     4KB    — empty directory
```

### Size Breakdown

| Component | Size | % of Total | What It Is |
|-----------|------|-----------|------------|
| `.copilot/pkg/` (Copilot binary) | ~47MB | 65% | Copilot CLI v0.0.410 distribution |
| `.cache/mise/` (runtime cache) | 17MB | 24% | Python/Node/Go version metadata |
| `copilot-sessions/` | 3.8MB | 5% | 5 conversation sessions with plans/files |
| `.npm/_cacache` | 2.8MB | 4% | 2 cached npm packages |
| Everything else | ~1.4MB | 2% | Config, scripts, logs, history |

### File Type Inventory (1,631 files total)

| Extension | Count | Source |
|-----------|-------|--------|
| `.patch` | 256 | Copilot session file edits |
| (no extension) | 127 | Binary/config/cache files |
| `.diff` | 49 | Copilot session diffs |
| `.md` | 40 | Skills, READMEs, checkpoints |
| `.bats` | 40 | Copilot session checkpoint data |
| `.json` | 18 | Config files (MCP, LSP, npm, etc.) |
| `.js` | 16 | Copilot CLI runtime code |
| `.gz` | 13 | Compressed cache files |
| `.wasm` | 3 | Tree-sitter parsers (bash, powershell) |
| `.node` | 3 | Native Node.js addons |
| `.log` | 7 | NPM + Copilot process logs |
| `.toml` | 1 | Mise config |
| `.yaml` | varies | Session workspace metadata |

### Data Organization Pattern

The `.yolo/home/` directory is a **union mount overlay** — it mirrors the container's `/home/agent/` home directory. The architecture has three layers:

1. **Shared home** (`~/.local/share/yolo-jail/home/` on host → `/home/agent/` in container): Persists across all workspaces. Contains actual installed binaries (npm globals, Go binaries).
2. **Per-workspace overlays** (`.yolo/home/<item>` → specific container paths): Workspace-specific state. This is what we're analyzing.
3. **Generated configs** (written by `entrypoint.py` on each jail start): `.bashrc`, bootstrap script, MCP/LSP configs, shims.

The per-workspace overlay captures:
- **Session state** (`copilot-sessions/`, `copilot-command-history`, `bash_history`, `gemini-history/`): What the user did
- **Runtime configs** (`.copilot/`, `.gemini/`, `.config/`): How tools are configured
- **Caches** (`.cache/`, `.npm/`): Speed optimization for repeated starts
- **Binary distribution** (`.copilot/pkg/`): The Copilot CLI itself (largest component)

### Privacy Considerations

| Data Type | Location | Sensitivity | Notes |
|-----------|----------|------------|-------|
| **Git identity** | `.gitconfig` | 🟡 PII | Real name + email (Matthew Schulkind, mschulkind@gmail.com) |
| **Conversation history** | `copilot-command-history` | 🟡 Medium | Full text of 14 user prompts — reveals work patterns, bugs encountered, feature requests |
| **Session diffs/patches** | `copilot-sessions/` | 🟡 Medium | Contains actual code changes made during sessions |
| **Bash history** | `bash_history` | 🟢 Low | Only 3 commands (cat AGENTS, run copilot) |
| **MCP/LSP configs** | `.copilot/`, `.gemini/` | 🟢 Low | Tool configurations, no secrets |
| **Telemetry** | `telemetry_state.json` | 🟢 Low | Just a timestamp |
| **NPM logs** | `.npm/_logs/` | 🟢 Low | Package version lookups, no auth tokens |
| **Copilot process log** | `.copilot/logs/` | 🟢 Low | Startup/shutdown events, MCP timing |
| **No auth tokens** | — | ✅ Good | No `.ssh/`, no API keys, no cloud credentials visible in the overlay |

**Key finding**: The jail's isolation design is working — no host SSH keys, cloud credentials, or auth tokens leaked into the workspace overlay. The `.gitconfig` PII is intentional (needed for commits). The most sensitive data is the conversation history and session patches, which contain the developer's actual work context.
