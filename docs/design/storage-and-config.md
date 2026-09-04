# YOLO Jail Storage, Configuration & Identity

**Status:** REFERENCE — describes shipped behaviour. **§1–§9 spot-checked 2026-08-23**; **§10 is a
dated follow-up that is DECIDED and BUILT** (2026-08-21, `9424284d` + `a2f2126d`, with its own
Decision Ledger at the end). `AGENTS.md` sends readers here for storage paths and state separation,
so treat a disagreement between a §1–§9 path and the tree as a bug in this file. What was **not**
re-verified this pass: the §4 mount map's per-backend rows on macOS (no Mac here — `macos-user` has
**no bind mounts at all**, which §4 should be read against) and the byte-level `.yolo/` contents.

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

All persistent jail state lives under `~/.local/share/yolo-jail/`. The tree below is **not
exhaustive** and has not been for a long time: `packs/`, `captures/`, `build/`, `approvals/`,
`flake-bundle/`, `bin/`, `locks/`, `logs/`, `state/`, `owners/`, `loopholes/` and `archive/` are
siblings of what it lists (`rg -n 'paths.GlobalStorage\(\)'` is the authority). `captures/` is
spelled out here because it is a new on-disk contract; the rest of the gap is left as-is.

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
├── captures/              → Machine-wide install-capture store (program-delivery.md §6.3),
│                            also mounted :ro at /ctx/captures in every jail so a native
│                            launcher can materialize instead of downloading:
│                            entries/<key>/tree/ is a captured installer's delta, unpacked
│                            and REFLINKED into each workspace (hardlink, then copy, when the
│                            filesystem cannot — §6.3's 2026-09-04 amendment); staging/<id>/
│                            is the scratch for a capture in flight, INSIDE this dir because
│                            admission is an os.Rename (paths.CapturesDir)
└── agents/                → Per-container AGENTS.md files
    └── yolo-<hash>/
        └── AGENTS.md      → Mounted read-only at each pack's declared briefing
                              destination — ~/.copilot/copilot-instructions.md,
                              ~/.gemini/config/AGENTS.md, ~/.claude/CLAUDE.md, …
                              (read the packs; this list is pack data)
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

1. Built-in skills (configuring-the-jail, diagnosing-the-jail)
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
- `/home/agent/.copilot/copilot-instructions.md`
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

## 10. Follow-up (2026-08-21): the launcher refuses a pack `yolo check` already calls delivered

**Status:** DECIDED and BUILT, 2026-08-21 (`9424284d`, `a2f2126d`). Both questions ruled and
implemented; the Decision Ledger is below and §10.4/§10.5 record what landed. Two independent defects
shared one symptom, and both are fixed. §1.1 above describes the
generated user scope; this section is about what the *launcher* does with what it generates — and,
after two wrong turns, the answer is that the generated config was never the problem (§10.2).

**Reads with:** [`trust-paths.md`](trust-paths.md) (OQ-TP7 refuses a copied gate for the same reason
§10.4 does), [`agent-install-in-ci.md`](agent-install-in-ci.md) (whose integration work surfaced this).

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

### 10.2 Diagnosis: not inheritance — a fix that landed at one call site

> [!WARNING]
> **The inheritance framing was wrong, and two earlier revisions of this section were built on it.**
> They argued that the generated user scope should not carry a host path the child cannot reach, and
> weighed four options for filtering or rewriting `packs` in the nested projection. **None of them is
> the bug.** The generated config is *correct*: `file:///home/matt/.dotfiles/packs/matt-core` is the
> pack's true provenance, and `yolo pack ls` showing it is right. Preserved because the symptom
> ("config names an unreachable path") points straight at inheritance, and it took reading the other
> caller to see that the config was never the problem.

The real shape: **`Store.Resolve` has two callers, and only one of them knows what a jail is.**

Both ask `packsrc` to resolve a local pack address, and both get the same error from
[`store.go:223`](../../internal/packsrc/store.go#L223) — `local pack %s is not a directory`. Then they
diverge:

| Caller | On that error | Result |
| :--- | :--- | :--- |
| `yolo check` ([`check/packs.go:127`](../../internal/cli/check/packs.go#L127)) | asks `stagedPackDir(name)` — is there a delivered copy under `YOLO_PACK_ROOT`? | `[PASS] matt-core: staged at /ctx/packs/matt-core` |
| `yolo run` ([`run/packs.go`](../../internal/cli/run/packs.go)) | nothing | refuses the launch |

**And this exact bug was already found, ruled, and fixed — in `check` alone.** Its regression test
records the identical symptom, character for character:

> The reported symptom: `yolo check` run inside a jail printed
> `[FAIL] matt-core: local pack /home/matt/.dotfiles/packs/matt-core is not a directory`
> for three packs that were functioning normally, staged at /ctx/packs.
> — [`check/packs_test.go:300-311`](../../internal/cli/check/packs_test.go#L300-L311)

The ruling is in the code, and it is the right one: *"A SOURCE THAT IS NOT VISIBLE FROM HERE IS NOT A
BROKEN PACK … What was actually delivered is the STAGED TREE the launcher mounted, so ask that
instead. Keyed on the filesystem rather than on 'am I in a jail' deliberately: the question is whether
a staged copy exists, which is the thing that decides whether the pack works, and it cannot misfire on
a host (where no staged tree is mounted, so this branch never fires)."*

So there is nothing to decide about *what* the answer is. `grep stagedPackDir` returns **one call
site**, in `check`. The launch path never learned it, and the test pins only the caller that did —
the failure class `AGENTS.md` names under Testing, in its second spelling: the fix is real, the test is
real, and deleting the *other* call site's absence changes nothing that goes red.

### 10.3 Why `yolo check` in a jail was never going to catch it

`yolo check` run inside a jail has two uses, and they are wildly unequal in frequency:

1. **Check a nested config before launching a jail-in-jail** — rare.
2. **Check a workspace config after editing it, before asking a human to restart the host jail** —
   this is what `AGENTS.md` and the `configuring-the-jail` skill instruct on every config edit, and
   it is essentially the only use.

For use 2 the pack list is **structurally irrelevant**: `packs` is user-scope-only by construction —
`LoadPacks` *"deliberately takes no merged config: reading the user file directly is what makes
workspace scope inexpressible"* ([`config/packs.go:179-183`](../../internal/config/packs.go#L179-L183))
— so a workspace config being checked cannot name a pack at all. `sectionPacks` is reporting on the
OUTER jail's inherited selection, which is true, and beside the point of the thing the agent changed.

That is why "make `check` predict the refusal" was the wrong instinct (and why the OQ that proposed it
is withdrawn below). `check` is not behind the launcher here — it is **ahead** of it. It already
computed the staged path the launcher needs.

### 10.4 The one question left: where does the fallback live?

Porting `stagedPackDir` into `run/packs.go` would fix the launch and create the defect this repo has
already written down once. [`trust-paths.md`](trust-paths.md) OQ-TP7 poses the same shape for the host-access
gate and rejects the copy outright: *"A third gate copied into `check` would satisfy that scan
vacuously … So the question is **where the gate lives** if a third caller needs it."* Two
implementations of one resolution rule is the drift, not the fix.

Three homes, and the choice is a real one:

| | Home | Consequence |
| :--- | :--- | :--- |
| **(i)** | `packsrc.Store.Resolve` itself consults `YOLO_PACK_ROOT` | One writer, every caller fixed at once, including any future one. Costs `packsrc` an awareness of the jail's delivery convention — a store that resolves *addresses* would start knowing about *mounts* |
| **(ii)** | A shared resolver beside `Resolve` (`ResolveOrStaged`) that both callers use | Keeps `Resolve` address-only and makes the fallback explicit at each call site. Costs a second entry point that a new caller can forget — the same way `run` forgot this one |
| **(iii)** | Keep it in `check`, and have `run` refuse only when no staged tree exists | Smallest diff, and exactly the two-implementations outcome OQ-TP7 refuses |

_Leaning:_ **(i).** The predicate is already filesystem-keyed and already argued to be safe on a host
(*"it cannot misfire on a host, where no staged tree is mounted, so this branch never fires"*), so
putting it in the one place that owns resolution makes every caller correct by construction — which is
the property (ii) leaves to each caller's memory and (iii) abandons. The honest cost is the layering
smudge in the (i) row, and it is worth naming when it lands rather than pretending resolution and
delivery are cleanly separable.

**LANDED (2026-08-21), option (i).** `Store.Resolve(a Addr, name string)` tries the address first and
falls back to the delivered tree only when that fails; `Resolved.StagedFrom` names the tree it fell
back to, and is empty for every ordinary resolve. `check` reports off that field and keeps its
`staged at <path>` line plus the host-side note; `run/packRoot` passes the same name and stays silent,
because for a nested launch the delivered copy is the NORMAL source rather than a degradation.
`Store.Getenv` (nil ⇒ `os.Getenv`) is the seam the tests drive, since this repo's own jail always has
`YOLO_PACK_ROOT` set and an ambient read would measure the machine. The layering cost from the (i)
row is real and is now written where it happens, in `Resolve`'s doc comment.

Three things the section above did not say, found while landing it:

- **The fallback is not local-only.** A jail cannot see the pack STORE either, so a fetched pack
  resolves to `pack %s has never been fetched` in here for the same structural reason. Keying on the
  failure rather than on `IsLocal()` fixes both, and `run`'s loophole/supersession resolvers — which
  `continue` silently past an unresolvable pack — stop dropping local packs' loopholes in a jail as a
  side effect.
- **The delivered dir is named by the entry's SLUG, not its Name** (`run` stages into
  `stagingRoot/<entry.Slug()>`). `check`'s original lookup used `e.Name`, which agrees for every
  conventional name and silently misses one needing escaping (`a_b` stages as `a_5fb`). Both callers
  now pass `Slug()`.
- **The name has to be threaded, not derived.** It is a config fact (an explicit `name:`, else the
  source URL's last path segment), so `packsrc` cannot recover it from an `Addr` — deriving it there
  would be a second spelling of a rule `internal/config` owns, i.e. the same defect one layer down.

### 10.5 The separate defect: integration tests read machine state

Unchanged by the above, and worth fixing on its own merits — though note that fixing §10.4 makes the
six observed failures disappear as a side effect, since the launch would succeed.

**Only some container tests isolate `HOME`.** `packHome` gives a test its own user config, and tests
reaching it through `writeProjectWithPacks`/`tempProject` inherit that isolation; everything else reads
whatever the machine has. Counted 2026-08-21: six files with jail tests have no isolation at all
(`cgroup`, `imageskew`, `network`, `packagecollection`, `packages`, `reachability` — **10** tests),
`cli_test.go` isolates 3 of its **8**, and the list above **misses two**:
`isolation_test.go:TestMiseVenvActivation` and `mcp_test.go:TestSameFilePresetAndNullOverrideIsRejected`
both use a bare `t.TempDir()`. Recounted while implementing OQ-SC3 — the first pass undercounted the
per-file totals and overcounted the file list, so the exposure is **wider** than stated, not narrower.
That is also why the isolation went into `requireJail` rather than `writeProject`: four of the exposed
tests never call `writeProject` at all, so a hook there would have missed them.

**The failure mode is worse than the failures.** An ambient user config does not only break
assertions; it can satisfy them. `security.blocked_tools`, `mise_tools`, `mcp_servers` and `loopholes`
all merge into the jail a test launches, so a machine-local config can make a test pass for a reason
the test never states. And it is **asymmetric with CI**: a fresh runner has no user config, so CI
structurally cannot observe either direction — the same invisible-in-CI/fatal-locally shape that bit
`warmJail`'s first draft ([`agent-install-in-ci.md`](agent-install-in-ci.md) §11).

**LANDED (2026-08-21).** The isolation sits in `requireJail`, not `writeProject` — it is the line
every container test already writes, so a NEW test cannot read machine state by forgetting a helper;
it would have to skip the gate that makes it a container test. `isolateHome` wraps the existing
`seedPackHome`, so there is still one implementation of the shared-store rule, and `ambientHome(t,
reason)` is the opt-out (no test needs it). Two things the landing added that this section did not
foresee:

- **A `hostHome` capture.** Every packs-needing test now isolates TWICE (once in `requireJail`, again
  in `packHome`), and reading `os.Getenv("HOME")` the second time would link the second home's stores
  to the FIRST temp home's symlinks — a chain that dangles as soon as that tempdir is removed, i.e.
  the `mkdir: file exists` CI outage one indirection further out. The host home is captured on first
  redirect so every isolated home links straight to the machine.
- **`.cache/nix` and `.config/nix` joined `packHomeSharedStores`.** Isolation put the nix-driven tests
  behind the redirect for the first time; the nix STORE is shared regardless, but the fetcher cache
  mapping a locked flake input to its store path is HOME-rooted, and `.config/nix` holds the
  substituter that decides download-vs-compile. Hiding them would have sent nix to the network for
  inputs it already had.

Two tests were passing for a machine-local reason and now state their inputs: `TestCgroupDelegation`
selects the `cgroup-delegate` pack and sets its `enabled` switch, and `TestInJailServiceReachability`
selects `claude` — without a selection nothing publishes an endpoint, so it would have skipped
everywhere and silently deleted the coverage that exists because a loopback-TLS outage shipped.

This completes a rule the harness already half-enforces rather than inventing one. `writeProject`
already **refuses** a workspace config containing `packs` — *"which is USER SCOPE ONLY … Use
writeProjectWithPacks so the key lands in the user config where it is read"* — so a test is already
forced to state its pack selection explicitly, while `security`, `mise_tools`, `mcp_servers` and
`loopholes` merge in from the machine unannounced. A test should specify its inputs.

### Open Questions

1. ✅ **OQ-SC1: where does the staged-tree fallback live? — RESOLVED (2026-08-21)**

   > This question previously asked whether to drop, rewrite, or stop inheriting `packs` in the nested
   > scope, and went through two leanings before the framing itself turned out to be wrong (§10.2).
   > Withdrawn and restated under the same ID before being answered — the inherited config was never
   > the defect.

   **Answer:**
   > Delegated to the implementer's judgement ("your call — I don't care about code design"), and the
   > call is **(i): inside `Resolve`**, so every caller is correct by construction rather than by
   > remembering. §10.4 has the reasoning and the honest cost — `packsrc` gains an awareness of the
   > jail's delivery convention, which is a layering smudge worth naming rather than hiding.

2. ✅ **OQ-SC2: should `yolo check` predict this refusal? — WITHDRAWN (2026-08-21)**

   **Answer:**
   > Wrong question. `check` is ahead of the launcher here, not behind it: it already resolves the
   > staged tree and reports `[PASS]`. The general OQ-TP7 concern — a preflight that does not predict a
   > launch refusal — stands on its own for the fetched-pack/installer case; this was not an instance
   > of it. See §10.3.

3. ✅ **OQ-SC3: does the harness isolate `HOME` by default? — RESOLVED (2026-08-21)**

   **Answer:**
   > Yes. It finishes the rule `writeProject` already half-enforces by refusing a workspace `packs`
   > key, and no known test wants the machine's config; any that does opts in explicitly and names its
   > reason. §10.5.

## Decision Ledger — §10

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| OQ-SC1 | The staged-tree fallback lives **inside `packsrc.Store.Resolve`**, not copied into `run` — one writer, every caller correct by construction. Supersedes four withdrawn *inheritance* options; the generated config was never the defect | 2026-08-21 | [§10.2](#102-diagnosis-not-inheritance--a-fix-that-landed-at-one-call-site), [§10.4](#104-the-one-question-left-where-does-the-fallback-live) |
| OQ-SC2 | **Withdrawn.** "Should `check` predict the refusal" was the wrong question — `check` is ahead of the launcher, already resolving the staged tree and reporting `[PASS]`. The general OQ-TP7 concern stands on its own for the fetched-pack case | 2026-08-21 | [§10.3](#103-why-yolo-check-in-a-jail-was-never-going-to-catch-it) |
| OQ-SC3 | The harness **isolates `HOME` by default** for every container test; an ambient-config test must opt in and name its reason | 2026-08-21 | [§10.5](#105-the-separate-defect-integration-tests-read-machine-state) |
