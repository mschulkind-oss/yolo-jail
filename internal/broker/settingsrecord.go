// What a host-wide daemon was STARTED WITH, recorded at spawn, so a later launch can tell a
// daemon serving settings its config no longer says from one that is current
// (docs/design/host-daemon-ownership.md HD-D2).
//
// # The defect this closes
//
// A singleton reads its settings file ONCE, at startup (the `{settings}` token,
// internal/loopholes/settings.go). Every launch rewrites that file from the merged config
// and then ENSURES the daemon, and the ensure was a pure liveness question: a daemon that
// was alive was reused. So a corrected `loopholes.aws-auth.settings.profile` reached the
// file and never the process, and every mint kept failing on the old profile until
// someone ran `yolo host-daemon restart aws-auth` (measured 2026-09-28).
//
// # Key names, never values
//
// The record holds a SALTED DIGEST per key, not the value, because a setting can be a
// credential (nothing in the schema says it is not). A digest is enough to answer the only
// question asked of it, "which keys differ", and a per-spawn random salt keeps a
// low-entropy value (a profile name) from being matched against a precomputed table. The
// file is 0600 besides, beside the PID file it shares a lifecycle with.
package broker

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// settingsRecord is the on-disk form: the spawn's salt and one digest per settings key.
type settingsRecord struct {
	Salt string            `json:"salt"`
	Keys map[string]string `json:"keys"`
}

// settingsRecordPath is the record, a sibling of the PID file so the lifecycle that owns
// that path creates, finds and removes it (BrokerKill), exactly like the capability stamp.
func settingsRecordPath(deps Deps) string { return deps.PIDFilePath + ".settings" }

// SettingsDrift is how a running daemon's recorded settings differ from a wanted set.
type SettingsDrift struct {
	// Unrecorded: the daemon has no record, so it was started by a yolo that predates
	// this file (or its record could not be written). Nothing says what it is serving.
	Unrecorded bool
	// Changed lists the KEYS whose value differs, was added or was removed, sorted.
	// Names only, by design (see the package comment above).
	Changed []string
}

// Stale reports whether the daemon cannot be shown to run the wanted settings.
func (d SettingsDrift) Stale() bool { return d.Unrecorded || len(d.Changed) > 0 }

// settingsPathIn returns the settings file a singleton's argv hands it, or "" when the argv
// names none. DERIVED from the loophole name (loopholes.SettingsFileFor), the one place the
// `{settings}` token resolves, and gated on the argv actually naming it: a daemon that is
// never handed the file cannot be running a stale copy of it.
func settingsPathIn(name string, argv []string) string {
	if name == "" {
		return ""
	}
	p := loopholes.SettingsFileFor(name)
	for _, a := range argv {
		if strings.Contains(a, p) {
			return p
		}
	}
	return ""
}

// parseFlatSettings decodes a settings payload (the flat JSON object the settings file
// holds) into one canonical, compacted JSON value per key.
func parseFlatSettings(raw []byte) (map[string][]byte, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, errors.New("settings payload is not a JSON object")
	}
	out := make(map[string][]byte, len(obj))
	for k, v := range obj {
		var buf bytes.Buffer
		if err := json.Compact(&buf, v); err != nil {
			return nil, err
		}
		out[k] = buf.Bytes()
	}
	return out, nil
}

// settingDigest is the salted digest one key's value is recorded as.
func settingDigest(salt, key string, value []byte) string {
	h := sha256.New()
	h.Write([]byte(salt))
	h.Write([]byte{0})
	h.Write([]byte(key))
	h.Write([]byte{0})
	h.Write(value)
	return hex.EncodeToString(h.Sum(nil))
}

// readSpawnSettings reads the settings file a spawn is about to hand its daemon. nil means
// there is nothing to record: the argv names no file, or it cannot be read or parsed.
func readSpawnSettings(deps Deps) map[string][]byte {
	if deps.SettingsPath == "" {
		return nil
	}
	raw, err := os.ReadFile(deps.SettingsPath)
	if err != nil {
		return nil
	}
	values, err := parseFlatSettings(raw)
	if err != nil {
		return nil
	}
	return values
}

