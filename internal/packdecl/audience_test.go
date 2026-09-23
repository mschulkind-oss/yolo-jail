package packdecl

// audience_test.go pins the `agent`/`agents` pair docs/reference/agent-briefings.md#audiences-what-varies-per-destination adds to the manifest:
// the identity a DESTINATION declares for itself, and the AUDIENCE a contribution names instead
// of a path.
//
// The rule the whole field rests on is that `into` and `agents` are two answers to ONE question
// and an entry gives exactly one — so these tests are as much about what stays refused as about
// what newly validates. A pack that could declare both would be a content pack asserting
// ".claude/CLAUDE.md", which is the coupling the field exists to delete.

import (
	"strings"
	"testing"
)

// decodeOne runs one contributes entry through the strict authoring path and joins its problems.
func decodeOne(t *testing.T, raw string) string {
	t.Helper()
	_, probs := Decode([]byte(`{"name":"acme","contributes":[` + raw + `]}`))
	return strings.Join(probs, "; ")
}

// THE ADDRESSED SHAPE VALIDATES: an audience and no path. This is the one a user's own content
// pack writes, and until this change the validator refused it outright (`kind "briefing" needs
// "into"`), which is why it can only be reached by a pack yolo loads with a CURRENT binary —
// see TestShippedAgentPacksKeepIntoForSkew for the other half of that boundary.
func TestAudienceWithoutIntoValidates(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"briefing","agents":["claude"]}`,
		`{"kind":"briefing","from":"prose/claude.md","agents":["claude","pi"]}`,
		`{"kind":"skills","agents":["claude"]}`,
		`{"kind":"files","from":"tree","agents":["claude"]}`,
	} {
		if probs := decodeOne(t, raw); probs != "" {
			t.Errorf("an addressed contribution %s must validate with no `into`, got %q", raw, probs)
		}
	}
}

// THE DESTINATION SHAPE VALIDATES: the six shipped agent packs' new line, `agent` beside `into`.
func TestDestinationIdentityValidates(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"briefing","into":".claude/CLAUDE.md","agent":"claude"}`,
		`{"kind":"skills","into":".claude/skills","agent":"claude"}`,
		`{"kind":"files","into":".pi/agent/extensions","agent":"pi"}`,
	} {
		if probs := decodeOne(t, raw); probs != "" {
			t.Errorf("a destination declaring its identity %s must validate, got %q", raw, probs)
		}
	}
}

// `into` STAYS REQUIRED FOR A DESTINATION. P2 (docs/reference/pack-system.md#briefing-p2) made route-less
// briefing and skills CONTENT a broadcast, so the rule this pins moved: the conditional must not
// have widened into "into is optional now" for the other role. A destination (`agent` set) IS
// its path — with no `into` it names a landing place that does not exist — so it stays refused,
// and `files` content, which cannot broadcast (pack-system.md#briefing-non-goals), still needs a route.
func TestIntoStillRequiredWithoutAnAudience(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"briefing","agent":"claude"}`,
		`{"kind":"skills","agent":"claude"}`,
		`{"kind":"files","agent":"pi"}`,
	} {
		if probs := decodeOne(t, raw); !strings.Contains(probs, `needs "into"`) {
			t.Errorf("destination %s must still be refused for a missing `into`, got %q", raw, probs)
		}
	}
	if probs := decodeOne(t, `{"kind":"files","from":"tree"}`); !strings.Contains(probs, `needs "into" or "agents"`) {
		t.Errorf(`route-less files content must be refused, naming both routes, got %q`, probs)
	}
}

// `agent` BESIDE `agents` WITH NO `into` validated before P5 and is refused now, so its refusal
// names both readings of what the author meant instead of a bare `needs "into"`.
func TestAgentBesideAgentsNamesBothReadings(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"briefing","agent":"claude","agents":["pi"]}`,
		`{"kind":"skills","agent":"claude","agents":["pi"]}`,
		`{"kind":"files","from":"tree","agent":"pi","agents":["pi"]}`,
	} {
		probs := decodeOne(t, raw)
		for _, want := range []string{`needs "into"`, `drop "agent"`, `give it "into" and drop "agents"`} {
			if !strings.Contains(probs, want) {
				t.Errorf("%s: refusal must contain %q, got %q", raw, want, probs)
			}
		}
	}
}

