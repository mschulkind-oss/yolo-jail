---
title: "A pack file should state only what is particular to it: four conventions that imply the rest, and the rule that keeps an older yolo safe"
date: 2026-10-05
status: in-review
stage: DESIGN
next: "Rule OQ-PC1, whether a folder named after an agent delivers to it with no line in pack.json; PC-D1, which lets a patched extension in pi's package folder load with no list entry, needs no ruling and rides pack-pi-resources.md's build"
tags: [packs, manifest, conventions, pi, patched-extensions, skew, design]
summary: "Pack files repeat facts the system already knows: a patched pi extension and the list entry naming its own path, one entry per file in a shared folder, a permission posture restating a surface's path and codec, a loophole line naming its own folder. Each convention here is a load-time expansion into contributions the pack could have written, so validation, the footprint, the launch's disclosures and the render never know a convention exists. An agent pack declares the conventions about its own layout and core applies them by name. Every convention is spelled so a yolo older than it skips that part and names the skip, never refuses the pack, because omitting a field an older yolo requires refuses every launch on that host."
vantage:
  status-chip: true
---

# A pack file should state only what is particular to it

**Status:** 2026-10-05. Nothing built. Read against the tree at `c321336a8`; counts are in
[Appendix A](#appendix-a-counts), evidence in [Appendix B](#appendix-b-evidence).

> **In short.** A convention is a load-time expansion into contributions the pack could have
> written, so nothing after it (validation, the footprint, the launch's disclosures, the render)
> knows it exists. What is new is only who writes the rule: the pack that owns a layout declares
> it, and core applies it by name, never by agent.

**Why it matters.** The maintainer, 2026-10-05: *"we need to be able to consolidate our pack
files, they repeat way too much. we need a bunch of convention over configuration."* His patched
pi extensions each take a `files` contribution and a list entry spelling the same path.

**The shape.** Two expansion points that already exist, the pack load and destination
resolution, gain four conventions ([§4](#4-the-conventions)). **Cost:** two spellings of a few
things for good, and a yolo older than a convention leaves that part out, saying so.

**Start at [§2](#2-what-an-older-yolo-does-with-a-convention)**: what an older yolo does with a
pack decides what shape every convention may take.

**Needs your ruling:** [OQ-PC1](#OQ-PC1), [OQ-PC2](#OQ-PC2).

**Reads with:** [`pack-pi-resources.md`](pack-pi-resources.md) (the slot [PC-D1](#PC-D1) widens),
[`patched-extensions.md`](patched-extensions.md) (the tree and its entry),
[`manifest-language.md`](manifest-language.md) (syntax and per-kind defaults),
[`slots-and-contributions.md`](slots-and-contributions.md) (the agent named once per pack), and
[`pack-conventions-plan.md`](pack-conventions-plan.md) (the implementation sketch, incomplete).

---

## Defined terms

- **Convention** *(coined here)*: a rule core applies to a pack's tree, or to a declaration
  another selected pack made, that yields contributions the pack did not write. Not a default
  value for a field: a convention never fills in a field an author left out
  ([§2](#2-what-an-older-yolo-does-with-a-convention) says why).
- **Implied contribution** *(coined here)*: a contribution a convention yields. It is an ordinary
  contribution plus a note of the rule and the pack that declared it.
- **Expansion** *(coined here)*: the step that turns a pack's tree and declarations into its
  implied contributions ([§5.1](#51-two-expansion-points-both-already-there)).
- **Written form**: the same contributions spelled out in `pack.json`, as today.
- **Use read** and **authoring read**: terms coined by
  [PF-D68](patched-forks.md#PF-D68). A use read is one a launch or host verb acts on, and it skips
  what it cannot read; an authoring read (`yolo pack lint`, `yolo pack footprint`) refuses it.
- **Slot**, **addressed tree**, **landing** and **registration**: as
  [`pack-pi-resources.md`](pack-pi-resources.md#defined-terms) defines them. A slot is a `files`
  destination an agent pack declares; an addressed tree is content sent to it with `agents`; its
  landing is `<slot>/<pack>`; a registration is the list entry core adds for a landed tree.
- **Patched extension**: a `files` contribution with `source` and `patches`, built from an
  upstream with a series replayed ([`patched-extensions.md`](patched-extensions.md#1-defined-terms)).
- **Posture**: one side of an `autonomy` contribution, `autonomous` (every jail) or `guarded` (the
  host). Its `config` entries set keys on a config surface.
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
  [OQ-D5](slots-and-contributions.md#OQ-D5): core knows a name only as something that resolves
  an address.
- <a id="p3"></a>**P3. A convention is spelled so an older yolo skips it and says so, never
  refuses the pack.** [§2](#2-what-an-older-yolo-does-with-a-convention).
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

The use read and the jail's tolerant read **skip** a contribution holding a kind, a `via` or a
field they do not know, and name the skip in one line. They **refuse** a missing required field,
a malformed value, or a combination they validate as wrong. [PF-D68](patched-forks.md#PF-D68):
*"What no newer yolo makes readable stays a problem: … a missing required field."* A refusal fails
every launch on that host, which is the first of the maintainer's patch-series requests over again.

So the obvious spelling of a convention, leaving a field out, is the one spelling that refuses:

| Convention spelled as an omission | What a yolo older than it does |
| :--- | :--- |
| `codec` taken from the file extension | `missing "codec"`: refused |
| A patched extension's `patches` defaulting to `patches/<name>` | `source` without `patches` is refused on `files` |
| A patched extension addressed with `agents` instead of `into` | `agents` beside `source` is refused (`patchedext.go:110-114`) |
| A pack-wide `defaults` block | Ignored, so a default for an optional field silently changes the contribution, and a default for a required one is refused ([§7](#7-considered-and-not-proposed)) |

**The rule.** Every convention here takes one of three spellings, each of which an older yolo
skips and names:

1. **A field or kind an older yolo does not know.** It skips the contribution, or keeps a
   restricting kind without the field ([PF-D69](patched-forks.md#PF-D69)), and says so.
   [§4.4](#44-c4-a-posture-names-a-surface-instead-of-restating-it) is this shape.
2. **A declaration in the receiving pack**, while the content pack writes a form an older yolo
   already reads. The older yolo delivers the content without what the convention adds, and its
   existing lint names the gap.
   [§4.1](#41-c1-what-lands-in-pis-package-folder-is-registered) is this shape.
3. **A pack-wide opt-in line**, which an older yolo ignores and names. This is
   [OQ-PC1](#OQ-PC1)'s option B, and only the folder conventions need it.

Each convention also gets a `yolo features` name in the commit that builds it, by
[PF-D71](patched-forks.md#PF-D71)'s rule, so a host can be asked before a pack relies on it.

> [!NOTE]
> This rule bears on [OQ-M1](manifest-language.md#OQ-M1)'s third lever, per-kind defaults for
> `into`, `path` and `codec`. Built as omissions, those defaults refuse every older host. They need
> either a format change older yolos skip whole, which [OQ-M1](manifest-language.md#OQ-M1)'s
> option (B) could carry with the `exposes` rewrite, or one of the spellings above. Recorded here as a fact for that question; this
> doc does not rule it.

A yolo older than the skip itself (`skips-unreadable-contributions` in `yolo features`, not yet
released) refuses any field it does not know. Nothing a pack writes helps there.

## 3. Where packs repeat themselves

Every repetition found in the shipped packs, the integration fixtures, and the maintainer's own
packs. Counts are in [Appendix A](#appendix-a-counts).

| Repetition | Where | Here |
| :--- | :--- | :--- |
| A patched extension's `files` contribution plus a list entry that is `~/` and its own `into` | The maintainer's five, the patch-series guide, the integration fixture | [C1](#41-c1-what-lands-in-pis-package-folder-is-registered) |
| One `files` entry per file in a folder the user and other packs share | The maintainer's pi extensions, his tok-stats mod, the pi pack's own extensions | Route C (ruled, [OQ-PR1](pack-pi-resources.md#OQ-PR1)), then [C2](#42-c2-a-folder-named-after-an-agent-is-addressed-to-it) |
| Briefing prose addressed to one agent | The maintainer's pi rules, the claude and pi packs' worktree notes | [C2](#42-c2-a-folder-named-after-an-agent-is-addressed-to-it) |
| `{"kind": "loophole", "from": "loopholes/<name>"}` | Every loophole yolo ships | [C3](#43-c3-a-loophole-folder-is-the-loophole) |
| A posture `config` entry restating the `agent`, `name`, `path` and `codec` of a surface | Every shipped posture with a config patch | [C4](#44-c4-a-posture-names-a-surface-instead-of-restating-it) |
| `agent` on every destination and surface of an agent pack | Every shipped agent pack | Ruled ([OQ-D5](slots-and-contributions.md#OQ-D5)); its build waits on [OQ-D6](slots-and-contributions.md#OQ-D6). Not re-asked |
| `codec` matching the extension; `"scope": "workspace"` (already the default); `after` equal to `host:` plus `into` | Every surface; most `state` entries; every briefing destination | [OQ-M1](manifest-language.md#OQ-M1)'s defaults and the `exposes` rewrite. Not here |
| A provider's same-named profile; one `install_hints` value for every manager; a machine `state` and the hook whose `at` names it | Provider packs; `requires` entries; three agent packs | Not proposed ([§7](#7-considered-and-not-proposed)) |

## 4. The conventions

### 4.1 C1: what lands in pi's package folder is registered

**The rule.** A registering slot registers every tree whose landing is a **direct child** of the
slot, whichever spelling put it there: an addressed tree at `<slot>/<pack>`, or a written `into`
such as `<slot>/pi-subagents`. A patched extension is one such tree. If the pack writes any list
entry that loads the tree (`loadsTree`, [PPX-D36](patched-extensions.md#PPX-D36)), that entry
replaces the registration ([PC-D2](#PC-D2)).

[`pack-pi-resources.md`](pack-pi-resources.md#33-what-core-does) registers addressed trees only;
this widens it ([PC-D1](#PC-D1)). The slot is the one that doc already specifies, unchanged:

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
  `pi-subagents` (`patchedext.go:41-73`). So the migration need not wait for this build.
- **For hosts on both sides, write the entry too.** A yolo without C1 then loads the tree from
  the written entry, and a yolo with it registers nothing extra ([P5](#p5)). `yolo pack lint`
  notes the entry as redundant but harmless ([PC-D8](#PC-D8)).

**How core applies it without knowing pi.** The slot names a surface, a JSON Pointer and a
template; core substitutes each direct-child landing and folds the entry exactly as a written
`config-list`, attributed to the contributing pack ([PR-D2](pack-pi-resources.md#decision-ledger)).
The registration is added to the contributing pack's list contributions before any reader runs,
so the owning-agent-pack rule ([PPX-D4](patched-extensions.md#PPX-D4)), what a launch does when a
tree has no build ([PPX-D18](patched-extensions.md#PPX-D18), as
[PPX-D40](patched-extensions.md#PPX-D40) amends it), and the duplicate-load lint
([PPX-D34](patched-extensions.md#PPX-D34)) all see it as a written entry. Today the missing-entry
lint names pi's surface in core's own text (`patchedtrees.go:199`); under C1 it names the
folder the selected packs declare ([PC-D4](#PC-D4)).

**On an older yolo.** The tree is built and mounted, and nothing registers it. The patched-tree
lint fires at `yolo pack lint` and at the launch, naming the entry to add
([PPX-D10](patched-extensions.md#PPX-D10)). Degraded, and named.

**Cost.** The slot means more: every direct child of it is registered, not just addressed trees.
The patch-series guide's examples move `into` from `yolo-patched/` into the folder. A tree name
and a pack name now share one directory, so a pack named `pi-subagents` and another pack's
extension of that name collide. That is the existing exclusive-path collision, reported and
refused at `yolo check`.

### 4.2 C2: a folder named after an agent is addressed to it

Two folders, both keyed on an agent's declared identity, never on a list in core:

- **`<agent>/` at the pack root** is the pack's addressed tree for that agent, when a selected
  pack declares a `files` slot whose `agent` is that name. It expands to
  `{"kind": "files", "agents": ["<agent>"], "from": "<agent>"}`.
- **`briefing/<agent>/`** holds prose addressed to that agent, when a selected pack declares a
  briefing destination with that `agent`. Each `*.md` directly inside expands to
  `{"kind": "briefing", "agents": ["<agent>"], "from": "briefing/<agent>/<file>"}`. Today a
  subdirectory of `briefing/` is not read, and lint names it.

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
`{ "kind": "files", "agents": ["pi"], "from": "files/pi" }`.

**After C2**: no lines. The tree moves, and the pack writes nothing for either folder.
[OQ-PC1](#OQ-PC1) decides whether it writes one `"conventions": 1` instead.

```
matt/pi/extensions/{compact-tools,thinking-preview,…,slash-aliases}.ts
matt/pi/themes/*.json
matt/briefing/pi/rules.md
```

**The rules** ([PC-D9](#PC-D9) to [PC-D11](#PC-D11)):

- **Spelled exactly.** The folder is read only when the pack root holds an entry spelled exactly
  as the identity, as `briefing/` is read ([`pack-system.md`](../reference/pack-system.md#briefing-directory)).
- **Core's own folders win.** An identity spelled like `skills`, `briefing` or `loopholes` gets no
  agent folder.
- **Present agents only.** A folder naming an agent no selected pack declares is just a folder,
  never the fatal unmatched audience that a written `agents` entry is
  ([BA-P3](../reference/agent-briefings.md#ba-p3)). BA-P3 governs what an author writes as an
  audience, and a folder name is not written as one. Lint names a `briefing/` subdirectory that
  no shipped agent claims, as it names a skipped one today.
- **Written wins.** For `<agent>/`, a written contribution whose `from` is the folder or a path
  inside it governs the whole folder, since a tree is one mount and cannot lose one file, and so
  does a written addressed `files` tree for the same agent, since a pack has one per agent. For
  `briefing/<agent>/`, a written `from` governs its one file, as in `briefing/` today.

**Cost.** A file two agents share must sit in each folder or stay written: the tok-stats core
that the maintainer's Claude mod and pi extension share is the case (`/ctx/packs/matt-mods/pack.json:10-34`).
And whichever option [OQ-PC1](#OQ-PC1) takes, a folder is one more place a reader has to look.

### 4.3 C3: a loophole folder is the loophole

**The rule.** Each directory directly under `loopholes/` holding a `manifest.jsonc` expands to
`{"kind": "loophole", "from": "loopholes/<dir>"}`. The module loader already forces the module's
name to equal its directory's. [OQ-PC2](#OQ-PC2) decides whether this convention exists; if it
does, it takes [OQ-PC1](#OQ-PC1)'s terms for a folder (no line, or the opt-in), and if [OQ-PC1](#OQ-PC1)
rules out folder conventions it falls with C2.

**Before** (`packs/journal/pack.json`, and the same line in every pack that ships a loophole):

```jsonc
{ "contributes": [ { "from": "loopholes/journal", "kind": "loophole" } ], "name": "journal" }
```

**After**: `loopholes/journal/manifest.jsonc` and no contribution. A pack that ships only a
loophole needs no `pack.json` beyond its opt-in, if [OQ-PC1](#OQ-PC1) asks for one.

**What it changes, and what it does not.** What a loophole runs on the host lives in the module's
`manifest.jsonc` either way (`kinds.go:225-233`), and its claims are read from there either way
(`moduleClaims`). So the launch banner and `yolo pack footprint` say the same thing, and the
footprint marks the contribution implied. The line in `pack.json` says only where the module is.
A directory under `loopholes/` with no `manifest.jsonc` implies nothing, and lint names it.

### 4.4 C4: a posture names a surface instead of restating it

**The rule.** A posture takes `managed`, a map from a surface address (`<agent>/<name>`, the
spelling `config-overlay` and `config-list` use) to the keys to set. Both spellings decode to the
same value: a surface key and its keys.

**Before** (`packs/pi/pack.json:149-177`):

```jsonc
{ "kind": "autonomy",
  "autonomous": { "config": [ { "agent": "pi", "codec": "json", "name": "settings",
      "path": "~/.pi/agent/settings.json", "managed": { "defaultProjectTrust": "always" } } ] },
  "guarded": { "config": [ { "agent": "pi", "codec": "json", "name": "settings",
      "path": "~/.pi/agent/settings.json", "managed": { "defaultProjectTrust": "ask" } } ] } }
```

**After**:

```jsonc
{ "kind": "autonomy",
  "autonomous": { "managed": { "pi/settings": { "defaultProjectTrust": "always" } } },
  "guarded":    { "managed": { "pi/settings": { "defaultProjectTrust": "ask" } } } }
```

**Why the long form's extra fields are pure restatement.** The schema requires `path` and
`codec` on every posture entry (`load.go:74-79`). On the pack's own surface the fold matches on
the agent and name alone and never reads them (`foldPostureManaged`, `packload.go:312-319`). On
another pack's surface they must equal the owner's, or the entry is refused
([NS-D21](notch-scoped-config-contributions.md#NS-D21), `packoverlay.go:394-405`). The
short form has nothing there to restate, so that refusal cannot arise.

**How core applies it.** A key naming a surface the pack declares folds into that surface's
managed layer, as today. Any other key is a posture overlay on its owner's surface, in the
owner's file and codec, and an ownerless one is the existing orphan report. A posture may carry
both spellings, but naming one surface in both is refused ([PC-D5](#PC-D5)). When
[OQ-D5](slots-and-contributions.md#OQ-D5) is built, a bare `name` may name the pack's own surface
([SC-D3](slots-and-contributions.md#SC-D3)). The address keeps working.

**On an older yolo.** `autonomy` is a restricting kind, so it is kept without `managed`
([PF-D69](patched-forks.md#PF-D69)) and the line says so: that posture's keys are lost. For a
`guarded` posture that is a host agent without yolo's guarded keys, so a pack an older host reads
keeps its guarded keys in the written form ([§9](#9-risks)).

## 5. How core applies a convention

### 5.1 Two expansion points, both already there

| Point | What it already does | What it gains |
| :--- | :--- | :--- |
| **The pack load** (`packload.LoadDir` and its use-read twin), per pack | Runs the manifest decode (`Decode`, `DecodeForUse`, `DecodeTolerant`) and reads the pack's own `briefing/` | C4, in the decode: the `managed` map becomes the posture's surface patches. C3, in the load, which has the pack's root where `packdecl` has none: `loopholes/*/` directories become contributions |
| **Destination resolution** (`packload.ResolveDestinations`), per selection | Turns an addressed tree into one at `<slot>/<pack>` | C2: agent-named folders become addressed contributions, keyed on the identities the selected packs declare. C1: each registering slot adds an entry for each direct-child landing |

Two traps carry over from the code:

> [!WARNING]
> **Conventional sources keep ONE reader, `packload.GovernedSources`**
> ([`pack-system.md`](../reference/pack-system.md#one-governance-reader), R5). C2 and C3 join it.
> A second reader that re-lists a folder will disagree with the first, which is how one narrow
> declaration once switched off a pack's whole broadcast at every notch.

> [!WARNING]
> **Governance reads the pack's original declaration, never the resolved copy**
> (`Pack.origDecl`). An implied contribution read back as written would govern its own source and
> switch the convention off for the next reader. Every other reader takes the expanded set.

The expansion is a pure function of the pack's tree and the selected packs' declarations. It
runs once per read, holds no state between reads, and has no ordering question beyond pack order,
which every fold already uses.

### 5.2 What the reports show

| Reader | What it shows for an implied contribution |
| :--- | :--- |
| `yolo pack lint <dir>` | Validates the expanded set strictly, then prints one line per convention it applied. It runs with no config, so for C1 and C2 it reads the slots and identities of the packs yolo ships plus the pack itself, and says so ([PC-D10](#PC-D10)) |
| `yolo pack footprint` | One claim line per implied contribution, as for a written one, marked with its rule and the pack that declared it. `--format json` carries the same as `implied_by` ([PC-D7](#PC-D7)) |
| `yolo check`, the launch, `yolo host apply` | Resolve against the selected set. A tier-4 disclosure of an implied claim, such as a patched extension's build line or a loophole's daemon, is printed exactly as the written one's |

*Shape, not wording*, for a pack that has moved two patched extensions into the folder:

```
files        .pi/agent/yolo-packs/pi-subagents   patched extension of git+https://…/pi-subagents?ref=main  (review)
config-list  pi/settings /packages + "~/.pi/agent/yolo-packs/pi-subagents"
             implied: pi's package folder registers what lands in it (pack pi)
files        .pi/agent/yolo-packs/pi-archimedes  patched extension of git+https://…/pi-archimedes?ref=main  (review)
config-list  pi/settings /packages + "~/.pi/agent/yolo-packs/pi-archimedes/packages/session-name"
```

### 5.3 Written wins

[P5](#p5) in each convention:

- **C1.** A written entry that loads the tree replaces its registration.
- **C2.** A written `from` naming an `<agent>/` folder, or a path inside it, governs the folder,
  and so does a written addressed tree for the same agent; in `briefing/<agent>/` it governs its
  one file.
- **C3.** A written loophole contribution naming the directory is the same contribution, and is
  not doubled.
- **C4.** The two spellings may share a posture, but not a surface.

A written contribution that equals what a convention would imply gets a lint `note:` saying it can
go once every host reads the convention. It is never a warning, and the exit status is unchanged
([PC-D8](#PC-D8)).

## 6. Behavior in every case

| Case | Behavior |
| :--- | :--- |
| No pack uses a convention | Every rendered file is byte-identical to today's |
| C1: a tree lands directly in a registering slot, and no written entry loads it | One registration at every notch, attributed to the contributing pack. The patched-tree lint is satisfied |
| C1: a written entry loads it (any list, any posture) | That entry alone. No registration |
| C1: a tree lands deeper than a direct child of the slot | Not registered. `yolo pack lint` and `yolo check` warn, naming the direct-child form. Never refused ([PC-D3](#PC-D3)) |
| C1: the tree has no build at this notch | The registration is added as a written entry would be, and the no-build rule ([PPX-D18](patched-extensions.md#PPX-D18), [PPX-D40](patched-extensions.md#PPX-D40)) decides what happens, unchanged |
| C1: pi is not selected | No registering slot, so nothing is registered, and the patched-tree lint fires as it does today |
| C1: two packs land one leaf | The existing exclusive-path collision, named and refused at `yolo check` |
| C1: the pack is dropped | The registration goes with it, at both notches: `config-list`'s rule ([`pack-pi-resources.md`](pack-pi-resources.md#34-behavior-in-every-case)) |
| C2: an agent folder, and a selected pack declares that agent's slot or briefing destination | Delivered as the written addressed contribution would be |
| C2: no selected pack declares the agent | Nothing. Lint says which shipped pack would receive it, or that none would |
| C2: an empty folder, or one spelled in another case | Nothing |
| C2: a written contribution governs the folder | The written one alone. Lint names the folder it did not deliver |
| C3: `loopholes/<dir>/manifest.jsonc` | One loophole contribution, review-worthy as every loophole is |
| C3: a directory there with no `manifest.jsonc` | Nothing. Lint names it |
| C4: a key naming the pack's own surface | Folded into its managed layer |
| C4: a key naming another pack's surface | A posture overlay; ownerless, the existing orphan report |
| C4: one surface in both spellings in one posture | Refused, naming both |
| A yolo older than a convention | That part is left out and named: C1 by the patched-tree lint, C4 by the kept-without-field line, C2 and C3 by the ignored opt-in line if [OQ-PC1](#OQ-PC1) is B. Under its A nothing names them |
| macos-user and Apple Container | Whatever the written form does there. C1 and C2 ride the addressed-tree delivery, **UNVERIFIED** per backend as in [`pack-pi-resources.md`](pack-pi-resources.md#34-behavior-in-every-case) |

**Forbidden.** Core never names an agent in a convention, nor in its messages. An implied
contribution is never left out of a report a written one appears in. A convention never fills in
a field an author left out, and never makes a pack an older yolo refuses.

## 7. Considered and not proposed

| Shape | Verdict |
| :--- | :--- |
| A pack-wide `defaults` block for any field | **Rejected.** An older yolo ignores it, so a default for an optional field silently changes the contribution (a patched extension built without its `build` line, a different tree under a different recipe), and a default for a required field is refused |
| Omission defaults: `codec` from the extension, `patches` from `into` | **Rejected here**; per-kind defaults are [OQ-M1](manifest-language.md#OQ-M1)'s, and [§2](#2-what-an-older-yolo-does-with-a-convention) records what they cost |
| A patched extension addressed with `agents` and no `into` | **Rejected.** An older yolo refuses `agents` beside `source`, and the launch fails |
| A new kind bundling a tree and its list entry | **Rejected.** A second name for one destination, whose combine rule would have to track `files` and `config-list` by hand |
| `each`: one declaration fanned out to one claim per file of a folder | **Not proposed.** Route C covers the only folder with several writers in the corpus, pi's extensions. Revisit when a second agent's shared folder appears |
| A provider implying its same-named profile | **Rejected.** It saves two lines per provider pack, all shipped, and a selector a user types belongs in the file that defines it; `openai-codex`'s profile is named `codex` in the packs that use it |
| One `install_hints` value for every manager | **Rejected.** The value is per manager by nature, a shipped hint must cite a source per manager (`TestEveryShippedAndExampleInstallHintNamesItsSource`), and the skip-safe spelling would be a second field for one map |
| A shared-dir hook implying its machine `state` | **Not proposed.** Three pairs, all in shipped packs. Found on the way: `Decode` checks only that `at` is present (`contributes.go:3844-3849`), and the match against the pack's machine `state` is made at boot (`packhooks.go:269-274`), whose failure fails the boot step. I found no host-side check |
| A manifest written in Lua or Starlark | [`manifest-language.md`](manifest-language.md)'s [OQ-M2](manifest-language.md#OQ-M2) and [OQ-M3](manifest-language.md#OQ-M3) |

## 8. Non-goals

- **Grouping by kind, the syntax, and per-kind defaults**: [`manifest-language.md`](manifest-language.md).
- **Naming the agent once per pack**: ruled ([OQ-D5](slots-and-contributions.md#OQ-D5)), and its
  build waits on [OQ-D6](slots-and-contributions.md#OQ-D6).
- **A registration that loads part of a tree, or reaches one posture.** The written entry does
  that, and replaces the registration ([PC-D2](#PC-D2)).
- **Moving the pi pack's own `yolo-*.js` extensions** into its package folder. Possible under
  C1, and a change to pi's own pack that [`pack-pi-resources.md`](pack-pi-resources.md#34-behavior-in-every-case)
  leaves unchanged.

## 9. Risks

| Risk | Mitigation |
| :--- | :--- |
| A host on an older yolo reads a pack that relies on C1, and pi does not load the tree | The patched-tree lint names it at that launch. Write the entry too until every host reads C1 ([§4.1](#41-c1-what-lands-in-pis-package-folder-is-registered)) |
| A `guarded` posture in the short form, read by an older host, loses its keys | The kept-without-field line names it. A pack an older host reads keeps guarded keys written out |
| A folder that happens to carry an agent's name is delivered to that agent | [OQ-PC1](#OQ-PC1): under B only a pack that opted in delivers one, under C none does; under A the footprint and the launch name it |
| A loophole module copied into a pack runs on the host at the next launch | [OQ-PC2](#OQ-PC2). The launch banner names every loophole's daemon, as for a written one |
| A second reader of a conventional folder drifts from `GovernedSources` | [§5.1](#51-two-expansion-points-both-already-there)'s warning. One reader |
| An implied contribution is read back as written and governs itself | Governance reads `origDecl` |

## 10. What done looks like

- The maintainer's patched extensions pack, with three trees in pi's package folder and no list
  entry for them, gives a jail's pi all three, and `settings.json`'s `packages` holds one
  `~/.pi/agent/yolo-packs/<name>` entry for each.
- `pi-archimedes` and `pi-automode` in the same folder with their written entries load as those
  entries say, with no root entry.
- `yolo pack footprint` on that pack lists each registration, marked implied, with the slot's pack.
- The patched-tree lint's message names a folder taken from a pack's declaration, and no string
  `pi/settings` remains in core's text for it.
- Every shipped posture in the short form renders byte-identical settings files at both notches.
- Each convention appears in `yolo features`, and a yolo older than it, reading a pack that uses
  it, launches and names what it left out.

## 11. What I would build, in order

1. **C1, inside [`pack-pi-resources.md`](pack-pi-resources.md)'s build**: emission over every
   direct-child landing, the written-entry replacement, the generic lint message, its feature
   name. Building the slot without it would ship the narrower rule only to widen it later.
2. **C4**: the `managed` map, then the shipped postures moved to it, in one commit with the test
   that their renders are byte-identical.
3. **After [OQ-PC1](#OQ-PC1)**: the agent folders, joined to `GovernedSources`, and the opt-in if B.
4. **After [OQ-PC2](#OQ-PC2)**: loophole folders, and the loophole-only shipped packs emptied.
5. **The maintainer**: his packs, keeping written entries until every host he uses reads C1.

## 12. Open questions

1. 💬 <a id="OQ-PC1"></a>**OQ-PC1: Does a folder named after an agent deliver to it with no line
   in `pack.json`?**

   Decides whether pi files in `pi/` and pi-only prose in `briefing/pi/` take zero lines or one,
   and what an older yolo says ([§4.2](#42-c2-a-folder-named-after-an-agent-is-addressed-to-it)).

   - **A — Yes, in every pack.** Nothing to write. An older yolo delivers nothing and says
     nothing, and a pack already holding such a folder starts delivering it on upgrade.
   - **B — Yes, once a pack opts in with `"conventions": 1`.** One line per pack. An older yolo
     names the line it ignored, and a later folder convention needs `2`.
   - **C — No.** One written line per folder, route C's. An older yolo names the orphan.

   <!-- vantage: question id=OQ-PC1 leaning="B: one line buys zero lines per folder for every folder convention, and it is the only option under which an older yolo says what it left out and an upgrade never changes what an existing pack delivers." -->

   _Leaning:_ **B**: one line buys zero lines per folder for every folder convention, and it is the
   only option under which an older yolo says what it left out and an upgrade never changes what an
   existing pack delivers.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 <a id="OQ-PC2"></a>**OQ-PC2: May a `loopholes/<name>/` folder alone give a pack that
   loophole?**

   A loophole runs a daemon on the host. Every loophole yolo ships is one line naming its own
   folder ([§4.3](#43-c3-a-loophole-folder-is-the-loophole)).

   - **A — Yes**, on [OQ-PC1](#OQ-PC1)'s terms for a folder. The launch and
     `yolo pack footprint` disclose it as today, marked implied.
   - **B — No.** A loophole is always written in `pack.json`.

   <!-- vantage: question id=OQ-PC2 leaning="A: the line in pack.json says only where the module is; what runs on the host is in the module's own manifest.jsonc either way, and the disclosure is the same." -->

   _Leaning:_ **A**: the line in `pack.json` says only where the module is; what runs on the host
   is in the module's own `manifest.jsonc` either way, and the disclosure is the same.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

Implementation decisions made in drafting, each reversible and each the maintainer's to overrule.

| ID | Decision | Why it holds | Built |
| :--- | :--- | :--- | :--- |
| <a id="PC-D1"></a>PC-D1 | A registering slot registers every tree whose landing is a direct child of the slot, whichever spelling put it there: an addressed tree or a written `into`, a patched extension included. Widens [PR-D2](pack-pi-resources.md#decision-ledger) from addressed trees | The one rule that ends the extension-and-entry pair without a field or combination an older yolo refuses ([§2](#2-what-an-older-yolo-does-with-a-convention)) | — |
| <a id="PC-D2"></a>PC-D2 | A written list entry that loads a tree (`loadsTree`, [PPX-D36](patched-extensions.md#PPX-D36)), in any list or posture, replaces that tree's registration | [P5](#p5); lets a monorepo tree and a guarded-only tree live in the folder | — |
| <a id="PC-D3"></a>PC-D3 | A tree deeper than a direct child of a registering slot is not registered; lint and `yolo check` warn, naming the direct-child form; never refused | A deeper tree can sit inside another pack's landing, and a new refusal on a use read refuses a launch | — |
| <a id="PC-D4"></a>PC-D4 | The patched-tree lint names the registering folder the selected packs declare (the shipped packs, at `yolo pack lint`), not pi's surface | [P2](#p2): `patchedtrees.go:199` names pi in core's text today | — |
| <a id="PC-D5"></a>PC-D5 | C4's spelling: `managed` in a posture, a map from surface address to keys, decoding to the value a written entry yields; one surface in both spellings in one posture is refused | The address is `config-overlay`'s; [NS-D21](notch-scoped-config-contributions.md#NS-D21)'s mismatch has nothing to compare | — |
| <a id="PC-D6"></a>PC-D6 | Each convention gets a `yolo features` name in the commit that builds it; names provisional | [PF-D71](patched-forks.md#PF-D71)'s rule: a yolo without it would skip that use | — |
| <a id="PC-D7"></a>PC-D7 | An implied contribution carries its rule and declaring pack, never serialized into a manifest; footprint, lint, `yolo check`, the launch and `yolo host apply` name it; JSON outputs carry `implied_by` | [P4](#p4) | — |
| <a id="PC-D8"></a>PC-D8 | Lint prints a `note:` for a written contribution equal to what a convention implies, saying it can go once every host reads the convention; never a warning, exit status unchanged | The written form is the one an older host needs, so lint must not push authors off it | — |
| <a id="PC-D9"></a>PC-D9 | An agent folder is read only when the pack root holds an entry spelled exactly as the identity, and an identity spelled like a core folder (`skills`, `briefing`, `loopholes`) gets none | The `briefing/` rule ([`pack-system.md`](../reference/pack-system.md#briefing-directory)); core's own folders keep one meaning | — |
| <a id="PC-D10"></a>PC-D10 | Single-pack views resolve agent-declared conventions against the packs yolo ships plus the pack itself, and say so; `yolo check` and the launch resolve against the selected set | Lint takes no config; the shipped set is the one other source a lint already reads (`project_dirs`' source set) | — |
| <a id="PC-D11"></a>PC-D11 | A written `from` naming an `<agent>/` folder or a path inside it governs the whole folder, and a written addressed tree for the same agent does too; in `briefing/<agent>/` a written `from` governs its one file; agent folders deliver only to agents the selected packs declare | A tree is one mount; one addressed tree per agent per pack; briefing governance is per file; [BA-P3](../reference/agent-briefings.md#ba-p3) governs written audiences, not folder names | — |

## Appendix A: counts

Measured 2026-10-05 at `c321336a8` over `packs/*/pack.json` (comments stripped), and over the
maintainer's packs as staged on 2026-10-04 at `/ctx/packs/`.

| What | Count |
| :--- | ---: |
| Shipped packs / contributions | 24 / 103 |
| Loophole contributions, every one `from: "loopholes/<name>"` | 10 |
| Posture config entries, every one restating its own pack's surface's `path` and `codec` | 10 |
| Config surfaces / of them whose `codec` matches the file extension | 21 / 21 |
| Briefing destinations whose `after` is `host:` plus `into` | 7 |
| `state` entries / of them writing `"scope": "workspace"`, the default | 9 / 6 |
| Profiles / of them named after the provider they select | 11 / 6 |
| `"agent":` keys in shipped packs (surfaces, postures, destinations) | 45 |
| Shipped `files` entries whose `into` ends in the `from` file's name | 5 |
| Maintainer's `matt`: `files` entries naming one pi path each, plus one addressed briefing | 10 + 1 |
| Maintainer's `matt-mods`: `files` entries naming one pi path each | 5 |
| Maintainer's patched extensions: each a `files` and a matching list entry | 5 pairs |

## Appendix B: evidence

- **The skip and the refusal**: `internal/packdecl/skew.go:24-87` (`DecodeForUse`; lines 38-40,
  a missing required field stays a problem), `:130-147` (`restrictingKind`), `:149-168`;
  `internal/packdecl/packdecl.go:576-710` (`DecodeTolerant`), `:485-498` (strict `Decode`).
- **A patched extension refuses `agents`**: `internal/packdecl/patchedext.go:110-114`; its name is
  `into`'s last segment, `:41-57`; the recipe reads no `into`, `:63-73`.
- **The pair**: `internal/packload/patchedtrees.go:106-110` (`TreeListEntry`), `:112-146`
  (`owningAgentPack`), `:180-204` (`LintPatchedTrees`, naming pi's surface at `:199`);
  `integration/patchedextension_test.go:84-90`; the patch-series guide's example,
  [`patch-series.md`](../../userguide/guides/patch-series.md#patch-a-pi-extension).
- **The landing**: `internal/packload/mergedest.go:567-572` (`SlotLanding`), `:293`
  (`ResolveDestinations`); `internal/packload/packload.go:105-111` (`origDecl`).
- **Posture restatement**: `internal/agentcfg/manifest/load.go:74-79` (path and codec required);
  `internal/packload/packload.go:312-319` (the fold matches agent and name);
  `internal/packoverlay/packoverlay.go:394-405` (NS-D21's mismatch refusal);
  `internal/packdecl/contributes.go:1370-1398` (`AutonomyPosture.Config`).
- **Existing conventions**: `internal/packdecl/contributes.go:1921-1924` (`skills/`), `:1977-1986`
  (`briefing/`); `internal/packload/deriveenv.go:43` (`derive.lua`); `internal/packdecl/kinds.go:66-75`
  (a wrapped plugin recognized from the tree); `internal/packload/governance.go:92`
  (`GovernedSources`).
- **The loophole module**: `internal/packdecl/kinds.go:216-241`.
- **The feature names**: `internal/packdecl/features.go:21-36`.
- **The hook pair, checked at boot only**: `internal/packdecl/contributes.go:3844-3849`;
  `internal/entrypoint/packhooks.go:261-274`.
- **The maintainer's packs**: `/ctx/packs/matt/pack.json:8-62` and
  `/ctx/packs/matt-mods/pack.json:10-34` (staged copies, outside the repository); his patch-series
  requests and the 2026-10-05 pack advice are kept outside the repository too
  (`scratch/yolo-patch-series-requests.md`, `.yolo/durable/pack-advice/2026-10-05-first-patched-launch.md`).
