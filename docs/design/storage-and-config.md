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

### 1.1 Config-ownership principle (generated config)

The rules above govern *yolo's own* config (`yolo-jail.jsonc`). A separate,
load-bearing principle governs **every config file yolo generates inside the
jail** — coding-agent settings (Claude/Copilot/Gemini/pi/opencode/Codex —
`.claude/settings.json`, `~/.pi/agent/settings.json`, `config.toml`, …), but
equally the MCP-server config, LSP config, the global mise config, and git
identity:

> **yolo composes generated config into the jail USER scope only. The workspace
> tree is the operating agent's, and mirrors the host.**

Concretely:

- **User scope (yolo-owned).** yolo writes each generated config under
  `/home/agent/…` (a per-workspace r/w overlay; §3–§4). This is the *only*
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

**Snapshot location:** `~/.local/share/yolo-jail/approvals/<container-name>.json` — HOST-side and
never mounted into a jail, so the record of what was approved cannot be rewritten by whatever edited
the config (`config-safety.md`, OQ-D1). A non-interactive launch with a changed config is refused;
`yolo --accept-config-changes` approves it for that launch only (OQ-D2).

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
- **image build:** the flake builds in place (`cmd.Dir = repoRoot`, no
  staging dir); Nix's own store handles concurrent builds atomically
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
│   ├── yolo-shims/               → /home/agent/.yolo-shims (blocked-tool shims, FIRST on PATH)
│   ├── yolo-launchers/           → /home/agent/.yolo-launchers (lazy installers, LAST on PATH)
│   ├── config/                   → /home/agent/.config (mise, nvim config)
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
└── config-assembled.json         → Merged config the host assembled for this launch
                                    (read verbatim in-jail; the APPROVAL record lives
                                    host-side under approvals/, not here)
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
  ├── .yolo-shims/           ← PER-WORKSPACE overlay (blocked-tool shims)
  ├── .yolo-launchers/       ← PER-WORKSPACE overlay (lazy-install launchers)
  ├── .config/               ← PER-WORKSPACE overlay (mise, nvim config)
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
/opt/yolo-jail          ← BAKED install prefix (not a mount): real-file CLI
                          binaries at bin/ + flake bundle at share/yolo-jail
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
```

### Key design decisions

- **No `~/.gitconfig` mount**: The host gitconfig may contain credentials,
  aliases, or tokens. Only `user.name` and `user.email` are extracted
  and passed as env vars.
- **Global gitignore is mounted read-only**: The host's `core.excludesFile`
  is bind-mounted to `/home/agent/.config/git/ignore:ro`.
- **Identity set on every startup**: Even on container reuse (`podman exec`),
  the entrypoint re-runs `configure_git()` with fresh
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
| Blocked tool shims | `/home/agent/.yolo-shims/` | Generated by entrypoint (first on PATH) |
| Lazy-install launchers | `/home/agent/.yolo-launchers/` | Generated by entrypoint (last on PATH) |

**PATH order:**
```
$SHIM_DIR:/home/agent/.npm-global/bin:/home/agent/go/bin:$MISE_DATA_DIR/shims:/bin:/usr/bin:$LAUNCHER_DIR
```

Blocked tool shims are first in PATH to intercept blocked commands. Lazy-install
launchers are LAST, after `/bin`: an installer only needs to run when nothing else
provides the name, so ordering it there makes it structurally impossible for a pack's
declared `program` to shadow a binary the image already bakes.

---

## 10. Follow-up (2026-08-21): the generated user scope can name paths the jail cannot reach

**Status:** DIAGNOSED, not fixed. Two defects share one symptom, and they are independently
actionable. §1.1 above describes the generated user scope; this section is what happens when what it
generates points outside the container.

**Reads with:** [`trust-paths.md`](trust-paths.md) (OQ-TP7 is the same check-passes/launch-refuses
shape, for a different cause), [`agent-install-in-ci.md`](agent-install-in-ci.md) (whose integration
work surfaced this).

### 10.1 What was measured

Inside a jail whose parent's user config selects a **local** (`file://`) pack, a nested launch is
refused outright:

```console
$ cd /tmp/nesttest && echo '{}' > yolo-jail.jsonc
$ yolo run --accept-config-changes -- bash -lc 'echo NESTED_OK'
packs: matt-core: local pack /home/matt/.dotfiles/packs/matt-core is not a directory
```

The generated inner config faithfully carries `file:///home/matt/.dotfiles/packs/matt-core` — a HOST
path with no referent in the container. Three observations, all measured 2026-08-21:

