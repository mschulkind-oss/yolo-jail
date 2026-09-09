---
status: current
verified: 2026-09-09
verified_commit: 356bcec8
covers:
  - internal/cli/help.go
  - internal/cli/subhelp.go
  - internal/cli/dispatch.go
  - internal/cli/outputformat.go
  - internal/cli/config_ref.txt
  - internal/cli/configrefcoverage_test.go
  - internal/cli/usageexamples_test.go
  - internal/cli/help_test.go
tags: [standard, cli, help, json, discoverability, agents]
---

# Standard: the CLI is its own manual

**Status:** CURRENT as of 2026-09-09, verified against `356bcec8`.

**Audience:** anyone adding a `yolo` subcommand, a flag, a state-reporting surface, or a new
subsystem an agent has to operate (loopholes, builders, composed config, …). Read this before
shipping a command, and before assuming a design doc or an `AGENTS.md` note is where an operator
will look.

This is a **standard with numbered requirements**, and the numbers are an API: code comments across
`internal/cli`, `internal/prune`, `internal/loopholes` and `internal/broker` cite them as
*"self-documenting-cli item N"*. Never renumber. The enforcement list at the end has its own
numbering, cited the same way (*"enforcement item 3"*).

| Concern | Lives in |
| :--- | :--- |
| The dispatch registry — the authority on what commands exist | `internal/cli/dispatch.go` (`registry`) |
| Top-level command list and the hidden set | `internal/cli/help.go` (`commandHelp`, `hiddenFromCommandHelp`) |
| Per-command help: the shared predicate and usage registry | `internal/cli/subhelp.go` (`answerHelp`, `isHelpToken`) |
| `--format json` / `--json` parsing and refusal | `internal/cli/outputformat.go` (`parseOutputFormat`) |
| The config-schema concept surface | `internal/cli/config_ref.txt` (embedded; rendered by `runConfigRef`) |
| The briefing that points an agent at all of it | `internal/jailcontent` (`BriefingContent`) |

**Reads with:** [`information-at-the-point-of-need.md`](information-at-the-point-of-need.md) (the
principle that decides whether a fact belongs in a message or in a briefing — this standard is what
makes the "in a message" half reachable at all).

---

## The principle

An agent must be able to discover and operate **every** part of yolo purely by interrogating the
CLI — no reading source, no reading design docs, no host tooling. `yolo --help` names every command;
every command answers `--help` with its own synopsis, flags, arguments, effects, and an example;
every cross-cutting concept is reachable from a `config-ref` section; and anything that reports state
can emit machine-readable output on request. The CLI *is* the API, and it *is* the manual.

> If an agent has to `rg` the source or open `docs/` to learn how to drive a subsystem, the CLI has
> failed. Fix the CLI, not the docs.

## Why this matters here specifically

Three properties of yolo make this non-negotiable rather than nice-to-have:

1. **Agents are the primary operators.** The whole product exists to be driven by coding agents.
   Agents discover capabilities by trying `--help` and parsing output — they do not browse a docs
   site. A command that only reveals itself in source is, to the actual operator, invisible.
2. **The jail is credential- and identity-isolated.** An in-jail agent cannot lean on the host's
   shell history, the maintainer's memory, or host-side tooling to fill gaps. What the CLI tells it
   is *all* it has. The briefing already leans on this — it points at `yolo --help` and
   `yolo config-ref` as the entry points precisely because those are the only surfaces guaranteed to
   be present.
3. **Docs drift; the CLI can be tested against itself.** A checked-in doc and the code diverge
   silently. Help text and JSON output generated from the same registry the dispatcher uses can be
   asserted in `go test`. Self-documentation that lives *in* the binary is the only kind we can keep
   honest mechanically.

## The standard

Every **command** must satisfy 1–5; every **concept surface** must satisfy 6; programmatic surfaces
must satisfy 7.

### 1. Every command answers `--help` / `-h` / `help <sub>` — on demand, exit 0

Help is a *request*, not an error: it prints full help to **stdout**, exits **0**, and never
triggers the command's real work.

Every key in the dispatch registry answers all three spellings. `run` answers through its own
`runHelpRequested`, whose scan is deliberately stricter so `yolo -- claude --help` still reaches the
inner command; the command groups answer inside their own testable bodies via `isHelpToken`; the
rest answer via `answerHelp` at the top of each handler.

> [!WARNING]
> **`dispatchNative` is deliberately NOT a central interception point, and central interception is
> unimplementable here.** `yolo -- claude --help` must deliver `--help` to the inner command, which
> is why the top-level check counts only the first token. A central hook would have to carry a
> per-command predicate anyway, so the shape is one shared predicate plus a registry of usage texts,
> called at the top of each handler. That file's header states it; do not "simplify" it into the
> dispatcher.

