---
title: "Plan: OpenRouter and Kilo provider packs"
date: 2026-09-15
status: accepted
tags: [plan, providers, packs, openrouter, kilo]
summary: "Implementation hand-off for the settled gateway-provider pack design."
vantage:
  status-chip: true
---

# Plan: OpenRouter and Kilo provider packs

**Status:** BUILT 2026-09-15 (`f7b14308`). MEASURED: the two manifests, the codex credential-field
fix and the per-agent projections are pinned by `internal/entrypoint/providerderive_test.go`; no run
against a live OpenRouter or Kilo endpoint is recorded. Written against `a97ee688`, 2026-09-15.

**Design:** [`gateway-provider-packs.md`](gateway-provider-packs.md) — which is not yet a graduation
candidate; its status line says why.

Precedence: the design wins on behavior, the tree wins on fact, this file is
advice and is the first thing to be wrong.

## Map

| Path | Change |
| :--- | :--- |
| `packs/openrouter/pack.json` | New provider and profile contributions. |
| `packs/kilo/pack.json` | New provider and profile contributions, plus a conditional dependency on the existing wire bridge. |
| `packs/codex/derive.lua` | Correct custom-provider credential field from `api_key_env` to `env_key`. |
| `internal/entrypoint/providerderive_test.go` | Pin each real pack's catalog and selection projection across supported agents. |
| `docs/reference/providers.md` | Add packs, capabilities, and the user-curated map example. |
| `docs/guides/USER_GUIDE.md` | Add a runnable user-config example. |

## Reuse

- Mirror the CLI-less declaration shape in `packs/zai/pack.json`; provider facts
  live in the pack, while models are omitted.
- Reuse the real embedded pack harness in
  `internal/entrypoint/providerderive_test.go`; it catches a manifest that
  validates but is not consumed by the derives.
- Reuse `models` plus the `model` profile option; `ComposeProviders` already
  merges user aliases over a pack's provider (`internal/packload/providers.go`).

## Traps

- **Constraint:** Codex only emits providers with `openai-responses`; an
  OpenAI-compatible Chat Completions URL is not enough.
- **Constraint:** `ModelProviderInfo` reads `env_key`, not `api_key_env`; fix
  this existing typo in the same change or OpenRouter's Codex entry is
  credential-less.
- **Constraint:** no model ids in either manifest. A model selection without a
  user alias must remain absent.
- **Constraint:** Kilo's local Anthropic URL must be unique and `wire-bridge`
  must join when Claude or Copilot is selected; that existing service is the
  translator, not a new proxy.
- **Advice:** test the Kilo Codex omission explicitly; a generic positive test
  would not catch an undocumented Responses route being advertised.

## Build order

1. Add failing real-pack projection tests for OpenRouter and Kilo, including
   Kilo's Codex omission and bridge route. → `go test ./internal/entrypoint ./internal/wirebridged`
2. Add manifests and correct Codex's credential field. → `go test ./internal/entrypoint ./internal/packload`
3. Document the config and provider matrix. → `uvx vantage-check docs/design/gateway-provider-packs.md docs/research/gateway-providers.md docs/reference/providers.md docs/guides/USER_GUIDE.md`
4. Run `just test-fast`; then `just build-go` and the required nested-jail
   verification from a throwaway workspace.

## Ships with

- Unit: each pack's exact endpoints, credential variable, profile option, empty
  shipped models, catalog projection, default selection, Kilo's Codex omission,
  and its bridge route.
- Regression: Codex's generated TOML contains `env_key` and never
  `api_key_env`.
- Docs: provider reference and user guide example, plus the research and design
  records beside this plan.
- Cheap and yours: test fixture names and alias strings; no behavior depends on
  them.

## Don't

- Do not add a model-list fetch or ship a changing provider catalog.
- Do not emit Kilo as a Responses provider without a separately documented and
  measured contract.
