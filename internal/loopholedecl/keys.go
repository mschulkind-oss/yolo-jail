package loopholedecl

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/json5"
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
	keyCallBroker     = "call_broker"
	// The `settings.<key>` DECLARATION keys (settings.go). `description` is shared
	// with the top level and spelled once, which is the whole reason this block of
	// constants exists.
	keyType    = "type"
	keyScope   = "scope"
	keyDefault = "default"
	// keyDisclose is a bool setting's launch disclosure (OQ-SSO10); see Setting.Disclose.
	keyDisclose = "disclose"
	// keyLegacyWorkspace reads a legacy project file's value of a user setting; see
	// Setting.LegacyWorkspace.
	keyLegacyWorkspace = "legacy_workspace"

	keyCmd           = "cmd"
	keySettingsCheck = "settings_check"
	keyEnv           = "env"
	keyPublishes     = "publishes"
	keyRequestEnd    = "request_end"
	keyPreamble      = "preamble"
	keyLaunchCheck   = "launch_check"
	keyStartupReason = "startup_reason"
	keyDetach        = "detach"
	keyLaunchInput   = "launch_input"
	keyRestart       = "restart"
	keyCallerToken   = "caller_token"
	keyListen        = "listen"
	keyHostCmd       = "host_cmd"
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
// (docs/reference/loophole-system.md#default_enabled-versus-enabled, whose reverse-skew
// warning is this paragraph's source).
const (
	// RetiredKeyEnabled is the manifest's first enablement key. OQ-A9 renamed it to
	// `default_enabled` and flipped its default; LG-D3 then retired that too
	// (docs/reference/loophole-grants.md §4.5).
	RetiredKeyEnabled = "enabled"
	// RetiredKeyDefaultEnabled is the pack author's default for whether the loophole ran.
	// Every loophole now runs whenever its pack is selected and grants nothing until a grant
	// covers the project, so the key would have one legal value and no reader (LG-D3).
	RetiredKeyDefaultEnabled = "default_enabled"
)

// # Reverse skew
//
// The refusal protects a NEW build reading an OLD manifest. The opposite direction, an
// OLD build reading a manifest with neither key, reads the absence as OFF (R2): the old
// build does not run a loophole written for this one, which is the fail-closed direction.
//
// The refusal runs in BOTH decoders, so a jail an older yolo launched, whose frozen pack
// tree still declares `default_enabled`, loses those records at an attach by this yolo
// until it is relaunched; relaunching stages the current manifests.

// # The legacy window (docs/reference/config/rulings.md LC-D10)
//
// A LEGACY manifest (manifest.jsonc or manifest.json, the form v0.15.1 read) that declares
// `default_enabled` is read as what the key meant there, never refused: v0.15.1 ran a loophole on
// `default_enabled: true` unless a config switched it off, and on `false` only where a config's
// `loopholes.<name>.enabled: true` switched it on. `true` is what selection means now, so it reads
// as nothing. `false` reads as a loophole off until its launch holds a grant
// (Manifest.LegacyDefaultOff), and a manifest that declares no grant kinds of its own gets the one
// kind a legacy switch becomes (LegacyRunGrantKind), so the old switch, its TOML conversion and
// `yolo request` all spell it. A loophole.toml declaring the key is refused as LG-D3 rules: the
// window is for a manifest written before that form existed. The older `enabled` key stays refused
// in both forms; v0.15.1 refused it too.

// LegacyRunGrantKind is the grant kind a legacy manifest's `default_enabled: false` declares when
// the manifest declares none of its own: the kind a `loopholes.<name>.enabled: true` becomes
// (Grant.LegacyEnable), and the one a project asks for with `yolo request <pack> run`.
const LegacyRunGrantKind = "run"

