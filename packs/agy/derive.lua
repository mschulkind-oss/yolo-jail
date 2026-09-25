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

-- agy: the MCP surface's dynamic layer. A passthrough of the servers this launch is
-- eligible for.
--
-- The `provides = "web_search"` strip that used to be here is GONE: agy's native Google
-- search is a fact about its BUILT-IN authentication source, so the pack states it once
-- (the program contribution's `capabilities`) and core drops the redundant servers before
-- ctx.mcp_servers reaches any derive. The surface still needs this producer — mode is
-- `computed` over `defaults.mcpServers = {}`, so without a dynamic layer agy would render
-- an empty table rather than the configured one.
yolo.derive("agy", "mcp", function(ctx)
  local servers = {}
  for name, cfg in pairs(ctx.mcp_servers or {}) do
    servers[name] = cfg
  end
  return { mcpServers = in_full(ctx, servers) }
end)

