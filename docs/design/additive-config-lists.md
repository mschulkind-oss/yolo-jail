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

**Needs your ruling:** [OQ-AL1](#OQ-AL1), [OQ-AL2](#OQ-AL2).

**Reads with:** [`pack-system.md`](../reference/pack-system.md) (current pack/overlay semantics), [`config-ownership-and-promotion.md`](./config-ownership-and-promotion.md) (configuration layer ownership).

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

1. **Ordering.** Fold ordinary defaults, host settings, workspace settings and pack overlays as today. Apply all list contributions in loaded-pack and declaration order before captured in-jail edits, computed settings and the managed floor. The final list retains existing entries in order, then first occurrences of contributed entries in contribution order. An empty `add` is a no-op.
2. **Duplicates.** Compare whole JSON values for equality; the first occurrence wins. Do not parse package specifications, normalize Git URLs, or sort the output. Equal entries contributed twice produce one entry, not an error.
3. **Type safety.** A missing path acts as an empty array. If the path or an intermediate parent exists with a non-array/non-object value, refuse that surface's render and name the surface, path and contributing pack; do not overwrite the conflicting value or silently skip it. A malformed declaration fails pack validation before launch.
4. **Precedence.** Captured edits, computed output and managed values retain their existing ability to replace or delete the *whole* array. This addition is not a mandatory package policy or a way to defeat higher layers. Ordinary `config-overlay` arrays continue to replace; a pack wanting replacement uses that existing operation.
5. **Visibility.** `config render --explain` and `config diff` must distinguish an array replaced by an overlay from an array assembled from existing values and named pack contributions. A top-level `packages: config-overlay:personal` label alone is misleading when several contributors survive. Show the ordered contributors, including an indication when a higher layer replaces their result.
6. **Removal.** Dropping a contributing pack removes its added entries on the next clean render, but must not delete an identical value independently retained in the host, workspace, capture or another pack's input. Host apply's read-modify-write path must use its ownership records rather than infer ownership from value equality; if it cannot prove safe removal, it must report an unresolved retained entry, never silently delete user data.

> [!WARNING]
> A merge patch with `"packages": null` removes the whole key; it is not a request to remove one package. This proposal must not redefine null or make all JSON arrays additive.

## Costs and alternatives

| Alternative | Disposition |
| :--- | :--- |
| Put the package in `packs/kilo` as an ordinary overlay | Rejected: still copies or replaces the whole list. |
| Append every array automatically | Rejected: breaks existing replacement semantics, including deliberate empty arrays. |
| Generate the personal overlay's full array from Matt's list | Useful interim workaround, but retains a copied/generated list and its update dependency. |
| Add an explicit list contribution | Proposed: compositional and visible without changing merge-patch behavior elsewhere. |

The main cost is that rendering currently tracks provenance by top-level key and host apply also supports read-modify-write rather than just clean composition. Both need a trustworthy account of individual contributed entries. This is a behavior design, not an assumption that the existing provenance map already provides one.

## What done looks like

With Matt's Pi package list and the personal contribution selected, Pi receives both lists in stable order; without the personal contribution it receives Matt's original list. Selecting the same package twice writes it once. The rendered explanation identifies both sources. A captured or computed replacement still wins, and host apply never removes a user's independently declared matching package when a pack is dropped. The same outcome is checked at the jail and host rendering boundaries.

## Open Questions

1. 💬 **OQ-AL1: Should list contributions be allowed on host-applied surfaces immediately?** The read-modify-write host path can retain data after a pack disappears. A jail-only first release avoids claiming reversible host ownership that has not been proved, but makes a pack's declaration behave differently at the two rendering boundaries.

   <!-- vantage: oq id=OQ-AL1 leaning="Support both host and jail only once host removal can distinguish pack-owned entries from identical user-owned entries; otherwise refuse host apply rather than silently retain or delete." -->

   _Leaning:_ Support both only when host removal can distinguish pack-owned entries from identical user-owned entries; otherwise refuse host apply rather than silently retain or delete.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-AL2: Can a later list contribution re-add an entry removed by an earlier overlay?** Applying additions after all ordinary overlays is simple, but it means an overlay cannot express a durable per-entry veto; allowing such a veto would need another operation rather than merge-patch null.

   <!-- vantage: oq id=OQ-AL2 leaning="Yes; later additive contributions add entries after ordinary overlays, while only higher capture, computed and managed layers can replace the final array." -->

   _Leaning:_ Yes; additions follow ordinary overlays, while higher capture, computed and managed layers can still replace the final array.

   **Answer:**
   > _(empty — fill in when decided)_
