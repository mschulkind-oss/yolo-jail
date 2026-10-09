// Package run implements the `yolo run` command — the container-startup
// command and the heaviest module in the CLI. It builds the full podman /
// Apple-Container argv (mounts, env, network, devices, GPU, kvm, loopholes),
// prestarts the host-side service plumbing, and either execs into an existing
// container or launches a fresh one.
// `run` is the default subcommand (bare `yolo -- cmd` → run).
package run

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/awsauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostcas"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/nixchildren"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
	"github.com/mschulkind-oss/yolo-jail/internal/progress"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

// ExecResult is the outcome of a short subprocess probe (git identity,
// lsusb, runtime lookups). Ran is false when the binary was absent or the
// process could not be started; Timeout is true when the call exceeded its
// deadline. Both degrade gracefully — callers swallow them.
type ExecResult struct {
	Stdout  string
	Stderr  string
	RC      int
	Ran     bool
	Timeout bool
}

// Options configures a run(). The CLI flags map to the leading fields; every
// side-effecting seam below is injectable so the probe + argv-assembly paths
// are deterministically unit-testable. nil/zero fields are
// filled with real implementations by fillDefaults.
type Options struct {
	// --- CLI surface (typer options + ctx.args) ---
	//
	// Network is `--network <mode>` as typed on THIS launch, and it beats the config's
	// `network.mode`. "" means the flag was not given: the config decides, and with no
	// key the launch runs bridged. Never read it as the mode — resolveNetMode is the one
	// reader, and nothing (NewDefaultOptions, fillDefaults) may fill it in, because a
	// filled-in default is indistinguishable from a typed `--network bridge` (G25).
	Network string
	// Notch is `--at <jail|guest|host>` as typed on THIS launch: the confinement
	// notch this invocation asks for, overriding the config's `confinement` key.
	// "" means the flag was not given and the config decides. launchNotch (run.go) is
	// the one reader that folds the two.
	//
	// A LAUNCH HONORS `jail` everywhere and `guest` on macOS, where it is the
	// macos-user backend (env-manager plan EMP-D1). A Linux `guest` and `host` are
	// REFUSED rather than ignored (refuseUnbuiltNotch, run.go; OQ-DP3 in
	// docs/design/declaration-parity.md). Before this field existed
	// cli.parseRunArgs had no `--at` case at all, so the token fell to its
	// default arm and STARTED THE COMMAND: `yolo --at guest -- claude` launched a
	// jail and then failed inside it with `--at: command not found` (DP-B22).
	// `--at host` never reaches here from the CLI: the front door (cli.routeArgv)
	// routes every launch spelling carrying it to the `host` subcommand
	// (docs/plans/notch-convergence.md item 10). A caller setting it directly still
	// gets refuseUnbuiltNotch's host refusal, as `confinement: host` does.
	Notch string
	// atNotch is the notch this launch runs at, Notch folded over the config's `confinement`
	// (launchNotch), recorded by Run once refuseUnbuiltNotch has passed it. Read by the
	// macos-user arm's printers that hold no config: at a guest, a next step naming a
	// container runtime names the jail notch too (containerStepClause). The zero value
	// reads as the jail notch, which is what every Options built outside Run describes.
	atNotch config.Confinement
	// NeverAttach skips the attach-to-running-container branch entirely. NOT a
	// CLI flag (the old --new was removed 2026-09-06): it is the capture jail's
	// programmatic "this launch must boot, never re-enter" — a capture runs its
	// installer in a home the boot just made, and attaching to a live container
	// would run it in whatever state that container is in. The scratch workspace
	// is fresh per capture so nothing CAN be running; this makes that true by
	// construction rather than by luck (capturehost.go's comment).
	NeverAttach bool
	// NoProgramReadiness turns the jail's readiness act off for this launch whatever the host
	// environment holds: the launcher forwards paths.NoProgramReadinessEnv with the value
	// paths.NoProgramReadinessCaptureJail (programReadinessArgs). NOT a CLI flag: it is the
	// capture and build jails' (cli.runCaptureJail), whose command is itself an install. A
	// readiness act ahead of it installed the program before `yolo capture` took its baseline,
	// so the capture recorded nothing, and refused a fork's build jail for the program the
	// build had not produced yet.
	NoProgramReadiness bool
	// Timing is --timing, AS TYPED ON THIS INVOCATION: an explicit, per-launch
	// request for this launch's performance timings — the full span system
	// (docs/reference/perf-logging.md).
	//
	// It is one of the two EXPLICIT signals (Verbose is the other), and that
	// distinction is the whole of D12: an explicit flag records AND prints, while
	// a persistent setting (`perf_logging: true`, or YOLO_TIMING/YOLO_VERBOSE
	// exported in a shell profile) records SILENTLY. So nothing folds into this
	// field any more — fillDefaults used to fold the config opt-in here, which
	// erased exactly the distinction the two gates below now make. Call sites read
	// the gates (timingRecording / timingReporting), never this field.
	Timing bool
	// Verbose is the global --verbose / -v flag, AS TYPED ON THIS INVOCATION —
	// the second explicit signal, equal to Timing in every gate below.
	//
	// It exists because the front door publishes that flag as YOLO_VERBOSE
	// (internal/cli/verbose.go, so every Getenv-seam reader in the process sees
	// it), which leaves `--verbose` and a YOLO_VERBOSE=1 inherited from a shell
	// profile INDISTINGUISHABLE downstream — and under D12 they must differ: the
	// typed flag prints, the environment does not. An in-process field is the
	// signal a second env var could not be, since an env var would be inherited by
	// the next `yolo` too, which is the very thing being distinguished.
	// internal/cli sets it from the front door's own strip.
	Verbose bool
	// PerfLoggingConfig reads the persistent `perf_logging` opt-in from the user
	// config. nil => config.PerfLoggingEnabled. Its answer is folded into
	// perfLoggingOn once, by fillDefaults.
	//
	// A seam for the reason every host-environment read in this struct is one:
	// without it, fillDefaults would consult the DEVELOPER's real
	// ~/.config/yolo-jail/config.jsonc during unit tests, and a maintainer who
	// turned timing on for their own jails would watch the off-path tests
	// (TestTeardownChainSilentWhenOff) start failing on their machine and
	// nowhere else. goldenOptions pins it false.
	PerfLoggingConfig func() bool
	// perfLoggingOn is PerfLoggingConfig()'s answer, read ONCE by fillDefaults,
	// single-threaded, before any span exists — so timingRecording() stays a field
	// read rather than a read of a file on every call site. Unexported because it
	// is derived state: a caller sets the seam, never this.
	perfLoggingOn bool
	// DryRun is --dry-run (macos-user only; a hard error elsewhere).
	DryRun bool
	// AcceptConfigChanges is --accept-config-changes: it grants the config-change
	// approval on a launch with no terminal to prompt on, which without it is a
	// REFUSED launch (docs/reference/config-safety.md, OQ-D2).
	//
	// A flag and not an environment variable, because it grants an APPROVAL rather
	// than suppressing a diagnosis, and an approval must not be inherited by every
	// child process or outlive the launch that was approved. See
	// config.AcceptConfigChangesFlag, which owns the spelling.
	AcceptConfigChanges bool
	// ProfileName is --profile <name> or -p <name> (e.g. "glm" or "glm-dev" — the
	// name is the next token, and there is no other reading of either flag). It is
	// GLOBAL, whether or not a command follows `--`: the selected profile of every CLI
	// every pack this launch selects installs and UseProfiles does not name
	// (effectiveUseProfiles) — the flag half of what the config `profile` key's "*" says.
	// The command after `--` is not read as a profile target anywhere — it was, in
	// checkProfileTargets' refusal alone, which is why a non-agent command used to be refused
	// here while the table it produced had never keyed on one.
	ProfileName string
	// UseProfiles is the -p <cli>=<name> overrides, keyed by CLI name (the spelling was
	// --pack-profile until 2026-09-03, when -p took both grammars). It is the only
	// profile flag spelling that NAMES a CLI, so a key no resolvable pack installs is
	// refused at launch (checkProfileTargets). A named CLI keeps its entry beside a bare
	// -p, as a named entry of the config `profile` key keeps its own beside "*" (PP-D10).
	UseProfiles map[string]string
	// WithCredentials is --with-credentials as typed on THIS launch: provider names and `all`,
	// every occurrence's comma list split, in order; nil when the flag was not given
	// (docs/design/credential-sources-separation.md OQ-ES5's jail half, jailgrant.go). It is
	// the grant's ONLY source: no config key, no environment variable, no -p and no
	// use_profiles entry sets it, and nothing else in this struct implies it. The front door's
	// parse is the one writer (internal/cli's parseRunArgs).
	WithCredentials []string
	// jailGrant is WithCredentials resolved over this launch's composition (resolveJailGrant),
	// nil without the flag: what a fresh launch hands the jail it starts, and what an attach
	// asks the running jail to hold already.
	jailGrant *jailGrant
	// jailGrantFile is the host copy of the grant file this FRESH launch staged (stageJailGrant),
	// which the podman argv binds; "" when none was staged. Run's deferred discard removes it
	// unless a container came to hold it (discardUnheldJailGrant).
	jailGrantFile string
	// heldGrant is the grant the processes this entry starts hold, for the disclosure: this
	// launch's own on a fresh launch and on macos-user, the running jail's (its keeper's start
	// record) on an attach. nil when they hold none.
	heldGrant *jailGrant
	// stagingCfg is the launch's merged config, handed to stagePacks so the `via` closure
	// (OQ-WG6/WG7 (c)) sees the config's profile as well as -p. Set by Run before
	// staging; nil in a caller that stages without a config (every such caller is a test),
	// where only the flags select.
	stagingCfg *jsonx.OrderedMap

	// configSources is where each value of the config loadAndValidateConfig loaded was
	// written (config's sources.go), for a refusal later in the launch that names a key of
	// that config: the host_files two-writers refusal (config.SurfaceCollisions). nil before
	// the load, and where the config came from no files (the in-jail assembled copy).
	configSources *config.Sources
	// launchLock is this launch's hold on the per-workspace launch lock, taken where each
	// backend first touches what the workspace's launches share and released where it stops
	// (holdLaunchLock has the window, per backend). nil until then, and in a caller that
	// never takes it.
	launchLock *workspaceLock
	// sessionLock is this launch's hold on its container jail's SESSION LOCK (sessionlock.go):
	// LOCK_SH, taken under the launch lock by the fresh launch before its container starts and
	// by an attach before its exec, and released by Run's deferred releaseSessionLock. nil
	// until then, on macos-user, and in a caller that never takes it.
	sessionLock *sessionLock
	// keeperDrainSeen is set when this launch's count found the jail's keeper ending it
	// (takeSessionLock's errKeeperDraining): the arrival then waits for the keeper instead of
	// entering a jail being stopped (docs/design/jail-lifetime-last-session-wins.md JL-D28).
	keeperDrainSeen bool
	// keeperMode is set on a KEEPER's own Options (keeper.go): the children it starts get the
	// kernel's death signal, and its self-execs run its own binary.
	keeperMode bool
	// keeperCommand is, on a keeper at macos-user, the command of the launch that spawned it, which
	// the supervision of a doorway or service it holds names (macosUserCommandName).
	keeperCommand string
	// macosUserKey is a macos-user launch's place at its KEY (keeperspawn.go's arriveMacosUser;
	// docs/design/jail-lifetime-last-session-wins.md §9.9): the arrival lock it holds, the roster it
	// joined, its session record and the keeper it spawned. nil on every other backend, on a dry
	// run, and for a caller that hands Run no backend.
	macosUserKey *macosUserKeying
	// packTree is the pack tree THIS launch staged (packtree.go), one per launch and never
	// edited afterwards (docs/reference/pack-system.md#oq-pk2). "" until staging.
	packTree string
	// packTreeHeld is set by the fresh container path just before its container starts: from
	// then on the container holds packTree, so Run's deferred discardUnheldPackTree leaves it
	// and forgetGoneContainer removes it once the runtime answers the container is gone.
	packTreeHeld bool
	// launchGuard is a container launch's signal arm from its pack staging until an arm of its own
	// takes over, and what it would discard on a signal before then (launchguard.go). nil on
	// macos-user and in a caller that never stages.
	launchGuard *launchGuard
	// servicesSession is a macos-user launch's own host-services dir and its liveness lock
	// (servicessession.go), created by the spawn (startLoopholesMatching) and removed by the
	// arm's deferred endServicesSession. nil on every other backend, before the spawn, and after
	// the teardown.
	servicesSession *servicesSession
	// startupRefusal is an attempt-specific fatal configuration refusal captured while a per-jail
	// service was starting. The lifecycle caller returns it through the same unwind boundary as
	// olderDaemonRefusal, while never treating historical log text as evidence.
	startupRefusal *hostStartupRefusal
	// startupOutcomes holds this launch's owner-local evidence and is never serialized to a keeper.
	startupOutcomes []hostservice.StartupOutcome
	startupAttempt  uint64
	// singletonDepsForStart is nil in production; it lets owner tests inject lifecycle failures
	// through the actual startHostSingleton and selected-loop consumption edges.
	singletonDepsForStart func(string, []string) broker.Deps
	settingsSnapshots     map[string]*ownedSettingsSnapshot
	settingsPrepared      bool
	settingsPlan          *preparedLoopholeSettings
	settingsFrozen        map[string][]byte
	// reachSubject is who a host service's failure leaves unable to reach it, in the warnings
	// that say so (unreachableBy): "" for a jail launch, which says "the jail", and the agent's
	// name for a `yolo host` launch opening a doorway (HostDoorways.Start), which runs no jail.
	reachSubject string
	// launchLockWaited records that taking launchLock meant waiting for another launch of
	// this workspace to finish its window — so a jail found running afterwards is the one
	// THAT launch started, and the attach says so.
	launchLockWaited bool
	// callerTokens are the pack-service caller tokens this process has settled on, keyed by
	// variable (callertokens.go, docs/reference/wire-bridge.md WB-D18): minted by the first
	// composition that needed one, or adopted from the running jail by an attach. nil until then.
	callerTokens map[string]string
	// unstartedDaemons are the profile-served jail daemons (packload.UnselectedProfileServedDaemons,
	// providers.md OQ-CN7 (b)) the last jail-daemon payload this process composed
	// left out because no agent's selection delivers a gate they serve. Read by the one disclosure
	// that says so (noteUnstartedProfileDaemons). nil when none was left out.
	unstartedDaemons []packload.ProfileServedDaemon
	// unstartedDaemonProfiles is, per daemon in unstartedDaemons, the declared profiles whose
	// selection would start it, for the disclosure's remedy.
	unstartedDaemonProfiles map[string][]string
	// shadowedServices are the pack `service` declarations the last jail-daemon payload this
	// process composed set aside because a later pack declares the same name
	// (packload.HeldServices, notch-convergence NC-D59). Read by the one disclosure that says so
	// (noteShadowedServices). nil when no service name is declared twice.
	shadowedServices []packload.ShadowedService
	// jailBound are the BOUND LOOPHOLES (loopholes.JailBoundNames: no jail daemon, host binds or
	// devices) whose binds the last jail-daemon payload this process composed would put on the
	// container argv, recorded beside it so servedDaemons serves their names: a pack env pointer
	// `served_by` one reaches the jail only where its binds do (docs/design/loophole-packaging.md
	// LP-D1). nil on macos-user, which binds nothing, and when none is active.
	jailBound []string
	// parentPointers are the loopholes whose credential pointer the last jail-daemon payload this
	// process composed takes from the launching jail instead of running their daemons
	// (parentjailpointers.go, docs/design/sso-backed-bedrock.md SSO-D2), each mapped to the
	// launching environment's value of every variable its pointer carries. Recorded beside the
	// payload so servedDaemons delivers them, and read by the one disclosure that says so
	// (noteParentJailPointers). VALUES ARE CREDENTIALS: never printed, never logged. nil when none.
	parentPointers map[string]map[string]string
	// launchServices are the LAUNCH-OWNED SERVICES this macos-user launch planned
	// (macosuserservices.go, docs/design/host-notch-services.md §4.7): pack services whose host
	// half runs as this launch's child because a profiled agent's pairing needs one. Settled
	// once per process, so the channel's compositions and the start share one port and token.
	// launchServiceAgents names, per service, the agents whose pairing needed it.
	launchServices      []*launchservice.Plan
	launchServiceAgents map[string][]string
	// refusedDoorways are the doorways whose host argv (`jail_daemon.host_cmd`) the last
	// jail-daemon payload this process composed did not admit (launchservice.AdmitDoorways: a pack
	// yolo does not ship, or an argv not naming `yolo`), so each is judged as the jail daemon it
	// also is. Read by the one disclosure that says so (noteRefusedDoorways). nil when none.
	refusedDoorways []launchservice.RefusedDoorway
	// refusedServiceHosts are the pack services whose host half the last jail-daemon payload this
	// process composed did not admit (launchservice.AdmitServiceHosts: a pack yolo does not ship,
	// or an argv not naming `yolo`), so each is judged as the jail daemon it also is. Read by the
	// one disclosure that says so (noteRefusedServiceHosts). nil when none.
	refusedServiceHosts []launchservice.RefusedDoorway
	// launchDoorways are the DOORWAYS this macos-user launch opens outside its sandbox as
	// launch-owned listeners (macosuserdoorways.go, docs/design/host-notch-services.md HS-D15),
	// planned from the payload at the served addresses and caller tokens the channel composed.
	launchDoorways []*launchservice.Plan
	// served is the SERVED ADDRESSES this process has settled on (servedaddresses.go,
	// docs/plans/notch-convergence.md NC-D41): the ports picked for a jail sharing this
	// process's network namespace, or the running jail's, adopted by an attach. Zero until then.
	served servedAddressState
	// approvedScopes is the repository scope this fresh launch's config-change gate approved,
	// per brokered loophole name, each repository with its sources: the remotes and the
	// workspace's `brokered.<source>.repos` entry (brokeredscope.go,
	// docs/design/workspace-widening.md WW-P2). Set only by a gate that passed, from its
	// in-memory result; the spawn writes each one to the launch's scope file.
	approvedScopes map[string][]config.ScopeRepo
	// scopeFiles are the scope files this launch wrote, per brokered loophole name, which
	// {repository_scope} resolves to at the spawn and the loophole's stop removes (BB-D32).
	scopeFiles map[string]string
	// runtime is the backend Run resolved (resolveRuntime), recorded for the compositions that
	// run below it and must see the same one — the jail-daemon payload the caller tokens are
	// minted from (profilechannel.go). "" until Run resolves it, which every hand-built test
	// Options is, and which composes as a container runtime does.
	runtime string
	// Args is ctx.args — the command after `--` (empty → interactive bash).
	Args []string

	// --- seams ---
	// Perf is the timing-span collector behind --timing/--verbose. nil (Timing
	// off) is a valid, fully no-op state — every call site is unconditional.
	// Constructed by Run right after the early refusals, never by callers, and
	// deliberately NOT by fillDefaults: an off launch must construct nothing.
	// The one thing it must never be given is o.Now (see initPerf).
	Perf *perf.Log
	// PerfRef, when non-nil, receives the collector initPerf builds, so a caller
	// holding a COPY of Options can span work that happens after Run returns — or
	// that a handler it injected does while Run runs: initPerf publishes it before any
	// seam is called, and the macos-user handler hands it to the backend (internal/cli's
	// macosUserRun), whose steps are spanned on it.
	// Pointer for the reason above; nil is fine and means "no caller is asking".
	PerfRef *PerfRef
	// MacosUserArm is the macos-user launch's signal arm (macosuserarm.go), made by the front door
	// so the handler it injects (MacosUserRun) can hand the arm's session runner, Ending and
	// AgentStarting to the backend, and installed by Run's macos-user arm. A pointer for PerfRef's
	// reason: Options crosses the launchRunPipeline seam by value. nil installs no arm (a capture
	// act's launch, a test), whose handler never asks one: its signals keep their default action
	// (armMacosUser).
	MacosUserArm *MacosUserArm
	// perfReportOnce makes the timing report once per Run invocation — a
	// POINTER, not an embedded sync.Once, because Options is copied by value
	// (Run's own signature) and a copied lock is a vet copylocks error. Created
	// by initPerf, single-threaded, before either teardown arm exists; nil for
	// a timing-off launch, which never reports.
	//
	// The signal arm's stopJail is what makes the child exit, which unblocks
	// the normal exit arm while onTerminate is still running — both arms
	// reaching emitTimingReport is the ORDINARY interleaving on that path, and
	// the terminal should see one report and one Window A query, not two.
	perfReportOnce *sync.Once
	// perfWindowAOnce makes the Window A query run once per Run invocation, for
	// the same interleaving reason perfReportOnce exists — and as a SEPARATE
	// Once, because the two now sit on different gates. The query records, so
	// it runs behind every opt-in; the table prints, so it runs behind the typed
	// flags only. One Once could not serve both without putting the query back
	// on the reporting gate, which is the bug being fixed.
	//
	// Pointer, and nil for a timing-off launch, for the reasons above it.
	perfWindowAOnce *sync.Once
	// windowA is what that one query found — stashed so the report renders the
	// recorded answer rather than asking podman a second time. Written inside
	// perfWindowAOnce.Do and read only after a Do returns, which is what makes
	// it safe across the two arms' goroutines.
	windowA windowAResult
	// linger is the Window A probe's slot (lingerprobe.go): created by initPerf
	// with the collector, so a launch that records nothing arms nothing. A
	// pointer for perfWindowAOnce's reason — Options crosses seams by value.
	linger *lingerSlot

	// runtimeClientEnv is added to the environment of each session's runtime client
	// (runArmedSession) and nothing else. A launch in a herdr pane sets HERDR_AGENT here
	// (herdragent.go); it is never passed into the container.
	runtimeClientEnv []string
	// herdr is this launch's registration with the herdr pane it runs in (herdragent.go). Run
	// makes it before any signal arm, whose teardown releases it; nil registers nothing. A
	// pointer for linger's reason.
	herdr *herdrPane

	// Now is the clock seam. nil => time.Now.
	Now func() time.Time
	// ServiceReadyTimeout bounds each spawned host service's readiness wait
	// (endpoint publish / socket bind). 0 => serviceReadyTimeoutDefault (5s).
	// Injectable ONLY to shrink it in tests — the wait runs on the real wall
	// clock, so the default would otherwise cost the unit suite five real seconds
	// per unreachable daemon; production always uses the default.
	ServiceReadyTimeout time.Duration
	// ServiceTermGrace bounds how long a spawned host service's teardown waits
	// after SIGTERM before SIGKILLing its process group. 0 =>
	// serviceTermGraceDefault (5s). Injectable ONLY to shrink it in tests, for
	// ServiceReadyTimeout's reason: the wait is real wall clock, and the test that
	// pins the escalation notice would otherwise cost the suite five seconds.
	ServiceTermGrace time.Duration
	// Getenv reads environment variables. nil => os.Getenv.
	Getenv func(string) string
	// FetchModelList asks a platform's credential service for a region's model list, where no
	// selected pack and no user config gives a provider one (bedrockmodels.go,
	// docs/design/model-lists-and-pickers.md OQ-MM6). nil fetches nothing and so refuses nothing
	// for want of a list: every hand-built Options in a test, which must never reach AWS. The front
	// door installs DefaultFetchModelList, as it installs MacosUserRun.
	FetchModelList func(ModelListRequest) awsauthdaemon.ModelListAnswer
	// fetchedListNotes is the fetched-list lines this launch printed, so a channel composed twice
	// (an attach's rekey, macos-user's service retry) says each once.
	fetchedListNotes map[string]bool
	// LookPath resolves an executable on PATH (shutil.which). nil => real.
	LookPath func(string) (string, bool)
	// PodmanReadiness is the podman readiness gate's seams (podmanready.go,
	// docs/design/podman-reboot-readiness.md): the attempt runner, its clock, its sleep and
	// its interrupt. A zero field takes the real one; the attempt runner is never o.Exec,
	// whose kill-at-timeout is exactly what the gate exists to avoid.
	PodmanReadiness runtime.ReadySeams
	// podmanFacts is the gate's answer, the launch's one `podman info --format json`; every
	// later podman fact on the launch path reads it. nil until the gate answered, and on
	// every backend the gate does not cover.
	podmanFacts *podmanFacts
	// readiness is the gate's whole result, success or not, for the refusal's exit code and
	// the machine-wide launch line. nil when no gate ran.
	readiness *runtime.ReadyResult
	// launchRecord is this launch's machine-wide launch line (launchrecord.go), armed by Run
	// and written once. nil in a caller that never ran Run's top, which records nothing.
	launchRecord *launchRecord
	// Exec runs a short subprocess probe with a timeout in dir ("" = inherit)
	// with extra env entries ("KEY=VALUE", appended to the parent env). nil =>
	// real. Used for git identity, lsusb, runtime version/liveness probes.
	Exec func(argv []string, dir string, env []string, timeout time.Duration) ExecResult
	// StartDetached starts argv in its own session with stdio on /dev/null and returns
	// without waiting for it — the scratch remover's spawn (scratchremoval.go), which has
	// to outlive the launcher and must never hold its exit. A non-nil inherit becomes
	// the child's fd 3 (the remover's in-flight lock, scratchRemoverLock). nil =>
	// startDetached.
	StartDetached func(argv []string, inherit *os.File) error
	// scratchVolumes are the named scratch volumes THIS launch's argv mounts
	// (ScratchVolumeNames), set by the fresh podman path just before assembly and handed
	// to the remover by whichever teardown arm runs (startScratchRemoval). Empty on an
	// attach, which mounts nothing, and in tmpfs mode.
	scratchVolumes []string
	// scratchRemovalOnce makes the two teardown arms one remover spawn. A pointer for
	// perfReportOnce's reason: Options is copied by value.
	scratchRemovalOnce *sync.Once
	// durable is this launch's durable dir (durabledir.go): made by ensureDurableDir on a
	// fresh launch, read back from the running jail's environment on an attach, and read by
	// refreshJailBriefings and by the backend's $YOLO_DURABLE_DIR emission. Nil until one of
	// those two ran, which renders no durable line and exports nothing.
	durable *jailcontent.DurableDir
	// attachSkewNotice is the acknowledged skew an attach hands its briefing refresh
	// (attachskewbriefing.go, SK-D15): set by attachExisting just before refreshJailBriefings
	// and cleared after it, so no other briefing, a fresh launch's included, can carry it.
	attachSkewNotice *jailcontent.AttachSkew

	// acVersion memoizes the `container --version` probe for this launch. Unexported
	// and nil-by-default so every hand-built Options in a test starts unprobed; see
	// appleContainerVersion for why the answer must be shared rather than re-asked.
	acVersion *acVersionProbe
	// machineShares memoizes the macOS Podman Machine share-list read for this launch
	// (machineshares.go), which both bind-source pre-flights consult. nil = not read yet.
	machineShares *machineSharesProbe
	// macosCtxSiting is the macOS layout the macos-user context-mount siting reads
	// (macosCtxLinks). nil, every real launch, is a default macOS install's
	// (macosuser.DarwinContextSiting); a test sets it to state the facts a Linux machine cannot
	// produce, such as a source outside every writable place.
	macosCtxSiting *macosuser.ContextSiting
	// Stdout/Stderr receive the human output (console.print goes to stderr in
	// rich by default for status; run() uses console (stdout) for most lines).
	// nil => os.Stdout / os.Stderr.
	Stdout io.Writer
	Stderr io.Writer
	// JailStdout/JailStderr receive the JAIL'S OWN lines up to its ready: what the runtime client
	// starting the container and pid 1 print, which the keeper relays apart from its own
	// (keeperframe.go's frameJailStdout and frameJailStderr) — a runtime that refused the
	// container, a boot that failed. nil => os.Stdout / os.Stderr, the process's own streams, where
	// they always went. A build jail's act hands writers of its own, so that a jail that stopped
	// before its build line can be relayed with what it said (cli's jailTail, PPX-D39).
	JailStdout io.Writer
	JailStderr io.Writer
	// OnJailReady, when non-nil, is called once, when the keeper relays the jail's boot done (its
	// ready frame): after the last of the jail's lines that reaches JailStdout and JailStderr. nil on
	// every launch but a build jail's, whose act tells a boot that went on to be done from one that
	// stopped by it (cli's jailTail).
	OnJailReady func()
	// SessionStdout is where the jail's FIRST SESSION's standard output goes. nil is this process's
	// stdout, through the terminal proxy when stdin is a terminal — an agent's session, whose output
	// is the product. A caller whose jail prints progress for another command names its own writer,
	// and the session then runs off the proxy, its stdin and stderr still this process's
	// (ttyproxy.Observer.Stdout): the host floor's capture and build jails, which run before a
	// `yolo host` launch execs an agent whose stdout is routinely parsed, take this process's stderr
	// for it and for JailStdout alike (internal/cli's hostJailStdout).
	SessionStdout io.Writer
	// Stdin is read for the config-change approval prompt. nil => os.Stdin.
	Stdin io.Reader
	// Color enables ANSI styling in the human output.
	Color bool
	// IsMacOS / IsLinux override the compile-time platform. Both exist because
	// the argv assembly asks two different questions ("is this a mac host?" for
	// the mise named-volume, "is this a Linux host?" for podman's
	// --read-only-tmpfs) and a golden test must be able to pin each one
	// independently of the host it runs on. Reading paths.IsLinux /
	// paths.IsMacOS directly from assembly code bypasses the seam and makes the
	// golden argv host-dependent — that is exactly how
	// TestAssembleRunCmdPodmanLinuxGolden started failing on the macOS runner.
	IsMacOS bool
	IsLinux bool
	// Workspace is Path.cwd() — the directory whose jail is launched. "" => cwd.
	Workspace string
	// RepoRoot resolves the yolo-jail repo root for nix builds. Returns
	// (resolution, ok); ok=false is the degraded-launch branch (D2). The
	// resolution carries WHICH candidate produced the root, because a launch
	// reports its image source (reportImageSource) rather than leaving the reader
	// to infer it from a path. nil => default resolver.
	RepoRoot func() (reporoot.Resolution, bool)
	// PathExists tests filesystem presence. nil => os.Stat.
	PathExists func(string) bool
	// HostCASProbe answers "does this host path exist, is it a writable
	// directory, is it empty?" for L9's host-cache alias (hostcasalias.go).
	// nil => hostcas.DefaultProbe.
	//
	// A seam for the reason PerfLoggingConfig is one: without it the decision
	// reads the DEVELOPER's real ~/.cache, so a maintainer who happens to have a
	// pants store would get an extra -v on every fixture in this package and
	// nobody else would. Every argv golden is already immune by a second route
	// (goldenOptions' Getenv returns "" for HOME, so there is no cache root to
	// look under), and the two protections are deliberate: one keeps the
	// DECISION host-independent, the other keeps the FIXTURES so.
	HostCASProbe func(string) hostcas.Presence
	// BuildJailPrefix realizes the /opt/yolo-jail install prefix the launch
	// mounts into the jail, for a flake source that ships no prebuilt binaries
	// (a live checkout). Returns (storePath, nixStderrTail); "" is failure and
	// refuses the launch. nil => image.BuildJailPrefix.
	//
	// A seam because it is a multi-second subprocess on the ONE path the unit
	// tests cover densely (argv assembly): the golden argv has to be able to say
	// where the prefix came from without a nix build having run.
	BuildJailPrefix func(repoRoot string) (string, []string)
	// Getpid returns the current PID (owner-PID file, out-link name). nil =>
	// os.Getpid.
	Getpid func() int
	// PIDAlive probes whether a recorded PID is still running — the gate in
	// front of every kill/reap decision. nil => pidAlive. Injectable because a
	// test that hands real PIDs to a kill path is signalling real processes: a
	// PID reaped moments earlier can already have been RECYCLED (macOS wraps at
	// PID_MAX 99999, four orders of magnitude below Linux's default 4194304),
	// and a drain loop then SIGTERMs — and seconds later SIGKILLs — some unrelated
	// process, quite possibly a sibling `go test` binary.
	PIDAlive func(int) bool
	// IsTTYStdout / IsTTYStdin report tty-ness (the -t flag, the approval
	// prompt, the tty-proxy fallback). nil => real isatty.
	IsTTYStdout func() bool
	IsTTYStdin  func() bool
	// IsTTYStderr reports whether the launch stream is a terminal, which is what
	// decides whether a long step's progress redraws in place or is written as
	// lines (progressConfig). nil => real isatty on os.Stderr.
	IsTTYStderr func() bool
	// Progress, when non-nil, replaces the rendering progressConfig derives from
	// the stream — a test seam, so a call site can be shown to narrate a fake that
	// returns in microseconds (progress.Config.Immediate). nil on every real launch.
	Progress *progress.Config
	// CapturesDir resolves the machine-wide install-capture store, which every
	// launch binds :ro into the jail so a native launcher can MATERIALIZE an
	// already-captured install instead of downloading it (program-delivery.md §6.3;
	// entrypoint.CapturesDirEnv), and which a macos-user launch reads its entries from to stage
	// a root-owned copy of them (MacosUserCaptures). nil => paths.CapturesDir.
	//
	// RETURNING "" IS THE MEANINGFUL OVERRIDE, and it has one production caller:
	// `yolo capture` (internal/cli/capturehost.go) suppresses the mount for the
	// throwaway jail it runs the vendor installer in. Without that the launcher
	// inside a capture jail would find the previous capture of the very program it
	// is capturing, materialize it, and record a "new" capture of bytes no installer
	// run produced this time — which would also make re-capturing (§6.3's *update*:
	// "a NEW capture, on an explicit act") structurally impossible. Suppressing the
	// MOUNT rather than teaching the launcher a second exception makes it
	// unrepresentable: there is nothing in that jail to resolve against.
	//
	// It is also the pipeline's one mark of a CAPTURE JAIL (Options.captureJail, seal.go):
	// no fork or tree is built or delivered from inside one, and it is handed none of the
	// user config's `mise_tools` (FP-D19).
	CapturesDir func() string
	// MaterializeStorePackages realizes a `buildEnv` of the config's `packages:` for the
	// JAIL's platform and returns (profile store path, names nix has no build for, error)
	// — C4's host half. nil => realMaterializeStorePackages, which is
	// internal/darwinpkg's Materialize.
	//
	// A seam for the same reason MacosUserRun is one: it shells out to a real `nix build`
	// that can take minutes, so a unit test of the launch's DECISION (opt in? fall back?
	// refuse?) must be able to answer it without nix on the machine. Only ever called on
	// a launch that has already been found eligible.
	MaterializeStorePackages func(repoRoot string, packages []any) (string, []string, error)
	// BuildImageExtras realizes `.#yoloImageExtras` — C5's store-delivered replacement for
	// the image's `fullPackages` — and returns its store path. nil =>
	// realBuildImageExtras. A seam for the same reason MaterializeStorePackages is one,
	// and only ever called on a launch that already took the store-delivery fast path.
	BuildImageExtras func(repoRoot string) (string, error)
	// autoLoad is the image build/load itself. Unexported: it is not a CLI-facing seam,
	// it exists so autoLoadImage's own DECISIONS are assertable without nix and podman —
	// above all C4's, which is a single assignment (`extra = nil`) that silently reverts
	// the whole feature if it goes. nil => image.AutoLoadImage.
	autoLoad func(image.AutoLoadOptions) image.LoadResult
	// AutoCapture records what a vendor installer leaves behind for every program in
	// bins that the machine store has no entry for at platform — OQ-PD18's *"(d),
	// DEFAULT ON"*. It receives the selected packs' `via: "installer"` program names
	// and the JAIL's platform, blocks while it works, and never fails a launch.
	//
	// A seam because captureHost — which launches jails — lives in internal/cli, the
	// package that imports this one; the front door injects it, exactly as it does
	// MacosUserRun and CaptureOnTerminate.
	//
	// nil => no auto-capture, which is what every non-launch caller of Run gets. That is
	// a legitimate state and not a broken one: the store filling is an optimisation, and
	// a launch with an empty store installs the way it did before capture existed.
	//
	// The BINS are a parameter rather than something the handler re-derives, for the
	// reason MacosUserRun's packRoot is: the pack set is the pipeline's to resolve, and
	// making it an argument means the trigger cannot fire without one being decided. The
	// PLATFORM likewise — only the pipeline knows which backend is about to run, and the
	// one answer that looks right and is wrong is the host's own (containerJailPlatform).
	AutoCapture func(bins []string, platform string)
	// MacosUserCaptures picks, from the install-capture store at dir (CapturesDir's answer), the
	// entry the materialize path's own resolver chooses for each of bins at platform, and names
	// the store's other current entries there (kept) — the macos-user launch's half of hand-off
	// H4 (docs/plans/install-capture.md): the backend stages a root-owned copy of each picked
	// entry and names that store to its launchers (macosuser.StageCaptureCommands).
	//
	// A seam for AutoCapture's reason: the resolver and the receipt adapter it reads through live
	// in internal/cli, which imports this package. nil stages no capture, and every launcher on
	// that backend then downloads, as it did before H4.
	MacosUserCaptures func(dir string, bins []string, platform string) (stage []macosuser.CaptureEntry, kept []string)
	// MacosUserLaunchProbes answers macos-user's launch preconditions (macosuser.LaunchProbes)
	// for the arm's auto-capture, which runs only for a launch the backend will not refuse at its
	// first two steps (autoCaptureMacosUser, macosuser.PreflightLaunch). nil =>
	// macosuser.RealLaunchProbes, the launch's own probes, so production wires nothing; a test
	// stands a Mac in.
	MacosUserLaunchProbes func() macosuser.LaunchProbes
	// BuildForks builds, for every pinned fork in the request, the store entry the jail needs at
	// its platform when the machine holds none, and returns per bin the entry's key or why there is
	// none (docs/design/forked-programs-as-packs.md OQ-FP4, FP-D1, FP-D8); for a PATCHED fork it
	// runs the advance (docs/design/patched-forks.md §6, §7), which the request's Hand records. It
	// blocks while it works — waiting, bounded, for a build another launch is already running — and
	// never fails the launch.
	//
	// A seam for AutoCapture's reason: the build act launches jails, so it lives in internal/cli,
	// which imports this package; the front door injects it. nil builds nothing, and every fork
	// then reaches its jail with the reason that says so.
	BuildForks func(req ForkBuildRequest) map[string]entrypoint.ForkDelivery
	// BuildTrees is the TREE ARM's act (docs/design/patched-extensions.md §6.1, §8.1;
	// patchedtrees.go): for every patched extension in the request, the check and the advance
	// (unless the request says this launch builds nothing), then the per-launch copy of the good
	// build beside this launch's pack tree, by extension key. Injected for BuildForks' reason, and
	// nil builds and copies nothing, each tree reaching its jail with the reason that says so.
	BuildTrees func(req TreeBuildRequest) map[string]TreeDelivery
	// BuildSlot is the WHOLE fork-build slot's act (buildslot.go; docs/design/pi-extension-store-builds.md
	// XB-D10): BuildForks' and BuildTrees' work at once, every key of both in one pool under one
	// interrupt scope, returning both halves' answers. When set a fresh launch calls it in place of
	// the two; nil leaves them, one after the other. Injected for BuildForks' reason.
	BuildSlot func(req BuildSlotRequest) (map[string]entrypoint.ForkDelivery, map[string]TreeDelivery)
	// prewarmImage starts the image's own build beside the slot (imageprewarm.go); nil is
	// image.Prewarm, and a launch whose image step is a test's fake (autoLoad) warms nothing.
	prewarmImage func(image.AutoLoadOptions)
	// imageIdentity is this launch's image identity, evaluated once whether the prewarm or the image
	// step asks first (imageprewarm.go); nil evaluates it as the image step always did.
	imageIdentity *identityMemo
	// Sealed is THE SEAL (docs/design/forked-programs-as-packs.md FP-D9; seal.go): this launch is
	// a fork BUILD, whose command is arbitrary code from a repository a pack named, so the jail is
	// handed no credential and nothing that writes outside its own workspace and home. Every
	// CROSSING SITE — a place in this pipeline that hands the jail something of the host's: an env
	// pair, a bind, a started host service — asks it where it hands that thing over, rather than
	// the loaded config being narrowed, because much of the pipeline reads the user config file
	// directly. seal.go lists the sites. Set by the build act alone (internal/cli's forkbuild.go);
	// false for every other launch, `yolo capture` of an installer included.
	Sealed bool
	// SealedTree is, under the seal, the name of the PATCHED EXTENSION this launch builds, "" for a
	// fork's build: the jail is told it (entrypoint.TreeBuildEnv), since its selection is the
	// contributing pack alone (docs/design/patched-extensions.md PPX-D5, PPX-D30).
	SealedTree string
	// OnlyPacks, when non-nil, narrows this launch's `packs` entries to the ones named here, before
	// the selection closure runs (so the packs those entries need or fork still join). The build
	// act sets it to the fork and its configured base (FP-D9: a build that works only while some
	// unrelated pack is selected would not reproduce on a second machine). nil selects every entry,
	// the conventional local pack included, as every other launch does.
	OnlyPacks []string
	// MacosUserRun handles the runtime==macos-user native branch. It receives the resolved config,
	// workspace, selected agents, the post-`--` argv, the repo root, the staged pack
	// root, the dry-run flag, and the launch's composed profile/provider channel in
	// launch-env form, returning the process exit code. nil => the branch
	// prints an actionable "not wired" error (keeps run free of the macosuser +
	// darwinpkg deps; the front door injects the real handler).
	//
	// packRoot is the host-side staged pack tree (stagePacks' root). It is a PARAMETER
	// rather than something the handler re-derives, because the whole B-0 defect was
	// this backend running with no pack root at all: making it an argument means a
	// backend cannot be dispatched without one being decided.
	//
	// packEnv is the channel in the same spirit, one hoist later: the pack env fold, the
	// provider env vars and the two wire tables (YOLO_PROVIDERS,
	// YOLO_USE_PROFILES), composed above the dispatch and handed to whichever arm runs.
	// The container arm writes the same content into the channel file; this arm layers it into its
	// plan env and relays the wire tables to its bootstrap. Making it an argument means a
	// `-p` launch cannot be dispatched to this backend without the environment it
	// selected being decided.
	//
	// hostCtx is the third of that family and the one that carries HOST BYTES: the
	// composed /ctx context tree plus the record of what went into it (DP-L1,
	// macosctxtree.go). It is a PARAMETER for the strongest version of packRoot's reason
	// — composing it requires reading the invoking user's own config and home, which the
	// backend must not do (macosuser.HostContext states why), so the backend cannot be
	// dispatched without the host CLI having decided what crosses.
	MacosUserRun func(cfg *jsonx.OrderedMap, workspace string, agents, agentArgv []string, repoRoot, packRoot string, homeOverlay macosuser.HomeOverlay, hostCtx macosuser.HostContext, dryRun bool, packEnv *jsonx.OrderedMap, blocked []packload.BlockedTool, jailDaemons macosuser.JailDaemons) int
	// OnRuntimeResolved is called once after Run selects and validates its actual backend, before
	// backend launch/provisioning begins. An error stops the launch; callers that must preserve the
	// exact backend for teardown can record it without predicting from env or PATH. nil is normal.
	OnRuntimeResolved func(runtime string) error
	// CaptureOnTerminate folds this session's in-jail edits to capture-mode surfaces
	// into their overlay sidecars once the jail is down (E3). It receives the
	// workspace and the resolved runtime, and reads only HOST-side dirs — by
	// teardown the container may be gone. nil => no capture, which is a legitimate
	// state rather than a broken one: the fold is idempotent with the one the next
	// boot render performs, so skipping it costs visibility, never data.
	//
	// A seam because the capture engine lives in internal/cli (the parent package),
	// which imports this one — the front door injects it, exactly as it does
	// MacosUserRun.
	CaptureOnTerminate func(workspace, runtime string)
	// RestoreTerminal undoes whatever the front door did to the terminal for the
	// duration of the launch — the kitty tab's jail icon and colour, or the tmux
	// pane's. nil is legitimate: a launch not attached to a kitty or tmux terminal
	// changed nothing and has nothing to undo.
	//
	// ⚠ IT CANNOT BE A DEFER, AND IT WAS ONE. The front door calls
	// SetupJailIndicator and defers the restore it returns, which is correct on the
	// normal path and never runs on the signal one: ttyproxy's SIGINT/SIGHUP/SIGTERM
	// arm calls onTerminate and then os.Exit(128+n), and os.Exit does not run
	// deferred functions — a fact this file's emitTimingReport comment already states
	// for the report's sake. So Ctrl-C left the tab wearing the jail's icon and
	// colour for the rest of that terminal's life. Reported on a real host
	// 2026-09-19.
	//
	// The closure must be idempotent: BOTH arms call it, and which one runs is not
	// this package's business.
	RestoreTerminal func()

	// ioSysRoot is the filesystem root the disk resolver reads the mount table and sysfs
	// under (internal/ioprio.Resolve, for noteIOPriority): "" is the real root, and a test
	// points it at a fake tree.
	ioSysRoot string

	// forkPinned is this launch's forks with what the fork lock says each is pinned to, read once
	// above the backend dispatch (noteForkPins) and acted on by the fork build trigger.
	forkPinned []packload.ForkPin
	// cachedGoodOwner is the explicit, one-launch selector for a selected patched program.
	cachedGoodOwner string
	// forkDelivered is what this launch hands its jail per forked program (forkDeliveriesFor).
	forkDelivered map[string]entrypoint.ForkDelivery
	// handedForks are the bins a patched fork's advance recorded itself, under its record lock
	// (ForkBuildRequest.Hand), which the trigger's own record of the rest leaves as they are.
	handedForks []string
	// patchedTrees are this launch's patched extensions a jail is delivered (PPX-D35), read above
	// the dispatch (notePatchedTrees), and treeDelivered what the tree arm handed the jail for each, by
	// extension key (treeDeliveriesFor).
	patchedTrees  []packload.Fork
	treeDelivered map[string]TreeDelivery
	// patchedAct is this launch's act interrupt (actInterrupt, PF-D57), handed to the fork builds
	// and the tree arm alike.
	patchedAct *ActInterrupt
}

