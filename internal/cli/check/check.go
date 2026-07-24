package check

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/nixdiag"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/storage"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// Check validates environment, config, and build. Returns the
// process exit code (0 = no failures, 1 = any fail). The whole section sequence
// is driven off the injected seams so it is deterministic.
func Check(opts Options) int {
	fillDefaults(&opts)
	o := &opts
	// Gate color on a real terminal: o.Color merely requests it, but ANSI must
	// never leak to a pipe/redirect (the cli-color-audit gate rule). The
	// injectable IsTTYStdout seam defaults to the shared ioctl probe on
	// os.Stdout; a test buffer or a redirect reports false, so goldens stay
	// Color=false. Mirrors run's `Color && IsTTYStdout()`.
	color := o.Color && o.IsTTYStdout()
	r := newReporter(o.Stdout, color)

	// Ensure global storage — best-effort; check should still run the probes
	// even on a hard fs error, so the error is ignored (it never fails in normal
	// operation).
	if !o.SkipEnsureStorage {
		// Wire the v2 layout migration (audit §B#2: nil left it dead). canReclaim
		// returns false — the fail-safe defer (the full live-jail probe is the run
		// path's concern; declining never harms).
		_ = storage.EnsureGlobalStorage(func() {
			insideJail := os.Getenv("YOLO_VERSION") != ""
			storage.MigrateStorageLayout(insideJail, func() bool { return false }, func(msg string) {
				fmt.Fprintln(os.Stderr, msg)
			})
		})
	}

	workspace := o.Workspace

	r.blank()
	r.section("YOLO Jail Check")
	r.blank()

	// Version line (dim).
	ver := o.Version
	if ver == "" {
		repoRootForVer := ""
		if rr, ok := o.RepoRoot(); ok {
			repoRootForVer = rr
		}
		ver = version.Get(repoRootForVer)
		if ver == "" {
			ver = "unknown"
		}
	}
	r.line(r.style("Version: "+ver, ansiDim))
	r.blank()

	// --- Container Runtime ---
	detectedRuntime := o.sectionContainerRuntime(r)

	// --- Nix ---
	o.sectionNix(r)

	// --- macOS Platform ---
	if o.IsMacOS {
		o.sectionMacOSPlatform(r, nil) // config not yet loaded; uses workspace
	}

	// --- Global Storage ---
	o.sectionGlobalStorage(r)

	// --- Config Files ---
	userConfig, workspaceConfig, parseFailed := o.sectionConfigFiles(r, workspace)
	if parseFailed {
		r.summaryFailOnly()
		return 1
	}

	// Merge + flake.nix resolution.
	merged := config.MergeConfig(userConfig, workspaceConfig)
	var repoRoot string
	repoRootOK := false
	if rr, ok := o.RepoRoot(); ok {
		repoRoot = rr
		repoRootOK = true
		if o.PathExists(filepath.Join(rr, "flake.nix")) {
			r.ok("flake.nix found: " + filepath.Join(rr, "flake.nix"))
		} else {
			r.warn("flake.nix not found at "+filepath.Join(rr, "flake.nix"), "")
		}
	} else {
		r.fail("Could not resolve the yolo-jail repo root", "")
	}

	// --- Merged Configuration ---
	if exit := o.sectionMergedConfig(r, merged, workspace, userConfig, workspaceConfig); exit {
		r.summaryFailWarn()
		return 1
	}

	// Accumulated-fail gate: short-circuit here on ANY failure so far — not just
	// config-validation errors — so a failed repo-root resolution or flake check
	// (above) stops the run BEFORE the Entrypoint Dry-Run and the real `nix
	// build` / orphan-cleanup prompt in the Image section. Without this, check
	// would do destructive-ish work (a surprise nix build) on an unhealthy host
	// (re-audit §C).
	if r.failed > 0 {
		r.summaryFailWarn()
		return 1
	}

	runtimeSel, _ := o.runtimeForCheck(merged)
	isNativeRuntime := inStrSlice(paths.NativeRuntimes, runtimeSel)

	// Cache relocations are provisioned here — after the runtime is known — so
	// that check leaves a fresh host ready for the next run (target dir + the
	// GLOBAL_CACHE mountpoint), because a relocation that cannot be provisioned
	// is a jail podman refuses to start ("statfs …: no such file or directory").
	// Gated on the runtime for the same reason run.go gates it: only podman
	// mounts them, and creating a mountpoint nothing will ever mount over just
	// leaves an empty dir in the cache that reads like lost data.
	// Silent on a load error (the Config Files section above reports a bad user
	// config with far better context) and silent when nothing is configured. The
	// loader's own warnings ARE surfaced: the "target directory does not exist"
	// skip is the loader's alone, and it is the one problem that makes a run
	// quietly leave the cache where it was.
	if !isNativeRuntime && runtimeSel != "container" {
		if rels, err := config.LoadCacheRelocations(r.warningLine); err == nil && len(rels) > 0 {
			if err := storage.EnsureCacheRelocations(rels); err != nil {
				r.fail(err.Error(), "Fix the path or drop the key from "+paths.UserConfigPath())
			}
		}
	}

	// --- Entrypoint Dry-Run ---
	o.sectionEntrypointDryRun(r, repoRoot, repoRootOK, workspace, merged)

	// --- macos-user backend readiness (native runtime only) ---
	if isNativeRuntime {
		o.checkMacosUserBackend(r)
		r.blank()
	}

	// --- GPU (NVIDIA) ---
	o.sectionGPUNvidia(r, merged)
	// --- GPU (AMD / ROCm) ---
	o.sectionGPUAmd(r, merged)
	// --- KVM ---
	o.sectionKVM(r, merged)

	// --- Image & Containers ---
	imageBuildSkipped := o.sectionImageBuild(r, merged, repoRoot, repoRootOK, isNativeRuntime)
	notLoadedHint := "Run 'yolo' once to build and load the image"
	if imageBuildSkipped {
		notLoadedHint = "This host can't build the image (needs a Linux builder — see the " +
			"steps printed above), or download a prebuilt image once the " +
			"cache is published."
	}

	if !isNativeRuntime && detectedRuntime != "" {
		o.sectionContainerImage(r, detectedRuntime, notLoadedHint)
		o.sectionRunningJails(r, detectedRuntime)
	}

	// --- Host-side loopholes ---
	r.section("Loopholes")
	o.checkLoopholes(r)
	r.blank()

	// --- Per-jail host-service liveness ---
	if !isNativeRuntime {
		r.section("Per-jail host-service liveness")
		o.checkHostServiceLiveness(r)
	}
	r.blank()

	// --- Disk usage ---
	r.section("Disk usage")
	o.checkDiskUsage(r, merged)
	r.blank()

	// --- Loopholes (config-inline daemons) ---
	o.sectionInlineLoopholes(r, merged)

	// --- FHS loader (nix-ld) baseline-drift tripwire (in-jail only) ---
	o.sectionNixLD(r)

	// --- Nix auto-GC (store growth net; storage §2) — detect-and-warn only ---
	o.sectionAutoGC(r)

	// --- Summary ---
	r.summaryFinal()

	if r.failed > 0 {
		return 1
	}
	return 0
}

