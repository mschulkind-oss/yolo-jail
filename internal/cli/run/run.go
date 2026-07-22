package run

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/agents"
	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/storage"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// Run validates config, resolves the runtime, then either execs into
// an existing container or launches a fresh one. Returns the process exit code.
// The whole flow is driven off the
// injected seams so the probe + argv-assembly paths are unit-testable.
func Run(opts Options) int {
	fillDefaults(&opts)
	o := &opts

	// --- Phase 1: probes (repo root, storage, config, runtime) ---
	// D2 (graceful launch degradation): repo-root resolution is no longer a hard
	// gate. When it fails, repoRoot is "" and the launch proceeds DEGRADED — no
	// nix build, no /opt/yolo-jail source bind, no YOLO_REPO_ROOT — running
	// whatever image is already loaded or cached. The degraded consumers
	// (autoLoadImage's SkipBuild, the assembler's repoBound gate) each handle the
	// empty repoRoot; only a truly imageless host still fails, with an actionable
	// message. macos-user with empty `packages:` never needs a repo at all.
	repoRoot, _ := o.RepoRoot()
	if err := ensureStorage(); err != nil {
		o.pr(o.Stdout).printf("[bold red]%s[/bold red]", err.Error())
		return 1
	}
	cfg, ok := o.loadAndValidateConfig()
	if !ok {
		return 1
	}
	rt, ok := o.resolveRuntime(cfg)
	if !ok {
		return 1
	}

	// macos-user native branch: route to the injected handler,
	// which wires internal/macosuser (SBPL sandbox, dscl provisioning, the
	// sandbox-exec launch) + the darwinpkg streaming-build materialize adapter.
	// Falls back to an actionable error if the front door didn't inject it.
	if rt == "macos-user" {
		if o.MacosUserRun == nil {
			o.pr(o.Stdout).print(
				"[bold red]macos-user runtime handler not wired.[/bold red]  " +
					"This build cannot launch the native macOS backend.")
			return 1
		}
		// (a bare `yolo` opens an interactive login zsh in the sandbox).
		agentArgv := o.Args
		if len(agentArgv) == 0 {
			agentArgv = []string{"/bin/zsh", "-l"}
		}
		return o.MacosUserRun(cfg, o.Workspace, config.SelectedAgents(cfg), agentArgv, repoRoot, o.DryRun)
	}
	if o.DryRun {
		o.pr(o.Stdout).print(
			"[bold red]--dry-run is only supported for the macos-user runtime.[/bold red]  " +
				`Set runtime: "macos-user" (or YOLO_RUNTIME=macos-user) to use it.`)
		return 1
	}
	// D2: warn once when launching degraded (no source tree). autoLoadImage then
	// runs on a cached/loaded image; if none exists it fails with an actionable
	// message. This is a notice, not an error — the launch continues.
	if repoRoot == "" {
		o.pr(o.Stderr).print("[yellow]No yolo-jail source tree found — launching on the " +
			"cached image (no rebuild). Set `repo_path` in ~/.config/yolo-jail/config.jsonc " +
			"to enable image rebuilds.[/yellow]")
	}
	return o.runContainer(cfg, rt, repoRoot)
}

// ensureStorage wraps storage.EnsureGlobalStorage, wiring the v2 layout
// migration (audit 2026-07-18 §B#2: passing nil left the dangling-mise-symlink
// heal + layout-version stamp as dead code that never ran under the gate).
// canReclaim returns false — the conservative fail-safe (DEFER the heal
// when it can't confirm no live jail holds the store, leaving the marker
// unstamped to retry); the full live-container probe is the run-slice's concern,
// and declining is always safe. insideJail short-circuits (never scans /mise).
func ensureStorage() error {
	return storage.EnsureGlobalStorage(func() {
		insideJail := os.Getenv("YOLO_VERSION") != ""
		storage.MigrateStorageLayout(insideJail, func() bool { return false }, func(msg string) {
			fmt.Fprintln(os.Stderr, msg)
		})
	})
}

