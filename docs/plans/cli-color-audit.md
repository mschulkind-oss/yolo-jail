# Plan: CLI color audit — render rich markup, don't strip it

**Status:** DONE (2026-07-22). The confirmed bug class is closed and every
command is classified: `prune`/`builder`/`macosuser` fixed (2026-07-20),
`broker` (`8e5302f`) and `ps` (`d71dba3`) folded in since, the last rich→ANSI
duplicate (`run/console.go`) consolidated onto `internal/richtext` (`67454a8`),
the TTY probe unified onto `internal/tty` (`b76b2ba`), a genuine `check`/`doctor`
ANSI-leak-to-a-pipe closed (`c9ea5e8`), and the last three commands
(`loopholes`/`init`/`init-user-config`) classified — `init` already colors
correctly, the other two are intentionally plain (no leaked markup). Pulled out
of the archived `go-port-post-transition.md` §5. Was jail-testable end-to-end
(no host needed); paired with the renderer consolidation in
[module-consolidation-and-cleanup.md](module-consolidation-and-cleanup.md).

## The bug class

Several command packages carry a `printer` that threads a `color` flag but
**strips rich markup unconditionally** (`richTagRe.ReplaceAllString(s, "")`)
instead of rendering it to ANSI on a TTY. So their output is silently colorless.
`internal/cli/run` was fixed (its `console.go` has the color-aware `richToANSI` +
`stripRich` + `Color && IsTTY` gate); the others were never audited.

This is **cosmetic** — the byte-parity contract was always on the ANSI-*stripped*
text, so numbers/decisions were never affected, only on-screen color. Safe, but
worth closing.

## Current state (verified 2026-07-20)

The shared renderer landed as `internal/richtext` (`ToANSI`/`Strip`/`Render` +
`Printer{W, Color}`), extracted from `run`'s `console.go`. The three
strip-always printers were routed through it:

- [x] **`prune`** — printer embeds `richtext.Printer`; ANSI gated on
  `Color && IsTTYStdout()` (genuine TCGETS/TIOCGETA ioctl). (`5abc09c`)
- [x] **`builder`** — printer wraps `richtext.Printer`; `Deps` gained
  `Color` + `IsTTYStdout`; `newPrinter` resolves the gate. (`36af30e`)
- [x] **`macos-*`** — printer gains a `color` field; dry-run plan render forced
  color-OFF (byte-pinned goldens), only live chatter colors. (`20e73c0`)

The last unconsolidated duplicate is gone: `internal/cli/run/console.go` now
wraps `internal/richtext` (`67454a8`) — its `printer` embeds a
`richtext.Printer` and `pr` builds it with `Color: o.Color && o.IsTTYStdout()`,
so the color-aware behavior and gate are unchanged but the renderer code is no
longer a copy.

Classification of the remaining commands:

- [x] **`check`/`doctor`** (`internal/cli/check`) — colors on a TTY (verified: a
  pty run emits ANSI, e.g. `[1;32m[PASS]`), but it was a genuine
  **leak-to-a-pipe** bug: `Color` was requested unconditionally with no TTY gate,
  so a piped/redirected run spilled 35 SGR escapes. Fixed by the same
  `Color && IsTTYStdout()` gate as run, with an injectable `IsTTYStdout` seam
  (`c9ea5e8`); regression `TestColorGatedOnTTY`.
- [x] **`config-ref`** (`internal/cli/configref.go` — a single file in package
  `cli`, not a `configref/` package/dir) — has a `tagReplacer` ANSI renderer
  gated on a `color` bool. TTY wiring is **confirmed**: `RunStdout` calls
  `configRefRun(os.Stdout, isTTY(os.Stdout))`, and its `isTTY` now delegates to
  the shared `internal/tty` ioctl probe (`b76b2ba`) — no longer the char-device
  check.
- [x] **`loopholes`, `init`, `init-user-config`** — classified:
  - **`init`** *colors* — its `printBriefing` calls `renderMarkup(text, color)`
    with `color = isTTYStdout()` (`commands.go:210`, the shared ioctl gate), so
    the briefing's `[bold …]` tags render to ANSI on a terminal and strip on a
    pipe. Correct as-is.
  - **`init-user-config`** is *intentionally plain* — it emits only status lines
    (`Created %s`, `%s already exists`) with no rich markup and no color seam.
  - **`loopholes`** is *intentionally plain* — `List`/`Status`/`CmdSetEnabled`
    are pure `fmt.Fprintln`/`Fprintf` (bullets, `[PASS]`-style prefixes) with no
    ANSI, no color flag, and — verified — no leaked literal `[bold …]` tags.

## The work

- [x] Port `run`'s render path to `prune`, `builder`, `macosuser` via the shared
  `internal/richtext` renderer.
- [x] **Consolidate the renderer.** The color-aware rich→ANSI renderer now lives
  in exactly ONE place (`internal/richtext`); `prune`, `builder`, `macosuser`,
  `broker` (`8e5302f`), `ps` (`d71dba3`), and finally `internal/cli/run/console.go`
  (`67454a8` — the copy richtext was extracted *from*) all route through it. The
  natural intersection with
  [module-consolidation-and-cleanup.md](module-consolidation-and-cleanup.md).
- [x] Audit + classify the remaining commands; fix the ones that lost color.
  `check`/`doctor` (leak fixed) and `config-ref` done; `loopholes`/`init`/
  `init-user-config` classified — `init` colors, the other two are intentionally
  plain.
- [x] **Unify the TTY probe.** The two conventions were folded onto one shared
  `internal/tty` helper (`IsTerminal(fd)` / `IsTerminalFile(*os.File)`, the
  `TCGETS`/`TIOCGETA` ioctl) in `b76b2ba`. The old `os.ModeCharDevice`
  char-device check (which false-positived on the container `-t` flag and on
  `/dev/null`) is gone from `commands.go`, `terminal.go`, `configref.go`,
  `runcmd.go`, `broker`, and `builder`; the dead `isattyFD` copies were deleted.
  Regression tests cover the nil, pipe, and `/dev/null` cases.
- [ ] **Gate rule (same as run):** render ANSI only when `Color &&
  IsTTYStdout()` — never emit escapes to a pipe/redirect, so captured/greppable
  output stays clean and `NO_COLOR` is honored.

## Tests

Each fixed command gets a test asserting: markup → ANSI escapes when color+TTY,
and plain text (no escapes, no literal `[bold]`) when not — mirroring the
existing `internal/cli/run` console tests.
