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
	"YOLO_MISE_SKILL_TOOLS", "YOLO_TOOL_SKILLS_TOKEN",
}

// toolSkillInputs are the two session inputs of mise tool skills (docs/design/mise-tool-skills.md
// MS-D1, MS-D9): the allowlist of the user scope's mise tools, and the workspace's snapshot token.
// The zero value is the sealed, capture and nested answer: an empty list and no token.
type toolSkillInputs struct {
	tools []string
	token string
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
	userEnv *jsonx.OrderedMap, skills toolSkillInputs) []sessionInput {
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
		// A JSON array, as YOLO_MISE_TOOLS is JSON: the allowlist in declaration order.
		"YOLO_MISE_SKILL_TOOLS":  jsonDumps(append([]string{}, skills.tools...)),
		"YOLO_TOOL_SKILLS_TOKEN": skills.token,
	}
	out := make([]sessionInput, 0, len(sessionInputNames))
	for _, name := range sessionInputNames {
		out = append(out, sessionInput{name: name, value: values[name]})
	}
	return out
}

// toolSkillInputs reads this launch's allowlist from the USER scope (config.UserScopeConfig, the
// read launchinputmcp.go makes) and mints or reads the workspace's snapshot token. The token is
// minted only for a non-empty allowlist, and an unreadable user scope or a token that cannot be
// stored gives the empty answer: nothing is delivered rather than something unguarded. The
// sealed, capture and nested answers never read the user scope.
func (o *Options) toolSkillInputs() toolSkillInputs {
	sealed, capture, nested := o.Sealed, o.Sealed || o.subLaunch(), config.InJail()
	if sealed || capture || nested {
		return toolSkillInputs{}
	}
	user, err := config.UserScopeConfig(false, func(string) {})
	if err != nil {
		return toolSkillInputs{}
	}
	tools := config.MiseSkillTools(user, sealed, capture, nested)
	if len(tools) == 0 {
		return toolSkillInputs{}
	}
	token, err := config.ToolSkillsToken(o.Workspace)
	if err != nil {
		return toolSkillInputs{}
	}
	return toolSkillInputs{tools: tools, token: token}
}
