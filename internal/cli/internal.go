package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/awsauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/flakebundle"
	"github.com/mschulkind-oss/yolo-jail/internal/footer"
	"github.com/mschulkind-oss/yolo-jail/internal/hostmigrate"
	"github.com/mschulkind-oss/yolo-jail/internal/hostprocesses"
	"github.com/mschulkind-oss/yolo-jail/internal/journald"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/oauthbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauthhost"
	"github.com/mschulkind-oss/yolo-jail/internal/openauthclient"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/serialdaemon"
)

// runInternal dispatches the hidden `yolo internal <cmd>` family — debugging
// tooling and the in-process host-daemon entry points. This group is
// deliberately kept OUT of the dispatch registry (the documented CLI surface)
// and intercepted before RewriteArgv, so it never participates in `--`->run
// rewrite semantics.
func runInternal(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yolo internal <capture-materialize|capture-run|config-dump|daemon|darwin-bootstrap|footer|migrate-host|node-floor-satisfied|openai-auth|openai-auth-client|refresh-servers|bundle-dir> [args...]")
		return 2
	}
	switch args[0] {
	case "footer":
		// The agent footer's one renderer (docs/design/agent-footer.md §2), run by the
		// status-line command each agent pack writes into its agent's settings. Hidden
		// because its caller is a pack's command string, not a person. It never exits
		// non-zero and never writes to stderr: an agent shows a failing status command's
		// noise, and a footer that errors costs the user the row.
		//
		// hostFooterTables is the host notch's profile read (OQ-FT6): outside a jail, with
		// no selection in the agent's env, the tables come from the user config.
		return footer.Main(args[1:], os.Stdin, os.Stdout, hostFooterTables)
	case "capture-materialize":
		// Install-capture's second verb (program-delivery.md §6.3), called by the
		// GENERATED NATIVE LAUNCHER before it would download. Hidden for capture-run's
		// reason from the other end: it writes a vendor's files into the home it is
		// pointed at.
		return runCaptureMaterialize(args[1:])
	case "capture-run":
		// The in-jail half of install-capture (program-delivery.md §6.3). Hidden
		// rather than documented: it MOVES an installer's output out of the home it
		// is pointed at, which is correct only inside a throwaway capture jail.
		return runCaptureRun(args[1:])
	case "config-dump":
		return runConfigDump(args[1:])
	case "daemon":
		return runInternalDaemon(args[1:])
	case "darwin-bootstrap":
		return runDarwinBootstrap(args[1:])
	case "migrate-host":
		return runMigrateHost(args[1:])
	case "openai-auth":
		// PROMOTED: this is `yolo openai-auth` now (dispatch.go's registry, help.go's
		// list, subhelp.go's usage table), and the hidden spelling is a RETAINED ALIAS
		// into the very same handler — not a copy of it. args[0] is the verb name in
		// both namespaces, so one body serves both addresses with nothing to keep in
		// sync; see runOpenAIAuth for what it does and why the alias is kept.
		//
		// The pointer line is the whole of the alias's cost. It is written before the
		// work rather than after, the rule the operator's own machine-wide notice
		// follows: a line printed afterwards is a notification about something that
		// already happened.
		fmt.Fprintln(os.Stderr, "yolo internal openai-auth is now `yolo openai-auth` "+
			"(this spelling still works).")
		return runOpenAIAuth(args)
	case "openai-auth-client":
		return openauthclient.Main(args[1:])
	case "refresh-servers":
		// Evergreen's TRANSITIVE half (program-delivery.md §3.5, OQ-PD12a), called by
		// the GENERATED AGENT LAUNCHERS before they exec the agent. Hidden for
		// capture-materialize's reason: it installs into the home it is pointed at.
		return runRefreshServers(args[1:])
	case "node-floor-satisfied":
		// OQ-AR3's predicate, called by the GENERATED BOOTSTRAP SCRIPT before and after it
		// installs. It exists as a subcommand because the resolution is Go (a version compare
		// over the mise store) while the eager slot is a shell stage — and a shell
		// reimplementation of the comparison is the second implementation this repo keeps
		// deleting. Exit 0 = satisfied, 1 = not (with what IS available on stdout, for the
		// refusal to name), 2 = misuse.
		return runNodeFloorSatisfied(args[1:], os.Stdout)
	case "bundle-dir":
		// The flake-bundle paths `just install` stages through, printed so the
		// recipe never recomputes them — the drift that once aimed `rm -rf` at the
		// state dir. See runBundleDir for the three forms.
		return runBundleDir(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "yolo internal: unknown command %q\n", args[0])
		return 2
	}
}

