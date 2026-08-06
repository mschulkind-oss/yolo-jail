package run

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	_ "github.com/mschulkind-oss/yolo-jail/internal/packreg" // registers the embedded packs with packload
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
	// Repo-root resolution is a HARD GATE for the container backends: without a
	// flake there is nothing to build the image from, and silently running a
	// stale loaded/cached image instead is worse than failing — it hides that the
	// environment is not what the config describes. (This reverts D2's graceful
	// degradation: launching on an old image with no rebuild was deemed a
	// footgun, not a convenience.) The gate is applied at repoRootFatal below,
	// AFTER the macos-user branch, because macos-user with empty `packages:`
	// genuinely needs no repo (it materializes native darwin packages only when
	// `packages:` is non-empty, and MaterializeDarwin fails loudly on a bad flake
	// root of its own accord).
	repoRoot, repoRootOK := o.RepoRoot()
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
		// Same notice as the container paths: a brand-new macos-user user has no packs
		// either, and the native backend is where a "where is my agent?" is hardest to
		// diagnose (no image, no provisioning output to read back).
		o.warnIfNoPacks()
		return o.MacosUserRun(cfg, o.Workspace, config.SelectedAgents(cfg), agentArgv, repoRoot, o.DryRun)
	}
	if o.DryRun {
		o.pr(o.Stdout).print(
			"[bold red]--dry-run is only supported for the macos-user runtime.[/bold red]  " +
				`Set runtime: "macos-user" (or YOLO_RUNTIME=macos-user) to use it.`)
		return 1
	}
	// Container backends need a flake to build the image from. A missing repo
	// root is FATAL rather than a degraded launch on a stale image: running the
	// wrong environment silently is the failure this refuses. reporoot.Resolve
	// already found nothing here (env → cwd-walk → exe-relative bundle all
	// missed), so print the same actionable fix the resolver would and exit.
	if !repoRootOK {
		o.pr(o.Stderr).print("[bold red]Cannot find yolo-jail repo root.[/bold red]\n" +
			"The yolo CLI needs the repo (a flake) to build the jail image, and refuses to\n" +
			"launch on a possibly-stale cached image instead.\n\n" +
			"Fix: run yolo from inside a yolo-jail checkout, point it at one with\n" +
			"[bold]YOLO_REPO_ROOT[/bold], or reinstall so the flake bundle ships beside the\n" +
			"binary (`just install`):\n" +
			"  YOLO_REPO_ROOT=~/code/yolo-jail yolo …")
		return 1
	}
	return o.runContainer(cfg, rt, repoRoot)
}

// warnIfNoPacks prints the empty-packs notice when the user has no packs configured.
//
// The text is config.NoPacksMessage/NoPacksGuidance, shared with the `yolo check` Packs
// section so the two surfaces cannot drift; the sentence-ending period is added here
// because this is prose and a check badge line is not. It is deliberately free of
// blame: an empty pack list is exactly what a brand-new install looks like, not a
// mistake anybody made. It names `yolo pack --help` rather than `yolo config-ref`
// because that is the shorter answer to "what do I put here" — packUsage opens with
// what a pack is, where config-ref's `packs` entry is the key schema underneath it.
//
// Packs are the only way content — an agent included — gets installed into a jail, so
// an empty list is not a lean jail, it is a jail with nothing in it. That state is
// otherwise SILENT: with no packs there are no selected agents, so refreshJailBriefings
// writes zero briefings (its loop runs over the RESOLVED agents) and stages zero
// per-agent skills. There is no file left to put a note in, which is why this is
// printed rather than written — and why it keys off the PACK list rather than the agent
// list, which is both the thing the user edits and the thing that still exists.
//
// Silent whenever `packs` is present but UNUSABLE, which is not the same test as
// "LoadPacks returned an error". An error covers only a JSONC parse failure; a
// non-list value and a list whose every entry is invalid both come back as zero
// entries with a nil error, because checkPacks routes per-entry problems to the warn
// callback instead. All three mean the user DID configure packs, so "you have no
// packs" would misdiagnose "your packs are malformed" — and stagePacks (via
// validatePacks) already fails the launch naming the real problem. Hence the callback
// is non-nil and any problem suppresses the notice: only a genuinely absent or empty
// list reaches the print.
//
// Counting callback invocations is exact rather than approximate because LoadPacks
// loads strict: every loader-side warning (parse failure, bad include_if_found) is an
// ERROR under strict, so the only thing that can reach this callback is a checkPacks
// per-entry problem. An unrelated config warning cannot false-suppress the notice.
//
// It re-reads the user config rather than taking a count threaded down from stagePacks:
// one small file read per launch is cheaper than making every staging-side signature
// carry a value only this notice consumes.
func (o *Options) warnIfNoPacks() {
	problems := 0
	entries, err := config.LoadPacks(func(string) { problems++ })
	// HasConfiguredPack, not len(entries): the conventional local pack is included with no
	// config line (config.localPackEntry), and it is CONTENT — a jail whose only pack is
	// ~/.config/yolo-jail/local has skills and prose and still nothing to run them. Counting
	// it here would silence a notice that is still true.
	if err != nil || problems > 0 || config.HasConfiguredPack(entries) {
		return
	}
	// Stderr, like every other launch notice: a launch is usually `yolo -- cmd`, and
	// the user redirects the COMMAND's stdout — a notice on stdout would be swallowed
	// by that redirect, or corrupt a piped payload.
	out := o.pr(o.Stderr)
	out.print("[bold yellow]" + config.NoPacksMessage + ".[/bold yellow]")
	out.print("[yellow]" + config.NoPacksGuidance + "[/yellow]")
}

