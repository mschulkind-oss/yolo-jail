---
title: Skill import projection and observations
status: accepted
stage: CURRENT
verified: 2026-11-16
verified_commit: 6b8879611
covers:
  - internal/skillimports/project.go
  - internal/jailcontent/workspaceskills.go
  - internal/jailcontent/skillinventory.go
  - internal/cli/run/workspaceskills.go
  - internal/cli/run/skillinventory.go
  - internal/cli/skills.go
summary: Only confined skill directories arrive, below every existing skill layer.
---

# Skill import projection and observations

**Status:** Verified 2026-11-16 against `6b8879611`; focused immutable-object,
shared-budget and public inventory/projection regressions passed.

[Projection](README.md#vocabulary) reads the selected commit's objects and admits only
immediate child skill directories beneath the configured path. It is not checkout,
pack interpretation or plugin discovery. Mixed repositories remain usable because
non-skill paths are excluded rather than activated or used to reject all content.

## Shape and confinement

Each admitted directory needs a confined regular `SKILL.md` marker, including safe
internal marker-link resolution. No marker means no skill, even if bundled scripts
look useful. The source's executable files remain executable inside the jail, but
nothing invokes them during import.

- Every link must stay inside **that individual imported skill**, not merely inside
  the workspace or source repository. Cross-skill links are refused.
- Special files, gitlinks/submodules, unsupported object modes, escaping links and
  read/copy failures refuse the complete affected skill. No partial output arrives.
- Sibling skills may still be eligible; each refusal names the path and reason.
- Pack manifests, dependency declarations, hooks, program install specs, surfaces,
  provider/profile settings and unconditional briefing text are never interpreted.

A plugin repository can expose its skill directory through the selected path; no
plugin component is registered. Visible non-skill paths are observable but their
presence does not enable anything. There is no import dependency closure.

## Deterministic selection and budgets

The optional name filter selects immediate-child basenames, not arbitrary paths.
Absent means all; explicit empty means none at that merged config value. Invalid or
duplicate names fail validation. A valid missing name is reported while other names
remain eligible. Inspection and projection process selected names in ascending order,
including authored filters: budget exhaustion cannot make list and stage disagree
solely because their orders differ.

Object traversal spends a shared budget across imports. Work is charged before
entry/blob processing and **never refunded** after a late refusal. Otherwise many
failing skills could evade the resource bound. Root-tree/listing and link-target
reads also have bounds; unsafe or overbound roots are visible refusals in inspection.

Confined staging shares the existing workspace-skills cap across workspace-owned
and imported input, rather than multiplying it per import or agent destination.
Projection has its own monotonic shared object-walk budget; these are distinct
bounded phases, not a single counter promised across unlike readers.

Inspection runs the same object validator without scratch/staging writes. It reports
eligible names, refusals and visible unimported paths. It cannot promise a runtime
program dependency exists or inventory latent behavior described only in manifests.

## Priority and delivery

Skills keep their original names, with no handle prefix. Lowest precedence first:

1. Imports, with ascending handle order resolving import/import collisions.
2. Workspace-owned skills.
3. Built-in skills.
4. Selected shared-pack skills in their established order.
5. Conventional local-pack skills, last.

Every actual shadow is observable, including the winning import handle and
higher-layer or reserved-destination owner. Inspection uses the same deterministic
budget/order and carries winning ownership forward; a refused candidate cannot claim
a name and shadow a later eligible one.

Imports feed the existing workspace-derived skill destinations in containers and
macos-user. They have no per-agent audience because no manifest is interpreted.
Nothing reaches `yolo host`, whose workspace layer is absent under
[WS-D24](../agent-briefings.md#ws-d24). Pack-tree staging is not used for imports.

## Observations

Healthy unchanged imports add no routine launch line. Information appears for actual
events and failures:

| Observation | What it establishes |
| :--- | :--- |
| Resolution created | The writer committed a new record; includes selected commit/access and projection summary |
| Resolution moved | The writer committed a different target; names previous/new commit |
| Stale warning | Refresh failed but a usable recorded commit was retained |
| Withheld/refused path | No eligible content for that import/skill; gives reason and next step |
| Shadowed name | Another layer or earlier eligible handle owns that name |

Creation/movement events are **writer-only**, returned after successful publication
and never serialized in the record. Attach/list/readers cannot replay them. A failed
publication must not print a created event. Same-commit refresh updates record metadata
without fabricating a commit movement.

Host `yolo skills list` is the on-demand view of declarations, commit/time/access,
object availability, selected/refused names, collisions and visible unimported paths.
Jail list shows declarations only. Neither form asserts that a running jail already
received a command's newly stored content; it remains staged until attach/fresh launch.

## Current values

| Value | Setting | Owner |
| :--- | :--- | :--- |
| Default selected path | `skills` | `SkillImports` in `internal/config` |
| Import object-walk budget | 32 MiB; 4096 entries shared across imports | `ProjectionBudget` in `internal/skillimports` |
| Tree-listing bound | 8 MiB | `maxTreeListingBytes` in `internal/skillimports` |
| Link target bound | 4096 bytes | `maxLinkTargetBytes` in `internal/skillimports` |
| Confined staging budget | 32 MiB; 4096 files/directories over workspace/import input | Workspace skill bounds in `internal/jailcontent` |
| Staged regular-file mode | Executable source: 0755; otherwise 0644 | Confined workspace reader in `internal/jailcontent` |

`InspectWithBudget`, `ProjectWithBudget` and `PrepareSkillsWith` are the greppable
consumer anchors. The public regression compares list/project with exhausted budgets
and later collisions; the launch integration checks actual imported bytes, not agent use.
