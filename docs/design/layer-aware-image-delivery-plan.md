---
title: "Plan: layer-aware image delivery (nix2container) — GATED"
date: 2026-09-08
status: draft
tags: [image, nix, podman, skopeo, prune, plan]
summary: "Hand-off for replacing streamLayeredImage + `podman load` with nix2container and a negotiating `skopeo copy` over a pinned three-tier layer plan. NOT authorized to build: the maintainer's go/no-go, a re-measured cold copier build, and one measured nix:-source boot per backend are all outstanding."
vantage:
  status-chip: true
---

# Plan: layer-aware image delivery (nix2container)

**Design:** [`layer-aware-image-delivery.md`](layer-aware-image-delivery.md) ·
**Status:** 🔒 **GATED — DO NOT IMPLEMENT.** Written against `ba072719`, 2026-09-08.

**Precedence:** the design wins on behavior; the tree wins on fact; this file is advice and is the
first thing to be wrong. Never twist code to match it — correct it in the commit.

> [!IMPORTANT]
> **Nothing here may be built yet, the flake input included.** All three parts of the gate are
> open — read [Blockers](#blockers) first. No step here is a safe warm-up.

**Which text is authoritative.** All five Open Questions were ruled 2026-09-08: the
`**Answer (…)**` blockquotes and
[§3.5](layer-aware-image-delivery.md#35-one-mechanism-no-way-back) govern, and every `_Leaning:_`
line is **history** — [OQ-LI1](layer-aware-image-delivery.md#91-decision-ledger),
[OQ-LI2](layer-aware-image-delivery.md#91-decision-ledger) and
[OQ-LI5](layer-aware-image-delivery.md#91-decision-ledger) went against or past theirs. So: no cachix dependency
and no fallback wired to a cache miss; Apple Container ships in the **same** pass;
`streamLayeredImage`, `YOLO_LEGACY_IMAGE_STREAM` and the second mechanism are **deleted in the
change that adds the new path**.
[§8](layer-aware-image-delivery.md#8-what-i-would-build-in-order)'s third step ("wire the copy
behind the env var, defaulting off") predates [OQ-LI5](layer-aware-image-delivery.md#91-decision-ledger) and is void.

## Map

| Path | Change |
| :--- | :--- |
| `flake.nix` | new `nix2container` input (`inputs.nixpkgs.follows`); `mkOciImage` `:1079-1152` → `buildImage`, three tiers; `created = "now"` `:1083` deleted; `maxLayers` `:1084` per-tier; `contents` `:1086-1092` → one `buildEnv` per tier; `fakeRootCommands` `:1095-1122` → a `runCommand` in the top tier; new `packages.imageCopier`. **`streamLayeredImage` at `:1245` (`builderImage`) stays** |
| `internal/image/nixflags.go` | a copier-attr constant beside `installPrefixAttr` `:104` |
| `internal/image/autoload.go` | `StreamLoad` seam `:120` → `LayerCopy`; the `ImageLoadStdinCmd` branch `:522-543` becomes the copy; span `:529` → `image.layer_copy`; delete `streamImageArgv` `:972`, `streamImageCommand` `:983`, `materializeImage` `:826`, `loadAppleContainerFromCache` `:673`, `convertViaSkopeo` `:1026`, `convertViaDaemon` `:1051`, the `Materialize` seam. `newestTars` `:1081` + the degraded branch stay |
| `internal/image/streamload.go` | → a `layercopy.go`; `tailWriter`, `reportPipeEnd`, `printTail` move across intact |
| `internal/image/image.go` | delete `StreamRepoTag` `:130-152` and `ImageLoadStdinCmd` `:55`; the size sentinel loses its last writer |
| `internal/prune/prune.go` `:208`, `probes.go` `:211-320` | the keep-window stops sorting by `CreatedAt` |
| `internal/cli/check/sections_nix.go` `:43` | the dry-run probe must cover the copier attr too |
| `integration/harness_test.go` `:383-429` | `ensureJailImage` executes the out-link as a stream script — must copy |
| `integration/imageskew_test.go` `:335` | the printed fix command is `./result \| podman load` |
| `Justfile` `:204-205` | `just load` pipes `./result` |
| `.github/workflows/ci.yml` `:170`, `packs.yml` `:79`, `nightly-macos.yml` `:37` | `./result > /tmp/jail-image.tar` |
| docs | by path in [Ships with](#ships-with) |

## Reuse

- **Import the input; do not use its `packages` outputs.** `import <input> { inherit pkgs; }` yields
  `{ nix2container = { buildImage buildLayer … }; skopeo-nix2container; }` — mirror
  `ociTools = pkgs.dockerTools` (`flake.nix:63`), which already runs the generator from the
  per-system `pkgs` while contents come from `imagePkgs`. **Constraint:**
  `nix2container.packages.x86_64-darwin.*` evaluates `nixpkgs.legacyPackages.x86_64-darwin`, which
  **throws** under our `follows` — the failure the input comment at `flake.nix:21-42` says cost 29
  red CI nights. MEASURED 2026-09-08: `import` from a store path works (its
  `lib.fileset.gitTracked` does not object) and resolves to skopeo **1.24.0**.
- **`copyTo`, never `copyToPodman`.** Both are passthru of the image derivation, but `copyToPodman`
  hardcodes `containers-storage:${imageName}:${imageTag}` while `copyTo` takes the destination as
  `"$@"` — only `copyTo` satisfies [§3.2](layer-aware-image-delivery.md#32-the-copy)'s "the
  destination ref is an argument". Both are `writeShellApplication`s carrying the patched skopeo on
  their own `runtimeInputs`, so it is store-resolved and never a `PATH` lookup of ours.
- Argument names: `copyToRoot`, `deps`, `layers`, `maxLayers`, `perms`, `created`, `config` — and
  `config` is unmarshalled into `v1.ImageConfig`, so `flake.nix:1125-1150`'s
  `{ Cmd; Env; WorkingDir; }` transfers **verbatim**.
- `imagePkgs.buildEnv { ignoreCollisions = true; }` — copy `yoloImageExtras` (`flake.nix:1184-1187`)
  including its `fontconfig.out` trap. One `buildEnv` per tier is also trap 2's fix.
- Unchanged, reused by name: `image.ImageStoreKey` (`gcroot.go:27`), `JailImageRef`,
  `JailImageRepository`, `AddLoadedPath`, `CurrentLoadedPath`, `RegisterImageRoot`,
  `ImageInspectCmd`, `pointLatestAt`.
- `BuildJailPrefix` + `JailPrefixOutLink` (`prefix.go`) is the shape for realizing the copier: a
  second attr, `runNixBuild` (`autoload.go:746`) for the build-and-tail contract (`""` means
  failure), and an out-link that **is** its GC root — keyed by source, **not** under `build/roots/`
  (that reaper deletes any root that is not a loaded image) and **not** named `run-result-*`
  (`SweepDanglingOutLinks` scans those).
- `tailWriter`/`reportPipeEnd`/`printTail` (`streamload.go:242-313`) for skopeo's stderr; the four
  pipe hazards documented above them stop applying (one process, no pipe).
- Counters for [§3.10](layer-aware-image-delivery.md#310-what-done-looks-like): every layer's `Size`
  is already in `image.json` (`types.Image.Layers[].Size`), so copied-vs-skipped is that map minus
  skopeo's per-blob "already exists" lines — read it with `internal/jsonx`. The bytes no longer pass
  through us, so there is nothing left to count in transit.
- `internal/prune`: `ProtectedImageTags` (`imageroots_probe.go:79`) already maps store path → tag
  through `ImageStoreKey`; recency is the sentinel's file order (`AddLoadedPath` appends last).

## Traps

- **`maxLayers` is not a popularity contest.** `nix/layers.go`'s `newLayers` emits `maxLayers - 1`
  **single-path** layers in closure-graph (alphabetical) order, then dumps every remaining path into
  one tail layer. [§3.1](layer-aware-image-delivery.md#31-the-layer-plan)'s "the base tier keeps a
  popularity split *within itself*" is unobtainable from `maxLayers`, so do not write it into a
  comment. It costs nothing — [§3.10](layer-aware-image-delivery.md#310-what-done-looks-like) item 3 sets no target for a `flake.lock` bump — and a real
  sub-split needs nested `buildLayer`s, which is a design question, not a coding one.
- **Cross-layer dedup is `reflect.DeepEqual` over `{Path, Options}`** (`isPathInLayers`), not over
  the path, so a store path carrying a `rewrite` in the base tier and no options in the top tier is
  **not** deduped — it is tarred into both. Symptom: the top layer is hundreds of MB instead of
  ~27 MB and every `flake.nix` edit re-copies it. Only top-level `copyToRoot` entries get rewrites,
  so **one `buildEnv` per tier** (packages then appear as bare closure paths everywhere) is what
  makes [§3.8](layer-aware-image-delivery.md#38-degenerate-inputs-defaults-triggers)'s "placed in
  the lowest tier that claims it" true.
- **`contents` → `copyToRoot` inverts collision resolution TWICE.** [§3.1](layer-aware-image-delivery.md#31-the-layer-plan)'s VERIFIED note covers the
  cross-tier half (`lndir` first-wins → union highest-wins). The other half is *within* a tier: two
  tar entries for one name is **last**-wins where `lndir`/`buildEnv` is first-wins — and `gcc` and
  `binutils` both ship `bin/ld` today, `gcc` winning by sitting earlier in `fullPackages`
  (`flake.nix:1006-1034`). A `buildEnv` per tier keeps that resolution where it is.
- **There is no `fakeRootCommands`.** `buildImage` has no such hook, so `/etc/passwd`, `/etc/group`,
  `./var/tmp`, `./run`, `./var/lib/containers` and the `./opt/yolo-jail/{bin,share/yolo-jail}`
  mountpoints (`flake.nix:1095-1122`) become a `runCommand` in the top tier, with `perms` for any
  mode that matters. Symptom of missing one: pid1 dies on the read-only rootfs three genSteps in,
  after a full copy — the `a813b865` shape.
- **`--created` is `time.Parse(time.RFC3339, …)`**, so `"now"` fails the **nix build**, not the
  copy. The default is already the constant `0001-01-01T00:00:00Z`: delete the line, do not pick a
  date.
- **`image.json` carries no repo:tag at all** (`types.Image` has no name field), so `StreamRepoTag`
  and `--repo_tag` have nothing left to name and the destination argv is the only name. C2 gets
  *stronger*; do not invent a manifest-side name to preserve it.
- **The integration harness is itself a delivery path** — `ensureJailImage` runs the resolved
  out-link as a program (`harness_test.go:401`), so every container test dies in `TestMain`
  until it copies. Its skew oracle then reads `readlink /etc/yolo-jail-image-identity`: keep
  `imageIdentity` in a tier whose rewrite lands `etc/` at the root (its symlink target is an
  absolute store path, which the rewrite does not touch).
- **`--accept-flake-config` is already on every flake-evaluating invocation** (`NixFlakeFlags`,
  `nixflags.go:38-44`, pinned by `TestFlakeInvocationsCarryAcceptFlakeConfig`), so [OQ-LI1](layer-aware-image-delivery.md#91-decision-ledger)'s "no path
  may require it" is a claim to **test**, not a flag to remove: the copier must build with the
  substituter absent.
- Leave `initializeNixDatabase` false — in-jail nix talks to the host daemon (`NIX_REMOTE=daemon`),
  so an in-image nix DB buys nothing and costs a sqlite layer.

## Build order

1. **Prune's keep-window** — the prerequisite
   ([§3.3](layer-aware-image-delivery.md#33-what-does-not-change)'s WARNING); it must land before a
   constant `created` reaches any machine. **Its grouping key does not exist — see
   [Blockers](#blockers).** → `go test ./internal/prune`
2. **The copier attr alone.** Input added, `packages.imageCopier`, no image change. →
   `nix build .#imageCopier`, then `nix eval --impure .#packages.x86_64-darwin.imageCopier.drvPath`
   must not throw (the image's own answer today is `…-stream-yolo-jail.drv`). Take gate part 2's
   measurement here.
3. **The manifest beside the stream.** A second image attr with the layer plan written out, consumed
   by nothing. → `jq '.layers|map({Size,n:(.Paths|length)})' result` against
   [§2.2](layer-aware-image-delivery.md#22-the-layer-sizes), then by hand
   `skopeo copy nix:./result containers-storage:localhost/yolo-jail:probe` and
   `podman run --rm …:probe readlink /bin/bash /etc/yolo-jail-image-identity`. Gate part 3's
   evidence comes from here.
4. **The collision test, before the switch**, so it has a real before-value ([§3.1](layer-aware-image-delivery.md#31-the-layer-plan)'s owed test).
   → `go test ./internal/image`
5. **The switch, in ONE commit** ([OQ-LI5](layer-aware-image-delivery.md#91-decision-ledger)): all three variants move, the jail's `streamLayeredImage`
   attrs are deleted, `LayerCopy` replaces `StreamLoad`, Apple Container's converters become one
   `skopeo copy nix:… oci-archive:<file>:<ref>`, the span is renamed, and the harness, the three
   workflows and `Justfile` follow in the same commit. → `just check-ci`, then
   `go test -count=1 -timeout 0 ./integration`
6. **Docs, error text, and R4's byte-budget test**
   ([§7](layer-aware-image-delivery.md#7-risks)). → `just check-ci`
7. **The evidence** — [§3.10](layer-aware-image-delivery.md#310-what-done-looks-like)'s four measurements on a real host plus the Apple Container boot. Not a
   code step, and **the legacy baseline has to be recorded before step 5 lands**: with no knob, the
   A/B is across commits.

## Ships with

- **Unit, by case.** The owed [§3.1](layer-aware-image-delivery.md#31-the-layer-plan) test: `/bin/bash`'s target in a built image whose `packages:`
  ships a colliding `bin/` name, asserted equal before and after the split. [§3.8](layer-aware-image-delivery.md#38-degenerate-inputs-defaults-triggers)'s degenerate
  inputs: an empty `packages:` **omits** the extras layer (never a zero-path layer), one package
  yields one extras layer, N packages still one, and no store path appears in two layers — that last
  is trap 2's mutation check, so drop the shared `buildEnv` and it must fail.
  [§3.6](layer-aware-image-delivery.md#36-failure-paths)'s failures: interrupted → no image record
  and the ref still absent; nonzero exit → **exactly one** retry; digest mismatch → **no** retry.
  Plus R4's byte budget: a `flake.nix`-only change copies under it.
- **Integration.** The harness change is itself the end-to-end exercise; add an explicit test that a
  second copy of the same store path is a no-op and that `podman image inspect <content ref>` still
  gates the copy. The unit suite stubs the runtime and can see neither.
- **Rewrites, not repairs.** All of `streamload_test.go`'s behavior cases
  (`TestPodmanHappyPathStreamsAndNeverWritesATar`, `TestTheStreamIsToldTheImageName`,
  `TestAppleContainerStillGetsAFileNotAStream`, `TestStreamAndLoadFailuresAreReportedDistinctly`,
  `TestStreamedBytesDriveProgressAndTheSizeSentinel`); `contentref_test.go`'s
  `TestTheImageIsNamedOnTheWayIn`, `TestAppleContainerIsNamedGoingIn` and
  `TestAConcurrentLoadCannotStealTheContentRef` (the mechanism changes, the property does not);
  `image_test.go`'s `TestImageCommands` and `TestSizeFileQuirk`; and `probes_test.go:163`
  `TestPruneOldImages` + `prune_test.go:66`, which assert the lexical `CreatedAt` sort — rewrite to
  the new key, never relax to green. `tareviction_test.go` goes except
  `TestCachedTarFallbackSkipsATarEvictedAfterListing`: no backend holds a tar between two steps now,
  but the degraded branch still reads legacy ones.
- **Docs describing the old thing**, by path: `docs/reference/nix-across-backends.md:75`;
  `docs/guides/USER_GUIDE.md:164`; `docs/design/minimal-disk-footprint.md:184` (the
  `streamLayeredImage` diagram), `:216`, `:318`, `:401`; [`docs/reference/image-staging-vs-baking.md`](../reference/image-staging-vs-baking.md#streaming-into-the-runtime)
  (the streaming section) and its [OQ-6](../reference/image-staging-vs-baking.md#why-its-this-way) row; `docs/guides/macos.md:791`;
  `AGENTS.md:148`; and the skill shipped into every jail,
  `internal/jailcontent/builtinskills/developing-yolo-jail/SKILL.md:64,143,162`.
- **Surfaces that are neither.** The three workflows and `Justfile:204-205`
  (`skopeo copy nix:./result docker-archive:…` replaces `./result > tar`);
  `imageskew_test.go:335`'s printed fix command; [§3.8](layer-aware-image-delivery.md#38-degenerate-inputs-defaults-triggers)'s error text (print skopeo's stderr, say no
  image was written, **name no fallback**); and `yolo check`'s dry-run probe, which without the
  copier attr reports "nothing will build" while the launch compiles skopeo for minutes.
- **Norms.** `just format` before each commit; the pre-commit hook runs `just check-ci`. Stage with
  `git add -N` then `git commit -- <paths>` — other agents are in this tree. `git add` before any
  image verification: nix sees tracked files only.
- **Cheap and yours:** the copier attr's name, the formatting of the copied/skipped counters, and
  `buildImage`'s `name`/`tag` arguments (only the unused passthru copiers read them).
- **Delete this plan when the work lands**, recording in the landing commit what it got wrong.

## Don't

- **Don't add the input or touch `flake.nix`** until the gate clears.
- Don't delete `ociTools` or every `streamLayeredImage`: `packages.builderImage` (`flake.nix:1245`)
  is the macOS offload's sshd+nix builder, pushed to GHCR by `publish.yml`, and is out of scope.
- Don't keep a legacy knob, a retained attribute, or a fallback on copy failure
  ([§3.5](layer-aware-image-delivery.md#35-one-mechanism-no-way-back)), and don't build [§8](layer-aware-image-delivery.md#8-what-i-would-build-in-order)'s env-var
  step, which [OQ-LI5](layer-aware-image-delivery.md#91-decision-ledger) voided.
- Don't delete `newestTars` or the degraded branch's `podman load -i`: that branch has no store path
  (`SkipBuild`, or a build the operator opted past), so it is not a delivery mechanism for a new
  image and [§3.5](layer-aware-image-delivery.md#35-one-mechanism-no-way-back)'s "one mechanism" does not reach it.
- Don't make the project cachix load-bearing or wire any behavior to a cache miss — a miss means the
  copier is built, and that is the whole consequence ([OQ-LI1](layer-aware-image-delivery.md#91-decision-ledger)'s three constraints). And don't
  materialize an OCI layout in the nix store
  ([§6](layer-aware-image-delivery.md#6-alternatives-considered) C):
  [OQ-5](../reference/image-staging-vs-baking.md#why-its-this-way) ruled that a bug after 404 GiB.
- Don't fold this into the prune liveness defect, and don't re-decide
  [OQ-LI4](layer-aware-image-delivery.md#91-decision-ledger)'s ordering key — cite
  [OQ-LS3](the-load-sentinel-is-not-a-liveness-oracle.md#111-decision-ledger), which makes the retention **unit**
  the configuration and subsumes the reorder.
- Don't bless the numbers from a nested jail: it can see this class ([§3.10](layer-aware-image-delivery.md#310-what-done-looks-like)'s NOTE) but cannot speak
  for the host's absolutes or its storage driver.

## Blockers

**All three parts of the gate are open. Any one of them stops all work.**

1. **The maintainer's go/no-go on adopting nix2container at all.**
   [OQ-6](../reference/image-staging-vs-baking.md#why-its-this-way) explicitly *moved* that authorization to this design and
   granted none — "the withheld authorization stays withheld".
   [§1](layer-aware-image-delivery.md#1-the-verdict)'s "Adopt nix2container" is a verdict written
   for the decider, not an approval, and the doc's own status is "DESIGN SKETCH. Nothing built."
2. **Re-measure the cold copier build on the maintainer's host, with the real `follows`-ed
   nixpkgs.** The doc's 34 s is against nix2container's *own* nixpkgs (skopeo 1.21.0) and its NOTE
   asks for this. **MEASURED here 2026-09-08 and it has already moved: 2m27s cold in this jail**
   against our nixpkgs — skopeo **1.24.0**, two derivations built, the rest substituted from
   `cache.nixos.org`. Two facts de-risk it: the `fetchpatch2` patch **applies** to 1.24.0, and the
   built binary's `nix:` transport is live (`skopeo copy nix:/tmp/nope.json …` answers
   "open …: no such file", so it parsed). The absolute number is still this jail's, not his.
3. **One measured `nix:`-source copy that loads AND boots a jail on EVERY backend that gets the new
   path** — podman/Linux, and Apple Container on the maintainer's Mac
   ([OQ-LI2](layer-aware-image-delivery.md#91-decision-ledger)). Load-bearing rather than nice-to-have because
   [OQ-LI5](layer-aware-image-delivery.md#91-decision-ledger) **deleted the fallback**: read R8 in [§7](layer-aware-image-delivery.md#7-risks). A
   delivery bug that reaches a release is a machine that cannot start a jail until a fix ships.

**And one prerequisite inside step 1 — DISCHARGED 2026-09-09, so step 1 is not blocked.** This
paragraph read: [OQ-LS3](the-load-sentinel-is-not-a-liveness-oracle.md#111-decision-ledger) rules the
keep-window's unit to be the *configuration*, but no config identity exists in the tree — C2's tag is
`sha256(storePath)[:16]`, per **image**, so every image is its own group of one. That was also the
first blocker of the sentinel plan (now deleted; the work landed).

**It was resolved by deleting the need for a config identity, not by inventing one** (`ae190ac4`).
There is no keep *window* any more: `--keep-images` REFUSES and names its replacement, because
sorting every image row by creation time and keeping the newest N had no notion of a workspace or a
configuration at all. Retention is now the union of the **per-workspace current-image pointers**
(`prune.CurrentImageTags`, written by each launch right after its image load, under the same
machine-wide housekeeping lock) and the running-container veto. So nothing here waits on a grouping
key: recency ordering ([OQ-LI4](layer-aware-image-delivery.md#91-decision-ledger)) was never the
constraint, and the grouping is not coming.
