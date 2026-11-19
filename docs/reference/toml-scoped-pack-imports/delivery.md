---
title: "Scoped imports: carriers and recorded delivery"
status: accepted
stage: GRADUATED
verified: 2026-11-17
verified_commit: 35055d92094c248c1cde87dfe3273fc3a5255e29
covers: [internal/config/projectedcarriers.go, internal/packload/projectedcarriers.go, internal/packload/scopedrecord.go, internal/packload/packtreerecord.go, internal/packsrc/skillpayload.go, internal/hostskills/sourcedregistration.go, internal/cli/run/gitskillcopy.go, internal/cli/run/packtree.go, internal/entrypoint/packsurfaces.go]
tags: [packs, skills, carriers, replay]
---

# Scoped imports: carriers and recorded delivery

A carrier supplies supporting bytes; a registration selects the named directory agents receive.
The two have distinct identities and authority. [Selection](selection.md) produces rows before
common carrier validation and registration composition.

## Shared carriers, private registrations

| Selected row source | Carrier |
| :--- | :--- |
| Direct or named Git pack | Existing immutable repository/commit payload |
| Trusted local or shipped pack | Per-launch selected-directory copy outside discovery roots |
| Manifest sourced row | Existing own-repository Git payload and unchanged marker/non-root contract |

Marker and directory rows at one repository/commit share payload bytes.
Selection never keys, mutates or filters a shared mirror, checkout, stamp or payload.
Unselected manifest sourced rows are removed before store work.
The complete selected row group validates before any registration publication.

The common composer writes private registrations in declared skills destinations.
A present marker is a regular non-executable copy with its name rewritten under existing rules.
A license may be a regular private copy. Other direct entries are validated links into the
selected subtree of the carrier. The upstream marker and payload remain unchanged.

Local staged carriers dereference trusted selected content under existing local publication
rules. They copy only selected directories, not the original discovery root.
Host process copies retain process lifetime; a returned path cannot outlive its carrier owner.

## Explicit kind and marker state

A **marker row** requires a direct regular `SKILL.md`. Raw and manifest sourced rows are
always marker rows. A **directory row** comes only from a typed ordinary child, with its pack
root, governed source and child name as eligibility evidence.

An absent marker is an explicit `absent` state, not an unchecked fallback.
A present regular marker has state `present` and receives full marker validation.
A marker symlink, directory or other nonregular entry refuses; it is never treated as absent.
The composer creates no marker for an absent state. Inspection labels such a delivered folder
as not discoverable. Host apply still counts it and advertises invocation, a
[tracked reporting defect](../../design/toml-scoped-pack-imports-plan.md#remaining-presentation-and-verification),
not a discovery or carrier guarantee.

Directory kind has no authored TOML, manifest, legacy, CLI or environment option.
Requests and records carry it only after typed enumeration; hydration rechecks its immediate
child relationship and structural evidence. Raw and manifest sourced rows cannot forge it.

## Licenses and support omissions

Direct rows use existing own-license then repository-root-license discovery.
Conflicting, unsafe or oversized license evidence refuses; no license found is recorded honestly.
Direct config adds no explicit license path. Manifest sourced rows keep their explicit option.

Git payload validation refuses unsafe selected content and omits unsafe unselected support
with recorded reasons. Support stays outside discovery roots even when it contains manifests,
markers, hooks or native plugin metadata. Metadata names do not activate supporting bytes.
Pure inspection reports omission/license facts only when it has a safe actual validated plan.

A payload-backed script can reach repository siblings through a linked child path.
The synthetic sourced integration executes a script under `bin` whose parent walk reaches
repository `shared/value`, while `shared/SKILL.md` remains unregistered.
This proves that path form, not arbitrary relative marker paths, workspace installation,
dependency availability or provider/credential access.

## Recorded replay

A scoped staged tree contains an effective record and selected carriers, never the original
pack root as a discovery input. The record includes mode, roots, selectors, digest, origin,
selected names, explicit kind/state, governed directory, source directory, audience, carrier
location, resolved commit, license result and omitted contribution counts.
Modern withholding is false.

A versioned envelope prevents old array readers from silently accepting scoped entries.
The shared host/jail loader validates projection structure, exact row coverage, copied content
and per-row commit/digest evidence. Missing, duplicate or extra rows refuse.
Unknown/missing/ineligible kind, changed marker state or incompatible record version refuses.
A scoped sentinel prevents a missing record from falling through to ordinary pack loading.

Attach and reload consume the running record rather than enumerating current sources.
They still work after original sources, mirrors, checkouts and payload caches are deleted.
This is a replay guarantee, not a claim that every host preflight/comparison is source-free.
Host and guest hydrate the same names, audiences and license plans.
No failure falls back to whole pack, plain directory copy or a synthesized marker.

## Lock-first refresh and recovery

Host launch-shaped refresh uses the existing source store and handle lock.
It saves an advanced pin before resolution; selection failure does not roll the pin back.
A renamed or removed candidate invalidates the new projection and refuses without publishing
partial registrations or reusing the prior subset. An older running staged tree remains intact.
A later unchanged launch repeats the refusal rather than degrading it to a warning.

The refusal names the new full commit, selector, candidates and next step.
Edit the selection with `yolo config set`, or set a known-good full ref on the entry/source,
then restart to deliver the corrected rows. Both recoveries are caller-tested.
Pure inspection binds to a matching recorded full pin; a readable branch without that pin
is unverified, never a reason to float to cached branch content.

## Current values and ownership

| Value | Definition authority |
| :--- | :--- |
| Scoped staged envelope version `2` | `scopedRecordVersion` in `internal/packload` |
| Selected carrier directory `_scoped-data` | `ScopedCarrierDir` in `internal/packload` |
| Missing-record refusal marker `_scoped-entry` | `ScopedEntryMarker` in `internal/packload` |

`LoadRecordedScopedPack` in `internal/packload` is the common replay boundary.
`LoadJailPacks` in `internal/entrypoint` and the run pipeline use that boundary.
`registrationContent` in `internal/hostskills` enforces absent-marker composition.
The [inspection and verification map](inspection.md) names permanent callers and limitations.
