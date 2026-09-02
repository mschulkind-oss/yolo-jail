---
title: "Audiences: a pack's content should be able to name who it is for"
date: 2026-08-31
status: accepted
tags: [packs, briefing, skills, resolution, context]
summary: "A pack's briefing prose and skills reach every agent in the jail, so content that applies to one agent is either broadcast to all of them or deleted. An optional `agents` selector on `briefing` and `skills`, keyed by launcher command (the `bin` namespace `-p` and `use_profiles` already use) rather than by pack slug, matched against an `agent` identity the destination declares for itself — so a content pack names its audience and never a path. Naming an agent the jail has not enabled is fatal, one name has exactly one owning pack, and all seven questions are ruled."
---

# Audiences: a pack's content should be able to name who it is for

**Status:** DECIDED, 2026-08-31 — all seven questions this doc asked are settled (Decision
Ledger, at the foot). Nothing built; §9 is the build order.

**The short version.** Every pack's briefing prose is composed **once** and written to
**every** destination, so a pack whose rules apply to one agent must broadcast them to all
of them or drop them. Skills have the same defect and take the same fix. The
answer is an optional `agents` selector on `briefing` and `skills` — a list of **launcher
commands**, which is the same `bin` namespace `-p <name> -- <bin>` and `use_profiles.<cli>`
already key on, and explicitly **not** the pack slug. The destination it matches against
**declares** that name as `agent`, exactly as a config surface already does — nothing is derived,
because nothing in the `-p` chain derives anything either (§4.2). A content pack names only the
audience, never a path: that constraint (P4) is what turns `into` from a required field into one
the selector replaces (§4.1). Naming an agent this jail has not enabled is **fatal**, with no
laxer tier (P3), and a name has exactly **one** owning pack, which provides all of that agent's
plumbing (P5). The host notch already composes per-destination and needs a filter; the jail notch composes
once before its per-destination loop and needs that composition moved inside it — which is
also how the jail's known one-prose-per-pack limit gets lifted for free (§5).

**The most important section is §4.2** — where a destination's identity comes from, and why an
earlier draft got it wrong. Everything else follows from it.

**Reads with:** [`agent-briefings.md`](agent-briefings.md) (how briefings are composed and
delivered today), [`profiles-as-pack-variants.md`](profiles-as-pack-variants.md) §2.5 and §8
(the CLI-name namespace, and the universe-vs-selection split this reuses),
[`stringly-typed-references-principle.md`](stringly-typed-references-principle.md) (R1–R5,
which govern any new name-a-component-by-string field),
[`pack-system.md`](pack-system.md) (the `contributes` vocabulary).

---

## 1. Verdict, and the principles it rests on

**Build it, as one optional field on `briefing` and `skills`.** No new contribution kind, no
new namespace, no config key. Five principles carry the design:

