---
title: "Plan: split the load sentinel's two consumers (OQ-LS1/LS2)"
date: 2026-09-08
status: accepted
tags: [prune, images, storage, nix]
summary: "The GC-root reaper becomes a pure age policy (a week, no liveness input), a declined sweep stops being silent, and the liveness gate the root reaper gives up has to reappear in the one place it was actually protecting something — the bounded store GC that runs later in the same `yolo prune` invocation."
vantage:
  status-chip: true
---

# Plan: split the load sentinel's two consumers

**Design:** [`the-load-sentinel-is-not-a-liveness-oracle.md`](the-load-sentinel-is-not-a-liveness-oracle.md) ·
**Status:** ready · Written against `ba072719`, 2026-09-08.

**Precedence:** the design wins on behavior; the tree wins on fact; this file is advice and is the
first thing to be wrong. Never twist code to match it — correct it in the commit.

**Scope.** Consumer B shipped in `feddc5e0` (the `podman ps` veto, and no more `rmi -f`). This
plan builds [OQ-LS1](the-load-sentinel-is-not-a-liveness-oracle.md#111-decision-ledger) (Consumer A becomes
age-only) and [OQ-LS2](the-load-sentinel-is-not-a-liveness-oracle.md#111-decision-ledger) (a decline is an error
where the user asked, loud where it is impossible, silent where there is nothing to do).
[OQ-LS3](the-load-sentinel-is-not-a-liveness-oracle.md#111-decision-ledger)'s mechanism is step 6, gated — see
Blockers. The `_Leaning:_` lines in the design are history; all three rulings went past them.

## Map

| Path | Change |
| :--- | :--- |
| `internal/prune/imageroots.go` | `PruneOrphanImageRoots` drops `protected` + `liveKnown`; guards #1/#2 and their (false) comment go with them; age is the whole policy |
| `internal/prune/prunecmd.go` | call site `:589` drops both args and its `!live.Known` branch; `imageRootOlderThanSeconds` `:215` → a one-week `time.Duration`; images section reports *why* it declined; `Run` `:778` returns non-zero on a decline; `--nix-gc` gate `:733` gains running-container evidence |
| `internal/prune/probes.go` | `PruneOldImages` reports its decline reason (three identical `return []string{}` at `:266`, `:287`, `:291`) |
| `internal/prune/nixgc.go` | `UnrootedProtectedPaths` (or a sibling) takes the running containers' image refs |
| `internal/prune/autoreap.go` | `AutoReapOldImages`' `ran bool` cannot say *declined*; give it the reason |
| `internal/cli/run/autoreapimages.go` | warn loudly on a decline; stay silent on the debounce and on nothing-to-do |
| `internal/prune/imageroots_probe.go` | `ProtectedImagePaths` doc comment: no longer A's evidence (still B's and the GC gate's) |
| `internal/cli/commands.go` | `pruneUsage` `:118` — exit status is no longer always 0 |
| `integration/prune_test.go` | new — see *Ships with* |
| `docs/plans/storage-lifecycle.md` | the *Landed* header and the [§1](../plans/storage-lifecycle.md#1-root-the-running-images-closure--first-everything-depends-on-it) checkbox both say prune "reaps roots no live jail needs (tri-state fail-safe)" |
| `docs/design/minimal-disk-footprint.md` `:228`, `docs/design/disk-levers-and-backfill.md` `:215` `:248` | "triple-guarded (… an LRU-10 protected set, a 3600 s age floor)"; "reaps once their images age out of the LRU" |

## Reuse

- `imagesInUseByRunningContainers(rt, run)` (`probes.go:440`) — the runtime authority, already
  built and already tri-state. Step 3 needs it too; do not write a second `ps` probe.
- `tagOf(ref)` (`imageroots_probe.go`) + `image.ImageStoreKey` + `image.ImageRootsDir()` /
  `ImageRootLink` (`internal/image/gcroot.go`): a running container's ref is
  `localhost/yolo-jail:<sha16-of-storePath>` and the root file is `roots/<that same sha16>`, so
  container → root is a string split, with no reverse lookup and no store query. That is the whole
  mechanism step 3 needs.
- `mkRoot(t, rootsDir, name, target, age)` (`imageroots_test.go:17`) back-dates the **symlink's
  own** mtime via `unix.Lutimes` (`os.Chtimes` follows the link). Reuse it for the week boundary.
- `baseOpts(t)` + `stubExec(mapping, calls)` + `hasLine` (`prunecmd_test.go:11-70`) — the
  whole-command harness. `pruneRunOnTemp` (`internal/cli/prunecaptures_test.go:110`) is the same
  thing one layer up, through the real front door.
- `internal/cli/run/autoreapcallsite_test.go` — the AST call-site pin, the repo's answer to
  AGENTS.md's "a test that pins the callee while the call site is unpinned is not a test". Copy its
  shape if step 5 adds a call site.
- Exit codes: `internal/cli/configdrift.go` — `1` plain failure, `2` usage, `3`/`4` semantic
  states. Advice: use `1`; the number is cheap and yours.

## Traps

- **`nix-store --add-root` refreshes the link's mtime even when the link already points at the
  same store path** (MEASURED in-jail 2026-09-08), and `AutoLoadImage` calls `RegisterRoot` on
  **every** success, not only on a load (`autoload.go`, just after `AddLoadedPath`). So the root
  mtime already means *last launch that used this image* — exactly what the week policy wants, with
  no new bookkeeping. **Constraint:** it does not mean "when the root was created", which is what
  the current comment ("grace window… in-flight `yolo run`") claims. A jail up for more than a week
  without relaunching does not refresh it and is reaped — by ruling, not by accident.
- **THE STORE-GC HOLE, and the reason A's veto cannot simply vanish.** `assemble.go:390` bind-mounts
  `/nix/store:ro` into the jail, so a running jail's `/bin/*` resolve through the **host** store —
  which is how the 2026-07-22 incident in `docs/plans/storage-lifecycle.md` killed 235 of 467 `/bin`
  symlinks under a live jail. `yolo prune --nix-gc --apply` reaps image roots at `:589` and then runs
  the bounded store GC at `:733` **in the same invocation**, and the GC's only guard is
  `UnrootedProtectedPaths` over the sentinel's LRU-10. A live jail that has both aged out of the LRU
  and passed the week is then unrooted *and* invisible to the gate. **Constraint:** liveness moves
  into the GC gate (step 3); it does not stay in the root reaper.
- **`PruneOldImages` must keep its sentinel tag veto.** The launch-path reap fires before this
  launch's container starts (`run.go:803`), so `podman ps` cannot see this launch's own image —
  `ProtectedImageTags` is the only thing protecting it. `autoreapcallsite_test.go`'s ORDER assertion
  encodes this; its stated reason needs rewriting, its assertion does not.
- **`ProtectedImagePaths` has two other consumers** — `ProtectedImageTags` and
  `UnrootedProtectedPaths`. Deleting it with A's argument breaks both.
- **Apple Container has never answered either query.** Nothing in the tree passes `--format` to
  `container` (every listing goes through `runtime.ParseContainerLs*`), so `container ps --format` /
  `container images --format` fail and `PruneOldImages` has always returned empty there, reported as
  a bland "none". Turning a decline into an error makes `yolo prune` exit non-zero on every Apple
  Container host. See Blockers.
- **A fresh machine has no sentinel**, so `liveKnown==false` is also the nothing-to-do case
  (`ProtectedImageTags`' own comment says so). `TestDryRunEmptyEnv` and
  `TestPruneWiresTheCaptureReceiptReaderAndReapsThroughIt` both assert `rc == 0` against an empty
  `BuildDir` — they are the guard for LS2's third row, keep them green.
- `imageRootOlderThanSeconds` is `float64` seconds only because its two siblings feed
  float-taking helpers; `PruneOrphanImageRoots` takes a `time.Duration`. Advice: spell the week as a
  Duration and delete the conversion at the call site — one fewer unit to get wrong.
- `prunecmd.go:231` says *"always 0 — prune never fails the process"*. That sentence is the contract
  step 4 changes; fix it in the same commit.

## Build order

1. **Comments and doc claims only** — `imageroots.go`'s "always the most recent sentinel entry"
   (design [§5.1](the-load-sentinel-is-not-a-liveness-oracle.md#51-the-premise-that-is-false)),
   plus the three docs in the map. No behavior. → `just test-fast`
2. **A becomes age-only.** Drop `protected`/`liveKnown`, week-long `olderThan`, call site un-gated.
   → `go test ./internal/prune`
3. **Close the hole step 2 opens** — the `--nix-gc` rooting confirmation asks the runtime which
   images have containers on them and refuses while any of those closures is unrooted. This is the
   step that gets expensive late: land it with step 2 or `--nix-gc --apply` is briefly worse than
   before. → `go test ./internal/prune`
4. **LS2, manual path.** A decline carries its reason; `Run` returns non-zero; nothing-to-do stays
   silent and 0. → `go test ./internal/prune ./internal/cli`
5. **LS2, automatic path.** `AutoReapOldImages` distinguishes debounced / declined / ran; the
   launch prints a real warning only for a decline. → `go test ./internal/cli/run`
6. **LS3's mechanism — gated, do not start it without a ruling.** See Blockers.

## Ships with

- **Unit rewrites, not repairs.** `imageroots_test.go` cases (1) unknown-liveness-declines and (2)
  protected-root-spared assert the guards being removed — delete them. New cases: 6d survives, 8d
  reaped, and **an 8d root whose target is still in the sentinel is reaped** (that one fails if
  `protected` ever sneaks back). `prunecmd_test.go:377`
  `TestSweepsDeclineWhenLivenessUnknown` claims the decline line "appears for
  relays/agent-staging/image-roots" — image-roots no longer declines.
  `internal/cli/run/autoreapimages_test.go:101` asserts silence on unknown liveness; it must assert
  the warning.
- **New unit cases.** GC gate: a live container on `localhost/yolo-jail:<key>` with no `roots/<key>`
  → the GC section declines naming the container (mutation check: delete the new evidence and this
  must fail). LS2: `podman ps` unreadable *with* image rows present → error line and `rc != 0`;
  unreadable with nothing to sweep → `rc == 0`.
- **Integration** — `integration/` has no prune coverage at all. Add `runYoloCLI(t, ws, "prune")`
  (**dry-run only**; `--apply` would delete the suite host's real images) asserting exit 0 and no
  error line against a healthy podman. Nothing in the unit suite can catch "the new non-zero path
  fires on a healthy machine": it stubs the runtime.
- **Verification note.** No nested jail: AGENTS.md's rule targets launcher/argv changes, and a
  nested launch here would run a real reap against a real store — `YOLO_NO_AUTO_IMAGE_REAP=1` exists
  for exactly that. Host-side `yolo prune` dry-run is the honest check.
- **CLI surface.** `pruneUsage` gains an exit-status line; `--no-image-roots`' one-liner should say
  the reaper is now age-based. No new flag and no config key — the week is a constant (the design
  names neither).
- **The design doc, when this lands.** [§6.1](the-load-sentinel-is-not-a-liveness-oracle.md#61-the-holes-answered)'s
  "Trigger and defaults, unchanged by this doc" still says a 3600 s root age floor; the status header
  still says the second half is untouched. Then delete this plan (genre rule) and note what it got
  wrong in the landing commit.
- **Norms.** `just format` before each commit; the pre-commit hook runs `just check-ci`. Stage with
  `git add -N` and `git commit -- <paths>` — other agents are in this tree.

## Don't

- Don't give the root reaper any liveness input, and don't restore `protected` when the store-GC
  hole surfaces — that is step 3's job. [P3](the-load-sentinel-is-not-a-liveness-oracle.md#1-verdict)
  stops applying to A because an age policy has no authority to consult, not because it was weakened.
- Don't build the size cap. It is permitted only *after* age and there is no number yet
  ([OQ-BF9](disk-levers-and-backfill.md#OQ-BF9)).
- Don't implement [§5.3](the-load-sentinel-is-not-a-liveness-oracle.md#53-why-the-age-floor-does-not-cover-it--as-written)'s
  conclusion — it is kept in the doc *because* it is refuted and tempting.
- Don't touch the ten-entry cap, `AddLoadedPath`'s single-writer status, the 24 h debounce, `keep=2`,
  C2's content tags, or `internal/capture/gc.go` ([§7](the-load-sentinel-is-not-a-liveness-oracle.md#7-what-this-does-not-propose)). No heartbeat writer ([§8](the-load-sentinel-is-not-a-liveness-oracle.md#8-alternatives-considered)).
- Don't move the launch-path reap call: [OQ-BF5](disk-levers-and-backfill.md#OQ-BF5) relocates it to
  a housekeeping slot behind a machine-wide lock. Same file, different change.

## Blockers

- **[OQ-LS3](the-load-sentinel-is-not-a-liveness-oracle.md#111-decision-ledger) step 1 has no key. Stop and ask.**
  The ruling groups the keep window by configuration using C2's permanent tags, but that tag is
  `sha256(storePath)[:16]` — per **image**, and it moves on every `flake.lock` bump. Every image is
  then its own group of one and "current plus N superseded per config" degenerates to keep-everything.
  Nothing else records a config identity: `internal/cli/run` sets no `--label`, and the sentinel
  stores store paths.
- **The store-GC interaction (trap 2) is behavior [OQ-LS1](the-load-sentinel-is-not-a-liveness-oracle.md#111-decision-ledger)
  did not consider** — its "losing a root costs a rebuild" holds for podman's blobs but not for a
  running jail's `/bin` while `/nix/store` is mounted `:ro`. Step 3 keeps the ruling intact by moving
  the gate rather than restoring the veto; confirm that reading before shipping it.
- **Does `yolo prune` exit non-zero on Apple Container?** Under LS2 as written, yes, on every
  macOS AC host, for a query that runtime has never supported. Cheap default if no answer comes:
  loud/error for podman, dim "not supported on this runtime" for `container`.
