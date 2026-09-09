---
title: "Plan: disk levers and backfill"
author: "Agent"
date: 2026-09-08
status: accepted
tags: [plan, storage, prune, nix]
summary: "Build hand-off for the ruled disk-levers design: file map, reuse by symbol, traps, build order, and what ships besides code."
vantage:
  status-chip: true
---

# Plan: disk levers and backfill

**Design:** [`disk-levers-and-backfill.md`](disk-levers-and-backfill.md) — the [Decision Ledger](disk-levers-and-backfill.md#111-decision-ledger)
and each `**Answer**` are authoritative; every `_Leaning:_` is history and several rulings went against it.
**Status:** ready · Written against `ba072719`, 2026-09-08.
Precedence: the design wins on behavior, the tree wins on fact, this file is advice and is the first thing to be wrong.

Scope: [OQ-BF1](disk-levers-and-backfill.md#OQ-BF1)–[OQ-BF6](disk-levers-and-backfill.md#OQ-BF6),
[OQ-BF9](disk-levers-and-backfill.md#OQ-BF9), and [OQ-BF10](disk-levers-and-backfill.md#OQ-BF10) as a later slice.
[OQ-BF7](disk-levers-and-backfill.md#OQ-BF7) shipped (`6a855b6d`); [OQ-BF8](disk-levers-and-backfill.md#OQ-BF8) dissolved — no work for either.

## Map

| Path | Change |
| :--- | :--- |
| `internal/prune/stores.go` | new — inventory engine (one row per [§2.1](disk-levers-and-backfill.md#21-every-store-one-table) store: size + how obtained, reclaimer, trigger, verdict) and the bounded sample ledger |
| `internal/cli/stores.go` | new — `yolo stores` front door: `--json`, `--age`, `--no-record`, frame header (host vs in-jail) |
| `internal/cli/dispatch.go`, `internal/cli/help.go`, `internal/cli/subhelp.go` | register `stores`; blurb; usage text |
| `internal/prune/cachepurge.go` | `nce` into `CachePurgeDefaultSubdirs`; a deadline seam on `purgeOldFilesUnder` for the 60 s measurement budget |
| `internal/prune/prunecmd.go` | `ImageCacheKeep` default resolved per runtime inside `Run`; two new sections (prefix roots, yolo's own store outputs) |
| `internal/cli/commands.go` | `pruneUsage` default text; `--image-cache-keep` parse against an "unset" marker |
| `internal/cli/run/housekeeplock.go` | new — machine-wide `locks/housekeeping.lock`: blocking acquire (load side), try-acquire (pass side) |
| `internal/image/autoload.go` | `AutoLoadOptions.Lock` seam bracketing the re-inspect → `AddLoadedPath` step |
| `internal/cli/run/imageload.go` | inject the lock seam beside `RegisterRoot` (~`imageload.go:79`) |
| `internal/cli/run/housekeeping.go` | new — the slot orchestrator: try-lock, per-class stamp, classes in order, never stdout |
| `internal/cli/run/run.go` | reap call leaves ~`run.go:803`; slot call inside the `onStarted` closure after `lock.Close()` (~`run.go:1034`); offer after `checkConfigChanges`, before the workspace lock (~`run.go:700`) |
| `internal/cli/run/autoreapimages.go` | becomes one slot class; its stdout line goes |
| `internal/prune/autoreap.go` | stamp helpers take a class name; `last-image-reap` keeps its spelling |
| `internal/paths/paths.go` | `PrefixRootsDir()` = `build/prefix-roots`, doc-commented like `PackageRootsDir` |
| `internal/image/prefixroot.go` | new — `RegisterPrefixRoot(storePath, out)` |
| `internal/cli/run/jailprefix.go` | `jailPrefix.storePath`; register the root in `resolveJailPrefix`, gated `!inJail && underDir(binDir, hostNixStore)` |
| `internal/prune/prefixroots.go` | new — liveness-gated reaper of `prefix-roots/*`, liveness = live containers' `/opt/yolo-jail/bin` mount sources |
| `internal/prune/storeoutputs.go` | new — named candidates minus yolo's own link targets, `nix-store --query --roots` filter, batched `nix store delete` |
| `internal/cli/run/offer.go` | new — offered tier: measurement stamps, recorded answers, TTY prompt |

## Reuse

- `prune.DueForAutoImageReap` / `RecordAutoImageReap` (`internal/prune/autoreap.go`) are already generic over a sentinel path — a per-class stamp is the same two calls with `last-<class>-reap`. No new debounce code.
- `prune.AutoReapOldImages` is the shape of every automatic class: debounce → fail-safe gate → the same function `yolo prune --apply` calls → stamp only when it ran. The `o.Exec`→`prune.RunFunc` adapter at `autoreapimages.go:46-53` (the `Ran && !Timeout` fold) — copy it verbatim.
- Liveness for prefix roots: `prune.inspectMountSource(rt, name, dest, run)` (`probes.go:110`) already reads `podman inspect --format {{json .Mounts}}` for any `dest`; `InspectWorkspaceMount` is its only caller. Call it with `dest = run.JailPrefixBinDir`, passed in as a parameter (run imports prune, never the reverse). Tri-state via `prune.LiveYoloContainers` → `runtime.LiveSet.Known`.
- Rooting: `image.RegisterImageRoot` (`gcroot.go:54`) is the argv (`nix-store --add-root <link> --realise <storePath>`), `image.ImageStoreKey` the link name; `rootExtrasProfile` (`storepackages.go:286`) is the same thing in a second dir. BF4 is the third copy, in a third dir — `paths.PackageRootsDir`'s doc comment states the rule. Gate as `run.rootImageFn` does.
- Lock halves: `acquireWorkspaceLock` (`flock.go`, blocking with a `waiting` notice) and `cli.tryFlockAt` (`hostapplylock.go:92`, non-blocking). `flockSyscall` is the test seam. Inject into `image` through a seam on `AutoLoadOptions`, mirroring `RegisterRoot` — `image` cannot import `run`.
- Prompt: `changePrompter.Prompt` (`preflight.go:279`) — `bufio.Scanner` on `o.Stdin`, `o.IsTTYStdin()` gate; non-TTY polarity is `config.ChangedNonInteractiveError` (print, never an implicit yes).
- Sample ledger: `image.AddLoadedPath` (`image.go:270`) is the read-append-trim-write shape; 30 instead of 10.
- Inventory sizing/rendering: `prune.DiskUsageReport`, `dirSizeBytes`, `mountPointOf`, `FmtBytes`, `fmtComma`, `sortByValueDesc`. Dead bytes = `PurgeCacheByAge(..., apply=false)`. Runtime + relocations resolution: copy `pruneOptions` (`commands.go:169`).
- BF3's belt: `prune.UnrootedProtectedPaths` and the `nixgc.go` section gate (`prunecmd.go:723-761`) — "decline unless every live thing is rooted". `nixgc.go:96` is the `nix --extra-experimental-features nix-command flakes store …` spelling.
- `--json` precedent is `describe --json` (`describe.go:45`); `configDrift` is the exit-code precedent, not a JSON one.
- Fixtures: `autoreapimages_test.go` (temp `HOME` + fake `Exec` keyed on `argv[1]`), `prunecmd_test.go:TestApplyOnTempRoot` (temp roots via seams), `imageroots_test.go`, `flock_test.go`, `autoreapcallsite_test.go` (AST call-site pin), `prefix_test.go:TestJailPrefixOutLinkIsOutOfBothReapersReach` (dir-reach pin to mirror).

## Traps

- **`rmi -f` is already gone** (`feddc5e0`, guard #0 `podman ps`); [§5.4](disk-levers-and-backfill.md#54-one-writer-concurrency-failure) still says `-f`. The live race is narrower: B's `ImageInspectCmd` says present → A's plain `rmi` succeeds (B has no container yet) → B records and `podman run` fails "image not known". **Constraint:** A holds the lock from sentinel read through `rmi`; B holds it from a re-inspect through `AddLoadedPath`. Advice: take B's lock *after* the stream and re-inspect under it (reload if gone) — holding it across `image.stream_load` serialises every launch's load on the machine.
- `onStarted` is a goroutine (`ttyproxy.go:57`) and the process `os.Exit`s from `onTerminate` or returns after `runWithProxy` — a class can be cut mid-delete. Stamp only after a class completes. **Constraint:** nothing in the slot writes `o.Stdout`; it is the pty by then.
- `ImageCacheKeep` is fixed in `NewDefaultOptions` (3) before `Run` resolves `rt`, and the flag parser's fallback is the current value (`commands.go:212`). Use an unset marker, resolve after `DetectRuntime`.
- BF4 registration happens in `resolveJailPrefix`, a whole image build before `podman ps` can see the container. A liveness-only reaper in another launch's slot reaps the fresh root. Keep a startup grace floor (`imageRootOlderThanSeconds` shape) — [OQ-LS1](the-load-sentinel-is-not-a-liveness-oracle.md#111-decision-ledger) distinguishes a race guard from a policy; this is the guard.
- `nix-store --add-root --realise` wants `/nix/store/<hash>-yolo-jail-install-prefix`, not `…/opt/yolo-jail/bin`. `jailPrefix` keeps only `binDir`/`shareDir` today.
- `nix store delete a b c` aborts the whole batch on one live member. Pre-filter with `nix-store --query --roots` (read-only, no GC lock), batch the rest, fall back per path on a refusal. Never `--ignore-liveness`.
- `PruneOrphanImageRoots` is being rewritten under [OQ-LS1](the-load-sentinel-is-not-a-liveness-oracle.md#111-decision-ledger) (drop `protected`, week floor) — do not build on it, do not share `build/roots`.
- A nested green is blind to BF3 and BF4: both gate on `!inJail`. Only a host launch shows `nix-store --query --roots <prefix>` non-empty.
- `podman images` without `-a` hides `<none>` rows; [OQ-DF3](minimal-disk-footprint.md#OQ-DF3) REACH ruled pre-label rows are left alone permanently and only *surfaced* by `stores` — the [§5.2](disk-levers-and-backfill.md#52-two-tiers-one-mapping) "offered" row predates that ruling.

## Build order

1. **BF9 `yolo stores`** — engine + ledger + CLI. Needs no other step and makes every later one show a number moved. → `go test ./internal/prune ./internal/cli`
2. **BF6 + BF2's list** — runtime-resolved tar default (0 podman, 3 `container`), `nce` added. Rewrite the `(keep=3)` goldens to the new behavior. → `go test ./internal/prune ./internal/cli`
3. **BF5 — lock + slot, reap moved** — lock seam in `image`, orchestrator in `run`, image class moves into `onStarted`, tar class (keep=0, podman only) and the L8 sweeps join it. First thing that makes the slot exist. → `go test ./internal/image ./internal/prune ./internal/cli/run`, then a nested launch from a throwaway workspace (`YOLO_REPO_ROOT=/workspace`, see [`../../AGENTS.md`](../../AGENTS.md) Testing) — verifies the podman classes only.
4. **BF4 — prefix roots** — `PrefixRootsDir`, `RegisterPrefixRoot`, the liveness reaper as a prune section and a slot class. A correctness fix; BF3's gate. → `go test ./internal/image ./internal/paths ./internal/prune ./internal/cli/run`; host launch, then `nix-store --query --roots` on the running prefix.
5. **BF3 — named store delete** — prune section (manual) and slot class (automatic, host-only), declining unless every live container's prefix is rooted. Ordering 4 before 5 removes the "offer until BF4 lands" arm the ruling permits — one fewer client of step 6. → `go test ./internal/prune ./internal/cli/run`; host launch, `*-yolo-jail-go-0-dev` count reaches 0.
6. **BF1/BF2 — the offer** — measurement stamps written by the slot, recorded answers, pre-attach TTY prompt, L1 cache purge as the one client. → `go test ./internal/cli/run`; a TTY launch shows the prompt once the measured class is ≥ 1 GiB.
7. **BF10 — CAS aliasing** — later slice, its own plan. Gated on host OS+arch = jail, writable bind, never `named_caches`, never macOS; the stranded jail-side copy becomes a backfill row. Not before a post-step-6 `yolo stores` sample.

Run `just format` then `just check-ci` before each commit. Commits: `git add -N <paths>` then `git commit -- <paths>` — never `-A` (other agents are in the tree).

## Ships with

- **Unit, by case.** Stores: absent store → `absent`/0, unreadable → `unknown` with reason, one unreadable never fails the run, `--no-record` writes nothing, 31st sample trims the oldest, in-jail frame header. Lock: pass side loses the tie and leaves the stamp unwritten; load side blocks; seam ordering pinned (lock → re-inspect → `AddLoadedPath` → unlock). Slot: debounced class skipped, unreadable liveness declines and does not stamp, a mid-class failure stamps nothing. Prefix roots: registered only when `!inJail` and under `/nix/store`; reaper keeps a root whose target a live container mounts, keeps one younger than the floor, declines on `Known=false`, never touches a non-symlink. Store outputs: candidate set is exactly the three suffixes minus every `roots/*`, `prefix-roots/*`, `jail-prefix-*`, resolving `run-result-*` target; a refused batch falls back per path; in-jail is a no-op. Offer: non-TTY prints and skips, `never` sticky, `not now` re-asks after 7 d, below 1 GiB silent.
- **Rewrite, do not repair:** `autoreapcallsite_test.go:TestRunContainerReapsImagesAfterTheLoad` — pin the call inside `onStarted` after `lock.Close()`, not merely after the load; `prunecmd_test.go` goldens printing `(keep=3)`; `TestAutoReapOldImagesWiring` if it asserts the stdout line.
- **Integration** (`integration/`, `requireJail`, serial, no `t.Parallel`): launch with a pre-tagged stale `localhost/yolo-jail:<fake16>` image and `YOLO_NO_AUTO_IMAGE_REAP` unset; assert the jail was attached before the `rmi` and `last-image-reap` exists after exit. Nothing in `integration/` covers the reap today.
- **Docs now describing the old thing:** [`minimal-disk-footprint.md`](minimal-disk-footprint.md#OQ-DF3) TRIGGER ledger row (`run.go:803`, "under revision"); [`../plans/storage-lifecycle.md`](../plans/storage-lifecycle.md) rows saying `PruneImageCache(keep=3)` / `--image-cache-keep 3`; `pruneUsage` `(default 3)`; `prune.NewDefaultOptions` comment; `autoreapimages.go` header ("Call it AFTER autoLoadImage succeeds"); `internal/image/prefix.go` header ("THE OUT-LINK IS THE GC ROOT" — now one of two); [`storage-and-config.md`](storage-and-config.md) `build/` listing; [`../guides/USER_GUIDE.md`](../guides/USER_GUIDE.md) gains `yolo stores` and the offer prompt; the design's own `rmi -f` sentences and the `<none>` row — correct them in the landing commit.
- **Surfaces:** `yolo stores [--json] [--age] [--no-record]`; new state under `BuildDir()`: `prefix-roots/`, `last-<class>-reap`, `measure/<class>`, `offers/<class>`; under `GlobalStorage()`: `stores/<store>.samples`, `locks/housekeeping.lock`. Defaults with units are [§5.3](disk-levers-and-backfill.md#53-triggers-defaults-and-the-post-launch-slot)'s — do not re-derive them.
- **Cheap and yours:** stamp/answer file formats (one line, Unix seconds — `RecordAutoImageReap`'s shape); ledger file layout; class ordering inside the slot; where the 60 s deadline is checked.
- **Norms touched:** `just check-ci` (pre-commit; `go test -short ./...`), `uvx vantage-check <doc>` on every touched doc, conventional commits, no attribution trailers, nested-jail verification from a throwaway workspace for the run-path change, and a **host** launch for steps 4–5 (state it in the commit when it could not be run).

## Don't

- No `nix store gc`, `nix-collect-garbage`, `podman image prune`, `podman system prune` in the slot — named deletion only ([§5.4](disk-levers-and-backfill.md#54-one-writer-concurrency-failure) Forbidden).
- No prefix roots in `build/roots` or `build/package-roots`; no sentinel protection for them ([OQ-BF4](disk-levers-and-backfill.md#OQ-BF4), [OQ-BF8](disk-levers-and-backfill.md#OQ-BF8)).
- Do not hold the housekeeping lock across the nix build or the stream (trap 1).
- Do not remove `newestTars` ([OQ-BF6](disk-levers-and-backfill.md#OQ-BF6)); do not drop `go-build` from or add `staticcheck` to the purge list ([OQ-BF2](disk-levers-and-backfill.md#OQ-BF2)).
- Do not offer `<none>` rows; do not touch `PruneOrphanImageRoots`, `AddLoadedPath`'s writer role, or the LRU cap.
- `yolo stores` never runs on the launch path or in the slot, never mutates a store, never takes the nix GC lock, never launches a container.
- No daemon, timer, or cron ([§6](disk-levers-and-backfill.md#6-alternatives-considered) A6). No rename of `last-image-reap` — stamps on disk keep working.

## Blockers

- **Stop and ask:** does `YOLO_NO_AUTO_IMAGE_REAP` widen to the whole slot, or does each automatic class get its own opt-out? The design names only the image class's ([OQ-DF3](minimal-disk-footprint.md#OQ-DF3)), and the house norm is a loud opt-out per aggressive default.
- Tar class on Apple Container stays manual until [OQ-DF2](minimal-disk-footprint.md#OQ-DF2) names the component; nothing here waits on it.
- [OQ-DF4](minimal-disk-footprint.md#OQ-DF4) is unblocked by step 1 but is not this plan's.
