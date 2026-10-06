---
status: current
verified: 2026-09-09
verified_commit: d14bdab7
covers:
  - internal/macosuser/
  - internal/darwinpkg/
  - internal/entrypoint/darwin.go
  - internal/entrypoint/darwinoverlay.go
  - internal/cli/run/macoshomeoverlay.go
  - internal/cli/run/loopholeinert.go
  - internal/cli/run/jaildaemondecline.go
  - internal/loopholes/guestrun.go
  - internal/supervisor/supervisorcmd.go
  - integration/macosuserjaildaemon_test.go
tags: [macos-user, seatbelt, nix, darwin, backend-parity, packages]
summary: "The macos-user backend as built: nix materializing packages: natively into a buildEnv profile, the native bootstrap running the same pure generators the container boot runs, and the surface that is off — grouped by whether it is structurally impossible, absent-but-warned, or fixed. Includes the two permanent differences of a copied home overlay versus a bind mount, and the refusals (rlimits for `resources`, per-workspace homes) that are terminal answers rather than pending gaps."
---

# macos-user — native nix, and the surface a container would have given you

**Status:** verified 2026-09-09 against `d14bdab7`. The
[jail-daemon section](#the-jail-daemons-run-in-the-sandbox) and its rows in
[Why it's this way](#why-its-this-way) were rewritten against `d4e435a3` on 2026-10-01, when the
plan that built them (`jail-daemon-on-macos-user-plan.md`) graduated into them; the rest was not
re-verified then.

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
| The jail daemons: which the sandbox runs, their supervisor's argv, env file, start and stop | `internal/loopholes` (`JailDaemonsRunIn`), `internal/cli/run` (`jailDaemonsFor`, `jaildaemondecline.go`), `internal/macosuser` (`jaildaemon.go`: `GuestBinaries`, `GuestBinDir`, `startJailDaemons`; `RunPlan.JailDaemonArgv`) |

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

The Linux-only boot steps — the loader cache, cgroup delegation, port forwarding, the container
bootstrap and venv scripts — are deliberately **not** run. They are no-ops or nonsensical on a
native user. The bootstrap does not start the daemon supervisor either, and that one is not a
no-op: the LAUNCH starts it instead, after the bootstrap exits (next section).

### The jail daemons run in the sandbox

A **jail daemon** is a `jail_daemon` a selected pack declares — the in-jail half of a loophole, or
a pack service's daemon ([`loopholes.md`](../../userguide/guides/loopholes.md)). Since
[OQ-DP8](../design/declaration-parity.md#OQ-DP8) and [OQ-DP9](../design/declaration-parity.md#OQ-DP9)
(ruled 2026-09-28: *"if you would have run it in the jail container, you run it on the guest"*),
this backend runs them the way a container does, in the sandbox:

- **The binaries.** The flake bundle carries a darwin copy of the in-jail set as
  `bin/darwin-<arch>` beside its Linux dirs (`flake.nix`'s `guestBinaries`; today that is
  `yolo-jaild` alone). The launch copies it into `/var/yolo-jail/bin`, root-owned, beside the
  `yolo` it already stages for the bootstrap; a checkout with no prebuilt dir builds
  `.#guestPrefix` instead. That directory is on the sandbox's PATH and on no host PATH, so the
  host ship set stays `yolo`.
- **The supervisor.** `yolo-jaild supervise` runs as `_yolojail`, under the session's Seatbelt
  profile, through the same `sudo` → `env -i` → `sandbox-exec` → env-file reader layers as the
  agent. It starts after the provisioning stage and before the agent, and its process group is
  signalled when the agent exits. Each declared `cmd` runs exactly as declared: `yolo-jaild` is
  resolved on the sandbox PATH, with no rewrite and no shim.
- **Their addresses.** The sandbox shares the Mac's loopback, so each daemon's declared port is
  replaced by one the launch picks, and the pointer its clients get (codex's refresh URL, the
  `bedrock` credential URI) names the same port.
- **What is declined, by name.** One `Declined:` header, then each declined daemon on its own
  line with its reason (`loopholes.JailDaemonsRunIn`, the one split the launch's served set,
  `yolo check`'s prediction and the decline printer all read; [JD-3](#jd-3)). These shapes are
  declined:
  - an **intercepting** loophole's daemon (the Claude OAuth terminator), which needs a
    container's `--add-host` and port 443 ([below](#the-oauth-terminator-stays-declined));
  - a **pack service's** daemon whose admitted host half serves the service's adaptation
    instead (the wire bridge), as a launch-owned child
    ([JD-4](../design/jail-daemon-on-macos-user-plan.md#JD-4) is a maintainer follow-up on
    whether that should change), and a pack service's daemon that publishes an endpoint file,
    whose path, under `/run/yolo-services`, has no sandbox counterpart. Every other pack
    service's daemon runs in the sandbox, a service whose host half the launch refuses
    included, which the launch says on a `Not started outside the sandbox:` line
    ([JD-9](../design/jail-daemon-on-macos-user-plan.md#JD-9));
  - a **doorway**: a daemon that also declares a host argv (`jail_daemon.host_cmd`), which opens
    outside the sandbox for this launch instead, as a listener the launch owns on the Mac's
    loopback the agent shares ([HS-D15](../design/host-notch-services.md#HS-D15)). Both shipped
    credential adapters, the OpenAI refresh adapter and the AWS credential adapter, are doorways
    since 2026-09-29, so no shipped pack hands the sandbox a daemon today, and the supervisor
    runs only for a pack that declares a jail daemon without a host argv;
  - a program in the loophole's folder that is a **Linux executable** (its first bytes are ELF
    magic): `{jail_loophole_dir}` resolves here to the folder's place in the sandbox's
    root-owned copy of the staged packs, under `/var/yolo-jail/packs/<cname>/`, so a script or
    a macOS program the pack ships runs, `hello-daemon` included
    ([JD-10](../design/jail-daemon-on-macos-user-plan.md#JD-10)). ⚠ Unlike a container's
    per-launch pack tree, that copy is one per workspace and each launch of the workspace
    replaces it, so a second session swaps the folder under the first session's running
    daemon, whose next restart runs the newer copy (JD-10 names the follow-up, a per-launch
    copy);
  - a command naming a path that exists only in a container: a jail binary's container path
    (`{jail_binary:<name>}`, deferred by
    [BP-D6](../design/broker-as-a-pack.md#BP-D6)), or a loophole folder the launch did not
    stage.

  Each line for a daemon that then runs nowhere (an endpoint publisher, a Linux executable, a
  container path) ends with the next step: a container runtime runs it, `YOLO_RUNTIME=podman` or
  `YOLO_RUNTIME=container` for one launch.

**Starting and stopping the supervisor.** The launch starts it with `sudo -n`, which fails rather
than prompting beside the agent's terminal, in a process group of its own, and stops it with
SIGTERM, a grace period longer than the supervisor's own SIGTERM-to-SIGKILL wait, then SIGKILL;
`sudo` relays the SIGTERM ([JD-6](#jd-6)). The supervisor writes one readiness line before it
starts anything, and its own stdout and stderr go to `supervisor.log` beside its daemons' logs,
written by a `/bin/sh` wrapper inside the Seatbelt profile as the sandbox account and readable by
the host user through the workspace's inherited ACEs ([JD-8](#jd-8)). The launch waits a short
bound for that line in the part of the log this start added:

- the line appears: the launch prints one line naming the daemons it started. That line is a
  disclosure, printed on every launch;
- the supervisor exits first: the launch **refuses**, quoting the log's new lines and whatever
  `sudo` or `sandbox-exec` printed before the log took over, because the served set already
  pointed this launch's agents at those daemons' addresses;
- the bound passes with the supervisor still running and silent: the launch says it is
  unconfirmed and goes on, since a timer of yolo's is no reason to refuse a slow Mac.

**The boundary is the token files.** There is no network isolation here, so a caller proves it
belongs to this launch with the launch's per-launch caller token
([NC-D2](../plans/notch-convergence.md#7-decision-ledger)). The supervisor reads every token its
daemons demand from its own file, `/var/yolo-jail/env/<session>.daemons.env` (`<session>` is [the session key](macos-user-provisioning.md#the-session-key), so each terminal's supervisor reads its own): root-owned, mode
`0600`, in a `0700` directory, with one `user:_yolojail` ACE granting read (and search on the
directory), installed like the session's env file and swept after the supervisor stops
([JD-5](#jd-5)). Not a `group:` ACE, because the `_yolojail` group holds the host user too. The
host services' endpoint files are the same shape: `0600` in a `0700` directory, one `user:` read
ACE. A scoped token (`aws-auth`'s) is exported only in that daemon file and in the environment
of an agent whose profile selects `bedrock`.

**MEASURED** on a Mac: `TestMacosUserJailDaemonRunsConfinedInTheGuest`
(`integration/macosuserjaildaemon_test.go`), recorded as passing in the `macos-user.yml` run
36719581090 of 2026-09-30, at `8f7468dd`. Its subject is a local pack's copy of the OpenAI
refresh adapter less its host argv, since no shipped pack hands the sandbox a daemon. It settles
that the Linux-built darwin `yolo-jaild` execs from `/var/yolo-jail/bin`, that `sandbox-exec`
admits the supervisor and the daemon it starts, that the daemon reads its `0600` env file through
the `user:` ACE, that the host user reads `supervisor.log` and finds the readiness line, and that
no supervisor outlives the session. **UNMEASURED:** two concurrent launches of one workspace
(the per-launch address pick, [JD-7](#jd-7)), how long the start takes against the readiness
bound, and what a real `sudo -n` refusal or `sandbox-exec` denial prints.

<a id="the-oauth-terminator-stays-declined"></a>

> [!WARNING]
> **Do not start the OAuth terminator on this backend, and do not file it as deferred work.**
> Its default port, 443, is unprivileged only because a container runs as UID 0, and the
> sandbox account cannot bind it; its interception needs the `--add-host` a backend with no
> container cannot emit, so nothing would reach a running terminator anyway. The more useful
> reason is that it may not need to exist on any backend: it serves only the OAuth broker, and
> the broker exists only because the credential file is shared across jails while the vendor's
> refresh lock is not. If [`OQ-CI1`](claude-oauth-interposition.md#OQ-CI1) is ruled against
> sharing, the terminator goes everywhere, with the `/etc/hosts` pin and the CA.

> [!WARNING]
> **Do not hand the payload to the bootstrap.** Setting `YOLO_JAIL_DAEMONS` in the bootstrap's
> environment would start the supervisor unconfined and orphaned: the bootstrap argv has no
> `sandbox-exec`, and the bootstrap is a one-shot process that exits. The payload is composed
> once, above the backend dispatch (`jailDaemonsFor`, over `loopholes.Set.JailDaemons`), and has
> one writer per backend: a second `-e` line on a container would make the winner depend on
> duplicate-flag resolution. Do not move the composition back into the container argv
> assembler either. That is where it was until 2026-09-18, and this backend, which never reaches
> the assembler, then neither started nor named the jail daemons a bare `"packs": ["claude"]`
> selects, which was the default configuration.

> [!WARNING]
> **A symlink cannot stand in for `yolo-jaild`.** Both binaries dispatch on plain `args[0]`,
> never on `argv[0]`, so `yolo-jaild supervise` through a symlink to `yolo` reaches
> `yolo supervise`, which is not a command. That is why the guest set is a real darwin build.

### Content delivery is a copy, not a mount

Skills and briefings **are** delivered. The host composes the same trees and bodies the
container path composes and lays them out **by destination**; the launch stages that tree and
names it in an environment variable; the boot copies it over the sandbox home. It runs **last**
among the writers, because the per-surface writers above it create the agent home directories
it copies into.

Two properties were recorded here as **permanent differences**, and neither is one any more:

- **The delivered files are write-protected**, as a container's bind is `:ro`, since
  2026-09-27. They were writable copies until then, and the launch warned. The session's
  Seatbelt profile now denies every write, rename and delete of a delivered skills dir or
  briefing at the physical path it lands at, and the directories above it may not be moved
  aside ([`macos-user-home-tiers.md`](macos-user-home-tiers.md#the-staged-skills-and-briefings-are-write-protected-at-the-path-the-kernel-sees)).
  ⚠ Linux pins the rules; that the kernel refuses is asserted by two Mac tests that have not
  run yet.
- **The destination is per-workspace**, and used to be the second permanent difference. It
  is not one any more: skills and briefings land under `<workspace>/.yolo/home` through the
  layout symlinks, so a second workspace launching concurrently no longer replaces what this
  one delivered.

> [!WARNING]
> **The overlay replaces each destination whole, and nothing outside one; it must not merge.**
> The overlay is authoritative for the paths it lists, exactly as a bind mount is — a skill a
> pack stopped shipping has to **disappear** from the home, and a merge would keep serving it
> forever. Everything else in the home (credentials, sessions, generated config, anything the
> agent wrote beside or above a destination) is untouched.

> [!WARNING]
> **The tree carries a list of its destinations, and no mapping.** The host laid it out at the
> destinations the container would have mounted and lists those destinations beside it, because
> the tree alone cannot say where one starts: an install that guessed deleted pi's, omp's, agy's
> and opencode's state dirs on every launch
> ([G36](macos-user-home-tiers.md#the-overlay-replaces-its-destinations-and-nothing-else)). The
> list names paths the tree already spells, so the copier still carries no mapping logic — which
> would be a second implementation of the mount assembler's, exactly the drift this repo removes
> elsewhere. It is also the one list the profile write-protects, so what is replaced and what is
> protected cannot differ ([HT-D7](macos-user-home-tiers.md#ht-d7)).

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
- **`cache_relocations`** — **delivered by link, since 2026-10-05, unmeasured on a Mac.** There
  is no mount, so a user-scope entry becomes a link the bootstrap lays at the sandbox home's
  `~/.cache/<subdir>` to the target, and the Seatbelt profile opens the target read and write
  after its `/Volumes` and `/Users` read denies. The target may be on another volume; it may not
  be in a home, the workspace or a context source. A DAC preflight before the nix build and one
  write under the session profile refuse a target the sandbox account cannot use, naming
  `yolo macos-fix-permissions`. Only `~/.cache` moves: a tool caching under `~/Library/Caches`
  keeps its cache in the sandbox home ([`cache-relocation.md`](../plans/cache-relocation.md#macos-user-a-link-plus-seatbelt-rules)).
- **`per_side_paths`** — **structurally impossible, and warned.** It gives the host and the
  sandbox *different contents at the same path*, which is a mount-namespace capability.
  Seatbelt can deny a path; it cannot fork one. This matters more than it looks, because
  `node_modules` and `.venv` are in the default shadow set on the container backends — so the
  warning names every user entry AND each default the workspace uses (the path or its manifest
  exists), not only what the config lists. uv is the one tool redirected:
  `UV_PROJECT_ENVIRONMENT=.venv-macos-user`, relative to each project root, under any value the
  user's own env layers set.
- **config `mounts` and a pack `mount`** — **delivered by link, from outside every home.** Each
  is a root-owned symbolic link in `$YOLO_CONTEXT_DIR` to the host folder itself, the Seatbelt
  profile decides access to the folder, and a check run as the sandbox account confirms it can
  reach the folder before the sandbox starts. A folder this backend cannot serve, one in a home
  above all, refuses the launch with its reason
  ([`../design/context-mounts.md`](../design/context-mounts.md#3-delivering-context-dirs-on-macos-user)).
  Built 2026-10-01 and not yet run on a Mac.
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

**`resources`** has no cgroup to write, so each key does what macOS allows (2026-10-04): `io` sets
the session's process disk policy ([`io-priority.md` §5.5](../design/io-priority.md#55-macos-user-the-second-step)),
`cpus` sets four parallelism defaults cooperatively, `memory` is a sampled guard inside the
sandbox that stops the largest process, and `pids_limit` is **not enforced, and warned**
([`declaration-parity.md` DP-I8, DP-I9 and OQ-DP10](../design/declaration-parity.md#decision-ledger)).

> [!WARNING]
> **An rlimit fix is refused, not pending, and rlimits are the trap.** `RLIMIT_AS` is **address space,
> not RSS**, so it is not what a memory cap means — capping it breaks JITs and the Go runtime,
> both of which reserve far more virtual address space than they ever fault in. `RLIMIT_NPROC`
> is **per-user, not per-process-tree**, so on this backend it would collide across concurrent
> sessions, every one of which runs as the same account. A cap the user believes in but that
> does not hold is worse than a documented absence, so this warns and will keep warning.

`yolo-cglimit` and the cgroup-delegate daemon are Linux-only and simply absent.

### Networking, devices, GPU

A native process runs on the host's real network, so the **network modes** have no namespace
to switch and are not applied, and a host service is reachable directly. **Port forwarding**'s
container mechanism (`-p`, the host socat) lives in the launch path this backend returns before;
here a remap in either port key is carried by a TCP relay the launch opens outside the sandbox
when the session starts and closes with the command, and a same-port entry needs nothing
([`declaration-parity.md` DP-I14](../design/declaration-parity.md#DP-I14), unmeasured on a Mac). **GPU** is unavailable on every
macOS backend (Metal, no CUDA or ROCm). **Devices**: there is nothing to pass through, since the
sandbox opens a `/dev` node under ordinary permissions, but the profile refuses `ioctl` on all but
terminals, so a `devices` entry naming a `/dev` node re-allows that node's ioctls and is disclosed
at launch; raw disks and bpf stay denied (unmeasured on a Mac). `usb:` and cgroup rules are Linux
kernel features and are warned as not read.

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
OpenAI loophole is active and whose broker did not start is refused. Each session publishes into
a host-services directory of its own and removes only that one, so a second terminal in the same
workspace neither replaces the first's endpoints nor removes them when it exits
([`HSD-4`](jail-home.md#why-its-this-way)). Since 2026-10-05 one keeper per workspace holds the
fronts, doorways and launch-owned services for every macos-user session of it, in one
host-services directory, and stops them when the last session ends; a launch that plans none of
them starts no keeper and runs as before
([JL-D86](../design/jail-lifetime-last-session-wins.md#JL-D86),
[JL-D42](../design/jail-lifetime-last-session-wins.md#JL-D42)).

**The JAIL half runs too**, in the sandbox ([above](#the-jail-daemons-run-in-the-sandbox)), except
the jail daemons it declines by name. The launch says both things: one `Declined:` line per
declined jail daemon, and — on the
**platform** axis only — one line per loophole this machine cannot run at all (`audio`,
`journal` and `cgroup-delegate` declare `platforms: ["linux"]`). The
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
- **The host-process view** runs here, and it is the same feature as in a container. The
  sandbox lists host processes on its own, but the profile denies it another process's
  command line (`seatbelt.go`'s `cross-process-procargs-deny`), so the loophole's allowlisted
  window, args included, is exactly what the agent lacks. The host daemon has a BSD-ps arm
  (`internal/hostprocesses`), and the launch stages `yolo-ps` into the sandbox whenever the
  loophole's endpoint is published (`macosuser.GuestClients`). Unmeasured on a Mac:
  `TestMacosUserYoloPsListsAnAllowlistedHostProcess`.
- **The serial bridge** runs here. The host daemon opens the device on the Mac, and the
  launch stages `yolo-serial` into the sandbox whenever the loophole's endpoint is published;
  `yolo-serial pty` allocates its virtual PTY with darwin's own ioctls
  (`cmd/yolo-serial/pty_darwin.go`), which the profile's terminal ioctl allow covers.
  Unmeasured on a Mac: `TestMacosUserSerialClientDrivesAHostPtyFromTheSandbox`, and a real USB
  device is a human's check.
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
  would perform it is the one loophole `jail_daemon` this backend declines — so refreshes are not
  serialized here even with the daemon up.

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
still warns. The `chrome-devtools` pack is the delivered route for that server: its program
installs through the launchers as any pack's does, and its wrapper uses the Mac's own browser.

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

Verified at `d14bdab7`. The prose above explains what each of these is for; this table is the
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
| Pack tree wire var | `YOLO_PACK_ROOT`, baked onto the bootstrap argv; since 2026-10-04 also in the session env file, with `YOLO_DARWIN_WORKSPACE`, when packs were staged, so an in-sandbox `yolo programs` reads this jail | `macosuser.BuildRunPlan`, asserted by `PlanInvariants`; read in the sandbox by `entrypoint.JailEnvFromOS` |
| Login-rc PATH var | `YOLO_DARWIN_LOGIN_PATH`, assembled from `macosuser.SandboxPath` | `entrypoint.DarwinBootstrapOptions`, `WriteLoginRC` |
| Unified-logging dial | `macos_log`: `off` / `user` / `full`, default `off` | `macosuser.MacosLogWrapperScript`, `macosuser.macosLogMode`, `entrypoint.InstallYoloLog` |
| Unified-log deny under `off` | file-read of `/private/var/db/diagnostics` and `/private/var/db/uuidtext`, mach-lookup of `com.apple.diagnosticd`; none under `user`/`full` (inferred, unmeasured on a Mac) | `macosuser.macosLogDenies` |
| Seatbelt write policy | deny all, re-allow the workspace, the sandbox home, and the temp dirs | `macosuser.SeatbeltProfile` |
| Seatbelt read denials | under the users root (with intermediate literals re-allowed), under `/Volumes` except the boot volume, and the keychain dir | `macosuser.SeatbeltProfile`, `ancestorLiterals` |
| Process visibility | the process list is allowed; another process's command line and environment are denied (procargs and pidinfo), except within the same sandbox | `macosuser.SeatbeltProfile` |
| The narrower capture profile | drops the workspace and the sandbox home from the write set | `macosuser.SeatbeltCaptureProfile` |
| Host nix daemon opt-in (container backends only) | `YOLO_NIX_HOST_DAEMON` | `internal/cli/run/hostprobes.go` |
| The guest prefix, holding the staged `yolo` and the darwin in-jail set | `/var/yolo-jail/bin`, root-owned; the set is `yolo-jaild`, `yolo-serial`, `yolo-ps` | `macosuser.GuestBinDir`, `macosuser.GuestBinaries`; `flake.nix` `guestBinaries`; `stage-source-bundle.sh` `GUEST_BINARIES` |
| The supervisor's env file (verified at `d4e435a3`; named per session since 2026-10-04) | `/var/yolo-jail/env/<session>.daemons.env` ([`<session>`](macos-user-provisioning.md#the-session-key)), `0600` in a `0700` directory, one `user:_yolojail` read ACE | `macosuser.SandboxDaemonEnvFile` |
| The sandbox's TLS trust (added 2026-10-04) | `NIX_SSL_CERT_FILE`, `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE`, `GIT_SSL_CAINFO` name `/var/yolo-jail/env/<session>.ca-bundle.crt` (the profile's public roots plus the CAs the System keychain trusts for TLS), or the profile's own bundle when it adds none, and when the launch's own env layers set any of the five, all five name that value instead; `NODE_EXTRA_CA_CERTS` names `<session>.extra-ca.pem` only when there is one and the layers do not set it | `macosuser.ComposeCATrust` (`cabundle.go`); [PS-D10](../design/provisioner-sets.md#PS-D10) |
| The supervisor's own log (verified at `d4e435a3`) | `<workspace>/.yolo/home/local/state/yolo-jail-daemons/supervisor.log`, `~/.local/state/yolo-jail-daemons` in the sandbox | `macosuser.SupervisorLogName`, `SupervisorLogPath` |
| The supervisor's readiness line, and the launch's wait for it (verified at `d4e435a3`) | `yolo-jaild supervise: supervising <names>`; 1.5 s | `supervisor.StartedLinePrefix`; `macosuser.supervisorReadyBound` |
| The supervisor's stop (verified at `d4e435a3`) | SIGTERM to its process group, 10 s, then SIGKILL | `macosuser.jailDaemonStopGrace` (`real.go`) |
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
| <a id="jd-1"></a>`JD-1` | **The guest set is `yolo-jaild`, `yolo-serial` and `yolo-ps`**, one list in three spellings that tests pin together, staged whole when the launch runs a jail daemon or its session env carries an endpoint one of the two clients reads. *Amended 2026-10-04, an implementation decision taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible:* it applies [`OQ-DP8`](../design/declaration-parity.md#OQ-DP8)'s "if you would have run it in the jail container, you run it on the guest" to the clients | `yolo-jaild` is the supervisor and every in-jail daemon; the two clients belong to the loopholes that run on a Mac (`serial`, and `host-processes` since its BSD arm). `yolo` is staged from the running host binary, the bootstrap is `yolo internal darwin-bootstrap` rather than `yolo-entrypoint`, and `yolo-cglimit` and `yolo-journalctl` belong to Linux-only loopholes. The trigger is each client's own endpoint variable (`macosuser.GuestClients`), so a checkout launch builds `.#guestPrefix` only when something in the sandbox will run it. Adding a binary to one spelling and not the others ships a guest that cannot run what it declares. |
| <a id="jd-2"></a>`JD-2` | **The guest prefix is `/var/yolo-jail/bin`, and the staged `yolo` lives in it too** | `SandboxPath` derives its one entry from the staged `yolo`'s directory, so both names resolve and no PATH list is reordered. A checkout builds `.#guestPrefix` only when the launch has a daemon to run or a guest client's endpoint, and a failed build refuses the launch, naming the daemon or the client and its loophole, rather than starting a sandbox that cannot run them. A bundle whose `bin/darwin-<arch>` lacks a member of the set is refused before any build, naming what is missing and the restage (`just install`, or reinstalling yolo-jail): a bundle ships no Go sources, and the flake's prebuilt branch asks only whether the directory exists. |
| <a id="jd-3"></a>`JD-3` | **What the guest declines is one split, `loopholes.JailDaemonsRunIn`**, read by the served set, `yolo check`'s prediction and the decline printer | Three readers with their own copies of the rule would disagree about which daemons a launch runs, and the served set would point an agent at an address nothing serves. The split keys on manifest facts (an intercept list, a service, a host argv, a container path), never on a daemon's name. |
| <a id="jd-5"></a>`JD-5` | **The supervisor reads an env file of its own**, separate from the agent's session file, and swept after the supervisor stops | It carries the payload, the shared channel values, and every caller token its daemons demand, a scoped one included, since no agent reads this file. Folding it into the session file would hand every agent the scoped tokens. |
| <a id="jd-6"></a>`JD-6` | **The supervisor starts after the provisioning stage and before the agent, under `sudo -n`, in its own process group, and stops after the agent** | `sudo -n` fails rather than prompting beside the agent's terminal. Killing the group, as container teardown does, is what leaves no daemon behind; the grace is longer than the supervisor's own wait so it can stop its children first. |
| <a id="jd-7"></a>`JD-7` | **macos-user picks served addresses** for the daemons it runs | The sandbox shares the Mac's loopback, so a declared port is the machine's real one and two concurrent launches of one workspace would collide on it. |
| <a id="jd-8"></a>`JD-8` | **The supervisor's stdout and stderr go to `supervisor.log`, and the launch says it started the daemons only after this start's readiness line** | With both on `/dev/null`, a `sudo -n` refusal, a `sandbox-exec` denial or an exec failure left no trace while the launch still printed that it started them. The log is appended, not truncated, and the per-workspace launch lock covers the start, so the launch reads only the bytes this start added. Every failure the bound exists for exits within milliseconds, so the bound only limits a start that is alive and silent, and that case continues rather than refusing. |
| <a id="jd-11"></a>`JD-11` | **The reachability witness runs as a confined stage of the launch**, after the jail daemons and before the agent: `yolo internal probe-services` as the sandbox account, under the session profile, reading the session env file, with plain `sudo`. Status 78 refuses; any other status warns and launches. *Implementation decision, taken under the maintainer's 2026-10-04 delegation; reversible.* (`JD-9` and `JD-10` are [the plan stub's](../design/jail-daemon-on-macos-user-plan.md#JD-9).) | The bootstrap runs outside the profile, so a probe there could pass where the agent's client is refused. Plain `sudo`, unlike the supervisor's `-n`, because the stage is in the foreground and a long provisioning stage can outlive sudo's credential cache. A stage that never answered learned nothing about the services, so it cannot refuse (provisioning's rule). See [loopback-tls-reachability.md, On macos-user](loopback-tls-reachability.md#on-macos-user). |
| [`OQ-BP-2`](../design/backend-parity.md#decision-ledger) | Briefings and skills **are delivered**, composed above the dispatch and copied into the sandbox home | Answered by code. The part of the leaning that did **not** hold is the hardware half: it asked to land with a Mac session, and it landed without one — so the ruling is answered and the verification is still owed. |
| [`OQ-BP-3`](../design/backend-parity.md#decision-ledger) | Whether a warned disposition needs suppressing is owned there, not here | Several launch warnings exist now, most of them on this backend. A warning people learn to skip is worse than none, which is why the question is real — and why answering it per-backend rather than per-key would be the wrong shape. |
