---
title: "A pack's AGENTS.md has two readers, and declaring anything takes one of them away"
date: 2026-09-22
status: in-review
tags: [design, packs, briefing, skills, defaults, audiences, zero-ceremony]
summary: "A pack's root AGENTS.md is read by two unrelated audiences — agents working IN the pack's repository, and every agent in every jail that selects the pack — and yolo's delivery defaults undo each other: declaring one narrow briefing silently stops the pack's AGENTS.md from shipping, while broadcasting from a manifest is refused outright. The fix gives shipped prose its own conventional file (BRIEFING.md) that no agent tool reads as repository instructions, makes broadcast declarable, and makes every declaration additive and per-file, so no line in a manifest can switch off a delivery it does not name."
vantage:
  status-chip: true
---

# A pack's AGENTS.md has two readers, and declaring anything takes one of them away

**Status:** DESIGN, 2026-09-22. Nothing built. Evidence verified at `3ac4e8b1`.

> **In short.** yolo reads a pack's content through defaults that override each other, and it reads
> prose from the one filename the rest of the agent ecosystem already reserves for a different
> reader. A pack should ship prose from a file only yolo reads, and every line in its manifest
> should add a delivery — none should be able to remove one it does not name.

**Why it matters.** Both failures are live in one config today. The `matt` pack's house rules
(`matt/AGENTS.md`) reach **no agent**, because the pack also declares one pi-only briefing. The
`matt-craft` repository's *contributor* guide reaches **every agent in every jail**, because it
has no manifest. Neither pack is misconfigured by any rule its author could have found.

**The shape.** Three defaults are replaced. `AGENTS.md` stops being a delivery source. Silence
becomes broadcast in a manifest as well as without one. The implicit delivery becomes per file
instead of per kind.

**Cost.** A pack whose `AGENTS.md` *was* meant to ship must rename it, and the pack is told so. On
the day this lands, the prose in those packs stops reaching agents until the rename.

