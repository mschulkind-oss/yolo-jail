---
title: "Baking vs. staging — what the image must contain, and what a launch can deliver"
date: 2026-09-06
status: in-review
tags: [design, image, nix, podman, disk]
summary: "The measured cost model of the jail image: what forces a rebuild and a reload, what each candidate reduction buys, which of them shipped (C1–C3), and what is still open (C4/C5's go/no-go, the layer-sharing lever, the commit stamp on the bundle's binaries)."
vantage:
  status-chip: true
---

# Baking vs. staging — what the image must contain, and what a launch can deliver

**Status:** DECIDED on the five original questions ([OQ-1](#101-decision-ledger)–[OQ-5](#101-decision-ledger), [§10.1](#101-decision-ledger)) and
**IMPLEMENTED for C1–C3** (`7830f65` 2026-08-15, `be7b8591` 2026-08-25). **Two new questions are OPEN** as of the
2026-09-06 re-audit — [OQ-6](#102-open-questions) and [OQ-7](#102-open-questions), [§10.2](#102-open-questions) — and C4/C5's go/no-go is still the
maintainer's, on the evidence in [§1.8](#18-re-measured-after-c2--c3--this-is-11-step-5) and [§1.9](#19-re-measured-2026-09-06--what-a-go-only-rebuild-costs-podman-and-what-chooses-the-flake). Written 2026-08-15; re-checked against the tree
2026-08-23, 2026-08-25 (twice) and **2026-09-06**, when the body was compacted and every anchor it still
carries was re-derived.

**The question, from the maintainer, twice:** first *"what we can do to avoid cache rebuilds/reloads by
changing how we stage things"* (2026-08-15), then *"I want to stop rebuilding images so much"* (2026-09-06).

**The short version.** Moving content out of the image was the *third*-best lever, and the two better
ones have shipped. yolo's own Go code is **3.25 %** of the image closure and moves in roughly **half** of
all commits; nixpkgs is **96.75 %** and moves in well under **1 %** ([§1.1](#11-what-triggers-a-rebuild-and-how-often), [§1.2](#12-what-the-image-contains-by-size)). The image is
therefore already stratified almost perfectly — the waste was never *what* is baked, it was a pipeline
that treated a 3 %-delta image as a brand-new artifact end to end. **C1** made a failed build fail as
itself; **C2** named each loaded image by the hash of its store path, so two configs stop evicting each
other; **C3** pipes the nix stream straight into `podman load`, so no 3.3 GiB tar is written. What is
left, measured 2026-09-06: on podman a Go-only rebuild changes **2 of 99 layer digests**, but the first
change sits at **position 78 of the chain**, and overlay storage keys a layer by its parent chain — so
`podman load` re-stores the ~22 layers behind it, **about 2.7 GB per rebuild** (which is how this jail's
image store reached 38.68 GB in three days), and reads all 3.5 GB either way; a cold launch is **52 s**
against a warm **4 s**, and of that, `nix build` is ~7.9 s against ~26 s of stream-plus-`podman-load`
([§1.10](#110-re-measured-2026-09-06-continued--splitting-the-52-s-nix-build-vs-stream-vs-podman-load)) — load, not build, is where the time goes. The lever is the layer *order*, not the layer count, and
that is the candidate this doc rejected (C6) coming back as [OQ-6](#102-open-questions). Separately, on the default
launch path the image is rebuilt on **every `just install`**, because the bundle's binaries carry a
`git describe` stamp — a docs-only commit followed by `just install` mints a new 3.5 GB image for zero
functional change ([§1.9](#19-re-measured-2026-09-06--what-a-go-only-rebuild-costs-podman-and-what-chooses-the-flake), [OQ-7](#102-open-questions)).

**What is built, so nothing below is read as a plan when it is a record** (re-checked 2026-09-06):

| Item | State | Evidence |
| :--- | :--- | :--- |
| **C1** — a failed image build fails as itself | ✅ `7830f65`, 2026-08-15 | `internal/image/autoload.go:286-301` sets `buildFailed`; `:333-340` prints the classification plus nix's stderr and returns an empty `LoadResult`; opt-out `YOLO_ALLOW_STALE_IMAGE` (`internal/image/buildfailure.go:40`). [OQ-2](#101-decision-ledger) |
| **`--accept-flake-config`** on every flake-evaluating nix call ([§6](#6-the-binary-cache-alternative-argued-fairly) item 3) | ✅ `b7f2ade3`, 2026-08-17 | `NixFlakeFlags`, `internal/image/nixflags.go:32-37`; the rationale at `:9-20` cites [§6](#6-the-binary-cache-alternative-argued-fairly) item 3 by number |
| **C2** — the image is addressed by content | ✅ `be7b8591`, 2026-08-25 | `JailImageRef` (`internal/image/image.go:126`); the load decision is `image inspect <content ref>` (`autoload.go:447-449`); the ref is threaded to the argv as `assembleInput.imageRef` (`internal/cli/run/assemble.go:44`, read at `:905`). [OQ-3](#101-decision-ledger) |
| **C3** — stream, write no tar (podman) | ✅ `be7b8591`, 2026-08-25; the Apple Container arm's tar-eviction race closed by `cc53b591`, 2026-09-02 | `ImageLoadStdinCmd` (`image.go:55`) is the decision point; the pipe is `internal/image/streamload.go`, reached at `autoload.go:499-516`. `cache/images` stays unchanged on a podman load — asserted on disk (`internal/image/streamload_test.go:60`) and measured live ([§1.8](#18-re-measured-after-c2--c3--this-is-11-step-5)). [OQ-5](#101-decision-ledger) |
| **C4 · C5** — `packages:` / `fullPackages` from the mounted store | ❌ not built; **gated** | [OQ-1](#101-decision-ledger) fixed the shape (opt-in fast path, baked path kept). The gating re-measurement is [§1.8](#18-re-measured-after-c2--c3--this-is-11-step-5); [§1.9](#19-re-measured-2026-09-06--what-a-go-only-rebuild-costs-podman-and-what-chooses-the-flake) adds the one row it left unmeasured. The call is still the maintainer's ([§11](#11-what-to-do-first--dependency-ordered) step 6) |
| **The retention rule (R3)** | ✅ ruled and armed, `33c7de4e` + `d0c1f9b7` | C2 armed `yolo prune`'s old-image pass, so a dedup and a fail-safe liveness veto shipped with it (`PruneOldImages` in `internal/prune/probes.go`; `ProtectedImageTags` in `internal/prune/imageroots_probe.go`; hardened in `4064f720`). [OQ-DF3](./minimal-disk-footprint.md#OQ-DF3) ruled the pair: the NUMBER stays 2 (`prune.DefaultKeepImages`, now read by the manual pass too so the two cannot diverge) because the count was never the defect — the veto is what protects a live jail's image — and the TRIGGER is the launch path itself, debounced to once every 24 h (`prune.AutoReapOldImages`, called from `runContainer` after `autoLoadImage` so this launch's own image is already sentinel-protected). Opt out with `YOLO_NO_AUTO_IMAGE_REAP=1` |
| **The binary cache** ([§6](#6-the-binary-cache-alternative-argued-fairly)) | ✅ pushed to and read from, settled 2026-09-02 | [`../plans/handoff-cachix-cache.md`](../plans/handoff-cachix-cache.md) — CI's `push-image-cache` pushed both arches and the second variant substituted the four this-repo derivations from `yolo-jail.cachix.org`. Only the Mac-side download proof remains |
| **The flake is chosen by name, never by cwd** | ✅ `46655873`, 2026-08-31 | `reporoot.Resolve` (`internal/reporoot/reporoot.go:95-118`): `YOLO_REPO_ROOT` → bundle beside the binary → the bundle `just install` staged. Every launch prints `Flake source: … (…)` (`internal/cli/run/probes.go:49`). This changed *what triggers a rebuild* — [§1.1](#11-what-triggers-a-rebuild-and-how-often) |
| **A launch refuses when the host binary is older than the tree** | ✅ `3a348c18`, 2026-08-30 | `version.SourceSkew` (`internal/version/srcskew.go:99`) diffs the stamped commit against HEAD through `ImageSourcePaths` (`:26-35` — the `goSrc` fileset plus the two flake files, pinned to the flake by `internal/version/srcskew_test.go:157`); `refuseOnSourceSkew` (`internal/cli/run/srcskew.go:34`), overruled by `YOLO_ALLOW_SOURCE_SKEW=1` |

**The most important section is [§1](#1-the-cost-model)** (the cost model). [§5](#5-the-central-table-must-bake--could-move--already-delivered)'s table is the deliverable the original
question asked for; [§2](#2-what-the-image-contains-and-what-invalidates-each-part) is why the numbers in [§1](#1-the-cost-model) are what they are.

**Reads with:** [`minimal-disk-footprint.md`](minimal-disk-footprint.md) (executes the [OQ-5](#101-decision-ledger) ruling — this doc
measured the 404 GiB and carries the verdict that it is a bug; that one owns the fix),
[`../reference/nix-across-backends.md`](../reference/nix-across-backends.md) (the system reference for how nix is used on
each backend — the evergreen home for what here is still argument),
[`storage-and-config.md`](storage-and-config.md) (where these bytes live),
[`../plans/storage-lifecycle.md`](../plans/storage-lifecycle.md) (the 2026-07-22 baseline the growth numbers are measured
against), [`macos-user-nix-and-features.md`](macos-user-nix-and-features.md) (the backend with no image at all),
[`../plans/handoff-cachix-cache.md`](../plans/handoff-cachix-cache.md) (the binary-cache prior art, argued in [§6](#6-the-binary-cache-alternative-argued-fairly)).

> [!NOTE]
> **On anchors.** Every `file:line` below was re-derived against the tree on 2026-09-06 or is dated
> otherwise in place; where a line number would rot faster than it informs, the citation is the
> symbol (`AutoLoadImage`, `shouldMountHostNix`) and the file. `flake.nix` has moved by zero net lines
> since the 2026-08-25 sweep (`14231796` swapped one line in place), so its anchors are the
> 2026-08-25 ones, spot-checked. All measurements were taken in this development jail; every number is
> labelled **MEASURED** or **NOT MEASURED**, and dated when it is not from 2026-08-15.

---

## 1. The cost model

### 1.1 What triggers a rebuild, and how often

**The mechanism, as of 2026-09-06.** Every container launch runs `nix build .#ociImage --impure`
(`ociBuildArgv`, `internal/image/nixflags.go:47-56`; the run path never sets `SkipBuild`,
`internal/cli/run/imageload.go:33`). That build is a no-op evaluation when the derivation's output
already exists — ~1.3 s ([§1.3](#13-what-a-rebuild-actually-costs)). A **reload** happens only when the runtime lacks the image for the
resulting store path: the decision is `image inspect <content ref>` (`internal/image/autoload.go:447-449`),
and the sentinel `build/last-load-<runtime>` only explains *why* (`:459-471`). So a launch costs a
full stream-and-load exactly when the **store path** moved, and the store path moves when:

1. ~~**The flake source's Go inputs move** — the `goSrc` fileset~~ — **NO LONGER TRUE since [C8](#c8--deliver-yolos-own-binaries-by-mount-shipped-2026-09-06)
   (2026-09-06).** `goSrc` left the image derivation with `installPrefix`; the binaries are mounted.
   MEASURED there: a Go-only edit leaves `.#ociImage.outPath` unchanged. What remains on this line is
   **`flake.nix` / `flake.lock`** — measured at 1 commit in 567, and 0, in the windows below. Everything
   downstream in this section was written when the two moved together; read the frequencies as the
   history they are.
2. **`packages:` changes** ([§1.5](#15-the-multiplication-factor-packages-and---impure)) — one image per distinct list, coexisting since C2.
3. **On the default launch path, `just install` runs.** Since `46655873` (2026-08-31) the working
   directory never chooses the flake: `reporoot.Resolve` takes `YOLO_REPO_ROOT`, then a bundle beside
   the binary, then the bundle `just install` staged (`internal/reporoot/reporoot.go:84-118`). That
   bundle is "two files and a binary" — `flake.nix`, `flake.lock`, and prebuilt binaries the flake copies
   in through its short-circuit (`flake.nix:110-128`) instead of compiling `goSrc`. The prebuilt binaries
   are built by `scripts/build-go.sh:55` with `-ldflags -X …buildVersion=$(git describe --tags --dirty
   --always) -X …GitCommit=$(git rev-parse --short HEAD)` (`:48-49`, invoked from
   `scripts/stage-source-bundle.sh:115`, from `Justfile:111`). **Every commit changes that stamp, and so
   does a dirty tree**, so every `just install` yields byte-different binaries, a different
   `installPrefix`, and a different image — whether or not any `goSrc` file moved. The from-source nix
   build carries no such stamp (`go build -trimpath`, no ldflags, `flake.nix:149`), so on the
   `YOLO_REPO_ROOT` path a docs-only commit does *not* move the image. This asymmetry is
   **not measured as two store paths side by side**; it follows from content addressing and is
   [OQ-7](#102-open-questions).

   **⚠ THE LAST SENTENCE OF THAT IS NOW WRONG, AND IT WAS MEASURED — [OQ-7](#101-decision-ledger) IS MOOT.** Since [C8](#c8--deliver-yolos-own-binaries-by-mount-shipped-2026-09-06) the
   stamped binaries are not image content, so they cannot move the image's store path. MEASURED
   2026-09-06 with two bundles differing ONLY in the bytes of their prebuilt binaries — which is exactly
   what the stamp changes: both evaluate `.#ociImage.outPath` to
   `kwvlhbp8…-stream-yolo-jail`, while their `.#installPrefix` paths differ (`37pcx1hk…` vs
   `qxgcdz67…`). The same two bundles under `ce0d6324`'s flake evaluate to **different** images
   (`fbzzsgl8…` vs `39h058sw…`), which is the before-state this item describes. `just install` still
   mints a new *prefix* — a `runCommand` that copies seven files — and no image at all. (A bonus the
   measurement shows: that `kwvlhbp8…` is the SAME image the source checkout evaluates to, so the bundle
   path and the source path now agree on the image byte for byte.)
4. **Nothing else.** In-jail, bare `yolo` resolves the baked `/opt/yolo-jail/share/yolo-jail` bundle and
   builds the image it is already running; only `YOLO_REPO_ROOT=/workspace` builds from live source,
   and then `refuseOnSourceSkew` (`internal/cli/run/run.go:121`) stops a launch whose host binary is
   older than the tree through any of `ImageSourcePaths`.

A build that ran and **failed** is fatal (`7830f65`, [§7](#7-the-silent-fallback-defect--why-staging-is-worthless-without-honest-failure)), so a failure never reads as a rebuild avoided.

> [!IMPORTANT]
> **So "the image rebuilds on every launch" is true only of the `YOLO_REPO_ROOT` path.** `AGENTS.md`'s
> "two halves" bullet and the refusal text in `internal/cli/run/srcskew.go:50` still say it
> unconditionally; both predate the cwd removal by one day. On the default path the trigger is
> `just install`, and the frequency question becomes "how often do I install", which the tree cannot
> measure and this jail's logs do not record.

**MEASURED, 2026-08-15**, over the 200 commits from `23cee7a` (2026-08-05) to `9bae9f3`:

| Path set | Commits | Share |
|---|---:|---:|
| `goSrc` fileset (`cmd/ internal/ packs/ bundled_loopholes/ go.mod go.sum vendor/`) | 119 | **59.5 %** |
| `flake.nix` | 2 | 1.0 % |
| `flake.lock` | 1 | 0.5 % |
| `packs/` alone | 4 | 2.0 % |
| `vendor/ go.mod go.sum` alone | 0 | 0.0 % |
| **Union — any commit that forces a rebuild** | **121** | **60.5 %** |
| Neither — no rebuild | 79 | 39.5 % |
| (`docs/` — for scale) | 91 | 45.5 % |

(`bundled_loopholes/` was the sixth fileset entry when this was measured; it left on 2026-08-19 and
`flake.nix:102-106` keeps the record.) Over 500 commits from `c937394` (2026-07-25): union **53.2 %**,
`flake.nix` **1.2 %**.

**MEASURED again, 2026-09-06**, same method, over the commits reachable from HEAD (merges in the
denominator):

| Window | Commits | `goSrc` ∪ flake files | `flake.nix` | `flake.lock` | `docs/` |
|---|---:|---:|---:|---:|---:|
| `c26ca850` (2026-08-23) → `25f28534` (2026-09-06) | 567 | **266 — 46.9 %** | 1 | 0 | 345 |
| `183cdfda` (2026-09-02) → HEAD | 267 | **135 — 50.6 %** | 0 | 0 | 161 |
| since `be7b8591` (C2/C3) | 468 | **238 — 50.9 %** | 1 | 0 | 273 |

**The finding that reorders everything, unchanged in three windows:** the flake barely moves — once in
567 commits, and `flake.lock` not at all. What forces roughly half of all rebuilds is our own Go source,
and our own Go source is a rounding error inside the image ([§1.2](#12-what-the-image-contains-by-size)). Any proposal framed as "bake less
nixpkgs" is aimed at the sub-1 % case.

### 1.2 What the image contains, by size

**MEASURED** with `nix path-info -S` / `-r` against the image derivation at the head of the load sentinel
on 2026-08-15 (`/nix/store/q3hbzcn…-stream-yolo-jail`):

| Component | Bytes | Store paths | Share |
|---|---:|---:|---:|
| Whole image closure | 3,461,437,424 (3.22 GiB) | 577 | 100 % |
| `imageClosureRoot` — the nixpkgs half (`flake.nix:973-976`) | 3,349,065,480 (3.12 GiB) | 571 | **96.75 %** |
| Everything yolo builds (`installPrefix`, `binPathLinks`, `nix-ld`, metadata drvs) | 112,371,944 (107 MiB) | 6 | **3.25 %** |
| `installPrefix` closure alone (`flake.nix:809-837`) | 82,781,928 (79 MiB) | — | 2.39 % |
| The shipped Go binaries — **four** when measured | 39,943,902 (38 MiB): `yolo` 16.0 MB, `yolo-jaild` 9.6 MB, `yolo-entrypoint` 7.2 MB, `yolo-ps` 7.1 MB | — | 1.15 % |

`shippedBinaries` holds **seven** today (`flake.nix:808` — `yolo-cglimit` and `yolo-journalctl` added
`02438f86`, `yolo-serial` added `14231796` 2026-08-26); every `cmd/` directory but `goprobe`. The row is
left as the dated measurement — the share is a rounding error either way, which is its point.

`installPrefix` stores each binary **twice** — `/opt/yolo-jail/bin/` and the `share/yolo-jail/bin/linux-<arch>/`
bundle (`flake.nix:822-828`) — which is why 38 MB of binaries occupy 79 MB of closure. Deliberate
(`flake.nix:768-777`: a symlink would break exe-relative bundle resolution), 2 % of the image, not worth
attacking.

### 1.3 What a rebuild actually costs

**The build is cheap and the delivery is not.**

**MEASURED**, `nix build --impure --dry-run .#ociImage` on a warm store: **5 derivations** to build with no
`packages:` (`yolo-jail-customisation-layer`, `excludePaths`, `layers.json`, `yolo-jail-conf.json`,
`stream-yolo-jail` — all metadata); **6** with `YOLO_EXTRA_PACKAGES=["zbar"]` (adds `bin-path-links`, the
`/lib` symlink farm); **5** with `["hello"]` — `bin-path-links` did *not* appear, unexplained, reported as
observed.

**MEASURED**, warm eval cache, three runs each: `nix eval --impure .#installPrefix.outPath` **0.22 s**;
`nix eval --impure .#ociImage.drvPath` **1.28 s**; streaming the image derivation to `/dev/null`
**11.2 s** for 3,524,710,400 B — **299 MiB/s**. The machine has 32 cores and 125 GiB RAM; a laptop is
materially slower, macOS slower again.

**NOT MEASURED here:** a cold `nix build` of the image (a Go build plus the five derivations); `podman load`
on its own — the pipe means it is never observed apart from the stream ([§1.9](#19-re-measured-2026-09-06--what-a-go-only-rebuild-costs-podman-and-what-chooses-the-flake) says why that
matters now). **Both are measured in [§1.10](#110-re-measured-2026-09-06-continued--splitting-the-52-s-nix-build-vs-stream-vs-podman-load):** the cold build directly (~7.9 s), `podman load`'s own
share as an approximation (the pipe still prevents a clean isolation). Documented-but-not-independently-verified durations, for triangulation: **~12–13 s** for a
`packages:`-bearing `--impure` rebuild plus cold start (`integration/packages_test.go:64`); **~45 s**
for a forced in-jail rebuild + reload (`AGENTS.md`, the integration-suite knobs); **~2–5 min** for a first
build on Linux (`docs/research/platform-comparison.md:267`).

### 1.4 The amplification factor — the number this doc exists for

**MEASURED**, `nix store diff-closures` between the two most recently loaded images in
`~/.local/share/yolo-jail/build/last-load-podman`, 2026-08-15:

```
yolo-jail-install: 180.2 KiB
```

That is the entire output. One package changed; every other one of the 577 store paths was
byte-identical. For that, the pipeline built a new `stream-yolo-jail`, wrote a fresh **3.28 GiB** tar to
`cache/images/<sha16>.tar`, and ran a full `podman load` reading it back.

**Re-run 2026-09-06** on this jail's two newest sentinel entries: `yolo-jail-install: 41.9 KiB`, closures
of 3,512,637,368 and 3,512,594,488 B. Same shape.

> [!NOTE]
> **Half of the 2026-08-15 sentence is history — C3 shipped `be7b8591`.** On podman the tar is gone: the
> nix stream pipes straight into `podman load` (`internal/image/autoload.go:499-516`, the pipe in
> `internal/image/streamload.go`) and `cache/images` stays unchanged. The full load still happens; **that**
> is the half this measurement is about, and what [§1.9](#19-re-measured-2026-09-06--what-a-go-only-rebuild-costs-podman-and-what-chooses-the-flake) and [OQ-6](#102-open-questions) are about. The file form
> survives on Apple Container alone, whose converters interpolate a path (`autoload.go:517-533`,
> writing through `materializeImage`, `:786`).

For contrast, the same command between the *oldest* and *newest* entries of the ten-deep sentinel — a
`flake.lock` bump — reported chromium 150→151, gcc 15.2→15.3, icu4c 76→78, git 2.54→2.55 and ~60
more. **That** case needs a whole new image, and it happens about once per 500 commits ([§1.1](#11-what-triggers-a-rebuild-and-how-often)).

### 1.5 The multiplication factor: `packages:` and `--impure`

`nix build .#ociImage --impure` runs with `YOLO_EXTRA_PACKAGES` set from the config's `packages:`
(`config.EffectivePackages`, `internal/cli/run/imageload.go:24`); the flake reads it through
`builtins.getEnv` (`flake.nix:166-169`), which is why `--impure` exists at all.

**MEASURED**, derivation paths by `nix eval --impure`:

| Attr | no `packages:` | `["hello"]` | verdict |
|---|---|---|---|
| `ociImage.drvPath` | `4wm5csvm…` | `fzvb9xyd…` | **changes** |
| `binPathLinks.drvPath` | `nmbdb0nq…` | `6222wgbl…` | **changes** |
| `installPrefix.drvPath` / `.outPath` | `hw7r9820…` / `7d2payjy…` | same | invariant |
| `goBinaries.drvPath` | `6smyba51…` | same | invariant |

So **one package added to `packages:` produces a distinct image**, and the `installPrefix` invariance
confirms by measurement that it is the right staleness oracle for the integration suite
(`ensureJailImage`, `integration/harness_test.go:346`).

**What it used to cost, and no longer does.** Before C2 there was exactly one tag,
`localhost/yolo-jail:latest`, and the load decision compared the current store path with the single
most-recent sentinel entry. Two workspaces with different `packages:` lists therefore **reloaded the
whole image on every alternation, forever**. C2 named each image `yolo-jail:<sha16-of-store-path>`
(`JailImageRef`, `internal/image/image.go:126`), so an alternation now costs one `image inspect`
(`internal/image/contentref_test.go:221` asserts 2 loads for 2 configs and none after). The legacy tag
survives for the two jobs with no store path to hash (`JailImage`, `image.go:95`; the degraded branch at
`autoload.go:357-361`), and the sentinel survives as prune's liveness ledger and the load diagnosis.

**The scope is settled, and it is not the lever.** `packages:` is workspace-scope — `validatePackages`
(`internal/config/validate.go:215`) imposes no user-scope restriction, unlike `packs` or `host_files` —
and the maintainer ruled it stays so: *"yes, has to be"* ([OQ-4](#101-decision-ledger)). The reasoning is
[`gate-placement-principle.md`](gate-placement-principle.md) **Test 1**: `packs` and `host_files` are
user-scope because they grant **host access**; `packages:` grants a *tool*, which an agent inside the jail
can already install. A scope gate there would sit where the authority already exists, and it would cost a
repo the ability to declare its own toolchain.

> [!WARNING]
> **The cross-workspace cost was real and was never an argument for user-scoping.** It was proposed,
> argued, and refused: the ruling was to **fix the cost, never the scope**. C2 fixed it; C4 would delete
> it at the root by making the image independent of `packages:`. Do not re-derive "just make `packages:`
> user-scope" from the paragraph above.

### 1.6 What it has actually cost, on disk

**MEASURED**, this jail's state dir, 2026-08-15 — **left as the dated evidence the ruling was made on;
re-running it in place would destroy the growth series it is half of**:

| Thing | Value |
|---|---|
| `~/.local/share/yolo-jail/cache/images` | **125 tars, 404.4 GiB**, mean 3.24 GiB |
| `/nix/store` | **209 GB** |
| Root device (shared by store, home, `/tmp`, `/workspace`) | 3.7 T, **2.5 T used, 69 %** |
| Realized `*-stream-yolo-jail` / `*-install-prefix` / `*-yolo-jail-go-0-dev` store paths | **212** / **152** / **177** |
| Busiest single day of tar creation | **40 tars on 2026-07-27** (~130 GiB); 37 on 2026-08-02 |

Against the 2026-07-22 baseline ([`../plans/storage-lifecycle.md`](../plans/storage-lifecycle.md)): `cache/images` was
9.5 GiB / 3 tars and the device 1.6 TiB (45 %). Twenty-four days later, 404.4 GiB and 69 % — roughly
**+16 GiB/day of image tar** on top of the store closures.

Retention existed and was opt-in: `PruneImageCache` keeps the newest 3 by mtime (`internal/prune/imagecache.go:15`;
defaults `internal/prune/prunecmd.go:151`), reachable only through `yolo prune --apply`. Nothing calls it
automatically; the only disk-budget knob, `prune.warn_threshold_gb`, is read by `yolo check` alone
([`minimal-disk-footprint.md`](minimal-disk-footprint.md) [OQ-DF4](./minimal-disk-footprint.md#11-open-questions)'s finding). A loaded image is stored
**three times**: the store closure (~3.22 GiB, kept alive by a durable GC root, `internal/image/gcroot.go:54`),
the cache tar (3.28 GiB, podman: none since C3), and podman's own image store
(`internal/prune/prunecmd.go:321` records that the first two are separate ledgers).

**The ruling: this is a bug, not a configuration** ([OQ-5](#101-decision-ledger), 2026-08-25, verbatim: *"bug, for sure. I see no
reason to keep any of this around … we need to use minimal disk space … we've done some GC work, but
it's nowhere near enough"*). Normatively: the 404 GiB is yolo's defect, not a tuning mistake; the target
is **minimal** disk, not bounded; `yolo` may delete a user's cached tars without `--apply`; and the shipped
GC work made a GC *safe* without making one *happen*. **The fix is not designed here** —
[`minimal-disk-footprint.md`](minimal-disk-footprint.md) owns what gets deleted, when, and by whom.

### 1.7 The cost model in one paragraph — as it stood before C2 and C3

> [!NOTE]
> Kept in the past tense as the argument [§4](#4-candidates-ranked) had to answer. Roughly half of commits forced a rebuild —
> and still do. The rebuild itself was five metadata derivations plus a Go build — cheap, then and now.
> The *delivery* was 3.28 GiB written and 3.28 GiB loaded, every time, to ship a delta of 180 KiB; **C3
> retired the write term on podman**. A `packages:` entry multiplied that by the number of distinct
> lists on the machine, per launch, forever; **C2 retired the per-launch alternation**, so an extra list
> now costs one *coexisting* image ([§1.9](#19-re-measured-2026-09-06--what-a-go-only-rebuild-costs-podman-and-what-chooses-the-flake) prices it). The 404 GiB backlog is still on disk: C3 stopped the
> creation and swept nothing. **Nothing in this paragraph was fixed by moving content out of the image**
> — C2 and C3 are load-path changes — and that is still the argument C4/C5 have to answer.

### 1.8 Re-measured after C2 + C3 — this is [§11](#11-what-to-do-first--dependency-ordered) step 5

Taken 2026-08-25 in this jail after `be7b8591` and `4064f720`, tree clean. It measures what C2 and C3
*changed*; [§1.6](#16-what-it-has-actually-cost-on-disk) is deliberately not re-run. One machine, one jail (R7).

**MEASURED — wall clock, nested-jail launch running the freshly built binary:**

| Launch | What the run did | Wall |
|---|---|---:|
| **Cold** — store path changed | full build → stream → load; printed `Image load needed: nix store path changed`, `Streamed image: 3.3 GB`, `Done: loaded image` | **52 s** |
| **Warm** — store path unchanged | no load line at all | **4 s** |

**MEASURED — zero tars, by directory mtime rather than file count.** Across the cold run, `cache/images`
held **149 files before and after** and its mtime stayed at `2026-08-25 19:17:21` — the last **pre-C3**
load, which had minted a C2 content tag but still wrote a tar. The mtime is the load-bearing half: a tar
written and then unlinked would have left the count at 149 and still moved it. This is the on-disk twin
of `TestPodmanHappyPathStreamsAndNeverWritesATar` (`internal/image/streamload_test.go:60`).

> [!NOTE]
> Read "`cache/images` stays empty on success" as "stays **unchanged**" on a machine that has run yolo:
> the 149 files are pre-C3 backlog C3 deliberately did not sweep.

**MEASURED — C2's tags coexist instead of orphaning each other**, `podman system df -v` in this jail
(images section, containers column elided):

```console
REPOSITORY           TAG               IMAGE ID      CREATED     SIZE     SHARED SIZE  UNIQUE SIZE
<none>               <none>            f3f0380b0645  22 hours    3.554GB  718.5MB      2.836GB
<none>               <none>            226a6fd81f36  10 hours    3.555GB  718.5MB      2.836GB
localhost/yolo-jail  de22e97910302cee  8297369f734d  2 hours     3.555GB  718.5MB      2.836GB
localhost/yolo-jail  964291b5b71ae40f  ebe57f0bd183  2 hours     3.555GB  718.5MB      2.836GB
<none>               <none>            4e0bed933b89  58 minutes  3.555GB  3.554GB      91.36kB
localhost/yolo-jail  0a4521491d9f3f55  7c593f227b15  38 minutes  3.555GB  3.554GB      91.36kB
localhost/yolo-jail  latest            71c8b04cfe6c  27 minutes  3.555GB  718.5MB      2.836GB
localhost/yolo-jail  82f665d0341cee1d  71c8b04cfe6c  27 minutes  3.555GB  718.5MB      2.836GB
```

What it settles: **(1)** four content tags coexist as permanent names; `:latest` and `82f665d…` are one
image ID — the alias `pointLatestAt` writes downstream (`autoload.go:516`, the function at `:593`).
**(2)** `pointLatestAt` still strips `:latest` from whatever held it, so a `:latest` move can still leave
a row nameless; what C2 stopped is narrower — an image whose *only* name is `:latest` can no longer be
produced by the normal load path. **(3)** Re-streaming the *same* store path mints a new image ID,
because the flake bakes `created = "now"` (`flake.nix:982`); it costs **91.36 kB** unique. **(4)** A
coexisting image built from a *different* store path cost **2.836 GB unique** here against 718.5 MB shared
— **[§1.9](#19-re-measured-2026-09-06--what-a-go-only-rebuild-costs-podman-and-what-chooses-the-flake) reproduces it (2.725 GB) and explains it: the first changed layer sits at position 78 of 99, and every layer behind it is stored again.**

**NOT MEASURED here** (two of the four are now measured in [§1.9](#19-re-measured-2026-09-06--what-a-go-only-rebuild-costs-podman-and-what-chooses-the-flake)): the multi-workspace alternation with
genuinely different `packages:` lists; podman's incremental cost across different `packages:` closures;
Apple Container and `macos-user`; the host's own podman and `cache/images`.

#### The finding, for the [OQ-1](#the-finding-for-the-oq-1-gate) gate

**C4 and C5 remain NOT BUILT and gated.** [OQ-1](#101-decision-ledger) ruled their *shape* and left the go/no-go to this measurement;
nothing here rules on it. **(1)** C4's disk case has largely collapsed, and C3 collapsed it: on podman —
the only backend C4 runs on ([§3.2](#32-the-mounted-nix-store--the-key-lever-and-its-hard-limit)) — there is no tar. What survives is bounded and sits in podman's
ledger: one coexisting image per distinct config, whose retention rule is
[`minimal-disk-footprint.md`](minimal-disk-footprint.md) [OQ-DF3](./minimal-disk-footprint.md#OQ-DF3)'s. **(2)** What remains is time, and
C4 does not remove it: the cold 52 s is a `nix build` plus a 3.3 GB stream-and-load, driven by `goSrc`
moving in ~half of commits, and C4 touches none of that. **(3)** The one workload C4 exists for — several
workspaces with *different* `packages:` lists — has one number now: [§1.9](#19-re-measured-2026-09-06--what-a-go-only-rebuild-costs-podman-and-what-chooses-the-flake) prices a second package
closure at **3.03 GB unique** in podman's store, plus the binary-cache property in [§4](#4-candidates-ranked) C4 item 2.

**What a "yes" would cost is fixed** by [OQ-1](#101-decision-ledger): a second package-delivery mechanism maintained forever (R1).
Whether that earns its keep now that the frequency benefit is C2's and the disk benefit is C3's is the
maintainer's call, and [§11](#11-what-to-do-first--dependency-ordered) step 6 stays as written.

### 1.9 Re-measured 2026-09-06 — what a Go-only rebuild costs podman, and what chooses the flake

Taken during the re-audit, in this development jail's own (nested) podman, which by then held **24
images, 38.68 GB** (`podman system df`), **23 of them minted across 2026-09-04/05/06** (13, 8, 2 —
`podman images --format '{{.CreatedAt}}'`, which is the stream time because of `created = "now"`). That is
the dev-loop rate: nested-jail verification plus the integration suite. The host's rate is **NOT
MEASURED** — `/ctx/host-yolo-logs` carries broker and crossings logs only, no launch records.

**MEASURED — a Go-only rebuild changes two layer digests, and podman stores twenty-two layers.** Every
pair of `packages:`-free images in that store has **99 layers with 96–97 in common**
(`podman image inspect --format '{{json .RootFS.Layers}}'`, ten pairs compared, the newest being
`aa2fc084dd62` 2026-09-06 03:34 and `f6913572306c` 2026-09-05 14:53) — and in every pair **the first
differing layer is at position 78 or 79 of 99**. That position is the whole cost. Overlay storage keys a
layer by its parent *chain*, not by its diff digest, so a layer whose bytes are identical but whose parent
moved is a new layer: `podman load` stores everything from the first change to the top again. `podman
system df -v` prices it — an image with no same-store-path twin shows **850.6 MB shared / 2.725 GB
unique** (`aa2fc084dd62`, `f6913572306c`, and five more at 826–851 MB / 2.72–2.75 GB), and the 2026-08-25
figure in [§1.8](#18-re-measured-after-c2--c3--this-is-11-step-5) row 4 (718.5 MB / 2.836 GB) is the same class. The six rows at 3.377 GB shared /
188–195 MB unique are images that have a **re-stream twin** — same store path, identical chain to the
last layer — so their sharing is with the twin, not with the next rebuild; the eight rows at 91.3 kB
unique are those twins. **So a Go-only rebuild costs podman ~2.7 GB of storage and a read of the whole
3.5 GB stream, to deliver a 41.9 KiB closure delta** ([§1.4](#14-the-amplification-factor--the-number-this-doc-exists-for)). The write is not the layer *count* — 97 of 99
digests are reused — it is the layer *order*: yolo's own content sits at position ~78 among the other
leaves of a popularity-ordered `streamLayeredImage` (`maxLayers = 100`, `flake.nix:983`, over ~577 store
paths), and every leaf behind it is re-chained.

**MEASURED — a different `packages:` closure re-cuts the layers from much lower down.** The 3.756 GB
images in the same store are the integration suite's `packages:` images — confirmed by listing `/lib`
inside one (`298520515082`: 9 `libzbar`/`libsodium` entries; the stock `639f5cb6836b`: 0). Against stock,
that image has **93 layers, 56 in common**, priced at **723.7 MB shared / 3.032 GB unique**. This is the row
[§1.8](#18-re-measured-after-c2--c3--this-is-11-step-5) left NOT MEASURED: a package set that changes the `/lib` farm shifts the layer assignment and
roughly 40 % of the digests change, not 2. **Each distinct `packages:` list therefore costs ~3 GB of podman
storage** — only marginally more than a Go-only rebuild, which is the one cost C4 still addresses and the
number [OQ-DF3](./minimal-disk-footprint.md#OQ-DF3)'s retention rule has to price.

> [!WARNING]
> **The 2.7 GB is per rebuild, not per tag, and it is what fills a podman store.** Twenty-three loads in
> three days at ~2.7 GB each is the 38.68 GB above, and `--keep-images 2` ([§9](#9-risks) R3) has never run here.
> The premise under C6's 2026-08-15 rejection — "`podman load` skips layers it already has" — is TRUE
> only for the chain *prefix* up to the first moved layer, and FALSE for everything behind it. Do not
> re-derive "layers dedup, so a rebuild is cheap" from the 97-of-99 figure alone.

**What this changes.** C6 ([§4](#4-candidates-ranked)) was rejected for pricing the *tar*; the tar is gone (C3), and the cost the
rejection did not price is now measured: ~2.7 GB stored and 3.5 GB read per Go-only rebuild. A **stable
chain** — every nixpkgs layer first, yolo's own content in the trailing layers over a base whose chain
never moves — cuts the store cost to the size of those trailing layers by construction. Whether it also
cuts the 52 s cold path depends on how that time splits between `nix build`, stream generation
(≥ 11.2 s, [§1.3](#13-what-a-rebuild-actually-costs)) and `podman load` — **now split, [§1.10](#110-re-measured-2026-09-06-continued--splitting-the-52-s-nix-build-vs-stream-vs-podman-load)**.

### 1.10 Re-measured 2026-09-06 (continued) — splitting the 52 s: `nix build` vs. stream vs. `podman load`

[§1.9](#19-re-measured-2026-09-06--what-a-go-only-rebuild-costs-podman-and-what-chooses-the-flake) priced the storage half of C6's case; this is the time half [OQ-6](#102-open-questions) asked for. Taken in this jail's own
podman, no nested launch, on a **real** Go-only change: `ce0d6324` (this session's HEAD) touches
`internal/capture/relocate.go`, `internal/capture/store.go`, `internal/pluginpack/pluginpack.go` and one
comment line in `flake.nix` — a `--dry-run` confirmed 7 derivations needed building, `yolo-jail-go-0-dev`
among them, so this is the class of rebuild [§1.9](#19-re-measured-2026-09-06--what-a-go-only-rebuild-costs-podman-and-what-chooses-the-flake) priced, occurring naturally rather than manufactured for
this measurement (this task's brief forbids editing `flake.nix` or any Go file).

**MEASURED — `nix build`, split by dependency stage since the two do not fall inside one stopwatched call**
(the second call sees the first stage already valid and does not redo it): `nix build .#goBinaries --impure
--rebuild --no-link`, three runs, wall clock: **6.528 s, 6.636 s, 6.542 s** (mean **6.57 s**) — the Go
compile. `nix build .#ociImage --impure --rebuild --no-link` immediately after (goBinaries now valid, only
`stream-yolo-jail.drv` and its metadata siblings re-checked), three runs: **1.371 s, 1.354 s, 1.344 s** (mean
**1.36 s**). **Total nix-build phase ≈ 7.9 s.** The same-session real build of `ce0d6324`
(`nix build .#ociImage --impure --out-link …`, the same 7 derivations, exit 0) is the build this
decomposition describes, but its own wall clock was not separately captured — this section's number is the
sum of the two isolated re-runs above, not a single stopwatched original.

**MEASURED — stream generation alone**, the built `stream-yolo-jail` script run to `/dev/null` (the same
method [§1.3](#13-what-a-rebuild-actually-costs) used): three runs, 3,569,756,160 B (3.32 GiB) each: **8.886 s, 8.366 s, 8.171 s** (mean
**8.47 s**, 374–407 MiB/s) — faster than [§1.3](#13-what-a-rebuild-actually-costs)'s 299 MiB/s, plausibly a warm page cache over the store
paths this session had already touched; the point is the *shape*, not the exact rate.

**MEASURED — stream piped into `podman load`**, the actual production mechanism (`internal/image/streamload.go`),
run twice on the same never-before-loaded store path (`podman rmi` between runs to force each one cold —
podman would otherwise recognize the content and no-op): **24.984 s, 27.093 s** (mean **26.04 s**). Both
runs added exactly one new image at **2.719 GB unique / 850.6 MB shared** — the identical class [§1.9](#19-re-measured-2026-09-06--what-a-go-only-rebuild-costs-podman-and-what-chooses-the-flake) priced
(2.719–2.745 GB there), an independent same-day replication. Both loaded images were removed after
measurement (`podman rmi -f`); the store returned to its pre-measurement 38.68 GB.

**What this splits out.** `podman load`'s own share is not directly observable — piping means the read and
the write overlap, the same limitation [§1.3](#13-what-a-rebuild-actually-costs) and [§1.9](#19-re-measured-2026-09-06--what-a-go-only-rebuild-costs-podman-and-what-chooses-the-flake) already flagged — but the **difference of means**,
26.04 s − 8.47 s ≈ **17.6 s**, is podman's added cost to write ~2.72 GB of new layers (≈155 MB/s
effective), against 8.47 s to merely read the 3.32 GiB stream. Summing the two measured phases as the
production pipeline actually runs them (build, then stream-into-load): **7.9 s + 26.0 s ≈ 34 s** — build is
**~23 %**, stream-and-load **~77 %**, and podman's write alone (17.6 s) already exceeds the entire build
phase (7.9 s).

> [!NOTE]
> **34 s accounts for most, not all, of the 52 s−4 s = 48 s delta [§1.8](#18-re-measured-after-c2--c3--this-is-11-step-5) attributes to the cold path.** The
> ~14 s gap is not measured here: it is most plausibly the nested-jail launch's own container-creation and
> entrypoint-provisioning overhead, which a direct `nix build` / `podman load` pair (this measurement) never
> exercises and which this task was explicitly told not to re-launch to check. Read this section as "where
> the *image pipeline's* 48 s goes," not as a reproduction of the full nested-launch number.

**What it settles.** Load (stream-read plus podman-write) is the clear majority of the image pipeline —
~3.3× the build phase counting the whole stream-and-load pipeline, and still ~2.2× the build phase counting
podman's write share alone. Per the decision rule this doc and [OQ-6](#102-open-questions) already stated: since the load's
read is *not* even the majority *within* the load (podman's write is), the lesson sharpens rather than
reverses — the largest unbuilt lever is a thin image built against an **already-loaded base ref**, not a
`fromImage` base (which re-emits the base layers into the stream and would add to, not remove, the write
side measured here). This re-ranks C6 above C4 on the measured workload; it is not, by itself, authorization
to build it — see [OQ-6](#102-open-questions).

---

## 2. What the image contains, and what invalidates each part

Read `flake.nix` as four strata; they correspond almost exactly to the size/frequency split in [§1](#1-the-cost-model).

**(a) Package sets — 96.75 % of the closure, invalidated by `flake.lock`.** `corePackagesFromNixpkgs`
(`flake.nix:848-903`) is what the integration suite touches plus POSIX essentials; `fullPackages`
(`:909-937`) is the bulk it does not — chromium, gcc, binutils, nix, podman, tmux, bat, eza, delta, fzf.
The minimal variant drops the second set, documented as **~1.6–2 GB smaller** (`Justfile:198-199`), not
independently measured.

**(b) Our own Go build — ~~2.4 %~~ NOT IN THE IMAGE since [C8](#c8--deliver-yolos-own-binaries-by-mount-shipped-2026-09-06) (2026-09-06).** `goBinaries`
(`flake.nix:122-155`) still compiles every `cmd/*` in one derivation — or copies the bundle's prebuilt
binaries in (`:110-128`) — and `installPrefix` (`:817`) still copies the seven `shippedBinaries`
(`:816`) into `/opt/yolo-jail/bin/` plus the flake bundle. **What changed is who consumes it:** the
launch bind-mounts that prefix, and `corePackages` (`:971`) carries only `jailPrefixLinks` (`:870` — the
`/bin/<name>` NAMES) and `imageIdentity` (`:892`). So this stratum is now a *launch* input, not an image
input, and the two strata that used to move together no longer do.

The `goSrc` fileset trap survives the move intact, one layer over: a top-level Go package outside the
fileset now vanishes from **the mounted prefix** while `go build ./...` stays green — same silence, same
fix (`flake.nix:94-107`).

**(c) Generated-into-the-image content.** `mkBinPathLinks` (`flake.nix:540-748`) is one `runCommand`
producing the FHS symlinks (`:543-549`); the nix-ld interpreter at `/lib/` and `/lib64/` (`:581-583`); the
`/lib` + `/usr/lib` farm for the core trio and chromium's stack, and the **user-package** half of it from
`extraLibPackages` (`:636-646`); the nested-podman `/etc` files (`:682-715`); and three symlinks into
`/run` — `/etc/localtime`, `/etc/timezone` (`:563-564`) and the `ld.so.cache` under `/etc` (`:747`). `fakeRootCommands`
(`:992-1014`) adds mountpoint dirs and `/etc/passwd` + `/etc/group`. **The `/run` symlinks are this doc's
pattern already in production: the image bakes a stable name and the boot path supplies the content.**

**(d) `config.Env`** (`flake.nix:1022-1039`): `PATH=/bin:/usr/bin`, `SSL_CERT_FILE` (`:1024`),
`LD_LIBRARY_PATH=/lib:/usr/lib:/usr/lib/<multilib>` (`:1025`), `TZDIR` (`:1038`) and friends. Two of these
are literal store paths burned into the image config — moving `cacert` or `tzdata` out means moving the
env too.

**What `installPrefix` covers.** Exactly the `goSrc` fileset plus the flake files, invariant across
full/minimal and across `packages:` ([§1.5](#15-the-multiplication-factor-packages-and---impure), MEASURED). It does **not** cover the package sets' content,
`binPathLinks`, or anything in `fakeRootCommands` — which made it a good oracle for "is the loaded image
built from this tree's Go code" and a bad one for "is the loaded image current".

> [!IMPORTANT]
> **It is no longer an image oracle at all**, because it is no longer in the image ([C8](#c8--deliver-yolos-own-binaries-by-mount-shipped-2026-09-06)). The
> question it answered — "does the loaded image carry this tree's Go code?" — has no referent: the
> binaries a jail runs are mounted from the tree, so they cannot be stale relative to it. What replaced
> it is `imageIdentity` (`flake.nix:892`), a derivation over `flake.nix` + `flake.lock` and nothing
> else — the image's remaining input set — read back out of a loaded image as
> `readlink /etc/yolo-jail-image-identity` (`integration/imageskew_test.go`).

---

## 3. Delivery mechanisms that already exist

The palette. Every row ships today; [§4](#4-candidates-ranked) extends the pattern, it does not invent one.

### 3.1 The boot-written anchors

`~/.yolo/bin/block` (blockers — refuse, suggest, `exit 127`) and `~/.yolo/bin/launch` (lazy
installers/updaters — install or update on use, then `exec`) are **generated at boot by
`internal/entrypoint`, not baked** (`GenerateShims`, `GenerateAgentLaunchers`,
`GeneratePackageManagerLaunchers`, each under `genStep` at `internal/entrypoint/boot.go:457-461`, so a
failure is fatal and collected). Both live under one bind-mount anchor at `~/.yolo/bin`
(`e.BlockDir()` / `e.LaunchDir()`, `internal/entrypoint/env.go:277`, `:312`; the mount from
`<ws>/.yolo/home/yolo-bin`, `internal/cli/run/assemble_parts.go:118`) under a `:ro` `/home/agent` (`:107`),
and are cleared contents-only (`resetAnchorDir`, `internal/entrypoint/shims.go:27`). They were
`~/.yolo-shims` and `~/.yolo-launchers` until `a813b865` (2026-08-30); `flake.nix:1018-1021`'s comment
still names the old dir. PATH has one authority, `BootPath` (`boot.go:376`); the second, hand-spelled copy
this section once flagged as disagreeing with it was deleted (`boot.go:566-572`).

**The ordering constrains [§4](#4-candidates-ranked), and it changed on 2026-09-04** (B2, [`program-delivery.md`](program-delivery.md)
[§3.5](./program-delivery.md#35-the-second-axis-who-the-dependency-serves-amendment-2026-09-03)). The launch dir used to be *last*, after `/bin`, so a pack-declared `program fzf` could not shadow
`/bin/fzf` by position — which also made a launcher unreachable past its first install. It is now
**second**, and the protection is a generation-time check (`internal/entrypoint/launchercollision.go`): no
launcher is written for a name the image or a declared mise tool provides. A boot-written delivery dir
still **cannot shadow anything the image bakes** — by a check rather than by position, a weaker
guarantee. A candidate that moves a package *out* of the image into a launch-time dir is safe on that
axis; one that leaves it baked *and* stages it is not — the baked copy silently wins (R2).

### 3.2 The mounted nix store — the key lever, and its hard limit

When `shouldMountHostNix` says so, the launch mounts the daemon socket read-write, the store `:ro`, and
sets `NIX_REMOTE=daemon` (`internal/cli/run/assemble.go:364-370`). **The entire host store is then
visible inside the jail, and any store path is runnable by absolute path without being in the image.**
yolo's own code relies on it: `streamImageCommand` (`internal/image/autoload.go:943`) returns a bare store
path as argv, and on the nested-jail dev loop that path exists only because `/nix/store` is bind-mounted.

The gate (`internal/cli/run/hostprobes.go:15-30`):

| Condition | Store mounted? |
|---|---|
| socket or store missing on host | **no** |
| `rt == "container"` (Apple Container) | **no** |
| Linux + podman + host daemon | **yes** |
| macOS + podman | **no** unless `YOLO_NIX_HOST_DAEMON` is truthy |
| `macos-user` | n/a — returns before image load (`internal/cli/run/run.go:231` vs the load at `:731`) |

**Every candidate that depends on the mounted store is Linux + podman only, and requires a running nix
daemon.** I found no fallback that lets Apple Container reach a store path. This is the largest
constraint in the design. The socket is mounted read-write, gated on Linux by nothing but path existence
— see R8.

### 3.3 The other three backends' shapes

- **podman** — `/home/agent` is a `:ro` bind of `GlobalHome()` with rw anchors inside it
  (`internal/cli/run/assemble_parts.go:107-118`).
- **Apple Container** — no store mount; one writable `/home/agent`; cannot bind-mount a single file
  (apple/container#1089, so `acMaterialize`, `internal/cli/run/helpers.go:105`, copies) and silently
  ignores `:ro` (apple/container#889). Pack staging does not even cross as a mount: `YOLO_PACK_ROOT` is
  the *host* path (`assemble.go:615-618`). Anything staged as a *file* is copied here.
- **`macos-user`** — no container, no image (`internal/entrypoint/darwin.go:81`), no bind mounts. It already
  solves the problem the other way: `packages:` is a **`buildEnv` profile whose `bin` is prepended to
  PATH** (`flake.nix:1204`). **That is C4's mechanism, already a reusable Go package:** `internal/darwinpkg`
  builds `.#yoloNoncontainerPackages` with `YOLO_EXTRA_PACKAGES` (`darwinpkg.BuildEnv`,
  `internal/darwinpkg/darwinpkg.go:104`), GC-roots it (`ProfileRootLink`, `internal/darwinpkg/gcroot.go:60`),
  and its package comment declares the mechanism platform-neutral with Linux as the next consumer
  (`darwinpkg.go:1-15`). The host half of C4 exists and is tested; the jail-side wiring does not.

### 3.4 Everything else already delivered at launch

Bind mounts carry `/workspace`, `/home/agent` and its rw anchors, the nix socket + store, the `--read-only`
scratch mounts, `/ctx/packs`, `/ctx/host-*`, `/mise` (`assemble_parts.go:158-160`, always a mount) and
host-composed git identity. `flake.nix:999-1006` records that podman creates a `/ctx` mountpoint on demand
even under `--read-only`, so **a new `/ctx` consumer needs no flake edit** — the cheapest extension point
in the system. Boot-time generation, every boot, carries the timezone files (`boot.go:447`), the
`ld.so.cache` under `/run` (`:451`), the anchor dirs ([§3.1](#31-the-boot-written-anchors)), `.bashrc`, the MCP wrappers, every pack surface in one
loop (`ConfigurePackSurfaces`, `:536`), and user `host_files`. Agent CLIs npm-install into the rw
`npm-global` bind (`assemble_parts.go:108`); mise tools install into `/mise`; only `mise` itself is baked.

> [!NOTE]
> **One class went the other way on purpose.** `yolo-cglimit` and `yolo-journalctl` used to be scripts
> generated in-jail; they are baked binaries now, and `RemoveStaleGeneratedClients`
> (`internal/entrypoint/scripts.go:40`, run at `boot.go:553`) exists only to unlink what an older
> entrypoint wrote, because `~/.local/bin` precedes `/bin` ([`loophole-transport.md`](loophole-transport.md)
> [§8.4](./loophole-transport.md#84-what-did-not-change-and-what-is-still-owed)).

**The third column of [§5](#5-the-central-table-must-bake--could-move--already-delivered) is the largest.** Almost everything mutable is already staged. What remains
baked is baked because it is needed before yolo code runs, or because it is a nixpkgs package — the
96.75 % that almost never changes.

---

## 4. Candidates, ranked

Ranked by (frequency × cost) ÷ (risk + work), using [§1](#1-the-cost-model)'s measured frequencies.

### C1 — Make a failed image build fail as itself. **Rank 1. SHIPPED `7830f65`, 2026-08-15.**

Not a staging change; a precondition. Every failed build, every backend. [§7](#7-the-silent-fallback-defect--why-staging-is-worthless-without-honest-failure) is the record; [OQ-2](#101-decision-ledger)
the ruling.

### C2 — Address the loaded image by content, not by the `:latest` tag. **Rank 2. SHIPPED `be7b8591`, 2026-08-25.**

**As built.** The loaded image is named `yolo-jail:<sha16-of-store-path>` — the key `keyFor`
(`internal/image/image.go:290`, exported as `ImageStoreKey`, `gcroot.go:27`) already computed for the GC
root; `JailImageRef` composes it (`image.go:126`). The old single-most-recent-path comparison is gone;
the question is "is *this* ref present", asked by `image inspect` (`autoload.go:447-449`). [OQ-3](#101-decision-ledger) chose this
over the cheaper "keep `:latest`, test LRU membership" variant.

**The image is named ON THE WAY IN, and that is load-bearing.** The first implementation loaded and then
ran `podman tag :latest <ref>` — reading a shared, mutable name a second time with nothing serializing
loads across workspaces; a concurrent load could bind this config's ref to another config's image, and
because a tag is permanent the wrong image would then be run *forever*. nixpkgs' stream script takes
`--repo_tag`, so the name goes into the archive: `StreamRepoTag` (`image.go:152`) → `streamImageArgv`
(`autoload.go:932-937`) → `podman load`. `:latest` is pointed at the new image *downstream*
(`pointLatestAt`, `autoload.go:516`, `:593`), best-effort, so the degraded branch still has a name to ask
about.

> [!WARNING]
> **Do not "simplify" C2 back into an LRU-membership test on `:latest`.** The pre-C2 comment is preserved
> in the shipped code (`autoload.go:418-446`, the quote at `:426-430`) as the argument **for** C2: *"nix
> builds are content-addressed: reverting a config change can reproduce a store path that's still in the
> history from an earlier load, even though a different, newer path has since become `:latest`."* That
> describes not knowing what `:latest` is. Equality was the least-wrong answer while one tag named every
> image; LRU membership reintroduces exactly the bug it warns about; content addressing dissolves the
> question — when the ref *is* the hash, "is this ref present" cannot be ambiguous.

**The tag is not a public surface** — the maintainer, verbatim: *"for container images? definitely not."*
Any code that hardcodes `localhost/yolo-jail:latest` is a bug, not a compatibility constraint.

> [!NOTE]
> **The cachix caveat in the same ruling is a different surface.** *"Although we have plans on making
> cachix useful"* is about the **nix binary cache** ([§6](#6-the-binary-cache-alternative-argued-fairly)) — addressed by store path, not by podman tag. "The
> image tag is not public" is no licence over a cachix artifact name, a flake attr (`.#ociImage`,
> `.#ociImageMinimal`) or the substituter config.

> [!NOTE]
> **C2 armed a prune pass that had never fired — read this before touching `PruneOldImages`.** It filters
> by repository and removes with `rmi -f`, which destroys the containers using the image; while one tag
> named everything the query returned one row and `keep=2` could select nothing. Per-config tags return a
> row per **name**, and measured on the maintainer's host the day C2 landed, `keep=2` selected the
> second workspace's live image. The same change deduped by image ID and added a liveness veto from the
> load sentinel (`PruneOldImages`, `internal/prune/probes.go:256`; `ProtectedImageTags`,
> `internal/prune/imageroots_probe.go:77`); `4064f720` made the veto fail **safe** — the sweep declines
> when the ledger cannot be read. The retention *number* is still [OQ-DF3](./minimal-disk-footprint.md#OQ-DF3)'s.

**What it bought.** The [§1.5](#15-the-multiplication-factor-packages-and---impure) thrash is gone; alternating workspaces costs an `image inspect`. **What it
costs.** Coexisting images — ~2.7 GB apiece for a Go-only rebuild, ~3 GB for a different `packages:`
closure ([§1.9](#19-re-measured-2026-09-06--what-a-go-only-rebuild-costs-podman-and-what-chooses-the-flake)) — under a retention rule that is still `--keep-images 2` sorted by CreatedAt (R3).
**Verified by** `internal/image/contentref_test.go`: `:221` asserts 2 loads for 2 configs and none on
alternation; `:268` that the archive carries the content name; `:347` that a concurrent load cannot steal
the ref. Backends: podman and Apple Container.

### C3 — Stop writing a 3.28 GiB tar on the load path. **Rank 3. SHIPPED `be7b8591`, 2026-08-25.**

**As built.** `just load` had demonstrated the pipe all along (`./result | {{runtime}} load`,
`Justfile:205`); C3 is that in Go with failure detection a shell pipeline lacks. The decision point is
`ImageLoadStdinCmd` (`internal/image/image.go:55`) — `podman load` reads stdin without `-i`, and Apple
Container is *unrepresentable* there because its converters interpolate a path. The pipe is
`internal/image/streamload.go` (bytes counted in transit, `copyCounting`, `:209`), reached through the
`StreamLoad` seam (`autoload.go:499-516`). The cached-tar **shortcut** is gone from the podman branch and
the code says why (`:482-494`): a tar at that name is now a legacy artifact, and preferring an unverified
file to a verified stream would let one truncated leftover brick a workspace.

**What it kept working.** The build-failure fallback still loads a tar `newestTars` finds
(`autoload.go:366-397`, the function at `:1041`; `TestBuildFailureFallbackStillLoadsAnExistingTar`,
`streamload_test.go:218`) — C3 removed the creation of tars, not the ability to consume one. Apple
Container still materializes a file (`materializeImage`, `:786`, via `loadAppleContainerFromCache`,
`:648`), and `cc53b591` (2026-09-02) made that arm survive a concurrent `yolo prune` evicting the tar
mid-launch by re-materializing once rather than failing. `convertViaSkopeo` (`:986`) writes a second
full-size tar during conversion and removes it when the load returns — peak disk, not accrued.

**The verdict [OQ-5](#101-decision-ledger) handed it.** This section originally hedged for "keep N tars, stream the rest". The
ruling reversed the burden of proof: tars are a **bug**, so the target is **zero retained tars**, and the
floor — [`minimal-disk-footprint.md`](minimal-disk-footprint.md) [OQ-DF1](./minimal-disk-footprint.md#11-open-questions) — was ruled the same day, *"stream,
keep zero tars"*. C3 is that ruling implemented. **Verified on disk** (`streamload_test.go:60` fails the
`Materialize` seam outright and asserts `cache/images` empty; the four pipe-failure classes are driven
through a real pipe and told apart) and live ([§1.8](#18-re-measured-after-c2--c3--this-is-11-step-5)). Backends: podman for the pipe form.

### C4 — Deliver `packages:` from the mounted store instead of baking it. **Rank 4. Shape RULED 2026-08-25; go/no-go still gated.**

**Mechanism.** Stop threading `YOLO_EXTRA_PACKAGES` into the image build. On the host, realize a
`buildEnv` of the config's `packages:`; at boot, symlink its `bin` into a boot-written PATH dir, its
`lib/*.so*` into a boot-written `LD_LIBRARY_PATH` dir, its `lib/pkgconfig` into a `PKG_CONFIG_PATH` dir.
The store paths resolve because `/nix/store` is mounted ([§3.2](#32-the-mounted-nix-store--the-key-lever-and-its-hard-limit)). **The host half is
`internal/darwinpkg`, verbatim** ([§3.3](#33-the-other-three-backends-shapes)); the jail half is a `genStep` beside the anchor generators,
fatal-on-failure for free ([§3.1](#31-the-boot-written-anchors)). Unbuilt: the wiring and the lib-farm story.

**What it buys.** The image stops depending on `builtins.getEnv`, so `--impure` leaves the run path and
the image becomes **one artifact per machine** — the [§1.5](#15-the-multiplication-factor-packages-and---impure) multiplication deleted at the root; the image
becomes cacheable for `packages:` users, who today are a guaranteed cache miss ([§6](#6-the-binary-cache-alternative-argued-fairly) item 2); adding
a package costs a small `buildEnv`, not a ~3 GB coexisting image ([§1.9](#19-re-measured-2026-09-06--what-a-go-only-rebuild-costs-podman-and-what-chooses-the-flake)).

**What breaks.** `LD_LIBRARY_PATH` is baked (`flake.nix:1025`) and the user half of the `/lib` farm is
image content on a `--read-only` root (`:636-646`); a scrubbed-env consumer cannot be rescued (`:728-732`;
the nix-ld fallback dir `:592-599` is the only surviving search path — R5). The two `packages:`
integration tests assert baked paths (`integration/packages_test.go:87`, `:101-102`). Podman + Linux + nix
daemon only; Apple Container and macOS-podman keep baking, so two mechanisms are maintained.

**The [OQ-1](#101-decision-ledger) ruling, both halves.** **(1) Shape settled:** if C4/C5 ship, they ship as an **opt-in fast path
with the baked path retained** for the backends and launches that do not opt in. The "accept a documented
asymmetry and make store-delivery *the* mechanism" branch is no longer live. **(2) Go/no-go NOT settled**
— the gate is [§11](#11-what-to-do-first--dependency-ordered) step 5, taken as [§1.8](#18-re-measured-after-c2--c3--this-is-11-step-5) and extended by [§1.9](#19-re-measured-2026-09-06--what-a-go-only-rebuild-costs-podman-and-what-chooses-the-flake): the disk term is C3's, the
frequency term is C2's, the 52 s cold path is one C4 does not shorten, and the one cost it still removes
is ~3 GB of podman storage per distinct `packages:` list.

> [!WARNING]
> **"Retained as fallback" is per LAUNCH, never per package — R2 is why.** A package both baked and staged
> silently runs the **baked** copy. The only shape that satisfies the ruling and R2: a launch that opts in
> builds the stock image with no `YOLO_EXTRA_PACKAGES` and gets its packages from the store; a launch that
> does not builds the baked image and gets them from `/bin`. Exactly one mechanism is live in any jail.
> **A ruling on shape is not approval to build, and neither is a measurement.** C4 stays unbuilt until
> the maintainer says otherwise.

### C5 — Move `fullPackages` out of the run-path image. **Rank 5. Shape RULED 2026-08-25 with C4.**

Same mechanism as C4, applied to `flake.nix:909-937`; the run path then builds the minimal variant, ~1.6–2 GB
smaller (`Justfile:198-199`, documented not measured), on every rebuild. **What breaks:** the "cannot shadow
the image" invariant in [§3.1](#31-the-boot-written-anchors) inverts for every name that moves out of `/bin` (`fzf` vs a pack's
`program fzf`), and chromium drags the `withChromium` half of `mkBinPathLinks` (`flake.nix:565-569`) —
font links and `/etc/fonts`, baked content. **Verdict:** best size-per-risk *after* C4 exists, because it
reuses the mechanism; building it first builds that mechanism for the lower-value case. [OQ-1](#101-decision-ledger)'s opt-in
shape contains the inversion to launches that opt in.

### C6 — Layer the image so the delta is the unit of transfer. **Rejected 2026-08-15; premise MEASURED 2026-09-06 — re-opened as [OQ-6](#102-open-questions).**

The 2026-08-15 reasoning: `streamLayeredImage` with `maxLayers = 100` (`flake.nix:983`) already gives popular
store paths their own layer and `podman load` skips layers it already has, so a thin top image over a
stable base would only shrink the *tar* — subsumed by C3 for podman, unproven for Apple Container's skopeo
conversion. **Both halves of that premise were wrong in the way that matters.** [§1.9](#19-re-measured-2026-09-06--what-a-go-only-rebuild-costs-podman-and-what-chooses-the-flake) measures that
`podman load` skips only the layers *before* the first moved one: a Go-only rebuild changes 2 of 99
digests at position 78, and podman stores the ~22 layers behind it again — **~2.7 GB per rebuild**, which
is how 23 loads made a 38.68 GB image store in three days — while 3.5 GB still cross the pipe and the cold
launch is 52 s against 4 s warm. So the "stable base" C6 described is not about the tar at all: it is
about the **chain**. Put every nixpkgs layer first and yolo's own content (`installPrefix`, `binPathLinks`,
the customisation layer) in the trailing layers over a base that never moves, and the stored delta becomes
those layers' size by construction. **The time half is now measured too** ([§1.10](#110-re-measured-2026-09-06-continued--splitting-the-52-s-nix-build-vs-stream-vs-podman-load)): of a ~34 s build-plus-load
pipeline for one Go-only change, `nix build` is ~7.9 s and stream-plus-`podman-load` is ~26 s — load is the
clear majority, and podman's own write share (~17.6 s) alone exceeds the whole build phase. So a thin image
built *against the already-loaded base ref* — not a `fromImage` base, which re-emits the base layers into
the stream and would add to the write side just measured — is the largest unbuilt lever on the
maintainer's actual complaint, and **this re-ranks C6 above C4 on the measured workload**. Apple
Container's skopeo conversion is still unproven to preserve either property, and a measurement is not a
build authorization — see [OQ-6](#102-open-questions).

> [!NOTE]
> **If the concurrent `flake.nix` work removing `installPrefix` from the image lands, C6's remaining
> subject narrows.** `installPrefix` was one of three things this candidate wanted in the trailing layers
> (`binPathLinks`, the customisation layer, and it). If yolo's own binaries stop being baked at all, a
> Go-only commit may no longer move the image's store path the way this measurement assumes, and what is
> left to reorder is `binPathLinks` and the customisation layer — not yolo's binaries. The load-versus-build
> *shape* found here does not depend on which content sits in the trailing layer, only that content which
> changes often should trail content that does not; but re-derive the frequency claim before acting on it
> once that change lands, rather than reusing this section's premise unchecked.

### C7 — Skip the build when nothing moved. **Considered, rejected.**

A cheap `nix eval .#installPrefix.outPath` gate (0.22 s vs 1.28 s, MEASURED) before the build. Rejected:
it saves ~1 s on a path that costs seconds-to-minutes elsewhere, and `installPrefix` is invariant under
`flake.lock` ([§2](#2-what-the-image-contains-and-what-invalidates-each-part)), so the gate would be wrong exactly when it mattered
([`gate-placement-principle.md`](gate-placement-principle.md) Test 1, by analogy).

### C8 — Deliver yolo's own binaries by mount. **SHIPPED 2026-09-06.**

**This candidate did not exist in this doc; [§8](#8-what-this-does-not-cover) refused it by name.** The refusal's premise was
that reopening the `/opt/yolo-jail` bind meant reopening the dev-override that let a **stale** binary
shadow a **baked** one. What ships here has no shadow to lose to: the baked copy is gone, so the mount is
the only copy, and nothing can silently win over anything. The maintainer authorized it across all three
backends in one pass.

`installPrefix` leaves the image derivation (`flake.nix:971` — `corePackages` now carries
`jailPrefixLinks` and `imageIdentity` instead), and the launch supplies its content as two `:ro` bind
mounts — `<bin dir>:/opt/yolo-jail/bin` and `<bundle>:/opt/yolo-jail/share/yolo-jail`
(`internal/cli/run/jailprefix.go:148`). Two rather than one because the host layouts differ: `just
install` stages `bin/linux-<arch>/` beside the flake files, not `bin/` beside `share/yolo-jail/`, and
restaging into the prefix shape would copy ~200 MB per launch. The container argv names
`/opt/yolo-jail/bin/yolo-entrypoint` absolutely (`internal/cli/run/assemble.go:744`); what the image
keeps is the NAMES (`/bin/<name>` → the mountpoint, `flake.nix:870`) and the two mountpoint dirs
(`:1071`).

**MEASURED, 2026-09-06, on this tree** — `nix eval`, never a build. Appending one comment line to
`internal/version/version.go`:

| | `.#ociImage.outPath` | `.#installPrefix.outPath` |
|---|---|---|
| **before** (`ce0d6324`'s flake), clean | `jxl27j84…-stream-yolo-jail` | — |
| **before**, one Go comment | `1lrspd9a…-stream-yolo-jail` — **MOVED** | — |
| **after**, clean | `kwvlhbp8…-stream-yolo-jail` | `69pivd58…` |
| **after**, one Go comment | `kwvlhbp8…` — **unchanged** | `084zadmp…` — moved |
| **after**, one flake.nix comment | `5zisv87a…` — moved | — |

So the image now moves for `flake.nix`, `flake.lock` and `packages:`, and for nothing else. Against
[§1.1](#11-what-triggers-a-rebuild-and-how-often)'s measured frequencies that removes the trigger behind **~half of all commits** and leaves
one that fired **once in 567**.

Where the mounted content comes from — a bundle and the binaries it ships, in two spellings:

- **Prebuilt.** The resolved flake source's own `bin/linux-<arch>/`. Every installed bundle has one, and
  `installPrefix` bakes one INTO the mounted prefix, so a nested jail inherits prebuilt binaries and
  never compiles Go for this.
- **Built.** A live checkout ships none, so `nix build .#installPrefix` (`internal/image/prefix.go:62`).
  **No macOS offload is needed and none is wired:** `goBinaries` cross-compiles with the HOST Go
  toolchain (`CGO_ENABLED=0 GOOS=linux`), so on darwin `installPrefix` is a darwin derivation producing
  Linux binaries — unlike `.#ociImage`, which is why that one has a builder-container path and this one
  does not.

A failed prefix build **refuses the launch** ([OQ-2](#101-decision-ledger)'s ruling, applied to the half that moved out):
the image no longer carries a `yolo-entrypoint`, so there is nothing to fall back on. The out-link is
the prefix's GC root, keyed by source tree, and deliberately outside both reapers' reach —
`build/roots/` belongs to `PruneOrphanImageRoots`, which deletes anything that is not a loaded image.

**THE SECURITY DELTA, and it is a trade rather than a free win.** `/bin/<name>` used to target an
immutable store path on purpose, and what runs in the jail — pid1 included — was image content
addressed by the image's own content hash. It is now a host directory that changes with **no rebuild
and no reload**: editing the staged bundle changes the next launch's `yolo-entrypoint`. That mutability
*is* the feature (it is what makes a Go-only commit free). It does not move the host trust boundary — a
host that can write `~/.local/share/yolo-jail` could already replace the `yolo` that builds the argv —
but it does end the jail's binaries being as reproducible as its image, and that is the property being
spent. The pid1-brick hazard the old shadow hardening guarded against is answered on the other side:
the argv is absolute, so a missing mount fails as "no such file" naming the path.

**Backends.** podman and Apple Container each emit the pair from their own base-mount function (two
*directory* mounts; the single-file limitation apple/container#1089 does not apply, and `:ro` is
ignored there as everywhere else on that backend — apple/container#889). **`macos-user` needed nothing
and got nothing**: it runs no container, loads no image, and its `yolo` is the host's own binary. That
is the same "no image" fact [§3.3](#33-the-other-three-backends-shapes) records, reaching the same conclusion from the other end.
**NOT VERIFIED ON HARDWARE**: both macOS arms. Only podman-on-Linux was exercised (a nested jail booted
on the mounted prefix, resolved its flake bundle at `/opt/yolo-jail/share/yolo-jail` from
inside, and ran
`yolo --version`).

### Ranking summary

| # | Candidate | Frequency | Cost avoided | Risk | Backends | State, 2026-09-06 |
|---|---|---|---|---|---|---|
| C1 | Honest build failure | every failed build | wrong-layer debugging | very low | all | ✅ `7830f65` |
| C2 | Content-addressed image ref | every cross-workspace alternation | a 3.3 GB load | low | podman, Apple Container | ✅ `be7b8591` ([OQ-3](#101-decision-ledger)) |
| C3 | Stream into the runtime, no tar | ~half of commits | 3.28 GiB write per rebuild; 404 GiB accrued | low–medium | podman | ✅ `be7b8591` ([OQ-5](#101-decision-ledger)); AC race closed `cc53b591` |
| C4 | `packages:` from the mounted store | every `packages:` user | the `--impure` axis; ~3 GB per distinct list | **high** | podman + Linux + nix daemon | ❌ shape ruled ([OQ-1](#101-decision-ledger)); go/no-go the maintainer's on [§1.8](#18-re-measured-after-c2--c3--this-is-11-step-5) + [§1.9](#19-re-measured-2026-09-06--what-a-go-only-rebuild-costs-podman-and-what-chooses-the-flake) |
| C5 | `fullPackages` from the mounted store | ~half of commits | ~1.6–2 GB per rebuild | high | same as C4 | shape ruled with C4; ordered after it |
| C6 | A stable layer chain; stream only the moved layers | ~half of commits | ~2.7 GB of podman storage per Go-only rebuild (MEASURED); ~26 s of a ~34 s measured build-plus-load pipeline is stream+load, of which ~17.6 s is podman's write share (MEASURED, [§1.10](#110-re-measured-2026-09-06-continued--splitting-the-52-s-nix-build-vs-stream-vs-podman-load)) | medium | podman | re-opened as [OQ-6](#102-open-questions); time split measured, load dominates — go/no-go still the maintainer's |
| C8 | yolo's own binaries by mount | **~half of commits — the dominant trigger** | the whole rebuild+load for every Go-only commit (MEASURED: the image store path no longer moves) | medium — see the security delta | all three | ✅ 2026-09-06 |

> [!IMPORTANT]
> **C8 changes what the rows above are worth.** C3's and C6's frequencies were "~half of commits"
> because that is how often `goSrc` moved. It no longer forces anything: what is left on that axis is
> `flake.nix` and `flake.lock`, measured at **1 commit in 567** and **0**. C4/C5 are untouched — their
> trigger is `packages:`, which C8 does not touch — and C6's remaining case is the `flake.lock` bump,
> which is exactly the case a binary cache already serves ([§6](#6-the-binary-cache-alternative-argued-fairly)). Re-price before building either.

---

## 5. The central table: must bake / could move / already delivered

**MUST BAKE** — needed before any yolo code runs, or must resolve on the image's own PATH
(`PATH=/bin:/usr/bin`, `flake.nix:1023`).

| Content | Why it cannot move |
|---|---|
| ~~`/bin/yolo-entrypoint`~~ — **MOVED, C8, 2026-09-06.** What must bake is the mountpoint (`flake.nix:1071`) and the `/bin/<name>` NAME (`:870`) | The old reason — "the container argv ends `<image-ref> yolo-entrypoint`, resolved on the image's PATH before one line of yolo code has run" — was true of a BARE name. The argv now names `/opt/yolo-jail/bin/yolo-entrypoint` absolutely (`internal/cli/run/assemble.go:744`), and the mount is live before exec, so there is no bootstrap ordering problem to solve. This row is kept as a correction: it was the strongest-looking entry in the column and it was wrong |
| `/bin/bash`, `/bin/sh`, `/usr/bin/env`, coreutils (`flake.nix:543-549`) | The generated scripts and the runtime's exec path need a shell that exists in the rootfs |
| nix-ld at `/lib/ld-*` and `/lib64/ld-*` (`flake.nix:581-583`) | A `PT_INTERP` — an absolute path burned into every FHS binary, not a PATH entry |
| `/usr/share/nix-ld/lib` core trio (`flake.nix:592-599`) | The *only* library search path an FHS binary gets under a fully scrubbed environment |
| `/etc/passwd`, `/etc/group` (`flake.nix:1010-1013`) | Read by podman before and independently of yolo |
| `/etc/containers/*.conf`, `/etc/subuid`, `/etc/subgid` (`flake.nix:682-715`) | Nested-podman config on a `--read-only` root |
| `config.Env` incl. `SSL_CERT_FILE`, `TZDIR` (`flake.nix:1022-1039`) | Literal store paths in the image config; moving `cacert`/`tzdata` means moving these |
| `ld.so.conf` under `/etc`, and the `/run/*` **symlinks** (`flake.nix:742-747`, `:563-564`) | The *link* is baked because `/etc` is read-only; the *target* is staged — this row is the pattern |

**COULD MOVE** — mechanism and cost.

| Content | Mechanism | Cost / what breaks |
|---|---|---|
| `extraPackages` from `packages:` (`flake.nix:343-344`, in `contents` at `:985-989`) | C4 | Podman+Linux+daemon only; `LD_LIBRARY_PATH` and the `/lib` farm are baked; two integration tests assert baked paths. **Per [OQ-1](#101-decision-ledger) an *addition*, not a move: the baked path stays as fallback.** Scope stays workspace ([OQ-4](#101-decision-ledger)) |
| `extraLibPackages` — the user half of the `/lib` farm (`flake.nix:636-646`) | Same; append a boot-written dir to `LD_LIBRARY_PATH` | Scrubbed-env consumers lose it (`:728-732`); nix-ld's fallback dir stays baked |
| `fullPackages` (`flake.nix:909-937`) | C5 | Inverts the "image beats launcher" invariant for every moved name ([§3.1](#31-the-boot-written-anchors)); chromium drags baked font links. [OQ-1](#101-decision-ledger)'s opt-in shape contains it |
| The `share/yolo-jail/bin/linux-<arch>/` duplicate of the binaries | Nothing — deliberate | No longer image content at all (C8). It is still a deliberate duplicate INSIDE the mounted prefix, and it earns more there than it did here: it is what lets a nested jail mount prebuilt binaries instead of compiling them |
| ~~yolo's own binaries, delivered by mount instead of baked~~ | **SHIPPED — [C8](#c8--deliver-yolos-own-binaries-by-mount-shipped-2026-09-06), 2026-09-06** | Was: *"Refused, and not proposed here."* The refusal is retracted in [§8](#8-what-this-does-not-cover); the shadow hazard it rested on required a baked copy to lose to, and there is none |

**ALREADY DELIVERED** — the largest column.

| Content | How |
|---|---|
| Blockers and lazy launchers (`~/.yolo/bin/{block,launch}`) | Generated every boot ([§3.1](#31-the-boot-written-anchors)); the baked shim layer was removed (`flake.nix:1018-1021`) |
| The loader cache and the timezone files (`ld.so.cache`, `/etc/localtime`, `/etc/timezone`) | Boot-populated `/run` targets (`internal/entrypoint/system_boot.go:58`, `:20`; rationale `flake.nix:734-747`) |
| CA bundle, `.bashrc`, bootstrap + venv scripts, MCP wrappers | Boot-generated by `internal/entrypoint`; **not** `yolo-cglimit`/`yolo-journalctl` — baked, with the old scripts unlinked ([§3.4](#34-everything-else-already-delivered-at-launch)) |
| The whole nix store | `-v /nix/store:/nix/store:ro` (`internal/cli/run/assemble.go:364-370`), gated per [§3.2](#32-the-mounted-nix-store--the-key-lever-and-its-hard-limit) |
| Workspace, home + rw anchors, scratch mounts, `/ctx/*`, `/mise` | `internal/cli/run/assemble_parts.go`, `runmount.go`; `/ctx` mountpoints on demand (`flake.nix:999-1006`) |
| Pack content and every pack surface, incl. MCP config | Staged host-side (`internal/packstage`), `:ro` at `/ctx/packs`; rendered in one loop every boot (`ConfigurePackSurfaces`, `internal/entrypoint/packsurfaces.go:125`) |
| mise tools | `mise` is baked; tools install into the `/mise` mount (`MISE_DATA_DIR=/mise`, `assemble.go:746`) |
| Agent CLIs | npm-installed into `/home/agent/.npm-global`, the rw bind at `assemble_parts.go:108` |
| LSP servers | Sentinel-tracked install *and uninstall* (`~/.yolo-installed-lsps`, `internal/entrypoint/shell.go:325`) |
| `packages:` on `macos-user` | A store `buildEnv` PATH-prepend (`flake.nix:1204`, `internal/darwinpkg`) — C4's shape, shipped there |

---

## 6. The binary-cache alternative, argued fairly

A binary cache turns a "rebuild" into a download. **Where it helps:** first-run cost on a new machine or CI
runner, and the `flake.lock` bump — the one case where the whole 3.12 GiB nixpkgs half moves ([§1.4](#14-the-amplification-factor--the-number-this-doc-exists-for)) —
which is why `imageClosureRoot` is factored to be substitutable from `cache.nixos.org` (`flake.nix:939-976`).
**Where it does not:** the developer's inner loop. A from-source build of uncommitted local code can never
be in any cache; ~half of commits change `goSrc` ([§1.1](#11-what-triggers-a-rebuild-and-how-often)), and those images have never existed anywhere
before. **The cache is structurally incapable of touching the dominant case.**

Four limits, each checked:

1. ~~**The cache has never been pushed to.**~~ **⚠ RETRACTED 2026-09-06 — settled 2026-09-02.**
   [`../plans/handoff-cachix-cache.md`](../plans/handoff-cachix-cache.md): CI's `push-image-cache` job pushed both arches and the
   same log shows the second variant substituting the four this-repo derivations from
   `yolo-jail.cachix.org`. Only the Mac-side download proof remains.
2. **Even populated, it holds only the stock image.** `.github/workflows/publish.yml` builds `.#ociImage`
   and `.#ociImageMinimal` with no `YOLO_EXTRA_PACKAGES`, so **every `packages:` user is a guaranteed cache
   miss by construction.** That makes C4 a *prerequisite* for the cache being useful to them — complements,
   not alternatives.
3. ~~**The image build path does not opt into the flake's substituter.**~~ **⚠ RETRACTED — fixed `b7f2ade3`,
   2026-08-17.** `NixFlakeFlags` (`internal/image/nixflags.go:32-37`) passes `--accept-flake-config` on every
   flake-evaluating call; the original symptom — nix printing *"ignoring untrusted flake configuration
   setting 'extra-substituters'"* — and the deliberate non-coupling (`nix store gc`, `nix path-info`, `nix
   copy` take a store path and do not get the flag) are at `:9-25`. **The item number is kept because
   `nixflags.go:17` cites "[§6](#6-the-binary-cache-alternative-argued-fairly) item 3" by number.** The substituter surface it opened is what
   [`macos-user-build-step-threat-model.md`](macos-user-build-step-threat-model.md) Q2 asks about.
4. **It serves at most two of three backends.** `macos-user` has no image.

**Verdict:** worth having, cheap, and now had — but not an alternative to [§4](#4-candidates-ranked). The cache attacks first-run and
`flake.lock` cost; C1–C3 attacked the inner loop; item 2 is the link from "make cachix useful" back to C4.

---

## 7. The silent-fallback defect — why staging is worthless without honest failure

**This section exists because the question surfaced from a wrong-layer diagnosis.** On 2026-08-15 two
macOS integration tests — `TestExtraPackageLibFarm` (`integration/packages_test.go:82`) and
`TestDevPackageLinksRuntimeLib` (`:135`) — failed with a lib-farm assertion (`libzbar.so.0 not linked into
/lib`, `:101-102`). The actual cause was a failed image build.

**The mechanism, as it was** (pre-`7830f65` code; read it with `git show 7830f65a^:internal/image/autoload.go`):
when the `--impure` build failed, `buildImageStorePath` returned `("", tail)` and control reached

```go
if currentPath == "" {
    imageName := JailImage(o.Runtime)
    if rc, ran := o.Run(ImageInspectCmd(o.Runtime, imageName)); ran && rc == 0 {
        fmt.Fprintln(out, "Using existing "+imageName+" image.")
        return true
    }
```

Three things went wrong at once: it returned **true**, so the jail launched on the *previous* image,
which had no zbar; the captured nix stderr was **dropped**, surfaced only when there was *also* no image
and no cached tar; and "Using existing … image" read as a cache hit. The diagnosis landed two layers from
its cause, on code that was working.

**The framing that matters for this whole document: a staging change is worthless if a failure to stage
is invisible.** Every candidate in [§4](#4-candidates-ranked) makes the pre-container phase do more work; C4 in particular
replaces "the package is baked, or the build failed loudly" with "the package is symlinked at boot,
or … something". **C1 is a precondition for C2–C5, not a parallel nicety.**

**The fix, as shipped (`7830f65`)** goes further than this section proposed: `buildFailed` distinguishes a
failed build from a suppressed one (`internal/image/autoload.go:286-301`); the classification and nix's
own stderr are printed before anything else (`:333-336`); and the fallback is **fatal by default**, with
`YOLO_ALLOW_STALE_IMAGE=1` the opt-in (`:337-340`) — the three-option argument is in the comment above it
(`:305-332`). See [OQ-2](#101-decision-ledger) and the warning under [§10.1](#101-decision-ledger).

---

## 8. What this does NOT cover

- **The build's own speed.** [§1.3](#13-what-a-rebuild-actually-costs) measures `go build` and nix evaluation and finds them small.
- **Image content policy.** Whether `chromium` or `gcc` *should* be in a jail is a product question; C5
  only addresses where they are delivered from.
- **`macos-user`**, except as the existence proof in [§3.3](#33-the-other-three-backends-shapes).
- **The GC-root / store-lifecycle ledger** — `internal/image/gcroot.go` and
  [`../plans/storage-lifecycle.md`](../plans/storage-lifecycle.md).
- **Disk lifecycle, as of [OQ-5](#101-decision-ledger).** This doc measured the 404 GiB and carries the verdict; it does not
  design the fix. [`minimal-disk-footprint.md`](minimal-disk-footprint.md) owns what gets deleted, when, on whose
  authority, and the reclaimers that are not image tars. Where the two disagree about a retention number,
  that one wins. C3 stays here because it is a change to how an image is *delivered*.
- ~~**Delivering yolo's own binaries outside the image.**~~ **⚠ RETRACTED 2026-09-06 — it is [C8](#c8--deliver-yolos-own-binaries-by-mount-shipped-2026-09-06), and it
  shipped.** The refusal was right about the stakes ("a decision about the trust model of the boot path")
  and wrong about the hazard. What was tried and removed was the `/opt/yolo-jail/dist-go`
  **dev-override**: a second copy that let a *stale* binary shadow the *baked* one, so a fixed jail
  looked broken. C8 removes the baked copy, which is what removes the shadow — there is one copy, named
  absolutely on the argv, and a missing mount fails saying which path is missing. The trust-model call
  the bullet reserved for a human was made by the maintainer, and the property it costs is written down
  in C8's security delta rather than left implied.
- **A `podman load` timed in true isolation from the stream that feeds it.** [§1.3](#13-what-a-rebuild-actually-costs) flagged both a cold
  `nix build` and a standalone `podman load` as NOT MEASURED; [§1.10](#110-re-measured-2026-09-06-continued--splitting-the-52-s-nix-build-vs-stream-vs-podman-load) measures the cold build directly and approximates
  `podman load`'s own share as a difference of means against the stream-alone time — the pipe still means
  the two are never observed apart for real, only estimated.

---

## 9. Risks

| # | Risk | Mitigation |
|---|---|---|
| R1 | **C4/C5 are podman-on-Linux-only**, so shipping either means two package-delivery mechanisms indefinitely — the "fill the matrix" failure [`happy-path-principle.md`](happy-path-principle.md) warns about. | **Settled by [OQ-1](#101-decision-ledger):** opt-in fast path, baked path retained, only after C1–C3. Two mechanisms are the accepted, priced cost. What is open is the go/no-go, on [§1.8](#18-re-measured-after-c2--c3--this-is-11-step-5) + [§1.9](#19-re-measured-2026-09-06--what-a-go-only-rebuild-costs-podman-and-what-chooses-the-flake). |
| R2 | **A PATH-delivered package cannot shadow a baked one** ([§3.1](#31-the-boot-written-anchors)), so a half-migration silently runs the baked copy. | All-or-nothing, and the unit is the **launch**: opt-in launches build the stock image with no `YOLO_EXTRA_PACKAGES`; others bake. A test that `which <pkg>` resolves to the staged dir catches the half-state. |
| R3 | **C2 multiplies loaded images**, and `--keep-images 2` by CreatedAt is the wrong rule for per-config tags. | **Safety discharged** with C2 and `4064f720` (dedup by image ID, fail-safe liveness veto — [§4](#4-candidates-ranked) C2's note). **The number is not**: default 2 is untouched (`internal/prune/prunecmd.go:151`) and belongs to [OQ-DF3](./minimal-disk-footprint.md#OQ-DF3). The cost is now priced ([§1.9](#19-re-measured-2026-09-06--what-a-go-only-rebuild-costs-podman-and-what-chooses-the-flake)): ~2.7 GB per Go-only image, ~3 GB per distinct `packages:` closure — so with the dev-loop rate measured there, `keep=2` never running is the difference between ~6 GB and ~39 GB. |
| R4 | **C3 removes the offline safety net** if taken to "never write a tar". | **Superseded by [OQ-5](#101-decision-ledger):** tars are a bug; the target is zero after a successful load. The safety net needs *at most one* tar for a jail that must start when the build fails and nothing is loaded (`newestTars`) — a fallback mechanism, not a retention policy, designed in [`minimal-disk-footprint.md`](minimal-disk-footprint.md). C1 shipped first, as this row required. |
| R5 | **Scrubbed-environment breakage.** A consumer scrubbing `LD_LIBRARY_PATH` cannot be rescued (`flake.nix:728-732`); C4 moves user libs from a baked dir to an env-dependent one. | Keep the nix-ld fallback dir baked (`:592-599`, kept to the trio on purpose); growing it is an explicit call. |
| R6 | **The `goSrc` fileset trap** bites any new top-level Go package, silently (`flake.nix:94-107`). Unchanged by [C8](#c8--deliver-yolos-own-binaries-by-mount-shipped-2026-09-06) except in where it bites: the package now vanishes from the MOUNTED PREFIX rather than from the image. | Any C4/C5 package outside `cmd/`/`internal/` is added to the fileset in the same commit. |
| R7 | **Every number here comes from one machine — this jail.** | The *ratios* are machine-independent and are what the ranking rests on; absolute figures are illustrative. |
| R8 | **C4/C5 make the jail structurally dependent on the host nix daemon** — the socket is mounted read-write, gated on Linux by path existence alone (`internal/cli/run/hostprobes.go:22-24`). | Pre-existing, but C4 turns "convenient" into "load-bearing". A deliberate decision, per [`gate-placement-principle.md`](gate-placement-principle.md) Test 2. |
| R9 | **What executes in the jail is now HOST-MUTABLE with no rebuild** ([C8](#c8--deliver-yolos-own-binaries-by-mount-shipped-2026-09-06)). pid1 comes from a bind-mounted host directory, not from content-addressed image content; editing the staged bundle changes the next launch's `yolo-entrypoint`, and the image's content hash no longer witnesses it. | **Accepted, deliberately** ([OQ-8](#101-decision-ledger)) — it is the mechanism, not a side effect. Bounded by what it does NOT change: the host trust boundary is unmoved (a writer of `~/.local/share/yolo-jail` could already replace the `yolo` that builds the argv), the mount is `:ro` from inside, and the launch PRINTS which directory it took (`Jail binaries: …`) beside the flake source. What is genuinely spent is reproducibility: a jail's binaries are now only as pinned as the directory mounted in, where a nix-built prefix is immutable and a staged bundle is not. |

---

## 10. Decisions

The five original questions are ruled — [OQ-2](#101-decision-ledger) on 2026-08-15, the rest by the maintainer on 2026-08-25 — and
their reasoning lives in the body sections that govern them. Two more were ruled on 2026-09-06:
[OQ-7](#101-decision-ledger) as MOOT and [OQ-8](#101-decision-ledger), the mount lever, as shipped. **IDs are an API and are never renumbered**:
[`program-delivery.md`](program-delivery.md) [§7](./program-delivery.md#7-what-this-does-not-cover) cites [OQ-4](#101-decision-ledger),
[`gate-placement-principle.md`](gate-placement-principle.md) cites [OQ-2](#101-decision-ledger), [`minimal-disk-footprint.md`](minimal-disk-footprint.md)
[§12](./minimal-disk-footprint.md#12-inherited-rulings) inherits all four 2026-08-25 rulings, and [`../plans/roadmap.md`](../plans/roadmap.md) cites [OQ-1](#101-decision-ledger) and
[OQ-5](#101-decision-ledger) by ID. The 2026-09-06 re-audit added two questions ([§10.2](#102-open-questions)); it changed no ruling.

### 10.1 Decision Ledger

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| OQ-1 | **Shape only, and conditional.** If C4/C5 ship, they ship as an **opt-in fast path with the baked path retained** — two mechanisms, accepted deliberately. **Not approval to build C4:** the go/no-go stays with the maintainer. Its gate, the [§11](#11-what-to-do-first--dependency-ordered) step 5 re-measurement, was taken 2026-08-25 ([§1.8](#18-re-measured-after-c2--c3--this-is-11-step-5)) and extended 2026-09-06 ([§1.9](#19-re-measured-2026-09-06--what-a-go-only-rebuild-costs-podman-and-what-chooses-the-flake)); that discharges the step, not the gate | 2026-08-25 | [§4](#4-candidates-ranked) C4/C5, [§9](#9-risks) R1, [§11](#11-what-to-do-first--dependency-ordered) step 6 |
| OQ-2 | A build that **ran and failed** is FATAL — the classification and nix's own stderr are printed and an empty `LoadResult` returned (`internal/image/autoload.go:333-340`). Opt-out is `YOLO_ALLOW_STALE_IMAGE=1`, not a TTY test. Shipped `7830f65` | 2026-08-15 | [§7](#7-the-silent-fallback-defect--why-staging-is-worthless-without-honest-failure) |
| OQ-3 | **Content-addressed image tags win** (C2); the LRU-membership variant is refused. `localhost/yolo-jail:latest` is **not a public surface**. The *"plans on making cachix useful"* caveat is about the nix binary cache, a different surface | 2026-08-25 | [§4](#4-candidates-ranked) C2, [§6](#6-the-binary-cache-alternative-argued-fairly), [§9](#9-risks) R3 |
| OQ-4 | **`packages:` stays workspace-scope** — *"yes, has to be."* Per [`gate-placement-principle.md`](./gate-placement-principle.md) Test 1, scope gates exist for host access; `packages:` grants a tool. **Fix the cost, never the scope** | 2026-08-25 | [§1.5](#15-the-multiplication-factor-packages-and---impure) |
| OQ-5 | **404 GiB of cached tars is a BUG, not a configuration.** *"No reason to keep any of this around … minimal disk space."* The shipped GC work is *"nowhere near enough."* `yolo` **may** delete cached tars without `--apply`. Executed in [`minimal-disk-footprint.md`](minimal-disk-footprint.md) | 2026-08-25 | [§1.6](#16-what-it-has-actually-cost-on-disk), [§4](#4-candidates-ranked) C3, [§8](#8-what-this-does-not-cover), [§9](#9-risks) R4 |
| OQ-7 | **MOOT — do not implement.** The question was whether the bundle's binaries should stop carrying the `git describe` stamp, so a `just install` that moved no image input stops minting a new image. [C8](#c8--deliver-yolos-own-binaries-by-mount-shipped-2026-09-06) took the binaries out of the image, so stamped bytes are no longer image content: MEASURED 2026-09-06, two bundles differing only in their binaries' bytes evaluate to the SAME `.#ociImage.outPath` (and to different `.#installPrefix` paths, as they must). The cost the question existed to remove is gone; removing the stamp would now buy only a `runCommand` that copies seven files, at the price of the fallback the in-jail version banner keeps for a launcher that set no `YOLO_VERSION`. [§11](#11-what-to-do-first--dependency-ordered) step 8 is struck | 2026-09-06 | [§1.1](#11-what-triggers-a-rebuild-and-how-often) item 3, [§4](#4-candidates-ranked) C8 |
| OQ-8 | **yolo's own binaries are delivered by MOUNT, on all three backends, in one pass** — the lever [§8](#8-what-this-does-not-cover) refused. Authorized by the maintainer knowing the macOS arms cannot be hardware-verified from this jail. The traded property is named in [C8](#c8--deliver-yolos-own-binaries-by-mount-shipped-2026-09-06)'s security delta: what executes in the jail stops being content-addressed image content and becomes a host directory that changes with no rebuild | 2026-09-06 | [§4](#4-candidates-ranked) C8, [§5](#5-the-central-table-must-bake--could-move--already-delivered), [§8](#8-what-this-does-not-cover) |

> [!WARNING]
> **[OQ-2](#101-decision-ledger)'s ruling deliberately contradicts [`gate-placement-principle.md`](gate-placement-principle.md)'s "tell a human from a
> pipe", and that divergence was argued, not overlooked.** What makes a stale run safe is not *who* is
> running but that somebody **said** the image may be stale — the knowledge whose absence caused the bug.
> Refusing costs a rerun with one env var; continuing costs an investigation at the wrong layer. **`SkipBuild`
> is untouched, and its silence is deliberate**: no build was attempted, so nothing failed. Do not "fix"
> that asymmetry.

### 10.2 Open Questions

1. 💬 **OQ-6: Now that both halves of C6's case are measured — ~2.7 GB of podman storage per Go-only
   rebuild, and a ~34 s build-plus-load pipeline that splits ~7.9 s build / ~26 s stream-and-load — does a
   stable layer chain (C6) get built?** [§1.9](#19-re-measured-2026-09-06--what-a-go-only-rebuild-costs-podman-and-what-chooses-the-flake) measured the storage half (first changed layer at chain
   position 78 of 99); [§1.10](#110-re-measured-2026-09-06-continued--splitting-the-52-s-nix-build-vs-stream-vs-podman-load) measured the time half this question was blocked on, in this jail, on a Go-only
   change that landed naturally during the same session (`ce0d6324`, not manufactured for the
   measurement). **Load dominates, decisively:** stream-and-load (~26 s) is ~3.3× the build phase (~7.9 s),
   and podman's own write share alone (~17.6 s, a difference-of-means approximation — the pipe still
   prevents a clean isolation) already exceeds the entire build. This settles the diagnostic half this
   question asked — the maintainer's complaint is a *layering* problem, not a *build* problem — and
   re-ranks C6 above C4 on the measured workload. It does not, by itself, settle the remaining half: whether
   to actually build it, on what schedule, and against Apple Container's still-unproven skopeo path.
   **A premise worth checking before acting on the ruling:** the concurrent `flake.nix` work removing
   `installPrefix` from the image would narrow C6's remaining subject to `binPathLinks` and the
   customisation layer, not yolo's binaries — see the note under [§4](#4-candidates-ranked) C6.

   <!-- vantage: oq id=OQ-6 leaning="Both halves are now measured (§1.9 storage, §1.10 time): load is ~3.3x the build phase, and podman's write share alone exceeds the whole build. Build the thin image against the already-loaded base ref, not a fromImage base (which re-emits the base layers into the stream and would add to the write side just measured), and rank C6 above C4 on this evidence. Re-check the premise first if the concurrent flake.nix work has removed installPrefix from the image by the time this is acted on - the mechanism then targets binPathLinks and the customisation layer, not yolo's binaries. A measurement is not a build authorization; the go/no-go is still the maintainer's, same as C4/C5's OQ-1." -->

   _Leaning:_ **Both halves are now measured** ([§1.9](#19-re-measured-2026-09-06--what-a-go-only-rebuild-costs-podman-and-what-chooses-the-flake) storage, [§1.10](#110-re-measured-2026-09-06-continued--splitting-the-52-s-nix-build-vs-stream-vs-podman-load) time): load is ~3.3× the build phase, and
   podman's write share alone exceeds the whole build. Build the thin image against the already-loaded base
   ref, not a `fromImage` base (which re-emits the base layers into the stream and would add to the write
   side just measured), and rank C6 above C4 on this evidence. Re-check the premise first if the concurrent
   `flake.nix` work has removed `installPrefix` from the image by the time this is acted on — the mechanism
   then targets `binPathLinks` and the customisation layer, not yolo's binaries. A measurement is not a
   build authorization; the go/no-go is still the maintainer's, same as C4/C5's [OQ-1](#101-decision-ledger).

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-7: Should the bundle's image binaries stop carrying the `git describe` stamp, so that
   `just install` after a commit that moved no image input does not mint a new image?** [§1.1](#11-what-triggers-a-rebuild-and-how-often) item 3:
   `scripts/build-go.sh:55` stamps `buildVersion` and `GitCommit` into the binaries the bundle's flake
   copies into `installPrefix`, so every commit — and a dirty tree — changes the image store path even when
   nothing in `goSrc` or the flake files moved; the from-source nix build has no stamp and does not. This
   decides whether the default launch path's rebuild trigger is "an image input changed" (matching the
   `YOLO_REPO_ROOT` path and `version.SourceSkew`'s own definition of what the image is built from) or
   "`just install` ran". The cost of removing the stamp is the in-jail version banner, which today reads
   `YOLO_VERSION` from the launcher first (`internal/version/version.go:94`) and would fall back to the
   stamp only where the launcher set nothing.

   <!-- vantage: oq id=OQ-7 leaning="Stamp only the host yolo that go install builds, and leave the bundle's image binaries unstamped so they match what nix's own build produces; the jail already learns its version from YOLO_VERSION. Low stakes: a from-source developer usually runs just install because goSrc moved, so the wasted rebuilds are the docs-only-then-install case - but there is no reason for a version string to be image content." -->

   _Leaning:_ **Stamp only the host `yolo`** (the `go install` at `Justfile:65`) and leave the bundle's
   image binaries unstamped, matching what the nix build produces; the jail already learns its version
   from `YOLO_VERSION`. Low stakes — a from-source developer usually installs *because* `goSrc` moved, so
   the waste is the docs-only-then-install case — but a version string has no business being image content.

   **Answer:**
   > **MOOT, 2026-09-06 — ruled in the ledger, not implemented.** [C8](#c8--deliver-yolos-own-binaries-by-mount-shipped-2026-09-06) removed the binaries from the
   > image, so the stamp is no longer image content and the question's whole premise ("changes the image
   > store path") is false. MEASURED: two bundles differing only in their prebuilt binaries' bytes
   > evaluate to the same `.#ociImage.outPath` (`kwvlhbp8…`) and to different `.#installPrefix` paths;
   > the same pair under the pre-C8 flake evaluate to two different images. The leaning's second
   > sentence — *"a version string has no business being image content"* — got its wish by a route that
   > did not touch the stamp. Leave `scripts/build-go.sh` alone: what a `just install` mints now is a
   > `runCommand` copying seven files, and the stamp still serves the in-jail banner's fallback for a
   > launcher that set no `YOLO_VERSION`.

---

## 11. What to do first — dependency-ordered

Re-stated 2026-09-06. **Steps 1–5, and now 9, are done** and stay in the list because the order is the
argument. Step 6 is a decision, not a build task; step 7 is the one question still open, and step 8 is
struck by the step that came after it.

1. ~~**C1 — a failed image build fails as itself**~~ ([§7](#7-the-silent-fallback-defect--why-staging-is-worthless-without-honest-failure)). **SHIPPED `7830f65`, 2026-08-15**, further than proposed:
   fatal by default, `YOLO_ALLOW_STALE_IMAGE=1` the opt-out ([OQ-2](#101-decision-ledger)).
2. ~~**`--accept-flake-config` on the image `nix` invocations**~~ ([§6](#6-the-binary-cache-alternative-argued-fairly) item 3). **SHIPPED `b7f2ade3`, 2026-08-17.**
3. ~~**C2 — content-addressed image ref**~~. **SHIPPED `be7b8591`, 2026-08-25** ([OQ-3](#101-decision-ledger)). Revisiting the
   retention rule, as R3 demanded, showed per-config tags **arm** a pass that had never fired; the safety
   half landed with C2 and `4064f720`, the number is [OQ-DF3](./minimal-disk-footprint.md#OQ-DF3)'s.
4. ~~**C3 — stream to the runtime**~~. **SHIPPED `be7b8591`, 2026-08-25** ([OQ-5](#101-decision-ledger); floor ruled as
   [OQ-DF1](./minimal-disk-footprint.md#11-open-questions), *"stream, keep zero tars"*). The Apple Container arm's tar-eviction race closed in
   `cc53b591`, 2026-09-02.
5. ~~**Re-measure.**~~ **TAKEN 2026-08-25 ([§1.8](#18-re-measured-after-c2--c3--this-is-11-step-5)) and extended 2026-09-06 ([§1.9](#19-re-measured-2026-09-06--what-a-go-only-rebuild-costs-podman-and-what-chooses-the-flake)).** Cold 52 s / warm
   4 s; zero tars written; a Go-only rebuild changes 2 of 99 layer digests but re-stores ~2.7 GB because
   the first change sits at chain position 78; a distinct `packages:` closure costs ~3 GB.
6. **C4, then C5 — only if the maintainer calls it**, on steps 5's evidence. [OQ-1](#101-decision-ledger) fixes the shape, not
   the go-ahead. Neither is queued on [`../plans/roadmap.md`](../plans/roadmap.md), deliberately.
7. ~~**[OQ-6](#102-open-questions) — split the 52 s.**~~ **TAKEN 2026-09-06 ([§1.10](#110-re-measured-2026-09-06-continued--splitting-the-52-s-nix-build-vs-stream-vs-podman-load)).** Build ~7.9 s, stream-and-load
   ~26 s — load is the majority by ~3.3×, and re-ranks C6 above C4. **Not yet done:** the maintainer's
   go/no-go on actually building it, same status as C4/C5.
8. ~~**[OQ-7](#102-open-questions) — the stamp.**~~ **STRUCK 2026-09-06 — MOOT, not done.** It was to stop the default
   launch path rebuilding on commits that moved no image input; step 9 stopped that for every commit,
   stamped or not, by taking the binaries out of the image. MEASURED in [C8](#c8--deliver-yolos-own-binaries-by-mount-shipped-2026-09-06). Do not implement it.
9. ~~**C8 — yolo's own binaries by mount.**~~ **SHIPPED 2026-09-06**, all three backends in one pass, on
   the maintainer's authorization. Not in this list before, because [§8](#8-what-this-does-not-cover) refused it; that refusal is
   retracted there. It removes the trigger behind ~half of all commits and moots step 8. **Unverified:
   both macOS arms** — no hardware here.

**What is not in this list, deliberately.** The disk work [OQ-5](#101-decision-ledger) licenses — podman's untagged image store,
the cache subdirs, whether any reclaimer runs without a human typing `yolo prune` — is
[`minimal-disk-footprint.md`](minimal-disk-footprint.md)'s sequencing; the host `/nix/store` beyond yolo's roots stays with
[`../plans/storage-lifecycle.md`](../plans/storage-lifecycle.md) [§2](../plans/storage-lifecycle.md#2-auto-gc-safety-net-min-freemax-free--only-after-1). What
this paragraph used to exclude as well — delivering yolo's own binaries outside the image, refused for
the reasons that were in [§8](#8-what-this-does-not-cover) — is step 9, and shipped.
