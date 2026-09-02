-- codex: project MCP servers and model_providers into codex's TOML format.

-- The DIALECT MAP (provider-table-fidelity.md §3.4 / OQ-PT1): yolo's canonical wire_api
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

-- The provider's URL for the protocol codex speaks — `openai`, per the resolution table
-- in zai-plumbing.md §5. The single-protocol `base_url` shorthand wins; otherwise the
-- openai endpoint. Total over non-tables so the call site stays a one-line gate. Returns
-- nil when the provider names no URL an openai-speaking agent can use, which is what
-- keeps that gate honest: an endpoints-only provider still reaches the catalog (the
-- pre-endpoints gate on prov.base_url silently dropped it), while a provider whose only
-- endpoint speaks anthropic would emit an entry with no URL.
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
    res.mcp_servers = out
  end

  -- 2. Model providers
  if ctx.providers and next(ctx.providers) ~= nil then
    local provOut = {}
    for name, prov in pairs(ctx.providers) do
      local baseUrl, api = codexReachable(prov)
      if baseUrl then
        local entry = {
          base_url = baseUrl,
          wire_api = api,
        }
        if prov.api_key_env_name then
          entry.api_key_env = prov.api_key_env_name
        end
        provOut[name] = entry
      end
    end
    if next(provOut) ~= nil then
      res.model_providers = provOut
    end
  end

  -- 3. The selection — model_provider and model, codex's OWN selection keys, verified from
  -- the codex CLI binary 2026-08-20 (docs/research/local-model-endpoints.md §"Codex CLI";
  -- provider-catalog-and-selection.md §3 codex row). An active profile names the provider
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
  -- (provider-catalog-and-selection.md §5.1, the hazard OQ-CS2 names). The stateful render
  -- takes the namespace, decides per key — write on activation, never on absence, and a
  -- user's interactive edit stands until a NEW selection value differs from the last one
  -- yolo wrote — and lifts the winners onto the surface root, so config.toml still shows
  -- `model_provider` and `model` at top level where codex reads them. The namespace is an
  -- implementation detail of the layer, never of the file.
  --
  -- OQ-CS2 is the GUARD, not a default: when no profile is active at codex's CLI name,
  -- nothing selection-shaped is written — not a default, not a clear. The no-profile case
  -- is the agent's own (provider-catalog-and-selection.md §5.1). And when the selected
  -- provider is not codex-reachable, the SAME gate that keeps it out of the catalog keeps
  -- it out of the selection: no keys at all, never a `model_provider` naming a provider
  -- whose row the catalog dropped — codex refuses that config at startup.
  if ctx.selected_provider ~= nil and ctx.selected_provider ~= "" then
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

  return res
end)