// sectionContainerRuntime runs the Container Runtime block. Returns the
// detected (live) container runtime, or "".
func (o *Options) sectionContainerRuntime(r *reporter) string {
	r.section("Container Runtime")
	detectedRuntime := ""

	// Cheap early read of the effective runtime (env wins; else config runtime).
	earlyRuntime := o.Getenv("YOLO_RUNTIME")
	if !inStrSlice(paths.AllRuntimes, earlyRuntime) {
		if cfg, err := config.LoadConfig(o.Workspace, false, func(string) {}); err == nil {
			earlyRuntime = configRuntime(cfg)
		} else {
			earlyRuntime = ""
		}
	}
	if inStrSlice(paths.NativeRuntimes, earlyRuntime) && o.IsMacOS {
		r.ok("Native runtime '" + earlyRuntime + "' — no container runtime needed")
		r.blank()
		return ""
	}

	type probe struct {
		name         string
		versionCmd   []string
		livenessCmd  []string
		livenessHint string
	}
	// The hint for an installed-but-not-connected runtime is the user's to act
	// on. On macOS a not-connected podman almost always means the VM is down, so
	// lead with `podman machine start`; `podman info` is still the way to triage
	// anything else. On Linux podman is daemonless, so a failing liveness probe
	// is a socket/service issue — `podman info` is the whole story.
	podmanHint := "Run 'podman info' to diagnose"
	if o.IsMacOS {
		podmanHint = "Start the VM: podman machine start " +
			"(first time: podman machine init && podman machine start).  " +
			"Otherwise run 'podman info' to diagnose"
	}
	probes := []probe{
		{"podman", []string{"podman", "--version"}, []string{"podman", "info"}, podmanHint},
		{"container", []string{"container", "--version"}, []string{"container", "system", "status"}, "Start with: container system start"},
	}
	selectedRuntime := o.Getenv("YOLO_RUNTIME")
	if !inStrSlice(paths.SupportedRuntimes, selectedRuntime) {
		selectedRuntime = ""
	}

	type offlineEntry struct{ rt, version, hint string }
	var offline []offlineEntry
	for _, p := range probes {
		if _, ok := o.LookPath(p.name); !ok {
			continue
		}
		verRes := o.Exec(p.versionCmd, "", nil, 5*time.Second)
		if !verRes.Ran {
			r.fail(p.name+" found but not working: exec failed", "")
			continue
		}
		if verRes.Timeout {
			r.fail(p.name+" found but not working: timeout", "")
			continue
		}
		version := firstLine(strings.TrimSpace(verRes.Stdout))
		pingRes := o.Exec(p.livenessCmd, "", nil, 10*time.Second)
		if !pingRes.Ran || pingRes.Timeout {
			r.fail(p.name+" found but not working: liveness probe failed", "")
			continue
		}
		pingOK := pingRes.RC == 0
		if p.name == "container" && pingOK {
			pingOK = strings.Contains(strings.ToLower(pingRes.Stdout), "running")
		}
		if pingOK {
			r.ok(p.name + ": " + version)
			if detectedRuntime == "" {
				detectedRuntime = p.name
			}
		} else {
			offline = append(offline, offlineEntry{p.name, version, p.livenessHint})
		}
	}

	for _, e := range offline {
		if e.rt == selectedRuntime || detectedRuntime == "" {
			r.warn(e.rt+": "+e.version+" (not connected)", e.hint)
		} else {
			r.dim(e.rt + ": " + e.version + " (not connected, not selected)")
		}
	}

	if detectedRuntime == "" {
		if len(offline) > 0 {
			var names []string
			var starts []string
			for _, e := range offline {
				names = append(names, e.rt)
				starts = append(starts, e.rt+": "+e.hint)
			}
			r.fail("Container runtime installed but not started ("+strings.Join(names, ", ")+")",
				"It's installed — you just need to START it.\n"+strings.Join(starts, "; "))
		} else {
			r.fail("No container runtime installed",
				"Install one:\n"+
					"  Linux:  your package manager, e.g. `sudo apt install podman`\n"+
					"  macOS:  `brew install podman` then `podman machine init "+
					"&& podman machine start`,\n"+
					"          or `brew install container` then `container system start`")
		}
	}
	r.blank()
	return detectedRuntime
}

