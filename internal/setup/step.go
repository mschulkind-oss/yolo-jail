package setup

// Choose spells "pick the setup named name" as a next step, the way the reader picks one: the
// `--setup <name>` flag on the command they type, or, inside a launched session (YOLO_VERSION
// set), `YOLO_SETUP=<name>` in that session's environment. It is the one spelling every message
// naming another setup as its remedy uses, so no message names a removed key or flag (`--at`,
// `runtime`, `confinement`) as a step.
func Choose(name string, inSession bool) string {
	if inSession {
		return "`" + EnvVar + "=" + name + "`"
	}
	return "`--setup " + name + "`"
}

// ContainerStep is the next step a Seatbelt message offers when a container would shadow, mount
// or publish what the sandbox cannot: both container setups, each spelled by Choose. It reads as
// a parenthetical after the words "a container setup".
func ContainerStep(inSession bool) string {
	return "(" + Choose("podman-self", inSession) + ", or " + Choose("apple-container-self", inSession) +
		" with Apple Container)"
}
