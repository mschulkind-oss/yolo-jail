package cli

// applyhostaudienceorphan_test.go pins WHICH orphan message a pack gets, end to end through
// applyHost — the call site of reportInferredDestinations.
//
// There are two ways a pack's content can reach nothing, and until the audience selector landed
// (docs/design/briefing-audiences.md, shipped 2026-09-02) there was one:
//
//   - NO DESTINATION EXISTS for the kind, anywhere in `packs`. Remedy: select an agent pack, or
//     declare an `into`.
//   - A DESTINATION EXISTS and none of them declares an `agent` matching the contribution's
//     `agents` selector. Remedy: select the pack that OWNS that name, or correct the selector.
//     `into` is not on the list — packdecl refuses it beside `agents`.
//
// The defect these gate: both messages named the first cause, so a pack whose audience was
// unmatched was told to declare a path it is not allowed to declare.
//
// END TO END rather than against the reporter, so that deleting the reportInferredDestinations
// call in applyHost fails them (AGENTS.md: a test that pins the callee while the call site is
// unpinned is not a test).
//
// Every test uses a t.TempDir() home with XDG_CONFIG_HOME inside it. The real $HOME is never
// read or written.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The two messages' distinctive fragments, so each test can assert its own fires and the other
// does not. Naming them once keeps "neither fires for the other's cause" from drifting into two
// half-checked lists.
var (
	unmatchedAudienceMarkers = []string{
		"`agents` selector",
		"declares a matching `agent`",
		"declaring `into` is not the remedy",
	}
	noDestinationMarkers = []string{
		"no pack in `packs` names a",
		"declare `into` in",
	}
)

func reportHasNone(t *testing.T, report string, markers []string, why string) {
	t.Helper()
	for _, m := range markers {
		if strings.Contains(report, m) {
			t.Errorf("the report says %q, which is the OTHER cause's message — %s:\n%s",
				m, why, report)
		}
	}
}

func reportHasAll(t *testing.T, report string, markers []string) {
	t.Helper()
	for _, m := range markers {
		if !strings.Contains(report, m) {
			t.Errorf("the report never says %q:\n%s", m, report)
		}
	}
}

