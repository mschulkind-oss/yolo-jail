package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/builder"
	"github.com/mschulkind-oss/yolo-jail/internal/checkcmd"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/darwinpkg"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// runBuilder dispatches `yolo builder {setup,start,stop,status}` (macOS-only
// on-demand Linux builder VM).
func runBuilder(args []string) int {
	var sub string
	var rest []string
	if len(args) > 1 {
		sub = args[1]
		rest = args[2:]
	}
	return builder.RunBuilder(builder.RealDeps(), sub, rest)
}

// runMacosSetup/Teardown/Unshare/FixPermissions dispatch the four macos-*
// commands (macOS-only; refuse/no-op on Linux).
func runMacosSetup(_ []string) int    { return macosuser.MacosSetup(macosuser.RealDeps(nil, nil)) }
func runMacosTeardown(_ []string) int { return macosuser.MacosTeardown(macosuser.RealDeps(nil, nil)) }

func runMacosUnshare(args []string) int {
	ws := ""
	if len(args) > 1 {
		ws = args[1]
	}
	return macosuser.MacosUnshare(macosuser.RealDeps(nil, nil), ws)
}

func runMacosFixPermissions(args []string) int {
	path := ""
	if len(args) > 1 {
		path = args[1]
	}
	return macosuser.MacosFixPermissions(macosuser.RealDeps(nil, nil), path)
}

// runPrune runs `yolo prune` (disk reclaim). Default dry-run; --apply reclaims.
func runPrune(args []string) int {
	opts := prune.NewDefaultOptions()
	opts.Color = true
	// args: ["prune", <flags>...]
	for i := 1; i < len(args); i++ {
		a := args[i]
		nextInt := func(def int) int {
			if i+1 < len(args) {
				i++
				if n, err := strconv.Atoi(args[i]); err == nil {
					return n
				}
			}
			return def
		}
		switch {
		case a == "--apply":
			opts.Apply = true
		case a == "--no-hardlink":
			opts.NoHardlink = true
		case a == "--dedup-global":
			opts.DedupGlobal = true
		case a == "--no-containers":
			opts.NoContainers = true
		case a == "--no-images":
			opts.NoImages = true
		case a == "--keep-images":
			opts.KeepImages = nextInt(opts.KeepImages)
		case a == "--no-image-cache":
			opts.NoImageCache = true
		case a == "--no-build-roots":
			opts.NoBuildRoots = true
		case a == "--no-shadowed-home":
			opts.NoShadowedHome = true
		case a == "--image-cache-keep":
			opts.ImageCacheKeep = nextInt(opts.ImageCacheKeep)
		case a == "--cache-age":
			opts.CacheAge = nextInt(opts.CacheAge)
		case a == "--purge-heavy-caches":
			opts.PurgeHeavyCaches = true
		}
	}
	return prune.Run(opts)
}

// runBroker dispatches `yolo broker {status,stop,restart,logs}`. args is the
// rewritten argv[1:] (args[0]=="broker").
func runBroker(args []string) int {
	var sub string
	var rest []string
	if len(args) > 1 {
		sub = args[1]
		rest = args[2:]
	}
	deps := broker.CLIRealDeps()
	switch sub {
	case "status":
		return broker.PrintStatus(deps)
	case "stop":
		return broker.Stop(deps)
	case "restart":
		return broker.Restart(deps)
	case "logs":
		// -n/--lines (default 50) and -f/--follow.
		lines, follow := 50, false
		for i := 0; i < len(rest); i++ {
			a := rest[i]
			switch {
			case a == "-f" || a == "--follow":
				follow = true
			case a == "-n" || a == "--lines":
				if i+1 < len(rest) {
					i++
					if n, err := strconv.Atoi(rest[i]); err == nil {
						lines = n
					}
				}
			case strings.HasPrefix(a, "-n"):
				if n, err := strconv.Atoi(a[2:]); err == nil {
					lines = n
				}
			case strings.HasPrefix(a, "--lines="):
				if n, err := strconv.Atoi(a[len("--lines="):]); err == nil {
					lines = n
				}
			}
		}
		return broker.Logs(deps, lines, follow)
	default:
		fmt.Fprintf(os.Stderr, "Usage: yolo broker {status|stop|restart|logs}\n")
		return 1
	}
}

// runInit runs `yolo init` (scaffold yolo-jail.jsonc + briefing). Parses
// repeatable --mount/-m.
func runInit(args []string) int {
	var mounts []string
	for i := 1; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--mount" || a == "-m":
			if i+1 < len(args) {
				i++
				mounts = append(mounts, args[i])
			}
		case strings.HasPrefix(a, "--mount="):
			mounts = append(mounts, a[len("--mount="):])
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot resolve cwd: %v\n", err)
		return 1
	}
	return Init(cwd, mounts, os.Stdout, isTTYStdout())
}

// runInitUserConfig runs `yolo init-user-config`.
func runInitUserConfig(_ []string) int {
	return InitUserConfig(os.Stdout)
}

