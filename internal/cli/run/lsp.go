package run

import (
	"slices"
	"strings"
)

// lspInstallRecipe maps a configured LSP server name to the npm + go packages
// the bootstrap should ensure installed. Frozen from _LSP_INSTALL_RECIPES.
type lspInstallRecipe struct {
	npm []string
	go_ []string
}

var lspInstallRecipes = map[string]lspInstallRecipe{
	"python":     {npm: []string{"pyright"}},
	"typescript": {npm: []string{"typescript-language-server", "typescript"}},
	"go":         {go_: []string{"golang.org/x/tools/gopls@latest"}},
}

// lspGeminiBridgeGo is pulled whenever ANY LSP is configured (Gemini wraps every
// LSP through it). Frozen from _LSP_GEMINI_BRIDGE_GO.
const lspGeminiBridgeGo = "github.com/isaacphi/mcp-language-server@latest"

// ResolveLSPInstalls translates a configured lsp_servers set into newline-joined
// npm + go install lists (parser-free for the bash side). Mirrors
// _resolve_lsp_installs, including the quirk that the Gemini bridge is appended
// only when lsp_servers is NON-EMPTY (an empty set returns two empty strings and
// skips the bridge entirely). Server names outside the recipe table contribute
// nothing but still count toward "non-empty" (so a custom-only LSP set still
// pulls the bridge). Dedup preserves first-seen order.
//
// serverNames is the set of configured LSP server names, in the iteration order
// Python's dict would yield (config load order); pass them in that order.
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
	// Bridge appended unconditionally for a non-empty set (matches Python:
	// go.append runs after the early empty return).
	goList = append(goList, lspGeminiBridgeGo)
	return strings.Join(npmList, "\n"), strings.Join(goList, "\n")
}
