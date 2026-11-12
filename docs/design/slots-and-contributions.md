---
title: "Slots are not kinds: a pack accepts content through a named exposure, addressed by agent"
date: 2026-09-20
status: in-review
tags: [packs, manifest, declarations, slots, audience, design]
summary: "A `kind` names what a pack CONTRIBUTES. A destination names what an agent pack ACCEPTS, which is the opposite role — so it is not a contribution and should not be a kind or a field shape within one. Today `briefing`, `skills` and `files` each carry both roles and tell them apart with `agent` vs `agents`, a singular/plural flag that already produced a real layout bug (`files` required `from`, so a destination had to carry content). This splits the manifest into two axes — `contributes` (kinds) and `exposes` (named slots) — and addresses a slot by the AGENT (the `bin` name audiences already key on), never the pack slug, so content survives swapping which pack supplies that agent."
vantage:
  status-chip: true
stage: DESIGN
next: "Rule OQ-D6, the migration window: the exposes build, pi-pack-extensions.md's rework and the DS-D33 worktree lines (jail-home.md) all wait on it"
---

# Slots are not kinds: a pack accepts content through a named exposure, addressed by agent

**Status:** 2026-09-21 — four rulings are owed. The ROLE MODEL is decided (2026-09-20) and
the BUILD is blocked (2026-09-21). Triaged 2026-09-30: [OQ-D7](#7-decision-ledger), [OQ-D8](#7-decision-ledger) and
[OQ-D11](#7-decision-ledger) each had one sensible answer inside the ruled role model, and are decided as
implementation choices [SC-D1](#SC-D1) to [SC-D3](#SC-D3), each reversible. Re-checked against
the tree 2026-09-24: the premises of [OQ-D6](#OQ-D6), [OQ-D8](#7-decision-ledger) and [OQ-D9](#OQ-D9) have
moved since they were filed, and each question carries a note saying how. [OQ-D6](#OQ-D6) and
[OQ-D9](#OQ-D9) are not ruled. [OQ-D6](#OQ-D6)'s moved again on
2026-09-29, when two shipped agent packs took one addressed content line each.
[OQ-D1](#7-decision-ledger)–[OQ-D5](#7-decision-ledger) are ruled and stand. An attempt to build slice 1 on
2026-09-21 stopped at seven places where the doc does not say enough for an implementer to proceed
without CHOOSING A BEHAVIOUR — filed below as [OQ-D6](#OQ-D6)–[OQ-D12](#OQ-D12), one of them
(a migration window, [OQ-D6](#OQ-D6)) a boot-bricking class with a test in the tree already
documenting it. **Nothing of `exposes` is built.** What DID land that day is
[§5.1](#51-what-landed-instead-2026-09-21): **four** shipped defects in the mechanism this doc
reworks — three of them already ruled elsewhere and never built, the fourth found by measuring —
now fixed and pinned, including the alias-root layout, which was live at the host notch.
Companion to [`pi-pack-extensions.md`](pi-pack-extensions.md), whose slot shape this supersedes.
⚠ Review moved this doc twice in one day: the [OQ-D3](#7-decision-ledger) leaning was WITHDRAWN (it rested on
the `agent`/`agents` flag living in all three kinds, which the corpus does not bear out), then
[OQ-D5](#7-decision-ledger) was FILED when writing the concrete replacement line showed that
[§2](#2-what-a-kind-is-and-what-a-slot-is)'s *"`agent` and `agents` both disappear"* was asserted
rather than shown — and then RULED, which unblocked [OQ-D3](#7-decision-ledger) in the opposite direction from
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

**Needs your ruling:** [OQ-D6](#OQ-D6), [OQ-D9](#OQ-D9), [OQ-D10](#OQ-D10), [OQ-D12](#OQ-D12).
The build attempt found **seven** questions, all with the same shape — a
choice a reasonable implementer would make silently, that changes what lands on disk or whether a
jail boots. Four are still yours: [OQ-D6](#OQ-D6) (the migration window — **the blocker**),
[OQ-D9](#OQ-D9) (`to` is a scalar and `agents` is a list),
[OQ-D10](#OQ-D10) (one severity for two failures the tree split on purpose), and
[OQ-D12](#OQ-D12) (the prior one-slot-per-agent ruling is now BUILT and this doc legalizes what it
refuses). Three are decided as implementation choices on 2026-09-30: [OQ-D7](#7-decision-ledger) (`accepts`
is a closed vocabulary used as a compatibility check, [SC-D1](#SC-D1)), [OQ-D8](#7-decision-ledger) (the
briefing and skills slots are named for their kinds, [SC-D2](#SC-D2)) and [OQ-D11](#7-decision-ledger) (the
owner segment survives, and a pack's own declarations take its identity, [SC-D3](#SC-D3)).
[OQ-D1](#7-decision-ledger)–[OQ-D5](#7-decision-ledger) stay ruled; see [§7](#7-decision-ledger).

**Reads with:** [`pack-system.md`](../reference/pack-system.md) (the `contributes` vocabulary),
[`agent-briefings.md`](../reference/agent-briefings.md#audiences-what-varies-per-destination) (the `agent`/`agents` mechanism this splits
apart), [`stringly-typed-references-principle.md`](../reference/stringly-typed-references-principle.md)
(what a name may reference), [`pi-pack-extensions.md`](pi-pack-extensions.md) (the concrete
case, and the bug that forced this), [`pi-extensions.md`](../reference/pi-extensions.md#17-the-pre-launch-refresh) (the sibling fetch axis),
[`pack-manifests.md` §7](pack-manifests.md#7-the-manifest-language-principles-alternatives-and-rulings) (the
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
current. The contributor declares only the recipient. **`agent` and `agents` both leave the
entries**: a slot is never addressed by an audience (it *is* the receiving end), and a
contribution is never a slot. The agent identity a slot's address needs is declared once, by the
pack ([§3.1](#31-where-the-agent-identity-lives)). A slot is a second axis rather than a kind of
its own, because making it a kind would put the receiving role back inside the supplying
vocabulary ([OQ-D1](#7-decision-ledger)).

**The field names are provisional** ([OQ-D4](#7-decision-ledger)): `exposes` and `accepts` read
as the receiving end, and `to` is the shortest word that is not `into`, the owner's landing path.
They are cheap to change until packs adopt them, and settled only until someone proposes better.
⚠ The answer's own constraint, that a name must not collide with an existing key, is already
unmet by `to`. It is a JSON key three times in this manifest language: `Mount.To` (a jail path),
`HostFile.To` (a `/ctx` path) and `AdapterPair.To` (a protocol name). The sharpest collision is
inside one contribution entry, since `adapts` is a contribution field:
`{"kind":"adapter","adapts":{"from":…,"to":"anthropic"}}` would sit beside
`{"kind":"files","to":"pi/extensions","from":"pi-extensions"}`. That is the measurement that says
someone should propose better.

### 2.1 What a slot declares

- **`name`** — what this pack accepts. Local to the agent it belongs to.
- **`into`** — the landing path, the owner's own fact.
- **`accepts`** — the **shape** it takes: an opaque tree, a single file, a JSON document, a set of
  *things*. This is the piece `files` got wrong by implication: **a slot is not named after its
  transport.** `accepts` is a closed vocabulary whose one job is a compatibility check, and how
  content combines stays the kind's `Combine` ([SC-D1](#SC-D1)).

A slot that receives `briefing` or `skills` is named for that kind, by rule, so a content pack
writes `to: "pi/briefing"` without reading pi's manifest, and a contribution that carries a kind
and no address finds its slots by that name ([SC-D2](#SC-D2)).

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

The `bin` name ratifies what two of the three kinds already did: every `briefing` and `skills`
destination in the seven shipped agent packs declared `agent: "<bin>"` beside its `into`
(`oh-omp` for the `omp` pack, whose bin is not its slug), measured 2026-09-20. Only `files`
lacked an address ([OQ-D2](#7-decision-ledger)).

### 3.1 Where the agent identity lives

**Core knows an "agent" only insofar as it identifies a config target.** A pack provides **0 or
1** agents, and if it provides one it **must name it**: declared once per pack, never derived
([OQ-D5](#7-decision-ledger)). This narrows [`AGENTS.md`](../../AGENTS.md)'s *"core does not know
what an agent is"*; it does not contradict it. Core still has no agent registry, no `agents`
config key, and no idea what an agent does. What it has, since config surfaces were keyed, is a
name that resolves an address, and the rule bounds it. The cost: a pack supplying two agents
becomes two packs. Nothing ships that shape.

- **The `<agent>/<slot>` address already ships**, for config surfaces: a surface is
  `{agent, name, path}`, addressed `claude/settings`, and `config-overlay` and `config-list`
  target one that way. `exposes`/`to` generalizes that address to every slot.
- **Providing is not addressing.** `packs/matt` contributes to `claude`, `agy`, `codex` and `pi`
  while naming no agent. It addresses agents without providing one, which is the distinction the
  rule draws. Its `files` line is a raw path (`"into": ".pi/agent/themes"`), hardcoding pi's
  layout, because `files` has no slot to name.
- **The identity is not derived from the `program` bin**, although the declared `agent` equals
  it in every shipped agent pack, `oh-omp` included. [`OQ-BA2`](../reference/agent-briefings.md#oq-ba2)
  forbade deriving it: the audience match is against a declared string, compared literally.
  (`packload.binOwner` maps a CLI name to its pack for the profile chain; the audience match
  deliberately does not use it.)
- **The split reaches all three kinds**, `briefing`, `skills` and `files`
  ([OQ-D3](#7-decision-ledger)): the conflation is identical, and fixing `files` alone leaves two
  kinds carrying the flag. Declaring the identity once makes the migration a deletion of `agent`
  from every destination rather than a respelling.
- ⚠ **The identity has four carriers, not three.** The restatements are `briefing`/`skills`
  `agent`, config SURFACE `agent`, and the autonomy-posture config patches'
  `AutonomyPosture.Config[].agent`, matched to surfaces by `(agent, name)` in `packload`. A
  slice 3 that touches only `internal/agentcfg/manifest` leaves that last carrier in place and
  the fold's match key inconsistent with everything else. (`config-overlay`'s `surface` is an
  ADDRESS, not an identity, and is not one of them.)
- **Core's own surfaces keep an explicit owner** (`mise/config`, `user/<slug>`), and what a pack
  declares takes the pack's identity ([SC-D3](#SC-D3)).

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

⚠ **THE TWO BULLETS ABOVE PREDATE [OQ-D5](#7-decision-ledger) AND UNDERSTATE THE COST.** They scope the
change to destinations, and D5 moved the *identity* to pack scope — which reaches the **config
surfaces**: `agent` there is the same field NAME, and
[`packdecl`](../../internal/packdecl/contributes.go) says so at the field. It is read across
`internal/agentcfg` and `internal/entrypoint` — compose, the state render, the host render and its
revert, the overlay prune, the adoption archive, the surface manifest — not the two mechanisms this
section names. (A file count stood here and reproduced at neither of two readings; the number was
the drift, so it is gone. `rg -n 'Agent' internal/agentcfg internal/entrypoint` is the measurement,
and [OQ-D11](#7-decision-ledger), decided as [SC-D3](#SC-D3), is the part of it that is a decision rather
than a count: the same field name does **not** hold the same string.)

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
([OQ-D5](#7-decision-ledger)) and removes the only place a content pack hardcodes another pack's layout.

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

<a id="oq-d6-background"></a>**Background to [OQ-D6](#OQ-D6), below: what the skew test documents, and how its premise has
moved since.** [`agentsurfaces_test.go`](../../internal/packload/agentsurfaces_test.go)'s
`TestShippedAgentPacksKeepIntoForSkew` documents why a shipped pack cannot migrate: `DecodeTolerant` ignores
unknown FIELDS but still validates the entries it keeps, so an entrypoint baked before the change
refuses `{"kind":"briefing"}` with `kind "briefing" needs "into"` — *"a fatal boot, from a
manifest the host staged, unrecoverable without a `just load`."* Its conclusion is explicit:
*"Only a USER's own pack — which no old entrypoint has ever seen — may reach the addressed
shape."* This is [`AGENTS.md`](../../AGENTS.md)'s *"the two halves deploy on different
cadences"* class, and `version.SourceSkew` does not cover it — it compares the host binary to
HEAD, not a staged manifest to a baked entrypoint.

> **Premise changed, not ruled here.** The image has baked no yolo binary since 2026-09-06: the
> entrypoint is mounted from the flake bundle on every launch
> ([`jailprefix.go`](../../internal/cli/run/jailprefix.go)). So "an entrypoint baked before the
> change" and "unrecoverable without a `just load`" no longer describe the tree. (The test
> comment said them too when this note was written; by 2026-09-30 it had been rewritten to name
> where an older entrypoint still comes from: a from-source launch whose binary and tree differ,
> under `YOLO_ALLOW_SOURCE_SKEW=1` or a skew the gate cannot prove.) The shipped manifests are staged from the host `yolo`'s own
> embed, so the pairing that bricks a boot is now a host `yolo` newer than the bundle its
> entrypoint comes from. `just install` publishes the two together, a release ships them
> together, and `version.SourceSkew` refuses a from-source launch whose binary and checkout
> disagree. Whether any supported path still produces that pairing is not measured here, and
> it decides how wide this window is. Separately, today's entrypoint accepts
> `{"kind":"briefing"}` as a broadcast ([briefing P2](../reference/pack-system.md#briefing-p2)),
> so only an entrypoint older than that refuses it.

> **Premise changed again, 2026-09-29, not ruled here.** Since
> [DS-D33](../reference/jail-home.md#DS-D33) the claude and pi packs each ship one ADDRESSED
> CONTENT line, `{"kind": "briefing", "from": "briefing/worktrees.md", "agents": ["claude"]}`
> (`["pi"]` for pi), and `TestShippedAgentPacksKeepIntoForSkew` no longer says *"Only a USER's
> own pack … may reach the addressed shape"*: it admits exactly that line on an agent pack. The
> rule for DESTINATIONS is unchanged — they keep `into` and take no `agents` — so the `exposes`
> migration this question is about stands where it did. The line opens this question's window
> for content: an entrypoint older than the audiences field (`9218bf76`, first released in
> v0.9.0) refuses it and fails the boot. The note above overstates the guard:
> `version.SourceSkew` refuses a from-source launch only when the checkout contains the
> binary's commit, and returns nil when it does not or when the flake tree is not a git
> repository (`TestSourceSkewSilentWithoutProof`). Pairing is what closes the window:
> `just install` and a release each publish both halves together. Measured the same day, the
> two lines widen no window: the readers at v0.8.0 and at `9218bf76`'s parent already refuse
> every launch that selects claude or pi for other reasons (DS-D33, and its doc's Appendix A). Whether
> those two lines may ship on that premise is part of this question, and DS-D33 waits on it.

1. 💬 <a id="OQ-D6"></a>**[OQ-D6](#OQ-D6): what is the MIGRATION WINDOW — and does a shipped pack
   ever get to use the new shape?** **This is the blocker.** Slice 4 migrates the seven agent packs,
   and `TestShippedAgentPacksKeepIntoForSkew` already documents why it cannot
   ([the background above](#oq-d6-background), with how that premise has moved since).
   Both outcomes of ignoring it are bad and one is silent: a migrated pack either bricks an
   older entrypoint's boot, or (if the destination is removed cleanly) that entrypoint drops the
   unknown `exposes`, honors nothing, and delivers **every briefing and every skill NOWHERE** with
   no error anywhere.

   <!-- vantage: question id=OQ-D6 leaning="Ship `exposes` as ADDITIVE and keep `into` on every shipped pack for a release: a jail reads whichever it understands, and only a user's own pack may go exposes-only — which is the boundary the skew test already draws." -->

   _Leaning:_ additive, in the shape the skew test already permits. The new axis is skew-safe for
   free (`DecodeTolerant` is a plain `json.Unmarshal`, so an older build ignores `exposes`), so the
   question is entirely about the DESTINATION a shipped pack keeps. Keeping both for a release
   (`exposes` for the new reader, the `{agent, into}` destination for the old) costs the redundancy
   [OQ-D5](#7-decision-ledger) exists to delete, temporarily and on purpose — and an implementer needs to be
   told whether "temporarily" means a release, a `just load` floor, or forever for shipped packs.
   **A DOUBLE-DELIVERY CHECK BELONGS TO THIS ANSWER:** a build that understands both must not honor
   both, or the new reader and the old destination compose one pack's prose twice — the exact fault
   `GeneratedHostBriefings` was added to stop.

   **Answer:**
   > _(empty — fill in when decided)_

<a id="oq-d9-background"></a>**Background to [OQ-D9](#OQ-D9), below: broadcast.** **Correcting the obvious first guess:** BROADCAST is not at risk. A manifest cannot
declare one today (a bare `{"kind":"briefing"}` is refused for want of `into`), and broadcast is
reachable only through the borrower core SYNTHESIZES for a pack that declares no destination of
the kind — core owns that value, so it survives the schema change untouched. The audience LIST is
the whole loss.

> **Premise changed, not ruled here:** since [briefing P2](../reference/pack-system.md#briefing-p2) a manifest CAN declare broadcast (`{"kind":"briefing"}` is valid), and two content contributions sharing one `from` are refused ([`OQ-PB5`](../reference/pack-system.md#oq-pb5)), so `to` needs a broadcast spelling.

2. 💬 <a id="OQ-D9"></a>**[OQ-D9](#OQ-D9): is `to` a scalar, and where does a MULTI-AGENT audience
   go?** `agents` is a LIST, two entries are supported today and pinned
   (`{"kind":"briefing","from":"prose/claude.md","agents":["claude","pi"]}` validates, and
   `packload` UNIONS audiences across contributions when it dedups skills sources).
   [§2](#2-what-a-kind-is-and-what-a-slot-is)'s `to` is a scalar and [§2](#2-what-a-kind-is-and-what-a-slot-is)
   says `agents` "disappears". So: is `to` a list? Is it two contributions sharing one `from`? Those
   differ in behaviour, not spelling — a union of destinations versus N deliveries with N provenance
   headers. Whether BROADCAST is at risk too, and why `to` now needs a broadcast spelling, is
   [in the background above](#oq-d9-background).

   <!-- vantage: question id=OQ-D9 leaning="`to` takes one address or a list of them, decoded as N deliveries of one source — the same thing two `agents` entries mean today, so no behaviour moves and the provenance stays per destination." -->

   _Leaning:_ a list, decoded as N deliveries of one source, because that is precisely what
   `agents: ["claude","pi"]` means today — nothing moves, and each destination keeps its own
   provenance line. Forcing two entries would be the only alternative that keeps `to` scalar, and it
   makes the common "these rules are for both my agents" case say the same `from` twice.

   **Answer:**
   > _(empty — fill in when decided)_

<a id="oq-d10-background"></a>**Background to [OQ-D10](#OQ-D10), below: why the tree split the two failures.**
[`agentaudience.go`](../../internal/packload/agentaudience.go)'s package comment is a whole essay
on why the second failure is REPORTED rather than FATAL — *"where the remedy is a line in
the owning pack rather than a line in the addressing one"* — because refusing the launch over it
*"would punish the wrong author"* (agent-briefings.md [R1](../reference/agent-briefings.md#ba-r1)/[R4](../reference/agent-briefings.md#ba-r4), and the `files` half of it is
implemented verbatim in the run pipeline). "Refused at load" also lands
the gate where R5 says it must not be — `yolo pack lint` takes a pack root with no config and
cannot know the selected slot vocabulary.

3. 💬 <a id="OQ-D10"></a>**[OQ-D10](#OQ-D10): does an unmatched `to` have ONE severity or two?**
    [§3](#3-the-address-the-agent-never-the-pack) says *"an unmatched `to` is refused at load with a
    fix naming the slots that exist"*. That fuses two failures the tree split ON PURPOSE
    ([why, and where "refused at load" would put the gate](#oq-d10-background)):
    **is the name in the vocabulary?** is FATAL (the addressing author's mistake), while
    **did the name reach a destination of this kind?** is REPORTED. `to: "pi/extensions"` fuses them into one string: `pi`
    unknown is case 1, `pi` known with no `extensions` slot is case 2.

    <!-- vantage: question id=OQ-D10 leaning="Two severities, split exactly where they are split today: an unknown AGENT segment is fatal at the launch pre-flight and `host apply`; a known agent with no such slot is reported, never fatal, and never at `pack lint`." -->

    _Leaning:_ keep both severities and split them on the two SEGMENTS of the address, which is
    where the existing rule already cuts. Reversing R1/R4 is a live option — a slot vocabulary is
    more declarative than an inferred destination, so "refuse the typo" is arguable — but it is a
    REVERSAL of a shipped ruling and has to be written as one.

    **Answer:**
    > _(empty — fill in when decided)_

<a id="oq-d12-background"></a>**Background to [OQ-D12](#OQ-D12), below.** [`pi-pack-extensions.md`](pi-pack-extensions.md)'s
[OQ-1](pi-pack-extensions.md#10-decision-ledger) — *"one files destination per agent; a second
is a load error"* — is no longer merely unretired: it is **built** as of 2026-09-21
([§5.1](#51-what-landed-instead-2026-09-21)). Note which gate can see either of the two new cases: the existing
cross-pack `AgentNameCollisions` fires only when TWO PACKS hold one name, so it is not the guard
against one pack declaring a name twice. And for the mirror-image half,
[§5.1](#51-what-landed-instead-2026-09-21) item 4 refused a second tree, because the
landing path carries the contributing pack and two trees then name one directory — but MERGING
them is a coherent alternative that the host notch already performs by accident and the jail
cannot perform at all (a bind mount unions nothing, so it would need a staged merge).

4. 💬 <a id="OQ-D12"></a>**[OQ-D12](#OQ-D12): how many slots may one agent expose, and what makes
    two of them an error?** `exposes` plus a slot `name` is exactly what makes
    two slots per agent legal, so this doc must retire
    [pi-pack-extensions' built one-destination ruling](#oq-d12-background) EXPLICITLY, and answer the two
    cases the new shape invents: two `exposes` entries with the SAME `name` on one agent, and two
    with different names pointing at the same `into`.

    **And the mirror-image half, which is the one that bit:** how many trees may ONE pack send to
    one slot? Today a second one is refused ([background](#oq-d12-background)), and merging them is
    the coherent alternative. Whichever
    way this goes, it must be one answer for both notches.

    <!-- vantage: question id=OQ-D12 leaning="Many slots per agent, that being the point; a duplicate slot NAME within a pack is a load error like every other duplicate name in this schema, and two names at one `into` is a real arrangement (two shapes, one directory) — reported, not refused." -->

    _Leaning:_ many, with a duplicate `name` refused exactly as duplicate service, profile, provider
    and adapter names already are (authoring path only — the tolerant decoder cannot see siblings
    and a new refusal there is a bricked boot). Two names at one `into` is the less obvious half and
    is probably legitimate: a landing path is not an identity.

    **Answer:**
    > _(empty — fill in when decided)_

## 7. Decision ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| **OQ-D1** | **A second axis, `exposes`.** A `kind` names a contribution; a slot is not one, and making it a kind puts the receiving role inside the supplying vocabulary | 2026-09-20 | [§2](#2-what-a-kind-is-and-what-a-slot-is) | no |
| **OQ-D2** | **The agent (`bin`) name**, `<bin>/<slot>` — content survives swapping which pack supplies the agent. ⚠ Ratifies what `briefing` and `skills` already do in all seven agent packs; only `files` lacked it | 2026-09-20 | [§3](#3-the-address-the-agent-never-the-pack) | partly — the address exists for two of three kinds |
| **OQ-D3** | **All three kinds.** The conflation is identical and fixing `files` alone leaves two kinds carrying the flag. ⚠ The original leaning was WITHDRAWN first (`agents` appears in no shipped manifest, and every `briefing`/`skills` contribution is already unambiguously a destination), then ruled the same way in the opposite direction once [OQ-D5](#7-decision-ledger) made the migration a DELETION rather than a rename | 2026-09-20 | [§3.1](#31-where-the-agent-identity-lives) | no |
| **OQ-D5** | **Declared once per pack (B), stated as a rule about core's vocabulary:** core knows an "agent" only insofar as it identifies a config target; a pack provides **0 or 1** and must NAME the one it provides — declared, never derived | 2026-09-20 | [§3.1](#31-where-the-agent-identity-lives) | no. [OQ-D11](#7-decision-ledger) was the unpriced half (core's own `mise`/`user` surface owners are not agents and have no pack to take an identity from); [SC-D3](#SC-D3) prices it |
| **OQ-D4** | **Provisional.** `exposes`/`accepts` read as the receiving end; `to` is the shortest thing that is not `into`. Settled only until someone proposes better. ⚠ Its own "must not collide with an existing key" is already unmet — `to` is a JSON key three times in this schema (a jail path, a `/ctx` path, a protocol), and `adapts.to` would sit inside the same contribution entry as a slot `to` | 2026-09-20 | [§2](#2-what-a-kind-is-and-what-a-slot-is) | no |
| **OQ-D6** | — **open, and the BLOCKER.** The migration window: a shipped pack that drops `into` bricks an older baked entrypoint's boot, or silently delivers every briefing and skill nowhere. `TestShippedAgentPacksKeepIntoForSkew` already draws the boundary — only a user's own pack may reach the addressed shape. ⚠ The "baked entrypoint" premise has changed: the entrypoint is mounted from the flake bundle every launch, so the window is now a host `yolo` newer than that bundle, unmeasured. ⚠ 2026-09-29: since [DS-D33](../reference/jail-home.md#DS-D33) the claude and pi packs each ship one addressed CONTENT line, so the test's boundary now admits that shape on an agent pack; destinations still keep `into`. That line's window (an entrypoint before v0.9.0 fails the boot) is closed by pairing, not by `version.SourceSkew`, which is silent unless the checkout contains the binary's commit; measured, it widens no window, those old readers already refusing every launch that selects either pack for other reasons. Accepting it waits on this question | — | — | — |
| **OQ-D7** | ✅ **Decided as an implementation choice, [SC-D1](#SC-D1).** It was filed because `accepts` had no vocabulary, no stated consumer and no stated relation to `kind`'s `Combine` | 2026-09-30 | [§2.1](#21-what-a-slot-declares) | no |
| **OQ-D8** | ✅ **Decided as an implementation choice, [SC-D2](#SC-D2).** It asked what the `briefing` and `skills` slots are CALLED | 2026-09-30 | [§2.1](#21-what-a-slot-declares) | no |
| **OQ-D9** | — **open.** `to` is a scalar and `agents` is a list, so a multi-agent audience has no spelling. ⚠ The body's "broadcast is not at risk" premise has changed: since [briefing P2](../reference/pack-system.md#briefing-p2) a manifest can declare a broadcast, so `to` needs a spelling for one too | — | — | — |
| **OQ-D10** | — **open.** "An unmatched `to` is refused at load" collapses two severities the tree split on purpose — unknown NAME fatal, no-such-destination reported — and lands the gate where R5 forbids it (`pack lint` has no config) | — | — | — |
| **OQ-D11** | ✅ **Decided as an implementation choice, [SC-D3](#SC-D3).** It asked what pack-scope identity does to core's own surfaces (`mise/config` has no pack) and to a pack naming a surface for an agent it does not provide | 2026-09-30 | [§3.1](#31-where-the-agent-identity-lives) | no |
| **OQ-D12** | — **open.** How many slots per agent, and what makes two an error. The prior one-per-agent ruling is now BUILT, so this doc must retire it explicitly | — | — | — |
| **Alias-root layout** | **`<slot>/<pack>`, at every notch**, through one resolver — [`pi-pack-extensions.md`](pi-pack-extensions.md) [`OQ-4`](pi-pack-extensions.md#10-decision-ledger) restated where it could be read, since the host notch had violated it since the slot shipped | 2026-09-21 | [§5.1](#51-what-landed-instead-2026-09-21) | **yes** — `packload.SlotLanding`, pinned at both notches |
| <a id="SC-D1"></a>**SC-D1** | *Implementation decision*, [OQ-D7](#7-decision-ledger). **`accepts` is a closed vocabulary used only as a compatibility check, and combining stays the kind's.** It has one value per shape a shipped kind produces. The spellings are the builder's and stay provisional under [OQ-D4](#7-decision-ledger). A contribution whose kind cannot produce the shape its addressed slot accepts is refused by name, naming the slot, what it accepts and the kind sent, at the point where the `to` resolves. How severe that refusal is, beside the address checks, is [OQ-D10](#OQ-D10)'s to rule, not this row's. `Combine` stays on the kind (`files` exclusive, `briefing` concat, `skills` merge), because it is what `Collisions` reads, and a slot restating it would declare one fact twice. A slot named for its kind ([SC-D2](#SC-D2)) takes that kind's shape and needs no `accepts`, and writing a different one there is refused. Every other slot declares one, since a slot that accepted anything would check nothing. Tolerance follows `via`'s closed enum (`packdecl.unknownViaSkip`): the authoring path refuses an unknown value, and the tolerant path skips that slot and reports it. That tolerance lands first, in its own change, as `packdecl` asks of every skew-sensitive closed enum. Why it has one answer: reading (b) alone is an accepted-and-ignored declaration, which this schema refuses everywhere else, and (c) moves the combine fact instead of removing its duplicate | 2026-09-30 | [§2.1](#21-what-a-slot-declares) | no |
| <a id="SC-D2"></a>**SC-D2** | *Implementation decision*, [OQ-D8](#7-decision-ledger). **A slot that receives `briefing` or `skills` is named for its kind, by rule.** Every other slot's name stays local to its agent (`extensions`, `themes`). These two names are reserved: an agent pack declares that kind's destination under that name, and the slot accepts that kind's shape and nothing else ([SC-D1](#SC-D1)). A contribution that carries a kind and no address resolves to the selected agents' slots of that name. That covers core's implicit borrower, the implicit broadcast of every `briefing/` file no line names, and a declared `{"kind":"briefing"}` broadcast ([briefing P2](../reference/pack-system.md#briefing-p2)). Why it has one answer: per-agent names (`CLAUDE.md`, `AGENTS.md`) would make every content pack depend on how its supplier spells a slot, the coupling [OQ-D2](#7-decision-ledger) ruled out so that content survives swapping the pack that supplies an agent, and would leave a kind-only contribution nothing to match | 2026-09-30 | [§2.1](#21-what-a-slot-declares) | no |
| <a id="SC-D3"></a>**SC-D3** | *Implementation decision*, [OQ-D11](#7-decision-ledger). **`Surface.Agent` stays the owner segment of every surface address, and the pack-scope identity fills it for what a pack declares.** Core's own surfaces keep an explicit owner: `mise/config` in `agentcfg.BuiltinManifest`, and `user/<slug>` for host files. They belong to no pack and have no identity to inherit, and deleting the segment would break both, along with every `config-overlay` that targets `mise/config`. So slice 3 means packs stop REPEATING the segment, not that it moves. A pack's own `config` surfaces, its `exposes` entries and its autonomy postures' config patches take its declared identity. Naming a different owner on a surface or slot the pack declares is refused, because under [OQ-D5](#7-decision-ledger) identifying a config target is what makes a name an agent, and a pack provides at most one. This goes past the leaning, which left the explicit segment open to packs; [OQ-D5](#7-decision-ledger)'s words close it. An ADDRESS keeps naming its target explicitly: `config-overlay` and `config-list` `surface`, and a contribution's `to`. That is the provide/address distinction [OQ-D5](#7-decision-ledger) draws with `packs/matt`. An `exposes` on a pack that names no agent is refused on the authoring path (strict validation, `yolo pack lint`), because its slots could never be addressed and this schema refuses accepted-and-ignored declarations. It is not refused on the tolerant path, where a new refusal is a bricked boot ([§5.1](#51-what-landed-instead-2026-09-21) item 2's precedent). Measured 2026-09-30: every `agent` field in `packs/*/pack.json` names its own pack's identity, so no shipped pack loses a capability | 2026-09-30 | [§3.1](#31-where-the-agent-identity-lives) | no |
