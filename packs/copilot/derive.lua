-- copilot has two dynamic surfaces and one env producer.

-- mcp: passthrough — canonical mcp_servers lands verbatim under mcpServers.
yolo.derive("copilot", "mcp", function(ctx)
  return { mcpServers = ctx.mcp_servers }
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
  return { lspServers = out }
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
yolo.env("copilot", function(ctx)
  local p = ctx.providers[ctx.selected_provider]
  if not p then return {} end
  local base, ptype, wire
  if p.endpoints and p.endpoints.anthropic and p.endpoints.anthropic.base_url then
    -- zai is the worked example: its anthropic route is the richer surface (claude's
    -- own channel), and `anthropic` is copilot's first-class spelling for it (D-3).
    base = p.endpoints.anthropic.base_url
    ptype = "anthropic"
  elseif p.endpoints and p.endpoints.openai and p.endpoints.openai.base_url then
    base = p.endpoints.openai.base_url
    ptype = "openai"
    wire = "completions"
    if p.endpoints.openai.wire_api == "openai-responses" then
      wire = "responses"
    end
  else
    return {}
  end
  local m = p.models or {}
  local alias = (ctx.profile and ctx.profile.model) or "default"
  if not m[alias] then return {} end
  local out = {
    COPILOT_PROVIDER_BASE_URL = base,
    COPILOT_PROVIDER_TYPE = ptype,
    COPILOT_MODEL = m[alias],
  }
  if wire then out.COPILOT_PROVIDER_WIRE_API = wire end
  if p.api_key then out.COPILOT_PROVIDER_API_KEY = p.api_key end
  return out
end)
