---
title: "Jail daemons on macos-user — the guest pack-tree lifetime follow-up"
date: 2026-09-17
status: accepted
tags: [macos-user, loopholes, jail-daemon, parity, lifetime, design]
summary: "The original jail-daemon build graduated; JD-4 remains a maintainer follow-up. JD-10's guest pack-tree lifetime correction is built in source: each launch uses its own immutable guest copy and retains it whenever writer or consumer liveness is uncertain; native macOS behavior remains unmeasured."
stage: DECIDED
next: "Run the native overlapping-session, restart, and capture proof for JD-10; ownership, permissions, Seatbelt behavior, and those concurrent cases remain unmeasured"
vantage:
  status-chip: true
---

# Jail daemons on macos-user — the guest pack-tree lifetime follow-up

**Status:** 2026-10-08 — JD-10's guest pack-tree correction is implemented in the source candidate. The parent-owned format, `check-ci`, Darwin/arm64 vet and build, fresh-built nested launch, and full applicable integration gates passed. The full integration run executed no native macos-user tests: 0 executed and 91 skipped. Native root ownership and modes, Seatbelt execution, overlapping sessions and restart behavior, and capture isolation remain **UNMEASURED**; the source gates do not establish them. The earlier single-session evidence covers only the original daemon-start behavior.

> **In short.** Each launch keeps the guest pack bytes it selected; another terminal cannot
> replace them. Lack of proof that a writer or the last consumer ended keeps the tree, not a guess at cleanup.

