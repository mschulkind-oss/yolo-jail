---
title: "Plan: the briefing diet"
date: 2026-11-13
status: draft
stage: SKETCH
next: "Planner: complete Tasks 1 and 3 against the tree; Task 2 after OQ-BD1 is ruled"
depends-on: [briefing-diet.md#OQ-BD1]
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
| 1 | 1 (core and topics), 2 (order), 3 (shipped packs' pointers and skills) | 0.18.0 | Task 2 only: [OQ-BD1](briefing-diet.md#OQ-BD1) |
| 2 | 4 (the launch's size line), 5 (reference documents) | After 0.18.0 | Slice 1 |

Tasks 1 and 3 touch disjoint files and may be built in parallel. Task 2 lands after Task 1,
since both edit the composer's call site. If [OQ-BD1](briefing-diet.md#OQ-BD1) is still open at
the release, slice 1 ships Tasks 1 and 3: yolo's share then falls from 26,867 bytes to about
2.2 KB with the order unchanged.

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
