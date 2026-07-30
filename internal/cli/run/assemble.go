package run

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/storage"
)

// MISE_STORE_VOLUME is the named volume backing the jail-land mise store on
// macOS (podman + Apple Container), mounted at /mise. Versioned name (bump the
// suffix to force a fresh store).
const miseStoreVolume = "yolo-mise-data-v2"

// assembleInput carries everything the ordered-argv assembler needs that isn't
// on Options. It is populated by the fresh-launch path before assembly; grouping
// it keeps assembleRunCmd a pure function of its inputs ().
type assembleInput struct {
	cfg   *jsonx.OrderedMap
	rt    string
	cname string
	// packs are this run's loaded packs (embedded official + configured). Their
	// DECLARATIONS drive the mounts below — writable dirs, mount targets, host-file
	// grants — which is what lets core stay ignorant of what an "agent" is.
	packs      []*packload.Pack
	agentsPath string // AGENTS_DIR/<cname> (briefings + skills staging)
	// packStaging is AGENTS_DIR/<cname>/packs — the staged pack trees, mounted :ro so
	// the entrypoint renders the same declarations the host read.
	packStaging  string
	wsState      string // <workspace>/.yolo/home
	miseStore    string // _jail_mise_store_dir()
	hostTZ       string // "" => no TZ
	yoloVersion  string // _git_describe_version() or "unknown"
	mountTargets map[string]struct{}
	// lspNPMInstall / lspGoInstall are the resolved YOLO_LSP_*_INSTALL values
	// (ResolveLSPInstalls over the lsp_servers keys).
	lspNPMInstall string
	lspGoInstall  string
	// storePruneOK is true when the host CLI proved no other jail is live and
	// grants the in-jail store prune (`-e YOLO_STORE_PRUNE_OK=1`). Set by the
	// lifecycle phase; false leaves the env unset.
	storePruneOK bool
	// cacheRelocations are the user-scope cache subdir → host dir relocations,
	// already loaded, validated and provisioned by the run pipeline (assembly
	// only emits the -v pairs, and must stay free of the fs access + the
	// user-config read that producing them requires).
	cacheRelocations []config.CacheRelocation
	// writableHomeDirs are extra home-relative paths (config writable_home_dirs)
	// mounted read-write off <wsState>/writable-home, letting an agent extension
	// that hardcodes a $HOME path (e.g. ~/.pi-lens) write through the :ro base.
	// Already derived + validated by the run pipeline; prepareWsState created
	// each backing dir, so assembly only emits the -v pairs.
	writableHomeDirs []string
	// hostFiles are the resolved user `host_files` entries (config.LoadHostFiles),
	// already scope-filtered and validated by the run pipeline — assembly only
	// emits the -v pairs + the YOLO_HOST_FILES env, and must stay free of the
	// user-config read and the host stat that producing them requires (the same
	// split as cacheRelocations). prepareHostFiles provisioned each destination's
	// writable staging before assembly.
	hostFiles []config.HostFileEntry
}

// lspNPM / lspGo return the resolved YOLO_LSP_*_INSTALL values.
func (in *assembleInput) lspNPM() string { return in.lspNPMInstall }
func (in *assembleInput) lspGo() string  { return in.lspGoInstall }

// storePruneEnv returns the `-e YOLO_STORE_PRUNE_OK=1` pair when granted, else
// nil.
// that set storePruneOK live in the lifecycle phase).
func (in *assembleInput) storePruneEnv() []string {
	if in.storePruneOK {
		return []string{"-e", "YOLO_STORE_PRUNE_OK=1"}
	}
	return nil
}

