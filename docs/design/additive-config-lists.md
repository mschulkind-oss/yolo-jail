---
title: "Let packs contribute list entries without replacing the owner's list"
date: 2026-09-23
status: in-review
tags: [packs, config, design]
summary: "A narrow additive contribution for configuration arrays, without changing JSON Merge Patch."
vantage:
  status-chip: true
---

# Let packs contribute list entries without replacing the owner's list

**Status:** DESIGN, 2026-09-23. Nothing built.

> **In short.** An explicitly additive contribution can let a pack add a Pi package without copying every package another pack selected. Ordinary configuration overlays must keep their existing array-replacement semantics.

**Why it matters.** A personal overlay currently has to copy Matt's entire Pi package list just to add `git:github.com/mschulkind/kilo-pi-provider`; that copy can silently drift.

**The shape.** A list contribution is a separate operation on one declared configuration path, applied by the configuration composer after ordinary pack overlays.

**Cost.** A second, deliberately narrow composition operation and an explanation format that can name multiple contributors to one key.

**Start at [The proposed contract](#the-proposed-contract)** — it distinguishes appending entries from replacing an array.

**Needs your ruling:** [OQ-AL1](#OQ-AL1).

**Reads with:** [`pack-system.md`](../reference/pack-system.md) (current pack/overlay semantics), [`config-ownership-and-promotion.md`](./config-ownership-and-promotion.md) (configuration layer ownership; its [§4.5](./config-ownership-and-promotion.md#45-retiring-assert--the-two-value-key) retires `assert`, the one read-modify-write host mode).

---

## Diagnosis and boundary

Checked against [`packoverlay.go`](../../internal/packoverlay/packoverlay.go), [`compose.go`](../../internal/agentcfg/compose.go), and [`engine.go`](../../internal/agentcfg/engine.go) on 2026-09-23: cross-pack overlays fold in pack order, and arrays replace rather than merge under [RFC 7386 JSON Merge Patch](https://www.rfc-editor.org/rfc/rfc7386). The Pi settings surface belongs to the [`pi` pack](../../packs/pi/pack.json) and reads host settings; its `packages` array is not an independently owned surface. Moving the kilo extension declaration to another pack changes the author, not the replacement behavior.

The personal pack itself is not a bug: a personal-only package still needs a personal selection or declaration. The bug is requiring that declaration to carry a copy of someone else's entire list. Eliminating the *pack* as the place for a personal choice would require a separate user-level declaration channel and is outside this design.

## The proposed contract

A **list contribution** *(coined here)* is a pack's request to add JSON values at one array-valued path of an existing configuration surface. It is not a JSON Merge Patch and cannot create a surface. The target is identified by its owning surface and a path within that surface, rather than by a file path chosen by the contributing pack.

For example, the declarative intent is:

```json
{
  "kind": "config-list",
  "surface": "pi/settings",
  "path": "packages",
  "add": ["git:github.com/mschulkind/kilo-pi-provider"]
}
```

The spelling is illustrative; the behavior below is the contract. A single entry does not have to reproduce the owner's list. The `pi` pack must be selected; otherwise the contribution is inert and reported just like an ownerless `config-overlay`. The operation is independent of Pi itself: core knows configuration paths and arrays, not Pi package syntax or installation rules.

1. **Ordering.** Fold ordinary defaults, host settings, workspace settings and pack overlays as today. Apply all list contributions in loaded-pack and declaration order before captured in-jail edits, computed settings and the managed floor. The final list retains existing entries in order, then first occurrences of contributed entries in contribution order. An empty `add` is a no-op. Additions apply AFTER every ordinary overlay, so a later list contribution may re-add an entry an earlier overlay's replacement dropped; an overlay cannot express a per-entry veto, and only the higher capture, computed and managed layers can replace the final array ([OQ-AL2](#decision-ledger)).
2. **Duplicates.** Compare whole JSON values for equality; the first occurrence wins. Do not parse package specifications, normalize Git URLs, or sort the output. Equal entries contributed twice produce one entry, not an error.
3. **Type safety.** A missing path acts as an empty array. If the path or an intermediate parent exists with a non-array/non-object value, refuse that surface's render and name the surface, path and contributing pack; do not overwrite the conflicting value or silently skip it. A malformed declaration fails pack validation before launch.
4. **Precedence.** Captured edits, computed output and managed values retain their existing ability to replace or delete the *whole* array. This addition is not a mandatory package policy or a way to defeat higher layers. Ordinary `config-overlay` arrays continue to replace; a pack wanting replacement uses that existing operation.
5. **Visibility.** `config render --explain` and `config diff` must distinguish an array replaced by an overlay from an array assembled from existing values and named pack contributions. A top-level `packages: config-overlay:personal` label alone is misleading when several contributors survive. Show the ordered contributors, including an indication when a higher layer replaces their result.
6. **Removal is recomposition, never inference.** A composed surface is a function of its declared inputs, so dropping a contributing pack removes its entries on the next render with no ownership bookkeeping, and a value the user independently holds (a captured edit, a host or workspace input, another pack's contribution) survives because it is still an input. **List contributions are refused on any surface rendered read-modify-write** — a file yolo edits without wholly composing it — because there the only way to remove an entry is to guess its owner from its value. Today that is `host_management: "assert"` alone, which is retired by ruling ([config-ownership §4.5](./config-ownership-and-promotion.md#45-retiring-assert--the-two-value-key)); until that retirement is built, an `assert` host apply refuses a surface that carries a list contribution, naming it.

> [!WARNING]
> A merge patch with `"packages": null` removes the whole key; it is not a request to remove one package. This proposal must not redefine null or make all JSON arrays additive.

## Costs and alternatives

| Alternative | Disposition |
| :--- | :--- |
| Put the package in `packs/kilo` as an ordinary overlay | Rejected: still copies or replaces the whole list. |
| Append every array automatically | Rejected: breaks existing replacement semantics, including deliberate empty arrays. |
| Generate the personal overlay's full array from Matt's list | Useful interim workaround, but retains a copied/generated list and its update dependency. |
| Add an explicit list contribution | Proposed: compositional and visible without changing merge-patch behavior elsewhere. |

The main cost is that rendering currently tracks provenance by top-level key, so [explanation](#the-proposed-contract) needs an account of individual contributed entries. Removal needs none, because every surface a list contribution may touch is composed rather than edited in place. This is a behavior design, not an assumption that the existing provenance map already provides one.

## What done looks like

With Matt's Pi package list and the personal contribution selected, Pi receives both lists in stable order; without the personal contribution it receives Matt's original list. Selecting the same package twice writes it once. The rendered explanation identifies both sources. A captured or computed replacement still wins, and host apply never removes a user's independently declared matching package when a pack is dropped. The same outcome is checked at the jail and host rendering boundaries.

## Open Questions

1. 💬 <a id="OQ-AL1"></a>**[OQ-AL1](#OQ-AL1): may a pack ADD entries to an array on a surface another pack owns — and only at paths that owner declares open?** The axis is not host versus jail; it is **clean composition versus read-modify-write**. In the jail, and at the host under `own`, yolo composes the whole surface from declared inputs every render, so an addition is just one more input and removing it is a recompose ([rule 6](#the-proposed-contract)). The only surface where an addition is genuinely "into something we don't own" is a read-modify-write file, and that is `assert` — retired by ruling. What remains is an **authority** question: should any selected pack be able to extend any array another pack's surface holds? Stakes: without a limit, a content pack could append to a security-relevant array (a permission allow-list, a trusted-folder list, an MCP server set) as easily as to pi's `packages`.

   <!-- vantage: oq id=OQ-AL1 leaning="Yes, with two limits: only at array paths the OWNING surface declares additive (opt-in, so a content pack cannot append to a permission allow-list), and only on composed surfaces (refused on read-modify-write, which retiring assert removes)." -->

   _Leaning:_ **yes, with two limits.** (1) **Owner opt-in:** the pack that owns the surface declares which array paths accept additions (`packs/pi` would declare `packages`); a contribution to any undeclared path is refused at pack validation, naming the path and the owner. That is what makes "adding to something we don't own" acceptable: the owner granted it, and nothing security-relevant is open unless its owner says so. (2) **Composed surfaces only**, per rule 6. Refusing adds altogether leaves the kilo case where it is — a personal pack copying pi's whole list and drifting — which is the defect this doc exists for.

   **Answer:**
   > _(empty — fill in when decided)_

## Decision Ledger

| ID | Ruling | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-AL2 | A later list contribution may re-add an entry an earlier overlay's replacement dropped: additions follow ordinary overlays, and only the higher capture, computed and managed layers replace the final array | 2026-09-23 | [Rule 1, Ordering](#the-proposed-contract) | — |
