package run

import "github.com/mschulkind-oss/yolo-jail/internal/paths"

// holdOnRefusalArgs forwards the IN-JAIL hold's opt-in (paths.HoldOnRefusalEnv)
// from the host environment into the container, and composes the exec line the jail
// cannot compose for itself (paths.HoldExecEnv).
//
// The forwarding half is reachabilityOptOutArgs' argument exactly: the user types
// the variable on the HOST, in front of `yolo`, and a container inherits nothing
// from the launcher's environment — so an in-jail-only opt-in would be one nobody
// could reach, and the user it exists for is by definition the one whose jail will
// not start.
//
// The composition half is not. `<runtime> exec -it <container> bash` is a sentence
// only this side can say: `cname` is minted here, and the jail's own YOLO_RUNTIME is
// a hard-coded `podman` on every backend (commonEnvBlock), so an entrypoint under
// Apple Container that derived the command would print one that does not exist.
// Emitting it as a string rather than as its two parts keeps the jail's job to
// printing what it was handed.
//
// EMITTED ONLY WHEN THE USER ASKED, both of them: the golden argv is a contract
// (assemble_test.go) and a launch that did not opt into a hold must not carry the
// machinery for one in its frozen environment.
//
// Container backends only, by where it is called: the macos-user arm returns above
// assembleRunCmd, which is right — that backend has no container to hold open and
// no `exec` to offer.
func (o *Options) holdOnRefusalArgs(rt, cname string) []string {
	val := o.Getenv(paths.HoldOnRefusalEnv)
	if val == "" {
		return nil
	}
	return []string{
		"-e", paths.HoldOnRefusalEnv + "=" + val,
		"-e", paths.HoldExecEnv + "=" + rt + " exec -it " + cname + " bash",
	}
}
