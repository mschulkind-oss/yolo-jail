# Plan: CLI color audit — render rich markup, don't strip it

**Status:** OPEN — confirmed bug class, partially fixed. Pulled out of the
archived `go-port-post-transition.md` §5. Jail-testable end-to-end (no host
needed); pairs naturally with the renderer consolidation in
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

Confirmed still strip-always:

- [ ] **`prune`** (`internal/prune/prunecmd.go:437` — `printer.line` does
  `richTagRe.ReplaceAllString(s, "")` despite carrying `p.color`).
- [ ] **`builder`** (`internal/builder/buildercmd.go:90` — `printer.print` same
  pattern).
- [ ] **`macos-*`** (`internal/macosuser/orchestrator.go:83` — `printer.print`
  same pattern; dry-run plan + macos-setup/teardown output).

Reference (already correct): `internal/cli/run/console.go:57-99` — `richToANSI`
renders known style tags to ANSI, `stripRich` for the no-color path, gated on
`Color && IsTTYStdout()`.

Still to classify (audit each — "intentionally plain" vs "lost its color"):

- [ ] **`check`/`doctor`** (`internal/cli/check`) — has its own ANSI path;
  verify it actually colors on a TTY and isn't a strip-always in disguise.
- [ ] **`config-ref`** (`internal/cli/configref`) — has a `tagReplacer` ANSI
  renderer gated on a `color` bool; confirm it's wired to a real TTY check.
- [ ] **`loopholes`, `broker`, `init`, `init-user-config`, `ps`** — some are
  intentionally plain (byte-parity, no color), some should color. Classify each.

## The work

- [ ] Port `run`'s `richToANSI` path to `prune`, `builder`, `macosuser` (or,
  better, route them through a shared renderer — see below).
- [ ] Audit + classify the remaining commands; fix the ones that lost color.
- [ ] **Consolidate the renderer.** Four+ packages carry near-duplicate
  `richTagRe` printers. Lift the color-aware rich→ANSI renderer into ONE shared
  helper (mirror `internal/cli/run/console.go`'s `richToANSI` + `isStyleTag` +
  the gate) and route every command through it — fixes the bug everywhere and
  removes the duplication. This is the natural intersection with
  [module-consolidation-and-cleanup.md](module-consolidation-and-cleanup.md); do it there if the
  consolidation lands first.
- [ ] **Gate rule (same as run):** render ANSI only when `Color &&
  IsTTYStdout()` — never emit escapes to a pipe/redirect, so captured/greppable
  output stays clean and `NO_COLOR` is honored.

## Tests

Each fixed command gets a test asserting: markup → ANSI escapes when color+TTY,
and plain text (no escapes, no literal `[bold]`) when not — mirroring the
existing `internal/cli/run` console tests.
