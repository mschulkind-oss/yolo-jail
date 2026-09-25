package agentcfg

// rejectedvalues.go repairs ONE THING: a value yolo itself shipped that the target program
// CANNOT LOAD, frozen into a user's own config state by a render that happened before the
// pack was fixed.
//
// ── WHAT THIS IS NOT, AND MUST NEVER BECOME ────────────────────────────────────────────
//
// It is NOT "yolo re-writes a key it once wrote as a default when the default changes".
// That is a different mechanism, it is BLOCKED BY RULING, and the ruling is one line up the
// file: LayerAsserted (compose.go) deliberately excludes `defaults` because a default is
// FILL-IF-ABSENT — "yolo writes a default once and then never touches the key, so the moment
// it is in the file its value is the user's to change … A dropped default is not yolo's
// output to retire." Converting `defaults` from write-once to write-when-changed would
// silently overwrite real choices, and nothing here may be generalized in that direction.
//
// It is also NOT a predicate over "values the agent rejects". yolo cannot enumerate what pi
// accepts — a theme can come from `~/.pi/agent/themes/`, so even the builtin list is not the
// answer — so the closed list below is the only implementable shape. Each entry is one
// measured fact about one value, auditable and deletable.
//
// ── WHY THIS IS DIFFERENT IN KIND ───────────────────────────────────────────────────────
//
// `"theme": "system"` is not a preference anyone could hold: pi ships `dark` and `light` as
// builtins, `getAvailableThemes()` has no `system`, and no theme directory supplies one, so
// every launch died with `Error: Failed to load theme "system": Theme not found: system`.
// Removing a value the TARGET REJECTS is not overriding a user's choice, because there is no
// choice there to override — and that distinction is the whole justification for this file.
// An entry that does not carry it does not belong here.
//
// ── THE TWO GUARDS ─────────────────────────────────────────────────────────────────────
//
//  1. THE VALUE MUST MATCH EXACTLY. An entry names one value. A user who chose `dracula`,
//     or whose host layer says `light/dark-readable`, is untouched — that is what keeps this
//     from being the blocked mechanism above.
//  2. THE PACK MUST ALREADY BE FIXED. RejectedValuesFor fires only when the surface's own
//     `defaults` still declares the key AND declares something OTHER than the rejected
//     value. A pack that still ships the bad default would have the repair hand the key
//     straight back to `defaults`, which would re-supply the same unloadable value — churn,
//     a boot notice every launch, and no fix. So the repair is INERT until the manifest is
//     corrected, which also means the fix lives in exactly one place: the pack.
//
// ── THE REPAIR IS A DELETE, WHICH IS THE SUBTLE HALF ───────────────────────────────────
//
// It REMOVES the key from the state that froze it; it never writes the replacement. Writing
// `light/dark` explicitly would put the value back in a layer that outranks `defaults`
// (`host` on an rmw file, the capture overlay in a jail) and freeze it there forever — the
// NEXT default change would not reach the user either, which is the original bug reproduced
// one value later. Deleting the key lets `defaults` fill it by absence and stay
// authoritative, and the provenance record then reads `defaults` rather than `host`/`overlay`.
//
// ── HOW AN ENTRY IS RETIRED ────────────────────────────────────────────────────────────
//
// Delete it. An entry is a transitional fact about homes rendered before a specific commit,
// so it is retired once every home that could still be carrying the value has booted at
// least once with the repair in place. There is no sentinel and no expiry date: the
// mechanism is self-idempotent (a state that no longer holds the value produces no repair),
// so an entry left in place too long costs one map lookup per surface per render, and an
// entry removed too early simply leaves the remaining homes frozen as they were. Retiring is
// a one-line deletion plus its test, with no migration of its own.
//
// ⚠ If the pack's default for a repaired key moves AGAIN before the entry is retired, the
// entry keeps working (guard 2 asks only that the default differ from the rejected value) —
// but the boot notice names whatever the manifest now declares, so nothing here has to be
// updated in step with it.

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonptr"
)

// RejectedValue is one entry of the closed list: a value yolo shipped for a key of a
// surface, which the program that reads the surface cannot load.
type RejectedValue struct {
	// Agent and Surface identify the surface, matching manifest.Surface's own fields.
	Agent   string
	Surface string
	// Key is the TOP-LEVEL key, matching the granularity every other per-key mechanism
	// here uses (Compose's provenance, the promote verb, rmwProvenance).
	Key string
	// Value is the exact rejected value. Compared with reflect.DeepEqual against the
	// decoded layer value, so a JSON string compares as a string and nothing coerces.
	Value any
	// Why is the measured reason the target rejects it, printed in the boot notice. It is
	// the part a user needs to accept the mutation, so it names the program's own failure
	// rather than yolo's intent.
	Why string
}

