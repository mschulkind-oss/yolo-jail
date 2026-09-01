package run

import (
	"path/filepath"
	"slices"
	"sort"
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
	packs []*packload.Pack
	// imageRef is the ref of the image AutoLoadImage actually made ready this
	// launch — content-addressed (`localhost/yolo-jail:<sha16>`) on the normal
	// path, the legacy :latest tag on a degraded fallback with no store path.
	//
	// It is INPUT, not something assembly derives. Before C2 both this position
	// in the argv and runNormal's host-service insert point called a
	// jailImageRef(rt) helper that returned a constant, and they agreed only
	// because the constant was the same in both. With one image per config there
	// is no constant left to agree on, so the ref is threaded from the one place
	// that knows it and read twice from this single field. See jailImage() for
	// what an unset field does, and why it does not fall back.
	imageRef   string
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

	// userEnv is the env_sources the run pipeline hydrated — the same resolution that
	// wrote yolo-user-env.sh, handed to assembly rather than read a second time. It is
	// the secret channel inside the composed channel (a provider env_shape's {key}
	// placeholder resolves through it, and so does the credential pre-flight); assembly
	// reads it only through envChannel, and only when the pipeline did not hand a channel
	// over — every construction that does not come from the run pipeline is a test. Nil
	// there means nothing was hydrated, and the lookup then falls back to the CLI's own
	// environment.
	userEnv *jsonx.OrderedMap

	// acCtxMaterialized is set by EITHER host-file emitter when it copies a grant into
	// the Apple Container ctx dir. The YOLO_CTX_ROOT that tells the entrypoint where to
	// look is then emitted ONCE, below — two emitters each appending their own would put
	// the same -e on the argv twice, which is at best noise on a frozen argv and at worst
	// a backend that rejects a duplicate flag.
	acCtxMaterialized bool

	// channel is the profile/provider environment the run pipeline composed above the
	// backend dispatch (profilechannel.go). The env block below EMITS it; it must not
	// re-derive any part of it, or the argv and whatever the macos-user arm delivered
	// from the same launch could disagree. Nil only on a hand-built assembleInput that
	// never came from the run pipeline — every one is a test — which envChannel then
	// composes from these same fields through the one composer.
	channel *packChannel
}

