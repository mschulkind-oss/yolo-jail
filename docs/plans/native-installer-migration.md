---
title: "Plan: agent CLIs from npm to their vendors' native installers"
date: 2026-09-03
status: ready
tags: [packs, program-delivery, installers, evergreen]
summary: "Implementation plan for OQ-PD13. Two of the four npm packs flip cleanly (copilot, codex); opencode is blocked on a hardcoded install dir under a read-only home; pi's 'native installer' is an npm wrapper and must not be flipped. Also removes packs/claude/pack.json's dead autoUpdaterStatus."
---

# Plan: agent CLIs from npm to their vendors' native installers

**Design:** [`program-delivery.md` §3.5](../design/program-delivery.md#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03),
ruling **OQ-PD13** · **Status:** ready · Written against `a25e718b`, 2026-09-03.

**Precedence:** the design wins on behavior, the tree wins on fact, this file is advice and is the
first thing to be wrong. Never twist the code to match it.

**Scope.** Flip `program.via` from `npm` to `installer` for **copilot** and **codex**; do **not**
flip **pi**; **defer opencode** behind two prerequisites. Delete the dead
`preferences.autoUpdaterStatus` from `packs/claude/pack.json`. No Go changes are required for the
two flips — and that is the problem, see Traps.

## What was verified, 2026-09-03

Every URL below was **downloaded and read**; none was executed. All six answer HTTP 200 with a
shell script.

| Pack | Effective URL | Lands the bin at | Prefix override | Version pin |
| :--- | :--- | :--- | :--- | :--- |
| copilot | `gh.io/copilot-install` → `raw.githubusercontent.com/github/copilot-cli/…/install.sh` | `$PREFIX/bin`, `PREFIX` defaults to `$HOME/.local` for non-root | ✅ `PREFIX=` | ✅ `VERSION=` |
| codex | `chatgpt.com/codex/install.sh` → `releases.openai.com/codex/install.sh` | `${CODEX_INSTALL_DIR:-$HOME/.local/bin}`; payload under `${CODEX_HOME:-$HOME/.codex}/packages/standalone` | ✅ `CODEX_INSTALL_DIR=` | ✅ `CODEX_RELEASE=` |
| opencode | `opencode.ai/install` → `raw.githubusercontent.com/anomalyco/opencode/…/install` | **`$HOME/.opencode/bin`, hardcoded** (served script line 68 — a bare assignment, no `${…:-}`) | ❌ none | ✅ `VERSION=` |
| pi | `pi.dev/install.sh` | **npm's global prefix** — it runs `npm install -g --ignore-scripts --min-release-age=0 @earendil-works/pi-coding-agent` (`install.sh:925-927`); `$HOME/.local` only when that prefix is unwritable | indirect | ❌ none found |

**Could not verify:** whether copilot's `--no-auto-update` launch flag (declared at
`packs/copilot/pack.json:82`) suppresses the `isSea()` self-updater the flip is bought for — the
"no agent tests" rule forbids running the CLI to find out. Whether codex prompts without a TTY
(`CODEX_NON_INTERACTIVE` defaults to `false`) — read but not exercised.

## Map

| Path | Change |
| :--- | :--- |
| `packs/copilot/pack.json` | `via: npm` + `package` → `via: installer` + `url: https://gh.io/copilot-install`; decide `--no-auto-update` |
| `packs/codex/pack.json` | `via: npm` + `package` → `via: installer` + `url: https://chatgpt.com/codex/install.sh` |
| `packs/claude/pack.json` | delete the `managed.preferences` block (lines 58–62); KEEP the surface — its `retireOnFirstRender` is load-bearing, and a surface with neither `managed` nor `defaults` is valid (`packs/agy/pack.json:41-47`) |
| `README.md:290-293` | the "installed via" column for copilot/codex |
| `docs/design/program-delivery.md` §3.5 | the per-agent table's "Today" column |
| `docs/design/agent-install-in-ci.md` §2.3 | "two mechanisms, six packs, nine installs" — the split moves |
| `integration/installmechanism_test.go:14-22` | header says "eight of them the same npm code path" |

## Reuse

- **The verification harness already exists, per pack, and is exactly the "`--version` probe only"
  shape AGENTS.md permits.** `integration/agents_test.go`'s `packMatrix` +
  `TestPackInstallsVersionsAndConfigures/<pack>`, driven by `.github/workflows/packs.yml`'s
  `pack: [claude, copilot, opencode, pi, codex, agy]` matrix on both arches, triggered by any
  `packs/**` edit. One flip = one commit = one green cell per arch. Nothing new to write.
