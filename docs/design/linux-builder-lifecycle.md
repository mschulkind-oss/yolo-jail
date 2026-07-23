# The macOS Linux builder: two mechanisms, and why the VM one is going away

**Status:** DECISION (2026-07-23) — **remove the VM builder entirely; the
container builder is the sole builder.** Ratifies revival-plan
[Open Decision #3](../plans/macos-revival-and-distribution-plan.md). This doc is
the canonical record of *why*, plus the mechanism explanation for both builders
and the diagnosis of the live VM-builder bug that is part of the evidence.
Tracked as an open work item on the [ROADMAP](../plans/ROADMAP.md).

**Audience:** anyone debugging `yolo check` / `yolo builder *` on macOS, or
touching `internal/builder` / `internal/containerbuilder`. For *why the builder
is a fallback below the Cachix download at all* see
[happy-path-principle.md](happy-path-principle.md); for the container-builder
reasoning see
[../research/macos-container-builder-exploration.md](../research/macos-container-builder-exploration.md);
for end-user setup see
[docs/guides/macos.md](../guides/macos.md#building-the-image-on-macos-cache-vs-linux-builder)
(which still documents the VM builder and is slated to be reconciled — see §9).

## 1. Why a builder exists at all

The yolo-jail OCI image is a **Linux** (`aarch64-linux`) image. Most of its
closure comes prebuilt from `cache.nixos.org`, but a few derivations are built
from **this repo's own source** (`yolo-jail-conf`, the entrypoint package, the
image stream script) and are therefore never on the public cache. macOS cannot
execute Linux build steps locally, so Nix must **offload** those derivations to
something running Linux.

Three layers, in happy-path order:

1. **Cachix download (the real happy path).** Download the fully-built image;
   build nothing, need no builder. Revival plan D4 — substituter enabled, Mac
   download proof pending. When this lands, the builder question is moot for
   almost everyone.
2. **A builder (the fallback), for an uncached `packages:` derivation.** This is
   what this doc is about. There are **two** implementations of it in the tree
   (§2) — and the decision here is to keep one and delete the other.
3. **macos-user needs no builder at all** — it runs native `aarch64-darwin`
   binaries, no Linux image. Out of scope here.

## 2. The two builder mechanisms

Both present the same thing to Nix — *a remote build machine reached over
`ssh-ng`* — so the `nix.conf`/`--builders` line, the SSH key, and
`builders-use-substitutes` are the same idea in each. Only **what answers the
SSH port** differs.

### 2a. The VM builder — `internal/builder` (being removed)

`nixpkgs#darwin.linux-builder`: a NixOS VM under QEMU/Apple Virtualization,
reached on **localhost:31022**. Orchestrated by the `yolo builder
{setup,start,stop,status}` commands. Requires:

- a one-time privileged `yolo builder setup` (one `sudo`: `nix.conf` builders
  line, `trusted-users` merge, SSH alias, daemon kickstart),
- an interactive **first boot** where the VM installs its SSH keypair into
  `/etc/nix` via `sudo`,
- a detached VM process with a PID/log file, and an idle-stop story that was
  never finished.

Constants in `internal/builder/builder.go` (`BuilderPort = 31022`,
`BuilderKeyPath = /etc/nix/builder_ed25519`). Wired into exactly two places:
the `yolo builder` command (`internal/cli/commands.go`) and `yolo check`'s
Image Build section (`internal/cli/check/`). **Notably, the real `yolo` run path
does *not* use it** — see §2b.

### 2b. The container builder — `internal/containerbuilder` (the keeper)

A tiny `nix + sshd` container (`ghcr.io/mschulkind-oss/yolo-jail-builder:latest`,
built on arm-Linux CI, so no Mac ever builds it) run on the **runtime that's
already up** — podman or Apple Container — reached over `ssh-ng`. Lifecycle:
pull → run detached → wait for its sshd → hand Nix a `--builders` line → tear it
down. **Zero idle RAM** (it exists only during a build), **no `sudo`**, **no
QEMU**, **no `yolo builder` command, no first-boot dance.**

It is wired into the **actual run path**: `internal/image/autoload.go`'s
`BuildOffload` seam calls `buildImageWithContainerBuilder`
(`internal/image/builderoffload.go`) when a from-source build is needed on macOS.
Per-runtime argv differs correctly (podman publishes `127.0.0.1:31022:22`; Apple
Container is reachable on its per-container VM IP with no `-p`). Its client key
lives out of the way under `$GLOBAL_STORAGE` (`containerbuilder.BuilderKeyDir`)
and is regenerated on demand — none of the VM builder's `/etc/nix` reconcile
problem.

## 3. The decision: remove the VM builder

**The container builder covers every matrix cell the VM builder did, more
happy-path.** Proven end-to-end:

| Cell | VM builder | Container builder |
|---|---|---|
| podman (macOS) | works, but QEMU + sudo + first-boot | ✅ proven end-to-end **in-jail** |
| Apple Container (macOS) | works | ✅ **PROVEN on real HW 2026-07-17** (`AC-CONTAINER-BUILDER-WORKS`) |
| macos-user | n/a (no builder) | n/a (no builder) |
| Setup cost | `yolo builder setup` (one sudo) + interactive first boot | **none** — automatic, part of the build |
| Idle RAM | a ~3 GB VM (idle-stop never finished) | **0** (ephemeral per build) |
| `sudo` at build time | yes (key reconcile — see §5) | **no** |

Per [happy-path-principle.md](happy-path-principle.md), a second builder only
earns its place if it covers a cell the first cannot. The VM builder covers
**none**. The one historical reason it was kept — *"can Apple Container even host
an sshd container the host daemon can reach?"* was unproven
([macos-container-builder-exploration.md](../research/macos-container-builder-exploration.md)
§5) — is **discharged** (proven 2026-07-17). So it drops entirely, not to a
"parked fallback." (`macos-linux-builder-explained.md:184` already said "the
current builder direction is the container builder"; this makes it official and
deletes the loser.)

**What removal is *not*:** it does not remove a user's ability to point Nix at
*their own* remote builder. Someone already running **nix-darwin
`linux-builder`**, or with a Linux box in `/etc/nix/machines`, still works —
that's their nix config, orthogonal to ours. We are deleting *our* VM-builder
machinery (`internal/builder`, the `yolo builder` commands), not the generic
`ssh-ng` remote-builder mechanism.

## 4. Work items (tracked on the ROADMAP)

1. **Delete `internal/builder`** and the `yolo builder {setup,start,stop,status}`
   subcommand wiring in `internal/cli/commands.go`. Drop its tests.
2. **Rewire `yolo check`'s Image Build section** (`internal/cli/check/`,
   `sections_nix.go` + `builder.go`) off the VM-builder probes
   (`EnsureBuilder`/`ensureBuilderReal`, the `nix run …darwin.linux-builder`
   remedy) and onto the container-builder reality: on an uncached-build macOS
   host, report that the build will offload to a container on the active runtime
   (and that the runtime must be up), not "run `yolo builder start`."
