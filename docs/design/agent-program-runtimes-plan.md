---
title: "Agent program runtimes — companion sketch"
date: 2026-09-21
status: draft
tags: [sketch, packs, programs, mise, node, launchers]
summary: "File-level map and measured anchors for [agent-program-runtimes.md]. A parking lot, not a hand-off: no design decision lives here, and it must not be built from while it is a SKETCH."
vantage:
  status-chip: true
---

# Agent program runtimes — companion sketch

**Status:** SKETCH, 2026-09-21 — incomplete, and unstable while questions are open.

> [!IMPORTANT]
> **Not a hand-off artifact.** An agent must not build from this while it is stamped SKETCH. What a
> real plan's product is — the map, the reuse, the traps — is written only by someone who has just
> read the tree, and that is `implementation-plan`'s job once
> [`agent-program-runtimes.md`](agent-program-runtimes.md)'s questions are ruled.

**Reads with:** [`agent-program-runtimes.md`](agent-program-runtimes.md) — **the design wins on
behavior.** If an entry here contradicts that doc, this file is wrong.

This file exists because the design doc is held at the altitude of systems and invariants, and the
material below is below that altitude: which files, which symbols, which measurements. It is here so
that material has somewhere to go that is not the design doc.

---

## 1. Measured anchors

Taken in a jail on 2026-09-21 at `753bcb88`. Re-measure rather than trust any of it — in particular
the pi version, which is installed evergreen.

| Fact | Where it was measured |
|---|---|
| pi declares `engines.node: ">=22.19.0"` | `~/.npm-global/lib/node_modules/@earendil-works/pi-coding-agent/package.json` |
| pi's bundle imports `enableCompileCache` unconditionally | same package, `dist/bundle/cli.js:2` |
| `enableCompileCache` is absent on 20.20.2, present on 22.23.2 and 24.19.0 | `/mise/installs/node/*/bin/node -e "import('node:module')…"` |
| The generated launcher's tail is a bare `exec "$REAL_BIN"` | `~/.yolo/bin/launch/pi` (generated from `npmLauncherTemplate`, `internal/entrypoint/shims.go`) |
| `$REAL_BIN` is a symlink to a `#!/usr/bin/env node` script | `~/.npm-global/bin/pi` |
| mise keeps alias symlinks beside real version dirs | `ls -l /mise/installs/node/` → `24 -> ./24.19.0` |
| `opencode-ai`'s npm bin is a native ELF | `~/.npm-global/bin/opencode` → `bin/opencode.exe` (`file` reports ELF) |
| The image's node is baked and always present | `flake.nix:1169` (`nodejs_24` in `coreFloorNames`); `/bin/node` is v24.19.0 here |
| The image identity is a content sha, with no node version | `/etc/yolo-jail-image-identity` |
| The entrypoint already executes binaries at boot | `internal/entrypoint/{runtime,identity,system_boot,boot}.go` (`ldconfig`, `iptables`, `socat`, `supervise`, `mise uninstall`) |

### The one thing not measurable from here

The macos-user node. `flake.nix:1169` includes `nodejs_24` in `coreFloorNames`, whose comment says a
second backend takes the list "without a second list"; [`macos-user-provisioning.md:224`](../design/macos-user-provisioning.md)
records a *measured* `npm: command not found` there, dated 2026-09-11 — the day before that floor
shipped. **Settle which is true before building anything backend-shaped.**

## 2. Presumed file map

Offered as a starting map, not an instruction — the resolver's home, its decomposition, and whether
resolution happens once per boot or once per program are the implementer's calls.

| File | Presumed change |
|---|---|
| `internal/packdecl/packdecl.go` | The optional node floor on `Install`, with its doc comment (these comments **are** the manifest reference) |
| `internal/packdecl/contributes.go` | Validation: value grammar, and refusal on any kind that is not `program` |
| `internal/entrypoint/shims.go` | The new splice in `npmAgentLauncher`, a new sentinel in `npmLauncherTemplate`, and the changed tail |
| `internal/entrypoint/` (a new resolver) | Candidate enumeration, version comparison, preference order, disclosure |
| `packs/pi/pack.json` | Declare the floor pi's package states |
| `internal/entrypoint/launchersplice_test.go`, `npmlauncher_test.go`, `launcherdir_test.go` | The hostile-value splice row, the byte-identical no-declaration case, the deleted-call-site test |
| `internal/packdecl/*_test.go` | Field validation and the wrong-kind refusal |
| [`tool-provisioning.md` §2](../research/tool-provisioning.md#2-the-node-question-resolved-one-by-default-two-only-on-override) | Its override table currently says agent CLIs follow the mise node. **It is accurate today** — amend it only when the code changes, not before |

## 3. Traps

- **`launcherUnpublished` and `launcherShadows` run at generation, and the launcher dir is cleared
  contents-only.** Anything the resolver writes has to live under the same anchor discipline
  (`resetAnchorDir`) or it disappears on the next boot.
- **`generate_agent_launchers` runs before `generate_mise_config`** in `boot.go`'s genStep list, and
  before the `mise install` provisioning step. A cold boot therefore has no mise-installed node at
  generation time — this is the fact that shapes [OQ-AR2](agent-program-runtimes.md#OQ-AR2).
- **Every spliced value is `shquote`'d into a bare position** (`npmLauncherTemplate`'s contract).
  A version string is attacker-influenceable only through a pack, but the contract has no
  exemptions and `launchersplice_test.go`'s hostile-value table is where a miss would show.
- **The no-declaration path must stay byte-identical.** The cheapest way to be sure is a golden of
  the generated body for a program with no floor, before and after.
- **Tests that pin a helper rather than the call site.** `GenerateAgentLaunchers` is the call site;
  a test that exercises the resolver directly will stay green if the splice is dropped from the
  generator.

## 4. Open blockers

Every entry that would rest on an unruled decision names it. None of these can be resolved here.

- The value's meaning shapes the comparator and the manifest grammar —
  [OQ-AR1](agent-program-runtimes.md#OQ-AR1).
- Whether anything is installed, and where, decides whether this sketch grows a launcher-lazy
  branch or a boot branch — [OQ-AR2](agent-program-runtimes.md#OQ-AR2).
- The unsatisfiable case decides whether the resolver returns a path, a path plus a warning, or a
  refusal for that one program — [OQ-AR3](agent-program-runtimes.md#OQ-AR3).
- Declared versus derived decides whether there is a schema change at all —
  [OQ-AR4](agent-program-runtimes.md#OQ-AR4).
