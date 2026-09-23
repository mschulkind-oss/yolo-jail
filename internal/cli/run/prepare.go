package run

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
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
	netMode := o.resolveNetMode(cfg)

	// WHAT THE LAUNCH APPLIES, not what the config asked for — the same predicate
	// assembleRunCmd spells its `--net=` selector from, so the two cannot disagree
	// (docs/design/backend-parity.md §6).
	//
	// NO HOST PROBE BELONGS IN THIS FUNCTION, and this is where the temptation arrives:
	// the fuller truth about networking is hostLoopbackFactsFor's — which rootless stack
	// podman picked, whether it takes the loopback-forwarding option — and it is one
	// `podman info` plus one `<stack> --help` away. This function runs on EVERY
	// invocation including attach, where it is the only work done and runs no subprocess
	// at all, and those two answers can differ between the launch that started a jail and
	// the attach that re-renders its briefing. So the briefing carries the DETERMINISTIC
	// decision only: appliedNetMode reads the runtime, the config and o.inContainer()
	// (a PathExists seam, no process), which re-derive the same answer on attach as at
	// launch. What the probe decided is already in the jail as $YOLO_HOST_LOOPBACK, which
	// the bridge paragraph names.
	appliedNet := appliedNetMode(rt, netMode, o.inContainer())

	// The two port keys read that SAME applied mode, because the assembler's publish gate
	// does now: it used to read the configured one, which is how a nested launch — forced
	// to host networking whatever `network.mode` says — emitted -p flags plus the DNAT
	// sysctl the host namespace refuses, and could not start at all. Both ends moved
	// together on purpose. On Apple Container the applied mode is bridge however the key
	// is set, so an unhonored `network.mode: "host"` no longer takes the ports with it,
	// here or in the argv.
	publishPorts, forwardHostPorts := briefingPortsFor(appliedNet, netSec, cfgMap(cfg, "providers"))

	// Blocked-tools → jailcontent.BlockedTool records.
	blocked := blockedToolRecords(config.NormalizeBlockedToolsWith(cfgMap(cfg, "security"),
		packload.BlockedTools(staged.packs)))

	// mount_descriptions for existing config.mounts, filtered to the ones the backend
	// will actually bind: Apple Container refuses every one of them (roBindsUnsupported),
	// and a section headed "Additional Context Mounts (read-only)" naming /ctx paths that
	// were never mounted is the same lie as a network mode that was never applied.
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
	mountDescriptions = o.appliedCtxMounts(rt, mountDescriptions)

	// ACTIVE loopholes (name, description) — census site 1, through the converged set.
	//
	// GATED ON THE BACKEND, because `Honored()` has no backend term. It answers "is this
	// loophole enabled, supported here, and allowed to run host code" — all true on a
	// backend that then starts none of them. Apple Container returns from startLoopholes
	// before any host service starts, so there the unfiltered list renders a section headed
	// "host capabilities wired into this jail" describing daemons that do not exist.
	//
	// macos-user was the second such backend until 2026-09-17 and is no longer one: it now
	// routes through startLoopholesDisclosed and starts every admitted loophole, so the
	// section is TRUE there and this gate correctly stops filtering it. The gate needed no
	// edit for that — it asks the backend, and the backend's answer changed — which is the
	// property to preserve if a third backend ever joins or leaves.
	//
	// That is the exact failure briefingLoopholes was already fixed once to prevent —
	// its own comment records switching from Enabled() to Active() so an enabled-but-
	// inactive loophole would stop being advertised. The backend is the third term that
	// census never got. An agent reading a false capability list does not merely lack a
	// feature; it plans around one it does not have.
	var loops []jailcontent.Loophole
	if backendInertReason(rt) == "" {
		loops = briefingLoopholes(cfgMap(cfg, "loopholes"))
	}

	// Pack staging (C3) already ran, ABOVE the backend dispatch, and its ordering
	// relative to skills is still load-bearing in the same way: stagePacks sets
	// jailcontent.SetPackSkillDirs as a side effect and PrepareSkills consumes it below.
	loadedPacks, packBriefings := staged.packs, staged.briefings

	// PACK-DECLARED skills destinations. A pack mount whose source is "skills" says
	// "put my skills tree here"; core builds a staging dir per pack and mounts it there.
	jailcontent.SetPackSkillTargets(packSkillTargets(loadedPacks))

	// The user's `lsp_servers` table, for the one plugin yolo renders from it — option D of
	// docs/reference/mcp-configuration.md#oq-lsp1. Injected rather than read inside jailcontent for the
	// same reason the two setters above are: that package is called from here and does not read
	// config itself.
	jailcontent.SetLSPServers(cfgMap(cfg, "lsp_servers"))

	// Skills staging.
	staging, err := jailcontent.PrepareSkills(cname, homeDir(), nil)
	if err != nil {
		return "", err
	}

	// Resource limits the backend actually IMPOSES (sorted-key rendering handled inside
	// BriefingContent) — the same list assembleRunCmd turns into flags. Read straight
	// from the `resources` map, this told an Apple Container jail its pids_limit was
	// kernel-enforced when that flag is never passed there, and told it nothing at all
	// about the memory and cpu caps the backend applies by default.
	resources := briefedResourceLimits(rt, cfgMap(cfg, "resources"))

	// The one-time handoff: a fresh .yolo/handover.md pointer the host agent filed for
	// this transition, surfaced via the briefing and then consumed so it never resurfaces
	// as a stale task. READ here, CONSUMED below — the two are deliberately split around
	// the write loop, because consuming a handoff that was never written anywhere burns
	// it for good. A jail with no briefing destination (no packs — run.go's
	// warnIfNoPacks) writes zero briefings, and an unconditional consume ate the pointer
	// on exactly the launch that could not deliver it.
	handoff := readHandoff(o.Workspace)

	in := jailcontent.BriefingInput{
		Workspace:          o.Workspace,
		BlockedTools:       blocked,
		MountDescriptions:  mountDescriptions,
		NetMode:            netMode,
		AppliedNetMode:     appliedNet,
		PublishPorts:       publishPorts,
		ForwardHostPorts:   forwardHostPorts,
		Loopholes:          loops,
		Resources:          resources,
		ProvisioningFailed: jailcontent.ReadProvisioningFailed(o.Workspace),
		Confinement:        string(config.ResolveConfinement(cfg)),
		// THE MECHANISM, which is the second axis of the header and was a boolean until
		// OQ-DP2 (docs/design/declaration-parity.md §2.3). `rt` rather than the config,
		// for the same reason the network, resource and mount fields above are: this
		// describes what the launch APPLIES. The header derives both answers from it —
		// whether there is a container at all, and which primitives are actually
		// enforcing the boundary — where `NoContainer: rt == "macos-user"` could only
		// carry the first, so a Seatbelt sandbox was handed the container's own vector.
		Mechanism: rt,
		// The agent's home, which the briefing cannot derive: internal/jailcontent cannot
		// import internal/macosuser without routing through internal/entrypoint, whose
		// tests import jailcontent back. Empty for every container backend, which renders
		// /home/agent exactly as before.
		Home: nativeHomeFor(rt),
		// The platform this launch runs ON, for the one answer the mechanism does not
		// carry: a `guest` notch has no backend yet, so its Seatbelt-vs-Landlock spelling
		// comes from here. `yolo describe` passes paths.IsMacOS for the same input; this
		// path takes the injectable seam so the briefing stays deterministic in tests.
		IsMacOS: o.IsMacOS,
		// THE STANDING CONSTRAINTS OF THIS BACKEND, in the agent's voice. Unset for the
		// whole life of the field (DP-B21): backendLimits had no production call site, so
		// the "What this environment does NOT do for you" section never rendered once —
		// while run.noteMacosUserHostByteGaps' no-refusal carve-out says in as many words
		// that it "is only defensible while the deficiency is SAID — here, and in the
		// agent's own briefing (backendLimits)". Half of a shipped ruling's stated
		// precondition did not execute.
		BackendLimits: backendLimits(rt, staged.packs, cfg),
		Handoff:       handoff,
	}
	briefingBody := jailcontent.BriefingContent(in)
	briefingBody = jailcontent.ComposeBriefing(briefingBody, cfgStr(cfg, "agents_md_extra"))

	// Write one briefing per PACK-DECLARED briefing DESTINATION. The pack says where its
	// prose goes; core composes that destination's content and writes it to the matching
	// staging file, and (for a pack whose origin permits it) prepends the user's own host
	// briefing first, so a personal AGENTS.md still outranks anything a pack ships.
	//
	// This is the loop that used to iterate selected agents, which is why a zero-agent
	// jail wrote NO briefing at all. It now follows declarations, so a pack always gets
	// its briefing whether or not anything calls it an agent.
	//
	// AND IT IS NOW DESTINATION-FIRST, which is the jail half of docs/reference/agent-briefings.md#where-each-notch-narrows.
	// Composition used to happen ABOVE this loop — one body, folded once, written to every
	// destination — so the loop was per (pack, contribution) with a per-PACK staging name and
	// every file held identical bytes. That is what made scoping impossible: a pack whose
	// rules apply to one agent had to broadcast them to all of them or drop them. Composing
	// INSIDE the loop, with the destination's own declared identity as the input, is the whole
	// mechanism — and it lifts the one-prose-per-pack limit packload.BriefingProse recorded
	// for free, because the composition input is now a list of contributions rather than one
	// text per pack.
	home := homeDir()
	// The destinations `yolo host apply` composed itself, which must NOT be prepended: they
	// already hold every pack's prose, and this loop is about to compose the same packs again.
	// See entrypoint.GeneratedHostBriefings — the briefing half of S3.
	generated := entrypoint.GeneratedHostBriefings(home)
	// And, in a jail, every destination: there each one is a read-only bind of the OUTER
	// launch's staged briefing, which that record cannot know about (mayPrependHostBriefing).
	dests := briefingDestinations(loadedPacks)
	briefingsWritten := 0
	for _, d := range dests {
		// Pack prose last. `d.Agent` is the identity this destination declared for itself,
		// and prose that named an audience reaches it only if that audience names this
		// identity. Each pack's section is labelled with its name only when the user's
		// `briefing_provenance` asks for it — unlabelled by default, because the label made
		// agents treat the user's every-repository rules as foreign (config.BriefingProvenance).
		content := jailcontent.ComposePackBriefings(briefingBody, packBriefings, d.Agent,
			config.BriefingProvenance(cfg))
		if hostOverlay := briefingHostOverlay(d.After); hostOverlay != "" {
			if src := filepath.Join(home, hostOverlay); mayPrependHostBriefing(src, home, dests, generated) {
				content = jailcontent.PrependHostBriefing(src, content)
			}
		}
		if err := jailcontent.WriteBriefing(
			filepath.Join(staging, briefingStagingName(d.Into)), content); err != nil {
			return "", err
		}
		briefingsWritten++
	}

	// Delivered, so consume: the handoff reached at least one briefing this launch and
	// must not resurface as a stale task. With zero briefings written the pointer stays
	// fresh for the launch that can actually carry it.
	if handoff != "" && briefingsWritten > 0 {
		o.noteHandoffConsumed()
	}
	// `rt` reaches four fields now — loopholes, network, resources, mounts — where it was
	// `_ = rt` for as long as this function took the runtime and ignored it, composing
	// every field straight from cfg and so describing what was CONFIGURED rather than
	// what was APPLIED (docs/design/backend-parity.md §6).
	//
	// WHAT IS LEFT, stated so the next reader does not have to re-derive it:
	//
	//   - The bridge paragraph's own claims — the `host.containers.internal` hop and
	//     `$YOLO_HOST_LOOPBACK` — are podman's shape and are rendered for Apple Container
	//     too. Whether an AC container reaches a host loopback listener AT ALL was OQ-BP-4;
	//     a Mac settled it on 2026-09-16 and the answer is NO, in both directions and for
	//     published ports as well (docs/plans/setup-support-gaps.md §5.1 rows 6-7), so this
	//     paragraph is still rendered to a jail for which it is false. Nothing here can see
	//     that (AGENTS.md's nested-jail carve-out); the paragraph's own numeric address is
	//     gone, which is a smaller lie rather than none.
	//   - podman's own `--pids-limit 32768` is applied and not briefed, on purpose —
	//     briefedResourceLimits says why.
	//   - macos-user REACHES ALL OF THIS, and the sentence that stood here saying it
	//     reached none of it ("Run() returns before runContainer, so that backend gets no
	//     briefing at all — a delivery gap rather than a false sentence") was true until
	//     the B-0 content fix put this function on that backend's arm of Run, and exactly
	//     inverted afterwards: the briefing was delivered and four of its sections were
	//     false. The four are closed together (declaration-parity.md §11 step 1) — net
	//     mode and both port lists through appliedNetMode, the /ctx list through
	//     appliedCtxMounts, the resource line through briefedResourceLimits, and the
	//     enforcement vector plus the standing-constraints section through Mechanism and
	//     BackendLimits above. What remains open there is the ## Environment block, which
	//     is still written in the container's own vocabulary — `/workspace`,
	//     `/home/agent`, "NixOS-based minimal container" — and is none of those things on
	//     this backend. It has no catalog row yet.
	//   - The startup BANNER keeps its own spelling of the resource rule (resPartsFor in
	//     run.go), so on Apple Container it prints only what the config set while the
	//     backend caps anyway. Same shape, a different surface: that line is the human's,
	//     and it is read once beside a launch rather than planned around by an agent.
	return staging, nil
}

