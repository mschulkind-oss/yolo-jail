# Handoff — publish the prebuilt image to a Cachix cache

**Status:** wired, **not yet enabled** — pending a Cachix account (deferred
by the maintainer; will be done on another setup).
**Why:** the OCI image contains a few `aarch64-linux` derivations built from
*this repo's* source (`yolo-jail-conf`, the entrypoint pkg, the stream
script) that are **never** on `cache.nixos.org`. So building the image on
macOS needs a Linux builder — *unless* we publish the built image to a
binary cache that macOS users can download from. Publishing = the "everybody,
zero setup, at any point" happy path; a per-machine Linux builder becomes the
rare fallback (custom uncached packages only).

## What's already wired (works the moment the cache exists)

- **flake.nix** — a commented `nixConfig` block with the substituter +
  trusted-public-key. Enabling = uncomment + paste the key.
- **Justfile** — `just cachix-push` builds both image variants on a Linux
  host and pushes their closures.
- **.github/workflows/publish.yml** — a `push-image-cache` job (release-gated)
  that builds + pushes on every published release. It **no-ops** until the
  secret + variable below exist, so releases stay green in the meantime.
- **Proven:** the image builds cleanly on Linux here (`nix build .#ociImageMinimal`
  → exit 0), so the build/publish-from-Linux path is validated; only the
  Cachix account + push remain.

## Do this once (the deferred signup + wiring)

1. **Create the cache.** Sign in at <https://app.cachix.org>, create a cache.
   Cache names are **global** — the wiring assumes **`yolo-jail`**; if that's
   taken, pick another and see step 5. Make it **public** (so users read
   without auth).

2. **Enable the substituter in `flake.nix`.** Uncomment the `nixConfig` block
   near the top and replace `<PUBLIC_KEY>` with the key Cachix shows on the
   cache's "Settings → Public key" (format
   `yolo-jail.cachix.org-1:AAAA…=`):
   ```nix
   nixConfig = {
     extra-substituters = [ "https://yolo-jail.cachix.org" ];
     extra-trusted-public-keys = [ "yolo-jail.cachix.org-1:<PUBLIC_KEY>" ];
   };
   ```

3. **Add the CI credential** (GitHub → repo Settings → Secrets and variables →
   Actions):
   - **Secret** `CACHIX_AUTH_TOKEN` = a **write** auth token from Cachix
     (cache → Settings → Auth Tokens, or `cachix authtoken`). This is the ONLY
     thing CI gates on — once it exists, `push-image-cache` runs on the next
     release.
   - **Variable** `CACHIX_CACHE` (optional) = the cache name. Defaults to
     `yolo-jail` when unset; only set it to push to a differently-named cache
     (e.g. a fork's).

4. **First push (prove it), from a Linux box:**
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

## Final test (on a Mac, no Linux builder configured)

This is the whole point — a macOS user with NO builder should get the image
by download:

```sh
# fresh Mac / clean nix store, no linux-builder:
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

- **Cadence:** push on **published releases** only (the `push-image-cache`
  job in `publish.yml`), not on every `main` merge. Change the job's `if:`
  if you want per-merge freshness.
- **Fallback builder** for users who add custom uncached packages:
  **nix-darwin `linux-builder`** (persistent, launchd-managed) — the single
  documented builder in `docs/guides/macos.md`, per the
  [happy-path principle](../design/happy-path-principle.md).
- **Alternative if you never want Cachix:** publish the built image tarball
  as a GitHub Release asset and have the CLI download+`load` it — no cache
  infra, everything on GitHub. Not wired; mentioned as an escape hatch.