// openaiAuthUsage is openaiauthhost's own help, REFERENCED rather than copied.
//
// One string, both doors: this package registers it as the subcommand's usage, and
// that package prints the same text from its own unknown-subcommand path. A copy
// here would be a second list of the verb's subcommands, so a fourth could be added
// in one place and missed in the other.
const openaiAuthUsage = openaiauthhost.OperatorUsage

// runOpenAIAuth is `yolo openai-auth {status,import,logout}`: the HOST OPERATOR's
// verbs for the machine-wide OpenAI grant. It resolves the daemon's PRIVATE socket
// (starting the daemon if it is not up), states what a mutation is about to do, and
// then delegates to the one wire client — openaiauthhost/operator.go carries both
// halves of why that indirection exists.
//
// args is the rewritten argv[1:], so args[0] is the verb name, exactly as it is when
// runInternal hands the same slice over for the hidden spelling.
//
// WHY IT IS PUBLIC. It was `yolo internal openai-auth` until this row: `logout`
// deletes a credential for every workspace, every jail and the host user at once,
// and a destructive, machine-wide operation is not something to discover in a
// namespace whose whole point is that nothing advertises it.
//
// WHY THE HIDDEN SPELLING IS KEPT. The same call `yolo broker` got when `host-daemon`
// generalized, on a weaker version of the same argument: nothing in this tree invokes
// it (`rg -n "internal openai-auth" docs internal packs` finds prose only), but the
// verb manages CREDENTIALS, and the failure mode of deleting a spelling someone
// scripted while it was the only one is a credential operation that silently stops
// running. An alias that is one `case` arm and a pointer line costs less than that.
// It is an ALIAS and not a second path: both names enter here.
//
// Help is answered HERE rather than by the operator, so `--help` is a request and
// nothing else — no socket resolved, no daemon spawned, no state directory created.
// That is the property TestEveryRegisteredCommandAnswersHelp asserts by running each
// probe with an empty cwd and an empty $HOME and requiring both to stay empty.
func runOpenAIAuth(args []string) int {
	if answerHelp("openai-auth", args, os.Stdout) {
		return 0
	}
	// A BARE `yolo openai-auth` is answered here, with the text registered above,
	// because the operator's own usage still spells itself `yolo internal
	// openai-auth` — and the first thing a user does with a newly public verb is
	// type it with no subcommand, which is precisely where being told the wrong
	// address costs the most. Misuse, not a request: stderr, exit 2.
	//
	// ⚠ It is NOT the whole of that seam. An unknown SUBCOMMAND still reaches the
	// operator and still answers in the hidden spelling. Catching that here would
	// mean restating {status, import, logout} in this file — a second copy of the
	// verb set, which is the drift rather than the fix. The one real repair is to
	// export the operator's usage from internal/openaiauthhost and register THAT
	// text above, so there is one string; it lives in a file this change does not
	// own. Until then the misuse text names a spelling that still works.
	if len(args) <= 1 {
		fmt.Fprintln(os.Stderr, openaiAuthUsage)
		return 2
	}
	return openaiauthhost.RunOperator(args[1:], os.Stdout, os.Stderr)
}

