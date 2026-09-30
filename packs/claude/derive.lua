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

-- THE MODELS OF A MULTI-MAKER PROVIDER THIS AGENT CAN CALL. callableModels expands a provider's
-- `models` and `model_options` (for Bedrock, the declaration packs/bedrock/pack.json ships,
-- with the user's `providers.<name>` merged over it) into the ordered list of the entries this
-- agent's own client can call (docs/design/bedrock-plumbing.md OQ-BR9). Each entry declares its
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
-- packs/opencode/derive.lua and packs/pi/derive.lua, because a derive cannot load another file
-- (the sandbox has no require and no io). internal/entrypoint/bedrock_model_list_test.go fails
-- when the copies differ.
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

-- callableModel is the model this agent starts on among `list` (callableModels' answer for it):
-- the profile's `model` when it names, as an alias or as an id, an entry this agent can call,
-- or names an id the provider does not list at all (the user's own literal, passed through);
-- else the provider's `default` alias when this agent can call it; else, only when `pick` is
-- set, the first entry this agent can call. nil when none of those applies. A profile `model`
-- naming a listed entry this agent CANNOT call is skipped, never sent: that id is one this
-- agent's client would refuse.
--
-- `pick` is off for an agent whose own default on the service is already one of its models
-- (claude's Bedrock client picks an Anthropic model itself), because yolo picks a model only to
-- make a session valid (docs/design/model-lists-and-pickers.md OQ-ML2, ruled 2026-09-29).
local function callableModel(p, list, profile, pick)
  local models = (type(p) == "table" and type(p.models) == "table") and p.models or {}
  local callable, listed = {}, {}
  for _, e in ipairs(list) do callable[e.id] = true end
  for alias, id in pairs(models) do
    if type(id) == "string" then listed[id] = true end
    if type(alias) == "string" then listed[alias] = true end
  end
  local m = type(profile) == "table" and profile.model or nil
  if type(m) == "string" and m ~= "" and m ~= "default" then
    local id = models[m]
    if type(id) ~= "string" then id = m end
    if callable[id] then return id end
    if not listed[id] then return id end
  end
  local d = models["default"]
  if type(d) == "string" and callable[d] then return d end
  if pick and list[1] then return list[1].id end
  return nil
end

-- pinModel is the profile's opt-in to pinning claude's START model (MM-D3; `pin_model` is an
-- option name coined there, provisional): "true" or "1". Anything else, absence included, is
-- no opt-in. It cannot be the `model` option: ResolveProfiles merges a provider's declared
-- option defaults under the profile's own values, so a provider's declared `model` reads
-- exactly like a user's choice, and every provider declares a default (OQ-ML1's ruling).
local function pinModel(profile)
  local v = type(profile) == "table" and profile.pin_model or nil
  return v == "true" or v == "1"
end

-- THE TIER PINS (docs/design/model-lists-and-pickers.md MM-D2). claude's background, hook and
-- classifier requests pick a model BY TIER, and the Default row resolves by tier whenever that
-- tier's model is allowed, so on a list claude has no catalog for, an unpinned tier reaches a
-- model the list does not hold (claude's own Anthropic default, through a route that serves
-- none). Each tier takes the id under one of its own names where the provider's `models` map
-- declares one (tierAlias), else `default`, the list's default entry. A tier resolving to the
-- default entry says so in its description, which is what claude's Default row shows. `entries`
-- carries each id's name and description.
--
-- THE TIER NAMES (MM-D17, for OQ-PSW1): each tier reads claude's own name for it, then yolo's
-- conventional alias for it, the vocabulary OQ-XM2 ruled (docs/research/extension-model-defaults.md):
-- `frontier` for opus, `balanced` for sonnet and `fast` for haiku. So a provider that declares
-- only the conventional names, which XM-D2's warning asks every provider for, still pins claude's
-- Sonnet and Haiku tiers rather than leaving them on its default. claude's own name comes first
-- and wins where a provider declares both, because whoever wrote `sonnet` wrote it for claude;
-- the old names stay, so every config written for them keeps working. The fable tier has no
-- conventional alias. This table is the one place the names are spelled.
local tierOpus = { names = { "opus", "frontier" }, var = "OPUS" }
local tierSonnet = { names = { "sonnet", "balanced" }, var = "SONNET" }
local tierHaiku = { names = { "haiku", "fast" }, var = "HAIKU" }
local tierFable = { names = { "fable" }, var = "FABLE" }
local claudeTiers = { tierOpus, tierSonnet, tierHaiku, tierFable }

-- tierAlias is the id a provider's `models` map declares for tier: the first of the tier's names,
-- in order, that maps to a non-empty id `usable` accepts (nil accepts every id); nil when none
-- does. A name whose id claude cannot use gives way to the tier's next name (MM-D18).
local function tierAlias(models, tier, usable)
  if type(models) ~= "table" then return nil end
  for _, name in ipairs(tier.names) do
    local id = models[name]
    if type(id) == "string" and id ~= "" and (usable == nil or usable(id)) then return id end
  end
  return nil
end

local function pinTiers(out, models, entries, default)
  local byId = {}
  for _, e in ipairs(entries) do
    if byId[e.id] == nil then byId[e.id] = e end
  end
  for _, tier in ipairs(claudeTiers) do
    local id = tierAlias(models, tier) or default
    if id then
      local e = byId[id] or {}
      local var = "ANTHROPIC_DEFAULT_" .. tier.var .. "_MODEL"
      out[var] = id
      out[var .. "_NAME"] = e.name
      if e.description then
        out[var .. "_DESCRIPTION"] = (id == default) and (e.description .. " (default)") or e.description
      end
    end
  end
end

-- nativeBedrock: claude reaches the selected provider through its OWN Bedrock client, which
-- CLAUDE_CODE_USE_BEDROCK switches on. Two facts decide it, and neither is a profile's name
-- (docs/design/providers-and-profiles-redesign.md OQ-BR8, ruled 2026-09-29: "just because you
-- have a differently named profile here doesn't mean that things should happen differently"):
--
--   - THE PROVIDER IS BEDROCK: its `platform` says so (ctx.selected_platform, OQ-BR2). So a
--     user's own profile over `bedrock` (`bedrock-sso`, trap D5) and a user's own provider that
--     declares "platform": "aws-bedrock" switch it on exactly as `-p bedrock` does. It was a
--     `profile: "bedrock"` gate in pack.json, which matched the name and nothing else.
--   - THE TRANSPORT IS CLAUDE'S OWN: its profile routes through no via service
--     (ctx.via_url, the wire bridge). The everything profile reaches the same provider through
--     the bridge (OQ-BR11, OQ-BR1's `bedrock-bridge`), so it must NOT turn claude's native client
--     on; aws-auth's credential pointer still reaches it, keyed on the platform alone. claude is
--     then routed at the provider's anthropic endpoint (routedProvider below), which for the
--     shipped `bedrock`, named by region alone, is the wire bridge's adapter address, composed
--     for the via (docs/design/wire-bridge-gateway.md WG-I39); the bridge reaches runtime in the
--     region claude was handed. A Bedrock provider whose anthropic address is its own carries
--     none of claude's requests through the bridge, and the launch's via-route gate says so
--     (wirebridged.unroutedViaNotice).
local function nativeBedrock(ctx)
  return ctx.selected_platform == "aws-bedrock" and (ctx.via_url or "") == ""
end

-- routedProvider: claude reaches the selected provider at an anthropic endpoint yolo composed
-- — a gateway's own (z.ai, OpenRouter, llama.cpp) or the wire bridge's (Kilo, Cerebras, a via
-- profile) — rather than through its own login or its own Bedrock client. There claude has no
-- catalog of its own, so the provider's list is its WHOLE UNIVERSE
-- (docs/design/model-lists-and-pickers.md §14): its built-in rows are only its tier names,
-- meaning whatever the tier pins say. The same predicate the env derive below routes on.
local function routedProvider(ctx, p)
  return type(p) == "table" and type(p.endpoints) == "table" and type(p.endpoints.anthropic) == "table"
    and type(p.endpoints.anthropic.base_url) == "string" and p.endpoints.anthropic.base_url ~= ""
    and not nativeBedrock(ctx)
end

-- routedSpelling returns the function that spells a provider's wire id the way claude sends it
-- on that provider, the rule the env derive below applies to its tier pins: Kilo's bare
-- `deepseek-` ids gain their `deepseek/` vendor prefix, and every id gains `[1m]` when the
-- provider's context window (the profile's, else the provider's; Kilo's 1,048,576 when neither
-- says) is 1,000,000 tokens or more, claude's own spelling for the 1M-context beta. A picker
-- row spelled differently from the tier pins would be a second model to claude.
local function routedSpelling(ctx, p)
  local provOpts = (type(p) == "table" and type(p.options) == "table" and p.options) or {}
  local profileOpts = (type(ctx.profile) == "table" and ctx.profile) or {}
  local kilo = ctx.selected_provider == "kilo" or (type(p) == "table" and type(p.endpoints) == "table"
    and type(p.endpoints.openai) == "table"
    and string.find(p.endpoints.openai.base_url or "", "api%.kilo%.ai") ~= nil)
  local cw = profileOpts.context_window or profileOpts.max_context_tokens or provOpts.context_window
    or provOpts.max_context_tokens
  if not cw and kilo then cw = "1048576" end
  local cwNum = tonumber(cw or "")
  local suffix = (cwNum and cwNum >= 1000000) and "[1m]" or ""
  return function(id)
    if type(id) ~= "string" or id == "" then return id end
    if kilo and string.find(id, "^deepseek%-") and not string.find(id, "/") then
      id = "deepseek/" .. id
    end
    if suffix ~= "" and string.sub(id, -4) ~= "[1m]" then id = id .. suffix end
    return id
  end
end

-- routedRows is a routed provider's list as claude's picker shows it: the one expansion every
-- list consumer shares (codexModelList: `order`, then id; a `long_context_window` variant after
-- its base), each id spelled by routedSpelling. `wire` keeps the id as the provider declares
-- it, which is what a row with no name of its own is labelled by.
local function routedRows(ctx, p)
  local spell = routedSpelling(ctx, p)
  local rows = {}
  for _, e in ipairs(codexModelList(p)) do
    table.insert(rows, { id = spell(e.id), wire = e.id, name = e.name, description = e.description })
  end
  return rows
end

-- completeRoutedTiers finishes MM-D2's tier pins for a routed provider over what the env
-- derive's provider branch pinned: the fable tier, which that branch never pinned, takes the
-- provider's `fable` alias or else the opus tier's model (the branch's default entry), and
-- every pinned tier gains its entry's name and description, so claude's Default row and its
-- tier labels name a model of the list rather than a Claude tier. Nothing is pinned when the
-- branch pinned nothing: no model resolved, which is claude's own choice.
local function completeRoutedTiers(ctx, p, out)
  if not routedProvider(ctx, p) or not out.ANTHROPIC_DEFAULT_OPUS_MODEL then return out end
  local spell = routedSpelling(ctx, p)
  if not out.ANTHROPIC_DEFAULT_FABLE_MODEL then
    local fable = tierAlias(p.models, tierFable)
    out.ANTHROPIC_DEFAULT_FABLE_MODEL = (fable and spell(fable)) or out.ANTHROPIC_DEFAULT_OPUS_MODEL
  end
  local byId = {}
  for _, r in ipairs(routedRows(ctx, p)) do
    if byId[r.id] == nil then byId[r.id] = r end
  end
  for _, tier in ipairs(claudeTiers) do
    local var = "ANTHROPIC_DEFAULT_" .. tier.var .. "_MODEL"
    local e = out[var] and byId[out[var]]
    if e then
      out[var .. "_NAME"] = e.name
      out[var .. "_DESCRIPTION"] = e.description
    end
  end
  return out
end

-- THE LIST UNDER AN `only` (docs/design/model-lists-and-pickers.md §14.1, MM-D1, MM-D2,
-- MM-D5). listOnly says a `models` contribution's `only` narrowed the provider's list: the
-- composed entry carries `models_only` (packload.ModelsOnlyKey). That list is then claude's
-- exact menu on every path claude reaches the provider by — its own Bedrock client as much as a
-- routed endpoint — and, while the profile's switch is on, claude's own allowlist refuses
-- anything off it.
--
-- WHAT IS NOT HERE, ON PURPOSE. A list that only ADDS keeps today's rendering: what an `add`
-- does to an agent's own catalog is OQ-MM1's, unruled. And a routed list no `only` narrowed
-- writes no allowlist: whether it refuses on a gateway that serves more than it is OQ-MM3's.
local function listOnly(p)
  return type(p) == "table" and p.models_only == true
end

-- listFact reads one fact of an id from the provider's model_options: the alias spelled as
-- the id first, then any alias naming the id, in sorted order (codexModelList's precedence).
local function listFact(p, id, fact)
  local models = type(p) == "table" and type(p.models) == "table" and p.models or {}
  local opts = type(p) == "table" and type(p.model_options) == "table" and p.model_options or {}
  local function at(alias)
    local f = opts[alias]
    if type(f) == "table" and type(f[fact]) == "string" and f[fact] ~= "" then return f[fact] end
    return nil
  end
  if models[id] == id and at(id) then return at(id) end
  local aliases = {}
  for alias, target in pairs(models) do
    if type(alias) == "string" and target == id then table.insert(aliases, alias) end
  end
  table.sort(aliases)
  for _, alias in ipairs(aliases) do
    if at(alias) then return at(alias) end
  end
  return nil
end

-- onlyRows is the narrowed list as claude can use it: routedRows, less, on claude's own Bedrock
-- client, every entry whose `vendor` is not anthropic, since Bedrock's Messages API serves Claude
-- alone (docs/design/bedrock-plumbing.md OQ-BR9). An entry with no vendor is offered, as a
-- user's string-form alias always is.
local function onlyRows(ctx, p)
  local rows = routedRows(ctx, p)
  if not nativeBedrock(ctx) then return rows end
  local kept = {}
  for _, r in ipairs(rows) do
    local vendor = listFact(p, r.wire, "vendor")
    if vendor == nil or vendor == "anthropic" then table.insert(kept, r) end
  end
  return kept
end

-- listDefault is the list's DEFAULT ENTRY for claude (§7.2's rule): the entry the profile's
-- `model` option names, as an alias or as an id, when claude can use it; else the provider's
-- `default` alias when claude can use it; else the first entry. nil only for an empty list.
local function listDefault(ctx, p, rows)
  local spell = routedSpelling(ctx, p)
  local byId = {}
  for _, r in ipairs(rows) do byId[r.id] = byId[r.id] or r end
  local models = type(p) == "table" and type(p.models) == "table" and p.models or {}
  local m = type(ctx.profile) == "table" and ctx.profile.model or nil
  if type(m) == "string" and m ~= "" then
    local id = models[m]
    if type(id) ~= "string" then id = m end
    if byId[spell(id)] then return byId[spell(id)] end
  end
  if type(models.default) == "string" and byId[spell(models.default)] then
    return byId[spell(models.default)]
  end
  return rows[1]
end

-- onlyTierPins pins every tier to a narrowed list's id (MM-D2): the id under one of the tier's
-- names (tierAlias) where the list declares one claude can use, else the default entry.
local function onlyTierPins(ctx, p, out, rows, default)
  local spell = routedSpelling(ctx, p)
  local byId = {}
  for _, r in ipairs(rows) do byId[r.id] = true end
  local tiers = {}
  for _, tier in ipairs(claudeTiers) do
    local id = tierAlias(p.models, tier, function(id) return byId[spell(id)] == true end)
    if id then tiers[tier.names[1]] = spell(id) end
  end
  pinTiers(out, tiers, rows, default.id)
end

-- applyOnlyPins is the env derive's half under an `only`: every tier on the list, and the START
-- pin only on the `pin_model` opt-in while the switch is on, because claude's allowlist (the
-- settings derive's) then replaces an off-list saved model with Default, which the tier pins
-- make the list's default (MM-D3). With the switch off no allowlist renders, and what keeps
-- claude's start valid there is OQ-MM3's to rule, so the start pin the provider branch wrote
-- is left as it is. A list claude can use none of changes nothing.
local function applyOnlyPins(ctx, p, out)
  local rows = onlyRows(ctx, p)
  if #rows == 0 then return out end
  local default = listDefault(ctx, p, rows)
  for _, tier in ipairs(claudeTiers) do
    local var = "ANTHROPIC_DEFAULT_" .. tier.var .. "_MODEL"
    out[var], out[var .. "_NAME"], out[var .. "_DESCRIPTION"] = nil, nil, nil
  end
  onlyTierPins(ctx, p, out, rows, default)
  out.ANTHROPIC_SMALL_FAST_MODEL = out.ANTHROPIC_DEFAULT_HAIKU_MODEL
  if ctx.enforce_models ~= false then
    if pinModel(ctx.profile) then
      out.ANTHROPIC_MODEL = default.id
      out.CLAUDE_CODE_SUBAGENT_MODEL = default.id
    else
      out.ANTHROPIC_MODEL = nil
      out.CLAUDE_CODE_SUBAGENT_MODEL = nil
    end
  end
  return out
end

-- listPins is what the env derive's provider branch hands back: the `only` rendering where an
-- `only` narrowed the list and claude reaches the provider, and otherwise MM-D2's remaining pins
-- on a routed provider (completeRoutedTiers).
local function listPins(ctx, p, out)
  if listOnly(p) and (nativeBedrock(ctx) or routedProvider(ctx, p)) then
    return applyOnlyPins(ctx, p, out)
  end
  return completeRoutedTiers(ctx, p, out)
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
  -- CLAUDE_CODE_USE_BEDROCK in the settings file's `env` block, so a bare `claude` outside yolo
  -- still runs in Bedrock mode (providers.md#pv-d8); the env producer below sets it for a process
  -- yolo launches. Asserted, never tombstoned: a user's own switch here is theirs, and yolo
  -- deletes nothing it did not write (PP-D1). One this derive wrote into the real file at
  -- `yolo host apply` is yolo's, and that apply clears it once this stops asserting it, from
  -- the host's computed-leaf record (docs/design/host-computed-layer.md HC-D25).
  if nativeBedrock(ctx) then env.CLAUDE_CODE_USE_BEDROCK = "1" end
  local out = {
    mcpServers = ctx.tombstone,
    env = env,
  }
  -- Claude Code's picker accepts exact gateway model IDs.  The Codex Responses
  -- bridge likewise sends model IDs unchanged, so expose the subscription
  -- catalog directly instead of asking users to infer a Claude tier alias.
  -- Replacing the built-ins prevents retired choices from leaking into
  -- a Codex-profile launch.
  --
  -- THE LIST IS THE ONE DECLARATION (codexModelList above; packs/openai-auth/pack.json),
  -- the same list pi's extension registers, so the two pickers cannot drift.
  -- modelPicker.options follows its order; availableModels leads with the default entry.
  -- Claude Code's RETAINED built-in `Default` row keeps the tier default whenever that tier's
  -- model is allowed (2.1.285's enforcement fallback, docs/design/model-lists-and-pickers.md
  -- §14.2), so what makes it the list's default is the env derive's tier pins (pinTiers), not
  -- this order.
  if ctx.selected_provider == "openai-codex" then
    -- The allowlist is what keeps a session valid without steering it (MM-D1): an off-list
    -- saved model is replaced at startup by Default, and a valid `/model` choice survives the
    -- next launch, since the env derive pins no start model without `pin_model` (MM-D3).
    -- modelPicker lists every declared model beneath Default, each 1M variant right after its
    -- base.
    --
    -- A list the user's config emptied writes none of the three keys: an enforced empty
    -- allowlist would leave claude with no model it may pick.
    local list = codexModelList(ctx.providers and ctx.providers["openai-codex"])
    if #list > 0 then
      -- availableModels leads with the DEFAULT ENTRY, the one the profile's `model` names or
      -- else the first declared (MM-D1); modelPicker keeps the declared order.
      local default = codexDefault(list, ctx.profile)
      local ids, options = {}, {}
      for _, e in ipairs(list) do
        if e.id == default then table.insert(ids, e.id) end
      end
      for _, e in ipairs(list) do
        if e.id ~= default then table.insert(ids, e.id) end
        table.insert(options, { model = e.id, label = e.name or e.id, description = e.description })
      end
      -- The allowlist is a REFUSAL, so the profile's switch governs it (MM-D5): with
      -- enforce_models off the list only shapes the picker.
      if ctx.enforce_models ~= false then
        out.availableModels = ids
        out.enforceAvailableModels = true
      end
      out.modelPicker = {
        options = options,
        replaceBuiltInOptions = true,
      }
    end
  elseif routedProvider(ctx, ctx.providers and ctx.providers[ctx.selected_provider]) then
    -- EVERY OTHER ROUTED PROVIDER (MM-D1): the list is claude's whole universe there, so its
    -- rows replace the built-ins, which on a routed provider are only claude's tier names. Each
    -- row is spelled as the env derive's tier pins spell it (routedSpelling), so the Default
    -- row, which resolves by tier, is one of them. A provider that lists no model writes no
    -- picker, and claude keeps its built-ins over whatever the provider serves.
    --
    -- NO ALLOWLIST HERE, and that is an open question, not an omission: whether a list no
    -- `only` narrowed refuses other models on a gateway that serves more than it (z.ai, Kilo,
    -- OpenRouter) is OQ-MM3's (docs/design/model-lists-and-pickers.md), so claude's off-list
    -- refusal stays where it was, on openai-codex alone.
    local rows = routedRows(ctx, ctx.providers[ctx.selected_provider])
    if #rows > 0 then
      local options = {}
      for _, r in ipairs(rows) do
        table.insert(options, { model = r.id, label = r.name or r.wire, description = r.description })
      end
      out.modelPicker = {
        options = options,
        replaceBuiltInOptions = true,
      }
    end
  end
  -- UNDER AN `only` (§14.1's "Under an `only`" cells): the narrowed list is claude's exact
  -- menu whether claude reaches the provider through its own Bedrock client or a routed
  -- endpoint, and while the profile's switch is on (enforce_models, MM-D5) claude's own
  -- allowlist refuses what is off it, with the default entry first. On claude's own Bedrock
  -- client the refusal is claude's alone, client side, with §14.2's four gaps: yolo writes no
  -- managed settings file, so a repository's .claude/settings.json can add entries or switch
  -- enforcement off, and a managed source switches it off silently.
  --
  -- The native client's tier pins go in this file's `env` block, beside CLAUDE_CODE_USE_BEDROCK,
  -- the channel a bare `claude` and a host apply both keep (MM-D2, providers.md PV-D8); a routed
  -- pin never does, since a bare `claude` would send it to Anthropic, so the env derive carries
  -- those.
  local selectedP = ctx.providers and ctx.providers[ctx.selected_provider]
  if ctx.selected_provider ~= "openai-codex" and listOnly(selectedP)
    and (nativeBedrock(ctx) or routedProvider(ctx, selectedP)) then
    local rows = onlyRows(ctx, selectedP)
    if #rows > 0 then
      local default = listDefault(ctx, selectedP, rows)
      local options, ids = {}, { default.id }
      for _, r in ipairs(rows) do
        table.insert(options, { model = r.id, label = r.name or r.wire, description = r.description })
        if r.id ~= default.id then table.insert(ids, r.id) end
      end
      out.modelPicker = { options = options, replaceBuiltInOptions = true }
      if ctx.enforce_models ~= false then
        out.availableModels = ids
        out.enforceAvailableModels = true
      end
      if nativeBedrock(ctx) then
        onlyTierPins(ctx, selectedP, env, rows, default)
      end
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
    -- THE LOGIN PRELAUNCH (ES-D28): the launcher proves the shared OpenAI login before claude
    -- starts. Keyed on the PROVIDER (OQ-BR8), not on the profile being named `codex`, so a
    -- user's own profile over openai-codex gets it too. openai-codex is recognized by name
    -- because it is every agent's built-in subscription provider id, which the derives match
    -- this way throughout (PP-D2).
    local login = "1"
    -- THE ADDRESS IS RESOLVED, NOT SPELLED. It used to be the literal 127.0.0.1:8215, a
    -- hand-copy of internal/wirebridged's CodexResponsesListenAddr that this file had no
    -- way to keep true. The openai-auth pack now declares the Responses endpoint the
    -- subscription actually serves, and the adapter that fronts it declares its own
    -- address, so core composes the pairing into this provider's entry exactly as it does
    -- for every other bridged provider (protocol-resolution.md, outcome 2).
    local codexAnthropic = (type(p) == "table" and type(p.endpoints) == "table"
      and type(p.endpoints.anthropic) == "table" and p.endpoints.anthropic) or nil
    local codexEp = codexAnthropic and codexAnthropic.base_url or nil
    -- NO ADDRESS, NOTHING COMPOSED. The constants below describe the Responses models, and
    -- without ANTHROPIC_BASE_URL beside them claude runs on its own Claude login with a
    -- context window no Claude model has. That was `yolo host -p codex -- claude` on a bare
    -- `"packs": ["claude"]` (measured 2026-09-27: exit 0, three constants, no address). Core's
    -- protocol gate refuses every shape of that launch it can see, naming why (ES-D25); this
    -- is the producer's own half, so a route with no address is never half-composed here.
    -- Nor the login prelaunch: with no address nothing points claude at the subscription, so
    -- proving the OpenAI login first would be a prompt for a credential this launch never uses.
    if not codexEp then return {} end
    local list = codexModelList(p)
    local model = codexDefault(list, ctx.profile)
    local out = {
      YOLO_AUTH_PRELAUNCH_CLAUDE_LOGIN = login,
      ANTHROPIC_BASE_URL = codexEp,
      -- THE TOKEN THAT STOPS THE LOGIN LEAKING. With no ANTHROPIC_AUTH_TOKEN, claude sends its
      -- saved Claude login's OAuth bearer to whatever ANTHROPIC_BASE_URL names
      -- (docs/design/agent-auth-modes.md §8.1, measured), and this route's base URL is the wire
      -- bridge on loopback, a port anything sharing that loopback can reach or squat. Setting
      -- the variable overrides the login, so claude sends this instead: the bridge's per-launch
      -- caller token, which the composed endpoint names as its credential (routedAuthToken).
      ANTHROPIC_AUTH_TOKEN = codexEp and routedAuthToken(codexAnthropic),
      -- These are the Responses models' real 1.05M-token context capacity and
      -- Claude Code's documented 1M maximum proactive-compaction threshold.
      -- Stating both is necessary for an unrecognised custom model ID.
      CLAUDE_CODE_MAX_CONTEXT_TOKENS = "1050000",
      CLAUDE_CODE_AUTO_COMPACT_WINDOW = "1000000",
      CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC = "1",
    }
    -- EVERY TIER ON THE LIST (MM-D2). Claude Code keeps its own Default row even when the
    -- picker's options replace the built-ins, and that row, like every background request,
    -- resolves by tier; pinned only at opus, the other tiers reached the Claude subscription's
    -- models through a bridge that serves the Responses models alone. Each is omitted when the
    -- list names no default.
    pinTiers(out, p.models, list, model)
    -- THE START IS PINNED ONLY ON THE OPT-IN (MM-D3). claude returns to ANTHROPIC_MODEL at
    -- every launch, over the `/model` choice it saved, so this used to override a valid choice
    -- every time. The allowlist the settings derive writes makes the session valid without it:
    -- an off-list saved model is replaced at startup by Default, which the tier pins above make
    -- the list's default. A profile that wants every session to start on its model says
    -- `pin_model`. With the switch off (enforce_models: false) no allowlist renders, and what
    -- keeps claude's start valid there is OQ-MM3's to rule, so today's pin stays.
    if pinModel(ctx.profile) or ctx.enforce_models == false then
      out.ANTHROPIC_MODEL = model
      out.CLAUDE_CODE_SUBAGENT_MODEL = model
    end
    return out
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
  --
  -- NOT FOR CLAUDE'S OWN BEDROCK CLIENT (nativeBedrock): CLAUDE_CODE_USE_BEDROCK composes its
  -- own URL from the region, and an anthropic endpoint on a Bedrock provider is the wire
  -- bridge's (a user who gave the provider an `openai` endpoint gets the adapter's twin), which
  -- only a profile routing claude through the bridge asks for.
  if p.endpoints and p.endpoints.anthropic and p.endpoints.anthropic.base_url and not nativeBedrock(ctx) then
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
  -- Claude Code's own Bedrock client, for the launched process (nativeBedrock above; the
  -- settings derive writes the same switch into the file).
  if nativeBedrock(ctx) then
    out.CLAUDE_CODE_USE_BEDROCK = "1"
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
  -- attribution_header -> CLAUDE_CODE_ATTRIBUTION_HEADER: whether claude sends its attribution
  -- header, a provider option (OQ-BR8) because it is a fact about the server. packs/llamacpp
  -- declares "false": with the attribution block prepended to the system prompt, llama.cpp
  -- fails prefix reuse and reprocesses the whole prompt every turn (packs/llamacpp/README.md).
  -- Any provider can declare it, and a profile can set it. It was a gated env
  -- keyed on the profile's NAME, `llamacpp`, which a second profile over the same provider
  -- lost, and which reached every agent on it although only claude reads the variable.
  local attribution = profileOpts.attribution_header or provOpts.attribution_header
  if attribution == "false" or attribution == "0" then
    out.CLAUDE_CODE_ATTRIBUTION_HEADER = "0"
  elseif attribution == "true" or attribution == "1" then
    out.CLAUDE_CODE_ATTRIBUTION_HEADER = "1"
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
    -- First-party and Bedrock launches keep it (nativeBedrock owns that mode).
    out.CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC = "1"
  end
  local m = p.models or {}
  -- OPUS takes the alias the active profile's `model` option names (OQ-CS4: what an
  -- option means is the derive's business), falling back to the provider's declared
  -- `default` when the profile carries none (OQ-CS3). SONNET and HAIKU keep their own
  -- aliases, `sonnet` or `balanced` and `haiku` or `fast` (tierAlias, MM-D17): they are
  -- Claude's routing names inside the same provider, not a selection surface, and no profile
  -- option speaks for them. Declaring them matters even though
  -- z.ai translates claude's own tier names server-side (measured 2026-09-04:
  -- claude-sonnet-* serves as glm-5.3-flash — the FAST model), because the aliases pin
  -- each tier to the model the provider actually intends for it.
  local alias = (ctx.profile and ctx.profile.model) or (type(p) == "table" and type(p.options) == "table" and p.options.model) or "default"
  local selected = m[alias] or m["default"]
  if not selected and alias ~= "default" then
    selected = alias
  end
  -- A BEDROCK PROVIDER SERVES SEVERAL MAKERS' MODELS, and claude's own Bedrock client calls
  -- Anthropic's alone: Bedrock's Messages API serves Claude only
  -- (docs/design/bedrock-plumbing.md §2, OQ-BR9). So on it the model comes from the entries
  -- whose `vendor` is anthropic, or that declare none, through callableModel: the profile's
  -- `model`, else the provider's `default` alias, and otherwise NOTHING. No first-callable
  -- pick, because Claude Code on Bedrock starts on an Anthropic model of its own, which is a
  -- valid session yolo does not steer (docs/design/model-lists-and-pickers.md OQ-ML2), and the
  -- shipped list names no `default`. A profile routed through a via service with no
  -- anthropic endpoint to carry it pins nothing either: claude runs on its own login there,
  -- where a Bedrock id is one it cannot call.
  local bedrockCallable = nil
  if ctx.selected_platform == "aws-bedrock" then
    if nativeBedrock(ctx) then
      local list = callableModels(p, { anthropic = true })
      selected = callableModel(p, list, ctx.profile, false)
      bedrockCallable = {}
      for _, e in ipairs(list) do bedrockCallable[e.id] = true end
    elseif not routed then
      selected = nil
    end
  end
  -- A tier's names, claude's own first and then yolo's conventional alias (tierAlias, MM-D17).
  -- The tier aliases a Bedrock provider names are held to the same makers.
  local function tier(t)
    return tierAlias(m, t, function(id) return not bedrockCallable or bedrockCallable[id] == true end)
  end
  if selected then
    if isKilo then
      selected = normalizeKiloModel(selected)
    end
    out.ANTHROPIC_MODEL = selected .. suffix
    out.ANTHROPIC_DEFAULT_OPUS_MODEL = selected .. suffix
    -- A curated provider can publish real picker IDs instead of Claude-tier aliases.
    -- Keep all tiers on the selected model rather than falling back upstream.
    local sonnet = tier(tierSonnet) or selected
    local haiku = tier(tierHaiku) or selected
    if isKilo then
      sonnet = normalizeKiloModel(sonnet)
      haiku = normalizeKiloModel(haiku)
    end
    out.ANTHROPIC_DEFAULT_SONNET_MODEL = sonnet .. suffix
    out.ANTHROPIC_DEFAULT_HAIKU_MODEL = haiku .. suffix
    out.ANTHROPIC_SMALL_FAST_MODEL = haiku .. suffix
  end
  -- The list's pins: under an `only` its whole rendering, else MM-D2's remaining tier pins on a
  -- routed provider (listPins).
  return listPins(ctx, p, out)
end)
