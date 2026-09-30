// Command yolo-entrypoint is the in-jail bootstrap (PID-1 role): it generates
// every file a jail session needs (shims, launchers, per-agent configs, bashrc,
// mise config, CA bundle, helper scripts), spawns the socat port-forwarders and
// jail-daemon supervisor, then execs bash with the requested command — or, as the
// container's main process, holds the jail open for the sessions that exec into it.
//
// All boot orchestration lives in internal/entrypoint.Main; this file is the
// thin argv → Main shim.
//
// On success Main never returns for a session (it execs bash), and for the main process
// returns once its hold ends: nil when it followed its first session out, and an
// *entrypoint.ExitStatus of 128+SIGTERM when a SIGTERM (a stop) ended it. It returns an error
// when the boot refused or the final exec failed, in which case we exit non-zero: with the
// status an *entrypoint.ExitStatus carries (provisioning's own), and 1 otherwise.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

func main() {
	err := entrypoint.Main(os.Args[1:])
	if err == nil {
		return
	}
	var status *entrypoint.ExitStatus
	if errors.As(err, &status) {
		if status.Message != "" {
			fmt.Fprintln(os.Stderr, "yolo-entrypoint:", status.Message)
		}
		os.Exit(status.Code)
	}
	fmt.Fprintln(os.Stderr, "yolo-entrypoint:", err)
	os.Exit(1)
}