// runContainer is the post-config flow: the attach-to-existing decision
// (with orphan reaping), then the fresh-launch path (config-change approval,
// workspace flock + raced re-check, stale-container removal, image load, argv
// assembly, host-service start, tracking/owner-PID, port forwarding, the
// run_with_proxy launch with the FROZEN teardown guard stack).
func (o *Options) runContainer(cfg *jsonx.OrderedMap, rt, repoRoot string) int {
	out := o.pr(o.Stdout)

	agentsList := config.SelectedAgents(cfg)
	agentSpecs := agents.ResolveAgents(agentsList)

	// Command construction (needed for both exec and run paths).
	fullCommand := append([]string{}, o.Args...)
	targetCmd := "bash"
	if len(fullCommand) > 0 {
		fullCommand = agents.InjectYoloFlags(fullCommand)
		targetCmd = shquoteJoin(fullCommand)
	}

	identityEnv := o.collectIdentityEnv()

	cname := runtime.FromWorkspace(o.Workspace)

	// Sweep jails orphaned by an uncatchable kill before the attach decision.
	o.reapOrphanedJails(rt)

	existingCID := ""
	if !o.New {
		existingCID = o.findRunningContainer(cname, rt)
	}

	// Refresh the per-jail skills + AGENTS/CLAUDE staging on every invocation.
	agentsPath, err := o.refreshJailBriefings(cname, cfg, rt)
	if err != nil {
		out.printf("[bold red]%s[/bold red]", err.Error())
		return 1
	}

	if existingCID != "" {
		return o.attachExisting(cname, rt, targetCmd, identityEnv, false)
	}

	// --- Fresh launch: config-change approval ---
	if !o.checkConfigChanges(cfg) {
		return 1
	}

	// --- Workspace flock (blocking) ---
	lockDir := filepath.Join(paths.GlobalStorage(), "locks")
	_ = os.MkdirAll(lockDir, 0o755)
	lock, lerr := acquireWorkspaceLock(filepath.Join(lockDir, cname+".lock"),
		func(msg string) { out.printf("[dim]Warning: %s[/dim]", msg) })
	if lerr != nil {
		out.printf("[bold red]%s[/bold red]", lerr.Error())
		return 1
	}

	// Re-check after acquiring the lock — another process may have won.
	if !o.New {
		if raced := o.findRunningContainer(cname, rt); raced != "" {
			lock.Close()
			return o.attachExisting(cname, rt, targetCmd, identityEnv, true)
		}
	}

	// Remove any stopped container left from an unclean shutdown.
	if stale := o.findExistingContainer(cname, rt); stale != "" {
		o.pr(o.Stderr).printf("Removing stale container %s...", cname)
		o.removeStaleContainer(cname, rt)
	}

	// Retire jail-made workspace venvs from the old shared-store model.
	o.retireJailMadeVenv(cfg)

	profileStart := o.Now()

	// Image build/load.
	if !o.autoLoadImage(cfg, rt, repoRoot) {
		lock.Close()
		return 1
	}

	// ws_state overlay prep.
	wsState := o.prepareWsState(cfg, agentSpecs, agentsList)

	// yolo-user-env.sh (frozen writer).
	userEnv := config.ResolveEnvSources(o.Workspace, cfg, func(msg string) { out.print(msg) })
	writeUserEnvFile(filepath.Join(wsState, "yolo-user-env.sh"), userEnv)

	// Broker singleton + relay: ensure BEFORE building the argv (the sockets-dir
	// mount + broker env are emitted by the assembler when the socket exists).
	socketsDir := hostServiceSocketsDir(cname, o.IsMacOS)
	if rt != "container" {
		_ = os.MkdirAll(socketsDir, 0o755)
		o.brokerEnsure()
		if o.PathExists(broker.BrokerSingletonSocket) {
			o.ensureBrokerRelay(cname, rt)
		}
	}

	// Store-prune gate + orphan-relay reap (host-only; never from inside a jail
	// — an inner CLI can't see its siblings). Both piggyback on the single
	// live-container enumeration.
	storePruneOK := false
	if !o.inJail() {
		live, known := o.liveYoloContainers(rt)
		if known && len(live) == 0 {
			storePruneOK = true
		}
		// Backstop reap of orphaned per-jail broker relays: a relay outlives the
		// yolo process that spawned it, and stopLoopholes only reaps the current
		// jail's relay in the original process's graceful tail — jails ended from
		// attach sessions leak their relay otherwise. Declines when liveness is
		// unknown (known==false); excludes the current jail's just-ensured relay.
		if known {
			func() {
				defer func() { _ = recover() }() // cleanup must never block a run
				o.relayReapOrphans(known, live, cname)
			}()
		}
	}

	// Cache relocations: read from the HOST user config only (never the merged
	// config — see config.LoadCacheRelocations for the threat model) and
	// provisioned BEFORE the argv is assembled. Both halves of the ordering
	// matter: podman kills the whole container with a bare
	// "statfs …: no such file or directory" when a bind source is missing, and
	// the mountpoint it would otherwise invent for us is root-owned. A failure
	// here is fatal rather than a warning — continuing would start a jail whose
	// cache silently sits back on the filesystem the user moved it off.
	relocations, relErr := config.LoadCacheRelocations(func(msg string) {
		out.printf("[yellow]Warning: %s[/yellow]", msg)
	})
	if relErr != nil {
		out.printf("[bold red]%s[/bold red]", relErr.Error())
		lock.Close()
		return 1
	}
	// Apple Container gets the list (assembly warns that it is skipping them) but
	// not the directories: provisioning a mountpoint nothing will mount over just
	// leaves an empty stub in the cache that reads like lost data.
	if rt != "container" {
		if err := storage.EnsureCacheRelocations(relocations); err != nil {
			out.printf("[bold red]%s[/bold red]", err.Error())
			lock.Close()
			return 1
		}
	}

	// --- Assemble the ordered argv ---
	in := &assembleInput{
		cfg:              cfg,
		rt:               rt,
		cname:            cname,
		repoRoot:         repoRoot,
		agentsList:       agentsList,
		agentSpecs:       agentSpecs,
		agentsPath:       agentsPath,
		wsState:          wsState,
		miseStore:        jailMiseStoreDir(o.inJail()),
		identityEnv:      identityEnv,
		hostTZ:           detectHostTZ(),
		yoloVersion:      o.yoloVersion(repoRoot),
		mountTargets:     BindMountTargets(),
		lspNPMInstall:    lspNPMOf(cfg),
		lspGoInstall:     lspGoOf(cfg),
		storePruneOK:     storePruneOK,
		cacheRelocations: relocations,
		writableHomeDirs: config.WritableHomeDirs(cfg),
	}
	runCmd := o.assembleRunCmd(in)

	// Determine the port-forward socket dir (Linux podman + AC only).
	var forwardHostPorts []any
	netMode := o.Network
	if netSec := cfgMap(cfg, "network"); netSec != nil {
		if m := mapStr(netSec, "mode"); m != "" {
			netMode = m
		}
		if netMode == "bridge" {
			forwardHostPorts = asAnyList(mapGet(netSec, "forward_host_ports"))
		}
	}
	var portSocketDir string
	if len(forwardHostPorts) > 0 && (rt == "container" || !o.IsMacOS) {
		portSocketDir = o.fwdSocketDir(cname)
	}

	// Tracking + owner-PID + window title.
	_ = runtimeWriteTracking(cname, o.Workspace)
	o.writeOwnerPID(cname)

	// Start host-side port forwarding BEFORE the container.
	var socatProcs []*exec.Cmd
	if portSocketDir != "" {
		socatProcs = o.startHostPortForwarding(forwardHostPorts, cname, portSocketDir)
	}

	// Start host services (cgroup delegate + external) BEFORE the container,
	// inserting each `-e VAR=sock` pair at index(image).
	hostServices := o.startLoopholes(cname, rt, cfg)
	imageRef := jailImageRef(rt)
	for _, svc := range hostServices {
		idx := indexOfSlice(runCmd, imageRef)
		if idx < 0 {
			continue
		}
		runCmd = insertStrsAt(runCmd, idx, []string{"-e", svc.envVarName + "=" + svc.jailSocketPath})
	}

	// Final internal command tail.
	runCmd = append(runCmd, buildFinalInternalCmd(targetCmd, o.Profile))

	if o.Getenv("YOLO_DEBUG") != "" {
		// Write RAW (not via the rich-stripping printer): the argv contains
		// literal bracket sequences (e.g. the grep block_flags "-*[rR]*", the
		// "[path]" suggestion) that the rich-tag regex would eat.
		fmt.Fprintln(o.Stderr, shquoteJoinDebug(runCmd))
	}

	// Launch under the TTY proxy. on_started releases the lock once the
	// container is visible; on_terminate is the window-close/SIGTERM teardown.
	onStarted := func(_ *os.Process) {
		for i := 0; i < lockReleasePollAttempts; i++ {
			if o.findRunningContainer(cname, rt) != "" {
				break
			}
			time.Sleep(time.Duration(lockReleasePollIntervalSeconds * float64(time.Second)))
		}
		lock.Close()
	}
	onTerminate := func() {
		o.stopJail(cname, rt)
		cleanupPortForwarding(socatProcs, portSocketDir)
		lock.Close()
		o.stopLoopholes(hostServices, socketsDir, cname, rt)
	}

	// Fresh-launch startup banner (with resource parts) to stderr for log
	// capture (audit §B#4.
	o.emitStartupBanner(rt, cname, resPartsFor(cfg, rt), "")

	rc, runErr := runWithProxy(runCmd, onStarted, onTerminate)
	if runErr != nil {
		out.printf("[bold red]Configured runtime '%s' not found on PATH.[/bold red]", rt)
		out.print("[dim]Run `yolo check` to validate runtime availability before restarting.[/dim]")
		cleanupPortForwarding(socatProcs, portSocketDir)
		// Release the lock BEFORE stop_loopholes (its guard takes the same lock
		// non-blocking, and on_started never ran).
		lock.Close()
		o.stopLoopholes(hostServices, socketsDir, cname, rt)
		clearOwnerPID(cname)
		return 1
	}

	// Normal exit teardown.
	cleanupPortForwarding(socatProcs, portSocketDir)
	o.stopLoopholes(hostServices, socketsDir, cname, rt)
	clearOwnerPID(cname)
	o.maybeWarnAboutOOMKiller(rc, rt)

	if o.Profile {
		o.pr(o.Stderr).printf("[bold cyan]--- Host-side timing ---[/bold cyan]")
		o.pr(o.Stderr).printf("  Total (host-side):  %.3fs", o.Now().Sub(profileStart).Seconds())
	}
	return rc
}

