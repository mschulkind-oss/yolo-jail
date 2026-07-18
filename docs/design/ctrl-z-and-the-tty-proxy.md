# Ctrl-Z, claude-in-jail, and the host-side TTY proxy

This is a session-spanning brain dump on why pressing Ctrl-Z while Claude
Code (or any TTY app) was running under `yolo -- <cmd>` could wedge the
jail with no recourse, what we tried, what didn't work, and the proxy we
landed on.  The fix shipped as `src/cli/tty_proxy.py` (commit `f10feda`),
but the analysis here is what'll matter if we ever want to revisit the
design (e.g., to add a way for ^Z to drop you back to the host shell
*and* keep claude alive in the background — currently we suspend the
proxy, so claude waits where it is).

## The wedge, end-to-end

User runs `yolo -- claude --continue` on the host.  The sequence inside
the container ends up looking roughly:

```
PID 1  /run/podman-init -- yolo-entrypoint <inner shell command>
PID 2    bash -c "...; eval $(mise env -s bash); exec claude --continue"
PID N      claude (the actual TUI)
```

claude takes over the TTY (sets it to raw-ish mode via Ink), reads
keystrokes itself, and has its own Ctrl-Z handler.  When it sees the
0x1A byte it does roughly:

```js
this.props.stdout.write("Claude Code has been suspended. Run `fg` to bring Claude Code back.");
this.props.stdout.write("Note: ctrl + z now suspends Claude Code, ctrl + _ undoes input.");
this.internal_eventEmitter.emit("suspend");
process.on("SIGCONT", _on_resume_handler);
process.kill(0, "SIGTSTP");        // <-- send SIGTSTP to *every* process in the pgrp
```

`process.kill(0, "SIGTSTP")` targets **the entire process group** of
the calling process.  Inside the container the process group for the
foreground job consists of PID 1 + claude + claude's children.  So:

  * **claude** receives SIGTSTP → default disposition stops it.
  * **PID 1** (the bash `-c` wrapper) receives SIGTSTP → default
    disposition stops it.
  * Anything else in the pgrp gets stopped too.

There is no shell with job control between PID 1 and claude — `bash -c
'cmd'` runs `cmd` in the same pgrp as the bash, doesn't enable job
control, and just `wait`s.  Once both are stopped:

  * `podman attach` is just relaying TTY bytes; nothing alive is
    reading them.
  * The host shell's `podman run`/`podman exec` is *not* stopped (it's
    on the host; the SIGTSTP went to processes inside the container's
    pid namespace).  So host bash thinks the foreground job is still
    running and won't give you a prompt.
  * From the user's perspective: keystrokes go nowhere, ^C is also
    just bytes (we put the TTY in raw mode upstream), and the only way
    out is `kill -CONT $(pgrep -f yolo-entrypoint)` from another host
    terminal — or the podman default detach keys (^P^Q).

It's *easy to hit by accident* — e.g. fat-fingering ^Z while reaching
for ^X or ^A — and there is no in-band recovery.  That's the bug.

## What we tried before settling on the proxy

We worked through several less-invasive fixes and ruled each out.

### Option 1 — `stty susp undef` in the entrypoint

Disable the TTY driver's translation of 0x1A → SIGTSTP for the
container-side pty.  Cheap (one line of bash before exec).

**Why it doesn't help.**  The TTY driver isn't what's generating the
suspend.  Claude reads the byte off stdin in raw mode and calls
`process.kill` itself.  Even with VSUSP disabled, claude's handler
fires and the kill succeeds.

We empirically confirmed claude isn't relying on VSUSP: a baseline
test of `podman run --rm -it docker.io/library/alpine:latest cat`
already prints a literal `^Z` and doesn't suspend, meaning podman's
container pty has VSUSP off by default.  Nothing for us to disable.

### Option 2 — `signal.signal(SIGTSTP, SIG_IGN)` before exec

`SIG_IGN` is preserved across `exec()`, so we could install it in the
entrypoint and have claude inherit it.

**Why it doesn't help.**  `process.kill(0, ...)` sends to every
process in the pgrp, and *each* process's disposition is checked
independently.  If we set IGN on PID 1, PID 1 ignores the SIGTSTP —
but claude's disposition is whatever Node initialized (default-stop
for SIGTSTP), and Node *does* set its own SIGTSTP handler when the
TUI library initializes raw-mode handling.  Claude still stops.
Bash (PID 1) is no longer stopped, but it's stuck in `wait()` for
the stopped child anyway.  Net effect: same wedge for slightly
different reasons.

### Option 3 — interactive bash REPL after the command

