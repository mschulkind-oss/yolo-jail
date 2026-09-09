---
title: "Performance logging — timing spans across the yolo lifecycle"
date: 2026-09-06
status: accepted
tags: [observability, timing, run, shutdown]
summary: "The launcher's only timing number was a single host-side Total. This design grows --timing into a span system covering launch, the child window, and both shutdown arms; adds --verbose as the future diagnostics gate; and attributes the invisible stretch inside podman's own --rm cleanup — because the question that motivated it ('who is holding my shell prompt for 30 seconds after the agent exits?') had no answerable spelling in the code."
---

# Performance logging — timing spans across the yolo lifecycle

**Status:** DECIDED 2026-09-06 and built in the same change; **amended 2026-09-08 by
[D12](#decision-ledger)**, which split RECORDING from REPORTING so an always-on setting stops
printing a table at every jail quit (two maintainer rulings up front: the flag
surface is **both** `--timing` grown and a new `--verbose`; the persistent log lives in
**`<workspace>/.yolo/`**). No Open Questions remain: one was answered
([D13](#decision-ledger)), one was a stated policy all along ([D14](#decision-ledger)), and the
other four were never questions for a person — they are decided fix candidates *this feature
exists to name*, each waiting on a span rather than a ruling
([§8.1](#81-deferred-work-each-with-the-trigger-that-fires-it)). **Built and measured on a real launch** the same day — what that changed is [§7](#7-what-the-first-real-runs-measured).

## Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| D1 | Grow the existing `--timing` flag rather than coin a new performance flag — it already exists, is already documented, and already crosses into the jail. AND add a global `--verbose` / `-v` now, initially enabling the same spans, as the gate non-timing diagnostics can grow into. | 2026-09-06 | [maintainer ruling, this doc §4](#4-the-gates) | ✅ |
| D2 | The persistent log is `<workspace>/.yolo/host-perf.log` — beside `boot.log`, per-workspace, so concurrent jails never interleave — with trim-to-last-50-runs retention (the entrypoint perf log's idiom). | 2026-09-06 | [maintainer ruling, §5](#5-where-the-log-lives) | ✅ |
| D3 | Events reach the sink **as they happen**, never buffered for an end-of-run dump. The tty proxy's signal arm exits via `os.Exit(128+n)` where defers do not run, and a hang is only answerable if the file already shows the last span that started and never ended. | 2026-09-06 | [§3](#3-the-collector--internalperf) | ✅ |
| D4 | The in-jail half (the `YOLO_PROFILE=1` argv pair and the bash timers in `buildFinalInternalCmd`) is **unchanged in v1**. Host and image redeploy on different cadences; a contract change across that boundary is the skew class AGENTS.md records. | 2026-09-06 | [§4](#4-the-gates) | ✅ (by omission) |
| D5 | `YOLO_TIMING` / `YOLO_VERBOSE` are **host-process** opt-ins, read by the launcher and deliberately NOT forwarded into the jail. A second host↔jail spelling is a skew bug, not a convenience. | 2026-09-06 | [§4](#4-the-gates) | ✅ |
| D6 | The signal arm's report prints **inside** `onTerminate`, before the `os.Exit` that follows it; the normal-arm report prints before `runRun`'s deferred title restore, so it never interleaves with the returning shell prompt. | 2026-09-06 | [§6](#6-window-a-attribution-and-where-reports-print) | ✅ |
| D7 | A span slower than `perf.SlowSpanThreshold` (1s) names itself on stderr, dim, the moment it ends — the culprit announced live, which is the whole point when the terminal is sitting there hung. | 2026-09-06 | [§3](#3-the-collector--internalperf) | ✅ |
| D8 | The collector's clock is `time.Now`, never the `Options.Now` seam — that seam exists so tests can freeze time, and a span system built on a frozen clock reports `0.000s` everywhere under test. | 2026-09-06 | [§3](#3-the-collector--internalperf) | ✅ |
| D9 | Window A attribution runs `podman events` through the `o.Exec` seam, gated on the podman runtime, always with `--until` (the journald backend streams otherwise) and a 3s timeout; every failure is silence. | 2026-09-06 | [§6](#6-window-a-attribution-and-where-reports-print) | ✅ |
| D10 | `launch.auto_capture` is spanned. Not in the original design — the first real `--timing` run put 109 of its 125 seconds between two spans, in `autoCaptureInstallerPrograms`. A span table's holes are only visible on a real launch. | 2026-09-06 | [§7](#7-what-the-first-real-runs-measured) | ✅ |
| D11 | The report is emitted once per `Run`, guarded by a `*sync.Once` on Options. On the signal path BOTH teardown arms legitimately run — the terminate arm's `stopJail` makes the child exit, unblocking the normal-exit arm mid-teardown — so the guard is about not printing twice, not about a fault. Pointer, not embedded: `Options` is passed by value and vet's copylocks refuses a copied lock. | 2026-09-06 | [§7](#7-what-the-first-real-runs-measured) | ✅ |
| D12 | **RECORDING and REPORTING are separate gates.** Every opt-in records to `<workspace>/.yolo/host-perf.log`; only an EXPLICIT per-invocation flag (`--timing`, `--verbose`/`-v`) prints the span table, the Window A line and the in-container block. A PERSISTENT setting — `perf_logging: true`, or `YOLO_TIMING`/`YOLO_VERBOSE` exported in a shell profile — records in silence, plus one dim line naming the file. The principle for classifying the next opt-in: *an explicit per-invocation flag prints; a persistent setting records silently.* | 2026-09-08 | [§4](#4-the-gates), [§7.1](#71-what-it-costs-to-leave-on) | ✅ |
| D13 | **`YOLO_PROFILE` is renamed — RULED 2026-09-08 (*"yes"*) — and the reason the question hesitated turns out to be false.** It called the var *"a host↔jail wire contract"* that must be *"coordinated across the deploy-skew boundary or not at all"*. **It is not a wire contract: both halves are host-side.** The launcher emits it (`internal/cli/run/assemble.go:738`, `-e YOLO_PROFILE=1`) and the consumer is bash the same launcher generates (`internal/cli/run/command.go:103`, the `=== YOLO Jail Profile ===` block) — one binary, one commit, nothing in the image or the entrypoint reads it. So the rename is local and skew-free. **A second, better reason to do it than matching `--timing`:** `YOLO_PROFILE` sits one character from `YOLO_PROFILES`, the resolved auth-profile table, which IS read in the jail (`internal/entrypoint/providers.go:59`) and is a completely unrelated mechanism. **Constraint on the new name: it must NOT be `YOLO_TIMING`** — [D5](#decision-ledger) makes that a host-process opt-in deliberately *not* forwarded into the jail, and reusing it here would make forwarding look intended. `YOLO_TIMING_INNER` or similar; the requirement is that it names the in-container block and cannot be mistaken for either neighbour. `integration/timing_test.go` asserts the block's text and pins the rename. | 2026-09-08 | [§4](#4-the-gates), [§8.1](#81-deferred-work-each-with-the-trigger-that-fires-it) | ⬜ |
| D14 | **`--verbose` gets no vocabulary until something needs one** — the entry that used to be the sixth OQ-T, recorded as the policy it always was rather than a pending ruling. v1 aliases the timing surface ([D1](#decision-ledger)); the **first non-timing diagnostic that wants a gate** decides what `--verbose` means, and until then it must not sprout meaning by accident. Nothing to answer today, and a person cannot usefully answer it earlier than the first real case. | 2026-09-08 | [§4](#4-the-gates) | ✅ (by omission) |

## 1. The symptom, and why it had no answer

Quitting an agent in a jail looks like this on a slow day: the agent's TUI exits, the backing
terminal shows, and then the host shell prompt takes **30+ seconds** to return. Nothing in the
output names the delay, because nothing in the code can:

- the host side's entire timing surface was one number — `Total (host-side)`, printed under
  `--timing` from a stopwatch started mid-pipeline;
- `yolo`'s own logs (`~/.local/share/yolo-jail/logs/`) carry only loophole-daemon output; there is
  no per-launch host log at all;
- and the largest candidate for the delay lives **inside the podman child**, where yolo has no
  code executing and therefore no timestamps.

The question "who is doing it" needs a *timeline with names in it*, taken on the host where every
candidate either runs or is waited upon. That is this feature.

## 2. The shutdown path (the map the spans pin)

What happens between "agent process exited" and "shell prompt returns", in order:

1. The container's PID 1 dies. On a normal exit the in-jail side contributes ~nothing — the
   entrypoint `exec`s bash, the kernel reaps the daemons; there are no traps, no atexit.
2. **Window A** *(coined here)* — the stretch entirely inside the `podman run --rm` child: conmon
   notices PID 1 died and writes the exit file; podman then removes the container, tearing down the
   network (netavark/pasta) and unmounting the overlay root plus **~20–30 bind mounts**. Yolo has
   zero instrumentation and zero timeout here; it learns the window closed only when the child
   exits.
3. The tty proxy returns: its final master drain finishes, the host terminal's cooked termios is
   restored.
4. Host teardown, in order: socat port-forwarding cleanup (SIGTERM → 2s wait → SIGKILL, per port,
   serial) → `stopLoopholes` (per daemon: front close ≤2s, then process-group SIGTERM → ≤5s wait →
   SIGKILL — serial; then an **unbounded** `podman ps`) → config capture → owner-PID clear → OOM
   check (macOS) → timing report → deferred terminal-title restore → embedded-pack temp release.

The ranked hypotheses for a 30-second delay, each now visible or attributable:

| # | Candidate | How the spans answer it |
| :--- | :--- | :--- |
| H1 | podman's `--rm` cleanup (Window A) | the `child.*` marks bound it, and [§6](#6-window-a-attribution-and-where-reports-print)'s events query names the stages inside it |
| H2 | the tty proxy's final drain — a blocking master read with no poll guard | `child.exited` → `child.drain_done` gap |
| H3 | serial loophole teardown, ≤7s per lingering daemon | `shutdown.stop_front.<name>` sub-spans |
| H4 | the unbounded `podman ps` inside `stopLoopholes` | the `container_check` mark, and the dangling start line if it hangs |
| H5 | the signal arm's `podman stop -t 5` chain (SIGHUP/SIGTERM only) | `terminate.*` spans |
| H6 | terminal-title restore subprocesses with no timeout | `process.title_restore` |

## 3. The collector — `internal/perf`

A **span** is a named interval with a start and an end (the field's usual meaning, as in
OpenTelemetry); a **mark** is a point event ("child exited"). Both are recorded by
`internal/perf.Log`:

- **Nil-safe and free when off.** Every method works on a nil `*Log`; `Span` returns nil and
  `Span.End` is a no-op, so call sites are unconditional — no `if timing {}` wrappers around the
  spans themselves. The disabled path is one pointer check.
- **Thread-safe.** Spans end on the tty proxy's signal goroutine while the main goroutine reports;
  one mutex covers the record slice, and sinks lock themselves.
- **Best-effort, always.** No method errors upward — a timing logger that can fail a launch is a
  worse bug than the blindness it fixes. The file sink warns once, then stays permanently quiet
  (the `crossaudit` discipline).
- **Sinks see events as they happen** (D3). The sink line carries a millisecond UTC timestamp, so
  the host file, the jail's perf log, and `podman events` output can be correlated by eye.
- **The report renders in the entrypoint's register** — elapsed, `+delta`, label, in the exact
  widths of `boot.go`'s perf log dump — so the two halves of one launch read as siblings. Starts
  are never rendered (they exist for the file, where a dangling one names a hang); marks print with
  a `-` duration.

A slow span names itself as it ends (D7): one dim stderr line per span past
`perf.SlowSpanThreshold` — the same register as the flock-wait notices.

## 4. The gates

There are **two** gates, not one (D12), and every opt-in below answers both:

| Gate | What it is | Records to the file | Prints the report |
| :--- | :--- | :---: | :---: |
| `--timing` | run flag, typed this launch | ✅ | ✅ |
| `--verbose` / `-v` | global flag, typed this launch | ✅ | ✅ |
| `perf_logging: true` | user config, always on | ✅ | ❌ |
| `YOLO_TIMING=1` / `YOLO_VERBOSE=1` | environment, usually a shell profile | ✅ | ❌ |

`--timing` (run flag) is grown, not replaced: it means "span the whole launch, the child window,
and the shutdown chain", reported to stderr and appended to the file. The in-jail half it already
wired — the `YOLO_PROFILE=1` argv pair and the bash timers — is untouched (D4), and rides the
REPORTING gate, because it is print-only: the jail appends to its own `~/.yolo-perf.log` either
way.

`--verbose` / `-v` is a **global** flag, stripped at the front door beside `--user-layer` and
published through the process environment the same way, so it reaches every subcommand without
four flag parsers growing a fifth forgetter. For v1 it enables the same span surface; it exists so
non-timing diagnostics have a gate to grow into that does not further load the word "timing".

Two host-process env vars, `YOLO_TIMING` and `YOLO_VERBOSE`, enable RECORDING without flag surgery
(`YOLO_TIMING=1 yolo -- bash`). Both are deliberately **not** forwarded into the jail (D5).

**The trap in that table**: the `--verbose` flag *publishes itself* as `YOLO_VERBOSE`, so the two
rows that must behave differently are the same variable to every `Getenv` reader downstream. The
distinction is carried in-process instead — `internal/cli`'s `verboseFlagTyped`, handed to the
pipeline as `run.Options.Verbose`. A second env var could not do it: it would be inherited by the
next `yolo` too, which is the very thing being distinguished. In the pipeline the two gates are
`timingRecording()` (any opt-in) and `timingReporting()` (the two explicit flags), and the
persistent opt-in reaches the first ONLY — `fillDefaults` reads `perf_logging` into a field of its
own rather than folding it into `Options.Timing`, which is what it did until D12 and what erased
the distinction.

`yolo stop` is the same rule in its small form: recording rides the env opt-ins (it has no
`--timing` of its own), printing needs a `--verbose` typed on that invocation.

## 5. Where the log lives

`<workspace>/.yolo/host-perf.log` (D2) — beside `boot.log`, per-workspace:

- the jail's own half already lands next to it (the `~/.yolo-perf.log` bind backs onto
  `<workspace>/.yolo/home/yolo-perf.log`), so one directory holds both halves of one launch;
- per-workspace files cannot interleave across concurrent jails;
- retention is the entrypoint's trim-to-last-50-runs idiom, applied at open, never at exit.

> [!WARNING]
> **This directory is inside the live workspace bind**, which means a jail *can* write the host's
> diagnostic record — the honest cost of the maintainer's ruling, accepted on the precedent that
> `boot.log` already lives there. The rejected alternative — the host-global
> `~/.local/share/yolo-jail/logs/` beside `crossings.log`, outside any jail's reach — is the shape
> to reconsider if a jail ever mutates a host perf log.

## 6. Window A attribution, and where reports print

Window A cannot be spanned — no yolo code runs inside it — but it can be **attributed**: after the
child exits, the launcher asks `podman events` (bounded, best-effort, D9) for the container's
`die`/`cleanup` timestamps and renders the `die → podman exit` gap into the report. A wedged or
empty events log renders nothing; the attribution step's own duration is itself spanned so it can
never become a new mystery.

Report placement (D6): the normal-arm report prints in `Run`, before the deferred title restore;
the signal arm's prints inside `onTerminate` — the last statement that will ever run, because the
tty proxy `os.Exit`s the moment it returns.

Both arms go through the same `emitTimingReport`, which is also where D12's two gates part company:
a launch that recorded without being asked to report prints one dim line naming the file and
returns, so the attribution query above never runs on that path. The `*sync.Once` (D11) covers both
outcomes, so the quiet line cannot double either.

## 7. What the first real runs measured

The spans exist to answer a question, so the first answers belong here — measured in a nested jail
(`/tmp/yolo-nested`, `YOLO_REPO_ROOT=/workspace`, the binary by path), 2026-09-06.

| Span | First run | After a store prune |
| :--- | ---: | ---: |
| `launch.auto_capture` | 109.0s | 0.002s |
| `launch.auto_load_image` | 7.3s | 85.9s |
| `launch.run_with_proxy` (whole child window) | 7.8s | 4.6s |
| the entire `shutdown.*` chain | 0.045s | 0.045s |

Two things follow, and both are about where NOT to look next.

**On this host, in this mode, teardown is not the delay.** The whole shutdown chain measures tens of
milliseconds; the seconds live in the image build and the installer capture, which are launch costs.
That does not refute the 30-second symptom — see the warning below — it says the instrument now
distinguishes the two, which nothing could before.

> [!WARNING]
> **A nested jail cannot confirm or refute H1.** Podman-in-podman forces `--net=host` and the nested
> store, image and mount set are not the maintainer's; Window A is exactly the stretch whose cost is
> a property of the real host's storage driver and network stack (the same structural blindness
> [AGENTS.md](../../AGENTS.md) records for reachability). The ranked table in [§2](#2-the-shutdown-path-the-map-the-spans-pin)
> is still hypotheses. The measurement that settles it is one `--timing` quit on the real host, whose
> report names the arm and whose `host-perf.log` survives it.

### 7.1 What it costs to leave on

Measured on one complete nested launch, 2026-09-06 (podman 5.8.4):

| Cost | Measured | Bound |
| :--- | ---: | :--- |
| Log file, one launch | **2,194 bytes / 35 lines** | — |
| Log file, per workspace | — | **~110 KB**, hard: trim-to-50-runs at open |
| Added wall-clock, post-exit | **39 ms** (whole chain incl. attribution) | — |
| `shutdown.window_a_podman_events` | **0.019s** | 3s exec timeout |

The line count is stable because it is structural — one `start`/`end` pair per span plus the marks,
about 35 events for a fresh launch, fewer for an attach. It does not grow with how *long* anything
took, so a pathological 30-second shutdown writes the same 2 KB as a fast one.

> [!WARNING]
> **This was 3.002s per launch until `--until` was fixed.** `podman events --until <future>` waits
> for that wall-clock moment instead of returning what it has, and the first version passed
> `podmanExited+5s`. The instrument had the exact bug class it was built to find, and what exposed
> it was its own report — a suspiciously round `3.002s` on every single run. Pinned by
> `TestWindowAUntilIsNeverInTheFuture`.

**The remaining cost of always-on was noise, not resources — and that is now fixed** (D12,
2026-09-08). It was real: with `perf_logging: true` every jail quit dumped a ~25-line table to
stderr and, through `YOLO_PROFILE=1`, an in-container `=== YOLO Jail Profile ===` block on top of
it, scrolling away whatever the user had been reading. The split this section predicted — *always
append, print only when asked* — is what shipped, and it was small:

- the persistent opt-ins (`perf_logging: true`, an exported `YOLO_TIMING`/`YOLO_VERBOSE`) record
  everything and print **one dim line** naming the file;
- an explicit `--timing` or `--verbose` prints the full report, both halves, exactly as before;
- what the quiet path KEEPS is the part that is not noise: the file, written as it happens (D3),
  and the live slow-span notices (D7) — one line that names a culprit while the user is still
  waiting is the opposite of a table nobody asked for. Window A attribution is part of the report,
  so a quiet launch does not even run its `podman events` query.

So the resource row above is now the whole cost of leaving it on.

**What the signal path proved instead.** SIGTERM to the launcher, under a real pty: `terminate.*`
spans reached the file *after* the arm's `os.Exit(128+n)`, and the report printed at rc 143. That is
[D3](#decision-ledger) working — an end-of-run dump would have lost the whole record on the one path
most worth seeing.

## 8. Open Questions

**None.** This section held six entries and **only one of them was ever a question for the
maintainer** — an audit prompted by the maintainer noticing exactly that: *"OQ section isn't
complete and has questions that aren't for me?"* Both halves were right. The entries carried no
`**Answer:**` scaffolding and no `vantage: oq` markers, so nothing could track them; and four of
the six were engineering items already decided in all but name, each waiting on a measurement
rather than on a ruling. They are [§8.1](#81-deferred-work-each-with-the-trigger-that-fires-it) now.
[OQ-T5](#81-deferred-work-each-with-the-trigger-that-fires-it) was answered
(ledger [D13](#decision-ledger)); [OQ-T6](#81-deferred-work-each-with-the-trigger-that-fires-it) was
never a pending decision but a stated policy (ledger [D14](#decision-ledger)).

> [!NOTE]
> **The reusable test, since this doc got it wrong six times in a row.** An entry belongs in Open
> Questions only if a human's answer would change what gets built. *"Do X once the span shows Y"*
> is not that — it is a decision with a trigger, and parking it here makes a work queue look like
> an unanswered design and buries the one entry that did need a person.

### 8.1 Deferred work, each with the trigger that fires it

Not questions. Each is decided; the trigger is the evidence that says *now*, and every trigger is
a span this design already emits — which is the point of having built the instrument first.

| Was | Decision | Fires when |
| :--- | :--- | :--- |
| **T1** — the tty proxy's post-exit master drain blocks on `unix.Read` with no timeout, so any process still holding the pty slave (a lingering conmon fd is the classic) pins it | Guard the read with poll-and-timeout. **Not on spec** — an unbounded drain that never blocks in practice is not worth exit-semantics churn | `child.exited` → `child.drain_done` shows a real gap. The span pair names it the first time it happens |
| **T2** — the `podman ps` inside `stopLoopholes` runs with timeout 0 mid-shutdown-chain, so a hung runtime holds the prompt forever | Bound it at a few seconds | the shutdown spans show what a healthy one costs, so the bound is derived rather than guessed |
| **T3** — `hostservice.serveListener`'s `inFlight.Wait()` is unbounded, so an accepted-but-idle connection pins a daemon until the 5 s SIGKILL | Read deadline on the request header | a `shutdown.stop_front.*` span over ~2 s is diagnosed |
| **T4** — `stopLoopholes` stops serially, so the worst case is the sum rather than the max | Parallelize **only** after measurement: the serial order may be load-bearing for shared fronts, and that has to be checked before it is broken | the shutdown chain is measured on a real host and the sum is the term that matters. [§7](#7-what-the-first-real-runs-measured) measured the whole chain at **0.045 s** in a nested jail, which is the opposite of a case for this work — and cannot speak for the real host |

> [!IMPORTANT]
> **T4's trigger has already half-fired, in the direction of NOT doing it.** The first real runs put
> the entire `shutdown.*` chain at 45 milliseconds. That does not settle it — a nested jail cannot
> speak for the maintainer's storage and network stack ([§7](#7-what-the-first-real-runs-measured)'s
> warning) — but it does mean parallelising teardown is currently a solution to a 45 ms problem, and
> whoever picks it up should re-read that number first.
