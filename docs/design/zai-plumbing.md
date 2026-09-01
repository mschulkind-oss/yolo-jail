---
title: "Z.ai plumbing: one provider, every agent"
date: 2026-08-31
status: draft
tags: [packs, providers, profiles, resolution, zai]
summary: "The first real consumer of profiles-as-pack-variants: what it takes to go from a z.ai key in an untracked file to `-p zai` firing every selected agent at GLM by whatever protocol it speaks. Maps both routes the maintainer named — name-the-protocol-and-fill-the-values (pure config, mostly shipped), and a layered zai pack you drop a key into — and the endpoint-by-protocol resolution that would make `-p zai` anywhere automatic."
---

# Z.ai plumbing: one provider, every agent

**Status:** DRAFT, 2026-08-31. Nothing here is built beyond what it inherits from the shipped
`providers` key and the three derives. This doc is the **first real consumer** of
[`profiles-as-pack-variants.md`](profiles-as-pack-variants.md) and is meant to evolve in parallel
with it: where the two disagree, that design wins and this one files an issue against it.

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

One API key from the z.ai console serves both. Whether the OpenAI route speaks only
chat-completions or also the **Responses API** is **unverified** — OQ-Z1; it decides
`wire_api: "openai-chat"` vs `"responses"` for codex, which defaults to `"responses"`.

## 3. Route A — name the protocol, fill the values (pure config)

**User config** (`~/.config/yolo-jail/config.jsonc`) — everything except the key:

```jsonc
{
  "providers": {
    "zai": {
      "base_url": "https://api.z.ai/api/paas/v4",
      "wire_api": "openai-chat",            // OQ-Z1: or "responses"
      "api_key_env": "ZAI_API_KEY",          // the NAME of the var — rename under review, parent OQ-6
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

**4.1 The claude bridge** — a profile plus, if claude needs teaching, a config-overlay (the exact
shape the parent doc's [§7.1
audit](profiles-as-pack-variants.md#71-feature-complete-against-the-fragment-wish-list) proved
feature-complete against fragments):

```jsonc
// packs/zai/pack.json
{ "name": "zai",
  "contributes": [
    { "kind": "profile", "name": "zai", "requires_provider": "zai",
      "env": { "ANTHROPIC_BASE_URL": "https://api.z.ai/api/anthropic" },
      "config": [ { "agent": "claude", "name": "settings",
                    "managed": { "env": { "ANTHROPIC_BASE_URL": "https://api.z.ai/api/anthropic" } } } ] },
    { "kind": "config-overlay", "profile": "zai", "surface": "claude/settings",
      "config": { } }   // only if claude needs a key the owner doesn't write — see the split below
  ] }
