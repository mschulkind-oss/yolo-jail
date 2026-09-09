# Host↔jail state separation — the one open question

**Status:** the design SHIPPED (2026-07-03) and its as-built account is
[`../reference/jail-state-separation-design.md`](../reference/jail-state-separation-design.md) —
read that for the split mise store, the neutral `/mise` path, per-side venv shadows, the
jail↔jail store residue and its gated prune, the migration, and the `SS-1`…`SS-5` rulings.

This file exists only to hold the one question that is still open, so that a reference doc does not
carry a live `💬`. Nothing else belongs here.

## Open Questions

1. <a id="ss-6"></a>💬 **SS-6: File the upstream mise issue for the project-agnostic store key.**
   The root defect is that mise's rust backend writes a *project-configured* value
   (`$CARGO_HOME/bin`) into a store entry keyed only by `(tool, version)`. The three layers that
   shipped — no-op for the common case, a host-gated prune, and a `mise.jail.toml` escape hatch —
   **heal** the symptom; nothing yet **fixes** it, so every future backend that records a
   side-specific path reintroduces the class. This decides whether yolo carries the gated-prune
   machinery indefinitely or eventually deletes it.

   _Leaning:_ File it, expect nothing, keep the prune. *"Worth filing; not a fix to wait on"* was
   the original leaning and nothing has changed it — but the filing itself is still unrecorded,
   which is the only reason this is open rather than settled. Whether an issue exists is not
   verifiable from inside this repo.

   **Answer:**
   > _(empty — fill in when decided)_

Dispositioned on the roadmap as one of the *deliberately not* rows: it concerns a shipped mechanism
working as designed, not a gap, and it blocks nothing.
