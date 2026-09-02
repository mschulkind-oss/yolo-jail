# `zai` — the official pack that ships z.ai as a provider

The first real consumer of the provider/profile pair
([`zai-plumbing.md`](../../docs/design/zai-plumbing.md) §4 route B): z.ai's service facts as
one `kind: "provider"` contribution, and the selection that points the agents at them as one
`kind: "profile"`. The pack installs **no CLI** — the GLM Coding Plan speaks both wire
protocols, so every agent you already have can use it except codex (see the table below),
and one API key from the z.ai console serves them all.

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
| claude | `ANTHROPIC_BASE_URL=https://api.z.ai/api/anthropic` + `ANTHROPIC_AUTH_TOKEN` | the agent pack's own env derive (`yolo.env` in `packs/claude/derive.lua`), composed at launch — the endpoint is configuration, the token is relayed from the hydrated variable and never written to a file |
| pi | a `zai` entry pointing at the openai route, `api: "openai-completions"`; plus `defaultProvider`/`defaultModel` when a profile is selected | its derive, reading `YOLO_PROVIDERS` (the composed table) |
| opencode | a `zai` entry pointing at the openai route; plus `model = "zai/<id>"` when a profile is selected | its derive, reading `YOLO_PROVIDERS` (the composed table) |
| codex | **nothing** | codex speaks `responses` only and z.ai's openai route speaks chat completions only, so no `wire_api` value makes the pairing work — the derive emits no entry rather than one that 404s at first request ([provider-table-fidelity.md](../../docs/design/provider-table-fidelity.md) §3.3). Codex has no z.ai route; the anthropic endpoint is claude's, via claude's env derive. |

A launch that selects this pack and never hydrates `ZAI_API_KEY` **refuses outright**
(`yolo run`'s pre-flight, OQ-13) — the alternative is a jail whose first API call fails
somewhere unattributable. `YOLO_ALLOW_MISSING_PROVIDERS=1` continues loudly instead.

## Selection

The catalog is presence, not choice: `-p zai` puts `zai` in pi's and opencode's catalogs
(codex gets none — the pairing is unwireable, see the table above). The pack ALSO ships the
selection that points them at it: one `kind: "profile"` contribution, `{"name": "zai",
"provider": "zai"}` — a named selection over the provider, which is the whole kind
([provider-catalog-and-selection.md](../../docs/design/provider-catalog-and-selection.md)
§5.2). Selecting it — `yolo -p zai`, or `"use_profiles": {"pi": "zai"}` — is what makes each
agent's derive write its own selection key (pi's `defaultModel`, opencode's `model`) from the
provider's `default` alias, or from whichever alias a profile's `model` option names: the
provider declares `"options": {"model": "default"}` and ships `fast` → `glm-4.7-air` beside
`default` → `glm-4.7`, so a profile stating `"model": "fast"` selects the air model.

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
replaces it. Claude needs none of this: it has no catalog to choose from, and the env
derive above is its whole delivery.

## Verifying

```console
$ yolo pack lint packs/zai          # claims + the strict manifest read
$ yolo pack footprint zai           # the provider claim: two endpoints, the key by name
$ ZAI_API_KEY=x yolo -p zai         # pi and opencode answer to zai; claude gets the pair
$ yolo -p zai -- claude --version   # the per-invocation spelling, keyed on the CLI
```

The last two refuse with `ZAI_API_KEY is not set in this launch's environment` when the key
is missing — measured in a nested jail with a fresh image, refusal and composition both.
