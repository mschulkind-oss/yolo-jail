---
title: "Model allowlist: implementation plan (seed)"
date: 2026-11-06
status: draft
stage: SKETCH
next: "Wait for the design's open questions; then a planner completes this against the tree"
depends-on: [model-allowlist.md#OQ-MA1, model-allowlist.md#OQ-MA2, model-allowlist.md#OQ-MA3]
tags: [models, allowlist, bedrock, aws-auth, plan]
summary: "Seed of the implementation plan for the model allowlist: notes the design surfaced, parked here for the planner. Not a hand-off."
---

# Model allowlist: implementation plan (seed)

**Status:** 2026-11-06. Seeded during design; not a hand-off, and incomplete until the design is
accepted. Nothing here may be built from while this file is a seed.

The design is [`model-allowlist.md`](model-allowlist.md), and it wins on behavior wherever the two
disagree.

## Checks for the planner

Each is a note from the design work, not a survey of the tree:

- **The size budget.** The design's figures (285 characters for an empty set; about twelve
  geographic or nine global entries) were computed from minified JSON with the statements of
  [§4.3](model-allowlist.md#43-the-statement). Re-derive them in the composer's table tests rather
  than trusting the prose.
- **The wildcard region.** Whether `arn:aws:bedrock:*::foundation-model/<id>` matches the
  region-less global ARN is unmeasured; the composer writes the region-less ARN until the live
  measurement says otherwise ([MA-D5](model-allowlist.md#MA-D5)).
- **`bedrock-mantle` resources.** AWS does not document their ARN form; the design denies the
  whole prefix ([MA-D6](model-allowlist.md#MA-D6)). Check whether that changes before building.
- **The per-launch input producer.** Check how `mcp-auth`'s producer is registered under the
  closed producer set before adding the `served_by`-keyed one
  ([§5.2](model-allowlist.md#52-how-the-set-reaches-the-daemon)).
- **The briefing line's list of ids** waits on the computed-briefing producer of
  [`computed-briefings.md`](computed-briefings.md); check whether it has been built.
- **A changed list mid-session.** The design applies a change at the next launch
  ([MA-D12](model-allowlist.md#MA-D12)); check whether [`live-config-apply.md`](live-config-apply.md)
  has since given launch inputs a reload path.
- **Quoting a model id as a config key.** Ids contain dots (`us.openai.gpt-6.1-sol`); check how
  `yolo config add` and `yolo config set` handle a value or key segment with dots before writing
  the user-facing examples for [OQ-MA1](model-allowlist.md#OQ-MA1)'s ruling.
- **The cache key.** The shared credential cache is keyed by profile alone today; check every
  reader of it when adding the digest ([MA-D9](model-allowlist.md#MA-D9)).

Blocked on [OQ-MA1](model-allowlist.md#OQ-MA1), [OQ-MA2](model-allowlist.md#OQ-MA2) and
[OQ-MA3](model-allowlist.md#OQ-MA3): the user key, the `unnarrowed` behavior and the background
traffic rule each shape a producer or refusal path above.
