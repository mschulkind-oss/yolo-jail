---
title: "What each environment can actually provision — the survey, the coverage matrix and the nix resolver"
date: 2026-09-20
status: accepted
tags: [packs, program, requires, provisioning, notch, nix, npm, brew, capture, host, guest, evidence]
summary: "The evidence half of provisioner-sets.md, split out on 2026-09-20: which mechanisms can make a binary present at each notch and whether yolo drives them, which manager covers which agent CLI, the nix resolver read in depth, and the dated commands behind all of it. No proposal, no leanings, no open questions — the model and the rulings stay in the doc this one serves."
vantage:
  status-chip: true
---

# What each environment can actually provision — the survey, the coverage matrix and the nix resolver

**Status:** CURRENT — the evidence companion to
[`provisioner-sets.md`](provisioner-sets.md), split out of it on 2026-09-20 because one document
was carrying both the argument and everything the argument stands on. **Nothing here proposes
anything and nothing here needs a ruling**: every ruling, every leaning and every open question
lives in that doc, which cites this one. Claims are labelled **MEASURED**, **READ FROM CODE** or
**NOT MEASURED** and keep the date they were made on — the tree claims at `77190a2b`/`6eb7fe7f`
on 2026-09-11, the inherited nix claims on 2026-08-02 and 2026-08-23
([§4.1](#41-inherited-from-the-retired-doc-with-its-own-dates)).

> **In short.** Three readers are served by this material and only one of them needs this file.
> Someone deciding the pack contract reads [`provisioner-sets.md`](provisioner-sets.md); someone
> checking **why the contract is shaped that way** reads this; someone sitting at a Mac with a
> list of things to run reads the runbook. This is the middle one: what mechanisms exist per
> environment, which manager covers what, the nix resolver in depth, and the dated facts behind
> all of it.

**Where to start.** [§1](#1-the-provisioner-inventory-per-environment) is the inventory, and the
findings in [§1.1](#11-five-findings-the-table-forces) are what the model doc's questions fall
out of. [§2](#2-the-coverage-matrix-which-manager-covers-what) is the coverage matrix — the
single fact that makes *"prefer the system manager"* impossible as a rule.
[§3](#3-the-nix-resolver-in-depth) is the one resolver read in depth, and
[§4](#4-facts-verified-and-how) is the ledger of what was actually run to get here.

> [!NOTE]
> **`M1`–`M5` are the five Mac measurements of 2026-09-11**, run on the maintainer's Mac —
> facts no Linux jail can reach, because a nested jail is structurally blind to the `macos-user`
> backend and to rootless podman. They are cited by id throughout; the commands, the results and
> what each one decides are in
> [`mac-provisioner-measurements.md`](../plans/runbooks/mac-provisioner-measurements.md), not here.

**Reads with:** [`provisioner-sets.md`](provisioner-sets.md) (the model, the rulings and the open
questions — this doc is its evidence half),
[`noncontainer-nix-environment.md`](noncontainer-nix-environment.md) (retired;
[§3](#3-the-nix-resolver-in-depth) is its content, and the note there says why it must not be
split back out), [`program-delivery.md`](program-delivery.md) (the jail's delivery classes and
resolvers, named from the **record** side where this doc names the same mechanisms from the
**environment** side), [`../reference/macos-user-provisioning.md`](../reference/macos-user-provisioning.md) (the guest's
floor and its provisioning stage),
[`../plans/runbooks/mac-provisioner-measurements.md`](../plans/runbooks/mac-provisioner-measurements.md)
(the third reader's file — the five Mac items, with the commands and what each decides), and
[`../reference/nix-across-backends.md`](../reference/nix-across-backends.md) (what each backend's
nix path produces, as built).

---

## 1. The provisioner inventory, per environment

**Three terms, coined here.** A **provisioner** is a mechanism that can make a binary present in
an environment, together with whether yolo drives it there. Every resolver in
[`program-delivery.md` §6](program-delivery.md#6-the-general-seam-one-ledger-many-resolvers) is one —
that doc names them from the **record** side (who keeps the lockfile); this doc names the same
mechanisms from the **environment** side (is it here, and does yolo run it). Two provisioners
below are *not* [`program-delivery.md` §6](program-delivery.md#6-the-general-seam-one-ledger-many-resolvers) resolvers: the system package manager, which yolo hints, and the capture
store, which yolo drives. An environment's **provisioner set** is the provisioners it offers. A
provisioner's **disposition** at a notch is one of **drives** (yolo runs it), **hints** (yolo
prints its command and stops) or **absent** (no code path). None of the three words appears in
the code; the nearest thing is the confinement Profile's one provisioning primitive
([§1.1](#11-five-findings-the-table-forces), F4).

Notches are the three values of the confinement dial —
[`yolo-as-environment-manager.md` §4](yolo-as-environment-manager.md#4-confinement-a-dial-with-three-notches).
**guest** below means the shipped `macos-user` backend; the Linux guest is unbuilt and has no
row.

| # | Provisioner | Underlying tool | Declared by | jail | guest (`macos-user`) | host | Record |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| 1 | **nix image** — the baked floor plus `packages:` | nix (`nix build .#ociImage`) | `packages:` (config key, not a pack kind) | **drives** | absent — no image | absent | `flake.lock`, load sentinel, image GC root |
| 2 | **nix profile** — a buildEnv realized by `darwinpkg.Materialize`/`MaterializeAt`. ⚠ **Two attrs since 2026-09-12**: `yoloNoncontainerProfile` (the FLOOR plus `packages:`) for a notch with no image, `yoloNoncontainerPackages` (declared alone) for a container that already has one | nix | the floor, plus `packages:` | **drives**, opt-in only: `YOLO_STORE_PACKAGES=1` on podman + Linux + a nix daemon (`internal/cli/run/storepackages.go:314`) — declared packages ONLY, never the floor | **drives** — and no longer "the one mechanism that works there", since the floor arrives the same way | **absent — no caller.** `yolo host apply` has no `packages` path, honoured or refused ([§3.1](#31-what-is-already-solved-stated-precisely)) | `--out-link` GC root |
| 3 | **mise** | mise | `mise_tools` (config key) | **drives** — `mise install` in `setupScript` (`internal/cli/run/command.go`) | **drives, since 2026-09-12** — `mise` is on the floor and the confined provisioning stage runs `mise install` before the agent (`internal/macosuser/provision.go`); the warning that said "absent" was retired with the gap. ⚠ NOT MEASURED on hardware | absent | `mise.lock` honoured, never written by yolo |
| 4 | **npm / go, for servers** — LSP and MCP presets | `npm install -g`, `go install` | `lsp_servers`, `mcp_presets` | **drives** — bootstrap script (`internal/entrypoint/shell.go:204`) plus the evergreen refresh (`internal/entrypoint/serverrefresh.go`) | **drives, since 2026-09-13 — ⚠ NOT MEASURED on hardware, and MEASURED FALSE there on 2026-09-12.** The bootstrap generated the script and the stage execed it, but the script installs from `$YOLO_LSP_NPM_INSTALL` / `$YOLO_LSP_GO_INSTALL` and the only producer of either was the container's podman `-e` lines — so the list was empty and the stage exited 0 having installed nothing. Both variables now cross into the bootstrap env **and** the session env file the stage sources ([handoff](../plans/handoff-macos-user-open-threads.md#1-lsp_servers-installs-nothing-on-this-backend--ruled-and-wired-2026-09-13)). `mcp_presets` is still absent and **warned**: the wrappers are Linux-absolute and the stage installs none of the npm packages behind them. ⚠ the refresh is baked into every launcher and **silently no-ops** ([§1.1](#11-five-findings-the-table-forces), F5) | absent | `~/.yolo-installed-lsps` sentinel; receipts |
| 5 | **npm, for programs** — `program via: npm` | `npm install -g` | pack `program` | **drives** — lazy launcher, hourly update (`internal/entrypoint/shims.go:342`) | **driven but unprovisioned** — the launcher is generated (`RunDarwinBootstrap`, `internal/entrypoint/darwin.go`) and second on `SandboxPath` (`internal/macosuser/macosuser.go`), and nothing supplies `npm`; it fails on the first invocation — **MEASURED 2026-09-11** ([M1](../plans/runbooks/mac-provisioner-measurements.md#m1--does-the-guest-have-any-working-program-provisioner)): `npm: command not found` then `⚠ <bin> not available`, exit 1, so *warned* after all | **hints** — present/missing plus a remedy; a `yolo host -- <bin>` wrapper is written (`hostwrap.Body`, `internal/hostwrap/hostwrap.go`) and exits 127 when the binary is absent (`internal/cli/host.go`) | receipt `kind:"npm"` |
| 6 | **vendor installer** — `program via: installer` | `curl` the script, then `bash <file>` (`shims.go:1478`) | pack `program` | **drives** | **drives** — `curl` and `bash` exist at `/usr/bin` and it succeeds: **MEASURED 2026-09-11** (M1), three of three packs, two installing from scratch. The one working `program` provisioner the guest has | **hints**, as row 5 | receipt `kind:"installer"` |
| 7 | **capture store** — yolo's own CAS | reflink → hardlink → copy (`internal/capture/materialize.go:148`) | derived from row 6 | **drives**, cold install only; auto-capture default on | **recording half only, and that half is MEASURED 2026-09-11** ([M4](../plans/runbooks/mac-provisioner-measurements.md#m4--does-the-capture-recording-half-work-on-hardware)): `yolo capture claude` records and stores an entry; what refuses here is AUTO-capture and `_try_materialize` (`internal/cli/run/autocapture.go:16-31`; F5) | absent | capture manifest; receipt `kind:"capture"` |
| 8 | **system package manager** — brew, brew-cask, apt, dnf, pacman; nix by elimination | none — command strings only | `install_hints` on `program` and `requires` | n/a (the image is the floor) | **hints** — `AssertRequiredBins` warns by name (`darwin.go`) | **hints only** — `check-deps` / `host apply` print the remedy and write `~/.config/yolo/Brewfile` and kin (`internal/cli/checkdeps.go`); **never executed** | the generated manifest |
| 9 | pnpm launcher | `npm install -g` | hardcoded (`shims.go:588`) | **drives** | driven, unprovisioned (as row 5) | absent | receipt `kind:"npm"` |
| 10 | claude plugins | `claude plugins install` | pack hook | **drives** | **drives** (`darwin.go:112`) | refused by design (`internal/render/fieldset.go:118-120`) | claude's own file |

Every cell is READ FROM CODE at `77190a2b`. **The guest column's rows 5, 6 and 7 are now also
MEASURED on hardware** (2026-09-11, M1 and M4), and two of the three changed: row 6 from *may succeed* to **drives**, row 7's refusal from `yolo capture` to
auto-capture. The rest of that column is still READ FROM CODE only.

### 1.1 Five findings the table forces

**F1 — The nix asymmetry: the host is the only notch where yolo owns no provisioner.** The jail
gets nix as an image (row 1) or, opted in, as a profile (row 2); the guest gets nix as a profile
(row 2). The same MECHANISM serves both — two consumers, `macos-user` and the Linux store farm
(`internal/cli/run/storepackages.go:327` calls `darwinpkg.MaterializeAt`) — and **zero at the
host**. ⚠ It was the same flake ATTRIBUTE until 2026-09-12, and it is now two: the floor belongs
in the notch with no image and must stay out of the store farm, whose directory outranks `/bin`
([`../reference/macos-user-provisioning.md`](../reference/macos-user-provisioning.md)). So the host is not merely "the notch with
the fewest provisioners"; it is the notch with none yolo drives. That, and not anything about
the kinds, is why `program` degenerates there: with nothing to drive, every declaration reduces
to *is it on PATH, and what would install it* — which is the whole of `requires`. The merged
doc reached this same conclusion for its one resolver and stopped there; its
[§3.4](#34-not-orthogonal-to-confinement-the-provisioning-primitive-below-jail) is the argument.

**F2 — The system package manager is a provisioner yolo models completely and never uses.**
`detectManager` picks brew on macOS and probes apt/dnf/pacman/brew on Linux, reaching `nix` by
elimination (`internal/depcheck/depcheck.go`). `installCmd` knows each manager's verb,
brew's cask verb included (`:190`). `Manifest` writes the manager's own bundle file
(`:237`), and `check-deps` puts it at `~/.config/yolo/Brewfile` (`internal/cli/checkdeps.go:78-93`).
Then it stops. **MEASURED negative:** no reader of a remedy ever executes it — every consumer of
`Result.Remedy`, `.Fallback` and `.SelfInstall` is a print or a string builder, and no
`exec.Command` exists in `internal/depcheck`, `internal/render`, `checkdeps.go` or
`applyhostdeps.go`. The deferral is by name, in the code:

```go
// It NEVER installs anything (BACKLOG's detect-vs-apply split): it detects and hands off
// with the command. The offer-to-run (behind a batched, sudo-shown-through confirm,
// OQ-9) belongs to `apply` at a lower notch — this verb is the probe half, usable by a
// project's own doctor over the same declared hints.
```

(`internal/cli/checkdeps.go:9-12`.) The offer's UX was ruled on 2026-08-01 — env-manager
[`OQ-9`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase): *batched by
elevation class, sudo first* — and that plan's own audit says its resolution *"has no
consumer at all today"*. What the roadmap's thread 29 did on 2026-09-10 was **authorise** Phase
6.4 and 4.3 as the mechanism for the `--assert` fatal, not design them, and
[`OQ-RO7`](../reference/report-tiers.md#why-its-this-way) — whether the fatal covers `program` as well as `requires` — was
RULED on 2026-09-11: **both fatal, only `program` gets the offer**, which is the predicate
[`OQ-PS3`](provisioner-sets.md#decision-ledger)'s recipe model reinforces rather than disturbs (a `requires` is a need with no
runnable recipe, so there is nothing to offer). ⚠ **And the shipped precedence has the pack choosing.** `depcheck.Check` ranks the
declaring pack's *own* installer first and the detected manager's hint second, keeping the
manager's command only as `Fallback` (`depcheck.go:147-151`, `:160-164`, re-read 2026-09-11;
commit `b796d8b8`, *"remedies that lead with upstream"*). Under P1 that order is inverted: the
user's preferred manager leads, and the pack's recipe is what it falls back to — which is the
[shipped-default-order ruling](provisioner-sets.md#decision-ledger), and the reason that
precedence comment is preserved rather than deleted.

**F3 — `program` and `requires` differ nine ways in a jail and collapse at the host, and the
code says so itself.** In a jail: combine rule (`CombineExclusive` by bin vs `CombineShared`,
`internal/packdecl/kinds.go:296-307`); a launcher vs nothing (`internal/entrypoint/requires.go:12-14`:
*"this generates NOTHING"*); capturable (installer-via only, `internal/cli/capturehost.go:283`) vs
never; a receipt vs none; an agent-name claim vs deliberately not one
(`internal/packload/footprint.go:857`, `:908-918`); review-worthy vs not; a launch disclosure vs
none; a self-install command vs `""` (`contributes.go:570-571`); seven accepted fields vs two. At
the host: one collector, `Manifest.DepRequirements`, one probe, one report line differing in its
kind label (`internal/cli/applyhostdeps.go:129`), and one docstring that states this finding in
the code's own words — *"The kinds differ in what they do to a JAIL (a program gets a launcher,
a requires gets an assertion), not in what they ask of a host"* (`contributes.go:594-598`). The one
host-side artefact that does differ is the `yolo host --` wrapper `program` gets and `requires`
does not (row 5), and it installs nothing. **The noun/verb mismatch** — `program` is a thing,
`requires` is an act — is part of why the pair reads as incomparable; it is noted here and spent
nowhere else, because naming is downstream ([`provisioner-sets.md`](provisioner-sets.md)).

**F4 — The code already half-models the provisioner set, with one member.** The confinement
Profile is a vector of primitives, and one of its six is not a confinement primitive at all:

```go
// PrimBakedImage: a nix-built OCI image (the jail's package closure). A provisioning
// primitive, not a confinement one, but it travels with the jail notch and is absent
// below it.
```

(`internal/render/confinement.go:39-42`, re-read 2026-09-11.)
[§3.4](#34-not-orthogonal-to-confinement-the-provisioning-primitive-below-jail) argues the nix
profile is a candidate seventh (`PrimNixProfile`) and notes that `describe` switches on
`PrimBakedImage`'s **absence** to decide whether to print the profile. The provisioner set is
that idea completed: a per-environment vector with as many members as
[§1](#1-the-provisioner-inventory-per-environment) has rows, of which the tree today spells exactly
one. Whether it lives in the Profile or beside it is an implementation choice the model doc
delegates ([`provisioner-sets.md`](provisioner-sets.md)).

**F5 (a defect, not a finding) — the guest has two provisioners armed and unreachable.** The
macos-user run plan sets `YOLO_LSP_SERVERS` and `YOLO_MCP_PRESETS`, so every guest launcher is
baked with a non-empty server list and `SERVERS_ENABLED=1`; but `_refresh_servers` and
`_try_materialize` both open with `command -v yolo || return`, and the sandbox's `yolo` is staged
at `/var/yolo-jail/yolo` (`internal/macosuser/macosuser.go:139`), which is **not** on
`SandboxPath` (`:461-475`). Both no-op silently. Nothing in the tree records this, and the launch
warning at `loopholeinert.go:316-320` blames the missing bootstrap script alone. READ FROM CODE;
it is P3's failure mode exactly, and it is small enough to fix ahead of any ruling here.

---

## 2. The coverage matrix: which manager covers what

This table is the single most consequential fact the provisioner work rests on — it is what
makes P4 and P5 ([`provisioner-sets.md`](provisioner-sets.md)) true rather than merely
plausible, and it is the only reason *"prefer the system manager"* cannot be a rule. It was
**measured 2026-08-02** in the retired
[`noncontainer-nix-environment.md`](noncontainer-nix-environment.md), sourced from the pack-host
plan's [§8.3](../plans/pack-host-management-plan.md#phase-8--host-deps-for-the-fzf-case--scoped-closes-the-acceptance-test--shipped),
and is **cited, not re-measured, here** — the two docs carried two copies of it and this is the
survivor.

| manager | of the six agent packs | the detail that matters |
| :--- | :--- | :--- |
| `apt` | **0** | no Debian/Ubuntu release packages any of them, in any release |
| `dnf` | **1** | `pi-coding-agent`, and only in Rawhide |
| `pacman` | **2** | `openai-codex`, `opencode`; the other four are AUR-only, which `pacman -S` cannot install |
| `brew` | **6** | four are **casks** (`claude-code`, `copilot-cli`, `codex`, `antigravity-cli`) — a Brewfile defect fixed 2026-08-02 by the `brew-cask` hint key, **exercised on a Mac 2026-09-11**: `brew bundle check` parses the generated cask lines and reports misses ([M2](../plans/runbooks/mac-provisioner-measurements.md#m2--does-the-generated-brewfile-actually-apply-casks-included)) |
| `nix` | **6** | three are **`unfree`** (`claude-code`, `github-copilot-cli`, `antigravity-cli`), so a bare `nix profile install` refuses |

Four things follow, and the maintainer asked for the third to be checked.

- **On macOS the system-manager default is well-founded:** brew covers all six.
- **On Linux it fails:** a non-Arch host gets zero or one of six from its native manager. The
  default is therefore **conditional on the platform** — which reinforces P1 and P4 rather than
  weakening them, since "the environment" includes the distro.
- ⚠ **The premise *"there's no nix package"* is wrong, and the real reason is more useful.** nix
  covers six of six on every live platform; three refuse under a bare install because they are
  **`unfree`** — a licensing gate, not an absence. That widens the option space: `allowUnfree` is
  a decision a user can make once, where a missing package would have been a dead end. The jail
  and guest already handle it as warn-and-skip via `meta.available`
  ([`OQ-NX6`](provisioner-sets.md#decision-ledger)), and yolo deliberately never sets it on the user's behalf.
- **The sharpest sentence cuts against a naive *prefer the system manager* rule:** on Linux, nix
  is the only manager covering all six, so **the reproducible path and the only-path-that-works
  path are the same path**. That is a much stronger argument for a nix route than
  "reproducibility is nice", and it is why [`OQ-PS1`](provisioner-sets.md#OQ-PS1) is not a nicety.

### 2.1 `install_hints` and a nix profile are complementary, not competitors

The boundary is structural, not a preference. Inherited from the merged doc, verified 2026-08-02
and re-read 2026-09-11:

| | `install_hints` | a nix profile on PATH |
| :--- | :--- | :--- |
| Whose machine changes | **the user's**, permanently, in their manager's namespace | nothing outside `/nix/store` |
| Reproducibility | **none** — `brew install claude-code` is "whatever brew has today" | yolo's `flake.lock`, byte-identical per platform |
| Who runs it | the user (or Phase 4.3's confirm-gated offer) | yolo, as a build |
| Scope | machine-global | **process-scoped** if yolo launches; otherwise nothing |
| Works with no nix | **yes** — the entire point | no |
| Coverage of the six agent packs | **weak** (above) | **6/6 on all three live platforms** ([§3.7](#37-macos-vs-linux-coverage-freshness-and-the-traps)) |

**A nix profile does not make `install_hints` unnecessary, for three reasons**, and all three
survive the ruling:

1. **A user with no `/nix` gets nothing from it** ([§3.8](#38-what-if-the-user-has-no-nix)), and
   telling a brew user to install nix to get `copilot` is a worse experience than
   `brew install copilot-cli`. This is half of [`OQ-PS1`](provisioner-sets.md#OQ-PS1).
2. **`install_hints` answers a different question** — a pack's *host dependencies* generally.
   The pack-host plan's motivating case is `fzf` and `fd` for a file-suggestion pack, not agent
   CLIs. `fzf` is in nixpkgs, brew, apt and pacman alike; for that class a nix profile is
   overkill.
3. **The printed remedy is the floor the design deliberately guarantees** (env-manager
   [§3.5](yolo-as-environment-manager.md#35-dependency-provisioning-declare-once-check-once-hand-off-with-a-manifest):
   *"the composed manifest is always the floor"*). Anything driven is an *additional* offer, never
   a replacement for the floor — which is exactly how [`OQ-PS2`](provisioner-sets.md#decision-ledger) was ruled on
   2026-09-11 ([the drive-the-winner ruling](provisioner-sets.md#decision-ledger)): the
   offer was taken, the floor was not touched.

---

## 3. The nix resolver, in depth

**This section is the absorbed
[`noncontainer-nix-environment.md`](noncontainer-nix-environment.md)**, compacted. It is the only
resolver with depth like this, for a reason worth stating: it is the one that already works at
two notches and has no caller at the third, so it is where
[`OQ-PS1`](provisioner-sets.md#OQ-PS1) is decided. Its original analysis ran 2026-08-02 and was
re-verified 2026-08-23; dates below are the original measurements' unless stated.

> [!IMPORTANT]
> **Do not split this section back into a document of its own.** It was merged out of
> [`noncontainer-nix-environment.md`](noncontainer-nix-environment.md) on 2026-09-11, and the
> reason recorded in that retirement stub still holds: the nix doc *"was that doc's argument,
> reached early and stopped at one row of the table"*, and the two documents had been carrying
> **two copies of the coverage matrix** — the one in
> [§2](#2-the-coverage-matrix-which-manager-covers-what) is the survivor. Giving the nix
> resolver a file again re-creates exactly the duplication that merge fixed. It belongs here,
> with the rest of the evidence, as one row of [§1](#1-the-provisioner-inventory-per-environment)
> read all the way down; the model it feeds is in
> [`provisioner-sets.md`](provisioner-sets.md).

**Four terms, pinned**, because three of them are routinely conflated in conversation about nix.

- **A `devShell`** — a `mkShell` derivation, entered with `nix develop` / `nix-shell`, or dumped
  with `nix print-dev-env`. A **build environment**: the whole `stdenv` (a C compiler, GNU
  coreutils, `make`) plus ~100 build variables.
- **A `buildEnv`** (a *profile*) — `pkgs.buildEnv { paths = [ … ]; }`, a derivation whose output
  is one symlink tree union-ing exactly the packages named. **No** toolchain, **no** environment
  variables. Realized with `nix build --print-out-paths`, consumed by prepending `<out>/bin` to
  PATH. Nothing is "entered." This is what `packages.yoloNoncontainerPackages` is.
- **`nix profile`** — an imperative, mutable, per-user generation-tracked profile whose `bin` is
  on the user's PATH via their nix install. It records a locked flake URL per entry, so it is
  not *unpinned* — but the pin is whatever nixpkgs the registry resolved at install time, per
  entry, drifting independently. It is not yolo's `flake.lock`.
- **`nix shell`** (not `nix develop`) — the forgotten fourth mechanism; it behaves like a
  `buildEnv`, prepending exactly the requested store `bin` dirs (verified 2026-08-02:
  `nix shell nixpkgs#hello --command bash -c 'echo $PATH'` prepends **one** entry).

### 3.1 What is already solved, stated precisely

The most common way to waste effort here is to design something that exists. State column
verified 2026-08-23; the flake line numbers re-resolved 2026-09-11 (**the merged doc's
`flake.nix:1204`/`:1210` are stale — the file grew above them**).

| Capability | State | Where |
| :--- | :--- | :--- |
| A nix expression materializing `packages:` as a pure, toolchain-free profile | **SHIPPED** | `flake.nix:1636` `packages.yoloNoncontainerPackages` |
| …for **every** `eachDefaultSystem` system, Linux included | **SHIPPED** | `noncontainerResolved`, `flake.nix:416`; verified on `x86_64-linux` |
| Realizing it and putting `<out>/bin` on an agent's PATH, no container | **SHIPPED** | `internal/darwinpkg` → `internal/macosuser/orchestrator.go` |
| Per-package "no build on this platform" filtering, warn-and-skip | **SHIPPED** | `noncontainerSkipped` (`flake.nix:521`), `yoloUnavailablePackages` (`:1642`) |
| Pinning to yolo's `flake.lock` rather than the user's channel | **SHIPPED** (structural — it *is* the flake) | `flake.lock`, plus a second `nixpkgs-x86-darwin` input ([§3.7](#37-macos-vs-linux-coverage-freshness-and-the-traps)) |
| A target system that follows the machine instead of a constant | **SHIPPED 2026-08-05** | `darwinpkg.NativeSystem()`, `internal/darwinpkg/darwinpkg.go:46-76` |
| A **gcroot** on the realized profile | **SHIPPED 2026-08-05** — the root *is* the build's `--out-link`, so it cannot be skipped | `internal/darwinpkg/gcroot.go`, `darwinpkg.go:117-141` |
| The resolved profile **reported** to a human | **SHIPPED 2026-08-05** for `describe`, and **2026-09-14** for `check` — both now gated on `PrimBakedImage` being absent ([the model doc's build order](provisioner-sets.md), step 2) | `printPackageProfile` (`internal/cli/describe.go`), `sectionPackageProfile` (`internal/cli/check/section_packageprofile.go`) |
| `yolo check` verifying nix + `/nix` + trusted-user **on macOS** | **SHIPPED** | `cli/check/section_nix_probe.go`, `sections_macos_platform.go` |
| The same, on **Linux** | **NOT WIRED** — `IsMacOS`-gated | the `o.IsMacOS && hasNix` branch in `section_nix_probe.go`, and `sectionMacOSPlatform`'s call in `check.go` ([`OQ-NX9`](provisioner-sets.md#OQ-NX9)) |
| A **caller** for the profile at the `host` notch | **DOES NOT EXIST** | `yolo host apply` never touches nix (`cli/apply.go`: no `packages` handling) |
| `packages:` reported by `yolo host apply` / `check --at host` | **DOES NOT EXIST** — `packages` is not a pack *kind*, so the `FieldSet` census never sees it | `render/fieldset.go`, `cli/apply.go` ([`OQ-NX8`](provisioner-sets.md#OQ-NX8)) |

**So the honest framing was never "should yolo build a host nix environment."** It was: *yolo
already has one, for one notch on one platform, called by one backend.* Two of the original five
qualifiers are gone (the name, the GC root). What remains is F1 restated from the resolver's
side: **one notch, one backend, no non-macOS coverage, and no host caller.**

### 3.2 The four nix mechanisms compared, and why never a devShell

| | reproducible? | PATH pollution | must be *entered*? | mutates the user's machine |
| :--- | :--- | :--- | :--- | :--- |
| **devShell** (`nix develop` / `print-dev-env`) | yes (flake-pinned) | **severe — below** | **yes** (subshell) or source a 70 KB bash script | no |
| **`nix shell nixpkgs#x`** | yes | **none** (one dir prepended) | **yes** (subshell / `--command`) | no |
| **`buildEnv` + PATH prepend** (shipped) | yes (flake-pinned) | **none** | **no** — the caller sets PATH for the process it launches | no |
| **`nix profile add`** | per-entry, drifting | the user's whole profile | **no** — always on their PATH | **yes** — that is the point |

> [!WARNING]
> **The devShell is rejected in all forms, and the measurement is the argument.** Against this
> repo's own nearly-empty `devShells.default` (its `buildInputs` is literally `[ pkgs.just ]`),
> measured 2026-08-02: `nix print-dev-env` emits **22 PATH entries and 121 environment
> variables**. The 22 for a one-package shell are `patchelf`, `gcc-wrapper`, `gcc`, `glibc-bin`,
> `coreutils`, `binutils-wrapper`, `binutils`, **`just`**, then `stdenv.initialPath`'s
> `coreutils findutils diffutils gnused gnugrep gawk gnutar gzip bzip2 gnumake bash patch xz
> file`. **One of the 22 is what was asked for.** The 121 variables include `CC`, `CXX`, `AR`,
> `LD`, `NIX_CFLAGS_COMPILE`, `SOURCE_DATE_EPOCH`, `TZ` and `SHELL`.
>
> **It is worse at the host notch than it was on macos-user**, which is the new half of the
> argument: on macos-user the pollution lands on a sandboxed agent's PATH; at `host` it lands in
> **the human's own interactive shell**, beside their dotfiles, for the session. On macOS
> `stdenv.initialPath` puts **GNU** `sed`, `grep`, `awk`, `tar`, `find` ahead of `/usr/bin`,
> where those are BSD — `sed -i` alone differs — and `stdenv.cc` on `aarch64-darwin` is
> `clang-wrapper-21.1.8`, ahead of Xcode's. The flake already recorded this rejection at
> `flake.nix:1186-1194`; the host notch strengthens it.
>
> **Two caveats, so it is not overstated.** `nix develop --ignore-environment` with
> `stdenvNoCC` would reduce the dump — at which point you have hand-built a `buildEnv` with
> extra steps. And a devShell *does* carry one thing a `buildEnv` cannot: environment variables
> and `shellHook`s as part of the derivation. That is the whole of [`OQ-NX4`](provisioner-sets.md#OQ-NX4).

**`nix shell` is the interesting dark horse and it loses on two shipped facts.** It is the
cheapest correct mechanism for "run this command with these tools available" and needs no flake
output at all — but it is *only* a launcher, and it forfeits the two things that have since
become load-bearing rather than theoretical: a **single stable path** (one dir to symlink,
report in `describe`, and GC-root, where `nix shell` re-resolves per invocation) and
**`flake.lock` pinning** (`nixpkgs#x` resolves through the user's *registry*, not yolo's lock).
Keep it in mind as a simplification if the `buildEnv` ever proves more machinery than it earns.

### 3.3 `nix profile --profile <dir>`: the only candidate that reaches a user's own PATH

This is [`OQ-PS10`](provisioner-sets.md#OQ-PS10) — the *what does it leave behind* question, carved out of the old
compound [`OQ-PS1`](provisioner-sets.md#OQ-PS1)(c) on 2026-09-11 — and the reason the merged doc's own
[`OQ-3`](provisioner-sets.md#decision-ledger) folded into it.

`nix profile add` puts the binary on the user's PATH **forever, in every shell, with no
cooperation from yolo** — which is precisely what a user asking *"how do I install copilot"*
wants, and it is what `depcheck.installCmd` already prints for the `nix` manager. Its costs are
real: it **mutates the user's machine** (the same category `install_hints` is in, so it belongs
behind Phase 4.3's confirm, not a silent apply); its pin is per-entry and drifts from
`flake.lock`; `nix profile upgrade` only works for unlocked references; and a bare install
refuses the three `unfree` packages.

**A `--profile <dir>` variant is the genuinely interesting middle ground**, and nothing else in
the corpus considered it. `nix profile add --profile ~/.local/state/yolo/host-profile nixpkgs#…`
builds a **yolo-owned** profile the user's PATH does not see by default — a `buildEnv`-like
stable path *with* generations, rollback and `nix profile list` provenance. Verified working
2026-08-02 into a temp dir, and again on darwin 2026-09-11
([M3](../plans/runbooks/mac-provisioner-measurements.md#m3--does-nix-profile-install-refuse-the-unfree-agent-clis-on-darwin)).

> [!WARNING]
> **⚠ Retracted 2026-09-11: "it costs the imperative/declarative purity the env-manager's sealing
> story rests on… the closure table gains a row."** That sentence stood here and was the whole
> basis of the old [`OQ-PS1`](provisioner-sets.md#OQ-PS1)(c) conditional. It is **wrong**, and the correction is what
> made [`OQ-PS10`](provisioner-sets.md#OQ-PS10) answerable. Sealing's rule over the closure tiers is *"`--sealed`
> refuses the **Undeclared** tier and **reports** the Declared-impure tier"*, and its criterion is
> **nameability**
> ([`yolo-as-environment-manager.md` §3.3](yolo-as-environment-manager.md#33-apply---sealed-the-definition-binds-or-the-apply-fails)).
> A profile at a path yolo names is therefore **Declared-impure** — the tier `mise_tools` already
> occupies — so it is reported and never refused. Confirmed against the code: `applySealed` refuses
> exactly two inputs, a present `yolo-jail.local.jsonc` and outstanding capture overlay keys
> (`internal/cli/apply.go:800-833`, read 2026-09-11); it reads no toolchain and no store path.
> ⚠ Do not reintroduce a "sealing forbids a mutable profile" argument anywhere in this corpus —
> at `guest` and `host`
> [§3.3](yolo-as-environment-manager.md#33-apply---sealed-the-definition-binds-or-the-apply-fails)
> already puts the **whole toolchain** outside the sealed closure, and
> that is the design, not an oversight.

**Three costs that are real**, and they are what [`OQ-PS10`](provisioner-sets.md#OQ-PS10) actually weighs: it
**mutates state yolo then owns** (a thing to reap, report and reason about across versions); its
**pin is weaker** — MEASURED 2026-09-11, a `--profile` entry locks to whatever channel tarball
`nixpkgs#…` resolved to, not to yolo's `flake.lock` (M3); and a bare install still **refuses
the three `unfree` packages**, which on a flake needs `--impure` rather than an environment
variable (M3 again).

⚠ **One argument for it has since been taken off the table:** the declarative `buildEnv` now
GC-roots itself ([§3.8](#38-what-if-the-user-has-no-nix)), so *"it gcroots itself"* is no longer
a `nix profile` advantage.

### 3.4 Not orthogonal to confinement: the provisioning primitive below `jail`

The maintainer's original framing was that a nix env supplies *tools* while confinement supplies
*isolation*, so the two vary independently. That is true as a statement about the two **concepts**
and false as a statement about the **work**, for three reasons that compound. This is F1 and F4
from the resolver's side, and it is the merged doc's load-bearing finding.

1. **`guest` needs the identical mechanism, and it is unbuilt.** Phase 7 is *"a real home on the
   real filesystem, no image, an LSM boundary"*. **No image means no baked package closure**, so
   `confinement: guest` has by construction the same hole as `host`. macOS `guest` already
   answers it with the existing `buildEnv`; **Linux `guest` (7.2) has no package layer at all.**
   A host-notch nix env designed in isolation would be Phase 7.2's package layer under a
   different name. That is a shared dependency, not orthogonality.
2. **The notch decides whether a PATH-prepend has a consumer.** At `jail` and `guest` yolo
   launches the process, so a prepend works. At `host`, `yolo host apply` launches nothing —
   hence the `launch` and `env` kinds' refusals — though its sibling verb `yolo host -- <cmd>`
   does (shipped 2026-08-30). **The mechanism's viability is a function of the notch**, which is
   the definition of not-orthogonal.
3. **The primitive model already says so** — `PrimBakedImage`, F4's quoted comment. Since
   2026-08-05 the code acts on it without minting the primitive: `describe` prints the resolved
   profile line **iff `PrimBakedImage` is absent** from the notch's vector, because *"the
   question 'where does my toolset come from' has a nix-profile answer only below the jail
   notch"* (`internal/cli/describe.go:177-180`, re-read 2026-09-11). The *absence* of
   `PrimBakedImage` is already the live switch for the whole mechanism — the argument for a
   seventh primitive, made in the negative.

**Where the instinct *is* right, and it is not a small consolation.** The nix env is orthogonal
to the *enforcement* primitives — namespaces, Seatbelt, Landlock, separate-user. So the correct
statement is:

> A nix tool environment is orthogonal to the **enforcement** primitives and load-bearing for
> the **provisioning** primitive. It is not a peer of the dial; it is what fills the
> `PrimBakedImage`-shaped hole at the two notches that have no image.

**The practical consequence:** designing this as "a host feature" risks a Linux `guest` package
layer being built twice.

### 3.5 The isolation/environment split: what a non-container notch can reproduce

*"Mimic our in-jail envs more"* holds up, but only for about half of what the jail does, and
making the split precise is the merged doc's main contribution. Everything the jail gives its
agent, sorted by whether a nix closure plus a launch env could supply it off-container:

| What the jail provides | Class | Off-container? |
| :--- | :--- | :--- |
| `corePackages` / `fullPackages` (the baked set) | **environment** | ✅ a `buildEnv` of the same attrs, minus the Linux-only ones |
| `packages:` (user's extras) | **environment** | ✅ **already shipped** as `yoloNoncontainerPackages`, GC-rooted since 2026-08-05 |
| `mise_tools` | **environment** | ✅ already runs natively on macos-user (`ConfigureMisePrism`) |
| Env hygiene (`PAGER`/`GIT_PAGER=cat`, `EDITOR=cat`, `VISUAL=nvim`) | **environment** | ⚠️ **only for a process yolo launches.** In a shell yolo does not start this is a shell-rc edit, refused by name. And `EDITOR=cat` in a *human's* shell is hostile: it exists because an agent cannot drive an editor |
| `PATH` order | **environment** | ⚠️ same. macos-user already needs a **login-rc re-prepend** to survive macOS `path_helper` — evidence of how far you must reach to own a PATH you did not start |
| Blocked-tool shims (`grep -r`, `find`) | **hybrid** | ⚠️ mechanically yes; the design flags it opt-in — *"shims would land on your real PATH"* |
| `/lib` farm + `LD_LIBRARY_PATH` + nix-ld | **environment, Linux-container-only** | ❌ no darwin analogue ([§3.6](#36-the-lib-farm-has-no-darwin-analogue-worth-building)) |
| Composed agent config (settings, MCP, LSP, skills, briefing) | **environment** | ✅ **already shipped** — `yolo host apply` |
| Disposable home / overlay; credential omission; `resources`; `network`; `devices` | **isolation** | ❌ — and credential omission is *inverted* at `host`: your creds are the point |
| Agent autonomy | **policy, decided by confinement** | ✅ already correct — `host` renders the *guarded* posture |

**The line, in one sentence: a nix closure plus a launch env can supply everything in the
"environment" class for a process yolo starts, and nothing in the "isolation" class ever.**

**The blocked-tool shims are the one genuine hybrid**, and they look like environment while
behaving like policy: `grep -r` is blocked because a recursive grep wastes an agent's context,
which is an environment property — but at `host` the shims would land on **the human's** PATH,
and a human typing `grep -r` and being told to use `rg` is a different product. **If yolo
launches the host agent, the shims scope to that process and the dilemma dissolves** — a point
for the launcher answer over the rc-editing answer.

**The most valuable "mimic" target is none of the above.** All six agent CLIs are in nixpkgs for
all three live platforms while the jail installs them **lazily, at first use, via npm and
curl-to-shell**. So the jail does not get its agent CLIs from nix either, and a host nix env
would be **more reproducible than the jail** on exactly the axis the copilot question is about.
That inversion is [`OQ-PS6`](provisioner-sets.md#OQ-PS6)'s jail row.

> [!WARNING]
> **The merged doc's `via` census is STALE and is corrected here.** It said *"four npm
> (`opencode`, `pi`, `copilot`, `codex`), two installer (`claude`, `agy`)"* as of 2026-08-23.
> **MEASURED 2026-09-11** (`rg -n '"via"' packs/*/pack.json`): **three npm** (`pi`, `copilot`,
> `opencode`) and **three installer** (`claude`, `agy`, `codex`). `codex` flipped on 2026-09-04
> under [`OQ-PD13`](program-delivery.md#decision-ledger) (`dc640752`). Still **zero nix**, which
> is the part the argument rests on.

### 3.6 The `/lib` farm has no darwin analogue worth building

The clearest environment-vs-isolation boundary case, so it gets its own heading. Inside the jail,
non-nix binaries find shared libraries through a three-part Linux-only contraption: the `/lib`
symlink farm, the baked `LD_LIBRARY_PATH`, and **nix-ld** as the FHS interpreter at `/lib64`.

The darwin analogue would be `DYLD_LIBRARY_PATH`, and it does not work, for reasons that are not
yolo's to fix: **SIP strips `DYLD_*`** from the environment of any protected binary and across
`exec` of platform binaries, with no `dyld` equivalent of nix-ld; macOS has no `/lib64` FHS
interpreter to replace, since Mach-O binaries carry absolute `LC_LOAD_DYLIB` paths; and **there
is nothing in the repo that tries** (`rg DYLD` → two hits, both vendored `golang.org/x/sys`
constants — confirmed 2026-08-23). That silence is itself evidence: the problem the Linux farm
solves barely exists on macOS, where foreign binaries expect `/usr/lib` and macOS **has**
`/usr/lib`. **Explicitly out of scope** for any non-container provisioner work.

### 3.7 macOS vs Linux: coverage, freshness, and the traps

Platform coverage of the six agent CLIs, measured 2026-08-02 by eval from a Linux jail (nixpkgs
eval is platform-independent; only *building* needs the platform), against `flake.lock` rev
`241313f4`: **6/6 on `aarch64-darwin`, `aarch64-linux` and `x86_64-linux`** —
`claude-code`, `github-copilot-cli`, `codex`, `opencode`, `pi-coding-agent`, `antigravity-cli`,
of which `claude-code`, `github-copilot-cli` and `antigravity-cli` are `unfree`. That is better
coverage than any other manager and is the strongest single fact in favour of a nix route.

**Freshness is close but not equal, and it is a real trade rather than a footnote.** At the time
of measurement `codex` and `pi-coding-agent` matched npm exactly, `claude-code` matched the
version running in the jail, and `opencode` and `github-copilot-cli` lagged. A nix route means
"pinned, and a few days-to-weeks behind" — which for a CLI that ships daily interacts with the
packs' auto-updater-off keys. **The specific version numbers are stale and deliberately not
restated**; the freshness *argument* stands and is exactly what the
[shipped-default-order ruling](provisioner-sets.md#decision-ledger) settles.

> [!WARNING]
> **⚠ Retracted, 2026-08-23: "`x86_64-darwin` is dead, an Intel Mac gets nothing."** The
> observation was right and mattered more than the doc knew; the consequence is wrong. nixpkgs
> 26.11's throw did not merely deny an Intel Mac its packages — it took out **every host-side
> nix call on that system**, `nix eval .#installPrefix` included, which is the integration
> suite's staleness oracle. The macOS nightly went red for **29 consecutive nights** and the
> roadmap recorded it as *"nix is broken on that runner, not in our tree"* — the opposite of
> true. The fix (2026-08-18, `927fb9f5`) is a **second nixpkgs input used for `x86_64-darwin`
> alone**, `nixpkgs-26.05-darwin`, deliberately **not** used for `aarch64-darwin`. Re-measured
> 2026-08-23: an Intel Mac gets **5 of 6**, not 0 of 6; `antigravity-cli` is the skip.
>
> **Do not "simplify" the flake back to one nixpkgs input.** `pkgs` is evaluated for every
> system `flake-utils` enumerates, so a throw on any one system is a throw on every attribute —
> which is why a single dead platform took CI down for a month while looking like an
> infrastructure problem. Any future platform drop wants the same shape: a per-system input
> override, not a `packages`-level filter.

> [!WARNING]
> **Three traps in the unfree warn-and-skip fix ([`OQ-NX6`](provisioner-sets.md#decision-ledger)), each of which
> cost a measurement to find. Do not re-derive them.**
>
> - **`availableOn` alone can never catch unfree, and more platform probing will not help.** It
>   reads `meta.platforms`/`badPlatforms`; a licence is not a platform fact. The `tryEval` around
>   it *absorbs* the unfree assertion, so the package is reported available and the abort lands
>   later, inside `buildEnv` — **an eval that succeeds is not evidence the build will.**
> - **Test `meta.available`, not `meta.unfree`.** `meta.available` flips back to true under
>   `NIXPKGS_ALLOW_UNFREE=1`, so a user who deliberately opted in still gets the package instead
>   of a silent skip. **yolo does not set that variable on the user's behalf** — unfree is a
>   licence decision the user makes once, machine-wide, and slipping the override in would make
>   it for them silently. ⚠ **Amended 2026-09-11, measured on darwin
>   (M3): that flip requires an IMPURE eval.** A pure
>   flake evaluation does not read the environment at all, so `NIXPKGS_ALLOW_UNFREE=1` changes
>   nothing and the user's opt-in is invisible — `nix profile add nixpkgs#claude-code` refuses
>   identically with and without it. yolo's own image build is `--impure` already (for
>   `builtins.getEnv "YOLO_EXTRA_PACKAGES"`), which is why this bullet has held there; **any NEW
>   nix call added for a provisioner has to pass `--impure` for the opt-in to be honored**, and
>   that is a flag yolo would be choosing on the user's behalf, unlike the variable.
> - **The warning has to ride on the BUILD path**, not the skip list alone, whose separate eval
>   discards stderr. And reason precedence puts the **platform** case first, because
>   `meta.available` folds `unsupported` in with the licence checks — testing it first mislabels
>   a plain platform miss (`iptables` on darwin) as "broken or blocklisted". A collection or a
>   non-package is **fatal, not skipped**, and that test sits deliberately OUTSIDE the `tryEval`,
>   which would otherwise relabel a typo'd `packages` entry as "no `<system>` build".

**A `buildEnv`'s "no pollution" claim is really "no *undeclared* pollution."** A `buildEnv`
containing `gnugrep` still shadows `/usr/bin/grep` when its `bin` is prepended; the difference
from a devShell is **legibility, not effect**. On a Mac host that is the BSD-vs-GNU hazard
arriving by the front door instead of the back. Nothing warns today, on any path (confirmed
absent 2026-08-23). That is [`OQ-NX5`](provisioner-sets.md#OQ-NX5), and it is now also
[`OQ-P2`](../reference/macos-user-provisioning.md#why-it-is-this-way)'s problem one level down.

### 3.8 What if the user has no nix?

Three sub-cases, and only one is interesting. This is the half of [`OQ-PS1`](provisioner-sets.md#OQ-PS1) the merged
doc treated as terminal.

1. **No nix at all** (the common macOS/Linux user). The resolver is unavailable. `yolo check`
   already fails on `nix not found` and prints the download URL
   (`internal/cli/check/section_nix_probe.go:25`, read 2026-09-11) — but on Linux the `/nix`-exists
   and `nix store info` probes are `IsMacOS`-gated and never run ([`OQ-NX9`](provisioner-sets.md#OQ-NX9)). **Telling
   a brew user to install nix to get `copilot` is worse than `brew install copilot-cli`**, so
   `install_hints` stays the floor. ⚠ **Re-examined 2026-09-11: that objection is about RANKING,
   and it does not reach [`OQ-PS9`](provisioner-sets.md#OQ-PS9).** It says nix must not lead the macOS default order —
   which [`OQ-PS6`](provisioner-sets.md#OQ-PS6)'s leaning already honours — and says nothing about a user whose only
   covering provisioner *is* nix, which on a non-Arch Linux host is the ordinary case
   ([§2](#2-the-coverage-matrix-which-manager-covers-what)). The old
   [`OQ-PS1`](provisioner-sets.md#OQ-PS1)(b) leaning cited it for work it could not do.
2. **nix present, user not trusted.** Already handled as a warning, not a failure: a non-trusted
   user can still substitute from `cache.nixos.org`; being trusted is what makes
   `--accept-flake-config` consult yolo's cachix.
3. **nix present, but the closure must be *built* rather than substituted.** All six are prebuilt
   in `cache.nixos.org` for the three live systems, so in practice a download — but the failure
   mode (a from-source darwin build streaming for minutes) is documented for macos-user and
   identical here. `nixdiag.ParseDryRunWillBuild` already classifies it
   (build/substitutable/**inconclusive**, where inconclusive must never be read as a miss).

> [!WARNING]
> **Four things about the profile's GC root are deliberate. Changing any re-opens the defect it
> closed** (a user's next `nix-collect-garbage` deleting the realized profile out from under a
> running session — on a notch with no baked image that closure *is* the agent's toolset).
>
> - **The root IS the build's `--out-link`**, not a follow-up `nix-store --add-root`. The
>   two-step leaves a window in which a concurrent GC can collect a just-built closure. It also
>   makes rooting non-optional — failing to create the root fails the build, the right polarity
>   when the alternative is an agent executing from an unrooted closure
>   (`internal/darwinpkg/darwinpkg.go:117-141`).
> - **The leaf name is FIXED (`packages`), not keyed by `sha256(storePath)`.** `--out-link`
>   *replaces* the link in place, so a changed `packages:` retargets the one root and the old
>   closure becomes collectable — verified empirically. A content-keyed leaf would accumulate one
>   permanent root per package set ever configured, with no reaper: right for images, a slow disk
>   leak here.
> - **It lives in `build/package-roots/`, a SIBLING of `build/roots/`, on purpose.**
>   `prune.PruneOrphanImageRoots` enumerates every symlink under `build/roots` and reaps the ones
>   no recently-loaded image needs — a package root parked there would be swept by a routine
>   `yolo prune --apply`, unrooting the very closure it exists to pin.
> - **Registering it from inside a jail does not work, and that is fine.** `nix build --out-link`
>   does register the indirect root, and the host daemon then prunes it as stale, because the
>   link's path is the jail's spelling of a directory the host mounts elsewhere. Harmless today
>   because every caller is a non-container notch provisioning a real host home. Worth knowing
>   before someone reuses this from in-jail code and wonders why the root evaporates.

---

## 4. Facts verified, and how

Recorded so a later reader can tell measurement from inference. Everything in this table ran from
a Linux podman jail at `77190a2b`/`6eb7fe7f` on 2026-09-11, for
[`provisioner-sets.md`](provisioner-sets.md), before this evidence was split out of it.

| Claim | Status | How |
| :--- | :--- | :--- |
| No install hint is ever executed; no `brew`/`apt`/`dnf`/`pacman` is ever run | **MEASURED** negative | `rg -n 'exec\.Command\|syscall.Exec\|StartProcess'` over `internal/depcheck`, `internal/render`, `checkdeps.go`, `applyhostdeps.go` → none; every `.Remedy`/`.Fallback`/`SelfInstall` consumer is a print |
| `DetectManager` has no config path | **MEASURED** negative | `rg` for a manager-preference key over `internal/config`, `internal/cli` (non-test) → none |
| No build-time / `makedepends` / `optional` vocabulary exists | **MEASURED** negative | `rg -i` over `internal/packdecl`, `internal/depcheck`, `checkdeps.go`, `applyhostdeps.go`; every kind's field set read |
| `knownVias` is exactly `{npm, installer}` | READ FROM CODE | `internal/packdecl/contributes.go:378-381` |
| `program`/`requires` share one host path | READ FROM CODE | `contributes.go:599-604`; `applyhostdeps.go:61`, `:129`, `:187` |
| The remedy precedence, and its stated reason, are at `depcheck.go:147-151` | READ FROM CODE, **re-resolved** | the merged brief cited `:145-151`; `:145` is the docstring's last line and `:147` opens `REMEDY PRECEDENCE`. The quoted sentence is `:148-150` |
| `Fallback` exists *for* the user who prefers their manager | READ FROM CODE | `depcheck.go:150-151` — *"a user who prefers their package manager still sees the token"* |
| **Three** packs are `via: npm` and **three** `via: installer`; **zero** nix | **MEASURED** | `rg -n '"via"' packs/*/pack.json` → `pi`/`copilot`/`opencode` npm, `claude`/`agy`/`codex` installer. ⚠ Corrects the retired doc's 4/2 census ([§3.5](#35-the-isolationenvironment-split-what-a-non-container-notch-can-reproduce)) |
| Seven manifests under `packs/` declare `program` or `requires` | **MEASURED** | `rg -l '"kind": *"(program\|requires)"' packs/` → agy, claude, codex, copilot, opencode, pi, guardrails |
| The guest generates and runs launchers; npm is unprovisioned; refresh and materialise no-op | READ FROM CODE, then **MEASURED ON HARDWARE 2026-09-11** | `darwin.go:76-83`; `macosuser.go:139`, `:461-475`; `shims.go` launcher bodies — and M1 ran all five of the measuring host's launchers: the three `via: installer` ones install and run, the two `via: npm` ones fail loudly on a missing `npm` |
| `PrimBakedImage`'s comment calls itself a provisioning primitive | READ FROM CODE | `internal/render/confinement.go:39-42` |
| `describe` gates the profile line on `PrimBakedImage` being absent | READ FROM CODE | `internal/cli/describe.go:177-180` |
| Auto-capture cannot run on macos-user, for two independent reasons | READ FROM CODE | `internal/cli/run/autocapture.go:16-31` — an empty `CAPTURES_DIR`, and slice 6's relocation contract refusing until H2 |
| Any behaviour on macOS | **MEASURED 2026-09-11**, on a Mac — not from here | nothing in this table's own run could reach it: that jail is Linux, and a nested jail is structurally blind to the `macos-user` backend and to rootless podman (AGENTS.md carve-outs). The five-item Mac measurement list was what would close it, and it did — all five answered and recorded with the host and version that measured them ([`mac-provisioner-measurements.md`](../plans/runbooks/mac-provisioner-measurements.md)) |

**Drift found while verifying, reported not fixed:** `internal/cli/config_ref.txt:999-1000` still
says the launcher is *"last on PATH"* (B2 moved it second, 2026-09-04);
`internal/cli/packkinddocs_test.go:16` and `:37` speak of a "16th kind" and "15 kinds" against a
set of 19; the two stale sibling-doc cells named in [`provisioner-sets.md`](provisioner-sets.md); F5;
and the three stale citations in [§4.1](#41-inherited-from-the-retired-doc-with-its-own-dates).

### 4.1 Inherited from the retired doc, with its own dates

Carried, not re-run. Original measurements from a Linux jail on **2026-08-02** against
`flake.lock` rev `241313f4`, re-verified **2026-08-23** against `f13ff45a`. Three citations were
**re-resolved 2026-09-11** because the files grew; the claims themselves still hold.

| Claim | Date | Note |
| :--- | :--- | :--- |
| `yoloNoncontainerPackages` builds on `x86_64-linux`, and the darwin buildEnv *evaluates* from Linux | 2026-08-02 | the finding that reframed the work from "design a host nix env" to "give the existing one a caller" |
| devShell pollution: **22 PATH entries, 121 variables** for a one-package shell | 2026-08-02 | `nix print-dev-env --impure --json \| jq` on this repo's own `devShells.default` |
| `nix shell` prepends exactly one dir | 2026-08-02 | `nix shell nixpkgs#hello --command bash -c 'echo $PATH'` |
| All six agent attrs exist for `aarch64-darwin` and both Linuxes; three are `unfree` | 2026-08-02 | per-attr `nix eval` of `meta.platforms`/`meta.unfree`/`version`. **Versions deliberately not restated** — stale, and the freshness argument does not need them |
| Unfree warn-and-skip fires and `NIXPKGS_ALLOW_UNFREE=1` overrides it | 2026-08-23 | skip list is exactly `["claude-code","github-copilot-cli","antigravity-cli"]` on all three systems; `[]` on `aarch64-darwin` with the var |
| `x86_64-darwin` evaluates rather than throwing; an Intel Mac gets **5 of 6** | 2026-08-23 | the retraction in [§3.7](#37-macos-vs-linux-coverage-freshness-and-the-traps) |
| `nix profile` records a locked flake URL per entry; `--profile <dir>` works | 2026-08-02 | `nix profile add --profile <tmp> nixpkgs#hello; nix profile list --profile <tmp>` |
| No `DYLD_*` handling exists anywhere in the repo | 2026-08-23 | `rg DYLD` → 2 hits, both vendored `x/sys` constants |
| Nothing warns when a declared package shadows a system binary | 2026-08-23 | no shadow check in `internal/darwinpkg` or `internal/macosuser` |
| ⚠ **`flake.nix:1204` / `:1210`** for the two attrs | **STALE** | re-resolved 2026-09-11: `packages.yoloNoncontainerPackages` is `flake.nix:1636`, `yoloUnavailablePackages` is `:1642`, `noncontainerResolved` is `:416` |
| ⚠ **Four npm, two installer** packs | **STALE** | re-measured 2026-09-11: three and three ([§4](#4-facts-verified-and-how)) |
