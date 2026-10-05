package packdecl

// describes_test.go pins `describes` (Contribution.Describes, docs/design/boundary-broker.md
// BB-D69): accepted on briefing CONTENT naming a kind the pack declares, refused everywhere else,
// and its unknown-kind and sibling halves held to the strict path so a jail reading a newer
// pack's manifest still boots.

import (
	"reflect"
	"strings"
	"testing"
)

// interceptJSON is a well-formed sibling for the briefing to describe.
const interceptJSON = `{"kind":"intercept","bin":"gh","forward":["yolo","gh","--"]}`

func TestDescribesIsAcceptedOnBriefingContent(t *testing.T) {
	for _, raw := range []string{
		// The github pack's shape: a declared broadcast naming one file.
		`{"kind":"briefing","from":"briefing/gh.md","describes":["intercept"]},` + interceptJSON,
		// Addressed and path-named content take it too: the gate is about the prose, not its route.
		`{"kind":"briefing","from":"briefing/gh.md","agents":["claude"],"describes":["intercept"]},` + interceptJSON,
		`{"kind":"briefing","from":"briefing/gh.md","into":".acme/A.md","describes":["intercept"]},` + interceptJSON,
		// An omitted `from` governs the convention's unnamed files, and gates them.
		`{"kind":"briefing","describes":["intercept"]},` + interceptJSON,
	} {
		if probs := decodeOne(t, raw); probs != "" {
			t.Errorf("%s must validate, got %q", raw, probs)
		}
	}
	m, probs := Decode([]byte(`{"name":"acme","contributes":[` +
		`{"kind":"briefing","from":"briefing/gh.md","describes":["intercept"]},` + interceptJSON + `]}`))
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	if got := m.Contributions()[0].Describes; !reflect.DeepEqual(got, []Kind{KindIntercept}) {
		t.Errorf("Describes = %v, want [intercept]", got)
	}
}

func TestDescribesIsRefusedOffBriefingContent(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`{"kind":"skills","describes":["intercept"]},` + interceptJSON,
			`kind "skills" does not take "describes"`},
		{`{"kind":"files","from":"tree","agents":["claude"],"describes":["intercept"]},` + interceptJSON,
			`kind "files" does not take "describes"`},
		{`{"kind":"intercept","bin":"gh","forward":["yolo","gh","--"],"describes":["intercept"]}`,
			`kind "intercept" does not take "describes"`},
		// P5: a destination ships no prose, so there is nothing to withhold.
		{`{"kind":"briefing","agent":"acme","into":".acme/A.md","describes":["intercept"]},` + interceptJSON,
			`a briefing DESTINATION (agent "acme") takes no "describes"`},
		{`{"kind":"briefing","from":"briefing/a.md","describes":["briefing"]}`,
			`describes[0]: "briefing" — a briefing cannot be about itself`},
		{`{"kind":"briefing","from":"briefing/a.md","describes":[""]}`,
			`describes[0]: empty kind`},
		{`{"kind":"briefing","from":"briefing/a.md","describes":["intercpt"]}`,
			`contributes[0].describes[0]: unknown kind "intercpt"`},
		// The sibling half: the kinds are the pack's OWN.
		{`{"kind":"briefing","from":"briefing/a.md","describes":["intercept"]}`,
			`contributes[0].describes[0]: "intercept" — this pack declares no "intercept" contribution`},
	} {
		if probs := decodeOne(t, tc.raw); !strings.Contains(probs, tc.want) {
			t.Errorf("%s: want a problem containing %q, got %q", tc.raw, tc.want, probs)
		}
	}
}

// THE VERSION BOUNDARY. The in-jail reader is DecodeTolerant, and the boot treats any problem as
// fatal, so the two halves that need the whole manifest or this build's kind list stay off it:
// a kind a newer build adds, named by a pack it ships, must not brick an older jail's boot. The
// placement refusals both builds agree about stay on both paths.
func TestDescribesUnknownAndUndeclaredKindsAreStrictOnly(t *testing.T) {
	raw := []byte(`{"name":"acme","contributes":[` +
		`{"kind":"briefing","from":"briefing/a.md","describes":["from-a-newer-yolo"]},` +
		`{"kind":"briefing","from":"briefing/b.md","describes":["intercept"]}]}`)
	if _, probs := Decode(raw); len(probs) != 2 {
		t.Errorf("the strict path must refuse both, got %q", probs)
	}
	if _, probs, _ := DecodeTolerant(raw); len(probs) != 0 {
		t.Errorf("the tolerant path must pass both, got %q", probs)
	}
	misplaced := []byte(`{"name":"acme","contributes":[{"kind":"skills","describes":["intercept"]}]}`)
	if _, probs, _ := DecodeTolerant(misplaced); len(probs) != 1 ||
		!strings.Contains(probs[0], `does not take "describes"`) {
		t.Errorf("a misplaced describes is refused on the tolerant path too, got %q", probs)
	}
}
