# Why `yolo` Needs the Source Tree — and How Installs Get It

> **Status: FIXED (2026-07-20).** This doc originally described a regression from
> the Python→Go port. That regression is now resolved by **D1** (`just deploy`
> records the checkout path) and **D3** (a source bundle ships beside the
> binary). The sections below describe **how it works today**; the "Historical:
> the regression" callouts preserve why it was broken and how the Python wheel
> did it, since that context still explains the design. Plan:
> `docs/plans/macos-revival-and-distribution-plan.md` (Track D).

**TL;DR** — `yolo` builds the jail's container image from the repo's own source
(`nix build .#ociImage`) on first run, so every `yolo -- <cmd>` invocation must
first locate a yolo-jail **source tree**. Three ways it finds one now:

1. **From a checkout** — running anywhere inside a `git clone` (the cwd walk
   finds `flake.nix`+`go.mod`).
2. **From `repo_path`** — `just deploy` writes the checkout path into
   `~/.config/yolo-jail/config.jsonc`, so an installed-from-source `yolo` works
   from *any* directory.
3. **From a shipped bundle** — Homebrew and the GitHub release archive now carry
   a `share/yolo-jail/` source bundle beside the binary; `yolo` stages it into
   `~/.local/share/yolo-jail/nix-build-root` and builds from there. **No checkout
   required.**

Only if all three miss (an installed binary, no `repo_path`, no bundle) do you
get the actionable error:

```
Cannot find yolo-jail repo root.
The yolo CLI needs the repo for nix image builds.

Fix: add repo_path to ~/.config/yolo-jail/config.jsonc:
  { "repo_path": "~/code/yolo-jail" }
```

> **Historical: the regression.** The old Python wheel bundled the source
> *inside itself*, so an installed `yolo` was self-contained. The Go binary
> can't carry setuptools package-data, and for a while nothing replaced it — so
> the "installed binary works from anywhere" property was silently lost. D1+D3
> restored it (D3 is the direct Go analog of the wheel bundling).

---

## 1. Why does running a jail need the *source* at all?

Because **the jail image is not a prebuilt artifact — it is compiled from this
repo's source on first run.**

The launch path (`internal/cli/run/run.go`) resolves the repo root as its very
first probe and threads it through the whole launch:

- `run.go:30` — `repoRoot, ok := o.RepoRoot()` is the first thing `Run()` does.
- `run.go:31-32` — if resolution fails, `return 1` (a hard exit) *before* config
  load, runtime selection, or any container work.
- `run.go:167` — `repoRoot` becomes the argument to `autoLoadImage`.

`autoLoadImage` runs the Nix build with **`repoRoot` as the process working
directory**:

- `internal/image/autoload.go:234-240` — argv is
  `nix --extra-experimental-features "nix-command flakes" build .#ociImage --impure --out-link <link> --print-build-logs`,
  and `cmd.Dir = repoRoot`.
- `.#ociImage` is a **relative flake reference** — `.` means "the flake in the
  current directory." So Nix reads `flake.nix` out of `repoRoot`. No source tree
  there → nothing to build.

And `flake.nix` builds the image **by compiling the Go binaries from source**:

- `flake.nix:65-80` — `goSrc` is a fileset rooted at `./.` unioning `go.mod`,
  `go.sum`, `vendor/`, `cmd/`, `internal/`, `bundled_loopholes/`.
- `flake.nix:81-107` — `goBinaries` is a `stdenv.mkDerivation` whose buildPhase
  loops `for d in cmd/*/; do go build -trimpath -o "$out/bin/$name" "./$d"; done`
  (hermetic: `CGO_ENABLED=0`, `-mod=vendor`, no network).
- `flake.nix:573-661` — those compiled binaries flow through `corePackages` into
  `streamLayeredImage`'s `contents`, so `.#ociImage` **transitively depends on
  compiling the repo's Go source**.

Two secondary consumers of `repoRoot`, for completeness:

- **Version string** — `version.Get(repoRoot)` runs `git describe` in the repo
  for the banner (`run.go:225`).
- **In-jail repo** — `repoRoot` is bind-mounted read-only at `/opt/yolo-jail`, so
  the in-jail `yolo` has a repo and dev-override wrappers can prefer
  `/opt/yolo-jail/dist-go/` binaries over the baked ones.

