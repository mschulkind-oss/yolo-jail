---
title: "MCP presets removal — implementation sketch"
date: 2026-09-12
status: draft
tags: [plan, sketch, mcp, packs, removal]
summary: "Parking lot for the implementation material of the mcp_presets removal: the call sites, the tests that pin the preset names, the staging traps, and the documentation that describes the old behaviour. Not a hand-off artifact."
vantage:
  status-chip: true
---

# MCP presets removal — implementation sketch

**Status:** SKETCH, 2026-09-12 — incomplete. Every design question it used to wait on is settled
(2026-09-20), so what it owes now is completion against the tree rather than a decision.

**The design wins on behaviour.** This file holds settled-but-boring material only; every design
decision lives in [`mcp-presets-removal.md`](mcp-presets-removal.md), and nothing here may decide
one. An agent must **not** build from this file while it carries the SKETCH stamp — a real plan's
product is codebase knowledge, and the `implementation-plan` skill owns what this has to become
first.

Inventory first taken against `7079d3ef`, 2026-09-12, and re-checked 2026-09-22: every symbol named
below still exists under that name.

---

## Call sites that name a preset

The seven in [§2](mcp-presets-removal.md#2-what-mcp_presets-is-today--seven-places-two-packages)
of the design, as symbols:

| Symbol | Package |
| :--- | :--- |
| `validMCPPresets` | `internal/config` |
| `Env.LoadMCPServers` (the `presets` map) | `internal/entrypoint` |
| `Env.chromeDevtoolsArgs` | `internal/entrypoint` |
| `mcpPresetNpmPackages` | `internal/entrypoint` |
| `bootstrapTemplate` (the `case "$pkg" in` arm) | `internal/entrypoint` |
| `chromeWrapper`, `GenerateMCPWrappers` | `internal/entrypoint` |
| `catalogLocalBinOrphans` (the `declared` map) | `internal/entrypoint` |

## Call sites that name only the key

Mechanical; they follow the key rather than the names.

| Symbol / site | Package |
| :--- | :--- |
| `knownTopLevelConfigKeys`, `retiredTopLevelConfigKeys`, `validateMCPPresets` | `internal/config` |
| the `mcp_presets` row of the inheritance table | `internal/config` |
| `checkPresetNullConflicts` — **two copies** | `internal/cli/run`, `internal/cli/check` |
| the `YOLO_MCP_PRESETS` pair in `commonEnvBlock` | `internal/cli/run` |
| `mcpPresetsJSON` in the macos-user run plan | `internal/macosuser` |
| `Env.SkipMCPPresets`, `Env.LoadMCPPresetNames` | `internal/entrypoint` |
| `RunDarwinBootstrap`'s warning | `internal/entrypoint` |
| `macosuser.ProvisionNeeded`'s carve-out comment | `internal/macosuser` |
| the store-packages inert-feature note | `internal/cli/run` |
| `config_ref.txt` | `internal/cli` |

## Consumers of `mcpPresetNpmPackages` — all three must keep working

It is not only the installer. Losing any of these silently is the failure mode.

1. `bootstrapTemplate`'s npm-install arm — the install itself.
2. `catalogNpmOrphans`' `declared` set — or every jail reports the package as an orphan.
3. `serverRefreshSet` — the evergreen refresh.

A `kind: "program"`, `via: "npm"` contribution covers all three through `HonoredInstalls` and the
launcher in `~/.yolo/bin/launch/`, which mediates every invocation since B2. Check each rather
than assuming the substitution is total.

## Tests that pin the preset names

**The set is a grep, not a list.** A written-down list goes stale silently and this one did —
`internal/entrypoint/mcp_test.go`, which covers the wired chrome-devtools argv, arrived after it was
taken. Take it fresh:

```console
$ rg -l 'chrome-devtools|sequential-thinking|mcp_presets|MCPPresets' --glob '*_test.go' internal integration
```

Two need a replacement fixture rather than a deletion: `injail_snapshot_test.go` and
`validate_test.go` use `mcp_presets` only as *"some top-level key"*, so a live key substitutes
cleanly. `integration/mcp_test.go`'s `TestMcpPresetCanBeEnabled` becomes the pack's own test —
same assertion, different selection mechanism — and `TestSameFilePresetAndNullOverrideIsRejected`
loses its subject entirely.

## Traps

- **`packs/` is in `flake.nix`'s `goSrc` fileset, but the embed list is explicit.** A new pack
  directory must be added to `packs/embed.go`'s `go:embed` directive or it vanishes from every
  installed binary; `TestEmbedMatchesTree` catches the drift.
- **`git add` before verifying in a nested jail.** Nix sees tracked files only, so an untracked
  new pack directory moves neither the image nor its identity hash and the skew check reports a
  false "matches".
- **The exec bit survives staging; the DESTINATION is the trap.** `packstage`'s copier preserves
  `0o111` — a pack ships its tools, pinned by `TestStageShipsExecutables` — so a wrapper that is
  executable in the pack arrives executable in the jail. What it may **not** do is land on the jail's
  PATH: `packdecl` refuses a `files` destination that is, contains, or sits inside any of
  `paths.JailPathHomeDirs`, and that list includes `.local/bin` — where `GenerateMCPWrappers` writes
  `chrome-devtools-mcp-wrapper` today. So the pack puts its wrapper
  somewhere off PATH and the `mcp` entry names it by absolute path, which is what an MCP client needs
  anyway: it spawns servers with a sanitized environment, so no PATH lookup happens.
- **Apple Container cannot bind a single file**, so a one-file `files` contribution is copied into
  the workspace state dir (which that backend mounts wholesale at the home) instead of mounted. A
  directory contribution needs no such dance.
- **Two copies of `checkPresetNullConflicts`** exist because `check` cannot import the run
  pipeline. Delete both or neither.
- **`config_ref.txt` carries a second, unrelated staleness** in the same region: it still
  documents `${VAR}` expansion for `mcp_servers.env`, removed 2026-08-03. Fix it in a separate
  commit so the removal's diff stays readable.

## Two rulings that decide shapes in here

Each one pins a shape the inventory above could otherwise only describe two ways.

- **Composition is HOST-SIDE** ([OQ-MP4](mcp-presets-removal.md#OQ-MP4) was *dissolved* 2026-09-20 —
  its premise, that the host cannot know a jail path, was false). An `mcp` declaration composes
  exactly like `providers`, because `paths.JailPathHomeDirs` is a host-side constant naming the jail's
  PATH directories and `TestJailPathHomeDirsCoversBootPath` keeps it in step with `BootPath`. So the
  composed table crosses as a host-assembled env pair the way `YOLO_PROVIDERS` does, and the in-jail
  side READS it: `liveTables` gains a reader, not a composer. No new placeholder vocabulary is owed —
  that list is the vocabulary.
- **One plain wrapper script, carrying `--executablePath`** ([OQ-MP8](mcp-presets-removal.md#OQ-MP8),
  2026-09-20 — not a service, not a daemon, and not the orphan's `--browser-url` shape). So
  `GenerateMCPWrappers` loses exactly one writer, the fat `chrome-devtools-mcp-wrapper`, and keeps the
  two thin ones (`mcp-wrappers/node`, `mcp-wrappers/npx`), which are not preset-specific.

⚠ **What the removal is still gated on is [OQ-MP7](mcp-presets-removal.md#OQ-MP7)'s consequence, not
on anything in this file.** The ruling redraws the `packs` scope rule on **host reach** rather than on
install, and that redrawing lives in
[`loophole-system.md`](../reference/loophole-system.md#principles)'s `R5` and in
[`workspace-skills.md`](workspace-skills.md)'s [`OQ-WS1`](workspace-skills.md#OQ-WS1) — still open.

## Documentation that describes the old behaviour

| File | What it says today |
| :--- | :--- |
| [`USER_GUIDE.md`](../../userguide/README.md) | An "MCP Presets" section with an availability table, an enable example, a disable example, and a troubleshooting line |
| [`mcp-configuration.md`](../reference/mcp-configuration.md) | Presets in the pipeline diagram, in the loader rules, and two rows of its Current values table |
| [`README.md`](../../README.md) | One sentence naming `mcp_presets` beside `mcp_servers` |
| [`yolo-jail.jsonc`](../../yolo-jail.jsonc), `template_tail.txt` | A commented example line carrying both names |
| [`macos-user-nix-and-features.md`](../reference/macos-user-nix-and-features.md), [`../reference/macos-user-provisioning.md`](../reference/macos-user-provisioning.md) | `mcp_presets` as an inert key on that backend |
| [`configuring-the-jail`](../../internal/jailcontent/builtinskills/configuring-the-jail) (built-in skill) | Lists the key among the config surfaces it covers |

## Roadmap

The roadmap is owned elsewhere and is deliberately untouched by this file. What it needs is in the
hand-off, not here.
