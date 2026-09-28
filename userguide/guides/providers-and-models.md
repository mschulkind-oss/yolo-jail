# Providers and Models

By default each agent talks to its own vendor's models, with the login you give it inside the jail.
yolo can point an agent at a different model service instead, such as z.ai, OpenRouter, a model on
AWS Bedrock, or a model you run yourself, without you editing each agent's settings.

Two words do the work:

- A **provider** is a model service: its address, the name of the environment variable that holds
  its API key, and short names for its models. A provider never holds the key itself.
- A **profile** is a named choice of a provider and, optionally, a model. Nothing changes until a
  launch selects a profile.

## Use a provider in three steps

1. **Add the provider's pack** to your user config, `~/.config/yolo-jail/config.jsonc`, beside your
   agent:

   ```jsonc
   {
     "packs": ["claude", "zai"],
     "env_sources": ["~/.config/yolo-jail/secrets.env"]
   }
   ```

2. **Put the key in a dotenv file** listed under `env_sources`, outside any repository:

   ```bash
   # ~/.config/yolo-jail/secrets.env
   ZAI_API_KEY=your-key-here
   ```

   Never put a key value in a `.jsonc` file.

3. **Select the profile** for one launch with `-p`, or make it the default for an agent with
   `use_profiles`:

   ```bash
   yolo -p zai -- claude
   ```

   ```jsonc
   { "use_profiles": { "claude": "zai" } }
   ```

A key reaches only the agents whose profile selects its provider. Another agent, or a plain shell in
the jail, does not see it, and the launch lists which keys went where. If the selected provider's
key is empty, the launch stops and says which variable is missing.

## The providers yolo ships

| Pack | Provider | Works with | Key variable |
|---|---|---|---|
| `zai` | [z.ai](https://z.ai) GLM models | Claude Code, Copilot, pi, opencode | `ZAI_API_KEY` |
| `openrouter` | [OpenRouter](https://openrouter.ai) | Claude Code, Codex, Copilot, pi, opencode | `OPENROUTER_API_KEY` |
| `kilo` | [Kilo](https://kilo.ai) gateway | Claude Code and Copilot through the wire bridge; pi and opencode directly | `KILO_API_KEY` |
| `cerebras` | [Cerebras](https://www.cerebras.ai) | Claude Code and Copilot through the wire bridge; pi and opencode directly | `CEREBRAS_API_KEY` |
| `llamacpp` | a [llama.cpp](https://github.com/ggml-org/llama.cpp) `llama-server` you run on port 8080 | Claude Code, Copilot, pi, opencode | none |

Two more come with the agent packs, with no extra pack to add:

- **`bedrock`**, in the `claude` pack: Claude Code on AWS Bedrock. With the `aws-auth` loophole on,
  it uses your host's `aws sso login`, narrowed to one role before it reaches the jail. See
  [Host Access and Loopholes](loopholes.md#the-loopholes-yolo-ships).
- **`codex`**, in the `claude`, `codex` and `pi` packs: your ChatGPT subscription, through yolo's
  shared OpenAI login. `codex` and `pi` use this login by default; `yolo -p codex -- claude` runs
  Claude Code against it. See [Logins](authentication.md#a-shared-chatgpt-login-for-codex-and-pi).

OpenRouter and Kilo ship no model list, because their catalogs change too quickly. Name the models
you want in your user config and make profiles for them:

```jsonc
{
  "packs": ["claude", "pi", "openrouter", "kilo"],
  "providers": {
    "openrouter": { "models": { "coding": "~anthropic/claude-sonnet-latest" } },
    "kilo":       { "models": { "economy": "kilo-auto/efficient" } }
  },
  "profiles": {
    "router-coding": { "provider": "openrouter", "model": "coding" },
    "kilo-economy":  { "provider": "kilo", "model": "economy" }
  },
  "use_profiles": { "claude": "router-coding", "pi": "kilo-economy" }
}
```

## The wire bridge

Agents and providers speak different request formats, and not every pair matches. Claude Code, for
example, speaks only Anthropic's format, while Cerebras and Kilo offer only OpenAI's. The **wire
bridge** is a small service that runs inside the jail and translates between the two, so a profile
like `yolo -p cerebras -- claude` still works. It comes in automatically with the packs that need
it, and it is not a loophole: it runs entirely inside the jail and gives no access to your host.

The wire bridge does not run on `macos-user` yet, so a profile that needs it is refused there, with
the reason.

## Your own provider

A provider is plain config, so you can declare one the packs do not ship. For example, a
self-hosted server that speaks OpenAI's format:

```jsonc
{
  "providers": {
    "myserver": {
      "endpoints": {
        "openai": { "base_url": "https://llm.example.com/v1", "wire_api": "openai-chat-completions" }
      },
      "api_key_env_name": "MYSERVER_API_KEY",
      "models": { "default": "my-model" },
      "options": { "model": "default" }
    }
  },
  "profiles": { "myserver": { "provider": "myserver" } }
}
```

Provider addresses, `profiles` and `use_profiles` are read from your user config only. A project's
`yolo-jail.jsonc` cannot set them, because an agent that can edit its project could otherwise send
its requests to a server you did not choose. `yolo config-ref` documents every provider field.

## After a change

Run `yolo check` after editing your config. A new key or a new `-p` choice reaches a running jail
the next time you run `yolo` in it. Adding or removing a provider pack needs a fresh jail:
`yolo stop`, then launch again.

On your own machine, `yolo host -p zai -- claude` runs a host agent with the same profile; see
[Writing your own pack](migrating-to-packs.md#part-2--manage-your-host).
