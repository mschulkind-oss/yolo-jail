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

   Never put a key value in a `.jsonc` file. Keep the key in `env_sources` rather than
   exporting it in your shell: inside a jail, pi, opencode and Codex see only the keys yolo
   delivers, so a launch that finds their key only in your shell stops and tells you to move it.

3. **Select the profile** for one launch with `-p`, or make it your default with the `profile` key
   in your user config:

   ```bash
   yolo -p zai -- claude
   ```

   ```jsonc
   { "profile": { "claude": "zai" } }
   ```

   The key takes the same three forms as `-p`. `"profile": "zai"` selects zai for every agent,
   like `-p zai`. `"profile": { "claude": "zai" }` selects it for one agent, like
   `-p claude=zai`. `"profile": { "*": "zai", "pi": "kilo" }` selects zai for every agent you
   do not name, like `-p zai -p pi=kilo`. A `-p` on the command line beats the key for that
   launch, for the agents it selects for: `-p kilo` runs every agent on kilo, and
   `-p claude=kilo` changes only claude, the key still choosing for the rest. The key used to be
   called `use_profiles`; yolo refuses the old name and shows your entries under the new one.

A key reaches only the agents whose profile selects its provider. Another agent, or a plain shell in
the jail, does not see it, and the launch lists which keys went where. If the selected provider's
key is empty, the launch stops and says which variable is missing. This holds on every runtime: on
`macos-user`, an agent you start from the sandbox's shell gets its own profile's settings too.

A value you set yourself wins over the one a profile sets. `ANTHROPIC_MODEL=my-model claude`, or an
`export` in the jail's shell before you start the agent, keeps your value for that run. On your own
machine it does not: `yolo host -- claude` replaces a value your shell exports with the
profile's.

## The providers yolo ships