// sectionGlobalStorage runs the Global Storage block.
func (o *Options) sectionGlobalStorage(r *reporter) {
	r.section("Global Storage")
	entries := []struct {
		name string
		path string
	}{
		{"Home", paths.GlobalHome()},
		{"Mise (jail store)", paths.GlobalMise()},
		{"Containers", paths.ContainerDir()},
		{"Agents", paths.AgentsDir()},
		{"Build", paths.BuildDir()},
	}
	for _, e := range entries {
		if o.PathExists(e.path) {
			r.ok(e.name + ": " + e.path)
		} else {
			r.warn(e.name+" directory missing: "+e.path, "Will be created on first run")
		}
	}
	r.blank()
}

// sectionConfigFiles runs the Config Files block. Returns (userConfig,
// workspaceConfig, parseFailed). A parse failure sets parseFailed so the caller
// early-exits with the fail-only summary.
func (o *Options) sectionConfigFiles(r *reporter, workspace string) (*jsonx.OrderedMap, *jsonx.OrderedMap, bool) {
	r.section("Config Files")
	userPath := paths.UserConfigPath()
	failed := false

	userConfig, err := config.LoadJSONCWithIncludes(userPath, userPath, true, func(string) {}, nil)
	if err != nil {
		userConfig = jsonx.NewOrderedMap()
		r.fail(err.Error(), "")
		failed = true
	} else if o.PathExists(userPath) {
		r.ok("Parsed user config: " + userPath)
	} else {
		r.ok("No user config found: " + userPath)
	}

	wsPath := filepath.Join(workspace, "yolo-jail.jsonc")
	workspaceConfig, err := config.LoadJSONCWithIncludes(wsPath, "yolo-jail.jsonc", true, func(string) {}, nil)
	if err != nil {
		workspaceConfig = jsonx.NewOrderedMap()
		r.fail(err.Error(), "")
		failed = true
	} else if o.PathExists(wsPath) {
		r.ok("Parsed workspace config: " + wsPath)
	} else {
		r.ok("No workspace yolo-jail.jsonc found")
	}
	r.blank()
	return userConfig, workspaceConfig, failed
}

