// Command yolo-entrypoint is the Go port of src/entrypoint (PID-1 role): the
// in-jail bootstrap that generates every file a jail session needs (shims,
// launchers, per-agent configs, bashrc, mise config, CA bundle, helper scripts),
// spawns the socat port-forwarders and jail-daemon supervisor, then execs bash
// with the requested command.
//
// It is the "go" arm of the dual-impl image seam: flake.nix's /bin/yolo-entrypoint
// wrapper branches on YOLO_ENTRYPOINT_IMPL (default python) to this binary. All
// boot orchestration lives in internal/entrypoint.Main; this file is the thin
// argv → Main shim (mirroring src/entrypoint/__main__.py's `from . import main`).
//
// On success Main never returns (it execs bash). It returns an error only if the
// final exec itself fails, in which case we exit non-zero.
package main

import (
	"fmt"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

func main() {
	if err := entrypoint.Main(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "yolo-entrypoint:", err)
		os.Exit(1)
	}
}
