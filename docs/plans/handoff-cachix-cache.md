---
title: "Handoff — publish the prebuilt image to a Cachix cache"
status: accepted
stage: BUILT
next: "Record the CACHIX lines in this status line: the 2026-10-03 scheduled macOS nightly (GitHub Actions run 37118791671, shard 11, at 0e34798c6) passed TestMacImageSubstitutesFromCachix, each case would build 13 to 15 derivations no substituter serves, and 2 of about 600 fetched paths came from yolo-jail.cachix.org"
---

# Handoff — publish the prebuilt image to a Cachix cache

**Status:** 2026-09-02, re-verified 2026-09-30 — **working**: every release since
`v0.8.0` has pushed (the latest, `v0.11.0` on 2026-09-28, run `36473315012`, logged `Pushed
image closures to yolo-jail.cachix.org` on both arches), and since `1006fe6d` (2026-09-13) the
macOS nightly pushes on every run as well (`.github/workflows/nightly-macos.yml`, the
`build-image` and `push-arm-image-cache` jobs). MEASURED: CI pushes both Linux arches and
substitutes the four this-repo-source paths back from the cache (run `31749547095`, below).
UNMEASURED: the Mac-side download — no Mac, human or CI runner, has been shown substituting these
paths rather than building them ("Final test" below). The instrument for it was written on
2026-10-01 and has not run yet: `TestMacImageSubstitutesFromCachix`
([`maccachixsubstitution_test.go`](../../integration/maccachixsubstitution_test.go)), which falls
into one shard of the macOS nightly's computed partition.
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

**Why:** the OCI image contains a few `aarch64-linux` derivations built from
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
from `flake.nix`'s `nixConfig`). It logs one `CACHIX <variant>:` line each (SUBSTITUTES, WOULD
BUILD, NOTHING TO DO when an earlier test already realized it, or VOID when nix ignored the cache
for an untrusted user) and builds nothing. A measurement: only a dry run that planned nothing
fails it. It does not replace the run below, which is the one a user's own Mac makes.

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