// readLegacyDefaultEnabled reads a legacy manifest's `default_enabled`: off is true when it says
// false. A non-boolean is refused, as v0.15.1 refused it ("Type-checked, not coerced").
func readLegacyDefaultEnabled(data *jsonx.OrderedMap, manifestPath string) (off bool, err error) {
	v, ok := data.Get(RetiredKeyDefaultEnabled)
	if !ok {
		return false, nil
	}
	b, isBool := v.(bool)
	if !isBool {
		return false, Errorf("%s: 'default_enabled' must be true or false", manifestPath)
	}
	return !b, nil
}

// legacyRunGrant is the grant kind legacyDefaultOff declares for a manifest with none.
func legacyRunGrant(name string) Grant {
	return Grant{
		Kind:         LegacyRunGrantKind,
		Text:         "run the " + name + " loophole",
		LegacyEnable: true,
	}
}

// legacyRunAccess is the access line of a manifest given legacyRunGrant: its description, or a
// line naming the loophole when it has none.
func legacyRunAccess(name, description string) string {
	if strings.TrimSpace(description) != "" {
		return description
	}
	return "what the " + name + " loophole serves"
}

// LegacyNotesDir is what the legacy window read in the module's manifest, one note per key, for
// `yolo pack lint` to print beside its pass: what each key reads as, and what `yolo pack migrate`
// asks the author to restate. nil for a loophole.toml, for a manifest the window reads nothing of,
// and for one that does not decode (lint reports that as a problem).
func LegacyNotesDir(dir string) []string {
	var out []string
	for _, n := range LegacyNotes(dir) {
		out = append(out, n.Text)
	}
	return out
}

// LegacyNote is one thing a legacy manifest says through the window (LC-D10). Restate is
// true when loophole.toml has no spelling for it, so `yolo pack migrate` refuses until the
// author restates it, and false when the migration can drop it as meaning nothing.
type LegacyNote struct {
	Text    string
	Restate bool
}

// LegacyNotes is LegacyNotesDir with each note's kind, for `yolo pack migrate`.
func LegacyNotes(dir string) []LegacyNote {
	raw, err := readManifest(dir)
	if err != nil || looksLikeTOML(raw) {
		return nil
	}
	if _, err := Decode(raw, dir); err != nil {
		return nil
	}
	decoded, err := json5.Decode(raw)
	if err != nil {
		return nil
	}
	data, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		return nil
	}
	manifestPath := ManifestPath(dir)
	var out []LegacyNote
	if v, ok := data.Get(RetiredKeyDefaultEnabled); ok {
		if v == true {
			out = append(out, LegacyNote{Text: fmt.Sprintf("%s: default_enabled: true reads as nothing: a loophole runs whenever its pack "+
				"is selected. `yolo pack migrate` drops the key.", manifestPath)})
		} else {
			out = append(out, LegacyNote{Restate: true, Text: fmt.Sprintf("%s: default_enabled: false reads as a loophole that runs only where a config "+
				"grants its %q kind (a legacy `loopholes.<name>.enabled: true` does). loophole.toml has no such key: "+
				"before `yolo pack migrate`, declare what a project may be granted under [[grants]], with 'access', "+
				"and have the daemon read the grant file, or delete the key if the loophole should run whenever its "+
				"pack is selected.", manifestPath, LegacyRunGrantKind)})
		}
	}
	if block, ok := getOrNil(data, keySettings).(*jsonx.OrderedMap); ok {
		for _, key := range block.Keys() {
			decl, _ := getOrNil(block, key).(*jsonx.OrderedMap)
			if decl == nil || getOrNil(decl, keyScope) != SettingScopeWorkspace {
				continue
			}
			out = append(out, LegacyNote{Restate: true, Text: fmt.Sprintf("%s: settings.%s.scope = %q reads as %q: the user's value is every project's "+
				"default, and a project's replaces it there. Write scope = %q before `yolo pack migrate`.",
				manifestPath, key, SettingScopeWorkspace, SettingScopeRequest, SettingScopeRequest)})
		}
	}
	return out
}