// briefingPortsFor is the two port lists the jail's briefing advertises, in
// (publish, forward) order — network.ports is HOST → JAIL, forward_host_ports is
// JAIL → HOST, and they are returned as separate values because they are separate
// directions with opposite entry orders. A jail used to be told only the second
// one, leaving an agent unable to see which of its own ports were published.
//
// Both are gated on bridge mode, matching the launch: under host networking the
// stacks are shared and assembleRunCmd honors neither key, so advertising them
// would describe forwarding that is not happening.
//
// A FUNCTION rather than six inline lines, for the reason briefingLoopholes gives
// below: the same expression retyped in a test asserts nothing about this file.
// briefingPortsFor is the port pair the briefing describes, and the forward half is the
// MERGED list rather than the config section (OQ-PC2,
// docs/reference/wire-bridge.md#oq-pc2).
//
// A forward a user provider caused is a hole into the host that the user's own config cannot
// be grepped for — so a briefing built from `network.forward_host_ports` alone tells the
// agent a port is closed when the launch has opened it. Merging here rather than at the call
// site is deliberate: this function is what refreshJailBriefings calls, so the merge cannot
// be lost by a caller that forgets it.
//
// netSec may be nil while providers is not — a config with providers and no `network`
// section still forwards, which is why the nil guard moved off the early return.
func briefingPortsFor(netMode string, netSec, providers *jsonx.OrderedMap) (publish, forward []any) {
	if netMode != "bridge" {
		return nil, nil
	}
	if netSec != nil {
		publish = asAnyList(mapGet(netSec, "ports"))
		forward = asAnyList(mapGet(netSec, "forward_host_ports"))
	}
	return publish, mergeHostForwards(forward, localProviderForwards(providers))
}

