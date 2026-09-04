package config

// programs.go is the `programs` key: today it carries exactly one option, and that option
// deletes files.
//
//	"programs": { "autoprune": true }
//
// It is OQ-PD4's third clause (docs/design/program-delivery.md §10 step four): "removal
// happens only on an explicit act; autoprune exists as an option, DEFAULT OFF". Turning it on
// makes every launch remove what the boot catalog names — the explicit act
// (`yolo programs remove --apply`), performed by the boot, with nobody present.
//
// USER SCOPE ONLY, for validateCacheRelocations' reason applied to a sharper case: a
// workspace config travels with the repo and is agent-editable, and this key authorises
// DELETING BINARIES out of the per-workspace home — including anything the human put in
// ~/.local/bin by hand, which yolo cannot distinguish from a dropped pack's leftovers. A repo
// that could turn it on would be a repo that could clear its collaborators' tools on the next
// launch. So the loader reads the user file DIRECTLY (workspace scope is inexpressible, the
// same construction LoadPacks uses) and validatePrograms reports a workspace-scoped key as an
// error rather than as a silent no-op.
//
// FALSE ON EVERY FAILURE. A malformed user config, an unreadable one, a value that is not a
// boolean: all of them answer OFF. That is the opposite of LoadPacks' rule (a malformed
// config there is an ERROR, because dropping a pack silently looks like the feature not
// working) and the difference is which way the failure points — an unreadable config that
// prunes nothing costs a user nothing, and one that prunes on a guess costs them a binary.

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// programsKey is the top-level key, and autopruneKey its one option.
const (
	programsKey  = "programs"
	autopruneKey = "autoprune"
)

// knownProgramsKeys is the key census for the `programs` object.
var knownProgramsKeys = set(autopruneKey)

// ProgramsAutoprune reports whether the USER's config turns the boot's orphan autoprune on.
//
// The host launcher is its only caller: it converts the answer into the one env var the
// entrypoint reads (YOLO_PROGRAMS_AUTOPRUNE), and emits nothing when the answer is false, so
// an absent variable and an off knob are the same thing in the jail — which they are.
func ProgramsAutoprune(warn Warn) bool {
	if warn == nil {
		warn = func(string) {}
	}
	userPath := paths.UserConfigPath()
	cfg, err := loadUserScopeConfig(userPath, userPath, false, warn)
	if err != nil || cfg == nil {
		return false
	}
	v, present := cfg.Get(programsKey)
	if !present || v == nil {
		return false
	}
	programs, ok := asMap(v)
	if !ok {
		return false
	}
	on, present := programs.Get(autopruneKey)
	if !present {
		return false
	}
	b, ok := on.(bool)
	return ok && b
}

// validatePrograms type-checks the key and enforces its scope. Both halves are errors: a
// misspelled option that validated would be a knob the user believes they set, and a
// workspace-scoped one that validated would be a destructive setting silently ignored — the
// two failure modes are opposite and both are worse than a message.
func validatePrograms(config *jsonx.OrderedMap, workspace string, errs *[]string) {
	v, present := config.Get(programsKey)
	if !present {
		// Every workspace key survives into the merged map, so an absent key here proves
		// the workspace config has none either — no re-read needed
		// (validateCacheRelocations' rule).
		return
	}
	if wsCfg, err := LoadWorkspaceConfig(workspace, false, func(string) {}); err == nil && wsCfg != nil {
		if _, atWorkspace := wsCfg.Get(programsKey); atWorkspace {
			add(errs, "config."+programsKey+": user-scope only — move it to "+
				"~/.config/yolo-jail/config.jsonc. A workspace config travels with the "+
				"repo and is agent-editable, so it cannot authorise deleting programs "+
				"out of the jail's home.")
		}
	}
	if v == nil {
		return
	}
	programs, ok := asMap(v)
	if !ok {
		add(errs, "config."+programsKey+": expected an object")
		return
	}
	reportUnknownKeys(programs, knownProgramsKeys, "config."+programsKey, errs)
	if on, present := programs.Get(autopruneKey); present && on != nil {
		if _, ok := on.(bool); !ok {
			add(errs, "config."+programsKey+"."+autopruneKey+": expected true or false")
		}
	}
}
