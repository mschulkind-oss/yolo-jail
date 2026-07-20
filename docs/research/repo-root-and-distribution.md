# Why `yolo` Needs the Source Tree — and the Homebrew / uvx Distribution Story

**TL;DR** — `yolo` builds the jail's container image from the repo's own source
(`nix build .#ociImage`) on first run. So every `yolo -- <cmd>` invocation must
first locate a yolo-jail **source checkout**. An installed-only binary (Homebrew,
`go install`, PyPI wheel) ships no source, so unless it's launched from inside a
checkout it fails with:

```
Cannot find yolo-jail repo root.
The yolo CLI needs the repo for nix image builds.

Fix: add repo_path to ~/.config/yolo-jail/config.jsonc:
  { "repo_path": "~/code/yolo-jail" }
```

This is a **regression introduced by the Python→Go port**. The old Python wheel
bundled the source *inside itself*, so an installed `yolo` was self-contained for
repo-root resolution. The Go binary can't carry package data the same way, and no
replacement was wired up — so the "installed binary works from anywhere" property
was silently lost. This doc explains the mechanism, the Python-vs-Go difference,
every distribution channel, and what it would take to make Homebrew work
standalone.

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

## 2. How the repo root is resolved (and which steps are dead)

`resolveRepoRoot` (`internal/cli/run/probes.go:27`) tries five things in order:

| # | Step | Code | Works for an *installed-only* binary? |
|---|------|------|----------------------------------------|
| 1 | `YOLO_REPO_ROOT` env, if it contains `flake.nix` **or** `go.mod` | `probes.go:28-34` | No — only set inside jails / CI |
| 2 | Walk up from cwd for a dir with **both** `flake.nix` **and** `go.mod` | `probes.go:39-51` | Only if you happen to `cd` into a checkout |
| 3 | Bundled source next to the binary (`../share/yolo-jail`, then exe dir) | `probes.go:57-61, 94-112` | **Never — permanently dead** (see below) |
| 4 | `repo_path` from `~/.config/yolo-jail/config.jsonc` (if dir has `flake.nix`) | `probes.go:63-76` | **Yes — the only functional path** |
| 5 | Print the error and exit 1 | `probes.go:78-86` | — |

Notes on the guards, which are deliberate:

- **Step 1** accepts `flake.nix` **or** `go.mod` because a nested jail's
  `/opt/yolo-jail` bind can be empty — the OR guards against pointing at an empty
  mount.
- **Step 2** requires **both** files, on purpose: a bare `flake.nix` match would
  hijack *a user's own* flake workspace as the yolo-jail repo
  (`probes.go:37-38`).
- **Step 4** requires only `flake.nix` (not `go.mod`).

**Step 3 is the ghost of the Python wheel.** `bundledSourceDir` looks for a
`flake.nix` at `<exe>/../share/yolo-jail/` or beside the executable. In the
Python days the wheel put source where the code could find it; in Go, **no
distribution channel ever places a `flake.nix` + source tree next to the
binary** (proven per-channel in §4), so `bundledSourceDir` always returns
`("", false)`. The code is faithfully ported but structurally can never fire off
a real install.

The **`yolo check`** command has its own, shorter `resolveRepoRoot`
(`internal/cli/check/probes.go`) that only does steps 1–2 — no bundled-source,
no `repo_path`, no error message. So `check` and `run` can disagree about repo
discovery.

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

### What the Go port changed

- `c7e210d` (*"wipe(python)"*) deleted the Python `src/` tree, `pyproject.toml`,
  and the `src/flake.*` symlinks.
- A Go binary has no setuptools package-data mechanism. The port kept step 3's
  *code* (`bundledSourceDir` / `stageInstalledWheel`) but **nothing populates
  `share/yolo-jail/` next to the binary**, so it's inert.
- Nothing else replaced the bundling: `just deploy` doesn't record the checkout
  path, and no path is baked via ldflags (§5). So the "installed binary works
  anywhere" property was dropped on the floor.

---

## 4. Distribution channels — does each ship the source?

| Channel | How | Ships source for `nix build`? | Evidence |
|---|---|---|---|
| **GitHub Release tar.gz** (goreleaser) | one build `id: yolo` → `binary: yolo`; archive has no `files:`/`extra_files:` | **No** — binary (+ README/LICENSE globs) only | `.goreleaser.yaml:26-46` |
| **Homebrew tap** (`mschulkind-oss/homebrew-tap`) | external tap repo; `release.yml` clones it and copies a generated **source-build** formula (`depends_on go`, `go build ./cmd/yolo`) | **No** — installs only `bin/yolo`; never copies flake/src into `pkgshare` | `.github/workflows/release.yml:87-156` |
| **PyPI wheel** | `tools/build-wheels` embeds only the `cmd/yolo` binary + README/LICENSE/NOTICE + two tiny launcher `.py` files | **No** — no flake.nix, no source tree | `tools/build-wheels/main.go:66-68,205-229`; `publish.yml:48-74` |
| **From source** (`git clone` + `just deploy`) | `go install ./cmd/yolo` from inside the checkout | **Yes** — the checkout itself *is* the source | `README.md`, `Justfile:12-40` |
| **GHCR builder image** | a Nix *builder helper* image, not the jail image | N/A — helps macOS build offload, not source-less launch | `.github/workflows` |
| **Cachix binary cache** | prebuilt image closures for `nix` substitution | **Wired but disabled** (no-op today) | below |