> [!WARNING]
> **Asking a command what it does must never change the machine.** This requirement exists because
> two commands violated it: `yolo init --help` scaffolded a `yolo-jail.jsonc` and appended to
> `.gitignore` in the cwd, and `yolo init-user-config --help` wrote the user config. `check --help`
> ran a full check including a nix build, and two groups printed a one-line usage to stderr with
> exit 1 because `--help` landed on their misuse branch. Any new handler must answer help *before*
> its first side effect.

### 2. Help lists synopsis, flags, positional args, effects, and ≥1 example

Flag coverage is **derived, not maintained by hand**: `TestUsageListsEveryParsedFlag` parses this
package's own source, reads the name→handler mapping out of the `registry` composite literal, and
requires every `--flag` string literal in a handler's body to appear in that command's help. It
passes vacuously for a handler that delegates its parsing, which is the one gap in the mechanism.

Examples are derived too, rather than listed: `TestEveryCommandShowsACopyableExample` requires each
registry key's usage to carry an `Examples:` block holding a literal invocation a reader can paste.

### 3. Misuse exits non-zero with usage on stderr — and is distinct from help

The two branches must differ in every respect: help → stdout, exit 0; misuse → stderr, non-zero.
An unknown top-level command is stderr + exit 1. The command groups (`config`, `pack`, `apply`,
`describe`, `check-deps`, `host`, `programs`) exit 2 on an unexpected argument or unknown flag, and
that is the shape the rest should take. A refused `--format` exits 2 with stdout left **empty** —
a command that refused on stderr and then printed its human report anyway would hand a parser prose
with a non-zero exit, which is the trap in a friendlier disguise.

> [!WARNING]
> **This requirement is only half met, and the unmet half is the machine-detectability half.** The
> flag-parsing commands — `run`, `check`, `init`, `ps`, `prune` — still treat an unrecognized flag as
> ignorable and return their normal (often 0) code, so a typo'd flag is silently dropped. `check`
> says so in `checkOptions`' comment; `runInit`'s flag scan has no default arm. Two narrower
> refusals do exist and are not the general case: `parseOutputFormat` refuses a bad `--format`, and
> `prune` refuses flags it used to have, by name. Closing this properly is open work, not a
> documented exemption.

### 4. Top-level help lists every registered command

`commandHelp` is **exhaustive over the registry, minus `hiddenFromCommandHelp`**, and both directions
are enforced: `TestUsageListedCommandsAreRegistered` (help → registry) and
`TestEveryRegisteredCommandIsListedInHelp` (registry → help). A command may be omitted only by
naming itself in the hidden set *with a reason string* — an unlisted command needs a stated one, and
a hidden entry that is not a registry key also fails. The `internal` namespace is daemon plumbing
rather than an operator surface, and is hidden on that basis.

> [!WARNING]
> **A one-directional sync test is not a sync test.** This requirement was violated for months
> behind a green `TestUsageListedCommandsAreRegistered`, because it only checked that every *listed*
> command was registered — three registered macos commands were absent from `yolo --help` and
> answered a `--help` nobody could discover they had. The reverse direction is the half that catches
> a new command.

### 5. The discovery chain is closed: top help → each command's help → onward

The footer of `yolo --help` and each command's help point onward — a sibling command,
`yolo config-ref`, or a group's own `--help` — so an agent can walk from the top to any leaf. The
briefing opens the chain (`Jail tooling: yolo --help; config reference: yolo config-ref`), and the
loopholes branch is closed end to end.

> [!WARNING]
> **Do not soften a promise the CLI makes; make it true.** The `--help` footer once hedged with
> "Run `yolo <subcommand> --help` **where supported**". The hedge was deleted rather than softened,
> because requirement 1 made the promise true for every registered command. A hedge in help text is
> a standing admission that the standard is not met, and it is read by the operator least able to
> work around it.

### 6. Concepts are reachable from the CLI

Cross-cutting concepts must be operable from the CLI, not only from `docs/`. `yolo config-ref` is the
concept surface for the config schema; `yolo config --help` documents the composition pipeline's
layer order and subcommands. Both point onward at `docs/` rather than dead-ending.

> [!WARNING]
> **There is no `yolo help <topic>` mechanism.** Concepts with no config key to hang off — backends,
> mounts and overlays, the cgroup model — are reachable only through prose in a command's help or in
> `docs/`. That is the remaining structural gap in this requirement, and adding a topic surface is
> open work.

