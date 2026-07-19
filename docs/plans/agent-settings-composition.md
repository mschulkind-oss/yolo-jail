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
| `user` | `yolo-jail.jsonc` → `agent_config.<agent>.settings` | jail-only config the user declares |
| `runtime` | harvested overlay sidecar | what the agent changed at runtime |
| `managed` | manifest data | yolo's security-boundary keys — always win |

### Merge

**One recursive deep merge** (RFC 7386 semantics: objects merge at every depth, `null`
deletes, arrays replace) as the default. The manifest may pin a per-keypath strategy:
`deep | replace | append` (append with dedupe). No depth cliff, no atomic-list surprise —
the strategy is *readable from the manifest*, and the same ~40-line function runs at every
level. This directly replaces the two-level merge that already caused Go parity bugs.

### Projections (this is where the strip problem dies)

A **projection** = `(destination, layer list, filter program, enforce set)`.

- **Host projection:** the *identity* over the `host` layer. yolo never writes host files —
  the host `settings.json` *is* the host layer, and host Pi keeps reading it natively. Nothing
  on the host changes. (Defining it as a projection keeps one mental model and leaves
  host-side materialization expressible later if ever wanted.)
- **Jail projection:** folds all five layers, then applies a **filter program** — a tiny,
  closed, portable vocabulary of three ops declared as data:
  - `{op: "drop", path}` — remove a keypath (JSON Pointer, `*` wildcard segments)
  - `{op: "dropItems", path, match}` — remove glob-matching elements from an array
  - `{op: "set", path, value}` — per-side transform

That is the whole redaction primitive. Deliberately **not** RFC 6902 JSON-patch — three ops
cover every observed need and port trivially.

**Directionality is just where a value lives in the model:** host-only = present in the host
source, filtered out of the jail projection. Jail-only = the `user` layer. Transformed-per-side
= a `set` filter. There is no separate mechanism to build.

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
| `user` | per-workspace | `yolo-jail.jsonc` → `agent_config.<agent>` (git-committable) | that workspace (travels with the repo) |
| `runtime` overlay | per-workspace identity | sidecar keyed on `sha256(host workspace dir)` | that workspace's jails, across restarts |
| **render output** | per-boot | the agent's actual file in jail home | nobody — ephemeral, rebuilt each boot |

That yields a clean, VS-Code-shaped contract the user can reason about without knowing the
internals:

- **Edit the host file → global.** Every jail picks it up on its next boot (they all mount
  the same host source).
- **Edit `yolo-jail.jsonc` → per-workspace.** Committable; travels with the repo to teammates.
- **Edit inside the jail (`/settings`) → that workspace's jail only.** Persisted
  per-workspace (via the agent's own user-scope file for Claude, or the overlay sidecar for
  single-scope agents — §5.2); it does not leak to other workspaces or back to the host.

### 5.2 Surviving regeneration — three ways to make `/settings` stick

The build-product model only works if a live jail edit survives the rebuild. The enabling
trick: **yolo always knows exactly what *it* wrote last time, so anything different on disk
is definitionally your edit.** Three mechanisms, most-general to simplest:

**A. Runtime overlay (capture-diff) — the universal mechanism.** Keep two sidecars the agent
never sees: `last_render` (the exact bytes yolo wrote last boot) and `overlay` (accumulated
jail edits). Each boot/attach:
```
delta   = mergeDiff(last_render, current_file)     # current = last_render + your /settings edits
overlay = deepMerge(overlay, delta)                # accumulate (deletes recorded as null tombstones)
render  = layers(defaults, host, user, overlay, managed)   # overlay is a LAYER, below managed
write(render); last_render = render
```
So your `/settings` change is captured on the next regeneration and re-applied on every one
after. Three details make or break it: **precedence** (overlay sits above host/user so your
edit wins — but an entry auto-retires when the host value converges to yours, plus a
`yolo config overlay --reset <agent>` escape hatch); **deletions** (recorded as `null`
tombstones so the rebuild does not resurrect a key you removed — the exact bug today's merge
has); and **managed keys** (the `managed` layer sits *above* the overlay, so an attempt to
change a security-boundary key via `/settings` is captured but overridden on render — it
visibly reverts, which is correct). This replaces today's fragile per-agent three-way
snapshot with one diff-against-our-own-output, and jail edits now win at *full depth*.

**B. Native scope split — the cleanest, and for Claude it removes the overlay entirely.** The
most robust fix is to *not share a file at all*. Claude's verified precedence is
`managed > CLI args > .claude/settings.local.json (local) > .claude/settings.json (project) >
~/.claude/settings.json (user)`, and — the load-bearing fact — **Claude mutates the *user*
file at runtime** (`/config` writes `model`/`theme`/`effortLevel`/`fastMode` there, permission
approvals go to `local`) but **never writes the *project* file** (`.claude/settings.json`) — it
only reads it. So split the scopes by who writes them:

- **Security / YOLO keys → the managed file** (`/etc/claude-code/managed-settings.json` on
  Linux; `/Library/Application Support/ClaudeCode/managed-settings.json` on macOS). Highest
  precedence, user cannot override — genuinely *enforced*. (Caveat: writing it needs root;
  whether the jail entrypoint can write `/etc` at boot is §11.)
