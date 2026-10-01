---
title: "Bedrock plumbing: which transport reaches Bedrock in each agent, and what you type"
date: 2026-09-04
status: in-review
stage: DECIDED
next: "Write §12 step 7: the hand-written API-key provider and mantle recipes in the user guide, with P1 stated where users hit it"
depends-on:
  - wire-bridge-gateway.md
tags: [packs, providers, profiles, bedrock, aws, codex, opencode, pi, wire-bridge]
summary: "How each shipped agent reaches Bedrock's one shipped endpoint family, bedrock-runtime: through its own native Bedrock client where it has one (codex, opencode, pi, and claude for Anthropic models), or through yolo's wire bridge where it has none (claude for every other model, copilot, oh-omp). It also covers what a user types to get either one, and the shape of the single `bedrock` provider every agent reads. Signing, routing, model lists, search, credential scoping and the provider/profile redesign each have their own doc, listed under 'Where the rest went'."
---

# Bedrock plumbing: which transport reaches Bedrock in each agent, and what you type

**The question.** When you type `yolo -p bedrock -- <agent>`, how does that agent reach Amazon
Bedrock? It can go through its own built-in Bedrock client, or through yolo's wire bridge. This doc
also covers what you type to pick one, and what shape yolo's Bedrock provider takes so that every
agent can read it.

