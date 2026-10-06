package macosuser

import (
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/claudeview"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/durable"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/provision"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

// RunPlan is the fully-resolved, ordered artifacts + commands for one session.
// real gate rather than a pretty-printer.
type RunPlan struct {
	Workspace string
	// Cname is the WORKSPACE's container name: the launch lock and the staged trees are keyed
	// by it, and shared by every session of the workspace. SessionID is this session's own id
	// (SessionPlaceholder in a plan no launch minted one for), and SessionKey(Cname, SessionID)
	// names every root-owned file only this session writes: its profile, its env and daemons env
	// files and its CA files (sessionfiles.go).
	Cname       string
	SessionID   string
	ProfilePath string
	Seatbelt    string
	// ProfileRemoveCommands remove the session's Seatbelt profile when the session ends.
	ProfileRemoveCommands [][]string
	// StagedDir is the root-owned state dir; StagedYolo is the staged yolo
	// binary the sandbox self-execs. StageCommands stage that binary
	// (fresh-inode copy).
	StagedDir     string
	StagedYolo    string
	StageCommands [][]string
	// PackRoot is the root-owned staged pack tree this session's bootstrap renders
	// from (YOLO_PACK_ROOT in BootstrapArgv), or "" when the launch staged no packs.
	PackRoot string
	// CtxRoot is the root-owned staged CONTEXT tree this session's bootstrap reads host
	// bytes out of (YOLO_CTX_ROOT in BootstrapArgv), or "" when the host CLI composed
	// none. Absence is the honest way to say "this launch carried no host bytes"; it is
	// also what makes the host-layer report say `unsupported`, so the two can never
	// disagree about whether this backend delivered.
	CtxRoot string
	// ContextDir is what $YOLO_CONTEXT_DIR names for the agent and the bootstrap: the same
	// root-owned tree as CtxRoot, but set on EVERY launch and staged every launch, empty when
	// nothing was composed (docs/design/context-mounts.md CX-D4).
	ContextDir string
	// ContextLinks are the context mounts this launch delivers, each a root-owned link in
	// ContextDir with the profile deciding access to its source (ctxlinks.go); ContextPreflight
	// is the DAC preflight the launch asks before the sandbox starts, and ContextOccupied the
	// context-dir paths yolo's own staging uses, which no link may touch. All empty with no
	// context mount.
	ContextLinks     []ContextLink
	ContextPreflight []ContextProbe
	ContextOccupied  []string
	// ContextCopies are the pack `mount` files this launch COPIED into ContextDir
	// (HostContext.Copied), for the dry run to name beside the links: without them a launch
	// whose only context mount is a copy printed "no context mounts" while its briefing listed
	// one.
	ContextCopies []ContextLink
	// CacheRelocations are the user's `cache_relocations` this launch delivers
	// (HostContext.Relocations), each a link the bootstrap lays at ~/.cache/<subdir> to its
	// resolved target, which the profile opens read and write (ctxlinks.go).
	// CacheRelocationPreflight is the DAC preflight the launch asks of each target before the
	// nix build, and CacheRelocationProbes the write-and-remove each gets under this session's
	// profile once it is installed. All empty with no relocation.
	CacheRelocations         []CacheRelocation
	CacheRelocationPreflight []CacheRelocationProbe
	CacheRelocationProbes    []CacheRelocationProbe
	BootstrapArgv            []string
	// ProvisionArgv is the CONFINED provisioning stage, run between the bootstrap and
	// the agent — nil when this config gives it nothing to do (ProvisionNeeded), which
	// is what makes `yolo -- bash` in a tool-less workspace pay nothing for it.
	// ProvisionScriptPath is the generated script that argv execs; both are "" together.
	ProvisionArgv       []string
	ProvisionScriptPath string
	// ProvisionFloors is the host's answer to whether a declared Node floor starts the stage
	// (FloorStage, AR-L3), carried so the dry run can say why the stage runs or is skipped.
	ProvisionFloors FloorStage
	LaunchArgv      []string
	// JailDaemonArgv is the CONFINED supervisor (jaildaemon.go): `yolo-jaild supervise`
	// under the session's Seatbelt profile, reading DaemonEnvFile. nil when the launch
	// handed this backend no daemon to run, which is the common case and costs nothing.
	// JailDaemonNames is what its payload names, sorted.
	JailDaemonArgv  []string
	JailDaemonNames []string
	// SupervisorLog is the file JailDaemonArgv sends the supervisor's own stdout and stderr
	// to (SupervisorLogPath), and the one the launch reads its readiness line from. "" with
	// no daemon.
	SupervisorLog string
	// GuestBinSource is where the darwin guest binaries are copied from ("" with no daemon
	// and no guest client's endpoint), and StageCommands carries the copies into GuestBinDir.
	// GuestClients names the clients (GuestClients' binaries) whose endpoint the session env
	// carries, each of which is then staged with the set, so the agent can run it.
	GuestBinSource string
	GuestClients   []string
	// ProbeArgv is the CONFINED host-service witness (serviceprobe.go): `yolo internal
	// probe-services` under the session's Seatbelt profile, as the sandbox account, reading
	// the session env file, so it dials each published endpoint exactly as the agent's clients
	// will. nil when the session env carries no YOLO_SERVICE_*_ENDPOINT, which is every launch
	// that started no host service, and then it costs nothing.
	ProbeArgv []string
	// DaemonEnvFile is the supervisor's own env file, beside EnvFile and delivered the same
	// way (root-owned 0600 in the 0700 env dir, one `user:` read ACE for the sandbox
	// account); DaemonEnvFileContent is what to write. Both "" with no daemon.
	// DaemonEnvRemoveCommands sweep it when the session ends.
	DaemonEnvFile           string
	DaemonEnvFileContent    string
	DaemonEnvRemoveCommands [][]string
	// EnvFile is the per-session, root-owned 0600 file carrying everything this launch
	// COMPOSED — git identity, TERM, the profile/provider channel, the hydrated
	// env_sources. The three sandboxed argvs above name it and read it; none of them
	// carries its values (envfile.go states why, and PlanInvariants checks it).
	// EnvFileContent is what to write into it; the two are "" together.
	//
	// EnvFileCommands prepare the 0700 directory and MUST run BEFORE the write;
	// EnvFileGrantCommands add the sandbox account's read ACE and MUST run after;
	// EnvFileRemoveCommands sweep it when the session ends.
	EnvFile               string
	EnvFileContent        string
	EnvFileCommands       [][]string
	EnvFileGrantCommands  [][]string
	EnvFileRemoveCommands [][]string
	// CATrust is the TLS trust this launch composed (cabundle.go): the zero value composed none.
	// CABundleFile and CAExtrasFile are the session's two CA files beside the env file, with what
	// to write into each, each "" when no variable names it: the variables name the tool
	// profile's own bundle, the launch's env layers named a bundle of their own, or nothing was
	// composed. EnvFileRemoveCommands sweep both. CAFollows is the bundle variable those layers
	// set, which the other four then name too (applyCATrust), or "".
	CATrust            CATrust
	CABundleFile       string
	CABundleContent    string
	CAExtrasFile       string
	CAExtrasContent    string
	CAFollows          string
	GitIdentity        *jsonx.OrderedMap
	OffendingHome      string // "" when on neutral ground
	OffendingHomeSet   bool   // true when a home contains the workspace
	DarwinPathPrefix   []string
	DarwinEnv          *jsonx.OrderedMap
	DarwinSkipped      []string
	DarwinMaterialized bool
	// NixClientDir is the host nix client's store bin dir when this launch put one on the
	// sandbox PATH (it is also the last entry of DarwinPathPrefix), "" when it did not.
	NixClientDir string
	// IOPriority is the declared resources.io priority, which the launcher sets on itself as
	// a process disk policy before the bootstrap, so every process of the session inherits it
	// (orchestrator.go, applyDiskIOPolicy; docs/design/io-priority.md §5.5). Normal sets
	// nothing.
	IOPriority ioprio.Priority
	// SessionGuard is the declared resources.memory, which the launch argv enforces by
	// sampling (sessionguard.go); the zero value runs no guard and leaves the argv untouched.
	SessionGuard SessionGuard
	// CooperativeCPUs is ceil(resources.cpus), at least 1, when cpus is declared, and 0 when
	// it is not: the DECLARED count the parallelism variables (CooperativeCPUVars) default
	// from in the session env file, below any value the user's own env layers set. The file
	// holds the values themselves, which buildPlan caps at the Mac's own CPU count
	// (cooperativeCPUCount), so PrintPlan prints this number beside the values the file holds.
	CooperativeCPUs int
	// HomeReadonly is what Seatbelt was told to write-protect in the sandbox home: every
	// staged skills dir and briefing this launch delivered, at the PHYSICAL path the kernel
	// will see, and the chain above each (ResolveHomeReadonly). Empty when the launch
	// delivered no content. Carried so a reader can check the profile against the
	// delivery rather than re-deriving one from the other.
	HomeReadonly HomeReadonly
	// CapturesDir is the root-owned staged copy of the install-capture store this launch's
	// launchers materialize from (StagedCapturesRoot, what entrypoint.CapturesDirEnv names to the
	// bootstrap), "" when the launch stages no capture; Captures are the entries it stages there
	// (HostContext.Captures, less any a root script may not be handed), for the dry run and for
	// PlanInvariants, which checks each is staged.
	CapturesDir string
	Captures    []CaptureEntry
	// CaptureStageCommands bring that store up to date (StageCaptureCommands), run as root after
	// StageCommands and BEST-EFFORT, which is why they are not among them: a failure warns, names
	// the program that downloads instead, and the launch goes on (orchestrator.go's
	// stageCaptures). Empty when the launch stages no capture.
	CaptureStageCommands [][]string
}

// HostContext is what the HOST CLI composed for this launch's `/ctx` delivery: the tree
// of host bytes, and the record of what went into it. It is the macos-user answer to
// "the bytes cross on a /ctx mount and this backend has no mounts" — the mechanism is a
// COPY (docs/design/declaration-parity.md DP-L1 / §6.1, ruled by OQ-DP4).
//
// ⚠ EVERY FIELD IS COMPOSED BY THE CALLER AND NEVER BY THIS PACKAGE, and that is a
// credential-boundary constraint rather than a layering preference. Filling it means
// reading the invoking user's own config (config.LoadHostFiles reads ~/.config/yolo-jail
// DIRECTLY, which is what makes a source-bearing entry user-scope-only) and stat'ing
// paths in the invoking user's home. The plan builder is PURE — the dry-run plan must
// touch no disk — so the read lives in the host CLI's run pipeline
// (internal/cli/run/macosctxtree.go) and only its RESULT crosses here. OQ-DP4's ledger
// row states this in as many words and the ruling does not override it.
//
// The zero value is a launch that carried no host bytes, which is exactly the state
// every macos-user launch was in before DP-L1: no tree to stage, no YOLO_CTX_ROOT, and
// a host-layer report of `unsupported`.
type HostContext struct {
	// Tree is the host-side root the caller composed, laid out at the /ctx-relative
	// paths the jail reads (`host-<staged slug>/<basename>` for a pack `reads-host`
	// grant, `host-user/<slug>` for a source-bearing `host_files` entry, file or directory,
	// `<into>` for a pack's single-file `mount`, `host-user/_global-gitignore` for the
	// host's global gitignore). "" means the caller composed nothing.
	//
	// It crosses as a TREE rather than as a mapping for macoshomeoverlay.go's reason:
	// the container path's mapping lives in its mount list, and re-sending it as data
	// would put one mapping in two implementations. Laying it out by DESTINATION makes
	// delivery one `cp -R` and one env var — the paths ARE the manifest.
	Tree string
	// Delivered is the /ctx destination (packload.CtxPath) of every pack `reads-host`
	// grant whose bytes are in Tree. It becomes the launcher's half of the jail's
	// FAIL-CLOSED host-layer read (packload.HostLayerReport): the jail cannot tell "the
	// user has no such file" from "it did not arrive", so the launcher says which.
	//
	// It is the caller's list rather than a walk of Tree on purpose. A walk would make
	// the jail's check tautological — it would re-derive the same answer from the same
	// bytes — and the bug that read is for is precisely a host side that wrote the
	// right file at the WRONG path (measured 2026-09-05, a pack whose staged directory
	// name was escaped).
	Delivered []string
	// Rendered is the subset of Delivered whose bytes are YOLO'S OWN RENDER: a `readsHost`
	// surface a writing `yolo host apply` has already rendered into this home, which the
	// launcher reads off the host provenance mark (run.hostLayerIsRender →
	// entrypoint.HostSurfaceRendered). It crosses as entrypoint.HostLayerWire's `rendered`
	// label, and the jail keeps such a copy as a BASELINE rather than composing it as the
	// user's layer ([OQ-CR6], docs/reference/config-target-resolution.md) — so a key or an
	// entry yolo wrote into the host file does not come back into the sandbox as the user's.
	//
	// The caller's to compute, for Delivered's reason and one more: the mark lives in the
	// invoking user's state dir, and this package's plan builder reads no disk. The container
	// launcher computes the same label with the same call (hostFileArgs), which is what keeps
	// the two backends' readings of one home identical.
	Rendered []string
	// HostFiles are the user's SOURCE-BEARING `host_files` entries this launch RESOLVED —
	// additive to the source-less ones the plan builder reads from the merged config,
	// which is the only half a pure function may see.
	//
	// ⚠ RESOLVED, not "delivered", and the difference is deliberate. An entry whose host
	// file does not exist yet belongs here: the destination then renders from its
	// `defaults`/`content` layers, which is what every other backend does (the container
	// emits the entry and skips only the bind). Only the BYTES are conditional on the
	// source existing; the declaration crosses either way.
	//
	// Directory-shaped entries ARE here since 2026-10-05, on the file entries' rule: the host
	// CLI copies the tree into Tree confined to its source, and caps the copy (run's
	// macosctxtree.go), because a container's directory host_files is a full copy at boot too
	// (entrypoint.stageHostFile), so DP-D15's size reason never applied to this key. They used
	// to be left out and warned about.
	HostFiles []config.HostFileEntry
	// Copied is every selected pack's single-FILE `mount` the host CLI copied into Tree at its
	// /ctx destination (Dest, packload.MountCtxPath), rather than linked: a pack grant names a
	// path in the user's home, which a link cannot serve here (OQ-CX7), and a copy can. Each
	// keeps its source and pack for the dry run, which names what it copied. Separate from
	// Delivered, which is the host-layer report's subject and nothing else; a context link may
	// land at, inside or around neither (ContextOccupied).
	Copied []ContextLink
	// GlobalGitignore is the /ctx destination of the host's global gitignore
	// (paths.ContextGlobalGitignore) when the host CLI copied it into Tree, "" when there is
	// none. The bootstrap is told its staged path (YOLO_GLOBAL_GITIGNORE) and points the
	// sandbox's core.excludesFile at it, as the container launch points the jail's at its
	// read-only bind.
	GlobalGitignore string
	// Links are the CONTEXT MOUNTS this launch delivers — config `mounts` elements and pack
	// `mount` grants — each sited by the caller with SiteContextLinks against its resolved
	// source (docs/design/context-mounts.md §3). Not bytes: each becomes a root-owned link in
	// the context dir, so nothing is copied (DP-D15's size argument stands) and the bytes the
	// agent reads are live. The caller's for the same reason as the rest of this struct:
	// resolving a source is a read of the invoking user's filesystem.
	Links []ContextLink
	// Relocations are the user's `cache_relocations` this launch delivers, read from the USER
	// config alone (config.LoadCacheRelocations), their targets resolved and, where absent,
	// created and granted the sandbox's access by the host CLI, and sited with
	// SiteCacheRelocations (internal/cli/run's macosuserrelocations.go). The caller's for the
	// same reason as the rest of this struct: reading the user's config and making a directory
	// in their filesystem are host acts, and the plan builder is pure.
	Relocations []CacheRelocation
	// Captures are the install-capture store entries this launch stages for its launchers
	// (CaptureEntry; docs/plans/install-capture.md hand-off H4): for each selected pack's
	// `via: "installer"` program, the entry the materialize path's own resolver chooses at this
	// backend's platform, darwin. The caller's because the store is in the invoking user's state
	// dir. Empty stages nothing and names no store to the bootstrap, so every launcher downloads,
	// as before H4.
	//
	// CapturesKept are the keys of the store's other current entries at that platform — programs
	// this launch does not select and another workspace may — whose staged copies the launch
	// leaves in place (StageCaptureCommands).
	Captures     []CaptureEntry
	CapturesKept []string
}

// GlobalGitignoreEnv names the global gitignore's staged path to the bootstrap, whose git step
// (entrypoint's configureGit) reads it under this name; TestTheBootstrapAppliesTheStagedGlobalGitignore
// runs the real bootstrap with it, so the two spellings cannot drift apart unnoticed.
const GlobalGitignoreEnv = "YOLO_GLOBAL_GITIGNORE"

// Darwin carries the already-materialized native `packages:` result threaded
// into a RunPlan
// plan builder stays pure — the nix build happened in the caller). A nil
// *Darwin means "not materialized".
type Darwin struct {
	PathPrefix []string
	Env        *jsonx.OrderedMap
	Skipped    []string
	// System is the nix system double the materialization actually resolved
	// against (e.g. "aarch64-darwin"). Carried on the RESULT rather than read
	// from a constant here so the skip message names the real target: on an
	// Intel Mac a skip is an x86_64-darwin fact, and this package must not
	// acquire a darwinpkg import to say so — the whole point of the injected
	// MaterializeDarwin seam is that macosuser stays free of that dependency.
	System string
	// ProfilePath is the buildEnv store out path (PathPrefix is <it>/bin). The
	// GC-rooted closure the agent's tools come from.
	ProfilePath string
	// Nix is the host's nix client as the sandbox can use it (hostnix.go). A set BinDir
	// joins the sandbox PATH AFTER PathPrefix — so a `packages:` nix still outranks it —
	// and brings NIX_REMOTE/NIX_CONFIG with it. The zero value delivers no nix.
	Nix HostNix
}

// darwinSystemLabel is the system double for a skip message, falling back to the
// generic word when the materializer did not report one — a message reading "no
// build" is honest where one reading "no aarch64-darwin build" on an Intel Mac is
// not, so an unset System degrades rather than guessing.
func darwinSystemLabel(d *Darwin) string {
	if d == nil || d.System == "" {
		return "native"
	}
	return d.System
}

// DarwinBootstrapArgv returns the self-exec bootstrap argv (J2 §3): run the
// staged yolo binary AS the sandbox user via `sudo --user=<sb> /usr/bin/env -i
// K=V… <stagedYolo> internal darwin-bootstrap`.
//
// The env is baked onto the argv the same way LaunchArgv bakes the launch env
// (env -i K=V…; secrets normally ride ${VAR} placeholders).
// HOME/JAIL_HOME point the entrypoint generators at the sandbox
// home; the generator contract (git identity + YOLO_*) and the three
// YOLO_DARWIN_* extras (workspace, macos-log, login-path) ride verbatim. No
// --set-home: the subcommand self-sets HOME/JAIL_HOME, and env -i controls the
// environment precisely.
func DarwinBootstrapArgv(stagedYolo, home string, bootstrapEnv *jsonx.OrderedMap, user string) []string {
	if user == "" {
		user = SandboxUser
	}
	if home == "" {
		home = SandboxHome()
	}
	protected := map[string]struct{}{"HOME": {}, "JAIL_HOME": {}}
	envPairs := []string{
		"HOME=" + home,
		"JAIL_HOME=" + home,
	}
	if bootstrapEnv != nil {
		for _, k := range bootstrapEnv.Keys() {
			if _, ok := protected[k]; ok {
				continue
			}
			v, _ := bootstrapEnv.Get(k)
			envPairs = append(envPairs, k+"="+asStr(v))
		}
	}
	out := []string{
		"sudo",
		"--user=" + user,
		"/usr/bin/env",
		"-i",
	}
	out = append(out, envPairs...)
	out = append(out, stagedYolo, "internal", "darwin-bootstrap")
	return out
}

// BuildRunPlan assembles the full RunPlan (pure — no shelling out). `config` is
// the loaded jail config; `sandboxEnv` is the fully-resolved launch env;
// `selfExe` is the running yolo binary (os.Executable()) staged for the sandbox
// to self-exec as the bootstrap; `hostPackRoot` is the host-side staged pack tree
// the run pipeline produced before dispatching here (""=no packs); `hostHomeOverlay`
// is the host-side composed CONTENT — the tree of skills and briefings, already laid out
// at their home-relative destinations (Tree ""=nothing to deliver), and the destinations
// it holds, which the Seatbelt profile write-protects (HomeOverlay); `hostCtx` is the host-side
// composed CONTEXT tree and the record of what the host CLI put in it (HostContext, and
// see it for why this package may not compose one itself); `blockedTools` are the
// selected packs' own blocked-tool declarations, merged with the config's security
// section (core blocks nothing by default). `darwin` may be nil.
func BuildRunPlan(workspace string, cfg *jsonx.OrderedMap, agents, agentArgv []string, selfExe, hostPackRoot string, hostHomeOverlay HomeOverlay, hostCtx HostContext, sandboxEnv *jsonx.OrderedMap, darwin *Darwin, blockedTools []packload.BlockedTool) RunPlan {
	return BuildRunPlanWithDaemons(workspace, cfg, agents, agentArgv, selfExe, hostPackRoot,
		hostHomeOverlay, hostCtx, sandboxEnv, darwin, blockedTools, JailDaemons{}, FloorStage{},
		PlanSession{})
}

// PlanSession is what the orchestrator minted and composed for ONE session, beside the
// workspace's inputs: its id (sessionfiles.go; "" is SessionPlaceholder) and its TLS trust
// (cabundle.go; the zero value composes none).
type PlanSession struct {
	ID      string
	CATrust CATrust
}

// sandboxPathPrefix is the store bin dirs this launch puts on the sandbox PATH, in order: the
// materialized floor and `packages:` (darwin.PathPrefix), then the host's nix client when the
// launch delivers one (hostnix.go), after them so a declared `packages:` nix still wins. That one
// list reaches the launch PATH, the provisioning stage's PATH and the bootstrap's
// $YOLO_DARWIN_LOGIN_PATH (PlanInvariants checks the first and the last), and the orchestrator's
// Node floor check reads the same PATH through it (floorStageFor). Empty for a nil darwin.
func sandboxPathPrefix(darwin *Darwin) []string {
	prefix := []string{}
	if darwin == nil {
		return prefix
	}
	prefix = append(prefix, darwin.PathPrefix...)
	if darwin.Nix.BinDir != "" {
		prefix = append(prefix, darwin.Nix.BinDir)
	}
	return prefix
}

// BuildRunPlanWithDaemons is BuildRunPlan plus the jail daemons this launch runs in the guest
// (jaildaemon.go), the host's answer to whether a declared Node floor starts the provisioning
// stage (FloorStage, AR-L3), and what the launch minted and composed for this one session
// (PlanSession). The orchestrator's buildPlan calls this one; BuildRunPlan is the plan of a
// launch that runs no daemon, whose floors, if any, the host showed met, and that no launch
// minted a session for.
func BuildRunPlanWithDaemons(workspace string, cfg *jsonx.OrderedMap, agents, agentArgv []string, selfExe, hostPackRoot string, hostHomeOverlay HomeOverlay, hostCtx HostContext, sandboxEnv *jsonx.OrderedMap, darwin *Darwin, blockedTools []packload.BlockedTool, jailDaemons JailDaemons, floors FloorStage, session PlanSession) RunPlan {
	// SYMLINK-RESOLVED ONCE, HERE, BECAUSE THE KERNEL RESOLVES BEFORE THE POLICY IS CONSULTED.
	// Measured on hardware 2026-09-13 (declaration-parity.md §6.1's probe 2): a profile denying
	// `(subpath "/tmp")` does not stop `touch /tmp/canary`, while one denying
	// `(subpath "/private/tmp")` does. So an SBPL rule naming an unresolved path matches
	// NOTHING, and `SeatbeltProfile` used to be handed `workspace` raw while `YOLO_HOST_DIR`
	// (:341) and `MISE_TRUSTED_CONFIG_PATHS` (orchestrator.go) both resolved it. A workspace
	// reached through a symlink therefore got a profile whose workspace rules were dead — no
	// writes, no reads, fail-closed and confusing.
	//
	// ⚠ AND THE SAME LINE CLOSES A POLICY BYPASS, which is why it is one assignment rather than
	// a fix at the profile call site. `HomeContaining` below decides the neutral-ground refusal
	// (DP-D15: the agent shares only neutral ground, never a path inside your home), and it is
	// only as good as the spelling it is given: `/Users/Shared/yolo/link` → `/Users/matt/proj`
	// passed it, measured through a real `--dry-run`. The dead profile was all that stood
	// between that and a live grant into the invoking user's home, so resolving for the profile
	// ALONE would have unmasked it. Both consumers read this one value.
	//
	// Go's os.Getwd honours $PWD when it stats to the same inode, so the launcher really does
	// receive a shell's logical spelling — this is reachable from an ordinary `cd`, not only
	// from an argument somebody constructed.
	workspace = resolvePathAbs(workspace)
	darwinPrefix := sandboxPathPrefix(darwin)
	darwinEnv := jsonx.NewOrderedMap()
	darwinSkipped := []string{}
	if darwin != nil {
		if darwin.Env != nil {
			for _, k := range darwin.Env.Keys() {
				v, _ := darwin.Env.Get(k)
				darwinEnv.Set(k, v)
			}
		}
		darwinSkipped = append([]string{}, darwin.Skipped...)
	}
	// Merge non-PATH darwin build vars into the launch env (the store PATH rides
	// the separate path_prefix channel); darwin vars win on conflict.
	if darwinEnv.Len() > 0 {
		merged := jsonx.NewOrderedMap()
		if sandboxEnv != nil {
			for _, k := range sandboxEnv.Keys() {
				v, _ := sandboxEnv.Get(k)
				merged.Set(k, v)
			}
		}
		for _, k := range darwinEnv.Keys() {
			v, _ := darwinEnv.Get(k)
			merged.Set(k, v)
		}
		sandboxEnv = merged
	}
	// THE HOST'S nix CLIENT (hostnix.go): its bin dir is already the last entry of darwinPrefix
	// (sandboxPathPrefix); its NIX_REMOTE and NIX_CONFIG ride the launch env.
	nixClientDir := ""
	if darwin != nil && darwin.Nix.BinDir != "" {
		nixClientDir = darwin.Nix.BinDir
		sandboxEnv = withHostNixEnv(sandboxEnv)
	}

	cname := cnameFor(workspace)
	// THE SESSION'S OWN NAMES (sessionfiles.go): every root-owned file only this session writes
	// is named by its key, never by the workspace's cname, which every terminal in the workspace
	// shares.
	sessionID := session.ID
	if sessionID == "" {
		sessionID = SessionPlaceholder
	}
	sessionKey := SessionKey(cname, sessionID)
	profilePath := SessionProfilePath(sessionKey, "")

	// Git identity = the sandbox-env keys prefixed YOLO_GIT.
	gitIdentity := jsonx.NewOrderedMap()
	if sandboxEnv != nil {
		for _, k := range sandboxEnv.Keys() {
			if strings.HasPrefix(k, "YOLO_GIT") {
				v, _ := sandboxEnv.Get(k)
				gitIdentity.Set(k, v)
			}
		}
	}

	// THE TWO STAGED TREES the bootstrap renders from, resolved here from their host-side
	// counterparts and handed to buildBootstrapEnv, which turns each into the env var that
	// tells the bootstrap it exists. Each is named only when the launch actually staged
	// one, so a launch that staged nothing says so by ABSENCE rather than by naming a
	// directory that is not there — see buildBootstrapEnv for what each does, and
	// StageCommands below for the copies that put them there.
	packRoot := ""
	if hostPackRoot != "" {
		packRoot = StagedPackRoot(cname, "")
	}
	homeOverlay := ""
	// WHAT THAT TREE DELIVERS IS WRITE-PROTECTED, and resolved here for the reason the
	// workspace is resolved at the top of this function: the profile is a list of paths,
	// and a path the kernel never reports is a deny that matches nothing. See
	// homereadonly.go for the layout walk and for why the chain above each destination is
	// denied too. Nothing delivered, nothing to protect: the zero value renders no rule.
	var homeReadonly HomeReadonly
	if hostHomeOverlay.Tree != "" {
		homeOverlay = StagedHomeOverlay(cname, "")
		homeReadonly = ResolveHomeReadonly(SandboxHome(), workspace,
			hostHomeOverlay.WorkspaceDirs, hostHomeOverlay.Dests)
	}
	// AND THE THIRD, on the same rule: the CONTEXT tree (DP-L1). The host CLI composed it
	// — it is the only half that may read the invoking user's config and home — and this
	// resolves where it will land. "" when the caller composed nothing, which is what the
	// host-layer report below turns into `unsupported`, so a launch that delivered no host
	// bytes and a backend that cannot deliver them remain the same statement.
	ctxRoot := ""
	if hostCtx.Tree != "" {
		ctxRoot = StagedCtxRoot(cname, "")
	}
	// AND THE FOURTH, the install-capture store (H4): named only when the launch stages an
	// entry, so a launch with none bakes an empty store into every launcher, which downloads.
	captures := stageableCaptures(hostCtx.Captures)
	capturesRoot := ""
	if len(captures) > 0 {
		capturesRoot = StagedCapturesRoot("")
	}
	// THE CONTEXT DIR (docs/design/context-mounts.md CX-D4): the same root-owned tree, named
	// to the AGENT on every launch whether or not anything was composed into it, so pack
	// text and agents spell a context path `$YOLO_CONTEXT_DIR/<rel>` here as on a container
	// (where it is /ctx). The tree is staged every launch for that reason — empty when the
	// host CLI composed nothing (StageEmptyCtxCommands) — which is the deliberate departure
	// from YOLO_CTX_ROOT's absence-is-the-signal rule: that variable keeps it, and stays
	// the entrypoint's alone. The agent's copy rides the session env file, since the launch
	// argv's `env -i` list is closed (sandboxEnvPairs).
	contextDir := StagedCtxRoot(cname, "")
	sandboxEnv = withEnvVar(sandboxEnv, paths.ContextDirEnv, contextDir)
	// THE HOST-LOOPBACK DISPOSITION (paths.HostLoopbackEnvVar), which every container launch
	// emits and this backend did not: `shared`, BY CONSTRUCTION. The sandbox is an ordinary child
	// of the launcher on the Mac's own network stack, which is why internal/cli/run's
	// sharesLauncherNetns answers true for macos-user whatever `network.mode` says and every host
	// daemon advertises 127.0.0.1 here (loopholesruntime.go). The witness reads it to decide
	// severity (internal/entrypoint's loopbackDisposition), and `shared` escalates (OQ-R5): there
	// is no forwarding hop for an unusable service to hide behind. Set over every layer, as a fact
	// of the launch rather than a value a composed layer may change, and HERE rather than in
	// buildPlan so every plan carries it, the one the probe argv below reads included, and
	// PlanInvariants can check it.
	sandboxEnv = withEnvVar(sandboxEnv, paths.HostLoopbackEnvVar, paths.HostLoopbackShared)
	// THE STAGED PACK TREE AND THE WORKSPACE, NAMED TO THE SESSION, so an in-sandbox `yolo`
	// reads the jail the way its bootstrap did. `yolo programs` and `yolo pack update` decide
	// whether they are looking at a jail by YOLO_PACK_ROOT (cli/programs.go states why that
	// variable and not YOLO_VERSION), and read the receipts from the workspace's .yolo, which
	// this backend's session names as YOLO_DARWIN_WORKSPACE: entrypoint.JailEnvFromOS turns it
	// into the same Env DarwinEnvFrom gives the bootstrap. The container launch passes both
	// through its environment already (YOLO_PACK_ROOT=/ctx/packs, and /workspace is literal).
	//
	// Both on the PACK ROOT's condition, the bootstrap's own rule (buildBootstrapEnv): a launch
	// that staged no tree says so by absence, and the verbs then say "no staged packs here"
	// rather than computing every installed program as undeclared. The values are paths, root-
	// owned or the workspace itself; nothing composed rides with them.
	if packRoot != "" {
		sandboxEnv = withEnvVar(sandboxEnv, "YOLO_PACK_ROOT", packRoot)
		sandboxEnv = withEnvVar(sandboxEnv, "YOLO_DARWIN_WORKSPACE", workspace)
	}
	// THE WORKSPACE SIDECAR — <workspace>/.yolo/home, the same directory the podman argv
	// binds the jail home's per-workspace dirs from (paths.WorkspaceHomeState, one spelling
	// for both backends). Naming it is what turns the tier collapse off: the bootstrap
	// symlinks the account home's per-workspace dirs into it
	// (entrypoint.InstallDarwinHomeLayout). A capture passes none — see the parameter.
	launchMiseTools, _ := config.JailMiseTools(cfg, false)
	bootstrapEnv := buildBootstrapEnv(workspace, cfg, gitIdentity, sandboxEnv, packRoot,
		homeOverlay, ctxRoot, capturesRoot, hostCtx, paths.WorkspaceHomeState(workspace), SandboxHome(),
		darwinPrefix, blockedTools, launchMiseTools)
	bootstrapEnv.Set(paths.ContextDirEnv, contextDir)
	// THE COMPOSED MCP TABLE (packload.ComposeMCPServers): the staged packs' `mcp` entries joined
	// to the sandbox account's home, under the config's own `mcp_servers` — what the container
	// launch hands its jail as YOLO_MCP_SERVERS (docs/design/mcp-presets-removal.md OQ-MP4).
	// Composed from the host-side tree the stage commands copy, which is the tree the bootstrap
	// renders from; here rather than in buildBootstrapEnv, which an install capture shares and
	// whose throwaway home renders no agent. A tree that cannot be read leaves the config's own
	// table here, and the bootstrap, which reads the copy of that tree, fails the launch for it.
	userServers, _ := getSectionOrEmptyMap(cfg, "mcp_servers").(*jsonx.OrderedMap)
	if servers, err := entrypoint.MCPServersAt(hostPackRoot, userServers, SandboxHome()); err == nil {
		if wire, err := jsonx.DumpsCompact(servers); err == nil {
			bootstrapEnv.Set("YOLO_MCP_SERVERS", wire)
		}
	}
	// `programs.autoprune` — the catalog's removal act at boot (OQ-PD4's third clause, off by
	// default), relayed exactly as the container launch relays it (internal/cli/run's
	// assembleRunCmd): read from the USER config alone, so an agent-editable workspace config
	// cannot turn on a destructive act, and emitted only when on. Nor can an env_sources value:
	// the session env file carries those, and the bootstrap takes no YOLO_ name from it
	// (entrypoint's hydrate_session_env, program-delivery.md OQ-PD29). Here and not in
	// buildBootstrapEnv, because the install capture shares that function, and a capture's
	// throwaway staging home has nothing a removal could be for.
	if config.ProgramsAutoprune(nil) {
		bootstrapEnv.Set(entrypoint.OrphanAutopruneEnv, "1")
	}
	// THE CACHE RELOCATIONS' LINKS (ctxlinks.go; docs/plans/cache-relocation.md): the bootstrap
	// lays ~/.cache/<subdir> → target for each, and removes a link it laid for a subdir no longer
	// relocated (entrypoint's DarwinCacheRelocationsEnv). Named only when there is one, so a
	// launch with none says so by absence and the bootstrap's sweep removes every link it laid.
	// Here rather than in buildBootstrapEnv, which the install capture shares: its throwaway
	// home has no cache a relocation could be for.
	relocs := append([]CacheRelocation(nil), hostCtx.Relocations...)
	if len(relocs) > 0 {
		wire := map[string]string{}
		for _, r := range relocs {
			wire[r.Subdir] = r.Target
		}
		bootstrapEnv.Set(entrypoint.DarwinCacheRelocationsEnv, entrypoint.DarwinCacheRelocationsWire(wire))
	}

	stagedYolo := StagedYoloPath("")
	offendingHome, offendingSet := HomeContaining(workspace)

	// THE PROVISIONING STAGE (docs/design/macos-user-provisioning.md half two), composed
	// here so the dry-run plan shows it and PlanInvariants can check it — the two things
	// a step assembled inside the orchestrator would be invisible to.
	//
	// Skipped ENTIRELY, argv and all, when the config declares nothing for it. Absence is
	// the honest representation: a plan carrying a stage the launch will not run describes
	// a launch nobody performs, and every invariant below is written to say nothing about
	// an empty argv rather than to demand one.
	// THE SESSION ENV FILE, resolved before the two argvs that read it. It is named
	// whenever this launch composed anything at all — a workspace with no env_sources, no
	// profile and no git identity composes an empty map, and then there is no file, no
	// directory to prepare and no wrapper on either argv.
	//
	// THE TLS VARIABLES first (cabundle.go), each a default under the caller's layers, so the file
	// carries them and names the session's own CA files.
	sandboxEnv, caBundleFile, caExtrasFile, caFollows := applyCATrust(sandboxEnv, session.CATrust, sessionKey)
	envFile := ""
	envFileContent := SandboxEnvFileContent(sandboxEnv)
	if envFileContent != "" {
		envFile = SandboxEnvFile(sessionKey, "")
		// AND THE BOOTSTRAP IS TOLD WHERE IT IS, by path and never by value: the launch writes
		// the file before the bootstrap runs (orchestrator.go, step 2.5), and the bootstrap
		// reads it into its generator Env only (entrypoint's hydrate_session_env step), so the
		// MCP requires_env gate sees the hydrated env_sources the agent will have. Without it,
		// a server gated on a shared env_sources variable was dropped from every agent config
		// although the agent's own environment carried the variable.
		bootstrapEnv.Set(SandboxEnvFileEnv, envFile)
	}

	// THE DECLARED RESOURCES THIS BACKEND NOW ACTS ON (docs/design/declaration-parity.md
	// DP-D1's two rejected stand-ins were RLIMIT_AS and RLIMIT_NPROC; neither is used): the I/O
	// priority the orchestrator sets as a disk policy, the memory guard the launch argv runs,
	// and the cpus value buildPlan's parallelism defaults were set from. pids_limit is the one
	// still read and ignored.
	resCfg := cfgSection(cfg, "resources")
	ioPriority := ioprio.FromResources(resCfg)
	guard := SessionGuardFor(resCfg)
	cpus, _ := CooperativeCPUs(resCfg)

	var provisionArgv []string
	provisionScriptPath := ""
	if ProvisionNeeded(cfg, floors) {
		provisionScriptPath = ProvisionBootstrapScript(workspace)
		// The console line's color is the NO_COLOR half of the one gate (tty.NoColor), read
		// from the env this stage will run in — the forwarded host value (MacosSandboxEnv), or
		// one the user's own env layers set — because the stage prints from the sandbox, a
		// stream nothing here can probe, exactly as the container's does.
		scriptColor := !tty.NoColor(func(k string) string {
			if sandboxEnv == nil {
				return ""
			}
			v, _ := sandboxEnv.Get(k)
			return asStr(v)
		})
		provisionArgv = ProvisionArgv(ProvisionScript(workspace, provisionScriptPath, scriptColor),
			profilePath, envFile, workspace, "", "", darwinPrefix)
	}

	stageCommands := append([][]string{}, StageBinaryCommands(selfExe, "")...)
	stageCommands = append(stageCommands, StagePackCommands(hostPackRoot, cname, "")...)
	stageCommands = append(stageCommands, StageHomeOverlayCommands(hostHomeOverlay.Tree, cname, "")...)
	// THE CONTEXT DIR, with the composed tree in it when there is one and a root-owned link per
	// delivered context mount (ctxlinks.go). The links ride the same `.new`-then-swap as the
	// tree, so a mount dropped from the config stops being named on the next launch.
	ctxLinks := append([]ContextLink(nil), hostCtx.Links...)
	stageCommands = append(stageCommands, StageContextDirCommands(hostCtx.Tree, ctxLinks, cname, "")...)
	stageCommands = append(stageCommands, endpointGrantCommands(sandboxEnv)...)

	// THE GUEST'S BINARIES (OQ-DP8; jaildaemon.go), staged as ONE SET when the launch runs a
	// jail daemon OR its session env carries an endpoint a guest client reads (GuestClientsIn,
	// keyed on each client's own variable) — so a launch with neither stages nothing and a
	// checkout launch builds no `.#guestPrefix` for nothing.
	daemonNames := jailDaemons.Names()
	guestClients := GuestClientsIn(sandboxEnv)
	guestSource := ""
	if len(daemonNames) > 0 || len(guestClients) > 0 {
		guestSource = jailDaemons.GuestBinSource
		// The binaries the supervisor, the payload's argvs and the agent's clients name, beside
		// the staged yolo.
		stageCommands = append(stageCommands, StageGuestBinaryCommands(guestSource, "")...)
	}

	// THE GUEST'S JAIL DAEMONS (OQ-DP8, OQ-DP9; jaildaemon.go), composed only when the
	// launch handed this backend a payload naming at least one — so every artifact below is
	// absent, not empty, on a launch that runs none. The supervisor, its env file and its log
	// are the daemons' alone: a launch staging the set for a client starts no supervisor.
	var jailDaemonArgv []string
	daemonEnvFile, daemonEnvContent, supervisorLog := "", "", ""
	if len(daemonNames) > 0 {
		daemonEnvFile = SandboxDaemonEnvFile(sessionKey, "")
		daemonEnvContent = SandboxEnvFileContent(jailDaemons.Env)
		supervisorLog = SupervisorLogPath(workspace)
		jailDaemonArgv = JailDaemonArgv(profilePath, daemonEnvFile, supervisorLog, "", "", darwinPrefix)
		// Every endpoint the DAEMONS dial is granted too — the same grant the agent's
		// endpoints get, read off the daemon env for endpointGrantCommands' reason (the env
		// is the manifest), deduped against the ones already granted.
		stageCommands = appendNewCommands(stageCommands, endpointGrantCommands(jailDaemons.Env))
	}

	// THE HOST-SERVICE WITNESS (serviceprobe.go), composed only when the session env carries a
	// published endpoint, so a launch that started no host service runs no probe. The env file
	// exists whenever it does (the endpoint variable is in it).
	var probeArgv []string
	if carriesServiceEndpoint(sandboxEnv) {
		probeArgv = ProbeServicesArgv(stagedYolo, profilePath, envFile, "", "", darwinPrefix)
	}
	// AND ONE REAL WRITE PER CACHE RELOCATION under this session's profile (ctxlinks.go's
	// CacheRelocationWriteProbe), the question no DAC preflight can ask.
	var relocationProbes []CacheRelocationProbe
	for _, r := range relocs {
		relocationProbes = append(relocationProbes, CacheRelocationWriteProbe(r, profilePath, sessionID, ""))
	}

	// workspace_readonly with the config self-lock the container backends perform, a symlinked
	// config's target included wherever it sits (workspacereadonly.go).
	readonlyRels, readonlyTargets := workspaceReadonlyRels(workspace, cfg)

	caBundleContent, caExtrasContent := caTrustContents(session.CATrust, caBundleFile, caExtrasFile)
	envFileRemove := SandboxEnvRemoveCommands(envFile)
	for _, f := range []string{caBundleFile, caExtrasFile} {
		envFileRemove = append(envFileRemove, SandboxEnvRemoveCommands(f)...)
	}

	return RunPlan{
		Workspace:             workspace,
		Cname:                 cname,
		SessionID:             sessionID,
		ProfilePath:           profilePath,
		ProfileRemoveCommands: [][]string{{rmBin, "-f", profilePath}},
		// workspace_readonly and its config lock, each raw-path `devices` entry's ioctl
		// carve-out (devices.go), and macos_log, whose "off" is a deny (SeatbeltProfile).
		Seatbelt: seatbeltProfile(workspace, SandboxHome(), readonlyRels, readonlyTargets,
			homeReadonly, ctxLinks, relocs, cfgStrList(cfg, "devices"), macosLogMode(cfg)),
		StagedDir:  stateDir,
		StagedYolo: stagedYolo,
		// Binary first, then the pack trees, then the content overlay, then the context
		// tree: all four are prerequisites of the bootstrap the caller runs immediately
		// after this list, and the binary is the one that fails most cheaply.
		StageCommands:    stageCommands,
		PackRoot:         packRoot,
		CtxRoot:          ctxRoot,
		ContextDir:       contextDir,
		ContextLinks:     ctxLinks,
		ContextPreflight: ContextPreflight(ctxLinks, ""),
		// A copied pack `mount` occupies its path as a copied host file does, so the plan's own
		// re-siting (contextLinkProblems) refuses a link at, inside or around either.
		ContextOccupied: ContextOccupied(append(append([]string(nil), hostCtx.Delivered...),
			copiedDests(hostCtx.Copied)...)),
		ContextCopies: hostCtx.Copied,
		// THE CACHE RELOCATIONS (ctxlinks.go): the links the bootstrap lays, the profile's rules
		// above, and the two probes the launch asks before the agent.
		CacheRelocations:         relocs,
		CacheRelocationPreflight: CacheRelocationPreflight(relocs, ""),
		CacheRelocationProbes:    relocationProbes,
		BootstrapArgv:            DarwinBootstrapArgv(stagedYolo, SandboxHome(), bootstrapEnv, ""),
		ProvisionArgv:            provisionArgv,
		ProvisionScriptPath:      provisionScriptPath,
		ProvisionFloors:          floors,
		LaunchArgv: LaunchArgvWithGuard(agentArgv, profilePath, envFile, workspace, "", "",
			darwinPrefix, guard, stagedYolo),
		IOPriority:      ioPriority,
		SessionGuard:    guard,
		CooperativeCPUs: cpus,

		JailDaemonArgv:          jailDaemonArgv,
		JailDaemonNames:         daemonNames,
		SupervisorLog:           supervisorLog,
		GuestBinSource:          guestSource,
		GuestClients:            guestClientNames(guestClients),
		ProbeArgv:               probeArgv,
		DaemonEnvFile:           daemonEnvFile,
		DaemonEnvFileContent:    daemonEnvContent,
		DaemonEnvRemoveCommands: SandboxEnvRemoveCommands(daemonEnvFile),

		EnvFile:               envFile,
		EnvFileContent:        envFileContent,
		EnvFileCommands:       SandboxEnvDirCommands(envFile, ""),
		EnvFileGrantCommands:  SandboxEnvGrantCommands(envFile, ""),
		EnvFileRemoveCommands: envFileRemove,

		CATrust:         session.CATrust,
		CABundleFile:    caBundleFile,
		CABundleContent: caBundleContent,
		CAExtrasFile:    caExtrasFile,
		CAExtrasContent: caExtrasContent,
		CAFollows:       caFollows,

		GitIdentity:        gitIdentity,
		OffendingHome:      offendingHome,
		OffendingHomeSet:   offendingSet,
		DarwinPathPrefix:   darwinPrefix,
		DarwinEnv:          darwinEnv,
		DarwinSkipped:      darwinSkipped,
		DarwinMaterialized: darwin != nil,
		NixClientDir:       nixClientDir,
		HomeReadonly:       homeReadonly,
		CapturesDir:        capturesRoot,
		Captures:           captures,
		// THE CAPTURE STORE'S ENTRIES (H4), copied once per machine and pruned to what the user's
		// store still selects; nothing at all when the launch stages none. Their own field, run
		// best-effort, never StageCommands, every one of which refuses the launch when it fails.
		CaptureStageCommands: StageCaptureCommands(captures, hostCtx.CapturesKept, ""),
	}
}

// endpointGrantCommands is the cross-uid grant for EVERY published host-service endpoint this
// launch carries, read off the launch env rather than named one service at a time.
//
// IT USED TO NAME ONE VARIABLE, `YOLO_SERVICE_OPENAI_AUTH_BROKER_ENDPOINT`, because the
// macos-user arm started exactly one host service. The arm goes through the whole lifecycle
// now (internal/cli/run/run.go), so a hardcoded name would deliver the credential broker's
// endpoint and silently withhold every other one — and a withheld grant is not a missing
// feature but a service the sandbox is TOLD about and cannot open, which is the worst of the
// three states available.
//
// THE ENV IS THE MANIFEST, for macoshomeoverlay.go's reason one layer over: the run pipeline
// already decided which services published and wrote one variable per published endpoint, so
// re-deriving the set here would be a second selector over the same launch. paths' own prefix
// and suffix are the discriminator, never a literal — the producer (hostServiceEnvVar) and
// this consumer must not be able to drift apart by a re-typing.
//
// ⚠ ENDPOINT FILES ONLY, never the `_SOCKET` spelling, and the exclusion is about the ACE
// rather than about tidiness: a Unix socket needs WRITE to connect(2) and EndpointGrantCommands
// grants READ (loophole-transport.md OQ-T5), so a socket handed to it would be granted an
// access that cannot be used. No shipped loophole reaches that state on a Mac — the one
// socket-transport service left is the cgroup delegate, which is Linux-only — so this is a
// boundary being stated before it is crossed, not one being worked around.
//
// DEDUPED ON THE WHOLE COMMAND, because every endpoint of one launch lives in the SAME
// services dir, the session's own (internal/cli/run/servicessession.go), so the directory's
// search ACE repeats once per service. A stage
// command's failure is FATAL to the launch (orchestrator.go), so a repeated `chmod +a` is not
// a cosmetic duplicate. Deduping the emitted argv rather than the parent path is what keeps
// this from re-deriving what EndpointGrantCommands decided to emit.
func endpointGrantCommands(sandboxEnv *jsonx.OrderedMap) [][]string {
	if sandboxEnv == nil {
		return nil
	}
	var out [][]string
	seen := map[string]bool{}
	for _, k := range sandboxEnv.Keys() {
		if !isServiceEndpointVar(k) {
			continue
		}
		value, _ := sandboxEnv.Get(k)
		endpoint, ok := value.(string)
		if !ok || endpoint == "" {
			continue
		}
		for _, cmd := range EndpointGrantCommands(endpoint, "") {
			key := strings.Join(cmd, "\x00")
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, cmd)
		}
	}
	return out
}

