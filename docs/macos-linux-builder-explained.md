# The macOS Linux-builder, explained for a Linux person

You know Linux, not macOS. This doc explains *why* macOS needs a "Linux
builder" at all, the macOS-specific machinery involved (translated to Linux
terms), and the real options for running it — so the choice in
[macos-no-vm-direction.md](macos-no-vm-direction.md) /
[handoff-macos-ondemand-builder.md](handoff-macos-ondemand-builder.md) is an
informed one, not a leap of faith.

---

## 0. Rosetta stone (macOS → the Linux thing you already know)

| macOS term | The Linux analog | Notes |
|---|---|---|
| **launchd** | systemd (as init + service manager) | PID 1 on macOS; supervises all services. |
| **LaunchDaemon** | a system-wide systemd unit (`/etc/systemd/system/…`) | Runs as root, at boot, no user logged in. Lives in `/Library/LaunchDaemons/*.plist`. |
| **LaunchAgent** | `systemd --user` unit | Runs as *you*, only while you're logged in. `~/Library/LaunchAgents/`. |
| **.plist** | a `.service` unit file | XML instead of INI. Describes the service. |
| **`launchctl`** | `systemctl` | `launchctl kickstart -k system/<label>` ≈ `systemctl restart`. `launchctl list` ≈ `systemctl status`. |
| **Virtualization.framework** | KVM + a bit of libvirt | Apple's built-in hypervisor API. Fast, native, no install. |
| **QEMU** | QEMU (same thing) | Portable emulator/VM. On Apple Silicon it uses the HVF accelerator (≈ KVM) so it's not slow, but it's a separate userspace process you can get "stuck inside" — which is what bit you. |
| **nix-darwin** | NixOS, but layered on top of macOS | Declarative *system* config (`darwin-rebuild switch` ≈ `nixos-rebuild switch`). It is NOT nix itself — it's an optional framework built on nix. |
| **Determinate Nix** | a vendor distro of nix (like how Ubuntu ships its own packaged kernel vs. upstream) | A specific nix *installer + daemon* from Determinate Systems, vs. "official/upstream" nix. Different features, same `nix` CLI. |
| **Seatbelt / `sandbox-exec`** | seccomp + namespaces (roughly) | macOS's process sandbox. Weaker/older than Linux namespaces; no cgroups. |

Keep the launchd row in mind: **every *good* option below is "run the builder
as a launchd service."** The bad option (the one you hit) is "run it as a
foreground QEMU process in your terminal."

---

## 1. Why macOS needs a "Linux builder" at all

On Linux this whole problem doesn't exist: you want a Linux binary, you build
it. Done.

On macOS, nix builds **macOS** (Darwin/Mach-O) binaries. yolo's jail image is a
**Linux** (`aarch64-linux`, ELF) image — it has to be, because the container
that runs it is Linux. Nothing on a Mac can *execute* Linux build steps (a build
runs the compiler, shell scripts, `configure`, etc. — all Linux ELF binaries
needing a Linux kernel + glibc). So to build a Linux derivation on a Mac, nix
must hand the work to **something running Linux**. That "something" is a small
Linux **VM**.

> This is the entire reason a VM exists in the macOS story. It is not
> yolo-specific — *any* nix user cross-building Linux on a Mac hits it.

nix models this exactly like its normal **distributed builds** feature: the VM
is presented to the nix daemon as a **remote build machine reachable over SSH**
at `localhost:31022`. When you ask nix to build a Linux thing, the daemon SSHes
into the VM, runs the build there, and copies the result back into your local
`/nix/store`. The `builders = ssh-ng://builder@linux-builder …` line you saw is
just registering that remote machine. (If you've ever set up
`nix.buildMachines` / `/etc/nix/machines` to offload builds to a beefy Linux box
on your LAN, this is the identical mechanism — the "beefy box" is just a local VM
instead.)

**The good news, and why this isn't as heavy as it sounds:** the *builder VM
itself* is a stock NixOS image that lives on `cache.nixos.org`. You **download**
it; you don't build it. So "getting a Linux builder" is a fast substitution, not
a bootstrap. It only turns into an actual build when *your* packages aren't in a
cache.

