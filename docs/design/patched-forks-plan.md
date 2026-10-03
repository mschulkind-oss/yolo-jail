---
title: "Patched forks — implementation sketch"
date: 2026-10-03
status: draft
stage: SKETCH
tags: [plan, packs, programs, forks, build]
summary: "Where the patched-fork mode's code would land, read against the tree at 026fca672, and the traps found while designing it. A parking lot for the design, not a hand-off: nothing here may be built from while it is a sketch."
next: "Promote against the tree once OQ-PFK1 to OQ-PFK3 are ruled, with the implementation-plan skill"
depends-on:
  - patched-forks.md#OQ-PFK1
  - patched-forks.md#OQ-PFK3
---

# Patched forks — implementation sketch

**Status:** 2026-10-03 — incomplete, and unstable while questions are open.

**Design:** [`patched-forks.md`](patched-forks.md). **Precedence:** the design wins on behavior,
the tree wins on fact. No design decision is made here; an entry that would need one is a question
in the design.

## Map, sketched

Read at `026fca672`. Every row is a pointer to check before relying on it.

| Where | What the mode touches |
| :--- | :--- |
| [`packdecl/contributes.go`](../../internal/packdecl/contributes.go#L48-L88) | `patches` and `follow` beside the four fork fields; their placement refusal in `forkFieldPlacementProblems` ([`fork.go:67`](../../internal/packdecl/fork.go#L67)) |
| [`packdecl/fork.go`](../../internal/packdecl/fork.go#L280-L286) | the recipe's canonical array gains the series digest for a patched fork only; a plain fork's bytes must not move |
| [`packload/forks.go`](../../internal/packload/forks.go#L53-L65) | `Fork` gains the series (read from the fork pack's `Root`) and the follow rule; `ApplyForks` carries the digest onto the base's `Install` so every recipe reader gets one value |
| [`packsrc/forklock.go`](../../internal/packsrc/forklock.go#L40-L52) | the entry's four additive fields; the mode marker an entry needs so the other mode reads it as no pin |
| [`packsrc/forkpin.go`](../../internal/packsrc/forkpin.go#L79) | the check beside `PinForks`: stamp read before any git, then `refreshItems` |
| `packsrc` (new) | the check stamp and the check record, beside `stamps/` in the pack store |
| [`cli/forkbuild.go`](../../internal/cli/forkbuild.go#L310-L326) | `checkOutForkSource`'s patched arm: scratch repository, `git am --3way`, patched tree id; the receipt's `series` and `tree` |
| [`cli/forkbuild.go`](../../internal/cli/forkbuild.go#L177-L212) | `buildForksForLaunch` asks for the pin's recipe, runs the advance, and writes the pin after the admit |
| [`entrypoint/buildreceipt.go`](../../internal/entrypoint/buildreceipt.go#L44-L49) | two receipt fields |
| [`cli/forkpin.go`](../../internal/cli/forkpin.go#L38) | `pack update`, `pack install` and `pack status` arms for a patched fork |
| [`cli/pack.go`](../../internal/cli/pack.go#L257-L280) | the `rebase` subcommand |
| [`hostfloor/ensure.go`](../../internal/hostfloor/ensure.go#L155-L162), [`floor.go`](../../internal/hostfloor/floor.go#L206-L215) | the pin reader returns the pin's recipe; `servesANearMiss` compares against it |

## Traps

- **The check cannot reuse the per-mirror step's stamp.** `refreshMirror` runs `rev-parse` before
  it reads its stamp ([`refresh.go:316-335`](../../internal/packsrc/refresh.go#L316-L335)), and the
  launch's pack refresh can leave that stamp fresh. The fork's own stamp is read first.
- **The check must not sit in `noteForkPins`.** That runs above the dispatch on every launch,
  attach included ([`run.go:319`](../../internal/cli/run/run.go#L319)); the check belongs with the
  build, in the fresh-launch slot ([`run.go:1330`](../../internal/cli/run/run.go#L1330)).
- **No lazy fetch outside a receiving run** ([`store.go:322-325`](../../internal/packsrc/store.go#L322-L325)):
  the scratch repository's checkout and the three-way fallback's preimages are receiving runs, with
  fsck, or the blobless mirror answers "missing".
- **Hold the upstream mirror's lock across the apply.** The scratch repository borrows the mirror's
  objects; an explicit fetch there may start a gc.
- **The build sees no `.git`.** Keep the scratch repository's git directory outside `src/`, as a
  plain fork's copied tree has none.
- **An older yolo rewriting the lock drops the new fields** (`LoadForkLock` then `Save`,
  [`forklock.go:77`](../../internal/packsrc/forklock.go#L77)); the design's reading of an entry
  without `recipe` is the recovery, and needs its own test.
- **`resolveForkBuild` is newest-then-check** ([`capturematerialize.go:311-332`](../../internal/cli/capturematerialize.go#L311-L332)):
  correct only because a failed candidate admits nothing. A test should fail if a failed build ever
  admits.
- **The call-site rule** (AGENTS.md, Testing): the trigger's test must fail when the check's call in
  the fresh-launch slot is deleted, as `TestALaunchWiresTheForkBuildTrigger` does for the build.

## Waiting on the design

- How the launch path is shaped after the check, waiting or not, is blocked on
  [OQ-PFK3](patched-forks.md#OQ-PFK3).
- Whether the floor and the jail ever serve a pin while a candidate fails is blocked on
  [OQ-PFK1](patched-forks.md#OQ-PFK1); the map above assumes its leaning.

## Measurements to make

- A real `git am --3way` over a series against a **blobless** mirror, with and without
  `base-commit:`: the design's preimage claim is UNMEASURED there.
- The maintainer's own pi series against the newest upstream release: apply time on the host, and
  the build time the design quotes from a stand-in and one host log.