// briefingLoopholes is the loophole list the jail's briefing advertises — census site 1
// (docs/reference/loophole-system.md#selection-and-discovery), read through the converged set.
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

// orderedMapToStrAny is GONE: BriefingInput.Resources is fed by briefedResourceLimits
// (backendcaps.go) now, so the briefing states the limits the backend imposes rather than
// re-typing the `resources` block the user wrote.

// prepareWsState prepares the ws_state overlay: create the
// per-workspace overlay dirs + touch the overlay files, seed selected agents'
// config dirs, sync claude.json, and run the old-overlay migrations. Returns the
// ws_state path (<workspace>/.yolo/home).
func (o *Options) prepareWsState(cfg *jsonx.OrderedMap, loadedPacks []*packload.Pack, rt string) string {
	wsState := paths.WorkspaceHomeState(o.Workspace)
	// Through the chokepoint rather than a bare MkdirAll of wsState's parent: this is the
	// WIDEST creator of <workspace>/.yolo in the pipeline (the whole home overlay hangs off
	// it), so it is the one that most wants the state dir's own .gitignore and its refusal
	// of a directory that may not be a workspace. Defence in depth — Run's workspace-scope
	// guard has already refused such a workspace long before here — and best-effort in the
	// same way the mkdirs below are: the launch fails on the missing bind source with a
	// message naming the path, which is the failure a human can act on.
	_, _ = paths.EnsureWorkspaceStateDir(o.Workspace)
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
		"npm-global", "local", "go", "yolo-bin", "config",
		filepath.Join("pi", "agent"),
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

	// Mountpoints for the PACK-DECLARED `files` trees, skills, and briefings. A files
	// destination below writable pack state belongs in wsState and carries an ownership
	// record so a dropped contribution can retire it; destinations in the read-only base,
	// plus skills and briefings, still need their GlobalHome mountpoints.
	for _, archived := range preparePackFiles(loadedPacks, wsState, rt) {
		o.pr(o.Stdout).printf("[yellow]Archived an unclaimed zero-byte legacy pack-file mountpoint: %s[/yellow]", archived)
	}
	preparePackFilesGlobal(loadedPacks, rt)
	for _, target := range packSkillTargets(loadedPacks) {
		_ = os.MkdirAll(filepath.Join(paths.GlobalHome(), filepath.FromSlash(target.Dest)), 0o755)
	}
	for _, d := range briefingDestinations(loadedPacks) {
		dest := filepath.Join(paths.GlobalHome(), filepath.FromSlash(d.Into))
		_ = os.MkdirAll(filepath.Dir(dest), 0o755)
		touchFile(dest)
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

	// THE MACHINE-WIDE TIER, RESCUED OUT OF THE PER-WORKSPACE ONE (#39).
	//
	// Apple Container mounted no shared dirs until 2026-08-24, so its /home/agent →
	// wsState bind is where a shared dir's contents actually landed: an AC user's
	// ~/.claude-shared-credentials/.credentials.json is sitting in THIS workspace's
	// state dir. Now that the dir is mounted from GlobalHome, that mount shadows the
	// stranded copy — so without this the fix would read to the user as "the upgrade
	// logged me out", with a perfectly good credential left behind a mount they cannot
	// see.
	//
	// COPY, NEVER MOVE, and only into a file that is missing — migrateOldOverlay's
	// existing contract. That is not caution for its own sake: the Python-era version
	// of this same credential path unlinked the real file before re-linking and
	// destroyed the token on every boot (the symptom that opened #39). Writing new
	// credential-MOVING code here would be re-running the exact experiment that failed.
	// A stranded duplicate costs disk; a deleted credential costs a login yolo cannot
	// perform for you.
	//
	// Ungated by runtime on purpose. The source only exists where the bug put it, so
	// this is a no-op everywhere else (migrateOldOverlay returns on a missing or empty
	// source), and gating on rt would make a repair that is about a DIRECTORY depend on
	// which backend happens to be launching now — the same coupling that caused the bug.
	for _, dir := range packload.SharedDirs(loadedPacks) {
		migrateOldOverlay(filepath.Join(wsState, dir), filepath.Join(paths.GlobalHome(), dir))
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

// briefingHostOverlay returns the host-home path a briefing `after` prepends (its
// `after: "host:<path>"`), or "" for none. Replaces the old filename-based
// magic-string dispatch (isBriefingMount) — a briefing is now a kind, not a mount
// whose source happened to be named AGENTS.md/CLAUDE.md.
//
// It takes the `after` STRING rather than the contribution because its caller is now
// destination-first (briefingDest carries the declaring contribution's `after`, since the
// prepend is a property of the destination and not of every pack that briefs there).
func briefingHostOverlay(after string) string {
	if strings.HasPrefix(after, "host:") {
		return strings.TrimPrefix(after, "host:")
	}
	return ""
}

// packSkillTargets turns pack mount declarations into skills staging targets.
//
// It used to also set each target's HostSource to `c.Into` — the DESTINATION — so the jail's
// last skills layer read the host's own ~/.<agent>/skills. That is S3: since `yolo host apply`
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
			// A CONTRIBUTION THAT NAMES NO DESTINATION IS NOT A DESTINATION. An ADDRESSED
			// skills tree (`{"kind":"skills","agents":["claude"]}`) declares who its content
			// is FOR and never where it goes, because where an agent reads is that agent
			// pack's business (docs/reference/agent-briefings.md#ba-p4) — so `Into` is empty by design, and
			// its content reaches real destinations by AUDIENCE MATCHING, not by a mount of
			// its own.
			//
			// Without this guard `Dest` was "", the bind resolved to the home ROOT, and podman
			// refused every such launch with `"/home/agent": duplicate mount destination`.
			// Measured 2026-09-03 and reproduced at 49bb2088, so it shipped with
			// the audience selector's first two build steps on 2026-09-02; an addressed BRIEFING was never
			// affected because only skills mount.
			if c.Into == "" {
				continue
			}
			out = append(out, jailcontent.SkillTarget{
				Staging: jailcontent.SkillStagingName(p.Name), Dest: c.Into, Agent: c.Agent,
			})
		}
	}
	return out
}