func isTTYStdout() bool {
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// runConfigRef prints the full configuration reference. args ignored.
func runConfigRef(_ []string) int {
	return RunStdout()
}

// runLoopholes dispatches the `yolo loopholes {list,status,enable,disable}`
// group. args is the rewritten argv[1:], so args[0] == "loopholes" and args[1]
// is the sub-subcommand.
func runLoopholes(args []string) int {
	// args: ["loopholes", <sub>, <rest>...]
	var sub string
	var rest []string
	if len(args) > 1 {
		sub = args[1]
		rest = args[2:]
	}
	deps := loopholes.RealDeps()
	switch sub {
	case "", "list":
		return loopholes.List(deps)
	case "status":
		return loopholes.Status(deps)
	case "enable", "disable":
		if len(rest) < 1 {
			fmt.Fprintf(os.Stderr, "Usage: yolo loopholes %s <name>\n", sub)
			return 1
		}
		return loopholes.CmdSetEnabled(deps, rest[0], sub == "enable")
	default:
		fmt.Fprintf(os.Stderr, "Usage: yolo loopholes {list|status|enable|disable} [name]\n")
		return 1
	}
}

// runPs runs `yolo ps` (list running jails). args is ignored (ps takes no
// flags). Uses platform-aware runtime resolution: on macOS with Apple Container
// running, `podman ps` would be empty and the tracking-prune would delete live
// jails' files.
func runPs(_ []string) int {
	detect := func() string {
		return runtime.PsRuntime(paths.IsMacOS, func(bin string) bool {
			_, err := exec.LookPath(bin)
			return err == nil
		})
	}
	return psRun(psRealDeps(psRunCmd, detect))
}

// psRunCmd runs a container-runtime probe and returns (stdout, ok). ok=false on
// a spawn error OR non-zero exit — the tri-state "could not enumerate" that
// pscmd must NOT collapse to "no jails" (else it prunes live jails' tracking
// files, audit §D11).
func psRunCmd(argv []string) (string, bool) {
	cmd := exec.Command(argv[0], argv[1:]...)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

// runRun parses the run flags (--network, --new, --profile, --dry-run) and the
// post-`--` command from args (the rewritten argv[1:]) and runs the container
// launch.
//
// The front-door RewriteArgv inserts "run" at the `--` position, so flags that
// preceded `--` end up BEFORE the "run" token (e.g. `yolo --new -- true` →
// [--new, run, --, true]). We therefore scan the WHOLE args: skip the "run"
// token wherever it appears, parse flags until `--`, and take everything after
// `--` as the command (ctx.args).
func runRun(args []string) int {
	opts := run.NewDefaultOptions()
	opts.Color = true
	afterDashDash := false
	sawRun := false
	var cmdArgs []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if afterDashDash {
			cmdArgs = append(cmdArgs, a)
			continue
		}
		switch {
		case a == "--":
			afterDashDash = true
		case a == "run" && !sawRun:
			sawRun = true // the injected/leading subcommand token
		case a == "--new":
			opts.New = true
		case a == "--profile":
			opts.Profile = true
		case a == "--dry-run":
			opts.DryRun = true
		case a == "--network":
			if i+1 < len(args) {
				i++
				opts.Network = args[i]
			}
		case len(a) > len("--network=") && a[:len("--network=")] == "--network=":
			opts.Network = a[len("--network="):]
		default:
			// An unrecognized bare token before `--` starts the command (typer
			// would error, but the front door already classified this as run).
			cmdArgs = append(cmdArgs, a)
			afterDashDash = true
		}
	}
	opts.Args = cmdArgs
	// Wire the macos-user native branch. run stays free of the macosuser +
	// darwinpkg deps; the front door injects the handler.
	opts.MacosUserRun = macosUserRun
	// Set the tmux/kitty jail indicator around the run, restoring on exit.
	restore := SetupJailIndicator()
	if restore != nil {
		defer restore()
	}
	return run.Run(opts)
}

// macosUserRun is the run.Options.MacosUserRun seam impl: it assembles the
// real macosuser deps (TTY proxy + native darwin nix materialize) and runs the
// Seatbelt-sandboxed launch. repoRoot is the yolo-jail repo root; RepoSrc is
// repoRoot/src (Python passes repo_src=repo_root/"src"). macos-hardware-gated;
// on Linux macosuser fails closed at its IsMacOS precondition (dry-run works
// anywhere).
func macosUserRun(cfg *jsonx.OrderedMap, workspace string, agents, agentArgv []string, repoRoot string, dryRun bool) int {
	runProxy := run.RunWithProxy
	materialize := func(nixRoot string, packages []any) (*macosuser.Darwin, bool, error) {
		pkgs, err := darwinpkg.Materialize(nixRoot, packages, "", os.Stderr)
		if err != nil {
			return nil, false, err
		}
		env := jsonx.NewOrderedMap()
		// darwinpkg env is a small map (at most PKG_CONFIG_PATH); sort for a
		// deterministic OrderedMap ordering.
		keys := make([]string, 0, len(pkgs.Env))
		for k := range pkgs.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			env.Set(k, pkgs.Env[k])
		}
		return &macosuser.Darwin{
			PathPrefix: pkgs.PathPrefix,
			Env:        env,
			Skipped:    pkgs.Skipped,
		}, true, nil
	}
	deps := macosuser.RealDeps(runProxy, materialize)
	return macosuser.RunMacosUser(deps, macosuser.Options{
		Workspace: workspace,
		Config:    cfg,
		Agents:    agents,
		AgentArgv: agentArgv,
		RepoSrc:   filepath.Join(repoRoot, "src"),
		DryRun:    dryRun,
	})
}

// runCheck parses the check/doctor flags (--build/--no-build) from args and runs
// the native Go check. args is the rewritten argv[1:] (subcommand included), so
// the leading token is "check"/"doctor". Exit code: 0 = no failures, 1 = fail.
func runCheck(args []string) int {
	opts := checkcmd.NewDefaultOptions()
	opts.Color = true
	// Parse flags. Only --build/--no-build are defined for check/doctor; any
	// stray flag is ignored (typer would error, but the front door has already
	// classified this as the check subcommand — the flag surface is tiny).
	for _, a := range args {
		switch a {
		case "--no-build":
			opts.Build = false
		case "--build":
			opts.Build = true
		}
	}
	return checkcmd.Check(opts)
}
