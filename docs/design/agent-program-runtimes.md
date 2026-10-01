---
title: "Agent program runtimes — graduated"
date: 2026-09-21
status: accepted
tags: [packs, programs, mise, node, provisioning, graduated]
summary: "A pointer. The whole of this design graduated to docs/reference/agent-program-runtimes.md: the Node floor, its resolution, the launcher splice and the refusal on 2026-09-25, and the three gaps this stub then held (OQ-AR5 to OQ-AR7, decided as AR-L3 to AR-L7 and built 2026-09-30) on 2026-10-01. Nothing is owed here."
stage: GRADUATED
next: "Nothing is owed here. Delete this pointer once the roadmap's link to it moves to the reference"
---

# Agent program runtimes — graduated

**Status:** 2026-10-01 — every id this file held now resolves in
[`../reference/agent-program-runtimes.md`](../reference/agent-program-runtimes.md#why-its-this-way),
which is the authority. Its body graduated on 2026-09-25, and the three questions left here
graduated on 2026-10-01, after they were decided and built on 2026-09-30:

| Id | What it decided |
| :--- | :--- |
| [`OQ-AR5`](../reference/agent-program-runtimes.md#oq-ar5), decided as [`AR-L3`](../reference/agent-program-runtimes.md#ar-l3) | a declared floor starts macos-user's stage unless the host can show it met |
| [`OQ-AR6`](../reference/agent-program-runtimes.md#oq-ar6), decided as [`AR-L4`](../reference/agent-program-runtimes.md#ar-l4) | the bootstrap runs whether or not `mise install` succeeded |
| [`OQ-AR7`](../reference/agent-program-runtimes.md#oq-ar7), decided as [`AR-L5`](../reference/agent-program-runtimes.md#ar-l5) | the stage regenerates the launchers of a floor it met |
| [`AR-L6`](../reference/agent-program-runtimes.md#ar-l6) | the regeneration finishes the boot's own render |
| [`AR-L7`](../reference/agent-program-runtimes.md#ar-l7) | the host reads the staged pack tree strictly and asks only candidate 1 |

UNMEASURED, as the reference states: no run on a real workload, and the macos-user half has not
run on a Mac. The questions' options and leanings are in git history
(`git log --follow -- docs/design/agent-program-runtimes.md`).
