package loopholedecl

import (
	"net"
	"strconv"
	"strings"
)

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

// TokenListen is a jail daemon's LISTEN ADDRESS token, legal in `jail_daemon.cmd` and in the
// values of a pack `env` contribution `served_by` that daemon. It resolves to the `host:port`
// the daemon serves at in THIS launch: `jail_daemon.listen` as declared on a jail with a
// network namespace of its own, and a port the launcher picked on one that shares the
// launcher's (docs/plans/notch-convergence.md §2.4, NC-D41). One declaration, composed into
// both the daemon's argv and its clients' pointer, so the port is written once.
//
// Spelled here because three packages read it: this schema refuses it where it cannot
// resolve, internal/loopholes resolves it in the jail-daemon payload, and internal/packload
// resolves it in a served pack env value.
const TokenListen = "{listen}"

// TokenCallerToken is a jail daemon's CALLER TOKEN token (paths.ServiceCallerTokenEnv), legal as
// the WHOLE value of a variable in a `profile`-gated pack `env` contribution `served_by` that
// daemon. It resolves to the per-launch token the launcher minted for the daemon, and a
// contribution naming it SCOPES the token: the token reaches only the agents that contribution
// is delivered to, in their own env files, and is never exported into the shared per-entry
// channel every jail process inherits (docs/reference/providers.md OQ-CN7 (c)).
// Profile-gated because an ungated contribution is delivered to every process, which is the
// exposure the scoping removes. Whole-value because a client sends the value verbatim as a
// credential (the AWS SDKs send AWS_CONTAINER_AUTHORIZATION_TOKEN as `Authorization`), so a
// token spliced into a longer string is one no daemon demands.
const TokenCallerToken = "{caller_token}"

// ListenAddressProblem returns why raw cannot be a jail daemon's `listen`, or "". It must be a
// LOOPBACK IP literal and a port, `127.0.0.1:1460` or `[::1]:1460`: the address is bound inside
// the jail and dialed by its clients over plain http, which the AWS SDK allows only to
// loopback, and a hostname would be resolved by whoever reads it.
func ListenAddressProblem(raw string) string {
	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		return "must be a loopback host:port, such as \"127.0.0.1:1460\""
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "must name a loopback IP literal (127.0.0.1 or [::1]), because the daemon " +
			"binds it inside the jail and its clients dial it"
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "must carry a port between 1 and 65535"
	}
	return ""
}

// refuseListenTokenMismatch holds `jail_daemon.listen` and TokenListen in cmd together: a
// declared address the argv never names is a port the launcher would move while the daemon
// kept binding its own, and the token with no declaration resolves to nothing.
func refuseListenTokenMismatch(manifestPath string, cmd []string, listen string) error {
	named := false
	for _, s := range cmd {
		if strings.Contains(s, TokenListen) {
			named = true
			break
		}
	}
	switch {
	case named && listen == "":
		return Errorf("%s: 'jail_daemon.cmd' names '%s', but 'jail_daemon.listen' is not "+
			"declared — the token resolves to that address, so declare it (e.g. "+
			"\"127.0.0.1:1460\") or drop the token", manifestPath, TokenListen)
	case listen != "" && !named:
		return Errorf("%s: 'jail_daemon.listen' is declared but 'jail_daemon.cmd' never "+
			"names '%s' — the launcher moves the address on a shared network namespace, and a "+
			"daemon binding a port of its own would not move with it; pass the token where "+
			"the daemon takes its listen address", manifestPath, TokenListen)
	}
	return nil
}

// TokenState is the per-loophole STATE dir token, legal in `ca_cert`,
// `host_daemon.cmd`, and `doctor_cmd`. It resolves
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
//
// {listen} is refused here too: it is a jail daemon's own address, which only the jail
// daemon's argv and its clients' pointer resolve.
func refuseJailTokenInHostField(manifestPath, field string, args []string) error {
	for _, s := range args {
		if strings.Contains(s, TokenListen) {
			return Errorf(
				"%s: %s names '%s', a jail daemon's listen address — this field resolves"+
					" on the HOST, where nothing substitutes it; only 'jail_daemon.cmd' and 'jail_daemon.host_cmd' take it",
				manifestPath, field, TokenListen)
		}
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
