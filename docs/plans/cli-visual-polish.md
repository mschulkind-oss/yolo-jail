# Plan: CLI visual polish — color to guide the eye

**Status:** DECIDED, 2026-07-20 — in progress, **no checklist item has moved since 2026-07-21 —
re-checked 2026-09-24**, when this doc's line-number anchors were replaced by symbol names because
they had drifted. `broker status` below is now `yolo host-daemon status`; the `broker` spelling
survives as an alias for the Claude broker (2026-09-20). One item in
the "remaining" list below is now unbuildable as written: `builder` polish, because `yolo builder`
and `internal/builder` were **deleted** on 2026-07-23 when the container builder became the sole
builder. Audit complete (2026-07-20); top surfaces landed
(2026-07-21): the richtext palette gained blue+magenta; `yolo --help` routes
through the renderer (bold headers, cyan command names); `config render
--explain` is syntax-highlighted provenance (one hue per layer); `yolo ps` is
colored (dim/red/yellow, bold header, TTY-gated, byte-parity locked). `check`
keeps its own richer private ANSI (background/inverse badges richtext lacks) —
left as-is per the palette-gaps note. Remaining: `init`/`init-user-config`
status lines, `broker`/`builder` polish, and the run-boot frozen lines (golden
updates needing sign-off). The invariant's `NO_COLOR` clause, unbuilt until then, is
enforced by one gate since 2026-09-26 — see
[Implementation decisions](#implementation-decisions--no_color-2026-09-26), which also names
the one gap left. This is the *content* follow-on to
[cli-color-audit.md](cli-color-audit.md): that plan fixed the color-*rendering*
mechanism (rich markup now renders to ANSI on a TTY via `internal/richtext`
instead of being stripped unconditionally); this plan spends that capability by
making every `yolo` CLI surface actually colorful, scannable, and eye-friendly
(a syntax-highlighting feel). Jail-testable end-to-end (no host needed).

## Goal

Every human-facing `yolo` surface uses color to guide the eye — status
vocabulary (ok/fail/warn) is colored, headers are bold, identifiers and paths
are distinct from prose, and action hints stand out — following ONE consistent
semantic convention across all commands. Today four surfaces already hit this
bar (`check`, `host-daemon status`, `prune`, `macos-*`); several others emit
textbook color-mappable content as flat monochrome.

## The invariant — color is ADDITIVE

Color is layered on top of text that is otherwise unchanged. The load-bearing
rule for every edit below:

- **ANSI-stripped output stays byte-identical.** All coloring is done by wrapping
  *existing* literal text in `internal/richtext` tags (`[bold]…[/bold]`,
  `[green]…[/green]`, …). When color is off (`Strip`), the tags vanish and the
  bytes are exactly what they were before. Golden/parity tests that pin
  `Color=false` therefore do not break.
- **Color only on a TTY, and never under `NO_COLOR`.** Every color decision
  goes through one **color gate** — the predicate that decides whether one
  stream receives ANSI escapes — which is `tty.Color` in `internal/tty`. It
  renders ANSI only when the command requested color, the stream it writes is
  a real terminal, and `NO_COLOR` is unset or empty (the
  [NO_COLOR convention](https://no-color.org): any non-empty value disables
  color). Never emit escapes to a pipe/redirect. Captured/greppable output
  stays clean. Text that another process prints — the bash a launch generates
  for the jail, the entrypoint's hand-over line — takes the gate's `NO_COLOR`
  half alone, `tty.NoColor`, because the process that decides cannot probe the
  stream that text reaches. [Implementation decisions](#implementation-decisions--no_color-2026-09-26)
  records how, and the one gap left.
- **Glyphs are literal text.** Any `✓`/`✗`/`!` symbols added are plain Unicode
  colored via existing tags — they survive ANSI-strip as literal characters, so
  they are part of the (new but stable) plain-text baseline, not escape codes.
- **Frozen-byte surfaces are the exception.** A few surfaces are pinned
  byte-for-byte by goldens *including* their current bytes (run boot lines,
  banner strings, macos dry-run plan). Changing those is a deliberate golden
  update requiring human sign-off, not a silent additive change — flagged per
  item below.

## Implementation decisions — `NO_COLOR` (2026-09-26)

The invariant has said to honor `NO_COLOR` since 2026-07-20, and until 2026-09-26 nothing
read the variable: each engine composed `Color && IsTTYStdout()` itself, and most `yolo`
entry points passed a terminal probe as their color. These are the mechanism choices that
made the ruled behavior work. None is a product ruling.

| # | Decision | Why |
| :--- | :--- | :--- |
| 1 | **One gate.** `tty.Color(getenv, requested, terminal)` decides every stream yolo writes. `tty.NoColor(getenv)` is the only reader of the variable, and `Color` is built on it. In package `cli`, `colorForWriter` is the one color decision, and every entry point writing to stdout asks it. | "Honor `NO_COLOR`" is one rule. Written into each gate separately, one gets missed — and before this, every one was. |
| 2 | **An empty value counts as unset.** `NO_COLOR= yolo check` colors. | The convention's own definition of "set". |
| 3 | **The environment read is the one the command already reads.** The run pipeline and `check` read `NO_COLOR` through their injected `Options.Getenv`, and macos-user through `Deps.Getenv`; its provisioning stage's line reads the env the sandbox will run in. Everything else reads the process environment. | One invocation reads one environment, and a test's fake environment decides color the way it decides everything else. |
| 4 | **Text another process prints takes only the `NoColor` half.** That is the generated container script's three lines (`📦 Provisioning tools...`, the provisioning failure line, `⚡ Executing`), the macos-user provisioning stage's failure line, and the entrypoint's exec-into `⚡ Executing` line. With `NO_COLOR` unset their bytes are unchanged, so `internal/cli/run/testdata/final_cmd_bash.txt` did not move. | They print from a stream the deciding process cannot probe, and they were never terminal-gated. `provision.Script` therefore takes the decision as a parameter rather than making it. |
| 5 | **`NO_COLOR` crosses into the jail**, beside `TERM` and `COLORTERM`: as `-e NO_COLOR=<value>` on a container launch and on an attach's `exec`, and in the session env file on macos-user. It crosses only when non-empty. | The in-jail `yolo`, the entrypoint and the prompt read it there. A running container keeps its launch environment, so the attach carries it too. The consequence is deliberate: an agent CLI that honors the convention also sees it, which is what setting it asks. |
| 6 | **The jail's shell honors it at start.** The generated `.bashrc` defines the prompt colors and the `ls --color=auto` alias only when `NO_COLOR` is unset or empty. | The prompt is color yolo adds. GNU `ls` does not read the variable, so the alias is what has to be withheld. |
| 7 | **`NO_COLOR` changes color and nothing else.** It never changes whether yolo is interactive (the run's `-t` flag, its prompts), never touches JSON output (already colorless), and never suppresses ttyproxy's terminal reset. | `isTTYStdout` stays the interaction probe. The reset *clears* attributes a child left set; the convention is about adding them. |
| 8 | **Enforced by an inventory**, not by review. `TestEveryColorEmitterIsInventoried` (in `internal/tty`) lists every file that emits ANSI together with the gate that governs it, and fails on an unlisted one. `TestNoPrivateTerminalProbe` fails on a terminal probe outside `internal/tty`. Every gate and entry point has a test that fails when it stops consulting the gate, with an unset-`NO_COLOR` control run so the veto cannot pass on output that never colored. | Two private copies of the probe, `prune`'s and ttyproxy's, survived [the color audit](cli-color-audit.md)'s record that the probe was unified. |

Two defects surfaced on the way and are fixed by the same routing: `yolo capture` and the
auto-capture trigger `yolo run` wires both passed a literal `true` as their color, so both
wrote ANSI to a pipe as well as ignoring `NO_COLOR`.

> [!WARNING]
> **The terminal half is still missing for the text in decision 4.** A launch whose stderr
> is redirected still receives the colored provisioning and `⚡ Executing` lines unless
> `NO_COLOR` is set. Those lines are frozen bytes pinned by the golden above, so gating them
> on a terminal is a deliberate golden update needing sign-off, like the run sub-steps item
> in Group B.

## Semantic color convention (apply everywhere)

One grammar for the whole CLI, expressible entirely in the current 8-tag palette:

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

`internal/richtext` offers 8 tags: `bold`, `dim`, `red`, `green`, `yellow`,
`blue`, `magenta`, `cyan` (`ansiForTag`, `internal/richtext/richtext.go`) — 6 true hues plus 2
modifiers, no background/inverse. One audited surface still wants more than that:

1. **`config render --explain` — one hue per provenance layer. RESOLVED.**
   Option (a) shipped: richtext gained `magenta` (ANSI 35) + `blue` (ANSI 34) in
   6be7884, and `--explain` took the one-hue-per-layer route in 59568e4 —
   `colorLayer` (config.go) maps `defaults`→`[dim]`, `host`→`[blue]`,
   `workspace`→`[cyan]`, `overlay`→`[magenta]`, `managed`→`[green]`, so the
   closed layer set gets one distinct color each. (It was six layers and six
   hues when this was written; `transform`→`[yellow]` went with the Lua
   transform on 2026-09-11 — [`OQ-LT1`](../reference/pack-system.md#oq-lt1) — so the palette now has a
   hue to spare rather than a gap.)
2. **`check` badges use background/inverse video. (still open.)**
   `internal/cli/check/reporter.go` renders `[FAIL]` white-on-red and `[WARN]` black-on-yellow
   via its own ANSI constants — backgrounds richtext cannot express. This is only
   relevant *if* `check` is ever unified onto richtext; today check keeps its
   private, strictly-richer ANSI set (recommended). If unification is ever
   desired, richtext must gain background/inverse tags first, else the badges
   degrade to plain fg red/yellow.

**Richtext-extension task (background/inverse only, still open):** the
`magenta`/`blue` hues already landed (6be7884). Only if check is ever unified
would `ansiForTag` need background/inverse tags. Everything else in this plan
fits the existing 8 tags.

## The one structural change — `yolo --help` (DONE)

Unlike the other items (which just wrap existing tags around existing text),
`yolo --help` needed a **mechanism** change, now landed in 59568e4. `usageText()`
(`help.go`) emits rich tags (`[bold]` section headers, `[cyan]` command
names) and is rendered at the print site via
`richtext.Render(usageText(), isTTY(os.Stdout))` (cli.go, in the
`wantsTopLevelHelp` branch) — the plain path strips the tags so `help_test.go`'s
substring assertions keep passing.

- **Still plain:** the same mechanism cost applies to `config --help` /
  `configUsage` (`config.go`, a pure-plain string written via `io.WriteString`
  with no color path). `config.go` otherwise has a color path now (`colorForWriter`
  + a `richtext.Printer` in `renderSurface`); only the usage string is uncolored.

## Per-command checklist

Impact = eye-friendliness gain; Effort accounts for plumbing + golden risk.
Palette is sufficient for every item unless noted.

### Group A — RED: flat monochrome, needs richtext plumbing + coloring

These emit color-mappable status vocabulary but have no color path at all.
Highest value, low risk (text stays byte-identical after strip).

- [ ] **`yolo loopholes status`** (`Status` in `internal/loopholes/loopholescmd.go`) —
  **Impact: high · Effort: med.** *The single biggest missed opportunity.* Wire a
  `richtext.Printer` + color gate into `Deps` (package currently writes to a raw
  `io.Writer`, no TTY probe). Then color the existing bracket prefixes:
  `[ok]`→green, `[fail]`→red, `[inactive]`→yellow, `[disabled]`/`[no-check]`→dim
  (direct reuse of check's proven pass/fail vocabulary). Bold the loophole Name;
  dim `rc=%s` and the wrapped Output detail lines; cyan the suggested
  `yolo loopholes status` command in the in-jail short-circuit line.
- [ ] **`yolo loopholes list`** (`List` in `internal/loopholes/loopholescmd.go`) —
  **Impact: high · Effort: med** (same Deps plumbing as status). Color the status
  label green `active` / yellow `inactive (reason)` / dim `disabled`; bold the
  loophole Name so each row anchors; dim the `(source/transport/lifecycle)` tags
  and the `transport=`/`intercepts=` metadata; dim the description continuation; bold the `• bundled/user/workspace` empty-state bullet labels.
- [x] **`yolo ps`** (ps.go + runtime/display.go RenderPsTable) — **DONE
  (d71dba3), listed in the Status header.** A color gate + `richtext.Printer`
  are now wired into `psDeps` (ps.go: `richtext.Printer{W: deps.Out, Color:
  deps.Color}`), TTY-resolved via `colorForWriter(os.Stdout)`. The framing lines
  are colored — bold header row, `[dim]No running jails.[/dim]`, the
  `[yellow]⚠  %d problem jail(s):[/yellow]` block with `[red]` reason rows, the
  `[dim]Run 'yolo doctor' to clean up[/dim]` hint, and the `[red]Could not
  query…[/red]` runtime-error line. `TestPsColorParity` (ps_test.go) locks the
  additive-color/byte-parity contract.

### Group B — YELLOW: partial or plain, mostly additive

- [x] **`config render --explain` layer column** (`renderSurface` and
  `colorLayer` in `config.go`, plus `ProvenanceLines` in `agentcfg/compose.go`) —
  **DONE (59568e4).** `renderSurface` builds a `richtext.Printer{W: out, Color:
  color}`, cyans each key, and runs the LAYER token through `colorLayer`, which
  gives one hue per layer (palette gap #1 resolved via the extended palette). The
  output now scans like syntax highlighting — which keys `managed` clobbered vs
  came from `host`/`workspace`.
- [x] **`yolo --help`** (`usageText` in `help.go`, rendered at cli.go via
  `richtext.Render(usageText(), isTTY(os.Stdout))`) — **DONE (59568e4).**
  `usageText` emits `[bold]` headers (`Usage:`/`Commands:`), `[cyan]` on each
  command NAME (the scan target), the literal `yolo --`/subcommand usage
  tokens, and the trailing `yolo <subcommand> --help` pointer. The
  print site is TTY-gated so stripped output stays byte-identical.
- [ ] **`config --help` / `configUsage`** (`config.go`) —
  **Impact: med · Effort: med** (same config.go color path). Headers
  `Usage:`/`Subcommands:`/`render flags:`→bold; `render <agent>` token and each
  canonical surface identity (`pi/settings`) and flags (`--explain`,
  `--help, -h`)→cyan; file paths→cyan or dim.
  (Written when `configUsage` named the two `config.lua` files; both are gone
  with the Lua transform — [`OQ-LT1`](../reference/pack-system.md#oq-lt1) — so the paths left to
  color are whatever the help text names today.)
- [ ] **`yolo init` / `init-user-config`** (the status lines in `init.go`) —
  **Impact: med · Effort: low-med.** Color the scaffolder's own status lines to
  match the richly-styled briefing that follows: `Created …`→green,
  `already exists`→yellow, the two error paths→`[bold red]`. **Blocker:** init.go
  uses cli/markup.go's closed-set replacer (bold/cyan/green/yellow only — **no
  red**). Either add `red`/`dim` to `markupANSI`+`markupStrip`, or route init's
  status lines through `internal/richtext.Printer` (which has red). Flag this
  missing-tag gap.
- [ ] **`yolo run` progress sub-steps** (`setupScript` in `run/command.go`) —
  **Impact: med · Effort: med, FROZEN BYTES.** The `↳ mise install / mise
  upgrade / bootstrap` phase lines render as flat plain text against mise's own
  chatter; give them `[cyan]` or `[bold]` so each phase boundary reads as a
  heading. **Caveat:** testdata/final_cmd_bash.txt + command_test.go pin these
  exact bytes → deliberate golden update, human sign-off, NOT additive.
- [ ] **`yolo` startup banner** — **Impact: low-med · Effort: low.** Fully
  monochrome. It is now TWO lines from two renderers, so this item is two edits:
  `internal/banner`'s `Startup` (the `yolo-jail <version> | <platform> |
  host|in-jail` line every subcommand writes, emitted by
  `internal/cli.emitStartupBanner`) and `run/banner.go`'s `LaunchBanner` (the
  `Jail: <name> | <runtime>` line a launch adds, emitted by
  `run.emitLaunchBanner`). Render through `richtext.Render` **at each emit site**
  so both returned strings stay byte-identical for their tests: dim the whole
  thing (it's metadata), or cyan the runtime / bold the version so the
  pipe-delimited fields parse.

### Group C — GREEN: already good, optional polish

- [ ] **`yolo check` / doctor** — add scannable glyphs before badges
  (`[PASS]`→green ✓, `[FAIL]`→red ✗, `[WARN]`→yellow `!`); dim the ` -> workspace`
  tail of the running-jail rows (`check.go`) so the jail name pops; cyan the
  config/storage paths in `ok()` lines. Keep check's private ANSI set (it's
  richer than richtext — see palette gap #2). **Low effort, additive.**
- [ ] **`yolo host-daemon status`** (was `broker status`) — dim/cyan the `pid file:`/`socket:`
  PATH values (`internal/broker/brokercmd.go`) so label/value split is visible; add ✓/✗ glyphs before
  live/present/ok; align trailing marks into a fixed status column for vertical
  scanning. **Low effort, additive.**
- [ ] **`yolo prune` mode banner** (`internal/prune/prunecmd.go`) — color the header mode
  token so DRY-RUN vs APPLY is obvious at the top, not only in the far summary:
  `[bold yellow]yolo prune (DRY-RUN)` vs `[bold red/green]yolo prune (APPLY)`.
  Goldens pin `Color=false` → stripped bytes unchanged. **Low effort, additive.**
- [ ] **`config-ref`** — optional: `[green]` for value literals
  (Values:/Default: lines) and `[dim]` for annotation lines (Override:/
  Auto-detect:) to add a third scan tier. Already GREEN; polish only.

## Cross-cutting

- [ ] **Colorize resource PATHS consistently** across the state commands —
  dim or cyan the path portion of `label: /some/path` lines so the label/value
  boundary is visible (host-daemon pid-file/socket, check storage/config paths,
  config-ref file paths; the builder conf paths went with `yolo builder`).
- [ ] **Consolidate tag→ANSI renderers.** Three-plus parallel tables exist:
  richtext's `ansiForTag`, configref.go's `tagReplacer`, cli/markup.go's
  `markupANSI`. `richtext.ansiForTag` already covers the full palette. Route the
  plain surfaces onto `internal/richtext` rather than growing a fourth table
  (also unblocks init's missing-red gap). Natural intersection with
  `module-consolidation-and-cleanup.md` (retired; `git log -- docs/plans/module-consolidation-and-cleanup.md`).

## Enforcement / tests

The byte-parity contract is already the enforcement mechanism: golden and
substring tests assert on the **ANSI-stripped** text, so additive coloring can't
regress them. For each newly-colored command add (mirroring the existing
`internal/cli/run` console tests): markup → ANSI escapes when `color && TTY`, and
plain text (no escapes, no literal `[bold]`) when not. For the frozen-byte
surfaces (run boot lines, banner, macos dry-run plan) any change is a deliberate
golden update — call it out for human sign-off, don't fold it in silently.

## Suggested order

1. ~~Richtext palette extension (magenta/blue)~~ **SHIPPED (6be7884)** — and
   `--explain` took the one-hue-per-layer route (59568e4).
2. Group A (loopholes status → loopholes list → ps): highest value, self-
   contained plumbing, no goldens. (`ps` done — d71dba3.)
3. ~~`--help`~~ + config surfaces (the structural renderer-routing change), then
   ~~`--explain`~~ — `--help` and `--explain` both landed (59568e4); the
   `config --help`/`configUsage` string remains plain.
4. init status lines (resolve the markup.go red gap or route to richtext).
5. Group C polish + cross-cutting path/glyph passes.
6. Frozen-byte surfaces (run sub-steps, banner) last, each with human sign-off.
