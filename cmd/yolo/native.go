package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/checkcmd"
	"github.com/mschulkind-oss/yolo-jail/internal/configref"
	"github.com/mschulkind-oss/yolo-jail/internal/initcmd"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholescmd"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
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