// ROUTE-LESS BRIEFING AND SKILLS CONTENT IS A BROADCAST (P2), so it validates: silence means
// every destination of the kind, in a manifest as well as without one.
func TestRoutelessContentIsABroadcast(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"briefing"}`,
		`{"kind":"briefing","from":"prose/all.md"}`,
		`{"kind":"skills"}`,
		`{"kind":"skills","from":"skills"}`,
	} {
		if probs := decodeOne(t, raw); probs != "" {
			t.Errorf("route-less content %s is a broadcast and must validate, got %q", raw, probs)
		}
	}
}

// `into` AND `agents` TOGETHER ARE REFUSED (agent-briefings.md#the-two-halves-and-why-neither-knows-the-others-business, #ba-p4). The content pack that wrote both would
// hardcode a path only the agent pack can keep current, which is exactly the coupling the
// selector replaces — and it would also be ambiguous, since the declaration and the inference
// would each name a destination.
func TestIntoAndAgentsTogetherAreRefused(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"briefing","into":".claude/CLAUDE.md","agents":["claude"]}`,
		`{"kind":"skills","into":".claude/skills","agents":["claude"]}`,
		`{"kind":"files","from":"tree","into":".pi/agent/extensions","agents":["pi"]}`,
	} {
		probs := decodeOne(t, raw)
		if !strings.Contains(probs, `takes "into" or "agents", not both`) {
			t.Errorf("%s must be refused for naming both a path and an audience, got %q", raw, probs)
		}
	}
}

// A files DESTINATION CARRIES NO `from` — the slot/content split pi-pack-extensions.md §3 draws.
// A destination that also ships a tree is the overload that made the slot a mount with addressed
// mounts nested inside it, so it is refused, with the addressed spelling as the fix.
func TestFilesDestinationRejectsFrom(t *testing.T) {
	probs := decodeOne(t, `{"kind":"files","agent":"pi","from":"extensions","into":".pi/agent/extensions"}`)
	if !strings.Contains(probs, `takes no "from"`) {
		t.Errorf("a files destination carrying `from` must be refused, got %q", probs)
	}
	// And the addressed spelling of the same content validates.
	if got := decodeOne(t, `{"kind":"files","agents":["pi"],"from":"extensions"}`); got != "" {
		t.Errorf("the addressed spelling must validate, got %q", got)
	}
}

// REFUSED ON EVERY OTHER KIND, in `profile`'s position and for `profile`'s reason: no consumer
// reads either field there, so accepting it would ship a declaration that silently does nothing.
func TestAudienceFieldsRefusedOnOtherKinds(t *testing.T) {
	refused := []struct{ raw, field string }{
		{`{"kind":"program","bin":"claude","via":"npm","package":"c","agent":"claude"}`, "agent"},
		{`{"kind":"program","bin":"claude","via":"npm","package":"c","agents":["claude"]}`, "agents"},
		{`{"kind":"requires","bin":"claude","agent":"claude"}`, "agent"},
		{`{"kind":"state","at":".acme","agents":["claude"]}`, "agents"},
		{`{"kind":"env","vars":{"A":"1"},"agent":"claude"}`, "agent"},
	}
	for _, tc := range refused {
		probs := decodeOne(t, tc.raw)
		if !strings.Contains(probs, `does not take "`+tc.field+`"`) {
			t.Errorf("%s must be refused for %q, got %q", tc.raw, tc.field, probs)
		}
	}
}

// THE AUDIENCE NAMESPACE IS THE BIN NAMESPACE (OQ-BA1), so it gets the bin namespace's guard.
// The values are the launcher command `-p <name> -- <bin>` already keys on; a value carrying
// path structure has misnamed something, and letting the two namespaces accept different strings
// is how they come to mean different things.
func TestAudienceValuesMustBeBareProgramNames(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"briefing","into":".claude/CLAUDE.md","agent":"bin/claude"}`,
		`{"kind":"briefing","into":".claude/CLAUDE.md","agent":".."}`,
		`{"kind":"briefing","agents":["claude","pi/agent"]}`,
		`{"kind":"skills","agents":["a:b"]}`,
	} {
		if probs := decodeOne(t, raw); !strings.Contains(probs, "must be a bare program name") {
			t.Errorf("%s must be refused as a non-bare name, got %q", raw, probs)
		}
	}
	// An EMPTY entry gets its own message rather than binProblem's, which treats "" as the
	// required-field check's business — and here there is no required-field check to defer to.
	if probs := decodeOne(t, `{"kind":"briefing","agents":[""]}`); !strings.Contains(
		probs, "empty agent name") {
		t.Errorf("an empty agents entry must be refused, got %q", probs)
	}
}