// notePackHostAccess prints, to stderr, what each loaded pack reads from the host
// this launch — its mounts, host-file reads, and env vars. This is the transparency
// half of the fetched-pack approval model: a pack (fetched or local) that touches
// the host says so at every launch, not just once in a lockfile, so the effective
// environment is always visible.
//
// It reads the FOOTPRINT, which already reflects the approval gate: an unapproved
// fetched pack has MayAccessHost=false, so its host-read claims are absent from the
// footprint and correctly do not appear here (they were refused). Env is always
// shown (it is never gated). A pack that touches nothing prints nothing.
func (o *Options) notePackHostAccess(loadedPacks []*packload.Pack) {
	type line struct{ pack, claim string }
	var lines []line
	for _, p := range loadedPacks {
		for _, c := range packload.FootprintOf(p).Claims {
			switch c.Kind {
			case packdecl.KindMount, packdecl.KindReadsHost, packdecl.KindEnv:
				detail := c.Target
				if c.Detail != "" {
					detail += " " + c.Detail
				}
				lines = append(lines, line{p.Name, string(c.Kind) + " " + detail})
			}
		}
	}
	if len(lines) == 0 {
		return
	}
	out := o.pr(o.Stderr)
	out.print("[dim]Pack environment this launch:[/dim]")
	for _, l := range lines {
		out.print("[dim]  " + l.pack + ": " + l.claim + "[/dim]")
	}
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

	// Command construction (needed for both exec and run paths).
	//
	// Flags come from the EMBEDDED packs, not this run's loaded set, for two reasons that
	// both point the same way. Ordering: the command is built here, before packs are staged
	// (staging needs the container name, which needs the workspace). And reachability: this
	// same construction serves the ATTACH path into an already-running jail, where the
	// loaded set belongs to whatever launched it. Using what yolo ships means `yolo --
	// copilot` gets its flags identically on both paths.
	//
	// The cost is that a CONFIGURED pack's launchFlags do not apply to a bare `yolo --
	// <bin>` invocation. Real but narrow: the in-jail launcher that pack generates still
	// applies them, so the flags are not lost — only this one host-side injection misses.
	fullCommand := append([]string{}, o.Args...)
	targetCmd := "bash"
	if len(fullCommand) > 0 {
		fullCommand = packload.InjectLaunchFlags(packload.Embedded(), fullCommand)
		targetCmd = shquoteJoin(fullCommand)
	}

	cname := runtime.FromWorkspace(o.Workspace)

	// Sweep jails orphaned by an uncatchable kill before the attach decision.
	o.reapOrphanedJails(rt)

	existingCID := ""
	if !o.New {
		existingCID = o.findRunningContainer(cname, rt)
	}

	// Refresh the per-jail skills + AGENTS/CLAUDE staging on every invocation.
	agentsPath, packStaging, loadedPacks, err := o.refreshJailBriefings(cname, cfg, rt)
	if err != nil {
		out.printf("[bold red]%s[/bold red]", err.Error())
		return 1
	}

	if existingCID != "" {
		return o.attachExisting(cname, rt, targetCmd, false)
	}

	// --- Fresh launch: config-change approval ---
	if !o.checkConfigChanges(cfg) {
		return 1
	}

	// --- Freeze the workspace-config boot baseline ---
	// So an in-jail `yolo config drift` has an immutable record of the workspace
	// config THIS jail was built from. Workspace-only (not the merged cfg) and read
	// through the same loader the in-jail diff uses, so the two sides compare exactly.
	// Best-effort: a jail must not fail to launch because a baseline write hiccuped —
	// drift then reports "cannot determine" rather than a false "no drift".
	if wsCfg, wsErr := config.LoadWorkspaceConfig(o.Workspace, false, func(string) {}); wsErr == nil {
		if err := config.WriteWorkspaceBootBaseline(o.Workspace, wsCfg); err != nil {
			out.printf("[dim]Warning: could not write config drift baseline: %s[/dim]", err.Error())
		}
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
			return o.attachExisting(cname, rt, targetCmd, true)
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
	wsState := o.prepareWsState(cfg, loadedPacks)

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

	// User host_files (docs/plans/host-file-staging.md). Read with the same
	// scope rule as cache_relocations — a SOURCE-BEARING entry comes only from the
	// host user config, never the merged/workspace one, so a repo cannot decide
	// which host files cross into the jail (config.LoadHostFiles enforces that by
	// construction). probeSource is on host-side only: host paths are deliberately
	// not in a jail's mount namespace, so stat'ing them from a nested run would
	// turn a valid host config into a fatal error.
	//
	// Unlike cache_relocations a failure here is a WARNING, not fatal: every entry
	// renders fail-open in the entrypoint anyway (a missing source falls back to
	// the defaults layer), so a jail that starts without one composed file is the
	// feature degrading, not the jail running against the wrong storage.
	hostFiles, hfErr := config.LoadHostFiles(cfg, func(msg string) {
		out.printf("[yellow]Warning: %s[/yellow]", msg)
	}, !o.inJail())
	if hfErr != nil {
		out.printf("[yellow]Warning: host_files: %s — no host files staged[/yellow]", hfErr.Error())
		hostFiles = nil
	}
	// Provision each destination's writable staging BEFORE the argv is assembled:
	// a missing bind source kills the whole container, and the GlobalHome symlink
	// hatch must exist before the :ro base is applied.
	if rt != "container" {
		prepareHostFiles(wsState, hostFiles)
	}

	// --- Assemble the ordered argv ---
	in := &assembleInput{
		cfg:              cfg,
		rt:               rt,
		cname:            cname,
		packs:            loadedPacks,
		agentsPath:       agentsPath,
		packStaging:      packStaging,
		wsState:          wsState,
		miseStore:        jailMiseStoreDir(o.inJail()),
		hostTZ:           detectHostTZ(),
		yoloVersion:      o.yoloVersion(repoRoot),
		mountTargets:     BindMountTargets(),
		lspNPMInstall:    lspNPMOf(cfg),
		lspGoInstall:     lspGoOf(cfg),
		storePruneOK:     storePruneOK,
		cacheRelocations: relocations,
		writableHomeDirs: config.WritableHomeDirs(cfg),
		hostFiles:        hostFiles,
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
		// "[path]" suggestion) that the rich-tag regex would eat. Redact
		// secret-bearing env values (…_TOKEN=…) so the per-jail broker token
		// isn't leaked to the debug log.
		fmt.Fprintln(o.Stderr, shquoteJoinDebug(redactSecretsForDebug(runCmd)))
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

	// The empty-packs notice rides immediately behind the banner: this is the LAST
	// host-side output before the container takes the terminal, so it is the only spot
	// where the message is still on screen when the agent (or the fallback bash)
	// starts. Printed any earlier it scrolls away behind the nix build.
	o.warnIfNoPacks()

	// Right behind that: what each loaded pack reads from the host this launch. A
	// fetched pack CAN read the host now (with approval), so the effective host access
	// must be visible every launch, not just recorded in a lockfile — the transparency
	// half of the approval model.
	o.notePackHostAccess(loadedPacks)

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
func (o *Options) attachExisting(cname, rt, targetCmd string, raced bool) int {
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
	// Attach gets the notice too, and that is not symmetry for its own sake: once a
	// jail is up, attaching is how a user re-enters it, so a fresh-launch-only notice
	// is one a user with a long-lived jail may never see.
	o.warnIfNoPacks()
	// Heal the per-jail relay before handing the session over.
	o.ensureBrokerRelay(cname, rt)

	execFlags := []string{"-i"}
	if o.IsTTYStdout() {
		execFlags = append(execFlags, "-t")
	}
	runCmd := append([]string{rt, "exec"}, execFlags...)
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
	// Resolve the repo root via the shared method (o.RepoRoot → reporoot.Resolve),
	// so the banner version matches run/check and describes the yolo-jail repo,
	// not whatever repo the cwd happens to sit in. "" → version.Get falls back to
	// the baked stamp / "unknown".
	repoRoot := ""
	if o.RepoRoot != nil {
		if rr, ok := o.RepoRoot(); ok {
			repoRoot = rr
		}
	}
	banner := StartupBanner(version.Get(repoRoot), rt, cname, resParts, jailVersion)
	// Fprintln, not Fprint: StartupBanner returns no trailing newline, so the old
	// Fprint left the cursor mid-line and whatever printed next was glued onto the
	// banner ("…pids=32768No packs are configured…", observed in a nested launch).
	// It was invisible before only because the next writer happened to be the
	// container's own output, which opens with its own newline.
	fmt.Fprintln(o.Stderr, banner)
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
