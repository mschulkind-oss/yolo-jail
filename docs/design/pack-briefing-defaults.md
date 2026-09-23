---
title: "A pack's AGENTS.md has two readers, and declaring anything takes one of them away"
date: 2026-09-22
status: accepted
tags: [design, packs, briefing, skills, defaults, audiences, zero-ceremony, prior-art]
summary: "DECIDED. A pack's root AGENTS.md is read by two unrelated audiences — agents working IN the pack's repository, and every agent in every jail that selects the pack — and yolo's delivery defaults undo each other: declaring one narrow briefing silently stops the pack's AGENTS.md from shipping, while broadcasting from a manifest is refused outright. AGENTS.md stays the repository's (ruled). Shipped prose moves to a location only yolo reads — a `briefing/` component directory (ruled), the shape of Cursor's plugin rules and of every shipped component (`skills/`, `agents/`), rather than a root file like the two formats that collide — broadcast becomes declarable, and every declaration becomes additive and per-file."
vantage:
  status-chip: true
---

# A pack's AGENTS.md has two readers, and declaring anything takes one of them away

**Status:** DECIDED, 2026-09-22. Nothing built. Every question is ruled
([Decision Ledger](#decision-ledger)); what is owed is the build in
[§9](#9-what-i-would-build-in-order). Evidence verified at `3ac4e8b1`.

> **In short.** yolo reads a pack's content through defaults that override each other, and it reads
> prose from the one filename the rest of the agent ecosystem already reserves for a different
> reader. The repository keeps that file. A pack should ship prose from a place only yolo reads, and
> every line in its manifest should add a delivery — none should be able to remove one it does not
> name.

**Why it matters.** Both failures are live in one config today. The `matt` pack's house rules
(`matt/AGENTS.md`) reach **no agent**, because the pack also declares one pi-only briefing. The
`matt-craft` repository's *contributor* guide reaches **every agent in every jail**, because it
has no manifest. Neither pack is misconfigured by any rule its author could have found.

**The shape.** Three defaults are replaced. `AGENTS.md` stops being a delivery source, and a
yolo-only location takes its place. Silence becomes broadcast in a manifest as well as without one.
The implicit delivery becomes per file instead of per kind.

**Cost.** A pack whose `AGENTS.md` *was* meant to ship must move it under `briefing/`, and nothing
announces that (ruled). On the day this lands, the prose in those packs stops reaching agents until
the move.

**Start at [§2.2](#22-declaring-anything-switches-the-default-off)**, where the trap is shown. The
design is [§3](#3-the-design). Nothing in any other format constrains the new name
([Appendix A](#appendix-a-prior-art-how-other-agent-ecosystems-ship-instructions)).

**Needs your ruling:** None.

**Reads with:** [`briefing-audiences.md`](briefing-audiences.md) (its P2, "silence means
broadcast", is ruled and was never true for a manifest — this doc makes it true);
[`slots-and-contributions.md`](slots-and-contributions.md) (`exposes` will re-spell the fields
this doc changes; its [OQ-D8](slots-and-contributions.md#OQ-D8) and
[OQ-D9](slots-and-contributions.md#OQ-D9) are affected, see [§8](#8-how-this-sits-with-the-sibling-designs));
[`pack-briefing-defaults-plan.md`](pack-briefing-defaults-plan.md) (the implementation sketch — to
be completed into a plan against the tree before the build).

---

## 1. Verdict

These are the principles the rest of the doc spends its length earning. They are numbered so
sibling docs and code comments can cite them.

- **P1. One file, one reader.** yolo never ships a file as pack prose if an agent tool reads it as
  a repository's own instructions. Today that means `AGENTS.md`, `CLAUDE.md` and `GEMINI.md` —
  Copilot CLI alone reads all three ([Appendix A](#appendix-a-prior-art-how-other-agent-ecosystems-ship-instructions)).
  **Ruled 2026-09-22:** those files stay where they are, as the repository's; shipped prose moves.
- **P2. Silence means broadcast, and a manifest can say it.** A `briefing` or `skills`
  contribution that names neither `into` nor `agents` reaches every destination of its kind in the
  selected pack set. This is [`briefing-audiences.md`](briefing-audiences.md#1-verdict-and-the-principles-it-rests-on)'s
  P2, already ruled, finally reachable from a `pack.json`.
- **P3. Declarations add; they never subtract.** A contribution governs the files it names.
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
repository"* — [agents.md](https://agents.md) calls it "a README for agents". The installed pi,
copilot, codex and opencode packages all reference it, and the Claude Code 2.1.280 binary does 13
times (`CLAUDE.md`: 212). yolo reads the same filename with a second meaning — *"prose this pack
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

matt-craft is a Claude plugin, and by Claude's own rule its root instruction file is **not**
shipped content: *"A `CLAUDE.md` file at the plugin root is not loaded as project context"*
([plugins reference](https://code.claude.com/docs/en/plugins-reference)). yolo recognises the
plugin (`pluginpack.DiscoverIn` loads a plugin-shaped pack root) — but only at the host notch, and
no briefing path consults plugin-ness at all. So yolo ships the one file the wrapped format says
not to.

The leak runs the other way as well. A pack author working in their own pack repository gets the
pack's shipped prose loaded as that repository's instructions, whether or not it describes how to
work there.

`skills/` has **no** equivalent collision. Every plugin format reads `skills/` as shipped content,
and an agent's own in-repository skills live under `.claude/skills/`. So P1 is a briefing-only
change.

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

The new location is a **`briefing/`** directory ([OQ-PB1](#decision-ledger), ruled). The
runner-up, a root `BRIEFING.md`, is in [§6](#6-alternatives-considered).

### 3.1 The `briefing/` directory is what a pack ships; `AGENTS.md` is never read

- **`AGENTS.md`, `CLAUDE.md` and `GEMINI.md` are never read as pack prose**, with or without a
  manifest, at any notch (P1, ruled). For a repository pack they are that repository's own
  instructions, and yolo leaves them to the reader they were written for.
- **The conventional prose source is a `briefing/` directory at the pack root.** Its contents
  are every regular `*.md` file directly inside it:
  - **Order is per pack, never global.** Packs keep the config order they have today — the order
    that decides which of two conflicting rules an agent reads last. *Within* a pack, its files
    are ordered by filename, byte-wise, and stay contiguous. A global sort across packs is
    rejected: it would interleave packs, so one pack's filenames could reorder another pack's
    prose, and a rename in one repository would change what a different pack's rules override.
  - **Joining.** A pack's files that reach one destination are joined with one blank line between
    them, exactly as separate packs' prose is. Unlabelled unless `briefing_provenance` is on, in
    which case the pack's one label heads its joined section. Files the pack routed elsewhere
    ([§3.3](#33-a-file-is-governed-by-the-contribution-that-names-it)) are simply absent from
    this destination; they do not break the pack's section in two.
  - **Not recursive.** A subdirectory is not read, matching Agent Plugins' *"clients do not
    recursively search deeper"* for `skills/`. Lint names it ([§3.6](#36-every-delivery-and-every-refusal-to-deliver-is-shown-before-launch)).
  - **Only `*.md`.** Anything else is not read, and lint names it.
  - **Empty or whitespace-only files contribute nothing**, silently, as an absent convention does
    today.
  - **Names match case-sensitively.** `Briefing/` is not the convention.
- **Those three basenames are refused as a source at any depth** ([OQ-PB2](#decision-ledger)):
  an explicit `from` whose basename is `AGENTS.md`, `CLAUDE.md` or `GEMINI.md`, and a file with one
  of those names inside `briefing/`, are both refused at lint and at launch, with a message naming
  the move. Agent tools read those names in subdirectories too, so depth does not make one safe,
  and an allowed exception is how the dual use would come back one pack at a time.
- **The measured premise.** No agent tool I could scan reads `briefing/`, or a file called
  `BRIEFING.md`, as instructions: zero mentions in Claude Code 2.1.280, and none in the installed
  pi, codex, copilot and opencode packages. The same scan found `AGENTS.md` in every one of them.
  **UNMEASURED:** agy and omp were not installed in this jail.
- **A pack that wants one text for both readers** writes it once under `briefing/` and points the
  in-repository file at it. `CLAUDE.md` can say `@briefing/house-rules.md`. This is the author
  choosing the dual use, visibly, in a file yolo does not read.

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
- **Two contributions naming one source are REFUSED** ([OQ-PB5](#decision-ledger)). Within one
  pack and one kind, two content contributions that resolve to the same source — the same cleaned
  `from`, or both omitting it and so both naming the convention — are an error at lint and at
  launch, naming both. They contradict [§3.3](#33-a-file-is-governed-by-the-contribution-that-names-it)'s
  rule that a named file has exactly ONE governing contribution, and every legitimate shape has a
  one-contribution spelling: `agents: [a, b]` for several audiences, silence for all of them.
  Destination-declaring contributions (`agent` set) source nothing ([§3.5](#35-a-destination-ships-nothing))
  and are outside the rule.
- **`into` + `agents` together** stays refused, unchanged.
- **`files` is excluded** ([§5](#5-non-goals)). An `into`-less, `agents`-less `files` contribution
  keeps being refused. The refusal says why: `files` has no conventional source and its
  destinations are agent-specific slot types.

### 3.3 A file is governed by the contribution that names it

The implicit delivery becomes **per file**, never per kind:

- **Every file in the conventional source (`briefing/*.md`, and `skills/` as one tree) is
  broadcast implicitly unless some contribution of that kind names it.**
- **A contribution names a file by `from`.** A `briefing` contribution that omits `from` names
  every file of `briefing/` that no other contribution names — it is how a pack narrows its whole
  convention in one line.
- **Once a contribution names a file, that contribution alone decides where it goes.** For example,
  `{"kind": "briefing", "from": "briefing/pi.md", "agents": ["pi"]}` sends that one file to pi
  only, and every other file in `briefing/` keeps broadcasting.
- **A contribution naming a file outside the convention has no effect on the convention.** Adding
  `{"from": "files/pi-rules.md", "agents": ["pi"]}` beside a `briefing/` directory delivers both.
  That is the case [§2.2](#22-declaring-anything-switches-the-default-off) gets wrong.
- **The host notch's no-widening promise still holds.** A pack declaring
  `{"kind": "briefing", "into": ".claude/CLAUDE.md"}` names its unclaimed convention by omission,
  so `briefing/` goes to that one path and nowhere else. `ResolveDestinations`' contract
  (*"a declaration must not be widened into every other agent's directory"*) is kept by the files
  being named, not by the kind being declared.
- **Order-independent.** Governance is a property of the declaration set, so the order of the
  `contributes` list never changes which files are delivered, or where.

### 3.4 A declared source is the only source

- A `briefing` `from` that is absent, not a file, empty, or whitespace-only **delivers nothing
  from that contribution and is reported**, naming the path. That aligns `briefing` with what
  `skills` does today. The fallback chain is deleted.
- **The severity of that report stays what it is today** — a warning at launch, and at lint the
  treatment an unreadable `skills` source already gets. Whether a mistyped reference refuses
  instead is already an open question elsewhere
  ([`reference-mismatch-diagnostics.md`](reference-mismatch-diagnostics.md), roadmap row 9), so
  this doc removes the substitution and leaves the severity to that ruling.
- **An absent convention is silent**, as now. Most packs carry no prose.
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
  for example, `briefing/house-rules.md → every agent (implicit broadcast)` beside
  `files/pi-rules.md → pi`.
- **It names every conventional-looking file it will not ship**, one info line each:
  - A root `AGENTS.md`, `CLAUDE.md` or `GEMINI.md` gets *"not shipped: this is the repository's own
    agent instructions — ship prose under `briefing/`"*. Info, not a warning: for a repository
    pack, not shipping it is correct.
  - A subdirectory or a non-`.md` file inside `briefing/` gets *"not read: `briefing/` is read one
    level deep, `*.md` only"*.
- **The advisory at [`pack.go:595`](../../internal/cli/pack.go#L595) states what dropping the line
  does**, which is widen, not nothing.
- **The scaffold** writes `briefing/<pack>.md`. Its advice shows an addressed contribution *adding*
  to the broadcast, and shows narrowing as a contribution that names a `briefing/` file.
- **The reference docs and the `Agents` field comment** state the rules in
  [§3.2](#32-silence-means-broadcast-in-a-manifest-too)–[§3.3](#33-a-file-is-governed-by-the-contribution-that-names-it)
  in the same change that builds them.
- **What `lint` cannot see is unchanged.** It takes one pack and no config, so it cannot know the
  selected set. It reports *"every agent"*, and the resolved list is `yolo host apply`'s and the
  launch banner's to print, as today.

### 3.7 The two packs that started this, before and after

| | Today | After `mkdir matt/briefing && git mv matt/AGENTS.md matt/briefing/house-rules.md`, no other edit |
|---|---|---|
| Claude's briefing | matt-craft's contributor guide | matt's house rules |
| pi's briefing | `pi-rules.md` and matt-craft's contributor guide | matt's house rules and `pi-rules.md` |
| codex, agy, opencode | matt-craft's contributor guide | matt's house rules |
| `yolo pack lint` on matt | `✓`, one claim | Two deliveries, one implicit; nothing unshipped |
| `yolo pack lint` on matt-craft | `✓` | `AGENTS.md`, `CLAUDE.md`: not shipped (repository instructions) |

matt-craft needs **no** edit: its leak stops because yolo stops reading the file.

## 4. State that already exists

- **Packs whose root `AGENTS.md` was meant to ship.** This covers every `yolo pack init` scaffold
  ever written, the `matt` pack, and an unknown number of third-party packs. **A hard cut, by
  ruling** ([OQ-PB3](#decision-ledger)): on the day this ships, a root `AGENTS.md` simply stops
  being read. It is never a refusal, since the pack may be someone else's repository, and there is
  **no notice** at launch or at `yolo host apply`. The one place it surfaces is `yolo pack lint`'s
  delivery listing ([§3.6](#36-every-delivery-and-every-refusal-to-deliver-is-shown-before-launch)),
  when an author asks. There is no escape hatch: a pack's file layout is authoring, and a hatch
  would keep the second reader alive behind a variable.
- **The conventional local pack.** `yolo host apply` itself moves an adopted host briefing to
  `~/.config/yolo-jail/local/AGENTS.md`
  ([`applyhostbriefings.go:55-70`](../../internal/cli/applyhostbriefings.go#L55-L70)). yolo chose
  that location, so yolo moves it:
  - The migration writer targets a file under `local/briefing/` from the day this lands. Its name
    is the implementer's; it affects only the join order.
  - The next `yolo host apply` moves an existing `local/AGENTS.md` into `local/briefing/` and
    reports the move. If the target name is already taken it refuses, naming both files, rather
    than choosing one.
  - Until that apply runs, the local pack's old `AGENTS.md` is not delivered — the same hard cut as
    any other pack. With `host_apply_on_launch` on, the next wrapped launch runs that apply.
- **Fetched packs.** A fetched pack whose `AGENTS.md` is a contributor guide (matt-craft) is fixed
  with no action. One whose `AGENTS.md` was consumer prose cannot be moved by the user; its author
  moves it upstream.
- **Host destinations yolo already composed.** They are composed wholesale under the ownership
  record, so the next `yolo host apply` recomposes them without the dropped prose. No separate
  cleanup is needed.
- **Existing manifests.** Every manifest valid today stays valid (P2 is a relaxation), with one
  three exceptions, each refused with the corrected spelling: `from` on a destination-declaring
  `briefing` or `skills` contribution (P5), which no shipped pack uses; a reserved basename as a
  source ([OQ-PB2](#decision-ledger)); and two contributions naming one source
  ([OQ-PB5](#decision-ledger)).

## 5. Non-goals

- **`files` broadcast.** `files` destinations are typed slots (pi extensions, themes), and "every
  agent" has no meaning for them yet. `files` stays `into` or `agents`, unchanged.
- **Addressing by filename** (`briefing/claude.md` going to Claude alone because of its name).
  Audience stays a manifest field; a filename that silently narrows delivery is the class of
  implicit rule this doc removes.
- **The severity of a mistyped `from`** belongs to
  [`reference-mismatch-diagnostics.md`](reference-mismatch-diagnostics.md)
  ([§3.4](#34-a-declared-source-is-the-only-source)).
- **The `exposes` schema.** This doc changes behaviour on today's fields; the re-spelling is
  [`slots-and-contributions.md`](slots-and-contributions.md)'s.
- **A per-file consumer toggle** in the style of Cursor's Always / Agent Decides / Manual. It is a
  real option for later ([Appendix A](#appendix-a-prior-art-how-other-agent-ecosystems-ship-instructions)).
  [OQ-PB4](#decision-ledger) ruled against gating fetched packs, so it waits for a real third-party
  pack that abuses always-on prose.
- **What a composed briefing contains around the pack prose** — the jail body, the host prepend,
  the handoff. That is unchanged ([`agent-briefings.md`](../reference/agent-briefings.md)).

## 6. Alternatives considered

| Alternative | Verdict |
|---|---|
| **Move the repository's file instead** — ask pack authors to keep contributor instructions elsewhere | Rejected by ruling ([P1](#decision-ledger)). `AGENTS.md` is an ecosystem standard with its own readers; yolo does not get to move it. |
| **A wildcard audience** (`"agents": ["*"]`) and keep `AGENTS.md` | Rejected. It fixes [§2.3](#23-broadcast-cannot-be-written-down) with a second spelling of what silence should already mean, and leaves the dual-reader file ([§2.1](#21-one-file-two-readers)) and the suppression ([§2.2](#22-declaring-anything-switches-the-default-off)) exactly as they are. |
| **Keep today's suppression and have lint warn about it** | Rejected. It documents the trap instead of removing it, and a warning on every pack that declares one briefing stops being read in a week. |
| **No convention at all** — prose only through an explicit `from` | Rejected. It deletes the zero-ceremony prose path that [`briefing-audiences.md`](briefing-audiences.md#1-verdict-and-the-principles-it-rests-on) P2 and the pack guide promise. What that path got wrong was its location and its suppression rule, not its existence. |
| **Read `AGENTS.md` only when the pack is not a git repository** | Rejected. A file's meaning must not depend on whether `.git` is present: every fetched pack is a repository, and a local pack becomes one the moment someone runs `git init`. |
| **Keep `AGENTS.md` and make yolo strip a marked section** | Rejected. It is two readers of one file with a parser in between, and it breaks the first time either reader's author forgets the marker. |
| **A root `BRIEFING.md`** instead of a directory | Rejected ([OQ-PB1](#decision-ledger)). Equally unread by agent tools, but it is shipped content spelled in the repository's grammar (uppercase root Markdown), which is how `AGENTS.md` got its second reader; and one file cannot be the unit [§3.3](#33-a-file-is-governed-by-the-contribution-that-names-it) governs. |
| **Allow an explicit `from: "AGENTS.md"`** as an opt-in dual use | Rejected ([OQ-PB2](#decision-ledger)). An allowed exception returns the defect one pack at a time; one text for both readers is already spelled `CLAUDE.md` → `@briefing/x.md`. |
| **A notice when a root `AGENTS.md` stops shipping** | Rejected ([OQ-PB3](#decision-ledger)). It would fire forever on every correctly-unshipped repository guide (matt-craft's); lint's listing is where an author looks. |
| **Require fetched packs to declare prose before it is always-on** | Rejected ([OQ-PB4](#decision-ledger)). It makes one pack behave differently by how it was fetched; selecting a pack is the consent, as for every other kind. |
| **Deliver pack prose as a skill** — the Claude and Agent Plugins answer | Rejected for this kind. A skill loads when its description matches the task; house rules must be in front of the agent before it has chosen one. `skills` already exists for the on-demand case. |

## 7. Risks

| Risk | Mitigation |
|---|---|
| **R1.** A pack whose `AGENTS.md` was consumer prose goes quiet on ship day | **Accepted by ruling** ([OQ-PB3](#decision-ledger)): no notice. The move is one `git mv`; `yolo pack lint` shows it as not shipped when the author runs it; the local pack is moved by yolo itself ([§4](#4-state-that-already-exists)) |
| **R2.** An agent tool later adopts `briefing/` as an instruction location, recreating the collision | Checked at design time ([§3.1](#31-the-briefing-directory-is-what-a-pack-ships-agentsmd-is-never-read); agy and omp unmeasured). P1 is stated as a rule, so the location is re-chosen if that happens, rather than tolerated |
| **R3.** Broadcast is now one missing field away, so an author meaning "Claude only" who writes nothing reaches everyone | That is already the zero-ceremony behaviour and what P2 ruled. Lint's delivery list ([§3.6](#36-every-delivery-and-every-refusal-to-deliver-is-shown-before-launch)) shows *"every agent"* before any launch |
| **R4.** Per-file governance keys on the resolved source path, so two spellings of one file (`briefing/a.md`, `./briefing/a.md`) could be read as two files | Compare cleaned pack-relative paths; the escape check already cleans them |
| **R5.** The two notches diverge again, because the gate lives at three sites today | One predicate, used by all three; see the sketch |
| **R6.** A multi-file directory makes join order observable, so renaming a file reorders a briefing | Stated as the rule (byte-wise filename order); lint's delivery list shows the order |

## 8. How this sits with the sibling designs

- **[`briefing-audiences.md`](briefing-audiences.md)** — P2 is **fulfilled, not overturned**. Its
  [§4.1](briefing-audiences.md#41-the-two-halves-and-why-neither-knows-the-others-business) sentence
  *"a contribution with neither is today's zero-ceremony broadcast, unchanged"* becomes true. Its P3
  (an unmatched audience is fatal) is untouched, and it is the reason broadcast has to be spellable
  at all.
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
2. **Broadcast becomes declarable** ([§3.2](#32-silence-means-broadcast-in-a-manifest-too)). It is a
   validator relaxation that loosens nothing else, and it unblocks the `matt` pack immediately with
   `{"kind": "briefing"}`.
3. **Per-file governance, declared-source-only, destination-ships-nothing**
   ([§3.3](#33-a-file-is-governed-by-the-contribution-that-names-it)–[§3.5](#35-a-destination-ships-nothing)),
   as one change, because each of them edits the same fallback sites.
4. **The move** ([§3.1](#31-the-briefing-directory-is-what-a-pack-ships-agentsmd-is-never-read),
   [§4](#4-state-that-already-exists)), with the scaffold, the local-pack migration writer, the
   reserved-basename and duplicate-source refusals, and the corrected reference docs.

## 10. What done looks like

- `{"kind": "briefing"}` in a manifest lints clean, and a jail selecting that pack plus claude and
  pi delivers the pack's `briefing/` files to both.
- The [§3.7](#37-the-two-packs-that-started-this-before-and-after) table holds in a real jail:
  Claude's composed briefing carries matt's house rules and not matt-craft's contributor guide.
- `yolo pack init`, followed by the scaffold's own advice, lints as **two** deliveries.
- A declared `from` that does not exist delivers nothing and is reported. No other file arrives in
  its place.
- `from` on a destination-declaring `briefing` is refused, naming the addressed spelling.
- A `from` naming `AGENTS.md` (at any depth), or a `briefing/AGENTS.md`, is refused, naming the move.
- Two contributions naming one source in one pack are refused at lint, naming both.
- One `yolo host apply` on a home with `local/AGENTS.md` leaves a file under `local/briefing/`,
  reports the move, and the prose still reaches every agent.
- `pack-system.md`, `agent-briefings.md` and the `Agents` field comment describe the behaviour the
  lint output shows.

## 11. Open Questions

None. Every question is ruled; the rulings are in the [Decision Ledger](#decision-ledger) and folded
into the sections each one governs.

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| P1 | The repository's instruction files (`AGENTS.md`, `CLAUDE.md`) stay where they are, as the repository's; shipped prose moves to a different location. *(Maintainer: "the repo briefing one has to stay in place. Can't change that one. Gonna have to pick something else for the other one.")* | 2026-09-22 | [§3.1](#31-the-briefing-directory-is-what-a-pack-ships-agentsmd-is-never-read) | — |
| OQ-PB1 | Shipped prose lives in a `briefing/` directory of `*.md` files at the pack root, beside `skills/`: read one level deep, ordered per pack by filename, never globally | 2026-09-22 | [§3.1](#31-the-briefing-directory-is-what-a-pack-ships-agentsmd-is-never-read) | — |
| OQ-PB2 | A source whose basename is `AGENTS.md`, `CLAUDE.md` or `GEMINI.md` — an explicit `from`, or a file inside `briefing/` — is refused at any depth, naming the move | 2026-09-22 | [§3.1](#31-the-briefing-directory-is-what-a-pack-ships-agentsmd-is-never-read) | — |
| OQ-PB3 | Ship-day cut is hard: a root `AGENTS.md` stops being read. Never a refusal, and **no notice** at launch or host apply; only `yolo pack lint`'s listing shows it | 2026-09-22 | [§4](#4-state-that-already-exists) | — |
| OQ-PB4 | No separate rule for fetched packs: selecting a pack is the consent; P6's delivery list is the disclosure; a per-file consumer toggle only if a real third-party pack abuses it | 2026-09-22 | [§5](#5-non-goals) | — |
| OQ-PB5 | Two content contributions of one kind in one pack naming the same source are refused at lint and launch, naming both. *(Raised in review: "why do we allow this and not fatal? surely this is an error?")* | 2026-09-22 | [§3.2](#32-silence-means-broadcast-in-a-manifest-too) | — |

## Appendix A. Prior art: how other agent ecosystems ship instructions

Read 2026-09-22 from each vendor's own documentation unless marked; the installed-binary rows were
measured in this jail. **The question asked of each:** can a package put text in front of the
agent on every turn, by what mechanism, and does that mechanism reuse the repository's own
instruction filename?

### A.1 What a package can put in front of the agent

| Ecosystem | Always-on text from a package | Mechanism | Reuses the repo's instruction file? |
|---|---|---|---|
| **Claude Code plugins** | Only through code | A `SessionStart` hook returning `additionalContext` ([hooks](https://code.claude.com/docs/en/hooks)). For static text the docs say *"put them in a skill"* | **No** — *"A `CLAUDE.md` file at the plugin root is not loaded as project context"* ([plugins reference](https://code.claude.com/docs/en/plugins-reference)) |
| **Agent Plugins 1.0** (Codex, Copilot, Cursor, VS Code, Kiro) | Not in the portable core | Closed manifest; *"Add instructions as Agent Skills"*; client extras live in a reverse-domain directory ([agentplugins.codes](https://agentplugins.codes)) | No |
| **Copilot** (Agent Plugins namespace) | Possibly | `com.github.copilot/rules/` ([CLI plugin reference](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-plugin-reference)). **Unconfirmed:** the pages read list the directory and not its semantics. The legacy format has no instruction component | No |
| **Codex plugins** | Not found | `.codex-plugin/plugin.json`, skills, apps, `mcp.json` — *secondary sources only* | No |
| **Cursor plugins** | **Yes, as files** | `rules/` in the plugin; **the consumer toggles each rule** between Always, Agent Decides and Manual ([plugins](https://cursor.com/docs/plugins)) | No — plugin `rules/` is not the repository's `.cursor/rules/` |
| **Kiro powers** | Bundled, but on demand | A power carries steering files and *"activates dynamically based on context"* ([powers](https://kiro.dev/docs/powers)) | No — the repository's is `.kiro/steering/` |
| **pi packages** | Only through code | An extension's `before_agent_start` can *"modify the system prompt"*; package resources are extensions, skills, prompts and themes (installed `docs/extensions.md`, `docs/packages.md`) | No |
| **opencode plugins** | Through code, apparently | The hook name `experimental.chat.system.transform` is present in the installed binary. **Inferred from the name**; its documentation was not read | No |
| **Gemini CLI extensions** | **Yes, as a file** | `contextFileName` in `gemini-extension.json`, **defaulting to `GEMINI.md`** ([extension reference](https://geminicli.com/docs/extensions/reference/)) | **Yes** — `GEMINI.md` is also Gemini's in-repository instruction file |
| **yolo today** | **Yes, as a file** | Root `AGENTS.md`, broadcast by default | **Yes** |

### A.2 What the pattern says, and why most keep it out

**Three observations**, each checkable against the table:

1. **Nobody forbids it outright.** The mainstream routes always-on package text through *code*
   (Claude, pi, opencode) or through a component the *consumer* controls (Cursor's per-rule
   toggle). The portable standard leaves it out entirely.
2. **The static mechanisms that do not collide are component directories** — Cursor's plugin
   `rules/`, the same shape as `skills/`, `agents/` and `commands/`. Kiro's powers bundle steering
   files too, loaded on demand; their layout inside a power was not read.
3. **The only two that reuse the repository's own filename — Gemini and yolo — are the two with
   the dual-reader defect.** Gemini mitigates it by making the name a manifest field; yolo has no
   such field for the default.

**Why most keep it out.** What the vendors *state*:

- Claude: ship instructions as a skill.
- Agent Plugins: *"declare identity, not runtime behavior"*, and put instructions in skills.
- Kiro: powers are designed to load on demand, not up front.
- Cursor: the consumer decides each rule's mode.

None of the pages read says *why*. My inference, not a quote:

- **Context budget.** Always-on text is paid for on every turn, for every installed package.
  Skills exist to be progressive disclosure.
- **Trust.** Always-on text from a third party is an instruction with no trigger, from someone the
  user did not write.
- **Precedence.** Two packages' always-on rules can conflict, and the only arbiter is the user's own
  instruction file — which a package would be competing with.

**Why yolo can still have it, and why the name is ours to choose.** A yolo pack is not a
marketplace plugin. The user selects it into their own environment, and yolo composes it into the
user's own instruction file, per launch — closer to dotfiles than to an app store. None of the
formats yolo wraps defines "shipped prose", so no external convention names it. The one hard
constraint the table does impose is **negative**: stay off every other tool's instruction names —
`AGENTS.md`, `CLAUDE.md`, `GEMINI.md`, `.github/copilot-instructions.md`, `.cursor/rules/`,
`.kiro/steering/`. That is P1, and it is why [OQ-PB1](#decision-ledger) was a free choice among
names that avoid them.
