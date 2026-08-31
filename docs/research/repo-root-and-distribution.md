# Why `yolo` Needs a Flake — and How Installs Get One

> **Status: CURRENT — written 2026-07-23, amended 2026-07-29 (D2 revert) and
> 2026-08-31 (the cwd-walk removal, §2a), re-checked against the tree
> 2026-08-23.** The load-bearing claim was
> re-verified today: a missing repo root is **FATAL**, not a degraded launch —
> `5d34dece` *"fix(run)!: a missing repo root is fatal again, not a degraded
> launch"* (2026-07-29) is in `main`, and §6 already carries the reasoning. Do
> not resurrect D2's graceful degradation; §6 explains why the soft path
> silently launched jails on stale images.
>
> This doc describes how a checkout-less
> install builds the jail image today, after the **prebuilt-bundle cutover**.
> The bundle is no longer a copy of the source tree — it is "two files and a
> binary" (`flake.nix` + `flake.lock` + prebuilt `bin/linux-<arch>/`), and the
> flake builds the image from it via a **prebuilt short-circuit** with no Go
> toolchain and no source. The old staging machinery (`stageInstalledWheel`,
> `nix-build-root`, the `/opt/yolo-jail` bind mount) is **gone**; where its
> history explains the design, "Historical" callouts preserve it. Plan:
> `docs/plans/macos-revival-and-distribution-plan.md` (Track D).

**TL;DR** — `yolo` builds the jail's container image from a **flake**
(`nix build .#ociImage`) on first run, so every `yolo -- <cmd>` invocation must
first locate a yolo-jail flake. That flake either compiles the Go binaries from
source *or* consumes prebuilt ones, decided purely by what sits next to it. Four
ways `yolo` finds a flake now (`internal/reporoot.Resolve`) — **three, since the
cwd walk was removed on 2026-08-31 (§2a)**:

1. **`YOLO_REPO_ROOT` env** — CI and the integration harness set it; validated
   to actually contain `flake.nix` **or** `go.mod`. Also the ONLY way to point
   yolo at a **live checkout**, from any directory, including from inside that
   checkout.
2. **From a shipped/baked bundle** — an exe-relative `share/yolo-jail/` bundle
   (Homebrew, the release archive, and the in-image baked `/opt/yolo-jail`
   prefix all use this ONE method). **No checkout required.**
3. **From the state-dir bundle** — `paths.FlakeBundleDir()`
   (`~/.local/share/yolo-jail/flake-bundle`), which a from-source `just install`
   stages so the install is **self-contained**: an installed `yolo` builds the
   image from any directory. Last, because a real distribution bundle (step 2)
   ships with the binary that is running. Its path comes from the binary
   (`yolo internal bundle-dir`), and it is a dedicated leaf UNDER the state dir —
   never the state dir itself.

Every container launch **reports which one it took**, in Phase 1 before pack
staging and the nix build: `Flake source: <path> (<what selected it>)`.
`yolo check` appends the same provenance to its `flake.nix found:` line.

> **Retired (2026-07-23): the `repo_path` config key.** A step used to read
> `repo_path` from `~/.config/yolo-jail/config.jsonc`, written by
> `just install`/`just deploy`. It was dropped in favor of the exe-relative
> bundle (step 2 above). The state-dir bundle (step 3, added 2026-07-29) restores
> the *self-contained from-source install* the `repo_path` note below worried about
> — but as a **prebuilt bundle keyed to a fixed yolo-owned path**, not a config
> pointer at a live checkout. The concern that "a staged prebuilt bundle would
> build jails from stale artifacts, not their edits" was answered by precedence
> while the cwd-walk existed; since its removal (§2a) the answer is different and
> more explicit — a developer who wants live source **says so** with
> `YOLO_REPO_ROOT`, and otherwise `just install` is the step that delivers their
> edits to the staged bundle. The `repo_path` key is still *tolerated*
> (known key, so an existing config does not hard-error) but ignored with a
> deprecation warning; the `internal/repopath` package + `yolo internal
> write-repo-path` subcommand were deleted.

Only if all four miss do you get the actionable error:

```
Cannot find yolo-jail repo root.
The yolo CLI needs the repo for nix image builds.

Fix: install so the flake bundle ships with the binary (`just install`), or
point yolo at a checkout with YOLO_REPO_ROOT — the cwd is never
consulted:
  YOLO_REPO_ROOT=~/code/yolo-jail yolo …
```