// writeSettingsRecord records the settings a just-spawned daemon was handed. With nothing to
// record it REMOVES any previous record instead, so a record never describes a different
// process than the one the PID file names.
//
// Failures are discarded, and the consequence is bounded and loud: a missing record reads
// as Unrecorded, which the next ensure answers by restarting the daemon and saying why.
func writeSettingsRecord(deps Deps, values map[string][]byte) {
	path := settingsRecordPath(deps)
	removeIgnoreMissing(path)
	if values == nil {
		return
	}
	saltBytes := make([]byte, 16)
	if _, err := rand.Read(saltBytes); err != nil {
		return
	}
	rec := settingsRecord{Salt: hex.EncodeToString(saltBytes), Keys: map[string]string{}}
	for k, v := range values {
		rec.Keys[k] = settingDigest(rec.Salt, k, v)
	}
	payload, err := json.Marshal(rec)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, werr := f.Write(append(payload, '\n'))
	cerr := f.Close()
	if werr != nil || cerr != nil {
		removeIgnoreMissing(path)
	}
}

// CompareSettings reports how the settings the RUNNING daemon was started with differ from
// want, a flat JSON object in the settings file's format. ok is false when want is not one,
// so there is nothing to judge.
//
// It asks nothing about liveness: a caller that cares whether the daemon is running asks
// BrokerIsAlive first, because a stopped daemon's leftover record describes nothing.
func CompareSettings(deps Deps, want []byte) (drift SettingsDrift, ok bool) {
	values, err := parseFlatSettings(want)
	if err != nil {
		return SettingsDrift{}, false
	}
	return compareRecorded(deps, values), true
}

// compareRecorded compares parsed wanted values against the record.
func compareRecorded(deps Deps, want map[string][]byte) SettingsDrift {
	raw, err := os.ReadFile(settingsRecordPath(deps))
	if err != nil {
		return SettingsDrift{Unrecorded: true}
	}
	var rec settingsRecord
	if json.Unmarshal(raw, &rec) != nil || rec.Salt == "" || rec.Keys == nil {
		return SettingsDrift{Unrecorded: true}
	}
	var changed []string
	for k, v := range want {
		if got, present := rec.Keys[k]; !present || got != settingDigest(rec.Salt, k, v) {
			changed = append(changed, k)
		}
	}
	for k := range rec.Keys {
		if _, present := want[k]; !present {
			changed = append(changed, k)
		}
	}
	sort.Strings(changed)
	return SettingsDrift{Changed: changed}
}

// ConfiguredSettingsDrift compares a host-wide loophole's RUNNING daemon against the settings
// file a launch would write for supplied (the `loopholes.<name>.settings` object of the
// merged config), resolved through loopholes.SettingsPayload — the launch's own function.
// It is the read-only question `yolo check` and an attach ask; neither restarts anything.
//
// applicable is false when there is no running daemon to judge: lp is not host-scoped,
// declares no settings its argv is handed, is not running, or its settings cannot be
// rendered. A stopped daemon's leftover record describes nothing, so liveness comes first.
func ConfiguredSettingsDrift(lp *loopholes.Loophole, supplied *jsonx.OrderedMap) (drift SettingsDrift, applicable bool) {
	if lp == nil || lp.HostDaemon == nil || lp.HostDaemon.Scope != loopholes.ScopeHost ||
		len(lp.Settings) == 0 {
		return SettingsDrift{}, false
	}
	deps := SingletonDeps(lp.Name, SingletonArgv(lp.Name, lp.HostDaemon.Cmd))
	if deps.SettingsPath == "" || !BrokerIsAlive(deps) {
		return SettingsDrift{}, false
	}
	want, _, err := loopholes.SettingsPayload(lp, supplied)
	if err != nil {
		return SettingsDrift{}, false
	}
	return CompareSettings(deps, []byte(want))
}

// RunningSettingsDrift compares the running daemon's record against the settings file its
// argv names — what a respawn NOW would hand it, and therefore the right comparison for an
// ensure. ok is false when there is nothing to judge: the daemon is handed no settings, or
// the file cannot be read (a launch that failed to write it has already said so).
func RunningSettingsDrift(deps Deps) (drift SettingsDrift, ok bool) {
	values := readSpawnSettings(deps)
	if values == nil {
		return SettingsDrift{}, false
	}
	return compareRecorded(deps, values), true
}

// reportSettingsRestart is the ONE LINE a settings-driven restart prints: which daemon, why,
// and the keys — never a value. It is a disclosure of an act that reaches every jail sharing
// the daemon, so it says that too.
func reportSettingsRestart(deps Deps, drift SettingsDrift) {
	if deps.Out == nil {
		return
	}
	reason := "its settings changed since it started (" + strings.Join(drift.Changed, ", ") + ")"
	if drift.Unrecorded {
		reason = "it predates yolo recording the settings a daemon starts with, so it may be " +
			"serving settings the config no longer says"
	}
	richtext.Printer{W: deps.Out, Color: deps.Color}.Print(
		"[yellow]Restarting " + singletonSubject(deps) + ": " + reason + ". It is shared: " +
			"every jail using it gets the new settings from its next request.[/yellow]")
}
