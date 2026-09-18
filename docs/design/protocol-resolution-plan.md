---
title: "Protocol resolution — implementation sketch, spent"
date: 2026-09-18
status: accepted
tags: [plan, providers, packs, wire-bridge, protocols, sketch, graduated]
summary: "A stub. Every step this hand-off sketched is built and the design graduated to docs/reference/protocol-resolution.md, which carries the three findings that outlived the build. What is left here is build residue — the file map, the step order, the draft refusal strings — and it dies with this file."
vantage:
  status-chip: true
---

# Protocol resolution — implementation sketch, spent

**Status:** SPENT, 2026-09-18 — every build step landed, and the design it served graduated to
[`../reference/protocol-resolution.md`](../reference/protocol-resolution.md). A plan is a hand-off
artifact, not an evergreen record; this one has been consumed.

> [!IMPORTANT]
> **What survived, and where it went.** Three things this sketch learned in the building, all of
> them corrections to itself, are stated as behaviour in the reference rather than as history:
>
> - **The refusal did not land where this file said.** It is at `packload.AgentEnv`, not in the
>   run pre-flight, and the reason is load-bearing — the gate needs a resolved SELECTION, and the
>   pre-flight reads the merged user config only, so it cannot see a pack-shipped provider's
>   endpoints and a copy there would refuse a different set of launches.
>   [Where the gate lives](../reference/protocol-resolution.md#where-the-gate-lives-and-why-not-in-the-run-pre-flight).
> - **The adapter's port could not leave every pack**, because a declaration has to live where it
>   is declared. What it did leave is every CONSUMER, and the restated done-condition is in
>   [What the done-conditions could not be](../reference/protocol-resolution.md#what-the-done-conditions-could-not-be).
> - **There were more derives reading the shorthand than this file listed**, and two of the arms
>   it would have had you delete are not dead code — a jail launched by an older host `yolo` still
>   carries the key, because the retirement is an error on the host and a warning in a jail. The
>   retirement condition, and the ⚠ that goes with it, are in
>   [the shorthand's section](../reference/protocol-resolution.md#the-single-protocol-base_url-shorthand-is-removed).
>
> **What did not survive, deliberately:** the file-by-file change map, the seven-step build order,
> the draft refusal strings, the reuse notes and the `Blocked` section. Every one describes work
> that is finished, and git holds them.

**Why it still exists.** Inbound references only. Nothing outside `docs/` names this path; when
the citation sweep has repointed the ones that do, **delete this file**, which is what the
graduation would otherwise have done here.