> **Historical: the regression.** The old Python wheel bundled the source
> *inside itself*, so an installed `yolo` was self-contained. The Go port lost
> that for a while (a Go binary has no setuptools package-data). It was first
> restored by shipping a *copy of the source tree* beside the binary (D3,
> 2026-07-20), then simplified to the prebuilt bundle described here — the
> binaries the flake would have compiled ship directly, so the source tree need
> not.

---

## 1. Why does running a jail need a *flake* at all?

Because **the jail image is not pulled from a registry — it is built from a
flake on first run.**

The launch path (`internal/cli/run/run.go`) resolves the repo root as its very
first probe and threads it through the launch:

- `run.go` — `repoRoot, repoRootOK := o.RepoRoot()` is the first thing `Run()`
  does. Resolution is a **hard gate** for the container backends: an
  unresolvable repo root exits 1 with an actionable message rather than
  launching on a possibly-stale cached image. (This reverts D2's graceful
  degradation — see §6 for why the soft path was removed.) The gate fires
  *after* the macos-user branch, which needs no repo when `packages:` is empty.
- `run.go` — `repoRoot` becomes the argument to `autoLoadImage`.

`autoLoadImage` runs the Nix build **in `repoRoot` as the working directory**,
building the flake **in place** — there is no copy-to-a-staging-dir step:

- `internal/image/autoload.go:314-322` — argv is
  `nix --extra-experimental-features "nix-command flakes" build .#ociImage --impure --out-link <link> --print-build-logs`,
  and `cmd.Dir = repoRoot`.
- `.#ociImage` is a **relative flake reference** — `.` means "the flake in the
  current directory," so Nix reads `flake.nix` out of `repoRoot`.

And `flake.nix` produces the image's Go binaries **two ways**, chosen by what is
on disk next to it (`flake.nix:77-122`, `goBinaries`):

- **Prebuilt short-circuit** — if `./bin/linux-<arch>` exists
  (`builtins.pathExists prebuiltBinDir`), copy those prebuilt binaries in. No Go
  toolchain, no source tree read.
- **Compile from source** — otherwise, a `stdenv.mkDerivation` runs
  `for d in cmd/*/; do go build …; done` over the `goSrc` fileset (`go.mod`,
  `go.sum`, `vendor/`, `cmd/`, `internal/`, `bundled_loopholes/`), hermetically
  (`CGO_ENABLED=0`, `-mod=vendor`, no network).

**A git flake only ever sees *tracked* files, and the repo never commits
`bin/`** (enforced by `.gitignore: /bin/`). So an in-repo build (a source
checkout, the self-hosting `/workspace` jail, or `just build-image`) always
compiles from source, while a `path:` flake — the shipped release archive, the
Homebrew bundle, or the in-image baked `/opt/yolo-jail/share/yolo-jail` — sees
`bin/linux-<arch>` and short-circuits. **One flake, one resolution method, two
outcomes decided purely by what is on disk.** This is what lets "two files and a
binary" build the same image the source tree does.

Two secondary consumers of `repoRoot`, for completeness:

- **Version string** — `version.Get(repoRoot)` runs `git describe` in the repo
  for the banner (`run.go:433`, via `emitStartupBanner`).
- **`bundled_loopholes`** — resolved from the repo when present, else from the
  binary's `go:embed` copy (`internal/loopholes`, `materializeEmbedded`). The
  prebuilt bundle carries **no** `bundled_loopholes` tree, so an installed
  binary uses the embedded copy — the normal production path.

> **`--impure` is load-bearing, not incidental.** It exists so `flake.nix` can
> read `YOLO_EXTRA_PACKAGES` via `builtins.getEnv` (`flake.nix`, the *only*
> `getEnv` in the flake), which the CLI sets from your `packages:` config
> (`autoload.go:308-312`). Under pure eval `getEnv` returns `""` and per-project
> packages silently no-op.

---

## 2. How the repo root is resolved

`internal/reporoot.Resolve(getenv)` is **THE single shared resolver** used by
both `yolo run` (`internal/cli/run/probes.go` → `resolveRepoRoot`, which adds
only the run-side error banner) and `yolo check`
(`internal/cli/check/probes.go` → `resolveRepoRoot`, a thin delegate). Both
agree on where the repo is, and — critically — **it resolves identically inside
and outside the jail**. There is no in-jail-special code path any more.

