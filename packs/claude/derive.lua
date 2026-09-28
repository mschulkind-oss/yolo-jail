-- in_full declares a table this derive regenerates in full — ctx.in_full, the CO13 sentinel
-- (docs/design/config-ownership-and-promotion.md). A table returned WITHOUT it is one this
-- derive only asserts leaves of, and yolo claims nothing under it but those leaves. The
-- fallback is for an entrypoint older than the sentinel, which treated every non-empty
-- table as regenerated in full anyway.
-- It covers that direction ONLY: a NEWER entrypoint handed an OLDER copy of this file (packs
-- are staged from the host yolo's embed) sees no declaration and adopts every table leaf by
-- leaf, and only the launch's source-skew check (version.SourceSkew) refuses that pairing.
local function in_full(ctx, t)
  if ctx.in_full then return ctx.in_full(t) end
  return t
end

-- THE openai-codex MODEL LIST. codexModelList expands the one declaration of it — the
-- `models` and `model_options` packs/openai-auth/pack.json ships on the openai-codex provider,
-- with the user's `providers.openai-codex` merged over it — into the ordered list every
-- consumer presents (docs/design/model-lists-and-pickers.md ML-D1). `p` is
-- ctx.providers["openai-codex"]; anything without a `models` table expands to {}.
--
-- Each declared id is WIRE-TRUE. Its facts come from model_options under the alias spelled
-- as the id: `order` (the map is unordered all the way here, so this is the only order there
-- is; unordered ids go last, by id), `name`, `description`, `context_window`, and
-- `long_context_window`, which means "this model also has a 1M variant". That variant is
-- emitted right after its base as `<id>[1m]`, a CLIENT spelling Claude Code, packs/pi's
-- extension and the wire bridge each strip before the request leaves.
--
-- ⚠ DUPLICATED VERBATIM in packs/claude/derive.lua, packs/pi/derive.lua and
-- packs/codex/derive.lua, because a derive cannot load another file (the sandbox has no
-- require and no io). internal/entrypoint/codex_model_list_test.go fails when the copies
-- differ, and when any consumer stops reading the declaration.
local function codexModelList(p)
  if type(p) ~= "table" or type(p.models) ~= "table" then return {} end
  local opts = type(p.model_options) == "table" and p.model_options or {}
  local aliases = {}
  for alias in pairs(p.models) do
    if type(alias) == "string" then table.insert(aliases, alias) end
  end
  table.sort(aliases)
  local function text(v)
    if type(v) == "string" and v ~= "" then return v end
    return nil
  end
  -- ONE ROW PER ID, and the alias spelled as the id carries its facts. Another alias naming
  -- the same id (a `default` or `fast` the user added) fills only a fact that row still
  -- lacks, in sorted alias order, so adding one moves nothing. Both of those names sort
  -- before every declared id, and a first-alias-wins walk handed the id the new alias's
  -- facts, which are none (docs/design/model-lists-and-pickers.md ML-D6).
  local rows, byId = {}, {}
  local function absorb(id, alias)
    local r = byId[id]
    if not r then
      r = { id = id }
      byId[id] = r
      table.insert(rows, r)
    end
    local f = type(opts[alias]) == "table" and opts[alias] or {}
    if r.order == nil then r.order = tonumber(f.order) end
    if r.name == nil then r.name = text(f.name) end
    if r.description == nil then r.description = text(f.description) end
    if r.context_window == nil then r.context_window = tonumber(f.context_window) end
    if r.long_context_window == nil then r.long_context_window = tonumber(f.long_context_window) end
  end
  for _, alias in ipairs(aliases) do
    if alias ~= "" and p.models[alias] == alias then absorb(alias, alias) end
  end
  for _, alias in ipairs(aliases) do
    local id = p.models[alias]
    if type(id) == "string" and id ~= "" and id ~= alias then absorb(id, alias) end
  end
  table.sort(rows, function(a, b)
    if a.order and b.order and a.order ~= b.order then return a.order < b.order end
    if a.order and not b.order then return true end
    if b.order and not a.order then return false end
    return a.id < b.id
  end)
  local list = {}
  for _, r in ipairs(rows) do
    table.insert(list, {
      id = r.id,
      name = r.name,
      description = r.description,
      context_window = r.context_window,
    })
    if r.long_context_window then
      table.insert(list, {
        id = r.id .. "[1m]",
        base = r.id,
        name = r.name and (r.name .. " (1M context)"),
        description = r.description and (r.description .. " · 1M context"),
        context_window = r.long_context_window,
      })
    end
  end
  return list
end

-- codexDefault is the model a codex-profile launch starts on, by ONE rule in every consumer:
-- the profile's `model` option unless it is absent or "default", else the first declared id,
-- which is always a base id. nil when the list is empty and the profile names nothing.
local function codexDefault(list, profile)
  local m = type(profile) == "table" and profile.model or nil
  if type(m) == "string" and m ~= "" and m ~= "default" then return m end
  return list[1] and list[1].id
end

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
--
-- mcpServers is REGENERATED IN FULL, and says so (in_full): every entry comes from
-- ctx.mcp_servers, so one on disk this run did not produce is yolo's own stale output and a
-- server dropped from config must not come back.
yolo.derive("claude", "config", function(ctx)
  local servers = {}
  for name, cfg in pairs(ctx.mcp_servers or {}) do
    servers[name] = cfg
  end
  return { mcpServers = in_full(ctx, servers) }
end)

-- settings (~/.claude/settings.json): three derivations.
--  1. tombstone mcpServers — MCP belongs in .claude.json, so strip any host
--     settings.json copy. (Was computed[] {to: mcpServers, tombstone: true}.)
--  2. enabledPlugins — enable the LSP plugin for each language whose LSP is
--     configured, and say NOTHING about the others. (Was computed[] flags
--     whenPresent x3.)
--  3. env.ENABLE_LSP_TOOL — "1" when ANY LSP is configured, else nothing.
--     (Was computed[] flags whenAny.)
--
-- ⚠ 2 AND 3 USED TO TOMBSTONE THE UNCONFIGURED CASE, AND THAT WAS A DATA-LOSS BUG —
-- measured on this machine 2026-09-20: the host ~/.claude/settings.json enabled
-- pyright-lsp and gopls-lsp, the jail's composed copy held `enabledPlugins: {}`, and
-- the three tombstones were why. A tombstone is an RFC-7386 DELETE aimed at the
-- lower layers, and the lowest layer under this one is the user's OWN host file —
-- so "remove a stale enable" removed the user's deliberate enable instead.
--
-- Nothing is owed in its place. yolo's own previous enable is not a layer: the
-- surface is RECOMPOSED from layers every boot, so a toggle this derive stops
-- asserting simply stops being written, and a leaf neither this derive nor a lower
-- layer supplies is already absent. The tombstone could only ever reach past yolo's
-- own output to the user's, which is the one thing it must not do
-- (docs/design/config-ownership-and-promotion.md §6.3.1).
--
-- Both tables are therefore EMPTY rather than tombstoned when nothing is configured,
-- which also keeps the adoption drop off them: an empty computed table asserts no
-- leaf and so claims none of the agent's own entries under the same key
-- (agentcfg.dropComputedTables).
--
-- ⚠ NEITHER `env` NOR `modelPicker` IS in_full, and that is the point (CO13): yolo asserts
-- ENABLE_LSP_TOOL and can never own the rest of a user's environment, and modelPicker's
-- keys are a fixed pair, not entries tracking a live table. Not declared in full, each
-- claims only the leaves it names — so an adopting render keeps the agent's own variables,
-- and the host notch never treats `env` as a table to clear and rewrite.
yolo.derive("claude", "settings", function(ctx)
  -- enabledPlugins IS NO LONGER WRITTEN AT ALL, and that is OQ-LSP1's option D
  -- (docs/reference/mcp-configuration.md#oq-lsp1). This used to map three hardcoded languages to three
  -- `claude-plugins-official` marketplace ids and enable the ones `lsp_servers` mentioned --
  -- an arbitrary, five-language-short subset of a table yolo already owns, and INERT besides,
  -- since the hook that installed those plugins was retired and nothing filled what this
  -- named.
  --
  -- yolo now renders ONE plugin of its own from the whole `lsp_servers` table
  -- (jailcontent.writeLSPPlugin). A plugin auto-loaded from the skills tree is ENABLED BY
  -- DEFAULT there -- the settings test for that sentinel is opt-OUT, measured against Claude
  -- Code 2.1.278 -- so there is no id for this derive to assert, and dropping the table also
  -- drops its whole interaction with agentcfg.dropComputedTables.
  local env = {}
  if next(ctx.lsp_servers) then env.ENABLE_LSP_TOOL = "1" end
  local out = {
    mcpServers = ctx.tombstone,
    env = env,
  }
  -- Claude Code's picker accepts exact gateway model IDs.  The Codex Responses
  -- bridge likewise sends model IDs unchanged, so expose the subscription
  -- catalog directly instead of asking users to infer a Claude tier alias.
  -- Replacing the built-ins prevents retired choices from leaking into
  -- a Codex-profile launch; `Default` resolves to ANTHROPIC_MODEL below.
  --
  -- THE LIST IS THE ONE DECLARATION (codexModelList above; packs/openai-auth/pack.json),
  -- the same list pi's extension registers, so the two pickers cannot drift. Both
  -- availableModels and modelPicker.options follow its order, the declared default first.
  -- availableModels' FIRST entry is what Claude Code's RETAINED built-in `Default` row
  -- resolves to, so leading with the default makes `Default` resolve to it while the
  -- explicit picker list beneath it presents the same models in the same order.
  if ctx.selected_provider == "openai-codex" then
    -- The client retains a hard-coded Default row. Constraining Default makes it
    -- resolve to the FIRST allowlisted ID, the declared default; modelPicker then lists
    -- every declared model beneath it, each 1M variant right after its base.
    --
    -- A list the user's config emptied writes none of the three keys: an enforced empty
    -- allowlist would leave claude with no model it may pick.
    local list = codexModelList(ctx.providers and ctx.providers["openai-codex"])
    if #list > 0 then
      local ids, options = {}, {}
      for _, e in ipairs(list) do
        table.insert(ids, e.id)
        table.insert(options, { model = e.id, label = e.name or e.id, description = e.description })
      end
      out.availableModels = ids
      out.enforceAvailableModels = true
      out.modelPicker = {
        options = options,
        replaceBuiltInOptions = true,
      }
    end
  end
  return out
end)

-- routedAuthToken is ANTHROPIC_AUTH_TOKEN for a routed anthropic endpoint that names its own
-- credential (`endpoints.anthropic.api_key_env_name`, which core composes onto an address a
-- pack service serves — the wire bridge's): the value the launch hydrated into that
-- endpoint's `api_key`, or the "local" dummy when it hydrated none. Never the provider's key,
-- and never absent: with no ANTHROPIC_AUTH_TOKEN claude sends its saved login's OAuth bearer
-- to the base URL (docs/design/agent-auth-modes.md §8.1), and the dummy at least draws a
-- clear 401 from the bridge instead.
local function routedAuthToken(ep)
  if type(ep) == "table" and type(ep.api_key) == "string" and ep.api_key ~= "" then
    return ep.api_key
  end
  return "local"
end

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
  -- openai-codex is a broker-backed subscription identity, like Pi's native provider. Its
  -- YOLO_PROVIDERS row (packs/openai-auth) carries the source's `capabilities` and the
  -- PUBLIC ADDRESS of the Responses API, and deliberately no credential pointer — the wire
  -- bridge gets its short-lived access-token view from openai-auth, never from a generated
  -- configuration file. The Responses endpoint accepts concrete Codex model IDs, and the
  -- ones below come from the provider's one declared list (codexModelList), so the model
  -- this launch starts on is pi's and codex's by construction, not by a copy kept aligned.
  if ctx.selected_provider == "openai-codex" then
    -- THE ADDRESS IS RESOLVED, NOT SPELLED. It used to be the literal 127.0.0.1:8215, a
    -- hand-copy of internal/wirebridged's CodexResponsesListenAddr that this file had no
    -- way to keep true. The openai-auth pack now declares the Responses endpoint the
    -- subscription actually serves, and the adapter that fronts it declares its own
    -- address, so core composes the pairing into this provider's entry exactly as it does
    -- for every other bridged provider (protocol-resolution.md, outcome 2).
    local codexAnthropic = (type(p) == "table" and type(p.endpoints) == "table"
      and type(p.endpoints.anthropic) == "table" and p.endpoints.anthropic) or nil
    local codexEp = codexAnthropic and codexAnthropic.base_url or nil
    local list = codexModelList(p)
    local first = list[1] or {}
    local model = codexDefault(list, ctx.profile)
    return {
      ANTHROPIC_BASE_URL = codexEp,
      -- THE TOKEN THAT STOPS THE LOGIN LEAKING. With no ANTHROPIC_AUTH_TOKEN, claude sends its
      -- saved Claude login's OAuth bearer to whatever ANTHROPIC_BASE_URL names
      -- (docs/design/agent-auth-modes.md §8.1, measured), and this route's base URL is the wire
      -- bridge on loopback, a port anything sharing that loopback can reach or squat. Setting
      -- the variable overrides the login, so claude sends this instead: the bridge's per-launch
      -- caller token, which the composed endpoint names as its credential (routedAuthToken).
      ANTHROPIC_AUTH_TOKEN = codexEp and routedAuthToken(codexAnthropic),
      -- Pin a fresh Codex-profile chat and every unassigned subagent to the
      -- profile's model, else the declared default. The picker above remains
      -- available for an intentional per-session choice.
      ANTHROPIC_MODEL = model,
      CLAUDE_CODE_SUBAGENT_MODEL = model,
      -- Claude Code retains its own Default row even when custom picker
      -- options replace the built-ins. Pin and label that row as the declared
      -- default too, so choosing it cannot silently return to the Claude
      -- subscription model advertised by the local client. Each is omitted when
      -- the list has no first entry to name.
      ANTHROPIC_DEFAULT_OPUS_MODEL = first.id,
      ANTHROPIC_DEFAULT_OPUS_MODEL_NAME = first.name,
      ANTHROPIC_DEFAULT_OPUS_MODEL_DESCRIPTION = first.description and (first.description .. " (default)"),
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
  -- The provider's address for the protocol claude speaks, and there is ONE spelling of
  -- it: the single-protocol `base_url` shorthand is deleted (protocol-resolution.md).
  -- The same bare field meant `openai` to pi and `anthropic` here, so one line of user
  -- config pointed two agents at two different services — and its headline case, a local
  -- llama.cpp/ollama/vLLM endpoint, speaks OpenAI, so this branch aimed
  -- ANTHROPIC_BASE_URL at a server claude cannot talk to. A user config carrying it is a
  -- validation refusal naming this spelling.
  if p.endpoints and p.endpoints.anthropic and p.endpoints.anthropic.base_url then
    baseUrl = p.endpoints.anthropic.base_url
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
  --
  -- AN ENDPOINT THAT NAMES ITS OWN CREDENTIAL TAKES THAT ONE (routedAuthToken): the wire
  -- bridge's address carries its per-launch caller token, because the bridge holds the
  -- provider's key itself and demands proof the caller is this launch's claude
  -- (docs/reference/wire-bridge.md WB-D18). Sending the provider's key there would hand it
  -- to whatever holds the bridge's port.
  local anthropicEp = p.endpoints and p.endpoints.anthropic
  if routed and type(anthropicEp) == "table" and anthropicEp.api_key_env_name then
    out.ANTHROPIC_AUTH_TOKEN = routedAuthToken(anthropicEp)
  elseif p.api_key then
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
  local isKilo = (ctx.selected_provider == "kilo" or (type(p) == "table" and type(p.endpoints) == "table" and type(p.endpoints.openai) == "table" and string.find(p.endpoints.openai.base_url or "", "api%.kilo%.ai") ~= nil))

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
