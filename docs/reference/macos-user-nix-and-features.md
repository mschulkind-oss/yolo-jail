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
job here is not to build an image: it materializes `packages:` as native `aarch64-darwin`
binaries and the launch prepends their store `bin` to the agent's PATH.

Almost everything absent on this backend follows mechanically from *no container*: a feature
implemented as a container flag or a bind mount has no host to attach to.

| Component | Lives in |
| :--- | :--- |
| The launch orchestrator, its gates and its warnings | `internal/macosuser` (`RunMacosUser`, `orchestrator.go`) |
| The run plan and its self-checks | `internal/macosuser` (`BuildRunPlan`, `RunPlan`, `PlanInvariants`) |
| Sandbox identity, paths, and the shared root | `internal/macosuser` (`SandboxUser`, `SandboxHome`, `SandboxGroup`, `SharedRootDefault`, `SandboxPath`) |
| The Seatbelt profiles | `internal/macosuser` (`SeatbeltProfile`, `ancestorLiterals`, `SeatbeltCaptureProfile`) |
| Native package realization | `internal/darwinpkg` (`MaterializeDarwin`, `ProfilePaths`, `BuildProfileArgv`, `skippedNames`) |
| The native bootstrap, and the home-overlay install | `internal/entrypoint` (`RunDarwinBootstrap`, `DarwinEnvFrom`, `InstallHomeOverlay`) |
| Host-side composition of the delivered content tree | `internal/cli/run` (`buildMacosHomeOverlay`) |
| The inert-feature reports | `internal/cli/run` (`loopholeinert.go`), `internal/macosuser` (`orchestrator.go`) |

**Reads with:** [`nix-across-backends.md`](nix-across-backends.md) (the same split from nix's
side), [`../design/backend-parity.md`](../design/backend-parity.md) (the sweep this backend's
warnings came out of, and the census that would make a silent drop unwritable),
[`macos-no-vm-direction.md`](macos-no-vm-direction.md) (why the backend exists),
[`../design/macos-user-build-step-threat-model.md`](../design/macos-user-build-step-threat-model.md)
(the host-side nix build as an attack surface),
[`../guides/macos.md`](../guides/macos.md) (user-facing setup).

---

## The one thing to internalize

On the container backends every host↔jail seam is a **bind mount**: the workspace, the home
overlay, `/nix`, the host-service socket directory, cache relocations. Here there are **no
bind mounts of any kind**.

- The "workspace" is the **actual host directory**, reached through a shared-group ACL rather
  than a mount. It must be **neutral ground** — never inside any user's home; a plan
  invariant rejects a home-directory workspace and names the shared root to move it under.
- "Home" is the **real** `/Users/_yolojail`, and it is a constant with no workspace
  component — so it is **machine-wide**, shared by every workspace on the machine.
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

Not an image. A `buildEnv` profile of `packages:` only, realized natively for this Mac, whose
`bin` the launch prepends to the agent's PATH (plus a `pkg-config` path when that directory
exists). **`yolo` never execs the build output** — it reads the out-path and builds a PATH.

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
   half-provisioned sandbox.
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

### Requirements, and nested nix

`yolo check` verifies: **nix on PATH** (native darwin nix — without it the agent gets none of
its declared tools); a **flake lock at the repo root**, which pins nixpkgs so darwin packages
are reproducible across machines; and a **trusted nix user**, which is a warning rather than a
failure — a non-trusted user can still build from cache, and being trusted is what lets the
flake's own substituter actually be consulted.

Note what the lock buys: *declarative* reproducibility (the same nixpkgs attributes), **not**
byte-identity with the Linux jail. These are darwin builds.

**Nested nix needs no arrangement.** On the container backends yolo can mount the host nix
daemon socket and the store. Here the agent runs natively and simply sees the host's real
`/nix`, subject to the Seatbelt read policy — no mount, and no opt-in toggle.

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
- **The home is machine-wide**, so the delivered content is shared by every workspace.

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
- **pack `state` at `scope: workspace`** — **machine-wide here, and warned.** Every other
  backend gives each workspace its own copy by mounting a per-workspace host directory at each
  path. The home here is a constant with no workspace component, so the whole per-workspace
  tier collapses into one directory shared by every workspace. This is the mirror image of a
  known Apple Container defect, where the *machine* tier collapsed into the per-workspace one.

> [!WARNING]
> **"Just symlink the cache yourself" does not work here.** The session Seatbelt profile
> denies writes everywhere and re-allows only the workspace, the sandbox home, and the
> temporary directories — and separately denies reads under `/Volumes` except the boot volume.
> So a host symlink onto another disk resolves fine for the invoking user and is **refused
> inside the sandbox**, which is the *worse* failure: the cache silently stays on the boot
> volume, the one outcome the feature exists to prevent. Any doc that says otherwise about this
> backend is wrong; this one is the authority.

> [!WARNING]
> **The sandbox enforces workspace isolation one layer down and then leaks it through the
> shared home.** The profile denies reads under the users root and re-allows the workspace's own
> subpath plus each *intermediate* directory as a bare literal — chosen precisely so a sibling
> checkout beside the workspace stays denied. So the agent cannot read another workspace's
> files by path — and **can** read that same workspace's agent session transcripts under the
> sandbox home, which the profile allows wholesale. The denial and the leak are the same content
> reached two ways.

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

