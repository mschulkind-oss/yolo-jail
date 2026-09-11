---
title: "RETIRED: merged into provisioner-sets.md (2026-09-11)"
date: 2026-09-11
status: deprecated
tags: [retired, nix, notch, host, guest, provisioning]
summary: "RETIRED 2026-09-11. This doc's subject — how a tool closure reaches a notch with no container image — was one resolver's view of a larger question, and it was merged into provisioner-sets.md. Its questions keep their numbers under an NX prefix; two were folded into questions that doc already had. Nothing was deleted: the live material, the shipped rulings and every preserved trap moved."
vantage:
  status-chip: true
---

# RETIRED: merged into [`provisioner-sets.md`](provisioner-sets.md) (2026-09-11)

> [!IMPORTANT]
> **This document is retired. It was merged into
> [`provisioner-sets.md`](provisioner-sets.md) on 2026-09-11 and holds no content of its own.**
> Do not add to it, and do not cite it as the owner of anything — cite the merged doc.

**Why it was retired.** The two docs held one subject from two directions.
[`provisioner-sets.md`](provisioner-sets.md) has the **model**: every environment has a
*provisioner set* — the mechanisms that can make a binary present there — and a pack should
declare a **need** the environment resolves, rather than naming one backend with `via`. This doc
had the **depth on a single resolver**: nix below the jail notch. Its own load-bearing finding —
*"a nix env is not a peer of confinement; it is the missing provisioning primitive below the
`jail` notch"* — is the same conclusion the merged doc reaches for every resolver at once, so
this doc was that doc's argument, reached early and stopped at one row of the table. Two of its
six live questions were the merged doc's own questions asked earlier.

**Where its content went**, all of it inside
[`provisioner-sets.md`](provisioner-sets.md):

| What it held | Now |
| :--- | :--- |
| The nix resolver: what is shipped, the four mechanisms, `nix profile --profile`, the orthogonality finding, macOS vs Linux, the no-nix cases | [§6](provisioner-sets.md#6-the-nix-resolver-in-depth), in full |
| The coverage matrix (which manager covers how many of the six agent CLIs) | [§4](provisioner-sets.md#4-the-coverage-matrix-which-manager-covers-what) — the two docs carried two copies; this is the survivor |
| The `install_hints`-vs-nix-profile comparison | [§4.1](provisioner-sets.md#41-install_hints-and-a-nix-profile-are-complementary-not-competitors) |
| The isolation/environment split (*"mimic our in-jail envs more"*) | [§6.5](provisioner-sets.md#65-the-isolationenvironment-split-what-a-non-container-notch-can-reproduce) |
| Options 0–3 and their shipped status | [§10](provisioner-sets.md#10-alternatives-each-with-a-verdict), alternatives G, H, I and J |
| The preserved traps: the devShell dump, the unfree warn-and-skip, the `x86_64-darwin` retraction, the GC root's four deliberate properties | the `> [!WARNING]` blocks in [§6.2](provisioner-sets.md#62-the-four-nix-mechanisms-compared-and-why-never-a-devshell), [§6.7](provisioner-sets.md#67-macos-vs-linux-coverage-freshness-and-the-traps) and [§6.8](provisioner-sets.md#68-what-if-the-user-has-no-nix) |
| Its Decision Ledger and its verified-facts tables | the merged [Decision Ledger](provisioner-sets.md#decision-ledger) and [§14.1](provisioner-sets.md#141-inherited-from-the-retired-doc-with-its-own-dates), which keeps their original 2026-08-02 / 2026-08-23 dates |

**Question ids.** Every number survives; each gained an `NX` prefix, because bare numbers collide
once two docs share one file — concretely, this doc's [`OQ-9`](provisioner-sets.md#OQ-NX9) and
[`../plans/environment-manager-plan.md`](../plans/environment-manager-plan.md)'s
[`OQ-9`](../plans/environment-manager-plan.md#open-questions-to-resolve-before-their-phase) are
different questions and the merged doc cites both. The full map, including the two that were
folded into questions the merged doc already had, is
[there](provisioner-sets.md#question-id-map-old-spelling--new):

| Was | Now |
| :--- | :--- |
| [`OQ-1`](provisioner-sets.md#decision-ledger) (also cited as `N3`) | [`OQ-NX1`](provisioner-sets.md#decision-ledger) — settled |
| [`OQ-2`](provisioner-sets.md#decision-ledger) (also cited as `N1`) | [`OQ-NX2`](provisioner-sets.md#decision-ledger) — settled |
| [`OQ-3`](provisioner-sets.md#decision-ledger) | folded into [`OQ-PS1`](provisioner-sets.md#OQ-PS1)(c) |
| [`OQ-4`](provisioner-sets.md#decision-ledger) | [`OQ-NX4`](provisioner-sets.md#OQ-NX4) |
| [`OQ-5`](provisioner-sets.md#decision-ledger) | [`OQ-NX5`](provisioner-sets.md#OQ-NX5) |
| [`OQ-6`](provisioner-sets.md#decision-ledger) | [`OQ-NX6`](provisioner-sets.md#decision-ledger) — settled |
| [`OQ-7`](provisioner-sets.md#decision-ledger) | folded into [`OQ-PS6`](provisioner-sets.md#OQ-PS6) |
| [`OQ-8`](provisioner-sets.md#decision-ledger) | [`OQ-NX8`](provisioner-sets.md#OQ-NX8) |
| [`OQ-9`](provisioner-sets.md#decision-ledger) | [`OQ-NX9`](provisioner-sets.md#OQ-NX9) |
| `N2` | unchanged — a roadmap id, in the merged [Decision Ledger](provisioner-sets.md#decision-ledger) |

**Ownership that moved with it.** [`program-delivery.md`](program-delivery.md)'s
[`OQ-PD16`](program-delivery.md#decision-ledger) named this doc as the host notch's owner
(2026-09-03). It carries a dated amendment naming
[`provisioner-sets.md`](provisioner-sets.md) instead, made in the same commit as this
retirement.

**Two corrections the merge found in this doc, recorded so nobody re-derives them from the git
history:** its `flake.nix:1204` / `:1210` citations are stale (the attrs are at `:1636` and
`:1642` as of 2026-09-11), and its *"four npm, two installer"* pack census is stale (three and
three since `codex` flipped on 2026-09-04). Both are fixed in the merged doc.