---

## 2. What actually went wrong for you (the "can't exit" trap)

`nix run nixpkgs#darwin.linux-builder` is the **demo / kick-the-tires** form. It
boots the QEMU VM **in the foreground of your terminal**, on a **serial
console**, and auto-logs-you-in as the `builder` user.

- Your keystrokes go *into the guest*, not to your Mac shell.
- Ctrl-C is trapped by the guest shell; Ctrl-D logs the `builder` user out, but
  the guest's getty just auto-logs-in again → the login loop you saw.
- **Escape:** `Ctrl-a` then `x` (QEMU's "kill the VM" for a serial console), or
  from another terminal tab `pkill -f qemu-system`.

**Nobody runs it this way for real.** It's the "does it work?" form, like running
a daemon in the foreground with `-D`/`--foreground` on Linux to watch it before
you `systemctl enable` it. The real deployments all run it as a **launchd
service** (§3), where there's no terminal to be trapped in.

---

## 3. The real options

All four "keep it" options below end in a launchd service. They differ in *who
writes the plist* and *what hypervisor* runs the VM.

### Option A — nix-darwin one-liner *(the community-canonical answer)*

If you run (or adopt) **nix-darwin**:

```nix
# in your darwin configuration, then: darwin-rebuild switch
nix.linux-builder.enable = true;
nix.settings.trusted-users = [ "@admin" ];   # lets your user hand the daemon the builder
nix.linux-builder.ephemeral = true;          # optional: wipe the VM's disk each restart
# resource knobs (defaults: 1 CPU, 3 GB RAM, 20 GB disk):
nix.linux-builder.maxJobs = 4;
```

`darwin-rebuild switch` installs a LaunchDaemon `org.nixos.linux-builder`,
installs the SSH key **as root during the rebuild** (no interactive per-build
sudo), and registers the builder with the nix daemon. Headless, starts at boot,
no terminal.

- **Linux analogy:** like `nixos-rebuild switch` dropping in a `systemd` unit +
  `nix.conf` line for you.
- **Pro:** the blessed, best-documented path; one line; fully declarative.
- **Con:** requires **nix-darwin** — a whole declarative-system framework. If the
  user isn't already running it, that's a *big* thing to make them adopt just for
  a builder. **This is the deal-breaker for yolo's audience** (a user with a
  plain nix install, no nix-darwin).
- **Con:** default is **resident** (`KeepAlive`) — the ~3 GB VM stays up whether
  or not you're building.

### Option B — Determinate Nix's built-in native builder *(zero VM management)*

**Determinate Nix** (the vendor distro; ≥ 3.8.4) has a Linux builder **built into
its daemon**, using **Virtualization.framework** directly (not QEMU). Advertised
as working with **no extra setup** — no `nix run`, no plist, no VM to manage.

- **Linux analogy:** your distro's nix package shipping the builder as a
  first-class daemon feature, vs. you assembling it from upstream parts.
- **Pro:** the least work of all — nothing to install or babysit; Apple's
  hypervisor is faster/lighter than QEMU.
- **Con:** **Determinate-only**, and currently an **access-gated rollout** (sign
  into FlakeHub, email their support to be allowed in). We can't assume a yolo
  user has it. Good to *detect and defer to* if present; can't *depend* on it.

### Option C — install the launchd plist ourselves *(fits yolo's audience)*

This is Option A's *mechanism* without the nix-darwin *framework*. We write
`/Library/LaunchDaemons/org.yolo-jail.linux-builder.plist` (runs
`create-builder`/`linux-builder-start`, `wait4path /nix/store && exec …`),
install the SSH key, and wire the daemon's `nix.conf` builder line + ssh_config —
**all inside the one `yolo builder setup` sudo we already prompt for**. Then
`yolo builder {start,stop,status}` are thin `launchctl` wrappers.

- **Linux analogy:** shipping our own `systemd` unit + enabling it, instead of
  requiring the user to adopt a config-management framework.
