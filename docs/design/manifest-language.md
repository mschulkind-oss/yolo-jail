---
title: "The manifest language: declarations stay inert data, and the syntax should stop fighting us"
date: 2026-09-20
status: in-review
tags: [packs, manifest, config, format, lua, starlark, design]
summary: "A pack manifest compresses to a quarter of its bytes — more redundant than the repo's own prose — because it is a flat tagged union that repeats the pack's identity and the `kind` tag on every entry, and spells mechanism rather than intent. The syntax and the MODEL are two independent decisions, and the model change (group by kind, state the agent name once per pack, keep distinct facts explicit) carries most of the gain under any syntax. This doc ARGUES both and rules neither yet: what a manifest may be written in (JSON restructured, data-only Lua, Starlark, or the heavier config languages), and the rule that compression may remove repetition but never a claim."
vantage:
  status-chip: true
---

# The manifest language: declarations stay inert data, and the syntax should stop fighting us

**Status:** DESIGN, 2026-09-20. Nothing built; four questions open. Re-checked against the tree
and the sibling rulings 2026-09-24: the identity lever in [§3](#3-what-is-actually-free-to-change)
now follows [`OQ-D5`](./slots-and-contributions.md#OQ-D5) (declared once per pack, never derived),
which that doc ruled on 2026-09-20. Sibling to
[`slots-and-contributions.md`](./slots-and-contributions.md) — that one fixes the manifest's
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

**Needs your ruling:** [`OQ-M1`](#OQ-M1) (do the model change?), [`OQ-M2`](#OQ-M2) (which syntax?), [`OQ-M3`](#OQ-M3) (may a manifest be pure code that returns data?), [`OQ-M4`](#OQ-M4) (does the user config share the language?).

**Reads with:** [`pack-system.md`](../reference/pack-system.md) (the contribution model and the
total-enumeration rule), [`trust-paths.md`](./trust-paths.md) (the origin gate and why a pack
never runs at boot), [`slots-and-contributions.md`](./slots-and-contributions.md) (the role split),
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
  ([`trust-paths.md`](./trust-paths.md)): a pack that could run at boot would make shipping
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
> row derived it from `program.bin`. [`OQ-D5`](./slots-and-contributions.md#OQ-D5) ruled
> otherwise on 2026-09-20: a pack provides zero or one agents and must name the one it provides,
> which is [`OQ-BA2`](../reference/agent-briefings.md#oq-ba2)'s refusal of a derived identity
> restated at pack scope. Neither the pack's `name` nor its bin could be the source anyway:
> `packs/omp` is `name: "omp"` with `bin: "oh-omp"`, and `oh-omp` is the agent its briefings and
> skills declare. The saving in the table is the same either way, since it comes from deleting
> the per-contribution repeats.

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
[`slots-and-contributions.md`](./slots-and-contributions.md)'s, whose [`OQ-D4`](./slots-and-contributions.md#OQ-D4) ruled the field names
provisional. Its [`OQ-D3`](./slots-and-contributions.md#OQ-D3) ruled on 2026-09-20 that `briefing` and
`skills` destinations become `exposes` entries too, so the `briefing` line above is shown in
today's shape only. What those slots are called is still open there, as
[`OQ-D8`](./slots-and-contributions.md#OQ-D8).

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

## 7. Open questions

1. 💬 **OQ-M1: Do the syntax-free model change first?**

   <!-- vantage: oq id=OQ-M1 leaning="Yes. Grouping by kind and stating the pack's identity once are syntax-independent and are the two largest sources of redundancy." -->

   _Leaning:_ Yes — it is independent of the syntax decision, needs no dependency, and removes the
   bulk of the measured redundancy. The identity half of it has a recorded shape already:
   [`OQ-D5`](./slots-and-contributions.md#OQ-D5) ruled that a pack declares its agent once, so
   what this question still decides is whether to make the change, and when. ⚠ **The re-measure this used to require was TAKEN 2026-09-22
   without building anything, and it retired the gzip metric rather than answering with it** — see
   [OQ-M2](#OQ-M2). So M1 no longer gates on a measurement, and M2 no longer gates on M1.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-M2: Which syntax — JSON restructured, data-only Lua, or Starlark?**

   <!-- vantage: oq id=OQ-M2 leaning="JSON restructured if it reads well; else data-only Lua (already vendored); Starlark only if we accept a new dependency for a purpose-built hermetic language." -->

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

   _Leaning:_ **Strike the re-measure precondition and decide on readability, not ratio.** If a
   language is still wanted, Lua remains the cheapest (vendored, familiar, comments + reuse) and
   Starlark the best-designed at the cost of a dependency — but the measurement no longer argues for
   either.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-M3: May a manifest be pure code that RETURNS data — executed to be read?**

   <!-- vantage: oq id=OQ-M3 leaning="Yes, if the sandbox is pure and the RESULT is validated and footprinted, so the total-claim rule survives by construction rather than by trust." -->

   _Leaning:_ Yes — with M5 as the condition: the sandbox has no I/O and no effects, and the *value*
   it returns goes through the same schema check and footprint as a literal manifest. This is the
   one genuine trust-model change in the doc and it should be ruled explicitly, not slipped in with
   a syntax choice. `derive.lua` already establishes the precedent.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-M4: Does the user/workspace config (`yolo-jail.jsonc`) share the language?**

   <!-- vantage: oq id=OQ-M4 leaning="Not necessarily. The user config is settings, not declarations; it may keep a syntax that is read-only-data even if the manifest moves." -->

   _Leaning:_ Not necessarily — the user config is *settings* (no claims to enumerate), so it may
   keep a plain data syntax even if the manifest moves. Sharing is a convenience, not a
   requirement, and coupling the two decisions would slow the manifest's.

   **Answer:**
   > _(empty — fill in when decided)_

## 8. Decision ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| — | None settled yet | — | — | — |
| **OQ-M1** | — | — | — | — |
| **OQ-M2** | — | — | — | — |
| **OQ-M3** | — | — | — | — |
| **OQ-M4** | — | — | — | — |
