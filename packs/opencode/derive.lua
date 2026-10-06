-- opencode: project the canonical MCP and provider entries into opencode's dialect.

-- The provider's URL for a protocol opencode speaks, and which protocol it is: `openai` first,
-- then `openai-responses`, the order packs/opencode declares in its `protocols` list. ONE
-- spelling: the single-protocol `base_url` shorthand is deleted (protocol-resolution.md),
-- because the same bare field meant `openai` here and `anthropic` in claude's derive. Total
-- over non-tables so the call site stays a one-line gate. Returns nil when the provider names
-- no URL opencode can use, which is what keeps the gate below honest: a provider whose only
-- endpoint speaks anthropic would otherwise emit an entry with no URL. opencode consumes no
-- wire_api; the endpoint's KEY picks the SDK (opencodeSDK).
local function providerEndpoint(prov)
  if type(prov) ~= "table" or type(prov.endpoints) ~= "table" then return nil end
  for _, protocol in ipairs({"openai", "openai-responses"}) do
    local ep = prov.endpoints[protocol]
    if type(ep) == "table" and ep.base_url then
      return ep.base_url, protocol
    end
  end
  return nil
end

-- THE SDK OPENCODE LOADS FOR EACH PROTOCOL IT SPEAKS, read from the opencode 1.18.34 binary
-- the launcher installs (its bundled packages, never run): `@ai-sdk/openai-compatible`, whose
-- language model is chat completions and which has no Responses model at all, for `openai`;
-- `@ai-sdk/openai`, whose language model IS Responses (it posts to `<baseURL>/responses`), for
-- `openai-responses`. A row's npm is what opencode builds its client from, so a Responses-only
-- provider on the compatible SDK would send chat completions to an endpoint that serves none.
local opencodeSDK = {
  ["openai"] = "@ai-sdk/openai-compatible",
  ["openai-responses"] = "@ai-sdk/openai",
}

-- THE ChatGPT SUBSCRIPTION (docs/design/model-lists-and-pickers.md ML-D1). yolo's provider is
-- `openai-codex` (packs/openai-auth); opencode's is its own built-in `openai`, which its
-- built-in ChatGPT login (CodexAuthPlugin, opencode 1.18.34) puts on the subscription whenever
-- an `oauth` credential is stored under `openai`. That is opencode's NATIVE client for the
-- provider, so it is never catalogued as a yolo row (docs/design/pi-codex-provider-shadowing.md
-- OQ-1, OQ-2: recognized by name, as packs/pi and packs/codex do): no `npm`, no `baseURL`, no
-- via row. yolo's own opencode plugin (plugins/yolo-openai-auth.js) hands that client the
-- broker's access token, so opencode never refreshes the token itself.
local opencodeCodexProvider = "openai-codex"
local opencodeOpenAIProvider = "openai"

-- OPENCODE'S OWN PROVIDERS (docs/design/pi-codex-provider-shadowing.md OQ-3, ruled 2026-10-05:
-- yolo writes no model entry over any provider an agent has built in, and the agent uses its own
-- list). packs/opencode/pack.json's `built_in_providers` names them, and core hands the answer
-- here as ctx.built_in_providers, keyed by yolo provider name: `{ id = <opencode's own id> }`
-- where opencode reaches the provider through its own client, `false` where opencode has the
-- name built in for another plan and none for this one. The id differs from the name where the
-- pack's `plans` say so: yolo's zai is z.ai's coding plan, which opencode serves as its own
-- `zai-coding-plan` (its `zai` is the metered API), and the core delivers that provider's key as
-- ZHIPU_API_KEY, the name it reads (packload.AgentEnv). opencodeOwn returns that value, or nil for
-- a provider opencode does not implement, whose row this derive writes as before.
--
-- openai-codex answers as opencode's own `openai` whatever the table says: it is the original
-- rule (OQ-1, OQ-2, opencodeCodexProvider above), so an entrypoint older than the table still
-- keeps the subscription on opencode's own client.
local function opencodeOwn(ctx, name)
  if type(name) ~= "string" or name == "" then return nil end
  local own = nil
  if type(ctx.built_in_providers) == "table" then
    own = ctx.built_in_providers[name]
  end
  if own == nil and name == opencodeCodexProvider then
    return { id = opencodeOpenAIProvider }
  end
  if own == false or type(own) == "table" then
    return own
  end
  return nil
end

local function isLocalEndpoint(url)
  if type(url) ~= "string" then return false end
  return string.find(url, "://localhost") or
         string.find(url, "://127%.0%.0%.1") or
         string.find(url, "://host%.containers%.internal") or
         string.find(url, "://169%.254%.1%.2") or
         string.find(url, "://0%.0%.0%.0")
end

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

