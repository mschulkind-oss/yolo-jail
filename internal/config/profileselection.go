package config

// profileselection.go is the `profile` config key and the one fold every notch reads a
// profile selection through (docs/design/providers-and-profiles-redesign.md PP-D10, ruled
// 2026-09-29). The key is the persistent spelling of `-p`/`--profile`, and it mirrors the
// flag form for form:
//
//	"profile": "bedrock"                              = -p bedrock
//	"profile": {"pi": "codex", "claude": "bedrock"}   = -p pi=codex,claude=bedrock
//	"profile": {"*": "bedrock", "pi": "codex"}        = -p bedrock -p pi=codex
//
// Both spellings lower to one ProfileSelection, and ProfileTableFor is the only function that
// turns selections into the CLI-name → profile-name table a launch carries (YOLO_USE_PROFILES
// in a jail, the one agent's entry at `yolo host`). So the key and the flag cannot drift: a
// form means what it means because the same two fields say it, and one fold reads them.
//
// SCOPE is `use_profiles`' before it (OQ-CS5): user scope only, refused at workspace scope by
// validateProfiles, because selecting a profile steers the endpoint and the model an agent
// talks to.

import (
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// ProfileKey is the top-level config key that selects the profile each agent runs.
const ProfileKey = "profile"

// ProfileEveryAgent is the `profile` object's key for every agent the object does not name:
// the config spelling of a bare `-p <name>`. It cannot collide with an agent, because an agent
// is named by the program its pack installs and no program is called `*`.
const ProfileEveryAgent = "*"

// retiredUseProfilesKey is the key `profile` replaced on 2026-09-29. It shipped in v0.11.0, so
// it keeps a by-name refusal (validateUseProfilesRetired) rather than becoming an unknown key.
const retiredUseProfilesKey = "use_profiles"

// ProfileSelection is one source's profile selection, in the shape both of its spellings lower
// to: every `-p`/`--profile` value of one launch, or the `profile` config key.
//
// Precedence WITHIN a selection is fixed: a Named entry beats Default for the CLI it names.
// Precedence BETWEEN selections is the order ProfileTableFor is handed them.
//
// Both fields hold ONE profile name. The active-set design (docs/design/active-provider-sets.md,
// OQ-AP1 to OQ-AP3) gives a CLI an ordered list; when it is built, Default and each Named
// value become that list, and ApplyFlag, ProfileSelectionOf and ProfileTableFor are the three
// readers that change with them — so the key's list form and -p's comma continuation still
// lower through one shape.
type ProfileSelection struct {
	// Default is the profile for every CLI the selection does not name: a bare `-p <name>`
	// (the last one typed), the key's string form, or its "*" entry. "" selects nothing.
	Default string
	// Named maps a CLI name (the binary a pack installs) to the profile it selects: a
	// `-p <cli>=<name>` pair (later pairs winning), or one of the key's per-agent entries. An
	// empty value names the CLI and selects NO profile for it, so Default does not reach it:
	// the key's null, and the flag's `-p <cli>=`.
	Named map[string]string
}

// IsZero reports whether the selection selects nothing and names nothing.
func (s ProfileSelection) IsZero() bool { return s.Default == "" && len(s.Named) == 0 }

// namedKeys is Named's keys in the order the table emits them: sorted, whichever spelling
// they came from, so the key and its -p twin emit the same bytes, on every run.
func (s ProfileSelection) namedKeys() []string {
	keys := make([]string, 0, len(s.Named))
	for k := range s.Named {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ParseProfileFlag is the `-p`/`--profile` grammar every notch reads: a value with no "=" is a
// bare profile name (pairs nil), and one with "=" is comma-separated cli=name pairs (a non-nil
// map, later pairs winning). An element with no "=" inside the pair grammar is dropped, as the
// run path always dropped it. Profile names refuse "=" at declaration (config `profiles` and
// the pack manifest), so a value containing "=" is unambiguously the pair grammar.
func ParseProfileFlag(v string) (name string, pairs map[string]string) {
	if !strings.Contains(v, "=") {
		return v, nil
	}
	pairs = make(map[string]string)
	for _, pair := range strings.Split(v, ",") {
		if parts := strings.SplitN(pair, "=", 2); len(parts) == 2 {
			pairs[parts[0]] = parts[1]
		}
	}
	return "", pairs
}

// ApplyFlag folds one `-p`/`--profile` value into s: a bare name replaces Default, and each
// cli=name pair sets that CLI's Named entry. Repeated flags fold in the order typed, so the
// last bare name and the last pair for a CLI win.
func (s *ProfileSelection) ApplyFlag(v string) {
	name, pairs := ParseProfileFlag(v)
	if pairs == nil {
		s.Default = name
		return
	}
	if s.Named == nil {
		s.Named = make(map[string]string, len(pairs))
	}
	for cli, profile := range pairs {
		s.Named[cli] = profile
	}
}

// ProfileSelectionOf lowers a `profile` value: a string selects that profile for every agent,
// an object selects per CLI name with "*" for every agent it does not name, and null selects
// nothing. A null ENTRY names its CLI and selects no profile for it, so "*" does not reach it.
//
// ok is false for a value validateProfile refuses (a non-string entry, a value that is neither
// a string nor an object); the well-formed rest still lowers, so a reader that runs without
// having validated selects what it can read, and validation is where the refusal is said.
func ProfileSelectionOf(v any) (ProfileSelection, bool) {
	var s ProfileSelection
	switch val := v.(type) {
	case nil:
		return s, true
	case string:
		s.Default = val
		return s, true
	case *jsonx.OrderedMap:
		ok := true
		for _, k := range val.Keys() {
			entry, _ := val.Get(k)
			var name string
			switch e := entry.(type) {
			case nil:
			case string:
				name = e
			default:
				ok = false
				continue
			}
			if k == ProfileEveryAgent {
				s.Default = name
				continue
			}
			if s.Named == nil {
				s.Named = map[string]string{}
			}
			s.Named[k] = name
		}
		return s, ok
	}
	return s, false
}

// ConfigProfileSelection is cfg's `profile` key, lowered (ProfileSelectionOf). A config
// without the key selects nothing.
func ConfigProfileSelection(cfg *jsonx.OrderedMap) ProfileSelection {
	if cfg == nil {
		return ProfileSelection{}
	}
	v, _ := cfg.Get(ProfileKey)
	s, _ := ProfileSelectionOf(v)
	return s
}

// InstalledBins is every binary the packs install, in pack order: the CLIs a selection's
// Default reaches.
func InstalledBins(packs []*packload.Pack) []string {
	var bins []string
	for _, p := range packs {
		bins = append(bins, p.InstallBins()...)
	}
	return bins
}

// ProfileTableFor folds selections into the CLI-name → profile-name table, LOWEST PRECEDENCE
// FIRST. Each selection sets its Default on every CLI in bins, then its Named entries, so:
//
//   - within one selection a named CLI keeps its own entry and Default reaches every other
//     installed CLI (`-p bedrock -p pi=codex`, and `{"*": "bedrock", "pi": "codex"}`);
//   - a later selection's every form beats an earlier one's for each CLI it reaches: a launch
//     hands the config key first and the flag second, so any `-p` beats the key.
//
// Default reaches the INSTALLED CLIs only, never the command after `--` (the 2026-09-03
// ruling). A Named entry is kept whether or not bins holds it: a key no pack installs is the
// validator's and checkProfileTargets' refusal, not a silent drop here. A Named entry with no
// profile is emitted as null, which every reader of the table (packload.ProfileTable) reads
// as no selection.
func ProfileTableFor(bins []string, selections ...ProfileSelection) *jsonx.OrderedMap {
	out := jsonx.NewOrderedMap()
	for _, s := range selections {
		if s.Default != "" {
			for _, bin := range bins {
				out.Set(bin, s.Default)
			}
		}
		for _, cli := range s.namedKeys() {
			if name := s.Named[cli]; name != "" {
				out.Set(cli, name)
			} else {
				out.Set(cli, nil)
			}
		}
	}
	return out
}

// ConfigProfileTable is the table a config alone selects over packs: its `profile` key folded
// with no flag above it. It is what every reader with no launch in hand reads (`yolo check`'s
// predictions, the selection closure of a host verb that launches nothing).
func ConfigProfileTable(cfg *jsonx.OrderedMap, packs []*packload.Pack) map[string]string {
	return packload.ProfileTable(ProfileTableFor(InstalledBins(packs), ConfigProfileSelection(cfg)))
}

// ProfileKeySpelling is the `profile` key's JSON for a selection, for a message that tells the
// user what to write: the string form when the selection is a Default alone, the object form
// otherwise, "*" first. "" for an empty selection.
func ProfileKeySpelling(s ProfileSelection) string {
	if s.IsZero() {
		return ""
	}
	if len(s.Named) == 0 {
		out, _ := jsonx.DumpsCompact(s.Default)
		return `"` + ProfileKey + `": ` + out
	}
	m := jsonx.NewOrderedMap()
	if s.Default != "" {
		m.Set(ProfileEveryAgent, s.Default)
	}
	for _, cli := range s.namedKeys() {
		if name := s.Named[cli]; name != "" {
			m.Set(cli, name)
		} else {
			m.Set(cli, nil)
		}
	}
	out, _ := jsonx.DumpsCompact(m)
	return `"` + ProfileKey + `": ` + out
}
