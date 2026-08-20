# Pointing agents at a local model server

How do you point each agent yolo-jail ships at a **local inference server** — a
llama.cpp `llama-server` on the host, speaking the OpenAI API — and make that
choice **per-agent** and **declarative**? This is the evergreen domain doc for
that question. It covers the six shipped agents' provider surfaces, the server
side, and which yolo machinery would carry the config.

Researched 2026-08-20. Every per-agent claim was verified against the **shipped
implementation** — the installed binary or npm bundle in this jail — not just
vendor docs, and carries a provenance tag saying which. Claims that could not be
confirmed from a primary source are marked **UNCONFIRMED**.

Companion docs: [`agent-config-distribution.md`](./agent-config-distribution.md)
(how config is *shared*; this doc is how it is *pointed*) and
[`../design/loopback-tls-reachability.md`](../design/loopback-tls-reachability.md)
(why host-reachability is the load-bearing half).

---

## TL;DR

- **`llama-server` speaks the Anthropic Messages API natively**, not only OpenAI.
  `POST /v1/messages` and `/v1/messages/count_tokens` are registered
  unconditionally, no flag, since PR
  [#17570](https://github.com/ggml-org/llama.cpp/pull/17570) (merged 2025-11-28,
  first tag `b7187`). **This collapses the hardest case in the survey.** Claude
  Code needs no translator, no shim, and no proxy — just
  `ANTHROPIC_BASE_URL` and a dummy token. Every "write an Anthropic→OpenAI
  bridge" plan is obsolete.
- **The difficulty ranking inverted.** The two agents expected to be hardest
  (Claude Code, Copilot) are **pure environment variables**. The two expected to
  be easiest (pi, opencode) need a **composed config file**. That split, not the
  per-agent detail, is what determines the yolo-side design.
- **Copilot CLI has a first-class BYOK path** and the "GitHub welds you to their
  endpoints" prior is **refuted from its own shipped bundle**. `copilot help
  providers` explicitly names local OpenAI-compatible servers, and **GitHub
  authentication is skipped entirely** when BYOK is active.
- **Codex got *harder*, not easier.** `wire_api = "chat"` was **removed from the
  product** — `responses` is now the only legal value. Codex works against
  llama-server only via llama-server's own `/v1/responses` shim, and the PR that
  makes that endpoint actually Codex-compatible
  ([#21174](https://github.com/ggml-org/llama.cpp/pull/21174)) is **still open**.
- **`agy` is impossible.** Closed `ccpa | gemini | stubby` transport enum, no
  base-URL or API-key env var anywhere in the binary. Rejected, with evidence.
- **Transport is already solved in-tree.** `network.forward_host_ports` hops via
  a **bind-mounted Unix socket**, so a host `127.0.0.1:8080` becomes the jail's
  own `127.0.0.1:8080`. It sidesteps the entire pasta/slirp4netns loopback saga
  this repo has bled over.
- **There is a zero-code recipe available today**: `host_files` +
  `forward_host_ports`. `yolo config-ref` already names `~/.pi/agent/models.json`
  as its own `host_files` example.
- **The principled version is six mechanical edits.** `derive.lua`'s `ctx` is a
  closed, core-owned set of exactly two sources. Adding an `llm_endpoints` source
  clones the road `mcp_servers` already walks, then one `yolo.derive(...)` per
  agent pack.
- **Prior art in this repo for any model or provider config: zero.** `rg` over
  `internal/ packs/ cmd/` for `ANTHROPIC_|OPENAI_|base_url|BASE_URL` returns no
  hits. Clean greenfield.
- **The universal tool-calling fallback is gone from llama.cpp** (PR
  [#18675](https://github.com/ggml-org/llama.cpp/pull/18675), 2026-03-06). Tool
  calling now requires the model's own Jinja template to support it natively.
  `GET /props` → `chat_template_caps.supports_tools` is the pre-flight check.

---

## Part 1 — The six agents, ranked by what it costs

| Agent | Wire format it needs | Where the config goes | Cost | Verdict |
|---|---|---|---|---|
| **Copilot CLI** | OpenAI Chat Completions | **env vars only** | trivial | **Adopt** — documented BYOK, skips GitHub auth |
| **Claude Code** | **Anthropic Messages** | **env vars only** | trivial | **Adopt** — llama-server speaks it natively |
| **pi** | OpenAI Chat Completions | `~/.pi/agent/models.json` | one JSON file | **Adopt** — working precedent already on this machine |
| **opencode** | OpenAI Chat Completions | `~/.config/opencode/opencode.json` | one JSON file | **Adopt** — adapter is compiled into the binary |
| **Codex CLI** | **OpenAI Responses** | `~/.codex/config.toml` | one TOML file + upstream risk | **Shortlist** — blocked on an unmerged llama.cpp PR |
| **agy** | — | — | — | **Reject** — no provider extension point exists |

> [!IMPORTANT]
> The two env-var agents and the three file agents want **different yolo
> delivery mechanisms**. Env vars are the pack `env` kind or `env_sources`; files
> are prism config surfaces. Any design that assumes one mechanism covers the
> field is wrong. See [Part 3](#part-3--the-yolo-side-what-would-carry-this).

### Claude Code

**Verdict: trivial, and the interesting finding is upstream, not client-side.**
Claude Code has **no** first-party OpenAI mode and never needs one here. The sole
provider selector `Hn()` returns exactly `gateway | bedrock | foundry |
anthropicAws | anthropicGoogleCloud | mantle | vertex | firstParty` — there is no
`CLAUDE_CODE_USE_OPENAI`, no provider plugin point, and no SDK custom-transport
hook `[verified from source: installed binary 2.1.220, fn Hn, 2026-08-20]`. The
only `openai` strings in the whole bundle are a secret-scanning regex and prose
in a bundled skill.

None of that matters, because you point it at llama-server's **Anthropic**
endpoint:

```bash
llama-server -hf <repo>:<quant> --host 127.0.0.1 --port 8080 -ngl 99 -c 65536 --jinja