// jailStdout is where the jail's own standard output goes (JailStdout): the writer a caller named,
// or this process's stdout.
func (o *Options) jailStdout() io.Writer {
	if o.JailStdout != nil {
		return o.JailStdout
	}
	return os.Stdout
}

// jailStderr is where the jail's own standard error goes (JailStderr): the writer a caller named,
// or this process's stderr.
func (o *Options) jailStderr() io.Writer {
	if o.JailStderr != nil {
		return o.JailStderr
	}
	return os.Stderr
}

// sessionStdout is where the first session's standard output goes (SessionStdout): the writer a
// caller named, or this process's stdout.
func (o *Options) sessionStdout() io.Writer {
	if o.SessionStdout != nil {
		return o.SessionStdout
	}
	return os.Stdout
}

// captureConfigOnTerminate runs the injected E3 capture for a jail that has just
// been torn down.
//
// It swallows everything. The hook already warns-and-proceeds internally; this is
// the second guard, for the case where the hook itself is what fails. A teardown
// that could be blocked by an observability fold would be a worse bug than the
// stale `yolo config diff` the fold exists to fix.
//
// Idempotent, so calling it from both teardown paths (the signal/window-close
// onTerminate and the normal exit tail, which both run when a session is
// interrupted) folds the same edits against the same baseline and converges.
func (o *Options) captureConfigOnTerminate(rt string) {
	if o.CaptureOnTerminate == nil {
		return
	}
	defer func() { _ = recover() }()
	o.CaptureOnTerminate(o.Workspace, rt)
}