- **yolo's asserted keys (host-projected + defaults) → the project file**
  (`.claude/settings.json`). Claude never writes it, so yolo **regenerates it freely every
  boot with zero contention** — this is the build-product surface.
- **The user file (`~/.claude/settings.json`) → yolo seeds it once, then never touches it, and
  marks it `runtime-state` (persisted per-workspace).** Because Claude both writes it (via
  `/config`) and yolo leaves it alone, **the user's in-jail `/config` changes survive
  regeneration for free — no overlay, no capture, no merge.**
- **Local (`.claude/settings.local.json`) → left alone** (permission approvals; irrelevant
  since the jail is the security boundary).

Now yolo's config and the user's live edits live in *different files*, and precedence does the
composition. The one rule to respect: **only put a key in the project file that you actually
mean to assert over the user** (project outranks user), so anything you want the user to be
able to change in-jail is simply *absent* from yolo's project file and flows from their user
file. This is idea C's key-ownership contract realized through native scopes — and for Claude
it makes the overlay unnecessary.

**C. Key-ownership (surgical merge) — the minimal version.** yolo declares an *ownership set*
(the keypaths it manages — permissions, MCP, host-projected keys) and writes only those,
leaving every other key in the file untouched. Your `/settings` addition of an unowned key
just survives. Closest to today's code, but replaces the fuzzy three-way merge with an
explicit "these keypaths are mine, everything else is yours" contract. Trade-off: you lose
the clean build-product property, and removing an *owned* host key still needs tombstone
memory.

**Recommendation:** B where the agent has native scopes with a stable, yolo-writable slot —
**Claude qualifies, and there B eliminates the overlay entirely** (verified: project scope is
Claude-read-only, user scope is where `/config` persists edits). A (overlay) is the universal
fallback for single-scope / contested-file agents like pi, opencode, Codex. C (key-ownership)
is not really a third option so much as *the rule that drives B*: it is how you decide which
keys yolo asserts (project/managed) vs leaves to the user (user scope).

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
PER-WORKSPACE                user (yolo-jail.jsonc)             ┤  → project(host | jail, filters)
PER-WORKSPACE                runtime overlay                    ┘  → RENDER
                                                                     ↓
PERSISTED (mounted/symlinked, NOT part of the render):               jail home  [rendered + reflected]
  shared creds (GLOBAL) · per-workspace history/trust ─────────────► jail home  [runtime-state]
```

So the sharing story is explicit and sensible: **host + defaults + managed + shared
credentials are one truth across all jails; `user` + overlay + history are per-workspace;
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

**What the user writes (the only per-project config):**
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

**Result:**
- **Host:** unchanged. Host Pi still loads the gate extension; every host command still
  requires approval.
- **Jail:** the `extensions` array is rendered without the gate entry, and the extension file
  itself is never staged. No approval prompts in the jail (the container is the security
  boundary). No sanitized second host file, no manual stripping.

That is the entire feature. It generalizes: any host key you want kept out of any jail is one
filter line.

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
   languages.
8. **Fail-closed on unparseable input** (graft from the patch-pipeline design): keep the last
   good staging (host side) or last render (jail side) with a loud warning — instead of the
   current mass-rollback on a transient typo.
9. **Shared golden-fixture corpus** (`layers/filters in → render/overlay/provenance out`) run
   byte-identically by `pytest` *and* `go test`. This **inverts the Go-parity model**: instead
   of byte-matching bespoke Python with `OrderedMap`/`pyEqual` scaffolding, both languages
   implement the same spec against the same vectors. It is the direct antidote to the
   `host_pi_files` / untested-Go-pi-merge drift class.
10. **Provenance sidecar → `yolo config explain <agent> [keypath]`.** Free once render is a
    fold over named layers. Answers "why is this key set?" and "where did my host key go?"
    (*dropped by filter X*) — turning today's archaeology into a command.
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
  `defaults < host < user < runtime < managed`.
- **scope** — the *storage* coordinate: where a layer/file lives and who shares it — `global`
  (all jails), `per-host` (host source, all jails here), `per-workspace` (`yolo-jail.jsonc` +
  overlay + history), `per-boot` (the ephemeral render). Every piece of config = one layer ×
  one scope (§5.1).
- **projection** — the materialization of the stack for one side: `(destination, layers,
  filters, enforce)`. Host = identity over `host`; jail = all five + filters.
- **staging** — the host-CLI step each `yolo` invocation: glob-filtered copy of an agent's host
  files into `ws_state`, relative paths preserved, truncate-in-place, bind-mounted `:ro`.
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

---

*Provenance: produced 2026-07-18 from a research→design→synthesis workflow — 4 research agents
mapping the real mechanism (agent surfaces, host↔jail plumbing, failure modes, prior art), 4
independent composition-model designs (Prism / conf.d fragments / patch-pipeline / capability
planes), and a judged synthesis. This doc is the maintainer-facing writeup; the raw structured
output is retained in the session scratchpad, not committed.*
