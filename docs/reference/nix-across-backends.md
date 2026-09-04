---
status: current
verified: 2026-09-04
verified_commit: ef5945e3
covers:
  - flake.nix
  - internal/darwinpkg/
  - internal/image/
  - internal/containerbuilder/
tags: [nix, packages, macos-user, podman, backends]
summary: "Both backends realize `packages:` with nix on the host, and that is where the similarity ends. The container backends bake them into an OCI image, which is a Linux artifact and needs a Linux builder; macos-user realizes them natively into a buildEnv profile and prepends its bin to the sandbox PATH. What follows from that split: when a rebuild happens, what a missing build costs, whether nix is reachable from inside, and why only one of them has a /lib farm."
---

# nix across the backends — one tool, two products

Every yolo backend gets its `packages:` from nix, run on the **host**, by the
invoking user. What differs is *what nix is asked to produce*, and almost every
other difference follows from that one.

- **Container backends** (`podman`, `container`): nix builds an **OCI image**, and
  the packages are baked into it. The artifact is a Linux filesystem.
- **macos-user**: nix builds a **buildEnv profile** — one store path whose `bin/`
  holds exactly the declared packages, natively for this Mac — and the launch
  prepends that `bin` to the sandbox's PATH. No image, no VM, no layer.

| Component | Lives in |
| :--- | :--- |
| Both flake outputs (`ociImage`, `packages.<system>.yoloNoncontainerPackages`, `yoloUnavailablePackages`) | `flake.nix` |
| Native realization, GC root, PATH/env derivation | `internal/darwinpkg` (`Materialize`, `BuildProfileArgv`, `ProfilePaths`, `ProfileRootLink`) |
| Image build, load, staleness, GC root | `internal/image` (`AutoLoadImage`, `RegisterImageRoot`) |
| The macOS Linux-builder offload | `internal/containerbuilder` |
| Platform filtering of the declared list | `internal/config` (`EffectivePackages`, `PackagesExcludedOn`) |

**Reads with:** [`noncontainer-nix-environment.md`](../design/noncontainer-nix-environment.md),
[`image-staging-vs-baking.md`](../design/image-staging-vs-baking.md),
[`macos-user-nix-and-features.md`](../design/macos-user-nix-and-features.md).

---

## The invariants

**The declared list is filtered by platform before nix sees it.** An entry may
carry `platforms`, and `EffectivePackages` takes the target platform and drops
non-matching entries *before* anything is realized. This is why the image path
asks for Linux and the macos-user path asks for darwin even on the same machine
with the same config — and why a package excluded on one platform never appears in
the other's skip list.

**Nix always runs on the host, as the invoking user.** Neither backend runs nix
inside the jail as part of provisioning. On macos-user this is load-bearing for
the threat model: the build step is the one part of that launch that runs
*unconfined*, which is why the config-change approval prompt gates it.

