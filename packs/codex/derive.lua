-- codex: project MCP servers and model_providers into codex's TOML format.

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

-- The DIALECT MAP (docs/reference/providers.md §3.4 / OQ-PT1): yolo's canonical wire_api
-- → the value codex reads from model_providers.<id>.wire_api. Every row is a measured
-- fact about codex, carried here because a dialect map with no provenance is the same
-- unverified assertion in a new location:
--
--   openai-responses → "responses"  codex's one value. `chat` was removed from the
--                                   product; verified from source: codex-cli 0.145.0
--                                   binary, strings @0x7B7B47, 2026-08-20
--                                   (docs/research/local-model-endpoints.md §"Codex CLI").
--
-- The canonical names ABSENT here are the protocols codex cannot speak, and the caller
-- drops the whole entry for them rather than emitting a half-configured one: `anthropic`
-- (Anthropic Messages) and `openai-chat-completions` (chat completions) — chat is the
-- value codex removed, so no spelling of it works. An unknown value (a newer build's
-- canonical name) is unspeakable the same way, which is the safe direction: codex loses a
-- catalog row it could not have used.
local codexDialect = {
  ["openai-responses"] = "responses",
}

-- codexWireAPI maps one canonical protocol to the wire_api codex reads, or nil when this
-- derive must emit NO entry for the provider at all. The two nils mean different things
-- and only one of them is a default:
--
--   nothing declared → "responses", the one value codex accepts. The default is a fact
--                      about CODEX's vocabulary, not about the endpoint's HTTP surface:
--                      zai OQ-Z1 (2026-09-01, authenticated probe: POST /v4/responses is
--                      404 on both z.ai routes while /v4/chat/completions completes) says
--                      what z.ai speaks, and an endpoint whose protocol codex cannot speak
--                      must SAY SO in its wire_api and lose the entry — not inherit a
--                      default that hides the mismatch behind a 404 at first request.
--   declared, no row → nil. Emitting an entry would hand codex a provider it cannot
--                      reach; dropping it is the honest degradation (design §3.4).
--
-- THE SHIPPED CONSEQUENCE (design §3.3): packs/zai's openai endpoint declares
-- openai-chat-completions, so zai yields NO codex entry at all — z.ai speaks chat
-- completions only, codex speaks responses only, and no wire_api value makes that pairing
-- work. That is a fact about the world to record, not a bug to fix here.
local function codexWireAPI(canonical)
  if canonical == nil then
    return "responses"
  end
  return codexDialect[canonical]
end

-- The provider's URL for the protocol codex speaks — `openai-responses` (preferred) or
-- `openai`, per docs/reference/providers.md (the per-agent table). The single-protocol `base_url`
-- shorthand wins; otherwise the openai-responses or openai endpoint. Total over non-tables
-- so the call site stays a one-line gate. Returns nil when the provider names no URL an
-- openai-speaking agent can use, which is what keeps that gate honest: an endpoints-only
-- provider still reaches the catalog (the pre-endpoints gate on prov.base_url silently
-- dropped it), while a provider whose only endpoint speaks anthropic would emit an entry
-- with no URL.
--
-- ⚠ THE SHORTHAND ARM STAYS, AND IT IS NOT DEAD CODE. The key is REMOVED from user config
-- (docs/reference/protocol-resolution.md), but `validateProviderShorthandRetired` is an
-- ERROR ON THE HOST AND A WARNING IN A JAIL — in here the config is the host-generated
-- snapshot, so a jail launched by a host `yolo` that predates the removal still carries the
-- key and `knownProviderKeys` still passes it through. Deleting this arm turns that jail's
-- provider into one with no address. Measured 2026-09-18: removing it reddens six cases in
-- internal/entrypoint/selectionapply_test.go. It goes when the snapshot can no longer carry
-- the key, not when the host config refuses it.
local function providerEndpoint(prov)
  if type(prov) ~= "table" then return nil end
  if prov.base_url then
    return prov.base_url, prov.wire_api
  end
  local ep = prov.endpoints and (prov.endpoints["openai-responses"] or prov.endpoints.openai) or nil
  if type(ep) == "table" and ep.base_url then
    return ep.base_url, ep.wire_api
  end
  return nil
