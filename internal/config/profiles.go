package config

// The profiles/profile config keys: docs/reference/providers.md

// profiles.go implements the `profiles` config key: the USER-declared half of a profile
// (docs/reference/providers.md — Profiles and options: a profile is a named selection over a
// provider, user intent over a surface the provider defines).
//
// SCOPE, and it is the same security model `packs` runs (packs.go's header carries the
// full argument, OQ-CS5 ruled it for BOTH keys — `profiles` and the `profile` selector,
// `use_profiles` until PP-D10, the pair this file's validateProfiles refuses at workspace
// scope): USER-SCOPE ONLY. `profiles` is read from paths.UserConfigPath() DIRECTLY
// rather than from the merged config, so workspace scope is inexpressible by
// construction; `profile` is read from the merged map by the launch (assemble's
// effectiveUseProfiles), so a workspace spelling there is a REFUSAL rather than an
// impossibility. The reason is one sentence long and covers the pair: a workspace config
// travels with the repo and is agent-editable, and a profile steers which ENDPOINT and
// which MODEL an agent talks to — the steering a committed, agent-writable file may not
// do, whether it does it by declaring the profile or by switching it on.
//
// This file LOWERS only. It decides that an entry is well-formed — an object naming a
// provider, with string option values, over the three profile FIELDS that are not options
// (`via`, `enforce_models` and `subagents`) — and nothing else: what an option MEANS is the
// derive's business and the provider owns which options exist (docs/reference/providers.md
// §"Profiles and options"), so the lowering never validates a value against anything. The
// pack-side resolution (defaults under these values, the census, the undeclared-name
// refusal) is packload.ResolveProfiles.
//
// `subagents` is the one field with a SHAPE of its own, so its shape is checked here
// (checkProfileSubagents): a nested object whose halves name models, refused rather than
// skipped when a key or a value cannot be read. Its VALUES are still not validated — an
// alias is resolved by the deriving agent's own ladder, at the render.

