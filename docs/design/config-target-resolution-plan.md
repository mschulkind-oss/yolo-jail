---
title: "Plan: one resolved config target — spent"
date: 2026-09-17
status: accepted
tags: [plan, config, cli, implementation, graduated]
summary: "A stub. Every step of this hand-off is built and the design graduated to docs/reference/config-target-resolution.md, which carries the traps and the two open blockers that outlived the build. What is left here is build residue — the file map, the step order, the test-rewrite list — and it dies with this file."
vantage:
  status-chip: true
---

# Plan: one resolved config target — spent

**Status:** SPENT, 2026-09-18 — every build step landed, and the design it served graduated to
[`../reference/config-target-resolution.md`](../reference/config-target-resolution.md). A plan
is a hand-off artifact, not an evergreen record; this one has been consumed.

> [!IMPORTANT]
> **What survived, and where it went.** The reference carries everything from this file that
> outlives the build: the two-`render.Target` split and why collapsing it breaks either the
> store or the preview; the backend-aware resolver that maps a surface path to the host file
> backing a jail's home; the reason the test seam is the whole target rather than a stubbable
> predicate; and the two blockers this plan raised that are still **open** — `yolo apply
> --sealed` keeping the bare-working-directory walk, and the duplicated `--at` token parse —
> stated as live gaps in
> [*Where this does not reach*](../reference/config-target-resolution.md#where-this-does-not-reach).
> The blockers this plan answered in the building (the fifth host-layer disposition, and the
> tri-state liveness probe whose `--force` reaches both refusals) are stated as behaviour there,
> not as answers to questions.
>
> **What did not survive, deliberately:** the file-by-file change map, the eight-step build
> order, the tests-to-rewrite list, the *expensive if late* notes, and this file's `file:line`
> corrections to the design. Every one describes work that is finished, and git holds them.

**Why it still exists.** Inbound references only. Nothing outside `docs/` names this path; when
the citation sweep has repointed the ones that do, **delete this file**, which is what the
graduation would otherwise have done here.