export ANTHROPIC_BASE_URL=http://127.0.0.1:8080   # origin, NOT .../v1
export ANTHROPIC_AUTH_TOKEN=dummy                  # any non-empty string
export ANTHROPIC_DEFAULT_OPUS_MODEL=<local-model>
export ANTHROPIC_DEFAULT_SONNET_MODEL=<local-model>
export ANTHROPIC_DEFAULT_HAIKU_MODEL=<local-model>
export CLAUDE_CODE_ATTRIBUTION_HEADER=0            # prompt-cache fix, see below
claude --model <local-model>
```

ggml-org's own announcement shows the minimal form verbatim
`[docs: https://huggingface.co/blog/ggml-org/anthropic-messages-api-in-llamacpp, 2026-08-20]`.
The `ANTHROPIC_DEFAULT_*_MODEL` triple is the necessary addition — it is what
makes *internal* call sites (the haiku-tier summarizer, etc.) land on the local
model instead of reaching for a name the server doesn't have.

Knobs worth knowing:

| Knob | Effect |
|---|---|
| `ANTHROPIC_BASE_URL` | Origin; Claude Code appends `/v1/...` itself |
| `ANTHROPIC_AUTH_TOKEN` | Sent as `Authorization: Bearer <v>`; **overrides a saved claude.ai login with no prompt** `[verified from source: 2.1.220, fn VXn, 2026-08-20]` |
| `ANTHROPIC_API_KEY` | Sent as `x-api-key`; needs one-time interactive approval before overriding a subscription |
| `ANTHROPIC_MODEL` | Behind a custom base URL, **an arbitrary string is accepted with no warning** — the unrecognized-model check early-returns when the base URL isn't `api.anthropic.com` `[verified from source: 2.1.220, fn pxm, 2026-08-20]` |
| `ANTHROPIC_DEFAULT_{OPUS,SONNET,HAIKU,FABLE}_MODEL` | Alias resolution — **the practical knob** |
| `ANTHROPIC_SMALL_FAST_MODEL` | **Deprecated**, superseded by `ANTHROPIC_DEFAULT_HAIKU_MODEL` |
| `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` | Suppresses bootstrap/feature-flag/telemetry calls to `api.anthropic.com` |
| `ANTHROPIC_UNIX_SOCKET` | **Undocumented.** Routes the API fetch over a unix socket instead of TCP `[verified from source: 2.1.220, fn Ih, 2026-08-20]` — potentially a cheaper jail wiring than TCP; see [OQ-5](#oq-5) |

> [!WARNING]
> **Scope trap.** There are two settings-`env` application passes
> `[verified from source: 2.1.220, fn n7t and its caller, 2026-08-20]`. User-level
> (`~/.claude/settings.json`), managed, and policy scopes get an unconditional
> `Object.assign(process.env, …)`. **Project scope** (`.claude/settings.json`) is
> filtered through a hardcoded allowlist — and `ANTHROPIC_BASE_URL`,
> `ANTHROPIC_AUTH_TOKEN`, and `ANTHROPIC_API_KEY` are **not in it**. So a
> workspace file cannot set the base URL or credential. For yolo that means the
> jail's process env or a `host_files`-delivered `~/.claude/settings.json` —
> never `/workspace/.claude/settings.json`.

Three gotchas that actually bite:

1. **Prompt-cache killer.** Claude Code prepends an attribution block to the
   system prompt; llama.cpp then fails prefix reuse and reprocesses the entire
   prompt every turn. `CLAUDE_CODE_ATTRIBUTION_HEADER=0` fixes it
   `[docs: https://www.mykolaaleksandrov.dev/posts/2026/06/claude-code-llamacpp-prompt-cache-fix/, 2026-08-20]`.
2. **`--jinja` for tool calling** (now the server default — see
   [Part 2](#tool-calling-the-part-that-changed-most)).
3. **Context ≥ 32K.** Claude Code's system prompt plus tool definitions is
   ~6–10K tokens before any conversation. Below ~32K, sessions degrade
   *silently* — truncated edits, dropped tool args — rather than erroring.
   `[inferred from community guides; the exact figure is a heuristic, not measured]`

**The Agent SDK inherits all of this**: *"The Agent SDK has no gateway-specific
options; it passes environment variables to the Claude Code process it spawns"*
`[docs: https://code.claude.com/docs/en/llm-gateway-connect#agent-sdk, 2026-08-20]`.
One asymmetry to remember: in **TypeScript**, setting `options.env` **replaces**
the environment entirely (spread `process.env` or your vars vanish); in
**Python**, `ClaudeAgentOptions(env=…)` **merges** over the inherited env.

#### If you ever do need a translator

Kept because "why not just use LiteLLM" will otherwise be re-litigated every six
months. Anthropic publishes a formal contract at
[llm-gateway-protocol](https://code.claude.com/docs/en/llm-gateway-protocol); the
required surface is `POST /v1/messages?beta=true` (**match on path, not full
URL**), unbuffered SSE with strict event ordering, `ping` events during silent
gaps (a 300 s byte watchdog aborts otherwise), an open-ended `anthropic-beta`
list, and tolerance for `cache_control` and `thinking: {"type":"adaptive"}` —
that last one being the single most likely 400 against a naive shim, because it
is sent unconditionally for model names Claude Code doesn't recognize, which
includes every local alias. `count_tokens` is officially optional with a
documented fallback; the client returns `null` on failure
`[verified from source: 2.1.220, fn _Mt, 2026-08-20]`.

| Translator | Runtime | `count_tokens` | Maintained | Verdict |
|---|---|---|---|---|
| **llama-server (native)** | C++, in nixpkgs | **Yes** | `b10516`, 2026-08-20 | **Adopt** — zero extra derivations |
| llama-swap | **Go, single binary**, in nixpkgs | passthrough | v250, 2026-08-14 | **Shortlist** — only if you need model hot-swap |
| bifrost | **Go, single binary** | No (#2902 open) | active, 7.5k★ | **Shortlist (3rd)** — if fronting a non-llama.cpp backend |
| anthropic-proxy-rs | **Rust, ~3 MB** | No | v1.2.0, 2026-05-29 | **Shortlist (3rd)** — cleanest by size; you'd own the `buildRustPackage` |
| LiteLLM | **Python** + FastAPI (+DB) | Yes (approximated) | very active | **Reject** — heaviest closure; also 1.82.7/1.82.8 shipped a credential stealer (2026-03-24) |
| claude-code-router | Node ≥22 | UNCONFIRMED | v3.0.21; 36.8k★ / 1089 open issues | **Reject** — Node closure, huge issue surface, no benefit over native |
| Ollama | Go | **No** | very active | **Reject** — [#13949](https://github.com/ollama/ollama/issues/13949): the `count_tokens` 404 cascades into 500s until manual restart |
| y-router | TS / CF Worker | — | **archived 2026-01-11** | **Reject — dead** |
| vLLM | Python | Yes | very active | **Reject for this repo** — Python closure; Rust frontend has no `/v1/messages` |

The `claude-code-proxy` name collides across four unrelated repos; all four are
rejected (Python, dormant, or solving a different problem — `raine/` relays a
ChatGPT subscription). Runtime is the deciding axis for this repo specifically:
the image build is hermetic Nix, so a Go or Rust single binary is dramatically
cheaper than anything Python.

### Copilot CLI

**Verdict: the easiest of all six, and the biggest surprise.** BYOK is a
documented, first-class path, and it is **env-var only** — there is no provider
key in `~/.copilot/config.json`
`[verified from source: @github/copilot 1.0.48, app.js help topic "config", 2026-08-20]`.

The shipped `copilot help providers` topic says it outright:

> Set the COPILOT_PROVIDER_BASE_URL environment variable to activate BYOK mode.
> … **GitHub authentication is not required when using a custom provider.**
> … Use "openai" type for any OpenAI-compatible endpoint, including Ollama, vLLM, and Foundry Local.

`[verified from source: app.js @11810457, 2026-08-20]`

```bash
export COPILOT_PROVIDER_BASE_URL=http://127.0.0.1:8080/v1   # activates BYOK
export COPILOT_PROVIDER_TYPE=openai                          # {openai,azure,anthropic}
export COPILOT_PROVIDER_WIRE_API=completions                 # {completions,responses}
export COPILOT_MODEL=llama                                   # REQUIRED
export COPILOT_PROVIDER_API_KEY=sk-no-key-required           # optional locally
export COPILOT_OFFLINE=true                                  # no GitHub network at all
copilot
```

- Enum values read from code, not docs: `COPILOT_PROVIDER_TYPE ∈
  {openai, azure, anthropic}`; `COPILOT_PROVIDER_WIRE_API ∈ {completions,
  responses}`, defaulting to `completions`
  `[verified from source: app.js @9794415 and @4967235, 2026-08-20]`.
  **`completions` is correct for llama-server**; `responses` is documented as
  "for GPT-5 series models".
- **A model is mandatory** — BYOK refuses to start without one
  `[verified from source: app.js fn mQo, 2026-08-20]`. An *unknown* model is only
  a warning; you get default token limits unless you set
  `COPILOT_PROVIDER_MAX_PROMPT_TOKENS` / `_MAX_OUTPUT_TOKENS`.
- **Activation is gated solely on `COPILOT_PROVIDER_BASE_URL`.** Set any other
  `COPILOT_PROVIDER_*` without it and the whole provider config is ignored with a
  warning.
- Nicer variant: set `COPILOT_PROVIDER_MODEL_ID=gpt-4.1` (borrow a catalog
  model's agent config and tool support) alongside
  `COPILOT_PROVIDER_WIRE_MODEL=llama` (what actually goes on the wire).
- `COPILOT_OFFLINE=true` skips *all* network — auth, telemetry, web tools, the
  GitHub MCP server, auto-update — and **requires** a local provider
  `[verified from source: app.js help topic "environment", 2026-08-20]`.
- The bundled `foundry-local-sdk/` is a red herring: it backs **voice dictation**,
  not chat routing.

### pi

**Verdict: easy — ~10 lines of JSON, no code.** pi has first-class
OpenAI-compatible provider support, and **this machine already runs a working
custom provider through exactly that mechanism** (the `bedrock-mantle` provider
delivered by the `matt-local` dotfiles pack). That precedent is the best
available template.

```json
{
  "providers": {
    "llama-local": {
      "name": "llama.cpp (local)",
      "baseUrl": "http://127.0.0.1:8080/v1",
      "api": "openai-completions",
      "apiKey": "local",
      "compat": {
        "supportsStore": false,
        "supportsDeveloperRole": false,
        "supportsReasoningEffort": false,
        "supportsUsageInStreaming": false,
        "supportsStrictMode": false,
        "maxTokensField": "max_tokens"
      },
      "models": [
        {
          "id": "llama-local",
          "name": "Llama (local GGUF)",
          "reasoning": false,
          "input": ["text"],
          "contextWindow": 32768,
          "maxTokens": 32768,
          "cost": { "input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0 }
        }
      ]
    }
  }
}
```

That `compat` block is not guesswork — it is **transcribed from the flags pi's
own built-in llama.cpp provider generates**
`[verified from source: pi 0.82.1, dist/extensions/llama/provider.js:18-35, 2026-08-20]`,
i.e. pi's authoritative statement of what llama-server does and doesn't support.

Details that matter:

- **`baseUrl` must include `/v1`** — pi hands it straight to the OpenAI SDK
  without appending
  `[verified from source: pi-ai/dist/api/openai-completions.js:505-510, 2026-08-20]`.
- **A keyless server still needs a dummy `apiKey`.** A provider with no
  resolvable credential has its models filtered out of `/model` and
  `--list-models`
  `[verified from source: dist/core/provider-composer.js:204-218, 2026-08-20]`.
  pi's own docs say so: *"keyless local servers should keep a dummy value"*.
- **`models.json` is user-level only** — there is no project override
  `[verified from source: dist/config.js:424, 2026-08-20]`. *Selection*, by
  contrast, is genuinely two-tier: `.pi/settings.json` deep-merges over the
  global file, gated behind pi's project-trust prompt.
- **Set `contextWindow`/`maxTokens` explicitly.** Omitted, they default to
  128000/16384, which would let pi overrun a 32K server before its own
  compaction triggers.
- **`PI_CODING_AGENT_DIR` relocates the whole agent dir**, and
  `models.json`/`settings.json`/`auth.json` all derive from it
  `[verified from source: dist/config.js:397,412-434, 2026-08-20]`. That is the
  clean launcher escape hatch: ship a pre-baked config without touching the
  user's `~/.pi`.
- **The `!command` escape hatch**, for anything non-static: a value whose first
  character is `!` is run through `/bin/sh -c`, 10 s timeout, stdout trimmed
  `[verified from source: dist/core/resolve-config-value.js:64-68,159-171, 2026-08-20]`.
  Sharp edge: `auth.json` values are **memoized for the process lifetime**, but
  `models.json` provider `apiKey` values are **re-executed on every request** —
  deliberately, per pi's docs.
- **There is a second, separate path** — a built-in `llama.cpp` provider driven
  purely by `LLAMA_BASE_URL`/`LLAMA_API_KEY`. It is genuinely zero-config **but
  only works against llama-server in *router* mode**, because it discovers models
  through the router API (`GET /models`, `/models/load`) rather than `/v1/models`
  `[verified from source: dist/extensions/llama/client.js:144-175, 2026-08-20]`.
  Against a plain `-m model.gguf` server it finds zero models.

### opencode

**Verdict: easy, and unusually container-friendly.** The
`@ai-sdk/openai-compatible` adapter is **compiled into the binary** — the loader
checks an in-binary chunk map first and only falls through to a vendored npm for
a name that isn't there
`[verified from source: opencode-linux-x64@1.18.19 bin/opencode, provider map, 2026-08-20]`.
So the documented config **never touches the network for its adapter**, which
matters for a hermetic image. The official docs even carry a llama.cpp example.

```json
{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "llamacpp": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "llama-server (local)",
      "options": { "baseURL": "http://127.0.0.1:8080/v1" },
      "models": {
        "qwen3-coder-30b": {
          "name": "Qwen3-Coder 30B (local)",
          "limit": { "context": 128000, "output": 65536 },
          "tool_call": true,
          "cost": { "input": 0, "output": 0 }
        }
      }
    }
  },
  "model": "llamacpp/qwen3-coder-30b",
  "small_model": "llamacpp/qwen3-coder-30b"
}
```

- **Set `small_model`.** It defaults to a Zen-hosted `gpt-5-nano` used for
  title/summary generation, so a "local-only" setup silently phones home without
  it `[docs: https://opencode.ai/docs/providers/, 2026-08-20]`. It is
  **top-level only** — `AgentConfig` has no `small_model` field, so it cannot be
  overridden per agent `[verified from source: config.json $defs.AgentConfig, 2026-08-20]`.
- **Field names are snake_case**: `tool_call`, `release_date`, `cache_read`.
  `options` and the model map are both `additionalProperties: false`.
- **A models.dev entry is not required.** Every field falls back to the config
  value via `??`; `npm` defaults to `@ai-sdk/openai-compatible` and `tool_call`
  defaults to **`true`**
  `[verified from source: bin/opencode, config-provider merge loop, 2026-08-20]`.
  The one thing that genuinely breaks is context accounting: `limit.context` and
  `limit.output` default to **`0`**, so opencode can't compute remaining context
  or drive auto-compaction. **Always set `limit`.**
- **The real network dependency is the model catalog, not the adapter.** On
  startup opencode background-fetches `https://models.opencode.ai/api.json` into
  `~/.cache/opencode/models.json`. `OPENCODE_DISABLE_MODELS_FETCH=1` neutralizes
  it; custom providers work with an empty catalog.
- **`OPENCODE_CONFIG_CONTENT` carries the entire config as an inline string**,
  parsed and merged at "local" scope — the cleanest env-var-only fit for a
  container, and it also suppresses auto-creation of a default config file
  `[verified from source: bin/opencode, config loader, 2026-08-20]`.
- `OPENAI_BASE_URL` will **not** redirect opencode: it passes an explicit
  `baseURL` into the factory for its built-in `openai` provider `[inferred]`.

### Codex CLI

**Verdict: possible, but blocked on an unmerged upstream PR — shortlist, don't
promise.** Codex still has a full custom-provider extension point, but the
question's premise no longer holds: **`wire_api = "chat"` has been removed from
the product.** The binary says so itself:

> `` `wire_api = "chat"` is no longer supported. `` … set `wire_api = "responses"` in your provider config.

`[verified from source: codex-cli 0.145.0 binary, strings @0x7B7B47, 2026-08-20]`

Deprecated 2025-12-09, non-functional by v0.92.0, removed early Feb 2026; the
stated rationale is that `chat/completions` *"originated in the GPT-3.5 era and
was not designed for today's agentic coding and reasoning use cases"*
`[docs: https://github.com/openai/codex/discussions/7782, 2026-08-20]`.

Codex therefore reaches llama-server only through llama-server's **own**
`/v1/responses` endpoint, which exists and is documented as a Chat-Completions
shim `[verified from source: llama.cpp tools/server/README.md:1455-1495, 2026-08-20]`:

```toml
model          = "llama"        # must match what GET /v1/models reports
model_provider = "llamacpp"     # NOT "ollama"/"lmstudio"/"openai" — reserved

[model_providers.llamacpp]
name     = "llama.cpp (local)"
base_url = "http://127.0.0.1:8080/v1"
wire_api = "responses"          # only legal value; "chat" is refused
env_key  = "LLAMA_API_KEY"      # optional; if declared, must be non-empty
```

> [!WARNING]
> **Residual risk.** llama.cpp PR
> [#21174](https://github.com/ggml-org/llama.cpp/pull/21174) — *"server: improve
> Responses API compliance and Codex CLI compatibility"* — was **still open**
> (`merged: false`) when checked on 2026-08-20. Its author reports Codex working
> **with the PR applied**; it adds `sequence_number` to streaming events, fixes
> `id`/`call_id` mapping, and stops 400-ing on non-function tool types. On plain
> master you may hit exactly those gaps. **Nobody has measured a completed Codex
> turn against stock llama-server** — this is the highest-value thing to test by
> hand.

Two more traps: `openai`, `ollama`, and `lmstudio` are **reserved provider IDs**
and naming your table one of them is a hard config error; and `--oss` mode is a
dead end — `CODEX_OSS_BASE_URL` exists, but the OSS providers are bespoke clients
for Ollama's and LM Studio's *management* APIs (`/api/tags`, model-load calls),
none of which llama-server implements. Use the custom `model_providers` table.
`codex doctor` prints the resolved provider, wire API, endpoint, and
reachability — cheap verification without burning a turn.

### agy (Antigravity)

**Verdict: rejected — no provider extension point exists.** `agy` is a client for
Google's hosted Cloud Code backend; model selection is a server-side menu.

- The model transport is a **closed enum**: `invalid model API client type %q:
  must be 'ccpa', 'gemini', or 'stubby'` `[verified from source: agy 1.1.7 binary, 2026-08-20]`.
  There is no OpenAI/chat-completions client type to select.
- `GEMINI_BASE_URL`, `GOOGLE_GEMINI_BASE_URL`, and `GEMINI_API_KEY` are **all
  absent** from the binary; the `AGY_*` vars that exist are UI/telemetry knobs.
  The widely-repeated `GOOGLE_GEMINI_BASE_URL` tip is a **`gemini-cli`** fact
  that does not transfer — and even there it takes a Gemini-shaped endpoint, not
  an OpenAI-compatible one.
- Only two model hosts are baked in (`cloudcode-pa.googleapis.com` and its daily
  twin), with OAuth into `~/.gemini/antigravity-cli/antigravity-oauth-token`.
- The `CustomModelsConfig` / `MODEL_PRICING_TYPE_BYOK` strings are Windsurf
  heritage: `custom_models` is **desktop-IDE application state synced from the
  server**, and provider keys are held server-side. The CLI reads custom models
  it is *told about*; it cannot be told about one locally.

The only route is an unofficial proxy impersonating the Cloud Code API.
Reverse-engineered, unsupported, breaks on any backend change — **do not put it
in a recipe.**

---

## Part 2 — The server side: `llama-server`

Verified against llama.cpp `master` on 2026-08-20, current release build
`b10516`. llama.cpp tags a release per commit and has no semantic versions, so
"version" below means build number or merge date.

> [!CAUTION]
> llama.cpp's own `docs/function-calling.md` is roughly 18 months stale — it
> still claims "function calling is supported for all models" and still lists
> handler tables and response shapes that were deleted. It contradicts current
> source on `finish_reason`, tool-call shape, and the existence of a fallback.
> **Distrust it; read `tools/server/README.md` and the source instead.**

### Endpoint surface

`/v1` is **not** a global prefix — it is part of each literal route string, and
coverage is uneven (there is no `/v1/tokenize`, no `/v1/props`). Point a client
at `http://host:8080/v1` and everything an OpenAI client needs is there.

Routes that matter for this survey, read off registration in
`tools/server/server.cpp:234-292` `[inferred from source, 2026-08-20]`:

| Route | Notes |
|---|---|
| `/v1/chat/completions` | also bare `/chat/completions`, identical handler |
| `/v1/completions`, `/v1/embeddings` | bare variants exist but use **different handlers** (native shape vs OpenAI shape) |
| `/v1/models` | dual-shaped payload satisfying **both** OpenAI and Ollama clients at once |
| `/v1/responses`, `/v1/responses/input_tokens` | the Codex path; internally converted to chat completions |
| **`/v1/messages`, `/v1/messages/count_tokens`** | **Anthropic-compatible** — the Claude Code path |
| `/health`, `/v1/health` | the only auth-exempt routes |
| `/props` | `chat_template_caps` — the tool-support pre-flight |
| `/models/{load,unload,sse}` | router mode only |

`--api-prefix PREFIX` prepends to every route. **Sharp edge:** the auth-exemption
set is the hardcoded literals `/health` and `/v1/health` with **no prefix
applied**, so under a prefix your health endpoint starts demanding the API key —
which breaks naive container healthchecks
`[inferred from source: server-http.cpp:195-204, 2026-08-20]`.

### Tool calling: the part that changed most

**`--jinja` has defaulted to *enabled* since 2025-11-27** (PR
[#17524](https://github.com/ggml-org/llama.cpp/pull/17524)). Every guide telling
you to pass it — including several cited in this doc — is stale but harmless.
`--no-jinja` is the opt-out.

**The universal fallback is gone.** Until March 2026 llama.cpp had a "Generic"
handler that synthesized tool calling for *any* model, plus hardcoded per-family
handlers. PR [#18675](https://github.com/ggml-org/llama.cpp/pull/18675) (merged
2026-03-06) deleted all of it — Generic fallback, per-model handlers, and the
Minja polyfills — replacing them with a **differential autoparser** that renders
the model's Jinja template under varied inputs and generates a parser plus
constraining grammar. `common_chat_format` collapsed from a per-family
enumeration to five values.

The consequence: **tool calling now requires the model's own template to support
it natively.** A template the autoparser can't crack throws `Unable to generate
parser for this template`.

> [!TIP]
> **The pre-flight check that saves an afternoon**: `GET /props` returns
> `chat_template_caps` with booleans `supports_tools`,
> `supports_parallel_tool_calls`, `supports_system_role`, and others. If
> `supports_tools` is false, **no agent will work against that GGUF** no matter
> what you configure client-side.

Families with dedicated specialized parsers (tried before the autoparser)
include Qwen3-Coder, GPT-OSS, Ministral/Devstral-2, DeepSeek V3.2/V4, Kimi K2/K3,
MiniMax-M3, Gemma4, and Cohere2 MoE. **Qwen3-Coder is the safest pick for a
coding agent** — dedicated parser, and explicitly covered by the XML-style
streaming work. Llama 3.x, Hermes, and Mistral Nemo now go through the
autoparser; they should still work but are no longer specially tested.

Also: `parallel_tool_calls` is **off by default** and must be opted into
per-request; `grammar` and `tools` are **mutually exclusive**.

### Streaming

SSE fidelity is high and has been actively converged on the OpenAI spec.
Streamed tool calls are exactly OpenAI-shaped — `delta.tool_calls: [{index, id,
type, function:{name, arguments}}]` with `arguments` accumulating incrementally —
and `stream_options: {include_usage: true}` correctly emits a final chunk with an
empty `choices` array plus `usage`
`[inferred from source: server-task.cpp:464-520, 2026-08-20]`.

**Anything you have read about llama.cpp buffering tool calls to the end of the
stream predates a long fix chain**: PRs #12379 → #13800 → #16932 → #20177
(2026-03-07, true incremental streaming). This was genuinely weak and is now
fixed.

### Context and concurrency — the agent-killer

- **`-c/--ctx-size` now defaults to `0` = the model's trained context** (it used
  to be 4096, the classic agent-killing footgun). **`-np/--parallel` defaults to
  `-1` = auto.**
- **`--kv-unified` is on by default** when the slot count is auto, so a single
  request can use the whole context rather than `n_ctx / n_parallel`. This
  **inverts** the old "multiply your context by your slot count" advice. With an
  explicit `-np N` and `--no-kv-unified`, the old division applies — read the
  startup line `n_slots = N, n_ctx_slot = M, kv_unified = 'false'` for your real
  per-request budget.
- **Exceeding the slot context is a hard 400 — it does not truncate or shift**,
  and it does **not** use OpenAI's `context_length_exceeded` code. It surfaces as
  `type: "exceed_context_size_error"` with an **integer** `error.code` where
  OpenAI's is a string, which strict client SDK error parsers can choke on
  `[inferred from source: server-context.cpp:3126-3145, 2026-08-20]`. **No
  agent's auto-compaction logic will recognize it** — the turn just dies with an
  opaque API failure.
- Prompt caching is on by default, with host-memory caching via `-cram`
  (default 8192 MiB). `-sps/--slot-prompt-similarity` governs slot routing —
  relevant to agents, because it is what keeps a multi-turn loop pinned to the
  slot holding its warm 30k-token prefix.
- **Sizing:** budget `-c 32768` as a floor, 65536+ if the agent reads files into
  context. Set `-c` explicitly rather than relying on `0` — a long-context model
  (Qwen3-Coder trains to 256k) will try to allocate an enormous KV cache.

### Auth, model naming, reasoning

- **No key configured → validation is skipped entirely**, before any header is
  examined `[inferred from source: server-http.cpp:206-215, 2026-08-20]`. A client
  that insists on sending `Authorization: Bearer whatever` is fine — the header is
  never read. This is why every llama.cpp example uses `sk-no-key-required`.
- **While the model is loading, every route returns `503`.** Agents that probe
  `/v1/models` at startup and treat non-200 as fatal will fail against a cold
  server loading a 30 GB GGUF. Poll `/health` instead.
- **`model` is ignored in single-model mode but authoritative in router mode** —
  the same config string, two opposite failure modes (silently accepted, or a
  hard 400). **Pass `-a/--alias <stable-name>`** and configure every agent with
  that exact string; it costs nothing in single-model mode and is required in
  router mode.
- **Reasoning lands in `reasoning_content`**, the DeepSeek/vLLM convention — *not*
  an OpenAI field. A client reading only `delta.content` drops the model's
  thinking silently. If an agent renders empty assistant turns against a thinking
  model, set `--reasoning-format none` (thinking stays inline in `content`) or
  disable thinking via `--reasoning-budget 0`.

### Alternatives

All of these speak OpenAI-compatible `/v1`, so switching between them is a
base-URL-and-model-name change. **The differentiators are operational, not
protocol.**

| Option | Verdict |
|---|---|
| **llama-server router mode** | Launch with no `-m` and it loads/unloads models on demand keyed by `model`. **Try this before reaching for a proxy** — it is built in. |
| **llama-swap** | Better for process-level isolation, per-model flags, TTL unloading, and fronting non-llama.cpp backends. Go, single binary, in nixpkgs. |
| **Ollama** | Easiest path, but **disqualified for Claude Code** by the `count_tokens` 404 wedge. |
| **vLLM** | The production answer for real concurrency on NVIDIA+Linux. Python/CUDA closure; poor fit here. |
| **LM Studio** | Closed-source, so not Nix-packageable. GUI-first. |
| **text-generation-webui** | Least recommended for agent work; tool-calling support lags. |

### Deployment shape

Defaults are **port 8080** and **`--host 127.0.0.1`** (loopback-only). Run
llama-server on the **host**, where the GPU is; running it inside the container
means solving GPU passthrough for no benefit. The decision point is the bind
address, and it is the hinge for [Part 3's reachability
question](#reachability-can-the-jail-reach-a-host-llama-server). `--host` also
accepts a path ending in `.sock` for a Unix socket.

---

## Part 3 — The yolo side: what would carry this

**Prior art in this repo: none.** `rg 'ANTHROPIC_|OPENAI_|base_url|BASE_URL'`
over `internal/ packs/ cmd/` returns zero hits. Nothing configures a model or a
provider today.

### Mechanism verdict table

| Mechanism | What it does today | Fits? | What's missing |
|---|---|---|---|
| **Pack `config` surface (prism)** `internal/packdecl/kinds.go:77` | A pack owns one config file: path + codec + `defaults`/`managed` layers + optional Lua transform | **Yes — the vehicle for pi/opencode/codex** | Content is a static literal in `pack.json`; nothing lets user config reach it except via `derive.lua` |
| **`derive.lua`** `internal/agentcfg/luahook/derive.go` | Pre-merge producer: reads live tables, returns the `computed` layer | **Yes — the projection engine, already built** | `ctx` carries only `mcp_servers` and `lsp_servers` — a **closed, core-owned set** (`derive.go:180`) |
| **Pack `env` kind** `kinds.go:96-99` | Static literal env vars, emitted as `-e K=V` on the podman argv (`assemble.go:470-484`) | **Yes for Claude Code / Copilot** | **Jail-global, not per-agent**, despite being declared per-pack; literal strings only, undrivable from user config |
| **Pack `launch` kind** `kinds.go:100-101` | Injects flags after a binary at launch; **keyed by `bin`, so genuinely per-agent** | Partly | Static literals in `pack.json` |
| **`env_sources`** `internal/config/envsources.go:58-85` | Ordered dotenv files → `~/.config/yolo-user-env.sh`, sourced by `.bashrc` and hydrated into the entrypoint's env | **Works today** | Jail-wide, untyped, no per-agent scoping, no provenance |
| **`host_files`** `internal/config/hostfiles.go:73-75` | Brings any host file in as a composed surface. **`yolo config-ref` names `~/.pi/agent/models.json` as its own example** | **Yes — the zero-code answer today** | Hand-written per agent in each native dialect; N agents = N maintained blobs |
| **`network.forward_host_ports`** `internal/cli/run/hostports.go` | Host↔jail port hop over a bind-mounted Unix socket | **Yes — the transport answer** | Requires `socat` both sides; no scope gate (see [OQ-4](#oq-4)) |
| **Loophole** `internal/loopholedecl` | Manifest-declared host daemon, per-jail token, pinned TLS, typed settings, approval | **Overkill for transport; right for policy** | Only transport is `loopback-tls`; **no agent's HTTP client speaks that**, so it needs a *baked* jail-side shim — a core change, not a pack |

### The layer model, and the precedent that constrains this

Composition order, ascending
(`internal/agentcfg/compose.go:354-379`, `:445-489`):

```
defaults < host < workspace < config-overlay:<pack> < overlay(capture) < computed
        → transform (Lua, post-merge) → managed (re-enforced last, always wins)
```

`computed` — what `derive.lua` returns — sits **above** the captured in-jail
overlay on purpose: *regenerate, don't reconcile*.

The only interpolation anywhere is `${workspace}`, and it substitutes **map keys
only, never values**, with an explicit refusal to generalize
(`internal/agentcfg/builtin.go:32,43-46`).

> [!IMPORTANT]
> **The governing precedent** is `internal/entrypoint/mcp.go:33-62`. `${VAR}`
> interpolation *did* exist for MCP `env`/`headers`/`url` and was **removed by
> ruling on 2026-08-03**, for two reasons that apply verbatim here: an
> interpolated value has **no layer**, so nothing can attribute it; and it sourced
> config content from ambient process env at render time. The comment names the
> sanctioned alternative outright — *"the honest form is a **LAYER with
> provenance resolved at launch** — not a string substitution during render."*
> **Any `llm_endpoints` design that reaches for `${VAR}` is re-opening a closed
> decision.**

### The `ctx` diff, concretely

`ctx` is built in `internal/entrypoint/packsurfaces.go`, function `liveTables`
at `:239-244`:

```go
return map[string]map[string]any{
    manifest.SourceMCPServers: prismMap(e.LoadMCPServers()),
    manifest.SourceLSPServers: prismMap(LoadLSPServers(e)),
}
```

An `llm_endpoints` source would clone the road `mcp_servers` already walks, in
**six mechanical edits**:

1. known top-level key — `internal/config/config.go:63`
2. per-entry validation — `internal/config/validate.go`
3. onto the container — `internal/cli/run/assemble.go:605` (`-e YOLO_…`)
4. loaded in-jail — a `LoadLLMEndpoints` beside `internal/entrypoint/mcp.go:84`
5. into the table map — `packsurfaces.go:241`
6. one `manifest.Source*` const + one `knownDeriveSources` entry — `derive.go:180`

…then one `yolo.derive(...)` block per agent pack. Nothing structural changes.
`packs/pi` ships no `derive.lua` today, so pi would gain both its first derive
**and** a new config surface at `~/.pi/agent/models.json`.

### Reachability: can the jail reach a host llama server?

| Case | Works? | Conditions |
|---|---|---|
| **A.** `llama-server --host 0.0.0.0` | **Yes, out of the box** | `http://host.containers.internal:8080/v1`; `host.containers.internal` → `169.254.1.2` (`hostloopback.go:144`), forwarded to the host's global address. **Cost: the model server is on your LAN** — pair with `--api-key` |
| **B.** `llama-server --host 127.0.0.1` | **Not by default on a pasta host** | Needs the launcher's loopback forwarding to fire: rootless podman + non-macOS + unset/`bridge` network mode + a recognised `rootlessNetworkCmd` + that binary advertising the flag + `YOLO_NO_HOST_LOOPBACK` unset (`hostloopback.go:352-425,645-647,721-746`) |
| **C.** `network.forward_host_ports: [8080]` | **Yes — the clean answer** | Host `socat UNIX-LISTEN → TCP:127.0.0.1:8080`, socket dir bind-mounted, in-jail `socat TCP-LISTEN:8080 → UNIX-CONNECT`. **The hop is a Unix socket, not the network stack**, so none of Case B's conditions apply |

Case C is the recommendation: the agent dials plain `http://127.0.0.1:8080/v1`,
and the jail briefing already announces forwarded ports
(`internal/jailcontent/briefing.go:205-220`). It fails when `socat` is absent on
either side, when `network.mode: host` is set (warned and ignored), or when the
port is already bound in-jail (silently skipped). On macOS it degrades to
`YOLO_FWD_HOST_GATEWAY=host.containers.internal`, i.e. back to Case A/B
semantics.

**The FATAL reachability witness does not apply here.**
`ProbeServiceReachability` enumerates only `YOLO_SERVICE_<NAME>_ENDPOINT`
variables — yolo's own loophole endpoint files
(`internal/entrypoint/reachability.go:659-670`). A user's llama server is
invisible to it. That cuts both ways: no free diagnostic, but also no risk of
bricking a launch.

> [!WARNING]
> **Verification constraint.** A nested jail is **structurally blind** to Cases A
> and B — podman-in-podman forces `--net=host`, so the launcher's loopback and the
> jail's are the same loopback and the bug cannot reproduce (`AGENTS.md`
> CARVE-OUT; `hostloopback.go:24-25`). Case C *is* testable in-jail. Anything
> settled about A/B must be reported together with
> `podman info --format '{{.Host.RootlessNetworkCmd}}'` from a **real** rootless
> host.

### Loophole verdict

**Overkill for transport; the right vehicle only if you want policy at the
boundary.** What it would buy: host-side credential holding (an API key never
visible in the jail), per-jail bearer identity, typed `scope: "user"` settings a
workspace config can't widen, and auditing — `docs/guides/loopholes.md:15`
literally names `llm-audit` ("logs every inference request") as its canonical
hypothetical.

What it costs: `loopback-tls` is the **only** transport
(`internal/loopholedecl/enums.go:25-28,122`), and a pack-shipped loophole is
refused if it declares `publishes: "endpoint"`. **No agent's HTTP client speaks
pinned-TLS-plus-bearer-from-an-endpoint-file.** You would need a jail-side shim
re-exposing plain HTTP locally — and in-jail clients must be *baked*, meaning
`flake.nix` `shippedBinaries` **and** `scripts/stage-source-bundle.sh`. That is a
core change, not a pack.

**Recommendation: ship the projection over `forward_host_ports` first.** If an
`llm-gateway` loophole ever lands, the projection layer is unchanged — the derive
just emits a different `base_url`.

---

## Part 4 — What it would actually take

Three coherent options, in ascending cost.

### Option 1 — Document the recipe. Zero code, available today.

`network.forward_host_ports: [8080]` for transport, plus `host_files` entries
carrying each agent's native config, plus `env_sources` for the two env-var
agents. Everything already exists; `yolo config-ref` even uses
`~/.pi/agent/models.json` as its `host_files` example.

**Cost:** N agents = N hand-maintained blobs in N dialects, and the endpoint URL
is repeated in every one. **Verdict: do this first regardless** — it is a
doc-and-example commit, and it de-risks Option 2 by proving each agent's config
actually works before any Go is written.

### Option 2 — The `llm_endpoints` source + per-pack derives.

One canonical declaration in `yolo-jail.jsonc`; each pack's `derive.lua` projects
it into that agent's dialect. Six mechanical Go edits plus five Lua blocks.

**This is the design the prism was built for** — `packs/opencode/derive.lua` is
already a 20-line working example of exactly this shape for MCP servers.
**Verdict: the right target**, gated on the open questions below.

### Option 3 — An `llm-gateway` loophole.

Host-held credentials, per-jail identity, request auditing. **Verdict: defer.**
It requires a new baked in-jail client binary and does not change the projection
layer, so it composes cleanly on top of Option 2 later.

### A wrinkle worth naming up front

The env-var agents (Claude Code, Copilot) are the *cheapest* to deliver but sit
on the *weakest* mechanism: pack `env` is **jail-global**, not per-agent, despite
being declared per-pack (`-e` hits every process). In practice the variable names
are already agent-namespaced — `ANTHROPIC_*`, `COPILOT_PROVIDER_*` — so
collisions are unlikely.

> [!CAUTION]
> The one real hazard: **`ANTHROPIC_BASE_URL` also captures every nested Claude
> Code and Agent SDK process in the jail**, including the agent doing the work.
> Setting it jail-wide silently repoints the outer agent at the local model.
> Per-agent scoping is not cosmetic here.

---

## Fast-moving — verify before building

Ranked by how likely each is to be stale within three months.

1. **llama.cpp default flag values.** `--jinja`, `-c`, `-np`, `--kv-unified`,
   `-fa`, `--context-shift`, `--cache-ram` have **all** flipped defaults within
   the last ~12 months. Never trust a remembered default; run
   `llama-server --help`.
2. **Whether the autoparser handles your specific GGUF.** This is emergent
   behavior over a template, not a declared capability — a re-quantized GGUF with
   a tweaked template can change the answer. Test with one real `tools` request.
3. **The specialized-parser list.** New per-model parsers landed in June, July,
   and August 2026.
4. **llama.cpp PR #21174** (Codex Responses-API compliance) — open as of
   2026-08-20. If it merges, Codex moves from *shortlist* to *adopt*.
5. **Claude Code's `/v1/models` filter and byte-watchdog scope.** Both differ
   between the installed 2.1.220 and current docs (see UNCONFIRMED below).
6. **Codex's reserved provider-ID list.** Docs say `openai`/`ollama`/`lmstudio`;
   the binary also carries `amazon-bedrock` as a built-in.
7. **llama-server router mode** (`/models/*`, presets INI) — new enough that the
   endpoints and preset keys are still moving.
8. **`--tools`/`--agent`** are explicitly experimental **and a security landmine**
   (`exec_shell_command`, `write_file` over HTTP). Do not enable on anything
   network-reachable.

---

## Explicitly UNCONFIRMED

Carry these forward; do not build on them without re-checking.

- **No end-to-end request was made against a live llama-server for any agent.**
  The repo rule forbids agent API calls, so every config above is validated
  against shipped schemas and vendor examples, not observed traffic. **The single
  highest-value next step is one manual smoke test per agent.**
- Whether llama.cpp's `/v1/messages` accepts Claude Code's `cache_control`,
  `context_management`, or `output_config` fields without 400ing, and whether it
  emits `ping` events during long pauses.
- Open llama.cpp defect
  [#18613](https://github.com/ggml-org/llama.cpp/issues/18613) — "llama-server
  responds only with last token in Claude Code CLI", `bug-unconfirmed`, no
  diagnosis, unresolved as of 2026-08-20.
- Claude Code's settings-`env` allowlist (`n7t`) is source-derived from a minified
  bundle; Anthropic does not document it. Behaviour matches the docs' symptom
  description, but treat the exact scope semantics as inferred.
- `ANTHROPIC_UNIX_SOCKET`, `CLAUDE_CODE_USE_GATEWAY`,
  `CLAUDE_GATEWAY_ALLOW_LOOPBACK` are observed in the binary but **undocumented**.
- Byte-watchdog scope: 2.1.220's gate excludes custom base URLs; current docs say
  it applies to them. Almost certainly a version delta (2.1.223+), not re-verified.
- Whether a config-only opencode custom provider appears in `/models` with no
  `/connect` step. If it doesn't, run `/connect → Other` with a dummy key.
- Codex: whether the *"only supports changing `base_url`, `auth`, `http_headers`…"*
  restriction applies to built-in provider IDs only (the reading here) or to all
  entries. A 30-second `codex doctor` check settles it.
- Whether llama.cpp's context-overflow error string matches pi's compaction
  patterns. Given the non-OpenAI error type, **assume not**.
- pi's `samplingParams` appears in web results but is **absent from shipped
  0.82.1** — likely a fork or newer release. It would fail schema validation today.

---

## Open Questions

<a id="oq-1"></a>
💬 **OQ-1: Is `llm_endpoints` the thin version of B3, or a competing design?**
`docs/design/agent-auth-modes.md:60-72` argues that **a mode is a bundle**
(credential + env + model IDs) and that splitting them is exactly how a real
switch silently half-lands — that document was written from a measured
Bedrock→Teams miss. A bare `{base_url, model}` key re-creates that bug for local
models.
_Leaning:_ make local endpoints a **mode**, reusing the existing framing, rather
than a parallel key that will need reconciling later.

> **Answer:**

<a id="oq-2"></a>
💬 **OQ-2: Where does the API key live?**
Options: `env_sources` (today's answer — jail-wide, no provenance); an
`api_key_env` reference the derive emits for the agent to resolve; or a
`requires_env`-style gate that drops the endpoint when the key is absent
(`mcp.go:136-175`). A raw key in `yolo-jail.jsonc` would put a secret in a
repo-tracked, **agent-editable** file. Note that for a purely local llama-server
the answer may be "there is no key" — every agent here accepts a dummy.
_Leaning:_ `requires_env`-style gating, matching the MCP precedent; and document
that local endpoints normally need no key at all.

> **Answer:**

<a id="oq-3"></a>
💬 **OQ-3: Config scope — can a workspace config repoint inference?**
`mcp_servers` has no scope gate, so an `llm_endpoints` clone would be settable
from a workspace `yolo-jail.jsonc` — **a file the agent inside the jail can
rewrite**. Repointing an agent's inference at an attacker-chosen URL from a file
that agent controls is a real hazard. Compare the deliberate `scope: "user"` on
journal's `full` setting (`packs/journal/loopholes/journal/manifest.jsonc:84-97`).
_Leaning:_ **user-scope only.** The blast radius of getting this wrong is total.

> **Answer:**

<a id="oq-4"></a>
💬 **OQ-4: Is `forward_host_ports` missing a scope gate today?**
No scope gate was found (`internal/config/validate.go:479-496`), so a workspace
config can forward an arbitrary host `127.0.0.1` port into the jail. This looks
like a pre-existing hole **independent of this feature** and may deserve its own
fix regardless of what happens here.
_Leaning:_ file separately; do not couple it to this work.

> **Answer:**

<a id="oq-5"></a>
💬 🤷 **OQ-5: Ship order — recipe first, or build the source?**
Option 1 is honestly available this afternoon and de-risks Option 2. Option 2 is
what the prism exists for. They are not mutually exclusive, but they compete for
the same attention.
_Leaning:_ Option 1 now — with one manual smoke test per agent, since **nothing
in this doc has been exercised against a live server** — then Option 2 once the
per-agent configs are proven.

> **Answer:**

<a id="oq-6"></a>
💬 **OQ-6: Who owns five `derive.lua` blocks, and what about the path collision?**
Each agent pack needs its own projection. Two specific snags: config claims are
keyed by `agent/name`, **not by path**
(`internal/packload/footprint.go:231`), and a configured pack's surface path is
unreserved against `host_files` (`internal/config/hostfiles.go:704-708`) — so if
a pack starts owning `~/.pi/agent/models.json`, an existing `host_files` entry at
that path silently becomes a **second writer**. Worse, on this maintainer's own
machine that exact path is **already pack-managed and mounted `:ro`** by the
`matt-local` dotfiles pack.
_Leaning:_ resolve the two-writers question before shipping, not after.

> **Answer:**

---

## Sources

Grouped by what they settle. Vendor docs are cited only where the shipped
implementation agreed with them.

**Shipped implementations read directly** (the primary evidence for Part 1):

- `~/.local/share/claude/versions/2.1.220` — Claude Code's Bun-compiled bundle;
  source of the provider enum, the settings-`env` allowlist, and every
  `[verified from source]` claim in that section.
- `@github/copilot 1.0.48` `app.js` — contains the authoritative
  `copilot help providers` text **and** the BYOK implementation. Settles Copilot
  without a network call.
- `@openai/codex-linux-x64` `codex` (0.145.0) — source of the
  `wire_api = "chat"` removal message, the reserved-ID error, and the
  `ModelProviderInfo` field table.
- `@earendil-works/pi-coding-agent` 0.82.1 `dist/` — the compiled TypeBox schema
  at `core/model-config.js:112-176` is authoritative over the prose docs.
- `opencode-linux-x64@1.18.19` `bin/opencode` — proves the SDK adapter is
  in-binary, and is the source of every defaults claim.
- `~/.local/bin/agy` 1.1.7 — proves the *absence* of any base-URL/API-key var
  and the closed transport enum. A negative result, read from the binary.
- `~/.pi/agent/models.json` — the live, working custom-provider precedent on this
  machine; the best available template for the pi section.

**Upstream llama.cpp:**

- [`tools/server/README.md`](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md)
  — the canonical flag table; current and maintained.
- `tools/server/server.cpp:234-292` — the *actual* route registrations, more
  complete than the README's endpoint list.
- [PR #17570](https://github.com/ggml-org/llama.cpp/pull/17570) — the merge that
  added `/v1/messages`; **the finding that collapses the Claude Code problem**.
- [PR #18675](https://github.com/ggml-org/llama.cpp/pull/18675) — the autoparser
  refactor that deleted the universal tool-calling fallback.
- [PR #17524](https://github.com/ggml-org/llama.cpp/pull/17524) — `--jinja` on by
  default; why every guide telling you to pass it is stale.
- [PR #21174](https://github.com/ggml-org/llama.cpp/pull/21174) — **open**; the
  Codex-compatibility blocker.
- [Issue #18613](https://github.com/ggml-org/llama.cpp/issues/18613) — open
  Claude-Code-specific streaming defect.

**Vendor docs worth reading in full:**

- [llm-gateway-protocol](https://code.claude.com/docs/en/llm-gateway-protocol) —
  Anthropic's formal contract for what an endpoint must implement. The build spec
  if a shim is ever needed.
- [ggml-org's Anthropic-API announcement](https://huggingface.co/blog/ggml-org/anthropic-messages-api-in-llamacpp)
  — upstream's own Claude Code setup snippet.
- [opencode.ai/config.json](https://opencode.ai/config.json) — the authoritative
  JSON Schema; every opencode field name came from `$defs.ProviderConfig`.
- [opencode.ai/docs/providers](https://opencode.ai/docs/providers/) — carries a
  verbatim llama.cpp example.
- [Codex config reference](https://learn.chatgpt.com/docs/config-file/config-reference)
  and [discussion #7782](https://github.com/openai/codex/discussions/7782) — the
  Chat-Completions deprecation named by the binary's own error message.
- [antigravity.google/docs/cli/reference](https://antigravity.google/docs/cli/reference)
  — independently confirms no provider key exists.

**Deliberately distrusted:**

- llama.cpp `docs/function-calling.md` — still published and still linked from
  the server README, but its handler table and response examples date to 2024 and
  contradict current source. Flagged rather than cited.