// timingRecording is the RECORDING gate: does this launch collect spans and
// append them to <workspace>/.yolo/host-perf.log as they happen? EVERY opt-in
// turns it on — the two explicit flags, either host-process env opt-in
// (YOLO_TIMING for this surface, YOLO_VERBOSE for the global flag's published
// form, design D1), and the persistent `perf_logging` user config (read once by
// fillDefaults into perfLoggingOn).
//
// Pure, so the gate is table-testable without launching anything; called through
// Getenv so injected environments behave exactly like real ones.
func (o *Options) timingRecording() bool {
	return o.timingReporting() || o.perfLoggingOn ||
		o.Getenv(paths.TimingEnv) != "" || o.Getenv(paths.VerboseEnv) != ""
}

// timingReporting is the REPORTING gate: does this launch PRINT — the span table
// at exit, the Window A attribution line below it, and the in-container half
// (the YOLO_JAIL_TIMING=1 argv pair and the bash timers it switches on, which are
// print-only; the jail appends to its own ~/.yolo-perf.log either way)?
//
// Only the two EXPLICIT per-invocation flags say yes (D12). A persistent setting
// — `perf_logging: true`, or YOLO_TIMING/YOLO_VERBOSE exported in a shell
// profile — records and stays quiet, because it is "always on" and a ~25-line
// table at every jail quit scrolls away whatever the user was reading. The
// principle, whenever a new opt-in has to be classified: an explicit
// per-invocation flag prints; a persistent setting records silently.
//
// What the quiet path keeps: the file (above), the live slow-span notices
// (newTimingLog — low-volume, and they name a culprit while the user is still
// waiting), and one dim line at exit naming the file (noteTimingLogLocation).
func (o *Options) timingReporting() bool {
	return o.Timing || o.Verbose
}

