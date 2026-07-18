package main

import (
	"github.com/mschulkind-oss/yolo-jail/internal/checkcmd"
	"github.com/mschulkind-oss/yolo-jail/internal/runcmd"
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
}

// runRun parses the run flags (--network, --new, --profile, --dry-run) and the
// post-`--` command from args (the rewritten argv[1:], leading token "run") and
// runs the native Go container-launch. Gated behind YOLO_IMPL=go.
func runRun(args []string) int {
	opts := runcmd.NewDefaultOptions()
	opts.Color = true
	// args[0] is "run". Options precede an optional "--"; everything after "--"
	// is the command (ctx.args). Typer also accepts options anywhere before the
	// command, but the front-door rewrite always puts "run" first and the app's
	// flags before "--", so a simple scan suffices.
	i := 1
	rest := args[1:]
	afterDashDash := false
	var cmdArgs []string
	for i = 0; i < len(rest); i++ {
		a := rest[i]
		if afterDashDash {
			cmdArgs = append(cmdArgs, a)
			continue
		}
		switch a {
		case "--":
			afterDashDash = true
		case "--new":
			opts.New = true
		case "--profile":
			opts.Profile = true
		case "--dry-run":
			opts.DryRun = true
		case "--network":
			if i+1 < len(rest) {
				i++
				opts.Network = rest[i]
			}
		default:
			if len(a) > len("--network=") && a[:len("--network=")] == "--network=" {
				opts.Network = a[len("--network="):]
				continue
			}
			// An unrecognized bare token before "--" is treated as the start of
			// the command (typer would error, but the front door already
			// classified this as run; be lenient).
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
