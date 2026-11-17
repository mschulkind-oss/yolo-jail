---
title: "Scoped broker policy — companion seed"
status: draft
stage: SKETCH
next: "Wait for design acceptance and engineering closure; then a planner reads the current tree and completes this seed"
depends-on:
  - scoped-broker-policy.md#OQ-SBP1
  - ../research/scoped-broker-policy-grounding.md
tags: [plan, brokers, policy, seed]
---

# Scoped broker policy — companion seed

**Status:** 2026-11-17 — seeded during design; incomplete and not a build hand-off.
**Design:** [`scoped-broker-policy.md`](scoped-broker-policy.md). The design wins on behavior; the tree wins on fact; this seed is not implementation authority.

Nothing is built from this seed. A planner may complete it only after the design is accepted with no open question and its engineering dependencies closed.

## Material surfaced during design

- Anonymous Git fetch behavior is blocked on [OQ-SBP1](scoped-broker-policy.md#OQ-SBP1); do not assume the recommendation is a ruling.
- The design's [unverified claims](scoped-broker-policy.md#13-unverified-claims-this-design-relies-on) require bounded source tracing and synthetic reproductions before they become build preconditions. Permanent reproductions precede behavior fixes.
- Preserve the [compatibility contract](scoped-broker-policy.md#10-compatibility), including historical broad grants and approval digests. The classifier and launch-input version notes surfaced here; their exact mechanics await the planner.
- Config spelling, CLI verbs and the bounded generic in-call hold remain planner work under the design's behavioral constraints; this seed chooses none of them.

No file map, reuse survey, build order or implementation tasks have been prepared.
