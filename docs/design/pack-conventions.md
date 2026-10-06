---
title: "A pack file should state only what is particular to it: three conventions weighed, what each saves, and the rule that keeps an older yolo safe"
date: 2026-10-05
status: in-review
stage: DESIGN
next: "Rule OQ-PC3, then OQ-PC1 and OQ-PC2, which depends on it; each leans toward keeping what packs write today. Nothing here gates the patch-series work, which waits on a release carrying the launch's skip with every host on it (§11 step 0). PC-D4 needs no ruling and rides pack-pi-resources.md's build"
tags: [packs, manifest, conventions, pi, patched-extensions, skew, design]
summary: "Pack files repeat facts the system already knows: a patched pi extension and the list entry naming its own path, one entry per file in a shared folder, prose addressed by a line rather than by a folder, a loophole line naming its own folder. Each convention weighed here is a load-time expansion into contributions the pack could have written, so validation, the footprint, the launch's disclosures and the render never know it exists, and the pack that owns a layout declares the convention about it so core applies it by name. Reviewed the day it was drafted: across today's packs the three save about five lines in all, each amends or reverses a standing ruling, and every new pack field is refused at every launch by every released yolo, so each question leans toward keeping today's written form until every host runs a release that skips what it cannot read."
vantage:
  status-chip: true
---

# A pack file should state only what is particular to it

