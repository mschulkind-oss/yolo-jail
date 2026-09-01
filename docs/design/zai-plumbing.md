---
title: "Z.ai plumbing: one provider, every agent"
date: 2026-08-31
status: accepted
tags: [packs, providers, profiles, resolution, zai]
summary: "The first real consumer of profiles-as-pack-variants: what it takes to go from a z.ai key in an untracked file to `-p zai` firing every selected agent at GLM by whatever protocol it speaks. Maps both routes the maintainer named — name-the-protocol-and-fill-the-values (pure config, mostly shipped), and a layered zai pack you drop a key into — and the endpoint-by-protocol resolution that would make `-p zai` anywhere automatic."
---

# Z.ai plumbing: one provider, every agent

**Status:** DECIDED, 2026-09-01 — every question this doc asked is settled (ledger, §7);
implementation rides the parent doc's build order. Nothing here is built beyond what it inherits
from the shipped `providers` key and the three derives. This doc is the **first real consumer**
of [`profiles-as-pack-variants.md`](profiles-as-pack-variants.md): where the two disagree, that
design wins and this one files an issue against it — one such conflict was found and corrected
(§4.1, OQ-Z5).

**The want** *(the maintainer's words, 2026-08-31, lightly compressed)*: the user has
`providers: { zai: { … } }` and a key; `-p zai` works anywhere; every selected agent fires at
z.ai "by whatever is supported, with some resolution thing." Two routes were named and both are
mapped here: **name the protocol in the provider and fill all the values** (§3), or **ship a
layered zai template so the user just drops in a key** (§4). §5 is the resolution thing, which is
where the maintainer's own instinct points.

**Reads with:** [`profiles-as-pack-variants.md`](profiles-as-pack-variants.md) (the parent design —
its §3.1 profile schema, §6.2 credential preflight, §7.1 fragment-parity audit are assumed),
[`pack-profiles.md`](pack-profiles.md) §4–5 (the provider schema as first proposed),
[`host-agent-environment.md`](host-agent-environment.md) §4 (the per-agent delivery matrix).

---

## 1. The wiring, named — three different things carry the word "zai"

The schema confusion is real and it is one word doing three jobs. Separated:

| Thing | Where it lives | What it is |
| :--- | :--- | :--- |
| **the provider name** | the key in the `providers` map (`providers.zai`) | pure namespace. The derives iterate it and it lands in each agent's file as the provider/model id. Nothing resolves it; it is what other things reference. |
| **`requires_provider`** | a field on a `kind: "profile"` declaration | an **assertion, not a definition and not a reference anything follows**: when that profile is ACTIVE, `providers.zai` must exist and its `api_key_env` must be hydrated in the launch environment, or the launch refuses (the [§6.2 preflight](profiles-as-pack-variants.md#62-an-activated-profile-with-no-credential-is-a-preflight-failure)). It demands; it does not supply. |
| **the profile name `zai`** | the selector value `-p` sets | gates `kind: "profile"` contributions and reaches every selected pack's derive as `ctx.pack_profiles[c] == "zai"` — globally, declared or not (OQ-5's ruling, 2026-08-31). |

> [!IMPORTANT]
> **What already ships, and it is more than the parent doc advertises:** all three derives
> iterate **every** provider —
> [`packs/pi/derive.lua`](../../packs/pi/derive.lua) does `for name, prov in pairs(ctx.providers)`
> and so does codex's. Adding `providers.zai` to user config wires **pi, codex and opencode with
> zero packs and zero profiles, today**. The profile is for *selection* (`-p zai` making it the
> one you use) and for the one agent no derive covers: claude.

## 2. The z.ai facts

The **GLM Coding Plan** *(z.ai's coding-plan subscription; z.ai is Zhipu AI's international
brand — see [docs.z.ai](https://docs.z.ai/devpack/tool/others))* speaks **both** wire protocols,
each with its own base URL:

| Protocol | Base URL | Who can use it |
| :--- | :--- | :--- |
| Anthropic-compatible | `https://api.z.ai/api/anthropic` | Claude Code (its native wire), anything Anthropic-shaped |
| OpenAI-compatible | `https://api.z.ai/api/paas/v4` (coding-plan route: `…/api/coding/paas/v4`) | pi, codex, opencode, anything OpenAI-client-shaped |

One API key from the z.ai console serves both. **The OpenAI route speaks chat-completions only —
measured 2026-09-01 with an authenticated probe: `POST /v4/responses` is 404 on BOTH routes
while `/v4/chat/completions` returns a real completion on the same host** (a keyless probe
cannot settle this — z.ai's edge 401s garbage paths too, authenticating before routing).

> [!WARNING]
> **codex's derive DEFAULTS `wire_api` to `"responses"`**
> ([`packs/codex/derive.lua:27`](../../packs/codex/derive.lua#L27)) — a zai provider entry that
> omits `wire_api` gets codex wired to a route that 404s. Every zai entry must set
> `wire_api: "openai-chat"` explicitly until that default is reconsidered (it predates this
> measurement; a provider-agnostic default of `"openai-chat"` is the safe correction).

## 3. Route A — name the protocol, fill the values (pure config)

**User config** (`~/.config/yolo-jail/config.jsonc`) — everything except the key:

```jsonc
{
  "providers": {
    "zai": {
      "base_url": "https://api.z.ai/api/paas/v4",
      "wire_api": "openai-chat",            // measured: /v4/responses is 404 — and REQUIRED,
                                            // because codex's derive defaults to "responses"
      "api_key_env_name": "ZAI_API_KEY",     // renamed per parent OQ-6; the value is the NAME
      "models": { "default": "glm-4.7", "fast": "glm-4.7-air" }
    }
  },
  "env_sources": ["~/.config/yolo-jail/env"]
}
```

**The key** (`~/.config/yolo-jail/env`, untracked, `0600`) — this is the whole "drop in my api
key" step, and per the parent doc's P7 it is the *only* place a value may live:

```bash
ZAI_API_KEY=<the actual key>
```

**What lands, with no pack involved** (the derives, already shipped):

| Agent | File | Row the derive writes |
| :--- | :--- | :--- |
| pi | `~/.pi/agent/models.json` | `zai: { baseUrl, api: "openai-chat", apiKeyEnv: "ZAI_API_KEY", models: […] }` |
| codex | `~/.codex/config.toml` | `[model_providers.zai] base_url, wire_api, api_key_env` |
| opencode | `opencode.json` | `provider: zai` with `{env:ZAI_API_KEY}` interpolation |

**The wrinkle:** the schema has ONE `base_url` per provider, and z.ai needs two — one per
protocol. Today that means two provider entries (`zai` and `zai-claude`) sharing one
`api_key_env` — a bridge only, and **ruled out as an end-state** (OQ-Z2, 2026-08-31: one
provider and one key must produce any shape needed). §5's endpoint map is the fix.

**What Route A cannot reach:** claude. No derive consumes `providers` on claude's behalf — claude
needs the *anthropic* endpoint delivered as `ANTHROPIC_BASE_URL` + `ANTHROPIC_AUTH_TOKEN` in its
process env or settings `env` block, which is precisely the Bedrock-shape case the parent design
exists for. That gap is Route B's entrance.

## 4. Route B — the zai pack (a layered template; drop in a key)

A `packs/zai` that carries everything except the key. Two payloads:

**4.1 The claude bridge** — a profile for the pack's OWN env, plus a `config-overlay` for
claude's surface. **The earlier draft of this manifest was wrong** *(corrected 2026-09-01,
found by the completeness audit)*: it put a `config` patch targeting `claude/settings` inside
the `kind: "profile"` — but a profile's config patches fold into surfaces **the same pack owns**
(parent §3.1/§3.2, `autonomy`'s own-surfaces rule), and `packs/zai` owns no claude surface. The
cross-pack shape is exactly what `config-overlay` + the `profile` modifier is for (parent §7.1):

```jsonc
// packs/zai/pack.json
{ "name": "zai",
  "contributes": [
    // own declarations: env is ambient to the launch, so any pack may set it
    { "kind": "profile", "name": "zai", "requires_provider": "zai",
      "env": { "ANTHROPIC_BASE_URL": "https://api.z.ai/api/anthropic" } },
    // another pack's surface: config-overlay, gated on the same profile name
    { "kind": "config-overlay", "profile": "zai", "surface": "claude/settings",
      "config": { "env": { "ANTHROPIC_BASE_URL": "https://api.z.ai/api/anthropic" } } }
  ] }
```

The **payload split** (parent §5) doing its job: `ANTHROPIC_BASE_URL` is
configuration (routes, no secret) → settings `env` block (**measured honored**, OQ-Z4 — applied
before the first API call), surviving any invocation;
`ANTHROPIC_AUTH_TOKEN` **routes but cannot deliver** — the settings block would need the key
*value*, and `kind: "env"` is literal-only with no interpolation
([`kinds.go:96-99`](../../internal/packdecl/kinds.go#L96-L99)), a boundary that is now
**ruled, not just shipped**: yolo's `${VAR}` env-reference expansion was REMOVED from MCP
rendering by maintainer ruling on 2026-08-03
([`mcp-configuration.md:208-212`](mcp-configuration.md#L208-L212),
[`agent-credentials.md:214-218`](agent-credentials.md#L214-L218)) — yolo writes the reference
verbatim and the *consumer* resolves it from its own environment. So the token rides the
**process env** under its own name — one more line in the same `0600` file:

```bash
ZAI_API_KEY=<key>
ANTHROPIC_AUTH_TOKEN=<the same key, when -p zai is active for claude>
```

That second line is the settled cost of the no-reference-form ruling (OQ-Z3, ledger) — and it is
one line, because the **consumer derefs** everywhere else: pi's derive writes
`apiKeyEnv: "ZAI_API_KEY"` (a NAME) into `models.json` and the agent resolves it per request.
Compose-time code never sees secret values — the derive's `ctx` is a closed set of config tables
(`mcp_servers`, `lsp_servers`, `providers`, `agent_profiles` — [`sources.go`](../../internal/agentcfg/manifest/sources.go)),
and that boundary is the point.

**4.2 The template itself** — the pack shipping the provider's *facts* (base URLs, wire protocols,
model aliases) so the user's config shrinks to the key. Three shapes, in increasing machinery:

| Shape | How | Cost |
| :--- | :--- | :--- |
| **B1 — derive-side defaults** | the pack's derive fills `base_url`/`wire_api`/`models` when `ctx.providers.zai` is absent or partial | the effective provider becomes invisible in config — what you run on is assembled in Lua |
| **B2 — providers as a composed surface** | a `defaults` layer from the pack, user config the override, provenance in `yolo config diff` | the honest version of "layered template"; **amends the parent doc's non-goal** ("providers stays a hand-written map", §10) and needs a schema |
| **B3 — parameterize the protocol** | §5 below: the provider declares *endpoints by protocol*, resolution picks per agent | the maintainer's stated instinct; the smallest schema that makes `-p zai` automatic everywhere |

## 5. The resolution thing (B3)

One provider, endpoints keyed by protocol; every agent resolves to the endpoint for a protocol it
speaks:

```jsonc
{ "providers": { "zai": {
    "api_key_env": "ZAI_API_KEY",
    "models": { "default": "glm-4.7", "fast": "glm-4.7-air" },
    "endpoints": {
      "anthropic": { "base_url": "https://api.z.ai/api/anthropic" },
      "openai":    { "base_url": "https://api.z.ai/api/paas/v4", "wire_api": "openai-chat" }
    } } } }
```

Resolution is then a fixed table — no pack per agent, no N×M (the parent doc §3.2's argument,
kept): claude → `anthropic`; pi → `openai` (`openai-completions`/`openai-chat`); codex →
`openai` (**chat-completions only — measured, OQ-Z1: `/v4/responses` is 404 on both routes**);
opencode → `openai`. With OQ-5's global ruling, `-p zai`
sets the name everywhere and each agent's derive (or a shared derive library) emits its own
dialect of the one provider it selected.

Three closure rules the schema sketch owed *(added 2026-09-01, from the completeness audit)*:

1. **Coexistence:** `base_url` is valid ONLY alone — the single-protocol shorthand. `endpoints`
   and `base_url` together is a validation error (refuse the ambiguity; the message points at
   `endpoints`). Neither is an error too (a provider that exists only to be named).
2. **The derive gate must move:** all three derives currently skip a provider without
   `prov.base_url` (pi/derive.lua:8, codex/derive.lua:24, opencode/derive.lua:25) — an
   `endpoints`-only provider would be silently dropped from every catalog. The gate becomes
   `base_url OR endpoints`, with the per-protocol endpoint feeding each agent's dialect.
3. **Selection is config, and the mechanism exists:** the derives write a *catalog* (presence),
   not a choice — `-p zai` must also set each agent's use-this-one field (pi's default model,
   codex's `model`/`model_provider`, opencode's `provider`). That is one more
   `config-overlay`+`profile` patch per agent from `packs/zai` — no new mechanism, just this
   pack doing its job with the §4.1 shape.

The `capabilities` string-list already in the schema
([`config.go:88`](../../internal/config/config.go#L88)'s `knownProviderKeys`) is a natural home
for "this provider also speaks X" marking, if it graduates.

## 6. Shipped vs proposed

| Piece | State |
| :--- | :--- |
| `providers` key, closed schema, `api_key_env` name-contract | **shipped** ([`validate.go:885-945`](../../internal/config/validate.go#L885-L945)) |
| pi / codex / opencode derives wiring every provider | **shipped** — Route A works today, minus the endpoint wrinkle |
| z.ai wire protocol: chat-completions only | **measured 2026-09-01** (OQ-Z1; codex's `responses` derive default is a known 404 for zai — §2) |
| `kind: "profile"` + `requires_provider` + §6.2 preflight | proposed (parent §3.1, §6.2 — which now also covers the missing-provider case, parent OQ-9) |
| `-p <name>` global, declared-or-not | **ruled 2026-08-31** (parent OQ-5); mechanism proposed |
| claude bridge (settings `env` block honored for `ANTHROPIC_*`) | **measured YES, 2026-08-31** (OQ-Z4: controlled listener, settings-only run hit) |
| `endpoints` by protocol + resolution | **ruled 2026-08-31** (OQ-Z2: one provider, one key, any shape); schema proposed (§5, with its three closure rules) |
| provider defaults layer (B2) | not needed while B3 covers it; only if B3 proves insufficient |

## 7. Decision Ledger

No open questions remain in this doc or in the parent — the family's last live decision, the
`api_key_env` rename, was ruled 2026-09-01 (parent OQ-6: `api_key_env_name`). IDs are stable —
the parent doc and code comments cite them.

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| OQ-Z1 | **z.ai's OpenAI route speaks chat-completions only.** Authenticated probe: `POST /v4/responses` → 404 on both `api/paas/v4` and `api/coding/paas/v4`, while `/v4/chat/completions` → a real completion. `wire_api: "openai-chat"` always; codex's `"responses"` derive default is a known 404 for zai. (A keyless probe cannot settle this — the edge 401s nonexistent paths too.) | 2026-09-01 | §2 |
| OQ-Z2 | **One provider, one api key, any shape needed** — the two-entry bridge is a temporary spell; `endpoints`-by-protocol is the required end-state. *(Ruled by the maintainer.)* | 2026-08-31 | §3, §5 |
| OQ-Z3 | **No env reference form — env stays literal-only.** The controlling ruling is shipped: yolo's `${VAR}` expansion was REMOVED from MCP rendering 2026-08-03 (ambient process env must not be a config-rendering input; yolo writes references verbatim and the consumer resolves them). The alias costs one literal line in the same `0600` file; the shipped pattern everywhere else is consumer-deref (`apiKeyEnv` name-slots), with the derive `ctx` a closed set of config tables that never carries secret values. | 2026-09-01 | §4.1 |
| OQ-Z4 | **Claude Code honors `settings.json`'s `env` block before the first API call.** Controlled listener, claude 2.1.252, scratch `CLAUDE_CONFIG_DIR`, inherited `ANTHROPIC_*` scrubbed: settings-only `ANTHROPIC_BASE_URL` produced traffic identical to the process-env control. Retires the parent's R3. | 2026-08-31 | §4.1, parent §5 |

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| OQ-Z5 | **The claude patch is `config-overlay`, not the profile's `config` field** — `packs/zai` owns no claude surface, and a profile's config patches fold only into surfaces the same pack owns (parent §3.1/§3.2). Found as a live bug in the flagship manifest by the 2026-09-01 completeness audit. | 2026-09-01 | §4.1 |
| OQ-Z6 | **`endpoints` closure rules:** `base_url` valid only alone (together = validation error naming `endpoints`); the derives' `prov.base_url` gate becomes `base_url OR endpoints`; and selection is one more `config-overlay`+`profile` patch per agent setting that agent's use-this-one field — no new mechanism. | 2026-09-01 | §5 |
