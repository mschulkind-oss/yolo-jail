---
title: "Scoped imports: inspection and verification"
status: accepted
stage: GRADUATED
verified: 2026-11-17
verified_commit: 35055d92094c248c1cde87dfe3273fc3a5255e29
covers: [internal/config/packresolve.go, internal/hostskills/scopedinspection.go, internal/packload/skillinspection.go, internal/cli/scopedpackexplain.go, internal/cli/scopedpackediting.go, internal/cli/check/packs.go, integration/scopedpacks_test.go]
tags: [packs, skills, inspection, verification]
---

# Scoped imports: inspection and verification

Configured inspection reports authored selection and available source facts, not inferred
publication. [The overview](README.md) defines projection, row, carrier and registration.

## Verified, unverified and refused

A verified group names its read basis: local directory, shipped content or matching cached full
commit. It reports roots/mode, candidates, selected/excluded names, registration names, origin,
row kind, marker state, governor and safely established carrier/license facts. Audience is retained
in rows but omitted by this inspection branch; unpublished manifest-sourced rows can also show
the enclosing pack's commit instead of their own. Both are [tracked defects](../../design/toml-scoped-pack-imports-plan.md#remaining-presentation-and-verification),
not verified reporting behavior.

If reading needs transport or materialization, the group prints authored facts followed by:

```text
selection not yet verified; resolved at next launch
```

Unverified is not a failure. It has no invented candidates, rows, counts, licenses or omissions.
A readable invalid selection is refused with the resolver's origin/index/candidate remedy.
A cached branch without the corresponding recorded full pin is unverified, not floated.

Optional trusted-local license/support plans say **determined at publication** where not safely
established. Pure inspection must not walk the whole original local root, reject an eligible
within-boundary selected link or charge large/unsafe unselected support bytes.
Existing root confinement and selected marker validation are still mandatory.

## Explain and check

Explain reports every matching import tuple in authored order. Group headers give index, origin,
subdir, raw directory and mode. A single ordinary unscoped entry retains its baseline body.
Whole Explain calculates that body without staging; absent content is unverified, not guessed.

