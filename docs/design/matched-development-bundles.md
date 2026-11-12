---
title: "Build a matched launcher bundle without changing the install"
status: accepted
stage: BUILT
next: "Graduate the shipped contract into a reference doc and delete this doc."
tags: [development, builds, launch]
summary: "A build-only development artifact pairs the host launcher with every helper and flake it will run."
---

# Build a matched launcher bundle without changing the install

**Status:** 2026-10-30. Built, including [OQ-MDB1](#OQ-MDB1)'s fallback to the last development build (see its row in [§6](#6-owner-rulings)).

> **In short.** A launcher and the jail helpers it starts are one build artifact. Developers need a build-only path that produces that pair from one source snapshot and can be invoked directly, without changing the installed launcher, `PATH`, or the source selected by any other launcher.

**Why it matters.** A checkout without prebuilt helpers can take a launch path that invokes Nix to compile them. On a Mac Podman Machine that cannot see the selected `/nix/store` path, this source-visible fallback can end in an unreachable-prefix refusal unless the store is shared. That is the observed code limitation; it does not establish the cause of any particular reported command or failure.

**The shape.** A shared build-and-package pipeline emits immutable, executable-relative bundles; launchers discover only their neighboring bundle, and launches never compile helper binaries.

**Cost.** `YOLO_REPO_ROOT` ceases to select launch sources. CI, update checks, configuration guidance, and the integration harness must use the matched artifact or installed bundle instead.

**Start at [§2](#2-the-matched-artifact-contract)** — every later behavior follows from it.

**Needs your ruling:** None. The owner ruled [OQ-MDB1](#OQ-MDB1) on 2026-10-30; it is recorded with the earlier rulings below.

**Reads with:** [§9](#9-build-invariants) (what the retired build plan pinned beyond this design), [`image-staging-vs-baking.md`](../reference/image-staging-vs-baking.md) (the existing image and mounted-prefix contract).

---

## 1. Current behavior and the gap

[`reporoot.go`](../../internal/reporoot/reporoot.go) currently resolves `YOLO_REPO_ROOT` first, then a bundle beside the running executable, then the installed state-directory bundle. It deliberately ignores the working directory. A live checkout usually has no prebuilt helper directories.

[`jailprefix.go`](../../internal/cli/run/jailprefix.go) mounts prebuilt Linux helpers when the selected flake has them; otherwise it calls [`BuildJailPrefix`](../../internal/image/prefix.go), which builds `.#installPrefix` in the Nix store during launch. On macOS Podman, [`prefixUnreachableFromVM`](../../internal/cli/run/jailprefix.go) refuses store-backed mounts unless `/nix` was explicitly shared. This is independent of the normal Nix image build and load.

The macos-user backend has the analogous fallback: [`resolveGuestBinaries`](../../internal/cli/commands.go) calls `.#guestPrefix` when the selected flake lacks its Darwin helper set. The successful path stages executable files into the backend's scoped guest-bin directory with sealed permissions; it does not install them into the account or workspace.

[`just install`](../../Justfile) currently installs the host launcher first, stages Linux helpers (and Darwin guest helpers on macOS) into a fresh flake generation, then activates that generation. [`stage-source-bundle.sh`](../../scripts/stage-source-bundle.sh) packages `flake.nix`, `flake.lock`, a build stamp and the selected helper binaries—not a source checkout. This machinery already supplies the distribution bundle format, but independently reading a mutable checkout in separate build steps does not guarantee the host launcher and helper set came from one source snapshot.

## 2. The matched-artifact contract

A **matched bundle** is a self-contained development output containing a native host `yolo` executable and the flake plus all helper binaries that this host's supported jail backends can invoke. The build stamps and every byte-producing step come from one frozen snapshot of the intended source files. It may be built from a dirty checkout: the frozen snapshot captures tracked edits and intended non-ignored new source, but excludes `.git`, ignored output, credentials, unrelated host files, and symlinks whose targets escape the snapshot.

The snapshot is described by a bounded input manifest, not a per-file hash inventory: the flake's Go source inputs (`go.mod`, optional `go.sum`, `vendor/`, `cmd/`, `internal/`, and `packs/`), `flake.nix`, `flake.lock`, and the build/package entry points and their required inputs (`Justfile`, `scripts/build-go.sh`, and `scripts/stage-source-bundle.sh`). For each declared source root, include tracked files with their worktree contents and intended untracked, non-ignored source files; do not recursively copy every file beneath a directory merely because it is in the flake fileset. Exclude ignored files and external symlink targets; if a required input is an excluded symlink, fail before building rather than read outside the snapshot. Freeze this bounded file set first, then execute the copied build scripts and flake operations from that snapshot, never from the live checkout. Compute one snapshot identity from the sorted paths and contents and embed it in the launcher and bundle metadata; commit/version strings alone cannot distinguish dirty builds from the same commit.

The bundle is a distinct, immutable output for each build. It is not an activation of the installed bundle, a mutation of a running jail's mount source, or a symlink to the live checkout. A failed build publishes no usable launcher. Concurrent builds reserve different output generations; neither overwrites a bundle another process may be running. Unused development outputs are collected by `prune.PruneDevBundles` ([`devbundles.go`](../../internal/prune/devbundles.go)), which the newly published launcher runs after each publish and `yolo prune` runs too: it keeps the most recently used few (by directory mtime, which a reuse refreshes), anything used within the last hour, and anything a running jail mounts or a live process runs from.

### Platform contents

| Build host and backend | Host launcher | Target helpers in the bundle |
| :--- | :--- | :--- |
| Linux | Native Linux `yolo` | `bin/linux-<host-arch>` shipped helper set |
| macOS with container backend | Native Darwin `yolo` | `bin/linux-<host-arch>` container helper set |
| macOS with macos-user enabled | Native Darwin `yolo` | Linux helper set plus `bin/darwin-<host-arch>` guest set |
| Release/distribution build | Existing distribution launcher layout | Existing declared OS/architecture sets; no local-only set or second ship list |

The executable and `share/yolo-jail` use the existing executable-relative discovery shape: `bin/yolo` beside `share/yolo-jail/`, with the flake and architecture-keyed helpers inside the latter. A developer runs that exact launcher by path. No PATH change, active-bundle swap, configuration knob, or checkout-path override selects the artifact.

### Launch selection and failure

A complete executable-relative bundle is authoritative for its launcher, regardless of whether it came from Homebrew, a release archive, a mounted image prefix, a system package, a sealed layout, or a development build. When a recognized adjacent bundle directory exists but is incomplete, refuse; do not hide the broken pair by falling back to installed state. A binary-only launcher with no adjacent bundle may use installed state only when it is an installed launcher carrying an embedded installation identity: resolve the active generation to one canonical absolute directory once, compare its bundle identity with the launcher's embedded identity, and use that pinned directory for the flake and every helper. A mismatch or unmarked binary-only layout refuses with the build/install remedy. Thus a later activation cannot switch generations between helper and flake resolution, and same-commit dirty builds remain distinguishable by their snapshot identity.

`YOLO_REPO_ROOT` is retired as a launcher source selector, not given compatibility precedence. Every production, test, CI and diagnostic consumer must move to the right replacement: invoke a matched bundle by its executable path for development; use the complete installed/distribution bundle for normal launches; and pass source directories only to build/package operations that explicitly compile from source. Inherited `YOLO_REPO_ROOT` must not silently pair one launcher with another tree's helpers. A refusal should name the obsolete variable and offer the build-only command or `just install`, whichever fixes the context.

Development artifacts also carry an explicit development classification in build metadata, distinct from an installed/release channel. The updater must recognize it before archive classification and suppress automatic network checks, disclosure/notices, and update prompts for that artifact; explicit update commands retain their existing behavior. Omitting install source stamps alone is insufficient because a complete adjacent bundle otherwise looks like an archive.

### Backend delivery

- **Container backends:** mount the selected prebuilt helper directory and flake bundle read-only, following the current mounted-prefix contract. Preserve the existing image realization, image load, and `packages:` paths. A helper bundle is not an image derivation and does not remove the ordinary Nix image build.
- **macos-user:** select the bundled Darwin guest set and stage it into a new immutable guest-bin child under the existing scoped staging root, keyed by bundle identity plus a unique launch/session token. Each running session's staged PATH names only its own child; never replace or remove those executables while that session runs. Build the child privately, seal its files and directory, then publish it with fresh-inode atomic rename. Remove it only after its session exits. Keep the existing Seatbelt rules, root, host grants, account home and workspace boundary unchanged; do not add a new writable location or broaden access. A launch of bundle B in the same workspace must not change bundle A's guest executables or PATH.
- **Default macOS Podman Machine:** place the development output in a user-home location the default VM shares, not `/nix/store`, the checkout, or a VM-only path. The user needs no `/nix` share, machine recreation, or installed-binary replacement for the development loop. The Nix image build/load and image-identity mechanisms remain unchanged.

## 3. Build and use sequence

`just build-dev-bundle` is the build-only counterpart to `just install`: it shares the same source snapshot, stamp calculation, binary ship sets, cross-compilation, completeness checks and bundle assembly, then prints the generated launcher's absolute path without installing it or activating a state-directory bundle. The developer may invoke that exact artifact directly:

```bash
just build-dev-bundle
/path/printed/by/the/build/bin/yolo -- bash
```

The checkout-resident wrapper is the everyday PATH entry, without requiring that artifact to be an installed `yolo`:

```bash
# After symlinking scripts/dev-wrapper.sh under a chosen PATH name:
yolo-dev -- bash
just dev -- bash
```

A separate explicit development wrapper lives in the checkout as an executable script. The user places a symlink to it under any chosen name and anywhere on `PATH`, including `yolo-dev` or deliberately `yolo`; no wrapper generator, installed `yolo`, or automatic PATH edit is required. The script follows relative, absolute and multi-hop symlinks to discover its real checkout, independent of the current working directory. It uses that resolved checkout as the build source while preserving the caller's original cwd as the launch workspace; source discovery must never change which workspace the jail runs in.

At each invocation, the wrapper computes the identity of the current bounded intended-source snapshot. If a complete bundle with that identity exists, it reuses it; otherwise it builds one, then `exec`s that bundle's launcher by absolute path with the caller's arguments. Freshness is measured at source capture; there is no promise of an instantaneous snapshot across concurrent edits to multiple files. When the build fails, the wrapper runs the newest complete development bundle it already has for this host and says the checkout's edits are not running and which build is; with none, it fails with the build error ([OQ-MDB1](#OQ-MDB1)). `just build-dev-bundle` still fails on a failed build: it produces a bundle, it does not run one. Concurrent wrappers serialize publication or reserve distinct immutable outputs, so neither can overwrite a bundle that may be running.

The wrapper never treats `YOLO_REPO_ROOT` as source selection and does not alter the caller's shell or global config. When the variable is present, it removes the selector only from its metadata/build/exec child environments and prints one concise notice that the wrapper ignores the export and it may be removed; it does not disclose the value or host path, and it is silent when the variable is absent. Normal installed and directly invoked bundle launchers retain the actionable refusal for the obsolete selector. The wrapper never starts an installed or stale launcher to build helper halves, invokes no second Go compiler, and performs no recursive host-source reads. When the running wrapper encounters a broken/cyclic link during self-resolution, it reports the failure and repair step where it can. A PATH alias whose chain is already dangling or cyclic cannot start the script at all: the OS reports the pre-execution `ENOENT`/`ELOOP`, so no custom wrapper message is promised. Repair it by replacing the alias with a link to the checkout script, for example `ln -sfn /absolute/path/to/checkout/scripts/dev-wrapper.sh /path/to/yolo-dev` (substitute the chosen checkout and PATH alias). Symlink resolution must work on Linux and macOS without assuming `readlink -f`.

The ordinary workflow remains: an installed launcher starts the outer jail; an agent edits the live checkout and either directly builds/invokes its matched bundle or invokes the checkout-resident wrapper. The wrapper always execs the exact matched launcher by absolute path, so even when the symlink is named `yolo` it cannot recursively select itself. The caller's cwd remains the workspace. `yolo -- bash` against the installed launcher continues to use the installed generation.

Build/package publication happens only after every required binary and flake file is present and validated. For `just install`, prepare the complete matching generation before making it active and install/activate the corresponding launcher-bundle pair with a verifiable identity. Resolve the active installed symlink once to its canonical generation and pin that root for the full launch; no helper or flake consumer resolves the mutable active link again. A launch that races publication must see one complete old pair or one complete new pair, or refuse clearly; it must never mutate bytes mounted by an existing jail. This pairing check also makes a partially failed install detectable on the next invocation.

## 4. Alternatives and costs

| Choice | Verdict |
| :--- | :--- |
| Keep compiling `.#installPrefix` or `.#guestPrefix` during launch | Rejected. It leaves launch behavior dependent on a Go/Nix build and preserves the inaccessible-store failure on default macOS Podman. |
| Add a wrapper that builds a Nix helper derivation | Rejected. It duplicates the Go/package path and keeps compilation in launch orchestration. `scripts/build-go.sh` remains the helper compiler. |
| Add a dev environment variable selecting a checkout or bundle | Rejected. An override can select a flake whose helper binaries do not match the running launcher. The explicit launcher path is the selection. |
| Copy the checkout into a jail or use it as the mounted flake | Rejected. It exposes ignored/local files and allows the live source to move independently of mounted binaries. |
| Replace the image/load or package delivery flow | Out of scope. The helper bundle is separate from the Nix image and `packages:` contracts. |

The cost is a breaking change for scripts that rely on `YOLO_REPO_ROOT` selecting runtime source, and a locally built development artifact occupies user-state storage until the development-bundle sweep collects it. In exchange, installed operation remains unchanged and a development invocation states its exact launcher path.

## 5. Acceptance

The design is built when all of the following hold:

1. One build-only command emits a complete, self-contained launcher bundle from clean or dirty source without changing PATH, the installed launcher, or the active installed bundle.
2. The native launcher and every Linux/Darwin helper set it may start are built from the same immutable source snapshot and stamp; the artifact's executable-relative discovery selects that bundle ahead of any installed generation. `yolo` itself is compiled once, for the host platform: on Linux the launcher is the bundle's own `bin/linux-<arch>/yolo`, and on macOS, whose bundle carries only the guest helpers for darwin, it is the one darwin build ([OQ-IP2](installed-launcher-provenance.md#OQ-IP2)). The Linux `yolo` a container on a Mac runs is a different program from that launcher by nature, built from the same snapshot.
3. A missing helper never starts a launch-time Go/Nix helper build. It refuses with a next step naming the build-only command or install path.
4. The complete artifact works when invoked by path, including nested container launches on a default Mac Podman VM without `/nix` sharing or VM reconfiguration. Container helper mounts remain read-only; macos-user delivery remains scoped and sealed.
5. Two macos-user launches using bundles A and B in the same workspace retain distinct, immutable guest-bin paths for their full lifetimes; ending either session does not alter or remove the other's staged executables, and no Seatbelt root, grant, account, home or workspace boundary expands.
6. A later build cannot change an output a running jail has mounted. Failed and concurrent builds cannot expose a partial or cross-paired generation.
7. A checkout-resident executable script discovers its checkout by resolving its complete symlink chain; users may symlink it to PATH under any name, with no generator, installed launcher, or automatic PATH edit. It uses the real checkout as build source but preserves the caller's cwd as workspace. Linux/macOS resolution handles relative, absolute and multi-hop links portably without `readlink -f`; if named `yolo`, it execs the absolute matched launcher to avoid recursion. `just dev` delegates to the same script/build core. Each invocation reuses only a complete bundle matching the bounded source-capture identity or builds one, then execs with arguments, signals and exit status preserved. A failed build runs the newest complete earlier development bundle with a notice naming it, or fails when there is none ([OQ-MDB1](#OQ-MDB1)). The wrapper child-scrubs obsolete `YOLO_REPO_ROOT`, only notifying when present without revealing its value; a normal direct launcher still refuses it. A dangling/cyclic PATH alias is rejected by the OS before the wrapper can run; tests assert nonzero with no build/launch, while resolver failures observed after start get custom repair guidance where possible. Document the explicit `ln -sfn /absolute/path/to/checkout/scripts/dev-wrapper.sh /path/to/yolo-dev` repair form.
8. `just install`, release packaging, image realization/load, and `packages:` delivery still provide their existing supported behavior using the shared package machinery. The standalone `installPrefix` package remains available for mounted-prefix packaging; removing `guestPrefix` changes the content hash input `imageIdentity` derives from `flake.nix`, but does not change the identity mechanism or image/load path.

## 6. Owner rulings

| Ruling | Recorded contract |
| :--- | :--- |
| Remove launch-time helper compilation | Container and macos-user launch paths consume prebuilt target helpers; missing helpers refuse instead of invoking a helper build. |
| Add a build-only counterpart to install | It emits a matched native launcher, target helpers and flake bundle from one source snapshot, including dirty development edits. |
| Keep installed use and development use separate | The normal installed launcher and PATH remain unchanged; developers invoke the generated launcher explicitly. |
| Add a checkout-resident PATH-ready development wrapper | Its executable resolves its real path through symlinks, uses that checkout as build source, preserves the caller's cwd for launch, and execs the absolute matched launcher. Users symlink it under any name; `just dev` delegates to the same script. |
| Make default Mac Podman viable | Deliver the artifact in the VM-shared home path; do not require `/nix` sharing, VM setup, or an installed replacement. |
| Preserve trust and runtime boundaries | No broader credential/home grants, workspace mutation, writable helper injection, alternate Go implementation, or image/load/package redesign. Keep the standalone mounted-prefix `installPrefix` package; `imageIdentity` continues to hash `flake.nix` and `flake.lock`, so its value naturally follows a flake edit without changing that mechanism. |
| <a id="OQ-MDB1"></a>[`OQ-MDB1`](#OQ-MDB1) (2026-10-30): a failed development build falls back to the last development build | The owner's words: *"a failed build falls back to the last dev launcher build. don't we save them somewhere and GC them? if you don't have a build yet, it just fails, doesn't fall back to anything."* When building the checkout's bundle fails, the development wrapper runs the newest complete development bundle under `~/.local/share/yolo-jail/dev-bundles/` (the bundles `prune.PruneDevBundles` keeps), says plainly that the checkout's edits are not running and which build is, and still reports the build failure. With no development bundle at all it fails with the build error. It never falls back to the installed launcher. Built `c2ae2c268`; pinned by `test_a_failed_build_runs_the_newest_earlier_bundle_and_names_it`, `test_a_failed_build_skips_a_broken_newer_bundle_and_leaves_its_age`, `test_a_failed_build_with_no_earlier_bundle_fails_naming_the_fix`, `test_a_failed_build_never_runs_a_bundle_for_another_architecture`, `test_build_dev_fails_on_a_failed_build_even_with_an_earlier_bundle` and `test_broken_and_cyclic_aliases_fail_without_build_or_launch` in [`test_matched_bundle.py`](../../scripts/test_matched_bundle.py). |

## 7. Terms

**Matched bundle** *(coined here)* — one immutable build output pairing the host launcher with the helper binaries and flake bundle that launcher selects. Not merely binaries with equal Git commit strings: dirty builds and concurrent edits require one frozen source snapshot.

## 8. Open questions

None open.

## 9. Build invariants

The build was handed off through a companion plan, now retired; git history keeps its file map, task gates and report lists. These are the rules it pinned that the sections above do not already state.

- **The wrapper is in its own snapshot.** The bounded input manifest of [§2](#2-the-matched-artifact-contract) also names `scripts/dev-wrapper.sh` and `scripts/matched-bundle.py`, so a change to the wrapper or the bundle assembler changes the snapshot identity.
- **Install stamps stay install-only.** A development build carries an explicit development classification (`KindDevelopment`, read before `KindArchive`) and sets no install `SourceDir` or `SourceBranch` stamp; `scripts/test_matched_bundle.py` asserts the latter. Tests of updater suppression go through the caller that decides automatic checks, notices and prompts, not only the classifier.
- **`YOLO_REPO_ROOT` is migrated by role, not renamed.** Its former uses are source selection, child-environment filtering, the update conflict diagnostic, the CI and integration harness, and prose. Each moves to its own replacement, and no alias may silently override an adjacent bundle. Because CI and the harness once set it, a green run proves the new selection only when it runs the production launcher by its path and reads the reported bundle provenance.
- **The no-build refusal is pinned at the production call site.** `TestLiveCheckoutBuildsThePrefix` drives `resolveJailPrefix` and asserts the refusal names the build-only command; `TestResolveGuestBinariesRefusesWhenTheSourceShipsNone` and `TestResolveGuestBinariesRefusesACheckoutWithAStrayPartialDir` do the same for the macos-user guest set. A test of a callee alone does not count: these fail if a builder is wired back into the launch.
- **The default Mac VM is proven only on a native Mac.** macOS Podman CI shares `/nix`, and a nested jail cannot see the host VM's share list, so neither proves acceptance item 4. Only a native Mac run on a default Podman Machine without a `/nix` share does; Linux, Apple Container and the shared-`/nix` nightly do not substitute for it.
