package manifest

// sources.go names the LIVE CONFIG TABLES a surface's derive function may read
// (exposed to derive Lua as ctx.<name>). These are DOMAIN nouns core owns — an
// MCP server and an LSP server are yolo config concepts, not agent concepts
// (pack-declaration-reform §4.1) — so they outlive the computed[] op DSL that was
// deleted in Phase 3b. The boot path (entrypoint.liveTables) builds the table map
// keyed by exactly these, and hands it to the derive VM.
//
// Closed on purpose, same reason as every other closed set here: a name core
// cannot produce is a typo that would otherwise hand a derive an empty table with
// nothing to grep for.
const (
	// SourceMCPServers is the reconciled shared MCP-server table (config mcp_servers).
	SourceMCPServers = "mcp_servers"
	// SourceLSPServers is the configured LSP-server table (config lsp_servers).
	SourceLSPServers = "lsp_servers"
)
