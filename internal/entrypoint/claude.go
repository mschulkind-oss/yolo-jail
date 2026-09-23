package entrypoint

// This file held claudeLSPPluginOrder: three hardcoded language→plugin pairs naming
// `pyright-lsp`, `typescript-lsp` and `gopls-lsp` from the `claude-plugins-official`
// marketplace, which the derive enabled when the matching language appeared in
// `lsp_servers`.
//
// DELETED 2026-09-22 by OQ-LSP1's option D (docs/reference/mcp-configuration.md#oq-lsp1): yolo now
// renders ONE plugin of its own whose `lspServers` comes from the user's whole
// `lsp_servers` table, so there is no per-language opinion left to hardcode and no
// marketplace id to enable. jailcontent.writeLSPPlugin is the replacement, and it needs no
// `enabledPlugins` entry at all — a plugin auto-loaded from the skills tree is enabled by
// default there.
//
// The three ids were also INERT by the time they went: the `claude_plugins` hook that
// installed them was retired, so the derive was enabling plugins nothing put on disk.
//
// linkThroughShared used to live here too, which made a generic rule read as claude's. It is
// in sharedlink.go now, beside the two payload shapes it serves.
