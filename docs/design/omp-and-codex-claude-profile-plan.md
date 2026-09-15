---
title: "OMP and Codex-Claude profile — implementation sketch"
date: 2026-09-15
status: draft
tags: [implementation-sketch, omp, claude, codex, translation]
summary: "Parking lot for implementation evidence while the associated design remains open."
vantage:
  status-chip: true
---

# OMP and Codex-Claude profile — implementation sketch

**Status:** SKETCH, 2026-09-15 — incomplete but stable: every design ruling is
settled, and this is still not a build hand-off.

> **In short.** The feature has two shipping seams: an OMP program/config pack,
> and a new authenticated Codex Responses route in the existing in-jail wire
> bridge. Their shared dependency is an OpenAI broker view, not a shared
> credential file.

**Reads with:** [`omp-and-codex-claude-profile.md`](omp-and-codex-claude-profile.md)
(the design — it wins on behaviour; this is not a build hand-off),
[`../reference/wire-bridge.md`](../reference/wire-bridge.md) (current bridge
route/lifecycle), and [`openai-auth-broker-plan.md`](openai-auth-broker-plan.md)
(the broker's existing implementation plan).

---

## Evidence to re-check before a real plan

- Identify OMP's official package-manager distribution and exact config
  file/schema for the pinned selected version.
- Probe the current Codex Responses request and streaming-event surface with the
  broker's access-token view; record which Anthropic Messages fields can map
  without semantic loss, including thinking, caching, beta headers, and token
  counting where Codex Responses exposes equivalents.
- Locate the pack contribution and derive interfaces that add an OMP program,
  config surface, state root, and profile-aware provider selection without adding
  core knowledge of `omp`.
- Locate the `wire-bridge` route selector and the OpenAI broker client boundary;
  ensure the route receives a short-lived view rather than a secret-bearing
  config value.
- Establish the `terra` alias's provider mapping and its preflight/disclosure
  behavior.

## Expected verification seams

- Unit fixtures for Anthropic Messages ↔ Codex Responses conversion, including
  streamed tool calls and unsupported feature refusals.
- A call-site test proving that `claude=codex` starts the route and that a normal
  Claude launch cannot start or point at it.
- Broker concurrency test across Codex, OMP, and bridge clients: one canonical
  upstream refresh after expiry.
- Pack render test proving OMP gets only its own generated surfaces/state.
- A nested-jail smoke test for the new in-jail route, plus real-backend checks
  for any host-reachability or backend-specific auth path.