// HostPerfLogName is the host half's file, under <workspace>/.yolo/ beside
// boot.log (design D2). The jail half is yolo-perf.log, bind-mounted from
// <workspace>/.yolo/home/, so one directory holds both halves of one launch.
const HostPerfLogName = "host-perf.log"

// PerfRef is a one-field holder a caller passes in to be handed the collector
// Run constructs. It exists because Options crosses the launchRunPipeline seam
// by value, so the front door cannot see a pointer the callee assigns.
type PerfRef struct{ Log *perf.Log }

// initPerf constructs the collector when the gate is on. See newTimingLog for
// the sinks; call once, after the early refusals have had their say — a
// refused launch writes no file.
func (o *Options) initPerf(cname string) {
	notice := func(msg string) { o.pr(o.Stderr).printf("[dim]yolo: %s[/dim]", msg) }
	slow := notice
	if o.subLaunch() {
		// A CAPTURE OR BUILD JAIL'S LAUNCH NAMES NO SLOW SPAN: its stream is its parent's record, not
		// a terminal anyone watches (a build's is its launch's log alone, internal/cli's
		// buildreport.go), and the parent's own progress line already says it is waiting. The file
		// still records every span.
		slow = nil
	}
	o.Perf = newTimingLogWith(o.timingRecording(), o.Workspace, cname, o.Stderr, notice, slow)
	if o.Perf != nil {
		o.perfReportOnce = &sync.Once{}
		o.perfWindowAOnce = &sync.Once{}
		o.linger = newLingerSlot()
	}
	// Publish it to the CALLER, which cannot otherwise see it: Options is passed
	// BY VALUE (launchRunPipeline's seam), so a collector constructed here is
	// invisible to the front door that wrapped this call. The same reason
	// perfReportOnce is a *sync.Once rather than a value.
	//
	// WHAT THIS FIXES, and it is a measurement hole rather than a style point:
	// `yolo run`'s deferred title restore spanned itself as
	// process.title_restore, on the front door's own copy of Options, whose Perf
	// is nil forever. Span on a nil *Log is a silent no-op, so the span never
	// fired and design H6 — the last unmeasured step between the report printing
	// and the prompt returning — stayed unmeasured while looking instrumented.
	if o.PerfRef != nil {
		o.PerfRef.Log = o.Perf
	}
}