// runBundleDir is the `yolo internal bundle-dir` family: the three paths a
// from-source `just install` needs, each printed by the binary that resolves it
// rather than recomputed by the recipe.
//
//	(no args)         the STABLE path reporoot.Resolve consults, and what a
//	                  launch mounts through. Unchanged, and still the answer for
//	                  anything that just wants to find the bundle.
//	--stage           a fresh, empty GENERATION directory to stage into.
//	--activate <dir>  point the stable path at that generation, atomically.
//
// Why an install is three steps rather than one `rm -rf` + restage: a launch
// mounts <bundle>/bin/linux-<arch> into the jail, a bind mount pins an inode,
// and rewriting that directory in place therefore deletes the binaries out from
// under every RUNNING jail. See internal/flakebundle.
func runBundleDir(args []string) int {
	stable := paths.FlakeBundleDir()
	switch {
	case len(args) == 0:
		fmt.Println(stable)
		return 0
	case args[0] == "--stage":
		dir, err := flakebundle.StageDir(stable, time.Now(), os.Getpid())
		if err != nil {
			fmt.Fprintln(os.Stderr, "yolo internal bundle-dir:", err)
			return 1
		}
		fmt.Println(dir)
		return 0
	case args[0] == "--activate" && len(args) == 2:
		migrated, err := flakebundle.Activate(stable, args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "yolo internal bundle-dir:", err)
			return 1
		}
		// The migration is announced because it is a one-time, invisible move of
		// a directory that running jails are mounting, and a human who sees an
		// unexplained flake-bundles/legacy-* later deserves to have been told.
		if migrated != "" {
			fmt.Fprintf(os.Stderr, "Preserved the previous bundle as %s "+
				"(jails running right now are still mounting it).\n", migrated)
		}
		return 0
	default:
		fmt.Fprintln(os.Stderr, "usage: yolo internal bundle-dir [--stage | --activate <dir>]")
		return 2
	}
}

// runDarwinBootstrap is the self-exec target the macos-user launch stages and
// runs AS the sandbox user (J2 §3): `sudo --user=_yolojail … /var/yolo-jail/yolo
// internal darwin-bootstrap`. It replaces the old generated-Python bootstrap
// that imported the deleted src/ tree. It self-sets JAIL_HOME/HOME (sudo without
// --set-home is not a reliable HOME source), builds an *entrypoint.Env pointed
// at the sandbox home + real workspace, and runs the native generation entry.
//
// Inputs arrive as env vars the launcher bakes into the `env -i K=V…` argv
// (matching how the launch env already crosses into the sandbox): the git
// identity + YOLO_* generator contract ride through verbatim; the darwin extras are
// YOLO_DARWIN_WORKSPACE, YOLO_DARWIN_MACOS_LOG, YOLO_DARWIN_HOME_SIDECAR and
// YOLO_DARWIN_LOGIN_PATH — the last two read off the Env rather than passed as options,
// because the layout is derived from the staged packs and the login rc files now re-prepend
// the variable itself instead of a value baked at generation time.
func runDarwinBootstrap(_ []string) int {
	home := firstNonEmptyEnv("JAIL_HOME", "HOME")
	if home == "" {
		home = macosuser.SandboxHome()
	}
	// Rebind HOME/JAIL_HOME before Env resolves its home-derived paths.
	os.Setenv("JAIL_HOME", home)
	os.Setenv("HOME", home)

	// The env→Env translation lives in ONE place (entrypoint.DarwinEnvFrom) so a test
	// can exercise the real thing rather than assembling an Env by hand — which was a
	// second implementation of this contract, and drifted on its first outing.
	e := entrypoint.DarwinEnvFrom(envMap(os.Environ()), home)
	e.Stderr = os.Stderr

	// THE ONE HOST-SIDE GENERATION THAT IS NOT BEHIND THE LAUNCH GUARD. Everything
	// RunDarwinBootstrap goes on to create — the home overlay under <ws>/.yolo/home, the
	// prism sidecars, the adoption archive, the staged bootstrap script — is a bare mkdir
	// under a workspace this process is TOLD about, and unlike every other host-side
	// creator it does not run inside run.Run. A LAUNCH cannot arrive here with a bad
	// workspace (the macos-user backend is a seam inside run.Run, downstream of the
	// workspace-scope guard), so what this covers is a hand-run of the hidden self-exec
	// target. Asked off e.Workspace rather than the env var directly: one spelling of
	// "which workspace is this", the same one the generators below act on.
	//
	// ⚠ IT CHECKS THE SANDBOX IDENTITY'S ROOTS, NOT THE INVOKING HUMAN'S, and it cannot do
	// otherwise. `sudo --user=_yolojail` without --set-home is an unreliable HOME source —
	// the reason this function rebinds HOME above — so the home in scope here is
	// /Users/_yolojail whichever way it resolved. Refusing to plant a .yolo inside the
	// SANDBOX's own home or state dir is what this can honestly promise. The human's home
	// is the launch guard's promise, upstream, where their HOME is what paths resolves.
	if breach := paths.WorkspaceScopeBreach(e.WorkspaceDir()); breach != nil {
		fmt.Fprintln(os.Stderr, "yolo internal darwin-bootstrap:", breach)
		return 1
	}

	opts := entrypoint.DarwinBootstrapOptions{
		MacosLog:      os.Getenv("YOLO_DARWIN_MACOS_LOG"),
		YoloLogScript: macosuser.MacosLogWrapperScript(os.Getenv("YOLO_DARWIN_MACOS_LOG")),
	}
	if err := entrypoint.RunDarwinBootstrap(e, opts); err != nil {
		// A12: do NOT print "ok" over a failed bootstrap.
		fmt.Fprintln(os.Stderr, "yolo-jail macos-user bootstrap:", err)
		return 1
	}
	fmt.Println("yolo-jail macos-user bootstrap ok")
	return 0
}