// rejectedValues IS THE CLOSED LIST. One line per measured fact; see the file header for
// what qualifies and how an entry is retired.
var rejectedValues = []RejectedValue{
	{
		Agent: "pi", Surface: "settings", Key: "theme", Value: "system",
		Why: `pi ships only "dark" and "light" as builtin themes and getAvailableThemes() ` +
			`has no "system", so every launch failed with ` +
			`'Failed to load theme "system": Theme not found: system'`,
	},
}

// RejectedListEntry is one entry of the closed list for list paths: an array entry yolo shipped
// for a list path of a surface, which the program that reads the surface cannot load (e.g.
// conflicts with a replacement extension package).
type RejectedListEntry struct {
	Agent   string
	Surface string
	Path    string // JSON pointer, e.g. "/packages"
	Value   any    // exact value to match, e.g. "npm:@quintinshaw/pi-dynamic-workflows"
	Why     string
}

// rejectedListEntries IS THE CLOSED LIST for list paths.
var rejectedListEntries = []RejectedListEntry{
	{
		Agent: "pi", Surface: "settings", Path: "/packages",
		Value: "npm:@quintinshaw/pi-dynamic-workflows",
		Why: `conflicts with git:github.com/mschulkind/pi-dynamic-workflows ` +
			`(duplicate tool registrations "workflow" and "workflow_control") ` +
			`causing pi startup to fail`,
	},
	{
		Agent: "pi", Surface: "settings", Path: "/packages",
		Value: "npm:pi-subagents",
		Why: `conflicts with git:github.com/mschulkind/pi-subagents ` +
			`(duplicate tool registrations "subagent", "bg_wait", and "subagents_enable") ` +
			`causing pi startup to fail`,
	},
}

// RejectedListEntriesFor returns the rejected list entries for surface s.
func RejectedListEntriesFor(s manifest.Surface) []RejectedListEntry {
	var out []RejectedListEntry
	for _, rle := range rejectedListEntries {
		if rle.Agent == s.Agent && rle.Surface == s.Name {
			out = append(out, rle)
		}
	}
	return out
}

// RejectedValuesFor is the entries that apply to this surface AND whose pack has already
// been fixed — guard 2 of the file header. It returns nothing for every surface with no
// entry, which is every surface but one.
func RejectedValuesFor(s manifest.Surface) []RejectedValue {
	defaults, isMap := s.Defaults.(map[string]any)
	if !isMap {
		return nil
	}
	var out []RejectedValue
	for _, rv := range rejectedValues {
		if rv.Agent != s.Agent || rv.Surface != s.Name {
			continue
		}
		declared, present := defaults[rv.Key]
		if !present || reflect.DeepEqual(declared, rv.Value) {
			// No default to fall back to, or the pack still ships the rejected value:
			// deleting the key would only have `defaults` re-supply it.
			continue
		}
		out = append(out, rv)
	}
	return out
}

// Repair is one applied repair, for the caller to report. Nothing here reads it back.
type Repair struct {
	RejectedValue
	// Replacement is what the surface's `defaults` layer now declares for the key —
	// read from the manifest rather than stored beside the rejected value, so the notice
	// cannot state a replacement the pack no longer ships.
	Replacement any
	// Path is the list path (e.g. "/packages") when this repair was for a list entry,
	// or empty for top-level key repairs.
	Path string
}

// Describe is the notice for one repair, and it is REQUIRED reading rather than decoration:
// this is a one-shot mutation of the user's own config state, so the line has to say what was
// removed, who put it there, why it could not stay, and what supplies the key now.
//
// `verb` and `where` are the caller's own vocabulary: the two notches that WRITE say
// "removed" and name the state they removed it from ("the file", "the captured in-jail
// edits"), and the host's dry-run posture says "would remove" about the same file. The verb is
// a parameter rather than baked in because a preview that claimed a mutation had happened
// would be the one lie a dry run cannot afford — and the rest of the sentence is tense-free on
// purpose, so no tense can be got wrong twice.
//
// One sentence-builder for every call site, so no two notches describe the same mutation
// differently.
func (r Repair) Describe(verb, where string) string {
	if r.Path != "" {
		return r.Agent + "/" + r.Surface + ": " + verb + " " + r.Path + " entry " + literal(r.Value) +
			" from " + where + " — " + r.Why + ". With the entry removed, pack contributions supply it"
	}
	return r.Agent + "/" + r.Surface + ": " + verb + " " + r.Key + " = " + literal(r.Value) +
		" from " + where + " — yolo shipped that value as a default and " + r.Why +
		". With the key unset the pack default (" + literal(r.Replacement) +
		") supplies it; nothing you chose was changed, and a later change to that default " +
		"will reach you"
}

