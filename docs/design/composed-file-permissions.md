---
title: "Composed-file postures — the three questions left open"
date: 2026-07-25
status: in-review
tags: [config, prism, permissions, open-questions]
summary: "A stub. The taxonomy, the postures, the 0o444 finding and the writer-class split graduated to docs/reference/composed-file-permissions.md; what stays here is the three open questions (CFP-1…CFP-3), which are consequence questions about shipped mechanisms rather than gaps."
---

# Composed-file postures — the three questions left open

**Status:** the settled body of this doc **graduated 2026-09-09** to
[`../reference/composed-file-permissions.md`](../reference/composed-file-permissions.md) — the
Derived/Shared/State taxonomy, the host-linked axis, the `0o444` asymmetry finding, the model,
the `host_files` mode collapse, the home-root symlink decision, the program-operation /
directed-agent split, and the restart axis all live there and describe shipped behavior.

**This file is what remains: three open questions.** They are deliberately *not* roadmap rows —
each concerns a shipped mechanism working as designed rather than a gap, and none blocks
anything. Read the reference first; each question below assumes it.

**Related decisions tracked elsewhere:** the `0o444`-vs-`:ro` question for `host_files` is
`E1`/`E2` in [`../plans/BACKLOG.md`](../plans/BACKLOG.md) and `OQ-B` in
[`../plans/pack-host-management-plan.md`](../plans/pack-host-management-plan.md). **CFP-1 is
deliberately not a fourth ID for it** — it is the narrower consequence question that only arises
*after* `E2` is answered yes.

---

## Open Questions

IDs (`CFP-*`) minted 2026-08-23.

1. 💬 **CFP-1: Does `:ro` for a Derived surface need host-side composition — and is that
   per-surface or blanket?** Yes to the first half, and it is the cost nobody has priced: you
   cannot compose into a `:ro` mount, so a `:ro` posture gives up `managed`/`defaults`
   and the overlay for that surface and moves its rendering to the host CLI. **This is the
   question `E2` (`readonly` as a real `:ro` mount) turns into the moment it is answered yes**, so
   it decides how expensive `E2` actually is.

   _Leaning:_ per-surface, not blanket. For a pure-overwrite computed surface the trade is clean —
   there is nothing layered to lose. ~~For anything carrying a Lua transform it is not, and a
   blanket rule would silently downgrade those.~~

   **2026-09-12: the second half of that leaning has lost its referent.** The Lua transform is
   removed ([`lua-transform-removal.md`](lua-transform-removal.md)), so no surface carries one and
   the per-surface-vs-blanket call now turns on `managed`/`defaults` and the overlay alone.
   Whoever answers CFP-1 owns that re-weighing; this note does not make it.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **CFP-2: Should `macos-user` and Apple Container get a documented degradation table?** Both
   lose `:ro` silently — Apple Container ignores it outright and `macos-user` has no bind mounts
   at all, so every `:ro` surface degrades to a writable materialized copy. This decides whether
   "read-only" means anything on two of the three backends.

   _Leaning:_ yes, per-surface, but only when `macos-user` is next worked on — writing it earlier
   means maintaining a table against a backend nobody is touching. Note it has grown a neighbour:
   `render.FieldSet` / `HostUnimplemented` is now the mechanism for "this notch cannot honor that,
   and says so by name", so the table may want to be *code* rather than a doc.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **CFP-3: Is per-workspace the right scope for the capture overlay sidecars?** They live
   under `<workspace>/.yolo/prism/`, so the same host file composed in two workspaces can diverge
   invisibly in different directions. Unexamined rather than obviously wrong. It is the same scope
   question a sidecar-relocation decision would be blocked on: is a captured edit per-workspace or
   per-machine?

   _Leaning:_ per-workspace is right for the jail, and the answer is already settled for the
   *host* notch in the opposite direction — `render.Target.ProvenanceDir` puts host records under
   the user's own state directory, user-scoped, with the reasoning in its doc comment. So the
   shape of the answer already exists; what is open is whether the *jail* overlay should follow
   it.

   **Answer:**
   > _(empty — fill in when decided)_
