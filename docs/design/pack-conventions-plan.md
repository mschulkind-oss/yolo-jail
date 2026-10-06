---
title: "Plan sketch: pack-file conventions"
date: 2026-10-05
status: draft
stage: SKETCH
next: "An implementation-plan pass re-reads the tree once OQ-PC1 and OQ-PC2 are ruled; C1 and C4 need neither and can be planned now"
depends-on:
  - pack-conventions.md#OQ-PC1
  - pack-conventions.md#OQ-PC2
tags: [packs, manifest, conventions, plan]
summary: "Parking lot for build-level detail behind pack-conventions.md: where each convention decodes or expands, which existing readers must see the expanded set, the tests each owes, and the order. Not a hand-off; the design wins on behavior."
---

# Plan sketch: pack-file conventions

**Status:** 2026-10-05 — incomplete, and unstable while questions are open. Do not build from it.

**Precedence:** [`pack-conventions.md`](pack-conventions.md) wins on behavior. This file holds
settled detail the design does not need.

---

## Where things go

- **C1, registration of every direct-child landing.** It rides
  [`pack-pi-resources-plan.md`](pack-pi-resources-plan.md)'s emission step, at the same place
  (`packload.ResolveDestinations` / `SlotLanding`). The emission walks every `files` contribution
  of every selected pack whose resolved `into` has the slot as its parent, not only the addressed
  ones. The written-entry replacement asks `loadsTree` over the contributing pack's list
  contributions and posture lists. The synthesized entry joins the contributing pack's
  `ListContributions()` on the resolved copy, never on `origDecl`.
- **C1's lint text.** `LintPatchedTrees` takes the registering slots of the set it is given (the
  shipped packs at `yolo pack lint`), and names a folder from a declaration instead of the
  literal `pi/settings` it prints today.
- **C4.** `AutonomyPosture` gains `Managed map[string]json.RawMessage` (or an ordered map, to keep
  declaration order for later-wins), decoded into the same surface patches `manifest.DecodeSurfaces`
  yields, minus path and codec. `foldPostureManaged` needs nothing new. `packoverlay.Collect` skips
  NS-D21's comparison for an entry that came from the map. A same-surface clash between the two
  spellings is a `validateAutonomyPosture` problem.
- **C2, C3.** Both join `packload.GovernedSources`, never a second reader. C3 can expand at decode
  time from the pack root; C2 needs the selected identities, so it expands inside
  `ResolveDestinations`.
- **Provenance.** A non-serialized field on `packdecl.Contribution`, like `ForkedBy`, read by the
  footprint's detail column and the JSON outputs.
- **Features.** One `namedFeatures` entry per convention, each with its
  `TestEveryNamedFeatureIsReadable` case.

## Check before relying on it

- That the patched-tree pipeline reads the resolved copy's list contributions at every call site
  (`owningAgentPack`, `LintDuplicateLoads`, the tree arm of a launch, `yolo host apply`), so a
  registration counts as a written entry everywhere.
- What an empty posture left behind by a skipped `managed` does on the tolerant path today
  (kept, refused, or ignored), for the design's risk row.
- Whether `yolo check` or the boot's surface loop would catch the shared-dir hook mismatch the
  design found only at boot, before saying no host-side check exists in a user-facing doc.

## Tests the build owes

- C1: a direct-child patched tree with no written entry gets exactly one registration at both
  notches; a written entry inside the tree suppresses it; a deeper tree warns and registers
  nothing; deleting the emission call site turns the test red.
- C4: every shipped posture moved to the map renders byte-identical files; a same-surface clash
  is refused; an older read keeps the posture without `managed` and names it.
- Each convention: an older-yolo read of a pack using it launches and names the skip.

## Order

1. C1 with pack-pi-resources' build.
2. C4, and the shipped postures in the same commit.
3. C2 after [OQ-PC1](pack-conventions.md#OQ-PC1); C3 after [OQ-PC2](pack-conventions.md#OQ-PC2).