**Status:** 2026-09-25, rewritten after the maintainer's review of that day.
- Ruled: yolo ships one endpoint family, `bedrock-runtime` ([DIR-BR3](#DIR-BR3)). Every agent
  reaches every Bedrock model, across the whole matrix ([DIR-BR1](#DIR-BR1), [DIR-BR2](#DIR-BR2)).
  claude gets a native profile and an everything profile ([OQ-BR11](#OQ-BR11)). pi gets its native
  Converse client and a bridge route ([OQ-BR5](#OQ-BR5)). A launch refuses when no region is
  visible ([OQ-BR6](#OQ-BR6)).
- Built: the D1 fix (`f7b14308`, 2026-09-15): the codex derive now writes codex's own
  credential field, `env_key`. The no-region refusal, [OQ-BR6](#OQ-BR6) (2026-09-29,
  `packload.ProviderRegionGaps` and `ProviderRegionRefusal`, with the review's fixes
  [BR-D1](#BR-D1), [BR-D4](#BR-D4) and [BR-D5](#BR-D5) and its config-ref entry), at every notch,
  keyed on the provider's platform and asked of each agent; since [BR-DIR1](#BR-DIR1) (built
  2026-09-29, [BR-D20](#BR-D20) to [BR-D26](#BR-D26)) an agent with no region is first given its
  credential profile's region from the host's `~/.aws/config`, corrected the same day by a review
  in those rows and in [BR-D2](#BR-D2). Build step 2's marker
  ([OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2), `packdecl.PlatformProblem` and
  `ctx.selected_platform`): the `bedrock` provider declares `"platform": "aws-bedrock"`. Step 6's
  D5 half ([OQ-BR8](providers-and-profiles-redesign.md#OQ-BR8)): claude's switch and aws-auth's
  pointer key on that platform, and a user's own Bedrock provider co-claims the AWS credential
  names ([PP-D9](providers-and-profiles-redesign.md#PP-D9), `packload.credentialClaims`). The credential
  half, [`sso-backed-bedrock.md`](sso-backed-bedrock.md), is built.
- Built 2026-09-29, build step 2 ([OQ-BR9](#OQ-BR9), [OQ-BR1](#OQ-BR1); [§12](#12-what-i-would-build-in-order)
  steps 3 to 5 and 8.2): the provider moved into its own pack, `packs/bedrock`, which claude,
  codex, opencode and pi need, with one model list of every maker, each entry naming its
  `vendor` ([BR-D6](#BR-D6) to [BR-D8](#BR-D8)); codex, opencode and pi are bound to their own
  Bedrock clients, and claude's picks only Anthropic entries ([BR-D9](#BR-D9) to
  [BR-D14](#BR-D14)); and `bedrock-bridge` ships ([BR-D16](#BR-D16)).
- Built 2026-09-30: the bridge's own Bedrock upstream
  ([`wire-bridge-gateway.md` §8](wire-bridge-gateway.md#8-build-order), step 1,
  [WG-I37](wire-bridge-gateway.md#WG-I37) to [WG-I39](wire-bridge-gateway.md#WG-I39)). A provider
  named by region alone is reached at runtime's own `/openai/v1` in the served agent's region, so
  `bedrock-bridge` carries codex, opencode, pi and oh-omp on their via routes and claude on the
  everything profile, and copilot and oh-omp have a Bedrock path under it.
- Built 2026-09-30, [OQ-BR1](#OQ-BR1)'s bridge half on plain `-p bedrock`: it carries copilot
  and oh-omp, which have no Bedrock client of their own, through the wire bridge wherever the
  bridge is in the launch, as it is beside claude ([`wire-bridge-gateway.md` WG-I44](wire-bridge-gateway.md#WG-I44)),
  and a launch that gives the bridge's one adapter route to another agent's provider is refused
  ([WG-I45](wire-bridge-gateway.md#WG-I45)). claude, codex, opencode and pi keep their own clients
  there. What is not built: copilot and oh-omp in a launch with no bridge. Neither needs the bridge
  or `packs/bedrock` ([BR-D15](#BR-D15)), so beside them alone the profile is still undeclared,
  and with `bedrock` listed but no bridge it still reaches nothing for them.
- MEASURED: yolo's code at `f491d192` and `5e8e64f6` (2026-09-24). The codex, opencode and pi
  Bedrock clients were read statically from their shipped artifacts and never run
  ([§14](#14-evidence-and-how-to-re-check-it)).
- MEASURED 2026-09-29, for step 2: the codex-cli 0.158.0, opencode 1.18.32 and pi 0.99.1 Bedrock
  clients, read statically from the copies the launcher installed and never run; every shipped
  model id, read from its AWS model card ([`packs/bedrock/README.md`](../../packs/bedrock/README.md#sources)).
- UNMEASURED: no request has reached Bedrock from any agent, through the bridge, or with any of
  the three credentials.

**Needs your ruling:** [OQ-BR24](#OQ-BR24), raised 2026-09-30 when the bridge began carrying
copilot and oh-omp on plain `-p bedrock` ([BR-D15](#BR-D15)'s amendment). Both re-asks were answered 2026-09-29. [BR-D17](#BR-D17)'s is
ruled ([BR-D19](#BR-D19)): GPT-6.1 Sol everywhere, with no Region detection. [OQ-BR6](#OQ-BR6)'s is
directed ([BR-DIR1](#BR-DIR1)): yolo reads the effective profile's region from `~/.aws/config` and
delivers it the same way at `yolo host` and in a jail. Both were built 2026-09-29. [OQ-BR9](#OQ-BR9) and [OQ-BR1](#OQ-BR1) were ruled 2026-09-29:
one `bedrock` pack pulls in every model family, and `-p bedrock` uses each agent's own Bedrock
client, with `bedrock-bridge` forcing the wire bridge. Both are built, and the bridge's own
Bedrock upstream that [BR-D16](#BR-D16) waited on followed on 2026-09-30
([`wire-bridge-gateway.md` WG-I39](wire-bridge-gateway.md#WG-I39)), and so did copilot and
oh-omp through the bridge on plain `-p bedrock` wherever the bridge is in the launch
([`wire-bridge-gateway.md` WG-I44](wire-bridge-gateway.md#WG-I44)); the question above is what
[OQ-BR1](#OQ-BR1)'s ruling still lacks.

## The Bedrock and provider design set

Yes, the weekly list's three rows are one set of questions. On 2026-09-25 they, and the rest of
this doc's former questions, were split across these eight docs. Rule what is still open in this
order, because each later doc leans on the earlier ones. Each doc's own **Needs your ruling** line
names what is open there.

| Order | Doc | Its one question | Ruled |
| :--- | :--- | :--- | :--- |
| 1 | [`providers.md`](../reference/providers.md#deselection-clear-what-yolo-wrote-keep-what-the-user-wrote) | what happens to the model id yolo wrote when you drop a profile ([OQ-PSW2](../reference/providers.md#oq-psw2), [OQ-PSW4](../reference/providers.md#oq-psw4)) | 2026-09-25 |
| 2 | this doc | one Bedrock provider, and what you type ([OQ-BR9](#OQ-BR9) with [OQ-BR1](#OQ-BR1)) | 2026-09-29, built |
| 3 | [`providers.md`'s credential gate](../reference/providers.md#the-credential-gate) | which credentials and env a profile lets through to which agent ([OQ-CN6](../reference/providers.md#oq-cn6) with [OQ-CN2](../reference/providers.md#oq-cn2) first) | 2026-09-26 |
| 4 | [`providers-and-profiles-redesign.md`](providers-and-profiles-redesign.md) | what a provider and a profile should mean ([OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2), then [OQ-BR8](providers-and-profiles-redesign.md#OQ-BR8), then the PP questions) | [OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2), [OQ-BR8](providers-and-profiles-redesign.md#OQ-BR8): 2026-09-29, built; the PP questions open |
| 5 | [`model-lists-and-pickers.md`](model-lists-and-pickers.md) | which models each picker shows, and which yolo picks ([OQ-ML2](model-lists-and-pickers.md#OQ-ML2) first) | [OQ-ML1](model-lists-and-pickers.md#OQ-ML1), [OQ-ML2](model-lists-and-pickers.md#OQ-ML2): 2026-09-29; the MM questions open |
| 6 | [`wire-bridge-gateway.md`](wire-bridge-gateway.md) | how the bridge signs, and sending all traffic through it ([OQ-WG1](wire-bridge-gateway.md#OQ-WG1) first) | 2026-09-25 |
| 7 | [`bedrock-web-search.md`](bedrock-web-search.md) | web search on Bedrock profiles | [OQ-BR21](bedrock-web-search.md#OQ-BR21), [OQ-BR22](bedrock-web-search.md#OQ-BR22): 2026-09-29; the rest open |
| 8 | [`sso-backed-bedrock.md`](sso-backed-bedrock.md) | the SSO credential's last two edges ([OQ-SSO8](sso-backed-bedrock.md#OQ-SSO8), [OQ-SSO9](sso-backed-bedrock.md#OQ-SSO9)) | 2026-09-25 |

## Where the split ended up

The maintainer asked for this in plain words (*"I'm actually not sure where the split ended up"*).
First, two words, as [`providers.md`](../reference/providers.md) defines them:
- A **provider** is a declaration of where models come from: its region, model ids, and endpoint
  URLs if it has any. You never type it.
- A **profile** is the name you type after `-p`, and it selects one provider. `-p bedrock` picks
  the profile `bedrock` for every agent in the launch, and `-p codex=bedrock` picks it for codex
  alone.

Whether yolo should keep both concepts is
[`providers-and-profiles-redesign.md`](providers-and-profiles-redesign.md)'s question.

1. **One endpoint family, `bedrock-runtime`** ([DIR-BR3](#DIR-BR3)). Mantle, AWS's other
   OpenAI-compatible endpoint, is not shipped, so no name has to say which one.
2. **One provider, `bedrock`, holding every model family.** Each model entry declares its maker
   (its `vendor`). [OQ-BR9](#OQ-BR9) ruled it on 2026-09-29, and it is built: `packs/bedrock`
   ships it ([BR-D6](#BR-D6)).
3. **Two transports, and this is the real axis.**
   - The **native client** is the agent's own built-in Bedrock code talking to AWS. yolo supplies
     a region and a model id, never a URL. codex (OpenAI models only), opencode and pi (every
     model) have one; claude has one for Anthropic models only.
   - The **wire bridge** is yolo's in-jail daemon ([`wire-bridge.md`](../reference/wire-bridge.md)).
     The agent speaks its own dialect to the bridge on the jail's loopback. The bridge forwards to
     runtime's OpenAI-compatible route, and will sign the request itself
     ([OQ-BR10](wire-bridge-gateway.md#OQ-BR10), ruled).

   Earlier drafts, and some sibling docs, call these two the *native arm* and the *bridge arm*.
   Those words are retired here and mean exactly the native client and the wire bridge. A third
   route, a plain user provider with an API key and no bridge, is documentation only
   ([§6.3](#63-a-hand-written-api-key-provider-documentation-only)).
4. **claude has two profiles** ([OQ-BR11](#OQ-BR11)).
   - **native** is `-p bedrock` as shipped today: claude's own Bedrock mode, Anthropic models only.
   - **everything** *(coined here)* points claude at the bridge. The bridge forwards Anthropic ids
     untranslated and translates the rest, so one session can switch between Claude and GPT.

   pi likewise gets a native route and a bridge route ([OQ-BR5](#OQ-BR5)).

Each cell below names the transport, then the models it can call. The names are
[OQ-BR1](#OQ-BR1)'s ruling (2026-09-29), and the last column says what is built.

| Agent | You type `-p bedrock` | You type `-p bedrock-bridge` | Delivered by |
| :--- | :--- | :--- | :--- |
| **claude** | native (`CLAUDE_CODE_USE_BEDROCK=1`): **Anthropic models only**, because Messages serves Claude only ([§2](#2-what-bedrock-is--sourced-from-awss-pages)). Shipped | the everything profile: the bridge, **every model**, Anthropic ids passed untranslated ([OQ-BR11](#OQ-BR11)) | native: built, its model filtered to Anthropic entries ([BR-D9](#BR-D9)); everything: built 2026-09-30, claude routed at the bridge's adapter address ([WG-I39](wire-bridge-gateway.md#WG-I39)) |
| **codex** | native (`amazon-bedrock-runtime`, Responses): **OpenAI models only**, since the evidence covers GPT alone and Anthropic's cards list no Responses API; widened when done-condition 5 measures another family | its via row, Responses at its via URL ([WG-I20](wire-bridge-gateway.md#WG-I20)), which the bridge carries to runtime's `/openai/v1/responses` | native: built ([BR-D12](#BR-D12)); bridge: built 2026-09-30 ([BR-D16](#BR-D16), [WG-I39](wire-bridge-gateway.md#WG-I39)) |
| **opencode** | native (`amazon-bedrock`, routed per model): **every model** | its via row, carried the same way | native: built ([BR-D13](#BR-D13)); bridge: built 2026-09-30 |
| **pi** | native (`amazon-bedrock` on Converse): **every model**; pi-ai 0.87.1's 165 Bedrock ids are all Converse | the bridge's sign-only chat-completions route, **every model** ([OQ-BR5](#OQ-BR5)), carried the same way | native: built ([BR-D14](#BR-D14)); bridge: built 2026-09-30 |
| **copilot** | no native client, so the bridge: **every model** | the same | built 2026-09-30, copilot routed at the bridge's adapter address and started on the list's first model: `-p bedrock-bridge` ([WG-I39](wire-bridge-gateway.md#WG-I39)), and plain `-p bedrock` wherever the bridge is in the launch ([WG-I44](wire-bridge-gateway.md#WG-I44)); copilot does not need the pack or the bridge ([BR-D15](#BR-D15)) |
| **oh-omp** | the bridge's [sign-only route](wire-bridge-gateway.md#4-part-3--the-sign-only-openai-chat-completions-route-ruled); its own client speaks mantle (SOURCED, not installed). Models unverified | the same | built 2026-09-30, on its via route: `-p bedrock-bridge`, and plain `-p bedrock` wherever the bridge is in the launch, as copilot's ([WG-I44](wire-bridge-gateway.md#WG-I44)) |
| **agy** | nothing: its transport is a closed enum | nothing | nobody; it is the one hole ([§4](#4-what-each-agent-can-actually-do)) |

"Every model" means every entry of the provider's list; which models each filter takes is by the
entry's declared vendor ([§6.1](#61-the-provider-shape-one-bedrock-provider-or-two), [BR-D11](#BR-D11)).

**Reads with** [`providers.md`](../reference/providers.md) (catalog, selection, derives,
`wire_api`), [`protocol-resolution.md`](../reference/protocol-resolution.md) (why an endpoint-less
provider passes), [`zai-plumbing.md`](../reference/zai-plumbing.md) (the predecessor) and
[`local-model-endpoints.md`](../research/local-model-endpoints.md) (per-agent config surfaces). The
sibling [`sso-backed-bedrock.md`](sso-backed-bedrock.md) owns where the credential comes from. Its
service, adapter and `packs/aws-auth` landed 2026-09-18 (`b94351fe`, `e76e43b2`, `9fc4879d`), and a
podman jail has reached them since 2026-09-20 (`62e553a8`), though never against a live
`aws sso login` ([README](../../packs/aws-auth/README.md)). Its plan's done-condition 7 waits on
this doc's native clients.

---

## 1. Verdict and principles

**Use each agent's own Bedrock client where it has one** (codex, opencode, pi, and claude for
Anthropic models): an endpoint-less provider plus one derive binding per agent. **Put the wire
bridge in front of the same provider** for every agent or model that has no native client; the
bridge signs its own requests. **Ship the hand-written API-key provider and mantle as documented
recipes, not code.**

The native clients are where the leverage is. Three agents already implement Bedrock better than a
plain HTTP provider would: [SigV4](https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv.html) (AWS's
request signing) from whatever the credential chain resolves, or an API key; cross-Region
inference; and in codex's case a first-class `bedrock_api_key` mode in `auth.json`. Rewriting that as "a base URL plus an API key" throws the work away, pins yolo to a URL, and shuts
out the SSO credential, the primary one ([§6.4](#64-the-credential-three-are-supported)).

**P1. An auth mode is a bundle of `{credential channel, environment, model ids}` that moves
together.** This machine's 2026-05 switch from Bedrock to Teams left the Bedrock-shaped model pin
behind, and it failed later as a 404. For Bedrock, the endpoint family and the model id are one
decision. The general form, and the defect it left in selection, is
[the providers reference](../reference/providers.md#deselection-clear-what-yolo-wrote-keep-what-the-user-wrote).

**P2. Where the agent already knows the service, yolo supplies facts, not plumbing.** A provider
with no endpoints says "the client composes its own URL from a region", and packs/claude's
`bedrock` is exactly that. [The credential preflight](../reference/providers.md#the-credential-preflight)
demands nothing for it, and [protocol resolution](../reference/protocol-resolution.md) treats it as
"repointed nothing" and resolves it for every agent (`ResolveProtocol`,
`internal/packload/protocolresolution.go`).

**P3. One model-id spelling per provider entry.** A profile's alias resolves through the one
`models` map once. So an agent-dependent spelling means the map is wrong somewhere, and an
endpoint family is a provider entry, never a switch
([§5](#5-one-endpoint-family--runtime-ships-and-why-not-mantle)).

---

## 2. What Bedrock is — sourced from AWS's pages

SOURCED from AWS's documentation on 2026-09-04, with some rows re-read 2026-09-24/25
([§14](#14-evidence-and-how-to-re-check-it)). No request was made.

| | `bedrock-runtime` (shipped) | `bedrock-mantle` (a recipe) |
| :--- | :--- | :--- |
| Base URL | `https://bedrock-runtime.{region}.amazonaws.com/openai/v1` | `https://bedrock-mantle.{region}.api.aws/openai/v1` per the model cards, `/v1` per the API pages (⚠ disputed) |
| GPT-5.6 Sol id | `us.`/`global.openai.gpt-5.6-sol`; **no in-Region** | bare `openai.gpt-5.6-sol`; **no Geo/Global** |
| GPT-6 Astra | `us.`/`global.openai.gpt-6-astra`, Converse included | `openai.gpt-6-astra`, **`us-west-2` only**, no Converse |
| APIs | Responses, Chat Completions, Converse, InvokeModel, Messages | Responses, Chat Completions, Messages |
| Auth | SigV4 or a Bedrock API key | the same; its SigV4 service name is undocumented |
| IAM | `bedrock:InvokeModel`, `…WithResponseStream` | `bedrock-mantle:CreateInference`, `CountTokens`, `ListModels` |
| Server-side tools (Web Search), `background` | no | yes |
| Price | Global cross-Region inference ≈10% cheaper ($10 vs $11/M input, GPT-6 Astra) | In-Region rate only; per-token otherwise identical |
| AWS's advice | *"For most new applications, use the `bedrock-runtime` endpoint"* | when you need a capability only it has |

> [!IMPORTANT]
> **The model id is a function of the endpoint family, and AWS says so.** The GPT-5.6 Sol card:
> *"On `bedrock-runtime`, name a cross-Region inference profile as the model —
> `us.openai.gpt-5.6-sol` or `global.openai.gpt-5.6-sol`."* The bare id is mantle's spelling.
> Sending it to runtime is the P1 failure, and it surfaces as a model error rather than an
> endpoint error.

- **Bedrock is not only OpenAI.** SOURCED 2026-09-24. Runtime's `/openai/v1/chat/completions`
  serves DeepSeek, Qwen3/Qwen3 Coder, Kimi, GLM, MiniMax, Mistral/Devstral, Grok, Nemotron,
  Gemma 3, GPT-5.x/6, and Anthropic models (AWS's example uses `us.anthropic.claude-sonnet-4-6`).
  It streams and is signed as the service `bedrock`. **Messages serves Claude only**, so a
  non-Anthropic model reaches claude only by translation.
- **The credential is not this design's choice.** Both endpoints take SigV4 or a Bedrock API key.
  Which one a jail carries is [`sso-backed-bedrock.md`](sso-backed-bedrock.md)'s question.
- **Runtime has no model-list endpoint and no search.** There is no `GET /openai/v1/models`, and
  Web Search is mantle-only. The replacements are in
  [`model-lists-and-pickers.md`](model-lists-and-pickers.md) and
  [`bedrock-web-search.md`](bedrock-web-search.md).

---

## 3. What yolo has today

Only the parts of the built provider system that Bedrock lands on. MEASURED at `f491d192` and
`5e8e64f6`.

- **One `bedrock` provider exists, and it is claude's.** `packs/claude/pack.json` ships a
  provider `bedrock` (no endpoints, no models, the six AWS credential names under
  `api_key_env_name`) and a profile `bedrock` selecting it. It is
  [the worked example](../reference/providers.md#two-channels-split-by-payload-type) in
  `providers.md`. ⚠ **Changed 2026-09-29**, after the measurements above: the provider declares
  `"platform": "aws-bedrock"` ([OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2)) and
  `region_env_name`, the region refusal's variables ([OQ-BR6](#OQ-BR6), [BR-D1](#BR-D1)).
  `CLAUDE_CODE_USE_BEDROCK`, which a profile-gated `env` and `config-overlay` pair set, is now
  set by claude's own derive, in env and in `claude/settings`, whenever claude's provider
  declares that platform and routes through no via service; `packs/aws-auth` gates its
  credential pointer on the platform, not on the profile name `bedrock`
  ([OQ-BR8](providers-and-profiles-redesign.md#OQ-BR8), [PP-D2 to PP-D4](providers-and-profiles-redesign.md#PP-D2)).
  ⚠ **Changed again 2026-09-29, by build step 2**: the provider and its profile left packs/claude
  for `packs/bedrock`, which lists models ([BR-D6](#BR-D6), [BR-D15](#BR-D15)).
- **Only the claude derive reads `region`**, mapping `p.region` to `AWS_REGION`. The `Region`
  field's comment in `internal/packdecl/contributes.go` reads *"Region is the region a regional
  provider is reached through — Bedrock's address half"*.
- **Every other derive drops an endpoint-less provider.** codex, pi, opencode and oh-omp gate
  their catalog row and selection on a `providerEndpoint` helper, which returns nil when no URL is
  named. copilot's derive composes nothing for such a provider, and a comment there names Bedrock.
- **Provider names are sole-owned across packs** (`KindProvider`, `CombineExclusive`,
  `internal/packdecl/kinds.go`). Two packs shipping one name refuse the launch; one pack shipping
  two names is ordinary.
- **The wire bridge carries a declared provider to claude and copilot, with an API key only.**
  `adaptEndpoints` gives an `openai` endpoint an `anthropic` twin at the bridge's loopback.
  `routeFor` accepts only `openai-chat-completions`. The bridge sends the key that
  `api_key_env_name` names, and nothing in the tree signs SigV4
  ([`wire-bridge-gateway.md`](wire-bridge-gateway.md)).

**So:** claude on Bedrock is wired but unmeasured. claude and copilot reach Bedrock's other models
through the bridge with an API key. codex, opencode and pi cannot see Bedrock at all. (The state
on 2026-09-24. Build step 2 bound all three, [BR-D12](#BR-D12) to [BR-D14](#BR-D14).)

---

## 4. What each agent can actually do

Verified from shipped artifacts, not documentation, except where noted
([§14](#14-evidence-and-how-to-re-check-it)).

| Agent | Native Bedrock support | How it authenticates | Evidence |
| :--- | :--- | :--- | :--- |
| **claude** | **Anthropic only**: `CLAUDE_CODE_USE_BEDROCK=1` drives Messages | the AWS chain plus `AWS_REGION`; `AWS_BEARER_TOKEN_BEDROCK` present in the binary, precedence not read | claude 2.1.282, 2026-09-24 |
| **codex** | built-ins `amazon-bedrock` (mantle) and `amazon-bedrock-runtime`, both `wire_api = responses`; `model_catalog_json` | `AWS_BEARER_TOKEN_BEDROCK` first, else the SDK chain; region from `aws.region`, `AWS_REGION` or `AWS_DEFAULT_REGION` | codex-cli **0.156.1**, 2026-09-24 |
| **opencode** | built-in `amazon-bedrock`, `options: {region, profile, endpoint}` | bearer over chain; region from `options.region`, else `AWS_REGION`, **else silently `us-east-1`** | opencode 1.18.32, 2026-09-22/24 |
| **pi** | built-in `amazon-bedrock` on `bedrock-converse-stream` | stored key, `AWS_BEARER_TOKEN_BEDROCK`, `AWS_PROFILE`, access keys, container-credentials vars, web identity; `AWS_BEDROCK_SKIP_AUTH=1` disables | pi-ai **0.87.1** |
| **copilot** | none; the bridge, whose `anthropic` endpoint its derive prefers | the bridge ignores `COPILOT_PROVIDER_API_KEY` and sends its own | `packs/copilot/derive.lua` |
| **oh-omp** | **unverified**; not installed here, and `packs/omp` shipped after drafting | — | — |
| **agy** | none; its transport is a closed enum (`ccpa`/`gemini`/`stubby`) | — | [`local-model-endpoints.md`](../research/local-model-endpoints.md) §"agy" |

**The convergence is the finding** (INFERRED from the SOURCED reads). codex, opencode and pi
(since pi-ai 0.87.1) agree on the provider id `amazon-bedrock`, the API-key variable
`AWS_BEARER_TOKEN_BEDROCK`, the precedence (an API key over the chain) and the region knobs. That
is a de-facto interface, and yolo's job is to feed it. The precedence is why a jail never
carries a key beside a chain credential.

**Where they diverge is the endpoint family**, which stays invisible until the first request.
- **codex.** Its `amazon-bedrock` is a mantle client: it bakes
  `https://bedrock-mantle.{region}.api.aws/openai/v1` (`amazon_bedrock/mantle.rs`), maps
  `gpt-5.6-sol` to bare `openai.gpt-5.6-sol`, and keeps its own region allowlist. Between 0.145.0
  and 0.156.1 it gained `amazon-bedrock-runtime` (`runtime.rs`, a `.amazonaws.com/openai/v1`
  suffix), whose catalog has `global.` ids for `-terra` and `-luna`. Read from strings; what it
  sends is INFERRED.
- **opencode** routes per model: bare ids to mantle, geo-prefixed ids to runtime
  ([§14](#14-evidence-and-how-to-re-check-it)).
- **pi** drives Converse, which runtime alone serves. ⚠ Its catalog lists bare
  `openai.gpt-5.6-sol` against a runtime URL: the P1 failure, shipped by a vendor.

So without intervention, codex and pi want different spellings of the same model, which P3 forbids.

---

## 5. One endpoint family — runtime ships, and why not mantle

**The ruling: yolo ships `bedrock-runtime` only.** The maintainer, 2026-09-25: *"let's skip
mantle"* ([DIR-BR3](#DIR-BR3)). Mantle is not refused. It is a documented manual recipe, revisited
when someone needs a model or feature only mantle serves. SOURCED
([§14](#14-evidence-and-how-to-re-check-it)):

- **Runtime serves every model family mantle does, plus Converse**, and is ≈10% cheaper through
  Global cross-Region inference, which is runtime's alone. That holds per family, not per model:
  a model only mantle serves is the revisit trigger.
- **AWS recommends runtime**, and mantle's signing is undocumented and its base path disputed.
- **Mantle needs its own IAM set** and would double the
  [matrix](#67-the-whole-matrix--every-agent-every-model-every-credential).

The evidence for each is in [§14](#14-evidence-and-how-to-re-check-it).

**What mantle alone offers:** server-side tools, background inference, and Projects and
Workspaces. The one a coding agent would use is Web Search, which covers GPT-5.4 to 5.6 in three
US Regions. The agents here drive client-side tools, which runtime serves, and search comes from
AgentCore instead ([`bedrock-web-search.md`](bedrock-web-search.md)).

**The mantle recipe** is [§6.3](#63-a-hand-written-api-key-provider-documentation-only)'s entry with mantle's
base URL and bare ids, declared as its own provider (P3). It carries only a Bedrock API key,
because the bridge's signer signs for runtime's host alone. Claude Code's
`CLAUDE_CODE_USE_MANTLE=1`, with `anthropic.`-prefixed ids, is the claude half. It goes in the user
guide ([§12](#12-what-i-would-build-in-order), step 7).

**A family is a provider entry, never an option.** The model id is a function of the family.
`options` is a flat map a derive reads, while `models` is a provider field the option layer never
touches. A family option would move the endpoint and leave the ids behind, which is P1 with a knob.

**codex reaches runtime with no override** (INFERRED from 0.156.1 strings; codex was not run). The
derive selects `amazon-bedrock-runtime` and writes only `aws.region`, and nothing yolo ships
selects the mantle built-in. An older codex falls back to an override its guard permits
(verbatim, 0.156.1):

> `` model_providers.<id> only supports changing `base_url`, `auth`, `http_headers`, `aws.profile`, `aws.region`, `aws.credential_export`, and `aws.auth_refresh`; ``

The override is `model_providers.amazon-bedrock.base_url =
https://bedrock-runtime.{region}.amazonaws.com/openai/v1`, beside `aws.region`. The list grew by two
`aws.*` keys between 0.145.0 and 0.156.1, which is why [R1](#11-risks) pins the version.

---

## 6. The proposed shape

### 6.1 The provider shape: one `bedrock` provider, or two

> [!NOTE]
> **Built 2026-09-29 as A** ([OQ-BR9](#OQ-BR9)'s ruling). The shipped file differs from the
> snippet below in two ways: it declares no `options` (declaring any turns on the profile-option
> census and would refuse user profiles that pass today, the reason [ML-D1](model-lists-and-pickers.md#ML-D1)
> gives), and each model's facts ride `model_options`, `vendor` among them ([BR-D6](#BR-D6)).
> The file is [`packs/bedrock/pack.json`](../../packs/bedrock/pack.json).

**Under [OQ-BR9](#OQ-BR9)'s leaning (A), one runtime provider holds every model family.** It keeps
the name `bedrock` and moves out of packs/claude into a new `bedrock` pack, which packs/claude
`needs` (as it needs `openai-auth`). It must move, because a provider every agent reads needs an
owner every jail selects, and sole ownership rules out packs/claude. The new pack has no CLI; it
is declarative facts, like `packs/zai`.

Each model entry declares its **vendor** *(coined here)*: the model's maker, as an open-vocabulary
lowercase string (`anthropic`, `openai`, `deepseek`, …). Derives read it, core does not interpret
it, and it is never parsed from the id. Each derive resolves the default among the entries its
agent can call; which vendors each agent and transport takes, and the evidence for each filter, is
the "models it can call" half of [the per-agent table](#where-the-split-ended-up).

An entry with no vendor, such as a user's string-form entry, is offered to every agent, as today.
The shipped entries come from a built-in pack, ruled 2026-09-25
([OQ-BR3](model-lists-and-pickers.md#OQ-BR3)). How a pack carries object-form entries with a
`vendor` is [`model-lists-and-pickers.md`](model-lists-and-pickers.md)'s. A derive recognizes
the provider as Bedrock without matching its name by its `platform`
([OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2), ruled and built 2026-09-29: the derive reads
`ctx.selected_platform`), so the native derives are unblocked.

```jsonc
// packs/bedrock/pack.json under option A: the shape, not the file
{ "name": "bedrock", "contributes": [
    { "kind": "provider", "name": "bedrock",
      "platform": "aws-bedrock",                    // OQ-BR2's marker, built
      "region_env_name": ["AWS_REGION", "AWS_DEFAULT_REGION"],   // OQ-BR6, built (BR-D1)
      "region_file": { "path": ".aws/config", "key": "region" },  // BR-DIR1, built (BR-D21); abridged
      "options": { "model": "default", "aws_profile": null } },   // entries: model-lists doc
    { "kind": "profile", "name": "bedrock", "provider": "bedrock" } ] }
```

There is no endpoint-family field ([OQ-BR7](#OQ-BR7)): consumers compose runtime's host from
`region`, and a mantle recipe carries its own URL. **No region ships**
([OQ-BR9](#OQ-BR9)'s ruling, [OQ-BR6](#OQ-BR6)): the user sets one, the host's `~/.aws/config`
names one for the credential's profile ([BR-DIR1](#BR-DIR1)), or the launch is refused (a
shipped region would make the refusal unreachable, since the composed entry would always carry
one). The provider keeps the six AWS credential names packs/claude's `bedrock` claimed under
`api_key_env_name` ([CN-D2](provider-credential-scope.md#CN-D2)), which the
credential gate withholds by; a list of several points an agent at none of them, so one entry
still serves all three credentials ([§6.4](#64-the-credential-three-are-supported)). The
snippet was corrected on 2026-09-29, when it paired a shipped region with the refusal's field.

**What would have shipped had [OQ-BR9](#OQ-BR9) gone B** (it went A). claude's `bedrock` stays in packs/claude with
Anthropic ids. A new `bedrock` pack ships a provider `bedrock-openai` with
`global.openai.gpt-5.6-*` ids for codex, pi and opencode, selected by the profile `bedrock-gpt`.
The reason: one `default` alias cannot name an Anthropic id for claude and a GPT id for codex. The
`Name` field's comment calls that the ordinary case (*"one pack shipping two names is two
contributions"*).

### 6.2 What each derive emits

Each binding lives in its agent's own derive: core learns no agent's vocabulary and resolves no
model ([OQ-CS8](../reference/providers.md#oq-cs8)).

| Agent | Catalog | Selection | Region / profile |
| :--- | :--- | :--- | :--- |
| **codex** | `[model_providers.amazon-bedrock-runtime]` with `aws.region`. `base_url` only for codex older than 0.156.1, and then on `amazon-bedrock`. No other field: codex refuses them (D3) | `model_provider = "amazon-bedrock-runtime"`, `model = <id>` | `aws.region` from `region`; `aws.profile` from `aws_profile` |
| **opencode** | `provider["amazon-bedrock"] = { options: { region, profile? }, models: {…runtime-spelled ids…} }`, with **no `npm` and no `endpoint`/`baseURL`** | `model = "amazon-bedrock/<id>"` | `options.region`, `options.profile` |
| **pi** (native half of [OQ-BR5](#OQ-BR5)) | bind to pi's built-in `amazon-bedrock`, with no `baseUrl` and no `api`. `providers["amazon-bedrock"].models` adds only runtime-spelled ids the built-in lacks; pi's docs say a `models` entry *"adds or replaces a model with the same ID on that provider"* | `defaultProvider = "amazon-bedrock"`, `defaultModel = <id>` | `AWS_REGION` in the jail env |
| **claude** | none; claude has no catalog | env, as today | `AWS_REGION` from `region` |

**codex's row** is INFERRED from binary strings. **pi's row** was re-read for this rewrite at
pi-ai 0.87.1, the copy yolo's launcher installed here (read, not run). `amazon-bedrock` sits on
`bedrockConverseStreamApi()`, and its catalog holds 165 ids, all Converse. Its region comes from an
inference-profile ARN in the id, else `options.region`/`AWS_REGION`/`AWS_DEFAULT_REGION`, else the
catalog entry's endpoint region, else `us-east-1`. The earlier row targeted 0.82.1's bare API id;
pi changes fast, so re-read it at the shipping version.

**opencode's row** omits a provider-level `npm` and any `endpoint`/`baseURL`, because either one
drags opencode's bare ids off mantle and onto runtime; a bare id a user adds still reaches mantle
through opencode's own catalog. ⚠ `options.region` alone does not enable the provider: it needs one
of six credential signals, and each supported credential supplies one. The reading behind both is
in [§14](#14-evidence-and-how-to-re-check-it).

Selection keys ride the reserved `selection` namespace, unchanged: they are written on activation
and never on absence, and an interactive `/model` still stands.

> [!NOTE]
> **As built, 2026-09-29, where it differs from the table.** codex's override carries `aws.region`
> only for a region the provider declares, and no `aws.profile`: codex reads `AWS_REGION`,
> `AWS_DEFAULT_REGION` and `AWS_PROFILE` itself, and the provider declares no `aws_profile`
> option ([BR-D12](#BR-D12)). opencode's row likewise writes `options.region` only from the
> provider and no `options.profile` ([BR-D13](#BR-D13)). pi's `models` lists every entry of the
> list, not only ids pi's catalog lacks, since a derive cannot read pi's catalog, and pi's own
> region comes from `AWS_REGION`, which pi's env derive sets from the provider's `region`
> ([BR-D14](#BR-D14)). claude's env pins a model only when the profile or a user `default` names
> an Anthropic entry ([BR-D9](#BR-D9)). pi's, opencode's and codex's facts were re-read at the
> versions the launcher installed on 2026-09-29: pi 0.99.1, opencode 1.18.32, codex-cli 0.158.0.

### 6.3 A hand-written API-key provider (documentation only)

An agent or model with no native path, or a user who wants a plain HTTP provider, needs no yolo
code. It needs an ordinary user-scope `providers` entry. A workspace file carrying
`endpoints.<protocol>.base_url` is a fatal config error
([providers.md, Current values](../reference/providers.md#current-values)).

```jsonc
"providers": { "bedrock-gw": {
  "endpoints": { "openai": { "base_url": "https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1",
                             "wire_api": "openai-chat-completions" } },
  "api_key_env_name": "AWS_BEARER_TOKEN_BEDROCK",
  "models": { "default": "global.openai.gpt-5.6-sol" } } }
```

**The `wire_api` must be `openai-chat-completions`**, the one dialect the bridge translates to. In
a jail with packs/claude the bridge fronts the entry, so claude and copilot reach it and pi
catalogs it directly. codex gets no row, because it speaks Responses only. Any runtime
chat-completions id works. INFERRED end to end. The user writes the region into the URL: a
`${region}` template would need interpolation the composer lacks and the URL validator rejects.

> [!WARNING]
> **Today this provider carries an API key and nothing else.** That has three consequences:
> - an SSO or static-key user needs a minted key, which is
>   [option D](sso-backed-bedrock.md#4-five-options) and unbuilt;
> - a bearer beside `aws-auth`'s pointer is refused, so this provider and SSO cannot share a jail;
> - an account denying `bedrock:CallWithBearerToken` rejects every request.
>
> For claude and copilot, the bridge's signer ([OQ-BR10](wire-bridge-gateway.md#OQ-BR10)) removes
> all three.

<a id="65-the-credential-three-are-supported"></a>

### 6.4 The credential: three are supported

The maintainer ruled on 2026-09-24 that yolo supports three Bedrock credentials. The ruling is
recorded as [OQ-SSO7](sso-backed-bedrock.md#13-decision-ledger) in the doc that owns credentials:
*"We will support bearer tokens, we will support secret and key, and we also need to support
sessions through single sign-on. That's the big one."* This section rules nothing.

- A **Bedrock API key** is AWS's Bedrock-only credential. It is sent as-is as
  `Authorization: Bearer`, read from `AWS_BEARER_TOKEN_BEDROCK`, and "bearer" here means only
  this. A long-term key expires in anywhere from a day to never; a short-term key is a
  SigV4-presigned token that lives at most 12 hours. It is not an access key.
- A **chain credential** *(coined here)* is anything the
  [AWS credential chain](https://docs.aws.amazon.com/sdkref/latest/guide/standardized-credentials.html)
  resolves. It is signed onto each request with SigV4 and never sent as-is.

| | Credential | Reaches a jail through | The agent | Refreshes in a running jail? |
| :--- | :--- | :--- | :--- | :--- |
| **1** | a Bedrock API key | `env_sources` → `AWS_BEARER_TOKEN_BEDROCK` | sends it as a bearer | no, frozen at launch |
| **2** | a static key and secret | `env_sources` → `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` | signs (chain credential) | nothing to refresh |
| **3** | an SSO session via an assumed role (**primary**) | `packs/aws-auth`'s pointer `AWS_CONTAINER_CREDENTIALS_FULL_URI` | fetches and signs (chain credential) | yes, the host re-mints it |

**The native clients need nothing credential-specific.** Every native client takes a bearer if one is
set, and a chain credential otherwise. So any one of the three works, the preflight demands none,
and this design writes no credential anywhere.

> [!WARNING]
> **Never two at once.** Every measured client prefers a bearer, so credential 1 beside credential
> 3 silently uses the frozen bearer. Static keys beside the pointer fail the same way, because the
> chain's environment provider comes first. yolo refuses both launches, with no hatch: the pair is
> let through only when `AWS_PROFILE` is delivered beside it, which makes the JavaScript SDKs skip
> the environment provider. The refusal is declared by `aws-auth` under `overridden_by`, not
> hardcoded in core ([OQ-SSO8](sso-backed-bedrock.md#OQ-SSO8), built 2026-09-25;
> [`sso-backed-bedrock.md` §8](sso-backed-bedrock.md#8-behaviour-this-design-specifies)).

A bearer minted from `aws-auth`'s narrowed, role-chained session lives at most an hour (INFERRED
from STS's caps). That is why the bridge signs rather than carrying a minted key. AWS's guidance on
each credential, and option D's retirement ([OQ-SSO9](sso-backed-bedrock.md#OQ-SSO9), ruled
2026-09-25: yolo mints no bearer), are [`sso-backed-bedrock.md`](sso-backed-bedrock.md)'s.

### 6.5 Every Bedrock model, in every agent — the direction

**The maintainer's direction, 2026-09-24** ([DIR-BR1](#DIR-BR1)): model choices are current at
launch for claude, codex and the rest, and a company pack selects the interesting models and
presents them everywhere. *"Claude should be able to use all of those models with basically no
change."* Most of it is true already: claude and copilot reach every runtime chat-completions
model through the bridge with an API key ([§3](#3-what-yolo-has-today)). Three pieces are missing:

| Missing piece | Where it is designed |
| :--- | :--- |
| the bridge taking the other two credentials (a SigV4 signer, ruled but unbuilt) | [`wire-bridge-gateway.md`](wire-bridge-gateway.md) |
| one provider shape every agent reads | [§6.1](#61-the-provider-shape-one-bedrock-provider-or-two) |
| a model list an org can ship and every picker renders | [`model-lists-and-pickers.md`](model-lists-and-pickers.md) |

```mermaid
flowchart LR
  list["effective model list<br/>one provider, vendor per entry"] --> cn["claude, native<br/>vendor anthropic"]
  list --> cb["claude, everything profile"]
  list --> cp["copilot, oh-omp, pi bridge"]
  list --> cx["codex<br/>vendor openai"]
  list --> pi["pi / opencode native<br/>every vendor"]
  cb --> br["wire bridge<br/>SigV4 signer"]
  cp --> br
  br --> rt["bedrock-runtime<br/>chat completions / Messages"]
  cn --> rtm["bedrock-runtime<br/>Messages"]
  cx --> rt2["bedrock-runtime<br/>Responses"]
  pi --> rt3["bedrock-runtime<br/>Converse / per model"]
```

### 6.6 IAM, per use

SOURCED from AWS's endpoints, model API compatibility and AgentCore gateway pages, 2026-09-24/25.

| Use | Actions the jail's credential needs |
| :--- | :--- |
| runtime: native clients, chat completions, Messages | `bedrock:InvokeModel`, `bedrock:InvokeModelWithResponseStream`, plus `project/default` ([R5](#11-risks)) |
| web search through an AgentCore gateway | `bedrock-agentcore:InvokeGateway` on the gateway ARN; `InvokeWebSearch` belongs to the gateway's own role |

The `aws-auth` README's example policy grants only the first row, and
[`bedrock-web-search.md`](bedrock-web-search.md) carries that trap. Mantle's `bedrock-mantle:*`
actions are the recipe user's to grant.

### 6.7 The whole matrix — every agent, every model, every credential

**The requirement** ([DIR-BR2](#DIR-BR2), 2026-09-24): *"We need to support single sign on through
all of the methods so I can use any model. I want to use Kimi in Claude through Bedrock by just
choosing it in the menu, or I want to use OpenAI and then I want to switch to Claude. So we got to
support everything everywhere, the whole matrix."* A cell is one agent, one model family and one
credential. Every cell must work, and each model must be one pick in the agent's own menu. This is
the umbrella acceptance runbook.

| Agent | Anthropic models | Other models | API key | Static / SSO | Web search | Delivered by |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| **claude** | native, or everything pass-through | everything, bridge | yes | native yes; bridge needs the signer | AgentCore preset | native shipped; rest [`wire-bridge-gateway.md`](wire-bridge-gateway.md), [`model-lists-and-pickers.md`](model-lists-and-pickers.md) |
| **codex** | native (measure: done 5) | native | yes | yes, via the chain | preset | [§6.2](#62-what-each-derive-emits) |
| **opencode** | native | native | yes | yes | preset | [§6.2](#62-what-each-derive-emits) |
| **pi** | native Converse, or bridge | native Converse, or bridge | yes | yes | preset, via pi's MCP adapter | [§6.2](#62-what-each-derive-emits); [`wire-bridge-gateway.md`](wire-bridge-gateway.md) |
| **copilot** | bridge | bridge | yes | needs the signer | preset | [`wire-bridge-gateway.md`](wire-bridge-gateway.md) |
| **oh-omp** | the bridge's sign-only route | same | yes | needs that route | **none**: no MCP table | [`wire-bridge-gateway.md`](wire-bridge-gateway.md); unverified |
| **agy** | no Bedrock transport | — | — | — | — | **the one hole** |

The search column is [`bedrock-web-search.md`](bedrock-web-search.md)'s. Under an API key alone
there is no search, because a bearer cannot sign. **Done means the matrix, measured:** a cell counts
as built only once a request has gone through it on a real host. That is checked by a manual
runbook, not a test, because automated tests never make API calls (done-condition 11).

---

## 7. Traps — read before writing code

**D3. codex actively manages its `amazon-bedrock` entry.** MEASURED by a string search of the
0.156.1 binary, which carries *"Amazon Bedrock login cannot select `X` because `Y` sets
`model_provider` to `Z`"*, *"selected Codex-managed Amazon Bedrock API key is no longer
available"* and *"…configured to use `aws.credential_export`. Please clear this setting…"*.
(0.145.0's *"retrying once"* message is gone.) So a field beyond the permitted seven is refused,
not ignored. And pinning `model_provider` blocks `codex login` for Bedrock while the profile is
active, which is acceptable but must be in the briefing.

⚠ The derive's generic catalog row cannot be reused. Every row carries `name` and `wire_api`, plus
`env_key` when a variable is named; `name` is there because codex refuses an empty one
(`TestCodexDeriveSetsModelProviderName`). None of the three is among the seven. Whether codex
accepts one set to its built-in default is UNMEASURED.

**D4. codex may not know the pinned runtime ids.** Its catalog holds mantle spellings, plus
`global.` ids for `-terra` and `-luna` but none for `-sol` (MEASURED by binary search). An unknown
id yields *"Unknown model … This will use fallback model metadata"*: the request works, but the
context-window and pricing metadata are wrong. That is the accepted cost of the runtime pin.

D2, D5, D6 and D7 moved with their questions ([Where the rest went](#where-the-rest-went--moved-on-2026-09-25)).

---

## 8. Behaviour this design fixes

Written for the implementer. Anything not here and not an open question is theirs.

**Degenerate inputs.**
- **No region: refuse the launch** ([OQ-BR6](#OQ-BR6), ruled, **built 2026-09-29**). Refuse only
  when the selected Bedrock provider declares no `region` **and** the composed jail environment
  holds neither `AWS_REGION` nor `AWS_DEFAULT_REGION`, both of which yolo can see. The refusal
  names the three places. ⚠ The ruling's premise called an agent reading an `~/.aws/config`
  region "unproven"; that is false of claude, whose resolver reads `AWS_REGION`, then
  `AWS_DEFAULT_REGION`, then the shared-config region for the active profile, then falls back
  to `us-east-1` (read statically from Claude Code 2.1.285 on 2026-09-29, never run). Without
  the refusal, codex fails at first request, and claude, opencode and pi silently use
  `us-east-1`, a region nobody chose. **Revised 2026-09-29 by [BR-DIR1](#BR-DIR1), built the
  same day:** yolo reads the region of the profile the agent's AWS credential comes from in the
  host's `~/.aws/config` and delivers it, in the first variable that agent reads, wherever the
  agent would otherwise have none, at `yolo host` and in a jail alike; the refusal then counts
  it, and when the file gives none it names the file, the profile and why
  ([BR-D20](#BR-D20) to [BR-D26](#BR-D26),
  [`providers.md`](../reference/providers.md#the-region-file)). As built
  ([BR-D1](#BR-D1) to [BR-D5](#BR-D5)): the requirement keys on the provider's `platform`, whose
  region variables a pack declares with `region_env_name` (packs/bedrock, for `aws-bedrock`), so a
  provider a user declares with that platform is refused the same way; each agent on such a
  provider is asked about the region that reaches IT; and the refusal is the provider
  pre-flight's second half, at the fresh container launch, the attach, every `macos-user`
  invocation and `yolo host --`
  ([`providers.md`](../reference/providers.md#the-region-preflight)). ⚠ One gap the ruling
  leaves: it counts `AWS_DEFAULT_REGION` alone as a region, and opencode 1.18.32 reads only
  `options.region` and `AWS_REGION` ([§14](#14-evidence-and-how-to-re-check-it)). So with only
  `AWS_DEFAULT_REGION` set, the launch proceeded and opencode still used `us-east-1`. Its binding
  (step 5 of [§12](#12-what-i-would-build-in-order), built 2026-09-29) writes `options.region`
  only from the provider's own `region`, since a derive cannot read an environment value.
  **Closed 2026-09-29 by [BR-D18](#BR-D18)**: packs/opencode says opencode reads `AWS_REGION`
  alone on `aws-bedrock`, and the per-agent ask counts only that for it, so such a launch is
  refused, naming the variable that reached opencode unread.
- **An empty `models` map, or a missing alias:** emit the catalog row and omit the model key. The
  agent resolves its own model.
- **A profile for an agent with no path** writes nothing and warns nothing, like any unreachable
  provider today. That covers agy; oh-omp until its support is read; and copilot until the bridge
  serves a Bedrock provider. As built, none of the three needs `packs/bedrock`, so in a jail of
  them alone `-p bedrock` is refused as undeclared ([BR-D15](#BR-D15)). *(Amended 2026-09-29:
  this bullet also listed codex and opencode under a bridge-forcing profile. As built they write
  their via rows, and the launch refuses them until the bridge has a Bedrock upstream,
  [BR-D16](#BR-D16).)* *(Amended 2026-09-30: the bridge serves a Bedrock provider, and since
  [WG-I44](wire-bridge-gateway.md#WG-I44) carries copilot and oh-omp on plain `-p bedrock`
  wherever it is in the launch. The bullet now covers agy, and copilot and oh-omp in a launch
  with no bridge, where the profile line warns that the selection reaches nothing for them.)*
- **Two Bedrock-marked providers** are two catalog rows, and only the selected one gets a selection.
  *(Superseded 2026-09-29 by [BR-D10](#BR-D10): each agent has one built-in Bedrock provider, so
  only the selected one gets a row.)*

**Failure paths.**
- **Credential absent.** The native provider declares no `api_key_env_name`, so the preflight
  demands nothing. Failure is the agent's AWS auth error at first request. The hand-written API-key
  provider ([§6.3](#63-a-hand-written-api-key-provider-documentation-only)) declares one, and is
  preflighted.
- **Credential expiry.** Only credential 1 and credential 3's SSO session can expire. codex reports
  *"…its AWS signature has expired… If `AWS_BEARER_TOKEN_BEDROCK` is set, update or unset it, then
  restart Codex."* This design refreshes nothing: refresh is
  [`sso-backed-bedrock.md` §7](sso-backed-bedrock.md#7-refresh--what-happens-when-you-log-in-again)'s,
  and nothing refreshes a bearer.
- **Region does not serve the model.** The user sees AWS's model error, or codex's *"Amazon Bedrock
  does not support region …"*. yolo carries no region allowlist, since one would rot within a
  quarter.

**Defaults.** The endpoint family is not a setting. No `region` ships ([OQ-BR9](#OQ-BR9)'s
ruling and [OQ-BR6](#OQ-BR6)): the user sets one, or the credential profile's section of the
host's `~/.aws/config` gives one ([BR-DIR1](#BR-DIR1)), or the launch is refused, naming every
way to set it. This line said `"us-east-1"`, shipped, until 2026-09-29, which would have made the
refusal unreachable. `aws_profile` is `null`, meaning use the chain. The model alias is the profile's
`model` option, else `default` ([OQ-CS3](../reference/providers.md#oq-cs3)). The native bindings add no
timeouts or retries.

**Trigger:** once per launch in the ordinary boot render, plus the host notch's env derive at
`yolo host -- <agent>`. There is no watcher. **One writer:** yolo owns the catalog rows it renders
(`model_providers.amazon-bedrock-runtime`, `provider["amazon-bedrock"]`, pi's `providers` entry)
as managed layers re-asserted every boot. Selection keys ride `selection`, so a user's interactive
choice wins until yolo's own value changes. codex owns `auth.json`.

**Forbidden:** a codex Bedrock field outside the seven; a credential value in the composed table
or `YOLO_PROVIDERS` (the name crosses, and the value is hydrated per derive); a selection whose
provider the catalog dropped; a region allowlist or model catalog in core (the ruled built-in model
pack is a pack).

**What done looks like.** The numbers are kept from the earlier draft; the rest moved.
- **1.** `yolo -p bedrock -- codex` completes one turn against GPT-5.6, in a jail with one
  credential and a region, and `codex doctor` reports `amazon-bedrock-runtime`, `responses` and
  `bedrock-runtime.<region>`. The name is [OQ-BR1](#OQ-BR1)'s; it would be `-p bedrock-gpt` under
  [OQ-BR9](#OQ-BR9) B.
- **1b.** Condition 1 holds under each of the three credentials, one per jail.
- **2.** The same flag completes one turn on opencode and on pi, against the same id.
- **5.** A user entry with no vendor naming an Anthropic id reaches Claude through codex with no
  yolo change. That turn is also the measurement that widens codex's filter
  ([§6.1](#61-the-provider-shape-one-bedrock-provider-or-two)). *(Restated 2026-09-25: the old
  wording, overriding `providers.bedrock-openai.models` to Anthropic ids, contradicted the vendor
  filter.)*
- **7.** claude's native `bedrock` profile completes a turn against an Anthropic id, and its picker
  lists only Anthropic entries.
- **11.** The matrix ([§6.7](#67-the-whole-matrix--every-agent-every-model-every-credential)). On
  a real host, one request per cell completes: each agent, an Anthropic and a non-Anthropic model
  where its transport carries both, and each credential, with each model picked from the agent's
  own menu. It is run by hand from a runbook.

---

## 9. Non-goals

- **No credential lifecycle**: no rotation, minting, refresh daemon or broker. That is
  [`sso-backed-bedrock.md`](sso-backed-bedrock.md)'s, and it is built. The signer reading the chain
  and caching until expiry is consumption.
- **No `~/.aws` mount.** It disables the SSO credential while looking like it works, and yolo refuses it
  wherever a loophole serves container credentials
  ([`sso-backed-bedrock.md` §8](sso-backed-bedrock.md#8-behaviour-this-design-specifies)).
- **No new agent.** copilot and agy get no invented native path.
- **No canonical Converse `wire_api`.** [OQ-BR5](#OQ-BR5) ruled that pi gets both routes. As a
  design consequence, not part of the ruling, the native route binds pi's own
  `bedrock-converse-stream` as a per-agent fact, and the bridge version speaks OpenAI
  chat-completions.
- **No mantle in what yolo ships**: no shipped provider, profile or derive branch names it.
- **Not a rewrite of claude's native mode.** packs/claude's four contributions stay, except as the
  gate fix in [`providers.md`'s credential gate](../reference/providers.md#the-credential-gate) narrows them.

"No bridge for an agent that has a native client" is **withdrawn for pi** by
[OQ-BR5](#OQ-BR5)'s ruling (claude's everything profile was already its exception). For codex,
opencode and every other agent it stands until [OQ-WG5](wire-bridge-gateway.md#OQ-WG5) rules.

---

## 10. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **A hand-written API-key provider only** | **Rejected as primary; kept as a recipe** ([§6.3](#63-a-hand-written-api-key-provider-documentation-only)). It discards SigV4, the chain, codex's `auth.json` mode and opencode's options, makes yolo own a URL, and carries only an API key, so no SSO |
| **Ship mantle too** | **Rejected by [DIR-BR3](#DIR-BR3)** ([§5](#5-one-endpoint-family--runtime-ships-and-why-not-mantle)) |
| **`endpoint_family` as a profile option** | **Cannot work**: it moves the endpoint and leaves the ids |
| **One `bedrock` provider for every agent** | **Reopened by [OQ-BR9](#OQ-BR9)**: the shared-`default` objection is answered by a per-entry vendor |
| **Move `bedrock` out of packs/claude** | **Reopened by [OQ-BR9](#OQ-BR9)**: a provider every agent reads needs an owner every jail selects |
| **Coin `bedrock-converse` as a fourth `wire_api`** | **Not needed** under [OQ-BR5](#OQ-BR5)'s ruling: native is per-agent, and the bridge route is chat-completions |

---

## 11. Risks

| Risk | Mitigation |
| :--- | :--- |
| **R1.** codex renames `amazon-bedrock-runtime` or stops permitting `base_url`; the list changed between 0.145.0 and 0.156.1 | Both are binary strings ([§14](#14-evidence-and-how-to-re-check-it)). Pin the version and re-verify on upgrade; fall back to a hand-written API-key provider ([§6.3](#63-a-hand-written-api-key-provider-documentation-only)) under a non-reserved id |
| **R2.** D4's fallback metadata mis-estimates a 1M-token context window | Measurable in one session. Fix it with an object-form `context_window` ([`model-lists-and-pickers.md`](model-lists-and-pickers.md)); the mantle recipe is the escape meanwhile |
| **R3.** The shared `amazon-bedrock` id drifts between agents | Each derive owns its spelling, so a rename is one line with provenance |
| **R4.** The design ships on schema reading alone | Every done-condition is a live turn, and `codex doctor` settles codex |
| **R5.** IAM also needs `bedrock:InvokeModel` on `arn:aws:bedrock:{region}:{account}:project/default`, which the invoke-only `matt-bedrock` user may lack | Test with the real account; the AccessDenied names the ARN |

R6 to R11 moved with the bridge, model-list and search designs.

---

## 12. What I would build, in order

1. ~~Fix D1~~: done 2026-09-15.
2. ~~**Rule [OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2)**, whose marker the native derives
   key on~~: ruled and built 2026-09-29, the provider field `platform`, which a derive
   reads as `ctx.selected_platform`.
3. ~~**The `bedrock` pack**~~: built 2026-09-29 ([BR-D6](#BR-D6), [BR-D7](#BR-D7),
   [BR-D15](#BR-D15); the list reshaped the same day by [BR-D19](#BR-D19)). The runtime provider,
   its two profiles, the model list and a README that dates each id's source. Its provider declares `"platform": "aws-bedrock"` and
   `region_env_name`, so claude's switch, aws-auth's pointer and the region refusal followed the
   provider into the new pack ([BR-D1](#BR-D1), [PP-D2](providers-and-profiles-redesign.md#PP-D2)).
4. ~~**The codex binding**~~: built 2026-09-29 ([BR-D12](#BR-D12)); `codex doctor` has not run it.
5. ~~**The opencode and pi native bindings**~~: built 2026-09-29 ([BR-D13](#BR-D13),
   [BR-D14](#BR-D14)), each with a provenance comment naming the version read.
6. **Close the gate leaks**, per [`providers.md`'s credential gate](../reference/providers.md#the-credential-gate) and
   [`providers-and-profiles-redesign.md`](providers-and-profiles-redesign.md). The D2 half is
   BUILT (2026-09-26, the credential gate); the D5 half is BUILT too (2026-09-29,
   [OQ-BR8](providers-and-profiles-redesign.md#OQ-BR8)).
7. **The hand-written API-key provider and mantle recipes** in the user guide, with P1 stated where users hit it.
8. **The direction**, once [OQ-BR9](#OQ-BR9) rules:
   1. the signer ([`wire-bridge-gateway.md`](wire-bridge-gateway.md));
   2. ~~**the shared provider with per-entry `vendor` and the derive filters**~~, built
      2026-09-29 ([BR-D6](#BR-D6) to [BR-D11](#BR-D11));
   3. claude's everything profile and pi's bridge route ([`wire-bridge-gateway.md`](wire-bridge-gateway.md));
   4. lists and pickers ([`model-lists-and-pickers.md`](model-lists-and-pickers.md)).
9. **Search**: [`bedrock-web-search.md`](bedrock-web-search.md).
10. **Fold the settled parts into [`providers.md`](../reference/providers.md)**, and retire this doc
    via `system-doc`.

---

## 13. Open Questions

1. ✅ <a id="OQ-BR9"></a>**[OQ-BR9](#OQ-BR9): One Bedrock provider, holding every model family, in its own pack?** With mantle
   not shipped, the question is whether one runtime provider carries every family the org selected,
   and who owns it ([§6.1](#61-the-provider-shape-one-bedrock-provider-or-two)). Stakes: whether a
   new vendor is a list entry or a new provider plus profile; whether `-p bedrock` means one thing
   for every agent; and whether claude's `bedrock` leaves packs/claude.

   | Option | Verdict |
   | :--- | :--- |
   | **A.** One runtime provider, every family, a declared `vendor` per entry, owned by a new `bedrock` pack | **Leaning** |
   | **B.** Keep the split: claude's `bedrock` for Anthropic ids, `bedrock-openai` for the rest | A provider per vendor group and a profile per provider; a company pack must know which one to extend |
   | **C.** A, but infer the vendor from the id prefix | Weaker: prefixes vary by inference profile, user ids defeat it, and [`stringly-typed-references-principle.md`](../reference/stringly-typed-references-principle.md) forbids the match |

   It reopens two [§10](#10-alternatives-considered) rows and decides [OQ-BR1](#OQ-BR1)'s premise.
   It sets which ids the built-in pack targets ([OQ-BR3](model-lists-and-pickers.md#OQ-BR3)). It
   makes `-p codex=bedrock` the ordinary gesture, which
   [OQ-BR4](../reference/providers.md#oq-br4)'s ruling (*"no I don't want it to leak"*) makes
   safe, and which is built (2026-09-26). The provider must not declare `web_search`, since on runtime that is false.
   [OQ-PP2](providers-and-profiles-redesign.md#OQ-PP2) may later reshape providers, and does not
   block this.

   _Leaning:_ A. The provider moves into a new `bedrock` pack under its existing name, so
   `-p bedrock` keeps working for claude and starts working for everyone. The same pack ships the
   AgentCore search preset ([`bedrock-web-search.md`](bedrock-web-search.md)). The vendor is
   declared, never parsed.

   <!-- vantage: oq id=OQ-BR9 -->

   **Answer:**
   > **Ruled 2026-09-29: A, with the packaging left to the build.** The maintainer: *"I guess we
   > can split these into different packs if that's convenient, but I do still want one pack that
   > pulls them all in."* One `bedrock` provider holding every model family (each entry declaring
   > its maker, never parsed from the id) is the leaning's shape; the build may split it across
   > packs where that is simpler, provided one `bedrock` pack selects all of it, and every agent
   > pack that binds Bedrock `needs` that pack, so `"packs": ["codex"]` alone still gets `-p
   > bedrock`. The traps stand: the provider keeps its six AWS credential names (the gate
   > withholds by them), ships no default region ([OQ-BR6](#OQ-BR6)), and an agent's fallback is
   > the first model that agent can call.

2. ✅ <a id="OQ-BR1"></a>**[OQ-BR1](#OQ-BR1): What does a user type to put an agent on Bedrock, and is "native client or wire
   bridge" something they name?** The maintainer, 2026-09-25, on the earlier version: *"I have no
   idea what any of these are. This is very unclear … maybe bedrock-codex would be the native,
   which is really only useful against the codex agent? would you want that against claude code?
   straight bedrock is better? isn't it really provider native and then the wire bridge. I'm
   actually not sure where the split ended up."*
   [Where the split ended up](#where-the-split-ended-up) answers the last sentence.

   The earlier version weighed vendor names (`bedrock-gpt`, `bedrock-openai`) and `-mantle` names.
   [DIR-BR3](#DIR-BR3) removed the second, and [OQ-BR9](#OQ-BR9)'s leaning removes the need for the
   first. What remains is the axis the maintainer named. Stakes: profile names are typed, and
   expensive to change, and a name that means different things per agent is the confusion itself.

   | Option | Verdict |
   | :--- | :--- |
   | **A.** `-p bedrock` means Bedrock in every agent: its native client where one exists (codex, opencode, pi, and claude for Anthropic), the bridge where none does (copilot, oh-omp). One more profile forces the bridge, spelled for the transport (proposed `bedrock-bridge`) | **Leaning** |
   | **B.** A profile per agent (`bedrock-codex`) | Adds nothing: native is already each agent's default, and `-p codex=bedrock` already scopes a pick |
   | **C.** A profile per vendor (`bedrock-gpt`) | Meaningful only under [OQ-BR9](#OQ-BR9) B; under A the vendor is on each entry and the picker chooses |
   | **D.** `bedrock-native` beside `bedrock-bridge` | The native name is a synonym of `bedrock` everywhere |

   Under A, `-p bedrock-bridge` means claude's everything profile and pi's bridge version
   ([OQ-BR5](#OQ-BR5)). For copilot and oh-omp it is the same as `-p bedrock`. For codex and
   opencode it writes nothing until [OQ-WG5](wire-bridge-gateway.md#OQ-WG5) gives them a bridge
   route, and for agy it writes nothing. claude's
   `-p bedrock` keeps its shipped meaning. Two later questions do not block this:
   - how all-traffic mode is switched on, [OQ-WG2](wire-bridge-gateway.md#OQ-WG2), and which
     native-client agents get a bridge route, [OQ-WG5](wire-bridge-gateway.md#OQ-WG5);
   - whether transport becomes a first-class provider fact,
     [OQ-PP2](providers-and-profiles-redesign.md#OQ-PP2), which may later subsume this.

   _Leaning:_ A. One name, `-p bedrock`, means Bedrock everywhere, by each agent's native client
   where it has one and the bridge where it does not. The only second name forces the bridge, and
   its spelling is yours to rule. No profile is named for an agent or a vendor. Rule it with
   [OQ-BR9](#OQ-BR9).

   <!-- vantage: oq id=OQ-BR1 -->

   **Answer:**
   > **Ruled 2026-09-29, as leaned.** The maintainer: *"we get to select it and point it at an
   > agent just like everywhere else … by default we should pick the right thing, the native by
   > default, but we should allow configurations to force the bridge."* `-p bedrock` (or `-p
   > codex=bedrock` for one agent) puts an agent on Bedrock through its own Bedrock client where
   > it has one, and through the wire bridge where it has none. One shipped profile forces the
   > bridge, `bedrock-bridge`, which is also claude's route to non-Anthropic models; a user's own
   > profile can force it the same way with `via`. No profile is named for an agent or a model
   > maker.

3. ✅ <a id="OQ-BR5"></a>[**OQ-BR5**](#OQ-BR5) (ruled 2026-09-25): **Is pi bound through its native
   Converse client, or through runtime's OpenAI-compatible route?**

   <!-- vantage: oq id=OQ-BR5 -->

   Both ([Decision Ledger](#decision-ledger)). The
   native half is [§6.2](#62-what-each-derive-emits)'s pi row. The bridge half is pi's OpenAI
   chat-completions client pointed at the bridge's sign-only route
   ([`wire-bridge-gateway.md`](wire-bridge-gateway.md)). Its premise: a hand-written API-key provider
   would leave pi needing a bearer because of yolo, not AWS (INFERRED); pi-ai 0.87.1 ships the built-in
   (SOURCED).
4. ✅ <a id="OQ-BR6"></a>[**OQ-BR6**](#OQ-BR6) (ruled 2026-09-25, as its leaning): **Refuse the
   launch when no region is resolvable?**

   <!-- vantage: oq id=OQ-BR6 -->

   Only when yolo can see none; the rule is in
   [§8](#8-behaviour-this-design-fixes). Its premise: codex refuses and names the sources, while
   opencode 1.18.32 and pi-ai 0.87.1 fall silently to `us-east-1` (read, not run), and so does
   Claude Code 2.1.285 (read statically 2026-09-29). **Built 2026-09-29**; the implementation
   decisions are [BR-D1](#BR-D1) to [BR-D5](#BR-D5).

   > [!NOTE]
   > **The re-ask, answered 2026-09-29 by [BR-DIR1](#BR-DIR1), and built.** It asked whether
   > yolo should read `~/.aws/config` for the effective profile at the host notch only, since
   > claude's resolver reads the shared-config region of the active `AWS_PROFILE` before
   > falling back to `us-east-1` ([BR-D5](#BR-D5)). The direction was the same at every notch:
   > yolo reads the region of the profile the credential comes from in the host's file and
   > delivers it, at `yolo host` and in a jail alike. The decisions are [BR-D20](#BR-D20) to
   > [BR-D26](#BR-D26).

5. ✅ <a id="OQ-BR7"></a>[**OQ-BR7**](#OQ-BR7) (answered 2026-09-25 by [DIR-BR3](#DIR-BR3)): **Is
   `endpoint_family` its own field?**

   <!-- vantage: oq id=OQ-BR7 -->

   No; with one family there is nothing to name. The fact it
   protected now shows as the mantle recipe being its own provider.

6. ✅ <a id="OQ-BR11"></a>[**OQ-BR11**](#OQ-BR11) (ruled 2026-09-24): **How does claude use native
   Bedrock for Anthropic ids and the bridge for the rest?**

   <!-- vantage: oq id=OQ-BR11 -->

   Both profiles, with the everything
   profile routing by model id; [`wire-bridge-gateway.md`](wire-bridge-gateway.md) owns the routing
   mechanism. It touched [OQ-BR8](providers-and-profiles-redesign.md#OQ-BR8): the everything
   profile must not set claude's `CLAUDE_CODE_USE_BEDROCK`, yet the credential pointer must reach
   the bridge. Built 2026-09-29 as a transport check
   ([PP-D4](providers-and-profiles-redesign.md#PP-D4)): claude's derive sets the switch only for a
   profile that routes through no via service, and aws-auth's pointer keys on the platform alone.

7. 💬 <a id="OQ-BR24"></a>[**OQ-BR24**](#OQ-BR24): **Do copilot and oh-omp need `bedrock` and `wire-bridge`,
   so either alone gets `-p bedrock`?** Since 2026-09-30 the bridge carries both on plain
   `-p bedrock`, because neither has a Bedrock client of its own
   ([`wire-bridge-gateway.md` WG-I44](wire-bridge-gateway.md#WG-I44)). But it carries them only
   where another selected pack already brings the provider, `aws-auth` and the bridge in, since
   neither pack needs them ([BR-D15](#BR-D15)). So `"packs": ["copilot"]` alone gets nothing on
   `-p bedrock`, while `"packs": ["codex"]` alone does, by [OQ-BR9](#OQ-BR9)'s ruling that every
   agent binding Bedrock needs the pack. Needing them puts the Bedrock provider, `aws-auth` and an
   idle bridge into every copilot or oh-omp jail, Bedrock or not.

   <!-- vantage: oq id=OQ-BR24 leaning="Yes: both need bedrock and wire-bridge, so one pack alone works on -p bedrock the way codex does, at the cost of an idle bridge in each such jail." -->

   _Leaning:_ yes, as [OQ-BR9](#OQ-BR9) ruled for the agents with their own client: a user should
   not have to select a second agent to make `-p bedrock` work for the first.

   > **Answer:**

### Decision Ledger

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| [OQ-BR9](#OQ-BR9) | **Maintainer ruling:** one provider for every model family, packaging free, one `bedrock` pack pulling all of it in | 2026-09-29 | [§6.1](#61-the-provider-shape-one-bedrock-provider-or-two) | ✅ 2026-09-29: `packs/bedrock`, needed by claude, codex, opencode and pi, the six credential names kept, no region, each agent's fallback its first callable entry ([BR-D6](#BR-D6) to [BR-D11](#BR-D11), [BR-D15](#BR-D15)). Pinned by `TestEveryBedrockAgentAloneGetsTheBedrockProfile` and `TestOnlyTheBindingAgentsNeedBedrock` |
| [OQ-BR1](#OQ-BR1) | **Maintainer ruling:** `-p bedrock` everywhere, native client by default, `bedrock-bridge` or a user profile's `via` forces the bridge | 2026-09-29 | [§12](#12-what-i-would-build-in-order) | ✅ 2026-09-29 for the native half: codex, opencode and pi on their own clients, claude's filtered ([BR-D9](#BR-D9) to [BR-D14](#BR-D14)); `bedrock-bridge` ships ([BR-D16](#BR-D16)), and since 2026-09-30 carries every agent, copilot and oh-omp included, the bridge reaching Bedrock by region ([`wire-bridge-gateway.md` WG-I39](wire-bridge-gateway.md#WG-I39)); and the bridge half for `-p bedrock`, 2026-09-30: copilot and oh-omp, which have no Bedrock client, carried through the bridge wherever it is in the launch ([`wire-bridge-gateway.md` WG-I44](wire-bridge-gateway.md#WG-I44)), pinned by `TestPlainBedrockCarriesOnlyTheAgentsWithNoClientOfTheirOwn` and `TestPlainBedrockCarriesTheClientlessAgentsThroughTheBridge` |
| <a id="DIR-BR1"></a>DIR-BR1 | **Every agent reaches every Bedrock model its transport can carry, and every picker shows a current list an org can shape with one pack.** Given as a direction in review: *"Claude should be able to use all of those models with basically no change."* | 2026-09-24 | [§6.5](#65-every-bedrock-model-in-every-agent--the-direction) | — |
| <a id="DIR-BR2"></a>DIR-BR2 | **The whole matrix**: every agent, every model its transport carries, every supported credential with SSO included, and every model one pick in the agent's menu. *"We need to support single sign on through all of the methods so I can use any model… So we got to support everything everywhere, the whole matrix."* agy is the one hole | 2026-09-24 | [§6.7](#67-the-whole-matrix--every-agent-every-model-every-credential) | — |
| [OQ-BR11](#OQ-BR11) | **Both claude profiles: native and everything.** *"if you only do native or the rest, then you'll never be able to switch between Claude and, say, OpenAI in one Claude session, which I think we'll want. So I guess just both options."* The bridge routes by model id: Anthropic ids pass untranslated to runtime's Messages, and the rest are translated. It amends the bridge's one-upstream rule. The leaning (two profiles, no routing) was overruled | 2026-09-24 | [Where the split ended up](#where-the-split-ended-up); routing in [`wire-bridge-gateway.md`](wire-bridge-gateway.md) | The routing, 2026-09-29 ([`wire-bridge-gateway.md` §3.1](wire-bridge-gateway.md#31-how-it-is-built)): on a Bedrock upstream the bridge reaches, a model the list declares Anthropic's goes untranslated to runtime's `/anthropic/v1/messages`, the rest translated. The everything profile reaches it since the bridge's region-composed URL, 2026-09-30 ([`wire-bridge-gateway.md` §8](wire-bridge-gateway.md#8-build-order), step 1) |
| <a id="DIR-BR3"></a>DIR-BR3 | **yolo ships `bedrock-runtime` only; mantle is a documented manual recipe.** *"let's skip mantle"*. The reasons are in [§5](#5-one-endpoint-family--runtime-ships-and-why-not-mantle). It is revisited when a model or feature only mantle serves is needed. `-p bedrock-gpt-mantle` and every mantle provider are gone. A direction, so no question id | 2026-09-25 | [§5](#5-one-endpoint-family--runtime-ships-and-why-not-mantle) | — |
| [OQ-BR7](#OQ-BR7) | **Moot under DIR-BR3**: one family, so no `endpoint_family` field. The leaning (its own field) had nothing left to decide | 2026-09-25 | [§6.1](#61-the-provider-shape-one-bedrock-provider-or-two) | — |
| [OQ-BR5](#OQ-BR5) | **Both: pi's native Converse, and a bridge version.** *"native converse is just basically the pi agent's native which we're supplying for all the others and then also there will be the bridge version because I'm sure there will be some different features we can offer and we're just going to want to support both as fully as possible."* | 2026-09-25 | [§6.2](#62-what-each-derive-emits) (native); [`wire-bridge-gateway.md`](wire-bridge-gateway.md) (bridge). Design consequence, not part of the ruling: no canonical Converse `wire_api` is coined, and the bridge version speaks OpenAI chat-completions ([§9](#9-non-goals)) | — |
| [OQ-BR6](#OQ-BR6) | **As its leaning:** *"Refuse only when the provider declares no region AND no AWS_REGION / AWS_DEFAULT_REGION is in the composed jail environment — both of which yolo can see at launch. Anything beyond that (an ~/.aws/config region) is unproven, and unproven emits nothing."* Its re-ask was answered by [BR-DIR1](#BR-DIR1) | 2026-09-25 | [§8](#8-behaviour-this-design-fixes) | ✅ 2026-09-29: `packload.ProviderRegionGaps` and `ProviderRegionRefusal`, called at every notch; the review's fixes [BR-D1](#BR-D1), [BR-D4](#BR-D4), [BR-D5](#BR-D5) and the config-ref entry (`TestEveryProviderContributionFieldIsDocumented`); pinned by `TestCheckProviderCredentialsAsksForTheRegion`. The `~/.aws/config` region counts since [BR-DIR1](#BR-DIR1) |
| <a id="BR-D1"></a>BR-D1 | *Implementation decision, revised 2026-09-29 by the review.* **The requirement keys on the provider's `platform`; a pack declares the platform's region variables with `region_env_name`.** Every selected provider whose composed entry declares a platform some selected pack declares variables for (on a provider of that platform, which packdecl refuses without a `platform` beside it) requires a region from those variables. the `bedrock` provider declares `AWS_REGION` and `AWS_DEFAULT_REGION` for `aws-bedrock` (in packs/claude when this was built, in packs/bedrock since [BR-D15](#BR-D15)), so a user's own `providers.bedrock-eu` with `"platform": "aws-bedrock"` is required a region without restating them. Core names no provider, no platform and no AWS variable. The first build made the field a per-provider opt-in on the premise that the marker was [OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2)'s open question; [OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2) was ruled the same day, so a user's Bedrock provider got the credential and derive behavior and never the refusal, and the review found it. A user's `providers` entry declares a platform, not its variables, so a user provider whose platform no selected pack declares variables for carries none | 2026-09-29 | [§8](#8-behaviour-this-design-fixes) | ✅ `packload.regionRequirements`; pinned by `TestTheRegionRequirementKeysOnThePlatform` |
| <a id="BR-D2"></a>BR-D2 | *Implementation decision.* **"The composed jail environment" is what reaches the agent.** In a jail that is env_sources, a selected pack's env, the profile's provider environment and, on a container, the argv's `-e` pairs, and never the shell yolo was launched from, which no backend forwards and nothing relays a region out of. A region found only in that shell is named in the refusal as not delivered. At `yolo host` the exec'd environment includes that shell, so it counts there, and an env_sources `null` removing `AWS_REGION` removes it. *Kept through the region fill, 2026-09-29, from the review:* the first build of [BR-DIR1](#BR-DIR1) gave the jail's agent the file's region over a region variable, or read the default profile over an `AWS_PROFILE`, left in that shell, so `AWS_REGION=ap-south-1 AWS_PROFILE=prod yolo -p bedrock -- claude` ran in the default profile's region, disclosed as chosen "since nothing names another". Such a variable is now `RegionFileSource.Stranded` at the jail notch: the file is not read, and the refusal names it, offering to deliver the profile through env_sources. Relaying the shell's value into the jail was the other option, and was not built: nothing relays that shell, by this row. `AWS_PROFILE=default` there names the profile read anyway, and a profile aws-auth serves decides first | 2026-09-29 | [§8](#8-behaviour-this-design-fixes) | ✅ each notch's `RegionAsk` lookup; per agent since [BR-D4](#BR-D4); pinned by `TestARegionOnlyInTheLaunchShellIsNamedAndNotCounted`, and through the fill by `TestTheRegionFillLeavesTheLaunchShellsChoiceToTheRefusal` and `TestAJailLaunchDoesNotReplaceARegionOrProfileLeftInTheLaunchShell` |
| <a id="BR-D3"></a>BR-D3 | *Implementation decision.* **The refusal is the provider pre-flight's second half and honors its hatch**, `YOLO_ALLOW_MISSING_PROVIDERS=1`, as a loud continuation. It runs wherever the credential half runs, so no arm can ask one question without the other. The one launch that may need the hatch is an agent that does read a region the refusal cannot count, such as an `~/.aws/config` one | 2026-09-29 | [§8](#8-behaviour-this-design-fixes) | ✅ `ProviderRegionRefusal`; pinned by `TestTheRegionRefusalHonorsTheProviderHatch` |
| <a id="BR-D4"></a>BR-D4 | *Implementation decision, from the review.* **The region is asked of each agent on the provider.** The first build answered "is a region delivered" through the launch-wide lookup, which counts a value reaching any process; since the credential gate, delivery is per agent, so claude on `bedrock` and codex on a profile whose gated env gives codex alone `AWS_REGION` passed, and claude started with none. `ProviderRegionGaps` takes one ask per agent whose profile selects a provider, each answered from what reaches that agent (the env_sources the gate delivers to it, the shared pack env, its own gated env and shape vars, `CredentialScope.DeliveredTo`, and on a container the argv's `-e` pairs), and the refusal names the agents that receive none | 2026-09-29 | [§8](#8-behaviour-this-design-fixes) | ✅ `packload.RegionAsk`; pinned by `TestTheRegionIsAskedOfEachAgentOnTheProvider` |
| <a id="BR-D5"></a>BR-D5 | ⚠ **Revised 2026-09-29 by [BR-DIR1](#BR-DIR1)**: yolo reads that file now, delivers its region ([BR-D20](#BR-D20)), and the refusal's "not counted" line is gone. The row as decided: *Implementation decision, from the review.* **The refusal says an `~/.aws/config` region is not counted because yolo does not read it**, not that an agent reading it is "unproven": Claude Code's resolver reads `AWS_REGION`, then `AWS_DEFAULT_REGION`, then the shared-config region for the active `AWS_PROFILE` (through the AWS SDK's `loadConfig` with `NODE_REGION_CONFIG_FILE_OPTIONS`), then falls back to `"us-east-1"`. The reviewer read it in 2.1.284; re-read statically in 2.1.285 on 2026-09-29 (`grep -a readAwsSharedConfigRegion` on the installed binary), never run. The premise is corrected to name claude beside opencode and pi. Whether the host notch should count that region is put back to the maintainer ([OQ-BR6](#OQ-BR6)); until then the hatch covers it | 2026-09-29 | [§8](#8-behaviour-this-design-fixes) | Revised: the wording's absence is pinned by `TestProviderRegionRefusalWordsTheVerdictAndTheHatch` |
| <a id="BR-D6"></a>BR-D6 | *Implementation decision.* **A model's vendor is a `model_options` fact on a pack's provider, and a key of a user's object-form model entry.** The ruled `models` contribution kind ([OQ-BR12](model-lists-and-pickers.md#OQ-BR12)) is unbuilt, and `model_options` is the per-model fact channel the `openai-codex` list already uses ([ML-D1](model-lists-and-pickers.md#ML-D1)), so the one declaration needed no new schema. A user writes `{"id": …, "vendor": …}` under `providers.<name>.models`, lowered to the same flat key. Both layers check only the shape, one lowercase token (`packdecl.ValidModelVendor`); core interprets no value, and an entry with no vendor is offered to every agent | 2026-09-29 | [§6.1](#61-the-provider-shape-one-bedrock-provider-or-two) | ✅ `packs/bedrock/pack.json`, `config.validateModelEntry`, `packload.flattenModelFacts`; pinned by `TestComposeProvidersLowersAUserModelsVendor` and `TestAProviderModelVendorIsShapeChecked` |
| <a id="BR-D7"></a>BR-D7 | ⚠ **Superseded 2026-09-29 by [BR-D19](#BR-D19)**, which drops GPT-6 Sol: three ids ship, Claude Opus 5.5, GPT-6.1 Sol and GPT-6 Astra, in that order. The row as decided: *Implementation decision, corrected 2026-09-29 by the review.* **Four ids ship, each read from its AWS model card on 2026-09-29, and none is a `default` alias.** Claude Opus 5.5 (`global.`), GPT-6 Sol (`global.`), GPT-6.1 Sol (`us.`, the only runtime id AWS offers for it) and GPT-6 Astra (`global.`), ordered in that sequence by the `order` fact. The maintainer's rule was GPT-6.1 Sol where Bedrock offers it and GPT-6 Sol where it offers only that: AWS offers GPT-6.1 Sol through the US inference profile alone, and GPT-6 Sol through `us.` and `global.`, whose source Regions include the EU, Asia Pacific and South America, so both ship. The first build shipped no GPT-6 Sol, on a reading that AWS published no page for it; the page exists (launched 2026-09-22), and the review found it. A `default` alias would steer claude's own Bedrock client, which [OQ-ML2](model-lists-and-pickers.md#OQ-ML2) forbids, so each agent's default is its first callable entry, which [BR-D17](#BR-D17) orders. The sources are dated in the pack README | 2026-09-29 | [§5.3 of model-lists](model-lists-and-pickers.md#53-the-prerequisite-verify-before-an-id-ships) | ✅ pinned by `TestTheShippedBedrockListDeclaresEachEntrysMaker` |
| <a id="BR-D8"></a>BR-D8 | *Implementation decision.* **One Lua helper decides, for one agent, which entries it can call and which it starts on**, copied verbatim into the four binding derives because a derive cannot load another file. `callableModels` orders the entries by `order` and keeps those whose vendor the agent's client serves, or that name none. `callableModel` takes the profile's `model` when it names such an entry (as an alias or an id) or an id the provider does not list at all, which passes through as the user wrote it; else the provider's `default` alias when callable; else, for an agent yolo must pick for, the first callable entry. A profile naming a listed entry the agent cannot call is skipped, never sent | 2026-09-29 | [§6.1](#61-the-provider-shape-one-bedrock-provider-or-two) | ✅ pinned identical by `TestBedrockModelListHelperIsIdenticalInEveryDerive` |
| <a id="BR-D9"></a>BR-D9 | *Implementation decision.* **claude on its own Bedrock client pins a model only when the profile or a user `default` alias names an Anthropic entry**, and otherwise none. Claude Code starts on an Anthropic model of its own there, which is a valid session, and [OQ-ML2](model-lists-and-pickers.md#OQ-ML2) rules that yolo does not steer one. A profile over a Bedrock provider with a `via` and no anthropic endpoint pins no model either, since claude runs on its own login there. The native client ignores an anthropic endpoint on the provider, which can only be the bridge's | 2026-09-29 | [§6.2](#62-what-each-derive-emits) | ✅ `packs/claude/derive.lua`; pinned by `TestClaudeOnBedrockStartsOnlyOnAnAnthropicModel`, and its three branches (no bridge address under the native client, tier aliases held to Anthropic's, no model on a bridged profile) by `TestClaudeOnBedrockTakesNothingItsOwnClientCannotUse`, added after a review deleted each with the unit gate green |
| <a id="BR-D10"></a>BR-D10 | *Implementation decision.* **A native row is written only for the selected Bedrock provider, only on the agent's own transport, and a Bedrock provider never gets a generic row.** Each agent has one built-in Bedrock provider id, so two Bedrock providers cannot both have one; the credential gate withholds the AWS credentials from an agent that did not select the provider anyway; and a generic row carries one key, while Bedrock's credential is the AWS chain only the agent's own client signs with. This supersedes [§8](#8-behaviour-this-design-fixes)'s "two Bedrock-marked providers are two catalog rows" | 2026-09-29 | [§8](#8-behaviour-this-design-fixes) | ✅ the codex, opencode and pi derives; the no-generic-row rule pinned by `TestABedrockProviderWithAnEndpointGetsNoGenericRow`, over a Bedrock provider that names an endpoint, since the shipped one names none and passed with the rule deleted |
| <a id="BR-D11"></a>BR-D11 | *Implementation decision.* **Which makers each client calls:** claude Anthropic's (Messages serves Claude only); codex OpenAI's (it drives Responses, and the Claude Opus 5.5 card lists no Responses API on runtime); opencode and pi every maker's (both drive Converse, which each shipped entry's card lists) | 2026-09-29 | [the per-agent table](#where-the-split-ended-up) | ✅ the four derives |
| <a id="BR-D12"></a>BR-D12 | *Implementation decision.* **codex's binding is `model_provider = "amazon-bedrock-runtime"`, the model, and an override carrying `aws.region` only for a region the provider declares.** Read from the codex-cli 0.158.0 binary's strings: the built-in id, the runtime URL composed from the region and the seven-field override guard. The region order (the override, `AWS_REGION`, `AWS_DEFAULT_REGION`) is INFERRED: the one string naming it is about the mantle provider's key and bearer-token auth (corrected 2026-09-29, from the review). No `aws.profile`: `AWS_PROFILE` reaches codex's credential chain directly. That runtime takes the id unchanged is INFERRED from the fallback-metadata message (trap D4) | 2026-09-29 | [§6.2](#62-what-each-derive-emits) | ✅ `packs/codex/derive.lua`; pinned by `TestCodexOnBedrockUsesItsOwnRuntimeClient` |
| <a id="BR-D13"></a>BR-D13 | *Implementation decision.* **opencode's binding is `provider["amazon-bedrock"]` with the list's models and `options.region` only from the provider**, no `npm`, no endpoint, and the selection `amazon-bedrock/<id>` with `enabled_providers` naming it. Read from the opencode 1.18.32 binary's strings. ⚠ opencode reads `AWS_REGION` and not `AWS_DEFAULT_REGION`, and the derive cannot see an environment value to copy, so [§8](#8-behaviour-this-design-fixes)'s gap for a region only the latter delivers is closed by refusing the launch instead ([BR-D18](#BR-D18)) | 2026-09-29 | [§6.2](#62-what-each-derive-emits) | ✅ `packs/opencode/derive.lua`; pinned by `TestOpencodeOnBedrockUsesItsOwnClient` |
| <a id="BR-D14"></a>BR-D14 | *Implementation decision.* **pi's binding lists every entry under its built-in `amazon-bedrock`, with `models` alone, and hands pi a provider-declared region as `AWS_REGION`.** Read from pi 0.99.1's installed sources: a models-only row keeps each model on pi's Converse client, taking `api` and base URL from pi's own catalog, and pi reads its region from the environment. [§6.2](#62-what-each-derive-emits)'s "adds only ids the built-in lacks" is not buildable, since a derive cannot read pi's catalog, so a row replaces pi's entry of the same id and carries the window, output cap, inputs and reasoning the list declares; pi's cost and thinking levels for that id are lost. The scope and pi-subagents' policy are the list, the start model first | 2026-09-29 | [§6.2](#62-what-each-derive-emits) | ✅ `packs/pi/derive.lua`; pinned by `TestPiOnBedrockUsesItsOwnConverseClient` and `TestPiOnBedrockReceivesTheProvidersRegion` |
| <a id="BR-D15"></a>BR-D15 | *Implementation decision.* **The agent packs that bind Bedrock need `packs/bedrock`, and it needs `aws-auth`.** claude, codex, opencode and pi; the aws-auth need moved from packs/claude to the pack that ships the provider it serves. copilot, oh-omp and agy do not need it: none has a Bedrock client, and the bridge has none to offer them, so a `-p bedrock` there is refused as a profile the launch does not declare rather than accepted to configure nothing. The need is pinned to the derives: a pack whose derive keys on `aws-bedrock` needs the pack, and one that does not, does not. *(Amended 2026-09-30: "the bridge has none to offer them" no longer holds. The bridge reaches the shipped `bedrock` by region ([WG-I39](wire-bridge-gateway.md#WG-I39)) and carries copilot and oh-omp on `-p bedrock` wherever it is in the launch ([WG-I44](wire-bridge-gateway.md#WG-I44)). The need is unchanged, so beside them alone `-p bedrock` is still undeclared; whether they should need `bedrock` and `wire-bridge` is not decided here, and is asked in this doc's opening **Needs your ruling** line.)* | 2026-09-29 | [§6.1](#61-the-provider-shape-one-bedrock-provider-or-two) | ✅ pinned by `TestOnlyTheBindingAgentsNeedBedrock` and `TestBedrockNeedsAWSAuth` |
| <a id="BR-D16"></a>BR-D16 | *Implementation decision.* **`bedrock-bridge` ships as `{provider: bedrock, via: wire-bridge}`, and under it no agent runs its own Bedrock client.** codex gets its via row (Responses at its via URL), as pi, opencode and oh-omp already do. Until the bridge reached a provider named by region alone ([`wire-bridge-gateway.md` §8](wire-bridge-gateway.md#8-build-order), step 1, built 2026-09-30 as [WG-I39](wire-bridge-gateway.md#WG-I39)), the launch's via gate ([WG-I13](wire-bridge-gateway.md#WG-I13), [WG-I15](wire-bridge-gateway.md#WG-I15)) refused codex, pi, opencode and oh-omp and warned claude, which started on its own login; since then the bridge carries all five. Falling back to the native client was rejected: the profile asked for the bridge, and the gate's "the via has no effect" would then have been false for codex | 2026-09-29 | [OQ-BR1](#OQ-BR1) | ✅ pinned by `TestTheShippedBedrockBridgeProfileMeetsEachAgentAsItCan` and the per-agent `…OnABridgedBedrockProfile…` tests |
| <a id="BR-D17"></a>BR-D17 | ⚠ **Superseded 2026-09-29 by [BR-D19](#BR-D19)**, the maintainer's answer to its re-ask: codex starts on GPT-6.1 Sol in every Region, and no start model is chosen to be callable everywhere. The row as decided: *Implementation decision, from the review; put back to the maintainer.* **Every agent's start model is a `global.` entry, so the pick is valid in every Region.** yolo picks only to make a session valid ([OQ-ML2](model-lists-and-pickers.md#OQ-ML2)) and ships no region ([OQ-BR6](#OQ-BR6)), so the entry an agent falls back to must be callable from whatever Region the user sets. A geography's id is callable only from that geography's source Regions: with GPT-6.1 Sol (`us.` only) first among OpenAI's, codex started on a model AWS refuses from eu-west-1, which the integration test then pinned. So Claude Opus 5.5 leads (opencode and pi start on it) and GPT-6 Sol precedes GPT-6.1 Sol (codex starts on it); in the US a user names GPT-6.1 Sol with a profile's `model`. Choosing by Region in a derive was not built: the derive sees only a provider-declared region, never one the environment delivers, and a map of which Regions a geography serves is the region allowlist [§8](#8-behaviour-this-design-fixes) forbids. Whether US users should start on GPT-6.1 Sol by default is the maintainer's to rule, and is asked in this doc's opening **Needs your ruling** line | 2026-09-29 | [§8](#8-behaviour-this-design-fixes) | Superseded: its pin, `TestEachBedrockAgentStartsOnAModelEveryRegionCanCall`, was replaced by [BR-D19](#BR-D19)'s |
| <a id="BR-D18"></a>BR-D18 | *Implementation decision, from the review.* **An agent is asked for a region among the variables it reads, and opencode reads `AWS_REGION` alone.** A program contribution may declare `platform_regions: [{platform, region_env_name}]`, the region variables that program reads on that platform, replacing for that agent alone the list the platform's provider declares; the per-agent ask ([BR-D4](#BR-D4)) then counts only those, and names a platform variable that reached the agent unread. packs/opencode declares `AWS_REGION` for `aws-bedrock`, since opencode 1.18.32's loader takes `options.region`, else `AWS_REGION`, else `"us-east-1"` (read from the installed binary, never run). So a launch delivering opencode only `AWS_DEFAULT_REGION`, which passed the refusal and sent every request to `us-east-1`, is refused. Exporting `AWS_REGION=${AWS_REGION:-$AWS_DEFAULT_REGION}` in opencode's launch was the other option; it was not built, because an env derive's values are literals at every notch, and the copy would be a second meaning of a variable written in two writers (the jail's agent-env file and the host's exec environment). Core names no variable: which one a binary reads is its pack's fact, like `platform_switches` | 2026-09-29 | [§8](#8-behaviour-this-design-fixes) | ✅ `packdecl.PlatformRegion`, `packload.agentRegionVars`, `packs/opencode/pack.json`; pinned by `TestOpencodeOnBedrockIsNotGivenARegionItDoesNotRead` (the jail entry point), `TestBedrockRefusesOpencodeARegionItDoesNotRead` (a real launch), `TestAProgramIsAskedOnlyForTheRegionVariablesItReads` and `TestTheShippedOpencodeReadsOnlyAWSRegionOnBedrock` |
| <a id="BR-D19"></a>BR-D19 | **Maintainer ruling** on [BR-D17](#BR-D17)'s re-ask: *"let's just use 6.1 everywhere. Let's not auto-detect anything. I'm sure it will be there soon. People can override it if they need to. I just don't think that's our top issue at the moment. So yeah, just 6.1 everywhere. Forget the region specificness."* Every agent that yolo starts on an OpenAI model on Bedrock starts on GPT-6.1 Sol (`us.openai.gpt-6.1-sol`) in every Region, with no Region detection. Read with the earlier menu rule (*drop GPT-6 Sol where 6.1 exists, keep it where 6.1 is not available yet*): with 6.1 treated as available everywhere, GPT-6 Sol leaves the shipped Bedrock list. A user outside 6.1's Regions names another model in a profile's `model`; an id the provider does not list passes through as written. Supersedes BR-D17's global-first pick for OpenAI's models | 2026-09-29 | [§8](#8-behaviour-this-design-fixes) | ✅ 2026-09-29: `packs/bedrock/pack.json` ships Claude Opus 5.5, GPT-6.1 Sol and GPT-6 Astra in that order, so the one `order` fact decides every start and no derive reads a Region; pinned by `TestEachBedrockAgentStartsOnTheRuledModelInEveryRegion` (the order, both start models, no GPT-6 Sol), `TestCodexOnBedrockUsesItsOwnRuntimeClient` (GPT-6.1 Sol under a provider region of eu-west-1) and `TestBedrockRendersEachAgentsOwnClient` (the same at a real launch) |
| <a id="BR-DIR1"></a>BR-DIR1 | **Maintainer direction** on [OQ-BR6](#OQ-BR6)'s re-ask (neither counting the host's `~/.aws/config` region only at `yolo host` nor refusing everywhere): *"I don't see why we can't wire this also in the jail. Like, we can provide this into the jail. Why wouldn't we? Should be the same on the host in the jail, and it should follow that."* yolo reads the region of the profile a launch's AWS credential comes from, in the host's `~/.aws/config`, and delivers it as the region the agent reads, at `yolo host` and in a jail alike; the region refusal then counts it. Revises [BR-D5](#BR-D5)'s "yolo does not read it" | 2026-09-29 | [§8](#8-behaviour-this-design-fixes) | ✅ 2026-09-29: the region fill, [BR-D20](#BR-D20) to [BR-D26](#BR-D26); pinned at the gate by `TestTheRegionFillReadsTheProfileTheCredentialComesFrom`, at the jail by `TestAJailLaunchDeliversTheRegionOfTheHostsAWSConfig`, at the host by `TestHostLaunchTakesTheRegionOfTheHostsAWSConfig` and `TestHostEnvShowsTheRegionOfTheHostsAWSConfig`, and at a real launch by `TestBedrockTakesTheRegionOfTheHostsAWSConfig` |
| <a id="BR-D20"></a>BR-D20 | *Implementation decision, on [BR-DIR1](#BR-DIR1).* **The region fill is part of the credential gate's answer, not a vehicle's.** `packload.ScopeCredentials` runs it once per launch, after every agent's delivery is composed, and appends the region to that agent's shape vars. So the jail's per-agent env files, the macos-user session and each agent's file there, and the environment `yolo host` execs all deliver it with no vehicle learning a field, and the region pre-flight counts it through each notch's own lookup. Each notch hands the gate only a `RegionFileSource`: where the file is read, what the agent inherits beyond the gate (the invoking shell at `yolo host`, nothing in a jail, per [BR-D2](#BR-D2)), and the configured loophole settings. This is one code path per concern ([NC-D1](../plans/notch-convergence.md#7-decision-ledger)). A step writing the region into each vehicle was rejected: that is three writers of one value | 2026-09-29 | [§8](#8-behaviour-this-design-fixes) | ✅ `packload.ScopeInput.RegionFiles`, `fillRegions`; the sources in `run.composePackChannelWith` and `cli.composeHostVarsWith`; pinned by `TestAJailLaunchDeliversTheRegionOfTheHostsAWSConfig` and `TestHostLaunchTakesTheRegionOfTheHostsAWSConfig`, each failing with its notch's source deleted |
| <a id="BR-D21"></a>BR-D21 | *Implementation decision.* **Which file, section and key are pack facts, and the profile is the credential's.** packs/bedrock's provider declares a `region_file`: `path` `.aws/config`, relocated by `path_env_name` `AWS_CONFIG_FILE`, the `key` `region` of `[profile NAME]` (`profile_section` `profile {profile}`), and the name chosen by `profile_env_name` `AWS_PROFILE`. The `default_profile` has two spellings, and the first the file has is its section: `[profile default]`, then `[default]` (corrected 2026-09-29 by the review: the first build read `[default]` alone, while Claude Code's bundled JavaScript loader lets `[profile default]` replace `[default]` and codex's aws-config crate says "profile `[default]` ignored because `[profile default]` was found which takes priority", so yolo delivered a region other than the one the agent would choose, and refused a file holding only `[profile default]`; `packdecl.RegionFile.Sections`, pinned by `TestTheRegionFillReadsProfileDefaultBeforeDefault`). Core names no AWS file, section or variable, the rule [OQ-SSO8](sso-backed-bedrock.md#OQ-SSO8) set. The profile, in order: (1) when aws-auth serves the agent, the one it mints for, which its pointer declares with `region_profile_setting: "profile"` (the loophole's `settings.profile`), since that is the profile the credential comes from; (2) the `AWS_PROFILE` the agent receives (env_sources at every notch, and the invoking shell at `yolo host` unless an env_sources `null` removes it, [BR-D26](#BR-D26)); (3) `default`. Only that profile's own section is read, so an `[sso-session]` block's `sso_region`, which is the SSO portal's region and not Bedrock's, never is | 2026-09-29 | [§8](#8-behaviour-this-design-fixes) | ✅ `packdecl.RegionFile`, `regionFileProblems`, `regionProfileSettingProblems`; `packs/bedrock/pack.json`, `packs/aws-auth/pack.json`; pinned by `TestTheShippedPacksDeclareTheBedrockRegionFile`, `TestTheRegionFillReadsTheProfileTheCredentialComesFrom` (every rung and the sso-session case), `TestARegionFileIsValidated` and `TestARegionProfileSettingNeedsItsLoopholeAndPlatform`; the first rung at the jail notch by `TestAJailLaunchReadsTheProfileAWSAuthMintsFor`, added after a review deleted the notch's `Setting` with the unit gate green |
| <a id="BR-D22"></a>BR-D22 | *Implementation decision.* **yolo parses the file itself and never asks `aws`.** The file is AWS's documented INI format ([the shared config file format](https://docs.aws.amazon.com/sdkref/latest/guide/file-format.html)): `[default]` and `[profile NAME]` sections, `key = value` lines, `#` and `;` comments, and indented sub-setting lines under a key. `packload.iniValue` reads it as configparser, the reader botocore uses, does, in microseconds on the launch path. `aws configure get region` would start the AWS CLI once per agent per launch, and would need it installed, which a macos-user host or a `yolo host` machine need not have. internal/awsauth's `DetectForm` already reads the same format by hand. *Corrected 2026-09-29 by the review:* where the readers differ it reads as the most lenient of them, since a value it reads is delivered only as one DNS label, and a value it misses refuses a launch the agent could have made. So it strips an inline `#` or `;` comment after whitespace (`region = us-east-1 # prod`), as Claude Code's bundled loader and codex's aws-config crate do, which the first build refused as "not a region", and unquotes a quoted profile name (`[profile "dev"]`), as Claude Code's loader and botocore do. Keys still compare case-insensitively, as configparser's do, though Claude Code's loader keeps them as written: `REGION = eu-west-1` is delivered, the region the user wrote, where claude alone would fall back to `us-east-1`. A role profile whose region sits only on its `source_profile` is still refused: claude reads the selected profile's own section alone, and whether codex's crate follows the chain for a region is inferred from its source, not run | 2026-09-29 | [§8](#8-behaviour-this-design-fixes) | ✅ `packload.iniValue`; pinned by `TestIniValueReadsTheSharedConfigFormat` |
| <a id="BR-D23"></a>BR-D23 | *Implementation decision.* **The precedence mirrors the SDKs, and the region goes in the variable the agent reads.** First the provider's own `region` (the user's `providers` entry over the pack's), then a region variable that already reaches the agent, then the file. A platform region variable the agent does not read is no region of its ([BR-D18](#BR-D18)), and no license to put the file's in its place either: opencode receiving only `AWS_DEFAULT_REGION` is given nothing, and the pre-flight refuses it, naming the variable that reached it unread. *Corrected 2026-09-29 by the review:* the first build gave opencode the file's region there, so with `AWS_DEFAULT_REGION=eu-west-1` in env_sources claude ran in eu-west-1 and opencode in the file's us-east-2, with no refusal, and a disclosure saying no region variable reached opencode. The file's region is delivered only where the agent would otherwise have none, in the first variable it reads (`AWS_REGION` for every shipped agent), and in a jail's env file as a default, so a value exported at the agent's own launch wins, and so does one the container's frozen environment carries (a `loopholes.<name>.jail_env` region on the argv), on an attach as on the fresh launch. *Corrected 2026-09-29 by the review:* an attach read that frozen value as a stale yolo default ([OQ-CN8](../reference/providers.md#oq-cn8)'s `case` form) and overrode it with the file's, so an attached terminal ran in another region than the first; the fill's value now leaves the frozen environment out of the values it overrides (`run.filledRegion`, pinned by `TestAnAttachKeepsTheContainersRegionOverTheFilledOne`). One gap is left: the gate cannot see the argv, so the disclosure still names the file's region when a `jail_env` region wins at run time. The value must be one DNS label (`packdecl.RegionProblem`), since agents build a host name from it, and one that is not is never delivered | 2026-09-29 | [§8](#8-behaviour-this-design-fixes) | ✅ `packload.fillRegion`; pinned by `TestTheRegionFillFillsOnlyAnAgentWithNoRegion`, `TestTheRegionFillDeliversInTheVariableTheAgentReads`, `TestTheRegionFillLeavesAnUnreadRegionToTheRefusal` and `TestTheRegionFillReadsTheFileTheLaunchNames` (the DNS label case) |
| <a id="BR-D24"></a>BR-D24 | *Implementation decision.* **The file is the one on the machine yolo launches on, found from the environment yolo was launched from.** `AWS_CONFIG_FILE` there, with a leading `~` expanded, else `.aws/config` under that environment's `HOME`, which is where the `aws` command in the same shell would look. A relocation delivered into a jail names a path inside the jail, so it is not the host's file. That shell locates the file and chooses nothing else in a jail: a region variable or `AWS_PROFILE` left there, which the jail's agent never receives, keeps the file unread and the launch refused, naming it ([BR-D2](#BR-D2)). An empty `HOME` reads nothing, and says so: the jail notch reads `HOME` through the launch environment it was handed, so a test's stubbed environment never reads the machine's `~/.aws` | 2026-09-29 | [§8](#8-behaviour-this-design-fixes) | ✅ `RegionFileSource.Getenv`; pinned by `TestTheRegionFillReadsTheFileTheLaunchNames` |
| <a id="BR-D25"></a>BR-D25 | *Implementation decision.* **One disclosure line, and a refusal that names the file.** When the file supplied a region, every notch prints `Region: AWS_REGION=<region> for <agents> on provider "<name>", read from ~/.aws/config [<section>] (profile "<profile>", <what chose it>): the provider sets no region, and no region variable reaches <it or them>`, beside the credential gate's disclosure (the jail's `noteCredentialScope`, the host's exec and `yolo host env` blocks). A disclosure, so there is no quiet switch ([OQ-RO3](../reference/report-tiers.md#why-its-this-way)). When the file gives none (no such file, no section, no key, not a region, no `HOME`) the refusal is the existing one, reworded: a fact line names the file, the profile and why; the remedy offers `region = <region> under [<section>] in ~/.aws/config` as a third way; and the consulted line ends with the file | 2026-09-29 | [§8](#8-behaviour-this-design-fixes) | ✅ `CredentialScope.RegionLines`, `RegionAsk.File`; the declaration itself is a review-worthy `reads-host` claim, so `yolo pack footprint` and the launch's pack-environment block name the file as read, not mounted (added 2026-09-29 from the review, which found this host read in no disclosure; `packload.regionFileClaimDetail`, pinned by `TestARegionFileIsAHostReadInTheFootprint`); pinned by `TestTheRegionFillDisclosesTheRegionTheFileAndTheProfile`, `TestTheRegionPreflightCountsAFilledRegionAndNamesTheFileItRead`, `TestCheckProviderCredentialsAsksForTheRegion`, and the notch tests of [BR-D20](#BR-D20) |
| <a id="BR-D26"></a>BR-D26 | *Implementation decision.* **`yolo host` delivers the file's region too.** There the agent could read `~/.aws/config` itself, and claude would. yolo delivers it anyway so the refusal, the disclosure and the value are the same at every notch, as [BR-DIR1](#BR-DIR1) asks. No concrete reason not to was found: for claude the value is the one its own resolver would reach, except when aws-auth serves it, where the credential's own profile is the better answer. The invoking shell counts at this notch, so a region or `AWS_PROFILE` exported there wins or chooses, as it would for the agent. A name an env_sources `null` removes is not inherited, since the exec'd agent never receives it (corrected 2026-09-29 by the review: with `AWS_PROFILE` removed and exported in the shell, the fill read that profile's region while the agent's SDK used the default profile's credential). One gap is left: an env_sources `null` that removes a region variable also removes a filled region of that name, and the launch is then refused, naming the file's region as delivered and removed | 2026-09-29 | [§8](#8-behaviour-this-design-fixes) | ✅ `cli.composeHostVarsWith`'s `RegionFiles`, `cli.inheritedExcept`; pinned by `TestHostLaunchTakesTheRegionOfTheHostsAWSConfig` and `TestHostRegionFillReadsNoProfileTheAgentDoesNotReceive` |
| [OQ-BR10](#OQ-BR10) | Moved to [`wire-bridge-gateway.md`](wire-bridge-gateway.md#OQ-BR10): the bridge signs its own requests | 2026-09-24 | there | — |
| [OQ-BR16](#OQ-BR16) | Moved to [`wire-bridge-gateway.md`](wire-bridge-gateway.md#OQ-BR16): the everything profile carries the subscription | 2026-09-24 | there | — |
| [OQ-BR17](#OQ-BR17) | Moved to [`wire-bridge-gateway.md`](wire-bridge-gateway.md#OQ-BR17): opt-in per-model failover | 2026-09-24 | there | — |
| [OQ-BR18](#OQ-BR18) | Moved to [`wire-bridge-gateway.md`](wire-bridge-gateway.md#OQ-BR18): the bridge may carry a Claude subscription | 2026-09-24 | there | — |
| [DIR-BR4](#DIR-BR4) | Moved to [`bedrock-web-search.md`](bedrock-web-search.md#DIR-BR4): AgentCore web search on every Bedrock profile | 2026-09-25 | there | — |

---

## Where the rest went — moved on 2026-09-25

The maintainer's review split this doc. Every id keeps its number, and these anchors catch old
links.

| Moved | Home | Notes |
| :--- | :--- | :--- |
| <a id="OQ-BR2"></a>[OQ-BR2](providers-and-profiles-redesign.md#OQ-BR2), the Bedrock marker (the `service` field), and the former section 6.2 | [`providers-and-profiles-redesign.md`](providers-and-profiles-redesign.md) | ruled 2026-09-29 as the field `platform`, and built |
| <a id="OQ-BR8"></a>[OQ-BR8](providers-and-profiles-redesign.md#OQ-BR8), does a gate key on the name or the provider, and trap D5 | [`providers-and-profiles-redesign.md`](providers-and-profiles-redesign.md) | [OQ-BR4](../reference/providers.md#oq-br4)'s other direction |
| <a id="OQ-BR4"></a>[OQ-BR4](../reference/providers.md#oq-br4), the wide env gate, trap D2 and done-condition 3 | [`providers.md`'s credential gate](../reference/providers.md#the-credential-gate) | ruled 2026-09-25: no leak, *"as specific as possible"*; the all-traffic direction it spawned is [DIR-WG1](wire-bridge-gateway.md#DIR-WG1) |
| <a id="OQ-BR3"></a>[OQ-BR3](model-lists-and-pickers.md#OQ-BR3), <a id="OQ-BR12"></a>[OQ-BR12](model-lists-and-pickers.md#OQ-BR12), <a id="OQ-BR13"></a>[OQ-BR13](model-lists-and-pickers.md#OQ-BR13), <a id="OQ-BR14"></a>[OQ-BR14](model-lists-and-pickers.md#OQ-BR14), <a id="OQ-BR15"></a>[OQ-BR15](model-lists-and-pickers.md#OQ-BR15); parts 5 to 8 of the former section 6.6; done-conditions 8 to 10; R8 and R9; the "no model catalog" non-goal | [`model-lists-and-pickers.md`](model-lists-and-pickers.md) | [OQ-BR3](model-lists-and-pickers.md#OQ-BR3) ruled 2026-09-25: a built-in pack picks the models |
| <a id="OQ-BR10"></a>[OQ-BR10](wire-bridge-gateway.md#OQ-BR10), <a id="OQ-BR16"></a>[OQ-BR16](wire-bridge-gateway.md#OQ-BR16), <a id="OQ-BR17"></a>[OQ-BR17](wire-bridge-gateway.md#OQ-BR17), <a id="OQ-BR18"></a>[OQ-BR18](wire-bridge-gateway.md#OQ-BR18); the signer and everything-profile routing (parts 3 and 4 of the former section 6.6); the Claude-subscription route (former section 6.8); done-condition 6; R6 and R7 | [`wire-bridge-gateway.md`](wire-bridge-gateway.md) | all ruled 2026-09-24 |
| <a id="OQ-BR19"></a>[OQ-BR19](bedrock-web-search.md#OQ-BR19), <a id="OQ-BR20"></a>[OQ-BR20](bedrock-web-search.md#OQ-BR20), <a id="OQ-BR21"></a>[OQ-BR21](bedrock-web-search.md#OQ-BR21), <a id="OQ-BR22"></a>[OQ-BR22](bedrock-web-search.md#OQ-BR22), <a id="OQ-BR23"></a>[OQ-BR23](bedrock-web-search.md#OQ-BR23), <a id="DIR-BR4"></a>[DIR-BR4](bedrock-web-search.md#DIR-BR4); the former section 6.9; D6 and D7; done-condition 12; R10 and R11; the AgentCore evidence | [`bedrock-web-search.md`](bedrock-web-search.md) | DIR-BR4 ruled 2026-09-25 |
| static keys beside `aws-auth`'s pointer ([OQ-SSO8](sso-backed-bedrock.md#OQ-SSO8)), option D's retirement ([OQ-SSO9](sso-backed-bedrock.md#OQ-SSO9)), AWS's guidance on the three credentials | [`sso-backed-bedrock.md`](sso-backed-bedrock.md) | ruled there |
| deselection and the id a dropped profile leaves (P1's general form, done-condition 4) | [`providers.md`](../reference/providers.md#deselection-clear-what-yolo-wrote-keep-what-the-user-wrote) | — |

---

## 14. Evidence, and how to re-check it

Every third-party fact carries its source and date; re-run these rather than trusting the tables.
Repo claims were verified at `f491d192` (2026-09-24), cited by symbol. No request was made to AWS.

**codex**: the standalone **codex-cli 0.156.1** yolo's launcher installs
(`~/.codex/packages/standalone/releases/0.156.1-x86_64-unknown-linux-musl/bin/codex`). It was read
with `strings` on 2026-09-24 and never executed. A stale npm-global 0.145.0 was read for the
comparisons, and the 2026-09-04 reading was of 0.145.0.

| Claim | String found in 0.156.1 |
| :--- | :--- |
| Built-in provider ids | `responses` `openai` `amazon-bedrock` `amazon-bedrock-runtime` `ollama`; 0.145.0 has no `-runtime` |
| Bedrock modules | `amazon_bedrock/` `auth.rs` `auth_refresh.rs` `catalog.rs` `credential_export.rs` `mantle.rs` `mod.rs` `runtime.rs` |
| Override list | the seven keys quoted in [§5](#5-one-endpoint-family--runtime-ships-and-why-not-mantle); 0.145.0's stopped at `aws.region` |
| Base URLs | `https://bedrock-mantle.` + `.api.aws/openai/v1`; `https://bedrock-runtime.` + `.amazonaws.com/openai/v1` |
| Region sources | ``Amazon Bedrock bearer token auth requires `model_providers.amazon-bedrock.aws.region`, `AWS_REGION`, or `AWS_DEFAULT_REGION` `` |
| Bearer first | `AWS_BEARER_TOKEN_BEDROCK`; *"Bedrock API key auth is only supported by the Amazon Bedrock model provider"* |
| Catalog | `openai.gpt-5.6-{sol,terra,luna}`, `global.openai.gpt-5.6-{terra,luna}` (no `global.` `-sol`), `openai.gpt-6-astra`; `model_catalog_json` (also in 0.145.0) |
| Managed entry, unknown model, region, expiry | the D3/D4 strings ([§7](#7-traps--read-before-writing-code)); *"Amazon Bedrock does not support region …"* (0.145.0: *"…Mantle does not support region"*); *"…its AWS signature has expired"* |

**pi**: pi-ai **0.87.1** (`@earendil-works/pi-coding-agent`, this jail's npm-global), read and never
run on 2026-09-24, then re-read for this rewrite. `dist/providers/amazon-bedrock.js` defines
`amazon-bedrock` on `bedrockConverseStreamApi()` with [§4](#4-what-each-agent-can-actually-do)'s auth
order. `dist/api/bedrock-converse-stream.js` takes the bearer from options or
`AWS_BEARER_TOKEN_BEDROCK`, honours `AWS_BEDROCK_SKIP_AUTH=1`, and resolves the region as in
[§6.2](#62-what-each-derive-emits). The bundled catalog has 165 ids, all Converse, and it lists
`openai.gpt-5.6-*` and `openai.gpt-6-astra` bare and `us.`/`global.` against
`bedrock-runtime.us-east-1`. Its `docs/models.md` says a `models` entry *"adds or replaces a model
with the same ID on that provider"*. The 2026-09-04 reading was of 0.82.1, which had only the API
id, and [`local-model-endpoints.md`](../research/local-model-endpoints.md) saw pi's compat block
change within one doc's lifetime. **Re-read pi at the shipping version before writing its derive.**

**opencode**: 1.18.32 (`opencode-linux-x64-baseline`), read statically on 2026-09-22 and 2026-09-24.
It has the comment *"1. Bearer token (`AWS_BEARER_TOKEN_BEDROCK` or /connect)"*, falls back to the
chain, and takes the region from `options.region`, else `AWS_REGION`, else `"us-east-1"`.

> [!WARNING]
> **opencode, MEASURED 2026-09-22 statically from the installed 1.18.32 bundle; no agent was
> started.** It already routes Bedrock's OpenAI-shaped models to mantle by default:
> - its vendored `@ai-sdk/amazon-bedrock` 4.0.166 ships a mantle factory;
> - its models.dev catalog marks 13 models, all three bare GPT-5.6 ids among them, with a
>   per-model mantle `npm`/`api` override;
> - geo-prefixed ids take the runtime default.
>
> A provider-level `npm` overrides those markers (`config.provider?.npm ?? … ?? model.api.npm`),
> and so does `options.endpoint`/`baseURL`. Either one drags bare ids onto runtime. So the derive
> omits both and emits runtime-spelled ids, and a bare id a user adds reaches mantle through
> opencode's own catalog.
>
> ⚠ **`options.region` alone does not enable the provider.** Autoload gates on six credential
> signals: `AWS_PROFILE`/`options.profile`, `AWS_ACCESS_KEY_ID`, a bearer, `options.apiKey`,
> `AWS_WEB_IDENTITY_TOKEN_FILE`, and the container-credentials variables. Each supported
> credential supplies one. Not shown: whether a request reaches AWS.

**claude**: 2.1.282, 2026-09-24. `AWS_BEARER_TOKEN_BEDROCK` occurs in the binary (12 `grep -c -a`
matches), which shows presence only.

**Why runtime ships and mantle does not** ([§5](#5-one-endpoint-family--runtime-ships-and-why-not-mantle)), SOURCED:

| Reason | Evidence |
| :--- | :--- |
| Runtime serves every model family mantle does, plus Converse | ⚠ That holds per family, not per model. AWS names *"a model that is available only on `bedrock-mantle`"* as a reason to use it, and Claude Code says mantle *"has its own model lineup"*. That is the revisit trigger |
| GPT-6 Astra is broad on runtime | US Geo and Global inference on runtime; mantle serves it in `us-west-2` only |
| ≈10% cheaper through Global inference | $10.00 vs $11.00 per M input for GPT-6 Astra, $4.00 vs $4.40 for GPT-5.6 Sol; cross-Region inference is runtime's alone |
| AWS recommends runtime | the endpoints page and the Sol card |
| Mantle's signing is undocumented | third parties sign it as `bedrock-mantle`; its base path is disputed between AWS's own pages |
| Mantle needs its own IAM set | the `bedrock-mantle:*` actions, which `aws-auth`'s example policy does not grant |
| It doubles the matrix | [§6.7](#67-the-whole-matrix--every-agent-every-model-every-credential) |

**AWS** (every row is documentation; nothing was called):

| Claim | Source | Read |
| :--- | :--- | :--- |
| URLs, the id split, region matrices, API tables, `project/default`, the Global discount | GPT-5.6 Sol card, endpoints page | 2026-09-04 |
| ⚠ mantle's path: `/openai/v1` (Sol and Astra cards) against `/v1` (Chat Completions and Responses pages) | those pages | 2026-09-25 / 09-24 |
| Astra: runtime `us.`/`global.` with Converse; mantle *"available only in `us-west-2`"*, no Converse; $10.00 vs $11.00 | GPT-6 Astra card | 2026-09-25 |
| *"For most new applications, use the `bedrock-runtime` endpoint…"*; cross-Region inference is runtime-only; per-token price identical; `bedrock-mantle:CreateInference`; *"Whenever possible, we recommend using the `bedrock-runtime` endpoint"* | endpoints page; Sol card | 2026-09-25 |
| Web Search: mantle Responses only, five GPT models, three US Regions | Bedrock Web Search | 2026-09-25 |
| no WebSearch on Bedrock; mantle has its own Anthropic lineup under `CLAUDE_CODE_USE_MANTLE` | Claude Code on Bedrock | 2026-09-25 |
| all three APIs on both endpoints; runtime chat completions serve the [§2](#2-what-bedrock-is--sourced-from-awss-pages) families; closed GPT needs `us.`/`global.`; signed as `bedrock` under `InvokeModel`(`WithResponseStream`); no `GET /openai/v1/models` on runtime, `GET /v1/models` on mantle; Messages is Claude-only | endpoints, model API compatibility | 2026-09-24 |
| mantle's SigV4 name `bedrock-mantle`, which no AWS page read names | codex `mantle.rs`, oh-my-pi, LiteLLM #31475, openai-go `bedrock` docs | 2026-09-23/24 |
| SigV4 and API keys on both endpoints; the SigV4 `curl` example `--aws-sigv4 "aws:amz:us-east-1:bedrock"`; an API key *"required for OpenAI SDK"* on Responses | endpoints, Chat Completions, Responses pages | 2026-09-24 |
| short-term keys ≤12 h; one `CallWithBearerToken` action per endpoint; long-term key mechanics | API-key pages; IAM user guide | 2026-09-24; 2026-09-04 |

**Sources:**
[Bedrock endpoints](https://docs.aws.amazon.com/bedrock/latest/userguide/endpoints.html) ·
[Model API compatibility](https://docs.aws.amazon.com/bedrock/latest/userguide/models-api-compatibility.html) ·
[GPT-5.6 Sol card](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-openai-gpt-56-sol.html) ·
[GPT-6 Astra card](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-openai-gpt-6-astra.html) ·
[Cross-Region inference for GPT-5.6](https://aws.amazon.com/blogs/machine-learning/introducing-cross-region-inference-for-openai-gpt-5-6-models-on-amazon-bedrock/) ·
[Bedrock API keys](https://docs.aws.amazon.com/bedrock/latest/userguide/api-keys.html) ·
[How API keys work](https://docs.aws.amazon.com/bedrock/latest/userguide/api-keys-how.html) ·
[API key permissions](https://docs.aws.amazon.com/bedrock/latest/userguide/api-keys-permissions.html) ·
[Chat Completions](https://docs.aws.amazon.com/bedrock/latest/userguide/inference-chat-completions.html) ·
[Responses API](https://docs.aws.amazon.com/bedrock/latest/userguide/inference-responses-api.html) ·
[openai-go `bedrock`](https://pkg.go.dev/github.com/openai/openai-go/v3/bedrock) ·
[Bedrock API keys (IAM)](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_credentials_bedrock.html) ·
[AWS SDK credential providers](https://docs.aws.amazon.com/sdkref/latest/guide/standardized-credentials.html) ·
[Codex with Amazon Bedrock](https://learn.chatgpt.com/docs/amazon-bedrock) ·
[codex PR #18744](https://github.com/openai/codex/pull/18744) ·
[opencode providers](https://opencode.ai/docs/providers/) ·
[Bedrock Web Search](https://docs.aws.amazon.com/bedrock/latest/userguide/web-search.html) ·
[Claude Code on Amazon Bedrock](https://code.claude.com/docs/en/amazon-bedrock)
