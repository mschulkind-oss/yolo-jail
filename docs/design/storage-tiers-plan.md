---
status: draft
stage: SKETCH
next: "Resolve the design questions, then re-read the tree and complete the implementation plan"
depends-on:
  - storage-tiers.md#OQ-BS2
  - storage-tiers.md#OQ-BS3
  - storage-tiers.md#OQ-BS4
  - storage-tiers.md#OQ-BS5
tags: [storage, tiers, implementation-sketch]
---

# Storage tiers implementation sketch — not a build hand-off

**Status:** 2026-10-09. Incomplete and unstable while design questions are open; re-checked against
`ccf073633`.

**Design:** [One workspace across several disks, seen as one tree](storage-tiers.md). The design
wins on behavior and the tree wins on fact. This sketch is advice and will be the first part to go
stale. Do not implement from it.

## Reuse and traps worth retaining

| Evidence in today's tree | Why the builder should inspect it |
| :--- | :--- |
| [Per-side shadows](../../internal/cli/run/mounts.go#L105-L148) (`venvShadowMountArgs`) | A jail-only part changes only the bind source this function picks. It skips a linked path today ([`mounts.go:116`](../../internal/cli/run/mounts.go#L116-L121)), and that skip has to stay for any link the tier step did not make. Its backing directories are made with `ensureBindSourceDir` beneath the workspace state; a tier directory needs the same no-follow creation beneath the tier root |
| [Per-side set](../../internal/perside/perside.go) (`ShadowCandidates`, `ValidRel`) | The one authority on which parts are jail-only. Reuse `ValidRel` for rule paths and add the [§4.2](storage-tiers.md#42-what-may-be-a-part) refusals around it |
| [Workspace-state operations beneath a root](../../internal/cli/run/wsstatebeneath.go) | The pattern for writing `.git/info/exclude` and laying links in a tree the jail can write |
| [Writable-source guard](../../internal/paths/workspacescope.go) (`WritableSourceScopeBreach`), [rw-mount refusal](../../internal/config/mounts.go) (`rwMountRefusal`) | Resolved-path containment, and the either-direction workspace overlap clause |
| [Cache relocation loader](../../internal/config/relocations.go) (`LoadCacheRelocations`) | Direct trusted user-scope reads; the merged config and in-jail snapshot are not equivalent. Reuse the loading shape, not its parent-exists availability policy ([`ensure.go`](../../internal/storage/ensure.go)) |
| [Per-workspace file](../../internal/config/workspacefile.go) | Where `yolo tiers set` writes rules, and the `<folder>-<hash>` naming the workspace id reuses |
| [Durable reporting](../../internal/durable/report.go) (`Measure`, `WalkBudget`) | The on-demand size walk for `yolo stores`. Never run it on the launch path, where it would spin up an idle HDD |
| [macos-user links](../../internal/macosuser/ctxlinks.go) | The second delivery's precedent: links plus Seatbelt rules, and the `/Volumes` admission with a write probe |
| The launch lock (`holdLaunchLock`) | The per-workspace tier lock must serialize launches and `yolo tiers` commands without being held across the container run |

## What waits on the rulings

- [OQ-BS2](storage-tiers.md#OQ-BS2) decides whether `storage_tiers` gains a defaults member and
  whether the apply step reads two rule sources.
- [OQ-BS3](storage-tiers.md#OQ-BS3) decides what `init` records. Advice for option A: find the
  containing mount by statx mount id against `/proc/self/mountinfo`'s first field; record mount
  point and filesystem type in host state under `~/.local/share/yolo-jail`, never in the jail-mounted
  cache.
- [OQ-BS4](storage-tiers.md#OQ-BS4) decides the macos-user work and whether rootful is refused.
- [OQ-BS5](storage-tiers.md#OQ-BS5) decides whether jail-only parts ever get a host link. B needs
  the mirror-path shadow measured in [Appendix A](storage-tiers.md#appendix-a-what-was-measured-in-this-jail) (A2b).

## Complete before hand-off

Re-read the mount assembly order, the attach path, `yolo stores`, the reapers and the briefing's
storage section after the rulings. Supply a bounded file map, and tests that exercise the production
caller and not just a standalone resolver. Cover these cases: an unmounted disk at its mountpoint,
a rewritten link that must not change a mount, a moved workspace, an interrupted move, the
trailing-slash `.gitignore` case, and real rootless ownership. A nested jail cannot verify the
last one.

Advice: use fixture-sized trees for the move tests. Documents to reconcile when it ships: the
storage reference, the jail-home mount table, the briefing authority, the supported-settings
matrix, the config reference and the user storage guide.
