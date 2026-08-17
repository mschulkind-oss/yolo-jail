# Principle: the CLI is its own manual

**Audience:** anyone adding a `yolo` subcommand, a flag, a state-reporting
surface, or a new subsystem an agent has to operate (loopholes, builders,
composed config, …). Read this before shipping a command, and before assuming a
design doc or `AGENTS.md` note is where an operator will look.

## The principle

An agent must be able to discover and operate **every** part of yolo purely by
interrogating the CLI — no reading source, no reading design docs, no host
tooling. `yolo --help` names every command; every command answers `--help` with
its own synopsis, flags, arguments, effects, and an example; every cross-cutting
concept is reachable from a `config-ref` section or a `yolo help <topic>`; and
anything that reports state can emit machine-readable output on request. The CLI
*is* the API, and it *is* the manual.

> If an agent has to `rg` the source or open `docs/` to learn how to drive a
> subsystem, the CLI has failed. Fix the CLI, not the docs.

## Why this matters here specifically

Three properties of yolo make this non-negotiable rather than nice-to-have:

1. **Agents are the primary operators.** The whole product exists to be driven
   by `claude`/`copilot`/`gemini`/`pi`/etc. Agents discover capabilities by
   trying `--help` and parsing output — they do not browse a docs site. A
   command that only reveals itself in source is, to the actual operator,
   invisible.
2. **The jail is credential- and identity-isolated.** An in-jail agent cannot
   lean on the host's shell history, the maintainer's memory, or host-side
   tooling to fill gaps. What the CLI tells it is *all* it has. The briefing
   already leans on this — it points at `yolo --help` and `yolo config-ref` as
   the entry points (`internal/jailcontent/briefing.go`, `BriefingContent`) precisely because those
   are the only surfaces guaranteed to be present.
3. **Docs drift; the CLI can be tested against itself.** A checked-in doc and
   the code diverge silently. Help text and JSON output that are generated from
   the same registry the dispatcher uses can be asserted in `go test` (we
   already do this for the command list — `TestUsageListedCommandsAreRegistered`).
   Self-documentation that lives *in* the binary is the only kind we can keep
   honest mechanically.

## The standard

Every **command** MUST satisfy 1–5; every **concept-surface** MUST satisfy 6;
programmatic surfaces MUST satisfy 7. Each item is marked with its state
**today**, so this section doubles as the current-state audit. Evidence is
`file:line` or a verified live probe.

### 1. Every command answers `--help` / `-h` / `help <sub>` — on demand, exit 0

Help is a *request*, not an error: it prints full help to **stdout** and exits
**0**, and it never triggers the command's real work.

- **State: MET (2026-08-17).** Every key in the dispatch registry answers
  `--help`/`-h`/`help` on stdout with exit 0 and no side effect. `run` answers
  through `runHelp`/`runHelpRequested` (`internal/cli/runcmd.go`), whose scan is
  deliberately stricter so `yolo -- claude --help` still reaches the inner
  command; `config`/`pack`/`apply`/`describe`/`check-deps` answer inside their own
  testable bodies via `isHelpToken`; the remaining twelve keys answer via
  `answerHelp` at the top of each handler (`internal/cli/subhelp.go`).
  `dispatchNative` is deliberately NOT a central interception point — see that
  file's header for why the `run` invariant forbids it.

  **What it was:** worse than a missing feature. `yolo init --help` scaffolded a
  `yolo-jail.jsonc` and appended to `.gitignore` in the cwd, and
  `yolo init-user-config --help` wrote `~/.config/yolo-jail/config.jsonc` — TWO
  commands mutated state during what an operator intends as a read-only
  interrogation, not the one this doc recorded. `check --help` ran a full check
  including a nix build, `prune --help` walked the disk, and `broker`/`loopholes`
  printed a one-line usage to stderr with exit 1 because `--help` landed on their
  misuse branch.

  **Kept honest by** `TestEveryRegisteredCommandAnswersHelp`
  (`internal/cli/subhelp_test.go`): it walks `registry` rather than a list, and
  each probe runs with cwd and `$HOME` pointed at empty temp trees that must still
  be empty afterwards — so the `init` regression is caught as a *write*, not only
  as wrong output. Enforcement item 1 below is therefore built.

