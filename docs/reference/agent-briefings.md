---
status: current
verified: 2026-09-23
verified_commit: 7ad8358c
covers:
  - internal/jailcontent/briefing.go
  - internal/jailcontent/skills.go
  - internal/jailcontent/write.go
  - internal/cli/run/prepare.go
  - internal/cli/run/briefingdest.go
  - internal/cli/run/briefingreadback.go
  - internal/cli/run/unmatchedaudience.go
  - internal/cli/run/backendcaps.go
  - internal/entrypoint/hostbriefing.go
  - internal/packload/agentaudience.go
  - internal/packload/mergedest.go
tags: [briefings, skills, packs, agents-md, staging, audiences]
summary: "Where the text an in-jail agent reads at session start comes from: the four composition parts, the audience model that decides what varies per destination (the agent/agents fields, the fatal unknown-name gate, one owner per agent name), the per-destination staging file and its injective name, the read-only mount that makes an in-jail edit fail, and the inode-preserving refresh that lets a live jail see host edits."
---

# Agent briefings — the text an agent reads at session start

**Status:** CURRENT as of 2026-09-23, verified against `7ad8358c`. MEASURED: the audience
model runs end to end in a real container in CI's integration jobs, which were green at that
commit.

Every coding agent reads an instruction file at session start. yolo **composes one per
destination**, host-side, on every invocation, stages it under a per-jail directory, and
bind-mounts it read-only where the agent expects it. Which destinations exist is entirely
pack data: a pack's `briefing` contribution names its own `into` path, and a jail with no
packs gets no briefing at all.

Skills ride the same staging area and the same read-only delivery, so they are covered
here too.

| Component | Lives in |
| :--- | :--- |
| Composition of the jail-managed body | `internal/jailcontent` (`BriefingContent`, `BriefingInput`, `confinementHeader`, `enforcementLines`) |
| The three composition steps around it | `internal/jailcontent` (`ComposeBriefing`, `ComposePackBriefings`, `PrependHostBriefing`) |
| The inode-preserving write | `internal/jailcontent` (`WriteBriefing`) |
| Skills staging and its layering | `internal/jailcontent` (`PrepareSkills`, `SetPackSkillDirs`, `SetPackSkillTargets`, `SkillStagingName`, `PackSkillSource`, `SkillTarget`) |
| The per-invocation refresh, and what it feeds the composer | `internal/cli/run` (`refreshJailBriefings`, `briefingPortsFor`, `briefedResourceLimits`, `briefingLoopholes`) |
| The one enumeration of destinations and staging names | `internal/cli/run` (`briefingDestinations`, `briefingStagingName`, `briefingDest`) |
| A pack's prose entries | `internal/cli/run` (`packBriefingProses`) |
| The in-jail read-back refusal on the host prepend | `internal/cli/run` (`mayPrependHostBriefing`) |
| The audience fields and their validation | `internal/packdecl` (`Contribution.Agent`, `Contribution.Agents`) |
| The fatal unknown-name gate, and the agent vocabulary | `internal/packload` (`AgentAudienceProblems`, `AgentNames`) |
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
> **Host dotfiles outside these paths are bypassed and inert.** Managing dotfiles into `~/.pi/agent/APPEND_SYSTEM.md` or `~/.claude/CLAUDE.md` on the host does **not** project into `/home/agent/` inside the jail. Inside the jail, `/home/agent/.pi` (and similar agent state directories) is an isolated per-workspace state overlay (`<workspace>/.yolo/state/pi`), completely decoupled from host home storage. Ad-hoc agent-specific files like `APPEND_SYSTEM.md` are not mounted into the jail, nor are they read by YOLO's host prepend (which looks strictly for unmanaged `~/.pi/agent/AGENTS.md`). To deliver durable instructions across host and jails, use a pack briefing audience.

**There is no agent registry and no config key naming agents.** Which destinations get a
briefing follows from which packs are selected, and nothing is selected by default.

## What a composed briefing contains

Four parts, in the order they appear in the finished file:

**1. The host user's own briefing, prepended — and only if it *is* the user's.** Driven by
the declaring contribution's `after: "host:<path>"`. If that host file exists **and yolo did
not compose it itself**, its content comes first, separated from the rest by a `---` rule.
This is how a user's global instructions reach every jail *on a machine where the host notch
has never run*. The mapping is filename-exact: a `CLAUDE.md` destination reads `CLAUDE.md`,
and variants like `CLAUDE.local.md` are not picked up.