Run `bash --rcfile <bashrc> -i -c '<cmd>; bash'` so an interactive
REPL inherits.  When ^Z hits, claude stops, the REPL is alive, you
type `fg`, claude resumes, claude exits, REPL drops to a prompt, you
type `exit` to leave.

**Why we rejected it.**  Normal exit becomes a two-step (claude exits
→ REPL prompt → `exit`).  User explicitly didn't want that.  Also
brings interactive bash startup overhead (mise activation hook every
prompt, ~100–300ms).

### Option 4 — `bash -ic '<cmd>'`

The single-stage version: `bash -i -c '<cmd>'`, hoping bash's
interactive mode plus job control would catch the stopped child and
prompt.

**Why it doesn't work.**  Bash with `-c '<single-command>'` does
*not* establish a foreground process group transition for its child,
even with `-i`.  Plus bash often `exec`s itself away when the body of
`-c` is a single simple command.  We tested manually
(`bash -ic 'cat'` and `bash -i scriptfile` on a real host TTY): in
both shapes, ^Z either prints literal `^Z` (no signal) or kills the
child outright; no `[1]+ Stopped` job lands at a bash prompt.  Bash's
job-control machinery requires reading commands from stdin, not
running a one-shot script.

### Option 5 (chosen) — host-side PTY proxy

Sit a small proxy *between* the host shell and `podman run/exec`,
have it own the host TTY, and intercept the 0x1A byte before it ever
reaches the inside-container pty.  When the proxy sees ^Z it
self-suspends; the host shell sees a stopped child, prints `[1]+
Stopped`, prompts.  `fg` resumes the proxy.

This is what we built (`src/cli/tty_proxy.py`).

### Other options we noted but didn't pursue

  * **A claude opt-out flag.**  We grepped the binary; the suspend
    handler is hard-coded with no settings/env hook.  The "Note: ctrl
    + z now suspends Claude Code" text suggests the team knew about
    the lifecycle issue, but the only escape hatch is `fg` — which
    requires a job-control parent we don't have.
  * **`tmux new-session -A -s yolo` as PID 1.**  tmux has full
    JC/TTY machinery and survives detaches.  Big architectural change;
    every existing piece of the entrypoint that talks to PID 1's stdio
    would have to learn about a tmux pane instead.  Filed as a future
    option.
  * **podman's built-in detach keys (`^P^Q`).**  Free, gets you back
    to host shell.  Doesn't help for *accidental* ^Z because by the
    time you'd hit ^P^Q the wedge has already happened; ^P^Q-style
    "escape hatch from a wedged jail" is fine but it still loses the
    claude session that was inside.

## What the proxy does

```
host TTY ──> proxy (raw mode) ──> master pty ──> podman ──> container pty ──> claude
                  │                                                              │
                  │   intercepts 0x1A  ────────────────► self-suspend             │
                  │   raises SIGTSTP   ◄─── host shell ───  fg                    │
host TTY <── proxy ──── master pty <── podman <── container pty <── claude        │
```

