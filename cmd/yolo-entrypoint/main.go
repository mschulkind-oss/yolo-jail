// Command yolo-entrypoint is the in-jail bootstrap (PID-1 role): it generates
// every file a jail session needs (shims, launchers, per-agent configs, bashrc,
// mise config, CA bundle, helper scripts), spawns the socat port-forwarders and
// jail-daemon supervisor, then execs bash with the requested command.
//
// All boot orchestration lives in internal/entrypoint.Main; this file is the
// thin argv → Main shim.
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