// isServiceEndpointVar reports whether a variable name is a host service's ENDPOINT-FILE
// variable — the producer's own two halves (paths.ServiceEnvVarPrefix/Suffix), never a
// literal, so the grant here and the emission in internal/cli/run cannot drift apart by a
// re-typing.
//
// The length guard rejects the one degenerate name the two halves overlap on
// (`YOLO_SERVICE_ENDPOINT`), which no service can produce and which would otherwise be read
// as an endpoint with an empty name.
func isServiceEndpointVar(key string) bool {
	return len(key) > len(paths.ServiceEnvVarPrefix)+len(paths.ServiceEnvVarSuffix) &&
		strings.HasPrefix(key, paths.ServiceEnvVarPrefix) &&
		strings.HasSuffix(key, paths.ServiceEnvVarSuffix)
}

// stageCommandsNameEnvValue reports whether some staged command names the exact value the
// rendered env file exports for key.
//
// It offers each ARGUMENT back to the file's own renderer rather than cutting a value out of
// the file, so the question is "did the thing the sandbox will read get staged" asked through
// one notion of the file's syntax — the reason SandboxEnvFileSets exists at all.
func stageCommandsNameEnvValue(cmds [][]string, content, key string) bool {
	for _, cmd := range cmds {
		for _, arg := range cmd {
			if SandboxEnvFileSets(content, key, arg) {
				return true
			}
		}
	}
	return false
}