**P1. The audience namespace is the CLI-name namespace, and there is no second one.**
[`profiles-as-pack-variants.md`](profiles-as-pack-variants.md) §2.5 already settled this for
profiles: `program` and `launch` are `CombineExclusive` by `bin`
([`kinds.go:243-244`](../../internal/packdecl/kinds.go#L243-L244),
[`:100`](../../internal/packdecl/kinds.go#L100)), so a CLI name resolves to at most one pack
by construction, and *"the agents"* are simply the union of the `bin`s the selected packs
install. An audience is the same question about the same set, so it gets the same key.

**P2. Scoping is opt-in and silence means broadcast.** A `briefing` with no selector behaves
exactly as it does today. This is what makes the change safe to land ahead of any pack
adopting it, and it is why the zero-ceremony pack — a bare `AGENTS.md` at a pack root, with
no manifest to put a selector in — keeps working unchanged.

**P3. The vocabulary is the ENABLED packs, and anything else is fatal.** *(Maintainer,
2026-08-31: "the choices are only the enabled packs. otherwise fatal.")* There is no second,
laxer tier for a name that exists somewhere but is not here — `agents: ["cloude"]` and
`agents: ["codex"]` in a jail that did not select codex fail the same way, because from the
jail's point of view they are the same mistake: the prose names an audience this jail does not
have. That is [R2](stringly-typed-references-principle.md#6-the-rules) taken at its word —
*"explicit opt-in for permissive selection"* — and this field has no such opt-in, so the strict
reading is the only one available.

**P4. A content pack names its audience; it never names a path.** *(Maintainer, 2026-08-31:
"a pack that needs to add claude-specific briefing shouldn't need to know anything about where
claude puts its briefings.")* Where an agent reads — prose or skills — is the agent pack's
business and changes when that agent changes; a house-rules pack that hardcoded
`.claude/CLAUDE.md` would be coupled to a fact it has no way to keep current. This is not a
nicety — §4.1 shows it is the difference between a selector that works and one that delivers
nowhere.

**P5. A name has exactly ONE owner, and that owner provides all of the agent's plumbing.**
*(Maintainer, 2026-08-31.)* There is one `claude`. A pack that provides it provides everything
that goes with it — where its briefing lands, where its skills land, its config surfaces, its
launch flags — whether it installs the binary (`program`) or asserts one already there
(`requires`). Two packs cannot both be the claude pack:

> Imagine you could have one of two packs: `claude-official` and `claude-matt-fork`. Both launch
> with `yolo -- claude`. I want to apply `-p claude=zai` for either. They clearly can't both be
> enabled.

The exclusivity that makes `-p claude=zai` unambiguous is the same exclusivity that makes
"deliver this prose to claude" unambiguous. It is one rule, not two, and §4.2 is where it lands
in the code.

**The fields are spelled `agent` / `agents`; the VALUE is still the bin.** *(Maintainer,
2026-08-31: "they are bins, but users think of them by their launcher command and call them
agents.")* Nothing about the namespace moves — the string is the launcher command, resolved the
way P1 says. Only the word a pack author types changes, and it changes toward the one already in
the tree for this exact string: a config surface names its owner
[`"agent": "pi"`](../../internal/agentcfg/manifest/load.go#L27), not `"bin": "pi"`. Singular for
the identity a destination declares, plural for the audience a contribution names.

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
- [`assemble.go:660-688`](../../internal/cli/run/assemble.go#L660-L688) — each staging file is bind-mounted `:ro` at the contribution's `into`, deduplicated by destination.

The host notch composes the same content by a different route:
[`ComposeHostBriefings`](../../internal/entrypoint/hostbriefing.go#L133) builds a `byPath` map,
appending one attributed section per contribution. **It is already per-destination** — it just
appends every pack to every path.

**The shipped shape.** All six agent packs declare exactly one `program` and exactly one
`briefing`:

| Pack | `program.bin` | surface `agent` id | `briefing.into` |
| :--- | :--- | :--- | :--- |
| `claude` | `claude` | `claude` | `.claude/CLAUDE.md` |
| `codex` | `codex` | `codex` | `.codex/AGENTS.md` |
| `copilot` | `copilot` | `copilot` | `.copilot/copilot-instructions.md` |
| `opencode` | `opencode` | `opencode` | `.config/opencode/AGENTS.md` |
| `pi` | `pi` | `pi` | `.pi/agent/AGENTS.md` |
| `agy` | `agy` | `agy` | `.gemini/config/AGENTS.md` |

**Read the middle column against the second: every agent pack already writes its own identity
out by hand, and it already equals its `bin`.** That is §4.2's whole foundation. The third
column is the one thing a `briefing` cannot say anything about — it names a path and nothing
else. The five loophole-only packs (`audio`, `cgroup-delegate`, `host-processes`, `journal`,
`serial`) declare none of the three.

---

## 3. The gap, and the two spellings that do not work

**`into` does not scope, and the reason is §2, not `from`.** A content pack that declares
`briefing { into: ".claude/CLAUDE.md" }` does not thereby address Claude: composition already
merged its prose into the one body every other destination receives, and the destination dedup
at `assemble.go:677` means its mount is dropped as a duplicate of the `claude` pack's.

> [!WARNING]
> **`yolo config-ref` still carries a stale note that looks like it answers this.** It says a
> jail *"reads briefing prose from a root `AGENTS.md` regardless of `from`"*. That divergence
> was **fixed 2026-08-04** — both notches now resolve through
> [`packload.BriefingProseFor`](../../internal/packload/briefingsource.go#L56) over
> `BriefingCandidates()` ([`pack-system.md`](pack-system.md), §6a-4). It is also about `from`
> (the *source*) and not `into` (the *destination*), so it never bore on scoping at all. The
> stale note is a separate small fix; do not cite it as evidence either way.

**`agents: [...]` on a pack entry is the one spelling already deleted on purpose.**
[`internal/config/packs.go:75-82`](../../internal/config/packs.go#L75-L82) is its tombstone:

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

### 4.1 The two halves, and why neither knows the other's business

There are two declarations, in two packs, and **the whole design is that neither needs what the
other holds** (P4).

```jsonc
// packs/claude/pack.json — the AGENT pack. It owns the name and the path,
// because both are facts about claude. `into` is what it declares today; `agent` is new.
{ "kind": "briefing", "into": ".claude/CLAUDE.md", "agent": "claude" }
```

```jsonc
// any content pack — house rules. It names WHO, and nothing else.
{ "kind": "briefing", "from": "AGENTS.md" },                              // everyone (today's behavior)
{ "kind": "briefing", "from": "prose/claude.md", "agents": ["claude"] },  // claude only — no path
{ "kind": "skills",   "from": "skills/claude",   "agents": ["claude"] },  // same field, same rule
{ "kind": "briefing", "from": "prose/pi.md",     "agents": ["pi"] }       // pi only — no path
```

**`skills` takes the same field, and the parallel is exact** *(maintainer, 2026-08-31: "they're
parallel in every way here and basically come for free")*. Both kinds have a conventional source
and a required `into`; both are merged from many packs into destinations the agent packs name;
both misdirect today for the same reason. A Claude-specific skill is broadcast to `.pi/agent/skills`
right now ([`mergedest.go:74-76`](../../internal/packload/mergedest.go#L74-L76)). Everything below
is written about `briefing` because that is where the cost is loudest — a briefing is read in full
every session — but every rule in §4.2 and §4.3 reads the same with `skills` substituted.

**`into` and `agents` are two answers to one question, and a contribution gives exactly one.**
That is not a stylistic rule; it falls out of what the validator already says. `into` is
**required** on `briefing` today ([`contributes.go:1222`](../../internal/packdecl/contributes.go#L1222)),
and its own rationale states the reason it cannot be defaulted
([`:1214-1218`](../../internal/packdecl/contributes.go#L1214-L1218)):

> *"A source has one right answer per KIND; a destination has one right answer **per AGENT**, so
> inferring it means inferring the agent set — which is what the `packs` list is for."*

**The selector supplies precisely that missing input.** A contribution that says `agents: ["claude"]`
has named the agent, so its destination becomes inferable — and `into` must then be *omitted*,
because declaring both would be the content pack asserting a path it has no business knowing.
A contribution with neither is today's zero-ceremony broadcast, unchanged.

Two mechanism consequences follow, and they are the concrete content of P4:

- **`declares` must test for a DESTINATION, not for the kind.** [`mergedest.go:141-148`](../../internal/packload/mergedest.go#L141-L148) returns true for *any* contribution of the kind, so an `agents`-only briefing would look like a pack that named its own destinations and skip inference entirely — **prose delivered nowhere**. It has to test `Into != ""`.
- **`borrowedDestinations` filters by the selector.** [`mergedest.go:174-192`](../../internal/packload/mergedest.go#L174-L192) already hands a silent pack every other pack's briefing `into`, which is exactly the path-free routing P4 wants; the selector narrows that list to destinations whose owner declared a matching `agent`.

The `<!-- from pack: NAME -->` provenance header is unchanged; traceability does not move.

### 4.2 Where a destination's identity comes from — declared, like every other kind

This was the crux and it turned out not to be one. A `briefing` contribution names a **path**,
not a CLI name, so `agents: ["claude"]` needs something to match against — and an earlier draft of
this doc proposed *deriving* it, computing each destination's audience from the `bin`s its
declaring pack installs. **That was wrong, and the `-p` mechanism is the evidence** *(corrected
2026-08-31 — see OQ-BA2 in the Decision Ledger)*.

**Nothing in the profile chain derives an identity. The string is typed, carried, and matched
against a string a pack declared about itself.** End to end:

| Step | Code | What happens to the name |
| :--- | :--- | :--- |
| produce | [`assemble.go:862`](../../internal/cli/run/assemble.go#L862) | `filepath.Base(o.Args[0])` — literally the word the user typed after `--`, stored as a map key |
| carry | [`assemble.go:845`](../../internal/cli/run/assemble.go#L845) | `for k, v := range o.UseProfiles` — copies the map the user wrote; the value is carried as a plain string, never derived |
| match | [`luahook/derive.go:163`](../../internal/agentcfg/luahook/derive.go#L163) | `fn, ok = envs[ctx.Agent]` — a map lookup keyed by the literal agent string, against whatever name a pack registered itself under |
| match | [`packs/claude/derive.lua:5`](../../packs/claude/derive.lua#L5) | `ctx.use_profiles.claude` — **the pack hardcodes its own name** |

**There is no `bin`→pack index in the tree** (checked 2026-08-31, repinned 2026-09-02), because nothing needs one. And
identity-by-declaration is not a quirk of profiles — it is the house style. A config surface's
owner is a declared string, [`SurfaceDTO.Agent`](../../internal/agentcfg/manifest/load.go#L27),
keyed as [`SurfaceKey{Agent, Name}`](../../internal/agentcfg/manifest/manifest.go#L208), and all
six agent packs write it out by hand — `"agent": "pi"`, `"agent": "claude"` — identical to their
own `bin` in every case.

**So the design is: the agent pack declares its briefing destination's `agent`, the same field
name its config surfaces already use** (§4.1's first block). A selector then matches that string
directly. No map built over the selected set, no derivation, no new concept — and the three
complications the derived version dragged in all disappear with it:

- The `ResolveDestinations` wrinkle is gone. Inference folds a borrowed destination into a *copy
  of the borrowing pack's* declaration ([`mergedest.go:84`](../../internal/packload/mergedest.go#L84)),
  which would have broken a derivation that had to read the original declaring pack. A declared
  string travels with the contribution and is unaffected.
- **Unaudienced destinations** are no longer a special state to define — a destination that
  declares no identity is simply never named by any selector.
- The audience stops being a thing to compute, so it stops being a thing that can be computed
  differently at the two notches.

> [!NOTE]
> **The cost this moves rather than removes: six pack.json files gain a field, and a
> third-party agent pack must declare one to be addressable.** That is the objection the derived
> version was invented to dodge, and it is not worth dodging — declaring identity is what
> `program` (`bin`), `config` (`agent`) and `state` (`at`) all already require, and a pack that
> omits it fails the same way a pack that omits `bin` does. What it is NOT allowed to do is fail
> *silently*, which is §4.3's job.

**Ownership is per NAME, not per kind (P5), and two packs claiming one name is fatal.** So the
check is not "two `briefing` contributions declared `agent: claude`" but "two packs claimed
`claude` **at all**" — across `program`, `requires`, `launch`, `briefing` and `skills` together.
A same-pack repeat stays normal and legal: `packs/copilot` declares `copilot` on both `program`
and `launch`, and one pack claiming its own name in five kinds is one pack owning one name.
`Collisions` already ignores that case — it skips any target whose claimants are a single pack
([`footprint.go:425-427`](../../internal/packload/footprint.go#L425-L427)) — so what changes is
the KEY, from `(kind, target)` to the name itself for this one namespace.

This also needs its own pass rather than falling out of the generic one:
[`Collisions`](../../internal/packload/footprint.go#L399) keys claims by `(kind, target)` and
**skips every kind that is not `CombineExclusive`** ([`:435`](../../internal/packload/footprint.go#L435)),
and `briefing` is `CombineConcat` by design — several packs contributing prose at one path is the
whole point. So an `agent` claim inside it is invisible to that loop. The precedent is exact and
already in the file twice: `pluginNameCollisions` exists because *"the generic loop above cannot
see this (the claim's kind is skills, which merges by design), so it is its own pass"*
([`:460-461`](../../internal/packload/footprint.go#L460-L461)), and `LoopholeNameCollisions`
([`:565`](../../internal/packload/footprint.go#L565)) is the same shape. The agent-name namespace
is a third instance of it — and the widest, since it spans five kinds rather than sitting inside
one.

### 4.3 Resolution and severity

**One question, one answer** (P3). An earlier draft had three rows here, splitting "is this a
real name?" from "is it selected?" and skipping cleanly on the second. That split is gone:

> **Does every name in `agents` belong to a pack enabled in this jail? If not, refuse the
> launch.** The candidate set is the enabled packs — nothing wider.

The diagnostic still does the work [R3](stringly-typed-references-principle.md#6-the-rules) asks
of it: name the offending string, the declaring pack, **the enabled agents as the candidate
list**, and a did-you-mean. What it must not do is print two different messages for `cloude` and
for `codex`-in-a-jail-without-codex, because the remedy is the same either way — fix the name, or
enable the pack.

**The gate lands at the host launch pre-flight and at `yolo host apply`**, because those are the
two points that hold the enabled set. `yolo pack lint` takes a single pack root with no config
(`yolo pack --help`), so it **cannot** decide the question and must not pretend to: it reports
the targeting a contribution declares and refuses nothing. That is R5's "move the gate, do not
lower the severity" applied literally — the severity lives upstream where the user has every
remedy.

> [!NOTE]
> **This is why there is no denylist.** An `except: ["pi"]` form exists to spare an author from
> listing names — but under P3 the author must name only enabled agents anyway, so the list is
> already bounded by the jail rather than by the set of agents that exist. A negative form would
> buy nothing and would need its own answer to "except an agent that is not enabled", which under
> P3 is a refusal. Allowlist only.

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
2. `briefingStagingName` ([`:457`](../../internal/cli/run/prepare.go#L457)) is keyed by destination, and `assemble.go`'s mount must use the same key — the two spellings are already coupled by comment ([`assemble.go:656-659`](../../internal/cli/run/assemble.go#L656-L659)) and would now be coupled by content too.
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
| A1 | **Key by pack slug** — `for: ["claude"]` meaning *the pack named claude*. | **Rejected (maintainer, 2026-08-31).** A slug is a fetch-address artifact that config can rename per entry (`PackEntry.Name`, [`packs.go:90`](../../internal/config/packs.go#L90)), so a reference to it can be broken by a line the referencing pack cannot see. The `bin` namespace is exclusive by construction and is what the user types. |
| A2 | **Derive the destination's identity** from the `bin`s its declaring pack installs, so no pack is edited. | **Rejected 2026-08-31 (OQ-BA2), and it was this doc's own first proposal.** Nothing in the `-p` chain derives an identity — the name is typed, carried as a map key, and matched against a string the pack declared about itself, down to `derive.lua` hardcoding `ctx.use_profiles.claude`. There is no `bin`→pack index to derive through, and inventing one for briefings alone would make this the only kind whose owner is inferred. §4.2. |
| A3 | **Use `files` instead** — deliver agent-specific prose as an owned tree at an agent-specific path. | **Rejected.** `files` is `CombineExclusive` ([`kinds.go:264`](../../internal/packdecl/kinds.go#L264)), so it cannot co-exist with the agent pack's briefing at that path, and it bypasses both the composed jail-environment prose and the provenance header. It works today only where an agent reads a *second* file nobody else claims (pi's `APPEND_SYSTEM.md`), which is precisely the split-mechanism problem this design closes. |
| A4 | **Per-entry `only`/`exclude` globs** ([`packs.go:96`](../../internal/config/packs.go#L96)). | **Not applicable.** They filter the pack *tree* by glob — which files stage — not the destination. No combination of them routes one file to claude and another to pi. |
| A5 | **A denylist form** (`except: ["pi"]`). | **Rejected 2026-08-31 (OQ-BA3).** Under P3 an author may name only ENABLED agents, so an allowlist is already bounded by the jail rather than by the set of agents that exist — the burden a denylist relieves does not arise. §4.3. |

---

## 7. Non-goals

- **Not per-project scoping.** A rule that matters in one repo belongs in that repo's own `AGENTS.md`, which is already the right mechanism and needs nothing from yolo. Different axis, and it is not a gap.
- **Not a scoping story for `agents_md_extra`.** It is the user's own config key, and a user who wants it addressed can put the prose in a local pack instead.
- **Not a new contribution kind, config key, or CLI flag.** One optional field on one existing kind.
- **Not a way for a pack to address a destination another pack owns.** The selector filters what a pack's *own* prose reaches; it never lets a pack write somewhere it could not already write.
- **Not a change to provenance or ownership.** `<!-- from pack: NAME -->` is untouched, and a pack that skips a destination does not become an owner of it.

---

## 8. Risks

| Risk | Mitigation |
| :--- | :--- | 
| **R1. A pack addresses a CLI whose pack is unselected, and silently briefs nothing.** The whole point of the pack is then inert with no signal. | The skip is *reported*, not silent (§4.3) — the launch banner already lists what each pack reads and honors; an addressed contribution that matched no destination belongs in the same report. |
| **R2. The jail's staging-key change is a host↔jail contract move.** Renaming the staging file while `assemble.go` still emits the old name is exactly the skew class `AGENTS.md` warns about. | Both spellings are in one package and one commit; `version.SourceSkew` refuses a skewed launch. The existing comment coupling them ([`assemble.go:656-659`](../../internal/cli/run/assemble.go#L656-L659)) becomes a shared helper. |
| **R3. A test that pins the selector's resolver while the call site stays unpinned.** The repo has shipped this shape five times. | The test that must exist: delete the filter call in `ComposeHostBriefings` and in the jail loop, and assert both fail. Per-notch, since §5 shows the two notches change differently. |
| **R4. A third-party agent pack that declares no identity is not addressable**, and its users cannot tell why a scoped pack skipped it. | The remedy is one field in that pack, and §4.3's reporting names the destination that declared nothing whenever a selector finds no match. This is the cost §4.2 accepts in exchange for deleting the derivation, not one it hides. |

---

## 9. What I would build, in order

1. **`agent` on `briefing`**, plus the six one-line additions to the shipped agent packs (§4.1), and its collision pass (§4.2). Inert as *routing* — nothing reads it for delivery yet — but the ownership check is real from day one, which is the half that wants to land before anyone depends on a name.
2. **The host notch filter**, in `ComposeHostBriefings`. Smallest change, immediately observable via `yolo host apply --observe`, and it needs nothing from the jail half.
3. **The path-free half** — `agents` on a contribution, `into` refused alongside it and required without it, `declares` testing `Into != ""`, and `borrowedDestinations` filtered by the selector (§4.1). This is what makes P4 true rather than aspirational, and step 2 is worth little without it.
4. **The jail notch move** — composition into the write loop, staging keyed by destination, `assemble.go` following. This is where the one-prose-per-pack limit lifts (§5).
5. **Resolution and severity** (§4.3) at the two gates R5 selects, with R3-grade diagnostics.
6. **`skills`, by substitution** — the same field, the same resolution, the same refusal. Left last only because a briefing's cost is read every session and a skill's is not; nothing in it is a new decision.
7. **Reporting** — `yolo pack lint` and `yolo pack footprint` state a contribution's targeting, so a pack's claims read as legibly as its file claims.

Steps 1–2 are independently shippable and answer nothing this doc leaves open. **Step 3 is the one
to review hardest**: it makes `into` conditional on a field, and a briefing that silently delivers
nowhere is the exact failure mode `declares` would produce if that change were missed.

---

## 10. Open Questions

**None.** Every question this doc asked is settled — see the Decision Ledger below, and §1's
principles for the rulings that shaped the body. What is left is build order (§9).

---

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| OQ-BA1 | Key the selector by **CLI name (`bin`)**, not by pack slug — the namespace `-p <name> -- <bin>` and `use_profiles.<cli>` already use | 2026-08-31 | §1 P1, §4.1, §6 A1 |
| OQ-BA6 | The identity is **declared by the agent pack, which OWNS that name** — two packs claiming one `bin` is a **fatal** error. Needs its own collision pass, since `briefing` is `CombineConcat` and the generic loop skips non-exclusive kinds (`pluginNameCollisions`/`LoopholeNameCollisions` are the precedent). | 2026-08-31 | §4.1, §4.2 |
| OQ-BA5 | The fields are **`agent`** (identity) and **`agents`** (selector) — not `bins`, not `for`. The value is still the bin; the spelling follows what users call the thing and what a config surface already calls it. | 2026-08-31 | §1 (after P4), §4.1 |
| OQ-BA7 | Ownership is **per NAME, across kinds** — one `claude`, one owner, which provides all of that agent's plumbing (briefing, skills, surfaces, launch flags) whether it `program`s the binary or `requires` it. The "briefing-only pack owned by a second pack" case this question posed does not exist: `claude-official` and `claude-matt-fork` both launch as `claude` and cannot both be enabled. Collision key moves from `(kind, target)` to the name. | 2026-08-31 | §1 P5, §4.2 |
| OQ-BA3 | **Allowlist only, and the candidate set is the ENABLED packs — anything else is fatal.** No universe/selection split and no denylist: naming an agent this jail does not have is the same mistake as a typo, with the same remedy. | 2026-08-31 | §1 P3, §4.3, §6 A5 |
| OQ-BA4 | **`skills` is IN**, taking the same `agents` field and every rule in §4.2–§4.3 unchanged. The two kinds are parallel — conventional source, required `into`, many packs merging into destinations agent packs name — so this is one mechanism, not two. | 2026-08-31 | §4.1, §9 |
| OQ-BA2 | The destination's identity is **declared, not derived**. This doc's first draft proposed deriving it from the declaring pack's `bin`s; the `-p` chain derives nothing (name typed → map key → literal comparison, with `derive.lua` hardcoding its own name), there is no `bin`→pack index, and identity-by-declaration is what `program`, `config` and `state` all already do. Deleting the derivation also deletes the `ResolveDestinations` wrinkle and the unaudienced-destination state. | 2026-08-31 | §4.2, §6 A2 |
