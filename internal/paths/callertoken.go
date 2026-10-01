package paths

import (
	"regexp"
	"strings"
)

// ServiceCallerTokenSuffix composes YOLO_SERVICE_<NAME>_TOKEN, the variable that carries a
// pack SERVICE's per-launch CALLER TOKEN (coined in docs/reference/wire-bridge.md, WB-D18):
// a random secret the launcher mints for every selected service that runs a jail daemon,
// hands to that daemon and to every agent a derive points at the service, and that the
// daemon then demands on every request. It exists because "the jail is the boundary" is not
// true of a service on loopback: a jail on `network.mode: host` and a macos-user sandbox
// share the host's loopback, and a nested podman forced onto `--net=host` shares its parent
// jail's, so a port the service listens on is one every process on that loopback can reach,
// and a port it does not yet hold is one such a process can take first.
//
// The value is a credential, which is why it travels only through the 0600 per-entry
// channel (yolo-user-env.sh) and the 0600 per-agent env files, and never on an argv, in a
// rendered config file, or in a log. A rendered config names the VARIABLE and the agent
// reads the value from its own environment.
//
// The producer (internal/cli/run, which mints it and writes the channel line) and the
// consumers (the wire bridge, which demands it, internal/packload, which composes the
// variable's name onto the addresses the service serves, and the entrypoint's boot log)
// live in different binaries, so the spelling lives here for ServiceEnvVarPrefix's reason.
// The suffix keeps it apart from the _ENDPOINT variables the reachability witness and the
// macos-user grant scan for.
const ServiceCallerTokenSuffix = "_TOKEN"

var serviceSlugRe = regexp.MustCompile(`[^A-Za-z0-9]+`)

// ServiceEnvSlug is a service name folded into the middle of its YOLO_SERVICE_* variables:
// every run of non-alphanumerics becomes one underscore, the ends are trimmed, and the rest
// is upper-cased. `wire-bridge` → `WIRE_BRIDGE`.
func ServiceEnvSlug(serviceName string) string {
	s := serviceSlugRe.ReplaceAllString(serviceName, "_")
	return strings.ToUpper(strings.Trim(s, "_"))
}

// ServiceCallerTokenEnv is the variable carrying serviceName's caller token:
// YOLO_SERVICE_<SLUG>_TOKEN. "" for a name that folds to nothing, which no service can have.
func ServiceCallerTokenEnv(serviceName string) string {
	slug := ServiceEnvSlug(serviceName)
	if slug == "" {
		return ""
	}
	return ServiceEnvVarPrefix + slug + ServiceCallerTokenSuffix
}

// JailCallerTokenDir is where the entrypoint writes each caller token it was handed as a file
// of its own, 0600, named by the token's variable (JailCallerTokenFile). It exists for a
// client that reads a credential from a FILE and cannot be handed a secret any other way, as
// the AWS SDKs' container-credentials provider can (AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE;
// docs/plans/notch-convergence.md §2.3). The aws-auth pack used it until its token was scoped
// to the selecting agents' env files (docs/reference/providers.md OQ-CN7 (c)); a
// scoped token gets no file. On the /run tmpfs, so it dies with the container, and in-jail
// only, so no host process can read it.
const JailCallerTokenDir = "/run/yolo/caller-tokens"

// JailCallerTokenFile is the in-jail file holding the caller token carried in tokenEnv — the
// path a pack's `env` contribution names for a client that reads its credential from a file.
func JailCallerTokenFile(tokenEnv string) string { return JailCallerTokenDir + "/" + tokenEnv }

// IsServiceCallerTokenEnv reports whether key has the shape ServiceCallerTokenEnv composes:
// YOLO_SERVICE_<SLUG>_TOKEN with a non-empty slug.
func IsServiceCallerTokenEnv(key string) bool {
	return len(key) > len(ServiceEnvVarPrefix)+len(ServiceCallerTokenSuffix) &&
		strings.HasPrefix(key, ServiceEnvVarPrefix) && strings.HasSuffix(key, ServiceCallerTokenSuffix)
}
