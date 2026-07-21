# Plan: CLI color audit — render rich markup, don't strip it

**Status:** OPEN — confirmed bug class, `prune`/`builder`/`macosuser` fixed
(2026-07-20). Pulled out of the archived `go-port-post-transition.md` §5.
Jail-testable end-to-end (no host needed); pairs naturally with the renderer
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

Reference (already correct): `internal/cli/run/console.go` delegates to
`internal/richtext`; the gate is `Color && IsTTYStdout()`.

Still to classify (audit each — "intentionally plain" vs "lost its color"):

- [ ] **`check`/`doctor`** (`internal/cli/check`) — has its own ANSI path;
  verify it actually colors on a TTY and isn't a strip-always in disguise.
- [ ] **`config-ref`** (`internal/cli/configref`) — has a `tagReplacer` ANSI
  renderer gated on a `color` bool; confirm it's wired to a real TTY check.
- [ ] **`loopholes`, `broker`, `init`, `init-user-config`, `ps`** — some are
  intentionally plain (byte-parity, no color), some should color. Classify each.

## The work

- [x] Port `run`'s render path to `prune`, `builder`, `macosuser` via the shared
  `internal/richtext` renderer.
- [x] **Consolidate the renderer.** The color-aware rich→ANSI renderer now lives
  in ONE place (`internal/richtext`); `run`, `prune`, `builder`, and `macosuser`
  all route through it. Remaining duplicate `richTagRe` printers (e.g.
  `internal/broker`) still need routing — the natural intersection with
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
