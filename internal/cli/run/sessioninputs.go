package run

import (
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// sessionInput is one SESSION INPUT: a value every session's boot reads from its environment
// and renders from on the session pass (docs/design/live-config-apply.md LA-S4a, LA-D18).
type sessionInput struct{ name, value string }

// sessionInputNames are the session inputs, in the order the channel section writes them: the
// blocked tools (generate_shims), the mise tools and the list mise must not install
// (generate_mise_config, and the session's mise install, misereprovision.go), and the LSP and MCP
// tables (configure_pack_surfaces, generate_mcp_wrappers).
var sessionInputNames = []string{
	"YOLO_BLOCK_CONFIG", "YOLO_MISE_TOOLS", "MISE_DISABLE_TOOLS", "YOLO_LSP_SERVERS", "YOLO_MCP_SERVERS",
}

// composeSessionInputs is this entry's session inputs, from the config and the selected packs.
// They cross on the PER-ENTRY CHANNEL (userEnvFileBody's section) and nowhere else, so an attach's
// rewrite reaches the next session of a running jail (LA-D18). They used to be `-e` pairs on the
// argv, frozen into the container at its launch, which no later entry could change — and an MCP
// server's `env` is a literal string, an API key among them, that `podman inspect` printed.
//
// THE SEAL (seal.go) withholds the user's servers and mise tools from a sealed build
// (agentServerTables, config.JailMiseTools) and hydrates no env_sources, and a capture jail takes
// none of the user's mise tools; the blocked tools cross either way, since a build is confined by
// them too. MISE_DISABLE_TOOLS merges the user's own env_sources value with the tools the jail
// declares (config.MergeMiseDisabledTools: a declared mise pnpm lifts pnpm out of the list).
func composeSessionInputs(cfg *jsonx.OrderedMap, packs []*packload.Pack, sealed, captureJail bool,
	userEnv *jsonx.OrderedMap) []sessionInput {
	blocked := config.NormalizeBlockedToolsWith(cfgMap(cfg, "security"), packload.BlockedTools(packs))
	miseTools, _ := config.JailMiseTools(cfg, sealed || captureJail)
	lsp, mcp := agentServerTables(cfg, sealed, packs)
	var userDisabled any
	if !sealed {
		userDisabled = mapGet(userEnv, "MISE_DISABLE_TOOLS")
	}
	values := map[string]string{
		"YOLO_BLOCK_CONFIG":  jsonDumps(blocked),
		"YOLO_MISE_TOOLS":    jsonDumps(miseTools),
		"MISE_DISABLE_TOOLS": config.MergeMiseDisabledTools(userDisabled, miseTools),
		"YOLO_LSP_SERVERS":   jsonDumpsOrEmptyObj(lsp),
		"YOLO_MCP_SERVERS":   jsonDumpsOrEmptyObj(mcp),
	}
	out := make([]sessionInput, 0, len(sessionInputNames))
	for _, name := range sessionInputNames {
		out = append(out, sessionInput{name: name, value: values[name]})
	}
	return out
}