// handoffPointer is the per-workspace file the host agent files to carry a one-time task
// across the boundary, and handoffConsumed is where it moves once a briefing has carried
// it (docs/reference/host-to-jail-handoff.md).
const (
	handoffPointer  = "handover.md"
	handoffConsumed = handoffPointer + ".consumed"
)

// readHandoff returns the content of <workspace>/.yolo/handover.md, or "" when the host
// filed no handoff for this launch. Reading does not consume — see consumeHandoff.
func readHandoff(workspace string) string {
	data, err := os.ReadFile(filepath.Join(paths.WorkspaceStateDir(workspace), handoffPointer))
	if err != nil {
		return ""
	}
	return string(data)
}

// consumeHandoff renames <workspace>/.yolo/handover.md to handover.md.consumed and
// reports whether it did. This is what makes the carry-in one-time: a present pointer is
// fresh, a consumed one is done, so a later launch surfaces no handoff and the task comes
// from the user. Renaming rather than deleting leaves a visible "already handed off"
// state that a human can undo.
//
// Called from refreshJailBriefings — host-side, deterministic, not agent self-erasure —
// and only AFTER a briefing carrying the handoff was actually written. Consuming ahead of
// the write is the failure that loses a handoff outright: a jail with no briefing
// destination renders the section nowhere and would still have burned the pointer.
//
// The rename is best-effort in the other direction too: if it fails (a read-only .yolo,
// say), the handoff still surfaced this launch and resurfaces on the next one — the
// pre-consumption behavior, which is noisy but never loses the task.
func consumeHandoff(workspace string) bool {
	dir := paths.WorkspaceStateDir(workspace)
	return os.Rename(filepath.Join(dir, handoffPointer), filepath.Join(dir, handoffConsumed)) == nil
}