// buildBootstrapEnv composes the env baked onto the `yolo internal darwin-bootstrap` self-exec
// argv: the generator contract the entrypoint reads
// (YOLO_HOST_DIR/BLOCK_CONFIG/MISE_TOOLS/LSP/MCP/HOST_FILES/PACK_ROOT), the git identity, the
// two provider/profile wire tables, and the four YOLO_DARWIN_* extras the subcommand consumes
// (workspace, macos-log mode, login-rc PATH, home overlay). Reuses the container-side resolvers.
//
// `home` is the home the bootstrap will generate INTO, and it is a parameter rather than
// SandboxHome() because an install capture bootstraps a THROWAWAY STAGING HOME instead
// (capture.go): it needs the same launchers, shims and pack surfaces a launch would produce —
// the capture runs the generated launcher, not a second implementation of the install — but
// generated against the home the capture is about to run in. It is used for the login-rc PATH;
// the HOME/JAIL_HOME pair is baked by DarwinBootstrapArgv, which takes the same value.
//
// `packRoot`, `homeOverlay`, `ctxRoot` and `capturesRoot` are the ALREADY-STAGED destinations
// (StagedPackRoot, StagedHomeOverlay, StagedCtxRoot, StagedCapturesRoot), not their host-side
// sources, and "" means the caller staged nothing of that kind. They are resolved by the caller
// rather than here because the caller is also what emits the commands that stage them, and the
// two must not be able to disagree. An install capture passes "" for capturesRoot always: a
// capture whose launcher could materialize would record the store's bytes as a fresh install
// (install-capture.md slice 4(f), the container capture jail's own suppression).
//
// `hostCtx` is the caller's RECORD of what went into that tree (HostContext). Two variables
// read it — the host-layer report and the source-bearing half of YOLO_HOST_FILES — and both
// are statements about delivery, so neither may be derived from the config: a wire naming a
// host file nobody staged is the silent-wrong-composition this whole path exists to end.
//
// `homeSidecar` is <workspace>/.yolo/home, the per-workspace tier the bootstrap symlinks the
// account home into. "" means LAY NO LAYOUT, and the caller that passes it is the install
// capture: its home is a throwaway staging tree whose whole contract is that everything an
// installer writes lands under it, and its delta walk does not follow symlinks. A launch
// always names it.
//
// `blockedTools` are the selected packs' blocked-tool declarations. They are a PARAMETER
// because core blocks nothing by default since the guardrails pack took the rules over — the
// config's security section alone would render an empty YOLO_BLOCK_CONFIG and the generated
// home would carry no blockers at all.
//
// `miseTools` is the `mise_tools` table the caller hands this jail (config.JailMiseTools): a
// launch's is the config's, and a capture's is none of the config's own (FP-D19,
// docs/design/forked-programs-as-packs.md). A PARAMETER, for hostCtx's reason: which tools cross
// is the caller's statement about delivery, and cfg alone cannot say which caller it is.
func buildBootstrapEnv(workspace string, cfg, gitIdentity, sandboxEnv *jsonx.OrderedMap,
	packRoot, homeOverlay, ctxRoot, capturesRoot string, hostCtx HostContext, homeSidecar, home string,
	darwinPrefix []string, blockedTools []packload.BlockedTool, miseTools *jsonx.OrderedMap) *jsonx.OrderedMap {
	bootstrapEnv := jsonx.NewOrderedMap()
	bootstrapEnv.Set("YOLO_HOST_DIR", resolvePathAbs(workspace))
	blockJSON, _ := jsonx.DumpsCompact(config.NormalizeBlockedToolsWith(securitySection(cfg), blockedTools))
	bootstrapEnv.Set("YOLO_BLOCK_CONFIG", blockJSON)
	miseJSON, _ := jsonx.DumpsCompact(orderedMapToAny(miseTools))
	bootstrapEnv.Set("YOLO_MISE_TOOLS", miseJSON)
	lspJSON, _ := jsonx.DumpsCompact(getSectionOrEmptyMap(cfg, "lsp_servers"))
	bootstrapEnv.Set("YOLO_LSP_SERVERS", lspJSON)
	mcpSrvJSON, _ := jsonx.DumpsCompact(getSectionOrEmptyMap(cfg, "mcp_servers"))
	bootstrapEnv.Set("YOLO_MCP_SERVERS", mcpSrvJSON)
	mcpPresetsJSON, _ := jsonx.DumpsCompact(getSectionOrEmptyList(cfg, "mcp_presets"))
	bootstrapEnv.Set("YOLO_MCP_PRESETS", mcpPresetsJSON)
	// The `agent_updates` policy. macos-user is the backend where missing this hides
	// least: it bakes no image, so the launchers ARE the delivery.
	//
	// ⚠ It used to add "and the one whose single machine-wide home makes the install-prefix
	// lock they carry reachable". Since the home-tier layout the install prefixes
	// (~/.npm-global, ~/.local) are symlinks into <workspace>/.yolo/home, so the lock is
	// per-workspace here exactly as it is on every other backend — which is not a loss: two
	// workspaces now update two different installs, so there is nothing left for a
	// machine-wide lock to serialise.
	bootstrapEnv.Set(entrypoint.AgentUpdatesEnv, config.AgentUpdatesWire())
	// git identity rides verbatim (the subcommand's Env.Vars carries it into
	// configureGit).
	for _, k := range gitIdentity.Keys() {
		v, _ := gitIdentity.Get(k)
		bootstrapEnv.Set(k, v)
	}
	// THE DURABLE DIR, relayed from the launch env the run pipeline composed, so the
	// bootstrap's launch line reports on the same directory the agent is told about
	// (docs/design/durable-scratch-space.md §5.4) — and, like there, only when the launch
	// made it: an absent variable is how "none this launch" reaches this side.
	if sandboxEnv != nil {
		if v, ok := sandboxEnv.Get(durable.EnvVar); ok {
			bootstrapEnv.Set(durable.EnvVar, v)
		}
		// THE FORK DECISIONS, relayed the same way (entrypoint.ForkBuildsEnv): the bootstrap generates
		// the sandbox's launcher for a forked program, which bakes its bin's decision in. This backend
		// delivers no fork's build (docs/design/forked-programs-as-packs.md FP-D3), and the launch says
		// why per fork; relayed, the launcher repeats that reason and its next step when the program is
		// typed, rather than blaming a launch that never meant to build it.
		if v, ok := sandboxEnv.Get(entrypoint.ForkBuildsEnv); ok {
			bootstrapEnv.Set(entrypoint.ForkBuildsEnv, v)
		}
	}
	// host_files, IN TWO HALVES THAT COME FROM DIFFERENT PLACES, and the split is the
	// credential boundary rather than a structure.
	//
	// The SOURCE-LESS half is read from the config map handed in, never via
	// config.LoadHostFiles: the plan builder is pure, and a source-less entry is legal at
	// any scope, so the merged map is the right source for exactly that subset.
	//
	// The SOURCE-BEARING half cannot be read here AT ALL — it lives in the user config
	// and its bytes live in the user's home, which is what makes it user-scope-only — so
	// it arrives as hostCtx.HostFiles, already RESOLVED by the host CLI. That is DP-L1:
	// until 2026-09-13 this backend DROPPED the source-bearing half outright, because the
	// bytes cross on a /ctx mount and there were no mounts; they now cross by COPY into
	// the root-owned tree ctxRoot names, so the entries cross with them.
	//
	// ⚠ RESOLVED, not "staged": an entry is on the wire whether or not its host file
	// EXISTS. An absent dotfile is a normal state, the destination then renders from its
	// `defaults`/`content` layers, and that is what the container path does too — it
	// emits the entry and skips only the bind. Gating the wire on the copy would leave
	// this backend answering differently in exactly that state.
	if wire := hostFilesWire(cfg, hostCtx); wire != "" {
		bootstrapEnv.Set("YOLO_HOST_FILES", wire)
	}

	// YOLO_CTX_ROOT — where the host bytes actually landed. Both of the entrypoint's /ctx
	// readers resolve through it (entrypoint.ctxRoot for a pack's `readsHost` surface,
	// entrypoint.hostUserPath for the user's own host_files), which is the SAME seam
	// Apple Container already uses for the same reason: a backend that cannot present
	// /ctx at the constant path is TOLD the path instead of assuming one.
	//
	// Set only when the launch staged a tree, so a launch carrying no host bytes says so
	// by ABSENCE — a variable naming a directory that is not there would make an empty
	// delivery indistinguishable from a broken one, which is YOLO_PACK_ROOT's rule and
	// the reason the report below reads off the same condition.
	if ctxRoot != "" {
		bootstrapEnv.Set("YOLO_CTX_ROOT", ctxRoot)
	}

	// YOLO_GLOBAL_GITIGNORE — the host's global gitignore, at the PHYSICAL path the staged tree
	// holds it at (there is no /ctx on macOS to name instead). The entrypoint's git step
	// (configureGit) points core.excludesFile at it when it is a regular file, which is the
	// container launch's composed `excludesFile = ~/.config/git/ignore` done by the one reader
	// this backend's bootstrap already has. Git identity reaches the bootstrap only through
	// the YOLO_GIT prefix filter above, so the variable is set here, from the caller's record,
	// rather than through the launch env. Only with a tree: the file is in it or nowhere.
	if ctxRoot != "" && hostCtx.GlobalGitignore != "" {
		bootstrapEnv.Set(GlobalGitignoreEnv,
			ctxRoot+strings.TrimPrefix(hostCtx.GlobalGitignore, paths.ContainerContextDir))
	}

	// YOLO_HOST_LAYERS — the host-layer report (packload.HostLayerReport), and since
	// DP-L1 this backend can answer `supported` like every other one.
	//
	// ⚠ THE CARVE-OUT THAT STOOD HERE IS RETIRED, and retiring it is the whole point
	// rather than a side effect. It read: a `readsHost` surface's bytes cross on a /ctx
	// mount, this backend has no mounts, so every host layer is missing by construction —
	// and since the jail's read fails CLOSED (OQ-CO10), reporting `supported` would have
	// refused every launch selecting the claude or pi pack on a Mac with a settings.json.
	// The premise is now false: the bytes cross by COPY into ctxRoot, so a delivered file
	// is a file the jail can really open, and `unsupported` would be the lie.
	//
	// WHAT THE REPORT IS GATED ON IS DELIVERY, NOT THE PLATFORM. `supported` the moment
	// the host CLI composed a tree; `unsupported` when it composed none, which is both the
	// pre-DP-L1 state and the state of a caller that cannot compose one (a capture, a unit
	// test). Keeping the two readings on ONE condition — ctxRoot, which is itself derived
	// from hostCtx.Tree — is what stops a launch from claiming a delivery mechanism it did
	// not use, and OQ-R3's rule survives intact for the `unsupported` case: a backend that
	// did not deliver is still not REFUSED for it.
	//
	// Delivered is the launcher's own list, never a walk of the staged tree: a witness
	// that re-derives its expectation from the evidence is not a witness.
	bootstrapEnv.Set(packload.HostLayerEnvVar, hostLayerWire(ctxRoot, hostCtx))

	// YOLO_PACK_ROOT — the same generator-contract variable the container entrypoint
	// reads off its /ctx/packs mount, pointed at the root-owned staged copy. Without it
	// RunDarwinBootstrap's LoadJailPacks returns nothing and every pack loop below it
	// (ConfigurePackSurfaces, RunPackHooks) iterates an empty list — silently, since
	// "no packs mounted" is a legitimate state that renders nothing rather than failing
	// (B-0). The caller passes "" when the launch staged nothing, so a genuinely pack-less
	// launch still says so by ABSENCE rather than by naming a directory that is not there.
	if packRoot != "" {
		bootstrapEnv.Set("YOLO_PACK_ROOT", packRoot)
	}

	// YOLO_CAPTURES_DIR — the staged install-capture store (StagedCapturesRoot), which the
	// bootstrap bakes into every generated launcher (entrypoint's capturesDir) so its
	// `_try_materialize` hands it to `capture-materialize --store=`. The container launch emits
	// the same variable beside its `:ro` bind of the store (internal/cli/run's captures.go).
	// Only when the launch stages an entry, on YOLO_PACK_ROOT's rule: a store named and empty is
	// a launcher asking a directory that answers every program with a miss.
	if capturesRoot != "" {
		bootstrapEnv.Set(entrypoint.CapturesDirEnv, capturesRoot)
	}

	// YOLO_DARWIN_HOME_OVERLAY — the composed CONTENT tree (skills + briefings) the
	// bootstrap copies over the home it is generating into. Set only when there is content
	// to deliver, on the same reasoning as YOLO_PACK_ROOT: absence is the honest way to
	// say "nothing to install", and a variable naming a directory that is not there
	// would make an empty delivery indistinguishable from a broken one.
	//
	// It carries a PATH, not a mapping. The tree is already laid out at the
	// home-relative destinations the container path would have mounted, so installing
	// it is one recursive copy and the bootstrap needs no table to interpret.
	if homeOverlay != "" {
		bootstrapEnv.Set("YOLO_DARWIN_HOME_OVERLAY", homeOverlay)
	}

	// YOLO_DARWIN_HOME_SIDECAR — the per-workspace tier's location, and the switch that
	// decides whether the bootstrap lays the home layout at all. Absence is meaningful here
	// in a way it is not above: the two vars above say "there is nothing staged to install",
	// this one says "this home is not a workspace's" (the capture staging home), and a
	// launch that dropped it would fall silently back to the shared account home — the tier
	// collapse, restored by an omission.
	if homeSidecar != "" {
		bootstrapEnv.Set(entrypoint.DarwinHomeSidecarEnv, homeSidecar)
	}

	// THE WIRE TABLES, relayed from the launch env into the bootstrap env. The container
	// boot reads all three out of the jail environment (ConfigurePackSurfaces resolves the
	// selection into every pack's config patch through the resolved profile table; the
	// derives read the provider table through the prism), and the native bootstrap runs the
	// SAME generators — so without this relay a `-p` launch would compose the variant's env
	// correctly and still render every pack surface as if no variant were selected, which
	// is the silent half of the same defect. Read out of sandboxEnv by name, the way git
	// identity above is read out of it by prefix: the launch env is the ONE place the
	// channel lands, and the bootstrap is a consumer of it, not a second composition site.
	//
	// RANGED OVER entrypoint.WireTables, never spelled here. This loop used to name two of
	// the three and drop YOLO_PROFILES, and so did the invariant in PlanInvariants, so the
	// two agreed while codex on this backend rendered no model_provider: its selection named
	// a profile the bootstrap had no table for (docs/plans/notch-convergence.md, row D2).
	//
	// Always relayed when present, including the empty `{}`: an absent variable and an
	// empty table mean the same thing to the readers, but the container emits the empty
	// table explicitly, so the two backends' bootstraps see the same input shape.
	for _, wire := range entrypoint.WireTables() {
		if v, ok := sandboxEnv.Get(wire); ok {
			bootstrapEnv.Set(wire, v)
		}
	}
	// THE CREDENTIAL-VIEW SWITCH, relayed for the same reason: the bootstrap renders the pack
	// hooks, and on a view launch the claude pack's shared_credentials hook must not link the
	// view's path (entrypoint.skipsForCredentialView). The run pipeline puts the RESOLVED value
	// in the launch env when the view is on (opt-in here, claudeview.DefaultOn). Temporary, with
	// the switch (docs/design/claude-login-without-interception.md, CL-D10).
	if v, ok := sandboxEnv.Get(claudeview.SwitchEnv); ok {
		bootstrapEnv.Set(claudeview.SwitchEnv, v)
	}

	// MISE_DATA_DIR, named rather than defaulted — the bootstrap's Env resolves
	// $HOME/.local/share/mise when it is unset (entrypoint.NewEnv), and ~/.local is a
	// symlink into the workspace sidecar under the home-tier layout, so the default would
	// put the tool store in the per-workspace tier. The launch env carries the same value
	// from the same function (sandboxEnvPairs), so the store the generated .bashrc names
	// and the store the agent's PATH resolves through cannot disagree.
	bootstrapEnv.Set("MISE_DATA_DIR", SandboxMiseData(home))

	// Darwin extras consumed by `yolo internal darwin-bootstrap`.
	bootstrapEnv.Set("YOLO_DARWIN_WORKSPACE", workspace)
	bootstrapEnv.Set("YOLO_DARWIN_MACOS_LOG", macosLogMode(cfg))
	bootstrapEnv.Set(entrypoint.DarwinLoginPathEnv, SandboxPath(home, darwinPrefix))
	return bootstrapEnv
}

