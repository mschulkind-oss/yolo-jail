---
title: "Plan: one resolved config target"
date: 2026-09-17
status: accepted
tags: [plan, config, cli, notch, workspace, capture, disclosure, implementation]
summary: "Build hand-off for config-target-resolution.md, written against the tree: the files that change, the resolvers that already exist (jailHomeHostPath is the one the design calls missing), the traps, an eight-step order, and the five places the rulings under-specify once written against code."
vantage:
  status-chip: true
---

# Plan: one resolved config target

**Status:** DECIDED, 2026-09-17 — a SKETCH until today; now a **hand-off**, promoted against the
tree at `324ee848`. Nothing is built.

**Design:** [`config-target-resolution.md`](config-target-resolution.md) — the
[config target](config-target-resolution.md#3-one-resolved-target) is coined there, the failures
it closes are [§2.3](config-target-resolution.md#23-the-four-failures), the order is
[§8](config-target-resolution.md#8-what-i-would-build-in-order), and the rulings are the
[Decision Ledger](config-target-resolution.md#10-decision-ledger). **Seven of eight are ruled**;
[OQ-CR8](config-target-resolution.md#oq-cr8) is live, and it is step 6's premise rather than its
blocker.

**Precedence.** The design wins on behaviour. The tree wins on fact — a moved symbol below is
followed, and the commit says so. This file is advice, and the first thing to be wrong.

> [!WARNING]
> **The design's own `file:line` citations in [§2.1](config-target-resolution.md#21-two-predicates-resolved-independently) predate `202d4bff`** and are 12–15 lines
> short: `workspaceRoot` is at `configls.go:350` and `surfacesAreLocal` at `:403`. Read the code.

## Map

| Path | Change |
| :--- | :--- |
| `internal/cli/configtarget.go` | **new** — the resolver, the one resolution point, and the disclosure line |
| [`internal/cli/configls.go`](../../internal/cli/configls.go) | `workspaceRoot`/`surfacesAreLocal`/the four `prism*` builders become the resolver's internals; `composedFileExists` and the existence filter take the target |
| [`internal/cli/configdiff.go`](../../internal/cli/configdiff.go) | `readOverlayValue`, `readLastRenderKeys`, `userSidecarSurfaces`, `resetCapturePaths`, `resetBaselineMode`, `refuseHostSideWrite`, `captureSurface` read the target; `configDiff` loses the provenance block ([OQ-CR7](config-target-resolution.md#oq-cr7)); host-side jail-notch `reset` (step 7) |
| [`internal/cli/config.go`](../../internal/cli/config.go) | the verb dispatch resolves once and prints the disclosure; `renderSurface`'s host-layer read (step 6); `configUsage` grows `--at` |
| [`internal/cli/configcapture.go`](../../internal/cli/configcapture.go) | none — `jailHomeHostPath` gains a second caller, unchanged |
| [`internal/config/load.go`](../../internal/config/load.go) | **export the marker predicate** — one authority for the four workspace-config names, beside `resolveWorkspaceConfigPath` |
| [`internal/entrypoint/packsurfaces.go`](../../internal/entrypoint/packsurfaces.go) | export the host-layer read so preview, truncation and boot share one definition (step 6) |
| [`internal/packload/hostlayer.go`](../../internal/packload/hostlayer.go) | a fifth disposition, if [Blockers](#blockers) 1 rules that way |
| [`internal/cli/apply.go`](../../internal/cli/apply.go) | `--at`'s token parse extracted; `applySealed`'s two predicate calls ([Blockers](#blockers) 3) |
| `internal/cli/configtarget_test.go` | **new** — the resolution table and the disclosure assertions |
| `integration/hostfiles_test.go` | extend `TestHostFilesConfigLsAndReset` with the non-workspace cwd |

No new `cmd/` binary, no new store, no new file format, no migration — every path this touches is
derived at read time ([§4.5](config-target-resolution.md#45-defaults-and-triggers)).

## Reuse

- **`render.Target` for every store path, built from the RESOLVED workspace** — `SidecarDir`,
  `OverlayPath`, `LastRenderPath`, `ProvenancePath`, `SidecarFileMode`, `ArchivePath`
  ([`internal/render/target.go`](../../internal/render/target.go)). The shape to copy is
  `resetBaselineMode` (`configdiff.go:939`): `render.Jail(paths.Home(), <resolved ws>, nil)`, used
  for store paths only. At the jail notch it is byte-identical to today's `prismSidecarDir` —
  both spell `<ws>/.yolo/prism` — so step 4 changes behaviour **only** under `own`.
- **`jailHomeHostPath(workspace, runtime, surfacePath)`
  ([`internal/cli/configcapture.go`](../../internal/cli/configcapture.go)) is the resolver
  [§2.4](config-target-resolution.md#24-what-the-user-asked-for-and-why-it-does-not-exist) calls
  missing.** It already maps `~/.claude/settings.json` to the workspace's host-side backing,
  already branches on the backend (Apple Container binds `ws_state` at the home; podman strips the
  leading dot of the first segment), and already returns `ok=false` for a path this workspace does
  not back — which is [§4.1](config-target-resolution.md#41-degenerate-inputs)'s *"not resolvable
  at this notch"*, for free. It has exactly one caller today.
- **`detectListingRuntime(workspace)` (`internal/cli/commands.go:810`)** supplies that function's
  `runtime` argument for a read verb, the way `yolo ps` gets it.
- **`config.WorkspaceConfigBootPath(dir)`
  ([`internal/config/drift.go`](../../internal/config/drift.go))** for the `config-boot.json` half
  of the marker, and `resolveWorkspaceConfigPath`'s `.jsonc`→`.json` fallback for the other half.
  Both spellings of both names live there; do not write four literals in the CLI.
- **`paths.WorkspaceScopeBreach`
  ([`internal/paths/workspacescope.go`](../../internal/paths/workspacescope.go))** stays in the
  walk, and it is NOT made redundant by the ruled marker — it is the only thing that rejects a
  **pre-guard** stray `~/.yolo/config-boot.json`, which `EnsureWorkspaceStateDir` can no longer
  create but machines already carry (measured 2026-09-17).
- **`render.KindForNotch` / `render.SelectableNotches` (`target.go:249`)** for `--at`, and
  **`render.NotchUnbuilt("config diff")`** for the `guest` refusal — the same sentence `apply`
  prints, parameterised by verb.
- **`config.HostManagementDeclared`** for the ownership half; it gives absent and unreadable
  deliberately different answers, which a raw config read does not.
- **`entrypoint.PruneWorkspaceKeyed`** is the precedent for step 6: a boot-render internal
  exported so the CLI's truncation calls the one definition rather than a second one.
- **`paths.WorkspaceHomeState(ws)`**, `packload.WritableDirs` + `strings.TrimPrefix(dir, ".")`
  (`run/prepare.go:362`) and `paths.HomeFileRedirects()` are the mapping's inputs — reach them
  through `jailHomeHostPath` rather than re-deriving them.

## Traps

- **A workspace home root is NOT a `Home` swap.** `render.Jail(paths.WorkspaceHomeState(ws), …)`
  resolves `~/.claude/settings.json` to `<ws>/.yolo/home/.claude/settings.json` — the leading dot
  is stripped per writable dir, and three home-root files are redirected instead. That is why
  `jailHomeHostPath` exists and why nothing may hand-join this.
- **One config target, TWO `render.Target`s, and the split is load-bearing.** A Target's
  `Workspace` field feeds both `${workspace}` substitution and `SidecarDir`, and the config verbs
  need different values: the store wants the resolved workspace, a `render` preview wants the
  container literal (`containerWorkspace`, `config.go:276`). `localTarget`'s ⚠ (`config.go:380`)
  states this; collapsing them either moves the store to `/workspace/.yolo/prism` or previews host
  paths into a jail file.
- **The test seams are load-bearing, not hygiene.** `prismSidecarDir`, `surfacesAreLocal`,
  `hostProvenancePath` and `hostCaptureDir` are package vars **because tests stub them** — a bare
  runner has neither `YOLO_VERSION` nor a `/workspace` mount, and without the seam an in-jail
  `go test` would read (and `reset` would DELETE) the real `/workspace` sidecars. Four test files
  assign one directly and seven call the `withSidecarDir`/`withLocalSurfaces` wrappers
  (`configls_test.go`). Whatever replaces them keeps an equivalent seam.
- **`expandHome` has callers that are not surface paths.** Give the surface-path call sites the
  target; leave the helper alone.
- **The callee/call-site rule, which [§8](config-target-resolution.md#8-what-i-would-build-in-order)
  step 1 states as its test contract.** A test that resolves a target directly and asserts its
  fields passes with the resolution deleted from every verb. The test that counts runs the verb
  and reads its **disclosure line** — which is why steps 1 and 2 of the design are one commit
  here (see [Build order](#build-order)). `AGENTS.md` names five shipped instances of the other
  shape; adversarial mutation finds this class more often than it finds wrong logic.
- **`t.TempDir()` is a symlink on darwin** (`/var/folders/…` → `/private/var/…`) and this design
  walks the cwd upward. Mint resolved paths in the fixture — `workspacerootscope_test.go`'s
  `scopeWalkHome` is the shape — or it passes here and fails `check-macos`. Reproduce locally:
  `mkdir -p /tmp/real && ln -sfn /tmp/real /tmp/link && TMPDIR=/tmp/link go test -short ./...`.
- **`config drift` and `config dump` do NOT use `workspaceRoot()`** — they pass `""` to
  `config.WorkspaceConfigDrift` / `config.LoadConfig`, which default to the **bare cwd** with no
  upward walk. The marker ruling changes nothing for them. (The sketch this file replaces claimed
  the opposite; that line was never checked.)
- **One question id is used by two docs here** — [this design's](config-target-resolution.md#oq-cr1)
  and [`cache-relocation.md`](../plans/cache-relocation.md#-oq-cr1--is-cache_relocations-the-right-level-held)'s
  are both `CR1`. Link every id with its file.

## Build order

Follows [§8](config-target-resolution.md#8-what-i-would-build-in-order), with its steps 1 and 2
merged (see step 1) and its unstated presence case promoted to a step of its own (step 3). Every
step ends green on `just test-fast` and commits alone; **Proves** is the assertion that fails if
the step's call site is deleted.

| # | Step | Proves | Test lands in |
| :--- | :--- | :--- | :--- |
| 1 | **The resolver + the disclosure**, design steps 1–2 in one commit. `configTarget` (notch, workspace, store, home root, provenance, may-write, chosen-by), resolved once per invocation — `--at` is an input, so the seat is after argv is parsed and before the verb's first read; the retired predicates become its internals | two cwds, one jail: each verb's **printed** disclosure names a different workspace and says what chose it. Deleting the resolve call from a verb drops its line | `internal/cli/configtarget_test.go` (new), through `configRunW` — plus a census pin that no file but `configtarget.go` names the retired predicates |
| 2 | **The marker and the three unknown states** (design step 3): `.yolo/config-boot.json` OR a workspace config file; the breach check applied to the cwd itself; the *"else the cwd stands"* fallback replaced by the host target; store-absent → *"never rendered here"*; store-unreadable → say so, non-zero | `cd /tmp && yolo config ls` names the host target instead of an empty workspace answer; a fresh clone carrying only `yolo-jail.jsonc` resolves | `configtarget_test.go`; **rewrites** in `workspacerootscope_test.go` and `configls_test.go` (see [Ships with](#ships-with)) |
| 3 | **Presence at a workspace target**, via `jailHomeHostPath` + `detectListingRuntime` — closing [F2](config-target-resolution.md#23-the-four-failures)'s row inflation and printing *"not resolvable at this notch"* for a surface the workspace does not back | host-side `config ls` in a workspace lists what the jail rendered, with no four-row inflation | `configls_test.go`, beside `TestComposedFileExistsNeverClaimsAbsenceElsewhere` |
| 4 | **`diff`/`ls` read the target's store, and `diff` drops the provenance block** (design step 4 — both halves, one commit: *one verb, one subject, one home*). Provenance moves to `ls` | on an owned host, `diff` reports exactly the keys `reset` discards. The red state is today's *"No captured in-jail edits"* | `hostownedreset_test.go`, extending `hostResetFixture`; the moved block's new home in `configls_test.go`; the three provenance test files are **rewrites** (see [Ships with](#ships-with)) |
| 5 | **`--at` on the read verbs** (design step 5): the token shape extracted from `runApply`, validated through `render.KindForNotch`; `guest` refused by `render.NotchUnbuilt`; `--at jail` with no workspace refused by name | the read verbs and `apply` accept the same set and refuse `guest` with the same sentence | `configtarget_test.go`, plus the parser pin beside `apply`'s own tests |
| 6 | **`render`'s `host` layer through `Surface.HostSource`** (design step 6): the staged `/ctx` copy in a jail, *unavailable* host-side, and **nothing** under `assert`/`own`. ⚠ The jail half is blocked — [Blockers](#blockers) 1 | a preview of a `readsHost` surface composes the boot render's bytes; an unreachable layer is reported, not silently substituted | `confignotch_test.go` (it already owns "which target does render compose at") |
| 7 | **Host-side jail-notch `reset`** (design step 7, the only write): the two sidecars off the target, the surface file through `jailHomeHostPath`, truncation via the jail arm, refused while that workspace's jail is running | the fourth disposition of [§2.4](config-target-resolution.md#24-what-the-user-asked-for-and-why-it-does-not-exist)'s matrix exists, and a running jail refuses naming the in-jail verb | `hostownedreset_test.go`; the running-jail refusal needs an injected runtime probe, and `ps_test.go`'s `psDeps` is the shape |
| 8 | **Graduate**: fold the built behaviour into a system doc, retire the design, delete this file, move the roadmap row in the same commit | `uvx vantage-check docs/` clean | — |

**Expensive if late:** step 4's provenance move (every later test written against `diff`'s output
has to move with it) and step 2's marker (it changes what every fixture in the package means).

## Ships with

- **Unit, by case:** cwd inside a workspace / in a subdirectory of one / in a directory with no
  marker / `$HOME` carrying a pre-guard `.yolo/config-boot.json` / a workspace inside a workspace
  (innermost wins, disclosed by path) / `--at host` with `host_management` unset (stays `assert`,
  writes stay refused) / `--at guest` / `--at jail` with no workspace / a store dir that is absent
  versus unreadable. No agent is started anywhere.
- **Tests to REWRITE to the new behaviour, not repair until green.** Four fixtures build a
  workspace as a bare `.yolo` directory, which the ruled marker no longer accepts:
  `TestWorkspaceRootWalksUp` and `TestComposedFileExistsNeverClaimsAbsenceElsewhere`
  (`configls_test.go`), and both tests in `workspacerootscope_test.go`.
  `TestWorkspaceRootStopsAtABoundaryDirectory` needs care rather than a fixture edit: give the
  home's stray `.yolo` a `config-boot.json`, or it passes because the marker rejects the directory
  and stops measuring the breach stop it exists for.
  `configoverlay_test.go`, `configdiffretired_test.go` and `hostprovenancediff_test.go` all
  assert the provenance block inside `diff`'s output and move with it in step 4 — including the
  retired-key half, which is read out of the same record.
- **Integration:** extend `TestHostFilesConfigLsAndReset`
  ([`integration/hostfiles_test.go`](../../integration/hostfiles_test.go)) — it already drives the
  verbs host-side against a real jail's sidecars. Add the same commands from a non-workspace cwd,
  and assert the disclosure line names which home. That is the test that would catch the whole
  resolution breaking; the unit tests would not. `requireJail`, no `t.Parallel()`.
- **CLI surface:** `configUsage` ([`internal/cli/config.go`](../../internal/cli/config.go)) — the
  `--at` flag, and `diff`'s description, which today advertises the provenance block step 4
  removes. **Not `yolo config-ref`**: that documents config-FILE keys, so a read verb's flag is
  documented where it is enforced (`AGENTS.md`'s rule for `YOLO_*` dials, same reason).
- **Docs whose claims move:**
  [`config-ownership-and-promotion.md`](config-ownership-and-promotion.md)
  [§6.2](config-ownership-and-promotion.md#62-host-capture-and-the-privacy-ruling-it-has-to-answer-to) calls the CLI's
  `prism*` twins *"a standing hazard"* and predicts *"the next hand-built path is the next pair"* —
  the hazard landed in the readers, and step 1 removes it;
  [`../reference/config-migration-to-prism.md`](../reference/config-migration-to-prism.md) names
  what `diff` shows in two places.
- **Verification:** these verbs run **in-jail** as well as host-side, and step 6 touches the boot
  render — so exercise them in a nested jail from a throwaway workspace
  (`cd /tmp/yolo-nested && YOLO_REPO_ROOT=/workspace /workspace/dist-go/linux-$(go env GOARCH)/yolo -- bash`),
  never from `/workspace`, whose `.yolo/home` **is** this session's own home. Nothing here touches
  host reachability, so `AGENTS.md`'s nested-blindness carve-out does not apply.
- **Norms:** `just format` per commit; the pre-commit hook runs `just check-ci`; `uvx vantage-check`
  on every doc touched.
- **Cheap and yours:** the disclosure's exact wording (match the launch's
  `Flake source: <path> (<what selected it>)` shape and let review word it); the target's field
  spelling; whether the store arrives as a field or a method.

## Don't

- **Do not make the no-workspace answer a permission.** [OQ-CR2](config-target-resolution.md#oq-cr2)
  is explicit: it is a target, not a permission. `refuseHostSideWrite` and `hostOwnsSurfaces` are
  untouched, `capture` stays refused host-side even under `own`, and the integration test's
  `--force` stays — it is the product behaving correctly.
- **Do not retarget `promote`.** Host-side it reads the cwd's workspace store **by design** — its
  job is lifting a jail's captured keys into a pack — and its destination is user scope, which is
  not a notch. `refuseInJailPromote`'s two conditions both stay.
- **Do not hardcode `/workspace` in-jail.** That is alternative
  [B](config-target-resolution.md#6-alternatives-considered), rejected, and `workspaceRoot`'s
  docstring records what it cost: the nested-jail and integration cases read another workspace's
  sidecars and `reset` would have deleted them.
- **Do not invent a second notch vocabulary.** `--at`, `render.Kind`, and the same refusal
  sentences; no `yolo host config`, no `--jail`/`--host` pair.
- **Do not let a read verb create the store dir it found absent** — *"never rendered here"* is the
  answer, and [§4.4](config-target-resolution.md#44-one-writer-and-what-nothing-may-do) keeps the
  workspace store to two writers until step 7 adds the third deliberately.
- **Do not add the provenance block to `render --explain`'s output without checking**
  [OQ-CR7](config-target-resolution.md#oq-cr7)'s wording: it names `ls` **or** `render` as the new
  home, and `render --explain` already prints per-key layers for a composition it performs itself,
  which is a different fact from the recorded one.
- Delete this file with step 8 and move the roadmap row in the same commit.

## Blockers

Stop and ask on each: the tree forces a choice the rulings do not make. None blocks steps 1–5.

1. **The jail cannot learn `host_management`, so [OQ-CR6](config-target-resolution.md#oq-cr6)'s
   two-case table is not implementable at the jail notch as ruled.** The key is deliberately NOT
   inherited into a jail ([`internal/config/inherit.go`](../../internal/config/inherit.go), with a
   stated reason: in here the referent rebinds to the container's disposable home), so the boot
   render cannot tell whether the staged `/ctx` copy is the user's own file or yolo's previous
   output. The host knows, and there is already a channel for exactly this kind of fact — the
   launch's `YOLO_HOST_LAYERS` report, whose dispositions
   ([`internal/packload/hostlayer.go`](../../internal/packload/hostlayer.go)) are decided host-side
   and read in-jail. A fifth disposition ("staged, but it is yolo's own render — do not layer it")
   is the shape; inheriting the key is not, because that refusal is itself a ruling.
2. **There is no `--workspace` flag, and the launcher says there never will be**
   (`internal/cli/run/run.go:62`: *"there is no --workspace flag: cd into the project you meant"*).
   [§4.1](config-target-resolution.md#41-degenerate-inputs) and
   [§7](config-target-resolution.md#7-risks) both name it as the remedy for an over-strict marker
   and for `--at jail` with no workspace. Either that refusal names `cd` instead, or a new flag is
   in this design's scope.
3. **`yolo apply --sealed` shares both retired predicates and reproduces
   [F2](config-target-resolution.md#23-the-four-failures) in a verb the design never names.**
   `applySealed` (`apply.go:1133`) calls `workspaceRoot()` for its `yolo-jail.local.jsonc` test and
   `overlayKeyCount` for its capture test, so from a directory with no workspace it reports
   **"sealed"** — the confident empty answer, in the one verb whose whole job is refusing on
   undeclared input. Does it take the config target, or keep the bare cwd?
4. **The running-jail probe is tri-state, and the refusal's polarity is a ruling.**
   [OQ-CR4](config-target-resolution.md#oq-cr4) says refuse while that workspace's jail is running;
   it does not say what happens when the runtime cannot be queried. `yolo ps` treats
   "could not enumerate" as *not* an answer, and `AGENTS.md`'s reaper rule is that a sweeper which
   cannot ask declines — so a write should refuse. Confirm, and say whether `--force` reaches it.
   The probe itself is `run.Options.findRunningContainer` (unexported, method-bound) over
   `runtime.FromWorkspace(ws)`; a read-only twin beside `ps`'s deps is the cheap shape.
5. **[OQ-CR8](config-target-resolution.md#oq-cr8) is the only live question, and it is step 6's
   premise rather than its blocker.** Build step 6 under its leaning, (a); if it ever rules (b) —
   a jail composes from packs and captures only — `readsHost` disappears and step 6 has no
   subject. Nothing in steps 1–5 or 7 depends on it.
