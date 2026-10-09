---
title: "Handoff — publish the prebuilt image to a Cachix cache"
status: accepted — Option A report split independently reviewed and verified offline; Apple silicon native Mac measurement remains UNMEASURED
stage: BUILT
next: "Run the human Final test on an Apple silicon Mac, which no CI job covers; decide whether the 13 to 15 darwin-system helper derivations a Mac builds locally are worth pushing (Proposed fix, option B, after a timed build)"
---

# Handoff — publish the prebuilt image to a Cachix cache

**Status:** 2026-09-02, re-verified 2026-09-30 — **working**: every release since
`v0.8.0` has pushed (the latest, `v0.11.0` on 2026-09-28, run `36473315012`, logged `Pushed
image closures to yolo-jail.cachix.org` on both arches), and since `1006fe6d` (2026-09-13) the
macOS nightly pushes on every run as well (`.github/workflows/nightly-macos.yml`, the
`build-image` and `push-arm-image-cache` jobs). MEASURED: CI pushes both Linux arches and
substitutes the four this-repo-source paths back from the cache (run `31749547095`, below).
UNMEASURED: the human Final test on an Apple silicon Mac with its own clean store. The instrument
for the macOS nightly was written on 2026-10-01: `TestMacImageSubstitutesFromCachix`
([`maccachixsubstitution_test.go`](../../integration/maccachixsubstitution_test.go)), which falls
into one shard of the computed partition. **It has run since** (2026-10-03, see
[What the Mac nightly measured](#what-the-mac-nightly-measured-2026-10-03)): an Intel Mac fetches
every Linux path the image needs and builds no Linux derivation; what it builds is 13 to 15 small
darwin derivations that no CI job pushes. That result is not the human Apple-silicon Final test.
**Settled 2026-09-02 from the Actions log**, which closes the disagreement this doc
carried against [`README.md`](README.md): README's *"CI has already pushed data"* was
the correct sentence.

> [!NOTE]
> **The measurement, so nobody has to re-take it.** Run **`31749547095`** (`v0.8.0`,
> 2026-08-13), job `push-image-cache`, **both** arches (`ubuntu-latest` and
> `ubuntu-24.04-arm`) → **success**, gate **open** (`Set up Cachix` ran with the real
> token, `name: yolo-jail`, `skipPush: false`), and the step logged
> `Pushed image closures to yolo-jail.cachix.org`.
>
> **And the same log shows CI READING the cache**, which is stronger than the push:
> the second variant reported `these 4 paths will be fetched (507.7 KiB download,
> 25.7 MiB unpacked)` and substituted all four from `https://yolo-jail.cachix.org` —
> `stream-yolo-jail`, `bin-path-links`, `yolo-jail-conf.json`,
> `yolo-jail-customisation-layer`, i.e. exactly the this-repo-source derivations that
> are never on `cache.nixos.org` and that the "Why" below is about.
>
> ⚠ **A real defect was found in the same log and fixed 2026-09-02.** The build step
> ran bare `nix build --impure` with **no `--accept-flake-config`**, so nix printed
> `ignoring untrusted flake configuration setting 'extra-substituters'` and the flake's
> own declaration was DISCARDED. The hits above happened only because `cachix-action`
> adds the substituter to `nix.conf` itself — a grace that would vanish silently if the
> step were reordered or the action swapped. The flag is now passed at **all six**
> `nix build` sites across `publish.yml`, `ci.yml`, `nightly-macos.yml` and `packs.yml`;
> the latter three have **no** `cachix-action` at all, so before the fix they could not
> see the cache under any circumstances and rebuilt the closure from source every run.
>
> **Scope caveat, corrected 2026-09-30:** `publish.yml`'s push is release-gated (`on: push:
> tags: v*`), but it is no longer the only push. Since `1006fe6d` (2026-09-13) the macOS nightly
> pushes the x86_64 closure from `build-image` on every run and the aarch64 closure from
> `push-arm-image-cache`, so the cache also holds whatever `main` the last nightly built. A
> consumer between those builds gets a cache hit on the last built paths and builds the delta.
> That delta is smaller than it was: the image no longer contains yolo's own binaries (they are mounted
> from the launch's prefix — [`image-staging-vs-baking.md`](../reference/image-staging-vs-baking.md)),
> so a commit touching only `cmd/` or `internal/` does not move the image at all, and a
> release's cached image stays current until `flake.nix`, `flake.lock` or a `packages:`
> list changes it.

**Why** (as first written; the image has changed since, and
[the Mac nightly](#what-the-mac-nightly-measured-2026-10-03) found that a Mac now needs no Linux
builder for it): the OCI image contains a few `aarch64-linux` derivations built from
*this repo's* flake (`yolo-jail-conf`, the bin-path links, the stream script, the
customisation layer) that are **never** on `cache.nixos.org`. (The entrypoint used to be one;
since the image stopped containing yolo, it is mounted rather than baked.) So building the image on
macOS needs a Linux builder — *unless* we publish the built image to a
binary cache that macOS users can download from. Publishing = the "everybody,
zero setup, at any point" happy path; the rare fallback (custom uncached
packages only) is an **automatic, ephemeral container builder** that a normal
`yolo` run offloads to on the active runtime, then tears down — no per-machine
VM.

## What's wired (all live as of 2026-07-20)

- **flake.nix** — the `nixConfig` block is **enabled** with the substituter
  `https://yolo-jail.cachix.org` and the public key
  `yolo-jail.cachix.org-1:6SMCmaSd8DsVfj5EHAdpgIZi0RE14zyYrAWnV8WxFLM=`.
- **Justfile** — `just cachix-push` builds both image variants and the copier on a Linux
  host and pushes their closures.
- **.github/workflows/publish.yml** — the `push-image-cache` job (release-gated)
  builds + pushes both image variants **and the image copier** (`.#imageCopier`, a source
  build no public cache serves) on every published release. It gates on the
  `CACHIX_AUTH_TOKEN` **secret alone** (set ✅); the cache name defaults to
  `yolo-jail`, overridable by the optional `CACHIX_CACHE` variable.
- **Proven end to end in CI** (run `31749547095`, `v0.8.0`, 2026-08-13, both arches):
  both variants built, the closures pushed, and the four this-repo-source paths were
  **substituted back from the cache** in the same run. Only the Mac download proof remains.

## What the Mac nightly measured (2026-10-03)

MEASURED by the scheduled macOS nightly, run `37118791671` at `0e34798c6`, shard 11 (job
`111191164571`, `macos-26-intel`, so `x86_64-darwin`), whose `TestMacImageSubstitutesFromCachix`
passed in 163.6 s:

| Variant | Would build | Would fetch | Of those, from yolo-jail.cachix.org |
| :--- | ---: | ---: | ---: |
| stock | 13 | 579 | 2 |
| `zbar` | 15 | 617 | 2 |
| `libsodium.dev` | 15 | 580 | 2 |

**Every derivation in the "would build" column is `x86_64-darwin`.** None is Linux. The stock
list is nix2container's own tool (`nix2container-1.0.0`), its JSON metadata (`layers.json` twice,
`closure-graph.json` twice, `rewrites.json` twice, `config.json`, `history.json`,
`image-yolo-jail.json`) and three symlink trees (`bin-path-links`, `yolo-jail-prefix-links`,
`yolo-jail-root`). A `packages:` variant adds one more `layers.json` and `closure-graph.json`, its
extra layer. A dry run of `.#packages.x86_64-darwin.ociImage` at `337086f64`, evaluated on Linux on
2026-10-08, lists the same 13 (MEASURED).

**Why no cache serves them** (READ, [`flake.nix`](../../flake.nix) and
[`nightly-macos.yml`](../../.github/workflows/nightly-macos.yml)):

- The flake builds these with the **host** `pkgs`, on purpose: nix2container is imported with the
  host's `pkgs` because its generator runs at build time on the host, and `bin-path-links`,
  `yolo-jail-prefix-links` and `yolo-jail-root` are `pkgs.runCommand` and `pkgs.symlinkJoin`. Only
  the image's contents come from `imagePkgs`, the Linux package set.
- So a Mac's helper derivations have the system `x86_64-darwin` or `aarch64-darwin`, and their
  store paths differ from the ones a Linux runner builds and pushes. Every push in CI runs on a
  Linux runner (`publish.yml`, and the nightly's `build-image` and `push-arm-image-cache`).
- The Mac jobs that do realize them push nothing: `archive-delivery-macos` sets `skipPush: true`
  by an earlier decision (its comment: the darwin paths "would only grow the cache"), and the
  `integration-macos` shards configure no Cachix action at all.

**What the 2 hits are.** The Linux image's own closure at `337086f64` is 578 paths. Every one of
them is on `cache.nixos.org` except four, and all four are on yolo-jail.cachix.org (MEASURED, each
path's `.narinfo` asked of both caches): `nix-ld-2.0.6`, the flake's `overrideAttrs` of nixpkgs'
nix-ld, and the three host-built paths a Linux build makes (`image-yolo-jail.json`,
`yolo-jail-root`, `bin-path-links`). A Mac builds its own copies of the host-built three, so of
the four only nix-ld is one it fetches from our cache. The log names no paths, so which path is
the second hit is not established; INFERRED: a Linux path outside the stock closure, such as one
a Linux helper pulls in.

**What this changes.** The handoff's premise was that a Mac without a Linux builder cannot build
the image. Measured, it does not need one: every Linux path is fetched, and the remaining builds
run on the Mac's own darwin `stdenv`. What they cost on a Mac is **not measured**; the dry run
builds nothing. The largest is nix2container's Go build; `yolo-jail-root` is about 27 MB of links,
and each `layers.json` hashes its layer's store paths.

### Proposed fix

Two changes, neither made here (`flake.nix` stays as it is; both are CI or test edits):

- **A — Report the truth.** `TestMacImageSubstitutesFromCachix` prints "WOULD BUILD 13
  derivation(s) no substituter serves", which reads as a gap. Split the list by system: a
  host-system build needs no Linux builder, and only an image-system (`*-linux`) build does. A
  `WOULD BUILD` verdict would then name the Linux builds alone, and this run would have reported a
  substitution, with the 13 darwin helpers listed apart as built locally. The test already names
  each derivation's system.
- **B — Push the darwin helpers.** Let one Mac job push what it realizes, by dropping
  `skipPush: true` in `archive-delivery-macos` (or adding `cachix/cachix-action` to one
  `integration-macos` shard that builds `.#ociImage`). The cost is the growth the earlier
  decision named: the JSON files and `yolo-jail-root` change with every image change, and the job
  would also push the per-commit install prefix unless a `pushFilter` excludes it. ⚠ It covers
  only `x86_64-darwin`: the nightly's Mac shards run on `macos-26-intel`, and an Apple silicon
  Mac would need a push from a `macos-latest` job too.

Recommended: A, now; B only if a timed build on an Apple silicon Mac shows these builds cost
more than a few seconds.

## Setup runbook (wiring done; only the Mac proof remains)

1. **Create the cache.** ✅ Done — the **public** `yolo-jail` cache exists at
   <https://app.cachix.org>. (Cache names are **global**; the wiring assumes
   **`yolo-jail`**. If a fork needs a different name, see step 5.)

2. **Enable the substituter in `flake.nix`.** ✅ Done — the `nixConfig` block is
   live in `flake.nix`'s `nixConfig` with the committed public key:
   ```nix
   nixConfig = {
     extra-substituters = [ "https://yolo-jail.cachix.org" ];
     extra-trusted-public-keys = [ "yolo-jail.cachix.org-1:6SMCmaSd8DsVfj5EHAdpgIZi0RE14zyYrAWnV8WxFLM=" ];
   };
   ```

3. **Add the CI credential** (GitHub → repo Settings → Secrets and variables →
   Actions). ✅ Done:
   - **Secret** `CACHIX_AUTH_TOKEN` = a **write** auth token from Cachix
     (cache → Settings → Auth Tokens, or `cachix authtoken`). This is the ONLY
     thing CI gates on — now that it exists, `push-image-cache` runs on the next
     release.
   - **Variable** `CACHIX_CACHE` (optional) = the cache name. Defaults to
     `yolo-jail` when unset; only set it to push to a differently-named cache
     (e.g. a fork's).

4. **First push — ✅ DONE by CI**, not by hand: the release-gated `push-image-cache`
   job did it on the `v0.8.0` tag (run `31749547095`, 2026-08-13, both arches). The
   manual route below still works and is the way to push a closure **between**
   releases, since the CI trigger is tag-only:
   ```sh
   nix profile install nixpkgs#cachix     # if cachix isn't installed
   cachix authtoken <write-token>          # or: export CACHIX_AUTH_TOKEN=…
   just cachix-push                        # builds + pushes both variants + the copier
   #   (override name: just cachix-push CACHE=my-cache)
   ```

5. **If you chose a different cache name than `yolo-jail`:** rename it in
   three places — the `flake.nix` `nixConfig` URLs+key, the `just cachix-push`
   `CACHE` default, and set the `CACHIX_CACHE` repo variable (which otherwise
   defaults to `yolo-jail` in CI).

## Final test (on a Mac, no builder needed)

**The unattended half, written 2026-10-01.** `TestMacImageSubstitutesFromCachix` runs on any
darwin host with nix, which makes it part of the macOS nightly's sharded run on the hosted
`macos-26-intel` runners. For the stock image and the two `packages:` variants `build-image`
pushes for the shards (`["zbar"]`, `["libsodium.dev"]`) it runs `nix build --dry-run` of
`.#ociImage` with `--accept-flake-config`, names each derivation nix would BUILD with its system,
and asks `yolo-jail.cachix.org` for each path nix would FETCH (`<hash>.narinfo`; the URL is read
from `flake.nix`'s `nixConfig`). It logs separate host-system helper, Linux-image, and unknown-system
build lists; only known Linux-image derivations are reported as requiring Linux builds. Cache hits
remain aggregate counts across fetched paths. It builds nothing. A measurement: only a dry run that
planned nothing fails it. Unknown derivation systems cannot produce a no-Linux-build verdict.
It does not replace the run below, which is the one a user's own Mac makes.
Its first result, from the 2026-10-03 nightly, is in
[What the Mac nightly measured](#what-the-mac-nightly-measured-2026-10-03).

This is the whole point — a macOS user with NO builder should get the image
by download (if it *did* have to build, it would offload to a container on the
active runtime — but the cache should make that unnecessary):

```sh
# fresh Mac / clean nix store, no builder:
cd some-project && yolo init
yolo check          # Image Build: should PASS by substituting from the cache
                    #   ("every image path is served from the binary cache")
yolo -- claude      # boots without ever building a Linux derivation
```

If `yolo check` still says a package must be built from source, the cache
doesn't have that path yet — re-run `just cachix-push` after the change that
introduced it (or it's a custom `{version,url,hash}` package, which is never
cacheable by construction).

## Notes / decisions already made

- **Cadence:** set by the `on:` triggers of `publish.yml` (`push.tags: v*` plus
  `release.types: [published]`) — the load-bearing trigger is the tag push. The
  `push-image-cache` job has **no** job-level `if:`; it gates per-step on the
  `CACHIX_AUTH_TOKEN` secret (its `gate` step). For per-merge freshness, add
  `push: branches: [main]` to `on:`, not a job `if:`.
- **Fallback builder** for users who add custom uncached packages: the
  **ephemeral container builder** — a tiny nix+sshd container a normal `yolo`
  run offloads the build to on the active runtime (podman/Apple Container) over
  `ssh-ng`, then tears down (zero idle RAM, no VM, no `sudo`, no `yolo builder`
  command). The single shipped/documented fallback, per the
  [fill-the-matrix principle](../reference/fill-the-matrix-principle.md); see
  `linux-builder-lifecycle.md` (archived 2026-09-09 — the removal is DONE, git has the file,
  and the mechanism is in [`macos-linux-builder-explained.md`](../research/macos-linux-builder-explained.md)).
  (A user's *own* nix-darwin `linux-builder` or `/etc/nix/machines` box still
  works as an advanced escape hatch — that's their nix config, orthogonal to
  ours.)
- **Alternative if you never want Cachix:** publish the built image tarball
  as a GitHub Release asset and have the CLI download+`load` it — no cache
  infra, everything on GitHub. Not wired; mentioned as an escape hatch.
