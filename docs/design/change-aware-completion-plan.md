---
title: "Sketch: map read-only completion onto the real recipe"
date: 2026-10-09
status: draft
stage: SKETCH
next: "Prove the context-input set, anchor comparison and automatic baseline fixtures against the current tree; then promote a closed first-slice hand-off"
depends-on: [change-aware-completion.md]
tags: [testing, tooling, implementation-sketch]
summary: "Existing seams and adversarial cases for a future completion implementation; not ready to build from."
---

# Sketch: map read-only completion onto the real recipe

**Status:** 2026-10-09 — incomplete source investigation; no implementation or test execution.
Written against `b2eeffc12b631d5cda108238e77a6c4cb5bb777c`; the primary speed measurements
were read separately, not copied into this worktree's older speed plan.

**Design:** [completion behavior](change-aware-completion.md). The design wins on behavior;
the tree wins on facts; this sketch is advice and is not a hand-off. A future builder must not
fill its engineering gaps by guessing. There are no first-slice owner questions.

## Before promotion

The smart role that investigates the code must close these points before a builder receives a plan:

1. **Verification context.** Trace effective inputs of short tests, lint, gofmt, official pins and
   documentation checking. Specify the exact non-secret environment/tool/version input set,
   treatment of Go env files/workspaces, tags, cgo, PATH-selected tools and ignored input readers.
   If a relevant input cannot be identified, baseline inheritance stays disabled: full route.
2. **Anchors.** Inspect the installed checker's current renderer/slugger interface and pick a
   proven extraction/comparison mechanism for heading, explicit and question IDs. Pin duplicate
   heading order and fenced syntax. No approximate heading regex may enable the shortcut.
3. **Incoming links.** Define the lightweight changed-doc/referrer walk, its parser boundaries and
   error handling. Keep additions/deletions/renames/anchor changes on full until source citations
   are genuinely covered. Semantic numbered references still require source review.
4. **Baseline state.** Reproduce worktree-specific Git administrative paths and atomic successful
   pointer replacement in detached, linked and ordinary checkouts. Prove bad/missing/nonancestor
   records cannot become no-check routes and that a failed attempt blocks stale acceptance.
5. **Stable interval.** Specify the orchestration assertion for a no-edit interval, how unsupported
   callers refuse and how known transient mutations invalidate coverage. An advisory lock alone
   is not enough. Do not impose the suite-wide lock rejected by the speed plan.
6. **Executable red fixture.** Complete an exact failing real-`just done` test, with literals,
   fake-tool argv/outcome protocol, command and expected red output. No test was run in this
   documentation stage. Promotion needs reproducible evidence, not a guessed red command.

These are agent investigations. Missing evidence schedules conservative full verification or
ordinary repair work, not an owner pause. A changed product/source-landing policy is outside them.

## Existing file map and reuse

