package packdecl

// warnsabovechars_test.go pins `warns_above_chars` (Contribution.WarnsAboveChars,
// docs/reference/agent-briefings.md BD-D9): read from a briefing destination, refused on every other
// contribution, and refused when it is not a positive count, each refusal naming the fix.

import (
	"strings"
	"testing"
)

func TestWarnsAboveCharsIsReadFromABriefingDestination(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"briefing","agent":"acme","into":".acme/AGENTS.md","warns_above_chars":40000}]}`))
	if len(probs) != 0 {
		t.Fatalf("a well-formed declaration was refused: %v", probs)
	}
	var got *int
	for _, c := range m.Contributions() {
		if c.Kind == KindBriefing {
			got = c.WarnsAboveChars
		}
	}
	if got == nil || *got != 40000 {
		t.Fatalf("WarnsAboveChars = %v, want 40000", got)
	}
}

// The claude pack writes it in TOML, the spelling every shipped pack uses.
func TestWarnsAboveCharsIsReadFromTOML(t *testing.T) {
	m, probs := DecodeTOML([]byte("name = \"acme\"\n\n[[briefing]]\nagent = \"acme\"\n" +
		"into = \".acme/AGENTS.md\"\nwarns_above_chars = 40000\n"))
	if len(probs) != 0 {
		t.Fatalf("a well-formed TOML declaration was refused: %v", probs)
	}
	found := false
	for _, c := range m.Contributions() {
		if c.Kind == KindBriefing {
			found = c.WarnsAboveChars != nil && *c.WarnsAboveChars == 40000
		}
	}
	if !found {
		t.Fatal("warns_above_chars = 40000 was not read from a TOML briefing destination")
	}
}

func TestWarnsAboveCharsRefusals(t *testing.T) {
	for name, tc := range map[string]struct{ entry, want string }{
		"zero":       {`{"kind":"briefing","agent":"a","into":".a/AGENTS.md","warns_above_chars":0}`, "a positive character count"},
		"negative":   {`{"kind":"briefing","agent":"a","into":".a/AGENTS.md","warns_above_chars":-1}`, "remove the field"},
		"on content": {`{"kind":"briefing","agents":["a"],"warns_above_chars":40000}`, "briefing DESTINATION"},
		"on skills":  {`{"kind":"skills","agent":"a","into":".a/skills","warns_above_chars":40000}`, `does not take "warns_above_chars"`},
	} {
		t.Run(name, func(t *testing.T) {
			_, probs := Decode([]byte(`{"name":"acme","contributes":[` + tc.entry + `]}`))
			if !strings.Contains(strings.Join(probs, "; "), tc.want) {
				t.Errorf("want a problem containing %q, got %v", tc.want, probs)
			}
		})
	}
}
