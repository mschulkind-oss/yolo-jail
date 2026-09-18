---
title: "Workspace skills — the repo picks the content, never the agent"
date: 2026-09-17
status: in-review
tags: [design, skills, packs, workspace, notch, git, trust]
summary: "A repo that commits .claude/skills/ has chosen its readers' agent for them: a codex or pi user gets a worse experience from the same repo for no reason either of them chose. yolo already owns the one place every agent's skills converge — the per-agent staging it composes host-side and binds read-only — and the cheapest agent-neutral answer is to make the workspace one more SOURCE of that composition, declared by the agent packs rather than known to core. Whether the workspace may be a source at all is the user's ruling, and the argument runs both ways; the fact that most reshapes the answer is that the staging copier dereferences symlinks host-side, which a cloned repo turns into a host-file read."
vantage:
  status-chip: true
---

# Workspace skills — the repo picks the content, never the agent

**Status:** DESIGN, 2026-09-17 — nothing built; six questions open. Every current-behaviour
claim below was checked against the tree at `3fceb366` on 2026-09-17, and every claim about an
agent's discovery paths against the bundle installed in this jail, version named in the table
that makes it.

> **In short.** A repo may hold an opinion about *what* its agents should know; it must not
> get to hold one about *which* agent its readers use. The one place every agent's skills
> already converge is the per-agent staging yolo composes host-side and binds read-only into
> the jail — so the agent-neutral answer is to make the workspace one more **source** of that
> composition, with each agent pack declaring where its agent reads at project scope exactly
> as it already declares where it reads at home scope. Whether the workspace may be a source at
> all is [OQ-WS1](#OQ-WS1), and it is genuinely arguable in both directions.

**Why it matters.** Today a `.claude/skills/` tree reaches `claude`, `copilot` and `opencode`
and misses `codex`, `pi` and `agy`; no single project path reaches all six
([§2.1](#21-where-each-agent-reads-skills--measured)). yolo's only cross-agent content
channel, `packs`, is closed to the workspace by construction and by a ruling that stands
([§2.5](#25-the-packs-boundary-and-the-ruling-behind-it)) — so the tax lands on the reader who
brought the "wrong" agent.

**The shape.** Two candidate mechanisms and two baselines ([§4](#4-the-candidate-mechanisms)):
a **staged mirror** — the workspace's skills as a bottom layer of the existing composition,
nothing written into the repo — and **in-workspace links** — agent-specific project paths
written into the repo as symlinks to one dir and kept out of git. The source set and the skip
rule are pack-declared ([§5](#5-the-pack-declares-what-its-agent-reads)); core learns no path.

**Cost.** The one agent-writable tree becomes a skills source, so the "skills are out of the
agent's reach" property ([§2.3](#23-the-writability-asymmetry--measured)) is given up for this
layer on purpose. The links variant writes into a repo yolo does not own.

**Start at [§4.1](#41-mechanism-a--the-staged-mirror)** — the mirror, and the symlink rule
that reshapes it. [§6](#6-both-notches-honestly) is where the host answer has to differ.

**Needs your ruling:** [OQ-WS1](#OQ-WS1), [OQ-WS2](#OQ-WS2), [OQ-WS3](#OQ-WS3),
[OQ-WS4](#OQ-WS4), [OQ-WS5](#OQ-WS5), [OQ-WS6](#OQ-WS6).

**Reads with:** [`workspace-skills-plan.md`](workspace-skills-plan.md) (the implementation
sketch — incomplete, and unstable while these questions are open);
[`../reference/agent-briefings.md`](../reference/agent-briefings.md#skills) (the skills
composition as built, and the deleted layer this design must not re-add);
[`../plans/agent-config-packs.md`](../plans/agent-config-packs.md#whether-pack_requests-in-workspace-scope-is-worth-its-complexity-in-v1)
(the 2026-07-26 ruling this design does not reopen, and
[OQ-ACP2](../plans/agent-config-packs.md#-oq-acp2--whether-opencodes-skills-gap-should-be-closed-by-writing-into-workspace),
the live question [OQ-WS4](#OQ-WS4) and [OQ-WS5](#OQ-WS5) would close);
[`host-render-target.md`](host-render-target.md#66-a-host-target-is-user-scoped-not-workspace-scoped)
(the host ruling that fixes the host half's shape).

---

## 1. Verdict, and the principles it rests on

**Conditional on [OQ-WS1](#OQ-WS1)**: if the workspace may contribute skills, build the
staged mirror in containers and on `macos-user`, declared per agent pack, at the lowest
precedence, with symlinks that escape the workspace refused; serve the host notch with
in-workspace links under `yolo host -- <agent>` and never under `yolo host apply`. If
[OQ-WS1](#OQ-WS1) rules the workspace out, the deliverable is the documented convention in
[§4.3](#43-baseline-c--conventions-only) and this doc closes.

The principles that produce that verdict, numbered so later sections and the sketch can cite
them:

- **P1. The repo owns the content; yolo owns the reach.** A repo's skills are the repo's
  business. Which agent its reader runs is not. (The user's thesis, restated as a rule.)
- **P2. Core knows no agent's paths.** Where an agent reads at project scope is a fact about
  that agent, declared by its pack — the rule
  [`../reference/extension-point-principle.md`](../reference/extension-point-principle.md#the-principle)
  states and `internal/entrypoint`'s one render loop already obeys (no switch on a tool name).
- **P3. Delivery, never selection.** Whatever the mechanism, it moves bytes that are already in
  the workspace to a path an agent reads. It never fetches, never names a source outside the
  tree, and never chooses which packs exist. That is the line that separates it from `packs`
  ([§2.5](#25-the-packs-boundary-and-the-ruling-behind-it)).
- **P4. What lands in the repo is visible in a diff, or is not written.** The blind cell
  ([`../reference/host-execution-from-the-workspace.md`](../reference/host-execution-from-the-workspace.md#the-two-axes))
  is the class of file that decides visibility while being invisible itself; this design must
  not make yolo a writer of one without a ruling ([OQ-WS6](#OQ-WS6)).
- **P5. A cloned repo can never reach the host through this.** The staging runs host-side, so
  any path that dereferences a committed symlink is a host read
  ([§4.1](#41-mechanism-a--the-staged-mirror)). This one is not a leaning; it is forbidden
  behaviour.

## 2. What exists today — measured

### 2.1 Where each agent reads skills — measured

Every row was read from the bundle installed in this jail on 2026-09-17, by grepping its
strings; the vendor docs were not trusted, following the lesson
[`../plans/pack-host-management-plan.md`](../plans/pack-host-management-plan.md#n6-new--copilot-reads-claudes-plugin-manifests-and-namespaces-plugin-skills)
records (a docs-only pass got three facts wrong about `copilot`). "Home scope" is the
directory under `$HOME`; "project scope" is relative to the directory the agent is started in.
Those are plain words, not terms — the vendors say *personal*, *user* and *global* for the
first and *project* for the second.

| Agent, version | Project-scope skills dirs the binary names | Home-scope dir (= the pack's `into`) | Collision rule, as documented | Follows symlinks |
| :--- | :--- | :--- | :--- | :--- |
| `claude` 2.1.275 | `.claude/skills` only. `.agents/skills` appears in the binary solely inside an **importer** that copies it into `.claude/skills` | `~/.claude/skills` | personal > project, same name = higher wins **silently**; plugin skills namespaced `plugin:skill` | yes (documented; verified 2026-08-01) |
| `copilot` 1.0.48 | `.github/skills`, `.agents/skills`, `.claude/skills` | `~/.copilot/skills` (also reads `~/.agents/skills`, `~/.claude/skills`, `COPILOT_SKILLS_DIRS`) | project first; **first-found-wins, deduplicated by bare name**, loser silently dropped; plugin skills namespaced but deduplicated flat | not verified |
| `codex` 0.145.0 | `.codex/skills` | `$CODEX_HOME/skills` = `~/.codex/skills` | not verified | not verified |
| `opencode` 1.18.31 | `.opencode/skills`, `.claude/skills`, `.agents/skills` | `~/.config/opencode/skills` | not verified | not verified |
| `pi` 0.85.1 | `.pi/skills` (its `CONFIG_DIR_NAME` is `.pi`) | `~/.pi/agent/skills` | loads both scopes; **deduplicates by real path**, so a symlink to an already-loaded file loads once | yes (follows symlinked dirs, skips broken ones) |
| `agy` 1.1.7 | `.agents/skills` | `.gemini/config/skills` (the pack's `into`; the binary's strings do not name it) | not verified | not verified |
| `oh-omp` | not installed here — unverified | `.oh-omp/agent/skills` | — | — |

> [!NOTE]
> **Codex and `.agents/skills`.** [`../research/agent-config-distribution.md`](../research/agent-config-distribution.md#part-1--where-agent-configuration-lives-per-agent)
> (gathered 2026-07-25) lists `.agents/skills/` for Codex. The 0.145.0 binary contains
> `.codex/skills` and `CODEX_HOME/skills` as literals and no `.agents/skills`; but it is a Rust
> binary that assembles paths from segments, so the absence proves nothing either way. Treat
> the row as *unconfirmed*, not as a correction.

**The fact this table settles:** there is **no project-scope path every agent reads.**
`.agents/skills/` — the Agent Skills standard's emerging interop path — reaches `copilot`,
`opencode` and `agy` and misses `claude` and `pi`. `.claude/skills/` reaches `claude`,
`copilot` and `opencode` and misses `codex`, `pi` and `agy`. A repo that wants all six ships at
least three trees, or two symlinks, and has to know that.

### 2.2 What yolo composes today

The skills composition is built and documented
([`../reference/agent-briefings.md`](../reference/agent-briefings.md#skills);
[`internal/jailcontent/skills.go`](../../internal/jailcontent/skills.go)). The parts this
design leans on:

- **Layers, in order:** the built-in suite, then every selected pack's `skills/` in config
  order, the **conventional local pack** (`~/.config/yolo-jail/local`) appended last by
  `config.LoadPacks`. A later layer overwrites an earlier same-named directory. There is no
  workspace layer.
- **A layer was deleted here, and its docstring is the closest prior art.** `SkillTarget`
  carried a `HostSource` — "the user's OWN skills tree to layer in last" — set to the
  *destination*, the host's `~/.<agent>/skills`. Once `yolo host apply` composed that
  directory, the jail read yolo's own output back in as the user's tree. The docstring's
  verdict: *"There is nothing to replace it with, because the slot it described already has a
  home: the CONVENTIONAL LOCAL PACK is layer 4."*
  ([`../plans/retired-decisions.md`](../plans/retired-decisions.md#the-local-pack-is-layer-4).)
  That cuts against one obvious design — "add a path-shaped source" — **only when the path is
  a destination.** A workspace directory is not one: nothing yolo renders lands there, so
  reading it is not circular. The design must still not become a fourth layer that reads
  generated output, and nothing below does.
- **Per destination, `:ro`.** Each agent pack declares `{"kind": "skills", "into": …,
  "agent": …}`; the launcher builds one staging directory per pack under the machine
  storage's per-jail `agents/<container>/` tree and binds it read-only at `~/<into>`
  ([`../reference/jail-home.md`](../reference/jail-home.md)). Rebuilt on **every**
  invocation, attach included, clearing *inside* the directory so the bind's inode survives.
- **An audience per source.** A source may name `agents`; empty is broadcast
  ([`briefing-audiences.md`](briefing-audiences.md)). This is the only point at which "who is
  this for?" is asked.
- **Symlinks are dereferenced on copy** — deliberately, "because its source is the user's own
  home" ([`../reference/pack-system.md`](../reference/pack-system.md#skills)). A pack's
  *staging*, by contrast, refuses a symlink whose target escapes the pack root.
- **Collisions:** the host notch is **fatal** at apply time, naming both packs; the jail is
  **silent last-writer-wins**, and that gap is a live question of its own
  ([S5](../plans/BACKLOG.md#-s5--a-jail-resolves-a-skill-name-collision-silently)). This design
  must not add a third silent path.

### 2.3 The writability asymmetry — measured

Read from this jail's mount table on 2026-09-17: `/workspace` is `rw`; `/home/agent` is `ro`;
and all five staged skills directories — `~/.codex/skills`, `~/.claude/skills`,
`~/.gemini/config/skills`, `~/.pi/agent/skills`, `~/.config/opencode/skills` — are `ro`.
So in a container an agent can create `/workspace/.claude/skills/x/SKILL.md` and `claude` will
read it at project scope; it cannot touch a single byte under any home-scope skills dir.
**The workspace is the only place a jail-side answer can land**, and it is also the only
agent-writable one — both halves of this design follow from that one line.

Two backends weaken it and this design must not pretend otherwise: on `macos-user` the staged
skills are **writable copies**, and on Apple Container below the read-only floor the skills
bind lands writable onto the launcher's own staging dir
([G14](../plans/setup-support-gaps.md)). Neither is changed here, in either direction.

### 2.4 What yolo writes into a workspace today

The durable statement is the config-ownership principle
([`../reference/storage-and-config.md`](../reference/storage-and-config.md#the-config-ownership-principle)):
*"yolo composes generated config into the jail USER scope only. The workspace tree is the
operating agent's, and mirrors the host."* Measured against it, yolo writes exactly three
things under a workspace root:

| What | Who writes, when | Kept out of git how |
| :--- | :--- | :--- |
| `<ws>/.yolo/` — the state dir (home overlays, logs, sidecars, archive) | every launch | its own `.gitignore` containing `*`, **written once and never again** — a user who edits or empties it is not fought ([`internal/paths/paths.go`](../../internal/paths/paths.go)) |
| `<ws>/yolo-jail.jsonc` | `yolo init`, once, never overwritten | committed on purpose |
| a `.yolo/` line appended to `<ws>/.gitignore` | `yolo init`, only if the file lacks it ([`internal/cli/init.go`](../../internal/cli/init.go)) | it *is* the ignore |

Nothing yolo ships writes a symlink under a workspace root outside `.yolo/`; every
`os.Symlink` site in the tree targets a directory yolo owns (a home overlay, a managed
`CODEX_HOME`, the store-package farm, the flake bundle). The principle's word is **config**; a
skills tree is content. But the principle's *reason* — the workspace is the agent's and
mirrors the host — applies to content just as well, so the links mechanism in
[§4.2](#42-mechanism-b--in-workspace-links) is a re-reading of the principle, not a carve-out
from it, and it says so.

### 2.5 The `packs` boundary, and the ruling behind it

`packs` is **user-scope only, by construction**: `config.LoadPacks` reads the user config path
directly rather than the merged config, and the package doc names that direct read as "the
whole security model of the feature" — a workspace config travels with the repo and is
agent-editable, "so it must not be able to name content that enters the jail"
([`internal/config/packs.go`](../../internal/config/packs.go)). The same doc records **the
ruling** (2026-07-26, [`../plans/agent-config-packs.md`](../plans/agent-config-packs.md#whether-pack_requests-in-workspace-scope-is-worth-its-complexity-in-v1)):

> *"packs are only at the user level. At the repo/jail level you can just design everything in
> the workspace however you want — you've got a git repo already."*

That ruling deleted a proposed inert `pack_requests` key and its `approve --from-workspace`
verb. It **stands**, and this design does not reopen it: nothing here lets a workspace name a
pack, a source, or a grant (P3). What this design does is test the ruling's *premise*. "You've
got a git repo already" is true for one agent at a time — the one whose project path the repo
chose. It was never claimed for six.

Two things the boundary rests on are worth having in front of you before ruling
[OQ-WS1](#OQ-WS1), because they pull in opposite directions:

- **A pack can grant host reads, install programs and run hooks; a skill is prose an agent
  reads.** The `packs` boundary exists because a committed file must not *select* content that
  crosses into the jail from elsewhere. Workspace skills cross nothing: the bytes are already
  in the jail, readable by the agent, and for `claude` already *loaded* — the shipped
  `claude`, `codex` and `agy` packs declare the jail workspace **trusted** in their composed
  config (`hasTrustDialogAccepted`, `trust_level: "trusted"`, `trustedWorkspaces`), so the
  repo's project-scope skills load in a jail with no dialog today. By the authority test
  ([`../reference/gate-placement-principle.md`](../reference/gate-placement-principle.md#test-1--the-authority-test-could-this-actor-already-do-it)),
  a repo that ships every project path already reaches every agent; a mirror grants it no
  authority it lacks — it removes the tax of knowing the paths.
- **Against:** the workspace is the one source an in-jail agent can write. Today the pack and
  built-in layers are beyond its reach ([§2.3](#23-the-writability-asymmetry--measured));
  a workspace layer is not, and "an agent that edits its own instructions between launches is
  the failure the `:ro` bind exists to prevent" is the sentence
  [G14](../plans/setup-support-gaps.md) uses for exactly this. The `:ro` property buys
  **non-persistence** — the next run rewrites the staged file — and a committed skill is
  persistent by nature.

## 3. The gap, stated

A repo's skills reach the agent whose path the repo chose. Every other reader's agent gets the
briefing (`AGENTS.md` is read natively by most of the field and bridged for the rest) and none
of the skills. Two zero-code routes exist and both are **user-scope acts the repo cannot make
for its readers**: add the checkout's skills dir as a local `packs` entry (machine-wide, so the
skills then leak into every other workspace on that machine), or copy the tree into the
conventional local pack (a fork that goes stale). Neither is workspace-scoped, and neither is
something a repo author can do on a reader's behalf. That is the hole.

## 4. The candidate mechanisms

Two terms, coined here so the rest of the doc can be short:

- **Workspace skills** *(coined here)* — skill directories present in the workspace tree, at a
  path an agent pack declares as one its agent reads at project scope. Not pack skills (those
  arrive through `packs`, user-scope), not the local pack's, and not yolo's built-ins.
- The **source set** *(coined here)* — the workspace skills directories that exist at launch,
  after the declaration and existence checks in
  [§5](#5-the-pack-declares-what-its-agent-reads). Empty is legal and common.

### 4.1 Mechanism A — the staged mirror

**Staged mirror** *(coined here)*: the source set becomes one more layer of the existing skills
composition. Nothing is written into the workspace. Not a mount — a copy-merge, like every
other layer, because merge is the only operation that unions several sources into one
destination.

**What it does, exhaustively:**

| Question | Answer |
| :--- | :--- |
| What is created, where | Skill subdirectories inside each pack's existing staging directory under the machine storage's per-jail tree — the same place the built-ins and pack skills already land. Nothing under `<ws>` |
| Who writes | The launcher, host-side, in the same pass that stages every other layer; on `macos-user`, the same composed content tree the sidecar already receives |
| Trigger | Every `yolo` invocation against the jail, fresh launch and attach alike — the existing refresh contract, unchanged |
| Layer position | [OQ-WS2](#OQ-WS2). The leaning is **lowest** — below the built-ins — for the reason in [§2.5](#25-the-packs-boundary-and-the-ruling-behind-it)'s *against* bullet |
| Which agents receive a given source dir | Every declared destination **except an agent whose own declared project paths include that dir** — it already reads it natively, and a second copy is at best redundant ([§5](#5-the-pack-declares-what-its-agent-reads)) |
| Name collision with a pack or built-in skill | Resolved by layer order. Under the leaning the workspace copy is dropped and **one line per shadowed name is disclosed at launch**, at the once-per-run tier ([`../reference/report-tiers.md`](../reference/report-tiers.md#principles) P4 — a disclosure is never suppressible). Whether the jail should *refuse* on a collision is [S5](../plans/BACKLOG.md#-s5--a-jail-resolves-a-skill-name-collision-silently)'s question, not this doc's |
| Two source dirs carry the same skill name | Config-order of the declaring packs decides, and the same disclosure line names both |
| The repo already has the file | Not applicable — nothing is written into the repo |
| `git status` afterwards | Unchanged |
| When the jail stops | The staging dir persists on the host as it does today, and is rebuilt on the next invocation; nothing in the workspace changes |
| Symlink inside a source dir | **Refused if its target resolves outside the workspace root** (P5), the entry skipped and **named** at launch; a symlink that stays inside the workspace is dereferenced as today. A dangling symlink is skipped and named |
| Source dir absent or empty | Nothing staged, nothing said — the common case must be silent |
| Source dir is a file, or unreadable | Skipped and named |
| A skill dir with no `SKILL.md` | Copied as-is; what counts as a skill is the agent's business, as it is for every other layer today |
| `macos-user` | Works through the same composed tree; the copies are writable there, as they are today ([G14](../plans/setup-support-gaps.md)) |
| Host notch (`yolo host`) | **Never.** Writing a workspace's skills into the real `~/.<agent>/skills` is what [§6](#6-both-notches-honestly) rules out |

> [!CAUTION]
> **The single fact that most changes the shape of this design.** The staging copier follows
> symlinks — `copySkillSubdirs` stats through a symlinked source and `copyFileDeref` opens
> the link's target — and it runs **on the host**, against a tree a `git clone` populated and
> an in-jail agent can edit. A committed `.agents/skills/x/SKILL.md → ~/.ssh/id_ed25519` would
> be read by the host user, copied into the staging dir, and bound `:ro` into the jail as a
> skill. Worse, the in-jail agent can create that link itself and wait for the next attach.
> That is the credential boundary yolo exists to hold, opened by a symlink. So the workspace
> source **cannot reuse the existing copier as-is**: it walks with `Lstat`, resolves every link,
> and refuses any whose real path leaves the workspace root — the rule pack staging already
> applies to pack roots, applied to a root that is far less trusted. P5 is not negotiable
> whatever [OQ-WS1](#OQ-WS1) rules.

**What it changes about the system.** The skills an agent sees at `~/.<agent>/skills` are no
longer beyond the agent's reach: an edit under `/workspace/<declared path>/` reaches every
agent's home-scope dir at the next `yolo` invocation. That is the feature — a skill authored in
the repo, by whoever commits to it, reaches whoever clones it — and it is also the cost named
in [§2.5](#25-the-packs-boundary-and-the-ruling-behind-it). Under the lowest-layer leaning, the
workspace can never *shadow* a skill yolo or the user delivers; it can only add.

### 4.2 Mechanism B — in-workspace links

**In-workspace links** *(coined here)*: yolo writes, inside the workspace, the project-scope
path each selected agent reads, as a symlink to one source dir that exists — and keeps those
links out of git. It is the user's own sketch ("automatically gitignoring some symlinks that we
actually create"), made precise.

**What it does, exhaustively:**

| Question | Answer |
| :--- | :--- |
| What is created, where | For each selected agent pack whose declared project paths are **all absent** from the workspace: a relative symlink at the pack's first declared project path, pointing at the one source dir that exists — and the parent directory when it does not exist (`.codex/`, `.pi/`). Nothing when the source set is empty |
| Who writes | The launcher, host-side, before the container starts — so the same step serves `yolo host -- <agent>`, where there is no container. (The entrypoint could write it in-jail; that would leave the host notch unserved, which is the one thing this mechanism is for) |
| Trigger | Every launch and every `yolo host -- <agent>` exec, idempotent: an existing link that already points at the right target is left alone |
| Two source dirs exist with different content | A link can point at one. The other agents receive **only one of them**, and the launch says which was dropped — this is the case where B is strictly weaker than A |
| Name collision | None yolo can see: the agent reads a link to a tree and applies its own rule ([§2.1](#21-where-each-agent-reads-skills--measured)) |
| The repo already has the path | **Never touched, in any of the four cases**, and reported by name: a real directory (the repo's own opinion for that agent — respected), a committed symlink (same), a file (refused), or a symlink yolo wrote earlier (refreshed only if its target is stale). Ownership is decided by a record in `<ws>/.yolo/`, never by inspecting the link |
| `git status` afterwards | Depends on [OQ-WS6](#OQ-WS6): with no ignore, `?? .codex/` etc. — the user's next `git add .` commits yolo's links; with a root `.gitignore` append, `M .gitignore` once, then clean for every clone once committed; with `.git/info/exclude`, clean and invisible, per clone |
| When the jail stops | **The links persist** — the workspace is the user's real directory. They are removed when the pack that declared them leaves `packs` (the record says which are yolo's), or by hand |
| Not a git repo | The link is still written; the ignore step reports that there is nothing to write to |
| Worktree or submodule | `.git` is a file; `.git/info/exclude` lives in the common dir and is shared across worktrees. The path must be resolved through git, never spelled |
| `workspace_readonly` locks `.git/info` | The exclude route cannot write; it says so and falls back per [OQ-WS6](#OQ-WS6) |
| `macos-user` | Works — the workspace is a real path |
| Host notch | Works under `yolo host -- <agent>` (the cwd *is* the project). **Never under `yolo host apply`**, whose ruling is that the cwd selects nothing ([§6](#6-both-notches-honestly)) |
| The agent's own trust gate | Unchanged. In a jail the shipped packs pre-trust the workspace; on the host they do not, so `claude`'s project trust dialog, `pi`'s trust file and `codex`'s `trust_level` still govern whether the linked tree is read |

**What it changes about the system.** yolo becomes a writer inside the repo, outside `.yolo/`,
for the first time — a re-reading of the config-ownership principle
([§2.4](#24-what-yolo-writes-into-a-workspace-today)). And it makes yolo either an editor of a
tracked file on a launch (`.gitignore`) or a writer of the blind cell (`.git/info/exclude`) —
which is why [OQ-WS6](#OQ-WS6) is a ruling and not a detail.

### 4.3 Baseline C — conventions only

Document, for repo authors: ship `.agents/skills/`, commit symlinks `.claude/skills` and
`.pi/skills` pointing at it, and the six agents in
[§2.1](#21-where-each-agent-reads-skills--measured) all read it (`codex` unconfirmed). Zero
code.

**Verdict: the baseline every mechanism is measured against, and the whole deliverable if
[OQ-WS1](#OQ-WS1) rules the workspace out.** Rejected as the answer otherwise, because it puts
the work on the one party that has the opinion — the repo author who already picked an agent
has no reason to learn five others' paths — and it is exactly the "lesser experience for
readers" the thesis names.

### 4.4 Baseline D — the reader points a local pack at the checkout

A user adds `"packs": ["/abs/path/to/checkout/<dir-with-skills>"]` to their user config. Zero
code, works today.

**Verdict: rejected as the answer, kept as today's escape hatch.** A pack applies to the whole
jail set, so the repo's skills then reach **every workspace on the machine**; the path is
absolute and per-clone; and it is a user-scope act, which is precisely the thing a repo cannot
do for its readers ([§3](#3-the-gap-stated)).

### 4.5 Side by side

| | A — staged mirror | B — in-workspace links | C — conventions | D — local pack |
| :--- | :--- | :--- | :--- | :--- |
| Writes into the repo | no | **yes** (links + ignore) | no (the author does) | no |
| Reaches a container jail | yes | yes | yes | yes |
| Reaches `macos-user` | yes | yes | yes | yes |
| Reaches the host notch | **no** ([§6](#6-both-notches-honestly)) | yes, under exec only | yes | yes (machine-wide) |
| Unions several source dirs | yes | no — one target per link | n/a | n/a |
| Host-read exposure | closed by P5 | none (relative links, no host copy) | none | none |
| Agent-writable source | yes, lowest layer | yes (the repo) | yes | no |
| `git status` after a launch | clean | [OQ-WS6](#OQ-WS6) | clean | clean |
| Survives the jail stopping | staging only | **the links persist** | n/a | n/a |
| Who bears the cost | yolo | yolo + the repo's ignore file | the repo author | each reader, per machine |

## 5. The pack declares what its agent reads

Core learns no path (P2). The agent pack's `skills` contribution today declares the home-scope
destination (`into`) and the identity that reads it (`agent`). It grows one more declaration:
**the project-relative directories its agent reads at project scope**, in the agent's own
precedence order. The field's name and shape are the sketch's
([`workspace-skills-plan.md`](workspace-skills-plan.md#the-declaration)); its semantics are
this doc's:

- **The source set is the union of every selected pack's declared project paths that exist in
  the workspace.** With `packs: ["claude", "pi"]` and a repo that ships only `.claude/skills/`,
  the source set is `{.claude/skills}`. Nothing is conventional to core: `.agents/skills/`
  enters the set only because `copilot`, `opencode` and `agy` declare it.
- **The skip rule.** An agent whose own declared project paths contain a source dir does not
  receive that dir's mirror — it reads it natively, and a second copy is redundant at best
  ([§2.1](#21-where-each-agent-reads-skills--measured): `claude` would resolve it silently,
  `copilot` would drop the second copy silently, `pi` deduplicates by real path so a *copy*
  would be two skills). So in the example above `claude` receives nothing and `pi` receives
  `.claude/skills`'s tree in `~/.pi/agent/skills`.
- **A pack with no such declaration contributes nothing to the source set and receives every
  source dir** — the empty-is-broadcast rule, applied the way the audience filter applies it.
- **The declaration is data, verified by a probe test that pins nothing but the strings**, as
  the existing `--version`-only agent probes do. An agent moving its own project path is
  exactly the silent break a docs citation would not catch.
- **`macos-user` and the container backends share the declaration**; the host notch reads it
  only for [§4.2](#42-mechanism-b--in-workspace-links).

This is [OQ-WS3](#OQ-WS3)'s subject: whether the source set is pack-declared as above, or one
directory yolo names. The leaning is written into the bullets; the alternative is in the
question.

## 6. Both notches, honestly

The user is explicit that they do not know whether the host can be served. It can, and the
answer differs by mechanism, not by wish.

| Notch | Mechanism A | Mechanism B | What decides it |
| :--- | :--- | :--- | :--- |
| Container jail (`podman`, Apple Container) | yes | possible, redundant with A | [§2.3](#23-the-writability-asymmetry--measured): the home-scope dirs are yolo's `:ro` mounts |
| `macos-user` | yes — same composed tree into the sidecar | yes | the workspace is a real path; skills are writable copies there today |
| Host, `yolo host -- <agent>` | **no** | yes | below |
| Host, `yolo host apply` | **no** | **no** | below |

**Why A is closed on the host, and why this doc does not ask to open it.** Two rulings, in
sequence, both standing and one built:

1. [`host-render-target.md`](host-render-target.md#66-a-host-target-is-user-scoped-not-workspace-scoped)
   ruling 9.5 (2026-08-01): *"What yolo asserts into your real `$HOME` is a function of your
   user configuration and the packs you have installed — never of which repository you happened
   to run `apply --host` from. The `cwd` selects nothing."*
2. [`config-ownership-and-promotion.md`](config-ownership-and-promotion.md#13-decision-ledger)
   [OQ-CO8](config-ownership-and-promotion.md#13-decision-ledger) (2026-09-11): `--to
   workspace` is out of scope *as a decision*, and "whoever wires that layer owns the argument
   that a jail-writable layer must not reach a real home."

Mirroring a checkout's skills into the real `~/.claude/skills` is the exact act both forbid. I
searched for a reversal, since this session had already been burned by finding the reversal
and reading it as the whole history: the later
[`config-target-resolution.md`](config-target-resolution.md#3-one-resolved-target) (2026-09-17)
does let the cwd select *which target a `yolo config` verb describes*, and states in as many
words that this refines 9.5 rather than overturning it — the cwd may choose what you are
*asking about*, never what is *rendered into a real home*. So the ruling holds, and the host
half of this design is B or nothing ([OQ-WS5](#OQ-WS5)).

**What B on the host buys and costs.** `yolo host -- codex` runs in a cwd that is the project;
writing `.codex/skills → .agents/skills` there serves codex exactly as the repo would have by
committing the link — and the agent's own project trust gate still stands between the link
and the read, because on the host yolo pre-trusts nothing. The cost is the one in
[§4.2](#42-mechanism-b--in-workspace-links): a launch that edits the repo.

## 7. What this does not propose

- **No workspace `packs`, no `pack_requests`, no approval verb.** The 2026-07-26 ruling stands
  in full; this design moves bytes the workspace already holds and selects nothing (P3).
- **No fetch.** A workspace skill is committed content or an in-tree edit, never a URL.
- **No path into a real home from a workspace.** Not for skills, not for anything
  ([§6](#6-both-notches-honestly)).
- **No change to pack precedence, to `skills_tier`, or to the jail's collision behaviour.**
  Workspace skills are flat, as an agent's own project skills are; S5 keeps its question.
- **No briefing half.** `AGENTS.md` at the repo root is read natively by most agents and bridged
  for the rest, and the reference already says yolo does not touch the repo's own file
  ([`../reference/agent-briefings.md`](../reference/agent-briefings.md#skills)).
- **No re-added layer that reads a destination.** The deleted `HostSource` stays deleted; a
  workspace dir is a source and never a destination ([§2.2](#22-what-yolo-composes-today)).
- **No change to `macos-user`'s writable copies or Apple Container's floor** — G14 is its own
  gap.

## 8. Risks

| Risk | Mitigation |
| :--- | :--- |
| **R1. A committed or agent-written symlink is dereferenced host-side into the staging dir** — a host-file read from a cloned repo (A) | P5: `Lstat` walk, refuse any link whose real path leaves the workspace root, skip and name it. Forbidden behaviour, not a knob |
| **R2. The in-jail agent shadows a jail-management skill** (`configuring-the-jail`) by writing a same-named dir in the workspace and waiting for the next attach (A) | [OQ-WS2](#OQ-WS2)'s lowest-layer leaning: the workspace can add, never shadow; the shadowed name is disclosed |
| **R3. yolo commits to your repo by proxy** — links land in the next `git add .` (B) | [OQ-WS6](#OQ-WS6); and the links are relative, so even committed they work for the next reader, which limits the damage to "yolo chose for you" |
| **R4. Double delivery** — an agent reads the source natively and receives the mirror (A) | the skip rule in [§5](#5-the-pack-declares-what-its-agent-reads); `pi`'s real-path dedup is why it is a rule and not a nicety |
| **R5. A pack's declared project path is wrong or stale** — the mirror skips an agent that no longer reads there | the probe test in [§5](#5-the-pack-declares-what-its-agent-reads); the declaration is data with a witness |
| **R6. An attach re-stages a colleague's working-tree edits into a live session** (A) | the existing refresh contract already does this for pack edits; the disclosure line makes it visible. The pairing case is [OQ-ACP1](../plans/agent-config-packs.md#-oq-acp1--what-happens-when-two-people-attach-to-the-same-jail-with-different-pack-sets)'s and stays there |
| **R7. B creates a `.codex/` or `.pi/` directory in a repo that had none**, and the agent starts reading other things there | only the skills link is written; no config file is created. Stated so an implementer does not "helpfully" add one |
| **R8. Two source dirs with different content under B** | reported by name; A unions them, which is why A is the container answer |
| **R9. `.git/info/exclude` becomes a file yolo writes** — the blind cell, and the file `workspace_readonly` wants locked (B) | [OQ-WS6](#OQ-WS6); P4 |

## 9. What I would build, in order

Prose, not tickets; the sketch carries the file map.

1. **Rule [OQ-WS1](#OQ-WS1).** Nothing below survives a *no* except [§4.3](#43-baseline-c--conventions-only)'s paragraph of documentation.
2. **The declarations** — each shipped agent pack states the project paths in
   [§2.1](#21-where-each-agent-reads-skills--measured), and the probe test pins them. Data
   only; no behaviour yet.
3. **Mechanism A in containers and on `macos-user`**, with the P5 walk as the first thing
   written and the first thing tested — the test is a repo carrying a symlink to a file under
   `$HOME`, asserting the staging dir does not contain its bytes — then the layer, the skip
   rule, and the disclosure line. The call site must be pinned, not the helper.
4. **Mechanism B for the host notch**, if [OQ-WS5](#OQ-WS5) puts it in scope, under
   `yolo host -- <agent>` only, with [OQ-WS6](#OQ-WS6)'s ignore route.
5. **The corpus:** the skills section of the briefings reference gains a layer;
   [G32](../plans/setup-support-gaps.md)'s row and the `AGENTS.md` skill-priority sentence are
   re-read against it; [OQ-ACP2](../plans/agent-config-packs.md#-oq-acp2--whether-opencodes-skills-gap-should-be-closed-by-writing-into-workspace)
   is closed in its own doc by [OQ-WS4](#OQ-WS4) and [OQ-WS5](#OQ-WS5)'s rulings.

## 10. What done looks like

Observable by a human, not by a test name:

- A repo that ships only `.claude/skills/review/` is opened in a jail with `packs: ["pi"]`;
  `ls ~/.pi/agent/skills` shows `review`, and `pi` lists it once.
- The same repo with `packs: ["copilot"]`: `~/.copilot/skills` does **not** contain `review`
  (copilot reads `.claude/skills` natively), and `copilot` lists it once.
- The same repo with `packs: ["claude", "pi"]` and a second tree `.agents/skills/lint/`:
  `claude` receives `lint` in `~/.claude/skills` and nothing else from the workspace; `pi`
  receives both.
- A repo containing `.agents/skills/x/SKILL.md → ~/.ssh/config`: the jail starts, no skill
  named `x` exists in any home-scope dir, and the launch names the refused link.
- A repo with `.agents/skills/configuring-the-jail/`: every agent still sees yolo's built-in
  under that name, and the launch says the workspace's copy was shadowed (under the
  [OQ-WS2](#OQ-WS2) leaning).
- After any of the above, `git status` in the repo is unchanged (mechanism A).
- On the host, `yolo host -- codex` in that repo leaves `.codex/skills → .agents/skills`
  behind, `git status` shows what [OQ-WS6](#OQ-WS6) ruled and nothing else, and `yolo host
  apply` from the same cwd writes nothing skills-related into `~/.codex/skills`.

## 11. Open Questions

Every question below was checked against the sibling ledgers before it was opened
([`agent-config-packs.md`](../plans/agent-config-packs.md#whether-pack_requests-in-workspace-scope-is-worth-its-complexity-in-v1),
[`host-render-target.md`](host-render-target.md#66-a-host-target-is-user-scoped-not-workspace-scoped),
[`config-ownership-and-promotion.md`](config-ownership-and-promotion.md#13-decision-ledger),
[`yolo-as-environment-manager.md`](yolo-as-environment-manager.md),
[`../reference/pack-system.md`](../reference/pack-system.md#skills),
[`../plans/setup-support-gaps.md`](../plans/setup-support-gaps.md)), and for each ruling found,
whether a later doc overturned it. What was found is in
[§2.5](#25-the-packs-boundary-and-the-ruling-behind-it) and [§6](#6-both-notches-honestly);
none of it answers these, and one live sibling question
([OQ-ACP2](../plans/agent-config-packs.md#-oq-acp2--whether-opencodes-skills-gap-should-be-closed-by-writing-into-workspace))
is answered *by* two of them.

1. 💬 **OQ-WS1: May the workspace contribute skills at all?** This is the closure question;
   every other question is moot on a *no*. What it decides: whether a `git clone` (and the
   in-jail agent, which can write the same tree) becomes a source of the composed skills every
   agent in the jail reads. The case for and against is
   [§2.5](#25-the-packs-boundary-and-the-ruling-behind-it)'s two bullets; the fact that most
   bears on it is that three shipped agent packs already declare the jail workspace trusted, so
   the repo's project-scope skills load for `claude` today with no gate — and the fact that
   most tempers it is that the staging copier would have turned a committed symlink into a host
   read had this been built without [§4.1](#41-mechanism-a--the-staged-mirror)'s caution.

   <!-- vantage: oq id=OQ-WS1 leaning="Yes, in containers and macos-user, at the lowest layer, with escaping symlinks refused — the repo already reaches every agent by shipping every path, so the mirror grants no authority it lacks; but this is the user's call and the against case is real." -->

   _Leaning:_ **yes** — jail and `macos-user`, lowest layer, P5 enforced. By the authority
   test the repo already reaches every agent by shipping every path; a mirror removes a tax,
   not a boundary. The *against* bullet is real and is why the leaning is lowest-layer rather
   than "another pack".

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-WS2: Where does the workspace layer sit?** Decides who wins a same-name collision
   between a workspace skill and a built-in, a shared pack's, or the local pack's. Three
   positions: **(a) lowest** — below the built-ins, so the workspace can add but never shadow;
   **(b) between the shared packs and the local pack** — a repo's conventions beat a company
   baseline, the user's own still beat the repo; **(c) top** — project beats everything, the
   Agent Skills standard's own recommendation and `copilot`'s order. Note the vendors disagree
   among themselves (`claude` personal > project; `copilot` project > personal), so no position
   matches every agent's native rule.

   <!-- vantage: oq id=OQ-WS2 leaning="(a) lowest — the workspace is the one agent-writable and clone-populated source, so it must never be able to shadow a jail-management skill; every shadowed name is disclosed." -->

   _Leaning:_ **(a).** The workspace is the one source a clone populates and an agent edits;
   letting it shadow `configuring-the-jail` is R2. Disclosure of every shadowed name makes the
   loss visible without reopening S5.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-WS3: Is the source set pack-declared, or one directory yolo names?** Decides
   whether core learns a path. **(a)** the union of every selected agent pack's declared
   project paths ([§5](#5-the-pack-declares-what-its-agent-reads)) — a repo that already chose
   `.claude/skills/` is served with no change to the repo, which is the motivating case;
   **(b)** one conventional directory (`.agents/skills/`, the field's interop path) that core
   knows the way it knows a pack's `skills/` — simpler, and a repo has to adopt it; **(c)** both.

   <!-- vantage: oq id=OQ-WS3 leaning="(a) — the union of pack-declared project paths; core names no path, and the repo that already picked .claude/skills is served as-is, which is the case the user described." -->

   _Leaning:_ **(a).** P2, and it serves the repo that already made its choice — the user's
   framing — rather than asking it to make another.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-WS4: In containers, mechanism A, B, or both?** Decides whether yolo ever writes
   into a repo where a container is available. A serves every agent from the `:ro` dirs yolo
   already owns and unions several source dirs; B is what the user first sketched, writes into
   the repo, and cannot union. Building both in a jail delivers each skill twice to some agents.
   Answering this also answers
   [OQ-ACP2](../plans/agent-config-packs.md#-oq-acp2--whether-opencodes-skills-gap-should-be-closed-by-writing-into-workspace)
   ("never write into `/workspace`", leaning unruled since 2026-07), whose premise — opencode
   had no home-scope skills dir — has since fallen: the `opencode` pack declares
   `.config/opencode/skills` and it is bound `:ro` in this jail today.

   <!-- vantage: oq id=OQ-WS4 leaning="A alone in containers and on macos-user; B is a host-notch tool and nothing else — and this ruling closes OQ-ACP2 in its own doc." -->

   _Leaning:_ **A alone** where a composed home exists; B only where it does not
   ([OQ-WS5](#OQ-WS5)). Record the answer in
   [`agent-config-packs.md`](../plans/agent-config-packs.md#-oq-acp2--whether-opencodes-skills-gap-should-be-closed-by-writing-into-workspace)
   too.

   **Answer:**
   > _(empty — fill in when decided)_

5. 💬 **OQ-WS5: Is the host notch in scope for v1 — and by B only?** Decides whether
   `yolo host -- <agent>` writes links into the cwd repo. The mirror is closed on the host by
   two standing rulings this doc does not reopen ([§6](#6-both-notches-honestly)); the question
   is only whether B is worth shipping there now, given that a host user can commit the same
   links by hand and that the agent's own trust gate still governs the read. A *yes* makes
   [OQ-WS6](#OQ-WS6) load-bearing; a *no* parks it.

   <!-- vantage: oq id=OQ-WS5 leaning="Out of scope for v1 — ship A in containers first; the host half is B or nothing, is one exec-time step, and can follow once OQ-WS6 is ruled." -->

   _Leaning:_ **v2.** The container answer stands alone; the host half is a single exec-time
   step that can land later without reshaping anything, and it should not delay A.

   **Answer:**
   > _(empty — fill in when decided)_

6. 💬 **OQ-WS6: For the links, root `.gitignore`, `.git/info/exclude`, or a yolo-owned
   parent's `.gitignore`?** Decides what `git status` shows after a launch under B, and which
   file yolo becomes a writer of. **(a) root `.gitignore` append**, write-once by content check
   — `yolo init`'s precedent; visible as `M .gitignore` once, then quiet for every clone once
   committed; but it is a launch editing a tracked file. **(b) `.git/info/exclude` append** —
   invisible, per clone, never committed; but it is the blind cell, the file
   [`../reference/host-execution-from-the-workspace.md`](../reference/host-execution-from-the-workspace.md#the-two-axes)
   calls load-bearing precisely because writing it moves things from visible to invisible; it
   is shared across worktrees; and `workspace_readonly` may lock it. **(c) a `.gitignore` inside
   each parent directory yolo itself created** — the `.yolo/` trick; clean when yolo made
   `.codex/`, impossible when the repo already has `.codex/`, so it needs (a) or (b) as a
   fallback anyway.

   <!-- vantage: oq id=OQ-WS6 leaning="(a) root .gitignore append, write-once and never fought — the yolo init precedent, and P4: the change is visible in a diff, and once committed every clone is quiet without yolo writing the blind cell." -->

   _Leaning:_ **(a).** P4 decides it: a visible one-line edit the user commits once beats an
   invisible per-clone write to the file that defines invisibility. The write-once rule
   `.yolo/.gitignore` already follows — a user who removes the line is not fought — carries
   over unchanged.

   **Answer:**
   > _(empty — fill in when decided)_
