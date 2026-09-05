# `cerebras` — the official pack that ships Cerebras as a provider

The second purely-declarative pack after `zai` ([design](../../docs/design/cerebras-pack-and-copilot-delivery.md)):
Cerebras's service facts as one `kind: "provider"` contribution, plus the `kind:
"profile"` selection over them — and, since the wire bridge shipped, a top-level
`needs` entry (the first in any shipped pack). The pack installs **no CLI**. Cerebras
serves one wire protocol — OpenAI chat completions at `https://api.cerebras.ai/v1` —
and one key from [cloud.cerebras.ai](https://cloud.cerebras.ai) serves every agent it
reaches.

**What it ships, and what it deliberately does not:** facts and a pointer. The endpoints,
the wire protocols, the model alias and *which variable holds the key* are the pack's;
the key itself is unrepresentable in a manifest — `api_key_env_name` carries a NAME,
never a value. Two endpoints: the native one Cerebras serves
(`https://api.cerebras.ai/v1`, OpenAI chat completions) and `http://127.0.0.1:8214` —
the wire bridge's loopback URL, declared as the `anthropic` endpoint and made true by
the `wire-bridge` pack this pack's `needs` entry joins whenever claude or copilot is in
the launch ([wire-bridge.md](../../docs/design/wire-bridge.md) §3). The one alias is
`qwen-3.8-27b` (public since 2026-09-03; agentic-coding tuned, parallel tool calls +
strict schemas, 64K context free / 128K paid, ~1500 tok/s). `gpt-oss-120b` is
deliberately absent: Cerebras's own docs warn it "may invoke tools it wasn't given" and
its live capability flag has `parallel_tool_calls: false` — a model that invents tool
calls has no tier in an agent, not even the fast one. Want it anyway? `providers` is a
merged-scope key: add `"cerebras": {"models": {"oss": "gpt-oss-120b"}}` in user config
and it lands in every catalog beside the pack's alias.

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

Put the key in the env file (an `env_sources` channel) when claude or copilot is in
the launch: the bridge reads its credential from the jail's 0600 `yolo-user-env.sh`
at boot, so a key that lives only in the invoking environment reaches the agents but
not the daemon — and a bridge that cannot authenticate refuses the launch at the
witness rather than serve unauthenticated upstream traffic.

Then `yolo -p cerebras` (or the persistent spelling, `"use_profiles": {"pi": "cerebras"}`).

## What lands where

| Agent | What it gets | Channel |
|---|---|---|
| pi | a `cerebras` catalog entry — `api: "openai-completions"`, `apiKey: "${CEREBRAS_API_KEY}"` (the reference, not the value) — plus `defaultProvider`/`defaultModel` when a profile is selected | its derive, reading `YOLO_PROVIDERS` (the `openai` endpoint) |
| opencode | a `cerebras` catalog entry — `baseURL` and `apiKey: "{env:CEREBRAS_API_KEY}"` under `options` — plus `model = "cerebras/qwen-3.8-27b"` when a profile is selected. Cerebras's own integrations index lists OpenCode as a supported client. | its derive, reading `YOLO_PROVIDERS` (the `openai` endpoint) |
| copilot | BYOK env routed through the wire bridge: `COPILOT_PROVIDER_BASE_URL=http://127.0.0.1:8214`, `COPILOT_PROVIDER_TYPE=anthropic`, `COPILOT_MODEL=qwen-3.8-27b`, `COPILOT_PROVIDER_API_KEY` — its derive prefers the anthropic endpoint of any provider declaring one (D-3), and the bridge speaks that wire | the copilot pack's env derive; the bridge from its `needs`-joined pack |
| claude | routed at the bridge: `ANTHROPIC_BASE_URL=http://127.0.0.1:8214`, the key as `ANTHROPIC_AUTH_TOKEN`, auto-compact sized to the 64K window — Cerebras has no native Anthropic-compatible endpoint; the bridge ([wire-bridge.md](../../docs/design/wire-bridge.md)) is the translating service that makes the declared URL true, joined automatically through this pack's `needs` entry | its derive, reading `YOLO_PROVIDERS`; the bridge from its `needs`-joined pack |
| codex | **nothing — no entry and no selection** | codex speaks `responses` only and the bridge translates exactly one pair, anthropic ↔ chat-completions — codex-on-cerebras stays unwireable ([wire-bridge.md](../../docs/design/wire-bridge.md) §7) |
| agy | **nothing, ever** | Google-locked: its only custom-endpoint hook speaks the Gemini protocol (design doc's audit table) |

A launch that selects this pack and never hydrates `CEREBRAS_API_KEY` **refuses
outright** (the credential preflight follows catalog membership, OQ-PT4).
`YOLO_ALLOW_MISSING_PROVIDERS=1` continues loudly instead.

## Selection

The catalog is presence, not choice: `-p cerebras` puts it in pi's and opencode's
catalogs, composes copilot's BYOK block, and routes claude at the bridge. Select it
with `-p cerebras` before the `--`, `-p pi=cerebras` for one agent, or persistently as
`"use_profiles": {"pi": "cerebras"}`. The provider declares two options, `model` (whose
default is the alias named `default`; a user profile stating `"model": "oss"` selects
whatever alias you merged in yourself) and `context_window: "65536"` — the free-tier
figure, so claude's auto-compact triggers at the real window (a paid-tier user
overrides it to `"131072"` in their own profile).

**The wire bridge joins by itself.** When a launch selects claude or copilot beside
this pack, the launcher adds `wire-bridge` automatically and prints the cause on the
banner — `+ wire-bridge (needed by cerebras: claude selected)` — and the same line
shows beside the pack list in `yolo check`. Nothing to configure; the launch that
composes the loopback URL is the launch that stages the listener answering on it.

## Verifying

```console
$ yolo pack lint packs/cerebras     # claims + the strict manifest read
$ yolo pack footprint cerebras      # the claims: the provider (two endpoints, the key
                                    # by name), the selection over it, and the need
$ CEREBRAS_API_KEY=x yolo -p cerebras -- pi --version
```
