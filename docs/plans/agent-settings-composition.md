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

## 5. Runtime overlay (what replaces the snapshot)

The rendered jail file is a build product. Agent runtime edits are harvested into an explicit
overlay sidecar:

```
overlay' = deepMerge(overlay, mergeDiff(last_render_sidecar, current_file))
```

At boot we diff the current file against *what we rendered last time* (`last_render`), and the
difference is definitionally the agent's own edits. Those accumulate in `overlay` (the
`runtime` layer); deletions persist as `null` tombstones; entries auto-retire when host and
runtime values converge. Two small, inspectable sidecars replace the history-dependent
three-way snapshot — and jail edits now win at *full depth*, not just the first level.

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
   YOLO block to **`/etc/claude-code/managed-settings.json`** instead of into the contested
   `settings.json`. Strictly stronger than any boot-time enforcement — a *live* runtime rewrite
   by Claude cannot drop the managed keys mid-session. **Independent of the whole engine**;
   shippable now. (Open question: confirm the pinned Claude honors it and the entrypoint can
   write `/etc` at boot — see §10.)
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
- **layer** — one named, ordered source for a structured surface: `defaults < host < user <
  runtime < managed`.
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

- **Claude `managed-settings.json`:** does the pinned Claude honor `/etc/claude-code/
  managed-settings.json`, and can the entrypoint write `/etc` at boot (no sudo in-jail)? This
  gates the strongest single win (idea 6).
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