// PlanInvariants returns static-check violation messages over a RunPlan (all
// ordering.
func PlanInvariants(plan RunPlan) []string {
	var problems []string

	// B2 (Go): the staged yolo binary must live under the root-owned state dir,
	// and the bootstrap argv must self-exec THAT staged path — never the host
	// checkout (unreadable to the sandbox uid) or a bare "yolo" off PATH.
	if !strings.HasPrefix(plan.StagedYolo, plan.StagedDir+"/") {
		problems = append(problems,
			"staged yolo "+plan.StagedYolo+" is not under the root-owned state dir "+
				plan.StagedDir+"; the sandbox could rewrite its own launch binary")
	}
	if !containsArg(plan.BootstrapArgv, plan.StagedYolo) {
		problems = append(problems,
			"bootstrap argv does not self-exec the staged yolo ("+plan.StagedYolo+
				"); it would run an unstaged/unreadable binary")
	}
	// The stage step must have a real source binary to copy — an empty selfExe
	// (os.Executable failed) would stage nothing and the self-exec would fail.
	if stageCopySourceEmpty(plan.StageCommands) {
		problems = append(problems,
			"no source yolo binary resolved to stage (os.Executable failed); "+
				"the sandbox would have no bootstrap binary to exec")
	}

	// B3 (Go): the stage commands must produce a FRESH inode (copy-to-temp + mv),
	// not overwrite in place — macOS caches Mach-O signatures per vnode, so an
	// in-place overwrite gets the next exec SIGKILLed.
	if !stageCommandsUseFreshInode(plan.StageCommands) {
		problems = append(problems,
			"stage commands overwrite the staged binary in place; macOS signature "+
				"caching requires a fresh inode (copy-to-temp then mv)")
	}

	// The workspace must be neutral ground — never inside a user's home.
	if plan.OffendingHomeSet {
		problems = append(problems,
			"workspace "+plan.Workspace+" is inside the home directory "+
				plan.OffendingHome+"; the macos-user backend shares only "+
				"neutral ground. Move it under "+SharedRootDefault()+".")
	}

	// Git identity must reach the BOOTSTRAP env (baked onto the self-exec argv).
	bootStr := strings.Join(plan.BootstrapArgv, " ")
	for _, k := range plan.GitIdentity.Keys() {
		if !strings.Contains(bootStr, k) {
			problems = append(problems, "git identity "+k+" not baked into the bootstrap env")
		}
	}

	// The pack tree must be STAGED and must reach the BOOTSTRAP env, or the bootstrap
	// renders zero pack surfaces while reporting success — B-0, which survived because
	// nothing on this path ever asserted that the pack root arrived. Both halves are
	// checked because either one alone is silently useless: an env var naming a tree
	// nothing copied is as empty as a copied tree the bootstrap is never told about.
	if plan.PackRoot != "" {
		if !strings.HasPrefix(plan.PackRoot, plan.StagedDir+"/") {
			problems = append(problems,
				"staged pack root "+plan.PackRoot+" is not under the root-owned state dir "+
					plan.StagedDir+"; the sandbox could rewrite a pack manifest and grant "+
					"itself host access on the next launch")
		}
		if !stagesTreeAt(plan.StageCommands, plan.PackRoot) {
			problems = append(problems,
				"nothing stages the pack tree at "+plan.PackRoot+
					"; the bootstrap would render zero pack surfaces")
		}
		if !containsArg(plan.BootstrapArgv, "YOLO_PACK_ROOT="+plan.PackRoot) {
			problems = append(problems,
				"YOLO_PACK_ROOT="+plan.PackRoot+" is not baked into the bootstrap env; "+
					"LoadJailPacks would find no packs and every surface/hook loop would "+
					"iterate an empty list")
		}
	}

	// THE CONTEXT TREE, on the pack tree's rule and for a sharper failure (DP-L1). Three
	// halves, because any one alone is silently useless or actively wrong:
	//
	//   - staged somewhere the sandbox cannot REWRITE, or an agent edits the very bytes
	//     its own config is composed from on the next launch — the same argument that
	//     makes the pack root root-owned, one step closer to the credential;
	//   - actually COPIED, or the bootstrap opens an empty directory;
	//   - NAMED to the bootstrap, or the entrypoint's readers stay pointed at the literal
	//     /ctx, which does not exist on macOS at all, and every surface silently composes
	//     from its defaults layer while the launch reports a delivery.
	if plan.CtxRoot != "" {
		if !strings.HasPrefix(plan.CtxRoot, plan.StagedDir+"/") {
			problems = append(problems,
				"staged context root "+plan.CtxRoot+" is not under the root-owned state dir "+
					plan.StagedDir+"; the sandbox could rewrite the host bytes its own config "+
					"surfaces are composed from")
		}
		if !stagesTreeAt(plan.StageCommands, plan.CtxRoot) {
			problems = append(problems,
				"nothing stages the context tree at "+plan.CtxRoot+
					"; every `reads-host` surface and every source-bearing host_files entry "+
					"would compose from an empty directory")
		}
		if !containsArg(plan.BootstrapArgv, "YOLO_CTX_ROOT="+plan.CtxRoot) {
			problems = append(problems,
				"YOLO_CTX_ROOT="+plan.CtxRoot+" is not baked into the bootstrap env; the "+
					"entrypoint would read host layers from the literal /ctx, which does not "+
					"exist on macOS, and every surface would compose from its defaults layer")
		}
	}

	// THE GLOBAL GITIGNORE IS READ FROM THE STAGED TREE OR NOT AT ALL. The bootstrap runs
	// outside Seatbelt as the sandbox account and sets core.excludesFile to whatever this names,
	// so a path outside the root-owned tree is a file the agent could write and every git in the
	// sandbox would then obey.
	if v, ok := argvEnvValue(plan.BootstrapArgv, GlobalGitignoreEnv); ok &&
		(plan.CtxRoot == "" || !strings.HasPrefix(v, plan.CtxRoot+"/")) {
		problems = append(problems,
			GlobalGitignoreEnv+"="+v+" is not under the staged context root "+
				quoteOrNone(plan.CtxRoot)+"; the sandbox's git would read its global "+
				"gitignore from a file yolo did not stage")
	}

	// THE CONTEXT DIR IS NAMED AND IT EXISTS (CX-D4). Every launch tells the agent where its
	// context mounts live, so the name must be the root-owned staged tree — never a path the
	// agent can write, where a context tree could be re-pointed — something must stage it,
	// and both the bootstrap and the agent's env file must carry it.
	if plan.ContextDir == "" || !strings.HasPrefix(plan.ContextDir, plan.StagedDir+"/") {
		problems = append(problems,
			"the context dir "+quoteOrNone(plan.ContextDir)+" is not under the root-owned state "+
				"dir "+plan.StagedDir+"; $"+paths.ContextDirEnv+" would name a tree the agent "+
				"can rewrite, or nothing")
	} else {
		if !stagesTreeAt(plan.StageCommands, plan.ContextDir) {
			problems = append(problems,
				"nothing stages the context dir at "+plan.ContextDir+"; $"+paths.ContextDirEnv+
					" would name a directory that is not there")
		}
		if !containsArg(plan.BootstrapArgv, paths.ContextDirEnv+"="+plan.ContextDir) {
			problems = append(problems,
				paths.ContextDirEnv+"="+plan.ContextDir+" is not baked into the bootstrap env")
		}
		if !SandboxEnvFileSets(plan.EnvFileContent, paths.ContextDirEnv, plan.ContextDir) {
			problems = append(problems,
				"the session env file does not export "+paths.ContextDirEnv+"="+plan.ContextDir+
					"; the agent would not know where its context mounts live")
		}
	}

	problems = append(problems, contextLinkProblems(plan)...)
	problems = append(problems, cacheRelocationProblems(plan)...)

	// THE REPORT AND THE TREE ARE ONE FACT, checked against each other rather than each
	// against itself. The jail's read fails CLOSED (OQ-CO10), so a report claiming
	// `supported` with nothing staged refuses every host layer on the machine, and a
	// report claiming `unsupported` while a tree IS staged silently un-delivers bytes the
	// launch really did copy. Both are reachable by editing one of the two lines in
	// buildBootstrapEnv, and neither shows up in any rendered artifact.
	if got, ok := argvEnvValue(plan.BootstrapArgv, packload.HostLayerEnvVar); !ok {
		problems = append(problems,
			packload.HostLayerEnvVar+" is not baked into the bootstrap env; the jail's "+
				"fail-closed host-layer read would have no disposition and would silently "+
				"compose every `readsHost` surface without the user's own file")
	} else if report, parsed := packload.ParseHostLayerReport(got); !parsed {
		problems = append(problems,
			packload.HostLayerEnvVar+"="+got+" is not the report shape the jail parses; it "+
				"would be read as UNKNOWN, restoring the fail-open behaviour OQ-CO10 ended")
	} else if (report.Delivery == packload.HostLayersSupported) != (plan.CtxRoot != "") {
		problems = append(problems,
			"the host-layer report says delivery is "+report.Delivery+" while the staged "+
				"context root is "+quoteOrNone(plan.CtxRoot)+"; the two are one fact and the "+
				"jail refuses a launch that claims a delivery it did not make")
	}

	// AND THE WIRE MUST BE DECODABLE BY THE HALF THAT READS IT. A YOLO_HOST_FILES the
	// entrypoint cannot parse makes ConfigureHostFiles skip EVERY entry at once — the
	// user's whole `host_files` key, silently, from one malformed value — and nothing else
	// in the plan shows it, because the wire is a JSON blob on an argv.
	//
	// ⚠ THERE IS DELIBERATELY NO "a source-bearing entry must have been staged" CHECK, and
	// an earlier cut of this block had one that was WRONG. A source that does not exist yet
	// is a normal state (a dotfile the user has not written), the entry still crosses so the
	// destination renders from its `defaults`/`content` layers, and that is exactly what the
	// container path does — it emits the entry and skips only the bind. Refusing the launch
	// for it would have made this backend answer differently in the one state nobody would
	// think to test. The defect that check was reaching for — bytes staged and the root not
	// named — is caught above, against the tree rather than against an entry.
	if wire, ok := argvEnvValue(plan.BootstrapArgv, "YOLO_HOST_FILES"); ok {
		if _, err := config.UnmarshalHostFiles(wire); err != nil {
			problems = append(problems,
				"YOLO_HOST_FILES is not decodable by the entrypoint ("+err.Error()+
					"); every declared host_files entry would be skipped at once")
		}
	}

	// THE WORKSPACE TIER crosses as exactly one env var, and nothing downstream reports its
	// absence: a bootstrap that is not told the sidecar lays no layout, every pack `state`
	// dir stays in the shared account home, and the launch looks perfectly healthy — which
	// is the defect docs/design/macos-user-home-tiers.md exists to end, reachable again by
	// deleting one line in BuildRunPlan. Checked against the workspace's own sidecar path
	// rather than "some value", because a layout pointed at another workspace's sidecar is
	// the collapse with extra steps.
	if want := entrypoint.DarwinHomeSidecarEnv + "=" + paths.WorkspaceHomeState(plan.Workspace); !containsArg(plan.BootstrapArgv, want) {
		problems = append(problems,
			want+" is not baked into the bootstrap env; the sandbox home would keep every "+
				"workspace's agent state in one place (/Users/_yolojail)")
	}

	// Every wire table must reach BOTH the launch env and the BOOTSTRAP env. The launch env
	// alone composes the agent's process env; the bootstrap env is what renders the pack
	// surfaces and the derives — so a launch that carries YOLO_USE_PROFILES while the
	// bootstrap does not would run the selected variant's environment against config
	// written as if no variant were selected. Relayed in BuildRunPlan over the same list
	// (entrypoint.WireTables); this is what fails if that relay is deleted, which no test on
	// the launch env alone can see.
	//
	// READ FROM THE ENV FILE, where the launch env's composed values live. This check used to
	// scan LaunchArgv, which stopped carrying any composed value when the channel moved into
	// the session env file (envfile.go), so it could no longer fire for any table.
	for _, wire := range entrypoint.WireTables() {
		v, ok := sandboxEnvFileValue(plan.EnvFileContent, wire)
		if ok && !containsArg(plan.BootstrapArgv, wire+"="+v) {
			problems = append(problems,
				wire+" is in the launch env but not baked into the bootstrap env "+
					"("+wire+"="+v+"); the pack surfaces and derives would render as if no "+
					"profile were selected")
		}
	}

	// EVERY PUBLISHED ENDPOINT THE LAUNCH CARRIES MUST HAVE ITS CROSS-UID GRANT STAGED, and
	// this is the check that survives the generalisation of the host-service lifecycle.
	// Before it, one variable was named by hand in BuildRunPlan and one service started on
	// this arm; now the arm starts every loophole the machine supports
	// (internal/cli/run/run.go), so the failure to guard against is a NEW service whose
	// endpoint reaches the env file with no ACE behind it.
	//
	// That state is worse than an absent service and is invisible everywhere else: the
	// sandbox is TOLD where the endpoint is, the file is 0600 in a 0700 directory owned by
	// the invoking user, and the client fails with a permission error naming a path that
	// plainly exists — on the one backend whose entire point is the uid boundary. Nothing
	// else in the plan shows it, because both halves look present.
	//
	// ASKED THROUGH THE RENDERED FILE (SandboxEnvFileSets), never by parsing a value out of
	// it: each staged ARGUMENT is offered back to the writer's own renderer, so this and the
	// env file cannot disagree about quoting rather than about the value.
	for _, key := range SandboxEnvFileKeys(plan.EnvFileContent) {
		if !isServiceEndpointVar(key) {
			continue
		}
		if !stageCommandsNameEnvValue(plan.StageCommands, plan.EnvFileContent, key) {
			problems = append(problems,
				key+" carries a published endpoint that no stage command grants the sandbox "+
					"account; the file is 0600 under the invoking user, so the sandbox would "+
					"be told exactly where its service is and be unable to open it")
		}
	}

	// ⚠ NO `--login` ON THE LAUNCH ARGV EITHER, since 2026-09-12. The stage's copy of this
	// check (below) has always been here; the launch's absence was the fix that let a
	// forwarded command survive at all. `sudo -i` concatenates and backslash-escapes the
	// command it is given, leaving `$` for an intermediate login shell — so every `$var` in a
	// user's own `yolo -- bash -lc …` arrived empty and every newline was removed, silently
	// and with exit 0. It cost runbook item 6 a measurement (nine blank lines) and made this
	// backend the only one that rewrites the argv it is handed.
	if containsArg(plan.LaunchArgv, "--login") {
		problems = append(problems,
			"the launch argv carries `sudo --login`, which does not execve the command it "+
				"is given: it concatenates and backslash-escapes it, dropping newlines and "+
				"letting an intermediate login shell expand every `$var` against an empty "+
				"environment. A forwarded command would be silently rewritten and would "+
				"still exit 0 — see docs/design/macos-user-provisioning.md §1.1")
	}

	// THE AGENT'S OWN ARGV MUST BE CONFINED, UNDER THIS SESSION'S PROFILE — the check
	// that was missing while its two siblings were here.
	//
	// The provisioning stage (below) and the capture driver (CapturePlanInvariants) have
	// each been pinned to `sandbox-exec -f <profile>` since they were written, on the
	// argument that they run vendor code. The AGENT was not, and the agent is the process
	// this whole backend exists to confine: on macos-user the Seatbelt profile IS the
	// trust boundary — there is no container, no uid boundary between the agent and its
	// own tools, and nothing else in a launch that says "this may not read /Users".
	// Deleting `"/usr/bin/sandbox-exec", "-f", profilePath` from LaunchArgv (macosuser.go)
	// left every test in this repo green, which is the callee-pinned/call-site-unpinned
	// shape AGENTS.md names — one level up, because the callee here is the ARGV BUILDER
	// and the unpinned call site is the one place its output is trusted.
	//
	// THE THREE WORDS ARE CHECKED CONSECUTIVELY (containsArgPair) rather than as three
	// memberships: `-f` and the profile path both appear elsewhere on a real argv — the
	// path is also inside the env file's own vicinity and `-f` is a common flag — so three
	// independent membership tests would pass for an argv that named all three in
	// unrelated positions and confined nothing.
	//
	// Silent for an EMPTY LaunchArgv, which is a plan that launches nothing (a capture's
	// plan builder, a unit fixture); a launch that reaches the orchestrator always has one.
	if len(plan.LaunchArgv) > 0 &&
		!containsArgPair(plan.LaunchArgv, "/usr/bin/sandbox-exec", "-f", plan.ProfilePath) {
		problems = append(problems,
			"the agent launch argv does not run under `sandbox-exec -f "+plan.ProfilePath+
				"`; the agent would run unconfined as "+SandboxUser+", and on this backend "+
				"that profile is the whole trust boundary — nothing else denies it /Users, "+
				"the keychains or another process's command line")
	}

	// THE MEMORY GUARD IS ON THE LAUNCH ARGV EXACTLY WHEN IT IS DECLARED, run by the staged
	// yolo. Declared and absent, the launch tells the human resources.memory is guarded and
	// guards nothing; present and undeclared, a launch that asked for nothing runs a sampler
	// it never heard of; run by another binary, the sandbox execs one it may not be able to
	// read. The words are checked consecutively, like the profile above, because `--memory`
	// and a byte count alone could sit anywhere on an argv.
	guardWords := plan.SessionGuard.Argv(plan.StagedYolo)
	hasGuard := containsArgPair(plan.LaunchArgv, plan.StagedYolo, "internal", SessionGuardVerb)
	switch {
	case len(plan.LaunchArgv) == 0:
	case plan.SessionGuard.Enabled() && !containsArgRun(plan.LaunchArgv, guardWords):
		problems = append(problems,
			"resources.memory is declared ("+formatBytes(plan.SessionGuard.MemoryBytes)+") but the "+
				"launch argv does not run `"+strings.Join(guardWords, " ")+"`; the launch would "+
				"say the session's memory is guarded and guard nothing")
	case !plan.SessionGuard.Enabled() && hasGuard:
		problems = append(problems,
			"the launch argv runs the memory guard although resources.memory is not declared")
	}

	// Acceptance-bar guard: darwin store bin dirs must reach the launch PATH.
	launchStr := strings.Join(plan.LaunchArgv, " ")
	for _, storeBin := range plan.DarwinPathPrefix {
		if !strings.Contains(launchStr, storeBin) {
			problems = append(problems,
				"darwin package bin dir "+storeBin+" did not reach the launch "+
					"PATH — declared tools would be silently missing")
		}
	}

	// AND THE BOOTSTRAP'S OWN COPY OF THAT PATH, which is a different reader with a
	// different failure. $YOLO_DARWIN_LOGIN_PATH is what entrypoint.agentPath
	// returns, and the generators that ask "will the agent have this binary?" read it
	// (via agentPath and imageProbePath — `rg -n 'agentPath|imageProbePath'` for the set;
	// a count here drifts every time one is added)
	// before writing anything:
	//
	//   • GenerateShims — a blocked tool declaring a `replacement` is only blocked
	//     when the replacement is on that PATH, so a floor missing from it silently
	//     UN-BLOCKS `grep` and `find` instead of failing;
	//   • launchercollision's imageProbePath — a pack's `program` launcher is
	//     suppressed for a name the environment already provides, so a floor
	//     missing from it lets a pack shadow git or node;
	//   • AssertRequiredBins — a pack's `requires` entry warns as absent.
	//
	// All three answer WRONG rather than failing, and all three run in the
	// bootstrap, which the launch argv's PATH never reaches. So the launch guard
	// above cannot see this: a plan can put the store on the agent's PATH and still
	// generate the agent's environment as if the store were not there.
	for _, storeBin := range plan.DarwinPathPrefix {
		if !strings.Contains(bootStr, storeBin) {
			problems = append(problems,
				"darwin package bin dir "+storeBin+" did not reach "+
					entrypoint.DarwinLoginPathEnv+" in the bootstrap env — the "+
					"generators would decide what to write against a PATH that "+
					"does not have the floor on it")
		}
	}

	// THE PROVISIONING STAGE, when there is one. Every check below is silent for an
	// EMPTY ProvisionArgv, because "this config asked for no tools" is a legitimate plan
	// and the skip is the feature (ProvisionNeeded).
	if len(plan.ProvisionArgv) > 0 {
		// ⚠ NO `--login`. This is the invariant this file exists to carry, and it guards a
		// MEASURED defect rather than a style: `sudo -i` does not execve its argv — it
		// concatenates and backslash-escapes the command, leaving `$` unescaped for an
		// intermediate login shell to expand. The stage script is dense with `$_prc`,
		// `${PIPESTATUS[0]}` and `$(date …)`, and the failure is silent: the wrong command
		// runs and exits 0. A plan that grew the flag would provision nothing and report
		// success, which is precisely what the startup log cannot then record.
		if containsArg(plan.ProvisionArgv, "--login") {
			problems = append(problems,
				"the provisioning stage argv carries `sudo --login`, which does not execve "+
					"the command it is given: it concatenates and backslash-escapes it, and "+
					"leaves `$` for the login shell to expand. The stage script would be "+
					"mangled and would exit 0 having provisioned nothing — see "+
					"docs/design/macos-user-provisioning.md §1.1")
		}
		// CONFINED, under THIS session's profile. The stage runs vendor code (npm
		// postinstall hooks, mise plugins), which the container runs inside its jail; the
		// bootstrap's unconfined argv is tolerable only because it runs yolo's own code
		// (the design's P4). A stage that lost its sandbox-exec would be the one step
		// running third-party installers on a naked macOS account.
		if !containsArg(plan.ProvisionArgv, "/usr/bin/sandbox-exec") {
			problems = append(problems,
				"the provisioning stage is not run under sandbox-exec; it executes vendor "+
					"install code (npm postinstall hooks, mise plugins) and would be the "+
					"only unconfined step in the launch")
		}
		if !containsArg(plan.ProvisionArgv, plan.ProfilePath) {
			problems = append(problems,
				"the provisioning stage does not name this session's Seatbelt profile ("+
					plan.ProfilePath+"); it would be confined by some other launch's policy "+
					"or none")
		}
		// IT MUST EXEC THE SCRIPT THE BOOTSTRAP WRITES. Two halves, and either alone is
		// silently useless: a stage naming a path nothing generates fails on its first
		// line, and a script generated somewhere the stage never looks is a file nobody
		// runs. Both sides resolve through ProvisionBootstrapScript, so this is what
		// fails if one of them starts spelling the path itself.
		if want := ProvisionBootstrapScript(plan.Workspace); plan.ProvisionScriptPath != want {
			problems = append(problems,
				"the provisioning stage execs "+plan.ProvisionScriptPath+", but the bootstrap "+
					"generates the script at "+want)
		}
		if !argvMentions(plan.ProvisionArgv, plan.ProvisionScriptPath) {
			problems = append(problems,
				"the provisioning stage argv never names the generated bootstrap script ("+
					plan.ProvisionScriptPath+"); the stage would install nothing")
		}
		// AND IT MUST RECORD ITS FAILURE WHERE THE BRIEFING READS IT. The reader
		// (jailcontent.ReadProvisioningFailed) resolves the log from the HOST's workspace;
		// this backend has no bind to make a second path name the same file, so an emitter
		// rooted anywhere else leaves a failed provision reported as healthy.
		if !argvMentions(plan.ProvisionArgv, provision.StartupLog(plan.Workspace)) {
			problems = append(problems,
				"the provisioning stage does not write "+provision.StartupLog(plan.Workspace)+
					"; a failure would never reach the agent's briefing")
		}
	}

	// A LAUNCH THAT MATERIALIZED MUST HAVE SOMETHING TO SHOW FOR IT. Since the
	// floor landed there is no such thing as an empty native closure: even with an
	// empty `packages:` the profile holds mise, node, git and the rest, so an empty
	// prefix means the build returned a store path nothing derived a bin dir from.
	// Without this the two loops above are vacuously true in exactly that case —
	// they iterate an empty list and report a healthy plan.
	if plan.DarwinMaterialized && len(plan.DarwinPathPrefix) == 0 {
		problems = append(problems,
			"the native package closure was materialized but contributed no bin dir "+
				"to PATH; the floor (mise, node, git, ripgrep, …) would be absent "+
				"from the sandbox — see docs/design/macos-user-provisioning.md")
	}

	// NO COMPOSED VALUE ON A COMMAND LINE, AND THE FILE ACTUALLY READ. The two halves of
	// envfile.go's contract, checked together because either alone passes for a plan that is
	// wrong in the other way: an argv with no pairs and no reader is a sandbox with no
	// credentials, and an argv that reads the file and still carries the pairs has moved
	// nothing. Both are applied to every argv the sandbox user runs UNDER SEATBELT — the
	// bootstrap is excluded, and SandboxArgvEnvProblems says why.
	for _, pair := range [][2]any{
		{"launch", plan.LaunchArgv},
		{"provisioning stage", plan.ProvisionArgv},
	} {
		label, argv := pair[0].(string), pair[1].([]string)
		if len(argv) == 0 {
			continue
		}
		problems = append(problems, SandboxArgvEnvProblems(label, argv)...)
		if !SandboxArgvReadsEnvFile(plan.EnvFile, argv) {
			problems = append(problems,
				"the "+label+" argv never reads the session env file ("+plan.EnvFile+
					"); it would run with the identity quartet and nothing this launch "+
					"composed — no provider credentials, no env_sources, no git identity")
		}
	}
	// The file itself must sit under the root-owned state dir, for the staged binary's
	// reason: a file the sandbox could REWRITE is a sandbox that chooses its own
	// environment, and this one is sourced by the shell that execs the agent.
	if plan.EnvFile != "" && !strings.HasPrefix(plan.EnvFile, plan.StagedDir+"/") {
		problems = append(problems,
			"session env file "+plan.EnvFile+" is not under the root-owned state dir "+
				plan.StagedDir+"; the sandbox could rewrite the environment it is launched with")
	}
	// AND THE BOOTSTRAP IS TOLD WHERE THE FILE IS. It renders every agent's MCP table, and the
	// requires_env gate there asks the environment the agent will have; that environment is
	// this file. A bootstrap not told about it answers from its own closed contract, which
	// carries no env_sources value, and drops every server gated on one — silently, from every
	// agent config, while the agent's own environment has the variable.
	if plan.EnvFile != "" && !containsArg(plan.BootstrapArgv, SandboxEnvFileEnv+"="+plan.EnvFile) {
		problems = append(problems,
			SandboxEnvFileEnv+"="+plan.EnvFile+" is not baked into the bootstrap env; the MCP "+
				"requires_env gate would not see the hydrated env_sources, and every server gated "+
				"on one would be dropped from every agent config")
	}

	problems = append(problems, jailDaemonInvariants(plan)...)
	problems = append(problems, guestClientInvariants(plan)...)
	problems = append(problems, serviceProbeInvariants(plan)...)
	problems = append(problems, sessionFileInvariants(plan)...)
	problems = append(problems, captureStoreInvariants(plan)...)
	return problems
}

