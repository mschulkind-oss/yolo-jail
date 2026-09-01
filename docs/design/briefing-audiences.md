---
title: "Briefing audiences: a pack's prose should be able to name who it is for"
date: 2026-08-31
status: in-review
tags: [packs, briefing, resolution, context]
summary: "A pack's briefing prose reaches every agent in the jail, so a rule that applies to one agent is either broadcast to all of them or deleted. This proposes an optional audience selector on the `briefing` kind, keyed by CLI name (the `bin` namespace `-p` and `pack_profiles` already use) rather than by pack slug — and shows that the host notch needs a filter while the jail notch needs its composition moved inside its own write loop."
---

# Briefing audiences: a pack's prose should be able to name who it is for

**Status:** DESIGN SKETCH, 2026-08-31. Nothing built. One thing already ruled (Decision
Ledger, OQ-BA1).

**The short version.** Every pack's briefing prose is composed **once** and written to
**every** destination, so a pack whose rules apply to one agent must broadcast them to all
of them or drop them. The fix is an optional selector on the `briefing` contribution naming
the **CLI names** it is for — the same `bin` namespace `-p <name> -- <bin>` and
`pack_profiles.<cli>` already key on, and explicitly **not** the pack slug. A destination's
own audience is *derived*, not declared: it is the set of `bin`s installed by the pack that
declared the destination, which is a total function over every pack yolo ships today (§4.2).
The host notch already composes per-destination and needs a filter; the jail notch composes
once before its per-destination loop and needs that composition moved inside it — which is
also how the jail's known one-prose-per-pack limit gets lifted for free (§5).

**The most important section is §4.2** — where a destination's audience comes from. Everything
else follows from it.

**Reads with:** [`agent-briefings.md`](agent-briefings.md) (how briefings are composed and
delivered today), [`profiles-as-pack-variants.md`](profiles-as-pack-variants.md) §2.5 and §8
(the CLI-name namespace, and the universe-vs-selection split this reuses),
[`stringly-typed-references-principle.md`](stringly-typed-references-principle.md) (R1–R5,
which govern any new name-a-component-by-string field),
[`pack-system.md`](pack-system.md) (the `contributes` vocabulary).

---

## 1. Verdict, and the principles it rests on

**Build it, as one optional field on one kind.** No new contribution kind, no new namespace,
no config key. Three principles carry the design:

