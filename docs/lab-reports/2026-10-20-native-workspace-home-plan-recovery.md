---
title: "Native workspace-home plan provenance"
date: 2026-10-20
tags: [lab-report, macos-user, workspaces, home, seatbelt]
summary: "Reconciles the archived one-account workspace-home proposal, the later audit-derived global HOME implementation, current bounded history coverage, and the owner-selected but unbuilt concurrent-workspace architecture."
---

# Native workspace-home plan provenance, 2026-10-20

A [lab report](README.md). This report separates archived design evidence, native conversation coverage, the current implementation, and the architecture confirmed by the owner on 2026-10-20. It records no implementation or runtime evidence.

## Finding

The selected architecture is **parallel private workspace homes and persistent workspace state under exactly one guest OS user**, with Seatbelt allowing only the selected workspace and explicitly selected shared machine paths. A separate OS account per workspace and waiting for or refusing a second workspace as product policy are not selected. The owner's 2026-10-20 confirmation and the [HT6 decision ledger](../reference/macos-user-home-tiers.md#decision-ledger) are the current authority.

The [concurrent-workspace design](../design/macos-user-model.md#7-the-ruled-model-two-modes-one-profile) records this choice separately from its remaining questions about the same-UID threat boundary and state reclamation. The exact physical home/state layout and shared-link geometry still need design and native verification; no implementation or native trial is authorized by this report.

## Source chronology

| Evidence | What it establishes | Limit |
| :--- | :--- | :--- |
| Archived source proposal, Git commit `d28f951f3` (2026-09-03) | Proposed multiple private workspace homes under one guest account, with selected shared machine directories and a narrower Seatbelt path set. It also rejected creating one OS account per workspace. | This was a proposal. Its example location was illustrative, not an owner-approved physical path or schema. |
| Home-tier decision history, Git commits `c21f6848c`, `2b70665a5`, and `70dd6fa98` (2026-09-11) | HT4 was first left open, then marked settled **“by principle in audit”** and compacted. The global-HOME symlink layout is therefore recorded as an audit-derived implementation choice based on parity, not as a recovered direct owner prohibition on per-workspace homes. See the current [HT4 record](../reference/macos-user-home-tiers.md#oq-ht4). | The audit record alone cannot establish what the original conversation said or settle the later concurrency choice. |
| Current implementation hold, Git commit `fe427df7b` (2026-10-05), and [HT-D15](../reference/macos-user-home-tiers.md#ht-d15) | The account-home hold is a reversible guard for the then-current mutable global-HOME link set. | This describes current implementation behavior; it is not the selected future product policy. |
| Native Pi and Claude history review (bounded, completed 2026-10-20) | Four topical query angles and four bounded session reads corroborated the one-guest-user, parallel-workspace direction and the need to preserve workspace isolation. | Some early-September sessions did not appear in the available index. That is a coverage gap, not evidence that no discussion occurred. Session identifiers and conversation text are intentionally not reproduced. |
| Owner clarification (2026-10-20), recorded in the [HT6 decision ledger](../reference/macos-user-home-tiers.md#decision-ledger) | Settles the architecture: one guest user, private parallel workspace homes/state, and explicit shared machine paths. | It does not select the exact physical backing, settle every residual threat/lifetime detail, or show that the architecture has shipped. |

## Current code and selected architecture are different facts

At source baseline `94989f129274a6e377a4c069b182ba65bea0e980`, ordinary `macos-user` launches still use the constant guest HOME and the per-workspace sidecar layout described in the [current home-tier reference](../reference/macos-user-home-tiers.md#concurrent-workspace-ruling-and-current-implementation). The current account-home hold still refuses another workspace while that mutable link set is in use. This code was not changed by the documentation reconciliation.

The selected future model preserves private workspace state and existing declared sharing under one UID. Its kernel boundary must deny sibling workspace homes/state while allowing only the already-selected shared paths; it does not add credential, provider, MCP, host-file, account, or sudo permissions. Do not infer that current global-HOME callers already implement parallel homes, or remove the hold before the replacement and its native behavior are verified.

## Remaining limits

- The status of arbitrary same-UID processes running outside the applicable Seatbelt profile remains open as [OQ-MCW2](../design/macos-user-model.md#decision-ledger). The design makes no isolation claim about such a process.
- The explicit event, if any, that can reclaim persistent workspace-home/state bytes remains open as [OQ-MCW3](../design/macos-user-model.md#decision-ledger). Pi-session cleanup is a separate lifecycle and does not decide ordinary workspace-home retention.
- The broad shared-resource scope is settled: selected pack machine scope and existing configured shared paths remain shared. Exact backing and relative-hook geometry are engineering design, not new permission; native Seatbelt enforcement remains unmeasured.
- The early-September index gap is not proof that a prior ruling did or did not occur. The present owner clarification is the authority regardless of that incomplete historical coverage.

No source code, account configuration, privileges, credentials, Mac runtime behavior, or agent/API behavior was changed or exercised.