```

The **payload split** (parent §5, corrected) doing its job: `ANTHROPIC_BASE_URL` is
configuration (routes, no secret) → settings `env` block, surviving any invocation;
`ANTHROPIC_AUTH_TOKEN` **routes but cannot deliver** — the settings block would need the key
*value*, and `kind: "env"` is literal-only with no interpolation
([`kinds.go:96-99`](../../internal/packdecl/kinds.go#L96-L99)). So the token rides the
**process env** under its own name — one more line in the same `0600` file:

```bash
ZAI_API_KEY=<key>
ANTHROPIC_AUTH_TOKEN=<the same key, when -p zai is active for claude>
```

That second line is Route B's real cost and OQ-Z3's question in one line: a profile
cannot *compose* env (say "token = the zai key") until env gains a reference form — which the
parent design deliberately does not have yet. (The `env` **block** itself is now measured
honored — OQ-Z4 — so the `ANTHROPIC_BASE_URL` half of the patch above is confirmed working.)

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
`openai` (`responses` or chat, OQ-Z1); opencode → `openai`. With OQ-5's global ruling, `-p zai`
sets the name everywhere and each agent's derive (or a shared derive library) emits its own
dialect of the one provider it selected. The `capabilities` string-list already in the schema
(`validate.go`'s `knownProviderKeys`) is a natural home for "this provider also speaks X"
marking, if it graduates.

## 6. Shipped vs proposed

| Piece | State |
| :--- | :--- |
| `providers` key, closed schema, `api_key_env` name-contract | **shipped** (`validate.go:885-944`) |
| pi / codex / opencode derives wiring every provider | **shipped** — Route A works today, minus the endpoint wrinkle |
| `kind: "profile"` + `requires_provider` + §6.2 preflight | proposed (parent §3.1, §6.2) |
| `-p <name>` global, declared-or-not | ruled 2026-08-31 (parent OQ-5); mechanism proposed |
| claude bridge (settings `env` block honored for `ANTHROPIC_*`) | **measured YES, 2026-08-31** (OQ-Z4: controlled listener, settings-only run hit) |
| `endpoints` by protocol + resolution | proposed (§5); OQ-Z2 |
| provider defaults layer (B2) | proposed, amends a non-goal; only if B3 is insufficient |

## 7. Open Questions

1. 💬 🔒 **OQ-Z1: Does z.ai's OpenAI route speak the Responses API, or only chat-completions?**
   Decides `wire_api` for codex (which defaults to `"responses"`). Ten minutes against the live
   endpoint with a scratch key; no design work should guess it.

   _Leaning:_ Test before writing either config.

   **Answer (leaning affirmed by the maintainer, 2026-08-31):**
   > Test first — and note the keyless probe was TRIED and is insufficient: unauthenticated
   > `POST` to `…/v4/chat/completions`, `…/v4/responses`, and (as the control) a garbage path
   > `…/v4/nonexistent-probe` all return **401** — z.ai's edge authenticates before routing, so
   > path existence cannot be read without a credential. Only an authenticated probe settles it.

2. ✅ **OQ-Z2: `endpoints` by protocol, or two provider entries?** §5's map is one subscription,
   one key, two URLs; two entries (`zai`, `zai-claude`) work today but split the name and
   duplicate the key. If `endpoints` lands, `base_url` becomes the single-protocol shorthand.

   _Leaning:_ `endpoints`, with `base_url` kept as shorthand for single-protocol providers —
   the wrinkle in §3 is the motivating case and it is not rare (Bedrock is the same shape).

   **Answer (ruled by the maintainer, 2026-08-31):**
   > "However you do it, **one provider**: one provider and one api key should be able to
   > produce any shape needed." The two-entry bridge is demoted to a temporary spell; §5's
   > endpoint map (or equivalent) is the required end-state.

3. 💬 **OQ-Z3: Does env need a reference form?** — *(rewritten in files, per review)* Route B's
   token line (`ANTHROPIC_AUTH_TOKEN` = the zai key) is spelled by hand today because no writer
   of files knows the key's value. Two questions inside the review comment, answered first:

   - **"A derive can do whatever it wants — why can't it deref?"** It cannot see a value to
     deref: the derive's `ctx` is a **closed set of live config tables** — `mcp_servers`,
     `lsp_servers`, `providers`, `agent_profiles`
     ([`sources.go`](../../internal/agentcfg/manifest/sources.go)) — and no member carries
     secret values. Compose-time code sees configuration, never credentials; that boundary is
     the point.
   - **"I guess a kind is just from another pack?"** No — a **kind** is the closed vocabulary of
     declarative contribution shapes core renders (footprint-checked,
     [`kinds.go`](../../internal/packdecl/kinds.go)); a **derive** is the pack's own Lua at
     compose time. Neither is "from another pack" — and neither needs to deref, because the
     **consumer** does. File A shows the shipped pattern.

   **A. What ships — the consumer derefs, nobody else.** pi's derive writes a NAME into pi's
   config and the agent resolves it per request:

   ```jsonc
   // ~/.pi/agent/models.json — written by packs/pi/derive.lua
   { "zai": { "baseUrl": "https://api.z.ai/api/paas/v4", "api": "openai-chat",
              "apiKeyEnv": "ZAI_API_KEY", "models": [ … ] } }   // a NAME; pi reads $ZAI_API_KEY itself
   ```

   Claude is the gap only because its equivalent slot — `settings.json`'s `env` block — takes
   **literal values** (now measured honored, OQ-Z4), and nothing that writes a file may know the
   token's value, so nothing can fill a literal slot with it.

   **B. Today's answer — the alias is spelled by hand, once, in the env file:**

   ```bash
   # ~/.config/yolo-jail/env (untracked, 0600)
   ZAI_API_KEY=<key>
   ANTHROPIC_AUTH_TOKEN=<the same key>   # the alias; claude reads process env (works today — measured)
   ```

   **C. The proposal — launch-time deref in the env renderer.** A `{env:VAR}` value expanded
   where `env_sources` hydration already happens (the run pipeline's env assembly) — not in the
   derive, not in a kind:

   ```jsonc
   // packs/zai/pack.json — the profile says where the value comes FROM
   { "kind": "profile", "name": "zai",
     "env": { "ANTHROPIC_AUTH_TOKEN": "{env:ZAI_API_KEY}" } }   // expanded at launch → process env
   ```

   _Leaning:_ Keep env literal (B). C is real machinery — a renderer that composes values
   instead of names, plus a security read (what else `{env:…}` could pull, and who may write the
   string). Pattern A covers every agent with a name-slot; claude is the only literal-slot
   agent, and B costs one duplicated line in a `0600` file.

   **Answer:**
   > _(empty — fill in when decided)_

4. ✅ **OQ-Z4: Does Claude Code honor `settings.json`'s `env` block for `ANTHROPIC_BASE_URL`
   and friends?** Inherited from the parent's OQ-4 — this doc's Route B was blocked on the same
   measurement. If no, the claude bridge is profile-gated `kind: "env"`, jail-notch plus wrapper.

   **Answer (measured 2026-08-31):**
   > **YES.** Controlled listener experiment — claude 2.1.252, scratch `CLAUDE_CONFIG_DIR`,
   > inherited `ANTHROPIC_*` scrubbed from the process env. Control run: `ANTHROPIC_BASE_URL`
   > in the **process env** → the CLI sent its startup probe (`HEAD /api/hello`) and
   > `POST /v1/messages?beta=true` to the listener (8 attempts with backoff). Experiment run:
   > the same var present **ONLY in `settings.json`'s `env` block** → identical traffic to the
   > listener, same startup probe. The block is applied before the first API call. Scope: the
   > witness var is `ANTHROPIC_BASE_URL`; `CLAUDE_CODE_USE_BEDROCK` rides the same
   > settings-env mechanism but was not separately exercised (a Bedrock re-test needs AWS
   > credentials). **Resolves the parent's OQ-4 and retires R3.**