// AN UNMATCHED AUDIENCE NAMES THE AUDIENCE AND THE OWNER, not `into`.
//
// THE NAME MUST BE ONE THE PACK SET PROVIDES, and this test used to get that wrong: it
// addressed `codex` with only `claude` selected, which step 6's resolution pass now REFUSES
// outright (AgentAudienceProblems — a name outside the vocabulary is the addressing pack's
// typo, and fatal). That is `TestApplyHostRefusesAnAudienceNoPackProvides`' case, not this
// one. The *reported* case needs a name that exists and simply routes nowhere for this KIND,
// so `beta` claims its name on its SKILLS destination and leaves its briefing unclaimed —
// then a briefing addressed to `beta` is a good name with no matching destination, and the
// remedy belongs to beta's author (R4), not to house's.
func TestApplyHostUnmatchedAudienceNamesTheAudienceNotInto(t *testing.T) {
	home := t.TempDir()
	base := t.TempDir()
	agentPack := filepath.Join(base, "beta")
	writeFile(t, filepath.Join(agentPack, "pack.json"),
		`{"name":"beta","description":"a","contributes":[`+
			`{"kind":"skills","into":".beta/skills","agent":"beta"},`+
			`{"kind":"briefing","into":".beta/AGENTS.md"}]}`)
	packDir := filepath.Join(base, "house")
	writeFile(t, filepath.Join(packDir, "pack.json"),
		`{"name":"house","description":"d","contributes":[`+
			`{"kind":"briefing","from":"prose/beta.md","agents":["beta"]}]}`)
	writeFile(t, filepath.Join(packDir, "prose", "beta.md"), "Beta house rules.\n")
	selectPacks(t, home,
		`{"source":"file://`+agentPack+`","name":"beta"},`+
			`{"source":"file://`+packDir+`","name":"house"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	// A GOOD NAME THAT ROUTED NOWHERE IS REPORTED, NOT REFUSED — the split step 6 settled, and
	// the half of roadmap 💬 20 that stays a warning: the fix is an `agent` on the OWNING pack's
	// briefing contribution, so refusing would punish the wrong author (R4).
	if rc != 0 {
		t.Fatalf("host apply --assert rc=%d — a name the set PROVIDES that reached no briefing "+
			"destination is reported, not refused\n%s", rc, report)
	}
	reportHasAll(t, report, unmatchedAudienceMarkers)
	if !strings.Contains(report, `house addresses "beta"`) {
		t.Errorf("the report does not name the pack and the audience it addressed:\n%s", report)
	}
	if !strings.Contains(report, "correct `agents` in house's pack.json") {
		t.Errorf("the report does not offer the remedy that exists for this cause:\n%s", report)
	}
	reportHasNone(t, report, noDestinationMarkers,
		"a briefing destination DOES exist, and `into` cannot be declared beside `agents`")

	// And the prose really did go nowhere: the destination that exists must not have received
	// content addressed to a name it does not answer to.
	brief, _ := os.ReadFile(filepath.Join(home, ".beta", "AGENTS.md"))
	if strings.Contains(string(brief), "Beta house rules.") {
		t.Errorf("prose addressed to `beta` reached beta's unclaimed briefing destination — the "+
			"report would be describing a delivery that happened:\n%s", brief)
	}
}

// A MISSING DESTINATION KEEPS THE MESSAGE IT WAS ALWAYS RIGHT ABOUT. No agent pack is selected,
// so nothing anywhere names a briefing destination and the pack named no audience — `into` and
// "select an agent pack" are exactly the two remedies.
func TestApplyHostNoDestinationKeepsTheIntoRemedy(t *testing.T) {
	home := t.TempDir()
	packDir := filepath.Join(t.TempDir(), "solo")
	writeFile(t, filepath.Join(packDir, "pack.json"),
		`{"name":"solo","description":"d","contributes":[`+
			`{"kind":"skills","from":"skills","into":".solo/skills"}]}`)
	writeFile(t, filepath.Join(packDir, "skills", "sskill", "SKILL.md"),
		"---\nname: sskill\ndescription: d\n---\nbody\n")
	writeFile(t, filepath.Join(packDir, "AGENTS.md"), "Solo prose.\n")
	selectPacks(t, home, `{"source":"file://`+packDir+`","name":"solo"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("a pack that delivered its declared skills must not fail over an unrouted "+
			"AGENTS.md: rc=%d\n%s", rc, report)
	}
	reportHasAll(t, report, noDestinationMarkers)
	reportHasNone(t, report, unmatchedAudienceMarkers,
		"this contribution named no audience, so there is no selector to correct")
}

// BOTH CAUSES IN ONE PACK GET BOTH MESSAGES, each attributed to its own kind — the case neither
// of step 6's two audience tests covers, since both of those carry a single cause.
//
// `beta` claims its name on its SKILLS destination and leaves its briefing unclaimed (see
// TestApplyHostUnmatchedAudienceNamesTheAudienceNotInto for why the name must be one the set
// provides). So `both`'s addressed briefing is a good name that routed nowhere, while its
// unaddressed `skills/` tree borrows beta's skills destination and is DELIVERED. One pack, one
// reported audience, one silent inference — and the report must not describe either cause for
// the other's kind.
func TestApplyHostReportsEachOrphanCauseForItsOwnKind(t *testing.T) {
	home := t.TempDir()
	base := t.TempDir()
	agentPack := filepath.Join(base, "beta")
	writeFile(t, filepath.Join(agentPack, "pack.json"),
		`{"name":"beta","description":"a","contributes":[`+
			`{"kind":"skills","into":".beta/skills","agent":"beta"},`+
			`{"kind":"briefing","into":".beta/AGENTS.md"}]}`)
	packDir := filepath.Join(base, "both")
	writeFile(t, filepath.Join(packDir, "pack.json"),
		`{"name":"both","description":"d","contributes":[`+
			`{"kind":"briefing","from":"prose/beta.md","agents":["beta"]}]}`)
	writeFile(t, filepath.Join(packDir, "prose", "beta.md"), "Beta only.\n")
	writeFile(t, filepath.Join(packDir, "skills", "bskill", "SKILL.md"),
		"---\nname: bskill\ndescription: d\n---\nbody\n")
	selectPacks(t, home,
		`{"source":"file://`+agentPack+`","name":"beta"},`+
			`{"source":"file://`+packDir+`","name":"both"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	rc, report := applyWith(t, true, strings.NewReader("y\n"))
	if rc != 0 {
		t.Fatalf("host apply --assert rc=%d\n%s", rc, report)
	}
	// The unaddressed skills tree borrowed claude's destination, so it is INFERRED, not
	// orphaned — which is what makes the briefing line's cause the only one in the report.
	if _, err := os.Stat(filepath.Join(home, ".beta", "skills", "bskill", "SKILL.md")); err != nil {
		t.Fatalf("the unaddressed skills tree did not reach beta's destination: %v\n%s",
			err, report)
	}
	reportHasAll(t, report, unmatchedAudienceMarkers)
	if !strings.Contains(report, "briefing") {
		t.Errorf("the unmatched audience is not attributed to the briefing kind:\n%s", report)
	}
	reportHasNone(t, report, noDestinationMarkers,
		"nothing in this set is orphaned for want of a destination")
}
