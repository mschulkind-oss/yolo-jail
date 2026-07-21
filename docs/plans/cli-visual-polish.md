# Plan: CLI visual polish — color to guide the eye

**Status:** OPEN — audit complete (2026-07-20), no code written yet. This is the
*content* follow-on to
[cli-color-audit.md](cli-color-audit.md): that plan fixed the color-*rendering*
mechanism (rich markup now renders to ANSI on a TTY via `internal/richtext`
instead of being stripped unconditionally); this plan spends that capability by
making every `yolo` CLI surface actually colorful, scannable, and eye-friendly
(a syntax-highlighting feel). Jail-testable end-to-end (no host needed).

## Goal

Every human-facing `yolo` surface uses color to guide the eye — status
vocabulary (ok/fail/warn) is colored, headers are bold, identifiers and paths
are distinct from prose, and action hints stand out — following ONE consistent
semantic convention across all commands. Today three surfaces already hit this
bar (`check`, `broker status`, `builder status`, `prune`, `macos-*`); several
others emit textbook color-mappable content as flat monochrome.

## The invariant — color is ADDITIVE

Color is layered on top of text that is otherwise unchanged. The load-bearing
rule for every edit below:

- **ANSI-stripped output stays byte-identical.** All coloring is done by wrapping
  *existing* literal text in `internal/richtext` tags (`[bold]…[/bold]`,
  `[green]…[/green]`, …). When color is off (`Strip`), the tags vanish and the
  bytes are exactly what they were before. Golden/parity tests that pin
  `Color=false` therefore do not break.
- **Color only on a TTY.** Render ANSI only when `Color && IsTTYStdout()`
  (the gate `internal/cli/run` and now `prune` use). Never emit escapes to a
  pipe/redirect; honor `NO_COLOR`. Captured/greppable output stays clean.
- **Glyphs are literal text.** Any `✓`/`✗`/`!` symbols added are plain Unicode
  colored via existing tags — they survive ANSI-strip as literal characters, so
  they are part of the (new but stable) plain-text baseline, not escape codes.
- **Frozen-byte surfaces are the exception.** A few surfaces are pinned
  byte-for-byte by goldens *including* their current bytes (run boot lines,
  banner strings, macos dry-run plan). Changing those is a deliberate golden
  update requiring human sign-off, not a silent additive change — flagged per
  item below.

## Semantic color convention (apply everywhere)

One grammar for the whole CLI, expressible entirely in the current 6-tag palette:

| Meaning | Tag | Used for |
|---|---|---|
| ok / success / healthy | `[green]` | PASS, ok, active, Up/running, "yes", ✓, reclaimed |
| failure / danger | `[red]` | FAIL, problem jails, dead, destructive-action warnings, ✗ |
| warning / attention | `[yellow]` | WARN, inactive, not-set-up, DRY-RUN, soft errors, `!` |
| secondary / detail | `[dim]` | paths-as-detail, annotations, tips, "none", idle-is-normal notes |
| header / structure | `[bold]` | section headers, table header rows, summary rows, mode banners |
| identifier / path / action | `[cyan]` | command names to run, flags, config/socket/conf paths, tokens |

Compound where it sharpens: `[bold green]` for a strong "all good" verdict,
`[bold red]` for a destructive-action banner, `[bold cyan]` for the primary
scan target (e.g. `--help` command names).

## Palette gaps (enabling dependency — do FIRST if needed)

`internal/richtext` offers 6 tags: `bold`, `dim`, `red`, `green`, `yellow`,
`cyan` (`ansiForTag`, richtext.go:34-37) — 4 true hues plus 2 modifiers, no
background/inverse. Two audited surfaces want more than that:

1. **`config render --explain` — 6 provenance layers, 4 hues.** The layer column
   (`defaults`/`host`/`workspace`/`overlay`/`transform`/`managed`) is a closed
   set that begs for one-hue-per-layer, but 6 layers can't each get a distinct
   hue. **Options:** (a) extend richtext with `magenta` (ANSI 35) + `blue`
   (ANSI 34) so each layer gets its own hue; or (b) map by semantic *role*
   within the current palette — e.g. `managed`→`[bold red]` (authoritative/
   locked), `transform`→`[yellow]` (computed), `host`→`[cyan]`,
   `workspace`→`[bold cyan]`, `overlay`→`[green]`, `defaults`→`[dim]`. Option (b)
   ships today with no renderer change; (a) is the cleaner long-term key.
2. **`check` badges use background/inverse video.** `reporter.go:20-23` renders
   `[FAIL]` white-on-red and `[WARN]` black-on-yellow via its own ANSI constants
   — backgrounds richtext cannot express. This is only relevant *if* `check` is
   ever unified onto richtext; today check keeps its private, strictly-richer
   ANSI set (recommended). If unification is ever desired, richtext must gain
   background/inverse tags first, else the badges degrade to plain fg red/yellow.

