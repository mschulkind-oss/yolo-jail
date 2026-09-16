---
title: "Tavily delivery from a Kilo profile"
date: 2026-09-16
status: draft
tags: [kilo, tavily, mcp, profiles, capabilities, research]
summary: "Why the current capability labels cannot activate Tavily for a Kilo profile, and the two additions required to deliver it to Pi."
vantage:
  status-chip: true
---

# Tavily delivery from a Kilo profile

**Status:** source-verified 2026-09-16 against the checked-out yolo tree and
the personal configuration. This records an implementation boundary, not a
Tavily integration: the desired target agent remains a user decision.

**Profile-gated MCP delivery** *(coined here)* means giving an MCP server to a
specific agent only while that agent has a specified provider profile. It is
not a capability requirement, which states that a job must be available but
does not say how to configure a provider.

## Finding

There is no configuration-only spelling for “include Tavily when Kilo is the
profile.” The current personal configuration selects Kilo for Pi, but Pi has
no native MCP integration in yolo. Adding a Tavily entry to `mcp_servers`
would therefore deliver it to every selected agent that supports MCP — Claude,
Codex, OpenCode, Copilot, and agy — and not to Pi.

The configured Kilo profile is a valid place to express the intent. It is not
yet a condition that the MCP table understands.

## What the existing mechanisms do

| Mechanism | What it does now | Why it does not deliver Tavily from Kilo |
| :--- | :--- | :--- |
| `mcp_servers.<name>.provides: "web_search"` | Rejects two servers claiming the same job; Claude and agy omit it when their native search is active. | It describes a server's job after it is configured. It never causes a server to be configured. |
| `requires_env: ["TAVILY_API_KEY"]` | Omits a configured server when the key is absent. | It is an environment gate, not a profile gate. |
| `required_capabilities: ["web_search"]` | Validates and crosses the host–jail boundary. | Nothing consumes it yet; the planned fatal refusal is tracked in [OQ-CAP2](../design/agent-auth-modes.md#12-decision-ledger). It neither chooses nor configures Tavily. |
| Pack `serves` / `supersedes` capabilities | Retires a loophole whose work a selected pack makes unnecessary. | This is a loophole-lifecycle mechanism, not an agent-tool selection mechanism. |
| `profile:` on `env` and `config-overlay` contributions | Gates an ordinary pack contribution by an active profile. | It cannot gate a user-owned `mcp_servers` table entry, and Pi has no MCP surface to receive one. |

## The necessary work

The smallest coherent implementation is two parts, landed together.

1. Add a validated per-agent profile condition to `mcp_servers`, for example:

   ```jsonc
   "tavily": {
     "command": "npx",
     "args": ["-y", "tavily-mcp"],
     "env": {"TAVILY_API_KEY": "${TAVILY_API_KEY}"},
     "requires_env": ["TAVILY_API_KEY"],
     "provides": "web_search",
     "when_profiles": {"pi": "kilo"}
   }
   ```

   Core should filter the MCP source separately for each rendered agent
   surface, using that agent's active profile. It must strip `when_profiles`
   before the server reaches the client. Filtering the shared table once would
   leak Tavily into unrelated agents in the same jail.

2. Add a Pi MCP integration. The repository documents that Pi has no native
   MCP support, and its pack currently renders only the model catalog. A Pi
   extension or other supported adapter must be investigated and then made a
   normal Pi surface before Pi can receive the filtered Tavily declaration.

The tests must prove the production render path: Pi receives Tavily only when
its Kilo profile is active; another agent in the same jail does not; an absent
`TAVILY_API_KEY` still removes it; and an unrelated MCP server remains
unchanged. A test that exercises only the filter function would not pin the
surface-render call site.

## Rejected shortcuts

- **Put Tavily in personal `mcp_servers` without a profile condition.** This
  makes it available to every MCP-capable selected agent, regardless of the
  selected provider, and still does not reach Pi.
- **Use `required_capabilities`.** Its intended enforcement is a refusal when
  a job is missing; it is not a provisioning directive and is unimplemented.
- **Teach the Kilo provider to name Tavily.** A provider is a model endpoint
  and credential fact. Making it own an unrelated web-search server couples
  two independently chosen services and prevents a user from selecting Kilo
  without Tavily.

## Open question

1. 💬 **OQ-KT1: Which agent should receive Tavily when Kilo is selected?**

   <!-- vantage: oq id=OQ-KT1 leaning="Scope Tavily to the agent whose Kilo profile is active; a provider choice for Pi must not alter the tools of Claude, Codex, OpenCode, Copilot, or agy in the same jail." -->

   _Leaning:_ Scope it to the agent whose Kilo profile is active. That preserves
   profile selection as an agent-local choice and avoids cross-agent tool leaks.

   **Answer:**
   > _(empty — fill in when decided)_

## Sources and re-check points

- [The provider reference](../reference/providers.md#profiles-and-options) — a
  profile selects a provider and profile-gated contributions are restricted to
  `env` and `config-overlay`.
- [The MCP configuration reference](../reference/mcp-configuration.md) — the
  shared MCP table is projected by each agent pack into its own dialect.
- [The capability design](../design/agent-auth-modes.md#6-capability-resolution--selective-tool-augmentation-the-web-search-pattern)
  — the original web-search intent and the still-unbuilt capability refusal.
- [The project README](../../README.md#agents) — the current MCP agent
  support matrix, including Pi's absence.
