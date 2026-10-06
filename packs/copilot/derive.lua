-- copilot has two dynamic surfaces and one env producer.

-- in_full declares a table this derive regenerates in full — ctx.in_full, the CO13 sentinel
-- (docs/design/config-ownership-and-promotion.md): its entries track a live table, so one on
-- disk this run did not produce is yolo's own stale output. A table returned without it is
-- one the derive only asserts leaves of. The fallback is for an entrypoint older than the
-- sentinel, which treated every non-empty table as regenerated in full anyway.
-- It covers that direction ONLY: a NEWER entrypoint handed an OLDER copy of this file (packs
-- are staged from the host yolo's embed) sees no declaration and adopts every table leaf by
-- leaf, and only the launch's source-skew check (version.SourceSkew) refuses that pairing.
local function in_full(ctx, t)
  if ctx.in_full then return ctx.in_full(t) end
  return t
end

-- mcp: passthrough — canonical mcp_servers lands verbatim under mcpServers.
yolo.derive("copilot", "mcp", function(ctx)
  return { mcpServers = in_full(ctx, ctx.mcp_servers) }
end)

-- lsp: project each lsp_servers entry into copilot's dialect. Was computed[]
-- project ops: copy command (omitEmpty), copy args, default args=[], copy
-- fileExtensions, default fileExtensions={}.
yolo.derive("copilot", "lsp", function(ctx)
  local out = {}
  for name, s in pairs(ctx.lsp_servers) do
    local e = {}
    -- copy command, omitEmpty: skip an empty/absent command.
    if s.command ~= nil and s.command ~= "" then e.command = s.command end
    -- copy args, then default to [] when absent — ctx.empty_array so an absent
    -- args renders as JSON [], not {} (Lua can't tell {} array from {} object).
    e.args = s.args or ctx.empty_array
    -- copy fileExtensions, then default to {} (object) when absent.
    e.fileExtensions = s.fileExtensions or {}
    out[name] = e
  end
  return { lspServers = in_full(ctx, out) }
end)

-- env: the provider environment copilot's own process launches with — copilot has no
-- provider directory and no file keys to write; its BYOK is env-var-only, so this
-- producer IS copilot's whole provider delivery (docs/reference/providers.md §"Per-agent
-- delivery"; design doc cerebras-pack-and-copilot-delivery.md D-2).
--
-- Dialect map, canonical → copilot's spellings
-- [provenance: @github/copilot 1.0.48 app.js — the "config" help topic and the enum
-- tables at @11811457/@9794415/@4967235, read 2026-08-20 (docs/research/
-- local-model-endpoints.md §"Copilot CLI"); re-confirmed against GitHub's BYOK docs
-- 2026-09-04]:
--   anthropic               → COPILOT_PROVIDER_TYPE=anthropic (no WIRE_API — copilot's
--                             wire_api enum is {completions, responses} and speaks only
--                             to the openai type)
--   openai-chat-completions → TYPE=openai, COPILOT_PROVIDER_WIRE_API=completions
--   openai-responses        → TYPE=openai, WIRE_API=responses
--   (absent wire_api)       → completions, copilot's own default
-- Copilot is the one agent for which no canonical value is unspeakable — it talks both
-- surviving protocol families — so this derive emits nothing only when the provider
-- names no endpoint at all (bedrock's region facts: copilot's `azure` type is Azure
-- OpenAI's deployment URL shape, not a bedrock address).
--
-- Activation is gated SOLELY on COPILOT_PROVIDER_BASE_URL (copilot ignores every other
-- COPILOT_PROVIDER_* without it), and a MODEL is mandatory — BYOK refuses to start
-- without one. So a provider declaring no resolvable model alias composes NOTHING:
-- arming the base URL without a model would trade a working GitHub-auth copilot for a
-- copilot-side refusal. GitHub auth is skipped entirely in BYOK mode — selecting a
-- provider for copilot is a mode switch, not an extra.
local function isLocalEndpoint(url)
  if type(url) ~= "string" then return false end
  return string.find(url, "://localhost") or
         string.find(url, "://127%.0%.0%.1") or
         string.find(url, "://host%.containers%.internal") or
         string.find(url, "://169%.254%.1%.2") or
         string.find(url, "://0%.0%.0%.0")
end

-- THE MODELS OF A MULTI-MAKER PROVIDER THIS AGENT CAN CALL. callableModels expands a provider's
-- `models` and `model_options` (for Bedrock, the list a pack's `models` contribution or the
-- user's `providers.<name>` supplies: packs/bedrock ships none, docs/design/model-lists-and-pickers.md
-- MM-D32) into the ordered list of the entries this agent's own client can call
-- (docs/design/bedrock-plumbing.md OQ-BR9). Each entry declares its
-- maker as the `vendor` fact, and the maker is never parsed out of the id. `makers` is the set
-- of vendors this agent's client serves, nil meaning every one, and an entry that declares no
-- vendor (a user's string-form alias) is offered to every agent.
--
-- ONE ROW PER ID, as codexModelList builds it (docs/design/model-lists-and-pickers.md ML-D6):
-- the alias spelled as the id supplies its facts first, and every other alias naming the same
-- id fills only a fact still missing, in sorted alias order. A row's `facts` are those merged
-- model_options strings. The rows are ordered by the `order` fact (declared before undeclared),
-- then by id, so the first row is the provider's declared default among what this agent can
-- call, which is the fallback OQ-BR9's ruling names: "the first model that agent can call".
--
-- ⚠ DUPLICATED VERBATIM in packs/claude/derive.lua, packs/codex/derive.lua,
-- packs/opencode/derive.lua and packs/pi/derive.lua, and callableModels alone in
-- packs/copilot/derive.lua, because a derive cannot load another file (the sandbox has no
-- require and no io). internal/entrypoint/bedrock_model_list_test.go fails when the copies differ.
local function callableModels(p, makers)
  if type(p) ~= "table" or type(p.models) ~= "table" then return {} end
  local opts = type(p.model_options) == "table" and p.model_options or {}
  local aliases = {}
  for alias in pairs(p.models) do
    if type(alias) == "string" then table.insert(aliases, alias) end
  end
  table.sort(aliases)
  local rows, byId = {}, {}
  local function absorb(id, alias)
    local r = byId[id]
    if not r then
      r = { id = id, facts = {} }
      byId[id] = r
      table.insert(rows, r)
    end
    local f = type(opts[alias]) == "table" and opts[alias] or {}
    for k, v in pairs(f) do
      if r.facts[k] == nil and type(v) == "string" and v ~= "" then r.facts[k] = v end
    end
  end
  for _, alias in ipairs(aliases) do
    if alias ~= "" and p.models[alias] == alias then absorb(alias, alias) end
  end
  for _, alias in ipairs(aliases) do
    local id = p.models[alias]
    if type(id) == "string" and id ~= "" and id ~= alias then absorb(id, alias) end
  end
  for _, r in ipairs(rows) do
    r.order = tonumber(r.facts.order)
    r.vendor = r.facts.vendor
  end
  table.sort(rows, function(a, b)
    if a.order and b.order and a.order ~= b.order then return a.order < b.order end
    if a.order and not b.order then return true end
    if b.order and not a.order then return false end
    return a.id < b.id
  end)
  local list = {}
  for _, r in ipairs(rows) do
    if r.vendor == nil or makers == nil or makers[r.vendor] then table.insert(list, r) end
  end
  return list
end

-- listFirst is a provider list's first entry in the order every list consumer shares
-- (callableModels: the `order` fact, declared before undeclared, then the id), nil for an empty
-- list. copilot through the bridge takes every maker, so nothing is filtered.
local function listFirst(p)
  local first = callableModels(p, nil)[1]
  return first and first.id
end

-- COPILOT'S STARTING MODEL ON BEDROCK (docs/design/model-lists-and-pickers.md MM-D32, MM-D34).
-- yolo ships no Bedrock model list, so every agent with a Bedrock catalog of its own starts on
-- that catalog's default. copilot has none, and its environment-variable setup refuses to start
-- without a model, so on a Bedrock provider whose list names nothing, with no model named, it
-- starts here: OpenAI's open-weight gpt-oss-120b, as bedrock-runtime spells it in-Region. Among the
-- cheapest models Bedrock serves ($0.15 / $0.60 per million input / output tokens in the US, as
-- read 2026-10-05), and served on runtime's chat completions, the route the wire bridge translates
-- copilot's requests onto (MEASURED 2026-10-05: one signed request from a jail in us-east-1
-- answered 200). A pack's `models` contribution, the user's own list, or a profile's `model`
-- replaces it.
local bedrockStartModel = "openai.gpt-oss-120b-1:0"

-- providersFile is copilot's providers.json (MM-D31, MM-D33) for provider `name`, reached at base
-- as ptype (wire for an openai type) with key: the provider and one row per entry of its list, in
-- list order, then `start` when the list does not hold it (a profile's own literal id). Each row's
-- id is the wire id, so copilot's selection id is `<name>/<id>`; a row carries the entry's display
-- name and limits where the list declares them, and every row the profile's context window.
local function providersFile(p, name, base, ptype, wire, key, start, cw)
  local provider = { name = name, type = ptype, baseUrl = base }
  if wire then provider.wireApi = wire end
  if key then provider.apiKey = key end
  local rows, listed = {}, {}
  local function row(id, facts)
    local r = { id = id, provider = name, wireModel = id }
    if type(facts.name) == "string" and facts.name ~= "" then r.name = facts.name end
    local window = tonumber(facts.context_window)
    if window and window > 0 then r.maxContextWindowTokens = window end
    local output = tonumber(facts.max_tokens)
    if output and output > 0 then r.maxOutputTokens = output end
    local prompt = tonumber(cw)
    if prompt and prompt > 0 then r.maxPromptTokens = prompt end
    table.insert(rows, r)
    listed[id] = true
  end
  for _, e in ipairs(callableModels(p, nil)) do row(e.id, e.facts) end
  if start and not listed[start] then row(start, {}) end
  return { providers = { provider }, models = rows }
end

yolo.env("copilot", function(ctx)
  local p = ctx.providers[ctx.selected_provider]
  if not p then return {} end
  local base, ptype, wire
  -- ep is the endpoint copilot is sent to, when it came from `endpoints`: the one whose own
  -- credential, if it names one, is the one copilot sends (below).
  local ep
  -- AN ADDRESS COMPOSED FOR A VIA PROFILE (`for_via`, docs/design/wire-bridge-gateway.md WG-I39)
  -- is copilot's only when its profile routes it through that service, which ctx.via_url says:
  -- the shipped `bedrock` names a region and no address, and the wire bridge fronts it. copilot
  -- has no Bedrock client of its own, so the bridge carries it on plain `-p bedrock` too, wherever
  -- the bridge is in the launch (WG-I44, the profile's carrier), and hands it a via URL there as
  -- `bedrock-bridge` does. With no via URL nothing serves the address, so copilot composes
  -- nothing, and the launch's profile line says the profile reaches nothing for it.
  local viaOnly = p.endpoints and type(p.endpoints.anthropic) == "table" and p.endpoints.anthropic.for_via
  if viaOnly and (ctx.via_url or "") == "" then return {} end
  if p.endpoints and p.endpoints.anthropic and p.endpoints.anthropic.base_url then
    -- zai is the worked example: its anthropic route is the richer surface (claude's
    -- own channel), and `anthropic` is copilot's first-class spelling for it (D-3).
    ep = p.endpoints.anthropic
    base = ep.base_url
    ptype = "anthropic"
  elseif p.endpoints and p.endpoints.openai and p.endpoints.openai.base_url then
    ep = p.endpoints.openai
    base = p.endpoints.openai.base_url
    ptype = "openai"
    wire = "completions"
    if p.endpoints.openai.wire_api == "openai-responses" then
      wire = "responses"
    end
  -- ⚠ NOT DEAD CODE, though the key is REMOVED from user config
  -- (docs/reference/protocol-resolution.md): `validateProviderShorthandRetired` is an error
  -- on the HOST and only a warning IN A JAIL, where the config is the host-generated
  -- snapshot, so a jail launched by an older host `yolo` still carries the shorthand. This
  -- arm is what keeps that jail's provider addressable. See packs/codex/derive.lua for the
  -- measurement.
  elseif p.base_url then
    base = p.base_url
    if p.wire_api == "anthropic" then
      ptype = "anthropic"
    else
      ptype = "openai"
      wire = "completions"
      if p.wire_api == "openai-responses" then
        wire = "responses"
      end
    end
  else
    return {}
  end
  local m = p.models or {}
  local alias = (ctx.profile and ctx.profile.model) or "default"
  local model = m[alias]
  -- UNDER AN `only` (docs/design/model-lists-and-pickers.md §14.1, copilot) the narrowed list
  -- is the provider's whole menu, and its DEFAULT ENTRY is §7.2's: the profile's `model` when
  -- the list holds it, else the `default` alias, else the list's first entry. An only that drops
  -- the profile's model must not leave copilot on its GitHub login, so the first entry answers.
  -- copilot's environment-variable setup carries this one model; where the notch writes copilot's
  -- providers.json (below, MM-D31) the file shows the whole list and this is the one it starts on.
  if not model and p.models_only == true then
    model = m.default or listFirst(p)
  end
  -- THROUGH THE BRIDGE TO A PROVIDER OF SEVERAL MAKERS (viaOnly above: the shipped `bedrock`, whose
  -- list, when a pack or the user supplies one, may name no `default`) copilot still needs one
  -- model, since BYOK refuses to start without one, and the bridge carries every maker on the list
  -- (translating all but Anthropic's). OQ-ML2's rule picks it: the provider's declared default,
  -- else the first model it lists.
  -- A PROFILE'S OWN ID ON THAT PATH, one the list does not hold, is passed through as the user wrote
  -- it, ahead of any pick, as every Bedrock binding passes one (callableModel in the other derives):
  -- naming a model in a profile is how a user replaces the default below (MM-D32).
  if not model and viaOnly then
    local named = ctx.profile and ctx.profile.model
    if type(named) == "string" and named ~= "" and named ~= "default" then model = named end
  end
  if not model and viaOnly then
    model = m.default or listFirst(p)
  end
  -- A BEDROCK PROVIDER WHOSE LIST NAMES NOTHING (MM-D32: packs/bedrock ships none) still needs
  -- copilot's one model, and copilot has no Bedrock catalog to default from: bedrockStartModel.
  if not model and viaOnly and ctx.selected_platform == "aws-bedrock" then
    model = bedrockStartModel
  end
  if not model then return {} end
  local out = {
    COPILOT_PROVIDER_BASE_URL = base,
    COPILOT_PROVIDER_TYPE = ptype,
    COPILOT_MODEL = model,
  }
  if wire then out.COPILOT_PROVIDER_WIRE_API = wire end
  local cw = ctx.profile and (ctx.profile.context_window or ctx.profile.max_context_tokens)
  if not cw and type(p.options) == "table" then
    cw = p.options.context_window or p.options.max_context_tokens
  end
  if cw then
    out.COPILOT_PROVIDER_MAX_PROMPT_TOKENS = tostring(cw)
  end
  local key
  -- AN ENDPOINT THAT NAMES ITS OWN CREDENTIAL TAKES THAT ONE, over the provider's key: core
  -- composes it onto an address a pack service serves (the wire bridge's), whose service holds
  -- the provider's key itself and demands this launch's caller token of every caller
  -- (docs/reference/wire-bridge.md WB-D18). An unhydrated one sends the "local" dummy, which
  -- the service refuses with a clear 401, rather than the provider's key to a loopback port.
  if type(ep) == "table" and ep.api_key_env_name then
    if type(ep.api_key) == "string" and ep.api_key ~= "" then
      key = ep.api_key
    else
      key = "local"
    end
  elseif p.api_key then
    key = p.api_key
  elseif type(p.options) == "table" and p.options.api_key then
    key = p.options.api_key
  elseif isLocalEndpoint(base) then
    key = "local"
  end
  out.COPILOT_PROVIDER_API_KEY = key
  -- THE WHOLE LIST, WHERE ONE IS SUPPLIED (docs/design/model-lists-and-pickers.md MM-D31, ruled
  -- 2026-10-05): a provider whose list names any model, by a pack's declaration, a `models`
  -- contribution or the user's own `providers.<name>.models`, reaches copilot as its
  -- providers.json, which shows every entry in its picker. copilot's own help says the file
  -- replaces the COPILOT_PROVIDER_* variables once it declares anything, so those above stay as the
  -- delivery wherever no file is written, and in the file's mode a model is selected as
  -- `<provider>/<id>`. The file's models JOIN GitHub's own: a copilot signed in to GitHub shows
  -- GitHub's models beside the list, and a GitHub model picked there is GitHub's to serve, which
  -- the ruling accepts. Only where this notch writes the file (ctx.agent_files: a jail, MM-D33),
  -- since it carries the key as literal text; at `yolo host` copilot keeps the one model above.
  if ctx.agent_files and #callableModels(p, nil) > 0 then
    local name = ctx.selected_provider
    out.COPILOT_PROVIDERS_CONFIG = providersFile(p, name, base, ptype, wire, key, model, cw)
    out.COPILOT_MODEL = name .. "/" .. model
  end
  return out
end)

