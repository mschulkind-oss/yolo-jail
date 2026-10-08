---
title: "Jail daemons on macos-user — the guest pack-tree lifetime follow-up"
date: 2026-09-17
status: accepted
tags: [macos-user, loopholes, jail-daemon, parity, lifetime, design]
summary: "The original jail-daemon build graduated; JD-4 stays an owner follow-up and JD-9/JD-10 preserve the declared-argv guest rules. JD-10's unbuilt lifetime correction gives every launch an immutable guest copy of its selected pack tree, with conservative retention when no-holder liveness is unknown."
stage: DECIDED
next: "Add the two-launch pack-root regression from jail-daemon-guest-pack-tree-plan.md; stop at the focused source-test gate before any native execution"
vantage:
  status-chip: true
---

# Jail daemons on macos-user — the guest pack-tree lifetime follow-up

**Status:** 2026-10-08 — the original daemon start/stop build graduated on 2026-10-01;
its authority remains [the jail-daemon reference](../reference/macos-user-nix-and-features.md#the-jail-daemons-run-in-the-sandbox).
The lifetime correction below is specified, **not built or natively measured**. Source audit:
`22afb2d38483`. The earlier Mac measurement remains evidence for the original build only:
`TestMacosUserJailDaemonRunsConfinedInTheGuest`, run 36719581090, 2026-09-30, at `8f7468dd`.
The JD-9/JD-10 native fixtures, `TestMacosUserRunsAPackServiceJailDaemonInTheGuest` and
`TestMacosUserRunsAModuleDirJailDaemonInTheGuest`, remain unverified in this audit; neither
single-session fixture establishes the concurrent-tree correction.

> **In short.** Each launch keeps the guest pack bytes it selected; another terminal cannot
> replace them. Lack of proof that the last consumer ended keeps the tree, not a guess at cleanup.

**Needs your ruling:** none for this correction. [JD-4](#JD-4) remains the maintainer's
separate follow-up; native privileges, keychain decisions and held host-daemon work are unchanged.

**Reads with:** [the bounded build handoff](jail-daemon-guest-pack-tree-plan.md) (tests and source fence),
[the immutable-pack-tree ruling](../reference/pack-system.md#oq-pk2) (the host-side precedent),
[the session key](../reference/macos-user-provisioning.md#the-session-key) (existing session files and records),
and [the macos-user keeper](jail-lifetime-last-session-wins.md#99-the-keeper-at-yolo-host-and-macos-user)
(what remains workspace-owned).

## Defined terms

- **Workspace identity** — the resolved workspace path's jail name, `cname`; it groups shared
  home/config state and locks. It is **not** a launch identity. From
  [the keeper's per-workspace key](jail-lifetime-last-session-wins.md#993-one-keeper-per-workspace-per-notch).
- **Session** — one macos-user invocation, from launch to teardown, not a second terminal
  attaching to a container. From [the session-key authority](../reference/macos-user-provisioning.md#the-session-key).
- **Host pack tree** — the immutable selected-pack copy staged by one launch; its existing
  unique directory leaf is the **tree id** used here, not a content digest or the workspace name.
  From [the pack-tree layout](../reference/pack-system.md#oq-pk2).
- **Guest pack tree** *(defined here for this correction)* — the root-owned copy of that host
  pack tree read by this launch's bootstrap, session and module-dir daemons. Not the shared
  guest binary prefix, home overlay, context tree or install-capture store.
- **Holder** *(defined here for cleanup)* — any process still reading, executing or able to
  restart from that guest pack tree, including a supervisor and its descendants. A launcher,
  workspace keeper or record alone is **not** the complete set of holders.

## Source proof: the destination loses the launch identity

READ at the audit revision; the consequences are inferred from these callers, not a new Mac run.

| Source | Actual behavior |
| :--- | :--- |
| [`StagedPackRoot`, `StagePackCommands`](../../internal/macosuser/macosuser.go) | The destination is `/var/yolo-jail/packs/<cname>`; staging uses `<destination>.new`, removes the destination and moves the new copy there. Removal and rename are separate commands, not an atomic swap. |
| [`BuildRunPlanWithStages`](../../internal/macosuser/runplan.go) | Both `PackRoot` and the copy commands use only `cname`; bootstrap and session `YOLO_PACK_ROOT` name that same reusable destination. |
| [`placeModuleDirsInGuest`](../../internal/cli/run/macosuserguestdaemons.go) | Independently computes that destination from `runtime.FromWorkspace(o.Workspace)`, then places each module at its relative path under it. |
| [`jailDaemonsFor`](../../internal/cli/run/packservices.go), [`Run`](../../internal/cli/run/run.go) | Placement happens before the guest split and payload. The backend receives the same launch's `staged.root`, but its supervisor payload has already been placed at the reusable path. |
| [`BuildCapturePlan`](../../internal/macosuser/capture.go) | Uses `cnameFor(stagingRoot)` for packs too. The capture staging root is per program; repeated captures have the same identity there. Its cleanup lists staging home/output, profile and env/CA files, **not** the guest pack copy. Fork builds embed this capture plan. |
| [`runCaptureJail`](../../internal/cli/capturehost.go) | The macos-user callback receives the pipeline's fresh `packRoot` and passes it as `HostPackRoot` to both install capture and fork-build capture; `cleanupCaptureWorkspace` deletes host staging, not `/var/yolo-jail/packs`. |
| [`superviseOne`, `child.start`](../../internal/supervisor/supervisor.go) | Restart executes the stored argv again. That argv still names the reusable guest path, so its next exec reads whatever a later launch copied there. |

Consequently B can remove A's directory while A is running, then supply changed code or omit a
module A's restart still names. The host tree is already unique; the defect is the guest
**destination**, not host pack selection, token resolution semantics or the daemon's restart policy.

## The correction: one destination, one writer, no reuse

**Reversible implementation direction under [JD-10](#JD-10), not a new owner ruling.**

1. **Reuse the host tree's identity.** The guest destination is a flat sibling of the legacy
   workspace tree: `/var/yolo-jail/packs/<cname>.<tree-id>`. The tree id is the actual host
   staged tree's directory leaf. It is already unique before daemon composition; do not move
   session-id minting earlier or add an independent random id/content cache. A local single
   path component is required; empty source means no tree and no commands.
2. **One calculation serves every reader.** Pack staging, the run plan, capture plan,
   bootstrap/session `YOLO_PACK_ROOT` and module-dir payload placement use the same helper
   over the exact host tree supplied by the pipeline. Preserve the module's relative path
   and every argv token's existing meaning. Symlinked host spellings must agree between
   placement and the plan; retain `relUnder`'s containment check and outside-tree decline.
3. **Create, never replace or merge.** Only this launch's authenticated staging commands
   populate its destination. Reserve the destination exclusively before copying contents;
   an existing destination is a staging failure, never a reason to delete, adopt or overwrite
   it. Populate before starting any consumer; after success nothing yolo runs edits the tree.
   Keep files readable and existing execute bits, with no guest write permission. This uses
   the existing root-staging authority, not new sudoers, helpers or profile grants.
4. **Different selections are different trees.** Changed bytes, a dropped module and an
   unchanged selection on another launch each go into that other launch's tree. They cannot
   alter A's tree or its restart argv. A pack-less launch never falls back to a legacy tree.
5. **Plan rendering stays side-effect free.** Derive the destination from the supplied staged
   host tree; no root directory, lease, session id or cleanup is created by pure plan building.
   The existing CLI dry-run pipeline may stage its own host comparison tree; this adds no
   guest-side effect. No directory existence probe is needed to render the copy commands.

A create-once directory, filled before any consumer starts, needs no replace-by-rename window.
An interrupted or failed stage leaves only its exact partial tree; it never clears another
launch's destination. A staging refusal names the exact conflict/failure and offers a new launch
for a fresh id, with inspection of the named retained path if repeated; no instruction deletes
possibly held bytes as the price of launching.

## Ownership and cleanup: unknown keeps the bytes

The existing [workspace keeper](jail-lifetime-last-session-wins.md#994-what-the-keeper-owns-there)
holds host services and its host pack tree, **not** root staging or guest supervisors. Each
session still runs its own sudo stages, supervisor and sandbox. Its unique guest tree is owned
by that launch and is never handed to the keeper or reclaimed by its last-session count.
The existing [workspace launch lock](../reference/macos-user-provisioning.md#the-workspace-lock)
still protects shared provisioning; holding it for the session would merely serialize terminals.

**Cleanup removes only an exact owned tree, after positive proof of no holder.** No age rule,
account process scan, workspace-root removal or inherited reusable-tree sweep participates.

| State | Disposition |
| :--- | :--- |
| No source / no destination created | Nothing to remove. |
| This launch exclusively created the tree; staging has completed/unwound, no stage writer remains, and no consumer was ever dispatched | Known no holder by construction: remove only this exact tree on the launch's failure/return path. Never remove an existing directory whose exclusive reservation failed. |
| Guest consumer dispatched; all consumers positively known ended, including supervisor and module-dir descendants/restart capability | Remove the exact tree, after stopping the supervisor and before releasing any record that still tracks this cleanup. A test may supply this proof; production must actually possess it. |
| Launcher died, record is missing/free/unreadable, keeper ended, sudo exited, `Background.Exited` closed, or any descendant status cannot be established | **Unknown: retain.** None of these observations alone proves every holder gone. No automatic pack removal is added to the existing session-file sweep. |
| Proven-safe exact removal fails, including expired/noninteractive sudo | Retain the bytes; keep any existing record that specifically tracks the failed removal and report the exact path and recovery step. Never convert cleanup failure to the session's exit failure. |

**The current lifecycle has no complete post-consumer no-holder proof.**
[`startBackgroundReal`](../../internal/macosuser/real.go) waits for the sudo launcher and may
fall back to SIGKILL, which sudo does not relay; `Background.Stop` returns no descendant result.
The agent or a module-dir program can also leave the tracked process group. Therefore this
bounded build **retains trees after any consumer was dispatched**, including an ordinary session
or capture return. Do not weaken this rule to get clean directories in a smoke test.
A future guest-held lease or stronger lifecycle evidence is optional reclamation work, not a
prerequisite for closing the replacement defect.

The session's existing [liveness record](../reference/macos-user-provisioning.md#the-session-key)
continues to protect/sweep its env, profile and CA files. Its free flock proves the launcher
ended, not the guest's whole process population; leave that contract intact. Do not infer a
root tree path from a malformed record, add guest-tree removal to that sweep, or make record
failure a new launch prerequisite.

**Recovery disclosure:** say which exact guest tree was retained and why, and direct the user
to `yolo run --dry-run` for the workspace's paths/argv and `yolo stores` for storage inspection.
To end known workspace sessions use `yolo stop` from that workspace. This is **not** proof that
all guest descendants vanished; if any may remain, keep the tree. Only after the user confirms
no process uses or can restart from that exact path, an explicit `sudo /bin/rm -rf -- <exact-tree>`
may reclaim it. Never offer a glob, `packs/<cname>` blanket removal or account-wide termination.

## Capture, compatibility and scope

- **Capture follows the same destination rule.** Its per-program capture lock protects its
  neutral staging home, not an old guest daemon's bytes. Every pipeline invocation supplies
  its own host tree; sequential captures of the same program therefore keep distinct guest
  copies. Capture/fork cleanup must not delete a guest tree merely because the driver returned.
  Standalone pure builders given one host tree derive one destination; a live duplicate stage
  refuses rather than replacing it.
- **Existing workspace trees are untouched.** `/var/yolo-jail/packs/<cname>` and its old `.new`
  may belong to old sessions with no trustworthy record. New launches neither read, replace
  nor collect them. The new flat siblings avoid a migration through an old live tree.
- **No container change.** Podman mounts, Apple Container copies, attach behavior and
  [the immutable-tree ruling](../reference/pack-system.md#oq-pk2) remain as built.
- **No revival of [HD-R1](host-daemon-ownership.md#HD-R1)**, no new privilege prerequisite,
  keychain decision, account layout, shared binary-prefix lifetime, endpoint policy,
  guest-port collision policy, restart policy, or daemon-decline rule. Those remain their
  authorities' work. This tree is readable under the existing profiles; native access is
  still proof owed, not asserted here.

## Cost and observable acceptance

One guest copy per launch costs more retained disk than a reusable workspace copy. Unknown
post-consumer liveness can keep it indefinitely; that is the explicit trade for never changing
or deleting live module bytes. Content deduplication and automatic crash collection are not part
of this correction.

Acceptance requires overlapping A/B launches with distinct roots: B changes and then drops a
module while A reads and restarts from A's original bytes. A later capture cannot touch either
root. Known pre-consumer unwind removes only what it created; every unknown/post-consumer
case retains. The old one-session Mac smoke test does not prove these properties. The handoff
names source-test mutations and the separate native proof still owed.

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
| <a id="JD-9"></a>JD-9 | *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible.* **The one guest rule for a pack service's daemon: the guest declines it only when (a) the service serves a protocol adaptation and this launch admits its host half, or (b) it declares an `endpoint`.** (a) is the wire bridge: its host half runs launch-owned when a profiled agent's pairing needs it (NC-D65), and running the jail daemon too would serve one address twice. (b) is a file the daemon publishes under `/run/yolo-services`, a container path with no sandbox counterpart. Everything else runs confined in the guest, as a container runs it: a service that declares only a `jail_daemon`; a service whose host half the launch refuses, because a pack yolo does not ship declares it ([OQ-HS4](host-notch-services.md#OQ-HS4)), which the launch says in a `Not started outside the sandbox:` line naming the pack and the reason; and a **pure worker**, `packdecl`'s word for a service that publishes no endpoint, when it also serves no adaptation, so its host half has no pairing to serve here. A worker that declares both halves runs its jail half in the guest and its host half is not started: confinement is preferred. **Why:** [OQ-DP8](declaration-parity.md#OQ-DP8)'s *"if you would have run it in the jail container, you run it on the guest"*, [OQ-DP9](declaration-parity.md#OQ-DP9)'s confinement, and [HS-D15](host-notch-services.md#HS-D15)'s doorway precedent, where a refused host argv's jail daemon also runs in the guest. The old rule declined every service's daemon for "its host half runs instead", which was false for every service but the bridge, so those ran nowhere. JD-4's question is unchanged: the bridge is still declined, now by (a). Built in `loopholes.JailDaemonsRunIn` over specs from `launchservice.ServiceJailDaemons`, admitted by `launchservice.AdmitServiceHosts`, both read by the launch and by `yolo check` | built 2026-10-04; [the decline list](../reference/macos-user-nix-and-features.md#the-jail-daemons-run-in-the-sandbox) once re-verified |
| <a id="JD-10"></a>JD-10 | *Implementation decision, taken under the maintainer's 2026-10-04 delegation ("make them and build it … adjust later"); reversible.* **`{jail_loophole_dir}` resolves to where the jail sees the module directory: in the sandbox, its place in the root-owned copy of the launch's staged packs, under `/var/yolo-jail/packs/<jail name>/` at the same relative path as in the host-side staged tree.** The orchestrator already copies the staged tree there, world-readable and keeping each file's exec bits, for the bootstrap to render from, so a program a pack configured by path ships in its folder runs in the guest from that copy. **⚠ That copy is not a container's per-launch tree.** It is one per workspace, keyed by the jail name, and every launch of the workspace replaces it (`macosuser.StagePackCommands` removes it and renames the new copy into place), while a container runs from the tree its own launch staged ([OQ-PK2](../reference/pack-system.md#oq-pk2)). The bootstrap reads the copy once, at start; a guest daemon reads it for the whole session. So a second session of the same workspace, which macos-user allows, swaps the folder under the first session's running daemon: the first one's files vanish during the swap and come back as the second launch's, and a restart of its daemon runs the second launch's copy, or, when that launch dropped the pack or the loophole, finds no program and logs `spawn failed` under its restart policy for the rest of the session. **Follow-up, not built:** [one immutable guest tree per launch](#the-correction-one-destination-one-writer-no-reuse), using the existing host tree id, and [exact cleanup only on known no-holder liveness](#ownership-and-cleanup-unknown-keeps-the-bytes). Conservative retention is settled for this slice; a free launcher record or sudo exit does not prove guest descendants ended. The workspace keeper remains unchanged, as does held [HD-R1](host-daemon-ownership.md#HD-R1). Until then the guest's module-dir daemons match a container's only for one session per workspace. The guest still declines a daemon whose program there is a Linux executable (its first bytes are ELF magic; the sandbox runs macOS programs), one naming a `{jail_binary:<name>}` path (deferred, [BP-D6](broker-as-a-pack.md#BP-D6)), and one whose module directory is outside the launch's staged tree, which a launch's own loopholes never are. Each of those `Declined:` lines, and a service's endpoint decline under JD-9, names the next step, since a daemon a selected pack declared then runs nowhere: a container runtime runs it (`YOLO_RUNTIME=podman` or `YOLO_RUNTIME=container` for one launch), and for a Linux executable a script or macOS build the pack could ship instead. So does a refused host argv's `Not started outside the sandbox:` or `Not opened outside the sandbox:` line when the guest declines its jail daemon too. **The module directory outside the staged tree is declined, not refused** as the build brief proposed. No launch reaches it, because a launch discovers its pack loopholes from the packs it staged into its own tree (`run.stagePacksInto` hands those to `loopholes.SetPackModules`). And the guest declines every other daemon it cannot run as declared, such as an argv naming another loophole's mount, while it runs the rest. A refusal would also need a check in the macos-user arm ahead of its `--dry-run` branch, so that a plan render refuses too; the doorway start, the one refusing helper this build touched, runs only on the live path. **Why:** the token never named a container path: the author wrote "my loophole's folder, as the jail sees it", and the container mount point was its only resolution because a container was the only jail with a daemon supervisor. Resolving it here is not the `argv[0]` rewrite [OQ-DP8](declaration-parity.md#OQ-DP8) rejected; it is the load-time resolution done for the backend that runs the daemon. Built in `loopholes.JailDaemonSpec.InGuest`, applied by `run.placeModuleDirsInGuest` when the launch composes its payload, so the split, the decline and the supervisor's payload all read the guest's argv | built 2026-10-04; [the decline list](../reference/macos-user-nix-and-features.md#the-jail-daemons-run-in-the-sandbox) once re-verified |
