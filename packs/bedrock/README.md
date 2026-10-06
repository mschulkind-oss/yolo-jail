# `bedrock` — Amazon Bedrock, as one provider every agent reads

This pack declares Amazon Bedrock's `bedrock-runtime` endpoint as one provider, `bedrock`, and
two profiles over it, `bedrock` and `bedrock-bridge`. It installs no program and ships no model list. Every agent pack
that can put its agent on Bedrock `needs` it, so selecting that agent brings it in:

```jsonc
// ~/.config/yolo-jail/config.jsonc
{ "packs": ["codex"] }
```

`yolo -p bedrock -- codex` then configures codex to reach Bedrock through its own Bedrock
client, on its own default model, with nothing else listed but a region. No request to Bedrock has been measured from any
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

## No model list

The pack ships no model list. Each agent on `-p bedrock` starts on its own Bedrock default and
offers its own Bedrock catalog, whatever that holds: yolo does not pick or filter Bedrock models
for it ([MM-D32](../../docs/design/model-lists-and-pickers.md#MM-D32)). An agent's own catalog may
be out of date or spell an id the endpoint refuses (pi's lists some bare ids `bedrock-runtime`
does not take); that is the agent's to fix, and you work around it by naming a model.

copilot is the one exception. It has no Bedrock catalog and will not start without a model, so
through the wire bridge it starts on `openai.gpt-oss-120b-1:0`, OpenAI's open-weight
gpt-oss-120b, one of the cheapest models Bedrock serves
([MM-D34](../../docs/design/model-lists-and-pickers.md#MM-D34)). It is served in-Region, so in a
Region without it, name another model.

### Which agent can call which maker

When you or a pack supply a list, each entry names its model's maker as `vendor`, and each agent
is offered only the entries whose maker its own Bedrock client is known to serve. yolo never
guesses the maker from the id.

| Agent | Makers it takes on Bedrock |
| :--- | :--- |
| claude | Anthropic models: its Bedrock client drives the Messages API, which serves Claude only |
| codex | OpenAI's: its built-in `amazon-bedrock-runtime` client drives the Responses API, which AWS serves for them and not for Anthropic's. The filter is by declared maker, not by what codex could call |
| opencode | every entry: its built-in `amazon-bedrock` provider sends a cross-Region id to runtime's Converse API |
| pi | every entry: its built-in `amazon-bedrock` provider drives Converse |

With a list, codex, opencode and pi start on its first entry they can call, and pi and opencode
list its entries in their menus (pi's in place of its own catalog). claude pins a model only when
a profile names one, or the list names a `default` alias, that Claude can call.

copilot and oh-omp have no Bedrock client of their own, and reach Bedrock only through yolo's
wire bridge: under `-p bedrock-bridge` (below), which brings the bridge in, and under `-p bedrock`
whenever the bridge is already in the jail, as it is beside claude. A jail of them alone lists
this pack in `packs` to have either profile, and `wire-bridge` too for `-p bedrock`. agy has no
way to reach Bedrock at all. With a list, copilot in a jail shows the whole of it in its model
picker, beside GitHub's own models when copilot is signed in to GitHub; a GitHub model you pick
there is served by GitHub, not Bedrock.

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

pi lists a supplied list's models under its own `amazon-bedrock` provider. An id pi's own catalog
also holds takes the facts the list declares in place of pi's (its cost and thinking levels among
them), because a pi model row replaces the catalog entry of the same id. A region you set on the
provider reaches pi as `AWS_REGION`.

### Choosing a model

Name one in a profile of your own. It is passed through as you wrote it, unless a list you
supplied names it with a maker the agent cannot call, which is skipped, never sent:

```jsonc
// ~/.config/yolo-jail/config.jsonc
"profiles": { "astra": { "provider": "bedrock", "model": "global.openai.gpt-6-astra" } }
```

Or supply a list, with each model's maker, so each agent's maker filter applies to it: here
opencode and pi can use both, claude Claude Opus 5.5 alone, and codex GPT-6.1 Sol alone:

```jsonc
"providers": { "bedrock": { "models": {
  "opus": { "id": "global.anthropic.claude-opus-5-5", "vendor": "anthropic" },
  "sol":  { "id": "us.openai.gpt-6.1-sol", "vendor": "openai" } } } }
```

A company pack does the same for its people with a `models` contribution
([`providers.md`](../../docs/reference/providers.md#model-lists-shaped-by-packs)). A model you add
with no `vendor` (a plain `"alias": "id"`) passes every agent's filter.

Bedrock model ids depend on the endpoint family: on `bedrock-runtime` a model is named by its
cross-Region inference profile (`global.` or a geography such as `us.`) where it has one, and the
bare id is the other family's spelling. Read the id off the model's AWS card, since the prefixes a
model offers differ per model and move.

### Sources

copilot's starting model, read 2026-10-05:

- [gpt-oss-120b](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-openai-gpt-oss-120b.html):
  runtime id `openai.gpt-oss-120b-1:0`, in-Region, with no global inference id; Chat Completions
  on runtime, not the Responses API; a 128K-token window, 16K output.
- [Amazon Bedrock pricing](https://aws.amazon.com/bedrock/pricing/): gpt-oss-120b at $0.15 per
  million input tokens and $0.60 per million output tokens in the US.

Until 2026-10-05 this section dated the three model ids the pack shipped, each read from its AWS
model card on 2026-09-29; the list is recorded in
[`bedrock-plumbing.md` BR-D7](../../docs/design/bedrock-plumbing.md#BR-D7) and
[BR-D19](../../docs/design/bedrock-plumbing.md#BR-D19).

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

- **claude** runs its own Bedrock support pointed at the bridge's Anthropic address, which signs
  for it: the everything profile, every model on the list in one session. A Claude model such as
  Claude Opus 5.5 goes to Bedrock untranslated, and the bridge translates the OpenAI models.
  claude's menu is its own Bedrock one, so another maker's model is the profile's `model`; under
  an `only` the menu is the narrowed list, every maker's entries included.
- **codex, pi, opencode and oh-omp** send their own OpenAI-shaped requests through the bridge
  unchanged, pi, opencode and oh-omp chat-completions and codex Responses.
- **copilot** is routed at the bridge's Anthropic address too, and starts on a supplied list's
  `default` or first model, else on `openai.gpt-oss-120b-1:0`.

No agent quietly falls back to its own Bedrock client, since the profile asked for the bridge. At
`yolo host`, which has no bridge, the profile uses each agent's own client. Requests sent through
the bridge on 2026-10-01, from a jail in `us-east-1`, were answered on three of these routes:
Claude Opus 5.5 on claude's untranslated Messages route, GPT-6.1 Sol and GPT-6 Astra, streamed
and not, on the translating route claude and copilot share (on 2026-10-05, also
`openai.gpt-oss-120b-1:0` there, not streamed), and a request shaped like codex's on
codex's Responses route. No agent sent them, and the chat-completions route pi, opencode and oh-omp use has not
been sent one. See
[the first live requests](../../docs/design/wire-bridge-gateway.md#24-the-first-live-requests-measured-2026-10-01)
and [the wire bridge](../../docs/reference/wire-bridge.md#which-upstream-is-bedrocks).

## Why it needs `aws-auth`

[`aws-auth`](../aws-auth/README.md) turns a host `aws sso login` into a narrowed credential a
jail can use. It is the one Bedrock credential that refreshes inside a running jail, so it comes
with the provider it serves. Selecting it changes nothing until you enable the loophole.
