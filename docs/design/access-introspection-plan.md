---
title: "Access introspection: implementation plan (seed)"
date: 2026-11-04
status: draft
stage: SKETCH
depends-on:
  - access-introspection.md#OQ-AX1
  - access-introspection.md#OQ-AX2
tags: [plan, seed, broker, dry-run, introspection]
summary: "Seed of the implementation plan for access-introspection.md: notes the design work surfaced. Not a hand-off; the design wins on behavior."
---

# Access introspection: implementation plan (seed)

**Status:** 2026-11-04 — seeded during design; not a hand-off, and incomplete until the design is
accepted.

This seeds the plan for [`access-introspection.md`](access-introspection.md). Where the two
disagree on behavior, the design wins. Do not build from this file while it is a sketch.

## Notes the design surfaced

- **Check that git's own push dry run works over the broker's remote helper.** The helper in
  `internal/gitremote` speaks `connect` only; confirm `git push --dry-run` there reads the remote's
  advertisement and sends no commands, before relying on it to compute ref updates
  ([design §5.3](access-introspection.md#53-what-evaluate-does-at-each-kind-of-call)).
- **The mode field needs frame-protocol versioning.** An older broker must answer an evaluate or
  readout request with a clear "unsupported" rather than treat it as an act; check how the
  GitHub and git frames are versioned today before adding the field.
- **The GitHub decision split.** `Authority.Classify`, `coveredWrite` and `readIssue` interleave
  host reads with the decision; the split moves those reads into the fact-read step. Check the
  existing decision tests can run unchanged in both modes.
- **The access snapshot** is written in the same launch step as the grant file, from the same
  in-memory result; find that step and the live-apply path of `yolo config apply`.
- **Verb name** is blocked on [OQ-AX2](access-introspection.md#OQ-AX2); `--ask` is blocked on
  [OQ-AX1](access-introspection.md#OQ-AX1).
