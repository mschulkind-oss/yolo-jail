package macosuser

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/provision"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
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
	// Out receives the human output. Rich markup is rendered to ANSI when
	// Color is set, else stripped to plain text.
	Out io.Writer
	// Color is the resolved color capability (the caller's requested color AND
	// stdout is a real TTY). When false the printer strips rich markup. It is
	// forced OFF for the dry-run plan render (byte-pinned goldens) and any
	// non-TTY path — only interactive chatter gains color.
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

	// HostHomeOverlay is the host-side tree of composed CONTENT — skills and
	// pack-declared briefings — already laid out at the home-relative paths it belongs
	// at, or "" when there is nothing to deliver. The container backends mount each
	// staged dir at its destination; this one has no mounts, so the tree is staged
	// root-owned beside the packs and copied over the sandbox home by the bootstrap.
	// See internal/cli/run/macoshomeoverlay.go for why it crosses as a TREE rather
	// than as a mapping the bootstrap would have to re-implement.
	HostHomeOverlay string

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
	// (docs/design/provider-credential-scope.md; the run pipeline's packChannel.launchEnv)
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
	if res := cfgSection(opts.Config, "resources"); res != nil && len(res.Keys()) > 0 {
		out.print("[yellow]Warning: resources are NOT enforced on macos-user[/yellow] — " +
			"macOS has no cgroups and there is no VM to size, so " + strings.Join(res.Keys(), ", ") +
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
	// vehicle of docs/design/provider-credential-scope.md §2.3, bypassing the credential
	// gate. Its env_sources now arrive inside PackEnv, already narrowed by the gate to what
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
	// It crosses in the session env file, so it reaches the provisioning stage and the agent,
	// not the bootstrap, which reads a closed contract of its own (buildBootstrapEnv). What
	// setting it changes on this backend is audited in the design's §2.2.
	env.Set("YOLO_VERSION", version.Get(opts.RepoRoot))
	selfExe := ""
	if deps.SelfExe != nil {
		selfExe = deps.SelfExe()
	}
	return BuildRunPlan(opts.Workspace, opts.Config, opts.Agents, opts.AgentArgv,
		selfExe, opts.HostPackRoot, opts.HostHomeOverlay, opts.HostCtx, env, darwin,
		opts.BlockedTools)
}

// RunMacosUser launches agent_argv in the dedicated-user + Seatbelt sandbox.
// Returns the agent exit code (or 1 on a precondition/setup failure). dry-run
// builds + prints the plan and RETURNS before the macOS/root gates (so it
// runs on Linux CI); 1. cheap preconditions (macOS, not-root, sandbox-exec,
// sandbox user) BEFORE the up-to-30-min nix build; 2. the plan is built AFTER
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
	// The plan (and the env-source warnings intermixed with it) is byte-pinned
	// by the goldens, so force color OFF for the whole dry-run render — only
	// interactive live chatter gains color.
	if opts.DryRun {
		plainDeps := deps
		plainDeps.Color = false
		plan := buildPlan(plainDeps, opts, nil)
		problems := PlanInvariants(plan)
		PrintPlan(deps.Out, plan, problems)
		if len(problems) > 0 {
			return 1
		}
		return 0
	}

	// Fail closed BEFORE any subprocess when we can't run here.
	if !deps.IsMacOS() {
		out.print("[bold red]runtime 'macos-user' requires macOS.[/bold red] " +
			"Use 'podman' or 'container' on this host.\n" +
			"[dim]Tip: `yolo run --dry-run` prints the full plan on any OS.[/dim]")
		return 1
	}
	// Must NOT be run under sudo — the launch self-escalates, and running as
	// root makes _host_user() → 'root', misassigning the git identity + ACL.
	if deps.Geteuid() == 0 {
		out.print("[bold red]Don't run `yolo` under sudo for the macos-user " +
			"backend.[/bold red]  It escalates each step itself; running as " +
			"root breaks the per-user identity/ACL.")
		return 1
	}

	// Cheap preconditions FIRST — before the (potentially slow) nix build.
	if !deps.Which("sandbox-exec") {
		out.print("[bold red]sandbox-exec not found[/bold red] — the macos-user " +
			"backend needs Apple Seatbelt (built into macOS).")
		return 1
	}
	if !deps.SandboxUserExists() {
		out.printf("[bold red]Sandbox user '%s' does not exist.[/bold red]\n"+
			"Run the one-time setup to create it (`yolo macos-setup`; see "+
			"`docs/reference/macos-no-vm-direction.md`).", SandboxUser)
		return 1
	}
	// THE HOME IS A SEPARATE FACT FROM THE ACCOUNT, and this check used to make only the
	// one above. A DELETED home — what `sudo rm -rf /Users/_yolojail` leaves, which the
	// runbook prescribes for an account predating the home-tier layout — is unrepairable
	// from inside: /Users is root-owned 0755, so the sandbox uid cannot create it. The
	// launch therefore ran the entire native nix build and then died in the bootstrap with
	// twenty `mkdir /Users/_yolojail: permission denied` generator failures, under a
	// diagnosis that blamed the WORKSPACE ACL and prescribed `macos-fix-permissions`, a
	// remedy that cannot reach this path. Same rule the sidecar-mirror refusal was fixed
	// for: name the remedy that reaches the path it names. Measured on hardware
	// 2026-09-12, and cheap here — one stat, before the build.
	if !deps.PathIsDir(SandboxHome()) {
		out.printf("[bold red]Sandbox home '%s' is missing.[/bold red]\n"+
			"The account '%s' exists, so this is a home that was DELETED rather than a "+
			"machine that\nwas never set up — and the sandbox user cannot recreate it "+
			"itself (/Users is root-owned).\n\n"+
			"Reprovision it — idempotent, and it leaves the account record alone:\n"+
			"  [bold]yolo macos-setup[/bold]", SandboxHome(), SandboxUser)
		return 1
	}
	// THE WORKSPACE MUST BE SHARED WITH THE SANDBOX, and this is the cheapest place
	// to learn it is not. `macos-setup` shares everything under the shared root, and
	// anything CREATED there afterwards inherits the grant — so by the time a launch
	// runs, one route is left: a project MOVED or copied in, because rename() creates
	// nothing and inherits nothing. That is the case this message assumes, because
	// after setup it is the only one left.
	//
	// It REFUSES and names the command rather than offering to fix it inline. An
	// earlier cut prompted y/N here; the command is the better answer because it is
	// one thing to learn, it is idempotent, `macos-setup` has already named it, and
	// it is in `yolo --help` and the diagnosing-the-jail skill. A prompt in every
	// path teaches nothing and still needs the command to exist.
	//
	// Refusing also keeps the O(files) walk off the hot path — ~0.16ms per object,
	// so ~16s on a repo with a fat node_modules, which is what 84c55268 removed by
	// moving to an inheriting entry. The check itself is one `ls`.
	if deps.RunBash(WorkspaceGrantedScript(opts.Workspace, "")) != 0 {
		out.printf("[bold yellow]%s is not shared with the sandbox user.[/bold yellow]\n"+
			"It carries no usable ACL entry for the [bold]%s[/bold] group, so the sandbox "+
			"cannot write here\nand the launch would fail partway through provisioning.\n\n"+
			"Most likely this project was MOVED or copied into %s: macOS applies the\n"+
			"shared ACL when a directory is CREATED, and a move inherits nothing. (An ACL "+
			"also names a\nUUID rather than a name, so entries made before the sandbox "+
			"account was last recreated are\ninert while still looking correct in `ls -le`.)\n\n"+
			"Share it — idempotent, safe to re-run:\n"+
			"  [bold]yolo macos-fix-permissions %s[/bold]",
			opts.Workspace, SandboxGroup, SharedRootDefault(), opts.Workspace)
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
	var darwin *Darwin
	pkgs := config.EffectivePackages(opts.Config, config.PlatformDarwin)
	// The nix build runs from the repo ROOT (the flake dir).
	d, ok, err := deps.MaterializeDarwin(opts.RepoRoot, pkgs)
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
	// before pack staging (run.holdLaunchLock), because the host-side staging this backend
	// copies below is written there and read back here, and a second launch restaging in
	// between is the race docs/reference/pack-system.md#concurrent-launches-of-one-workspace
	// records. The seam then hands back THAT hold (run.AcquireWorkspaceLockFor), so the
	// release below is what ends the launch's whole window, before the agent as always.
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
			out.printf("[bold red]Could not stage entrypoint (%s).[/bold red]", strings.Join(cmd, " "))
			return 1
		}
	}

	// 2.5 THE SESSION ENV FILE — everything this launch composed, delivered as a root-owned
	// 0600 file the sandbox account may read, instead of as words on three command lines
	// (envfile.go). Before the bootstrap, because the provisioning stage is the next thing
	// after it that reads the file.
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
	if deps.Run(plan.BootstrapArgv) != 0 {
		out.print("[bold red]entrypoint bootstrap failed[/bold red] — the sandbox " +
			"user's shims/agent configs were not generated, so the agent " +
			"would not run correctly. Aborting.")
		return 1
	}

	// 3.5 THE PROVISIONING STAGE — the third privileged step, between the bootstrap and
	// the agent (docs/design/macos-user-provisioning.md half two). Confined under the same
	// Seatbelt profile the agent gets, which step 2 already installed; empty when the
	// config declares no tools, and then this costs nothing at all.
	if len(plan.ProvisionArgv) > 0 && !runProvisionStage(deps, out, plan) {
		return 1
	}

	// 4. Launch under the TTY proxy — OUTSIDE the lock. Everything that writes the
	// per-workspace tier has happened; the agent's own writes are the same ones two
	// sessions on one workspace already share on every backend.
	release()
	return deps.RunWithProxy(plan.LaunchArgv)
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
// stripped — parity is on the ARTIFACTS, which are byte-pinned by the producer
// differential). Color is deliberately OFF here: the plan output is byte-pinned
// by the goldens, so it must stay plain text on every path.
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
	for _, cmd := range plan.StageCommands {
		p.print("  sudo " + strings.Join(cmd, " "))
	}
	// The env-file steps, in the order they run and NAMED — the directory's mode and the
	// sandbox's read ACE are the whole of what keeps this file private, so a dry run that
	// hid them would hide the security property it is being read to check.
	for _, cmd := range plan.EnvFileCommands {
		p.print("  sudo " + strings.Join(cmd, " "))
	}
	if plan.EnvFile != "" {
		p.printf("  sudo %s %s  [dim](content on stdin, never argv)[/dim]", teeBin, plan.EnvFile)
		p.printf("  sudo %s 0600 %s", chmodBin, plan.EnvFile)
	}
	for _, cmd := range plan.EnvFileGrantCommands {
		p.print("  sudo " + strings.Join(cmd, " "))
	}
	p.print("  sudo " + strings.Join(plan.BootstrapArgv[1:], " "))
	// NAMED EVEN WHEN THERE IS NO STAGE, for the reason the pack line above is: "this
	// launch installs nothing" and "this backend cannot install anything" were
	// indistinguishable until half two, and a dry run that simply omitted the step would
	// keep them that way.
	if len(plan.ProvisionArgv) > 0 {
		p.print("  sudo " + strings.Join(plan.ProvisionArgv[1:], " "))
	}
	p.print("")

	section := func(title, body string) {
		p.printf("[bold]── %s ──[/bold]", title)
		p.print(strings.TrimRight(body, "\n"))
		p.print("")
	}
	section("Seatbelt profile", plan.Seatbelt)
	p.print("[bold]── bootstrap argv (self-exec as sandbox) ──[/bold]")
	p.print("  " + strings.Join(plan.BootstrapArgv, " "))
	p.print("")
	if len(plan.ProvisionArgv) == 0 {
		p.print("[bold]── provisioning stage ──[/bold]")
		p.print("  [dim]skipped — no mise_tools declared[/dim]")
	} else {
		p.print("[bold]── provisioning stage (confined, before the agent) ──[/bold]")
		p.print("  " + strings.Join(plan.ProvisionArgv, " "))
	}
	p.print("")
	p.print("[bold]── launch argv ──[/bold]")
	p.print("  " + strings.Join(plan.LaunchArgv, " "))
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
		HostNix:           hostNixReal,
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