// firstNonEmptyEnv returns the first environment variable in keys with a
// non-empty value, or "".
func firstNonEmptyEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// runInternalDaemon dispatches the hidden `yolo internal daemon <name>` group,
// callable in-process so a single yolo binary can serve as each one.
//
// The MEMBERS are the switch below and the usage line beside it, never a count in
// this comment: it said "the three host daemons" while the switch held six, and a
// number here is one more thing to keep true for no reader's benefit. Nor are they
// all host-scoped — `scope: "host"` is a manifest fact per loophole
// (`rg -n '"scope": "host"' packs/*/loopholes/*/manifest.jsonc`), and a daemon in
// this group may be either. The remaining argv is passed through verbatim, so each daemon's
// flag surface (--socket, --self-check, --init-ca, …) is byte-identical to its
// standalone binary.
//
// `broker-relay` was the fourth and is GONE. It fronted the broker singleton for
// one jail and stamped a host-asserted jail_id into the request; both jobs are the
// framework's now — svcendpoint's front publishes the endpoint, and its connection
// preamble carries the identity — so the daemon was deleted rather than moved
// (docs/design/broker-as-a-pack.md §7). A name removed from this switch reports
// "unknown daemon", which is the right answer for an argv nothing emits any more.
func runInternalDaemon(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yolo internal daemon <aws-auth|claude-oauth-broker|host-processes|journal|openai-auth-broker|serial> [args...]")
		return 2
	}
	rest := args[1:]
	switch args[0] {
	case "aws-auth":
		return awsauthdaemon.Main(rest)
	case "claude-oauth-broker":
		return oauthbroker.Main(rest)
	case "host-processes":
		return hostprocesses.Main(rest)
	case "journal":
		return journald.Main(rest)
	case "openai-auth-broker":
		return openaiauthdaemon.Main(rest)
	case "serial":
		return serialdaemon.Main(rest)
	default:
		fmt.Fprintf(os.Stderr, "yolo internal daemon: unknown daemon %q\n", args[0])
		return 2
	}
}

