package run

import (
	"slices"
	"strings"
)

// lspInstallRecipe maps a configured LSP server name to the npm + go packages
// the bootstrap should ensure installed. Frozen contract (recipe values must not
// drift — the bootstrap installer depends on them).
type lspInstallRecipe struct {
	npm []string
	go_ []string
}

var lspInstallRecipes = map[string]lspInstallRecipe{
	"python":     {npm: []string{"pyright"}},
	"typescript": {npm: []string{"typescript-language-server", "typescript"}},
	"go":         {go_: []string{"golang.org/x/tools/gopls@latest"}},
}

// ResolveLSPInstalls translates a configured lsp_servers set into newline-joined
// npm + go install lists (parser-free for the bash side). An empty set returns
// two empty strings. Server names outside the recipe table contribute nothing.
// Dedup preserves first-seen order.
//
// serverNames is the set of configured LSP server names, in config load order;
// pass them in that order.
func ResolveLSPInstalls(serverNames []string) (npm, goPkgs string) {
	if len(serverNames) == 0 {
		return "", ""
	}
	var npmList, goList []string
	for _, name := range serverNames {
		recipe, ok := lspInstallRecipes[name]
		if !ok {
			continue
		}
		for _, pkg := range recipe.npm {
			if !slices.Contains(npmList, pkg) {
				npmList = append(npmList, pkg)
			}
		}
		for _, pkg := range recipe.go_ {
			if !slices.Contains(goList, pkg) {
				goList = append(goList, pkg)
			}
		}
	}
	// The mcp-language-server BRIDGE IS GONE with the gemini agent (A1): gemini was
	// its only consumer — it wrapped every configured LSP as an MCP server keyed
	// "<name>-lsp" (the old buildGeminiMCPServers). Every surviving agent consumes
	// LSP servers natively (copilot via its own lsp-config.json surface; claude via
	// its own tools), so appending the bridge would install a Go binary nothing
	// invokes.
	return strings.Join(npmList, "\n"), strings.Join(goList, "\n")
}
