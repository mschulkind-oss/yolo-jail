---
title: "Plan: prove an image is ours with a label (OQ-DF3 REACH)"
date: 2026-09-08
status: accepted
tags: [prune, images, storage, flake]
summary: "Bake an owner label into the jail image config so a row that has lost its repository name is still attributable, then widen `PruneOldImages` from one repo-name probe to a union of that probe and a label probe — keeping the tagged pass byte-for-byte, because pre-label tagged rows are the only ones the shipped reap can see."
vantage:
  status-chip: true
---

# Plan: prove an image is ours with a label

**Design:** [`minimal-disk-footprint.md`](minimal-disk-footprint.md) ·
**Status:** ready · Written against `c619a7e8`, 2026-09-08.

**Precedence:** the design wins on behavior; the tree wins on fact; this file is advice and is the
first thing to be wrong. Never twist code to match it — correct it in the commit.

**Scope — one item:** [OQ-DF3](minimal-disk-footprint.md#OQ-DF3)'s REACH half, ruled 2026-09-08.
DF1 shipped (`be7b8591`), DF2 is answered by composition, DF3's NUMBER and TRIGGER shipped
2026-09-06, DF4 is **blocked** (see *Blockers*). The `_Leaning:_` lines in
[§11.2](minimal-disk-footprint.md#112-open-questions) are history;
[§11.1](minimal-disk-footprint.md#111-decision-ledger) binds.

**Three deliberate boundaries stay open** (DF1, and the WARNING under
[§3](minimal-disk-footprint.md#3-the-three-ledgers)): pre-existing tars are **not** swept, Apple
Container still writes one tar per store path, and the fallback READER (`newestTars`,
`internal/image/autoload.go`) survives. None is a loose end to tidy while you are in the file.

## Map

| Path | Change |
| :--- | :--- |
| `flake.nix` | `mkOciImage`'s `config` block (`:1125`, beside `Cmd`/`Env`) gains `Labels` — the owner key, plus `${imageIdentity}` as the provenance value |
| `internal/paths/paths.go` | new constants beside `JailImageRepo` (`:43`): the label key and its owner value — the Go half of a two-language spelling |
| `internal/prune/probes.go` | `PruneOldImages` (`:263`) runs TWO `images` probes and merges rows by ID; the `:261` doc comment saying REACH "stays open" is now false |
| `internal/prune/imageroots_probe.go` | `ProtectedImageTags` doc comment: a nameless row has no tag to match, so guard #0 is its only veto |
| `internal/prune/probes_test.go`, `autoreap_test.go` | new cases + the exact `imagesCalls` counts, below |
| `internal/cli/run/autoreapimages_test.go` | its stub answers one `images` argv shape; teach it both |
| `integration/imagelabel_test.go` | new — the end-to-end pin (*Ships with*) |
| `internal/cli/commands.go` | `--keep-images` help (`:132`) — "jail images" now includes labeled nameless ones |
| `docs/design/minimal-disk-footprint.md` | status ¶1, the [§3](minimal-disk-footprint.md#3-the-three-ledgers) mermaid edge `CANNOT SEE untagged rows`, [§3.3](minimal-disk-footprint.md#33-ledger-c--podmans-own-image-store-the-one-with-no-reclaimer-for-a-nameless-row) title + tail, [§10](minimal-disk-footprint.md#10-sequencing--what-i-would-build-in-order) step 1's "does NOT do", [§11.1](minimal-disk-footprint.md#111-decision-ledger) Settled-in |
| `docs/design/disk-levers-and-backfill.md` | [§5.5](disk-levers-and-backfill.md#55-yolo-stores--the-inventory-including-what-nothing-reclaims)'s unreclaimable class is now **pre-label** rows only |
| `docs/plans/roadmap.md` | the DF3 item (~`:428`) |

## Reuse

- `imagesInUseByRunningContainers` (`probes.go:440`) and the no-`-f` `rmi` (`:329`) are the whole
  safety story for a nameless row: `ProtectedImageTags` matches TAGS, and `<none>` has none.
  **Constraint:** provenance is not liveness — neither veto moves in this change.
- `paths.JailImageRepoShort` — `probes.go:289` spells the repo `"yolo-jail"` as a literal. Use the
  constant for the repo probe, and put the label key beside it.
- The cross-language pin: mirror `flakeSource(t)` (`internal/cli/run/jailprefix_test.go:215`) or
  `repoRoot(t)` (`internal/entrypoint/shippedclients_test.go:50`) — read `flake.nix`, assert the
  key. Five copies of that 8-line helper already exist; a sixth is house style, not duplication.
- `imagesRunnerWithRunning(rows, psRows, rmiCalls)` (`probes_test.go:137`) and
  `imagesRunnerCounting` (`autoreap_test.go:43`) are the stubs. Both key on `argv[1] == "images"`
  and answer any shape with the same rows — which is why the union passes them unchanged, and why
  new cases have to branch on the presence of `--filter`.
- `internal/cli/run/autoreapcallsite_test.go` already AST-pins the launch-path call site and its
  order. **No new call site ships here** — do not add a second wiring.

## Traps

- **`podman images <repo> --filter …` is a hard error**: `Error: cannot specify an image and a
  filter(s)` (MEASURED, podman 5.8.4, this jail 2026-09-08). The positional repo arg and `--filter`
  are mutually exclusive, so this is two probes, not one query with a flag added.
- **Do not drop the repo-name probe for the label probe.** A label only marks images built after it
  ships; every tagged row already on a machine (12 rows over 11 images here) is repo-name-visible
  and reapable TODAY. Label-only strands them, and no test goes red.
- **No `-a`.** Plain `podman images --filter label=…` already returns the untagged row; `-a`
  additionally surfaced a build INTERMEDIATE carrying the same label (both MEASURED 2026-09-08) —
  rows the ruling does not authorize removing.
- **The label cannot be the store-path key.** Nix cannot reference a derivation's own output path,
  and the stream script's only CLI knob is `--repo_tag` (`add_argument` is the whole parser), so
  nothing per-launch can be injected into the config. `${imageIdentity}` is the finest identity
  spellable there — and it is per `flake.nix`+`flake.lock`, so every `packages:` variant and the
  full/minimal/lean trio share one value. That is enough for ownership; it is not a per-image key.
- **Editing `flake.nix` mints exactly one new image per machine, once.** The closure is unchanged,
  so it shares every layer — the 91.36 kB-unique re-stream case
  ([`image-staging-vs-baking.md`](image-staging-vs-baking.md) [§1.8](image-staging-vs-baking.md#18-re-measured-after-c2--c3--this-is-11-step-5)),
  not 2.836 GB. It also moves `imageIdentity`, so the integration skew check demands a rebuild —
  and nix sees TRACKED files only, so `git add` first or that check reports a false match.
- **A `<none>` row can be LIVE.** A re-stream takes the tag and leaves the running jail's image
  nameless; `podman ps --format {{.ImageID}}` prints the same 12-hex ID as
  `images --format {{.ID}}` (MEASURED 2026-09-08), so guard #0 catches it. That equality is
  load-bearing now in a way it was not for tagged rows.
- **A failed label probe must widen nothing and decline nothing.** Treat it as zero extra rows: the
  set shrinks, which is the safe direction. Declining the whole pass would regress the shipped
  tagged reap on Apple Container, where `--filter label=` is NOT MEASURED.
- Scan for protected tags over BOTH probes' rows before the keep decision; a per-probe `keepIDs`
  lets an unprotected duplicate row delete a protected image (`probes.go:303`).

## Build order

1. **Bake the label + pin it across the two languages.** `flake.nix` `config.Labels`, the
   `internal/paths` constants, the flake-reading test. →
   `nix build .#ociImage --no-link --print-out-paths`, then
   `jq -c .config.Labels "$(grep -o '/nix/store/[^ "]*-conf\.json' <that script>)"` (it is `null`
   today), and `go test ./internal/paths/... ./internal/prune/...`.
2. **Union query in `PruneOldImages`** + unit cases + the count fixups. Land it after step 1 so no
   machine can see the widened reach before its images carry the mark. → `just test-fast`
3. **The integration pin.** → `git add -N` the new file (never `-A`: other agents share this
   tree), then
   `YOLO_TEST_REBUILD_IMAGE=1 go test -count=1 -timeout 0 ./integration -run ImageOwnerLabel`
4. **Docs and comments that still describe the blind spot** (Map's last four rows). → `just done`

## Ships with

- **Unit** (`internal/prune/probes_test.go`): a labeled `<none>:<none>` row is selected; a labeled
  `<none>` row a running container uses is NOT (guard #0, the only veto it has); a labeled row
  duplicating a repo-name row is counted once; the label probe returning `Ran=false` leaves the
  tagged result identical to today's; an UNlabeled `<none>` row is never selected — that last one
  is the ruling's permanence, and nothing else asserts it.
- **Rewrites, not repairs.** `autoreap_test.go:88,108,117` assert `imagesCalls` is 1, 1, 2 — one
  probe per pass. The union makes them 2, 2, 4. Update the numbers **and** make the stub answer the
  two argv shapes differently, or the counts pass while the union is never exercised.
- **Integration** (`integration/imagelabel_test.go`, package `integration`, `requireJail(t)`, no
  `t.Parallel()`): the loaded jail image carries the key
  (`podman image inspect … --format '{{.Labels}}'`) and `podman images --filter label=…` finds it.
  That is the one test that fails if the Nix spelling and the Go constant drift — the class
  `shippedclients_test.go` exists for, one language pair over. `integration/` had zero prune
  coverage as of [§1](minimal-disk-footprint.md#1-the-ruling-and-what-the-bug-actually-is); this is
  the first.
- **Surfaces:** the label key is a *de facto* public name once it is on disk — pick it once
  (`org.yolo-jail.owner=yolo` is what the design measured) and never rename it; a renamed key
  silently un-owns every image already built.
- **Cheap and yours:** the exact key string, and whether provenance is a second label or folded
  into the owner value — `--filter label=key` and `label=key=value` both work (MEASURED
  2026-09-08). Two labels keeps the filter's value exact.
- **Nested-jail verification collapses to almost nothing here** — the call site is untouched and
  the new code is pure behind the `RunFunc` seam. Launch one anyway and
  `YOLO_NO_AUTO_IMAGE_REAP=1` is required: this jail's store holds 15 real yolo images (11 tagged,
  4 nameless, 1.5-3.8 GB each), which is why the 2026-09-06 TRIGGER ruling verified with it set.

## Don't

- **Don't `podman image prune`, and don't filter `dangling=true`.** BROAD was refused: on a shared
  podman those are the user's images.
- **Don't reap pre-label `<none>` rows** by any inference — creation time, layer overlap with a
  known image, `NamesHistory`. Ruled: left alone permanently. Their `yolo stores` row belongs to
  [`disk-levers-and-backfill.md`](disk-levers-and-backfill.md) [§5.5](disk-levers-and-backfill.md#55-yolo-stores--the-inventory-including-what-nothing-reclaims),
  and never to the launch path — that command does not exist yet, so this change ships no warning.
- **Don't label `yolo-jail-builder`** (`flake.nix` ~`:1245`). Podman's positional repo filter
  matches the name component exactly — `alpine` does not match `alpine-extra` (MEASURED
  2026-09-08) — so `yolo-jail-builder` has never been in the reap's reach, and a label would put it
  there.
- **Don't add a second `keep` for nameless rows.** The ruling is that a labeled row is treated
  exactly like a tagged one, so `keep=2` counts the union. `DefaultKeepImages` does not move.
- **Don't restore `rmi -f`**, and don't start checking `rmi`'s exit status: a stopped container's
  image surviving a refused removal is the safety, and the returned list overstating by one is
  pre-existing.
- **Don't touch Ledger B** — no tar sweep, no `ImageCacheKeep` change. That is
  [OQ-BF6](disk-levers-and-backfill.md#OQ-BF6)/[OQ-DF2](minimal-disk-footprint.md#OQ-DF2).

## Blockers

- [OQ-DF4](minimal-disk-footprint.md#OQ-DF4) — **blocked**, awaiting the second dated sample from
  [OQ-BF9](disk-levers-and-backfill.md#OQ-BF9)'s ledger. No byte-budget config key here.
- [`the-load-sentinel-is-not-a-liveness-oracle-plan.md`](the-load-sentinel-is-not-a-liveness-oracle-plan.md)
  is accepted and unbuilt (`PruneOrphanImageRoots` still takes `protected, liveKnown`), and its
  step rewrites the same three `return []string{}` in `PruneOldImages` into decline reasons.
  Whichever lands second rebases; do not do its work here.
- **Stop and ask** before moving the reap into
  [`disk-levers-and-backfill.md`](disk-levers-and-backfill.md) [§5.1](disk-levers-and-backfill.md#51-the-housekeeping-slot)'s
  housekeeping slot: [OQ-BF5](disk-levers-and-backfill.md#OQ-BF5) rules that move and wants the
  machine-wide lock in the same change. Widening reach and relocating the trigger are two changes.
