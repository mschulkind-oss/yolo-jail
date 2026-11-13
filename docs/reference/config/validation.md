---
title: "yolo's own config: checking an edit with yolo config check"
status: current
stage: CURRENT
verified: 2026-11-12
verified_commit: 8dc1bab44
covers:
  - internal/cli/check/configcheck.go
  - internal/cli/check/sections_misc.go
  - internal/cli/check/checkcmd.go
  - internal/cli/config.go
  - internal/cli/configeditverbs.go
  - internal/config/validate.go
tags: [config, check, validation, reference]
summary: "yolo config check answers whether a config edit is valid: the same validator yolo check uses, over config files and the selected packs' declarations only, with no persistent write, network, runtime, Nix or credential, and a closing scope block naming what it did not look at. yolo check stays the machine-readiness report."
---

# yolo's own config: checking an edit with `yolo config check`

**Status:** Verified 2026-11-12 against `8dc1bab44`.

"Is my config edit valid?" and "is this machine ready to launch a jail?" are two questions with
two commands. **`yolo config check`** answers the first: it parses, merges and validates the user
and workspace config and the selected packs' declarations, with the validator `yolo check` uses,
and touches nothing else. **`yolo check`** stays the broad readiness report: runtime, Nix, image,
services, loopholes and the rest. Disk inventory and reclaim are neither; they are `yolo stores`
and `yolo prune`.

The one rule every guidance surface repeats: after a config change, by a `yolo config …` or
`yolo pack …` verb or by hand, run `yolo config check`; when a jail will not start or the machine
is suspect, run `yolo check`.

## What it reads, and the membership rule

A probe belongs in `yolo config check` only if it is **config-determined** (a term the validation
design coined): its verdict depends only on the user and workspace config files, the declarations
of the packs that config selects, and, on the host only, the existence and type of paths the config
names. Anything else is readiness and stays in `yolo check`.

| Area | In `config check` |
| :--- | :--- |
| Config files: parse and load, TOML and legacy JSONC | yes |
| Merged config: schema, scopes, paths, providers, profiles, models, capabilities, pack relationships | yes |
| Selected packs: resolution, declarations, cross-pack name collisions | yes, selected packs only |
| Runtime availability, executable resolution, Nix, image, services, loophole liveness | no |
| Entrypoint dry-run (it renders every embedded pack surface, not only the selected) | no |
| Git status of a local clone | no (`SkipLocalCloneGit`) |
| Config-change approval | no; asked at launch, or `yolo check --accept-config-changes` |
| Storage ensure, pack nudge, cache relocation, approval snapshot, `doctor_cmd` | never: these write or may mint credentials |

The validator is shared, not copied: `ConfigCheck` calls `config.ValidateConfigWithOptions` and the
same capability, profile and collision helpers as `yolo check`, so every FAIL `config check`
raises, `yolo check` raises on the same inputs.

## Effects

- **No persistent write.** Selected-pack staging goes to a private temporary directory, removed on
  every exit path; that is the one qualification to "read-only".
- **No network, credential, prompt, runtime, Nix, build or install.** The one subprocess is the
  read-only `git rev-parse` / `git ls-tree` the pack resolver runs against an existing store for a
  selected fetched pack; it never fetches, clones or writes a store. A config with no fetched pack
  runs no subprocess.
- **No lock.** A concurrent writer can at worst cause a parse FAIL that a re-run clears.

## Invocation and output

- **Forms:** `yolo config check`, `--json`, `--format json`, `--help`. Any other argument, including
  `--build`, `--no-build` and `--accept-config-changes`, exits 2 with `unexpected argument`
  before any read.
- **Exit codes:** 0 when nothing failed, 1 when anything failed, 2 for usage. WARN and SKIP do not
  fail. Exit 0 means "no config FAIL", not "ready to launch", and the help says so.
- **Sections:** Config Check (the files), Merged Configuration, Selected Packs, Scope, Summary.
- **Staged gating:** a file that does not parse ends the run; the later stages are listed as
  *not reached* (in scope but gated by an earlier FAIL), never as OK and never counted as SKIP.
- **A fetched pack not in the local store** (not fetched, not checked out, revision missing, no
  default ref, or no runnable `git`) is a SKIP for that pack, naming `yolo pack install` (or
  repairing Git) and the re-run; everything that does not need its declaration still runs, and
  the summary counts the skipped pack checks. A readable but malformed declaration is a FAIL,
  never downgraded (`configPackCoverage`).
- **JSON** carries the same findings as text, plus `mode` (`"config"`; `yolo check` reports
  `"full"`) and `scope`: `not_checked`, an array of `{area, reason, verb}`, and `not_reached`.

