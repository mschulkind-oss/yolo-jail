package loopholedecl

import "strings"

// Module-dir tokens. {loophole_dir} resolves to the HOST-side absolute module
// dir and is legal in host_daemon.cmd, doctor_cmd and host_bind_mounts[].host;
// {jail_loophole_dir} resolves to the module dir's CONTAINER mount point
// (JailLoopholeDir) and is legal in jail_daemon.cmd. Two tokens on purpose —
// one token with two resolutions is the asymmetry an author discovers by
// debugging — and each is refused in the other half, at load, naming the fix.
//
// SUBSTITUTING them is not this package's job: the host-side resolution needs the
// module's real path, which is a runtime fact. Refusing the wrong one is, because
// "this token is illegal in this field" is a statement about the schema.
const (
	TokenLoopholeDir     = "{loophole_dir}"
	TokenJailLoopholeDir = "{jail_loophole_dir}"
)

// JailLoopholeDir returns the CONTAINER path where a loophole's module dir is
// bind-mounted (RuntimeArgsFor emits the -v). It is what {jail_loophole_dir}
// resolves to in jail_daemon.cmd, and it lives here because the refusal message
// below has to name it — a duplicated literal would drift the day the mount point
// moves.
func JailLoopholeDir(name string) string {
	return "/etc/yolo-jail/loopholes/" + name
}

// refuseJailTokenInHostField rejects {jail_loophole_dir} in a field that runs
// (or resolves) on the HOST.
func refuseJailTokenInHostField(manifestPath, field string, args []string) error {
	for _, s := range args {
		if strings.Contains(s, TokenJailLoopholeDir) {
			return Errorf(
				"%s: %s names '%s', the module dir's CONTAINER mount point — this field"+
					" resolves on the HOST; write '%s'",
				manifestPath, field, TokenJailLoopholeDir, TokenLoopholeDir)
		}
	}
	return nil
}

// refuseHostTokenInJailField rejects {loophole_dir} in a field that runs inside
// the container.
func refuseHostTokenInJailField(manifestPath, field string, args []string) error {
	for _, s := range args {
		if strings.Contains(s, TokenLoopholeDir) {
			return Errorf(
				"%s: %s names '%s', the module dir's HOST path — this command runs inside"+
					" the container, where the dir is mounted at %s; write '%s'",
				manifestPath, field, TokenLoopholeDir, JailLoopholeDir("<name>"), TokenJailLoopholeDir)
		}
	}
	return nil
}
