---
title: "Jail mechanics — generated guidance for the applied environment"
status: accepted
stage: CURRENT
covers:
  - internal/changetiming/changetiming.go
  - internal/cli/applyhostskills.go
  - internal/cli/run/liveapply.go
  - internal/cli/run/mechanicsfacts.go
  - internal/cli/run/mechanicsrecord.go
  - internal/cli/run/prepare.go
  - internal/cli/run/run.go
  - internal/jailcontent/briefing.go
  - internal/jailcontent/briefingtopics.go
  - internal/jailcontent/builtinskills/
  - internal/jailcontent/mechanicsfacts.go
  - internal/jailcontent/mechanicsrender.go
  - internal/jailcontent/mechanicsstage.go
  - internal/jailcontent/skills.go
tags: [jail, skills, briefings, config-apply, mechanics]
summary: "How yolo derives, renders, stages and refreshes built-in guidance from launch-applied facts across jail, attach, config apply and host apply."
---

# Jail mechanics — generated guidance for the applied environment

**Status:** Implemented. The source and permanent regression tests define the behavior below.

**UNMEASURED:** Native macOS Seatbelt enforcement and a real rootless Linux Podman host have not
been observed. Nested Podman evidence cannot certify those conditions or generic host reachability.
The [native and real-rootless verification plan](../plans/jail-mechanics-verification.md) owns
those observations.

Jail mechanics is yolo-generated guidance about the environment an entry actually created. It
contains a short index, a host-path map, a topic file per subject of the jail, and only the change
recipes relevant to that entry. The
same applied facts inform the jail-managed briefing and the other built-in skills. The host notch renders its host-specific suite from host facts; it does not invent jail paths or
restart steps.

## Principles

- <a id="JM-P1"></a>**JM-P1. State the case that holds.** No rendered sentence names a backend,
  notch or setting this environment does not have, and none says "if" about a fact the launcher
  already knows. This avoids instructions that require an agent to select among situations the
  host already resolved.
