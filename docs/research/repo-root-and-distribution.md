# Why `yolo` Needs a Flake — and How Installs Get One

> **Status: CURRENT (2026-07-23).** This doc describes how a checkout-less
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
ways `yolo` finds a flake now (`internal/reporoot.Resolve`):

1. **`YOLO_REPO_ROOT` env** — CI and the integration harness set it; validated
   to actually contain `flake.nix` **or** `go.mod`. Also the from-source escape
   hatch: point an installed `yolo` at a checkout from any directory.
2. **From a checkout** — the cwd walk finds a dir with **both** `flake.nix` and
   `go.mod` (host dev, the self-hosting `/workspace` jail, and a from-source
   install launched from inside its checkout).
3. **From a shipped/baked bundle** — an exe-relative `share/yolo-jail/` bundle
   (Homebrew, the release archive, and the in-image baked `/opt/yolo-jail`
   prefix all use this ONE method). **No checkout required.**

> **Retired (2026-07-23): the `repo_path` config key.** A fourth step used to
> read `repo_path` from `~/.config/yolo-jail/config.jsonc`, written by
> `just install`/`just deploy`. It was dropped: steps 1–3 already cover every
> case (a from-source developer resolves their LIVE checkout via step 2 or step
> 1, which is what a source install wants — a staged prebuilt bundle would build
> jails from stale artifacts, not their edits). The key is still *tolerated*
> (known key, so an existing config does not hard-error) but is ignored with a
> deprecation warning; `just install` no longer writes it, and the
> `internal/repopath` package + `yolo internal write-repo-path` subcommand were
> deleted.

Only if all three miss do you get the actionable error:

