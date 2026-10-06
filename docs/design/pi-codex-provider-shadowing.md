---
title: "Why a bridge endpoint shadowed Pi's Codex provider — and how ambient keys took over"
date: 2026-09-25
status: in-review
stage: DESIGN
next: "Rule OQ-4: whether the ruling reaches a list a pack declares for a provider an agent has built in (the openai-codex list, a pack's Bedrock list, and a `models` only on zai and the like, which the 2026-10-05 build of OQ-3 left as they were). Pi's Converse route (wire-bridge-gateway.md WG-I36) may no longer sit on amazon-bedrock"
tags: [providers, codex, pi, openai-auth, shadowing, credentials]
summary: "Adding an openai-responses endpoint to the openai-codex provider allowed wire-bridge to route to ChatGPT, but caused Pi's derive to shadow its built-in subscription provider with a third-party models.json row. When Pi treated openai-codex as a generic OpenAI platform endpoint, it picked up the workspace's ambient OPENAI_API_KEY, resulting in 401 errors against the Codex backend. On 2026-10-05 the maintainer ruled that the rule this led to covers every provider an agent has built in: yolo writes no model entry over any of them and keeps only their names. That is built: pi's, omp's and opencode's packs declare their own providers, and their derives write no row under one."
---

# Why a bridge endpoint shadowed Pi's Codex provider — and how ambient keys took over

