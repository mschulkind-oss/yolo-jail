package main

import (
	"github.com/mschulkind-oss/yolo-jail/internal/checkcmd"
)

// nativeDispatch maps a subcommand to its native Go handler. Unconditionally
// native slices (config-ref, init, …) register here as they land; gated slices
// (check/doctor) register here too but only run when frontdoor.IsNative gates
// them on via YOLO_IMPL=go — dispatchNative is only reached for a subcommand
// IsNative already approved, so a plain map entry is correct.
var nativeDispatch = map[string]func(args []string) int{
	"check":  runCheck,
	"doctor": runCheck, // doctor is an alias for check (same body + flag).
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
