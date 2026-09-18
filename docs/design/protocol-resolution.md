---
title: "Who says which wire — graduated to the protocol-resolution reference"
date: 2026-09-18
status: accepted
tags: [design, providers, packs, wire-bridge, protocols, resolution, graduated]
summary: "A stub. All seven build steps shipped and the as-built system is described in docs/reference/protocol-resolution.md, with the P1-P6 principles, the OQ-PR ids and both unmet done-conditions carried across. This filename survives only while inbound references — most of them in Go and Lua comments, which no markdown tool can see — are repointed."
vantage:
  status-chip: true
---

# Who says which wire — graduated to the protocol-resolution reference

**Status:** GRADUATED, 2026-09-18 — the system is described in
[`../reference/protocol-resolution.md`](../reference/protocol-resolution.md), verified against
`7da1c628`. Nothing is owed here.

> [!IMPORTANT]
> **The design is BUILT, and this file is no longer where it is described.** Everything durable
> moved: the principles keep their `P1`–`P6` ids in
> [Principles](../reference/protocol-resolution.md#principles), the one-writer and sole-ownership
> rules are [Invariants](../reference/protocol-resolution.md#invariants), and the three
> declarations, the four outcomes and their refusals are the body.
>
> **Two claims this file made are corrected there rather than carried.** The resolver does NOT
> compute outcome 2 — an adapter's address is injected at provider composition, below the user
> layer, so by the time the gate asks, an adapted pairing is indistinguishable from a native one.
> And an adaptation is NOT disclosed at the launch: the `adapter` kind is classified
> `disclosureSkip` beside `provider` and `service`, because it declares an address rather than a
> read of this machine. Both are stated where they happen, in
> [The four outcomes](../reference/protocol-resolution.md#the-four-outcomes).

## Where each section number now resolves

**A section number does not survive a graduation**, so every numbered section this file carried is
a named anchor in the reference. The citations that name one are almost all Go and Lua comments,
which no markdown checker reads, and they spell it with a section sign — these are the tokens to
grep for:

```text
§3   §4.1   §4.2   §4.3   §4.4   §4.5   §5   §6   §7   §10   §11   §12
```

| Cited here as | Now |
| :--- | :--- |
| section 3 (one resolution) | [The three declarations](../reference/protocol-resolution.md#the-three-declarations) and [The four outcomes](../reference/protocol-resolution.md#the-four-outcomes) |
| section 4.1 (degenerate inputs) | [Degenerate inputs, and what each resolves to](../reference/protocol-resolution.md#degenerate-inputs-and-what-each-resolves-to) |
| sections 4.2 and 4.3 (failure paths, ordering) | [Degenerate inputs](../reference/protocol-resolution.md#degenerate-inputs-and-what-each-resolves-to) and [Where the gate lives](../reference/protocol-resolution.md#where-the-gate-lives-and-why-not-in-the-run-pre-flight) |
| section 4.4 (forbidden) | [Invariants](../reference/protocol-resolution.md#invariants) and [What this does not license](../reference/protocol-resolution.md#what-this-does-not-license) |
| section 4.5 (what done looks like) | [What the done-conditions could not be](../reference/protocol-resolution.md#what-the-done-conditions-could-not-be) |
| section 5 (the shorthand) | [The single-protocol `base_url` shorthand is removed](../reference/protocol-resolution.md#the-single-protocol-base_url-shorthand-is-removed) |
| section 6 (the adapter owns its address) | [The adapter's address](../reference/protocol-resolution.md#the-adapters-address) |
| section 7 (what this does not propose) | [What this does not license](../reference/protocol-resolution.md#what-this-does-not-license) |
| sections 10 and 11 (build order, open questions) | gone — the work is built and every question is ruled |
| section 12 (decision ledger) | [Why it's this way](../reference/protocol-resolution.md#why-its-this-way) |

## Decision ledger

**Moved.** [`OQ-PR1`](../reference/protocol-resolution.md#oq-pr1),
[`OQ-PR2`](../reference/protocol-resolution.md#oq-pr2) and
[`OQ-PR3`](../reference/protocol-resolution.md#oq-pr3) keep their ids in the reference's
[*Why it's this way*](../reference/protocol-resolution.md#why-its-this-way) appendix, beside the
four unnumbered rulings worth keeping. All three are cited from Go comments in
`internal/packload` and `internal/packdecl`, so that appendix is the only place they resolve once
this file goes.

**Why it still exists.** Inbound references only, and most of them are source comments this
workflow does not own — `internal/packdecl`, `internal/packload`, `internal/config`,
`internal/wirebridged`, `internal/cli/check`, six `packs/*/derive.lua` files and two pack READMEs
each name this path, usually with a `§N`. Point each at
[`../reference/protocol-resolution.md`](../reference/protocol-resolution.md) using the table
above, then **delete this file**.
