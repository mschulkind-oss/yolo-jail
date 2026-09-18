---
title: "Darwin image provenance — folded into the image-delivery reference"
date: 2026-09-18
status: accepted
tags: [design, ci, macos, image, provenance, graduated]
summary: "A stub. The identity invariant, the stock tag, the two safety rulings and every OQ-IP id now live in docs/reference/image-staging-vs-baking.md, which already owned the image-identity subsystem. This filename survives only while inbound references are repointed."
---

# Darwin image provenance — folded into the image-delivery reference

**Status:** GRADUATED, 2026-09-18 — folded into
[`../reference/image-staging-vs-baking.md`](../reference/image-staging-vs-baking.md) rather than
moved to a reference of its own, which is the disposition
[`doc-triage.md`](../plans/doc-triage.md#what-did-not-move-and-why) had already ruled. Nothing is
owed here.

> [!IMPORTANT]
> **The design is BUILT, and this file is no longer where it is described.** Minting a second
> reference for the image identity was the failure to avoid: that subsystem already had an
> authority, and everything durable here is now inside it —
> [the invariants](../reference/image-staging-vs-baking.md#invariants) carry the cross-system
> requirement and the placement rule that enforces it,
> [a failed build is fatal](../reference/image-staging-vs-baking.md#a-failed-build-is-fatal)
> carries both safety rulings and the trap about the stale-image hatch in CI,
> [the stock tag](../reference/image-staging-vs-baking.md#the-stock-tag-and-the-question-asked-before-the-build)
> carries the build-or-not decision and the pre-cutover diagnostic, and
> [`## Why it's this way`](../reference/image-staging-vs-baking.md#why-its-this-way) is where
> [`OQ-IP1`](../reference/image-staging-vs-baking.md#why-its-this-way) through
> [`OQ-IP4`](../reference/image-staging-vs-baking.md#why-its-this-way) resolve.
>
> **One of this doc's findings is closed rather than folded.** Its separate finding — that no CI
> job exercised the `macos-user` backend at all, so a green nightly would move none of those
> designs' unmeasured claims — was true when written and is not now: `.github/workflows/macos-user.yml`
> is a nightly for exactly that backend, deliberately image-free and in a workflow of its own so
> its verdict is visible. The two macOS instruments still cover different backends, and a green
> one still says nothing about the other.

**Why it still exists.** Inbound references that could not be repointed in the same commit, all in
files this workflow does not own: prose citations of this path in `flake.nix`,
`internal/image/autoload.go`, `internal/image/stockimage.go`, `integration/imageskew_test.go`,
`integration/imagelabel_test.go`, `integration/harness_test.go` and
`.github/workflows/macos-user.yml`. Point each of them at
[`../reference/image-staging-vs-baking.md`](../reference/image-staging-vs-baking.md) — the `OQ-IP`
citations at its
[`#why-its-this-way`](../reference/image-staging-vs-baking.md#why-its-this-way) anchor — and
**delete this file**.

## Decision ledger

**Moved.** Every `OQ-IP` ruling now lives in
[`../reference/image-staging-vs-baking.md`](../reference/image-staging-vs-baking.md#why-its-this-way)'s
*Why it's this way* appendix, with its original id. This heading survives only so the ledger
citations named above keep resolving until they are repointed; it goes with the rest of this file.
