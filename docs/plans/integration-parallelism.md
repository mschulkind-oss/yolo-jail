---
title: "Plan: bounded parallelism for the integration suite"
status: accepted
stage: DECIDED
next: "Measure the full Linux suite serial against N separate `go test -run` shard processes, which the machine lock in integration/machinelock_test.go already makes safe, before any in-process t.Parallel work"
---

# Plan: bounded parallelism for the integration suite

**Status:** 2026-07-20 — deliberately parked; re-checked 2026-08-23 and against the tree
2026-09-30. Deferred deliberately:
CI is free (open-source runners), so integration wall time is a convenience, not
a cost; and the fast **local** dev loop (`just test-fast`) never runs these
container tests at all (they're `requireJail`-gated, skipped under
`testing.Short()`). So this only pays off when someone runs the FULL `just test`
(container suite) locally and wants it faster than serial. Pick it up if that
becomes a real friction; the launch-merges (below) already landed in `c4ae68a`.
`integration/` still carries the explicit rule that nothing in it may call `t.Parallel()`
(`integration/harness_test.go`), so this is a deliberate non-decision rather than a forgotten one.

**What moved under this plan by 2026-09-30, checked in the tree:**

- **Step 1's premise changed: the shared state is now locked, not isolated.** `requireJail`
  calls `isolateHome` (`integration/harness_test.go`, `integration/packs_test.go`), which points
  `HOME` at a per-test temp dir, and each `go test` process gets a yolo state dir of its own
  (`integration/runstore_test.go`). But `build` — the load sentinel and the GC roots — is
  linked back to the machine's on purpose, because the image reaper must see every jail's
  images; the sentinel is written under a lock in `internal/image/autoload.go`, and image
  copies take the machine-wide lock in `internal/image/copylock.go`.
- **Separate test processes may already overlap safely.** `integration/machinelock_test.go`
  is a cross-run readers-writer lock: every container test holds it shared, and the few tests
  that take machine state over (`requireJailExclusive`) hold it exclusive. It was added after
  6 of 16 overlapping full runs failed on 2026-09-27. Whether shards run this way are faster
  than one serial run is not measured.
- **Step 2 as written would hit a Go rule.** `isolateHome` sets `HOME` with `t.Setenv`,
  and Go's `testing` package refuses `t.Setenv` in a test that calls `t.Parallel()`, so
  in-process parallelism needs `HOME` passed per command instead. The machine lock's
  bookkeeping is also one package variable (`heldMachineLock`) because the tests run
  serially.
- **CI already splits the macOS suite across jobs.** `.github/workflows/nightly-macos.yml`
  runs twelve shards: doubled to eight on 2026-09-13 after two of four shards hit the 50-minute
  cap, then raised to twelve with a 75-minute cap on 2026-09-15 (`7b59711f`).
  That is sharding across runners, not `t.Parallel()`, and it is the one place where wall time
  has cost evidence rather than a convenience.

## Why the suite is serial today (and why it can't just flip)

`integration/` runs strictly serially — AGENTS.md forbids `t.Parallel()`. The
stated reason ("the session image load must not run per worker") is only half of
it; the real blocker is **shared global state that every `yolo run` touches**:

- The image-load **sentinel** `last-load-<runtime>` lives at a single global path
  `paths.BuildDir()` = `GlobalStorage()/build` (`image.LoadSentinelPath` in
  `internal/image/image.go`, written from `AutoLoadImage` in `internal/image/autoload.go`),
  and **every** `yolo run` reads/writes it via `AutoLoadImage` — not just
  `TestMain`'s one-time `ensureJailImage()`. Tests with `packages:` configs
  (zbar, libsodium in `packages_test.go`) trigger *additional* per-run `--impure`
  image rebuilds + `podman load`s that hit that same sentinel and the shared nix
  build-root.
- So two tests in parallel race on the sentinel, the build-root staging, and the
  `podman load` — a real correctness hazard, not just the stale "load once" note.

`TestMain` loading the image once is necessary but **not** sufficient for
parallelism: the per-test rebuilds re-enter the shared load path.

## The work (in order)

1. **Give each test its own global-storage root.** Point `HOME` (or
   `XDG_DATA_HOME` — whichever `paths.GlobalStorage()` derives from; verify) at a
   per-test `t.TempDir()` in the harness's `runCommand`, so the sentinel, nix
   build-root, and image cache stop being shared. Confirm container **names**
   already don't collide — they derive from the per-test workspace `t.TempDir()`
   via `naming.FromWorkspace` (`harness_test.go`), so that half is already safe.
   - Cost caveat: a per-test `GlobalStorage` means the image `podman load` / nix
     build could no longer be shared across tests → potentially re-loading the
     image many times. Mitigate: keep the *image* load shared (it's read-only
     once loaded into the runtime) but isolate the *sentinel + build-root* writes,
     OR accept the reload cost only for the handful of `packages:` tests. This
     tradeoff is the crux — measure before committing.
2. **Bounded `t.Parallel()`.** Add `t.Parallel()` to the container tests and cap
   concurrency with `go test -p N` (or a semaphore in the harness). **N is bound
   by MEMORY, not cores** — each jail is a real container/VM (Apple Container
   spins a per-container VM; podman a heavy container), so 32 cores does NOT mean
   32 jails. Start at N=4, tune against runner memory; a 20-min serial run could
   drop to a few minutes at N=4–8.
3. **Update AGENTS.md.** The "Do NOT add `t.Parallel()`" note becomes "bounded
   `t.Parallel()` with per-test GlobalStorage; cap via `-p N` sized to memory."
   Preserve the real invariant (no *unbounded* fan-out; the shared-state isolation
   is a precondition).
4. **Verify** in a nested jail at N>1: no sentinel/build-root races, no container
   name collisions, no OOM. Re-run several times (races are probabilistic).

## Cheaper win (landed in `c4ae68a`)

The launch-merges from the timing analysis cut container *count* with zero
parallelism risk and landed in commit `c4ae68a` (zbar trio, cli blocked-tool
trio, six isolation probes, gated cgroup pair — merged into
`packages_test.go`, `cli_test.go`, `isolation_test.go`, `cgroup_test.go`; the
old per-check tests are gone). Those recovered a big chunk of the wall time
without touching the shared-state hazard — re-measure whether the parallelism
refactor is still worth it before investing in it.

## Risks

- The per-test GlobalStorage refactor could *increase* total work (repeated image
  loads) if done naively — the isolation must be surgical (sentinel + build-root,
  not the whole loaded image). Measure.
- Apple Container's per-container VM memory footprint is the real cap; an
  over-eager N OOMs the runner (the same class of failure the CI "Free disk
  space" purge guards against — see `9cf52bc`).
- Bounded parallelism reduces failure-attribution clarity less than the merges do
  (each test still stands alone), so this is safe on that axis.