3. **Reconcile the run-path failure diagnosis** (`nixdiag.LinuxBuilderRemedy` /
   `linuxBuilderRemedy` in `internal/cli/run/imageload.go`) so a build-offload
   failure names the container builder, not the VM.
4. **Reconcile the user-facing docs** (§9).

Security-adjacent and gated by `just build-go` + a nested-jail verification per
[AGENTS.md](../../AGENTS.md#testing).

## 5. The VM-builder bug (kept as evidence + a manual unblock)

This is what surfaced the whole question — and it's a good illustration of *why*
the VM builder is the wrong shape. Observed 2026-07-23: `yolo check` FAILs at
Image Build, and following its advice (`yolo builder start`) dies with:

```
Could not start builder: builder process exited early (sudo: a password is required)
```

**Why.** `nix run …darwin.linux-builder` runs `create-builder` → `add-keys`,
which defaults `KEYS=./keys` **relative to the current working directory** and,
on a mismatch with `/etc/nix/builder_ed25519.pub`, runs `sudo install-credentials
./keys` to reconcile. yolo starts the VM with CWD = your workspace and pins
neither `KEYS` nor `cmd.Dir` (`startVMForegroundReal`/`startVMDetachedReal` in
`internal/builder/real.go`). So:

- a stray `./keys` in the workspace (leftover from a manual `nix run
  …linux-builder`) whose pubkey differs from `/etc/nix` triggers the `sudo`; and
- on the **detached** auto-start path there is no TTY to answer it, so the child
  dies. `BuilderStartCmd` takes the detached path because the `Key` state probe
  (`BuilderSetupState`, `internal/builder/buildercmd.go`) only checks that a key
  *exists* in `/etc/nix`, not that it *matches* the CWD `./keys` — so `yolo
  builder status` cheerfully says "ssh key: yes" while the start wedges.

The container builder has none of this: no CWD-relative key, no `/etc/nix`
reconcile, no `sudo`, no detached-vs-foreground TTY split. **The bug is not worth
fixing — it's worth deleting.**

**Manual unblock, if you hit it before removal lands** (needs your sudo, from a
directory with no stray `./keys`):

```bash
rm -rf ./keys                                   # remove the stray workspace key
mkdir -p ~/.local/share/yolo-jail/builder-keys
cd ~                                            # anywhere without a ./keys
KEYS=~/.local/share/yolo-jail/builder-keys nix run nixpkgs#darwin.linux-builder
# answer the single sudo prompt; at "builder@… login:" press Ctrl-C
```

Better: if the container builder is available (podman/AC up), just let a normal
`yolo` run offload the build — no sudo, no keys.

## 6. Related cruft worth cleaning

The wedge was easy to hit because untracked leftovers accumulate in the repo
root. As of 2026-07-23 the tree carried three untracked, unignored, git-untracked
dirs:

- `keys/` — a stray builder keypair whose pubkey did **not** match `/etc/nix`;
  the direct trigger of the §5 wedge.
- `src/` (`_version.py`) and `yolo_jail.egg-info/` — stale Python packaging
  artifacts from before the Go port (`git ls-files src/` → empty; ROADMAP J2
  note).

None are needed; a one-time cleanup + a `.gitignore` entry would prevent
recurrence. (Once the VM builder is gone, no workspace `./keys` matters at all.)

## 7. Where things live

| Topic | Authority |
|---|---|
| The removal decision | this doc + revival plan Open Decision #3 |
| Why container-builder, image sourcing, AC risk (now discharged) | [../research/macos-container-builder-exploration.md](../research/macos-container-builder-exploration.md) |
| Linux-person's explainer of the whole builder question | [../research/macos-linux-builder-explained.md](../research/macos-linux-builder-explained.md) |
| Runtime × builder × config state matrix | [../research/macos-support-matrix.md](../research/macos-support-matrix.md) |
| Why a builder is a fallback below Cachix at all | [happy-path-principle.md](happy-path-principle.md) |
| The Cachix happy path (D4) | [../plans/handoff-cachix-cache.md](../plans/handoff-cachix-cache.md) |
| VM-builder code (to delete) | `internal/builder/`, `internal/cli/commands.go`, `internal/cli/check/{sections_nix,builder}.go` |
| Container-builder code (the keeper) | `internal/containerbuilder/`, `internal/image/{autoload,builderoffload}.go` |
| Proof the container builder works on AC | [../plans/runbooks/mac-ac-container-builder.md](../plans/runbooks/mac-ac-container-builder.md) |

## 8. Escape hatch (unchanged by removal)

A power user who *already* runs **nix-darwin** can set
`nix.linux-builder.enable = true;`, or point Nix at any Linux box via
`/etc/nix/machines`. That's their nix configuration and keeps working — yolo just
stops shipping and orchestrating its own VM builder.

## 9. User-facing docs to reconcile (follow-up)

These still name the VM builder as *the* fallback and never mention the container
builder; they need updating (or, since the container builder is automatic, the
"builder" concept may largely disappear from user docs):

- [docs/guides/macos.md](../guides/macos.md) — "Building the image on macOS" §
  presents `nix run nixpkgs#darwin.linux-builder` as the fallback.
- [happy-path-principle.md](happy-path-principle.md) — the worked example names
  `nix-darwin linux-builder` as the single fallback and lists a QEMU VM under
  "deliberately NOT supported." The worked example should be updated to the
  container builder.
- `README.md`, [docs/guides/USER_GUIDE.md](../guides/USER_GUIDE.md),
  [../plans/handoff-cachix-cache.md](../plans/handoff-cachix-cache.md) — all
  frame the macOS fallback as a "Nix remote Linux builder" (the VM).