// newTimingLog builds the collector behind every timing gate, wiring two sinks:
//
//   - the file sink at <workspace>/.yolo/host-perf.log (design D2) — events
//     land there AS THEY HAPPEN, so a hang is diagnosable by tail and the
//     signal arm's os.Exit still leaves a record;
//   - the slow-span notice — one line per span past perf.SlowSpanThreshold,
//     naming the culprit the moment it finishes (how the line is rendered is
//     the caller's business; the run pipeline dims it), SILENT for the window
//     in which the terminal belongs to the container (slowSpanNoticeSink).
//
// The clock is time.Now, never an Options.Now seam, by rule (design D8): that
// seam exists so tests can freeze time, and a span system built on a frozen
// clock reports 0.000s everywhere under test.
func newTimingLog(enabled bool, ws, cname string, stderr io.Writer, notice func(string)) *perf.Log {
	return newTimingLogWith(enabled, ws, cname, stderr, notice, notice)
}

// newTimingLogWith is newTimingLog with the slow-span notice apart from the file sink's: slow nil
// wires no slow-span sink, which a sub-launch's collector has none of (initPerf, subLaunch).
func newTimingLogWith(enabled bool, ws, cname string, stderr io.Writer, notice, slow func(string)) *perf.Log {
	if !enabled {
		return nil
	}
	sinks := []perf.Sink{hostPerfFileSink(ws, cname, stderr, notice)}
	if slow != nil {
		sinks = append(sinks, slowSpanNoticeSink(slow))
	}
	return perf.New(time.Now, sinks...)
}

