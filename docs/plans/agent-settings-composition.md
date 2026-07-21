# Composing agent settings across host and jail — a better model

**Status:** RFC / idea doc · 2026-07-18
**Audience:** maintainer. Not tied to the Go port, but the port makes it urgent — the
current mechanism has already caused parity bugs (`host_pi_files`), and re-porting a
fragile design entrenches it.

This is a design doc, not a plan of record. It reframes the settings problem, proposes a
model (**Prism**), and — because you asked for a menu — carries a full **idea catalog** of
primitives worth stealing regardless of which model wins (§7).

---

## 1. The reframe: these are all one problem

Pi's linked settings, Claude's "sometimes works" host copy, and your Pi permissions case
feel like three separate annoyances. They are one problem wearing three hats:

> **We have no way to say how a piece of configuration should be *composed* from multiple
> sources and *projected differently* onto the host and into the jail.**

Everything today is a special case of a general operation we never built:

- **"Pi links in other files"** → we can compose only a *single* `settings.json`; there is
  no notion of a config *tree* (extensions, skills, hooks, prompts).
- **"Claude host copy sometimes works"** → the host→jail merge is a bespoke, one-level-deep,
  snapshot-based three-way merge that silently degrades on a transient host typo and clobbers
  jail-local edits below the first level.
- **"Pi needs approval on the host but not in the jail"** → there is **no redaction/transform
  step** anywhere between the host file and the jail file. A host key only stays out of the
  jail if it happens to collide with a hardcoded force-managed key. Your permission gate is a
  *linked extension* plus an `extensions` entry — neither of which the current single-file
  sync can mount or strip.

Name the operation and all three collapse into one engine. That operation is **layered
composition with directional projection**: sources stack in a defined order; each
destination (host, jail) is a *projection* of that stack through a transform that can
redact, override, or reshape.

## 2. What actually exists today (so we fix the real thing)

Verified from the code, per the research pass:

- **Only 2 of 6 agents** reflect host config at all (Claude, Pi). The other four
  (Copilot, Gemini, opencode, Codex) have none — adding it means copy-pasting the fragile
  machinery two more times, in two languages.
- **Host files are single-file `:ro` bind mounts** at `/ctx/host-<agent>/<file>`, gated by
  `host_claude_files` / `host_pi_files` — flat filename lists that *reject any path
  separator* (`config.py:826-841`). So anything in a subdirectory (`~/.pi/agent/extensions/*`,
  `~/.claude/hooks/*`) or a linked secondary file is **inexpressible**. Claude's hook-script
  auto-discovery works around this by mounting scripts *by basename*, which flattens
  directory structure and dangles the `settings.json` path references inside the jail.
- **The merge is a snapshot three-way merge** (`_sync_host_settings`, `agent_configs.py:360`):
  per key, host-only→add, jail-untouched→host wins, jail-edited→jail wins, host-dropped→
  roll back. It is **exactly one dict level deep** (`deep=True` calls `deep=False`, line 408);
  hooks arrays and nested permission objects are compared *atomically*. The snapshot loader
  returns `{}` on *any* error, so **one boot with a host JSON typo or a missing mount looks
  identical to "host removed all keys"** and rolls the jail back.
- **Two contradictory semantics coexist for the same agent:** `settings.json` gets the
  three-way merge (jail edits win); every *other* host Claude file is a blind `copy2` every
  boot (host clobbers jail). 
- **Jail-managed keys are hardcoded per agent, per language,** and applied by *code order*,
  not declaration: Claude force-sets a permissions block (~50 lines), Pi force-sets one key,
  and **Gemini uses `setdefault`** — meaning a user value silently disables the intended YOLO
  default. That inconsistency is a latent security-posture bug.
- **The real YOLO enforcement is the injected `--dangerously-skip-permissions` CLI flag**
  (`agent_registry.py:111`); the settings keys are defence-in-depth. Worth remembering: the
  file is not the only lever.
- **Multi-file agent state** (Pi's `auth.json`/`models.json`/`trust.json`, Claude's
  `.claude.json` + credentials symlink + history isolation) is handled by one-off mechanisms,
  none of which share a model.

The through-line: the config file is a **shared mutable file** — the agent (Claude
especially) rewrites the same `settings.json` we write — which is *why* the three-way merge
exists at all. Fix that and most of the complexity evaporates.

## 3. The core idea: regenerate, don't reconcile

The single deepest simplification, which every design in the exploration converged on:

> **Treat each agent's config file as a build product that yolo regenerates from sources on
> every boot — not a file yolo edits in place.**

Once the file is *rebuilt* from an ordered stack of sources, host-key removal needs **zero
memory**: if the host drops a key, it is simply absent from the next render. That deletes the
snapshot/rollback machinery and its poison-on-typo failure mode outright. The only thing we
must still remember is *what the agent itself changed at runtime* — and that is a small,
explicit overlay (§5), not an inferred diff against a full-file snapshot.

## 4. Proposed model: Prism (layers in, per-side projections out)

**Per agent, a data-only manifest** (embedded identically in the Python and Go builds)
declares one or more **surfaces**. A surface is either:

- a **structured file** the agent reads (`settings.json`, `config.toml`) — parsed, merged,
  rendered; or
- a **tree** (`extensions/`, `skills/`, `hooks/`) — staged with relative paths preserved and
  include/exclude globs.

Structured surfaces are composed from a fixed five-layer stack and materialized per
destination by a **projection**.

### The five layers (lowest to highest precedence)