### The scope block

Every report from either command ends with a **scope block** (a term the validation design coined):
each area the run did not examine, why, and the verb that does. It carries no badge and no count,
so an omission can never read as a pass. `yolo config check` lists the readiness areas above, each
with `yolo check`, and disk with `yolo stores / yolo prune` (`configCheckScopeEntries`). In a jail
it adds "Host paths and mounts": the config there is a generated snapshot, so host-path findings
are not emitted at all, and the entry says to run `yolo config check` on the host.

`yolo check`'s own scope block names disk usage and `yolo stores / yolo prune`
(`sectionScope`); the disk walk it once ran is gone from it on the host and in a jail alike.

## After an edit

A `yolo config set|add|unset` that writes runs this same check right after the write, through
`runConfigCheckAt`: the output still says the file was written, and a failing check makes the edit
exit non-zero with "the post-write check did not pass" (`assertPublicPostWriteCheck`). The check
runs after a user-scope write as well as a workspace one; the design's CV-D10 said a user-scope
edit keeps its earlier behavior, and the code does not distinguish them. A dry run or a no-op writes
nothing and checks nothing.

## `yolo check` in a jail

Inside a jail, `yolo check` with neither build flag does not build the image
(`check.DefaultBuildEnabled`); explicit `--build` still does. `--no-build` keeps its meaning on the
host: everything but the build. `doctor` stays an alias of `check`.

## What it is not

- **Not a readiness check.** It never asks whether a runtime, image or service works.
- **Not an approval.** It never approves a config change, migrates a file, adds a pack or runs a
  `doctor_cmd`.
- **Not a second validator.** A new rule is added to the shared validator, never only here.

## Why it's this way

| Id | Decision |
| :--- | :--- |
| <a id="CV-D1"></a>CV-D1 | Add `yolo config check`: config validation only, read-only, the same exit contract as `check`. |
| <a id="CV-D2"></a>CV-D2 | A probe belongs in it only if it is config-determined. |
| <a id="CV-D3"></a>CV-D3 | It calls the same validator as `yolo check`; a section that mixes validity with environment probing is split in place, never copied. |
| <a id="CV-D4"></a>CV-D4 | Every report from either command ends with a scope block. |
| <a id="CV-D5"></a>CV-D5 | `yolo check` keeps its breadth; `--no-build` is not narrowed; `doctor` stays an alias. |
| <a id="CV-D6"></a>CV-D6 | The disk-usage section leaves `yolo check`, for `yolo stores` and `yolo prune`. |
| <a id="CV-D7"></a>CV-D7 | Inside a jail, `yolo check` with no build flag does not build; `--build` is honored. |
| <a id="CV-D8"></a>CV-D8 | `config check` takes no build or approval flag, and never approves, nudges, migrates or runs a `doctor_cmd`. |
| <a id="CV-D9"></a>CV-D9 | Guidance is one rule on every surface. |
| <a id="CV-D10"></a>CV-D10 | A public `config set|add|unset` that writes runs this check after the write, keeping the write's disclosure and exit status. |

Rejected alternatives: narrowing `--no-build` to config only (it would silently change a flag
scripts rely on), making `yolo check` config-only with `doctor` broad (every caller would lose
coverage), a `--scope` or `--fast` flag (a matrix beside the build flags), and keeping disk behind
`--disk` (`yolo stores` and `yolo prune` already own it).

**Guards.** `TestCheckOptionsWiring` (`internal/cli/outputformat_test.go`) and
`internal/cli/configcheck_test.go` fail if `configRunW` loses the route or the route enters `Check`
or storage initialization; `TestEverySectionIsWired` keeps `yolo check`'s sections wired;
`assertPublicPostWriteCheck` (`internal/cli/configeditdispatch_test.go`) pins the post-write check;
`TestYoloConfigCheckValidWorkspace` and `TestYoloConfigCheckInvalidWorkspace`
(`integration/cli_test.go`) run the real CLI.

## Where the code lives

- The command: `internal/cli/config.go` (`runConfigCheckAt`), `internal/cli/check/configcheck.go`
  (`ConfigCheck`, `configPackCoverage`, `configCheckScopeEntries`, `ConfigCheckHelp`).
- The shared validator: `internal/config/validate.go` (`ValidateConfigWithOptions`).
- `yolo check`'s scope block and build default: `internal/cli/check/sections_misc.go`,
  `checkcmd.go` (`DefaultBuildEnabled`).
- The post-write route: `internal/cli/configeditverbs.go` (`runConfigEditWithPostWriteCheck`).
