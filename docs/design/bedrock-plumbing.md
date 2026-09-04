---
title: "Bedrock plumbing: one service, four agents, two arms"
date: 2026-09-04
status: draft
tags: [packs, providers, profiles, bedrock, aws, codex, gpt]
summary: "How GPT-5.6 (and anything else Bedrock serves) reaches codex, pi and opencode. The surprise: three of the four agents already ship their own amazon-bedrock provider reading the same AWS_BEARER_TOKEN_BEDROCK, so the work is not an endpoint — it is picking ONE endpoint family so one model-id spelling is right for all of them, and teaching three derives to bind natively to an endpoint-less provider."
---

# Bedrock plumbing: one service, four agents, two arms

**Status:** DESIGN SKETCH, 2026-09-04. Nothing built. Every code claim verified against
`4c60a220`; every vendor claim carries its source and date in §14.

**The short version.** Bedrock reaches an agent two ways, and yolo should build the first
and document the second. The **native arm** *(coined here)* is the path where the agent's
own client speaks to AWS — codex, opencode and pi each ship a built-in Bedrock provider,
all three keyed on `AWS_BEARER_TOKEN_BEDROCK` then the AWS credential chain — so yolo
supplies a **region, a credential and a model id**, and never a URL. The **gateway arm**
*(coined here)* is Bedrock's own OpenAI-compatible `/openai/v1` route, which is already an
ordinary yolo provider today and needs no new machinery at all. The work that is actually
hard is neither: it is that **the three native clients default to different Bedrock
endpoint families, and the model id spelling differs between them** — `openai.gpt-5.6-sol`
on `bedrock-mantle`, `global.openai.gpt-5.6-sol` on `bedrock-runtime`. So the family and the
model ids are **one entry, shipped twice**: `-p bedrock-gpt` for runtime,
`-p bedrock-gpt-mantle` for mantle, both available to measure against each other.

**The most important section is §5** — why the family is a provider entry rather than a
knob. Everything else follows from it. **§7 is the one to read before writing code**: four
traps, two of which are live defects in shipped code that no test catches.

**Scope note.** The general problem this doc's P1 names — *a model id is provider-local, so
every provider switch is a rename, and yolo only does half of it* — is split out into
[`provider-switching.md`](provider-switching.md). Nothing about it is Bedrock-specific and
its fix lands in the provider system rather than in a pack. This doc neither depends on that
one nor blocks it.

**Reads with:** [`../reference/providers.md`](../reference/providers.md) (the provider
system this extends — catalog, selection, derives, the canonical `wire_api` vocabulary),
[`zai-plumbing.md`](zai-plumbing.md) (the same exercise for a plain HTTP provider; this doc
is its sequel and borrows its resolution vocabulary),
[`../research/local-model-endpoints.md`](../research/local-model-endpoints.md) (the
per-agent config surfaces, source-verified),
[`provider-switching.md`](provider-switching.md) (the sibling this doc's P1 spawned — tier
aliases, and the id a deselected profile leaves behind).

---

## 1. Verdict and principles

**Build the native arm as endpoint-less providers — one per model family, one per endpoint
family — plus three derive bindings. Ship the gateway arm as a documented config recipe,
not as code.**

The native arm is where the leverage is: three agents already implement Bedrock, and what
they implement is better than what a gateway gives them — SigV4 or bearer, the AWS
credential chain, cross-Region inference, and in codex's case a first-class `bedrock_api_key`
auth mode stored in `auth.json`. Reimplementing that as "a base URL plus a bearer token"
throws away work someone else already did and pins yolo to a URL it must keep current.

Three principles the rest of the doc leans on.

**P1. An auth mode is a BUNDLE of `{credential channel, environment, model ids}`, and the
three move together.** This is not new — it is the lesson of the 2026-05 Bedrock→Teams
switch on this machine, where the credential channel and the env moved and the
Bedrock-shaped model pin stayed behind, failing later as a 404 on an unknown model rather
than as an auth error. Bedrock hands us the same shape one layer down: the endpoint family
and the model id are one decision, and a design that lets them be set independently has
built the same trap again. The general form of this — and the live defect it has already
left in the selection mechanism — is [`provider-switching.md`](provider-switching.md).

**P2. Where the agent already knows the service, yolo supplies facts, not plumbing.** A
provider entry with no endpoints is not a degenerate provider — it is the correct
representation of "the client composes its own URL from a region." packs/claude's `bedrock`
provider has been exactly this since it shipped (`packs/claude/pack.json:121-124`, a
`kind: "provider"` with a name and nothing else), and the credential preflight already
treats it correctly: an entry with no endpoints demands no credential
(`docs/reference/providers.md`, "the credential preflight follows catalog membership").

**P3. One model-id spelling per provider entry, and every agent that selects it gets that
one.** The `models` map is one map and the alias a profile names resolves through it once,
so an agent-dependent spelling is not a configuration — it is a `models` map that is wrong
somewhere. The corollary is the shape of §5: two endpoint families means two entries, never
one entry with a switch, because a switch is exactly the thing that could move the endpoint
and leave the ids.

---

## 2. What Bedrock is now — measured 2026-09-04

Two endpoints, both OpenAI-compatible, and they are **not** interchangeable.

| | `bedrock-runtime` | `bedrock-mantle` |
| :--- | :--- | :--- |
| Base URL | `https://bedrock-runtime.{region}.amazonaws.com/openai/v1` | `https://bedrock-mantle.{region}.api.aws/openai/v1` |
| GPT-5.6 Sol model id | `us.openai.gpt-5.6-sol` / `global.openai.gpt-5.6-sol` — **in-Region is not available** | `openai.gpt-5.6-sol` — **Geo and Global are not supported** |
| Regions serving GPT-5.6 Sol | 25+, via Geo/Global cross-Region inference | `us-east-1`, `us-east-2` only |
| APIs | Responses, Chat Completions, Converse, InvokeModel, Messages | Responses, Chat Completions, Messages |
| Auth | SigV4 **or** Bedrock API key (bearer) | SigV4 **or** Bedrock API key (bearer) |
| Server-side tools, `background=true` | Not supported | Supported |
| Structured outputs (this model) | Not supported | Not supported |
| Price | Global CRIS ≈10% cheaper per token than In-Region/Geo | same per-token rates |