// captureStoreInvariants is PlanInvariants' rule for the staged install-capture store (H4): the
// bootstrap is told a store exactly when the plan stages an entry, the store it is told is under
// the root-owned state dir and under no spelling of /Users, and every entry the plan carries has
// its stage command. Each is a way a launch could look healthy and hand every launcher a store
// the sandbox can rewrite — the bytes every workspace on the machine then runs — or a store
// nothing filled, which downloads in silence.
func captureStoreInvariants(plan RunPlan) []string {
	var problems []string
	v, named := argvEnvValue(plan.BootstrapArgv, entrypoint.CapturesDirEnv)
	switch {
	case !named && len(plan.Captures) == 0:
		return nil
	case !named:
		return append(problems, "the plan stages "+itoa(len(plan.Captures))+" install capture(s) "+
			"but "+entrypoint.CapturesDirEnv+" is not baked into the bootstrap env; every launcher "+
			"would bake an empty store and download what is staged")
	case len(plan.Captures) == 0:
		problems = append(problems, entrypoint.CapturesDirEnv+"="+v+" is baked into the bootstrap "+
			"env but the plan stages no install capture; every launcher would ask a store "+
			"nothing filled")
	}
	if !strings.HasPrefix(v, plan.StagedDir+"/") {
		problems = append(problems, entrypoint.CapturesDirEnv+"="+v+" is not under the root-owned "+
			"state dir "+plan.StagedDir+"; the sandbox could rewrite captured bytes every "+
			"workspace on this machine runs")
	}
	if underUsersRoot(v) {
		problems = append(problems, entrypoint.CapturesDirEnv+"="+v+" is under /Users, which the "+
			"session profile denies reads of and where a capture's own files are the sandbox "+
			"account's; the store must be the root-owned copy under "+plan.StagedDir)
	}
	for _, c := range plan.Captures {
		if !containsCommand(plan.CaptureStageCommands, stageCaptureArgv(v, c)) {
			problems = append(problems, "nothing stages the capture of "+c.Bin+" ("+c.Key+") into "+
				v+"; its launcher would find no entry and download")
		}
	}
	// AND NONE OF IT IS FATAL: a capture copy among the stage commands, every one of which
	// refuses the launch when it fails, would make the store a launch's requirement.
	for _, c := range plan.StageCommands {
		if containsArg(c, stageCaptureScriptName) || containsArg(c, pruneCapturesScriptName) {
			problems = append(problems, "a capture-store script is among the stage commands a "+
				"launch refuses without; the store is optional, and a copy that fails must cost its "+
				"program the copy alone")
		}
	}
	return problems
}

