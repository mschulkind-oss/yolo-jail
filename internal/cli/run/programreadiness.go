package run

import "github.com/mschulkind-oss/yolo-jail/internal/paths"

// programReadinessArgs forwards the jail readiness act's two dials from the host environment
// into the container, each only when the user set it (docs/design/jail-notch-readiness.md,
// OQ-JR1): paths.AllowMissingProgramsEnv, the hatch its refusal offers, and
// paths.NoProgramReadinessEnv, which turns the act off.
//
// The forwarding is reachabilityOptOutArgs' argument exactly. The act runs in the jail's
// provisioning stage, the user types the variable on the HOST in front of `yolo`, and a
// container inherits nothing from the launcher's environment, so a dial that is only read
// in-jail is one nobody can reach; and the user the hatch exists for is the one whose jail will
// not start, who has no in-jail shell to set it from. The entrypoint bakes both values into the
// bootstrap it generates (entrypoint.BootstrapScript).
//
// EMITTED ONLY WHEN SET, for holdOnRefusalArgs' reason: the golden argv is a contract, and a
// launch that asked for neither must carry neither in its frozen environment.
//
// Container backends only, by where it is called: macos-user's stage does not run the readiness
// act yet (JR-D2), so there is nothing there for either dial to reach.
func (o *Options) programReadinessArgs() []string {
	var out []string
	for _, k := range []string{paths.AllowMissingProgramsEnv, paths.NoProgramReadinessEnv} {
		if v := o.Getenv(k); v != "" {
			out = append(out, "-e", k+"="+v)
		}
	}
	return out
}
