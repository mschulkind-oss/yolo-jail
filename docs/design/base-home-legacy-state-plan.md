---
title: "Per-jail home skeleton — build sketch"
date: 2026-09-20
status: draft
tags: [plan, sketch, base-home, jail-home, storage]
summary: "Build sketch for the per-jail read-only skeleton that replaces podman's shared base home: the seed fixes that ship first, the skeleton builder and the writers it takes over, the tests and fixtures that move, and the traps found in the tree. No design decision lives here."
---

# Per-jail home skeleton — build sketch

**Status:** SKETCH, 2026-09-24 — incomplete, and unstable while questions are open. Rewritten
with the design; the previous quarantine sketch is superseded and lives in git history.

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

- **Root path:** blocked on [OQ-BH9](base-home-legacy-state.md#OQ-BH9). Under the leaning it is
  `filepath.Join(paths.AgentsDir(), cname, "home")`, and a `paths` helper keeps the spelling in
  one place.
- **Builder:** one function taking the root, the selected packs, the config, the `host_files`
  entries and `rt`. It runs for podman only, on the fresh-launch path, where `prepareWsState`
  and `prepareHostFiles` run today. `prepareHostFiles` runs later in `run.go` than
  `prepareWsState`, so either call the builder after both inputs exist, or split it into two
  calls that share the root. Reconcile behavior is blocked on
  [OQ-BH10](base-home-legacy-state.md#OQ-BH10).
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

Blocked on [OQ-BH13](base-home-legacy-state.md#OQ-BH13). Under the leaning:

- `noteLegacyBaseHome` and `legacyBaseHomeHatch` (`internal/cli/run/basehomedisclosure.go`)
  and their tests go, and `ensureStorage` returns only `EnsureGlobalStorage`'s error.
- `internal/cli/check/basehomedisclosure.go` stays, reworded.

## Step 4 — the Apple Container seed

Blocked on [OQ-BH12](base-home-legacy-state.md#OQ-BH12). It needs a Mac.

## Step 5 — docs and comments

- `jail-home.md`: reserving a name is not the same as creating a directory, and the base
  section changes. `storage-and-config.md` changes too.
- Rewrite the code comments that cite the pre-rewrite section numbers:
  `internal/basehome/classify.go`, `internal/paths/basehomecore.go`,
  `internal/storage/ensure.go`, `internal/cli/run/run.go`,
  `internal/cli/check/basehomedisclosure.go`.
- The comment in `internal/render/modes.go` says the jail home "is bind-mounted from
  `paths.GlobalHome()`". The comment in `internal/config/loopholeplacement.go` calls
  `GlobalHome` "the shared /home/agent backing tree".

## Traps found in the tree

- `PruneOrphanAgentStaging` removes the **whole** `AgentsDir/<cname>` once it is neither live
  nor tracked and is older than one hour. The skeleton goes with it, which is fine because no
  container holds it. `internal/cli/capturehost.go` also `RemoveAll`s `AgentsDir/<cname>` after
  a capture.
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
