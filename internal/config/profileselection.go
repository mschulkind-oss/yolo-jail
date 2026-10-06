package config

// profileselection.go is the `profile` config key and the one fold every notch reads a
// profile selection through (docs/design/providers-and-profiles-redesign.md PP-D10, ruled
// 2026-09-29). The key is the persistent spelling of `-p`/`--profile`, and it mirrors the
// flag form for form:
//
//	"profile": "bedrock"                              = -p bedrock
//	"profile": {"pi": "codex", "claude": "bedrock"}   = -p pi=codex,claude=bedrock
//	"profile": {"*": "bedrock", "pi": "codex"}        = -p bedrock -p pi=codex
//	"profile": {"pi": ["zai", "openrouter"]}          = -p pi=zai,openrouter
//	"profile": ["zai", "openrouter"]                  = -p zai,openrouter
//
// A LIST is an agent's ACTIVE SET (docs/design/active-provider-sets.md OQ-AP1 to OQ-AP3, ruled
// 2026-09-29; the doc coins the term): the profiles it runs on, in order, the first where a
// fresh session starts. In config it is a JSON array; on the command line a comma continues the
// list of the agent named before it. A list NAMED at an agent whose pack does not declare
// provider_sets is refused (OQ-AP2, validateProfile and the launch's own checks); a BARE list —
// the string or list form, "*", or a `-p` naming no agent — goes whole to every agent whose pack
// declares provider_sets and its first entry to every other, and the launch says so (OQ-AP3,
// FoldProfiles).
//
// Both spellings lower to one ProfileSelection, and FoldProfiles (ProfileTableFor for its table)
// is the only function that turns selections into the CLI-name → profile table a launch
// carries (YOLO_USE_PROFILES in a jail, the one agent's entry at `yolo host`). So the key and
// the flag cannot drift: a form means what it means because the same two fields say it, and one
// fold reads them.
//
// SCOPE is `use_profiles`' before it (OQ-CS5): user scope only, refused at workspace scope by
// validateProfiles, because selecting a profile steers the endpoint and the model an agent
// talks to.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// ProfileKey is the top-level config key that selects the profile each agent runs.
const ProfileKey = "profile"

// ProfileEveryAgent is the `profile` object's key for every agent the object does not name:
// the config spelling of a bare `-p <name>`. It cannot collide with an agent, because an agent
// is named by the program its pack installs and no program is called `*`.
const ProfileEveryAgent = "*"

// retiredUseProfilesKey is the key `profile` replaced on 2026-09-29. It shipped in every release
// from v0.9.0 through v0.11.0, so it keeps a by-name refusal (validateUseProfilesRetired) rather
// than becoming an unknown key.
const retiredUseProfilesKey = "use_profiles"

// ProfileSelection is one source's profile selection, in the shape both of its spellings lower
// to: every `-p`/`--profile` value of one launch, or the `profile` config key.
//
// Precedence WITHIN a selection is fixed: a Named entry beats Default for the CLI it names.
// Precedence BETWEEN selections is the order FoldProfiles is handed them.
//
// Both fields hold an ACTIVE SET (docs/design/active-provider-sets.md): an ordered list of
// profile names, one entry for the one-profile selection every config before lists wrote.
type ProfileSelection struct {
	// Default is the set for every CLI the selection does not name: a bare `-p <name>` or
	// `-p <a>,<b>` (the last one typed), the key's string or list form, or its "*" entry. Empty
	// selects nothing. A list of more than one is a BARE list: FoldProfiles gives it whole to
	// every CLI whose pack declares provider_sets and its first entry to every other (OQ-AP3).
	Default []string
	// Named maps a CLI name (the binary a pack installs) to its set: a `-p <cli>=<name>,…`
	// pair (later pairs winning, each replacing the CLI's whole list), or one of the key's
	// per-agent entries. An empty set names the CLI and selects NO profile for it, so Default
	// does not reach it: the key's null, and the flag's `-p <cli>=`.
	Named map[string][]string
}

// IsZero reports whether the selection selects nothing and names nothing.
func (s ProfileSelection) IsZero() bool { return len(s.Default) == 0 && len(s.Named) == 0 }

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

