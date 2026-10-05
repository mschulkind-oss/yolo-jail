package cli

// probeservices.go is `yolo internal probe-services`: the macos-user launch's host-service
// witness, run INSIDE the sandbox by the confined stage macosuser.ProbeServicesArgv builds.
//
// It is the container boot's last step (entrypoint.ProbeServiceReachability, then the boot's
// own gate) over an Env read from this process's environment — the session env file the
// stage's reader sourced, which is where the endpoint variables, the host-loopback disposition
// and the reachability hatch all arrive — translated by entrypoint.DarwinEnvFrom, the one
// spelling of "this is the macos-user sandbox" (it is what makes the witness choose its
// native-sandbox wording). See internal/macosuser/serviceprobe.go for why the question has to
// be asked from in here.
//
// Hidden: its caller is that stage, and a hand-run outside a sandbox would answer for the
// wrong account.

import (
	"fmt"
	"io"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/provision"
)

const probeServicesUsage = "usage: yolo internal " + macosuser.ProbeServicesVerb +
	"   (no arguments: it probes every YOLO_SERVICE_*_ENDPOINT in its environment)"

// runProbeServices runs the witness and maps its answer onto the stage's status:
//
//   - 0: every enabled service is usable, or YOLO_ALLOW_UNREACHABLE_SERVICES let an unusable one
//     through (the witness says so on stderr). Silent when healthy, as at a container's boot.
//   - provision.RefusedStatus (78): the witness REFUSED. Its message, naming each service, why,
//     and the hatch, is already on stderr, and the launch stops on this status
//     (macosuser.runServiceProbe). A dedicated status rather than 1, so a refusal cannot be
//     confused with sudo or sandbox-exec failing to run the stage at all.
//   - 2: misuse.
func runProbeServices(args, environ []string, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintln(stderr, probeServicesUsage)
		return 2
	}
	vars := envMap(environ)
	home := vars["JAIL_HOME"]
	if home == "" {
		home = vars["HOME"]
	}
	if home == "" {
		home = macosuser.SandboxHome()
	}
	e := entrypoint.DarwinEnvFrom(vars, home)
	e.Stderr = stderr
	if err := entrypoint.RunServiceProbe(e); err != nil {
		return provision.RefusedStatus
	}
	return 0
}
