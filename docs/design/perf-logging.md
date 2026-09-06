---
title: "Performance logging — timing spans across the yolo lifecycle"
date: 2026-09-06
status: accepted
tags: [observability, timing, run, shutdown]
summary: "The launcher's only timing number was a single host-side Total. This design grows --timing into a span system covering launch, the child window, and both shutdown arms; adds --verbose as the future diagnostics gate; and attributes the invisible stretch inside podman's own --rm cleanup — because the question that motivated it ('who is holding my shell prompt for 30 seconds after the agent exits?') had no answerable spelling in the code."
---

# Performance logging — timing spans across the yolo lifecycle

**Status:** DECIDED 2026-09-06 and built in the same change (two maintainer rulings up front: the flag
surface is **both** `--timing` grown and a new `--verbose`; the persistent log lives in
**`<workspace>/.yolo/`**). Six Open Questions remain — all of them *fix candidates this feature is
supposed to name*, not gaps in this design; see [§7](#7-open-questions).

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

`--timing` (run flag) is grown, not replaced: it now means "span the whole launch, the child
window, and the shutdown chain", reported to stderr and appended to the file. The in-jail half it
already wired — the `YOLO_PROFILE=1` argv pair and the bash timers — is untouched (D4).

`--verbose` / `-v` is a new **global** flag, stripped at the front door beside `--user-layer` and
published through the process environment the same way, so it reaches every subcommand without
four flag parsers growing a fifth forgetter. For v1 it enables the same span surface; it exists so
non-timing diagnostics have a gate to grow into that does not further load the word "timing".

Two host-process env vars, `YOLO_TIMING` and `YOLO_VERBOSE`, enable the same surface without flag
surgery (`YOLO_TIMING=1 yolo -- bash`). Both are deliberately **not** forwarded into the jail (D5).

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

## 7. Open Questions

### 💬 [OQ-T1](#-oq-t1--should-the-tty-proxys-final-drain-have-a-poll-guard) — should the tty proxy's final drain have a poll guard?

The post-exit master drain blocks on `unix.Read` with no timeout; any process still holding the pty
slave (a lingering conmon fd is the classic) pins it indefinitely. The span pair
`child.exited`/`child.drain_done` will name it the first time it happens. *Leaning:* guard the
read with poll-and-timeout once a diagnosis confirms it fires in practice, not before — an
unbounded drain that never blocks in practice is not worth exit-semantics churn on spec.

### 💬 [OQ-T2](#-oq-t2--should-the-podman-ps-inside-stoploopholes-be-bounded) — should the `podman ps` inside `stopLoopholes` be bounded?

It runs with timeout 0 today and sits mid-shutdown-chain, so a hung runtime holds the prompt
forever. *Leaning:* yes, a few seconds, once the spans show how long a healthy one takes.

### 💬 [OQ-T3](#-oq-t3--does-hostserviceservelisteners-unbounded-inflightwait-need-an-idle-connection-deadline) — does `hostservice.serveListener`'s unbounded `inFlight.Wait()` need an idle-connection deadline?

An accepted-but-idle connection pins a daemon until the 5s SIGKILL.
*Leaning:* read deadline on the request header; decide after the first `shutdown.stop_front.*`
span over ~2s is diagnosed.

### 💬 [OQ-T4](#-oq-t4--parallelize-stoploopholes) — parallelize `stopLoopholes`?

Stops are serial today; worst case is the sum, not the max. *Leaning:* only after measurement —
the serial order may be load-bearing for shared fronts.

### 💬 [OQ-T5](#-oq-t5--rename-yolo_profile-to-match---timing) — rename `YOLO_PROFILE` to match `--timing`?

The flag was renamed, the env var was not. It is a host↔jail wire contract, so the rename is
coordinated across the deploy-skew boundary or not at all.

### 💬 [OQ-T6](#-oq-t6--what-does---verbose-grow-into) — what does `--verbose` grow into?

v1 aliases the timing surface. The first non-timing diagnostic that wants a gate decides its
vocabulary; until then it should not sprout meaning by accident.