| # | Step | Works for an *installed-only* binary? | Source |
|---|------|----------------------------------------|--------|
| 1 | `YOLO_REPO_ROOT` env, if it contains `flake.nix` **or** `go.mod` | Yes — CI / integration harness, and the only way to name a live checkout | `FromEnv` |
| 2 | **Exe-relative bundle** (`BundledSourceDirFrom`) | **Yes — Homebrew / release archive / baked `/opt/yolo-jail`** | `FromBesideBinary` |
| 3 | **State-dir bundle** (`paths.FlakeBundleDir`, `~/.local/share/yolo-jail/flake-bundle`) | **Yes — a from-source `just install`, from any dir** | `FromInstalledBundle` |

_(Two earlier steps are gone: a user-config `repo_path` read, retired 2026-07-23,
and the cwd walk, removed 2026-08-31 — see §2a.)_

`Resolve` returns a `Resolution{Root, Source}`, not a bare path: the `Source`
column above is what a launch prints, so "which flake, and why that one" has one
answer in one place rather than being re-derived per caller.

Notes on the guards, which are deliberate:

- **Step 1** accepts `flake.nix` **or** `go.mod` — a lenient override for CI.
- **Step 2** requires only `flake.nix` (the bundle has no `go.mod`).
- **Step 3** requires only `flake.nix`, at a fixed yolo-owned path. It is LAST so
  a real distribution bundle (step 2) — which shipped with the very binary that
  is running — wins over one a previous `just install` happened to leave behind.

### 2a. The cwd walk, and why it was removed (2026-08-31)

A step 2 used to walk up from the working directory for a dir holding **both**
`flake.nix` and `go.mod` (both, so a bare `flake.nix` could not hijack a user's
own flake workspace), and it outranked every bundle. Its purpose was the
from-source developer: `cd ~/code/yolo-jail && yolo` built the image from that
live tree.

It was removed because **the working directory is not a thing a user picks in
order to say something about the jail image.** Two costs:

- **The same command did different things in different directories.** One
  binary, one config: a live checkout here, an install-time snapshot one
  directory up — for a multi-gigabyte artifact, with no way to tell from the
  output. The startup banner cannot disambiguate, because its version string is
  `version.Get(repoRoot)`, a `git describe` **of whatever root won**.
- **It was the only way the two halves of yolo could disagree.** Both bundles
  ship WITH the binary, so neither can be older than it; only a live checkout
  moves independently, and the host launcher moves only at `just install`. That
  gap is what `version.SourceSkew` refuses — and the walk opened it *by default*
  for anyone standing in the repo, which for this repo's maintainer is always.
  (The refusal is still there, and now fires only on a root someone asked for.)

What replaced it: `YOLO_REPO_ROOT` — already the CI and integration-harness
override — plus the `Flake source:` report, so the selection is stated rather
than inferred. Consequences worth stating plainly:

- A **from-source developer** gets the staged bundle in every directory,
  including their checkout. `just install` is therefore the delivery step for an
  image change, not a chore to remember before launching. Exporting
  `YOLO_REPO_ROOT=~/code/yolo-jail` restores build-from-live-source everywhere,
  with the skew gate still guarding it.
- **In-jail**, a bare `yolo` resolves the baked `/opt/yolo-jail` bundle rather
  than a self-hosted `/workspace` checkout, so nested-jail verification of a Go
  or `flake.nix` change must pass `YOLO_REPO_ROOT=/workspace`. `AGENTS.md` and
  the `developing-yolo-jail` skill carry that in every nested command.