- `TestPackRendersConfigAndLauncher` is the every-push half and installs nothing — it asserts the
  launcher file exists. It is `via`-agnostic and needs no edit.
- `internal/entrypoint/nativelauncher_test.go` already covers the native template against an
  `httptest` server, including the served-a-web-page diagnosis. Add cases there, not a new file.
- `packs/claude/pack.json` and `packs/agy/pack.json` are the manifest shape to copy: `url` +
  `via: "installer"`, no `package`, `install_hints` unchanged.

## Traps

- **`nativeLauncherTemplate` hardcodes `REAL_BIN="$HOME/.local/bin/$BIN"`**
  (`internal/entrypoint/shims.go:868`). **Constraint:** a native flip works only where the
  installer's *default* lands the binary at exactly that path. Miss it and the launcher reinstalls
  on every invocation, then exits 1 with `⚠ <bin> not available` — installed, and not found.
- **The manifest cannot pass env or argv to an installer.** `packdecl.Install` is
  `{Kind, Bin, Package, Flags, InstallerURL}` and `Flags` is npm-only (`nativeAgentLauncher` splices
  BIN, URL, STAMP_DIR, RECEIPTS and nothing else). So `PREFIX=`, `VERSION=`, `CODEX_INSTALL_DIR=`
  and `CODEX_NON_INTERACTIVE=` are **not expressible today** — the flip rides vendor defaults.
- **PATH order silently defeats the flip on every existing workspace.** `BootPath`
  (`internal/entrypoint/boot.go:356-361`) puts `$NPM_CONFIG_PREFIX/bin` **second** and
  `$HOME/.local/bin` **fifth**. A workspace that already npm-installed copilot keeps resolving the
  stale npm binary forever; the launcher is last on PATH and never runs. `catalogNpmOrphans`
  (`internal/entrypoint/catalog.go:80`) *reports* the leftover at boot and, per OQ-PD4, does not
  remove it. **This is the migration's real cost** and it is what OQ-PD12a (B2) fixes. Symptom: a
  green CI cell (fresh workspace) beside a user whose `copilot --version` never moves.
- **The native launcher's update branch is a hardcoded `"$REAL_BIN" install`** (shims.go:936, 941),
  `|| true`. Right for claude; already wrong for agy (`agy update`). After a flip, copilot and codex
  get an hourly unknown-subcommand call that changes nothing. **The flip buys the cold-install
  mechanism; OQ-PD14's declared verb is what buys evergreen.**
- **The origin gate.** Verified against `packload.HonoredInstalls` (`internal/packload/packload.go:491`):
  it refuses only `InstallerURL != "" && !MayAccessHost`, and `MayAccessHost` is `true` by
  construction for embedded and local packs (`internal/config/packs.go:182`). **So the shipped packs
  are unaffected.** What changes is the *claim surface*: `HostAccessClaims`
  (`internal/packdecl/contributes.go:1156`) now emits `installer <URL>` and
  `NeedsHostAccessContributions` emits "program via installer (runs a fetched script)" for each
  flipped pack. For a **fetched** pack shipping an agent this is approvable and refusable — and per
  [`trust-paths.md` §3.1](../design/trust-paths.md) (OQ-TP6) a refusal **refuses the launch**, not
  the contribution. npm stays deliberately ungated. Say this in the commit body.
- **Installer scripts call shimmed tools.** codex's uses `find` (line 745), pi's uses `grep -Fxq`.
  The launcher already runs them under `YOLO_BYPASS_SHIMS=1`; do not remove it.
- **`packs/claude/pack.json:60` is dead — but not for the reason the ticket gives.** Measured
  against the installed ELF at `~/.local/share/claude/versions/2.1.260` (this jail's version, not
  2.1.220): there is **no** `tengu_dead_probe_autoupdater_status` — nine `tengu_dead_probe_*` names
  exist and that is not one. The string `"preferences"` appears **zero** times in the binary.
  `autoUpdaterStatus` appears twice, read by one migration on the **`~/.claude.json` global-config**
  object: `if(e.installMethod!==void 0)return e; … case"disabled": autoUpdates=false`. So the pack's
  entry is dead because it is in the wrong **file** (`~/.claude/settings.json`) under a
  **`preferences` wrapper nothing reads** — the reader is live, it just never sees this key. Delete
  it; do not "fix" it to `autoUpdates: false`, which is the opposite of what §3.5 wants.

## Build order

Each step is one commit, one pack, independently revertible. `packs.yml` fires on every `packs/**`
edit, so each step's proof is its own CI cell on both arches.

1. **Delete claude's dead `preferences.autoUpdaterStatus`.** Zero behavior change (verified above).
   → `just test-fast`, then the `claude` cell of `packs.yml`.
