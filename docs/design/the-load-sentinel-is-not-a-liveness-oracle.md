---
title: "The load sentinel is not a liveness oracle — graduated to the reference tree"
date: 2026-09-18
status: accepted
tags: [prune, images, storage, incident, graduated]
summary: "A stub. The as-built account is docs/reference/image-retention.md — the two reapers and the two questions they ask, P1–P3, the per-workspace current-image pointers, the GC-root age policy, the decline vocabulary and every OQ-LS ruling. This filename survives only while inbound references are repointed."
---

# The load sentinel is not a liveness oracle — graduated to the reference tree

**Status:** GRADUATED, 2026-09-18 — the settled body of this doc moved to
[`../reference/image-retention.md`](../reference/image-retention.md). Nothing is owed here.

> [!IMPORTANT]
> **The design is BUILT and measured, and this file is no longer where it is described.**
> [`../reference/image-retention.md`](../reference/image-retention.md) is the as-built account:
> the two reapers and why they ask different questions; `P1`–`P3`, including the one deliberate
> exception to `P3`; what the load sentinel may and may not be cited for; image retention as one
> pointer per workspace with no undo buffer; the GC-root reaper's pure age policy and what its
> modification time now means; the decline vocabulary and its volume rule; and
> [`## Why it's this way`](../reference/image-retention.md#why-its-this-way), where
> [`OQ-LS1`](../reference/image-retention.md#why-its-this-way),
> [`OQ-LS2`](../reference/image-retention.md#why-its-this-way) and
> [`OQ-LS3`](../reference/image-retention.md#why-its-this-way) resolve.
>
> **The filename changed and the move is deliberate.** A reference is landed in from a grep hit,
> so its title and its path name the thing rather than make a claim about it — this doc's title
> was an argument, and the argument is over.
>
> **One residual this doc flagged is closed.** It warned that `internal/prune/imageroots.go`
> justified its age cutoff with a sentence the stock-tag short-circuit had made false. That
> comment now states the weaker, correct meaning: the modification time is the last time a launch
> *built and delivered* an image, not the last time one used it.

**Why it still exists.** Prose citations of this path — most of them naming section numbers this
graduation deliberately did not inherit — live in `internal/prune/prune.go`,
`internal/prune/currentimages.go`, `internal/prune/imageroots_probe.go`,
`internal/prune/autoreap.go`, `internal/cli/prunekeepimages_test.go`,
`internal/cli/run/currentimage.go` and `internal/cli/run/currentimagecallsite_test.go`, none of
which this workflow owns. Point each of them at
[`../reference/image-retention.md`](../reference/image-retention.md) — the `OQ-LS` citations at
its [`#why-its-this-way`](../reference/image-retention.md#why-its-this-way) anchor — and **delete
this file**.

## 11.1 Decision Ledger

**Moved.** Every `OQ-LS` ruling now lives in
[`../reference/image-retention.md`](../reference/image-retention.md#why-its-this-way)'s
*Why it's this way* appendix, with its original id. This heading survives only so that citations
of this doc's ledger keep resolving until they are repointed; it goes with the rest of this file.
