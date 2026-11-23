---
status: accepted
stage: BUILT
next: "Root combined gate and native Apple runtime verification"
---

# Apple Container image prune: bounded repair plan

**Status:** 2026-10-29. Production repair is built in the reviewed candidate and assembled for the combined gate. **Measured natively 2026-10-29:** Apple Container 1.1.0's JSON parses through the new reader, and a dry run and `--apply` removed three `yolo-jail` tags and kept `latest`, the running container's image and every non-yolo image ([Mac evidence](macos-native-tls.md#mac-evidence-witness-run)). **Owed:** that a launch after a prune drops the stale image-delivery record and reloads (not observed on Apple Container; re-run at main `6b0e53ed4` or later).
Written against `8c0bc42f3a72e19b9f27a06f33b1fdf4a63c4679`, 2026-10-29.
Behavior authority: [image retention](../reference/image-retention.md#principles),
reported native defect (the old probe listed images with podman's `images` verb, which Apple Container lacks, so the reaper declined on every run; Apple's spelling is `container image list`).
Accepted tactical execution is an ordinary repair, not a new retention policy.
Precedence: retention invariants win on behavior; current tree wins on fact; this plan is advice.

## Grounding and consumers

- `PruneOldImages` calls `pruneOldImages` in `internal/prune/probes.go`.
- Manual `RunPrune` in `internal/prune/prunecmd.go` and automatic
  `AutoReapOldImagesGuarded` in `internal/prune/autoreap.go` share that production selector.
- Current source asks Apple Container for podman's `images --format`,
  `ps --format {{.ImageID}}`, and `rmi` grammar. Native report establishes the first
  is rejected; primary source establishes the supported grammar for all three.
- `TestPruneOldImagesAppleEmptyListing` in `internal/prune/appleimages_test.go`
  permanently drives the real selector with a rejecting fake Apple runtime.
  `go test ./internal/prune -run '^TestPruneOldImagesAppleEmptyListing$' -count=1`
  is RED: required listing declines with `could not list this runtime's images`.

## Map and reuse

| Path | Change |
| --- | --- |
| `internal/prune/probes.go` | Dispatch the existing selector to supported Apple probes; retain podman path |
| `internal/prune/appleimages.go` (new, if useful) | Strict Apple JSON ownership/liveness parsing and conservative selection |
| `internal/prune/appleimages_test.go` | Permanent production-caller fixture regressions |
| `docs/reference/image-retention.md` | Narrow backend evidence/optional-widening note |
| `docs/reference/disk-footprint.md` | Replace now-obsolete no-Apple-reclaimer claim, retaining native witness gap |

Advice: keep Apple-specific parsing local to prune: no reusable image parser exists in
`internal/runtime`; broad adapter changes add consumers unnecessarily.
Reuse `image.ImageInspectCmd` from `internal/image/image.go` if inspection is necessary.
`runtime.MountsFromContainerInspectJSON` exemplifies strict unknown-vs-empty handling,
but its mount schema is not an image schema. Existing `RunFunc` is the production subprocess seam.
`Guard.Do` and automatic recheck must continue to surround each deletion.

## Settled constraints — do not reopen

- REQUIRED repository ownership and liveness errors/malformed/ambiguous payloads decline deletion.
- Optional owner-label widening contributes zero candidates on unsupported/error/malformed answers.
  Smallest repair may intentionally skip Apple optional widening; do not invent label filtering.
- Protect every current tag, every alias of its image, and all images referenced by containers.
- Never force deletion, use a broad runtime prune, infer liveness from side files, or broaden repository match.
- Keep podman behavior and return/report vocabulary stable. Dry-run executes no delete.
- Empty JSON arrays are known empty, not errors; null/scalars/missing identity are unknown.
- Preserve guarded automatic recheck; do not bypass it with a separate apply loop.

## Task — independent, one implementation commit

Preconditions: named planning ref exists, implementation tree clean, original RED reproduced.
Extend the permanent caller tests before the implementation. Cases: supported valid list,
empty list, malformed list, failed required call, failed optional widening (zero extra rows),
ambiguous identity, missing/failed/malformed liveness, current/protected alias, in-use images,
dry-run, and exact supported non-force deletion arguments. A deletion-helper-only test is insufficient.
Use primary v1.1.0 source JSON shapes below, not guessed capitalization.

## Primary protocol transfer

Verified source: Apple Container tag `1.1.0`, commit
`5973b9cc626a3e7a499bb316a958237ebe14e2ed`.
[ImageList.swift](https://github.com/apple/container/blob/5973b9cc626a3e7a499bb316a958237ebe14e2ed/Sources/ContainerCommands/Image/ImageList.swift)
emits an array for `container image list --format json`.
[ImageResource.swift](https://github.com/apple/container/blob/5973b9cc626a3e7a499bb316a958237ebe14e2ed/Sources/ContainerResource/Image/ImageResource.swift)
serializes `configuration.name` (stored ref), `configuration.descriptor.digest`
(full OCI digest), and `variants`; `id` is the digest without algorithm prefix.
[ContainerList.swift](https://github.com/apple/container/blob/5973b9cc626a3e7a499bb316a958237ebe14e2ed/Sources/ContainerCommands/Container/ContainerList.swift)
supports `container ls --format json`; use `--all` to conservatively retain all container references.
Each container's `configuration.image.reference` and
`configuration.image.descriptor.digest` give liveness identity, across foreign and yolo containers.
Match full descriptor digest; never platform `variants[].digest` or mutable tag alone.

Fixtures are source-derived, NOT captured native output:

```json
[{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","configuration":{"name":"yolo-jail:old","descriptor":{"digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"creationDate":"2026-01-01T00:00:00Z"},"variants":[]}]
```

```json
[{"id":"foreign","configuration":{"image":{"reference":"yolo-jail:old","descriptor":{"digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}},"status":{"state":"running"}}]
```

[ImageDelete.swift](https://github.com/apple/container/blob/5973b9cc626a3e7a499bb316a958237ebe14e2ed/Sources/ContainerCommands/Image/ImageDelete.swift)
uses `container image delete <stored-ref>`; no force/all. Unlike podman it has NO proven
in-use backstop and runs global orphan cleanup. Do not claim otherwise.
Constraint: freshly re-read container identities and image/reference binding before each apply
inside `Guard.Do`, honoring automatic pointer/load recheck. Unknown or changed binding
skips deletion; failed delete must not be reported successful. Preserve all aliases sharing
protected/in-use digest. External Apple-runtime races remain an explicit native risk, not
something fake fixtures prove away. Optional label widening intentionally absent in this slice.
Repository equality recognizes `yolo-jail`, `localhost/yolo-jail`, and Apple's normalized
`docker.io/library/yolo-jail`, never suffix/prefix lookalikes. Reject ambiguous duplicate refs.

## Review backlog — not fixed

- Add independent production-caller cases where a live container retains the same stored image reference but has a different descriptor digest, for both initial selection and guarded fresh recheck. Existing tests can pass through digest protection without independently exercising the reference veto.
- Native Apple 1.1.0 list, container-list and delete behavior was measured 2026-10-29 (see the status line); inspect was not. The external final-check-to-delete race remains: this source-derived repair does not establish an atomic in-use veto.


Gates:

```sh
go test ./internal/prune -run 'TestPruneOldImagesApple' -count=1
go test -short ./internal/prune ./internal/runtime ./internal/image ./internal/cli/stores ./internal/cli/run -count=1
just format
```

Run relevant scoped integration (`TestImageOwnerLabel`, `TestImagesRepoAndFilterAreMutuallyExclusive`)
if a nested executor is available; no Apple deletion or Apple launch required.
Logic-change nested smoke uses the absolute launcher emitted by `just build-dev-bundle`
from a throwaway workspace, never installed `yolo`. If unavailable, preserve exact missing witness.
No full check-ci/full integration suite in this lane: Root runs the selected combined landing gate.
Only run completion census if new/changed tests read files outside their package.
Go caches remain owner-selected; inspect `go env GOCACHE GOMODCACHE GOENV`, never override or clean.

Fence (tier 3 brief, reinforced by isolated worktree): only map paths above and this plan;
no Main writes, A/C/D callers, auth/credentials, launch/install/deploy, retention counts,
new schema or optional label authority. No observed shared hunks with A/C/D.
Stop and report a dirty fenced file, missing planned symbol, original test not RED, needed
out-of-fence change, or unrelated gate failure; do not silently broaden.
Cheap choice: helper factoring and map types, provided the required unknown/empty distinction survives.
Commit message: `fix(prune): use Apple Container image probe grammar`.
Report committed SHA/ref, clean status, exact RED/GREEN commands, diffstat, native witness gap,
and durable raw evidence locations. Keep manual implementation worktree locked for Root.
