package packdecl

// audience_test.go pins the `agent`/`agents` pair briefing-audiences.md adds to the manifest:
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
	} {
		if probs := decodeOne(t, raw); probs != "" {
			t.Errorf("a destination declaring its identity %s must validate, got %q", raw, probs)
		}
	}
}

// `into` STAYS REQUIRED for a contribution that names no audience. This is the rule the
// conditional must not have widened into "into is optional now": an unaudienced briefing with no
// destination is the delivered-nowhere failure, and it has to stay unrepresentable.
func TestIntoStillRequiredWithoutAnAudience(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"briefing"}`,
		`{"kind":"skills","from":"skills"}`,
		// `files` takes no audience at all (see below), so `agents` must not make its `into`
		// optional either — otherwise one refusal would quietly disable another.
		`{"kind":"files","from":"tree","agents":["claude"]}`,
	} {
		if probs := decodeOne(t, raw); !strings.Contains(probs, `needs "into"`) {
			t.Errorf("%s must still be refused for a missing `into`, got %q", raw, probs)
		}
	}
}

// `into` AND `agents` TOGETHER ARE REFUSED (§4.1, P4). The content pack that wrote both would
// hardcode a path only the agent pack can keep current, which is exactly the coupling the
// selector replaces — and it would also be ambiguous, since the declaration and the inference
// would each name a destination.
func TestIntoAndAgentsTogetherAreRefused(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"briefing","into":".claude/CLAUDE.md","agents":["claude"]}`,
		`{"kind":"skills","into":".claude/skills","agents":["claude"]}`,
	} {
		probs := decodeOne(t, raw)
		if !strings.Contains(probs, `takes "into" or "agents", not both`) {
			t.Errorf("%s must be refused for naming both a path and an audience, got %q", raw, probs)
		}
	}
}

// REFUSED ON EVERY OTHER KIND, in `profile`'s position and for `profile`'s reason: no consumer
// reads either field there, so accepting it would ship a declaration that silently does nothing.
func TestAudienceFieldsRefusedOnOtherKinds(t *testing.T) {
	refused := []struct{ raw, field string }{
		{`{"kind":"program","bin":"claude","via":"npm","package":"c","agent":"claude"}`, "agent"},
		{`{"kind":"program","bin":"claude","via":"npm","package":"c","agents":["claude"]}`, "agents"},
		{`{"kind":"files","from":"tree","into":"x","agents":["claude"]}`, "agents"},
		{`{"kind":"launch","bin":"claude","flags":["--x"],"agent":"claude"}`, "agent"},
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