- **Pro:** works on any Mac with plain/Determinate nix, no nix-darwin; we own the
  UX (doctorable via `yolo check`, off by default).
- **Con:** we maintain a plist (small, stable — it mirrors the upstream one).
- **This is the right primitive for yolo.** See §5.

### Option D — the foreground `nix run` *(what you hit — NOT a real option)*

Listed only to name it: the demo form. No plist, foreground QEMU, terminal trap.
Fine for a 30-second "does my Mac boot the VM?" check; never for actual use.

### Non-options (for completeness)
- **Colima / Docker-VM as the builder** — it's a Docker VM, not a nix builder;
  you'd install nix *inside* it and copy closures. Strictly more setup than any
  of A–C. Already rejected in [happy-path-principle.md](happy-path-principle.md).
- **A remote Linux box on your LAN** — works (it's the same ssh-remote-builder
  mechanism), but requires you to *own and run* a Linux machine. Fine as a
  power-user escape hatch, not a default.

---

## 4. The one axis that's genuinely a decision: resident vs. on-demand

Every "keep it" option installs a launchd service. The remaining question is
**when the VM runs**:

| | Resident (`KeepAlive=true`) | On-demand + idle-stop |
|---|---|---|
| What it means | VM boots at login and stays up | VM starts only when a build needs it, stops after N min idle |
| RAM when idle | **~3 GB held 24/7** | **0** |
| First build of a session | instant (already warm) | +a few seconds (boot) |
| Precedent | what nix-darwin & everyone does | rare — most people just eat the 3 GB |
| Effort | trivial (it's the default) | more (kickstart-on-build + an idle watchdog) |

- **Resident** is the standard, simplest choice. Its cost is exactly the pain
  yolo set out to avoid on macOS: RAM permanently held for a thing used minutes a
  day.
- **On-demand + idle-stop** is the yolo-ideal ("as fast as Linux, no stolen
  RAM"). launchd *can* do it (demand-started service + a watchdog that stops the
  VM when no build has run for a while), but it's the less-trodden path.

This is the same open decision recorded in
[handoff-macos-ondemand-builder.md](handoff-macos-ondemand-builder.md) — now with
the correct implementation primitive (a launchd plist) identified.

---

## 5. What this means for the code we already have

Heads-up for continuity: the current `src/cli/builder.py` **reinvents a worse
Option C.** It runs the builder as a **detached `Popen` of `nix run …` + a PID
file**, and needs a `first_boot_interactive()` hack to answer the SSH-key sudo.
That approach is fighting the platform:

- The "can't exit / babysit" failure mode **only exists because we run the
  foreground `nix run` form.** A launchd daemon has no controlling terminal — the
  trap simply cannot happen.
- The interactive-first-boot sudo hack **disappears** under launchd: the daemon
  runs as root, so the SSH key is installed once when the plist is installed (the
  single `yolo builder setup` sudo), not as a prompt mid-build.

**Recommendation: rework `builder.py` around a launchd plist (Option C).** It
deletes the trap *and* the first-boot hack, and `yolo builder {start,stop,status}`
become thin `launchctl` wrappers — which is what the design originally sketched
before the detached-`Popen` detour. The resident-vs-on-demand choice (§4) is then
just `KeepAlive` in the plist vs. kickstart-on-build + a watchdog.

---

## 6. TL;DR

- macOS needs a Linux VM *only* to build Linux derivations — it's nix's normal
  ssh-remote-builder mechanism pointed at a local VM. The VM image is downloaded,
  not built.
- **Never** run the foreground `nix run` form (that's the terminal trap). Escape
  it with `Ctrl-a x`.
- The right shape is a **launchd service** (systemd's cousin). Three ways to get
  one: nix-darwin one-liner (needs nix-darwin), Determinate's built-in (needs
  Determinate + access), or **we install the plist ourselves** (works for
  everyone — the yolo answer).
- One real decision left: **resident** (simple, holds 3 GB) vs. **on-demand +
  idle-stop** (yolo-ideal, more work).
- Our `builder.py` should move to the launchd-plist model; it eliminates the trap
  and the first-boot hack.