end

-- codexReachable is THE gate both halves below ask — can codex reach this provider at
-- all — so the catalog and the selection cannot grow two answers to it. It returns the
-- URL and the wire_api codex would use, or nil when the provider names no URL for the
-- protocol codex speaks (anthropic-only, or none at all) or names one whose wire_api codex
-- cannot speak. Reusing the catalog's own predicate for the selection is what makes a
-- half-selection unrepresentable: `model_provider = <id>` with no `model_providers.<id>`
-- row underneath it is a config codex refuses at startup, which is the catalog's dropped
-- entry reintroduced one level up — so a provider that loses its catalog row loses its
-- selection key with it.
--
-- Total over non-tables, like providerEndpoint: a selected name that is absent from the
-- composed table (a profile whose provider the table does not hold — which
-- creates no requirement of its own) reads as nil here, and nil selects nothing.
local function codexReachable(prov)
  if type(prov) ~= "table" then return nil end
  local baseUrl, wireApi = providerEndpoint(prov)
  -- An endpoint's own wire_api is the per-protocol fact; the provider-level one only
  -- speaks for the shorthand.
  local api = codexWireAPI(wireApi or prov.wire_api)
  if baseUrl and api then
    return baseUrl, api
  end
  return nil
end

-- THE NATIVE BEDROCK BINDING (docs/design/bedrock-plumbing.md §6.2, OQ-BR1: `-p bedrock` puts an
-- agent on Bedrock through its OWN Bedrock client where it has one). codex has one: the
-- built-in provider `amazon-bedrock-runtime`, which speaks Responses to
-- https://bedrock-runtime.<region>.amazonaws.com/openai/v1 and signs from the AWS credential
-- chain, or sends AWS_BEARER_TOKEN_BEDROCK first when it is set. Every fact below was read
-- from the strings of the codex-cli 0.158.0 binary the launcher installs (2026-09-29), never
-- run:
--
--   - the built-in ids, in order: `responses` `openai` `amazon-bedrock` `amazon-bedrock-runtime`
--     `ollama`; `amazon-bedrock` is the mantle client, which yolo does not ship (DIR-BR3);
--   - `model-provider/src/amazon_bedrock/runtime.rs` beside `https://bedrock-runtime.` and
--     `.amazonaws.com/openai/v1`, the endpoint family yolo ships;
--   - the override guard, verbatim: "only supports changing `base_url`, `auth`,
--     `http_headers`, `aws.profile`, `aws.region`, `aws.credential_export`, and
--     `aws.auth_refresh`; other non-default provider fields are not supported" (trap D3), so
--     the one row this writes carries `aws.region` and nothing else, and the generic row's
--     `name`, `wire_api` and `env_key` never appear on it;
--   - the region order: "`model_providers.amazon-bedrock.aws.region`, `AWS_REGION`, or
--     `AWS_DEFAULT_REGION`", so a region only the environment carries needs no row at all.
--     ⚠ INFERRED for this provider: that message names the mantle provider's key
--     (`amazon-bedrock`) and bearer-token auth, and no string read names runtime's own order.
--
-- The model is a runtime id from the provider's list (P3: one spelling per entry), sent as
-- `model`. An id codex's own catalog does not hold runs on "fallback model metadata" (trap
-- D4); that the id reaches runtime unchanged is INFERRED from that message, and `codex doctor`
-- is the check.
--
-- ONLY the selected provider, and only when codex's own client is the transport. A via profile
-- (`bedrock-bridge`) forces the wire bridge instead: codex then gets the via row every provider
-- does, speaking Responses at its via URL (codexViaBedrock), which runtime serves for OpenAI's
-- models. It is written even though the provider names no endpoint, because the bridge, not
-- codex, is the one that reaches Bedrock on that route, and it never quietly runs codex
-- natively instead. The bridge composes that route's upstream from the region, runtime's own
-- /openai/v1, which codex's Responses reach at /responses (docs/design/wire-bridge-gateway.md
-- WG-I39).
-- codex has one built-in Bedrock provider, so a second Bedrock provider in the table gets no
-- native row of its own: two rows cannot share one built-in id.
local codexBedrockProvider = "amazon-bedrock-runtime"

