---
title: Skill import lifecycle and commands
status: accepted
stage: CURRENT
verified: 2026-11-16
verified_commit: 6b8879611
covers:
  - internal/skillimports/resolver.go
  - internal/skillimports/store.go
  - internal/configedit/directorylock.go
  - internal/configedit/apply.go
  - internal/configedit/creation.go
  - internal/cli/skills.go
  - internal/cli/run/run.go
summary: Fresh launches and explicit host commands are writers; attaches and lists are readers.
---

# Skill import lifecycle and commands

**Status:** Verified 2026-11-16 against `6b8879611`; focused transaction,
no-op refresh and config-symlink command regressions passed.

[Resolution records](README.md#vocabulary) select one immutable commit per handle.
Fetch permission comes from [the authority decision](authority.md), not the record.
The lifecycle distinguishes writers from readers so an attach cannot silently refresh
content and a reader cannot repeat another process's resolution disclosure.

## Pin and moving-ref selection

A pin selects its full `rev` commit. An optional `ref` is only the label an explicit
update uses to re-pin; launch never substitutes that ref's current target.
A moving entry has `ref` and no `rev`. Full branch/tag names are used as written.
A short name matching both a branch and tag is refused; the next step is a full name.
Annotated tags resolve to commits. Tags freeze until explicit update; branches refresh
when due at a fresh launch. Approving a moving entry covers later ref movement: the
writer reports movement but does not open another approval prompt.

## Fresh launch

1. Validate workspace config offline and pass the existing config-change gate.
2. Take the current narrowed Git access decision for each stable-sorted handle.
3. Read the matching record and check object availability/access class.
4. Resolve only if state is missing/unusable or a branch refresh is due.
5. Publish the complete record and return a writer event if created or moved.
6. Project/stage eligible recorded content through the confined workspace skill path.

A healthy pinned import needs no network after initial resolution. A healthy branch
uses its recorded commit until due; a tag is never automatically due. A failed branch
refresh may retain a valid usable commit with a stale warning; with none, the import
is withheld. Runtime import failure never turns into a launch-wide fatal error.

## Attach and other readers

Attach passes its existing config gate and takes a local current-authority decision,
then reads record/objects and re-stages eligible skills. It never fetches, refreshes or
writes a record. A missing valid commit waits for the next fresh launch. If another
writer has advanced the record, attach can stage that newer commit; each projection
still reads one whole commit. The selected pack tree remains the running jail's live
tree: attach never re-stages packs.

Host `list` and `pin` read valid host state; list has no network or staging effects.
`yolo host` does not resolve or deliver imports at all.

## Explicit commands

The complete flags live in `yolo skills --help`. The effects, rather than a copied
command registry, are the important contract:

| Operation | Host behavior | Jail behavior |
| :--- | :--- | :--- |
| Add | Fetch the command-line source; default to a pin, or track explicitly; write declaration/record on success | Print the exact human-mediated host-run line; nothing runs |
| Update | Contact only declarations with matching valid records; move branch/tag record or re-pin a labeled pin | Host-run line; no config/state/network effects |
| Pin | Write valid recorded commit into workspace config; preserve ref label; no network/record write | Host-run line; no effects |
| Remove | Delete workspace declaration only; leave stored state alone | Same workspace-config-only edit |
| List | Read declarations, eligibility, objects, selected/refused/shadowed names and visible unimported paths | Declarations only; explain host state is unavailable and give the host-run line |

An unlabeled pin is left alone by update. A handwritten/changed entry with no matching
record is not contacted by update; it resolves after the next fresh-launch gate.
Jailed write commands do not transparently forward or expose a host process RPC.

## Transactions and lock ownership

One writer transaction spans declaration recheck, authority snapshot, fetch, targeted
config edit, record publication and rollback. Objects are add-only. Records are replaced
atomically, so readers see a whole old or whole new record, then read by commit ID.
Readers take no writer lock.

The transaction locks the real workspace-parent directory inode read-only, without
creating/deleting a lock pathname. Sibling-workspace writers conservatively serialize,
while their objects/records remain separate. Configedit then locks the physical config
target's parent when distinct. Supported user config symlinks are preserved.

A symlink target in the already-held directory borrows the verified live
`DirectoryLock` token. Borrowing never closes/unlocks the owner's descriptor. Tokens
expire after their callback; pathname replacement or expired ownership is refused.
Lock order is import transaction then distinct config-target parent, never reverse.

A due refresh with usable content skips a busy lock and uses its current record. A
launch without usable content waits, re-reads the holder's result, and makes an attempt
if necessary. Explicit commands wait because the operator requested the operation.

Publication follows a successful config edit; a publication failure rolls the edit back.
A same-commit pinned update still publishes refreshed access/timestamp after a semantic
config no-op. Refused or failed new adds leave no owned import directory/lock artifact.
An update failure preserves the previous config and resolution record.

## Current values

| Value | Setting | Owner |
| :--- | :--- | :--- |
| Default feature veto | Enabled; user-only `workspace_skill_imports = false` disables resolve/stage and fetching commands | `WorkspaceSkillImportsEnabled` in `internal/config` |
| Branch refresh | Shared `packsrc.RefreshInterval`; no separate knob | `refreshDue` in `internal/skillimports` |
| Store partition | `GlobalStorage()/skill-imports/<runtime.FromWorkspace>` | `Store.Open` in `internal/skillimports` |
| Object/record layout | `objects.git`; `records/<hex-handle>.json` | `Store` in `internal/skillimports` |
| Record version and read bound | Version 1; bounded regular file up to 1 MiB | `resolutionVersion`, `readResolution` in `internal/skillimports` |

Stored state is outside checkout, user config, approvals and jail mounts. Removal is
not garbage collection; no automatic import-store cleanup policy is supplied.
