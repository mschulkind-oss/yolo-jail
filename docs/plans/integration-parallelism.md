# Plan: bounded parallelism for the integration suite

**Status:** OPEN — parked (do the launch-merges first). Deferred deliberately:
CI is free (open-source runners), so integration wall time is a convenience, not
a cost; and the fast **local** dev loop (`just test-fast`) never runs these
container tests at all (they're `requireJail`-gated, skipped under
`testing.Short()`). So this only pays off when someone runs the FULL `just test`
(container suite) locally and wants it faster than serial. Pick it up if that
becomes a real friction; otherwise the launch-merges (below) are the cheaper win.

## Why the suite is serial today (and why it can't just flip)

`integration/` runs strictly serially — AGENTS.md forbids `t.Parallel()`. The
stated reason ("the session image load must not run per worker") is only half of
it; the real blocker is **shared global state that every `yolo run` touches**:

- The image-load **sentinel** `last-load-<runtime>` lives at a single global path
  `paths.BuildDir()` = `GlobalStorage()/build` (`internal/image/autoload.go:122`),
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

## Cheaper win to do FIRST (mostly done)

The launch-merges from the timing analysis cut container *count* with zero
parallelism risk and are landing separately (zbar trio, cli blocked-tool trio,
six isolation probes, gated cgroup pair). Those recover a big chunk of the wall
time without touching the shared-state hazard — do them before investing in the
parallelism refactor, and re-measure whether parallelism is still worth it.

## Risks

- The per-test GlobalStorage refactor could *increase* total work (repeated image
  loads) if done naively — the isolation must be surgical (sentinel + build-root,
  not the whole loaded image). Measure.
- Apple Container's per-container VM memory footprint is the real cap; an
  over-eager N OOMs the runner (the same class of failure the CI "Free disk
  space" purge guards against — see `9cf52bc`).
- Bounded parallelism reduces failure-attribution clarity less than the merges do
  (each test still stands alone), so this is safe on that axis.