// sectionMergedConfig runs the Merged Configuration block. Returns true when
// there were validation errors (the caller early-exits with fail+warn summary).
func (o *Options) sectionMergedConfig(r *reporter, merged *jsonx.OrderedMap, workspace string, userConfig, workspaceConfig *jsonx.OrderedMap) bool {
	r.section("Merged Configuration")
	resolver := loopholes.NewResolver()
	errors, warnings := config.ValidateConfig(merged, workspace, resolver)
	runtimeSel, runtimeErr := o.runtimeForCheck(merged)
	if runtimeErr != "" {
		errors = append(errors, runtimeErr)
	} else if runtimeSel != "" {
		r.ok("Runtime available: " + runtimeSel)
	}

	// Same-file preset+null contradictions.
	errors = append(errors, checkPresetNullConflicts(userConfig, paths.UserConfigPath())...)
	errors = append(errors, checkPresetNullConflicts(workspaceConfig, "yolo-jail.jsonc")...)

	for _, msg := range warnings {
		r.warn(msg, "")
	}
	if len(errors) > 0 {
		for _, msg := range errors {
			r.fail(msg, "")
		}
		r.blank()
		return true
	}
	r.ok("Merged config is semantically valid")
	r.blank()
	return false
}

// sectionEntrypointDryRun runs the Go entrypoint generators in a temp home and
// reports success/failure.
func (o *Options) sectionEntrypointDryRun(r *reporter, repoRoot string, repoRootOK bool, workspace string, merged *jsonx.OrderedMap) {
	r.section("Entrypoint Dry-Run")
	if !repoRootOK {
		r.fail("Entrypoint preflight failed", "repo root resolution failed")
		r.blank()
		return
	}
	if err := o.entrypointPreflight(r, repoRoot, workspace, merged); err != "" {
		r.fail("Entrypoint preflight failed", err)
	} else {
		r.ok("Generated Copilot/Gemini/Claude jail config in a temp home")
	}
	r.blank()
}

