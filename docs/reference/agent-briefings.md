---
status: current
verified: 2026-09-23
verified_commit: 7ad8358c
covers:
  - internal/jailcontent/briefing.go
  - internal/jailcontent/briefingcore.go
  - internal/jailcontent/briefingtopics.go
  - internal/jailcontent/briefingsize.go
  - internal/cli/run/briefingsizeline.go
  - internal/cli/applyhostbriefings.go
  - internal/jailcontent/skills.go
  - internal/jailcontent/workspaceskills.go
  - internal/cli/run/workspaceskills.go
  - internal/jailcontent/write.go
  - internal/cli/run/prepare.go
  - internal/jailcontent/persistence.go
  - internal/cli/run/persistencemap.go
  - internal/cli/run/briefingdest.go
  - internal/cli/run/briefingreadback.go
  - internal/cli/run/unmatchedaudience.go
  - internal/cli/run/backendcaps.go
  - internal/entrypoint/hostbriefing.go
  - internal/packload/agentaudience.go
  - internal/packload/mergedest.go
tags: [briefings, skills, packs, agents-md, staging, audiences]
summary: "Where the text an in-jail agent reads at session start comes from: the two blocks (the user's prose first, then yolo's budgeted core and shipped packs' pointers), the launch's size line, the audience model that decides what varies per destination (the agent/agents fields, the fatal unknown-name gate, one owner per agent name), the per-destination staging file and its injective name, the read-only mount that makes an in-jail edit fail, and the inode-preserving refresh that lets a live jail see host edits."
---

# Agent briefings — the text an agent reads at session start