Scoped Explain, Check and add dry-run share pure inspection. They fetch nothing, create no
mirror/stamp/lock/checkout, extract no embedded tree, and copy/publish no selected carrier.
Directory rows with absent marker are labeled folders, not discoverable skills, and excluded
from the inspection skill counts. Host apply still counts these folders and advertises invocation;
its [remaining reporting repair](../../design/toml-scoped-pack-imports-plan.md#remaining-presentation-and-verification)
is separate from correct absent-marker delivery. Scoped imports have no author-footprint counts.

Check's **readiness pass** is the existing whole-entry throwaway staging used by ordinary physical
readers. Its tree remains available to later sections and is cleaned when the command returns,
including the existing signal lifecycle. Mixed scoped groups add no effects to that baseline.
The whole `yolo check` command is not claimed to be effect-free; this feature migrates no readiness reader.

Author `pack lint` and `footprint` retain their directory/ref arguments. A path spelled like a
configured handle still means the author input; neither routes into configured selection.
Lint still reports a missing ordinary marker in the author's pack, although typed selection
can deliver that ordinary folder. Author validation and discoverable-skill counting are distinct.

## Editing and removal

```bash
yolo pack add local/corpus --skills-dir skills/core-tools --only skill-a --dry-run
yolo pack add local/corpus --skills-dir skills/core-tools --only skill-a
yolo pack add --git https://example.invalid/team/repo --ref v1 --only 'review*'
yolo config why 'packs[0].skills.only[0]'
yolo config set 'packs[0].skills.only[0]' '"skill-b"' --user
yolo pack remove local/corpus --skills-dir skills/core-tools
```

No table options preserves plain string writing. Table options require modern user TOML;
legacy files receive migration advice, not new legacy syntax. Repeat `--only`/`--exclude` for
selectors. `--subdir` alone preserves whole effects. Direct Git and positional handle are exclusive.

Bound complete-graph transactions preserve fragment ownership and neighboring comments.
Duplicate import identity refuses. Remove finds strings/tables by derived handle and removes
at most one tuple; ambiguous handles list indices/roots and exact indexed-edit remedies.
Empty selector/table edits refuse transactionally. Why reports the author file and value.
Legacy filtered migration retains the old bytes and refuses equivalence; candidate selectors
need a manual rewrite, not automatic conversion of old path globs.

## Permanent acceptance map

The acceptance IDs below map to observed callers and their source-named tests, not a promise
that every native setup was exercised. Grouped fixture files contain the named variants.
The nested integration cases use user TOML fragments, inert destination readers, no real agent,
local synthetic Git and owned workspaces; the short suite compiles but skips these jail cases.

| ID | Permanent caller/control boundary |
| :--- | :--- |
| A-01 | `TestDirectUserScopedPackImportsAcrossWorkspaces` in [nested callers](../../../integration/scopedpacks_test.go): malformed and valid poison manifests/plugin/derive metadata |
| A-02 | Same caller runs the user fragment in two independently owned workspaces |
| A-03 | `TestDirectUserScopedManifestSourcedRowsInJail` in [nested callers](../../../integration/scopedpacks_test.go): linked script reads sibling support, sibling marker unregistered |
| A-04 | `TestResolvePackScopedSelectorRefusals` and `TestResolvePackRawRootAndNonrecursiveCandidates` in [resolver controls](../../../internal/config/scopedpackprojection_test.go) |
| A-05 | `TestDirectUserScopedRawRootNamesInJail` in [root naming caller](../../../integration/scopedpackroots_test.go): bare/namespaced raw root |
| A-06 | Raw nested integration and nonrecursive root/child variants in [resolver controls](../../../internal/config/scopedpackprojection_test.go) |
| A-07 | `TestDirectUserScopedTypedMixedRowsInJail` in [nested callers](../../../integration/scopedpacks_test.go) |
| A-08 | Typed nested caller plus `TestSelectedPackDeclarationsProjectsTypedBeforeClosure` in [effective declaration controls](../../../internal/config/scopedpackprojection_test.go) |
| A-09 | Sourced nested caller plus `TestDirectScopedGitMixedRowsUseCommonPayloadAndComposer` in [mixed composer](../../../internal/cli/scopedpackgit_test.go) |
| A-10 | `TestHostApplyScopedModernBesideLegacyInclusionKeepsWithholding` in [mixed format host caller](../../../internal/cli/scopedpacklegacy_test.go) and [wire producer](../../../internal/config/scopedpacks_test.go) |
| A-11 | `TestResolvePackWholeRelocatedGitRootsUseOnlyNamedSubtree` in [direct/named Git whole resolver](../../../internal/config/scopedpackwholeroot_test.go), plus local whole variants in [resolver controls](../../../internal/config/scopedpackprojection_test.go) |
| A-12 | [Grammar refusals](../../../internal/config/scopedpacksgrammar_test.go), [selector resolver](../../../internal/config/scopedpackprojection_test.go) and [malformed Explain tuple](../../../internal/cli/scopedpackinvalidtuple_test.go) |
| A-13 | [Root confinement](../../../internal/config/scopedpackprojection_test.go), [named Git resolver](../../../internal/config/scopedpackgit_test.go), [payload kind/path validation](../../../internal/packsrc/projectedskillpayload_test.go) |
| A-14 | `TestLoadPackEntriesScopedImportTuples` and shipped/legacy conflicts in [grammar callers](../../../internal/config/scopedpacksgrammar_test.go) |
| A-15 | `TestScopedNeedsRefusesCanonicalShippedRoot` in [closure caller](../../../internal/config/scopedpackprojection_test.go) |
| A-16 | [Native editing/why dispatch](../../../internal/cli/scopedpackdispatch_test.go), [bound tuple edits/removal](../../../internal/cli/scopedpackediting_test.go) |
| A-17 | [Shared Git payload/composer](../../../internal/cli/scopedpackgit_test.go), `TestDirectScopedSameHandleRootsReplayAndCompareSeparately` in [restart diff](../../../internal/cli/run/scopedpackdiff_test.go) |
| A-18 | `TestDirectScopedPackRecordedAttachIgnoresDeletedSource` in [attach/composer caller](../../../internal/cli/run/scopedpackrecord_test.go), [tamper controls](../../../internal/packload/scopedrecord_test.go) |
| A-19 | Host and guest compose the same hydrated record in [attach parity caller](../../../internal/cli/run/scopedpackrecord_test.go), plus [host mixed format apply](../../../internal/cli/scopedpacklegacy_test.go) |
| A-20 | [Pure Explain/dry-run](../../../internal/cli/scopedpackcached_test.go), [edit validation](../../../internal/config/scopedpackeditvalidation_test.go), [scoped Check](../../../internal/cli/check/scopedpackcached_test.go) |
| A-21 | Own/root/none/conflict variants in [projected license validation](../../../internal/packsrc/projectedskilllicense_test.go), reached by [mixed composer](../../../internal/cli/scopedpackgit_test.go) |
| A-22 | `TestScopedPackMigrationRetainsLegacyFiltersAndNamesManualRewrite` in [real Plan/Apply retention](../../../internal/configmigrate/scopedpackfilters_test.go) |
| A-23 | Typed nested caller, [Git mixed composer](../../../internal/cli/scopedpackgit_test.go), [inspection counts](../../../internal/cli/scopedpackinspection_test.go), [author parity](../../../internal/cli/scopedpacktuples_test.go) |
| A-24 | Raw/legacy marker requirements in [payload variants](../../../internal/packsrc/projectedskillpayload_test.go) and raw resolver no-match |
| A-25 | Nonregular present-marker variants in [payload validation](../../../internal/packsrc/projectedskillpayload_test.go) |
| A-26 | Kind/state/mode/version/coverage/content tampering in [record controls](../../../internal/packload/scopedrecord_test.go), [jail loader](../../../internal/entrypoint/scopedpackrecord_test.go) |
| A-27 | `TestDirectScopedGitMixedRecordedRowsIgnoreDeletedSourceAndCache` in [Git record caller](../../../internal/cli/run/scopedpackgitrecord_test.go) and local recorded attach |
| A-28 | Typed nested inert hooks data plus plugin-classified selector refusal in [resolver controls](../../../internal/config/scopedpackprojection_test.go) |
| A-29 | [Modern beside legacy host apply](../../../internal/cli/scopedpacklegacy_test.go), [legacy marker compatibility](../../../internal/packsrc/projectedskillpayload_test.go) |
| A-30 | `TestDirectScopedRefreshFailureKeepsAdvancedPinWithoutPublication` in [refresh/attach caller](../../../internal/cli/run/scopedpackrefresh_test.go) |
| A-31 | Both selector and known-good full-ref recoveries, plus repeated unchanged refusal, in the same [refresh caller](../../../internal/cli/run/scopedpackrefresh_test.go) |
| A-32 | [Three legal tuples and baseline body](../../../internal/cli/scopedpacktuples_test.go), [cached/unverified/refused state](../../../internal/cli/scopedpackcached_test.go), [mixed readiness baseline](../../../internal/cli/check/scopedpackmixed_test.go) |
| A-33 | `TestAuthorLintAndFootprintIgnoreScopedUserSelection` in [author parity callers](../../../internal/cli/scopedpacktuples_test.go) |

The reviewed refusal repairs have additional actual caller controls in
[CLI review regressions](../../../internal/cli/scopedpackreview_test.go) and
[Check review regressions](../../../internal/cli/check/scopedpackreview_test.go): whole-subdir
transactions validate selected source versus delivered roots; inaccessible scoped local sources
permit unrelated jail edits with honest unchecked coverage; cached payload license refusals
propagate through Explain and Check without writes.

## What verification cannot infer

Caller-deletion controls must lose real output/state when selection, recorded hydration or common
registration dispatch is removed. Helper-only assertions and comment matching are not delivery proof.
Transport-refusing Git and before/after snapshots establish pure-reader effects; they do not prove
that the unrelated whole check command has no effects.
Nested Podman uses shared networking/user namespace here: native reachability/rootless-only paths
remain outside these observations. No real agent, installer or provider API is part of this evidence.
