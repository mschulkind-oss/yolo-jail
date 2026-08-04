package render

// notchnames_test.go pins the ONE remaining place core spells a notch's name against the
// config boundary that owns the vocabulary (plan §6c step 3).
//
// The refactor's claim is "core reasons about primitives; only the config boundary knows the
// names". That leaves exactly two name tables — config.KnownConfinements (parsing) and this
// package's kindNames (the inbound lookup plus output labels) — and a claim of one vocabulary
// spread over two tables is only true if something checks. Nothing did: a notch added to
// config alone would parse, resolve to no Kind, and take whatever the caller does with
// ok=false; added to render alone it would be unselectable and silently unreachable.
//
// It imports internal/config, which is safe in exactly one direction: config does NOT import
// render (checked — render sits below it, on packdecl + paths), so this test-only edge cannot
// become a cycle. If config ever needs render, this test moves, not the tables.

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// Every config-selectable confinement value resolves to a DISTINCT selectable Kind, and every
// selectable Kind is reachable from a config value. Both directions, because each catches a
// different half of the drift: the first is "a name nobody can act on", the second is "a notch
// nobody can select".
func TestNotchNamesMatchTheConfigVocabulary(t *testing.T) {
	if len(config.KnownConfinements) != len(SelectableNotches()) {
		t.Fatalf("config knows %d confinement levels, render has %d selectable Kinds — one of "+
			"the two tables gained a notch without the other (config.KnownConfinements, "+
			"render.SelectableNotches)", len(config.KnownConfinements), len(SelectableNotches()))
	}
	seen := map[Kind]string{}
	for _, c := range config.KnownConfinements {
		k, ok := KindForNotch(string(c))
		if !ok {
			t.Errorf("confinement %q is a config value with no render.Kind — nothing downstream "+
				"can reason about it; add it to render.kindNames and SelectableNotches", c)
			continue
		}
		if prev, dup := seen[k]; dup {
			t.Errorf("confinement %q and %q both resolve to Kind %s — two notches sharing a Kind "+
				"means one of them silently inherits the other's policy", c, prev, k)
		}
		seen[k] = string(c)
		// And back out again: the label a message prints must be the name the user configured,
		// or the report attributes a render to a level the user never named.
		if k.String() != string(c) {
			t.Errorf("Kind %d labels itself %q but its config value is %q", k, k.String(), c)
		}
	}
}

// The non-selectable Kinds must NOT be nameable from config, and must still have a label. Both
// halves matter: `confinement: "preview"` would select a target that writes to a scratch dir
// (so a real render would silently write nothing), and `confinement: "unset"` would select the
// absence of a choice. Meanwhile both can reach OUTPUT, so a missing label is a blank in a
// message rather than a caught error.
func TestNonSelectableKindsAreLabelledButNotSelectable(t *testing.T) {
	for _, k := range []Kind{KindUnset, KindPreview} {
		if got, ok := KindForNotch(kindNames[k]); ok {
			t.Errorf("%q is selectable as a confinement level (resolved to %s) — it is not a "+
				"notch on the dial", kindNames[k], got)
		}
		if k.String() == "" {
			t.Errorf("Kind %d has no label, so a message about it prints a blank", k)
		}
	}
}

// An unknown name fails CLOSED: no Kind, and the zero value it returns is KindUnset — which is
// not a notch (see its doc), so a caller that ignores ok gets the most restricted answer rather
// than the strongest one.
func TestKindForNotchRejectsAnUnknownName(t *testing.T) {
	k, ok := KindForNotch("vm")
	if ok {
		t.Errorf("an unknown notch name must not resolve, got %s", k)
	}
	if k != KindUnset {
		t.Errorf("a rejected lookup must return KindUnset (the fail-closed value), got %s", k)
	}
	if ProfileFor(k).AgentAutonomy {
		t.Error("the fail-closed Kind must not carry agent autonomy — an unknown notch name " +
			"would grant a pack's permission bypass")
	}
}