// underUsersRoot reports whether p is any spelling of the users root (macOSHomes) or lies beneath
// it, /Users/Shared included.
func underUsersRoot(p string) bool {
	for _, q := range append([]string{p}, pathParents(p)...) {
		if macOSHomes.isUsersRoot(q) {
			return true
		}
	}
	return false
}

// containsCommand reports whether cmds holds want, argv for argv.
func containsCommand(cmds [][]string, want []string) bool {
	for _, c := range cmds {
		if len(c) == len(want) && containsArgRun(c, want) {
			return true
		}
	}
	return false
}

// jailDaemonInvariants is PlanInvariants' rule for the guest's jail daemons (jaildaemon.go),
// the plan's "a plan carrying a payload must carry a daemon argv" in full: the supervisor
// exists exactly when the payload names a daemon, it runs UNDER THE SESSION'S SEATBELT
// PROFILE (OQ-DP9), it reads its own env file and carries no composed value on its argv, that
// file sits in the root-owned state dir, and the binary it names is one this plan stages
// there. Each half is a way a plan could look healthy and run a daemon unconfined, run none,
// or run one out of a directory the sandbox can rewrite.
func jailDaemonInvariants(plan RunPlan) []string {
	var problems []string
	if len(plan.JailDaemonNames) == 0 {
		if len(plan.JailDaemonArgv) > 0 {
			problems = append(problems, "the plan starts a jail-daemon supervisor with an "+
				"empty payload; it would supervise nothing")
		}
		return problems
	}
	argv := plan.JailDaemonArgv
	if len(argv) == 0 {
		return append(problems, "the payload names jail daemons ("+
			strings.Join(plan.JailDaemonNames, ", ")+") and the plan starts no supervisor; "+
			"the launch would serve their addresses with nothing listening")
	}
	confinedAt := -1
	for i := 0; i+2 < len(argv); i++ {
		if argv[i] == "/usr/bin/sandbox-exec" && argv[i+1] == "-f" && argv[i+2] == plan.ProfilePath {
			confinedAt = i
			break
		}
	}
	jaild := GuestBinaryPath(JaildName, plan.StagedDir)
	jaildAt := -1
	for i, a := range argv {
		if a == jaild && i+1 < len(argv) && argv[i+1] == "supervise" {
			jaildAt = i
		}
	}
	switch {
	case confinedAt < 0:
		problems = append(problems, "the jail-daemon supervisor does not run under the "+
			"session's Seatbelt profile ("+plan.ProfilePath+"); a pack-declared long-running "+
			"process would sit outside the only confinement this backend has (OQ-DP9)")
	case jaildAt < 0:
		problems = append(problems, "the jail-daemon argv does not exec "+jaild+" supervise")
	case jaildAt < confinedAt:
		problems = append(problems, "the jail-daemon supervisor is started before "+
			"sandbox-exec, outside the profile (OQ-DP9)")
	}
	// The supervisor's own stdout and stderr go to its log, from inside the profile and in front
	// of everything that can fail after sandbox-exec: the one place a refusal of it is written
	// down, and where the launch reads its readiness line (JD-8).
	loggedAt := -1
	for i := 0; i+4 < len(argv); i++ {
		if argv[i] == sandboxEnvShell && argv[i+1] == "-c" && argv[i+2] == supervisorLogWrapper &&
			argv[i+4] == plan.SupervisorLog {
			loggedAt = i
			break
		}
	}
	switch {
	case plan.SupervisorLog == "" || plan.SupervisorLog != SupervisorLogPath(plan.Workspace):
		problems = append(problems, "the jail-daemon supervisor's log is \""+plan.SupervisorLog+
			"\", not "+SupervisorLogPath(plan.Workspace)+" beside the daemons' own logs")
	case loggedAt < 0:
		problems = append(problems, "the jail-daemon supervisor's stdout and stderr do not go to "+
			plan.SupervisorLog+"; a refusal of it would leave no trace, and the launch could not "+
			"see it start")
	case loggedAt < confinedAt || (jaildAt >= 0 && loggedAt > jaildAt):
		problems = append(problems, "the jail-daemon supervisor's log redirect is not between "+
			"sandbox-exec and "+JaildName+" supervise")
	}
	problems = append(problems, SandboxArgvEnvProblems("jail-daemon", argv)...)
	if plan.DaemonEnvFile == "" || !SandboxArgvReadsEnvFile(plan.DaemonEnvFile, argv) {
		problems = append(problems, "the jail-daemon supervisor never reads its env file, so "+
			"it has no payload and its daemons no caller tokens")
	}
	if plan.DaemonEnvFile != "" && !strings.HasPrefix(plan.DaemonEnvFile, plan.StagedDir+"/") {
		problems = append(problems, "jail-daemon env file "+plan.DaemonEnvFile+" is not "+
			"under the root-owned state dir "+plan.StagedDir)
	}
	if !SandboxEnvFileKeysInclude(plan.DaemonEnvFileContent, jailDaemonsEnv) {
		problems = append(problems, "the jail-daemon env file does not set "+jailDaemonsEnv+
			"; the supervisor would exit with nothing to supervise")
	}
	if !strings.HasPrefix(GuestBinDir(plan.StagedDir), plan.StagedDir+"/") {
		problems = append(problems, "the guest bin dir is not under the root-owned state dir")
	}
	staged := false
	for _, c := range plan.StageCommands {
		if len(c) == 4 && c[0] == mvBin && c[3] == jaild {
			staged = true
		}
	}
	if !staged {
		problems = append(problems, "the plan stages no "+JaildName+" into "+
			GuestBinDir(plan.StagedDir)+"; the supervisor argv names a binary that would not "+
			"exist (OQ-DP8)")
	}
	return problems
}

