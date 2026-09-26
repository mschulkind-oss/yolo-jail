---
title: "Why a bridge endpoint shadowed Pi's Codex provider — and how ambient keys took over"
date: 2026-09-25
status: accepted
tags: [providers, codex, pi, openai-auth, shadowing, credentials]
summary: "Adding an openai-responses endpoint to the openai-codex provider allowed wire-bridge to route to ChatGPT, but caused Pi's derive to shadow its built-in subscription provider with a third-party models.json row. When Pi treated openai-codex as a generic OpenAI platform endpoint, it picked up the workspace's ambient OPENAI_API_KEY, resulting in 401 errors against the Codex backend."
---

# Why a bridge endpoint shadowed Pi's Codex provider — and how ambient keys took over

**Status:** BUILT, 2026-09-26, at `c9982bce` (pi) and `d079ac31` (omp); codex already excluded `openai-codex`. Measured through the boot render only, by the catalog tests; no live pi or omp has run it. Evidence verified at `c5bab09b`.

> **In short.** A pack-level endpoint added for wire-bridge adaptation caused Pi's derive
> to generate a `models.json` entry for `openai-codex`, overriding Pi's built-in subscription
> client with an API-key-driven platform dialect that picked up the workspace's ambient
> `OPENAI_API_KEY`. Suppressing first-party subscription providers from catalog generation
> restores the separation between subscription agents and platform API keys.

**Why it matters.** A user working in a repo with platform credentials (such as image generation
workflows or scripts) who runs Pi on their ChatGPT subscription suddenly suffers HTTP 401 failures
because the agent attempts to authenticate to ChatGPT's Codex backend using an OpenAI platform
service account key.

