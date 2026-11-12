---
title: "The manifest language: declarations stay inert data, and the syntax should stop fighting us"
date: 2026-09-20
status: accepted
tags: [packs, manifest, config, format, lua, starlark, design]
summary: "A pack manifest compresses to a quarter of its bytes — more redundant than the repo's own prose — because it is a flat tagged union that repeats the pack's identity and the `kind` tag on every entry, and spells mechanism rather than intent. The syntax and the MODEL are two independent decisions, and the model change (group by kind, state the agent name once per pack, keep distinct facts explicit) carries most of the gain under any syntax. This doc ARGUES both; the readable-config redesign ruled them on 2026-10-25 (the model change, in TOML) and it is built. The options it weighed: what a manifest may be written in (JSON restructured, data-only Lua, Starlark, or the heavier config languages), and the rule that compression may remove repetition but never a claim."
vantage:
  status-chip: true
stage: BUILT
next: "Every question is ruled or moot, and the readable-config build shipped the manifest change: nothing is owed here"
---

# The manifest language: declarations stay inert data, and the syntax should stop fighting us

**Status:** 2026-10-25 — [`OQ-M1`](#OQ-M1) and [`OQ-M2`](#OQ-M2) ruled in the readable-config
redesign ([ledger](readable-config.md#decision-ledger)), and [`OQ-M3`](#OQ-M3) closed as moot by
the same ruling, when the maintainer accepted that redesign;
[`OQ-M4`](#8-decision-ledger) was decided as an implementation choice ([MN-D1](#MN-D1)). Built by 2026-10-30 through the readable-config build: every shipped pack manifest is a
`pack.toml` ([`pack-manifests.md`](pack-manifests.md)), and every shipped loophole manifest a
`loophole.toml` since `9839d26d3`. MEASURED (2026-11-12, in the tree): no `pack.json` is left under
`packs/`. Re-checked against the tree
and the sibling rulings 2026-09-24: the identity lever in [§3](#3-what-is-actually-free-to-change)
now follows [`OQ-D5`](slots-and-contributions.md#7-decision-ledger) (declared once per pack, never derived),
which that doc ruled on 2026-09-20. Sibling to
[`slots-and-contributions.md`](slots-and-contributions.md) — that one fixes the manifest's
**role** model, this one its **surface**.

> **In short.** A pack manifest is a low-level, repetitive description of environment
> construction: a flat list of objects each carrying `kind`, each repeating the pack's own
> identity, spelling mechanism (`managed`, `retireOnFirstRender`, `mode: rmw`) rather than
> intent. It reads like bytecode, and it gzips to **0.24** — three bytes in four derivable from
> the rest, more redundant than English prose.
>
> **Two decisions, and they are independent.** *What the declaration model is* (group by kind,
> state the pack's own identity once, keep every distinct fact explicit) is worth most of the gain
> and is **syntax-independent**. *What it is written in* (JSON, YAML, a data-only Lua, Starlark,
> Jsonnet/CUE/Dhall) is the smaller, separable choice. This doc argues to do the model change
> first, and to move off JSON only if the model change leaves a file a human still cannot read.

**Why now.** The user's own words: *"we're at a point right now where we can make huge breaking
changes… We don't need transitions. It will be very painful very soon."* Every pack manifest
written today is a migration debt tomorrow, so the format should be chosen once.

> **The line that must not move.**
> [`pack-system.md`](../reference/pack-system.md): *"The claim enumeration must be TOTAL. A
> crossing with no claim is a crossing that appears in **no report at all**."* Every declaration
> that crosses the boundary emits its own disclosed claim, and the footprint and launch banner are
> the only reports there are. **So: compress repetition, never information.** Deriving
> `agent: "claude"` from a pack named `claude` removes redundancy. Dropping a `mount` because a
> pack "conventionally" mounts something removes a claim. The first is the goal; the second is
> forbidden under every option below.

**Needs your ruling:** nothing. [`OQ-M3`](#OQ-M3) (may a manifest be pure code that returns data?) is moot: the syntax is TOML, a data syntax. [`OQ-M1`](#OQ-M1) (the model change) and [`OQ-M2`](#OQ-M2) (the syntax) were ruled 2026-10-25 by [OQ-RC19 and OQ-RC5](readable-config.md#decision-ledger): the model change goes, in TOML, in the same break as the configs. [`OQ-M4`](#8-decision-ledger) (does the user config share the language?) was decided as an implementation choice, [MN-D1](#MN-D1): it does not.

**Reads with:** [`pack-system.md`](../reference/pack-system.md) (the contribution model and the
total-enumeration rule), [`trust-paths.md`](trust-paths.md) (the origin gate and why a pack
never runs at boot), [`slots-and-contributions.md`](slots-and-contributions.md) (the role split),
and `yolo config-ref` (the user-config surface).

---

## 1. The measurement

`gzip -c <file> | wc -c` over the real files, 2026-09-20:

| File | raw | gzip | ratio |
| :--- | ---: | ---: | ---: |
| `~/.config/yolo-jail/config.jsonc` (user config) | 2 575 | 1 252 | **0.49** |
| `/workspace/yolo-jail.jsonc` (workspace config) | 6 170 | 2 643 | 0.43 |
| `packs/pi/pack.json` | 2 662 | 717 | 0.27 |
| **`packs/claude/pack.json`** | 4 162 | 1 015 | **0.24** |
| `README.md` (prose, for scale) | 17 761 | 7 104 | 0.40 |

A ratio is "how much of this file could not be predicted from the rest". Prose sits near 0.40.
The pack manifest sits at **0.24** — the file a human is *supposed* to read and edit is the most
redundant thing in the repo. That is the whole complaint, quantified.

### 1.1 Where the redundancy is

`packs/claude/pack.json`, key occurrences: `kind` **17**, `name` 8, `agent` **6**, `config` 5,
`codec` 4, `path` 4, `managed` 4, `from` 3, `at` 3, `hook` 3. Four causes:

1. **The pack repeats its own identity.** `packs/claude` already declares `bin: "claude"` on its
   `program`; six further contributions each say `agent: "claude"`.
2. **A flat tagged union.** Seventeen `kind` tags for seventeen entries — an AST serialized by
   hand, where the key could *be* the kind.
3. **Mechanism, not intent.** `managed`, `computed`, `retireOnFirstRender`, `after`, `mode: rmw`
   are instructions to the construction engine. The manifest reads like bytecode for a program
   the author never sees.
4. **No default for the common case.** `into`, `path`, `codec` repeated where a convention exists
   or the destination is implied.

## 2. Principles

- **M1. A declaration is inert data, never an effect.** The origin gate rests on it
  ([`trust-paths.md`](trust-paths.md)): a pack that could run at boot would make shipping
  content and executing code one grant. Nothing below may change that.
- **M2. The claim enumeration is total.** Compression removes repetition, never a distinct fact.
- **M3. No bespoke language.** We do not invent a config language or maintain its parser. Every
  option below is a real, maintained language or a restructured use of one already in the tree.
- **M4. Readable by someone who did not write it.** The audience for a pack file is a maintainer
  and an agent, not the author alone.
- **M5. Any computation is pure, sandboxed, and its RESULT is what is validated.** If a manifest
  may compute, the *value it produces* is schema-checked and footprinted exactly as if it had
  been written literally — so M2 survives by construction, not by trust.

## 3. What is actually free to change

The syntax and the model are separable, and it is worth being exact about which buys what:

| Lever | Syntax-independent? | Share of the redundancy |
| :--- | :--- | :--- |
| Group by kind (the key IS the kind; delete `kind`) | **yes** — applies to JSON too | large (17 tags) |
| State the agent name once per pack (no `agent` on a pack's own contributions) | **yes** | large (6 repeats) |
| Per-kind conventions for `into`/`path`/`codec` | **yes** | moderate |
| Comments, trailing commas, unquoted keys, `local` reuse | no — needs the language | moderate |
| Loops for repeated rows | no — needs computation | small, once grouped |

**So most of the win does not need a new language at all.** That is the first proposal: change
the *shape*, and see how much of the problem is left.

> ⚠ **The agent name is declared once per pack, and never derived.** An earlier draft of this
> row derived it from `program.bin`. [`OQ-D5`](slots-and-contributions.md#7-decision-ledger) ruled
> otherwise on 2026-09-20: a pack provides zero or one agents and must name the one it provides,
> which is [`OQ-BA2`](../reference/agent-briefings.md#oq-ba2)'s refusal of a derived identity
> restated at pack scope. Neither the pack's `name` nor its bin could be the source anyway:
> `packs/omp` is `name: "omp"` with `bin: "oh-omp"`, and `oh-omp` is the agent its briefings and
> skills declare. The saving in the table is the same either way, since it comes from deleting
> the per-contribution repeats.

**The user and workspace config stay JSONC, whatever a manifest ends up written in**
([MN-D1](#MN-D1)). `yolo-jail.jsonc` and `~/.config/yolo-jail/config.jsonc` are settings, with no
claims to enumerate, so the total-claim rule this doc protects does not reach them, and moving them
would be a proposal of its own.

## 4. The options

### A. JSON (JSONC), restructured — the model change only

Keep the syntax; change the model. Group by kind, derive the identity, keep conventions:

```jsonc
{
  "name": "pi",
  "agent": "pi",  // declared once per pack (OQ-D5)
  "program": { "bin": "pi", "via": "npm", "package": "@earendil-works/pi-coding-agent" },
  "exposes": [ { "name": "extensions", "into": ".pi/agent/extensions", "accepts": "tree" } ],
  "briefing": { "into": ".pi/agent/AGENTS.md", "after": "host:.pi/agent/AGENTS.md" },  // a destination: an exposes entry under OQ-D3
  "config": [ /* surfaces */ ],
  "state": [ { "at": ".pi", "scope": "workspace" } ]
}
```

- *Pros:* no parser, no dependency, inert by construction, toolable, zero trust change. Removes
  the `kind` tags and the repeated identity — the two largest sources.
- *Cons:* JSON still has no comments or trailing commas in its strict form (JSONC does), and no
  reuse for genuinely repeated rows.

*Illustrative only:* the `exposes` list form is
[`slots-and-contributions.md`](slots-and-contributions.md)'s, whose [`OQ-D4`](slots-and-contributions.md#7-decision-ledger) ruled the field names
provisional. Its [`OQ-D3`](slots-and-contributions.md#7-decision-ledger) ruled on 2026-09-20 that `briefing` and
`skills` destinations become `exposes` entries too, so the `briefing` line above is shown in
today's shape only. What those slots are called is still open there, as
[`OQ-D8`](slots-and-contributions.md#7-decision-ledger).

### B. Data-only Lua (reuse the sandbox already in the tree)

The manifest is a Lua chunk that **returns a table**, run in the existing pure sandbox
([`internal/agentcfg/luahook`](../../internal/agentcfg/luahook)) — no `os`, no `io`, no `require`,
no effects. It may use locals and loops to remove repetition; it may not perform an effect.

```lua
return {
  name = "pi",
  program = { bin = "pi", via = "npm", package = "@earendil-works/pi-coding-agent" },
  exposes = { extensions = ".pi/agent/extensions", skills = ".pi/agent/skills" },
  config = {
    { name = "settings", path = "~/.pi/agent/settings.json", codec = "json", readsHost = true },
    { name = "models",   path = "~/.pi/agent/models.json",   codec = "json", mode = "computed" },
  },
}
```

- *Pros:* already vendored (`gopher-lua`, used for `derive.lua`); comments; trailing commas; keys
  without quotes; `local` reuse for repeated fragments; loops for the repeated-row case. The
  terseness is real — the file above is ~⅓ the bytes of the JSON it replaces.
- *Cons:* **the manifest becomes code that is executed to be read** — a trust-model change, even
  sandboxed and pure (see [OQ-M3](#OQ-M3)). It also invites *computing* declarations, which M5
  permits only if the result is validated and footprinted.

### C. Starlark

A Python-like, hermetic, deterministic config language (Bazel/Buck), with a Go implementation.

- *Pros:* **purpose-built for exactly this** — no I/O, no non-determinism, bounded evaluation,
  readable by anyone who has seen Python. Comments, functions, `load()` for sharing fragments.
- *Cons:* a new dependency; a second language in the tree beside `derive.lua`'s Lua; and it is
  still "config is code", with the same M3/M5 question as B.

### D. Jsonnet / CUE / Dhall

Real config languages: Jsonnet (JSON superset, functions/imports), CUE (unification, constraints),
Dhall (total, typed).

- *Pros:* Jsonnet is a drop-in mental model for JSON; CUE can *validate* as well as declare; Dhall
  is total by construction.
- *Cons:* heaviest dependencies, least familiar to a pack author, and each is a large new surface
  to learn for a file that mostly lists destinations. Overpowered for a manifest that should be
  data.

### E. YAML / TOML — syntax swap, no model change

- *Pros:* YAML anchors give the reuse in [§3](#3-what-is-actually-free-to-change) without a
  language; TOML is pleasant for the flat parts.
- *Cons:* **they only move the same repetition into another file**, which is the user's own
  objection. YAML's implicit typing and anchors-as-behaviour are their own footguns. Neither
  changes the model, so neither removes the `kind` tags or the repeated identity. **Not a fix on
  its own.**

## 5. Evaluation

| | A JSON-restructured | B Lua (data) | C Starlark | D Jsonnet/CUE/Dhall | E YAML/TOML |
| :--- | :--- | :--- | :--- | :--- | :--- |
| Removes `kind` + identity repeats | **yes** | yes | yes | yes | no (model unchanged) |
| Comments / reuse / terseness | partial (JSONC) | **yes** | **yes** | **yes** | anchors only |
| New dependency | none | none (vendored) | one (Go) | heavy | none |
| Inert by construction | **yes** | needs the sandbox + M5 | needs the runner + M5 | needs the evaluator + M5 | yes |
| Readable by a non-author | good | good | **best** | fair | fair |
| Trust-model change | none | **yes** | **yes** | yes | none |
| Wins on the compression metric | **largest share** | rest | rest | rest | **none** |

## 6. Recommendation

**Do A now; treat B or C as a second, separately-ruled step.**

1. **A first, because it is syntax-free and carries the two biggest sources** — the `kind` tags
   and the repeated identity. If the resulting file reads well, stop: no new language, no trust
   change, no migration risk.
2. **Then re-measure.** If the file is still redundant, the remaining cause is *reuse of repeated
   fragments*, and only a language (B or C) fixes that. Lua (B) is the smaller step — zero new
   dependencies and the sandbox is already written — but it moves the manifest from data to pure
   code ([OQ-M3](#OQ-M3)). Starlark (C) is the better-designed fit and a real new dependency.
3. **E is rejected as a standalone answer** — it is the same data in another syntax, which is the
   complaint, not the cure. It may ride along inside A if JSONC's gaps bite.

## 7. Questions, all closed

1. **[OQ-M1](#OQ-M1) and [OQ-M2](#OQ-M2) are ruled**, in the [decision ledger](#8-decision-ledger): the model change goes,
   in TOML, in the same break as the configs ([`pack-manifests.md`](pack-manifests.md)).

2. **[OQ-M3](#OQ-M3) is moot**, in the [decision ledger](#8-decision-ledger): the syntax is TOML, so no manifest
   is executed to be read. M5's condition stands ready if a code syntax is ever proposed again.

#### Background to [OQ-M1](#OQ-M1)

"The model change" is
[§3](#3-what-is-actually-free-to-change)'s three syntax-independent levers: group entries by kind,
so `"program": {…}` replaces `{"kind": "program", …}` in a list; state the pack's agent once, which
is [`OQ-D5`](slots-and-contributions.md#7-decision-ledger)'s ruled rule and is not built; and give each kind
defaults for `into`, `path` and `codec`.

⚠ **Added 2026-10-05: what an older yolo does with each lever**
([`pack-conventions.md` §2](pack-conventions.md#2-what-an-older-yolo-does-with-a-convention)).
Grouping by kind replaces every top-level key, so a yolo with the launch's skip reads a regrouped
manifest as an empty pack, naming each key it ignored. Defaults built as omissions are refused,
since a missing required field stays a problem. Every release through v0.11.1 refuses either. That
conflicts with this question's premise, *"We don't need transitions"*, while hosts run different
yolos. The same review handed this question a fourth candidate lever, a posture naming a surface
instead of restating it, with its measured costs
([`pack-conventions.md` §7](pack-conventions.md#7-considered-and-not-proposed)).

#### Background to [OQ-M2](#OQ-M2)

⚠ **MEASURED 2026-09-22, and the result is that this question should be DISSOLVED rather than
answered on the stated rule.** The re-measure was takeable with no build — I restructured
`packs/claude/pack.json` by hand per [§4](#4-the-options)'s option A, machine-checked both variants fact-preserving, and
canonicalised them identically to the baseline:

| rendering | raw | gzip | ratio |
| :--- | ---: | ---: | ---: |
| baseline | 4142 | 1006 | 0.243 |
| A1 (strict option A) | 3313 | 942 | 0.284 |
| A2 (+ keyed by discriminator) | 2838 | 879 | 0.310 |
| data-only Lua, A2's facts | 2227 | 841 | 0.378 |

Neither restructuring reaches this doc's ~0.40 threshold, so the rule as written would answer
"get a language". **Three measurements say the rule is unsound:**

- **Prose at the manifest's size is 0.49, not 0.40.** The 0.40 came from a 17 761-byte README, and
  the ratio rises with size, so the target was never size-matched to what it judges.
- **`jq -c` alone reaches 0.368** — minifying the baseline, with zero model change and zero
  readability gain, gets 92% of the way to the target. 44% of the baseline is indent whitespace.
- **The numerator barely moves**: A1 drops 20% of raw bytes and only **6.4%** of gzip bytes. That
  is the signature of deleting *already-predicted* redundancy — so the ratio necessarily RISES
  whenever you remove better-than-average-compressible bytes.

> [!WARNING]
> **Compression ratio is orthogonal to the complaint it was chosen to quantify.** The objection is
> about what a HUMAN must read; gzip had already predicted the repetition, which is why deleting
> it moves the number the wrong way. Direction is robust across xz/bzip2 and levels 1–9; the
> THRESHOLD is not. Do not re-derive a byte-ratio gate for this question.

## 8. Decision ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="OQ-M1"></a>[OQ-M1](#OQ-M1) | **Yes: grouped by kind, the pack's identity stated once, in the same break as the configs.** Ruled 2026-10-25 as lever P3 of [OQ-RC19](readable-config.md#decision-ledger), landing under [OQ-RC5](readable-config.md#decision-ledger); the `exposes` split is not the break it waits for | 2026-10-25 | [`pack-manifests.md` §4](pack-manifests.md#4-flags-that-add-and-a-model-said-once) | ✅ by 2026-10-30: every shipped pack manifest is a `pack.toml` ([`pack-manifests.md`](pack-manifests.md)) |
| <a id="OQ-M2"></a>[OQ-M2](#OQ-M2) | **TOML, with the model change.** Ruled 2026-10-25 by [OQ-RC2](readable-config.md#decision-ledger) for every file a person writes, manifests included; option E was no fix on its own, and the model change ([OQ-M1](#OQ-M1)) is what rides with it | 2026-10-25 | [`pack-manifests.md`](pack-manifests.md) | ✅ by 2026-10-30: every shipped pack manifest is a `pack.toml` ([`pack-manifests.md`](pack-manifests.md)) |
| <a id="OQ-M3"></a>[OQ-M3](#OQ-M3) | **Moot: a manifest is a literal TOML file, so nothing is executed to be read.** The question mattered only if [OQ-M2](#OQ-M2) picked a code syntax; it picked TOML ([OQ-RC2](readable-config.md#decision-ledger)). Closed 2026-10-25 when the maintainer accepted the readable-config redesign; a code syntax proposed again reopens it with M5 as the condition | 2026-10-25 | [`pack-manifests.md`](pack-manifests.md) | nothing to build |
| OQ-M4 | **No: the user and workspace config keep JSONC whatever [OQ-M2](#OQ-M2) rules.** Decided as an implementation choice, [MN-D1](#MN-D1), reversible; moving them would be a proposal of its own | 2026-09-30 | [§3](#3-what-is-actually-free-to-change) | nothing to build |
| <a id="MN-D1"></a>MN-D1 | *Implementation decision, [OQ-M4](#8-decision-ledger).* **The user and workspace config keep JSONC, whatever [OQ-M2](#OQ-M2) rules for manifests.** The user config is settings, with no claims to enumerate, so the rule this doc exists to protect ([M2](#2-principles)) does not reach it. yolo already deleted the one scripted user config it had, `config.lua`, because it had no user ([`OQ-LT1`](../reference/pack-system.md#oq-lt1)). And coupling the two would make the manifest ruling wait on a rewrite of every user's config. Reversible: a proposal to move the user config would start from the manifest's ruling rather than wait for it. **Reversed 2026-10-25:** the configs move to TOML ([OQ-RC2](readable-config.md#decision-ledger)) | 2026-09-30 | [§3](#3-what-is-actually-free-to-change) | nothing to build |
