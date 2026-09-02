-- opencode: project the canonical MCP and provider entries into opencode's dialect.

-- The provider's URL for the protocol opencode speaks — `openai`, per the resolution
-- table in zai-plumbing.md §5. The single-protocol `base_url` shorthand wins; otherwise
-- the openai endpoint. Total over non-tables so the call site stays a one-line gate.
-- Returns nil when the provider names no URL an openai-speaking agent can use, which is
-- what keeps the gate below honest: an endpoints-only provider still reaches the catalog
-- (the pre-endpoints gate on prov.base_url silently dropped it), while a provider whose
-- only endpoint speaks anthropic would emit an entry with no URL. opencode consumes no
-- wire_api, so only the URL comes back.
local function providerEndpoint(prov)
  if type(prov) ~= "table" then return nil end
  if prov.base_url then
    return prov.base_url
  end
  local ep = prov.endpoints and prov.endpoints.openai or nil
  if type(ep) == "table" and ep.base_url then
    return ep.base_url
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
    res.mcp = out
  end

  -- 2. Providers
  if ctx.providers and next(ctx.providers) ~= nil then
    local provOut = {}
    for name, prov in pairs(ctx.providers) do
      local baseUrl = providerEndpoint(prov)
      if baseUrl then
        local models = {}
        if type(prov.models) == "table" then
          for alias, modelId in pairs(prov.models) do
            models[modelId] = { name = alias }
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
          npm = "@ai-sdk/openai-compatible",
          models = models,
        }
        entry.options = { baseURL = baseUrl }
        if prov.api_key_env_name then
          entry.options.apiKey = "{env:" .. prov.api_key_env_name .. "}"
        end
        provOut[name] = entry
      end
    end
    if next(provOut) ~= nil then
      res.provider = provOut
    end
  end

  -- 3. The selection — `model`, opencode's OWN selection key, source-verified from the
  -- installed release (upstream v1.18.18, the tag the shipped binary reports):
  -- packages/core/src/v1/config/config.ts:74-76 declares it "Model to use in the format
  -- of provider/model", split on the FIRST slash at model.ts:33-39, and an unknown prefix
  -- is a ModelNotFoundError with no silent fallback (provider-catalog-and-selection.md §3
  -- opencode row). The provider half is the catalog key above, the model half is a bare
  -- model id, and one slash joins them.
  --
  -- It travels under the RESERVED `selection` key of the computed layer, exactly as codex's
  -- and pi's do, and for the same reason: a plain computed key is re-asserted by every
  -- boot, and opencode lets a user change the model interactively mid-session, so a key
  -- yolo re-asserted would silently revert that choice on the next launch
  -- (provider-catalog-and-selection.md §5.1, the hazard OQ-CS2 names). The stateful render
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
  -- OQ-CS2 is the GUARD, not a default: no active variant at opencode's CLI name, or a
  -- selected provider the gate drops, writes nothing — not a default, not a clear. The
  -- no-profile case is opencode's own, and what opencode owns is a persisted interactive
  -- choice (~/.local/state/opencode/model.json) that `model` unset falls back to.
  --
  -- The model half is the derive's business (OQ-CS3), and the fallback ladder is
  -- shortest-claim-first: the provider's declared `default` alias; else the ONE model it
  -- declares, where "which model" has only one possible answer; else nothing — and with
  -- `model` being a single key, nothing means NO selection at all, never a half one
  -- naming a provider with no model under it.
  if ctx.selected_provider ~= nil and ctx.selected_provider ~= "" then
    local p = ctx.providers and ctx.providers[ctx.selected_provider] or nil
    if providerEndpoint(p) then
      local modelID = nil
      if type(p) == "table" and type(p.models) == "table" then
        if p.models.default then
          modelID = p.models.default
        else
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
      if modelID then
        res.selection = { model = ctx.selected_provider .. "/" .. modelID }
      end
    end
  end

  return res
end)
