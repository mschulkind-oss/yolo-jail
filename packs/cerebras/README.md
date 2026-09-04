# `cerebras` — the official pack that ships Cerebras as a provider

The second purely-declarative pack after `zai` ([design](../../docs/design/cerebras-pack-and-copilot-delivery.md)):
Cerebras's service facts as one `kind: "provider"` contribution, plus the `kind:
"profile"` selection over them. The pack installs **no CLI**. Cerebras serves one wire
protocol — OpenAI chat completions at `https://api.cerebras.ai/v1` — and one key from
[cloud.cerebras.ai](https://cloud.cerebras.ai) serves every agent it reaches.

**What it ships, and what it deliberately does not:** facts and a pointer. The endpoint,
the wire protocol, the model alias and *which variable holds the key* are the pack's;
the key itself is unrepresentable in a manifest — `api_key_env_name` carries a NAME,
never a value. The one alias is `qwen-3.8-27b` (public since 2026-09-03; agentic-coding
tuned, parallel tool calls + strict schemas, 64K context free / 128K paid, ~1500 tok/s).
`gpt-oss-120b` is deliberately absent: Cerebras's own docs warn it "may invoke tools it
wasn't given" and its live capability flag has `parallel_tool_calls: false` — a model
that invents tool calls has no tier in an agent, not even the fast one. Want it anyway?
`providers` is a merged-scope key: add `"cerebras": {"models": {"oss": "gpt-oss-120b"}}`
in user config and it lands in every catalog beside the pack's alias.

Free-tier note: 5 req/min / 30K tok/min / 1M tokens-day is thin for an agent loop; the
Developer tier lifts it to 300 req/min / 150K tok/min for this model.

## The user's entire setup

```jsonc
// ~/.config/yolo-jail/config.jsonc
{
  "packs": ["pi", "cerebras"]         // the provider pack beside the agents you use
}
```

```bash
# ~/.config/yolo-jail/env (untracked, 0600) — or the invoking environment:
CEREBRAS_API_KEY=csk-…                # the ONLY secret, spelled once
```

Then `yolo -p cerebras` (or the persistent spelling, `"use_profiles": {"pi": "cerebras"}`).

## What lands where

| Agent | What it gets | Channel |
|---|---|---|
| pi | a `cerebras` catalog entry — `api: "openai-completions"`, `apiKey: "${CEREBRAS_API_KEY}"` (the reference, not the value) — plus `defaultProvider`/`defaultModel` when a profile is selected | its derive, reading `YOLO_PROVIDERS` |
| opencode | a `cerebras` catalog entry — `baseURL` and `apiKey: "{env:CEREBRAS_API_KEY}"` under `options` — plus `model = "cerebras/qwen-3.8-27b"` when a profile is selected. Cerebras's own integrations index lists OpenCode as a supported client. | its derive, reading `YOLO_PROVIDERS` |
| copilot | BYOK env: `COPILOT_PROVIDER_BASE_URL`, `COPILOT_PROVIDER_TYPE=openai`, `COPILOT_PROVIDER_WIRE_API=completions`, `COPILOT_MODEL=qwen-3.8-27b`, `COPILOT_PROVIDER_API_KEY` | the copilot pack's env derive |
| claude | **nothing — and no envy owed** | Cerebras has no Anthropic-compatible endpoint; `ANTHROPIC_BASE_URL` has nothing to point at. Routing claude at Cerebras means a translating proxy, which is a running service rather than a provider fact (design doc OQ-1) |
| codex | **nothing — no entry and no selection** | codex speaks `responses` only and Cerebras serves chat completions only — the same unwireable pairing as codex+zai ([providers.md](../../docs/reference/providers.md) §"canonical wire_api"); the derive emits no entry rather than one that fails at first request |
| agy | **nothing, ever** | Google-locked: its only custom-endpoint hook speaks the Gemini protocol (design doc's audit table) |

A launch that selects this pack and never hydrates `CEREBRAS_API_KEY` **refuses
outright** (the credential preflight follows catalog membership, OQ-PT4).
`YOLO_ALLOW_MISSING_PROVIDERS=1` continues loudly instead.

## Selection

The catalog is presence, not choice: `-p cerebras` puts it in pi's and opencode's
catalogs and composes copilot's BYOK block. Select it with `-p cerebras` before the
`--`, `-p pi=cerebras` for one agent, or persistently as
`"use_profiles": {"pi": "cerebras"}`. The provider declares one option, `model`, whose
default is the alias named `default`; a user profile stating `"model": "oss"` selects
whatever alias you merged in yourself.

## Verifying

```console
$ yolo pack lint packs/cerebras     # claims + the strict manifest read
$ yolo pack footprint cerebras      # both claims: the provider (one endpoint, the key
                                    # by name) and the selection over it
$ CEREBRAS_API_KEY=x yolo -p cerebras -- pi --version
```
