package macosuser

import (
	"fmt"
	"github.com/mschulkind-oss/yolo-jail/internal/progress"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/nixchildren"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/provision"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// Deps are the injectable seams for the macOS-only orchestrator + the four
// macos-* command bodies. Every subprocess / filesystem / platform probe is a
// seam so the whole surface is unit-testable on Linux (the
// cli/check + ps deps-injection precedent). RealDeps wires the production implementations.
type Deps struct {
	// IsMacOS reports whether the host OS is darwin.
	IsMacOS func() bool
	// Geteuid returns the effective uid (0 under sudo).
	Geteuid func() int
	// Which reports whether a binary is on PATH.
	Which func(string) bool
	// SandboxUserExists reports `id <SANDBOX_USER>` returned 0.
	SandboxUserExists func() bool
	// SelfExe returns the path to the running yolo binary (os.Executable()),
	// staged into the root-owned state dir for the sandbox to self-exec as the
	// bootstrap (J2 §3).
	SelfExe func() string
	// GitConfig reads a host git config value best-effort ("" + false if unset).
	GitConfig func(key string) (string, bool)
	// Getenv reads an environment variable.
	Getenv func(string) string
	// HostUser is the invoking (admin) user ("" on failure).
	HostUser func() string
	// Run runs argv (inherit stdio) and returns the returncode. Used for the
	// sudo command lists + the bootstrap launch.
	Run func(argv []string) int
	// RunBash runs `bash -c <script>` and returns the returncode (unshare /
	// fix-permissions).
	RunBash func(script string) int
	// RunWithProxy launches argv under the TTY proxy and returns the agent exit
	// code.
	RunWithProxy func(argv []string) int
	// InstallRootFile writes content to a root-owned file (sudo mkdir+tee+chmod).
	InstallRootFile func(path, content, mode string) bool
	// MaterializeDarwin realizes `packages:` natively (nix build). ok=false with
	// a non-empty err aborts the run (DarwinPackagesError). A nil result with
	// ok=true means "no packages" (materialize not called).
	MaterializeDarwin func(repoRoot string, packages []any) (*Darwin, bool, error)
	// HostNix resolves the host's nix client for the sandbox PATH (hostnix.go). Asked once
	// per launch, after the floor build that proved that client works. nil delivers no nix.
	HostNix func() HostNix
	// NodeFloorMet reports whether a node the host can read on loginPath, outside home, meets
	// floor: AR-L3's host-side check (entrypoint.PackageFloorMeets), asked for each Node floor
	// the staged packs declare, so a floor already met starts no provisioning stage. nil
	// answers no for every floor, which starts the stage (the stage checks again).
	NodeFloorMet func(floor, loginPath, home string) bool
	// LockWorkspace takes the per-workspace launch lock and returns the release
	// (idempotent, never nil). nil means "no lock available", which degrades the launch
	// rather than refusing it — the same choice the container's own acquire makes.
	//
	// A SEAM because the implementation lives in internal/cli/run, which imports this
	// package: the front door wires it to run.AcquireWorkspaceLockFor. The lock is what
	// keeps two launches in one workspace from running two provisioning stages against
	// one npm prefix and one mise store; the container serialises the same window and
	// then attaches to the jail that won, which this backend cannot do.
	LockWorkspace func(workspace, cname string) func()
	// TakenIDs returns the union of existing UIDs+GIDs (macos_setup).
	TakenIDs func() map[int]struct{}
	// SetRandomPassword sets a random password on the sandbox account.
	SetRandomPassword func() bool
	// PathIsDir reports whether a path is an existing directory.
	PathIsDir func(string) bool
	// PathExists reports whether a path exists (broker socket, etc.).
	PathExists func(string) bool
	// ReadFile reads a file, reporting whether the content is KNOWN — an ABSENT
	// file is ("", true), because "it is not there" is knowledge; only a real read
	// error is ("", false). The provisioning stage's outcome is read through it —
	// see runProvisionStage for why a returncode alone cannot answer the question
	// it is asked, and why that distinction decides the answer.
	ReadFile func(string) (string, bool)
	// RemoveFile removes a file, reporting whether it is gone afterwards (removed
	// or already absent). Used to clear the previous launch's startup log before
	// the stage writes its own, so a stale marker cannot be read as this launch's.
	RemoveFile func(string) bool
	// GuestBinaries resolves the directory holding the darwin guest binaries
	// (jaildaemon.go's GuestBinaries) for a flake source: the bundle's prebuilt
	// bin/darwin-<arch>, else a `nix build .#guestPrefix`. Asked only when the launch has
	// a jail daemon to run. A SEAM because the build lives in internal/image, which this
	// package does not import; the front door wires it (internal/cli's guestBinariesSeam).
	// nil refuses a launch that has a daemon to run, naming why.
	GuestBinaries func(repoRoot string) (string, error)
	// StartBackground starts argv in the background, in a process group of its own, with
	// no terminal, and returns its handle: the stop that ends it (idempotent, never nil on
	// success), a channel closed when it exits, and what it wrote on its own stdout and
	// stderr. The jail-daemon supervisor's one seam (startBackgroundReal).
	StartBackground func(argv []string) (Background, error)
	// Out receives the human output. Rich markup is rendered to ANSI when
	// Color is set, else stripped to plain text.
	Out io.Writer
	// Progress is the rendering of the launch's slow steps on Out (the native nix
	// build). The zero value — line-oriented, silent for its first 2 s — is the
	// only one a real launch uses: the build streams nix's own stderr to the
	// terminal beside it, and a live redraw would tear on every line of that.
	Progress progress.Config
	// Color is the resolved color decision, made through the one gate (tty.Color:
	// requested, stdout a real terminal, no NO_COLOR veto). When false the printer
	// strips rich markup. It is forced OFF for the dry-run plan render (see
	// PrintPlan) and false on any non-TTY path — only interactive chatter gains
	// color.
	Color bool
}

// Options carries the run() inputs the front door resolves (workspace,
// config, agents, agent argv, repo src).
type Options struct {
	Workspace string
	Config    *jsonx.OrderedMap
	Agents    []string
	AgentArgv []string
	// BlockedTools are the `blocked-tool` contributions of the selected packs, which
	// core no longer supplies any of by default (config.defaultBlockedList is empty
	// since 2026-09-04). Passed in rather than re-derived here because the run pipeline
	// already loaded the packs, and a second load could disagree with the first.
	BlockedTools []packload.BlockedTool

	// HostHomeOverlay is the host-side composed CONTENT — skills and pack-declared
	// briefings — as a tree already laid out at the home-relative paths it belongs at
	// (Tree "" when there is nothing to deliver), with the destinations that tree holds.
	// The container backends mount each staged dir `:ro` at its destination; this one
	// has no mounts, so the tree is staged root-owned beside the packs and copied over
	// the sandbox home by the bootstrap, and the destinations become Seatbelt write
	// denies (homereadonly.go). See internal/cli/run/macoshomeoverlay.go for why it
	// crosses as a TREE rather than as a mapping the bootstrap would have to
	// re-implement.
	HostHomeOverlay HomeOverlay

	// HostCtx is the host-side composed CONTEXT tree and the record of what the run
	// pipeline put in it: pack `reads-host` grants and the user's source-bearing
	// `host_files` entries, laid out at the /ctx-relative paths the jail reads. The
	// container backends carry those bytes on a `:ro` mount; this one has no mounts, so
	// the tree is staged root-owned beside the packs and named to the bootstrap as
	// YOLO_CTX_ROOT (DP-L1, docs/design/declaration-parity.md §6.1).
	//
	// A PARAMETER and not something this package composes, for the reason HostContext
	// states: composing it is a read of the invoking user's own config and home, which is
	// the credential boundary, and the plan builder below it is pure. The zero value is a
	// launch that carried no host bytes.
	HostCtx HostContext

	// RepoRoot is the yolo-jail checkout root — passed to MaterializeDarwin as
	// the nix build root when `packages:` is non-empty. The native bootstrap
	// needs no source tree, only the flake root for darwin packages.
	RepoRoot string
	// HostPackRoot is the host-side staged pack tree (the run pipeline's stagePacks
	// root), copied into the root-owned state dir by the plan's stage commands and
	// named to the bootstrap as YOLO_PACK_ROOT. Empty means this launch staged no
	// packs, and the bootstrap is told nothing rather than pointed at an absent dir.
	HostPackRoot string
	// PackEnv is the launch's composed channel in launch-env form, for the ONE program this
	// invocation starts: the pack env fold, the provider env vars, the three wire tables
	// (YOLO_PROVIDERS, YOLO_PROFILES, YOLO_USE_PROFILES), and the hydrated env_sources LAST
	// — all of it already narrowed by the credential gate
	// (docs/reference/providers.md; the run pipeline's packChannel.launchEnv)
	// to the shared values plus what that program's own profile scopes to it. The run
	// pipeline composes the channel above the backend dispatch, so a `-p` launch composes
	// the same environment natively that it does in a container. Nil is the pre-channel
	// shape and layers nothing.
	//
	// Layered into the plan env BEFORE SandboxEnv. env_sources closes the map, so a user's
	// own dotenv entry beats a pack's default here, as it did when this package hydrated
	// env_sources itself. Its two wire tables are ALSO relayed into the bootstrap env
	// (BuildRunPlan), because the native bootstrap renders pack surfaces and derives from
	// them exactly as the container boot does.
	PackEnv *jsonx.OrderedMap
	// JailDaemons is what this launch runs in the guest: the supervisor's composed env,
	// payload included (jaildaemon.go). The run pipeline composes it from the daemons the
	// guest runs (internal/loopholes' JailDaemonsRunIn) and this package never selects.
	// The zero value runs none. GuestBinSource is filled here, not by the caller.
	JailDaemons JailDaemons
	// SandboxEnv is an optional caller-supplied env layered last, under only the
	// jail marker buildPlan sets over everything; nil is the common case.
	SandboxEnv *jsonx.OrderedMap
	DryRun     bool
}

// printer wraps the shared richtext renderer. When color is set the rich markup
// ([bold red]…[/bold red], [dim]…) is rendered to ANSI; otherwise it is stripped
// to plain text (the runcmd/check precedent; the dry-run ARTIFACTS are byte-
// pinned separately, and the dry-run plan render forces color=false).
type printer struct {
	w     io.Writer
	color bool
}

func (p printer) print(msg string)          { fmt.Fprintln(p.w, richtext.Render(msg, p.color)) }
func (p printer) printf(f string, a ...any) { p.print(fmt.Sprintf(f, a...)) }

// MacosSandboxEnv returns the extra env layered into the sandbox launch (git
// identity + TERM/COLORTERM/NO_COLOR). Host credentials never cross.
//
// NO_COLOR crosses for the container's reason (run.Options.noColorEnvArgs): a user
// who asked the host for no color asked it of the sandbox's programs too, and it
// crosses only when set — non-empty, the convention's definition
// (https://no-color.org).
func MacosSandboxEnv(deps Deps, cfg *jsonx.OrderedMap) *jsonx.OrderedMap {
	env := jsonx.NewOrderedMap()
	if term := deps.Getenv("TERM"); term != "" {
		env.Set("TERM", term)
	}
	if ct := deps.Getenv("COLORTERM"); ct != "" {
		env.Set("COLORTERM", ct)
	}
	if tty.NoColor(deps.Getenv) {
		env.Set(tty.NoColorVar, deps.Getenv(tty.NoColorVar))
	}
	for _, pair := range [][2]string{{"YOLO_GIT_NAME", "user.name"}, {"YOLO_GIT_EMAIL", "user.email"}} {
		if val, ok := deps.GitConfig(pair[1]); ok && val != "" {
			env.Set(pair[0], val)
		}
	}
	return env
}

// buildPlan starts from the sandbox env, layers the composed channel (PackEnv, which
// carries the gate-narrowed env_sources last), layers the caller's sandbox_env, sets the
// jail marker over all of them, then builds the plan.
// unenforcedResourceKeys is every `resources` key the warning below names: all of them,
// whatever their value, except an `io` that resolves to "normal" (that string, null or {}).
// That one makes no call on any backend, so it is already honored here, and naming it would
// report a declaration of nothing (docs/design/io-priority.md IO-D8). Any other `io` is named
// until setiopolicy_np ships, build step 5.
func unenforcedResourceKeys(res *jsonx.OrderedMap) []string {
	if res == nil {
		return nil
	}
	var keys []string
	for _, k := range res.Keys() {
		if k == "io" {
			v, _ := res.Get(k)
			if p, problems := ioprio.Parse(v, "resources.io"); len(problems) == 0 && !p.Declared() {
				continue
			}
		}
		keys = append(keys, k)
	}
	return keys
}

func buildPlan(deps Deps, opts Options, darwin *Darwin) RunPlan {
	env := MacosSandboxEnv(deps, opts.Config)
	// Trust the workspace's mise configs, for the same reason the container gets this on its
	// `-e` line: we ENTER this environment through a `yolo` command, so the launch env is ours
	// to set, and a repo-committed mise.toml under the workspace must not stop the agent with
	// an untrusted-config prompt it cannot answer.
	//
	// The env var rather than a `mise trust` call, deliberately — see boot.go's "Workspace mise
	// trust — REMOVED": the call writes a mark under ~/.local/state that is per-workspace and
	// re-earned every launch, while this is a fact about the tree that travels with the
	// environment. Scoped to the workspace, so a config outside it stays untrusted.
	//
	// This closes a real gap rather than mirroring the container for symmetry: macos-user had
	// NEITHER the env var nor a trust call, so its agent could hit a prompt the container path
	// never sees. Set BEFORE env_sources and SandboxEnv so a user who wants a different value
	// can still override it. At the `host` notch there is deliberately nothing — we do not own
	// that environment and have no business asserting trust in it.
	if opts.Workspace != "" {
		env.Set("MISE_TRUSTED_CONFIG_PATHS", resolvePathAbs(opts.Workspace))
	}
	// The composed profile/provider channel, ahead of env_sources — the container's
	// precedence, where the channel rides the `-e` base env and yolo-user-env.sh
	// (sourced later) overrides it. Before the channel crossed at all, a `-p` launch on
	// this backend validated the selector and then composed nothing: no variant env, no
	// provider env, no provider table for the derives. Layering it here is what makes
	// "a profile works on macos-user" a property of the pipeline rather than a second
	// implementation.
	if opts.PackEnv != nil {
		for _, k := range opts.PackEnv.Keys() {
			v, _ := opts.PackEnv.Get(k)
			env.Set(k, v)
		}
	}
	// The resolver's warnings (e.g. "env_sources file not found") must reach
	// deps.Out via the rich-stripping printer so the plan output includes them
	// (the container path wires the same warn callback; a no-op here would
	// silently drop the line).
	out := printer{w: deps.Out, color: deps.Color}
	// `per_side_paths` cannot be honoured here and must SAY so. Unlike
	// `workspace_readonly` — whose policy this backend can express natively, and now
	// does (SeatbeltProfile's readonlyRels) — a per-side path needs the host and the
	// sandbox to see DIFFERENT contents at one path. That is a mount-namespace
	// capability; Seatbelt filters permissions and cannot fork a path, so there is no
	// SBPL spelling of it and no prospect of one.
	//
	// The warning matters more since 2026-08-23, when `node_modules` joined the
	// DEFAULT shadow set (internal/cli/run/mounts.go): every Node workspace now gets
	// a protection on the container backends that is absent here, with nothing in the
	// config to hint at the difference. Shipping that silently would repeat exactly
	// the defect the workspace_readonly wiring above exists to fix.
	// See docs/reference/host-execution-from-the-workspace.md §5.5.
	if perSide := cfgStrList(opts.Config, "per_side_paths"); len(perSide) > 0 {
		out.print("[yellow]Warning: per_side_paths is NOT enforced on macos-user[/yellow] — " +
			"per-side shadowing needs a mount namespace and this backend has none, so " +
			"the host and the sandbox share these paths: " + strings.Join(perSide, ", "))
	}
	// THE REST OF WHAT THIS BACKEND CANNOT DO, said at the same boundary and for the
	// same reason as per_side_paths above. Each of these renders, validates and reads
	// exactly like it does on a container backend, and then does nothing here — which
	// is the silent-drop shape the sweep behind #39 found ten more of.
	//
	// resources: macOS has no cgroups and there is no VM to size. RLIMIT_AS is not what
	// --memory means (address space, not RSS — it breaks JITs and the Go runtime) and
	// RLIMIT_NPROC is per-USER, so it would collide across concurrent sessions on the
	// shared _yolojail account. A cap a user believes in but that does not hold is worse
	// than a documented absence, so this warns and will keep warning.
	if keys := unenforcedResourceKeys(cfgSection(opts.Config, "resources")); len(keys) > 0 {
		out.print("[yellow]Warning: resources are NOT enforced on macos-user[/yellow] — " +
			"macOS has no cgroups and there is no VM to size, so " + strings.Join(keys, ", ") +
			" are read and ignored. The agent runs with your user's own limits.")
	}
	// cache_relocations: the container path nests a bind inside ~/.cache. There are no
	// binds here, and the documented "just symlink it yourself" workaround does NOT
	// work either — the Seatbelt profile denies writes outside the workspace, the
	// sandbox home, /tmp and /var/folders, and denies reads under /Volumes. So a large
	// cold cache stays on the boot volume, which is the one outcome the feature exists
	// to prevent.
	if relocs := cfgSection(opts.Config, "cache_relocations"); relocs != nil && len(relocs.Keys()) > 0 {
		out.print("[yellow]Warning: cache_relocations are NOT implemented on macos-user[/yellow] — " +
			strings.Join(relocs.Keys(), ", ") + " stay on their original filesystem. " +
			"A host symlink is not a workaround here: the sandbox profile denies writes " +
			"outside the workspace and sandbox home, and denies reads under /Volumes.")
	}
	// NO env_sources HYDRATION HERE ANY MORE. This backend used to call
	// config.ResolveEnvSources itself and layer EVERY hydrated value — the second delivery
	// vehicle the credential gate's design counted (docs/reference/providers.md), bypassing
	// the credential gate. Its env_sources now arrive inside PackEnv, already narrowed by the gate to what
	// the launched program may see, and LAST in it (the run pipeline's launchEnv), which
	// is exactly where this layer used to sit: a user's own dotenv entry still beats every
	// channel value. One hydration per launch also means one set of "file not found"
	// warnings rather than two.
	if opts.SandboxEnv != nil {
		for _, k := range opts.SandboxEnv.Keys() {
			v, _ := opts.SandboxEnv.Get(k)
			env.Set(k, v)
		}
	}
	// THE JAIL MARKER, the one variable every container launch sets (`-e YOLO_VERSION=` in
	// internal/cli/run's commonEnvBlock) and this backend did not (docs/design/agent-footer.md
	// OQ-FT13). config.InJail() and the probes that copy it read it, so without it every
	// `yolo` the agent runs in here answered "host" from inside a Seatbelt sandbox, the agent
	// footer included, although this backend renders at the jail notch (render.Jail).
	//
	// The launcher's version, resolved as the container arm resolves it (version.Get over the
	// same repo root), so the in-sandbox banner reports it as a jail's does. Set LAST, after
	// env_sources and the caller's own env: whether this process is a jail is the launcher's
	// fact, and a composed layer that emptied it would turn every in-jail refusal off.
	//
	// It crosses in the session env file, so it reaches the provisioning stage and the agent.
	// The bootstrap reads that file too, but into its generator Env alone (entrypoint's
	// hydrate_session_env step), never into its process environment: config.InJail reads the
	// process, so it stays false there and the bootstrap's children inherit nothing. What
	// setting it changes on this backend is audited in the design's §2.2.
	env.Set("YOLO_VERSION", version.Get(opts.RepoRoot))
	selfExe := ""
	if deps.SelfExe != nil {
		selfExe = deps.SelfExe()
	}
	// AR-L3 (docs/reference/agent-program-runtimes.md): a Node floor the staged packs declare starts
	// the provisioning stage unless the host can show it met. Asked HERE, host-side, because the
	// plan builder below is pure and this backend has no mount namespace: the package-floor
	// nodes the sandbox's resolution reads first are the same files on the sandbox's PATH that
	// this process can read now. A dry run asks too, against a PATH with no floor materialized,
	// so its plan shows the stage for any declared floor, which is the direction the rule fails.
	home := SandboxHome()
	loginPath := SandboxPath(home, sandboxPathPrefix(darwin))
	var met func(string) bool
	if deps.NodeFloorMet != nil {
		met = func(floor string) bool { return deps.NodeFloorMet(floor, loginPath, home) }
	}
	floors := floorStageFor(opts.HostPackRoot, met)
	return BuildRunPlanWithDaemons(opts.Workspace, opts.Config, opts.Agents, opts.AgentArgv,
		selfExe, opts.HostPackRoot, opts.HostHomeOverlay, opts.HostCtx, env, darwin,
		opts.BlockedTools, opts.JailDaemons, floors)
}

// RunMacosUser launches agent_argv in the dedicated-user + Seatbelt sandbox.
// Returns the agent exit code (or 1 on a precondition/setup failure). dry-run
// builds + prints the plan and RETURNS before the macOS/root gates (so it
// runs on Linux CI); 1. the cheap preconditions (LaunchPreconditions: macOS,
// not-root, sandbox-exec, the sandbox user and its home, the workspace off every
// home and shared) BEFORE the up-to-30-min nix build; 2. the plan is built AFTER
// the gates (it reads host git config); 3. install profile + stage
// entrypoint; 4. bootstrap; 5. launch.
func RunMacosUser(deps Deps, opts Options) int {
	out := printer{w: deps.Out, color: deps.Color}

	// ONE SPELLING OF THE WORKSPACE FOR THE WHOLE BACKEND. BuildRunPlan resolves it too, and
	// has to — it is called directly — but the checks in THIS function run before it and would
	// otherwise judge a different path than the plan they gate. The ACL probe below is the one
	// that matters: given a symlink it reports on the target while naming the link in its
	// remedy, which is the "a remedy that cannot reach the path it names" shape this backend has
	// already been bitten by twice. See BuildRunPlan for the measurement and for the policy
	// bypass the same resolution closes.
	opts.Workspace = resolvePathAbs(opts.Workspace)

	// 0. Dry-run: build the plan, print it + invariants, execute nothing. Pure
	// (darwin=nil → no nix build), so CI and a Mac agent can both inspect it.
	// The plan (and the env-source warnings intermixed with it) stays plain text,
	// so force color OFF for the whole dry-run render — only interactive live
	// chatter gains color. No golden pins these bytes; this and PrintPlan's
	// forced-off printer are the whole guard.
	if opts.DryRun {
		plainDeps := deps
		plainDeps.Color = false
		// A plan render builds nothing, so the guest binaries are named at the prebuilt
		// spelling; a launch whose flake source ships none builds `.#guestPrefix` instead.
		if len(opts.JailDaemons.Names()) > 0 && opts.JailDaemons.GuestBinSource == "" {
			opts.JailDaemons.GuestBinSource = PrebuiltGuestBinDir(opts.RepoRoot)
		}
		plan := buildPlan(plainDeps, opts, nil)
		problems := PlanInvariants(plan)
		PrintPlan(deps.Out, plan, problems)
		if len(problems) > 0 {
			return 1
		}
		return 0
	}

	// THE LAUNCH'S PRECONDITIONS (preconditions.go): the machine and workspace conditions it
	// refuses without — cheap, and asked BEFORE the up-to-30-minute nix build, in the order
	// that list gives. The order is load-bearing (the in-home rule before the ACL probe), and
	// `yolo check` reports from the same list. The first one that does not hold refuses the
	// launch with its own message; nothing after it is asked.
	if c, unmet := unmetLaunchPrecondition(deps.launchProbes(), opts.Workspace); unmet {
		out.print(c.Refusal(opts.Workspace))
		return 1
	}

	// THE DAC PREFLIGHT for every context mount this launch delivers (ctxlinks.go;
	// docs/design/context-mounts.md §3.5), asked here, BEFORE the nix build, for the reason the
	// preconditions above are: it is cheap, it is a fact about this machine, and a refusal after
	// a half-hour build is the worst place to learn it. The same probes the plan carries
	// (BuildRunPlan → ContextPreflight over the same links), which PlanInvariants checks.
	if !runContextPreflight(deps, out, opts.HostCtx.Links) {
		return 1
	}

	// Materialize the native tool closure for THIS Mac's arch (the acceptance
	// bar): the FLOOR plus `packages:`. Runs nix on the HOST user before any
	// sandbox; on failure abort.
	//
	// UNCONDITIONAL SINCE THE FLOOR LANDED, and the change is not a refactor.
	// Until 2026-09-12 this was `if len(pkgs) > 0`, which was right when the
	// closure held nothing but what the user declared: an empty `packages:` meant
	// an empty profile, so there was nothing to build and nothing to put on PATH.
	// The floor makes an empty `packages:` the COMMON case that most needs the
	// build — a launch that skipped it is exactly the jail with no mise, no node
	// and no git that docs/design/macos-user-provisioning.md §1 is about.
	//
	// The cost is real and is the one OQ-P1 accepted: every macos-user launch now
	// needs a repo root and a nix that can evaluate this flake, where a bare
	// `yolo -- bash` with no config previously needed neither. run.Run's gate
	// moved with it, so the refusal still lands host-side with an actionable
	// message rather than three layers down in nix.
	//
	// A SIGNAL SENT TO YOLO ALONE WHILE IT RUNS NIX HERE STOPS THAT NIX (internal/nixchildren):
	// this build and the guest-binaries build below. This backend has no signal arm of its own
	// before the TTY proxy's, so the default action ended yolo here and left the nix building
	// with no parent. The arm stops that nix, then ends the launch with status 128+N, and is
	// removed once the two builds are done (disarmNix below), so the privileged steps and the
	// session keep the signal behavior they had.
	disarmNix := nixchildren.StopOnSignal()
	defer disarmNix()
	var darwin *Darwin
	pkgs := config.EffectivePackages(opts.Config, config.PlatformDarwin)
	// The nix build runs from the repo ROOT (the flake dir).
	//
	// A flake eval (the skip list) and a build of the whole floor: seconds warm,
	// up to half an hour cold, and the eval prints nothing while it runs — so the
	// step has a progress line.
	build := deps.Progress.Start(deps.Out, "Building the sandbox's tools with nix")
	d, ok, err := deps.MaterializeDarwin(opts.RepoRoot, pkgs)
	if ok {
		build.Done("done")
	} else {
		build.Done("failed")
	}
	if !ok {
		out.printf("[bold red]Could not materialize packages natively:[/bold red] %s\n"+
			"[dim]Fix the package, or use the Apple Container runtime "+
			"(runtime: \"container\") which builds them in a Linux VM.[/dim]", errStr(err))
		return 1
	}
	darwin = d
	// A SUCCESSFUL BUILD THAT CONTRIBUTES NO bin DIR IS ALSO FATAL, and it became
	// possible only when the floor made the closure unconditional. Before that an
	// empty profile was the honest answer to an empty `packages:`; now it means the
	// build returned something nothing could derive a PATH entry from, and
	// launching on it produces a sandbox with no mise, no node and no git — the
	// state this whole change exists to end, reached through a success path.
	//
	// PlanInvariants carries the same rule for the non-nil case. This one covers
	// the nil, which no invariant can see: a nil *Darwin reads as "not
	// materialized" and every store-bin loop over it is vacuously true.
	if darwin == nil || len(darwin.PathPrefix) == 0 {
		out.print("[bold red]The native package build reported success but produced no " +
			"tool directory.[/bold red]\n" +
			"Every macos-user launch builds a FLOOR — mise, node, git, ripgrep and the " +
			"rest of\nwhat the container image bakes — so an empty result is not a " +
			"launchable sandbox.\n\n" +
			"[dim]This is a yolo bug rather than a config error; `yolo run --dry-run` " +
			"prints the plan.[/dim]")
		return 1
	}
	// A DECLARED PACKAGE THAT DID NOT BUILD IS FATAL (A2 piece 1, shipped
	// 2026-09-04 — the ruling was made 2026-07-23 and the tree warn-and-skipped
	// for a year).
	//
	// The old behaviour printed the skipped names and launched anyway, which
	// masks the two cases that matter and are indistinguishable from the
	// message: a TYPO ("ripgrpe" is not an attr, so it is "skipped"), and a
	// package that genuinely has no darwin build. Either way the user asked for
	// a tool, the jail started without it, and the failure surfaced later as a
	// command that mysteriously does not exist.
	//
	// The eval still does NOT abort — the flake filters via
	// yoloUnavailablePackages and builds only the available set, which was the
	// original in-code objection to erroring. The error is raised HERE, host-side,
	// AFTER the eval, from the returned skip list. That ordering is the whole
	// trick: nix stays green, the CLI decides.
	//
	// EXPECTED-ABSENT entries are already gone: EffectivePackages dropped every
	// `platforms` entry excluding darwin before the build, so nix never saw them
	// and they cannot appear here. What remains is, by construction, a package the
	// user declared for THIS platform and did not get. PackagesExcludedOn supplies
	// the names only to explain the escape hatch in the message.
	if darwin != nil && len(darwin.Skipped) > 0 {
		sys := darwinSystemLabel(darwin)
		msg := "[bold red]These packages have no " + sys + " build:[/bold red] " +
			strings.Join(darwin.Skipped, ", ") + "\n\n" +
			"The jail did not start, because a package you declared would have been " +
			"silently missing inside it.\nThree ways forward:\n" +
			"  • a TYPO is the most common cause — an unknown attribute name is " +
			"indistinguishable from\n    a package with no build for this platform, " +
			"so check the spelling first;\n" +
			"  • mark it Linux-only and it becomes expected-absent here, still " +
			"installed in a container:\n" +
			"      {\"name\": \"<pkg>\", \"platforms\": [\"linux\"]}\n" +
			"  • or use the Apple Container runtime (runtime: \"container\"), which " +
			"builds them in a Linux VM."
		if excluded := config.PackagesExcludedOn(opts.Config, config.PlatformDarwin); len(excluded) > 0 {
			msg += "\n\n[dim]Already marked Linux-only and skipped without complaint: " +
				strings.Join(excluded, ", ") + ".[/dim]"
		}
		out.print(msg)
		return 1
	}

	// THE HOST'S nix CLIENT, for the sandbox (hostnix.go) — and SAID either way, because the
	// one backend that requires a host nix for every launch is the last place a user would
	// expect `nix: command not found`, and the reason it is absent is a fact about their host.
	if deps.HostNix != nil {
		darwin.Nix = deps.HostNix()
		if darwin.Nix.BinDir != "" {
			out.printf("[dim]nix: the host's client (%s) is on the sandbox PATH, as a daemon "+
				"client.[/dim]", darwin.Nix.BinDir)
		} else {
			out.printf("[yellow]nix is not available inside the sandbox:[/yellow] %s.",
				darwin.Nix.Absent)
		}
	}

	// THE GUEST BINARIES (OQ-DP8), resolved only when there is a daemon to run, and FATAL
	// when they cannot be: the launch has already told its agents these addresses are served
	// (the served set composed them), so starting the agent without them hands it a pointer
	// at a dead port — the state steps 3 and 4 exist to end. The same rule as a container
	// launch that cannot build its prefix.
	if len(opts.JailDaemons.Names()) > 0 && opts.JailDaemons.GuestBinSource == "" {
		if deps.GuestBinaries == nil {
			out.print("[bold red]This build cannot stage the sandbox's jail daemons[/bold red] " +
				"(no guest-binary resolver is wired).")
			return 1
		}
		src, err := deps.GuestBinaries(opts.RepoRoot)
		if err != nil {
			out.printf("[bold red]Could not provide the sandbox's in-jail binaries:[/bold red] %s\n"+
				"[dim]The jail daemons this launch runs (%s) are started by %s inside the "+
				"sandbox, and there is no copy of it to stage.[/dim]", errStr(err),
				strings.Join(opts.JailDaemons.Names(), ", "), JaildName)
			return 1
		}
		opts.JailDaemons.GuestBinSource = src
	}
	// The host nix builds are done: the signal arm goes before anything else runs.
	disarmNix()

	plan := buildPlan(deps, opts, darwin)
	problems := PlanInvariants(plan)
	if len(problems) > 0 {
		out.print("[bold red]macos-user run plan is not viable:[/bold red]")
		for _, p := range problems {
			out.printf("  ✗ %s", p)
		}
		out.print("\n[dim]Run `yolo run --dry-run` to inspect the full plan.[/dim]")
		return 1
	}

	// THE PER-WORKSPACE LAUNCH LOCK, held across the three privileged steps below and
	// released before the agent — never across the agent itself, which would make a
	// second terminal in the same workspace block until the first session ended, a
	// serialisation no backend has.
	//
	// Taken HERE, above the first side effect, and not merely around the stage: the
	// bootstrap generates the very script the stage execs, into the same per-workspace
	// sidecar, so a second launch bootstrapping between our bootstrap and our stage would
	// have us exec its script. The window the lock has to cover is bootstrap-through-stage
	// or it covers the wrong half.
	//
	// ON A LAUNCH THE LOCK IS ALREADY HELD by the time this runs: the run pipeline takes it
	// before it writes the per-workspace skills and briefing staging (run.holdLaunchLock),
	// because the home-overlay and context trees this backend copies below are built from
	// that staging, and a second launch rewriting it in between would hand this session the
	// other launch's content. The pack tree is not in that window: each launch stages its own
	// (docs/reference/pack-system.md#oq-pk2), so the copy below reads a tree no other launch
	// writes. The seam then hands back THAT hold (run.AcquireWorkspaceLockFor), so the release
	// below is what ends the launch's whole window, before the agent as always.
	release := func() {}
	if deps.LockWorkspace != nil {
		if r := deps.LockWorkspace(opts.Workspace, plan.Cname); r != nil {
			release = r
		}
	}
	defer release()

	out.print("[dim]Setting up the sandbox (Seatbelt profile + bootstrap) — sudo may " +
		"prompt for your password once.[/dim]")

	// 2. Install the root-owned Seatbelt profile (0444) + stage entrypoint.
	if !deps.InstallRootFile(plan.ProfilePath, plan.Seatbelt, "0444") {
		out.printf("[bold red]Could not write Seatbelt profile %s", plan.ProfilePath)
		return 1
	}
	for _, cmd := range plan.StageCommands {
		if deps.Run(append([]string{"sudo"}, cmd...)) != 0 {
			out.printf("[bold red]Could not stage entrypoint (%s).[/bold red]", shquote.JoinDisplay(cmd))
			return 1
		}
	}

	// 2.5 THE SESSION ENV FILE — everything this launch composed, delivered as a root-owned
	// 0600 file the sandbox account may read, instead of as words on three command lines
	// (envfile.go). Before the bootstrap, because the bootstrap is the first thing that reads
	// it: its argv names the file (SandboxEnvFileEnv), and the MCP requires_env gate it renders
	// every agent config through asks what the file holds.
	//
	// SWEPT ON EVERY EXIT PATH BELOW THIS LINE, including the failures: the file holds this
	// launch's credentials, and a launch that died at the bootstrap has no more use for them
	// than one whose agent exited. Best-effort — a session must not be reported as failed
	// because its env file could not be removed, and the next launch of this workspace
	// rewrites the same path.
	if !installSandboxEnvFile(deps, out, plan) {
		return 1
	}
	defer func() {
		for _, cmd := range plan.EnvFileRemoveCommands {
			_ = deps.Run(append([]string{"sudo"}, cmd...))
		}
	}()

	// 3. Bootstrap the sandbox user's home via the staged-yolo self-exec; ABORT
	// on failure. The binary was staged (fresh inode) by the StageCommands above;
	// no bootstrap FILE to install — the sandbox runs `yolo internal
	// darwin-bootstrap` with the generator env baked onto the argv.
	//
	// The failure line names the boot log, HEDGED: the log is this launch's only when the
	// bootstrap got as far as opening it. A refusal before that (the workspace-scope check in
	// `yolo internal darwin-bootstrap`), a sudo or exec failure, or a linked `.yolo` the open
	// refuses all leave the PREVIOUS launch's log at the path, which may end "boot complete".
	// So the line says how to tell (the log's first line carries the time it started) and where
	// the output is otherwise.
	if deps.Run(plan.BootstrapArgv) != 0 {
		out.print("[bold red]entrypoint bootstrap failed[/bold red] — the sandbox " +
			"user's shims/agent configs were not generated, so the agent " +
			"would not run correctly. Aborting. If it got as far as opening its log, its full " +
			"output and the reason it refused are in " + entrypoint.BootLogPath(plan.Workspace) +
			", whose first line carries the time it started; if that time is older than this " +
			"launch, it stopped before opening the log, and the lines above are all it printed.")
		return 1
	}

	// 3.5 THE PROVISIONING STAGE — the third privileged step, between the bootstrap and
	// the agent (docs/design/macos-user-provisioning.md half two). Confined under the same
	// Seatbelt profile the agent gets, which step 2 already installed; empty when the
	// config declares no tools, and then this costs nothing at all.
	if len(plan.ProvisionArgv) > 0 && !runProvisionStage(deps, out, plan) {
		return 1
	}

	// 3.6 THE JAIL DAEMONS (OQ-DP8/DP9, jaildaemon.go): the supervisor, confined, as the
	// sandbox account, started after the provisioning stage — it runs nothing the stage
	// installs, but a failed stage the human vetoed has already returned above — and before
	// the agent, so an address the launch composed is being bound by the time a client asks.
	// Stopped when the agent exits (LIFO: the supervisor first, then its env file swept).
	if len(plan.JailDaemonArgv) > 0 {
		stop, ok := startJailDaemons(deps, out, plan)
		if !ok {
			return 1
		}
		defer stop()
	}

	// 4. Launch under the TTY proxy — OUTSIDE the lock. Everything that writes the
	// per-workspace tier has happened; the agent's own writes are the same ones two
	// sessions on one workspace already share on every backend.
	release()
	return deps.RunWithProxy(plan.LaunchArgv)
}

// runContextPreflight asks the kernel, as the sandbox account, whether it can reach every
// context mount's source, and reports whether the launch should continue. Every link is asked
// in full rather than stopping at the first refusal, so one message names every source to move;
// a link's later probes are skipped once one fails, since its line is already written.
//
// No links asks nothing and prints nothing, which is every launch that declares no context mount.
func runContextPreflight(deps Deps, out printer, links []ContextLink) bool {
	if len(links) == 0 {
		return true
	}
	out.printf("[dim]Checking that %s can reach %d context mount source(s) — sudo may "+
		"prompt for your password.[/dim]", SandboxUser, len(links))
	var failed []ContextProbe
	refused := map[string]bool{}
	for _, p := range ContextPreflight(links, "") {
		key := p.Link.Dest + "\x00" + p.Link.Source
		if refused[key] {
			continue
		}
		if deps.Run(p.Argv) != 0 {
			refused[key] = true
			failed = append(failed, p)
		}
	}
	if len(failed) > 0 {
		out.print(ContextPreflightRefusal(failed))
		return false
	}
	return true
}

// runProvisionStage runs the confined provisioning stage and reports whether the launch
// should continue. false means the human asked to stop, or the stage REFUSED the launch.
//
// ⚠ A FAILING STAGE MUST NOT ABORT THE LAUNCH (§4 of the design doc), and the stage's
// RETURNCODE CANNOT TELL YOU WHETHER IT FAILED. That is the whole reason this is a
// function. The script completes with status 0 on every failure it survives — it records
// the banner, the red console line and the PROVISIONING FAILED marker in
// <workspace>/.yolo/startup.log that the next launch's briefing reports, and only an
// explicit `n` at the interactive prompt propagates a non-zero status.
//
// But `deps.Run` returns non-zero for a second, entirely different reason: THE STAGE
// NEVER RAN. sudo refusing authorization, sandbox-exec rejecting the profile, /bin/bash
// missing — each is an exec-layer failure nobody chose, and each is on the design doc's "what a Mac has to settle" list of
// things only a Mac can settle. This code used to state as fact that "the exit code says
// only whether the human asked it to" and return 1 on every non-zero, so on those paths a
// workspace that merely DECLARES mise_tools could not launch at all, and the message
// blamed the user for it — the exact inversion of the rule above.
//
// The discriminator is the marker, because the marker is written by the script and
// therefore exists only if the script ran:
//
//   - non-zero AND the marker is in this launch's log → the stage ran, its body failed,
//     and a human answered `n`. A deliberate veto: abort, as before.
//   - provision.RefusedStatus AND the marker → the stage ran and REFUSED the launch, which
//     the script does without asking anyone (a selected pack's Node floor nothing
//     satisfies, docs/reference/agent-program-runtimes.md OQ-AR3). Abort, naming it as a
//     refusal rather than a veto nobody gave; the reason is already on the console.
//   - non-zero AND no marker → the stage never reported anything, so the status came from
//     the exec layer. WARN and continue to the agent, per §4.
//
// The log is cleared first so "this launch's log" is a fact rather than a hope; the script
// truncates it one instruction later anyway, so clearing costs nothing. If the clear or
// the read fails we CANNOT prove the stage never ran, and the conservative direction is
// the one that honors a possible veto — so an unreadable log aborts, as it did before.
func runProvisionStage(deps Deps, out printer, plan RunPlan) bool {
	log := provision.StartupLog(plan.Workspace)
	cleared := deps.RemoveFile != nil && deps.RemoveFile(log)
	rc := deps.Run(plan.ProvisionArgv)
	if rc == 0 {
		return true
	}
	vetoed := true // the conservative reading; see the docstring.
	if cleared && deps.ReadFile != nil {
		body, ok := deps.ReadFile(log)
		vetoed = !ok || strings.Contains(body, provision.FailedMarker)
	}
	if vetoed && rc == provision.RefusedStatus {
		out.print("[bold red]Provisioning refused the launch.[/bold red] A selected pack " +
			"declares something this sandbox cannot provide; the reason is printed above " +
			"and in " + log + ".")
		return false
	}
	if vetoed {
		out.print("[bold red]Provisioning was aborted.[/bold red] The sandbox is set up " +
			"but its declared tools were not installed — the log is at " + log + ".")
		return false
	}
	out.print("[bold yellow]The provisioning stage could not be started.[/bold yellow] It " +
		"wrote nothing to " + log + ", so it never ran — sudo, sandbox-exec or the " +
		"profile, not the tools themselves. Launching anyway: the declared tools are " +
		"NOT installed in this sandbox.")
	return true
}

// PrintPlan renders a RunPlan for --dry-run (human-readable; rich markup
// stripped). Color is deliberately OFF here, on every path: the dry run is what
// CI and a Mac agent read to inspect a launch, and no golden pins its bytes, so
// this printer is the only thing keeping it plain
// (docs/reference/cli-color.md).
func PrintPlan(w io.Writer, plan RunPlan, problems []string) {
	p := printer{w: w, color: false}
	p.print("[bold]macos-user run plan[/bold] (dry-run — nothing executed)\n")
	p.printf("workspace:   %s", plan.Workspace)
	p.printf("session:     %s", plan.Cname)
	p.printf("profile:     %s", plan.ProfilePath)
	p.printf("staged yolo: %s", plan.StagedYolo)
	// Named even when empty: "this launch renders no packs" is the state that used to be
	// indistinguishable from "packs work here", so the dry run has to say which one it is.
	if plan.PackRoot == "" {
		p.print("packs:       [dim]none staged — no pack surfaces will be rendered[/dim]")
	} else {
		p.printf("packs:       %s", plan.PackRoot)
	}
	// THE HOST BYTES, named on the same rule and for the same defect one step over
	// (DP-L1). "this launch carried no host bytes" and "host bytes cannot cross on this
	// backend" were indistinguishable from the outside for as long as this backend
	// existed, and that indistinguishability IS the row the delivery closes — so a dry run
	// that simply omitted the tree when there was nothing in it would preserve it.
	//
	// It names the STAGED path, never the config's /ctx half: the destination is not a
	// constant either half may assume (entrypoint.CapturesDirEnv and YOLO_PACK_ROOT are the
	// same pattern), and on macOS the /ctx spelling names a directory that cannot be made
	// to exist without /etc/synthetic.conf and a reboot.
	if plan.CtxRoot == "" {
		p.print("host bytes:  [dim]none staged — `reads-host` surfaces and source-bearing " +
			"host_files compose from their lower layers[/dim]")
	} else {
		p.printf("host bytes:  %s [dim](root-owned; the sandbox reads it and cannot "+
			"write it)[/dim]", plan.CtxRoot)
	}
	// THE CONTEXT MOUNTS, named on the same rule: "this launch declares none" and "this
	// backend delivers none" were one statement until §4 step 4. Each is the link the agent
	// opens and the host folder behind it, at the STAGED path — never the /ctx spelling, which
	// names nothing on macOS.
	if len(plan.ContextLinks) == 0 {
		p.print("context:     [dim]no context mounts[/dim]")
	} else {
		for _, l := range plan.ContextLinks {
			p.printf("context:     %s/%s → %s [dim](%s, %s; a root-owned link, and the "+
				"profile decides access)[/dim]", plan.ContextDir, l.Rel(), l.Source, l.Mode(), l.Origin())
		}
	}
	p.printf("git identity: %s", gitIdentityRepr(plan.GitIdentity))
	// THE ENV FILE IS DISCLOSED BY NAME AND BY KEY, NEVER BY VALUE — and that is the
	// disclosure this dry run owes its reader rather than a redaction (envfile.go).
	// Everything the launch composed used to be visible on the argvs below; it now crosses
	// in this file, so a plan that simply stopped mentioning it would have made the dry run
	// LESS truthful about the launch, which is the failure this whole change exists to
	// avoid. Names, not values: a variable's name is a fact about the launch, its value is
	// the credential, and stdout is teed into <workspace>/.yolo/launch.log.
	if plan.EnvFile == "" {
		p.print("env file:    [dim]none — this launch composed no environment[/dim]")
	} else {
		p.printf("env file:    %s [dim](0600, root-owned, read by %s only)[/dim]",
			plan.EnvFile, SandboxUser)
		p.printf("  [dim]sets, values not shown:[/dim] %s",
			strings.Join(SandboxEnvFileKeys(plan.EnvFileContent), ", "))
	}
	// THE GUEST'S JAIL DAEMONS, named even when there are none, on the pack line's rule:
	// "this launch runs no jail daemon" and "this backend runs none" were the same statement
	// until OQ-DP8, and a dry run is how a user tells them apart.
	if len(plan.JailDaemonNames) == 0 {
		p.print("jail daemons: [dim]none run in the sandbox for this launch[/dim]")
	} else {
		p.printf("jail daemons: %s [dim](confined; %s supervise, as %s)[/dim]",
			strings.Join(plan.JailDaemonNames, ", "), JaildName, SandboxUser)
		p.printf("  guest bins: %s → %s", plan.GuestBinSource, GuestBinDir(plan.StagedDir))
		p.printf("  env file:   %s [dim](0600, root-owned, read by %s only)[/dim]",
			plan.DaemonEnvFile, SandboxUser)
		p.printf("  log:        %s [dim](the supervisor's own stdout and stderr)[/dim]",
			plan.SupervisorLog)
		p.printf("  [dim]sets, values not shown:[/dim] %s",
			strings.Join(SandboxEnvFileKeys(plan.DaemonEnvFileContent), ", "))
	}
	if plan.DarwinMaterialized {
		p.printf("darwin pkgs: %d store bin dir(s) on PATH", len(plan.DarwinPathPrefix))
		if len(plan.DarwinSkipped) > 0 {
			p.printf("  [yellow]skipped (no darwin build):[/yellow] %s", strings.Join(plan.DarwinSkipped, ", "))
		}
	} else {
		p.print("darwin pkgs: [dim]not materialized (dry-run — nix build skipped)[/dim]")
	}
	p.print("")

	p.print("[bold]── privileged commands (run via sudo) ──[/bold]\n" +
		"[dim]sudo may prompt for your password; it's forwarded through the " +
		"TTY proxy so you can answer inline.[/dim]")
	// The DAC preflight first, as the launch runs it: before the nix build, as the sandbox
	// account. Each is the whole argv, `sudo` included.
	for _, probe := range plan.ContextPreflight {
		p.print("  " + shquote.JoinDisplay(probe.Argv) + "  [dim](can " + SandboxUser + " " +
			probe.Access + " it?)[/dim]")
	}
	for _, cmd := range plan.StageCommands {
		p.print("  sudo " + shquote.JoinDisplay(cmd))
	}
	// The env-file steps, in the order they run and NAMED — the directory's mode and the
	// sandbox's read ACE are the whole of what keeps this file private, so a dry run that
	// hid them would hide the security property it is being read to check.
	for _, cmd := range plan.EnvFileCommands {
		p.print("  sudo " + shquote.JoinDisplay(cmd))
	}
	if plan.EnvFile != "" {
		p.printf("  sudo %s %s  [dim](content on stdin, never argv)[/dim]", teeBin, shquote.QuoteDisplay(plan.EnvFile))
		p.printf("  sudo %s 0600 %s", chmodBin, shquote.QuoteDisplay(plan.EnvFile))
	}
	for _, cmd := range plan.EnvFileGrantCommands {
		p.print("  sudo " + shquote.JoinDisplay(cmd))
	}
	p.print("  sudo " + shquote.JoinDisplay(plan.BootstrapArgv[1:]))
	// NAMED EVEN WHEN THERE IS NO STAGE, for the reason the pack line above is: "this
	// launch installs nothing" and "this backend cannot install anything" were
	// indistinguishable until half two, and a dry run that simply omitted the step would
	// keep them that way.
	if len(plan.ProvisionArgv) > 0 {
		p.print("  sudo " + shquote.JoinDisplay(plan.ProvisionArgv[1:]))
	}
	p.print("")

	section := func(title, body string) {
		p.printf("[bold]── %s ──[/bold]", title)
		p.print(strings.TrimRight(body, "\n"))
		p.print("")
	}
	section("Seatbelt profile", plan.Seatbelt)
	p.print("[bold]── bootstrap argv (self-exec as sandbox) ──[/bold]")
	p.print("  " + shquote.JoinDisplay(plan.BootstrapArgv))
	p.print("")
	if len(plan.ProvisionArgv) == 0 {
		p.print("[bold]── provisioning stage ──[/bold]")
		p.print("  [dim]skipped — no mise_tools declared, and no declared Node floor " +
			"the host could not show met[/dim]")
	} else {
		p.print("[bold]── provisioning stage (confined, before the agent) ──[/bold]")
		if why := plan.ProvisionFloors.Reason(); why != "" {
			p.print("  [dim]runs for: " + why + "[/dim]")
		}
		p.print("  " + shquote.JoinDisplay(plan.ProvisionArgv))
	}
	p.print("")
	if len(plan.JailDaemonArgv) > 0 {
		p.print("[bold]── jail-daemon supervisor (confined, beside the agent) ──[/bold]")
		p.print("  " + shquote.JoinDisplay(plan.JailDaemonArgv))
		p.print("")
	}
	p.print("[bold]── launch argv ──[/bold]")
	p.print("  " + shquote.JoinDisplay(plan.LaunchArgv))
	p.print("")
	if len(problems) > 0 {
		p.print("[bold red]plan invariant violations:[/bold red]")
		for _, pr := range problems {
			p.printf("  ✗ %s", pr)
		}
	} else {
		p.print("[green]✓ all plan invariants hold[/green]")
	}
}

// gitIdentityRepr renders the git-identity map as a dict repr, or a fallback
// string when there is no identity.
func gitIdentityRepr(m *jsonx.OrderedMap) string {
	if m == nil || m.Len() == 0 {
		return "(none — commits use no identity)"
	}
	return pyDictRepr(m)
}

// pyDictRepr renders an OrderedMap as a dict repr ({'k': 'v', …}), embedded in
// the dry-run plan for the git identity. Keys/values are string reprs.
func pyDictRepr(m *jsonx.OrderedMap) string {
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range m.Keys() {
		if i > 0 {
			b.WriteString(", ")
		}
		v, _ := m.Get(k)
		b.WriteString(reprStr(k))
		b.WriteString(": ")
		b.WriteString(reprStr(asStr(v)))
	}
	b.WriteByte('}')
	return b.String()
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// launchWriter is the writer a LAUNCH's Deps print through, published by the run
// pipeline for the length of one launch and nil everywhere else.
//
// WHY IT IS NOT SIMPLY os.Stdout. Everything the launcher prints is teed into
// <workspace>/.yolo/launch.log, and the tee is installed on the PIPELINE's writers
// (internal/cli/run/launchlog.go) — which this backend never took. So its half of the
// launch stream reached a terminal and nothing else: the dry-run plan, the Seatbelt
// profile, the three argvs, every `not enforced on macos-user` warning and every setup
// and teardown line, with the log holding that launch's header and trailer and nothing
// between them (docs/plans/handoff-macos-user-open-threads.md §7). A launch has no quiet
// mode by ruling and answers "too much on the terminal" by pointing at the file
// (OQ-RO3), which is half a bargain when a backend's disclosures never reach it — the
// pack read/exec banners among them, and those are the whole trust boundary.
//
// PUBLISHED RATHER THAN PASSED, which is what leaves `yolo macos-setup` and its three
// siblings alone. They build their Deps from this same RealDeps with no pipeline to take
// a writer from (internal/cli/commands.go), so a writer PARAMETER would have had to be
// threaded through every one of them; nothing publishes on their path, LaunchWriter
// answers os.Stdout, and not one of those call sites moves.
var (
	launchWriterMu sync.Mutex
	launchWriter   io.Writer
)

// SetLaunchWriter publishes w as the writer the next RealDeps resolves Out from, and
// returns the undo (never nil). The run pipeline calls it where it installs the launch
// log's tee and undoes it when the run block closes — the log file is closed there, so a
// writer that outlived it would be a half-closed tee.
//
// Mutex-guarded for SetCrossingSink's reason rather than for a launch's: one launch is
// one call in one process, but a test binary restores this from one test while another
// reads it, which a bare var cannot make safe.
func SetLaunchWriter(w io.Writer) func() {
	launchWriterMu.Lock()
	defer launchWriterMu.Unlock()
	prev := launchWriter
	launchWriter = w
	return func() {
		launchWriterMu.Lock()
		defer launchWriterMu.Unlock()
		launchWriter = prev
	}
}

// LaunchWriter returns the published writer, or os.Stdout when nothing published one —
// which is every `yolo macos-*` command, every caller outside a launch, and a launch
// whose log could not be opened (that failure degrades silently and leaves the launch
// the writers it already had).
func LaunchWriter() io.Writer {
	launchWriterMu.Lock()
	defer launchWriterMu.Unlock()
	if launchWriter != nil {
		return launchWriter
	}
	return os.Stdout
}

// RealDeps returns Deps backed by real subprocesses / filesystem. runProxy is
// the TTY-proxy launcher the front door
// supplies (internal/cli/run's runWithProxy is Linux/macOS-specific);
// materialize wires internal/darwinpkg's streaming nix build. Both are passed
// in so this package needs no build-tagged syscall dependencies. color is the
// resolved color capability (the caller's requested color AND a real TTY);
// it drives ANSI vs. plain output.
//
// Out is NOT a parameter, deliberately: it is resolved from LaunchWriter, so a launch's
// Deps take the run pipeline's teed stdout and every caller outside a launch keeps the
// process's own.
func RealDeps(runProxy func(argv []string) int, materialize func(repoRoot string, packages []any) (*Darwin, bool, error), color bool) Deps {
	return Deps{
		IsMacOS:           func() bool { return isMacOSReal() },
		Geteuid:           os.Geteuid,
		Which:             whichReal,
		SandboxUserExists: func() bool { return sandboxUserExistsReal(SandboxUser) },
		SelfExe:           selfExeReal,
		GitConfig:         gitConfigReal,
		Getenv:            os.Getenv,
		HostUser:          hostUserReal,
		Run:               runReal,
		RunBash:           runBashReal,
		RunWithProxy:      runProxy,
		InstallRootFile:   installRootFileReal,
		MaterializeDarwin: materialize,
		StartBackground:   startBackgroundReal,
		HostNix:           hostNixReal,
		NodeFloorMet:      entrypoint.PackageFloorMeets,
		TakenIDs:          takenIDsReal,
		SetRandomPassword: func() bool { return setRandomPasswordReal(SandboxUser) },
		PathIsDir:         pathIsDirReal,
		PathExists:        pathExistsReal,
		ReadFile:          readFileReal,
		RemoveFile:        removeFileReal,
		Out:               LaunchWriter(),
		Color:             color,
	}
}
