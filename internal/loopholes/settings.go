package loopholes

import (
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// settings.go is the DELIVERY half of docs/design/pack-config-keys.md: the
// manifest declared the keys (internal/loopholedecl/settings.go), the user
// supplied values under `loopholes.<name>.settings`, internal/config validated
// them — and this is where the two become a file yolo owns.
//
// # Why a file yolo writes, rather than any channel that already existed
//
// There was no channel. The manifest spawns `--socket {socket}` with no config
// argument, the spawn sets no cwd, and nothing ever set YOLO_HOST_PROCESSES_CONFIG
// — so `host_processes.visible` was per-workspace only because the DAEMON opened the
// raw workspace file itself from an inherited cwd. That is a coincidence of process
// cwd, not a designed path, and it had a hole in it: the file was re-read on EVERY
// REQUEST, so an agent could rewrite yolo-jail.jsonc mid-session and widen its own
// allowlist with no relaunch and therefore no approval gate.
//
// Reading a file CORE writes closes that (OQ-K3): the value is resolved once, at
// launch, from the merged config, and changing it now needs a restart — which is
// exactly where the config-approval gate lives.
//
// The one channel that would have been easier is the one that is forbidden:
// `loopholes.<name>.env` is user-scope-only because it reaches a host daemon's spawn
// ENVIRONMENT. A path is different in kind, not in degree — the workspace supplies
// VALUES and yolo decides what the file says.
//
// # It is a flat map, and it stays one
//
// No includes, no layering, no comments, no provenance. Every declared key is
// present exactly once, in declaration order, already coerced to its declared type.
// A settings file that grew any of those would be a second config system, which is
// the risk pack-config-keys.md §6 names.

// SettingsFileName is the basename of the resolved settings file inside a
// loophole's state dir.
//
// An ALIAS rather than a second spelling: loopholedecl owns the literal because its
// schema refusal has to name the file (refuseSettingsFileCrossingIntoTheJail), and
// two packages spelling one filename is how the write side and the rule about it come
// to disagree.
const SettingsFileName = loopholedecl.SettingsFileName

// SettingsFileFor returns the path {settings} resolves to for a loophole: a file in
// its own per-loophole STATE dir.
//
// The state dir is name-keyed rather than staging-keyed (see StateDirFor), so the
// path survives a restage — which matters for the same reason it matters for a
// pack-shipped CA: an argv baked into a manifest must name a stable location, or
// every launch would hand the daemon a different path.
func SettingsFileFor(name string) string {
	return filepath.Join(StateDirFor(name), SettingsFileName)
}

// ResolveSettings folds one loophole's config-supplied values over its manifest
// declarations and returns the flat map to write.
//
// supplied is the `loopholes.<name>.settings` object from the MERGED config (nil
// when nothing supplied any). The result carries EVERY declared key — a supplied
// value where there is one, the declaration's `default` otherwise, and the type's
// zero where the declaration named no default. Totality is the contract a daemon
// reads against: it never has to tell "absent" from "false".
//
// problems is non-empty only for a value internal/config would already have refused
// (wrong type, or a key no declaration knows). It cannot normally happen on the
// launch path — ValidateConfig runs first and its errors are fatal — but it CAN
// in-jail, where the scope pass downgrades to warnings, and it must not become
// silence: a value dropped without a word is how a daemon comes to run on a
// configuration nobody wrote. The DECLARATION wins in every such case, which is the
// fail-closed direction: an unvalidatable value never reaches the file.
func ResolveSettings(lp *Loophole, supplied *jsonx.OrderedMap) (*jsonx.OrderedMap, []string) {
	out := jsonx.NewOrderedMap()
	var problems []string
	if lp == nil {
		return out, nil
	}
	for _, decl := range lp.Settings {
		value := decl.Default
		if supplied != nil {
			if v, ok := supplied.Get(decl.Key); ok && v != nil {
				coerced, typeErr := loopholedecl.CoerceSettingValue(decl.Type, v)
				if typeErr != "" {
					problems = append(problems, "loopholes."+lp.Name+".settings."+decl.Key+": "+
						typeErr+" — keeping the declared default")
				} else {
					value = coerced
				}
			}
		}
		out.Set(decl.Key, value)
	}
	// A supplied key no declaration knows never reaches the file (the loop above is
	// driven by the DECLARATIONS, not by what was supplied), but it is reported, or a
	// typo would be invisible at exactly the moment the user is wondering why nothing
	// changed. internal/config already errors on it host-side; this is the in-jail
	// warning path saying the same thing.
	if supplied != nil {
		for _, key := range supplied.Keys() {
			if _, declared := loopholedecl.SettingByKey(lp.Settings, key); !declared {
				problems = append(problems, "loopholes."+lp.Name+".settings."+key+
					": no such setting — "+pytext.Repr(lp.Name)+" declares "+
					settingKeyList(lp.Settings))
			}
		}
	}
	return out, problems
}

// WriteSettings resolves and writes one loophole's settings file, returning its
// path. A loophole declaring no settings writes NOTHING and returns "": there is no
// file to name, which is the same fact loopholedecl refuses a `{settings}` token
// over.
//
// # Written whole, and written 0600
//
// Whole because a daemon started concurrently with a partial write would read a
// truncated file; the write goes to a temp name in the same dir and renames over,
// so a reader sees the old bytes or the new ones. 0600 because the values are
// whatever a user's config put there — nothing in the schema says a setting is not
// a credential.
//
// THE MODE IS NOT WHAT KEEPS IT OUT OF THE JAIL, and reading it that way was a live
// defect until 2026-08-18. A jail's agent runs as UID 0, so a file the container can
// see at all it can read. What keeps this one host-side is a SCHEMA rule: the state
// dir crosses into a jail only for a loophole with a `jail_daemon`, and such a
// manifest may no longer leave `state_files` absent (the whole-dir mount) or name
// this file in it — loopholedecl.refuseSettingsFileCrossingIntoTheJail refuses both
// at load. The mode is the host-side, single-user half of the story only.
func WriteSettings(lp *Loophole, supplied *jsonx.OrderedMap) (string, []string, error) {
	if lp == nil || len(lp.Settings) == 0 {
		return "", nil, nil
	}
	values, problems := ResolveSettings(lp, supplied)
	payload, err := jsonx.DumpsCompact(values)
	if err != nil {
		return "", problems, err
	}
	path := SettingsFileFor(lp.Name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", problems, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), SettingsFileName+".*")
	if err != nil {
		return "", problems, err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", problems, err
	}
	if _, err := tmp.WriteString(payload + "\n"); err != nil {
		_ = tmp.Close()
		return "", problems, err
	}
	if err := tmp.Close(); err != nil {
		return "", problems, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return "", problems, err
	}
	return path, problems, nil
}

// settingKeyList renders the declared keys for an error message.
func settingKeyList(settings []Setting) string {
	keys := loopholedecl.SettingKeys(settings)
	if len(keys) == 0 {
		return "no settings at all"
	}
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += ", "
		}
		out += pytext.Repr(k)
	}
	return out
}