No loophole host service starts here — the startup lives in the container path. **The
inertness is reported**, one stderr line per inert loophole naming the backend as the reason,
for **both** pack-declared and config-declared loopholes. It is deliberately *not* routed
through the disclosure wrapper that makes disclosure inseparable from the spawn: nothing spawns
here. The **briefing** is gated on the backend too, so it no longer advertises these under a
heading reading "host capabilities wired into this jail".

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
  would need root-global DNS or hosts control. `EndpointGrantCommands` exists — two ACL entries
  letting the sandbox user read one published endpoint file and traverse, not list, the
  directory holding it — and is **not called anywhere**. Treat it as dead-until-needed, not
  vestigial.

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
  backend a `launch` contribution **did nothing at all**. The two shipped instances failed
  differently, which is why this ranked as a must-fix rather than a warning: one pack's flags
  are a plain `launch` kind with no config half to fall back on — a total drop — while
  another's are the launch half of an autonomy contribution whose config half still rendered,
  so it degraded to a *partial, silent* downgrade of the autonomy the user asked for. Injection
  is now hoisted above the backend dispatch and threaded in as a parameter, the same move pack
  staging made. Nothing downstream ever recovered these flags: both in-jail launcher templates
  end by exec'ing the real binary and never read the contributions.
- **The config-change approval prompt** is called on this arm itself, before the launch. It
  sits in the arm rather than hoisted above the dispatch **on purpose**: the container arm gates
  the *fresh-launch* path only, because attaching to a running jail deliberately skips the check.
  This backend has **no attach** — every invocation is a fresh sandbox — so the arm's own call
  site is where the two backends actually agree. A dry run is exempt: it launches nothing, so
  there is nothing to approve, and refusing would only hide the diff the user asked to inspect.

One remains absent and warned: **`lsp_servers` binaries never install.** The config renders and
the agent is *told* the server is enabled, but the installer is a block of the generated
container bootstrap script this backend deliberately does not run — and there is no lazy-install
channel to fall back on, because the launcher directory carries only pack `program`
contributions and one package manager. So the warning names the configured keys and points at
`packages:` as the native alternative.

> [!WARNING]
> **The blocked-tool blockers ARE generated, which made the old briefing gap a trap.** When
> briefings were not delivered, `grep -r` exited 127 having never told the agent *why*: the
> enforcement arrived without the contract. Any future generator that emits enforcement must
> ship, or account for, the prose that explains it.

## What this does not license

- **Not** a per-workspace home. The single home *is* this backend's shared-credentials
  mechanism, so splitting it would break the machine tier to repair the workspace tier. A fix
  has to restore both tiers explicitly, which is a design change and not a launch-time patch.
  Refused, with a warning attached.
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
| Nix build target | `.#packages.<darwin system>.yoloDarwinPackages` | `internal/darwinpkg/darwinpkg.go` |
| Skip-list eval, and its bound | the darwin-unavailable list, timeout-bounded, non-fatal | `darwinpkg.skippedNames` |
| PATH additions from the profile | `<out>/bin`, plus `PKG_CONFIG_PATH=<out>/lib/pkgconfig` when present | `darwinpkg.ProfilePaths` |
| Content-overlay wire var | `YOLO_DARWIN_HOME_OVERLAY` | `internal/cli/run/macoshomeoverlay.go`, `entrypoint.InstallHomeOverlay` |
| Pack tree wire var | `YOLO_PACK_ROOT`, baked onto the bootstrap argv | `macosuser.BuildRunPlan`, asserted by `PlanInvariants` |
| Login-rc PATH var | `YOLO_DARWIN_LOGIN_PATH`, assembled from `macosuser.SandboxPath` | `entrypoint.DarwinBootstrapOptions`, `WriteLoginRC` |
| Unified-logging dial | `macos_log`: `off` / `user` / `full` | `macosuser.MacosLogWrapperScript`, `entrypoint.InstallYoloLog` |
| Seatbelt write policy | deny all, re-allow the workspace, the sandbox home, and the temp dirs | `macosuser.SeatbeltProfile` |
| Seatbelt read denials | under the users root (with intermediate literals re-allowed), under `/Volumes` except the boot volume, and the keychain dir | `macosuser.SeatbeltProfile`, `ancestorLiterals` |
| Process visibility | allowed wholesale | `macosuser.SeatbeltProfile` |
| The narrower capture profile | drops the workspace and the sandbox home from the write set | `macosuser.SeatbeltCaptureProfile` |
| Host nix daemon opt-in (container backends only) | `YOLO_NIX_HOST_DAEMON` | `internal/cli/run/hostprobes.go` |

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
| `#39` mirror | Per-workspace homes on macos-user are **refused** | The single home is this backend's shared-credentials mechanism. Splitting it repairs the workspace tier by breaking the machine tier; a real fix restores both explicitly and is a design change. |
| [`OQ-BP-2`](../design/backend-parity.md#decision-ledger) | Briefings and skills **are delivered**, composed above the dispatch and copied into the sandbox home | Answered by code. The part of the leaning that did **not** hold is the hardware half: it asked to land with a Mac session, and it landed without one — so the ruling is answered and the verification is still owed. |
| [`OQ-BP-3`](../design/backend-parity.md#decision-ledger) | Whether a warned disposition needs suppressing is owned there, not here | Several launch warnings exist now, most of them on this backend. A warning people learn to skip is worse than none, which is why the question is real — and why answering it per-backend rather than per-key would be the wrong shape. |
