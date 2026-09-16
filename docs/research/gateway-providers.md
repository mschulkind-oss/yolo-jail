---
title: "OpenRouter and Kilo are compatible gateways with different protocol surfaces"
date: 2026-09-15
status: accepted
tags: [providers, gateways, openrouter, kilo, agents]
summary: "Evidence for adding opt-in OpenRouter and Kilo provider packs without yolo owning either fast-moving model catalog."
vantage:
  status-chip: true
---

# OpenRouter and Kilo are compatible gateways with different protocol surfaces

**Status:** CURRENT — verified 2026-09-15.

Both services are multi-model gateways, but they are not interchangeable at the
wire boundary. The distinction decides which existing agent derives can use each
one; it does not justify a new yolo provider abstraction.

## Findings

| Gateway | Credential | OpenAI route | Anthropic route | Consequence |
| :--- | :--- | :--- | :--- | :--- |
| OpenRouter | `OPENROUTER_API_KEY` | `https://openrouter.ai/api/v1`; Chat Completions and Responses | `https://openrouter.ai/api`; Messages API | Every provider-capable pack can use it. |
| Kilo Gateway | `KILO_API_KEY` | `https://api.kilo.ai/api/gateway`; documented Chat Completions | No documented Messages route | Claude uses yolo's existing jail-local translator; Codex has no route. |

OpenRouter documents its [Codex configuration](https://openrouter.ai/docs/cookbook/coding-agents/codex-cli),
its [Responses endpoint](https://openrouter.ai/docs/api/api-reference/responses/create-responses),
and its [Claude Code Messages integration](https://openrouter.ai/docs/guides/coding-agents/claude-code-integration).
Kilo documents one [OpenAI-compatible gateway base URL](https://kilo.ai/docs/gateway),
the [Chat Completions API](https://kilo.ai/docs/gateway/api-reference), and a
[models endpoint](https://kilo.ai/docs/gateway/models-and-providers).

## Model catalogs stay user-owned

The gateways' model lists change far faster than yolo releases. A pack should
therefore ship its endpoint and credential-variable facts, but **no model ids**.
The user supplies a finite `providers.<gateway>.models` alias map. Existing
derives render precisely that set into Pi and OpenCode's model pickers; a
profile's `model` option selects one alias as an agent default. It is a list the
user chose, not a mirror of either gateway's whole catalog.

> [!WARNING]
> A Kilo model name is not proof that Kilo offers the protocol an agent needs.
> In particular, its documentation describes Chat Completions as the gateway's
> primary API. Kilo's own [open feature request](https://github.com/Kilo-Org/kilocode/issues/7397)
> says it exposes only that API and asks for Anthropic Messages compatibility.
> Do not give Kilo an `openai-responses` endpoint until Kilo documents and a
> Codex turn verifies that route.

## Verdict

Adopt two small, independent provider packs. OpenRouter uses its native
Anthropic route. Kilo declares the existing `wire-bridge` translator's
jail-local Anthropic endpoint and its Chat Completions upstream, so Claude Code
works without inventing another proxy. They reuse the catalog and profile system
described in [`providers.md`](../reference/providers.md); no new generic gateway
layer or automatic model discovery is warranted.
