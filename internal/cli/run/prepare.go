package run

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// refreshJailBriefings rebuilds the per-jail skills staging + one briefing per
// pack-declared briefing destination. Called on
// every invocation (incl. attach) so host-side skill/briefing edits propagate to
// a live jail via inode-preserving writes. Returns the staging dir
// (AGENTS_DIR/<cname>).
//
// It takes the ALREADY-STAGED pack set rather than staging it itself. Staging moved up
// to Run(), above the backend dispatch, because it is not a container-path step: the
// macos-user backend returns before this function is ever called, which is exactly how
// that backend came to render zero pack surfaces (B-0). What stays here is the part that
// genuinely is per-jail staging — skills layering and briefing composition — which reads
// the staged set through the `staged` argument.
func (o *Options) refreshJailBriefings(cname string, cfg *jsonx.OrderedMap, rt string, staged stagedPacks) (string, error) {
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

	// Blocked-tools → jailcontent.BlockedTool records.
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

	// ACTIVE loopholes (name, description) — census site 1, through the converged set.
	loops := briefingLoopholes(cfgMap(cfg, "loopholes"))

	// Source-tree gating: staged skills + the briefing's dev section both key
	// off this. Derived from the stable workspace, so launch and attach agree.
	isSrc := jailcontent.WorkspaceIsYoloSourceTree(o.Workspace)

	// Pack staging (C3) already ran, ABOVE the backend dispatch, and its ordering
	// relative to skills is still load-bearing in the same way: stagePacks sets
	// jailcontent.SetPackSkillDirs as a side effect and PrepareSkills consumes it below.
	loadedPacks, packBriefings := staged.packs, staged.briefings

	// PACK-DECLARED skills destinations. A pack mount whose source is "skills" says
	// "put my skills tree here"; core builds a staging dir per pack and mounts it there.
	jailcontent.SetPackSkillTargets(packSkillTargets(loadedPacks))

	// Skills staging.
	staging, err := jailcontent.PrepareSkills(cname, homeDir(), nil, isSrc)
	if err != nil {
		return "", err
	}

	// Resources map (sorted-key rendering handled inside BriefingContent).
	resources := orderedMapToStrAny(cfgMap(cfg, "resources"))

	in := jailcontent.BriefingInput{
		Workspace:          o.Workspace,
		BlockedTools:       blocked,
		MountDescriptions:  mountDescriptions,
		NetMode:            netMode,
		ForwardHostPorts:   forwardHostPorts,
		Loopholes:          loops,
		Resources:          resources,
		IsYoloSourceTree:   isSrc,
		ProvisioningFailed: jailcontent.ReadProvisioningFailed(o.Workspace),
		Confinement:        string(config.ResolveConfinement(cfg)),
	}
	briefingBody := jailcontent.BriefingContent(in)
	briefingBody = jailcontent.ComposeBriefing(briefingBody, cfgStr(cfg, "agents_md_extra"))
	// Pack prose last, each attributed to its pack (C3): it is instructions the
	// agent will follow, so it must be traceable to a source.
	briefingBody = jailcontent.ComposePackBriefings(briefingBody, packBriefings)

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
		for _, c := range p.Decl.Contributions() {
			if c.Kind != packdecl.KindBriefing {
				continue
			}
			content := briefingBody
			if hostOverlay := briefingHostOverlay(c); hostOverlay != "" && p.MayAccessHost {
				content = jailcontent.PrependHostBriefing(filepath.Join(home, hostOverlay), content)
			}
			if err := jailcontent.WriteBriefing(filepath.Join(staging, briefingStagingName(p.Name)), content); err != nil {
				return "", err
			}
		}
	}
	_ = rt
	return staging, nil
}

// briefingLoopholes is the loophole list the jail's briefing advertises — census site 1
// (docs/design/loophole-packaging.md §5.1), read through the converged set.
//
// ACTIVE(), NOT ENABLED(), and that is a bug fix. This filtered on `enabled` alone, so an
// enabled-but-INACTIVE loophole — one whose `requires` is unmet on this host (no `claude` on
// PATH, no PipeWire socket) — was advertised to the agent as a live capability. The briefing
// is instructions the agent ACTS ON: telling it audio is available when the sockets never
// crossed is how an agent comes to debug ALSA instead of reading one line saying the
// loophole is inactive here. Every other consumer of this set already keyed on Active() —
// the container argv (RuntimeArgsFor), the daemon spawn, the broker predicate. The briefing
// was the one surface that did not, which is exactly the kind of divergence the
// seven-surface convergence exists to remove.
//
// HONORED(), which is Active() plus the ORIGIN GATE, and the second half is the same bug fix
// on a second axis (§4.3 G3). An UNAPPROVED fetched pack's loophole is perfectly Active — it
// is enabled, the platform matches, its `requires` are met — and yet nothing of it crosses,
// because the gate withheld it. Advertising it here tells the agent a capability is available
// when the daemon never started and the binds never happened, which is the identical failure
// to the one above with a different cause: the agent goes and debugs host wiring that was
// deliberately refused.
//
// A FUNCTION rather than four inline lines, so the property is testable at the code the
// launch runs: the same expression retyped in a test asserts nothing about this file.
func briefingLoopholes(loopholesCfg *jsonx.OrderedMap) []jailcontent.Loophole {
	var out []jailcontent.Loophole
	for _, lo := range loopholes.NewHostSet(loopholesCfg).Honored() {
		out = append(out, jailcontent.Loophole{Name: lo.Name, Desc: lo.Description})
	}
	return out
}