// assembleRunCmd builds the ordered container argv: flags-before-image, the -e
// env block, the mount order, network, devices, GPU/KVM, resources, loopholes,
// then the image + "yolo-entrypoint".
// It is a pure function of (o, in) EXCEPT for the ws_state dir/file touches and
// venv-shadow backing mkdirs performed inline while building the argv — those
// side effects are preserved (they are part of the launch, not the argv), so
// callers pass a prepared ws_state. The final internal command and the
// host-service -e insertion are handled by the lifecycle phase.
// The argv this returns ends at the image ref + "yolo-entrypoint"; the
// final_internal_cmd is appended after inserting host-service env at
// index(image); see runContainer for that tail.
func (o *Options) assembleRunCmd(in *assembleInput) []string {
	cfg := in.cfg
	rt := in.rt
	out := o.pr(o.Stdout)

	// --- Network mode + ports ---
	netMode := o.Network
	if netSec := cfgMap(cfg, "network"); netSec != nil {
		if m := mapStr(netSec, "mode"); m != "" {
			netMode = m
		}
	}
	var publishArgs []string
	if netMode == "bridge" {
		if netSec := cfgMap(cfg, "network"); netSec != nil {
			for _, p := range asAnyList(mapGet(netSec, "ports")) {
				publishArgs = append(publishArgs, "-p", pyStrCoerce(p))
			}
		}
	}
	var forwardHostPorts []any
	if netMode == "bridge" {
		if netSec := cfgMap(cfg, "network"); netSec != nil {
			forwardHostPorts = asAnyList(mapGet(netSec, "forward_host_ports"))
		}
	}

	normalizedBlocked := config.NormalizeBlockedTools(cfgMap(cfg, "security"))
	blockedConfigJSON := jsonDumps(normalizedBlocked)

	// --- Extra mounts (config.mounts → -v host:container:ro) ---
	var mountArgs []string
	ctxMountsUnsafe := rt == "container"
	for _, mountAny := range cfgList(cfg, "mounts") {
		mount, ok := mountAny.(string)
		if !ok {
			continue
		}
		hostPath, containerPath := splitMountSpec(mount)
		hostPath = resolveExpand(hostPath)
		if !fileExists(hostPath) {
			out.print("[yellow]Warning: mount path does not exist, skipping: " + hostPath + "[/yellow]")
			continue
		}
		if ctxMountsUnsafe {
			out.print("[yellow]Skipping mount " + hostPath + " → " + containerPath + ": Apple " +
				"Container ignores read-only (:ro), so it would be writable. " +
				"Use `YOLO_RUNTIME=podman` for read-only context mounts.[/yellow]")
			continue
		}
		mountArgs = append(mountArgs, "-v", hostPath+":"+containerPath+":ro")
	}

	// --- run_flags ---
	runFlags := []string{"--rm", "-i", "--init", "--read-only", "--name", in.cname}
	if rt != "container" {
		// insert("--cgroupns=private", 3)
		runFlags = insertAt(runFlags, 3, "--cgroupns=private")
	}
	if rt == "podman" && o.IsLinux {
		runFlags = append(runFlags, "--read-only-tmpfs=false")
	}
	if rt == "podman" {
		runFlags = append(runFlags, "--pull=never")
		runFlags = append(runFlags, "--log-driver", "none")
		runFlags = append(runFlags, "--security-opt", "unmask=/proc/sys")
	}
	if o.IsTTYStdout() {
		runFlags = append(runFlags, "-t")
	}

	// --- base run_cmd (mounts) ---
	var runCmd []string
	if rt == "container" {
		runCmd = appleContainerBaseMounts(rt, runFlags, o.Workspace, in, out)
	} else {
		runCmd = podmanBaseMounts(rt, runFlags, o.Workspace, in, o.IsMacOS)
		// Ephemeral scratch dirs.
		runCmd = append(runCmd, ScratchMountArgs(cfgStr(cfg, "ephemeral_storage"))...)
		// PACK-DECLARED writable dirs, backed per-workspace. Core does not know these
		// belong to an "agent" — a pack asked for a writable dir and got one.
		for _, dir := range packload.WritableDirs(in.packs) {
			runCmd = append(runCmd, "-v",
				filepath.Join(in.wsState, strings.TrimPrefix(dir, "."))+":/home/agent/"+dir)
		}
		// B5: machine-wide (cross-jail) dirs, from the registry rather than a
		// hardcoded per-agent branch. These come from GlobalHome, NOT ws_state, so a
		// credential survives across workspaces — see agents.SharedDirs for why that
		// tier exists and why widening it is a real decision.
		for _, dir := range packload.SharedDirs(in.packs) {
			runCmd = append(runCmd, "-v",
				filepath.Join(paths.GlobalHome(), dir)+":/home/agent/"+dir)
		}
	}

	// --- Common env block (frozen order) ---
	runCmd = append(runCmd, o.commonEnvBlock(in, blockedConfigJSON, netMode)...)

	// --- yolo-user-env.sh (written by the lifecycle phase; mounted here) ---
	// Apple Container can't do single-file mounts under the ws_state parent
	// mount without dropping it, so it materializes the file into ws_state
	// instead. Skipping the container branch silently dropped every env_sources
	// var (the file is sourced with 2>/dev/null).
	userEnvFile := filepath.Join(in.wsState, "yolo-user-env.sh")
	if rt == "container" {
		acMaterialize(userEnvFile, ".config/yolo-user-env.sh", in.wsState)
	} else {
		runCmd = append(runCmd, "-v", userEnvFile+":/home/agent/.config/yolo-user-env.sh")
	}

	// --- container cwd ---
	// The in-jail CLI no longer needs a source bind: the image bakes the flake
	// bundle + real-file binaries at /opt/yolo-jail (installPrefix in flake.nix),
	// so the shared resolver (internal/reporoot) finds the repo exe-relative,
	// identically inside and outside the jail. When the workspace itself is the
	// yolo-jail checkout (self-hosting), the cwd-walk wins instead. Either way
	// there is nothing to bind and no YOLO_REPO_ROOT to set.
	runCmd = append(runCmd, "--workdir", "/workspace")

	// --- nested-container detection ---
	inContainer := !o.IsMacOS && (o.PathExists("/run/.containerenv") || o.PathExists("/.dockerenv"))

	// --- GPU availability probe (gates the uidmap/runc branch below) ---
	gpuRequested := false
	gpuVendor := "nvidia"
	gpuUnavailableReason := ""
	gpuEnabled := false
	if gpuSec := cfgMap(cfg, "gpu"); gpuSec != nil {
		gpuRequested = mapBoolOr(gpuSec, "enabled", false)
		gpuVendor = mapStrOr(gpuSec, "vendor", "nvidia")
	}
	if gpuRequested {
		var okGPU bool
		if gpuVendor == "amd" {
			okGPU, gpuUnavailableReason = o.rocmHostAvailable(rt)
		} else {
			okGPU, gpuUnavailableReason = o.gpuHostAvailable(rt)
		}
		gpuEnabled = okGPU
	}

	// --- Podman nesting / GPU userns / device+cap block ---
	if rt == "podman" {
		runCmd = append(runCmd, o.podmanNestingArgs(inContainer, gpuEnabled, gpuVendor)...)
	}

	// --- host nix daemon + store ---
	nixSocket := "/nix/var/nix/daemon-socket"
	nixStore := "/nix/store"
	if shouldMountHostNix(rt, o.PathExists(nixSocket), o.PathExists(nixStore), o.IsMacOS, o.Getenv("YOLO_NIX_HOST_DAEMON")) {
		runCmd = append(runCmd,
			"-v", nixSocket+":"+nixSocket,
			"-v", nixStore+":"+nixStore+":ro",
			"-e", "NIX_REMOTE=daemon")
	}

	// --- network mode flag ---
	if rt == "container" {
		// Apple Container handles networking internally.
	} else if rt == "podman" && inContainer {
		runCmd = append(runCmd, "--net=host")
	} else if netMode != "bridge" {
		runCmd = append(runCmd, "--net="+netMode)
	}

	// --- git identity + global gitignore (host-composed, :ro-mounted) ---
	runCmd = append(runCmd, o.gitIdentityMountArgs(rt, in.wsState, in.mountTargets)...)

	// --- publish + extra mounts ---
	runCmd = append(runCmd, publishArgs...)
	runCmd = append(runCmd, mountArgs...)

	// --- published-port DNAT sysctl + env ---
	if len(publishArgs) > 0 && rt == "podman" {
		runCmd = append(runCmd, "--sysctl", "net.ipv4.conf.all.route_localnet=1")
		var publishedPorts []string
		if netSec := cfgMap(cfg, "network"); netSec != nil {
			for _, p := range asAnyList(mapGet(netSec, "ports")) {
				spec := pyStrCoerce(p)
				proto := "tcp"
				if i := strings.LastIndex(spec, "/"); i >= 0 {
					proto = spec[i+1:]
					spec = spec[:i]
				}
				parts := strings.Split(spec, ":")
				containerPort := parts[len(parts)-1]
				publishedPorts = append(publishedPorts, containerPort+"/"+proto)
			}
		}
		if len(publishedPorts) > 0 {
			runCmd = append(runCmd, "-e", "YOLO_PUBLISHED_PORTS="+jsonDumpsStrings(publishedPorts))
		}
	}

	// --- host port forwarding flags (the socat lifecycle is separate) ---
	runCmd = append(runCmd, o.forwardHostPortsArgs(rt, in.cname, forwardHostPorts)...)

	// --- host services sockets dir + broker relay env ---
	runCmd = append(runCmd, o.hostServicesMountArgs(rt, in.cname)...)

	// --- device passthrough ---
	runCmd = append(runCmd, o.deviceArgs(cfg)...)

	// --- GPU warn + memlock + vendor-specific flags ---
	if gpuRequested && !gpuEnabled {
		out.print("[yellow]Warning: GPU requested but " + gpuUnavailableReason + " — " +
			"starting without GPU passthrough[/yellow]")
	}
	runCmd = append(runCmd, o.gpuArgs(cfg, rt, gpuEnabled, gpuVendor)...)

	// --- KVM ---
	runCmd = append(runCmd, o.kvmArgs(cfg, rt, slices.Contains(runCmd, "keep-groups"))...)

	// --- resources ---
	runCmd = append(runCmd, o.resourceArgs(cfg, rt)...)

	// --- host nvim config ---
	hostNvim := filepath.Join(homeDir(), ".config", "nvim")
	if isDir(hostNvim) {
		runCmd = append(runCmd, "-v", hostNvim+":/ctx/host-nvim-config:ro")
	}

	// --- shadow .vscode/mcp.json + .overmind.sock ---
	if fileExists(filepath.Join(o.Workspace, ".vscode", "mcp.json")) {
		runCmd = append(runCmd, "-v", "/dev/null:/workspace/.vscode/mcp.json:ro")
	}
	if fileExists(filepath.Join(o.Workspace, ".overmind.sock")) {
		runCmd = append(runCmd, "-v", "/dev/null:/workspace/.overmind.sock:ro")
	}

	// --- workspace-readonly overlays ---
	runCmd = append(runCmd, o.workspaceReadonlyMountArgs(cfg, rt)...)

	// --- per-side venv shadows ---
	runCmd = append(runCmd, o.venvShadowMountArgs(cfg, in.wsState)...)

	// --- user config mount (nested jails) ---
	runCmd = append(runCmd, o.userConfigMountArgs(rt, in.wsState, in.mountTargets)...)

	// --- MISE_DISABLE_TOOLS env ---
	userEnv := config.ResolveEnvSources(o.Workspace, cfg, nil)
	miseDisabled := config.MergeMiseDisabledTools(mapGet(userEnv, "MISE_DISABLE_TOOLS"))
	runCmd = append(runCmd, "-e", "MISE_DISABLE_TOOLS="+miseDisabled)

	// --- store-prune gate (host-only) --- handled by the lifecycle phase
	// (needs live-container enumeration + relay reaping); the -e is inserted
	// there. Placeholder here keeps argv order: it is appended before skills.
	runCmd = append(runCmd, in.storePruneEnv()...)

	// --- skills mounts (selected agents with a skills dir) ---
	// PACK-DECLARED skills mounts. The SOURCE is the per-pack staging dir, not the
	// pack's own tree, because PrepareSkills merges three sources into it (built-ins <
	// pack skills < the user's own host skills) and that merge has to land somewhere.
	// Core reads the destination off the pack's declaration and mounts it; it does not
	// know the content is "an agent's skills".
	for _, target := range packSkillTargets(in.packs) {
		runCmd = append(runCmd, "-v",
			filepath.Join(in.agentsPath, target.Staging)+":/home/agent/"+target.Dest+":ro")
	}

	// --- PACK MANIFESTS, read-only at /ctx/packs ---
	// The entrypoint renders each pack's declared SURFACES in-jail, so it needs the
	// same declarations the host just read. Mounting the staged tree is how they cross,
	// rather than an env var carrying serialized JSON: the tree is already staged (the
	// exec-bit and symlink-escape refusals in packstage have run on it), and a surface
	// may name a Lua transform FILE that has to exist at a path in-jail.
	//
	// :ro, and that is load-bearing rather than tidiness — a pack manifest is an INPUT
	// to composition, and an agent that could rewrite one in-jail could grant its own
	// pack a host file on the next boot.
	if in.packStaging != "" {
		if rt == "container" {
			// Apple Container can't nest this under the ws_state mount; the staged tree
			// is read straight from the host path instead (the AC host filesystem is
			// visible), so there is nothing to emit.
			runCmd = append(runCmd, "-e", "YOLO_PACK_ROOT="+in.packStaging)
		} else {
			runCmd = append(runCmd, "-v", in.packStaging+":"+packCtxDir+":ro",
				"-e", "YOLO_PACK_ROOT="+packCtxDir)
		}
	}

	// --- host files (pack-declared, origin-gated) ---
	runCmd = append(runCmd, o.hostFileArgs(in)...)

	// --- user host_files: :ro source mounts, writable destinations, wire env ---
	// Order within the group is fixed: the destination's writable subtree must be
	// declared alongside the other home binds (podman sorts by destination depth,
	// so adjacency is cosmetic, but a deterministic argv is not), then the :ro
	// source inputs under /ctx, then the resolved-entry env the entrypoint decodes.
	runCmd = append(runCmd, o.hostFileWritableDirArgs(in)...)
	runCmd = append(runCmd, o.hostUserFileArgs(in)...)
	runCmd = append(runCmd, o.hostFilesEnv(in)...)

	// --- PACK-DECLARED briefings ---
	// Same Apple-Container single-file-mount limitation as yolo-user-env.sh: AC
	// materializes the staged briefing into ws_state. Skipping the container branch
	// silently dropped every briefing on that backend.
	//
	// The staging filename must match what refreshJailBriefings wrote
	// (briefingStagingName), or the mount points at a file that does not exist and the
	// jail comes up with no briefing at all — silently, since a missing bind source for
	// a FILE is not an error the way a missing dir is.
	for _, p := range in.packs {
		for _, c := range p.Decl.Contributions() {
			if c.Kind != packdecl.KindBriefing {
				continue
			}
			staged := filepath.Join(in.agentsPath, briefingStagingName(p.Name))
			if rt == "container" {
				acMaterialize(staged, c.Into, in.wsState)
			} else {
				runCmd = append(runCmd, "-v", staged+":/home/agent/"+c.Into+":ro")
			}
		}
	}

	// --- TERM + profile ---
	if term := o.Getenv("TERM"); term != "" {
		runCmd = append(runCmd, "-e", "TERM="+term)
	}
	if o.Profile {
		runCmd = append(runCmd, "-e", "YOLO_PROFILE=1")
	}

	// --- host-side loopholes runtime args (--add-host, CA mounts, env) ---
	runCmd = append(runCmd, o.loopholesRuntimeArgs(cfg, rt)...)

	// --- image + entrypoint ---
	runCmd = append(runCmd, jailImageRef(rt), "yolo-entrypoint")
	return runCmd
}