// attachExisting runs the exec-into-existing-container branch (and the
// raced-attach twin). raced selects the second banner text.
func (o *Options) attachExisting(cname, rt, targetCmd string, identityEnv []string, raced bool) int {
	out := o.pr(o.Stdout)
	// Startup banner to stderr — surfaces the jail's BAKED version so a host CLI
	// upgrade attaching to a pre-upgrade container (stale shims/mounts/entrypoint)
	// is visible at a glance (audit §B#4.
	o.emitStartupBanner(rt, cname, nil, o.bakedJailVersion(rt, cname))
	if raced {
		out.printf("[bold cyan]Attaching to jail started by another process [dim](%s)[/dim]...[/bold cyan]", cname)
	} else {
		out.printf("[bold cyan]Attaching to existing jail [dim](%s)[/dim]...[/bold cyan]", cname)
	}
	// Heal the per-jail relay before handing the session over.
	o.ensureBrokerRelay(cname, rt)

	execFlags := []string{"-i"}
	if o.IsTTYStdout() {
		execFlags = append(execFlags, "-t")
	}
	runCmd := append([]string{rt, "exec"}, execFlags...)
	runCmd = append(runCmd, identityEnv...)
	runCmd = append(runCmd, cname, "yolo-entrypoint", targetCmd)

	rc, err := runWithProxy(runCmd, nil, nil)
	if err != nil {
		out.printf("[bold red]Configured runtime '%s' not found on PATH.[/bold red]", rt)
		out.print("[dim]Run `yolo check` to validate runtime availability before restarting.[/dim]")
		return 1
	}
	o.maybeWarnAboutOOMKiller(rc, rt)
	return rc
}