// ParseProfileFlag is the `-p`/`--profile` grammar every notch reads (OQ-AP1, ruled 2026-09-29):
//
//   - a value with no "=" is a BARE list — one profile name, or several separated by commas —
//     returned as bare (pairs nil): `-p zai`, `-p zai,openrouter`. "" is no selection (nil);
//   - one with "=" is comma-separated elements, each either `cli=name`, which starts that
//     CLI's list, or a bare name, which CONTINUES the list of the CLI named before it:
//     `-p pi=zai,openrouter,claude=codex` gives pi [zai, openrouter] and claude [codex]. A
//     later pair for a CLI already named replaces its list, as later pairs always won.
//
// Refused, naming the element, and never dropped (AP-P2: yolo never accepts a list and quietly
// runs part of it): a bare name before any `cli=` in the pair grammar (`-p zai,pi=openrouter`
// has no CLI for zai to join), an empty element (`zai,,x`, a trailing comma), and a name
// continuing a `cli=` that selected nothing (`-p pi=,openrouter`, which reads both as "nothing"
// and as a list). An EMPTY PAIR is not an empty element: `cli=` selects nothing for that CLI (an
// empty list), alone or beside other pairs (`-p pi=,claude=zai`), as it always did. Until
// 2026-09-29 a bare element inside the pair grammar was dropped in silence, so
// `-p pi=zai,openrouter` started pi on zai alone.
//
// Profile names refuse "=" and "," at declaration (config `profiles` and the pack manifest), so
// the grammar has one reading.
func ParseProfileFlag(v string) (bare []string, pairs map[string][]string, err error) {
	elements := strings.Split(v, ",")
	if !strings.Contains(v, "=") {
		if v == "" {
			return nil, nil, nil
		}
		if len(elements) > 1 {
			for i, e := range elements {
				if e == "" {
					return nil, nil, emptyProfileElement(v, i)
				}
			}
		}
		return elements, nil, nil
	}
	pairs = make(map[string][]string)
	current, currentEmpty := "", false
	for i, e := range elements {
		if cli, name, isPair := strings.Cut(e, "="); isPair {
			current, currentEmpty = cli, name == ""
			if currentEmpty {
				pairs[cli] = nil
			} else {
				pairs[cli] = []string{name}
			}
			continue
		}
		if e == "" {
			return nil, nil, emptyProfileElement(v, i)
		}
		if i == 0 {
			return nil, nil, fmt.Errorf("-p %s: %q names no agent — in a list of cli=name pairs a "+
				"profile after a comma continues the list of the agent named before it, and "+
				"nothing is named before %q. Name its agent first (`-p <agent>=%s,…`)",
				shquote.Quote(v), e, e, e)
		}
		if currentEmpty {
			return nil, nil, fmt.Errorf("-p %s: %q follows %s=, which selects no profile for %s, "+
				"so it has no list to continue — `-p %s=%s` selects it for %s, and `-p %s=` alone "+
				"selects nothing", shquote.Quote(v), e, current, current, current, e, current, current)
		}
		pairs[current] = append(pairs[current], e)
	}
	return nil, pairs, nil
}

// emptyProfileElement is the grammar's refusal of an empty entry in a profile list.
func emptyProfileElement(v string, i int) error {
	return fmt.Errorf("-p %s: entry %d of the list is empty — a comma separates profile names, "+
		"so a list has no empty entry and no trailing comma", shquote.Quote(v), i+1)
}

// ApplyFlag folds one `-p`/`--profile` value into s: a bare list replaces Default, and each
// cli=list pair replaces that CLI's Named entry. Repeated flags fold in the order typed, so the
// last bare list and the last pair for a CLI win. A value the grammar refuses changes nothing
// and is returned, for the launch to refuse as misuse before anything runs.
func (s *ProfileSelection) ApplyFlag(v string) error {
	bare, pairs, err := ParseProfileFlag(v)
	if err != nil {
		return err
	}
	if pairs == nil {
		s.Default = bare
		return nil
	}
	if s.Named == nil {
		s.Named = make(map[string][]string, len(pairs))
	}
	for cli, set := range pairs {
		s.Named[cli] = set
	}
	return nil
}

// ProfileSelectionOf lowers a `profile` value: a string or a list selects for every agent, an
// object selects per CLI name with "*" for every agent it does not name, and null selects
// nothing. An entry is a name, a list of names (the agent's active set, in order) or null; a
// null ENTRY names its CLI and selects no profile for it, so "*" does not reach it.
//
// ok is false for a value validateProfile refuses (an entry that is neither a name, a list nor
// null, a list entry that is not a name, a value that is none of the forms); the well-formed
// rest still lowers, so a reader that runs without having validated selects what it can read,
// and validation is where the refusal is said.
func ProfileSelectionOf(v any) (ProfileSelection, bool) {
	var s ProfileSelection
	switch val := v.(type) {
	case nil:
		return s, true
	case string, []any:
		set, ok := profileSetValue(val)
		s.Default = set
		return s, ok
	case *jsonx.OrderedMap:
		ok := true
		for _, k := range val.Keys() {
			entry, _ := val.Get(k)
			set, entryOK := profileSetValue(entry)
			if !entryOK {
				ok = false
				continue
			}
			if k == ProfileEveryAgent {
				s.Default = set
				continue
			}
			if s.Named == nil {
				s.Named = map[string][]string{}
			}
			s.Named[k] = set
		}
		return s, ok
	}
	return s, false
}

