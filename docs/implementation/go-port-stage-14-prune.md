# Go-port Stage 14 — `yolo prune` native command body (handoff)

**Status:** the prune ENGINE (all pure + runtime-probe functions) and the
`yolo prune` COMMAND BODY are landed as importable Go packages. The front-door
native dispatch (`cmd/yolo/native.go` `runPrune` + `internal/frontdoor`
`gatedNativeSubcommands += "prune"`) is the orchestrator's to wire — this slice
delivers a clean `prunecmd.Run(opts) int` + `prunecmd.NewDefaultOptions()` in
the pscmd/checkcmd mold, ready for a one-line handler.
**Plan:** [`docs/plans/go-port-plan.md`](../plans/go-port-plan.md) Stage 14 (prune).
**Built on:** `internal/prune` (engine), `internal/runtime` (live-set parsers +
`DetectRuntime`, consumed read-only), `internal/paths`, `internal/execx`,
`internal/console`.

## Commits (main, newest first)

- `4cfca95` test(go): real-host prune differential (native Go vs live yolo prune)
- `d111ca5` docs(go): D14 — prune tied-breakdown display lines are name-ordered (proposed)
- `5637cc1` feat(go): prune command body — internal/prunecmd
- `9704df7` feat(go): prune runtime-probe engine (workspaces, stopped, images, build-roots, relay)

(These build on the earlier engine batch: `bd0de13` hardlink-dedup + tri-state
build-root sweep, `b0f24dd` FmtBytes, `7c0da99` DiskUsageReport / PruneShadowedHome
/ PurgeCacheByAge / PruneImageCache.)

## What landed

### `internal/prune` — the runtime-probe layer (this slice)

The four probe functions that were still missing, each wrapping a
container-runtime subprocess behind an injectable `RunFunc` seam (the
`internal/pscmd` Deps pattern applied to the pure engine, so the whole layer is
unit-testable with canned runtime output):

- **`FindYoloWorkspaces` / `InspectWorkspaceMount`** — `ps -a --format
  {{.Names}}` → keep `yolo-*` → inspect each's `{{json .Mounts}}` for the
  `/workspace` bind Source → resolve + dedup (first-seen order preserved).
- **`PruneStoppedContainers`** — `ps -a --format {{.Names}} {{.State}}` → keep
  `yolo-*` whose state is NOT live (running/paused/restarting) → `rm <name>`
  each on apply; returns removed/would-remove names.
- **`PruneOldImages`** — `images --format {{.ID}} {{.Repository}}:{{.Tag}}
  {{.CreatedAt}} yolo-jail` → parse via `pySplitMax` (str.split(None,2)) → the
  EXISTING `OldImagesToRemove` lexical CreatedAt sort → `rmi -f <id>` each on
  apply.
- **`FindReferencedBuildRoots`** — the None-vs-empty tri-state (`ReferencedSet`),
  preserving the fail-safe polarity: an unenumerable `ps` yields `Known=false`
  (the sweep declines), never an empty set that reads as "nothing live". Note
  the inverted selection vs the stopped-container prune: LIVE containers are
  KEPT here, their `/opt/yolo-jail` binds collected.
- **`LiveYoloContainers` + `ReapRelayOrphans`** — the orphaned broker-relay
  sweep (`ps`/`container ls` tri-state via runtime's parsers; the SIGTERM/
  SIGKILL relay kill behind a seam; mtime grace floor + no-live-hash filter).

Match Python's exact argv, timeouts (`ps`=10s, `inspect`=5s, `rm`=10s,
`rmi`=15s), and the FileNotFoundError/OSError/Timeout/non-zero-RC → empty/None
degrade. `pySplitMax` reproduces `str.split(None, 2)` INCLUDING
trailing-whitespace PRESERVATION in the remainder field — a live-Python parity
test caught the naive trailing-strip bug.

### `internal/prunecmd` — the command body (this slice)

`prune_cmd(apply, …)` ported as an importable `Run(opts Options) int` +
`NewDefaultOptions()`, mirroring `pscmd.Run`/`checkcmd`:

- **Flags (1:1 with prune_cmd typer options):** `--apply` (default dry-run),
  `--no-hardlink`, `--dedup-global`, `--no-containers`, `--no-images`,
  `--keep-images` (2), `--no-image-cache`, `--no-build-roots`,
  `--no-shadowed-home`, `--image-cache-keep` (3), `--cache-age` (30),
  `--purge-heavy-caches`.
- **Seams (all injectable, filled with real impls at Run):** `Exec` (the
  `prune.RunFunc` probe seam), `Now` (clock for cache-age + grace floors),
  `DetectRuntime`, `GlobalStorage`/`GlobalHome`/`GlobalCache` getters,
  `RelayBase`, `RelayKill`, `Out`.
