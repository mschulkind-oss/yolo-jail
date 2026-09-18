-- config (~/.claude.json, RMW): the mcpServers managed table — a passthrough of the
-- servers this launch is eligible for.
--
-- The suppression that used to live here is GONE, and not because it was unwanted: which
-- MCP servers a launch needs is a fact about the active AUTHENTICATION SOURCE, and core
-- resolves it before ctx.mcp_servers is exposed (capability-driven MCP delivery). This
-- derive's own version asked "is the profile bedrock or codex?", which is a different
-- question with the same answer for exactly two profiles — it suppressed web search for
-- every OTHER profile too, so a Kilo launch lost its search MCP to a source that has no
-- native search. The declarations replacing it: packs/claude's program contribution says
-- the claude subscription performs web_search, and each provider says for itself.
yolo.derive("claude", "config", function(ctx)
  local servers = {}
  for name, cfg in pairs(ctx.mcp_servers or {}) do
    servers[name] = cfg
  end
  return { mcpServers = servers }
end)

-- settings (~/.claude/settings.json): two derivations.
--  1. tombstone mcpServers — MCP belongs in .claude.json, so strip any host
--     settings.json copy. (Was computed[] {to: mcpServers, tombstone: true}.)
--  2. enabledPlugins — enable the LSP plugin for each language whose LSP is
--     configured; tombstone the others so a stale enable is removed. (Was
--     computed[] flags whenPresent x3.)
--  3. env.ENABLE_LSP_TOOL — "1" when ANY LSP is configured, else tombstone.
--     (Was computed[] flags whenAny.)
yolo.derive("claude", "settings", function(ctx)
  local plugin = {
    python     = "pyright-lsp@claude-plugins-official",
    typescript = "typescript-lsp@claude-plugins-official",
    go         = "gopls-lsp@claude-plugins-official",
  }
  local enabled = {}
  for lang, id in pairs(plugin) do
    enabled[id] = ctx.lsp_servers[lang] and true or ctx.tombstone
  end
  local out = {
    mcpServers = ctx.tombstone,
    enabledPlugins = enabled,
    env = { ENABLE_LSP_TOOL = next(ctx.lsp_servers) and "1" or ctx.tombstone },
  }
  -- Claude Code's picker accepts exact gateway model IDs.  The Codex Responses
  -- bridge likewise sends model IDs unchanged, so expose the subscription
  -- catalog directly instead of asking users to infer a Claude tier alias.
  -- Replacing the built-ins prevents retired pre-5.6 choices from leaking into
  -- a Codex-profile launch; `Default` resolves to ANTHROPIC_MODEL below.
  if ctx.selected_provider == "openai-codex" then
    -- The client retains a hard-coded Default row. Constraining Default makes
    -- it resolve to the first allowlisted ID (Terra), while modelPicker keeps
    -- the user-facing choice order below independent of that fallback rule.
    out.availableModels = {
      "gpt-5.6-terra",
      "gpt-6-astra",
      "gpt-5.6-sol",
      "gpt-5.6-luna",
    }
    out.enforceAvailableModels = true
    out.modelPicker = {
      options = {
        { model = "gpt-6-astra",   label = "GPT-6 Astra",   description = "Frontier" },
        { model = "gpt-5.6-sol",   label = "GPT-5.6 Sol",   description = "Most capable" },
        { model = "gpt-5.6-terra", label = "GPT-5.6 Terra", description = "Balanced" },
        { model = "gpt-5.6-luna",  label = "GPT-5.6 Luna",  description = "Fast" },
      },
      replaceBuiltInOptions = true,
    }
  end
  return out
end)