**Status:** CURRENT as of 2026-09-23, verified against `7ad8358c`. MEASURED: the audience
model runs end to end in a real container in CI's integration jobs, which were green at that
commit. [The workspace layer](#the-workspace-layer) was added on 2026-09-27 with the code that
builds it; its principles and rulings ([`OQ-WS1`](#oq-ws1) to [`OQ-WS7`](#oq-ws7)) moved in on
2026-10-01, checked against `d4e435a3`, when the workspace skills design graduated. Its
former host half and [`OQ-WS6`](#OQ-WS6), the measured
table of [where each agent reads skills](#where-each-agent-reads-skills) and
[the build record](#the-workspace-layers-build-record) moved in on 2026-10-16, when that design's
stub was deleted; that host half was removed on 2026-11-07 by the owner's ruling, and
[the host has no workspace skills layer](#the-host-has-no-workspace-skills-layer). UNMEASURED: no observed launch is recorded;
`TestWorkspaceSkillsReachAContainerJail` and, on a Mac,
`TestMacosUserWorkspaceSkillsArriveThroughTheComposedTree` are the instruments. The Storage classes item and part 4's shipped-pack prose were brought up to date on
2026-09-29 with the code that changed them. [What a composed briefing contains](#what-a-composed-briefing-contains)
was rewritten on 2026-11-13, checked against `cf97ab226`, when the briefing-diet design and its
plan graduated: the two blocks, the core, the topic files, the budgets and the launch's size line,
with that design's ledger, [BD-D1](#BD-D1) to [BD-D11](#BD-D11), under
[Why it's this way](#why-its-this-way). UNMEASURED: no launch with the new shape is recorded
beyond the unit and staging tests, and the agy cut is an owner observation not yet made.

Every coding agent reads an instruction file at session start. yolo **composes one per
destination**, host-side, on every invocation (on an attach, from the packs the running jail
booted with), stages it under a per-jail directory, and bind-mounts it read-only where the agent
expects it. Which destinations exist is entirely
pack data: a pack's `briefing` contribution names its own `into` path, and a jail with no
packs gets no briefing at all.

Skills ride the same staging area and the same read-only delivery, so they are covered
here too.

| Component | Lives in |
| :--- | :--- |
| Composition of the jail-managed body | `internal/jailcontent` (`BriefingContent`, `BriefingInput`, `setupHeader`, `enforcementLines`) |
| The core and its pointer | `internal/jailcontent` (`coreLines`, `corePointer`, in `briefingcore.go`) |
| The topic files, and the inline fallback | `internal/jailcontent` (`BriefingTopics`, `BriefingTopic`, `BriefingInput.InlineTopics`, in `briefingtopics.go`) |
| The two-block composition, at both notches | `internal/jailcontent` (`ComposeBriefingFile`, `BriefingParts`, `ComposeBriefingSections`, `ComposePackBriefings`, `ReadHostBriefing`, `HostBriefingBase`) |
| The launch's size line | `internal/jailcontent` (`BriefingSizeWarning`, `BriefingChars`); `internal/cli/run` (`noteBriefingSize`) |
| The inode-preserving write | `internal/jailcontent` (`WriteBriefing`) |
| Skills staging and its layering | `internal/jailcontent` (`PrepareSkills`, `SkillPlan`, `SetPackSkillDirs`, `SetPackSkillTargets`, `SkillStagingName`, `PackSkillSource`, `SkillTarget`); the pack layers' writer is `hostskills.ComposeInto`, and the launch's collision pre-flight is `internal/cli/run/jailskills.go` |
| The per-invocation refresh, and what it feeds the composer | `internal/cli/run` (`refreshJailBriefings`, `briefingPortsFor`, `briefedResourceLimits`, `briefingLoopholes`) |
| The one enumeration of destinations and staging names | `internal/cli/run` (`briefingDestinations`, `briefingStagingName`, `briefingDest`) |
| A pack's prose entries | `internal/cli/run` (`packBriefingProses`) |
| The in-jail read-back refusal on the host prepend | `internal/cli/run` (`mayPrependHostBriefing`) |
| The launch line for a host prepend that is there and cannot be read | `internal/cli/run` (`readHostBriefing`, `unreadHostSource`); the link-chain walk is `entrypoint.FindDanglingLink` |
| The audience fields and their validation | `internal/packdecl` (`Contribution.Agent`, `Contribution.Agents`) |
| The unknown-name skip, and the agent vocabulary | `internal/packload` (`AgentAudienceSkips`, `AgentNames`, `AgentVocabulary`) |
| One owner per agent name | `internal/packload` (`AgentNameCollisions`) |
| Host-side audience resolution and its report | `internal/packload` (`ResolveDestinations`, `Destinations`, `Orphan`, `AddressedDelivery`) |
| The ownership record that gates the host prepend | `internal/entrypoint` (`GeneratedHostBriefings`, `HostBriefingManifestPath`) |

**Reads with:** [`pack-system.md`](pack-system.md) (the `briefing` and `skills` kinds, and
the `contributes` vocabulary), [`jail-home.md`](jail-home.md#staged-read-only-content)
(how staged content is mounted), [`stringly-typed-references-principle.md`](stringly-typed-references-principle.md)
(the rules any name-a-component-by-string field follows), [`host-to-jail-handoff.md`](host-to-jail-handoff.md)
(the Handoff section).

---

## The three supported paths for agent instructions

Agents read instruction files at different scopes. YOLO defines three architectural paths for delivering instructions, behavioral directives, or system prompts:

| Path | Mechanism | Scope | Recommended use |
| :--- | :--- | :--- | :--- |
| **Option A: Pack Briefing Audience** | Pack contribution (`"kind": "briefing", "agents": ["pi"]`, or no audience to reach every agent) | Global (Host + Jails) | Personal or team-wide agent rules, behavioral directives, and prompt additions. Composed into `~/.<agent>/agent/AGENTS.md` (host) and `/home/agent/.<agent>/agent/AGENTS.md` (jail). |
| **Option B: Workspace Project File** | `<workspace>/AGENTS.md` or `<workspace>/CLAUDE.md` | Per-repository | Rules specific to a repository's codebase and development workflow. Live bind-mounted; yolo never rewrites project files. |
| **Option C: Explicit Projection** | `host_files` in user config (`yolo-jail.jsonc`) | Per-jail bridge | Explicit projection of legacy host dotfiles into a sandbox. |

> [!WARNING]
> **Host dotfiles outside these paths are bypassed and inert.** Managing dotfiles into `~/.pi/agent/APPEND_SYSTEM.md` or `~/.claude/CLAUDE.md` on the host does **not** project into `/home/agent/` inside the jail. Inside the jail, `/home/agent/.pi` (and similar agent state directories) is an isolated per-workspace state overlay (`<workspace>/.yolo/home/pi`, or `.yolo/home/.pi` on Apple Container), completely decoupled from host home storage. Ad-hoc agent-specific files like `APPEND_SYSTEM.md` are not mounted into the jail, nor are they read by YOLO's host prepend (which looks strictly for unmanaged `~/.pi/agent/AGENTS.md`). To deliver durable instructions across host and jails, use a pack briefing audience.

**There is no agent registry and no config key naming agents.** Which destinations get a
briefing follows from which packs are selected, and nothing is selected by default.

## What a composed briefing contains

**Two blocks, the user's first** ([BD-D11](#BD-D11)). Every destination's file, at both notches,
is composed by one function, `jailcontent.ComposeBriefingFile`, from the parts
`jailcontent.BriefingParts` names:

- **The user block**: the host file the destination's `after` names (when it is the user's),
  then `agents_md_extra`, then the prose of every selected pack that is not shipped with yolo,
  in config order.
- **yolo's block**: the jail-managed body (the header, any alert, and the core), then each
  shipped pack's prose in config order.

A `---` rule separates the two when both are non-empty. With no user prose the file is yolo's
block alone, with no rule; with no selected pack there is no briefing at all. yolo's block comes
last so that an agent that cuts a long file from the end loses yolo's text before any of the
user's. The terms, each coined in the briefing-diet design that built this shape:

- **User prose**: briefing text the user wrote or chose: the `after` host file,
  `agents_md_extra`, and every non-shipped pack's prose. A shipped pack the user has forked is no
  longer shipped, so its prose is user prose too.
- **Core**: the part of yolo's jail-managed body that every agent loads at session start, a
  budgeted page of facts ([below](#the-core)). Not the whole file.
- **Topic file**: one file in the generated `jail-mechanics` skill's `topics/` that covers one
  subject of this jail ([the mechanics reference](jail-mechanics-skill.md#topic-files)).
- **Alert**: one of the three conditional sections that report an event rather than describe the
  jail: provisioning failed, the attach-skew notice, and the handoff.

**Shipped means `packload.Pack.Official`**, carried into the composer as
`jailcontent.PackBriefing.Shipped`. An attach composes over the running jail's pack tree, whose
record does not carry a pack's origin, so `run.markOfficialFromLayout` marks a pack under the
tree's `_official/` directory as shipped; without it, every attach would move yolo's pack prose
into the user block (`TestAnAttachReadsEachPacksOriginBackFromTheTree`). The order is pinned at
the jail by `TestTheUsersProsePrecedesYolosCoreInTheJail` and `TestAForkedShippedPackIsUserProse`,
and at the host by `TestApplyHostPutsTheUsersProseBeforeYolosBlock` and
`TestApplyHostWithNoUserProseHasNoRule`.

### The user block

**1. The host user's own briefing, and only if it *is* the user's.** Driven by the declaring
contribution's `after: "host:<path>"`. If that host file exists **and yolo did not compose it
itself**, its content opens the user block (`jailcontent.ReadHostBriefing`). This is how a user's
global instructions reach every jail *on a machine where the host notch has never run*. The
mapping is filename-exact: a `CLAUDE.md` destination reads `CLAUDE.md`, and variants like
`CLAUDE.local.md` are not picked up. An absent host file is the normal case and says nothing, and
one that holds only whitespace composes as no user prose, so it adds no rule. One that is there
and cannot be read is left out with one launch line naming it, and the briefing composes without
it: a symlink to nothing (the file's own link or a link on a
directory above it, named with its target), something that is not a regular file, or a read that
fails ([agent-directory-map.md §4.2](../design/agent-directory-map.md#42-each-verb)).

> [!WARNING]
> **The jail must not read yolo's own host output back in.** Once `yolo host apply` made the
> host destination a file yolo composes *wholesale*, `after: "host:.claude/CLAUDE.md"` named
> yolo's own output: the jail prepended a file already holding every pack's prose and then
> composed the same packs again, so each pack section appeared **twice**,
> byte-identical. The prepend is gated on **ownership proved from a record** —
> `GeneratedHostBriefings`, reading the same manifest `yolo host apply` writes — never
> inferred from content, because a file that merely *looks* composed is still the user's. It
> **fails open**: an absent or unreadable record prepends as before, because dropping the
> user's instructions from their jail is a worse failure than repeating a pack's prose. On a
> machine that *has* run the host notch, the user's prose arrives through the conventional
> local pack, as user prose, instead — the same route the skills half took.
>
> **Inside a jail the record is not enough.** There, "home" is the outer jail's home, and every
> briefing destination in it is a read-only bind of the *outer* launch's staged file. That is
> yolo's output too, and the host-apply record cannot know about it. So a nested launch also
> refuses to prepend a file that is one of the selected packs' briefing destinations. Without
> that rule a nested jail's briefing held the outer one's entire briefing, every heading twice.
> Every shipped agent pack declares `after` equal to its own `into`, so this was every nested
> jail. A prepend source that is not a destination is still prepended in a jail.
>
> **Both checks match a file under another name too.** In a dotfiles layout one file has two
> names (`~/.foo/AGENTS.md` a link to `~/AGENTS.md`, with `after: "host:AGENTS.md"`), and
> `yolo host apply` writes through the link, so the record lists the link while `after` names
> the target. The launch compares file identity as well as the path, against the record and, in
> a jail, against every destination, so yolo's composition is not prepended under its other name.

`after: "host:…"` is **honored at the host too, where it names the user's own file**
([DP-B26](../design/declaration-parity.md#54-the-host-notch-and-the-entry-point)): `yolo host
apply` opens the user block with it, through the same composer as the jail. It is not read
when it names yolo's own output — a destination the apply composes, which is what every shipped
agent pack's `after` names (its own `into`), or a file the briefing record lists as yolo's, by
its path or as the same file through a link — and it is never opened when it cannot be read as
a file: the three cases above, each a warning in the launch's words, the destination composed
without it. A prepended file is named once in the report; a skip as yolo's output is `--verbose`
only, since every shipped pack reaches it. It is no longer origin-gated — a fetched pack's
`after` is honored like anyone else's.

**2. `agents_md_extra`**, verbatim — a config key for injecting arbitrary extra instructions
into every generated briefing, legal at user or workspace scope. `yolo host apply` reads the
user-scope value only, and passes it as `HostBriefingRequest.Extra`, never inside yolo's base: a
workspace config is agent-editable, and what it says reaches a jail's briefing and never a file in
the real home.

**3. Each selected non-shipped pack's prose that this destination's audience admits**, in config
order, **unlabelled** — one blank line between packs and nothing else. Within a pack its files are
ordered by pack-relative path, byte-wise, and joined with the same one blank line, so a pack's
prose reads as one section. Empty prose is skipped rather than emitting an empty section. What a
pack ships is its `briefing/` directory and any file a contribution names with `from` — never a
root `AGENTS.md`, `CLAUDE.md` or `GEMINI.md`, which is the pack repository's own
([`pack-system.md`](pack-system.md#what-a-pack-is-on-disk)). **yolo does not cap, trim or reorder
user prose**: the shipped-pack budget binds shipped packs only, and a local or fetched pack's
long prose is composed whole at both notches (`TestAUserPacksLongProseIsComposedWhole`).

### yolo's block

Emission order, from `BriefingContent` and then the composer:

1. **The confinement header** — see [Confinement](#confinement).
2. **The alerts**, each conditional and outside the core's budget ([BD-D5](#BD-D5)): each appears
   only after its event, and each is something the agent must see without asking. They sit at
   the top of yolo's block, not above the user block: an alert is yolo's text, and the ruling puts
   the user's rules first.
   - **⚠ Provisioning failed**, when the provisioning log records a failure. Refreshed every
     invocation, so it appears on the next attach after a failed boot.
   - **⚠ This session runs in a jail that could not take what started it**, only in the briefing
     an attach refreshes when it went ahead under `YOLO_ALLOW_ATTACH_SKEW` into a jail that could
     not receive what the entry delivers: the version the jail was launched with, each contract
     tag it lacks, what else differs, what was withheld (names only) and the restart that ends
     it. The next entry's refresh drops it ([`SK-D15`](attach-skew.md#why-its-this-way)). An
     Apple Container jail never shows it, since its briefing is the copy its launch made.
   - **Handoff**, when a fresh handover pointer is carried this launch.
3. **The core** ([below](#the-core)), ending in the pointer to the `jail-mechanics` skill.
4. **The topics inline**, only when this entry did not write them
   ([below](#the-inline-fallback)).
5. **Each selected shipped pack's prose** for this destination, in config order, composed like a
   user pack's. Each is a **pointer** *(a term coined in the briefing-diet design)*: at most 250
   bytes that say what the pack changes and which of the pack's own skills holds the rest
   ([BD-D2](#BD-D2); [`pack-system.md`](pack-system.md#the-shipped-pack-prose-budget)). The git
   and github packs point at `yolo-git` and `yolo-gh`. The claude and pi packs, each addressed to
   its own agent alone, say how that harness starts the background `yolo wait` and point at
   `yolo-claude` and `yolo-pi`, which carry where the agent's own workflow tools put worktrees
   ([DS-D33](jail-home.md#DS-D33)).

There is no tool inventory and no MCP listing: agents read their own generated config.

#### The core

The core builds a model; it does not instruct a procedure. A line stays in it only if an agent
that never read it would do damage or waste a turn *before* it had a reason to look anything up.
A refusal already carries its own next step, so the core does not restate what a refusal prints.
Its bullets, from `jailcontent.coreLines`, are each conditional on the fact they state:

| Bullet | What goes wrong without it |
| :--- | :--- |
| **Workspace**: `/workspace` is the host directory, bind-mounted live (on a native backend, the real path, and that no `/workspace` exists) | Agents invent a sync step, or treat `/workspace` as a copy they may rewrite |
| **What survives a restart**: the workspace and `$YOLO_DURABLE_DIR`; `/tmp` does not; most of home is read-only; never move or delete `.yolo`, never `git clean -x` the workspace | Work put in `/tmp` is lost at the restart, and a `git clean -x` deletes the jail's own state; neither announces itself beforehand |
| **Network**: `localhost` is the jail's own, the host's name, the forwarded and published ports | `localhost` silently reaches the wrong machine; no refusal follows, only a connection error |
| **Not in here**: the host's credentials, sudo, the host's home, and where shared host directories are | Agents spend turns hunting for `~/.ssh` or trying `sudo` |
| **Asking the human**: relay a refusal's whole menu, each command alone in a code block, `yolo wait` in the background, which commands are the human's, and `yolo host-run` | A refusal prints the menu; it cannot make the agent relay it |
| **Blocked here**, only when a selected pack blocks a tool | Names each block's replacement and groups the guards by kind; the `rg -r` trap is stated only when a replacement is `rg` ([BD-D7](#BD-D7)) |

**The pointer** closes the core (`jailcontent.corePointer`): read the `jail-mechanics` skill
before editing config, asking for a restart, or diagnosing a refusal, then the topic files this
jail has, by name. It names the skill file of the agent the destination belongs to, home-relative
(`~/.claude/skills/jail-mechanics/SKILL.md` for Claude), so an agent with no skill mechanism can
still read it; a destination with no skills directory of the same agent identity gets the
sentence without the path ([BD-D6](#BD-D6); `TestThePointerNamesTheDestinationsSkillPath`). The
run pipeline sets it per destination (`BriefingInput.MechanicsSkillPath`).

**Unbounded facts have a fixed cost.** Past four entries (`corePortListMax`), the network bullet
gives the port count and names the network topic instead of listing them
(`TestTheCoreDoesNotGrowWithEveryPort`). The macos-user core is the same bullets, tighter: a
one-line autonomy sentence in the header, the context directory as `$YOLO_CONTEXT_DIR`, and the
durable directory spelled from the workspace.

**The jail header lost its tooling line** (`yolo --help`, `yolo config-ref`) to the
`jail-mechanics` index, so the core spends no bytes on what the index carries; the host notch's
header keeps it (`TestBriefingHeaderStatesTheToolingLineOnce`).

#### Where the rest went

Everything the body carried before the core existed is a topic file, rendered from the same
`BriefingInput` by one renderer, `jailcontent.BriefingTopics`, and staged in the generated
`jail-mechanics` skill ([BD-D1](#BD-D1); the files, the index and the refresh are in
[the mechanics reference](jail-mechanics-skill.md#topic-files)). A topic exists only when its
subject does.

| Former body section | Topic file |
| :--- | :--- |
| Environment's workspace and home lines; Storage classes | `topics/storage.md` |
| The network paragraph, both port sections | `topics/network.md` |
| Additional Context Mounts | `topics/mounts.md` |
| Running a command on the host | `topics/host-requests.md` |
| Loopholes | `topics/loopholes.md` |
| Blocked Tools, with each message | `topics/blocked-tools.md` |
| Limitations, Packages & Resource Limits, What this environment does NOT do for you, nix | `topics/limits.md` |
| Skills; the tooling line | The skill's index |

What the topics hold that a maintainer could otherwise break:

- **Storage** leads with the launch's durable dir, `$YOLO_DURABLE_DIR`, as the place for work
  that must survive a restart and for `--lock`ed worktrees, or says why this launch has none
  ([`jail-home.md`](jail-home.md#storage-classes-and-the-durable-dir)). On a container backend
  the jail's storage classes follow (per launch, per workspace, every workspace on the machine,
  the workspace itself), each with its paths, what it survives, who shares it and what yolo
  cleans up, rendered from the launch's persistence map (see below). The map's two backend facts
  pick the wording, never the backend's name: a per-launch set in RAM (Apple Container, or podman
  under `ephemeral_storage: "tmpfs"`) is gone when the jail stops and uses the jail's memory;
  after the class bullets, a read-only home (podman) gets a bullet for its read-only rest, and a
  whole-home bind (Apple Container) one for the files yolo rewrites at each launch instead. The
  Secrets line comes last on both. On `macos-user` the topic opens with the home sentences, then
  the durable dir and one line about the Mac's shared `/tmp`.
- **Mounts** are filtered to the mounts the backend will actually bind (`run.briefedCtxMounts`):
  config `mounts` entries and pack `mount` grants, each labelled read-only or read-write, a grant
  with its pack, under a line naming what `$YOLO_CONTEXT_DIR` is on this backend. On macos-user a
  pack's single-file `mount` is a copy, and its entry says "copied at launch; host edits arrive
  at the next launch" ([`context-mounts.md` CX-D25](context-mounts.md#CX-D25)).
- **Loopholes** name the actual active set rather than an instruction to enumerate it; `yolo
  loopholes list` stays the live answer.
- **Limits** carries the OS, the credential boundary, the resource limits the backend actually
  imposes, the disk I/O priority and the host nix line, then the backend's own limitations under
  their old heading. On a backend with no container it offers no resource cap and says outright
  that `resources` is not enforced there.

#### The inline fallback

**The core never points at a topic file this entry did not write.** An attach refreshes a running
jail's briefing and its mechanics files in two places, so the run pipeline decides per entry.
When no fact sheet reached it (a legacy attach keeps the prior skill byte for byte), or the skill
render failed and left the prior tree, it sets `BriefingInput.InlineTopics`: the topics are
appended to the briefing under `##` headings, followed by the old Skills section, and the pointer
names no file and says the sections below hold the rest. Both forms read `BriefingTopics`, so the
skill file and the inline text cannot disagree (`TestInlinedTopicsCarryEveryFactAndNameNoFile`).

### Budgets, and the size line

Every bound is a test, and none is a config key: the number is yolo's
([BD-D4](#BD-D4)). A failure names the bytes over and the longest lines, and its fix is moving
text into a topic file or a pack's skill, never raising the number.

| Bound | Limit | Held by |
| :--- | ---: | :--- |
| The jail body (header and core, no alert) for one reference fact sheet per jail setup: `podman-self`, `apple-container-self`, `seatbelt-self`, `seatbelt-yolo-account` | 1,800 bytes | `TestTheCoreFitsItsBudget` (`coreBudget`) |
| yolo's block for Claude with `claude`, `git`, `github` and `guardrails` selected, composed through the launch's own staging | 2,600 bytes | `TestYolosBlockAloneHasNoRuleAndFitsItsBudget` (`yoloBlockBudget`) |
| One shipped pack's prose for one destination | 250 bytes | `TestEveryShippedPacksProseFitsItsBudgetPerDestination` |
| The `description` of a skill yolo ships: the built-in suite and the generated index | 200 bytes | `TestShippedSkillDescriptionsFitTheirBudget` |
| The `description` of a shipped pack's skill | 200 bytes | `TestEveryShippedPacksSkillDescriptionFitsItsBudget` |

The reference fact sheets are fixtures, not a live jail: each has the facts that add lines (a
forwarded port, a context mount, a durable dir, the guardrails blocks, a loophole).

**A launch says when a composed file is longer than its agent warns at**
([BD-D9](#BD-D9)). A briefing destination may declare `warns_above_chars`
([`pack-system.md`](pack-system.md#warns-above-chars)). When a composed file passes it, the launch
prints one yellow `Warning:` line on stderr (`run.noteBriefingSize`, worded by
`jailcontent.BriefingSizeWarning`): the file, its length, the limit and who warns at it, the
largest three parts (each pack's prose that reached the file, and the rest: yolo's text,
`agents_md_extra` and any host file), and the next step: move a section of the largest pack's
prose into a skill in that pack, or, with no pack prose, a section of the file into a skill. It
never refuses and never trims, and a launch has no quiet mode to hide it.

- **Characters are UTF-16 code units** (`jailcontent.BriefingChars`), what a JavaScript string's
  length counts, since Claude Code is a Node program; for ASCII prose that is the byte count.
- **Over means strictly longer**: a file of exactly the limit gets no line.
- **The file is measured at its final point**, after every part is composed and just before it
  is written to staging, from the write loop in `refreshJailBriefingsMode`. Pinned through the
  production staging by `TestALaunchWarnsWhenClaudesBriefingPassesItsMeasuredLimit`, which fails
  when the call is deleted.
- **Jail notch only.** `yolo host apply` composes the host notch's files on its own path and
  prints no size line.
- **Only a measured value is declared** ([BD-D10](#BD-D10)): today that is Claude Code's 40,000,
  on the claude pack's `.claude/CLAUDE.md` destination, and
  `TestOnlyTheClaudePackDeclaresAMeasuredBriefingLimit` fails if any other shipped pack declares
  one.

What each agent was measured to do with a long file, on 2026-11-13:

| Agent | Finding | Status |
| :--- | :--- | :--- |
| Claude Code 2.1.323 | Warns "Large … will impact performance" when one instruction file is longer than `max(40,000, context window × 0.05 × a per-model factor)` characters, and when the files together pass `max(120,000, that)` | Read from the binary's source text; the per-model factor was not read. At a 200,000-token window the floor of 40,000 applies |
| agy 1.2.9 | The maintainer reports that it silently drops the end of the file | UNMEASURED. The binary's strings name no limit |
| codex, copilot, opencode, pi, omp, muse | No limit recorded | UNMEASURED |

An unmeasured cap is not designed around: putting the user's prose first protects it from a cut
from the end, and nothing claims any agent then reads the whole file. The agy observation is the
maintainer's, since no automated test may start an agent: a unique word on the last line of a
personal pack's briefing, agy started in a jail and asked for the word. Its result belongs in the
table above, and a measured limit becomes that agent pack's `warns_above_chars`.

### At the host notch

**The body is the confinement header alone.** Every topic describes a launch — the `/workspace`
bind, the network the launch applied, the mounts, the shims, the jail's limitations — and `yolo
host apply` launches nothing, so each would be a false sentence to an agent on the real machine.
What the header says is the one thing a host agent must know: it is on the human's real machine,
nothing is disposable, nothing enforces a boundary, and permission prompts stay on. `yolo host
apply` composes the same two blocks in the same order ([BD-D8](#BD-D8)), through
`jailcontent.ComposeBriefingFile` with `jailcontent.HostBriefingBase` as yolo's base, into every
briefing destination a selected pack declares, with or without pack prose, so yolo owns each of
those files for as long as a selected pack declares it.

**Prose about a jail-only kind is left out.** A contribution that declares `describes` reaches a
destination only where every kind it names applies
([`pack-system.md`](pack-system.md#briefing-describes)). The github pack's `briefing/gh.md`
describes its `intercept`, the jail's `gh` forwarder, so `yolo host apply` composes it into no
file in a real home and says so once in its notch line, while every jail that selects the pack
still delivers it. A `skills` contribution takes the same field ([BD-D3](#BD-D3)), so the
`yolo-git` and `yolo-gh` skills are withheld at the host the same way.

### Provenance labels

**`briefing_provenance: true` labels each pack's section** with `<!-- from pack: NAME -->`, once
per section however many files it joins, in either block, as a debugging aid. It is off by
default, for two measured reasons:

- **The label worked against the prose.** A pack's briefing is the user's own rules for every
  repository, and an agent that sees "from pack: X" reads it as someone else's, scoped to
  something other than the repository in front of it — and discounts it.
- **Claude never saw it.** Claude Code strips HTML comments from `CLAUDE.md` before the model
  reads the file (2.1.280, 2026-09-22), so the agent the attribution was most wanted for never
  received it. Only a human opening the file did.

The jail reads the key from the effective config; `yolo host apply` reads it from the user config.

### What the body describes: applied, never configured

Every fact in the jail-managed body is what the launch **applies**, not what the config
asked for. This is one rule with several call sites, and each was a real defect:

- **Network mode** is the applied mode, so a nested jail forced onto host networking is told
  so instead of being told about a bridge it does not have. Both port sections key off the
  same value and are suppressed under host networking, where neither key is honored.
- **Resource limits** come from what the backend imposes, not from the `resources` map — the
  map told an Apple Container jail its pids limit was kernel-enforced when that flag is never
  passed there, and told it nothing about the caps that backend applies by default.
- **Context mounts** are filtered to what the backend binds. A section headed "Additional
  Context Mounts (read-only)" naming paths that were never mounted is the same lie as a
  network mode that was never applied.
- **Loopholes** are gated on the backend as well as on the loophole's own predicates:
  `Honored` has no backend term, so on a backend that starts no host services the unfiltered
  list advertises daemons that do not exist. An agent reading a false capability list does
  not merely lack a feature — it plans around one it does not have.
- **Paths** come from the **persistence map** (a term coined in
  [`jail-home.md`](jail-home.md#storage-class-terms)): each path the
  launch mounts writable, with a durability class, built from the same lists the mount argv
  iterates. A unit test compares it against the assembled argv in both directions, so a
  writable mount added without the map fails the gate. The Home line points at the section and
  no longer says the home is "persistent across sessions", which was false of podman's
  read-only home.
- **Nix** gets a line only where the launch mounts the host's nix daemon and store, decided by
  the predicate that emits those mounts. It says that a link nix roots under the workspace or the
  home is kept for the host as a managed root, with the lease and cap read from
  `nixroots.DefaultLease` and `nixroots.DefaultCap`, names `yolo nix-roots`, says a link under
  `/tmp` is not kept, and keeps "if a link dangles, rebuild it"
  ([the reference](in-jail-nix-roots.md#the-command)). `macos-user` shares host paths
  and never gets the line.

> [!WARNING]
> **No host probe belongs in the composer.** The fuller truth about networking is one
> `podman info` away, and the temptation arrives exactly here. This function runs on **every**
> invocation including attach, where it is the only work done and runs no subprocess at all —
> and a probe's answer can differ between the launch that started a jail and the attach that
> re-renders its briefing. So the briefing carries the *deterministic* decision only, derived
> from the runtime, the config and a path-existence seam. What the probe decided is already in
> the jail as an environment variable, which the bridge paragraph names.

### Confinement

The briefing opens with a header for the **notch** this environment runs at, and it reads the
notch's *profile*, not just its name. The name picks the title and the framing sentence — that
prose genuinely differs per notch and a human reads it — but the two facts an agent most needs
are **derived**: which primitives actually enforce the boundary, and whether agent autonomy is
on. The vocabulary comes from the same table `yolo describe` prints, so the two human-facing
descriptions of one primitive cannot drift.

That is what makes the header correct for a notch nobody has enumerated yet: an unrecognized
name falls to the default branch and describes its real enforcement vector instead of
asserting a container that may not be there.

> [!WARNING]
> **Never let a branch claim a container it does not have.** An agent told it is in a
> disposable container reasons about its home as throwaway. The `macos-user` backend runs at
> the *jail* notch — the notch dial is a separate axis from the runtime — but has no container
> anywhere in it, and it said "a sandboxed container" until that branch was split out. The
> profile is read for exactly this reason, and a caller that has not resolved a backend must
> not be handed a header that guesses a stronger boundary than it has.

A container jail's header is the title, the setup sentence and "You are running inside a
container." and nothing more: it counts against the core's budget, and the core's bullets say the
rest. The derived enforcement tail is appended on the guest, host, no-container jail and
unrecognized paths only, in a one-line autonomy form on all but the host notch, whose body the
core does not replace.

### Audiences: what varies per destination

**Parts 1–4 are composed per destination, and parts 1 and 4 actually differ.** Two fields on a
contribution carry the whole mechanism, and they are two opposite roles:

- **A destination identity** is the `agent` an agent pack declares beside its own `into`. It
  means "this path is where the agent launched as `claude` reads". Every shipped pack that
  installs an agent declares one on its `briefing` destination and on its `skills` destination,
  and each identity equals that pack's `program` bin.
- **An audience** is the `agents` list a content contribution names: the launcher commands its
  content is *for*. It reaches only the destinations whose owning pack declared a matching
  `agent`. A contribution that carries one is **addressed**.

A contribution that names no audience and no `into` **broadcasts**, and so does every
`briefing/` file no contribution names. That holds with a manifest as well as without one:
`{"kind": "briefing"}` is a valid declaration of "every agent", and it needs no agent names that
a jail might not select.

`packdecl` accepts `agent` and `agents` on three kinds, `briefing`, `skills` and `files`, and
refuses them on every other kind. That refusal runs ahead of the per-kind checks, so a kind added
later inherits it. `files` differs in one way: it has no conventional source, and its
destinations are agent-specific slot types, so it cannot broadcast. A `files` contribution still
names `into` or `agents`.

> [!NOTE]
> [`slots-and-contributions.md`](../design/slots-and-contributions.md) proposes replacing the
> `agent` field with a destination axis (`exposes`). **None of that is built.** The `agent` and
> `agents` fields described here are the system.

#### The two halves, and why neither knows the other's business

An agent pack owns both the name and the path, because both are facts about its agent. A content
pack names **who** and never **where**:

```jsonc
// the agent pack: a DESTINATION. It sources nothing.
{ "kind": "briefing", "into": ".claude/CLAUDE.md", "agent": "claude" }

// a content pack: addressed prose, and a broadcast beside it
{ "kind": "briefing", "from": "prose/claude.md", "agents": ["claude"] }
{ "kind": "briefing" }
```

The validator enforces the split from both sides:

- **`into` and `agents` together are refused.** They are two answers to one question. A
  destination has one right answer per agent, so `into` cannot be inferred while the agent is
  unknown. An audience supplies exactly that input: the destination is then borrowed from the
  pack that owns the named agent. A content pack that named both would be asserting a path only
  the agent pack can keep current.
- **A destination sources nothing.** Once `agent` is set, `from` is refused, and the message
  gives the addressed spelling that ships the pack's own content. `agent` beside `agents` with no
  `into` gets its own message, which spells out both readings, because the entry could be either
  one half-written.

#### The audience principles

These five principles are cited by id from code comments. **In this document `P1`–`P5` are the
audience model's.** [`pack-system.md`](pack-system.md#briefing)'s briefing defaults have their
own `P1`–`P6`, which is a separate namespace (its `P5`, for example, is "a destination sources
nothing").

<a id="ba-p1"></a>**P1. The audience namespace is the launcher-command namespace, and there is no
second one.** The value of `agent` and of every `agents` entry is a **bin**: the binary basename a
`program` contribution installs, the word a user types after `yolo --`. That is the namespace
`-p <cli>=<name>` and the `profile` key's `<cli>` entries already key on. It is never the pack slug. The
fields are *spelled* `agent` and `agents` because users think of a launcher command as an agent,
and because a config surface already names its owner with `"agent": "pi"`. Singular is the
identity a destination declares; plural is the audience a contribution names. The validator puts
both through the same guard as `bin` (`packdecl`'s `binProblem`), so the two namespaces cannot
drift apart.

<a id="ba-p2"></a>**P2. Scoping is opt-in, and silence means broadcast.** A contribution with no
audience reaches every destination of its kind. An empty identity is the other half of the same
rule: a destination that declared none still receives every broadcast, and no addressed content.
This is what let the field land before any pack adopted it. It also makes a broadcast portable,
because a list of every agent by name prints a skip line for each one a jail does not select (P3).

<a id="ba-p3"></a>**P3. The vocabulary is the ENABLED packs, and anything else is skipped.** An
`agents` entry reaches only an agent some pack in `packs` claims. Content addressed to any other
name delivers nothing for that name, the launch runs, and the skip is disclosed
([FS-D6](../design/pack-files-for-agent-state.md#FS-D6), which amended this principle from
"fatal"). `agents: ["cloude"]` and `agents: ["codex"]` in a jail without codex are skipped
identically, because yolo cannot tell a misspelling from an agent whose pack the user did not
select: a name outside the selection may belong to a pack yolo has never seen.

<a id="ba-p4"></a>**P4. A content pack names its audience; it never names a path.** Where an
agent reads, prose or skills, is the agent pack's business, and it changes when that agent
changes. A house-rules pack that hardcoded `.claude/CLAUDE.md` would be coupled to a fact it has
no way to keep current.

<a id="ba-p5"></a>**P5. A name has exactly ONE owner, and that owner provides all of the agent's
plumbing.** There is one `claude`. The pack that provides it declares where its briefing lands,
where its skills land, its config surfaces and its launch flags. Two packs cannot both be the
claude pack. The exclusivity that makes `-p claude=zai` unambiguous is the same exclusivity that
makes "deliver this prose to claude" unambiguous: one rule, not two.

#### The match is against a declared string

The match compares the audience with the destination's declared `agent`, literally. Nothing is
derived: not from the pack's name, not from a path, not from the bins the pack installs. The
profile chain does map a CLI name to a pack, through `packload.binOwner`, which returns the one
selected pack whose `program` surface installs that bin. That lookup is safe because a `program`
bin has exactly one owner. It answers "whose launch is this", and the audience match never calls
it.

> [!WARNING]
> **Do not derive a destination's identity from its pack's `program` bins, however convenient it
> looks.** It would make this the only kind whose owner is inferred. A derived identity also
> changes meaning silently when a pack or a path is renamed. A third-party agent pack that
> declares no `agent` is therefore unaddressable until it adds one. That cost is accepted, and
> [`yolo host apply` and the jail launch report it](#two-severities-an-unknown-name-is-fatal-an-unmatched-destination-is-reported)
> rather than hiding it.

<a id="two-severities-an-unknown-name-is-fatal-an-unmatched-destination-is-reported"></a>

#### Two outcomes: an unknown name is skipped, an unmatched destination is reported

An addressed contribution can reach nothing in two ways. The outcome splits where the user's
remedy splits:

| Question | Decidable from | Outcome | Whose fix |
| :--- | :--- | :--- | :--- |
| Is the name in the vocabulary? Does any enabled pack claim `claude` at all? | the claim set alone | **skipped and disclosed** (P3, [FS-D6](../design/pack-files-for-agent-state.md#FS-D6)) | the user, if they want it: `yolo pack add <pack>` when a shipped pack provides the name; otherwise the addressing pack's spelling |
| Did the name reach a destination of *this kind*? The pack owning `claude` is here, but does it declare a `skills` destination with `agent: claude`? | resolution | **reported**, never refused | the *owning* pack: an `agent` on its destination of that kind |

Refusing the second case would punish the wrong author, because the addressing pack's `agents` is
correct.

**The skip is `packload.AgentAudienceSkips`.** Its vocabulary is `packload.AgentNames` over the
**loaded** packs only; the packs yolo ships are read only to name a provider in the next step,
which reserves nothing. Each skip is one line naming the pack, the kind and the agent as written:

```text
pack matt-agentwork: files for agy not delivered — no selected pack provides agy
```

with `If you want it here:  yolo pack add agy` beside it when a shipped pack provides the agent,
and nothing beside it otherwise, so a misspelling reads as written. Three readers print it, and
none refuses: the launch's pack loading (`discloseAudienceSkips` in
`internal/cli/run/unmatchedaudience.go`, every launch it applies to, a narrowed build jail's
included, without the `yolo pack add` step there), `yolo host apply` (a `skipped` line in its
report), and `yolo check` (a `[SKIP]` row in its Pack audiences section,
`internal/cli/check/section_packaudiences.go`).

`yolo pack lint` does **not** read it, and must not. Lint takes a single pack root with no config,
so it cannot know the enabled set.

**The report is resolution's outcome, `packload.Destinations`,** which carries two views that are
not redundant:

- `Orphaned` says *why nothing arrived*. It is one `Orphan` per kind and audience that reached no
  destination. `Agents` records the orphaned contribution's own selector. An empty list means an
  unaddressed contribution found no destination of its kind anywhere in `packs`. A non-empty list
  means an addressed contribution matched no destination, whether or not other destinations of
  that kind exist. Declaring `into` is the remedy in neither case.
- `Addressed` says *where everything went*. It is one `AddressedDelivery` per addressed
  contribution: its audience, its source, and the destinations it reached. An empty destination
  list is the unmatched case. It describes the successful deliveries too, which an orphan by
  definition cannot.

`yolo host apply` prints both views (`reportInferredDestinations` in `internal/cli`). An unmatched
audience is a `no effect` warning at exit status 0. It names the owning pack and the selector, and
it says outright that declaring `into` is not the remedy.

**The jail launch prints its own warning for the unmatched case, and still refuses nothing**
(`reportUnmatchedAudiences` in `internal/cli/run/unmatchedaudience.go`, called from the launch's
pack loading right after the skip disclosure). The line names the addressing pack, the kind, the
pack-relative sources and the audience, and it says that declaring `into` is not the remedy. It
does not tell the user to select a pack, because it reports only a contribution naming at least
one agent a selected pack provides; one naming none was skipped, and said so in its own line. The launch still reads neither `Orphaned` nor
`Addressed`. The jail never resolves destinations the host's way: it composes each destination
from the whole pack set, filtering by that destination's declared identity. So the report asks
the jail's own enumerations instead: `briefingDestinations` for briefing, `packSkillTargets` for
skills, and the `files` slot rule `packFilesTargets` applies. Tests in
`unmatchedaudience_test.go` pin each of the three against the code that delivers it: briefing
composition (`refreshJailBriefings`), skills staging (`jailcontent.PrepareSkills`, read off the
staged files), and the files mount targets (`packFilesTargets`). So the warning prints exactly when
the jail delivers that content to no agent. Both notches report per contribution: content that
reaches at least one destination prints nothing, however many of its names matched none.

**Allowlist only.** No `except: [...]` form exists. Under P3 an author may name only enabled
agents, so the list is already bounded by the jail rather than by the set of agents in the world.
A denylist would relieve a burden that does not arise, and it would need its own answer to
"except an agent that is not enabled", which under P3 is a refusal.

#### One agent name, one owning pack

P5 is enforced by `packload.AgentNameCollisions`, and two packs claiming one name is **fatal** at
the launch pre-flight, at `yolo host apply` and at `yolo check`. The refusal names every claim
together with the field it came through. It ends in the one remedy that fits: the two packs cannot
both be selected. It does not tell a pack author to rename, because the name is the command the
user types.

A pack claims a name by owning part of that agent's plumbing. **Four kinds claim:** `program`
through its `bin`, and `briefing`, `skills` and `files` through a declared `agent`.
`AgentNameCollisions` and `AgentNames` read one shared gathering of those claims, so a name cannot
be an owner to the collision check and a stranger to the vocabulary check. A pack repeating its
own name across kinds is one pack owning one name: `packs/claude` claims `claude` three times, and
single-pack groups are skipped.

> [!WARNING]
> **A need is deliberately not a claim.** A need is a `program` with no `via`
> ([OQ-PS11](../design/provisioner-sets.md#OQ-PS11)). A content pack needing `claude` beside
> the pack that provides claude is the most ordinary dependency a pack can declare, and a pack
> needing `fzf` must not collide with another that installs it. That is why a need combines
> as Shared. Nothing is lost by leaving it out: a pack that owns an agent while only asserting its
> binary still claims the name through the `agent` its briefing or skills declares.

The name needs its own pass, beside `pluginNameCollisions` and `LoopholeNameCollisions`. The
generic collision loop keys claims by `(kind, target)` and skips every kind that is not
exclusive, and `briefing` and `skills` merge by design. The key has to cross kinds too:
`program claude` and `briefing agent: claude` are two different `(kind, target)` pairs. See
[`pack-system.md`](pack-system.md#collisions-the-generic-loop-cannot-see).

#### Per file, not per pack

**The audience is per FILE.** Each source file has exactly one governing contribution: the one
whose `from` names it, or else the one that omits `from`, or else nobody, which is the implicit
broadcast. So a pack that adds `{"from": "files/pi-rules.md", "agents": ["pi"]}` beside its
`briefing/` directory delivers both: pi's file to pi, and the directory to everyone. Declaring one
narrow delivery never switches off a broader one it does not name
([`pack-system.md`](pack-system.md#briefing-governance)).
A destination (`agent` + `into`) sources nothing, so an agent pack's own destination line
suppresses nothing either.

This is why composition happens **inside** the per-destination write loop. Composing once
above it was what made scoping impossible — a pack whose rules applied to one agent had to
broadcast them to all of them or drop them. Two consequences:

- The composition input is one entry **per source file**, each carrying its governor's audience,
  in the pack's filename order. At each destination the entries the audience excludes drop out,
  and the rest join as the pack's one section.
- One file is composed **once** per destination, because it has one governor. Two content
  contributions naming one source are refused at launch
  ([`OQ-PB5`](pack-system.md#oq-pb5)); one `agents` list names
  several audiences. The host notch composes the same bytes from the same predicate
  (`packload.GovernedSources`), less any file whose contribution `describes` a kind of its
  pack's own the host does not deliver ([BB-D69](../design/boundary-broker.md#BB-D69)), and
  [`briefingparity_test.go`](../../internal/cli/run/briefingparity_test.go) compares the two.

#### Where each notch narrows

The two notches apply one predicate at different points:

- **The jail** narrows at composition. `jailcontent.ComposePackBriefings` takes the destination's
  identity and drops every entry whose audience excludes it. For skills,
  `jailcontent.PrepareSkills` filters every source against each destination's identity before it
  copies. Both filters implement P2 the same way: an empty audience matches everything, and an
  empty identity matches only broadcasts.
- **The host** narrows at resolution. `packload.ResolveDestinations` turns an addressed
  contribution into ordinary contributions carrying the `into` of each matching destination, one
  resolution per borrowing contribution. `entrypoint.ComposeHostBriefings` then composes only
  contributions that carry an `into`, and it has no audience filter of its own.

> [!WARNING]
> **Do not add an audience check to `ComposeHostBriefings`.** An addressed contribution carries
> no `into`, so it never reaches that loop. A check there would be dead code sitting beside the
> real filter, and it would read as though it were doing the narrowing.

Each notch has its own call-site pin, because the two notches narrow in different places and a
test of the shared predicate alone stays green when a call site is deleted. The host tests drive
the resolve-then-compose pairing, the way `yolo host apply` does, so they fail when the audience
check in resolution is removed. The jail tests drive the refresh, so they fail when the
destination identity stops reaching the compose call, or when either skills wiring is dropped:
the destination's `agent` or the source's `agents`.

**Reporting at the single-pack views.** `yolo pack lint` and `yolo pack footprint` print an
addressed contribution's target as `→ <agents>` and a declared broadcast as `→ every agent`. A
blank target read as "goes nowhere", which is the opposite of what a broadcast does. The detail
column says either which audience a contribution addresses, or which identity a destination
declares, so a reader can tell whether a destination is addressable at all.

**Measured end to end in a container.** `integration/packaudience_test.go` selects two agent
packs and one content pack whose prose and skills are both addressed to one of them. It asserts
that the prose and the skill are present at that agent's destination and absent from the other.
A second test asserts that an audience naming an unselected agent refuses the launch.

## Staging and delivery

Composed files land host-side in the per-jail staging directory, **one file per
DESTINATION**, then bind-mount read-only at that destination:

```
<staging>/briefing-.claude~1CLAUDE.md   →  /home/agent/.claude/CLAUDE.md:ro
<staging>/briefing-.codex~1AGENTS.md    →  /home/agent/.codex/AGENTS.md:ro
```

`briefingDestinations` and `briefingStagingName` are the **one** enumeration of destinations
and the **one** encoding of a staging filename, and both halves of the launch call them: the
refresh composes and writes, the assembler binds. That coupling is structural rather than
conventional for a reason — **a missing bind source for a *file* is not an error the way a
missing directory is**, so a disagreement here does not fail the launch, it produces a jail
whose agent reads a blank briefing.

Destinations are **deduplicated by path, first declaration winning**, because the mount made
that the rule: `briefing` is a concatenating kind — several packs contributing prose at one
path is designed behavior, and the composition merges all of it — but podman rejects a
duplicate mount destination and kills the boot, so exactly one bind per path may be emitted.

A contribution with an empty `into` is dropped, checked rather than assumed: since a
contribution may legally name an audience instead of a path, and the mount half appends the
`into` to the home root, an empty one would bind a single staged file over the jail's entire
home.

> [!WARNING]
> **The staging name must be injective, and the obvious escape is not.** It is RFC 6901's
> JSON-Pointer encoding — `~`→`~0`, `/`→`~1` — because two destinations sharing a staging file
> would deliver one agent's composed briefing to the other, silently. Doubling `~` and mapping
> `/` to `~` was tried and caught by test: it sends both `a/~b` and `a~/b` to `a~~~b`. Two
> escape sequences that cannot prefix each other is what fixes it, which is exactly what
> RFC 6901 chose them for. A hash prefix would be injective only probabilistically. Nothing
> decodes the name — it is a one-way disambiguator, kept readable because the thing being
> debugged is usually "which file did this jail actually mount".

The read-only mount is why an in-jail agent gets `Read-only file system` when it tries to edit
its own briefing: kernel-enforced and intentional. On Apple Container, single-file mounts under
the home are unsupported, so the files are materialized under the workspace state dir instead —
same content, different plumbing. On macos-user there are no mounts at all: the same staged tree
is **copied** over the sandbox home, and the session's Seatbelt profile write-protects the copy
instead of a mount doing it — an edit, rename or delete of a delivered briefing or skills dir is
refused at the path it physically lands at, since 2026-09-27
([`macos-user-home-tiers.md`](macos-user-home-tiers.md#the-staged-skills-and-briefings-are-write-protected-at-the-path-the-kernel-sees);
the kernel's refusal is not yet measured on a Mac).

## Skills

Skills ride the same staging directory, one subdirectory per `skills` contribution, each
mounted read-only at that contribution's `into`. Staging is **rebuilt every invocation**,
clearing contents *inside* each directory.

**Three layers, lowest first:** the **workspace**, the built-in skill suite, then every
selected pack's skills in config order. On an attach the pack layer comes from the packs the
running jail booted with, not the configured ones, since a running jail keeps its pack tree
([`OQ-PK2`](pack-system.md#oq-pk2)). A pack may override an ordinary built-in — a legitimate
reason to ship one — and the conventional local pack is appended last among packs. Fact-backed
`jail-mechanics` is the exception: its generated built-in replaces a same-named pack skill and
reports the override; the workspace layer cannot replace it. The [mechanics reference](jail-mechanics-skill.md)
explains how this keeps the applied-environment guidance tied to its host-side deciders.

**The pack layers are the host's.** Since
[`OQ-NC11`](../plans/notch-convergence.md#7-decision-ledger) the jail builds the host's layer plan for
each destination (`jailcontent.SkillPlan`: every source whose audience admits the destination,
one layer per pack) and writes it with the host render's own writer
(`hostskills.ComposeInto`) into an empty scratch directory. Three things follow, each the host's
behavior:

- **Two packs shipping one skill name to one destination refuse the launch**, the local pack
  included. The refusal is a pre-flight beside the agent-name one
  (`run.checkSkillCollisions`), so it runs host-side before any container exists and on an attach
  too, and it prints the host's message (`hostskills.CollisionError`): the destination as the
  agent reads it (`~/.claude/skills`), both packs, the source path the user edits, and the two
  remedies. `PrepareSkillsWith` checks the whole plan again before it writes any destination.
  A personal skill therefore no longer silently replaces a shared pack's of the same name.
- **`skills_tier` is honored.** A namespaced pack's skills land in a subtree of their own with
  yolo's plugin manifest and invoke as `/<pack>:<skill>`, as at the host.
- **Wrapped plugins are delivered as at the host**: verbatim, with yolo's marker, from a
  namespaced pack; at the flat default only the plugin's skills, with every other component
  Claude Code loads, prose and code alike, named as refused
  ([`pluginpack.Components`](../../internal/pluginpack/pluginpack.go), the table
  [trust-paths.md](../design/trust-paths.md#where-a-pin-would-change-the-outcome) mirrors). Each
  refusal names the fix, setting `skills_tier` to `namespaced` in the pack's
  `pack.json`, or, when a namespaced delivery was downgraded, the folder in its way. A plugin at
  the pack's root is delivered the same way whether or not the pack has a `skills/` folder. That
  is the shape of a Claude Code [mod](https://code.claude.com/docs/en/plugins/mods/overview), a
  manifest and `hooks/` and nothing else. It reaches the destinations the pack's skills are
  addressed to (`packload.Pack.SkillsAudience`), since it sits in none of the pack's skills
  sources. `yolo host apply` delivers it too (`packload.Pack.ResolveDestinations`). There, a pack
  that names no skills destination of its own reaches every agent its `skills/` folder is
  addressed to, or every agent when nothing addresses it. That covers a `pack.json` saying only
  `"skills_tier": "namespaced"` and a pack with no `pack.json` at all. A pack that names a
  destination delivers the plugin there and nowhere else, as it does its skills. A plugin whose
  manifest `name` is not a plain folder name, one with a `/` or a `..`, is refused at both
  notches, with the rename to make, rather than written where that path points.

The built-in suite and the workspace fill only the names the packs left free, except for the
fact-backed `jail-mechanics` built-in described above. A **reserved child** a destination declares
(`packs/claude` reserves `synced`) is withheld whichever layer ships it, and no lower layer fills
it. What the writer refused or withheld prints on stderr at every invocation, as `Skills: …` lines
beside the workspace ones.
The **fan-out stays the jail's**: every selected pack's skills reach every destination their
audience admits, where the host narrows a content `into`
([`OQ-S4`](../plans/BACKLOG.md#OQ-S4), open).

### The workspace layer

A repo's committed skills reach every agent, not only the one whose path the repo chose
([`OQ-WS1`](#oq-ws1)): the repo picks the content, never the agent. Five principles shape the
layer. They are the workspace layer's `P1` to `P5`, anchored `ws-p1` to `ws-p5`, a namespace of
their own beside [the audience principles](#the-audience-principles) above:

- <a id="ws-p1"></a>**P1. The repo owns the content; yolo owns the reach.** A repo's skills are the
  repo's business. Which agent its reader runs is not.
- <a id="ws-p2"></a>**P2. Core knows no agent's paths.** Where an agent reads at project scope is
  a fact about that agent, declared by its pack (`project_dirs`).
- <a id="ws-p3"></a>**P3. Delivery, never selection.** The layer moves bytes already in the
  workspace to a path an agent reads. It never fetches, never names a source outside the tree, and
  never chooses which packs exist, which is the line that separates it from `packs`: no workspace
  config selects a pack.
- <a id="ws-p4"></a>**P4. What lands in the repo is visible in a diff, or is not written.** So in
  a container and on macos-user nothing is ever written into the workspace.
- <a id="ws-p5"></a>**P5. A cloned repo can never reach the host through this.** The staging runs
  host-side, so any path that dereferenced a committed symlink would be a host read. This is
  forbidden behavior, not a knob.

The **source set** is every
project-scope skills directory any agent pack declares on its skills destination
(`project_dirs`, [the `skills` kind](pack-system.md#skills)) — the shipped packs' whether or not
they are selected, then the selected packs' own. Today that is `.claude/skills`,
`.agents/skills`, `.github/skills`, `.codex/skills`, `.opencode/skills` and `.pi/skills`; core
names none of them. Each source's skill directories are copied, host-side, into every skills
destination's staging dir, and nothing is ever written into the workspace.

- **Lowest, so it adds and never shadows.** A workspace skill takes only a name no built-in,
  pack, local-pack skill or yolo's own LSP plugin took. A shadowed name is **disclosed, one line
  per name**, however many destinations it was shadowed in. That order governs only what yolo
  stages: an agent that reads the repo's directory itself sees the repo's copy beside yolo's
  whatever yolo stages, so the launch names each such agent and skill instead
  (`pi reads .agents/skills/configuring-the-jail natively, so yolo cannot keep it from competing
  with yolo's built-in skill of that name`).
- **The skip rule.** A destination whose own `project_dirs` include a source directory reads it
  natively, so it gets no copy of that directory — nor of a skill it reaches natively through a
  link from another source, nor of any skill whose **name** a directory it reads natively
  carries. `pi` deduplicates by real path, so a copy would load as a second skill of the same
  name.
- **Two source dirs, one name.** The first in the source set's order wins in every destination
  yolo delivers it to (selected packs in config order, then the rest of the shipped packs by
  name), and the collision is said, naming any agent that reads a losing copy natively and so is
  sent neither. A skill held back for that reason with no collision to explain it gets a line of
  its own.
- **Nothing outside the workspace is read, and nothing but what was checked.** The reader
  resolves every link itself, inside the workspace, through an `os.Root` confined to it, and then
  reads only the real path it classified: every open is a walk from the workspace root that
  follows no link, so an entry swapped for a link after it was checked is refused, not followed.
  A link that leaves the workspace, a dangling one, a cycle, a special file, anything resolving
  into `.git`, `.yolo` or (in a container) a per-side path, an entry whose path inside its skill
  passes 512 bytes, and one whose copy cannot be written are all **skipped and named** — never
  fatal — and a skill left with no file to stage is not delivered at all. A refusal names the
  entry and a fixed reason, never the link's target. An absolute link spelled `/workspace/…` is
  read as the workspace in a container, since that is how the agent there wrote it.
- **One launch copies at most 32 MiB and 4096 files and directories.** Each skill copies the
  trees its links reach, so many skills linking one large in-repo directory copy it once per
  skill. A skill whose copy would pass either cap is **refused whole and named**, with the cap
  and what it would add; the skills copied before it stay, and a later one that still fits is
  delivered. Everything the layer writes counts, a refused skill's partial copy included, and
  every destination copies from that one scratch tree, so none receives more than the cap
  ([`OQ-WS7`](#oq-ws7); the measurement behind the numbers is [WS-D18](#ws-d18)).
- **Re-read on every invocation**, attach included, so an edit under a declared path reaches the
  next `yolo` command against a running jail — the same tree the agent there already reads live.
  Each source that delivered anything gets one `Workspace skills from <dir> mirrored into …` line.
- **The mirror is containers' and `macos-user`'s only.** macos-user receives it through the same
  composed tree it copies; the host notch never does, by ruling ([`OQ-WS5`](#oq-ws5)), and
  `yolo host` writes nothing into the workspace for skills in its place: by the owner's ruling of
  2026-11-07, [the host has no workspace skills layer](#the-host-has-no-workspace-skills-layer)
  ([WS-D24](#ws-d24)).

After both layers, yolo writes its **own LSP plugin** into every skills destination, rendered
from `lsp_servers`, or removes it when that list is empty. This is not a third content layer: it
is yolo's generated output, and it goes last so that no pack can ship a directory of the same
name and replace it. Only Claude reads a plugin; at another agent's destination it is a directory
nothing looks for, which is cheaper than teaching the loop which agent is which.

Each source carries an **audience**, matched against the destination's declared identity by
the same empty-is-broadcast rule the briefing half uses. Without it the source list is global —
every selected pack's skills reach every destination — so an agent-specific skill would be
copied into another agent's tree with nothing able to stop it.

> [!WARNING]
> **There is no layer reading the host's own `~/.<agent>/skills` tree, and adding one
> back is circular.** The workspace layer is not that layer: it reads project-scope *sources* no
> render writes, and refuses anything under `.yolo`. That layer named "the user's own skills tree" but was set to the
> *destination* — the host's copy of the very path the staging dir gets mounted over. It was
> right while the destination held loose user files and became circular the moment
> `yolo host apply` **composed** it: the jail read yolo's own generated output back in as the
> user's tree, and since the local pack is an ordinary pack entry, its content arrived twice by
> two routes. Invisible only because a flat copy was last-writer-wins, and since
> [`OQ-NC11`](../plans/notch-convergence.md#7-decision-ledger) it would be a collision refusing the launch.
> The slot it described already has a home — the conventional local pack is the last pack layer —
> so a personal skill arrives by the same route every other pack's content takes.

`PrepareSkills` still takes a home directory and an agent-name list; both are vestigial, kept
because its callers pass them and churning those would be a bigger diff than the fix with no
behavior in it. The launch calls `PrepareSkillsWith`, which is the same composition with the
workspace layer as an argument.

#### The host has no workspace skills layer

`yolo host -- <agent>` writes nothing into the workspace for skills, by the owner's ruling of
2026-11-07 ([WS-D24](#ws-d24)):

> *"maybe the answer is just for yolo host we don't try to help with this I don't see any way
> around that because everything else is worse."*

> *"this agent skill stuff and the gitignore … those aren't features that were opted in. They
> create files that are left around that are hard to put in ignore rules"*

The mirror never reaches the host notch ([`OQ-WS5`](#oq-ws5)): a host render's content is a
function of the user's configuration and installed packs, never of the directory it runs from. The
only other mechanism was a link written into the repository, and that is what the ruling removes.
A link is a surprising file a launch creates in the user's repository, and no ignore rule can
reliably cover it, because which agent's path is canonical differs per repository and per reader.
An agent on the host reads the repository's own project skills paths, whatever the repository
commits there, and nothing more.

From 2026-10-04 to 2026-11-07 the host did write such a link: one relative symlink at the
launched agent's first project skills path, pointing at the first skills directory the repository
kept under another agent's path (`.codex/skills -> ../.claude/skills`), plus a `/<link>` line
appended to the tracked `.gitignore`, with records under yolo's state dir. Those were agent
decisions taken under delegation, [WS-D19](#ws-d19) to [WS-D23](#ws-d23) and
[`OQ-WS6`](#OQ-WS6)'s "decided on its leaning (a)", not owner rulings; all are superseded.
**yolo does not clean them up**: a link or `.gitignore` line a past launch left in a repository,
and the records in `~/.local/share/yolo-jail/host-workspace-skills/`, stay until the user removes
them, because a silent edit to the user's repository is the very thing the ruling rejects.

<a id="oq-ws6-background"></a>**Background to [`OQ-WS6`](#OQ-WS6), below, kept as the record
of a superseded question: the three options in full.** The question decided what `git status` shows after a launch under the host half's links,
and which file yolo becomes a writer of.

- **(a) root `.gitignore` append**, write-once by content check — `yolo init`'s precedent; visible
  as `M .gitignore` once, then quiet for every clone once committed; but it is a launch editing a
  tracked file.
- **(b) `.git/info/exclude` append** — invisible, per clone, never committed; but it is the blind
  cell, the file
  [`host-execution-from-the-workspace.md`](host-execution-from-the-workspace.md#the-two-axes)
  calls load-bearing precisely because writing it moves things from visible to invisible; it is
  shared across worktrees; and `workspace_readonly` may lock it.
- **(c) a `.gitignore` inside each parent directory yolo itself created** — the `.yolo/` trick;
  clean when yolo made `.codex/`, impossible when the repo already has `.codex/`, so it needs (a)
  or (b) as a fallback anyway.

⚠ **Note, 2026-09-24, on the leaning's last sentence's premise.** `.yolo/.gitignore` is keyed on
the FILE: it is written whenever the file is absent, so emptying it is not fought and deleting it
is. (a)'s "write-once by content check" is keyed on the LINE, and run on every launch it re-adds a
line the user removed, as `yolo init`'s own "append if the file lacks it" would if it ran more than
once. So "not fought" does not carry over by itself; (a) needs its own record of having written
the line once. The leaning itself is unchanged.

- 💬 **[`OQ-WS6`](#OQ-WS6): For the links, root `.gitignore`, `.git/info/exclude`, or a
  yolo-owned parent's `.gitignore`?** Superseded on 2026-11-07: the links are gone, so none of
  the three is written ([WS-D24](#ws-d24)). The options in full, and the
  2026-09-24 note on the leaning's premise, are [in the background above](#oq-ws6-background):

  - **(a) root `.gitignore` append.**
  - **(b) `.git/info/exclude` append.**
  - **(c) a `.gitignore` inside each parent directory yolo itself created.**

  <!-- vantage: question id=OQ-WS6 leaning="(a): P4 decides it. A visible one-line edit the user commits once beats an invisible per-clone write to the file that defines invisibility. The write-once rule .yolo/.gitignore already follows (a user who removes the line is not fought) carries over unchanged." -->

  _Leaning:_ **(a).** [P4](#ws-p4) decides it: a visible one-line edit the user commits once beats
  an invisible per-clone write to the file that defines invisibility. The write-once rule
  `.yolo/.gitignore` already follows — a user who removes the line is not fought — carries over
  unchanged.

  **Answer:**
  > **Deferred with [`OQ-WS5`](#oq-ws5)**, 2026-09-27: the maintainer confirmed it is moot for v1
  > (*"and OQ6 is moot, right?"*). The links are the host half's mechanism, which v1 does not
  > build; the question returns, unchanged, when the host half is taken up.
  >
  > **Decided on its leaning, (a), 2026-10-04, when the host half was taken up under the
  > maintainer's delegation; open to the maintainer's revision.** The launch appends one line,
  > `/<link>`, to the `.gitignore` of the directory it runs in, once, and only inside a git work
  > tree. The 2026-09-24 note's premise is met by a record: yolo remembers that the line stood
  > there, whether it wrote the line or found it, so a line the user removed is not added back
  > ([WS-D22](#ws-d22)).
  >
  > **Superseded, 2026-11-07, by the owner's ruling** ([WS-D24](#ws-d24)): the 2026-10-04 decision
  > was an agent decision under delegation, not an owner ruling. The host writes no link, so it
  > writes no ignore line in any of the three places.

#### Where each agent reads skills

**Measured**, and the table the shipped `project_dirs`
declarations and their probe rest on (moved here on 2026-10-16 from the deleted design). Every row
was read from the bundle installed in yolo's development jail on 2026-09-17, by grepping its
strings, unless the row says otherwise; the vendor docs were not trusted, following the lesson
[`../plans/pack-host-management-plan.md`](../plans/pack-host-management-plan.md#n6-new--copilot-reads-claudes-plugin-manifests-and-namespaces-plugin-skills)
records (a docs-only pass got three facts wrong about `copilot`). "Home scope" is the directory
under `$HOME`; "project scope" is relative to the directory the agent is started in. Those are
plain words, not terms — the vendors say *personal*, *user* and *global* for the first and
*project* for the second.

| Agent, version | Project-scope skills dirs the binary names | Home-scope dir (= the pack's `into`) | Collision rule, as documented | Follows symlinks |
| :--- | :--- | :--- | :--- | :--- |
| `claude` 2.1.275 | `.claude/skills` only. `.agents/skills` appears in the binary solely inside an **importer** that copies it into `.claude/skills` | `~/.claude/skills` | personal > project, same name = higher wins **silently**; plugin skills namespaced `plugin:skill` | yes (documented; verified 2026-08-01) |
| `copilot` 1.0.48 | `.github/skills`, `.agents/skills`, `.claude/skills` | `~/.copilot/skills` (also reads `~/.agents/skills`, `~/.claude/skills`, `COPILOT_SKILLS_DIRS`) | project first; **first-found-wins, deduplicated by bare name**, loser silently dropped; plugin skills namespaced but deduplicated flat | not verified |
| `codex` 0.145.0 (row corrected against 0.157.0, 2026-09-27) | `.codex/skills` (the `skills` dir of every project config layer's `.codex/` folder) **and `.agents/skills`** (every directory from the project root down to the cwd). Read from the skills loader's source at the `rust-v0.157.0` tag, `codex-rs/ext/skills/src/host_roots.rs`: the binary carries neither as text, and this row first read its strings and missed the second ([WS-D12](#ws-d12)) | `$CODEX_HOME/skills` = `~/.codex/skills` (deprecated there in favor of `~/.agents/skills`) | not verified | not verified |
| `opencode` 1.18.31 (`.opencode` row re-read against 1.19.4, 2026-10-22) | `.opencode/skills`, `.claude/skills`, `.agents/skills`. 1.19.4 names none of them as one string: it walks up from the project for `.opencode` dirs and globs each with `{skill,skills}/**/SKILL.md`, so it also reads `.opencode/skill` | `~/.config/opencode/skills` | not verified | not verified |
| `pi` 0.85.1 (row corrected against 0.87.1, 2026-09-27) | `.pi/skills` (its `CONFIG_DIR_NAME` is `.pi`) **and `.agents/skills`**, the latter discovered from the cwd up to the repository root — its `docs/skills.md` and the package manager's ancestor walk both say so; this row first read `CONFIG_DIR_NAME` alone and missed it ([WS-D12](#ws-d12)) | `~/.pi/agent/skills` | loads both scopes; **deduplicates by real path**, so a symlink to an already-loaded file loads once | yes (follows symlinked dirs, skips broken ones) |
| `agy` 1.1.7 | `.agents/skills` | `.gemini/config/skills` (the pack's `into`; the binary's strings do not name it) | not verified | not verified |
| `oh-omp` | not installed here — unverified | `.oh-omp/agent/skills` | — | — |

> [!NOTE]
> **Codex and `.agents/skills` — settled 2026-09-27: codex reads it.**
> The deleted research survey `agent-config-distribution.md`
> (gathered 2026-07-25) listed `.agents/skills/` for Codex, and this note once called the row
> *unconfirmed* because the binary names no such literal. The loader's source at the tag of the
> installed 0.157.0 settles it: `repo_agents_skill_roots` joins the constants `.agents` and
> `skills` onto every directory between the project root and the cwd. The binary's strings agree
> once read closely — its only `.codex/skills` literals are prose about the home dir, and its one
> `.agents/skills` literal belongs to an external-agent migration — which is why a string grep
> could neither confirm nor refute the row.

**The fact this table settles:** there is **no project-scope path every agent reads.**
`.agents/skills/` — the Agent Skills standard's emerging interop path — reaches `copilot`,
`opencode`, `agy`, `pi` and `codex`, and misses `claude`. `.claude/skills/` reaches `claude`,
`copilot` and `opencode` and misses `codex`, `pi` and `agy`. A repo that wants all six ships two
trees, or one tree and a symlink, and has to know that. (The sentence first said `.agents/skills/`
missed `pi` and, unconfirmed, `codex`, from the two rows corrected above.)

#### The workspace layer's build record

The implementation decisions the layer was built on, `WS-D1` to `WS-D24`, which code comments
cite by id; moved here on 2026-10-16 from the deleted design, whose argument (the gap, the four
candidate mechanisms, both notches, its numbered risks R1 to R10 and its list of what done looks
like) is in git history (`git log --follow -- docs/design/workspace-skills.md`). A row's "risk R*n*"
or "done criterion" names one of those. [`OQ-WS1`](#oq-ws1) to [`OQ-WS5`](#oq-ws5) and
[`OQ-WS7`](#oq-ws7) are in [the why-appendix](#why-its-this-way), [`OQ-WS6`](#OQ-WS6) is
[above](#oq-ws6-background).

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| <a id="ws-d1"></a>WS-D1 | *Implementation decision.* The declaration is `project_dirs` on the agent pack's `skills` **destination** (`agent` + `into`), in the agent's own precedence order, read through one accessor (`Manifest.ProjectSkillDirs`). Entries must be clean, relative, inside the workspace and outside `.git`/`.yolo`, each once; the field is refused on any other kind and on a content entry. The in-jail decode tolerates it as it tolerates any newer field, so the strict-decoder trap the sketch warned of does not arise | 2026-09-27 | [`project_dirs`](pack-system.md#project_dirs) | ✅ `bd79aed3` |
| <a id="ws-d2"></a>WS-D2 | *Implementation decision.* The source set is the **selected** packs' declarations in config order, then every **shipped** pack's by name, each path at its first appearance. The selected packs are read too so a configured agent pack yolo does not ship counts: its destination's skip rule already names its paths. This order is the collision rule (WS-D3) | 2026-09-27 | [OQ-WS3](#oq-ws3) | ✅ `7df13e51` |
| <a id="ws-d3"></a>WS-D3 | *Implementation decision.* Two source dirs with a same-named skill: the **first** in WS-D2's order wins in every destination the mirror delivers to, the loser is never delivered, and one line names both. One real directory reached by two spellings (a committed link) is one skill, not a collision, and a source dir that is a link to another source is one source. *Amended by WS-D13:* a destination whose agent natively reads a losing copy is sent neither, and the line names it — this row first said the first wins "in every destination", which that agent made false | 2026-09-27 | [the workspace layer](#the-workspace-layer) | ✅ `7df13e51`, `debb2d98` |
| <a id="ws-d4"></a>WS-D4 | *Implementation decision.* The skip rule works at two grains: a destination gets no copy of a source dir its own `project_dirs` resolve to, **nor** of a skill whose real directory it already reaches through another source it reads (`.claude/skills/x → ../../.agents/skills/x` reaches `pi` natively). A destination declaring none receives every source. The third grain, the name, is WS-D13 | 2026-09-27 | [`project_dirs`](pack-system.md#project_dirs) | ✅ `7df13e51` |
| <a id="ws-d5"></a>WS-D5 | *Implementation decision.* "Lowest" is built as **fill the free names**: the built-ins, the packs and yolo's LSP plugin compose first, then the workspace takes only names none of them took. The LSP plugin's name is always taken, because its writer removes a same-named dir when nothing is configured. Each shadowed name is one line naming who took it and in which destinations | 2026-09-27 | [OQ-WS2](#oq-ws2) | ✅ `7df13e51` |
| <a id="ws-d6"></a>WS-D6 | *Implementation decision.* P5's reader is `confinedTree`: links are resolved lexically through an `os.Root` on the workspace, with the root's own `Lstat`/`Readlink`, before anything is opened, so an escape is named without anything outside being touched; every open is `O_NONBLOCK` (`O_DIRECTORY` for a dir), since an unflagged `os.Root` open blocked forever on a FIFO (measured). *Amended by WS-D15:* reads no longer go through the `os.Root`. Skipped and named, never fatal: an escape, a dangling link, a chain over 40 links, a cycle, a second link to one directory within a skill, a special file, an unreadable entry. The second-link rule bounds one skill's copy by its links times the largest tree one of them reaches; this row first called that "linear in the links", which hid that the bound is not the workspace's size (risk R10). A refusal names the entry, never its target — its reason included, since WS-D16 | 2026-09-27 | [the workspace layer](#the-workspace-layer) | ✅ `7df13e51`, `c011321e` |
| <a id="ws-d7"></a>WS-D7 | *Implementation decision.* Also never read: anything resolving under `.git` or `.yolo` (yolo's state — reading it would be a layer reading generated output, [the skills composition](#skills)), and in a container the per-side shadow set `venvShadowMountArgs` mounts. The jail sees its own copy there, and the mirror must grant no authority the repo lacks ([OQ-WS1](#oq-ws1)'s answer). macos-user shadows nothing, so nothing is excluded there | 2026-09-27 | [OQ-WS1](#oq-ws1) | ✅ `7df13e51` |
| <a id="ws-d8"></a>WS-D8 | *Implementation decision.* An absolute link is inside the workspace when it names the root as given, its real path, or, in a container, the jail's mount path `/workspace`: the agent there writes links that way. Nothing at that path on the host is read. Every other absolute target is an escape | 2026-09-27 | [the workspace layer](#the-workspace-layer) | ✅ `7df13e51` |
| <a id="ws-d9"></a>WS-D9 | *Implementation decision.* The layer is re-read on **every** entry, attach included, though an attach keeps the pack tree the jail booted with ([`OQ-PK2`](pack-system.md#oq-pk2)). The jail's agents already read the same tree natively and live, so a frozen mirror would be the stale half; each entry's `mirrored into` line discloses it. Closes R6's open half | 2026-09-27 | risk R6 | ✅ `7df13e51` |
| <a id="ws-d10"></a>WS-D10 | *Implementation decision.* The disclosure goes to stderr, where an attach's other lines go, and has no quiet mode ([`OQ-RO3`](report-tiers.md#why-its-this-way)). One line each per refusal, collision and shadowed name, plus one `Workspace skills from <dir> mirrored into <destinations>: <skills>` line per delivering source dir. Silent when there is nothing to say. A workspace-supplied name that is not plain text is printed Go-quoted with `[` escaped, so a directory name cannot forge a line or restyle one; a refusal's reason gets the same treatment since WS-D16, and the held-back and native-reading lines are WS-D13's and WS-D14's | 2026-09-27 | [the workspace layer](#the-workspace-layer) | ✅ `7df13e51`, `c011321e`, `debb2d98` |
| <a id="ws-d11"></a>WS-D11 | *Implementation decision.* The layer is copied **once** per invocation into a private scratch tree and handed to each destination from there, so a refusal is said once and every destination gets the same bytes. The execute bit is carried, as packstage carries it. A top-level file in a source dir is not a skill, as for every layer; a skill with no `SKILL.md` is copied as-is, but one of which **no file** could be staged (every entry refused, or none there) is not delivered at all, so no empty `x` stands where a refused link was (the design's fourth done criterion) and a later source's skill of that name may win; absent and empty dirs are silent; a source dir that is a file, the workspace root, or a link out is named. The path bound and scratch-write refusals are WS-D17. *Amended by WS-D18:* this row first said there was no byte bound, which [OQ-WS7](#oq-ws7) ruled and WS-D18 built | 2026-09-27 | [the workspace layer](#the-workspace-layer) | ✅ `7df13e51`, `7219dcf6` (the no-empty-skill rule) |
| <a id="ws-d12"></a>WS-D12 | *Implementation decision.* The shipped declarations come from the agents installed on 2026-09-27. `pi` declares `.agents/skills` too, correcting [where each agent reads skills](#where-each-agent-reads-skills). `codex` declares `.codex/skills` and `.agents/skills`, from its skills loader's source at the tag of the installed 0.157.0 (`codex-rs/ext/skills/src/host_roots.rs`). This row first kept `.codex/skills` alone because the binary's one `.agents/skills` literal sits beside an external-agent migration — true, and beside the point, since the loader builds that path from segments; until corrected, every skill in a repo's `.agents/skills` reached `codex` twice. `omp` declares nothing, because it was not measured, so it receives every source. The witness is `internal/packload`'s census test and a real-install integration probe that greps each installed agent, from its launcher's `REAL_BIN`, for every declared path **at project scope** — not preceded by `/`, `~` or a path character. It first matched a bare substring, which `codex` passed on its home-dir prose alone. The rows no installed bundle carries as text are listed in the probe with where each was measured instead, and logged rather than checked: `codex`'s (built from segments), `copilot`'s (1.0.88 ships its code compressed) and `opencode`'s `.claude/skills` and `.agents/skills` (named only as home dirs). No string probe can find a path an agent reads and its pack does not declare (risk R5) | 2026-09-27 | [`project_dirs`](pack-system.md#project_dirs) | ✅ `bd79aed3`, `22daf939` |
| <a id="ws-d13"></a>WS-D13 | *Implementation decision.* The skip rule's third grain, the **name**: a destination is sent no copy of a skill whose name any directory its agent reads natively carries — a collision's losing copy, or an entry the reader refused, which the agent resolves in the jail where its target may be a skill. The collision line names the agents that read a losing copy natively (`…; pi reads the copy in .agents/skills natively and is sent no other`); a hold-back no collision explains gets a line of its own. Before this, with `.codex/skills/lint` and `.agents/skills/lint` and `packs: ["codex", "pi"]`, `pi` was sent `codex`'s `lint` beside the one it reads | 2026-09-27 | [`project_dirs`](pack-system.md#project_dirs), risk R4 | ✅ `debb2d98` |
| <a id="ws-d14"></a>WS-D14 | *Implementation decision.* An agent that natively reads a workspace skill under a name a higher layer delivers to it sees both, which no layer order reaches; the launch says so — one line per name and directory, naming the agents and who took the name — and yolo still stages its own skill there. This is how the design's fifth done criterion is kept for those agents: the mirror never shadows, and native reading is disclosed rather than presented as covered | 2026-09-27 | [OQ-WS2](#oq-ws2), risk R2 | ✅ `debb2d98` |
| <a id="ws-d15"></a>WS-D15 | *Implementation decision.* The reader READS by a walk from the workspace root's own descriptor, one component at a time, every open `O_NOFOLLOW` and `O_NONBLOCK` (and `O_DIRECTORY` for a directory), along the real path the `os.Root` classified; the `os.Root` only classifies. It follows a link that stays inside it, so a file flipped to a link into `node_modules` between its lstat and its open copied the host's per-side bytes into the staging, breaking [OQ-WS1](#oq-ws1)'s "no authority the repo lacks" (P5 held: nothing outside the root was ever read). A swapped entry is refused as changed while it was being read | 2026-09-27 | [the workspace layer](#the-workspace-layer) | ✅ `c011321e` |
| <a id="ws-d16"></a>WS-D16 | *Implementation decision.* A refusal's reason is fixed text, and for a system error only the errno's own description — never the error's message, whose path can be a link's target, a newline and a counterfeit disclosure line included. The launch also quotes a reason that is not plain text, as it does a name, because the per-side reason names a path the repo's own `mise.toml` can set | 2026-09-27 | [the workspace layer](#the-workspace-layer) | ✅ `c011321e` |
| <a id="ws-d17"></a>WS-D17 | *Implementation decision.* A clone cannot fail a launch through the scratch copy either. An entry whose path inside its skill passes 512 bytes is refused: the reader reaches any depth one component at a time, while the copy is written by absolute path under the scratch tree, a compose dir, a staging dir and on `macos-user` the sandbox home, and macOS's `PATH_MAX` is 1024 — a `git clone` of a path past `PATH_MAX` failed every launch and attach of its workspace with `ENAMETOOLONG`. A write into scratch that fails refuses that entry. The one error left is failing to create the scratch root, and nothing the layer made outlives its call | 2026-09-27 | [the workspace layer](#the-workspace-layer) | ✅ `c011321e` |
| <a id="ws-d18"></a>WS-D18 | *Implementation decision.* [OQ-WS7](#oq-ws7)'s cap is **32 MiB and 4096 files and directories per launch**, counted over everything the layer writes into scratch, and the refusal names the skill, the cap and what the skill would add. The numbers come from the largest real skill sets available on 2026-09-28, counting regular-file bytes with links followed and entries as files plus directories: yolo's built-in skills are 21,770 bytes in 5 entries; two personal skill packs, 217,524 bytes in 29 and 199,410 in 22; one user's composed `~/.claude/skills`, 437,859 in 55; the largest single plugin skills dir in Claude's official plugin marketplace, 488,003 in 76; and all eighteen of that marketplace's plugin skills dirs taken as ONE set, which no repository carries, 1,389,298 bytes in 210 entries. The caps sit about 24× and 19× above that last figure, which leaves room for a vendored tool of a few megabytes. No shipped pack carries skills of its own. Four mechanism choices: (1) **stage, then commit**: each skill is copied into a directory of its own and joins the layer only once the whole of it is there, so a refused skill leaves nothing behind and the skills before it stay; (2) **every write is charged before it is made**, a file its size before a byte of it is read, so a skill refused on one oversized file costs nothing more, and a file that grows past its charge is refused as changed while it was being read; (3) **the walk stops at the crossing**, so what a skill "would add" is a lower bound (what it had copied, plus the write that crossed): counting the rest would cost the reads the cap exists to prevent; (4) **the charge never falls**: a refused skill's partial copy stays spent, so a later skill that still fits is delivered, but a clone of many skills each just past the cap cannot make the host write the cap once per skill. The bound on one launch is therefore the cap into scratch plus the cap into each destination (risk R10). macos-user stages through the same `PrepareSkillsWith` call, so the cap applies there too | 2026-09-28 | [OQ-WS7](#oq-ws7), risk R10 | ✅ `60ceac43` |
| <a id="ws-d19"></a>WS-D19 | **Superseded by [WS-D24](#ws-d24), 2026-11-07: removed with the host half.** *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible.* **The host half is B, one step of `yolo host -- <agent>`, and nothing else.** It is the launch's last write, made just before the hand-over on both of its paths: after every pre-flight, the OpenAI prelaunch and every launch-owned service and doorway, so a launch refused at any of them, a bridge that cannot bind included, writes nothing into the workspace. It is a no-op, and silent, in a jail ([OQ-WS4](#oq-ws4): the mirror alone there), in a directory that is or holds the credential boundary (`paths.WorkspaceScopeBreach`: the home itself), and for a program no selected pack gives a skills destination declaring `project_dirs`. `yolo host apply` and `yolo host env` never write it: the cwd selects nothing for a render ([OQ-WS5](#oq-ws5)), and `env` launches nothing | 2026-10-04 | [OQ-WS5](#oq-ws5), [the host half](#the-host-has-no-workspace-skills-layer) | ❌ removed by WS-D24 |
| <a id="ws-d20"></a>WS-D20 | **Superseded by [WS-D24](#ws-d24), 2026-11-07: removed with the host half.** *Implementation decision, under the same delegation; reversible.* **One link, at the agent's first declared path, to the first source present.** When no path in the destination's `project_dirs` exists in any form (a directory, a committed link, a file, a link out of the tree), the launch writes a RELATIVE symlink at the first of them (`.codex/skills -> ../.claude/skills`). Its target is the first source present in the jail's own order ([WS-D2](#ws-d2), through `run.WorkspaceSkillDirs`), less the agent's own paths and less any link yolo itself recorded for another agent; every later source present is named as not handed to this agent, since a link points at one directory (a second spelling of the chosen one, a committed link to it, is not another source), which is the host half's stated weakness of B against the mirror. A missing parent (`.codex/`) is created and recorded, and the link and its parents are written by a walk from the workspace root in which every directory open is `O_NOFOLLOW`, so a parent the repository made a link refuses the write instead of being written through | 2026-10-04 | [the host half](#the-host-has-no-workspace-skills-layer) | ❌ removed by WS-D24 |
| <a id="ws-d21"></a>WS-D21 | **Superseded by [WS-D24](#ws-d24), 2026-11-07: removed with the host half.** *Implementation decision, under the same delegation; reversible.* **P5 fails closed: the source is checked by the mirror's own reader before a link names it.** `jailcontent.CheckWorkspaceSkillSource` runs the staging reader over that one directory, and any refusal (a link out of the workspace, a FIFO, the [OQ-WS7](#oq-ws7) cap) writes no link and removes a link yolo wrote before. Each refused entry is named by its workspace path and fixed reason through the jail's `displaySafe`, never by a link's target | 2026-10-04 | [P5](#ws-p5) | ❌ removed by WS-D24 |
| <a id="ws-d22"></a>WS-D22 | **Superseded by [WS-D24](#ws-d24), 2026-11-07: removed with the host half.** *Implementation decision, building [OQ-WS6](#OQ-WS6) as decided on its leaning (a); open to the maintainer's revision.* **One `.gitignore` line, once, in a git work tree.** Whether the workspace is in one is an `Lstat` of `.git` at the directory and every directory above it: git is never run, since a repository's `.git/config` can name a program git runs. The line is `/<link>`, appended on a line of its own to the `.gitignore` of the directory the launch ran in, which must be a regular file and is opened `O_NOFOLLOW` and `O_NONBLOCK`. A line already there is left alone, and so is its later removal: a line that stood in `.gitignore` at a launch that placed or kept the link, whether yolo wrote it or a teammate's commit or the user's own edit put it there, and that is gone at a later launch, is not added back. The record's `ignore_seen` is the record the 2026-09-24 note asked for, and that launch says `git status` will list the link without saying who wrote the line; so does a launch whose `.gitignore` is a link, not a regular file, or larger than the 1 MiB yolo reads, and one that cannot open it for the append, whose line names the errno. The record outlives the link: a line the user removed stays out when the link goes and comes back | 2026-10-04 | [OQ-WS6](#OQ-WS6) | ❌ removed by WS-D24 |
| <a id="ws-d23"></a>WS-D23 | **Superseded by [WS-D24](#ws-d24), 2026-11-07: removed with the host half.** *Implementation decision, under the same delegation; reversible.* **A link is yolo's only by a record, and every launch says what it did.** The record is a file under yolo's state dir (`paths.HostWorkspaceSkillsDir`), named by a digest of the workspace's resolved path and the link's path, never in the workspace's `.yolo/`, which the repository's agent can write. A link is refreshed or removed only while its `readlink` equals the record. It is removed, with any parents yolo made that are left empty, when its source vanishes, when another path the agent reads appears, or when the check refuses its source; an unrecorded link at the same path is the repository's and is untouched. Each launch prints, after a `yolo host: workspace skills:` prefix, what it placed, kept, refreshed or removed, the sources it did not hand the agent, every refusal, the ignore line, and each skill name the agent's home-scope directory also has. A line saying yolo could not do something also names the next step: make a linked parent a real directory or create the link by hand, remove a link it could not refresh, fix the temporary folder its check copies into, or add the ignore line by hand. No line claims the agent reads the link: whether `codex`, `copilot`, `opencode` and `agy` follow a symlinked project skills directory is unmeasured ([where each agent reads skills](#where-each-agent-reads-skills)) | 2026-10-04 | [the host half](#the-host-has-no-workspace-skills-layer) | ❌ removed by WS-D24 |
| <a id="ws-d24"></a>WS-D24 | *Owner's ruling.* **The host has no workspace skills layer.** `yolo host -- <agent>` writes nothing into the workspace for skills: no link, no `.gitignore` line, no record. The owner: *"maybe the answer is just for yolo host we don't try to help with this I don't see any way around that because everything else is worse."* and *"this agent skill stuff and the gitignore … those aren't features that were opted in. They create files that are left around that are hard to put in ignore rules"*. The link was a surprising file a launch created in the user's repository, and it could not be reliably ignored because which agent's path is canonical is not knowable. Supersedes WS-D19 to WS-D23 and [OQ-WS6](#OQ-WS6)'s 2026-10-04 decision, which were agent decisions under delegation. Nothing a past launch wrote is removed: no silent edit to a user's repository | 2026-11-07 | [the host has no workspace skills layer](#the-host-has-no-workspace-skills-layer) | ✅ the host-link code and `paths.HostWorkspaceSkillsDir` deleted; `TestHostLaunchWritesNothingIntoTheWorkspaceForSkills` |

## Refresh — a live jail sees host edits

The refresh runs on **every** `yolo` invocation, fresh launch *and* attach-to-running, so
editing a host briefing file, a skill, or `agents_md_extra` propagates into an already-running
jail the next time any `yolo` command is run against it.

This works only because the refresh **preserves inodes**: the write truncates the existing
file in place, and the skills refresh clears *inside* the staged directories rather than
recreating them. A file→file bind mount is pinned to the inode it captured at container start,
so if either write path switches to unlink-and-recreate, running jails silently stop seeing
refreshes. The general form of the rule is
[`jail-home.md`](jail-home.md#invariants).

Composition happens **before the container exists** and before provisioning runs. That is why
the provisioning-failure signal is a conditional section pointing at the provisioning log
rather than an inline error: the briefing is written before any failure can have happened, and
the read-only mount means nothing in-jail can append to it afterward.

### The handoff is read before the loop and consumed after it

The one-time handoff pointer is **read** before the write loop and **consumed** after it, and
the split is load-bearing: consuming a handoff that was never written anywhere burns it for
good. A jail with no briefing destination writes zero briefings, and an unconditional consume
ate the pointer on exactly the launch that could not deliver it. Consumption is therefore
gated on at least one briefing having actually been written. See
[`host-to-jail-handoff.md`](host-to-jail-handoff.md).

## Customizing, in practice

- **All jails, one destination:** edit the host-level file that destination's `after` names.
  Prepended in every jail, and live-refreshes. Where `after` names the destination itself, as
  every shipped agent pack's does, on a machine where `yolo host apply` has run put it in the
  local pack instead; an `after` naming a file of its own (`host:mine.md`) opens that destination
  at the host as well.
- **All jails, every destination:** `agents_md_extra` in the user config.
- **One workspace:** `agents_md_extra` in the workspace config, or the repo's own checked-in
  project-level file, which yolo does not touch. A repo's skills under any agent's project path
  reach every agent in the jail through [the workspace layer](#the-workspace-layer).
- **One session:** write the handover pointer in the workspace state dir; it is surfaced as a
  **Handoff** section on the next launch and consumed once that briefing is written.
- **Sharing one corpus across agents:** ship it as a pack. Per-agent copies drift, which is
  precisely why the host notch moves the user's own prose and skills into the local pack and
  regenerates every destination from there.

## Gotchas

- The briefing describes the jail as configured **at generation time**. Config edits mid-session
  refresh the text on the next invocation, but the running container's actual mounts and limits
  do not change until restart — so the text can be ahead of reality.
- In-jail skill directories are read-only by the same mechanism. Skill development happens in
  the workspace tree: a skill under a declared project path reaches every agent's home-scope
  dir at the next invocation, as the lowest layer, and is promoted host-side (into a pack) when it
  should reach every workspace.
- A prepended host briefing is unrelated to that agent's host **settings** file, which is a
  separate `reads-host` grant composed into the agent's settings rather than into prose.

> [!WARNING]
> **`BriefingContent` is not golden-pinned.** No test asserts its full output — the tests cover
> the helpers, the header's lines, the core's size, and individual topics and facts. So a line
> can be added or removed without regenerating a golden, and only the budget tests notice growth.
> Do not rely on a golden that is not there; if a change must not move the rendered surface, pin
> the line it touches.

## What this does not license

- **Not** a briefing destination that core knows about. Destinations are pack declarations, and
  a jail with no packs writes no briefing.
- **Not** a fact in the briefing that was read from config rather than from what the launch
  applied. Every one of those has been a defect.
- **Not** a probe in the composer. It runs on attach, where it must be deterministic.
- **Not** a fourth skills layer that reads a generated destination.
- **Not** an inline manual. Anything a `--help` or `yolo config-ref` answers is a pointer, not
  a copy.
- **Not** a cap, trim or reorder of user prose, and **not** a config key for any budget. The
  budgets bind yolo's own text, and a test owns each number.
- **Not** a declared size limit for an agent whose limit was not measured.
- **Not** a `yolo guide` or `yolo docs` command: it would be a second renderer of the topic files'
  facts, from a binary frozen at launch while the skill refreshes at each attach.
- **Not** per-project scoping. A rule that matters in one repository belongs in that
  repository's own instruction file, which needs nothing from yolo.
- **Not** an audience for `agents_md_extra`. It is the user's own config key; prose that should
  be addressed goes in the local pack instead.
- **Not** a way for a pack to write where it could not already write. An audience narrows what a
  pack's own content reaches, and a pack that skips a destination does not become an owner of
  it.
- **Not** a denylist, and **not** an audience keyed by pack slug. A slug is a fetch-address
  artifact that a config entry can rename, so a reference to it can break from a line the
  referencing pack cannot see.

## Current values

Verified at `7ad8358c`; the separator, extra-prose, budget, port-list and size-limit rows at
`cf97ab226`. The prose above explains what each of these is for; this table is the
only place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Staging directory, per jail | `<machine storage>/agents/<container-name>/` | `paths.AgentsDir`, `jailcontent.PrepareSkills` |
| Briefing staging filename | `briefing-<RFC 6901-escaped destination>` | `run.briefingStagingName` |
| Skills staging subdirectory | `skills-<pack>` | `jailcontent.SkillStagingName` |
| Host-briefing prepend selector | `after: "host:<home-relative path>"` on a `briefing` contribution | `packdecl` (`Contribution.After`), `run.briefingHostOverlay`; at the host, `entrypoint.hostBriefingOverlay` |
| Ownership record gating the prepend | the host-briefing manifest under the user's config dir | `entrypoint.HostBriefingManifestPath`, `HostBriefingOwner` |
| Pack prose sources | every `*.md` directly inside a pack's `briefing/`, plus any file a `from` names; one governing contribution each | `packload.GovernedSources`, `run.packBriefingProses` |
| Per-pack label (off by default) | `<!-- from pack: NAME -->`, when `briefing_provenance: true` | `jailcontent.ComposeBriefingSections` (both notches); `config.BriefingProvenance` |
| Host base body | the host confinement header alone; the user-scope `agents_md_extra` goes in the user block | `jailcontent.HostBriefingBase`, `config.AgentsMDExtraUser` |
| Block separator | `---` between the user block and yolo's block, only when both are non-empty | `jailcontent.ComposeBriefingFile` |
| Extra-prose config key | `agents_md_extra` (string; user or workspace scope) | `jailcontent.ComposeBriefingFile` (`BriefingParts.Extra`); `yolo config-ref` |
| Budgets | core 1,800 bytes; yolo's Claude reference block 2,600; shipped pack prose 250 per destination; shipped skill descriptions 200 | `coreBudget`, `yoloBlockBudget`, `shippedProseBudget`, `skillDescriptionBudget`, `shippedSkillDescriptionBudget` (tests) |
| Core port list | at most four entries, then a count | `jailcontent.corePortListMax` |
| Declared size limit | `warns_above_chars = 40000` on the claude pack's `.claude/CLAUDE.md` destination, and no other | `packs/claude/pack.toml` |
| Built-in skills | one embedded suite | `internal/jailcontent/builtinskills` (`FS`) |
| yolo's own LSP plugin | written last into every skills destination when `lsp_servers` is non-empty | `jailcontent.LSPPluginDir`, `writeLSPPlugin` |
| Audience fields | `agent` (a destination's identity), `agents` (a contribution's audience); accepted on `briefing`, `skills` and `files` only | `packdecl.Contribution` |
| Shipped agent identities | each equals the pack's `program` bin; for `omp` that is `oh-omp`, not the pack name | `packs/*/pack.json` |
| Unknown-name skip | skipped and disclosed at the launch, `yolo host apply` and `yolo check` (a `[SKIP]` row); never refused; not in `yolo pack lint` | `packload.AgentAudienceSkips` |
| One-owner gate | fatal at the launch pre-flight, `yolo host apply` and `yolo check` | `packload.AgentNameCollisions` |
| Skill-name collision gate | fatal at the launch pre-flight (an attach included) and at `yolo host apply`; one message at both | `hostskills.Collisions`, `hostskills.CollisionError`, `run.checkSkillCollisions` |
| Unmatched-audience report | a `no effect` warning at exit status 0, from `yolo host apply` only | `cli.reportInferredDestinations`, `packload.Destinations` |
| Footprint target of an addressed or broadcast contribution | `→ <agents>`, or `→ every agent` | `packload.audienceTarget` |
| Provisioning-failure marker | a known string in `<workspace>/.yolo/logs/startup.log` | `jailcontent.ReadProvisioningFailed` |

## Why it's this way

Forward-facing rulings a maintainer would otherwise undo, with their original ids. The `R` ids
are the audience model's (the design called them risks, and each became a ruling when it was
built). They are prefixed `BA-` in the anchors because other documents use bare `R1`–`R5` for
their own rules. The audience model's principles `P1`–`P5` are in the body, under
[The audience principles](#the-audience-principles), anchored `ba-p1` to `ba-p5`.

| ID | Ruling | Why it stays |
| :--- | :--- | :--- |
| <a id="s3"></a>[`S3`](#s3) | Neither briefings nor skills read yolo's own generated host output back in | Once the host notch composes a destination wholesale, reading it back in composes every pack twice — measured, byte-identical duplicates in prose, and last-writer-wins invisibility in skills. The user's own content reaches a jail through the conventional local pack instead, which is an ordinary pack entry with ordinary precedence. The in-jail read-back refusal on the host prepend is the same rule at the nesting boundary. |
| <a id="oq-ba1"></a>[`OQ-BA1`](#oq-ba1) | The audience is keyed by **launcher command (`bin`)**, never by pack slug ([P1](#ba-p1)) | A slug can be renamed per config entry, so a reference to it breaks from a line the referencing pack cannot see. The bin namespace is exclusive by construction, and it is what the user types. |
| <a id="oq-ba2"></a>[`OQ-BA2`](#oq-ba2) | The audience match is against a **declared** string, never anything derived | A derived identity (a pack name, a path segment, the pack's bins) would silently change meaning the moment either was renamed, and nothing would report it. The profile chain's `packload.binOwner` finds whose launch a CLI name is, through sole-owned `program` bins; the audience match does not use it. |
| <a id="oq-ba3"></a>[`OQ-BA3`](#oq-ba3) | **Allowlist only**, drawn from the ENABLED packs, and anything else is skipped and disclosed ([P3](#ba-p3); "fatal" until [FS-D6](../design/pack-files-for-agent-state.md#FS-D6) amended it) | yolo cannot tell a typo from an unselected agent, so both are skipped the same way. A denylist relieves a burden that P3 already bounds, and would need its own answer for an excluded agent that is not enabled. |
| <a id="oq-ba4"></a>[`OQ-BA4`](#oq-ba4) | **`skills` takes the same fields and every rule unchanged** | The two kinds are parallel: a conventional source, many packs merging into destinations agent packs name. One mechanism, not two. A second, different audience rule for skills would be the drift. |
| <a id="oq-ba5"></a>[`OQ-BA5`](#oq-ba5) | The fields are spelled **`agent`** (identity) and **`agents`** (audience), and the value is still the bin | The spelling follows what users call a launcher command and what a config surface already calls its owner. Renaming them to `bins` or `for` would change nothing about the namespace and break every manifest. |
| <a id="oq-ba6"></a>[`OQ-BA6`](#oq-ba6) | The identity is **declared by the agent pack that owns the name**, and two packs claiming one name is **fatal**, through a pass of its own | The generic collision loop skips kinds that merge, which `briefing` and `skills` do by design, so folding this into it makes the collision invisible. |
| <a id="oq-ba7"></a>[`OQ-BA7`](#oq-ba7) | Ownership is **per NAME, across kinds** ([P5](#ba-p5)) | `claude-official` and `claude-matt-fork` both launch as `claude` and cannot both be selected. A per-kind key would let one own the briefing and the other the program. `-p claude=<profile>`, `use_profiles.claude` and `agents: ["claude"]` would then each resolve to whichever declaration they happened to read. |
| <a id="oq-ws1"></a>[`OQ-WS1`](#oq-ws1) | **The workspace may contribute skills**, in containers and on macos-user, at the lowest layer, with escaping symlinks refused ([P5](#ws-p5)) (2026-09-27) | The repo already reaches every agent by shipping every path, so the mirror grants no authority it lacked; it only spares the reader who brought another agent. |
| <a id="oq-ws2"></a>[`OQ-WS2`](#oq-ws2) | **The workspace is the lowest layer**: it adds names and never shadows a built-in, shared-pack or local-pack skill, and every shadowed name is disclosed (2026-09-27) | It is the one source a clone populates and an agent edits, so it must never be able to replace a jail-management skill such as `configuring-the-jail`. |
| <a id="oq-ws3"></a>[`OQ-WS3`](#oq-ws3) | **The source set is every shipped agent pack's declared project-scope path, whether or not the pack is selected**; core names none (2026-09-27) | The maintainer: *"I want it to be from the world of agents. Like if I clone an open source project and I trust that person, like I still want these skills."* Keying on the selected packs would miss a repo whose convention belongs to an agent nobody here runs. |
| <a id="oq-ws4"></a>[`OQ-WS4`](#oq-ws4) | **The staged mirror alone**, in containers and on macos-user; in-workspace links are a host-notch tool only (2026-09-27) | A mirror writes nothing into the repo ([P4](#ws-p4)). The same ruling closed a question in the deleted `agent-config-packs.md` about opencode's skills gap: yolo does not write into the workspace for skills. |
| <a id="oq-ws5"></a>[`OQ-WS5`](#oq-ws5) | **The host notch is out of v1** (2026-09-27); its one mechanism, links written into the repo, and how they are kept out of git ([`OQ-WS6`](#OQ-WS6)) were deferred with it, built after v1 on 2026-10-04 under the maintainer's delegation ([WS-D19](#ws-d19) to [WS-D23](#ws-d23)), and removed on 2026-11-07 by the owner's ruling ([WS-D24](#ws-d24)) | A host render's content is a function of the user's configuration and installed packs, never of the directory it runs from, so a staged mirror into a real home is ruled out; a host half could only write links into the repo under `yolo host --`, which waits on its own ruling. |
| <a id="oq-ws7"></a>[`OQ-WS7`](#oq-ws7) | **A per-launch cap on what the layer copies**, set where no real skill set meets it; a skill that would cross it is refused whole and named (2026-09-28) | Symlinks cost nothing to commit, so a clone could make a launch write many times its own size into every destination. |
| <a id="ba-r1"></a>[`R1`](#ba-r1) | An addressed contribution that matches no destination of its kind is **reported, not refused** | The addressing pack's `agents` is correct; the fix belongs to the owning pack. Refusing would punish the wrong author, and the fatal half is P3's. Both notches print the report: `yolo host apply` and the jail launch ([two severities](#two-severities-an-unknown-name-is-fatal-an-unmatched-destination-is-reported)). |
| <a id="ba-r2"></a>[`R2`](#ba-r2) | The destination enumeration and the staging-name encoding live in one place each, called by both halves | A mismatch does not fail the launch — podman binds an absent file source happily — so the failure is a *blank briefing*, which nothing reports. Coupling by comment had already let the two drift. |
| <a id="ba-r3"></a>[`R3`](#ba-r3) | Each notch carries its own call-site pin for the audience | The two notches narrow in different places. A test of the shared predicate stays green when either call site is deleted, which is the shape this repo has shipped repeatedly. |
| <a id="ba-r4"></a>[`R4`](#ba-r4) | A destination that declares no identity can be named by no `agents` selector, but still receives every broadcast | It is the state every pack was in before the field existed, so treating it as an error would break every existing pack, and treating it as matchable would deliver addressed prose to a destination that never claimed the identity. |
| [`OQ-PB2`](pack-system.md#oq-pb2) | `AGENTS.md`, `CLAUDE.md` and `GEMINI.md` are never a pack-prose source, at any depth (mirrored here; the ruling is pack-system.md's) | Agent tools read those names as a repository's own instructions, and a pack is usually a repository. Shipping one gave a single file two readers who wanted different things from it. |
| <a id="c2"></a>[`C2`](#c2) | The confinement header derives its enforcement vector from the notch's profile | A header that claims a container for a notch that has none is the dangerous falsehood — an agent treats its home as disposable. Deriving keeps it true for a notch nobody has enumerated yet. |
| <a id="oq-tp9"></a>[`OQ-TP9`](#oq-tp9) | A `briefing` contribution's `after: "host:…"` is **not** origin-gated | A fetched pack's `after` is honored like anyone else's; the gate was removed rather than extended. Prepending the user's own file is not a credential crossing. |

### The briefing diet

Before the diet, yolo's always-loaded text was a manual composed ahead of the user's own rules.
Measured on 2026-11-13 in the maintainer's jail, his Claude briefing was 42,386 bytes, of which
26,867 were yolo's (the jail-managed body and the shipped packs' prose) and 15,519 his, starting
at byte 25,717. An agent that cut a long file from the end cut his rules, and Claude Code warned
about the file's size at every session. The rows below are the briefing-diet design's ledger,
carried here when it graduated; BD-D11 is the owner's ruling, the rest are implementation
decisions the design seat took, each reversible.

| ID | Decision | Pinned by |
| :--- | :--- | :--- |
| <a id="BD-D1"></a>[`BD-D1`](#BD-D1) | The on-demand store is the generated `jail-mechanics` skill with topic files; no new `yolo` command | `TestTheMechanicsSkillHoldsTheTopicsItsIndexLists`, `TestTheCorePointsAtExactlyTheTopicsTheJailHas` |
| <a id="BD-D2"></a>[`BD-D2`](#BD-D2) | A shipped pack's manual is that pack's own skill, named `yolo-<subject>`; its briefing prose is a pointer | `packs/{git,github,claude,pi}/skills/yolo-*`; `TestEveryShippedPacksProseFitsItsBudgetPerDestination` |
| <a id="BD-D3"></a>[`BD-D3`](#BD-D3) | `describes` is accepted on a `skills` contribution, with the meaning it has on `briefing` | `hostskills.ComposeHostSkillsFor`; `TestASkillsTreeAboutAJailOnlyKindIsNotComposedAtTheHost` |
| <a id="BD-D4"></a>[`BD-D4`](#BD-D4) | Budgets: core 1,800 bytes; shipped pack prose 250 per destination; Claude's reference block 2,600; skill descriptions 200. Shipped content only | the tests in [Budgets, and the size line](#budgets-and-the-size-line) |
| <a id="BD-D5"></a>[`BD-D5`](#BD-D5) | Alerts stay always-loaded, at the top of yolo's block, outside the budget | `jailcontent.briefingAlerts` |
| <a id="BD-D6"></a>[`BD-D6`](#BD-D6) | The pointer names the skill file's path for the destination's own agent | `TestThePointerNamesTheDestinationsSkillPath` |
| <a id="BD-D7"></a>[`BD-D7`](#BD-D7) | The `rg -r` warning is rendered only when a block names `rg` as a replacement | `TestTheRgWarningNeedsAnRgBlock` |
| <a id="BD-D8"></a>[`BD-D8`](#BD-D8) | `agents_md_extra` and non-shipped packs' prose form the user block with the host file; the host notch composes the same two blocks | `jailcontent.ComposeBriefingFile` at both notches; `TestApplyHostPutsTheUsersProseBeforeYolosBlock`, `TestAForkedShippedPackIsUserProse` |
| <a id="BD-D9"></a>[`BD-D9`](#BD-D9) | An over-long composed file is disclosed at launch against a pack-declared, measured value; never refused, never trimmed | `packdecl.Contribution.WarnsAboveChars`, `jailcontent.BriefingSizeWarning`, `run.noteBriefingSize`; `TestALaunchWarnsWhenClaudesBriefingPassesItsMeasuredLimit` |
| <a id="BD-D10"></a>[`BD-D10`](#BD-D10) | No size limit is declared for an agent whose limit was not measured; agy's is an owner observation | `TestOnlyTheClaudePackDeclaresAMeasuredBriefingLimit` |
| <a id="BD-D11"></a><a id="OQ-BD1"></a>[`BD-D11`](#BD-D11) | **Owner ruling** (it settled [`OQ-BD1`](#OQ-BD1)): the user block comes first and yolo's block last, so a cut from the end removes yolo's text first (the owner, 2026-11-13: *"yes, put my rules first"*) | `TestTheUsersProsePrecedesYolosCoreInTheJail`, `TestYolosBlockAloneHasNoRuleAndFitsItsBudget` |

Alternatives the design weighed and rejected:

| Alternative | Why not |
| :--- | :--- |
| Move the user's prose first and leave yolo's body as it was | It fixes the cut and leaves about 27 KB of yolo's text in every context, with Claude's warning in place |
| No always-loaded core; everything in the skill | An agent that has not opened the skill does not know `/tmp` is lost or that `localhost` is not the host, and neither mistake produces a refusal that would send it there |
| A `yolo guide <topic>` command instead of topic files | A second renderer of the same facts, from a binary frozen at launch |
| Topic files for `git` and `gh` inside `jail-mechanics` | Core would have to know two packs by name |
| A briefing contribution marked on-demand, staged by yolo into a skill | A pack can already ship a skill; a new kind earns its place when a user pack needs it |
| One static skill instead of the generated one | The removed text is per-jail fact: paths, ports, the active loopholes |

Risks the design named, and what answers each:

| Risk | Answer |
| :--- | :--- |
| An agent does not open the skill and misses a rule that used to be always-loaded | The core keeps every rule whose breach is silent; the rest sit behind a refusal that names its own next step |
| An agent with no skill mechanism cannot find the topics | The pointer gives the file's path |
| A running jail attached by a newer yolo gets a core that points at topics its skill tree lacks | [The inline fallback](#the-inline-fallback) |
| The budget is met by deleting facts | Every former body section has a topic file, and a budget failure names moving text, not cutting it |
| A user pack ships a skill named `yolo-git` | The skill-name collision refusal names both packs and the two remedies |
| An older yolo reads a pack with `describes` on `skills` or `warns_above_chars` | v0.17.1 refuses the first and skips the claude briefing destination for the second; both are listed in `knownReleaseBreaks` (`packs/releasedecode_test.go`) with the attach guard that keeps an older jail on its own staged tree |