local function codexNativeBedrock(ctx)
  return ctx.selected_platform == "aws-bedrock" and (ctx.via_url or "") == ""
end

-- codexViaBedrock: the selected provider is a Bedrock one and codex's profile routes it through a
-- via service, so its row is the via row whatever endpoints the provider names.
local function codexViaBedrock(ctx, name)
  return ctx.selected_platform == "aws-bedrock" and (ctx.via_url or "") ~= "" and
    name == ctx.selected_provider
end

-- codex's Bedrock client drives the Responses API, and the evidence covers OpenAI's models on
-- it: Anthropic's are served on runtime by Messages and Converse alone (the Claude Opus 5.5 AWS
-- card, read 2026-09-29). Widened when a turn measures another maker (done-condition 5).
local codexBedrockMakers = { openai = true }

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

yolo.derive("codex", "config", function(ctx)
  local res = {}

  -- 1. MCP servers
  if next(ctx.mcp_servers) ~= nil then
    local out = {}
    for name, s in pairs(ctx.mcp_servers) do
      local e = {}
      e.command = s.command
      -- copy args, default to [] when absent (empty_array → JSON/TOML []).
      e.args = s.args or ctx.empty_array
      -- copy env, omitEmpty: only when non-empty.
      if s.env ~= nil and next(s.env) ~= nil then e.env = s.env end
      out[name] = e
    end
    res.mcp_servers = in_full(ctx, out)
  end

  -- 2. Model providers
  if ctx.providers and next(ctx.providers) ~= nil then
    local provOut = {}
    for name, prov in pairs(ctx.providers) do
      -- openai-codex is Codex's native subscription provider backed by OAuth
      -- credentials, not a custom third-party endpoint with an API key.
      if name ~= "openai-codex" then
        local baseUrl, api = codexReachable(prov)
        if codexViaBedrock(ctx, name) then
          -- Reachable through the bridge's Responses pass-through, which is the one route
          -- the via re-points it to (codexViaBedrock above).
          baseUrl, api = ctx.via_url, "responses"
        end
        -- VIA (docs/design/wire-bridge-gateway.md §4.1, WG-I20/WG-I22): when codex's active
        -- profile routes through a service, the SELECTED provider's row points at codex's own
        -- route on it, ctx.via_url, which passes codex's Responses requests through to the
        -- provider's own Responses endpoint. Only a provider codex can reach at all rides it:
        -- the same gate as the catalog, so a chat-only provider still gets no row. Every
        -- other row is untouched — via is one profile's choice.
        local viaRow = (baseUrl ~= nil and ctx.via_url ~= nil and ctx.via_url ~= "" and
          name == ctx.selected_provider)
        if viaRow then
          baseUrl = ctx.via_url
        end
        -- A BEDROCK PROVIDER GETS NO GENERIC ROW, even one a user gave an `openai` endpoint: the
        -- row's one credential is an env_key, and Bedrock's is the AWS credential chain, which
        -- only codex's own amazon-bedrock-runtime client signs with (the native binding below).
        -- A via row still rides the bridge, which signs for it.
        if baseUrl and not viaRow and type(prov) == "table" and prov.platform == "aws-bedrock" then
          baseUrl = nil
        end
        if baseUrl then
          local displayName = name
          if type(prov.name) == "string" and prov.name ~= "" then
            displayName = prov.name
          end
          local entry = {
            name = displayName,
            base_url = baseUrl,
            wire_api = api,
          }
          -- A via row's env_key is the via service's CALLER TOKEN, never the provider's key:
          -- the service holds the provider's credential and adds it upstream itself, and it
          -- demands this launch's token of every caller (docs/reference/wire-bridge.md
          -- WB-D18), which codex sends as its bearer. codex fails each request when a declared
          -- env_key is unset (docs/design/wire-bridge-gateway.md WG-I22), and that cannot fire
          -- here: a via profile brings in the bridge, a service with a jail daemon, so the launch
          -- always writes this variable into the per-entry channel every jail process inherits.
          -- An entrypoint older than ctx.via_api_key_env_name hands nil, and the
          -- row then names no env_key, as it did before: with neither env_key nor
          -- requires_openai_auth, codex sends no Authorization at all (codex 0.157.0,
          -- model-provider/src/auth.rs resolve_provider_auth).
          if viaRow then
            local viaKey = ctx.via_api_key_env_name
            if viaKey ~= nil and viaKey ~= "" then
              entry.env_key = viaKey
            end
          elseif prov.api_key_env_name then
            entry.env_key = prov.api_key_env_name
          end
          provOut[name] = entry
        end
      end
    end
    -- The native Bedrock row (codexNativeBedrock above): the built-in provider's override,
    -- carrying `aws.region` when the selected provider declares one and nothing when the region
    -- arrives in the environment, which codex reads itself.
    if codexNativeBedrock(ctx) and provOut[codexBedrockProvider] == nil then
      local p = ctx.providers[ctx.selected_provider]
      if type(p) == "table" and type(p.region) == "string" and p.region ~= "" then
        provOut[codexBedrockProvider] = { aws = { region = p.region } }
      end
    end
    if next(provOut) ~= nil then
      res.model_providers = in_full(ctx, provOut)
    end
  end

  -- 3. The selection — model_provider and model, codex's OWN selection keys, verified from
  -- the codex CLI binary 2026-08-20 (docs/research/local-model-endpoints.md §"Codex CLI";
  -- docs/reference/providers.md §3 codex row). An active profile names the provider
  -- it selects; a provider codex can reach becomes the selection, and the model is the id
  -- under the alias the profile's `model` option names (OQ-CS4: what an option means is
  -- the derive's business), or under the provider's declared `default` alias when the
  -- profile carries none (OQ-CS3: core resolves no model — the fallback is the derive's
  -- business, and `default` stays an ordinary open-vocabulary alias).
  --
  -- They travel under the RESERVED `selection` key of the computed layer, not as plain
  -- computed keys, and that is load-bearing rather than cosmetic. A plain computed key is
  -- re-asserted by every boot — right for an MCP table, which is yolo's own output, and
  -- exactly wrong for a model the user can change interactively mid-session (`/model`), so
  -- that a key yolo re-asserted would silently revert their choice on the next launch
  -- (docs/reference/providers.md §5.1, the hazard OQ-CS2 names). The stateful render
  -- takes the namespace, decides per key — write on activation, never on absence, and a
  -- user's interactive edit stands until a NEW selection value differs from the last one
  -- yolo wrote — and lifts the winners onto the surface root, so config.toml still shows
  -- `model_provider` and `model` at top level where codex reads them. The namespace is an
  -- implementation detail of the layer, never of the file.
  --
  -- OQ-CS2 is the GUARD, not a default: when no profile is active at codex's CLI name,
  -- nothing selection-shaped is written — not a default, not a clear. The no-profile case
  -- is the agent's own (docs/reference/providers.md — Selection: write on activation, never on absence). And when the selected
  -- provider is not codex-reachable, the SAME gate that keeps it out of the catalog keeps
  -- it out of the selection: no keys at all, never a `model_provider` naming a provider
  -- whose row the catalog dropped — codex refuses that config at startup.
  --
  -- openai-codex (docs/reference/providers.md#selecting-openai-codex-for-codex) is Codex's native first-party
  -- subscription provider. When selected, the selection asserts `model` directly without
  -- `model_provider`, allowing Codex CLI to authenticate natively via OAuth while clearing
  -- any third-party `model_provider` residue.
  --
  -- The model is codexDefault's: the profile's own `model`, else the FIRST id of the one
  -- declared list (packs/openai-auth/pack.json), the same default claude and pi start on.
  -- A list emptied by the user's config and a profile naming nothing write NO model, so
  -- codex falls back to its own default, and the deselect rule (OQ-PSW2) clears the model
  -- an earlier launch wrote.
  if ctx.selected_provider ~= nil and ctx.selected_provider ~= "" then
    if ctx.selected_provider == "openai-codex" then
      local list = codexModelList(ctx.providers and ctx.providers["openai-codex"])
      local model = codexDefault(list, ctx.profile)
      res.selection = model and { model = model } or {}
    elseif codexNativeBedrock(ctx) then
      -- Bedrock through codex's own client: the built-in provider, and the model among the
      -- entries codex can call (callableModels, codexBedrockMakers), the profile's own when it
      -- names one of them, else the list's first. yolo picks here because codex's own default
      -- is a first-party slug, not a Bedrock id, so a session left to it would not start
      -- (docs/design/model-lists-and-pickers.md OQ-ML2). A list with nothing codex can call
      -- writes the provider alone, and codex resolves its own model.
      local p = ctx.providers and ctx.providers[ctx.selected_provider] or nil
      local sel = { model_provider = codexBedrockProvider }
      local model = callableModel(p, callableModels(p, codexBedrockMakers), ctx.profile, true)
      if model then
        sel.model = model
      end
      res.selection = sel
    elseif codexViaBedrock(ctx, ctx.selected_provider) then
      -- The same provider through the bridge: its via row, on the same OpenAI entries, since
      -- the route passes codex's Responses requests through unchanged.
      local p = ctx.providers and ctx.providers[ctx.selected_provider] or nil
      local sel = { model_provider = ctx.selected_provider }
      local model = callableModel(p, callableModels(p, codexBedrockMakers), ctx.profile, true)
      if model then
        sel.model = model
      end
      res.selection = sel
    else
      local p = ctx.providers and ctx.providers[ctx.selected_provider] or nil
      if codexReachable(p) then
        local sel = { model_provider = ctx.selected_provider }
        local m = type(p) == "table" and p.models or nil
        local alias = (ctx.profile and ctx.profile.model) or "default"
        if type(m) == "table" and m[alias] then
          sel.model = m[alias]
        elseif type(m) == "table" and p.models_only == true then
          -- UNDER AN `only` (docs/design/model-lists-and-pickers.md §14.1, codex): the narrowed
          -- list's default entry (§7.2), the `default` alias else the first entry, when the only
          -- dropped the profile's model. The selection is all codex gets for now: its exact menu
          -- is a catalog filtered at prelaunch (MM-D9), not built.
          local first = codexModelList(p)[1]
          sel.model = m.default or (first and first.id)
        end
        res.selection = sel
      end
    end
  end

  return res
end)