- **Section flow** reproduced exactly: pre-report (usage total + global-storage
  breakdown + cache top-5 + the ≥20 GiB images HDD hint) → hardlink dedup →
  stopped containers → orphaned broker relays → old images → cached image
  tarballs → orphaned build-root generations → shadowed seed subtrees → cache
  purge → summary (the `Reclaimed`/`DRY-RUN: would reclaim` line).

**Output contract** (Stage 14/15 approved OQ, same as check/run): rich markup is
stripped; the port reproduces the section ordering, the "would remove"/"removed"
verbs, and the report INFO — NOT byte-identical ANSI. The **FmtBytes numbers,
reclaim decisions, and removed-name lists ARE byte-exact** vs live Python.

## Byte-parity / behavior invariants preserved

- Hardlink-dedup atomicity (`.yolo-dedup-tmp` link-then-rename, never unlink
  the original first; skip same-inode pairs) — engine, unchanged.
- **Tri-state liveness DECLINE-on-unknown** for BOTH the build-root sweep and
  the relay reap (unknown ≠ empty set).
- Shadowed-home deletes CONTENTS, never the anchor dirs (mount-anchor
  discipline; the 2026-07-04 incident).
- CreatedAt image sort is LEXICAL (never parsed).
- Forbidden cache subdirs (chromium/firefox family, copilot) hard-excluded.

## Tests

`go test ./internal/prune/ ./internal/prunecmd/` — all green.

- **`internal/prune`**: probe unit tests (workspaces incl. dedup + malformed
  inspect + missing runtime; stopped-container live/dead selection + apply rm
  calls; old-image lexical sort + keep; build-root tri-state; relay reap grace
  floor + unknown-decline; `pySplitMax`). `TestProbeParityVsLivePython` shells
  to `uv run python`/`python3`, cross-checking decode/selection against live
  `src.prune` (SKIP w/o python) — this caught the split trailing-whitespace bug.
- **`internal/prunecmd`**: dry-run empty + populated (reports-not-mutates),
  `--apply` on a TEMP storage root (shadowed dir emptied-not-removed; rm/rmi
  issued), flag gating, build-root/relay fail-safe decline, `fmtComma`.
  `TestParityVsLivePython` drives `tests/oracles/prune_oracle.py` (the real
  `prune_cmd` body with injected seams) over a shared populated tree — asserts
  byte-identical ANSI-stripped output.

## Real-host / nested-jail verification

`TestLiveHostVsPythonPrune` (gated behind `YOLO_PRUNE_LIVE_VERIFY=1`; reads real
storage, never runs in CI) runs the native `Run` with REAL seams (real podman
probes + the real ~61 GiB `GLOBAL_STORAGE`) and the live Python `yolo prune`,
both dry-run, and diffs the reclaim-DECISION lines after undoing rich's terminal
soft-wrap.

Verified identical against a live 61 GiB storage tree (jail, podman 5.8.4,
`YOLO_RUNTIME=podman`), 9 decision lines:

```
Current usage  total=61.3 GiB  (workspaces=0 B, global=61.3 GiB)
none                                   (stopped containers)
none                                   (broker relays)
none                                   (old images)
would remove: 49.6 GiB across 17 file(s)   (cached image tarballs)
none                                   (build-root generations)
would remove: 0 B across 4 path(s)     (shadowed seed subtrees)
would remove: 394.6 MiB across 2,766 files (cache purge)
DRY-RUN: would reclaim 49.9 GiB via 0 hardlinks, remove 0 container(s),
  0 image(s), 17 image tar(s), 0 build-root generation(s),
  4 shadowed seed path(s), 2,766 cache file(s).  Re-run with --apply to execute.
```

The Go command body was exercised directly (its real seams point at
`runtime.DetectRuntime` + `paths.Global*` + a real subprocess `Exec`), which is
the same code path a `YOLO_IMPL=go yolo prune` front-door dispatch will hit once
`runPrune` is wired. A full nested-jail `YOLO_IMPL=go yolo prune` run is pending
the orchestrator's front-door wiring (`native.go` + `frontdoor.go`), at which
point the two arms can be compared through the shim directly.

## Divergences

- **D14 (proposed)** — TIED disk-breakdown display lines are name-ordered in Go
  (deterministic) vs Python's stable sort over filesystem-arbitrary `iterdir()`
  order. Display-order ONLY; reclaim decisions / FmtBytes numbers / removed-name
  lists unaffected (a tie means equal bytes). See
  [`go-port-divergences.md`](../design/go-port-divergences.md#d14).

## Constraints honored

Byte-exact reclaim decisions + FmtBytes + removed lists vs LIVE Python;
tri-state DECLINE-on-unknown, shadowed-home delete-contents-not-dirs, CreatedAt
lexical sort all preserved; `--apply` tested only on temp storage roots;
`GOOS=darwin GOARCH=arm64 go build ./...` clean; gofmt/vet/staticcheck clean;
conventional commits, no AI-attribution trailers, no `--amend`. `native.go` and
`frontdoor.go` intentionally UNTOUCHED (the orchestrator wires all front-door
native dispatch).
