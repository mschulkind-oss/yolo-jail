# Plan: CLI color audit — render rich markup, don't strip it

**Status:** OPEN (cleanup only) — the confirmed bug class is closed:
`prune`/`builder`/`macosuser` fixed (2026-07-20), and `broker` (`8e5302f`) and
`ps` (`d71dba3`) folded in since. The only remaining work is consolidating the
last rich→ANSI duplicate (`run/console.go`) onto `internal/richtext` and
unifying the TTY probe. Pulled out of the archived `go-port-post-transition.md`
§5. Jail-testable end-to-end (no host needed); pairs naturally with the renderer
consolidation in
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

The last unconsolidated duplicate: `internal/cli/run/console.go` still carries
its own `richTagRe`/`richToANSI`/`stripRich` (it is the file `internal/richtext`
was extracted *from*, and it does **not** import `internal/richtext`). Its
color-aware behavior and gate are correct — `pr` builds the printer with
`color: o.Color && o.IsTTYStdout()` (console.go ~line 114) — but the renderer
code is a copy, so migrating it onto `internal/richtext` is the remaining
consolidation step.

Still to classify (audit each — "intentionally plain" vs "lost its color"):

- [ ] **`check`/`doctor`** (`internal/cli/check`) — has its own ANSI path;
  verify it actually colors on a TTY and isn't a strip-always in disguise.
- [ ] **`config-ref`** (`internal/cli/configref.go` — a single file in package
  `cli`, not a `configref/` package/dir) — has a `tagReplacer` ANSI renderer
  gated on a `color` bool. TTY wiring is **confirmed**: `RunStdout` calls
  `configRefRun(os.Stdout, isTTY(os.Stdout))`. Its `isTTY` is the char-device
  check, not the ioctl — the char-device-vs-ioctl concern is tracked under
  "Unify the TTY probe" below.
- [ ] **`loopholes`, `init`, `init-user-config`** — some are intentionally plain
  (byte-parity, no color), some should color. Classify each. (`broker` and `ps`
  are done — see status above.)

## The work

- [x] Port `run`'s render path to `prune`, `builder`, `macosuser` via the shared
  `internal/richtext` renderer.
- [~] **Consolidate the renderer.** The color-aware rich→ANSI renderer now lives
  in essentially ONE place (`internal/richtext`); `prune`, `builder`,
  `macosuser`, `broker` (`8e5302f`), and `ps` (`d71dba3`) all route through it.
  It still lives in TWO places pending one conversion: `internal/cli/run/console.go`
  is the last duplicate `richTagRe`/`richToANSI`/`stripRich` printer (the copy
  richtext was extracted from) and still needs routing — the natural
  intersection with
  [module-consolidation-and-cleanup.md](module-consolidation-and-cleanup.md).
- [ ] Audit + classify the remaining commands; fix the ones that lost color.
- [ ] **Unify the TTY probe.** Two conventions now coexist: the ioctl
  (`TCGETS`/`TIOCGETA`, used by `internal/cli/run` and now `prune`) vs. the
  `os.ModeCharDevice` char-device check (used by `internal/cli/commands.go`,
  `terminal.go`, `configref.go`, and now `builder`/`macosuser`). The ioctl is
  more correct (a char-device check false-positives on the container `-t` flag
  and on `/dev/null`); fold everything onto one shared `isTTY(fd)` helper.
- [ ] **Gate rule (same as run):** render ANSI only when `Color &&
  IsTTYStdout()` — never emit escapes to a pipe/redirect, so captured/greppable
  output stays clean and `NO_COLOR` is honored.

## Tests

Each fixed command gets a test asserting: markup → ANSI escapes when color+TTY,
and plain text (no escapes, no literal `[bold]`) when not — mirroring the
existing `internal/cli/run` console tests.
