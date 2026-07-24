# Migrating existing persistent configs onto the prism (composition) engine

**Status:** Design of record — **NEW, 2026-07-20.** Companion to the finalized
[../plans/agent-settings-composition.md](../plans/agent-settings-composition.md)
("the prism"). That doc fixes **how** a generated config composes going forward;
this doc fixes **how the config that already exists on disk** — written by
today's bespoke generators into the persistent jail home — converges cleanly onto
the prism the first time a prism-backed surface boots. It obeys the
config-ownership principle in
[storage-and-config.md](storage-and-config.md) §1.1.

It is a durable design, not a runbook: it names the invariants, the exact
first-boot bootstrap, the per-surface specifics (including a concrete immediate
fix for the mise `node`/`python`/`go` pin), the last-resort clear-and-rebuild
escape hatch, and how the work sequences into the roadmap's Phase B / Phase C.

---

## 0. Why this doc exists — the one confirmed live bug

The prism regenerates every owned surface each boot. But the surfaces it replaces
were written by **in-place editors that add and update but never remove** what
they previously wrote, into a **persistent** home (`/home/agent`, per-workspace
r/w overlay, survives across sessions). So the on-disk file already carries stale
bespoke output that the current generators cannot self-heal — and, worse, the
prism's capture-diff overlay (§5 of the plan) will, on first boot, mistake that
stale output for an intentional in-jail edit and **pin it forever**.

**Confirmed in this very jail:** `~/.config/mise/config.toml` still contains

```toml
[tools]
node = "22"
python = "3.13"
go = "latest"
```

`readlink -f "$(which node)"` resolves to `/mise/installs/node/22.23.1/bin/node`
— the non-nix mise runtime **shadowing the baked `/bin/node`**, the exact
`LD_LIBRARY_PATH` / MCP-wrapper whack-a-mole
[mise-node-dynamic-linking.md](mise-node-dynamic-linking.md) warns about. Those
lines were written by an older yolo when `miseBaseTools` was non-empty. Today
`miseBaseTools` is `[]` (all runtimes are baked — see `internal/entrypoint/mise.go`),
yet `GenerateMiseConfig` **does not remove them**: its only self-heal paths are
dropping duplicate key-lines, stripping `agents.AllMiseRetire` tokens, and
forcing base tools present/non-`system`. A departed base tool is invisible to all
three. The stale pin persists indefinitely.

This is not a mise quirk. It is a **class**: bespoke in-place editors with no
removal path. mise and git identity are its two acute members. The migration
must converge every surface to *what the prism would generate*, not carry stale
bespoke output forward — and must do so without the overlay freezing that stale
state into permanence.

---

## 1. The migration principle

> **yolo owns the bytes it generates. When a surface moves onto the prism, the
> existing on-disk file must converge to what the prism *would* generate from
> today's layers — it must not carry stale bespoke output forward, and it must
> not let the capture-diff overlay mistake pre-migration bytes for a
> user-authored in-jail edit.**

Corollaries, each load-bearing below:

1. **The pre-existing file is not authoritative.** The fresh pipeline render is.
   Treating the on-disk file as the baseline is precisely what pins `node = "22"`
   forever (§3). The migration's job is to *replace* the file with a truthful
   render, then begin capturing edits from that truthful baseline.
