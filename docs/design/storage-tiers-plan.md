---
status: draft
stage: SKETCH
next: "Resolve the design questions, then re-read the tree and complete the implementation plan"
depends-on:
  - storage-tiers.md#OQ-BS1
  - storage-tiers.md#OQ-BS2
  - storage-tiers.md#OQ-BS3
  - storage-tiers.md#OQ-BS4
tags: [storage, implementation-sketch]
---

# Storage placement implementation sketch — not a build handoff

**Status:** 2026-10-08. Incomplete and unstable while design questions are open; written against `ab6ee853`.

**Design:** [Keep code fast, and give large agent files somewhere else to go](storage-tiers.md).
The design wins on behavior; the tree wins on fact; this sketch is advice and the first thing to become stale. Do not implement from it.

## Reuse and traps worth retaining

| Evidence in today's tree | Why the builder should inspect it |
| :--- | :--- |
| [Cache relocation loader](../../internal/config/relocations.go#L26-L103) | Direct trusted-user-scope reads are the authority boundary; merged config and in-jail snapshots are not equivalent. Advice: reuse that loading shape, not its availability policy. |
| [Durable directory launch handling](../../internal/cli/run/durabledir.go) | Fresh launch versus attach already has frozen-environment semantics. Its nonfatal allocation failure is not the proposed bulk policy. |
| [Durable directory creation](../../internal/durable/durable.go#L73-L93) | Managed children are opened beneath a confined root, and links are refused rather than followed. Advice: inspect existing path helpers before inventing another walker. |
| [Writable-source guard](../../internal/paths/workspacescope.go), [context mounts](../../internal/config/mounts.go) | Host scope overlap is resolved-path logic, not a string-prefix test. The existing managed-state exception must not license arbitrary external roots inside host state. |
| [Durable reporting](../../internal/durable/report.go) | Metadata-only reporting has bounded walks. Do not copy its recursive size measurement into a bulk startup path. |
| [Storage-class briefing](../design/durable-scratch-space.md#51-the-storage-class-map-and-the-briefing-section) | Storage guidance and emitted mounts must agree; core must not hardcode an agent name to introduce another destination. |

## What waits on the rulings

- [OQ-BS1](storage-tiers.md#OQ-BS1) controls whether the single-object schema is retained; do not add it to the config reference yet.
- [OQ-BS2](storage-tiers.md#OQ-BS2) determines whether trusted per-workspace configuration needs a new switch and owner-facing command flow.
- [OQ-BS3](storage-tiers.md#OQ-BS3) controls filesystem identity admission. Advice: investigate Linux mounted-filesystem-to-UUID resolution without assuming the invoking agent has root or `blkid` privileges.
- [OQ-BS4](storage-tiers.md#OQ-BS4) determines native backend work. Linux nested Podman forces user namespaces off; it cannot verify a real rootless ownership mapping. Mac delivery requires native evidence, not cross-compilation alone.

## Complete before handoff

Re-read selected-source handling, launch/attach ordering, inventory, and briefing call sites after the rulings. Supply a bounded file map and tests that exercise the production caller, not only a standalone resolver. Include the missing-drive/underlying-filesystem case, workspace separation, restart survival, nondeletion, and real rootless permissions.

Advice: use fixture-sized writes for automated lifecycle tests; a genuinely large manual write proves capacity placement without forcing every CI job to allocate gigabytes. Documentation to reconcile includes the storage reference, agent briefing authority, supported-settings matrix, user storage guide, and config reference. Update release notes only when the behavior ships.
