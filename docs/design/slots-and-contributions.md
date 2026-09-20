---
title: "Slots are not kinds: a pack accepts content through a named exposure, addressed by agent"
date: 2026-09-20
status: accepted
tags: [packs, manifest, declarations, slots, audience, design]
summary: "A `kind` names what a pack CONTRIBUTES. A destination names what an agent pack ACCEPTS, which is the opposite role — so it is not a contribution and should not be a kind or a field shape within one. Today `briefing`, `skills` and `files` each carry both roles and tell them apart with `agent` vs `agents`, a singular/plural flag that already produced a real layout bug (`files` required `from`, so a destination had to carry content). This splits the manifest into two axes — `contributes` (kinds) and `exposes` (named slots) — and addresses a slot by the AGENT (the `bin` name audiences already key on), never the pack slug, so content survives swapping which pack supplies that agent."
vantage:
  status-chip: true
---

# Slots are not kinds: a pack accepts content through a named exposure, addressed by agent

**Status:** DECIDED, 2026-09-20. **Every ruling is in; nothing is built.**
Companion to [`pi-pack-extensions.md`](./pi-pack-extensions.md), whose slot shape this supersedes.
⚠ Review moved this doc twice in one day: the [OQ-D3](#OQ-D3) leaning was WITHDRAWN (it rested on
the `agent`/`agents` flag living in all three kinds, which the corpus does not bear out), then
[OQ-D5](#OQ-D5) was FILED when writing the concrete replacement line showed that
[§2](#2-what-a-kind-is-and-what-a-slot-is)'s *"`agent` and `agents` both disappear"* was asserted
rather than shown — and then RULED, which unblocked [OQ-D3](#OQ-D3) in the opposite direction from
where it had been left an hour earlier.

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

**Needs your ruling:** **None** — all five were ruled 2026-09-20. See
[§7](#7-decision-ledger).

**Reads with:** [`pack-system.md`](../reference/pack-system.md) (the `contributes` vocabulary),
[`briefing-audiences.md`](./briefing-audiences.md) (the `agent`/`agents` mechanism this splits
apart), [`stringly-typed-references-principle.md`](../reference/stringly-typed-references-principle.md)
(what a name may reference), [`pi-pack-extensions.md`](./pi-pack-extensions.md) (the concrete
case, and the bug that forced this), [`pi-pack-extensions-plan.md`](./pi-pack-extensions-plan.md)
(the build hand-off carrying the superseded slices 1–3),
[`pi-extension-lifecycle.md`](./pi-extension-lifecycle.md) (the sibling fetch axis),
[`manifest-language.md`](./manifest-language.md) (the
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
`env`, `loophole`, `service`, `provider`, `profile`, `briefing`, `skills`, `files` — the last three
being the ones that *also* carry a destination today, which is the conflation this doc splits
apart. That is the sense [`pack-system.md`](../reference/pack-system.md) already uses.

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

The recipient's derive decides what the content *becomes*, so content **arrives** as bytes and does
not necessarily **leave** as files — which is why a slot is not named after its transport. The
slot says what it accepts; what it does with it is the pack's.

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
- Every pack that declares a destination (all seven shipped agent packs: `agy`, `claude`,
  `codex`, `copilot`, `omp`, `opencode`, `pi`) migrates, and the two mechanisms that read
  destinations — `packload`'s borrowing (`inferrableKinds`, `carriesFor`) and `cli/run`'s mount
  resolution (`packFilesTargets`, `internal/cli/run/packfiles.go`) — change their input.
- It **supersedes the `files` work already landed** (`pi-pack-extensions` slices 1–3:
  `agent`/`agents` on `files`, `from` forbidden on a destination). That code is correct for the
  shape it encodes and wrong for this one; it is a rework, not a revert-and-forget.

⚠ **THE TWO BULLETS ABOVE PREDATE [OQ-D5](#OQ-D5) AND UNDERSTATE THE COST.** They scope the
change to destinations, and D5 moved the *identity* to pack scope — which reaches the **config
surfaces**, because `agent` there is the same field name holding the same string
([`packdecl`](../../internal/packdecl/contributes.go) says so at the field). Measured 2026-09-20:
`Agent` is read in **19 non-test files** across `internal/agentcfg` and `internal/entrypoint`
(compose, the state render, the host render and its revert, the overlay prune, the adoption
archive, the surface manifest) — not the two mechanisms this section names.

**So this is a pipeline, not a fan-out**, and the order is forced by what depends on what:

| Slice | Changes | Depends on |
| :--- | :--- | :--- |
| **1 — schema** | `internal/packdecl`: the `exposes` axis, `to` on a contribution, pack-scope `agent`, and the refusals that replace `agent`/`agents` on a contribution | — |
| **2 — resolution** | `internal/packload` borrowing, `internal/cli/run/packfiles.go` mount resolution | 1 |
| **3 — surfaces** | `internal/agentcfg/manifest` and its readers take the pack-scope identity | 1 |
| **4 — manifests** | the seven agent packs, and the raw `into` path a content pack writes today | 1–3 |

Slice 4 is the one that pays: it deletes 31 of the 38 identity restatements
([OQ-D5](#OQ-D5)) and removes the only place a content pack hardcodes another pack's layout.

## 6. Open questions

1. ✅ **OQ-D1: Is a slot a second axis (`exposes`) or its own kind?**

   <!-- vantage: oq id=OQ-D1 leaning="A second axis. A kind names a contribution; a slot is not one, and making it a kind would put the receiving role inside the supplying vocabulary." -->

   **Answer (2026-09-20):**
   > **A second axis.** A `kind` names a contribution; a slot is not one, and making it a kind
   > would put the receiving role inside the supplying vocabulary — the conflation this doc
   > exists to delete, one level up.

2. ✅ **OQ-D2: Is the slot address `(agent, name)` keyed on the `bin` name, or on the pack?**

   <!-- vantage: oq id=OQ-D2 leaning="The agent/bin name — so content survives swapping which pack supplies that agent." -->

   **Answer (2026-09-20):**
   > **The agent (`bin`) name** — so content survives swapping which pack supplies that agent. A
   > pack-keyed address would couple every content pack to a supplier it must not have to know.

   ⚠ **This ruling RATIFIES what two of the three kinds already do, which was not stated when the
   question was written.** Measured 2026-09-20 across `packs/*/pack.json`: every `briefing` and
   every `skills` contribution in all seven agent packs already declares `agent: "<bin>"` beside
   its `into` — `claude`, `codex`, `copilot`, `opencode`, `pi`, `agy`, and `oh-omp` (the `omp`
   pack's bin, which is not its slug, and is itself the argument). So the agent-keyed address is
   not a new design; it is the existing one, unnamed. Only `files` lacks it. That reframes
   [OQ-D3](#OQ-D3) and is why its leaning changed.

3. ✅ **OQ-D3: Does the split reach `briefing` and `skills`, or only `files`?**

   <!-- vantage: oq id=OQ-D3 leaning="All three. The conflation is identical, and fixing files alone leaves two kinds with the flag this doc exists to delete." -->

   ⚠ **The original leaning ("all three, eventually") rested on a premise the tree does not
   support, and it is withdrawn.** It said the `agent`/`agents` flag "lives in all three kinds",
   implying all three carry both roles. Measured 2026-09-20:

   - **No shipped manifest uses `agents` (plural) at all** — zero occurrences across
     `packs/*/pack.json`. The audience half exists in the schema and in the built-in
     `configuring-the-jail` skill as a *user-facing* example (`"agents": ["pi"]` for a personal or
     shared pack). Nothing yolo ships writes one.
   - **Every `briefing` and `skills` contribution is unambiguously a destination** — `agent` +
     `into`, seven packs, fourteen contributions, no exceptions.
   - **The one shipped `files` contribution declares neither** (`pi`'s extension: `from` + `into`).

   So the conflation is **real in the schema and absent from the corpus** for `briefing` and
   `skills`. For those two, the split is a **rename of a shape that is already unambiguous in
   practice**: seven packs migrate, fourteen contributions change spelling, and no behaviour moves.
   `files` is different in kind — that is where the shape actually broke, and where the alias-root
   bug came from.

   **⚠ AND THE MIGRATION CANNOT BE PRICED YET, WHICH IS THE REAL STATE OF THIS QUESTION.** Asked
   point-blank what the new spelling would be, this doc cannot answer. Today's line, in all seven
   agent packs:

   ```jsonc
   // packs/claude/pack.json — today
   { "kind": "briefing", "agent": "claude", "into": ".claude/CLAUDE.md" }
   ```

   [§2](#2-what-a-kind-is-and-what-a-slot-is) says a slot carries `name` / `into` / `accepts` and
   that **"`agent` and `agents` both disappear"** — but [§3](#3-the-address-the-agent-never-the-pack)
   addresses a slot as `(agent, name)`, so the agent has to come from *somewhere*. Three spellings
   satisfy [§2](#2-what-a-kind-is-and-what-a-slot-is)'s example and they are not the same proposal:

   | Candidate | The line becomes | What it costs |
   | :--- | :--- | :--- |
   | **A — the slot carries it** | `{"name": "briefing", "agent": "claude", "into": ".claude/CLAUDE.md", "accepts": "concat"}` | `agent` does **not** disappear; it is renamed in place, and [§2](#2-what-a-kind-is-and-what-a-slot-is)'s claim is false as written |
   | **B — the pack declares its identity once** | pack-level `"agent": "claude"`, then `{"name": "briefing", "into": …}` | The repetition goes, which is [`manifest-language.md`](./manifest-language.md)'s concern exactly — but it is a second structural change riding on this one |
   | **C — derived from the `program` bin** | `{"name": "briefing", "into": …}`, agent inferred | **Mechanically available and ruled out.** Measured 2026-09-20: the declared `agent` equals the pack's own `program` bin in **7 of 7** agent packs, `oh-omp` included — so the derivation would work. [`OQ-BA2`](./briefing-audiences.md#decision-ledger) forbade it anyway: nothing in the `-p` chain derives an identity, the name is typed and compared literally, and there is no bin→pack index |

   That 7-of-7 is the fact that reframes this question. The `agent` key on a destination is not
   carrying information today — it **restates the pack's own `program` bin, every time**. So the
   `briefing`/`skills` migration is not "a rename that changes no behaviour"; it is the occasion on
   which that redundancy is either removed (B), renamed (A), or deliberately kept (C rejected
   again). Those have different costs and different blast radii.

   **Answer (2026-09-20):**
   > **All three.** The conflation is identical, and fixing `files` alone leaves two kinds
   > carrying the flag this doc exists to delete.

   [OQ-D5](#OQ-D5) is what made that cheap rather than merely consistent: with the identity
   declared once per pack, the migration is not "rename `agent` fourteen times", it is **delete**
   `agent` from every destination and state it once — removing 31 of 38 restatements instead of
   respelling them. `files` remains the forcing case, being the only kind whose contributions
   carry a raw path today.

   **Answer:**
   > _(empty — fill in when decided)_

4. ✅ <a id="OQ-D5"></a> **OQ-D5: Where does a slot's agent identity live?**

   Forced by [OQ-D3](#OQ-D3) rather than filed alongside it: writing out the concrete
   `briefing` line is what showed that [§2](#2-what-a-kind-is-and-what-a-slot-is)'s *"`agent` and
   `agents` both disappear"* is asserted rather than shown. Candidates A/B/C are tabulated under
   [OQ-D3](#OQ-D3). The measurement that makes it live: the declared `agent` equals the pack's
   `program` bin in **7 of 7** agent packs, so the key is pure redundancy today and the split is
   the moment to decide whether it stays.

   <!-- vantage: oq id=OQ-D5 leaning="B — declare the agent identity once per pack. It is the only candidate that removes the 7-of-7 redundancy rather than renaming or re-deriving it, and OQ-BA2 already refused the derivation." -->

   _Leaning:_ **B**, the pack declaring its identity once. It is the only candidate that *removes*
   the redundancy rather than renaming it (A) or re-deriving what [`OQ-BA2`](./briefing-audiences.md#decision-ledger) refused (C) — and it is
   the same lever [`manifest-language.md`](./manifest-language.md) is pulling, so the two should be
   ruled together rather than twice.

   **Answer (2026-09-20):**
   > **B, stated as a rule about core's vocabulary rather than about a field:** core knows an
   > "agent" **only insofar as it identifies a config target**. A pack provides **0 or 1** agents,
   > and if it provides one it **must name it** — declared once, never derived.

   That formulation is stronger than the candidate it picks, because it is already how the tree
   works in three places and the exceptions are the bug:

   - **The `<agent>/<slot>` address already ships.** A `config` surface is
     `{agent, name, path}` and is addressed `claude/settings` — and a content pack already targets
     one that way: `packs/matt` (a user pack) writes `{"kind": "config-overlay", "surface":
     "claude/settings"}`. `exposes`/`to` is **that address generalized from config surfaces to
     every slot**, not a new scheme.
   - **The identity is restated 38 times for 7 distinct values** across the shipped agent packs
     (`agy` 7, `pi` 7, `claude` 6, `codex` 5, `copilot` 5, `opencode` 5, `oh-omp` 3). Declaring it
     once removes 31 restatements and is the same lever
     [`manifest-language.md`](./manifest-language.md) is pulling.
   - **0-or-1 is already true, including of the hard case.** Every shipped pack provides exactly
     one agent or none, and no pack names two. `packs/matt` contributes to `claude`, `agy`,
     `codex` **and** `pi` while naming **zero** agents — it *addresses* agents without *providing*
     one, which is exactly the distinction the rule draws.
   - **And the one kind with no address is the one that broke.** In that same pack, `files` is
     written `{"kind": "files", "into": ".pi/agent/themes"}` — a **raw path**, hardcoding pi's
     layout into a content pack, because `files` has no slot to name. That is the coupling this
     doc exists to delete, present in a real manifest today.

   ⚠ **This narrows [`AGENTS.md`](../../AGENTS.md)'s *"core does not know what an agent is"* and
   must be read as a narrowing, not a contradiction.** Core still has no agent registry, no
   `agents` config key, and no idea what an agent *is* or *does*. What it has — and has had since
   config surfaces were keyed — is a **name that resolves an address**. The rule says so out loud
   and bounds it: one per pack, declared, never inferred (which is
   [`OQ-BA2`](./briefing-audiences.md#decision-ledger)'s ruling restated at pack scope).

   **The cost, stated:** a pack supplying two agents becomes two packs. Nothing ships that shape,
   so the constraint is free today and is a real restriction tomorrow.

5. ✅ **OQ-D4: Are the field names `exposes` / `to` / `accepts` right?**

   <!-- vantage: oq id=OQ-D4 leaning="Provisional. 'exposes'/'accepts' read as the receiving end; 'to' is the shortest thing that is not 'into'." -->

   **Answer (2026-09-20):**
   > **Provisional.** `exposes`/`accepts` read as the receiving end; `to` is the shortest thing
   > that is not `into`. Names must not collide with an existing key, and `to` must stay
   > distinguishable from `into` (the owner's landing path). Cheap to change now and expensive
   > after packs adopt it, so this is settled only until someone proposes better.

## 7. Decision ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| **OQ-D1** | **A second axis, `exposes`.** A `kind` names a contribution; a slot is not one, and making it a kind puts the receiving role inside the supplying vocabulary | 2026-09-20 | [§2](#2-what-a-kind-is-and-what-a-slot-is), [§6](#6-open-questions) | no |
| **OQ-D2** | **The agent (`bin`) name**, `<bin>/<slot>` — content survives swapping which pack supplies the agent. ⚠ Ratifies what `briefing` and `skills` already do in all seven agent packs; only `files` lacked it | 2026-09-20 | [§3](#3-the-address-the-agent-never-the-pack) | partly — the address exists for two of three kinds |
| **OQ-D3** | — **open, and now blocked on [OQ-D5](#OQ-D5).** The original leaning is withdrawn: `agents` (plural) appears in no shipped manifest, and every `briefing`/`skills` contribution is already unambiguously a destination. The follow-up review then showed the migration cannot be priced at all while the target spelling is undecided | — | — | — |
| **OQ-D5** | — **open.** Where a slot's agent identity lives: carried by the slot (A), declared once per pack (B), or derived from the `program` bin (C, mechanically available at 7/7 but refused by [`OQ-BA2`](./briefing-audiences.md#decision-ledger)) | — | — | — |
| **OQ-D4** | **Provisional.** `exposes`/`accepts` read as the receiving end; `to` is the shortest thing that is not `into`. Settled only until someone proposes better | 2026-09-20 | [§6](#6-open-questions) | no |
