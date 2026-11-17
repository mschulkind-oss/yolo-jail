---
title: "Scoped broker policy — companion seed"
status: draft
stage: SKETCH
next: "Finish and read the full grounded plan, including V1–V5 and the published scope-matching/timed-cap closure, before this seed becomes a hand-off"
depends-on:
  - scoped-broker-policy.md
  - ../research/scoped-broker-policy-grounding.md
tags: [plan, brokers, policy, seed]
---

# Scoped broker policy — companion seed

**Status:** 2026-11-17 — seeded during design; incomplete and not a build hand-off.
**Design:** [`scoped-broker-policy.md`](scoped-broker-policy.md). The design wins on behavior; the tree wins on fact; this seed is not implementation authority.

Nothing is built from this seed. Fable 5.2's closure, corrective overlay and source-scope/native-cap closure are published in the accepted design; no owner question or engineering behavior choice remains. The full grounded plan is in flight, incorporating the same closure and its V5/tests. It has not been fully read or published here. A grounded planner must complete the file map, reuse survey, build order and implementation tasks, and the full output must be read, before this seed can become a build hand-off.

## Material surfaced during design

- [OQ-SBP1](scoped-broker-policy.md#OQ-SBP1) is ruled B: credential denial leaves public Git fetch/import anonymous and never blocks it. It authorizes no anonymous write, other source's anonymous path or arbitrary unclassified command.
- The design's [evidence closure and five facts to confirm](scoped-broker-policy.md#13-unverified-claims-this-design-relies-on) belong in the plan's first task. V1–V5 each have a fixed fallback; none reopens policy. Permanent synthetic reproductions precede behavior fixes.
- [Source-owned matching](scoped-broker-policy.md#38-scope-matching), [native timed caps](scoped-broker-policy.md#51-timed-caps) and [H1–H25](scoped-broker-policy.md#128-scope-matching-and-timed-caps) close the remaining engineering seams. Matching, route selection and reports stay pure and never admit; native reads and unresolved candidates use the existing common 15-minute default, while known writes keep their shipped maximum. New declaration metadata is TOML-only.
- Preserve the [compatibility contract](scoped-broker-policy.md#10-compatibility), including historical broad grants and approval digests, legacy on-switch records and the explicitly listed timing and defect corrections. The classifier and launch-input version mechanics await the grounded planner.
- [Config spelling](scoped-broker-policy.md#36-spelling), [hold and execution budgets](scoped-broker-policy.md#61-budgets), [fact-read hard-deny handling](scoped-broker-policy.md#41-fact-reads), [declaration-owned contexts](scoped-broker-policy.md#71-model), and [live snapshots and declaration-only gate behavior](scoped-broker-policy.md#8-authority-state-concurrency-withdrawal-failure) are settled author contracts, not planner choices. Gate changes are unbuilt; pack read/exec trust disclosures remain unsuppressible and there is no quiet mode.

No completed file map, reuse survey, build order or implementation tasks are published in this seed. Do not build from it or treat the unfinished planner artifact as a hand-off.
