package config

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// knownProfileKeys records the explicit profile fields, including model (which the legacy
// lowering treated as a provider option). Other provider-specific option names remain dynamic.
var knownProfileKeys = set("provider", "model", "via", "enforce_models", "subagents")

// validateAgentProfilesRetired refuses the pre-rename spelling by name, the
// validateJournalRetired pattern: error on the host, warning in-jail (a snapshot
// written by an older launcher legitimately carries the old key).
func validateAgentProfilesRetired(config *jsonx.OrderedMap, errs, warns *[]string) {
	if _, present := config.Get("agent_profiles"); !present {
		return
	}
	msg := "config.agent_profiles: RENAMED — this key is now `profile` (it was `use_profiles` " +
		"in between), because the keys were always CLI names and core knows packs, not agents " +
		"(docs/reference/providers.md#declaring-and-selecting-a-profile). Rename the key in place; " +
		"the values are unchanged."
	if inJail() {
		add(warns, msg+" (ignored here: this is the host-generated config snapshot, "+
			"so rename the key in the HOST config.)")
		return
	}
	add(errs, msg)
}

// validateUseProfilesRetired refuses `use_profiles` by name (PP-D10, 2026-09-29): the key is
// `profile` now, mirroring -p/--profile. The agent_profiles pattern above: an error on the
// host, a warning in-jail, where the config is the snapshot a launcher generated and one
// written by a launcher older than the rename legitimately carries the old key. The message
// respells the user's own value, since the object form takes the old table unchanged.
func validateUseProfilesRetired(config *jsonx.OrderedMap, errs, warns *[]string) {
	v, present := config.Get(retiredUseProfilesKey)
	if !present {
		return
	}
	msg := "config.use_profiles: RENAMED — this key is now `profile`, which mirrors -p/--profile: " +
		"a string selects one profile for every agent (`-p <name>`), and an object selects per " +
		"CLI name (`-p <cli>=<name>`), with \"*\" for every agent the object does not name " +
		"(docs/reference/providers.md#declaring-and-selecting-a-profile)."
	if sel, ok := ProfileSelectionOf(v); ok && !sel.IsZero() {
		msg += " Your entries, respelled: " + ProfileKeySpelling(sel)
	} else {
		msg += " Rename the key in place; an object's entries are unchanged."
	}
	if inJail() {
		add(warns, msg+" (ignored here: this is the host-generated config snapshot, "+
			"so rename the key in the HOST config.)")
		return
	}
	add(errs, msg)
}

// validateProfile checks the `profile` key's shape: a profile name or a LIST of them (every
// agent's), or an object of CLI name → profile name, list or null, with "*" for every agent it
// does not name.
//
// A LIST is an agent's ACTIVE SET (docs/design/active-provider-sets.md OQ-AP1, ruled
// 2026-09-29; the doc coins the term): the profiles it runs on, in order, its first entry where
// a fresh session starts. A list NAMED at an agent whose pack does not declare provider_sets is
// refused (OQ-AP2). The string form, the top-level list and "*" name no agent, so a list there
// is a BARE list, narrowed to its first entry at such an agent and said at launch (OQ-AP3,
// config.ProfileTableFor), never refused for it.
func validateProfile(config *jsonx.OrderedMap, errs *[]string, edit ...*editValidationContext) {
	v, present := config.Get(ProfileKey)
	if !present || v == nil {
		return
	}
	switch v.(type) {
	case string, []any:
		if _, problem := profileSetOf(v); problem != "" {
			add(errs, "config.profile: "+problem)
		}
		return
	}
	profiles, ok := asMap(v)
	if !ok {
		add(errs, "config.profile: expected a string (the profile every agent runs), a list "+
			"(the profiles every agent runs, in order) or an object (CLI name → profile name or "+
			"list, \"*\" for every agent it does not name)")
		return
	}
	// The KEY is a CLI name — the binary a pack installs — and an unknown one is fatal
	// (providers.md#what-the-launch-checks-and-prints). Before this check {"cloude": "bedrock"}
	// validated clean and silently did nothing, which is the live hole the design documents:
	// the values were checked as strings while the thing they were keyed by was never checked
	// at all.
	//
	// "*" names no CLI, so it is never checked against the namespace. A null value selects no
	// profile for its CLI and asserts nothing about the key, so an object holding only nulls
	// (and "*") costs no pack resolution at all — the same leniency the retired-key convention
	// gives a key being deleted.
	keys := profiles.Keys()
	wantsNamespace := false
	for _, k := range keys {
		if val, _ := profiles.Get(k); val != nil && k != ProfileEveryAgent {
			wantsNamespace = true
			break
		}
	}
	installed, namespaceKnown := []string(nil), false
	var capable map[string]bool
	if wantsNamespace {
		if len(edit) != 0 && edit[0] != nil {
			namespace, sets, err := explicitEditProfileNamespaces(edit[0])
			if errors.Is(err, errPackUnverifiableInJail) {
				// A jail cannot read that pack, so the namespace is unknowable here: the
				// namespace half steps aside, as below when UseProfileCLINames cannot answer.
			} else if err != nil {
				add(errs, "config."+ProfileKey+": cannot validate the planned CLI namespace: "+err.Error())
			} else {
				installed = explicitEditProfileNames(namespace)
				capable = sets
				namespaceKnown = true
			}
		} else {
			// An unresolvable configured pack makes the namespace unknowable, and that pack
			// is refused on its own terms — louder, and first — by `yolo check`'s Packs
			// section and by the launch's staging. Reporting it here too would misdiagnose a
			// broken install as a typo'd profile key, so the NAMESPACE half steps aside; the
			// shape half below still runs, because a value that is not a string is a fact
			// about this config alone.
			if names, known := UseProfileCLINames(); known {
				installed, namespaceKnown = names, true
				// The set-capable half is asked only when an agent is NAMED with a list of more
				// than one, since it walks the same universe again.
				for _, k := range keys {
					if val, _ := profiles.Get(k); k != ProfileEveryAgent {
						if list, isList := val.([]any); isList && len(list) > 1 {
							capable, _ = SetCapableCLINames()
							break
						}
					}
				}
			}
		}
	}
	for _, agent := range keys {
		profV, _ := profiles.Get(agent)
		path := "config.profile." + agent
		if profV == nil {
			continue
		}
		set, problem := profileSetOf(profV)
		if problem != "" {
			add(errs, path+": "+problem)
			continue
		}
		if agent == ProfileEveryAgent {
			continue
		}
		if namespaceKnown && !containsStr(installed, agent) {
			add(errs, unknownProfileKeyError(agent, installed))
			continue
		}
		// A list NAMED at an agent whose pack does not declare provider_sets is refused before
		// anything starts (OQ-AP2), over the same universe the key is checked against: whether
		// this workspace selects the agent's pack or not, the list asks it for what it cannot do.
		if len(set) > 1 && namespaceKnown && !capable[agent] {
			add(errs, path+": "+packload.SingleProviderSetRefusal(agent, set))
		}
	}
}

