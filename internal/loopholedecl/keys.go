package loopholedecl

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// keys.go is the manifest's key vocabulary — the thing the loader this package
// replaced did not have at all, which is why `"version": 1` was declared by every
// bundled manifest, documented as the schema version, and read by nothing, with no
// way for anyone to notice.
//
// The KEY set is what makes the strict/tolerant split possible: strict reports a
// key it does not know (a typo is otherwise a declaration that silently does
// nothing), tolerant ignores it and says so (a key only a newer build knows is
// version skew, and refusing it would refuse the jail).

// Manifest key names. Spelled once so the walk and the unknown-key census cannot
// disagree about what is known.
const (
	keyName           = "name"
	keyDescription    = "description"
	keyVersion        = "version"
	keyEnabled        = "enabled"
	keyTransport      = "transport"
	keyLifecycle      = "lifecycle"
	keyIntercepts     = "intercepts"
	keyBrokerIP       = "broker_ip"
	keyCACert         = "ca_cert"
	keyJailEnv        = "jail_env"
	keyDoctorCmd      = "doctor_cmd"
	keyHostDaemon     = "host_daemon"
	keyJailDaemon     = "jail_daemon"
	keyHostBindMounts = "host_bind_mounts"
	keyHostDevices    = "host_devices"
	keyStateFiles     = "state_files"
	keyRequires       = "requires"
	keyPlatforms      = "platforms"

	keyCmd           = "cmd"
	keyEnv           = "env"
	keyPublishes     = "publishes"
	keyRequestEnd    = "request_end"
	keyRestart       = "restart"
	keyHost          = "host"
	keyContainer     = "container"
	keyReadonly      = "readonly"
	keyCommandOnPath = "command_on_path"
	keyFileExists    = "file_exists"
)

// The known key set per object in the schema. `jail_env` and `host_daemon.env`
// are deliberately absent: their keys are environment variable NAMES, so every
// key in them is known by construction.
var (
	topKeys = []string{
		keyName, keyDescription, keyVersion, keyEnabled, keyTransport, keyLifecycle,
		keyIntercepts, keyBrokerIP, keyCACert, keyJailEnv, keyDoctorCmd, keyHostDaemon,
		keyJailDaemon, keyHostBindMounts, keyHostDevices, keyStateFiles, keyRequires,
		keyPlatforms,
	}
	hostDaemonKeys    = []string{keyCmd, keyEnv, keyPublishes, keyRequestEnd}
	jailDaemonKeys    = []string{keyCmd, keyRestart}
	interceptKeys     = []string{keyHost}
	hostBindMountKeys = []string{keyHost, keyContainer, keyReadonly}
	requiresKeys      = []string{keyCommandOnPath, keyFileExists}
)

// KnownKeys returns the manifest's top-level key names, sorted. Exported for
// authoring tools that want to suggest a spelling.
func KnownKeys() []string {
	out := copyOf(topKeys)
	sort.Strings(out)
	return out
}

// unknownKeyNotes reports every key in the decoded object that the schema does
// not know, phrased for the caller's strictness: strict says "unknown key" (an
// authoring mistake), tolerant says "ignoring unknown key" (version skew).
//
// It runs AFTER the structural walk succeeded, so every value it descends into
// already has the type the schema requires; a value of the wrong type was already
// refused with a better message than "unknown key" would be.
func unknownKeyNotes(data *jsonx.OrderedMap, manifestPath string, strict bool) []string {
	var out []string
	check := func(m *jsonx.OrderedMap, prefix string, known []string) {
		for _, k := range m.Keys() {
			if inList(k, known) {
				continue
			}
			out = append(out, unknownKeyNote(manifestPath, prefix+k, known, strict))
		}
	}
	check(data, "", topKeys)
	if m, ok := getOrNil(data, keyHostDaemon).(*jsonx.OrderedMap); ok {
		check(m, keyHostDaemon+".", hostDaemonKeys)
	}
	if m, ok := getOrNil(data, keyJailDaemon).(*jsonx.OrderedMap); ok {
		check(m, keyJailDaemon+".", jailDaemonKeys)
	}
	if m, ok := getOrNil(data, keyRequires).(*jsonx.OrderedMap); ok {
		check(m, keyRequires+".", requiresKeys)
	}
	if list, ok := getOrNil(data, keyIntercepts).([]any); ok {
		for i, entry := range list {
			if m, isMap := entry.(*jsonx.OrderedMap); isMap {
				check(m, fmt.Sprintf("%s[%d].", keyIntercepts, i), interceptKeys)
			}
		}
	}
	if list, ok := getOrNil(data, keyHostBindMounts).([]any); ok {
		for i, entry := range list {
			if m, isMap := entry.(*jsonx.OrderedMap); isMap {
				check(m, fmt.Sprintf("%s[%d].", keyHostBindMounts, i), hostBindMountKeys)
			}
		}
	}
	return out
}

func unknownKeyNote(manifestPath, path string, known []string, strict bool) string {
	if strict {
		return fmt.Sprintf(
			"%s: unknown key %q — not part of the loophole manifest schema, so it declares"+
				" nothing; known here: %s",
			manifestPath, path, strings.Join(sortedCopy(known), ", "))
	}
	return fmt.Sprintf(
		"%s: ignoring unknown key %q — this build does not know it, so whatever it declares"+
			" is not honored (version skew; a build that knows the key will read it)",
		manifestPath, path)
}

func sortedCopy(list []string) []string {
	out := copyOf(list)
	sort.Strings(out)
	return out
}
