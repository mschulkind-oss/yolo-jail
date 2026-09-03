-- pi: render ~/.pi/agent/models.json from declared providers, and write the selection
-- pair into ~/.pi/agent/settings.json when a profile is active at pi's CLI name.

-- The DIALECT MAP (docs/reference/providers.md §3.4 / OQ-PT1): yolo's canonical wire_api
-- → the value pi reads from providers.<id>.api. Every row is a measured fact about pi,
-- carried here because a dialect map with no provenance is the same unverified assertion
-- in a new location. PROVENANCE: pi's api vocabulary is a RUNTIME registry, not the
-- schema — `BUILTIN_APIS` (pi-ai/dist/compat.js:108-119) lists ten ids, of which the ones
-- below are the three yolo's canonical names translate to; verified from pi 0.84.4
-- (npm-extracted, the CLI itself never run), 2026-09-02. The schema's `api` is a free
-- string (pi-coding-agent/dist/core/model-config.js:173), so a value outside this map
-- would LOAD cleanly and die at first request with "No API provider registered for api".
local piDialect = {
  ["anthropic"] = "anthropic-messages",
  ["openai-chat-completions"] = "openai-completions",
  ["openai-responses"] = "openai-responses",
}

-- piAPI maps one canonical protocol to pi's `api` value, or nil when pi has no spelling
-- for it (the caller then emits no entry — a half-configured provider would fail at first
-- request from a jail that booted green). One deliberate default sits in front of the map:
-- NOTHING declared → "openai-completions". That is THIS DERIVE'S choice, not pi's — pi has
-- NO default, and an absent api is a composition error that deletes the provider from the
-- model list (pi-coding-agent/dist/core/provider-composer.js:48-52), so leaving the field
-- out is not an option the way it is for an agent with a default of its own. The openai
-- route is the one the packs ship for openai-speaking agents (zai-plumbing.md §5), so it
-- is what an undeclared endpoint means here.
local function piAPI(canonical)
  if canonical == nil then
    return "openai-completions"
  end
  return piDialect[canonical]
end

-- The provider's URL for the protocol THIS agent speaks — `openai`, per the resolution
-- table in zai-plumbing.md §5 (claude is the one `anthropic` consumer, and it has no
-- derive). The single-protocol `base_url` shorthand wins; otherwise the openai endpoint.
-- Total over non-tables so the call site stays a one-line gate. Returns nil when the
-- provider names no URL an openai-speaking agent can use, which is what keeps that gate
-- honest: an endpoints-only provider still reaches the catalog (the pre-endpoints gate
-- on prov.base_url silently dropped it), while a provider whose only endpoint speaks a
-- protocol pi cannot would emit an entry with no URL — a provider it cannot reach.
local function providerEndpoint(prov)
  if type(prov) ~= "table" then return nil end
  if prov.base_url then
    return prov.base_url, prov.wire_api
  end
  local ep = prov.endpoints and prov.endpoints.openai or nil
  if type(ep) == "table" and ep.base_url then
    return ep.base_url, ep.wire_api
  end
  return nil
end

-- piReachable is THE gate both halves below ask — can pi reach this provider at all — so
-- the catalog and the selection cannot grow two answers to the same question. It returns
-- the URL and the `api` value pi would use, or nil when the provider names no URL pi can
-- speak to.
--
-- "A protocol pi can speak" is the whole piDialect map, not one row of it, and that is the
-- sense in which this gate is wider than a chat-completions one: pi registers
-- openai-completions, openai-responses AND anthropic-messages, so a provider declaring
-- `wire_api = "anthropic"` reaches pi and is a catalog row — the shorthand form arrives
-- here through providerEndpoint with its wire_api intact, and translates to
-- anthropic-messages like any other protocol. What does NOT widen with it is the ENDPOINT
-- KEY: providerEndpoint resolves `openai` only, and that is the resolution table's ruling
-- (zai-plumbing.md §5, pinned by providerderive_test.go) — an endpoints-only provider with
-- no openai endpoint names no URL for the protocol pi resolves to, so it is no row, and a
-- provider that loses its catalog row loses its selection key with it. Writing
-- defaultProvider for such a provider would name an id pi has no entry for — the
-- half-selection a shared gate exists to make unrepresentable.
--
-- Total over non-tables, like providerEndpoint: a selected name that is absent from the
-- composed table (a profile whose provider the table does not hold — which
-- creates no requirement of its own) reads as nil here, and nil selects nothing.
local function piReachable(prov)
  if type(prov) ~= "table" then return nil end
  local baseUrl, wireApi = providerEndpoint(prov)
  -- An endpoint's own wire_api is the per-protocol fact; the provider-level one only
  -- speaks for the shorthand.
  local api = piAPI(wireApi or prov.wire_api)
  if baseUrl and api then
    return baseUrl, api
  end
  return nil