**The shape.** Filter first-party subscription providers out of Pi's `models.json` catalog generation
(matching Codex CLI's derive), align unit test pack sets with production composition, and preserve
the boundary between subscription OAuth tokens and ambient platform API keys.

**Cost.** None for user configuration. No breaking changes to existing profiles.

**Start at [§3](#3-the-mechanism-of-shadowing-how-modelsjson-overrode-pis-native-client)** — how the shadow happened. The rest falls out of it.

**Needs your ruling:** none. [OQ-1](#OQ-1) (exclude by name) and [OQ-2](#OQ-2) (keep the endpoint; never catalog a natively implemented provider) are ruled.

**Reads with:** [`pi-codex-provider-shadowing-plan.md`](pi-codex-provider-shadowing-plan.md) (the companion sketch — incomplete while questions are open),
[`provider-credential-scope.md`](provider-credential-scope.md) (the ambient environment delivery boundary),
[`../reference/providers.md`](../reference/providers.md) (the provider declaration and derivation reference).

---

## Terms, in plain words

- **First-party / subscription provider.** A provider backed by an interactive user subscription
  (e.g., ChatGPT Plus/Pro) using OAuth refresh tokens rather than a metered API key. In yolo,
  `openai-codex` is the primary subscription provider.
- **Third-party / catalog provider.** An external model endpoint configured in an agent's custom
  model catalog (`models.json` in Pi, `model_providers` in Codex). Typically authenticated with
  an API key or custom bearer header.
- **Provider shadowing.** When yolo writes a custom provider entry into an agent's configuration
  using the same name as a built-in provider, replacing the agent's native client, headers, and
  authentication logic with the custom endpoint's settings.
- **Wire API / dialect.** The wire protocol spoken over HTTP/WebSocket (`openai-codex-responses`
  for ChatGPT subscription sessions vs. `openai-responses` for OpenAI platform completions).
- **Ambient credentials.** Environment variables (such as `OPENAI_API_KEY`) hydrated from the host
  or `.env` into the jail environment, visible to all container processes.

---

## 1. Verdict and core principles

The failure observed in the `stories` workspace was not a failure of `openai-auth-broker`. The
broker daemon operated correctly, rotating and holding valid OAuth JWTs. The failure was a
configuration collision between two systems:

1. **Pack endpoint projection:** `packs/openai-auth` added a public `openai-responses` endpoint
   to `openai-codex` for inter-agent adaptation via `wire-bridge`.
2. **Catalog derivation:** Pi's derive script translated that endpoint into a custom `models.json`
   entry, changing `openai-codex` from a built-in OAuth subscription provider into a custom
   `openai-responses` endpoint.
3. **Ambient credential leakage:** When Pi treated `openai-codex` as a generic OpenAI platform
   endpoint, it consulted ambient environment variables and injected `OPENAI_API_KEY`
   (`sk-svcac...fvMA`), sending a platform service account key to `https://chatgpt.com/backend-api/codex`,
   which immediately rejected it with HTTP 401.

Three principles govern the fix:

- **P1. An agent must never derive custom catalog entries for providers it natively implements.**
  If an agent CLI possesses built-in client logic, headers, and OAuth handling for a named
  subscription provider (as both Codex CLI and Pi do for `openai-codex`), yolo must not emit a
  catalog entry for that provider into the agent's configuration file.
- **P2. Adaptation endpoints must not corrupt native consumers.** Declaring an endpoint on a pack
  contribution to enable third-party adapters (like `wire-bridge` translating Anthropic calls to
  Codex) must not alter the configuration of agents that speak to that provider natively.
- **P3. Test fixtures must mirror production pack composition.** When an agent pack declares an
  unconditional dependency (`"needs": ["openai-auth"]`), unit tests asserting derivation invariants
  must compose the full pack closure. Testing the agent pack in isolation creates false positives
  where shadowing assertions pass only because the shadowed provider was absent from the test set.

---

## 2. Background: How Codex and Pi authenticate and route requests

### 2.1 Codex CLI

Codex CLI speaks `openai-responses` natively. It connects to ChatGPT's backend using OAuth tokens
stored in `~/.codex/auth.json`.

In `packs/codex/derive.lua` ([lines 136–140](../../packs/codex/derive.lua#L136-L140)), Codex's derive
specifically excludes `openai-codex` from the generated `model_providers` table:

```lua
-- openai-codex is Codex's native subscription provider backed by OAuth
-- credentials, not a custom third-party endpoint with an API key.
if name ~= "openai-codex" then
  local baseUrl, api = codexReachable(prov)
  ...
```

When a user selects the `codex` profile (`yolo -p codex -- codex`), Codex sets `model = "gpt-6-sol"`
in `~/.codex/config.toml` without setting `model_provider`. Codex CLI defaults to its native first-party
backend and uses its internal OAuth mechanism.

### 2.2 Pi (`pi-coding-agent`)

Pi has two completely separate OpenAI provider implementations in `@earendil-works/pi-ai`:

| Provider ID | Implementation Function | Wire API (`api`) | Authentication Source | Target Endpoint |
| :--- | :--- | :--- | :--- | :--- |
| `openai-codex` | `openaiCodexProvider()` | `openai-codex-responses` | OAuth JWT (`auth.json` via `yolo-openai-auth.js`) | `https://chatgpt.com/backend-api/codex` |
| `openai` | `openaiProvider()` | `openai-responses` | API Key (`OPENAI_API_KEY`) | `https://api.openai.com/v1` |

`openai-codex-responses` (`openai-codex-responses.js` in `@earendil-works/pi-ai`)
is specialized for ChatGPT:
1. It decodes the OAuth access token JWT to extract `chatgpt_account_id`.
2. It sends `Authorization: Bearer <JWT>` and `chatgpt-account-id: <account-id>`.
3. It handles WebSocket connection upgrades (`wss://chatgpt.com/backend-api/codex/responses`).

In contrast, `openai-responses` (`openai-responses.js` in `@earendil-works/pi-ai`)
uses the official `openai` npm SDK, expecting an OpenAI Platform API key (`sk-...`).

---

## 3. The mechanism of shadowing: How `models.json` overrode Pi's native client

### 3.1 The Wire-Bridge Endpoint Addition

In commit `b7373476`, `packs/openai-auth/pack.json` was updated to declare an endpoint on `openai-codex`:

```json
{
  "capabilities": ["web_search"],
  "endpoints": {
    "openai-responses": {
      "base_url": "https://chatgpt.com/backend-api/codex",
      "wire_api": "openai-responses"
    }
  },
  "kind": "provider",
  "name": "openai-codex"
}
```

The intent of this change was to advertise the public Responses endpoint for `wire-bridge`, enabling
bridge adapters to route external agents (like Claude Code) to ChatGPT's backend without hardcoding
port numbers or addresses.

### 3.2 The Derivation Hole in Pi

While Codex CLI explicitly filtered `name ~= "openai-codex"` in its derive script, Pi's derive
script (`packs/pi/derive.lua`) lacked this check.

In `packs/pi/derive.lua` ([lines 299–301](../../packs/pi/derive.lua#L299-L301)):

```lua
for name, prov in pairs(ctx.providers) do
  local baseUrl, api = piReachable(prov)
  if baseUrl then
    ...
```

`piReachable` inspected `prov.endpoints["openai-responses"]`. Because `openai-responses` is in
`piDialect`, `piReachable` returned:
- `baseUrl = "https://chatgpt.com/backend-api/codex"`
- `api = "openai-responses"`

This resulted in `~/.pi/agent/models.json` containing:

```json
{
  "providers": {
    "openai-codex": {
      "baseUrl": "https://chatgpt.com/backend-api/codex",
      "api": "openai-responses"
    }
  }
}
```

### 3.3 What Pi Does With Shadowed Providers

In Pi's runtime (`provider-composer.js:335–351` in `@earendil-works/pi-coding-agent`),
when `models.json` configures a provider:
1. Pi merges the `models.json` entry over the built-in `openaiCodexProvider()`.
2. The merged provider's wire API becomes `openai-responses`.
3. When executing requests, `supportsBaseApi(model)` checks whether the built-in provider supports
   the model's API. Because the built-in provider supports `openai-codex-responses` and the model
   now requests `openai-responses`, `supportsBaseApi` returns `false`.
4. Pi falls back to calling `getApiProvider("openai-responses")`, executing the request through the
   OpenAI SDK rather than the native Codex subscription client.

---

## 4. The ambient credential collision

Once Pi routes `openai-codex` through `openai-responses` instead of its native client:

```
┌─────────────────────────────────────────────────────────────────────────┐
│                               Jail Host                                 │
│  Workspace env: OPENAI_API_KEY=sk-svcac...fvMA (Platform Service Acct)  │
└────────────────────────────────────┬────────────────────────────────────┘
                                     │ yolo userenv hydration
                                     ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                           Jail Environment                              │
│  /etc/yolo-user-env.sh: export OPENAI_API_KEY="sk-svcac...fvMA"         │
└────────────────────────────────────┬────────────────────────────────────┘
                                     │ process.env
                                     ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                            Pi Agent Process                             │
│  models.json: openai-codex -> api: "openai-responses"                   │
│                                                                         │
│  OpenAI SDK client creation:                                            │
│    apiKey = options?.apiKey ?? process.env.OPENAI_API_KEY              │
│    baseURL = "https://chatgpt.com/backend-api/codex"                    │
│                                                                         │
│  HTTP Request:                                                          │
│    POST https://chatgpt.com/backend-api/codex/responses                 │
│    Authorization: Bearer sk-svcac...fvMA                                │
└────────────────────────────────────┬────────────────────────────────────┘
                                     │ HTTP 401 Unauthorized
                                     ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                         OpenAI ChatGPT Gateway                          │
│  "Incorrect API key provided: sk-svcac...fvMA. You can find your        │
│   API key at https://platform.openai.com/account/api-keys."             │
└─────────────────────────────────────────────────────────────────────────┘
```

1. **Global environment hydration:** yolo hydrates all variables from `env_sources` into
   `/etc/yolo-user-env.sh` via `writeUserEnvFile` ([`internal/cli/run/userenv.go`](../../internal/cli/run/userenv.go)).
   In the `stories` repo, an OpenAI Platform service account key (`OPENAI_API_KEY=sk-svcac...fvMA`)
   was defined for background image generation and other platform tools.
2. **SDK Fallback:** The OpenAI SDK instantiated by `openai-responses` inspects `process.env.OPENAI_API_KEY`.
3. **Mismatched Gateway:** The request was dispatched to `https://chatgpt.com/backend-api/codex/responses`
   carrying `Authorization: Bearer sk-svcac...fvMA`. ChatGPT's backend gateway only accepts ChatGPT
   Plus/Pro subscription OAuth tokens. It rejected the platform key with HTTP 401.

---

## 5. Test blindness: Why CI did not detect the regression

The repository already contained an explicit unit test guarding against this exact failure!

In [`internal/entrypoint/pi_codex_profile_test.go:70–75`](../../internal/entrypoint/pi_codex_profile_test.go#L70-L75):

```go
models := r.piModels(t)
if catalog, _ := models["providers"].(map[string]any); catalog != nil {
    if _, shadowed := catalog["openai-codex"]; shadowed {
        t.Fatalf("models.json shadows Pi's built-in openai-codex provider: %#v", catalog)
    }
}
```

The test even carries an explanatory docstring:
> *"Pi owns openai-codex in its built-in catalog, so yolo selects it without shadowing it in models.json."*

However, the test set up its provider table using:

```go
pi := shippedPiPack(t)
providers, err := packload.ComposeProviders(nil, []*packload.Pack{pi})
```

It composed `providers` using the `pi` pack alone.
In production, `packs/pi/pack.json` declares `"needs": ["openai-auth"]`, so `openai-auth` is always
present when Pi runs. `openai-auth` is the pack that defines `openai-codex`.

Because `openai-auth` was omitted from the test fixture:
1. `providers` never contained `openai-codex`.
2. `packs/pi/derive.lua` never saw `ctx.providers["openai-codex"]`.
3. `models.json` never wrote `openai-codex`.
4. The test assertion passed vacuously.

In the same file, a second test (`TestPiCatalogNeverWritesAModelsMapWhereAnArrayBelongs`,
[lines 98–143](../../internal/entrypoint/pi_codex_profile_test.go#L98-L143)) *did* include `openai-auth`
in its pack set, but only verified that the `models` key was not rendered as an empty object—it did
not assert that `openai-codex` was absent from the catalog.

---

## 6. Proposed resolution

### 6.1 Catalog Exclusion in Pi's Derive

Mirror Codex CLI's derive logic in `packs/pi/derive.lua`. When iterating over `ctx.providers` to
construct `~/.pi/agent/models.json`, skip `openai-codex`:

```lua
for name, prov in pairs(ctx.providers) do
  if name ~= "openai-codex" then
    local baseUrl, api = piReachable(prov)
    if baseUrl then
      ...
    end
  end
end
```

Pi's settings derive (`yolo.derive("pi", "settings")`) already contains dedicated logic for `openai-codex`
([lines 475–515](../../packs/pi/derive.lua#L475-L515)), writing `enabledModels` and `selection` while
relying on Pi's built-in catalog. Excluding `openai-codex` from the `models` derive ensures Pi uses its
native `openai-codex-responses` implementation and OAuth extension.

### 6.2 Test Composition Alignment

Update `TestPiCodexProfileSelectsBuiltInProviderAndExplicitModel` in
`internal/entrypoint/pi_codex_profile_test.go` to include `openai-auth` in its input pack list:

```go
pi := shippedPiPack(t)
openaiAuth := shippedPack(t, "openai-auth")
providers, err := packload.ComposeProviders(nil, []*packload.Pack{pi, openaiAuth})
```

This transforms the existing shadowing assertion from a vacuum test into an active regression gate.

### 6.3 Relation to credential scoping

Excluding `openai-codex` from `models.json` prevents Pi from switching to `openai-responses` and
falling back to `OPENAI_API_KEY`. However, the broader issue of ambient environment leakage
remains tracked in [`provider-credential-scope.md`](provider-credential-scope.md). When a profile
selects a subscription provider, ambient platform keys for the same vendor should ideally be scoped
or masked to prevent tools or subagents from inadvertently picking them up.

---

## 7. Non-goals

- **Not modifying `openai-auth-broker`:** The broker's daemon protocol, lock files, and token
  refresh loops are unaffected.
- **Not removing the wire-bridge endpoint from `packs/openai-auth`:** Other agents (e.g. Claude
  Code via `wire-bridge`) rely on the `openai-responses` endpoint declaration to target ChatGPT's
  backend.
- **Not redesigning Pi's OAuth extension:** `packs/pi/extensions/yolo-openai-auth.js` correctly
  hooks into Pi's OAuth credential flow when Pi uses `openai-codex-responses`.

---

## 8. Risks and invariants

| Risk | Consequence | Mitigation |
| :--- | :--- | :--- |
| **R1. Pi cannot reach custom Codex proxies** | A user configuring a private reverse proxy for Codex via `providers.openai-codex.endpoints` would have their URL ignored if `openai-codex` is unconditionally skipped. | If custom endpoint overrides for Codex are ever needed in Pi, they must specify `wire_api: "openai-codex-responses"` or be routed through a distinct provider name. Built-in subscription providers must not be repurposed as custom endpoints. |
| **R2. Test pack omission re-occurs** | A future agent test might omit required dependencies and miss catalog collisions. | The helper `testPacksForAgent(agent)` should automatically resolve pack `needs` so unit tests always test the full closure that production runs. |

---

## 9. Open Questions

1. ✅ <a id="OQ-1"></a>**OQ-1: Distinguishing first-party subscription providers in agent derives.** Should
   `packs/pi/derive.lua` exclude `openai-codex` by bare name (matching `packs/codex/derive.lua`),
   or should `kind: "provider"` declare an explicit capability/flag (such as `is_subscription`
   or `native`) that all derives inspect?


   _Leaning:_ Name exclusion (`name ~= "openai-codex"`) in `packs/pi/derive.lua` for v1. Codex CLI
   already uses this exact check (`if name ~= "openai-codex"` in `packs/codex/derive.lua:139`). Adding
   a new manifest schema field to `packdecl` for a single provider is unnecessary complexity when
   `openai-codex` is already recognized across core as the sole subscription provider.

   **Answer:**
   > **Name exclusion for v1**, ruled in review 2026-09-26: *"to match Codex CLI, deferring schema
   > changes until another subscription provider exists."* `packs/pi/derive.lua` skips `openai-
   > codex` when it builds `models.json`, exactly as `packs/codex/derive.lua` does. A provider-
   > level flag waits until a second subscription provider exists (it is the same question as [OQ-
   > BR2](providers-and-profiles-redesign.md#OQ-BR2)'s marker). Built in `c9982bce`.

2. ✅ <a id="OQ-2"></a>**OQ-2: Packaging of inter-agent adaptation endpoints.** Does `packs/openai-auth` legitimately
   own `endpoints["openai-responses"]` on `openai-codex`, or should inter-agent adapter targets
   be declared in a separate namespace or contribution that catalog derives ignore?


   _Leaning:_ Keep the endpoint on `openai-codex` in `packs/openai-auth`. The endpoint declaration
   is factually true: ChatGPT's backend does expose an `openai-responses` wire API at that URL. The
   architectural defect was not declaring the endpoint; it was Pi's derive assuming that any declared
   endpoint must be cataloged in `models.json`, even for providers Pi implements natively.

   **Answer:**
   > **Keep the endpoint on `openai-codex` in `packs/openai-auth`, and establish the rule**, ruled
   > in review 2026-09-26: *an agent never derives catalog entries from providers it natively
   > implements.* The declaration is true and stays; the defect was a derive cataloging a provider
   > its agent already implements. The rule is P1's: it covers a *subscription* provider the agent
   > implements with its own client and login, not a vendor the agent also knows by the same key
   > name. Every agent derive was checked against it: omp had the same defect, fixed in `d079ac31`
   > (omp applies a `models.yml` row's `baseUrl` to its built-in provider of that name); claude,
   > copilot, agy and opencode catalog nothing that shadows a native subscription client.

---

## 10. Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-1 | **Exclude `openai-codex` from pi's catalog by name**, matching `packs/codex/derive.lua`; a provider flag waits for a second subscription provider ([OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2)'s marker) | 2026-09-26 | [OQ-1](#OQ-1) | yes, 2026-09-26 (`c9982bce`) |
| OQ-2 | **Keep the `openai-responses` endpoint on `openai-codex`**, and the rule: an agent never derives catalog entries from a subscription provider it natively implements ([P1](#1-verdict-and-core-principles)) | 2026-09-26 | [OQ-2](#OQ-2) | yes, 2026-09-26 (omp `d079ac31`; the other derives needed nothing) |
