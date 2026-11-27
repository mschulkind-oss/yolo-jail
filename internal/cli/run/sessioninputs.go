package run

import (
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/repoinstr"
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
	"YOLO_MISE_SKILL_TOOLS", "YOLO_TOOL_SKILLS_TOKEN", toolSkillsDeliveredEnv, repoinstr.EnvName,
}

// toolSkillsDeliveredEnv is YOLO_TOOL_SKILLS_DELIVERED (design MS-D7): the content digest of the
// tool-skills snapshot this entry's host composition delivered, "" when it delivered none. The
// in-jail refresh step (toolskills.RunStep) reads it after `mise install`, compares it with the
// digest it just published, and says "arrives with the next session" only when they differ.
const toolSkillsDeliveredEnv = "YOLO_TOOL_SKILLS_DELIVERED"

// toolSkillInputs are the session inputs of mise tool skills (docs/reference/mise-tool-skills.md
// MS-D1, MS-D7, MS-D9): the allowlist of the user scope's mise tools, the workspace's snapshot
// token, and the digest of the snapshot this entry delivered. The zero value is the sealed,
// capture and nested answer: an empty list, no token and no digest.
type toolSkillInputs struct {
	tools     []string
	token     string
	delivered string
	// problem is why the allowlist is non-empty and tool skills are off anyway ("" when none), for
	// the one line that says so (noteToolSkillsOff).
	problem string
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
		toolSkillsDeliveredEnv:   skills.delivered,
		// The repo_instructions mode, so the jail's boot hands its packs the selection the host
		// handed its own; "" keeps the files, and a sealed build carries none of the user's config.
		repoinstr.EnvName: repoInstructionsWireFor(cfg, sealed || captureJail),
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
		return toolSkillInputs{problem: err.Error()}
	}
	delivered := jailcontent.ToolSkillsDelivered(&jailcontent.ToolSkillsInput{
		Workspace: o.Workspace, Token: token, Tools: tools})
	return toolSkillInputs{tools: tools, token: token, delivered: delivered}
}

// toolSkillsLayerInput is the request the host's skills composition makes for the tool-skills
// layer: this launch's user-scope allowlist and the workspace's snapshot token, the same two
// values the jail was handed (toolSkillInputs). Nil asks for no layer: an empty allowlist (the
// default, and the sealed, capture and nested answer) or no token.
func (o *Options) toolSkillsLayerInput() *jailcontent.ToolSkillsInput {
	in := o.toolSkillInputs()
	if in.problem != "" {
		o.noteToolSkillsOff(in.problem)
	}
	if len(in.tools) == 0 || in.token == "" {
		return nil
	}
	return &jailcontent.ToolSkillsInput{Workspace: o.Workspace, Token: in.token, Tools: in.tools}
}

// repoInstructionsWireFor is the repo_instructions session input: the mode for an ordinary launch,
// "" for a sealed build or a capture jail, which carry none of the user's own settings.
func repoInstructionsWireFor(cfg *jsonx.OrderedMap, sealedOrCapture bool) string {
	if sealedOrCapture {
		return ""
	}
	return repoinstr.WireFromConfig(cfg)
}
