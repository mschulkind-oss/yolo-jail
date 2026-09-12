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

**Status:** SKETCH — incomplete, and unstable while questions are open.

**The design wins on behaviour.** This file holds settled-but-boring material only; every design
decision lives in [`mcp-presets-removal.md`](mcp-presets-removal.md), and nothing here may decide
one. An agent must **not** build from this file while it carries the SKETCH stamp — a real plan's
product is codebase knowledge, and the `implementation-plan` skill owns what this has to become
first.

Inventory below verified against `7079d3ef`, 2026-09-12.

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

Eleven files, listed so nobody discovers the eleventh at the end:

```text
integration/mcp_test.go                       internal/entrypoint/catalog_test.go
internal/agentcfg/zz_probe_test.go            internal/entrypoint/darwin_test.go
internal/config/injail_snapshot_test.go       internal/entrypoint/darwinstage_test.go
internal/config/validate_test.go              internal/entrypoint/receipts_test.go
internal/entrypoint/bashn_test.go             internal/entrypoint/serverrefresh_test.go
                                              internal/entrypoint/shell_test.go
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
- **`kind: "files"` and the executable bit.** The wrapper is bind-mounted `:ro` from the staged
  tree; confirm the mode survives staging before relying on it. Apple Container cannot bind a
  single file, so a one-file contribution is copied into the workspace state dir instead.
- **Two copies of `checkPresetNullConflicts`** exist because `check` cannot import the run
  pipeline. Delete both or neither.
- **`config_ref.txt` carries a second, unrelated staleness** in the same region: it still
  documents `${VAR}` expansion for `mcp_servers.env`, removed 2026-08-03. Fix it in a separate
  commit so the removal's diff stays readable.

Blocked on [OQ-MP4](mcp-presets-removal.md#OQ-MP4) — whether the composition is host-side or
in-jail decides which of `commonEnvBlock` and `liveTables` grows, and therefore what the wire
form is.

Blocked on [OQ-MP8](mcp-presets-removal.md#OQ-MP8) — one script or two decides whether
`GenerateMCPWrappers` loses one writer or three.

## Documentation that describes the old behaviour

| File | What it says today |
| :--- | :--- |
| [`USER_GUIDE.md`](../guides/USER_GUIDE.md) | An "MCP Presets" section with an availability table, an enable example, a disable example, and a troubleshooting line |
| [`mcp-configuration.md`](../reference/mcp-configuration.md) | Presets in the pipeline diagram, in the loader rules, and two rows of its Current values table |
| [`README.md`](../../README.md) | One sentence naming `mcp_presets` beside `mcp_servers` |
| [`yolo-jail.jsonc`](../../yolo-jail.jsonc), `template_tail.txt` | A commented example line carrying both names |
| [`macos-user-nix-and-features.md`](../reference/macos-user-nix-and-features.md), [`macos-user-provisioning.md`](macos-user-provisioning.md) | `mcp_presets` as an inert key on that backend |
| [`configuring-the-jail`](../../internal/jailcontent/builtinskills/configuring-the-jail) (built-in skill) | Lists the key among the config surfaces it covers |

## Roadmap

The roadmap is owned by another workflow in this sprint and is deliberately untouched. What it
needs is in the hand-off, not here.