-- opencodeLimit is a config model's `limit` from a context window and an output limit, each nil
-- when the provider states none, or nil when no limit can be written. opencode's config schema (ConfigProviderV1.Model, read from the installed
-- 1.18.34 binary, never run) declares `limit` as {context, input?, output} with both context and
-- output REQUIRED, so a limit naming one of them is not a row without a limit but a config opencode
-- refuses. A window alone carries `output` 0, opencode's own "not stated": its maxOutputTokens is
-- min(output, its 32,000 cap) or that cap, the cap it applies to a larger stated output too. An
-- output alone carries no limit at all, since a context of 0 would replace opencode's catalog
-- window for the id and turn its compaction off.
local function opencodeLimit(context, output)
  if context == nil then return nil end
  return { context = context, output = output or 0 }
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
-- extension and the wire bridge each strip before the request leaves, and opencode's row
-- names by its base id.
--
-- ⚠ DUPLICATED VERBATIM in packs/claude/derive.lua, packs/codex/derive.lua,
-- packs/opencode/derive.lua and packs/pi/derive.lua, because a derive cannot load another file
-- (the sandbox has no require and no io). internal/entrypoint/codex_model_list_test.go fails
-- when the copies differ, and when any consumer stops reading the declaration.
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

-- THE NATIVE BEDROCK BINDING (docs/design/bedrock-plumbing.md §6.2, OQ-BR1: `-p bedrock` puts an
-- agent on Bedrock through its OWN Bedrock client where it has one). opencode has one: the
-- built-in provider `amazon-bedrock` on @ai-sdk/amazon-bedrock. Every fact below was read from the
-- strings of the opencode 1.18.32 binary the launcher installs (2026-09-29), never run:
--
--   - its custom loader takes the region from `provider["amazon-bedrock"].options.region`, else
--     `AWS_REGION`, else `"us-east-1"` (AWS_DEFAULT_REGION is not read), and the profile from
--     `options.profile`, else `AWS_PROFILE`;
--   - it autoloads only when one credential signal is present: AWS_PROFILE or options.profile,
--     AWS_ACCESS_KEY_ID, AWS_BEARER_TOKEN_BEDROCK, options.apiKey, AWS_WEB_IDENTITY_TOKEN_FILE,
--     or the container-credentials variables, which aws-auth's pointer is;
--   - its getModel sends an id with a cross-Region prefix (`global.`, `us.`, `eu.`, `jp.`,
--     `apac.`, `au.`) to runtime unchanged, and routes a bare one by region;
--   - `options.endpoint` or `options.baseURL`, and a provider-level `npm`, override the per-model
--     routing, dragging bare ids off mantle (the evidence in bedrock-plumbing.md §14), so the row
--     carries neither.
--
-- So the row is `options.region` for a region the provider declares, and the models of the one
-- list opencode can call (every maker's: its client drives Converse, which serves each shipped
-- entry), each with its name and limits. Only the active set's Bedrock entry
-- (opencodeNativeBedrockEntry below), and only on opencode's own transport: a via profile
-- (`bedrock-bridge`) gets the ordinary via row instead, whose upstream the bridge composes from
-- the region (docs/design/wire-bridge-gateway.md WG-I39).
local opencodeBedrockProvider = "amazon-bedrock"

local function opencodeNativeBedrock(ctx)
  return ctx.selected_platform == "aws-bedrock" and (ctx.via_url or "") == ""
end