// commonEnvBlock builds the big -e env block. Frozen contract (order and
// content must not drift — yolo-entrypoint reads these exact vars).
func (o *Options) commonEnvBlock(in *assembleInput, blockedConfigJSON, netMode string) []string {
	cfg := in.cfg
	env := []string{
		"-e", "JAIL_HOME=/home/agent",
		"-e", "NPM_CONFIG_PREFIX=/home/agent/.npm-global",
		"-e", "NPM_CONFIG_CACHE=/home/agent/.cache/npm",
		"-e", "GOPATH=/home/agent/go",
		"-e", "MISE_DATA_DIR=/mise",
		"-e", "MISE_CACHE_DIR=/tmp/mise-cache",
		"-e", "MISE_PYTHON_PRECOMPILED_FLAVOR=install_only",
		"-e", "MISE_PYTHON_GITHUB_ATTESTATIONS=false",
		"-e", "MISE_TRUSTED_CONFIG_PATHS=/workspace",
		"-e", "MISE_ENV=jail",
		"-e", "RUSTUP_HOME=/mise/rustup",
		"-e", "CARGO_HOME=/mise/cargo",
		"-e", "MISE_YES=1",
		"-e", "COPILOT_ALLOW_ALL=true",
		"-e", "IS_SANDBOX=1",
		// Retained deliberately (not redundant cleanup): this mirrors the value
		// baked into the OCI image's config.Env (flake.nix), but re-asserting it
		// on -e makes the launch env self-describing and independent of whichever
		// image tag podman resolves — a `yolo run` that (mis)loads an image
		// without the baked env still gets a correct LD_LIBRARY_PATH. It is the
		// dlopen-by-soname discovery path for nix-built processes (which never
		// traverse /lib64 and so are unreachable by nix-ld); nix-ld handles the
		// FHS-binary case. See docs/design/mise-node-dynamic-linking.md step 6/7.
		"-e", "LD_LIBRARY_PATH=/lib:/usr/lib:/usr/lib/" + storage.LinuxMultilib(),
		"-e", "HOME=/home/agent",
		"-e", "EDITOR=cat",
		"-e", "VISUAL=nvim",
		"-e", "PI_TELEMETRY=0",
		"-e", "PAGER=cat",
		"-e", "GIT_PAGER=cat",
		"-e", "YOLO_BLOCK_CONFIG=" + blockedConfigJSON,
	}
	if in.hostTZ != "" {
		env = append(env, "-e", "TZ="+in.hostTZ)
	}
	env = append(env,
		"-e", "YOLO_HOST_DIR="+o.Workspace,
		"-e", "YOLO_VERSION="+in.yoloVersion,
		"-e", "OVERMIND_SOCKET=/tmp/overmind.sock",
		"-e", "YOLO_MISE_TOOLS="+jsonDumps(config.MergeMiseTools(cfg)),
		"-e", "YOLO_LSP_SERVERS="+jsonDumpsOrEmptyObj(cfgMap(cfg, "lsp_servers")),
		"-e", "YOLO_LSP_NPM_INSTALL="+in.lspNPM(),
		"-e", "YOLO_LSP_GO_INSTALL="+in.lspGo(),
		"-e", "YOLO_MCP_SERVERS="+jsonDumpsOrEmptyObj(cfgMap(cfg, "mcp_servers")),
		"-e", "YOLO_MCP_PRESETS="+jsonDumpsOrEmptyList(cfgList(cfg, "mcp_presets")),
		"-e", "YOLO_RUNTIME=podman",
	)
	// No YOLO_REPO_ROOT: the in-jail CLI resolves its repo root the same way the
	// host does — exe-relative to the baked /opt/yolo-jail bundle, or the
	// live-mounted /workspace checkout when self-hosting (internal/reporoot).
	// There is no jail-special env override any more.
	_ = netMode
	return env
}