// detectHostTZ resolves the host timezone for the TZ env (or "").
func detectHostTZ() string {
	if tz, ok := storage.DetectHostTimezone(); ok {
		return tz
	}
	return ""
}

func lspNPMOf(cfg *jsonx.OrderedMap) string { n, _ := resolveLSPInstalls(cfg); return n }
func lspGoOf(cfg *jsonx.OrderedMap) string  { _, g := resolveLSPInstalls(cfg); return g }

// runtimeWriteTracking wraps runtime.WriteContainerTracking with the resolved
// workspace path.
func runtimeWriteTracking(cname, workspace string) error {
	resolved := resolvePath(workspace)
	return writeTracking(cname, resolved)
}

// emitStartupBanner writes the start-of-run banner to stderr (audit §B#4). It
// reuses StartupBanner for consistent formatting. version is
// version.Get; jailVersion is the container's baked
// YOLO_VERSION (attach path only, else "").
func (o *Options) emitStartupBanner(rt, cname string, resParts []string, jailVersion string) {
	banner := StartupBanner(version.Get(o.RepoRootForBanner()), rt, cname, resParts, jailVersion)
	fmt.Fprint(o.Stderr, banner)
}

// RepoRootForBanner returns the repo root env hint for version resolution
// (YOLO_REPO_ROOT); version.Get falls back to git-describe / "unknown".
func (o *Options) RepoRootForBanner() string {
	if o.Getenv != nil {
		return o.Getenv("YOLO_REPO_ROOT")
	}
	return ""
}