-- model-list (~/.codex/yolo-model-list.json): yolo's list for the provider codex's own catalog
-- carries, which the codex launcher turns into codex's model menu before it execs codex
-- (packs/codex/pack.json's `model_menu`; docs/design/model-lists-and-pickers.md MM-D9, MM-D22).
-- The launcher reads codex's own catalog (`codex debug models --bundled`), keeps its entries for
-- these ids in this order, and hands codex the file with `-c model_catalog_json=…`, so codex's
-- /model picker offers exactly the list, each entry with the prompt text codex's catalog holds.
--
-- ONLY openai-codex. `--bundled` prints codex's OpenAI catalog whatever provider is selected
-- (codex 0.159.2, cli/src/main.rs run_debug_models_command, MEASURED 2026-09-30), and its ids are
-- the subscription's. Bedrock runtime's catalog is built in memory from other slugs
-- (model-provider/src/amazon_bedrock/runtime_catalog.rs), so a Bedrock list would name ids the
-- printed catalog lacks and every entry would be left out: MM-D9's measurement for Bedrock did
-- not hold, and a provider whose ids codex's catalog does not carry keeps `model` alone. The
-- `[1m]` rows are the other consumers' spelling, and codex's catalog has none.
--
-- {} for every other selection is "no menu this launch", and the launcher then adds no flag.
yolo.derive("codex", "model-list", function(ctx)
  if ctx.selected_provider ~= "openai-codex" then return {} end
  local models = {}
  for _, e in ipairs(codexModelList(ctx.providers and ctx.providers["openai-codex"])) do
    if e.base == nil then
      table.insert(models, { id = e.id, name = e.name })
    end
  end
  if #models == 0 then return {} end
  return { models = models }
end)