Concretely (`src/cli/tty_proxy.py`):

  1. `pty.openpty()` to get a master/slave pair.
  2. `subprocess.Popen([podman, run/exec, ...], stdin=slave,
     stdout=slave, stderr=slave)`.  The slave is now podman's stdio,
     so podman thinks it's connected to a TTY (no `--tty might not
     work properly` warning, no host-TTY raw-mode dance from podman's
     side).
  3. `tty.setraw(host stdin fd)` on the host TTY so 0x1A flows
     verbatim instead of being translated to SIGTSTP at the kernel
     TTY driver layer.
  4. `select()` loop: host stdin → master, master → host stdout.
     When host stdin contains 0x1A, write everything *before* it,
     queue everything *after* it, then call `_self_suspend()`.
  5. `_self_suspend()` restores the host TTY to cooked mode (so the
     shell prompt works) and `os.kill(getpid(), SIGTSTP)`.  Default
     disposition stops us.  Host bash sees WIFSTOPPED, prints `[1]+
     Stopped`, prompts.
  6. SIGCONT handler retakes raw mode when `fg` resumes us.  The
     queued-after-^Z bytes get flushed.
  7. SIGWINCH handler propagates host TTY size changes to the inside
     pty via `TIOCSWINSZ`.
  8. When stdin isn't a TTY (CliRunner, pipes, automation),
     `run_with_proxy` falls back to plain `subprocess.Popen` so test
     harnesses keep working.

## Subtleties worth re-reading before changes

### Why we use `os.kill(getpid(), SIGTSTP)` rather than `signal.raise_signal`

Either works in principle, but `os.kill` with our pid (not pgid)
sends only to us — not the daemon thread that runs `on_started`, not
any forked podman helpers.  POSIX says SIGTSTP delivered to a single
thread stops the *whole process*, so all our threads pause and
resume together.  We want that.

### Why we don't use `start_new_session=True` on Popen

We tried it and removed it.  `setsid()` would put podman in its own
session with no controlling TTY, breaking `-it`'s pty allocation.
We want podman to inherit our session and just have the slave pty as
its stdio.  Bash's job-control machinery still tracks the proxy
because bash put the proxy in its own pgrp before `tcsetpgrp`'d the
foreground.

### The `on_started` daemon thread

The new-container code path used to release a workspace lock between
`Popen()` and `wait()`.  We can't pass through the proxy as a
`subprocess.run` (which blocks the proxy loop).  So `run_with_proxy`
takes an optional callback that runs on a daemon thread post-spawn —
the proxy keeps pumping bytes while the callback waits-for-container
+ releases lock.  When SIGTSTP hits, the kernel stops *all* threads
in the process, including the daemon thread, so there's no race
between "we suspended" and "lock release thread is mid-write."

### Drain-on-exit

When the wrapped process exits, there can still be bytes in the
master pty buffer.  The loop does one final `os.read(master, ...)`
to drain before breaking.  Without this, the last few lines of
output (e.g. claude's exit summary) would be truncated.

## Things to revisit

  * **Detach-and-keep-running.**  Currently ^Z stops the proxy,
    which means podman is also waiting (its stdio is the proxy's
    pty).  In practice claude inside is *not* suspended (we didn't
    forward the byte), so it'll keep its API context but block on
    its next stdout write.  If we want true "background the jail and
    come back later," we'd need the proxy to detach (close podman's
    stdin/stdout, exit cleanly) on a different keystroke (^P^Q
    style).  Probably worth doing — pair it with an in-band reattach
    command.
  * **Double-^Z passthrough.**  Some users may legitimately want to
    send 0x1A as a literal byte to the inside (vim's `:stop` etc.).
    Not supported today.  Pattern would be: hold a key, then ^Z
    forwards instead of intercepts.  Punt.
  * **Tests for the byte-splitting math.**  The pre-^Z / post-^Z
    queueing logic in `_proxy_loop` deserves a unit test that fakes
    stdin/master fds with `os.pipe`.  Right now we only test the
    no-TTY fallback path.
  * **Behavior when claude itself is stopped from inside.**  If
    something *else* inside the jail manages to SIGSTOP claude, the
    proxy doesn't know — output stops, input still forwards, user
    sees a frozen claude.  Different bug, different fix.
  * **Windows / Apple Container.**  Apple Container probably needs
    its own treatment (different pty semantics).  Today the proxy
    runs uniformly because all three runtimes (`podman`, `docker`,
    `container`) share the `subprocess.run` call site.  Hasn't been
    tested on AC; flag if you go there.

## How to verify after a change

  1. **Smoke test the proxy alone, on a real host TTY:**
     ```
     python3 ~/code/system/yolo-jail/scratch/test_tty_proxy.py
     ```
     Should run `cat`; ^Z drops to host prompt with `[1]+ Stopped`,
     `fg` resumes, ^D exits cleanly.
  2. **Real claude:**
     ```
     yolo -- claude
     ```
     Hit ^Z while claude is up; you should land at the host shell
     (no "Claude Code has been suspended" message — claude never saw
     the byte).  `fg` brings the proxy back, claude is unchanged.
  3. **Plain shell:**
     ```
     yolo
     ```
     Should still feel like a normal interactive shell (^C, edits,
     window resize, exit) — the proxy is in the path but
     transparent.
  4. **Pipes:**
     ```
     echo hi | yolo -- cat
     ```
     `run_with_proxy` should fall back to plain `Popen` since stdin
     isn't a TTY; output should be `hi` and exit 0.

## Files involved

  * `src/cli/tty_proxy.py` — the proxy module.
  * `src/cli/run_cmd.py` — three call sites use `run_with_proxy`:
    exec-into-existing (line ~615), exec-into-raced-existing (~670),
    new-container (~1860).
  * `src/cli/__init__.py` — re-exports `run_with_proxy` so tests can
    `from cli import run_with_proxy`.
  * `tests/test_cli_unit.py` — `TestRunWithProxy` covers the no-TTY
    fallback path.
  * `scratch/test_tty_proxy.py` — manual smoke test harness (not
    committed; gitignored under `scratch/`).