// profileSetValue lowers one `profile` entry to its set: null and "" to no selection (nil), a
// name to a set of one, a list to its names in order. ok is false for anything else, and for a
// list holding an entry that is not a name, which lowers to the names it does hold.
func profileSetValue(v any) ([]string, bool) {
	switch val := v.(type) {
	case nil:
		return nil, true
	case string:
		if val == "" {
			return nil, true
		}
		return []string{val}, true
	case []any:
		var set []string
		ok := true
		for _, e := range val {
			name, isString := e.(string)
			if !isString || name == "" {
				ok = false
				continue
			}
			set = append(set, name)
		}
		return set, ok
	}
	return nil, false
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

// ProfileReceivers is what a selection's Default reaches: every CLI the packs install, in pack
// order, and which of them may hold a list of more than one profile — the CLIs whose program
// declares `provider_sets` (packload.HoldsProviderSets, AP-D2). Every other receiver takes a
// Default list's first entry alone (OQ-AP3).
type ProfileReceivers struct {
	Bins       []string
	SetCapable map[string]bool
}

// ReceiversOf is the receivers packs make: every binary they install, in pack order, each marked
// set-capable when the program installing it declares provider_sets.
func ReceiversOf(packs []*packload.Pack) ProfileReceivers {
	var r ProfileReceivers
	for _, p := range packs {
		for _, bin := range p.InstallBins() {
			r.Bins = append(r.Bins, bin)
			if packload.HoldsProviderSets(packs, bin) {
				if r.SetCapable == nil {
					r.SetCapable = map[string]bool{}
				}
				r.SetCapable[bin] = true
			}
		}
	}
	return r
}

// ProfileFold is what FoldProfiles made of its selections: the table, and what a BARE list did
// in it (OQ-AP3), for the one launch line that rules says and for the declaration check, which
// must still read the entries a receiver ignores (AP-D3).
type ProfileFold struct {
	// Table is the CLI-name → active-set table: a string for a set of one, an array for more
	// (packload.ProfileSetWire), null for a CLI named with no selection.
	Table *jsonx.OrderedMap
	// BareList is the Default list of more than one entry that reached some receiver in Table,
	// nil when none did. Every Default reaches every receiver, so only the last selection's
	// can survive, and BareFrom is its index among the selections FoldProfiles was handed.
	BareList []string
	BareFrom int
	// Whole names the receivers that took BareList whole and Narrowed the ones that took its
	// first entry alone, each in receiver order. A receiver a later Named entry took is in
	// neither: its entry is not the list's.
	Whole, Narrowed []string
}

// FoldProfiles folds selections into the CLI-name → active-set table, LOWEST PRECEDENCE FIRST.
// Each selection sets its Default on every receiver, then its Named entries, so:
//
//   - within one selection a named CLI keeps its own entry and Default reaches every other
//     installed CLI (`-p bedrock -p pi=codex`, and `{"*": "bedrock", "pi": "codex"}`);
//   - a later selection's every form beats an earlier one's for each CLI it reaches: a launch
//     hands the config key first and the flag second, so any `-p` beats the key;
//   - a Default of more than one entry (a BARE list) reaches a set-capable receiver whole and
//     every other as its first entry alone (OQ-AP3), recorded in the fold for the launch to say.
//
// Default reaches the RECEIVERS only, never the command after `--` (the 2026-09-03 ruling). A
// Named entry is kept whether or not a receiver holds it, and whole: a key no pack installs is
// the validator's and checkProfileTargets' refusal, and a list named at a CLI that cannot hold
// it is OQ-AP2's, never a silent narrowing here. A Named entry with no profile is emitted as
// null, which every reader of the table (packload.ProfileTable) reads as no selection.
func FoldProfiles(recv ProfileReceivers, selections ...ProfileSelection) ProfileFold {
	fold := ProfileFold{Table: jsonx.NewOrderedMap(), BareFrom: -1}
	fromBare := map[string]bool{}
	for i, s := range selections {
		if len(s.Default) > 0 {
			fromBare = map[string]bool{}
			fold.BareList, fold.BareFrom = nil, -1
			if len(s.Default) > 1 {
				fold.BareList, fold.BareFrom = s.Default, i
			}
			for _, bin := range recv.Bins {
				set := s.Default
				if len(set) > 1 {
					fromBare[bin] = true
					if !recv.SetCapable[bin] {
						set = set[:1]
					}
				}
				fold.Table.Set(bin, packload.ProfileSetWire(set))
			}
		}
		for _, cli := range s.namedKeys() {
			delete(fromBare, cli)
			if set := s.Named[cli]; len(set) > 0 {
				fold.Table.Set(cli, packload.ProfileSetWire(set))
			} else {
				fold.Table.Set(cli, nil)
			}
		}
	}
	seen := map[string]bool{}
	for _, bin := range recv.Bins {
		if !fromBare[bin] || seen[bin] {
			continue
		}
		seen[bin] = true
		if recv.SetCapable[bin] {
			fold.Whole = append(fold.Whole, bin)
		} else {
			fold.Narrowed = append(fold.Narrowed, bin)
		}
	}
	if len(fold.Whole)+len(fold.Narrowed) == 0 {
		fold.BareList, fold.BareFrom = nil, -1
	}
	return fold
}

// ProfileTableFor is FoldProfiles' table: what every reader that says nothing about a bare list
// reads.
func ProfileTableFor(recv ProfileReceivers, selections ...ProfileSelection) *jsonx.OrderedMap {
	return FoldProfiles(recv, selections...).Table
}

// BareListNote is OQ-AP3's one launch line for fold's bare list, "" when it narrowed no
// receiver. keyed is the bare list's spelling, for the line's source and its remedy: the
// `profile` key's (true) or a bare `-p` (false).
func (fold ProfileFold) BareListNote(keyed bool) string {
	return packload.BareListNote(fold.BareList, fold.Whole, fold.Narrowed, keyed)
}

// ConfigProfileTable is the table a config alone selects over packs: its `profile` key folded
// with no flag above it. It is what every reader with no launch in hand reads (`yolo check`'s
// predictions, the selection closure of a host verb that launches nothing).
func ConfigProfileTable(cfg *jsonx.OrderedMap, packs []*packload.Pack) map[string]string {
	return packload.ProfileTable(ConfigProfileSets(cfg, packs))
}

// ConfigProfileSets is ConfigProfileTable before it is lowered to each CLI's primary: the table
// whose values are the whole active sets (packload.ProfileSets reads them), for a reader that
// asks about every entry (`yolo check`'s set predictions).
func ConfigProfileSets(cfg *jsonx.OrderedMap, packs []*packload.Pack) *jsonx.OrderedMap {
	return ProfileTableFor(ReceiversOf(packs), ConfigProfileSelection(cfg))
}

// ProfileKeySpelling is the `profile` key's JSON for a selection, for a message that tells the
// user what to write: the string (or list) form when the selection is a Default alone, the
// object form otherwise, "*" first. A set of one is spelled as its name, a longer one as a list.
// "" for an empty selection.
func ProfileKeySpelling(s ProfileSelection) string {
	if s.IsZero() {
		return ""
	}
	if len(s.Named) == 0 {
		out, _ := jsonx.DumpsCompact(profileSetSpelling(s.Default))
		return `"` + ProfileKey + `": ` + out
	}
	m := jsonx.NewOrderedMap()
	if len(s.Default) > 0 {
		m.Set(ProfileEveryAgent, profileSetSpelling(s.Default))
	}
	for _, cli := range s.namedKeys() {
		if set := s.Named[cli]; len(set) > 0 {
			m.Set(cli, profileSetSpelling(set))
		} else {
			m.Set(cli, nil)
		}
	}
	out, _ := jsonx.DumpsCompact(m)
	return `"` + ProfileKey + `": ` + out
}

// ProfileDeselection is the clause a launch appends where the profile selection that reached cli
// reaches nothing for it (packload.ProfileDisclosureInput.Deselect): where that selection came
// from, and the exact spelling that selects no profile for cli in that place (PP-D12 in
// docs/design/providers-and-profiles-redesign.md).
//
// key is the config `profile` key's selection (ConfigProfileSelection) and flag the launch's -p
// selection, which FoldProfiles folds above it, so a -p that reaches cli is the source. The
// warning used to name only `-p <cli>=<name>`, so a selection the key made repeated on every
// launch with no word of the key: a key-made one now names the key's file, line and column and
// the key respelled with a null for cli — null being the key's spelling of none — and a -p-made
// one the `-p <cli>=` pair. The location is read from the user scope's record (UserScopeSources,
// the key being user-scope only), and only here, on the warning's path; a scope read from no
// file is named without one.
func ProfileDeselection(key, flag ProfileSelection, cli string) string {
	if _, named := flag.Named[cli]; named || len(flag.Default) > 0 {
		return "the selection is this launch's `-p`, so add `-p " + cli + "=` to it"
	}
	return "the selection is " + profileKeyFix(key, cli, "launch") + ", or add `-p " + cli +
		"=` for one launch"
}

// HostProfileDeselection is ProfileDeselection for `yolo host --` and `yolo host env`, whose -p
// selects for its one command and refuses `-p <cli>=` ("names no profile"), so no -p spelling
// selects none there and the clause never offers one. typed is whether this command's -p made
// the selection; keyReaches whether the `profile` key selects a profile for cli without it.
//
// A key-made selection names the key's fix exactly as a jail launch does: the fold the host
// composes from (FoldProfiles over ConfigProfileSelection) reads the key's null for cli as none,
// so the spelling it prints stops the warning on every later command. A typed one is undone by
// leaving the -p out, and when the key would then select for cli too, the key's fix follows.
func HostProfileDeselection(key ProfileSelection, typed, keyReaches bool, cli string) string {
	if !typed {
		return "the selection is " + profileKeyFix(key, cli, "command")
	}
	out := "the selection is this command's `-p`, so leave it out"
	if keyReaches {
		out += "; without it the selection is " + profileKeyFix(key, cli, "command")
	}
	return out
}

// profileKeyFix is where the `profile` key's selection for cli was written and the instruction
// to write the key respelled with a null for cli, so it selects none for cli on every launch or
// command (every names which).
//
// IN A JAIL the user scope is a copy the host generated and mounted read-only (inherit.go), so
// "write it there" would name a file nothing in the jail can write: the clause names the copy it
// was read from and the host's user config as the place to write. A --user-layer file is the one
// user-scope input a jail's own caller writes (userlayer.go), so a selection located there keeps
// the plain form.
func profileKeyFix(key ProfileSelection, cli, every string) string {
	spelling := "`" + ProfileKeySpelling(withNoProfileFor(key, cli)) + "`"
	none := " to select none for " + cli + " on every " + every
	loc := ""
	if locs := UserScopeSources().Locations(profileKeyPathFor(key, cli)); len(locs) > 0 {
		loc = locs[0]
	}
	if inJail() && !locatedInUserLayer(loc) {
		where := "your host config's `" + ProfileKey + "` key"
		if loc != "" {
			where += ", which this jail reads from a read-only copy at " + loc
		}
		return where + ", so write " + spelling + " in your user config on the host" + none
	}
	where := "your config's `" + ProfileKey + "` key"
	if loc != "" {
		where += " at " + loc
	}
	return where + ", so write " + spelling + " there" + none
}

// locatedInUserLayer reports whether loc, an origin as Sources spells it (a file, then its line
// and column), is in the --user-layer file.
func locatedInUserLayer(loc string) bool {
	layer := UserLayerPath()
	if layer == "" || loc == "" {
		return false
	}
	file := tildePath(layer)
	return loc == file || strings.HasPrefix(loc, file+":")
}

// profileKeyPathFor is the validator path of the `profile` entry that reached cli: its own
// entry when the object names it, the "*" entry when the object names others, the key itself
// for the string or list form.
func profileKeyPathFor(key ProfileSelection, cli string) string {
	path := "config." + ProfileKey
	if _, named := key.Named[cli]; named {
		return path + "." + cli
	}
	if len(key.Named) > 0 {
		return path + "." + ProfileEveryAgent
	}
	return path
}

// withNoProfileFor is s with cli named and given no profile, every other entry kept.
func withNoProfileFor(s ProfileSelection, cli string) ProfileSelection {
	named := make(map[string][]string, len(s.Named)+1)
	for k, v := range s.Named {
		named[k] = v
	}
	named[cli] = nil
	return ProfileSelection{Default: s.Default, Named: named}
}

// profileSetSpelling is one set as the key writes it: its name for a set of one, a list for more.
func profileSetSpelling(set []string) any {
	if len(set) == 1 {
		return set[0]
	}
	out := make([]any, len(set))
	for i, name := range set {
		out[i] = name
	}
	return out
}
