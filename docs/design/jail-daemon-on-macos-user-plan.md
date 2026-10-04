---
title: "Plan: start a jail daemon on macos-user — graduated"
date: 2026-09-17
status: accepted
tags: [macos-user, loopholes, jail-daemon, parity, plan, graduated]
summary: "A pointer. Every step of this plan is built, and its settled content graduated on 2026-10-01 into docs/reference/macos-user-nix-and-features.md: how the sandbox runs the jail daemons, what it declines and why, the supervisor's start, stop and log, the token-file boundary, the traps, and the decisions JD-1 to JD-8 except JD-4. What stays here is JD-4, a follow-up question parked for the maintainer (whether the wire bridge's jail daemon should move into the guest), and JD-9 and JD-10, two implementation decisions of 2026-10-04 that let the sandbox run a pack service's daemon and a program from a loophole's own folder."
stage: GRADUATED
next: "Nothing is owed by this file. JD-4 waits on the maintainer, outside the queue; JD-9 and JD-10 graduate into the reference with its next re-verification; delete this file once all three are ruled or moved and the roadmap's two links move"
vantage:
  status-chip: true
---

# Plan: start a jail daemon on macos-user — graduated

**Status:** 2026-10-01 — every step of this plan is in the tree, and its settled content is now
[`macos-user-nix-and-features.md`'s jail-daemon section](../reference/macos-user-nix-and-features.md#the-jail-daemons-run-in-the-sandbox),
which is the authority: the darwin guest set and its prefix, the supervisor and its log, the
shapes the sandbox declines, the token files, the warnings this plan carried as traps, and the
implementation decisions in its [Why it's this way](../reference/macos-user-nix-and-features.md#why-its-this-way)
table. MEASURED: `TestMacosUserJailDaemonRunsConfinedInTheGuest` passed in the `macos-user.yml`
run 36719581090 (2026-09-30, at `8f7468dd`). UNMEASURED, a human at a Mac: two concurrent
launches of one workspace, the start time against the readiness bound, and what a real `sudo -n`
refusal or `sandbox-exec` denial prints. The plan's map, reuse list, build order and
verification split described the tree before the build; they are in git history
(`git log --follow -- docs/design/jail-daemon-on-macos-user-plan.md`).

**2026-10-04:** two implementation decisions widened what the sandbox runs, both reversible:
[JD-9](#JD-9), the one rule for a pack service's daemon, and [JD-10](#JD-10), a loophole daemon
that runs a program from its own folder. Pinned by unit tests on Linux; the Mac halves are
`TestMacosUserRunsAPackServiceJailDaemonInTheGuest` and
`TestMacosUserRunsAModuleDirJailDaemonInTheGuest` in `macos-user.yml`, not yet run.

**Needs your ruling:** nothing this file owes. [JD-4](#decision-ledger) is a follow-up the build
surfaced and left for the maintainer, outside the queue. JD-9 and JD-10 are open to your revision.

## Decision ledger

The rulings are [OQ-DP8](declaration-parity.md#OQ-DP8) and [OQ-DP9](declaration-parity.md#OQ-DP9).
JD is this plan's own prefix for the implementation decisions the build took under them.

| ID | Decision | Now resolves at |
| :--- | :--- | :--- |
| JD-1 | The guest set is `yolo-jaild` alone | [`jd-1`](../reference/macos-user-nix-and-features.md#jd-1) |
| JD-2 | The guest prefix is `/var/yolo-jail/bin`, and the staged `yolo` lives in it | [`jd-2`](../reference/macos-user-nix-and-features.md#jd-2) |
| JD-3 | What the guest declines is one split, `loopholes.JailDaemonsRunIn` | [`jd-3`](../reference/macos-user-nix-and-features.md#jd-3) |
| <a id="JD-4"></a>JD-4 | **The wire bridge keeps its host half on macos-user, and whether its jail daemon should move into the guest is a follow-up for the maintainer, not decided.** Its jail daemon publishes at `/run/yolo-services`, which the guest has no counterpart of, and NC-D65 already serves the bridge launch-owned. Moving it into the guest would retire that host half. As built, the guest declines a pack service's daemon by name ([the decline list](../reference/macos-user-nix-and-features.md#the-jail-daemons-run-in-the-sandbox)) | stays here |
| JD-5 | The supervisor reads an env file of its own | [`jd-5`](../reference/macos-user-nix-and-features.md#jd-5) |
| JD-6 | Started after the provisioning stage and before the agent, under `sudo -n`, in its own process group | [`jd-6`](../reference/macos-user-nix-and-features.md#jd-6) |
| JD-7 | macos-user picks served addresses for the daemons it runs | [`jd-7`](../reference/macos-user-nix-and-features.md#jd-7) |
| JD-8 | The supervisor's own output goes to `supervisor.log`, and "Started" waits for its readiness line | [`jd-8`](../reference/macos-user-nix-and-features.md#jd-8) |
| <a id="JD-9"></a>JD-9 | *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible.* **The one guest rule for a pack service's daemon: the guest declines it only when (a) the service serves a protocol adaptation and this launch admits its host half, or (b) it declares an `endpoint`.** (a) is the wire bridge: its host half runs launch-owned when a profiled agent's pairing needs it (NC-D65), and running the jail daemon too would serve one address twice. (b) is a file the daemon publishes under `/run/yolo-services`, a container path with no sandbox counterpart. Everything else runs confined in the guest, as a container runs it: a service that declares only a `jail_daemon`; a service whose host half the launch refuses, because a pack yolo does not ship declares it (OQ-HS4), which the launch says in a `Not started outside the sandbox:` line naming the pack and the reason; and a **pure worker**, `packdecl`'s word for a service that publishes no endpoint, when it also serves no adaptation, so its host half has no pairing to serve here. A worker that declares both halves runs its jail half in the guest and its host half is not started: confinement is preferred. **Why:** [OQ-DP8](declaration-parity.md#OQ-DP8)'s *"if you would have run it in the jail container, you run it on the guest"*, [OQ-DP9](declaration-parity.md#OQ-DP9)'s confinement, and [HS-D15](host-notch-services.md#HS-D15)'s doorway precedent, where a refused host argv's jail daemon also runs in the guest. The old rule declined every service's daemon for "its host half runs instead", which was false for every service but the bridge, so those ran nowhere. JD-4's question is unchanged: the bridge is still declined, now by (a). Built in `loopholes.JailDaemonsRunIn` over specs from `launchservice.ServiceJailDaemons`, admitted by `launchservice.AdmitServiceHosts`, both read by the launch and by `yolo check` | built 2026-10-04; [the decline list](../reference/macos-user-nix-and-features.md#the-jail-daemons-run-in-the-sandbox) once re-verified |
| <a id="JD-10"></a>JD-10 | *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible.* **`{jail_loophole_dir}` resolves to where the jail sees the module directory: in the sandbox, its place in the root-owned copy of the launch's staged packs, under `/var/yolo-jail/packs/<jail name>/` at the same relative path as in the host-side staged tree.** The orchestrator already copies the staged tree there, world-readable and keeping each file's exec bits, for the bootstrap to render from, so a program a pack configured by path ships in its folder runs in the guest as it runs from a container's `:ro` mount. The guest still declines a daemon whose program there is a Linux executable (its first bytes are ELF magic; the sandbox runs macOS programs), one naming a `{jail_binary:<name>}` path (deferred, [BP-D6](broker-as-a-pack.md#BP-D6)), and one whose module directory is outside the launch's staged tree, which a launch's own loopholes never are. **Why:** the token never named a container path: the author wrote "my loophole's folder, as the jail sees it", and the container mount point was its only resolution because a container was the only jail with a daemon supervisor. Resolving it here is not the `argv[0]` rewrite OQ-DP8 rejected; it is the load-time resolution done for the backend that runs the daemon. Built in `loopholes.JailDaemonSpec.InGuest`, applied by `run.placeModuleDirsInGuest` when the launch composes its payload, so the split, the decline and the supervisor's payload all read the guest's argv | built 2026-10-04; [the decline list](../reference/macos-user-nix-and-features.md#the-jail-daemons-run-in-the-sandbox) once re-verified |