> [!WARNING]
> **The jail must not read yolo's own host output back in.** Once `yolo host apply` made the
> host destination a file yolo composes *wholesale*, `after: "host:.claude/CLAUDE.md"` named
> yolo's own output: the jail prepended a file already holding every pack's prose and then
> composed the same packs again at part 4, so each pack section appeared **twice**,
> byte-identical. The prepend is gated on **ownership proved from a record** —
> `GeneratedHostBriefings`, reading the same manifest `yolo host apply` writes — never
> inferred from content, because a file that merely *looks* composed is still the user's. It
> **fails open**: an absent or unreadable record prepends as before, because dropping the
> user's instructions from their jail is a worse failure than repeating a pack's prose. On a
> machine that *has* run the host notch, the user's prose arrives through the conventional
> local pack at part 4 instead — the same route the skills half took.
>
> **Inside a jail the record is not enough.** There, "home" is the outer jail's home, and every
> briefing destination in it is a read-only bind of the *outer* launch's staged file. That is
> yolo's output too, and the host-apply record cannot know about it. So a nested launch also
> refuses to prepend a file that is one of the selected packs' briefing destinations. Without
> that rule a nested jail's briefing held the outer one's entire briefing, every heading twice.
> Every shipped agent pack declares `after` equal to its own `into`, so this was every nested
> jail. A prepend source that is not a destination is still prepended in a jail.

`after: "host:…"` is **jail-only**: at the `yolo host apply` notch the path it names *is* the
generated destination, so the host render ignores it outright. It is no longer origin-gated —
a fetched pack's `after` is honored like anyone else's.

**2. The jail-managed body** — one document describing this specific jail, deliberately
limited to what an agent *cannot* discover through its own mechanisms, with inline manuals
replaced by pointers (`yolo --help`, `yolo config-ref`, `yolo-cglimit --help`) and
conditional sections that appear only when their data exists. Emission order, from
`BriefingContent`:

