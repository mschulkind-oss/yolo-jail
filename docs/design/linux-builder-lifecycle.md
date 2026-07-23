# The macOS Linux builder: what it is and how its lifecycle is managed

**Status:** REFERENCE (2026-07-23) — describes the builder as it exists today,
plus a **known bug** in the key lifecycle (§6) that has an open work item on the
[ROADMAP](../plans/ROADMAP.md).

**Audience:** anyone debugging `yolo check` / `yolo builder *` on macOS, or
touching `internal/builder/`. For end-user setup instructions see
[docs/guides/macos.md § Building the image on macOS](../guides/macos.md#building-the-image-on-macos-cache-vs-linux-builder);
for *why* this is a fallback and not the happy path see
[happy-path-principle.md](happy-path-principle.md). This doc is the mechanism —
how the pieces fit and where the sharp edges are.

## 1. Why a builder exists at all

The yolo-jail OCI image is a **Linux** (`aarch64-linux`) image. Most of its
closure comes prebuilt from `cache.nixos.org`, but a few derivations are built
from **this repo's own source** (`yolo-jail-conf`, the entrypoint package, the
image stream script) and are therefore never on the public cache. macOS cannot
execute Linux build steps locally, so Nix must **offload** those derivations to
a Linux machine.

The **happy path** is to sidestep building entirely: download the fully-built
image from yolo-jail's own Cachix cache (revival plan D4, substituter enabled;
Mac download proof still pending — see [ROADMAP](../plans/ROADMAP.md)). The
builder is the **single sanctioned fallback**, used only until the cache serves
your platform or when you add a custom package the cache doesn't have. Per
[happy-path-principle.md](happy-path-principle.md), `yolo check` names *this one*
fallback and nothing else.

## 2. What the builder actually is

`nixpkgs#darwin.linux-builder` — a small NixOS VM run under Apple
Virtualization. Nix's daemon reaches it over SSH on **localhost:31022** and
hands it `aarch64-linux` derivations to build. It is not bespoke to yolo-jail;
we just orchestrate its lifecycle. The moving parts:

| Piece | Where | Set by |
|---|---|---|
| `builders = ssh-ng://builder@linux-builder …` | `/etc/nix/nix.custom.conf` (or `nix.conf`) | `yolo builder setup` |
| `trusted-users` includes you | same file | `yolo builder setup` (merged, never clobbered) |
| SSH host alias `linux-builder` → `localhost:31022`, `IdentityFile /etc/nix/builder_ed25519` | `/etc/ssh/ssh_config.d/100-linux-builder.conf` | `yolo builder setup` |
| The builder SSH **keypair** | `/etc/nix/builder_ed25519{,.pub}` (root:nixbld) | first-boot, via `sudo` (see §5–6) |
| The VM process | detached, PID in `$GLOBAL_STORAGE/linux-builder.pid`, log in `$GLOBAL_STORAGE/logs/linux-builder.log` | `yolo builder start` / auto-start before a build |

Constants live in `internal/builder/builder.go` (`BuilderPort = 31022`,
`BuilderKeyPath = /etc/nix/builder_ed25519`, host alias `linux-builder`).

## 3. The three commands (`internal/builder`)

- **`yolo builder setup`** — one-time privileged wiring. Builds a single root
  script (`SetupRootScript`) and pipes it to **one** `sudo bash -s` (TTY
  inherited so it can prompt): append the `builders` line, merge `trusted-users`,
  write the SSH alias, `launchctl kickstart` the nix-daemon. Idempotent (guards
  on existing content). This does **not** touch the keypair.
- **`yolo builder start`** — bring the VM up now. Two internal paths, chosen by
  setup-state (§5).
- **`yolo builder stop`** — `killpg`→`kill` the recorded PID, remove the PID
  file.
- **`yolo builder status`** — read-only snapshot: setup done? key present?
  reachable on 31022?

Setup-state is probed by `BuilderSetupState` (`internal/builder/buildercmd.go`):

```
SSHConfig  = /etc/ssh/ssh_config.d/100-linux-builder.conf is a file
NixBuilder = nix.conf has an uncommented builders line naming aarch64-linux + linux-builder
Key        = /etc/nix/builder_ed25519 is a file      ← see §6, this check is too weak
Done       = SSHConfig && NixBuilder                  ← note: Key is NOT part of Done
```