> **`--impure` is load-bearing, not incidental.** It exists so `flake.nix` can
> read `YOLO_EXTRA_PACKAGES` via `builtins.getEnv` (`flake.nix:118-121`, the
> *only* `getEnv` in the flake), which the CLI sets from your `packages:` config
> (`autoload.go:229-232`). Under pure eval `getEnv` returns `""` and per-project
> packages silently no-op.

---

## 2. How the repo root is resolved

`resolveRepoRoot` (`internal/cli/run/probes.go:27`) tries five things in order:

| # | Step | Code | Works for an *installed-only* binary? |
|---|------|------|----------------------------------------|
| 1 | `YOLO_REPO_ROOT` env, if it contains `flake.nix` **or** `go.mod` | `probes.go:28-34` | Only inside jails / CI (where it's set) |
| 2 | Walk up from cwd for a dir with **both** `flake.nix` **and** `go.mod` | `probes.go:39-51` | Only when `cd`'d into a checkout |
| 3 | **Bundled source** next to the binary → stage into `nix-build-root` | `probes.go:57-61, 94-135` | **Yes — Homebrew / release archive (D3)** |
| 4 | `repo_path` from `~/.config/yolo-jail/config.jsonc` (if dir has `flake.nix`) | `probes.go:63-76` | **Yes — install-from-source, via `just deploy` (D1)** |
| 5 | Print the error and exit 1 | `probes.go:78-86` | — |

Notes on the guards, which are deliberate:

- **Step 1** accepts `flake.nix` **or** `go.mod` because a nested jail's
  `/opt/yolo-jail` bind can be empty — the OR guards against pointing at an empty
  mount.
- **Step 2** requires **both** files, on purpose: a bare `flake.nix` match would
  hijack *a user's own* flake workspace as the yolo-jail repo
  (`probes.go:37-38`).
- **Step 4** requires only `flake.nix` (not `go.mod`).

**Step 3 now fires (D3).** `bundledSourceDir` (`probes.go:94`, pure core
`bundledSourceDirFrom`) looks for a `flake.nix` bundle at three
executable-relative candidates:

- `<exe>/../share/yolo-jail` — the Homebrew Cellar layout (`bin/yolo`,
  `share/yolo-jail/…`).
- `<exe>/share/yolo-jail` — the release-archive layout (`yolo` and `share/` at
  one level).
- `<exe>` itself — a bundle unpacked directly beside the binary.

When one hits, `stageInstalledWheel` (§3) copies the bundle into
`~/.local/share/yolo-jail/nix-build-root` and returns that as the repo root.
§4 shows which channels ship the bundle. (In a source checkout or jail, step 2
resolves first, so step 3 is a no-op there.)

**`yolo check` now agrees with `run`.** check has its own `resolveRepoRoot`
(`internal/cli/check/probes.go`); D1 extended it to also honor `repo_path`
(step 4), and D3 added a **read-only** bundle probe (step 3 — it reports the
bundle dir but does *not* stage it, since staging has side effects and is
run-owned). So a checkout-less install with a bundle or `repo_path` passes
`yolo check` instead of wrongly reporting the repo missing.

---

## 3. Python (uvx / `uv tool install`) vs Go — the core difference

### How the Python wheel made an installed `yolo` self-contained

The Python distribution shipped the **entire source tree inside the wheel** as
package data:

- `pyproject.toml` (at `c7e210d~1`) declared
  `[tool.setuptools] packages = ["src", "src.bundled_loopholes", "src.cli", "src.entrypoint"]`
  and `[tool.setuptools.package-data] src = ["flake.nix", "flake.lock", "shims/*"]`.
- In the repo, `src/flake.nix` and `src/flake.lock` were **git symlinks** (mode
  120000) to `../flake.nix` / `../flake.lock`. When the wheel was built,
  setuptools **dereferenced** them into real files under `src/`, next to
  `cli/__init__.py`. (Verified against the built
  `dist/yolo_jail-0.6.1...whl`: `src/flake.nix` is a 44 KB regular file inside
  the wheel.)

At runtime, the Python `_resolve_repo_root` step 3
(`c7e210d~1:src/cli/run_cmd.py:232`) detected an installed package by checking
`pkg_dir = <…>/cli/__init__.py .parent.parent` (= the bundled `src/` dir) for a
`flake.nix`. If found, it **rehydrated the bundled tree onto disk** — copying
`flake.nix`/`flake.lock` and `copytree`-ing all of `src/` into
`~/.local/share/yolo-jail/nix-build-root` (via an inode-preserving rename swap)
— and returned that directory as the repo root.

Net effect: `uvx` / `uv tool install` carried the flake + source *inside the
install* and unpacked it on first run, so **repo-root resolution succeeded from
any directory** with no checkout and no `repo_path`. Step 4 (`repo_path`) was
only a fallback.

> ### Nuance worth recording (an adversarial-verify catch)
>
> By the very last Python commit (`c7e210d~1`), the flake had **already**
> switched to compiling the Go binaries from root-level `go.mod` / `cmd/` /
> `internal/` — which the wheel's package-data (`src/` + flake files only) did
> **not** ship. Empirically, `nix eval .#ociImage.drvPath` on a faithfully
> rehydrated wheel tree fails with *"go.mod is a path that does not exist"*,
> while the same eval on a full checkout succeeds.
>
> So at the transition point the wheel was self-contained for **repo-root
> *resolution*** but **not** for the **image *build***. The clean "uvx worked
> from anywhere" story is true for the pure-Python era; it was already eroding as
> the Go port landed. This doc's claims about "the Python wheel was
> self-contained" should always be read as *for resolution* — the build side
> depended on when you looked.

### What the Go port changed — and how D1+D3 restored it

- `c7e210d` (*"wipe(python)"*) deleted the Python `src/` tree, `pyproject.toml`,
  and the `src/flake.*` symlinks.
- A Go binary has no setuptools package-data mechanism. The port kept step 3's
  *code* (`bundledSourceDir` / `stageInstalledWheel`) but for a while **nothing
  populated `share/yolo-jail/` next to the binary**, so it was inert — the
  regression.
- **D3 (2026-07-20)** made step 3 live: a `share/yolo-jail/` bundle now ships in
  the Homebrew formula and the release archive (§4), and `stageInstalledWheel`
  was rewritten from the wheel's `build_root/src` layout to stage the Go bundle
  **flat** (§3). This is the direct Go analog of the wheel bundling.
- **D1 (2026-07-20)** covers the install-from-source case the bundle doesn't:
  `just deploy` records the checkout in `repo_path` (§5).

### How bundle staging works (`stageInstalledWheel`)

When step 3 finds a bundle, `stageInstalledWheel` (`probes.go:127`) copies it
into `~/.local/share/yolo-jail/nix-build-root` and returns that dir as the repo
root. Details that matter:

- **Flat layout.** The flake's `goSrc` fileset is rooted at `./.`, so the bundle
  (and thus `build_root`) must *be* the repo tree — `flake.nix`, `flake.lock`,
  `go.mod`, `go.sum`, `vendor/`, `cmd/`, `internal/`, `bundled_loopholes/` at the
  top level. D3 rewired staging from the wheel-era `build_root/src/` layout to a
  single flat `copyTree(bundle, build_root)`.
- **Idempotence marker.** A second launch with an unchanged bundle is a no-op:
  it checks `build_root/go.mod` + `build_root/flake.nix` exist and the staged
  `flake.nix` mtime is ≥ the bundle's, then returns `build_root` without
  recopying (the marker was the Python `src/cli/__init__.py`; now go.mod+flake).
- **FROZEN INVARIANT (unchanged by D3).** Staging **never** `rmtree`s the old
  `build_root` — a jail launched from a previous copy may still hold that inode
  bind-mounted read-only at `/opt/yolo-jail`, and deleting it out from under the
  live mount serves a `//deleted` inode. Instead it renames the old tree aside to
  a unique `nix-build-root.old.<hex>`, leaves it for the liveness-gated `prune`
  sweeper, and mtime-stamps it (the sweeper's age floor). It also never
  pre-creates `build_root`. D3 preserved all of this verbatim — an adversarial
  review confirmed the invariant is intact.

Once staged, the `nix build .#ociImage` runs with `cwd = build_root`, and that
same `build_root` is bind-mounted read-only into the jail at `/opt/yolo-jail`.

---

## 4. Distribution channels — does each ship the source?

| Channel | How | Ships source for `nix build`? | Evidence |
|---|---|---|---|
| **Homebrew tap** (`mschulkind-oss/homebrew-tap`) | external tap; `release.yml` generates a **source-build** formula (`depends_on go`, `go build ./cmd/yolo`) that also **`pkgshare`-installs the goSrc fileset** | **Yes (D3)** — `flake.nix`/`flake.lock`/`go.mod`/`go.sum` + `vendor/`,`cmd/`,`internal/`,`bundled_loopholes/` at `prefix/share/yolo-jail` → `<exe>/../share/yolo-jail` | `.github/workflows/release.yml:107-129` |
| **GitHub Release tar.gz** (goreleaser) | `before` hook stages the bundle via `scripts/stage-source-bundle.sh`; archive `files:` ships it beside the binary | **Yes (D3)** — `yolo` + `share/yolo-jail/…` → `<exe>/share/yolo-jail` | `.goreleaser.yaml` before-hook + archives `files:` |
| **From source** (`git clone` + `just deploy`) | `go install ./cmd/yolo`; `just deploy` also writes `repo_path` | **Yes** — the checkout is the source; `repo_path` records it | `README.md`, `Justfile:12-51` |
| **PyPI wheel** | `tools/build-wheels` embeds only the `cmd/yolo` binary + metadata | **No** — no bundle wired (D3 did brew+goreleaser; wheel not yet) | `tools/build-wheels/main.go:66-68,205-229` |
| **GHCR builder image** | a Nix *builder helper* image, not the jail image | N/A — helps macOS build offload, not source-less launch | `.github/workflows` |
| **Cachix binary cache** | prebuilt image closures for `nix` substitution | **Wired but disabled** (no-op today) | below |

The bundle producer is `scripts/stage-source-bundle.sh` (`just stage-bundle`):
`git archive HEAD` of the tracked tree (~11 MB, a clean superset of the goSrc
fileset), asserting the required members are present. The staged tree is proven
to evaluate: `nix eval .#ociImage.drvPath` succeeds on it.

Two remaining notes:

- **Cachix is still not enabled** (D4, human-gated). `flake.nix:4-20` has the
  `nixConfig` substituter block **commented out**, and `publish.yml`'s
  `push-image-cache` job **skips** unless `CACHIX_AUTH_TOKEN` + `CACHIX_CACHE`
  are set (`publish.yml:80-102`). See `docs/plans/handoff-cachix-cache.md`.
  **Even a fully-enabled Cachix cache would *not* remove the source requirement**
  — `nix build .#ociImage` against `.` must still *evaluate the local flake* to
  know which store paths to fetch. Cachix removes the *compile*, not the *flake
  read* — so it composes with D3's bundle, it doesn't replace it.
- **No prebuilt *jail* image** is pushed to any OCI registry for `podman pull`.
  The only registry image is the separate builder helper.

**Bottom line (today):** Homebrew, the release archive, and install-from-source
all resolve the repo — the first two via the shipped bundle, the third via
`repo_path`. The remaining gap is the **PyPI wheel** (no bundle wired) and
**Cachix** (account not created).

---

## 5. `just deploy` records `repo_path` (D1)

`just deploy` → `just install` (`Justfile:40`, `deploy: install`):

- `install` stamps `buildVersion` + `GitCommit` via ldflags, runs
  `migrate-host`, then `go install ./cmd/yolo`, and — **new in D1** — runs
  `yolo internal write-repo-path <checkout>` to record the checkout path in the
  user config (`Justfile:52-56`).
- **`yolo internal write-repo-path`** (`internal/repopath`) does an idempotent,
  **comment-preserving** JSONC edit: it sets `repo_path` in
  `~/.config/yolo-jail/config.jsonc` only when absent or changed, prints what it
  did (`Created`/`Updated`/`already set`), and refuses a dir with no `flake.nix`.
- `migrate-host` (`internal/hostmigrate`) still retires the old Python install
  (uninstalls the `yolo-jail` uv tool, clears stale GOBIN console scripts) — only
  when positively identified as stale; an unidentifiable `yolo` *blocks* the
  install rather than being deleted.
- `deploy` then retires legacy systemd token-refresher units, primes the
  claude-oauth-broker state, and restarts the broker.

So **after `just deploy`, an installed-from-source `yolo` resolves the repo from
any directory** via `repo_path` (step 4) — the exact scenario (`yolo` run from
`~/.dotfiles`) that first surfaced this. `repo_path` is read from the **user**
config only; a workspace `repo_path` is ignored, and `yolo check` warns if you
put one there (`internal/cli/check/check.go:356`).

---

## 6. The image-cache fallback — real, but unreachable here

`AutoLoadImage` *does* have a fallback when the Nix build returns `""`
(`autoload.go:133-162`):

1. If the jail image already exists in the runtime (`image inspect` rc==0) →
   use it, no rebuild.
2. Else load the newest `*.tar` from `~/.local/share/yolo-jail/cache/images/`.
   `newestTars` accepts **any** `*.tar` by mtime — so a prebuilt image tar
   dropped in out-of-band *would* be picked up (it does not require the
   sha256-named filename the success path writes).
3. Else diagnose the Nix failure and return false.

**But this fallback is unreachable for a source-less user**, because of the
ordering: `resolveRepoRoot` runs as the *first* probe and hard-exits 1
(`run.go:30-32`) **long before** `autoLoadImage` is ever called
(`run.go:167`). The cache fallback only helps when a repo root *resolves* but the
Nix build itself fails — not when there's no source tree at all.

This is a plausible fix seam (see below): if resolution failed but a usable
cached image or runtime image exists, `run` *could* proceed. Today it doesn't.

---

## 7. Status and what remains

**Done (2026-07-20):**

- **D1** — `just deploy` records `repo_path`; `yolo check` honors it. Fixes every
  install-from-source (`feat(install): just deploy records repo_path; check
  honors it too`).
- **D3** — `share/yolo-jail/` source bundle ships in Homebrew + the release
  archive; `stageInstalledWheel` stages it flat; `check` gained a read-only
  bundle probe. Fixes checkout-less Homebrew/release installs (`feat(dist): ship
  a Go source bundle so checkout-less installs build the image`). Regression
  tests: `internal/cli/run/probes_test.go` (`TestBundledSourceDirFrom`,
  `TestStageInstalledWheelStagesFlat`, `TestStageInstalledWheelIdempotent`) and
  `internal/cli/check/probes.go` parity test.

**Remaining (Track D of the revival plan):**

- **PyPI wheel bundle** — D3 wired the bundle into brew + goreleaser but not the
  wheel (`tools/build-wheels`); a wheel-only install still lacks source. Wire the
  same bundle in if PyPI stays a supported channel.
- **D2 — graceful degradation** — defer the repo-root hard-exit for the
  macos-user runtime with empty `packages:` (it needs no image), and let the
  container path fall back to a cached/runtime image when resolution fails (§6).
- **D4 — Cachix** — human-gated: create the account, uncomment `flake.nix:17-20`.
  Removes the compile; composes with D3 (still needs the local flake to evaluate).

Fallback seam: `git clone` at any point remains the universal escape hatch, and
`repo_path` a one-line manual fix if a bundle is ever missing.

---

## Appendix — evidence provenance

Verified by reading the current source and git history (`c7e210d~1` for the
Python era). Key anchors:

- Launch ordering: `internal/cli/run/run.go:30-32,167,225`
- Nix build cwd + argv: `internal/image/autoload.go:131,234-240`
- Image built from Go source: `flake.nix:65-107,573-661`
- Resolution order (5 steps, all live): `internal/cli/run/probes.go:27-135`
- Bundle staging (flat, frozen invariant): `internal/cli/run/probes.go:94-190`
- check-side resolver (repo_path + read-only bundle probe): `internal/cli/check/probes.go`
- `write-repo-path` / repo_path writer: `internal/repopath`, `internal/cli/internal.go`
- Bundle producer: `scripts/stage-source-bundle.sh`, `Justfile` (`stage-bundle`)
- Packaging: `.goreleaser.yaml` (before-hook + archives `files:`), `.github/workflows/release.yml` (brew `pkgshare`)
- Python wheel bundling (history): `c7e210d~1:pyproject.toml`, `c7e210d~1:src/cli/run_cmd.py:193-329`
- Cachix disabled: `flake.nix:4-20`, `publish.yml:80-102`, `docs/plans/handoff-cachix-cache.md`
- Cache fallback: `internal/image/autoload.go:133-162,488-521`
- Install/ldflags: `Justfile:12-40`, `internal/hostmigrate/hostmigrate.go`
