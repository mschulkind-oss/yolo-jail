package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokercmd"
	"github.com/mschulkind-oss/yolo-jail/internal/checkcmd"
	"github.com/mschulkind-oss/yolo-jail/internal/configref"
	"github.com/mschulkind-oss/yolo-jail/internal/frontdoor"
	"github.com/mschulkind-oss/yolo-jail/internal/initcmd"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholescmd"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prunecmd"
	"github.com/mschulkind-oss/yolo-jail/internal/pscmd"
	"github.com/mschulkind-oss/yolo-jail/internal/runcmd"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// nativeDispatch maps a subcommand to its native Go handler. Unconditionally
// native slices (config-ref, init, …) register here as they land; gated slices
// (check/doctor/run) register here too but only run when frontdoor.IsNative
// gates them on via YOLO_IMPL=go — dispatchNative is only reached for a
// subcommand IsNative already approved, so a plain map entry is correct.
var nativeDispatch = map[string]func(args []string) int{
	"check":            runCheck,
	"doctor":           runCheck, // doctor is an alias for check (same body + flag).
	"run":              runRun,
	"ps":               runPs,
	"loopholes":        runLoopholes,
	"config-ref":       runConfigRef,
	"init":             runInit,
	"init-user-config": runInitUserConfig,
	"broker":           runBroker,
	"prune":            runPrune,
}

// runPrune runs the native `yolo prune` (disk reclaim). Parses the prune flags
// (default dry-run; --apply reclaims). Gated behind YOLO_IMPL=go; ANSI-stripped
// output contract, byte-exact reclaim decisions.
func runPrune(args []string) int {
	opts := prunecmd.NewDefaultOptions()
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
	return prunecmd.Run(opts)
}

// runBroker dispatches `yolo broker {status,stop,restart,logs}`. args is the
// rewritten argv[1:] (args[0]=="broker"). Gated behind YOLO_IMPL=go; info-parity
// output, exact exit codes + paths + tail argv.
func runBroker(args []string) int {
	var sub string
	var rest []string
	if len(args) > 1 {
		sub = args[1]
		rest = args[2:]
	}
	deps := brokercmd.RealDeps()
	switch sub {
	case "status":
		return brokercmd.Status(deps)
	case "stop":
		return brokercmd.Stop(deps)
	case "restart":
		return brokercmd.Restart(deps)
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
		return brokercmd.Logs(deps, lines, follow)
	default:
		// Unknown/absent sub-subcommand → Python (typer prints group help).
		return delegateToPython(args)
	}
}

// runInit runs `yolo init` (scaffold yolo-jail.jsonc + briefing). Parses
// repeatable --mount/-m. Gated behind YOLO_IMPL=go; written file is byte-exact,
// briefing is info-parity Go-native color.
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
	return initcmd.Init(cwd, mounts, os.Stdout, isTTYStdout())
}

// runInitUserConfig runs `yolo init-user-config`. Gated behind YOLO_IMPL=go.
func runInitUserConfig(_ []string) int {
	return initcmd.InitUserConfig(os.Stdout)
}

func isTTYStdout() bool {
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// runConfigRef prints the full configuration reference. Gated behind
// YOLO_IMPL=go; info-parity Go-native output (color on a TTY). args ignored.
func runConfigRef(_ []string) int {
	return configref.RunStdout()
}

// runLoopholes dispatches the `yolo loopholes {list,status,enable,disable}`
// group. args is the rewritten argv[1:], so args[0] == "loopholes" and args[1]
// is the sub-subcommand. Gated behind YOLO_IMPL=go; plain typer.echo output
// (byte-parity with Python).
func runLoopholes(args []string) int {
	// args: ["loopholes", <sub>, <rest>...]
	var sub string
	var rest []string
	if len(args) > 1 {
		sub = args[1]
		rest = args[2:]
	}
	deps := loopholescmd.RealDeps()
	switch sub {
	case "", "list":
		return loopholescmd.List(deps)
	case "status":
		return loopholescmd.Status(deps)
	case "enable", "disable":
		if len(rest) < 1 {
			fmt.Fprintf(os.Stderr, "Usage: yolo loopholes %s <name>\n", sub)
			return 1
		}
		return loopholescmd.SetEnabled(deps, rest[0], sub == "enable")
	default:
		// Unknown sub-subcommand: fall back to Python (typer prints the group
		// help / error). Delegation keeps behavior faithful for edge cases.
		return delegateToPython(args)
	}
}

// runPs runs the native `yolo ps` (list running jails). Gated behind
// YOLO_IMPL=go; plain typer.echo output, byte-parity with Python. args is
// ignored (ps takes no flags). Uses PLATFORM-AWARE runtime resolution (audit
// §B/D11): on macOS with Apple Container running, `podman ps` would be empty and
// the tracking-prune would delete live jails' files.
func runPs(_ []string) int {
	detect := func() string {
		return runtime.PsRuntime(paths.IsMacOS, func(bin string) bool {
			_, err := exec.LookPath(bin)
			return err == nil
		})
	}
	return pscmd.Run(pscmd.RealDeps(psRunCmd, detect))
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
// post-`--` command from args (the rewritten argv[1:]) and runs the native Go
// container-launch. Gated behind YOLO_IMPL=go.
//
// The front-door RewriteArgv inserts "run" at the `--` position, so flags that
// preceded `--` end up BEFORE the "run" token (e.g. `yolo --new -- true` →
// [--new, run, --, true]). We therefore scan the WHOLE args: skip the "run"
// token wherever it appears, parse flags until `--`, and take everything after
// `--` as the command (ctx.args).
func runRun(args []string) int {
	opts := runcmd.NewDefaultOptions()
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
	// Set the tmux/kitty jail indicator (how the user knows a terminal is inside
	// a jail — a safety affordance) around the run, restoring on exit. This is
	// the native run path (never a delegation), so Go owns the indicator here —
	// mirrors Python's _tmux_rename_window / kitty tab branding (audit §B#4).
	restore := frontdoor.SetupJailIndicator()
	if restore != nil {
		defer restore()
	}
	return runcmd.Run(opts)
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