-- env: the provider environment claude's own process launches with. The variable NAMES
-- are Claude Code's facts, not any provider's, so the binding lives HERE — in the agent
-- pack, where a rename of what claude reads is one edit — rather than in a manifest
-- vocabulary every provider would restate (docs/reference/providers.md §3.1,
-- OQ-CS8). The producer reads the composed table; a selected provider's api_key arrives
-- hydrated for this invocation only, and never crosses in the wire table itself (D8).
-- An input that is absent drops its variable: an empty base URL is a request to the
-- wrong host, and an empty token is a credential that gets SENT.
yolo.env("claude", function(ctx)
  local p = ctx.providers[ctx.selected_provider]
  -- openai-codex is a broker-backed subscription identity, like Pi's native
  -- provider. Its YOLO_PROVIDERS row (packs/openai-auth) carries the source's
  -- `capabilities` and deliberately nothing else — no endpoint, no credential
  -- pointer — because the wire bridge gets its short-lived access-token view from
  -- openai-auth, never from a generated configuration file. So `p` is a table with
  -- no address in it, and every value below is stated here rather than read off it.
  -- The Responses subscription endpoint accepts concrete Codex model IDs, so this
  -- must stay aligned with Pi's Codex default.
  if ctx.selected_provider == "openai-codex" then
    return {
      ANTHROPIC_BASE_URL = "http://127.0.0.1:8215",
      -- Pin a fresh Codex-profile chat and every unassigned subagent to the
      -- stable balanced model. The picker above remains available for an
      -- intentional per-session choice.
      ANTHROPIC_MODEL = (ctx.profile and ctx.profile.model) or "gpt-5.6-terra",
      CLAUDE_CODE_SUBAGENT_MODEL = (ctx.profile and ctx.profile.model) or "gpt-5.6-terra",
      -- Claude Code retains its own Default row even when custom picker
      -- options replace the built-ins. Pin and label that row as Terra too,
      -- so choosing it cannot silently return to the Claude subscription
      -- model advertised by the local client.
      ANTHROPIC_DEFAULT_OPUS_MODEL = "gpt-5.6-terra",
      ANTHROPIC_DEFAULT_OPUS_MODEL_NAME = "GPT-5.6 Terra",
      ANTHROPIC_DEFAULT_OPUS_MODEL_DESCRIPTION = "Balanced (default)",
      -- These are the Responses models' real 1.05M-token context capacity and
      -- Claude Code's documented 1M maximum proactive-compaction threshold.
      -- Stating both is necessary for an unrecognised custom model ID.
      CLAUDE_CODE_MAX_CONTEXT_TOKENS = "1050000",
      CLAUDE_CODE_AUTO_COMPACT_WINDOW = "1000000",
      CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC = "1",
    }
  end
  if not p then return {} end
  local out = {}
  local routed = false -- claude is pointed at a non-first-party Anthropic-wire host
  local baseUrl = nil
  if p.endpoints and p.endpoints.anthropic and p.endpoints.anthropic.base_url then
    baseUrl = p.endpoints.anthropic.base_url
  elseif p.base_url then
    -- Shorthand base_url fallback (strip trailing /v1 or /v1/ as Claude Code requires the origin)
    local u = p.base_url
    if string.sub(u, -3) == "/v1" then
      u = string.sub(u, 1, -4)
    elseif string.sub(u, -4) == "/v1/" then
      u = string.sub(u, 1, -5)
    end
    baseUrl = u
  end
  if baseUrl then
    out.ANTHROPIC_BASE_URL = baseUrl
    routed = true
  end
  -- A CREDENTIAL TRAVELS WITH THE ADDRESS IT WAS MINTED FOR, OR NOT AT ALL — and this
  -- producer no longer has to check, because the state is now UNREACHABLE.
  --
  -- Two provider shapes reach here. One names an anthropic endpoint and is ROUTED, so its
  -- key belongs with that URL. One names NO endpoint at all and has repointed nothing —
  -- the deliberate BYO-key launch against Anthropic's own API, whose key is correct. The
  -- third shape, a provider that NAMED a protocol and did not name ours, used to arrive
  -- here and have its key composed with no base URL beside it, sending a third-party
  -- credential to api.anthropic.com (OQ-2). It was guarded here from 2026-09-18 and the
  -- guard said it was INTERIM: protocol-resolution.md's resolver refuses that pairing
  -- before any derive runs (packload.ResolveProtocol, above AgentEnv's producer call), so
  -- the shape cannot reach this line at all and the branch is deleted rather than reworked.
  --
  -- The inverse stays below — `elseif routed` substitutes a dummy so a routed launch is
  -- never keyless.
  if p.api_key then
    out.ANTHROPIC_AUTH_TOKEN = p.api_key
  elseif routed then
    -- Routed/local endpoint with no explicit API key needs a dummy token so Claude
    -- does not fail or leak the private claude.ai subscription OAuth token.
    out.ANTHROPIC_AUTH_TOKEN = "local"
  end
  if p.region then
    out.AWS_REGION = p.region
  end
  -- Provider FACTS reach the derive as profile options (OQ-CS4: the provider declares
  -- the knobs, this derive decides what each one means for claude), so the values stay
  -- the provider's while the variable names stay claude's:
  --   context_window (tokens) -> the auto-compact threshold, so a 1M-context model
  --     does not compact at claude's default window (verified against claude 2.1.259:
  --     CLAUDE_CODE_AUTO_COMPACT_WINDOW is read and "takes precedence"), plus
  --     CLAUDE_CODE_MAX_CONTEXT_TOKENS so Claude Code knows the model's capacity;
  --   api_timeout_ms -> claude's per-request ceiling, for providers whose reasoning
  --     turns run long, plus CLAUDE_STREAM_IDLE_TIMEOUT_MS to prevent streaming drops.
  local isKilo = (ctx.selected_provider == "kilo" or (type(p) == "table" and type(p.endpoints) == "table" and type(p.endpoints.openai) == "table" and string.find(p.endpoints.openai.base_url or "", "api%.kilo%.ai") ~= nil) or (type(p) == "table" and type(p.base_url) == "string" and string.find(p.base_url, "api%.kilo%.ai") ~= nil))

  local function normalizeKiloModel(modelId)
    if type(modelId) ~= "string" or modelId == "" then return modelId end
    if string.find(modelId, "^deepseek%-") and not string.find(modelId, "/") then
      return "deepseek/" .. modelId
    end
    return modelId
  end

  local profileOpts = (type(ctx.profile) == "table" and ctx.profile) or {}
  local provOpts = (type(p) == "table" and type(p.options) == "table" and p.options) or {}
  local cw = profileOpts.context_window or profileOpts.max_context_tokens or provOpts.context_window or provOpts.max_context_tokens
  if not cw and isKilo then
    cw = "1048576"
  end
  if cw then
    out.CLAUDE_CODE_MAX_CONTEXT_TOKENS = tostring(cw)
    out.CLAUDE_CODE_AUTO_COMPACT_WINDOW = tostring(cw)
  end
  local timeout = profileOpts.api_timeout_ms or provOpts.api_timeout_ms
  if timeout then
    out.API_TIMEOUT_MS = tostring(timeout)
  end
  local streamTimeout = profileOpts.stream_idle_timeout_ms or timeout
  if streamTimeout then
    out.CLAUDE_STREAM_IDLE_TIMEOUT_MS = tostring(streamTimeout)
  end

  -- The model ids the provider declares are WIRE-TRUE — every agent's catalog sends
  -- them verbatim, and z.ai's routes reject claude-only spellings (measured
  -- 2026-09-04: "glm-5.3[1m]" is a 400 on both routes; pi and opencode have no
  -- [1m] handling to strip one). "[1m]" is CLAUDE CODE's syntax: it strips the
  -- suffix client-side and sends the context-1m beta in its place, which is how a
  -- 1M-context model gets its full window. So this derive alone re-spells the ids
  -- claude uses, from the provider's own context_window fact: a provider declaring
  -- a 1,000,000-token window gets the beta requested; anything smaller gets the
  -- bare id.
  local cwNum = tonumber(cw or "")
  local suffix = (cwNum and cwNum >= 1000000) and "[1m]" or ""
  if routed then
    -- Z.AI's recommended Claude Code config disables claude's nonessential traffic
    -- (telemetry, update checks) on a routed launch: that traffic targets
    -- api.anthropic.com, which either fails or leaks through a third-party gateway.
    -- First-party and Bedrock launches keep it (Bedrock's gated env owns that mode).
    out.CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC = "1"
  end
  local m = p.models or {}
  -- OPUS takes the alias the active profile's `model` option names (OQ-CS4: what an
  -- option means is the derive's business), falling back to the provider's declared
  -- `default` when the profile carries none (OQ-CS3). SONNET and HAIKU keep their own
  -- aliases: they are Claude's routing names inside the same provider, not a selection
  -- surface, and no profile option speaks for them. Declaring them matters even though
  -- z.ai translates claude's own tier names server-side (measured 2026-09-04:
  -- claude-sonnet-* serves as glm-5.3-flash — the FAST model), because the aliases pin
  -- each tier to the model the provider actually intends for it.
  local alias = (ctx.profile and ctx.profile.model) or (type(p) == "table" and type(p.options) == "table" and p.options.model) or "default"
  local selected = m[alias] or m["default"]
  if not selected and alias ~= "default" then
    selected = alias
  end
  if selected then
    if isKilo then
      selected = normalizeKiloModel(selected)
    end
    out.ANTHROPIC_MODEL = selected .. suffix
    out.ANTHROPIC_DEFAULT_OPUS_MODEL = selected .. suffix
    -- A curated provider can publish real picker IDs instead of Claude-tier aliases.
    -- Keep all tiers on the selected model rather than falling back upstream.
    local sonnet = m.sonnet or selected
    local haiku = m.haiku or selected
    if isKilo then
      sonnet = normalizeKiloModel(sonnet)
      haiku = normalizeKiloModel(haiku)
    end
    out.ANTHROPIC_DEFAULT_SONNET_MODEL = sonnet .. suffix
    out.ANTHROPIC_DEFAULT_HAIKU_MODEL = haiku .. suffix
    out.ANTHROPIC_SMALL_FAST_MODEL = haiku .. suffix
  end
  return out
end)
