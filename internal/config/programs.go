package config

// programs.go is the `programs` key. Two options, both USER SCOPE ONLY.
//
//	"programs": {
//	  "autoprune": true,
//	  "overrides": {
//	    "board": { "path": "~/code/board" },
//	    "chart": { "prebuilt": true }
//	  }
//	}
//
// `autoprune` deletes files. `overrides` (docs/design/pack-buildable-programs.md §5, BG-D19)
// points a program a pack builds from its own tree at a host checkout the user is developing,
// which every fresh launch builds in place of the pack's tree, so the bytes that run are the
// user's own: choosing a program's bytes is the same grant as deleting one, so it is user scope
// too. ProgramOverrides is its one reader of dev overrides.
//
// An entry may instead be `{"prebuilt": true}` (BG-D18): the user's explicit opt-in to the
// pack's verified prebuilt artifact for a pack-tree program even when the pack ships its source,
// which yolo never does on its own (OQ-BG2, ruled 2026-10-23). It names no path, so it is no dev
// override, and ProgramPrebuiltOptIns is its one reader.
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
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// programsKey is the top-level key, autopruneKey and overridesKey its two options. The
// override's own fields are pathKey and prebuiltKey. retiredRunKey is the field an override
// had while it ran a checkout in place (before OQ-BG3's ruling), refused with what replaced it.
const (
	programsKey   = "programs"
	autopruneKey  = "autoprune"
	overridesKey  = "overrides"
	pathKey       = "path"
	prebuiltKey   = "prebuilt"
	retiredRunKey = "run"
)

// knownProgramsKeys is the key census for the `programs` object.
var knownProgramsKeys = set(autopruneKey, overridesKey)

// knownProgramOverrideKeys is the census for one `programs.overrides.<bin>` entry.
// retiredRunKey is in the census so it is reported once, with what replaced it, never also as an
// unknown key.
var knownProgramOverrideKeys = set(pathKey, prebuiltKey, retiredRunKey)

// ProgramOverride is one `programs.overrides.<bin>` entry as the user's config states it,
// after validation. Path is ~-expanded and cleaned; it must be absolute. The checkout is built
// with the pack's own `build` and run by its own `run` (BG-D19), so the entry names no file.
//
// The directory's existence and its symlink resolution are NOT checked here: they are
// checked at each fresh launch, on the host, where the bytes are read (BG-D9).
//
// Prebuilt is the `{"prebuilt": true}` form (BG-D18): Path is then empty, since the two forms
// are mutually exclusive.
type ProgramOverride struct {
	Path     string
	Prebuilt bool
}

// ProgramOverrides returns the user's dev overrides, keyed by program bin. It is the one
// reader of programs.overrides, and it reads the USER file directly, never the merged or
// workspace config: a repo-committed file cannot choose a program's bytes.
//
// FAILS CLOSED. A user file this cannot read, or a `programs` value that is not an object,
// yields an empty map, so no override applies. An entry that does not validate is dropped
// alone, with a warning naming it: its valid siblings still apply. ValidateConfig reports
// the same entry as an error, so `yolo check` is where the user learns why.
func ProgramOverrides(warn Warn) map[string]ProgramOverride {
	out := map[string]ProgramOverride{}
	for bin, o := range programOverrideEntries(warn) {
		if !o.Prebuilt {
			out[bin] = o
		}
	}
	return out
}

// ProgramPrebuiltOptIns returns the bins whose `programs.overrides.<bin>` entry is
// `{"prebuilt": true}`: the user's explicit opt-in to the pack's verified prebuilt artifact for
// that pack-tree program, even when the pack ships its source (BG-D18, OQ-BG2). It reads the USER
// file directly and fails closed, as ProgramOverrides does: an unreadable file opts nothing in, so
// the program is built from its source as it would be with no setting.
func ProgramPrebuiltOptIns(warn Warn) map[string]bool {
	out := map[string]bool{}
	for bin, o := range programOverrideEntries(warn) {
		if o.Prebuilt {
			out[bin] = true
		}
	}
	return out
}

// programOverrideEntries is every valid `programs.overrides` entry of the user file, of either
// form, keyed by bin.
func programOverrideEntries(warn Warn) map[string]ProgramOverride {
	out := map[string]ProgramOverride{}
	if warn == nil {
		warn = func(string) {}
	}
	userPath := paths.UserConfigPath()
	cfg, err := loadUserScopeConfig(userPath, userPath, false, warn)
	if err != nil || cfg == nil {
		return out
	}
	v, present := cfg.Get(programsKey)
	if !present || v == nil {
		return out
	}
	programs, ok := asMap(v)
	if !ok {
		return out
	}
	ov, present := programs.Get(overridesKey)
	if !present || ov == nil {
		return out
	}
	entries, ok := asMap(ov)
	if !ok {
		return out
	}
	for _, bin := range entries.Keys() {
		raw, _ := entries.Get(bin)
		var problems []string
		o, ok := parseProgramOverride(bin, raw, &problems)
		if !ok || len(problems) > 0 {
			for _, p := range problems {
				warn(p + " — this override is ignored")
			}
			continue
		}
		out[bin] = o
	}
	return out
}