## 4. Normal lifecycle (the intended flow)

```
yolo builder setup        one sudo: nix.conf + trusted-users + ssh alias + daemon restart
        │
        ▼
yolo builder start        first boot only: foreground `nix run …linux-builder`, real TTY
  (first boot)            → the VM's create-builder installs the keypair to /etc/nix (one sudo)
                          → you see "builder@… login:" → Ctrl-C to return
        │
        ▼
build offloads            nix daemon dials builder@linux-builder:31022, hands off
  automatically           aarch64-linux derivations; a launchd idle-timer stops the VM
        │
        ▼
later builds              yolo auto-starts the VM detached (no TTY) before a build if
                          it's not reachable — this is the steady state
```

`yolo check`'s Image Build section is the trigger surface: it is **quiet** when
everything is cached, and only escalates — naming the offending derivation —
when a from-source build is genuinely required. When it does, it names *the one
fix* (`yolo builder start`), per the happy-path principle.

## 5. Start: foreground vs. detached — and why it matters

`BuilderStartCmd` branches on whether the key is already present:

- **First boot (`!state.Key`):** `FirstBootInteractive` → `StartVMForeground`
  runs `nix run nixpkgs#darwin.linux-builder` with **stdin/stdout/stderr
  inherited** — a real TTY. This matters because the VM's own `create-builder`
  script installs the SSH keypair into `/etc/nix` via `sudo`, and a foreground
  TTY is where you answer that prompt. Ctrl-C is expected and treated as success.
- **Steady state (key present):** `EnsureBuilder` → `StartVMDetached` spawns the
  same command **detached** (`setsid`, output to the log file, **no TTY**), then
  `pollUntilReachable` waits up to 90s for port 31022. This is what auto-start
  before a build uses.

The whole design assumes: **any `sudo` the VM needs happens only on the
foreground first-boot path.** The detached path must never need `sudo`, because
there is no terminal to answer it. §6 is what happens when that assumption
breaks.

## 6. The key lifecycle — and the current bug

**How upstream manages the keypair.** `nix run …darwin.linux-builder` runs
`create-builder` → `add-keys`, which does (reconstructed from the store scripts):

```bash
KEYS="${KEYS:-./keys}"          # ← relative to the current working directory!
# generate KEYS/builder_ed25519{,.pub} if absent (fresh random key)
if ! cmp "$KEYS/builder_ed25519.pub" /etc/nix/builder_ed25519.pub; then
    sudo --reset-timestamp install-credentials "$KEYS"   # reinstall to /etc/nix
fi
```

Two facts collide:

1. **`KEYS` defaults to `./keys`, relative to CWD.** yolo starts the VM with CWD
   = wherever you invoked `yolo` (your workspace) and **pins neither `KEYS` nor
   `cmd.Dir`** (`startVMForegroundReal` / `startVMDetachedReal` in
   `internal/builder/real.go` just `exec.Command("nix", "run", …)`). So the key
   the VM compares against `/etc/nix` is whatever `./keys` happens to be in the
   directory you launched from.
2. **A mismatch triggers a `sudo`.** If `./keys/builder_ed25519.pub` differs from
   `/etc/nix/builder_ed25519.pub` — or `./keys` is absent, in which case a
   *fresh random* key is generated that also won't match — `add-keys` runs
   `sudo install-credentials` to reconcile.

**The failure.** On the **detached** auto-start path there is no TTY. If a stray
`./keys` dir sits in your workspace (e.g. leftover from a manual `nix run
…linux-builder` in that directory), or if CWD simply has no persistent matching
`./keys`, the `sudo` prompt cannot be answered and the child dies. yolo surfaces
the log's last line verbatim:

```
Could not start builder: builder process exited early (sudo: a password is required)
```

**Why `yolo builder status` still says "ssh key: yes".** The `Key` probe only
checks that `/etc/nix/builder_ed25519` *exists* — not that it *matches* the key
in the CWD the VM will use. So a key was installed once (setup looked complete),
`BuilderStartCmd` takes the **detached** path (§5), and it trips the `sudo` that
the detached path was never supposed to need. Observed 2026-07-23.

**Root causes, precisely:**

