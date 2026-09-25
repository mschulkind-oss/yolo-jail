---
title: "Per-jail home skeleton — build sketch"
date: 2026-09-20
status: draft
tags: [plan, sketch, base-home, jail-home, storage]
summary: "Build sketch for the per-jail read-only skeleton that replaces podman's shared base home: the seed fixes that ship first, the skeleton builder and the writers it takes over, the tests and fixtures that move, and the traps found in the tree. No design decision lives here."
---

# Per-jail home skeleton — build sketch

**Status:** SKETCH, 2026-09-25 — every design question this sketch builds is settled; one
found in the build is open ([OQ-BH15](base-home-legacy-state.md#OQ-BH15), which changes no step
here), and a second, [OQ-BH16](base-home-legacy-state.md#OQ-BH16), found making the reaper
work. **Every step BUILT 2026-09-25**: 1, 2 and 3 first, then 4, 4a and 5, then the review
fixes and follow-ups below. `integration/homeskeleton_test.go` passed in a nested, rootful jail
the same day, and **ROOTLESS in CI** on both arches (`ci.yml` run 36167524940 at `e56d871e`,
`integration (ubuntu-latest)` and `integration (ubuntu-24.04-arm)`, the same jobs' concurrency
test reporting `podman rootless=true`), its anywhere-under-`~` search included: the codex-only jail
found no Claude credential file. Still owed: a Mac run of step 4. Rewritten with the design; the previous quarantine sketch is superseded
and lives in git history.

**Where the build departed from this sketch** (2026-09-25):

- The builder is ONE call, `buildHomeSkeleton` (`internal/cli/run/homeskeleton.go`), made in
  `runContainer` after `prepareHostFiles`, so both inputs exist and no split was needed. It sits
  in its own `rt != "container"` arm right after the `assembleInput` literal, is handed
  `in.packs`, `in.cfg` and `in.hostFiles`, and writes `in.homeSkeleton` itself, so the skeleton
  and the argv binding it come from one value (moved there in review: the first cut assigned a
  local that the literal then read, and a dropped assignment passed every test). Each call
  makes a new `os.MkdirTemp` directory, timestamp-prefixed, under `paths.HomeSkeletonRoot(cname)`.
- The redirects are built right after the core entries and BEFORE every best-effort one, the
  order `EnsureGlobalStorage` used; the first cut built them last, so a pack `files` entry into
  `.gitconfig` made the fatal link fail every launch (found in review). A best-effort file
  mountpoint that finds a link or a directory in its place now warns instead of passing over
  it.
- Core's skeleton dirs are `paths.HomeSkeletonCoreDirs`, which is `paths.BaseHomeCoreDirs`
  without the pi pack's `.pi/agent`: in a skeleton that entry put a `~/.pi` in every jail,
  against DIR-BH1 (found in review). `BaseHomeCoreDirs` keeps it, because the legacy walk in
  `internal/basehome` must still treat an old base's empty `.pi/agent` as core's.
- `preparePackFilesGlobal` is gone; `packFilesSkeletonEntries` names the same targets for the
  builder. The single-file mountpoint list moved from `storage.fileMountpoints` to
  `paths.HomeFileMountpoints`. `storage.EnsureSymlink` had no production caller left and was
  deleted in the follow-ups.
- `internal/storage/basehomecoredirs_test.go` became `TestTheSkeletonHoldsTheCoreEntries` (in
  `internal/cli/run/homeskeleton_test.go`); `internal/storage/machinestore_test.go` pins what the
  machine store holds instead. `TestRunCallsEnsureStorage` moved there too, since the v2 layout
  migration still depends on that call.
- Step 1 could not land as its own commit: deleting `seedAgentDir` and re-pointing
  `prepareWsState`'s writes both edit `prepare.go`. The `SyncClaudeJSONSeed` half did.
- The `yolo check` report's wording and its printed `mv` live in `basehome.Report.CleanupLines`.
  The `mv` used to be printed only by the launch refusal.
- **Step 4** also stops creating podman's dot-stripped bind sources on `rt=container`
  (`preparePodmanBindSources` runs on podman only), since on that backend they were the stray
  `~/claude` and `~/npm-global` the design names; `go`, which has no dot to strip, is still
  created there. The seed's workspace path is one function, `claudeJSONInWsState`.
- **Step 4a** resolves validation's selection in `internal/config/selectedpacks.go`
  (`resolveSelectedPacks`): embedded entries by name, configured ones from the pack store,
  then the `needs` closure, as staging does. A reservation now names the pack that holds it.
  `writable_home_dirs` keeps reserving `.claude` in every workspace, as the first segment of
  core's redirect target `.claude/claude.json`; and `host_files`' surface-path reservation
  (`builtinSurfacePaths`) still covers every shipped pack, which the ruling's wording
  (directories) did not reach; that is [OQ-BH15](base-home-legacy-state.md#OQ-BH15).
- **Review fixes to steps 4 and 4a.** A `host_files` destination under a `writable_home_dirs`
  entry or a selected pack's shared dir is not staged (it was a second bind at one
  destination); Apple Container emits no `host_files` staging bind; the legacy migrations
  follow each backend's layout (`wsStateHomePath`); validation loads a filtered configured pack
  from a copy staged through its `only`/`exclude`; and `SyncClaudeJSONSeed` no longer reads or
  writes through a link, a hole the Apple Container path made wider (the jail's own
  `~/.claude.json`).
- **Step 5** re-points `internal/basehome`'s bare `§` numbers once, in the package header
  (`classify.go`), at `git show 030c8f52:docs/design/base-home-legacy-state.md`, the last text
  before the rewrite, rather than editing each of them.
- **Review follow-ups** (the design's [§8](base-home-legacy-state.md#8-build-order-and-done-conditions),
  last bullet, names the tests). The tracking file goes at each of the three ends of a launch
  once the container is known gone (`forgetGoneContainer`, `internal/cli/run/trackingcleanup.go`,
  which takes the workspace lock non-blocking and a tri-state `probeExistingContainer` split out
  of `findExistingContainer`). The builder removes its own partial directory on a fatal entry, and
  `runContainer` calls `discardUnheldSkeleton` before each return between the build and the
  container start. `ensureSharedDirSources` creates the selected packs' shared-dir sources
  before the argv, on both container backends. `resolveSelectedPacks` uses
  `packsrc.Store.ResolveExisting`. `packload.EmbeddedWritableDirs` and
  `internal/config/zz_probe_test.go` are deleted with `storage.EnsureSymlink`.

**Design:** [`base-home-legacy-state.md`](base-home-legacy-state.md). **Precedence:** the design
wins on behavior; this file is the first thing here to be wrong.

**Reads with:** [`base-home-legacy-state.md`](base-home-legacy-state.md) (the design this
sketches) and [`jail-home.md`](../reference/jail-home.md) (the home layout it changes).

---

## Step 1 — the seed fixes (every backend, ship alone)

- Delete `seedAgentDir` (`internal/cli/run/storagehelpers.go`) and its loop in `prepareWsState`
  (`internal/cli/run/prepare.go`). ⚠ No test references `seedAgentDir` today, so nothing fails
  when it goes; the new test is the one that proves it is gone. Plant a file in
  `<state>/home/.copilot` and assert it does not reach `wsState/copilot`.
- In `SyncClaudeJSONSeed` (`internal/storage/claudejson.go`), make the forward loop iterate
  `claudeJSONSeedKeys` instead of `seedData.Keys()`. Test: a seed carrying `projects` and
  `oauthAccount` forwards only `oauthAccount`, and a logged-in workspace still back-propagates.

## Step 2 — the skeleton

- **Root path** ([OQ-BH9](base-home-legacy-state.md#OQ-BH9)):
  `filepath.Join(paths.AgentsDir(), cname, "home")`, and a `paths` helper keeps the spelling in
  one place.
- **Builder:** one function taking the root, the selected packs, the config, the `host_files`
  entries and `rt`. It runs for podman only, on the fresh-launch path, where `prepareWsState`
  and `prepareHostFiles` run today. `prepareHostFiles` runs later in `run.go` than
  `prepareWsState`, so either call the builder after both inputs exist, or split it into two
  calls that share the root. Per [OQ-BH10](base-home-legacy-state.md#OQ-BH10), each fresh
  launch builds a new skeleton directory and never edits an old one; old ones go with the jail's
  `AgentsDir` entry.
- **Re-point:** move the home loops, `fileMountpoints` and `HomeFileRedirects` out of
  `storage.EnsureGlobalStorage`. It keeps the storage dirs, `EmbeddedSharedDirs` and the
  credential migration. Also re-point the `GlobalHome` writes in `prepareWsState`
  (`writable_home_dirs`, skills destinations, briefing `touchFile`s), in
  `preparePackFilesGlobal` and in `prepareHostFiles`. `podmanBaseMounts` binds the root.
- **Tests that move:** the golden argv in `assemble_test.go` (the `/home/agent:ro` line).
  `hostfiles_test.go` asserts the `GlobalHome` link and mountpoint. `packfiles_test.go` has
  `TestPrepareWsStateCreatesSkillsAndBriefingMountpointsInGlobalHome`.
  `internal/storage/basehomecoredirs_test.go` pins the core dirs in `EnsureGlobalStorage`, and
  moves to the builder. `internal/storage/shareddirs_test.go`, `sharedtier_test.go` and
  `sharedtiermigrate_test.go` should stay green unchanged, because the shared dirs do not move.
- **New tests:** every `-v` destination directly under `/home/agent` in the golden argv has a
  skeleton entry or sits inside another bind. A call-site test fails if the builder's call in
  the launch path is deleted. Attach leaves the skeleton unchanged. A fresh launch leaves
  `<state>/home` byte-identical outside the shared dirs, the seed and the credential migration.

## Step 3 — the refusal and the check report

Per [OQ-BH13](base-home-legacy-state.md#OQ-BH13), in the same change as step 2:

- `noteLegacyBaseHome` and `legacyBaseHomeHatch` (`internal/cli/run/basehomedisclosure.go`)
  and their tests go, and `ensureStorage` returns only `EnsureGlobalStorage`'s error.
- `internal/cli/check/basehomedisclosure.go` stays, reworded.

## Step 4 — the Apple Container seed

Per [OQ-BH12](base-home-legacy-state.md#OQ-BH12): runtime-aware seed paths in `prepareWsState`
(on `rt=container`, sync `wsState/.claude.json` and create the dotted dirs). It needs a Mac.

## Step 4a — reservation covers only the selected packs

Per [OQ-BH14](base-home-legacy-state.md#OQ-BH14):

- `reservedHomeDirs` and its segment set (`internal/config/writablehome.go`) and
  `hostFileWritableRoots` (`internal/config/hostfiles.go`) take the selected packs instead of
  `packload.Embedded*`. Validation already has the selection.
- Gate `StagingFor` on the selection the same way, so an entry under an unselected pack's dir is
  an ordinary path.
- Tests: `writable_home_dirs: [".codex"]` passes in a claude-only workspace and is refused once
  codex is selected; a `host_files` entry under `~/.codex/` works in a claude-only jail.
- Rewrite AGENTS.md's "`packload.Embedded*` is deliberately NOT selection-gated" bullet for
  these lists in the same change.

## Step 5 — docs and comments

- `jail-home.md`: reserving a name is not the same as creating a directory, and the base
  section changes. `storage-and-config.md` changes too.
- Rewrite the code comments that cite the pre-rewrite section numbers:
  `internal/basehome/classify.go`, `internal/storage/ensure.go`, `internal/cli/run/run.go`,
  `internal/cli/check/basehomedisclosure.go`. (`internal/paths/basehomecore.go` was rewritten
  in step 2's review.) `classify.go`'s `ProvisionedDir` comment also still says `EnsureGlobalStorage`
  creates the core dirs because the base is bound `:ro`; neither is true any more.
- The comment in `internal/render/modes.go` says the jail home "is bind-mounted from
  `paths.GlobalHome()`". The comment in `internal/config/loopholeplacement.go` calls
  `GlobalHome` "the shared /home/agent backing tree".

## Traps found in the tree

- `PruneOrphanAgentStaging` removes the **whole** `AgentsDir/<cname>` once it is neither live
  nor tracked and is older than one hour. The skeleton goes with it, which is fine because no
  container holds it. `internal/cli/capturehost.go` also `RemoveAll`s `AgentsDir/<cname>` after
  a capture.
- **Found while building: a skeleton can outlive its jail by a long time.** A tracking file was
  removed only by a stale-container removal (`removeStaleContainer`), `yolo ps`
  (`PruneStaleTrackingFiles`), `yolo check`'s cleanup and a capture; a `--rm` container that
  exited normally left it. The reaper keeps every tracked name, and `touchAgentStagingDir`
  refreshes the entry's mtime on every launch. So a workspace's `AgentsDir/<cname>` went
  unreaped for as long as the workspace was in use. **Fixed in the follow-ups:** the launch
  removes the tracking file once the container is known gone. What is left, a workspace used
  alone on its machine, whose own launches' housekeeping always keeps its name, is
  [OQ-BH16](base-home-legacy-state.md#OQ-BH16).
- `jailcontent.PrepareSkills` clears only the contents of `skills-*` under `AgentsDir/<cname>`,
  so it never touches a sibling `home/`.
- `EnsureGlobalStorage` has two callers: `ensureStorage` and `internal/cli/check/check.go`.
- `prepareHostFiles` is gated on `rt != "container"`. `preparePackFilesGlobal` skips
  `container` and `macos-user`. `macos-user` returns in `run.go` before `prepareWsState`.
- The workspace flock's error path is fail-open (`internal/cli/run/flock.go`), so reconcile must
  not remove anything when the lock was not acquired.
- **Verification:** a nested jail covers the argv, the mountpoints and the EROFS root. It
  cannot cover rootless ID mapping, because it forces `--userns=host`. That check needs a real
  rootless host or CI, reported with `podman info --format '{{.Host.Security.Rootless}}'`.