### 7. State-reporting surfaces offer `--format json`

Anything that reports state must emit stable, ANSI-free, machine-readable output on request:
`ps`, `check`, `loopholes list`, `loopholes status`, `broker status`, `prune`'s plan. Both `--json`
and `--format json` are accepted and must produce identical bytes.

Two properties matter more than the flag existing:

- **"Nothing to report" must still be a document.** A command that finds no config, no jails and no
  broker must emit valid JSON anyway. "Nothing to report" and "nothing was written" are the same
  bytes to a consumer, and the second is a bug it cannot diagnose.
- **A verb that would lie about the format refuses it.** An *acting* command does not accept
  `--format json` and then print prose; it refuses, so a caller never parses a report of work it
  did not get.

## Items the CLI already meets or exceeds

- **Dry-run / preview discipline.** `run --dry-run` renders a plan without launching; `prune` is
  dry-run *by default* and requires `--apply` to mutate. This is the destructive-safe default that
  best-in-class tools use.
- **`--version`.** Handled at the top level with a clean, stable version line.
- **An effective-config dump with provenance.** `yolo config dump` prints the computed config as
  canonical JSON, and `yolo config render <agent> --explain` names the layer or hook that set each
  key.

## Enforcement

The standard stays true only if it is machine-checked. yolo is a hand-rolled `registry` map rather
than cobra, so nothing comes for free. These numbers are cited from code comments.

1. **Every registered command has a `--help` handler that exits 0 to stdout and runs no side
   effect.** `TestEveryRegisteredCommandAnswersHelp` walks the registry, dispatches `--help` and
   `-h` with output captured, and asserts exit 0, stdout equal to the registered usage, empty
   stderr, and no ANSI.

   > [!IMPORTANT]
   > **The no-real-work clause is enforced by emptiness, not by an injected sink, and that is
   > stronger.** Each probe runs with cwd and `$HOME` pointed at fresh empty temp trees which must
   > still be empty afterwards. An injected sink only catches work that goes through the seam you
   > injected; emptiness catches *any* write — which is what `init --help` (cwd) and
   > `init-user-config --help` (`$HOME`) each did through no seam at all. A per-probe deadline bounds
   > the other failure mode: a regressed `check --help` would otherwise run a nix build inside
   > `just test-fast`.

2. **Registry ↔ help are bidirectionally in sync.** Both directions, plus the requirement that a
   hidden entry carries a reason and names a real registry key.
3. **`config-ref` covers every config key.** `TestConfigRefDocumentsEveryLiveKey` reads the schema's
   own key set (`config.TopLevelConfigKeys`) rather than a list retyped in the test, and
   `TestConfigRefAnnouncesTheRefusalForEveryRetiredKey` plus `TestConfigRefKeySectionsAreAllKnown`
   catch drift in the other direction. This is the highest-leverage single test in the set, because
   `config-ref` is hand-maintained and is the only thing an in-jail agent can interrogate about the
   schema.

   > [!IMPORTANT]
   > **Derive the check; never list the gaps.** Before this test existed, the live gap was
   > `required_capabilities` — accepted by the schema, documented nowhere. A hand-listed set of
   > known gaps named three different keys, and all three had since been resolved (two by
   > documentation, one by retirement). The list of gaps was wrong within weeks; the derivation was
   > not.

4. **Every state-reporting command accepts `--format json` and emits valid, ANSI-free JSON.**
   Driven end-to-end for the fast commands; asserted over injected report seams for `prune` and
   `check`, because one walks the whole disk and the other runs a nix image build, and dispatching
   either in the unit suite would put minutes into `just test-fast`.
5. **`yolo config render` fixture vectors:** `inputs → render` byte-checked in `go test`, so the
   composed output is pinned and the Lua/provenance path is exercised without a container.

## What this does not license

- **Not a licence to document a command in `docs/` instead of in its help.** A doc is where the
  *reasoning* goes; the operable facts go in `--help`. If the answer to "how do I drive this" is a
  path under `docs/`, the CLI has failed requirement 5.
- **Not a licence to add a flag the help text does not name.** Requirement 2's coverage is derived
  from the handler's own source, so an undocumented flag is a failing test rather than a style note.
- **Not a claim that every command should emit JSON.** Requirement 7 is about commands that *report
  state*. An acting verb refuses the flag rather than growing a second output mode.
- **Not a mandate for shell completions or man pages.** Both are explicit stretch goals — completions
  would have to be hand-authored against a hand-rolled dispatcher, and man pages are low priority
  for an agent-facing CLI. Neither blocks the bar.
