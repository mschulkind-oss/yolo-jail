package packdecl

import (
	"strings"
	"testing"
)

// decodeSupersedes decodes a pack manifest body, returning the problems.
func decodeSupersedes(t *testing.T, body string) (*Manifest, []string) {
	t.Helper()
	m, problems := Decode([]byte(body))
	return m, problems
}

// TestSupersedesDecodes is the happy path, and it also pins that `supersedes` is a
// TOP-LEVEL key: written beside `name`, not inside `contributes`.
func TestSupersedesDecodes(t *testing.T) {
	m, problems := decodeSupersedes(t, `{
	  "name": "claude-bedrock",
	  "supersedes": [
	    {"capability": "claude-oauth-refresh",
	     "because": "Bedrock overrides the OAuth path; no token is ever refreshed"}
	  ]
	}`)
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	got := m.Supersessions()
	if len(got) != 1 {
		t.Fatalf("Supersessions() = %v, want one entry", got)
	}
	if got[0].Capability != "claude-oauth-refresh" {
		t.Errorf("Capability = %q", got[0].Capability)
	}
	if !strings.Contains(got[0].Because, "no token is ever refreshed") {
		t.Errorf("Because = %q", got[0].Because)
	}
}

// TestSupersedesRequiresBecause is THE asymmetry, enforced. `serves` is a bare
// string list because it is a statement about yourself; `supersedes` is a claim that
// somebody else's job does not need doing, and it costs a reason. The message has to
// teach that rather than just report a missing field, because an author who omits it
// is reasoning by analogy with `serves`.
func TestSupersedesRequiresBecause(t *testing.T) {
	_, problems := decodeSupersedes(t, `{
	  "name": "p", "supersedes": [{"capability": "a-job"}]
	}`)
	if len(problems) == 0 {
		t.Fatal("a supersession with no `because` was accepted")
	}
	joined := strings.Join(problems, "\n")
	for _, want := range []string{`"because" is required`, "ANOTHER component's job"} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems %q missing %q", joined, want)
		}
	}
	// The reason is printed where the consequence lands, and the message must say so
	// — that is what makes `because` load-bearing rather than ceremony.
	if !strings.Contains(joined, "loopholes list") {
		t.Errorf("problems %q do not say where `because` is printed", joined)
	}
}

// TestSupersedesStructuralRefusals: every rule, each version-invariant (a typo both
// ends of a version boundary agree about), which is why they are safe on the
// TOLERANT path too — see TestSupersedesValidatedTolerantly.
func TestSupersedesStructuralRefusals(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"empty capability",
			`{"name":"p","supersedes":[{"capability":"","because":"b"}]}`, "non-empty"},
		{"whitespace in capability",
			`{"name":"p","supersedes":[{"capability":"a job","because":"b"}]}`, "rendezvous key"},
		{"control char in capability",
			`{"name":"p","supersedes":[{"capability":"a\njob","because":"b"}]}`, "control character"},
		{"control char in because",
			`{"name":"p","supersedes":[{"capability":"a-job","because":"x\n[red]forged"}]}`,
			"forge a claim line"},
		{"duplicate capability",
			`{"name":"p","supersedes":[{"capability":"a-job","because":"one"},` +
				`{"capability":"a-job","because":"two"}]}`, "already claimed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, problems := decodeSupersedes(t, tc.body)
			if len(problems) == 0 {
				t.Fatalf("accepted %s", tc.body)
			}
			if !strings.Contains(strings.Join(problems, "\n"), tc.want) {
				t.Errorf("problems %v, want one containing %q", problems, tc.want)
			}
		})
	}
}

// TestSupersedesValidatedTolerantly: the tolerant decoder — the one the in-jail
// entrypoint uses — must catch these too.
//
// Safe, and the distinction from validateSkillsTier's warning matters: that check is
// skew-SENSITIVE because it compares against an enum a newer build could extend.
// Nothing here compares against a list that can grow (design §6 rules out a
// yolo-owned registry of capability names on purpose), so a missing `because` is a
// mistake both ends of a version boundary agree about and cannot become the `tier`
// incident a fourth time.
func TestSupersedesValidatedTolerantly(t *testing.T) {
	_, problems, skipped := DecodeTolerant([]byte(
		`{"name":"p","supersedes":[{"capability":"a-job"}]}`))
	if len(problems) == 0 {
		t.Error("the tolerant decoder accepted a supersession with no `because`")
	}
	if len(skipped) != 0 {
		t.Errorf("skipped = %v, want none — `supersedes` is not an unknown kind", skipped)
	}
}

// TestSupersedesIsNotAContributionKind pins the OQ-CAP decision structurally: an
// author who writes it as a contribution hears that the kind does not exist, with the
// real list. If `supersedes` were ever added to the kind registry this fails, which is
// the forcing function — kinds.go's Footprint requires a Combine rule for "two claims
// on one target", and supersession has neither a target nor a conflict (§5).
func TestSupersedesIsNotAContributionKind(t *testing.T) {
	if KnownKind(Kind("supersedes")) {
		t.Fatal("\"supersedes\" is now a contribution kind — it delivers nothing and owns " +
			"no target, so it has no combine rule to state; see packdecl/supersedes.go")
	}
	// `because` is a real Contribution field (state's justification), so the entry gets
	// past the struct decoder and is refused for the reason under test — the KIND —
	// rather than for an unknown field.
	_, problems := decodeSupersedes(t,
		`{"name":"p","contributes":[{"kind":"supersedes","because":"a-job"}]}`)
	if len(problems) == 0 {
		t.Fatal("a contributes entry with kind \"supersedes\" was accepted")
	}
	if !strings.Contains(strings.Join(problems, "\n"), "unknown kind") {
		t.Errorf("problems = %v, want an unknown-kind refusal naming the real set", problems)
	}
}

// TestNoSupersedesIsSilent: a manifest without the key produces no problems and no
// claims — the same "silence is not participation" rule `serves` follows, on the
// other side of the rendezvous.
func TestNoSupersedesIsSilent(t *testing.T) {
	m, problems := decodeSupersedes(t, `{"name": "p", "description": "d"}`)
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	if len(m.Supersessions()) != 0 {
		t.Errorf("Supersessions() = %v, want none", m.Supersessions())
	}
}