// RepairRejectedListRecords removes rejected list entries from the per-entry capture records
// (lc.recs) and from the overlay map (if an older whole-array capture still holds them).
func RepairRejectedListRecords(s manifest.Surface, recs map[string]ListRecord, overlay map[string]any) (map[string]any, []Repair) {
	entries := RejectedListEntriesFor(s)
	if len(entries) == 0 {
		return overlay, nil
	}
	var done []Repair
	for _, rle := range entries {
		repaired := false
		if recs != nil {
			if rec, ok := recs[rle.Path]; ok {
				if containsEntry(rec.Add, rle.Value) {
					rec.Add = withoutEntry(rec.Add, rle.Value)
					recs[rle.Path] = rec
					repaired = true
				}
			}
		}
		if overlay != nil {
			tokens, err := jsonptr.Parse(rle.Path)
			if err == nil && len(tokens) > 0 {
				v, st, _ := walkPath(overlay, tokens)
				if st == pathFound {
					if arr, ok := v.([]any); ok && containsEntry(arr, rle.Value) {
						cleaned := withoutEntry(arr, rle.Value)
						if len(cleaned) == 0 {
							overlay = deletePath(overlay, tokens, pruneAlways)
						} else {
							overlay = setPath(overlay, tokens, cleaned)
						}
						repaired = true
					}
				}
			}
		}
		if repaired {
			done = append(done, Repair{
				RejectedValue: RejectedValue{
					Agent:   rle.Agent,
					Surface: rle.Surface,
					Value:   rle.Value,
					Why:     rle.Why,
				},
				Path: rle.Path,
			})
		}
	}
	return overlay, done
}

// literal renders a value the way the surface's own JSON would, so a notice about a string
// shows the quotes that make it a string.
func literal(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(data)
}

// MutableObject is the whole contract a repair needs of the state it repairs: read a
// top-level key, delete a top-level key.
//
// It is an interface because the two states that can freeze a value are different Go types
// — the capture overlay is a plain map[string]any inside ComposeStateful, and an rmw
// render's file is a *jsonx.OrderedMap — and one repair pass over both is the only way the
// closed list above has a single reader. *jsonx.OrderedMap satisfies it as it stands.
type MutableObject interface {
	Get(key string) (any, bool)
	Delete(key string)
}

// RepairRejected deletes from obj every rejected value this surface declares an entry for,
// and returns what it removed. It MUTATES obj.
//
// A nil obj, or a surface with no entry, is a no-op returning nil — so a caller may invoke
// it unconditionally, which is what keeps the two call sites from growing a condition of
// their own that could go stale against this list.
//
// IDEMPOTENT BY CONSTRUCTION, with no state of its own: the second run finds the key gone
// (or holding something else) and removes nothing. That is also why it can sit on the
// per-render path rather than behind a one-shot sentinel — a sentinel would have to be
// per-surface, per-notch, per-workspace state, and getting it wrong in either direction is
// worse than a map lookup.
func RepairRejected(s manifest.Surface, obj MutableObject) []Repair {
	if obj == nil {
		return nil
	}
	defaults, _ := s.Defaults.(map[string]any)
	var done []Repair
	for _, rv := range RejectedValuesFor(s) {
		got, present := obj.Get(rv.Key)
		if !present || !reflect.DeepEqual(got, rv.Value) {
			continue
		}
		obj.Delete(rv.Key)
		done = append(done, Repair{RejectedValue: rv, Replacement: defaults[rv.Key]})
	}
	return done
}

// MapObject adapts a plain decoded object to MutableObject, for the capture overlay.
type MapObject map[string]any

// Get implements MutableObject.
func (m MapObject) Get(key string) (any, bool) { v, ok := m[key]; return v, ok }

// Delete implements MutableObject.
func (m MapObject) Delete(key string) { delete(m, key) }