> [!IMPORTANT]
> **The model id is a function of the endpoint family, and AWS says so explicitly.** From
> the GPT-5.6 Sol model card: *"On `bedrock-runtime`, name a cross-Region inference profile
> as the model — `us.openai.gpt-5.6-sol` or `global.openai.gpt-5.6-sol`. This model is not
> available for in-Region inference on that endpoint."* The bare `openai.gpt-5.6-sol` is
> the **mantle** spelling. Sending it to `bedrock-runtime` is the P1 failure, and it
> surfaces as a model error, not an endpoint error.

Two more facts that shape the design:

- **The credential is a bearer token.** A Bedrock **long-term API key** is an IAM
  service-specific credential bound to one IAM user, scoped to Bedrock, with a
  configurable expiry from one day to indefinite; an IAM user may hold two, for rotation.
  A **short-term API key** is derived from ambient SigV4 credentials and lives at most 12
  hours. Both travel as `AWS_BEARER_TOKEN_BEDROCK`. This matters because it is the ONE
  credential shape all four agents accept.
- **Bedrock is not only OpenAI models.** The same `/openai/v1` route serves Anthropic
  models (AWS's own example calls it with `model="us.anthropic.claude-sonnet-4-6"`), and
  the Messages API serves them natively. Nothing in this design is GPT-specific except the
  contents of one `models` map.

---

## 3. What yolo has today

The provider system is fully built and is described in
[`../reference/providers.md`](../reference/providers.md); this section states only the
parts Bedrock lands on.

- **One `bedrock` provider exists, and it is claude's.** `packs/claude/pack.json:121-148`
  ships four contributions as a set: `kind: "provider"` named `bedrock` (no endpoints, no
  `api_key_env_name`, no models), `kind: "profile"` named `bedrock` selecting it, a
  `profile`-gated `kind: "env"` setting `CLAUDE_CODE_USE_BEDROCK=1`, and a `profile`-gated
  `config-overlay` writing the same key into `claude/settings`. It is the worked example
  the reference doc names for the post-OQ-PT8 decomposition.
- **Only the claude derive reads `region`.** `packs/claude/derive.lua:61-63` maps
  `p.region` → `AWS_REGION`. The schema field is documented as exactly this
  (`internal/packdecl/contributes.go:237-242`: *"Region is the region a regional provider
  is reached through — Bedrock's address half"*). The other three derives ignore it.
- **The other three derives drop an endpoint-less provider on the floor.** Each has a
  `providerEndpoint` helper resolving the `openai` endpoint key or the `base_url`
  shorthand, returning nil otherwise (`packs/codex/derive.lua:55`, `packs/pi/derive.lua:43`,
  `packs/opencode/derive.lua:11`), and each gates both its catalog row and its selection
  key on that one predicate. A provider with no URL is therefore invisible to codex, pi and
  opencode — correctly, today, because they had no other way to reach it.
- **Provider names are sole-owned across packs** (`internal/packdecl/kinds.go:335-341`,
  `CombineExclusive`): two packs shipping one provider name is a launch-refusing collision;
  one pack shipping two names is the ordinary multi-provider pack.

So: claude on Bedrock works. Nothing else can see Bedrock at all.

---

## 4. What each agent can actually do

Verified from the shipped artifacts, not from documentation, except where noted.

| Agent | Native Bedrock support | How it authenticates | Evidence |
| :--- | :--- | :--- | :--- |
| **claude** | Yes — `CLAUDE_CODE_USE_BEDROCK=1` | AWS credential chain; `AWS_REGION` | already shipped in yolo |
| **codex** | Yes — built-in provider id `amazon-bedrock`, `wire_api = responses` | `AWS_BEARER_TOKEN_BEDROCK` first, else the AWS SDK credential chain; region from `model_providers.amazon-bedrock.aws.region`, `AWS_REGION` or `AWS_DEFAULT_REGION` | codex-cli 0.145.0 binary, §14 |
| **opencode** | Yes — built-in provider `amazon-bedrock` with `options: {region, profile, endpoint}` | bearer token (`AWS_BEARER_TOKEN_BEDROCK` or `/connect`) takes precedence over the credential chain | opencode docs, 2026-09-04 |
| **pi** | Yes — built-in API id `bedrock-converse-stream` in its runtime registry | `options.bearerToken` / `apiKey` / `AWS_BEARER_TOKEN_BEDROCK`, else the SDK chain; `AWS_PROFILE`; `AWS_BEDROCK_SKIP_AUTH=1` disables | pi-ai **0.82.1**, `dist/compat.js:108-119` and `dist/api/bedrock-converse-stream.js:1-60` |
| **copilot** | No provider extension point in scope here | — | — |
| **agy** | No — model transport is a closed enum (`ccpa`/`gemini`/`stubby`) | — | `local-model-endpoints.md` §"agy" |

**The convergence is the finding.** Three independent agents chose the same provider id
(`amazon-bedrock`), the same credential variable (`AWS_BEARER_TOKEN_BEDROCK`), the same
precedence (bearer over chain) and the same region knobs. That is not a coincidence to
design around — it is a de-facto interface, and yolo's job is to feed it.

**Where they diverge is the endpoint family**, and that divergence is invisible until the
first request:

- codex's built-in provider is a **mantle** client: its baked base URL is
  `https://bedrock-mantle.{region}.api.aws/openai/v1`, from `amazon_bedrock/mantle.rs`, and
  its bundled catalog maps the slug `gpt-5.6-sol` to the bare id `openai.gpt-5.6-sol`. It
  carries its own region allowlist (*"Amazon Bedrock Mantle does not support region …"*).
- opencode and pi both drive AWS SDK clients, which resolve **`bedrock-runtime`**.

So with no intervention, `-p bedrock-gpt -- codex` and `-p bedrock-gpt -- opencode` want
*different model id spellings for the same model*. P3 says that cannot stand.

---

## 5. Two families, two providers — because the family and the ids are one entry

**The proposal: ship BOTH endpoint families, each as its own provider entry, and let a
profile name pick between them.** `-p bedrock-gpt` is runtime; `-p bedrock-gpt-mantle` is
mantle. One word apart at the CLI, and both available to try.

The reason this is two *providers* rather than one provider with an `endpoint_family`
option is structural, not stylistic. §2 established that the model id is a function of the
family — `openai.gpt-5.6-sol` on mantle, `global.openai.gpt-5.6-sol` on runtime — and an
option **cannot** carry model ids: `options` is a flat name→value map a derive reads, while
`models` is a provider field the option layer never touches. A family option would therefore
let a user move the endpoint and leave the ids behind, which is P1's failure with a knob
attached. Two entries make that unrepresentable: the family and its ids sit in the same
object, and switching families is switching objects.

Reaching runtime from codex is possible because codex explicitly permits it. Its guard on
built-in providers reads, verbatim from the binary:

> `` model_providers.<id> only supports changing `base_url`, `auth`, `http_headers`, `aws.profile`, and `aws.region`; other non-default provider fields are not supported ``

`base_url` is the first thing on that list. So for the runtime provider the codex derive
writes
`model_providers.amazon-bedrock.base_url = https://bedrock-runtime.{region}.amazonaws.com/openai/v1`
alongside `aws.region`; for the mantle provider it writes **no `base_url` at all** and lets
codex's own default stand. Either way codex's built-in Bedrock client — SigV4, bearer
support, `auth.json` integration — is what does the talking.

Which family you would reach for:

| | runtime | mantle |
| :--- | :--- | :--- |
| Regions for GPT-5.6 | 25+ | 2 (`us-east-1`, `us-east-2`) |
| Cross-Region inference (capacity headroom) | yes | no |
| Global CRIS price | ≈10% cheaper | n/a |
| Server-side tools / `background=true` | no | yes |
| Guardrails, intelligent prompt routing | yes | no |
| codex | needs the `base_url` override | its own default |

**Not every agent can reach both**, and the derives drop what they cannot — the ordinary
behaviour for an unreachable provider, no new machinery:

| Agent | runtime | mantle |
| :--- | :--- | :--- |
| codex | yes, via the `base_url` override | yes, natively |
| opencode | yes, natively (its SDK resolves runtime) | **unverified** — it has an `endpoint` option, untested against mantle |
| pi | yes, via `bedrock-converse-stream` | **no** — the model card marks Converse unsupported on mantle |
| gateway arm (§6.4) | yes | yes |

**Runtime is the one I would make the recommended default**, and the doc should say so in
the pack README: 25 regions against two, Global CRIS a little cheaper, reachable by all
three agents. The column mantle wins — server-side tool use and background inference — is a
column no coding agent in this repo uses; all four drive **client-side** tools, which both
endpoints serve. But "recommended" is a sentence in a README, not a constraint in the code:
both profiles ship, and the maintainer's stated reason for asking is to measure them against
each other rather than take my word for it.

---

## 6. The proposed shape

### 6.1 Three providers, because a `models` map cannot hold two model families

claude on Bedrock wants Anthropic ids (`us.anthropic.claude-opus-5`); codex wants GPT ids;
and the two Bedrock families spell the same GPT model differently. All of them resolve the
alias `default` through *a* map, so each needs its own — which
`internal/packdecl/kinds.go:337` already calls the ordinary case ("one pack shipping two
names is two contributions").

| Provider | Owner | Models | Reachable by |
| :--- | :--- | :--- | :--- |
| `bedrock` | packs/claude, **unchanged** | Anthropic ids | claude |
| `bedrock-openai` *(new)* | a new `bedrock` pack | `global.openai.gpt-5.6-*` | codex, pi, opencode |
| `bedrock-openai-mantle` *(new)* | the same pack | `openai.gpt-5.6-*` | codex, opencode (unverified) |

Keeping `bedrock` exactly where it is avoids renaming a name users already type and
sidesteps the sole-ownership collision entirely. The new pack ships two providers, two
profiles and no CLI — the same shape as `packs/zai`, the precedent for a pack whose whole
content is declarative facts.

```jsonc
// packs/bedrock/pack.json — the shape, not the file
{
  "name": "bedrock",
  "contributes": [
    { "kind": "provider", "name": "bedrock-openai",
      "service": "aws-bedrock",                  // the marker — see §6.2, OQ-BR2
      "endpoint_family": "runtime",              // decides the URL, not the ids
      "region": "us-east-1",
      "models": {
        "default":  "global.openai.gpt-5.6-sol",
        "balanced": "global.openai.gpt-5.6-terra",
        "fast":     "global.openai.gpt-5.6-luna"
      },
      "options": { "model": "default", "aws_profile": null }
    },
    { "kind": "provider", "name": "bedrock-openai-mantle",
      "service": "aws-bedrock",
      "endpoint_family": "mantle",
      "region": "us-east-1",
      "models": {
        "default":  "openai.gpt-5.6-sol",
        "balanced": "openai.gpt-5.6-terra",
        "fast":     "openai.gpt-5.6-luna"
      },
      "options": { "model": "default", "aws_profile": null }
    },
    { "kind": "profile", "name": "bedrock-gpt",        "provider": "bedrock-openai" },
    { "kind": "profile", "name": "bedrock-gpt-mantle", "provider": "bedrock-openai-mantle" }
  ]
}
```

`endpoint_family` is a **provider field, beside `region`**, not a profile option — same
reasoning as §5, and the same reasoning that put `region` there
(`internal/packdecl/contributes.go:237-242`: a service fact, saying where the provider
lives). A field a profile cannot reach is a field that cannot drift away from the `models`
map next to it. **OQ-BR7** asks whether it is a distinct field at all or just falls out of
`service`.

`yolo -p bedrock-gpt -- codex` is then the whole user gesture, with the credential arriving
the way every other credential does — `env_sources` hydrating `AWS_BEARER_TOKEN_BEDROCK`,
or an ambient AWS chain the jail can see. `-p bedrock-gpt-mantle` is the same gesture
against the other family, and the two can be compared back to back in one session.

### 6.2 How a derive recognizes "this is Bedrock"

An endpoint-less provider needs a marker, because the three derives currently key
reachability on a URL. Three candidates, and this is a real fork — **OQ-BR2**.

The leaning is a new open-vocabulary `service` field on the provider kind: `"aws-bedrock"`
here, unknown values inert (no derive claims them, nothing renders — the same tolerance the
open `endpoints` key set already has, for the same version-skew reason
`internal/packdecl/contributes.go:215-224` records). It is explicit, it is greppable, and it
survives a second regional service arriving later.

What it is **not**: a `wire_api` value. Bedrock is not a protocol — its native clients speak
Responses, Converse and the AWS SDK's own shapes — and putting a service name in the
canonical protocol enum is the pass-through mistake OQ-PT1 closed, one field over.

> [!WARNING]
> **An `endpoints` key cannot be the marker.** `internal/packdecl/contributes.go:1670`
> refuses an endpoint entry with no `base_url` (`endpoints[%q]: needs a "base_url"`), and a
> pack cannot ship a Bedrock URL because the URL contains a region it does not know. A
> `service` field or nothing.

### 6.3 What each derive emits

Each agent's binding lives in that agent's own derive — OQ-CS8, unchanged: core learns no
agent's vocabulary, and core resolves no model.

| Agent | Catalog | Selection | Region / profile |
| :--- | :--- | :--- | :--- |
| **codex** | `[model_providers.amazon-bedrock]` with `aws.region`, plus `base_url` **only for the runtime family** (the mantle provider writes none, so codex's own default stands); **no other fields** — codex refuses them | `model_provider = "amazon-bedrock"`, `model = <resolved id>` | `aws.region` from `region`; `aws.profile` from the `aws_profile` option when set |
| **opencode** | `provider["amazon-bedrock"] = { npm: "@ai-sdk/amazon-bedrock", options: { region, profile? }, models: {…} }` — **not** the `@ai-sdk/openai-compatible` + `baseURL` shape the derive writes today | `model = "amazon-bedrock/<resolved id>"` | `options.region`, `options.profile` |
| **pi** | `providers["bedrock-openai"] = { api: "bedrock-converse-stream", models: [...] }` — **runtime family only**; the mantle provider yields no pi row, because Converse is not served there | `defaultProvider` / `defaultModel` | region via `AWS_REGION` in the jail env |
| **claude** | none (claude has no catalog) | env, as today | `AWS_REGION` from `region` |

Selection keys keep riding the reserved `selection` namespace with the edge-triggered apply
— nothing about Bedrock changes the "write on activation, never on absence" rule, and an
interactive `/model` still stands.

### 6.4 The gateway arm ships as documentation

For any agent with no native path, or a user who wants a plain HTTP provider, the gateway
arm needs **no yolo code at all** once the §7 defect is fixed. It is an ordinary user
`providers` entry:

```jsonc
"providers": {
  "bedrock-gw": {
    "endpoints": { "openai": {
      "base_url": "https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1",
      "wire_api": "openai-responses"
    }},
    "api_key_env_name": "AWS_BEARER_TOKEN_BEDROCK",
    "models": { "default": "global.openai.gpt-5.6-sol" }
  }
}
```

The user writes the region into the URL themselves. That is deliberate: it is the one place
the region genuinely has to be a URL, and templating it in core would need a `${region}`
interpolation the composer does not have and the URL validator would reject. Two lines of
config beats a core feature.

---

## 7. Traps — read before writing code

**D1 (live defect). The codex derive writes a key codex does not read.**
`packs/codex/derive.lua:120-121` emits `api_key_env = <var name>`. codex's
`ModelProviderInfo` has no such field — its credential-variable field is **`env_key`**, and
the string `api_key_env` does not occur anywhere in the codex 0.145.0 binary. Every custom
provider yolo configures for codex therefore ships with no credential binding; whether that
manifests as an unauthenticated request or a startup refusal is untested, because no test in
this repo runs codex. This is the exact "test pins the callee while the call site is
unpinned" shape AGENTS.md warns about: `codex_selection_test.go` asserts the TOML yolo
writes, not that codex reads it. **Fix `env_key` first** — the gateway arm cannot deliver a
credential without it, and the zai path has the same hole today.

**D2 (live hazard). The profile env gate leaks across agents.**
`internal/packload/packload.go:430-443`: `profileActive` returns true if the profile is
active for a bin *this* pack installs **or for any bin the launch installs at all**. The
wide pass exists so a CLI-less pack's gated env is reachable, and it is correct for that.
But it means `-p codex=bedrock` in a jail that also selects packs/claude fires claude's
gated `CLAUDE_CODE_USE_BEDROCK=1` into the jail-wide environment, pointing claude at Bedrock
that nobody configured it for. The config-overlay twin does **not** have this problem —
`internal/packoverlay/packoverlay.go:194` gates on `profiles[key.Agent]`, agent-scoped. A
shared Bedrock profile name makes this routine rather than theoretical. **OQ-BR4.**

**D3. codex actively manages its `amazon-bedrock` entry.** The binary carries
*"configuration changed while clearing the managed Amazon Bedrock model provider; retrying
once"* and *"Amazon Bedrock login cannot select `X` because `Y` sets `model_provider` to
`Z`"*. Two consequences: writing fields beyond the permitted five is refused, not ignored;
and yolo pinning `model_provider` will block `codex login` for Bedrock while the profile is
active. The second is acceptable — a jail whose profile names the provider is not the place
to run an interactive login — but it must be in the briefing, not discovered.

**D4. codex will not know the pinned model ids.** Its bundled catalog holds the mantle
spellings (`gpt-5.6-sol` → `openai.gpt-5.6-sol`); a `global.`-prefixed id is unknown to it,
and it says so: *"Unknown model … is used. This will use fallback model metadata."* The
request works; the context-window and pricing metadata are wrong. That is an accepted cost
of the §5 pin, and the alternative — pinning mantle to keep codex's metadata — costs 23
regions and every other agent.

---

## 8. Behaviour this design fixes

Written for the implementer. Anything not here and not an OQ is theirs.

**Degenerate inputs.**
- Provider selected, `region` absent and no `AWS_REGION`/`AWS_DEFAULT_REGION` in the jail
  env → **refuse the launch**, naming the three places a region may come from. Every native
  client fails on this and codex's own error text already enumerates them; a jail that boots
  and dies at first request is the worse outcome.
- `models` map empty, or the alias the profile names is absent → emit the catalog row and
  **omit the model key**, exactly as the existing derives do. The agent resolves its own.
- Profile selected for an agent with no native path (copilot, agy) → **nothing written**,
  no warning. Same as any unreachable provider today.
- Two providers both marked `service: "aws-bedrock"` → both are ordinary catalog rows; only
  the selected one gets a selection key. Not a collision — and it is the **shipped** case,
  since the two endpoint families are two entries.
- A provider whose `endpoint_family` an agent cannot serve (mantle for pi) → **no catalog
  row and no selection key**, through the same gate that drops any unreachable provider. It
  is not an error: the other agents in the same jail still get theirs.

**Failure paths.**
- Credential absent: the provider declares no `api_key_env_name` (the native arm's
  credential is ambient), so the credential preflight demands nothing and the launch
  proceeds. Failure surfaces at first request, as an AWS auth error, from the agent. This is
  a deliberate asymmetry with the gateway arm, where `api_key_env_name` **is** declared and
  the preflight does demand it.
- Bedrock API key expired (long-term keys carry an expiry; short-term ones last ≤12h):
  surfaces as codex's *"Amazon Bedrock rejected the request because its AWS signature has
  expired… If `AWS_BEARER_TOKEN_BEDROCK` is set, update or unset it, then restart Codex."*
  yolo does not refresh, retry, or detect this. **Named as out of scope, not overlooked** —
  the broker pattern exists for OAuth and Bedrock keys are not OAuth.
- Region unsupported for the chosen family (mantle outside `us-east-*`): surfaces as
  codex's own *"Amazon Bedrock Mantle does not support region …"*. yolo carries no region
  allowlist of its own — a list that would rot within a quarter.

**Defaults, with units.** `endpoint_family` is a provider FIELD with no default — each
shipped entry states its own (`runtime`, `mantle`), and an entry omitting it is refused
rather than guessed, because a guessed family is a wrong model id (P1). `region:
"us-east-1"` (shipped on both entries; overridable per user). `aws_profile: null` — declared, no
default, meaning "use the chain". Model alias fallback: the profile's `model` option, else
`default` — the existing OQ-CS3 ladder, unchanged. No timeouts and no retries are introduced
by this design.

**Trigger.** Everything renders in the ordinary boot render, once per launch, from the
composed provider table — plus the host notch's env derive at `yolo host -- <agent>`. There
is no watcher, no refresh and no second write.

**One writer.** yolo owns the catalog rows it renders (`model_providers.amazon-bedrock`,
`provider["amazon-bedrock"]`, pi's `providers` entry) as managed layers, re-asserted every
boot. It does **not** own the selection keys in the same sense: those ride the reserved
`selection` namespace, and a user's interactive model change wins until yolo's own selection
value changes. codex owns its `auth.json`; yolo never writes it.

**Forbidden.** Never write a `[model_providers.amazon-bedrock]` field outside codex's
permitted five. Never put a credential value in the composed table or in `YOLO_PROVIDERS` —
the name crosses, the value is hydrated per derive invocation. Never emit a selection whose
provider the catalog dropped. Never carry a region allowlist or a model catalog in core.

**What done looks like.**
1. `yolo -p bedrock-gpt -- codex` in a jail with `AWS_BEARER_TOKEN_BEDROCK` and a region
   completes one turn against GPT-5.6, and `codex doctor` reports provider
   `amazon-bedrock`, wire api `responses`, endpoint `bedrock-runtime.<region>`.
2. The same flag on `opencode` and on `pi` completes one turn against the same model id.
2b. `yolo -p bedrock-gpt-mantle -- codex` completes one turn, and `codex doctor` reports the
   `bedrock-mantle` endpoint — the two families demonstrably side by side, which is the
   point of shipping both.
3. `yolo -p codex=bedrock-gpt -- codex` leaves `CLAUDE_CODE_USE_BEDROCK` **unset** in the
   jail env (D2 closed) — or, if OQ-BR4 rules otherwise, the briefing says why it is set.
4. Dropping the profile leaves each agent's interactively-chosen model untouched.
5. A user overriding `providers.bedrock-openai.models` to Anthropic ids reaches Claude on
   Bedrock through codex, with no yolo change — the "not just GPT" claim, demonstrated.

---

## 9. Non-goals

- **No credential lifecycle.** No key rotation, no short-term-key minting, no refresh
  daemon, no broker. `AWS_BEARER_TOKEN_BEDROCK` arrives through `env_sources` or the ambient
  chain like any other secret.
- **No `~/.aws` mount.** Whether the jail should see the host's AWS config is a host-file
  grant question with its own machinery and its own threat model; this design assumes only
  environment variables. (It composes fine with such a grant if one is added later.)
- **No model catalog in yolo.** The `models` map is three aliases a user can replace. yolo
  will not track Bedrock's model list, its region matrix, or its pricing.
- **No new agent.** copilot and agy have no Bedrock path and this design does not invent one.
- **No Converse-for-everyone.** pi's `bedrock-converse-stream` is used because pi ships it;
  no canonical `wire_api` name is coined for Converse (**OQ-BR5** if that changes).
- **Not a claude change.** packs/claude's four Bedrock contributions stay exactly as they
  are, except as D2's fix may narrow the env gate.

---

## 10. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **Gateway arm only** — one provider with a full `/openai/v1` base URL, no native bindings, no new pack | **Rejected as the primary path, adopted as the escape hatch (§6.4).** It works and costs almost nothing, but it discards SigV4, the credential chain, codex's `auth.json` Bedrock mode and opencode's region/profile options, and it makes yolo the owner of a URL it must keep current. It is the right answer for an agent with no native path. |
| **Ship one family only** — pick runtime, document mantle as a manual config | **Rejected on the maintainer's ask.** Both are wanted for comparison, and the second entry costs one JSON object plus one line in each derive. Runtime remains the *recommended* one, in the README. |
| **One provider, `endpoint_family` as a profile OPTION** | **Rejected — it cannot work.** Options are a flat name→value map; `models` is a provider field the option layer never reaches. The option would move the endpoint and leave the ids, which is P1's failure with a knob attached (§5). |
| **One `bedrock` provider for all four agents** | **Rejected.** Claude wants Anthropic ids and codex wants GPT ids through the same `default` alias. Separate entries is what the schema already calls the ordinary case. |
| **Move `bedrock` out of packs/claude into the new pack** | **Rejected as unnecessary churn.** Sole ownership means the name can only live in one place, and it already lives somewhere that works. Moving it renames nothing a user types but risks a collision for no gain. |
| **Match the provider by NAME in each derive** (`if name == "bedrock-openai"`) | **Rejected** — `stringly-typed-references-principle.md` exists for this, and it would silently break the moment a user declares their own Bedrock provider under another name. |
| **A `bedrock` key in the open `endpoints` map, with no URL** | **Rejected — unrepresentable.** `contributes.go:1670` refuses an endpoint with no `base_url`. |
| **Coin `bedrock-converse` as a fourth canonical `wire_api`** | **Deferred (OQ-BR5).** Only pi consumes it, and pi is reachable through the native marker without it. Coin it if a second Converse consumer appears. |

---

## 11. Risks

| Risk | Mitigation |
| :--- | :--- |
| **R1.** codex tightens its built-in-provider override list and `base_url` stops being permitted — the runtime entry loses its way in. | The permitted list is a string in the binary and is re-checkable in seconds (§14). Pin the codex version in the evidence table and re-verify on upgrade; the fallback is the gateway arm under a non-reserved id. |
| **R2.** D4's fallback metadata makes codex mis-estimate the context window and compact too early or too late against a 1M-token model. | Measurable in one session. If it bites, the escape is already shipped: `-p bedrock-gpt-mantle` is the family whose ids codex's catalog knows, and it moves both halves at once. |
| **R3.** The three agents' shared `amazon-bedrock` id drifts apart (one renames it). | Each derive already owns its agent's spelling; a rename is one line in one derive, with provenance. |
| **R4.** No end-to-end request is made during implementation, and this ships on schema reading alone — the standing weakness of every provider integration in this repo. | The done-conditions in §8 are all live turns. `codex doctor` settles codex without burning a turn; the other two need one real request each. |
| **R5.** Bedrock IAM needs `bedrock:InvokeModel` on the account's **default project** (`arn:aws:bedrock:{region}:{account}:project/default`) in addition to the inference profile — a policy the existing invoke-only `matt-bedrock` IAM user may not carry. | Test with the real account before declaring the arm done; the failure is an AccessDenied naming the project ARN, which is self-diagnosing. |

---

## 12. What I would build, in order

1. **Fix D1** — `env_key`, with a test that pins the emitted key against the codex field
   list. It is a one-line fix to a defect that makes every custom codex provider
   credential-less, and the gateway arm depends on it. Ship it alone.
2. **Rule OQ-BR2** (the marker) and add the field to `packdecl` with its tolerance
   behaviour. Nothing renders yet; the schema is the thing three derives will key on.
3. **The `bedrock` pack** — both providers, both profiles, README (including which family
   is recommended and why). Selecting either changes nothing observable until step 4, which
   makes it a safe landing.
4. **The codex derive binding** — the pin, `aws.region`, the selection. codex first because
   it is the motivating case and because `codex doctor` verifies it cheaply.
5. **opencode and pi bindings** — mechanically similar, each with its own provenance
   comment recording the version its spelling was read from.
6. **Close D2** (per OQ-BR4's ruling) with a test that fails when the call site is deleted.
7. **The gateway-arm recipe** in the user guide, and the model-id/endpoint-family pairing
   (P1) stated where a user will hit it.
8. **Fold the settled parts into `docs/reference/providers.md`** and retire this doc via
   `system-doc`.

---

## 13. Open Questions

1. 💬 **OQ-BR1: Provider and profile naming.** The proposal ships providers
   `bedrock-openai` / `bedrock-openai-mantle` and profiles `bedrock-gpt` /
   `bedrock-gpt-mantle`, alongside claude's existing `bedrock`/`bedrock`. The profile names
   are what a user types, so they are a surface, not internal ids. Stakes: these are the
   strings in this design that are expensive to change later, `bedrock-gpt-mantle` is long
   for something typed often, and `bedrock` for claude vs `bedrock-gpt` for everything else
   is an asymmetry a reader will trip on.

   _Leaning:_ Ship them as proposed. They read correctly at the point of use and leave
   claude's shipped name alone. A shorter `-p gpt` is tempting but would collide with a
   future first-party OpenAI provider, and the mantle one is typed rarely enough that
   explicit beats short.

   <!-- vantage: oq id=OQ-BR1 leaning="Ship bedrock-openai / bedrock-openai-mantle and bedrock-gpt / bedrock-gpt-mantle as proposed. They read correctly at the point of use and leave claude's shipped bedrock name alone. A shorter -p gpt would collide with a future first-party OpenAI provider, and the mantle profile is typed rarely enough that explicit beats short." -->

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 **OQ-BR2: How a derive recognizes a native-Bedrock provider.** An endpoint-less
   provider needs a marker, since all three derives currently gate on a URL. Candidates: a
   new open-vocabulary `service` field on the provider kind; matching the provider name;
   treating "has `region`, has no `endpoints`" as the marker implicitly. Stakes: this is the
   schema addition the whole native arm keys on, and it is the one piece of this design that
   touches core rather than a pack.

   _Leaning:_ The `service` field, open vocabulary, unknown values inert. Explicit,
   greppable, survives a second regional service, and it is the only candidate that does not
   put a service name where a protocol name belongs. The implicit "region and no endpoints"
   reading fails today: packs/claude's `bedrock` provider ships neither.

   <!-- vantage: oq id=OQ-BR2 leaning="The `service` field, open vocabulary, unknown values inert. Explicit, greppable, survives a second regional service, and it is the only candidate that does not put a service name where a protocol name belongs. The implicit 'region and no endpoints' reading fails today: packs/claude's `bedrock` provider ships neither." -->

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 **OQ-BR3: Does yolo ship model aliases at all, or only the marker?** The proposal
   ships three (`default`/`balanced`/`fast` → `global.openai.gpt-5.6-{sol,terra,luna}`). The
   alternative is an empty `models` map, leaving every user to write their own ids. Stakes:
   shipped aliases are the difference between `-p bedrock-gpt` working out of the box and
   being a two-step setup — but they are also a model list yolo now has to not-let-rot, and
   §9 says yolo tracks no catalog.

   _Leaning:_ Ship the three. They are a *default*, not a catalog: a wrong one is overridden
   in two lines, and the alternative makes the flag useless on first use. Say in the pack
   README when they were checked and against what.

   <!-- vantage: oq id=OQ-BR3 leaning="Ship the three aliases. They are a default, not a catalog: a wrong one is overridden in two lines, and the alternative makes the flag useless on first use. Record in the pack README when they were checked and against what." -->

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 **OQ-BR4: Fix D2's wide env gate, or accept the leak and document it?** Selecting
   `bedrock` for one CLI fires every pack's `bedrock`-gated env, jail-wide — so
   `-p codex=bedrock-gpt` today would also set `CLAUDE_CODE_USE_BEDROCK=1` if a pack gated
   on that name. Options: narrow the gate to the pack's own bins (breaks the CLI-less pack
   case the wide pass exists for); scope the env by agent the way `packoverlay` already does
   (env has no surface naming an agent, so this needs a new binding); or accept it and say
   so in the briefing. Stakes: it is a correctness question about a shipped mechanism, and
   a cross-agent Bedrock profile is what turns it from theoretical into routine.

   _Leaning:_ Scope it, following `packoverlay.go:194`'s precedent — the wide pass was a
   reachability fix for CLI-less packs, and "reachable" should not have meant "global". But
   it is a change to a shipped rule with its own OQ history, so it is the maintainer's call
   whether it rides this work or gets its own.

   <!-- vantage: oq id=OQ-BR4 leaning="Scope the env gate by agent, following `packoverlay.go:194`'s precedent — the wide pass was a reachability fix for CLI-less packs, and 'reachable' should not have meant 'global'. But it changes a shipped rule with its own OQ history, so it is your call whether it rides this work or gets its own." -->

   **Answer:**
   > _(empty — fill in when decided)_

5. 💬 **OQ-BR5: Is pi bound through `bedrock-converse-stream`, or through the gateway?** pi
   ships a native Converse client, but reaching it means the derive emits an `api` value
   with no canonical `wire_api` behind it — a per-agent fact with no cross-agent vocabulary.
   The alternative is pointing pi at `/openai/v1` with `openai-responses`, which stays
   entirely inside the existing three-name enum. Stakes: whether this design coins a fourth
   canonical protocol name, and whether pi gets SigV4 or needs a bearer token.

   _Leaning:_ Native Converse, no new canonical name — the `service` marker already says
   "this is Bedrock", and what pi does with that is pi's derive's business (OQ-CS8). Coining
   `bedrock-converse` for one consumer would be the enum growing to describe a client, not a
   protocol.

   <!-- vantage: oq id=OQ-BR5 leaning="Native Converse for pi, no new canonical `wire_api` name — the `service` marker already says 'this is Bedrock', and what pi does with that is pi's derive's business (OQ-CS8). Coining `bedrock-converse` for one consumer would be the enum growing to describe a client, not a protocol." -->

   **Answer:**
   > _(empty — fill in when decided)_

6. 💬 **OQ-BR6: Should the launch refuse when no region is resolvable?** §8 proposes
   refusing, on the grounds that every native client fails without one and a boot that dies
   at first request is worse. The counter: yolo cannot see `AWS_REGION` arriving from an
   ambient chain the agent can read, so a refusal could be wrong — the same
   "unproven fact emits nothing" discipline `hostloopback.go` follows. Stakes: a false
   refusal blocks a working setup; a missing one costs a confusing first-request failure.

   _Leaning:_ Refuse only when the provider declares no `region` **and** no `AWS_REGION` /
   `AWS_DEFAULT_REGION` is in the composed jail environment — both of which yolo can see at
   launch. Anything beyond that (an `~/.aws/config` region) is unproven, and unproven emits
   nothing.

   <!-- vantage: oq id=OQ-BR6 leaning="Refuse only when the provider declares no `region` AND no `AWS_REGION` / `AWS_DEFAULT_REGION` is in the composed jail environment — both of which yolo can see at launch. Anything beyond that (an `~/.aws/config` region) is unproven, and unproven emits nothing." -->

   **Answer:**
   > _(empty — fill in when decided)_

7. 💬 **OQ-BR7: Is `endpoint_family` its own provider field, or does it fall out of the
   marker?** §6.1 proposes a field beside `region` holding `runtime` | `mantle`, which each
   derive reads to decide whether to override codex's `base_url` and whether it can serve
   the entry at all. The alternative is folding it into `service` (`aws-bedrock-runtime` vs
   `aws-bedrock-mantle`), which adds no field but makes the marker carry two facts. Stakes:
   one schema field, and whether "is this Bedrock?" and "which Bedrock?" are one question or
   two — pi has to answer them separately, since it serves one family and not the other.

   <!-- vantage: oq id=OQ-BR7 leaning="Its own field. pi answers 'is this Bedrock' and 'which family' separately — it serves runtime and cannot serve mantle — so a marker carrying both facts would have to be destructured by every consumer anyway. A field beside region also keeps it out of a profile's reach, which is what makes the family and its model ids inseparable." -->

   _Leaning:_ Its own field. pi answers "is this Bedrock?" and "which family?" separately —
   it serves runtime and cannot serve mantle — so a marker carrying both facts would be
   destructured by every consumer anyway. A field beside `region` also keeps it out of a
   profile's reach, which is exactly what makes the family and its ids inseparable (§5).

   **Answer:**
   > _(empty — fill in when decided)_

---

## 14. Evidence, and how to re-check it

Everything in §2 and §4 is a fact about a third party, so it carries its source and its
date. Re-run these rather than trusting the table.

**codex** — all codex claims are from the shipped binary of **codex-cli 0.145.0**
(`@openai/codex-linux-x64`, `vendor/x86_64-unknown-linux-musl/bin/codex`), read
2026-09-04 with `strings` and never executed:

| Claim | String found |
| :--- | :--- |
| Built-in provider ids | `responses` `openai` `amazon-bedrock` `ollama`, adjacent |
| Override list | `` model_providers.<id> only supports changing `base_url`, `auth`, `http_headers`, `aws.profile`, and `aws.region`; other non-default provider fields are not supported `` |
| Default base URL (mantle) | `https://bedrock-mantle.` + `.api.aws/openai/v1`, beside `model-provider/src/amazon_bedrock/mantle.rs` |
| Region sources | ``Amazon Bedrock bearer token auth requires `model_providers.amazon-bedrock.aws.region`, `AWS_REGION`, or `AWS_DEFAULT_REGION` `` |
| Bearer first, then chain | `AWS_BEARER_TOKEN_BEDROCK`; `BedrockApiKeyAuth` struct `{api_key, region}` in `auth.json`; *"Bedrock API key auth is only supported by the Amazon Bedrock model provider"* |
| Model slugs | `gpt-5.6-sol` → `openai.gpt-5.6-sol`, `-terra`, `-luna`, `gpt-5.5`, `gpt-5.4` |
| Provider field list (**no `api_key_env`**) | `env_key` `env_key_instructions` `experimental_bearer_token` `aws` `query_params` `http_headers` `request_max_retries` `stream_max_retries` `stream_idle_timeout_ms` `websocket_connect_timeout_ms` `requires_openai_auth` `supports_websockets` — `struct ModelProviderInfo with 17 elements` |
| Managed entry | *"configuration changed while clearing the managed Amazon Bedrock model provider; retrying once"* |
| Unknown model tolerance | *"Unknown model … is used. This will use fallback model metadata."* |
| Mantle region allowlist | *"Amazon Bedrock Mantle does not support region `…`"* |

**pi** — pi-ai **0.82.1**, the copy installed in this jail (read, never run), 2026-09-04:
`dist/compat.js:108-119` lists `bedrock-converse-stream` among the ten `BUILTIN_APIS`;
`dist/api/bedrock-converse-stream.js:1-60` shows `@aws-sdk/client-bedrock-runtime`,
`AWS_PROFILE`, `AWS_BEARER_TOKEN_BEDROCK`, `AWS_BEDROCK_SKIP_AUTH`, and region resolution
"ARN-embedded > explicit option > env vars > SDK default chain". **Re-read at the shipped
version before writing pi's derive**: `local-model-endpoints.md` measured pi's compat block
changing between 0.82.1 and 0.84.4 within one doc's lifetime, so a transcription from 0.82.1
is a starting point, not a fact about what a jail installs today.

**opencode** — vendor documentation only, 2026-09-04 (opencode is not installed in this
jail): `amazon-bedrock` provider with `region`/`profile`/`endpoint` options, bearer token
ahead of the credential chain. **Unconfirmed against a shipped artifact** — re-read the
installed package before writing its derive, the way pi's and codex's were.

**AWS** — the GPT-5.6 Sol model card and the endpoints page, both read 2026-09-04:
endpoint URLs, the runtime/mantle model-id split, the region matrices, the API support
tables, the `project/default` IAM requirement, and the Global CRIS discount. Bedrock API key
mechanics (long-term vs short-term, ≤12h, two per IAM user) from the IAM user guide.

**Sources:**
[Bedrock endpoints](https://docs.aws.amazon.com/bedrock/latest/userguide/endpoints.html) ·
[GPT-5.6 Sol model card](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-openai-gpt-56-sol.html) ·
[Cross-Region inference for GPT-5.6](https://aws.amazon.com/blogs/machine-learning/introducing-cross-region-inference-for-openai-gpt-5-6-models-on-amazon-bedrock/) ·
[Bedrock API keys (IAM)](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_credentials_bedrock.html) ·
[Codex with Amazon Bedrock](https://learn.chatgpt.com/docs/amazon-bedrock) ·
[codex PR #18744 — built-in Bedrock provider](https://github.com/openai/codex/pull/18744) ·
[opencode providers](https://opencode.ai/docs/providers/)
