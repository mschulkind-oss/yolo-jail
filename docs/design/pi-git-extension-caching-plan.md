# Companion implementation sketch: pi git extension trees

**Status:** SKETCH, 2026-09-25 — incomplete, and unstable while [OQ-5](pi-git-extension-caching.md#OQ-5)
is open. Rewritten for the redesign ([design §3](pi-git-extension-caching.md#3-the-design--share-content-never-state)).
Nobody builds from this; the design wins on behavior.

## 1. The revert first (interim, independent of the rest)

| File | Change |
| :--- | :--- |
| `packs/pi/pack.json` | Remove the `.pi-shared-git` `state` contribution and its `shared_directory` hook (the two entries after `.pi-shared-npm`'s hook) |
| `internal/entrypoint/shareddirgit_test.go` | Delete |
| `internal/packload/packproperties_test.go` | `TestMachineGlobalTierStaysNarrow`: drop `.pi-shared-git` from `want` and the "THE FOURTH" comment |
| `internal/cli/run/homeskeleton_test.go` | Optional: the absent-list entry stays true |
| Boot | Remove a `~/.pi/agent/git` link whose target is exactly `/home/agent/.pi-shared-git` ([design §3.10](pi-git-extension-caching.md#310-migration-from-what-c402dd43-shipped)); find whether the shared-directory hook machinery has a retirement path to extend |

`due_on_change` (packdecl, `prelaunchrefresh.go`, its tests) stays.

## 2. The trees (after the revert)

- **Store:** a new machine `state` contribution of `packs/pi`, `.pi-git-store`. Check how
  `storage.EnsureGlobalStorage`, macos-user's `DeriveDarwinHomeLayout` mirrors and the machine-tier
  tests (`TestMachineGlobalTierStaysNarrow`) pick up a new shared dir, as they did for `.pi-shared-npm`.
- **Mirror and fetch:** reuse `internal/packsrc` (`Store.Sync`, `resolveCommit`, the stamp and
  `BranchRefreshInterval`, `LaunchFetchTimeout`, the tag reset in `Refresh`). It is host-side today;
  it must run in the jail against the mounted store. Check that the in-jail `yolo` binary carries it
  and that `CleanGitEnv` covers the launcher's environment.
- **`yolo.finalize`:** a new Lua registration in `internal/agentcfg/luahook`, called by the stateful
  render after the fold and before the write (`internal/entrypoint/prism.go`'s surface composition).
  It needs `ctx.notch`. Decide where it sits relative to the selection lift and to capture's
  last-render record: the record must hold the post-finalize bytes, or every boot looks like a user
  edit.
- **The launcher step:** a second pre-launch declaration on pi's `program` (the tree resolver), beside
  `refresh`. The launcher template (`prelaunchrefresh.go`) runs it before exec and stops on failure.
  The declaration names the settings file, the pointer root, and the dependency-step spec (the
  `npmCommand` pointer and the default argv).
- **Recipe hash:** the argv as configured, plus `node --version` under it, plus GOOS/GOARCH of the
  jail.
- **GC:** a `yolo prune` class for `.pi-git-store`, with `yolo stores` listing it and the retired
  `.pi-shared-git`.

## 3. Tests the design's done conditions need

- Two workspaces, one commit: one tree, two pointers.
- Two pins: two trees, each workspace on its own.
- An upstream moved: the running workspace's tree is byte-identical, the relaunched one has a new tree.
- Concurrent first launch: one build, one announced wait, one tree.
- A broken dependency build: the launch stops before exec, naming the extension and the log.
- The settings file holds no `git:` user-scope entry in a jail, and holds them untouched at the host
  notch.
- `pi update --extensions` (a fake pi, never the real one) changes nothing under `.pi-git-store`.

Blocked on [OQ-5](pi-git-extension-caching.md#OQ-5): whether `npm:` entries join the same resolver
now, which decides whether the pi `refresh` declaration survives.