| Existing seam | Future use; not an edit made here |
| :--- | :--- |
| [Completion recipe](../../Justfile#L481-L498) | Replace its mutating dependency with the completion front door; preserve actual caller coverage |
| [Developer/quality recipes](../../Justfile#L409-L474) | Reuse `check-ci` as read-only full fallback; leave `check` explicitly mutating |
| [Vantage wrapper](../../scripts/vantage-check.sh) | Preserve isolated output pipe and failure status; use actual changed Markdown paths |
| [Wrapper regression tests](../../scripts/test-vantage-check.py) | Follow existing fake-tool/output-failure style rather than inventing a silent wrapper |
| [Lint recipe pin](../../internal/capture/lintgate_pin_test.go) | Retain Linux/Darwin calls and Darwin checks configuration; extend front-door wiring evidence |
| [Toolchain declaration pin](../../internal/capture/gatetoolchain_pin_test.go) | Keep tool provisioning and new caller dependencies discoverable |
| [Official binary call sites](../../tools/pack-binaries/callsites_test.go) | Keep `check` and `check-ci` pin dependencies; add completion reachability, not skip exemptions |
| [Source citations](../../internal/paths/doccitations_test.go) | Preserve full-suite coverage and historical/fixture exceptions; not a ready lightweight checker |
| [Project testing/landing instructions](../../AGENTS.md#testing) | Eventual completion description only; no source-quality/runtime carve-out deletion |
| [Input research](../research/completion-check-inputs.md) | Recheck exact prose paths and direct readers when promoting |

Advice: keep a small checkout-only helper and source-named fixture beside existing script tests,
not an installed CLI subcommand. Python is a plausible default because these scripts already use
it; helper names, language, data structures and diagnostic wording are cheap choices, not owner
questions. Promotion must state exact new paths and a runnable test before implementation.

Constraint: this stage changes no helper, recipe, instruction, runtime, CLI or configuration file.
All file-map entries above describe future seams, not a built system.

## Fixture specification to complete before hand-off

Use a disposable Git fixture containing the real recipe and front-door wiring. Run the actual
`just done`; do not call only a classifier. Fake `go`, `gofmt`, `staticcheck` and doc-check commands
must append complete argv and effective mode to an external log and return controlled outcomes.
Git and just stay real. Supply immutable fixture tool-version replies and isolate caches/logs
outside task inputs. Unexpected command/argv is a fixture failure, not ignored output.

The full control must witness all four Linux/Darwin vet/staticcheck commands, non-mutating gofmt,
all-short-unit invocation and official-pin invocation. The fake's handling of `go run` must log the
pin check, never perform a real build/download. Document/guide/changelog commands get independent
outcomes so their failures cannot be masked by Go green. Missing tools/version evidence selects
full or refuses; it does not create a synthetic successful baseline.

| Source-named case | Required observable assertion |
| :--- | :--- |
| First invocation, no success record | Full control commands execute; successful clean run creates baseline automatically |
| Listed prose modification after full success | No go/gofmt/staticcheck argv; strict document and planning checks execute; inherited coverage is named |
| Bare clean invocation with empty range | Full commands execute; never “nothing changed, passed” |
| Source commit followed by prose commit | Full commands execute despite final-commit-only prose |
| Source commit then source revert then prose | Full route, even though baseline-to-HEAD net difference is prose |
| Merge-parent source changes | Full route includes parent-side paths; no first-parent-only shortcut |
| Staged, unstaged or untracked input | Paths and intended route reported; cleanliness refuses before gates and success does not advance |
| Rename/delete/copy/mode/symlink at listed name | Full route; old/new names visible; incoming reference defects fail |
| Changed heading, duplicate heading order, explicit/question ID | Full until lightweight source citations are available; full citation coverage remains live |
| Embedded skill/pack README, guide, examples, fixture comment or config | Full even for a prose-only edit or Markdown suffix |
| Missing/bad schema/nonancestor/unavailable baseline object | Full, with reason; no manual start marker needed |
| Changed tool/flags/environment/selector or unknown input reader | Full; old source coverage is not inherited |
| Full command failure or missing log | Nonzero/unknown remains nonzero; last success unchanged and failure log retained |
| Same-tree failed retry after older green | Fresh verification required; old success cannot hide latest failure |
| HEAD/index/untracked/source changes during gates | Nonzero stability failure; no success advancement |
| Controlled mutate/restore during gates | Known transient mutation rejects despite equal endpoints; retry needs new green |
| Second completion writer | Refused with retry instruction; does not alter first writer's record |
| No coordinated no-edit interval | Refusal; never asserts that its lock stopped all editors |
| `check-ci` versus integration/native coverage | Reports only actual obligations; fake skips cannot establish native execution |

The front-door test must also distinguish documentation checking from publication/export work:
no site builder, installations, API/auth calls, agent sessions or jail launches occur in this fixture.
Real runtime verification remains the repository's independent landing responsibility.

## Mutation evidence belongs at the caller

Required future red/restore/green controls:

- Delete the actual recipe call to the helper: the real-front-door test must fail.
- Replace conservative full routing with an empty working-diff shortcut: the multi-commit and
  intermediate-revert cases must fail.
- Delete each full-route lint/unit/pin dependency in turn: expected argv coverage must fail.
- Make a failed verifier advance success: failed-record regression must fail.
- Accept a sourceful Markdown path as ordinary: embed/guide/example case must fail.
- Delete the final stable/clean check: controlled concurrent mutation must fail.

Preserve every rejection log separately. Restore each mutation before the green retake; a restored
red is never the coverage record. This is a planned regression protocol, not execution evidence.

## Future build slices and reporting fence

1. **After promotion:** implement read-only completion and automatic baseline recording with full
   fallback only. Keep the old developer `check`, full `check-ci` and their call-site pins intact.
2. **After the control is green:** add exactly the design's five modified-regular-file prose paths,
   with proven context/anchor/referrer checks. Partial analysis still selects full.
3. **After independent acceptance:** consider package impact or exact result reuse in separate
   designs. Selective source landing waits on a future policy revision, not this first slice.

Promotion adds exact new-file map, complete red test/command, gates, scope fence, source-reviewed
context/anchor mechanisms and commit-sized build tasks. The later implementation report must
include actual changed paths, command outcomes, failed logs, selected/excluded/inherited coverage,
cleanliness before/after, mutation red/restore/green evidence and unrun native/runtime obligations.

Do not reinterpret [the suite-speed measurements](../plans/test-suite-speed.md)
as projected speedups. No measurement, build, native check or test result belongs to this sketch.