| Consumer | Behaviour | Why |
| :--- | :--- | :--- |
| `yolo pack ls` (reader) | **works**, lists all three local packs with their host paths | it only reports; a stale path costs nothing |
| `yolo check --no-build` (preflight) | **passes** — 27 passed, 2 warnings, rc 0 | it never asks whether the entry could be staged |
| `yolo run` (inner launcher) | **refuses** | staging a nonexistent directory is fatal |

So the preflight gives no warning that the very next launch cannot start. That is the same shape as
[`trust-paths.md`](trust-paths.md) OQ-TP7 — *"`yolo check` does not predict the refusal"* — arrived at
by a different route, and it is the second instance of it. Whatever fixes that one should be asked
whether it covers this.

> [!WARNING]
> This breaks the verification workflow `AGENTS.md` **mandates**: a nested `yolo -- bash` is how a
> `cmd/`/`internal/` change is supposed to be checked. It is not a test-only inconvenience. The blast
> radius is bounded — it bites only when the parent's config selects a local pack — but the
> maintainer's own setup does exactly that (`matt-core`, `matt-fzf`, `matt-local`), so the mandated
> path is broken for the person the instruction is written for.

### 10.2 Diagnosis: a key-level filter, and a key whose two consumers disagree

`internal/config/inherit.go` classifies every top-level key across two consumers, and `packs` claims
both:

```go
"packs": {preflight: true, nested: true,
  reason: "reported by `yolo pack ls/status` and staged by an inner launcher"},
```

The reason names the two consumers correctly and the disposition treats them as one. Only the second
breaks, because only the second *evaluates* the path.

The `loopholes` entry immediately above rules the analogous question the other way, and that ruling is
sound where it stands: its host-shaped `command`/`doctor_cmd` innards *"are not a reason to drop it:
those are evaluated host-side only… dropping the key would make `yolo loopholes list` blind to the
human's own installs — a visible omission, which §5.1 rules worse than a stale path."* The
distinction is **evaluation**, not shape: a stale path a consumer only prints is harmless, and a stale
path a consumer stages from is fatal. So "drop anything host-shaped" is the wrong generalisation, and
so is "keep it, readers cope."

What this needs is therefore a **value-level** treatment of one key in one projection, which the
current filter does not do — it is key-level throughout. The seam was built in anticipation:

> `FilterInheritErr` … *"The error is always nil today and exists so a future filter that can fail — a
> value-level rewrite rather than a key-level projection — does not change every call site."*

### 10.3 The separate defect: integration tests read machine state

The same symptom produced six failures in the container suite, and the cause there is not
inheritance — it is that **only some container tests isolate `HOME`**. `packHome` gives a test its own
user config; tests reaching it through `writeProjectWithPacks`/`tempProject` inherit that isolation,
and everything else reads whatever the machine has. Counted 2026-08-21: six files with jail tests have
no `HOME` isolation at all (`cgroup`, `imageskew`, `network`, `packagecollection`, `packages`,
`reachability` — 11 tests), and `cli_test.go` isolates 3 of its 9.

**The failure mode is worse than the failures.** An ambient user config does not only break
assertions; it can satisfy them. `security.blocked_tools`, `mise_tools`, `mcp_servers` and `loopholes`
all merge into the jail a test launches, so a machine-local config can make a test pass for a reason
the test never states. And it is **asymmetric with CI**: a fresh runner has no user config, so CI
structurally cannot observe either direction. That is the same invisible-in-CI/fatal-locally shape
that bit `warmJail`'s first draft (`agent-install-in-ci.md` §11, step one).

This is mechanical to fix now that `seedPackHome` exists as a non-`*testing.T` helper: make an
isolated `HOME` the default for every container test and let the ambient config be an explicit opt-in
that names its reason. It does not wait on any ruling in §10.4.

### 10.4 First: what is a nested jail FOR?

I reached for an answer to §10.4 before asking this, and the answer changes it. Searched
2026-08-21 across `docs/design/`, `docs/guides/` and `AGENTS.md`:

- **`USER_GUIDE.md` — the end-user documentation — never mentions nesting as a feature.** Not once.
- Every design-doc mention is one of two things: the **development verification path**
  (`AGENTS.md`: *"Verify Go changes by launching a nested jail (`yolo -- bash`)"*), or a **bug
  condition** that only manifests when the host is a jail
  ([`broker-ca-and-nested-hosts.md`](broker-ca-and-nested-hosts.md) §2, and `AGENTS.md`'s
  reachability carve-out).

So there is **no documented product use case for jail-in-jail.** It is a developer affordance whose
job is to run the *freshly built* code — a new image, a new argv — and not to be a faithful replica of
its parent.