// subLaunch reports whether this launch is one another launch runs — a capture jail's or a fork
// build jail's — by the one switch that makes it one: the capture store's mount suppressed
// (CapturesDir returning "", capturehost.go), which also keeps a build from starting another
// (forkDeliveriesFor).
func (o *Options) subLaunch() bool { return o.CapturesDir != nil && o.CapturesDir() == "" }

// slowSpanNoticeSink is the live "who is doing it" line — one notice per span
// past perf.SlowSpanThreshold — AND THE WINDOW IN WHICH IT SAYS NOTHING.
//
// THE BUG THIS FIXES, and it is housekeeping.go's property 2 arriving through a
// second door. Once the container is spawned the pty is attached, so BOTH host
// streams are the container's: a dim line from the launcher lands on top of
// whatever the agent's TUI is drawing. housekeepingNote already refuses to write
// to the terminal for exactly that reason — but the housekeeping slot is also a
// SPAN, and the slot's own `housekeeping.slot took 62.891s` went straight to
// stderr a minute into a live session (measured on the maintainer's host,
// 2026-09-19: the last line of <ws>/.yolo/launch.log, over a running agent).
// Nothing is lost by the silence: every event is already in host-perf.log the
// moment it happens, and --timing's table renders it at the end.
//
// This is NOT a quiet mode (OQ-RO3). The notice is a diagnostic, not one of the
// disclosures the launch may never suppress — those all print before the spawn,
// where the terminal is still the launcher's — and the window closes again the
// instant the child gives the terminal back, so the teardown notices that name a
// slow quit (the whole reason this sink exists) still print.
//
// THE WINDOW is opened by child.spawned and closed by EITHER child.exited or
// child.termios_restored, whichever arrives first, because the two teardown arms
// order them differently: the normal arm exits then restores, while the signal
// arm restores termios BEFORE running onTerminate (ttyproxy's terminate case),
// and the `terminate.*` spans that arm times are precisely the ones a user
// waiting through a slow Ctrl-C needs named. A fresh launch opens it earlier, at
// jail_main.spawned: the main process's boot is relayed to the terminal from then
// (jailmain.go), and jail_main.exited closes it like child.exited.
func slowSpanNoticeSink(notice func(string)) perf.Sink {
	var childHoldsTerminal atomic.Bool
	return func(e perf.Event) {
		if e.Kind == perf.KindMark {
			switch e.Name {
			// jail_main.spawned: the main process's boot is relayed to the terminal from its
			// spawn (jailmain.go), which is the stretch the container's own client held it
			// before the main process became a hold.
			case "child.spawned", "jail_main.spawned":
				childHoldsTerminal.Store(true)
			case "child.exited", "child.termios_restored", "jail_main.exited":
				childHoldsTerminal.Store(false)
			}
			return
		}
		if e.Kind != perf.KindEnd || e.Dur < perf.SlowSpanThreshold {
			return
		}
		if e.Name == "shutdown.window_a.client_exit" {
			// noteLingeringClient's line says the same number AND what the client
			// was blocked in; a bare "took" line above it is noise.
			return
		}
		if childHoldsTerminal.Load() {
			return // the terminal is the container's; the file sink already has it
		}
		notice(fmt.Sprintf("%s took %.3fs", e.Name, e.Dur.Seconds()))
	}
}