2. **Flip `codex`.** Lowest risk of the two: `~/.local/bin` is the installer default, and its
   payload dir `~/.codex/packages/standalone` is already a writable bind (`state at: .codex`).
   → `TestPackInstallsVersionsAndConfigures/codex`, both arches.
3. **Flip `copilot`**, and rule on `--no-auto-update` in the same commit (see Blockers).
   → `TestPackInstallsVersionsAndConfigures/copilot`, both arches.
4. **Stop.** opencode and pi do not flip here — see Don't.

## Ships with

- **Unit, `internal/entrypoint/nativelauncher_test.go`:** an installer that exits 0 having written
  nothing to `~/.local/bin` must produce the `⚠ <bin> not available` message and rc 1, and must
  write **no** receipt (the existing `_do_install` guard). That is the regression test for the
  landing-path trap, and it fails if the `[ -x "$REAL_BIN" ]` guard is deleted.
- **Unit, `internal/packload/packload_test.go`:** a *fetched* pack declaring `via: installer` is
  refused while an npm sibling on the same pack is granted. `HonoredInstalls` has this shape at
  `:114-129` already — extend rather than duplicate.
- **Integration:** none new. Steps 2 and 3 are proven by the existing per-pack cells.
- **No test may start an agent.** `--version` only; that is what `packMatrix` already does.
- **Docs, by path:** `README.md:290-293`; `docs/design/program-delivery.md` §3.5's per-agent table
  ("Today" column for copilot/codex); `docs/design/agent-install-in-ci.md` §2.3 (the eight-npm/one-
  native split, and the "nine installs" arithmetic); `integration/installmechanism_test.go`'s
  header comment.
- **Norms:** `just format` then `just check-ci` before each commit (the pre-commit hook runs it);
  `just done` at the end. Never `--no-verify`, never `--amend`.
- **Cheap and yours:** JSON key ordering in the manifests (they are gofmt-irrelevant and the loader
  is order-blind); whether `install_hints` stays (it should — it is unrelated to `via`).

## Don't

- **Don't flip `pi`.** Its "native installer" *is* npm: `pi.dev/install.sh` runs
  `npm install -g --ignore-scripts --min-release-age=0 @earendil-works/pi-coding-agent` into npm's
  global prefix, which in the jail is writable, so the binary lands at `$NPM_CONFIG_PREFIX/bin/pi`
  — exactly where it lands today. The flip would change nothing about delivery, break the launcher
  (`REAL_BIN` points at `~/.local/bin/pi`), lose the pack's declared `--ignore-scripts`, add an
  origin-gated claim, and pull in a script that shells out to `sudo` and drives an arrow-key TTY
  menu. pi's evergreen story is `pi update --self` under OQ-PD14, which needs no `via` change at all.
- **Don't flip `opencode` yet.** Two independent blockers: its `INSTALL_DIR=$HOME/.opencode/bin` is
  a bare assignment with no override, and `/home/agent` is a **`:ro`** bind
  ([`jail-home.md` §2.2](../design/jail-home.md)) with no `state at: .opencode` in its manifest — so
  `mkdir -p "$HOME/.opencode/bin"` fails EROFS before anything downloads. It needs a writable-dir
  contribution *and* an answer for a binary that is neither on `BootPath` nor at the launcher's
  `REAL_BIN`. Both are design work, not this plan's.
- **Don't add an `env` field to `packdecl.Install`** to pass `PREFIX=`/`VERSION=`. It is the obvious
  move and it is a schema change that OQ-PD14 is already opening the same struct for — land one
  contribution field, not two, and let that plan own it.
- **Don't `npm uninstall -g` from the entrypoint** to clear the stale copies. OQ-PD4 rules that
  dropping a program is an explicit act; the boot catalog reports and does not remove.

## Blockers

- **OQ-PD14 (the pack-declared update verb) is a hard dependency for the *benefit*, not for the
  flip.** Planned in [`evergreen-agent-updates.md`](evergreen-agent-updates.md); do not design it here. Until it lands, a flipped pack self-updates
  only insofar as its own binary does.
- **OQ-PD12a / B2 (launch dir ahead of the install prefixes)**, same sibling plan, is what makes
  the flip reach an existing workspace. Without it, steps 2 and 3 are correct for new workspaces and inert for old
  ones. Not a reason to hold the flip — a reason to say so in the commit body.
- **Stop and ask: copilot's `--no-auto-update`.** The pack declares it; the flip exists to turn the
  vendor's self-updater *on*. Keeping both is contradictory, and dropping it changes what a shipped
  agent does on a user's machine. It is not verifiable under the no-agent-tests rule. Ask before
  step 3.