// retiredTopKeyRefusal returns the refusal text for a retired TOP-LEVEL key, or ""
// when the key is not retired. Written as a lookup over all keys rather than an `if`
// per key so retiring the next one is a single entry, not a second call site to
// forget. The text follows "<manifest path>: ".
//
// legacy says the manifest is the legacy form, where the window reads `default_enabled`
// (readLegacyDefaultEnabled) rather than refusing it.
func retiredTopKeyRefusal(key string, legacy bool) string {
	switch key {
	case RetiredKeyDefaultEnabled:
		if legacy {
			return ""
		}
		return "default_enabled is gone. A loophole runs whenever its pack\n" +
			"  is selected and grants nothing until a grant covers the project; declare what can be granted under\n" +
			"  [[grants]] instead, and remove the key."
	case RetiredKeyEnabled:
		return "'enabled' is gone, and so is the 'default_enabled' that replaced it. A loophole runs" +
			" whenever its pack is selected and grants nothing until a grant covers the project;" +
			" declare what can be granted under [[grants]] instead, and remove the key."
	}
	return ""
}

// The known key set per object in the schema. `jail_env` and `host_daemon.env`
// are deliberately absent: their keys are environment variable NAMES, so every
// key in them is known by construction.
var (
	topKeys = []string{
		keyName, keyDescription, keyVersion, keyTransport, keyLifecycle,
		keyIntercepts, keyBrokerIP, keyCACert, keyJailEnv, keyDoctorCmd, keyHostDaemon,
		keyJailDaemon, keyHostBindMounts, keyHostDevices, keyStateFiles, keyRequires, keyPlatforms, keyServes,
		keySettings, keyCallBroker, keyBrokered, keyGrants, keyAccess, keyBinaries, keyInheritFromParentJail,
	}
	// settingDeclKeys is the census for ONE `settings.<key>` declaration. It is
	// enforced by parseSettings and DELIBERATELY NOT descended into by
	// unknownKeyNotes: every other census in this file has two voices (strict says
	// "unknown key", tolerant says "ignoring unknown key"), and a settings
	// declaration has exactly one — refusal, in both decoders. See parseSettings for
	// why the version-boundary tolerance stops here.
	settingDeclKeys = []string{keyType, keyScope, keyDefault, keyDescription, keyDisclose, keyLegacyWorkspace}
	// `preamble` is a host_daemon key and DELIBERATELY not a top-level one: it
	// describes the connection yolo serves in FRONT of a daemon, so it says
	// nothing for a transport:"none" loophole and nothing for a manifest with no
	// host_daemon at all. A top-level spelling would be a key an author could
	// write in a place where it silently declares nothing.
	// `scope` is spelled with the SAME constant the settings declaration uses, and
	// the two vocabularies are different (`user`/`workspace` there, `jail`/`host`
	// here) because the objects are different. That is the existing convention in
	// this block, not an exception to it — `cmd`, `host` and `description` are all
	// already shared across objects — and the word means the same thing in both
	// places: what is this value shared across.
	hostDaemonKeys    = []string{keyCmd, keySettingsCheck, keyEnv, keyPublishes, keyRequestEnd, keyPreamble, keyScope, keyLaunchCheck, keyStartupReason, keyDetach, keyLaunchInput}
	jailDaemonKeys    = []string{keyCmd, keyRestart, keyCallerToken, keyListen, keyHostCmd}
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
	// A build's keys, one level below a binary's platforms. The binary names and the platform
	// keys are the author's own vocabulary, validated by parseBinaries, so only the build
	// object has a census.
	if bins, ok := getOrNil(data, keyBinaries).(*jsonx.OrderedMap); ok {
		for _, name := range bins.Keys() {
			builds, _ := getOrNil(bins, name).(*jsonx.OrderedMap)
			if builds == nil {
				continue
			}
			for _, platform := range builds.Keys() {
				if m, isMap := getOrNil(builds, platform).(*jsonx.OrderedMap); isMap {
					check(m, keyBinaries+"."+name+"."+platform+".", binaryBuildKeys)
				}
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
