# YOLO Jail Storage, Configuration & Identity

How configuration files, persistent storage, overlays, and identities
are organized across the host, global storage, workspace state, and
inside running jails.

---

## 1. Configuration Hierarchy

Configuration is loaded and merged in this order (later overrides earlier):

```
User defaults          ~/.config/yolo-jail/config.jsonc
    ↓ merged over by
Workspace config       <workspace>/yolo-jail.jsonc
    ↓ merged over by
Workspace local        <workspace>/yolo-jail.local.jsonc   (if present)
    ↓ overridden by
Environment vars       YOLO_RUNTIME, YOLO_BYPASS_SHIMS
```

**Merge rules:**
- Lists (e.g. `packages`, `blocked`) are merged and deduplicated.
- Scalar and object values in workspace override user defaults.
- `YOLO_RUNTIME` env var overrides `runtime` from either config file.

`yolo-jail.local.jsonc` is auto-merged whenever it sits next to
`yolo-jail.jsonc` — no `include_if_found` entry needed. It's meant for
per-machine overrides kept out of version control: add the name to your
global gitignore and use it for tweaks that don't belong in the tracked
config.

### 1.1 Config-ownership principle (agent config)

The rules above govern *yolo's own* config (`yolo-jail.jsonc`). A separate,
load-bearing principle governs the config yolo generates *for the coding agents*
(Claude/Copilot/Gemini/pi/opencode/Codex — `.claude/settings.json`,
`~/.pi/agent/settings.json`, `config.toml`, …):

> **yolo composes agent config into the jail USER scope only. The workspace tree
> is the operating agent's, and mirrors the host.**

Concretely:

- **User scope (yolo-owned).** yolo writes each agent's user-level config under
  `/home/agent/…` (a per-workspace r/w overlay; §3–§4). This is the *only* agent
  config surface yolo regenerates.
- **Workspace scope (agent-owned, host-mirrored).** yolo does **not** write any
  agent's project/workspace config (e.g. `$CWD/.claude/settings.json`). `/workspace`
  is bind-mounted from the host and belongs to the operating agent; yolo leaves it
  as-is. The *only* exceptions are narrow **"internal details" shadow mounts** yolo
  owns for isolation — currently `.vscode/mcp.json` and `.overmind.sock`, each
  shadowed with `/dev/null` (see §4 mount map / `assemble.go`). These are
  isolation-boundary artifacts, not agent config, and are the deliberate, enumerated
  exception to "workspace mirrors host."
- **Managed scope (yolo-owned, outside both).** Security-boundary keys go to an
  agent's *managed* config where one exists (e.g. Claude's
  `/etc/claude-code/managed-settings.json`) — neither user nor workspace, so yolo
  owns it outright with no contention.

**Why it matters for regeneration:** because yolo and the agent both write the
*same* user-scope file (yolo regenerates it each boot; the agent persists in-jail
`/config`/`/settings` edits there), that file is a shared surface — so surviving
regeneration needs a capture-diff overlay at the user scope, uniformly across
agents. No agent gets a "yolo owns a separate file" shortcut, because the only
separate file (project scope) is a workspace file yolo won't touch. The full
mechanism — layered regeneration, a Lua transform, and the overlay — is
[`../plans/agent-settings-composition.md`](../plans/agent-settings-composition.md);
this section is the durable statement of the ownership rule that constrains it.

### Create configs

```bash
yolo init                  # Create workspace yolo-jail.jsonc
yolo init-user-config      # Create ~/.config/yolo-jail/config.jsonc
```

After **every** edit to either config file, run `yolo check` before restarting
or asking a human to restart the jail. Inside an already-running jail,
`yolo check --no-build` gives a faster config/entrypoint preflight.

### Config change safety

When `yolo-jail.jsonc` changes between jail startups, the CLI:
1. Compares current config against the saved snapshot
2. Shows a normalized diff of what changed
3. Asks for `y/N` confirmation before proceeding

This prevents agents from silently adding packages or mounts that the
human didn't approve. See `docs/design/config-safety.md` for details.

This approval step does **not** replace `yolo check` — agents should still run
`yolo check` after every config edit before the restart happens.

**Snapshot location:** `<workspace>/.yolo/config-snapshot.json`

---

## 2. Host Storage Layout

All persistent jail state lives under `~/.local/share/yolo-jail/`:

