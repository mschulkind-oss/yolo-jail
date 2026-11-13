---
title: "Plan: the briefing diet"
date: 2026-11-13
status: draft
stage: SKETCH
next: "Tasks 1, 2 and 3 built; Tasks 4 and 5 for 0.18.0"
depends-on: [briefing-diet.md#BD-D11]
tags: [briefing, skills, packs, plan]
summary: "Seed of the build hand-off for the briefing diet: a provisional map and task slices from the files read during design, to be completed by a planner against the tree."
---

# Plan: the briefing diet

**Status:** 2026-11-13. Seeded during design at `450287258`; not a hand-off, and incomplete
until the design is accepted. The map below lists only files read while writing the design. A
planner re-reads each at the build's commit, and writes the failing tests, before any task is
handed to an implementer.

**Design:** [`briefing-diet.md`](briefing-diet.md). Precedence: the design wins on behavior,
the tree wins on fact, this file is advice and is the first thing to be wrong.

**Stop and report, do not commit**, when a test does not fail the way its task predicts, a named
symbol is gone, a named file is dirty with someone else's work, the fix needs a file outside the
task's fence, or a gate fails outside the task's scope.

## Slices

| Slice | Tasks | Ships in | Waits on |
| :--- | :--- | :--- | :--- |
| 1 | 1 (core and topics), 2 (order, user block first: [BD-D11](briefing-diet.md#BD-D11)), 3 (shipped packs' pointers and skills) | 0.18.0 | Nothing |
| 2 | 4 (the launch's size line), 5 (reference documents) | 0.18.0 (the owner, 2026-11-13) | Slice 1 |

Tasks 1 and 3 touch disjoint files and may be built in parallel. Task 2 lands after Task 1,
since both edit the composer's call site.

## Map (provisional)

| Path | Change | Task |
| :--- | :--- | :--- |
| `internal/jailcontent/briefing.go` | `BriefingContent` renders the core; the section bodies it drops (`hostRunSection`, loopholes, blocked tools, context mounts, limitations, skills) become topic renderers | 1 |
| `internal/jailcontent/persistencesection.go` | The storage-classes text is rendered into `topics/storage.md`; the core takes one line from the same map | 1 |
| `internal/jailcontent/mechanicsrender.go` | Topic files beside the recipes: a topic list like `mechanicsRecipes`, an applies-check like `recipeApplies`, a renderer per topic | 1 |
| `internal/jailcontent/builtinskills/templates/index.tmpl` | A Topics list above Recipes; the description names the subjects | 1 |
| `internal/jailcontent/builtinskills/templates/mechanics-*.golden` | Regenerated | 1 |
| `internal/jailcontent/mechanicsstage.go` | `RefreshMechanicsFiles` also writes `topics/` on an attach | 1 |
| `internal/jailcontent/briefingbudget_test.go` | New: the core and description budgets | 1 |
| `internal/cli/run/prepare.go` (~`:383`–`:425`) | The per-destination loop composes two blocks; passes the destination's skills path to the core | 1, 2 |
| `internal/jailcontent/briefing.go` | `ComposePackBriefings` and `ComposeBriefingSections` take the shipped/user split | 2 |
| `internal/entrypoint/hostbriefing.go` (~`:318`), `internal/cli/applyhostbriefings.go` (~`:140`) | The host notch composes the same two blocks | 2 |
| `packs/git/briefing/git.md`, `packs/github/briefing/gh.md` | Replaced by the pointers in the design's packs section | 3 |
| `packs/git/skills/yolo-git/SKILL.md`, `packs/github/skills/yolo-gh/SKILL.md` | New: today's prose, under a description of at most 200 bytes | 3 |
| `packs/git/pack.toml`, `packs/github/pack.toml` | A `[[skills]]` contribution with `describes = ["intercept"]` | 3 |
| `internal/packdecl/contributes.go` (`describesProblems`, ~`:3231`) | Accept `describes` on `skills` | 3 |
| `packs/claude/briefing/*.md`, `packs/pi/briefing/*.md`, both `pack.toml` | One pointer each, at most 250 bytes; pi's 2,012-byte worktree text moves to a skill the pack ships | 3 |

## Task 1: the core and the topic files

Built in the lane that follows this plan; the rows below replace the map's Task 1 rows. The
**core** and **topic file** are the design's terms
([§1](briefing-diet.md#1-defined-terms), [§5](briefing-diet.md#5-where-the-rest-lives)).

| Path | What it holds now |
| :--- | :--- |
| `internal/jailcontent/briefingcore.go` (new) | The core's bullets and pointer: workspace, what survives a restart, network, what is not in here, asking the human, the blocked line, then the pointer to the skill |
| `internal/jailcontent/briefingtopics.go` (new) | `BriefingTopics(in)`, the one renderer of every topic, in index order: storage, network, mounts, host requests, loopholes, blocked tools, limits. A topic is rendered only when its subject exists: no mount, no `mounts.md` |
| `internal/jailcontent/briefing.go` | `BriefingContent` is the header, then the alerts, then the core; with `InlineTopics`, the topics follow it in full. The jail headers lose the tooling line; the host notch's header keeps it |
| `internal/jailcontent/mechanicsrender.go`, `templates/index.tmpl` | The skill holds `topics/<file>`, and its index lists them above the recipes, with the tooling line and the other two built-in skills |
| `internal/jailcontent/mechanicsstage.go` | `RefreshMechanicsFiles` writes every topic, and removes a topic file the facts no longer render, after every write has landed |
| `internal/cli/run/prepare.go` | Sets `InlineTopics` when this entry did not write the topics. Renders the core per destination, naming that agent's own `SKILL.md`. Passes each block's replacement and guard through |
| `internal/jailcontent/briefingbudget_test.go`, `briefingtopics_test.go` (new) | The budgets, and the topic, pointer and inline-fallback tests |

Decisions made while building, each the only answer the design left:

- **The alerts stay right after the header**, as today. The design's order of blocks is
  Task 2's.
- **Inline fallback.** When no fact sheet reached the entry (a legacy attach), or the skill
  render failed and left the prior tree, the topics are appended to the briefing under `##`
  headings, and the pointer names no file. This is the trap below, closed.
- **A topic's text is one renderer's output** for both the skill file and the inline form, so
  the two cannot disagree.
- **The macos-user core is the same bullets, tighter**: a one-line autonomy sentence, the
  context dir as `$YOLO_CONTEXT_DIR`, and the durable dir relative to the workspace. Without
  that, the seatbelt fixtures measured 2,089 and 2,126 bytes.
- **A native runtime's topics describe host networking** whatever mode the caller resolved,
  since the launcher applies host networking to every native runtime. A native launch with no
  applied home gets no `/home/agent`.
- **The bridge paragraph's macOS-podman clause about `unknown`** is rendered only on a macOS
  podman launch; elsewhere it says that `unknown` means the launcher did not ask.
- **The credentials sentence** says "when a pack says" where it said "when a section below
  says", since the sections it meant are no longer below it.
- **`BlockedTool` gained `Replacement` and `Guard`**, so the core's blocked line names a
  replacement per tool, groups the guards by kind, and states the `rg -r` trap only when a
  replacement is `rg` (BD-D7 in the [Decision Ledger](briefing-diet.md#decision-ledger)).
- **The core lists more than four ports as a count** and points to the network topic.

## Task 2: two blocks, the user's first

Built in its own lane on 2026-11-13, after Task 1, under the owner's ruling
[BD-D11](briefing-diet.md#BD-D11) and [BD-D8](briefing-diet.md#decision-ledger). The **user
block** and **yolo's block** are the design's terms ([§7](briefing-diet.md#7-order)).

**Decisions taken in the build** (implementation decisions; each is reversible):

- **The alerts stay at the top of yolo's block**, after the header and ahead of the core, not
  ahead of the user's prose. The design puts them in yolo's block ([§7](briefing-diet.md#7-order), BD-D5), the ruling puts
  the user's rules first, and an alert is yolo's text. Moving them above the user block would put
  yolo's text first again on every launch that has one. The cost is that a provisioning alert or a
  handoff sits after a long user block; Claude loads the whole file, and yolo's block is the part
  an agent that cuts from the end loses first either way.
- **One composer for both notches:** `jailcontent.ComposeBriefingFile` takes the host file,
  `agents_md_extra`, yolo's base and every pack's section, and splits the sections on their
  `Shipped` mark. The jail and `yolo host apply` both call it, so one pack set reads the same at
  both.
- **Shipped means `packload.Pack.Official`.** A pack read back from a running jail's tree on an
  attach gets the mark from the tree's layout (a pack under `_official/` is embedded), since the
  tree's record does not carry the origin. Without that, every attach would move yolo's pointers
  into the user block.
- **The host notch's `agents_md_extra` leaves its base.** `HostBriefingBase` is the header alone;
  the user-scope value travels as `HostBriefingRequest.Extra`, and the adoption gate composes from
  the same request, so it compares a file against the bytes the render writes.
- **The `after` file is read, not prepended.** `jailcontent.PrependHostBriefing` is replaced by
  `ReadHostBriefing`; the file's text opens the user block. An existing file with only whitespace
  composes as no user prose, so it adds no rule.

**Steps:**

1. `internal/jailcontent/briefing.go`: `PackBriefing.Shipped` and `BriefingSection.Shipped`;
   `PackBriefingSections`; `BriefingParts` and `ComposeBriefingFile`; `ReadHostBriefing` in place
   of `PrependHostBriefing`; `HostBriefingBase(isMacOS)` without `extra`; `ComposeBriefing`
   removed. `ComposePackBriefings` composes through `ComposeBriefingFile`. Pinned by
   `TestComposeBriefingFile` and `TestReadHostBriefingReportsAReadFailureButNotAbsence`.
2. `internal/cli/run/packs.go`, `internal/cli/run/packtree.go`: each `PackBriefing` carries
   `Shipped: p.Official`; `loadPackTree` marks a pack under `_official/` official
   (`markOfficialFromLayout`). Pinned by `TestAnAttachReadsEachPacksOriginBackFromTheTree`.
3. `internal/cli/run/prepare.go`, `internal/cli/run/hostsourceunread.go`: the per-destination
   loop reads the `after` file (`readHostBriefing`) and composes through `ComposeBriefingFile`.
   Pinned first, failing, by `TestTheUsersProsePrecedesYolosCoreInTheJail` and
   `TestAForkedShippedPackIsUserProse` in `internal/cli/run/briefingorder_test.go`.
4. `internal/entrypoint/hostbriefing.go`, `internal/cli/applyhostbriefings.go`: the host notch
   composes through `ComposeBriefingFile`, with `HostBriefingRequest.Extra` and
   `HostBriefingAdoptionsFor`. Pinned first, failing, by
   `TestApplyHostPutsTheUsersProseBeforeYolosBlock` in
   `internal/cli/applyhostbriefingorder_test.go`, and by
   `TestComposeHostBriefingsPrependsTheUsersAfterFile`.
5. The composed-block budget: `TestYolosBlockAloneHasNoRuleAndFitsItsBudget` drives the
   launch's staging and briefing write for Claude with `claude`, `git`, `github` and
   `guardrails`, one forwarded port and the durable directory, and holds yolo's block to 2,600
   bytes with no rule. `TestApplyHostWithNoUserProseHasNoRule` is the host half of "no rule".

## Task 3: shipped packs' pointers and skills

Firmed against the tree on 2026-11-13 and built in the same lane. Each step names the file and
the test that pins it.

**Decisions taken in the build** (implementation decisions under
[BD-D2](briefing-diet.md#decision-ledger) and BD-D3; each is reversible):

- The skill names are `yolo-git`, `yolo-gh`, `yolo-claude` and `yolo-pi`, the subject being
  the tool or agent the manual is about.
- The claude and pi skills carry an `agents` audience and no `describes`: their text is about
  the agent's own harness, true at the host too, so they reach the host notch as their
  briefings did. The git and github skills declare `describes = ["intercept"]` and are withheld
  at the host exactly as their briefings were.
- Each pointer stands alone: it states the one rule an agent needs before it reads the skill
  (push to a branch of your own; exit 64 is a refusal; how to wait on the host) and names the
  skill in bold.
- The skill texts keep the old briefing's words. A cross-reference to a briefing section that
  may not be loaded beside the skill ("as **Running a command on the host** says", "the storage
  guidance above") is rewritten to stand alone.
- An older yolo (v0.17.1) refuses `describes` on `skills`. That is a known release break, listed
  in `knownReleaseBreaks` in `packs/releasedecode_test.go` with the attach guard that keeps an
  older jail on its own staged tree.
- Known edge: a user's `shipped_skill_prefix` renames shipped skills at the host, so a pointer's
  bold skill name can differ from the installed directory there. The jail applies no prefix.
  Left as is; the agent finds the skill by its description.

**Steps:**

1. `internal/packdecl/contributes.go`: `describesProblems` accepts `describes` on `skills`
   content; it refuses it on a skills destination and refuses `briefing` as a skill's subject.
   Pinned first, failing, by `TestDescribesIsAcceptedOnBriefingContent` and
   `TestDescribesIsRefusedOffBriefingContent` in
   `internal/packdecl/describes_test.go`.
2. `internal/packload/governance.go`: `GovernedSkillsFor` returns the sources a skills
   contribution governs, so a synthesized host copy finds its governor's `describes`.
3. `internal/hostskills/compose.go`: `ComposeHostSkillsFor` takes the apply's census
   (`entrypoint.HostDelivery.Delivers`) and skips a contribution whose governor describes a kind
   the host withholds; `WithheldKinds` is the shared predicate. Pinned by
   `TestASkillsTreeAboutAJailOnlyKindIsNotComposedAtTheHost`.
4. `internal/cli/applyhostskills.go`, `internal/cli/apply.go`: the apply passes its census to
   the skills composer and to the destinations report. `internal/cli/applyhostbriefings.go`
   names a withheld skills tree in the notch line beside a withheld briefing. Pinned by
   `TestApplyHostWithholdsTheGitHubBriefingFromARealHome` and its verbose sibling.
5. Pack content: `packs/git`, `packs/github`, `packs/claude`, `packs/pi` each get a pointer
   under 250 bytes in `briefing/` and the old text in `skills/yolo-<subject>/SKILL.md`, with
   a `[[skills]]` line in `pack.toml`. The claude and pi packs' two briefing files each become
   one. Pinned by `TestTheGitHubPackBriefsEveryAgentOnGHInTheJail`,
   `internal/cli/run/agentworktreeprose_test.go`, `TestNoShippedPackOffersTheRemovedApproveFlags` and
   `integration/packaudience_test.go`.
6. `internal/packload/shippedprosebudget_test.go`: the shipped-pack prose budget (250 bytes per
   destination) and the skill-description budget (200 bytes). Each failure names the fix.
7. `internal/cli/run/userprosenotcapped_test.go`: a local and a fetched pack's long prose is
   composed whole at both notches.
8. Docs: [`pack-system.md`](../reference/pack-system.md) (`describes` on `skills`), the github
   pack's README, and the built columns of the ledger rows that named the old `gh.md`.

## Seed notes for the planner

- **Search:** how a `skills` contribution with no `into` in a content pack reaches each agent's
  skills directory in a jail and at `yolo host apply`, and where the host notch would apply
  `describes` to it. The briefing kind's handling is the shape to mirror.
- **Search:** which fact tells a shipped pack from a configured one at the composer
  (`selected_by_default` is "honored for shipped packs only", so the origin is known somewhere
  in `internal/packload`).
- **Search:** how the per-destination loop in `prepare.go` can learn the skills `into` of the
  same agent identity. `jailcontent.PackSkillTargets` carries `SkillTarget` values with an agent.
- **Search:** every test asserting a sentence of today's body. Read during design:
  `internal/jailcontent/*_test.go` (briefing, briefingapplied, briefingports, briefingprofile,
  briefingwrong, environmentblock, persistencesection, hostnix, iopriority, contextcopies,
  attachskewsection, setupbriefing), `internal/cli/run/{briefingparity,ctxmountsoptional,durabledir,loopholegrantlaunch}_test.go`,
  `internal/macosuser/briefingresources_test.go`, and the `integration/` files that read a
  briefing (`rg -l 'AGENTS.md|CLAUDE.md|briefing' integration`). Each is **rewritten** to assert
  the same fact where it now renders, not relaxed until green.
- **Trap:** `BriefingContent` has no full-output golden; only the jail-notch header bytes are
  pinned. The title and framing sentences keep their bytes; the tooling line leaves the header,
  so the pinning test is rewritten with it.
- **Trap:** an attach refreshes a running jail's briefing and its mechanics files in two
  places. The core must not point at `topics/` unless the same entry wrote them.
- **Trap:** the nix build sees tracked files only; `git add` the new pack skill files before
  any nested verification.
- **Trap:** `packs/` is embedded; a new `skills/` directory in a pack must be inside the
  embed and the `goSrc` fileset, which already includes `packs/`.
- **Constraint:** tests never start an agent. The agy observation in the design's done list is
  the maintainer's.
- **Budget test shape:** one table test over a reference fact sheet per setup, asserting
  `len(core) <= 1800`; one walk over the embedded packs asserting each pack's prose per
  destination `<= 250`; one composed-file test through the launch's own composer asserting
  yolo's block `<= 2600`; one walk over shipped skill descriptions asserting `<= 200`. A failure
  prints the bytes over and the three longest lines.
- **Gates:** `go test -short ./internal/jailcontent ./internal/cli/run ./internal/packdecl
  ./internal/packload ./internal/entrypoint`, the integration tests the search above names, then
  `just check-ci` once at landing. This is a launch-path change: the full integration suite runs
  once on the combined tree.

## Ships with

- [`agent-briefings.md`](../reference/agent-briefings.md): the four-part order, the emission
  list of the jail-managed body, and "pack prose appended last" all describe the old shape.
- [`jail-mechanics-skill.md`](../reference/jail-mechanics-skill.md): the skill gains topic
  files.
- [`pack-system.md`](../reference/pack-system.md): `describes` on `skills`.
- [`AGENTS.md`](../../AGENTS.md)'s Skills bullet, if the built-in list it names changes.
- No changelog entry: it is written at release time.

## Don't

- Don't add a config key for any budget.
- Don't trim, cap or reorder a non-shipped pack's prose.
- Don't declare a size limit for an agent other than Claude's measured 40,000 characters.
- Don't add a `yolo guide` or `yolo docs` command.