// bakedJailVersion reads the YOLO_VERSION baked into a running container via
// `<rt> inspect`, or "". Shown in the
// attach banner only when it differs from the host version.
func (o *Options) bakedJailVersion(rt, cname string) string {
	if o.Exec == nil {
		return ""
	}
	res := o.Exec([]string{rt, "inspect", "--format", "{{range .Config.Env}}{{println .}}{{end}}", cname}, "", nil, 3*time.Second)
	if !res.Ran || res.RC != 0 {
		return ""
	}
	if v, ok := runtime.BakedYoloVersionFromInspectEnv(strings.Split(res.Stdout, "\n")); ok {
		return v
	}
	return ""
}

// resPartsFor reconstructs the banner's resource-limit parts (memory/cpus/pids)
// from the resources config, matching the res_parts built
// during argv assembly. Podman path: pids defaults to 32768. Apple Container's
// half-host defaults are the run-slice's concern; here only explicit config is
// surfaced (the native run path is podman/Linux).
func resPartsFor(cfg *jsonx.OrderedMap, rt string) []string {
	var parts []string
	res, _ := cfg.Get("resources")
	rm, _ := res.(*jsonx.OrderedMap)
	get := func(k string) (any, bool) {
		if rm == nil {
			return nil, false
		}
		return rm.Get(k)
	}
	if mem, ok := get("memory"); ok {
		if s, ok := mem.(string); ok && s != "" {
			parts = append(parts, "memory="+s)
		}
	}
	if cpus, ok := get("cpus"); ok && cpus != nil {
		parts = append(parts, "cpus="+pyStrCoerce(cpus))
	}
	if rt != "container" {
		pids := "32768"
		if p, ok := get("pids_limit"); ok && p != nil {
			pids = pyStrCoerce(p)
		}
		parts = append(parts, "pids="+pids)
	}
	return parts
}