**Needs your ruling:** none for this correction. [JD-4](#JD-4) remains the maintainer's
separate follow-up; native privileges, keychain decisions and held host-daemon work are unchanged.

**Reads with:** [the jail-daemon reference](../reference/macos-user-nix-and-features.md#the-jail-daemons-run-in-the-sandbox), [the module-dir token reference](../reference/loophole-system.md#two-module-dir-tokens-and-value-sanitation), [the immutable-pack-tree ruling](../reference/pack-system.md#oq-pk2), [the session key](../reference/macos-user-provisioning.md#the-session-key), and [the macos-user keeper](jail-lifetime-last-session-wins.md#99-the-keeper-at-yolo-host-and-macos-user) (what remains workspace-owned).

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

## Source implementation

The source candidate now preserves each launch's selected pack bytes. The important seams are
shared so plan rendering, staged contents, environment and module-daemon argv agree:

| Source | Built behavior |
| :--- | :--- |
| [`StagedPackTreeRoot`, `StagePackCommands`](../../internal/macosuser/macosuser.go) | The guest destination is a sibling keyed by the workspace name and the supplied host pack tree's unique directory leaf. Staging reserves it with exclusive `mkdir`, copies into it and never replaces or merges an existing destination. |
| [`BuildRunPlanWithStages`](../../internal/macosuser/runplan.go), [`BuildCapturePlan`](../../internal/macosuser/capture.go) | Run and capture bootstrap/session environments name their own guest tree. Capture plan construction accepts nil environment input and copies the environment when adding `YOLO_PACK_ROOT`, leaving the caller's map unchanged. |
| [`placeModuleDirsInGuest`](../../internal/cli/run/macosuserguestdaemons.go) | Module-dir daemon argv resolve under that same guest destination, preserving the module's path relative to its host pack tree. |
| [`RunMacosUser`, `finishPackTree`](../../internal/macosuser/orchestrator.go), [`runCaptureSteps`](../../internal/macosuser/capture.go) | Executors claim only a successful exclusive reservation, mark writers before dispatch, and mark the bootstrap consumer before dispatch. They remove only their own reservation before any writer or consumer could be active; afterward they retain and disclose the exact tree. |

Source-level and parent gates passed, but no native macOS execution is claimed. Ownership,
permissions, Seatbelt execution, overlapping launch and restart behavior, and capture isolation
remain **UNMEASURED**.

## Source behavior: one destination, one writer, no reuse

The implementation follows the reversible [JD-10](#JD-10) direction without changing the
keeper, privilege boundary, supervisor policy or daemon-decline rules.

1. **Reuse the host tree's identity.** The guest destination is a flat sibling of the legacy
   workspace tree: `/var/yolo-jail/packs/<cname>.<tree-id>`. The tree id is the actual host
   staged tree's directory leaf, not a content digest or a newly minted session id. An empty
   host source means no tree and no staging commands.
2. **One calculation serves every reader.** Pack staging, run and capture plans,
   bootstrap/session `YOLO_PACK_ROOT` and module-dir payload placement use the same helper over
   the exact host tree supplied by the pipeline. The module's relative path and every argv
   token's existing meaning remain intact. Symlinked host spellings agree between placement
   and the plan; containment checks and outside-tree declines remain in force.
3. **Create, never replace or merge.** Only this launch's staging commands populate its
   destination. Exclusive reservation precedes copying; an existing destination is a staging
   failure, never a reason to delete, adopt or overwrite it. Copying finishes before a guest
   consumer starts; after success yolo does not edit the tree. Files remain readable, existing
   execute bits are preserved, and the guest cannot write the tree. No new privilege is added.
4. **Different selections are different trees.** Changed bytes, a dropped module and an
   unchanged selection on another launch each use that launch's tree. They cannot alter an
   earlier launch's tree or restart argv. A pack-less launch never falls back to a legacy tree.
5. **Plan rendering stays side-effect free.** Pure plan builders derive the destination from
   the supplied staged host tree; they create no guest directory, lease, session id or cleanup.
   The CLI dry-run may stage its own host comparison tree, but creates no guest-side tree.

A create-once directory is filled before any consumer starts. An interrupted or failed stage
never clears another launch's destination. A reservation refusal names the exact conflict,
recommends a fresh invocation and exact-path inspection with `sudo ls -la -- <path>`, and says
not to remove the collision.

## Ownership and cleanup: unknown keeps the bytes

The existing [workspace keeper](jail-lifetime-last-session-wins.md#994-what-the-keeper-owns-there)
holds host services and its host pack tree, **not** root staging or guest supervisors. Each
session still runs its own sudo stages, supervisor and sandbox. Its unique guest tree is owned
by that launch and is never handed to the keeper or reclaimed by its last-session count.
The existing [workspace launch lock](../reference/macos-user-provisioning.md#the-workspace-lock)
still protects shared provisioning; holding it for the session would merely serialize terminals.

**A tree is removed only while this launch owns its reservation and no writer or consumer was dispatched.** No age rule, account process scan, workspace-root removal or inherited reusable-tree sweep participates.

| State | Disposition |
| :--- | :--- |
| No source / no destination created | Nothing to remove. |
| Exclusive reservation fails | The existing destination is untouched and is not this launch's to remove. The refusal names the exact path, recommends a fresh invocation and `sudo ls -la -- <path>`, and says not to remove it. |
| This launch reserved the destination; no pack writer or guest consumer was dispatched | Remove only this exact owned tree on the launch's return path. |
| Any pack copy/chmod writer was dispatched | Retain and disclose the exact tree. The sudo wrapper's return does not prove that its writer stopped. |
| Bootstrap or another guest consumer was dispatched | Retain and disclose the exact tree on every return, including ordinary completion. The current lifecycle does not prove readers and restart capability ended. |
| Exact pre-consumer cleanup fails | Retain and name the exact path and recovery command; do not turn this cleanup failure into the launch's exit failure. |

**There is no complete post-consumer no-holder proof.** The sudo launcher status and
supervisor stop do not establish that every guest reader or restart capability ended. Run and
capture executors therefore retain trees after any possible writer or guest-consumer dispatch,
even on ordinary session or capture return. The full integration gate does not measure the
native macos-user process and filesystem behavior.

The session's existing [liveness record](../reference/macos-user-provisioning.md#the-session-key)
continues to protect and sweep its env, profile and CA files. Its free flock proves the launcher
ended, not the guest's whole process population; that contract is unchanged. No guest-tree
removal is added to that sweep, and record failure is not a new launch prerequisite.

**Recovery disclosure:** a reservation collision names its exact untouched destination and
recommends a fresh invocation plus inspection with `sudo ls -la -- <exact-path>`; it explicitly
says not to remove the collision. If a writer or guest consumer may still use a retained tree,
the disclosure names that exact path, recommends the same exact-path inspection and says not
to remove it while use is possible. A failed cleanup before any writer or consumer was
dispatched names only the owned path and the exact removal command. No glob, legacy-tree
removal or account-wide termination is offered.

## Capture, compatibility and scope

- **Capture follows the same destination rule.** Every pipeline invocation supplies its own
  host tree; repeated capture of one program therefore gets a distinct guest tree. Capture and
  fork-build plan construction treats nil environment as empty and adds `YOLO_PACK_ROOT` to a
  copy, leaving the caller's environment unchanged. Capture cleanup does not delete a guest
  tree after writer or consumer dispatch; a returned driver is not proof of no holder.
- **Existing workspace trees are untouched.** The legacy `/var/yolo-jail/packs/<cname>` and
  its old `.new` may belong to prior sessions. New launches neither read, replace nor collect
  those paths; each new guest tree is a flat sibling keyed by the host tree's leaf.
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

**Source acceptance has passed; native acceptance remains outstanding and UNMEASURED.** It
requires overlapping A/B launches with distinct roots: B changes and then drops a module while
A reads and restarts from A's original bytes. A later capture cannot touch either root. The
native run must also establish root ownership and modes, Seatbelt execution, collision behavior
and capture isolation. The existing source gates and skipped native tests do not establish
those properties.

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
| <a id="JD-10"></a>JD-10 | *Implementation decision, taken under the maintainer's 2026-10-04 delegation; reversible.* **Each launch's `{jail_loophole_dir}` resolves into a unique root-owned sibling tree keyed by the supplied host pack tree's leaf.** The launch and capture plans, bootstrap/session `YOLO_PACK_ROOT` and module-dir daemon argv use the same destination; it is exclusively reserved, populated before consumers, never replaces a collision, and leaves the legacy workspace tree untouched. An exact-path reservation refusal asks for a fresh invocation and `sudo ls -la -- <path>`, not removal. Only an owned reservation with no writer or consumer dispatched may be removed. Any possible writer or consumer dispatch retains and discloses the exact tree, even on ordinary return, because no current artifact proves they ended. Capture plan construction accepts nil environment input and does not mutate the caller's environment when adding the root. Source implementation and parent gates passed; native ownership, permissions, Seatbelt, overlapping session/restart and capture behavior remain **UNMEASURED**. The workspace keeper, held [HD-R1](host-daemon-ownership.md#HD-R1), privilege prerequisites, keychain behavior and daemon-decline rules are unchanged. The guest still declines a Linux executable, a `{jail_binary:<name>}` path ([BP-D6](broker-as-a-pack.md#BP-D6)), an outside-tree module directory and the JD-9 endpoint shape. See the [jail-daemon reference](../reference/macos-user-nix-and-features.md#jd-10) and [module-dir token reference](../reference/loophole-system.md#two-module-dir-tokens-and-value-sanitation). | built in source 2026-10-08; native behavior remains unmeasured |