import (
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
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
		// Each skipped entry names the file and line it was written at (sources.go).
		for _, p := range locateInUserScope(problems) {
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

// checkProfileEntry lowers ONE entry: the provider it selects, its option values, and the
// profile fields that are not options (`via`, `enforce_models`, `subagents`).
//
// `provider` is REQUIRED (§5.2 property 3, and the property is inverted, not softened —
// declaration was ruled mandatory after a first draft said otherwise). A null option
// value is refused rather than read as "unset": the null-as-delete convention this
// config speaks everywhere else would here compose the same nothing an omitted key
// already composes, so the two readings would differ only in which one a reader
// assumes. The PROVIDER's null is not this null, and for a reason that does not apply
// here: under a provider's `options` a null keeps the option DECLARED, because dropping a
// default must not un-declare an option a profile may name (docs/reference/providers.md
// §"How the table composes", the options-map carve-out).
func checkProfileEntry(name string, raw any) (packload.UserProfile, string) {
	path := "config." + profilesKey + "." + name
	if name == "" {
		return packload.UserProfile{}, "config." + profilesKey + `: a profile name must not be ""`
	}
	if strings.Contains(name, "=") {
		return packload.UserProfile{}, path + `: a profile name must not contain "=" — the ` +
			`-p/--profile value grammar dispatches on it (bare name = every selected ` +
			`pack; cli=name = one CLI), and a name carrying it would be unspellable`
	}
	if strings.Contains(name, ",") {
		return packload.UserProfile{}, path + `: a profile name must not contain "," — the ` +
			`-p/--profile value grammar separates the profiles of one agent's list with it ` +
			`(-p pi=zai,openrouter), and a name carrying it would be unspellable`
	}
	// "native" is the selection's one reserved word (OQ-RC20): selecting it leaves the agent on its
	// own login, so a profile under that name could never be selected.
	if name == NativeProvider {
		return packload.UserProfile{}, path + `: "` + NativeProvider + `" is reserved — selecting it ` +
			`means "no provider: the agent's own login" (OQ-RC20), so a profile by that name could ` +
			`never be selected. Rename the profile, and select it by its new name`
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
	if provider == "" {
		return packload.UserProfile{}, path + `.provider: must not be "" — a profile is a ` +
			`selection over a provider, and an empty one names nothing`
	}
	options := map[string]string{}
	via := ""
	var enforce *bool
	var subagents luahook.Subagents
	for _, key := range m.Keys() {
		if key == "provider" {
			continue
		}
		if key == "subagents" {
			// `subagents` is a profile FIELD, not a provider option, for via's reason: what
			// the profile's child agents run on (a `default` and a `roles` map) is user
			// intent about a surface the provider defines, and a nested object can be
			// neither a string option nor part of the provider-option census.
			v, _ := m.Get(key)
			sub, problem := checkProfileSubagents(path, v)
			if problem != "" {
				return packload.UserProfile{}, problem
			}
			subagents = sub
			continue
		}
		if key == "enforce_models" {
			// `enforce_models` is a profile FIELD, not a provider option, like `via`: whether
			// a narrowed model list refuses other models (docs/design/model-lists-and-pickers.md
			// MM-D5). A boolean; absent means on.
			v, _ := m.Get(key)
			b, isBool := v.(bool)
			if !isBool {
				return packload.UserProfile{}, path + ".enforce_models: expected true or false — " +
					"whether the agents refuse a model outside a list a `models` contribution " +
					"narrowed (on by default)"
			}
			enforce = &b
			continue
		}
		if key == "via" {
			// `via` is a profile FIELD, not a provider option (OQ-WG6): it names the service
			// pack that carries this profile's traffic instead of the agent's own client.
			v, _ := m.Get(key)
			s, isStr := asStr(v)
			if !isStr || !packdecl.ValidPackName(s) {
				return packload.UserProfile{}, fmt.Sprintf("%s.via: expected a service pack "+
					"name such as \"wire-bridge\" — via names the pack whose service carries "+
					"this profile's traffic", path)
			}
			via = s
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
	return packload.UserProfile{Provider: provider, Options: options, Via: via,
		EnforceModels: enforce, Subagents: subagents}, ""
}

// checkProfileSubagents lowers ONE profile's `subagents` declaration, the profile field that
// says what its child agents run on, and refuses a shape it cannot read with the fix in the
// message.
//
// The two halves answer the two questions a child's model raises that no rule can infer. A
// `default` replaces the child default the derive would otherwise take from the profile's own
// model, so one parent model can route its children elsewhere (the whale that hands its
// children the cheaper floor model of the same provider). `roles` pins one model per subagent
// ROLE, so two children of one parent need not run wherever the provider's tiers happen to
// fall. Both values are an alias or an exact id of the profile's PROVIDER — the same
// vocabulary the profile's `model` option speaks, never a `<provider>/<...>` spelling, because
// the derive resolves each through the selected provider's own ladder and a value naming
// another provider would be a child yolo let cross one.
//
// Unknown keys and empty pieces are refused here rather than skipped: a declaration the user
// believes is routing their children and is not is worse than a launch that stops and names
// the key. An entry that states neither half declares nothing and is refused too, since the
// whole object is then a no-op the user did not intend.
func checkProfileSubagents(path string, raw any) (luahook.Subagents, string) {
	m, ok := raw.(*jsonx.OrderedMap)
	if !ok {
		return luahook.Subagents{}, path + ".subagents: expected an object declaring \"default\" " +
			"(the model a child agent that names none of its own starts on) and/or \"roles\" " +
			"(one model per subagent role)"
	}
	var sub luahook.Subagents
	roles := map[string]string{}
	for _, key := range m.Keys() {
		v, _ := m.Get(key)
		switch key {
		case "default":
			s, isStr := asStr(v)
			if !isStr || s == "" {
				return luahook.Subagents{}, path + `.subagents.default: expected a non-empty string — ` +
					"the model a child agent that names none of its own starts on, as an alias or " +
					"exact model id of the profile's provider"
			}
			sub.Default = s
		case "roles":
			rs, isMap := v.(*jsonx.OrderedMap)
			if !isMap {
				return luahook.Subagents{}, path + `.subagents.roles: expected an object mapping a ` +
					`subagent role to a model, e.g. {"worker": "deepseek-floor"}`
			}
			for _, role := range rs.Keys() {
				rv, _ := rs.Get(role)
				if role == "" {
					return luahook.Subagents{}, path + `.subagents.roles: a role name must not be "" — ` +
						"it names the subagent the model is pinned to"
				}
				s, isStr := asStr(rv)
				if !isStr || s == "" {
					return luahook.Subagents{}, path + ".subagents.roles." + role +
						": expected a non-empty string — the model that role runs on, as an alias " +
						"or exact model id of the profile's provider"
				}
				roles[role] = s
			}
		default:
			return luahook.Subagents{}, path + ".subagents." + key + ": unknown key — a `subagents` " +
				`entry declares "default" (the model a child agent that names none starts on) and ` +
				`"roles" (one model per subagent role), and nothing else`
		}
	}
	if sub.Default == "" && len(roles) == 0 {
		return luahook.Subagents{}, path + `.subagents: declares nothing — give it a "default", ` +
			`a "roles" entry, or remove it`
	}
	sub.Roles = roles
	return sub, ""
}

// validateProfiles reports `profiles` problems, and it is the reason a malformed entry
// is FATAL at launch rather than a warning: loadAndValidateConfig runs this on every
// launch, so a profile that cannot lower refuses the launch instead of silently not
// existing (the OQ-CS6 failure mode, one layer up).
//
// It owns the SCOPE half of BOTH profile keys (OQ-CS5 — user scope only, both of them,
// the same rule `packs` follows): `profiles` is what a profile declares, `profile`
// (`use_profiles` until PP-D10) is what activates one, and a workspace config may write
// neither, because activating a profile steers the endpoint and the model an agent talks to
// exactly as declaring one does. `profile` is the reason this is a refusal rather than a
// construction: the selection is read off the merged config (assemble.effectiveUseProfiles),
// so a workspace spelling would have taken effect, and only this check stops it.
//
// It reads the USER config (validateUserScopeProfiles, the half `yolo host` runs too through
// ValidateProviderSection) and the WORKSPACE config separately, never the merged map —
// validatePacks is the pattern and the reason is stated there: in the merged map a
// `profiles` key from either scope looks the same, and only the workspace one is wrong.
// validateUserScopeProfiles is validateProfiles' user-file half: every `profiles` entry's shape.
func validateUserScopeProfiles(errs *[]string) {
	userPath := paths.UserConfigPath()
	userCfg, err := loadUserScopeConfig(userPath, userPath, false, func(string) {})
	if err != nil || userCfg == nil {
		return
	}
	validateUserScopeProfilesConfig(userCfg, errs)
}

func validateUserScopeProfilesConfig(userCfg *jsonx.OrderedMap, errs *[]string) {
	if v, present := userCfg.Get(profilesKey); present && v != nil {
		_, problems := checkProfiles(v)
		for _, p := range problems {
			add(errs, p)
		}
	}
}

func validateProfiles(workspace string, errs *[]string) {
	validateProfilesWithScopes(workspace, nil, nil, errs)
}

func validateProfilesWithScopes(workspace string, userCfg, wsCfg *jsonx.OrderedMap, errs *[]string) {
	if userCfg == nil {
		validateUserScopeProfiles(errs)
	} else {
		validateUserScopeProfilesConfig(userCfg, errs)
	}
	if wsCfg == nil {
		var err error
		wsCfg, err = LoadWorkspaceConfig(workspace, false, func(string) {})
		if err != nil || wsCfg == nil {
			return
		}
	}
	// ProfileKey (profileselection.go) is the key that ACTIVATES a profile; it shares one scope
	// rule and one refusal with profilesKey. The retired `use_profiles` keeps the scope refusal
	// beside its rename refusal, so a workspace that carries it is told both halves of the fix
	// at once: rename it, and move it to user scope.
	for _, key := range []string{profilesKey, ProfileKey, retiredUseProfilesKey} {
		if _, atWorkspace := wsCfg.Get(key); atWorkspace &&
			(key == retiredUseProfilesKey || WorkspaceUserScopeOnlyKey(key)) {
			add(errs, userScopeOnlyMessage(key))
		}
	}
}

// userScopeOnlyMessage is the ONE workspace-scope refusal both profile keys give. The
// shape is validatePacks' (the key that drew the boundary first), and the reason is
// shared rather than restated per key: a profile steers which provider an agent talks
// to, and the workspace file is the committed, agent-editable one — declaring the
// profile and activating it are two spellings of the same steering, so they get the same
// words with the key name swapped.
func userScopeOnlyMessage(key string) string {
	return "config." + key + ": user-scope only — move it to " +
		paths.UserConfigDisplayPath() + ". A workspace config travels with the repo " +
		"and is agent-editable, so it cannot decide which provider an agent talks to " +
		"— a profile steers the endpoint and the model an agent uses, which is " +
		"exactly the steering a committed, agent-writable file must not do."
}
