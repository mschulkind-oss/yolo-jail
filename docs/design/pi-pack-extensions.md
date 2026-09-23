---
title: "Extension delivery: files into agent-declared aliases"
date: 2026-09-19
status: accepted
tags: [pi, extensions, plugins, packs, architecture, audience, agent-plugins]
summary: "Where this landed: contributes a file tree to an agent by name (Architecture D), against the Agent Plugins 1.0 portable standard, with YOLO as the placer and no vendor install verb. Core parses nothing because the standard namespaces client-specific components. `claude_plugins` is retired. ⚠ The field-level encoding (`files` + `agent`/`agents`) is superseded by `slots-and-contributions.md`; the architecture — a slot an agent declares — stands."
vantage:
  status-chip: true
---

# Extension delivery: files into agent-declared aliases

**Status:** DECIDED, 2026-09-19 — the architecture; **encoding superseded** 2026-09-20. The architecture below
stands; only its field-level encoding is under revision.

> [!WARNING]
> **The encoding is superseded — read [`slots-and-contributions.md`](./slots-and-contributions.md).**
> Everything here about *architecture* still governs: one delivery primitive, Agent Plugins 1.0
> as the portable standard, YOLO as the placer with no vendor install verb, and hooks disclosed
> rather than gated. What does **not** govern is the field shape this doc settled on —
> `kind: "files"` carrying `agent` vs `agents` to tell a destination from a contribution. That
> singular/plural flag is exactly what [`slots-and-contributions.md`](./slots-and-contributions.md) retires: a destination is a
> **second axis** (`exposes`), addressed by the **agent `bin` name**. Slices 1–3 landed the
> superseded shape and are pending rework ([§5](#5-how-d-ran--the-superseded-implementation)).

> **In short.** A pack contributes a **tree of files to an agent by name** — the mechanism
> `briefing` and `skills` already use — and YOLO mounts it read-only into a directory the
> agent pack declares. That is the whole delivery mechanism. It is written against the
> **Agent Plugins 1.0** standard, whose whole design is that portable components (skills,
> MCP) are fixed-location and client-specific ones (hooks, LSP, commands) live under a
> reverse-domain namespace that every other client ignores. So **core parses nothing, refuses
> nothing, and knows no agent.** The one real leftover is `claude_plugins`, which this
> retires.

**Where we arrived, in one list:**

1. **Delivery is one primitive.** ✅ **Built.** `kind: "files"`'s audience refusal is lifted, so a
   content pack addresses `agents: ["pi"]` exactly as `skills` and `briefing` already do. Nothing
   new was invented; no `kind: "extensions"`, no per-agent Go.
2. **No `target` axis.** One files destination per agent; a second is a load error — ✅ enforced
   2026-09-21 (`packdecl.validateFilesDestinations`; see [§8](#8-invariants-and-failure-modes)).
   (`target` was Architecture B's extra routing token, and it is dropped.)
3. **Agent Plugins 1.0 is the portable standard** ([agent-plugins.org](https://agent-plugins.org/)),
   not Claude's format. Portable = `skills/` + `mcp.json`; everything else travels namespaced —
   and a full Claude plugin still reaches Claude in full ([§1.2](#12-the-standard-under-this-agent-plugins-10)).
4. **YOLO is the placer** (P7). No vendor install verb runs in a jail — the tree is
   materialized and the agent reads it.
5. **Hooks are disclosed, not gated.** Same trust class as skills and briefings, which can
   already instruct arbitrary action.
6. **Fetching is a different axis**, owned by [`pi-extension-lifecycle.md`](./pi-extension-lifecycle.md).
   Its resolver half already ships (`internal/packsrc` + `packs.lock.json`); the materializer half
   does not.
7. **`claude_plugins` is retired**, and nothing like it replaces it — no agent-named hook:
   deliver plugin trees locally, decompose them, or author one YOLO-owned Agent Plugins 1.0
   plugin rather than calling `claude plugins install`. ✅ **Built 2026-09-20**: the hook and
   `installClaudePlugins` are deleted and `packdecl` refuses the name with that migration. Its one
   live consumer took the **author-one-plugin** route, not the decomposing one —
   [`mcp-configuration.md`](../reference/mcp-configuration.md#oq-lsp1)'s option D, built 2026-09-22.

**The load path is no longer a question, and the answer carries one trap.** Claude reads a
YOLO-authored plugin with **no marketplace** — auto-loaded from `~/.claude/skills/*` — but a
`.claude-plugin/plugin.json` **is mandatory on that path**: the root-level manifest fallback that
marketplace and archive installs enjoy does not apply, so a manifest at the tree root is simply not
found. Measured statically against Claude Code 2.1.278
([`OQ-LSP3`](../reference/mcp-configuration.md#oq-lsp3) in
[`mcp-configuration.md`](../reference/mcp-configuration.md#lsp-claudes-route-is-a-generated-plugin)).

**Why you might still question all of it:**

- **"Against the standard" overpromises.** The portable set is *only* skills + MCP; hooks,
  LSP, commands and agents are client-specific by construction. One artifact does less than the
  phrase suggests.
- **YOLO becomes the distributor**, so it owns pinning third-party plugin bytes. That is a
  supply-chain surface the seed path does not remove either — it just moves it.
- **The derive rule widens.** [§6](#6-what-a-derive-may-read--the-rule-being-sharpened) makes a
  pack's own content a derive input for the first time, and that set was closed on purpose.
- **Determinism depends on ordering.** The addressed-content source must be ordered and
  declared, or a derive stops being a pure function of the manifests.

**Needs your ruling:** **None *here*** — all six questions are ruled ([decision ledger](#10-decision-ledger)).
The live questions have moved to the role model —
[`OQ-D1`–`OQ-D4`](./slots-and-contributions.md#OQ-D1) — and the surface —
[`OQ-M1`–`OQ-M4`](./manifest-language.md#OQ-M1).

**Reads with:** [`slots-and-contributions.md`](./slots-and-contributions.md) (**supersedes this
doc's encoding**), [`manifest-language.md`](./manifest-language.md) (the surface),
[`agent-config-distribution.md`](../research/agent-config-distribution.md) (the measured
formats), [`pi-extension-lifecycle.md`](./pi-extension-lifecycle.md) (the fetch axis),
[`mcp-configuration.md`](../reference/mcp-configuration.md#lsp-claudes-route-is-a-generated-plugin) (the one live consumer of the retired hook),
[`agent-briefings.md`](../reference/agent-briefings.md#audiences-what-varies-per-destination) (the `agent`/`agents` mechanism this reuses),
[`extension-point-principle.md`](../reference/extension-point-principle.md).

---

## 1. The problem, and the shape of the answer

A content pack that wants to ship Pi extensions must today write per-file `kind: "files"`
stanzas at literal paths like `~/.pi/agent/extensions/x.ts`, because `files` refuses the
audience selector that `skills` and `briefing` have. That couples the pack to one agent's
private layout, and it collides with what the agent pack already claims at the same path.

The answer is not a new mechanism. It is to let `files` name its recipient the way the other
two content kinds do, and mount the contributed tree under a per-pack directory the agent pack
declares. Everything after this section is either the standard that makes that safe or the
one hook it retires.

### 1.1 What "extension" means across agents

| Agent | Unit | Where it is read | Activation |
| :--- | :--- | :--- | :--- |
| **Pi** | a `.ts`/`.js` file | `~/.pi/agent/extensions/*.ts`, or a subdir with `index.ts` | automatic once present |
| **Claude** | a directory with `.claude-plugin/plugin.json` | a plugin cache, seeded or loaded locally — including auto-loaded from `~/.claude/skills/*` | `enabledPlugins` in settings, except on the local arms, which are enabled by default |
| **Codex, Copilot** | the same `.claude-plugin/` layout | their own plugin commands | marketplace + enable |
| **Skills (any)** | a `SKILL.md` tree | the agent's skills dir | automatic once present |

Four of five rows are "a directory tree the agent scans, plus sometimes a boolean in config."
The differences are which directory and whether a vendor registry must know first.

### 1.2 The standard under this: Agent Plugins 1.0

**Agent Plugins 1.0** is an open, vendor-neutral package format, and it is the standard here —
Claude's `.claude-plugin/` is one client's layout, not the standard. v1 defines exactly two
portable components:

| Portable | Location |
| :--- | :--- |
| Agent Skills | `skills/<name>/SKILL.md` |
| MCP servers | `mcp.json` at the plugin root |

Everything else — agents, commands, rules, **hooks**, LSP servers — is **client-specific by
construction**, and lives under a reverse-domain namespace: either the `extensions` map in
`plugin.json` or a top-level directory like `com.github.copilot/`. The spec is explicit that
clients **ignore namespaces they do not implement** and **ignore component types they do not
support**.

**Portable is a floor, not a filter.** Nothing a client owns is lost or lowered: a pack ships
the **whole tree**, YOLO moves it whole, and a full Claude plugin — hooks, LSP servers,
commands, agents and all — reaches Claude intact, because those components sit exactly where
Claude reads them. YOLO strips nothing, re-encodes nothing, and special-cases no client: it
delivers the tree and each client reads its own namespace. So a pack declaring a Claude plugin
gets the full Claude plugin; a pack declaring portable skills + MCP gets those everywhere;
and one pack can do both at once.

**That is why core parses nothing.** The standard does the namespacing; a pack ships one
directory, each client reads its own namespace, and YOLO moves opaque bytes. There is no
union to lower, no hook to police. The `.claude-plugin/` layout is a payload convention YOLO
may carry, never core's internal model.

**A pack can be a plugin.** A pack root may carry a root `plugin.json` (the portable artifact
any consumer reads) *and* `pack.json` (the YOLO-only contributions). A non-YOLO consumer
installs the same repo as a plugin and ignores `pack.json`.
[`yolo pack init --from-plugin`](../../internal/cli/pack.go) already wraps a `.claude-plugin/`
tree as a pack, reading only the name and which components are declared so the footprint can
say `⚠ RUNS CODE`.

**On hooks.** A hook is shell the host runs at a lifecycle event; a skill is prose the model
may obey. The capability is the same class and the difference is determinism, so the response
is **disclosure, not refusal** — which is already YOLO's rule (`packhostgrants.go`: "the
boundary today is DISCLOSURE, not consent"). A hook's presence is structural under the
standard, so saying so needs no parse.

## 2. Principles

- **P1. Core never knows an agent.** No per-tool Go, no global `kind: "extensions"`.
- **P2. A content pack names its recipient, never a path.**
- **P3. Reuse `files`; add no kind and no axis.**
- **P4. A derive may read *declared* sources, and another pack's addressed content is one.**
  The rule was "closed and core-owned"; the sharpening is "closed and **declared**" — core owns
  the routing, a pack may own the content. See [§6](#6-what-a-derive-may-read--the-rule-being-sharpened).
- **P5. Policy in packs, effects in core.** A pack decides *what*; core performs *how*. The
  derive returns values; it never stages files.
- **P6. One delivery mechanism for every agent.**
- **P7. YOLO places; agents read.** No vendor install verb runs in a jail, and no vendor cache
  is filled.

### 2.1 Non-goals, and the three axes

| Axis | Who does the work | Authority |
| :--- | :--- | :--- |
| **Delivery** | YOLO — place a tree, `:ro` | **this document** |
| **Fetch / resolve** | YOLO too, via `packsrc`'s pinned resolver | [`pi-extension-lifecycle.md`](./pi-extension-lifecycle.md) |
| **Knowledge / config** | YOLO derives | [`gateway-provider-packs.md`](./gateway-provider-packs.md), `derive.lua` |

A Pi package (`"packages": [...]` in `settings.json`) rides the **fetch** axis, and a pack can
declare it; this document only delivers trees. Non-goals: a cross-agent plugin standard (we
adopt Agent Plugins 1.0 rather than design one), and the data half (env, base URLs, model
catalogs).

## 3. The candidate architectures

| Architecture | Verdict |
| :--- | :--- |
| **A — a named Go hook per agent** (`pi_extensions`, and `claude_plugins` today) | **Rejected for new work.** It compiles agent-specific code into core, one routine per agent forever, and third parties can never ship the same shape. The existing hook is [§7](#7-reopening-claude_plugins)'s separate question. |
| **B — addressed `files` with a `target` slot** (a previous revision) | **Superseded by D.** It is D plus a second "which channel" axis beside `kind`, for a second channel that does not exist; the `target` token is dropped. |
| **C — a native package scanner** | **Not an architecture.** A pack's tree *may* be a plugin directory, but scanning for a manifest is a source convention D consumes, not a delivery mechanism. |

### Architecture D: content addressed to a slot an agent pack declares

Three parts, no new kind — but **two SHAPES of `files`, which the last revision got wrong by
overloading one.**

1. **The agent pack declares a bare SLOT** — a destination that ships nothing:
   ```json
   { "kind": "files", "agent": "pi", "into": ".pi/agent/extensions" }
   ```
   A destination carries `agent` and `into` and **no `from`**. It names where addressed content
   lands; it does not carry content. This is exactly how a `skills`/`briefing` destination
   already behaves — `into` with the content arriving from elsewhere.
2. **Content is ADDRESSED, by whoever ships it — including the agent pack's own:**
   ```json
   { "kind": "files", "agents": ["pi"], "from": "pi-extensions" }   // a content pack
   { "kind": "files", "agents": ["pi"], "from": "extensions" }      // pi shipping its own
   ```
   Core mounts each read-only at `.pi/agent/extensions/<contributing-pack>/`. **There is no
   special case for the owning pack** — it addresses itself like every other pack, so **nothing
   lands at the slot ROOT and the slot is never itself a mount.** That single rule is what the
   previous revision lacked, and it is why the root-vs-nested mount conflict cannot arise.
3. **The owning pack's `derive.lua` sees what was delivered** and decides activation. Pi does
   nothing (auto-discovery); Claude writes `enabledPlugins`. That source is
   [§6](#6-what-a-derive-may-read--the-rule-being-sharpened).

> [!NOTE]
> **Why this shape, and not a slot that also carries a tree.** The last revision let the agent
> pack ship its own `extensions/` *at the slot root*, which made the slot a mount with an
> addressed mount nested inside it — an EROFS-class failure. It got there by piggybacking a
> **destination** onto the **content** kind and inheriting `files`'s `from`-required rule. Splitting
> the two shapes (a destination carries no `from`; content always does) removes the overload and
> the conflict together.
>
> ⚠ **Superseded, one level up.** Even split, this is still `agent` vs `agents` — a singular/plural
> distinguishing two opposite roles. [`slots-and-contributions.md`](./slots-and-contributions.md)
> makes a slot a **second axis** (`exposes`) addressed by the **agent `bin` name**, and retires
> the flag here and in `briefing`/`skills`. Read that before building the pack migrations.

## 4. Why D and not A/B/C

A is agent Go; B is D plus a speculative axis; C is an input convention. D reuses the audience
rule that already exists for two kinds, adds no kind, and leaves activation where it belongs —
with the pack that owns the directory. The full comparison is not worth a second table.

## 5. How D ran — the superseded implementation

> [!NOTE]
> These four touchpoints encode the **superseded** shape and are pending rework under
> [`slots-and-contributions.md`](./slots-and-contributions.md). They are recorded as *what
> landed*, not as the build plan: slices 1–3 shipped, and slice 4 (the derive source) is parked
> on the shape decision.

1. [`internal/packdecl/contributes.go`](../../internal/packdecl/contributes.go) — allow
   `agent`/`agents` on `kind: "files"`, keep `into`-xor-`agents`, and **split `from`: required
   on an addressed contribution, forbidden on a destination.** A destination that carries `from`
   is the overload [§3](#3-the-candidate-architectures) removes.
2. [`internal/packload/mergedest.go`](../../internal/packload/mergedest.go) — extend
   destination borrowing to `files`; enforce one destination per agent.
3. [`internal/cli/run/packfiles.go`](../../internal/cli/run/packfiles.go) — the directory
   branch already mounts a tree wholesale. An addressed contribution needs only the
   `<into>/<pack>` join. No per-file staging, no filename rewriting.
4. [`internal/agentcfg/luahook/derive.go`](../../internal/agentcfg/luahook/derive.go) — add
   the addressed-content source, ordered by pack name.

## 6. What a derive may read — the rule being sharpened

The rule was stated as *"the source set for `derive` is closed and core-owned; a pack projects
and never invents a source."* The invariant that matters is narrower:

| Old wording | Sharpened wording |
| :--- | :--- |
| The source set is closed and core-owned. | Closed and **declared**; core owns the *routing*, a pack may own the *content*. |
| A pack projects from core's tables. | A pack projects from **declared** tables. |
| A pack never invents a source. | A pack never invents a source **at runtime**; an agent pack may *declare* an alias. |

Determinism survives (the addressed table is a function of the selected packs and their
manifests), and "project, don't invent" survives (the alias has a declared name and shape).
The cost is honest: this is the first time a pack's own content is a derive input, so the
mitigation is the usual one — an unmatched `agents` name is fatal, an unmatched alias is
reported with a fix.

## 7. Reopening `claude_plugins`

There was a decision here — 2026-07-27, `bbe84f1d` — and it was made as part of the
pack-declaration reform, before the plugin survey. Its own comment calls it "a deliberate
admission rather than an oversight." A second tool now wants the same shape, so it is re-ruled
rather than cited as precedent.

### 7.1 What it did

`packs/claude/derive.lua` wrote `enabledPlugins` (ordinary config), and the hook's only job was to
`claude plugins install` the plugin so the cache held what `enabledPlugins` named. Both are gone
now: the hook was deleted with the ruling below, and the derive's `enabledPlugins` with the LSP
rework that replaced it.

### 7.2 The decision

**Retire the hook.** YOLO places the plugin tree and Claude reads it. Two ways to fill it:

- **Decompose** — map the plugin's components onto kinds YOLO already owns.
- **Author one YOLO-owned Agent Plugins 1.0 plugin** and load it locally — off the skills tree,
  which needs no flag at all, or via `--plugin-dir`. ✅ **This is the route the LSP case took**
  ([`mcp-configuration.md`](../reference/mcp-configuration.md#lsp-claudes-route-is-a-generated-plugin), built 2026-09-22), so the shape and the load
  path are both proven: `jailcontent.writeLSPPlugin` renders one such tree every boot.

**The one alternative kept:** seeding Claude's plugin state and deleting the hook. It works
and is measured, but it reproduces the vendor cache layout, which is the thing
[§1.2](#12-the-standard-under-this-agent-plugins-10) argues against depending on.

## 8. Invariants and failure modes

1. One files destination per agent; a second is a load error.
2. Namespacing is the contributing pack's name; collisions are impossible.

   ⚠ Invariants 1 and 2 were UNBUILT until 2026-09-21, and invariant 2 was violated at the host
   notch, where a contributor's tree was written at the alias root. Both are now enforced and
   pinned — see the ledger rows for [OQ-1](#10-decision-ledger) and [OQ-4](#10-decision-ledger).
   Neither could fire on the shipped set, because no manifest in the corpus declares a files slot
   at all; that is why the gate stayed green through both.

   ⚠ **And invariant 2's "collisions are impossible" holds ACROSS packs only**, which it does not
   say. Within one pack, two addressed trees aimed at one agent resolve to the same
   `<alias>/<pack>` — measured 2026-09-21, and the notches disagreed (the jail emitted a duplicate
   mount podman refuses; the host merged both trees silently). Refused at the manifest as of the
   same day, with the remedy being one `from`; whether it should instead MERGE is
   [`slots-and-contributions.md`](./slots-and-contributions.md)'s
   [`OQ-D12`](./slots-and-contributions.md#OQ-D12).
3. Delivery is read-only and non-fatal; a missing source warns and skips.
4. The owner's derive is pure — it reads the addressed set and returns config.

| Failure mode | Response |
| :--- | :--- |
| Source tree missing | warning, skip |
| `agents` name not in the claim set | **fatal**, naming the addressee |
| Agent selected but declares no alias | reported orphan with the fix |
| Extension syntax error | the agent's loader reports it; boot continues |

## 9. Open questions

**None *here*** — but the six below are now **moot**, not settled: they were ruled against the
field shape [`slots-and-contributions.md`](./slots-and-contributions.md) supersedes. The live questions are the role model's
[`OQ-D1`–`OQ-D4`](./slots-and-contributions.md#OQ-D1).

The five earlier questions were ruled in review on 2026-09-19, and this review resolved
the sixth — the alias-root layout — by **removing the overload that created it**. A `files`
destination is a bare slot (no `from`), and every pack's content, the owner's own included, is
addressed, so nothing mounts at the slot root. See [§3](#3-the-candidate-architectures) and the
[decision ledger](#10-decision-ledger).

## 10. Decision ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| **SUPERSEDED** | The `files`+`agent`/`agents` field encoding, and the alias/destination questions with it — a slot is a second axis (`exposes`) addressed by the **agent `bin` name**, not a `files` variant | 2026-09-20 | [`slots-and-contributions.md`](./slots-and-contributions.md) | rework |
| **D** | Addressed `files` into agent-declared aliases; A rejected, B superseded, C an input convention | 2026-09-19 | [§3](#3-the-candidate-architectures) | — |
| **Standard** | Agent Plugins 1.0 is the portable convention (skills + MCP); client-specific components stay namespaced, so YOLO parses nothing | 2026-09-19 | [§1.2](#12-the-standard-under-this-agent-plugins-10) | — |
| **Placer** | YOLO places; no vendor install verb runs in a jail | 2026-09-19 | [§2](#2-principles) | — |
| **Derive sources** | A declared source may be another pack's addressed content | 2026-09-19 | [§6](#6-what-a-derive-may-read--the-rule-being-sharpened) | — |
| **OQ-1** | No `target` axis — one files destination per agent; a second is a load error | 2026-09-19 | this doc | **yes, 2026-09-21** — `packdecl.validateFilesDestinations`, authoring path only. It had shipped unimplemented: a manifest declaring two loaded clean, and the jail then honored the LAST while the host honored BOTH |
| **OQ-2** | Retire `claude_plugins`, and add nothing like it (no agent-named hook). Deliver plugin trees locally — decompose, or author one YOLO-owned Agent Plugins 1.0 plugin | 2026-09-19 | [§7](#7-reopening-claude_plugins) | **yes, 2026-09-20** — the hook and `installClaudePlugins` deleted, `packdecl` refusing the name with the migration. Its one consumer landed 2026-09-22 on the author-one-plugin route ([`mcp-configuration.md`](../reference/mcp-configuration.md#lsp-claudes-route-is-a-generated-plugin)) |
| **OQ-3** | `pi-extensions/` is the recommended source directory | 2026-09-19 | this doc | — |
| **OQ-4** | Subdirectory per pack inside the alias | 2026-09-19 | this doc | **yes, 2026-09-21** — `packload.SlotLanding`, the one resolver both notches call. The jail had joined since the day it shipped; destination borrowing never did, so the HOST wrote a contributor's tree at the alias ROOT, which is the layout this ruling exists to prevent |
| **OQ-5** | Support Agent Plugins 1.0 directly; a `.claude-plugin/` manifest beside the portable root is acceptable for Claude | 2026-09-19 | this doc | build |
| **OQ-6** | A `files` **destination** is a bare slot (`agent`+`into`, **no `from`**); all files content is addressed (`agents`+`from`), the owner's own included, so nothing mounts at the slot root | 2026-09-20 | [§3](#3-the-candidate-architectures) | — |
