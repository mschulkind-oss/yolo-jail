---
title: "Host agent floor — implementation sketch"
date: 2026-09-25
status: draft
tags: [host, provisioning, floor, plan]
summary: "Parking lot for build-level detail behind host-tool-provisioning.md: reuse of the jail launcher generator at the host, the prefix layout, the lock, and what to measure before OQ-HP3 and OQ-HP4 can be ruled. Not a hand-off; the design wins on behavior."
---

# Host agent floor — implementation sketch

**Status:** SKETCH, 2026-09-25. It is incomplete, and unstable while [OQ-HP1](host-tool-provisioning.md#OQ-HP1)
through [OQ-HP6](host-tool-provisioning.md#OQ-HP6) are open. Do not build from it.

**Precedence:** [`host-tool-provisioning.md`](host-tool-provisioning.md) wins on behavior. This file
holds settled detail the design doesn't need.

---

## Reuse

- **The launcher generator.** `GenerateAgentLaunchers` takes an `*entrypoint.Env` and already runs
  outside a container (macos-user, `internal/entrypoint/darwin.go`). Check whether a host-shaped
  `Env` can point `LaunchDir`, `NpmBin` and the stamp dir at the prefix without a second template.
  A second copy of the launcher body is the drift to avoid.
- **The collision and platform checks.** `launcherShadows` and `launcherUnpublished`
  (`internal/entrypoint/launchercollision.go`). The shadow check's probe path at the host is the
  composed host PATH minus the prefix itself. Never add the prefix's own install dirs, for the
  reason `launchercollision.go` gives: that turns evergreen off after the first install.
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
