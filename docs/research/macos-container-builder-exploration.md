# Exploring the container-based Linux builder for macOS

**Context:** on the container-runtime macOS path (`runtime: container`/`podman`),
when a `packages:` build isn't cached, we need a Linux builder. Decision so far
(yours): **run the builder as a container on the runtime that's already up**
(ephemeral, no QEMU, no idle RAM), and **publish our own builder image to GitHub
(GHCR)** for predictability rather than pulling a stock one. This doc pins down
the open questions: *what must the image contain, can it be tiny/alpine, and how
do we build+ship it without a chicken-and-egg.*

---

## 1. The one hard constraint that answers "alpine?": a nix builder MUST contain nix

A Linux person's instinct is "smallest base = alpine." **That doesn't apply
here, and it's worth understanding why.**

nix's remote-builder mechanism (`builders = ssh-ng://…`) works by the host nix
daemon **SSHing into the builder and running `nix-daemon --stdio` on the far
end** (verified: the ssh-ng protocol *is* "pipe to a remote `nix-daemon
--stdio`"; the older `ssh://` runs `nix-store --serve`). So the builder image
must contain:

1. **nix itself** (the `nix`/`nix-daemon` binary + its store) — non-negotiable;
   it's the thing doing the build.
2. **an ssh server** (openssh sshd) listening on a port we publish.
3. **a shell + coreutils** (`/bin/sh`, `env`) — nix shells out during builds.
4. the **builder user** marked `trusted-users` + its authorized key.

**Alpine is therefore a non-starter as a base.** Alpine's whole value is "tiny
libc userland without a package manager's baggage" — but a nix builder's bulk
*is* nix and its store closure, which you'd have to install *into* alpine anyway.
You'd end up with alpine + a full nix install = bigger and weirder than a
nix-native image. Alpine wins for shipping *one static app*; it loses for
"ship nix." **The image's size floor is nix's own closure (~100-150 MB), set by
nix, not by the base distro.** No base choice gets under that.

> Bottom line: "can we make it really small / alpine?" → smaller than the jail
> image, yes (it's just nix+sshd, not chromium+node+agents); alpine-small, no —
> nix's closure is the floor regardless of base.

## 2. Image options (all must satisfy §1)

| Option | What it is | Pro | Con |
|---|---|---|---|
| **Stock `nixos/nix`** | official minimal nix image (nix+bash+coreutils) | zero maintenance | **no sshd** — we'd add openssh + key + ForceCommand at runtime every launch (fragile); unpinned unless we digest-pin; external dep |
| **`LnL7/nix:ssh`** | the canonical "nix as remote builder" image (nix+sshd, ships an *insecure* demo key) | purpose-built for exactly this; well-trodden | third-party, ships a demo key we'd have to replace, unpinned, another external dep |
| **Our own, built with `dockerTools` + published to GHCR** (your pick) | small nix+openssh image from *our* flake: sshd baked, ForceCommand set to `nix-daemon --stdio`, builder key authorized | pinned to our flake.lock (predictable, matches yolo's identity); we control the key + sshd config; distroless-minimal; **built on Linux CI so no Mac ever builds it** | we build+publish+version it (but see §3 — we already do this for the jail image) |

**Your instinct is right that our own gives the most control** — and the con
(maintenance) is smaller than it looks because we already run this exact
pipeline (§3).

## 3. The chicken-and-egg — and why CI dissolves it

The obvious worry: "if we build the builder image *with nix*, it's an
`aarch64-linux` image → needs a Linux builder to build → the very problem we're
solving." **True on a Mac, false in CI.**

We already build `aarch64-linux` OCI images on **free `ubuntu-24.04-arm` GitHub
runners** (`ci.yml:92`, `publish.yml:188` — the jail image is built there today).
Those runners are *native Linux*, so they build the builder image with no
special machinery, and CI publishes it to GHCR. **The Mac only ever `pull`s the
finished image — it never builds it.** No chicken-and-egg.

And we clearly *can* do this: `dockerTools.streamLayeredImage` already produces
our jail image; a nix+sshd builder image is a *much smaller* instance of the same
tool. Publishing to GHCR is a `docker/login` + push step (the publish workflow
already has the shape; GHCR needs `packages: write` permission).

So the builder image is: **`flake.nix` → `packages.builderImage`
(streamLayeredImage of nix+openssh+builder-user) → CI builds on arm Linux →
push to `ghcr.io/mschulkind-oss/yolo-jail-builder:<pinned>` → Mac pulls it.**

## 4. How the builder is used at build time (ties back to existing setup wiring)

Nothing about the *setup* changes — this is the reassuring part. It's still "a
nix remote builder reached over SSH," so the nix.conf `builders = ssh-ng://…`
line, the ssh key, `builders-use-substitutes`, and trusted-users (all already in
`builder.py`) are reused verbatim. Only *what answers the SSH port* changes:

```
build needed (runtime: container, cache miss)
  → runtime already up (AC or podman — the builder only exists when a jail build runs)
  → pull ghcr.io/…/yolo-jail-builder  (once, cached)
  → run it ephemerally, publish its sshd port to 127.0.0.1:<port>
  → host nix daemon: builders = ssh-ng://builder@127.0.0.1:<port>  → runs the build
  → nix copies the result back into the host store
  → tear the container down   (0 idle RAM — the whole win)
```

## 5. The real risk to prototype: does Apple Container host this cleanly?

I won't hand-wave this — it's the one genuinely unproven part, and this session
has repeatedly shown Apple Container to be early-stage:

- **OCI conversion:** AC needs OCI-layout images (we already skopeo-convert the
  jail image for AC) — the builder image needs the same conversion.
- **Published port:** the builder needs its sshd reachable from the host nix
  daemon. AC's port/socket publishing (`--publish-socket`, no `--net=host`) is
  the exact area we've hit limits in. **A host-nix-daemon → AC-container-sshd
  connection is plausible but UNVERIFIED.**
- **podman** is lower-risk here: it's a container in the already-running machine
  VM, and `podman run -p 127.0.0.1:<port>:22` port-publishing is well-worn.

So the honest split: **podman container-builder is low-risk; AC container-builder
is the unproven bet.** If AC can't cleanly host an sshd container with a reachable
port, the fallback for AC users is the QEMU `darwin.linux-builder` (still the
boring, works-anywhere option) — i.e. the runtime picks the builder:
podman→container-builder, AC→container-builder *if it works*, else QEMU.

## 6. Recommendation / plan

1. **Build our own minimal builder image** (`dockerTools` nix+openssh, our key,
   ForceCommand `nix-daemon --stdio`), **published to GHCR from arm Linux CI** —
   your pick, and it's the predictable one. Pin by digest in the CLI.
2. **Wire it via the existing ssh remote-builder setup** — reuse `builder.py`'s
   nix.conf/ssh/trusted-users machinery; only the "start the far end" step
   changes from "boot QEMU" to "run the builder container + publish its port."
3. **Prototype on a Mac to settle the AC question** (§5) before committing AC to
   it — podman first (low-risk), then AC. This is Mac-only and the gating unknown.
4. **Keep QEMU `darwin.linux-builder` as the documented fallback** for AC-if-it-
   won't-host and for anyone not on a container runtime.

## 7. Open questions for the Mac prototype

- Can AC run a long-lived sshd container and let the **host nix daemon** reach
  its published port? (the make-or-break for AC.)
- Latency: ephemeral container up→build→down vs. QEMU warm VM — is the per-build
  container spin-up acceptable, or do we keep one builder container warm per
  session (still 0 idle across sessions)?
- Does the host nix daemon's ssh (running as root, `/var/root/.ssh` or
  `NIX_SSHOPTS`) reach a `127.0.0.1:<port>` container port cleanly on both
  runtimes?
- Image size in practice (nix closure floor) and pull time on first use.

---

### Appendix — facts behind §1 (why nix is mandatory in the image)
- ssh-ng remote build = host daemon runs **`nix-daemon --stdio`** on the builder
  over ssh (NixOS Discourse "restrict builder access through ssh" shows the exact
  ForceCommand: `nix-daemon --stdio` for ssh-ng, `nix-store --serve` for ssh).
- `nixos/nix` official image = nix+bash+coreutils, **no sshd** (the distroless
  multi-stage examples all use it as the *build* stage, adding nothing for ssh).
- `LnL7/nix-docker`'s `nix:ssh` = the canonical remote-builder image = nix+sshd,
  run as `docker run -p 3022:22 lnl7/nix:ssh` — proves the shape, ships a demo
  key we would NOT reuse.
- We already build `aarch64-linux` images on `ubuntu-24.04-arm` runners
  (`ci.yml`, `publish.yml`) → building our own builder image there is no new
  capability, and it sidesteps the on-Mac chicken-and-egg.