- **yolo doesn't pin the builder identity.** `KEYS` is left to default to a
  CWD-relative `./keys`, so the builder's identity depends on which directory
  you launched from — and any stray `./keys` can hijack it.
- **The `Key` check answers the wrong question.** "Does a key exist in
  `/etc/nix`?" instead of "does our pinned key *match* `/etc/nix`?" — so a
  mismatch routes to the no-TTY detached path instead of the interactive one.
- **The error is a leaked internal.** `sudo: a password is required` is the VM
  script's stderr, not guidance. It tells you nothing about `./keys`, CWD, or
  the one-time reconcile you actually need.

## 7. Fixing a wedged builder today (manual, needs your sudo)

Reconcile `/etc/nix` from a **stable** key directory once, from a directory with
no stray `./keys`:

```bash
# 1. Remove any stray ./keys in your workspace (and other stale cruft).
rm -rf ./keys

# 2. Reconcile from a persistent, out-of-workspace key dir, interactively.
mkdir -p ~/.local/share/yolo-jail/builder-keys
cd ~                                        # anywhere without a ./keys
KEYS=~/.local/share/yolo-jail/builder-keys nix run nixpkgs#darwin.linux-builder
# answer the single sudo prompt; at "builder@… login:" press Ctrl-C
```

After this, `/etc/nix/builder_ed25519.pub` matches
`~/.local/share/yolo-jail/builder-keys`. **Caveat:** until the code fix in §8
lands, a plain `yolo builder start` from your workspace can still regress,
because it will again run with CWD = workspace and an unpinned `KEYS`.

## 8. The durable fix (open work item)

Tracked on the [ROADMAP](../plans/ROADMAP.md). Three changes in
`internal/builder`, smallest-correct-first:

1. **Pin `KEYS` to `$GLOBAL_STORAGE/builder-keys`** in both
   `startVMForegroundReal` and `startVMDetachedReal` (set it in the child env).
   The builder identity becomes independent of the launch directory; a stray
   `./keys` can never hijack it again.
2. **Make the `Key` probe compare, not just exist-check.** `BuilderSetupState`
   should report `Key` true only when the pinned `KEYS` pubkey **matches**
   `/etc/nix/builder_ed25519.pub`. A mismatch (or absence) then routes
   `BuilderStartCmd` to the **foreground** first-boot path that can answer the
   `sudo` prompt — never the detached path.
3. **Replace the leaked stderr with guidance.** When start fails because the key
   needs a `sudo` reconcile, print the one fix — "run `yolo builder start` in a
   terminal and approve one sudo prompt" — instead of surfacing
   `sudo: a password is required`. Name the one path, per
   [happy-path-principle.md](happy-path-principle.md).

This is security-adjacent (`sudo` + builder keys) and gated by
`just build-go` + a nested-jail verification per
[AGENTS.md](../../AGENTS.md#testing). yolo should **never** auto-`sudo`; the user
always approves the single prompt.

## 9. Related cruft worth cleaning

The wedge is easy to hit because untracked leftovers accumulate in the repo
root. As of 2026-07-23 the tree carried three untracked, unignored dirs — none
tracked by git:

- `keys/` — a stray builder keypair whose pubkey did **not** match `/etc/nix`;
  the direct trigger of the §6 wedge.
- `src/` (`_version.py`) and `yolo_jail.egg-info/` — stale Python packaging
  artifacts from before the Go port; the ROADMAP already notes the Python tree
  is gone (`git ls-files src/` → empty). See ROADMAP J2 note.

None are needed. A `.gitignore` entry for the builder key dir (once §8 pins it
out of the workspace) plus a one-time cleanup would keep this from recurring.

## 10. Where things live

| Topic | Authority |
|---|---|
| End-user builder setup, cache-vs-builder | [docs/guides/macos.md](../guides/macos.md#building-the-image-on-macos-cache-vs-linux-builder) |
| Why the builder is a fallback, not the happy path | [happy-path-principle.md](happy-path-principle.md) |
| The Cachix happy path (D4) | [../plans/handoff-cachix-cache.md](../plans/handoff-cachix-cache.md) |
| Command bodies + lifecycle orchestration | `internal/builder/{builder,buildercmd,commands,real}.go` |
| `yolo check` Image Build section | `internal/cli/check/{sections_nix,section_nix_probe,builder}.go` |
