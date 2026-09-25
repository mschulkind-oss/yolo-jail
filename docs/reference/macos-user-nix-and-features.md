---
status: current
verified: 2026-09-09
verified_commit: d8cf1cf8
covers:
  - internal/macosuser/
  - internal/darwinpkg/
  - internal/entrypoint/darwin.go
  - internal/cli/run/macoshomeoverlay.go
  - internal/cli/run/loopholeinert.go
tags: [macos-user, seatbelt, nix, darwin, backend-parity, packages]
summary: "The macos-user backend as built: nix materializing packages: natively into a buildEnv profile, the native bootstrap running the same pure generators the container boot runs, and the surface that is off — grouped by whether it is structurally impossible, absent-but-warned, or fixed. Includes the two permanent differences of a copied home overlay versus a bind mount, and the refusals (rlimits for `resources`, per-workspace homes) that are terminal answers rather than pending gaps."
---

# macos-user — native nix, and the surface a container would have given you

**Status:** CURRENT as of 2026-09-09, verified against `d8cf1cf8`.

`macos-user` runs the agent as a **real macOS process** — the hidden `_yolojail` account,
confined by an Apple Seatbelt profile — with **no container, no VM and no OCI image**. nix's
job here is not to build an image: it materializes the tool closure as native darwin binaries —
for the running Mac's own system double, Apple Silicon or Intel — and the launch prepends their
store `bin` to the agent's PATH.

Almost everything absent on this backend follows mechanically from *no container*: a feature
implemented as a container flag or a bind mount has no host to attach to.

| Component | Lives in |
| :--- | :--- |
| The launch orchestrator, its gates and its warnings | `internal/macosuser` (`RunMacosUser`, `orchestrator.go`) |
| The run plan and its self-checks | `internal/macosuser` (`BuildRunPlan`, `RunPlan`, `PlanInvariants`) |
| Sandbox identity, paths, and the shared root | `internal/macosuser` (`SandboxUser`, `SandboxHome`, `SandboxGroup`, `SharedRootDefault`, `SandboxPath`) |
| The Seatbelt profiles | `internal/macosuser` (`SeatbeltProfile`, `ancestorLiterals`, `SeatbeltCaptureProfile`) |
| Native package realization | `internal/darwinpkg` (`Materialize`, `ProfilePaths`, `BuildFloorProfileArgv`, `skippedNames`); `macosuser.Deps.MaterializeDarwin` is the seam the orchestrator calls it through |
| The native bootstrap, and the home-overlay install | `internal/entrypoint` (`RunDarwinBootstrap`, `DarwinEnvFrom`, `InstallHomeOverlay`) |
| Host-side composition of the delivered content tree | `internal/cli/run` (`buildMacosHomeOverlay`) |
| The inert-feature reports | `internal/cli/run` (`loopholeinert.go`), `internal/macosuser` (`orchestrator.go`) |

**Reads with:** [`nix-across-backends.md`](nix-across-backends.md) (the same split from nix's
side), [`../design/backend-parity.md`](../design/backend-parity.md) (the sweep this backend's
warnings came out of, and the census that would make a silent drop unwritable),
[`macos-no-vm-direction.md`](macos-no-vm-direction.md) (why the backend exists),
[`../design/macos-user-build-step-threat-model.md`](../design/macos-user-build-step-threat-model.md)
(the host-side nix build as an attack surface),
[`../guides/macos.md`](../../userguide/guides/macos.md) (user-facing setup).

---

## The one thing to internalize

On the container backends every host↔jail seam is a **bind mount**: the workspace, the home
overlay, `/nix`, the host-service socket directory, cache relocations. Here there are **no
bind mounts of any kind**.

- The "workspace" is the **actual host directory**, reached through a shared-group ACL rather
  than a mount. It must be **neutral ground** — never inside any user's home; a plan
  invariant rejects a home-directory workspace and names the shared root to move it under.
- "Home" is the **real** `/Users/_yolojail`, and it is a constant with no workspace
  component — so the account home is the **machine** tier. The **workspace** tier is there
  too, as symlinks: every directory the container backends bind from `<workspace>/.yolo/home`
  is a symlink from the account home into that same sidecar, so a project's agent state lives
  where every other backend puts it
  ([`macos-user-home-tiers.md`](macos-user-home-tiers.md)).
  What the layout does **not** link stays machine-wide: credentials, `~/.cache`, the mise store.
- Nix produces a `buildEnv` profile on the host, not an image.

## Three states, and never blur them

Every absence on this backend is one of three things, and the distinction is this document's
whole value:

| State | Means |
| :--- | :--- |
| **Fixed** | the mechanism works here. Nothing to work around. |
| **Warned-but-absent** | still absent, but the launch **says so** on stderr. A wiring or design gap; the warning is the interim contract. |
| **Structurally impossible** | there is no macOS mechanism to implement it against. The warning is the terminal state, not a stopgap. |