// profileSetOf reads one `profile` value as an active set: a profile name, or a non-empty array
// of distinct profile names, in order. The problem is the refusal's text after the path, "" when
// the value is a set. null is the caller's (no selection), so it never reaches here.
func profileSetOf(v any) ([]string, string) {
	if name, isString := v.(string); isString {
		if problem := profileNameProblem(name); problem != "" {
			return nil, problem
		}
		return []string{name}, ""
	}
	list, isList := v.([]any)
	if !isList {
		return nil, "expected a profile name, a list of them (the agent's active set, first " +
			"entry where a session starts), or null for none"
	}
	if len(list) == 0 {
		return nil, "an empty list selects nothing, and nothing has one spelling: null " +
			"(or remove the key)"
	}
	seen := map[string]bool{}
	set := make([]string, 0, len(list))
	for i, e := range list {
		name, isString := e.(string)
		if !isString || name == "" {
			return nil, fmt.Sprintf("entry %d of the list is not a profile name", i+1)
		}
		if problem := profileNameProblem(name); problem != "" {
			return nil, fmt.Sprintf("entry %d of the list: %s", i+1, problem)
		}
		if seen[name] {
			return nil, fmt.Sprintf("profile %q is listed twice — a set names each profile once", name)
		}
		seen[name] = true
		set = append(set, name)
	}
	return set, ""
}

// profileNameProblem is what is wrong with name as a `profile` value, "" when nothing is. An
// empty name would select nothing in silence, so null is the one spelling of "none". A name
// holding "=" or "," can never be declared (profile names refuse both), and it is nearly always
// the flag's grammar written into a string — its cli=name pairs, or its comma list — so the
// message respells it as the object or the JSON list the key takes instead.
func profileNameProblem(name string) string {
	if name == "" {
		return "expected a profile name; null selects none"
	}
	if strings.Contains(name, "=") {
		msg := fmt.Sprintf("%q is not a profile name — a name cannot contain \"=\", and the "+
			"per-agent form is an object, not -p's cli=name grammar", name)
		if _, pairs, err := ParseProfileFlag(name); err == nil && len(pairs) > 0 {
			msg += ": " + ProfileKeySpelling(ProfileSelection{Named: pairs})
		}
		return msg
	}
	if strings.Contains(name, ",") {
		msg := fmt.Sprintf("%q is not a profile name — a name cannot contain \",\", and a list "+
			"of profiles is a JSON array, not -p's comma list", name)
		if bare, _, err := ParseProfileFlag(name); err == nil && len(bare) > 1 {
			list, _ := jsonx.DumpsCompact(profileSetSpelling(bare))
			msg += ", so write it in its place: " + list
		}
		return msg
	}
	return ""
}

// UnknownProfileKey is validateProfile's refusal for ONE `profile` key, for a caller that never
// runs ValidateConfig: `yolo host`, which reads the user file directly
// (docs/design/credential-sources-separation.md ES-D5). It returns the exact message the
// validator adds and true when no resolvable pack — selected or not — installs a CLI named key,
// and false when one does or when that namespace cannot be enumerated (an unresolvable
// configured pack), where the validator steps aside too. One rule and one message, so the host
// refuses exactly what `yolo check` and every jail launch refuse in the same user file.
func UnknownProfileKey(key string) (string, bool) {
	installed, known := UseProfileCLINames()
	if !known || containsStr(installed, key) {
		return "", false
	}
	return unknownProfileKeyError(key, installed), true
}

// unknownProfileKeyError is the one spelling of the refusal, path included.
func unknownProfileKeyError(key string, installed []string) string {
	return "config.profile." + key + ": " + unknownProfileCLIMessage(key, installed)
}

// unknownProfileCLIMessage explains a `profile` key no resolvable pack answers to.
//
// It lists what IS installed, because the most likely cause is a typo in a tool name and
// the real list is the whole fix — the same reason unknownEmbeddedMessage lists pack
// names, and the same list: both name the CLIs yolo can actually launch.
func unknownProfileCLIMessage(key string, installed []string) string {
	have := "none"
	if len(installed) > 0 {
		have = strings.Join(installed, ", ")
	}
	return fmt.Sprintf("no pack installs a CLI named %q (installed: %s) — a `profile` "+
		"key selects a profile by the binary a pack installs, not by pack or agent name "+
		"(\"*\" is the one key that names no CLI: every agent the object does not name)",
		key, have)
}