-- THE ACTIVE SET (docs/design/active-provider-sets.md, OQ-AP1 to OQ-AP3 ruled 2026-09-29; the
-- active set, a term that doc coins, is the ordered list of profiles one agent runs on for one
-- launch). opencode is SET-CAPABLE (packs/opencode/pack.json's `provider_sets`, AP-D15):
-- `-p opencode=zai,openrouter` or `"profile": {"opencode": ["zai", "openrouter"]}` makes every
-- listed provider live in one session, and ctx.active_set carries the entries in order. The
-- first entry, the PRIMARY, is ctx.selected_provider and ctx.profile as it always was, so
-- `model` and `small_model` below answer for it unchanged (AP-D1: a fresh session starts where
-- the first entry says), and a set of one renders byte for byte what the single profile renders
-- (AP-P1). The catalog already writes a row for every composed provider opencode can dial; what
-- the set widens is opencode's own provider filter, `enabled_providers`, and which entry the
-- native Bedrock row belongs to.
--
-- opencodeNativeBedrockEntry is the provider name of the ACTIVE SET entry opencode reaches
-- through its own amazon-bedrock client, or nil: the primary when opencodeNativeBedrock holds;
-- else the first later entry on the aws-bedrock platform, which carries no via (a via entry may
-- sit only first, AP-D9). A set names each regional platform once (AP-D12), so there is at most
-- one: opencode has ONE amazon-bedrock provider and reads ONE AWS_REGION (BR-D18).
local function opencodeNativeBedrockEntry(ctx)
  if opencodeNativeBedrock(ctx) then
    return ctx.selected_provider
  end
  if type(ctx.active_set) == "table" then
    for i, e in ipairs(ctx.active_set) do
      if i > 1 and e.platform == "aws-bedrock" then
        return e.provider
      end
    end
  end
  return nil
end

-- ⚠ WHERE A WHITELIST GOES IS STATED TWICE: here, and in this pack's program declaration
-- `exact_menu_refuses` (every list an `only` narrowed, and openai-codex's whole list), which
-- `yolo check` reads to say when a profile's switch leaves opencode's menu unnarrowed
-- (docs/design/model-lists-and-pickers.md MM-D29). Moving a whitelist here moves that declaration;
-- internal/entrypoint/unnarrowedmenus_test.go fails when the two disagree.
--
-- opencodeEnforceFor is the model-list switch (enforce_models, MM-D5) that governs provName's
-- row: the switch of the active-set entry on that provider, its own profile's, since every entry
-- is live (AP-P1); else the primary's, ctx.enforce_models, which is what every row read before
-- sets. An entrypoint older than the per-entry field hands nil there, and the row reads the
-- primary's.
local function opencodeEnforceFor(ctx, provName)
  if provName ~= ctx.selected_provider and type(ctx.active_set) == "table" then
    for _, e in ipairs(ctx.active_set) do
      if e.provider == provName and type(e.enforce_models) == "boolean" then
        return e.enforce_models
      end
    end
  end
  return ctx.enforce_models
end

-- opencodeSetProviders is `enabled_providers` for the active set: primary, the opencode provider
-- id the start `model` names, first, then each later entry's id in set order, a Bedrock entry's
-- being amazon-bedrock, opencode's own client's (opencodeNativeBedrockEntry), and an
-- openai-codex entry's being `openai`, the built-in provider opencode's ChatGPT login rides
-- (opencodeCodexRow). opencode reads the
-- key as a filter, not a list: its 1.18.32 schema describes it as "When set, ONLY these providers
-- will be enabled. All other providers will be ignored", and its provider loader keeps a provider
-- only when the key's Set has it (both read from the installed binary's strings, never run). So
-- every entry is enabled, the order is yolo's statement of the set rather than opencode's menu
-- order, and a provider outside the set is off even when a stored login would reach it.
--
-- AN ENTRY ON opencode's OWN FIRST-PARTY PROVIDER IS NAMED BY ITS ID, though the catalog wrote
-- it no row (opencodeFirstParty): a provider that names no endpoint repoints nothing and means
-- the agent's first-party API (docs/reference/protocol-resolution.md OQ-PR2), which for opencode
-- is its own built-in provider of that id, reading the key the gate delivers. The protocol gate
-- lets such an entry through on purpose (packload.ResolveProtocol), so leaving it out of the
-- filter would disable opencode's own provider and drop the entry in silence (AP-P2). Its name has
-- to be opencode's id for that provider (`anthropic`, `openai`): a name opencode has no provider
-- of enables nothing. Any other entry with no row is not named, since a bare name would enable
-- opencode's catalog provider of that id in place of the one the entry composed.
local function opencodeFirstParty(prov)
  return type(prov) == "table" and prov.platform ~= "aws-bedrock" and
    (type(prov.endpoints) ~= "table" or next(prov.endpoints) == nil)
end

local function opencodeSetProviders(ctx, primary, rows)
  local out, seen = { primary }, { [primary] = true }
  if type(ctx.active_set) ~= "table" then
    return out
  end
  local bedrockName = opencodeNativeBedrockEntry(ctx)
  for i, e in ipairs(ctx.active_set) do
    if i > 1 then
      local id = e.provider
      -- An entry on a provider opencode has built in (opencodeOwn, OQ-3) is named by opencode's
      -- own id though the catalog wrote it no row: opencode's own provider reaches it. One
      -- opencode has none of its own for (`false`) enables nothing.
      local own = opencodeOwn(ctx, e.provider)
      local builtIn = false
      if id ~= nil and id == bedrockName then
        id = opencodeBedrockProvider
      elseif id == opencodeCodexProvider then
        id = opencodeOpenAIProvider
      elseif type(own) == "table" then
        id, builtIn = own.id, true
      elseif own == false then
        id = nil
      end
      local named = builtIn or rows[id] ~= nil or
        (id == e.provider and opencodeFirstParty(ctx.providers and ctx.providers[id] or nil))
      if type(id) == "string" and id ~= "" and named and not seen[id] then
        seen[id] = true
        table.insert(out, id)
      end
    end
  end
  return out
end

-- opencodeSetHas says whether provider is the primary or any later entry of the active set: an
-- entry opencode can switch to mid-session needs its row and its login as much as the primary
-- does (docs/design/active-provider-sets.md AP-P1), the rule packs/pi's piSetHas states.
local function opencodeSetHas(ctx, provider)
  if ctx.selected_provider == provider then return true end
  if type(ctx.active_set) == "table" then
    for _, e in ipairs(ctx.active_set) do
      if e.provider == provider then return true end
    end
  end
  return false
end

-- A NON-KEY for opencode's `openai` SDK on the subscription. With yolo's credential stored, both
-- opencode's own ChatGPT fetch and yolo's plugin drop the Authorization header and send the
-- subscription's bearer, so this value is never sent. Without one (no login yet: the launcher
-- said so and continued), opencode's `openai` provider would otherwise take an ambient
-- OPENAI_API_KEY from the environment and send a ChatGPT-subscription request to the metered
-- platform API on it, the subscription/platform-key crossing
-- docs/design/pi-codex-provider-shadowing.md §1 forbids. A config `options.apiKey` outranks the
-- environment's key in opencode's SDK options (provider.ts resolveSDK, 1.18.34), so the request
-- fails instead, and the variable stays in the environment for every tool opencode runs.
local opencodeCodexNoKey = "yolo-chatgpt-subscription-needs-the-shared-login"

