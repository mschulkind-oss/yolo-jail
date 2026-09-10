---
title: "Report tiers — implementation sketch"
date: 2026-09-10
status: draft
tags: [plan, sketch, cli, host-apply, reporting]
summary: "Parking lot for the implementation material that surfaced while writing report-tiers.md — what the tests pin, where the survey grows, which pointers move with the default view. A sketch: not a hand-off, and nothing here decides behavior; the design wins on any disagreement."
---

# Report tiers — implementation sketch

**Status:** SKETCH — incomplete, and unstable while questions are open. Opened 2026-09-10 beside
[`report-tiers.md`](report-tiers.md), which owns every behavioral decision; where the two disagree,
**the design wins**. This is **not a hand-off artifact** — nothing may be built from it while it says
SKETCH. The `implementation-plan` skill owns what it must become first, against the tree, once the
design's questions are ruled.

Every anchor below was read on 2026-09-10 at `48f47e56`.

## The survey grows

- `hostChange` (`internal/cli/hostapplysurvey.go:22-33`) gains a tier and per-destination loss
  lists; the survey gains counters — replaced keys with a file count, dropped entries by name, the
  first-apply flag. The six feeders are the existing `survey.note` sites: `apply.go:427` (config),
  `applyhostskills.go:368` (skills, via `printSkillResult`), `applyhostbriefings.go:117/159/172`,
  `applyhostfiles.go:56`, `hostapply.go:133` (`noteWrapperPlan`). Counts derive from
  `HostRenderResult.Overwrites` / `EntryLosses` / `FirstApply` (`internal/entrypoint/hostrender.go:58-120`),
  never from printed lines.
- `Changes()` is the launch gate's whole question and must not change meaning; `Summary()`'s pin
  (`hostapplysurvey_test.go:62`, wants `0 would change`) stays true if the verdict block keeps the
  phrase.

## What the tests pin, and the two holes

- **Census** (`applyhostcensus_test.go:57-63`) asserts `strings.Contains(report, kind)` per kind
  in the closed set — a one-line tier-1 passes. Add a test that each refused kind is named
  **exactly once** in the default view, or step 2 of the design's build order can regress to
  per-contribution printing silently.
- **Skills count pins** (`applyhostskillscompose_test.go:353-361`) use a one-skill fixture and
  `countLines(report, "would move to your local pack") == 1`. A grouped line still contains the
  phrase once. Add a two-skill fixture asserting **one** grouped line carrying **both** names, or
  the grouping is untested.
- **Observe exits 0** (`applyhostmcp_test.go:218-236`). Blocked on
  [OQ-RO5](report-tiers.md#OQ-RO5).
- Nothing pins the refusal text (three phrases, zero hits across `*_test.go`, 2026-09-10) — free to
  move behind a flag.

## Pointers that move with the default view

- `hostapplygate.go:236` — *"(`yolo host apply --dry-run` shows exactly what changes in each.)"*
  names the detail view. Blocked on [OQ-RO2](report-tiers.md#OQ-RO2).
- `apply.go:545` — the footer names `--assert` and should also name the detail flag. Blocked on
  [OQ-RO2](report-tiers.md#OQ-RO2).
- `applyUsage` (`apply.go:837-860`) and `hostUsage` (`host.go:22`) gain the flag and, if ruled,
  the JSON line; `TestUsageListsEveryParsedFlag` (self-documenting-cli enforcement item 2) will
  refuse a parsed flag the help does not name.

## Printer

- `richtext.Printer` is `Print`/`Printf` only (`internal/richtext/richtext.go:100-113`). Do not
  add levels there — the design rejects that in
  [§7](report-tiers.md#7-alternatives-considered). A small package above it — tiered lines and
  verdict accumulation — that both `internal/cli` (apply) and `internal/cli/run` (notices,
  `console.go:18-31`) can import. Its tier→style map takes
  [`cli-visual-polish.md`](../plans/cli-visual-polish.md)'s semantic table; strip parity must hold
  (`TestPsColorParity` is the model).
- `⚠ ` vs `⚠  ` (`preflight.go:48` vs `:222`/`:283`) is the polish plan's; note only.

## Launch side

- **`launch.log`**: mirror `attachBootLog` (`internal/entrypoint/bootlog.go:79-104`) on the host —
  wrap `run.Options`' `Stdout`/`Stderr` in a `MultiWriter` to `<workspace>/.yolo/launch.log`; header
  shape from `bootlog.go:114-131`; retention as `host-perf.log` (`perf.MaxRuns`). Do not route the
  raw `[y/N]` prompt write (`preflight.go:286`) through anything that alters its bytes.
- **Frozen goldens a launch-line change can touch**: `internal/cli/run/testdata/final_cmd_bash.txt`
  (provisioning sub-steps); `integration/imagebuildfailure_test.go:123-128` (image lines);
  `integration/concurrentlaunch_test.go:187,202` (waiting/attaching). The boot-catalog one-liner
  touches none of them but is a golden update in spirit — sign-off.
- **Boot catalog compression**: `catalog.go:149-158` prints through `e.warn`; the one-liner goes to
  `e.Stderr` and the list to `e.LogOnly` (`env.go:60-70`), which already exists for exactly this
  split. Blocked on [OQ-RO3](report-tiers.md#OQ-RO3).

## JSON

- `parseOutputFormat("apply", args, errw)` (`internal/cli/outputformat.go:59`) gives both
  spellings and the exit-2 refusal of a bad value. Refuse when `--assert` is also present: exit 2,
  stdout empty (self-documenting-cli item 3). The survey struct is the document; `internal/outfmt`
  holds the format vocabulary. Zero packs must still emit a document. Blocked on
  [OQ-RO4](report-tiers.md#OQ-RO4).
- The version banner is already stderr (`internal/banner/banner.go:24-34`), so a JSON stdout is
  clean without touching it.

## Zero packs

- `apply.go:167-201` returns before the roll-up at `:540-545`. Restructure so the verdict and footer
  print on that branch too. No decision involved — the design says every observe ends in a verdict.

## Noticed, not this design's

- `NO_COLOR`: zero occurrences in `internal/` and `cmd/` (2026-09-10);
  [`cli-visual-polish.md`](../plans/cli-visual-polish.md) claims it as an invariant. Route there.
- The launch's stdout/stderr split (progress → stdout, notices → stderr) —
  [§2.4](report-tiers.md#24-the-launch-stream-both-halves) records it; not this work.
