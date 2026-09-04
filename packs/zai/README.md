# `zai` — the official pack that ships z.ai as a provider

The first real consumer of the provider/profile pair
([`zai-plumbing.md`](../../docs/design/zai-plumbing.md) §4 route B): z.ai's service facts as
one `kind: "provider"` contribution, and the selection that points the agents at them as one
`kind: "profile"`. The pack installs **no CLI**. The GLM Coding Plan speaks both wire
protocols, so one provider serves an anthropic-speaking agent and chat-completions ones at
once — and one API key from the z.ai console serves every agent it reaches.

Which agents that is is narrower than the protocol list suggests *(corrected 2026-09-02: this
paragraph said "every agent you already have can use it except codex", and only the delivery
below supports that)*. Delivery is per agent pack, and three of the six ship one: **claude**,
through the claude pack's own env derive; **pi** and **opencode**, a catalog entry plus each
one's own selection key. **codex** ships a delivery too and is excluded anyway — the pairing is
unwireable (see the table below). **copilot** and **agy** ship no provider delivery at all, so
there is nothing for this pack to hand them.

**What it ships, and what it deliberately does not:** facts and a pointer. The endpoints, the
wire protocols, the model aliases and *which variable holds the key* are the pack's; the key
itself is unrepresentable in a manifest — `api_key_env_name` carries a NAME, never a value.

## The user's entire setup

```jsonc
// ~/.config/yolo-jail/config.jsonc
{
  "packs": ["claude", "zai"]           // the provider pack beside the agents you use
}
```

```bash
# ~/.config/yolo-jail/env (untracked, 0600) — or the invoking environment:
ZAI_API_KEY=<key>                      # the ONLY secret, spelled once
```

Then `yolo -p zai` (or the persistent spelling, `"use_profiles": {"claude": "zai"}`).

## What lands where

| Agent | What it gets | Channel |
|---|---|---|
| claude | the full env block [Z.AI's Claude Code guide recommends](https://docs.z.ai/devpack/tool/claude): `ANTHROPIC_BASE_URL=https://api.z.ai/api/anthropic`, `ANTHROPIC_AUTH_TOKEN`, all three `ANTHROPIC_DEFAULT_{OPUS,SONNET,HAIKU}_MODEL` (the alias the profile's `model` option names for opus — `glm-5.3[1m]`, `glm-5.3-flash[1m]` for `"model": "fast"` — `sonnet` → `glm-5.3[1m]`, `haiku` → `glm-5.3-flash[1m]`; measured 2026-09-04: with sonnet unset, z.ai serves claude's sonnet-tier names as the FAST model), plus `CLAUDE_CODE_AUTO_COMPACT_WINDOW=1000000`, `API_TIMEOUT_MS=3000000`, and `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` — the last three composed from the provider's `context_window`/`api_timeout_ms` options and the routed launch itself, never hand-copied. | the agent pack's own env derive (`yolo.env` in `packs/claude/derive.lua`), composed at launch — the endpoint is configuration, the token is relayed from the hydrated variable, and nothing is written to a file |
| pi | a `zai` catalog entry pointing at the openai route — `api: "openai-completions"`, `apiKey: "${ZAI_API_KEY}"` (the reference, not the value: pi expands it at read time) — plus `defaultProvider`/`defaultModel` in `settings.json` when a profile is selected | its derive, reading `YOLO_PROVIDERS` (the composed table) |
| opencode | a `zai` catalog entry pointing at the openai route — `baseURL` and `apiKey: "{env:ZAI_API_KEY}"`, both under `options` because opencode reads them nowhere else — plus `model = "zai/<id>"` when a profile is selected | its derive, reading `YOLO_PROVIDERS` (the composed table) |
| codex | **nothing — no entry and no selection** | codex speaks `responses` only and z.ai's openai route speaks chat completions only, so no `wire_api` value makes the pairing work — the derive emits no entry rather than one that 404s at first request ([providers.md](../../docs/reference/providers.md) §3.3), and the same reachability gate keeps the selection off with it. Codex has no z.ai route; the anthropic endpoint is claude's, via claude's env derive. |

A launch that selects this pack and never hydrates `ZAI_API_KEY` **refuses outright**
(`yolo run`'s pre-flight, OQ-13) — the alternative is a jail whose first API call fails
somewhere unattributable. `YOLO_ALLOW_MISSING_PROVIDERS=1` continues loudly instead.

## Selection

The catalog is presence, not choice: `-p zai` puts `zai` in pi's and opencode's catalogs
(codex gets none — the pairing is unwireable, see the table above). The pack ALSO ships the
selection that points them at it: one `kind: "profile"` contribution, `{"name": "zai",
"provider": "zai"}` — a named selection over the provider, which is the whole kind
([providers.md](../../docs/reference/providers.md)
§5.2). The selection is not a second channel: the derive that wrote the catalog writes the
selection key too, per agent, in that agent's own dialect. This pack ships no per-agent
config-overlay — the one that carried claude's endpoint was deleted with `env_shape`
(2026-09-02, [`zai-plumbing.md`](../../docs/design/zai-plumbing.md) §4).

Selecting it — `-p zai` before the `--`, or `-p pi=zai` for one agent, or the
persistent `"use_profiles": {"pi": "zai"}` — is what makes each agent's derive write its own
selection key (pi's `defaultProvider`/`defaultModel`, opencode's `model`) from the provider's
`default` alias, or from whichever alias a profile's `model` option names: the provider
declares a `model` option whose declared default is the alias named `default` (the
provider's full option surface is `model`, plus `context_window`/`api_timeout_ms`, whose
values the claude derive translates into Claude Code's auto-compact window and API
ceiling) and ships `fast`/`haiku` → `glm-5.3-flash[1m]` beside
`default`/`sonnet` → `glm-5.3[1m]`, so a profile stating `"model": "fast"` selects the air
model.

Your own profile states the option rather than overriding the pack's:

```jsonc
// ~/.config/yolo-jail/config.jsonc (user scope — the key is refused at workspace scope)
{
  "profiles": {"zai-fast": {"provider": "zai", "model": "fast"}},
  "use_profiles": {"pi": "zai-fast", "opencode": "zai-fast"}
}
```

A profile that selects nothing does nothing, and deactivating one does not write the
agent's surface back — whatever a selection wrote stays until the agent's own choice
replaces it. Claude is selected by the same spellings (`-p zai`, `-p
claude=zai`, `"use_profiles": {"claude": "zai"}`) and reads none of those keys — it has no
catalog and no selection key, and the env derive in the table above is its whole delivery —
but it is not outside the option: its `ANTHROPIC_DEFAULT_OPUS_MODEL` is the same alias
lookup the other two make.

## Verifying

```console
$ yolo pack lint packs/zai          # claims + the strict manifest read
$ yolo pack footprint zai           # both claims: the provider (two endpoints, the key by
                                    # name) and the selection over it
$ ZAI_API_KEY=x yolo -p zai         # pi and opencode answer to zai; claude gets the pair
$ yolo -p zai -- claude --version   # the per-invocation spelling, keyed on the CLI
```

The last two refuse with `ZAI_API_KEY is not set in this launch's environment` when the key
is missing — measured in a nested jail with a fresh image, refusal and composition both.
