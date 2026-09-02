# Handoff — publish the prebuilt image to a Cachix cache

**Status:** **WORKING** — the push has happened and the cache is being read.
**Settled 2026-09-02 from the Actions log**, which closes the disagreement this doc
carried against [`README.md`](README.md): README's *"CI has already pushed data"* was
the correct sentence. Remaining: only the Mac-side download proof ("Final test" below),
which needs the hardware.

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
> **Scope caveat worth keeping:** the push is **release-gated only** (`on: push: tags:
> v*`), so the cache holds `v0.8.0`'s closure and nothing newer. Between releases a
> consumer gets a cache hit on the release-day paths and builds the delta.

**Why:** the OCI image contains a few `aarch64-linux` derivations built from
*this repo's* source (`yolo-jail-conf`, the entrypoint pkg, the stream
script) that are **never** on `cache.nixos.org`. So building the image on
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
- **Justfile** — `just cachix-push` builds both image variants on a Linux
  host and pushes their closures.
- **.github/workflows/publish.yml** — the `push-image-cache` job (release-gated)
  builds + pushes on every published release. It gates on the
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
   live at `flake.nix:13-16` with the committed public key:
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
   just cachix-push                        # builds + pushes both variants
   #   (override name: just cachix-push CACHE=my-cache)
   ```

5. **If you chose a different cache name than `yolo-jail`:** rename it in
   three places — the `flake.nix` `nixConfig` URLs+key, the `just cachix-push`
   `CACHE` default, and set the `CACHIX_CACHE` repo variable (which otherwise
   defaults to `yolo-jail` in CI).

## Final test (on a Mac, no builder needed)

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

- **Cadence:** set by the `on:` triggers of `publish.yml` (`push.tags: v*` at
  ~lines 22-25 + `release.types: [published]` at ~lines 26-27) — the
  load-bearing trigger is the tag push. The `push-image-cache` job (~line 85)
  has **no** job-level `if:`; it gates per-step on the `CACHIX_AUTH_TOKEN` secret
  (a `gate` step at ~line 101). For per-merge freshness, add
  `push: branches: [main]` to `on:`, not a job `if:`.
- **Fallback builder** for users who add custom uncached packages: the
  **ephemeral container builder** — a tiny nix+sshd container a normal `yolo`
  run offloads the build to on the active runtime (podman/Apple Container) over
  `ssh-ng`, then tears down (zero idle RAM, no VM, no `sudo`, no `yolo builder`
  command). The single shipped/documented fallback, per the
  [happy-path principle](../design/happy-path-principle.md); see
  [../design/linux-builder-lifecycle.md](../design/linux-builder-lifecycle.md).
  (A user's *own* nix-darwin `linux-builder` or `/etc/nix/machines` box still
  works as an advanced escape hatch — that's their nix config, orthogonal to
  ours.)
- **Alternative if you never want Cachix:** publish the built image tarball
  as a GitHub Release asset and have the CLI download+`load` it — no cache
  infra, everything on GitHub. Not wired; mentioned as an escape hatch.
