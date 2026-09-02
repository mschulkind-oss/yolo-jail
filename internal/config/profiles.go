package config

// profiles.go implements the `profiles` config key: the USER-declared half of a profile
// (provider-catalog-and-selection.md §5.2 — a profile is a named selection over a
// provider, user intent over a surface the provider defines).
//
// SCOPE, and it is the same security model `packs` runs (packs.go's header carries the
// full argument, OQ-CS5 ruled it for this key too): USER-SCOPE ONLY, read from
// paths.UserConfigPath() DIRECTLY rather than from the merged config, so workspace scope
// is inexpressible by construction. The reason is one sentence long: a workspace config
// travels with the repo and is agent-editable, and a profile steers which ENDPOINT and
// which MODEL an agent talks to — the steering a committed, agent-writable file may not
// do. This file is where the ruling lands, and validateProfiles is what makes a
// workspace spelling a hard error rather than a silently inert key.
//
// This file LOWERS only. It decides that an entry is well-formed — an object naming a
// provider, with string option values — and nothing else: what an option MEANS is the
// derive's business and the provider owns which options exist (OQ-CS7), so the lowering
// never validates a value against anything. The pack-side resolution (defaults under
// these values, the census, the undeclared-name refusal) is packload.ResolveProfiles.

import (
	"fmt"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// profilesKey is the top-level config key.
const profilesKey = "profiles"

// LoadProfiles returns the user's `profiles` entries, lowered.
//
// It takes no merged config, for the same reason LoadPacks does not: reading the user
// file directly is what makes workspace scope inexpressible, and a caller that handed it
// the merged map would undo that guarantee one call site at a time.
//
// A malformed entry is a WARNING plus a skip, never a silently dropped key and never a
// hard error here: the hard error is validateProfiles' job, at `yolo check` and at every
// launch (loadAndValidateConfig), where it can be fatal. This reader is the fallback for
// the paths that reach a profile without having validated — and a warning is the loudest
// thing it can say without inventing a second, weaker refusal channel.
func LoadProfiles(warn Warn) (map[string]packload.UserProfile, error) {
	if warn == nil {
		warn = func(string) {}
	}
	userPath := paths.UserConfigPath()
	// loadUserScopeConfig, not LoadJSONCWithIncludes — the same direct read LoadPacks
	// makes, so workspace scope stays inexpressible AND the --user-layer reaches these
	// entries the way it reaches `packs`, which is the layer's whole reason to exist.
	userCfg, err := loadUserScopeConfig(userPath, userPath, true, warn)
	if err != nil {
		return nil, err
	}
	var out map[string]packload.UserProfile
	if v, present := userCfg.Get(profilesKey); present && v != nil {
		entries, problems := checkProfiles(v)
		for _, p := range problems {
			warn(p + " — entry skipped")
		}
		out = entries
	}
	return out, nil
}

// checkProfiles lowers a raw `profiles` value, returning the entries it could read and a
// problem string per entry it could not. A non-object value yields one problem and no
// entries.
func checkProfiles(v any) (map[string]packload.UserProfile, []string) {
	m, ok := asMap(v)
	if !ok {
		return nil, []string{"config." + profilesKey + ": expected an object of profile name → profile entry"}
	}
	out := make(map[string]packload.UserProfile, m.Len())
	var problems []string
	for _, name := range m.Keys() {
		raw, _ := m.Get(name)
		entry, problem := checkProfileEntry(name, raw)
		if problem != "" {
			problems = append(problems, problem)
			continue
		}
		out[name] = entry
	}
	return out, problems
}

// checkProfileEntry lowers ONE entry: the provider it selects and its option values.
//
// `provider` is REQUIRED (§5.2 property 3, and the property is inverted, not softened —
// declaration was ruled mandatory after a first draft said otherwise). A null option
// value is refused rather than read as "unset": the null-as-delete convention this
// config speaks everywhere else would here compose the same nothing an omitted key
// already composes, so the two readings would differ only in which one a reader
// assumes — and OQ-CS7's note is the reason the PROVIDER's null is not this null.
func checkProfileEntry(name string, raw any) (packload.UserProfile, string) {
	path := "config." + profilesKey + "." + name
	if name == "" {
		return packload.UserProfile{}, "config." + profilesKey + `: a profile name must not be ""`
	}
	m, ok := raw.(*jsonx.OrderedMap)
	if !ok {
		return packload.UserProfile{}, path + ": expected an object with a \"provider\" and option values"
	}
	rawProvider, has := m.Get("provider")
	if !has {
		return packload.UserProfile{}, path + ": missing required \"provider\" — a profile is a " +
			"selection over a provider, so the name declares nothing without one"
	}
	provider, ok := asStr(rawProvider)
	if !ok {
		return packload.UserProfile{}, path + ".provider: expected a string"
	}
	options := map[string]string{}
	for _, key := range m.Keys() {
		if key == "provider" {
			continue
		}
		v, _ := m.Get(key)
		s, isStr := asStr(v)
		if !isStr {
			return packload.UserProfile{}, fmt.Sprintf(
				"%s.%s: expected a string option value (omit the key to leave the option unset; "+
					"a null here is not the delete it is elsewhere in this config)", path, key)
		}
		options[key] = s
	}
	return packload.UserProfile{Provider: provider, Options: options}, ""
}

// validateProfiles reports `profiles` problems, and it is the reason a malformed entry
// is FATAL at launch rather than a warning: loadAndValidateConfig runs this on every
// launch, so a profile that cannot lower refuses the launch instead of silently not
// existing (the OQ-CS6 failure mode, one layer up).
//
// It reads the USER config and the WORKSPACE config separately, never the merged map —
// validatePacks is the pattern and the reason is stated there: in the merged map a
// `profiles` key from either scope looks the same, and only the workspace one is wrong.
func validateProfiles(workspace string, errs *[]string) {
	userPath := paths.UserConfigPath()
	if userCfg, err := loadUserScopeConfig(userPath, userPath, false, func(string) {}); err == nil && userCfg != nil {
		if v, present := userCfg.Get(profilesKey); present && v != nil {
			_, problems := checkProfiles(v)
			for _, p := range problems {
				add(errs, p)
			}
		}
	}
	wsCfg, err := LoadWorkspaceConfig(workspace, false, func(string) {})
	if err != nil || wsCfg == nil {
		return
	}
	if _, atWorkspace := wsCfg.Get(profilesKey); atWorkspace {
		add(errs, "config."+profilesKey+": user-scope only — move it to "+
			"~/.config/yolo-jail/config.jsonc. A workspace config travels with the repo "+
			"and is agent-editable, so it cannot decide which provider an agent talks to "+
			"— a profile steers the endpoint and the model an agent uses, which is "+
			"exactly the steering a committed, agent-writable file must not do.")
	}
}