Two things that look like escape hatches but aren't (yet):

- **Cachix is not enabled.** `flake.nix:4-20` has the `nixConfig` substituter
  block **commented out** ("NOT YET ENABLED — pending the Cachix account"), and
  `publish.yml`'s `push-image-cache` job **skips entirely** unless
  `CACHIX_AUTH_TOKEN` + `CACHIX_CACHE` are set (`publish.yml:80-102`). See
  `docs/implementation/handoff-cachix-cache.md`. **Crucially, even a fully-enabled
  Cachix cache would *not* remove the source requirement** — `nix build .#ociImage`
  against `.` must still *evaluate the local flake* to know which store paths to
  fetch. Cachix removes the *compile*, not the *flake read*.
- **No prebuilt *jail* image** is pushed to any OCI registry for `podman pull`.
  The only registry image is the separate builder helper.

**Bottom line:** every automated install channel except "from source" ships the
`yolo` binary alone. A Homebrew-only (or PyPI-only) user with no separate
checkout gets neither source nor a pullable jail image, and hits the repo-root
error on first run.

---

## 5. Why `just deploy` doesn't fix it either

`just deploy` → `just install` (`Justfile:40`, `deploy: install`):

- `install` stamps **only** `buildVersion` and `GitCommit` via ldflags
  (`Justfile:17`), runs `migrate-host`, then `go install ./cmd/yolo`. It **never
  writes `repo_path`** and **bakes no repo path** into the binary. (A repo-wide
  sweep confirms the *only* `-X` ldflags anywhere — `Justfile`, `build-go.sh`,
  `.goreleaser.yaml`, `tools/build-wheels`, the brew formula — are `buildVersion`
  and `GitCommit`.)
- `migrate-host` (`internal/hostmigrate`) retires the *old* Python install: it
  uninstalls the `yolo-jail` uv tool and clears stale GOBIN console scripts
  (`yolo`, `yolo-ps`, `yolo-host-processes`,
  `yolo-claude-oauth-broker-host`) — but only when positively identified as stale
  (venv symlink / broken symlink / python shebang); an unidentifiable `yolo`
  *blocks* the install rather than being deleted.
- `deploy` then retires legacy systemd token-refresher units, primes the
  claude-oauth-broker state, and restarts the broker.

So **after `just deploy` from a clean checkout, the installed binary still can't
find the repo from any *other* directory** — nothing persisted the path. It only
works from within the checkout (step 2's cwd walk) or once you set `repo_path`
(step 4). This is exactly the situation that produced the error: run `yolo` from
`~/.dotfiles` (or anywhere outside the checkout) and steps 1–3 all miss.

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

## 7. What to do

### Right now (the user's case)

You installed from source, so you have a checkout. Point `repo_path` at it — for
this environment the host path is `~/code/system/yolo-jail`:

```jsonc
// ~/.config/yolo-jail/config.jsonc
{ "repo_path": "~/code/system/yolo-jail" }
```

Then `yolo -- claude --continue` works from any directory. Note `repo_path` is
read from the **user** config only (workspace `repo_path` is explicitly ignored —
`internal/cli/check/check.go:356`).

### To actually make installed binaries work (design options)

None of these are implemented yet; listed roughly easiest → most complete:

1. **`just deploy` writes `repo_path`** into the user config, pointing at the
   checkout it built from. Fixes the from-source case (the one hit here). One
   caveat: it silently edits user config, so it should be idempotent and visible.
2. **Bake the source path via ldflags** (a `version.RepoRoot`-style var), read as
   a resolution step. Fixes from-source; still nothing for Homebrew.
3. **Ship `share/yolo-jail/` in the goreleaser archive + brew formula** so the
   already-present step 3 (`bundledSourceDir`) fires as designed. This is the
   Go analogue of the Python wheel bundling, and the only option that helps
   **Homebrew users who have no checkout at all.**
4. **Enable Cachix + let `run` fall back to a cached/registry image when
   resolution fails.** Removes the *compile* on first run, but a bundled flake is
   still needed for the `.#ociImage` *evaluation* — so this composes with (3),
   it doesn't replace it.

A regression test belongs in `internal/cli/run/probes_test.go`: assert that a
binary with a bundled `share/yolo-jail/flake.nix` resolves via step 3 (guards
whichever fix lands), and that the current no-bundle case still produces the
actionable error.

---

## Appendix — evidence provenance

Every claim above was verified by reading the current source and git history
(`c7e210d~1` for the Python era). Key anchors:

- Launch ordering: `internal/cli/run/run.go:30-32,167,225`
- Nix build cwd + argv: `internal/image/autoload.go:131,234-240`
- Image built from Go source: `flake.nix:65-107,573-661`
- Resolution order: `internal/cli/run/probes.go:27-112`
- Error text: `internal/cli/run/probes.go:78-86`
- Python wheel bundling: `c7e210d~1:pyproject.toml`, `c7e210d~1:src/cli/run_cmd.py:193-329`, `c7e210d~1:src/flake.nix` (symlink)
- Channels: `.goreleaser.yaml`, `.github/workflows/release.yml`+`publish.yml`, `tools/build-wheels/main.go`
- Cachix disabled: `flake.nix:4-20`, `publish.yml:80-102`, `docs/implementation/handoff-cachix-cache.md`
- Cache fallback: `internal/image/autoload.go:133-162,488-521`
- Install/ldflags: `Justfile:12-40`, `internal/hostmigrate/hostmigrate.go`
