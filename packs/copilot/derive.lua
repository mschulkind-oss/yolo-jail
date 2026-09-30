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

-- narrowedFirst is a provider list's first entry in the order every list consumer shares: the
-- `order` fact (declared before undeclared, read under the alias spelled as the id first), then
-- the id. nil for an empty list.
local function narrowedFirst(p)
  local opts = type(p.model_options) == "table" and p.model_options or {}
  local function orderOf(id)
    local own = opts[id]
    if type(own) == "table" and tonumber(own.order) then return tonumber(own.order) end
    for alias, target in pairs(p.models or {}) do
      local f = opts[alias]
      if target == id and type(f) == "table" and tonumber(f.order) then return tonumber(f.order) end
    end
    return nil
  end
  local rows = {}
  for _, id in pairs(p.models or {}) do
    if type(id) == "string" and id ~= "" then table.insert(rows, { id = id, order = orderOf(id) }) end
  end
  table.sort(rows, function(a, b)
    if a.order and b.order and a.order ~= b.order then return a.order < b.order end
    if a.order and not b.order then return true end
    if b.order and not a.order then return false end
    return a.id < b.id
  end)
  return rows[1] and rows[1].id
end

yolo.env("copilot", function(ctx)
  local p = ctx.providers[ctx.selected_provider]
  if not p then return {} end
  local base, ptype, wire
  -- ep is the endpoint copilot is sent to, when it came from `endpoints`: the one whose own
  -- credential, if it names one, is the one copilot sends (below).
  local ep
  -- AN ADDRESS COMPOSED FOR A VIA PROFILE (`for_via`, docs/design/wire-bridge-gateway.md WG-I39)
  -- is copilot's only under a profile that routes through that service: the shipped `bedrock`
  -- names a region and no address, and the wire bridge fronts it for `bedrock-bridge`. copilot has
  -- no Bedrock client of its own, so on any other profile over such a provider it composes
  -- nothing, as before, and the launch's profile line says the profile reaches nothing for it.
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
  -- Still one model: copilot's environment-variable setup carries one, and the whole list is
  -- providers.json's to show, after the measurements MM-D10 names.
  if not model and p.models_only == true then
    model = m.default or narrowedFirst(p)
  end
  -- THROUGH THE BRIDGE TO A PROVIDER OF SEVERAL MAKERS (viaOnly above: the shipped `bedrock`, whose
  -- list names no `default`, docs/design/model-lists-and-pickers.md ML-D9) copilot still needs one
  -- model, since BYOK refuses to start without one, and the bridge carries every maker on the list
  -- (translating all but Anthropic's). OQ-ML2's rule picks it: the provider's declared default,
  -- else the first model it lists.
  if not model and viaOnly then
    model = m.default or narrowedFirst(p)
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
  -- AN ENDPOINT THAT NAMES ITS OWN CREDENTIAL TAKES THAT ONE, over the provider's key: core
  -- composes it onto an address a pack service serves (the wire bridge's), whose service holds
  -- the provider's key itself and demands this launch's caller token of every caller
  -- (docs/reference/wire-bridge.md WB-D18). An unhydrated one sends the "local" dummy, which
  -- the service refuses with a clear 401, rather than the provider's key to a loopback port.
  if type(ep) == "table" and ep.api_key_env_name then
    if type(ep.api_key) == "string" and ep.api_key ~= "" then
      out.COPILOT_PROVIDER_API_KEY = ep.api_key
    else
      out.COPILOT_PROVIDER_API_KEY = "local"
    end
  elseif p.api_key then
    out.COPILOT_PROVIDER_API_KEY = p.api_key
  elseif type(p.options) == "table" and p.options.api_key then
    out.COPILOT_PROVIDER_API_KEY = p.options.api_key
  elseif isLocalEndpoint(base) then
    out.COPILOT_PROVIDER_API_KEY = "local"
  end
  return out
end)