| Pack | Provider | Works with | Key variable |
|---|---|---|---|
| `zai` | [z.ai](https://z.ai) GLM models | Claude Code, Copilot, pi, opencode | `ZAI_API_KEY` |
| `openrouter` | [OpenRouter](https://openrouter.ai) | Claude Code, Codex, Copilot, pi, opencode | `OPENROUTER_API_KEY` |
| `kilo` | [Kilo](https://kilo.ai) gateway | Claude Code and Copilot through the wire bridge; pi and opencode directly | `KILO_API_KEY` |
| `cerebras` | [Cerebras](https://www.cerebras.ai) | Claude Code and Copilot through the wire bridge; pi and opencode directly | `CEREBRAS_API_KEY` |
| `llamacpp` | a [llama.cpp](https://github.com/ggml-org/llama.cpp) `llama-server` you run on port 8080 | Claude Code, Copilot, pi, opencode | none |

Two more come with the agent packs, with no extra pack to add:

- **`bedrock`**, in the `bedrock` pack, which the `claude`, `codex`, `opencode` and `pi` packs
  bring in: AWS Bedrock, for each of those agents through its own Bedrock support. Claude Code
  uses Anthropic's models there; codex uses OpenAI's; opencode and pi use any model on the list.
  yolo ships three: Claude Opus 5.5, GPT-6.1 Sol and GPT-6 Astra. codex starts on GPT-6.1 Sol in
  every Region, opencode and pi on Claude Opus 5.5, and Claude Code on its own Bedrock default,
  unless your profile names a model. AWS offers GPT-6.1 Sol in its US Regions only for now, so
  outside the US name another model, such as GPT-6 Astra:

  ```jsonc
  "profiles": { "astra": { "provider": "bedrock", "model": "global.openai.gpt-6-astra" } }
  ```

  Add a model with its maker, so it reaches only the agents that take that maker (opencode and
  pi take every maker, Claude Code Anthropic's and codex OpenAI's):
  `"providers": {"bedrock": {"models": {"kimi": {"id": "global.moonshotai.kimi-k3", "vendor": "moonshotai"}}}}`.
  Copilot and oh-omp have no Bedrock support of their own, so they reach Bedrock only through
  the wire bridge. `-p bedrock` sends them through it whenever the bridge is in the jail, as it is
  beside Claude Code, and `-p bedrock-bridge` (below) brings the bridge in itself; either way the
  bridge signs their requests with the credentials described below. Neither pack brings the
  `bedrock` pack in, so beside them alone, list it in `packs`, and list `wire-bridge` too for
  `-p bedrock`. No agent on Bedrock has yet been tested against a real AWS account, through its
  own client or the bridge.

  With the `aws-auth` loophole on,
  it uses your host's `aws sso login`, narrowed to one role before it reaches the jail. The
  credential service inside the jail runs only when an agent is on a Bedrock provider, and only
  that agent can use it. See [Host Access and Loopholes](loopholes.md#the-loopholes-yolo-ships).
  `yolo host -- pi` on a Bedrock profile gets the same credentials, from a helper that runs for
  that one command; a profile in your `~/.aws` that holds credentials still comes first for
  every agent (the one `AWS_PROFILE` names, or `[default]` when it is unset).
  Name the AWS region as `"providers": {"bedrock": {"region": "us-east-1"}}` in your config, as
  `AWS_REGION` in an `env_sources` entry, or as the `region` of your AWS profile in
  `~/.aws/config`. yolo reads that file on your machine for the profile your credential comes
  from: the one `aws-auth` serves, else the `AWS_PROFILE` the agent receives, else `default`. In
  a jail the agent receives an `AWS_PROFILE` only from your `env_sources`; at `yolo host` it also
  gets the one in your shell. yolo hands the agent that profile's region, and says so at launch,
  in a jail and at `yolo host` alike. A `bedrock` launch that finds no region in any of the three
  is refused, and names the three. In a jail an `AWS_REGION` or `AWS_PROFILE` exported in your
  own shell does not reach the agent, so the launch is refused and says so, rather than using
  another profile's region: put it in `env_sources` instead. Each agent on Bedrock needs its own:
  a region only another agent receives does not count for it.

  A profile of your own over `bedrock`, or a Bedrock provider of your own, works exactly like
  `-p bedrock`: say the provider is Bedrock with `"platform": "aws-bedrock"`.

  ```jsonc
  // ~/.config/yolo-jail/config.jsonc
  "providers": { "bedrock-eu": { "platform": "aws-bedrock", "region": "eu-west-1" } },
  "profiles": { "eu": { "provider": "bedrock-eu" } }
  ```

  `yolo -p eu -- claude` then runs Claude Code on its own Bedrock client in `eu-west-1`, with the
  `aws-auth` credentials when that loophole is on. `platform` belongs in your user config; a
  workspace `yolo-jail.jsonc` carrying it is refused. The AWS keys you put in `env_sources`
  (`AWS_ACCESS_KEY_ID` and the rest) reach the agents on your provider, as they reach the agents
  on `bedrock`, and no other process. If you list your own `api_key_env_name` on the provider,
  only those variables are kept for its agents.

  `-p bedrock-bridge` sends each agent to Bedrock through the wire bridge instead of its own
  client, and so does any profile that adds `"via": "wire-bridge"` to a Bedrock provider. The
  bridge reaches Bedrock in the region the agent was given, found the same three ways, and signs
  every request with your AWS credentials itself. Under it Claude Code can switch between Claude
  and every other model on the list in one session: a Claude model goes to Bedrock untranslated,
  so prompt caching and thinking keep working, and any other model is translated. codex,
  opencode, pi and oh-omp send their own requests through the bridge unchanged, and Copilot
  starts on the first model on the list. A Bedrock provider of your own that names its own
  address in `endpoints` is signed there too, whatever the address, once its `platform` says
  `aws-bedrock`. The bridge signs with a key pair, the `aws-auth` login or a Bedrock API key, and
  not with a profile in `~/.aws`, so in a jail whose only AWS credential is `AWS_PROFILE` it has
  nothing to sign with. codex, opencode, pi and oh-omp then get an error on every request, naming
  the three credentials it takes. Claude Code and Copilot get no bridge to talk to: with only them
  on the profile the jail does not start, and says why, naming the same three. At `yolo host`,
  which has no bridge, such a profile uses each agent's own Bedrock client, and Copilot and
  oh-omp reach nothing.

  If your own `~/.claude/settings.json` turns Bedrock on (`"env": {"CLAUDE_CODE_USE_BEDROCK":
  "1"}`) while claude's profile is not a Bedrock one, the launch says so in one line, naming the
  `-p` that fixes it; yolo leaves a key you wrote alone. `yolo host apply` writes that key itself
  while your host selection puts Claude Code on Bedrock, and removes it again once the selection
  moves off Bedrock; until then the line says the key is yolo's.
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
  "profile": { "claude": "router-coding", "pi": "kilo-economy" }
}
```

## Several providers in one session

pi and opencode can each use more than one provider at once and switch between them with their
own model picker (`/model` in pi, `/models` in opencode). List the profiles for the agent,
separated by commas on the command line or as a list in your config:

```bash
yolo -p pi=zai,openrouter -- pi
yolo -p opencode=zai,openrouter -- opencode
```

```jsonc
{ "profile": { "pi": ["zai", "openrouter"], "opencode": ["zai", "openrouter"] } }
```

- **The first entry is where a new session starts**: here pi and opencode open on z.ai's default
  model. A model you pick yourself in pi, on any listed provider, stays picked on the next launch.
  opencode goes back to the first entry's default model each time it starts, when that entry has
  one, so a model you pick in opencode lasts until you quit it.
- **Every listed provider is live**: each agent's model picker offers every listed provider's
  models, and the agent receives every listed provider's key, while other agents and a plain
  shell receive none of them. In pi the `/model` list shows the first entry's models first, and
  child agents started by pi-subagents may use any listed provider and no other. opencode shows
  exactly the listed providers and hides every other one, and it orders its picker itself. A
  provider you declared with a key and no address, which means the agent's own service, is
  opencode's built-in provider of the same name, so give it opencode's name for that service
  (`anthropic`, `openai`).
- **Every listed provider needs its key.** If one is missing, the launch stops and names that
  provider and its place in the list; it never starts the agent on the rest.
- On the command line a comma continues the list of the agent named before it:
  `-p pi=zai,openrouter,claude=codex` gives pi two providers and Claude Code one. A later
  `-p pi=…` replaces pi's whole list, and a `-p` replaces the list in your config for that launch.
- **Only pi and opencode take a list today.** Claude Code, Codex and Copilot use one provider per
  session, and oh-omp takes one profile until yolo learns to hand it a list, so a list named for
  any of them is refused before anything starts, and the message names the one-profile
  spelling. A list with no agent named, `-p zai,openrouter` or `"profile": ["zai", "openrouter"]`
  in your config, goes whole to pi and opencode and its first entry to every other agent, and the
  launch says which agents ignore the rest. Every name in it must still be a profile that exists,
  including the ones an agent ignores.
- **When the first entry names no model**, as `openrouter` and `kilo` do out of the box, opencode
  still shows only the listed providers and picks the model itself: a model you picked before on
  one of them, else a default of its own among them. List a provider that has a default model
  first to have opencode start there.
- A profile that routes through the wire bridge (`"via": "wire-bridge"`) can only be listed first.
  Two profiles over the same provider cannot share a list, and neither can two Bedrock providers,
  since each agent reads one AWS region. One Bedrock profile can sit anywhere in the list
  (`-p pi=zai,bedrock` or `-p opencode=zai,bedrock`), and the agent reaches it through its own
  Bedrock client either way, with the region from the provider or your `~/.aws/config`.
- A profile name cannot contain a comma, and in your config a list is always a JSON array, never
  `"zai,openrouter"`. The same list works at `yolo host -p pi=zai,openrouter --
  pi`, in `yolo host env --agent pi -p zai,openrouter`, on `macos-user`, and in the files
  `yolo host apply` writes, for opencode as for pi.

## The wire bridge

Agents and providers speak different request formats, and not every pair matches. Claude Code, for
example, speaks only Anthropic's format, while Cerebras and Kilo offer only OpenAI's. The **wire
bridge** is a small service that runs inside the jail and translates between the two, so a profile
like `yolo -p cerebras -- claude` still works. It comes in automatically with the packs that need
it, and it is not a loophole: it runs entirely inside the jail and gives no access to your host.

Translating costs a few features: prompt caching and extended thinking do not survive it. On
Amazon Bedrock the bridge skips translation for Claude models. When Claude Code or Copilot reaches
Bedrock through the bridge, a model the provider's list marks as Anthropic's goes to Bedrock's own
Claude endpoint exactly as the agent sent it, so caching and thinking work, and every other model
on the list is translated as before. The mark is the model's maker, `vendor`. A pack your
organization ships or your [local pack](packs-and-skills.md) sets it in the pack's
`model_options`, and a model you add yourself sets it in the object form shown above for Kimi,
with `"vendor": "anthropic"`. A model you add as a plain id is translated, and so is a pack's
short name you point at a different model, unless your entry names that model's maker. This
works for a Bedrock provider whose `openai` endpoint is Bedrock's `/openai/v1` address. The
shipped `bedrock` provider does not go through the bridge yet, so today it takes a provider that
names that address: one a pack ships, or one you declare under `providers`.

On your own machine the bridge runs for one command. `yolo host -p codex -- claude` runs Claude
Code on your ChatGPT subscription: yolo starts the bridge beside `claude`, lets only that `claude`
use it, and stops it when `claude` exits. A selection in the `profile` key works the same way through
`yolo host -- claude` and the host wrappers. `yolo host env` cannot start the bridge, so it refuses
such a profile and names the command that works. `yolo host apply` writes no bridge address into
your files, so a `claude` you start some other way, such as from an IDE that runs the program
directly, runs on its own login instead. The `macos-user` backend works like the host: each launch
that needs the bridge starts it outside the sandbox and stops it when the launch ends.

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

`models` gives your models short names. Four names mean the same thing on every provider, so a
pack can ask for a kind of model without knowing yours: `default`, `fast` for a cheap and quick
one, `balanced` for the middle tier and `frontier` for the most capable. None is required. If a
pack asks for one your provider does not name, the launch prints a warning and starts anyway.
Claude runs its Sonnet tier on your `balanced` model and its Haiku tier, which it also uses for
background work, on your `fast` one. If you name `sonnet` or `haiku` instead, Claude still uses
them, and they win when you name both.

When you run pi with a profile and the pi-subagents extension, a child agent starts on the same
model as pi and can use only that provider's models: the ones you list in `models`, or any of the
provider's models when you list none.

Provider addresses, `profiles` and `profile` are read from your user config only. A project's
`yolo-jail.jsonc` cannot set them, because an agent that can edit its project could otherwise send
its requests to a server you did not choose. `yolo config-ref` documents every provider field.

## Model menus, and a company's model list

When Claude Code reaches a provider through yolo that lists its models (z.ai, Cerebras,
llama.cpp, your ChatGPT subscription, or one of your own), its model menu lists that provider's
models instead of Opus, Sonnet and Haiku, and those three, Fable too, all point at the provider's
models. OpenRouter and Kilo ship no model list, so on them Claude Code keeps its usual menu until
your own `providers.<name>.models`, or a pack's `add` (below), lists some models.

Codex on your ChatGPT subscription (`yolo -p codex -- codex`) shows the models yolo lists for the
subscription in its `/model` menu, the ones Claude Code shows there, in the same order, and
nothing else. yolo builds that menu from Codex's own list of the
models it knows, again whenever Codex is updated, so a model your Codex is too old to know is left
out, and yolo says so each time Codex starts; updating Codex brings it back. On any other provider,
Codex keeps its usual menu.

`yolo host -- codex` shows the same menu when your config's `profile` picks your ChatGPT
subscription for Codex (`"profile": {"codex": "codex"}`), and yolo keeps the menu in its own
folder, never in your `~/.codex`. There, a `-p` does not change which provider Codex runs on:
Codex reads that from its own config, which `yolo host apply` writes for the profile your config
names. So a `-p` naming another provider than that one leaves Codex's usual menu in place, and
yolo says why each time. To use yolo's menu at the host, set `profile` for Codex in your config
and run `yolo host apply`.

pi shows the same models for your ChatGPT subscription and runs no other one there, as Claude
Code does. A model typed with `pi --model` stops with an error naming the list, and a session you
resume that was saved on another model continues on a different one, with pi saying it could not
restore the model. That holds when you launch with no profile too. To use a model yolo does not
list there, add it to `providers.openai-codex.models` in your config, or launch with a profile
that sets `"enforce_models": false`. Under `yolo host`, pi reads both from a file
`yolo host apply` writes for the profile your config's `profile` names for pi, and a `-p` on the
launch does not change it, so make the change in your config and run `yolo host apply`.

A model you pick with `/model` stays picked at the next launch on your ChatGPT subscription and on
a list a pack narrowed with `only` (below), as long as the profile's `enforce_models` is on, which
it is unless you turn it off. On the other providers Claude Code still starts on the provider's
default model each time.

A pack can shape a provider's model list, even one another pack ships. That lets a company hand
its people one list of the models it has approved, instead of everyone copying the list into
their own config. `add` puts models on the list, and `only` keeps just the ones it names:

```jsonc
// in the company pack's pack.json
"contributes": [
  { "kind": "models", "provider": "openrouter", "add": [
      { "id": "~anthropic/claude-sonnet-latest", "vendor": "anthropic",
        "name": "Claude Sonnet", "description": "Balanced" } ] },
  { "kind": "models", "provider": "zai", "only": ["glm-5.3", "glm-5.3-flash"] }
]
```

With an `only`, each agent's menu for that provider shows the list and nothing else, where the
agent allows it:

| Agent | What it shows under an `only` | Other models |
|---|---|---|
| Claude Code | exactly the list (on its own Bedrock client, the list's Claude models), below its Default row | refused |
| opencode | exactly the list | refused |
| pi | exactly the list | refused |
| oh-omp | exactly the list | a model typed with `--model` still runs |
| Codex | on your ChatGPT subscription, exactly the list; on any other provider, its usual menu, starting on the list's default model | not refused |
| Copilot | its usual menu, starting on the list's default model | not refused; whether it can show the whole list is still being decided |

When an agent reaches the provider through the wire bridge, the bridge refuses any other model
too, whatever the agent's own menu allows: pi, opencode, oh-omp or codex on a profile with
`"via": "wire-bridge"`, and Claude Code and Copilot on a provider the bridge carries to them, such
as Cerebras or `bedrock-bridge`. The agent gets an error naming the model, the list, and the
setting that turns the refusal off. Codex and Copilot are the exceptions: some of their own
background requests use models off the list, so the bridge lets their requests through and notes
an off-list model in its log. For the same reason, while Copilot shares the bridge with Claude
Code on one provider, the bridge refuses neither.

To keep the menus but stop the refusals, set `"enforce_models": false` on the profile:
`"profiles": {"zai-open": {"provider": "zai", "enforce_models": false}}`. opencode then shows its
full menu again, because it cannot narrow a menu without refusing. Claude Code also goes back to
starting every session on the profile's model, as it does on the other providers, so a model you
pick with `/model` lasts only for that session.

To have Claude Code start every session on the profile's model, even after you pick another with
`/model`, set `"pin_model": "true"` on the profile.

A pack that only adds models, with no `only`, puts them beside the agent's own models, as before.
Your own `providers.<name>.models` always has the last word: a model you add or remove there
applies after every pack's list. `yolo check` names a model a pack adds twice, an `only` that names
a model nothing added, and a list for a provider nothing declares.

`yolo check` also warns about a model in any list, yolo's own included, that no installed agent
knows. It asks each agent's own list of models, which is current with that agent's version, so a
warning usually means the model is newer than the agent or has been retired. It is only a warning:
nothing refuses to start. Today it can read pi's list. When no agent it can read is installed, it
says it could not check. A provider on your own machine, such as a llama.cpp server, is not
checked.

## After a change

Run `yolo check` after editing your config. A new key or a new `-p` choice reaches a running jail
the next time you run `yolo` in it. Adding or removing a provider pack needs a fresh jail:
`yolo stop`, then launch again.

On your own machine, `yolo host -p zai -- claude` runs a host agent with the same profile; see
[Writing your own pack](migrating-to-packs.md#part-2--manage-your-host).
