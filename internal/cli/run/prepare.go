package run

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agents"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// refreshJailBriefings rebuilds the per-jail skills staging + each selected
// agent's AGENTS.md/CLAUDE.md briefing. Called on
// every invocation (incl. attach) so host-side skill/briefing edits propagate to
// a live jail via inode-preserving writes. Returns the staging dir
// (AGENTS_DIR/<cname>).
// It also returns the LOADED PACKS, because the mount assembler needs their
// declarations (writable dirs, mount targets, host-file grants) and staging is where
// they are read. Returning them here rather than re-loading later keeps one source of
// truth for what this run's packs declared.
func (o *Options) refreshJailBriefings(cname string, cfg *jsonx.OrderedMap, rt string) (string, []*packload.Pack, error) {
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

	// Pack staging (C3), BEFORE skills so PrepareSkills can layer pack skills in.
	// Fail-closed per A12: a declared pack that cannot be staged is an error, not a
	// jail that silently comes up without it.
	loadedPacks, packBriefings, err := o.stagePacks(cname)
	if err != nil {
		return "", nil, err
	}

	// PACK-DECLARED skills destinations. A pack mount whose source is "skills" says
	// "put my skills tree here"; core builds a staging dir per pack and mounts it there.
	agents.SetPackSkillTargets(packSkillTargets(loadedPacks))

	// Skills staging.
	staging, err := agents.PrepareSkills(cname, homeDir(), agentsList, isSrc)
	if err != nil {
		return "", nil, err
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
	// Pack prose last, each attributed to its pack (C3): it is instructions the
	// agent will follow, so it must be traceable to a source.
	jailContent = agents.ComposePackBriefings(jailContent, packBriefings)

	// Write one briefing per PACK-DECLARED briefing mount. The pack says where its
	// prose goes; core writes the composed content to the matching staging file and
	// (for a pack whose origin permits it) prepends the user's own host briefing first,
	// so a personal AGENTS.md still outranks anything a pack ships.
	//
	// This is the loop that used to iterate selected agents, which is why a zero-agent
	// jail wrote NO briefing at all. It now follows declarations, so a pack always gets
	// its briefing whether or not anything calls it an agent.
	home := homeDir()
	for _, p := range loadedPacks {
		for _, mt := range p.Decl.Mounts {
			if !isBriefingMount(mt.From) {
				continue
			}
			content := jailContent
			if mt.HostOverlay != "" && p.MayAccessHost {
				content = agents.PrependHostBriefing(filepath.Join(home, mt.HostOverlay), content)
			}
			if err := agents.WriteBriefing(filepath.Join(staging, briefingStagingName(p.Name)), content); err != nil {
				return "", nil, err
			}
		}
	}
	_ = rt
	return staging, loadedPacks, nil
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
func (o *Options) prepareWsState(cfg *jsonx.OrderedMap, loadedPacks []*packload.Pack, agentsList []string) string {
	wsState := filepath.Join(o.Workspace, ".yolo", "home")
	_ = os.MkdirAll(wsState, 0o755)
	_ = os.MkdirAll(filepath.Join(wsState, "ssh"), 0o700)

	// Backing dirs for the PACK-DECLARED writable dirs. These must exist before the
	// :ro home bind is applied: podman refuses to start with a bare
	// "statfs …: no such file or directory" when a bind source is missing, which reads
	// as a yolo bug rather than a missing directory.
	var overlaySubdirs []string
	for _, dir := range packload.WritableDirs(loadedPacks) {
		overlaySubdirs = append(overlaySubdirs, strings.TrimPrefix(dir, "."))
	}
	for _, subdir := range append([]string{"npm-global", "local", "go", "yolo-shims", "config"}, overlaySubdirs...) {
		_ = os.MkdirAll(filepath.Join(wsState, subdir), 0o755)
	}

	// Writable home dirs (config writable_home_dirs): create BOTH the backing dir
	// under <wsState>/writable-home/<path> AND the mountpoint dir in GLOBAL_HOME.
	// podman binds the backing dir over /home/agent/<path> (nesting inside the
	// :ro GLOBAL_HOME base). The OCI runtime does NOT auto-create mountpoints
	// inside a :ro bind mount — it works for .npm-global/.config/etc. only because
	// those dirs already exist in GLOBAL_HOME. A path absent from GLOBAL_HOME
	// makes crun mkdirat inside the :ro bind → "read-only file system", which
	// surfaces as the cryptic "conmon bytes '': readObjectStart" error. Creating
	// the mountpoint in GLOBAL_HOME before the :ro bind is applied avoids this.
	// The paths are already validated (relative, no '..', no reserved segment), so
	// MkdirAll can never escape wsState or GLOBAL_HOME.
	for _, rel := range config.WritableHomeDirs(cfg) {
		_ = os.MkdirAll(filepath.Join(wsState, config.WritableHomeBackingSubdir, rel), 0o755)
		_ = os.MkdirAll(filepath.Join(paths.GlobalHome(), rel), 0o755)
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

// isBriefingMount reports whether a pack mount carries briefing prose rather than a
// content tree. Keyed on the source NAME because that is what a pack author writes;
// both spellings are in the wild and an author should not have to know which one yolo
// reads.
func isBriefingMount(from string) bool {
	return from == "AGENTS.md" || from == "CLAUDE.md"
}

// briefingStagingName is the staging filename for one pack's briefing. Per-pack rather
// than per-agent, so two packs cannot collide on one staged file.
func briefingStagingName(pack string) string { return "briefing-" + pack + ".md" }

// packSkillTargets turns pack mount declarations into skills staging targets.
//
// The user's OWN skills tree is layered in from the same jail-relative path the pack
// declared, because a tool reads its skills from one place regardless of who put them
// there — and a local skill must outrank a pack's. Only a pack whose origin permits
// host access gets that layer, since it reads the host home.
func packSkillTargets(loadedPacks []*packload.Pack) []agents.SkillTarget {
	var out []agents.SkillTarget
	for _, p := range loadedPacks {
		for _, mt := range p.Decl.Mounts {
			if mt.From != "skills" {
				continue
			}
			t := agents.SkillTarget{Staging: agents.SkillStagingName(p.Name), Dest: mt.To}
			if p.MayAccessHost {
				t.HostSource = mt.To
			}
			out = append(out, t)
		}
	}
	return out
}
