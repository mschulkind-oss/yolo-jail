# `bedrock` — Amazon Bedrock, as one provider every agent reads

This pack declares Amazon Bedrock's `bedrock-runtime` endpoint as one provider, `bedrock`, and
two profiles over it, `bedrock` and `bedrock-bridge`. It installs no program. Every agent pack
that can put its agent on Bedrock `needs` it, so selecting that agent brings it in:

```jsonc
// ~/.config/yolo-jail/config.jsonc
{ "packs": ["codex"] }
```

`yolo -p bedrock -- codex` then configures codex to reach Bedrock through its own Bedrock
client, with nothing else listed but a region. No request to Bedrock has been measured from any
agent yet: what each client sends was read from its shipped code, never run. The launch prints `+ bedrock (needed by codex)` and
`+ aws-auth (needed by bedrock)`.

Design: [`bedrock-plumbing.md`](../../docs/design/bedrock-plumbing.md) ([OQ-BR9](../../docs/design/bedrock-plumbing.md#OQ-BR9),
[OQ-BR1](../../docs/design/bedrock-plumbing.md#OQ-BR1)); behavior:
[`providers.md`](../../docs/reference/providers.md).

## What the provider declares

- **`"platform": "aws-bedrock"`**, what service it is. Each agent's own Bedrock binding,
  claude's Bedrock switch among them, `aws-auth`'s credential pointer and the region check all
  key on it, never on the provider's name, so a provider of your own that declares the same
  platform gets the same behavior.
- **No endpoint and no region.** Each agent's own Bedrock client composes its URL from a region,
  and yolo ships none: set one as `"providers": {"bedrock": {"region": "us-east-1"}}` in your
  user config, as `AWS_REGION` in an `env_sources` entry, or as the `region` of your AWS profile
  in `~/.aws/config`. A launch that finds none of the three is refused and says where it looked.
- **`region_env_name`**: `AWS_REGION` and `AWS_DEFAULT_REGION`, the variables that count as a
  region for every provider of this platform.
- **`region_file`**: `~/.aws/config` (or the file `AWS_CONFIG_FILE` names in the shell you
  launch yolo from), the `region` of `[profile NAME]`; for the `default` profile that of
  `[profile default]`, else of `[default]`, the order claude's and codex's AWS SDKs read them in.
  When an agent gets no
  region from the provider or the environment, yolo reads it there for the profile the agent's
  credential comes from, and hands the agent that region as `AWS_REGION`, in a jail and at
  `yolo host` alike. The profile is the one `aws-auth` serves when it serves the agent (its
  `profile` setting), else the `AWS_PROFILE` the agent receives, else `default`. The launch
  prints one line naming the region, the file and the profile:

  ```
  Region: AWS_REGION=eu-west-1 for codex on provider "bedrock", read from ~/.aws/config [profile my-sso] (profile "my-sso", the one loopholes.aws-auth.settings.profile names for the credential): the provider sets no region, and no region variable reaches it
  ```
- **The six AWS credential variables** under `api_key_env_name`: `AWS_BEARER_TOKEN_BEDROCK`,
  `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`, `AWS_PROFILE` and
  `AWS_CONTAINER_CREDENTIALS_FULL_URI`. yolo delivers each only to an agent whose profile selects
  a Bedrock provider. None is demanded: an agent's own client takes whichever credential it
  finds.

## The models it ships

One provider holds every maker's models. Each entry names its maker as `vendor`, and yolo never
guesses the maker from the id. Each agent picks among the entries whose maker its own Bedrock
client is known to serve (the table below), and starts on the first of those in the order below,
unless you name a model. pi and opencode also list those entries in their model menus; claude's
and codex's menus are not shaped yet
([OQ-BR13](../../docs/design/model-lists-and-pickers.md#OQ-BR13)), so for them the list only
decides which model yolo starts them on:

| Order | Model id | Maker | Name |
| :--- | :--- | :--- | :--- |
| 1 | `global.anthropic.claude-opus-5-5` | `anthropic` | Claude Opus 5.5 (Global) |
| 2 | `us.openai.gpt-6.1-sol` | `openai` | GPT-6.1 Sol (US) |
| 3 | `global.openai.gpt-6-astra` | `openai` | GPT-6 Astra (Global) |

Each entry also carries the context window, the output cap and the input kinds its AWS page
states (`context_window`, `max_tokens`, `input`), and Claude Opus 5.5 its reasoning support,
which its page states.

- **These are `bedrock-runtime` ids.** Runtime takes a cross-Region inference profile id,
  `global.` or a geography such as `us.`, where the bare id is the other endpoint family's
  spelling. `global.` routes worldwide with no data-residency constraint.
- **GPT-6.1 Sol is `us.` only, and codex starts on it in every Region.** AWS offers it on
  runtime through the US inference profile alone, with no global or in-Region id (read
  2026-09-29, its launch day), so a Region outside that profile's source Regions cannot call it
  yet. yolo does not choose a model by Region: the maintainer's ruling is GPT-6.1 Sol
  everywhere, on the expectation that AWS offers it more widely soon
  ([BR-D19](../../docs/design/bedrock-plumbing.md#BR-D19)). Outside the US, name another model
  yourself ([below](#choosing-another-model)); GPT-6 Astra is `global.`.
- **GPT-6 Sol is not shipped.** It shipped beside GPT-6.1 Sol, as the Sol that Bedrock offers
  outside the US, until the same ruling treated GPT-6.1 Sol as available everywhere. You can
  still name its id, `global.openai.gpt-6-sol`, in a profile, and yolo passes it through as
  written.

### Which agent starts where

| Agent | Makers it takes on Bedrock | Starts on, with no model named |
| :--- | :--- | :--- |
| claude | Anthropic models: its Bedrock client drives the Messages API, which serves Claude only | its own Bedrock default. yolo pins a model only when the profile names one, or your config names a `default` alias Claude can call |
| codex | OpenAI's: its built-in `amazon-bedrock-runtime` client drives the Responses API, which AWS serves for them and not for Anthropic's. The filter is by declared maker, not by what codex could call: another maker's model whose card lists Responses is still skipped until a turn measures it | GPT-6.1 Sol (US), in every Region |
| opencode | every entry: its built-in `amazon-bedrock` provider sends a cross-Region id to runtime's Converse API, which serves each of them | Claude Opus 5.5 (Global) |
| pi | every entry: its built-in `amazon-bedrock` provider drives Converse | Claude Opus 5.5 (Global) |

copilot and oh-omp have no Bedrock client of their own, and reach Bedrock only through yolo's
wire bridge, under `-p bedrock-bridge` (below); `-p bedrock` does not configure them, and a jail
of them alone lists this pack in `packs` to have either profile. agy has no way to reach Bedrock
at all.

codex's client reads the region from `AWS_REGION` or `AWS_DEFAULT_REGION` itself, so yolo writes
its `aws.region` only for a region you set on the provider. That order is INFERRED: the one
message in codex-cli 0.158.0 naming it is about codex's other Bedrock provider and its bearer
tokens, and codex was never run. While a codex profile selects Bedrock, yolo pins codex's
`model_provider`, so `codex login` cannot switch that session to another Bedrock login; pick
another profile for that.

opencode reads `AWS_REGION` and not `AWS_DEFAULT_REGION`. yolo writes its `options.region`
from a region you set on the provider, so set it there, deliver `AWS_REGION`, or name one in
your AWS profile, which yolo hands opencode as `AWS_REGION` when no region variable reaches it.
A launch that gives opencode only `AWS_DEFAULT_REGION` is refused, since opencode would
otherwise use `us-east-1`; yolo does not put your profile's region in its place, because the
region you delivered may differ.

pi lists the models under its own `amazon-bedrock` provider. An id pi's own catalog also holds
takes the facts this list declares in place of pi's (its cost and thinking levels among them),
because a pi model row replaces the catalog entry of the same id. A region you set on the
provider reaches pi as `AWS_REGION`.

### Choosing another model

Name one in a profile of your own. An entry of this provider that the agent cannot call is
skipped, never sent; an id the provider does not list is passed through as you wrote it:

```jsonc
// ~/.config/yolo-jail/config.jsonc
"profiles": { "astra": { "provider": "bedrock", "model": "global.openai.gpt-6-astra" } }
```

Add a model with its maker, so each agent's maker filter applies to it: here opencode and pi can
use it, and claude and codex skip it (codex's filter takes OpenAI's makers only, although Kimi
K3's card lists the Responses API codex drives):

```jsonc
"providers": { "bedrock": { "models": {
  "kimi": { "id": "global.moonshotai.kimi-k3", "vendor": "moonshotai" } } } }
```

A model you add with no `vendor` (a plain `"alias": "id"`) passes every agent's filter.

### Sources

Every id above was read from its AWS model card on 2026-09-29, and nothing was called:

- [Claude Opus 5.5](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-opus-5-5.html):
  runtime global id `global.anthropic.claude-opus-5-5`; Messages, Converse and Invoke on
  runtime, not Chat Completions or Responses; a 1M-token window, 128K output.
- [GPT-6.1 Sol](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-openai-gpt-6-1-sol.html):
  runtime US geo id `us.openai.gpt-6.1-sol`, no global or in-Region id; Responses, Chat
  Completions, Converse and Invoke on runtime, not Messages; a 1M-token window, 131,072 output.
- [GPT-6 Astra](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-openai-gpt-6-astra.html):
  runtime ids `us.openai.gpt-6-astra` and `global.openai.gpt-6-astra`; Responses, Chat
  Completions and Converse on runtime, not Messages or Invoke; a 1,050,000-token window,
  128,000 output.
- [Models at a glance](https://docs.aws.amazon.com/bedrock/latest/userguide/model-cards.html):
  the OpenAI models Bedrock lists, GPT-6 Astra and GPT-6.1 Sol among them.

Re-read the cards before changing an id: the prefixes each model offers differ per model and
move ([`model-lists-and-pickers.md` §5.3](../../docs/design/model-lists-and-pickers.md#53-the-prerequisite-verify-before-an-id-ships)).

## `bedrock-bridge`: the same provider through the wire bridge

The pack ships a second profile, `bedrock-bridge`: the same `bedrock` provider with
`"via": "wire-bridge"`, which sends an agent's traffic through yolo's wire bridge instead of the
agent's own Bedrock client. It is the one profile that forces the bridge, and it is where
claude's route to non-Anthropic models goes. A profile of your own forces it the same way with
`via`.

**Every agent rides it.** The provider names a region and no address, so the bridge composes
Bedrock runtime's own `https://bedrock-runtime.<region>.amazonaws.com/openai/v1` from the region:
the provider's `region` when you set one, else the `AWS_REGION` (then `AWS_DEFAULT_REGION`) the
agent was given, which the launch fills from `~/.aws/config` when nothing else names one. The
bridge signs each request with the agent's own AWS credentials: a key pair, the `aws-auth`
pointer, or a Bedrock API key (`AWS_BEARER_TOKEN_BEDROCK`), and never a profile in `~/.aws`.

- **claude** is routed at the bridge's Anthropic address, the everything profile: every model on
  the list in one session, Claude Opus 5.5 untranslated to Bedrock's own Messages route and the
  OpenAI models translated.
- **codex, pi, opencode and oh-omp** send their own OpenAI-shaped requests through the bridge
  unchanged, pi, opencode and oh-omp chat-completions and codex Responses.
- **copilot** is routed at the bridge's Anthropic address too, and starts on the list's first
  model, since the list names no `default`.

No agent quietly falls back to its own Bedrock client, since the profile asked for the bridge. At
`yolo host`, which has no bridge, the profile uses each agent's own client. No request has reached
Bedrock through the bridge yet; see [the wire bridge](../../docs/reference/wire-bridge.md#which-upstream-is-bedrocks).

## Why it needs `aws-auth`

[`aws-auth`](../aws-auth/README.md) turns a host `aws sso login` into a narrowed credential a
jail can use. It is the one Bedrock credential that refreshes inside a running jail, so it comes
with the provider it serves. Selecting it changes nothing until you enable the loophole.