**Status:** 2026-10-05 — [OQ-3](#OQ-3) is ruled, the broad reading: yolo writes no model entry
over any provider an agent has built in, and the agent uses its own list. **Built the same day**
([§6.5](#65-what-the-build-decided), [BI-D1](#BI-D1) to [BI-D10](#BI-D10)): the pi, omp and
opencode packs name their own providers in `built_in_providers`, their derives write no row under
one, via rows included, and opencode reaches z.ai's coding plan through its own `zai-coding-plan`.
MEASURED by unit tests through the boot render, revert-checked against the call sites and the
lists, and by integration launches; UNMEASURED: no pi, omp or opencode session was run, so no
request reached a provider. [OQ-4](#OQ-4), filed the same day, asks whether that reaches a list a
pack declares for such a provider, which two other rulings let land there. The `openai-codex` exclusion is built, 2026-09-26, at `92c20cc6` (pi) and `4ed48212` (omp); codex already excluded it. Measured through the boot render by the catalog tests, which compose each agent's `needs` closure since 2026-09-27 ([§6.2](#62-test-composition-alignment)), and for pi through a `-p codex` integration launch since 2026-10-01, whose rendered `models.json` holds no `openai-codex` row ([the plan's §5](pi-codex-provider-shadowing-plan.md#5-verification-checklist)); no live pi or omp has run it. Evidence verified at `c5bab09b`; pi's and claude's codex handling re-verified at `4dcebd18`.

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
the boundary between subscription OAuth tokens and ambient platform API keys. Since
[OQ-3](#OQ-3) (2026-10-05) the filter covers every provider an agent has built in, in every
agent's derive.

**Cost.** The `openai-codex` exclusion cost none. [OQ-3](#OQ-3)'s broad reading changes what pi,
omp and opencode do on `zai`, `cerebras`, `openrouter` and `kilo`: each uses its own model list,
and a profile routing one of them through the wire bridge stops doing so in that agent
([what the ruling settles](#what-the-ruling-settles)).

**Start at [§3](#3-the-mechanism-of-shadowing-how-modelsjson-overrode-pis-native-client)** — how the shadow happened. The rest falls out of it.

**Needs your ruling:** [OQ-4](#OQ-4) (**new**, 2026-10-05): does the ruling reach a list a pack
declares for a provider an agent has built in? [OQ-3](#OQ-3) was ruled in review on 2026-10-05:
the rule covers every provider an agent has built in.

**Releases:** pi's Converse route through the wire bridge. [OQ-WG8](wire-bridge-gateway.md#OQ-WG8)
was decided on 2026-09-30 as [WG-I36](wire-bridge-gateway.md#WG-I36), which left where pi's
override row lives to [OQ-3](#OQ-3). Under the ruling it may not sit on pi's built-in
`amazon-bedrock`, so the route puts its row under a key pi does not implement, which nobody has
checked pi's Converse client still serves, or it is not built.

**Reads with:** [`pi-codex-provider-shadowing-plan.md`](pi-codex-provider-shadowing-plan.md) (the companion sketch — incomplete while questions are open),
[`providers.md`'s credential gate](../reference/providers.md#the-credential-gate) (the ambient environment delivery boundary),
[`../reference/providers.md`](../reference/providers.md) (the provider declaration and derivation reference).

---

## Terms, in plain words

- **Built-in provider.** A provider an agent ships its own client and model list for, under its
  own key: `zai`, `openai-codex` and `amazon-bedrock` in pi, for example. [OQ-3](#OQ-3) ruled that
  yolo writes no model entry over one.
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
  subscription one. Which of the two the rule means was [OQ-3](#OQ-3), ruled on 2026-10-05: every
  natively implemented provider.
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

### 6.4 What each reading of the rule changes

Moved here verbatim from [OQ-3](#OQ-3), which asked which reading of [OQ-2](#OQ-2)'s rule holds.
It ruled the broad reading on 2026-10-05; [what the ruling settles](#what-the-ruling-settles)
follows the two readings, and replaces the broad reading's costs where the two differ.

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

<a id="what-the-ruling-settles"></a>**What the ruling settles** (2026-10-05, the broad reading).
Read 2026-10-05 from the copies installed in this jail, not run: pi 1.0.1 and opencode 1.18.34.

- **The lists.** Each agent uses its own model list for a provider it has built in. yolo keeps
  only the names of each agent's built-in providers, and each agent's own pack declares them,
  since core knows no agent. No derive writes a row under one of those names, a via row included,
  and nothing yolo writes for that provider names a model from yolo's list, pi's `enabledModels`
  included.
- **The plan's address.** Where a profile's plan needs a different address from the agent's
  built-in provider, yolo selects the agent's own provider for that plan when the agent has one,
  and when it has none, the launch says the profile cannot reach that agent's own client.
  - pi 1.0.1's own `zai` already calls the coding plan (`api.z.ai/api/coding/paas/v4`), with
    `zai-coding-cn` beside it.
  - opencode 1.18.34 ships `zai-coding-plan` (`api.z.ai/api/coding/paas/v4`) beside its metered
    `zai`, and it reads `ZHIPU_API_KEY`, so a zai profile selects it there, and the credential
    has to reach opencode under that name.
  - omp's own `zai` speaks Anthropic Messages at `api.z.ai/api/anthropic`; whether that address
    serves the coding plan is the build's to read.
- **Routing through the wire bridge stops for those providers** in that agent: a via profile
  selecting one re-points nothing, which the launch discloses as a via with no effect.
- **[OQ-WG8](wire-bridge-gateway.md#OQ-WG8)'s override may not sit on pi's `amazon-bedrock`**:
  [WG-I36](wire-bridge-gateway.md#WG-I36)'s Converse route needs a key pi does not implement, or
  is not built.
- **The name list goes stale** when an agent adds a provider: until its pack's list names the new
  one, yolo writes over it, which is the original failure, only rarer
  ([R3](#8-risks-and-invariants)).
- **Two lists already land on a built-in provider, and the ruling's words conflict with what
  placed them**, so [OQ-4](#OQ-4) asks:
  - the `openai-codex` list, the one list every consumer renders
    ([ML-D1](model-lists-and-pickers.md#ML-D1)). In pi, yolo's extension registers it over pi's
    built-in `openai-codex`, and opencode gets it as model rows of its built-in `openai`
    ([OQ-2](#10-decision-ledger)'s ledger row);
  - a pack's own Bedrock list on pi's built-in `amazon-bedrock` (pi's native Bedrock row, models
    only), which [MM-D32](model-lists-and-pickers.md#MM-D32), ruled the same day, allows: *"a pack
    may narrow or replace"* an agent's own Bedrock catalog. yolo ships no Bedrock list since
    MM-D32, so the row is written only when a pack supplies one.

### 6.5 What the build decided

Built 2026-10-05 on the ruling's words; the decisions it took are [BI-D1](#BI-D1) to
[BI-D10](#BI-D10) in the ledger, each an implementation decision the maintainer may overrule, and
[BI-D11](#BI-D11) and [BI-D12](#BI-D12) correct two of them after review.
Agent facts were read from the copies the build had, never run: pi 1.0.1 and opencode 1.18.34
installed in the jail, and oh-omp 0.15.3's linux-x64 binary fetched with `npm pack`.

- **The declaration** ([BI-D1](#BI-D1), [BI-D2](#BI-D2)). A program's `built_in_providers` holds
  `names`, every provider id the agent's own code registers, and `plans`, which maps a yolo
  provider to the agent's own provider for its plan, with the variable that provider reads, or to
  `null` when the agent has none of its own for it. Shipped: pi 43 names, oh-omp 49, opencode 225;
  opencode's `plans` map yolo's zai to `zai-coding-plan` (reading `ZHIPU_API_KEY`) and
  `openai-codex` to its own `openai`.
- **Who reads it** ([BI-D3](#BI-D3)). Core, three ways: every derive's `ctx.built_in_providers`,
  the launch's profile line, and the agent's environment. The derives check membership where they
  checked `openai-codex` by name, which stays as a fallback for a ctx without the table.
- **What the agent is handed** ([BI-D4](#BI-D4) to [BI-D7](#BI-D7)). No row and no via row; a
  selection naming the agent's own id and, from yolo's list, the profile's `model` option alone; no
  role variable; and the provider's key under the agent's own variable name, a re-pointed one
  included ([BI-D12](#BI-D12)). `openai-codex` on pi and opencode, which run yolo's list on their
  own client, keeps its tiers and its endpoint line (`yolo_lists`, [BI-D11](#BI-D11)).
- **What waits on [OQ-4](#OQ-4)** ([BI-D8](#BI-D8)). The two lists named there are as they were, and
  so is a list a `models` contribution narrows with `only` on a built-in provider, which still
  narrows through each agent's list channel without a catalog row.
- **What is not built.** codex declares no list ([BI-D9](#BI-D9)), and nothing keeps the lists true
  across an agent's releases (R3).

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
| **R3. An agent's built-in provider list goes stale** | Under [OQ-3](#OQ-3)'s ruling each agent pack declares its built-in provider names, and an agent release adding one leaves the list short. Until it is updated, yolo writes its own row over the new built-in, which is the original shadowing failure, only rarer. | The trap was stated with the ruling. The build read each list from the agent's own code and wrote where each came from in its `pack.json` comment ([BI-D2](#BI-D2)); a missed name costs only a collision with a provider of that name. Nothing yet keeps the lists true across releases: pi's `model_catalog` files are named by provider id, so `yolo check` could compare them, unbuilt. |

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

   <!-- vantage: question id=OQ-1 -->

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

   <!-- vantage: question id=OQ-2 -->

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

3. ✅ <a id="OQ-3"></a>**OQ-3: How far does the natively-implements rule reach?**
   [OQ-2](#OQ-2) ruled that an
   agent never derives catalog entries from providers it natively implements. Does that cover
   **every** provider an agent ships its own client for under the key a yolo provider uses (the
   broad reading)? Or does it cover only a **subscription** provider the agent implements with its
   own client and login (the narrow reading), which is how [P1](#1-verdict-and-core-principles)'s
   example describes it? The two readings agree on `openai-codex`, the only shipped provider the
   narrow reading reaches today. The broad reading also changes four other providers' rows in three
   agents, and it decides [OQ-WG8](wire-bridge-gateway.md#OQ-WG8), which defers to this rule.

   What each reading changes, agent by agent:
   [§6.4](#64-what-each-reading-of-the-rule-changes).

   _Leaning:_ **The narrow reading.** It is narrower than the words ruled, so it is offered for the
   maintainer to confirm or overrule, and nothing is built on it until then. The harm
   [P1](#1-verdict-and-core-principles) was written against is a subscription client displaced by
   a key-driven wire that then finds an ambient platform key. A same-named metered provider's row
   carries the same kind of credential the agent's own client reads, an API key, so that harm
   cannot arise there. The broad reading would take away behavior the shipped packs rely on, above
   all opencode's zai coding plan. It would also turn off via for every same-named provider and
   forbid [OQ-WG8](wire-bridge-gateway.md#OQ-WG8)'s shape, and it fixes no reported failure.

   **Answer:**
   > **Ruled in review 2026-10-05: the broad reading, against the leaning above.** The maintainer
   > first asked *"why wouldn't it cover all? we don't want to maintain model catalogs."*, and on
   > the question restated with the broad reading as its (A) and new leaning, answered *"168 A"*.
   > yolo writes no model entry over any provider an agent has built in, pi, omp and opencode
   > included, and the agent uses its own list. Where a profile's plan needs a different address
   > from the agent's built-in provider (z.ai's coding plan, against its pay-per-use API), yolo
   > selects the agent's own provider for that plan when the agent has one; when it has none, the
   > launch says the profile cannot reach that agent's own client. Each agent pack declares the
   > names of its built-in providers, since core knows no agent: yolo keeps names, never models.
   > Routing those providers through yolo's wire bridge stops for them. The leaning's two costs are
   > met that way: opencode's zai stays on the coding plan through opencode's own
   > `zai-coding-plan`, and via stops by the ruling's own terms. What it settles, agent by agent,
   > and the two lists it leaves to [OQ-4](#OQ-4): [what the ruling settles](#what-the-ruling-settles).

4. 💬 <a id="OQ-4"></a>**OQ-4: Does the ruling reach a list a pack declares for a provider an agent
   has built in?** Filed 2026-10-05 by ruling [OQ-3](#OQ-3). The two lists and what placed them:
   [what the ruling settles](#what-the-ruling-settles).

   - **(A) Yes, every list.** pi and opencode show their own codex catalogs; a pack's Bedrock
     list reaches only an agent with no Bedrock catalog. *Cost:* amends
     [ML-D1](model-lists-and-pickers.md#ML-D1), and narrows
     [MM-D32](model-lists-and-pickers.md#MM-D32)'s *"a pack may narrow or replace"*.
   - **(B) No, only a yolo provider's row under a built-in key.** *Cost:* yolo keeps maintaining
     the `openai-codex` list.
   - **(C) Only a list a pack declares as an override.** The `openai-codex` list stops, as in (A).
     *Cost:* the composed provider entry must record whether a list is an override.

   <!-- vantage: question id=OQ-4 leaning="(C): it keeps both of the maintainer's 2026-10-05 sentences as said, 'we don't want to maintain model catalogs' (OQ-3) and 'allow packs to override that if needed' (MM-D32), where (A) narrows the second to agents without a catalog and (B) gives up the first. The openai-codex list's loss costs pi only the [1m] variants, since pi 1.0.1's own catalog lists the three GPT-6 ids." -->

   _Leaning:_ **(C)** — it keeps both of the maintainer's 2026-10-05 sentences as said: *"we don't
   want to maintain model catalogs"* ([OQ-3](#OQ-3)) and *"allow packs to override that if
   needed"* ([MM-D32](model-lists-and-pickers.md#MM-D32)), where (A) narrows the second to agents
   without a catalog and (B) gives up the first. The `openai-codex` list's loss costs pi only
   yolo's `[1m]` variants, since pi 1.0.1's own catalog lists the three GPT-6 ids. ⚠ What
   opencode's own `openai` catalog lists for the subscription is unread.

   **Answer:**
   > _(empty — fill in when decided)_

---

## 10. Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| OQ-1 | **Exclude `openai-codex` from pi's catalog by name**, matching `packs/codex/derive.lua`; a provider flag waits for a second subscription provider ([OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2)'s marker). ⚠ Widened 2026-10-05 by [OQ-3](#OQ-3): the name check becomes a list of built-in provider names each agent pack declares | 2026-09-26 | [OQ-1](#OQ-1) | yes, 2026-09-26 (`92c20cc6`) |
| OQ-2 | **Keep the `openai-responses` endpoint on `openai-codex`**, and the rule: an agent never derives catalog entries from providers it natively implements. How far that reaches past `openai-codex` is [OQ-3](#OQ-3) | 2026-09-26 | [OQ-2](#OQ-2) | for `openai-codex`, 2026-09-26 (omp `4ed48212`; the other derives needed nothing). opencode, which had no route to the subscription then, gained one 2026-10-01 under the rule: no row for `openai-codex`, a via row included, and its own built-in `openai` client, with the list as model rows of that provider and no `npm` or `baseURL` (`TestOpencodeOnCodexRendersItsOwnOpenAIProvider`) |
| OQ-3 | **Ruled in review, the broad reading:** yolo writes no model entry over any provider an agent has built in (pi, omp and opencode included), and the agent uses its own list. A profile whose plan needs another address selects the agent's own provider for that plan when it has one, and the launch says when it has none. Each agent pack declares its built-in provider names; yolo keeps names, never models. Routing those providers through the wire bridge stops for them, and [WG-I36](wire-bridge-gateway.md#WG-I36)'s row may not sit on pi's `amazon-bedrock`. Widens [OQ-1](#OQ-1)'s name check into the packs' lists. The two pack-declared lists on a built-in provider are [OQ-4](#OQ-4)'s | 2026-10-05 | [OQ-3](#OQ-3), [what the ruling settles](#what-the-ruling-settles) | yes, 2026-10-05, as [BI-D1](#BI-D1) to [BI-D10](#BI-D10) ([§6.5](#65-what-the-build-decided)): `TestNoAgentCataloguesAProviderItHasBuiltIn`, `TestPiTakesItsOwnZaiWithNothingFromYolosList`, `TestOpencodeTakesZaisPlanThroughItsOwnCodingPlanProvider`, `TestOmpOnZaiRunsItsOwnZai` (boot render), `TestShippedDerivesHonorTheAgentsOwnProviders` (the derives' `false` branch), `TestAProfileOnAPlanTheAgentHasNoProviderOfItsOwnForSaysSo` (the launch line), `TestTheGateRelaysThePlansKeyAndNoTierOnABuiltInProvider`, `TestShippedViaOverABuiltInProviderRepointsNothing`; at a launch, `TestPiAndOpencodeSelectionFollowTheActiveProfile` |
| <a id="BI-D1"></a>BI-D1 | *Implementation decision, building [OQ-3](#OQ-3).* **The declaration is a program's `built_in_providers` field** (`internal/packdecl/builtinproviders.go`), not the provider/profile machinery: which providers a binary implements is that binary's fact, as `platform_switches` and `exact_menu_refuses` are, and core must read it for the launch line, which a derive cannot print. A provider-level flag would have yolo's provider state facts about agents. It holds `names` and `plans`: a plan maps a yolo provider name to the agent's own provider for that plan, with `api_key_env_name` when that provider reads another variable, or to `null` when the agent has the name built in for another plan and none of its own for this one. A plan's provider must be one of the names; the field is refused on any other kind and on a fork, whose base keeps it | 2026-10-05 | [§6.5](#65-what-the-build-decided) | ✅ `TestBuiltInProvidersIsAProgramsFact`, `TestBuiltInProvidersIsRefusedWhereNothingCouldReadIt` |
| <a id="BI-D2"></a>BI-D2 | *Implementation decision.* **Each list holds every provider id the agent's own code registers**, not only the ones a shipped provider shares, so a user's provider of such a name is caught too, as the plan's step 1 offered. Read 2026-10-05, never run: pi 1.0.1's `builtinProviders()` and its `llama.cpp` extension (43), oh-omp 0.15.3's bundled `models.json` and key map (49), and the models snapshot in opencode 1.18.34's binary (225; opencode also fetches models.dev at run time). Each `pack.json` says where its list came from. The cost is three lists to refresh with each agent's releases; a stale one misses only a collision ([R3](#8-risks-and-invariants)) | 2026-10-05 | [§6.5](#65-what-the-build-decided) | ✅ `packs/pi/pack.json`, `packs/omp/pack.json`, `packs/opencode/pack.json`; the shipped names [§6.4](#64-what-each-reading-of-the-rule-changes)'s table lists are pinned by `TestNoAgentCataloguesAProviderItHasBuiltIn`, which fails when one is dropped |
| <a id="BI-D3"></a>BI-D3 | *Implementation decision.* **Core reads the declaration by bin ownership and hands each derive the answer** as `ctx.built_in_providers`, keyed by yolo provider name (`{ id, api_key_env_name }`, or `false` for a `null` plan), on every path that runs a derive: the boot and `yolo check` (`surfaceSelectionFor`), the via-pointer scan (`DerivedViaPointers`), and the env derive. The derives test membership where they tested `openai-codex` by name, and keep that name as a fallback, so an entrypoint older than the table still keeps the subscription client unshadowed | 2026-10-05 | [§6.5](#65-what-the-build-decided) | ✅ revert-checked: deleting the boot's or the scan's call site, or a derive's check, fails the tests the [OQ-3](#OQ-3) row names |
| <a id="BI-D4"></a>BI-D4 | *Implementation decision.* **From yolo's list, only the profile's `model` option reaches a built-in provider, as the agent's own id**, never resolved through yolo's alias table, so `default` and any other alias name nothing. pi gets `defaultProvider` and that `defaultModel`, `enabledModels` of `<id>/*` and a pi-subagents scope of `<id>/*`: pi takes the saved pair only whole (`findInitialModel`, pi 1.0.1), and with nothing it starts on the first provider it finds a login for, which may be another; the scope keeps a fresh session on the provider, its first model when the pair does not resolve. opencode gets `model` and `small_model` only from the profile's own options, and `enabled_providers` naming its own id; oh-omp nothing, as before. ⚠ So the shipped zai profile still names `glm-5.3`, packs/zai's declared default, which pi's own zai lists; cerebras's declared `default` names nothing, and pi starts on its own cerebras scope. Whether even that one id should go is the maintainer's to say | 2026-10-05 | [§6.5](#65-what-the-build-decided) | ✅ `TestPiTakesItsOwnZaiWithNothingFromYolosList`, `TestOpencodeTakesZaisPlanThroughItsOwnCodingPlanProvider`, `TestPiSubagentsBlockFollowsEveryProvidersProfile` |
| <a id="BI-D5"></a>BI-D5 | *Implementation decision, the ruling's "the agent's own provider for that plan".* **opencode's yolo zai is its own `zai-coding-plan`, and the launch delivers zai's key to it as `ZHIPU_API_KEY`**, the variable that provider reads (opencode's own `zai` is the metered `api.z.ai/api/paas/v4`). Core composes it from the plan's `api_key_env_name` in `AgentEnv`, so it rides the agent's own env file through the credential gate, for every entry of an active set; the agent's own derive still wins a name it sets. It counts as a relay for the launch-shell pre-flight, so a zai key left only in the launching shell now reaches opencode. pi's own zai already calls the coding plan, and oh-omp's speaks Anthropic Messages at `api.z.ai/api/anthropic`, the plan's Anthropic route ([zai-plumbing.md](../reference/zai-plumbing.md)), both reading `ZAI_API_KEY`, so neither needs a plan | 2026-10-05 | [§6.5](#65-what-the-build-decided) | ✅ `TestTheGateRelaysThePlansKeyAndNoTierOnABuiltInProvider`, `TestAKeyOnlyInTheLaunchShellCountsOnlyForAnAgentWhoseDeriveRelaysIt`; at a launch, `TestOpencodeRunsOnEveryProviderOfItsSet` |
| <a id="BI-D6"></a>BI-D6 | *Implementation decision.* **No `YOLO_MODEL_<ROLE>` variable is composed for an agent on a built-in provider**: its tiers are composed as if the selection named none, which removes the ones another provider of the table names. A role variable is `<yolo name>/<yolo's id>`, a model from yolo's list, and on opencode's zai it would name a provider opencode knows as `zai-coding-plan` | 2026-10-05 | [§6.5](#65-what-the-build-decided) | ✅ `TestTheGateRelaysThePlansKeyAndNoTierOnABuiltInProvider` |
| <a id="BI-D7"></a>BI-D7 | *Implementation decision, the ruling's "the launch says".* **The profile line names the agent's own client, and a `null` plan is a warning naming the next step**: `through its own "<id>" client, with its own model list`, and for a plan the agent has none of its own for, that the profile reaches nothing for that agent, with `-p <agent>=<name>` or none. The via route gate's no-effect notice says which rule it is and how to route the provider anyway: declare it under `providers` with a name the agent has no provider of | 2026-10-05 | [§6.5](#65-what-the-build-decided) | ✅ `TestTheProfileLineSaysAnAgentReachesItsOwnProvider`, `TestAProfileOnAPlanTheAgentHasNoProviderOfItsOwnForSaysSo`, `TestShippedViaOverABuiltInProviderRepointsNothing` |
| <a id="BI-D8"></a>BI-D8 | *Implementation decision, holding [OQ-4](#OQ-4).* **The lists [OQ-4](#OQ-4) names stay as they were, and so does a list a `models` contribution narrows with `only` on a built-in provider**, which still narrows through each agent's list channel and never a catalog row: pi's extension registration, now naming no api, so the extension reads pi's own catalog and a list spanning two of pi's apis (OpenRouter's Anthropic models beside others) is the menu alone, without the refusal ([MM-D21](model-lists-and-pickers.md#MM-D21)); oh-omp's scope under its own id; and opencode's `whitelist` on its own id, alone, for a provider that names an address, so an endpoint-less first-party provider stays unnarrowed as `yolo check` says | 2026-10-05 | [§6.5](#65-what-the-build-decided) | ✅ `TestOpencodeWhitelistsANarrowedList`, `TestPiGetsANarrowedListToRegister`, `TestPiNarrowedOpenRouterListNamesItsRowsOneAPI`, `TestTheUnnarrowedMenuLineAgreesWithOpencodesWhitelist` |
| <a id="BI-D9"></a>BI-D9 | *Implementation decision.* **codex declares no list in this build** and keeps its by-name `openai-codex` check, the original rule, byte for byte. Which of codex's own providers a yolo provider could share a name with was not read, so declaring codex's list, and reading it, is a later build's. claude, copilot and agy write no catalog row | 2026-10-05 | [§6.5](#65-what-the-build-decided) | — |
| <a id="BI-D10"></a>BI-D10 | *Implementation decision.* **The tests that used zai, cerebras or openrouter as a catalogued provider moved to one no agent has built in**: the fixtures' `zhipu`, the user's `glm`, and the shipped kilo and llamacpp, so the selection, set and catalog mechanics stay pinned for a catalogued provider, while the shipped pairings are pinned at the new behavior | 2026-10-05 | [§6.5](#65-what-the-build-decided) | ✅ |
| <a id="BI-D11"></a>BI-D11 | *Implementation decision, correcting [BI-D6](#BI-D6) and [BI-D7](#BI-D7).* **A built-in provider whose list the agent's pack renders from yolo's declaration is named in `built_in_providers.yolo_lists`, and keeps its tiers and its endpoint line.** pi and opencode run `openai-codex` on their own client, but on the one list `packs/openai-auth` declares ([ML-D1](model-lists-and-pickers.md#ML-D1)): pi's extension registers it, and opencode's `opencodeCodexRow` writes it as the models and whitelist of its own `openai`. So BI-D6's reason, a model from yolo's list the agent's own may lack, does not hold there, and BI-D7's "with its own model list" was untrue. Both packs list `openai-codex` there; omp, on its own list, does not. The derives are unchanged | 2026-10-06 | [§6.5](#65-what-the-build-decided) | ✅ `TestTheCodexListStaysYolosForAnAgentThatRendersIt`, `TestYoloListsNamesTheBuiltInProvidersOnYolosList` |
| <a id="BI-D12"></a>BI-D12 | *Implementation decision, extending [BI-D5](#BI-D5).* **A key the user re-points is relayed under the variable the agent's own provider reads**: the plan's `api_key_env_name`, else the one variable the shipped provider declares. With no row written there is no `${OR_KEY}` reference left, so `providers.openrouter.api_key_env_name = "OR_KEY"` would otherwise leave pi's own openrouter, which reads only `OPENROUTER_API_KEY`, with no key. A literal `api_key` or `options.api_key` is not relayed, a literal key in the table being the drift [providers.md](../reference/providers.md) names; the profile line warns that it reaches no client and names `api_key_env_name` | 2026-10-06 | [§6.5](#65-what-the-build-decided) | ✅ `TestARepointedKeyReachesTheAgentsOwnClient`, `TestALiteralKeyOnABuiltInProviderWarns` |