**Richtext-extension task (small, optional, unblocks the above):** add
`magenta`/`blue` hues (and, only if check is ever unified, background/inverse
tags) to `ansiForTag`. Until then, prefer the role-mapping fallback for
`--explain`. Everything else in this plan fits the existing 6 tags.

## The one structural change — `yolo --help`

Unlike the other items (which just wrap existing tags around existing text),
`yolo --help` needs a **mechanism** change. `usageText()` (help.go:27-47) returns
a *pure plain string* printed via `fmt.Print` at cli.go:48 with **no color and no
TTY gate**, and `help_test.go` asserts on the raw text. Coloring it means routing
it through the renderer:

- Have `usageText` emit rich tags, then render at the print site gated on
  `Color && isTTY(os.Stdout)` — mirror `configref.go`'s `Render(color)` / `isTTY`
  pattern. Substring-presence tests keep passing because the plain path strips
  the tags. Same mechanism cost applies to `config --help` / `configUsage`
  (config.go:26-44, written via `io.WriteString` with no color path) and the
  whole of `config.go`, which has no color path at all today.

## Per-command checklist

Impact = eye-friendliness gain; Effort accounts for plumbing + golden risk.
Palette is sufficient for every item unless noted.

### Group A — RED: flat monochrome, needs richtext plumbing + coloring

These emit color-mappable status vocabulary but have no color path at all.
Highest value, low risk (text stays byte-identical after strip).