> [!WARNING]
> **"Absent and silent" is the defect; "absent and loud" is an honest backend.** A sweep
> across the non-podman backends found ten features that rendered, validated and read exactly
> as they do on podman and then did nothing here, with no message — a user could install and
> select a pack whose entire purpose is a loophole, watch it install, and get a successful
> launch that ran none of it. Anything new that a container implements with a flag or a mount
> must either work here or say it does not, at the launch.
> [`../design/backend-parity.md`](../design/backend-parity.md) calls the class a *silent drop*.

> [!CAUTION]
> **A Linux jail cannot verify this backend at all, and podman-in-podman cannot exercise it.**
> Everything here is pinned by unit tests, mutation checks and plan invariants on Linux; a Mac
> session is what closes the remaining question for any behaviour that touches the sudo stage
> commands, the shared-root ACLs, or a cross-uid write. Treat a green Linux test as evidence
> about the *plan*, never about the machine.

## How nix works here

### What nix produces

Not an image. A `buildEnv` profile realized natively for this Mac, whose `bin` the launch
prepends to the agent's PATH (plus a `pkg-config` path when that directory exists). **`yolo`
never execs the build output** — it reads the out-path and builds a PATH.

What is in the profile is the non-container **floor plus** `packages:`, not the declared
packages alone: a backend with no baked image has no other way to reach mise, node, git or
ripgrep. The floor joined the profile after this doc's verified commit, which is why the older
"`packages:` only" reading was true once and is not now.

There is therefore **no Linux builder and no image download** in this backend's launch. Those
exist only for the container runtimes.

### The two invocations

Both run **on the host, as the invoking (admin) user, before the sandbox is entered**, from
the flake directory:

1. **Build the profile** and print its store out-path.
2. **Best-effort eval** of the "no darwin build" list.

Why each flag on the build matters:

- **`--impure`** — the flake reads `packages:` from the environment, the same contract the
  image build uses. Without it that read returns empty and **no declared package is built**.
  This is structural, not a preference.
- **`--accept-flake-config`** — makes nix honor *this flake's own* declared binary cache.
  Without it nix prints an "ignoring untrusted flake configuration" notice and forces a
  from-source darwin build even when a cached closure exists. A trusted nix user still gates
  whether the substituter is actually consulted; nothing mutates a system `nix.conf`.
- **`--extra-experimental-features 'nix-command flakes'`** — so the invocation works whatever
  the host's `nix.conf` says.
- **`--print-build-logs`** — a from-source darwin build is long, and silent progress on a
  ten-minute build reads as a hang.

**The second invocation is advisory only**, and bounded by a timeout that returns nothing on
expiry or error. The build already drops packages with no darwin build — the flake filters
before the buildEnv — so it succeeds whether or not the eval runs. The eval's sole job is to
*name* the dropped packages. A `nix eval` can hang on an uncached nixpkgs, which is why it is
bounded; the only consequence of failure is a less specific message.

### Ordering, and what aborts

The orchestrator's sequence is load-bearing in two places:

1. **A `--dry-run` short-circuit first**, before the platform gates, so the plan and its
   invariants can be printed and inspected on Linux CI.
2. **Cheap gates before the expensive build** — macOS, not-root, `sandbox-exec` present,
   sandbox user exists. A long build that then fails a precondition wastes the build.
