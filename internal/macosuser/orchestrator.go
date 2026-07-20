package macosuser

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// Deps are the injectable seams for the macOS-only orchestrator + the four
// macos-* command bodies. Every subprocess / filesystem / platform probe is a
// seam so the whole surface is unit-testable on Linux (the
// cli/check + ps deps-injection precedent). RealDeps wires the production implementations.
type Deps struct {
	// IsMacOS reports sys.platform == "darwin".
	IsMacOS func() bool
	// Geteuid returns the effective uid (0 under sudo).
	Geteuid func() int
	// Which reports whether a binary is on PATH (shutil.which is not None).
	Which func(string) bool
	// SandboxUserExists reports `id <SANDBOX_USER>` returned 0.
	SandboxUserExists func() bool
	// ResolvePython resolves the sandbox interpreter ("" + false if none).
	ResolvePython func() (string, bool)
	// GitConfig reads a host git config value best-effort ("" + false if unset).
	GitConfig func(key string) (string, bool)
	// Getenv reads an environment variable.
	Getenv func(string) string
	// HostUser is the invoking (admin) user (getpass.getuser, "" on failure).
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
	// TakenIDs returns the union of existing UIDs+GIDs (macos_setup).
	TakenIDs func() map[int]struct{}
	// SetRandomPassword sets a random password on the sandbox account.
	SetRandomPassword func() bool
	// PathIsDir reports whether a path is an existing directory.
	PathIsDir func(string) bool
	// PathExists reports whether a path exists (broker socket, etc.).
	PathExists func(string) bool
	// Out receives the human output (rich markup stripped, parity on text).
	Out io.Writer
}

// Options carries the run() inputs the front door resolves (workspace,
// config, agents, agent argv, repo src). It mirrors run_macos_user's
// parameters.
type Options struct {
	Workspace string
	Config    *jsonx.OrderedMap
	Agents    []string
	AgentArgv []string
	RepoSrc   string
	// SandboxEnv is an optional caller-supplied env layered LAST (the Python
	// sandbox_env kwarg); nil is the common case.
	SandboxEnv *jsonx.OrderedMap
	DryRun     bool
}

// richTagRe strips rich console markup (parity is on text content — the
// runcmd/check precedent; the dry-run ARTIFACTS are byte-pinned separately).
var richTagRe = regexp.MustCompile(`\[/?[a-zA-Z][^\]]*\]`)

type printer struct{ w io.Writer }

func (p printer) print(msg string)          { fmt.Fprintln(p.w, richTagRe.ReplaceAllString(msg, "")) }
func (p printer) printf(f string, a ...any) { p.print(fmt.Sprintf(f, a...)) }

// MacosSandboxEnv returns the extra env layered into the sandbox launch (git
// identity + TERM/COLORTERM). Host credentials never cross.
func MacosSandboxEnv(deps Deps, cfg *jsonx.OrderedMap) *jsonx.OrderedMap {
	env := jsonx.NewOrderedMap()
	if term := deps.Getenv("TERM"); term != "" {
		env.Set("TERM", term)
	}
	if ct := deps.Getenv("COLORTERM"); ct != "" {
		env.Set("COLORTERM", ct)
	}
	for _, pair := range [][2]string{{"YOLO_GIT_NAME", "user.name"}, {"YOLO_GIT_EMAIL", "user.email"}} {
		if val, ok := deps.GitConfig(pair[1]); ok && val != "" {
			env.Set(pair[0], val)
		}
	}
	return env
}

// macos_sandbox_env, merge env_sources (swallowing any error — a bad entry must
// not crash the plan), layer the caller's sandbox_env last, then build the plan.
func buildPlan(deps Deps, opts Options, darwin *Darwin) RunPlan {
	env := MacosSandboxEnv(deps, opts.Config)
	// try: env.update(_resolve_env_sources(...)) except Exception: pass
	// The resolver's warnings (e.g. "env_sources file not found") go to the
	// console in Python (config.py console.print) — route them to deps.Out via
	// the rich-stripping printer so the plan output matches (the container path
	// wires the same warn callback; a no-op here silently dropped the line).
	out := printer{w: deps.Out}
	resolved := config.ResolveEnvSources(opts.Workspace, opts.Config, func(msg string) { out.print(msg) })
	for _, k := range resolved.Keys() {
		v, _ := resolved.Get(k)
		env.Set(k, v)
	}
	if opts.SandboxEnv != nil {
		for _, k := range opts.SandboxEnv.Keys() {
			v, _ := opts.SandboxEnv.Get(k)
			env.Set(k, v)
		}
	}
	interp, resolvedInterp := deps.ResolvePython()
	return BuildRunPlan(opts.Workspace, opts.Config, opts.Agents, opts.AgentArgv,
		opts.RepoSrc, env, interp, resolvedInterp, darwin)
}

