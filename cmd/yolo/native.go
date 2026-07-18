package main

import (
	"os/exec"

	"github.com/mschulkind-oss/yolo-jail/internal/checkcmd"
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
	"check":  runCheck,
	"doctor": runCheck, // doctor is an alias for check (same body + flag).
	"run":    runRun,
	"ps":     runPs,
}

// runPs runs the native `yolo ps` (list running jails). Gated behind
// YOLO_IMPL=go; plain typer.echo output, byte-parity with Python. args is
// ignored (ps takes no flags).
func runPs(_ []string) int {
	return pscmd.Run(pscmd.RealDeps(psRunCmd, runtime.DetectRuntime))
}

// psRunCmd runs a container-runtime probe and returns stdout (stderr discarded),
// matching Python's capture_output=True, text=True. A spawn error yields ""; the
// caller degrades (empty output → no jails / unknown workspace), never crashes.
func psRunCmd(argv []string) (string, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
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