// SandboxEnvFileKeysInclude reports whether a rendered env file sets key.
func SandboxEnvFileKeysInclude(content, key string) bool {
	for _, k := range SandboxEnvFileKeys(content) {
		if k == key {
			return true
		}
	}
	return false
}

// appendNewCommands appends each of more that cmds does not already hold, whole-argv equal.
func appendNewCommands(cmds, more [][]string) [][]string {
	seen := map[string]bool{}
	for _, c := range cmds {
		seen[strings.Join(c, "\x00")] = true
	}
	for _, c := range more {
		k := strings.Join(c, "\x00")
		if !seen[k] {
			seen[k] = true
			cmds = append(cmds, c)
		}
	}
	return cmds
}

// argvMentions reports whether any argv element CONTAINS sub — the substring test the
// stage checks need, because the script is one argv element and the paths it must name
// are embedded in it rather than being arguments of their own.
func argvMentions(argv []string, sub string) bool {
	if sub == "" {
		return false
	}
	for _, a := range argv {
		if strings.Contains(a, sub) {
			return true
		}
	}
	return false
}

// argvEnvValue returns the value of the `K=V` word naming key on an argv, and whether it
// is there at all. The two answers are different facts and both are load-bearing: an
// ABSENT variable is a crossing that was never written, while an EMPTY one is a launch
// that legitimately asked for nothing, so a helper returning only the string would make
// the two indistinguishable at exactly the check that has to tell them apart.
func argvEnvValue(argv []string, key string) (string, bool) {
	for _, a := range argv {
		if v, ok := strings.CutPrefix(a, key+"="); ok {
			return v, true
		}
	}
	return "", false
}