// sectionImageBuild runs the Image Build block. Returns imageBuildSkipped.
func (o *Options) sectionImageBuild(r *reporter, merged *jsonx.OrderedMap, repoRoot string, repoRootOK, isNativeRuntime bool) bool {
	r.section("Image Build")
	imageBuildSkipped := false
	if isNativeRuntime {
		r.ok("Not applicable — native macOS backend builds no Linux image")
	} else if o.Build {
		if !repoRootOK {
			r.fail("Skipped nix build", "repo root resolution failed")
		} else {
			extra := config.EffectivePackages(merged)
			var extraArg []any
			if len(extra) > 0 {
				extraArg = extra
			}
			buildable := o.preflightBuilderNeeds(r, repoRoot, extraArg)
			if !buildable {
				imageBuildSkipped = true
			} else {
				storePath, tail := o.BuildImage(repoRoot, extraArg)
				if storePath == "" {
					title, note := nixdiag.DiagnoseNixBuildFailure(tail, o.IsMacOS, linuxBuilderRemedy())
					r.fail(title, note)
				} else {
					r.ok("nix build succeeded: " + storePath)
				}
			}
		}
	} else {
		r.warn("Skipped nix build (--no-build)", "")
	}
	r.blank()
	return imageBuildSkipped
}

// sectionContainerImage runs the Container Image block.
func (o *Options) sectionContainerImage(r *reporter, detectedRuntime, notLoadedHint string) {
	r.section("Container Image")
	if o.inJail() {
		r.ok("Inside jail — image check skipped (managed by host)")
		r.blank()
		return
	}
	checkImage := image.JailImage(detectedRuntime)
	if detectedRuntime == "container" {
		res := o.Exec([]string{"container", "image", "inspect", checkImage}, "", nil, 10*time.Second)
		if !res.Ran || res.Timeout {
			r.warn("Could not check image: probe failed", "")
		} else if res.RC == 0 {
			r.ok("Image loaded: " + checkImage)
		} else {
			r.warn("Image '"+checkImage+"' not loaded", notLoadedHint)
		}
	} else {
		res := o.Exec([]string{detectedRuntime, "images", checkImage, "--format", "{{.Repository}}:{{.Tag}} ({{.Size}})"}, "", nil, 10*time.Second)
		if !res.Ran || res.Timeout {
			r.warn("Could not check image: probe failed", "")
		} else {
			images := strings.TrimSpace(res.Stdout)
			if images != "" {
				r.ok("Image loaded: " + firstLine(images))
			} else {
				r.warn("Image '"+checkImage+"' not loaded", notLoadedHint)
			}
		}
	}
	r.blank()
}

