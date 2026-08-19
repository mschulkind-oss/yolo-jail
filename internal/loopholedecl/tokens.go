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

// TokenState is the per-loophole STATE dir token, legal in `ca_cert`. It resolves
// (in internal/loopholes) to StateDirFor(<name>) under yolo's own state tree, which
// is name-keyed rather than staging-keyed and therefore survives a restage — the
// property that makes a pack-shipped CA possible at all, since a CA regenerated on
// every launch would break every long-lived TLS client in the jail.
//
// Named here for the same reason the two dir tokens are: the pack-shipped subset has
// to recognize it (a '{state}/ca.crt' is in scope, an absolute path is not), and a
// literal spelled in two packages is a literal that drifts.
const TokenState = "{state}"

// TokenSettings is the RESOLVED SETTINGS FILE token, legal in `host_daemon.cmd` and
// `doctor_cmd`. It resolves (in internal/loopholes) to a JSON file under the
// loophole's own state dir that YOLO WRITES after validating the user's values
// against this manifest's `settings` declarations.
//
// # Why a path, and why this is allowed where an `env` map is not
//
// There was no channel at all from core to a loophole's host daemon — the manifest
// spawns `--socket {socket}` and nothing else, and nothing set a config env var —
// so delivering settings needed a new one, and the obvious one is forbidden:
// `loopholes.<name>.env` is user-scope-only precisely because it reaches a host
// daemon's spawn ENVIRONMENT, which is how LD_PRELOAD would get there.
//
// A PATH is the one thing a spawn may carry, and the difference is not cosmetic:
// the workspace supplies VALUES, which core validates and then writes itself; it
// never supplies environment. Whoever edited the config decides what the numbers
// are, and yolo decides what the file says.
//
// # Refused when the manifest declares no settings
//
// A `{settings}` in an argv with an empty `settings` block would name a file core
// has no reason to write, and the daemon would be handed a path to nothing. Refused
// at load rather than resolved to a missing file, because "this token means nothing
// in this manifest" is a statement about the schema.
const TokenSettings = "{settings}"

// refuseSettingsTokenWithoutDeclaration rejects {settings} in a host-side field of a
// manifest that declares no settings keys.
func refuseSettingsTokenWithoutDeclaration(manifestPath, field string, args []string, declared int) error {
	if declared > 0 {
		return nil
	}
	for _, s := range args {
		if strings.Contains(s, TokenSettings) {
			return Errorf(
				"%s: %s names '%s', but this manifest declares no 'settings' — the token"+
					" resolves to a file yolo writes from the settings DECLARATIONS, so with"+
					" none there is nothing to write and the daemon would be handed a path to"+
					" a missing file; declare the keys or drop the token",
				manifestPath, field, TokenSettings)
		}
	}
	return nil
}

// refuseSettingsTokenInJailField rejects {settings} in a field that runs INSIDE the
// container. The settings file lives in the loophole's HOST-side state dir and is
// not among the paths that cross into a jail (StateFiles decides that, and a
// jail-side process reading its own settings is not a case anything has asked for),
// so the token would resolve to a host path the container cannot see.
func refuseSettingsTokenInJailField(manifestPath, field string, args []string) error {
	for _, s := range args {
		if strings.Contains(s, TokenSettings) {
			return Errorf(
				"%s: %s names '%s', which resolves to a HOST-side file in the loophole's"+
					" state dir — this command runs inside the container, where that path"+
					" does not exist; a jail-side process gets its configuration through"+
					" 'jail_env'",
				manifestPath, field, TokenSettings)
		}
	}
	return nil
}

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