3. **Materialize.** A failure **aborts** with an actionable message rather than launching a
   half-provisioned sandbox. Then resolve the host's nix client for the sandbox, and say
   whether one was delivered ([nix inside the sandbox](#nix-inside-the-sandbox)).
4. **Build the plan, then run the plan invariants** — including the acceptance-bar guard that
   every darwin store `bin` dir actually reached the launch PATH.
5. Install the Seatbelt profile, stage the yolo binary, bootstrap, launch.

**A declared package with no darwin build is fatal.** The jail does not start, because a
package the user declared would have been silently missing inside it — and the two cases that
matter are indistinguishable from a warning: a **typo** (an unknown attribute is "skipped")
and a package that genuinely has no darwin build. Either way the failure would surface later
as a command that mysteriously does not exist.

> [!NOTE]
> The nix **eval** still does not abort — the flake filters the unavailable set and builds
> only what is available, which was the original in-code objection to erroring. The error is
> raised **host-side, after the eval**, from the returned skip list. That ordering is the whole
> trick: nix stays green and the CLI decides. Entries excluded by a `platforms` declaration are
> already gone before the build, so what remains is by construction a package declared for
> *this* platform and not delivered.

### Requirements

`yolo check` verifies: **nix on PATH** (native darwin nix — without it the agent gets none of
its declared tools); a **flake lock at the repo root**, which pins nixpkgs so darwin packages
are reproducible across machines; and a **trusted nix user**, which is a warning rather than a
failure — a non-trusted user can still build from cache, and being trusted is what lets the
flake's own substituter actually be consulted.

Note what the lock buys: *declarative* reproducibility (the same nixpkgs attributes), **not**
byte-identity with the Linux jail. These are darwin builds.

### nix inside the sandbox

> [!IMPORTANT]
> **Added 2026-09-24; MEASURED on a Mac the same day** — all five subtests of the test below passed on
> GitHub's `macos-latest` runner ([run 36050645052](https://github.com/mschulkind-oss/yolo-jail/actions/runs/36050645052)), whose nix is a multi-user daemon install. The mechanism below is pinned on Linux by
> `internal/macosuser/hostnix_test.go`: the probe's arms, the real symlink resolution and
> socket check against a temp store, the plan's three PATH copies, the session env file, and
> the orchestrator call site. Two things only a Mac can catch: the probe's two production
> locations (`/nix/store`, the daemon socket path) and what `RealDeps`' probe answers there.
> The claim that the sandbox really runs it — `command -v nix`, `nix --version`,
> `nix eval --expr 1+1` → `2` with no flags, and `nix store info` completing a daemon handshake
> **as the sandbox account** (`nix eval` of a constant opens no store connection, so it alone
> would prove nothing about the daemon) — is `TestMacosUserNixIsTheHostDaemonClientWithNoFlags`
> ([`integration/macosusernix_test.go`](../../integration/macosusernix_test.go)), which runs
> only on the `macos-user` CI job. Still INFERRED: the Determinate and nix-darwin symlink layouts,
> which that runner does not have.

The agent runs natively and sees the host's real `/nix` — no mount, and no opt-in toggle. What
it lacked until 2026-09-24 was a `nix` **on its PATH**: the sandbox answered
`nix: command not found` on the one backend that needs a host nix for every launch. Nothing
about confinement was in the way. A hardware probe on 2026-09-16
([`setup-support-gaps.md`](../plans/setup-support-gaps.md#51-what-is-now-measured), rows 10-12)
found that `connect(2)` to the daemon socket survives the profile's write-deny, that
`nix build nixpkgs#hello` returns 0 inside `sandbox-exec`, and that an `--out-link`'s indirect
GC root is created, because the daemon writes it as root.

**What a launch delivers.** After the floor build, the launcher resolves the `nix` on the
launching user's PATH — the same one the build just ran — through its symlinks, and puts that
**store `bin` directory** on the sandbox PATH, after the floor's `bin` (so a `packages:` nix
still outranks it) and ahead of the system directories. It rides the same list as the floor,
so it reaches the agent's PATH, the provisioning stage's PATH and the bootstrap's
`YOLO_DARWIN_LOGIN_PATH` together, and the plan invariants that check the floor reached the
first and the last check it too. Two variables join the session env file with it:

- `NIX_REMOTE=daemon` — the sandbox account is not root and cannot write the store, so nix's
  own default would choose the daemon anyway; spelled so the answer never depends on that
  guess.
- `NIX_CONFIG=extra-experimental-features = nix-command flakes` — so `nix eval`/`nix build`
  work with no flags whatever the host's `/etc/nix/nix.conf` enables. The `extra-` prefix
  **adds** to the host file's list rather than replacing it; the sandbox reads that file,
  because the Seatbelt read denials do not cover `/etc`.

Both are **defaults**. A `NIX_REMOTE` the user set (`env_sources`, or the launch's own
sandbox env) wins whole. A `NIX_CONFIG` the user set is **kept**, and yolo's line is appended
after a newline, unless one of the user's lines already sets `experimental-features` or
`extra-experimental-features` — a user who names the features, even to turn them off, is left
exactly as written. The newline survives the trip: the session env file is **sourced** by
`/bin/sh` with each value single-quoted, and the appended line does not start `export `, so
the file still reads as setting `NIX_CONFIG` once.

**Why the host's client, not a nix in the floor.** Every nix the sandbox runs is a client of
the host's daemon. A floor nix would be whatever version yolo's flake pins, drifting from the
daemon with every host upgrade and every flake bump. The resolved client is the one that talked
to this daemon a moment earlier, to build the floor.

**Why the resolved store directory, not the profile directory it was found through.** On a
stock multi-user install `nix` is found as `/nix/var/nix/profiles/default/bin/nix`, under
nix-darwin as `/run/current-system/sw/bin/nix`, and per-user as `~/.nix-profile/bin/nix`. Each
is a symlink into `/nix/store/<hash>-nix-<version>/bin`. The profile directories carry whatever
else was installed into the profile, and the per-user one sits under `/Users`, which the
profile denies. The store directory holds only the nix package's own binaries, cannot be
written, and needs **no new Seatbelt allowance** — the floor already runs from `/nix/store`.
(INFERRED, from how the three installers lay out their profiles; the resolution is measured
only on Linux, where `/bin/nix` resolved to its store directory.)

**When the sandbox gets no nix — and the launch says which.** Nothing is delivered, and the
launch prints `nix is not available inside the sandbox:` with the reason, when:

- **there is no daemon socket** at `/nix/var/nix/daemon-socket/socket` — a **single-user**
  install, whose store belongs to the launching user. The sandbox account can neither write it
  nor reach a daemon, so a `nix` on its PATH would fail at its first store operation;
- the client **resolves outside `/nix/store`**, the one nix location the sandbox is known to
  read;
- or there is **no `nix` on the launching user's PATH** at all — unreachable on a real launch,
  which refuses earlier because the floor build needs that same `nix`.

A delivered client is announced the same way (`nix: the host's client (<dir>) is on the
sandbox PATH`). `--dry-run` resolves nothing and names no client: it builds no floor, and
the plan it prints is pure.

**The one residual, INFERRED and unmeasured:** the store directory is live only while
something roots it — normally the host profile generation that installed it. Upgrading the
host's nix and then collecting the old generation while a session runs deletes the client out
from under it; the next launch resolves the new one.

### The build step is the unconfined step

The host-side nix build runs as the invoking user, with inputs — the declared package list and
the flake directory — that a prior agent session could have written. That trust-boundary
inversion is analyzed in
[`../design/macos-user-build-step-threat-model.md`](../design/macos-user-build-step-threat-model.md)
and is not restated here. **Read it before touching the repo-root resolution or the darwin nix
flags.** The config-change approval prompt is the mitigation for a poisoned package edit, and
it is gated on this backend's own arm.

## What the native bootstrap carries

The bootstrap self-execs as the sandbox user and runs the **same pure generators the Linux
entrypoint runs** — they are already pure functions of an environment struct, so pointing that
struct at the sandbox user's real macOS paths makes them correct natively. The translation is
not mechanical, though, and lives in exactly one place (`DarwinEnvFrom`): three rebindings the
container boot does not make — the real workspace instead of the literal container path, the
system bin dir for shim real-binaries, and BSD rather than GNU `stat`.

> [!WARNING]
> **Do not assemble that environment by hand at a call site.** A caller that did would be a
> second implementation of the contract, free to drift — and it was one, briefly: the first
> darwin harness built its own, left the workspace at the container default, and every
> generator writing a workspace sidecar failed on a read-only-filesystem error.

**A generator failure is fatal here too**, with its own collect-then-abort. This path is easy
to miss: it has its own generator sites, and its caller used to print "bootstrap ok"
unconditionally.

So the per-workspace config surface is preserved: blocked-tool blockers, mise config, MCP and
LSP *config*, pack selection and every pack surface and hook, resolved env sources, git
identity. Two macOS-only writers run that the Linux boot does not: the unified-logging helper,
and the **login-rc PATH re-prepend**, which re-asserts the sandbox PATH *after* macOS's
`path_helper` reorders it.

The Linux-only boot steps — the loader cache, cgroup delegation, port forwarding, the daemon
supervisor, the container bootstrap and venv scripts — are deliberately **not** run. They are
no-ops or nonsensical on a native user.

> [!WARNING]
> ⚠ **The daemon supervisor is the one item in that list that is NOT a no-op**, and it was
> carried there with no id and no rationale beyond "nonsensical on a native user". It is the
> process that runs every `jail_daemon` a selected pack declares, and the shipped default
> declares three: a bare `"packs": ["claude"]` joins `openai-auth` and `wire-bridge` through
> `needs`, so `yolo-jaild oauth-terminator`, `yolo-jaild openai-auth-adapter` and
> `yolo-jaild wire-bridge` are all declared and none runs. Since 2026-09-18 the launch
> **declines each by name** rather than saying nothing
> ([`jail-daemon-on-macos-user-plan.md`](../design/jail-daemon-on-macos-user-plan.md)).
> Running them is blocked on two unfiled rulings: how a declared `jail_daemon.cmd` resolves on
> a backend with no image (`yolo-jaild` is not built for darwin), and whether such a child runs
> under the Seatbelt profile.

### Content delivery is a copy, not a mount

Skills and briefings **are** delivered. The host composes the same trees and bodies the
container path composes and lays them out **by destination**; the launch stages that tree and
names it in an environment variable; the boot copies it over the sandbox home. It runs **last**
among the writers, because the per-surface writers above it create the agent home directories
it copies into.

Two properties are **permanent differences, not pending gaps**:

- **The delivered files are WRITABLE**, where a container's bind is `:ro`. An agent here can
  edit its own skills, and the next launch overwrites them again. That is recorded in the
  launch warning rather than papered over.
- **The destination is per-workspace**, and used to be the second permanent difference. It
  is not one any more: skills and briefings land under `<workspace>/.yolo/home` through the
  layout symlinks, so a second workspace launching concurrently no longer replaces what this
  one delivered.

> [!WARNING]
> **The overlay overwrites per destination subtree; it must not merge.** The overlay is
> authoritative for the paths it contains, exactly as a bind mount is — a skills directory a
> pack stopped shipping has to **disappear** from the home, and a merge would keep serving it
> forever. Everything else in the home (credentials, history, anything the agent wrote) is
> untouched, because the overlay simply does not contain those paths.

> [!WARNING]
> **The tree carries no schema, and adding one would be a second mount assembler.** The host
> laid it out at the destinations the container would have mounted; the boot walks it and
> writes files. Any mapping logic in the copier would be a second implementation of the mount
> assembler's, which is exactly the drift this repo removes elsewhere.

A **missing** overlay at boot is a warning, not a boot failure: the launch may have raced a
teardown, and an agent is better off starting with no skills than not starting.

### MCP wrappers are skipped, and say so

No MCP wrapper scripts are generated here. Their bodies are **Linux-absolute** — a
`/usr/bin` chromium, a `/bin` node, a fontconfig path — with no platform guard, and this
backend bakes no image, so on macOS all three paths are simply absent. Generating them anyway
put three executables in the sandbox home that fail the moment anything execs one.

**Skipped, not ported.** A darwin variant would have to find a browser, a node and a
fontconfig on a machine yolo did not provision, and guess wrong on most of them. An absent
wrapper that says so beats a present one that lies. A configured MCP **preset** therefore
warns that it is not delivered, and points at configuring the server directly.

## The surface that is off

Grouped by *why*. This is not an inventory — the per-feature matrix this replaced drifted
twice, and the authority for a full comparison is the per-backend census in
[`../design/backend-parity.md`](../design/backend-parity.md).

### No bind mounts

A whole class of container features has no attachment point.

- **`writable_home_dirs`** — **not applicable.** The knob carves writable subpaths out of an
  otherwise-read-only home mount; here the home is natively writable, so the concept has no
  target.
- **`cache_relocations`** — **structurally impossible, and warned.** Relocation moves a cache
  subdirectory onto other storage *by bind-mounting it in*. There is no mount, so a configured
  relocation prints a per-key warning naming the subdirectories that stay put.
- **`per_side_paths`** — **structurally impossible, and warned.** It gives the host and the
  sandbox *different contents at the same path*, which is a mount-namespace capability.
  Seatbelt can deny a path; it cannot fork one. This matters more than it looks, because a
  common dependency directory is in the default shadow set on the container backends — so
  every such workspace gets a protection here that is absent, with nothing in the config
  hinting at the difference.
- **`workspace_readonly`** — **enforced.** It *was* silently inert, and the fix was a wiring
  gap rather than an impossibility: the Seatbelt profile is a write deny-list with re-allows,
  so the policy is expressed there. This is a behaviour change for anyone who set the key on
  this backend and had been writing to the workspace.
- **pack `state` at `scope: workspace`** — **fixed.** Every other backend gives each workspace
  its own copy by mounting a per-workspace host directory at each path; here each one is a
  **symlink** into `<workspace>/.yolo/home`, the same host directory podman binds from. It was
  machine-wide and warned about — the mirror image of a known Apple Container defect, where the
  *machine* tier collapsed into the per-workspace one — until the home layout landed, and the
  warning went with it.

> [!WARNING]
> **"Just symlink the cache yourself" does not work here.** The session Seatbelt profile
> denies writes everywhere and re-allows only the workspace, the sandbox home, and the
> temporary directories — and separately denies reads under `/Volumes` except the boot volume.
> So a host symlink onto another disk resolves fine for the invoking user and is **refused
> inside the sandbox**, which is the *worse* failure: the cache silently stays on the boot
> volume, the one outcome the feature exists to prevent. Any doc that says otherwise about this
> backend is wrong; this one is the authority.

> [!NOTE]
> **The sandbox used to enforce workspace isolation one layer down and leak it through the
> shared home, and that hole is closed — with no profile change.** The profile denies reads
> under the users root and re-allows the workspace's own subpath plus each *intermediate*
> directory as a bare literal, chosen precisely so a sibling checkout beside the workspace
> stays denied. The leak was that the same content was reachable a second way: another
> workspace's session transcripts sat under the sandbox home, which the profile allows
> wholesale. They now sit under **their own workspace**, which the literal-ancestor rule
> already denies — so the layout enforced the boundary the profile was already drawing.

### No cgroups, and no VM to size

**`resources`** (cpu, memory, pids) is **not enforced, and warned.**

> [!WARNING]
> **A fix is refused, not pending, and rlimits are the trap.** `RLIMIT_AS` is **address space,
> not RSS**, so it is not what a memory cap means — capping it breaks JITs and the Go runtime,
> both of which reserve far more virtual address space than they ever fault in. `RLIMIT_NPROC`
> is **per-user, not per-process-tree**, so on this backend it would collide across concurrent
> sessions, every one of which runs as the same account. A cap the user believes in but that
> does not hold is worse than a documented absence, so this warns and will keep warning.

`yolo-cglimit` and the cgroup-delegate daemon are Linux-only and simply absent.

### Networking, devices, GPU

A native process runs on the host's real network, so the **network modes** have no namespace
to switch and are not applied, and a host service is reachable directly. **Port forwarding**
lives in the container launch path this backend returns before. **GPU** is unavailable on every
macOS backend (Metal, no CUDA or ROCm), and **devices** and cgroup rules are Linux kernel
features.

### Loopholes: mostly moot, and the framework ports better

⚠ **This section said "no loophole host service starts here" and that is RETRACTED (2026-09-18).**
The heading still stands — each shipped loophole really is mostly moot natively, for the
reasons listed below — and an incoming link names it
([`macos-revival-and-distribution-plan.md`](../plans/macos-revival-and-distribution-plan.md)).
It described an arm that returned from `Run()` above the spawn boundary, and that stopped being
true when the lifecycle was generalised on 2026-09-17: this backend now goes through the same
`startLoopholesDisclosed` wrapper a container launch does, starts **every** admitted host
daemon, prints the same "This launch runs pack code on your machine" disclosure before it does,
publishes each endpoint and ACL-grants it to the sandbox account. Measured: a bare
`"packs": ["claude"]` publishes both `claude-oauth-broker.endpoint` and
`openai-auth-broker.endpoint`. The credential service is fail-closed here — a launch whose
OpenAI loophole is active and whose broker did not start is refused.

**What is inert here is the JAIL half.** No `jail_daemon` runs on this backend (see the warning
above), so a loophole whose work happens inside the jail does nothing however healthy its host
daemon is. The launch says both things: one `Declined:` line per jail daemon, and — on the
**platform** axis only — one line per loophole this machine cannot run at all (`audio`,
`journal`, `host-processes` and `cgroup-delegate` declare `platforms: ["linux"]`). The
**briefing** is gated on the backend too, so it does not advertise these under a heading
reading "host capabilities wired into this jail".

> [!WARNING]
> **That gap survived for months behind a test that pinned the callee, not the call site.** The
> test called the report function directly for both backends, so the macos-user half asserted a
> line no launch could produce. Ask of any new backend-conditional report: *does this test fail
> if I delete the call site?*

"Not wired" means something different for each shipped loophole, because a loophole is
machinery for punching a *specific* thing through a container boundary — and a native process
has no boundary to punch:

- **Host audio** is **moot**. The loophole bind-mounts host sound sockets and a device into a
  Linux container; a native process reaches the host's audio directly, subject to the Seatbelt
  profile and the OS privacy prompts.
- **The host-process view** is **moot, and if anything the native side is *less* restricted.**
  The loophole exists to give a *contained* jail an allowlisted read-only window via a daemon;
  a native process sees host processes directly, because the profile grants process info. The
  flip side matters: the visibility allowlist is a container-only control, so here the agent
  sees the **full** host process table. That is a widening of the surface, not a missing
  feature.
- **The Claude OAuth broker** is **mostly moot; leave it off.** It bundles two jobs. *Keeping
  one shared credentials file* is **free** here — every session shares the one real home, hence
  one real credentials file, so the shared home *is* the shared-credentials mechanism.
  *Serializing the refresh call* only bites with multiple **concurrent** sessions, and porting
  the interception is genuinely hard natively: there is no host-entry injection, and redirection
  would need root-global DNS or hosts control. ⚠ **Two clauses here are retracted (2026-09-18).**
  The broker's host daemon is no longer "off": it starts like every other host daemon on this
  arm. And `EndpointGrantCommands` — two ACL entries letting the sandbox user read one published
  endpoint file and traverse, not list, the directory holding it — is **called on every launch**
  (`BuildRunPlan` stages it for each endpoint the launch carries, and `PlanInvariants` refuses a
  plan that names an endpoint without one). It was dead-until-needed; it is needed. What is
  still true is the conclusion: the *interception* does not port, and the TLS terminator that
  would perform it is a `jail_daemon` this backend declines — so refreshes are not serialized
  here even with the daemon up.

**The loophole framework itself is worth keeping, and this backend is arguably a better fit
than a container.** A loophole is "a host-side daemon mediates the jail's access to a
resource"; nothing about that needs a container, only the *transport* differs. A native jailed
process reaches host loopback sockets and ports **directly**, and yolo already injects the
launch environment, so a loophole collapses to *host daemon on a loopback socket, plus a launch
env var pointing the jail's clients at it* — no mount, no redirection plumbing. An
access-scoping or auditing proxy fits that cleanly. The **only** loophole shape that does not
port cheaply is transparent interception of an opaque client that ignores proxy variables and
pins its host.

### The container-launch preamble

The macos-user arm returns before the container launch function, so everything living only in
there is skipped. Most of it is irrelevant — image load, stale-container reaping, the workspace
lock. Two are not, and both were fixed by moving the work rather than by warning:

- **Pack `launch` contributions** were injected inside the container function, so on this
  backend a `launch` contribution **did nothing at all**. What ranked it a must-fix rather than
  a warning is that the shipped bypass flags failed *differently*, so no one symptom led back
  to the cause: a flag declared as a plain `launch` contribution had no config half to fall
  back on and was a total drop, while one declared as the launch half of an autonomy
  contribution kept its config half rendering, so it degraded to a *partial, silent* downgrade
  of the autonomy the user asked for. (copilot's `--yolo` was the plain-`launch` case. It moved
  under `autonomy` on 2026-09-12, which changes where the flag is declared and not whether the
  hoist is needed.) Injection is now hoisted above the backend dispatch and threaded in as a
  parameter, the same move pack staging made. Nothing downstream ever recovered these flags:
  both in-jail launcher templates end by exec'ing the real binary and never read the
  contributions.
- **The config-change approval prompt** is called on this arm itself, before the launch. It
  sits in the arm rather than hoisted above the dispatch **on purpose**: the container arm gates
  the *fresh-launch* path only, because attaching to a running jail deliberately skips the check.
  This backend has **no attach** — every invocation is a fresh sandbox — so the arm's own call
  site is where the two backends actually agree. A dry run is exempt: it launches nothing, so
  there is nothing to approve, and refusing would only hide the diff the user asked to inspect.

⚠ **This section listed `lsp_servers` as "absent and warned" until 2026-09-12, and that is
retracted** — the installer it said this backend "deliberately does not run" now runs. The
macos-user launch has a **provisioning stage**: a
Seatbelt-confined step between the bootstrap and the agent that runs `mise install` and the
generated bootstrap script, so `mise_tools` install here the way they do everywhere else
([`macos-user-provisioning.md`](macos-user-provisioning.md)). (`lsp_servers` installed that way
too until 2026-09-25, when the LSP install recipes were deleted on every backend; the key now
renders config only, here as everywhere.)
The launch warnings for both keys were retired with it, on the rule this page applies
elsewhere: a warning describing a closed gap teaches the reader to distrust the ones still
true. What remains undelivered here is `mcp_presets`, whose preset *wrappers* hardcode Linux
paths — so the stage does not install the npm packages behind them either, and the bootstrap
still warns.

⚠ **NOT MEASURED.** Half two was built from a Linux jail, like half one — no `sandbox-exec`,
no `_yolojail`. Every sentence above about what the stage *does* is a description of code that
has never run.

The stage differs from the container's in three stated ways, each a decision rather than a
gap. It runs **confined**, where the darwin bootstrap beside it does not — the bootstrap runs
yolo's own code, the stage runs vendor install scripts. It does **not** forward its command
through `sudo --login`, which the agent launch does: `sudo -i` concatenates and
backslash-escapes the command instead of exec'ing it, leaving `$` for a login shell to expand,
and the stage script is full of `$`. And it takes **four of the six** steps the container
takes: the store prune needs a liveness proof this backend never computes, and the
venv-precreate script is Linux-absolute.

> [!WARNING]
> **The blocked-tool blockers ARE generated, which made the old briefing gap a trap.** When
> briefings were not delivered, `grep -r` exited 127 having never told the agent *why*: the
> enforcement arrived without the contract. Any future generator that emits enforcement must
> ship, or account for, the prose that explains it.

## What this does not license

- **Not** a per-workspace home. `HOME` is `/Users/_yolojail` on every launch, and the
  per-workspace tier is reached by symlinking directories out of it rather than by moving it
  ([`macos-user-home-tiers.md`](macos-user-home-tiers.md#oq-ht4) — a per-workspace home is the
  recorded runner-up). ⚠ The reason given here until the split landed — *"the single home IS
  this backend's shared-credentials mechanism"* — is **retracted**: the mechanism is the
  `shared_credentials` hook, which runs on every backend, and the home only ever supplied the
  backing of the directory a pack declared at `scope: machine`. What actually refuses a
  per-workspace home is parity: no other backend puts a project's agent state under the
  account home, and a home that reached credentials some other way would make "where are my
  credentials" a question every pack had to feature-detect.
- **Not** `resources` via rlimits. See the warning above; the semantics do not match the keys.
- **Not** a relocatable shared root. `/Users/Shared` exists on every stock macOS and is the
  OS-blessed neutral location for cross-user data, so the default satisfies the real
  requirement out of the box. A config key for it was designed, never implemented, and dropped
  — including from the plan-invariant message that used to advertise it. Revisit only if a
  relocated-root case actually lands, which would need the key agreed at **both** setup-time
  provisioning and the run-time workspace-location check.
- **Not** a hand-built bootstrap environment. One translation, one place.
- **Not** a new container-implemented feature that is silent here.

## Current values

Verified at `d8cf1cf8`. The prose above explains what each of these is for; this table is the
only place the values themselves are stated.

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Sandbox account, and its group | the hidden `_yolojail` user | `macosuser.SandboxUser`, `SandboxGroup` |
| Sandbox home | `/Users/_yolojail` — a constant, no workspace component | `macosuser.SandboxHome` |
| Neutral shared workspace root | `/Users/Shared/yolo` | `macosuser.SharedRootDefault` |
| Root-owned state dir for the staged binary and pack tree | under the sandbox state root, named per session | `macosuser.BuildRunPlan` (`StagedDir`, `StagedYolo`, `PackRoot`) |
| Nix build target | `.#packages.<system>.yoloNoncontainerProfile` — the non-container **floor** plus the declared `packages:`, for the system `NativeSystem()` reports rather than a hardcoded double. The declared-alone `yoloNoncontainerPackages` is the *container* store-delivery path's attr, not this one | `darwinpkg.FloorProfileAttr` (`ProfileAttr` is the other), `darwinpkg.NativeSystem`, floor list in `darwinpkg/floor.go` |
| Skip-list eval, and its bound | the darwin-unavailable list, timeout-bounded, non-fatal | `darwinpkg.skippedNames` |
| PATH additions from the profile | `<out>/bin`, plus `PKG_CONFIG_PATH=<out>/lib/pkgconfig` when present | `darwinpkg.ProfilePaths` |
| Content-overlay wire var | `YOLO_DARWIN_HOME_OVERLAY` | `internal/cli/run/macoshomeoverlay.go`, `entrypoint.InstallHomeOverlay` |
| Pack tree wire var | `YOLO_PACK_ROOT`, baked onto the bootstrap argv | `macosuser.BuildRunPlan`, asserted by `PlanInvariants` |
| Login-rc PATH var | `YOLO_DARWIN_LOGIN_PATH`, assembled from `macosuser.SandboxPath` | `entrypoint.DarwinBootstrapOptions`, `WriteLoginRC` |
| Unified-logging dial | `macos_log`: `off` / `user` / `full`, default `off` | `macosuser.MacosLogWrapperScript`, `macosuser.macosLogMode`, `entrypoint.InstallYoloLog` |
| Seatbelt write policy | deny all, re-allow the workspace, the sandbox home, and the temp dirs | `macosuser.SeatbeltProfile` |
| Seatbelt read denials | under the users root (with intermediate literals re-allowed), under `/Volumes` except the boot volume, and the keychain dir | `macosuser.SeatbeltProfile`, `ancestorLiterals` |
| Process visibility | allowed wholesale | `macosuser.SeatbeltProfile` |
| The narrower capture profile | drops the workspace and the sandbox home from the write set | `macosuser.SeatbeltCaptureProfile` |
| Host nix daemon opt-in (container backends only) | `YOLO_NIX_HOST_DAEMON` | `internal/cli/run/hostprobes.go` |
| The sandbox's `nix` (added after the verified commit) | the host client's resolved store `bin` dir, after the floor's; delivered only with a daemon socket at `/nix/var/nix/daemon-socket/socket`; plus `NIX_REMOTE=daemon` (unless the user set it) and `NIX_CONFIG=extra-experimental-features = nix-command flakes` (appended to a user's `NIX_CONFIG` that names no features) | `macosuser.resolveHostNix`, `hostNixEnv`, `withHostNixEnv` (`hostnix.go`); `BuildRunPlan` |

> [!NOTE]
> **`macos_log` now validates, and the class it came from is worth keeping.** The key was READ,
> HONORED and DOCUMENTED long before it was ACCEPTED: the bootstrap installed the `yolo-log` helper
> from it in all three modes while `config.knownTopLevelConfigKeys` had no entry — and an
> unrecognized key is a **fatal** config error, so every workspace declaring it was refused,
> including the one the helper's own remedy text told the user to write. It is accepted now
> (`internal/config/config.go`'s key set, plus the `config-ref` row an accepted key owes), and
> `internal/config/macoslog_test.go` pins the mundane fact that the key validates at all, *"because
> that is the fact that was false."*
>
> **The reusable lesson is the gap, not the key:** a dial can be fully implemented, fully
> documented and completely unreachable, and nothing in either half notices. Reading a key is not
> accepting it.

## Why it's this way

Forward-facing rulings a maintainer would otherwise undo. The two `OQ-BP` questions below are
**owned by [`../design/backend-parity.md`](../design/backend-parity.md)**, which is where their
stakes and any live answer live; they are linked rather than restated because a mirror that
drifts from its owner is worse than no mirror.

| ID | Ruling | Why it stays |
| :--- | :--- | :--- |
| `A1` | The config-change approval prompt is called on the macos-user arm itself, not hoisted above the dispatch | This is the backend where the nix build runs **unconfined as the invoking user**, so it is the worst place to lose that gate. The arm is the right call site because the container arm gates the fresh-launch path only, and this backend has no attach. |
| `A2` | A declared package with no darwin build is **fatal**, raised host-side after a green eval | The old warn-and-skip masked a typo and a genuinely-unavailable package with one message, and either way the jail started without a tool the user declared. Erroring inside the eval was the objection; erroring after it keeps nix green and lets the CLI decide. |
| `A3` | The relocatable-shared-root config key is **not implemented**, and the plan-invariant message no longer advertises it | The default is the OS-blessed neutral location and satisfies the requirement. A knob that names nothing is worse than no knob, and implementing it needs agreement at two separate places. |
| `#39` mirror | Per-workspace **homes** are refused; the per-workspace **tier** is a symlink layout ([`OQ-HT4`](macos-user-home-tiers.md#oq-ht4)) | `HOME` never moves, so the declared `scope: machine` directory never moves either and the `shared_credentials` hook's output is byte-identical here. Each `scope: workspace` directory is a symlink into `<workspace>/.yolo/home` — the same sidecar podman binds — so both tiers are restored explicitly, which is what the refusal always asked for. |
| [`OQ-BP-2`](../design/backend-parity.md#decision-ledger) | Briefings and skills **are delivered**, composed above the dispatch and copied into the sandbox home | Answered by code. The part of the leaning that did **not** hold is the hardware half: it asked to land with a Mac session, and it landed without one — so the ruling is answered and the verification is still owed. |
| [`OQ-BP-3`](../design/backend-parity.md#decision-ledger) | Whether a warned disposition needs suppressing is owned there, not here | Several launch warnings exist now, most of them on this backend. A warning people learn to skip is worse than none, which is why the question is real — and why answering it per-backend rather than per-key would be the wrong shape. |