### 2. Help lists synopsis, flags, positional args, effects, and ≥1 example

- **State: PARTIAL.** Synopsis, flags, positional args and effects are now
  present for every command: `prune`'s twelve flags, `run`'s
  `--new`/`--profile`/`--network`/`--dry-run`, `init`'s `--mount`/`-m`,
  `broker logs`'s `-n`/`--lines`/`-f`/`--follow`, and the positionals of
  `macos-unshare` (workspace) and `macos-fix-permissions` (path) are all in their
  command's usage text. Flag coverage is *derived*, not maintained by hand:
  `TestUsageListsEveryParsedFlag` parses this package's own source, reads the
  name→handler mapping out of the `registry` composite literal, and requires every
  `--flag` string literal in a handler's body to appear in that command's help. It
  passes vacuously for a handler that delegates its parsing (`runApply` →
  `applyMain`), which is the one gap left in the mechanism.

  What remains genuinely unmet is **examples**: most usage texts point onward
  (`yolo config-ref`, a sibling command) but only `run` and `pack` show a literal
  invocation to copy. The bar says ≥1 example per command.

### 3. Misuse exits non-zero with usage on stderr — and is distinct from help

- **State: PARTIAL.** Unknown top-level command is handled correctly: stderr +
  exit 1 (`internal/cli/cli.go:63,70`). The *conflation* half is fixed —
  `broker`/`loopholes` no longer answer help from their misuse branch, and the
  branches now differ in every respect the rule names (help → stdout/0, misuse →
  stderr/non-zero). What remains is the other half: the flag-parsing commands
  (`run`/`check`/`prune`/`init`/`ps`) still treat an unknown flag as ignorable
  and return their normal (often 0) code, so a typo'd flag is silently dropped
  and misuse is not machine-detectable. `config`/`pack`/`apply`/`describe`/
  `check-deps` already exit 2 on an unexpected argument — that is the shape the
  rest should take.

### 4. Top-level help lists every registered command

- **State: PARTIAL.** `commandHelp` (`internal/cli/help.go:10`) lists 11
  curated commands with blurbs, and `TestUsageListedCommandsAreRegistered`
  keeps that list in sync with the dispatch registry — genuinely good. But
  three *registered* commands are absent from it: `macos-teardown`,
  `macos-unshare`, `macos-fix-permissions` (`internal/cli/dispatch.go:28-30`),
  so they are invisible to `yolo --help`. The sync test only guards
  help→registry, not registry→help. (The hidden `internal` namespace is
  *intentionally* absent — daemon plumbing, not an operator surface — and is not
  a gap.)

### 5. The discovery chain is closed: top help → each command's help → onward

The footer of `yolo --help` and each command's help must point onward (related
commands, `config-ref`, concept help), so an agent can walk from the top to any
leaf.

- **State: PARTIAL.** The top of the chain is solid: the briefing opens with
  `Jail tooling: yolo --help; config reference: yolo config-ref`
  (`agentsmd.go:112`) and the `--help` footer names `config-ref`. That footer's
  hedge — "Run `yolo <subcommand> --help` **where supported**" — is gone, because
  the promise is now true everywhere (item 1); softening it was never the fix, and
  the fix was to make it honest. Each command's help also points onward now (a
  sibling command, `yolo config-ref`, or `yolo pack --help`).

  Still PARTIAL for one reason: `yolo --help` does not list `macos-teardown`,
  `macos-unshare` or `macos-fix-permissions` (item 4), so three commands answer a
  `--help` nobody can discover they have. The loopholes branch *is* closed
  end-to-end (`agentsmd.go:148` → `yolo loopholes list`), and `config-ref` is
  reachable and real — those are the model.

### 6. Concepts are reachable from the CLI (config-ref section or `yolo help <topic>`)

Cross-cutting concepts (backends, mounts/overlays, cgroup model, the composed-
config pipeline) must be operable from the CLI, not only from `docs/`.

