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

**Status:** SUPERSEDED, 2026-09-11 — merged whole into
[`provisioner-sets.md`](provisioner-sets.md); its questions keep their numbers there under an `NX`
prefix.

> [!IMPORTANT]
> **This document is retired. It was merged into
> [`provisioner-sets.md`](provisioner-sets.md) on 2026-09-11 and holds no content of its own.**
> Do not add to it, and do not cite it as the owner of anything — cite the doc that holds the
> material now, which the table below names directly.

**Why it was retired.** The two docs held one subject from two directions.
[`provisioner-sets.md`](provisioner-sets.md) has the **model**: every environment has a
*provisioner set* — the mechanisms that can make a binary present there — and a pack should
declare a **need** the environment resolves, rather than naming one backend with `via`. This doc
had the **depth on a single resolver**: nix below the jail notch. Its own load-bearing finding —
*"a nix env is not a peer of confinement; it is the missing provisioning primitive below the
`jail` notch"* — is the same conclusion the merged doc reaches for every resolver at once, so
this doc was that doc's argument, reached early and stopped at one row of the table. Two of its
six live questions were the merged doc's own questions asked earlier.

**Where its content went.** It was merged into [`provisioner-sets.md`](provisioner-sets.md) on
2026-09-11, and **that doc was itself split three ways on 2026-09-20**: the model, the rulings and
the questions stayed there, the survey became
[`provisioner-evidence.md`](provisioner-evidence.md), and the Mac measurements became
[`../plans/runbooks/mac-provisioner-measurements.md`](../plans/runbooks/mac-provisioner-measurements.md).
**Every row below names the final home**, so that intermediate hop is stated here once and never
per row. Most of this doc's material rode the split into the evidence doc, which is where the nix
depth now lives; what stayed with the model is the handful of findings its rulings quote.

| What it held | Now |
| :--- | :--- |
| The nix resolver: what is shipped, the four mechanisms, `nix profile --profile`, the orthogonality finding, macOS vs Linux, the no-nix cases | [the evidence doc's nix resolver](provisioner-evidence.md#3-the-nix-resolver-in-depth), in full |
| The coverage matrix (which manager covers how many of the six agent CLIs) | [the evidence doc's coverage matrix](provisioner-evidence.md#2-the-coverage-matrix-which-manager-covers-what) — the two docs carried two copies; this is the survivor |
| The `install_hints`-vs-nix-profile comparison | [the complementarity section](provisioner-evidence.md#21-install_hints-and-a-nix-profile-are-complementary-not-competitors) |
| The isolation/environment split (*"mimic our in-jail envs more"*) | [the isolation/environment split](provisioner-evidence.md#35-the-isolationenvironment-split-what-a-non-container-notch-can-reproduce) |
| Options 0–3 and their shipped status | [the model doc's alternatives](provisioner-sets.md#10-alternatives-each-with-a-verdict), alternatives G, H, I and J |
| The preserved traps: the devShell dump, the unfree warn-and-skip, the `x86_64-darwin` retraction, the GC root's four deliberate properties | the `> [!WARNING]` blocks in [the four mechanisms compared](provisioner-evidence.md#32-the-four-nix-mechanisms-compared-and-why-never-a-devshell), [macOS vs Linux](provisioner-evidence.md#37-macos-vs-linux-coverage-freshness-and-the-traps) and [what if the user has no nix](provisioner-evidence.md#38-what-if-the-user-has-no-nix) |
| Its Decision Ledger | the model doc's [Decision Ledger](provisioner-sets.md#decision-ledger) |
| Its verified-facts tables | [the facts inherited from this doc](provisioner-evidence.md#41-inherited-from-the-retired-doc-with-its-own-dates), which keep their original 2026-08-02 / 2026-08-23 dates |

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
| [`OQ-3`](provisioner-sets.md#decision-ledger) | folded into [`OQ-PS1`](provisioner-sets.md#OQ-PS1)(c), and carved out again as [`OQ-PS10`](provisioner-sets.md#OQ-PS10) on 2026-09-11 |
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
history:** its `flake.nix` citations for the two `yolo*Packages` attrs are stale, and its
*"four npm, two installer"* pack census is stale (three and three since `codex` flipped on
2026-09-04). Both are fixed and re-resolved in
[`provisioner-evidence.md`](provisioner-evidence.md#4-facts-verified-and-how), which is where the
nix depth and the census both ended up.