| Layer | Source | Purpose |
|---|---|---|
| `defaults` | manifest data | yolo builtin, user-overridable (the honest home for Gemini's `setdefault` tier) |
| `host` | staged host files, parsed fresh each boot | the user's host config |
| `workspace` | merged yolo config → `agent_config.<agent>.settings` (user-level `config.jsonc` merged under `yolo-jail.jsonc`, standard rules) | jail-only config the user declares — per-workspace, or host-wide when set at user level |
| `runtime` | harvested overlay sidecar | what the agent changed at runtime |
| `managed` | manifest data | yolo's security-boundary keys — always win |

### Merge

**One recursive deep merge** (RFC 7386 semantics: objects merge at every depth, `null`
deletes, arrays replace) as the default. The manifest may pin a per-keypath strategy:
`deep | replace | append` (append with dedupe). No depth cliff, no atomic-list surprise —
the strategy is *readable from the manifest*, and the same ~40-line function runs at every
level. This directly replaces the two-level merge that already caused Go parity bugs.

### Projections (this is where the strip problem dies)

A **projection** is the recipe that turns the layer stack into one side's actual config
file. It names a **destination** (the concrete file that gets written — for the jail, the
rendered `settings.json` in jail home), the **layers** that fold into it, a **filter list**
applied after the fold, and the **enforce set** (the managed keys re-asserted last, as the
second belt from §4). Its *result* is a plain JSON document, materialized at the destination
on every boot/attach. Each structured surface has exactly two:

- **Host projection:** the *identity* over the `host` layer — it has **no write step**.
  yolo never writes host files; managing host dotfiles is another tool's job and permanently
  out of scope here. The host `settings.json` *is* the host layer, and host Pi keeps reading
  it natively. Nothing on the host changes — calling this degenerate case a "projection"
  just keeps one mental model; read it as "the host file, unchanged."
- **Jail projection:** folds all five layers, then applies its **filter list**. Filters are
  *not* a language and there is no interpreter: a filter is one of three fixed, declarative
  ops written as plain JSON data — in yolo's builtin per-agent manifest, or (optionally) by
  the user in `yolo-jail.jsonc` under `agent_config.<agent>.jail_filters` (§6) — and applied
  by the single `applyFilters` engine function:
  - `{op: "drop", path}` — remove a keypath (JSON Pointer, `*` wildcard segments)
  - `{op: "dropItems", path, match}` — remove glob-matching elements from an array
  - `{op: "set", path, value}` — per-side transform

That is the whole redaction primitive. Deliberately **not** RFC 6902 JSON-patch — three ops
cover every observed need and port trivially.

**Directionality is just where a value lives in the model:** host-only = present in the host
source, filtered out of the jail projection. Jail-only = the `workspace` layer.
Transformed-per-side = a `set` filter. There is no separate mechanism to build.

**Correction adopted from the review:** filters apply **host-side, at staging time**, by
default — not only at in-jail render. Otherwise the redacted value sits readable in the `:ro`
mount at `/ctx/host/<agent>/` *inside the container*. The CLI already links the engine for
staging, so redacted data never enters the container; we **also** re-apply the filter on the
final in-jail render as a second belt against the runtime overlay or the agent resurrecting a
key. Costs nothing, closes a real confidentiality gap for future secret-shaped keys.

### The engine is five pure functions

`deepMerge(a, b, strategies)` · `mergeDiff(old, new)` · `applyFilters(doc, filters)` ·
`render(layers)` · `syncTree(src, dst, policy)`. Each has a canonical Go counterpart. The
whole subsystem becomes one small library over generic JSON values, driven by data manifests
— instead of ~50 lines of order-dependent force-sets per agent per language.

## 5. Runtime edits, scope, and what lives in jail home

The build-product model raises three questions that turn out to be one question: (1) if I
change a Claude setting *inside* the jail (via `/settings`), how does it survive the next
regeneration? (2) what is shared across jails vs per-jail? (3) what actually lands in jail
home? The unifying idea:

> **Every piece of config has two coordinates: a *layer* (precedence — §4) and a *scope*
> (where it is stored, and who shares it).** Get the scope model right and the rest follows.

### 5.1 The second coordinate: scope

| Layer | Scope | Stored where | Shared by |
|---|---|---|---|
| `defaults` | global | manifest data (code / image) | every jail |
| `managed` | global | manifest data (code / image) | every jail |
| `host` | per-host | host filesystem (`~/.claude`, `~/.pi/agent`), `:ro` mounted | every jail on this host |
| `workspace` | per-workspace | `yolo-jail.jsonc` → `agent_config.<agent>` (git-committable) | that workspace (travels with the repo) |
| `runtime` overlay | per-workspace identity | sidecar keyed on `sha256(host workspace dir)` | that workspace's jails, across restarts |
| **render output** | per-boot *content* | the agent's actual file in jail home (see below for where that physically lives) | authoritative for nobody — rebuilt from sources each boot |

A physical clarification, because "jail home" is not one thing: `/home/agent` is the **shared
host-backed home** (`~/.local/share/yolo-jail/home`, bind-mounted into every jail), with
**per-workspace overlay mounts** on top for specific files (e.g. Claude's `settings.json`
comes from `<workspace>/.yolo/home/claude-settings.json`). "Per-boot" in the table describes
the *content lifecycle* — the render is rebuilt from sources on every boot, so nothing may
treat the file as a source of truth — not physical isolation. That forces one implementation
requirement: **a rendered file must land on a per-workspace overlay path** under
`<workspace>/.yolo/home/…` (mounted into the jail's user home) — otherwise two workspaces'
renders would fight in the shared home. yolo renders only into these **user-scope** overlays,
never into a workspace-project file like `$CWD/.claude/settings.json` (§5.2's ownership rule).
Claude's user `settings.json` already satisfies this; Pi's `~/.pi/agent/settings.json` needs a
per-workspace overlay mount added, same as Claude's.

That yields a clean, VS-Code-shaped contract the user can reason about without knowing the
internals:

- **Edit the host file → global.** Every jail picks it up on its next boot (they all mount
  the same host source).
- **Edit `~/.config/yolo-jail/config.jsonc` → host-wide default for the `workspace` layer.**
  Standard config merge; a workspace's `yolo-jail.jsonc` overrides it.
- **Edit `yolo-jail.jsonc` → per-workspace.** Committable; travels with the repo to teammates.
- **Edit inside the jail (`/settings`) → that workspace's jail only.** Persisted
  per-workspace (via the agent's own user-scope file for Claude, or the overlay sidecar for
  single-scope agents — §5.2); it does not leak to other workspaces or back to the host.

### 5.2 Surviving regeneration — the mechanism

The build-product model only works if a live jail edit survives the rebuild. The enabling
trick: **yolo always knows exactly what *it* wrote last time, so anything different on disk
is definitionally your edit.**

**The scope yolo owns is fixed by the ownership rule, not chosen per agent.** yolo composes
config into the jail **user scope** only — `~/.claude/settings.json`, `~/.pi/agent/settings.json`,
`~/.gemini/settings.json`, `config.toml`, … under `/home/agent` (a per-workspace r/w overlay).
It does **not** write any agent's *project* / workspace-level config: the workspace tree
(`/workspace`) is owned by the operating agent and mirrors the host, except the narrow
"internal details" shadow mounts yolo owns for isolation (`.vscode/mcp.json`, `.overmind.sock`
→ `/dev/null`). See the **config-ownership principle** in
[../design/storage-and-config.md](../design/storage-and-config.md).

That single constraint collapses what earlier drafts split into a per-agent decision:

- Because yolo may only write the **user** scope, and every agent that persists in-jail edits
  (`/config`, `/settings`, permission approvals) writes them into **that same user scope**,
  yolo's regenerated config and the user's live edits **always share one file**. There is no
  agent for which yolo owns a separate, uncontended file — so the capture-diff overlay
  (mechanism **A**) is the **universal mechanism**, for Claude exactly as for pi/opencode/Codex.
- The one genuine exception is the **managed** scope (`/etc/claude-code/managed-settings.json`
  and analogs) — it lives *outside* both the user home and the workspace, so yolo can own it
  outright for security-boundary keys with no contention. That's mechanism **C**'s home.
- An agent that writes *none* of its config at runtime (opencode, verified) is a trivial case
  of A: the overlay is always empty, so yolo just regenerates the user file freely. No special
  path needed — same mechanism, degenerate input.

> **Rejected: "native scope split" (own the project scope).** An earlier draft proposed yolo
> own Claude's *project* file (`.claude/settings.json`) — read-only to Claude, so precedence
> would compose it with no overlay. It's rejected because **project scope is a workspace file**
> (`$CWD/.claude/`), and yolo does not write the workspace. Owning it would violate the
> ownership rule for a mechanism that A already covers. Claude is therefore **not special**: it
> uses the user-scope overlay like every other agent, and its own `/config` writes (also user
> scope) are captured by the same diff.

The two mechanisms that survive are described below — **A** (the universal user-scope overlay)
and **C** (managed keys, the security-boundary exception):

**A. Runtime overlay (capture-diff) — the universal mechanism.** Keep two sidecars the agent
never sees: `last_render` (the exact bytes yolo wrote last boot) and `overlay` (accumulated
jail edits). Each boot/attach:
```
delta   = mergeDiff(last_render, current_file)     # current = last_render + your /settings edits
overlay = deepMerge(overlay, delta)                # accumulate (deletes recorded as null tombstones)
render  = layers(defaults, host, workspace, overlay, managed)   # overlay is a LAYER, below managed
write(render); last_render = render
```
So your `/settings` change is captured on the next regeneration and re-applied on every one
after. Three details make or break it: **precedence** (overlay sits above host/workspace so your
edit wins — but an entry auto-retires when the host value converges to yours, plus a
`yolo config overlay --reset <agent>` escape hatch); **deletions** (recorded as `null`
tombstones so the rebuild does not resurrect a key you removed — the exact bug today's merge
has); and **managed keys** (the `managed` layer sits *above* the overlay, so an attempt to
change a security-boundary key via `/settings` is captured but overridden on render — it
visibly reverts, which is correct). This replaces today's fragile per-agent three-way
snapshot with one diff-against-our-own-output, and jail edits now win at *full depth*.

> **Where this lives on disk (the substrate).** Yes — the settings file the agent reads/writes
> (`.claude/settings.json`, `config.toml`, …) sits in the **per-workspace r/w overlay** that
> backs `/home/agent`: `<workspace>/.yolo/home/…` mounted writable over the read-only global
> home (see `docs/design/storage-and-config.md` and `jail-home.md`). So its contents *do*
> survive to the next boot as leftover-on-disk — that's the persistence substrate every
> mechanism here relies on. But mechanism A does **not** just "reuse whatever's on disk": the
> agent-visible file is a *build product* yolo overwrites each boot. What persists across the
> overwrite are the two **sidecars** (`last_render`, `overlay`) — also in `.yolo/home/` but
> outside the agent's view — which yolo reads to re-derive the render. The distinction matters
> because "reuse the file as-is" would let stale host/default values ride along and never let a
> changed host value propagate; capture-diff keeps the file a clean function of
> (defaults, host, workspace, your captured edits, managed). And yes — every file composed this
> way (rendered products *and* the sidecars) is part of that per-workspace overlaid r/w set;
> the read-only global home holds none of it.

**B. Native scope split — REJECTED (violates the config-ownership rule).** An earlier draft
proposed dodging the overlay by having yolo own Claude's *project* file (`.claude/settings.json`)
and letting native precedence compose it with the user file. This is rejected: **the project
scope is a workspace file** (`$CWD/.claude/`), and yolo does not write the workspace (see 5.2's
ownership constraint + [../design/storage-and-config.md](../design/storage-and-config.md)).
Owning it to save an overlay would trade the ownership boundary for a mechanism **A** already
provides at the user scope.

The verified Claude facts that drove the draft still hold and still matter (for the managed
exception and for understanding the contention): precedence is
`managed > CLI args > .claude/settings.local.json (local) > .claude/settings.json (project) >
~/.claude/settings.json (user)`; **Claude mutates the *user* file at runtime** (`/config` writes
`model`/`theme`/`effortLevel`/`fastMode`; permission approvals go to *local*) but **never writes
the *project* file** — it only reads it. The consequence under our ownership rule: yolo writes
the **user** scope (the only scope it owns), which is *also* where Claude's `/config` lands — so
the two share one file and need the capture-diff overlay (A). Claude's read-only project scope
is real, but it's off-limits to yolo because it's a workspace file. Security keys still go to the
**managed** scope (mechanism C), which is neither user nor workspace.

**C. Key-ownership (surgical merge) — the minimal version.** yolo declares an *ownership set*
(the keypaths it manages — permissions, MCP, host-projected keys) and writes only those,
leaving every other key in the file untouched. Your `/settings` addition of an unowned key
just survives. Closest to today's code, but replaces the fuzzy three-way merge with an
explicit "these keypaths are mine, everything else is yours" contract. Trade-off: you lose
the clean build-product property, and removing an *owned* host key still needs tombstone
memory.

**Recommendation:** **A (user-scope capture-diff overlay) for every agent** — because yolo owns
only the user scope, which every agent also writes, there is no agent for which a cleaner
single-file split is available. **C (managed scope)** carries the security-boundary keys, the one
place yolo owns outright. B is rejected (above). C is not a per-agent choice but the
key-ownership contract layered on top of A. The per-file classification is in §5.3.

### 5.3 What lands in jail home — classify every file

Jail home (`/home/agent`) is a *mix* of regenerated and persisted files. The model classifies
each so regeneration knows what it may overwrite. **Regeneration only ever writes the
`rendered` and `reflected` classes; everything else is untouched — that is what makes "rebuild
every boot" safe.**

- **rendered** — build products, rebuilt every boot, safe to blow away: `settings.json`,
  `~/.claude.json`, `config.toml`.
- **reflected (tree)** — staged from a host tree, rebuilt: `extensions/`, `hooks/`, `skills/`.
- **runtime-state** — persisted, **never** regenerated, sometimes shared: credentials,
  `auth.json`, `trust.json`, history, sessions.
- **overlay sidecar** — yolo's private memory (`last_render` + `overlay`), hidden from the
  agent, per-workspace.

Concrete Claude layout (reflecting the verified scope facts — see §5.2 B):
```
/home/agent/
  .claude/
    settings.json          [runtime-state] ← USER scope; Claude mutates it (/config model/theme/
                                             effort). yolo seeds once, then never touches; persisted
                                             per-workspace so in-jail edits survive reboots.
    .credentials.json       [runtime-state] → symlink to shared creds (GLOBAL, one login)
    history.jsonl           [runtime-state] → per-workspace symlink (isolated by sha256(host dir))
  ~/.claude.json           [runtime-state] ← OAuth tokens, MCP configs, per-project trust, caches;
                                             Claude owns it. yolo's MCP entries could instead go to
                                             project-scope .mcp.json to stop sharing this file.
  <workspace>/.claude/
    settings.json          [rendered]      ← PROJECT scope; Claude only READS it → yolo owns +
                                             rebuilds freely (host-projected + asserted keys).
    settings.local.json    [runtime-state] ← permission approvals; left alone (jail = boundary).
  /etc/claude-code/
    managed-settings.json  [rendered]      ← MANAGED scope; security/YOLO keys, user-uncoverride-able.
                                             (write needs root at boot — §11.)
  .pi/agent/
    settings.json          [rendered]      ← pi has no stable scope split → use overlay (idea A)
    extensions/  skills/    [reflected]     ← staged from host tree, paths preserved
    auth.json  trust.json   [runtime-state]
  .yolo/render/<agent>/
    last_render.json  overlay.json   [overlay sidecar]   ← yolo memory (idea A agents), per-workspace
```
Note the payoff: for Claude, **no file is contested** — every file is owned by exactly one
writer (Claude *or* yolo), so the overlay sidecar is only needed for single-scope agents like
pi. For pi, `~/.pi/agent/settings.json` *is* contested (yolo renders it, pi may write it), so
it takes the idea-A overlay.

### 5.4 The composition tree (sources cascade; jail home is the leaf)

"Composition works like a tree" in two senses that both hold: the config *value* is a tree
(nested JSON, deep-merged), and the config *sources* cascade from broad scope to narrow, with
jail home as the rendered leaf:

```
GLOBAL (every jail)          defaults + managed  (code)         ┐
PER-HOST (every jail here)   host source ~/.claude, ~/.pi/agent ┤  deep-merge by precedence
PER-WORKSPACE                workspace (yolo-jail.jsonc)        ┤  → project(host | jail, filters)
PER-WORKSPACE                runtime overlay                    ┘  → RENDER
                                                                     ↓
PERSISTED (mounted/symlinked, NOT part of the render):               jail home  [rendered + reflected]
  shared creds (GLOBAL) · per-workspace history/trust ─────────────► jail home  [runtime-state]
```

So the sharing story is explicit and sensible: **host + defaults + managed + shared
credentials are one truth across all jails; `workspace` + overlay + history are per-workspace;
the rendered files are per-boot.** Nothing per-jail is invented that a scope does not already
explain.

### 5.5 Does it hold together? (reconciliation with what exists)

This is not a from-scratch machine — it *names* mechanisms yolo already has, so most of it is
re-framing, not new code:

- **Shared credentials**, the **history isolation** keyed on `sha256(YOLO_HOST_DIR)`, and the
  **ws_state overlays** are already scope+class instances (runtime-state at global and
  per-workspace scope). The model gives them one vocabulary instead of three ad-hoc plumbings.
- Because regeneration only touches `rendered`/`reflected`, the failure cases degrade
  gracefully: a corrupt render is rebuilt next boot; a lost overlay costs you your accumulated
  jail edits (recoverable via `--reset`) but never touches credentials, history, or the host.
- The one genuinely new store is the **overlay sidecar** (per-workspace, hidden). Everything
  else maps onto an existing mechanism — which is the point: this is a smaller, more legible
  surface than the status quo, not a bigger one.

## 6. The Pi case, end to end

**Host reality (yolo never touches it):**
```jsonc
// ~/.pi/agent/settings.json
{
  "theme": "dark",
  "defaultModel": "claude-fable-5",
  "extensions": ["extensions/permission-gate.ts", "extensions/git-helper.ts"],
  "skills": ["skills/**"]
}
// ~/.pi/agent/extensions/permission-gate.ts  — on('tool_call') + ui.confirm; WANTED on host
```

**What the user writes** — shown here in the workspace file, but the same
`agent_config` key works in the user-level `~/.config/yolo-jail/config.jsonc`
to apply to every workspace on the host (the standard config merge: workspace
over user):
```jsonc
// /workspace/yolo-jail.jsonc
"agent_config": {
  "pi": {
    "host_exclude": ["extensions/permission-gate.ts"],
    "jail_filters": [
      { "op": "dropItems", "path": "/extensions", "match": "*permission-gate*" }
    ]
  }
}
```
The builtin Pi manifest already declares the settings surface, the tree surface
(`extensions/**`, `skills/**`, … with `auth.json`/`trust.json` excluded), and the managed
layer (`defaultProjectTrust: "always"`). The user adds *one glob and one filter*.

**How the manifest decides what crosses — include-first, not a blacklist.** Staging carries
*only* what a declared surface or include glob names; a host file the manifest doesn't
mention never enters the jail. So when an agent grows a new config file, the default is
**fail-closed** (not staged) until the manifest — or the user, via `host_include` — says
otherwise. The excludes are not a blocklist doing the safety work: they exist solely to
carve `runtime-state` files (`auth.json`, `trust.json` — the file classes of §5.3) out of a
tree that an include glob would otherwise sweep in. The per-agent curation itself is
irreducible — *something* must know that Pi keeps extensions in `extensions/` and secrets
in `auth.json` — but today that knowledge lives in ~50 lines of imperative per-agent code
in two languages; the manifest moves it into one declarative data blob exercised by the
shared fixture corpus (idea 9), which is what makes it *less* fragile, not more.

**Result:**
- **Host:** unchanged. Host Pi still loads the gate extension; every host command still
  requires approval.
- **Jail:** the `extensions` array is rendered without the gate entry, and the extension file
  itself is never staged. No approval prompts in the jail (the container is the security
  boundary). No sanitized second host file, no manual stripping.

That is the entire feature. It generalizes: any host key you want kept out of any jail is one
filter line.

## 6.1 Aside: the filter config is too clever — should this be Lua?

**The complaint (valid).** Look at what the Pi user had to write:

```jsonc
"agent_config": {
  "pi": {
    "host_exclude": ["extensions/permission-gate.ts"],
    "jail_filters": [
      { "op": "dropItems", "path": "/extensions", "match": "*permission-gate*" }
    ]
  }
}
```

You cannot *intuit* this. Three problems:

1. **`host_exclude` is unguessable.** Exclude what, from where? (Answer: don't
   *copy this file* into the jail during tree staging.) The name encodes neither
   the noun it operates on nor the verb.
2. **It's two operations for one intent.** "The permission gate shouldn't exist
   in the jail" requires *both* dropping the file (`host_exclude`) *and* editing
   the array that references it (`jail_filters`). Miss one and you get a dangling
   reference or a staged-but-unreferenced file. The model makes you say the same
   thing twice, in two vocabularies.
3. **`jail_filters` is a transformation language badly encoded as data.**
   `{op, path, match}` is a verb + a JSON-Pointer + a glob — a tiny interpreter
   whose ops (`drop`/`dropItems`/`set`) you must memorize, whose paths fail
   silently when mistyped, and which can only ever do what the closed vocabulary
   already supports. And an unknown key in this free-form blob is silently
   ignored — the **exact `host_pi_files` divergence** §7 lists as a real bug.

### What it looks like as Lua

```lua
-- yolo.lua  (pointed at by a `lua_config` key; one function per agent)
-- ctx.settings : the merged host settings table (defaults < host < workspace)
-- ctx.stage    : the file-staging handle (what gets copied into the jail)
-- ctx.managed  : keys the jail enforces regardless (the security boundary)

yolo.agent("pi", function(ctx)
  -- The permission gate is a host-only safety net; in the jail the container
  -- IS the boundary, so neither the extension entry nor its file should cross.
  ctx.settings.extensions = yolo.without(ctx.settings.extensions, "*permission-gate*")
  ctx.stage.exclude("extensions/permission-gate.ts")
end)
```

The intent is a comment; the two edits are two named calls. `dropItems` +
JSON-Pointer + glob collapses to `yolo.without(list, glob)`; `host_exclude`
becomes `ctx.stage.exclude(...)`. A typo'd function or field is a **loud runtime
error**, not a silently-ignored key. Arbitrary redaction needs no new `op` in
yolo's vocabulary — you just write the transform.

### What Lua wins vs. what it costs

| | Data model (`jail_filters`) | Lua transform |
|---|---|---|
| Legibility of complex edits | poor (pointer+verb+glob) | **good (reads as code)** |
| New transform without patching yolo | needs a new `op` | **just write it** |
| Typo failure mode | **silent** (unknown key ignored) | loud (runtime error) |
| `yolo config explain` (idea 10) | **free** — render is a fold over declared filters | must trace code / diff outputs |
| Config-change safety diff (a shipped feature) | **strong** — diff two data blobs, "added package X" | weak on *input* (text diff of a script); must diff the *rendered output* instead |
| Golden-fixture parity (idea 9) | **strong** — same vectors, any impl | Go-only VM (fine post-wipe; see below) |
| New surface area | none (all JSONC) | a second language + a sandboxed interpreter |
| Footguns | bounded (closed vocab) | infinite loops, `os.execute`, unbounded |

Two nuances that move the needle:

- **The parity objection is weaker than it was.** Idea 9's "run byte-identically
  by `pytest` *and* `go test`" mattered during the Python→Go transition. Post-wipe
  it's Go-only, so an embedded pure-Go Lua VM (e.g. `gopher-lua`, no cgo) is the
  *only* interpreter needed — the cross-language constraint is gone. Fixtures
  become "Lua + input → output," still fully testable.
- **The config-safety objection is real and the strongest argument for data.**
  The shipped config-change confirmation shows a *normalized diff* and asks y/N
  (that's why an agent can't silently add packages). You can diff two JSON blobs;
  you cannot meaningfully diff "what this Lua function *does*" without running it.
  The mitigation — run the transform and diff the *rendered output* — is arguably
  better, but it's a bigger change and it means the safety prompt can no longer
  fire *before* executing user code.

### Recommendation — narrow the blast radius, don't Lua-ify everything

The confusion is concentrated in **one** of the five `agent_config` keys:
`jail_filters`. The other four are genuinely declarative data and should stay
data — `host_include`/`host_exclude` are globs (rename them: see below),
`settings` is a merge patch, `overrides`/managed is enforced keys. Turning those
into code buys nothing and loses `explain` + the safety diff.

So, two options, smallest first:

1. **Fix the data model (recommended first pass).** (a) **Unify the two
   concerns** — one "keep this out of the jail" primitive that derives *both* the
   array edit and the file exclusion from a single declaration, so the Pi case is
   one line, not a file-exclude plus a filter. (b) **Rename for intuition** —
   `host_exclude` → `dont_stage` (or `jail_omit`), `jail_filters` →
   `jail_redactions`. (c) **Validate keys** — reject unknown `agent_config` keys
   loudly, killing the `host_pi_files` silent-drop class. This keeps `explain`,
   the safety diff, and fixtures intact and removes ~80% of the confusion.
2. **Add Lua as an optional escape hatch for the last 20%** — an optional
   per-agent `transform` hook (the mockup above) for redactions the closed
   vocabulary can't express, while the declarative keys handle the common case.
   Only worth it if real cases exceed `drop`/`dropItems`/`set`; the doc has not
   yet found one. Gate it behind an explicit opt-in, sandbox the VM (no
   `os`/`io`), and diff the rendered output for the safety prompt.

**Verdict:** the instinct is right that `jail_filters` is too clever, but the fix
is to *simplify and rename the data* first (option 1), and reach for Lua only if
a genuine transform appears that the vocabulary can't name (option 2). Jumping
straight to a Turing-complete config trades a memorization problem for an
`explain`/safety-diff regression and a whole new language in the surface.

## 6.2 `yolo config render` — run the config engine WITHOUT a jail

**Requirement (load-bearing).** Whatever we land on — the overlay capture-diff
(§5.2 A), the data filters (§6.1 option 1), or a Lua transform hook (§6.1 option
2) — the render is *executed*, not static. So there **must** be a command that
runs the whole engine offline, prints what it would produce, and never touches a
container. Without it, the only way to see what your config does is to boot a
jail and poke at `/home/agent` — a 30-second-plus loop that an agent iterating on
a config can't use, and that can't run in a unit test or a `--dry-run` check.
This is the developer-facing counterpart to the internal engine (§4's five pure
functions) and it's *cheap*, because that engine is already pure and jail-free by
construction — the render is a fold over layers on the host side.

### Surface

```bash
# Render every managed surface for one agent, to stdout — no container, no writes.
yolo config render <agent> [--workspace DIR]

# Just one surface / one file.
yolo config render claude --surface settings        # → the settings.json it WOULD write
yolo config render pi     --surface tree             # → the staged file list + contents

# Show the layer stack and who won each key (idea 10's `explain`, same engine).
yolo config render claude --explain [KEYPATH]
#   permissions.allow      ← managed (forced)
#   model                  ← runtime overlay (your /settings edit, 2026-07-19)
#   theme                  ← host (~/.claude/settings.json)
#   extensions             ← workspace, then filter dropItems '*permission-gate*' removed 1

# Feed it hypotheticals without editing your real files — the fixture path.
yolo config render pi --host FILE --workspace FILE --overlay FILE --format json
```

Contract:

- **Read-only, jail-free, side-effect-free.** It runs staging + merge + filters +
  render **in a temp dir** (or purely in memory), prints, and exits. It never
  starts a runtime, never writes `~/.claude`, never mutates the overlay. Safe to
  run in a loop, in CI, in a pre-commit hook.
- **Same engine as the real boot.** It calls the exact `render(layers)` /
  `applyFilters` / `syncTree` the entrypoint calls — not a reimplementation — so
  "what render prints" *is* "what the jail gets." (This is why the engine being
  pure, §4, matters: the offline runner is nearly free.)
- **Injectable inputs** for hypotheticals (`--host`/`--workspace`/`--overlay`
  file overrides) so you can test a config change against fixed inputs without
  editing your real host files. These same inputs are the golden-fixture vectors
  (idea 9) — the debug command and the test harness are the *same code path*.
- **`--explain`** surfaces the provenance sidecar (idea 10): which layer or
  filter won each leaf, including *negative* provenance (host keys a filter
  dropped). This is the "why did my host key vanish in the jail?" answer.

### Why this pays for itself

- **Config iteration without a jail.** An agent (or a human) editing
  `agent_config` runs `yolo config render <agent> --explain`, sees the effect
  instantly, adjusts, repeats — no container churn.
- **It's the Lua safety story (§6.1).** The strongest argument *against* Lua was
  that you can't diff "what a script does" for the config-change safety prompt.
  `render` resolves that: diff the **output** of `render` before vs. after — that
  works identically whether the transform is data or Lua, and it's what the
  change-safety prompt should show.
- **It's the test harness.** `yolo config render --format json` over the fixture
  vectors *is* idea 9's corpus runner. One command backs dev, CI, and `--explain`.
- **`yolo check` integration.** `check` can call the same render in-process to
  validate a config produces a well-formed result before you ever launch.

Naming note: `render` (produces the artifact) reads better than `explain`
(idea 10 is then the `--explain` *flag* on it) or `dry-run` (overloaded with the
run-path `--dry-run`). Pick one verb and make `--explain`/`--format` modes of it.

## 7. Idea catalog — primitives worth stealing regardless of the model

Even if we don't adopt Prism wholesale, these stand on their own (ranked by leverage):

1. **Regenerate, don't reconcile** (§3). The one change that deletes the snapshot machinery
   and its poison-on-typo failure. Highest leverage in the doc.
2. **Staged-*directory* `:ro` mount, regenerated every `yolo` invocation, truncate-in-place**
   (the proven briefing model). Kills three verified bugs at once: the frozen inode on
   atomic-rename host edits, the file *set* frozen at container creation, and single-file
   mount brittleness.
3. **Relative-path-preserving tree staging with include/exclude globs.** Makes Pi's
   resolve-relative-to-`~/.pi/agent` linking and Claude's hook-script references work in-jail;
   deletes the filename-only validation.
4. **RFC 7386 recursive merge as the one default** (objects merge at all depths, `null`
   deletes) — replaces the two-level cliff; ~40 LOC per language with a canonical Go lib.
5. **Managed/security-boundary keys as embedded data**, applied as the top layer — replaces
   the order-dependent per-agent force-sets and structurally resolves the Gemini
   `setdefault`-vs-`force` ambiguity (you *declare* whether a default is user-overridable).
6. **Native-layer offload** (graft from the conf.d design): write Claude's forced permissions/
   YOLO block to the **managed** scope (`/etc/claude-code/managed-settings.json` on Linux,
   `/Library/Application Support/ClaudeCode/` on macOS) instead of into a file Claude also
   writes. Verified strongest: managed is the top of Claude's precedence and *cannot* be
   overridden by user/project — a live runtime rewrite by Claude cannot drop the managed keys
   mid-session. **Independent of the whole engine**; shippable now. Caveat: the managed file
   needs root to write — if the entrypoint can't write `/etc` at boot, put the security keys in
   the **project** file instead (still outranks the user file; §11).
7. **Tiny closed filter vocabulary as data** (`drop` / `dropItems` / `set`, applied
   host-side). The minimal redaction primitive, user-declarable without patching yolo in two
   languages. **But see §6.1** — this is the one primitive that reads as too-clever; the
   recommendation is to unify+rename it (and validate keys) before adding any code-based
   transform.
8. **Fail-closed on unparseable input** (graft from the patch-pipeline design): keep the last
   good staging (host side) or last render (jail side) with a loud warning — instead of the
   current mass-rollback on a transient typo.
9. **Shared golden-fixture corpus** (`layers/filters in → render/overlay/provenance out`) run
   byte-identically by `pytest` *and* `go test`. This **inverts the Go-parity model**: instead
   of byte-matching bespoke Python with `OrderedMap`/`pyEqual` scaffolding, both languages
   implement the same spec against the same vectors. It is the direct antidote to the
   `host_pi_files` / untested-Go-pi-merge drift class.
10. **Provenance sidecar → `yolo config render <agent> --explain [keypath]`.** Free once render
    is a fold over named layers. Answers "why is this key set?" and "where did my host key go?"
    (*dropped by filter X*) — turning today's archaeology into a command. This is the
    `--explain` mode of the offline **`yolo config render`** command (§6.2), the jail-free
    runner for whatever execution model we adopt — the same code path backs dev iteration, CI
    fixtures (idea 9), and the config-change safety diff.
11. **Per-agent file manifest** classifying every file as `rendered | reflected(mirror|seed) |
    runtime-state`. Gives one nameable home to today's ad-hoc mechanisms (credentials symlink,
    `auth.json` hands-off, blind `copy2` mirrors, seed-once configs).
12. **One uniform `agent_config.<agent>` config key**
    (`host_include`/`host_exclude`/`jail_filters`/`settings`/`overrides`) replacing the
    per-agent `host_*_files` proliferation that produced the `host_pi_files` unknown-key
    divergence — and gives all six agents host reflection for free.
13. **MCP servers via the layer model:** managed layer holds the yolo set, user-added servers
    land in the runtime overlay, wholesale re-render replaces the *four* divergent managed-MCP
    sidecar reconciles (a 4× parity surface).
14. **Real TOML emitter with a per-surface fidelity flag** — fixes the Codex table-dropping
    bug as a *bug* rather than porting it bug-for-bug.
15. **`.host` / `.jail` filename-suffix fragments** as *optional* source sugar for users who
    like organizing config as fragments. Never required, never yolo-written.

## 8. Vocabulary (adopt this in config-ref and code)

- **surface** — one config destination yolo manages: a structured file or a file tree.
- **layer** — one named, ordered source for a structured surface (the *precedence* coordinate):
  `defaults < host < workspace < runtime < managed`. (The workspace layer was previously
  drafted as "user" — renamed because it is per-workspace config, and "user" collides with
  Claude's own user-scope terminology in §5.2.)
- **scope** — the *storage* coordinate: where a layer/file lives and who shares it — `global`
  (all jails), `per-host` (host source, all jails here), `per-workspace` (`yolo-jail.jsonc` +
  overlay + history), `per-boot` (the ephemeral render). Every piece of config = one layer ×
  one scope (§5.1).
- **projection** — the materialization of the stack for one side: `(destination, layers,
  filters, enforce)`. Host = identity over `host`; jail = all five + filters.
- **staging** — the host-CLI step each `yolo` invocation: include-first glob-filtered copy of
  an agent's host files into `ws_state` (only declared includes cross; excludes carve out
  runtime-state), relative paths preserved, truncate-in-place, bind-mounted `:ro`.
- **filter / redaction** — a data-declared transform when config crosses host→jail: `drop` /
  `dropItems` / `set`. Default boundary is host-side.
- **merge strategy** — how two layers combine at a keypath: `deep` (default) / `replace` /
  `append`.
- **runtime overlay** — the captured record of what the agent changed in its own file.
- **managed / jail-managed** — the security-boundary keys yolo forces in the jail; where an
  agent has a native override slot, they project *there* (see native-layer offload).
- **provenance** — per-leaf record of which layer (or filter) won; exposed via `yolo config
  explain`. Includes *negative* provenance: host keys deliberately dropped.
- **file class** — `rendered | reflected(mirror|seed) | runtime-state`.
- **manifest** — the per-agent data-only declaration (identical in Python and Go builds).

## 9. Why Prism over the alternatives (and what got grafted in)

Four models were designed independently and judged. Scores: **Prism 8.5**, patch-pipeline
7.8, conf.d fragments 7.0, capability planes 6.8.

- **Prism wins** because it solves the strip problem with *zero host-side reorganization*
  (you keep one host `settings.json` that host Pi reads natively) while keeping the smallest
  vocabulary that still covers all seven sub-problems. Named layers with fixed precedence are
  the industry-consensus shape (Nix priorities, VS Code scopes, Helm coalesce) — the easiest
  thing to explain and to keep byte-identical across two languages.
- **conf.d fragments** — strong on robustness, but requires migrating host config into
  fragments *and* makes yolo write host dotfiles (a contract change). **Grafted:** the
  native-layer offload (idea 6) and fail-closed (idea 8).
- **patch-pipeline** (kustomize/JSON-patch) — nearly isomorphic to Prism (its patch segments
  *are* layers) but with a larger op surface and a less legible "ordered patch program" mental
  model. **Grafted:** fail-closed, and `mergeKeyed` held in reserve for Claude hook matcher
  lists if a concrete need appears (do not ship in v1).
- **capability planes** (model permissions/MCP/hooks/prefs as typed capabilities) — solves the
  Pi case most elegantly (one line) but pays with claim tables, extractors, and a classifier
  you debug instead of reading a layer stack. **Grafted:** host-side error surfacing (validate
  host files at `yolo` invocation, where a human is present, not in entrypoint logs) and the
  file-class vocabulary. **Demoted:** don't build the extractors — the MCP dict already is a
  capability and can stay one.

## 10. Migration (each stage independently shippable, Python + Go same commit series)

The shared fixture corpus (idea 9) lands as the **first** commit — it becomes the new
Go-parity contract and prevents a rerun of the post-freeze pi-merge drift. Agents dispatch
old-vs-new by "does this agent have a manifest yet," so both mechanisms coexist during rollout.

0. **Engine as a leaf library, no callers** — the five pure functions + manifest schema in
   both languages, plus `tests/projection-fixtures/*.json` run by `pytest` and `go test`.
1. **Claude native-layer offload** (idea 6) — independent of everything, ship it right after
   stage 0.
2. **Pi** — the motivating case, smallest surface. Builtin manifest + the `agent_config.pi`
   key; deletes `host_pi_files` and the pi three-way merge.
3. **Claude** — the widest surface; migrate `settings.json` + `.claude.json`, classify
   credentials/history as `runtime-state`.
4. **Remaining four agents** get host reflection for free via the uniform key.
5. **Deletion** — remove the bespoke merges, snapshot constants, per-agent mount blocks, and
   the `host_*_files` keys.

Every stage ends with a nested-jail verification (per repo `CLAUDE.md`).

## 11. Open questions

**Resolved (Claude settings mechanics, verified 2026-07-18):** the `/settings` (`/config`)
write target *depends on the setting* — `model`/`theme`/`effortLevel`/`fastMode` go to the
**user** file (`~/.claude/settings.json`, which Claude mutates), permission approvals go to
**local** (`.claude/settings.local.json`), and there is no documented way to redirect this.
The **project** file (`.claude/settings.json`) is read-only from Claude's side (it never
writes it). Precedence: `managed > CLI args > local > project > user`. This is what §5.2 B
builds on: yolo owns project+managed, leaves user+local to Claude, and needs no overlay for
Claude. `~/.claude.json` is runtime-state (OAuth/MCP/trust/caches); yolo's MCP entries could
move to project-scope `.mcp.json` to stop co-writing it.

- **Claude `managed-settings.json` write-at-boot:** Claude honors
  `/etc/claude-code/managed-settings.json` (Linux; `/Library/Application Support/ClaudeCode/`
  on macOS, plus a `managed-settings.d/*.json` drop-in dir), and it *cannot be overridden* by
  user/project — ideal for the YOLO/security keys. But writing it needs **root**. Does the
  jail entrypoint (rootless-podman userns) have write access to `/etc` at boot? If yes, this
  is the strongest single win (idea 6). If not, fall back to putting the security keys in the
  **project** file (still outranks user; the jail is the boundary anyway).
- **Filter boundary default:** confirm applying `jail_filters` host-side at staging (so the
  `:ro` tree in the container is already redacted), with in-jail re-enforcement as the second
  belt. Acceptable that the staged tree is always post-filter?
- **Pi native override slot:** does Pi honor a project `.pi/settings.json` layered over the
  global one in a way yolo could exploit as a managed slot (a `managed-settings.json` analog)?
  Would shrink Pi's managed layer to zero force-writes.
- **Overlay UX:** is "a jail edit pins that keypath over host updates until values converge or
  `yolo config overlay --reset`" the right contract, or should overlay entries age out?
- **Go-port freeze discipline:** is the shared-fixture corpus the sanctioned exception to the
  parity freeze, and who signs off on the golden vectors as the spec?
- **Live host edits:** staging refreshes per `yolo` invocation and render happens per
  boot/attach — is attach-time re-render enough, or do we want an in-jail `yolo config sync`?
- **`yolo check` linting:** should it warn when a user filter targets a managed path and error
  when `overrides` try to weaken one?
- **Codex host reflection:** worth it given inherent TOML comment loss, or keep Codex
  render-only (defaults+user+managed) until asked?
- **Migration seeding accuracy:** the old snapshot tracked only one level, so seeded overlays
  may misclassify deep divergence as jail-local edits (frozen until reset). Offer a one-time
  `yolo config overlay --reset` per agent as the escape hatch.
- **`yolo config render` in the safety prompt:** the offline runner (§6.2) is what lets the
  config-change confirmation diff the *rendered output* rather than the config text — the
  mechanism that makes an execution-based model (Lua, or even the data filters) safe to prompt
  on. Confirm the change-safety prompt should key off `render` output diffs, and whether it
  runs pre- or post-transform-execution.

---

*Provenance: produced 2026-07-18 from a research→design→synthesis workflow — 4 research agents
mapping the real mechanism (agent surfaces, host↔jail plumbing, failure modes, prior art), 4
independent composition-model designs (Prism / conf.d fragments / patch-pipeline / capability
planes), and a judged synthesis. This doc is the maintainer-facing writeup; the raw structured
output is retained in the session scratchpad, not committed.*