2. **A former default is not a managed key.** The prism's `managed` layer is
   re-applied last and self-heals the keys yolo *asserts* (Claude's
   `permissions`, pi's `defaultProjectTrust`, …). But `node = "22"` was a
   *default*, not a managed key; the empty-`miseBaseTools` defaults layer will
   never emit it and the managed re-apply will never scrub it. Former-defaults and
   host-synced values are the exposure the managed layer does **not** cover.
3. **Convergence beats cataloguing.** Prefer a mechanism that needs no
   permanently-maintained list of every stale key yolo ever wrote. The clean
   per-surface path (§3) needs no such catalogue; the narrow mise cleanup (§4.1)
   is the one bounded exception, and only because it must run *before* the first
   prism render for that surface.
4. **Clear-and-rebuild is a real, allowed tool — but the last resort** (§5),
   gated behind an explicit marker so it never runs silently, and forbidden from
   touching anything yolo does not own.

---

## 2. Surface inventory and stale-state risk

Eight persistent surfaces under `/home/agent` move onto the prism. Risk =
severity of stale bespoke output that survives, absent a migration step.

| Surface | File(s) | Generator | Self-heal today | Stale risk |
|---|---|---|---|---|
| **mise global config** | `~/.config/mise/config.toml` | `prism_mise.go ConfigureMisePrism` (was `mise.go GenerateMiseConfig`) | ✅ **ported** — prism first-migration seed (was in-place editor) | **HIGH** (resolved) |
| **git identity** | `~/.gitconfig` | `identity.go configureGit` | none — sets keys only when env present, never unsets | **HIGH** |
| Claude settings | `~/.claude/settings.json` (+ snapshot `yolo-host-synced-settings.json`, sidecar `yolo-managed-mcp-servers.json`) | `claude.go ConfigureClaude` | 3-way host merge vs. snapshot; managed keys force-set each boot | MED |
| Pi settings | `~/.pi/agent/settings.json` (+ snapshot) | `agent_configs.go ConfigurePi` | same 3-way pattern; force-sets `defaultProjectTrust` | MED |
| Gemini settings | `~/.gemini/settings.json` (+ sidecar) | `agent_configs.go ConfigureGemini` | MCP reconciled via sidecar; some keys `setDefault` (see §4.5 bug) | MED |
| Opencode settings | `~/.config/opencode/opencode.json` (+ sidecar) | `agent_configs.go ConfigureOpencode` | MCP reconciled via sidecar; force-set `permission` | MED |
| Codex config | `~/.codex/config.toml` (+ sidecar) | `codex.go ConfigureCodex` | MCP reconciled via sidecar; force-set approval/sandbox | MED |
| Copilot MCP/LSP/config | `~/.copilot/mcp-config.json`, `lsp-config.json`, `config.json` | `agent_configs.go ConfigureCopilot` | full overwrite-from-scratch every boot | **NONE** — ported 2026-07-23 (config: stateful; mcp/lsp: stateless `renderSurfaceComputed`) |
| agy MCP | `~/.gemini/antigravity-cli/mcp_config.json` | `prism.go ConfigureAgyPrism` | full overwrite-from-scratch every boot | **NONE** — ported 2026-07-23 (stateless `renderSurfaceComputed`) |

**Reading the risk column:**

- **HIGH (mise, identity)** have *neither* a snapshot (Claude/pi) *nor* a
  managed-MCP sidecar (gemini/codex/opencode) *nor* full overwrite (copilot) to
  bound their output. Their bespoke output accretes and never leaves. These are
  the two surfaces that need an explicit pre-render cleanup in addition to the
  first-boot bootstrap.
- **MED** surfaces self-heal their yolo-managed slice each boot (via snapshot
  rollback or a managed-MCP sidecar), so their residue on migration is (a)
  **orphaned sidecar/snapshot files** the prism no longer reads, and (b) the
  **first-migration overlay freeze** (§3) if the bootstrap is done naïvely. Not
  garbage-in-the-config; a behavior change to prevent.
- **NONE (copilot)** already behaves like the prism: `mcp-config.json` and
  `lsp-config.json` are rebuilt from `LoadMCPServers`/`LoadLSPServers` and written
  without reading the existing file, so a dropped server simply vanishes next
  boot. `config.json` is a write-once `{"yolo": true}` bootstrap file. Cleanest to
  port; no sidecar because overwrite makes one unnecessary.

**The cross-cutting killer applies to all eight:** on the *first* prism boot there
is no `last_render` sidecar, so §5's `mergeDiff(last_render, current_file)` sees
the entire existing file as an in-jail edit. §3 is the fix, and it must land
**before any surface is ported.**

---

## 3. The first-boot bootstrap — the clean path (do this first, for every surface)

### 3.1 The hazard, confirmed against the engine

`internal/agentcfg/engine.go` `diffValue` (the object-vs-object branch): when
`old` is the empty/absent `last_render` (`{}`), **every** key in the new value
hits the `if !existed { patch[k] = nv }` path — the whole on-disk file is emitted
as the patch. So `mergeDiff(∅, current_file)` returns `current_file` verbatim. The
plan's §5 loop then runs `overlay = deepMerge(overlay, delta)` with
`delta = entire file`, capturing 100% of the pre-existing file — old bespoke
output (`node = "22"`) **plus** any genuine in-jail edits, indistinguishably —
into the overlay. Because the overlay outranks host/workspace (§4) and **never
ages** (§9, reset-only), that stale content is pinned forever. Even a corrective
render that drops `node` emits nothing that outranks the overlay.

The engine functions exist and are unit-tested, but **no production caller yet
wires `mergeDiff`/`last_render`/`overlay` into boot** (only `compose_test.go` /
`engine_test.go` exercise them; `cli/config.go`'s `render` path composes with
`Overlay` unset). So this is a **design gap to close before the boot loop lands,
not a live regression.** The fix belongs to whoever writes the §5 sidecar
persistence step.

### 3.2 The rule: key first-migration on the ABSENCE of `last_render`

Per surface, per boot, before running the capture step:

```
if last_render sidecar is ABSENT for this surface:      # first-migration signal
    render = pipeline(defaults, host, workspace, overlay=∅, transform, managed)
    write(surface_path, render)                          # replace the bespoke file
    write(last_render_sidecar, render)                   # truthful baseline
    write(overlay_sidecar, {})                           # overlay starts EMPTY
    # DO NOT run mergeDiff / capture this boot.
else:                                                     # steady state, plan §5
    delta   = mergeDiff(last_render, current_file)
    overlay = deepMerge(overlay, delta)
    render  = pipeline(defaults, host, workspace, overlay, transform, managed)
    write(surface_path, render); write(last_render_sidecar, render)
```

Why this is correct:

- The overlay begins **genuinely empty**, so no pre-existing byte is captured. The
  stale `node = "22"` line (a former default the empty-`miseBaseTools` layer no
  longer emits) is simply absent from the fresh render and never re-appears.
- From the **second** boot onward the normal §5 loop runs against a *truthful*
  `last_render`, so only genuinely-in-jail edits (a `/config` change, a permission
  approval, a plain file edit) become deltas and survive. The overlay's edit-
  preservation guarantee is intact going forward — it just starts from a clean
  baseline instead of a polluted one.
- **Never seed `last_render` from the pre-existing on-disk file.** That is the
  trap: it re-introduces the exact hazard of §3.1, because the on-disk bytes are
  not authoritative. Seed from the *fresh pipeline render*, always.

**Cost, stated plainly:** the first-migration boot **drops any un-captured
pre-migration in-jail edit** for that surface. This is correct and unavoidable —
with no `last_render` baseline, a pre-migration edit is on disk *indistinguishable*
from stale generator output. There is nothing to diff it against. We accept the
loss on the one migration boot; §5 is the escape hatch for the rare surface where
even the fresh render is unwanted.

### 3.3 Defensive handling of dangling sidecars

Treat the surfaces defensively, because a partially-migrated or hand-mangled home
can leave the sidecars inconsistent:

- **`last_render` absent, `overlay` present** (or any other mismatch): treat the
  missing `last_render` as the first-migration signal and **reset the overlay to
  `{}`.** Do not trust a dangling overlay that predates the engine — it may itself
  be a leftover from an aborted migration.
- **Both present:** steady state; run the §5 loop.
- **`last_render` present, `overlay` absent:** initialize `overlay = {}` and run
  the loop (no capture is lost; last boot simply had no edits).

### 3.4 The tombstone-preserving accumulation fix (required regardless)

Independently of first-boot, the §5 accumulation step `overlay = deepMerge(overlay,
delta)` uses RFC-7386 `deepMerge`, which treats a `null` tombstone applied to a
key **absent from the accumulator** as a no-op delete — silently dropping the
deletion (see `engine_test.go` note). This does **not** bite the clean seed (a
freshly-seeded `last_render` equals what was just written, so the first-boot delta
is empty — no tombstones). But it **does** make the naïve "seed from on-disk file"
path doubly unrecoverable, and it breaks multi-boot deletions in steady state. The
accumulation step must use a **tombstone-preserving merge** (store `null`
tombstones even when the key is absent in the accumulator). This is a correctness
fix for the overlay, orthogonal to but required alongside the migration.

---

## 4. Per-surface migration specifics

The §3 bootstrap is the uniform mechanism. A few surfaces need extra care because
either the bespoke file carries stale content the fresh render will *not* clean
(HIGH surfaces need a pre-render scrub), or there are orphan sidecars to delete,
or a pre-existing generator bug the prism round-trip must not inherit.

### 4.1 mise (`~/.config/mise/config.toml`) — HIGH — ✅ RESOLVED by the port (2026-07-22)

**As shipped, the §3 bootstrap alone fixed mise — no pre-render scrub was needed.**
The port (`ConfigureMisePrism`) confirmed in the engine that on the first prism
boot `ComposeStateful` seeds from a fresh render with an empty overlay and
**discards the on-disk file** (`staterender.go`), so a stale `node`/`python`/`go`
`[tools]` line — present in no yolo layer — simply does not render. The bounded
`miseFormerDefaults` catalogue this section originally proposed turned out
unnecessary: convergence (§1 corollary 3) is achieved by the seed, not a
catalogue. The subsections below are preserved as the ORIGINAL plan of record
(and its rationale); the actual implementation is simpler.

The `node = "22"` shadow was a **live, actively-harmful bug** (wrong node shadows
the baked runtime), which is why this section originally proposed shipping a
standalone scrub ahead of the full port. In the event, the port landed directly.
Historical proposal (superseded):

> **One-sentence fix:** in `GenerateMiseConfig`, after the existing dedupe/retire
> pass, strip any `[tools]` line for a runtime that is (a) in the set of
> **yolo-formerly-defaulted runtimes** (`node`, `python`, `go`) and (b) **not**
> present as an intentional workspace pin (`YOLO_MISE_TOOLS` / `/workspace/mise.toml`),
> because such a line can only be stale output from an older yolo whose
> `miseBaseTools` was non-empty.

Concretely:

- Add a `miseFormerDefaults = {"node", "python", "go"}` set (a bounded,
  documented catalogue — this is the narrow §1-corollary-3 exception, justified
  because it must run *before* the first prism render and because these three are
  the exact former defaults, not an open-ended list).
- Extend the existing in-place editor: for each `tool` in `miseFormerDefaults`, if
  `tool` is **not** a key in `loadInjectedTools(e)` (the `YOLO_MISE_TOOLS`
  workspace layer) and **not** pinned in `/workspace/mise.toml`, delete its
  `^tool\s*=\s*"…"` line — mirroring the `AllMiseRetire` regex strip that already
  exists a few lines above.
- **Guard rails — never strip an intentional override.** The check against
  `YOLO_MISE_TOOLS` and `/workspace/mise.toml` is mandatory: a workspace that
  legitimately pins `node = "20"` must keep it. The strip applies **only** to a
  former-default runtime with **no** live workspace/config layer asserting it.
  (A workspace pin is the one case that legitimately reintroduces a non-nix
  runtime — nix-ld makes that robust; see `mise.go`'s `miseBaseTools` comment.)

Under the full prism port later, this same logic is expressed declaratively: mise
becomes `codec=toml`, `defaults = {}` (empty `miseBaseTools`), `workspace =
mise_tools` (the `YOLO_MISE_TOOLS`/`mise.toml` layer), and the fresh render simply
never emits `node`/`python`/`go` unless a workspace layer pins them — at which
point the §4.1 pre-render scrub is subsumed and can retire. Ship the scrub now;
delete it in Phase C when mise is fully on the engine.

### 4.2 git identity (`~/.gitconfig`) — HIGH

`configureGit` runs `git config --global` only when
the `YOLO_GIT_*` env var is present and non-empty; there is **no
unset path**. So a host that *removes* `YOLO_GIT_EMAIL`, `YOLO_GIT_NAME`, or
`YOLO_GLOBAL_GITIGNORE` leaves the previously-written `user.email` / `user.name` /
`core.excludesFile` in the persistent `~/.gitconfig` forever. No snapshot exists
to roll it back.

Migration to the prism:

- Introduce a **git-kv codec** (a small `key = value` codec over the git-config
  keyspace yolo owns). The identity surface's manifest enumerates the **full set
  of keys yolo owns**: `user.name`, `user.email`, and `core.excludesFile`.
- Because the prism regenerates from the env each boot, a removed env var means
  the key is simply **absent from the render** — the removal path the bespoke
  editor never had. But: the pre-existing `~/.gitconfig` contains keys the pipeline
  has no memory of. The §3 bootstrap handles this **only for the owned keyspace**
  — the fresh render omits the departed key and, on write, the surface must be
  written such that the owned keys reflect the render.
- **Ownership boundary is critical here.** `~/.gitconfig` frequently contains
  user-authored keys yolo does not own (aliases, `pull.rebase`, credential
  helpers, host-mirrored settings). The git-kv codec/render must be **scoped to
  the owned keyspace**: on render it asserts (or, for a departed env var, removes)
  *only* yolo's enumerated keys, and leaves every other key untouched. A blanket
  overwrite of `~/.gitconfig` would be a data-loss regression. This is the one
  surface where "replace the file" (§3.2) must instead be "reconcile the owned
  keys within the file" — implement the codec as a scoped key reconciler, not a
  whole-file replace.

### 4.3 Claude & Pi settings — MED (snapshot + first-migration freeze)

Both use the three-way host merge (`syncHostSettings`) against a snapshot file,
plus unconditional force-set of managed keys (Claude: `permissions.*`,
`defaultMode`, `skipDangerousModePermissionPrompt`, autoupdater, LSP env, plugin
pruning; pi: `defaultProjectTrust`). The managed keys self-heal (they map cleanly
to the prism `managed` layer). Host-key removal is handled today by snapshot
rollback; under the prism it needs no memory (a dropped host key is absent from
the next render).

Migration residue:

- **Orphaned files:** `yolo-host-synced-settings.json` (Claude `~/.claude/`, pi
  `~/.pi/agent/`) and `yolo-managed-mcp-servers.json` (Claude) become dead files
  the prism never reads. **Delete them during the migration boot** (§4.7) to avoid
  confusion and future-yolo mis-reads.
- **First-migration freeze:** handled by §3 — the empty-overlay seed means the
  currently host-synced values are *not* captured as edits, so a host key removed
  *after* migration correctly disappears on the next render (instead of being
  wrongly pinned).
- Pi's host settings source no longer depends on any config key: `host_pi_files`
  (and its `YOLO_HOST_PI_FILES` env) was RETIRED — the host-file set is now the
  yolo-declared `agents.AgentSpec.HostFiles` constant and the prism reads
  `/ctx/host-pi/settings.json` fail-open (plan §10.4).

### 4.4 Opencode & Codex — MED (managed-MCP sidecar)

Both load the existing file, force-set a couple of managed keys (opencode:
`permission="allow"`, `$schema` via `setDefault`; codex: `approval_policy="never"`,
`sandbox_mode="danger-full-access"`), and reconcile the MCP table by deleting
previously-managed names (read from the `yolo-managed-mcp-servers.json` sidecar)
then re-adding. MCP staleness already self-heals via the sidecar, so the residue is
the **orphaned sidecar** (delete on migration, §4.7) plus the first-migration
freeze (handled by §3).

**Codex has a separate pre-existing data-loss bug the prism must fix, not
inherit:** `dumpCodexTOML` silently **drops any non-`mcp_servers` nested `[table]`**
a user added, emitting only a warning. The prism's TOML codec round-trip
(`codec/toml.go`) **must preserve** user-authored tables — this is a correctness
requirement of the Codex port, tracked here so it is not lost. (It also means the
Codex first-migration render must not discard user tables: the fresh render
composes the owned keys over the decoded existing tables, preserving the rest.)

### 4.5 Gemini — MED (managed-MCP sidecar + a latent security bug)

Same MCP-sidecar reconciliation pattern. `general.enableAutoUpdate*` are force-Set
(self-heal). But `security.approvalMode` and `security.enablePermanentToolApproval`
are written with **`setDefault`, not `Set`** — so a pre-existing user value
silently disables the intended YOLO default and **survives** (a latent
security-posture bug, independent of the prism). The Gemini manifest must place
these two keys in the **`managed`** layer (asserted, not defaulted) so the fresh
render force-corrects them. Fixing this is part of the Gemini port, and the §3
seed means the corrected values are what gets baselined.

### 4.6 Copilot — NONE

`config.json` (`{"yolo": true}`) is the static surface: it ported as the
proof-of-concept non-agent-config target (`ConfigureCopilotPrism`), rendered by
the edit-preserving stateful path exactly like pi/agy settings.

`mcp-config.json` / `lsp-config.json` are the **dynamic siblings** — full
overwrite-from-scratch every boot, no edit preserved. **Ported 2026-07-23** onto
a distinct, deliberately DIFFERENT prism path: `renderSurfaceComputed`, a
*stateless* render that composes the surface through the engine and writes ONLY
the surface file — **no `last_render`, no overlay, no host source**. This is the
key design point: a pure-overwrite sibling must NOT go through the stateful path
(`renderSurfaceStateful`), because that path would begin capturing in-jail edits
into an overlay, silently converting an intentional overwrite into an
edit-preserving surface. The live table (`LoadMCPServers` / reshaped
`LoadLSPServers`) rides the **computed layer**; each surface carries only an
empty-wrapper `Default` (`{"mcpServers":{}}` / `{"lspServers":{}}`) so the file
keeps its shape when the table is empty and `yolo config render` has a
meaningful preview. The bespoke `writeCopilotDynamicConfigs` is deleted.

Behavior is unchanged in substance (same content, same overwrite semantics). Two
inert byte-level diffs, both invisible to copilot (it re-parses as JSON) and both
already accepted for every other prism surface: keys now sort alphabetically (the
shared JSON codec vs. the old `OrderedMap` insertion order), and a commandless LSP
entry omits its `command` rather than emitting an explicit `null` (an RFC-7386
null-leaf the engine drops — such a server is nonfunctional either way).

**agy's `mcp_config.json`** (Google Antigravity CLI) is the same shape at a
different path and ported the same way (`renderSurfaceComputed(e, "agy", "mcp",
…)`) in the same change; agy's static `settings.json` was already on the stateful
path (§4, born-on-prism). Verified at real nested-jail boot (sorted keys,
computed servers present, no sidecars written for the sibling surfaces).

### 4.7 Orphan-file cleanup (all MED surfaces)

The prism obsoletes these bespoke sidecar/snapshot files. Delete them on the
migration boot for the surface (idempotent — `remove-if-exists`), so a stale file
never confuses a future reader:

| File | Location(s) |
|---|---|
| `yolo-host-synced-settings.json` | `~/.claude/`, `~/.pi/agent/` |
| `yolo-managed-mcp-servers.json` | `~/.claude/`, `~/.gemini/`, `~/.codex/`, `~/.config/opencode/` |

Cleanup is **scoped to files yolo wrote** (these exact names) — never a directory
sweep. It runs as part of that surface's first-migration boot, gated by the same
`last_render`-absent signal, so it happens exactly once.

---

## 5. Last-resort fallback — clear-and-rebuild (allowed, gated, never silent)

The §3 bootstrap is automatic, per-surface, and clean, and it is the default. But
the user explicitly permits a **whole-config clear-and-rebuild** as a last resort
for the case where even the fresh per-surface render yields an unwanted state (a
surface whose pre-existing file had drifted so badly that reconciling the owned
keys is not enough, or a corrupted/partially-migrated home). This is the manual
escape hatch, **not** the migration mechanism.

### 5.1 Shape

A one-time operator-initiated command, e.g. `yolo config migrate --rebuild`
(or, per-surface, the existing `yolo config overlay --reset <agent>` from plan
§5/§9, which is the narrower form). Its effect on an owned file is *identical* to
§3 (drop stale, regenerate from constructions) — the difference is that it is
**explicit and operator-triggered**, for when the automatic seed is not enough.

### 5.2 What it clears

Only **yolo-owned user-scope config** under `/home/agent`:

- The owned surface files themselves (mise config, agent settings files, MCP/LSP
  configs, the owned git keys).
- The prism sidecars: `.yolo/last_render`, `.yolo/overlay` (per surface).
- The obsolete bespoke sidecars/snapshots of §4.7.

After clearing, the next boot sees `last_render` absent everywhere and runs the §3
bootstrap for every surface — a full clean rebuild from constructions.

### 5.3 What it must NEVER clear (hard invariant)

- **Workspace files** — anything under `/workspace`. That is the operating agent's
  and mirrors the host (§2 of the plan; ownership principle §1.1). yolo never
  writes it and clear-and-rebuild never touches it. This explicitly includes
  `yolo-jail.config.lua` and `/workspace/mise.toml`.
- **Host mirrors / host source config** — the `:ro`-staged host files
  (`~/.claude` on the host, etc.). The clear operates only on the *jail user
  scope*, never reaches back to the host.
- **Agent project/session state** — `~/.claude/projects/`, `~/.copilot/logs/`,
  `~/.cache/gemini-cli/logs/`, conversation history, credentials/OAuth tokens.
  These are runtime state, not composed config. A clear that wiped them would
  destroy the agent's memory and log the user out.
- **The user's `config.lua` / `config.jsonc`** — `~/.config/yolo-jail/config.lua`
  and `config.jsonc` are *inputs* to the pipeline, not outputs. Clearing them
  would delete the user's own transforms and settings. Never touched.

### 5.4 Gating — never silent

Clear-and-rebuild runs **only** when explicitly requested: an explicit flag
(`yolo config migrate --rebuild` / `overlay --reset`) or a one-shot marker file
the operator drops. It must **never** be reachable from an ordinary boot, and it
must print exactly what it will delete and prompt for confirmation (or honor a
`--yes`) before acting. The automatic §3 path is the only thing an ordinary boot
runs; the destructive path is always operator-in-the-loop.

---

## 6. Sequencing with the roadmap (Phase A / B / C)

The plan's build is **serial engine (Phase A) → parallel surface fan-out (Phase B)
→ deletion (Phase C).** This migration threads through as follows:

**Phase A (engine, serial gate) — land the migration primitives *in* the engine
before any surface ports:**

- The **first-migration bootstrap** (§3.2): the `last_render`-absent detection,
  empty-overlay seed, and skip-capture-this-boot logic. This belongs to whoever
  writes the §5 sidecar persistence step — it is *part of* that step, not a later
  add-on. Without it, the very first ported surface (pi) freezes its state.
- The **tombstone-preserving accumulation merge** (§3.4) — a correctness fix to
  the overlay accumulation, required for multi-boot deletions regardless of
  migration.
- A **fixture/regression vector** in the Phase A corpus: a first-boot input where
  the on-disk file carries a stale key the new pipeline no longer emits; assert the
  render **drops it** and the overlay stays empty. This proves the seed path (not
  the naïve diff) is wired, and is the guard against regressing §3.

**Phase B (surface fan-out) — per-surface migration lands with each port.**
✅ **Done for the agent-config surfaces (2026-07-22).**

- **pi** (the proof-of-concept, first): exercises the §3 bootstrap + orphan
  cleanup (§4.3, §4.7) end-to-end. ✅
- **copilot** early: the zero-stale surface, cleanest bootstrap smoke test (§4.6). ✅
- **Claude/gemini/opencode/codex**: each carries its orphan-sidecar cleanup (§4.7)
  and its pre-existing-bug fix (gemini `setDefault`→managed §4.5; codex
  table-preservation §4.4) as part of the port. ✅ (plus **agy**, born on the prism.)
- **mise**: ✅ **Ported (2026-07-22).** `GenerateMiseConfig` is replaced by
  `ConfigureMisePrism` (`internal/entrypoint/prism_mise.go`), composing
  `~/.config/mise/config.toml` through the engine. The §4.1 pre-render scrub is
  **subsumed and gone**: the first-migration seed renders from the (empty) yolo
  layers and discards the on-disk file, so a stale `node`/`python`/`go` line —
  present in no layer — simply does not render (no catalogue needed). Injected
  `YOLO_MISE_TOOLS` pins ride the **computed** layer (above the overlay, so a pin
  beats a stale in-jail `mise use -g`), NOT a `workspace` layer (that engine input
  has no production caller). The render always emits a `[tools]` table, even empty,
  so `last_render` never decodes empty (an empty-decoding sidecar is treated as
  untrusted and would re-seed every boot). The `/workspace/mise.toml` retire
  surgery and the `mise uninstall` subprocess stay bespoke boot side effects (the
  prism never owns `/workspace` files, §5.3).
- **MCP/LSP siblings** (copilot `mcp-config.json`/`lsp-config.json`, agy
  `mcp_config.json`): ✅ **Ported (2026-07-23)** onto the *stateless*
  `renderSurfaceComputed` path (§4.6) — pure per-boot overwrites, no sidecars,
  the live table on the computed layer. `writeCopilotDynamicConfigs` deleted.
- **git identity**: the scoped git-kv codec (§4.2), owned-keyspace reconcile. ⏳
  See `docs/design/identity-prism-decision.md` for the open decision.

**Phase C (deletion, serial, last).** ✅ **Done for the agent-config surfaces
(2026-07-22).**

- Deleted the bespoke agent-config merges (`syncHostSettings`/`syncSettingsLevel`),
  the six `Configure*` writers, the codex TOML dumper, and the now-orphaned helper
  clusters; retired the `YOLO_PRISM_SURFACES` cutover gate so the prism is
  unconditional. The `host_*_files` keys survive (the prism host layer reads
  through them). ✅
- The obsolete snapshot/managed-MCP sidecars are removed on each surface's
  first-migration boot (§4.7). ✅ mise ported (2026-07-22) — the standalone §4.1
  scrub and the `GenerateMiseConfig` in-place editor are deleted; the surgical
  helpers (`miseBaseTools`, `bakedRuntimes`, `workspacePinsTool`, `miseTomlKey`,
  `splitKeepNL`) retired with it. ✅ MCP/LSP siblings ported (2026-07-23) — the
  bespoke `writeCopilotDynamicConfigs` deleted, the siblings rendered via the
  stateless `renderSurfaceComputed` path (§4.6). ⏳ still pending: git identity
  (see `docs/design/identity-prism-decision.md`).

The **clear-and-rebuild** command (§5) can land any time in Phase A/B as an
operator tool; it is not on the critical path — the automatic §3 bootstrap is.

---

## 7. Summary

- **Principle (one line):** yolo owns the bytes it generates — an existing on-disk
  config must converge to what the prism *would* generate from today's layers, not
  carry stale bespoke output forward, and the first-boot overlay must not freeze
  pre-migration bytes as if they were a user edit.
- **The mise fix (one sentence):** in `GenerateMiseConfig`, after the existing
  dedupe/retire pass, strip the `node`/`python`/`go` `[tools]` lines whenever they
  are *not* an intentional workspace pin (absent from `YOLO_MISE_TOOLS` and
  `/workspace/mise.toml`), since those lines can only be stale output from an older
  yolo whose `miseBaseTools` was non-empty — extending the existing `AllMiseRetire`
  strip and guarded to never remove a real workspace override.
- **The first-boot bootstrap (one sentence):** on detecting an absent `last_render`
  sidecar for a surface, run the new pipeline once with an **empty** overlay, write
  that render to both the surface path and `last_render`, initialize the overlay to
  `{}`, and skip the capture step this boot — so the overlay starts genuinely empty,
  stale keys drop, and capture of real in-jail edits begins from a truthful baseline
  next boot.
- **Is clear-and-rebuild needed for any surface?** **No** — the automatic §3
  bootstrap (plus the §4.1 mise scrub and the §4.2 git owned-key reconcile) cleanly
  migrates all eight surfaces without it. Clear-and-rebuild remains only as the
  explicitly-gated, operator-initiated **last resort** the user permitted, for a
  surface where even the fresh render is unwanted — never run silently, and
  forbidden from touching workspace files, host mirrors, agent project/session
  state, or the user's `config.lua`/`config.jsonc`.
