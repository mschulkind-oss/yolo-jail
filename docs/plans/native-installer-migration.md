---
title: "Plan: agent CLIs from npm to their vendors' native installers"
date: 2026-09-03
status: accepted
tags: [packs, program-delivery, installers, evergreen]
summary: "Implementation plan for OQ-PD13. Shipped 2026-09-04: codex flipped and claude's dead autoUpdaterStatus is gone. copilot did NOT flip — its installer picks PREFIX=/usr/local under root and the jail's rootfs is read-only, so the flip would make it uninstallable. Its --no-auto-update question was ruled separately on 2026-09-12 (option A: the flag is dropped, without the flip). opencode stays deferred; pi's 'native installer' is an npm wrapper and must not be flipped."
---

# Plan: agent CLIs from npm to their vendors' native installers

**Design:** [`program-delivery.md` §3.5](../design/program-delivery.md#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03),
ruling **[OQ-PD13](../design/program-delivery.md#decision-ledger)** · Written against `a25e718b`, 2026-09-03.

**Status:** DECIDED, 2026-09-04 — shipped in part: codex flipped and claude's dead
`autoUpdaterStatus` is gone; **copilot did not flip**, and that is the work left. Re-checked
against the tree 2026-09-24: both sibling rulings this plan waited on
([OQ-PD12a](../design/program-delivery.md#decision-ledger) and
[OQ-PD14](../design/program-delivery.md#decision-ledger)) shipped 2026-09-04, and codex's
installer prompt and payload capture were fixed 2026-09-14. `omp`, an npm pack added 2026-09-15,
postdates this plan and was not assessed by it.

> [!IMPORTANT]
> **OUTCOME, 2026-09-04. Steps 1 and 2 shipped; step 3 does not exist as written.**
>
> | Step | Outcome |
> | :--- | :--- |
> | 1 · delete claude's `managed.preferences` | ✅ shipped. Re-measured against the ELF this jail runs (**2.1.261**, not the 2.1.260 the plan read): `"preferences"` still appears **zero** times, `autoUpdaterStatus` twice, both in the `~/.claude.json` migration. Thirteen tests pinned the key as a specimen and were repointed at `permissions.defaultMode`. |
> | 2 · flip `codex` | ✅ shipped. `chatgpt.com/codex/install.sh` re-fetched 2026-09-04 (200, `text/x-sh`, 30285 bytes); `BIN_DIR="${CODEX_INSTALL_DIR:-$HOME/.local/bin}"` with **no root branch**, so the default landing path is the launcher's `REAL_BIN`. |
> | 3 · flip `copilot` | ⛔ **REFUSED — the flip would make copilot uninstallable.** See below. It never reached the `--no-auto-update` question, which was then ruled **on its own, without the flip**, on 2026-09-12: **option A, the flag is dropped** (Blockers). |
>
> **Why copilot cannot flip, and what the plan's Traps were missing.** Trap 1 names the constraint
> — the installer's *default* must land the binary at `$HOME/.local/bin/$BIN` — and the plan's
> verification table then records copilot's default as *"`$PREFIX/bin`, `PREFIX` defaults to
> `$HOME/.local` for non-root"*. **The jail is not non-root.** `flake.nix` sets `USER=root`, so
> `id -u` is 0 and the installer takes `PREFIX="${PREFIX:-/usr/local}"`; `internal/cli/run/assemble.go`
> passes `--read-only` unconditionally, so `mkdir -p /usr/local/bin` fails — measured in-jail
> 2026-09-04: `mkdir: cannot create directory '/usr/local': Read-only file system`. The installer
> then prints *"Error: Could not create directory /usr/local/bin"* and `exit 1`s, `_do_install`
> swallows the status with `|| true`, nothing lands at `REAL_BIN`, and every `copilot` invocation
> re-downloads and then exits 1 with `⚠ copilot not available`.
>
> `PREFIX=` would fix it and **is not expressible**: `packdecl.Install` cannot pass env to an
> installer, and this plan's own *Don't* forbids adding the field ahead of
> [OQ-PD14](../design/program-delivery.md#decision-ledger). **copilot's flip belongs to whatever
> opens that struct.**
>
> **The generalisation, since the plan's table could not have caught this:** *self-updates once
> native* is necessary and never sufficient. The sufficient question is **does the installer's
> default prefix — under the UID and the filesystem the jail actually runs with — equal
> `REAL_BIN`?** Three facts, and the verification table has a column for none of them. Ask them of
> `opencode` too before its blockers are called closed.
>
> **`--no-auto-update`: MEASURED 2026-09-04, THEN DECIDED 2026-09-12 — option A, the flag is
> DROPPED.** The measurement is what makes the ruling safe and is kept in full in the Blockers
> bullet; the ruling sits beside it there. The choice was the human's, as this plan said it had to
> be: *"drop the no auto update too, we decided to just let agents be agents. and of course fix the
> autonomy."* It shipped **without** the flip, which this plan is still refusing — so the cost
> option A names is not yet paid (the `isSea()` gate makes copilot's updater a notifier under npm),
> and the flip, whenever it comes, inherits a dropped flag rather than a decision.
>
> **What was inert until the sibling plan landed — and it landed the same day** (both stated in
> the flip's commit body): without [OQ-PD14](../design/program-delivery.md#decision-ledger)'s
> declared update verb, codex's native launcher called the hardcoded `"$REAL_BIN" install` hourly
> — an unknown subcommand for codex, `|| true`, so a no-op; and without
> **[OQ-PD12a](../design/program-delivery.md#decision-ledger)** the launch dir stayed last on
> `BootPath`, so an existing workspace kept resolving `$NPM_CONFIG_PREFIX/bin/codex` and never
> reached the new launcher. ✅ **Both shipped 2026-09-04** in
> [`evergreen-agent-updates.md`](evergreen-agent-updates.md)'s merge: `packs/codex` declares
> `update: ["update"]`, and the launch dir now sits ahead of the install prefixes. The flip now
> reaches old workspaces too.

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
| copilot | `gh.io/copilot-install` → `raw.githubusercontent.com/github/copilot-cli/…/install.sh` | ⛔ `$PREFIX/bin`, `PREFIX` defaults to `$HOME/.local` for non-root — **and to `/usr/local` for root, which the jail is** (see the outcome box) | ✅ `PREFIX=`, but not from a manifest | ✅ `VERSION=` |
| codex | `chatgpt.com/codex/install.sh` → `releases.openai.com/codex/install.sh` | `${CODEX_INSTALL_DIR:-$HOME/.local/bin}`; payload under `${CODEX_HOME:-$HOME/.codex}/packages/standalone` | ✅ `CODEX_INSTALL_DIR=` | ✅ `CODEX_RELEASE=` |
| opencode | `opencode.ai/install` → `raw.githubusercontent.com/anomalyco/opencode/…/install` | **`$HOME/.opencode/bin`, hardcoded** (served script line 68 — a bare assignment, no `${…:-}`) | ❌ none | ✅ `VERSION=` |
| pi | `pi.dev/install.sh` | **npm's global prefix** — it runs `npm install -g --ignore-scripts --min-release-age=0 @earendil-works/pi-coding-agent` (`install.sh:925-927`); `$HOME/.local` only when that prefix is unwritable | indirect | ❌ none found |

~~**Could not verify:** whether copilot's `--no-auto-update` launch flag suppresses the `isSea()`
self-updater the flip is bought for — the "no agent tests" rule forbids running the CLI to find
out.~~ **ANSWERED 2026-09-04 by reading the bundle instead of running it** (Blockers): it does, in
both builds. The flag itself is gone as of 2026-09-12, so there is nothing left to suppress.
~~Whether codex prompts without a TTY (`CODEX_NON_INTERACTIVE` defaults to `false`) — read but not
exercised.~~

**MEASURED 2026-09-11 — codex's installer PROMPTS, and the question was posed one notch too
narrowly.** Running `codex --version` in a `macos-user` jail installed 0.154.0 and asked
`Start Codex now? [y/N]`; a human answered `N`. The tty question is not "without a TTY" but *which*
tty: `prompt_yes_no` (`install.sh:830-851`) tries **`/dev/tty` first**, falls back to stdin when
that is a tty, and only declines when neither is — so redirecting stdin does not silence it, and a
yolo launch always has a tty to find. The one prompt reachable this way is at `:888`, immediately
after a successful install; a second at `:925` offers to uninstall a conflicting
package-manager-managed copy.

**FIXED 2026-09-14.** The Codex pack now sets `CODEX_NON_INTERACTIVE=1`, and install capture
walks the exact nested payload at `~/.codex/packages/standalone` alongside `~/.local`. The
captured `~/.local/bin/codex` symlink therefore has its target after materialization. The rest of
`~/.codex` remains outside capture and hardlink deduplication because it contains mutable auth,
sessions, histories, and databases.

Two consequences for this plan. **The flip is still right** — the install itself needed no npm and
no node, which is the whole point of it. But **a `via: installer` flip inherits the vendor's
prompts**, and `y` at that prompt starts an agent session. That is why this went unseen until a
human sat in front of it: the pack matrix reaches the `else` branch of `prompt_yes_no` — no
`/dev/tty`, stdin not a tty, so the installer declines and CI never blocks. **The exposure is an
interactive launch**, where the prompt is answerable and a wrong keystroke starts an agent inside
what the caller asked to be a `--version` probe. `codex` honors
`CODEX_NON_INTERACTIVE=1`, and **`packdecl.Install` has no field that can carry it** — filed as
[`OQ-PS8`](../design/provisioner-sets.md#OQ-PS8), with the alternative of giving installers no tty
at all in core. ✅ **Closed for codex on 2026-09-14 by neither of those**: the pack sets the
variable through its `env` contribution (above), which reaches the installer because it is set
for the whole jail. That route fits a vendor-prefixed name and not a generic one such as copilot's
`PREFIX`, so [`OQ-PS8`](../design/provisioner-sets.md#OQ-PS8) stays open for the general case.

**MEASURED 2026-09-14 — codex's standalone payload was not captured; FIXED the same day** (the
*FIXED 2026-09-14* paragraph above). The installer unpacks the standalone runtime under
`${CODEX_HOME:-$HOME/.codex}/packages/standalone` and symlinks `~/.local/bin/codex` to it. Capture
then walked only `paths.HomeSurfaces()` — `.npm-global`, `.local` and `go` — so `~/.codex` was
omitted entirely: capture `2a8d85fe399399fa` recorded only the symlink and a stray
`wire-bridge.log`, and materializing it in a fresh jail yielded a dangling symlink, so the launcher
fell back to a live download. Capture now walks
[`paths.InstalledProgramSurfaces()`](../../internal/paths/paths.go), which adds exactly
`.codex/packages/standalone` and nothing else under `~/.codex`.

## Map

| Path | Change |
| :--- | :--- |
| `packs/copilot/pack.json` | ⛔ **`via` not changed** — the flip is refused, see the outcome box. The file did change on 2026-09-12, for the other half of step 3: `--no-auto-update` dropped (option A) and `--yolo` moved under `autonomy`. |
| `packs/codex/pack.json` | `via: npm` + `package` → `via: installer` + `url: https://chatgpt.com/codex/install.sh` |
| `packs/claude/pack.json` | delete the `managed.preferences` block (lines 58–62); KEEP the surface — its `retireOnFirstRender` is load-bearing, and a surface with neither `managed` nor `defaults` is valid (`packs/agy/pack.json:41-47`) |
| `README.md:290-293` | the "installed via" column for copilot/codex |
| `docs/design/program-delivery.md` [§3.5](../design/program-delivery.md#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03) | the per-agent table's "Today" column |
| [`agent-install-in-ci.md`](../reference/agent-install-in-ci.md#two-install-mechanisms) | "two mechanisms, six packs, nine installs" — the split moves |
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
  (`internal/entrypoint/shims.go`). **Constraint:** a native flip works only where the
  installer's *default* lands the binary at exactly that path. Miss it and the launcher reinstalls
  on every invocation, then exits 1 with `⚠ <bin> not available` — installed, and not found.
- **The `program` contribution cannot pass env or argv to an installer.** `packdecl.Install` has
  grown since this was written (`UpdateVerb`, `NodeFloor`), but no installer env, and `Flags` is
  still npm-only. So `PREFIX=`, `VERSION=` and `CODEX_INSTALL_DIR=` are **not expressible per
  installer** — the flip rides vendor defaults. ⚠ *Corrected 2026-09-24:* a pack's `env`
  contribution sets a variable for the whole jail, and that is how `CODEX_NON_INTERACTIVE=1`
  reaches codex's installer since 2026-09-14. It is not a fix for `PREFIX=`, which every other tool
  in the jail would read too.
- ~~**PATH order silently defeats the flip on every existing workspace.**~~ ✅ **Resolved
  2026-09-04 by [OQ-PD12a](../design/program-delivery.md#decision-ledger) (B2).** As written:
  `BootPath` put `$NPM_CONFIG_PREFIX/bin` ahead of `$HOME/.local/bin` and the launch dir last, so a
  workspace that had already npm-installed a program kept resolving the stale npm binary and never
  reached the launcher. The launch dir now sits ahead of every install prefix. The leftover npm
  copy is still only *reported* at boot (`catalogNpmOrphans`, `internal/entrypoint/catalog.go`)
  and, per [OQ-PD4](../design/program-delivery.md#decision-ledger), removed only by
  `yolo programs remove --apply`.
- ~~**The native launcher's update branch is a hardcoded `"$REAL_BIN" install`**~~ ✅ **Resolved
  2026-09-04 by [OQ-PD14](../design/program-delivery.md#decision-ledger).** As written, every native
  launcher ran `"$REAL_BIN" install` hourly, `|| true` — right for claude, wrong for agy and codex.
  The verb is now declared per pack (`Install.UpdateVerb`). The point stands as a rule: **the flip
  buys the cold-install mechanism; the declared verb is what buys evergreen.**
- ~~**The origin gate.**~~ ⚠ **Gone since 2026-09-04, the day this plan shipped.** As written,
  `packload.HonoredInstalls` refused a `via: installer` from a **fetched** pack while npm stayed
  ungated, so a flip changed the fetched-pack claim surface. [OQ-TP9](../design/trust-paths.md#decision-ledger)
  deleted that refusal — `npm install -g` from the same fetched tree runs `postinstall` ungated, so
  the gate refused one path to arbitrary in-jail execution while permitting another — and
  `HonoredInstalls` now grants every install declaration (its `refused` return is always nil). A
  flip therefore changes no trust outcome; the pack read/exec banners are what disclose it.
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
  it; do not "fix" it to `autoUpdates: false`, which is the opposite of what [§3.5](../design/program-delivery.md#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03) wants.

## Build order

Each step is one commit, one pack, independently revertible. `packs.yml` fires on every `packs/**`
edit, so each step's proof is its own CI cell on both arches.

1. **Delete claude's dead `preferences.autoUpdaterStatus`.** Zero behavior change (verified above).
   → `just test-fast`, then the `claude` cell of `packs.yml`.
2. **Flip `codex`.** Lowest risk of the two: `~/.local/bin` is the installer default, and its
   payload dir `~/.codex/packages/standalone` is already a writable bind (`state at: .codex`).
   → `TestPackInstallsVersionsAndConfigures/codex`, both arches.
3. ~~**Flip `copilot`**, and rule on `--no-auto-update` in the same commit (see Blockers).~~
   ⛔ **REFUSED 2026-09-04** — the installer's root branch puts the binary nowhere the jail can
   write, let alone at `REAL_BIN`. The outcome box has the measurement. Its CI cell would have
   caught it, one downloaded image later; the read did. **The two halves separated:**
   `--no-auto-update` was ruled and dropped on its own on 2026-09-12 (option A, Blockers), in a
   commit that touched no `via`. "In the same commit" was a convenience, not a dependency — the
   flag is a launch contribution and the flip is an install one.
4. **Stop.** copilot, opencode and pi do not flip here — see Don't and the outcome box.

## Ships with

- **Unit, `internal/entrypoint/nativelauncher_test.go`:** an installer that exits 0 having written
  nothing to `~/.local/bin` must produce the `⚠ <bin> not available` message and rc 1, and must
  write **no** receipt (the existing `_do_install` guard). That is the regression test for the
  landing-path trap, and it fails if the `[ -x "$REAL_BIN" ]` guard is deleted.
- ~~**Unit, `internal/packload/packload_test.go`:** a *fetched* pack declaring `via: installer` is
  refused while an npm sibling on the same pack is granted.~~ Void since
  [OQ-TP9](../design/trust-paths.md#decision-ledger) deleted the refusal (see Traps); there is no
  longer a per-origin split to pin.
- **Integration:** none new. Steps 2 and 3 are proven by the existing per-pack cells.
- **No test may start an agent.** `--version` only; that is what `packMatrix` already does.
- **Docs, by path:** `README.md:290-293`; `docs/design/program-delivery.md` [§3.5](../design/program-delivery.md#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03)'s per-agent table
  ("Today" column for copilot/codex); [`agent-install-in-ci.md`](../reference/agent-install-in-ci.md#two-install-mechanisms) (the eight-npm/one-
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
  menu. pi's evergreen story is `pi update --self` under [OQ-PD14](../design/program-delivery.md#decision-ledger), which needs no `via` change at all.
- **Don't flip `opencode` yet.** Two independent blockers: its `INSTALL_DIR=$HOME/.opencode/bin` is
  a bare assignment with no override, and `/home/agent` is a **`:ro`** bind
  ([`jail-home.md` §2.2](../reference/jail-home.md)) with no `state at: .opencode` in its manifest — so
  `mkdir -p "$HOME/.opencode/bin"` fails EROFS before anything downloads. It needs a writable-dir
  contribution *and* an answer for a binary that is neither on `BootPath` nor at the launcher's
  `REAL_BIN`. Both are design work, not this plan's.
- **Don't add an `env` field to `packdecl.Install`** to pass `PREFIX=`/`VERSION=` from this plan.
  The reason as first written — [OQ-PD14](../design/program-delivery.md#decision-ledger) was opening
  the same struct — is spent: that verb shipped 2026-09-04 with no env field. What still holds is
  that per-recipe installer env is an open question, not a plan's to settle:
  [`OQ-PS8`](../design/provisioner-sets.md#OQ-PS8) owns it.
- **Don't `npm uninstall -g` from the entrypoint** to clear the stale copies. [OQ-PD4](../design/program-delivery.md#decision-ledger) rules that
  dropping a program is an explicit act; the boot catalog reports and does not remove.

## Blockers

- ✅ **[OQ-PD14](../design/program-delivery.md#decision-ledger) (the pack-declared update verb)** —
  a hard dependency for the *benefit*, not for the flip — **shipped 2026-09-04** in
  [`evergreen-agent-updates.md`](evergreen-agent-updates.md).
- ✅ **[OQ-PD12a](../design/program-delivery.md#decision-ledger) / B2 (launch dir ahead of the
  install prefixes)**, what makes a flip reach an existing workspace, **shipped 2026-09-04** in the
  same plan. Neither blocks copilot's flip; the `PREFIX` problem does.
- **copilot's `--no-auto-update`: ASKED, AND ANSWERED — option A, 2026-09-12. The flag is
  DROPPED.** The maintainer's words: *"drop the no auto update too, we decided to just let agents be
  agents. and of course fix the autonomy."* It shipped on its own, with copilot still on npm and the
  flip still refused (see the outcome box).

  **Why the measurement below is kept rather than replaced by the verdict.** The measurement is
  *why the ruling is safe now*: under npm `isSea()` is false, so what the dropped flag re-enables is
  a startup version CHECK that can only NOTIFY (`Update not supported when running js directly`,
  then `<tag> available · run /update`). That is the same poll-and-notify shape
  [`trust-paths.md` §1](../design/trust-paths.md#1-the-verdict) row 1 ([OQ-TP5](../design/trust-paths.md#decision-ledger))
  already requires of yolo's OWN launcher, so the ruling does not contradict *"a binary that changes
  between two invocations with nobody present"* — it adds a second poll beside yolo's, and no
  second writer.

  **What is therefore still open, and must not be treated as inherited.** Option A's stated cost —
  the vendor's updater running on a user's machine on its own schedule, outside yolo's record — is
  **unpaid**, because the `isSea()` gate withholds the download-and-replace half. The flip flips
  that gate. So the fork A-vs-B is settled for *copilot-on-npm* and NOT for the scope question
  underneath it (whether an agent CLI updating itself unobserved is acceptable at all —
  [P6](../design/program-delivery.md#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03)).
  **Whoever flips copilot re-asks it, with a dropped flag as the starting position rather than as
  the answer.**

  **What shipped with the ruling, and is not part of it.** `--yolo` moved from the pack's plain
  `launch` contribution into an `autonomy` contribution's autonomous posture — *"and of course fix
  the autonomy"* — because a permission-bypass flag declared as a plain launch flag is outside the
  notch policy of
  [`yolo-as-environment-manager.md` §4.2](../design/yolo-as-environment-manager.md#42-agent-autonomy-is-a-confinement-policy-not-baked-pack-config)
  by construction. Unrelated to delivery; recorded here only because it rode the
  same ruling. copilot declares **no guarded posture**: it has no persistent permission setting to
  tighten (the pack's `defaults: {"yolo": true}` is not one — 1.0.48 has no such settings key), and
  a launch flag not selected is already the tightening.

  **What is now fact** (read statically out of the installed `@github/copilot` 1.0.48 — `index.js`
  and `app.js`; no CLI was started, so the no-agent-tests rule is intact):

  - `--no-auto-update` **does** disable the self-updater, and does it in **both** builds:
    `if (argv.includes("--no-auto-update") || argv.includes("--prefer-version")) return false`.
    `COPILOT_AUTO_UPDATE=false` is the env spelling of the same switch.
  - The updater is **additionally** gated on `Aq() = require("node:sea").isSea()`. Under npm that
    is false and it only *notifies* — `"Update not supported when running js directly"`. That gate
    flipping true is the entire thing [OQ-PD13](../design/program-delivery.md#decision-ledger) buys for copilot.
  - It is off in CI regardless of either: the default consults
    `!(CI || BUILD_NUMBER || RUN_ID || SYSTEM_COLLECTIONURI)`.

  **The options. A is the one taken (2026-09-12), minus the flip it assumed:**

  | | Choice | What it costs |
  | :--- | :--- | :--- |
  | **A** ✅ | Flip, and **drop** the flag — *taken 2026-09-12, flag only* | The vendor's updater runs on a user's machine on its own schedule, outside yolo's record — the native launcher's vendor self-updates deliberately emit no receipt ([§6.3](../design/program-delivery.md)), so drift becomes the reconcile's problem. This is where [OQ-PD13](../design/program-delivery.md#decision-ledger)'s rationale points. |
  | B | Flip, and **keep** the flag | Buys the SEA build, `VERSION=` pinning and a single binary, but **not** evergreen — the one thing the flip was bought for. Evergreen would then have to come from [OQ-PD14](../design/program-delivery.md#decision-ledger)'s declared verb (`/update`, or re-running the installer), which has the merit of making an update something yolo triggers and can record. |
  | C | Flip, keep the flag, pin with `VERSION=` | Reproducible copilot. **Not expressible** — the manifest cannot pass env to an installer, the same wall the flip already hits. |
  | D | Do not flip | Where the tree is, and where it stays until the `PREFIX=` problem is solved regardless. |

  **A and B are a real fork, not a formality:** they disagree about whether an agent CLI updating
  itself unobserved is acceptable — a question about the scope of
  [P6](../design/program-delivery.md#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03),
  not about copilot. Worth settling alongside [OQ-PD14](../design/program-delivery.md#decision-ledger), since B only makes sense once the declared
  verb exists. **Taking A for the flag did not settle that fork** — under npm the unobserved update
  it disagrees about cannot happen (see the bullet above), so the disagreement is intact and waiting
  at the flip.
