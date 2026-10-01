---
title: "Proposed fixes for the open findings — retired index"
status: accepted
stage: GRADUATED
next: "Nothing is owed here. Delete this pointer once the roadmap's link to it moves"
tags: [findings, packs, programs, graduated]
summary: "A pointer. This was the index of eleven findings from building the first real packs (2026-08-02), each with a proposed fix and the maintainer's ruling. All eleven shipped or were overtaken, and on 2026-10-01 each outcome was confirmed described where it now lives; the one ruling with no other home, two provenance derivations until a third exists, moved into pack-system.md. The proposals and their review are in git history."
---

# Proposed fixes for the open findings — retired index

**Status:** 2026-10-01 — every item is closed in the tree, and each outcome is described in the
document that owns it, so this index is retired. Confirmed that day against `d4e435a3` by
reading each target below. MEASURED on 2026-09-30, as the index recorded:
`TestProvenanceParityAcrossBothDerivations`, `TestBlockedAndDeclaredToolGetsBothAndBlockerWins`,
`TestNoLauncherForANameTheImageProvides` and
`TestTheCollisionCheckNeverConsidersTheInstallPrefixes` passed in `internal/entrypoint`.
UNMEASURED then and since: #9's nightly, and the host-side #10 and #11 were not re-run. The
proposals, the review that ruled them and the three reversals it recorded are in git history
(`git log --follow -- docs/plans/proposed-fixes-open-findings.md`).

| # | Finding | Where the outcome is described |
| :--- | :--- | :--- |
| 1 | A `program` launcher shadowed a baked binary | [`pack-system.md`'s `program` section](../reference/pack-system.md#program): no launcher is written for a name the image provides, the generation-time half of [`OQ-PD12a`](../design/program-delivery.md#decision-ledger), which reversed the 2026-08-02 split of installers after `/bin` |
| 2 | Presence and install were one kind | [`requires`](../reference/pack-system.md#requires), ruling [`Q1.3`](../reference/pack-system.md#q1-3) |
| 3 | Only the first `program` in a pack installed | ruling [`Q2.1`](../reference/pack-system.md#q2-1) |
| 4 | A dropped pack's staged tree kept rendering | ruling [`Q3.1`](../reference/pack-system.md#q3-1), superseded by [`OQ-PK2`](../reference/pack-system.md#oq-pk2): each launch stages a tree of its own |
| 5 | The dependency manifest could not name a brew cask | [`pack-system.md`'s `program` section](../reference/pack-system.md#program), the `install_hints` passage (`brew-cask`) |
| 6 | `install_hints` routed agent CLIs through nix | the warning under that passage: agent packs carry no `nix` hint |
| 7 | `packages: ["claude-code"]` failed with a raw nix trace | [`OQ-NX6`](../design/provisioner-sets.md#decision-ledger) in `provisioner-sets.md` |
| 8 | `rmwProvenance` was a second "which layer won" | [two provenance derivations](../reference/pack-system.md#two-provenance-derivations), and [the ruling](../reference/pack-system.md#two-provenance-derivations-row): unify at the third |
| 9 | The nightly macOS builder's arch | [`BACKLOG.md`](BACKLOG.md), `E8`, done |
| 10 | A pack could not install Claude MCP servers on the host | [`host-apply-staleness.md`](../reference/host-apply-staleness.md): the `${workspace}`-keyed prune, and the first-apply confirm (`confirmHostLosses`) |
| 11 | A dropped pack's host output was never retired | [`pack-system.md`](../reference/pack-system.md#retiring-a-dropped-packs-host-output) |