end

yolo.derive("pi", "models", function(ctx)
  if not ctx.providers or next(ctx.providers) == nil then
    return {}
  end
  local providers = {}
  for name, prov in pairs(ctx.providers) do
    local baseUrl, api = piReachable(prov)
    if baseUrl then
      local modelList = {}
      if type(prov.models) == "table" then
        for alias, modelId in pairs(prov.models) do
          table.insert(modelList, { id = modelId, name = alias })
        end
      end
      local entry = {
        baseUrl = baseUrl,
        api = api,
        models = modelList,
      }
      -- D11: pi has no `apiKeyEnv` field — ProviderConfigSchema is name, baseUrl, apiKey,
      -- api, oauth, headers, compat, authHeader, models, modelOverrides, and nothing in the
      -- package reads one, so the name we used to write here was dead configuration that
      -- read as the thing delivering the credential. pi's env indirection is the config-value
      -- syntax ON apiKey (`${VAR}`; docs/custom-provider.md — the maintainer's own hand-written
      -- models.json uses it), and pi expands it at read time, so yolo writes the reference
      -- verbatim and the consumer resolves it. Written only when the provider names a var, so
      -- a key-less provider stays key-less rather than claiming an empty one.
      if prov.api_key_env_name then
        entry.apiKey = "${" .. prov.api_key_env_name .. "}"
      end
      providers[name] = entry
    end
  end
  if next(providers) == nil then
    return {}
  end
  return { providers = providers }
end)

-- The selection — defaultProvider and defaultModel, pi's OWN selection keys, verified from
-- the published package the launcher installs (pi 0.84.4, npm-extracted, the CLI never
-- run): dist/core/settings-manager.d.ts:71-72 declares the pair, the ids match EXACTLY
-- (`===`) against the provider's model list, and pi's own interactive writer persists
-- exactly this pair (core/settings-manager.js:460-475) — so the pair this writes is
-- byte-for-byte the shape pi itself would write, never a yolo spelling of it
-- (docs/reference/providers.md §3 pi row). Two verification notes that shaped the
-- surface rather than this function: a project-scope twin (.pi/settings.json in the
-- working directory) deep-merges over the global file, so the GLOBAL file is the right
-- surface for a jail-wide default; and pi resolves a saved default only when the
-- provider's credential is configured, which is D11's `apiKey: "${VAR}"` fix, already
-- landed in the catalog half above.
--
-- The pair travels under the RESERVED `selection` key of the computed layer, exactly as
-- codex's does (packs/codex/derive.lua), and for the same reason. A plain computed key is
-- re-asserted by every boot — right for models.json, which is yolo's own output, and
-- exactly wrong for a model the user can change interactively mid-session — so a key yolo
-- re-asserted would silently revert their choice on the next launch
-- (docs/reference/providers.md §5.1, the hazard OQ-CS2 names). The stateful render
-- takes the namespace, decides per key — write on activation, never on absence, and a
-- user's interactive edit stands until a NEW selection value differs from the last one
-- yolo wrote — and lifts the winners onto the surface root, so settings.json shows
-- defaultProvider/defaultModel at top level where pi reads them. The namespace is an
-- implementation detail of the layer, never of the file.
--
-- OQ-CS2 is the GUARD, not a default: when no profile is active at pi's CLI name, nothing
-- selection-shaped is written — not a default, not a clear; the no-profile case is the
-- agent's own (pi's own persisted interactive choice stands). And when the selected
-- provider is not pi-reachable, the SAME gate that keeps it out of the catalog above keeps
-- it out of the selection: no keys at all, never a defaultProvider naming a provider whose
-- catalog row the same gate dropped.
--
-- defaultModel is the id under the alias the active profile's `model` option names
-- (OQ-CS4: what an option means is the derive's business, and for pi that meaning is a
-- key of the provider's own models table), falling back to the provider's declared
-- `default` alias when the profile carries no option (OQ-CS3: the fallback is the
-- derive's business, and `default` stays an ordinary open-vocabulary alias). It is
-- omitted when the provider declares no models or names no such alias, leaving pi to
-- resolve its own model within the named provider — model ids must match the provider's
-- list exactly, so guessing one would be a selection pi refuses at resolution time.
yolo.derive("pi", "settings", function(ctx)
  if ctx.selected_provider == nil or ctx.selected_provider == "" then
    return {}
  end
  local p = ctx.providers and ctx.providers[ctx.selected_provider] or nil
  if not piReachable(p) then
    return {}
  end
  local alias = (ctx.profile and ctx.profile.model) or "default"
  local sel = { defaultProvider = ctx.selected_provider }
  if type(p) == "table" and type(p.models) == "table" and p.models[alias] then
    sel.defaultModel = p.models[alias]
  end
  return { selection = sel }
end)