**A package that was declared and did not arrive is never silent.** The two
backends spend that rule differently — see [Missing builds](#missing-builds) —
but neither starts a jail while quietly withholding a tool the user asked for.

**The realized closure is GC-rooted.** Both paths register a root, so an ordinary
`nix store gc` cannot collect the thing a running jail is using. They root
different objects (an image store path; a profile out-link) and in different
places, but the guarantee is the same.

> [!WARNING]
> Do not add a second materialization path for a new notch. `yoloNoncontainerPackages`
> is named for the axis rather than for macos-user precisely because every notch
> *below* `jail` needs it — "below jail" means "no baked image", and the tool
> closure has to come from somewhere. A Linux `guest` notch is the next consumer
> and needs the identical mechanism.

## How the container path works

`packages:` reaches the flake through the `YOLO_EXTRA_PACKAGES` environment
variable, read with `builtins.getEnv` — which is why every image build is
`--impure`. The flake appends them to its core package set and produces an OCI
image; `AutoLoadImage` builds it, materializes the tar, and loads it into the
runtime.

**Rebuilds are keyed on the nix store path, not on the config.** If the store path
the flake evaluates to is unchanged, the loaded image is reused and the launch
says so; if it moved, the image is rebuilt and reloaded. Adding a package changes
the path, so the rebuild is automatic — and re-running with an unchanged config
costs an evaluation rather than a build.

**The /lib farm is container-only.** Each package's shared libraries are symlinked
into `/lib` and `/usr/lib` inside the image, so a `.so` is loadable by bare soname,
backed by an `LD_LIBRARY_PATH` baked into the image's environment so it survives an
agent that scrubs the environment. There is no equivalent on macos-user: the farm
is a property of a filesystem yolo composes, and that backend composes no
filesystem.

**On macOS, this path needs a Linux builder.** An OCI image is a Linux artifact,
so a Mac cannot build one natively. `AutoLoadImage` falls back to
`internal/containerbuilder`, which runs the build in a Linux container — the
zero-sudo offload that replaced an earlier VM builder.

**Nix is reachable from inside a container jail.** The launch bind-mounts the host
nix daemon socket and the store read-only and sets `NIX_REMOTE=daemon`, so nix
inside the jail delegates to the host daemon. Without it, in-jail nix fails with
"build users group has no members".

## How the macos-user path works

`darwinpkg.Materialize` runs `nix build --impure` on
`.#packages.<system>.yoloNoncontainerPackages` with the same `YOLO_EXTRA_PACKAGES`
contract. `<system>` is derived from the running `GOOS`/`GOARCH`, never hardcoded —
an Intel Mac resolves `x86_64-darwin` and the messages say so.

**The product is a buildEnv, deliberately not a devShell.** A devShell's
`print-dev-env` would dump an entire stdenv toolchain — clang, GNU coreutils, sed,
grep, make — onto the agent's PATH *ahead of* the host userland. A buildEnv
contains only the declared packages, which is the whole point: the sandbox gets
what the config asked for and nothing else.

**Delivery is a PATH prefix.** `ProfilePaths` derives the PATH entry from the
profile's `bin`, plus `PKG_CONFIG_PATH` when the profile has a `lib/pkgconfig`.
That prefix rides a separate channel into the launch environment and into the
login rc files the sandbox shell sources — which is why anything asking "is this
tool present?" inside that jail must consult the sandbox PATH and not the
container-shaped default.

**The GC root is the build's own out-link.** The build is run with `--out-link`,
so nix creates the root *as part of the build it is already running*. The image
path instead builds with `--no-link` and roots the printed path in a second step,
which leaves a window where a concurrent GC could collect a just-built closure.
The out-link form has no such window.

**There is no rebuild trigger, because there is nothing to reload.** Every launch
runs the build; nix answers from its own store when nothing changed. There is no
image to diff and no load step to skip.

## Missing builds

A declared package can have no build for the target system. The flake filters
these out rather than failing the evaluation — a hard error inside nix would abort
the whole eval, taking the packages that *do* build with it — and exposes the
filtered names through `yoloUnavailablePackages`. The CLI then decides.

The two backends decide differently, and the asymmetry is not an oversight:

- **Container**: the image is Linux, and the flake's own package set is chosen for
  Linux, so this is rare and the skip is reported.
- **macos-user**: a skipped package **aborts the launch**, naming every one at
  once. The likeliest cause is a typo — an unknown attribute name is
  indistinguishable from a package with no build for the platform — and the second
  likeliest is a genuinely Linux-only tool. Both leave the user with a jail missing
  something they asked for, discovered later as a command that does not exist.

The escape hatch is `platforms` on the entry. Marking a package Linux-only makes
its absence on darwin *expected* rather than an error, and it still installs in a
container. Because the filter runs before the build, such an entry never reaches
nix and cannot appear in the skip list at all — which is what lets everything
remaining in that list be treated as a real problem, with no second list of
tolerated absences to maintain.

## What each backend does not get

| | Container | macos-user |
| :--- | :--- | :--- |
| Baked image | yes | **no** — nothing is baked; a tool comes from `packages:` or the host userland |
| `/lib` farm + `LD_LIBRARY_PATH` | yes | **no** — no composed filesystem to farm into |
| Nix usable *inside* the jail | yes, via the daemon socket | **no** — the sandbox has no daemon socket mount |
| Rebuild/reload cost model | store-path diff, reload on change | none — build every launch, nix short-circuits |
| Needs a Linux builder on macOS | yes | **no** — native builds all the way down |

The most consequential row is the first. On the container backends the image is a
floor: `git`, `rg`, `fd`, node and the rest are present whether or not anyone
configured them. On macos-user there is no floor, so a tool the config does not
name is present only if the Mac's own `/usr/bin` has it. Anything that assumes the
image — a blocked-tool rule pointing at a replacement, a pack's `requires`, an MCP
wrapper with an absolute path — is making an assumption that holds on one backend
and not the other.

## Current values

Verified at `ef5945e3`. The prose above says what each of these is for; this table
is the only place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Declared-package env contract | `YOLO_EXTRA_PACKAGES` (compact JSON) | `darwinpkg.BuildEnv`, `flake.nix` |
| Native profile attr | `packages.<system>.yoloNoncontainerPackages` | `darwinpkg.ProfileAttr` |
| Skip-list attr | `yoloUnavailablePackages.<system>` | `darwinpkg.UnavailableAttr` |
| Native profile GC root | `<global storage>/build/package-roots/packages` | `darwinpkg.ProfileRootLink` |
| Image reload sentinel | `<build dir>/last-load-<runtime>` | `internal/image` |
| Flags on every nix call | `--extra-experimental-features "nix-command flakes"`, `--accept-flake-config` | `darwinpkg.nixFlags`, `internal/image/nixflags.go` |
| In-jail nix (container only) | `/nix/var/nix/daemon-socket` mount, `/nix/store:ro`, `NIX_REMOTE=daemon` | `internal/cli/run` |

## Why it's this way

| Ruling | Why a maintainer would otherwise undo it |
| :--- | :--- |
| buildEnv, not devShell | `print-dev-env` is the obvious way to "get a nix environment", and it puts a whole stdenv toolchain ahead of the host userland on the agent's PATH |
| `--out-link` roots the native build; the image path roots separately | Making them consistent by moving the native path to `--no-link` + `--add-root` would reintroduce the collect-before-root window |
| The flake filters unavailable packages instead of erroring | Erroring inside nix aborts the whole evaluation, losing every package that does build |
| The hard error for a missing darwin build lives in the CLI, after the eval | It looks like it belongs in the flake, next to the filter — but there it cannot distinguish "unavailable" from "the user marked it Linux-only" |
| `platforms` filters before materialize, not after | Filtering after would require a second list of absences that are expected, kept in sync with the first |