**P1. The audience namespace is the CLI-name namespace, and there is no second one.**
[`profiles-as-pack-variants.md`](profiles-as-pack-variants.md) §2.5 already settled this for
profiles: `program` and `launch` are `CombineExclusive` by `bin`
([`kinds.go:197-198`](../../internal/packdecl/kinds.go#L197-L198),
[`:100`](../../internal/packdecl/kinds.go#L100)), so a CLI name resolves to at most one pack
by construction, and *"the agents"* are simply the union of the `bin`s the selected packs
install. A briefing audience is the same question about the same set, so it gets the same key.

**P2. Scoping is opt-in and silence means broadcast.** A `briefing` with no selector behaves
exactly as it does today. This is what makes the change safe to land ahead of any pack
adopting it, and it is why the zero-ceremony pack — a bare `AGENTS.md` at a pack root, with
no manifest to put a selector in — keeps working unchanged.

**P3. A name that resolves to nothing is fatal; a name that is merely unselected is a clean
skip.** Straight from [R1/R2](stringly-typed-references-principle.md#6-the-rules) and the
[§3 two-questions table](stringly-typed-references-principle.md#3-a-reference-asks-two-questions-and-only-one-of-them-is-optional).
A selector naming `cloude` is a typo and must stop the launch; a selector naming `codex` in a
jail that did not select codex is an opportunistic pack working as designed.

> [!NOTE]
> **"CLI name" here means the `bin` of a `program`/`requires` contribution — a binary
> basename such as `claude` or `pi`.** *(Term used in this sense throughout; it is
> [`profiles-as-pack-variants.md`](profiles-as-pack-variants.md) §2.5's vocabulary, not
> coined here.)* It is **not** the repo's other sense of *shim*, which is the generated
> blocker in `~/.yolo/bin/block` that refuses `grep -r` and `find` (`GenerateShims`). Nothing
> in this design touches those.

---

## 2. What happens today

Verified against the tree 2026-08-31, `yolo` 0.8.0+691.ga2e13614.

**Composition is destination-agnostic, and that is the whole mechanism.** The run pipeline
composes one string:

- [`prepare.go:154`](../../internal/cli/run/prepare.go#L154) — `ComposePackBriefings(briefingBody, packBriefings)` folds **every** selected pack's prose into one body, each under a `<!-- from pack: NAME -->` header.
- [`prepare.go:170-186`](../../internal/cli/run/prepare.go#L170-L186) — the write loop then iterates every pack's every `briefing` contribution and writes **that same body** to each, at `briefing-<pack>.md` in the staging dir ([`:457`](../../internal/cli/run/prepare.go#L457)).
- [`assemble.go:615-643`](../../internal/cli/run/assemble.go#L615-L643) — each staging file is bind-mounted `:ro` at the contribution's `into`, deduplicated by destination.

The host notch composes the same content by a different route:
[`ComposeHostBriefings`](../../internal/entrypoint/hostbriefing.go#L133) builds a `byPath` map,
appending one attributed section per contribution. **It is already per-destination** — it just
appends every pack to every path.

**The shipped shape.** All six agent packs declare exactly one `program` and exactly one
`briefing`:

| Pack | `program.bin` | `briefing.into` |
| :--- | :--- | :--- |
| `claude` | `claude` | `.claude/CLAUDE.md` |
| `codex` | `codex` | `.codex/AGENTS.md` |
| `copilot` | `copilot` | `.copilot/copilot-instructions.md` |
| `opencode` | `opencode` | `.config/opencode/AGENTS.md` |
| `pi` | `pi` | `.pi/agent/AGENTS.md` |
| `agy` | `agy` | `.gemini/config/AGENTS.md` |

The five loophole-only packs (`audio`, `cgroup-delegate`, `host-processes`, `journal`,
`serial`) declare neither, so they are neither destinations nor audiences.

---

## 3. The gap, and the two spellings that do not work

**`into` does not scope, and the reason is §2, not `from`.** A content pack that declares
`briefing { into: ".claude/CLAUDE.md" }` does not thereby address Claude: composition already
merged its prose into the one body every other destination receives, and the destination dedup
at `assemble.go:632` means its mount is dropped as a duplicate of the `claude` pack's.

> [!WARNING]
> **`yolo config-ref` still carries a stale note that looks like it answers this.** It says a
> jail *"reads briefing prose from a root `AGENTS.md` regardless of `from`"*. That divergence
> was **fixed 2026-08-04** — both notches now resolve through
> [`packload.BriefingProseFor`](../../internal/packload/briefingsource.go#L56) over
> `BriefingCandidates()` ([`pack-system.md`](pack-system.md), §6a-4). It is also about `from`
> (the *source*) and not `into` (the *destination*), so it never bore on scoping at all. The
> stale note is a separate small fix; do not cite it as evidence either way.

**`agents: [...]` on a pack entry is the one spelling already deleted on purpose.**
[`internal/config/packs.go:73-80`](../../internal/config/packs.go#L73-L80) is its tombstone:

> A PACK APPLIES TO THE WHOLE JAIL. There used to be a per-entry `agents` filter ("stage this
> pack only for claude"), and it is gone: it presumed a fixed, known agent list, which is the
> assumption the pack model deletes — a pack that installs an agent is just a pack, and
> nothing in this machinery knows what an agent is.

That rationale has two clauses and **this design kills only the second one.** The second —
*"redundant with where filtering actually happens: staging is per-agent at the DELIVERY end"* —
is true for `skills`, whose delivery genuinely is per-destination, and **false for `briefing`**,
whose delivery is per-destination but whose *composition* is not (§2). The first clause stands,
and it is why the selector must not name an agent as such. Under P1 it names a `bin`, which core
already knows without knowing what an agent is.

---

## 4. The design

### 4.1 The field

An optional selector on the existing `briefing` kind. A pack may declare several, so shared
rules and addressed rules live in one pack:

```jsonc
{ "kind": "briefing", "from": "AGENTS.md" },                              // everyone
{ "kind": "briefing", "from": "prose/claude.md", "bins": ["claude"] },    // claude only
{ "kind": "briefing", "from": "prose/pi.md",     "bins": ["pi"] }         // pi only
```

Absent selector = today's behavior (P2). The `<!-- from pack: NAME -->` provenance header is
unchanged; traceability does not move.

### 4.2 Where a destination's audience comes from — derive, do not declare

This is the crux. A `briefing` contribution names a **path**, not a CLI name, so the selector
`bins: ["claude"]` has nothing to match against until a destination has an **audience**
*(coined here: the set of CLI names a briefing destination serves)*.

**The audience of a destination is the union of the `bin`s installed by the pack that declared
it.** Nothing new is declared and no pack is edited: §2's table shows the derivation is a
**total function over every pack yolo ships**, and it yields exactly the six names a user
would type. It is also the same move [`mergedest.go:20-25`](../../internal/packload/mergedest.go#L20-L25)
already makes for destinations themselves — *"answered by the `packs` list, and not by core
knowing any tool's name."*

Two consequences to state rather than discover:

- **A destination whose declaring pack installs no program is *unaudienced*.** It receives all
  unscoped prose and no scoped prose — it cannot match a selector, because there is nothing to
  match. This is the bare content pack that named a path.
- **The derivation must read the ORIGINAL declaring pack.** `ResolveDestinations`
  ([`mergedest.go:84`](../../internal/packload/mergedest.go#L84)) folds an inferred destination
  into a *copy of the borrowing pack's* declaration, so the audience cannot be recomputed from
  the contribution's new owner. The destination→audience map is built once over the selected
  set, keyed by `into`, before inference runs.

### 4.3 Resolution and severity

Per P3, and per [R5](stringly-typed-references-principle.md#6-the-rules) on placement:

| Question | Checked against | Disposition |
| :--- | :--- | :--- |
| Does this string name a real CLI? | the **universe** — `bin`s of every resolvable pack | **Fatal, always.** Names the string, the declaring pack, the candidate set, and a did-you-mean (R3). |
| Is that CLI selected this launch? | `bin`s of the **selected** packs | Clean skip, reported. |
| Does any selected destination have it in its audience? | the destination→audience map | Clean skip, reported. |

**The gate lands at the host launch pre-flight and at `yolo host apply`**, because those are the
two points holding the full resolved pack set. `yolo pack lint` takes a single pack root with
no config (`yolo pack --help`), so it **cannot** decide the universe question and must not
pretend to: it reports the targeting a contribution declares, and refuses nothing. That is R5's
"move the gate, do not lower the severity" applied literally — the severity lives upstream where
the user has every remedy.

---

## 5. What it costs, and what it lifts

**The host notch is a filter.** `ComposeHostBriefings`
([`hostbriefing.go:133-172`](../../internal/entrypoint/hostbriefing.go#L133-L172)) is already a
per-destination `byPath` loop; the selector check goes beside the `prose == ""` skip at
[`:161`](../../internal/entrypoint/hostbriefing.go#L161). Destination ownership and the
`Packs` list are unaffected — an addressed pack that skips a destination is not an owner of it.

**The jail notch is a structural move**, and it is the whole implementation cost: compose
*inside* the write loop rather than before it, and key the staging file by destination instead
of by pack. Three things follow:

1. `ComposePackBriefings` moves from [`prepare.go:154`](../../internal/cli/run/prepare.go#L154) into the loop at [`:170`](../../internal/cli/run/prepare.go#L170), taking the destination's audience as an argument.
2. `briefingStagingName` ([`:457`](../../internal/cli/run/prepare.go#L457)) is keyed by destination, and `assemble.go`'s mount must use the same key — the two spellings are already coupled by comment ([`assemble.go:611-614`](../../internal/cli/run/assemble.go#L611-L614)) and would now be coupled by content too.
3. The per-destination `after: "host:<path>"` prepend and the `GeneratedHostBriefings` ownership gate ([`prepare.go:175-180`](../../internal/cli/run/prepare.go#L175-L180)) are already inside the loop and need no change.

**What it lifts for free.** [`briefingsource.go:106-108`](../../internal/packload/briefingsource.go#L106-L108)
records a live limit: *"the jail's composition takes one (pack, text) pair per pack … so a pack
declaring two briefing contributions with two different `from` files cannot deliver both there.
The host render is per-DESTINATION and does honor both; making the jail match would mean
composing per destination, which is a larger change than the `from` fix."* That larger change
**is** this design's jail half. The multi-entry shape §4.1 needs is not an extra cost — it is the
same cost, and paying it converges the two notches on one composition model.

**What it complicates.** One more input to briefing composition, and a second thing keyed off the
`bin` namespace — so a future change to how `bin`s resolve now moves briefings as well as
profiles. That coupling is the point (P1), but it should be stated: the namespace is now
load-bearing in two places.

---

## 6. Alternatives considered

| # | Alternative | Verdict |
| :--- | :--- | :--- |
| A1 | **Key by pack slug** — `for: ["claude"]` meaning *the pack named claude*. | **Rejected (maintainer, 2026-08-31).** A slug is a fetch-address artifact that config can rename per entry (`PackEntry.Name`, [`packs.go:88`](../../internal/config/packs.go#L88)), so a reference to it can be broken by a line the referencing pack cannot see. The `bin` namespace is exclusive by construction and is what the user types. |
| A2 | **Declare the audience on the agent pack's own `briefing`** (`audience: ["claude"]`). | **Rejected for v1.** Every agent pack must be edited, and a third-party agent pack that forgets it becomes silently unaudienced — the failure is invisible rather than loud. Kept in reserve as an *override* for the case §4.2's derivation cannot express (see OQ-BA2). |
| A3 | **Use `files` instead** — deliver agent-specific prose as an owned tree at an agent-specific path. | **Rejected.** `files` is `CombineExclusive` ([`kinds.go:218`](../../internal/packdecl/kinds.go#L218)), so it cannot co-exist with the agent pack's briefing at that path, and it bypasses both the composed jail-environment prose and the provenance header. It works today only where an agent reads a *second* file nobody else claims (pi's `APPEND_SYSTEM.md`), which is precisely the split-mechanism problem this design closes. |
| A4 | **Per-entry `only`/`exclude` globs** ([`packs.go:94`](../../internal/config/packs.go#L94)). | **Not applicable.** They filter the pack *tree* by glob — which files stage — not the destination. No combination of them routes one file to claude and another to pi. |
| A5 | **A denylist form** (`not_bins`). | **Deferred, not rejected** — see OQ-BA3. |

---

## 7. Non-goals

- **Not per-project scoping.** A rule that matters in one repo belongs in that repo's own `AGENTS.md`, which is already the right mechanism and needs nothing from yolo. Different axis, and it is not a gap.
- **Not a scoping story for `skills`.** The same misdirection exists there ([`mergedest.go:74-76`](../../internal/packload/mergedest.go#L74-L76): a pack declaring `into: ".claude/skills"` still merges into `.pi/agent/skills`), and it is deliberately out of scope — see OQ-BA4 for the one constraint this design accepts on its account.
- **Not a scoping story for `agents_md_extra`.** It is the user's own config key, and a user who wants it addressed can put the prose in a local pack instead.
- **Not a new contribution kind, config key, or CLI flag.** One optional field on one existing kind.
- **Not a way for a pack to address a destination another pack owns.** The selector filters what a pack's *own* prose reaches; it never lets a pack write somewhere it could not already write.
- **Not a change to provenance or ownership.** `<!-- from pack: NAME -->` is untouched, and a pack that skips a destination does not become an owner of it.

---

## 8. Risks

| Risk | Mitigation |
| :--- | :--- | 
| **R1. A pack addresses a CLI whose pack is unselected, and silently briefs nothing.** The whole point of the pack is then inert with no signal. | The skip is *reported*, not silent (§4.3) — the launch banner already lists what each pack reads and honors; an addressed contribution that matched no destination belongs in the same report. |
| **R2. The jail's staging-key change is a host↔jail contract move.** Renaming the staging file while `assemble.go` still emits the old name is exactly the skew class `AGENTS.md` warns about. | Both spellings are in one package and one commit; `version.SourceSkew` refuses a skewed launch. The existing comment coupling them ([`assemble.go:611-614`](../../internal/cli/run/assemble.go#L611-L614)) becomes a shared helper. |
| **R3. A test that pins the selector's resolver while the call site stays unpinned.** The repo has shipped this shape five times. | The test that must exist: delete the filter call in `ComposeHostBriefings` and in the jail loop, and assert both fail. Per-notch, since §5 shows the two notches change differently. |
| **R4. Unaudienced destinations become a silent hole** as third-party packs that brief without installing a program become common. | §4.2 makes the state explicit and reportable rather than inferred; OQ-BA2 asks whether it needs an escape hatch before that happens. |

---

## 9. What I would build, in order

1. **The destination→audience map**, over the selected pack set, keyed by `into` — built before `ResolveDestinations` runs (§4.2). It is inspectable on its own and both notches consume it.
2. **The host notch filter**, in `ComposeHostBriefings`. Smallest change, immediately observable via `yolo host apply --observe`, and it needs nothing from the jail half.
3. **The jail notch move** — composition into the write loop, staging keyed by destination, `assemble.go` following. This is where the one-prose-per-pack limit lifts (§5).
4. **Resolution and severity** (§4.3) at the two gates R5 selects, with R3-grade diagnostics.
5. **Reporting** — `yolo pack lint` and `yolo pack footprint` state a contribution's targeting, so a pack's briefing claims read as legibly as its file claims.

Steps 1–2 are independently shippable and answer nothing this doc leaves open.

---

## 10. Open Questions

1. 💬 **OQ-BA2: Does the derived audience need a declared override?** §4.2 derives a
   destination's audience from its declaring pack's `bin`s, which is total over every shipped
   pack — but it cannot express a pack that installs two CLIs sharing one briefing file, or an
   agent pack that installs nothing (a `requires`-only pack for a baked binary). A2 is the
   override shape. **This decides whether agent packs get edited at all**, and therefore whether
   a third-party agent pack has to know about this feature to participate in it.

   _Leaning:_ Derive only, for v1. No shipped pack needs the override, `requires` could be
   folded into the derivation cheaply if one appears, and shipping the override up front means
   every agent pack carries a field that restates what core already knows.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-BA3: Allowlist only, or also a denylist?** A house-rules pack that means "everyone
   except pi" writes five names today and silently stops covering the sixth agent the day one
   arrives — the allowlist fails *closed* on new agents, which is wrong for the shared-prose
   case and right for the addressed one. **This decides whether the common case is expressible
   without a maintenance burden.**

   _Leaning:_ Allowlist only for v1, and revisit when a real pack wants the negative. But I hold
   this weakly — the "everyone except one" shape is exactly `matt-local`'s pi section inverted,
   so the demand may already exist on this machine.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-BA4: Is `skills` in or out, and does that change the field now?** `skills` has the
   identical misdirection and larger bytes, but its cost is lazy (a skill costs its description
   until invoked) where a briefing costs its full text on every session. §7 puts it out of scope.
   **What this decides is not whether to build it, but whether the field is named and shaped so
   `skills` can take the same one later** — a `briefing`-only spelling that `skills` cannot reuse
   would mean two vocabularies for one question.

   _Leaning:_ Out of scope to build, in scope to accommodate — the selector is defined on the
   contribution, not on the briefing kind, so `skills` can adopt it without a new name.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 🤷 **OQ-BA5: `bins` or `for`?** `bins: ["claude"]` names its universe in the field name,
   which is what [R4](stringly-typed-references-principle.md#6-the-rules) asks of an open
   semantic slot and what `surface` already does. `for: ["claude"]` reads better in prose and
   survives OQ-BA4 without renaming if the audience ever stops being a bin.

   _Leaning:_ `bins`, narrowly — this repo has just been burned by references that did not say
   what they referenced. But it is a naming call and yours to make.

   **Answer:**
   > _(empty — fill in when decided)_

---

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| OQ-BA1 | Key the selector by **CLI name (`bin`)**, not by pack slug — the namespace `-p <name> -- <bin>` and `pack_profiles.<cli>` already use | 2026-08-31 | §1 P1, §4.1, §6 A1 |