**Start at [§2.2](#22-declaring-anything-switches-the-default-off)**, where the trap is shown. The
design is [§3](#3-the-design).

**Needs your ruling:** [OQ-PB1](#OQ-PB1), [OQ-PB2](#OQ-PB2), [OQ-PB3](#OQ-PB3).

**Reads with:** [`briefing-audiences.md`](briefing-audiences.md) (its P2, "silence means
broadcast", is ruled and was never true for a manifest — this doc makes it true);
[`slots-and-contributions.md`](slots-and-contributions.md) (`exposes` will re-spell the fields
this doc changes; its [OQ-D8](slots-and-contributions.md#OQ-D8) and
[OQ-D9](slots-and-contributions.md#OQ-D9) are affected, see [§8](#8-how-this-sits-with-the-sibling-designs));
[`pack-briefing-defaults-plan.md`](pack-briefing-defaults-plan.md) (the implementation sketch —
incomplete while any question here is open).

---

## 1. Verdict

These are the principles the rest of the doc spends its length earning. They are numbered so
sibling docs and code comments can cite them.

- **P1. One file, one reader.** yolo never ships a file as pack prose if an agent tool reads it as
  a repository's own instructions. Today that means `AGENTS.md` and `CLAUDE.md`, at any depth.
- **P2. Silence means broadcast, and a manifest can say it.** A `briefing` or `skills`
  contribution that names neither `into` nor `agents` reaches every destination of its kind in the
  selected pack set. This is [`briefing-audiences.md`](briefing-audiences.md#1-verdict-and-the-principles-it-rests-on)'s
  P2, already ruled, finally reachable from a `pack.json`.
- **P3. Declarations add; they never subtract.** A contribution governs the one file it names.
  Nothing a pack declares about one file changes whether a different file is delivered.
- **P4. A named source is the only source.** A declared `from` that cannot be read delivers
  nothing. It never quietly delivers a different file.
- **P5. A destination accepts content; it never supplies it.** A contribution that declares a
  destination (`agent` + `into`) sources nothing, for every kind, as `files` already enforces.
- **P6. Every delivery is visible before the first launch.** That includes the implicit ones, and
  every conventional-looking file that is *not* delivered.

## 2. What happens today

Measured on 2026-09-22 against `3ac4e8b1`, with a freshly built `yolo` (`just build-go`) and the
staged packs of a live jail.

### 2.1 One file, two readers

`AGENTS.md` is the agent ecosystem's filename for *"instructions for an agent working in this
repository"*. The same scan that turns up nothing for `BRIEFING.md` finds `AGENTS.md` referenced
by the installed pi, copilot, codex and opencode packages and 13 times in the Claude Code 2.1.280
binary (`CLAUDE.md`: 212). yolo reads the same filename with a second meaning — *"prose this pack
ships to every agent in every jail"* — and it is the **only** conventional briefing source
([`contributes.go:1286`](../../internal/packdecl/contributes.go#L1286)).

A pack that is also a repository puts both readers on one file, and every fetched pack is one:
the only fetch schemes are `git+ssh://` and `git+https://` (`internal/packsrc/addr.go`). A
`//subdir` address makes the pack root a subdirectory of the repository, so its `AGENTS.md` is a
*nested* one — which agent tools also read, when working in that subtree. `matt-craft` is the live
case:

| File | Written for | Actually reaches |
|---|---|---|
| `matt-craft/AGENTS.md` | Agents working **in** matt-craft (*"do not author new skills directly in this repository"*, *"`just done` runs `scripts/check-skills`"*) | Also every Claude, pi, codex, agy and opencode in every jail selecting the pack, where `just done` means something else entirely |
| `matt-craft/CLAUDE.md` | `@AGENTS.md` — Claude's pointer to the same guide | Not read by yolo (the `CLAUDE.md` fallback is already gone) |

The leak runs the other way as well. A pack author working in their own pack repository gets the
pack's shipped prose loaded as that repository's instructions, whether or not it describes how to
work there.

`skills/` has **no** equivalent collision. The plugin convention reads `skills/` as shipped
content, and an agent's own in-repository skills live under `.claude/skills/`. So P1 is a
briefing-only change.

### 2.2 Declaring anything switches the default off

A pack with no manifest broadcasts its root `AGENTS.md` and `skills/`. The fallback is gated on
the pack declaring **nothing** of that kind — `if !declared` in the jail composer
([`packs.go:1084-1101`](../../internal/cli/run/packs.go#L1084-L1101)), the same gate for skills
([`skillssource.go:137-148`](../../internal/packload/skillssource.go#L137-L148)), and
`borrowingSources`' `len(out) == 0 && !p.declares(kind)` at the host notch
([`mergedest.go`](../../internal/packload/mergedest.go)). All three sites agree, which is how the
trap reaches every notch.

So adding a *narrower* delivery removes a *broader* one it does not mention. Reproduced on
2026-09-22 with nothing but yolo's own scaffold:

1. `yolo pack init scaf` writes `AGENTS.md`, whose text says it *"is appended to every selected
   agent's briefing"*, and then advises: *"To address ONE agent instead, declare a briefing that
   names its audience… `{"kind": "briefing", "from": "prose/claude.md", "agents": ["claude"]}`"*
   ([`pack.go:275-281`](../../internal/cli/pack.go#L275-L281)).
2. Following that advice verbatim, `yolo pack lint` reports `✓ pack ok` and one claim: `briefing →
   claude`.
3. `AGENTS.md` now reaches **nothing**. Lint says nothing about it — no warning and no info line.

`matt` is exactly this shape: a root `AGENTS.md` of house rules, plus
`{"kind": "briefing", "agents": ["pi"], "from": "files/pi-rules.md"}`. Claude gets neither file.
pi gets `pi-rules.md` and not `AGENTS.md`.

### 2.3 Broadcast cannot be written down

The obvious repair — declare the broadcast explicitly — is refused:

```console
$ cat pack.json
{"name":"p","contributes":[{"kind":"briefing","from":"AGENTS.md"}]}
$ yolo pack lint .
✗ pack p: contributes[0]: kind "briefing" needs "into"
```

A contribution with no `agents` must carry an `into`
([`contributes.go:2220`](../../internal/packdecl/contributes.go#L2220)), so a manifest can only
target named agents or named paths. The only spelling that reaches "every agent" is **listing
them**. That is worse than tedious, because it is not portable: naming an agent the jail does not
select is fatal at launch ([`packs.go:463`](../../internal/cli/run/packs.go#L463)) and at `yolo host
apply` ([`apply.go:451`](../../internal/cli/apply.go#L451)), by
[`briefing-audiences.md`](briefing-audiences.md#1-verdict-and-the-principles-it-rests-on)'s P3.
A shared pack listing `["claude","pi","codex"]` refuses every jail that does not select codex.
It also silently skips an agent pack added later.

The corpus says the opposite, in four places:

| Claim | Where | Measured |
|---|---|---|
| *"ABSENT MEANS BROADCAST"* | `Contribution.Agents` doc ([`contributes.go:159`](../../internal/packdecl/contributes.go#L159)) | Refused unless `into` is present |
| P2 *"silence means broadcast"*; *"a contribution with neither is today's zero-ceremony broadcast, unchanged"* | [`briefing-audiences.md` §4.1](briefing-audiences.md#41-the-two-halves-and-why-neither-knows-the-others-business) | Refused |
| *"`agents` is the AUDIENCE, and absent means broadcast"* | [`pack-system.md`](../reference/pack-system.md#briefing) | Refused |
| *"A contribution naming no audience broadcasts"* | [`agent-briefings.md`](../reference/agent-briefings.md#audiences-what-varies-per-destination) | True only with no manifest |

Only [`slots-and-contributions.md`'s OQ-D9](slots-and-contributions.md#OQ-D9) states the build
correctly: *"a manifest cannot declare one today… broadcast is reachable only through the
borrower core SYNTHESIZES."* So P2 is a **ruled behaviour that was never built** for the manifest
case. It is not a new question.

### 2.4 On briefing, `from` is a preference

A briefing's source is a fallback chain, `[from, AGENTS.md]`
([`BriefingCandidates`](../../internal/packdecl/contributes.go#L1311)). A declared `from` that is
absent or whitespace-only delivers the pack's `AGENTS.md` instead, with a warning. `skills` does
the opposite: an absent declared source delivers **nothing**, with a warning
([`skillssource.go:58-83`](../../internal/packload/skillssource.go#L58-L83)). That file's header
states that the precedence *"matches `briefing`'s"* (`:16-18`); it does not.

### 2.5 A destination is also a source

Every shipped agent pack declares its briefing destination as `{agent, into}` with no `from`.
Seven of them do: agy, claude, codex, copilot, omp, opencode and pi. By the chain above, each one
would deliver its **own** root `AGENTS.md` into its own destination. None of them carries one
today, so the overload is latent. `files` already refuses `from` on a destination for exactly this
reason (*"a DESTINATION is a bare SLOT"*,
[`contributes.go`](../../internal/packdecl/contributes.go)), and `briefing` and `skills` never
took the same rule.

### 2.6 Nothing says so

- **`yolo pack lint`** reports `✓` in every trap state above, and lists only declared claims —
  never the implicit broadcast, and never a root `AGENTS.md` that has stopped shipping.
- **Its advisory is wrong in the direction that matters.** On a contribution whose `into` an agent
  pack also owns, it says the line *"adds nothing (drop it…)"*
  ([`pack.go:595`](../../internal/cli/pack.go#L595)). Dropping it *widens* delivery from that one
  agent to all of them.
- **The scaffold teaches the trap** ([§2.2](#22-declaring-anything-switches-the-default-off)).
- **The reference docs state the opposite of the code** ([§2.3](#23-broadcast-cannot-be-written-down)).

## 3. The design

### 3.1 `BRIEFING.md` is what a pack ships; `AGENTS.md` is never read

- **The conventional prose file is `BRIEFING.md`** at the pack root. The exact name and location
  are [OQ-PB1](#OQ-PB1). It is matched case-sensitively, and it is the only conventional briefing
  source at every notch.
- **`AGENTS.md` and `CLAUDE.md` are never read as pack prose**, with or without a manifest, at any
  notch. For a repository pack they are that repository's own instructions, and yolo leaves them
  to the reader they were written for.
- **An explicit `from` whose basename is `AGENTS.md` or `CLAUDE.md`** is [OQ-PB2](#OQ-PB2).
- **The measured premise.** Nothing I could scan reads `BRIEFING.md` as instructions: zero mentions
  in Claude Code 2.1.280, and none in the installed pi, codex, copilot and opencode packages, whose
  `AGENTS.md` references the same scan found. So an agent working in a pack's repository does not
  load the pack's shipped prose, and a consumer's agent does not load the repository's contributor
  guide. **UNMEASURED:** agy and omp were not installed in this jail.
- **A pack that wants one text for both readers** writes it once as `BRIEFING.md` and points the
  in-repository file at it. `CLAUDE.md` can say `@BRIEFING.md`. This is the author choosing the
  dual use, visibly, in a file yolo does not read.

### 3.2 Silence means broadcast, in a manifest too

- A `briefing` or `skills` contribution with **neither `into` nor `agents`** is valid. It reaches
  every destination of its kind that a selected pack declares. It is exactly what the synthetic
  zero-ceremony borrower already does, and the resolver already handles a nil audience
  ([`mergedest.go` `audienceOf`](../../internal/packload/mergedest.go)). Only the validator changes.
- **Portable by construction.** It names no agent, so P3's fatal unmatched-audience rule cannot
  fire. An agent pack selected later receives it with no edit.
- **Degenerate sets.** With zero destinations of the kind, the contribution is reported as
  orphaned, exactly as a manifest-less pack with no agent pack is today. It is never fatal: the
  pack names no agent, so no configuration of `packs` is *wrong*, only unused. With one
  destination, that destination.
- **Duplicates.** Two contributions from one pack that resolve to the same source reach a given
  destination once, as `skills` already dedupes. The provenance header names the pack once.
- **`into` + `agents` together** stays refused, unchanged.
- **`files` is excluded** ([§5](#5-non-goals)). An `into`-less, `agents`-less `files` contribution
  keeps being refused. The refusal says why: `files` has no conventional source and its
  destinations are agent-specific slot types.

### 3.3 A file is governed by the contribution that names it

The implicit delivery becomes **per file**, never per kind:

- **The conventional source (`BRIEFING.md`; `skills/`) is broadcast implicitly unless some
  contribution of that kind names it.** A content contribution that omits `from` names the
  conventional source. So does one that spells it.
- **Once a contribution names it, that contribution alone decides where it goes.** For example,
  `{"kind": "briefing", "agents": ["pi"]}` sends `BRIEFING.md` to pi only.
- **A contribution naming a different file has no effect on the conventional one.** Adding
  `{"from": "files/pi-rules.md", "agents": ["pi"]}` beside a `BRIEFING.md` delivers both files.
  That is the case [§2.2](#22-declaring-anything-switches-the-default-off) gets wrong.
- **The host notch's no-widening promise still holds.** A pack declaring
  `{"kind": "briefing", "into": ".claude/CLAUDE.md"}` names the conventional file by omission, so
  `BRIEFING.md` goes to that one path and nowhere else. `ResolveDestinations`' contract
  (*"a declaration must not be widened into every other agent's directory"*) is kept by the file
  being named, not by the kind being declared.
- **Order-independent.** Governance is a property of the declaration set, so the order of the
  `contributes` list never changes which files are delivered.

### 3.4 A declared source is the only source

- A `briefing` `from` that is absent, not a file, empty, or whitespace-only **delivers nothing
  from that contribution and is reported**, naming the path. That aligns `briefing` with what
  `skills` does today. The fallback chain is deleted.
- **The severity of that report stays what it is today** — a warning at launch, and at lint the
  treatment an unreadable `skills` source already gets. Whether a mistyped reference refuses
  instead is already an open question elsewhere
  ([`reference-mismatch-diagnostics.md`](reference-mismatch-diagnostics.md), roadmap row 9), so
  this doc removes the substitution and leaves the severity to that ruling.
- **An absent conventional file is silent**, as now. Most packs carry no prose.
- **Escaping the pack tree** stays refused outright, unchanged.

### 3.5 A destination ships nothing

- A contribution that declares a destination (`agent` set) **sources nothing**, for `briefing` and
  `skills`, as `files` already enforces. `from` on one is refused with the message `files` uses,
  naming the addressed spelling that ships the pack's own content.
- **No shipped pack is affected:** none of the seven destination-declaring packs carries a root
  `AGENTS.md` or `skills/` ([§2.5](#25-a-destination-is-also-a-source)). An agent pack that wants
  to ship prose to its own agent declares a separate content contribution, addressed to itself.
- This lands P5 on today's schema. `exposes` later changes the spelling and not the behaviour
  ([§8](#8-how-this-sits-with-the-sibling-designs)).

### 3.6 Every delivery, and every refusal to deliver, is shown before launch

- **`yolo pack lint` lists every delivery**, implicit ones included, and says which is which —
  for example, `BRIEFING.md → every agent (implicit broadcast)` beside `files/pi-rules.md → pi`.
- **It names every conventional-looking file it will not ship**, one info line each. A root
  `AGENTS.md` or `CLAUDE.md` gets *"not shipped: this is the repository's own agent instructions —
  ship prose as `BRIEFING.md`"*. Info, not a warning: for a repository pack, not shipping it is
  correct.
- **The advisory at [`pack.go:595`](../../internal/cli/pack.go#L595) states what dropping the line
  does**, which is widen, not nothing.
- **The scaffold** writes `BRIEFING.md`. Its advice shows an addressed contribution *adding* to the
  broadcast, and shows narrowing the broadcast as a contribution that names `BRIEFING.md`.
- **The reference docs and the `Agents` field comment** state the rule in [§3.2](#32-silence-means-broadcast-in-a-manifest-too)–3.3 in the same
  change that builds it.
- **What `lint` cannot see is unchanged.** It takes one pack and no config, so it cannot know the
  selected set. It reports *"every agent"*, and the resolved list is `yolo host apply`'s and the
  launch banner's to print, as today.

### 3.7 The two packs that started this, before and after

| | Today | After, with one `git mv matt/AGENTS.md matt/BRIEFING.md` and no other edit |
|---|---|---|
| Claude's briefing | matt-craft's contributor guide | matt's house rules |
| pi's briefing | `pi-rules.md` and matt-craft's contributor guide | matt's house rules and `pi-rules.md` |
| codex, agy, opencode | matt-craft's contributor guide | matt's house rules |
| `yolo pack lint` on matt | `✓`, one claim | Two deliveries, one implicit; nothing unshipped |
| `yolo pack lint` on matt-craft | `✓` | `AGENTS.md`, `CLAUDE.md`: not shipped (repository instructions) |

matt-craft needs **no** edit: its leak stops because yolo stops reading the file.

## 4. State that already exists

- **Packs whose root `AGENTS.md` was meant to ship.** This covers every `yolo pack init` scaffold
  ever written, the `matt` pack, and an unknown number of third-party packs. On the day this ships
  that prose stops being delivered. How loudly, and whether there is a window, is
  [OQ-PB3](#OQ-PB3). There is no escape hatch either way: a pack's file name is authoring, and a
  hatch would keep the second reader alive behind a variable.
- **The conventional local pack.** `yolo host apply` itself moves an adopted host briefing to
  `~/.config/yolo-jail/local/AGENTS.md`
  ([`applyhostbriefings.go:55-70`](../../internal/cli/applyhostbriefings.go#L55-L70)). yolo chose
  that name, so yolo renames it. The migration writer targets `BRIEFING.md` from the day this
  lands. The next `yolo host apply` renames an existing `local/AGENTS.md` to `BRIEFING.md` and
  reports the move. It refuses if both files exist, naming both, rather than choosing one. Until
  that apply runs, the local pack is covered by the same notice as any other pack under [OQ-PB3](#OQ-PB3).
- **Fetched packs.** A fetched pack whose `AGENTS.md` is a contributor guide (matt-craft) is fixed
  with no action. One whose `AGENTS.md` was consumer prose cannot be renamed by the user. The
  [OQ-PB3](#OQ-PB3) notice names the pack's source, so the report goes upstream.
- **Host destinations yolo already composed.** They are composed wholesale under the ownership
  record, so the next `yolo host apply` recomposes them without the dropped prose. No separate
  cleanup is needed.
- **Existing manifests.** Every manifest valid today stays valid (P2 is a relaxation), with one
  exception: `from` on a destination-declaring `briefing` or `skills` contribution (P5), which no
  shipped pack uses. That one is refused with the corrected spelling, and so is anything
  [OQ-PB2](#OQ-PB2) refuses.

## 5. Non-goals

- **`files` broadcast.** `files` destinations are typed slots (pi extensions, themes), and "every
  agent" has no meaning for them yet. `files` stays `into` or `agents`, unchanged.
- **The severity of a mistyped `from`** belongs to
  [`reference-mismatch-diagnostics.md`](reference-mismatch-diagnostics.md)
  ([§3.4](#34-a-declared-source-is-the-only-source)).
- **The `exposes` schema.** This doc changes behaviour on today's fields; the re-spelling is
  [`slots-and-contributions.md`](slots-and-contributions.md)'s.
- **Other ecosystems' instruction files** (`GEMINI.md`, `.github/copilot-instructions.md`,
  `.cursorrules`). yolo never read them as pack prose, so P1 has nothing to stop there. If yolo
  ever adds a conventional source, P1 governs the choice.
- **What a composed briefing contains around the pack prose** — the jail body, the host prepend,
  the handoff. That is unchanged ([`agent-briefings.md`](../reference/agent-briefings.md)).

## 6. Alternatives considered

| Alternative | Verdict |
|---|---|
| **A wildcard audience** (`"agents": ["*"]`) and keep `AGENTS.md` | Rejected. It fixes [§2.3](#23-broadcast-cannot-be-written-down) with a second spelling of what silence should already mean, and leaves the dual-reader file ([§2.1](#21-one-file-two-readers)) and the suppression ([§2.2](#22-declaring-anything-switches-the-default-off)) exactly as they are. |
| **Keep today's suppression and have lint warn about it** | Rejected. It documents the trap instead of removing it, and a warning on every pack that declares one briefing stops being read in a week. |
| **No convention at all** — prose only through an explicit `from` | Rejected. It deletes the zero-ceremony prose path that [`briefing-audiences.md`](briefing-audiences.md#1-verdict-and-the-principles-it-rests-on) P2 and the pack guide promise. What that path got wrong was its filename and its suppression rule, not its existence. |
| **Read `AGENTS.md` only when the pack is not a git repository** | Rejected. A file's meaning must not depend on whether `.git` is present: every fetched pack is a repository, and a local pack becomes one the moment someone runs `git init`. |
| **Keep `AGENTS.md` and make yolo strip a marked section** | Rejected. It is two readers of one file with a parser in between, and it breaks the first time either reader's author forgets the marker. |

## 7. Risks

| Risk | Mitigation |
|---|---|
| **R1.** A pack whose `AGENTS.md` was consumer prose goes quiet on ship day | The notice in [OQ-PB3](#OQ-PB3), at launch, lint and `host apply`, naming the pack and the rename |
| **R2.** An agent tool later adopts `BRIEFING.md` as an instruction file, recreating the collision | Checked at design time ([§3.1](#31-briefingmd-is-what-a-pack-ships-agentsmd-is-never-read); agy and omp unmeasured). P1 is stated as a rule, so the name is re-chosen if that happens, rather than tolerated |
| **R3.** Broadcast is now one missing field away, so an author meaning "Claude only" who writes nothing reaches everyone | That is already the zero-ceremony behaviour and what P2 ruled. Lint's delivery list ([§3.6](#36-every-delivery-and-every-refusal-to-deliver-is-shown-before-launch)) shows *"every agent"* before any launch |
| **R4.** Per-file governance keys on the resolved source path, so two spellings of one file (`BRIEFING.md`, `./BRIEFING.md`) could be read as two files | Compare cleaned pack-relative paths; the escape check already cleans them |
| **R5.** The two notches diverge again, because the gate lives at three sites today | One predicate, used by all three; see the sketch |

## 8. How this sits with the sibling designs

- **[`briefing-audiences.md`](briefing-audiences.md)** — P2 is **fulfilled, not overturned**. Its
  [`briefing-audiences.md` §4.1](briefing-audiences.md#41-the-two-halves-and-why-neither-knows-the-others-business) sentence *"a contribution with neither is today's zero-ceremony broadcast, unchanged"*
  becomes true. Its P3 (an unmatched audience is fatal) is untouched, and it is the reason
  broadcast has to be spellable at all.
- **[`slots-and-contributions.md`](slots-and-contributions.md)** — two open questions move:
  - [OQ-D9](slots-and-contributions.md#OQ-D9) rests on *"no manifest can declare [broadcast]"*.
    After this lands, one can, so `exposes`' `to` needs a broadcast spelling. Absent `to`, matching
    this doc's absent `agents`, is the one that keeps behaviour.
  - [OQ-D8](slots-and-contributions.md#OQ-D8) leans toward kind-named slots partly so that "core's
    synthetic zero-ceremony borrower can find anything". Per-file governance keeps that borrower,
    so the argument stands.
  - P5 is that doc's role split applied early, on today's fields.
- **[`reference-mismatch-diagnostics.md`](reference-mismatch-diagnostics.md)** owns the severity
  [§3.4](#34-a-declared-source-is-the-only-source) defers.

## 9. What I would build, in order

1. **Visibility first** ([§3.6](#36-every-delivery-and-every-refusal-to-deliver-is-shown-before-launch)),
   with no behaviour change: lint lists implicit deliveries and names a root `AGENTS.md` that
   currently ships or currently does not. This is useful on its own and makes every later step
   observable.
2. **Broadcast becomes declarable** ([§3.2](#32-silence-means-broadcast-in-a-manifest-too)). It is a validator relaxation that loosens nothing
   else, and it unblocks the `matt` pack immediately with `{"kind": "briefing"}`.
3. **Per-file governance, declared-source-only, destination-ships-nothing** ([§3.3](#33-a-file-is-governed-by-the-contribution-that-names-it)–3.5), as one
   change, because each of them edits the same fallback sites.
4. **The rename** ([§3.1](#31-briefingmd-is-what-a-pack-ships-agentsmd-is-never-read), [§4](#4-state-that-already-exists)), after [OQ-PB1](#OQ-PB1)–[OQ-PB3](#OQ-PB3) are ruled, along with the
   scaffold, the local-pack migration writer, and the corrected reference docs.

## 10. What done looks like

- `{"kind": "briefing"}` in a manifest lints clean, and a jail selecting that pack plus claude and
  pi delivers the pack's `BRIEFING.md` to both.
- The [§3.7](#37-the-two-packs-that-started-this-before-and-after) table holds in a real jail:
  Claude's composed briefing carries matt's house rules and not matt-craft's contributor guide.
- `yolo pack init`, followed by the scaffold's own advice, lints as **two** deliveries.
- A declared `from` that does not exist delivers nothing and is reported. No other file arrives in
  its place.
- `from` on a destination-declaring `briefing` is refused, naming the addressed spelling.
- One `yolo host apply` on a home with `local/AGENTS.md` leaves `local/BRIEFING.md`, reports the
  move, and the prose still reaches every agent.
- `pack-system.md`, `agent-briefings.md` and the `Agents` field comment describe the behaviour the
  lint output shows.

## 11. Open Questions

1. 💬 <a id="OQ-PB1"></a>**[OQ-PB1](#OQ-PB1): what is the conventional prose file called, and where
   does it sit?** It decides the one name every pack author types, and the one that has to stay
   free of every agent tool's instruction-file list.

   <!-- vantage: oq id=OQ-PB1 leaning="BRIEFING.md at the pack root: yolo's own word for the thing, read by no agent tool, and one file beside skills/ keeps the zero-ceremony pack two entries." -->

   _Leaning:_ **`BRIEFING.md` at the pack root.** "Briefing" is already yolo's word for exactly
   this, and it is the `kind` name, so `{"kind": "briefing"}` and the file it reads by default say
   the same thing. It is read by no agent tool scanned ([§3.1](#31-briefingmd-is-what-a-pack-ships-agentsmd-is-never-read)). Keeping it at the root keeps the
   zero-ceremony pack at two entries (`BRIEFING.md`, `skills/`). A subdirectory
   (`.yolo/BRIEFING.md`, `yolo/`) buys insulation against a future collision at the cost of
   discoverability, and R2's mitigation already covers the collision.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 <a id="OQ-PB2"></a>**[OQ-PB2](#OQ-PB2): may an explicit `from` name `AGENTS.md` or
   `CLAUDE.md`?** P1 stops the *default* from reading them. This asks whether a manifest may still
   opt into the dual use by name. It decides whether P1 is a default or a rule.

   <!-- vantage: oq id=OQ-PB2 leaning="Refuse, at any depth, naming the rename: an allowed exception is how the dual use returns one pack at a time, and the dual-use author already has a clean spelling (CLAUDE.md saying @BRIEFING.md)." -->

   _Leaning:_ **refuse, for that basename at any depth**, with a message naming the rename. Agent
   tools read those names in subdirectories too, so depth does not make one safe. An allowed
   exception is how the dual use comes back one pack at a time. The one legitimate reason to want
   it — one text for both readers — already has a clean spelling ([§3.1](#31-briefingmd-is-what-a-pack-ships-agentsmd-is-never-read)). The cost is one refusal
   for anyone who wrote `from: "AGENTS.md"` deliberately, who gets the fix in the message.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 <a id="OQ-PB3"></a>**[OQ-PB3](#OQ-PB3): on the day `AGENTS.md` stops being read, what
   happens to a pack that relied on it?** It decides whether a pack's prose can go silent, and
   for how long anything warns about it.

   <!-- vantage: oq id=OQ-PB3 leaning="Hard cut, never a refusal, with a notice at launch, lint and host apply naming the pack and the git mv, shown while the pack has a root AGENTS.md and no BRIEFING.md." -->

   _Leaning:_ **a hard cut, never a refusal, with a notice.** The notice goes out at launch, at
   `yolo pack lint` and at `yolo host apply`. It names the pack, its source, and the `git mv`, and
   it shows whenever a selected pack has a root `AGENTS.md` and no `BRIEFING.md`. It is silenced
   by adding `BRIEFING.md`, or by an explicit contribution naming another source.

   The alternative — a window in which `AGENTS.md` is still read when `BRIEFING.md` is absent —
   keeps the leak open for exactly the packs that cause it, since matt-craft has no `BRIEFING.md`.
   Refusing the launch is ruled out because the pack may be someone else's repository. The cost is
   that a pack author who ignores the notice has silent prose. The notice is a disclosure, and
   under [`report-tiers.md`](../reference/report-tiers.md) a disclosure is never suppressible.
   ⚠ It fires on matt-craft too, whose `AGENTS.md` is *correctly* unshipped. Wording it as *"not
   shipped — if this was meant for consumers, rename it"*, rather than as an error, is what keeps
   it true for both kinds of pack.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| — | *(no rulings yet)* | — | — | — |
