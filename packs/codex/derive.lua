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
    else
      local p = ctx.providers and ctx.providers[ctx.selected_provider] or nil
      if codexReachable(p) then
        local sel = { model_provider = ctx.selected_provider }
        local m = type(p) == "table" and p.models or nil
        local alias = (ctx.profile and ctx.profile.model) or "default"
        if type(m) == "table" and m[alias] then
          sel.model = m[alias]
        end
        res.selection = sel
      end
    end
  end

  return res
end)
