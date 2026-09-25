# `llamacpp` — the official pack that ships a local `llama-server` as a provider

Local inference is a **MODE**, not a second config surface: the endpoints, the model
alias, the context window and the credential story travel together as one
`kind: "provider"` contribution plus the `kind: "profile"` selection over it — the same
shape [`zai`](../zai/README.md) and [`cerebras`](../cerebras/README.md) ship. There is
no `llm_endpoints` key, and there is deliberately never going to be one: a bare
`{base_url, model}` pair splits a switch into halves that can land separately, which is
the measured failure this project already paid for once
([`agent-auth-modes.md`](../../docs/design/agent-auth-modes.md)).

The pack installs **no CLI** and **needs no credential**. It points at
[llama.cpp](https://github.com/ggml-org/llama.cpp)'s `llama-server` on the machine that
launches yolo, at its default port, over two protocols it serves natively:

| Protocol | URL | Who consumes it |
|---|---|---|
| `anthropic` | `http://localhost:8080` (origin — Claude Code appends `/v1/...` itself) | claude |
| `openai` (`openai-chat-completions`) | `http://localhost:8080/v1` | pi, opencode, copilot |

`llama-server` speaks the **Anthropic Messages API natively** — `POST /v1/messages` and
`/v1/messages/count_tokens`, registered unconditionally since
[PR #17570](https://github.com/ggml-org/llama.cpp/pull/17570) (first tag `b7187`) — so
claude needs no bridge, no translator and no proxy. That is why this pack ships **no
`needs` entry**, unlike `cerebras` and `kilo`, whose anthropic URL is the wire bridge's.

## Running the server

```bash
llama-server -hf <repo>:<quant> --host 127.0.0.1 --port 8080 \
  --alias llama -c 32768 -ngl 99
```

Three flags are load-bearing for the values this pack declares:

- **`--alias llama`** — the model id every agent sends. `model` is ignored in
  single-model mode and *authoritative* in router mode, so pinning a stable name costs
  nothing and is required by one of the two modes. Name it something else and override
  `providers.llamacpp.models.default` in your user config.
- **`-c 32768`** — must match the pack's `context_window` option, which is what sizes
  claude's auto-compact window and pi's/opencode's context accounting. Overflow is a
  hard 400 on this server; no agent's compaction recognises its error shape.
- **`--jinja`** is the server default since
  [PR #17524](https://github.com/ggml-org/llama.cpp/pull/17524), and tool calling now
  requires the model's own template to support it — `GET /props` →
  `chat_template_caps.supports_tools` is the pre-flight that saves an afternoon. A GGUF
  that reports `false` there will not drive any agent, whatever you configure here.

## The user's entire setup

```jsonc
// ~/.config/yolo-jail/config.jsonc
{
  "packs": ["claude", "pi", "llamacpp"],
  "network": { "forward_host_ports": [8080] }
}
```

```console
$ yolo -p llamacpp -- claude
```

**`forward_host_ports` is not optional on a bridged container launch, and it is not
automatic here.** A loopback URL written in *your* `providers` config makes yolo add
that forward implicitly; a loopback URL a **pack** declares never does, because a pack
endpoint is a jail-local service fact (the wire bridge's `127.0.0.1:8214` is the
worked example, and a pack must not be able to open a hole to the host by declaring
one). The forward is the ordinary Unix-socket hop, so it sidesteps the whole
pasta/slirp4netns loopback question — and on `network.mode: "host"` or the
`macos-user` backend there is nothing to forward, because the jail already shares the
launcher's loopback.

## What lands where

| Agent | What it gets |
|---|---|
| claude | `ANTHROPIC_BASE_URL=http://localhost:8080`, `ANTHROPIC_AUTH_TOKEN=local` (the derive's dummy for a routed keyless endpoint), `ANTHROPIC_MODEL` / `ANTHROPIC_DEFAULT_{OPUS,SONNET,HAIKU}_MODEL=llama`, `CLAUDE_CODE_MAX_CONTEXT_TOKENS` + `CLAUDE_CODE_AUTO_COMPACT_WINDOW=32768`, `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` |
| copilot | BYOK env: `COPILOT_PROVIDER_BASE_URL=http://localhost:8080` + `COPILOT_PROVIDER_TYPE=anthropic` (its derive prefers an anthropic endpoint when one exists), `COPILOT_MODEL=llama`, `COPILOT_PROVIDER_API_KEY=local`, `COPILOT_PROVIDER_MAX_PROMPT_TOKENS=32768` |
| pi | a `llamacpp` catalog entry in `~/.pi/agent/models.json` — `api: "openai-completions"`, `apiKey: "local"` (pi filters a credential-less provider out of `/model` *and* out of selection resolution), `contextWindow`/`maxTokens`, and the `compat` block this pack's compat options declare (below) — plus `defaultProvider`/`defaultModel` when the profile is selected |
| opencode | a `llamacpp` provider in `opencode.json` — `baseURL` and `apiKey` under `options`, `limit.context`/`limit.output` from this pack's options — plus `model = "llamacpp/llama"` |
| codex | **nothing — no entry and no selection**, deliberately. `wire_api = "chat"` was removed from codex; it speaks `responses` only, and this pack's openai endpoint declares `openai-chat-completions` because that is what the server serves best. llama-server *does* expose `/v1/responses`, but its Codex compatibility rides an unmerged upstream PR, so a `openai-responses` spelling here would be a config that boots green and fails at the first turn. |
| agy | **nothing, ever** — closed transport enum, no base-URL hook |

The pack also ships one **profile-gated** `kind: "env"` entry:
`CLAUDE_CODE_ATTRIBUTION_HEADER=0`, set only while this profile is active. Claude Code
prepends an attribution block to the system prompt; llama.cpp then fails prefix reuse
and **reprocesses the entire prompt every turn**, which is the difference between a
usable local agent and an unusable one. Verified in the shipped client rather than from
the post that first reported it — claude 2.1.274 reads
`process.env.CLAUDE_CODE_ATTRIBUTION_HEADER` and emits the empty block when it is set
falsey. A pack's env fold is jail-global, so it is gated: nothing is set for a launch
that did not ask for a local model.

## Credentials — normally none

The provider declares **no `api_key_env_name`**, which is the whole credential story for
a local server: `llama-server` skips key validation entirely when started without
`--api-key`, and every agent above is handed a dummy by its own derive rather than
being left half-configured. Because the entry names no credential variable, the
launch's credential pre-flight requires nothing of it.

A server started **with** `--api-key`, or a hosted OpenAI-compatible endpoint you point
this entry at, is the case the key path exists for. Name the variable in **user** config
and hydrate it through `env_sources`:

```jsonc
// ~/.config/yolo-jail/config.jsonc
{"providers": {"llamacpp": {"api_key_env_name": "LLAMA_API_KEY"}}}
```

From then on the requirement is enforced the way every other provider's is: a launch
that catalogs the entry and cannot deliver the variable **refuses**, naming the
variable, rather than starting an agent that will authenticate as nobody.
`YOLO_ALLOW_MISSING_PROVIDERS=1` continues loudly instead.

## Pointing it somewhere else — user scope only

`providers.llamacpp` merges over this pack per field, so a different port, a second
model alias or a real context window is one key in your config. **An address may only
be written at user scope.** A `base_url` or an `endpoints.<protocol>.base_url` in a
workspace `yolo-jail.jsonc` is refused by `yolo check` and by every launch: that file
travels with the repo and is writable by the agent running inside the jail, and an
agent that can rewrite where its own inference goes can route every prompt, every file
it has read and every hydrated credential to a host of its choosing. The rest of the
entry — `models`, `options`, `region`, `api_key_env_name` — still merges from either
scope.

```jsonc
// ~/.config/yolo-jail/config.jsonc — a 128K-context server on another port
{"providers": {"llamacpp": {
  "endpoints": {"openai": {"base_url": "http://localhost:9090/v1"},
                "anthropic": {"base_url": "http://localhost:9090"}},
  "options": {"context_window": "131072", "max_tokens": "16384"}}}}
```

`api_timeout_ms` is declared with no default so a profile can raise it without the
provider guessing a number: local inference on CPU is slow enough that an agent's own
request ceiling, not the server, is what ends a long turn.

## The compat facts — what this server does and does not support

Six of this provider's options state what `llama-server` supports at the wire level.
They are **service facts**, so the provider declares them and each agent's derive
translates them into whatever that agent calls the same thing — nothing detects a
vendor, and no agent's derive carries a llama.cpp branch.

| Option | Default | What it says |
|---|---|---|
| `supports_store` | `"false"` | the server does not accept the `store` request field |
| `supports_developer_role` | `"false"` | no `developer` message role |
| `supports_reasoning_effort` | `"false"` | no `reasoning_effort` |
| `supports_usage_in_streaming` | `"true"` | it does honour `stream_options.include_usage` |
| `supports_strict_mode` | `"false"` | no strict tool schemas |
| `max_tokens_field` | `"max_tokens"` | the output cap is `max_tokens`, not `max_completion_tokens` |

The values are not guesswork: they are the block **pi's own built-in llama.cpp provider
generates** (`pi-coding-agent/dist/extensions/llama/provider.js`, verified at 0.85.1),
i.e. pi's authoritative statement about this server. ⚠ `supports_usage_in_streaming` is
the one that is `true`, and it *flipped* from `false` between pi 0.82.1 and 0.84.4 — the
older example JSON in
[`local-model-endpoints.md`](../../docs/research/local-model-endpoints.md) still carries
the stale value, corrected in that section's 2026-09-02 re-verification. Re-read the
shipped file rather than copying either.

Today **pi** is the only agent whose derive reads them. An agent that does not is
unaffected: an option nobody consumes is inert, not invalid. One more reader is not an
agent: the wire bridge reads `supports_usage_in_streaming` for a provider it bridges, and
does not ask a `"false"` one for streamed usage
([`wire-bridge.md`](../../docs/reference/wire-bridge.md#streamed-usage)).

A build of `llama-server` that supports more is one key in your config, because
`providers.llamacpp.options` merges over this pack per field:

```jsonc
// ~/.config/yolo-jail/config.jsonc
{"providers": {"llamacpp": {"options": {"supports_strict_mode": "true"}}}}
```

### Another local server — ollama, vLLM, or your own

There is no pack for those, and adding one needs no code. A provider entry that
declares the same options gets the same treatment, because the mechanism keys on the
**declaration** and never on the name or the address:

```jsonc
// ~/.config/yolo-jail/config.jsonc
{"providers": {"ollama": {
  "endpoints": {"openai": {"base_url": "http://localhost:11434/v1",
                           "wire_api": "openai-chat-completions"}},
  "models": {"default": "qwen3-coder:30b"},
  "options": {"context_window": "65536", "max_tokens": "8192",
              "supports_store": "false", "supports_developer_role": "false",
              "supports_reasoning_effort": "false",
              "supports_usage_in_streaming": "false",
              "supports_strict_mode": "false",
              "max_tokens_field": "max_tokens"}}},
 "profiles": {"ollama": {"provider": "ollama"}}}
```

State only the facts you measured against *your* server. An option you leave out is
left to the agent's own default rather than filled in with a guess — which is the whole
reason these are declared and not inferred from the URL.

## Verifying

```console
$ yolo pack lint packs/llamacpp      # claims + the strict manifest read
$ yolo pack footprint llamacpp       # the provider, the selection over it, no grants
```

⚠ **Nothing here has been exercised against a live server.** Every per-agent spelling
is verified from the shipped implementation (each agent's own binary or npm bundle) and
from llama.cpp's source, never from observed traffic — this repo's rules forbid an
automated test that starts an agent or makes an API call. Each row of the table above is
therefore a claim awaiting one manual turn against a real server, and the known soft
spot to check first is copilot, which is handed the **anthropic** endpoint (its derive
prefers one when the provider declares it).