// hostPerfFileSink is the <workspace>/.yolo/host-perf.log sink, or a no-op sink when that
// directory may not exist.
//
// THE DIRECTORY IS THE POINT. paths.OpenWorkspaceStateFile creates its parent (through
// paths.EnsureWorkspaceStateDir), so this constructor is a creator of <workspace>/.yolo — and `yolo stop` reaches it (TimingLogFor) with the cwd as
// the workspace, outside the launch pipeline and therefore behind no launch guard. A
// `yolo stop` typed in the home, on a machine with `perf_logging: true`, minted a stray
// ~/.yolo/host-perf.log: a marker that hijacks workspaceRoot()'s upward walk for every later
// `yolo config` verb run anywhere below the home (internal/paths/workspacescope.go states
// both hazards).
//
// SKIP RATHER THAN REFUSE, and that asymmetry with the launch guard is deliberate: the perf
// log is best-effort by this package's contract — "a jail is never refused over its timing
// log" — so a directory yolo may not write is one more reason not to write, never a reason
// to fail the command the user actually asked for. The skip is disclosed through the same
// notice channel the slow-span lines use, because a timing log that silently is not there is
// the shape this repo calls a silent skip.
func hostPerfFileSink(ws, cname string, stderr io.Writer, notice func(string)) perf.Sink {
	if !paths.WorkspaceStateDirAllowed(ws) {
		notice("not recording timings: " + paths.WorkspaceStateDir(ws) +
			" may not exist (this directory is not a workspace)")
		return func(perf.Event) {}
	}
	// Beneath a root on `.yolo`, never by path (paths.OpenWorkspaceStateFile): the directory is
	// jail-writable, and a link the last jail left at the name would take the trim's rewrite
	// and every appended span to the host file it names.
	f, err := paths.OpenWorkspaceStateFile(ws, HostPerfLogName, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		if stderr != nil {
			fmt.Fprintf(stderr, "yolo: timing log unavailable at %s: %v (continuing without it)\n",
				filepath.Join(paths.WorkspaceStateDir(ws), HostPerfLogName), err)
		}
		return func(perf.Event) {}
	}
	return perf.FileSinkTo(f, cname, time.Now())
}

// TimingLogFor is the subcommand-facing constructor: the same RECORDING gate and
// sinks the run pipeline wires, for a command that is not the run pipeline
// (`yolo stop` today — the tool you reach for when a session is ALREADY wedged,
// and therefore the last place that should be unmeasured).
//
// It answers "record?" only. Whether the caller PRINTS what it recorded is the
// caller's decision under D12, because only the caller knows whether a flag was
// typed on this invocation: `yolo stop --verbose` reports, an inherited
// YOLO_VERBOSE=1 records in silence.
func TimingLogFor(ws string, getenv func(string) string, stderr io.Writer) *perf.Log {
	enabled := getenv(paths.TimingEnv) != "" || getenv(paths.VerboseEnv) != ""
	return newTimingLog(enabled, ws, runtime.FromWorkspace(ws), stderr, func(msg string) {
		fmt.Fprintf(stderr, "yolo: %s\n", msg)
	})
}

func fillDefaults(o *Options) {
	// o.Network is deliberately NOT defaulted: "" is "no --network typed", and the
	// bridge default belongs to resolveNetMode, below the config (G25).
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.PerfLoggingConfig == nil {
		o.PerfLoggingConfig = config.PerfLoggingEnabled
	}
	// The persistent opt-in is read HERE, once, single-threaded, before any span
	// exists — so the gate call sites downstream stay a field read rather than a
	// read of a file on every launch. It lands in its OWN field: folding it into
	// o.Timing (what this did until D12) erased the difference between "the user
	// typed --timing just now" and "this machine always records", and that
	// difference is exactly what decides whether the table prints. Skipped when a
	// flag already answers the question — the file read buys nothing then.
	if !o.timingReporting() {
		o.perfLoggingOn = o.PerfLoggingConfig()
	}
	if o.LookPath == nil {
		o.LookPath = func(name string) (string, bool) {
			p, err := exec.LookPath(name)
			return p, err == nil
		}
	}
	if o.Exec == nil {
		o.Exec = realExec
	}
	if o.StartDetached == nil {
		o.StartDetached = startDetached
	}
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	if o.JailStdout == nil {
		o.JailStdout = os.Stdout
	}
	if o.JailStderr == nil {
		o.JailStderr = os.Stderr
	}
	if o.Stdin == nil {
		o.Stdin = os.Stdin
	}
	if o.Workspace == "" {
		if wd, err := os.Getwd(); err == nil {
			o.Workspace = wd
		} else {
			o.Workspace = "."
		}
	}
	if o.PathExists == nil {
		o.PathExists = func(p string) bool {
			_, err := os.Stat(p)
			return err == nil
		}
	}
	if o.HostCASProbe == nil {
		o.HostCASProbe = hostcas.DefaultProbe
	}
	if o.BuildJailPrefix == nil {
		o.BuildJailPrefix = func(repoRoot string) (string, []string) {
			return image.BuildJailPrefix(repoRoot, o.Stderr)
		}
	}
	if o.RepoRoot == nil {
		// stderr is nil so the resolver stays silent: repo-root resolution IS
		// fatal on every backend now, but Run() owns the failure message, and the
		// two backends' messages differ (an image build vs. a native nix build).
		// Letting the resolver also print would double the "Cannot find repo
		// root" text.
		o.RepoRoot = func() (reporoot.Resolution, bool) { return resolveRepoRoot(o.Getenv, nil, o.Color) }
	}
	if o.Getpid == nil {
		o.Getpid = os.Getpid
	}
	if o.PIDAlive == nil {
		o.PIDAlive = pidAlive
	}
	if o.IsTTYStdout == nil {
		o.IsTTYStdout = func() bool { return isTTY(os.Stdout) }
	}
	if o.IsTTYStdin == nil {
		o.IsTTYStdin = func() bool { return isTTY(os.Stdin) }
	}
	if o.IsTTYStderr == nil {
		o.IsTTYStderr = func() bool { return isTTY(os.Stderr) }
	}
	if o.CapturesDir == nil {
		o.CapturesDir = paths.CapturesDir
	}
}