- <a id="JM-P2"></a>**JM-P2. The decider writes the sentence.** Each fact comes from the decision
  that makes it, never from a second list. This applies
  [the applied-facts rule](agent-briefings.md#what-the-body-describes-applied-never-configured)
  to every generated fact, not only storage classes.
- <a id="JM-P3"></a>**JM-P3. Applied, never configured.** An attach describes what the running jail
  has after effective live changes, not whatever the workspace config currently requests.
- <a id="JM-P4"></a>**JM-P4. Steps, not explanations.** A recipe tells the agent what to run, what
  to ask a human to run on the host, and what result to expect. The host command's own report
  remains authoritative.
- <a id="JM-P5"></a>**JM-P5. Small at rest.** The skill description is short; an agent that needs a
  change reads the index and the one matching recipe instead of every case.
- <a id="JM-P6"></a>**JM-P6. Generated guidance stays situation-specific.** The maintainer extended
  JM-P1 and JM-P2 to yolo-authored informational content pushed into a jail. The fact-backed
  built-in skills and jail-managed briefing follow that rule; pack- and workspace-authored content
  remains owned by its own source and is not silently rewritten by this renderer.

## The applied fact sheet

A **fact sheet** *(coined in the jail-mechanics design)* is a host-side snapshot of decisions that
an entry applied. It is not the desired config or an agent-writable config record. The run layer
collects already-resolved paths, staged host-file results, package delivery, backend capabilities,
and other launch decisions; `jailcontent.FactSheet` is the input shared by the generated built-in
suite and jail-managed briefing. The fact sheet is the renderer's only runtime input: it consumes
no external files, environment variables or runtime probes of its own.

The shared change-timing classifier supplies the explicit per-key expectation used by apply and
the generated recipes. Each accepted config key has a declared timing row for each backend
context; adding a schema key without a row is a test failure. Runtime observations can narrow the
expectation before rendering. They are not re-probed inside the renderer, and the recipe tells the
agent that the command's actual report wins.

A recipe is one generated file of ordered steps for a kind of change. Recipe presence and its
facts are selected from the current sheet: absent host or jail capabilities do not produce a
conditional branch that asks the agent to decide what might apply.

## Topic files

A **topic file** *(a term coined in the briefing-diet design)* is one file under the skill's
`topics/` that says how one part of this jail works. It is not a recipe: a recipe says what to do
after a change, a topic file describes the environment that holds. The topics are what the
jail-managed body used to say in full; the briefing now carries only a budgeted core, which names
this skill and the topics the jail has
([agent briefings](agent-briefings.md#the-core); [BD-D1](agent-briefings.md#BD-D1)).

| File | Subject |
| :--- | :--- |
| `topics/storage.md` | The workspace, the home, the durable dir and the storage classes |
| `topics/network.md` | The network mode, the host's name, published and forwarded ports |
| `topics/mounts.md` | Additional context mounts, beside `host-map.md` |
| `topics/host-requests.md` | Asking the human: the three rules, the approval menu, `yolo wait`, `yolo host-run` |
| `topics/loopholes.md` | The active loopholes, by name |
| `topics/blocked-tools.md` | Each blocked or guarded tool, with its message |
| `topics/limits.md` | The OS, the credential boundary, resource limits, I/O priority, nix, the backend's own limitations |

- **One renderer.** `jailcontent.BriefingTopics` renders every topic from the briefing's own
  `BriefingInput`, which is in the fact sheet, so a topic and the core cannot disagree about a
  path. The same output is the inline form below.
- **A topic exists only when its subject does** ([JM-P1](#JM-P1)): no context mount, no
  `topics/mounts.md` and no index line for it; no loophole, no loopholes topic; no blocked tool,
  no blocked-tools topic. The host notch has none.
- **The index lists them.** Its Topics list sits above Recipes, one line per file this jail has,
  and its `description` names the subjects so an agent's skill listing matches a storage, network,
  approval or refusal question to it. The description is held to 200 bytes
  (`TestShippedSkillDescriptionsFitTheirBudget`). The index also took the tooling line
  (`yolo --help`, `yolo config-ref`) and the pointers to the other two built-in skills, which the
  briefing used to carry. Pinned by `TestTheMechanicsSkillHoldsTheTopicsItsIndexLists`.
- **The inline fallback.** An entry never writes a core that points at a topic file it did not
  write. When no fact sheet reached it (a legacy attach, which keeps the prior tree), or the skill
  render failed and left the prior tree, the run pipeline sets `BriefingInput.InlineTopics` and
  the topics are appended to the briefing itself under `##` headings, with a pointer that names no
  file ([agent briefings](agent-briefings.md#the-inline-fallback);
  `TestInlinedTopicsCarryEveryFactAndNameNoFile`).
- **No `yolo` command prints them.** It would be a second renderer of the same facts, from a
  binary frozen at launch, while the skill refreshes at each attach. The skill names the commands
  that answer live questions instead, such as `yolo loopholes list`, `yolo config drift` and
  `yolo wait`.

## Lifecycle

### Fresh entry

The launcher makes the fact sheet after the backend's producers have resolved their outputs. The
same refresh constructs the jail-managed briefing and places that applied `BriefingInput` in the
sheet before the generated skills are rendered. This ordering matters for host
files, mounts, package delivery and the selected running pack set: a requested setting is not
reported as delivered until its producer has decided it.

The generated suite contains `configuring-the-jail`, `diagnosing-the-jail` and `jail-mechanics`.
The always-present briefing points to the mechanics skill rather than repeating its change loop.
Recipes stay out of context until the agent selects one.

### Attach

For container jails, the host accepts a stored sheet only when its token matches the inspected
running jail. The sheet records the launch's effective facts; attach does not substitute current
config for those facts. Authenticated live mount changes are folded in from the running record,
and the active pack view is the one the jail started with. The attach then refreshes its host-side
staging while preserving the running tree it reads.

If the authenticated snapshot is absent or does not match, the host does not fabricate facts from
the desired config. The compatibility path preserves the existing mechanics skill rather than
replacing it with defaults, and warns that the guidance still describes the prior applied state.
See [agent briefings](agent-briefings.md) for the broader skills delivery and attach rules.

### Live config apply

A running jail's guidance changes only when an effective apply changed its applied state. A
successful key fold proves a next-session update; a successful live mount or scope operation is
effective even if an unrelated key fold failed. A failed fold by itself cannot replace the
applied snapshot or refresh the guidance. Pending, record-only, unknown-runtime and no-op applies
leave the running suite alone.

When an effective change qualifies, the host updates the authenticated snapshot and replaces the
mechanics index, the host map and every topic file individually, using a temporary file and
rename for each (`jailcontent.RefreshMechanicsFiles`). After a target's writes it removes each
topic file the facts no longer render, so a removed context mount takes `topics/mounts.md` with
it, and a later failure restores the removed files with the rest. This is not one transaction across the snapshot and the skill files. If rendering or a later file
replacement fails, it attempts to roll back files already replaced and restore the prior snapshot.
A warning names a failed recovery or says the guidance still describes the prior applied state.
This is a guidance refresh, not a jail restart.

### Host apply

At the host notch, `yolo host apply` renders host versions of `configuring-the-jail`,
`diagnosing-the-jail` and `jail-mechanics` from host facts. Those versions retain host-applicable
instructions and exclude jail-only paths and restart recipes. The generated suite is part of the
existing owned-skills composition, adoption, observation and retirement flow; observe posture
writes nothing. On a render failure, the last successful suite is retained through that same
ownership path.

## Skill precedence and delivery

Ordinary skill-name precedence is workspace, built-in suite, then selected pack layers. Workspace
skills fill names above them left free; packs may override ordinary built-ins. **Fact-backed
`jail-mechanics` is a deliberate exception:** its generated form replaces a same-named pack skill
and reports that override; the workspace layer cannot replace it. This reserves the source of
truth for environment-specific guidance without changing the precedence of other skill names. A
legacy attach without authenticated facts preserves the existing mechanics tree instead of
manufacturing this override from defaults.

Container backends and native macOS deliver staged content through their existing bind or copy
mechanisms. Apple Container's copy is the launch's content and is not refreshed on re-entry; only
an attach-capable backend can refresh its host-side staging on attach. See [agent
briefings](agent-briefings.md#staging-and-delivery) for destination, mode and live-visibility
details. A generated skills-render error preserves the previous suite and names a recovery step;
unrelated skill-plan errors, such as a fatal pack collision, retain their own refusal behavior.

## Where the code lives

| Responsibility | Package and symbols |
| :--- | :--- |
| Shared timing rules and host-command wording | `internal/changetiming` (`Classify`, `HostCommand`) |
| Fact model and generated prose | `internal/jailcontent` (`FactSheet`, `RenderBuiltinSkills`, `RefreshMechanicsFiles`) |
| Fresh-entry facts, authenticated attach state and apply refresh | `internal/cli/run` (`mechanicsFactSheet`, `readMechanicsApplied`, `updateMechanicsAfterApply`, `refreshJailBriefings`) |
| Host-notch composition and ownership | `internal/cli` (`prepareHostBuiltinSkills`, `applyHostSkills`) |

**Reads with:** [agent briefings](agent-briefings.md) (the shared applied-facts rule and skill delivery),
[jail home](jail-home.md) (persistence sources and staged content), [host apply staleness](host-apply-staleness.md)
(host-owned composition and observation posture), and [config safety](config-safety.md) (approval versus
application).

## Current values

| Surface | Value | Defined by |
| :--- | :--- | :--- |
| Jail skill index | `jail-mechanics/SKILL.md` | `jailcontent.RenderBuiltinSkills` |
| Generated host-path map | `jail-mechanics/host-map.md` | `jailcontent.RenderMechanicsIndexAndMap` |
| Topic files | `jail-mechanics/topics/<subject>.md`, one per subject this jail has | `jailcontent.BriefingTopics` |
| Container applied-snapshot file | `mechanics.json`, outside jail binds beside the context-mount record | `run.mechanicsRecordFile` |
| Running-jail apply entry point | `yolo config apply` | `run.ConfigApply` |
| Host rendering entry point | `yolo host apply` | `cli.applyHostSkills` |
| Current pending-change report | `yolo config drift` | config-drift command |

The index links to the only recipe files that apply to the current sheet. The generated surfaces
are host-staged; their backend-specific delivery is described in [agent briefings](agent-briefings.md#staging-and-delivery).

## What this does not change

This guidance reports applied decisions; it does not make a setting live, grant a new host
capability, change configuration defaults, authorize a pack, or replace config validation. A
recipe's expected result is not a guarantee that a runtime probe succeeded. The command report
and the actual host-side outcome decide that.

The Linux fixture and nested witness results are not native macOS or real-rootless certification.
No claim about Seatbelt enforcement, real-rootless behavior or host reachability follows from
those results.

## Why it's this way

The OQs are maintainer rulings; the JM-D rows preserve implementation decisions that still constrain
this system. They are compacted here because a maintainer reading only the code could otherwise
undo a boundary that keeps generated guidance accurate and discoverable.

| ID | Ruling | Date | Settled in |
| :--- | :--- | :--- | :--- |
| OQ-JM1 | One pointer in the always-on briefing; the change steps live only in the on-demand skill. A second summary can drift into a second answer. | 2026-10-23 | [Lifecycle](#lifecycle) |
| OQ-JM2 | Generate yolo-authored informational guidance from one applied fact sheet so it describes the situation that holds; pack- and workspace-owned prose remains with its own source. | 2026-10-23 | [Principles](#principles) |
| OQ-JM3 | Render actual paths, including host paths; do not assume the container workspace path. This superseded the old generated-text path convention. | 2026-10-23 | [Fresh entry](#fresh-entry) |
| OQ-JM4 | Host apply renders only what applies on the host, with no jail-only restart recipe. | 2026-10-23 | [Host apply](#host-apply) |
| JM-D1 | Keep the `jail-mechanics` name and its static skill-selection description; the generated body is situation-specific. | 2026-10-23 | [Skill precedence](#skill-precedence-and-delivery) |
| JM-D2 | Fact-backed generated `jail-mechanics` overrides a same-named pack or workspace skill and reports the collision; no lower layer contains the launch facts. | 2026-10-23 | [Skill precedence](#skill-precedence-and-delivery) |
| JM-D3 | Attach reads the token-authenticated running snapshot, not desired config. | 2026-10-23 | [Attach](#attach) |
| JM-D4 | A settled effective live apply refreshes the on-demand mechanics index and host map (and, since the briefing diet, the topic files); it does not rebuild the briefing or restart the jail. | 2026-10-23 | [Live config apply](#live-config-apply) |
| JM-D5 | The recipe states its expected outcome and treats the host command's report as authoritative; the renderer cannot see per-run probes. | 2026-10-23 | [The applied fact sheet](#the-applied-fact-sheet) |
| JM-D6 | Do not render a pending-change list; direct the agent to the live drift command. | 2026-10-23 | [What this does not change](#what-this-does-not-change) |
| JM-D7 | Apple Container keeps its launch's staged copy and does not refresh it on re-entry. | 2026-10-23 | [Skill precedence](#skill-precedence-and-delivery) |
| JM-D8 | Generate the common “ask the human” host-step wording from the shared timing decision rather than another wording list. | 2026-10-23 | [Where the code lives](#where-the-code-lives) |
| JM-D9 | Golden tests report the skill-size budgets; they are targets, not enforced limits. | 2026-10-23 | [Where the code lives](#where-the-code-lives) |
| JM-D10 | A generated-guidance render failure keeps the prior suite, does not fail entry, and names a recovery step; unrelated skill-plan refusals keep their behavior. | 2026-10-23 | [Skill precedence](#skill-precedence-and-delivery) |
| JM-D11 | Host apply renders host versions of the three built-in skills, limits them to host-applicable content, and does not write a skill with nothing to say at the host notch. | 2026-10-23 | [Host apply](#host-apply) |
