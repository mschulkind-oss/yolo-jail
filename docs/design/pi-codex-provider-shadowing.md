---
title: "Why a bridge endpoint shadowed Pi's Codex provider — and how ambient keys took over"
date: 2026-09-25
status: in-review
stage: DESIGN
next: "Rule OQ-3 — pi's Converse route through the wire bridge is not built until it does (wire-bridge-gateway.md WG-I36)"
tags: [providers, codex, pi, openai-auth, shadowing, credentials]
summary: "Adding an openai-responses endpoint to the openai-codex provider allowed wire-bridge to route to ChatGPT, but caused Pi's derive to shadow its built-in subscription provider with a third-party models.json row. When Pi treated openai-codex as a generic OpenAI platform endpoint, it picked up the workspace's ambient OPENAI_API_KEY, resulting in 401 errors against the Codex backend."
---

# Why a bridge endpoint shadowed Pi's Codex provider — and how ambient keys took over

**Status:** 2026-10-01 — [OQ-3](#OQ-3) is open: how far [OQ-2](#OQ-2)'s rule reaches. The `openai-codex` exclusion is built, 2026-09-26, at `92c20cc6` (pi) and `4ed48212` (omp); codex already excluded it. Measured through the boot render by the catalog tests, which compose each agent's `needs` closure since 2026-09-27 ([§6.2](#62-test-composition-alignment)), and for pi through a `-p codex` integration launch since 2026-10-01, whose rendered `models.json` holds no `openai-codex` row ([the plan's §5](pi-codex-provider-shadowing-plan.md#5-verification-checklist)); no live pi or omp has run it. Evidence verified at `c5bab09b`; pi's and claude's codex handling re-verified at `4dcebd18`.

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

**Needs your ruling:** [OQ-3](#OQ-3): does the natively-implements rule cover every provider an agent implements natively, or only a subscription provider with its own client and login?

**Blocks:** pi's Converse route through the wire bridge. [OQ-WG8](wire-bridge-gateway.md#OQ-WG8) was decided on
2026-09-30 as [WG-I36](wire-bridge-gateway.md#WG-I36), which leaves where pi's override row lives to
[OQ-3](#OQ-3) and does not build the route until it rules.

**Reads with:** [`pi-codex-provider-shadowing-plan.md`](pi-codex-provider-shadowing-plan.md) (the companion sketch — incomplete while questions are open),
[`providers.md`'s credential gate](../reference/providers.md#the-credential-gate) (the ambient environment delivery boundary),
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
  catalog entry for that provider into the agent's configuration file. The first sentence and
  the ruling ([OQ-2](#OQ-2)) name every natively implemented provider, while this example names a
  subscription one. Which of the two the rule means is [OQ-3](#OQ-3).
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

When a user selects the `codex` profile (`yolo -p codex -- codex`), Codex sets `model` to the first
id of the provider's declared list (`gpt-6-sol` as shipped;
[ML-D1](model-lists-and-pickers.md#ML-D1)) in `~/.codex/config.toml` without setting `model_provider`. Codex CLI defaults to its native first-party
backend and uses its internal OAuth mechanism.

### 2.2 Pi (`pi-coding-agent`)

Pi has two completely separate OpenAI provider implementations in `@earendil-works/pi-ai`:

| Provider ID | Implementation Function | Wire API (`api`) | Authentication Source | Target Endpoint |
| :--- | :--- | :--- | :--- | :--- |
| `openai-codex` | `openaiCodexProvider()` | `openai-codex-responses` | OAuth JWT (`auth.json` via `yolo-openai-auth.js`) | `https://chatgpt.com/backend-api`, requests to `…/codex/responses` |
| `openai` | `openaiProvider()` | `openai-responses` | API Key (`OPENAI_API_KEY`) | `https://api.openai.com/v1` |

yolo's extension, [`packs/pi/extensions/yolo-openai-auth.js`](../../packs/pi/extensions/yolo-openai-auth.js),
re-registers `openai-codex` with `pi.registerProvider`. Since `e2f7bb89` it registers the whole
provider, not only the login:

- the OAuth login and refresh, both served by yolo's `openai-auth-broker`, with the access token as
  the key;
- `api: "openai-codex-responses"` and `baseUrl: "https://chatgpt.com/backend-api"`;
- yolo's own model list, which since 2026-09-27 is the one declaration on the `openai-codex`
  provider ([ML-D1](model-lists-and-pickers.md#ML-D1)): the extension reads it from
  `~/.pi/agent/yolo-openai-codex-models.json`, a file yolo renders at every jail boot, and takes each
  entry's cost, thinking and image facts from pi's own catalog
  ([ML-D3](model-lists-and-pickers.md#ML-D3)). It is the GPT-6 models with a `[1m]` variant
  beside each; the GPT-5.x ids a hand copy of pi's catalog carried until then are gone
  ([ML-D4](model-lists-and-pickers.md#ML-D4)). With no file it registers no models, and pi keeps
  its built-in catalog, which is what `yolo host apply` leaves on the host
  ([ML-D8](model-lists-and-pickers.md#ML-D8));
- a `before_provider_request` hook that strips the `[1m]` suffix from the model id before the
  request leaves.

Read from pi 0.87.1's installed code, not run: pi composes an extension's registration above
`models.json` (`composeModelProvider` in `core/provider-composer.js`), so this registration would
now also override a `models.json` row's address and model list for `openai-codex`. The exclusion
in [§6.1](#61-catalog-exclusion-in-pis-derive) does not lean on that. [P1](#1-verdict-and-core-principles)
is a rule about what yolo writes, and layer precedence is pi's to change.

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

In `packs/pi/derive.lua`'s models loop, before `92c20cc6`:

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

> [!WARNING]
> **Step 2 does not follow from a re-read at pi 0.87.1** (2026-09-27, installed code read, not
> run). There `applyModelsJson` applies a row's provider-level `api` only to models the row itself
> defines. The row in [§3.2](#32-the-derivation-hole-in-pi) defines none, so it moves each built-in
> model's address and keeps its `openai-codex-responses` wire. The mechanism above is recorded as
> filed from the `stories` workspace. Which pi version or row produced the 401 was not re-measured.
> The exclusion stands on [P1](#1-verdict-and-core-principles) either way.

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

In [`internal/entrypoint/pi_codex_profile_test.go`](../../internal/entrypoint/pi_codex_profile_test.go),
`TestPiCodexProfileSelectsBuiltInProviderAndExplicitModel` as it stood before the
[§6.2](#62-test-composition-alignment) edit:

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

In the same file, a second test (`TestPiCatalogNeverWritesAModelsMapWhereAnArrayBelongs`) *did*
include `openai-auth` in its pack set, but only verified that the `models` key was not rendered as
an empty object—it did not assert that `openai-codex` was absent from the catalog.

---

## 6. Proposed resolution

### 6.1 Catalog Exclusion in Pi's Derive

Mirror Codex CLI's derive logic in `packs/pi/derive.lua`: when iterating over `ctx.providers` to
construct `~/.pi/agent/models.json`, skip `openai-codex`. Built in `92c20cc6` in this shape:

```lua
for name, prov in pairs(ctx.providers) do
  local native = (name == "openai-codex")
  local baseUrl, api = nil, nil
  if not native then
    baseUrl, api = piReachable(prov)
  end
  -- a via row is also gated on `not native`
  ...
end
```

The flag gates the via row too, so a via profile selecting `openai-codex` writes no row either.
`packs/omp/derive.lua` does the same for omp (`4ed48212`).

Pi's settings derive (`yolo.derive("pi", "settings")`) has its own `openai-codex` branch. It writes
the selection pair and the `subagents` policy, whose `modelScope.allow` is the exact declared ids
([ML-D5](model-lists-and-pickers.md#ML-D5)). It writes no `enabledModels` since 2026-09-27:
pi's view of `openai-codex` is the list the extension registers
([ML-D2](model-lists-and-pickers.md#ML-D2)). A third derive, `yolo.derive("pi", "codex-models")`,
writes that list for the extension, and no `models.json` row. The model definitions it selects from are the ones yolo's extension registers
([§2.2](#22-pi-pi-coding-agent)), so with no `models.json` row pi uses its own
`openai-codex-responses` client and the broker-backed OAuth.

### 6.2 Test Composition Alignment

Built 2026-09-27. [R2](#8-risks-and-invariants)'s helper is `testPacksForAgent(t, agent, extra...)` in
[`internal/entrypoint/packclosure_test.go`](../../internal/entrypoint/packclosure_test.go). It
returns the named packs plus everything `packload.Selection.Close` adds for them, which is the
resolver the launch runs over its loaded packs (`internal/cli/run/packs.go`), drawing from the
embedded official set. No profile is selected at closure time, so the via half adds nothing. That
matches the launch for every profile these fixtures render, since only a via profile adds a pack.
`TestTestPacksForAgentResolvesTheNeedsClosure` pins it against the shipped manifests' `needs`.

`TestPiCodexProfileSelectsBuiltInProviderAndExplicitModel` composes `testPacksForAgent(t, "pi")`,
which is pi and `openai-auth`, and first asserts that the composed table holds `openai-codex`, so
the shadow assertion cannot pass vacuously again. The other pi and omp fixtures that composed a
hand-listed pack set use the helper too. **Revert-checked:** with the exclusion removed from
`packs/pi/derive.lua`, the edited test fails with `models.json shadows Pi's built-in openai-codex
provider`, and the pre-edit test passes. With pi's `needs` removed, the helper's own test,
`TestPiCatalogFromShippedPacksOmitsOpenAICodex` and the edited test fail.

This does not duplicate `TestPiCatalogFromShippedPacksOmitsOpenAICodex`. That test renders with no
profile selected. This one renders with the codex profile active, so `openai-codex` is the selected
provider and the settings branch runs in the same render.

### 6.3 Relation to credential scoping

Excluding `openai-codex` from `models.json` prevents Pi from switching to `openai-responses` and
falling back to `OPENAI_API_KEY`. However, the broader issue of ambient environment leakage
remains tracked in [`providers.md`'s credential gate](../reference/providers.md#the-credential-gate). When a profile
selects a subscription provider, ambient platform keys for the same vendor should ideally be scoped
or masked to prevent tools or subagents from inadvertently picking them up.

---

## 7. Non-goals

- **Not modifying `openai-auth-broker`:** The broker's daemon protocol, lock files, and token
  refresh loops are unaffected.
- **Not removing the wire-bridge endpoint from `packs/openai-auth`:** Other agents (e.g. Claude
  Code via `wire-bridge`) rely on the `openai-responses` endpoint declaration to target ChatGPT's
  backend.
- **Not redesigning Pi's OAuth extension:** `packs/pi/extensions/yolo-openai-auth.js` registers
  `openai-codex` with Pi's own `openai-codex-responses` wire and the broker-backed OAuth
  ([§2.2](#22-pi-pi-coding-agent)), and nothing here changes it.

---

## 8. Risks and invariants

| Risk | Consequence | Mitigation |
| :--- | :--- | :--- |
| **R1. Pi cannot reach custom Codex proxies** | A user configuring a private reverse proxy for Codex via `providers.openai-codex.endpoints` would have their URL ignored if `openai-codex` is unconditionally skipped. | If custom endpoint overrides for Codex are ever needed in Pi, they must specify `wire_api: "openai-codex-responses"` or be routed through a distinct provider name. Built-in subscription providers must not be repurposed as custom endpoints. |
| **R2. Test pack omission re-occurs** | A future agent test might omit required dependencies and miss catalog collisions. | The helper `testPacksForAgent(agent)` resolves pack `needs` so unit tests test the full closure that production runs. Built 2026-09-27 on the launch's own resolver ([§6.2](#62-test-composition-alignment)). |

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

   <!-- vantage: oq id=OQ-1 -->

   **Answer:**
   > **Name exclusion for v1**, ruled in review 2026-09-26: *"to match Codex CLI, deferring schema
   > changes until another subscription provider exists."* `packs/pi/derive.lua` skips `openai-
   > codex` when it builds `models.json`, exactly as `packs/codex/derive.lua` does. A provider-
   > level flag waits until a second subscription provider exists (it is the same question as [OQ-
   > BR2](providers-and-profiles-redesign.md#OQ-BR2)'s marker). Built in `92c20cc6`.

2. ✅ <a id="OQ-2"></a>**OQ-2: Packaging of inter-agent adaptation endpoints.** Does `packs/openai-auth` legitimately
   own `endpoints["openai-responses"]` on `openai-codex`, or should inter-agent adapter targets
   be declared in a separate namespace or contribution that catalog derives ignore?


   _Leaning:_ Keep the endpoint on `openai-codex` in `packs/openai-auth`. The endpoint declaration
   is factually true: ChatGPT's backend does expose an `openai-responses` wire API at that URL. The
   architectural defect was not declaring the endpoint; it was Pi's derive assuming that any declared
   endpoint must be cataloged in `models.json`, even for providers Pi implements natively.

   <!-- vantage: oq id=OQ-2 -->

   **Answer:**
   > **Keep the endpoint on `openai-codex` in `packs/openai-auth`, and establish the rule**, ruled
   > in review 2026-09-26 as leaned: *"Keep the endpoint on openai-codex in packs/openai-auth, but
   > establish the rule that an agent never derives catalog entries from providers it natively
   > implements."* The declaration is true and stays; the defect was a derive cataloging a provider
   > its agent already implements. Every agent derive was checked against the rule for
   > `openai-codex`: omp had the same defect, fixed in `4ed48212` (omp applies a `models.yml` row's
   > `baseUrl` to its built-in provider of that name); claude, copilot, agy and opencode catalog
   > nothing that shadows a native subscription client. How far "natively implements" reaches past
   > that case is [OQ-3](#OQ-3).

3. 💬 <a id="OQ-3"></a>**OQ-3: How far does the natively-implements rule reach?**
   [OQ-2](#OQ-2) ruled that an
   agent never derives catalog entries from providers it natively implements. Does that cover
   **every** provider an agent ships its own client for under the key a yolo provider uses (the
   broad reading)? Or does it cover only a **subscription** provider the agent implements with its
   own client and login (the narrow reading), which is how [P1](#1-verdict-and-core-principles)'s
   example describes it? The two readings agree on `openai-codex`, the only shipped provider the
   narrow reading reaches today. The broad reading also changes four other providers' rows in three
   agents, and it decides [OQ-WG8](wire-bridge-gateway.md#OQ-WG8), which defers to this rule.

   <!-- vantage: oq id=OQ-3 leaning="The narrow reading: only a subscription provider the agent implements with its own client and login. The harm P1 names needs a subscription client displaced by a key-driven wire; the broad reading would take opencode's zai off the coding plan, turn off via for every same-named provider and forbid OQ-WG8's override, while fixing no reported failure. It narrows the ruled words, so it is for the maintainer to confirm or overrule." -->

   **What yolo writes today.** Composed from the shipped packs, pi's, omp's and opencode's derives
   each write a catalog row for `zai`, `cerebras`, `openrouter`, `kilo` and `llamacpp`. Which of
   those keys the agent also implements itself, read from each agent's installed code (pi 0.87.1,
   oh-omp 0.15.3, opencode 1.18.32; none of them run):

   | Agent | Its own provider under the same key | Not one of its own |
   | :--- | :--- | :--- |
   | pi | `zai`, `cerebras`, `openrouter` | `kilo`; `llamacpp` (pi's is keyed `llama.cpp`) |
   | omp | `zai`, `cerebras`, `openrouter`, `kilo` | `llamacpp` (omp's is keyed `llama.cpp`) |
   | opencode | `zai`, `cerebras`, `openrouter`, `kilo` | `llamacpp` |

   **Under the broad reading**, each derive stops writing the rows in the middle column:

   - **pi** keeps the credential for all three: its own clients read the variables yolo's rows name
     (`ZAI_API_KEY`, `CEREBRAS_API_KEY`, `OPENROUTER_API_KEY`). What moves is the rest of each row.
     Pi's own model lists replace yolo's (pi's `zai` has no `glm-4.6`, which the settings derive
     still names in `enabledModels`), and yolo's context windows and model options stop reaching
     pi. `openrouter` also goes back from the `openai-responses` wire yolo's row sets to pi's own
     chat-completions client.
   - **omp** loses four rows. Its own `zai` speaks Anthropic Messages at `api.z.ai/api/anthropic`,
     where yolo's row points it at the chat-completions coding endpoint, and its own `openrouter`
     speaks chat completions where yolo's row sets Responses.
   - **opencode** loses four rows, and `zai` changes what a zai profile buys. opencode's own `zai`
     reads `ZHIPU_API_KEY` and calls the metered `api.z.ai/api/paas/v4`. yolo's row sends
     `ZAI_API_KEY` to the coding plan's `api.z.ai/api/coding/paas/v4`. A jail with only
     `ZAI_API_KEY` set would have no zai credential in opencode at all.
   - **Via stops working for every one of those providers in that agent.** A via row is a catalog
     row under the provider's own key, and pi and omp already drop it for `openai-codex` under this
     rule. A via profile selecting any of them would re-point nothing, which the launch discloses
     as a via with no effect.
   - **[OQ-WG8](wire-bridge-gateway.md#OQ-WG8)'s override is forbidden.** A `baseUrl` on pi's
     built-in `amazon-bedrock` is a `models.json` row for a provider pi implements natively. WG8's
     options (a) and (b) both put the re-pointing there. So under this reading WG8 needs its row
     under a key pi does not implement, where whether pi's Converse client still serves it is
     unverified, or its option (c).
   - **The exclusion list is per agent and per version**, because each agent's built-in set moves
     with its releases. Keeping it true is the provider marker [OQ-1](#OQ-1) deferred.

   **Under the narrow reading**, nothing built changes. `openai-codex` stays excluded in pi, omp
   and codex, and every row above is kept, via rows included. [OQ-WG8](wire-bridge-gateway.md#OQ-WG8)'s override is allowed, since
   pi's `amazon-bedrock` is no subscription: it authenticates with a bearer token or the AWS
   credential chain. The reading has one edge to state. pi's `openrouter` has an OAuth sign-in of
   its own and omp's `kilo` a device-code sign-in, and neither is a subscription, so both stay
   catalogued. If a sign-in of its own were the test instead of a subscription, those two rows
   would go.

   _Leaning:_ **The narrow reading.** It is narrower than the words ruled, so it is offered for the
   maintainer to confirm or overrule, and nothing is built on it until then. The harm
   [P1](#1-verdict-and-core-principles) was written against is a subscription client displaced by
   a key-driven wire that then finds an ambient platform key. A same-named metered provider's row
   carries the same kind of credential the agent's own client reads, an API key, so that harm
   cannot arise there. The broad reading would take away behavior the shipped packs rely on, above
   all opencode's zai coding plan. It would also turn off via for every same-named provider and
   forbid [OQ-WG8](wire-bridge-gateway.md#OQ-WG8)'s shape, and it fixes no reported failure.

   **Answer:**
   > _(empty — fill in when decided)_

---

## 10. Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-1 | **Exclude `openai-codex` from pi's catalog by name**, matching `packs/codex/derive.lua`; a provider flag waits for a second subscription provider ([OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2)'s marker) | 2026-09-26 | [OQ-1](#OQ-1) | yes, 2026-09-26 (`92c20cc6`) |
| OQ-2 | **Keep the `openai-responses` endpoint on `openai-codex`**, and the rule: an agent never derives catalog entries from providers it natively implements. How far that reaches past `openai-codex` is [OQ-3](#OQ-3) | 2026-09-26 | [OQ-2](#OQ-2) | for `openai-codex`, 2026-09-26 (omp `4ed48212`; the other derives needed nothing). opencode, which had no route to the subscription then, gained one 2026-10-01 under the rule: no row for `openai-codex`, a via row included, and its own built-in `openai` client, with the list as model rows of that provider and no `npm` or `baseURL` (`TestOpencodeOnCodexRendersItsOwnOpenAIProvider`) |