// RunMacosUser launches agent_argv in the dedicated-user + Seatbelt sandbox.
// Returns the agent exit code (or 1 on a precondition/setup failure). dry-run
// builds + prints the plan and RETURNS before the macOS/root gates (so it
// runs on Linux CI); 1. cheap preconditions (macOS, not-root, sandbox-exec,
// sandbox user) BEFORE the up-to-30-min nix build; 2. the plan is built AFTER
// the gates (it reads host git config); 3. install profile + stage
// entrypoint; 4. bootstrap; 5. launch.
func RunMacosUser(deps Deps, opts Options) int {
	out := printer{w: deps.Out}

	// 0. Dry-run: build the plan, print it + invariants, execute nothing. Pure
	// (darwin=nil → no nix build), so CI and a Mac agent can both inspect it.
	if opts.DryRun {
		plan := buildPlan(deps, opts, nil)
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
			"`docs/design/macos-no-vm-direction.md`).", SandboxUser)
		return 1
	}

	// Materialize `packages:` as native aarch64-darwin nix (the acceptance
	// bar). Runs nix on the HOST user before any sandbox; on failure abort.
	var darwin *Darwin
	pkgs := config.EffectivePackages(opts.Config)
	if len(pkgs) > 0 {
		// Python: darwin_packages.materialize(repo_src.parent, pkgs) — the nix
		// build runs from the repo ROOT (parent of src).
		d, ok, err := deps.MaterializeDarwin(parentDir(opts.RepoSrc), pkgs)
		if !ok {
			out.printf("[bold red]Could not materialize packages natively:[/bold red] %s\n"+
				"[dim]Fix the package, or use the Apple Container runtime "+
				"(runtime: \"container\") which builds them in a Linux VM.[/dim]", errStr(err))
			return 1
		}
		darwin = d
		if darwin != nil && len(darwin.Skipped) > 0 {
			out.printf("[yellow]Skipped packages with no aarch64-darwin build:[/yellow] "+
				"%s\n"+
				"[dim](use the container runtime for these — or, if a name is "+
				"unexpected, check for a typo: an unknown attr is skipped, not "+
				"errored, because a hard error would abort the whole eval.)[/dim]",
				strings.Join(darwin.Skipped, ", "))
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

	// 3. Bootstrap the sandbox user's home; ABORT on failure.
	if !deps.InstallRootFile(plan.BootstrapPath, plan.Bootstrap, "0444") {
		out.printf("[bold red]Could not write bootstrap %s", plan.BootstrapPath)
		return 1
	}
	if deps.Run(plan.BootstrapArgv) != 0 {
		out.print("[bold red]entrypoint bootstrap failed[/bold red] — the sandbox " +
			"user's shims/agent configs were not generated, so the agent " +
			"would not run correctly. Aborting.")
		return 1
	}

	// 4. Launch under the TTY proxy.
	return deps.RunWithProxy(plan.LaunchArgv)
}

// PrintPlan renders a RunPlan for --dry-run (human-readable; rich markup
// stripped — parity is on the ARTIFACTS, which are byte-pinned by the producer
// differential).
func PrintPlan(w io.Writer, plan RunPlan, problems []string) {
	p := printer{w: w}
	p.print("[bold]macos-user run plan[/bold] (dry-run — nothing executed)\n")
	p.printf("workspace:   %s", plan.Workspace)
	p.printf("session:     %s", plan.Cname)
	interp := plan.Interp
	if !plan.InterpResolved {
		interp = "[red]<unresolved>[/red]"
	}
	p.printf("interpreter: %s", interp)
	p.printf("  candidates: %s", strings.Join(plan.InterpCandidates, ", "))
	p.printf("profile:     %s", plan.ProfilePath)
	p.printf("staged src:  %s", StagedEntrypointDir(plan.StagedDir))
	p.printf("git identity: %s", gitIdentityRepr(plan.GitIdentity))
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
	p.print("  sudo " + strings.Join(plan.BootstrapArgv[1:], " "))
	p.print("")

	section := func(title, body string) {
		p.printf("[bold]── %s ──[/bold]", title)
		p.print(strings.TrimRight(body, "\n"))
		p.print("")
	}
	section("Seatbelt profile", plan.Seatbelt)
	section("bootstrap script", plan.Bootstrap)
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

// gitIdentityRepr renders the git-identity map the way _print_plan does:
// Python's `plan.git_identity or '(none …)'` — a dict repr, or the fallback.
func gitIdentityRepr(m *jsonx.OrderedMap) string {
	if m == nil || m.Len() == 0 {
		return "(none — commits use no identity)"
	}
	return pyDictRepr(m)
}

// pyDictRepr renders an OrderedMap as Python's dict repr ({'k': 'v', …}), which
// _print_plan embeds for the git identity. Keys/values are string reprs.
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

// RealDeps returns Deps backed by real subprocesses / filesystem, mirroring the
// pscmd RealDeps constructor. runProxy is the TTY-proxy launcher the front door
// supplies (internal/cli/run's runWithProxy is Linux/macOS-specific);
// materialize wires internal/darwinpkg's streaming nix build. Both are passed
// in so this package needs no build-tagged syscall dependencies.
func RealDeps(runProxy func(argv []string) int, materialize func(repoRoot string, packages []any) (*Darwin, bool, error)) Deps {
	return Deps{
		IsMacOS:           func() bool { return isMacOSReal() },
		Geteuid:           os.Geteuid,
		Which:             whichReal,
		SandboxUserExists: func() bool { return sandboxUserExistsReal(SandboxUser) },
		ResolvePython:     func() (string, bool) { return ResolvePython(nil) },
		GitConfig:         gitConfigReal,
		Getenv:            os.Getenv,
		HostUser:          hostUserReal,
		Run:               runReal,
		RunBash:           runBashReal,
		RunWithProxy:      runProxy,
		InstallRootFile:   installRootFileReal,
		MaterializeDarwin: materialize,
		TakenIDs:          takenIDsReal,
		SetRandomPassword: func() bool { return setRandomPasswordReal(SandboxUser) },
		PathIsDir:         pathIsDirReal,
		PathExists:        pathExistsReal,
		Out:               os.Stdout,
	}
}