// containsArg reports whether argv contains the exact arg.
func containsArg(argv []string, arg string) bool {
	for _, a := range argv {
		if a == arg {
			return true
		}
	}
	return false
}

// containsArgRun reports whether words appear in argv consecutively, in order: containsArgPair
// for a run of any length. An empty run is never contained, so an invariant handed a guard
// that renders no words cannot pass by vacuity.
func containsArgRun(argv, words []string) bool {
	if len(words) == 0 {
		return false
	}
	for i := 0; i+len(words) <= len(argv); i++ {
		match := true
		for j, w := range words {
			if argv[i+j] != w {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// stagesTreeAt reports whether the stage commands finish by moving a tree INTO dest —
// the last command each of the three tree stagers emits (StagePackCommands,
// StageHomeOverlayCommands, StageCtxCommands). Checking the destination of the final `mv`
// rather than merely "dest appears somewhere" is what makes the invariant meaningful: the
// path also appears in the preceding `rm -rf`, so a substring test would pass for a plan
// that deleted the tree and staged nothing.
//
// It was stagesPackRoot until the context tree joined the list. One predicate for all
// three deliberately: they share a shape, and a per-tree copy is three places for the
// `rm -rf` confusion above to be reintroduced one at a time.
func stagesTreeAt(cmds [][]string, dest string) bool {
	for _, c := range cmds {
		if len(c) >= 4 && c[0] == mvBin && c[len(c)-1] == dest {
			return true
		}
	}
	return false
}

// quoteOrNone renders a path for a problem message, or the word for its absence — so a
// mismatch message reads "the staged context root is none" rather than trailing an empty
// pair of quotes the reader has to interpret.
func quoteOrNone(p string) string {
	if p == "" {
		return "none"
	}
	return p
}

// stageCommandsUseFreshInode reports whether the stage commands end with an
// `mv` (the atomic rename that guarantees a fresh inode) rather than a bare
// in-place `cp` to the final path — the macOS signature-caching guard (J2 §3).
func stageCommandsUseFreshInode(cmds [][]string) bool {
	for _, c := range cmds {
		if len(c) > 0 && c[0] == mvBin {
			return true
		}
	}
	return false
}

// stageCopySourceEmpty reports whether the cp stage command has an empty source
// argument (StageBinaryCommands built from an empty selfExe) — i.e. nothing to
// stage. The cp argv is {cp, -f, <src>, <tmp>}, so the source is arg index 2.
func stageCopySourceEmpty(cmds [][]string) bool {
	for _, c := range cmds {
		if len(c) >= 4 && c[0] == cpBin && c[2] == "" {
			return true
		}
	}
	return false
}

func cnameFor(workspace string) string {
	return cnameFn(workspace)
}

// cnameFn is a package var so the run orchestrator can share a single naming
// definition; defaults to runtime.FromWorkspace.
var cnameFn = runtime.FromWorkspace

// --- config accessors (thin adapters over jsonx.OrderedMap) -----------------
// securitySection returns config["security"] as an OrderedMap, or nil.
// cfgSection is securitySection generalized to any top-level object key. Added for the
// backend-inert warnings in the orchestrator, which need `resources` and
// `cache_relocations` and would otherwise each grow their own copy of this five-line
// nil-and-type dance.
func cfgSection(cfg *jsonx.OrderedMap, key string) *jsonx.OrderedMap {
	if cfg == nil {
		return nil
	}
	v, ok := cfg.Get(key)
	if !ok {
		return nil
	}
	m, _ := v.(*jsonx.OrderedMap)
	return m
}

func securitySection(cfg *jsonx.OrderedMap) *jsonx.OrderedMap {
	if cfg == nil {
		return nil
	}
	v, ok := cfg.Get("security")
	if !ok {
		return nil
	}
	m, _ := v.(*jsonx.OrderedMap)
	return m
}

// getSectionOrEmptyMap returns config[key] as an OrderedMap, or an empty one.
// If the value is present but not a map, returns empty.
func getSectionOrEmptyMap(cfg *jsonx.OrderedMap, key string) any {
	if cfg != nil {
		if v, ok := cfg.Get(key); ok {
			if _, isMap := v.(*jsonx.OrderedMap); isMap {
				return v
			}
		}
	}
	return jsonx.NewOrderedMap()
}

// getSectionOrEmptyList returns config[key] as a list, or an empty list.
func getSectionOrEmptyList(cfg *jsonx.OrderedMap, key string) any {
	if cfg != nil {
		if v, ok := cfg.Get(key); ok {
			if _, isList := v.([]any); isList {
				return v
			}
		}
	}
	return []any{}
}

// macosLogMode returns config["macos_log"] as a string. An ABSENT key resolves to "off"
// here, which is why a jail whose config predates the key and one that sets it off are the
// same jail.
//
// It deliberately does not enum-check what it finds. config.validateMacosLog judges the
// value on the host (the key is in the schema since 2026-09-16 — before that every config
// declaring it was refused outright), and MacosLogWrapperScript rewrites anything it does
// not recognise to "off" downstream. A third check here would only shadow whichever of
// those two was wrong.
func macosLogMode(cfg *jsonx.OrderedMap) string {
	if cfg != nil {
		if v, ok := cfg.Get("macos_log"); ok {
			if s, ok := v.(string); ok {
				return s
			}
			// Non-string config value — rare; fall back to off, but
			// the container path only ever writes strings here.
		}
	}
	return "off"
}

// orderedMapToAny returns the OrderedMap as an `any` so jsonx.DumpsCompact
// encodes it (it accepts *OrderedMap directly).
func orderedMapToAny(m *jsonx.OrderedMap) any { return m }

// sourceLessHostFilesWire renders the merged config's SOURCE-LESS host_files
// entries as the YOLO_HOST_FILES wire string, or "" when there are none. The
// source-bearing half is deliberately excluded — it is unreachable from a pure
// function; see hostFilesWire.
func sourceLessHostFilesWire(cfg *jsonx.OrderedMap) string {
	wire, err := config.MarshalHostFiles(config.SourceLessHostFilesFrom(cfg))
	if err != nil {
		return ""
	}
	return wire
}

// hostFilesWire renders the WHOLE YOLO_HOST_FILES wire for this launch: the source-less
// entries this package can read from the merged config, plus the source-bearing entries
// the host CLI resolved (DP-L1), whose bytes it staged into the context tree when they
// existed.
//
// THE TWO HALVES CANNOT COME FROM ONE PLACE, and that is the feature. A source-less entry
// copies nothing from the host, so it is legal at any scope and the merged map is its
// proper source. A source-bearing entry names a host path — it can forward
// ~/.ssh/id_ed25519 — so it is read from the USER config directly and is inexpressible at
// workspace scope (config.SourceBearing). Reading the second half here would put a
// credential-boundary read inside a function whose contract is purity.
//
// SOURCE-BEARING WINS A DESTINATION COLLISION, which is config.LoadHostFiles' own rule
// for the same pair: it is the more specific declaration and it is the one the user wrote
// in their own config. Without the dedupe the same destination would be staged twice and
// the LAST loop iteration would decide, silently.
//
// Sorted by destination Path, like every other producer of this wire, so the bootstrap
// argv is deterministic.
func hostFilesWire(cfg *jsonx.OrderedMap, hostCtx HostContext) string {
	byPath := map[string]config.HostFileEntry{}
	var order []string
	for _, e := range config.SourceLessHostFilesFrom(cfg) {
		if _, seen := byPath[e.Path]; !seen {
			order = append(order, e.Path)
		}
		byPath[e.Path] = e
	}
	for _, e := range hostCtx.HostFiles {
		if !e.SourceBearing() {
			// Not reachable from the shipped caller, and dropped rather than trusted:
			// this field's contract is "entries whose SOURCE this launch staged", and a
			// source-less entry in it would be a second, unordered path to the same
			// destination the merged config already owns.
			continue
		}
		if _, seen := byPath[e.Path]; !seen {
			order = append(order, e.Path)
		}
		byPath[e.Path] = e
	}
	if len(order) == 0 {
		return ""
	}
	sort.Strings(order)
	entries := make([]config.HostFileEntry, 0, len(order))
	for _, p := range order {
		entries = append(entries, byPath[p])
	}
	wire, err := config.MarshalHostFiles(entries)
	if err != nil {
		return ""
	}
	return wire
}

// hostLayerWire is this launch's host-layer report, as the environment carries it.
//
// ONE PRODUCER FOR BOTH ANSWERS, keyed on the staged context root, because the two are a
// single fact seen from two sides: a launch that staged a tree DELIVERS host layers and
// must list what it put there, and a launch that staged none delivers nothing and must
// say the backend did not. Splitting them across two call sites is how a launch comes to
// claim `supported` while staging nothing — and the jail's read fails CLOSED on exactly
// that combination, so it would refuse every launch on this backend.
//
// Error-free for packload.HostLayersUnsupportedWire's reason: the report is a string and
// []strings, which cannot fail to marshal.
//
// IT IS entrypoint.HostLayerWire, THE CONTAINER LAUNCHER'S TYPE, and not a bare
// packload.HostLayerReport — render-mark parity (docs/design/notch-scoped-config-contributions.md
// §4.3, NS-D3). The bare report has no field for the fifth disposition, so until this
// marshalled the wire type a managed home's host file composed into the sandbox as the
// user's layer here while every container backend kept it as a baseline: the one backend on
// which a host-only entry `yolo host apply` wrote reached a jail. The wire embeds the report,
// so every reader that knows only the four dispositions reads this value exactly as before.
func hostLayerWire(ctxRoot string, hostCtx HostContext) string {
	if ctxRoot == "" {
		return packload.HostLayersUnsupportedWire()
	}
	wire, _ := entrypoint.HostLayerWire{
		HostLayerReport: packload.HostLayerReport{
			Delivery:  packload.HostLayersSupported,
			Delivered: hostCtx.Delivered,
		},
		Rendered: hostCtx.Rendered,
	}.Marshal()
	return wire
}

// cfgStrList reads config[key] as a list of strings, dropping non-string
// entries. The container path has its own copy (internal/cli/run/cfgval.go);
// duplicating four lines is cheaper than exporting a config accessor across a
// package boundary that otherwise shares nothing.
func cfgStrList(cfg *jsonx.OrderedMap, key string) []string {
	v, ok := cfg.Get(key)
	if !ok || v == nil {
		return nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, e := range list {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// caTrustFilePlans are the plan's CA files as installable files (cabundle.go).
func (p RunPlan) caTrustFilePlans() []sessionFilePlan {
	return caTrustFiles(p.EnvFile, p.CABundleFile, p.CABundleContent, p.CAExtrasFile, p.CAExtrasContent)
}

// envFile and envFileCommands make a RunPlan a sandboxEnvPlan (envfile.go).
func (p RunPlan) envFile() (string, string) { return p.EnvFile, p.EnvFileContent }
func (p RunPlan) envFileCommands() ([][]string, [][]string) {
	return p.EnvFileCommands, p.EnvFileGrantCommands
}

// withEnvVar returns a copy of env with key set to value, env itself untouched: the caller's
// launch env is theirs, and a plan builder that wrote into it would be a side effect a pure
// function is not allowed.
func withEnvVar(env *jsonx.OrderedMap, key, value string) *jsonx.OrderedMap {
	out := jsonx.NewOrderedMap()
	if env != nil {
		for _, k := range env.Keys() {
			v, _ := env.Get(k)
			out.Set(k, v)
		}
	}
	out.Set(key, value)
	return out
}
