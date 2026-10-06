---
title: "Companion implementation sketch: one keyed build for every pi extension"
date: 2026-10-05
status: draft
stage: SKETCH
next: "Built 2026-10-05 but for the background advance (step 3, which waits on the refresh-timing option) and the patched forks' share of step 2; the design's ledger, XB-D35 to XB-D42, records how"
---

# Companion implementation sketch: one keyed build for every pi extension

**Status:** 2026-10-05 — built but for two pieces, which the design's ledger names: step 1 whole
([XB-D14](pi-extension-store-builds.md#XB-D14), [XB-D41](pi-extension-store-builds.md#XB-D41)),
step 2 for extensions ([XB-D39](pi-extension-store-builds.md#XB-D39)), step 3 as a seam
([XB-D42](pi-extension-store-builds.md#XB-D42)), and step 4, which
[OQ-6](pi-git-extension-caching.md#OQ-6) (c) took ([XB-D35](pi-extension-store-builds.md#XB-D35) to
[XB-D38](pi-extension-store-builds.md#XB-D38)). The sketch below is what it was built from. It parks the implementation material of
[`pi-extension-store-builds.md`](pi-extension-store-builds.md), whose steps it follows; the design
wins on behavior, and nobody builds from this sketch.

## Step 1: the launcher

- **[XB-D14](pi-extension-store-builds.md#XB-D14), once confirmed, in one change: PG-D23 and the
  stamp beside the lock.** Confirmed and BUILT 2026-10-05
  ([XB-D29](pi-extension-store-builds.md#XB-D29)–[XB-D33](pi-extension-store-builds.md#XB-D33)
  record how); the rest of this bullet is the sketch it was built from. PG-D23 is part of `3ab39946f` on `held/pi-extension-store`, mixed with the store.
  Take its npm half by hand: in `packs/pi/pack.json`, drop the `.pi-shared-npm` `state` and its
  `shared_directory` hook, add `{kind:"hook", hook:"unshare_directory", from:".pi/agent/npm",
  at:".pi-shared-npm"}`, and set `refresh.lock` to `.pi/.yolo-update.lock`. Check which hunks of its
  tests are the npm half: `internal/entrypoint/unsharedir_test.go`, `shareddirhook_test.go`,
  `internal/basehome/decls_test.go`, `sharedstore_test.go`, `internal/packload/packproperties_test.go`
  (`TestMachineGlobalTierStaysNarrow`), `internal/storage/shareddirs_test.go`, and the docs
  [`jail-home.md`](../reference/jail-home.md) and
  [`macos-user-home-tiers.md`](macos-user-home-tiers.md).
- **The stamp beside the lock**, in the same change. `REFRESH_STAMP` and `REFRESH_SEEN_DIR` in
  `internal/entrypoint/prelaunchrefresh.go` derive from `REFRESH_STORE` (the lock's parent) instead
  of `STAMP_DIR`. Check that macos-user's baked paths still resolve under `env -i`.
- **The two tests that pin what this changes.** `TestShippedPiLauncherRefreshesItsExtensions`
  (`internal/entrypoint/prelaunchrefresh_test.go`) makes the `.pi-shared-npm` lock and asserts the
  stamp at `~/.cache/yolo-agent-stamps/refresh/pi.stamp`, and `3ab39946f` already had to change its
  lock path. It also drives the refresh through `--version`, which
  [XB-D24](pi-extension-store-builds.md#XB-D24) stops, so it needs a non-probe argument, and a
  second test pins that `--version` runs no refresh. `TestPackInstallsVersionsAndConfigures` and
  its helper `seedRefreshSeen` (`integration/agents_test.go`) build the stamp and seen paths this
  moves by hand.
- **The skip and the probe** are two new `packdecl` fields, on `Refresh` and on `program`; both must
  stay bash 3.2-safe in the template (`HAS_*` gates around every array expansion, as `HAS_REFRESH`
  does). The project file is relative to `$PWD`, the one non-home path any declaration has had. The
  probe skips the program's hourly update branch (not the cold install), `_refresh_servers`, the
  pre-launch refresh, the tree gate and the tree step, in every launcher template.
- **The gate first**: move `treeGateShell`'s splice in `npmLauncherTemplate`, the native template and
  `forkLauncherTemplate` to just after the update-mode exit. `TestTheTreeGate…`-style tests must pin
  the order in all three, failing when the splice moves back.
- **The failure throttle**: a `$REFRESH_SEEN_DIR/<key>.failed` stamp beside the seen marker.

## Step 2: the parallel advance

- `deliverTreesForLaunch` (`internal/cli/treedelivery.go`) and `buildForksForLaunch`'s patched loop
  (`internal/cli/forkbuild.go`) become one pool over owner keys. Two semaphores (checks, builds); the
  check/advance split must be visible to the pool, so the advance in `internal/cli/patchedadvance.go`
  exposes its check apart from its build.
- Per-key `bytes.Buffer` writers for `out` and `errw`, flushed in declaration order; the start line
  goes straight to `errw`.
- Cancellation: `req.Interrupt` already ends one advance's wait (PF-D57); check that a cancel reaches
  every in-flight `pidlock.Acquire` (its `Cancel` channel) and the build jail child.
- Measure first: a tree build of `pi-background-tasks` with no dependencies isolates the build jail's
  start (XB-D13's 5 s threshold).
- The build semaphore is 1 where the backend capability in `internal/cli/run/backendcaps.go` says
  Apple Container (XB-D10).

## Step 3: the update timing

Waits on [OQ-XB1](pi-extension-store-builds.md#OQ-XB1).

- `config.agentUpdatesProblem` accepts the two strings; `validateHostFloor` keeps its boolean check
  (it shares the shape rule today, so split the rule, not the reader).
- A timing reader beside `entrypoint.PackPolicyAllows`; the host's launch reads it from
  `config.AgentUpdatesWire()` before the fork-build slot.
- The background advance: a hidden `yolo internal` verb, started with `SysProcAttr{Setsid: true}`
  at normal priority (XB-D19 says why not `nice`), stdio on a log under yolo's host log directory.
  Its SIGTERM and SIGHUP handler kills each build's process group and runs `<runtime> rm -f <cname>`
  for each build jail; before `Store.Stage`, every build asks the runtime whether `cname` is running
  and clears the staging only on a known "no". It re-resolves packs through
  `config.ResolvePack`, never the launch's staged tree. Check how `yolo capture <pack>/<name>`
  resolves its selection and reuse that.
- Apple Container: the backend capability that already gates the fork slot's floor
  (`internal/cli/run/backendcaps.go`) decides XB-D21's branch.

## Step 4: under the capture route

Blocked on [OQ-6](pi-git-extension-caching.md#OQ-6).

- `packdecl.Contribution.IsPatchedExtension` (`internal/packdecl/patchedext.go:37`) requires
  `Patches != ""`; split it into "a built tree" (any `source` on `files`) and "patched". Every reader
  of the predicate needs reviewing: the fork fields' refusal (`internal/packdecl/fork.go`), the owner
  key, the lint, the footprint's claim and the recipe.
- The npm source: a new resolver beside packsrc's git check, writing the same check record; a Go
  node-semver range matcher with node-semver's own range cases as its table test.
- The fallback: where the contributing pack's `config-list` and posture-list entries become
  `agentcfg.ListContribution`s (`internal/agentcfg/listcontrib.go`), substitute by exact equality
  against the handed set from `YOLO_PATCHED_TREES`; the host render's insert path needs the same
  substitution.

## Landing the held store

Only under [OQ-6](pi-git-extension-caching.md#OQ-6) (a) or (b).

- **Replay, do not merge.** `git rebase --onto main a5665814 held/pi-extension-store` replays the six
  held commits; main holds re-committed copies of the branch's older line, so a plain rebase stops on
  the roadmap rebuild `897592d5`, and `git merge-tree --write-tree main held/pi-extension-store`
  reports 132 conflicting files from that stale base (MEASURED 2026-10-05).
- **The nine conflicts** of the replay (MEASURED with `git merge-tree --merge-base=a5665814f`):
  `internal/cli/internal.go` (the usage string: keep both verbs); `internal/entrypoint/forklauncher.go`
  and `shims.go` (the union of splices is refresh, trees, env, auth, model menu, tree gate, exec);
  `internal/entrypoint/packsurfaces.go` (one ctx constructor, keep main's `deriveLayerOver`);
  `internal/packload/forks.go` (a comment); `CHANGELOG.md` (move the entry into Unreleased and
  shorten it to the changelog rules); [`pi-git-extension-caching.md`](pi-git-extension-caching.md)
  and [its plan](pi-git-extension-caching-plan.md); [`pack-system.md`](../reference/pack-system.md).
- Then [XB-D16](pi-extension-store-builds.md#XB-D16)'s fixes, a re-read of pi 1.0.1 (the held docs
  read 0.87.1), a nested-jail run and the integration suite.
