---
title: "Let packs contribute list entries without replacing the owner's list"
date: 2026-09-23
status: accepted
tags: [packs, config, design]
summary: "A narrow additive contribution for configuration arrays, without changing JSON Merge Patch."
vantage:
  status-chip: true
---

# Let packs contribute list entries without replacing the owner's list

**Status:** DECIDED, 2026-09-23. Nothing built; every question is ruled ([Decision Ledger](#decision-ledger)).

> **In short.** An explicitly additive contribution can let a pack add a Pi package without copying every package another pack selected. Ordinary configuration overlays must keep their existing array-replacement semantics.

**Why it matters.** A personal overlay currently has to copy Matt's entire Pi package list just to add `git:github.com/mschulkind/kilo-pi-provider`; that copy can silently drift.

**The shape.** A list contribution is a separate operation on one declared configuration path, applied by the configuration composer after ordinary pack overlays.

**Cost.** A second, deliberately narrow composition operation and an explanation format that can name multiple contributors to one key.

**Start at [The proposed contract](#the-proposed-contract)** — it distinguishes appending entries from replacing an array.

**Needs your ruling:** None.

**Reads with:** [`pack-system.md`](../reference/pack-system.md) (current pack/overlay semantics), [`config-ownership-and-promotion.md`](./config-ownership-and-promotion.md) (configuration layer ownership), [`manifest.go`](../../internal/agentcfg/manifest/manifest.go) (the three surface modes: `computed`, `stateful`, `rmw`).

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
6. **Removal, and the read-back problem.** How hard removal is depends on the target surface's MODE ([`manifest.go`](../../internal/agentcfg/manifest/manifest.go)), not on host versus jail:
   - **`computed`** (regenerated every boot, in-jail edits discarded — pi's `models`/`mcp`, copilot's `lsp`/`mcp`, omp's `models`, agy's `mcp`): trivial. The array is a pure function of its inputs; dropping a pack removes its entries on the next render.
   - **`stateful`** (the default — composed, then the agent's and user's own edits to the file are READ BACK and captured as a layer: pi's `settings`, which is the kilo case's target, claude's `settings`, codex's and opencode's config, agy's settings): hard. Captured edits are stored as a durable merge patch whose arrays REPLACE whole ([`engine.go` `mergeAccumulate`](../../internal/agentcfg/engine.go)). So the first time the agent or user touches the array — `pi install` appending to `packages` — the capture holds the ENTIRE array, pack-added entries included, and it outranks every contribution: later additions are masked, and a dropped pack's entries can never be removed.
   - **`rmw`** (yolo edits an agent-owned file in place — `~/.claude.json`, copilot's config): the same problem, with yolo's rendered baseline as the only record of what it added.

   **Ruled ([OQ-AL1](#decision-ledger)): capture per entry at every list path.** At any array path a list contribution targets, capture records per-entry additions and removals relative to the last render, never the whole array. Contributed entries therefore never enter the capture; a dropped pack's entries vanish on the next compose; the agent's own edits (`pi install`) are kept as the user's; and a user deleting a contributed entry is a per-entry removal the capture holds. `rmw` surfaces do the same against their rendered baseline. **Until a surface's path captures per entry, a list contribution targeting it is refused at launch**, naming the surface and its mode — never composed into a whole-array capture that would freeze it.
   - Rejected: `computed` surfaces only (does not reach pi's `settings`, so the kilo case stays unsolved); making the path `computed` (wipes every `pi install`); an owner opt-in per path (any pack's `config-overlay` can already replace any key, so it would protect nothing).

> [!WARNING]
> A merge patch with `"packages": null` removes the whole key; it is not a request to remove one package. This proposal must not redefine null or make all JSON arrays additive.

## Costs and alternatives

| Alternative | Disposition |
| :--- | :--- |
| Put the package in `packs/kilo` as an ordinary overlay | Rejected: still copies or replaces the whole list. |
| Append every array automatically | Rejected: breaks existing replacement semantics, including deliberate empty arrays. |
| Generate the personal overlay's full array from Matt's list | Useful interim workaround, but retains a copied/generated list and its update dependency. |
| Add an explicit list contribution | Proposed: compositional and visible without changing merge-patch behavior elsewhere. |

The main cost is that capture and provenance both work per top-level key, while this operation is per ENTRY: explanation needs an account of individual contributed entries, and on `stateful` and `rmw` surfaces capture does too ([rule 6](#the-proposed-contract)). This is a behavior design, not an assumption that the existing provenance map already provides one.

## What done looks like

With Matt's Pi package list and the personal contribution selected, Pi receives both lists in stable order; without the personal contribution it receives Matt's original list. Selecting the same package twice writes it once. The rendered explanation identifies both sources. A captured or computed replacement still wins, and host apply never removes a user's independently declared matching package when a pack is dropped. The same outcome is checked at the jail and host rendering boundaries.

## Open Questions

None. Both are ruled; see the [Decision Ledger](#decision-ledger).

## Decision Ledger

| ID | Ruling | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-AL1 | List contributions survive the read-back of `stateful` and `rmw` surfaces by per-entry capture at every list path, relative to the last render; a list contribution on a path that does not yet capture per entry is refused at launch | 2026-09-23 | [Rule 6](#the-proposed-contract) | — |
| OQ-AL2 | A later list contribution may re-add an entry an earlier overlay's replacement dropped: additions follow ordinary overlays, and only the higher capture, computed and managed layers replace the final array | 2026-09-23 | [Rule 1, Ordering](#the-proposed-contract) | — |
