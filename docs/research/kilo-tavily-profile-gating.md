---
title: "Capability-driven MCP delivery from authentication sources"
date: 2026-09-16
status: draft
tags: [authentication, tavily, mcp, providers, capabilities, research]
summary: "The selected authentication source, rather than an agent, must decide whether an MCP capability is redundant."
vantage:
  status-chip: true
---

# Capability-driven MCP delivery from authentication sources

**Status:** source-verified 2026-09-16 against the checked-out yolo tree.
This records a settled product rule and the missing implementation connection;
it does not add Tavily or change a user's configuration.

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

## Settled rule

For each agent render target, resolve its active authentication source, then
compare that source's declared capabilities with each configured MCP server:

1. A server with no `provides` declaration remains eligible.
2. A server with `provides: "web_search"` is omitted when the active source
   declares `web_search`; otherwise it is eligible.
3. The existing `requires_env` check runs on eligible servers, so Tavily still
   disappears when `TAVILY_API_KEY` is absent.

This is evaluated separately for every rendered target. The decision follows
the selected source, not the agent name and not the jail-wide pack set.

The rule supplied on 2026-09-16 is that the Codex, Z.AI, Claude, and agy
authentication sources declare `web_search`; Kilo does not. Therefore a
personal Tavily server declaring `provides: "web_search"` is automatically
delivered while Kilo is active and automatically suppressed for those native
sources. The same resolver must work for any future named job, not just web
search.

## Gap in the current implementation

`providers.<name>.capabilities` already accepts a string list, but only
validation reads it. Built-in authentication modes have no equivalent source
declaration. Meanwhile Claude and agy each contain an agent-specific
`web_search` suppression branch in their derives; other MCP derives pass the
table through. The source fact is thus present in part, but no generic resolver
uses it before the per-agent MCP surfaces render.

The work is to make authentication-source capability declarations available to
the common MCP input and apply the rule above there. The representation must
cover both provider-backed profiles and built-in sources; individual MCP
servers should not carry source-selection metadata. Existing
`required_capabilities` remains a separate future enforcement path.

## Acceptance boundary

Tests must exercise the production render path, not only a filter helper:

- a Tavily server with `provides: "web_search"` is delivered for Kilo and
  removed for each native-search source;
- changing an agent's profile changes only that target's MCP result;
- an absent `TAVILY_API_KEY` removes Tavily after capability eligibility; and
- a server with another capability, or no `provides`, remains unaffected.

## Sources and re-check points

- [The provider reference](../reference/providers.md#profiles-and-options) —
  profiles select provider facts for an individual CLI.
- [The MCP configuration reference](../reference/mcp-configuration.md) — the
  shared MCP table is projected by each agent pack into its own dialect.
- [The capability design](../design/agent-auth-modes.md#6-capability-resolution--selective-tool-augmentation-the-web-search-pattern)
  — `required_capabilities` is a requirement, not provision selection.
- [`internal/config/validate.go`](../../internal/config/validate.go) —
  `providers.<name>.capabilities` is validated but not consumed.
