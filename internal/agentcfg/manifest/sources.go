package manifest

// sources.go names the LIVE CONFIG TABLES a surface's derive function may read
// (exposed to derive Lua as ctx.<name>). These are DOMAIN nouns core owns — an
// MCP server and an LSP server are yolo config concepts, not agent concepts
// (docs/reference/pack-system.md §0 principle 2, §7). The boot path
// (entrypoint.liveTables) builds the table map keyed by exactly these, and hands
// it to the derive VM.
//
// Closed on purpose, same reason as every other closed set here: a name core
// cannot produce is a typo that would otherwise hand a derive an empty table with
// nothing to grep for.
const (
	// SourceMCPServers is the reconciled shared MCP-server table (config mcp_servers).
	SourceMCPServers = "mcp_servers"
	// SourceLSPServers is the configured LSP-server table (config lsp_servers).
	SourceLSPServers = "lsp_servers"
	// SourceProviders is the declared cloud providers table (config providers).
	SourceProviders = "providers"
	// SourceUseProfiles is the active agent profile assignments: the CLI-keyed table the
	// launch folds from the config `profile` key and -p (YOLO_USE_PROFILES). The name is the
	// derive's `ctx.use_profiles` and predates the key's rename (PP-D10); it names the resolved
	// table, which is unchanged, so a pack derive reading it did not move.
	SourceUseProfiles = "use_profiles"
)