func jailImageRef(rt string) string {
	if rt == "container" {
		return paths.JailImageShort
	}
	return paths.JailImage
}

// jsonDumps renders v as compact JSON.
func jsonDumps(v any) string {
	s, _ := jsonx.DumpsCompact(v)
	return s
}

func jsonDumpsOrEmptyObj(m *jsonx.OrderedMap) string {
	if m == nil {
		return "{}"
	}
	return jsonDumps(m)
}

func jsonDumpsOrEmptyList(l []any) string {
	if l == nil {
		return "[]"
	}
	return jsonDumps(l)
}

func jsonDumpsStrings(ss []string) string {
	arr := make([]any, len(ss))
	for i, s := range ss {
		arr[i] = s
	}
	return jsonDumps(arr)
}

// asAnyList coerces a decoded value to []any (nil when absent/non-list).
func asAnyList(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return nil
}

// pyStrCoerce renders a config port entry (int/str) as a string. Ints render
// without ".0"; strings verbatim.
func pyStrCoerce(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	}
	if lit, ok := jsonx.AsIntLiteral(v); ok {
		return lit
	}
	if f, ok := v.(float64); ok {
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	s, _ := jsonx.DumpsCompact(v)
	return s
}

// splitMountSpec runs the "host:container" split: the LAST colon that precedes
// an absolute container path (starts with /). Plain host-only paths get
// /ctx/<resolved-name>.
func splitMountSpec(mount string) (hostPath, containerPath string) {
	idx := strings.LastIndex(mount, ":")
	if idx > 0 && idx+1 < len(mount) && mount[idx+1] == '/' {
		return mount[:idx], mount[idx+1:]
	}
	resolved := resolveExpand(mount)
	return mount, "/ctx/" + filepath.Base(resolved)
}

func resolveExpand(p string) string {
	return resolvePath(expandUser(p))
}

// insertAt inserts v at index i.
func insertAt(s []string, i int, v string) []string {
	out := make([]string, 0, len(s)+1)
	out = append(out, s[:i]...)
	out = append(out, v)
	out = append(out, s[i:]...)
	return out
}
