# macOS Setup Guide

**Status:** REFERENCE — user-facing setup guide. **Backend-parity sweep 2026-08-24:**
[Limitations](#limitations) now carries a disabled-feature table for **each** of the two
macOS-native backends, so the choice between them can be made by reading rather than by
launching. Those two tables cite `file → function` rather than `file:line`, on purpose:
the previous revision's `assemble_parts.go:83` had drifted ~30 lines, and every row in a
table that outlives a refactor will drift the same way. **Spot-verified 2026-08-23:**
the three backends and their auto-detection order (`internal/cli/run/preflight.go:90-133`
— macOS tries `container` then `podman`; `macos-user` is opt-in only,
`internal/paths/paths.go:27`); the `macos-*` command family
(`internal/cli/dispatch.go:31-34`); the container-builder offload with no `yolo
builder` command (`internal/containerbuilder/`, `internal/image/builderoffload.go:23`);
and the broker-relay deletion (`internal/brokerrelay` is gone; the front is a
goroutine at `internal/cli/run/loopholesruntime.go:892`). **Three commands this
guide told you to run do not exist** — corrected inline below. **Not verified:**
any macOS-hardware behaviour (vmnet NAT, Podman Machine, Determinate daemon
hang, Apple Container bind-mount limits) — nobody ran a Mac for this audit; the
Nix/Homebrew instructions; the ASCII architecture diagrams.

YOLO Jail supports macOS (Apple Silicon and Intel) in addition to Linux, with
two flavors of backend:

- **Linux container** (`podman`, `container`) — Podman Machine or Apple
  Container transparently runs a lightweight Linux VM, so the jail experience is
  nearly identical to a native Linux host.
- **Native, no-VM** (`macos-user`) — the agent runs directly on macOS as a
  hidden service user (`_yolojail`) confined by Apple Seatbelt, with `packages:`
  materialized via native `aarch64-darwin` nix. No VM, no Linux image. Verified
  end-to-end on real Apple Silicon (macOS 26.5, 2026-07-21).

**On Apple Silicon the container path is native arm64 — there is no emulation.**
The image is built as `aarch64-linux` (the flake maps `aarch64-darwin →
aarch64-linux`) and the runtime VM is `linux/arm64`, so it's arm-on-arm — no
qemu, no Rosetta. The only time you hit emulation is pulling an **amd64-only
image** (e.g. some database images); that's a property of that image, not of the
backend.

> `macos-user` was prototyped, briefly excised, then **revived** as a composed
> product (native macos-user + Apple Container fallback) and is now verified on
> hardware. See
> [macos-no-vm-direction.md](../reference/macos-no-vm-direction.md) for the standing
> decision and
> [macos-revival-and-distribution-plan.md](../plans/macos-revival-and-distribution-plan.md)
> for the current status.

## Choosing a runtime

| Runtime | What it is | Choose it for |
|---------|------------|---------------|
| **Podman** | Linux container in a Podman Machine VM | The portable default; Podman-in-Podman; **full feature parity with Linux hosts** — nothing in [Limitations](#limitations) is skipped for backend reasons |
| **Apple Container** | Linux container, one lightweight VM per container | Per-container CPU/memory limits, native socket forwarding (macOS 15+). Drops every loophole but one — the OpenAI credential broker starts, and the jail cannot reach it; context mounts and read-only protection arrive from `container` 1.1.0 and are declined below it — see [what it does not do](#apple-container-runtime-container--what-it-does-not-do) |
| **macos-user** | Native macOS user + Seatbelt, **no VM, no image** | Fastest startup; no container runtime to install; `packages:` via native darwin nix. Weaker isolation than a VM (Seatbelt, no cgroups) — see [Trade-offs](#macos-user-trade-offs) and [what it does not do](#macos-user-native-no-vm--what-it-does-not-do) |

The container runtimes are native arm64 on Apple Silicon. Set the runtime with
`YOLO_RUNTIME=podman`, `container`, or `macos-user` (or the `runtime` key in
`yolo-jail.jsonc`).

**If you are choosing between the two macOS-native backends**, the side-by-side
list is [Backend feature parity at a glance](#backend-feature-parity-at-a-glance).

Auto-detection priority:
- **macOS:** Apple Container → Podman (native-first). `macos-user` is
  **opt-in** — select it explicitly; it is not auto-detected.
- **Linux:** Podman

### macos-user trade-offs

`macos-user` swaps VM isolation for native speed. What you gain: no runtime to
install, no Linux image to build, instant startup, and `packages:` built
directly as `aarch64-darwin` nix. What you give up:

- **Weaker isolation** — Seatbelt (`sandbox-exec`) confinement, not a VM. No
  cgroups, so no resource limits.
- **Neutral-ground workspaces only** — the sandbox user can share a project
  under a non-home root like `/Users/Shared/yolo/<name>`, never a path inside
  your home. yolo refuses a home-dir workspace.
- **One-time setup** — `yolo macos-setup` creates the hidden `_yolojail` user
  (self-escalates; do **not** run under `sudo`). `yolo macos-teardown` reverses
  it. See [The macos-user backend](#the-macos-user-backend) below.

## Prerequisites

**Always required:**

| Tool | Install | Notes |
|------|---------|-------|
| **[Nix](https://nixos.org/download/)** | [Determinate Nix Installer](https://github.com/DeterminateSystems/nix-installer) recommended | Flakes must be enabled. Builds the jail image (container runtimes) or the native `aarch64-darwin` `packages:` (macos-user). Your user must be a **trusted** nix user — `yolo check` flags it if not. |

`yolo` is the only binary you install (`go install ./cmd/yolo`, `brew`, or a
release archive); everything else it provisions itself.

**Plus a runtime — pick ONE** (see [Choosing a runtime](#choosing-a-runtime)):

| Runtime | Install | Notes |
|---------|---------|-------|
| **[Podman](https://podman.io/)** | `brew install podman` | The portable default; requires Podman Machine (setup below) |
| **[Apple Container](https://github.com/apple/container)** | `brew install container` | Native per-container VM; macOS 15+ |
| **macos-user** | *(nothing to install)* | Native, no VM. Needs only Nix + `yolo macos-setup` (see [The macos-user backend](#the-macos-user-backend)) |

### Podman Machine Setup

Podman on macOS runs containers inside a Linux VM managed by `podman machine`.
Initialise it once:

```bash
# Create the VM (adjust resources to taste)
podman machine init --cpus 4 --memory 8192 --disk-size 50

# Start the VM
podman machine start
```

The machine persists across reboots. Use `podman machine stop` / `podman machine start`
to manage it.

### Apple Container (native macOS runtime)

[Apple Container](https://github.com/apple/container) uses Apple's
Virtualization.framework directly — each container runs in its own lightweight
VM with native resource limits (`--cpus`, `--memory`, integers only) and native
Unix socket forwarding (`--publish-socket`). ⚠ yolo's use of that last one is
broken today, and both port keys are worse than they look here — see
[what Apple Container does not do](#apple-container-runtime-container--what-it-does-not-do) below.

```bash
brew install container

# Start the container system daemon
container system start

# Verify it's working
container system info

# Install the recommended Linux kernel (required on first use)
container system kernel set --recommended
```

**Key advantages:**
- Native per-container CPU/memory limits (no cgroup delegation needed)
- Native Unix socket forwarding (no TCP gateway workaround)
- Smallest footprint — no separate VM daemon

**Key limitations:**
- A hard cap on directory-sharing devices (bind mounts) per container, which is why
  yolo consolidates the jail's home into a single `/home/agent` mount
- No security capabilities (`--cap-add`, `--security-opt`)
- Early-stage project — fewer features than Podman
- Several `yolo-jail.jsonc` keys do not reach this backend — see
  [Apple Container: what it does not do](#apple-container-runtime-container--what-it-does-not-do)
  for the full list, including the one that affects Claude logins

**Image conversion:** Apple Container requires OCI-format images. YOLO Jail
copies the Nix image straight into a temporary OCI archive, loads it, and removes
the archive — one full-size write instead of the two the old converter chain
needed. **Nothing has to be installed for this**: the `skopeo` that does it is
built by the flake (`nix build .#imageCopier`), because it carries a Nix-store
source transport that no released skopeo has. A `brew install skopeo` is neither
used nor needed, and the `podman`-as-fallback route is gone.

### The macos-user backend

`macos-user` runs the agent **natively on macOS** — no VM, no Linux image. The
agent executes as a hidden service user (`_yolojail`) confined by Apple Seatbelt
(`sandbox-exec`); `packages:` from your config are built as native
`aarch64-darwin` nix. It needs no container runtime — just Nix (with your user
trusted) and a one-time account setup.

**One-time setup** (run as your normal admin user — it self-escalates per
privileged step; do **NOT** prefix with `sudo`):

```bash
yolo macos-setup      # creates the hidden _yolojail user + shared root ACL
```

This provisions the neutral shared root at `/Users/Shared/yolo`. Put projects
you want to run under it (`/Users/Shared/yolo/<name>`) — the sandbox user can
only share neutral ground, never a path inside your home.

**Run:**

```bash
cd /Users/Shared/yolo/my-project
YOLO_RUNTIME=macos-user yolo -- claude       # or set runtime: "macos-user" in yolo-jail.jsonc
```

`sudo` prompts once per run to enter the sandbox — that's expected; yolo does
not change your sudo policy.

**Teardown** (fully reverses setup; idempotent):

```bash
yolo macos-teardown                          # removes the _yolojail user + home
yolo macos-unshare /Users/Shared/yolo/my-project   # strip the shared ACL from a workspace
```

**Preflight without changing anything:** `yolo check` reports the macos-user
readiness (Seatbelt, sandbox user, nix trusted), and
`YOLO_RUNTIME=macos-user yolo --dry-run` prints the full plan (Seatbelt profile,
bootstrap argv, launch argv) and runs its invariant checks — both zero-sudo.

See [Choosing a runtime](#macos-user-trade-offs) for when to pick it, and the
runbook [mac-macos-user-e2e.md](../plans/runbooks/mac-macos-user-e2e.md) for the
full verification procedure.

### Building the image on macOS (cache vs. Linux builder)

The OCI image is a **Linux** image (`aarch64-linux`). Most of its content
(chromium, bash, python, node, …) is standard nixpkgs, fetched from
`cache.nixos.org` — but a few derivations are built from **this repo's own
source** (`yolo-jail-conf`, the entrypoint pkg, and the patched `skopeo` that
delivers the image) and are therefore **never** on the public cache. macOS can't build a Linux
derivation locally, so those few must be built on Linux somehow.

Two things make that a non-event:

**Best — download the prebuilt image (no build at all).** When yolo-jail's
Cachix cache is published, macOS users download the fully-built image and
never compile anything. This is the intended happy path; see
[docs/plans/handoff-cachix-cache.md](../plans/handoff-cachix-cache.md) for its status. Once
live, `yolo check` shows "every image path is served from the binary cache".
CI pushes the **aarch64-linux** closure on every release (built natively on
an arm runner), so Apple Silicon Macs pull the exact arm image they run — no
cross-build, no builder needed.

**Otherwise — automatic offload to a container builder.** If a package must be
built from source (before the cache is published, or because you added a custom
package that isn't cached), a normal `yolo` run handles it **automatically**: it
starts a tiny nix+sshd Linux builder **container** on whichever runtime is
already up (podman or Apple Container), offloads the build to it over `ssh-ng`,
then tears it down. No VM to set up, no `sudo`, no `yolo builder` command, no
first boot, and zero idle RAM — the builder exists only for the duration of the
build. The one prerequisite is that your container runtime is running; `yolo
check` tells you exactly when a from-source build is required (naming the
offending derivation) and reminds you to start the runtime.

> **Important:** Do NOT set `extra-platforms = aarch64-linux` in your Nix
> config. This tells Nix to execute Linux binaries locally, which fails on
> macOS. You don't need it — the automatic container-builder offload handles
> any from-source Linux build for you.

**Your user must be trusted by the Nix daemon** (so it may offload builds to the
builder container). Check, set, and restart:

```bash
# Is a custom.conf include present? (Determinate adds it; official NixOS
# installer does not — on that one, edit nix.conf directly.)
grep -qF 'include /etc/nix/nix.custom.conf' /etc/nix/nix.conf \
  && echo 'trusted-users = root '"$(whoami)" | sudo tee -a /etc/nix/nix.custom.conf \
  || echo 'trusted-users = root '"$(whoami)" | sudo tee -a /etc/nix/nix.conf

# Restart the daemon (label depends on installer):
sudo launchctl kickstart -k system/systems.determinate.nix-daemon  # Determinate
# or: sudo launchctl kickstart -k system/org.nixos.nix-daemon       # official NixOS
```

> **Escape hatch (advanced):** if you already run your OWN Linux builder — a
> **nix-darwin** `linux-builder` (`nix.linux-builder.enable = true;`), or a
> Linux box registered in `/etc/nix/machines` (see the
> [Nix manual on distributed builds](https://nix.dev/manual/nix/latest/advanced-topics/distributed-builds)) —
> that keeps working untouched. Nix uses your configured builder and yolo never
> starts its own container; `yolo check` shows "Linux builder configured". This
> is your own nix configuration, orthogonal to yolo — the container-builder
> offload above is what covers everyone who hasn't set one up.

### Known Issue: Determinate Nix Daemon Hang

Some versions of `determinate-nixd` (notably v3.x) may hang on store
operations for non-root users. If `nix store info` hangs indefinitely:

```bash
# Kill the determinate daemon and start the vanilla nix-daemon
sudo pkill determinate-nixd
sudo /nix/var/nix/profiles/default/bin/nix-daemon &
```

This starts the standard Nix daemon which does not have the hang bug.

### Nested Nix builds inside the jail (advanced)

By default, YOLO Jail mounts the host's `/nix/store` and Nix daemon socket
into the container so `NIX_REMOTE=daemon` "just works" for nested Nix builds
inside the jail. On macOS, the runtime VM (Podman Machine, Apple container)
typically does **not** share `/nix` from the host, so the bind mount would
fail with a `statfs` error at startup. YOLO Jail therefore skips this mount
on macOS by default.

Opting back in takes **two** claims on macOS, because they are two different
facts and only the first one is about the VM:

```bash
export YOLO_NIX_HOST_DAEMON=1      # my runtime VM shares /nix
export YOLO_NIX_HOST_STORE_LINUX=1 # ...and my store holds the jail's Linux closure
yolo
```

With both set, YOLO Jail bind-mounts `/nix/var/nix/daemon-socket` and
`/nix/store:ro` into the jail and exports `NIX_REMOTE=daemon`, exactly as on
Linux. With only the first, it mounts neither and says so in one line.

> [!WARNING]
> **The second claim is not a formality, and getting it wrong bricks the jail.**
> The image's `/bin` is symlinks *into* the store — `flake.nix` writes
> `ln -s ${imagePkgs.bashInteractive}/bin/bash $out/bin/bash`, and the same for
> `sh`, `awk`, `sed`, `grep` and `find` — so `/nix/store` is where every baked
> binary actually lives. Bind-mounting the host's store on top **replaces that
> view**, and a Mac's store holds darwin paths: the jail is Linux. Every one of
> those symlinks then dangles and pid1 dies as
> `yolo-entrypoint: exec: "bash": executable file not found in $PATH`.
>
> Measured on the 2026-09-13 macOS nightly (run 34778464086), where one variable
> still meant both things and took all eight shards with it.
>
> Set `YOLO_NIX_HOST_STORE_LINUX` only if your Mac's store really does hold the
> jail's Linux closure — a `linux-builder` VM realized it there, or a substituter
> served it. yolo cannot check this for you: the image it runs may have arrived as
> a tar with no store path to compare against.

### The same rule now decides whether a live checkout can launch at all

Since 2026-09-06 yolo's own binaries are **bind-mounted** into the jail rather
than baked into the image ([`image-staging-vs-baking.md`](../reference/image-staging-vs-baking.md#the-mounted-prefix)). An
installed bundle — Homebrew, the release archive, `just install` — ships them
prebuilt under `$HOME`, which the VM does share, so nothing changes for it. A
**live checkout** ships none, so they are built, and a built prefix lives in
`/nix/store` — the one tree the VM does not share. Podman reports that as

```text
Error: statfs /nix/store/…-yolo-jail-install-prefix/opt/yolo-jail/bin: no such file or directory
```

and exits 125 before pid1 runs. Measured on the 2026-09-07 macOS nightly (run
34117863296), where it took down ~30 tests.

yolo now **refuses that launch** with both fixes named, rather than letting
podman report it as an unattributable `statfs`. To run from a live checkout on
macOS, share `/nix` with the VM and say so:

```bash
podman machine init --cpus 4 --memory 8192 -v /nix:/nix   # -v is init-only
podman machine start
export YOLO_NIX_HOST_DAEMON=1
YOLO_REPO_ROOT=~/code/yolo-jail yolo
```

`YOLO_NIX_HOST_DAEMON` is deliberately the same variable as above, and it is the
**only** one this recipe needs: it means "my runtime VM shares `/nix`", which is
what makes a `/nix/store` bind *source* resolve. Do **not** add
`YOLO_NIX_HOST_STORE_LINUX` to get a live checkout running — that is the separate
claim about your store's contents described above, and asserting it falsely is
what hides the image's own store. Otherwise unset `YOLO_REPO_ROOT` and launch
from the installed bundle, which needs no machine changes.

The two used to be one variable, on the reasoning that "the VM shares `/nix`"
decided both. It does not: reachability is about whether a path resolves, while
delegation replaces the tree the jail's own `/bin` points into. Splitting them is
what the 2026-09-13 nightly bought.

## Installation

Two options. Homebrew is easiest; the source install is for hacking on the CLI
or running an unreleased working tree. (`go install
github.com/mschulkind-oss/yolo-jail/cmd/yolo@latest` also works identically on
macOS.)

> [!NOTE]
> **`yolo` is the only binary you install on the host.** `just install` runs
> `go install ./cmd/yolo` and nothing else — `yolo-entrypoint`, `yolo-jaild`,
> `yolo-ps`, `yolo-cglimit` and `yolo-journalctl` are image-side only and never
> reach a macOS host.

### Option A — Homebrew (recommended for users)

```bash
brew tap mschulkind-oss/tap
brew install mschulkind-oss/tap/yolo-jail
```

The formula is generated on every tag by the release workflow and builds `yolo`
from the tagged source. No source checkout, no `just`, auto-updates via `brew
upgrade`. Works on Apple Silicon and Intel.

### Option B — Install from source

```bash
git clone https://github.com/mschulkind-oss/yolo-jail.git
cd yolo-jail
just deploy          # builds + installs the yolo CLI

# (Optional) Set user-level defaults
yolo init-user-config
```

> [!WARNING]
> **There is no `yolo build` command** (verified 2026-08-23 against the command
> registry, `internal/cli/dispatch.go:15-35`). This guide used to tell you to run
> it here; it would exit "unknown command". The image is built **automatically**
> by the first `yolo` run (`AutoLoadImage` nix-builds it and loads it into the
> runtime). To build it by hand, call nix directly:
> `nix build .#ociImage --no-link --print-out-paths`.

## Usage

Usage is identical to Linux:

```bash
cd /path/to/your/project
yolo run
```

Set the runtime explicitly if needed:

```bash
export YOLO_RUNTIME=podman   # or container
yolo run
```

## What Works on macOS

Everything that works on Linux works on macOS **except** the items listed in
[Limitations](#limitations) below. This includes:

- ✅ Full jail isolation (read-only root, no host credentials)
- ✅ Workspace mounting at `/workspace`
- ✅ Podman-in-Podman (nested containers via Podman Machine)
- ✅ MCP server presets (Chrome DevTools, Sequential Thinking, etc.)
- ✅ LSP servers (Pyright, TypeScript)
- ✅ Port forwarding and publishing (via TCP gateway on Podman, native sockets on Apple Container)
- ✅ `mise` tool management inside the jail
- ✅ Agent launchers for all six shipped agent packs — `claude`, `copilot`,
  `codex`, `opencode`, `pi`, `agy` (**not** Gemini CLI: the `gemini` agent was
  removed; `~/.gemini/antigravity-cli/` is now agy's tree)
- ✅ Container reuse across sessions
- ✅ Custom Nix packages in the image
- ✅ `yolo check` diagnostics (with macOS-aware checks)
- ✅ `yolo ps` and `yolo prune`
- ✅ Network modes (`bridge`, `host`) on **Podman** — those two are the whole vocabulary the
  key accepts; on Apple Container only `bridge` (the default) is honored; see
  [Apple Container: what it does not do](#apple-container-runtime-container--what-it-does-not-do)
- ✅ Read-only root filesystem and tmpfs mounts
- ✅ **Native no-VM backend** (`macos-user`): agent under Seatbelt as
  `_yolojail`, `packages:` via native `aarch64-darwin` nix, host creds invisible
  — verified end-to-end on real Apple Silicon (see
  [The macos-user backend](#the-macos-user-backend))

> [!WARNING]
> **`yolo stop` and `yolo clean` do not exist** (verified 2026-08-23,
> `internal/cli/dispatch.go:15-35`). This bullet used to name them. The reclaim
> command is **`yolo prune`**; the full registry is `check`/`doctor`, `run`,
> `ps`, `loopholes`, `config`, `describe`, `apply`, `check-deps`, `pack`,
> `config-ref`, `init`, `init-user-config`, `broker`, `prune`, `macos-setup`,
> `macos-teardown`, `macos-unshare`, `macos-fix-permissions`. Note the last of
> those — `yolo macos-fix-permissions` (`dispatch.go:34`) — is a real macOS
> command this guide never mentions; it re-applies the shared-root ACL inheritance
> to a workspace.

## Limitations

Two kinds of limitation live here, and they are worth keeping apart.

**Platform limitations** apply to *every* macOS backend: no cgroups, no GPU
passthrough, no USB device passthrough, no device cgroup rules. Those are the
`###` sections further down, and yolo skips each with a warning.

**Backend limitations** are the interesting ones when you are still choosing.
**Podman is the parity reference** — setting the platform limitations aside, a
config key that works on Linux is wired up on a Podman Machine too. Each of the
two macOS-native backends drops a different set of keys, and the two tables below
are the whole list.

### Backend feature parity at a glance

**Moved.** The per-setup grid now lives once, in
[the user guide's *What works in each setup*](USER_GUIDE.md#what-works-in-each-setup) — it covers all
four setups (this page's three plus `podman` on Linux, the parity reference), every top-level config
key rather than a selection, and a *takes effect* column for the frozen-at-launch keys. It also
carries the pre-flight gates that can refuse a launch outright, which are not macOS-specific.

This page keeps what is genuinely macOS-only: the two per-backend explanations below, which say
*why* each cell is what it is, and the platform sections after them.

`macos-user` **honors** none of the network or scratch-storage keys — it is a native process on
your own machine, so there is no network namespace to configure and no container filesystem to make
ephemeral. Read and honored are not the same thing here, and the two halves differ: past the
validator, nothing on this backend's path looks at `ephemeral_storage` at all (the only reader,
`run.ScratchMountArgs`, sits on the podman branch of the assembler), while the network keys are
read *in order to say so* —
`run.appliedNetMode` answers `host` for it (the only mode it has, since the sandbox is on the
launcher's own stack) and tells the briefing that, and `run.noteMacosUserPortKeys` prints one
warning per non-empty `network.ports` / `network.forward_host_ports` entry. Neither path changes
what the sandbox does.

### Apple Container (`runtime: container`) — what it does not do

The headline is **loopholes**: this backend starts a host service for exactly one
of them — the OpenAI credential broker, which starts and then cannot be reached
from the jail — and for none of the others, so treat the surface as off. If you
use Claude with an OAuth login, or Codex/pi with an OpenAI subscription, those are
the rows to read first.

Everything below is announced at launch **except** the rows marked *silent* —
those you have to know about, because nothing tells you.

| Config key / feature | On Apple Container | Where it is decided |
|---|---|---|
| `loopholes` — from packs and from your own config | **inert.** No host service starts except `openai-auth-broker` (two rows down), whatever the loophole declares — and since 2026-09-18 **no pack is exempt from the report**: one yellow line per inert loophole at every launch, naming the pack, the loophole and the reason. The credential service was the one pack that line was withheld for, which made the single service this backend starts the single one it said nothing about. The reason is measured on `container` 1.1.0 and is expected to expire with an upstream release: a container→host connection completes its handshake and then carries nothing, so a `loopback-tls` loophole could not be dialled from the jail even if its daemon ran | `run/loopholesruntime.go` → `startLoopholes`; `run/loopholeinert.go` → `backendInertReason` |
| `claude-oauth-broker` in particular | **not running.** Refreshes of your Claude OAuth token are not serialized between jails — see the warning below | `run/run.go` → `runContainer` (the broker-singleton gate) |
| `openai-auth-broker` in particular (the `openai-auth` pack, which the `codex` and `pi` packs join automatically) | **starts, and the jail cannot reach it — said out loud since 2026-09-18.** It is the single name `startLoopholes` admits on this backend: the host daemon runs and its endpoint directory is mounted, and the dead hop above then swallows every request, so `codex` says `OpenAI login is required.` and an interactive login fails the same way. ⚠ Until 2026-09-18 **nothing told you**: the inert line was withheld for this one pack, the endpoint variable is emitted anyway, and the in-jail reachability witness does not escalate here (it escalates only for a host loopback yolo itself asked to have forwarded), so the one backend-shaped cause went unnamed by all three. The inert line now names the pack, the loophole and the measured reason — and the reason names `container` 1.1.0, so it retires itself when an upstream release changes the measurement rather than standing forever | `run/loopholesruntime.go` → `startLoopholes` (the `openAIAuthBrokerName` allowance); `run/packloopholes.go` → `startLoopholesDisclosed`; `run/loopholeinert.go` → `backendInertReason` |
| `mounts` | **honored from `container` 1.1.0; skipped with a warning below it.** Older versions accepted `:ro` and ignored it, so the mount would arrive *writable* and yolo declined rather than hand a UID-0 jail a writable window onto your host. yolo reads `container --version` once per launch and decides from it — and an unreadable version **declines**, because the safe answer is the one that costs a feature rather than the one that costs the guarantee | `run/backendcaps.go` → `roBindsUnsupported` |
| Pack `mount` grants | **honored from `container` 1.1.0; skipped with a warning below it.** Same root cause and version gate as `mounts` above — and this one is a grant a human approved at `pack install` against the word *read-only*, so honoring it writably would have made the approval untrue. ⚠ A single-**file** pack `mount` is skipped at **every** version: that is [apple/container#1089](https://github.com/apple/container/issues/1089), a different limitation from the `:ro` one and not covered by the floor | `run/packhostgrants.go` → `hostMountArgs` |
| Your host `~/.config/nvim` | **honored from `container` 1.1.0.** Podman binds it `:ro` at `/ctx/host-nvim-config` and the jail copies it into the agent's home at boot. Below the floor the ignored `:ro` would leave a live write channel into your real editor config for the whole session, so it was skipped with a warning (2026-08-24 to 2026-09-14); the visible symptom of the skip is nvim starting unconfigured | `run/assemble.go` → `assembleRunCmd` (nvim block) |
| `workspace_readonly` | **enforced from `container` 1.1.0; not enforced below it, with a loud warning.** Same root cause and same version gate as `mounts` above | `run/mounts.go` → `workspaceReadonlyMountArgs` |
| `cache_relocations` | **skipped, with a warning.** The cache stays on its original filesystem | `run/assemble_parts.go` → `appleContainerBaseMounts` |
| `ephemeral_storage` | **not honored, silently.** The scratch dirs (`/tmp`, `/run`, `/dev/shm`, …) are always `--tmpfs` here; the `volume` mode is podman-only | `run/assemble.go` → `assembleRunCmd` (`ScratchMountArgs` is on the podman branch only) |
| `resources.memory`, `resources.cpus` | **honored** — emitted as native `--memory` / `--cpus`. If you omit them yolo fills in a default (half your RAM, min 4 GB; half your cores, min 2) | `run/assemble_parts.go` → `resourceArgs` |
| `resources.pids_limit` | **not emitted, silently.** Apple Container has no equivalent flag | `run/assemble_parts.go` → `resourceArgs` |
| `network.mode: "bridge"` (the default) | **honored.** Apple Container gives each container its own `vmnet` namespace and yolo emits no `--net` — which is correct here | `run/assemble.go` → `assembleRunCmd` (network-mode block) |
| `network.mode: "host"` | **not honored — and asking for it is worse than leaving it unset.** Warns | see the warning below |
| `network.mode: "none"` (or any other value) | **not a legal value on any backend.** The key accepts `bridge` and `host` only; anything else is a config error that refuses the launch before a backend is even chosen | `config/validate.go` (the `network.mode` check) |
| `network.ports` | **emitted and inert — measured 2026-09-16** on `container` 1.1.0 / macOS 25.5. `container inspect` records the mapping (`hostAddress: 0.0.0.0`) and the published address carries no data: the Mac's dial connects, the jail-side listener sees it arrive and reset, nothing crosses. The container's own vmnet IP answers normally, so the reachable address is not the one yolo publishes. Nothing warns | `run/assemble.go` → `assembleRunCmd` |
| `network.forward_host_ports` | **breaks the launch — measured 2026-09-16.** yolo starts host-side `socat` before creating the container (`run.go`, "Start host-side port forwarding BEFORE the container") and AC refuses the flag naming that socket: `Error: host socket <path> already exists and may be in use`. The direction is inverted too — `--publish-socket host_path:container_path` creates the host socket and forwards a *host* connection inward to a container-side listener, which is the opposite of this key. A published socket does carry data both ways, so it is the material for a fix, not a dead end | `run/assemble_parts.go` → `forwardHostPortsArgs` |
| Pack `state` at `scope: machine` (e.g. `~/.claude-shared-credentials`) | **honored as of 2026-08-24.** It was never mounted before that, so cross-jail credential sharing silently degraded to per-workspace — see the warning below | `run/assemble_parts.go` → `appleContainerBaseMounts` |
| Any single-**file** read-only mount | **copied, not mounted** — by choice, not by necessity. This was attributed to [apple/container#1089](https://github.com/apple/container/issues/1089) ("cannot bind a single file"), which is **false on `container` 1.1.0** — measured 2026-09-14: a regular-file bind arrives, propagates writes and honors `:ro`. yolo keeps copying because a copy works on every version with no version floor to get wrong, and every consumer here reads its file at boot, so a snapshot is equivalent. yolo copies each one into the jail's home — your `yolo-user-env.sh`, pack briefings, pack `files`, your global gitignore, pack `reads-host` grants and `host_files` file sources. You should not notice; the files arrive with the same contents. **The one exception is a pack `mount` whose source is a file**, which is skipped rather than copied — see the row above for why | `run/helpers.go` → `acMaterialize` |

Directory mounts nest perfectly well on this backend — the shared cache is mounted
at `/home/agent/.cache` *inside* the `/home/agent` mount on every launch. The
single-file limitation is specifically about files.

> [!WARNING]
> **`network.mode: "host"` is strictly worse than leaving it unset.** No host
> networking is set up (no `--net` flag is emitted), *and* both port keys are read
> only in `bridge` mode — so asking for host mode also drops every published port
> and every forwarded host port. Remove the key, or switch to
> `YOLO_RUNTIME=podman` if you genuinely need host networking. yolo warns about
> this at launch; it used to say nothing.

> [!WARNING]
> **Claude OAuth refreshes are not serialized on this backend.** The
> `claude-oauth-broker` loophole is what stops two jails burning the same
> single-use refresh token, and this backend does not start it — the OpenAI
> credential broker is the only loophole host service it starts at all. Your
> Claude credentials file *is* shared across every workspace on
> the machine, so two jails refreshing at the same time are racing on one token.
> If you run several jails concurrently against one Claude login, use
> `YOLO_RUNTIME=podman`.

> [!IMPORTANT]
> **Upgrading to the 2026-08-24 fix: expect your Claude logins to converge.**
> Machine-wide pack `state` dirs were never mounted on Apple Container, so what
> should have been one shared credential was really one credential per workspace.
> Now that the dir is mounted, the first launch after upgrading **copies a
> stranded credential up** out of the workspace state dir into the machine-wide
> location — copy, never move, and only into a file that is missing, so you should
> not lose a login. What no code can preserve is the accidental independence the
> bug created: if you logged in separately in several workspaces, whichever
> workspace launches first wins the machine-wide slot and every other workspace
> uses that credential from then on. Each old copy is left in place at
> `<workspace>/.yolo/home/.claude-shared-credentials/` and simply stops being
> read; delete it when you are satisfied.

### macos-user (native, no VM) — what it does not do

`macos-user` has **no bind mounts at all**, and its run path returns from
`run/run.go` → `Run` before the code that consumes most mount and network config.
It is the fastest backend and the one that delivers the least.

| Config key / feature | On `macos-user` | Where it is decided |
|---|---|---|
| `loopholes` — the HOST half | **every one of them starts**, through the same spawn boundary a container launch uses, with the same "This launch runs pack code on your machine" disclosure printed first. ⚠ THIS ROW SAID *"inert, with exactly one exception"* UNTIL 2026-09-18 and that is retracted: it described an arm that started one credential service by hand, which is not what ships. Measured (unit, 2026-09-18): a bare `"packs": ["claude"]` publishes both `claude-oauth-broker.endpoint` and `openai-auth-broker.endpoint`. Reaching them needs no bind mount, which is why this backend can carry them at all: each endpoint file gets a per-file macOS ACL grant (`chmod +a`) for the sandbox account and its path is written into the session env the confined stage sources. `openai-auth-broker` is the one that **refuses the launch** if it does not come up. The inert line you still see here is the **platform** axis only — `audio`, `journal`, `host-processes` and `cgroup-delegate` declare `platforms: ["linux"]`. ⚠ NOT MEASURED on hardware | `run/run.go` → `Run` (the macos-user arm's `startLoopholesDisclosed` call); `macosuser/macosuser.go` → `EndpointGrantCommands`; `run/loopholeinert.go` → `backendInertReason` |
| `loopholes` — the JAIL half (`jail_daemon`) | **none of them runs, and the launch now says so by name.** There is no in-jail supervisor on this backend and no `yolo-jaild` built for darwin at all, so nothing reads the `YOLO_JAIL_DAEMONS` payload here. One `Declined:` line per declared daemon at every launch, naming it and its argv — measured on that same bare `"packs": ["claude"]`, which declares **three** (`yolo-jaild oauth-terminator`, `yolo-jaild openai-auth-adapter --listen 127.0.0.1:1460`, `yolo-jaild wire-bridge`) because the `claude` pack `needs` `openai-auth` and `wire-bridge` unconditionally. **What it costs:** an intercepting loophole does nothing useful even though its host daemon runs — `claude-oauth-broker`'s TLS terminator IS its jail half, so Claude OAuth refreshes are **not** serialized here; a Codex session keeps working until its first token refresh, which dials a port nothing binds; and the `wire-bridge` service has no host half at all, so it is absent end to end. Starting any of them is blocked on two unfiled rulings — how a declared argv resolves with no image, and whether the child runs under the Seatbelt profile | `run/run.go` → `Run` (the hoisted `jailDaemonsFor` value); `run/jaildaemondecline.go` → `noteMacosUserJailDaemonDeclines`; [`jail-daemon-on-macos-user-plan.md`](../design/jail-daemon-on-macos-user-plan.md) |
| `mounts` | **silently ignored, with no warning** | consumed in `run/assemble.go` → `assembleRunCmd` and `run/prepare.go` → `refreshJailBriefings`, both after the early return |
| `cache_relocations` | not delivered (it is a nested bind mount) — **warns**. A hand-made symlink is not a workaround either: the sandbox profile denies writes outside the workspace and sandbox home, and denies reads under `/Volumes` | `macosuser/orchestrator.go` → `buildPlan` |
| `forward_host_ports` | not wired (container-side only) | `run/assemble.go` → `assembleRunCmd`; `run/hostports.go` → `ParsePortForwards` |
| `per_side_paths` | not enforced — **warns**. Per-side shadowing needs a mount namespace; Seatbelt filters permissions and cannot fork a path | `macosuser/orchestrator.go` → `buildPlan` |
| `resources` | not enforced — **warns**. No cgroups, and no VM to size | `macosuser/orchestrator.go` → `buildPlan` |
| `workspace_readonly` | **enforced**, as Seatbelt deny rules | `macosuser/seatbelt.go` → `readonlyDenies` |
| Pack briefings and skills | **delivered by COPY** — **warns**, because a copy is writable where every other backend's bind is `:ro`: the agent can edit its own skills and briefing, and the next launch overwrites them again. They were *not delivered at all* until 2026-09-03, which was the sharper gap — the blocked-tool shims are generated either way, so a blocked command exited 127 with nothing explaining it | `run/loopholeinert.go` → `noteMacosUserContentGaps` |
| `lsp_servers` | **installed, since 2026-09-13.** ⚠ NOT MEASURED on hardware — it was **MEASURED FALSE** there on 2026-09-12, when this backend set `YOLO_LSP_SERVERS` (the table that renders config) and neither of the two install variables the generated script's loop reads, so the stage exited 0 having installed nothing. Both now cross — into the bootstrap env and into the session env file the confined stage sources — from the recipe table both backends share (`config.LSPInstalls`); the ruling to wire rather than re-warn is [`OQ-P5`](../reference/macos-user-provisioning.md#why-it-is-this-way) | `macosuser/runplan.go` → `BuildRunPlan`; `macosuser/provision.go` → `ProvisionArgv` |
| `mise_tools` | **installed**, since 2026-09-12: `mise` is on the floor and the stage runs `mise install` into a machine-wide `MISE_DATA_DIR`. Also warned until that landed. ⚠ NOT MEASURED on hardware | `macosuser/provision.go` → `ProvisionSetup` |
| `mcp_presets` | config renders, wrappers are **not delivered** — **warns** from inside the bootstrap. The preset wrappers are Linux-absolute, and the stage installs none of the npm packages behind them | `entrypoint/darwin.go` → `RunDarwinBootstrap` |
| Pack `reads-host` grants | **delivered by COPY, since 2026-09-13.** ⚠ NOT MEASURED on hardware — the delivery is unit-tested only; what *was* measured on a Mac (2026-09-13) is the probe that ruled out the cheaper alternative, a symlink, because Seatbelt evaluates the link's TARGET. The launcher copies each granted file into a root-owned tree under `/var/yolo-jail` that the sandbox can read and cannot write, and names it with `YOLO_CTX_ROOT`, so each surface composes *your* file. From 2026-08-24 until then they **did not cross** and warned: every surface rendered from its *defaults* layer, which was the more dangerous of the two host-byte gaps because nothing about the result looked wrong | `run/macosctxtree.go` → `buildMacosCtxTree`; `macosuser/macosuser.go` → `StageCtxCommands` |
| `host_files` entries with a `source` | **a FILE source is delivered by COPY, since 2026-09-13** — into the same tree as the row above, and with the same measurement caveat. A **DIRECTORY** source is still not delivered, and is now skipped with a **warning that names it**: a copy does not scale to an arbitrary tree, so split it into the files you need or use `runtime: "container"`, which binds it. Every source was **dropped from the launch entirely** (warning since 2026-08-24) until then — no file appeared at those paths at all. Entries with `content`/`defaults` and no `source` were never affected | `macosuser/runplan.go` → `hostFilesWire`; `run/loopholeinert.go` → `noteMacosUserHostByteGaps` |
| Pack `state` at `scope: workspace` | **per-workspace**, as everywhere else. The sandbox home is still the constant `/Users/_yolojail`, but each state dir in it is a **symlink** into `<workspace>/.yolo/home` — the same host directory podman binds from. It was shared across every workspace on the machine, and warned about, until the home layout landed | `entrypoint/darwinhomelayout.go` → `DeriveDarwinHomeLayout` |
| cgroups / resource limits | unavailable (no cgroups on macOS) | as [Cgroup Delegation](#cgroup-delegation-resource-limits) below |

The `mounts` row is the sharp one: it fails **silently**, so a config that
declares context mounts appears to work and delivers nothing. Pack `mount`
grants take the same path and are equally quiet.

The two host-byte rows are worth reading together, because for a year they were the
two halves of one gap and they failed differently: a dropped `host_files` source left
*nothing* at the path, while a `reads-host` grant left a plausible substitute. Only the
first was honest. Both were silent until 2026-08-24, warned from then, and **both are
delivered from 2026-09-13** — by exactly the mechanism this section used to name as the
outstanding fix: materialize the bytes host-side, the way Apple Container's copies
already worked. One difference from a bind survives and is worth knowing: a copy is a
snapshot taken at launch, so a host-side edit reaches the sandbox on the *next* launch
rather than live.

What is left of the gap is directory-shaped, and deliberately so. A config `mounts`
entry, a pack `mount` grant and a `host_files` entry whose `source` is a directory all
name an arbitrary user tree that a copy does not scale to. The first two are still
**silent**; the third now warns by name.

`workspace_readonly` is the row that changed. It was a **silent no-op** on
`macos-user` until commit `d0961f2c` (2026-08-23) — the key validated, the
launch succeeded, and nothing was read-only, because the backend had no mount
to attach `:ro` to. It now renders natively as SBPL
`(deny file-write* (subpath …))` rules emitted *after* the writable-set allow,
so SBPL last-match-wins makes them stick. Absolute or escaping entries are
dropped rather than emitted.

> [!NOTE]
> **The two backends used to collapse the state tiers in opposite directions, and
> both are fixed now.** Apple Container made the *machine-wide* tier per-workspace
> (fixed 2026-08-24); `macos-user` made the *per-workspace* tier machine-wide,
> because its home is one constant directory. The fix there keeps `HOME` exactly
> where it is and symlinks each `scope: workspace` directory into
> `<workspace>/.yolo/home`, so the declared `scope: machine` directory never moves
> and credential sharing works by the same hook it uses everywhere
> ([`../reference/macos-user-home-tiers.md`](../reference/macos-user-home-tiers.md)).
> ⚠ The reason this note gave for leaving it — *"the single home is that backend's
> shared-credentials mechanism"* — is retracted; colocation only supplied that
> directory's backing.

### Cgroup Delegation (Resource Limits)

macOS has no cgroup filesystem. The `yolo-cglimit` helper inside the jail and
the host-side cgroup delegation daemon are unavailable. This means:

- `yolo-cglimit --cpu 50 --name job -- command` will not enforce CPU limits
- The cgroup delegate socket (`/run/yolo-services/cgroup-delegate.sock`) is not
  created because no daemon listens; on Podman the host services directory is
  still mounted so the container volume mount succeeds. Apple Container mounts
  that directory only when the `openai-auth` loophole is active — the one host
  service it starts — and otherwise mounts nothing there
  (`run/assemble_parts.go` → `hostServicesMountArgs`)

**Workaround:** Use Podman Machine's built-in resource controls to limit
the VM's CPU/memory instead:

```bash
# Podman: configure at init time
podman machine init --cpus 2 --memory 4096
```

**Apple Container:** Native per-container resource limits work out of the box:

```bash
YOLO_RUNTIME=container yolo run  # uses --cpus and --memory flags natively
```

### GPU Passthrough

GPU passthrough is not available on macOS — neither NVIDIA (Podman CDI) nor
AMD ROCm (`/dev/kfd` + render nodes). Apple Silicon GPUs use Metal, and have
neither CUDA nor ROCm support.

- `"gpu": {"enabled": true}` in config is silently skipped with a warning
- `yolo check` reports GPU passthrough as unavailable on macOS

### USB Device Passthrough

Linux device paths (`/dev/bus/usb/...`) and `lsusb` are not available on
macOS. USB device passthrough configured via `"devices"` in `yolo-jail.jsonc`
is skipped with a warning.

### Device Cgroup Rules

`--device-cgroup-rule` flags are a Linux kernel feature. Any `"cgroup_rule"`
entries in the devices config are skipped on macOS.

### Cache Relocation (`cache_relocations`)

Moving a cache subdir onto other storage (see
[USER_GUIDE — Relocating a Cache Subdir](USER_GUIDE.md#relocating-a-cache-subdir-to-other-storage))
is **not implemented on Apple Container**. Not because the backend can't nest a
bind mount — it already mounts the shared cache at `/home/agent/.cache` inside
its writable `/home/agent` mount, which is the same nesting a relocation needs —
but because that backend takes a separate mount path built around a device
limit, and relocation has never been verified on real Apple Container hardware.
Rather than half-apply it, `yolo` prints one warning naming the skipped subdirs
and starts the jail with the cache on its original filesystem. Use
`YOLO_RUNTIME=podman` if you need it, and open an issue if you want it on Apple
Container.

It is **not implemented on `macos-user`** either, and warns there too. A
hand-made symlink is not a workaround on that backend: the Seatbelt profile
denies writes outside the workspace, the sandbox home, `/tmp` and
`/var/folders`, and denies reads under `/Volumes` — so the cache you were trying
to move stays on the boot volume.

On **Podman Machine** the mechanism itself should work, but the target has to be
a path the VM can see. Podman Machine shares your home directory into the VM, so
a target under `$HOME` ought to be fine while one on an unshared volume
(`/Volumes/...`) should fail at startup the same way any missing bind source
does. **Untested** — nobody has run this on a Mac; if you try it, the result is
worth reporting. Add the volume to the machine's mounts first if you need a
target outside `$HOME`.

### SO_PEERCRED Socket Authentication

The cgroup delegation daemon uses `SO_PEERCRED` on Linux to verify the
identity of socket clients. macOS has `LOCAL_PEERPID` as a partial equivalent
(PID only, no UID/GID). Since the cgroup daemon is skipped entirely on macOS,
this has no practical impact.

## Architecture

### Podman

```
┌─────────────────────────────────────────┐
│  macOS Host                              │
│  ┌───────────────┐  ┌────────────────┐  │
│  │  yolo (Go CLI) │  │ Nix (devShell) │  │
│  │                │  │ macOS packages │  │
│  └───────┬───────┘  └────────────────┘  │
│          │                               │
│  ┌───────▼──────────────────────────┐   │
│  │  Podman Machine                    │   │
│  │  (Linux VM — Apple Hypervisor)    │   │
│  │  ┌────────────────────────────┐  │   │
│  │  │  yolo-jail container        │  │   │
│  │  │  ┌──────────────────────┐  │  │   │
│  │  │  │  yolo-entrypoint     │  │  │   │
│  │  │  │  (always Linux)      │  │  │   │
│  │  │  │  AI agent runs here  │  │  │   │
│  │  │  └──────────────────────┘  │  │   │
│  │  └────────────────────────────┘  │   │
│  └──────────────────────────────────┘   │
└─────────────────────────────────────────┘
```

### Apple Container

```
┌─────────────────────────────────────────┐
│  macOS Host                              │
│  ┌───────────────┐  ┌────────────────┐  │
│  │  yolo (Go CLI) │  │ Nix (devShell) │  │
│  │                │  │ macOS packages │  │
│  └───────┬───────┘  └────────────────┘  │
│          │                               │
│  ┌───────▼──────────────────────────┐   │
│  │  Apple Virtualization.framework   │   │
│  │  (one VM per container)           │   │
│  │  ┌────────────────────────────┐  │   │
│  │  │  yolo-jail container/VM     │  │   │
│  │  │  ┌──────────────────────┐  │  │   │
│  │  │  │  yolo-entrypoint     │  │  │   │
│  │  │  │  (always Linux)      │  │  │   │
│  │  │  │  --cpus / --memory   │  │  │   │
│  │  │  │  native limits       │  │  │   │
│  │  │  └──────────────────────┘  │  │   │
│  │  └────────────────────────────┘  │   │
│  └──────────────────────────────────┘   │
└─────────────────────────────────────────┘
```

### macos-user (native, no VM)

```
┌─────────────────────────────────────────┐
│  macOS Host                              │
│  ┌───────────────┐  ┌────────────────┐  │
│  │  yolo (Go CLI) │  │ Nix (daemon)   │  │
│  │  as your user  │  │ aarch64-darwin │  │
│  └───────┬───────┘  └────────────────┘  │
│          │ stages yolo → /var/yolo-jail  │
│          │ sudo --user=_yolojail         │
│  ┌───────▼──────────────────────────┐   │
│  │  sandbox-exec (Seatbelt profile)  │   │
│  │  ┌────────────────────────────┐  │   │
│  │  │  _yolojail (hidden user)    │  │   │
│  │  │  yolo internal              │  │   │
│  │  │    darwin-bootstrap         │  │   │
│  │  │  AI agent runs here         │  │   │
│  │  │  packages: native darwin nix│  │   │
│  │  └────────────────────────────┘  │   │
│  └──────────────────────────────────┘   │
└─────────────────────────────────────────┘
```

Key insight: `yolo` runs on the macOS host and is platform-aware.
`yolo-entrypoint` runs inside the Linux container (podman/AC) and needs no macOS
changes; on the macos-user path the host `yolo` self-execs `yolo internal
darwin-bootstrap` as `_yolojail` instead, running the same config generators
natively. The Nix flake uses `pkgs` (native macOS) for all build-time
derivations (image-layer tooling, `writeShellScriptBin`, `stdenv.mkDerivation`,
etc.) and `imagePkgs` (Linux target) only for the *content* of the image
(chromium, bash, python, etc.). This means the image can be built on macOS using
the NixOS binary cache — no cross-compilation or remote Linux builder required.

## Troubleshooting

### `yolo check` reports macOS-specific issues

Run `yolo check` — it includes macOS-specific diagnostics for Nix daemon
connectivity, container-runtime (VM backend) status, whether a from-source
build is needed (and thus that the runtime must be up so it can offload to a
container builder), and the Nix store APFS volume.

### Podman Machine won't start

On headless Macs (EC2, CI), Podman Machine may fail because Apple's
Hypervisor.framework requires a GUI session. On such hosts, consider using
Apple Container instead (`YOLO_RUNTIME=container`) which uses
Virtualization.framework per-container.

On desktop Macs, try resetting the machine:

```bash
podman machine stop
podman machine rm
podman machine init --cpus 4 --memory 8192 --disk-size 50
podman machine start
```

### Nix build fails or hangs

1. Check the daemon is responsive: `nix store info` (should return within 2s)
2. If it hangs, see [Known Issue: Determinate Nix Daemon Hang](#known-issue-determinate-nix-daemon-hang)
3. If a package must be built from source, yolo offloads to a container builder
   on your active runtime — make sure the runtime is up (`podman machine start`
   or `container system start`) and re-run `yolo`.
4. If you configured your OWN remote Linux builder (escape hatch), check it:
   `nix store info --store ssh-ng://nix-builder` and `ssh nix-builder echo ok`.

### Container image not loading

If `yolo run` fails to load the image, try manually (there is no `yolo build`
subcommand — see the warning under *Install from source*):

```bash
# Build the image manifest AND the copier that reads it. `./result` is a
# nix2container image.json naming the layer digests — not a tarball, and not a
# script whose stdout is one — and `./result-1` is the skopeo that can read it.
nix build --impure --accept-flake-config .#ociImage .#imageCopier

# Deliver it. On Podman Machine the storage lives inside the VM and does not
# share /nix, so the archive goes through `podman load -i`, which streams it
# over podman's own connection into the VM:
./result-1/bin/skopeo --insecure-policy copy \
    "nix:$(readlink -f ./result)" \
    "docker-archive:/tmp/jail-image.tar:localhost/yolo-jail:latest"
podman load -i /tmp/jail-image.tar && rm /tmp/jail-image.tar

# On Apple Container, the same copy into an OCI archive:
#   ./result-1/bin/skopeo --insecure-policy copy \
#       "nix:$(readlink -f ./result)" \
#       "oci-archive:/tmp/jail-image.oci:yolo-jail:latest"
#   container image load -i /tmp/jail-image.oci && rm /tmp/jail-image.oci
```

There is no remote-builder variant of this any more: the copier is a *darwin*
build (it runs on your Mac and reads your local `/nix/store`), so the layers a
Linux builder realized are copied back by `nix build` and tarred locally.

### Slow first build

The first `nix build` downloads the nixpkgs tarball and all Linux packages
from the binary cache. Subsequent builds are instant due to the Nix store
cache. Because all packages are fetched from the NixOS binary cache (no local
Linux build required), the bottleneck is download speed rather than
compilation time.

### File ownership issues

On macOS, Podman Machine handles file ownership mapping via virtiofs so
containers see your host-side files correctly. This is handled automatically
by `yolo`.

### Port forwarding not working

**Podman:** Host↔container port forwarding uses TCP via
`host.containers.internal` instead of Unix domain sockets (virtiofs doesn't
support them). This is automatic — if port forwarding fails, ensure:

1. `socat` is available inside the container (it's in the default image)
2. The host service is listening on the configured port
3. `host.containers.internal` resolves inside the container:
   `podman exec <container> ping -c1 host.containers.internal`

**Apple Container:** ⚠ **Both port keys are broken here, measured 2026-09-16.**
`network.ports` is emitted and carries nothing; `forward_host_ports` refuses the
launch outright, because yolo hands `--publish-socket` a socket `socat` has
already created and because AC's own direction for that flag is host→container.
Neither has a workaround on this backend today; the working route for reaching a
host service is a different backend.

### Apple Container: no outbound internet (macOS 15 vmnet limitation)

Apple Container on Darwin 24.x (macOS 15) has a `vmnet` limitation that leaves
containers without outbound internet even though the bridge gateway is
reachable. First-time setup stalls: `mise` times out resolving node/go/python
version lists, `git`/`curl` can't reach `github.com` or `nodejs.org`.

**Symptom.** A jail can ping the gateway `192.168.64.1` but reaches nothing
beyond it — while the host has full internet:

```bash
# from inside a jail (yolo -- bash):
ping -c2 192.168.64.1   # OK
ping -c2 1.1.1.1        # 100% packet loss
```

**Cause.** On macOS 15 the `vmnet` framework is supposed to NAT the container
subnet out to the internet and doesn't. The address allocation itself is fine
(gateway `192.168.64.1`, containers `192.168.64.2+/24`), and the gateway
process even proxies DNS — but nothing NATs the container subnet's egress, and
host IP forwarding is off. `sudo pfctl -a 'com.apple/*' -s nat` shows an empty
NAT anchor and `sysctl net.inet.ip.forwarding` reads `0`. Apple documents the
framework limitation in [Apple Container: macOS 15
limitations](https://github.com/apple/container/blob/main/docs/technical-overview.md#macos-15-limitations);
it is fixed in macOS 26.

`yolo check` (a.k.a. `yolo doctor`) detects this: on macOS 15 with Apple
Container running it reads `net.inet.ip.forwarding` and, when it's `0`, warns
with the remediation below.

**Remediation** (host-side; supply the NAT that `vmnet` failed to). Replace
`en0` with your default-route interface — find it with
`route -n get default | grep interface`:

```bash
sudo sysctl -w net.inet.ip.forwarding=1
echo 'nat on en0 from 192.168.64.0/24 to any -> (en0)' | \
  sudo pfctl -a 'com.apple/yolo-vmnet-nat' -f -
```

This loads a NAT rule into a sub-anchor under the stock `nat-anchor
"com.apple/*"` (defined in `/etc/pf.conf`), so it composes with the existing
ruleset without editing or flushing it. Verify from a fresh jail:

```bash
yolo run -- curl -sS -o /dev/null -w '%{http_code}\n' https://github.com  # 200
```

**Caveat: not persistent.** Both the `sysctl` and the pf anchor reset on reboot
(and a `pfctl -f /etc/pf.conf` reload drops the anchor). Re-run the two
commands after a reboot, or wrap them in a `LaunchDaemon`. The durable fixes are
upgrading to macOS 26 (where `vmnet` NATs correctly) or using the `podman`
backend instead of Apple Container.

**A second, distinct variant — subnet disagreement.** macOS 15 vmnet can also
fail *earlier*, at addressing: because the network is created lazily when the
first container starts, the network helper and vmnet can pick different subnets.
Then the gateway the helper hands to containers isn't on any host `bridge*`
interface, and a jail **can't even reach `192.168.64.1`** — the container is
completely cut off, not merely internet-less. The NAT workaround above does
*not* help this case; the fix is to recreate the network coherently:

```bash
container system stop && container system start
```

If it recurs, pin the CIDR in `~/.config/container/config.toml`
(`[network]` `subnet = "192.168.64.1/24"`). `yolo check` distinguishes the two:
it compares the helper's allocated gateway (from `container system logs`)
against the host interface addresses and warns with *this* remedy when they
disagree, versus the forwarding/NAT remedy when addressing is sound.

### Apple Container: "virtual machine failed to start"

Apple's Virtualization.framework has a hard limit on directory-sharing devices
(bind mounts) per VM. YOLO Jail works around it by consolidating the workspace
state into a single `/home/agent` mount instead of individual overlays. If you
add many custom mounts, you may still hit the limit.

*(An earlier revision of this guide put that limit at "~22". That number came
from an upstream issue report and is not something this repo measures or
enforces anywhere, so it is no longer repeated as fact.)*

### Apple Container: "default kernel not configured for architecture arm64"

Apple Container needs a Linux kernel to boot its VMs. Install the recommended
one:

```bash
container system kernel set --recommended
```

### Apple Container: image load fails

Apple Container only accepts OCI-layout image tars. YOLO Jail automatically
converts via skopeo (preferred) or podman as fallback:

```bash
# Recommended: install skopeo (no daemon needed)
brew install skopeo

# Or use podman as fallback (needs running daemon)
podman machine start
```

### `/tmp` bind mount failures

macOS `/tmp` is a symlink to `/private/tmp`.

**Podman Machine:** The VM mounts `/private` from the host via virtiofs but
does not resolve the `/tmp` symlink itself. YOLO Jail automatically calls
`.resolve()` on all socket/directory paths before passing them to Podman, so
`/tmp/...` paths are transparently converted to `/private/tmp/...`.

### Podman Machine: broker socket bind-mount fails (`EOPNOTSUPP`)

Podman Machine cannot bind-mount Unix socket *files* directly — Podman returns
`Error: statfs ...: operation not supported` or `EOPNOTSUPP`. Nothing crosses
that boundary as a socket any more: the jail reaches the host-wide broker
singleton over **loopback-TLS**, through a per-jail `svcendpoint` front that
publishes a plain *file* (`/run/yolo-services/claude-oauth-broker.endpoint`)
into the already-mounted directory. A file is what virtiofs carries fine, and
the address lives inside it rather than in the mount.

**There is no broker relay any more** (deleted 2026-08-19,
`docs/design/broker-as-a-pack.md` [§7](../design/broker-as-a-pack.md#7-what-this-deletes-what-it-costs-what-it-forecloses)). `internal/brokerrelay`, its
`/tmp/yolo-broker-relay-<hash>.{pid,lock,sock}` files and its
`~/.local/share/yolo-jail/logs/broker-relay-<hash>.log` are gone, and so is the
attach-time healing that used to restart one. The front is a **goroutine inside
the `yolo` process that launched the jail**, so it dies with that process: a jail
whose launcher is gone is **relaunched**, not attached-and-repaired. The daemon
behind it is host-wide and survives — one singleton, one front per jail. A host
upgrading across this change still has old relay processes and files in `/tmp`;
`yolo prune --apply` sweeps them, and so does a reboot.

### Podman Machine: TTY error (`crun: unlink /dev/console: Read-only file system`)

When stdout is a TTY, Podman passes `-t` to `crun`, which tries to unlink
`/dev/console` to set up a console device. With `--read-only` this fails unless
Podman's automatic read-only tmpfs support is active. YOLO Jail only sets
`--read-only-tmpfs=false` on Linux (where it's needed to avoid a conmon JSON
parsing conflict); on macOS the flag is omitted so crun can set up the console
correctly. No manual action is needed.

### `yolo check` reports "Nix daemon: user is NOT trusted"

With Determinate Nix on macOS, non-trusted users can still build the image via
binary cache substitution (no compilation needed). `yolo check` treats this as
a **warning** rather than a failure. To silence it, add your user to
`trusted-users` in `/etc/nix/nix.custom.conf` and restart the daemon:

```bash
# Add to /etc/nix/nix.custom.conf:
echo 'trusted-users = root your-username' | sudo tee -a /etc/nix/nix.custom.conf
sudo launchctl kickstart -k system/systems.determinate.nix-daemon
```

<!-- changelog -->
- [2026-09-18] Recorded that Apple Container now REPORTS the OpenAI credential service inert: the inert line was withheld for that one pack, which made the single service this backend starts the single one the launch said nothing about
- [2026-09-18] Corrected the `macos-user` loopholes row, which still said "inert, with exactly one exception": every loophole's HOST daemon starts there through the ordinary spawn boundary (measured: a bare `["claude"]` publishes two endpoints), and the JAIL half — `jail_daemon` — runs for none of them and is now declined by name at launch. The Apple Container rows are unchanged and still measured
- [4d54df64] Reworded intro to two approaches (Linux container by default vs native macos-user), dropping the "always a container" framing
- [9f082ebf] Added a "Choosing a runtime" section that leads with why (performance + native arch) before the model details, and retitled the macos-user section around that
- [78c23f1a] Replaced "never auto-detected" with "never selected automatically or by default — including when no container runtime is installed"
- [8a7a2d41] Split Prerequisites into "always required" vs "pick ONE runtime" (Podman / Apple Container / macos-user), so the runtimes read as options not co-requirements
- [2026-09-16] Corrected "no loophole host service runs on either macOS-native backend": exactly one does, on both — `openai-auth-broker`, which starts and cannot be dialled on Apple Container and genuinely works (refusing the launch if it fails to start) on `macos-user`
- [2026-08-24] Restructured Limitations into platform-wide vs per-backend, added the Apple Container disabled-feature table (loopholes, `mounts`, `cache_relocations`, `ephemeral_storage`, `pids_limit`, `network.mode`, single-file mounts) and an at-a-glance parity table beside the existing `macos-user` one; recorded what each backend DOES honor so a working backend stops reading as broken