Pinned by `TestResolveIgnoresCheckoutInCwd` / `TestResolveIgnoresCheckoutAboveCwd`
(the resolver), `TestResolveRepoRootIgnoresCwd` (run's wrapper), and
`TestRunReportsTheFlakeSource` (the report, at its call site in `Run`).

**Step 2 — the exe-relative bundle — is the checkout-less path.**
`BundledSourceDir` (`reporoot.go`, pure core `BundledSourceDirFrom`) looks for a
`flake.nix` at three executable-relative candidates, **all variants of one
method**:

- `<exe>/../share/yolo-jail` — the Homebrew layout (`bin/yolo`,
  `prefix/share/yolo-jail/…`) **and** the in-image baked prefix
  (`/opt/yolo-jail/bin/yolo`, `/opt/yolo-jail/share/yolo-jail`).
- `<exe>/share/yolo-jail` — the release-archive layout (`yolo` and `share/` at
  one level).
- `<exe>` itself — a bundle unpacked directly beside the binary.

When one hits, `Resolve` returns that dir **directly** — there is no staging,
no copy. `nix build .#ociImage` then runs with `cwd` = the bundle dir, and the
flake's prebuilt short-circuit builds the image from the bundle's
`bin/linux-<arch>`. (Standing in a source checkout changes nothing about this —
only `YOLO_REPO_ROOT` outranks it.)

> **Historical: staging is gone.** The wheel-era and the first Go bundle both
> *copied* the resolved bundle into `~/.local/share/yolo-jail/nix-build-root`
> (`stageInstalledWheel`) and built from that copy — because the source tree had
> to be rehydrated onto a writable path and the copy was also bind-mounted into
> the jail at `/opt/yolo-jail`. Both are removed. The bundle is read-only and
> self-sufficient (a flake + prebuilt binaries), so Nix builds it where it sits;
> the in-jail CLI gets its flake from the **baked** `/opt/yolo-jail` (an image
> layer, not a mount). `prune` still sweeps stray `nix-build-root*` dirs left by
> pre-cutover installs — clearly-legacy cleanup, see `internal/prune/sweep.go`.

---

## 3. Python (uvx) vs Go vs prebuilt — the evolution

### How the Python wheel made an installed `yolo` self-contained

The Python distribution shipped the **entire source tree inside the wheel** as
package data (`pyproject.toml` `[tool.setuptools.package-data]`; `src/flake.nix`
and `src/flake.lock` were git symlinks that setuptools dereferenced into real
files). At runtime `_resolve_repo_root` detected the installed package and
**rehydrated the bundled tree** into `~/.local/share/yolo-jail/nix-build-root`,
returning that as the repo root. Net effect: `uvx` / `uv tool install` carried
the flake + source inside the install and unpacked it on first run.

> ### Nuance worth recording (an adversarial-verify catch)
>
> By the very last Python commit (`c7e210d~1`), the flake had **already**
> switched to compiling the Go binaries from root-level `go.mod` / `cmd/` /
> `internal/` — which the wheel's package-data (`src/` + flake files only) did
> **not** ship. So at the transition point the wheel was self-contained for
> **repo-root *resolution*** but not for the **image *build***. Read every "the
> wheel was self-contained" claim as *for resolution*.

### What the Go port changed — and where it landed

- `c7e210d` (*"wipe(python)"*) deleted the Python `src/` tree. A Go binary has
  no setuptools package-data mechanism, so for a while nothing shipped a flake
  beside the binary — the regression.
- **D3 (2026-07-20)** first restored it by shipping a **copy of the source
  tree** (`git archive HEAD`, the whole tracked tree, ~11 MB) at
  `share/yolo-jail/`, and `stageInstalledWheel` copied it flat into
  `nix-build-root` before building.
- **The prebuilt cutover (2026-07-23)** replaced that with the model this doc
  describes: the flake gained a **prebuilt short-circuit**, and the bundle
  shrank to `flake.nix` + `flake.lock` + `bin/linux-{amd64,arm64}/` (the four
  shipped binaries). `stageInstalledWheel` and `nix-build-root` staging were
  deleted; the flake builds from the bundle in place. The image itself bakes the
  same bundle at `/opt/yolo-jail` (real-file binaries + a `share/yolo-jail`
  flake bundle), replacing the old `/opt/yolo-jail` source **bind mount** and
  the dev-override wrapper — so the in-jail and host resolution paths became
  identical.

### The bundle today (`scripts/stage-source-bundle.sh`)

The bundle producer cross-compiles both Linux arches (CGO off, so no C
toolchain) and stages exactly:

```
share/yolo-jail/
├── flake.nix
├── flake.lock
├── bin/linux-amd64/{yolo,yolo-entrypoint,yolo-jaild,yolo-ps}
└── bin/linux-arm64/{yolo,yolo-entrypoint,yolo-jaild,yolo-ps}
```

`goprobe` is **excluded** (a dev-only deployment tripwire; the script asserts it
never leaks in). The bundle is **arch-agnostic**: the same tree ships in every
platform archive, and the flake selects `bin/linux-<arch>` at eval time. The
bundle is proven to build: `nix eval .#ociImage.drvPath` succeeds on it (and
`.#goBinaries.name` evaluates to `yolo-jail-go-prebuilt`, confirming the
short-circuit fires).

---

## 4. Distribution channels — does each ship a buildable flake?

| Channel | How | Ships a buildable flake? | Evidence |
|---|---|---|---|
| **Homebrew tap** (`mschulkind-oss/homebrew-tap`) | `release.yml` generates a **source-build** formula (`depends_on go`): `go build ./cmd/yolo`, then `scripts/stage-source-bundle.sh` produces the **prebuilt** bundle into `pkgshare` | **Yes** — `flake.nix`/`flake.lock` + `bin/linux-{amd64,arm64}/` at `prefix/share/yolo-jail` → `<exe>/../share/yolo-jail` | `.github/workflows/release.yml` install block |
| **GitHub Release tar.gz** (goreleaser) | `before` hook runs `stage-source-bundle.sh`; archive `files:` ships it beside the binary | **Yes** — `yolo` + `share/yolo-jail/…` → `<exe>/share/yolo-jail` | `.goreleaser.yaml` before-hook + archives `files:` |
| **From source** (`git clone` + `just install`/`deploy`) | `go install ./cmd/yolo`, then `scripts/stage-source-bundle.sh` stages the **native-arch** bundle at `$(yolo internal bundle-dir)` = `~/.local/share/yolo-jail/flake-bundle` | **Yes** — the staged state-dir bundle (step 3), in every directory including the checkout. `YOLO_REPO_ROOT` (step 1) is the opt-in to live source | `README.md`, `Justfile` |
| **In-image baked prefix** | `flake.nix installPrefix` bakes real-file binaries + the `share/yolo-jail` bundle at `/opt/yolo-jail` (not a mount) | **Yes** — the in-jail `yolo` resolves it via step 2, identical to a host install | `flake.nix` `installPrefix` / `corePackages` |
| **PyPI wheel** | `tools/build-wheels` embeds only the `cmd/yolo` binary + metadata | **No** — no bundle wired (cutover did brew + goreleaser; wheel not yet) | `tools/build-wheels/main.go` |
| **Cachix binary cache** | prebuilt image closures for `nix` substitution | **Substituter live, cache not yet filled** (first push + Mac proof pending) | below |

Two remaining notes:

- **Cachix substituter is enabled, cache not yet filled** (D4). `flake.nix`'s
  `nixConfig` substituter + public key are **live** (`extra-substituters =
  ["https://yolo-jail.cachix.org"]`, ENABLED 2026-07-20), and `publish.yml`'s
  `push-image-cache` job runs when `CACHIX_AUTH_TOKEN` is set (the secret is
  configured). Human-gated remainder: the first push from a Linux box and the
  Mac-side download proof. See `docs/plans/handoff-cachix-cache.md`. **Even a
  fully-populated Cachix cache would *not* remove the flake requirement** — `nix
  build .#ociImage` against `.` must still *evaluate the local flake* to know
  which store paths to fetch. Cachix removes the *build*, not the *flake read* —
  so it composes with the bundle, it doesn't replace it. (With the prebuilt
  bundle the "build" is already just copies + a layered-image stream, so
  Cachix's win is smaller than it was under source compilation.)
- **No prebuilt *jail* image** is pushed to any OCI registry for `podman pull`.
  The only registry image is a separate builder helper.

**Bottom line (today):** Homebrew, the release archive, install-from-source, and
the in-image baked prefix all resolve a buildable flake — all four via the
prebuilt bundle now that `just install` stages it, with `YOLO_REPO_ROOT` the one
opt-in to a live checkout instead. The remaining gap is the **PyPI wheel** (no bundle wired) and **Cachix**
(substituter live, but the cache is not yet filled — first push + Mac download
proof pending).

---

## 5. The install-from-source path (no `repo_path`)

`just deploy` → `just install` (`deploy: install`):

- `install` stamps `buildVersion` + `GitCommit` via ldflags, runs
  `migrate-host`, then `go install ./cmd/yolo`, then stages the **native-arch**
  prebuilt bundle via `scripts/stage-source-bundle.sh` into the path the
  just-installed binary reports (`yolo internal bundle-dir` =
  `paths.FlakeBundleDir()` = `~/.local/share/yolo-jail/flake-bundle`).
  - **The path comes from the binary, not from `$GOBIN` arithmetic.** The first
    cut computed `$(dirname $GOBIN)/share/yolo-jail`, which for the common
    `GOBIN=~/.local/bin` collapses onto `~/.local/share/yolo-jail` — the entire
    yolo state dir — and the staging script leads with `rm -rf $DEST`. So
    `just deploy` deleted the state dir. Asking the binary for the ONE path it
    resolves (a dedicated leaf under the state dir, never the state dir itself)
    removes both the arithmetic and the drift. The script also refuses to
    `rm -rf` any non-empty dir that is not already a bundle (no `flake.nix` at
    its root), as a belt-and-suspenders guard.
  - **Native arch only.** The shipped bundle (goreleaser/brew) is arch-agnostic
    and builds both; a local install builds only `go env GOARCH` via
    `YOLO_BUNDLE_ARCHES`, so it reuses the warm cache from the `go install` just
    above instead of cold-cross-compiling the foreign arch.
- `migrate-host` (`internal/hostmigrate`) still retires the old Python install
  (uninstalls the `yolo-jail` uv tool, clears stale GOBIN console scripts) —
  only when positively identified as stale; an unidentifiable `yolo` *blocks*
  the install rather than being deleted.
- `deploy` then retires legacy systemd token-refresher units, primes the
  claude-oauth-broker state, and restarts the broker.

An installed-from-source `yolo` resolves the repo **two** ways now (the staged
bundle, or `YOLO_REPO_ROOT`), and the install is genuinely **self-contained** —
no external checkout is ever required. Since the cwd-walk was removed (§2a), the
staged state-dir bundle answers **every** directory, the checkout included, and
`YOLO_REPO_ROOT` is the single opt-in that points nix at the developer's LIVE
source.

> **Why the bundle is not "stale artifacts."** The concern is that a staged
> prebuilt bundle carries prebuilt binaries, not `goSrc`, so it would build jails
> from stale artifacts *for a source developer*. It used to be answered by
> precedence (a checkout in cwd outranked the bundle); it is now answered by
> **cadence**, which is the honest version: `just install` restages the bundle
> from the tree it installs, so the bundle is exactly as current as the launcher
> beside it — and `version.SourceSkew` refuses the pairing when it is not.
> A developer who wants the tree *ahead* of their last install exports
> `YOLO_REPO_ROOT`. The `~/code/forms6` case is unchanged: you want a working
> jail for forms6, not a rebuild of yolo-jail from your dirty tree — and now you
> get that even standing in the yolo-jail checkout.

> **Retired: `repo_path` + `write-repo-path` (2026-07-23).** `just install` used
> to run `yolo internal write-repo-path <checkout>`, which did an idempotent,
> comment-preserving JSONC edit to record `repo_path` in the user config (the
> `internal/repopath` package). That whole apparatus is gone — `install` no
> longer writes the key, the subcommand and package are deleted, and
> `reporoot.Resolve` no longer reads it. The key stays *tolerated* (a known key
> whose presence yields a deprecation warning, not a hard error) so an existing
> config keeps launching; `yolo check`/`yolo run` tell the user to remove it.
> Motivation: the bundle steps already covered every channel, and a from-source
> dev who wants their live checkout says so with `YOLO_REPO_ROOT` — a `repo_path`
> pointer only risked drift.

---

## 6. The image-cache fallback, and why a missing repo root is FATAL (D2 reverted 2026-07-29)

`AutoLoadImage` has a fallback when the Nix build returns `""` (e.g. a build
failure on a host that already has an image):

1. If the jail image already exists in the runtime (`image inspect` rc==0) →
   use it, no rebuild.
2. Else load the newest `*.tar` from `~/.local/share/yolo-jail/cache/images/`.
3. Else diagnose the Nix failure and return false.

**D2 (graceful degradation on a missing repo root) was reverted.** It used to
treat an unresolvable repo root as non-fatal: `run` discarded the `ok` flag,
printed a one-line notice, and called `autoLoadImage` with an empty `repoRoot`,
which `SkipBuild`-ed straight to the fallback above — so a source-less host ran
whatever image was already loaded or cached. That was judged a footgun: it
silently ran an image that **may not match the config**, hiding the fact that
the environment is stale, which is worse than failing. Now an unresolvable repo
root on a container backend is **fatal** (`run.go`, exit 1) with a message
pointing at the three fixes — launch from a checkout, set `YOLO_REPO_ROOT`, or
reinstall so the flake bundle ships beside the binary (§4, `just install` now
stages it). `macos-user` with empty `packages:` still needs no repo and is not
gated. `SkipBuild` stays a field on `AutoLoadOptions` as a dormant seam (its
fallback branch and the `autoload_test.go` regressions construct the options
directly), but the run path never sets it now.

---

## 7. Status and what remains

**Done:**

- **Single resolver** — `internal/reporoot.Resolve` is the one method for run +
  check, identical inside and outside the jail **and in every directory**.
  **Three steps** (env, exe-relative bundle, state-dir bundle); the `repo_path`
  fallback was retired 2026-07-23, the state-dir bundle added 2026-07-29, and the
  cwd walk removed 2026-08-31 (§2a). Resolution returns its `Source`, which every
  launch prints as `Flake source: …`.
- **Prebuilt bundle** — `flake.nix` + `flake.lock` + `bin/linux-{amd64,arm64}/`
  ships in Homebrew + the release archive and is baked into the image at
  `/opt/yolo-jail`; the flake's prebuilt short-circuit builds from it with no
  toolchain. `stageInstalledWheel` / `nix-build-root` staging and the
  `/opt/yolo-jail` source bind mount are removed. Regression tests:
  `internal/reporoot/reporoot_test.go` (`TestBundledSourceDirFrom`,
  `TestResolveIgnoresUserConfigRepoPath`).

**Remaining (Track D of the revival plan):**

- **PyPI wheel bundle** — the wheel (`tools/build-wheels`) still ships no bundle;
  a wheel-only install lacks a flake. Wire the same bundle in if PyPI stays a
  supported channel.
- **D4 — Cachix** — substituter block is live in `flake.nix` and the push job
  is wired; human-gated remainder is the first push from a Linux box + the
  Mac-side download proof (`docs/plans/handoff-cachix-cache.md`). Composes with
  the bundle (still needs the local flake to evaluate).

**Accepted regression:** the old dev-override fast loop (live-patching the outer
jail's binaries from a `just build-go` artifact via `/opt/yolo-jail/dist-go`) is
gone. The outer jail's binaries are frozen at the host-loaded image; iterate by
launching a **nested** `yolo -- bash`, which rebuilds the live `/workspace`
checkout from source. See `AGENTS.md` "Build & deploy."

Fallback seam: `git clone` at any point remains the universal escape hatch, and
`YOLO_REPO_ROOT=<checkout>` a one-line manual override if a bundle is ever
missing.

---

## Appendix — evidence provenance

Verified by reading the current source (and `c7e210d~1` for the Python era). Key
anchors:

- Launch ordering + fatal repo-root gate: `internal/cli/run/run.go` (`Run`,
  the `!repoRootOK` exit after the macos-user branch); regression:
  `internal/cli/run/reporoot_fatal_test.go`
- Nix build cwd + argv (in-place, no staging): `internal/image/autoload.go:314-322`
- Image built from Go source OR prebuilt: `flake.nix` `goBinaries` (prebuilt
  short-circuit `builtins.pathExists ./bin/linux-<arch>`), `installPrefix`,
  `corePackages`
- The single shared resolver (4 steps): `internal/reporoot/reporoot.go`
  (`Resolve`, `BundledSourceDirFrom`); run/check delegates in
  `internal/cli/run/probes.go`, `internal/cli/check/probes.go`
- `repo_path` retirement (2026-07-23): the key is tolerated-with-warning in
  `internal/config/validate.go` (`validateRepoPath`) but no longer resolved; the
  `internal/repopath` package + `yolo internal write-repo-path` subcommand were
  deleted, and `Justfile install` no longer writes it
- Bundle producer (prebuilt, both arches, goprobe excluded):
  `scripts/stage-source-bundle.sh`, `scripts/build-go.sh`, `Justfile`
  (`stage-bundle`)
- Packaging: `.goreleaser.yaml` (before-hook + archives `files:`),
  `.github/workflows/release.yml` (brew formula runs the bundle script)
- `bin/` never committed (invariant): `.gitignore` `/bin/`
- Python wheel bundling (history): `c7e210d~1:pyproject.toml`,
  `c7e210d~1:src/cli/run_cmd.py`
- Legacy `nix-build-root` cleanup (prune): `internal/prune/sweep.go`
- Cachix substituter live (cache not yet filled): `flake.nix` `nixConfig`,
  `publish.yml`, `docs/plans/handoff-cachix-cache.md`
- Cache fallback: `internal/image/autoload.go`