```
~/.local/share/yolo-jail/
├── home/                  → Mounted :ro as /home/agent (auth tokens, base configs)
│   ├── .claude/           │  Claude Code auth tokens
│   ├── .copilot/          │  Copilot auth tokens
│   └── .gemini/           │  Gemini auth tokens
├── cache/                 → Mounted :rw as /home/agent/.cache (shared download cache)
├── mise/                  → Mounted :rw as /mise (jail-land mise store — Linux podman;
│                             macOS podman and Apple Container use the yolo-mise-data-v2
│                             named volume instead, also mounted at /mise)
├── containers/            → Tracking files for running containers
└── agents/                → Per-container AGENTS.md files
    └── yolo-<hash>/
        └── AGENTS.md      → Mounted read-only over ~/.copilot/AGENTS.md,
                              ~/.gemini/AGENTS.md, and ~/.claude/CLAUDE.md
```

### Isolation model

The container runs with `--read-only` (immutable root filesystem) and
`/home/agent` is mounted `:ro`. All writable state goes to explicitly
mounted per-workspace overlays or shared mounts:

| Storage | Scope | Persistence | Writable? |
|---------|-------|-------------|-----------|
| `home/` | All jails | Survives restarts | **Read-only** |
| `cache/` | All jails | Survives restarts | Writable (shared download CAS) |
| `mise/` (jail-land mise store) | All jails | Survives restarts | Writable (shared tool CAS, mounted at `/mise` in every jail; the host's own mise install is not a party) |
| Per-workspace overlays | Per workspace | Survives restarts | Writable |
| `venv-shadows/` (under `<workspace>/.yolo/home/`) | Per workspace | Survives restarts | Writable (per-side backing for `/workspace/.venv` and other `per_side_paths`) |
| `agents/<name>/AGENTS.md` | Per container | Regenerated each run | Read-only (in jail) |
| `/tmp`, `/var/tmp` | Per container | tmpfs (ephemeral) | Writable |

No cross-jail interference: each jail writes to its own per-workspace
dirs under `<workspace>/.yolo/home/`. Concurrent startup is safe
because jails don't share writable paths.

The host CLI guards against races on global storage:
- **nix-build-root:** atomic rename (build in temp dir, swap in)
- **run-result link:** per-PID unique path prevents cross-build deletion

---

## 3. Per-Workspace State (`.yolo/`)

Each workspace has a `.yolo/` directory (gitignored) for isolated state:

```
<workspace>/.yolo/
├── home/
│   ├── npm-global/               → /home/agent/.npm-global (agent CLIs)
│   ├── local/                    → /home/agent/.local (claude, MCP wrappers)
│   ├── go/                       → /home/agent/go (gopls, mcp-language-server)
│   ├── yolo-shims/               → /home/agent/.yolo-shims (blocked tool shims)
│   ├── config/                   → /home/agent/.config (mise, jj, nvim config)
│   ├── bashrc                    → /home/agent/.bashrc
│   ├── gitconfig                 → /home/agent/.gitconfig
│   ├── yolo-bootstrap.sh         → /home/agent/.yolo-bootstrap.sh
│   ├── yolo-venv-precreate.sh    → /home/agent/.yolo-venv-precreate.sh
│   ├── yolo-perf.log             → /home/agent/.yolo-perf.log
│   ├── yolo-socat.log            → /home/agent/.yolo-socat.log
│   ├── yolo-entrypoint.lock      → /home/agent/.yolo-entrypoint.lock
│   ├── claude.json               → /home/agent/.claude.json
│   ├── copilot-sessions/         → /home/agent/.copilot/session-state
│   ├── copilot-command-history   → /home/agent/.copilot/command-history-state.json
│   ├── bash_history              → /home/agent/.bash_history
│   ├── gemini-history/           → /home/agent/.gemini/history
│   ├── claude-projects/          → /home/agent/.claude/projects
│   ├── ssh/                      → /home/agent/.ssh (mode 700)
│   └── venv-shadows/             → Per-side backing dirs, shadow-mounted over
│       └── .venv/                  /workspace/.venv (plus the mise-configured
│                                   venv path and any per_side_paths entries;
│                                   '/' in an entry becomes '__' in the dir name)
├── startup.log                   → Provisioning log from the last new-container boot
└── config-snapshot.json          → Last-confirmed config (for change detection)
```

These are mounted as **writable overlays** on top of the read-only global home.
Each workspace gets its own copy of installed tools, generated configs, and
history — no cross-jail interference. First boot for a new workspace installs
tools into empty overlay dirs; subsequent boots reuse cached installs.

---

## 4. Inside the Jail — Mount Map

The container runs with `--read-only` (immutable root filesystem).
All writable paths are explicitly mounted:

```
/ (root)                ← IMMUTABLE (--read-only container flag)
/workspace              ← Host workspace (read-write)
  ├── .venv/                 ← PER-SIDE shadow (backed by <workspace>/.yolo/home/
  │                            venv-shadows/ — the host keeps its own .venv underneath;
  │                            same treatment for the mise-configured venv path and
  │                            any per_side_paths entries)
  └── .yolo/startup.log      ← Provisioning log (fresh file per new container)
/home/agent             ← Global home :ro (auth tokens, base configs)
  ├── .npm-global/           ← PER-WORKSPACE overlay (agent CLI installs)
  ├── .local/                ← PER-WORKSPACE overlay (claude, MCP wrappers)
  ├── go/                    ← PER-WORKSPACE overlay (Go binaries)
  ├── .yolo-shims/           ← PER-WORKSPACE overlay (blocked tool shims)
  ├── .config/               ← PER-WORKSPACE overlay (mise, jj, nvim config)
  ├── .cache/                ← SHARED writable (download caches — CAS)
  ├── .bashrc                ← PER-WORKSPACE file overlay
  ├── .gitconfig             ← PER-WORKSPACE file overlay
  ├── .yolo-bootstrap.sh     ← PER-WORKSPACE file overlay
  ├── .yolo-venv-precreate.sh ← PER-WORKSPACE file overlay
  ├── .yolo-perf.log         ← PER-WORKSPACE file overlay
  ├── .yolo-socat.log        ← PER-WORKSPACE file overlay
  ├── .yolo-entrypoint.lock  ← PER-WORKSPACE file overlay
  ├── .claude.json           ← PER-WORKSPACE file overlay
  ├── .claude/
  │   ├── projects/          ← PER-WORKSPACE overlay
  │   ├── CLAUDE.md          ← agents/<name>/AGENTS.md (read-only)
  │   ├── skills/            ← MOUNTED :ro (merged on host, kernel-enforced)
  │   └── settings.json      ← PER-WORKSPACE overlay
  ├── .copilot/
  │   ├── session-state/     ← PER-WORKSPACE overlay
  │   ├── command-history-state.json ← PER-WORKSPACE overlay
  │   ├── AGENTS.md          ← agents/<name>/AGENTS.md (read-only)
  │   ├── skills/            ← MOUNTED :ro (merged on host, kernel-enforced)
  │   ├── mcp-config.json    ← PER-WORKSPACE overlay
  │   └── lsp-config.json    ← PER-WORKSPACE overlay
  ├── .gemini/
  │   ├── history/           ← PER-WORKSPACE overlay
  │   ├── AGENTS.md          ← agents/<name>/AGENTS.md (read-only)
  │   ├── skills/            ← MOUNTED :ro (merged on host, kernel-enforced)
  │   └── settings.json      ← PER-WORKSPACE overlay
  ├── .bash_history          ← PER-WORKSPACE overlay
  └── .ssh/                  ← PER-WORKSPACE overlay (mode 700)
/mise                   ← Jail-land mise store (~/.local/share/yolo-jail/mise on Linux;
                         yolo-mise-data-v2 named volume on macOS podman and Apple
                         Container). Shared writable CAS across all jails — the
                         host's ~/.local/share/mise is never mounted.
/opt/yolo-jail          ← yolo-jail repo (read-only)
/tmp                    ← tmpfs (ephemeral)
/var/tmp                ← tmpfs (ephemeral)
```

**Shadowed paths** (mounted as `/dev/null` to prevent leaks):
- `/workspace/.vscode/mcp.json` — prevents host VS Code MCP configs
- `/workspace/.overmind.sock` — prevents host overmind socket

---

## 5. Identity Propagation

Host identities are passed to jails via environment variables.
The entrypoint writes them into tool configs on every startup.

### Flow

```
Host                              Container
────                              ─────────
git config user.name  ─→  YOLO_GIT_NAME   ─→  git config --global user.name
git config user.email ─→  YOLO_GIT_EMAIL  ─→  git config --global user.email
jj config user.name   ─→  YOLO_JJ_NAME   ─→  jj config set --user user.name
jj config user.email  ─→  YOLO_JJ_EMAIL  ─→  jj config set --user user.email
```

### Key design decisions

- **No `~/.gitconfig` mount**: The host gitconfig may contain credentials,
  aliases, or tokens. Only `user.name` and `user.email` are extracted
  and passed as env vars.
- **Global gitignore is mounted read-only**: The host's `core.excludesFile`
  is bind-mounted to `/home/agent/.config/git/ignore:ro`.
- **Identity set on every startup**: Even on container reuse (`podman exec`),
  the entrypoint re-runs `configure_git()` and `configure_jj()` with fresh
  env vars. This means if you change your host identity, the next jail
  session picks it up.
- **Exec path gets env vars too**: Both `podman run` and `podman exec`
  pass `-e YOLO_GIT_NAME=...` etc. so identity works for both new
  containers and reattaching to existing ones.

---

## 6. Skills Directories

Skills are **merged on the host** by `cli.py` and **bind-mounted `:ro`**
into each container. This is kernel-enforced — agents cannot modify
skills and get a clear "Read-only file system" error on write attempts.

### Merge order (later overrides earlier)

1. Built-in skills (jail-startup)
2. Host user-level skills: `~/.copilot/skills/`, `~/.gemini/skills/`, `~/.claude/skills/`
3. Workspace skills: `<workspace>/.copilot/skills/`, `<workspace>/.gemini/skills/`, `<workspace>/.claude/skills/`

Merged skills are staged in `~/.local/share/yolo-jail/agents/<cname>/skills-{agent}/`
and mounted read-only over `~/.copilot/skills/`, `~/.gemini/skills/`, and
`~/.claude/skills/` inside the jail.

### Limitations

- Agents cannot create user-level skills inside a jail.
- To develop a new skill: create it in the workspace skills directory
  (e.g., `/workspace/.claude/skills/my-skill/`), test it, then promote
  to the host-level directory outside the jail.
- The skill becomes available in all jails after restart.

---

## 7. AGENTS.md Injection

Each jail gets a custom `AGENTS.md` generated by the host CLI
(`generate_agents_md()`) containing:

- Jail-specific instructions (blocked tools, available tools)
- Package management guidance (including the rule to run `yolo check` after every config edit)
- Environment details

This is stored at `~/.local/share/yolo-jail/agents/<container-name>/AGENTS.md`
on the host and mounted read-only over:
- `/home/agent/.claude/CLAUDE.md`
- `/home/agent/.copilot/AGENTS.md`
- `/home/agent/.gemini/AGENTS.md`

This ensures each workspace jail gets its own context without
stomping the shared home directory.

---

## 8. Environment Variables Inside the Jail

| Variable | Value | Purpose |
|----------|-------|---------|
| `HOME` | `/home/agent` | Home directory |
| `NPM_CONFIG_PREFIX` | `/home/agent/.npm-global` | NPM global install location |
| `GOPATH` | `/home/agent/go` | Go binary location |
| `MISE_DATA_DIR` | `/mise` | Jail-land mise store, shared by all jails (host dir bind mount on Linux; `yolo-mise-data-v2` named volume on macOS podman / Apple Container). The host's own mise dir is never mounted |
| `MISE_TRUSTED_CONFIG_PATHS` | `/workspace` | Trust every mise config under the workspace (recursive, path-component-aware prefix match) |
| `MISE_ENV` | `jail` | Jail-only overrides: a checked-in `mise.jail.toml` overrides `mise.toml` inside jails, no-op on the host |
| `RUSTUP_HOME` | `/mise/rustup` | mise's rust backend drives rustup, which installs OUTSIDE the mise store; its default `~/.rustup` is read-only in-jail. Workspace mise `[env]` overrides win on activation |
| `CARGO_HOME` | `/mise/cargo` | Same rust-backend escape; also makes the recorded `installs/rust/<ver> → $CARGO_HOME/bin` symlink resolve identically in every jail |
| `MISE_YES` | `1` | Skip mise confirmation prompts |
| `LD_LIBRARY_PATH` | `/lib:/usr/lib` | Library search path (survives agent env stripping) |
| `PAGER` | `cat` | No interactive pagers |
| `GIT_PAGER` | `cat` | No git pagers |
| `TERM` | `xterm-256color` | Terminal type (passed from host) |
| `YOLO_BLOCK_CONFIG` | JSON | Blocked tools configuration |
| `YOLO_HOST_DIR` | Host workspace path | For reference/logging |
| `YOLO_REPO_ROOT` | `/opt/yolo-jail` | Location of yolo-jail source |
| `NIX_REMOTE` | `daemon` | (If host nix available) Use host nix daemon |
| `OVERMIND_SOCKET` | `/tmp/overmind.sock` | Isolate from host overmind |

---

## 9. Tool Locations Inside the Jail

| Tool Type | Path | Source |
|-----------|------|--------|
| Nix image binaries | `/bin/`, `/usr/bin/` | Built into container image |
| Nix image libraries | `/lib/`, `/usr/lib/` | Built into container image |
| NPM global packages | `/home/agent/.npm-global/bin/` | Installed by bootstrap |
| Go binaries | `/home/agent/go/bin/` | Installed by bootstrap |
| MCP node wrappers | `/home/agent/.local/bin/mcp-wrappers/` | Generated by entrypoint |
| Mise shims | `/mise/shims/` | Managed by mise |
| Blocked tool shims | `/home/agent/.yolo-shims/` | Generated by entrypoint |

**PATH order:**
```
$SHIM_DIR:/home/agent/.npm-global/bin:/home/agent/go/bin:$MISE_DATA_DIR/shims:/bin:/usr/bin
```

Blocked tool shims are first in PATH to intercept blocked commands.