// noteHandoffConsumed consumes the pointer and says so on stderr.
//
// The notice is the cheap half of a residual this design cannot close: core has no agent
// concept (AGENTS.md), so it cannot tell `yolo -- claude` from `yolo -- bash`, and a
// briefing written for a shell burns the handoff just as thoroughly as one written for an
// agent. Rather than invent an agent test to guess at intent, the launch that consumes a
// handoff says which file it took and how to put it back — a burn stays recoverable, and
// is visible at the moment it happens instead of being discovered by an agent that never
// got its task.
func (o *Options) noteHandoffConsumed() {
	if !consumeHandoff(o.Workspace) {
		return
	}
	o.pr(o.Stderr).printf("[dim]Handoff: .yolo/%s surfaced in this jail's briefing and consumed "+
		"(restore with `mv .yolo/%s .yolo/%s`).[/dim]", handoffPointer, handoffConsumed, handoffPointer)
}

// nativeHomeFor is the agent's home directory for a backend that runs no container,
// and "" for one that does — which the briefing reads as the container's /home/agent.
//
// It exists so internal/jailcontent never has to import internal/macosuser: that path
// runs through internal/entrypoint, whose tests import jailcontent, and the cycle it
// would create is only avoided today by accident of which files are test files.
func nativeHomeFor(rt string) string {
	if slices.Contains(paths.NativeRuntimes, rt) {
		return macosuser.SandboxHome()
	}
	return ""
}
