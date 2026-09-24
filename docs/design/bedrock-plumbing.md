---
title: "Bedrock plumbing: one service, four agents, two arms"
date: 2026-09-04
status: in-review
tags: [packs, providers, profiles, bedrock, aws, codex, gpt]
summary: "How GPT-5.6 (and anything else Bedrock serves) reaches codex, pi and opencode. The surprise: all three already ship their own amazon-bedrock provider, and each takes a Bedrock API key if one is set and otherwise whatever the AWS credential chain resolves. So the work is not an endpoint and not a credential. It is picking ONE endpoint family so that one model-id spelling is right for all of them, and teaching three derives to bind natively to an endpoint-less provider. Which credential a jail carries is sso-backed-bedrock.md's question; this design works with all three that yolo supports."
---

# Bedrock plumbing: one service, four agents, two arms

**Status:** DESIGN, 2026-09-24. Eight rulings are owed. **This arm is still unbuilt, while the
credential half is built.** Drafted 2026-09-04, amended 2026-09-18, [OQ-BR8](#OQ-BR8) opened
2026-09-23, and refreshed 2026-09-24 against the maintainer's credential ruling (below).

Bedrock reaches yolo through two designs, and they are siblings, not alternatives:

- **This doc** answers *which endpoint family and which model-id spelling each agent needs*.
  It covers the NATIVE arm, where codex, opencode and pi each ship an `amazon-bedrock`
  provider of their own.
- **[`sso-backed-bedrock.md`](sso-backed-bedrock.md)** answers *where the credential comes
  from*. Its host credential service, its jail-side adapter and `packs/aws-auth` landed on
  2026-09-18 (`b94351fe`, `e76e43b2`, `9fc4879d`), and a podman jail has been able to reach
  the service since 2026-09-20 (`62e553a8`). It has not yet been run against a live
  `aws sso login` ([the pack README](../../packs/aws-auth/README.md)).

That doc's plan has done-condition 7 blocked on this one: codex, pi and opencode have no
Bedrock provider or region until this arm lands. Nothing of THIS doc is built, apart from the
[D1](#7-traps--read-before-writing-code) fix (`f7b14308`, 2026-09-15). Code claims verified
against `f491d192` on 2026-09-24. Every vendor claim carries its source and date in
[§14](#14-evidence-and-how-to-re-check-it).

**The short version.** Bedrock reaches an agent two ways, and yolo should build the first
and document the second.

- The **native arm** *(coined here)* is the path where the agent's own client speaks to AWS.
  codex, opencode and pi each ship a built-in Bedrock provider, so yolo supplies a **region
  and a model id**, and never a URL. The credential is not the native arm's to supply: each
  of the three takes a Bedrock API key if one is set, and otherwise whatever the
  [AWS credential chain](https://docs.aws.amazon.com/sdkref/latest/guide/standardized-credentials.html)
  resolves, so any credential yolo supports works unchanged
  ([§6.5](#65-the-credential-three-are-supported)).
- The **gateway arm** *(coined here)* is Bedrock's own OpenAI-compatible `/openai/v1` route.
  It is already an ordinary yolo provider today and needs no new machinery at all.

The work that is actually hard is neither. **The three native clients default to different
Bedrock endpoint families, and the model-id spelling differs between them**:
`openai.gpt-5.6-sol` on `bedrock-mantle`, `global.openai.gpt-5.6-sol` on `bedrock-runtime`. So
the family and the model ids are **one entry, shipped twice**: `-p bedrock-gpt` for runtime and
`-p bedrock-gpt-mantle` for mantle, both available to measure against each other.

**Which credential — ruled 2026-09-24 by the maintainer**, and recorded as
[`OQ-SSO7`](sso-backed-bedrock.md#13-decision-ledger) in the sso-backed Bedrock design's Decision
Ledger: *"We will support bearer tokens, we will support secret and key, and we also need to
support sessions through single sign-on. That's the big one."* So yolo supports three Bedrock
credentials:

1. a Bedrock API key, as `AWS_BEARER_TOKEN_BEDROCK`;
2. a static access key and secret, as `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` — for
   example through `env_sources`, which is the maintainer's current setup;
3. an SSO session through an assumed role, which `packs/aws-auth` delivers through the
   container-credentials endpoint. **This is the primary one.**

The native arm takes any of the three, one at a time: given two, every client silently picks
one.
[§6.5](#65-the-credential-three-are-supported) has what that means, what AWS itself says about
the three, and the one place a credential choice does bite: the gateway arm can carry only an
API key.

**The most important section is [§5](#5-two-families-two-providers--because-the-family-and-the-ids-are-one-entry)**: why the family is a provider entry rather than a
knob. Everything else follows from it. **[§7](#7-traps--read-before-writing-code) is the one to read before writing code**: five
traps. Two of them are live in shipped code today, and a third was fixed on 2026-09-15.

**Needs your ruling:** [OQ-BR1](#OQ-BR1), [OQ-BR2](#OQ-BR2), [OQ-BR3](#OQ-BR3), [OQ-BR4](#OQ-BR4), [OQ-BR8](#OQ-BR8), [OQ-BR5](#OQ-BR5), [OQ-BR6](#OQ-BR6), [OQ-BR7](#OQ-BR7).
Rule [OQ-BR4](#OQ-BR4) and [OQ-BR8](#OQ-BR8) together; they are one function. The 2026-09-24
refresh changed the premise of five of them, and each says how in a ⚠ note:
[OQ-BR2](#OQ-BR2), [OQ-BR3](#OQ-BR3), [OQ-BR4](#OQ-BR4), [OQ-BR5](#OQ-BR5) and
[OQ-BR6](#OQ-BR6). **One more question is owed but is not this doc's to ask:** whether static
keys delivered beside `aws-auth`'s pointer should be refused the way a bearer beside it already
is ([§6.5](#65-the-credential-three-are-supported)). It belongs to
[`sso-backed-bedrock.md`](sso-backed-bedrock.md), which owns where credentials come from.

**Scope note.** The general problem this doc's P1 names — *a model id is provider-local, so
every provider switch is a rename, and yolo only does half of it* — is split out into
[`provider-switching.md`](provider-switching.md). Nothing about it is Bedrock-specific and
its fix lands in the provider system rather than in a pack. This doc neither depends on that
one nor blocks it.

**Reads with:** [`../reference/providers.md`](../reference/providers.md) (the provider
system this extends — catalog, selection, derives, the canonical `wire_api` vocabulary),
[`../reference/protocol-resolution.md`](../reference/protocol-resolution.md) (how an agent
and a provider are paired by wire protocol, and why an endpoint-less provider passes),
[`zai-plumbing.md`](../reference/zai-plumbing.md) (the same exercise for a plain HTTP provider; this doc
is its sequel and borrows its resolution vocabulary),
[`../research/local-model-endpoints.md`](../research/local-model-endpoints.md) (the
per-agent config surfaces, source-verified),
[`provider-switching.md`](provider-switching.md) (the sibling this doc's P1 spawned — tier
aliases, and the id a deselected profile leaves behind),
[`provider-credential-scope.md`](provider-credential-scope.md) (why a credential delivered
through `env_sources` reaches every agent in the jail, whichever profile is selected).

---

## 1. Verdict and principles

**Build the native arm as endpoint-less providers — one per model family, one per endpoint
family — plus three derive bindings. Ship the gateway arm as a documented config recipe,
not as code.**

The native arm is where the leverage is. Three agents already implement Bedrock, and what they
implement is better than what a gateway gives them:
[SigV4](https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv.html) (AWS's request
signing, done with whatever the credential chain resolves) or an API key, cross-Region
inference, and in codex's case a first-class `bedrock_api_key` auth mode stored in `auth.json`.
Reimplementing that as "a base URL plus an API key" throws away work someone else already did,
pins yolo to a URL it must keep current, and — [§6.5](#65-the-credential-three-are-supported)
— shuts out the SSO credential that is the primary one.

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
provider has been exactly this since it shipped (its `kind: "provider"` contribution is a name
and nothing else), and the rest of the system already treats it correctly:

- [the credential preflight](../reference/providers.md#the-credential-preflight) demands no
  credential for an entry with no endpoints;
- [protocol resolution](../reference/protocol-resolution.md) treats a provider that names no
  endpoint as having "repointed nothing", so it resolves for every agent rather than being
  refused (`ResolveProtocol` in `internal/packload/protocolresolution.go`).

**P3. One model-id spelling per provider entry, and every agent that selects it gets that
one.** The `models` map is one map and the alias a profile names resolves through it once,
so an agent-dependent spelling is not a configuration — it is a `models` map that is wrong
somewhere. The corollary is the shape of [§5](#5-two-families-two-providers--because-the-family-and-the-ids-are-one-entry): two endpoint families means two entries, never
one entry with a switch, because a switch is exactly the thing that could move the endpoint
and leave the ids.

---

## 2. What Bedrock is now — measured 2026-09-04

Two endpoints, both OpenAI-compatible, and they are **not** interchangeable.

| | `bedrock-runtime` | `bedrock-mantle` |
| :--- | :--- | :--- |
| Base URL | `https://bedrock-runtime.{region}.amazonaws.com/openai/v1` | `https://bedrock-mantle.{region}.api.aws/openai/v1` (⚠ AWS's own pages disagree on `/openai/v1` versus `/v1` — [§14](#14-evidence-and-how-to-re-check-it)) |
| GPT-5.6 Sol model id | `us.openai.gpt-5.6-sol` / `global.openai.gpt-5.6-sol` — **in-Region is not available** | `openai.gpt-5.6-sol` — **Geo and Global are not supported** |
| Regions serving GPT-5.6 Sol | 25+, via Geo/Global cross-Region inference | `us-east-1`, `us-east-2` only |
| APIs | Responses, Chat Completions, Converse, InvokeModel, Messages | Responses, Chat Completions, Messages |
| Auth | SigV4 **or** Bedrock API key (bearer) | SigV4 **or** Bedrock API key (bearer) — re-read 2026-09-24, [§6.5](#65-the-credential-three-are-supported) |
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

- **The credential is not this design's choice.** Both endpoints accept a SigV4-signed
  request or a Bedrock API key (the Auth row above). Which credential a jail carries is
  [`sso-backed-bedrock.md`](sso-backed-bedrock.md)'s question, and
  [§6.5](#65-the-credential-three-are-supported) is how this design meets each of the three it
  supports.
- **Bedrock is not only OpenAI models.** The same `/openai/v1` route serves Anthropic
  models (AWS's own example calls it with `model="us.anthropic.claude-sonnet-4-6"`), and
  the Messages API serves them natively. Nothing in this design is GPT-specific except the
  contents of one `models` map.

---

## 3. What yolo has today

The provider system is fully built and is described in
[`../reference/providers.md`](../reference/providers.md); this section states only the
parts Bedrock lands on.

- **One `bedrock` provider exists, and it is claude's.** `packs/claude/pack.json` ships four
  contributions as a set: `kind: "provider"` named `bedrock` (no endpoints, no
  `api_key_env_name`, no models), `kind: "profile"` named `bedrock` selecting it, a
  `profile`-gated `kind: "env"` setting `CLAUDE_CODE_USE_BEDROCK=1`, and a `profile`-gated
  `config-overlay` writing the same key into `claude/settings`. It is the worked example
  the reference doc names
  ([Two channels, split by payload type](../reference/providers.md#two-channels-split-by-payload-type)).
- **The credential service is a separate pack.** `packs/aws-auth` ships the `aws-auth`
  loophole and one `kind: "env"` contribution, gated on the profile name `bedrock`, that sets
  `AWS_CONTAINER_CREDENTIALS_FULL_URI`. packs/claude does not `need` it yet (the plan's step
  5), so a user selects both, as the pack README's example does.
- **Only the claude derive reads `region`.** claude's `yolo.env` producer in
  `packs/claude/derive.lua` maps `p.region` → `AWS_REGION`. The schema field is documented as
  exactly this: the provider `Region` field's comment in `internal/packdecl/contributes.go`
  reads *"Region is the region a regional provider is reached through — Bedrock's address
  half"*. No other derive reads it.
- **Every other derive drops an endpoint-less provider on the floor.** codex, pi, opencode and
  oh-omp each have a `providerEndpoint` helper returning nil when the provider names no URL for
  the protocol that agent speaks, and each gates its catalog row and its selection on that one
  predicate. copilot's derive composes nothing for a provider with no endpoint, and says so in
  a comment naming Bedrock. A provider with no URL is therefore invisible to every agent but
  claude — correctly, today, because they had no other way to reach it.
- **The single-protocol `base_url` shorthand is gone from user config**
  ([protocol-resolution.md](../reference/protocol-resolution.md#the-single-protocol-base_url-shorthand-is-removed)).
  codex's and copilot's derives keep a read arm for it, only because a jail launched by an
  older host can still carry it in its config snapshot.
- **Provider names are sole-owned across packs** (the `KindProvider` row in
  `internal/packdecl/kinds.go`, `CombineExclusive`): two packs shipping one provider name is a
  launch-refusing collision; one pack shipping two names is the ordinary multi-provider pack.

So: claude on Bedrock is wired, though end to end it is unmeasured
([providers.md](../reference/providers.md#two-channels-split-by-payload-type)). Nothing else can
see Bedrock at all.

---

## 4. What each agent can actually do

Verified from the shipped artifacts, not from documentation, except where noted.

| Agent | Native Bedrock support | How it authenticates | Evidence |
| :--- | :--- | :--- | :--- |
| **claude** | Yes — `CLAUDE_CODE_USE_BEDROCK=1` | the AWS credential chain; `AWS_REGION`. The string `AWS_BEARER_TOKEN_BEDROCK` is also in the binary (presence only; its precedence is not read here) | already shipped in yolo; claude 2.1.282 binary, 2026-09-24 |
| **codex** | Yes — built-in provider id `amazon-bedrock`, `wire_api = responses` | `AWS_BEARER_TOKEN_BEDROCK` first, else the AWS SDK credential chain; region from `model_providers.amazon-bedrock.aws.region`, `AWS_REGION` or `AWS_DEFAULT_REGION` | codex-cli 0.145.0 binary, re-read 2026-09-24, [§14](#14-evidence-and-how-to-re-check-it) |
| **opencode** | Yes — built-in provider `amazon-bedrock` with `options: {region, profile, endpoint}` | a bearer (`AWS_BEARER_TOKEN_BEDROCK` or `/connect`) takes precedence over the credential chain; region from `options.region`, else `AWS_REGION`, **else `us-east-1`** | opencode 1.18.32 bundle, 2026-09-22 and 2026-09-24 |
| **pi** | Yes — built-in provider `amazon-bedrock` on the API `bedrock-converse-stream` | a stored key, else `AWS_BEARER_TOKEN_BEDROCK`, else `AWS_PROFILE`, else access keys, else the container-credentials variables, else a web-identity token file; `AWS_BEDROCK_SKIP_AUTH=1` disables | pi-ai **0.87.1**, `dist/providers/amazon-bedrock.js`, 2026-09-24 |
| **copilot** | No | its bring-your-own-key mode takes a URL and `COPILOT_PROVIDER_API_KEY` — an API key, so the gateway arm only | `packs/copilot/derive.lua` |
| **oh-omp** | **Unverified** — not installed in this jail | — | shipped as `packs/omp` after this doc was drafted |
| **agy** | No — model transport is a closed enum (`ccpa`/`gemini`/`stubby`) | — | `local-model-endpoints.md` §"agy" |

**The convergence is the finding.** codex, opencode and — since the provider it ships in pi-ai
0.87.1 — pi chose the same provider id (`amazon-bedrock`), the same API-key variable
(`AWS_BEARER_TOKEN_BEDROCK`), the same precedence (an API key over the credential chain) and
the same region knobs. That is not a coincidence to design around — it is a de-facto interface,
and yolo's job is to feed it. The precedence is also why a jail must never carry an API key
beside a chain credential ([§6.5](#65-the-credential-three-are-supported)).

**Where they diverge is the endpoint family**, and that divergence is invisible until the
first request:

- codex's built-in provider is a **mantle** client: its baked base URL is
  `https://bedrock-mantle.{region}.api.aws/openai/v1`, from `amazon_bedrock/mantle.rs`, and
  its bundled catalog maps the slug `gpt-5.6-sol` to the bare id `openai.gpt-5.6-sol`. It
  carries its own region allowlist (*"Amazon Bedrock Mantle does not support region …"*).
- opencode routes **per model**: its bundled catalog sends bare-spelled OpenAI ids to mantle
  and geo-prefixed ones to runtime ([§6.3](#63-what-each-derive-emits)'s measurement).
- pi drives Converse, which is served on **`bedrock-runtime`** only. ⚠ Its 0.87.1 catalog lists
  the bare `openai.gpt-5.6-sol` against a `bedrock-runtime` base URL too — the P1 failure, shipped
  in a vendor's catalog. Read client-side only; no request was made.

So with no intervention, `-p bedrock-gpt -- codex` and `-p bedrock-gpt -- pi` want
*different model id spellings for the same model*. P3 says that cannot stand.

---

## 5. Two families, two providers — because the family and the ids are one entry

**The proposal: ship BOTH endpoint families, each as its own provider entry, and let a
profile name pick between them.** `-p bedrock-gpt` is runtime; `-p bedrock-gpt-mantle` is
mantle. One word apart at the CLI, and both available to try.

The reason this is two *providers* rather than one provider with an `endpoint_family`
option is structural, not stylistic. [§2](#2-what-bedrock-is-now--measured-2026-09-04) established that the model id is a function of the
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
codex's own default stand. Either way codex's built-in Bedrock client — SigV4, API-key
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
| opencode | yes, natively (its SDK resolves runtime) | yes per its catalog's per-model markers ([§6.3](#63-what-each-derive-emits)); no request made |
| pi | yes, via `bedrock-converse-stream` | **no** — the model card marks Converse unsupported on mantle |
| gateway arm ([§6.4](#64-the-gateway-arm-ships-as-documentation)) | yes | yes |

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
alias `default` through *a* map, so each needs its own — which the provider `Name` field's
comment in `internal/packdecl/contributes.go` already calls the ordinary case ("one pack
shipping two names is two contributions").

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
reasoning as [§5](#5-two-families-two-providers--because-the-family-and-the-ids-are-one-entry), and the same reasoning that put `region` there
(the `Region` field's comment in `internal/packdecl/contributes.go`: a service fact, saying
where the provider lives). A field a profile cannot reach is a field that cannot drift away from the `models`
map next to it. **[OQ-BR7](#OQ-BR7)** asks whether it is a distinct field at all or just falls out of
`service`.

`yolo -p bedrock-gpt -- codex` is then the whole user gesture. The credential arrives by
whichever of the three supported routes the user has set up
([§6.5](#65-the-credential-three-are-supported)), and neither entry above names any of them:
an entry with no `api_key_env_name` points at no credential, which is what lets one entry
serve all three. `-p bedrock-gpt-mantle` is the same gesture against the other family, and
the two can be compared back to back in one session.

### 6.2 How a derive recognizes "this is Bedrock"

An endpoint-less provider needs a marker, because every derive but claude's currently keys
reachability on a URL ([§3](#3-what-yolo-has-today)). Three candidates, and this is a real fork — **[OQ-BR2](#OQ-BR2)**.

The leaning is a new open-vocabulary `service` field on the provider kind: `"aws-bedrock"`
here, unknown values inert (no derive claims them, nothing renders — the same tolerance the
open `endpoints` key set already has, for the same version-skew reason the `Endpoints`
field's comment in `internal/packdecl/contributes.go` records). It is explicit, it is
greppable, and it survives a second regional service arriving later. Two costs the leaning
did not state when it was written are in [OQ-BR2](#OQ-BR2)'s ⚠ note: the word `service`
already names a contribution kind, and a user-declared provider's key set is closed, so the
field lands in two schemas rather than one.

What it is **not**: a `wire_api` value. Bedrock is not a protocol — its native clients speak
Responses, Converse and the AWS SDK's own shapes — and putting a service name in the
canonical protocol enum is the pass-through mistake [OQ-PT1](../reference/providers.md#oq-pt1) closed, one field over.

> [!WARNING]
> **An `endpoints` key cannot be the marker.** `validateProviderEndpoints` in
> `internal/packdecl/contributes.go` refuses an endpoint entry with no `base_url` (`endpoints[%q]: needs a "base_url"`), and a
> pack cannot ship a Bedrock URL because the URL contains a region it does not know. A
> `service` field or nothing.

### 6.3 What each derive emits

Each agent's binding lives in that agent's own derive — [OQ-CS8](../reference/providers.md#oq-cs8), unchanged: core learns no
agent's vocabulary, and core resolves no model.

| Agent | Catalog | Selection | Region / profile |
| :--- | :--- | :--- | :--- |
| **codex** | `[model_providers.amazon-bedrock]` with `aws.region`, plus `base_url` **only for the runtime family** (the mantle provider writes none, so codex's own default stands); **no other fields** — codex refuses them | `model_provider = "amazon-bedrock"`, `model = <resolved id>` | `aws.region` from `region`; `aws.profile` from the `aws_profile` option when set |
| **opencode** | ⚠ **This cell is WRONG as written — see the measurement below.** It proposes `provider["amazon-bedrock"] = { npm: "@ai-sdk/amazon-bedrock", options: { region, profile? }, models: {…} }`, and both of those fields would break the mantle family | `model = "amazon-bedrock/<resolved id>"` | `options.region`, `options.profile` |
| **pi** | `providers["bedrock-openai"] = { api: "bedrock-converse-stream", models: [...] }` — **runtime family only**; the mantle provider yields no pi row, because Converse is not served there. ⚠ Written against pi-ai 0.82.1, which had only the API id; 0.87.1 ships a built-in `amazon-bedrock` provider ([§4](#4-what-each-agent-can-actually-do)), so the row may bind to that the way codex's does — re-read before writing ([§14](#14-evidence-and-how-to-re-check-it)) | `defaultProvider` / `defaultModel` | region via `AWS_REGION` in the jail env |
| **claude** | none (claude has no catalog) | env, as today | `AWS_REGION` from `region` |

> [!WARNING]
> **MEASURED 2026-09-22 against the installed opencode 1.18.32, and the opencode row above would
> ship a defect.** Read statically from its bundle; no agent was started.
>
> **opencode already routes Bedrock's OpenAI-shaped models to mantle by default, with no user
> config.** Its vendored `@ai-sdk/amazon-bedrock` 4.0.166 ships a first-class
> `@ai-sdk/amazon-bedrock/mantle` factory, and its embedded models.dev catalog marks **13** bedrock
> models with `provider:{npm:"@ai-sdk/amazon-bedrock/mantle", api:"…bedrock-mantle.${AWS_REGION}.api.aws/openai/v1"}`
> — including all three GPT-5.6 ids in **bare** spelling. The geo-prefixed ids (`global.`, `us.`,
> `in.`) carry no override and so take the provider default, which is runtime. **So [§5](#5-two-families-two-providers--because-the-family-and-the-ids-are-one-entry)'s
> model-id premise is not merely right, opencode enforces it structurally.**
>
> **Which is why the proposed cell is a defect.** The config→catalog merge resolves
> `npm = config.provider?.npm ?? … ?? model.api.npm`, so a **provider-level** `npm:"@ai-sdk/amazon-bedrock"`
> **overrides the catalog's per-model mantle markers** and silently drags every bare-spelled id onto
> the runtime endpoint, where that spelling is wrong. `options.endpoint`/`baseURL` does the same via
> `baseURL !== "" ? baseURL : model.api.url`. The derive must therefore set mantle **per model**, or
> **omit `npm` entirely** and let the catalog decide.
>
> ⚠ **And `options.region` alone does not enable the provider.** Autoload gates on six credential
> signals — `AWS_PROFILE`/`options.profile`, `AWS_ACCESS_KEY_ID`, a bearer token, `options.apiKey`,
> `AWS_WEB_IDENTITY_TOKEN_FILE`, or the container-credentials vars — and **region is not one of them**.
> A derive that writes only `region` yields `autoload: false`.
>
> **What this measurement cannot show:** whether any of it reaches AWS. This is client-side intent
> only — no request was made, so whether that endpoint accepts those ids is still unmeasured.

Selection keys keep riding the reserved `selection` namespace with the edge-triggered apply
— nothing about Bedrock changes the "write on activation, never on absence" rule, and an
interactive `/model` still stands.

### 6.4 The gateway arm ships as documentation

For any agent with no native path (copilot), or a user who wants a plain HTTP provider, the
gateway arm needs **no yolo code at all**. The one defect that stood in its way,
[D1](#7-traps--read-before-writing-code), was fixed on 2026-09-15. It is an ordinary
`providers` entry, and it goes in the **user** config (`~/.config/yolo-jail/config.jsonc`):
an `endpoints.<protocol>.base_url` is user-scope only, and a workspace file carrying one is a
fatal config error ([providers.md, Current values](../reference/providers.md#current-values)).

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

> [!WARNING]
> **This arm carries an API key and nothing else.** `api_key_env_name` is the only credential
> field a yolo provider has, and every derive hands the variable it names to its agent as an
> API key. So on this arm the credential is (1) of the three, whatever the jail's other
> credentials are. Three consequences:
>
> - An SSO user (credential 3) or a static-key user (credential 2) has no way onto this arm
>   except a short-term key minted from their credentials. yolo does not mint one: that is
>   [`sso-backed-bedrock.md`](sso-backed-bedrock.md)'s option D, unbuilt
>   ([§6.5](#65-the-credential-three-are-supported) says why it is only this arm that needs it).
> - The credential preflight demands `AWS_BEARER_TOKEN_BEDROCK` for this entry, and a jail that
>   also carries `aws-auth`'s pointer is refused at launch. So this arm and the SSO arm cannot
>   share a jail today.
> - An AWS account that denies `bedrock:CallWithBearerToken` and
>   `bedrock-mantle:CallWithBearerToken` rejects every request this arm makes.

### 6.5 The credential: three are supported

**The ruling, and where it lives.** The maintainer ruled on 2026-09-24 that yolo supports three
Bedrock credentials, and [`sso-backed-bedrock.md`](sso-backed-bedrock.md#13-decision-ledger)
records it as [`OQ-SSO7`](sso-backed-bedrock.md#13-decision-ledger), because that doc owns where credentials come from. This section is
what the ruling means for the two arms here. It rules nothing.

Two terms, used in this section and throughout the doc:

- A **Bedrock API key** is AWS's name for a Bedrock-only credential that a client sends as-is,
  as an HTTP `Authorization: Bearer` header, and every client in [§4](#4-what-each-agent-can-actually-do)
  reads it from `AWS_BEARER_TOKEN_BEDROCK`. "Bearer" in this doc means this and nothing else.
  It comes in two kinds: a **long-term** key is an IAM service-specific credential with an
  expiry from one day to never; a **short-term** key is a SigV4-presigned token that lives at
  most twelve hours. It is **not** an access key.
- A **chain credential** *(coined here)* is anything the
  [AWS credential chain](https://docs.aws.amazon.com/sdkref/latest/guide/standardized-credentials.html)
  resolves — the ordered list of places an AWS SDK looks for signing keys — and signs onto each
  request with [SigV4](https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv.html). It
  is never sent as-is, which is what separates it from a bearer.

| | Credential | How it reaches a jail today | What the agent does with it | Refreshes inside a running jail? |
| :--- | :--- | :--- | :--- | :--- |
| **1** | a Bedrock API key | `env_sources` → `AWS_BEARER_TOKEN_BEDROCK` | sends it as a bearer | **no** — frozen at launch |
| **2** | a static access key and secret | `env_sources` → `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` | a chain credential: the chain's environment provider finds it, and the SDK signs with it | nothing to refresh — it is long-lived |
| **3** | an SSO session through an assumed role — **the primary one** | `packs/aws-auth`: the pointer `AWS_CONTAINER_CREDENTIALS_FULL_URI` ([`sso-backed-bedrock.md` §5](sso-backed-bedrock.md#5-the-recommended-shape)) | a chain credential: the chain's container-credentials provider fetches it | **yes** — the host service re-mints it |

**What the native arm needs from this: nothing.** codex, opencode and pi each take a bearer if
one is set and a chain credential otherwise ([§4](#4-what-each-agent-can-actually-do)), so any
one of the three works with no per-credential code in this design. The native entries declare
no `api_key_env_name`, so the credential preflight demands none of them, and the design writes
no credential anywhere.

> [!WARNING]
> **Never two at once.** In every client measured a bearer beats the chain, so a jail carrying
> credential 1 beside credential 3 authenticates with the frozen bearer and the SSO service is
> never asked — a silent wrong answer. yolo refuses that launch, with no escape hatch
> (`internal/awschain`). The rule and its reasons are
> [`sso-backed-bedrock.md` §8](sso-backed-bedrock.md#8-behaviour-this-design-specifies) and
> [the `aws-auth` README](../../packs/aws-auth/README.md); they are not restated here.
>
> ⚠ **Credential 2 beside credential 3 is the same failure, and nothing refuses it.** The
> chain's environment provider comes FIRST, ahead of the container-credentials provider (the
> order measured in [`sso-backed-bedrock.md` §11](sso-backed-bedrock.md#11-evidence-and-how-to-re-check-it)
> from pi's vendored AWS SDK). So static keys in the jail environment win, and the SSO arm
> never runs. `internal/awschain`'s refusal checks only the bearer variable. Whether it should
> also check the access-key pair is a question for [`sso-backed-bedrock.md`](sso-backed-bedrock.md), and it is not
> ruled here. MEASURED: the refusal's code. INFERRED for codex and opencode: they use the same
> environment-first order every AWS SDK documents; only pi's was read.

**What AWS says about the three** (read 2026-09-24; sources in
[§14](#14-evidence-and-how-to-re-check-it)):

- **API keys are not deprecated.** None of the pages read says so, and AWS updated its guidance
  on them in July 2026.
- **AWS prefers temporary STS credentials.** Its security blog: *"Our recommendation is to use
  temporary security credentials provided by AWS Security Token Service (AWS STS) service
  whenever possible as a preferred authentication method."* Credential 3 is exactly that
  ([STS](https://docs.aws.amazon.com/STS/latest/APIReference/welcome.html) is the AWS service
  that issues short-lived credentials, including the assumed-role ones `aws-auth` serves).
- **Short-term keys for production, long-term keys for exploration.** The user guide says a
  short-term key is *"Recommended for production use"* and a long-term key is *"Recommended
  only for exploration"*. The blog ranks both below STS: use one *"when your use case blocks the
  use of temporary AWS STS credentials"*.
- **An organization can switch API keys off entirely**, with a
  [service control policy](https://docs.aws.amazon.com/organizations/latest/userguide/orgs_manage_policies_scps.html)
  (an organization-wide deny) on `bedrock:CallWithBearerToken` and
  `bedrock-mantle:CallWithBearerToken` — one action per endpoint, and AWS says both must be
  denied. On such an account credential 1 fails, and so does anything built on it: the
  gateway arm, and [`sso-backed-bedrock.md`](sso-backed-bedrock.md#4-five-options)'s option D. Credentials 2 and 3 are unaffected.
- The pages read do not rank credential 2, a long-lived IAM user key signed with SigV4.

**Can a bearer be made from an SSO session? Yes.** A short-term key is a SigV4-presigned token
made from whatever AWS credentials the generator is handed. AWS's generator libraries document
it with an assumed-role provider, and the Java one says *"Generate tokens with any credential
provider."* No page read names IAM Identity Center (AWS's SSO service) as a source; that it
works from an SSO session is INFERRED, because the generator reads the default credential
chain and the chain includes SSO. Its lifetime is *"the minimum of the requested expiration
time and the AWS credentials' expiry time"*, capped at twelve hours, and it cannot be refreshed
or revoked on its own.

> [!IMPORTANT]
> **A bearer minted from `aws-auth`'s narrowed session lives at most an hour.** The narrowed
> credential is an assumed role taken from an SSO role session, which is
> [role chaining](sso-backed-bedrock.md#33-four-aws-words-this-doc-leans-on), capped at one
> hour by STS. A bearer inherits that expiry, and a bearer in the jail is frozen for the
> launch. So option D over a narrowed session would hand a jail a key that dies within an hour
> of launch; from an un-narrowed permission-set session it lasts that session's remaining 1–12
> hours. INFERRED from the two documented caps; not measured.

**Where that answer matters.** Not on the native arm: every native client takes a chain
credential, and `aws-auth` keeps credential 3 fresh, so an SSO user never needs a bearer there.
It matters only for a route or a client that accepts **only** a bearer:

- **AWS does not make the gateway bearer-only.** Its endpoints page marks *"AWS SigV4
  authentication"* supported on both `bedrock-runtime` and `bedrock-mantle`, and its Chat
  Completions page says *"The `bedrock-runtime` endpoint supports AWS SigV4 authentication and
  Amazon Bedrock API key authentication"*, with a SigV4 example signed for the service
  `bedrock` against `/openai/v1/chat/completions`. For mantle it says the endpoint *"supports
  Amazon Bedrock API key authentication, AWS credentials, and the OpenAI SDK"*, and names no
  signing service; OpenAI's own Go SDK and third-party gateways sign it as `bedrock-mantle`.
  One AWS page, the Responses API page, says an API key is *"required for OpenAI SDK"* while
  plain HTTP requests may use AWS credentials.
- **yolo makes the gateway bearer-only.** The gateway arm is a yolo provider, whose only
  credential field is `api_key_env_name`, and no derive tells an agent's generic
  OpenAI-compatible client to sign ([§6.4](#64-the-gateway-arm-ships-as-documentation)).
  MEASURED: the schema and the derives. Whether any agent's generic client *could* sign is
  per-client and unread here.
- **So option D becomes necessary exactly where the gateway arm is the only path**: copilot,
  which has no native Bedrock support, and any agent a user deliberately points at the
  gateway. An SSO user there needs a bearer minted from the session, which is
  [`sso-backed-bedrock.md`](sso-backed-bedrock.md#4-five-options)'s option D, unbuilt — and,
  per the note above, short-lived if minted from the narrowed session.

---

## 7. Traps — read before writing code

**D1 (FIXED 2026-09-15, `f7b14308`). The codex derive wrote a key codex does not read.** It
emitted `api_key_env = <var name>`, and codex's `ModelProviderInfo` has no such field: its
credential-variable field is **`env_key`**, and the string `api_key_env` does not occur in the
codex 0.145.0 binary. Every custom provider yolo configured for codex therefore shipped with no
credential binding. The derive now writes `env_key`, and
`TestCodexDeriveUsesCodexCredentialField` (`internal/entrypoint/providerderive_test.go`) runs
the shipped derive and fails if `env_key` is missing or `api_key_env` returns. Kept here as the
worked example of the trap AGENTS.md names: the old test asserted the TOML yolo wrote, not
that codex reads it.

**D2 (live hazard). The profile env gate leaks across agents.**
`profileActive` in `internal/packload/packload.go` returns true if the profile is
active for a bin *this* pack installs **or for any bin the launch installs at all**. The
wide pass exists so a CLI-less pack's gated env is reachable, and it is correct for that.
But it means `-p codex=bedrock` in a jail that also selects packs/claude fires claude's
gated `CLAUDE_CODE_USE_BEDROCK=1` into the jail-wide environment, pointing claude at Bedrock
that nobody configured it for — and, since 2026-09-18, fires `packs/aws-auth`'s gated
`AWS_CONTAINER_CREDENTIALS_FULL_URI` jail-wide too, since that pack installs no bin at all. The
config-overlay twin does **not** have this problem: the profile gate in `packoverlay.Collect`
(`internal/packoverlay/packoverlay.go`) checks `profiles[key.Agent]`, agent-scoped. A shared
Bedrock profile name makes this routine rather than theoretical. **[OQ-BR4](#OQ-BR4).**

**D5 (live hazard). A second profile over `bedrock` silently loses every gated fact.** D2's
twin, pointing the other way: where D2 fires a gate for the wrong agent, D5 fails to fire it
for the right one. Every `profile:`-gated contribution matches the profile NAME literally, while
every derive matches the PROVIDER the name resolves to. A user profile `bedrock-sso` over
provider `bedrock` validates and selects, and then `-p bedrock-sso` delivers at most the
provider's region: no `CLAUDE_CODE_USE_BEDROCK`, no `claude/settings` overlay, no
credential pointer. Nothing reports it, by rule. **[OQ-BR8](#OQ-BR8)** has the evidence and
the options.

**D3. codex actively manages its `amazon-bedrock` entry.** The binary carries
*"configuration changed while clearing the managed Amazon Bedrock model provider; retrying
once"* and *"Amazon Bedrock login cannot select `X` because `Y` sets `model_provider` to
`Z`"*. Two consequences: writing fields beyond the permitted five is refused, not ignored;
and yolo pinning `model_provider` will block `codex login` for Bedrock while the profile is
active. The second is acceptable — a jail whose profile names the provider is not the place
to run an interactive login — but it must be in the briefing, not discovered.

⚠ **The derive's generic catalog row cannot be reused for it.** Today every row the codex
derive writes carries `name` and `wire_api` (and `env_key` when a variable is named) — `name`
because codex refuses a row whose name is empty (`TestCodexDeriveSetsModelProviderName`). None
of the three is in the permitted five, so the `amazon-bedrock` row needs its own shape.
Whether codex accepts one of them set to its built-in default value is unmeasured.

**D4. codex will not know the pinned model ids.** Its bundled catalog holds the mantle
spellings (`gpt-5.6-sol` → `openai.gpt-5.6-sol`); a `global.`-prefixed id is unknown to it,
and it says so: *"Unknown model … is used. This will use fallback model metadata."* The
request works; the context-window and pricing metadata are wrong. That is an accepted cost
of the [§5](#5-two-families-two-providers--because-the-family-and-the-ids-are-one-entry) pin, and the alternative — pinning mantle to keep codex's metadata — costs 23
regions and every other agent.

---

## 8. Behaviour this design fixes

Written for the implementer. Anything not here and not an OQ is theirs.

**Degenerate inputs.**
- Provider selected, `region` absent and no `AWS_REGION`/`AWS_DEFAULT_REGION` in the jail
  env → **refuse the launch**, naming the three places a region may come from. codex fails on
  this, and its own error text already enumerates them; a jail that boots and dies at first
  request is the worse outcome. ⚠ Not every native client fails: opencode and pi fall back to
  `us-east-1` silently, which is a region nobody chose rather than a refusal
  ([OQ-BR6](#OQ-BR6)).
- `models` map empty, or the alias the profile names is absent → emit the catalog row and
  **omit the model key**, exactly as the existing derives do. The agent resolves its own.
- Profile selected for an agent with no native path (copilot, agy; oh-omp until its support
  is read) → **nothing written**, no warning. Same as any unreachable provider today.
- Two providers both marked `service: "aws-bedrock"` → both are ordinary catalog rows; only
  the selected one gets a selection key. Not a collision — and it is the **shipped** case,
  since the two endpoint families are two entries.
- A provider whose `endpoint_family` an agent cannot serve (mantle for pi) → **no catalog
  row and no selection key**, through the same gate that drops any unreachable provider. It
  is not an error: the other agents in the same jail still get theirs.

**Failure paths.**
- Credential absent: the provider declares no `api_key_env_name` — the native arm's
  credential is whichever of the three the jail carries
  ([§6.5](#65-the-credential-three-are-supported)) — so the credential preflight demands
  nothing and the launch proceeds. Failure surfaces at first request, as an AWS auth error, from the agent. This is
  a deliberate asymmetry with the gateway arm, where `api_key_env_name` **is** declared and
  the preflight does demand it.
- A credential that expires inside a running jail. Only two of the three can: a Bedrock API
  key (credential 1: long-term keys carry an expiry, short-term ones last ≤12h) and the SSO
  session behind credential 3. An expired key surfaces as codex's *"Amazon Bedrock rejected
  the request because its AWS signature has expired… If `AWS_BEARER_TOKEN_BEDROCK` is set,
  update or unset it, then restart Codex."* **This design refreshes nothing**, and that is
  named rather than overlooked: refresh is [`sso-backed-bedrock.md`](sso-backed-bedrock.md)'s, whose service re-mints
  credential 3 and whose lapsed-session message names the `aws sso login` to run
  ([its §7](sso-backed-bedrock.md#7-refresh--what-happens-when-you-log-in-again)). Nothing
  refreshes a bearer.
- Region unsupported for the chosen family (mantle outside `us-east-*`): surfaces as
  codex's own *"Amazon Bedrock Mantle does not support region …"*. yolo carries no region
  allowlist of its own — a list that would rot within a quarter.

**Defaults, with units.** `endpoint_family` is a provider FIELD with no default — each
shipped entry states its own (`runtime`, `mantle`), and an entry omitting it is refused
rather than guessed, because a guessed family is a wrong model id (P1). `region:
"us-east-1"` (shipped on both entries; overridable per user). `aws_profile: null` — declared, no
default, meaning "use the chain". Model alias fallback: the profile's `model` option, else
`default` — the existing [`OQ-CS3`](../reference/providers.md#oq-cs3) ladder,
unchanged. No timeouts and no retries are introduced by this design.

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
1. `yolo -p bedrock-gpt -- codex` in a jail carrying one supported credential and a region
   completes one turn against GPT-5.6, and `codex doctor` reports provider
   `amazon-bedrock`, wire api `responses`, endpoint `bedrock-runtime.<region>`.
1b. Condition 1 holds for each of the three credentials in turn — a Bedrock API key, static
   keys, and `aws-auth`'s pointer — one per jail, never two
   ([§6.5](#65-the-credential-three-are-supported)).
2. The same flag on `opencode` and on `pi` completes one turn against the same model id.
2b. `yolo -p bedrock-gpt-mantle -- codex` completes one turn, and `codex doctor` reports the
   `bedrock-mantle` endpoint — the two families demonstrably side by side, which is the
   point of shipping both.
3. `yolo -p codex=bedrock-gpt -- codex` leaves `CLAUDE_CODE_USE_BEDROCK` **unset** in the
   jail env (D2 closed) — or, if [OQ-BR4](#OQ-BR4) rules otherwise, the briefing says why it is set.
4. Dropping the profile leaves each agent's interactively-chosen model untouched.
5. A user overriding `providers.bedrock-openai.models` to Anthropic ids reaches Claude on
   Bedrock through codex, with no yolo change — the "not just GPT" claim, demonstrated.

---

## 9. Non-goals

- **No credential lifecycle.** No key rotation, no short-term-key minting, no refresh
  daemon, no broker — not in this design. That half is
  [`sso-backed-bedrock.md`](sso-backed-bedrock.md)'s, and its service is built. This design
  reads no credential variable and names none; it works with whichever of the three
  supported credentials the jail carries ([§6.5](#65-the-credential-three-are-supported)).
- **No `~/.aws` mount.** This design assumes only environment variables and the
  container-credentials pointer. A grant of `~/.aws` is worse than out of scope: it
  **disables** the SSO arm while looking like it works, and yolo refuses it as a config error
  wherever a loophole serves container credentials
  ([`sso-backed-bedrock.md` §8](sso-backed-bedrock.md#8-behaviour-this-design-specifies)).
- **No model catalog in yolo.** The `models` map is three aliases a user can replace. yolo
  will not track Bedrock's model list, its region matrix, or its pricing.
- **No new agent.** copilot and agy have no native Bedrock path and this design does not
  invent one; copilot can take the gateway arm ([§6.4](#64-the-gateway-arm-ships-as-documentation)).
  oh-omp shipped after this design was drafted, and whether it has a native path is unread.
- **No Converse-for-everyone.** pi's `bedrock-converse-stream` is used because pi ships it;
  no canonical `wire_api` name is coined for Converse (**[OQ-BR5](#OQ-BR5)** if that changes).
- **Not a claude change.** packs/claude's four Bedrock contributions stay exactly as they
  are, except as D2's fix may narrow the env gate.

---

## 10. Alternatives considered

| Alternative | Verdict |
| :--- | :--- |
| **Gateway arm only** — one provider with a full `/openai/v1` base URL, no native bindings, no new pack | **Rejected as the primary path, adopted as the escape hatch ([§6.4](#64-the-gateway-arm-ships-as-documentation)).** It works and costs almost nothing, but it discards SigV4, the credential chain, codex's `auth.json` Bedrock mode and opencode's region/profile options, and it makes yolo the owner of a URL it must keep current. Since the 2026-09-24 credential ruling it has a sharper cost: it carries only an API key, so it cannot use the SSO credential the maintainer called the primary one ([§6.5](#65-the-credential-three-are-supported)). It is the right answer for an agent with no native path. |
| **Ship one family only** — pick runtime, document mantle as a manual config | **Rejected on the maintainer's ask.** Both are wanted for comparison, and the second entry costs one JSON object plus one line in each derive. Runtime remains the *recommended* one, in the README. |
| **One provider, `endpoint_family` as a profile OPTION** | **Rejected — it cannot work.** Options are a flat name→value map; `models` is a provider field the option layer never reaches. The option would move the endpoint and leave the ids, which is P1's failure with a knob attached ([§5](#5-two-families-two-providers--because-the-family-and-the-ids-are-one-entry)). |
| **One `bedrock` provider for all four agents** | **Rejected.** Claude wants Anthropic ids and codex wants GPT ids through the same `default` alias. Separate entries is what the schema already calls the ordinary case. |
| **Move `bedrock` out of packs/claude into the new pack** | **Rejected as unnecessary churn.** Sole ownership means the name can only live in one place, and it already lives somewhere that works. Moving it renames nothing a user types but risks a collision for no gain. |
| **Match the provider by NAME in each derive** (`if name == "bedrock-openai"`) | **Rejected** — [`stringly-typed-references-principle.md`](../reference/stringly-typed-references-principle.md) exists for this, and it would silently break the moment a user declares their own Bedrock provider under another name. |
| **A `bedrock` key in the open `endpoints` map, with no URL** | **Rejected — unrepresentable.** `validateProviderEndpoints` in `internal/packdecl/contributes.go` refuses an endpoint with no `base_url`. |
| **Coin `bedrock-converse` as a fourth canonical `wire_api`** | **Deferred ([OQ-BR5](#OQ-BR5)).** Only pi consumes it, and pi is reachable through the native marker without it. Coin it if a second Converse consumer appears. |

---

## 11. Risks

| Risk | Mitigation |
| :--- | :--- |
| **R1.** codex tightens its built-in-provider override list and `base_url` stops being permitted — the runtime entry loses its way in. | The permitted list is a string in the binary and is re-checkable in seconds ([§14](#14-evidence-and-how-to-re-check-it)). Pin the codex version in the evidence table and re-verify on upgrade; the fallback is the gateway arm under a non-reserved id. |
| **R2.** D4's fallback metadata makes codex mis-estimate the context window and compact too early or too late against a 1M-token model. | Measurable in one session. If it bites, the escape is already shipped: `-p bedrock-gpt-mantle` is the family whose ids codex's catalog knows, and it moves both halves at once. |
| **R3.** The three agents' shared `amazon-bedrock` id drifts apart (one renames it). | Each derive already owns its agent's spelling; a rename is one line in one derive, with provenance. |
| **R4.** No end-to-end request is made during implementation, and this ships on schema reading alone — the standing weakness of every provider integration in this repo. | The done-conditions in [§8](#8-behaviour-this-design-fixes) are all live turns. `codex doctor` settles codex without burning a turn; the other two need one real request each. |
| **R5.** Bedrock IAM needs `bedrock:InvokeModel` on the account's **default project** (`arn:aws:bedrock:{region}:{account}:project/default`) in addition to the inference profile — a policy the existing invoke-only `matt-bedrock` IAM user may not carry. | Test with the real account before declaring the arm done; the failure is an AccessDenied naming the project ARN, which is self-diagnosing. |

---

## 12. What I would build, in order

1. ~~**Fix D1**~~ — **done 2026-09-15** (`f7b14308`), with its regression test
   ([§7](#7-traps--read-before-writing-code)). The gateway arm no longer waits on anything.
2. **Rule [OQ-BR2](#OQ-BR2)** (the marker) and add the field to `packdecl` with its tolerance
   behaviour. Nothing renders yet; the schema is the thing three derives will key on.
3. **The `bedrock` pack** — both providers, both profiles, README (including which family
   is recommended and why). Selecting either changes nothing observable until step 4, which
   makes it a safe landing.
4. **The codex derive binding** — the pin, `aws.region`, the selection. codex first because
   it is the motivating case and because `codex doctor` verifies it cheaply.
5. **opencode and pi bindings** — mechanically similar, each with its own provenance
   comment recording the version its spelling was read from.
6. **Close D2 and D5 together** (per [OQ-BR4](#OQ-BR4) and [OQ-BR8](#OQ-BR8), ruled as one),
   each with a test that fails when the call site is deleted.
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

   > [!WARNING]
   > **⚠ Premise changed since the leaning was written (re-read 2026-09-24).** Two facts it
   > did not account for:
   >
   > - **`service` already names something.** `kind: "service"` is a contribution kind — the
   >   in-jail daemon `packs/wire-bridge` ships. A provider FIELD called `service` would give
   >   one word two meanings in one manifest vocabulary.
   > - **The field lands in two schemas.** A user-declared provider's key set is CLOSED
   >   (`knownProviderKeys` in `internal/config/config.go` refuses anything else), so a user's
   >   own Bedrock provider could not carry the marker until it is added there as well as to
   >   `internal/packdecl`.
   >
   > Neither decides the question; both are costs of the leaning's spelling.

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
   [§9](#9-non-goals) says yolo tracks no catalog.

   > [!WARNING]
   > **⚠ Premise changed (read 2026-09-24): the proposed aliases may be stale before they
   > ship.** pi-ai 0.87.1's bundled Bedrock catalog lists `openai.gpt-6-astra` — bare,
   > `us.` and `global.` — beside the GPT-5.6 ids, and yolo's own codex-subscription defaults
   > moved to GPT-6 in September (`2f11de95`, `7ad8358c`). Whether Bedrock serves GPT-6 was not
   > checked against AWS. It sharpens the stakes this question already names — a shipped model
   > list is one yolo has to keep from rotting — without choosing between them.

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

   > [!WARNING]
   > **It stopped being theoretical on 2026-09-18, and the stakes changed from a flag to a
   > credential.** `packs/aws-auth` (`9fc4879d`) ships
   > `{"kind":"env","profile":"bedrock","vars":{"AWS_CONTAINER_CREDENTIALS_FULL_URI":…}}`, and
   > `packs/claude` ships a profile named `bedrock`. That pack installs no bin, so it takes the
   > SECOND pass of the rule stated on the `Profile` field in
   > [`contributes.go`](../../internal/packdecl/contributes.go) — *"the profile active for a
   > bin the pack installs, else active for ANY bin … the second pass is what makes a CLI-less
   > pack's gated env reachable"*. So selecting `bedrock` for
   > claude points **every** AWS SDK in the jail at the credential adapter, whichever agent the
   > user chose it for.
   >
   > **It is also the first case where both horns bite.** Narrowing the gate to the pack's own
   > bins — this question's first option — would not merely break "the CLI-less pack case" in
   > the abstract; it would break `aws-auth` specifically, since CLI-less is exactly what that
   > pack is. So the first option is now disqualified by an instance rather than by an argument,
   > and the choice is between scoping by agent and accepting a credential-shaped leak.
   >
   > **⚠ And the 2026-09-24 credential ruling bounds what fixing it buys.** Of the three
   > supported credentials only the SSO one reaches the jail through a pack; the other two
   > arrive through `env_sources`, which is profile-blind and reaches every agent in the jail
   > whatever this gate does ([`provider-credential-scope.md`](provider-credential-scope.md)).
   > So scoping the gate narrows `aws-auth`'s pointer and claude's flag, and no other
   > credential. That is a fact about the stakes, not a vote for either option.

   _Leaning:_ Scope it, following the precedent of `packoverlay.Collect`'s profile gate — the wide pass was a
   reachability fix for CLI-less packs, and "reachable" should not have meant "global". The
   2026-09-18 instance strengthens this rather than changing it: `config-overlay` already keys
   its gate on the target surface's owning agent, and the reason `env` cannot is stated in the
   same comment — env has no surface to name an agent — which is the binding that has to be
   invented. It remains a change to a shipped rule with its own OQ history, so it is the
   maintainer's call whether it rides this work or gets its own.

   <!-- vantage: oq id=OQ-BR4 leaning="Scope the env gate by agent, following the precedent of packoverlay.Collect's profile gate — the wide pass was a reachability fix for CLI-less packs, and 'reachable' should not have meant 'global'. But it changes a shipped rule with its own OQ history, so it is your call whether it rides this work or gets its own." -->

   **Answer:**
   > _(empty — fill in when decided)_

5. 💬 **OQ-BR8: What does a contribution's gate key on — the profile NAME, or the provider it
   selects?** Depends on [OQ-BR2](#OQ-BR2), whose marker is what a provider-keyed fact would
   match; rule it with [OQ-BR4](#OQ-BR4), which is the same function asked from the other
   side ([D5](#7-traps--read-before-writing-code) and D2). Stakes: whether a user's second intent over a shipped provider is a
   supported case or a silent trap, and whether the credential adapter's port stays written in
   three places.

   **The split, measured at `7ad8358c` and re-read at `f491d192`.** Gates and derives answer "is Bedrock selected?"
   through different keys:

   | Consumer | Keys on | Where |
   | :--- | :--- | :--- |
   | `env` gate | the literal NAME: `EnvFold` → `profileActive` → `installsActiveBin`, `profiles[bin] == name` | [`packload.go`](../../internal/packload/packload.go), in `installsActiveBin` |
   | `config-overlay` gate | the literal NAME: `profiles[key.Agent] != ov.Profile` | [`packoverlay.go`](../../internal/packoverlay/packoverlay.go), in `Collect` |
   | surface derive | the resolved PROVIDER: `packload.ProviderFor(resolved, profiles[s.Agent])` | [`packsurfaces.go`](../../internal/entrypoint/packsurfaces.go), in `surfaceSelectionFor` |
   | env derive | the resolved PROVIDER: `selected := ProviderFor(cfg.resolved, profile)` | [`deriveenv.go`](../../internal/packload/deriveenv.go) |

   MEASURED: neither gate file calls `ProviderFor`. An inactive gate is a clean skip by rule,
   with no error or report (the doc comment on `Collect` in
   [`packoverlay.go`](../../internal/packoverlay/packoverlay.go), and the `Profile` field's
   comment in [`contributes.go`](../../internal/packdecl/contributes.go): *"It is a SKIP, not
   an error"*).

   **What that does to a second profile.** A user profile `bedrock-sso` over provider
   `bedrock` validates and selects. `-p bedrock-sso` then exports `AWS_REGION` only, where the
   provider declares a region. That comes from claude's derive, which keys on `ctx.selected_provider`
   (the `yolo.env` producer in [`packs/claude/derive.lua`](../../packs/claude/derive.lua)). Three
   things are lost, silently:

   - `CLAUDE_CODE_USE_BEDROCK` from claude's `bedrock`-gated `env` ([`packs/claude/pack.json`](../../packs/claude/pack.json))
   - the `bedrock`-gated `claude/settings` overlay, in the same file
   - aws-auth's `AWS_CONTAINER_CREDENTIALS_FULL_URI` pointer, its one `env` contribution ([`packs/aws-auth/pack.json`](../../packs/aws-auth/pack.json))

   MEASURED by a triage on 2026-09-23; re-derived here by reading the gates at HEAD, not
   re-run. So claude may run first-party on whatever login it holds. INFERRED: no request was
   made.

   **Not Bedrock-specific.** The same shape ships twice more, MEASURED:

   - [`packs/pi/pack.json`](../../packs/pi/pack.json) gates the codex prelaunch env on the name `codex`
   - [`packs/llamacpp/pack.json`](../../packs/llamacpp/pack.json) gates `CLAUDE_CODE_ATTRIBUTION_HEADER=0` on the name `llamacpp`

   **The port is written three times.** The adapter's `127.0.0.1:1461` appears in
   [`handler.go`](../../internal/awscredadapter/handler.go) (`DefaultListen`), in the
   loophole's
   [`manifest.jsonc`](../../packs/aws-auth/loopholes/aws-auth/manifest.jsonc) (the jail
   daemon's `--listen` argument) and in
   [`packs/aws-auth/pack.json`](../../packs/aws-auth/pack.json) (the pointer's value). MEASURED:
   they are pinned against each other by `TestAWSAuthPointerAndAdapterAgreeOnThePort` in
   [`aws_auth_pointer_test.go`](../../packs/aws_auth_pointer_test.go), which reads the embedded
   copies of the two manifests and nothing else. INFERRED: a user pack's copy is checked by
   nothing, and the workaround below writes exactly that copy.

   **It breaks a case the design called supported.** The deleted
   `provider-catalog-and-selection.md`, under *What a profile is*, names *"a user adding a second intent over the
   same provider"* as the case selection resolution exists for (`git show
   4e4ca2d1^:docs/design/provider-catalog-and-selection.md`, line 398). The SELECTION half was
   fixed there: `ProviderFor` reads the resolved table, user profiles included. The GATE half
   never moved.

   **Prior rulings, each checked for whether it already answers this:**

   | Ruling | What it settled | Does it answer this? |
   | :--- | :--- | :--- |
   | [`OQ-PT8`](../reference/providers.md#oq-pt8), 2026-09-01 | Moved profile bodies onto name-keyed `profile:` contributions | **No.** It settled what a profile carries. Name versus provider was a consequence, never a choice |
   | [`OQ-CS9`](../reference/providers.md#oq-cs9) | Profiles point at a provider; no `extends` | **Yes, for one option:** it rules out `bedrock-sso extends bedrock` |
   | [`OQ-PT3`](../reference/providers.md#oq-pt3) | A provider's fact is composed from the provider, not restated in a gated overlay — zai's URL literal left the overlay | **It is the precedent** the leaning follows |

   **Options:**

   | Option | Verdict |
   | :--- | :--- |
   | **Key provider facts on the provider**, mostly in the agent's derive keyed on `ctx.selected_provider`, then on [OQ-BR2](#OQ-BR2)'s marker once it rules | **Leaning** |
   | Let a profile `extends` another, so `bedrock-sso` inherits `bedrock`'s gates | **Rejected**: it reopens [`OQ-CS9`](../reference/providers.md#oq-cs9) |
   | Make the gate match the selected provider's NAME instead | **Weaker than the leaning.** The fact stays in a manifest, keyed on one provider name, so a second provider for a variant loses it again |
   | Warn at launch when a gate's name differs from the selected profile over the same provider | **Rejected as the fix**: it reports the trap instead of removing it. The one piece kept is a `yolo check` note for a USER pack whose name gate differs from the selected profile over the same provider |

   **The leaning, concretely:**

   - claude's `CLAUDE_CODE_USE_BEDROCK` moves into claude's derive, in both env and settings.
   - llamacpp's attribution header becomes a provider option.
   - pi's codex prelaunch env moves into a pi derive.
   - aws-auth's adapter address is composed into the provider row, as codex's Responses
     address already is (the `endpoints` of the `openai-codex` provider in [`packs/openai-auth/pack.json`](../../packs/openai-auth/pack.json)).
     That removes the duplicated `1461` and makes the pointer per-agent rather than jail-wide,
     which is also [OQ-BR4](#OQ-BR4)'s leak. ⚠ Provider names are sole-owned across packs
     ([§3](#3-what-yolo-has-today)), so aws-auth cannot add a field to claude's `bedrock` row.
     It ships its own provider, which is the next bullet.
   - Two variants of one service are modelled as two PROVIDERS, not two profiles over one.

   **Working today, as a workaround.** Declare a second provider in user `providers` (for
   example `bedrock-sso`, with its own `region`) and a profile over it. Then add a local pack
   (`~/.config/yolo-jail/local`) whose `env` and `config-overlay` contributions are gated on
   the new name. They restate `CLAUDE_CODE_USE_BEDROCK=1` in both places, plus
   `AWS_CONTAINER_CREDENTIALS_FULL_URI=http://127.0.0.1:1461/credentials`. That is the fourth,
   unchecked copy of the port. MEASURED working by the 2026-09-23 triage.

   _Leaning:_ Key provider facts on the provider. Put them in each agent's derive, keyed on
   `ctx.selected_provider` today and on [OQ-BR2](#OQ-BR2)'s marker once it rules. Compose
   aws-auth's adapter address into its own provider row, and model variants as providers. Rule
   it with [OQ-BR4](#OQ-BR4). Keep only a `yolo check` note for a user pack whose name gate
   disagrees with the selected profile.

   <!-- vantage: oq id=OQ-BR8 leaning="Key provider facts on the PROVIDER, mostly in the agent's derive keyed on ctx.selected_provider and on OQ-BR2's service marker once it rules: claude's CLAUDE_CODE_USE_BEDROCK in env and settings, llamacpp's header as a provider option, pi's codex prelaunch env via a pi derive. Compose aws-auth's adapter address into its own provider row, removing the duplicated 1461, and model two variants as two providers. Rule with OQ-BR4. Not extends (reopens OQ-CS9); not a warning alone (reports the trap instead of removing it) — keep only a yolo check note for a user pack whose name gate differs from the selected profile over the same provider." -->

   **Answer:**
   > _(empty — fill in when decided)_

6. 💬 **OQ-BR5: Is pi bound through `bedrock-converse-stream`, or through the gateway?** pi
   ships a native Converse client, but reaching it means the derive emits an `api` value
   with no canonical `wire_api` behind it — a per-agent fact with no cross-agent vocabulary.
   The alternative is pointing pi at `/openai/v1` with `openai-responses`, which stays
   entirely inside the existing three-name enum. Stakes: whether this design coins a fourth
   canonical protocol name, and whether pi gets SigV4 or needs a bearer token.

   > [!WARNING]
   > **⚠ Premise changed (read 2026-09-24).** Two facts sharpen the stakes:
   >
   > - **Through the gateway, pi needs a bearer — but because of yolo, not AWS.** AWS accepts
   >   SigV4 on `/openai/v1`; yolo's gateway arm carries only an API key
   >   ([§6.5](#65-the-credential-three-are-supported)). So the gateway option leaves an SSO
   >   user's pi needing a short-term key minted from the session — [`sso-backed-bedrock.md`](sso-backed-bedrock.md#4-five-options)'s
   >   option D, unbuilt — where the native option takes credential 3 as it is.
   > - **"Native" now has a built-in to bind to.** pi-ai 0.87.1 ships an `amazon-bedrock`
   >   provider on `bedrock-converse-stream` ([§4](#4-what-each-agent-can-actually-do)); in
   >   0.82.1, when this question was written, there was only the API id.

   _Leaning:_ Native Converse, no new canonical name — the `service` marker already says
   "this is Bedrock", and what pi does with that is pi's derive's business ([OQ-CS8](../reference/providers.md#oq-cs8)). Coining
   `bedrock-converse` for one consumer would be the enum growing to describe a client, not a
   protocol.

   <!-- vantage: oq id=OQ-BR5 leaning="Native Converse for pi, no new canonical `wire_api` name — the `service` marker already says 'this is Bedrock', and what pi does with that is pi's derive's business (OQ-CS8). Coining `bedrock-converse` for one consumer would be the enum growing to describe a client, not a protocol." -->

   **Answer:**
   > _(empty — fill in when decided)_

7. 💬 **OQ-BR6: Should the launch refuse when no region is resolvable?** [§8](#8-behaviour-this-design-fixes) proposes
   refusing, on the grounds that every native client fails without one and a boot that dies
   at first request is worse. The counter: yolo cannot see `AWS_REGION` arriving from an
   ambient chain the agent can read, so a refusal could be wrong — the same
   "unproven fact emits nothing" discipline `hostloopback.go` follows. Stakes: a false
   refusal blocks a working setup; a missing one costs a confusing first-request failure.

   > [!WARNING]
   > **⚠ Premise changed (read 2026-09-24): not every native client fails without a region.**
   > codex refuses and names the three sources. opencode 1.18.32 falls back to `us-east-1`
   > silently, and pi-ai 0.87.1, given no region, pins the model's own base URL — `us-east-1`
   > for every model in its bundled catalog (read from its source; not run). So for those two
   > a missing region is not a first-request failure but a request to a region nobody chose,
   > which may well succeed. The refusal's case rests on codex alone, or on that outcome being
   > worth refusing.

   _Leaning:_ Refuse only when the provider declares no `region` **and** no `AWS_REGION` /
   `AWS_DEFAULT_REGION` is in the composed jail environment — both of which yolo can see at
   launch. Anything beyond that (an `~/.aws/config` region) is unproven, and unproven emits
   nothing.

   <!-- vantage: oq id=OQ-BR6 leaning="Refuse only when the provider declares no `region` AND no `AWS_REGION` / `AWS_DEFAULT_REGION` is in the composed jail environment — both of which yolo can see at launch. Anything beyond that (an `~/.aws/config` region) is unproven, and unproven emits nothing." -->

   **Answer:**
   > _(empty — fill in when decided)_

8. 💬 **OQ-BR7: Is `endpoint_family` its own provider field, or does it fall out of the
   marker?** [§6.1](#61-three-providers-because-a-models-map-cannot-hold-two-model-families) proposes a field beside `region` holding `runtime` | `mantle`, which each
   derive reads to decide whether to override codex's `base_url` and whether it can serve
   the entry at all. The alternative is folding it into `service` (`aws-bedrock-runtime` vs
   `aws-bedrock-mantle`), which adds no field but makes the marker carry two facts. Stakes:
   one schema field, and whether "is this Bedrock?" and "which Bedrock?" are one question or
   two — pi has to answer them separately, since it serves one family and not the other.

   <!-- vantage: oq id=OQ-BR7 leaning="Its own field. pi answers 'is this Bedrock' and 'which family' separately — it serves runtime and cannot serve mantle — so a marker carrying both facts would have to be destructured by every consumer anyway. A field beside region also keeps it out of a profile's reach, which is what makes the family and its model ids inseparable." -->

   _Leaning:_ Its own field. pi answers "is this Bedrock?" and "which family?" separately —
   it serves runtime and cannot serve mantle — so a marker carrying both facts would be
   destructured by every consumer anyway. A field beside `region` also keeps it out of a
   profile's reach, which is exactly what makes the family and its ids inseparable ([§5](#5-two-families-two-providers--because-the-family-and-the-ids-are-one-entry)).

   **Answer:**
   > _(empty — fill in when decided)_

---

## 14. Evidence, and how to re-check it

Everything in [§2](#2-what-bedrock-is-now--measured-2026-09-04), [§4](#4-what-each-agent-can-actually-do) and [§6.5](#65-the-credential-three-are-supported) is a fact about a third party, so it carries its source and its
date. Re-run these rather than trusting the table. Repo claims are verified at `f491d192`
(2026-09-24) and cited by symbol rather than by line, because a line number is the first thing
to go stale.

**codex** — all codex claims are from the shipped binary of **codex-cli 0.145.0**
(`@openai/codex-linux-x64`, `vendor/x86_64-unknown-linux-musl/bin/codex`), read
2026-09-04 with `strings` and never executed. The same version is installed on 2026-09-24,
and the override list, the region-sources message and the mantle region allowlist were re-read
then:

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

**pi** — pi-ai **0.87.1**, the copy installed in this jail under
`@earendil-works/pi-coding-agent` (read, never run), 2026-09-24:

- `dist/providers/amazon-bedrock.js` defines the built-in provider `amazon-bedrock` on
  `bedrockConverseStreamApi()`, and its `resolve` order: a stored key, then
  `AWS_BEARER_TOKEN_BEDROCK`, then `AWS_PROFILE`, then `AWS_ACCESS_KEY_ID` +
  `AWS_SECRET_ACCESS_KEY`, then the two container-credentials variables, then
  `AWS_WEB_IDENTITY_TOKEN_FILE`.
- `dist/compat.js` still lists `bedrock-converse-stream` among its built-in APIs.
- `dist/api/bedrock-converse-stream.js` resolves the bearer from `options.bearerToken` or
  `AWS_BEARER_TOKEN_BEDROCK`, honours `AWS_BEDROCK_SKIP_AUTH=1`, and pins the model's catalog
  base URL when no region or `AWS_PROFILE` is configured.
- `dist/providers/data/amazon-bedrock.json` lists `openai.gpt-5.6-*` (bare, `us.`, `global.`)
  and `openai.gpt-6-astra` (the same three spellings), every one against a
  `bedrock-runtime.us-east-1` base URL.

The 2026-09-04 reading was of 0.82.1 (`dist/compat.js:108-119`,
`dist/api/bedrock-converse-stream.js:1-60`), which had the API id and no provider. pi changes
fast — `local-model-endpoints.md` measured its compat block changing between 0.82.1 and 0.84.4
within one doc's lifetime — so **re-read at whatever version ships before writing pi's derive**.

**opencode** — opencode **1.18.32** (`opencode-ai`, the `opencode-linux-x64-baseline` binary),
read statically on 2026-09-22 ([§6.3](#63-what-each-derive-emits)'s measurement) and again on
2026-09-24. The 2026-09-24 reading found the precedence comment *"1. Bearer token
(`AWS_BEARER_TOKEN_BEDROCK` or /connect)"*, the provider using the bearer when set and the
AWS SDK credential chain otherwise, and a region resolved as `options.region`, else
`AWS_REGION`, else `"us-east-1"`. The 2026-09-04 row was vendor documentation only.

**claude** — claude 2.1.282 (`~/.local/share/claude/versions/2.1.282`), 2026-09-24:
`AWS_BEARER_TOKEN_BEDROCK` occurs in the binary (12 matches of `grep -c -a`). Presence only;
its precedence against the chain was not read.

**AWS: the endpoints and the model** — the GPT-5.6 Sol model card and the endpoints page, both
read 2026-09-04: endpoint URLs, the runtime/mantle model-id split, the region matrices, the API
support tables, the `project/default` IAM requirement, and the Global CRIS discount. ⚠ **AWS's
pages disagree on the mantle path**: the model card says *"On `bedrock-mantle`, both APIs use
the `/openai/v1` base path, not `/v1`"*, while the Chat Completions and Responses API pages give
`https://bedrock-mantle.{region}.api.aws/v1` (read 2026-09-24).

**AWS: authentication** — all read 2026-09-24:

| Claim | Source |
| :--- | :--- |
| SigV4 and API keys both supported on `bedrock-runtime` and `bedrock-mantle`; *"Whether you use SigV4 or a Bedrock API key, the associated IAM principal must have permission for the required action"* | Bedrock endpoints page |
| *"The `bedrock-runtime` endpoint supports AWS SigV4 authentication and Amazon Bedrock API key authentication"*; a SigV4 `curl` example with `--aws-sigv4 "aws:amz:us-east-1:bedrock"` against `/openai/v1/chat/completions`; mantle *"supports Amazon Bedrock API key authentication, AWS credentials, and the OpenAI SDK"* | Chat Completions page |
| *"Amazon Bedrock API key (required for OpenAI SDK)"*, *"AWS credentials (supported for HTTP requests)"* | Responses API page |
| The mantle SigV4 signing service is `bedrock-mantle` (no AWS page read names it) | OpenAI's Go SDK `bedrock` package docs, 2026-09-23 |
| Short-term key: *"Recommended for production use"*; long-term key: *"Recommended only for exploration"*; no deprecation notice | Bedrock API keys page |
| Short-term key *"Valid for the shorter of … 12 hours; The duration of the session generated by the IAM principal used to generate the key"*, single-Region | How Bedrock API keys work; Generate API keys |
| `bedrock:CallWithBearerToken` and `bedrock-mantle:CallWithBearerToken` control API-key use, one per endpoint; *"To fully prevent all API key-based access, you must deny both actions"* | API keys permissions page |
| *"Our recommendation is to use temporary security credentials provided by AWS Security Token Service (AWS STS) service whenever possible as a preferred authentication method"*; keys *"when your use case blocks the use of temporary AWS STS credentials"*; SCPs to block `bedrock:CallWithBearerToken` and `bedrock-mantle:CallWithBearerToken`. Posted 2025-10-17, updated 2026-07-01 | AWS Security Blog, *Securing Amazon Bedrock API keys* |
| *"the actual token validity period will always be the minimum of the requested expiration time and the AWS credentials' expiry time"*, maximum 12 hours; AssumeRole examples; *"Generate tokens with any credential provider"* (Java) | the `aws-bedrock-token-generator` READMEs (Python, JavaScript, Java) |

The long-term key mechanics (one IAM user per key, two per user for rotation, an expiry from
one day to never) are from the IAM user guide, read 2026-09-04. No request was made to AWS for
any row above.

**Sources:**
[Bedrock endpoints](https://docs.aws.amazon.com/bedrock/latest/userguide/endpoints.html) ·
[GPT-5.6 Sol model card](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-openai-gpt-56-sol.html) ·
[Cross-Region inference for GPT-5.6](https://aws.amazon.com/blogs/machine-learning/introducing-cross-region-inference-for-openai-gpt-5-6-models-on-amazon-bedrock/) ·
[Bedrock API keys](https://docs.aws.amazon.com/bedrock/latest/userguide/api-keys.html) ·
[How Bedrock API keys work](https://docs.aws.amazon.com/bedrock/latest/userguide/api-keys-how.html) ·
[Generate Bedrock API keys](https://docs.aws.amazon.com/bedrock/latest/userguide/api-keys-generate.html) ·
[Bedrock API key permissions](https://docs.aws.amazon.com/bedrock/latest/userguide/api-keys-permissions.html) ·
[Chat Completions on Bedrock](https://docs.aws.amazon.com/bedrock/latest/userguide/inference-chat-completions.html) ·
[Responses API on Bedrock](https://docs.aws.amazon.com/bedrock/latest/userguide/inference-responses-api.html) ·
[Securing Amazon Bedrock API keys](https://aws.amazon.com/blogs/security/securing-amazon-bedrock-api-keys-best-practices-for-implementation-and-management/) ·
[aws-bedrock-token-generator (Python)](https://github.com/aws/aws-bedrock-token-generator-python) ·
[aws-bedrock-token-generator (JavaScript)](https://github.com/aws/aws-bedrock-token-generator-js) ·
[aws-bedrock-token-generator (Java)](https://github.com/aws/aws-bedrock-token-generator-java) ·
[openai-go `bedrock` package](https://pkg.go.dev/github.com/openai/openai-go/v3/bedrock) ·
[Bedrock API keys (IAM)](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_credentials_bedrock.html) ·
[AWS SDK credential providers](https://docs.aws.amazon.com/sdkref/latest/guide/standardized-credentials.html) ·
[Codex with Amazon Bedrock](https://learn.chatgpt.com/docs/amazon-bedrock) ·
[codex PR #18744 — built-in Bedrock provider](https://github.com/openai/codex/pull/18744) ·
[opencode providers](https://opencode.ai/docs/providers/)