```
Cannot find yolo-jail repo root.
The yolo CLI needs the repo for nix image builds.

Fix: run yolo from inside a yolo-jail checkout, or point it at one with
YOLO_REPO_ROOT:
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

- `run.go:38` — `repoRoot, _ := o.RepoRoot()` is the first thing `Run()` does.
  Resolution is **no longer a hard gate** (D2): an empty `repoRoot` proceeds
  DEGRADED (no rebuild, run whatever image is loaded/cached) with a one-line
  notice, rather than exiting.
- `run.go:178` — `repoRoot` becomes the argument to `autoLoadImage`.

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

| # | Step | Works for an *installed-only* binary? |
|---|------|----------------------------------------|
| 1 | `YOLO_REPO_ROOT` env, if it contains `flake.nix` **or** `go.mod` | Yes — CI / integration harness, and the from-source escape hatch from any dir |
| 2 | Walk up from cwd for a dir with **both** `flake.nix` **and** `go.mod` | Only when `cd`'d into a checkout (the from-source dev path) |
| 3 | **Exe-relative bundle** (`BundledSourceDirFrom`) | **Yes — Homebrew / release archive / baked `/opt/yolo-jail`** |

_(A former step 4 read `repo_path` from the user config; retired 2026-07-23 — see the box in the intro.)_

Notes on the guards, which are deliberate:

- **Step 1** accepts `flake.nix` **or** `go.mod` — a lenient override for CI.
- **Step 2** requires **both** files, on purpose: a bare `flake.nix` match would
  hijack *a user's own* flake workspace as the yolo-jail repo.
- **Step 3** requires only `flake.nix` (the bundle has no `go.mod`).
- **Step 4** requires only `flake.nix`.

**Step 3 — the exe-relative bundle — is the checkout-less path.**
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
`bin/linux-<arch>`. (In a source checkout, step 2 resolves first, so step 3 is
a no-op there.)

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
| **From source** (`git clone` + `just deploy`) | `go install ./cmd/yolo` | **Yes** — the checkout is the flake; resolved via the cwd-walk (step 2) when launched from inside it, or `YOLO_REPO_ROOT` (step 1) from anywhere | `README.md`, `Justfile` |
| **In-image baked prefix** | `flake.nix installPrefix` bakes real-file binaries + the `share/yolo-jail` bundle at `/opt/yolo-jail` (not a mount) | **Yes** — the in-jail `yolo` resolves it via step 3, identical to a host install | `flake.nix` `installPrefix` / `corePackages` |
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
the in-image baked prefix all resolve a buildable flake — the first two and the
baked prefix via the prebuilt bundle, install-from-source via the cwd-walk /
`YOLO_REPO_ROOT`. The remaining gap is the **PyPI wheel** (no bundle wired) and
**Cachix** (substituter live, but the cache is not yet filled — first push + Mac
download proof pending).

---

## 5. The install-from-source path (no `repo_path`)

`just deploy` → `just install` (`deploy: install`):

- `install` stamps `buildVersion` + `GitCommit` via ldflags, runs
  `migrate-host`, then `go install ./cmd/yolo`.
- `migrate-host` (`internal/hostmigrate`) still retires the old Python install
  (uninstalls the `yolo-jail` uv tool, clears stale GOBIN console scripts) —
  only when positively identified as stale; an unidentifiable `yolo` *blocks*
  the install rather than being deleted.
- `deploy` then retires legacy systemd token-refresher units, primes the
  claude-oauth-broker state, and restarts the broker.

An installed-from-source `yolo` resolves the repo the same way anyone with a
checkout does: launch it from inside the checkout (the cwd-walk, step 2), or set
`YOLO_REPO_ROOT` to point at it from any directory (step 1). Both point nix at
the developer's LIVE source, which is what a from-source install wants.

> **Retired: `repo_path` + `write-repo-path` (2026-07-23).** `just install` used
> to run `yolo internal write-repo-path <checkout>`, which did an idempotent,
> comment-preserving JSONC edit to record `repo_path` in the user config (the
> `internal/repopath` package). That whole apparatus is gone — `install` no
> longer writes the key, the subcommand and package are deleted, and
> `reporoot.Resolve` no longer reads it. The key stays *tolerated* (a known key
> whose presence yields a deprecation warning, not a hard error) so an existing
> config keeps launching; `yolo check`/`yolo run` tell the user to remove it.
> Motivation: steps 1–3 already covered every channel, and a from-source dev
> wants their live checkout (which step 2/1 give) — a `repo_path` pointer only
> risked drift.

---

## 6. The image-cache fallback and graceful degradation (D2)

`AutoLoadImage` has a fallback when the Nix build returns `""`:

1. If the jail image already exists in the runtime (`image inspect` rc==0) →
   use it, no rebuild.
2. Else load the newest `*.tar` from `~/.local/share/yolo-jail/cache/images/`.
3. Else diagnose the Nix failure and return false.

Under **D2**, repo-root resolution is no longer a hard gate: when `Resolve`
returns `("", false)`, `run` proceeds DEGRADED — it prints a one-line notice and
calls `autoLoadImage` with an empty `repoRoot`, which `SkipBuild`s straight to
this fallback. So a source-less user with a previously-loaded or cached image
still launches; only a truly imageless, flake-less host fails, with the
actionable message. (`macos-user` with empty `packages:` needs no image at all.)

---

## 7. Status and what remains

**Done:**

- **Single resolver** — `internal/reporoot.Resolve` is the one method for run +
  check, identical inside and outside the jail. **Three steps** (env, cwd-walk,
  exe-relative bundle) since the `repo_path` fallback was retired 2026-07-23.
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

- Launch ordering + degraded path: `internal/cli/run/run.go:38,79-84,178`
- Nix build cwd + argv (in-place, no staging): `internal/image/autoload.go:314-322`
- Image built from Go source OR prebuilt: `flake.nix` `goBinaries` (prebuilt
  short-circuit `builtins.pathExists ./bin/linux-<arch>`), `installPrefix`,
  `corePackages`
- The single shared resolver (3 steps): `internal/reporoot/reporoot.go`
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