// sectionRunningJails runs the Running Jails block (with stuck detection and
// the orphan-cleanup prompt).
func (o *Options) sectionRunningJails(r *reporter, detectedRuntime string) {
	r.section("Running Jails")
	type row struct{ name, runningFor string }
	var containers []row

	if detectedRuntime == "container" {
		res := o.Exec([]string{"container", "ls", "--filter", "name=yolo-"}, "", nil, 5*time.Second)
		if !res.Ran || res.Timeout {
			r.warn("Could not check running containers", "")
			r.blank()
			return
		}
		lines := strings.Split(strings.TrimSpace(res.Stdout), "\n")
		if len(lines) > 1 {
			for _, line := range lines[1:] {
				parts := strings.Fields(line)
				if len(parts) > 0 && strings.HasPrefix(parts[0], "yolo-") {
					containers = append(containers, row{parts[0], ""})
				}
			}
		}
	} else {
		res := o.Exec([]string{detectedRuntime, "ps", "--filter", "name=^yolo-", "--format", "{{.Names}}\t{{.RunningFor}}"}, "", nil, 5*time.Second)
		if !res.Ran || res.Timeout {
			r.warn("Could not check running containers", "")
			r.blank()
			return
		}
		for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "\t", 2)
			rf := ""
			if len(parts) > 1 {
				rf = parts[1]
			}
			containers = append(containers, row{parts[0], rf})
		}
	}

	if len(containers) == 0 {
		r.ok("No jails currently running")
		r.blank()
		return
	}

	type orphan struct{ name, runningFor, workspace, reason string }
	var orphans []orphan
	r.ok(pluralJails(len(containers)))
	for _, c := range containers {
		cws := o.getContainerWorkspace(c.name, detectedRuntime)
		wsExists := true
		if cws != "unknown" {
			wsExists = isDir(cws)
		}
		reason := ""
		if !wsExists {
			reason = "workspace gone"
		} else {
			reason = o.checkContainerStuck(c.name, detectedRuntime)
		}
		marker := ""
		if reason != "" {
			marker = " " + r.style("("+reason+")", ansiRed)
			orphans = append(orphans, orphan{c.name, c.runningFor, cws, reason})
		}
		r.line("    " + c.name + " -> " + cws + marker)
	}
	if len(orphans) > 0 {
		r.warn(pluralOrphans(len(orphans)),
			"These containers are stuck or have lost their workspace")
		r.blank()
		if o.orphanCleanupPrompt(r, len(orphans)) {
			for _, orph := range orphans {
				_ = o.Exec([]string{detectedRuntime, "rm", "-f", orph.name}, "", nil, 30*time.Second)
				cleanupTracking(orph.name)
				r.line("    " + r.style("Stopped "+orph.name, ansiGreen))
			}
		}
	}
	r.blank()
}

// sectionInlineLoopholes runs the "Loopholes — inline daemons" block.
func (o *Options) sectionInlineLoopholes(r *reporter, merged *jsonx.OrderedMap) {
	loopholesV, _ := merged.Get("loopholes")
	loopholesCfg, ok := loopholesV.(*jsonx.OrderedMap)
	if !ok || loopholesCfg.Len() == 0 {
		return
	}
	r.section("Loopholes — inline daemons")
	if o.inJail() {
		r.ok("Inside jail — exec checks skipped (host paths aren't reachable here)")
		r.blank()
		return
	}
	for _, name := range loopholesCfg.Keys() {
		if name == paths.BuiltinCgroupLoopholeName {
			continue
		}
		specV, _ := loopholesCfg.Get(name)
		spec, ok := specV.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		cmdV, _ := spec.Get("command")
		cmd, ok := cmdV.([]any)
		if !ok || len(cmd) == 0 {
			continue
		}
		exeArg := asString(cmd[0])
		if exeArg == "" {
			// A non-string cmd[0]; rare/never for real config. Render it and
			// skip resolution (would be a nonsense path).
			exeArg = pyStrOf(cmd[0])
		}
		exePath := expandUserPath(exeArg)
		if filepath.IsAbs(exePath) {
			if isExecutableFile(exePath) {
				r.ok("loopholes." + name + ": " + exePath)
			} else {
				r.fail("loopholes."+name+": command not found or not executable: "+exePath, "")
			}
		} else {
			if resolved, ok := o.LookPath(exeArg); ok {
				r.ok("loopholes." + name + ": " + resolved)
			} else {
				r.fail("loopholes."+name+": command not found on PATH: "+exeArg, "")
			}
		}
	}
	r.blank()
}

// entrypointPreflight runs the Go entrypoint generators in a temp home and
// returns "" on success or the failure detail.
func (o *Options) entrypointPreflight(r *reporter, repoRoot, workspace string, merged *jsonx.OrderedMap) string {
	return o.runEntrypointPreflight(r, repoRoot, workspace, merged)
}

// pluralJails / pluralOrphans render the count phrases
// ("N jail(s) running", "N orphaned jail(s)").
func pluralJails(n int) string   { return itoa(n) + " jail(s) running" }
func pluralOrphans(n int) string { return itoa(n) + " orphaned jail(s)" }

// cleanupTracking removes a container's tracking file.
func cleanupTracking(name string) {
	cleanupTrackingFn(name)
}