- [ ] **`yolo loopholes status`** (loopholescmd.go Status, L127-153) —
  **Impact: high · Effort: med.** *The single biggest missed opportunity.* Wire a
  `richtext.Printer` + color gate into `Deps` (package currently writes to a raw
  `io.Writer`, no TTY probe). Then color the existing bracket prefixes:
  `[ok]`→green, `[fail]`→red, `[inactive]`→yellow, `[disabled]`/`[no-check]`→dim
  (direct reuse of check's proven pass/fail vocabulary). Bold the loophole Name;
  dim `rc=%s` and the wrapped Output detail lines (L152); cyan the suggested
  `yolo loopholes status` command in the in-jail short-circuit line (L127).
- [ ] **`yolo loopholes list`** (loopholescmd.go List, L89-118) —
  **Impact: high · Effort: med** (same Deps plumbing as status). Color the status
  label green `active` / yellow `inactive (reason)` / dim `disabled`; bold the
  loophole Name so each row anchors; dim the `(source/transport/lifecycle)` tags
  and the `transport=`/`intercepts=` metadata; dim the description continuation
  (L117); bold the `• bundled/user/workspace` empty-state bullet labels.
- [ ] **`yolo ps`** (ps.go + runtime/display.go RenderPsTable) —
  **Impact: high · Effort: med.** No color plumbing at all today; `RenderPsTable`
  has no golden/parity test. Add a color gate + `Printer` into `psDeps.Out`
  first. Then: bold the header row (`CONTAINER STATUS WORKSPACE`); color the
  STATUS column by state (green Up/running, red/yellow otherwise — thread the
  color flag into `RenderPsTable`, or color at the ps.go layer); make the
  problem-jails block red (`[red]⚠  %d problem jail(s):[/red]`, `(reason)` in
  red/yellow); cyan the `yolo doctor` remediation hint (L110) and the
  "Could not query…" runtime-error line (L58), matching broker/builder.

### Group B — YELLOW: partial or plain, mostly additive

- [ ] **`config render --explain` layer column** (config.go renderSurface
  L167-176 + agentcfg/compose.go ProvenanceLines L216) —
  **Impact: high · Effort: med** (needs the config.go color path, see structural
  change; and see palette gap #1 for the hue decision). Color the LAYER token so
  the output scans like syntax highlighting — which keys `managed` clobbered vs
  came from `host`/`transform`. Also pad the key column to a common width (it's
  tab-separated + ragged today) — that helps scannability even before color.
- [ ] **`yolo --help`** (help.go usageText) — **Impact: high · Effort: med**
  (the structural change above). Headers `Usage:`/`Commands:`→bold; each command
  NAME (left column, L43)→`[bold cyan]` (the scan target); highlight the literal
  `yolo --` / subcommand tokens (L31-32)→cyan, description dim; trailing pointer
  line (L45)→dim; tagline (L29)→dim.
- [ ] **`config --help` / `configUsage`** (config.go L26-44) —
  **Impact: med · Effort: med** (same config.go color path). Headers
  `Usage:`/`Subcommands:`/`render flags:`→bold; `render <agent>` token and each
  flag (`--surface`, `--explain`, `--help, -h`)→cyan; file paths
  (`yolo-jail.config.lua`, `~/.config/yolo-jail/config.lua`)→cyan or dim.
- [ ] **`yolo init` / `init-user-config`** (init.go L66/73/76/105/108) —
  **Impact: med · Effort: low-med.** Color the scaffolder's own status lines to
  match the richly-styled briefing that follows: `Created …`→green,
  `already exists`→yellow, the two error paths→`[bold red]`. **Blocker:** init.go
  uses cli/markup.go's closed-set replacer (bold/cyan/green/yellow only — **no
  red**). Either add `red`/`dim` to `markupANSI`+`markupStrip`, or route init's
  status lines through `internal/richtext.Printer` (which has red). Flag this
  missing-tag gap.
- [ ] **`yolo run` progress sub-steps** (run/command.go setupScript L15-22) —
  **Impact: med · Effort: med, FROZEN BYTES.** The `↳ mise install / mise
  upgrade / bootstrap` phase lines render as flat plain text against mise's own
  chatter; give them `[cyan]` or `[bold]` so each phase boundary reads as a
  heading. **Caveat:** testdata/final_cmd_bash.txt + command_test.go pin these
  exact bytes → deliberate golden update, human sign-off, NOT additive.
- [ ] **`yolo` startup banner** (run/banner.go StartupBanner + run.go:385) —
  **Impact: low-med · Effort: low.** First line of every launch, fully
  monochrome. Render through `richtext.Render` **at the emit site** (run.go:385)
  so `StartupBanner`'s returned string stays byte-identical for banner_test.go:
  dim the whole banner (it's metadata), or cyan the runtime / bold the version so
  the pipe-delimited fields parse.

### Group C — GREEN: already good, optional polish

- [ ] **`yolo check` / doctor** — add scannable glyphs before badges
  (`[PASS]`→green ✓, `[FAIL]`→red ✗, `[WARN]`→yellow `!`); dim the ` -> workspace`
  tail of running-jail rows (check.go:536) so the jail name pops; cyan the
  config/storage paths in `ok()` lines. Keep check's private ANSI set (it's
  richer than richtext — see palette gap #2). **Low effort, additive.**
- [ ] **`yolo broker status`** — dim/cyan the `pid file:`/`socket:` PATH values
  (L92/98) so label/value split is visible; add ✓/✗ glyphs before
  live/present/ok; align trailing marks into a fixed status column for vertical
  scanning. **Low effort, additive.**
- [ ] **`yolo builder status`** — dim the parenthetical `(%s)` conf paths
  (L55/58); make `mark()` emit `[green]✓ yes` / `[red]✗ no`; bold the two
  top-level rows (`set up`, `reachable`) to separate summary from detail.
  **Low effort, additive.**
- [ ] **`yolo prune` mode banner** (prunecmd.go:166) — color the header mode
  token so DRY-RUN vs APPLY is obvious at the top, not only in the far summary:
  `[bold yellow]yolo prune (DRY-RUN)` vs `[bold red/green]yolo prune (APPLY)`.
  Goldens pin `Color=false` → stripped bytes unchanged. **Low effort, additive.**
- [ ] **`config-ref`** — optional: `[green]` for value literals
  (Values:/Default: lines) and `[dim]` for annotation lines (Override:/
  Auto-detect:) to add a third scan tier. Already GREEN; polish only.

## Cross-cutting

- [ ] **Colorize resource PATHS consistently** across all six state commands —
  dim or cyan the path portion of `label: /some/path` lines so the label/value
  boundary is visible (broker pid-file/socket, check storage/config paths,
  builder conf paths, config-ref file paths).
- [ ] **Consolidate tag→ANSI renderers.** Three-plus parallel tables exist:
  richtext's `ansiForTag`, configref.go's `tagReplacer`, cli/markup.go's
  `markupANSI`. `richtext.ansiForTag` already covers the full palette. Route the
  plain surfaces onto `internal/richtext` rather than growing a fourth table
  (also unblocks init's missing-red gap). Natural intersection with
  [module-consolidation-and-cleanup.md](module-consolidation-and-cleanup.md).

## Enforcement / tests

The byte-parity contract is already the enforcement mechanism: golden and
substring tests assert on the **ANSI-stripped** text, so additive coloring can't
regress them. For each newly-colored command add (mirroring the existing
`internal/cli/run` console tests): markup → ANSI escapes when `color && TTY`, and
plain text (no escapes, no literal `[bold]`) when not. For the frozen-byte
surfaces (run boot lines, banner, macos dry-run plan) any change is a deliberate
golden update — call it out for human sign-off, don't fold it in silently.

## Suggested order

1. Richtext palette extension (magenta/blue) — only if `--explain` takes the
   one-hue-per-layer route; otherwise skip and use role-mapping.
2. Group A (loopholes status → loopholes list → ps): highest value, self-
   contained plumbing, no goldens.
3. `--help` + config surfaces (the structural renderer-routing change), then
   `--explain`.
4. init status lines (resolve the markup.go red gap or route to richtext).
5. Group C polish + cross-cutting path/glyph passes.
6. Frozen-byte surfaces (run sub-steps, banner) last, each with human sign-off.