-- opencodeCodexModels is the subscription's ONE list (codexModelList's, ML-D1) as rows of
-- opencode's own `openai` provider: each entry keyed by the id the list spells, named by its
-- `name` fact (MM-D7), at the window the list declares. A `[1m]` variant is its own row that
-- names its base as the model id opencode sends (a config model's `id`), so the request carries
-- the wire-true id and only the window differs. The window is both `context` and `input`:
-- opencode's catalog gives these ids a 922,000-token input limit, and its compaction budgets
-- by `input` when one is set. `output` is required beside them by opencode's config schema, and
-- 0 is opencode's own "not stated" (its maxOutputTokens reads 0 as its 32,000 cap, and its own
-- ChatGPT plugin sends no output limit at all); the list declares none. An entry without a
-- window (a user's added id) carries none, so opencode's catalog answers for it.
local function opencodeCodexModels(list)
  local models = {}
  for _, e in ipairs(list) do
    local m = { name = e.name }
    if e.base then m.id = e.base end
    if e.context_window then
      m.limit = { context = e.context_window, input = e.context_window, output = 0 }
    end
    models[e.id] = m
  end
  return models
end

-- opencodeCodexRow is `provider.openai` while openai-codex is in the active set: the list's rows,
-- the non-key above, and, while the profile's switch is on (enforce_models, MM-D5), the
-- `whitelist` that makes the menu exactly the list, the one lever opencode has (MM-D7): its
-- catalog's `openai` models stay beside config rows otherwise, and its ChatGPT login keeps more
-- of them than the subscription list names. No `npm`, no `baseURL`: opencode's own client and
-- address answer, as for its own Bedrock client above.
local function opencodeCodexRow(ctx, p)
  local entry = {
    models = opencodeCodexModels(codexModelList(p)),
    options = { apiKey = opencodeCodexNoKey },
  }
  if opencodeEnforceFor(ctx, opencodeCodexProvider) ~= false and next(entry.models) ~= nil then
    local ids = {}
    for id in pairs(entry.models) do table.insert(ids, id) end
    table.sort(ids)
    entry.whitelist = ids
  end
  return entry
end

-- opencodeBedrockModels is the `models` table of the native row: each entry opencode can call,
-- keyed by its runtime id, with the display name and the limits the list declares. Facts it does
-- not declare are opencode's own (its models.dev catalog, for an id that catalog holds).
local function opencodeBedrockModels(list)
  local models = {}
  for _, e in ipairs(list) do
    local m = { name = e.facts.name or e.id }
    m.limit = opencodeLimit(tonumber(e.facts.context_window), tonumber(e.facts.max_tokens))
    models[e.id] = m
  end
  return models
end

-- THE DISPLAY NAME OF A CATALOG ROW (docs/design/model-lists-and-pickers.md MM-D7): the
-- entry's own `name` fact, or nil so the agent shows its own catalog's name for the id, or the
-- id. NEVER the yolo alias the id sits under: `default` and `fast` are yolo's pointers, and a
-- row named after one showed a model as "default". The fact is read from the alias spelled as
-- the id first, then from any other alias naming the id, in sorted alias order, the precedence
-- codexModelList's rows follow (ML-D6).
--
-- ⚠ DUPLICATED VERBATIM in packs/opencode/derive.lua, packs/pi/derive.lua and
-- packs/omp/derive.lua, because a derive cannot load another file.
-- internal/entrypoint/modeldisplayname_test.go fails when the copies differ.
local function modelDisplayName(prov, id)
  if type(prov) ~= "table" or type(prov.models) ~= "table" then return nil end
  local opts = type(prov.model_options) == "table" and prov.model_options or {}
  local function named(alias)
    local f = opts[alias]
    if type(f) == "table" and type(f.name) == "string" and f.name ~= "" then return f.name end
    return nil
  end
  if prov.models[id] == id and named(id) then return named(id) end
  local aliases = {}
  for alias, target in pairs(prov.models) do
    if type(alias) == "string" and target == id then table.insert(aliases, alias) end
  end
  table.sort(aliases)
  for _, alias in ipairs(aliases) do
    if named(alias) then return named(alias) end
  end
  return nil
end

yolo.derive("opencode", "config", function(ctx)
  local res = {}

  -- 1. MCP servers
  if next(ctx.mcp_servers) ~= nil then
    local out = {}
    for name, s in pairs(ctx.mcp_servers) do
      -- fold command + args into one array (command first, then each arg).
      local cmd = {}
      if s.command ~= nil then cmd[#cmd + 1] = s.command end
      for _, a in ipairs(s.args or {}) do cmd[#cmd + 1] = a end
      local e = { type = "local", enabled = true, command = cmd }
      -- rename env → environment, omitEmpty: only when non-empty.
      if s.env ~= nil and next(s.env) ~= nil then e.environment = s.env end
      out[name] = e
    end
    res.mcp = in_full(ctx, out)
  end

  -- 2. Providers. provOut is read again by the selection (opencodeSetProviders), which names
  -- only providers holding a row here.
  local provOut = {}
  if ctx.providers and next(ctx.providers) ~= nil then
    for name, prov in pairs(ctx.providers) do
      local baseUrl, protocol = providerEndpoint(prov)
      -- VIA (docs/design/wire-bridge-gateway.md OQ-WG6/WG7): the selected provider's entry
      -- points at this agent's route on the service its profile names. opencode speaks
      -- chat-completions there (@ai-sdk/openai-compatible), its first declared protocol and the
      -- one the via route passes through to the provider's own `openai` endpoint.
      local viaRow = (ctx.via_url ~= nil and ctx.via_url ~= "" and name == ctx.selected_provider)
      if viaRow then
        baseUrl, protocol = ctx.via_url, "openai"
      end
      -- A PROVIDER OPENCODE HAS BUILT IN IS NEVER A ROW, a via one included (opencodeOwn; OQ-3):
      -- opencode starts a config provider from its own catalog entry of the same id and lets the
      -- row's address and models override it, so a row written from yolo's declaration replaces
      -- opencode's own. openai-codex was the first case (opencodeCodexProvider above): opencode
      -- reaches it through its own `openai` client, the native row below. Under an `only` the
      -- narrowed list still becomes the whitelist of opencode's own provider (below), since that
      -- list is an override a pack or the user declared, which docs/design/pi-codex-provider-
      -- shadowing.md OQ-4 holds open.
      local own = opencodeOwn(ctx, name)
      if own ~= nil then
        -- The whitelist keeps to the providers that had a row to carry one before the ruling,
        -- the ones naming an address opencode speaks (providerEndpoint): an endpoint-less
        -- provider is opencode's own first-party API, which yolo never narrowed, and
        -- packload.UnnarrowedMenus says nothing of it (firstPartyOnly).
        local hadRow = providerEndpoint(prov) ~= nil
        baseUrl = nil
        if type(own) == "table" and name ~= opencodeCodexProvider and type(prov) == "table" and
          hadRow and prov.models_only == true and type(prov.models) == "table" and
          opencodeEnforceFor(ctx, name) ~= false then
          local ids, seen = {}, {}
          for _, id in pairs(prov.models) do
            if type(id) == "string" and id ~= "" and not seen[id] then
              seen[id] = true
              table.insert(ids, id)
            end
          end
          table.sort(ids)
          if #ids > 0 then
            provOut[own.id] = { whitelist = ids }
          end
        end
      end
      -- A BEDROCK PROVIDER GETS NO GENERIC ROW, even one a user gave an `openai` endpoint: the
      -- row speaks @ai-sdk/openai-compatible with one key, and Bedrock's credential is the AWS
      -- chain, which only opencode's own amazon-bedrock client signs with (the native row
      -- below). A via row still rides the bridge, which signs for it.
      if baseUrl and not viaRow and type(prov) == "table" and prov.platform == "aws-bedrock" then
        baseUrl = nil
      end
      if baseUrl then
        local models = {}
        local cw = nil
        local maxTokens = nil
        if type(prov.options) == "table" then
          cw = tonumber(prov.options.context_window or prov.options.max_context_tokens)
          maxTokens = tonumber(prov.options.max_tokens or prov.options.max_output_tokens)
        end
        if type(prov.models) == "table" then
          for _, modelId in pairs(prov.models) do
            -- The entry's own name, or none so opencode shows its catalog's (MM-D7). A
            -- config `name` beats the catalog's, so the alias this used to write showed an
            -- entry under `default` as "default".
            local m = { name = modelDisplayName(prov, modelId) }
            m.limit = opencodeLimit(cw, maxTokens)
            models[modelId] = m
          end
        end
        -- D10: opencode's Info schema declares baseURL/apiKey inside `options` only
        -- (packages/core/src/v1/config/provider.ts), the loader merges only
        -- `provider.options` into what the SDK sees, and resolveSDK reads
        -- `{ ...provider.options }` — a top-level spelling lists in /models and never
        -- reaches the SDK ("undefined/chat/completions cannot be parsed as a URL", zero
        -- requests). npm and models stay top-level: those ARE the two top-level fields
        -- upstream reads. `{env:VAR}` stays valid under options — substitution applies to
        -- the whole config text at load, before the schema ever sees it.
        local entry = {
          npm = opencodeSDK[protocol] or opencodeSDK.openai,
          models = models,
        }
        entry.options = { baseURL = baseUrl }
        -- A VIA ROW SENDS THE VIA SERVICE'S CALLER TOKEN, never the provider's key: the
        -- service adds that upstream itself, and demands this launch's token of every caller
        -- (docs/reference/wire-bridge.md WB-D18). An entrypoint older than
        -- ctx.via_api_key_env_name hands nil, and the row keeps the provider's reference.
        local viaKey = viaRow and ctx.via_api_key_env_name or nil
        if viaKey ~= nil and viaKey ~= "" then
          entry.options.apiKey = "{env:" .. viaKey .. "}"
        elseif prov.api_key_env_name then
          entry.options.apiKey = "{env:" .. prov.api_key_env_name .. "}"
        elseif prov.api_key then
          entry.options.apiKey = prov.api_key
        elseif type(prov.options) == "table" and prov.options.api_key then
          entry.options.apiKey = prov.options.api_key
        elseif isLocalEndpoint(baseUrl) then
          entry.options.apiKey = "local"
        end
        -- UNDER AN `only` (docs/design/model-lists-and-pickers.md §14.1, MM-D7): the list a
        -- `models` contribution narrowed (packload's `models_only`) is opencode's menu for this
        -- provider. opencode starts a config provider from its catalog entry of the same id, so
        -- the rows above add beside that catalog and cannot narrow it; `whitelist` is the one
        -- lever, and it also refuses every other model ("Model not found"), so it renders only
        -- while the profile's switch is on (enforce_models, MM-D5). Off, the menu is not
        -- narrowed. opencode orders the menu itself, newest first, and yolo fakes no
        -- release_date to reorder it.
        --
        -- NOT for a list no `only` narrowed: what a list that only adds does to opencode's own
        -- catalog is OQ-MM1's, and whether a list refuses on a gateway serving more than it is
        -- OQ-MM3's, both unruled; such a row stays as it was.
        --
        -- The switch is the one of the active-set entry on this provider (opencodeEnforceFor):
        -- every entry is live, so each keeps its own profile's.
        if prov.models_only == true and opencodeEnforceFor(ctx, name) ~= false and next(models) ~= nil then
          local ids = {}
          for id in pairs(models) do table.insert(ids, id) end
          table.sort(ids)
          entry.whitelist = ids
        end
        provOut[name] = entry
      end
    end
    -- The native Bedrock row (opencodeNativeBedrock above), the set's Bedrock entry's wherever
    -- it sits (opencodeNativeBedrockEntry), so opencode on [zai, bedrock] can switch to a
    -- Bedrock model its own catalog lacks. The region is the entry's provider's own; a region
    -- only ~/.aws/config holds reaches opencode as AWS_REGION through the credential gate's
    -- region fill, which reads for the first entry needing one (AP-D14), and the region
    -- pre-flight refuses a launch that delivers opencode none (BR-D18).
    local bedrockName = opencodeNativeBedrockEntry(ctx)
    if bedrockName and provOut[opencodeBedrockProvider] == nil then
      local p = ctx.providers[bedrockName]
      local entry = { models = opencodeBedrockModels(callableModels(p, nil)) }
      if type(p) == "table" and type(p.region) == "string" and p.region ~= "" then
        entry.options = { region = p.region }
      end
      -- Under an `only`, the same whitelist as a generic row's (above): these rows add beside
      -- opencode's own Bedrock catalog and cannot narrow it, so the whitelist is the menu, and
      -- it refuses too, so only while the entry's profile's switch is on (MM-D5, MM-D7).
      if type(p) == "table" and p.models_only == true and opencodeEnforceFor(ctx, bedrockName) ~= false and next(entry.models) ~= nil then
        local ids = {}
        for id in pairs(entry.models) do table.insert(ids, id) end
        table.sort(ids)
        entry.whitelist = ids
      end
      provOut[opencodeBedrockProvider] = entry
    end
    -- The subscription's native row (opencodeCodexRow), whenever openai-codex is in the set, so
    -- opencode on [zai, codex] can switch to it mid-session. It owns opencode's `openai` id
    -- outright: a yolo provider of that name cannot share it, and its generic row would put
    -- the subscription on another SDK and address.
    if opencodeSetHas(ctx, opencodeCodexProvider) and type(ctx.providers[opencodeCodexProvider]) == "table" then
      provOut[opencodeOpenAIProvider] = opencodeCodexRow(ctx, ctx.providers[opencodeCodexProvider])
    end
    if next(provOut) ~= nil then
      res.provider = in_full(ctx, provOut)
    end
  end

  -- 3. The selection — `model`, opencode's OWN selection key, source-verified from the
  -- installed release (upstream v1.18.18, the tag the shipped binary reports):
  -- packages/core/src/v1/config/config.ts:74-76 declares it "Model to use in the format
  -- of provider/model", split on the FIRST slash at model.ts:33-39, and an unknown prefix
  -- is a ModelNotFoundError with no silent fallback (docs/reference/providers.md §3
  -- opencode row). The provider half is the catalog key above, the model half is a bare
  -- model id, and one slash joins them.
  --
  -- It travels under the RESERVED `selection` key of the computed layer, exactly as codex's
  -- and pi's do, and for the same reason: a plain computed key is re-asserted by every
  -- boot, and opencode lets a user change the model interactively mid-session, so a key
  -- yolo re-asserted would silently revert that choice on the next launch
  -- (docs/reference/providers.md §5.1, the hazard OQ-CS2 names). The stateful render
  -- takes the namespace, decides per key, and lifts the winner onto the surface root —
  -- opencode.json shows `model` at top level, where opencode reads it. The namespace is an
  -- implementation detail of the layer, never of the file.
  --
  -- The gate is the catalog's own: opencode consumes no wire_api, so "names a URL
  -- opencode can dial" (providerEndpoint) is the whole of reachability, and there is no
  -- second predicate to lift out the way codex and pi need one. That is load-bearing
  -- rather than tidy — an unknown provider prefix is a hard ModelNotFoundError in
  -- opencode, so a selection whose provider the catalog dropped would be a config that
  -- fails at first request, not a preference opencode quietly ignores.
  --
  -- OQ-CS2 is the GUARD, not a default: no active profile at opencode's CLI name, or a
  -- selected provider the gate drops, writes nothing — not a default, not a clear. The
  -- no-profile case is opencode's own, and what opencode owns is a persisted interactive
  -- choice (~/.local/state/opencode/model.json) that `model` unset falls back to.
  --
  -- The model half is the derive's business (OQ-CS3), and the fallback ladder is
  -- shortest-claim-first: the alias the active profile's `model` option names (OQ-CS4 —
  -- for opencode that is a key of the provider's models table, the same keying the
  -- catalog half above already builds); else the provider's declared `default` alias;
  -- else the ONE model it declares, where "which model" has only one possible answer;
  -- else nothing — and with `model` being a single key, nothing means NO `model` at all,
  -- never a half one naming a provider with no model under it. The provider filter is written
  -- either way (enabled_providers, below): with no model yolo names the providers and opencode
  -- chooses within them (docs/design/active-provider-sets.md §4.4, AP-D17). The one-model
  -- answer belongs to the DEFAULT ask only: a profile that named an alias the provider
  -- does not declare asked a question the table cannot answer, and "some other model
  -- that happened to be alone" is not the answer — that is the same honest degradation
  -- as declaring nothing at all.
  if ctx.selected_provider ~= nil and ctx.selected_provider ~= "" then
    local p = ctx.providers and ctx.providers[ctx.selected_provider] or nil
    if opencodeNativeBedrock(ctx) then
      -- Bedrock through opencode's own client: its built-in provider, on the model the profile
      -- names among the entries opencode can call, else the list's first. yolo picks here
      -- because opencode's own default for `amazon-bedrock` is unread
      -- (docs/design/model-lists-and-pickers.md §4, "yes until measured"), and a session left to
      -- an unknown default may start on a bare id runtime refuses. The small model is the same
      -- one: the list states no cheaper tier. enabled_providers names the set, as below.
      local model = callableModel(p, callableModels(p, nil), ctx.profile, true)
      local sel = { enabled_providers = opencodeSetProviders(ctx, opencodeBedrockProvider, provOut) }
      if model then
        local qualified = opencodeBedrockProvider .. "/" .. model
        sel.model = qualified
        sel.small_model = qualified
      end
      res.selection = sel
    elseif ctx.selected_provider == opencodeCodexProvider then
      -- The subscription through opencode's own `openai` client (opencodeCodexRow): the model
      -- codexDefault picks, the ONE rule every codex consumer shares, so opencode starts where
      -- claude, codex and pi do; the small model by the alias rule the generic branch below
      -- follows, which the shipped list (no `fast` alias) answers with the same model. Never
      -- the generic branch, which would name `openai-codex/<id>`, a provider opencode has no row
      -- of. No row (the table holds no openai-codex entry, which the launch refuses first)
      -- writes nothing, OQ-CS2's guard.
      if provOut[opencodeOpenAIProvider] ~= nil then
        local model = codexDefault(codexModelList(p), ctx.profile)
        local sel = { enabled_providers = opencodeSetProviders(ctx, opencodeOpenAIProvider, provOut) }
        if model then
          sel.model = opencodeOpenAIProvider .. "/" .. model
          local models = type(p) == "table" and type(p.models) == "table" and p.models or {}
          local smallAlias = ctx.profile and ctx.profile.small_model
          local small = (smallAlias and (models[smallAlias] or smallAlias)) or
            models.haiku or models.fast or models.small or model
          sel.small_model = opencodeOpenAIProvider .. "/" .. small
        end
        res.selection = sel
      end
    elseif opencodeOwn(ctx, ctx.selected_provider) ~= nil then
      -- A PROVIDER OPENCODE HAS BUILT IN (opencodeOwn; OQ-3): the catalog wrote it no row, so
      -- opencode runs it on its own client, address and model list, and the selection names
      -- nothing from yolo's list. enabled_providers names opencode's own id (zai-coding-plan for
      -- yolo's zai), so opencode's menu is that provider's own models. `model` is the profile's
      -- `model` option as opencode's own model id, taken literally and never resolved through
      -- yolo's alias table, and `small_model` the profile's `small_model` the same way; with
      -- neither, opencode starts within the enabled providers by its own rule (AP-D17) and picks
      -- its own small model. Under an `only` the session starts on the profile's model when the
      -- list holds it, else the list's `default` alias, else its first entry, as the generic
      -- branch below does, since
      -- that list is an override a pack or the user declared (OQ-4). `false` is a provider
      -- opencode has built in for another plan with none of its own for this one: no selection,
      -- and the launch's profile line says the profile cannot reach opencode's own client.
      local own = opencodeOwn(ctx, ctx.selected_provider)
      if own then
        local function literal(v)
          if type(v) == "string" and v ~= "" and v ~= "default" then return v end
          return nil
        end
        local start = literal(ctx.profile and ctx.profile.model)
        if type(p) == "table" and p.models_only == true and type(p.models) == "table" then
          local list = callableModels(p, nil)
          local held = false
          for _, e in ipairs(list) do
            held = held or e.id == start
          end
          if not held then
            start = p.models.default or (list[1] and list[1].id)
          end
        end
        local sel = { enabled_providers = opencodeSetProviders(ctx, own.id, provOut) }
        if start then
          sel.model = own.id .. "/" .. start
          local small = literal(ctx.profile and ctx.profile.small_model)
          if small then
            sel.small_model = own.id .. "/" .. small
          end
        end
        res.selection = sel
      end
    elseif providerEndpoint(p) then
      local alias = (ctx.profile and ctx.profile.model) or "default"
      local modelID = nil
      if type(p) == "table" and type(p.models) == "table" then
        if p.models[alias] then
          modelID = p.models[alias]
        elseif p.models_only == true then
          -- UNDER AN `only` the list's DEFAULT ENTRY answers (docs/design/model-lists-and-
          -- pickers.md §7.2): the profile's model when the list holds it (above), else the
          -- `default` alias, else the list's first entry in its order (callableModels'). An only
          -- that dropped the profile's model must still start opencode on the list, and the
          -- menu follow the selection, as copilot, codex, claude and oh-omp do under one.
          local first = callableModels(p, nil)[1]
          modelID = p.models.default or (first and first.id)
        elseif alias == "default" then
          local count
          -- The VALUES are the model ids (the keys are the aliases); see the catalog
          -- half above, which builds opencode's models table keyed the same way.
          for _, id in pairs(p.models) do
            modelID, count = id, (count or 0) + 1
          end
          if count ~= 1 then
            modelID = nil
          end
        end
      end
      local sel = {}
      if modelID then
        sel.model = ctx.selected_provider .. "/" .. modelID
        local smallAlias = (ctx.profile and ctx.profile.small_model)
        local smallID = nil
        if smallAlias and type(p.models) == "table" then
          smallID = p.models[smallAlias] or smallAlias
        elseif type(p.models) == "table" then
          smallID = p.models.haiku or p.models.fast or p.models.small or modelID
        else
          smallID = modelID
        end
        sel.small_model = ctx.selected_provider .. "/" .. smallID
      end
      -- THE MENU FOLLOWS THE SELECTION (docs/reference/providers.md OQ-CN4,
      -- "both, named separately"): the credential gate withholds every other provider's
      -- key from opencode, and opencode registers a catalog row without an auth check, so
      -- without this its menu would still offer providers it can no longer call.
      -- enabled_providers is opencode's own HARD key — "When set, ONLY these providers
      -- will be enabled" — so this is the ergonomic half of the ruling, never a model list:
      -- it names the providers the profile selected and nothing else: the one provider of
      -- a single profile, and every provider of an active set, the primary first
      -- (opencodeSetProviders; docs/design/active-provider-sets.md §4.4, "enabled_providers
      -- names every provider in the set, in order"). It rides the selection beside `model`,
      -- so a deselect clears it with the model (OQ-PSW2) and a set that loses an entry
      -- rewrites it whole (§4.10).
      --
      -- IT IS WRITTEN WHETHER OR NOT A MODEL RESOLVES (AP-D17): a primary that declares no
      -- models (openrouter and kilo ship none) has no pick to give, so yolo names the providers
      -- and no model, and opencode chooses within them (§4.4). Its start order, read from the
      -- 1.18.32 binary's strings, is the config's `model`, then a recent pick whose provider is
      -- loaded, then the first loaded provider's default, so a saved choice inside the set is
      -- kept and one outside it is passed over (AP-P3, "the set is the boundary").
      sel.enabled_providers = opencodeSetProviders(ctx, ctx.selected_provider, provOut)
      res.selection = sel
    end
  end

  return res
end)

-- env: the environment opencode's own process launches with, run host-side by the env runner
-- (packload.AgentEnv) and delivered in opencode's own env file. One fact: THE OPENAI LOGIN
-- PRELAUNCH. opencode's launcher reads YOLO_AUTH_PRELAUNCH_OPENCODE_FLAG/_PATH before it execs
-- opencode, and writes the shared OpenAI login's view into opencode's auth file (the jail's
-- launcher, entrypoint's agentAuthPrelaunchShellFn), or hands opencode the host credential socket
-- (`yolo host`, internal/openaiauthhost). The view is the `oauth` credential opencode's built-in
-- ChatGPT support keys on, with the broker's generation marker as its refresh value, and
-- plugins/yolo-openai-auth.js serves every request from the broker, so opencode never holds or
-- spends the refresh token (docs/reference/agent-credentials.md OQ-OA2).
--
-- The path is where opencode's Auth store reads with XDG_DATA_HOME unset, which yolo never sets
-- for an agent: $XDG_DATA_HOME/opencode/auth.json, else ~/.local/share/opencode/auth.json
-- (src/auth/index.ts, opencode 1.18.34). An XDG_DATA_HOME of the user's own moves opencode's
-- store away from the view, and the launch then writes a file opencode does not read.
--
-- Keyed on the PROVIDER, never on the profile's name (docs/design/providers-and-profiles-redesign.md
-- OQ-BR8, PP-D2), and wanted when openai-codex is ANY entry of the active set (AP-P1), packs/pi's
-- rule: opencode on [zai, codex] can switch to the subscription mid-session.
yolo.env("opencode", function(ctx)
  local env = {}
  if opencodeSetHas(ctx, opencodeCodexProvider) then
    env.YOLO_AUTH_PRELAUNCH_OPENCODE_FLAG = "--opencode-auth"
    env.YOLO_AUTH_PRELAUNCH_OPENCODE_PATH = ".local/share/opencode/auth.json"
  end
  return env
end)