// inJail reports whether YOLO_VERSION is set. The host always sets
// YOLO_VERSION to a real (non-empty) version string inside a jail, so a
// non-empty read is the test-injectable signal.
func (o *Options) inJail() bool {
	return o.Getenv("YOLO_VERSION") != ""
}

// realExec runs argv with a timeout, capturing stdout/stderr as text. A missing
// binary or start failure yields Ran=false; a deadline overrun yields
// Timeout=true. dir sets the working directory (""=inherit); env entries are
// appended to os.Environ().
//
// THE TIMEOUT BOUNDS THE CALL, at timeout plus execDrainGrace. A call is over when the child
// has exited and both of its streams have ended, and a stream ends only when every process
// holding it has closed it, a grandchild the child forked included (a wrapper script's program,
// a CLI that starts a server). So the streams are pipes this function owns, not exec.Cmd
// writers, whose Wait reads them to their end however long that takes. At the deadline the
// child is killed and gets execDrainGrace to be reaped and to have what it wrote read; then the
// call returns with what was read. Before the deadline nothing is cut short: a grandchild that
// writes after its parent exited, and then closes the pipes, is read to the end. That is why
// this is not exec.Cmd.WaitDelay, whose clock also starts when the child exits and so cuts
// such a grandchild off long before the deadline.
//
// TIMEOUT KEEPS THE MEANING IT HAD: the child, or its output, outlived the deadline. A child
// that exited in time while a grandchild holds its output past it is a timeout, never a
// success, since what was read may not be all of it, and prune's callers would read a short
// success as an empty answer that means deletion (pruneRunFunc). A timed-out call carries no
// exit code.
//
// THE KILL REACHES THE DIRECT CHILD ALONE. The child stays in this process's group, so the
// terminal's Ctrl-C reaches it as it reaches the launcher. A grandchild it leaves runs on as it
// did when the call waited for it: its pipes stay open and are read to their end in the
// background, what arrives after the return discarded. Closing them instead would make its next
// write fail, and the SIGPIPE that raises kills a program that does not handle it, such as the
// rootless podman docs/design/podman-reboot-readiness.md finds finishing the post-boot refresh
// after its parent was killed; killed mid-refresh, it leaves the next podman call to start the
// refresh over. Reading on costs a goroutine and a descriptor per stream for as long as the
// grandchild holds it. A killed child that cannot die within the grace (one stuck in the
// kernel) is reaped whenever it does.
//
// timeout <= 0 means "no deadline" (matches the subprocess.run calls that pass no timeout,
// e.g. find_running_container / find_existing_container): the call waits for the child's exit
// and the end of both streams, as long as that takes.
//
// A NIX IT RUNS IS TRACKED (internal/nixchildren), such as the housekeeping slot's `nix store
// delete`, so the launch arm a signal ends the process through stops it
// (launchSignalArm.terminate) rather than leaving it running with no parent. It is released once
// its Wait returns; after a stop that release waits for the arm's exit, so a call with no
// deadline never returns then, and one with a deadline returns at it as timed out.
func realExec(argv []string, dir string, env []string, timeout time.Duration) ExecResult {
	if len(argv) == 0 {
		return ExecResult{}
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	if dir != "" {
		cmd.Dir = dir
	}
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	var stdout, stderr execOutput
	drained, release, err := startWithPipes(cmd, &stdout, &stderr)
	if err != nil {
		return ExecResult{Ran: false}
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		release()
		close(exited)
	}()
	var deadline <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		deadline = t.C
	}
	if bothBefore(exited, drained, deadline) {
		rc := 0
		if cmd.ProcessState != nil {
			rc = cmd.ProcessState.ExitCode()
		}
		return ExecResult{Stdout: stdout.take(), Stderr: stderr.take(), RC: rc, Ran: true}
	}
	_ = cmd.Process.Kill()
	grace := time.NewTimer(execDrainGrace)
	defer grace.Stop()
	bothBefore(exited, drained, grace.C)
	return ExecResult{Stdout: stdout.take(), Stderr: stderr.take(), Ran: true, Timeout: true}
}

// execDrainGrace is how long realExec waits, once it has killed a timed-out child, for the
// child to be reaped and its pipes to deliver what was already written. Both take a moment
// once the child is gone; a pipe a grandchild holds open is read no longer than this.
const execDrainGrace = 250 * time.Millisecond

// bothBefore waits for a and b to close, and reports whether both did before stop fired. A nil
// stop never fires.
func bothBefore(a, b <-chan struct{}, stop <-chan time.Time) bool {
	for a != nil || b != nil {
		select {
		case <-a:
			a = nil
		case <-b:
			b = nil
		case <-stop:
			return false
		}
	}
	return true
}

// execOutput collects one of a child's streams until realExec takes it. realExec may take it
// while the copy into it is still running (a grandchild holding the pipe past the deadline), so
// both sides lock, and a write after the take is discarded but reported as written, so the copy
// goes on reading the pipe.
type execOutput struct {
	mu    sync.Mutex
	b     strings.Builder
	taken bool
}

func (w *execOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.taken {
		_, _ = w.b.Write(p)
	}
	return len(p), nil
}

// take returns what was written so far, and makes every later write a discard.
func (w *execOutput) take() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.taken = true
	return w.b.String()
}

// startWithPipes starts cmd with its stdout and stderr on two pipes of this process's own and
// copies each into its writer. After a successful start the child, and whatever it forks, holds
// the only write ends, so a pipe ends exactly when the last of them closes it. drained closes
// when both copies have reached the end of their pipe. Each copy closes its read end there and
// nowhere sooner: the caller never cuts a pipe off under a process still writing to it.
//
// A nix starts through the tracked set (nixchildren.StartIfNix), and release is the caller's
// once cmd's Wait has returned.
func startWithPipes(cmd *exec.Cmd, stdout, stderr io.Writer) (drained <-chan struct{}, release func(), err error) {
	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		_ = outR.Close()
		_ = outW.Close()
		return nil, nil, err
	}
	cmd.Stdout, cmd.Stderr = outW, errW
	release, err = nixchildren.StartIfNix(cmd)
	_ = outW.Close()
	_ = errW.Close()
	if err != nil {
		_ = outR.Close()
		_ = errR.Close()
		return nil, nil, err
	}
	done := make(chan struct{})
	var copies sync.WaitGroup
	drain := func(w io.Writer, r *os.File) {
		defer copies.Done()
		_, _ = io.Copy(w, r)
		_ = r.Close()
	}
	copies.Add(2)
	go drain(stdout, outR)
	go drain(stderr, errR)
	go func() { copies.Wait(); close(done) }()
	return done, release, nil
}

// isTTY reports whether f is a real terminal via a TCGETS ioctl, NOT a
// character-device mode check — /dev/null is a char device but not a tty, and a
// mode check would wrongly add the container `-t` flag (observed divergence).
// The ioctl lives in the shared internal/tty package (platform split there).
func isTTY(f *os.File) bool {
	return tty.IsTerminalFile(f)
}

// NewDefaultOptions returns Options with the real platform predicate — the
// shape the CLI front door passes (then overrides the flags). It sets no flag:
// Network stays "" so a launch that typed no --network leaves the mode to the
// config (G25).
func NewDefaultOptions() Options {
	return Options{IsMacOS: paths.IsMacOS, IsLinux: paths.IsLinux}
}

// RunWithProxy launches argv under the platform-appropriate TTY proxy (Linux:
// internal/ttyproxy; other: a plain foreground exec) and returns the child exit
// code, or 1 on a launch error.
//
// IT LOST ITS LAST CALLER on 2026-10-05: it was the run-proxy seam the front door injected into
// macosuser, and a macos-user session now runs under its launch's signal arm instead
// (MacosUserArm.RunSession, macosuserarm.go), which this seam had none of. It goes, with
// proxy_other.go's runWithProxy, the next time those files and the comments naming it as the
// macos-user seam (proxy_linux.go, AGENTS.md) are edited together.
func RunWithProxy(argv []string) int {
	// A bare &Options{}: this seam has no collector, and a nil *Options would panic on the field
	// read — an empty Options' nil Perf is the intended no-op state.
	rc, err := runWithProxy(argv, nil, nil, &Options{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "launch failed: %v\n", err)
		return 1
	}
	return rc
}

// restoreTerminal runs the injected terminal restore, if there is one.
//
// A METHOD rather than a bare nil-check at the call site, for CaptureOnTerminate's
// reason: the seam is what a test can assert was REACHED, and a `if o.X != nil { o.X() }`
// written inline at one call site is a line the next call site forgets.
func (o *Options) restoreTerminal() {
	if o.RestoreTerminal != nil {
		o.RestoreTerminal()
	}
}
