---
title: "Plan sketch: pack-file conventions"
date: 2026-10-05
status: draft
stage: SKETCH
next: "Nothing to plan until OQ-PC3, OQ-PC1 and OQ-PC2 are ruled, except PC-D4, which rides pack-pi-resources-plan.md's emission step; an implementation-plan pass then re-reads the tree"
depends-on:
  - pack-conventions.md#OQ-PC1
  - pack-conventions.md#OQ-PC2
  - pack-conventions.md#OQ-PC3
tags: [packs, manifest, conventions, plan]
summary: "Parking lot for build-level detail behind pack-conventions.md: where each convention expands, which existing readers must see the expanded set, the tests each owes, and the order. Not a hand-off; the design wins on behavior."
---

# Plan sketch: pack-file conventions

**Status:** 2026-10-05, corrected after review the same day. Incomplete, and unstable while
questions are open. Do not build from it.

**Precedence:** [`pack-conventions.md`](pack-conventions.md) wins on behavior. This file holds
settled detail the design does not need.

---

## Where things go

- **The expansion point ([PC-D12](pack-conventions.md#PC-D12)).** One function over a selection,
  called beside `packload.ApplyForks` wherever that runs: `config.PackSelection.applyForks`
  (inside `config.SelectPacks`, which the launch's `stagePacksInto`, `cli.selectHostPacks` and
  `yolo check`'s pack checks call), the entrypoint's `loadPackRoot`, and an attach's
  `run.loadPackTree`. It returns clones, as `ApplyForks` does, whose `Decl` holds the implied
  contributions and whose `origDecl` holds the written ones; `ResolveDestinations` keeps an
  `origDecl` that is already set. `yolo pack lint` and `yolo pack footprint <dir>` call it over
  `packload.Embedded()` plus the pack ([PC-D10](pack-conventions.md#PC-D10)).
- **C1, if [OQ-PC3](pack-conventions.md#OQ-PC3) is A.** The readers that must see the
  registration all take the selection as loaded today:
  - `packload.PatchedTrees` and `owningAgentPack`, through `run.notePatchedTrees`,
    `cli.advanceHostTrees`, `cli.hostTreeGate`, `forkpin.go`, `capturehost.go`,
    `patchedrebase.go`, `patchedseriescheck.go`, `packlintseries.go` and the launch's tree arm in
    `run/packfiles.go`;
  - `LintPatchedTrees` and `LintDuplicateLoads`, on one pack, from `cli/pack.go` and
    `run/patchedtrees.go`;
  - `packoverlay.Collect`'s list pass, for the jail's surface render, the `yolo config render`
    preview and `config ls`'s provenance;
  - `yolo host apply`'s render.

  The registration joins the contributing pack's list contributions on the clone. The
  written-entry replacement asks `loadsTree` over the contributing pack's lists and posture lists.
  `run.packDestConflicts` must compare resolved landings, either by running over
  `ResolveDestinations` output or by applying `SlotLanding` to addressed trees inside it. Each
  single-pack lint reads the expansion of the set it is given.
- **PC-D4, no ruling.** `LintPatchedTrees` reads the registering slots of the set it is given and
  names the list from a `register`; with none it names `~/<into>` alone. The missing-`into`
  refusal in `packdecl/patchedext.go` drops its pi path.
- **C2, if [OQ-PC1](pack-conventions.md#OQ-PC1) is A or B.** `<agent>/` becomes an addressed
  `files` contribution at the expansion point, keyed on `packload.AgentNames` of the selection.
  The step records that vocabulary on each clone, and `governedBriefing` walks
  `briefing/<agent>/` for each name in it. The callers that must pass expanded packs are
  `packBriefingProses` (`run/packs.go`), `packtree.go`'s briefing refresh, `applyhostbriefings.go`,
  `borrowingSources`, `run/unmatchedaudience.go` and `cli/pack.go`'s lint.
  `reservedBriefingFiles` extends into `briefing/<agent>/` for an opted-in pack
  ([PC-D14](pack-conventions.md#PC-D14)). The opt-in's level is read by
  [PC-D13](pack-conventions.md#PC-D13)'s rule.
- **C3, if [OQ-PC2](pack-conventions.md#OQ-PC2) is A.** In the pack load (`LoadDir`, `LoadDirForUse`),
  which has the root, never in the manifest decode, which has none. The load records `origDecl`.
- **Provenance.** A non-serialized field on `packdecl.Contribution`, like `ForkedBy`, read by the
  footprint's detail column and the JSON outputs.
- **Features.** One `namedFeatures` entry per convention, each with its
  `TestEveryNamedFeatureIsReadable` case.

## Answered in review (2026-10-05)

- **Does the patched-tree pipeline read the resolved copy?** No. Every call site takes the
  selection as loaded, so C1 cannot ride destination resolution. That is why the expansion point
  moved.
- **What does an empty posture left by a skipped field do?** It validates
  (`validateAutonomyPosture` accepts one), so an older read keeps the launch flags and drops every
  key. Recorded in the design's [§7](pack-conventions.md#7-considered-and-not-proposed) row for the posture spelling, which is now [OQ-M1](manifest-language.md#OQ-M1)'s.
- **Does anything host-side match a shared-dir hook's `at` against the pack's machine `state`?**
  No. `RunPackHooks` runs only from the boot, so the defect was filed on the roadmap as a lint bug,
  and fixed by [PC-D15](pack-conventions.md#PC-D15): every host read now refuses it.

## Tests the build owes

- At each expansion point, a test through the production caller that fails when the expansion
  call is deleted.
- C1: a direct-child patched tree with no written entry gets exactly one registration at both
  notches; a written entry inside the tree suppresses it; a deeper tree warns and registers
  nothing; a landing equal to another pack's addressed landing is refused, naming both packs;
  `yolo host -- pi` stops for an implied registration whose tree is missing.
- PC-D4: the lint's text holds no agent name, with and without a registering slot selected.
- C2: a folder for a selected agent with no destination fires the no-destination report; a
  folder naming a shipped, unselected agent is refused or reported, per the ruling;
  `briefing/claude/CLAUDE.md` is refused only in an opted-in pack.
- Each convention: a read by a yolo with the skip that is older than the convention launches and
  names the skip; an opt-in at a higher level is read as the highest level known.

## Order

1. PC-D4 with pack-pi-resources' build.
2. C1, after [OQ-PC3](pack-conventions.md#OQ-PC3).
3. C2, after [OQ-PC1](pack-conventions.md#OQ-PC1); C3, after [OQ-PC2](pack-conventions.md#OQ-PC2).
