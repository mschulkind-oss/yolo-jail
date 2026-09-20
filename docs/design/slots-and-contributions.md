---
title: "Slots are not kinds: a pack accepts content through a named exposure, addressed by agent"
date: 2026-09-20
status: in-review
tags: [packs, manifest, declarations, slots, audience, design]
summary: "A `kind` names what a pack CONTRIBUTES. A destination names what an agent pack ACCEPTS, which is the opposite role — so it is not a contribution and should not be a kind or a field shape within one. Today `briefing`, `skills` and `files` each carry both roles and tell them apart with `agent` vs `agents`, a singular/plural flag that already produced a real layout bug (`files` required `from`, so a destination had to carry content). This splits the manifest into two axes — `contributes` (kinds) and `exposes` (named slots) — and addresses a slot by the AGENT (the `bin` name audiences already key on), never the pack slug, so content survives swapping which pack supplies that agent."
vantage:
  status-chip: true
---

# Slots are not kinds: a pack accepts content through a named exposure, addressed by agent

**Status:** DESIGN, 2026-09-20. Nothing built. Companion to
[`pi-pack-extensions.md`](./pi-pack-extensions.md), whose slot shape this supersedes.

> **In short.** One word — `kind` — is doing two opposite jobs. A `kind` names a **contribution**,
> something a pack *supplies* (`program`, `config`, `state`, `loophole`). But `briefing`, `skills`
> and `files` also use it for a **destination** — something an agent pack *accepts* — and the two
> are told apart by `agent` (singular, a destination) versus `agents` (plural, an audience). A
> singular/plural flag is a bad way to say "which direction is this", and it already bit: because
> `files` required `from` on every contribution, a *destination* had to carry content, which put
> the owning pack's tree at the slot root and produced a nested-mount conflict
> (the alias-root account in [`pi-pack-extensions.md`](./pi-pack-extensions.md#10-decision-ledger)).
>
> **The shape.** Two axes instead of one field-shape inside one kind. `contributes` stays what a
> pack **supplies**; a new `exposes` list is what a pack **accepts** — a named slot with a landing
> path and an accepted shape. A contribution targets a slot by name, and the name is the
> **agent**, not the pack, so a content pack that addresses `pi` keeps working whichever pack
> supplies `pi`.

**Why it matters.** Three things. It removes the flag that produced the bug — a slot is a
declaration, not a contribution with the fields reversed. It makes the address portable: content
names an **agent**, so the pack that supplies that agent is replaceable without touching the
content. And it is the vocabulary lever for the manifest's readability problem — most of what
makes a pack file hard to read is guessing which of several shapes a `kind` is in.

**Needs your ruling:** [OQ-D1](#OQ-D1) (is `exposes` a second axis or a kind?),
[OQ-D2](#OQ-D2) (is the slot address the agent `bin` name?),
[OQ-D3](#OQ-D3) (how far does the split reach — `files` only, or `briefing`/`skills` too?),
[OQ-D4](#OQ-D4) (field names).

**Reads with:** [`pack-system.md`](../reference/pack-system.md) (the `contributes` vocabulary),
[`briefing-audiences.md`](./briefing-audiences.md) (the `agent`/`agents` mechanism this splits
apart), [`stringly-typed-references-principle.md`](../reference/stringly-typed-references-principle.md)
(what a name may reference), [`pi-pack-extensions.md`](./pi-pack-extensions.md) (the concrete
case, and the bug that forced this), [`manifest-language.md`](./manifest-language.md) (the
sibling concern — the manifest's *surface*, or how many bytes say one fact).

---

## 1. The problem, stated once

A manifest's `kind` field currently answers **two different questions**, and the answer is
recovered from a field *shape*:

| Written today | What it is | How you can tell |
| :--- | :--- | :--- |
| `{kind:"skills", agent:"claude", into:".claude/skills"}` | a **destination** — where Claude reads skills | `agent` (singular) + `into` |
| `{kind:"skills", agents:["claude"], from:"skills"}` | a **contribution** — content for Claude | `agents` (plural) + `from` |

Two problems, and the second is the one that cost us:

1. **`agent` vs `agents` is a singular/plural distinguishing two features.** Nothing about the
   words says "identity vs audience"; a reader has to be told, and a one-letter difference is the
   classic way a subtle distinction gets typed wrong in silence.
2. **The shape leaks a requirement onto the wrong role.** `files` required `from` on *every*
   contribution, because a files tree has no conventional source. A destination is a `files`
   contribution, so a destination was *required to carry content* — and the agent pack's own tree
   landed at the slot root, with addressed packs nested inside a read-only mount of it. A rule
   ("`from` is forbidden when `agent` is present") was the patch; **a rule you need to say which
   of two things a declaration is means it is two declarations.**

## 2. What a kind is, and what a slot is

**A `kind` names a contribution — what a pack supplies.** `program`, `config`, `state`, `mount`,
`env`, `loophole`, `service`, `provider`, `profile`, `briefing`, `skills`, `files`. This is the
sense [`pack-system.md`](../reference/pack-system.md) already uses; nothing here changes it.

**A slot is what a pack accepts**, and accepting is not supplying. It is the other end of the
same wire, so it is a different axis, not a different value of the same one:

```jsonc
// the pack that supplies the agent — it ACCEPTS content
"exposes": [
  { "name": "extensions", "into": ".pi/agent/extensions", "accepts": "tree" }
]

// any content pack — it SUPPLIES content, and names the recipient
"contributes": [
  { "kind": "files", "to": "pi/extensions", "from": "pi-extensions" }
]
```

The owner declares `into` — where **it** reads is its business, and nobody else can keep that
current. The contributor declares only the recipient. **`agent` and `agents` both disappear**: a
slot is never addressed by an audience (it *is* the receiving end), and a contribution is never a
slot.

### 2.1 What a slot declares

- **`name`** — what this pack accepts. Local to the agent it belongs to.
- **`into`** — the landing path, the owner's own fact.
- **`accepts`** — the **shape** it takes: an opaque tree, a single file, a JSON document, a set of
  *things*. This is the piece `files` got wrong by implication: **a slot is not named after its
  transport.**

### 2.2 Why the receiver, not the sender, names the shape

The recipient's derive decides what the content *becomes* — it may write files, fold into a
config value, register a tool. So the content **arrives** as bytes and does not necessarily
**leave** as files, and a slot called "files" misnames the capability. The slot says what it
accepts; what it does with it is the pack's.

## 3. The address: the agent, never the pack

**A contribution addresses a slot by `(agent, name)`, and `agent` is the `bin` name** — the
launcher command, the same namespace `-p <name> -- <bin>`, `use_profiles.<cli>` and a config
surface's `agent` already key on. Spelled `pi/extensions`.

**Why not the pack slug.** The pack that supplies an agent should be replaceable — a `pi-fork`
pack tomorrow, a different vendor's pack the day after — and a content pack that ships Pi
extensions must keep working across that swap. If the address named the *pack*, every content pack
would have to change when the agent's pack did, for a fact it has no way to keep current. Naming
the **agent** is the same rule as naming an audience, for the same reason.

**Two packs supplying one agent is then a resolution question, not an addressing one.** Whichever
pack is selected declares `exposes: [{name:"extensions", …}]` for `pi`; `to: "pi/extensions"`
resolves against whatever owns `pi` this launch. If two selected packs both declare `pi`, that is
the existing one-owner collision (`AgentNameCollisions`), fatal and named — not something the
contributor has to disambiguate.

**The name is a stringly-typed reference**, so it obeys
[`stringly-typed-references-principle.md`](../reference/stringly-typed-references-principle.md):
an unmatched `to` is refused at load with a fix naming the slots that exist, and the vocabulary is
the *selected* packs, not the universe.

## 4. The split, applied

- **`files`** — a contribution ships a tree (`from`, and `to` when addressed). A slot is an
  `exposes` entry. `from` is never required on a slot, because a slot ships nothing.
- **`skills` and `briefing`** — these have the *same* conflation today, so the rule applies to
  them too: `{agent, into}` becomes an `exposes` entry, `{agents, from}` a contribution. The
  conventional source (`AGENTS.md`, `skills/`) is a property of the *contribution*, not the slot.
- **Everything else** (`program`, `config`, `state`, `mount`, `loophole`, …) is already
  contribution-only; nothing moves.

## 5. What this costs

- A manifest gains a second top-level list, and the three content kinds lose a field shape.
- Every pack that declares a destination (all six shipped agent packs, at least) migrates, and
  the two mechanisms that read destinations — `packload`'s borrowing and `cli/run`'s mount
  resolution — change their input.
- It **supersedes the `files` work already landed** (`pi-pack-extensions` slices 1–3:
  `agent`/`agents` on `files`, `from` forbidden on a destination). That code is correct for the
  shape it encodes and wrong for this one; it is a rework, not a revert-and-forget.

## 6. Open questions

1. 💬 **OQ-D1: Is a slot a second axis (`exposes`) or its own kind?**

   <!-- vantage: oq id=OQ-D1 leaning="A second axis. A kind names a contribution; a slot is not one, and making it a kind would put the receiving role inside the supplying vocabulary." -->

   _Leaning:_ A second top-level axis, `exposes`. A kind is a contribution by definition; the
   receiving end is not a contribution, and giving it a kind re-creates the conflation one level
   up.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-D2: Is the slot address `(agent, name)` keyed on the `bin` name, or on the pack?**

   <!-- vantage: oq id=OQ-D2 leaning="The agent/bin name — so content survives swapping which pack supplies that agent." -->

   _Leaning:_ The **agent** (`bin`) name, spelled `<bin>/<slot>`, for the reason in
   [§3](#3-the-address-the-agent-never-the-pack). A pack-keyed address would couple every content
   pack to a supplier it must not have to know.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-D3: Does the split reach `briefing` and `skills`, or only `files`?**

   <!-- vantage: oq id=OQ-D3 leaning="All three. The conflation is identical, and fixing files alone leaves two kinds with the flag this doc exists to delete." -->

   _Leaning:_ All three, eventually — the `agent`/`agents` flag is the thing being deleted, and it
   lives in all three kinds. `files` is the forcing case; the other two follow.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-D4: Are the field names `exposes` / `to` / `accepts` right?**

   <!-- vantage: oq id=OQ-D4 leaning="Provisional. 'exposes'/'accepts' read as the receiving end; 'to' is the shortest thing that is not 'into'." -->

   _Leaning:_ Provisional. The names must not collide with an existing key, and `to` must not be
   confusable with `into` (the owner's landing path). Naming is cheap to change now and expensive
   after packs adopt it.

   **Answer:**
   > _(empty — fill in when decided)_

## 7. Decision ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| — | None settled yet | — | — | — |
| **OQ-D1** | — | — | — | — |
| **OQ-D2** | — | — | — | — |
| **OQ-D3** | — | — | — | — |
| **OQ-D4** | — | — | — | — |
