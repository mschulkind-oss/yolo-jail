# `bedrock` — Amazon Bedrock, as one provider every agent reads

This pack declares Amazon Bedrock's `bedrock-runtime` endpoint as one provider, `bedrock`, and
the profile `bedrock` that selects it. It installs no program. Every agent pack that can put its
agent on Bedrock `needs` it, so selecting that agent brings it in:

```jsonc
// ~/.config/yolo-jail/config.jsonc
{ "packs": ["codex"] }
```

`-p bedrock` then resolves for codex with nothing else listed. The launch prints
`+ bedrock (needed by codex)` and `+ aws-auth (needed by bedrock)`.

Design: [`bedrock-plumbing.md`](../../docs/design/bedrock-plumbing.md) ([OQ-BR9](../../docs/design/bedrock-plumbing.md#OQ-BR9),
[OQ-BR1](../../docs/design/bedrock-plumbing.md#OQ-BR1)); behavior:
[`providers.md`](../../docs/reference/providers.md).

## What the provider declares

- **`"platform": "aws-bedrock"`**, what service it is. claude's Bedrock switch, `aws-auth`'s
  credential pointer and the region check all key on it, never on the provider's name, so a
  provider of your own that declares the same platform gets the same behavior.
- **No endpoint and no region.** Each agent's own Bedrock client composes its URL from a region,
  and yolo ships none: set one as `"providers": {"bedrock": {"region": "us-east-1"}}` in your
  user config, or as `AWS_REGION` in an `env_sources` entry. A launch that can see neither is
  refused and says so.
- **`region_env_name`**: `AWS_REGION` and `AWS_DEFAULT_REGION`, the variables that count as a
  region for every provider of this platform.
- **The six AWS credential variables** under `api_key_env_name`: `AWS_BEARER_TOKEN_BEDROCK`,
  `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`, `AWS_PROFILE` and
  `AWS_CONTAINER_CREDENTIALS_FULL_URI`. yolo delivers each only to an agent whose profile selects
  a Bedrock provider. None is demanded: an agent's own client takes whichever credential it
  finds.

## The models it ships

One provider holds every maker's models. Each entry names its maker as `vendor`, and yolo never
guesses the maker from the id. Each agent is offered only the entries its own Bedrock client
can call, and it starts on the first of those in the order below, unless you name a model:

| Order | Model id | Maker | Name |
| :--- | :--- | :--- | :--- |
| 1 | `global.anthropic.claude-opus-5-5` | `anthropic` | Claude Opus 5.5 (Global) |
| 2 | `us.openai.gpt-6.1-sol` | `openai` | GPT-6.1 Sol (US) |
| 3 | `global.openai.gpt-6-astra` | `openai` | GPT-6 Astra (Global) |

Each entry also carries the context window, the output cap and the input kinds its AWS page
states (`context_window`, `max_tokens`, `input`), and Claude Opus 5.5 its reasoning support.

- **These are `bedrock-runtime` ids.** Runtime takes a cross-Region inference profile id,
  `global.` or a geography such as `us.`, where the bare id is the other endpoint family's
  spelling. `global.` routes worldwide with no data-residency constraint.
- **GPT-6.1 Sol is `us.` only.** AWS offers it on runtime through the US inference profile
  alone, with no global or in-Region id (read 2026-09-29, its launch day). Outside the US and
  Canada Regions it is refused, so pick GPT-6 Astra there, or name your own model.
- **GPT-6 Sol is not shipped.** AWS publishes no GPT-6 Sol page and its model list names none
  (read 2026-09-29), so there is no id to verify. The maintainer's rule was to ship GPT-6.1 Sol
  where Bedrock offers it and GPT-6 Sol only where Bedrock offers nothing newer.

### Which agent starts where

| Agent | Can call on Bedrock | Starts on, with no model named |
| :--- | :--- | :--- |
| claude | Anthropic models: its Bedrock client drives the Messages API, which serves Claude only | its own Bedrock default. yolo pins a model only when the profile names one, or your config names a `default` alias Claude can call |
| codex | OpenAI models: its built-in `amazon-bedrock-runtime` client drives the Responses API, which AWS serves for them and not for Anthropic's | GPT-6.1 Sol (US) |

The agents that run their own Bedrock client are listed here as each is bound.

codex's client reads the region from `AWS_REGION` or `AWS_DEFAULT_REGION` itself, so yolo writes
its `aws.region` only for a region you set on the provider. While a codex profile selects
Bedrock, yolo pins codex's `model_provider`, so `codex login` cannot switch that session to
another Bedrock login; pick another profile for that.

### Choosing another model

Name one in a profile of your own. An entry of this provider that the agent cannot call is
skipped, never sent; an id the provider does not list is passed through as you wrote it:

```jsonc
// ~/.config/yolo-jail/config.jsonc
"profiles": { "astra": { "provider": "bedrock", "model": "global.openai.gpt-6-astra" } }
```

Add a model by its maker, so each agent is offered it only if it can call it:

```jsonc
"providers": { "bedrock": { "models": {
  "kimi": { "id": "global.moonshotai.kimi-k3", "vendor": "moonshotai" } } } }
```

A model you add with no `vendor` (a plain `"alias": "id"`) is offered to every agent.

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
  the OpenAI models Bedrock lists, GPT-6 Sol not among them.

Re-read the cards before changing an id: the prefixes each model offers differ per model and
move ([`model-lists-and-pickers.md` §5.3](../../docs/design/model-lists-and-pickers.md#53-the-prerequisite-verify-before-an-id-ships)).

## Why it needs `aws-auth`

[`aws-auth`](../aws-auth/README.md) turns a host `aws sso login` into a narrowed credential a
jail can use. It is the one Bedrock credential that refreshes inside a running jail, so it comes
with the provider it serves. Selecting it changes nothing until you enable the loophole.