// blockedToolRecords converts NormalizeBlockedTools output (a []any of ordered
// maps) into jailcontent.BlockedTool records.
func blockedToolRecords(blocked []any) []jailcontent.BlockedTool {
	var out []jailcontent.BlockedTool
	for _, b := range blocked {
		m, ok := b.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		out = append(out, jailcontent.BlockedTool{
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
func (o *Options) prepareWsState(cfg *jsonx.OrderedMap, loadedPacks []*packload.Pack) string {
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
	for _, subdir := range append([]string{
		"npm-global", "local", "go", "yolo-shims", "yolo-launchers", "config",
	}, overlaySubdirs...) {
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

	// Mountpoints for the PACK-DECLARED `files` trees, same GlobalHome recipe and same
	// reason as writable_home_dirs above (the source side needs nothing: the bind source
	// IS the pack's staged tree, which staging already created).
	preparePackFiles(loadedPacks)
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

	// One-time LAYOUT MIGRATIONS from a pre-overlay-subdir yolo (flat
	// <wsState>/claude-projects → <wsState>/claude/projects, and the same for copilot's
	// sessions).
	//
	// Gated on whether the pack that owns the dir is LOADED, not on an agent name. The
	// gate matters either way: creating <wsState>/claude/ for a user with no claude pack
	// would leave an empty dir that looks like state, and podman would then bind it into
	// a jail nothing writes.
	//
	// Transitional. Each entry cleans up a layout no yolo has written for some time, and
	// they can all go once no live workspace still carries one.
	if hasWritableDir(loadedPacks, ".claude") {
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
	if hasWritableDir(loadedPacks, ".copilot") {
		migrateOldOverlay(filepath.Join(wsState, "copilot-sessions"), filepath.Join(wsState, "copilot", "session-state"))
	}
	return wsState
}

// hasWritableDir reports whether any loaded pack declared dir writable.
func hasWritableDir(packs []*packload.Pack, dir string) bool {
	for _, d := range packload.WritableDirs(packs) {
		if d == dir {
			return true
		}
	}
	return false
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

// briefingHostOverlay returns the host-home path a briefing contribution prepends
// (its `after: "host:<path>"`), or "" for none. Replaces the old filename-based
// magic-string dispatch (isBriefingMount) — a briefing is now a kind, not a mount
// whose source happened to be named AGENTS.md/CLAUDE.md.
func briefingHostOverlay(c packdecl.Contribution) string {
	if strings.HasPrefix(c.After, "host:") {
		return strings.TrimPrefix(c.After, "host:")
	}
	return ""
}

// briefingStagingName is the staging filename for one pack's briefing. Per-pack rather
// than per-agent, so two packs cannot collide on one staged file.
func briefingStagingName(pack string) string { return "briefing-" + pack + ".md" }

// packSkillTargets turns pack mount declarations into skills staging targets.
//
// It used to also set each target's HostSource to `c.Into` — the DESTINATION — so the jail's
// last skills layer read the host's own ~/.<agent>/skills. That is S3: since `apply --host`
// composes those directories, the jail was reading yolo's generated output back in as "the
// user's tree", and the local pack's content therefore arrived twice. The user's own skills
// reach a jail through the local pack, which is an ordinary pack entry appended last.
func packSkillTargets(loadedPacks []*packload.Pack) []jailcontent.SkillTarget {
	var out []jailcontent.SkillTarget
	for _, p := range loadedPacks {
		for _, c := range p.Decl.Contributions() {
			if c.Kind != packdecl.KindSkills {
				continue
			}
			out = append(out, jailcontent.SkillTarget{
				Staging: jailcontent.SkillStagingName(p.Name), Dest: c.Into,
			})
		}
	}
	return out
}