// envChannel returns this input's composed channel, composing it from the input's own
// fields when the pipeline did not hand one over. There is one composer, so the fallback
// cannot produce a different channel — it exists so a test that assembles an argv by hand
// still exercises the same fold, not as a second production path.
func (in *assembleInput) envChannel(o *Options) *packChannel {
	if in.channel != nil {
		return in.channel
	}
	return o.composePackChannel(in.cfg, in.packs, in.userEnv)
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
// callers pass a prepared ws_state — and EXCEPT for the host probes it runs
// through the o.Exec / o.LookPath / o.PathExists seams: git identity, the GPU
// availability probe, and (since the host-loopback fix) `podman info` plus
// `<rootless-network-stack> --help`. Those are why every caller in the test suite
// goes through goldenOptions: a test that leaves the seams at their fillDefaults
// values shells out to the host and gets an argv that depends on which machine ran
// it — which is how TestAssembleRunCmdPodmanLinuxGolden once started failing on the
// macOS runner.
// The final internal command and the host-service -e insertion are handled by the
// lifecycle phase.
// The argv this returns ends at the image ref + "yolo-entrypoint"; the
// final_internal_cmd is appended after inserting host-service env at
// index(image); see runContainer for that tail.
func (o *Options) assembleRunCmd(in *assembleInput) []string {
	cfg := in.cfg
	rt := in.rt
	out := o.pr(o.Stdout)

	// --- Network mode + ports ---
	//
	// Through resolveNetMode rather than an inline copy of it: the loophole runtime
	// resolves the mode the same way to decide what each host daemon PUBLISHES
	// (advertiseHostFor), and the two now feed one shared predicate
	// (sharesLauncherNetns) whose whole value is that they cannot disagree.
	netMode := o.resolveNetMode(cfg)

	// --- nested-container detection ---
	// o.inContainer() rather than a second copy of the same two probes, for the
	// reason given at netMode above: this answer and the loophole runtime's have to
	// be one answer, not two that happen to match today.
	inContainer := o.inContainer()

	// THE MODE THIS LAUNCH RUNS UNDER, computed HERE — above the port gate — rather
	// than beside the `--net=` selector it also spells (see the network-mode-flag
	// section below, which is its other reader).
	//
	// The port keys used to answer to the CONFIGURED mode while the selector answered
	// to the applied one, and a nested launch is exactly where those two differ:
	// podman-in-podman is forced onto the launcher's namespace, so `network.mode:
	// "bridge"` (the default) emitted --net=host AND every -p, and the publish path
	// then appends the DNAT sysctl the host namespace refuses outright:
	//
	//	Error: ... sysctl net.ipv4.conf.all.route_localnet=1 can't be set since
	//	Network Namespace set to host
	//
	// — a `podman create` failure, so a nested jail declaring ANY port could not start
	// at all. That is this repo's own dev loop (AGENTS.md, "Nested-jail verification").
	applied := appliedNetMode(rt, netMode, inContainer)

	var publishArgs []string
	var forwardHostPorts []any
	if netSec := cfgMap(cfg, "network"); netSec != nil {
		declaredPorts := asAnyList(mapGet(netSec, "ports"))
		declaredForwards := asAnyList(mapGet(netSec, "forward_host_ports"))
		if applied == "bridge" {
			for _, p := range declaredPorts {
				publishArgs = append(publishArgs, "-p", pyStrCoerce(p))
			}
			forwardHostPorts = declaredForwards
		} else if netMode == "bridge" && (len(declaredPorts) > 0 || len(declaredForwards) > 0) {
			// ONLY THE FORCED DROP WARNS, and only when there is something to drop. An
			// explicit `network.mode: "host"` drops the same two keys and always has —
			// that is the user's own declaration, and on Apple Container (where the mode
			// is not honored at all) the branch further down is where it gets its line.
			// What has no declaration anywhere is NESTING: the config says bridge, the
			// launch applies host, and the ports vanish for a reason nothing the user
			// wrote can account for. Conditional on the declaration for the reason
			// backend-parity.md OQ-BP-3 gives: a launch line that fires only when the user
			// declared the thing is the kind a reader does not learn to skip.
			var dropped []string
			if len(declaredPorts) > 0 {
				dropped = append(dropped, "network.ports")
			}
			if len(declaredForwards) > 0 {
				dropped = append(dropped, "network.forward_host_ports")
			}
			out.print("[yellow]Warning: nested launch — podman-in-podman is forced onto this " +
				"container's network namespace (--net=host; netavark cannot create one without " +
				"NET_ADMIN), so " + strings.Join(dropped, " and ") + " " +
				"are NOT applied[/yellow] — the jail shares this process's network stack, so a " +
				"port it binds is already reachable on the same localhost, with no forwarding hop " +
				"to publish. Launch from the host for published ports.")
		}
	}

	normalizedBlocked := config.NormalizeBlockedTools(cfgMap(cfg, "security"))
	blockedConfigJSON := jsonDumps(normalizedBlocked)

	// --- Extra mounts (config.mounts → -v host:container:ro) ---
	var mountArgs []string
	ctxMountsUnsafe := roBindsUnsupported(rt)
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
		if ctxMountsUnsafe != "" {
			out.print("[yellow]Skipping mount " + hostPath + " → " + containerPath + ": " +
				ctxMountsUnsafe + "[/yellow]")
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
		// credential survives across workspaces — see packload.SharedDirs for why that
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
	// identically inside and outside the jail. Self-hosting is no exception since
	// the cwd-walk was removed: an in-jail agent that wants the live /workspace
	// checkout instead sets YOLO_REPO_ROOT itself. Nothing to bind, and no
	// YOLO_REPO_ROOT for the launcher to guess at.
	runCmd = append(runCmd, "--workdir", "/workspace")

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
	//
	// The default `bridge` mode still emits no --net flag: it means "let podman
	// decide", and it stays that way. What it no longer means is "say nothing at
	// all about networking" — hostLoopbackFactsFor asks podman which rootless
	// stack it will use and, on the stacks yolo can positively identify, adds the
	// option that forwards the host's LOOPBACK into the jail. Without it, pasta
	// (podman's default since 5.0) forwards host.containers.internal to the
	// host's global address and every loopback-TLS service is unreachable from
	// every jail — see hostloopback.go and
	// docs/design/loopback-tls-reachability.md §6. Every failure path there emits
	// nothing, so the worst case is exactly the behaviour above.
	//
	// Both emitting branches below spell the selector as `--net=` + appliedNetMode, so
	// the argv IS the predicate's answer rather than a second computation of it. That is
	// what the briefing reads (backend-parity.md §6): a jail forced onto host networking
	// by nesting used to be told it was bridged, because the forcing lived here as an
	// inline `rt == "podman" && inContainer` and nothing else could see it.
	//
	// `applied` is computed at the top of this function, beside the port gate that is
	// its other reader — the same answer, once, for both.
	var hostLoopback hostLoopbackPlan
	if rt == "container" {
		// Apple Container handles networking internally, so an explicit
		// `network.mode: "host"` does nothing here and used to say nothing either. No
		// --net is emitted (this branch), so there is no host networking. The agent was
		// also told "localhost resolves directly to the host" on top of that, by a
		// briefing composed from the config rather than from what was applied — it now
		// reads appliedNetMode, which answers "bridge" here for the reason this branch
		// emits no selector.
		//
		// The port keys follow that same answer now (the gate at the top of this
		// function reads `applied`, not the config), so an explicit host mode no longer
		// silently drops them on this backend: what AC applies is its own bridged
		// networking, and -p works there whatever the unhonored key says.
		//
		// Only an EXPLICIT host is warned: bridge is genuinely honored on this backend
		// (-p is emitted ungated, forward_host_ports goes through --publish-socket, and
		// AC gives each container its own vmnet netns), so warning on the default would
		// be noise on every launch.
		if netMode == "host" {
			out.print("[yellow]Warning: network.mode \"host\" is NOT honored on Apple Container[/yellow] — " +
				"the backend manages networking itself, so the jail does not share your host's " +
				"network stack (published ports and forward_host_ports still apply: this backend " +
				"runs bridged either way). Remove the key, or use YOLO_RUNTIME=podman for host " +
				"networking.")
		}
	} else if rt == "podman" && inContainer {
		// Podman-in-podman: netavark cannot create a netns without NET_ADMIN, so the
		// applied mode is host whatever the config asked for. This is also the one mode
		// in which the reachability bug CANNOT reproduce (the jail shares the launcher's
		// stack, so the two loopbacks are one), which is why a nested jail is no evidence
		// about the branch below — §7 of the design doc, and the carve-out in AGENTS.md.
		runCmd = append(runCmd, "--net="+applied)
	} else {
		if applied != "bridge" {
			runCmd = append(runCmd, "--net="+applied)
		}
		hostLoopback = decideHostLoopback(o.hostLoopbackFactsFor(rt, netMode))
		runCmd = append(runCmd, hostLoopback.args...)
		if hostLoopback.warning != "" {
			out.print(hostLoopback.warning)
		}
	}

	// What the jail is TOLD about all of that, rendered once for every shape a launch
	// can take — including the two branches above that never reach the decision at
	// all. The in-jail witness cannot recover any of it for itself: an unreachable
	// service looks identical whether yolo could not ask this host to forward
	// loopback (a known limitation), asked and was ignored (a fault), never reached a
	// conclusion, or had nothing to ask for because the jail shares this process's
	// namespace. Only some of those may ever fail a launch — OQ-R2 as scoped by
	// OQ-R3 and OQ-R5 — and none of that survives into the container by itself.
	//
	// THE SHARED CASE IS DECIDED HERE, NOT IN hostloopback.go, because the shapes
	// that have it never reach that file and must not: podman-in-podman is the
	// branch above (podman refuses a container carrying two network selectors, and
	// this is also the repo's own dev loop), and `network.mode: host` returns from
	// the decision before it looks at anything. A disposition computed inside a
	// function that did not run is a value that never arrives — measured exactly
	// that way in this repo on 2026-08-18, where the variable stayed absent in the
	// jail the patch was written for.
	//
	// It reads the SAME predicate the loophole runtime uses to choose each daemon's
	// advertised address, so the severity the witness applies and the address it
	// dials cannot disagree. It cannot mask a `requested` either: that value is only
	// ever produced on the default bridge mode, which is disjoint from every shape
	// sharesLauncherNetns accepts.
	disposition := hostLoopback.disposition
	if sharesLauncherNetns(rt, netMode, inContainer) {
		disposition = paths.HostLoopbackShared
	}
	runCmd = append(runCmd, jailLoopbackEnvArgs(disposition)...)

	// The in-jail witness's escape hatch, forwarded from the host environment where
	// the user types it. Outside the branch above on purpose: the witness runs
	// under every runtime and every network mode, so the way past it must too.
	runCmd = append(runCmd, o.reachabilityOptOutArgs()...)

	// --- git identity + global gitignore (host-composed, :ro-mounted) ---
	runCmd = append(runCmd, o.gitIdentityMountArgs(rt, in.wsState, in.mountTargets)...)

	// --- publish + extra mounts ---
	runCmd = append(runCmd, publishArgs...)
	runCmd = append(runCmd, mountArgs...)

	// --- published-port DNAT sysctl + env ---
	// `applied == "bridge"` is spelled again here rather than left implied by a
	// non-empty publishArgs, because THIS is the flag the kernel refuses: podman
	// rejects the sysctl outright under host networking ("can't be set since Network
	// Namespace set to host") and the container never gets created. The publish gate
	// above already withholds every -p in that case, so the condition is redundant
	// today — deliberately, so that relaxing either half alone cannot resurrect the
	// pairing that failed the launch.
	if len(publishArgs) > 0 && rt == "podman" && applied == "bridge" {
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

	// --- host services sockets dir + broker endpoint env ---
	runCmd = append(runCmd, o.hostServicesMountArgs(rt, in.cname, cfg)...)

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
	// Read once at boot (entrypoint copies /ctx/host-nvim-config into the jail's
	// ~/.config/nvim) — but the mount stays for the whole session, so on a backend that
	// ignores :ro it is a live write channel into the user's real editor config. Refuse
	// rather than downgrade, and say so: the visible symptom is nvim coming up
	// unconfigured, which is otherwise an odd thing to have to explain to yourself.
	hostNvim := filepath.Join(homeDir(), ".config", "nvim")
	if isDir(hostNvim) {
		if reason := roBindsUnsupported(rt); reason != "" {
			out.print("[yellow]Skipping host nvim config (~/.config/nvim): " + reason + "[/yellow]")
		} else {
			runCmd = append(runCmd, "-v", hostNvim+":/ctx/host-nvim-config:ro")
		}
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
	// (needs live-container enumeration); the -e is inserted there. Placeholder
	// here keeps argv order: it is appended before skills.
	runCmd = append(runCmd, in.storePruneEnv()...)

	// --- skills mounts (selected agents with a skills dir) ---
	// PACK-DECLARED skills mounts. The SOURCE is the per-pack staging dir, not the
	// pack's own tree, because PrepareSkills merges three sources into it (built-ins <
	// pack skills < the user's own host skills) and that merge has to land somewhere.
	// Core reads the destination off the pack's declaration and mounts it; it does not
	// know the content is "an agent's skills".
	//
	// DEDUP BY DESTINATION, for the same reason briefings do below: `skills` is
	// CombineMerge — several packs into one dir IS the feature — and PrepareSkills has
	// already merged EVERY pack's skills into each staging dir (built-ins < all packs <
	// the user's own). So a second mount at one destination carries the same merged
	// content, and podman rejects it with "duplicate mount destination", failing the boot.
	//
	// This was pack-system.md §14's known sharp edge, worked around by telling authors not
	// to declare a `skills` contribution whose `into` duplicates another pack's. That
	// advice was unfollowable in the configuration it most matters for: an agent pack
	// naming ~/.claude/skills plus a user pack sharing a skills corpus is the whole point
	// of the kind. Fixed rather than documented (plan OQ-C).
	seenSkillDest := map[string]bool{}
	for _, target := range packSkillTargets(in.packs) {
		if seenSkillDest[target.Dest] {
			continue
		}
		seenSkillDest[target.Dest] = true
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

	// --- pack `mount` contributions: host-home dir/file :ro under /ctx ---
	runCmd = append(runCmd, o.hostMountArgs(in)...)

	// --- pack `env` contributions: static jail env vars ---
	// Static values only (the env kind forbids interpolation/host reads), so they go
	// straight onto the command as -e. A key two packs both set is last-writer-wins
	// here; the footprint's per-key env claims are what surface such a collision.
	// Sorted for a deterministic argv.
	//
	// THE SAME merge the launch line describes: the CLI-keyed profile table is folded in
	// (EnvVarsFor, not a static-only fold) so a selected variant later-wins over the
	// pack's own static value (OQ-8), and a null in it removes the key — the jail starts
	// from an
	// empty env, so "unset" here is simply an absent -e. Read off the composed channel
	// rather than re-folded here: Run composed it above the backend dispatch, and the
	// macos-user arm delivers that same fold to its own plan env.
	if packEnv := in.envChannel(o).packEnv; len(packEnv) > 0 {
		keys := make([]string, 0, len(packEnv))
		for k := range packEnv {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			runCmd = append(runCmd, "-e", k+"="+packEnv[k])
		}
	}

	// --- user host_files: :ro source mounts, writable destinations, wire env ---
	// Order within the group is fixed: the destination's writable subtree must be
	// declared alongside the other home binds (podman sorts by destination depth,
	// so adjacency is cosmetic, but a deterministic argv is not), then the :ro
	// source inputs under /ctx, then the resolved-entry env the entrypoint decodes.
	runCmd = append(runCmd, o.hostFileWritableDirArgs(in)...)
	runCmd = append(runCmd, o.hostUserFileArgs(in)...)
	// ONE YOLO_CTX_ROOT for both host-file emitters above (see acCtxMaterialized).
	if in.acCtxMaterialized {
		runCmd = append(runCmd, "-e", "YOLO_CTX_ROOT=/home/agent/"+acCtxDirRel)
	}
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
	seenBriefingDest := map[string]bool{}
	for _, p := range in.packs {
		for _, c := range p.Decl.Contributions() {
			if c.Kind != packdecl.KindBriefing {
				continue
			}
			// DEDUP BY DESTINATION. `briefing` is CombineConcat — several packs contributing
			// prose at one path is the designed behavior, and refreshJailBriefings has
			// already merged every pack's prose into the composed content each staging file
			// holds. So the SECOND mount at a destination is not a second briefing, it is
			// the same content again — and podman rejects it with "duplicate mount
			// destination", killing the boot.
			//
			// That made a legitimate configuration unlaunchable: an agent pack naming
			// ~/.claude/CLAUDE.md plus a user pack contributing house rules to it is exactly
			// what briefings are for. First writer wins the mount; the content is identical
			// either way.
			if seenBriefingDest[c.Into] {
				continue
			}
			seenBriefingDest[c.Into] = true
			staged := filepath.Join(in.agentsPath, briefingStagingName(p.Name))
			if rt == "container" {
				acMaterialize(staged, c.Into, in.wsState)
			} else {
				runCmd = append(runCmd, "-v", staged+":/home/agent/"+c.Into+":ro")
			}
		}
	}

	// --- PACK-DECLARED `files` trees ---
	// An opaque tree the pack owns, bound :ro at its home-relative `into`. Emitted
	// beside skills and briefing because it is the third staged-tree kind — the one
	// that shipped inert (plan N1). See packfiles.go for the AC single-file split and
	// why the source is the STAGED tree.
	runCmd = append(runCmd, o.packFilesMountArgs(in)...)

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
	runCmd = append(runCmd, in.jailImage(), "yolo-entrypoint")
	return runCmd
}

// composedProviders is the providers table this launch carries: the user's `providers`
// config entries with every selected pack's `kind: "provider"` service facts composed
// UNDER them, per field (packload.ComposeProviders). It is the ONLY place that composition
// happens — the result crosses to the jail whole and the derives read it verbatim — so a
// user override of one model alias cannot cost the pack's endpoints, and nothing
// downstream re-derives a second copy of the merge.
func composedProviders(cfg *jsonx.OrderedMap, packs []*packload.Pack) *jsonx.OrderedMap {
	return packload.ComposeProviders(cfgMap(cfg, "providers"), packs)
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
	// Composed ONCE for the three readers below — the YOLO_PROVIDERS pair, the
	// YOLO_PACK_PROFILES pair, and the env-shape vars — all read off the channel Run
	// composed above the backend dispatch, and which the macos-user arm delivers to its
	// own plan env and bootstrap. Re-deriving any part of it here would let the argv and
	// the native arm answer differently about what the same profile delivers.
	channel := in.envChannel(o)
	effectiveProfiles := channel.profiles
	providers := channel.providers
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
		"-e", "YOLO_PROVIDERS="+jsonDumpsOrEmptyObj(providers),
		"-e", "YOLO_PACK_PROFILES="+jsonDumpsOrEmptyObj(effectiveProfiles),
		"-e", "YOLO_REQUIRED_CAPABILITIES="+jsonDumpsOrEmptyList(cfgList(cfg, "required_capabilities")),
		"-e", "YOLO_RUNTIME=podman",
	)
	// The profile-derived provider environment — the env shape a provider declares for
	// the protocol an agent speaks (OQ-14), which for bedrock is AWS_REGION and the
	// model ids — is composed by internal/agentenv, which is ALSO what
	// `yolo host -- <agent>` applies on the host. One implementation, so the two notches
	// cannot drift — that is the jail/host parity claim in host-agent-environment.md §2.2,
	// and it is not a claim two copies of this block could keep. This used to be thirty
	// lines of nested type assertions inline here, covered by no test at all.
	//
	// The shape's VALUES come from the composed table YOLO_PROVIDERS carries above, and
	// the shape ITSELF from packs/claude. What this block does not deliver is the
	// variant's own literal env (claude's CLAUDE_CODE_USE_BEDROCK) — that rode the pack
	// env block above, through the same profile table. The vars are the channel's, not
	// re-resolved here: Run composed them above the backend dispatch, and the macos-user
	// arm delivers that same list to its plan env.
	for _, v := range channel.shapeVars {
		if v.Unset {
			// Not reachable from a profile today, and deliberately not guessed at:
			// podman's `-e KEY` (no `=`) means INHERIT KEY from the host env, which
			// is the opposite of a removal. A jail starts from an empty environment
			// anyway, so "remove" is already its default state for anything we do
			// not pass; the host exec path is where Unset does real work.
			continue
		}
		env = append(env, "-e", v.Key+"="+v.Value)
	}
	// No YOLO_REPO_ROOT: the in-jail CLI resolves its repo root the same way the
	// host does — exe-relative to the baked /opt/yolo-jail bundle, or the
	// live-mounted /workspace checkout when self-hosting (internal/reporoot).
	// There is no jail-special env override any more.
	_ = netMode
	return env
}

// effectivePackProfiles merges the three profile sources, later winning: workspace/user
// config, then --pack-profile, then -p. Every key in the result is a CLI name — the bin a
// pack installs — which is what makes the table readable as "the profile each CLI runs"
// and what lets `yolo check` and the launch pre-flight validate it against one namespace.
//
// A method on Options taking the config and the pack set, rather than a method on
// assembleInput, because it has TWO consumers that must agree byte for byte: the env
// block below (the jail's copy of the table) and the launch's profile disclosure line,
// which describes the same table to the human. One merge, so neither can drift.
func (o *Options) effectivePackProfiles(cfg *jsonx.OrderedMap, packs []*packload.Pack) *jsonx.OrderedMap {
	out := jsonx.NewOrderedMap()
	if cfgProfiles, ok := cfg.Get("pack_profiles"); ok {
		if m, ok := cfgProfiles.(*jsonx.OrderedMap); ok {
			for _, k := range m.Keys() {
				v, _ := m.Get(k)
				out.Set(k, v)
			}
		}
	}
	for k, v := range o.PackProfiles {
		out.Set(k, v)
	}
	if o.ProfileName != "" {
		if len(o.Args) == 0 {
			// GLOBAL -p (§3.3, OQ-5): with no command there is no bin to key on, so the
			// name is the selected profile of EVERY selected pack. The key is the CLI
			// name each pack installs, not the pack slug — the table's keys are CLI
			// names everywhere else, and a derive reads its own bin. A pack that
			// installs nothing gets no key, because there is no CLI to select for;
			// it is still a receiver of the table, which is what the launch line says.
			for _, p := range packs {
				for _, bin := range p.InstallBins() {
					out.Set(bin, o.ProfileName)
				}
			}
		} else {
			out.Set(filepath.Base(o.Args[0]), o.ProfileName)
		}
	}
	return out
}

// unsetImageRef is what the argv gets when nobody threaded a ref in. It is
// deliberately NOT a working name.
//
// The obvious alternative — fall back to the legacy :latest constant — is the
// exact defect C2 exists to remove: assembly would quietly name a different
// image than the one the load pipeline prepared, and the launch would look fine
// while running whatever :latest happens to point at. This value cannot resolve,
// so a launch that reaches it fails immediately with a runtime error naming the
// placeholder, one line from its cause. In-package tests that do not care about
// the image tail also land here, which is why it reads as an obvious sentinel
// rather than as a plausible ref.
const unsetImageRef = "yolo-jail:<image-ref-not-threaded>"

// jailImage returns the image ref this launch must run. There is no per-runtime
// branch left: image.JailImageRef already spells the ref the way the runtime
// wants it (Apple Container drops the localhost/ prefix), and it did so at the
// point where the store path was known.
func (in *assembleInput) jailImage() string {
	if in.imageRef == "" {
		return unsetImageRef
	}
	return in.imageRef
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
