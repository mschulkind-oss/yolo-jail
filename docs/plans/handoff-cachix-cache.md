# Handoff — publish the prebuilt image to a Cachix cache

**Status:** **ENABLED** (2026-07-20) — the `yolo-jail` cache exists, the
`nixConfig` substituter + public key are live in `flake.nix`, and the
`CACHIX_AUTH_TOKEN` secret is set, so the release-gated push job now runs.
Remaining: the first push + the Mac-side download proof (steps 4 and "Final
test" below), which need a Linux box and a Mac respectively.
**Why:** the OCI image contains a few `aarch64-linux` derivations built from
*this repo's* source (`yolo-jail-conf`, the entrypoint pkg, the stream
script) that are **never** on `cache.nixos.org`. So building the image on
macOS needs a Linux builder — *unless* we publish the built image to a
binary cache that macOS users can download from. Publishing = the "everybody,
zero setup, at any point" happy path; a per-machine Linux builder becomes the
rare fallback (custom uncached packages only).

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
- **Proven:** the image builds cleanly on Linux here (`nix build .#ociImageMinimal`
  → exit 0), so the build/publish-from-Linux path is validated; the first
  actual push + the Mac download proof remain.

## Setup runbook (wiring done; first push + Mac proof remain)

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

4. **First push (prove it) — still TODO, from a Linux box:**
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

- **Cadence:** set by the `on:` triggers of `publish.yml` (`push.tags: v*` at
  ~lines 22-25 + `release.types: [published]` at ~lines 26-27) — the
  load-bearing trigger is the tag push. The `push-image-cache` job (~line 85)
  has **no** job-level `if:`; it gates per-step on the `CACHIX_AUTH_TOKEN` secret
  (a `gate` step at ~line 101). For per-merge freshness, add
  `push: branches: [main]` to `on:`, not a job `if:`.
- **Fallback builder** for users who add custom uncached packages:
  **nix-darwin `linux-builder`** (persistent, launchd-managed) — the single
  documented builder in `docs/guides/macos.md`, per the
  [happy-path principle](../design/happy-path-principle.md).
- **Alternative if you never want Cachix:** publish the built image tarball
  as a GitHub Release asset and have the CLI download+`load` it — no cache
  infra, everything on GitHub. Not wired; mentioned as an escape hatch.
