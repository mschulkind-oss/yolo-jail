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
	keyDefaultEnabled = "default_enabled"
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
	keyServes         = "serves"
	keySettings       = "settings"

	// The `settings.<key>` DECLARATION keys (settings.go). `description` is shared
	// with the top level and spelled once, which is the whole reason this block of
	// constants exists.
	keyType    = "type"
	keyScope   = "scope"
	keyDefault = "default"

	keyCmd           = "cmd"
	keyEnv           = "env"
	keyPublishes     = "publishes"
	keyRequestEnd    = "request_end"
	keyPreamble      = "preamble"
	keyRestart       = "restart"
	keyHost          = "host"
	keyContainer     = "container"
	keyReadonly      = "readonly"
	keyCommandOnPath = "command_on_path"
	keyFileExists    = "file_exists"
)

// Retired manifest key names, kept ONLY to RECOGNIZE them and say what to write
// instead — the same shape RetiredTransportTLSIntercept/UnixSocket have one level
// down (enums.go), and for the same reason: a value that still validates is a value
// someone will keep using.
//
// None of these is in topKeys, so KnownKeys() never suggests one and the unknown-key
// census would call it unknown. THAT CENSUS MUST NEVER SEE THEM, which is why the
// refusal lives in the structural walk and the walk runs FIRST (decode returns on a
// walk error before unknownKeyNotes is called). The distinction is not cosmetic: the
// tolerant census says *"this build does not know it, so whatever it declares is not
// honored (version skew; a build that knows the key will read it)"* — which is exactly
// BACKWARDS for a key that was REMOVED. A newer build will never read `enabled`; it
// deleted it. Telling a reader to wait for one is telling them to wait forever, and
// meanwhile the loophole quietly takes the new default
// (docs/design/loophole-activation.md §4).
const (
	// RetiredKeyEnabled is the manifest's old enablement key. It was renamed to
	// `default_enabled` AND its default flipped (OQ-A9/R2), which is why a tolerance
	// is not available here: silently dropping `"enabled": true` would leave the
	// loophole OFF, and silently dropping `"enabled": false` would leave a loophole
	// its author disabled looking like one that merely said nothing. Both readings
	// are defensible, which is precisely why the manifest must not be guessed at.
	RetiredKeyEnabled = "enabled"
)

// # REVERSE SKEW, and why this file cannot fix it — read before adding a tripwire
//
// The refusal above protects a NEW build reading an OLD manifest. The opposite
// direction is the dangerous one and it is NOT SOLVED HERE: an OLD build reading a
// manifest that declares `default_enabled` does not know the key, tolerates it, and
// falls back to enabled-defaults-TRUE. So a manifest shipping a loophole meant to be
// OFF runs it ON under a yolo that predates the rename — a security regression
// delivered by an upgrade of the CONTENT rather than of the binary.
//
// Three things were considered and only the third is honest.
//
//	A schema-version or unknown-key REFUSAL an old build trips on does not exist,
//	and it does not exist ON PURPOSE. `version` is recognized-but-never-enum-checked
//	(see Manifest.Version: refusing it "would brick a jail whose baked entrypoint is
//	one just load behind"), unknown keys are tolerated by DecodeTolerant and again by
//	packload.LoopholeModules, and BOTH tolerances are rulings with the `tier` incident
//	as their measured cost. A value planted in new manifests purely to make old
//	binaries choke reverses those rulings, and it fires for every pack manifest —
//	including every harmless default-true one.
//
//	SHIPPING BOTH KEYS through a transition needs the refusal above scoped to
//	non-shipped manifests. Pack-shippedness is deliberately the CALLER's fact and not
//	the manifest's (internal/loopholes/load.go says why), and `yolo pack lint` runs
//	the strict decoder over yolo's own packs — so the scoping would thread through
//	three loaders and then exempt yolo from its own lint.
//
//	A VERSION FLOOR is the tempting one, and the reason it fails is worth keeping:
//	internal/version does stamp a real version into the binary, so a `min_yolo` key
//	is expressible. It is not ENFORCEABLE. A floor has to be checked by the READER,
//	and the reader that needs stopping is precisely the build that shipped before the
//	key existed — to which `min_yolo` is one more unknown key, tolerated. A floor
//	added today can only bind the NEXT deletion-shaped change, never this one.
//
// So the residual is a RELEASE NOTE, and the population is smaller than it looks.
// For the three BUNDLED manifests reverse skew is structurally impossible: they are
// embedded into the binary with go:embed, and internal/loopholes' materializeEmbedded
// is content-addressed over
// the embedded bytes, so a binary reads its own copy and never a newer one. The lone
// exception is BundledLoopholesDir preferring a repo checkout when one resolves —
// a yolo-jail developer whose binary predates this change and whose checkout does
// not, one `just load` from fixed. For PACK-shipped manifests, written by third
// parties and updated independently of the binary, it is real and unclosable from
// inside this package.

// retiredTopKeyRefusal returns the refusal text for a retired TOP-LEVEL key, or ""
// when the key is not retired. Written as a lookup over all keys rather than an `if`
// per key so retiring the next one is a single entry, not a second call site to
// forget.
func retiredTopKeyRefusal(key string) string {
	switch key {
	case RetiredKeyEnabled:
		return "'enabled' was retired from the loophole MANIFEST: write 'default_enabled'" +
			" instead, and note that THE DEFAULT FLIPPED — an absent 'default_enabled' means" +
			" the loophole is OFF, where an absent 'enabled' meant ON" +
			" (docs/design/loophole-activation.md R2: \"presence never activates\"). So" +
			" \"enabled\": true becomes \"default_enabled\": true, and \"enabled\": false is" +
			" now what saying nothing means — delete the key." +
			" THIS IS NOT THE CONFIG KEY: 'loopholes.<name>.enabled' in config.jsonc or" +
			" yolo-jail.jsonc is the USER's switch and is unchanged. The manifest key was" +
			" the pack author's DEFAULT, and it was renamed to say so."
	}
	return ""
}

// The known key set per object in the schema. `jail_env` and `host_daemon.env`
// are deliberately absent: their keys are environment variable NAMES, so every
// key in them is known by construction.
var (
	topKeys = []string{
		keyName, keyDescription, keyVersion, keyDefaultEnabled, keyTransport, keyLifecycle,
		keyIntercepts, keyBrokerIP, keyCACert, keyJailEnv, keyDoctorCmd, keyHostDaemon,
		keyJailDaemon, keyHostBindMounts, keyHostDevices, keyStateFiles, keyRequires,
		keyPlatforms, keyServes, keySettings,
	}
	// settingDeclKeys is the census for ONE `settings.<key>` declaration. It is
	// enforced by parseSettings and DELIBERATELY NOT descended into by
	// unknownKeyNotes: every other census in this file has two voices (strict says
	// "unknown key", tolerant says "ignoring unknown key"), and a settings
	// declaration has exactly one — refusal, in both decoders. See parseSettings for
	// why the version-boundary tolerance stops here.
	settingDeclKeys = []string{keyType, keyScope, keyDefault, keyDescription}
	// `preamble` is a host_daemon key and DELIBERATELY not a top-level one: it
	// describes the connection yolo serves in FRONT of a daemon, so it says
	// nothing for a transport:"none" loophole and nothing for a manifest with no
	// host_daemon at all. A top-level spelling would be a key an author could
	// write in a place where it silently declares nothing.
	hostDaemonKeys    = []string{keyCmd, keyEnv, keyPublishes, keyRequestEnd, keyPreamble}
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
