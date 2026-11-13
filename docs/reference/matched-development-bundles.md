---
title: "Matched development bundles — a launcher and its helpers built as one artifact"
status: accepted
stage: CURRENT
verified: 2026-11-12
verified_commit: 741a04753
covers:
  - internal/reporoot/
  - internal/prune/devbundles.go
  - internal/selfupdate/channel.go
  - internal/cli/run/jailprefix.go
  - scripts/matched-bundle.py
  - scripts/dev-wrapper.sh
  - scripts/stage-source-bundle.sh
  - Justfile
tags: [development, builds, launch, bundles]
summary: "How yolo pairs a host launcher with the flake and helper binaries it runs: executable-relative bundle discovery, the build-only development bundle and its wrapper, the retired YOLO_REPO_ROOT selector, and the sweep that collects old development bundles."
---

# Matched development bundles — a launcher and its helpers built as one artifact

**Status:** Verified 2026-11-12 against `741a04753`. The development loop has run from dev bundles on
the owner's Mac (the Mac lab reports of 2026-11-06 and 2026-11-07). UNMEASURED: no recorded run on a
default macOS Podman Machine without a `/nix` share, the one cell
[the Mac-VM trap](#a-default-mac-podman-vm-is-proven-only-on-a-native-mac) says only a native Mac can
prove.

A **matched bundle** (a term this project coined) is one immutable build output that pairs a native
host `yolo` executable with the flake and every helper binary that launcher will start, all built
from one frozen source snapshot. It is the unit a launch runs: a launcher finds the bundle next to
itself, and launches never compile helpers. `just build-dev-bundle` makes one from a checkout,
dirty edits included, without touching the installed launcher, `PATH` or the installed bundle; the
checkout-resident development wrapper builds or reuses one and runs it.

| Component | Lives in |
| :--- | :--- |
| Bundle discovery and validation for a launch | `internal/reporoot` (`Resolve`, `Resolution`, `Source`) |
| Snapshot capture, build, publish, reuse and fallback | `scripts/matched-bundle.py` |
| The PATH-ready development wrapper | `scripts/dev-wrapper.sh` (`just dev` runs it) |
| Bundle packaging shared with `just install` | `scripts/stage-source-bundle.sh`, `scripts/build-go.sh` |
| The no-build refusal for container launches | `internal/cli/run` (`resolveJailPrefix`) |
| The no-build refusal and per-session staging for macos-user | `internal/cli` (`resolveGuestBinaries`), `internal/macosuser` (`GuestBinDir`) |
| Updater classification of a development build | `internal/selfupdate` (`KindDevelopment`) |
| Development-bundle collection | `internal/prune` (`PruneDevBundles`) |

**Reads with:** [`image-staging-vs-baking.md`](image-staging-vs-baking.md) (the mounted prefix a
container launch consumes), [`self-update.md`](self-update.md) (install channels and the installed
snapshot), [`installed-launcher-provenance.md`](../design/installed-launcher-provenance.md) (what an
installed launcher records about its source).

---

## Invariants

- **A launcher runs only its own bundle.** A complete bundle beside the running executable is
  authoritative for it, whatever produced it (a release archive, Homebrew, a mounted image prefix,
  a development build). The working directory never selects a flake.
- **An incomplete adjacent bundle refuses; it never falls back.** A recognized `share/yolo-jail`
  beside the launcher that is missing a flake file or a helper, or whose stamp disagrees with the
  launcher, is a refusal naming `just build-dev-bundle` and `just install`, never a silent switch to
  the installed bundle.
- **Only an installed launcher may use the installed bundle**, and only one whose embedded build
  kind is `installed` and whose snapshot identity matches the installed bundle's stamp. The active
  installed generation is resolved to one canonical directory once per launch and pinned, so a later
  `just install` cannot pair this launch's flake with another generation's helpers.
- **Launches never compile helper binaries.** A missing Linux helper set (containers) or Darwin
  guest set (macos-user) refuses with a next step naming the build-only command or `just install`.
- **A bundle is immutable once published**, and a running jail's mount source is never rewritten.
  Concurrent builds publish to distinct directories; a failed build publishes nothing usable.
- **`YOLO_REPO_ROOT` selects nothing.** A launcher that inherits it refuses, naming the
  replacement; the development wrapper strips it from its children and prints one notice that does
  not reveal its value.
- **Source discovery never moves the workspace.** The wrapper builds from the checkout its own
  symlink chain resolves to, and launches in the caller's working directory.

## The source snapshot and its identity

A build first freezes a bounded set of input files, then runs the copied build scripts and flake
operations from that frozen copy, never from the live checkout. The set is the flake's Go source
roots (`vendor/`, `cmd/`, `internal/`, `packs/`), the flake and module files, and the build entry
points, the wrapper and the bundle assembler included, so editing the wrapper changes the identity.
Within those roots it takes tracked files with their working-tree contents plus untracked,
non-ignored files; it excludes `.git`, ignored output and any symlink whose target leaves the
snapshot, and a required input that is such a symlink fails the build instead of reading outside.

The **snapshot identity** is a SHA-256 over the sorted paths and contents of that set. It is
stamped into the launcher and into the bundle's `yolo-build-stamp.json`, beside the build kind
(`development` or `installed`). A commit hash alone cannot tell two dirty builds of one commit
apart; the snapshot identity can, and discovery compares the two stamps.

## Bundle layout and platform contents

A bundle is `bin/yolo` beside `share/yolo-jail/`, which holds `flake.nix`, `flake.lock`, the build
stamp and the architecture-keyed helper directories. The launcher is found by path; discovery also
accepts the `share/yolo-jail` child of the launcher's own directory and the launcher's directory
itself when it holds bundle contents.

| Build host | Launcher | Helper sets in the bundle |
| :--- | :--- | :--- |
| Linux | native Linux `yolo` (the bundle's own `bin/linux-<arch>/yolo`) | `bin/linux-<arch>` |
| macOS | native Darwin `yolo`, compiled once | `bin/linux-<arch>` for containers, plus `bin/darwin-<arch>` guest set for macos-user |

The Linux `yolo` a container on a Mac runs is a different program from the Darwin launcher, built
from the same snapshot.

## The build-only command and the development wrapper

`just build-dev-bundle` is the build-only counterpart to `just install`: the same snapshot, stamp,
ship lists, cross-compilation, completeness checks and assembly, publishing a uniquely named,
read-only directory under the development-bundle root, then printing the launcher's absolute path.
It changes no `PATH`, installed launcher or installed bundle, and it fails on a failed build.

`scripts/dev-wrapper.sh` is the everyday entry. A user symlinks it under any name anywhere on
`PATH` (`yolo-dev`, or deliberately `yolo`); nothing generates it or edits `PATH`. On each run it:

1. Resolves its own relative, absolute or multi-hop symlink chain portably (no `readlink -f`) to
   find its real checkout.
2. Computes the current snapshot identity; reuses a complete bundle with that identity (touching
   it, which marks it recently used) or builds one.
3. `exec`s that bundle's launcher by absolute path with the caller's arguments, cwd, signals and
   exit status preserved. Because the target is an absolute path, a wrapper named `yolo` cannot
   select itself.

**When the build fails**, the wrapper runs the newest complete development bundle it already has
for this host's architecture, says plainly that the checkout's edits are not running and which
build is, and still reports the build failure. With no earlier bundle it fails with the build
error. It never falls back to the installed launcher ([OQ-MDB1](#why-its-this-way)).

Freshness is measured at capture time; there is no promise of an atomic snapshot across concurrent
edits to several files.

A dangling or cyclic `PATH` alias cannot start the script at all: the OS reports `ENOENT` or
`ELOOP` before it runs. The repair is to point the alias at the checkout script again, for example
`ln -sfn /absolute/path/to/checkout/scripts/dev-wrapper.sh /path/to/yolo-dev`, or
`just deploy-dev <name>` on the host when the alias is in `$GOBIN`.

## Backend delivery

- **Container backends** bind-mount the bundle's Linux helper directory and flake read-only, as the
  [mounted prefix](image-staging-vs-baking.md) describes. The Nix image build, image load and
  `packages:` delivery are unchanged; a helper bundle is not an image input.
- **macos-user** stages the bundle's Darwin guest set into a private child of the scoped guest-bin
  root, keyed by bundle identity and session token, sealed and published by atomic rename. A
  session's `PATH` names only its own child, which is removed only after that session exits, so two
  sessions in one workspace on bundles A and B never disturb each other. No Seatbelt root, grant,
  account home or workspace boundary widens.
- **macOS Podman Machine**: development bundles live under the user's home, which the default VM
  shares, so the loop needs no `/nix` share, VM recreation or installed-binary replacement.

## The updater and a development build

A development bundle's build kind is `development`, and the updater classifies it before archive
classification (`KindDevelopment`): no automatic network check, disclosure, notice or launch prompt.
Explicit `yolo update` commands behave as usual. A development build sets no install source stamp
(`SourceDir`, `SourceBranch`); omitting those alone would not suffice, because a complete adjacent
bundle otherwise looks like a release archive.

## Collecting old development bundles

Development bundles accumulate, one per distinct snapshot. `prune.PruneDevBundles` collects them,
run by the newly published launcher after each publish (`yolo internal dev-bundle-reap`) and by
`yolo prune`. It keeps the few most recently used (directory mtime, which a reuse refreshes), any
used within a grace window, and any a running jail mounts or a live process executes from; it also
clears abandoned `.building-*` publishes and interrupted deletions. A bundle is renamed to a
`.reap-` name before deletion, so a concurrent wrapper never picks a half-deleted one.

> [!WARNING]
> Collection needs a process table it trusts, which yolo reads only on Linux. On macOS (where a
> macos-user session runs as another account) the sweep removes nothing and names the by-hand
> removal instead; a reader that cannot ask declines rather than treating "unknown" as "unused".

## Traps

> [!WARNING]
> **Do not wire a builder back into a launch.** The no-build refusal is pinned at the production
> call sites: `TestLiveCheckoutBuildsThePrefix` drives `resolveJailPrefix`, and
> `TestResolveGuestBinariesRefusesWhenTheSourceShipsNone` and
> `TestResolveGuestBinariesRefusesACheckoutWithAStrayPartialDir` drive the macos-user guest set.
> They fail if a launch starts compiling helpers again.

> [!WARNING]
> **Do not reintroduce a selector variable.** Any environment variable that picks a checkout or
> bundle can pair a launcher with another tree's helpers. The explicit launcher path is the
> selection. `YOLO_REPO_ROOT`'s former roles (source selection, child-environment filtering, the
> update conflict diagnostic, CI and the integration harness) each moved to their own replacement;
> a green run proves the selection only when it runs the production launcher by path and reads the
> reported bundle provenance.

<a id="a-default-mac-podman-vm-is-proven-only-on-a-native-mac"></a>

> [!WARNING]
> **A default Mac Podman VM is proven only on a native Mac.** macOS Podman CI shares `/nix`, and a
> nested jail cannot see the host VM's share list. Only a native Mac run on a default Podman Machine
> without a `/nix` share shows that the development loop works there.

## Not in scope

- Not a replacement for the Nix image, image load, or `packages:` delivery.
- Not a way to mount or copy the live checkout into a jail: that would expose ignored and local
  files and let the source move independently of the mounted binaries.
- Not a change to installed operation: `yolo` from `PATH` keeps using its installed generation.

## Current values

Verified at `741a04753`.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Development-bundle root | `~/.local/share/yolo-jail/dev-bundles/` | `scripts/matched-bundle.py`, `prune.DevBundlesDirUnder` |
| Bundle directory name | `<first 16 hex of identity>-<32 hex uuid>` | `scripts/matched-bundle.py` |
| Bundles kept regardless of liveness | 3 most recently used | `prune.DevBundleKeep` |
| Recent-use grace | 1 hour | `prune.DevBundleGrace` |
| Abandoned publish floor | 1 hour | `devBundleStagingFloor` in `internal/prune` |
| Build stamp file | `share/yolo-jail/yolo-build-stamp.json` (`version`, `commit`, `snapshotIdentity`, `buildKind`) | `internal/reporoot` |
| Build kinds | `development`, `installed` | `internal/reporoot`, `internal/selfupdate` |
| Snapshot input files and roots | `INPUT_FILES`, `INPUT_DIRS` | `scripts/matched-bundle.py` |
| Retired selector | `YOLO_REPO_ROOT` (refused by a launcher, stripped by the wrapper) | `reporoot.Resolve`, `scripts/matched-bundle.py` |

## Why it's this way

| ID | Ruling | Why it holds |
| :--- | :--- | :--- |
| OQ-MDB1 | A failed development build falls back to the newest earlier development bundle, never to the installed launcher; with none it fails (owner, 2026-10-30) | The owner's words: *"a failed build falls back to the last dev launcher build ... if you don't have a build yet, it just fails, doesn't fall back to anything."* Falling back to the installed launcher would silently run unrelated source. Pinned by the `test_a_failed_build_*` cases in `scripts/test_matched_bundle.py`. |