// runMigrateHost retires host-side artifacts left by the pre-Go (Python)
// distribution, so `go install ./cmd/yolo` can land its binary. The Justfile
// `install` recipe runs it through `go run` immediately before `go install` —
// it cannot live in the installed binary's startup path, because the whole
// point is to unblock the install that produces that binary.
//
// Flags: --gobin=DIR (default: $GOBIN, else $GOPATH/bin).
func runMigrateHost(args []string) int {
	gobin := ""
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--gobin="):
			gobin = strings.TrimPrefix(a, "--gobin=")
		case len(a) > 0 && a[0] == '-':
			fmt.Fprintf(os.Stderr, "migrate-host: unknown flag %q\n", a)
			return 2
		default:
			fmt.Fprintf(os.Stderr, "migrate-host: unexpected argument %q\n", a)
			return 2
		}
	}

	if gobin == "" {
		resolved, err := hostmigrate.DefaultGOBIN()
		if err != nil {
			fmt.Fprintln(os.Stderr, "migrate-host:", err)
			return 1
		}
		gobin = resolved
	}

	if _, err := hostmigrate.New(gobin).Preflight(); err != nil {
		fmt.Fprintf(os.Stderr, "\nyolo-jail: cannot install over an existing file.\n  %v\n", err)
		return 1
	}
	return 0
}

// runConfigDump loads + merges the config for a workspace (default: cwd) via
// internal/config and prints the merged config as canonical snapshot JSON,
// followed by the validation errors/warnings. Used for differential testing
// and for eyeballing the merged shape.
//
// Flags: --strict (raise on malformed config), positional workspace dir.
func runConfigDump(args []string) int {
	strict := false
	workspace := ""
	for _, a := range args {
		switch {
		case a == "--strict":
			strict = true
		case len(a) > 0 && a[0] == '-':
			fmt.Fprintf(os.Stderr, "config-dump: unknown flag %q\n", a)
			return 2
		default:
			workspace = a
		}
	}
	if workspace == "" {
		if wd, err := os.Getwd(); err == nil {
			workspace = wd
		}
	}

	cfg, err := config.LoadConfig(workspace, strict, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config-dump:", err)
		return 1
	}
	// A REAL resolver, like check.go's sectionMergedConfig and the launch
	// preflight: with nil the known-loophole set is empty, so every name reads as
	// uninstalled and the enable-uninstalled rule fires a fatal for a config
	// both other callers accept. An oracle that disagrees with the thing it is an
	// oracle for is worse than no oracle.
	errs, warns := config.ValidateConfig(cfg, workspace, loopholes.NewResolver())

	out := jsonx.NewOrderedMap()
	out.Set("config", cfg)
	out.Set("errors", strAny(errs))
	out.Set("warnings", strAny(warns))
	snap, err := config.SnapshotJSON(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config-dump:", err)
		return 1
	}
	fmt.Println(snap)
	if len(errs) > 0 {
		return 1
	}
	return 0
}

func strAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// envMap turns os.Environ()'s KEY=VALUE slice into the map the entrypoint Env
// constructors take.
func envMap(environ []string) map[string]string {
	out := make(map[string]string, len(environ))
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[k] = v
		}
	}
	return out
}

// runNodeFloorSatisfied answers "is a Node meeting this floor available?" for the bootstrap's eager
// install and its refusal (docs/reference/agent-program-runtimes.md, OQ-AR2/OQ-AR3).
//
// Silent when satisfied. When NOT, it prints one line on stdout — the interpreters the resolution
// can see (entrypoint.DescribeAvailableNodes) — because OQ-AR3's refusal must name what is
// available and the bootstrap has no other way to learn it without re-implementing the store walk
// in shell. The bootstrap's first probe discards both streams; only its post-install probe keeps
// stdout, and only to quote it in the refusal. The floor is validated before it is used: a floor
// the comparison cannot handle exits 2 rather than reporting "not satisfied", because answering a
// malformed question with "no" would send the bootstrap into an install it cannot complete.
func runNodeFloorSatisfied(args []string, stdout io.Writer) int {
	if len(args) != 1 || args[0] == "" {
		fmt.Fprintln(os.Stderr, "usage: yolo internal node-floor-satisfied <floor>")
		return 2
	}
	if !packdecl.ValidNodeFloor(args[0]) {
		fmt.Fprintf(os.Stderr, "yolo internal node-floor-satisfied: %q is not a comparable floor\n", args[0])
		return 2
	}
	if entrypoint.ResolveNodeForFloor(args[0]) == "" {
		fmt.Fprintln(stdout, entrypoint.DescribeAvailableNodes())
		return 1
	}
	return 0
}