> [!WARNING]
> That retires the premise this section was originally written on. An earlier revision leaned toward
> **rewriting** local pack paths (option (b) below) so that *"a nested jail keeps its parent's
> content,"* and objected to option (c) on the grounds that *"a nested jail then has no agent, which
> is most of what nested verification is for."* **Both were wrong.** The mandated command is
> `yolo -- bash`, not an agent, so no-agent is not a cost of the documented workflow; and "should
> resemble its parent" was an assumption, never a requirement anyone wrote down. Preserved because it
> is an easy assumption to re-make: nesting *feels* like it should be transparent, and nothing about
> the feature says it must be.

### 10.5 Options for the inheritance half

| | Option | Cost |
| :--- | :--- | :--- |
| **(a)** | Drop local `file://` entries from the **nested** projection only; preflight keeps them | Simple, fails closed, keeps `yolo pack ls` honest. A nested jail silently gets **less** than its parent — here, no `matt-fzf` file-suggestion finder and no `matt-local` pi config |
| **(b)** | **Rewrite** local entries to the staged copy (`file:///ctx/packs/<name>`) in the nested projection | Verified viable: `/ctx/packs/` holds `matt-core`, `matt-fzf`, `matt-local` already staged, so the referent exists. Preserves content across nesting. Costs a value-level rewrite and care that re-staging a staged copy is faithful (`allow_exec` rides on the config entry, so it is inherited alongside) |
| **(c)** | Do not inherit `packs` into **nested** at all | Consistent with *nothing is active by default*, and cheaper than (a) since it needs no value-level filter at all. Costs more than (a) for no gain: an EMBEDDED pack's referent is reachable in the child, so dropping it discards something that would have worked |
| **(d)** | Keep inheriting; make a missing local pack a warning the launch survives | Rejected: fails open and silently drops declared content, which is what the fatal exists to prevent |

_Leaning:_ **(a), with the drop REPORTED rather than silent.** Revised after §10.4 — I leaned (b)
first and no longer do.

Three reasons. (a) is the census's own rule — *is the referent reachable in the child?* — applied one
level down, so it needs no new principle: an embedded pack's tree comes from the binary's `embed.FS`
and IS reachable, a `file://` host path is not, and that distinction is the whole filter. (b) by
contrast invents an equivalence — *the staged copy is the pack* — which would have to answer its own
questions about re-staging a copy (exec bits re-derived, `allow_exec`, an `only` filter applied
twice); §10.4 says the fidelity that buys is not something any use case asks for. And the one real
objection to (a), that the loss is silent, is not inherent: a line naming what was dropped and why
removes it, and matches how yolo reports every other withheld contribution. (d) is not on the table.

### Open Questions

1. 💬 **OQ-SC1: drop, rewrite, or stop inheriting `packs` into the nested scope?** §10.5. This is the
   ruling; the implementation is contained either way.

   **What it decides:** whether a nested jail carries its parent's local packs, and whether
   `inherit.go` grows its first value-level rewrite (only (b) requires one).

   _Leaning:_ **(a)** — drop the entries whose referent the child cannot reach, and say so out loud.
   Revised from (b) once §10.4 established that nesting has no documented product use case, so
   "resembles its parent" is not a requirement. Full reasoning in §10.5.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-SC2: should `yolo check` predict this refusal, and is that the same fix as OQ-TP7?**
   §10.1 measured `check` passing on a config whose next launch is refused — the shape
   [`trust-paths.md`](trust-paths.md) OQ-TP7 already has open for the fetched-pack/installer case.

   **What it decides:** whether these are one fix or two, and therefore whether OQ-TP7's answer is
   allowed to be narrow.

   _Leaning:_ ask it as one question. Two independent causes producing "check passes, launch refuses"
   is evidence about the preflight's coverage rather than about either cause.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-SC3: does the harness isolate `HOME` by default?** §10.3, and independent of OQ-SC1 — it
   is a test-hygiene change, not a product one.

   **What it decides:** whether the container suite measures the repository or the machine it runs on.

   _Leaning:_ **yes**, and this is less a new rule than the completion of one the harness already
   started. `writeProject` already **refuses** a workspace config containing a `packs` key —
   `t.Fatalf`'s with *"which is USER SCOPE ONLY … Use writeProjectWithPacks so the key lands in the
   user config where it is read"* — i.e. the harness already forces a test to state its pack
   selection explicitly, and is then silent about `security`, `mise_tools`, `mcp_servers` and
   `loopholes`, which merge in from the machine unannounced. A test should specify its inputs;
   default-isolating `HOME` is what makes the half-enforced rule total. I know of no test that wants
   the machine's config, and any that does can opt in and name its reason.

   **Answer:**
   > _(empty — fill in when decided)_