- **State: PARTIAL.** `yolo config-ref` is a genuine, thorough concept surface
  for the config schema (`internal/cli/config_ref.txt`, 673 lines) and is well
  cross-referenced inbound. But it is a terminal leaf (it never points onward to
  `docs/design/*`), it has real key drift (it omits the accepted top-level keys
  `repo_path`, `host_processes`, and `prune`, and titles the loopholes section
  with the stale name `host_services`), and — most important for this doc — it
  is **completely silent on the composed-config / Lua pipeline** (zero hits in
  `config_ref.txt` for `lua`, `transform`, or `compose`; the words `overlay`
  and `managed` do appear, but only in unrelated contexts — MCP per-workspace
  overlays and "yolo-managed section" / version-managed runtimes — never the
  composition layer stack). There is no `yolo help <topic>` mechanism at all
  for backends/mounts/cgroups. See the dedicated section below.

### 7. State-reporting surfaces offer `--format json` (or `-o json`)

Anything that reports state — `ps`, `check`, `loopholes list`, `prune` plan,
`broker status`, `--version` — must emit stable, ANSI-free, machine-readable
output on request.

- **State: UNMET.** No command anywhere accepts `--format json`/`--json` as an
  *output* flag. Every reporting command emits ANSI-decorated prose only. There
  is no `json.Marshal` in `internal/cli` at all, and the ~12 `--format`
  occurrences there are all `podman`/container Go-template args (e.g.
  `check/sections_misc.go:181`), never a yolo output flag. An agent cannot
  machine-read jail state, check results, or loophole status — it must scrape
  formatted text.

### Items we already meet or exceed the bar on

- **Dry-run / preview discipline: MET (exceeds many CLIs).** `run --dry-run`
  renders a plan without launching (`commands.go:315`); `prune` is dry-run *by
  default* and requires `--apply` to mutate (`commands.go:63`). This is the
  destructive-safe default that best-in-class tools use.
- **`--version`: MET.** Handled at the top level (`cli.go:36`) with a clean,
  stable version line.
- **A curated, tested top-level command list: MET** (item 4's PARTIAL is only
  the three unlisted macos commands and the missing registry→help direction).

### Explicit stretch goals (SHOULD, not MUST — do not block the bar on these)

- **Shell completions** (`yolo completion <bash|zsh|fish>`). UNMET; must be
  hand-authored since yolo is a hand-rolled dispatcher, not cobra.
- **Man pages.** UNMET; lower priority for an agent-facing CLI.
- **Effective merged-config dump with provenance** (`yolo config` showing which
  layer each value came from, à la `git config --show-origin`). Overlaps the
  composed-config work below; the hidden `yolo internal config-dump` exists but
  is not an operator surface.

**Scorecard (2026-08-17):** MET 4 (per-command `--help`, dry-run, `--version`,
tested top-level list), PARTIAL 5 (help content — examples only, misuse/exit
codes, top-level completeness, discovery-chain closure, concept reachability),
UNMET 2 (`--format json`, completions/man as stretch). Item 1 moved UNMET→MET and
took the P0 traps with it; `--format json` (item 7) plus the composed-config gap
below are now the substance of the action list.

## The composed-config / Lua / pi surface (the focus)

This is the sharpest instance of the principle, because the entire mechanism is
invisible to the CLI today. pi (and every agent) will have its settings
*regenerated each boot* from a layer stack, reshaped by a user/workspace Lua
transform. The design is `docs/plans/agent-settings-composition.md` §3/§6/§6.5;
the pure engine largely exists (`internal/agentcfg`, with `gopher-lua`
vendored). But **the engine is an orphan**: it has no exported pipeline
entrypoint (`grep '^func [A-Z]' internal/agentcfg/engine.go` → none), nothing
under `cmd/` or `internal/entrypoint/` imports it (`rg -l agentcfg cmd/
internal/entrypoint/` → none), and there is no `config`/`render` key in the
dispatch registry (`internal/cli/dispatch.go`). So an agent cannot reach any of
it by interrogation.

For an agent to get pi's Lua config going by the CLI alone, this exact surface
must exist:

1. **A `config` command group** in the registry (`internal/cli/dispatch.go`)
   and in `commandHelp` (`internal/cli/help.go`), so it appears in
   `yolo --help`. UNBUILT.
2. **`yolo config render <agent>`** — runs the pipeline (stage → merge → Lua
   transform → enforce → encode), prints what it *would* write, touches no live
   config. Runs host-side *and* in-jail (§6). This needs an **exported** engine
   entrypoint (`render()` — which implements the left-to-right layer fold — and
   `mergeDiff()` are both unexported today) plus shipped manifest data for
   pi/claude (only test fixtures exist). UNBUILT.
