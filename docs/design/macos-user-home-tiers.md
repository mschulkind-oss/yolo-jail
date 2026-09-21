---
title: "GRADUATED: the macos-user home tiers are a reference now (2026-09-21)"
date: 2026-09-21
status: deprecated
tags: [retired, macos-user, jail-home, backend-parity, design]
summary: "GRADUATED 2026-09-21 into docs/reference/macos-user-home-tiers.md. The design was built on 2026-09-12 and had no live questions left, so the reader stopped being someone deciding and became a maintainer. The reference states the system as built, reconciled against the tree; every ruling id survives there, including OQ-HT2, which other documents and Go comments cite."
vantage:
  status-chip: true
---

# GRADUATED: the macos-user home tiers are a reference now (2026-09-21)

**Status:** SUPERSEDED, 2026-09-21 — graduated whole into
[`../reference/macos-user-home-tiers.md`](../reference/macos-user-home-tiers.md), which is the
only place this subject is described. Designed 2026-09-03, built 2026-09-12, all four questions
settled 2026-09-11.

> [!IMPORTANT]
> **This document holds no content of its own.** Do not add to it and do not cite it as the owner
> of anything — cite the reference, whose sections the table below names. This file exists because
> inbound links do, in other documents and in Go comments.

**Why it graduated.** A design doc serves somebody **deciding**, and by 2026-09-12 there was
nothing left to decide: the layout shipped, all four questions were ruled, and the parts a reader
still needed — what the tiers are, what the mirror is for, which order the boot must keep, what an
occupied path does — are maintenance facts rather than arguments. The reference states them in the
present tense and was **reconciled against the tree** on the way, which the design doc could not
be: four of its five unmeasured claims have since been measured on hardware, its stated residual
about the courtesy flock has shipped, and its `file:line` citations had all drifted.

**What graduation dropped**, because the deciding reader is gone: the alternatives table (A, A′,
B, C, D), the risks table, the sequencing, the open-questions scaffold, and the decision ledger as
a *ledger*. Every ruling's **reason** survives, rewritten as a fact about the system.

**Where its material went.** Anchors on this page no longer resolve; these do.

Section numbers are deliberately not spelled below — they were this doc's and they are gone. The
old headings are, so a citation can be matched by name.

⚠ **Except for the ones a Go comment actually names.** A citation in source cannot be found and
repointed by any markdown tool, so the numeric and letter anchors that reach this page from code
are routed explicitly rather than left to the heading table. Measured 2026-09-21 by grepping
`internal/`, `cmd/` and `integration/` for this basename — this is the whole set, not a sample:

| Cited as | Cited from | Now |
| :--- | :--- | :--- |
| *alternative A′* | `internal/entrypoint/darwinhomelayout.go`, `internal/entrypoint/darwin.go`, and the integration twins (4 sites) | the shipped shape — [The layout: what is a symlink, what is a mirror](../reference/macos-user-home-tiers.md#the-layout-what-is-a-symlink-what-is-a-mirror). There is no "A′" any more because there are no alternatives to letter; it is simply what the backend does |
| [*§5.3*](../reference/macos-user-home-tiers.md#the-mirror-and-the-relative-credential-link-it-exists-for) | `internal/entrypoint/darwinhomelayout.go` (2 sites) | [The mirror, and the relative credential link it exists for](../reference/macos-user-home-tiers.md#the-mirror-and-the-relative-credential-link-it-exists-for) |

| This doc's heading | Now |
| :--- | :--- |
| *The shape of the problem*, *What the collapse actually costs* | [The three tiers, and where each one lives](../reference/macos-user-home-tiers.md#the-three-tiers-and-where-each-one-lives) — as the shape that holds, not the defect that did |
| *Why it has not been fixed by simply splitting*, *What the credential tier then needs, precisely* | [The mirror, and the relative credential link it exists for](../reference/macos-user-home-tiers.md#the-mirror-and-the-relative-credential-link-it-exists-for) |
| *The proposal*, *The plumbing already exists* | [The layout: what is a symlink, what is a mirror](../reference/macos-user-home-tiers.md#the-layout-what-is-a-symlink-what-is-a-mirror) |
| *The constraint that outranks the layout choice: one mechanism, every backend* | [`OQ-HT4`](../reference/macos-user-home-tiers.md#oq-ht4) |
| *Isolation is already enforced for workspaces* | [Isolation: the Seatbelt profile needs no change](../reference/macos-user-home-tiers.md#isolation-the-seatbelt-profile-needs-no-change) |
| *Seatbelt can replace more mounts than this one* | [Seatbelt does the read-only half of a bind](../reference/macos-user-home-tiers.md#seatbelt-does-the-read-only-half-of-a-bind-and-the-launcher-does-the-other) — compacted, with the per-declaration census handed to [`declaration-parity.md`](declaration-parity.md), which owns `DP-L1` |
| *The principles this rests on* | [`P1`](../reference/macos-user-home-tiers.md#p1), [`P2`](../reference/macos-user-home-tiers.md#p2) |
| *What shipped*, and its ordering rules | [Where the layout runs in the boot](../reference/macos-user-home-tiers.md#where-the-layout-runs-in-the-boot-and-the-order-it-must-keep) and [What is measured, and by what](../reference/macos-user-home-tiers.md#what-is-measured-and-by-what) |
| *Alternatives*, *Risks*, *What this does NOT propose* | dropped as a genre; the reasons that outlived them are in [Why it is this way](../reference/macos-user-home-tiers.md#why-it-is-this-way) |
| *Decision Ledger* | [Why it is this way](../reference/macos-user-home-tiers.md#why-it-is-this-way) — ids intact: [`OQ-HT1`](../reference/macos-user-home-tiers.md#oq-ht1), [`OQ-HT2`](../reference/macos-user-home-tiers.md#oq-ht2), [`OQ-HT3`](../reference/macos-user-home-tiers.md#oq-ht3), [`OQ-HT4`](../reference/macos-user-home-tiers.md#oq-ht4) |

⚠ **[`OQ-HT2`](../reference/macos-user-home-tiers.md#oq-ht2) is cited from Go comments** —
`internal/entrypoint/darwinhomelayout.go`, `internal/entrypoint/darwin.go`,
`internal/entrypoint/packhooks.go` and the integration twins name it by id, and one refusal
message prints it — so it keeps its spelling and its anchor in the reference. A markdown tool
cannot see a citation from a Go comment; the id surviving is what keeps those pointing somewhere.
