---
title: "Host agent floor — implementation sketch"
date: 2026-09-25
status: deprecated
tags: [host, provisioning, floor, plan]
summary: "Parking lot for build-level detail behind host-tool-provisioning.md: reuse of the jail launcher generator at the host, the prefix layout, the lock, and what to measure before OQ-HP3 and OQ-HP4 can be ruled. Not a hand-off; the design wins on behavior."
stage: SUPERSEDED
next: "Nothing: kept as history; host-tool-provisioning.md's ledger (HP-D4 to HP-D9) records what was built instead"
---

# Host agent floor — implementation sketch

**Status:** 2026-09-25, and **replaced by the build of 2026-09-29**: every question it
waited on is ruled, and what was built — a Go-side provisioner rather than a reused launcher
template, the `host-floor/` layout, the flock — is recorded as
[HP-D4](host-tool-provisioning.md#HP-D4) to [HP-D9](host-tool-provisioning.md#HP-D9). Read it as
history only.

**Precedence:** [`host-tool-provisioning.md`](host-tool-provisioning.md) wins on behavior. This file
holds settled detail the design doesn't need.

---

## Reuse

- **The launcher generator.** `GenerateAgentLaunchers` takes an `*entrypoint.Env` and already runs
  outside a container (macos-user, `internal/entrypoint/darwin.go`). Check whether a host-shaped
  `Env` can point `LaunchDir`, `NpmBin` and the stamp dir at the prefix without a second template.
  A second copy of the launcher body is the drift to avoid.
- **The platform check, and not the shadow check.** Reuse the platform predicate
  `packdecl.Install.UnpublishedReason` (was `launcherUnpublished`). Do not reuse `launcherShadows`
  (`internal/entrypoint/launchercollision.go`) at the host, on any probe path. The jail runs it
  so a launcher never shadows a tool the image provides. The floor has nothing to shadow: its
  `bin/` is last on the agent's PATH ([HE-D1](host-launch-environment.md#he-d1)), and no copy
  already on the machine stops the floor installing its own
  ([§4 of the design](host-tool-provisioning.md#4-when-provisioning-runs),
  [HP-DIR4](host-tool-provisioning.md#HP-DIR4)).
  - **Never probe the launch PATH for it.** That PATH is whatever the launcher handed yolo
    ([`host-launch-environment.md` §2.2](host-launch-environment.md#22-the-launch-path-and-what-host_path-adds)).
    A terminal's PATH holds `~/.local/bin/claude`, so a terminal launch would write no floor entry
    and a Waybar launch would. What the floor holds would then depend on who started yolo, and
    [HE-DIR1](host-launch-environment.md#he-dir1) keeps it fixed.
  - **A terminal's PATH also holds the user's install dirs** (`~/.local/bin`, the npm prefix).
    Probing them is the kill switch `launchercollision.go` warns about: after the first install
    the check finds the installed copy, so evergreen stops.
- **The Node floor resolution.** `internal/entrypoint/nodefloor.go` and `packdecl/nodefloor.go`
  for the exec prefix.
- **Consent recording.** Look at how fetched-pack host approvals are recorded before inventing a
  store for [§4](host-tool-provisioning.md#4-when-provisioning-runs)'s per-program consent.

## Layout

A proposal. Names are the implementer's.

```text
~/.local/share/yolo-jail/host-tools/     0700
  bin/<name>                             generated launchers, one per provisioned entry
  npm/                                   prefix-private npm prefix
  node/<version>/                        OQ-HP4 (a)
  locks/<name>.lock
  staging/<name>-<nonce>/
  receipts/<name>.json
```

Add the path to the `paths` package next to `WrapDir`, and add a test that no mount source emitted
by `internal/cli/run` is at or under it.

## Measure before ruling

- [OQ-HP3](host-tool-provisioning.md#OQ-HP3) (a): does a `yolo capture claude` tree, materialized
  on a Linux host, run outside the jail's `/lib` farm
  ([`mise-node-dynamic-linking.md`](../reference/mise-node-dynamic-linking.md))? Where does
  `claude`'s self-updater write when `HOME` is the real home?
- [OQ-HP6](host-tool-provisioning.md#OQ-HP6): does mise's `mise install <tool>` evaluate `[env]`
  templates? What does it do on an untrusted config, non-interactively and at a TTY? Does mise's
  own auto-install setting already install a missing version when a shim is exec'd? If so, the
  verdict may need to read that setting rather than report "stale".

## Order

1. The prefix, the lock and `yolo check`'s rows, report-only. This depends on no question except
   [OQ-HP1](host-tool-provisioning.md#OQ-HP1).
2. npm entries through `yolo host apply`'s consent. This waits on [OQ-HP4](host-tool-provisioning.md#OQ-HP4).
3. Launch-time install at a TTY, and the evergreen refresh. This waits on
   [OQ-HP5](host-tool-provisioning.md#OQ-HP5).
4. Installer entries. This waits on [OQ-HP3](host-tool-provisioning.md#OQ-HP3) and its measurement.
5. The mise remedy. This waits on [OQ-HP6](host-tool-provisioning.md#OQ-HP6).