3. **`yolo config render pi --explain [KEYPATH]`** — shows which layer/hook won
   each leaf, *including dropped host keys* (§6.5: `host -> [git-helper,
   permission-gate]`, then the transform dropped `permission-gate`). The engine
   folds layers but retains no per-leaf provenance today, so provenance tracking
   must be added. UNBUILT.
4. **`config.lua` placement + the `ctx` API documented in the CLI** — via a
   `config-ref` section or `yolo help config-composition`: the two auto-loaded
   paths (`yolo-jail.config.lua` at repo root, `~/.config/yolo-jail/config.lua`
   — §3.4), the layer model (`defaults < host < workspace < runtime-overlay <
   managed`), the surface list, and the `ctx` contract (`ctx.config` /
   `ctx.managed` / `ctx.stage` / `ctx.agent` / `ctx.surface` — §3.2,
   agent-settings-composition.md:129-133). `config-ref` has zero of this today.
   UNBUILT.
5. **Lua errors surfaced with file/line via the CLI.** The engine already
   produces good errors (`luahook/vm.go` `wrapLuaErr`; `sandbox.go`
   `ValidateSandbox` lints statically) — but they are only reachable by running
   the VM, which no command does. A CLI path (render/check) must execute the VM
   so the error reaches the operator. ENGINE-READY, CLI-UNREACHABLE.
6. **`yolo config overlay --reset <agent>`** — the escape hatch for the
   capture-diff overlay (§5/§9). The overlay exists in the engine but is not
   user-operable. UNBUILT.

### The interrogation sequence an agent SHOULD be able to run

```bash
yolo --help                                  # (a) see that a `config` group exists
yolo config --help                           # (b) see render/overlay + what they do
yolo help config-composition                 # (c) learn config.lua paths, layers, ctx API
yolo config render pi                         # (d) preview pi's composed settings, no writes
yolo config render pi --explain extensions    # (e) see why permission-gate was dropped
yolo config render pi --format json           # (f) machine-read the result
# ...edit ~/.config/yolo-jail/config.lua...
yolo config render pi --explain extensions    # (g) confirm the transform; file/line on error
yolo config overlay --reset pi                # (h) discard an in-jail capture if needed
```

**Every step (a)–(h) fails today** — steps (a)–(d),(f),(h) because the command
does not exist, (c) because no concept surface documents the pipeline, (e),(g)
because provenance/`--explain` is unimplemented even though the fold and the Lua
VM run. The engine is real; the CLI reachability is entirely absent. That gap —
"the mechanism exists in code and docs but not in the CLI an agent actually
drives" — is exactly what this principle exists to close.

## Enforcement

The standard stays true only if it is machine-checked. yolo is a hand-rolled
`registry` map (not cobra), so nothing comes for free; the intended shape is a
tiny shared help/flag-spec struct per command (`name`, `synopsis`, `flags[]`,
`examples[]`, plus a `Render()` and a `--json` hook) reused by every handler.
That makes the following tests possible; **do not implement them here** —
specify them in the owning backlog items.

1. **Every registered command has a `--help` handler that exits 0 to stdout and
   runs no side effect.** BUILT 2026-08-17 as
   `TestEveryRegisteredCommandAnswersHelp` (`internal/cli/subhelp_test.go`): it
   iterates the dispatch registry, dispatches `["<sub>", "--help"]` (and `-h`)
   with stdout/stderr captured, and asserts exit 0, stdout equal to the command's
   registered usage, empty stderr, and no ANSI.

   The no-real-work clause took a different shape than the injected-sink one
   proposed here, and a stronger one: each probe runs with cwd and `$HOME`
   pointed at fresh empty temp trees which must still be empty afterwards. An
   injected sink only catches work that goes through the seam you injected;
   emptiness catches ANY write, which is what `init --help` (cwd) and
   `init-user-config --help` ($HOME) each did through no seam at all. A 10s
   per-probe deadline bounds the other failure mode — a regressed `check --help`
   would otherwise run a nix build inside `just test-fast`.
2. **Registry ↔ help are bidirectionally in sync.** Today the test guards
   help→registry; add the reverse: every key in `registry` (minus an explicit
   hidden-set for `doctor` alias and any deliberately private commands) appears
   in `commandHelp`. This catches the three unlisted macos commands.
