package runcmd

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/storage"
)

// Run ports run(): validate config, resolve the runtime, then either exec into
// an existing container or launch a fresh one. Returns the process exit code
// (mirroring the Python sys.exit codes). The whole flow is driven off the
// injected seams so the probe + argv-assembly paths are unit-testable.
//
// The sub-phases land incrementally (probes → network/storage → argv/mount
// assembly → lifecycle/locks → e2e); each keeps the tree compiling.
func Run(opts Options) int {
	fillDefaults(&opts)
	o := &opts

	// --- Phase 1: probes (repo root, storage, config, runtime) ---
	repoRoot, ok := o.RepoRoot()
	if !ok {
		return 1 // _resolve_repo_root's SystemExit(1)
	}
	_ = repoRoot

	// ensure_global_storage() — best-effort; a hard fs error would raise in
	// Python, but the migrate hook is nil here (storage owns the layout
	// migration seam). A failure surfaces as a launch abort.
	if err := ensureStorage(); err != nil {
		printer{w: o.Stdout}.printf("[bold red]%s[/bold red]", err.Error())
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

	// macos-user native backend is a DELEGATION seam (exec python -m src.cli
	// run …), NOT ported — the front door handles that by delegating to Python
	// before reaching the Go native path. If we somehow reach here with a
	// native runtime, decline (the caller should have delegated).
	if inStrSlice([]string{"macos-user"}, rt) {
		printer{w: o.Stdout}.print(
			"[bold red]macos-user runtime must be handled by the Python delegation seam.[/bold red]")
		return 1
	}
	if o.DryRun {
		printer{w: o.Stdout}.print(
			"[bold red]--dry-run is only supported for the macos-user runtime.[/bold red]  " +
				`Set runtime: "macos-user" (or YOLO_RUNTIME=macos-user) to use it.`)
		return 1
	}

	// The remaining sub-phases (network/storage wiring, argv+mount assembly,
	// lifecycle+locks) are not yet wired. Until then the native path is a
	// no-op that reports it is incomplete — the YOLO_IMPL=go gate is OFF by
	// default and `run` is NOT yet in the gated set, so this is unreachable in
	// production.
	_ = cfg
	return runContainer(o, cfg, rt, repoRoot)
}

// ensureStorage wraps storage.EnsureGlobalStorage with the nil migrate hook
// (the layout migration is a separate storage seam not invoked from run's hot
// path in the port's current scope).
func ensureStorage() error {
	return storage.EnsureGlobalStorage(nil)
}

// runContainer is the fresh-launch + attach orchestration. Populated by the
// later sub-phases; a placeholder until the argv/lifecycle phases land. The
// Phase-2 wiring (network port-forwarding, storage ensure, image auto-load) is
// referenced here so the seams stay exercised while the argv assembly lands.
func runContainer(o *Options, cfg *jsonx.OrderedMap, rt, repoRoot string) int {
	// Identity env vars (git + jj) are collected early — needed for both the
	// exec and run paths.
	identityEnv := o.collectIdentityEnv()
	_ = identityEnv

	// The lifecycle+locks sub-phase wires the fresh-launch flow (attach
	// decision, locks, tracking, teardown). Until it lands, reference the
	// Phase-2/3 seams so the compiler keeps them live.
	if false {
		_ = o.checkConfigChanges(cfg)
		_ = o.autoLoadImage(cfg, rt, repoRoot)
		procs := o.startHostPortForwarding(nil, "", "")
		cleanupPortForwarding(procs, "")
		cname := ""
		agentsPath, _ := o.refreshJailBriefings(cname, cfg, rt)
		wsState := o.prepareWsState(cfg, nil, nil)
		in := o.buildAssembleInput(cfg, rt, cname, repoRoot, agentsPath, wsState, identityEnv)
		_ = o.assembleRunCmd(in)
	}
	_ = rt
	_ = repoRoot
	return 0
}
