package config

import (
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// The scope model for the `loopholes` block is RULED
// (docs/design/loophole-packaging.md §4.3b): INSTALL is user-scope only, ENABLE
// is either scope. Key by key:
//
//	command (inline)   install  user-only  — it IS the host execution
//	doctor_cmd         install  user-only  — a second host execution, run by two
//	                                         read-only-looking commands
//	env (either shape) install  user-only  — reaches a host daemon's spawn env
//	                                         (LD_PRELOAD into the broker, §4.1)
//	enabled            enable   either     — routes within the vetted set
//	jail_env           —        either     — container-side only
//
// The doctor_cmd row is about the INLINE shape, where the key is part of the
// install and the user config is a real destination for it. On a MANIFEST-backed
// loophole the same key is not a scope question: the manifest fixes doctor_cmd
// and applyWorkspaceOverrides honors only enabled/env/jail_env, so the key is
// refused at every scope by validateLoopholeOverride and the scope pass stays
// quiet about it. Telling that reader "move this key to the user config" was a
// dead end — the same key there answered "unknown key" — and it reported one
// mistake twice, which is the thing validateAgentsRetired's comment warns about.
//
// The violation is an ERROR on the host and a WARNING inside a jail — the same
// asymmetry as validateAgentsRetired, for the same reason: /workspace is
// live-mounted, so in-jail `yolo` and nested jails would break identically on a
// hard error, refusing every nested preflight over a file the in-jail user may
// still be migrating.
const loopholeScopeInJailSuffix = " (warning in-jail: an error here would refuse " +
	"every nested launch over the live-mounted workspace file — fix it host-side.)"

// loopholeUserConfigHint is the fix every scope violation names.
const loopholeUserConfigHint = "~/.config/yolo-jail/config.jsonc"

// validateLoopholes runs the `host_services = config.get("loopholes")` block
// of _validate_config, plus the §4.3b scope pass. Names matching a file-backed
// loophole are overrides (enabled/env/jail_env only); unknown override-shaped
// names warn; everything else is an inline service definition (command
// required).
//
// ValidateConfig only ever receives the MERGED map, and the merge carries no
// provenance — so, exactly like validateCacheRelocations, the scope pass
// re-reads the workspace config files. One extra file read on a cold path,
// much cheaper than threading provenance through every caller of the merge.
func validateLoopholes(config *jsonx.OrderedMap, workspace string, resolver LoopholeResolver, errs, warns *[]string) {
	v, present := config.Get("loopholes")
	if !present || v == nil {
		return
	}
	hostServices, ok := asMap(v)
	if !ok {
		add(errs, "config.loopholes: expected an object")
		return
	}

	// known_loopholes = _known_loopholes() if host_services else {}
	var known map[string]LoopholeInfo
	if hostServices.Len() > 0 && resolver != nil {
		if k, _ := resolver.Known(); k != nil {
			known = k
		}
	}
	// Every workspace key survives into the merged map, so an empty merged
	// block proves the workspace contributed none either — no re-read needed.
	var wsEntries map[string][]wsLoopholeEntry
	if hostServices.Len() > 0 {
		wsEntries = workspaceLoopholeEntries(workspace)
	}
	jail := inJail()

	for _, name := range hostServices.Keys() {
		specV, _ := hostServices.Get(name)
		var infoPtr *LoopholeInfo
		if info, isKnown := known[name]; isKnown {
			infoCopy := info
			infoPtr = &infoCopy
		}

		// --- Scope pass (§4.3b), on the workspace files' own contributions.
		// Name/type problems are the shape pass's to report; the scope pass
		// only runs where the entry is inspectable.
		suppressFallback := false
		scopeViolated := false
		spec, isMap := asMap(specV)
		if isMap && hostServiceName.MatchString(name) && name != paths.BuiltinCgroupLoopholeName {
			entries := wsEntries[name]
			scoped := func(msg string) {
				scopeViolated = true
				if jail {
					add(warns, msg+loopholeScopeInJailSuffix)
					return
				}
				add(errs, msg)
			}
			for _, e := range entries {
				if e.spec == nil {
					continue
				}
				for _, viol := range loopholeScopeKeyViolations(name, e.spec, e.file, infoPtr != nil) {
					scoped(viol)
				}
			}
			installed := infoPtr != nil || userInstalledInline(spec, entries)
			violation, disclosure := loopholeScopeEnableProblems(name, entries, installed)
			if violation != "" {
				scoped(violation)
				// The ruled fatal REPLACES the every-launch "treating the
				// entry as an override" warning (OQ-LP2).
				suppressFallback = true
			}
			if disclosure != "" {
				add(warns, disclosure)
			}
			// --- Placement pass (§4.3a): WHERE the declared host execution lives.
			// Scope decides who may declare it; this decides whether the file it
			// names is one an agent rewrites between launches.
			if !scopeViolated {
				for _, p := range loopholeEntryPlacementProblems(name, spec, infoPtr, workspace) {
					add(errs, p)
				}
			}
		}

		// --- Shape pass, over the merged entry (semantics unchanged).
		validateLoopholeEntryShape(name, specV, infoPtr, suppressFallback, errs, warns)
	}
}

// validateLoopholeEntryShape is the shape check for ONE merged
// `loopholes.<name>` entry: name rules, object rule, then the
// override/fallback/inline dispatch. info is non-nil when the name matches a
// file-backed loophole. suppressFallbackWarn drops the unknown-name "treating
// the entry as an override" warning (the ruled enable-uninstalled error
// replaces it).
func validateLoopholeEntryShape(name string, specV any, info *LoopholeInfo, suppressFallbackWarn bool, errs, warns *[]string) {
	path := "config.loopholes." + name
	// name is always a string key from a decoded JSON object, so only the
	// regex needs checking.
	if !hostServiceName.MatchString(name) {
		add(errs, "config.loopholes: service name "+pytext.Repr(name)+
			" must match ^[a-zA-Z][a-zA-Z0-9_-]{0,63}$")
		return
	}
	if name == paths.BuiltinCgroupLoopholeName {
		add(errs, path+": '"+paths.BuiltinCgroupLoopholeName+"' is reserved "+
			"for the built-in cgroup delegate service")
		return
	}
	spec, ok := asMap(specV)
	if !ok {
		add(errs, path+": expected an object")
		return
	}
	if info != nil {
		validateLoopholeOverride(name, spec, path, errs, info)
		return
	}
	// Override-shaped but no loophole discoverable from here:
	// spec (truthy) and "command" not in spec and set(spec) <= override keys
	if spec.Len() > 0 && !hasKey(spec, "command") && keysSubsetOf(spec, knownLoopholeOverrideKeys) {
		validateLoopholeOverride(name, spec, path, errs, nil)
		if !suppressFallbackWarn {
			add(warns, path+": no loophole named "+pytext.Repr(name)+" is installed on "+
				"this machine — treating the entry as an override of "+
				"a host-side loophole. If the loophole was removed, "+
				"this entry is a no-op; an inline service would need "+
				"a 'command'.")
		}
		return
	}
	validateInlineService(spec, path, errs)
}

// loopholeScopeKeyViolations applies the install-shaped-key rows of the §4.3b
// table to ONE workspace file's contribution to a `loopholes.<name>` entry.
// The returned messages are host-side errors (the caller downgrades in-jail).
//
// manifestBacked says the name resolves to a file-backed loophole, which changes
// what is true of doctor_cmd: see the doctor_cmd note at the top of this file.
func loopholeScopeKeyViolations(name string, spec *jsonx.OrderedMap, srcFile string, manifestBacked bool) []string {
	path := "config.loopholes." + name
	if hasKey(spec, "command") {
		// The whole entry is an install; one message covers it (its env and
		// doctor_cmd fall with the command).
		return []string{path + ": a loophole with a 'command' is INSTALLED here, and " +
			"installing is user-scope only — " + srcFile + " is agent-editable, so it " +
			"cannot declare host execution. Move this entry to " + loopholeUserConfigHint + "."}
	}
	var out []string
	if hasKey(spec, "env") {
		out = append(out, path+".env: user-scope only — 'env' reaches a host daemon's "+
			"spawn environment, and "+srcFile+" is agent-editable. Move this key to "+
			loopholeUserConfigHint+".")
	}
	// On a manifest-backed loophole this entry is an OVERRIDE, and doctor_cmd is
	// not overridable at any scope — validateLoopholeOverride refuses it with the
	// only advice that works (remove it). Adding a scope error here would send the
	// reader to a user config that answers "unknown key", and would report the one
	// mistake twice.
	if hasKey(spec, "doctor_cmd") && !manifestBacked {
		out = append(out, path+".doctor_cmd: user-scope only — 'doctor_cmd' is a host "+
			"command run by `yolo check` and `yolo loopholes status`, and "+srcFile+
			" is agent-editable. Move this key to "+loopholeUserConfigHint+".")
	}
	return out
}

// loopholeScopeEnableProblems handles the `enabled` column of §4.3b for one
// loophole name, across every workspace contribution (later files win, same
// as the merge):
//
//   - enabled:true naming a loophole that is NOT installed is the RULED fatal
//     (OQ-LP2): the error IS the human-in-the-loop moment, so it names the
//     loophole, the file that asked, and the user-config snippet that would
//     install it. The ruling explicitly allows upgrading this error into an
//     interactive, TTY-gated offer that appends the snippet to the user config
//     — that needs a comment-preserving JSONC writer, which does not exist, so
//     today the human pastes the snippet themselves.
//   - enabled:true on an INSTALLED loophole is legal and DISCLOSED, mirroring
//     the OFF line (docs/design/loophole-activation.md OQ-A13). R5 was written
//     when `enabled: true` was INERT — the manifest default was already true, so
//     the only meaningful thing a workspace could do was turn a loophole OFF, and
//     only that direction got a line. R2 flipped the manifest default and made
//     this key the ACTIVATION VERB, which left the newly dangerous direction as
//     the silent one.
//   - enabled:false on an INSTALLED loophole is legal but DISCLOSED: after the
//     ruling, scope no longer protects the OFF direction, so this line is the
//     only protection for a default-on loophole (the broker case, §4.3b
//     consequence 2).
//   - enabled:false naming an unknown loophole is a harmless no-op and stays
//     the caller's "treating the entry as an override" warning.
//
// Both disclosures are READABILITY rather than a control, and the ON wording is
// held to that: it names the file that holds the switch and stops. Nothing
// reachable from here establishes that a human read that file — the approval diff
// is a separate mechanism with its own integrity story (docs/design/config-safety.md),
// and a line implying review would be worth less than no line at all.
//
// Neither direction consults the loophole's own manifest default, so a workspace
// file restating a default is disclosed too. That is deliberate on both counts:
// LoopholeInfo carries no default (it is Name + HasHostDaemon), so this surface
// could NOT suppress the redundant case, and `yolo check` — which can see the
// default — must agree with this one or the two disclosures contradict each other
// over the same file. An explicit `enabled` in an agent-editable file is a
// deliberate act either way, which is what keeps the line off every launch.
func loopholeScopeEnableProblems(name string, entries []wsLoopholeEntry, installed bool) (violation, disclosure string) {
	var enabled *bool
	var file string
	for _, e := range entries {
		if e.spec == nil || hasKey(e.spec, "command") {
			// An install-shaped workspace entry already drew the key violation.
			continue
		}
		ev, ok := e.spec.Get("enabled")
		if !ok || !isBool(ev) {
			continue // a non-bool enabled is the shape pass's type error
		}
		b := ev.(bool)
		enabled = &b
		file = e.file
	}
	if enabled == nil {
		return "", ""
	}
	if *enabled && !installed {
		return "config.loopholes." + name + ": " + file + " enables a loophole that is " +
			"not installed on this machine. Installing is user-scope: add the entry to " +
			loopholeUserConfigHint + " yourself, e.g. " +
			`"loopholes": {"` + name + `": {"command": ["<host daemon argv>"]}}.`, ""
	}
	if !*enabled && installed {
		return "", "config.loopholes." + name + ": disabled by " + file +
			" (workspace scope) — the installed loophole " + pytext.Repr(name) +
			" will not run for jails launched from this workspace."
	}
	if *enabled && installed {
		return "", "config.loopholes." + name + ": enabled by " + file +
			" (workspace scope) — the installed loophole " + pytext.Repr(name) +
			" runs for jails launched from this workspace because that " +
			"agent-editable file switched it on."
	}
	return "", ""
}

// userInstalledInline reports whether the merged entry carries a `command`
// that no workspace file contributed — i.e. the USER config installs an
// inline service under this name, which counts as installed for the
// enable-uninstalled rule.
func userInstalledInline(mergedSpec *jsonx.OrderedMap, entries []wsLoopholeEntry) bool {
	if !hasKey(mergedSpec, "command") {
		return false
	}
	for _, e := range entries {
		if e.spec != nil && hasKey(e.spec, "command") {
			return false
		}
	}
	return true
}

// wsLoopholeEntry is one workspace config file's contribution to a
// `loopholes.<name>` key. spec is nil when the contribution is not an object
// (the shape pass reports the type error from the merged map).
type wsLoopholeEntry struct {
	file string
	spec *jsonx.OrderedMap
}

// workspaceLoopholeEntries re-reads the workspace-scope config files and maps
// loophole name → contributions in file order (yolo-jail.jsonc, then
// yolo-jail.local.jsonc — the merge order, so "later wins" holds). Includes
// fold into the file that pulled them in, which is also the file a human has
// to open to find the include; the seen set is shared exactly like
// LoadWorkspaceConfig's, so a tracked config that explicitly includes the
// local file does not double-report its entries. Load problems are ignored
// here: the same files were already loaded (and any parse error already
// reported) by whoever produced the merged config.
func workspaceLoopholeEntries(workspace string) map[string][]wsLoopholeEntry {
	if workspace == "" {
		workspace = cwd()
	}
	out := map[string][]wsLoopholeEntry{}
	seen := map[string]struct{}{}
	for _, fname := range []string{WorkspaceConfigName, WorkspaceLocalConfigName} {
		path := filepath.Join(workspace, fname)
		cfg, err := LoadJSONCWithIncludes(path, fname, false, func(string) {}, seen)
		if err != nil || cfg == nil {
			continue
		}
		blockV, ok := cfg.Get("loopholes")
		if !ok {
			continue
		}
		block, ok := asMap(blockV)
		if !ok {
			continue
		}
		for _, name := range block.Keys() {
			specV, _ := block.Get(name)
			spec, _ := asMap(specV)
			out[name] = append(out[name], wsLoopholeEntry{file: path, spec: spec})
		}
	}
	return out
}

// WorkspaceLoopholeOrigins maps loophole name → every workspace-scope config
// file that contributed to it, in merge order (yolo-jail.jsonc, then
// yolo-jail.local.jsonc, includes folded into whichever file pulled them in).
//
// It exists for callers that read the COLLAPSED workspace config — `yolo
// loopholes list`/`status` go through LoadWorkspaceConfig, which merges the two
// files and so cannot name an entry's origin. Refusing an entry while naming the
// wrong file sends a human to edit a file the entry is not in.
func WorkspaceLoopholeOrigins(workspace string) map[string][]string {
	out := map[string][]string{}
	for name, entries := range workspaceLoopholeEntries(workspace) {
		for _, e := range entries {
			out[name] = append(out[name], e.file)
		}
	}
	return out
}

// WorkspaceLoopholeSwitch is one loophole's effective workspace-scope `enabled`
// decision: what the winning workspace file said, and which file that was.
type WorkspaceLoopholeSwitch struct {
	// File is the workspace config file whose `enabled` won the merge — the LAST
	// one to carry the key, because later files win (yolo-jail.local.jsonc over
	// yolo-jail.jsonc). It is the file a human has to open, which is the only
	// thing a disclosure naming the wrong file would be good for.
	File string
	// Enabled is that file's value. BOTH directions are carried: OFF is the §4.3b
	// disclosure, ON is loophole-activation.md OQ-A13's mirror of it.
	Enabled bool
}

// WorkspaceLoopholeSwitches maps loophole name → the effective workspace-scope
// `enabled` decision for it. A name no workspace file mentions, and a name a
// workspace file mentions without setting `enabled`, are ABSENT from the map:
// "workspace scope said nothing" and "workspace scope said true" are different
// answers, and only absence can express the first.
//
// It is the provenance seam behind both §4.3b disclosures — the launch-time line
// (via validateLoopholes) and `yolo check`'s warning instead of a green pass — in
// both directions. This used to be WorkspaceDisabledLoopholes, which computed the
// same thing and threw the `true` case away, back when `enabled: true` from a
// workspace was inert: the manifest default was already on, so the only power the
// weak scope had was to turn things OFF. R2 flipped that default and made the key
// the ACTIVATION VERB while R5 kept it at workspace scope, so the direction with
// no disclosure became the dangerous one (docs/design/loophole-activation.md
// OQ-A13). Widening the existing seam rather than adding a second one is what
// keeps the two surfaces reading the same answer — and one vocabulary for a
// question that was always symmetric.
//
// Only WORKSPACE files are read, which is the whole point. A user-scope enable and
// a manifest's own `default_enabled` are decisions made where an agent cannot
// reach, and disclosing those would put a line under every loophole on every
// launch — which is how the one that matters gets skimmed past.
func WorkspaceLoopholeSwitches(workspace string) map[string]WorkspaceLoopholeSwitch {
	out := map[string]WorkspaceLoopholeSwitch{}
	for name, entries := range workspaceLoopholeEntries(workspace) {
		var enabled *bool
		var file string
		for _, e := range entries {
			if e.spec == nil {
				continue
			}
			if ev, ok := e.spec.Get("enabled"); ok && isBool(ev) {
				b := ev.(bool)
				enabled = &b
				file = e.file
			}
		}
		if enabled != nil {
			out[name] = WorkspaceLoopholeSwitch{File: file, Enabled: *enabled}
		}
	}
	return out
}

// LoopholeEntryErrors validates a single `loopholes.<name>` entry the way
// ValidateConfig does and returns only the messages that make it INVALID. It
// is the seam for commands that read the config WITHOUT the full schema pass
// (`yolo loopholes list`/`status`): an entry this returns messages for must be
// refused, not honored, because Status executes doctor_cmd from what it reads
// (loophole-packaging.md §4.1 finding 2).
//
// info is the file-backed loophole the name resolves to (nil when none);
// userInstalledInline reports whether the USER config installs an inline
// service under this name (either counts as "installed" for the §4.3b
// enable-uninstalled rule). fromWorkspace applies the workspace-scope rules;
// inJail downgrades them to the warnings ValidateConfig would emit, which this
// function does NOT return — matching the launch path, which honors such an
// entry in-jail and refuses it on the host. srcFile names the entry's origin
// in the scope messages. workspace is the workspace these commands are reading
// from, for the §4.3a placement rule — which applies at EITHER scope, because it
// is about the target file rather than the declaring one.
func LoopholeEntryErrors(name string, specV any, info *LoopholeInfo, userInstalledInline, fromWorkspace, inJail bool, srcFile, workspace string) []string {
	errs := &[]string{}
	warns := &[]string{}
	validateLoopholeEntryShape(name, specV, info, true, errs, warns)
	spec, isMap := asMap(specV)
	named := isMap && hostServiceName.MatchString(name) && name != paths.BuiltinCgroupLoopholeName
	scopeRefused := false
	if fromWorkspace && !inJail && named {
		beforeScope := len(*errs)
		*errs = append(*errs, loopholeScopeKeyViolations(name, spec, srcFile, info != nil)...)
		installed := info != nil || userInstalledInline
		entry := []wsLoopholeEntry{{file: srcFile, spec: spec}}
		if violation, _ := loopholeScopeEnableProblems(name, entry, installed); violation != "" {
			*errs = append(*errs, violation)
		}
		// One mistake, one message: a scope refusal already rejects the whole
		// entry and names a fix, so the placement rule stays quiet.
		scopeRefused = len(*errs) > beforeScope
	}
	if named && !scopeRefused {
		*errs = append(*errs, loopholeEntryPlacementProblems(name, spec, info, workspace)...)
	}
	return *errs
}

// validateLoopholeOverride validates a single loophole override. info is nil
// when the target is not resolvable on this machine (manifest-dependent checks
// skip).
func validateLoopholeOverride(name string, spec *jsonx.OrderedMap, path string, errs *[]string, info *LoopholeInfo) {
	if hasKey(spec, "command") {
		add(errs, path+".command: not overridable — "+pytext.Repr(name)+" is an existing "+
			"loophole whose command is fixed by its manifest; only "+
			"'enabled', 'env', and 'jail_env' may be overridden")
	}
	if hasKey(spec, "doctor_cmd") {
		// Not a scope rule: nothing reads doctor_cmd off an override
		// (applyWorkspaceOverrides honors enabled/env/jail_env only), so the key
		// is inert wherever it is written. The generic "unknown key" left the
		// reader with the wrong theory — that it belonged somewhere else.
		add(errs, path+".doctor_cmd: not overridable — "+pytext.Repr(name)+" is an existing "+
			"loophole whose doctor_cmd is fixed by its manifest; only "+
			"'enabled', 'env', and 'jail_env' may be overridden, so remove this key")
	}
	// "command" and "doctor_cmd" are allowed through here (each gets its own
	// dedicated error above).
	allowed := set("enabled", "env", "jail_env", "command", "doctor_cmd")
	reportUnknownKeys(spec, allowed, path, errs)
	if enabledV, ok := spec.Get("enabled"); ok && !isBool(enabledV) {
		add(errs, path+".enabled: expected a boolean (got "+pyReprValue(enabledV)+")")
	}
	if _, ok := spec.Get("env"); ok && info != nil && !info.HasHostDaemon {
		add(errs, path+".env: not applicable — "+pytext.Repr(name)+" has no host daemon, so "+
			"'env' would be silently ignored; use 'jail_env' to set "+
			"variables inside the jail")
	}
	for _, envKey := range []string{"env", "jail_env"} {
		envV, present := spec.Get(envKey)
		if !present || envV == nil {
			continue
		}
		env, ok := asMap(envV)
		if !ok {
			add(errs, path+"."+envKey+": expected an object")
			continue
		}
		for _, k := range env.Keys() {
			val, _ := env.Get(k)
			// k is always a string; only value type can fail.
			if !isStr(val) {
				add(errs, path+"."+envKey+": keys and values must be strings")
				break
			}
		}
	}
}

// validateInlineService runs the inline-service tail of the loopholes block.
func validateInlineService(spec *jsonx.OrderedMap, path string, errs *[]string) {
	reportUnknownKeys(spec, knownHostServiceKeys, path, errs)
	cmdV, present := spec.Get("command")
	if !present || cmdV == nil {
		add(errs, path+".command: required")
	} else if cmd, ok := asList(cmdV); !ok || len(cmd) == 0 {
		add(errs, path+".command: expected a non-empty list of strings")
	} else {
		for ci, ca := range cmd {
			if !isStr(ca) {
				add(errs, path+".command["+itoa(ci)+"]: expected a string, got "+typeName(ca))
			}
		}
	}
	envV, present := spec.Get("env")
	if present && envV != nil {
		env, ok := asMap(envV)
		if !ok {
			add(errs, path+".env: expected an object")
		} else {
			for _, k := range env.Keys() {
				val, _ := env.Get(k)
				if !isStr(val) {
					add(errs, path+".env: keys and values must be strings")
					break
				}
			}
		}
	}
	if dcV, present := spec.Get("doctor_cmd"); present && dcV != nil {
		if dc, ok := asList(dcV); !ok || len(dc) == 0 {
			add(errs, path+".doctor_cmd: expected a non-empty list of strings")
		} else {
			for ci, ca := range dc {
				if !isStr(ca) {
					add(errs, path+".doctor_cmd["+itoa(ci)+"]: expected a string, got "+typeName(ca))
				}
			}
		}
	}
	if dV, present := spec.Get("description"); present && dV != nil && !isStr(dV) {
		add(errs, path+".description: expected a string")
	}
	// Type-checked rather than left to the loader's truthiness, because for THIS
	// key truthiness gets the answer backwards in the one way a human is likely
	// to write it: `"preamble": "false"` is a non-empty string, hence true, hence
	// the preamble the author was trying to turn OFF.
	if pV, present := spec.Get("preamble"); present && pV != nil && !isBool(pV) {
		add(errs, path+".preamble: expected a boolean (got "+pyReprValue(pV)+")")
	}
	// `enabled` gets the identical check, and the identical wording as the OVERRIDE
	// path's (validateLoopholeOverride). One key, read by two loaders, must not be
	// strict in one entry shape and truthy in the other — `"enabled": "false"` is
	// the same slip in both places and it fails in the granting direction in both.
	if eV, present := spec.Get("enabled"); present && eV != nil && !isBool(eV) {
		add(errs, path+".enabled: expected a boolean (got "+pyReprValue(eV)+")")
	}
	// jail_endpoint is the canonical override; jail_socket stays an ACCEPTED ALIAS.
	// Retiring the older key rather than aliasing it would make a third-party
	// loophole's override silently vanish over a rename, which is the same class of
	// failure as a manifest disappearing because one enum value changed.
	for _, key := range []string{"jail_endpoint", "jail_socket"} {
		jsV, present := spec.Get(key)
		if !present || jsV == nil {
			continue
		}
		if js, ok := asStr(jsV); !ok {
			add(errs, path+"."+key+": expected a string")
		} else if !hasPrefix(js, paths.JailHostServicesDir+"/") {
			add(errs, path+"."+key+": must start with "+
				paths.JailHostServicesDir+"/ "+
				"(got "+pytext.Repr(js)+")")
		}
	}
}
