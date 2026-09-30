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

## Why it needs `aws-auth`

[`aws-auth`](../aws-auth/README.md) turns a host `aws sso login` into a narrowed credential a
jail can use. It is the one Bedrock credential that refreshes inside a running jail, so it comes
with the provider it serves. Selecting it changes nothing until you enable the loophole.
