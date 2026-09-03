---
title: "Auth modes and cloud provider swapping — declarative profiles across agents"
date: 2026-08-29
status: accepted
tags: [auth, providers, config, prism, agents]
summary: "A unified design for declarative cloud provider switching (Anthropic Teams vs Bedrock, GLM, OpenAI) across agent packs via YOLO config and CLI flags, selective capability augmentation (Tavily), and in-jail YOLO permission parity."
---

# Auth modes and cloud provider swapping — declarative profiles across agents

**Status:** ACCEPTED (2026-08-29), expanded from the 2026-08-05 sketch.

> [!NOTE]
> **Vocabulary drift (2026-09-02).** This doc's spellings are the 2026-08-29 design as accepted;
> the implementation renamed several of them within days, and the provider-catalog work
> ([`providers.md`](../reference/providers.md)) supersedes parts of
> §4/§5. When the body below disagrees with the code, the code is right:
>
> - `agent_profiles` → `pack_profiles` (2026-08-31) → **`use_profiles`** (2026-09-02, `43d24e9e`);
>   the first two spellings are refused by name.
> - `--claude-auth` / `--auth` / `--agent-profile` were built and then **deleted** (`4f589610`);
>   the surviving spellings are `-p`/`--profile` (name-only since `886a9191`, OQ-PT5) and
>   `--pack-profile agent=profile,…`.
> - `api_key_env` → **`api_key_env_name`** (`8b24a67a`).
> - The `wire_api` values in §4.1 (`openai_completions`, `anthropic_bedrock`) were never members of
>   the shipped enum, which is canonical and closed: `anthropic`, `openai-chat-completions`,
>   `openai-responses` (`internal/packdecl/contributes.go:1473`, per 💬 18's OQ-PT1).
> - There is no Bedrock *bundle switching* (§5.1 item 1's shape): bedrock shipped as a
>   `kind: "provider"` + `kind: "profile"` pair inside `packs/claude/pack.json` (`4f589610`), and
>   model IDs are pinned in the **user's** `providers.bedrock.models`, never in a pack.

**The short version.** yolo-jail manages the agent development environment so users never have to hand-edit disparate native agent config files (`~/.pi/agent/models.json`, `~/.claude/settings.json`, `~/.codex/config.toml`, `.opencode.json`). A provider or auth mode is an atomic **bundle** ($$\text{credentials} + \text{endpoint} + \text{wire format} + \text{model IDs} + \text{env vars}$$). This doc specifies declarative cloud provider configuration in `yolo-jail.jsonc`, transient CLI swapping (`yolo --claude-auth=bedrock`, `yolo --agent-profile pi=glm`), projection via Prism (`derive.lua`), selective capability augmentation (e.g. Tavily search for agents lacking native search), and fixing in-jail vs. host-CLI auto-YOLO permission parity.

**Reads with:** [`agent-credentials.md`](agent-credentials.md) (boundary credential crossing), [`pack-system.md`](pack-system.md) (the layer model, `config-overlay`, and `derive.lua`), [`pack-config-collaboration.md`](pack-config-collaboration.md) (surface sharing), [`../research/local-model-endpoints.md`](../research/local-model-endpoints.md) (per-agent wire formats and BYOK surfaces), [`../plans/roadmap.md`](../plans/roadmap.md) (**💬 3**).

---

## 1. The shape of the gap, in one sentence

**yolo models one credential channel per agent and lacks a first-class way to declare and swap cloud providers or auth modes from YOLO config or the CLI — forcing users to hand-edit heterogeneous agent configuration files in different dialects.**

A mode switch is never just an API key: it is a credential *plus* an endpoint, a wire API dialect, model aliases, and environment variables that only make sense together. See §3.

---

## 2. Measured state — Bedrock, Teams, and the manual switch

This section was measured during a live switch from Bedrock to Claude Teams:

### 2.1 Before — Bedrock only

| Fact | Evidence |
|---|---|
| Subscription was not reaching jails: `~/.claude-shared-credentials/.credentials.json` was **0 bytes** | `wc -c` in a live jail; the symlink was correctly wired and pointed at an empty file |
| **Bedrock served everything**, via `env_sources` | `~/.config/yolo-user-env.sh` exported `CLAUDE_CODE_USE_BEDROCK`, `AWS_REGION`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` |
| Model pinned to a **Bedrock-shaped ID** | `model: "us.anthropic.claude-opus-5[1m]"` in host `settings.json`, composed through to the jail |
| The `env` block of host `settings.json` was **empty** | `jq '.env'` → `{}` |

### 2.2 After — Teams, and the credential half works

| Fact | Evidence |
|---|---|
| **The subscription path works end to end** | creds file is 555 bytes; `subscriptionType: "team"`, `rateLimitTier: "default_claude_max_5x"`, five `user:*` scopes, access token present |
| The `shared_credentials` hook did its job | `~/.claude/.credentials.json` → `../.claude-shared-credentials/.credentials.json`, intact, populated |
| Bedrock env is **gone** | `yolo-user-env.sh` now exports only `TAVILY_API_KEY` |

### 2.3 The residue — why manual switching fails

**The model pin remained `us.anthropic.claude-opus-5[1m]`** — the Bedrock-shaped, region-prefixed ID — in **both** host `settings.json` and the composed jail copy after switching to first-party Teams.

The switch was made in two places (`/login`, and the `env_sources` Bedrock block) and **missed a third**. The credential channel and the env moved together; the model names did not, because nothing tied them into one unit. Claude Code then issued first-party requests for a model ID that only exists on Bedrock, failing with obscure 404s.

This proves that **a mode is a bundle**, and that manual multi-file editing is fundamentally error-prone.

---

## 3. Core Principle: A mode is a BUNDLE

Flipping an auth token or `CLAUDE_CODE_USE_BEDROCK` alone is **not** a mode switch. Every provider requires a coherent bundle:

$$
\text{Provider Bundle} = \{\text{Auth Channel / Keys}, \text{Base URL / Wire API}, \text{Model IDs \& Aliases}, \text{Environment Variables}, \text{Capability Plugins / MCP}\}
$$

### Example Provider Bundle Differences

| Attribute | Claude (1st-Party Subscription) | Claude (AWS Bedrock) | pi / opencode (GLM Cloud) | pi / opencode (OpenAI Cloud) |
|---|---|---|---|---|
| **Credential** | OAuth token via shared broker | AWS IAM keys (`AWS_ACCESS_KEY_ID`…) | API Key (`GLM_API_KEY`) | API Key (`OPENAI_API_KEY`) |
| **Endpoint** | Default `api.anthropic.com` | AWS Bedrock Regional Endpoint | `https://open.bigmodel.cn/api/paas/v4` | `https://api.openai.com/v1` |
| **Wire API** | Anthropic Messages | Anthropic Bedrock SDK | OpenAI Chat Completions | OpenAI Chat Completions |
| **Model IDs** | Bare: `claude-opus-5` | Prefix: `us.anthropic.claude-opus-5[1m]` | `glm-4-plus`, `glm-4-flash` | `gpt-5-nano`, `gpt-4.1` |
| **Web Search** | Built-in native tool | **None** (requires Tavily MCP) | **None** (requires Tavily MCP) | **None** / Per-provider |

If you switch the credential without the model IDs and endpoint, the client fails. Therefore, **yolo must manage the bundle atomically**.

---

## 4. Declarative Provider Profiles in YOLO Config

In v1, yolo does not bake hardcoded provider presets into the binary. Instead, the **user-level config** (`~/.config/yolo-jail/config.jsonc`) is the source of truth where users declare their provider backends with full details, accompanied by canonical examples in documentation. Core does not ask the user to write JSON for pi, TOML for Codex, and JSONC for Claude.

### 4.1 Configuration Schema & Examples

In `~/.config/yolo-jail/config.jsonc` (or workspace `yolo-jail.jsonc`):

```jsonc
{
  // 1. Declare reusable cloud provider definitions
  "providers": {
    "glm": {
      "base_url": "https://open.bigmodel.cn/api/paas/v4",
      "wire_api": "openai-chat-completions",
      "api_key_env": "GLM_API_KEY",
      "models": {
        "default": "glm-4-plus",
        "fast": "glm-4-flash"
      }
    },
    "bedrock": {
      "wire_api": "anthropic",
      "region": "us-east-1",
      "models": {
        "default": "us.anthropic.claude-opus-5[1m]",
        "haiku": "us.anthropic.claude-3-5-haiku-20241022-v1:0"
      }
    },
    "deepseek": {
      "base_url": "https://api.deepseek.com/v1",
      "wire_api": "openai-chat-completions",
      "api_key_env": "DEEPSEEK_API_KEY",
      "models": {
        "default": "deepseek-coder",
        "reasoner": "deepseek-reasoner"
      }
    }
  },

  // 2. Active agent assignments (omitted agents use their first-party default)
  "agent_profiles": {
    "claude": "bedrock",       // or "teams" / "anthropic" (default)
    "pi": "glm",               // pi routes to GLM
    "codex": "default",        // OpenAI default
    "opencode": "default"
  }
}
```

> **CORRECTED 2026-09-02 — the `wire_api` spellings this example shipped with were never valid.**
> The original text held `openai_completions` (twice) and `anthropic_bedrock`. Neither was ever a
> member of any closed enum the tree enforced, and neither is a value any agent reads:
> `openai_completions` differs from pi's real value `openai-completions` by one underscore, and
> `anthropic_bedrock` names no protocol at all. The honest history is three steps. Until
> 2026-09-01 (`0bc29bd5`) `wire_api` carried no closed vocabulary — any spelling passed the
> validator's string-type check — so nothing accepted or rejected either value, and it crossed
> into the agents' config files verbatim, failing at first request. `0bc29bd5` introduced the
> first closed enum
> (`anthropic`, `openai-chat`, `openai-completions`, `responses`) and never held either
> underscore spelling; the same commit had to re-spell `internal/config`'s test fixture, which had
> used the same two values. `0f04632d` (2026-09-02) then replaced that four-value union of
> borrowed dialects with the **canonical protocol vocabulary** this example now uses —
> `anthropic`, `openai-chat-completions`, `openai-responses` — three names chosen to be
> **nobody's dialect** (defined: a name that names a protocol, never a value an agent's config
> file reads), so a value cannot pass through and work by accident
> ([`providers.md`](../reference/providers.md) §3.0a, OQ-PT1). Translation, not
> pass-through, is the contract: each derive maps canonical → its own agent's spelling and emits
> nothing for a protocol that agent cannot speak (§3.4) — which is also why the Codex row of §5.1
> still says `responses`: that is codex's own dialect, the derive's output, not yolo's input.
>
> Two other keys in this example are stale and left as written, since renaming them is not this
> note's job: `api_key_env` became `api_key_env_name` on 2026-09-01 (now an ordinary unknown key),
> and `agent_profiles` became `pack_profiles` on 2026-09-01 and `use_profiles` on 2026-09-02
> (still named by the validator, but only
> to emit its rename message) — see the key censuses in `internal/config/config.go`.

### 4.2 Launch-time CLI Swapping & Ergonomics

For transient runs or quick experimentation without modifying `yolo-jail.jsonc`:

```bash
# Concise single-agent launch (applies provider directly without redundant "pi=glm")
yolo -p glm -- pi
yolo --profile glm -- pi
yolo --auth bedrock -- claude

# Compound profile across multiple agents
yolo --profile glm-dev

# Explicit multi-agent assignment
yolo --agent-profile pi=glm,claude=bedrock
```

CLI flags override `yolo-jail.jsonc` values for that launch only.

### 4.3 Pre-existing Jails & Re-entry Behavior

When entering or executing into an existing, running container jail:
* **Env-var agents (Claude, Copilot)**: Injected via process environment (`-e`) on `podman exec` / launch, applying immediately per-session without mutating jail filesystem state.
* **File-based agents (pi, opencode, Codex)**: Prism renders all declared `providers` into `models.json` / `opencode.json` at staging/boot time. Launching with `-p glm -- pi` selects the active provider via launch flags or runtime environment (`PI_DEFAULT_PROVIDER=...`) without rewriting persistent disk files. If a new provider definition is added to config, `yolo check` or container reload regenerates the surface.

---

## 5. Projection via Prism (`derive.lua`) and Core

Core validates the provider declarations and feeds the resolved provider data into the Prism layer:

```
┌─────────────────────────────────────────────────────────────┐
│  yolo-jail.jsonc ("providers", "agent_profiles")            │
│  + CLI flags (--claude-auth, --agent-profile, --profile)    │
└──────────────────────────────┬──────────────────────────────┘
                               │ Core Resolution & Validation
                               ▼
┌─────────────────────────────────────────────────────────────┐
│  Prism Live Tables: ctx.providers, ctx.active_profiles      │
└──────────────────────────────┬──────────────────────────────┘
                               │ derive.lua (Per-Pack Projections)
         ┌─────────────────────┼─────────────────────┐
         ▼                     ▼                     ▼
┌─────────────────┐   ┌─────────────────┐   ┌─────────────────┐
│ packs/claude:   │   │ packs/pi:       │   │ packs/opencode: │
│ env vars &      │   │ renders         │   │ renders         │
│ .claude.json    │   │ models.json     │   │ opencode.json   │
└─────────────────┘   └─────────────────┘   └─────────────────┘
```

### 5.1 Per-Agent Projection Mechanisms

1. **Claude Code**:
   - `subscription`: Uses `shared_credentials` symlink, OAuth broker relay, bare model IDs (`claude-opus-5`), no `AWS_*` env vars.
   - `bedrock`: Injects `CLAUDE_CODE_USE_BEDROCK=1`, `AWS_REGION`, model aliases (`ANTHROPIC_DEFAULT_OPUS_MODEL`), and Bedrock IAM credentials via `env_sources`.
2. **pi**:
   - `packs/pi/derive.lua` renders all defined providers into `~/.pi/agent/models.json` (OpenAI completions format with appropriate `compat` flags and `baseUrl`).
3. **opencode**:
   - `packs/opencode/derive.lua` renders all defined providers into `~/.config/opencode/opencode.json` using the built-in `@ai-sdk/openai-compatible` adapter.
4. **Codex CLI**:
   - `packs/codex/derive.lua` renders `[model_providers.<name>]` in `~/.codex/config.toml` with `wire_api = "responses"`.
5. **agy**:
   - Closed transport enum (`ccpa`, `gemini`, `stubby`); remains on Google Cloud Code.

### 5.2 Secret Discipline

* API keys are never written in cleartext into `yolo-jail.jsonc`.
* The `api_key_env` declaration specifies which environment variable holds the key (e.g. `GLM_API_KEY`, `DEEPSEEK_API_KEY`).
* Secrets are supplied through `env_sources` or the host shell environment, and gated via `requires_env`. Active provider keys must be resolvable at pre-flight; unresolvable keys for an active profile refuse the launch.

---

## 6. Capability Resolution & Selective Tool Augmentation (The Web Search Pattern)

### 6.1 Principles: Name the Job, Avoid Special Cases

To avoid brittle, hardcoded special cases (`if agent == "agy"` or `if tool == "tavily"`), yolo uses a declarative **Capability Resolution Contract**:
1. **Agents / Modes declare native capabilities** in `pack.json` or provider blocks (e.g., `capabilities: ["web_search"]`).
2. **MCP servers declare what capability they fulfill** (e.g., `provides: "web_search"`).
3. **Core applies a single universal rule:** *If the active agent/mode already has capability $$C$$, omit any MCP server that provides $$C$$.*

### 6.2 Capability Resolution Rules

* **Native Search**:
  * Claude Code (first-party subscription / Teams) and `agy` have native search tools. When running these, any MCP server declaring `provides: "web_search"` is **automatically suppressed**.
* **Augmentation**:
  * Claude Code (Bedrock), `pi`, `opencode`, `codex`, and offline `copilot` lack native search. They automatically receive the configured `provides: "web_search"` MCP server (e.g. `tavily`) whenever `TAVILY_API_KEY` is present.
* **Collision Policy (Multiple Providers)**:
  * If multiple MCP servers in config declare `provides: "web_search"` (e.g., both `tavily` and `brave`), yolo **refuses the launch with a fatal config error**. Ambiguous capability provision is never resolved by silent arbitrary selection.
* **Required Capabilities & Missing Tool Refusal**:
  * Core defaults to requiring baseline capabilities every agent meets: `["code_editing", "command_execution"]`.
  * **`web_search` is NOT required by default.**
  * If a project or profile explicitly declares `"required_capabilities": ["web_search"]`, and neither the active agent nor any enabled MCP server satisfies it, yolo **refuses the launch with a fatal error** naming the missing capability.

---

## 7. In-Jail vs Host-CLI Parity: Cleaning up Auto-YOLO Mode

### 7.1 The Asymmetry Bug Diagnosed

There is currently a behavioral discrepancy between launching an agent from the host vs. typing its name in an interactive in-jail shell:

* **Host CLI (`yolo -- claude`)**:
  [`internal/cli/run/run.go:125`](../../internal/cli/run/run.go#L125) calls `packload.InjectLaunchFlags()`, which checks `p.Decl.PostureFor(true).Launch` and injects `--dangerously-skip-permissions`.
* **In-Jail Shell (`claude` inside `yolo -- bash`)**:
  [`internal/entrypoint/shell.go:packAliases`](../../internal/entrypoint/shell.go#L27-L34) generates `.bashrc` shell aliases, but calls `p.Decl.LaunchFlagContributions()`. That helper **only inspects top-level `launch` contributions and skips the `autonomy` block**.
  Because Claude's and agy's flags are declared under `autonomy.autonomous.launch`, no alias is emitted. Furthermore, the lazy launcher in `~/.yolo-launchers/claude` only runs `exec "$REAL_BIN" "$@"`.

**Consequence:** `yolo -- claude` runs in autonomous YOLO mode, but running `claude` inside `yolo -- bash` prompts for permissions.

### 7.2 The Fix

1. **Fix `packAliases` in `shell.go`**: Call `packload.LaunchFlagsFor(packs, true)` so that `.bashrc` emits `alias claude='claude --dangerously-skip-permissions'` and `alias agy='agy --dangerously-skip-permissions'`.
2. **Launcher Script Default**: Ensure lazy launchers in `~/.yolo-launchers/` forward the pack's autonomous launch flags so non-interactive or non-aliased subshells also behave consistently.

---

## 8. Dynamic Overflow: What is reachable and what is not

The original ask proposed "subscription primary, with automatic overflow to Bedrock on rate limit."

### Why automatic dynamic failover is deferred:

1. **Rate limits are opaque to the boundary**: The HTTP 429 and `retry-after` are received directly by the agent binary inside the container. Core sees no boundary signal.
2. **Rate limits are per-model-bucket**: An Opus 429 does not mean Sonnet or Haiku is exhausted. A global failover switch would prematurely migrate unaffected models.
3. **Mid-session state**: Claude Code reads credentials at startup; changing credentials mid-flight without process restart leads to credential collisions and invalid sessions.

**Verdict:** Launch-time selection (`use_profiles` in config — spelled `agent_profiles` when this
was written — and the `-p`/`--pack-profile` flags) is deterministic, safe, and solves 95% of the
requirement without fragile in-jail interceptors.

### 8.1 Measured 2026-09-02: the subscription bearer follows `ANTHROPIC_BASE_URL`

The question the roadmap carried as *"auth OQ-1"* — does Claude Code send a subscription OAuth
bearer to a non-Anthropic base URL? — **is answered: yes, unconditionally.** Method: a loopback
HTTP listener on `127.0.0.1:18923` returning 503 `overloaded_error` (no 401, so no refresh path
was triggered); then `ANTHROPIC_BASE_URL=http://127.0.0.1:18923 claude -p 'say hi'` in a jail with
a saved **team** subscription login (`sk-ant-oat01…`) and neither `ANTHROPIC_AUTH_TOKEN` nor
`ANTHROPIC_API_KEY` set. `claude-cli/2.1.220` sent 8/8 requests to
`POST /v1/messages?beta=true` on the listener, each carrying `Authorization: Bearer sk-ant-oat0…`
and **no** `x-api-key`, retrying through the 503s. Three consequences:

1. **A config-writable base URL is a credential-exfiltration channel for subscription auth even
   when no API key is configured anywhere.** §9's scope-isolation trap is a measured fact, not a
   caution: whoever can set `ANTHROPIC_BASE_URL` (a provider entry, a `config-overlay`, a raw env
   var) receives the OAuth bearer of a logged-in claude. The zai pack's wiring is safe for a
   *different* reason — it sets `ANTHROPIC_AUTH_TOKEN`, which overrides the saved login
   ([`local-model-endpoints.md`](../research/local-model-endpoints.md) §env-var table) — so the
   safety is the token override, not any endpoint check in the client.
2. **[`boundary-broker.md`](boundary-broker.md) B2's mechanism is viable without client changes:**
   a broker/proxy can be interposed by base URL alone and will receive claude's real
   authentication. (Whether B2 is *wanted* is still open; this settles only that it can work.)
3. It also settles [`claude-oauth-refresh-mechanics.md`](../research/claude-oauth-refresh-mechanics.md)'s
   doubt that `BASE_API_URL` might be hardcoded in prod bundles: the env var is honored — the
   requests landed on the listener.

---

## 9. Traps and Failure Modes

* **Never export a blank `ANTHROPIC_API_KEY=""`**: A blank variable takes precedence in SDK credential resolution and attempts authentication with an empty key. Variables must be unset, never blank.
* **Single-use Refresh Tokens**: Anthropic OAuth refresh tokens are single-use. If two jails attempt to refresh concurrently without serialization, tokens are permanently burned. The Claude OAuth broker singleton remains mandatory for subscription mode.
* **Scope Isolation**: `providers` and `use_profiles` must be **user-scope only** (or gated by the config-approval flow). An in-jail agent or untrusted workspace repo must never be able to silently redirect inference endpoints to an attacker-controlled server. **This is measured, not hypothetical** — §8.1 shows a redirected base URL receives the full subscription bearer with no other misconfiguration required.
* **Wire API Incompatibilities**: Codex only speaks `wire_api = "responses"` (Chat Completions was removed upstream). pi and opencode speak OpenAI Chat Completions. Ensure `derive.lua` converts provider endpoints into the exact wire format expected by the target agent.

---

## 10. Order of Work

> [!NOTE]
> **Status 2026-09-02: every step below has shipped**, several under different spellings (see the
> vocabulary note at the top). Step 1 landed in the same commit that published this doc
> (`937bddf2`); step 5 landed as the provider+profile pair in `packs/claude` (`4f589610`), not as
> bundle switching; step 7's flags were built and then deleted — `-p`/`--profile` (name-only) and
> `--pack-profile` are what survives. This list is kept as the record of the plan, not as work.

1. **Fix in-jail auto-YOLO alias parity (§7)**: Update `packAliases` in `internal/entrypoint/shell.go` to use `LaunchFlagsFor(packs, true)`.
2. **Define `providers` and `agent_profiles` config schema (§4)**: Add typed schema and validation in `internal/config/`.
3. **Extend Prism `ctx` table**: Pass active providers and agent assignments into `liveTables` in `internal/entrypoint/packsurfaces.go`.
4. **Implement `derive.lua` projections for pi, opencode, and Codex (§5)**: Update pack derive scripts to project provider definitions into `models.json`, `opencode.json`, and `config.toml`.
5. **Implement Claude Bedrock/Teams bundle switching (§5)**: Support `claude_auth` / `agent_profiles.claude` injecting the proper env vars, model IDs, and broker wiring.
6. **Implement Capability-based MCP filtering (§6)**: Add search-tool suppression in `agy` and Claude subscription mode, while passing Tavily to Bedrock and other agents.
7. **Add CLI flags (§4.2)**: Wire `--claude-auth`, `--agent-profile`, and `--profile` flags in `internal/cli/run/`.

---

## 11. Open Questions

One — carried over from the pre-rewrite doc under its original ID, because the 2026-08-29 rewrite
dropped it without answering it (the roadmap and sibling docs cited it as `auth OQ-9`):

1. 💬 **OQ-9: AWS's two-part credential has no declarative home.** The provider vocabulary carries
   a single credential pointer (`api_key_env_name`, hydrated as `{key}`), and `packs/claude`'s
   `bedrock` provider declares only `AWS_REGION` — so `AWS_ACCESS_KEY_ID` + `AWS_SECRET_ACCESS_KEY`
   still flow through `env_sources` exactly as §2.1 measured. That works today and is not blocking
   anything; the question is whether a credential *pair* ever gets first-class declaration or
   whether `env_sources` is the permanent answer.

   _Leaning:_ leave it on `env_sources` until a second multi-var credential shows up. The
   provider-catalog work (💬 19's OQ-CS8) is moving env composition into per-agent env derives,
   which can read whatever the environment holds — that likely absorbs this question rather than
   answering it, and deciding it now would design against a moving surface.

   **Answer:**
   > _(empty — fill in when decided)_

---

## 12. Decision Ledger

| ID | Ruling / Decision | Date | Settled in |
| :--- | :--- | :--- | :--- |
| **OQ-1** | **Launch-time selection is sufficient for v1.** Dynamic in-session failover deferred due to opacity of 429s and per-model limits. | 2026-08-29 | §8 |
| **OQ-2** | **Config must be complete before launch.** Required keys for active profiles must resolve at pre-flight; unresolvable active keys refuse launch. | 2026-08-29 | §5.2 |
| **OQ-3** | **User-level config is source of truth in v1.** No binary presets baked; `~/.config/yolo-jail/config.jsonc` defines providers with canonical doc examples. | 2026-08-29 | §4 |
| **OQ-4** | **Support both compound profiles and concise per-agent CLI overrides.** `yolo -p glm -- pi` applies `glm` directly to `pi` without redundant `pi=glm` syntax. | 2026-08-29 | §4.2 |
| **OQ-CAP1** | **Fatal refusal on multiple MCP capability collision.** If two MCP servers declare the same `provides`, core refuses launch. | 2026-08-29 | §6.2 |
| **OQ-CAP2** | **Fatal refusal on unmet `required_capabilities`.** `web_search` is opt-in; if required and unsatisfied, launch refuses. | 2026-08-29 | §6.2 |
| **OQ-5** | **NO pack→pack composition.** `requires_pack` and `conflicts` retired — flat `packs` list is whole story. | 2026-08-13 | §11.1, [`retired-decisions.md`](retired-decisions.md) Thread A |
| **OQ-8** | **Generalize transport into loophole framework.** `loopback-tls` becomes the framework's only transport; unix-socket retired. | 2026-08-13 | [`loophole-transport.md`](loophole-transport.md) §7.3–§7.4 |
| **OQ-K1** | **Declarations are authoritative.** Core validates pack-declared settings offline. | 2026-08-18 | [`pack-config-keys.md`](pack-config-keys.md) §2.2 |
| **OQ-K3** | **Freeze `host_processes.visible`.** Require restart for config changes. | 2026-08-18 | [`pack-config-keys.md`](pack-config-keys.md) §5.1 |
