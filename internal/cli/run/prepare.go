package run

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agents"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// refreshJailBriefings rebuilds the per-jail skills staging + each selected
// agent's AGENTS.md/CLAUDE.md briefing. Called on
// every invocation (incl. attach) so host-side skill/briefing edits propagate to
// a live jail via inode-preserving writes. Returns the staging dir
// (AGENTS_DIR/<cname>).
func (o *Options) refreshJailBriefings(cname string, cfg *jsonx.OrderedMap, rt string) (string, error) {
	netSec := cfgMap(cfg, "network")
	netMode := o.Network
	if netSec != nil {
		if m := mapStr(netSec, "mode"); m != "" {
			netMode = m
		}
	}
	var forwardHostPorts []any
	if netMode == "bridge" && netSec != nil {
		forwardHostPorts = asAnyList(mapGet(netSec, "forward_host_ports"))
	}

	// Blocked-tools → agents.BlockedTool records.
	blocked := blockedToolRecords(config.NormalizeBlockedTools(cfgMap(cfg, "security")))

	// mount_descriptions for existing config.mounts.
	var mountDescriptions []string
	for _, mAny := range cfgList(cfg, "mounts") {
		mount, ok := mAny.(string)
		if !ok {
			continue
		}
		hostPath, containerPath := splitMountSpec(mount)
		resolved := resolveExpand(hostPath)
		if fileExists(resolved) {
			mountDescriptions = append(mountDescriptions, resolved+":"+containerPath)
		}
	}

	// Enabled loopholes (name, description).
	var loops []agents.Loophole
	for _, lo := range loopholes.Discover(loopholes.DiscoverOptions{
		IncludeBundled:  true,
		LoopholesConfig: cfgMap(cfg, "loopholes"),
	}) {
		loops = append(loops, agents.Loophole{Name: lo.Name, Desc: lo.Description})
	}

	agentsList := config.SelectedAgents(cfg)

	// Source-tree gating: staged skills + the briefing's dev section both key
	// off this. Derived from the stable workspace, so launch and attach agree.
	isSrc := agents.WorkspaceIsYoloSourceTree(o.Workspace)

	// Skills staging.
	staging, err := agents.PrepareSkills(cname, homeDir(), agentsList, isSrc)
	if err != nil {
		return "", err
	}

	// Resources map (sorted-key rendering handled inside BriefingContent).
	resources := orderedMapToStrAny(cfgMap(cfg, "resources"))

	in := agents.BriefingInput{
		Workspace:          o.Workspace,
		BlockedTools:       blocked,
		MountDescriptions:  mountDescriptions,
		NetMode:            netMode,
		ForwardHostPorts:   forwardHostPorts,
		Loopholes:          loops,
		Resources:          resources,
		IsYoloSourceTree:   isSrc,
		ProvisioningFailed: agents.ReadProvisioningFailed(o.Workspace),
	}
	jailContent := agents.BriefingContent(in)
	jailContent = agents.ComposeBriefing(jailContent, cfgStr(cfg, "agents_md_extra"))

	home := homeDir()
	for _, spec := range agents.ResolveAgents(agentsList) {
		content := agents.PrependHostBriefing(filepath.Join(home, spec.Briefing.HostSource), jailContent)
		if err := agents.WriteBriefing(filepath.Join(staging, spec.Briefing.Staging), content); err != nil {
			return "", err
		}
	}
	_ = rt
	return staging, nil
}

// blockedToolRecords converts NormalizeBlockedTools output (a []any of ordered
// maps) into agents.BlockedTool records.
func blockedToolRecords(blocked []any) []agents.BlockedTool {
	var out []agents.BlockedTool
	for _, b := range blocked {
		m, ok := b.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		out = append(out, agents.BlockedTool{
			Name:       mapStr(m, "name"),
			Message:    mapStr(m, "message"),
			Suggestion: mapStr(m, "suggestion"),
		})
	}
	return out
}

// orderedMapToStrAny converts an OrderedMap to a map[string]any (for
// BriefingInput.Resources; BriefingContent sorts keys itself).
func orderedMapToStrAny(m *jsonx.OrderedMap) map[string]any {
	if m == nil || m.Len() == 0 {
		return nil
	}
	out := make(map[string]any, m.Len())
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		out[k] = v
	}
	return out
}

// prepareWsState prepares the ws_state overlay: create the
// per-workspace overlay dirs + touch the overlay files, seed selected agents'
// config dirs, sync claude.json, and run the old-overlay migrations. Returns the
// ws_state path (<workspace>/.yolo/home).
func (o *Options) prepareWsState(cfg *jsonx.OrderedMap, agentSpecs []agents.AgentSpec, agentsList []string) string {
	wsState := filepath.Join(o.Workspace, ".yolo", "home")
	_ = os.MkdirAll(wsState, 0o755)
	_ = os.MkdirAll(filepath.Join(wsState, "ssh"), 0o700)

	overlaySubdirs := agentOverlaySubdirs(agentSpecs)
	for _, subdir := range append([]string{"npm-global", "local", "go", "yolo-shims", "config"}, overlaySubdirs...) {
		_ = os.MkdirAll(filepath.Join(wsState, subdir), 0o755)
	}
	for _, fname := range []string{
		"bash_history", "yolo-bootstrap.sh", "yolo-venv-precreate.sh",
		"yolo-perf.log", "yolo-socat.log", "yolo-entrypoint.lock",
		"yolo-ca-bundle.crt", "yolo-installed-lsps",
	} {
		touchFile(filepath.Join(wsState, fname))
	}

	// Seed selected agents' config dirs from the :ro GLOBAL_HOME base.
	for _, subdir := range overlaySubdirs {
		seedAgentDir(filepath.Join(paths.GlobalHome(), "."+subdir), filepath.Join(wsState, subdir))
	}

	if inStrSlice(agentsList, "claude") {
		syncClaudeJSONSeed(
			filepath.Join(paths.GlobalHome(), ".claude", "claude.json"),
			filepath.Join(wsState, "claude", "claude.json"))
		migrateOldOverlay(filepath.Join(wsState, "claude-projects"), filepath.Join(wsState, "claude", "projects"))
		// claude-settings.json → claude/settings.json (only if new absent).
		oldSettings := filepath.Join(wsState, "claude-settings.json")
		newSettings := filepath.Join(wsState, "claude", "settings.json")
		if isFile(oldSettings) && !fileExists(newSettings) {
			_ = os.MkdirAll(filepath.Join(wsState, "claude"), 0o755)
			_ = copyFile2(oldSettings, newSettings)
		}
	}
	if inStrSlice(agentsList, "copilot") {
		migrateOldOverlay(filepath.Join(wsState, "copilot-sessions"), filepath.Join(wsState, "copilot", "session-state"))
	}
	if inStrSlice(agentsList, "gemini") {
		migrateOldOverlay(filepath.Join(wsState, "gemini-history"), filepath.Join(wsState, "gemini", "history"))
	}
	return wsState
}

func touchFile(p string) {
	if fileExists(p) {
		return
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		_ = f.Close()
	}
}

// lspServerNames returns the lsp_servers config keys in load order.
func lspServerNames(cfg *jsonx.OrderedMap) []string {
	m := cfgMap(cfg, "lsp_servers")
	if m == nil {
		return nil
	}
	return m.Keys()
}

var _ = strings.Join
