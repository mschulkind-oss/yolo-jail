---
status: current
verified: 2026-09-09
verified_commit: 41dde711
covers:
  - internal/ttyproxy/
  - internal/cli/run/proxy_linux.go
  - internal/cli/run/proxy_other.go
  - internal/cli/run/runcmd.go
tags: [tty, pty, signals, ctrl-z, proxy, teardown]
---

# Ctrl-Z and the TTY proxy — why a launch runs under a pty of its own

**Status:** CURRENT as of 2026-09-09, verified against `41dde711`.

Every `yolo -- <cmd>` launch on Linux runs under an **in-process TTY proxy**: a pty pair the CLI
owns, with the container runtime's stdio on the slave side and the host terminal on the master
side. The proxy exists because pressing Ctrl-Z while a TUI application ran inside the jail could
**wedge the jail with no in-band recovery** — and because there is no shell with job control
between the container's PID 1 and the application to catch it.

It has since become the one place all signal teardown lives, so a window close tears the jail down
cleanly rather than orphaning it.

| Component | Lives in |
| :--- | :--- |
| The proxy: pty, pump loop, signal handling, teardown | `internal/ttyproxy` (`RunWithProxy`, `RunWithProxyHooked`, `proxyLoop`, `selfSuspend`) |
| The plain fallback for non-TTY stdin | `internal/ttyproxy` (`runPlain`) |
| Stage reporting, without the package learning what a span is | `internal/ttyproxy` (`StageHook`, the `Stage*` constants) |
| The Linux call seam, and its perf hook | `internal/cli/run/proxy_linux.go` |
| The non-Linux fallback — a plain foreground exec, no pty | `internal/cli/run/proxy_other.go` |
| The seam the `macos-user` path is given, so it never imports a Linux-only package | `internal/cli/run` (`RunWithProxy` in `runcmd.go`) |

**Reads with:** [`perf-logging.md`](perf-logging.md) (the `child.*` marks this emits, the
proxy-drain gap they bound, and `T1` — the deferred fix for the unguarded drain, with the
measurement that would trigger it).

---

## The wedge this exists to prevent

A TUI application takes over the terminal in raw mode, reads keystrokes itself, and handles Ctrl-Z
in its own code. The handler that causes the problem sends `SIGTSTP` to **process group 0** — every
process in its own group.

Inside the container that group is PID 1 plus the application plus its children, because
**`bash -c '<cmd>'` runs the command in its own process group, does not enable job control, and
just waits.** There is no job-control shell in between. So:

- the application receives `SIGTSTP` and stops;
- the container's PID 1 receives it and stops;
- the runtime's attach is only relaying bytes, and nothing alive is reading them;
- **the host shell's own process is not stopped** — the signal went to processes in the container's
  PID namespace — so the host shell thinks the foreground job is still running and never prompts.

From the user's seat: keystrokes go nowhere, `^C` is just another byte because the terminal is in
raw mode, and the only way out is a `kill -CONT` from a second terminal, or the runtime's detach
keys — which lose the session that was inside. It is easy to hit by accident and has no in-band
recovery. That is the bug.

## Frozen behavior

These are the proxy's invariants. The package header states the same list, deliberately, because
each one is a place a plausible "cleanup" reintroduces the wedge.

- **Non-TTY stdin ⇒ a transparent plain spawn, no pty.** Pipes, test harnesses and automation keep
  working unchanged.
- **`^Z` suspends the PROXY**, via a **targeted `SIGTSTP` to self** — never a pgroup-wide signal,
  which would stop the container runtime and so be a jail-visible change. The byte **never reaches
  the child**, and bytes after `^Z` in the same read are queued and flushed on resume.
- **No `Setsid`.** `setsid()` would put the runtime in its own session with no controlling
  terminal, breaking its pty allocation. The runtime inherits the proxy's session and simply has
  the pty slave as its stdio; the host shell's job control still tracks the proxy, because the
  shell put the proxy in its own process group before handing it the foreground.
- **Never install a handler for `SIGTSTP`.** The **default disposition is required** to actually
  stop the process. A handler is the one change that makes the suspend silently do nothing.
- **`SIGCONT` re-raws the host terminal**, and resyncs the size — a resize while stopped may leave
  no signal to see.
- **A resize is TWO acts, and the write alone is not enough.** `SIGWINCH` sets the new size on the
  pty with `TIOCSWINSZ` **and then sends a targeted `SIGWINCH` to the runtime child's pid**.
  Because the child is deliberately not given its own session (see the `setsid` prohibition
  below), the runtime sits in the proxy's process group on the host tty and receives the *same*
  kernel `SIGWINCH` at the same moment, while reading its size from the proxy pty. If its handler
  wins that race it reads a size not yet written and pushes the stale value into the container —
  and nothing corrects it, because `TIOCSWINSZ` on the proxy pty raises no signal at all: that pty
  has no foreground process group, so `tcgetpgrp` returns `ENOTTY`. The re-signal goes **after**
  the write, so the runtime's re-read cannot be early, and at the **pid** — never the process
  group, which would be jail-visible.

> [!WARNING]
> **Do not reintroduce `setsid` to fix window resizes.** Giving the runtime its own session would
> make the proxy pty a real controlling terminal, so `TIOCSWINSZ` would signal it naturally and
> this race would not exist — but that is exactly the change the job-control section forbids, and
> it is why the fix is a targeted re-signal rather than the textbook one.
- **`SIGHUP`/`SIGTERM` restore cooked termios, run the terminate callback, and exit `128+n`.** This
  is the window-close path, and it is why teardown is the proxy's job.
- **stdin EOF stops reading stdin and keeps pumping the master until the child exits.** Decided
  semantics, not an accident: the output after an EOF is still wanted.