// parseProgramOverride validates one entry and reports every problem as "<field>: <what to
// do>", the field spelled as the config path a user would edit. It returns ok only when
// nothing was reported.
func parseProgramOverride(bin string, v any, errs *[]string) (ProgramOverride, bool) {
	field := programsKey + "." + overridesKey + "." + bin
	before := len(*errs)
	if bin == "" || bin == "." || bin == ".." || strings.ContainsAny(bin, "/\\") {
		add(errs, "config."+programsKey+"."+overridesKey+"."+bin+": program name must be a "+
			"single file name, the bin a pack declares (no slash)")
		return ProgramOverride{}, false
	}
	m, ok := asMap(v)
	if !ok {
		add(errs, "config."+field+": expected an object with a \"path\" to the program's directory, "+
			"or \"prebuilt\": true")
		return ProgramOverride{}, false
	}
	if _, has := m.Get(retiredRunKey); has {
		// THE RETIRED FIELD, named with what replaced it rather than as an unknown key: a checkout
		// is built with the pack's own `build` and run by its own `run` (OQ-BG3, BG-D19).
		add(errs, "config."+field+"."+retiredRunKey+": no longer read — yolo now builds the checkout at "+
			"\"path\" with the pack's own build line and runs the pack's own file; remove \"run\"")
	}
	reportUnknownKeys(m, knownProgramOverrideKeys, "config."+field, errs)

	var o ProgramOverride
	if bv, present := m.Get(prebuiltKey); present && bv != nil {
		b, isBool := bv.(bool)
		_, hasPath := m.Get(pathKey)
		switch {
		case !isBool:
			add(errs, "config."+field+"."+prebuiltKey+": expected true or false")
			return o, false
		case b && hasPath:
			add(errs, "config."+field+": \""+prebuiltKey+"\" and \""+pathKey+"\" cannot both be set — "+
				"\"prebuilt\": true runs the pack's verified prebuilt artifact, and \"path\" builds your own "+
				"checkout; keep the one you mean")
			return o, false
		case b:
			o.Prebuilt = true
			return o, len(*errs) == before
		}
	}
	pv, present := m.Get(pathKey)
	switch {
	case !present || pv == nil:
		add(errs, "config."+field+"."+pathKey+": required — your checkout of the program, which yolo "+
			"builds in place of its pack's tree, an absolute path or one starting with ~/ (or \"prebuilt\": "+
			"true to run the pack's prebuilt artifact)")
	default:
		p, isStr := pv.(string)
		switch {
		case !isStr:
			add(errs, "config."+field+"."+pathKey+": expected a string")
		case p == "":
			add(errs, "config."+field+"."+pathKey+": empty — give your checkout of the program")
		case p == "~" || strings.HasPrefix(p, "~/"):
			o.Path = filepath.Clean(filepath.Join(paths.Home(), p[1:]))
		case strings.HasPrefix(p, "~"):
			add(errs, "config."+field+"."+pathKey+": "+p+" names another user's home; write the "+
				"absolute path, or ~/ for your own home")
		case !filepath.IsAbs(p):
			add(errs, "config."+field+"."+pathKey+": "+p+" is not absolute; write the absolute "+
				"path, or one starting with ~/")
		default:
			o.Path = filepath.Clean(p)
		}
	}

	return o, len(*errs) == before
}

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
func validatePrograms(config *jsonx.OrderedMap, workspace string, errs *[]string, edit ...*editValidationContext) {
	v, present := config.Get(programsKey)
	if !present {
		// Every workspace key survives into the merged map, so an absent key here proves
		// the workspace config has none either — no re-read needed
		// (validateCacheRelocations' rule).
		return
	}
	if wsCfg, err := loadValidationWorkspaceConfig(workspace, edit...); err == nil && wsCfg != nil {
		if _, atWorkspace := wsCfg.Get(programsKey); atWorkspace && WorkspaceUserScopeOnlyKey(programsKey) {
			add(errs, "config."+programsKey+": user-scope only — move it to "+
				paths.UserConfigDisplayPath()+". A workspace config travels with the "+
				"repo and is agent-editable, so it cannot authorise deleting programs "+
				"out of the jail's home, nor choose which bytes a program runs "+
				"(programs.overrides).")
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
	if ov, present := programs.Get(overridesKey); present && ov != nil {
		entries, ok := asMap(ov)
		if !ok {
			add(errs, "config."+programsKey+"."+overridesKey+": expected an object keyed by program bin")
			return
		}
		for _, bin := range entries.Keys() {
			raw, _ := entries.Get(bin)
			parseProgramOverride(bin, raw, errs)
		}
	}
}
