package entrypoint

// claudeLSPPluginOrder pins the iteration order used when enabling LSP plugins;
// the effect on enabledPlugins is order-independent for distinct keys, but the
// order is fixed for deterministic output.
//
// linkThroughShared used to live here too, which made a generic rule read as claude's. It
// is in sharedlink.go now, beside the two payload shapes it serves.
var claudeLSPPluginOrder = []struct{ lsp, plugin string }{
	{"python", "pyright-lsp@claude-plugins-official"},
	{"typescript", "typescript-lsp@claude-plugins-official"},
	{"go", "gopls-lsp@claude-plugins-official"},
}
