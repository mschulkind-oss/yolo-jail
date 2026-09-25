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

**Status:** DESIGN, 2026-09-21 — seven rulings are owed. The ROLE MODEL is decided (2026-09-20) and
the BUILD is blocked (2026-09-21). Re-checked against the tree 2026-09-24: the premises of
[OQ-D6](#OQ-D6), [OQ-D8](#OQ-D8) and [OQ-D9](#OQ-D9) have moved since they were filed, each
question carries a note saying how, and none of them is ruled.
[OQ-D1](#6-open-questions)–[OQ-D5](#OQ-D5) are ruled and stand. An attempt to build slice 1 on
2026-09-21 stopped at seven places where the doc does not say enough for an implementer to proceed
without CHOOSING A BEHAVIOUR — filed below as [OQ-D6](#OQ-D6)–[OQ-D12](#OQ-D12), one of them
(a migration window, [OQ-D6](#OQ-D6)) a boot-bricking class with a test in the tree already
documenting it. **Nothing of `exposes` is built.** What DID land that day is
[§5.1](#51-what-landed-instead-2026-09-21): **four** shipped defects in the mechanism this doc
reworks — three of them already ruled elsewhere and never built, the fourth found by measuring —
now fixed and pinned, including the alias-root layout, which was live at the host notch.
Companion to [`pi-pack-extensions.md`](pi-pack-extensions.md), whose slot shape this supersedes.
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
> (the alias-root account in [`pi-pack-extensions.md`](pi-pack-extensions.md#10-decision-ledger)).
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

**Needs your ruling:** [OQ-D6](#OQ-D6), [OQ-D7](#OQ-D7), [OQ-D8](#OQ-D8), [OQ-D9](#OQ-D9), [OQ-D10](#OQ-D10), [OQ-D11](#OQ-D11), [OQ-D12](#OQ-D12).
All **seven** were found by trying to build it and all have the same shape — a
choice a reasonable implementer would make silently, that changes what lands on disk or whether a
jail boots: [OQ-D6](#OQ-D6) (the migration window — **the blocker**), [OQ-D7](#OQ-D7) (`accepts`
has no vocabulary and no stated relation to `kind`), [OQ-D8](#OQ-D8) (what the briefing and skills
slots are CALLED), [OQ-D9](#OQ-D9) (`to` is a scalar and `agents` is a list),
[OQ-D10](#OQ-D10) (one severity for two failures the tree split on purpose),
[OQ-D11](#OQ-D11) (what pack-scope identity does to core's own surfaces), [OQ-D12](#OQ-D12) (the
prior one-slot-per-agent ruling is now BUILT and this doc legalizes what it refuses).
[OQ-D1](#6-open-questions)–[OQ-D5](#OQ-D5) stay ruled; see [§7](#7-decision-ledger).

**Reads with:** [`pack-system.md`](../reference/pack-system.md) (the `contributes` vocabulary),
[`agent-briefings.md`](../reference/agent-briefings.md#audiences-what-varies-per-destination) (the `agent`/`agents` mechanism this splits
apart), [`stringly-typed-references-principle.md`](../reference/stringly-typed-references-principle.md)
(what a name may reference), [`pi-pack-extensions.md`](pi-pack-extensions.md) (the concrete
case, and the bug that forced this), [`pi-pack-extensions-plan.md`](pi-pack-extensions-plan.md)
(the build hand-off carrying the superseded slices 1–3),
[`pi-extension-lifecycle.md`](pi-extension-lifecycle.md) (the sibling fetch axis),
[`manifest-language.md`](manifest-language.md) (the
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

**A `kind` names a contribution — what a pack supplies.** The kinds are `packdecl`'s closed
registry (`internal/packdecl/kinds.go`): `program`, `config`, `config-overlay`, `state`, `mount`,
`loophole`, and the rest. `briefing`, `skills` and `files` are the kinds that *also* carry a
destination today, which is the conflation this doc splits apart. That is the sense
[`pack-system.md`](../reference/pack-system.md) already uses.

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
  conventional source (`briefing/`, `skills/`) is a property of the *contribution*, not the slot.
  (The prose source was a root `AGENTS.md` when this was written. Since the briefing defaults
  were built, it is the pack's `briefing/` directory, and `AGENTS.md` is refused as a source:
  [`OQ-PB1`](../reference/pack-system.md#oq-pb1), [`OQ-PB2`](../reference/pack-system.md#oq-pb2).)
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
surfaces**: `agent` there is the same field NAME, and
[`packdecl`](../../internal/packdecl/contributes.go) says so at the field. It is read across
`internal/agentcfg` and `internal/entrypoint` — compose, the state render, the host render and its
revert, the overlay prune, the adoption archive, the surface manifest — not the two mechanisms this
section names. (A file count stood here and reproduced at neither of two readings; the number was
the drift, so it is gone. `rg -n 'Agent' internal/agentcfg internal/entrypoint` is the measurement,
and [OQ-D11](#OQ-D11) is the part of it that is a RULING rather than a count: the same field name
does **not** hold the same string.)

⚠ **AND THE DESTINATION-READER HALF IS UNDERSTATED TOO, which the ⚠ above does not cover.** Slice 2
names two mechanisms; a briefing/skills/files DESTINATION is read in the run pipeline, the host
briefing and skills composers, the host files tree, the overlay prune, `internal/basehome`,
`packload`'s footprint, `yolo pack`'s lint and footprint — and in
[`internal/render/fieldset.go`](../../internal/render/fieldset.go), which is the one that does not
merely need editing: it is a per-notch applicability census keyed on `packdecl.Kind`, and a slot is
by construction NOT a kind. Destinations ARE honored at the host notch today, so the second axis
has to acquire a way to say so, or the census loses the ability to refuse an inapplicable one by
name — the thing it exists for.

**So this is a pipeline, not a fan-out**, and the order is forced by what depends on what:

| Slice | Changes | Depends on |
| :--- | :--- | :--- |
| **1 — schema** | `internal/packdecl`: the `exposes` axis, `to` on a contribution, pack-scope `agent`, and the refusals that replace `agent`/`agents` on a contribution | — |
| **2 — resolution** | `internal/packload` borrowing, `internal/cli/run/packfiles.go` mount resolution | 1 |
| **3 — surfaces** | `internal/agentcfg/manifest` and its readers take the pack-scope identity | 1 |
| **4 — manifests** | the seven agent packs, and the raw `into` path a content pack writes today | 1–3 |

Slice 4 is the one that pays: it deletes 31 of the 38 identity restatements
([OQ-D5](#OQ-D5)) and removes the only place a content pack hardcodes another pack's layout.

⚠ **SLICE 4 IS ALSO THE ONE THAT CANNOT BE BUILT YET.** It migrates the seven shipped agent packs,
and a shipped pack dropping `into` is the version-boundary fault
[`agentsurfaces_test.go`](../../internal/packload/agentsurfaces_test.go)'s
`TestShippedAgentPacksKeepIntoForSkew` exists to prevent. See [OQ-D6](#OQ-D6): it is the blocker,
and it is why this pipeline stops before slice 1 rather than at slice 4 — a schema nothing may
adopt is not a schema yet.

### 5.1 What landed instead (2026-09-21)

The build attempt that filed [OQ-D6](#OQ-D6)–[OQ-D12](#OQ-D12) did not leave empty-handed: reading
every site that reads `agent` and `agents` turned up **four defects in the shipped mechanism**.
Three needed no ruling from this doc — each was already ruled in
[`pi-pack-extensions.md`](pi-pack-extensions.md) and simply never built — and the fourth was found
by measuring the arrangement rather than reading about it. All four were invisible for one reason:
**no manifest in the corpus declares a files slot**, so the mechanism shipped with zero users and a
green gate.

1. **The alias-root layout was live at the host notch.** The jail landed an addressed tree at
   `<slot>/<pack>`; destination borrowing landed it at the slot ROOT, so one `pack.json` delivered
   to two different paths and the host variant put a contributor's whole tree where the owner's own
   content and every other contributor's go — [OQ-4](pi-pack-extensions.md#10-decision-ledger)
   and [§8](pi-pack-extensions.md#8-invariants-and-failure-modes) invariant 2 ("collisions are impossible"), violated at one of two notches. The join now
   lives in ONE function both notches call
   ([`packload.SlotLanding`](../../internal/packload/mergedest.go)), and
   [`filesslotparity_test.go`](../../internal/cli/run/filesslotparity_test.go) pins the two against
   each other plus the catastrophic form itself: no delivery may reach the slot root or the home.
2. **`files` slots were not one-per-agent.** [OQ-1](pi-pack-extensions.md#10-decision-ledger)
   ruled a second one a load error; it was never implemented, and the two notches then picked
   DIFFERENTLY and silently (the jail's alias table is a map, so the last declaration won; borrowing
   dedups by path, so the host honored both). Now refused at authoring time by
   `packdecl.validateFilesDestinations`, naming the first entry's index — and deliberately NOT on
   the tolerant path, where a new refusal is a bricked boot.
3. **An addressed `files` claim had a blank footprint target, and two of them collided on it.**
   `files` is `CombineExclusive`, so the empty string grouped two contributors into a
   sole-ownership collision: `rc=1` from `pack lint`/`footprint` and fatal at `yolo check`, for the
   one arrangement per-pack namespacing exists to make legal.
4. **Two addressed trees from ONE pack to one agent named one path, and the notches disagreed
   maximally.** Found by measuring rather than reading: the landing path carries the CONTRIBUTING
   pack, so two of that pack's trees aimed at one agent resolve to the same directory — the jail
   emitted two binds at one destination (podman: *"duplicate mount destination"*, naming neither)
   and the host, which writes per FILE, merged both trees there in silence. The declaration
   pre-flight that exists for exactly this could not see it, because it reads the declared `into`
   and an addressed contribution has none: **a check pinned to the declaration while the emitter
   used the resolution.** Now a load error with the remedy (one `from` directory), at the manifest,
   where both notches see it. ⚠ **The alternative — MERGING two trees into one subdirectory — is a
   real option and is not built**: a bind mount cannot union two sources, so honoring it means
   staging a merged tree. Refusing is the reversible half, and the choice belongs to
   [OQ-D12](#OQ-D12).

Three smaller things, same pass: the refusal text and two comments still said the audience pair is
read "for `briefing` and `skills` only" (`files` joined them with the slot work); the built-in
`configuring-the-jail` skill told users to write `"agent": "pi"` in their OWN pack, which is the
destination spelling — a content pack writing it claims pi's identity, and the example as printed
does not even validate; and [`pack-system.md`](../reference/pack-system.md)'s `files` section
documented none of the slot shape at all.

**None of that is this doc's design, and it does not shrink it.** It is the pre-existing mechanism
made to match its own rulings, which is the honest baseline to rework FROM: every defect above was
a fact declared twice and drifting, which is the same disease `exposes` is prescribed for.

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
   | **B — the pack declares its identity once** | pack-level `"agent": "claude"`, then `{"name": "briefing", "into": …}` | The repetition goes, which is [`manifest-language.md`](manifest-language.md)'s concern exactly — but it is a second structural change riding on this one |
   | **C — derived from the `program` bin** | `{"name": "briefing", "into": …}`, agent inferred | **Mechanically available and ruled out.** Measured 2026-09-20: the declared `agent` equals the pack's own `program` bin in **7 of 7** agent packs, `oh-omp` included — so the derivation would work. [`OQ-BA2`](../reference/agent-briefings.md#oq-ba2) forbade it anyway: the audience match is against a declared string, typed and compared literally, never anything derived. (The profile chain does map a CLI name to the pack whose `program` installs it, `packload.binOwner`; the audience match deliberately does not use it.) |

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

   ⚠ **The measurement behind "38 for 7 distinct values" reproduces, on a reading this doc never
   states**, and the unstated part is a fourth carrier: 14 `briefing`/`skills` `agent`, 14 `config`
   SURFACE `agent`, and **10 autonomy-posture config-patch `agent`**
   (`AutonomyPosture.Config[].agent`, matched to surfaces by `(agent, name)` in `packload`). A
   slice 3 that touches only `internal/agentcfg/manifest` therefore leaves a quarter of the
   redundancy in place and the fold's match key inconsistent with everything else.
   (`packs/claude`'s `config-overlay surface: "claude/settings"` is a 39th occurrence and is an
   ADDRESS, not an identity; excluding it is what makes the total 38.)

4. ✅ <a id="OQ-D5"></a> **OQ-D5: Where does a slot's agent identity live?**

   Forced by [OQ-D3](#OQ-D3) rather than filed alongside it: writing out the concrete
   `briefing` line is what showed that [§2](#2-what-a-kind-is-and-what-a-slot-is)'s *"`agent` and
   `agents` both disappear"* is asserted rather than shown. Candidates A/B/C are tabulated under
   [OQ-D3](#OQ-D3). The measurement that makes it live: the declared `agent` equals the pack's
   `program` bin in **7 of 7** agent packs, so the key is pure redundancy today and the split is
   the moment to decide whether it stays.

   <!-- vantage: oq id=OQ-D5 leaning="B — declare the agent identity once per pack. It is the only candidate that removes the 7-of-7 redundancy rather than renaming or re-deriving it, and OQ-BA2 already refused the derivation." -->

   _Leaning:_ **B**, the pack declaring its identity once. It is the only candidate that *removes*
   the redundancy rather than renaming it (A) or re-deriving what [`OQ-BA2`](../reference/agent-briefings.md#oq-ba2) refused (C) — and it is
   the same lever [`manifest-language.md`](manifest-language.md) is pulling, so the two should be
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
     every slot**, not a new scheme. A second kind has used the same `surface` address since
     2026-09-24: `config-list`, which appends entries to one array of an owner's surface.
   - **The identity is restated 38 times for 7 distinct values** across the shipped agent packs
     (`agy` 7, `pi` 7, `claude` 6, `codex` 5, `copilot` 5, `opencode` 5, `oh-omp` 3). Declaring it
     once removes 31 restatements and is the same lever
     [`manifest-language.md`](manifest-language.md) is pulling.
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
   [`OQ-BA2`](../reference/agent-briefings.md#oq-ba2)'s ruling restated at pack scope).

   **The cost, stated:** a pack supplying two agents becomes two packs. Nothing ships that shape,
   so the constraint is free today and is a real restriction tomorrow.

5. ✅ **OQ-D4: Are the field names `exposes` / `to` / `accepts` right?**

   <!-- vantage: oq id=OQ-D4 leaning="Provisional. 'exposes'/'accepts' read as the receiving end; 'to' is the shortest thing that is not 'into'." -->

   **Answer (2026-09-20):**
   > **Provisional.** `exposes`/`accepts` read as the receiving end; `to` is the shortest thing
   > that is not `into`. Names must not collide with an existing key, and `to` must stay
   > distinguishable from `into` (the owner's landing path). Cheap to change now and expensive
   > after packs adopt it, so this is settled only until someone proposes better.

   ⚠ **THE ONE HARD CONSTRAINT IN THAT ANSWER IS ALREADY VIOLATED BY THE NAME IT PICKS**, and this
   is checkable rather than aesthetic. Measured 2026-09-21: `to` is a JSON key in this manifest
   language **three times already** — `Mount.To` (a home-relative jail PATH), `HostFile.To` (a
   `/ctx` PATH) and `AdapterPair.To` (a PROTOCOL name). A slot ADDRESS would be the fourth sense.
   The same shape has since shipped for another key: `config-list` (built 2026-09-24) puts a JSON
   Pointer in a contribution's `path`, while every config surface's `path` is a file path.
   The sharpest collision is inside ONE contribution entry, because `adapts` is a Contribution
   field: `{"kind":"adapter","adapts":{"from":…,"to":"anthropic"}}` — `to` a protocol — would sit
   beside `{"kind":"files","to":"pi/extensions","from":"pi-extensions"}` — `to` an address, next to
   a `from` that is still a path. A doc whose thesis is *"one word doing two opposite jobs is the
   defect"* cannot introduce a word doing four without saying so. Not filed as a new question,
   because this answer already says the name is settled only until someone proposes better: this is
   the measurement that says someone should.

6. 💬 <a id="OQ-D6"></a>**[OQ-D6](#OQ-D6): what is the MIGRATION WINDOW — and does a shipped pack
   ever get to use the new shape?** **This is the blocker.** Slice 4 migrates the seven agent packs,
   and [`agentsurfaces_test.go`](../../internal/packload/agentsurfaces_test.go)'s
   `TestShippedAgentPacksKeepIntoForSkew` already documents why it cannot: `DecodeTolerant` ignores
   unknown FIELDS but still validates the entries it keeps, so an entrypoint baked before the change
   refuses `{"kind":"briefing"}` with `kind "briefing" needs "into"` — *"a fatal boot, from a
   manifest the host staged, unrecoverable without a `just load`."* Its conclusion is explicit:
   *"Only a USER's own pack — which no old entrypoint has ever seen — may reach the addressed
   shape."* Both outcomes of ignoring it are bad and one is silent: a migrated pack either bricks an
   older entrypoint's boot, or (if the destination is removed cleanly) that entrypoint drops the
   unknown `exposes`, honors nothing, and delivers **every briefing and every skill NOWHERE** with
   no error anywhere. This is [`AGENTS.md`](../../AGENTS.md)'s *"the two halves deploy on different
   cadences"* class, and `version.SourceSkew` does not cover it — it compares the host binary to
   HEAD, not a staged manifest to a baked entrypoint.

   > **Premise changed, not ruled here.** The image has baked no yolo binary since 2026-09-06: the
   > entrypoint is mounted from the flake bundle on every launch
   > ([`jailprefix.go`](../../internal/cli/run/jailprefix.go)). So "an entrypoint baked before the
   > change" and "unrecoverable without a `just load`" no longer describe the tree, although the
   > test comment still says them. The shipped manifests are staged from the host `yolo`'s own
   > embed, so the pairing that bricks a boot is now a host `yolo` newer than the bundle its
   > entrypoint comes from. `just install` publishes the two together, a release ships them
   > together, and `version.SourceSkew` refuses a from-source launch whose binary and checkout
   > disagree. Whether any supported path still produces that pairing is not measured here, and
   > it decides how wide this window is. Separately, today's entrypoint accepts
   > `{"kind":"briefing"}` as a broadcast ([briefing P2](../reference/pack-system.md#briefing-p2)),
   > so only an entrypoint older than that refuses it.

   <!-- vantage: oq id=OQ-D6 leaning="Ship `exposes` as ADDITIVE and keep `into` on every shipped pack for a release: a jail reads whichever it understands, and only a user's own pack may go exposes-only — which is the boundary the skew test already draws." -->

   _Leaning:_ additive, in the shape the skew test already permits. The new axis is skew-safe for
   free (`DecodeTolerant` is a plain `json.Unmarshal`, so an older build ignores `exposes`), so the
   question is entirely about the DESTINATION a shipped pack keeps. Keeping both for a release
   (`exposes` for the new reader, the `{agent, into}` destination for the old) costs the redundancy
   [OQ-D5](#OQ-D5) exists to delete, temporarily and on purpose — and an implementer needs to be
   told whether "temporarily" means a release, a `just load` floor, or forever for shipped packs.
   **A DOUBLE-DELIVERY CHECK BELONGS TO THIS ANSWER:** a build that understands both must not honor
   both, or the new reader and the old destination compose one pack's prose twice — the exact fault
   `GeneratedHostBriefings` was added to stop.

   **Answer:**
   > _(empty — fill in when decided)_

7. 💬 <a id="OQ-D7"></a>**[OQ-D7](#OQ-D7): what is `accepts`, in a vocabulary — and who wins when it
   disagrees with `kind`?** [§2.1](#21-what-a-slot-declares) names four shapes in prose ("an opaque
   tree, a single file, a JSON document, a set of *things*"), [§2](#2-what-a-kind-is-and-what-a-slot-is)'s
   example writes `"tree"`, and [OQ-D3](#OQ-D3)'s candidate table writes a fifth value, `"concat"`.
   Nothing says whether the set is CLOSED, whether the field is REQUIRED, or what any value DOES.
   Three readings, three behaviours: **(a)** a closed enum — which makes it the fifth skew-sensitive
   closed enum in this schema, and `packdecl` warns that whoever adds one *extends its tolerance
   first, in its own change*; **(b)** documentation-only — the accepted-and-ignored declaration this
   schema refuses everywhere else (see the `adapts`/`profile`/`agent` refusals); **(c)** the
   authority for how content combines — which collides head-on with `Combine`, a fact about the KIND
   today (`files` exclusive, `briefing` concat, `skills` merge) and the thing that decides whether
   two packs addressing one slot is legal or fatal. A contribution still carries its `kind` in
   [§2](#2-what-a-kind-is-and-what-a-slot-is)'s own example, so under (c) one fact is declared twice
   — the disease, not the cure.

   <!-- vantage: oq id=OQ-D7 leaning="(b)-plus-a-match-check: `accepts` names the shape the slot takes and is REFUSED when a contribution's kind cannot produce it; combining stays the kind's fact. A slot that also decided the combine rule would declare one fact twice." -->

   _Leaning:_ the combine rule stays the KIND's — it already is, it is what `Collisions` reads, and
   moving it would relocate the duplication rather than remove it. That leaves `accepts` doing one
   job worth doing: a COMPATIBILITY check, refused by name when a contribution's kind cannot produce
   the shape the slot takes. Which makes the closed-enum tolerance question (a) unavoidable, and it
   should be answered in the same breath.

   **Answer:**
   > _(empty — fill in when decided)_

8. 💬 <a id="OQ-D8"></a>**[OQ-D8](#OQ-D8): what are the briefing and skills slots CALLED?**
   [§4](#4-the-split-applied) says `{agent, into}` "becomes an `exposes` entry" for both kinds and
   names no slot; [§2.1](#21-what-a-slot-declares) says a slot's `name` is *"local to the agent it
   belongs to"*. Both readings are legal and they are not the same design: `packs/claude` exposing
   **`"briefing"`** and `packs/pi` exposing `"briefing"` is a CONVENTION every content pack then
   depends on (and which "local to the agent" does not mandate), while `"CLAUDE.md"` and
   `"AGENTS.md"` are equally legal under that sentence and make every content pack hardcode a
   per-agent slot name — the coupling this doc exists to delete, relocated from paths to names. It
   also decides whether core's own synthetic zero-ceremony borrower can find anything at all: core
   mints it for any pack that declares no destination of that kind (the pack with no `pack.json` is
   the live case), it carries a `Kind` and no address, so it must match slots by SOMETHING — and the
   only candidates are a conventional name or the kind itself.

   > **Premise changed, not ruled here:** since the briefing defaults were built
   > ([P2](../reference/pack-system.md#briefing-p2), [P3](../reference/pack-system.md#briefing-p3)),
   > the kind-only borrower is not only core's. A manifest may declare a broadcast
   > (`{"kind":"briefing"}`), and the implicit one covers every `briefing/` file no contribution
   > names, whatever destinations the pack declares. Each carries a kind and no address, so this
   > question now decides how a DECLARED broadcast finds its slots too.

   <!-- vantage: oq id=OQ-D8 leaning="Conventional names, and say so: the slot for a kind is named for the kind (`briefing`, `skills`), so `to: pi/briefing` is writable without reading pi's manifest and the kind-only borrower still resolves." -->

   _Leaning:_ conventional, and stated as a rule rather than left to convention — the slot that
   receives a `kind` is NAMED for that kind. It is the only reading under which a content pack can
   write `to: "pi/briefing"` without reading pi's manifest, and the only one where the manifest-less
   pack (which can name nothing) still resolves. The cost is that "local to the agent" becomes
   "local to the agent, except for the kinds core already names", which is worth writing down.

   **Answer:**
   > _(empty — fill in when decided)_

9. 💬 <a id="OQ-D9"></a>**[OQ-D9](#OQ-D9): is `to` a scalar, and where does a MULTI-AGENT audience
   go?** `agents` is a LIST, two entries are supported today and pinned
   (`{"kind":"briefing","from":"prose/claude.md","agents":["claude","pi"]}` validates, and
   `packload` UNIONS audiences across contributions when it dedups skills sources).
   [§2](#2-what-a-kind-is-and-what-a-slot-is)'s `to` is a scalar and [§2](#2-what-a-kind-is-and-what-a-slot-is)
   says `agents` "disappears". So: is `to` a list? Is it two contributions sharing one `from`? Those
   differ in behaviour, not spelling — a union of destinations versus N deliveries with N provenance
   headers. **Correcting the obvious first guess:** BROADCAST is not at risk. A manifest cannot
   declare one today (a bare `{"kind":"briefing"}` is refused for want of `into`), and broadcast is
   reachable only through the borrower core SYNTHESIZES for a pack that declares no destination of
   the kind — core owns that value, so it survives the schema change untouched. The audience LIST is
   the whole loss.

   > **Premise changed, not ruled here:** since [briefing P2](../reference/pack-system.md#briefing-p2) a manifest CAN declare broadcast (`{"kind":"briefing"}` is valid), and two content contributions sharing one `from` are refused ([`OQ-PB5`](../reference/pack-system.md#oq-pb5)), so `to` needs a broadcast spelling.

   <!-- vantage: oq id=OQ-D9 leaning="`to` takes one address or a list of them, decoded as N deliveries of one source — the same thing two `agents` entries mean today, so no behaviour moves and the provenance stays per destination." -->

   _Leaning:_ a list, decoded as N deliveries of one source, because that is precisely what
   `agents: ["claude","pi"]` means today — nothing moves, and each destination keeps its own
   provenance line. Forcing two entries would be the only alternative that keeps `to` scalar, and it
   makes the common "these rules are for both my agents" case say the same `from` twice.

   **Answer:**
   > _(empty — fill in when decided)_

10. 💬 <a id="OQ-D10"></a>**[OQ-D10](#OQ-D10): does an unmatched `to` have ONE severity or two?**
    [§3](#3-the-address-the-agent-never-the-pack) says *"an unmatched `to` is refused at load with a
    fix naming the slots that exist"*. That fuses two failures the tree split ON PURPOSE, and
    [`agentaudience.go`](../../internal/packload/agentaudience.go)'s package comment is a whole essay
    on why: **is the name in the vocabulary?** is FATAL (the addressing author's mistake), while
    **did the name reach a destination of this kind?** is REPORTED — *"where the remedy is a line in
    the owning pack rather than a line in the addressing one"* — because refusing the launch over it
    *"would punish the wrong author"* (agent-briefings.md [R1](../reference/agent-briefings.md#ba-r1)/[R4](../reference/agent-briefings.md#ba-r4), and the `files` half of it is
    implemented verbatim in the run pipeline). `to: "pi/extensions"` fuses them into one string: `pi`
    unknown is case 1, `pi` known with no `extensions` slot is case 2. "Refused at load" also lands
    the gate where R5 says it must not be — `yolo pack lint` takes a pack root with no config and
    cannot know the selected slot vocabulary.

    <!-- vantage: oq id=OQ-D10 leaning="Two severities, split exactly where they are split today: an unknown AGENT segment is fatal at the launch pre-flight and `host apply`; a known agent with no such slot is reported, never fatal, and never at `pack lint`." -->

    _Leaning:_ keep both severities and split them on the two SEGMENTS of the address, which is
    where the existing rule already cuts. Reversing R1/R4 is a live option — a slot vocabulary is
    more declarative than an inferred destination, so "refuse the typo" is arguable — but it is a
    REVERSAL of a shipped ruling and has to be written as one.

    **Answer:**
    > _(empty — fill in when decided)_

11. 💬 <a id="OQ-D11"></a>**[OQ-D11](#OQ-D11): what does pack-scope identity do to core's OWN
    surfaces, and to a pack that addresses an agent it does not provide?**
    [§5](#5-what-this-costs)'s ⚠ pulls the config surfaces into scope on the claim that `agent`
    there is *"the same field name holding the same string"*. The field's own docstring disagrees:
    it is the owning agent id **"or a non-agent surface owner such as `mcp`, `lsp`, `mise`,
    `identity`"** — and core ships `Agent: "mise"` for a surface that *"belongs to no pack and
    renders unconditionally"*. `mise` is in neither agent vocabulary (not a pack claim, not an
    installed CLI); it is baked by the image and installed by no pack. So under "the pack-scope
    identity", `mise/config` has no pack to take an identity from, while `config-overlay
    surface: "mise/config"` is a live target. Mirror image, same ruling: nothing today restricts a
    pack to surfaces under its own name, and the pack-scope identity silently forbids that — a
    capability removal this doc does not state. Same question for `exposes` on a pack that provides
    NO agent: its slots have no address prefix and can never be named, so is that refused at load or
    accepted as inert?

    <!-- vantage: oq id=OQ-D11 leaning="`Surface.Agent` survives as a free address SEGMENT and slice 3 means only 'packs stop repeating it': core's `mise`/`user` owners are not agents and never were, so the pack-scope identity supplies a DEFAULT for that segment rather than replacing it." -->

    _Leaning:_ the field stays, and slice 3 is "packs stop REPEATING it" rather than "the field
    moves" — deleting the segment breaks `mise/config` and every `user/<slug>` host-file surface,
    neither of which is an agent. Which means the pack-scope identity is a DEFAULT for the segment,
    not the only source of it, and the doc should say which shapes may still name it explicitly.

    **Answer:**
    > _(empty — fill in when decided)_

12. 💬 <a id="OQ-D12"></a>**[OQ-D12](#OQ-D12): how many slots may one agent expose, and what makes
    two of them an error?** [`pi-pack-extensions.md`](pi-pack-extensions.md)'s
    [OQ-1](pi-pack-extensions.md#10-decision-ledger) — *"one files destination per agent; a second
    is a load error"* — is no longer merely unretired: it is **built** as of 2026-09-21
    ([§5.1](#51-what-landed-instead-2026-09-21)). `exposes` plus a slot `name` is exactly what makes
    two slots per agent legal, so this doc must retire that ruling EXPLICITLY, and answer the two
    cases the new shape invents: two `exposes` entries with the SAME `name` on one agent, and two
    with different names pointing at the same `into`. Note which gate can see either: the existing
    cross-pack `AgentNameCollisions` fires only when TWO PACKS hold one name, so it is not the guard
    against one pack declaring a name twice.

    **And the mirror-image half, which is the one that bit:** how many trees may ONE pack send to
    one slot? [§5.1](#51-what-landed-instead-2026-09-21) item 4 refused a second one, because the
    landing path carries the contributing pack and two trees then name one directory — but MERGING
    them is a coherent alternative that the host notch already performs by accident and the jail
    cannot perform at all (a bind mount unions nothing, so it would need a staged merge). Whichever
    way this goes, it must be one answer for both notches.

    <!-- vantage: oq id=OQ-D12 leaning="Many slots per agent, that being the point; a duplicate slot NAME within a pack is a load error like every other duplicate name in this schema, and two names at one `into` is a real arrangement (two shapes, one directory) — reported, not refused." -->

    _Leaning:_ many, with a duplicate `name` refused exactly as duplicate service, profile, provider
    and adapter names already are (authoring path only — the tolerant decoder cannot see siblings
    and a new refusal there is a bricked boot). Two names at one `into` is the less obvious half and
    is probably legitimate: a landing path is not an identity.

    **Answer:**
    > _(empty — fill in when decided)_

## 7. Decision ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| **OQ-D1** | **A second axis, `exposes`.** A `kind` names a contribution; a slot is not one, and making it a kind puts the receiving role inside the supplying vocabulary | 2026-09-20 | [§2](#2-what-a-kind-is-and-what-a-slot-is), [§6](#6-open-questions) | no |
| **OQ-D2** | **The agent (`bin`) name**, `<bin>/<slot>` — content survives swapping which pack supplies the agent. ⚠ Ratifies what `briefing` and `skills` already do in all seven agent packs; only `files` lacked it | 2026-09-20 | [§3](#3-the-address-the-agent-never-the-pack) | partly — the address exists for two of three kinds |
| **OQ-D3** | **All three kinds.** The conflation is identical and fixing `files` alone leaves two kinds carrying the flag. ⚠ The original leaning was WITHDRAWN first (`agents` appears in no shipped manifest, and every `briefing`/`skills` contribution is already unambiguously a destination), then ruled the same way in the opposite direction once [OQ-D5](#OQ-D5) made the migration a DELETION rather than a rename | 2026-09-20 | [§6](#6-open-questions) | no |
| **OQ-D5** | **Declared once per pack (B), stated as a rule about core's vocabulary:** core knows an "agent" only insofar as it identifies a config target; a pack provides **0 or 1** and must NAME the one it provides — declared, never derived | 2026-09-20 | [`OQ-D5`](#OQ-D5) | no. ⚠ [OQ-D11](#OQ-D11) is the unpriced half: core's own `mise`/`user` surface owners are not agents and have no pack to take an identity from |
| **OQ-D4** | **Provisional.** `exposes`/`accepts` read as the receiving end; `to` is the shortest thing that is not `into`. Settled only until someone proposes better. ⚠ Its own "must not collide with an existing key" is already unmet — `to` is a JSON key three times in this schema (a jail path, a `/ctx` path, a protocol), and `adapts.to` would sit inside the same contribution entry as a slot `to` | 2026-09-20 | [§6](#6-open-questions) | no |
| **OQ-D6** | — **open, and the BLOCKER.** The migration window: a shipped pack that drops `into` bricks an older baked entrypoint's boot, or silently delivers every briefing and skill nowhere. `TestShippedAgentPacksKeepIntoForSkew` already draws the boundary — only a user's own pack may reach the addressed shape. ⚠ The "baked entrypoint" premise has changed: the entrypoint is mounted from the flake bundle every launch, so the window is now a host `yolo` newer than that bundle, unmeasured | — | — | — |
| **OQ-D7** | — **open.** `accepts` has no vocabulary, no stated consumer and no stated relation to `kind`'s `Combine` — and combining decides whether two packs addressing one slot is legal or fatal | — | — | — |
| **OQ-D8** | — **open.** What the `briefing` and `skills` slots are CALLED. Conventional names make content packs portable and let the kind-only borrower resolve; per-agent names relocate the coupling from paths to names | — | — | — |
| **OQ-D9** | — **open.** `to` is a scalar and `agents` is a list, so a multi-agent audience has no spelling. ⚠ The body's "broadcast is not at risk" premise has changed: since [briefing P2](../reference/pack-system.md#briefing-p2) a manifest can declare a broadcast, so `to` needs a spelling for one too | — | — | — |
| **OQ-D10** | — **open.** "An unmatched `to` is refused at load" collapses two severities the tree split on purpose — unknown NAME fatal, no-such-destination reported — and lands the gate where R5 forbids it (`pack lint` has no config) | — | — | — |
| **OQ-D11** | — **open.** Pack-scope identity vs core's own surfaces (`mise/config` has no pack) and vs a pack naming a surface for an agent it does not provide (legal today, silently forbidden after) | — | — | — |
| **OQ-D12** | — **open.** How many slots per agent, and what makes two an error. The prior one-per-agent ruling is now BUILT, so this doc must retire it explicitly | — | — | — |
| **Alias-root layout** | **`<slot>/<pack>`, at every notch**, through one resolver — [`pi-pack-extensions.md`](pi-pack-extensions.md) [`OQ-4`](pi-pack-extensions.md#10-decision-ledger) restated where it could be read, since the host notch had violated it since the slot shipped | 2026-09-21 | [§5.1](#51-what-landed-instead-2026-09-21) | **yes** — `packload.SlotLanding`, pinned at both notches |