3. **`config-ref` covers every config key.** Assert every key in
   `knownTopLevelConfigKeys` (`internal/config/config.go:46-52`) — and known
   sub-key sets — appears literally in `config_ref.txt`, and that no
   documented top-level key is absent from the known set (catches drift both
   ways). This is the highest-leverage single test given `config-ref` is
   hand-maintained; it fails today on `repo_path`, `host_processes`, `prune`.
4. **Every state-reporting command accepts `--format json` and emits valid,
   ANSI-free JSON.** For the subset flagged in item 7, invoke with
   `--format json` and assert the output parses as JSON and contains no ANSI
   escape bytes.
5. **`yolo config render` fixture vectors** (once built): `inputs → render`
   byte-checked in `go test`, as §6 anticipates, so the composed output is
   pinned and the Lua/provenance path is exercised without a container.

## Prioritized action list

Sequenced so this doc coordinates work without duplicating the plans it points
at. Composed-config items belong to `docs/plans/agent-settings-composition.md`;
generic CLI items get their own backlog item (proposed:
`docs/plans/self-documenting-cli.md`, not yet created).

**P0 — active traps. DONE 2026-08-17; kept here for the correction they carry.**

1. ~~Universal `--help`/`-h`/`help <sub>` interception in `dispatchNative`
   (before the handler runs)~~ — **built, but NOT in `dispatchNative`.** Central
   interception is unimplementable without breaking the invariant it would sit
   next to: `yolo -- claude --help` must deliver `--help` to the inner command,
   which is why `wantsTopLevelHelp` counts only the first token and why
   `runHelpRequested` mirrors run's own parse. A central hook would have to carry
   a per-command predicate anyway, so the fix is one shared predicate + a
   registry of usage texts (`internal/cli/subhelp.go`) called at the top of each
   handler. Every trap this item named is closed, plus one it missed —
   `init-user-config --help` wrote `~/.config/yolo-jail/config.jsonc`.
2. ~~Until P0.1 lands, soften the footer~~ — overtaken: the footer's "where
   supported" hedge was DELETED rather than softened, because the promise it
   hedged is now true for every registered command.

**P1 — close the standard for existing commands.**

4. Per-command help content: synopsis, flags, args and effects are DONE (with
   `TestUsageListsEveryParsedFlag` deriving flag coverage from the handlers'
   source). Remaining: ≥1 copyable example per command — only `run` and `pack`
   have one. → generic CLI backlog (item 2).
5. Add the three unlisted macos commands to `commandHelp`; add the
   registry→help reverse-sync test. → generic CLI backlog (items 4, enforcement 2).
6. Normalize exit-code/usage semantics: help→0/stdout, misuse→non-zero/stderr,
   consistently. → generic CLI backlog (item 3).
7. `--format json` on `ps`, `check`, `loopholes list`, `prune`, `broker
   status`. → generic CLI backlog (item 7, enforcement 4).

**P1 — the composed-config surface (all → `agent-settings-composition.md`).**

8. Fix `config-ref` drift *now* (cheap, independent of the pipeline): document
   `repo_path`/`host_processes`/`prune`, retitle `host_services`→`loopholes`,
   add onward `see docs/design/...` pointers, and add the coverage test
   (enforcement 3).
9. Build the `config` command group + `yolo config render <agent>`: export an
   engine pipeline entrypoint in `internal/agentcfg`, ship pi/claude manifest
   data, and wire the decode/stage front-end that `internal/entrypoint` must
   call. → §6.
10. Add `--explain [KEYPATH]` provenance (per-leaf layer/hook tracking through
    the fold, including dropped host keys). → §6.5.
11. Document the pipeline in the CLI: a `config-ref` composition section and/or
    `yolo help config-composition` covering config.lua paths, the layer model,
    the surface list, and the `ctx` API. → §3.
12. Route Lua errors (file/line) and `yolo config overlay --reset <agent>`
    through the CLI. → §3.4/§5.

**P2 — stretch (SHOULD; do not block the bar).**

13. `yolo completion <bash|zsh|fish>`; `yolo help <topic>` for backends/mounts/
    cgroups; generated man pages; `yolo config` effective-merged dump with
    provenance.
