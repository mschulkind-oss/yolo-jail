---
title: "Feature request: relocate shared jail tools through Yolo configuration"
status: draft
stage: SKETCH
next: "Design and implement a user-scope path setting, then verify every store consumer before deployment."
tags: [storage, configuration, relocation]
summary: "Let users choose the shared jail tool-store directory without host bind mounts, fstab edits, or changing the host's own mise installation."
---

# Relocate shared jail tools through Yolo configuration

**Status:** 2026-10-06. Requested during a host storage migration; nothing implemented.
Current behavior checked against source commit `01428e40` and installed
`yolo-jail 0.11.1+654.g01428e40`.

## Problem

Yolo manages the development environment used by its jails. Its shared jail
mise store holds tool installations and related Rust/Cargo state; it is not
the host user's own mise installation. The host-side directory is currently
fixed at `~/.local/share/yolo-jail/mise`, exposed inside a Linux jail as `/mise`.
See [storage ownership and scope](../reference/storage-and-config.md#machine-wide-storage).

Moving that large tree to another drive should require application configuration,
not a host bind mount, an `/etc/fstab` entry, or a mount unit. Those workarounds
add reboot ordering and missing-drive behavior merely to preserve a hardcoded
application path. The user prefers supported application settings or supported
symlinks and has paused the migration rather than add that machinery.

A plain symlink is not a complete contract today: initialization follows paths
through ordinary directory creation, while deduplication deliberately refuses
symlinked writable roots. A fresh launch working once would not establish that
inventory and maintenance also operate on the intended store. Preserve those
safety checks rather than globally relax them.

## Requested behavior

1. Offer a supported **user-scope host directory setting** for the shared jail
   mise store. User scope is the trusted machine owner's config, not an
   agent-editable workspace; see
   [config scope rules](../reference/storage-and-config.md#the-config-scopes).
   Workspace, workspace-local, and pack settings must not select arbitrary host
   directories to expose writable to a jail.
2. With no setting, retain the existing directory and behavior. An explicit
   setting chooses one store; Yolo must not merge it with or migrate the old
   store automatically. The exact config spelling is delegated to implementation
   design and is not an already-supported key.
3. Keep the in-jail tool path `/mise` and its Rust/Cargo environment unchanged.
   Nested jails continue using the outer jail's `/mise`, never a host path from
   a copied configuration. Initially support the Linux rootless-Podman case;
   explicitly document and test other backends' support or refusal.
4. Every relevant consumer must agree: directory initialization, launch,
   `yolo check`, `yolo stores`, usage recording, migration/healing, inventory,
   pruning, and deduplication. Maintenance must not scan or delete the old
   default just because the launch uses a different directory.
5. Reject invalid configuration with the offending setting and an actionable
   correction. Validate directory type, access, and existing containment rules.
   A configured target that is missing, unreadable, or unsafe must not silently
   fall back to the default, a temporary directory, or a root-filesystem copy.
6. Keep migration explicit: stop writers, copy and verify the tree including
   hard links and ownership, select the new path, test a fresh jail, then remove
   the original only with separate approval. Updating Yolo itself does not
   copy, rename, delete, or switch existing user data.

## Acceptance evidence

- Existing configurations retain their original store and pass current tests.
- A user-selected populated directory is actually mounted at `/mise`; installed
  Python/Node/Rust tools run in a fresh CPU-only rootless jail.
- Initialization and all maintenance consumers report and use that same directory;
  a populated old default is neither counted as active nor modified inadvertently.
- Workspace attempts to select a host store are refused. A copied host setting
  cannot redirect a nested jail outside its existing shared store.
- Missing/non-directory/nonwritable targets produce useful failures without
  creating data at the old default. Host paths with spaces work.
- Existing symlink-containment tests remain passing. If supported relocation
  symlinks are added, their trusted ownership, resolution, and every maintenance
  consumer are tested explicitly; launch-only success is insufficient.
- Configuration remains effective across reboot without a new host mount entry.
- Run the repository's unit/quality gates and applicable rootless launch tests
  before installing the feature and resuming the real data migration.

## Scope boundary

This request changes where the shared **jail tools** live. It does not relocate
the host's mise installation, credential stores, the full Yolo state tree,
workspace working copies, or Podman storage. Shared download-cache relocation
can follow after this path contract is established. No boot, partition,
encryption, agent-approval, or permission-policy changes are required.

The host migration stays paused with originals retained. Its HDD seed is only
preparation and needs a new final sync after writers next stop. No public issue
has been posted; this file is the local request and implementation handoff.