> [!WARNING]
> **`SIGTSTP` delivered to one thread stops the whole process, and that is relied upon.** The
> post-spawn callback runs on its own goroutine so the pump loop is not blocked while it does its
> post-launch housekeeping. When a suspend lands, the kernel stops every thread in the process,
> which is what removes any race between "we suspended" and "the callback is mid-write." Sending to
> the process — not the process group — is what keeps that true without touching the runtime.

## What the proxy does

```
host TTY ──> proxy (raw mode) ──> master pty ──> runtime ──> container pty ──> the app
                  │                                                              │
                  │   intercepts 0x1A  ────────────────► self-suspend             │
                  │   raises SIGTSTP   ◄─── host shell ───  fg                    │
host TTY <── proxy ──── master pty <── runtime <── container pty <── the app      │
```

1. Open a pty pair, and give the child the **slave** as all three of its stdio streams — so the
   runtime believes it is on a terminal, with no host-terminal raw-mode dance of its own.
2. Put the **host** terminal in raw mode, so `0x1A` arrives as a byte rather than being translated
   to a signal by the kernel's terminal driver.
3. Pump: host stdin → master, master → host stdout. On seeing the suspend byte, write everything
   *before* it, queue everything *after* it, then self-suspend.
4. Self-suspend restores cooked termios — so the shell prompt works — and sends the targeted
   `SIGTSTP`. The host shell sees a stopped child, prints its stopped-job line, and prompts. `fg`
   resumes, `SIGCONT` retakes raw mode, and the queued bytes flush.
5. On child exit, **drain the master once more** before returning, then restore termios.

**The final drain is load-bearing.** Without it the last lines of output — an application's exit
summary — are truncated, because bytes can still be sitting in the pty buffer when the child
exits. Its cost is a named gap: see [`perf-logging.md`](perf-logging.md), whose `T1` records that
the drain read has no poll guard and states the measurement that would justify adding one. **Do not
absorb that decision here** — the trigger and the reasoning live there.

## Why the cheaper fixes do not work

Each of these is the obvious first move, and each was tried.

> [!WARNING]
> **Disabling the terminal's suspend character does nothing.** The terminal driver is not what
> generates the suspend: the application reads the byte in raw mode and signals itself. Measured —
> a container pty already has the suspend character off by default, so there is nothing to disable.

> [!WARNING]
> **Ignoring `SIGTSTP` before exec does not help either.** `SIG_IGN` survives `exec`, but a
> pgroup-wide signal is checked against *each* process's own disposition — and the application's
> runtime installs its own handler when its TUI library takes raw mode. The application still
> stops, PID 1 is then merely stuck waiting on a stopped child, and the wedge is the same for
> slightly different reasons.

> [!WARNING]
> **An interactive shell around the command does not catch it.** `bash -c '<single command>'` does
> not establish a foreground-process-group transition for its child even with `-i`, and often
> `exec`s itself away entirely for a simple command body. Measured on a real terminal in both
> shapes: `^Z` either prints a literal `^Z` or kills the child, and no stopped job ever lands at a
> prompt. Bash's job-control machinery needs to be reading commands from stdin, not running a
> one-shot script. The two-stage variant — command, then an interactive shell — does work, and was
> refused: it makes a normal exit a two-step, and pays interactive-shell startup on every launch.

The application's own suspend handler has no opt-out: it is hard-coded with no setting and no
environment hook, and the only escape it offers is `fg`, which requires the job-control parent that
does not exist inside the jail.

## Per-platform

| Platform | What runs |
| :--- | :--- |
| Linux, TTY stdin | the full proxy |
| Linux, non-TTY stdin | the plain spawn — no pty, no signal proxy, and the stage hook reports only spawn and exit |
| Non-Linux | a plain foreground exec. The terminate callback is **not wired**, because there is no signal proxy to run it from |
| `macos-user` | takes its own native path, reached through a `func([]string) int` seam the front door injects — so that path never imports the Linux-only package, which would break the darwin build |

Apple Container has never been exercised under a proxy: on darwin it takes the plain fallback. Its
pty semantics differ, so treat a proxy behavior there as unverified rather than as inherited.

## What this does not do

- **It does not detach and leave the jail running.** A suspend stops the proxy, which means the
  runtime is also waiting — its stdio *is* the proxy's pty. The application inside is not itself
  suspended, since the byte was never forwarded, so it keeps its state and blocks on its next
  write. True background-and-reattach would need the proxy to close the child's stdio and exit
  cleanly on a *different* keystroke, paired with an in-band reattach command.
- **It does not forward a literal suspend byte to the inside.** An application that wants `0x1A`
  for its own purposes cannot get it. A modifier-then-`^Z` passthrough would be the shape.
- **It does not notice the application being stopped from inside the jail.** If something else in
  there sends a stop signal, output ceases while input still forwards, and the user sees a frozen
  application. Different bug, different fix.
- **It does not own timing semantics.** It reports its own transitions to whoever asked, and never
  learns what a timing span is. [`perf-logging.md`](perf-logging.md) owns that.

## Current values

Verified at `41dde711`. The prose above explains what each of these is for; this table is the only
place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Suspend byte | `0x1A` | `ttyproxy` (`suspByte`) |
| Pump read size | 64 KiB | `ttyproxy` (`readChunk`) |
| Exit code on a killed proxy | `128 + signal` | `ttyproxy` |
| Reported stages | `spawned`, `exited`, `drain_done`, `termios_restored` | `ttyproxy` (`Stage*`), consumed as `child.*` marks — see [`perf-logging.md`](perf-logging.md) |
| Resize ioctl | `TIOCSWINSZ`, from a `TIOCGWINSZ` on the host terminal | `ttyproxy` (`setWinsize`, `getWinsize`) |
