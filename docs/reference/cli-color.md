---
status: current
verified: 2026-09-27
verified_commit: f937d0fd
covers:
  - internal/richtext/
  - internal/tty/
tags: [cli, ux, color, ansi, no-color, tty]
summary: "How a yolo command decides whether its output carries ANSI color. Messages are written once in rich markup and a shared renderer either turns the markup into ANSI escapes or strips it. Whether it renders is decided by one gate, which needs the command to have asked for color, the stream to be a real terminal according to one ioctl probe, and NO_COLOR to be unset or empty."
---

# Terminal color — rich markup, one gate, one probe

**Status:** CURRENT as of 2026-09-27, verified against `f937d0fd`.

Every human-facing yolo command writes its messages once, in **rich markup**, and hands them to
one shared renderer. The renderer turns the markup into ANSI escapes or deletes it, depending on a
single yes/no answer the caller already resolved. That answer comes from one **color gate**. The
gate says yes only when three things hold: the command asked for color, the stream it writes to is
a real terminal, and the [`NO_COLOR` convention](https://no-color.org) has not vetoed it. Whether a
stream is a terminal is answered by one **terminal probe**, an ioctl, and never by a file-mode
check.

| Component | Lives in |
| :--- | :--- |
| Rich-markup renderer | `internal/richtext` (`ToANSI`, `Strip`, `Render`, `Printer`) |
| Terminal probe | `internal/tty` (`IsTerminal`, `IsTerminalFile`; the syscall is platform-split in `isattyFD`) |
| Color gate and the one `NO_COLOR` reader | `internal/tty` (`Color`, `NoColor`, `NoColorVar`) |
| Package `cli`'s one color decision | `internal/cli` (`colorForWriter`, with `fileIsTerminal` as its probe seam) |
| Gate sites that take an injectable terminal seam | `internal/prune`, `internal/broker`, `internal/cli/stores`, `internal/cli/check`, `internal/cli/run` (`Options.pr`) |
| Text another process prints | `internal/cli/run` (`scriptColor`), `internal/provision` (`Script`'s color parameter), `internal/entrypoint` (`executingLine`), `internal/macosuser` (the provisioning stage's `scriptColor`) |
| Enforcement | `internal/tty` (`TestEveryColorEmitterIsInventoried`, `TestNoPrivateTerminalProbe`), `internal/cli` (`TestEveryCommandHonorsNoColor`, `TestEveryConfigVerbTakesTheOneColorDecision`), `internal/cli/check` (`TestColorGatedOnTTY`, `TestColorHonorsNoColor`, `TestColorStripsToPlain`) |

**Reads with:** [`cli-visual-polish.md`](../plans/cli-visual-polish.md) (what the color is spent
on: the palette, the glyphs and each command's styling, plus
[its `NO_COLOR` implementation decisions](../plans/cli-visual-polish.md#implementation-decisions--no_color-2026-09-26)),
[`self-documenting-cli.md`](self-documenting-cli.md#7-state-reporting-surfaces-offer---format-json)
(the ANSI-free JSON output), [`report-tiers.md`](report-tiers.md) (which lines a report prints at
all).

---

## Terms

- **Rich markup** — bracketed style tags written into a message, such as `[bold red]ERR[/bold red]`
  or `[dim]…[/dim]`. The syntax is the console markup of the Python
  [Rich](https://rich.readthedocs.io/en/stable/markup.html) library, which yolo's Python
  implementation printed through; the Go renderer keeps the syntax for a closed set of style
  words. A bracketed token that is not made only of those words (`[y/N]`, `[path]`, `[rR]`) is
  **not** markup and is printed verbatim in both modes.
- **Terminal probe** — the question "is this file descriptor a real terminal?", asked by requesting
  the descriptor's terminal attributes from the kernel. The request succeeds only on a terminal. It
  is not a check that the file is a character device: `/dev/null` is one, and so is what a
  container's `-t` flag hands a process, and neither is a terminal.
- **Color gate** — the predicate that decides whether one stream receives ANSI escapes. The term
  was coined in [`cli-visual-polish.md`](../plans/cli-visual-polish.md#the-invariant--color-is-additive).
  It is not the decision to be interactive (to prompt, or to pass a container `-t`), which asks the
  terminal probe directly and ignores `NO_COLOR`.
- **The `NO_COLOR` convention** — the cross-tool rule at [no-color.org](https://no-color.org): when
  `NO_COLOR` is set to any non-empty value, a program that adds color by default must not. An empty
  value counts as unset. Any non-empty value counts, `0` and `false` included.

## Invariants

1. **Markup is authored once, and the renderer decides.** A message exists in one form, with its
   style tags. The renderer turns them into ANSI or strips them according to a color flag the
   caller resolved. No command keeps a plain copy and a colored copy of the same line. The two
   halves never mix: `internal/richtext` never probes a terminal, and `internal/tty` never renders
   markup.
2. **The stripped bytes are the contract.** Stripping the markup yields exactly the text a
   `Color=false` run prints, and the goldens pin that `Color=false` output. So color is additive: a
   command gains color by wrapping text it already prints in tags, and no golden moves.
   `TestColorStripsToPlain` in `internal/cli/check` asserts that `yolo check`'s colored output,
   stripped, equals its `Color=false` output byte for byte, and `TestPsColorParity` in
   `internal/cli` pins `yolo ps`'s `Color=false` bytes exactly.
3. **There is one gate: `tty.Color(getenv, requested, terminal)`.** It is on only when `requested`
   is true, `terminal` is true, and `tty.NoColor(getenv)` is false. `NoColor` is the only place
   yolo decides anything from `NO_COLOR` in its own environment, and it treats an empty value as
   unset. (The launcher also reads the value, but only to copy it into the jail once `NoColor` has
   said it is set; see [crossing into the jail](#no_color-crossing-into-the-jail).) `getenv` is the
   environment the command already reads its other inputs from: the injected `Options.Getenv` for
   the run pipeline and `check`, and the process environment (`nil`) everywhere else. So one
   invocation reads one environment, and a test's fake environment decides color the way it decides
   everything else.
4. **There is one probe: the `TCGETS`/`TIOCGETA` ioctl behind `tty.IsTerminal`.** No non-test Go
   file under `cmd/` or `internal/` outside `internal/tty` asks the kernel for terminal attributes
   only to test the error, and none tests
   `os.ModeCharDevice`, which reports true for `/dev/null` and for a container's `-t`. The one
   exception is the file listed in `charDeviceReaders` in `internal/tty`'s inventory test, with the
   reason it asks a different question and decides no color.
5. **No ANSI goes to a pipe or a redirect from a stream yolo decides for.** A stream a yolo process
   writes itself gets color only through the gate, so its terminal half is always asked. The
   exception is text that another process prints (below), which takes only the `NO_COLOR` half.
6. **`NO_COLOR` changes color and nothing else.** It never changes whether yolo is interactive: the
   run's `-t` flag and every prompt ask the terminal probe (`isTTYStdout` in `internal/cli`). It
   never touches JSON output, which is encoded data rather than rendered markup and so is colorless
   whatever the gate says. And it never suppresses the terminal reset `ttyproxy` writes after a child
   exits (`termReset`), because that reset clears attributes rather than adding any.

## How a command reaches the gate

There are three shapes, and each one ends at `tty.Color` or at its `NoColor` half.

- **Package `cli`** makes one decision, `colorForWriter`: color only when the writer is an
  `*os.File` that the probe calls a terminal, and `NO_COLOR` is unset or empty. A `bytes.Buffer`
  (every test) and a pipe both get plain text. Every entry point that writes to stdout asks
  `colorForWriter(os.Stdout)`. `yolo config` asks it once, in its dispatch, and hands the answer
  to every verb in `configColorVerbs`. The macos-user commands receive the same answer through
  `macosuser.RealDeps`, and `yolo loopholes` through `loopholes.Deps.Color`, which its dispatch
  sets.
- **Command packages with their own terminal seam** (`prune`, the host daemons in
  `internal/broker`, `yolo stores`, `check`, and the run pipeline) call `tty.Color` themselves,
  passing their requested color and their seam's answer (`IsTTYStdout`, in most of them). The seam
  defaults to the real probe on `os.Stdout`; a test replaces it, so the tests can drive the
  terminal case and the pipe case without a real terminal.
- **Text another process prints** takes the `NoColor` half alone, because the process deciding
  cannot probe the stream the text will reach. That covers the three lines the generated container
  script prints (the provisioning line, the provisioning failure and the `⚡ Executing` hand-over),
  the macos-user provisioning stage's failure line, and the entrypoint's `⚡ Executing` line when it
  execs into an existing jail. The jail's `.bashrc` makes the same test in bash when the shell
  starts, for its prompt colors and its `ls --color=auto` alias.

Some color never passes through an ANSI literal in yolo's own source: the red kitty tab and tmux
pane border that `SetupJailIndicator` sets around a launch are drawn by kitty or tmux. They still
ask `tty.NoColor`, and under `NO_COLOR` the indicator keeps its words and drops its red.

### `NO_COLOR` crossing into the jail

`NO_COLOR` crosses into the jail beside `TERM` and `COLORTERM`, and only when it is non-empty, so the
in-jail `yolo`, the entrypoint and the prompt read the same answer the host did. On a container
launch it is an `-e` pair (`noColorEnvArgs` in `internal/cli/run`). An attach carries it the same
way, and when the attaching `yolo` has none but the running container was launched with one, the
attach passes an empty value to clear it for that session (`attachNoColorEnvArgs`). On macos-user it
goes into the session environment (`MacosSandboxEnv`).

## Enforcement

The gate is held by tests that walk the tree or drive the real call sites, not by review.

- **`TestEveryColorEmitterIsInventoried`** (in `internal/tty`) lists every non-test Go file under
  `cmd/` and `internal/` whose string literals hold an ANSI escape, each with the gate that governs
  it (`colorEmitters`). It fails on a file that starts emitting without being listed, and on a
  listed file that stopped. A new entry is a new place yolo can add color, and adding it is the
  moment to route it through `Color`, or through `NoColor` for text another process prints.
- **`TestNoPrivateTerminalProbe`** (in `internal/tty`) parses every non-test file outside
  `internal/tty` and fails on a termios request whose value is discarded (the probe shape) and on
  any `os.ModeCharDevice` test not listed in `charDeviceReaders`. Reading termios for its value, as
  `ttyproxy` does to set raw mode, is not a probe and passes.
- **`TestEveryCommandHonorsNoColor`** (in `internal/cli`) runs each entry point in
  `noColorEntryPoints` with a terminal stood in for stdout, once with `NO_COLOR` empty (unset, by
  the convention), where it must color, and once with `NO_COLOR=1`, where it must not. The unset run is the control: without it, a
  command that never colored would pass the veto. `TestEveryConfigVerbTakesTheOneColorDecision`
  does the same for the `yolo config` dispatch.
- **`TestColorGatedOnTTY`** and **`TestColorHonorsNoColor`** (in `internal/cli/check`) drive
  `yolo check` with color requested and a non-terminal stdout, and with a terminal and `NO_COLOR`
  set. Neither may emit an escape.
- The probe's own tests (in `internal/tty`) drive a nil file, both ends of a pipe and `/dev/null`,
  all of which must read as "not a terminal", and the gate's truth table.

> [!WARNING]
> **A printer that takes a color flag and strips the markup anyway is the original bug class.**
> Its `Color=false` goldens pass and its output is correct everywhere except on a terminal, where
> it is silently colorless. A test of a new printer has to assert that the colored path emits an
> escape, not only that the plain path is clean.

> [!WARNING]
> **Private copies of the probe survive unification.** The first time the probe was unified onto
> `internal/tty`, `prune` and `ttyproxy` each kept a private ioctl copy, and the record said the
> unification was complete. `TestNoPrivateTerminalProbe` exists because of that. Call
> `tty.IsTerminal` or `tty.IsTerminalFile`; do not reintroduce a termios request tested for its
> error, or a `os.ModeCharDevice` check.

> [!WARNING]
> **A call site that passes a literal `true` as its color defeats both halves of the gate.**
> `yolo capture` and the auto-capture trigger `yolo run` wires both did, so both wrote ANSI to a
> pipe and ignored `NO_COLOR`. `TestEveryCommandHonorsNoColor` catches that shape, but only for the
> entry points listed in `noColorEntryPoints`. A new entry point that colors has to be added there.
> The inventory does not catch it, because a literal `true` handed to `richtext.Printer` puts no
> escape in the calling file.

> [!WARNING]
> **Two surfaces are exceptions to additive color.** The run's boot lines, the generated container
> script's provisioning and `⚡ Executing` lines, are pinned byte for byte by
> `internal/cli/run/testdata/final_cmd_bash.txt`, and they carry color when `NO_COLOR` is unset even
> when stderr is redirected: that is the terminal-half gap recorded in
> [`cli-visual-polish.md`](../plans/cli-visual-polish.md#implementation-decisions--no_color-2026-09-26).
> Change them only as a deliberate golden update a maintainer signs off. The macos-user dry-run plan
> renders with color forced off on every path (`PrintPlan`, and the dry-run branch of
> `RunMacosUser`, which clears `Deps.Color` for the plan build). CI and a Mac agent inspect a launch
> through that plan, and no golden pins its bytes, so the forced-off printer is the whole guard: do
> not route the plan through the gate as a side effect of adding color.

## What is measured

The tests drive the pipe, `/dev/null` and `NO_COLOR` cases through the probe, the gate and the real
entry points, with a terminal stood in where one is needed. One observation on a real terminal is
recorded: on 2026-07-22, in a nested jail, a pty run of `yolo check` emitted ANSI while a piped run
emitted none, recorded by the commit that gated `check` on the terminal (`c9ea5e85`). Nothing else
is recorded as observed on a terminal.

## What this does not cover

- **The palette, the glyphs and each command's styling.** Which hue means what, which symbols a
  report uses, and which commands are styled belong to
  [`cli-visual-polish.md`](../plans/cli-visual-polish.md#semantic-color-convention-apply-everywhere).
  This doc covers only how markup becomes ANSI and when.
- **JSON output.** It is ANSI-free by
  [`self-documenting-cli.md`](self-documenting-cli.md#7-state-reporting-surfaces-offer---format-json),
  and the gate never has to reach it.
- **`check`'s private palette.** `internal/cli/check` writes its report styles, including its
  background-colored badges, from its own SGR constants, because the markup has no background
  colors. Its color flag still comes
  from the gate.

## Current values

Verified at `f937d0fd`. The prose above explains what each of these is for; this table is the only
place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| `NO_COLOR` variable name | `NO_COLOR` | `tty.NoColorVar` |
| Terminal probe on Linux | `ioctl(TCGETS)` through `unix.IoctlGetTermios` | `internal/tty` (`tty_linux.go`) |
| Terminal probe elsewhere (darwin, BSD) | `ioctl(TIOCGETA)` through `unix.IoctlGetTermios` | `internal/tty` (`tty_other.go`) |
| Style words the renderer knows | the keys of `ansiForTag` | `internal/richtext` |
| Files allowed to test `os.ModeCharDevice` | the keys of `charDeviceReaders` | `internal/tty` (`inventory_test.go`) |
| Files allowed to hold an ANSI literal | the keys of `colorEmitters` | `internal/tty` (`inventory_test.go`) |
