---
title: "Capability-driven MCP delivery from authentication sources"
date: 2026-09-16
status: accepted
tags: [authentication, tavily, mcp, providers, capabilities, research]
summary: "The selected authentication source, rather than an agent, decides whether an MCP capability is redundant — one resolver at the ctx boundary, replacing two per-agent branches."
vantage:
  status-chip: true
---

# Capability-driven MCP delivery from authentication sources

**Status:** BUILT, 2026-09-17 — UNMEASURED: no launch with a `provides` server configured is
recorded; the [acceptance boundary](#acceptance-boundary) is held by unit tests that drive the
boot loop, not by an observed run. The rule below shipped that day (`8e324800`), with the
`provides`-leak fix on 2026-09-18 (`67cf4c81`); re-verified against the tree
2026-09-22. This page is the vocabulary's home — three Go sites cite it for the
term *authentication source* — and it records the rule and where the tree
enforces it. It still adds no Tavily server and changes nobody's configuration:
the mechanism ships, the server is the user's to configure.

An **authentication source** *(coined here)* is the credential and endpoint
mode an agent uses for a launch. It may be a selected provider profile, such
as Kilo or Z.AI, or a built-in first-party mode, such as Claude subscription
or agy's Google-authenticated mode. It is not the agent: one agent may use
different sources in different profiles.

**Capability-driven MCP delivery** *(coined here)* is the per-render rule that
omits an MCP server when the active authentication source already performs the
job that server declares with `provides`. It is distinct from
`required_capabilities`, which is an assertion that a job must exist, not an
instruction to choose an implementation.

## The rule

For each agent render target, resolve its active authentication source, then
compare that source's declared capabilities with each configured MCP server:

1. A server with no `provides` declaration remains eligible.
2. A server with `provides: "web_search"` is omitted when the active source
   declares `web_search`; otherwise it is eligible.
3. `requires_env` is an independent gate and still applies, so Tavily
   disappears when `TAVILY_API_KEY` is absent whatever the source declares. The
   two filters sit at different layers on purpose — `requires_env` gates
   DELIVERY upstream of the derive (`LoadMCPServers`), `provides` feeds this
   rule at the derive's own boundary — and a server has to pass both.

This is evaluated separately for every rendered target. The decision follows
the selected source, not the agent name and not the jail-wide pack set.

The declaration is a pack manifest's, so the current membership is a grep away
(`rg -n web_search packs/*/pack.json`) rather than a list to trust here. Today
four shipped packs declare `web_search` — `claude` and `agy` for their agents'
built-in logins, `zai` and `openai-auth` for the providers they ship — and
`kilo` declares none. So a personal Tavily server declaring
`provides: "web_search"` is delivered while Kilo is active and suppressed for
those native sources, and the same resolver answers any future named job with
no edit to it and none to a pack.

## Where the tree enforces it

Two functions in [`internal/agentcfg/luahook/derive.go`](../../internal/agentcfg/luahook/derive.go),
both at the **ctx boundary** — the one place both production callers pass
through ([`packsurfaces.go`](../../internal/entrypoint/packsurfaces.go)'s surface
render and [`packload`](../../internal/packload/deriveenv.go)'s env composition),
so no agent opts in and none can opt out:

- `sourceCapabilities` resolves the active source's set. A selected provider's
  capabilities are a field of its row in the composed providers table; an
  agent's built-in login has no row, which is what
  `packdecl.Manifest.NativeCapabilities` (a `capabilities` list on the pack's
  `kind: "program"` contribution) exists to carry. A selected provider the
  composed table has no row for resolves to the EMPTY set, never to the
  built-in one — an absent row means the launcher composed nothing for that
  name, not that the agent fell back to its own login.
- `eligibleMCPServers` drops only the exact-name match: no `provides` passes, a
  capability the source does not declare passes. That is what makes it generic.

The per-agent `provides == "web_search"` branches this replaced were the opt-in
shape, and one had drifted — claude's suppressed web search for every profile
that was not bedrock or codex, so a Kilo launch lost its search MCP because of
the agent it happened to be running under.

`provides` is **yolo's own vocabulary and stops at the ctx boundary**:
`withoutProvidesKey` strips it from the survivors unconditionally, because four
packs copy an MCP entry verbatim into the agent's own config file. It runs
*after* the filter and outside it — a strip in entrypoint's `LoadMCPServers`,
the obvious structural precedent, would delete the input the rule reads and turn
the whole feature into a silent no-op with every test green.

`required_capabilities` remains a separate enforcement path, built the same
week, and the distinction is sharp rather than blurred: it refuses a launch
whose declared requirement nothing satisfies, while the rule here chooses an
implementation among things that already work. The shared vocabulary is real,
not coincidental — both read an `mcp_servers.<name>.provides` and a
`providers.<name>.capabilities`, so a change to either declaration moves both.

## Acceptance boundary

[`internal/entrypoint/capabilitymcp_test.go`](../../internal/entrypoint/capabilitymcp_test.go)
covers these, and every cell drives the boot loop (`ConfigurePackSurfaces`) over
the packs yolo ships and the wire tables the launcher composes — nothing there
calls the filter, the resolver or a derive directly, because the thing being
replaced was a branch in two `derive.lua` scripts and a helper-level test would
pass with the feature disconnected from every render:

- a Tavily server with `provides: "web_search"` is delivered for Kilo and
  removed for each native-search source;
- changing an agent's profile changes only that target's MCP result;
- an absent `TAVILY_API_KEY` removes Tavily even from a source that was eligible
  to keep it — capability eligibility does not stand in for the credential; and
- a server with another capability, or no `provides`, remains unaffected.

## Sources and re-check points

- [The provider reference](../reference/providers.md#profiles-and-options) —
  profiles select provider facts for an individual CLI.
- [The MCP configuration reference](../reference/mcp-configuration.md) — the
  shared MCP table is projected by each agent pack into its own dialect.
- [The capability design](../design/agent-auth-modes.md#6-capability-resolution--selective-tool-augmentation-the-web-search-pattern)
  — `required_capabilities` is a requirement, not provision selection.
- [`internal/config/validate.go`](../../internal/config/validate.go) — the shape
  of both declarations, and the refusal that keeps capability resolution
  unambiguous: two MCP servers may not declare the same `provides`.
