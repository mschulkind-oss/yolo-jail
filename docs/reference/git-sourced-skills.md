---
title: "Git-sourced skill registrations"
status: accepted
stage: GRADUATED
verified: 2026-11-17
verified_commit: bde502e4a
covers: [internal/packdecl/sourcedskills.go, internal/packsrc/skillpayload.go, internal/packload/sourcedskills.go, internal/packload/skillpayloadtree.go, internal/hostskills/sourcedregistration.go, internal/hostskills/sourcedprojection.go]
tags: [packs, skills, git, delivery]
---

# Git-sourced skill registrations

A selected pack can register individual skills from a Git repository without selecting that
repository as a pack. Yolo manages the coding environment's selected registrations and the
supporting files they read; a jail is one delivery notch, alongside host apply.

**Status:** Verified 2026-11-17 against `bde502e4a`. MEASURED: Linux synthetic
registration execution and recorded nested Podman attach. UNMEASURED: native macOS,
agent discovery and maximum-payload copy cost. This reference owns the implemented contract;
[follow-up verification](../plans/BACKLOG.md#git-sourced-skills-and-aws-verification)
keeps the unmeasured work distinct.

## Declare content, then a destination

```toml
[[skills]]
source = "git+https://example.invalid/team/repository//skills/chosen?ref=main"
as = "selected-name"
license = "legal/LICENSE"
agents = ["fixture"]

[[skills]]
agent = "fixture"
into = ".fixture/skills"
```

The manifest content row selects one non-root repository directory with a direct `SKILL.md`. Its Git
address must include a ref. This marker/non-root contract is specific to manifest sourced rows:
[direct user TOML scoped imports](toml-scoped-pack-imports/README.md) also use the common
carrier machinery, but raw direct rows may select a repository-root marker, and typed ordinary
rows may deliver a markerless folder. Selecting a manifest sourced alias through a direct entry
does not relax that alias's own marker, explicit-ref or non-root requirements. `as`, when present, replaces the selected directory's basename
before ordinary pack namespacing; two aliases of the same selected directory are independent
registrations. `license` is an optional repository-relative file, not a host path.
`source` requires a nonempty non-root subdirectory and explicit ref, uses existing Git
HTTP(S), SSH or `git+file` transports and refuses URL passwords. Plain `file://` directories
use `from` instead; `as`/`license` without source and unclean paths refuse offline.
User-scope pack selection is the authority. Fetch uses host pack-store Git transport, never
workspace imports or broker grants. Object reads disable global/system config and lazy fetch;
transport still uses the established user's Git route. No upstream filter/hook/LFS runs.

A destination (`agent` and `into`) does not accept `source`. A content row does not combine
`source` and ordinary pack-relative `from`. Ordinary `from` rows, conventional local skills,
plugins and their existing governance remain unchanged. A sourced row does not describe or
suppress the declaring pack's conventional tree. Legacy config `only` withholds every sourced
row without contacting Git; remove `only` to register them.

## Supporting payload versus registration

**Supporting payload** means the effective, immutable whole-repository tree for one repository
and commit. It is data outside every skills/plugin discovery root, not a selected pack. Root
pack manifests, hooks, plugins and unselected skill markers in it are never discovery inputs.
Aliases and refs resolving to the same repository/commit share its bytes.

**Registration** means the selected skill's delivered name and private directory in a declared
skills destination. For a manifest sourced marker row, yolo writes a regular, non-executable
private `SKILL.md` with its `name` rewritten to the delivered name. A typed direct directory
row instead records marker presence explicitly and creates no marker when absent. It may write a regular license copy. Its remaining direct
children are only the explicit links authorized by the validated selected-row plan, into
that skill's subtree of the supporting payload. The upstream marker and payload stay unchanged.

Consequently a selected executable can use the whole supporting repository. Relative paths
and a parent-depth walk through a linked child directory resolve in the payload, not in the
registration's private directory. A textual normalization of `..` before opening a path does not follow that linkage.
A climb starting from the real registration's top level leaves through the discovery root,
not the repository. A skill's workspace setup or dependencies are not supplied merely by
deploying their bytes. Arbitrary relocated layout assumptions are not guaranteed;
only tested path forms have execution evidence. This exposes supporting bytes, not another
metadata activation channel or another credential grant.

The host uses the sealed cache payload. Fresh launches stage deduplicated payloads beside the
selected packs under reserved `_skill-payloads`, with backend-specific reading paths. Podman
uses the context pack root; Apple Container uses its existing in-jail pack root; macos-user
uses its staged pack-tree root. Narrow recorded-tree copying preserves validated links and
executable modes; ordinary pack copying is not relaxed.

## Whole-selection transaction

Fresh launch, host apply, `yolo pack install` and `yolo pack update` resolve sourced rows only
after the complete selected pack closure is known. Every non-withheld row participates,
regardless of its audience. Rows group by parsed repository/ref, never by alias or selecting
pack. One invocation keeps one validated result per group through every registration write.

Each group holds the existing mirror lock, reloads the latest shared record, and fetches only
when there is no record, the branch pair stamp is due, or refresh is forced. Missing mirror
refs and caller-invalid recorded rows add no trigger. Required blobs are acquired explicitly,
not by Git lazy fetching during validation. The candidate's whole payload and all group rows
must pass before its payload and latest record are published. Failure can use only the latest
record, revalidated for the caller's whole group; there is no per-selection fallback history.
Pin a known full commit when a moving pair has become unusable for your selection.
Full-commit pins and tags do not auto-refresh; install/update forces moving refs by existing
policy. If another selection advances the shared pair to an unusable commit, current launch
and attach stay unchanged but the next writer fails closed by row/commit with the shared-record
explanation. An older payload on disk is not fallback. There is no retry or per-selection history.

The shared `skills.lock.json` is a schema-1 repository/ref map beside the normal pack lock.
Its new leaf lock is short-lived under the mirror lock, never held across network or
validation. No ordinary pack/fork lock ordering changes.

The existing repository/ref stamp is shared with ordinary pack refresh. Sourced transport
stamps once after required ref/pin/blob network steps and before pure validation. Required
network failure preserves stamp bytes/time for immediate retry; deterministic local failure
after ref success still stamps. A completely acquired but invalid candidate is paced.
Ordinary refresh retains its existing ref-step stamp and address-specific pin-error behavior.
The stamp helper remains best-effort: a failed write causes an extra eligible fetch, not a new
launch refusal.

## Strict selection, bounded supporting omissions

The selected subtree is strict: supported regular files/directories and safe links must all
survive; links stay within it and cannot be cyclic or dangling. An omitted selected directory,
marker, explicit license or registration target refuses the row. A raw-valid selected path
cannot bypass an effective supporting-tree omission.

Outside the selected subtree, unsupported links, gitlinks and case-collision groups are
omitted and counted. Safe internal supporting links remain. No unsupported group gets copied
around to rescue a selected row. Unicode simple-fold collisions are treated as collisions.

The fixed materialization bounds are listed in [Current values](#current-values).
Omissions consume the limits too, charged before blob reads and never refunded.
There are no size knobs. Refusal names measured size and bound; it does not promise a
transport byte limit or introduce a new Git protocol. Git LFS remains pointer text and
submodules are never initialized.

An explicit license must resolve to a regular blob within the license bound. A missing,
omitted or unsafe explicit file refuses the row, as does a case-insensitive basename collision
with any selected-directory entry: nothing is overwritten.

Without an explicit license, a selected directory's top-level `LICENSE`, `LICENSE.md`,
`LICENSE.txt` or `COPYING` prevents adding another one. Otherwise the first of those names
that is a regular blob at the repository root is copied; if none exists, inspection notes
"no license file found". An automatic candidate that is omitted or whose basename collides
also refuses the row; it is not silently skipped. Supporting licenses do not register skills.

## Reader states and commands

| State | Meaning | Next step |
| :--- | :--- | :--- |
| READY | Latest recorded full-commit objects validate every caller row and published payload exists | Use the selected registrations |
| REFUSED | Caller declarations or effective selected contents are invalid | Correct the named row or pin a known valid full commit |
| NOT FETCHED | No shared record or required recorded objects are absent | `yolo pack install`, or a fresh writer |
| NOT MATERIALIZED | Recorded objects validate, but published payload is absent | Next fresh launch or host writer restores it locally |

Readers neither fetch nor lazy-fetch, materialize, acquire locks, write records, nor consult
or touch stamps for decisions. Invalid own rows take precedence over absent publication.
A not-due writer repairs absent payloads from valid local objects without transport or
mirror/stamp changes. NOT MATERIALIZED is a derived skip, not a missing-object/network error:

```text
<row>: not materialized — objects present and valid; published payload absent; the next fresh launch or host writer rematerializes from local objects
```

- `yolo pack install` and `yolo pack update` force grouped sourced refresh.
- `yolo pack status`, `explain`, and `footprint` project cached objects only. Supporting
  deployment and selected registration counts are separate; non-READY rows claim no delivery.
- `yolo check` passes READY, skips missing objects/publication, and fails invalid contents.
  For a delivered staged pack, it validates the running tree's recorded payload instead of
  consulting an absent host store.
- Offline `yolo pack lint` says `contents not verified`. `--online` validates in disposable
  scratch and never advances the global mirror, shared record or payload cache.
- macos-user `--dry-run` prints cached readiness and composes READY rows only. It starts no
  sourced writer; declaration-based collision checks still include unready names.

Attach reads the running tree's staged declarations, recorded payloads and registration
identity. It never restages current host selection or runs a cache transaction. A changed
selection is a next-fresh-launch change. Deleted host cache and unavailable origin do not
change the running tree's registrations. Missing, duplicate, unknown or mismatched recorded
rows, and linked payload ancestors, refuse with a restore/restart next step. If the running
tree cannot be found, sourced registrations cannot be refreshed; the attach's skew gate
names a restart rather than adopting current host-cache registrations.

## Claims, collisions and cleanup

A sourced destination/name claim comes from declarations, even when unfetched. Same-pack
sourced/sourced or local/sourced duplicates, other-pack claims and built-in bare/prefixed
names refuse before adoption or destination writes. This includes rendered jail mechanics;
ordinary local later-source-wins and jail pack-over-built-in precedence remain unchanged.
The next step names `as` or namespace adjustment and both contributors. Reserved names are
withheld, not failed; workspace/import copies are lower-priority and shadowed with disclosure.
Audience and describes routing remains existing pack policy, not an agent registry in core.

Registration links are typed planned entries computed by yolo, never arbitrary links copied
from declarations or source trees. Ordinary source delivery still dereferences links, and
ordinary sync still refuses them. Digests hash link text without following dangling jail
paths. Typed archive fallback preserves owned link text and removes the original only after
a successful copy; it is not an ambient symlink permission.

Sealed launch payloads are removed only after the caller establishes ownership/liveness.
`RemoveSealedSkillTree` first attempts confined ordinary removal, preserving unreadable-file
and foreign-owner empty-directory behavior. Only failure triggers best-effort restoration of
**directory** modes before retry. It rejects own-base removal, linked ancestors and changed
root identity; no cache-wide chmod or per-file byte read is introduced. Detached discard,
orphan prune and capture cleanup use this boundary; unprivileged caller tests pin it.
Host-cache garbage collection remains deferred.

## Compatibility and code map

Older binaries keep their existing skew behavior: author lint refuses unknown fields, while
use skips rows with `as`/`license` and notes the newer field. A source-only row may fail its
older validation. No minimum-version mechanism or new updater command is added.

| Component | Package / anchor |
| :--- | :--- |
| Declaration and static source rules | `internal/packdecl`, `IsSourcedSkill` |
| Object validation, transaction, publication and cleanup | `internal/packsrc`, `ResolveSkillPayloads`, `ReadSkillPayloads`, `RemoveSealedSkillTree` |
| Typed rows and tree records | `internal/packload`, `HydrateSourcedSkills`, `BindSelectedSkillPayloads` |
| Registration, collisions and owned archives | `internal/hostskills`, `SourcedBuiltinCollisions` |
| Jail preflight/composition and owned sync | `internal/jailcontent`, `internal/treesync` |
| Selected writer and recorded attach | `internal/cli/run` |

## Current values

| Value | Setting | Defined in |
| :--- | :--- | :--- |
| Payload entries / blob bytes | 32,768 / 256 MiB | `internal/packsrc`, skill payload bounds |
| Tree listing / link target | 16 MiB / 4,096 bytes | `internal/packsrc`, skill payload bounds |
| Marker / license | 1 MiB each | `internal/packsrc`, selected-row validator |
| Shared record | schema-1 `skills.lock.json`, repository/ref keys | `internal/packsrc`, skills lock |
| Refresh pacing / detached timeout | Existing hourly branch pacing / 60s | `internal/packsrc`, sourced transport adapter |
| Payload area | `_skill-payloads`, beside packs, outside discovery | `internal/packload`, skill payload tree |

## Why it's this way

| ID | Constraint |
| :--- | :--- |
| GSS-D12 | Legacy `only` withholds every sourced row; it is not a failed delivery or a fetch trigger. |
| GSS-D13 | Whole-repository deployment is supporting data, never conventional pack activation. |
| GSS-D21 | One caller-local group transaction, mirror then short record leaf; transport stamps differ from content validity. |
| GSS-D22 | Only the shared latest record is fallback; pin a full commit rather than inventing selection history. |

## Evidence boundary

Source-named production-caller tests cover public host apply, install/update/status/explain/
footprint/lint, check, launch dry-run, fresh staging, actual running-tree adoption and the
entrypoint loader. Caller-removal controls fail and restoration passes. Synthetic scripts
execute through exposed registrations, reading repository-root and sibling bytes.

The matched nested Podman witness executes those same path forms and then attaches after
its private mirror/payload cache and local origin are unavailable. Its stopped-jail check
passes. This is neither native agent discovery nor rootless/reachability proof: nested
Podman uses host networking/user namespaces. Native Apple Container and macos-user checks
remain follow-up verification on main after Linux landing. Native scanners following nested
markers, case-insensitive own-license ergonomics and repeated whole-payload memory/read
costs are not established by the fixture; their next steps remain in the linked backlog.
