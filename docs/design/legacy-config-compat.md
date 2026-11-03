---
title: "Legacy configs keep working: every config that launched last release launches unchanged, and means the same"
date: 2026-11-03
status: in-review
stage: DESIGN
next: "Build in §9 order; no ruling is owed"
depends-on:
  - readable-config.md
  - config-migration.md
tags: [design, config, migration, toml, legacy, packs, loopholes, grants, inherit, happy-path]
summary: "The owner, 2026-11-03: 'why do I need to change my configs? that's not the point of this. the configs keep working.' A census of v0.15.1 against HEAD found 56 configs that launched then and now need an edit, a command or a migration, or launch meaning something else, plus 44 that launch but end in a dead-end message. They fall into six root causes, five of them build decisions no owner ruled. This document states the invariant, decides each class, carries the inherited-scope crossing decisions that belong in readable-config.md, names the corpus test that enforces the invariant through a shrinking allowlist, and files the one real conflict: three older owner rulings retire a legacy spelling outright, and each has a lossless mapping."
vantage:
  status-chip: true
---

# Legacy configs keep working: every config that launched last release launches unchanged, and means the same

**Status:** 2026-11-03; design. Nothing here is built. Evidence: a read-only census of every
legacy spelling v0.15.1 (`b6b057a06`) accepted, run against HEAD (`2fc63bdde`) with both binaries
built from the tree and `yolo check --no-build` in v0.15.1 as the oracle for "this launched";
each row's site is a `file:function` in the tree at that commit. The fragments on the
host-processes columns and the macOS second workspace were written the same day and applied to
[`loophole-grants.md`](../reference/loophole-grants.md#6-migration) and
[`macos-user-model-plan.md`](macos-user-model-plan.md#implementation-decisions).

> **In short.** A legacy config file is not an old spelling to be refused, warned about, or
> moved: it is an input yolo already knows how to read, and the new model is yolo's to map onto
> it. The only acceptable result for a config that launched in the previous release is that it
> launches and behaves as it did; everything else in this document is that one rule applied to
> a place where the tree does something else.

**Why it matters.** The owner, 2026-11-03, on being handed a `yolo allow` line for a workspace
block that worked a week earlier: *"why do I need to change my configs? that's not the point of
this. the configs keep working."* The census found 47 configs that are refused or need a command,
9 that launch meaning something else, and 44 that launch into a message with no next step. Five
of the six root causes are build decisions no owner ruled; the sixth is three owner rulings
that retire a spelling with a transition of their own, which stand ([OQ-LC1](#OQ-LC1)).

**The shape.** One invariant ([§1](#1-the-one-rule)); the **legacy projection** and the
**crossing**, the two read-time maps every legacy value goes through ([§2](#2-vocabulary)); six
classes of break, each with a decision ([§4](#4-the-classes-and-what-each-becomes)); the
**corpus test** that enforces the invariant through an allowlist that must empty
([§5](#5-the-enforcer-a-corpus-that-must-launch)).

**Cost.** The crossing's refusals and the migration's declines are deleted, not softened; the
TOML vocabulary gains the spellings the census found missing; the grant model's legacy
projection grants what v0.15.1 granted, which is more than it grants today. No hatch anywhere.

**Start at [§4](#4-the-classes-and-what-each-becomes).** [§1](#1-the-one-rule) is the rule it
applies; [Appendix A](#appendix-a-every-census-row-and-its-disposition) is every census row.

**Needs your ruling:** none; [OQ-LC1](#OQ-LC1), [OQ-LC2](#OQ-LC2) and [OQ-LC3](#OQ-LC3) are ruled.

**Reads with:** [`readable-config.md`](readable-config.md) (the TOML redesign; its ledger owns
the crossing rows [§4.2](#42-class-b-the-crossing-refuses-what-the-host-accepted) carries),
[`config-migration.md`](config-migration.md) (what `yolo config migrate` promises),
[`loophole-grants.md`](../reference/loophole-grants.md) (the grant model and its legacy
projection), [`happy-path-principle.md`](../reference/happy-path-principle.md) (every stop names
a next step). No companion plan is seeded: the build is per class, and each class's mechanism is
named below.

---

## 1. The one rule

**P1. Every config that launched in the previous release launches in this one, unchanged, and
means the same.** The owner's words of 2026-11-03, in full:

> why do I need to change my configs? that's not the point of this. the configs keep working.

**Its scope is the TOML switch.** The owner, ruling [OQ-LC1](#OQ-LC1) the same day: *I want specifically the Toml switch not to break things. The Chrome DevTools and the OpenAI rename had nothing to do with it. Those were already ruled on, transition paths ruled on there. For specifically the Toml, if one moment it worked with JSONC and then you convert, then it has to work with Toml. That's the only rule I'm implying here.* So P1 is about what the TOML transition (the readable-config
redesign, the release that brought TOML and the grant model with it) did to a config that
launched as JSONC: that config keeps launching, and its conversion launches meaning the same. A
spelling another ruling retired with its own transition path (`mcp_presets`, the `openai-auth`
pack name, the user pack `requires` kind; [§4.4](#44-class-d-a-retired-spelling-is-refused-outright))
is that ruling's, and outside P1.

Three corollaries, each a rule the body applies:

- **P2. A changed model is yolo's to map onto the old spelling, never the user's to move to.**
  No edit, no command, no `yolo allow`, no `yolo pack add`, no `yolo config migrate` is required
  of a config that used to launch. A message that says *the user runs X* or *move this key* for
  such a config is wrong, however good the message.
- **P3. Migration is optional and exact.** `yolo config migrate` is offered, never required; when
  run, it converts every file with nothing left over, and what the converted files mean is what
  the old ones meant. A legacy spelling with no TOML spelling is a TOML gap to close, never a
  refusal (the owner's piecemeal-migration ruling, [RC-D50](readable-config.md#RC-D50), 2026-11-02).
- **P4. A message is not a fix.** The one acceptable extra line for an unchanged config is the
  optional migration notice. The one host step that may remain is a y at the config-change
  gate, and only for a file the record has not approved yet.

**Where the rule stops.** P1 holds for every release while both readers exist
([RC-D19](readable-config.md#RC-D19) keeps the legacy reader *"for a few releases"*); the release
that removes the legacy reader is the owner's to time and is outside this document. A security
refusal ruled for its own reason (the two of 2026-10-29) is not a TOML-switch change, so it is
outside P1 and stands ([OQ-LC3](#OQ-LC3)).

## 2. Vocabulary

- **Legacy config** *(coined here)*: a JSONC file yolo read in v0.15.1: the user config and its
  includes, a `--user-layer`, the workspace's `yolo-jail.jsonc` and `.local`, the per-workspace
  file `~/.config/yolo-jail/workspaces/<slug>.jsonc` that `yolo loopholes enable` wrote, and a
  user pack's `pack.json` or `manifest.jsonc`. Not a TOML file, and not a JSONC file written by a
  release after v0.15.1 (those hold spellings v0.15.1 never read; the census counts five of them
  as breaks anyway, because P3 says every legacy file converts).
- **Legacy projection**: [`loophole-grants.md` §6](../reference/loophole-grants.md#6-migration)'s
  term for the read-time function that turns a legacy `loopholes.<name>` block into the grant
  model's ceiling, request and record lines. Used here for every such read-time map, not only
  the grant one: the port lowering, the null lowering and the env-sources lowering are
  projections in the same sense.
- **The crossing** *(coined in the 2026-11-03 crossing fragment, carried here)*: the launch
  rendering the host's merged user-and-workspace config into the user-scope file the jail reads
  at `~/.config/yolo-jail/config.toml` or `config.jsonc`. Not the per-workspace file, which the
  crossing excludes by design ([BB-D56](boundary-broker.md#BB-D56)).
- **Break** and **same-meaning break** *(coined by the census)*: a legacy config that now needs
  any user action to launch as before; and one that launches but behaves differently. A config
  that launches into a message with no next step is a **happy-path-only** defect, not a break.

## 3. What the census found

| Verdict | Count | Of which |
| :--- | :--- | :--- |
| Break | 47 | 5 at launch or `yolo check`, 12 at the crossing, 30 in `yolo config migrate` |
| Same-meaning break | 9 | 8 at launch, 1 at the crossing |
| Of those, retired by an owner ruling with its own transition (they stand) | 3 | `mcp_presets`, the `openai-auth` pack name, the `requires` kind ([OQ-LC1](#OQ-LC1)) |
| Ruled, and lets a config stop | 2 | a provider named `native` ([OQ-RC20](readable-config.md#decision-ledger)); `yolo allow` on a JSONC file holding `brokered` ([RC-D37](readable-config.md#RC-D37)) |
| A trust boundary the owner named | 3 | a workspace env file outside the workspace, a mount in or inside the workspace or another mount, broker ceilings never crossing; not TOML-switch changes, so they stand ([OQ-LC3](#OQ-LC3)) |
| Happy-path-only | 44 | 41 advice strings naming a legacy file or a JSON snippet, 3 migration texts |
| Unverified | 5 | listed in [Appendix A](#appendix-a-every-census-row-and-its-disposition); the corpus test settles them |

Every break has one of six root causes, and that is what makes the problem decidable in one
sitting: the rows are many, the decisions are six.

| Class | Root cause | Who decided it | Rows |
| :--- | :--- | :--- | :--- |
| [A](#41-class-a-the-grant-projection-grants-less-than-the-switch-did) | The grant model's legacy projection turned a v0.15.1 switch into a *request*, and moved a workspace setting to user scope | LG-D1 is the owner's; LG-D2 and LG-D5 are build decisions | L1, L2, L6 to L10, L17; M5 to M8, M12, M13; X7 to X9 |
| [B](#42-class-b-the-crossing-refuses-what-the-host-accepted) | The crossing applies the migration's declines to the merged scope, judges workspace values by user-scope rules, and reads TOML pack handles as legacy paths | RC-D55, a design-seat decision, against RC-D50, the owner's | X1 to X6, X10 to X14 |
| [C](#43-class-c-the-migration-declines-a-config-that-launches) | `yolo config migrate` declines where the TOML vocabulary has no spelling, and the launch offer then stops the launch | RC-D43, RC-D32, RC-D55: lane and design-seat decisions | M1 to M4, M9 to M11, M15, M16, M19 to M30, M32 |
| [D](#44-class-d-a-retired-spelling-is-refused-outright) | A legacy spelling is refused naming its replacement, with no read of what it meant | [OQ-MP6](mcp-presets-removal.md#decision-ledger), [OQ-PE3](provider-enablement.md#OQ-PE3), [OQ-PS11](provisioner-sets.md#OQ-PS11) are the owner's; LG-D3 and LG-D5 are build decisions | L3, L4, L11 (owner's); L9, L10 (build) |
| [E](#45-class-e-a-dangling-link-reads-as-no-config) | A dangling symlink where a config file may be is read as "no file" | Nobody; predates v0.15.1 | L12, L13 |
| [F](#46-class-f-the-message-names-a-file-to-edit) | A message names `config.jsonc` or `yolo-jail.jsonc` by hand, prints a JSON snippet, names a wrong verb, or makes a false claim | Nobody; RC-D40 already rules the fix | the 44 happy-path-only rows |

## 4. The classes, and what each becomes

Each decision below is an **LC-D** row in the [ledger](#decision-ledger). Where a class rests on
a mechanism that settles it, the decision is made here; where it rests on a product or trust
choice, it is an open question.

### 4.1 Class A: the grant projection grants less than the switch did

**What v0.15.1 meant.** A workspace `loopholes.<name>.enabled: true` switched the loophole on
for that workspace; the config-change gate showed the file and the human's y approved it. A user
`enabled: true` switched it on for every workspace, with no gate. A workspace
`settings.visible` or `settings.fields` narrowed what that project saw; a user `enabled: false`
on a service that was on by default (`claude-oauth-broker`, `aws-auth`, now `cgroup-delegate`)
turned it off.

**What the tree does.** The legacy projection maps a workspace switch to a *request* and
nothing admits it ([`loophole-grants.md` §6](../reference/loophole-grants.md#6-migration)), so
the launch prints *"this project runs without it … `yolo allow host-processes on_request view`"*;
a user switch on a valued kind is `on_request` with nothing requesting, so `yolo loopholes list`
shows `pending`; `fields` and `default_baud` are refused in a workspace file; a user `enabled:
false` is warned as *"switches nothing"* and the service runs.

**The decision.** <a id="LC-D2"></a>**LC-D2: the legacy projection grants what v0.15.1 granted.**
Each legacy line maps to the standing it had, not to the half of the new model that happens to
share its words:

- A **workspace `enabled: true`** is the request *and its admission*: the kind joins this
  launch's ceiling as `on_request`, the gate shows the names line, the y grants, and a file the
  record already approved carries its grant and prompts nothing. This is
  [LG-D16](../reference/loophole-grants.md#LG-D16), applied there; a legacy workspace `fields` is
  the project's columns request ([LG-D15](../reference/loophole-grants.md#LG-D15)).
- A **user `enabled: true`** on an unvalued kind is `always`, as the projection already reads
  it. On a **valued kind** (`host-processes` with `visible`, `serial` with `allowed_devices`) it
  is a request *from the user's config* to every project, admitted by the same ceiling line,
  shown once at each project's gate the way an `always` line is
  ([LG-D12](../reference/loophole-grants.md#LG-D12)), and granted by that y. The value is the
  one the old settings gave; for `serial` with none written, the five globs the manifest keeps as
  `legacy_default_values`. One y per project is the owner's own rule for a user-wide grant and
  is not an edit or a command (P4).
- A **user `settings.<key>`** that the manifest declares user scope stays a user setting
  (`allow.<pack>.<key>`), as today; `default_baud` in a *workspace* file is read as the user's
  value for that launch and disclosed, since a baud rate admits nothing.
- A **workspace or user `enabled: true` on a loophole whose pack is not selected** selects
  nothing and grants nothing, and the launch says so in one line naming `yolo pack add <pack>`
  as an offer, as v0.15.1 warned and launched. Not a refusal.

<a id="LC-D3"></a>**LC-D3: `enabled: false` keeps meaning off where off meant something.** For a
grant-declaring loophole, "off" and "on with no grant" give a project the same access, so the
projection's *dropped* is the same meaning and stands. For a loophole whose approval is a login
or a setting (`claude-oauth-broker`, `openai-auth-broker`, `aws-auth`) and for `cgroup-delegate`,
off meant the jail did not get the login, the credentials or the delegate, and running it now
hands them over: the projection reads `enabled: false` as *this loophole's machinery is not run
for launches that read this config*, at user or workspace scope. The TOML spelling is
`off = true` under the pack's table, `[allow.<pack>] off = true` in the user config and the
project's `off` of [`config-project.md` §3](config-project.md#3-the-local-file-and-turning-something-off)
in a project file, so a profile written beside the switch survives conversion; the rename table's
`loopholes.*.enabled → off` row already names the word. This closes the user-scope half the
owner reopened under [RC-D32](readable-config.md#RC-D32): under P1 the answer is forced, and
[LG-D1](../reference/loophole-grants.md#LG-D1)'s *"hidden from the user"* is about a loophole
whose only approval is a grant, which these are not.

> [!WARNING]
> **"The daemon runs but serves nothing" is not true of the three services whose approval is a
> login or a setting.** The Claude broker serves the host's login to any jail once a human is
> logged in; `aws-auth` mints once a profile is set. A v0.15.1 `enabled: false` beside a
> configured profile, which the owner's own Bedrock file holds, meant *configured, and off*.
> Dropping the switch is a widening, and reading it as a warning applies it.

### 4.2 Class B: the crossing refuses what the host accepted

**What v0.15.1 did.** The crossing composed the user scope plus the workspace config, less the
per-workspace file, and wrote it as JSONC for the jail's reader. Every value the host accepted
crossed.

**What the tree does.** With any TOML-written `packs` entry, even a bare shipped name, the JSONC
route is barred (`formatJudgedTOMLValue`), the scope is handed to the migration's lowering with
no provenance, and the lowering declines what `yolo config migrate` declines, judging workspace
values by user-scope rules and reading `yolo-packs/pi-fork` as a relative path. The stop ends
*"Run `yolo check` on the host and fix the key it names"*; `yolo check` renders no inherited
scope and names nothing.

**The decisions.** Four rows, written for [`readable-config.md`](readable-config.md)'s ledger as
RC-D63 to RC-D66 and for [`boundary-broker.md`](boundary-broker.md)'s as BB-D112; they are
carried here as LC-D4 to LC-D7 because that document is being edited, and move there, keeping
their RC and BB ids, when it is free.

- <a id="LC-D4"></a>**LC-D4 (RC-D63): the crossing never refuses a config the host accepted.** A
  scope with no TOML-written value crosses as JSONC with its origins line, as today. A scope with
  any TOML-written value crosses as TOML, *always*: each value in the vocabulary the jail's
  user-scope reader has, a TOML-written value as written, a legacy-written value through the
  read-side lowerings the host's own readers apply, or through the migration's lowering applied
  to legacy-written values only (LC-D5). A legacy value neither can spell is **left out and
  disclosed once**, naming the host file and line, what the jail does without it, and the step
  where a ruling names one; this is P3 at the crossing. `yolo check` renders both scope files
  from the same computation and prints the same omissions, so the old stop's promise becomes
  true. Deleted: the combined refusal in `RenderFilteredForDeliveryLowering`,
  `mixedFormatRefusal`, and the grant-declaring decline in `LowerInheritedUserScope`; the guard
  in `RenderFilteredWithOrigins` stays as an invariant the launch no longer reaches, and if it
  fires it says it is a yolo bug. Supersedes the *Still refused* sentence of
  [RC-D55](readable-config.md#RC-D55). No hatch: nothing is left to turn off.
- <a id="LC-D5"></a>**LC-D5 (RC-D64): the lowering the crossing uses respells only values a
  legacy file wrote.** It takes the scope's provenance, already in hand at the call site, and
  leaves every TOML-written value exactly as written: a `packs` entry, a provider key, a model
  row, an `env` table. Only legacy-written entries pass through `classifyPack` and its siblings,
  which is what [RC-D58](readable-config.md#RC-D58) already does for the migration's proof; the
  two share the respell. A mixed `packs` list keeps its order, since each entry is respelled in
  place. The migration itself is unchanged.
- <a id="LC-D6"></a>**LC-D6 (RC-D65): in a TOML delivery, a legacy `loopholes` block crosses as
  its readable reading, which is nothing.** Selection is `packs`, which crosses; settings and the
  user's ceiling are `allow`, a project's asks are `requests`, and the readable inherit census
  rules both into *neither* file. A TOML user's jail has never seen any of it; this jail's
  loophole daemons are the host's, started from the host's gate, which reads the block through
  the legacy projection, so the host-side meaning is untouched. The block is left out silently
  from whichever file wrote it; the JSONC delivery still carries it. Two shapes with no TOML
  home that would change a *nested* launch get the LC-D4 disclosure line and nothing else: an
  inline declaration (`loopholes.<name>.command`), and `enabled: false` on a selected pack's
  loophole, whose spelling LC-D3 now gives.
- <a id="LC-D7"></a>**LC-D7 (RC-D66, BB-D112): a broker switch an older yolo wrote into its own
  per-workspace file is yolo's to clean up.** The file is yolo-written, so the launch, `yolo
  check` and `yolo config apply` rewrite it through the writer `yolo loopholes disable` already
  uses, and print one line naming the file and what replaced the switch; a file that cannot be
  rewritten whole keeps a warning naming why. Nothing asks the user to run a command for it,
  revising the warning half of [BB-D109](boundary-broker.md#BB-D109). **The switch becomes the read
  grant it gave, for that workspace** ([OQ-LC2](#OQ-LC2)): recorded in the approval record as a
  carried grant, the way every other loophole's switch file is, and the one line says so:
  *"Removed the old github-broker switch from <file>; this workspace keeps the read grant it
  gave"*.

Two mechanics beside these, settled here because an implementer would otherwise guess:
<a id="LC-D8"></a>**LC-D8: the in-jail read of the jail's own workspace honors
`withWorkspaceFile=false`.** The assembled-snapshot short-circuit in the in-jail loader returns
the snapshot for every caller, so the per-workspace file's values reach a nested jail's user
scope, which [BB-D56](boundary-broker.md#BB-D56) forbids. The short-circuit keeps the snapshot
for callers that want the workspace file and composes without it for callers that do not; a
nested launch's crossing is then the same computation as the host's. And a legacy `null` in the
inherited bottom layer means what absence means, the rule the crossing already applies to `env`,
so `mise_tools`, `resources`, `network.ports` and a provider's `models` set to `null` cross as
absent, not as a refusal.

### 4.3 Class C: the migration declines a config that launches

**What the owner ruled.** [RC-D50](readable-config.md#RC-D50), 2026-11-02: *"We should be able to
piecemeal migrate ... period. There's no second thing there."* Any mix launches, and `yolo config
migrate` converts every legacy file exactly; a spelling with no mapping is a TOML gap to close.

**What the tree does.** Thirty shapes v0.15.1 launched are declined with *"keep the old file for
now"*, several with advice that cannot be followed (*"rewrite it by hand in the new spelling"*
where none exists; *"make those entries absolute"* which the reader refuses; `yolo pack add
github-broker`, a loophole's name where its pack's belongs). The launch offer answered y on such
a plan stops the launch ([RC-D35](readable-config.md#RC-D35), the owner's: a requested conversion
that fails must not continue), so an optional offer becomes a stop.

**The decision.** <a id="LC-D9"></a>**LC-D9: `yolo config migrate` never declines a config that
launches, and the launch offer therefore never stops a launch.** Every legacy spelling the census
lists converts to a TOML spelling that reads back to the same effective value, and the
equality proof is what makes each one safe. The gaps, each with the spelling this document
proposes; [`readable-config.md`](readable-config.md) owns the vocabulary row when it is free, and
a different spelling that reads back identically is the planner's to prefer:

| Legacy shape | Rows | TOML spelling proposed |
| :--- | :--- | :--- |
| A project `brokered.<src>.grants` of kind `read`, `create-branch`, `create-pr`, `create-issue` | M1 | `[requests.<src>] <capability> = true`, through the manifest's `read_capability` and `kind_capabilities`, as the four kinds `grantRequest` already handles |
| A project grant on `repo: "*"`, or on a repository that is not the workspace's single remote, or with extra keys | M2 to M4 | A `requests.<src>` entry may carry `repo = "<url or *>"`: `push = [{ branch = "main", repo = "*" }]`; the gate shows the repository on the line. A grant for the wrong broker was never valid and stays refused |
| User `enabled: false` on a selected pack; user or project `enabled` on an ungated loophole | M5, M6, M11 | `off = true` under the pack's table (LC-D3); `enabled: true` on an ungated selected loophole converts to nothing, as it says nothing `packs` does not |
| A user `enabled: true`, or settings, for a pack not in `packs` | M7, M13 | Dropped with one line in the plan naming `yolo pack add <pack>` as an offer, since v0.15.1 warned and launched; the proof's loophole layer agrees because neither side runs it |
| A user valued switch (`host-processes` `visible`) | M8 | The user-config request of LC-D2: `[allow.host-processes] on_request = ["view"]` and `view = [...]` as the default every project asks for, shown once per project |
| An inline loophole declaration, user or project | M9, M10 | `[loopholes.<name>]` keeps its legacy keys in TOML, the one place the TOML user and project schemas gain a `loopholes` root; the readable inherit census keeps it out of a TOML delivery (LC-D6), and `yolo pack init` is offered in the plan's report as the better home |
| Settings without `enabled` (`aws-auth` `profile`, `serial` `default_baud`) | M12 | `allow.<pack>.<key>` as written: `aws-auth` runs on selection, so "dormant" was never true of it; `serial`'s baud rate admits nothing |
| User `brokered.github.sets` without the pack; `grant_kinds` or `grants` beyond read on `*` | M15, M16 | `allow.<src>.sets` and `on_request` as [RC-D54](readable-config.md#RC-D54) and [RC-D42](readable-config.md#RC-D42) give them, kept whether or not the pack is selected (it grants nothing until it is, which the nudge already says) |
| Nulls: `mounts`, `devices`, `kvm`, `agents_md_extra`, `prune`, `security`, `workspace_readonly`, `gpu`, `resources`, `network` | M19 to M21 | A list is reset by a `replace` spelling, since `[]` union-merges: `mounts = { replace = [] }` is rejected as a second shape, so the reset is `mounts_replace = true` beside the list, one boolean per list the merge unions; a table is reset by `"off"` in its place ([OQ-RC7](readable-config.md#decision-ledger)'s rule, a string where a table is expected); a scalar null is the key's default, which [RC-D27](readable-config.md#RC-D27)'s table already answers |
| The `{source, name}` pack rename form; a pack directly under `~` or `/`; a git URL with no owner/repo; a nested `//a/b` subpath | M22 to M25 | A `[pack_sources.<name>]` entry may name one directory (`{ dir = "~/mypack" }`) or one repository path (`{ git = URL, ref = REF, path = "a/b" }`), and the entry's handle is the pack's name; the `name` key is the handle's left side. [RC-D43](readable-config.md#RC-D43) listed these as having no spelling; they have one now |
| A profile named for its provider carrying `enforce_models` or `subagents`; a profile name holding `/`; an agent key `default` | M26, M27 | A variant may share its provider's name when the provider table writes no `model` of its own; a `/` in a legacy profile name is escaped in the variant's key through `internal/slug` ([RC-D48](readable-config.md#RC-D48)) and read back; `default` as an agent key stays the legacy `"*"` reading when the config also selects per agent |
| A symlinked include with its own `include_if_found`; an include reached from both graphs; a file not resolving beside its name | M28 to M30 | The converted include keeps its entries relative to the *target's* directory by writing them as the paths the reader resolved them to, relative to the new file; an include two graphs reach is converted once and both references are rewritten in one journaled commit |
| The generic *"has no spelling in the TOML config"* fallback | M32 | Unreachable from a legacy file once every key in the legacy schema has a rename row or a lowering; `TestEveryLegacyKeyHasALoweringRule` already pins the rule and gains the sixteen keys the census did not sample |

Three texts go with the declines: *"keep the old file for now"*, *"this yolo keeps reading
them"* (false for every Class D row, whose refusal stands by [OQ-LC1](#OQ-LC1)), and *"This is a yolo bug …
Your files still work as they are"* (false for M17). Under LC-D9 a plan that cannot convert a
launching config *is* a yolo bug, and the one remaining text says so and where to file it.

### 4.4 Class D: a retired spelling is refused outright

Five spellings v0.15.1 accepted are refused naming their replacement, with no read of what they
meant.

**Three are the owner's rulings**, each made before 2026-11-03 and each with a lossless mapping:
`mcp_presets` ([OQ-MP6](mcp-presets-removal.md#decision-ledger), 2026-09-20: *"full retirement …
no warn-and-ignore release"*; the owner's own v0.15.1 config held `"mcp_presets":
["chrome-devtools"]`), the `openai-auth` pack name ([OQ-PE3](provider-enablement.md#OQ-PE3),
2026-10-29: *"the old pack and profile names are refused naming the new one"*; in the owner's
v0.15.1 `personal.jsonc`), and a user pack's `requires` kind
([OQ-PS11](provisioner-sets.md#OQ-PS11), 2026-10-23: *"We don't have to support the old
thing"*). **They stand** ([OQ-LC1](#OQ-LC1), the owner, 2026-11-03: *"The Chrome DevTools and
the OpenAI rename had nothing to do with it. Those were already ruled on, transition paths ruled
on there."*): each is a retirement with its own ruled transition, not a break the TOML switch
made, so its refusal naming the replacement is the behavior, and the corpus test expects it
rather than listing it as a break. Not built: the corpus's `mcp_presets` and `openai-auth`
allowlist rows become expected refusals.

**Two are build decisions**, and the decision here is theirs: <a id="LC-D10"></a>**LC-D10: a
legacy manifest key is read for the window as what it meant, never dropped with a warning that
removes the loophole.** A user pack's `default_enabled: true` means what selection means and
reads as nothing; `default_enabled: false` means the loophole runs only where a config switched
it on, which the legacy projection already reads; `"scope": "workspace"` on a setting reads as
user scope with the workspace value carried through the projection and disclosed, as LC-D2 reads
`default_baud`. `yolo pack lint` passes them with a note; `yolo pack migrate` converts
`default_enabled: true` to nothing and reports `default_enabled: false` and a workspace-scope
setting as the author's to restate (a `[[grants]]` kind, or a request-scope setting under
[LG-D15](../reference/loophole-grants.md#LG-D15)), because a manifest is the author's and a pack
conversion is the author's act ([OQ-RC8](readable-config.md#decision-ledger) already times the
refusal of an unconverted fetched pack to the release that removes the old reader). This revises
[LG-D3](../reference/loophole-grants.md#LG-D3)'s and [LG-D5](../reference/loophole-grants.md#LG-D5)'s
*refused at load* for a legacy manifest only; a `loophole.toml` is refused as before.

### 4.5 Class E: a dangling link reads as no config

A dangling `yolo-jail.jsonc`, or a dangling `~/.config/yolo-jail/config.jsonc` (an rcm link
whose target `yolo config migrate` renamed, which the owner's sandbox home shows), is read as
*absent*: the jail launches with no workspace config, or with no packs and no providers, and
nothing says why. This predates v0.15.1 and is a same-meaning break all the same.

<a id="LC-D11"></a>**LC-D11: a dangling link where a config file may be is a refusal naming the
link, its target and the step.** `pathExists` on a config path follows the link and reports a
dangling one as its own state; the launch and `yolo check` print *"<path> is a link to <target>,
which does not exist"* and, where the target's `.toml` twin exists, name it as what the link
should point at. A missing file stays absent; only a link that points at nothing is a stop.
[RC-D59](readable-config.md#RC-D59) built the stop, without the `.toml` twin. An
`include_if_found` entry is the one exception the owner ruled: a link to nothing there is *not
found*, so the load goes on without it and says what it skipped
([RC-D67](readable-config.md#RC-D67)).

### 4.6 Class F: the message names a file to edit

Forty-one advice strings name `~/.config/yolo-jail/config.jsonc` or `yolo-jail.jsonc` by hand
(`loopholeUserConfigHint`, `WorkspaceConfigName`, `dropRemedyUserConfig`, and thirty more),
print a JSON snippet (`"packs": ["<pack>"]`, `"runtime": "container"`, `"host_management":
"own"`), name a verb that does not exist (`yolo pack add github-broker`), or make a claim that
is false for the config in hand. [RC-D40](readable-config.md#RC-D40) already rules the fix for
the sites it reached; the census shows the rule did not reach the tree.

<a id="LC-D12"></a>**LC-D12: every message that names a config file names the one that exists,
and names a verb before a key.** The user file is `paths.UserConfigDisplayPath()` and the
workspace file `config.ResolveReadableWorkspaceConfigPath`, never a constant; a value is spelled
by the verb that writes it (`yolo pack add`, `yolo config set`, `yolo allow`, `yolo request`),
never as a JSON or TOML snippet to paste; a pack is named by its pack name, a loophole by its
loophole name, and a message that mixes them is a test failure. The enforcer is a test over the
message strings the way `TestEveryHintedYoloCommandExists` already checks verbs: a non-test
string holding `config.jsonc`, `yolo-jail.jsonc` or a `"key": ` snippet fails it, with an
allowlist for the strings that quote a legacy input they are retiring. Help text and
`config-ref` are in scope; a pack author's `pack.json` advice is converted in the same sweep at
low priority. `yolo init`'s help says it writes `yolo-jail.toml`, which it does;
`init-user-config` writes a TOML user file, so a new user is not onboarded onto the format the
launch then offers to convert.

## 5. The enforcer: a corpus that must launch

A rule that lives in a document is a rule no release can check. The enforcer is a permanent
test, being built now beside this document, that this document names by role and not by its
Go name:

- **The corpus**: a fixture set of legacy configs, each one a shape v0.15.1 accepted: every
  example in v0.15.1's `config-ref`, every row of [Appendix A](#appendix-a-every-census-row-and-its-disposition),
  and owner-shaped fixtures (a TOML user config with `pack_sources` and `<source>/<name>` handles,
  `config.d` fragments, a legacy workspace file with a `loopholes` block for two grant-declaring
  loopholes and a `settings` table, a per-workspace file with a broker switch, the Bedrock file
  with `enabled: false` beside a profile). The thin fixtures that pass while the owner's launch
  fails are the reason the owner's shapes are in it.
- **The oracle**: v0.15.1's own meaning, recorded per fixture as the effective config it
  resolved, the loopholes it ran, the grants it gave, and what crossed into the jail.
- **The checks**, per fixture, against HEAD: the host load and validation accept it; the gate
  asks nothing a v0.15.1 launch did not (a y for an unapproved workspace file is allowed; a
  `yolo allow` line is not); the crossing delivers; the effective config, the loophole standing
  per workspace and the delivered scope equal the oracle's; `yolo config migrate --dry-run`
  converts every file with no decline and no difference; and no message on the way names a
  legacy file by hand or a JSON snippet.
- **The allowlist**: every fixture the checks fail today, by census id, with the LC row that
  fixes it. The test fails when a fixture outside the allowlist fails, and when a fixture on the
  allowlist passes (the row is then removed). <a id="LC-D1"></a>**LC-D1: the allowlist may only
  shrink.** A commit that adds a row has introduced a break and does not land; the list empty is
  what *done* looks like for this document, and the test stays once it is empty.

The nested jail cannot see every class ([`AGENTS.md`](../../AGENTS.md), the two blind classes),
but nothing here is reachability- or rootless-shaped: the corpus runs on the Linux gate.

## 6. What this does not license

- **No aliases in the TOML vocabulary.** [`readable-config.md`](readable-config.md#4-non-goals)'s
  *"an old spelling is refused naming the new one"* is about a legacy key in a *new* file, and
  stands. The legacy projection reads legacy *files*; a TOML file that writes `mcp_presets` or
  `loopholes.x.enabled` is refused as before.
- **No automatic migration.** The launch offers; a person answers ([RC-D21](readable-config.md#RC-D21)).
- **No change to what the gate approves**, or to which scope may hold which key in TOML. Where
  the legacy projection admits a workspace value (LC-D2), it is the gate's y that admits it.
- **No reopening of the two security refusals** of 2026-10-29: they are not TOML-switch changes,
  so P1 does not reach them, and they stand ([OQ-LC3](#OQ-LC3)).
- **No hatch.** Nothing here has a `YOLO_ALLOW_*`: a legacy config that cannot launch is a yolo
  bug, and the corpus test is where it is caught.
- **Not the release that removes the legacy reader.** That release breaks every legacy config by
  design ([RC-D19](readable-config.md#RC-D19)), and when it is is the owner's.

## 7. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **Warn and ignore** a legacy value with no new spelling | Rejected. Ignored means the config launches meaning something else, and for a switch that admitted or refused a credential, something worse. |
| **Refuse with a good message** naming the verb | Rejected by the owner in as many words: *"why do I need to change my configs?"* A good message is still a required action. |
| **Migrate on launch** without asking | Rejected by [RC-D21](readable-config.md#RC-D21) and [RC-D35](readable-config.md#RC-D35), the owner's; and it rewrites files a dotfiles manager owns. |
| **Keep the legacy reader as the only reader of legacy files and never lower them** | Rejected: the crossing has to write one file for the jail, and a scope with a TOML-written value cannot cross as JSONC without the jail misjudging the TOML value. Lowering with provenance (LC-D5) is the smallest change that keeps both. |
| **A `YOLO_ALLOW_LEGACY_*` hatch** while the classes are fixed | Rejected: a hatch is for a user's broken config, never for a yolo bug, and the allowlist (LC-D1) is the honest record of what is still broken. |
| **Keep RC-D55's "still refused" list** and tell the user to migrate | Rejected: it contradicts [RC-D50](readable-config.md#RC-D50) (*"No case refuses"*), and migrate itself declines the same shapes, so the advice loops. |

## 8. Risks

| Risk | Mitigation |
| :--- | :--- |
| LC-D2 grants more than the tree does today, and a reviewer reads it as a widening | It grants exactly what v0.15.1 granted, which the oracle records per fixture; every admission still passes the gate's y, which v0.15.1 required for a workspace file and LG-D12 requires for a user-wide grant. |
| LC-D9's new spellings are invented here, and [`readable-config.md`](readable-config.md) owns the vocabulary | Each row proposes one spelling and the behavior it must read back to; the planner may prefer another spelling that reads back identically, and the rows move to that document when it is free. |
| A fixture passes the corpus by accident (a thinner shape than the owner's) | The owner's shapes are fixtures by name: twice this release a fixture thinner than the owner's config passed while the owner's launch failed, and the corpus exists so that cannot repeat. |
| The allowlist stalls with rows nobody owns | Each row names its LC decision; the roadmap routes this document; an empty list is the done-state. |
| The three retirements stand ([OQ-LC1](#OQ-LC1)), so three fixtures can never pass as written | They become expected refusals in the corpus with the owner's reason recorded in the ledger, and *"this yolo keeps reading them"* is deleted for them; the test is still green on everything else. |

## 9. What I would build, in order

1. **The corpus test with its full allowlist** (LC-D1), so every later step is measured by rows
   leaving the list and nothing new can land.
2. **The crossing** (LC-D4 to LC-D8): one fixture, the owner's shapes, is the landing check; it
   fixes the launches the owner hit this week.
3. **The grant projection** (LC-D2, LC-D3, with LG-D15 and LG-D16): the owner's workspace block
   grants what it did, the Bedrock file stays off.
4. **The migration's gaps** (LC-D9), one spelling at a time, each with its proof row; the launch
   offer stops stopping as a consequence.
5. **The three retire-outright spellings** stand ([OQ-LC1](#OQ-LC1)): the corpus expects their refusals, and each refusal keeps naming its replacement.
6. **The dangling links** (LC-D11) and **the message sweep** (LC-D12), with their tests.
7. **The manifest window** (LC-D10), last: no owner pack is hit today.

## Decision Ledger

Every LC row is a design-seat decision under the owner's words of 2026-11-03 unless it says
otherwise. LC-D4 to LC-D7 are written for [`readable-config.md`](readable-config.md) (RC-D63 to
RC-D66) and [`boundary-broker.md`](boundary-broker.md) (BB-D112) and move there with those ids
when that document is free.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="OQ-LC3"></a>[OQ-LC3](#OQ-LC3) | **The two security refusals of 2026-10-29 (a workspace `env_sources` file outside the workspace; a mount source inside the workspace) are not TOML-switch changes, so P1 does not reach them; they stand as ruled.** The owner's ruling, on the question whether they were the one exemption: *"this has nothing to do with TOML converfsion? not sure why we're thinkgin about this."* | 2026-11-03 | [§1](#1-the-one-rule); [§6](#6-what-this-does-not-license) | nothing to build; the corpus test expects the refusal (`ruledLegacyRefusals`) |
| <a id="OQ-LC2"></a>[OQ-LC2](#OQ-LC2) | **A broker switch an older yolo wrote into its own per-workspace file becomes the read grant it gave, for that workspace:** the launch removes the switch, records the grant in the approval record as a carried grant (as every other loophole's switch file is carried), and says so in one line; `gh` works as before, with no command. The owner chose option A, in the words of its leaning: *"A — it is what the migration already does for every other loophole's switch file (the human's host command was the approval), and the job makes no exception for brokers; but it carries a login, so it is yours."* | 2026-11-03 | [§4.2](#42-class-b-the-crossing-refuses-what-the-host-accepted), [LC-D7](#LC-D7) | not built |
| <a id="OQ-LC1"></a>[OQ-LC1](#OQ-LC1) | **P1's scope is the TOML switch: a config that launched as JSONC keeps launching, and its TOML conversion launches meaning the same. The three retire-outright rulings (`mcp_presets`, [OQ-MP6](mcp-presets-removal.md#decision-ledger); the `openai-auth` pack name, [OQ-PE3](provider-enablement.md#OQ-PE3); the user pack `requires` kind, [OQ-PS11](provisioner-sets.md#OQ-PS11)) stand, with the transition paths ruled there.** The owner's ruling: *I want specifically the Toml switch not to break things. The Chrome DevTools and the OpenAI rename had nothing to do with it. Those were already ruled on, transition paths ruled on there. For specifically the Toml, if one moment it worked with JSONC and then you convert, then it has to work with Toml. That's the only rule I'm implying here.* | 2026-11-03 | [§1](#1-the-one-rule); [§4.4](#44-class-d-a-retired-spelling-is-refused-outright) | built: the corpus test expects the `mcp_presets` and `openai-auth` refusals (`ruledLegacyRefusals` in `TestLegacyConfigsLaunchUnchanged`) |
| [LC-D1](#LC-D1) | **The corpus test's allowlist may only shrink.** A fixture outside it that fails, or a row that passes and is not removed, fails the test; empty is the done-state and the test stays | 2026-11-03 | [§5](#5-the-enforcer-a-corpus-that-must-launch) | — |
| [LC-D2](#LC-D2) | **The legacy projection grants what v0.15.1 granted.** A workspace `enabled: true` is the request and its admission ([LG-D16](../reference/loophole-grants.md#LG-D16)); a user `enabled: true` on a valued kind is a user-sourced request to every project, admitted and shown once per project like an `always` line; workspace `fields` is the project's columns ([LG-D15](../reference/loophole-grants.md#LG-D15)); a switch for an unselected pack selects nothing and offers `yolo pack add` | 2026-11-03 | [§4.1](#41-class-a-the-grant-projection-grants-less-than-the-switch-did) | — |
| [LC-D3](#LC-D3) | **`enabled: false` keeps meaning off** for the login- and setting-approved services and `cgroup-delegate`; TOML spelling `off = true` under the pack's table. Closes the user-scope half [RC-D32](readable-config.md#RC-D32) left open | 2026-11-03 | [§4.1](#41-class-a-the-grant-projection-grants-less-than-the-switch-did) | — |
| [LC-D4](#LC-D4) | **(RC-D63) The crossing never refuses a config the host accepted;** any TOML-written value means a TOML delivery, an unspellable legacy value is left out and disclosed once, `yolo check` renders the same; the combined refusal, `mixedFormatRefusal` and the grant-declaring decline are deleted. Supersedes RC-D55's *Still refused* | 2026-11-03 | [§4.2](#42-class-b-the-crossing-refuses-what-the-host-accepted) | built, but for `yolo check`: `config.RenderInheritedDelivery` (the combined refusal and `mixedFormatRefusal` deleted) and the grant-declaring decline deleted from `configmigrate.LowerInheritedUserScope`; omissions disclosed once per launch (`TestTheOwnersMixedScopeCrossesAsTOML`, `TestInheritedDeliveryWithTOMLWrittenValuesCrossesAsTOMLLeavingOutWhatHasNoSpelling`, the corpus case `toml-user-source-handle-legacy-ws-loopholes`). `yolo check` does not render the inherited scope yet |
| [LC-D5](#LC-D5) | **(RC-D64) The crossing's lowering is origin-aware:** only legacy-written values are respelled; a TOML handle crosses verbatim; a mixed list keeps its order. Shares [RC-D58](readable-config.md#RC-D58)'s respell | 2026-11-03 | [§4.2](#42-class-b-the-crossing-refuses-what-the-host-accepted) | built: `TestAMixedPacksListRespellsOnlyItsLegacyEntries`, `TestTheInheritedLoweringRespellsOnlyLegacyPackEntries` |
| [LC-D6](#LC-D6) | **(RC-D65) A legacy `loopholes` block crosses into a TOML delivery as nothing;** an inline declaration and a selected pack's `enabled: false` are disclosed, never refused; the JSONC delivery still carries the block | 2026-11-03 | [§4.2](#42-class-b-the-crossing-refuses-what-the-host-accepted) | built: `TestTheInheritedLoweringLeavesTheLoopholesBlockOut`, `TestASelectedPacksLegacyOffSwitchIsDisclosedOnce`, the `a loophole's settings` case of `TestMixedFormatLegacyUserSettingsCrossAsTheirMigration` |
| [LC-D7](#LC-D7) | **(RC-D66, BB-D112) A stale broker switch in yolo's own per-workspace file is removed by the launch, `yolo check` and `yolo config apply`, said once;** an unrewritable file keeps a warning naming why. Revises the warning half of [BB-D109](boundary-broker.md#BB-D109). What replaces the switch is the read grant it gave, for that workspace ([OQ-LC2](#OQ-LC2)) | 2026-11-03 | [§4.2](#42-class-b-the-crossing-refuses-what-the-host-accepted) | — |
| [LC-D8](#LC-D8) | **The in-jail loader honors `withWorkspaceFile=false`** past the assembled-snapshot short-circuit, and a legacy `null` in the inherited bottom layer means absence, as it does for `env` | 2026-11-03 | [§4.2](#42-class-b-the-crossing-refuses-what-the-host-accepted) | built: `TestInJailReadWithoutTheWorkspaceFileSkipsTheAssembledCopy`, `TestInheritedLegacyNullWithNoRemovalSpellingCrossesAsAbsent` |
| [LC-D9](#LC-D9) | **`yolo config migrate` never declines a config that launches;** every census gap gets a TOML spelling that reads back to the same value (the table in [§4.3](#43-class-c-the-migration-declines-a-config-that-launches) proposes each), so the launch offer never stops a launch; a plan that cannot convert a launching config is a yolo bug and says so | 2026-11-03 | [§4.3](#43-class-c-the-migration-declines-a-config-that-launches) | — |
| [LC-D10](#LC-D10) | **A legacy manifest key is read for the window as what it meant** (`default_enabled`, a workspace-scope setting), never dropped with a warning; `pack migrate` reports what the author must restate. Revises [LG-D3](../reference/loophole-grants.md#LG-D3) and [LG-D5](../reference/loophole-grants.md#LG-D5) for legacy manifests only | 2026-11-03 | [§4.4](#44-class-d-a-retired-spelling-is-refused-outright) | — |
| [LC-D11](#LC-D11) | **A dangling link where a config may be is a refusal** naming the link, its target and the `.toml` twin where one exists; a missing file stays absent | 2026-11-03 | [§4.5](#45-class-e-a-dangling-link-reads-as-no-config) | — |
| [LC-D12](#LC-D12) | **Every message names the file that exists and a verb, never a constant file name or a snippet;** enforced by a test over message strings. Extends [RC-D40](readable-config.md#RC-D40) to the whole tree; `init-user-config` writes TOML | 2026-11-03 | [§4.6](#46-class-f-the-message-names-a-file-to-edit) | — |

## Appendix A: every census row and its disposition

Ids are the census's, so a corpus fixture and an allowlist row can cite them. *Where* is a
`file:function` at `2fc63bdde`. **B** is a break, **B(sm)** a same-meaning break, **HP** a
happy-path-only defect, **?** unverified.

### Launch and `yolo check`

| Id | Legacy input | Where | Verdict | Disposition |
| :--- | :--- | :--- | :--- | :--- |
| L1 | workspace `loopholes.host-processes.settings.fields` | `config/validate_loopholesettings.go:loopholeSettingsScopeViolations` | B | [LG-D15](../reference/loophole-grants.md#LG-D15) |
| L2 | workspace `loopholes.serial.settings.default_baud` | same | B | LC-D2 (read as the user's value, disclosed) |
| L3 | `mcp_presets: ["chrome-devtools"]` | `config/validate.go:validateMCPPresetsRetired` | B, conflicts with [OQ-MP6](mcp-presets-removal.md#decision-ledger) | [OQ-LC1](#OQ-LC1) |
| L4 | `packs: ["openai-auth"]` | `config/packs.go` (`formerly`); TOML `packsrc/naming.go:parseHandle` | B, conflicts with [OQ-PE3](provider-enablement.md#OQ-PE3) | [OQ-LC1](#OQ-LC1) |
| L5 | user `loopholes.github-broker.enabled: true` | `config/validate_loopholes.go:brokeredUserSwitchRefusalWithSources` | B | LC-D2 (a user switch on a broker is the read grant's `always`, through `read_capability`); the refusal becomes that reading |
| L6 | `enabled: true` with `visible` / `allowed_devices` / journal `full`, user or workspace | `config/loopholegrantlegacy.go` (projects to requests) | B(sm) | LC-D2, [LG-D16](../reference/loophole-grants.md#LG-D16) |
| L7 | the per-workspace switch file for `github-broker` / `git-broker` | `validate_loopholes.go:staleBrokerSwitchNote` | B(sm) | LC-D7, [OQ-LC2](#OQ-LC2) |
| L8 | user `enabled: false` on `claude-oauth-broker`, `aws-auth`, `cgroup-delegate` | `validate_loopholes.go:userSwitchesNothingNote` | B(sm) | LC-D3 |
| L9 | a user pack's manifest with `default_enabled` | `loopholedecl/keys.go:retiredTopKeyRefusal` | B(sm) | LC-D10 |
| L10 | a user loophole setting with `"scope": "workspace"` | `loopholedecl/settings.go:parseSettings` | B(sm) | LC-D10 |
| L11 | a user pack with `{"kind": "requires"}` | `packdecl/kinds.go:retiredKinds` | B(sm), conflicts with [OQ-PS11](provisioner-sets.md#OQ-PS11) | [OQ-LC1](#OQ-LC1) |
| L12 | `yolo-jail.jsonc` is a dangling link | `config/load.go:resolveWorkspaceConfigPath` | B(sm), silent | LC-D11 |
| L13 | user `config.jsonc` is a dangling link | `resolveUserConfigPath` | B(sm), silent | LC-D11 |
| L14 | user JSONC `loopholes.aws-auth.settings.profile` warned as *"serves nothing"* | `config/readableallow.go:AllowPackNudges` | ? | corpus fixture; LC-D12 either way |
| L15 | a TOML `allow` table beside legacy `brokered` allowances | `config/readablescope.go` (`!userHasAllow`) | ? | corpus fixture; if it drops the legacy allowances, LC-D2 (both are read) |
| L16 | `packs: ["guardrails"]`, which now installs `fd` and `rg` | `entrypoint/readiness.go` | ? | corpus fixture; a need an older release only checked may not stop a launch that proceeded |
| L17 | user `enabled: true` on `serial` / `host-processes` | `loopholegrantlegacy.go` | ? | LC-D2 (one y per project, no command) |
| L18 | a legacy provider name colliding with a shipped one, or holding `/` | `providersemantic_selection.go` | ? | corpus fixture; LC-D9's variant-key escape if reachable |

### The crossing

| Id | Input the host accepts | Where | Verdict | Disposition |
| :--- | :--- | :--- | :--- | :--- |
| X1 | TOML user `packs` + a legacy `loopholes.<grant-declaring>` block anywhere in the scope | `configmigrate/inherited.go` → `config.mixedFormatRefusal` | B | LC-D4, LC-D6 |
| X2 | TOML `packs` with a source-qualified handle (`yolo-packs/pi-fork`) plus any trigger | `configmigrate/packs.go:classifyPack` | B | LC-D5 |
| X3 | TOML `packs = [{git=…, ref=…, bare=true}]` plus a trigger | same, object branch | B | LC-D5 |
| X4 | TOML `packs = [{pack="…"}]` plus a trigger | same | B | LC-D5 |
| X5 | per-workspace file's `github-broker` switch, in-jail for the jail's own workspace | `config/load.go` assembled short-circuit → `lowerLoopholes` | B and B(sm) | LC-D8, LC-D7 |
| X6 | any decline `mixedFormatRefusal` does not reword | `config/inherit.go:RenderFilteredForDeliveryLowering` | B, dead end | LC-D4 |
| X7 | workspace or include `loopholes.cgroup-delegate.enabled: false` | `configmigrate/loweruser.go:lowerLoopholes` | B | LC-D3, LC-D6 |
| X8 | workspace `loopholes.github-broker` / `git-broker` `.enabled` | same | B, wrong advice | LC-D6, LC-D12 |
| X9 | workspace `loopholes.guardrails.settings` (a pack with no loophole) | `configmigrate/inherited.go` activation proof | B | LC-D6 |
| X10 | legacy nulls in workspace, include or layer: `mise_tools`, `resources`, `resources.memory`, `network.ports`, `providers.X.models` | `configmigrate/lower.go` (`nullPaths`, `lowerNetwork`) | B | LC-D8 |
| X11 | any TOML user `packs` (the amplifier) | `config/inherit.go:formatJudgedTOMLValue` | B | LC-D4 |
| X12 | workspace values judged by `loweringContext{scope: ScopeUser}` | `configmigrate/inherited.go` | enabler | LC-D4 (no declines remain to misjudge) |
| X13 | legacy `--user-layer` over a TOML user config | `mixedFormatRefusal` | B, wrong advice | LC-D4, LC-D12 |
| X14 | legacy `aws-auth.settings.profile` crosses as `[allow.aws-auth]`; the same TOML table is dropped | `readableInheritCensus["allow"]` vs `lowerLoopholes` | B(sm) | LC-D6: the legacy block crosses as nothing too, so both formats agree |
| X15 | TOML `allow.git` / `allow.github`, legacy `brokered`, `requests`: never cross | `readableInheritCensus` | trust boundary | stands ([OQ-LC3](#OQ-LC3) names it) |

### `yolo config migrate`

| Id | Legacy input | Where | Verdict | Disposition |
| :--- | :--- | :--- | :--- | :--- |
| M1 to M4 | project `brokered.*.grants`: `create-branch`/`read`/`create-pr`/`create-issue`; `repo: "*"`; a repo not the single remote; extra keys | `configmigrate/lower.go:grantRequest`, `grantScopeReason` | B (post-v0.15.1 legacy) | LC-D9 |
| M5 | user `cgroup-delegate.enabled: true`, or any selected grantless loophole | `loweruser.go:lowerLoopholes` | B | LC-D9 (converts to nothing) |
| M6 | user `enabled: false` on a selected pack | same | B | LC-D3 |
| M7 | user `enabled: true`, pack not in `packs` | same | B | LC-D9 |
| M8 | user `host-processes.settings.visible` | `loweruser.go:lowerGrantLoophole` (prints `<kind>`) | B | LC-D2, LC-D9, LC-D12 |
| M9, M10 | inline `loopholes.x.command`, user or project | `lowerLoopholes`; `lower.go:unknownToReadableSchema` | B | LC-D9 |
| M11 | project `loopholes.<grantless>.enabled` | `lower.go:filterRenamedLeaves` | B | LC-D9, LC-D12 |
| M12 | user `aws-auth` `profile` / `serial` `default_baud` without `enabled` | `loweruser.go:lowerLoopholeSettings` | B | LC-D9 |
| M13 | user settings for a pack not in `packs` | same | B | LC-D9 |
| M15 | user `brokered.github.sets` without the pack | `brokered.go:userBrokerDeclaration` | B | LC-D9 |
| M16 | user `grant_kinds` / `grants` beyond read on `*` | `brokered.go` | B (post-v0.15.1), latent | LC-D9 |
| M17 | `packs: ["openai-auth"]`; migrate keeps it, its gate fails, then *"Your files still work as they are"* | `gate.go`, `identities.go` | B, conflicts with [OQ-PE3](provider-enablement.md#OQ-PE3) | [OQ-LC1](#OQ-LC1); the false footer goes under LC-D9 |
| M18 | `mcp_presets` | `lower.go` rename row `RenameUnrepresentable` | B, conflicts with [OQ-MP6](mcp-presets-removal.md#decision-ledger) | [OQ-LC1](#OQ-LC1) |
| M19 to M21 | nulls in an include or local file: `mounts`, `devices`, `kvm`, `agents_md_extra`, `prune`, `security`, `workspace_readonly`, `gpu`, `resources`, `network` | `lower.go` + `config.LowerLegacyNulls` | B | LC-D9 |
| M22 to M25 | `{source, name}` rename form; a pack directly under `~` or `/`; a git URL without owner/repo; a nested `//a/b` subpath | `configmigrate/packs.go` | B | LC-D9 |
| M26, M27 | a profile named for its provider with `enforce_models` / `subagents`; a `/` in a profile name; an agent key `default` | `loweruser.go:placeProfile`, `selectionOf` | B | LC-D9 |
| M28 to M30 | a symlinked include with its own `include_if_found`; a non-link file not resolving beside its name; one include reached from both graphs | `plan.go:targetFor`, `graph.go:walk` | B | LC-D9 |
| M31 | `migrate FILE` for a layer another graph also includes | `graph.go:discoverLayer` | HP | names a working alternative; stands |
| M32 | the generic *"has no spelling"* fallback; sixteen keys unsampled | `lower.go:unknownToReadableSchema` | B class | LC-D9 (`TestEveryLegacyKeyHasALoweringRule` gains the keys) |
| M34 | *"(or leave them; this yolo keeps reading them)"* | `plan.go` | HP, false for M17/M18 | LC-D12 |
| M35 | the launch offer answered y on a refused plan stops the launch | `run/migrateoffer.go` | HP | unreachable under LC-D9; RC-D35 stands |
| M36 | `yolo allow` / `yolo request` on a JSONC file holding `brokered`: *"convert first"* | `cli/allow.go`, `cli/request.go` | ruled ([RC-D37](readable-config.md#RC-D37)) | stands; its dead-end chain (M15) closes under LC-D9 |
| M37 | `yolo config apply` | `run/liveapply.go` (runs the launch's gate) | inherits every launch break | every LC row |

### Advice text

Forty-two sites, in three groups; each is LC-D12's, and the test names them.

- **A constant legacy path**: `config/validate_loopholes.go` (`loopholeUserConfigHint`, six
  refusals), `config/validate.go` (`mcp_presets`, `cache_relocations`), `hostfiles.go`,
  `programs.go`, `profiles.go`, `entrypoint/prism.go` (`dropRemedyUserConfig`),
  `run/bedrockmodels.go`, `run/programoverrides.go`, `run/macosuserrelocations.go`,
  `awsauth/settings.go`, `configdrift.go`; and `config.WorkspaceConfigName` /
  `WorkspaceLocalConfigName`, constants equal to the legacy names, interpolated by
  `brokered.go` and `cli/configtarget.go`; and the *"runtime from yolo-jail.jsonc"* labels in
  `run/preflight.go`, `check/probes.go`, `run/loopholesettings.go`, `entrypoint/requires.go`,
  `macosuser/sessionguard.go`, `macosuser/commands.go`, `macosuser/ctxlinks.go`,
  `hostprocesses/hostprocessescmd.go`, `loopholes/loopholescmd.go`.
- **A JSON snippet where a verb belongs**: `loopholeNotInstalledRemedy` (`yolo pack add`),
  `run/accounthomehold.go` (`yolo config set runtime container`, itself withdrawn by
  [MMP-D26](macos-user-model-plan.md#MMP-D26)), `host_management`, `host_wrappers`,
  `host_floor`, `env_sources` (the retired spelling), `allow.mounts`, `brokered` (retired),
  `loopholes` in `macosuser/orchestrator.go` and `packload/served.go` (not a TOML key at all),
  `enforce_models`, `"runtime": "podman"`.
- **A false or wrong-verb claim**: `yolo pack add github-broker` / `git-broker` (the packs are
  `github` and `git`); the literal `<kind>`; *"until per-project grants land"* (stale);
  *"rewrite it by hand in the new spelling"* (none exists); `allow.*` named for a project
  file; *"make entries absolute"* (refused by the reader); *"run yolo check"* (names nothing);
  `yolo init` *"scaffolds yolo-jail.jsonc"* (it writes `yolo-jail.toml`); about 45
  `config-ref` lines teaching the legacy files as the place to edit.
