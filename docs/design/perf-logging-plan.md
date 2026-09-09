---
title: "Plan: rename YOLO_PROFILE (perf-logging D13)"
date: 2026-09-08
status: accepted
tags: [observability, timing, run, naming]
summary: "The one unbuilt row of the perf-logging ledger: rename the launcher's YOLO_PROFILE marker to YOLO_JAIL_TIMING and rewrite the comments that argued against doing it. One string, six files, no behavior change."
vantage:
  status-chip: true
---

# Plan: rename `YOLO_PROFILE` (design D13)

**Design:** [`perf-logging.md`](perf-logging.md) · **Status:** ready ·
Written against `6e13af69`, 2026-09-08.

**Precedence:** the design wins on behavior; the tree wins on fact; this file is advice and is the
first thing to be wrong. Never twist code to match it — correct it in the commit.

**Scope:** ledger rows D1–D12 and D14 are built; [D13](perf-logging.md#decision-ledger) is the only
`⬜`. Nothing about the gates, the spans, the sinks or the report changes. This is a rename plus the
comments that justified *not* doing it.

## The new name: `YOLO_JAIL_TIMING`

D13 forbids `YOLO_TIMING` (it is [D5](perf-logging.md#decision-ledger)'s host-only opt-in) and
suggests `YOLO_TIMING_INNER`. **Take `YOLO_JAIL_TIMING` instead**, for two checkable reasons:

- `YOLO_TIMING_INNER` **recreates the exact defect being fixed**, one mechanism over: `YOLO_PROFILE`
  is a strict prefix of `YOLO_PROFILES`, so `rg YOLO_PROFILE` conflates two unrelated things —
  and `YOLO_TIMING` is a strict prefix of `YOLO_TIMING_INNER`. `YOLO_JAIL_TIMING` contains neither
  neighbour as a substring, so `rg YOLO_TIMING` and `rg YOLO_PROFILE` both stay exact.
- It joins the launcher→container argv family already on that command line — `YOLO_JAIL_DAEMONS`,
  `YOLO_JAIL_ID` — which is what this variable is: one `-e` pair the launcher emits, never a
  host-process opt-in a user exports.

Cheap and yours: the spelling, if the maintainer prefers another. It is one string and one test
constant, and no code reads it (see Traps).

## Map

| Path | Change |
| :--- | :--- |
| `internal/cli/run/assemble.go` | `:738` — the ONLY functional edit: `-e YOLO_PROFILE=1` → `-e YOLO_JAIL_TIMING=1`. Its comment (`:729`-`:737`) says the var "stays put rather than gaining a rename this step does not own" — that sentence is now false; D13 owns it. |
| `internal/cli/run/timingenv_test.go` | `:66` const value, and `:61`-`:65`'s comment, which restates the wire-contract premise D13 refutes. Rewrite, don't patch the string. |
| `internal/paths/paths.go` | `:245`, inside `TimingEnv`'s doc block: *"a second spelling crossing that boundary is a skew bug"* is the false premise. Keep D5's host-only ruling (it stands on its own), drop the skew reason, name the new spelling. |
| `internal/cli/run/runcmd.go` | `:328` — comment spelling, in `timingReporting`'s doc block. |
| `internal/cli/run/run.go` | `:1152` — comment spelling, in `emitTimingReport`'s doc block. |
| `integration/timing_test.go` | `:14` and `:95` — comment spelling only. **No assertion string changes.** |
| `docs/design/perf-logging.md` | D13's Built cell → `✅`; the new name in [§4](perf-logging.md#4-the-gates) (`:122`) and [§7.1](perf-logging.md#71-what-it-costs-to-leave-on) (`:231`); D4 (`:28`) gets a "(now `YOLO_JAIL_TIMING`)". D13's own row keeps the old spelling — it names what was renamed. |
| `docs/plans/roadmap.md` | 💬 21 (`:514`-`:534`) — it frames all six as open questions and links `#-oq-t1…`/`#-oq-t6` anchors the design no longer has. Rewrite to: D13 ruled and built, D14 was policy, T1–T4 are [§8.1](perf-logging.md#81-deferred-work-each-with-the-trigger-that-fires-it) deferred work. |

`rg 'YOLO_PROFILE\b'` is the whole census: 9 files, 6 of them code, and only two hits are code
rather than comment — the emit and the test constant.

## Reuse

- **The gate is already correct — do not touch it.** `Options.timingReporting()`
  (`internal/cli/run/runcmd.go:341`) drives both halves of the printed report: the `-e` pair at
  `assemble.go:738` and `buildFinalInternalCmd(targetCmd, o.timingReporting())` at
  `internal/cli/run/run.go:1023`. The rename touches a string literal inside that `if`.
- **No `paths` constant.** `paths.TimingEnv` / `paths.VerboseEnv` are consts because a producer and
  several consumers must share one spelling; this variable has no reader at all, so a shared const
  would only make `timingenv_test.go`'s assertion tautological. *Advice:* keep the literal in the
  test — it is the only thing in the tree that would notice a silent re-spelling.
- **Comment register to copy:** `paths.go:230`-`:238` (`AllowMissingProvidersEnv`) — the ruling,
  then why the boundary the reader expects does not apply. That is the shape the rewrites need.

## Traps

- **Nothing reads the variable.** D13 calls `command.go:103`'s `=== YOLO Jail Profile ===` block
  "the consumer"; that block is selected at *generation* time by the same Go bool, not by
  `$YOLO_PROFILE`. **Constraint:** do not wire the generated bash to read the new name. That
  manufactures the host↔jail contract D13 established does not exist, and it is a behavior change
  the design does not rule.
- **Therefore no test can go red on a wrong new name.** The symptom of getting it wrong is nothing
  at all; the unit constant and review are the entire guard.
- **Grep with the boundary.** Bare `rg YOLO_PROFILE` returns ~30 files, nearly all `YOLO_PROFILES` —
  the resolved auth-profile table, an unrelated mechanism that *is* read in the jail
  (`internal/entrypoint/providers.go:59`). That collision is the reason for the rename and the trap
  while doing it. Use `rg 'YOLO_PROFILE\b'`.
- **The block text is not the variable.** `=== YOLO Jail Profile ===` stays exactly as it is
  (`command.go:103`, asserted at `integration/timing_test.go:95`): D13 rules the env name only, and
  re-spelling a live assertion buys no ruling. It is also unrelated to the `awk` beside it, which
  matches the entrypoint's own `=== YOLO Jail Entrypoint Perf` header in `~/.yolo-perf.log`.

## Build order

1. The rename plus the comment rewrites — one commit. → `just format`, then `just check-ci`
   (`lint-ci` + `test-fast`); the pin is `TestAssembleCarriesTheTimingEnvOnlyForATimingLaunch`.
2. The two docs — one commit. → `uvx vantage-check docs/design/perf-logging.md docs/plans/roadmap.md`.
   *Advice:* after step 1, so D13's `✅` is true when it is written.
3. Confirm end to end: `go test -count=1 -timeout 0 -run 'TestTiming|TestVerbose' ./integration`.
   Real containers (`requireJail`); it is what proves the reporting path still prints both halves.
   If you cannot run containers, say so in the commit body rather than claiming it green.

## Ships with

- **No new test.** *Constraint:* the existing pins already cover both gates × the pair
  (`internal/cli/run/timingenv_test.go`) and the printed/absent block end to end
  (`integration/timing_test.go`). This is the one case [`AGENTS.md`](../../AGENTS.md) §Testing
  exempts — no behavior change, so running the suite *is* the QA step. State that in the commit body.
- **Rewrite, do not repair:** `timingenv_test.go`'s `timingEnvName` comment and
  `assemble.go`'s emit-site comment both argue for the old name. Swapping the string and leaving the
  argument standing is how a ruling gets silently reverted in a comment.
- **Docs:** the two in the Map. Nothing else names the variable — checked
  `internal/cli/config_ref.txt`, `internal/cli/runcmd.go`'s `--timing` help, and
  `docs/guides/USER_GUIDE.md:247`.
- **Lifecycle:** D13 was the last `⬜`. When it lands, both this file and the landed
  `docs/plans/perf-logging.md` are due for deletion per the plan genre — git keeps them.
- **Stop and ask:** if you conclude the `-e` pair should be **deleted** rather than renamed (it has
  no reader, and `timingenv_test.go` itself calls it "a marker nothing consumes"), that is a
  behavior change D13 does not rule. Ask; do not decide it in this commit.

## Don't

**[§8.1](perf-logging.md#81-deferred-work-each-with-the-trigger-that-fires-it)'s four deferred fixes are NOT in this slice.** Each is already decided and waits on a
measurement, not a ruling — do not pick one up while in these files:

- **T1** tty-proxy drain guard — fires when `child.exited` → `child.drain_done` shows a real gap.
- **T2** bound the `podman ps` in `stopLoopholes` — fires when a real host's shutdown spans say what
  a healthy one costs, so the bound is derived.
- **T3** read deadline in `hostservice.serveListener` — fires when a `shutdown.stop_front.*` span
  passes ~2 s.
- **T4** parallelize `stopLoopholes` — **currently points AWAY from the work**: [§7](perf-logging.md#7-what-the-first-real-runs-measured) measured the whole
  `shutdown.*` chain at **0.045 s**. Re-read that number before touching teardown.

Also: don't extend D12's recording/reporting split (no new gates), don't touch the in-jail bash
timers (D4 keeps them unchanged in v1), and don't edit `docs/plans/perf-logging.md:26` — it records
what shipped at `03b18afb` and carries its own written-against stamp.
