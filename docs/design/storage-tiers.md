---
status: in-review
stage: DESIGN
next: "Rule OQ-BS1 through OQ-BS4 before completing the implementation plan"
tags: [design, storage, scratch, placement]
summary: "Keep code and working trees on fast storage; expose a separate workspace-private directory for large agent scratch files on an owner-selected disk."
---

# Keep code fast, and give large agent files somewhere else to go

**Status:** 2026-10-08. Proposal only; current storage paths and scope rules checked against `ab6ee853`. No runtime or disk-performance measurement made.

> **In short.** Storage placement and storage lifetime are different decisions. Give agents an explicit bulk scratch path on an owner-selected disk, without moving their code, changing restart survival, or automatically shuffling files.

**Why it matters.** A restart-safe directory inside an SSD-backed workspace is still on the SSD; large downloads and intermediates compete with code for its capacity.

**The shape.** Trusted host configuration selects storage; the launcher exposes only this workspace's directory; the environment briefing tells every agent what belongs there.

**Cost.** External scratch outlives a deleted workspace and needs manual cleanup. A configured but unavailable disk prevents a fresh launch rather than consuming SSD space silently.

**Start at [The proposed split](#3-the-proposed-split)** — two destinations, not a storage migration engine.

**Needs your ruling:** [OQ-BS1](#OQ-BS1), [OQ-BS2](#OQ-BS2), [OQ-BS3](#OQ-BS3), [OQ-BS4](#OQ-BS4).

**Reads with:** [implementation sketch](storage-tiers-plan.md) (incomplete, not a build handoff), [tool-store relocation](shared-tool-store-relocation.md) (separate store selection), [existing durable scratch](durable-scratch-space.md) (lifetime and guidance).

---

## 1. Recommendation and boundaries

I recommend **explicit placement before automatic tiering**. In ordinary storage terminology, a *storage tier* groups storage with different cost, capacity, or performance. Here that means an SSD for frequent small accesses and a capacity-oriented drive for large files; yolo does not infer a drive's speed from its name or promise a performance class.

**Bulk scratch** *(coined here)* means workspace-private agent scratch placed on that second destination. It is not a backup, a shared dataset service, a tool cache, or the final home of a user deliverable. “Bulk” expresses the intended workload, not an enforced minimum file size.

Yolo manages the agent's environment first: it supplies a usable path and truthful instructions. A jail additionally limits which host directory the agent can reach. The initial proposal is for jail launches, not a new host-agent storage grant.

- **P1 — Placement does not imply lifetime.** Both existing durable scratch and bulk scratch survive jail restarts; neither is backed up by yolo.
- **P2 — No implicit SSD fallback.** An explicit bulk destination either works or produces a refusal with a next step.
- **P3 — Narrow grants.** A jail sees one workspace's child directory, never the entire bulk root or another workspace's contents.
- **P4 — No hidden movement or deletion.** Yolo creates directories and exposes them; agents choose what to write, and yolo never relocates or reclaims their contents.
- **P5 — No agent-specific mechanism.** Core provides the path and storage facts through existing briefing composition, not a named agent's hook.

### Non-goals

- Detecting SSDs, HDDs, access frequency, or file temperature (how recently or often a file is accessed).
- Automatically moving files by size, age, free space, or access patterns.
- Moving `/tmp`, the workspace, agent homes, the Nix store, container images, caches, or `/mise`.
- Quotas, reservations, deduplication, compression, backups, or a cleanup daemon.
- Persistent cross-workspace writable datasets or remote/object storage.
- Making an existing tool write somewhere else without that tool's own output-path option.

## 2. What exists, and what is missing

Source claims below were checked on 2026-10-08 against the header's commit; links are evidence, not an edit plan.

| Existing capability | What it solves | What it does not solve |
| :--- | :--- | :--- |
| `YOLO_DURABLE_DIR` at `<workspace>/.yolo/durable` | Restart-safe agent scratch, reached through the workspace bind; launcher exports it only when available. [Directory contract](../../internal/durable/durable.go#L31-L93), [launch behavior](../../internal/cli/run/durabledir.go#L31-L55) | Choosing another physical filesystem while code stays put. |
| `cache_relocations` | User-scope host directories behind selected cache subdirectories, loaded directly from trusted host config. [Loader](../../internal/config/relocations.go#L26-L103) | A discoverable destination for arbitrary agent-generated files. |
| Read-write context mounts | Explicit host directory access with fixed destination and scope checks. [Mount schema](../../internal/config/mounts.go#L33-L53) | A standard storage role, per-workspace allocation, or automatic agent guidance. |
| Shared tool-store relocation proposal | Selecting the jail's shared mise/Rust/Cargo store while preserving `/mise`. [Design](shared-tool-store-relocation.md#3-one-selected-store-everywhere) | Agent scratch placement; that proposal is not built. |

The missing capability is **an agent-neutral place to put capacity-heavy scratch without putting it beside code**. Ordinary read-write mounts can supply the bytes today, but each owner must invent the layout and instructions.

### Existing rulings this does not reopen

- [DS-D9](durable-scratch-space.md#DS-D9), ruled 2026-09-28, rejected moving default durable scratch outside the workspace. Keep that default unchanged; the new capacity destination is additive and opt-in.
- [DS-D35](durable-scratch-space.md#DS-D35), ruled 2026-10-02, says durable scratch is temporary agent work that must survive reboots, not project content or a place the user must browse. Apply that purpose to bulk scratch too.
- [Cache relocation's scope boundary](../plans/cache-relocation.md#threat-model-why-user-scope-is-the-whole-design) places arbitrary writable host-path grants in trusted user scope. A committed project file cannot install the proposed destination.
- The tool-store proposal's [availability question](shared-tool-store-relocation.md#OQ-STR2) is still open. Its missing-drive counterexample applies here too; this document proposes a concrete policy rather than treating that question as settled.

## 3. The proposed split

| Files | Destination | Reason |
| :--- | :--- | :--- |
| Source, active worktrees, small restart-safe intermediates | Existing workspace and `YOLO_DURABLE_DIR` | Keep frequent small/random I/O on the code filesystem. |
| Large archives, dataset downloads, generated media, expanded samples, large intermediate outputs | New `YOLO_BULK_DIR`, when present | Prefer capacity over low-latency random access. |
| Package-manager and model-tool caches | Existing cache paths; owner-configured relocations where appropriate | Tools already have cache and sharing semantics. |
| Final files meant for the project or user | Their intended project/output location | Scratch is not an artifact delivery mechanism. |
| Disposable short-lived files | Existing temporary paths | No lifetime change in this proposal. |

The bulk directory permits files of any size. There is **no size threshold** and no write interception. An agent estimates the workload before starting, uses the destination explicitly, and can leave latency-sensitive scratch on the SSD. An HDD may be worse for an expanded dataset with millions of small files than for a single large archive; placement guidance must say so.

### Proposed configuration, not an existing key

Under the narrow recommendation in [OQ-BS1](#OQ-BS1), the host user config contains one optional object:

```jsonc
{
  "bulk_storage": {
    "root": "/mnt/bulk/yolo-scratch",
    "filesystem_uuid": "<UUID of the mounted filesystem containing root>"
  }
}
```

- **Default:** absent means disabled; no directory, environment variable, mount, or new refusal.
- **Scope:** host user config and its trusted includes only; workspace, workspace-local, per-workspace switches, packs, and assembled in-jail config cannot choose a host source.
- **Shape:** require both non-empty strings; reject unknown members, null, empty objects, lists, relative paths, and conflicting duplicate keys. Expand only leading `~` or `~/`; accept spaces; reject mount-syntax ambiguity such as `:` for Podman delivery.
- **One destination:** no list, selector, default-tier name, or per-file rule. SSD/HDD is the owner's deployment choice, not a validated hardware category.
- **UUID:** a filesystem UUID is the persistent identifier assigned to a filesystem. Under [OQ-BS3](#OQ-BS3)'s recommendation, a local Linux filesystem with a verifiable UUID is required; network filesystems and unidentified filesystems are unsupported initially.

Exact key names remain proposed until the shape is ruled. The directory and environment names above define the recommended contract, not shipped syntax.

### Ownership and exposure

1. The owner creates a dedicated, empty root on the intended drive and supplies configuration. Yolo never creates a missing root or its parents, adopts arbitrary existing trees, formats a disk, or mounts a host filesystem.
2. At each fresh launch, yolo resolves the workspace's canonical host path and keys its child by the full SHA-256 digest of that path. Renaming or moving the workspace selects a different child; two path spellings resolving to the same workspace select the same child.
3. Yolo creates `<root>/workspaces/<digest>` and binds only that child at `/bulk` on supported container backends. It exports `YOLO_BULK_DIR=/bulk` only after successful exposure.
4. Agents own the contents and choose their own subdirectories, such as `downloads/` or `jobs/<task>/`. Shared sessions in the same workspace see the same files and must coordinate their own writers.
5. Host yolo owns allocation only. Allocation is serialized for the same root/workspace, creates directories idempotently, refuses links at managed components, and never empties or recursively changes ownership of an existing directory.

The prepared root may contain only the managed `workspaces` tree after first use. Unexpected top-level entries are a refusal, not an invitation to adopt them. A non-empty workspace child is normal on subsequent launches. The root itself and its ancestors remain invisible inside the jail; workspace hashes avoid name collisions, but are not an access-control mechanism.

Permissions must allow the actual mapped jail user to read and write the child without world-writable modes or recursive `chown`. An unsuitable filesystem or ownership mapping fails preflight and names the directory and host-side permission correction. No automatic permission widening is permitted.

## 4. Safety, availability, and lifecycle

### Resolve and validate before creating anything

- Canonicalize the owner-configured existing root once, then use the same resolved directory for validation, allocation, delivery, and reporting. A resolvable owner-created root symlink is allowed; dangling links fail.
- Apply the existing [writable-source boundary](../../internal/paths/workspacescope.go) and [context-source refusal set](context-mounts.md#23-refusal-set), including either-direction workspace overlap. No exception permits a configured root inside yolo's host state directory.
- Refuse managed-component symlinks and races that change the selected root or child during allocation/delivery. Bind the directory that was validated; if its identity cannot be preserved through setup, refuse and ask for a retry after stopping other directory changes.
- With the UUID policy, identify the mounted filesystem actually containing the canonical root, compare its UUID before allocation and again before delivery, and refuse unknown/mismatched identity. Comparing its device with the workspace's device is **not** equivalent.
- `yolo check` validates without creating directories. A real fresh launch creates managed descendants only after validation. No content walk is needed for admission.

> [!WARNING]
> A missing drive can leave `/mnt/bulk` as an ordinary directory on the SSD. “The path exists” is not proof the drive is mounted. A marker file is not sufficient either: a copied marker can exist on the wrong filesystem.

### Failures and changes

| Condition | Required recommended behavior |
| :--- | :--- |
| Explicit configuration is malformed, unsafe, inaccessible, or unsupported | Refuse a fresh launch. Name the reason and the host config correction; do not warn and ignore it. |
| Drive absent, wrong UUID, or root missing | Refuse before allocation. Name the expected drive/root and ask the owner to mount it and rerun `yolo check`. No retries or fallback directory. |
| Allocation or mount fails | Refuse; do not export the variable. Leave any empty directories already created for a later retry; remove no existing content. |
| Disk full or I/O error after launch | Writes fail normally at the filesystem. No automatic spill to SSD or retry loop. Agent reports the error; owner frees space or restores the drive. |
| Drive removed during a live session | Existing handles may fail or retain their mount; no claim of recovery or monitoring. Stop and restore/relaunch before resuming affected work. |
| Attach after host config changes | Use the running jail's frozen path and mount; allocate nothing. Disclose that changed placement requires a fresh launch. |
| Root/UUID changed for new launches | Select the new root, preserve the old files, and disclose that nothing was migrated. Running jails keep their old mount. |
| Configuration removed | New jails have no bulk directory; old data remains. Attached sessions keep their original grant until that jail stops. |
| Workspace deleted or moved | Its former directory remains on the bulk drive; no automatic deletion, reattachment by name, or transfer. |
| Read-only workspace | Bulk scratch can still be writable because it is a separate explicit grant; workspace read-only does not mean the whole jail is read-only. |
| Nested jail | Do not resolve host bulk paths or inherit the outer bulk grant automatically. Initial nested launches expose no bulk variable; ordinary nested behavior is unchanged. |
| `yolo host` | No new variable or access change in the initial proposal. The host already has its own filesystem access. |

Selecting another root is not a migration. To transfer scratch, the owner stops every writer to the affected directories, copies and verifies them on the new drive, selects the new root, and verifies a fresh launch. Deleting the old copy is a separate act. Unlike a shared tool store, distinct workspace scratch trees need no machine-wide single-writer version/pruning protocol.

### Survival and cleanup

Bulk scratch survives jail exit, restart, and workspace deletion **provided its filesystem remains intact**. Yolo never deletes it: no age-out, no inclusion in `yolo prune`, no dedup traversal, and no inference that an inactive workspace's files are expendable. It is not protected against disk failure, agent deletion, or owner deletion.

The external directory is not beneath the workspace, so workspace `git clean` does not reach it. This deliberately differs from existing durable scratch. Capacity management remains manual in v1; that simplicity also means abandoned workspace directories accumulate.

## 5. Discovery and reporting are part of the feature

An agent should not need to rediscover host mounts. The generated storage briefing names both scratch destinations and states placement, lifetime, sharing, cleanup, and the fact that final deliverables belong elsewhere. It recommends bulk for capacity-heavy intermediates and durable scratch for code/worktrees. No generic `TMPDIR`, cache environment, or agent-specific worktree default is redirected.

- **Fresh launch:** one disclosure names the canonical host child, jail path, and verified filesystem identity. No recursive size scan or HDD spin-up for a content walk at startup.
- **Attach:** report the existing grant, never present a newly configured root as already active.
- **Host `yolo check`:** show configured root, current workspace child, identity result, and permission result; absent configuration adds nothing. In-jail checks inspect the active path, not an unreachable host pathname.
- **`yolo stores`:** report the current workspace's bulk path even after its jail exits, plus root filesystem capacity/free bytes without recursively scanning all workspace children. Unknown capacity is labeled unknown, never zero; metadata-query errors remain visible and offer the host path to inspect.
- **Byte accounting:** external bytes are not reported as SSD space reclaimed. Do not double-count a child and its containing root as separate storage totals. No promise that reported free space remains available to the next writer.

Initial inventory does not enumerate every former workspace or offer deletion commands. Report formatting and internal decomposition are the implementer's choice; the path/identity/error facts and absence of recursive startup work are not.

## 6. Alternatives and costs

| Alternative | Benefit | Cost / verdict |
| :--- | :--- | :--- |
| Ordinary read-write mount plus handwritten instructions | Works as an owner-managed workaround with current mechanisms. | No standardized allocation, role, or drive-presence contract. **Useful today, not the full feature.** |
| Move all `.yolo` or durable scratch to the HDD | One scratch destination. | Moves worktrees and possibly latency-sensitive state; links are not transparent across jail mounts. **Rejected as the default; prior durable placement remains.** |
| Named destinations such as `fast`, `bulk`, `archive` | More than two disks and explicit project selection. | More grants, selection rules, availability states, and guidance. **Viable extension, not my recommended first slice; rule in [OQ-BS1](#OQ-BS1).** |
| Move files automatically above a size threshold | Agents need no explicit placement. | Open handles, path stability, cross-filesystem moves, concurrent writers, and mistaken workload classification become yolo's problem. **Rejected for v1.** |
| OS-managed automatic tiering | Transparent to applications and useful for a single logical filesystem. | Host administration, filesystem support, and recovery policy sit below yolo's remit. **Complementary, not a yolo implementation.** |
| Share one writable bulk directory across workspaces | Avoids duplicate datasets. | Cross-workspace writes, cleanup ownership, and name collisions are a different feature. **Rejected; explicitly mounted read-only datasets stay separate.** |

This adds one path agents must choose correctly and an external location owners must maintain. It does not enforce SSD capacity discipline: an agent can still write a huge file in the workspace. Enforcement would be a separate quota or policy design, not a hidden extension of guidance.

| Risk | Mitigation / remaining cost |
| :--- | :--- |
| An offline HDD silently fills the SSD | Verified filesystem identity and no fallback; a launch now depends on the configured drive. |
| Host reads or deletes agent-controlled links | Metadata-only inventory, no symlink traversal, no reclaimer; preserve directory identity during setup. |
| Rootless mapping is assumed to work | Real rootless-host verification of creation, read/write, and ownership; nested Podman cannot prove this. |
| HDD random I/O slows an agent | Workload guidance, not indiscriminate redirection; no performance guarantee. |
| Scratch orphans accumulate | Show the current workspace path and root free space; owner-managed cleanup only. |
| Scratch is mistaken for a backup or deliverable | Explicit briefing and lifecycle contract; moving final outputs back can require a full cross-filesystem copy. |

## 7. Delivery and observable success

First settle the scope, root authority, disk-presence contract, and supported setups below. Then deliver a complete vertical slice: selection, admission, narrow exposure, briefing, and inventory together. Cache and tool-store relocation remain independent; this proposal neither silently settles their open questions nor requires moving their bytes.

A completed implementation must demonstrate:

- With no setting, existing launches, storage guidance, and paths behave as before.
- Two workspaces on the same root receive distinct directories and cannot see each other's bulk files through the grant; two sessions of one workspace share the same directory.
- A large file explicitly written under the exported path occupies the intended drive, remains after a fresh jail launch, and is not written under workspace durable scratch.
- The intended drive absent at an existing mountpoint produces a refusal and creates no scratch on the underlying filesystem.
- Unsafe roots, malformed configuration, managed-directory symlinks, unsuitable permissions, and setup races cannot widen the grant or silently select another disk.
- Configuration changes do not move a running jail's files; restart, removal, and workspace relocation follow the lifecycle table.
- `yolo prune` leaves bulk contents alone, and reporting follows no agent-created link.
- Linux rootless verification records the runtime's actual rootless status; every supported macOS setup has native permission and restart evidence. A nested jail alone cannot establish these outcomes.

## 8. Open questions

The body specifies the recommended branch; these rulings may change it. Nothing is authorized for implementation until they are resolved.

1. 💬 **OQ-BS1: Start with one bulk destination, or a general named-tier model?**

   This decides whether v1 solves SSD-plus-capacity-drive placement or also introduces tier selection and multiple grants.

   - **A — One optional bulk destination.** Small surface; more disks require a later extension.
   - **B — Named destinations now.** Flexible, but needs a follow-up design for selection, exposure, and per-tier failures.

   <!-- vantage: question id=OQ-BS1 leaning="A — one bulk destination solves the stated need without turning yolo into a file-placement engine." -->

   _Leaning:_ A — one bulk destination solves the stated need without turning yolo into a file-placement engine.

   **Answer:**

   > _(empty — fill in when decided)_

2. 💬 **OQ-BS2: Does configuring the root grant bulk scratch to every workspace?**

   User scope already owns the host-path grant. Decide whether that grant automatically allocates a private child for every fresh jail or needs an additional trusted per-workspace switch.

   - **A — All workspaces.** One setup; each gets only its own child.
   - **B — Owner-enabled workspaces only.** Narrower grants; needs a separate enable/disable flow and no allocation when disabled.

   <!-- vantage: question id=OQ-BS2 leaning="A — automatic workspace-private allocation makes the configured disk useful without another setup step; the root remains a deliberate user-scope grant." -->

   _Leaning:_ A — automatic workspace-private allocation makes the configured disk useful without another setup step; the root remains a deliberate user-scope grant.

   **Answer:**

   > _(empty — fill in when decided)_

3. 💬 **OQ-BS3: Require filesystem identity, or allow unverified directory-only placement?**

   The [missing-drive failure](#resolve-and-validate-before-creating-anything) can defeat an existing-directory check. This revisits the still-open [tool-store availability question](shared-tool-store-relocation.md#OQ-STR2) for scratch, not a prior ruling.

   - **A — Required filesystem UUID on Linux.** Refuse unknown/mismatched identity and unidentified filesystems; strongest initial protection.
   - **B — Existing directory only.** Simpler and broader, but an offline drive can silently consume the wrong filesystem.

   <!-- vantage: question id=OQ-BS3 leaning="A — require the expected filesystem UUID; silent SSD fallback defeats the purpose of the feature." -->

   _Leaning:_ A — require the expected filesystem UUID; silent SSD fallback defeats the purpose of the feature.

   **Answer:**

   > _(empty — fill in when decided)_

4. 💬 **OQ-BS4: Which host/backend setups must the first delivery support?**

   A capacity path must be genuinely writable and persistent on the chosen host drive, not quietly replaced by a VM-local volume.

   - **A — Linux Podman, rootless and rootful.** Refuse explicit settings on other ordinary host setups; no-setting behavior is unchanged.
   - **B — All jail backends at launch.** Requires a follow-up macOS identity, filesystem-sharing, and sandbox-account permission design before building.

   <!-- vantage: question id=OQ-BS4 leaning="A — verify the SSD/HDD use case on Linux first, with explicit refusals elsewhere rather than unmeasured parity claims." -->

   _Leaning:_ A — verify the SSD/HDD use case on Linux first, with explicit refusals elsewhere rather than unmeasured parity claims.

   **Answer:**

   > _(empty — fill in when decided)_