1. **The confinement header** — see [Confinement](#confinement).
2. **⚠ Provisioning failed** — conditional, when the provisioning log records a failure.
   Refreshed every invocation, so it appears on the next attach after a failed boot.
3. **Handoff** — conditional, when a fresh handover pointer is carried this launch.
4. **Environment** — workspace, home, OS, the network paragraph, the two port sections, and
   the resource limits the backend actually imposes.
5. **The `rg --replace` trap** warning.
6. **What this environment does NOT do for you** — conditional, the backend's own
   limitations. Placed **before** the capability sections deliberately: these are
   constraints that change how everything below them should be read, and a constraint
   discovered after the capability it qualifies has already been read too late.
7. **Loopholes** — conditional, the actual active set by name rather than an instruction to
   enumerate.
8. **Blocked Tools** — conditional, from the blocked-tool config merged with what packs
   contribute.
9. **Additional Context Mounts** — conditional, and filtered to the mounts the backend will
   actually bind.
10. **Limitations**, **Packages & Resource Limits**, **Skills** — the three standing
    sections. On a backend with no container the middle one is **Packages**: it offers no
    resource cap and says outright that `resources` is not enforced there.

There is no tool inventory and no MCP listing: agents read their own generated config.

**3. `agents_md_extra`**, appended verbatim — a config key for injecting arbitrary extra
instructions into every generated briefing, legal at user or workspace scope.

**4. Each selected pack's prose that this destination's audience admits**, appended last, in
config order, **unlabelled** — one blank line between packs and nothing else. Within a pack its
files are ordered by pack-relative path, byte-wise, and joined with the same one blank line, so a
pack's prose reads as one section. Empty prose is skipped rather than emitting an empty section.
What a pack ships is its `briefing/` directory and any file a contribution names with `from` —
never a root `AGENTS.md`, `CLAUDE.md` or `GEMINI.md`, which is the pack repository's own
([`pack-system.md`](pack-system.md#what-a-pack-is-on-disk)).

**`briefing_provenance: true` labels each pack's section** with `<!-- from pack: NAME -->`, once
per section however many files it joins, as a debugging aid. It is off by default, for two measured reasons:

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

The jail notch's own header bytes are **byte-identical to their historical form** and pinned by
test, deliberately: every jail that boots today renders it, so adding detail there would move a
rendered surface for every existing user to say something the next two lines already say. The
derived enforcement tail is appended on the guest, host and unrecognized paths only.

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
`-p <name> -- <bin>` and `use_profiles.<cli>` already key on. It is never the pack slug. The
fields are *spelled* `agent` and `agents` because users think of a launcher command as an agent,
and because a config surface already names its owner with `"agent": "pi"`. Singular is the
identity a destination declares; plural is the audience a contribution names. The validator puts
both through the same guard as `bin` (`packdecl`'s `binProblem`), so the two namespaces cannot
drift apart.

<a id="ba-p2"></a>**P2. Scoping is opt-in, and silence means broadcast.** A contribution with no
audience reaches every destination of its kind. An empty identity is the other half of the same
rule: a destination that declared none still receives every broadcast, and no addressed content.
This is what let the field land before any pack adopted it. It also makes a broadcast portable,
because listing every agent by name is fatal in any jail that does not select one of them (P3).

<a id="ba-p3"></a>**P3. The vocabulary is the ENABLED packs, and anything else is fatal.** An
`agents` entry may name only an agent some pack in `packs` claims. There is no second, laxer tier
for a name that exists somewhere but is not selected here. `agents: ["cloude"]` and
`agents: ["codex"]` in a jail without codex fail identically, because from the jail's point of
view they are the same mistake: the prose names an audience this jail does not have. The remedy
is the same too: fix the name, or select the pack.

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

#### Two severities: an unknown name is fatal, an unmatched destination is reported

An addressed contribution can go wrong in two ways. The severity splits where the user's remedy
splits:

| Question | Decidable from | Severity | Whose fix |
| :--- | :--- | :--- | :--- |
| Is the name in the vocabulary? Does any enabled pack claim `claude` at all? | the claim set alone | **fatal** (P3) | the addressing pack: fix the name, or select the pack |
| Did the name reach a destination of *this kind*? The pack owning `claude` is here, but does it declare a `skills` destination with `agent: claude`? | resolution | **reported**, never refused | the *owning* pack: an `agent` on its destination of that kind |

Refusing the second case would punish the wrong author, because the addressing pack's `agents` is
correct.

**The fatal gate is `packload.AgentAudienceProblems`.** Its vocabulary is `packload.AgentNames`
over the **loaded** packs only, never the embedded universe. It runs at the two points that hold
the enabled set: the launch pre-flight (`run`'s pack loading, which also covers attach) and
`yolo host apply`. The refusal names the offending string, the declaring pack, every agent the
`packs` provide, and a did-you-mean guess. It prints one message for a typo and for an unselected
pack, because it cannot tell which mistake was made. The wording is notch-neutral ("no pack in
`packs` provides"), because one of its two callers runs where there is no jail.

`yolo pack lint` does **not** run this gate, and must not. Lint takes a single pack root with no
config, so it cannot know the enabled set. It reports what a contribution targets and refuses
nothing. The severity lives upstream, where the user has every remedy. `yolo check` does not run
it either.

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
pack loading right after the fatal gate). The line names the addressing pack, the kind, the
pack-relative sources and the audience, and it says that declaring `into` is not the remedy. It
does not tell the user to select a pack, because the fatal gate has just proved that every name
reaching it belongs to a selected pack. The launch still reads neither `Orphaned` nor
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
> **`requires` is deliberately not a claim.** A content pack asserting `requires claude` beside
> the pack that provides claude is the most ordinary dependency a pack can declare, and a pack
> requiring `fzf` must not collide with another that installs it. That is why `requires` combines
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
  (`packload.GovernedSources`), and
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
is **copied** over the sandbox home, which is a real difference in kind (the copy is writable)
and is recorded in that backend's launch warning rather than papered over.

## Skills

Skills ride the same staging directory, one subdirectory per `skills` contribution, each
mounted read-only at that contribution's `into`. Staging is **rebuilt every invocation**,
clearing contents *inside* each directory.

**Two layers, in this order:** the built-in skill suite, then every selected pack's skills in
config order. A pack may therefore override a built-in — a legitimate reason to ship one — and
because the conventional local pack is appended last among packs, a personal skill still
outranks every shared pack's.

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
> **There is no third layer reading the host's own `~/.<agent>/skills` tree, and adding one
> back is circular.** That layer named "the user's own skills tree" but was set to the
> *destination* — the host's copy of the very path the staging dir gets mounted over. It was
> right while the destination held loose user files and became circular the moment
> `yolo host apply` **composed** it: the jail read yolo's own generated output back in as the
> user's tree, and since the local pack is an ordinary pack entry, its content arrived twice by
> two routes. Invisible only because a flat copy is last-writer-wins. The slot it described
> already has a home — the conventional local pack is the last pack layer — so a personal skill
> reaches the same precedence by the same route every other pack's content takes.

`PrepareSkills` still takes a home directory and an agent-name list; both are vestigial, kept
because its callers pass them and churning those would be a bigger diff than the fix with no
behavior in it.

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
  Prepended everywhere, and live-refreshes — on a machine where `yolo host apply` has run, put
  it in the local pack instead.
- **All jails, every destination:** `agents_md_extra` in the user config.
- **One workspace:** `agents_md_extra` in the workspace config, or the repo's own checked-in
  project-level file, which yolo does not touch.
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
  the workspace tree and is promoted host-side.
- A prepended host briefing is unrelated to that agent's host **settings** file, which is a
  separate `reads-host` grant composed into the agent's settings rather than into prose.

> [!WARNING]
> **`BriefingContent` is not golden-pinned.** No test asserts its full output — the tests cover
> the helpers, plus the jail-notch header bytes and a few individual sections. So a section can
> be added or removed without regenerating a golden, and any claim that "the briefing bytes are
> pinned" is true only of the confinement header. Do not rely on a golden that is not there; if
> a change must not move the rendered surface, pin the section it touches.

## What this does not license

- **Not** a briefing destination that core knows about. Destinations are pack declarations, and
  a jail with no packs writes no briefing.
- **Not** a fact in the briefing that was read from config rather than from what the launch
  applied. Every one of those has been a defect.
- **Not** a probe in the composer. It runs on attach, where it must be deterministic.
- **Not** a fourth skills layer that reads a generated destination.
- **Not** an inline manual. Anything a `--help` or `yolo config-ref` answers is a pointer, not
  a copy.
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

Verified at `7ad8358c`. The prose above explains what each of these is for; this table is the
only place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Staging directory, per jail | `<machine storage>/agents/<container-name>/` | `paths.AgentsDir`, `jailcontent.PrepareSkills` |
| Briefing staging filename | `briefing-<RFC 6901-escaped destination>` | `run.briefingStagingName` |
| Skills staging subdirectory | `skills-<pack>` | `jailcontent.SkillStagingName` |
| Host-briefing prepend selector | `after: "host:<home-relative path>"` on a `briefing` contribution | `packdecl` (`Contribution.After`), `run.briefingHostOverlay` |
| Ownership record gating the prepend | the host-briefing manifest under the user's config dir | `entrypoint.HostBriefingManifestPath`, `HostBriefingOwner` |
| Pack prose sources | every `*.md` directly inside a pack's `briefing/`, plus any file a `from` names; one governing contribution each | `packload.GovernedSources`, `run.packBriefingProses` |
| Per-pack label (off by default) | `<!-- from pack: NAME -->`, when `briefing_provenance: true` | `jailcontent.ComposePackBriefings`, `entrypoint.appendHostBriefingSection`; `config.BriefingProvenance` |
| Host/jail separator | `---` between the prepended host prose and the rest | `jailcontent.PrependHostBriefing` |
| Extra-prose config key | `agents_md_extra` (string; user or workspace scope) | `jailcontent.ComposeBriefing`; `yolo config-ref` |
| Built-in skills | one embedded suite | `internal/jailcontent/builtinskills` (`FS`) |
| yolo's own LSP plugin | written last into every skills destination when `lsp_servers` is non-empty | `jailcontent.LSPPluginDir`, `writeLSPPlugin` |
| Audience fields | `agent` (a destination's identity), `agents` (a contribution's audience); accepted on `briefing`, `skills` and `files` only | `packdecl.Contribution` |
| Shipped agent identities | each equals the pack's `program` bin; for `omp` that is `oh-omp`, not the pack name | `packs/*/pack.json` |
| Unknown-name gate | fatal at the launch pre-flight and at `yolo host apply`; not in `yolo pack lint` or `yolo check` | `packload.AgentAudienceProblems` |
| One-owner gate | fatal at the launch pre-flight, `yolo host apply` and `yolo check` | `packload.AgentNameCollisions` |
| Unmatched-audience report | a `no effect` warning at exit status 0, from `yolo host apply` only | `cli.reportInferredDestinations`, `packload.Destinations` |
| Footprint target of an addressed or broadcast contribution | `→ <agents>`, or `→ every agent` | `packload.audienceTarget` |
| Provisioning-failure marker | a known string in `<workspace>/.yolo/startup.log` | `jailcontent.ReadProvisioningFailed` |

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
| <a id="oq-ba3"></a>[`OQ-BA3`](#oq-ba3) | **Allowlist only**, drawn from the ENABLED packs, and anything else is fatal ([P3](#ba-p3)) | A typo and an unselected agent are the same mistake from the jail's point of view, with the same remedy. A denylist relieves a burden that P3 already bounds, and would need its own answer for an excluded agent that is not enabled. |
| <a id="oq-ba4"></a>[`OQ-BA4`](#oq-ba4) | **`skills` takes the same fields and every rule unchanged** | The two kinds are parallel: a conventional source, many packs merging into destinations agent packs name. One mechanism, not two. A second, different audience rule for skills would be the drift. |
| <a id="oq-ba5"></a>[`OQ-BA5`](#oq-ba5) | The fields are spelled **`agent`** (identity) and **`agents`** (audience), and the value is still the bin | The spelling follows what users call a launcher command and what a config surface already calls its owner. Renaming them to `bins` or `for` would change nothing about the namespace and break every manifest. |
| <a id="oq-ba6"></a>[`OQ-BA6`](#oq-ba6) | The identity is **declared by the agent pack that owns the name**, and two packs claiming one name is **fatal**, through a pass of its own | The generic collision loop skips kinds that merge, which `briefing` and `skills` do by design, so folding this into it makes the collision invisible. |
| <a id="oq-ba7"></a>[`OQ-BA7`](#oq-ba7) | Ownership is **per NAME, across kinds** ([P5](#ba-p5)) | `claude-official` and `claude-matt-fork` both launch as `claude` and cannot both be selected. A per-kind key would let one own the briefing and the other the program. `-p claude=<profile>`, `use_profiles.claude` and `agents: ["claude"]` would then each resolve to whichever declaration they happened to read. |
| <a id="ba-r1"></a>[`R1`](#ba-r1) | An addressed contribution that matches no destination of its kind is **reported, not refused** | The addressing pack's `agents` is correct; the fix belongs to the owning pack. Refusing would punish the wrong author, and the fatal half is P3's. Both notches print the report: `yolo host apply` and the jail launch ([two severities](#two-severities-an-unknown-name-is-fatal-an-unmatched-destination-is-reported)). |
| <a id="ba-r2"></a>[`R2`](#ba-r2) | The destination enumeration and the staging-name encoding live in one place each, called by both halves | A mismatch does not fail the launch — podman binds an absent file source happily — so the failure is a *blank briefing*, which nothing reports. Coupling by comment had already let the two drift. |
| <a id="ba-r3"></a>[`R3`](#ba-r3) | Each notch carries its own call-site pin for the audience | The two notches narrow in different places. A test of the shared predicate stays green when either call site is deleted, which is the shape this repo has shipped repeatedly. |
| <a id="ba-r4"></a>[`R4`](#ba-r4) | A destination that declares no identity can be named by no `agents` selector, but still receives every broadcast | It is the state every pack was in before the field existed, so treating it as an error would break every existing pack, and treating it as matchable would deliver addressed prose to a destination that never claimed the identity. |
| [`OQ-PB2`](pack-system.md#oq-pb2) | `AGENTS.md`, `CLAUDE.md` and `GEMINI.md` are never a pack-prose source, at any depth (mirrored here; the ruling is pack-system.md's) | Agent tools read those names as a repository's own instructions, and a pack is usually a repository. Shipping one gave a single file two readers who wanted different things from it. |
| <a id="c2"></a>[`C2`](#c2) | The confinement header derives its enforcement vector from the notch's profile | A header that claims a container for a notch that has none is the dangerous falsehood — an agent treats its home as disposable. Deriving keeps it true for a notch nobody has enumerated yet. |
| <a id="oq-tp9"></a>[`OQ-TP9`](#oq-tp9) | A `briefing` contribution's `after: "host:…"` is **not** origin-gated | A fetched pack's `after` is honored like anyone else's; the gate was removed rather than extended. Prepending the user's own file is not a credential crossing. |