**Status:** 2026-10-05, drafted and corrected after review the same day. Nothing built. Read
against the tree at `c321336a8`; counts are in [Appendix A](#appendix-a-counts), evidence in
[Appendix B](#appendix-b-evidence).

> **In short.** A convention is a load-time expansion into contributions the pack could have
> written, so nothing after it (validation, the footprint, the launch's disclosures, the render)
> knows it exists. The pack that owns a layout declares it, and core applies it by name, never by
> agent. Measured against today's packs, the three conventions here save about five lines in all,
> and each one changes a ruling. Every question therefore leans toward keeping what packs write
> today until every host runs a release that skips what it cannot read.

**Why it matters.** The maintainer, 2026-10-05: *"we need to be able to consolidate our pack
files, they repeat way too much. we need a bunch of convention over configuration."* His patched
pi extensions each take a `files` contribution and a list entry spelling the same path.

**Not on the patch-series path.** Patched extensions work today with the written pair. What the
patch-series work waits on is a release carrying [the skip](#defined-terms), with each host on it
([§11](#11-what-i-would-build-in-order) step 0). Until then, any pack field a host's yolo does not
know refuses every launch on that host, and that includes the fields proposed here.

**The shape.** Two expansion points that already exist, the pack load and the selection's rewrite
that applies forks, would gain the conventions ([§4](#4-the-conventions),
[§5.1](#51-two-expansion-points-both-already-there)). **Cost:** two spellings of a few things for
good. A yolo older than a convention leaves that part out and says so, and a released yolo refuses
the pack outright.

**Start at [§2](#2-what-an-older-yolo-does-with-a-convention)**: what an older yolo does with a
pack decides what shape every convention may take.

**Needs your ruling:** [OQ-PC3](#OQ-PC3) first, then [OQ-PC1](#OQ-PC1), then [OQ-PC2](#OQ-PC2),
which is asked only if [OQ-PC1](#OQ-PC1) allows folders.

**Reads with:** [`pack-pi-resources.md`](pack-pi-resources.md) (the slot [OQ-PC3](#OQ-PC3) would
widen), [`patched-extensions.md`](patched-extensions.md) (the tree and its entry),
[`pi-pack-extensions.md`](pi-pack-extensions.md) (the per-pack landing [OQ-PC3](#OQ-PC3) would
amend), [`agent-briefings.md`](../reference/agent-briefings.md) (the audience rules
[OQ-PC1](#OQ-PC1) bears on), [`loophole-system.md`](../reference/loophole-system.md) (the
activation rulings [OQ-PC2](#OQ-PC2) bears on), [`manifest-language.md`](manifest-language.md)
(syntax, per-kind defaults, and the posture spelling handed there),
[`slots-and-contributions.md`](slots-and-contributions.md) (the agent named once per pack), and
[`pack-conventions-plan.md`](pack-conventions-plan.md) (the implementation sketch, incomplete).

---

## Defined terms

- **Convention** *(coined here)*: a rule core applies to a pack's tree, or to a declaration
  another selected pack made, that yields contributions the pack did not write. It generalizes
  what [`pack-system.md`](../reference/pack-system.md#briefing-directory) already calls a
  **conventional source** (`briefing/`, `skills/`), which its
  [§6a-3](../reference/pack-system.md#batch-6a-3) ruled as "conventions over configuration". A
  convention is not a default value for a field: it never fills in a field an author left out
  ([§2](#2-what-an-older-yolo-does-with-a-convention) says why).
- **Implied contribution** *(coined here)*: a contribution a convention yields. It is an ordinary
  contribution plus a note of the rule and the pack that declared it.
- **Expansion** *(coined here)*: the step that turns a pack's tree and declarations into its
  implied contributions ([§5.1](#51-two-expansion-points-both-already-there)).
- **Written form**: the same contributions spelled out in `pack.json`, as today.
- **Use read** and **authoring read**: terms coined by
  [PF-D68](patched-forks.md#PF-D68). A use read is one a launch or host verb acts on, and it skips
  what it cannot read; an authoring read (`yolo pack lint`, `yolo pack footprint`) refuses it.
- **The skip**: a launch's skipping, and naming, of a contribution holding a kind, a `via` or a
  field its yolo does not know, instead of refusing the pack ([PF-D68](patched-forks.md#PF-D68)).
  `yolo features` lists it as `skips-unreadable-contributions`. It is on `main` since 2026-10-05
  and in no release yet.
- **Slot**, **addressed tree**, **landing** and **registration**: as
  [`pack-pi-resources.md`](pack-pi-resources.md#defined-terms) defines them. A slot is a `files`
  destination an agent pack declares; an addressed tree is content sent to it with `agents`; its
  landing is `<slot>/<pack>`; a registration is the list entry core adds for a landed tree. A
  **registering slot** is a slot that declares `register`.
- **Selected vocabulary**: the agent names the selected packs claim, through a `program`'s `bin`
  or a briefing or skills destination's `agent`. An `agents` entry outside it fails the launch
  ([BA-P3](../reference/agent-briefings.md#ba-p3)).
- **Patched extension**: a `files` contribution with `source` and `patches`, built from an
  upstream with a series replayed ([`patched-extensions.md`](patched-extensions.md#1-defined-terms)).
- **Posture**: one side of an `autonomy` contribution, `autonomous` (every jail) or `guarded` (the
  host). It may carry config patches and list entries for that side.
- **Notch**: where an agent runs, a jail or the user's own home on the host.

## 1. Principles

- <a id="p1"></a>**P1. A convention is an expansion, not a mode.** It yields the contributions
  the pack could have written. Every reader after the expansion reads ordinary contributions, and
  none of them branches on whether one was written or implied. This is how
  [`manifest-language.md`](manifest-language.md#2-principles)'s M2 (*"Compression removes
  repetition, never a distinct fact"*) holds: an implied claim is still a claim, enumerated where
  every claim is.
- <a id="p2"></a>**P2. The pack that owns a layout declares its convention; core applies it by
  name.** A rule about pi's folders is a field in pi's pack. Core matches declared slots,
  surfaces, templates and identities, and holds no agent's name. [`AGENTS.md`](../../AGENTS.md):
  *"Core does not know what an agent is"*, as narrowed by
  [OQ-D5](slots-and-contributions.md#OQ-D5): core knows an "agent" *"only insofar as it
  identifies a config target"*. [OQ-PC1](#OQ-PC1)'s folders would also use a name to pick a
  source, which is part of what that question asks.
- <a id="p3"></a>**P3. A convention is spelled so an older yolo skips it and says so, never
  refuses the pack.** That holds for a yolo with the skip. Every release through v0.11.1 refuses
  any field it does not know ([§2](#2-what-an-older-yolo-does-with-a-convention)).
- <a id="p4"></a>**P4. Implied is disclosed as written.**
  [`report-tiers.md`](../reference/report-tiers.md)'s P4: *"Disclosures are never
  suppressible."* Its P5: *"No silent skip survives this."* An implied contribution is disclosed
  wherever its written form would be, and names its rule and the pack that declared it.
- <a id="p5"></a>**P5. Written wins.** A written contribution naming a convention's source or
  target governs it, and the convention covers only what nothing written names. This is the
  briefing governance rule ([`pack-system.md`](../reference/pack-system.md#one-governance-reader)),
  whose one reader is `packload.GovernedSources`. So the written form always works, and it is the
  form to keep while a host runs an older yolo.

## 2. What an older yolo does with a convention

Two kinds of older yolo read a pack differently.

**A yolo with the skip.** Its use read and the jail's tolerant read **skip** a contribution
holding a kind, a `via` or a field they do not know, and name the skip in one line. They keep a
restricting kind without the field ([PF-D69](patched-forks.md#PF-D69)), and they ignore a
pack-wide field they do not know and name it. They **refuse** a missing required field, a
malformed value, or a combination they validate as wrong. [PF-D68](patched-forks.md#PF-D68):
*"What no newer yolo makes readable stays a problem: … a missing required field."* A refusal fails
every launch on that host, which is the first of the maintainer's patch-series requests over again.

The skip reads a contribution and its typed sub-objects only. A config surface entry, and a
posture's `config` entry, are passed to the config engine as raw JSON and decoded strictly there
(`manifest.DecodeSurfaces`, `manifest.DecodePostureOverlay`). So a field the engine does not know
inside one is a problem on every yolo.

**Every release through v0.11.1.** The host decodes the whole manifest strictly (`Decode` with
`DisallowUnknownFields`), and v0.11.1 has neither the skip nor `yolo features`. Any field such a
yolo does not know refuses the pack, and with it every launch on that host. On those hosts the
only safe convention is one that adds nothing to the content pack: spelling 2 below, or writing
nothing new.

So the obvious spelling of a convention, leaving a field out, is the one spelling that refuses:

| Convention spelled as an omission | What a yolo older than it does |
| :--- | :--- |
| `codec` taken from the file extension | `missing "codec"`: refused |
| A patched extension's `patches` defaulting to `patches/<name>` | `source` without `patches` is refused on `files` |
| A patched extension addressed with `agents` instead of `into` | `agents` beside `source` is refused (`patchedext.go:110-114`) |
| A pack-wide `defaults` block | Ignored and named, but the contributions it shaped are still used, with other values: a default for an optional field changes the contribution, and a default for a required one leaves it refused ([§7](#7-considered-and-not-proposed)) |

**The rule.** Every convention here takes one of three spellings:

1. **A field or kind an older yolo does not know**, on a contribution or one of its typed
   sub-objects. A yolo with the skip skips the contribution, or keeps a restricting kind without
   the field, and says so. Every release refuses the pack. [OQ-PC3](#OQ-PC3)'s option B is this
   shape.
2. **A declaration in the receiving pack**, while the content pack writes a form an older yolo
   already reads. Every older yolo, released ones included, reads the content pack as it does
   today. A yolo with the patched-tree lint names what the convention would have added.
   [OQ-PC3](#OQ-PC3)'s option A is this shape.
3. **A pack-wide opt-in line.** A yolo with the skip ignores it and names it; every release
   refuses the pack. [OQ-PC1](#OQ-PC1)'s options A and B are this shape, and only the folder
   conventions need it.

Each convention also gets a `yolo features` name in the commit that builds it, by
[PF-D71](patched-forks.md#PF-D71)'s rule, so a host can be asked before a pack relies on it.

> [!NOTE]
> This rule bears on [OQ-M1](manifest-language.md#OQ-M1). Its per-kind defaults for `into`,
> `path` and `codec`, built as omissions, are refused by every older yolo. Its grouping by kind
> replaces every top-level key, so a yolo with the skip reads a regrouped manifest as an empty
> pack, naming each key it ignored, and a release refuses it. Both conflict with that question's
> premise, *"We don't need transitions"*, while the maintainer's hosts run different yolos. A
> pointer sits in [OQ-M1's background](manifest-language.md#background-to-oq-m1); this doc does
> not rule it.

## 3. Where packs repeat themselves

The repetitions found in the shipped packs, the integration fixtures and the maintainer's own
packs, and where each one is taken. Counts are in [Appendix A](#appendix-a-counts).

| Repetition | Where | Here |
| :--- | :--- | :--- |
| A patched extension's `files` contribution plus a list entry that is `~/` and its own `into` | The maintainer's five trees, three of them with a root entry a convention could remove; the patch-series guide; the integration fixture | [C1](#41-c1-a-patched-extension-in-pis-package-folder-loads-with-no-list-entry), [OQ-PC3](#OQ-PC3) |
| One `files` entry per file in a folder the user and other packs share | The maintainer's pi extensions and his tok-stats mod | Route C (ruled, [OQ-PR1](pack-pi-resources.md#OQ-PR1)), then [C2](#42-c2-a-folder-named-after-an-agent-is-addressed-to-it), [OQ-PC1](#OQ-PC1) |
| Briefing prose addressed to one agent | The maintainer's pi rules, the claude and pi packs' worktree notes | [C2](#42-c2-a-folder-named-after-an-agent-is-addressed-to-it), [OQ-PC1](#OQ-PC1) |
| `{"kind": "loophole", "from": "loopholes/<name>"}` | Every loophole yolo ships, one per pack | [C3](#43-c3-a-loophole-folder-is-the-loophole), [OQ-PC2](#OQ-PC2) |
| A `files` entry whose `into` ends in its `from` file's name | The pi pack's four extensions, opencode's plugin | Not proposed: pi's own extensions are a [non-goal](#8-non-goals), and opencode's is one line |
| A posture `config` entry restating the `agent`, `name`, `path` and `codec` of its own pack's surface | Every shipped posture with a config patch | Handed to [OQ-M1](manifest-language.md#OQ-M1) ([§7](#7-considered-and-not-proposed)) |
| A `config-overlay` wrapper, `{kind, surface, config: {managed: …}}`, around each overlay | The maintainer's packs; no shipped pack | [OQ-M1](manifest-language.md#OQ-M1)'s grouping by kind. Not here |
| A bare `{"kind": "briefing"}` | The maintainer's `matt` | Removable today: it governs every unnamed `briefing/*.md`, which the implicit broadcast already delivers, and briefing prose is ordered by filename either way. No convention needed |
| `agent` on every destination and surface of an agent pack | Every shipped agent pack | Ruled ([OQ-D5](slots-and-contributions.md#OQ-D5)); its build waits on [OQ-D6](slots-and-contributions.md#OQ-D6). Not re-asked |
| `codec` matching the extension; `"scope": "workspace"` (already the default); `after` equal to `host:` plus `into` | Most surfaces; most `state` entries; every briefing destination | [OQ-M1](manifest-language.md#OQ-M1)'s defaults and the `exposes` rewrite. Not here |
| A provider's same-named profile; one `install_hints` value for every manager; a machine `state` and the hook whose `at` names it | Provider packs; `requires` entries; three agent packs | Not proposed ([§7](#7-considered-and-not-proposed)) |

## 4. The conventions

Each convention is described as proposed, with what it amends and what it saves. Whether it
exists is its open question's to decide.

### 4.1 C1: a patched extension in pi's package folder loads with no list entry

**The rule, as proposed.** A registering slot registers every patched extension whose `into` is a
**direct child** of the slot, as it registers an addressed tree landed at `<slot>/<pack>`. If the
contributing pack writes any list entry that loads the tree (`loadsTree`,
[PPX-D36](patched-extensions.md#PPX-D36)), that entry replaces the registration
([PC-D2](#PC-D2)). Whether this exists is [OQ-PC3](#OQ-PC3).

It covers patched extensions only, on purpose. On a yolo without C1, the patched-tree lint names
a patched extension nothing loads ([PPX-D10](patched-extensions.md#PPX-D10)), but nothing names a
plain tree. A plain tree written into the slot would be delivered and never loaded, with no line,
which [P4](#p4) forbids. A plain `files` contribution, a tree or a single file, takes route C's
addressed form, which the slot registers.

[`pack-pi-resources.md`](pack-pi-resources.md#33-what-core-does) registers addressed trees only;
C1 would widen it. The slot is the one that doc already specifies, unchanged:

```jsonc
// packs/pi/pack.json (pack-pi-resources.md §3.1)
{ "kind": "files", "agent": "pi", "into": ".pi/agent/yolo-packs",
  "register": { "surface": "pi/settings", "path": "/packages", "entry": "~/{landing}" },
  "expects": ["extensions", "themes", "skills", "prompts", "package.json"] }
```

**Before**, per extension, in the shape the first patched launch's pack advice proposes for the
maintainer's own extensions (kept outside the repository, [Appendix B](#appendix-b-evidence)):

```jsonc
{ "kind": "files", "into": ".pi/agent/yolo-patched/pi-subagents",
  "source": "git+https://github.com/<upstream>/pi-subagents?ref=main",
  "patches": "patches/pi-subagents",
  "build": "npm install --omit=dev --legacy-peer-deps --ignore-scripts" },
{ "kind": "config-list", "surface": "pi/settings", "path": "/packages",
  "add": ["~/.pi/agent/yolo-patched/pi-subagents"] }
```

**After**: the list entry goes, and `into` moves into the folder.

```jsonc
{ "kind": "files", "into": ".pi/agent/yolo-packs/pi-subagents",
  "source": "git+https://github.com/<upstream>/pi-subagents?ref=main",
  "patches": "patches/pi-subagents",
  "build": "npm install --omit=dev --legacy-peer-deps --ignore-scripts" }
```

- **Two of the five keep a written entry, and land in the folder anyway.** `pi-archimedes` loads
  two subpackages, `~/<into>/packages/session-name` and `…/footer`, and `pi-automode` is listed
  only in the `guarded` posture. Each written entry loads its tree, so it replaces the root
  registration, which would load all of `pi-archimedes` and reach every jail with `pi-automode`.
- **Moving `into` rebuilds nothing.** The recipe hash reads neither `into` nor the list
  (`TreeRecipe`), and the extension key reads only `into`'s last segment, which stays
  `pi-subagents` (`patchedext.go`, `ExtensionName`).
- **For hosts on both sides, write the entry too.** A yolo without C1 then loads the tree from
  the written entry, and a yolo with it registers nothing extra ([P5](#p5)). `yolo pack lint`
  notes the entry as redundant but harmless ([PC-D8](#PC-D8)).

**What it amends.** Three rulings and a principle, none of which the first draft cited:

- [`pi-pack-extensions.md`](pi-pack-extensions.md)'s
  [OQ-4](pi-pack-extensions.md#10-decision-ledger): one subdirectory per pack inside the slot,
  which
  [`slots-and-contributions.md`](slots-and-contributions.md#51-what-landed-instead-2026-09-21)
  restates as `<slot>/<pack>`, at every notch. C1 puts a tree named after an extension at that
  depth.
- Its [invariant 2](pi-pack-extensions.md#8-invariants-and-failure-modes): *"Namespacing is the
  contributing pack's name; collisions are impossible."* Under C1 a tree name and a pack name
  share one directory.
- Its [OQ-6](pi-pack-extensions.md#10-decision-ledger): all `files` content in a slot is
  addressed. A written `into` there is not.
- [BA-P4](../reference/agent-briefings.md#ba-p4): *"A content pack names its audience; it never
  names a path."* C1's recommended shape writes pi's slot path in every content pack that uses it.

**The collision, concretely.** pi-subagents' upstream author publishes a yolo pack named
`pi-subagents` that addresses pi. Its landing is `.pi/agent/yolo-packs/pi-subagents`, the path the
maintainer's patched `pi-subagents` would take under C1. Today the two do not collide, because his
tree sits under `yolo-patched/`. Under C1, selecting both must be refused, and nothing refuses it
today. The launch's destination check (`packDestConflicts`) reads the packs as loaded and skips a
contribution with no `into`. `yolo check` compares only the packs yolo ships. With neither, the
boot stops on podman's duplicate-mount-destination error, which names neither pack. C1 would have
to add a check over resolved landings.

**Who must see the registration.** Every reader of a patched extension takes the selected packs
as loaded, never through destination resolution: the launch's block for its patched extensions,
`yolo pack lint`'s patched-tree and duplicate-load lints, the host's advance, its stop at
`yolo host -- pi`, the fork pins and `yolo capture`. A registration added only at destination
resolution reaches `yolo host apply`'s render and none of them. `yolo host -- pi` would then start
pi with a `packages` entry naming a tree nothing built, the failure
[PPX-D18](patched-extensions.md#PPX-D18) exists to stop. So C1 expands at the selection's rewrite
([§5.1](#51-two-expansion-points-both-already-there)).

**The launch's refusal.** [PPX-D40](patched-extensions.md#PPX-D40)'s refusal for a tree with no
build offers dropping "the list entry" as a way back. Under C1 the entry may be implied, so the
refusal names the implied registration and its two ways off: a written entry the pack controls,
such as one in a posture list for one notch only ([PC-D2](#PC-D2)), or an `into` outside the slot.

**On an older yolo.** A yolo with patched extensions but without C1 builds and mounts the tree,
nothing registers it, and the patched-tree lint names the entry to add
([PPX-D10](patched-extensions.md#PPX-D10)). Degraded, and named. A release through v0.11.1 knows
no patched extension and refuses `patches`, whichever `into` it names.

**Core's text.** The patch-series guide, `config-ref`'s `files` entry, and the missing-`into`
refusal in `packdecl` each recommend `.pi/agent/yolo-patched/<name>`. Under C1 they would
recommend the slot. Separately, the patched-tree lint names pi's surface in core's own text today
(`patchedtrees.go:199`); [PC-D4](#PC-D4) fixes that without C1.

**The other spelling.** A new field on the patched extension can name the agent instead, with core
picking a landing in the slot namespaced by the contributing pack. That keeps
[OQ-4](pi-pack-extensions.md#10-decision-ledger) and BA-P4 whole,
and nothing can collide. It is [§2](#2-what-an-older-yolo-does-with-a-convention)'s first
spelling: a yolo with the skip skips the extension and names it, and every release refuses the
pack. This is [OQ-PC3](#OQ-PC3)'s option B.

**What it saves.** Three lines, in one pack: the root entries of three of the maintainer's five
trees.

**The trap.** C1 adds no field, so it looks safe for older hosts, and it is. Its cost is
elsewhere: a tree name enters the namespace that pack names own, nothing checks that clash today,
and every reader of a patched extension would have to see a registration that no line in the pack
spells.

### 4.2 C2: a folder named after an agent is addressed to it

Two folders, both keyed on the selected vocabulary, never on a list in core:

- **`<agent>/` at the pack root** is the pack's addressed tree for that agent. It expands to
  `{"kind": "files", "agents": ["<agent>"], "from": "<agent>"}`.
- **`briefing/<agent>/`** holds prose addressed to that agent. Each `*.md` directly inside
  expands to `{"kind": "briefing", "agents": ["<agent>"], "from": "briefing/<agent>/<file>"}`.
  Today a subdirectory of `briefing/` is not read, and lint names it.

A folder is keyed on the vocabulary, not on a declared destination. A folder naming an agent
that is selected but declares no slot or briefing destination still expands, so the launch's
report of content addressed to no destination fires, as it does for the written form
([R1](../reference/agent-briefings.md#ba-r1)). Expanding only where a destination exists would
drop that report, which report-tiers' P5 forbids.

**Before** (the maintainer's pack as staged on 2026-10-04, `/ctx/packs/matt/pack.json:8-62`):
one briefing line and ten `files` lines.

```jsonc
{ "kind": "briefing", "agents": ["pi"], "from": "files/pi-rules.md" },
{ "kind": "files", "from": "files/themes", "into": ".pi/agent/themes" },
{ "kind": "files", "from": "files/extensions/compact-tools.ts",
  "into": ".pi/agent/extensions/compact-tools.ts" },
// … eight more, one per extension file
```

**After route C** (ruled): the files lines become one,
`{ "kind": "files", "agents": ["pi"], "from": "files/pi" }`, so the pack has two lines for pi.

**After C2**: the tree moves, and one `"conventions": 1` line replaces both.

```
matt/pi/extensions/{compact-tools,thinking-preview,…,slash-aliases}.ts
matt/pi/themes/*.json
matt/briefing/pi/rules.md
```

**What it reverses.** [`pack-system.md`](../reference/pack-system.md#briefing-non-goals)'s
briefing non-goals: *"A filename never addresses. `briefing/claude.md` reaches every agent like
any other file; audience is a manifest field, never an implicit rule read from a name."* A
subdirectory is a name too. And `files` has no conventional source, so its `from` stays required
(`packdecl`'s validation, whose comment cites that non-goal); C2 would give it one. [OQ-PC1](#OQ-PC1) asks
whether to reverse both.

**The rules**, all under [OQ-PC1](#OQ-PC1)'s options A and B ([PC-D9](#PC-D9),
[PC-D11](#PC-D11), [PC-D13](#PC-D13), [PC-D14](#PC-D14)):

- **Only in a pack that opted in.** A pack without the opt-in line reads exactly as today, so an
  upgrade never starts delivering a folder a pack already holds. Folders in every pack, with no
  opt-in, are not offered: an older yolo would deliver nothing and say nothing ([P3](#p3),
  report-tiers' P5).
- **Spelled exactly.** The folder is read only when the pack root holds an entry spelled exactly
  as the identity, as `briefing/` is read ([`pack-system.md`](../reference/pack-system.md#briefing-directory)).
- **The pack's own paths win.** A folder named `skills`, `briefing` or `loopholes`, or named by
  any field of the pack's own manifest (a `from`, a patched extension's `patches`), is never an
  agent folder, whatever a selected pack claims. Otherwise a pack whose `program` is called
  `patches` would receive every opted-in pack's series folder.
- **A folder outside the selected vocabulary.** If a shipped pack claims the name, it fails the
  launch as a written `agents` entry does, under option A
  ([OQ-BA3](../reference/agent-briefings.md#oq-ba3): *"There is no second, laxer tier for a name
  that exists somewhere but is not selected here"*). Under option B it is reported and skipped,
  the laxer tier that ruling rejected. A folder no shipped or selected pack claims is a plain
  folder, and lint names it.
- **Written wins.** For `<agent>/`, a written contribution whose `from` is the folder or a path
  inside it governs the whole folder, because a tree is one mount and cannot lose one file. A
  written addressed `files` tree for the same agent governs it too, because a pack has one per
  agent. For `briefing/<agent>/`, a written `from` governs its one file, and a briefing with no
  `from` governs only the files directly inside `briefing/`. So `matt`'s bare
  `{"kind": "briefing"}` does not take in `briefing/pi/`.
- **No repository instruction file.** `AGENTS.md`, `CLAUDE.md` or `GEMINI.md` inside
  `briefing/<agent>/` is refused in a pack that opted in, by
  [OQ-PB2](../reference/pack-system.md#oq-pb2)'s rule, which refuses those names as a source at
  any depth. Today's check reads only the files directly inside `briefing/`, because nothing
  deeper is a source.
- **The opt-in's value.** A build that reads a higher `conventions` number than it knows applies
  the highest level it knows and names the rest. It never refuses.

**What it saves.** About two lines across today's packs. `matt` saves one: its two route-C lines
become the one opt-in line. The claude pack saves one: its addressed briefing and, under C3, its
loophole line become the opt-in line. Every other pack saves nothing; the pi pack trades its one
addressed briefing for the opt-in line. No pack root yolo ships or the maintainer stages holds a
folder named after an agent today.

**Cost.** A file two agents share must sit in each folder or stay written: the tok-stats core
that the maintainer's Claude mod and pi extension share is the case
(`/ctx/packs/matt-mods/pack.json:10-34`). And a folder is one more place a reader has to look.

**The trap.** The opt-in looks like it costs one line. On a host still on a released yolo, that
line refuses every launch, which is the first patch-series request again. `matt` is selected on
every one of the maintainer's machines, so the refusal would start the moment the file is saved.

### 4.3 C3: a loophole folder is the loophole

**The rule.** Each directory directly under `loopholes/` holding a `manifest.jsonc` expands to
`{"kind": "loophole", "from": "loopholes/<dir>"}`. The module loader already forces the module's
name to equal its directory's. It applies only in a pack that opted in under [OQ-PC1](#OQ-PC1),
and falls with C2 if that question is C. [OQ-PC2](#OQ-PC2) decides whether it exists.

**Before** (`packs/journal/pack.json`, and the same line in every pack that ships a loophole):

```jsonc
{ "contributes": [ { "from": "loopholes/journal", "kind": "loophole" } ], "name": "journal" }
```

**After**: `loopholes/journal/manifest.jsonc`, and a `pack.json` that holds only the opt-in line.

**What it changes, and what it does not.** What a loophole runs on the host lives in the module's
`manifest.jsonc` either way (`kinds.go:225-233`), and its claims are read from there either way
(`moduleClaims`). So the launch banner and `yolo pack footprint` say the same thing, and the
footprint marks the contribution implied. The line in `pack.json` says only where the module is.
A directory under `loopholes/` with no `manifest.jsonc` implies nothing, and lint names it.

**What it saves.** Nothing in a loophole-only pack. Every pack yolo ships has exactly one loophole
line, and the opt-in line would replace it. It saves a line only where a pack opts in for C2 as
well, today the claude pack.

**What it bears on.** [`loophole-system.md`](../reference/loophole-system.md#principles)'s R1,
*"Presence never activates"*; its R2, under which a module's `default_enabled` defaults to off;
[OQ-A7](../reference/loophole-system.md#oq-a7), under which a loophole-only pack still needs
selecting; and [OQ-LP10](../reference/loophole-system.md#oq-lp10), which retired the hand-placed
loopholes directory as *"the one channel that started a host daemon with no selection step at
all"*. C3 keeps R2 and [OQ-A7](../reference/loophole-system.md#oq-a7): the module's own
`default_enabled` and the pack's selection still decide.

**The trap.** The first draft's leaning, that the disclosure is the same either way, is true.
What changes is the act that adds a daemon: a directory, not a line. In the conventional local
pack, which is selected whenever it exists, that is
[OQ-LP10](../reference/loophole-system.md#oq-lp10)'s channel again, behind a one-time opt-in line
instead of none. After the opt-in, a module directory dropped into
`~/.config/yolo-jail/local/loopholes/` joins the next launch with no edit. Its daemon starts if its
manifest says `default_enabled: true` (two of the ten yolo ships do) or the user enabled its name,
and the launch's disclosure names it either way. Excluding the local pack from C3 would close the
channel.

## 5. How core applies a convention

### 5.1 Two expansion points, both already there

| Point | What it already does | What it gains |
| :--- | :--- | :--- |
| **The pack load** (`packload.LoadDir` and its use-read twin), per pack | Runs the manifest decode (`Decode`, `DecodeForUse`, `DecodeTolerant`) and reads the pack's own `briefing/` | C3: `loopholes/*/` directories become contributions. The load has the pack's root, and every reader, `packload.Embedded()`'s included, reads the loaded pack |
| **The selection's rewrite** (`packload.ApplyForks`), per selection | Applies forks wherever a selection is assembled: in `config.SelectPacks`, which the launch, every host verb and `yolo check` call; in the jail's own load of its staged tree; and in an attach's read of the running jail's tree | C1: each registering slot adds an entry for each direct-child patched landing. C2: `<agent>/` folders become addressed `files` contributions, keyed on the selection's vocabulary, which the step also records on each pack |

`briefing/<agent>/` is read inside the one governance reader, `GovernedSources`, from the
vocabulary the selection's rewrite recorded on the pack. So the jail's briefing composer, the
host's borrower and lint read it wherever they read `briefing/` today.

**Not destination resolution.** The first draft put C1 and C2 in `packload.ResolveDestinations`.
Most readers never see its output: the patched-tree readers ([§4.1](#41-c1-a-patched-extension-in-pis-package-folder-loads-with-no-list-entry)),
the jail's surface render (it folds each pack's lists as loaded), the `yolo config render` preview
and `config ls`'s provenance, the launch's destination conflict check, and the jail's briefing. A
registration added there would reach `yolo host apply`'s render and the launch's `files` staging,
and none of those readers.

**The single-pack views**, `yolo pack lint` and `yolo pack footprint <dir>`, run the same step
over the packs yolo ships plus the pack itself ([PC-D10](#PC-D10)).

**The test each owes.** At each place in the table, a test through the production caller that
fails when the expansion call is deleted. A test of the expansion function alone still passes
with its caller gone ([`AGENTS.md`](../../AGENTS.md#testing)).

Two traps carry over from the code:

> [!WARNING]
> **Each conventional folder keeps ONE reader.** `briefing/<agent>/` joins
> `packload.GovernedSources` ([`pack-system.md`](../reference/pack-system.md#one-governance-reader),
> R5). `<agent>/` and `loopholes/` are read by the expansion and by nothing else. A second reader
> that re-lists a folder will disagree with the first, which is how one narrow declaration once
> switched off a pack's whole broadcast at every notch.

> [!WARNING]
> **Governance reads the written declaration, never the expanded one.** Every expansion records
> the pack's written declaration as `Pack.origDecl`, which destination resolution already keeps
> when it is set. Otherwise an implied contribution read back as written would govern its own
> source and switch the convention off for the next reader. Every other reader takes the expanded
> set.

The expansion is a pure function of the pack's tree and the selected packs' declarations. It
runs once per read, holds no state between reads, and has no ordering question beyond pack order,
which every fold already uses.

### 5.2 What the reports show

| Reader | What it shows for an implied contribution |
| :--- | :--- |
| `yolo pack lint <dir>` | Validates the expanded set strictly, then prints one line per convention it applied. It runs with no config, so for C1 and C2 it reads the slots and identities of the packs yolo ships plus the pack itself, and says so. It can therefore miss a C2 delivery to an agent that a configured, non-shipped pack provides, which the launch then makes ([PC-D10](#PC-D10)) |
| `yolo pack footprint` | One claim line per implied contribution, as for a written one, marked with its rule and the pack that declared it. `--format json` carries the same as `implied_by` ([PC-D7](#PC-D7)) |
| `yolo check`, the launch, `yolo host apply` | Resolve against the selected set. A tier-4 disclosure of an implied claim, such as a patched extension's build line or a loophole's daemon, is printed exactly as the written one's |

*Shape, not wording*, for a pack that has moved two patched extensions into the folder under
[OQ-PC3](#OQ-PC3)'s option A. Every name in the note comes from a declaration:

```
files        .pi/agent/yolo-packs/pi-subagents   patched extension of git+https://…/pi-subagents?ref=main  (review)
config-list  pi/settings /packages + "~/.pi/agent/yolo-packs/pi-subagents"
             implied: slot .pi/agent/yolo-packs registers what lands in it (declared by pack pi)
files        .pi/agent/yolo-packs/pi-archimedes  patched extension of git+https://…/pi-archimedes?ref=main  (review)
config-list  pi/settings /packages + "~/.pi/agent/yolo-packs/pi-archimedes/packages/session-name"
```

### 5.3 Written wins

[P5](#p5) in each convention:

- **C1.** A written entry of the contributing pack that loads the tree replaces its
  registration.
- **C2.** A written `from` naming an `<agent>/` folder, or a path inside it, governs the folder,
  and so does a written addressed tree for the same agent. In `briefing/<agent>/` a written `from`
  governs its one file, and a briefing with no `from` governs none of them.
- **C3.** A written loophole contribution naming the directory is the same contribution, and is
  not doubled.

A written contribution that equals what a convention would imply gets a lint `note:` saying it can
go once every host reads the convention. It is never a warning, and the exit status is unchanged
([PC-D8](#PC-D8)).

## 6. Behavior in every case

| Case | Behavior |
| :--- | :--- |
| No pack uses a convention | Every rendered file is byte-identical to today's |
| C1 ([OQ-PC3](#OQ-PC3) A): a patched extension lands directly in a registering slot, and the contributing pack writes no entry that loads it | One registration at every notch, attributed to the contributing pack. The patched-tree lint is satisfied |
| C1: a written entry of the contributing pack loads it (any list, any posture) | That entry alone. No registration |
| C1: a tree lands deeper than a direct child of the slot | Not registered. `yolo pack lint` and `yolo check` warn, naming the direct-child form. Never refused ([PC-D3](#PC-D3)) |
| C1: a plain `files` contribution is written into the slot | Not registered. Lint names route C's addressed form |
| C1: the tree has no build at this notch | The registration counts as a written entry would, and [PPX-D18](patched-extensions.md#PPX-D18) and [PPX-D40](patched-extensions.md#PPX-D40) decide. The refusal names the implied registration and its ways off ([§4.1](#41-c1-a-patched-extension-in-pis-package-folder-loads-with-no-list-entry)) |
| C1: no registering slot is selected | Nothing is registered, and the patched-tree lint fires, naming the entry `~/<into>` and no agent ([PC-D4](#PC-D4)) |
| C1: a tree's landing equals another pack's addressed landing | Refused at the launch, naming both packs, by the check over resolved landings C1 adds. Nothing compares them today |
| C1: the pack is dropped | The registration goes with it, at both notches: `config-list`'s rule ([`pack-pi-resources.md`](pack-pi-resources.md#34-behavior-in-every-case)) |
| C2 ([OQ-PC1](#OQ-PC1) A or B): an agent folder in an opted-in pack, naming an agent in the selected vocabulary | Delivered as the written addressed contribution would be, the no-destination report included |
| C2: the folder names an agent a shipped pack claims and this jail does not select | Fatal, as a written `agents` entry is, under A. Reported and skipped under B |
| C2: no shipped or selected pack claims the name | A plain folder. Lint names it |
| C2: the pack did not opt in | Nothing, as today |
| C2: an empty folder, or one spelled in another case | Nothing |
| C2: a written contribution governs the folder | The written one alone. Lint names the folder it did not deliver |
| C3 ([OQ-PC2](#OQ-PC2) A): `loopholes/<dir>/manifest.jsonc` in an opted-in pack | One loophole contribution, review-worthy as every loophole is |
| C3: a directory there with no `manifest.jsonc` | Nothing. Lint names it |
| A yolo with the skip, older than a convention | That part is left out and named: C1 by the patched-tree lint, C2 and C3 by the ignored opt-in line |
| A release through v0.11.1 | Under C1 the content pack writes no new field, so it reads as today (and `patches` is refused, as today). The opt-in line refuses the pack and every launch on that host |
| macos-user and Apple Container | Whatever the written form does there. C1 and C2 ride the addressed-tree delivery, **UNVERIFIED** per backend as in [`pack-pi-resources.md`](pack-pi-resources.md#34-behavior-in-every-case) |

**Forbidden.** Core never names an agent in a convention, nor in its messages. An implied
contribution is never left out of a report a written one appears in. A convention never fills in
a field an author left out, and never makes a pack that a yolo with the skip refuses.

## 7. Considered and not proposed

| Shape | Verdict |
| :--- | :--- |
| A pack-wide `defaults` block for any field | **Rejected.** An older yolo ignores the block and says it did, but still uses the contributions it shaped, with other values: a patched extension built without its `build` line, a different tree under a different recipe. A default for a required field leaves the contribution refused |
| Omission defaults: `codec` from the extension, `patches` from `into` | **Rejected here**; per-kind defaults are [OQ-M1](manifest-language.md#OQ-M1)'s, and [§2](#2-what-an-older-yolo-does-with-a-convention) records what they cost |
| A patched extension addressed with `agents` and no `into` | **Rejected.** An older yolo refuses `agents` beside `source`, and the launch fails |
| A new kind bundling a tree and its list entry | **Rejected.** A second name for one destination, whose combine rule would have to track `files` and `config-list` by hand |
| `each`: one declaration fanned out to one claim per file of a folder | **Not proposed.** Route C covers the only folder with several writers in the corpus, pi's extensions. Revisit when a second agent's shared folder appears |
| A provider implying its same-named profile | **Rejected.** It saves two lines per provider pack, all shipped, and a selector a user types belongs in the file that defines it; `openai-codex`'s profile is named `codex` in the packs that use it |
| One `install_hints` value for every manager | **Rejected.** The value is per manager by nature, a shipped hint must cite a source per manager (`TestEveryShippedAndExampleInstallHintNamesItsSource`), and the skip-safe spelling would be a second field for one map |
| **A posture naming a surface instead of restating it** (the first draft's C4: `"managed": {"pi/settings": {…}}` in place of each `config` entry) | **Handed to [OQ-M1](manifest-language.md#OQ-M1).** It is a second spelling of the pack's own declaration, not an expansion, so it is syntax ([§8](#8-non-goals)). Review found four costs. It saves 10 entries, all in packs yolo ships, which travel with their own binary. On a yolo with the skip but without it, the `guarded` half is kept without its keys, and the posture left behind validates empty (`validateAutonomyPosture`). So the host agent runs without yolo's guarded settings while the kept-without-field line says the restriction holds, which reverses [PF-D69](patched-forks.md#PF-D69)'s premise; every release refuses it outright. On another pack's surface it has no `path` and `codec` to carry, which the config engine requires and [NS-D21](notch-scoped-config-contributions.md#NS-D21) compares. And four readers decode posture entries, not `packdecl`. [OQ-M1](manifest-language.md#OQ-M1)'s rewrite of every posture would also make it a third spelling |
| A shared-dir hook implying its machine `state` | **Not proposed.** Three pairs, all in shipped packs. Found on the way, a lint bug: `Decode` checked only that a hook's `at` was present, and the match against the pack's machine `state` was made only at boot, so `yolo pack lint` and `yolo check` passed a pack whose boot then failed. Fixed by [PC-D15](#PC-D15) |
| A manifest written in Lua or Starlark | [`manifest-language.md`](manifest-language.md)'s [OQ-M2](manifest-language.md#OQ-M2) and [OQ-M3](manifest-language.md#OQ-M3) |

## 8. Non-goals

- **Grouping by kind, the syntax, per-kind defaults, and the posture spelling**:
  [`manifest-language.md`](manifest-language.md).
- **Naming the agent once per pack**: ruled ([OQ-D5](slots-and-contributions.md#OQ-D5)), and its
  build waits on [OQ-D6](slots-and-contributions.md#OQ-D6).
- **A registration that loads part of a tree, or reaches one posture.** The written entry does
  that, and replaces the registration ([PC-D2](#PC-D2)).
- **Moving the pi pack's own `yolo-*.js` extensions** into its package folder. Possible under
  route C, and a change to pi's own pack that [`pack-pi-resources.md`](pack-pi-resources.md#34-behavior-in-every-case)
  leaves unchanged.

## 9. Risks

| Risk | Mitigation |
| :--- | :--- |
| A pack gains a convention's field or the opt-in line while a host it reaches runs a release through v0.11.1 | That host refuses every launch. Nothing here is built before [§11](#11-what-i-would-build-in-order) step 0, and a pack an older host reads keeps the written form |
| A host on a yolo with the skip, older than C1, reads a pack that relies on C1, and pi does not load the tree | The patched-tree lint names it at that launch. Write the entry too until every host reads C1 ([§4.1](#41-c1-a-patched-extension-in-pis-package-folder-loads-with-no-list-entry)) |
| Under C1, a tree name collides with a pack name | The check over resolved landings C1 adds refuses the pair at the launch, naming both |
| A folder that happens to carry an agent's name is delivered to that agent | Only in a pack that opted in ([OQ-PC1](#OQ-PC1) A or B), and only for a name in the selected vocabulary |
| A loophole module dropped into an opted-in pack runs on the host | Only when its manifest is default-enabled or the user enabled its name; the launch's disclosure names it. In the local pack this is [OQ-LP10](../reference/loophole-system.md#oq-lp10)'s channel behind one line ([§4.3](#43-c3-a-loophole-folder-is-the-loophole)) |
| A second reader of a conventional folder drifts from the first | [§5.1](#51-two-expansion-points-both-already-there)'s warning. One reader |
| An implied contribution is read back as written and governs itself | Every expansion records the written declaration ([§5.1](#51-two-expansion-points-both-already-there)) |
| An expansion call is deleted at one place, and the readers there quietly see only the written form | The deletion test at each place ([§5.1](#51-two-expansion-points-both-already-there)) |

## 10. What done looks like

- **[PC-D4](#PC-D4)**: the patched-tree lint names the list from a registering slot's
  declaration, no string `pi/settings` remains in core's text for it, and with no registering
  slot selected it names the entry `~/<into>` and no agent. The missing-`into` refusal gives no
  agent's path.
- **If [OQ-PC3](#OQ-PC3) is A**: the maintainer's patched extensions pack, with three trees in
  pi's package folder and no list entry for them, gives a jail's pi all three, and
  `settings.json`'s `packages` holds one `~/.pi/agent/yolo-packs/<name>` entry for each.
  `pi-archimedes` and `pi-automode` in the same folder load as their written entries say, with no
  root entry. `yolo pack footprint` lists each registration, marked implied, with the slot's
  pack. A pack landing at one of those paths is refused at the launch, naming both.
- **If [OQ-PC1](#OQ-PC1) is A or B**: an opted-in pack's `pi/` and `briefing/pi/` reach pi as
  their written forms would, and a pack that did not opt in renders byte-identical files.
- Each convention built appears in `yolo features`, and a yolo with the skip that is older than
  it, reading a pack that uses it, launches and names what it left out.

## 11. What I would build, in order

0. **A release carrying the skip, with every host on it.** This is the maintainer's first
   patch-series request, and it is not this doc's build. It gates every new pack field anywhere,
   this doc's included: until a host runs it, a field that host's yolo does not know refuses every
   launch there. It is also what the patch-series work waits on. Nothing below is.
1. **[PC-D4](#PC-D4), inside [`pack-pi-resources.md`](pack-pi-resources.md)'s build**: the
   patched-tree lint names the list from the registering slot's `register`, and the missing-`into`
   refusal drops pi's path. That build registers addressed trees only. Keep its emission a loop
   over landings, so [OQ-PC3](#OQ-PC3)'s option A, if ruled, is one more predicate.
2. **After [OQ-PC3](#OQ-PC3), if A**: emission over direct-child patched landings at the
   selection's rewrite, the written-entry replacement, the launch's check over resolved landings,
   PPX-D40's wording, the guide and `config-ref`, and its feature name.
3. **After [OQ-PC1](#OQ-PC1), if A or B**: the opt-in line and its feature name, and agent
   folders at the selection's rewrite and in `GovernedSources`.
4. **After [OQ-PC2](#OQ-PC2), if A**: loophole folders, in the load.
5. **The maintainer**: his packs, keeping written entries until every host he uses reads each
   convention.

## 12. Open questions

1. 💬 <a id="OQ-PC1"></a>**OQ-PC1: May a pack opt in to folders named after an agent, so `pi/`
   and `briefing/pi/` reach pi with no line each?**

   Under route C, `matt` has two lines for pi. This would make them one opt-in line, and it
   reverses the ruled non-goal *"A filename never addresses"*
   ([§4.2](#42-c2-a-folder-named-after-an-agent-is-addressed-to-it)).

   - **A — Yes, with `"conventions": 1`, and a folder naming an agent this jail lacks fails the
     launch**, as a written `agents` entry does ([BA-P3](../reference/agent-briefings.md#ba-p3)).
   - **B — Yes, but such a folder is reported and skipped**: the laxer tier
     [OQ-BA3](../reference/agent-briefings.md#oq-ba3) rejected.
   - **C — No.** One written line per folder, as today.

   <!-- vantage: question id=OQ-PC1 leaning="C: across today's packs the folders save two lines, and A or B adds a line every released yolo refuses at every launch. Revisit once every host runs a release with the skip and a second pack ships agent-only folders." -->

   _Leaning:_ **C**: across today's packs the folders save two lines, and A or B adds a line every
   released yolo refuses at every launch. Revisit once every host runs a release with the skip and
   a second pack ships agent-only folders.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 <a id="OQ-PC2"></a>**OQ-PC2: May a `loopholes/<name>/` folder alone give an opted-in pack
   that loophole?**

   Every loophole yolo ships is one line naming its own folder, and runs a daemon on the host
   ([§4.3](#43-c3-a-loophole-folder-is-the-loophole)). Asked only if [OQ-PC1](#OQ-PC1) is A or B.

   - **A — Yes.** Disclosed as today, marked implied. A module directory added to an opted-in
     pack joins the next launch with no edit.
   - **B — No.** A loophole is always one written line.

   <!-- vantage: question id=OQ-PC2 leaning="B: each loophole pack would trade its one loophole line for the opt-in line, saving nothing, and in the conventional local pack A reopens the channel OQ-LP10 retired." -->

   _Leaning:_ **B**: each loophole pack would trade its one loophole line for the opt-in line,
   saving nothing, and in the conventional local pack A reopens the channel
   [OQ-LP10](../reference/loophole-system.md#oq-lp10) retired.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 <a id="OQ-PC3"></a>**OQ-PC3: May a patched extension placed directly in pi's package folder
   load with no list entry?**

   Each of the maintainer's patched extensions is a `files` tree plus a `config-list` entry naming
   it ([§4.1](#41-c1-a-patched-extension-in-pis-package-folder-loads-with-no-list-entry)). The
   first draft decided this as an implementation choice; it amends rulings, so it is asked.

   - **A — Yes, when `into` is a direct child of the folder.** Three of five trees lose a line,
     and a pack named like an extension collides with it. Amends
     [OQ-4](pi-pack-extensions.md#10-decision-ledger) and
     [BA-P4](../reference/agent-briefings.md#ba-p4).
   - **B — Yes, through a new field naming the agent.** No collision and no pi path, but every
     release through v0.11.1 refuses the pack.
   - **C — No.** Keep the entry.

   <!-- vantage: question id=OQ-PC3 leaning="C: it saves three lines in one pack; A buys them by amending a ruled invariant and opening a collision nothing checks today, and B adds a field every released yolo refuses. B is the shape to revisit once every host runs a release with the skip." -->

   _Leaning:_ **C**: it saves three lines in one pack. A buys them by amending a ruled invariant
   and opening a collision nothing checks today, and B adds a field every released yolo refuses.
   B is the shape to revisit once every host runs a release with the skip.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

Implementation decisions made in drafting and review, each reversible and each the maintainer's
to overrule. A decision under an open question applies only if that question is ruled its way.

| ID | Decision | Why it holds | Built |
| :--- | :--- | :--- | :--- |
| <a id="PC-D1"></a>PC-D1 | **Withdrawn in review, 2026-10-05: now [OQ-PC3](#OQ-PC3).** It registered a written `into` directly in a registering slot as an implementation decision. That amends [`pi-pack-extensions.md`](pi-pack-extensions.md)'s [OQ-4](pi-pack-extensions.md#10-decision-ledger), [OQ-6](pi-pack-extensions.md#10-decision-ledger) and [invariant 2](pi-pack-extensions.md#8-invariants-and-failure-modes), and [BA-P4](../reference/agent-briefings.md#ba-p4) | A ledger decision may not override a ruling | — |
| <a id="PC-D2"></a>PC-D2 | Under [OQ-PC3](#OQ-PC3) A: a written list entry of the **contributing pack** that loads a tree (`loadsTree`, [PPX-D36](patched-extensions.md#PPX-D36)), in any list or posture, replaces that tree's registration | [P5](#p5); `owningAgentPack` reads only the contributing pack's lists; lets a monorepo tree and a guarded-only tree live in the folder | — |
| <a id="PC-D3"></a>PC-D3 | Under [OQ-PC3](#OQ-PC3) A: a tree deeper than a direct child of a registering slot is not registered; lint and `yolo check` warn, naming the direct-child form; never refused | A deeper tree can sit inside another pack's landing, and a new refusal on a use read refuses a launch | — |
| <a id="PC-D4"></a>PC-D4 | The patched-tree lint names the list from the `register` of a registering slot the selected packs declare (the shipped packs, at `yolo pack lint`), not pi's surface. With none selected it names the entry `~/<into>` and no agent. The missing-`into` refusal gives no agent's path. Needs no ruling, and rides [`pack-pi-resources.md`](pack-pi-resources.md)'s build | [P2](#p2): core's text names pi today, at `patchedtrees.go:199` and in the missing-`into` refusal's example in `patchedext.go` | — |
| <a id="PC-D5"></a>PC-D5 | **Withdrawn in review, 2026-10-05: the posture spelling is handed to [OQ-M1](manifest-language.md#OQ-M1)** ([§7](#7-considered-and-not-proposed)) | It is syntax, and review found it reverses [PF-D69](patched-forks.md#PF-D69)'s premise for `guarded` on an older host | — |
| <a id="PC-D6"></a>PC-D6 | Each convention gets a `yolo features` name in the commit that builds it; names provisional | [PF-D71](patched-forks.md#PF-D71)'s rule: a yolo without it would skip that use | — |
| <a id="PC-D7"></a>PC-D7 | An implied contribution carries its rule and declaring pack, never serialized into a manifest; footprint, lint, `yolo check`, the launch and `yolo host apply` name it; JSON outputs carry `implied_by` | [P4](#p4) | — |
| <a id="PC-D8"></a>PC-D8 | Lint prints a `note:` for a written contribution equal to what a convention implies, saying it can go once every host reads the convention; never a warning, exit status unchanged | The written form is the one an older host needs, so lint must not push authors off it | — |
| <a id="PC-D9"></a>PC-D9 | Under [OQ-PC1](#OQ-PC1) A or B: an agent folder is read only in a pack that opted in, and only when the pack root holds an entry spelled exactly as the identity. A folder named like a core folder (`skills`, `briefing`, `loopholes`), or named by any field of the pack's own manifest, is never one; that list is derived from the manifest, never enumerated | The `briefing/` rule ([`pack-system.md`](../reference/pack-system.md#briefing-directory)); without the derived list, a pack claiming an agent name like `patches` would receive every opted-in pack's series folder | — |
| <a id="PC-D10"></a>PC-D10 | Single-pack views resolve agent-declared conventions against the packs yolo ships plus the pack itself, and say so, including that a delivery to an agent a configured, non-shipped pack provides is not shown. `yolo check` and the launch resolve against the selected set | The lint takes no config. The shipped set is what `reportShippedSurfaceClash` already reads for the same reason, and it warns rather than refuses because the selection is unknown there | — |
| <a id="PC-D11"></a>PC-D11 | Under [OQ-PC1](#OQ-PC1) A or B: a written `from` naming an `<agent>/` folder or a path inside it governs the whole folder, and a written addressed tree for the same agent does too. In `briefing/<agent>/` a written `from` governs its one file, and a briefing with no `from` governs only files directly inside `briefing/`. Agent folders expand for every name in the selected vocabulary, whether or not that agent declares a destination | A tree is one mount; one addressed tree per agent per pack; briefing governance is per file; expanding by vocabulary keeps the no-destination report ([R1](../reference/agent-briefings.md#ba-r1)) firing as for the written form. What a folder naming an unselected agent does is [OQ-PC1](#OQ-PC1)'s A or B | — |
| <a id="PC-D12"></a>PC-D12 | C1 and C2 expand at the selection's rewrite, beside `packload.ApplyForks`, wherever it runs; C3 at the pack load; `briefing/<agent>/` inside `GovernedSources`, from the vocabulary the rewrite records on the pack. Every expansion records the written declaration as `origDecl`, and each place gets a test that fails when its call is deleted | The readers of a selection take it as loaded, not resolved ([§5.1](#51-two-expansion-points-both-already-there)), and `ApplyForks` is already the one rewrite every assembled selection passes | — |
| <a id="PC-D13"></a>PC-D13 | Under [OQ-PC1](#OQ-PC1) A or B: the opt-in is `"conventions": <n>`. A build reading an `n` above its highest applies its highest and names the rest in one line, never refuses. Each level gets a `yolo features` name | The use read refuses a malformed value, so a build that read `2` as malformed would refuse every launch, against [P3](#p3) | — |
| <a id="PC-D14"></a>PC-D14 | Under [OQ-PC1](#OQ-PC1) A or B: `AGENTS.md`, `CLAUDE.md` or `GEMINI.md` inside `briefing/<agent>/` is refused in a pack that opted in. In a pack that did not, the folder stays unread | [OQ-PB2](../reference/pack-system.md#oq-pb2) refuses those names as a source at any depth, and the load's check reads only the files directly inside `briefing/` today; opt-in only, so an upgrade refuses no existing pack | — |
| <a id="PC-D15"></a>PC-D15 | A `shared_credentials` or `shared_directory` hook whose `at` is not one of its pack's machine-scope `state` dirs is refused by every host read of the manifest (`yolo pack lint`, `yolo check`, a launch's use read), through the predicate and the sentence the boot's hook step uses (`packdecl.Manifest.DeclaresMachineState`, `packdecl.UndeclaredHookStateProblem`), and the sentence names the `state` line to add. `unshare_directory` is exempt: it undoes a link into a dir the pack no longer declares. The jail's tolerant decode does not run it, so the boot's own refusal at the hook step stays where it was | The boot already refuses this pack, so a host refusal moves the same failure to where it is cheap to fix and refuses no pack that could boot. A machine `state` skipped as version skew on a use read leaves its hook refused on the host as the boot would refuse it; skipping the hook with it, as [PF-D76](patched-forks.md#PF-D76) skips an adapter, is not built | 2026-10-06 |

## Appendix A: counts

Measured 2026-10-05 at `c321336a8` over `packs/*/pack.json` (comments stripped), and over the
maintainer's packs as staged on 2026-10-04 at `/ctx/packs/`.

| What | Count |
| :--- | ---: |
| Shipped packs / contributions | 24 / 103 |
| Packs shipping a loophole / loophole lines in each, every one `from: "loopholes/<name>"` | 10 / 1 |
| Shipped loophole modules whose manifest says `default_enabled: true` | 2 of 10 |
| Posture config entries, every one on its own pack's surface and restating its `path` and `codec` | 10 |
| Config surfaces / of them whose `codec` is spelled as the file extension | 21 / 18 (21 if `.yml` reads as `yaml` and `.jsonc` as `json`) |
| Briefing destinations whose `after` is `host:` plus `into` | 7 |
| `state` entries / of them writing `"scope": "workspace"`, the default | 9 / 6 |
| Profiles / of them named after the provider they select | 11 / 6 |
| `"agent":` keys in shipped packs (surfaces, postures, destinations) | 45 |
| Shipped `files` entries whose `into` ends in the `from` file's name (four in pi, one in opencode) | 5 |
| Shipped packs declaring a `files` slot | 0 |
| Shipped addressed briefings (claude, pi) | 2 |
| Pack roots, shipped or the maintainer's, holding a folder named after an agent | 0 |
| Maintainer's `matt`: `files` entries naming one pi path each, plus one addressed briefing | 10 + 1 |
| Maintainer's `matt-mods`: `files` entries naming one pi path each | 5 |
| Maintainer's `config-overlay` contributions (`matt` 6, `matt-mods` 1, `matt-fzf` 1); shipped | 8; 0 |
| Maintainer's patched extensions, each a `files` and a matching list entry / with a root entry C1 would remove | 5 / 3 |

## Appendix B: evidence

- **The skip and the refusal**: `internal/packdecl/skew.go`, `DecodeForUse` (its doc names a
  malformed value and a missing required field as problems), `restrictingKind`,
  `unknownFieldSkip`, and `unknownFields` (a pack-wide unknown field is ignored and named);
  `internal/packdecl/packdecl.go`, `DecodeTolerant` and the strict `Decode`.
- **A release refuses any unknown field**: `git show v0.11.1:internal/packdecl/packdecl.go`,
  whose `Decode` calls `DisallowUnknownFields`, and whose host loads use it
  (`v0.11.1:internal/packload/packload.go`, `TolerateSkew` set only by the entrypoint); v0.11.1
  has no `skew.go` and no `features.go`.
- **Strict decode below a contribution**: `manifest.DecodeSurfaces`
  (`internal/agentcfg/manifest/load.go`) and `manifest.DecodePostureOverlay`
  (`internal/agentcfg/manifest/overlay.go`); `AutonomyPosture.Config` is raw JSON
  (`internal/packdecl/contributes.go`).
- **A patched extension refuses `agents`**: `internal/packdecl/patchedext.go:110-114`; its name is
  `into`'s last segment (`ExtensionName`); the recipe reads no `into` (`TreeRecipe`); the
  missing-`into` refusal's example path.
- **The pair**: `internal/packload/patchedtrees.go`, `TreeListEntry`, `owningAgentPack` (the
  contributing pack's lists only) and `LintPatchedTrees` (naming pi's surface at `:199`);
  `integration/patchedextension_test.go:84-90`; the patch-series guide's example,
  [`patch-series.md`](../../userguide/guides/patch-series.md#patch-a-pi-extension).
- **The patched-tree readers take the selection as loaded**: `run.notePatchedTrees`
  (`internal/cli/run/run.go`, over `staged.packs`), `cli.advanceHostTrees` and `cli.hostTreeGate`
  (`internal/cli/hosttrees.go`, over `selectConfiguredHostPacks`), `internal/cli/forkpin.go`,
  `internal/cli/capturehost.go`, and `LintPatchedTrees` / `LintDuplicateLoads` on one pack
  (`internal/cli/pack.go`).
- **The selection's rewrite**: `config.PackSelection.applyForks` inside `config.SelectPacks`
  (`internal/config/packselection.go`), the entrypoint's `loadPackRoot`
  (`internal/entrypoint/packsurfaces.go`), and an attach's `loadPackTree`
  (`internal/cli/run/packtree.go`), each calling `packload.ApplyForks`.
- **The landing and its checks**: `internal/packload/mergedest.go`, `SlotLanding` and
  `ResolveDestinations` (a clone keeps an `origDecl` already set); `run.packDestConflicts`
  (`internal/cli/run/packfiles.go`, skipping a contribution with no `into`); `yolo check`'s
  `packload.Collisions(packload.Embedded())` (`internal/cli/check/packs.go`); `filesTarget`
  (`internal/packload/footprint.go`).
- **Audience**: `packload.AgentAudienceProblems` (`internal/packload/agentaudience.go`), fatal at
  the launch (`internal/cli/run/packs.go`); the no-destination report
  (`internal/cli/run/unmatchedaudience.go`); what claims an agent name (`agentNameClaims`,
  `internal/packload/footprint.go`).
- **Briefing governance**: `internal/packload/governance.go`, `GovernedSources`,
  `governedBriefing` (a from-less briefing governs the remainder) and `declaration`;
  `reservedBriefingFiles` (`internal/packload/packload.go`, direct children only);
  `RepositoryInstructionFile` (`internal/packdecl/contributes.go`); `files` has no conventional
  source (`internal/packdecl/contributes.go`, the comment above the patched-extension switch).
- **Posture restatement**: `internal/agentcfg/manifest/load.go:74-79` (path and codec required);
  `foldPostureManaged` (`internal/packload/packload.go`, matching agent and name);
  `internal/packoverlay/packoverlay.go:394-405` (NS-D21's mismatch refusal);
  `validateAutonomyPosture` (`internal/packdecl/contributes.go`, accepting an empty posture).
- **Existing conventions**: `internal/packdecl/contributes.go`, `DefaultSkillsDir` and
  `DefaultBriefingDir`; `internal/packload/deriveenv.go:43` (`derive.lua`);
  `internal/packdecl/kinds.go:66-75` (a wrapped plugin recognized from the tree).
- **The loophole module**: `internal/packdecl/kinds.go:216-241`; `default_enabled` in each
  `packs/*/loopholes/*/manifest.jsonc`; the retired directory's notice,
  `internal/loopholes/retired.go`.
- **The feature names**: `internal/packdecl/features.go`, `namedFeatures`.
- **The hook pair, checked at boot only**: `internal/packdecl/contributes.go:3844-3849`;
  `internal/entrypoint/packhooks.go:269-274`; `RunPackHooks`'s one production caller,
  `internal/entrypoint/bootsteps.go`.
- **The maintainer's packs**: `/ctx/packs/matt/pack.json:5-62` and
  `/ctx/packs/matt-mods/pack.json:10-34` (staged copies, outside the repository); his patch-series
  requests and the 2026-10-05 pack advice are kept outside the repository too
  (`scratch/yolo-patch-series-requests.md`, `.yolo/durable/pack-advice/2026-10-05-first-patched-launch.md`).
